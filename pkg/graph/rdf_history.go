package graph

// The history of RDF triples: what the knowledge graph held, and when.
//
// kg_triples had one timestamp, created_at, and a delete — rdf.go, a SPARQL
// Update, inference maintenance — removed the row. So SPARQL,
// knowledge_graph_find and the export could answer about now and nothing else,
// and "which facts did we drop on Friday, and who dropped them" was a question
// only the change feed could half-answer, until retention pruned it.
//
// # Shape
//
// The same decision temporal.go made for nodes and edges, for the same reason:
// the live table, kg_triples, holds exactly the triples that are in effect, and
// a triple that stops being so moves to kg_triples_history in the transaction
// that deletes it. Every query in the module that reads kg_triples keeps its
// meaning with no edit; only an as-of read pays for the union of the two.
//
// There is one time axis, not two. A property-graph edge carries a valid
// interval because extraction can say "true from 2024"; an RDF triple states no
// validity of its own, so what the store can honestly record is when it came to
// hold the triple (recorded_at on the live row) and when it stopped
// (retracted_at on the history row). Both are written from Go, UTC, to the
// microsecond, from the store's monotonic clock — temporal.go says why — and a
// NULL recorded_at, which is every row written before this release, reads as
// "since before the beginning". The intervals are half-open, [recorded_at,
// retracted_at), so a triple deleted at T is visible at T-1us and not at T.
//
// # Asserted and inferred triples
//
// An asserted triple is a fact somebody wrote; losing it loses information, so
// every delete of one — rdf.go's DeleteTriple(s), each SPARQL Update form, the
// graph-management operations — records it in history with a reason and the
// producer that deleted it, in the same transaction as the delete.
//
// An inferred triple is derivable, and the rule here is: it is recorded too,
// with inference_rule and support_ids as they were. The alternative — leave it
// out and re-derive at read time — was rejected because a past read must
// reproduce what the store *said* then, not what today's engine would say about
// then's asserted set: the ruleset, the OWL options and any SHACL rules in force
// are not recorded anywhere that could be replayed, and re-deriving per query is
// a full inference run. Recording costs history rows only for triples that
// really stop being inferred.
//
// What must not happen is history filling on every inference refresh, which
// clears the inferred set and re-derives it. The refresh paths therefore
// archive what they clear under a transient reason (reasonInferenceRefreshing)
// and, once the new set is written, reconcileInferenceRefresh puts each triple
// that came back to its original recorded_at and deletes the transient row —
// membership is continuous, so the history shows nothing. Only triples that did
// not come back keep a history row, renamed ReasonUnderived. A refresh over an
// unchanged ontology therefore writes no net history at all.
//
// History records membership. A triple that stays inferred while its
// inference_rule or support_ids change is rewritten in place and keeps its
// recorded_at; an as-of read returns it with the explanation it has now. That is
// a deliberate limit: the set of triples that held at T is exact, the
// explanation of why is the latest one.
//
// # RDF 1.2 triple terms
//
// An as-of read matches a triple-term object given as a constant exactly. A
// partly bound one, <<( ?s :p ?o )>>, is resolved through kg_triple_terms, which
// holds only the terms some current triple still has as its object (deleting the
// last use removes the row, and that is tested behaviour), so an as-of read of
// such a pattern misses a triple term that no current triple uses.
//
// # Purge
//
// Purge (temporal.go) also removes triple history that closed before its
// cutoff, and reports the count in PurgeReport.Triples.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reasons and producers specific to triples. The reasons of temporal_reason.go
// apply as well: a delete is ReasonRetracted.
const (
	// ReasonUnderived: an inferred triple the inference engine no longer
	// derives. Its supports were retracted, or a rule or the ontology changed.
	ReasonUnderived = "underived"

	// reasonInferenceRefreshing marks a row an inference refresh cleared and
	// has not yet reconciled. It exists only between the clear and the
	// reconcile, and never survives either: every such row is deleted (the
	// triple was re-derived) or renamed ReasonUnderived.
	reasonInferenceRefreshing = "inference_refresh"
)

const (
	ProducerRDFDelete          = "rdf.delete"
	ProducerSPARQLUpdate       = "sparql.update"
	ProducerInferenceMaintain  = "inference.maintain"
	ProducerInferenceRefresh   = "inference.refresh"
	ProducerSHACLRules         = "shacl.rules"
	tripleHistoryDefaultLimit  = 100
	tripleHistoryMaxLimit      = 1000
	tripleHistoryStatusCurrent = "current"
	tripleHistoryStatusEnded   = "ended"
)

// tripleBaseColumns is what a triple read selects; tripleColumns adds the
// recording instant, and is the list live table and history share so a row can
// be copied between them with one INSERT ... SELECT.
const tripleBaseColumns = `id, graph_kind, graph_value, subject_kind, subject_value, predicate_value, ` +
	`object_kind, object_value, object_datatype, object_language, inferred, inference_rule, support_ids`

const tripleColumns = tripleBaseColumns + `, created_at, recorded_at`

const tripleHistoryInvalidationColumns = `retracted_at, reason, superseded_by, producer`

// createTripleHistorySchema adds recorded_at to kg_triples and creates the
// history table. Callers hold schemaMu; createGraphSchema calls it last.
//
// recorded_at is added with no DEFAULT, so a row written before this release
// has NULL, which reads as unbounded: an as-of read at any instant still finds
// it. No primary key on the history table: a triple deleted, re-asserted and
// deleted again has several history rows with one id.
func (g *GraphStore) createTripleHistorySchema(ctx context.Context) error {
	if _, err := g.exec(ctx, `ALTER TABLE kg_triples ADD COLUMN recorded_at TIMESTAMP`); err != nil && !g.dialect.IsDuplicateColumn(err) {
		return fmt.Errorf("cortexdb/graph: triple history migration: %w", err)
	}
	schema := `
	CREATE TABLE IF NOT EXISTS kg_triples_history (
		id TEXT NOT NULL,
		graph_kind TEXT,
		graph_value TEXT,
		subject_kind TEXT NOT NULL,
		subject_value TEXT NOT NULL,
		predicate_value TEXT NOT NULL,
		object_kind TEXT NOT NULL,
		object_value TEXT NOT NULL,
		object_datatype TEXT,
		object_language TEXT,
		inferred INTEGER NOT NULL DEFAULT 0,
		inference_rule TEXT,
		support_ids TEXT,
		created_at TIMESTAMP,
		recorded_at TIMESTAMP,
		retracted_at TIMESTAMP,
		reason TEXT,
		superseded_by TEXT,
		producer TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_kg_triples_history_id ON kg_triples_history(id);
	CREATE INDEX IF NOT EXISTS idx_kg_triples_history_subject ON kg_triples_history(subject_kind, subject_value);
	CREATE INDEX IF NOT EXISTS idx_kg_triples_history_retracted ON kg_triples_history(retracted_at);
	CREATE INDEX IF NOT EXISTS idx_kg_triples_history_object ON kg_triples_history(object_kind, object_value);
	`
	if _, err := g.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("cortexdb/graph: triple history table: %w", err)
	}
	return nil
}

// TripleSource names the triples a read should see: a table name for a FROM
// clause and the arguments it binds ahead of the query's own, like NodeSource.
// With no as-of on the context it is the bare table, so a current read is the
// query it always was.
func (g *GraphStore) TripleSource(ctx context.Context) (string, []any) {
	ro := ReadOptionsFrom(ctx)
	if !ro.IsAsOf() {
		return "kg_triples", nil
	}
	t := ro.AsOf
	src := "(SELECT " + tripleBaseColumns + " FROM kg_triples WHERE (recorded_at IS NULL OR recorded_at <= ?)" +
		" UNION ALL SELECT " + tripleBaseColumns + " FROM kg_triples_history" +
		" WHERE (recorded_at IS NULL OR recorded_at <= ?) AND (retracted_at IS NULL OR retracted_at > ?))"
	return src, []any{t, t, t}
}

// tripleDeletion is the invalidation recorded for a deleted triple.
func tripleDeletion(ctx context.Context, producer string) Invalidation {
	return InvalidationFrom(ctx).withDefaults(ReasonRetracted, "", producer)
}

// archiveTriplesTx copies the live rows matching where into history, closed at
// `at` with inv. Call it immediately before the DELETE of the same rows, in the
// same transaction; a row that matches nothing writes nothing.
func (g *GraphStore) archiveTriplesTx(ctx context.Context, tx *sql.Tx, where string, args []any, at time.Time, inv Invalidation) error {
	q := `INSERT INTO kg_triples_history (` + tripleColumns + `, ` + tripleHistoryInvalidationColumns + `)
		SELECT ` + tripleColumns + `, ?, ?, ?, ? FROM kg_triples WHERE ` + where
	head := []any{stamp(at), inv.Reason, nullIfEmpty(inv.SupersededBy), inv.Producer}
	if _, err := g.txExec(ctx, tx, q, append(head, args...)...); err != nil {
		return fmt.Errorf("cortexdb/graph: archive triples: %w", err)
	}
	return nil
}

// inferredIDWhere is the WHERE clause naming a chunk of inferred ids.
func inferredIDWhere(ids []string, excludeSHACL bool) (string, []any) {
	holes, args := placeholderList(ids)
	where := `id IN (` + holes + `) AND (inferred + 0) = 1`
	if excludeSHACL {
		where += ` AND ` + notSHACLRule
	}
	return where, args
}

// reconcileInferenceRefresh closes an inference refresh: a triple the refresh
// cleared and then wrote again takes back the recorded_at it had and its
// transient history row is dropped; every row left over is a triple that was
// not re-derived, and becomes an ordinary ReasonUnderived row.
func (g *GraphStore) reconcileInferenceRefresh(ctx context.Context) error {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cortexdb/graph: reconcile inference refresh: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const match = `FROM kg_triples_history h WHERE h.id = kg_triples.id AND h.reason = ? AND h.inferred = 1`
	if _, err := g.txExec(ctx, tx, `UPDATE kg_triples SET recorded_at = (SELECT h.recorded_at `+match+
		` ORDER BY h.retracted_at DESC LIMIT 1) WHERE inferred = 1 AND EXISTS (SELECT 1 `+match+`)`,
		reasonInferenceRefreshing, reasonInferenceRefreshing); err != nil {
		return fmt.Errorf("cortexdb/graph: reconcile inference refresh: %w", err)
	}
	if _, err := g.txExec(ctx, tx, `DELETE FROM kg_triples_history WHERE reason = ? AND EXISTS (
		SELECT 1 FROM kg_triples l WHERE l.id = kg_triples_history.id AND l.inferred = 1)`,
		reasonInferenceRefreshing); err != nil {
		return fmt.Errorf("cortexdb/graph: reconcile inference refresh: %w", err)
	}
	if _, err := g.txExec(ctx, tx, `UPDATE kg_triples_history SET reason = ? WHERE reason = ?`,
		ReasonUnderived, reasonInferenceRefreshing); err != nil {
		return fmt.Errorf("cortexdb/graph: reconcile inference refresh: %w", err)
	}
	return tx.Commit()
}

// --- reading the history -----------------------------------------------------

// TripleHistoryQuery selects the history of the triples matching a pattern.
type TripleHistoryQuery struct {
	// Subject is required: a history is read per subject, and an unbounded
	// one would page through every change the store ever recorded.
	Subject   *RDFTerm
	Predicate *RDFTerm
	Object    *RDFTerm
	// Inferred narrows to inferred (true) or asserted (false) triples; nil
	// reads both.
	Inferred *bool
	// From and To bound the window, [From, To): a version is listed when the
	// interval it held overlaps it. A zero bound is open.
	From, To time.Time
	// Limit is the page size (default 100, at most 1000). Cursor is the
	// NextCursor of the previous page.
	Limit  int
	Cursor string
}

// TripleHistoryEntry is one version of a triple: the interval the store held
// it for, and why it stopped.
type TripleHistoryEntry struct {
	Triple RDFTriple `json:"triple"`
	// Status is "current" for a triple in effect now, "ended" otherwise.
	Status      string    `json:"status"`
	RecordedAt  time.Time `json:"recorded_at,omitzero"`
	RetractedAt time.Time `json:"retracted_at,omitzero"`
	Invalidation
}

// TripleHistoryPage is one page of a triple history, oldest first.
type TripleHistoryPage struct {
	Entries    []TripleHistoryEntry `json:"entries"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// TripleHistory lists every version of the triples matching q — closed ones
// from history and the one in effect now — oldest first, with why each closed.
// Paged: it never loads more than a page plus its offset.
func (g *GraphStore) TripleHistory(ctx context.Context, q TripleHistoryQuery) (*TripleHistoryPage, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	if q.Subject == nil {
		return nil, fmt.Errorf("cortexdb/graph: triple history: a subject is required")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = tripleHistoryDefaultLimit
	}
	if limit > tripleHistoryMaxLimit {
		limit = tripleHistoryMaxLimit
	}
	offset := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(q.Cursor, "o"))
		if err != nil || n < 0 || !strings.HasPrefix(q.Cursor, "o") {
			return nil, fmt.Errorf("cortexdb/graph: triple history: malformed cursor %q", q.Cursor)
		}
		offset = n
	}

	var conds []string
	var args []any
	subject, err := g.normalizeTerm(ctx, *q.Subject, rdfPositionSubject)
	if err != nil {
		return nil, err
	}
	conds = append(conds, "subject_kind = ?", "subject_value = ?")
	args = append(args, subject.Kind, subject.Value)
	if q.Predicate != nil {
		predicate, err := g.normalizeTerm(ctx, *q.Predicate, rdfPositionPredicate)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "predicate_value = ?")
		args = append(args, predicate.Value)
	}
	if q.Object != nil {
		object, err := g.normalizeTerm(ctx, *q.Object, rdfPositionObject)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "object_kind = ?", "object_value = ?")
		args = append(args, object.Kind, object.Value)
	}
	if q.Inferred != nil {
		conds = append(conds, "inferred = ?")
		args = append(args, boolToInt(*q.Inferred))
	}
	where := strings.Join(conds, " AND ")

	// A version overlaps [From, To) when it began before To and ended after
	// From. The live table's rows have not ended.
	liveWhere, liveArgs := where, append([]any(nil), args...)
	histWhere, histArgs := where, append([]any(nil), args...)
	if !q.To.IsZero() {
		to := stamp(q.To)
		liveWhere += " AND (recorded_at IS NULL OR recorded_at < ?)"
		liveArgs = append(liveArgs, to)
		histWhere += " AND (recorded_at IS NULL OR recorded_at < ?)"
		histArgs = append(histArgs, to)
	}
	if !q.From.IsZero() {
		histWhere += " AND retracted_at > ?"
		histArgs = append(histArgs, stamp(q.From))
	}

	// Each side is fetched up to offset+limit+1 rows in the order the merge
	// uses, which is enough to produce the page and to know whether another
	// follows.
	need := offset + limit + 1
	order := ` ORDER BY CASE WHEN recorded_at IS NULL THEN 0 ELSE 1 END, recorded_at, id`
	var entries []TripleHistoryEntry

	rows, err := g.query(ctx, `SELECT `+tripleBaseColumns+`, recorded_at FROM kg_triples WHERE `+liveWhere+order+
		fmt.Sprintf(` LIMIT %d`, need), liveArgs...)
	if err != nil {
		return nil, fmt.Errorf("cortexdb/graph: triple history: %w", err)
	}
	for rows.Next() {
		var recorded sql.NullTime
		e, err := scanTripleHistoryRow(rows, &recorded, nil, nil)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		e.Status = tripleHistoryStatusCurrent
		e.RecordedAt = nullTime(recorded)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	rows, err = g.query(ctx, `SELECT `+tripleBaseColumns+`, recorded_at, retracted_at,
		COALESCE(reason, ''), COALESCE(superseded_by, ''), COALESCE(producer, '')
		FROM kg_triples_history WHERE `+histWhere+order+`, retracted_at`+fmt.Sprintf(` LIMIT %d`, need), histArgs...)
	if err != nil {
		return nil, fmt.Errorf("cortexdb/graph: triple history: %w", err)
	}
	for rows.Next() {
		var recorded, retracted sql.NullTime
		var inv Invalidation
		e, err := scanTripleHistoryRow(rows, &recorded, &retracted, &inv)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		e.Status = tripleHistoryStatusEnded
		e.RecordedAt, e.RetractedAt = nullTime(recorded), nullTime(retracted)
		e.Invalidation = inv
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	// Oldest first: by when the store came to hold the version, then by the
	// triple, then closed versions before the one still open.
	sortTripleHistory(entries)
	page := &TripleHistoryPage{Entries: []TripleHistoryEntry{}}
	if offset < len(entries) {
		end := min(offset+limit, len(entries))
		page.Entries = entries[offset:end]
		if len(entries) > end {
			page.NextCursor = "o" + strconv.Itoa(end)
		}
	}
	return page, nil
}

func sortTripleHistory(entries []TripleHistoryEntry) {
	less := func(a, b TripleHistoryEntry) bool {
		if !a.RecordedAt.Equal(b.RecordedAt) {
			return a.RecordedAt.Before(b.RecordedAt)
		}
		if a.Triple.ID != b.Triple.ID {
			return a.Triple.ID < b.Triple.ID
		}
		// Closed before open; among closed, by when they closed.
		if a.RetractedAt.IsZero() != b.RetractedAt.IsZero() {
			return !a.RetractedAt.IsZero()
		}
		return a.RetractedAt.Before(b.RetractedAt)
	}
	sort.SliceStable(entries, func(i, j int) bool { return less(entries[i], entries[j]) })
}

// scanWith lets scanTriple read the thirteen triple columns while the same
// Scan call fills the columns after them.
type scanWith struct {
	rows  *sql.Rows
	extra []any
}

func (s scanWith) Scan(dest ...any) error { return s.rows.Scan(append(dest, s.extra...)...) }

func scanTripleHistoryRow(rows *sql.Rows, recorded, retracted *sql.NullTime, inv *Invalidation) (TripleHistoryEntry, error) {
	extra := []any{recorded}
	if retracted != nil {
		extra = append(extra, retracted, &inv.Reason, &inv.SupersededBy, &inv.Producer)
	}
	triple, err := scanTriple(scanWith{rows: rows, extra: extra})
	if err != nil {
		return TripleHistoryEntry{}, fmt.Errorf("cortexdb/graph: triple history: %w", err)
	}
	return TripleHistoryEntry{Triple: triple}, nil
}

// --- purge -------------------------------------------------------------------

// purgeTripleHistory removes (or, for a dry run, counts) triple history that
// closed before the cutoff.
func (g *GraphStore) purgeTripleHistory(ctx context.Context, cutoff time.Time, dryRun bool) (int, error) {
	const closedBefore = `retracted_at IS NOT NULL AND retracted_at < ?`
	if dryRun {
		var n int
		if err := g.queryRow(ctx, `SELECT COUNT(*) FROM kg_triples_history WHERE `+closedBefore, cutoff).Scan(&n); err != nil {
			return 0, fmt.Errorf("cortexdb/graph: purge: count kg_triples_history: %w", err)
		}
		return n, nil
	}
	res, err := g.exec(ctx, `DELETE FROM kg_triples_history WHERE `+closedBefore, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cortexdb/graph: purge: kg_triples_history: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
