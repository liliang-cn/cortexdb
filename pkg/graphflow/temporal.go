package graphflow

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// Temporal / bitemporal facts on top of the existing property graph. A relation
// edge becomes a *fact with a validity interval*: valid_from..valid_to (nil
// valid_to = still true). The wall-clock moment the fact was recorded is kept
// separately as recorded_at, so the graph is bitemporal — you can ask both "what
// did we believe was true as of date X" (valid time, via QueryFactsAsOf) and,
// via recorded_at, when we learned it (transaction time).
//
// Storage reuses the ordinary relation edge: validity is written into the edge's
// JSON `properties` (valid_from / valid_to / recorded_at as RFC3339 strings) via
// the relation Metadata that UpsertRelations lands there. Nothing about the
// graph schema changes — a temporal fact is just an edge carrying valid_from.
//
// Supersession ("the new value replaces the old") is modeled by closing the
// prior open fact(s) for a subject+predicate at the moment the new one starts,
// so a subject's history is a chain of non-overlapping intervals.

// Metadata keys under which validity is stored on a relation edge's properties.
const (
	factValidFromKey  = "valid_from"
	factValidToKey    = "valid_to"
	factRecordedAtKey = "recorded_at"
)

// TemporalFact is a relation (From -Type-> To) that holds over a validity
// interval [ValidFrom, ValidTo). A nil ValidTo means the fact is open-ended —
// still valid now. RecordedAt (transaction time) is set by SaveTemporalFact and
// populated on read by QueryFactsAsOf.
type TemporalFact struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`

	ValidFrom *time.Time `json:"valid_from,omitempty"`
	ValidTo   *time.Time `json:"valid_to,omitempty"`

	// Supersede, when set on SaveTemporalFact, closes any currently-open fact
	// for the same (From, Type) subject at ValidFrom before recording this one
	// — the "new value replaces old" pattern (e.g. a changed job title).
	Supersede bool `json:"supersede,omitempty"`

	// RecordedAt is the wall-clock time the fact was written (transaction time).
	// Set by SaveTemporalFact; read back by QueryFactsAsOf.
	RecordedAt *time.Time `json:"recorded_at,omitempty"`

	// SupersededBy is the edge id of the fact that later replaced this one,
	// when SaveTemporalFact superseded it. Read back by QueryFactsAsOf, so a
	// question about the past can see what the answer changed to.
	SupersededBy string `json:"superseded_by,omitempty"`
}

// TemporalFilter optionally scopes QueryFactsAsOf to a subject and/or predicate.
// A zero filter returns every temporal fact valid at the queried instant.
type TemporalFilter struct {
	From string `json:"from,omitempty"` // subject display name or entity id
	Type string `json:"type,omitempty"` // predicate / edge type
}

// SaveTemporalFact records a fact with validity time. valid_from / valid_to /
// recorded_at are written (RFC3339) into the relation edge's JSON properties via
// UpsertRelations. When fact.ValidFrom is nil it defaults to now. When
// fact.Supersede is set, any currently-open fact for the same (From, Type)
// subject is closed at ValidFrom first, so the subject's history stays a chain
// of non-overlapping intervals.
func SaveTemporalFact(ctx context.Context, db *cortexdb.DB, fact TemporalFact) error {
	if db == nil {
		return fmt.Errorf("graphflow: temporal: nil db")
	}
	if strings.TrimSpace(fact.From) == "" || strings.TrimSpace(fact.To) == "" {
		return fmt.Errorf("graphflow: temporal fact requires From and To")
	}
	typ := firstNonEmptyTemporal(fact.Type, "related_to")

	recordedAt := time.Now().UTC()
	validFrom := recordedAt
	if fact.ValidFrom != nil {
		validFrom = fact.ValidFrom.UTC()
	}

	// Supersession: close prior open facts for this subject+predicate so the new
	// value takes over exactly where the old one ends.
	//
	// Forced by the caller, or decided by the ontology. Leaving it entirely to
	// the caller was the gap: a caller who forgets leaves two open facts
	// claiming to be current about the same thing, and nothing says so — the
	// graph simply answers a question about today with two contradictory
	// values. Asking the schema whether the link reaches one object closes
	// that hole for every link the schema describes, without ever guessing
	// about the ones it does not.
	// Re-stating an open fact must not move when it became true.
	//
	// An edge is identified by (from, type, to), so saying again that Leo lives
	// in Chengdu overwrites the same edge's properties — and used to carry the
	// new valid_from with it. The fact then claimed to have started on the day
	// it was last mentioned, and every question about the period before that
	// answered "nothing was true", which is how a graph forgets by being told
	// something it already knew.
	if earlier := openFactStart(ctx, db, fact, typ, validFrom); earlier != nil && earlier.Before(validFrom) {
		validFrom = *earlier
	}

	supersede := fact.Supersede
	if !supersede {
		supersede = shouldAutoSupersede(ctx, db, fact, typ, validFrom)
	}
	if supersede {
		// The new fact's edge id, as UpsertRelations below will write it, so
		// the closed fact's history names what replaced it.
		newID := fmt.Sprintf("edge:relation:%s:%s:%s",
			cortexdb.EntityNodeID(fact.From), cortexdb.EntityNodeID(fact.To), typ)
		if _, err := SupersedeFactWith(ctx, db, fact.From, typ, validFrom, SupersedeOptions{SupersededBy: newID}); err != nil {
			return err
		}
	}

	metadata := map[string]string{
		factValidFromKey:  validFrom.Format(time.RFC3339),
		factRecordedAtKey: recordedAt.Format(time.RFC3339),
	}
	if fact.ValidTo != nil {
		metadata[factValidToKey] = fact.ValidTo.UTC().Format(time.RFC3339)
	}

	// Ensure the endpoint entity nodes exist first: relation edges carry a
	// foreign key to graph_nodes, so an edge to a non-existent node is silently
	// dropped. This also seeds the display names QueryFactsAsOf reads back.
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{
		Entities: []cortexdb.ToolEntityInput{{Name: fact.From}, {Name: fact.To}},
	}); err != nil {
		return fmt.Errorf("graphflow: save temporal fact: ensure entities: %w", err)
	}

	if _, err := db.GraphRAGTools().UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{
		Relations: []cortexdb.ToolRelationInput{{
			From:     fact.From,
			To:       fact.To,
			Type:     typ,
			Metadata: metadata,
		}},
	}); err != nil {
		return fmt.Errorf("graphflow: save temporal fact: %w", err)
	}
	return nil
}

// SupersedeFact closes every currently-open fact matching (from, typ) by setting
// their valid_to to asOf, and returns how many were closed. "Open" means the
// edge carries a valid_from but no valid_to. This is the mechanism behind
// SaveTemporalFact's Supersede option and can also be called directly to retire
// a subject's current value without asserting a replacement.
// shouldAutoSupersede decides whether this fact contradicts what is already
// open, using the ontology rather than a guess.
//
// Three things all have to hold, and each of the other two is a way to get
// this wrong:
//
//   - the ontology says the link reaches at most one object. Without this,
//     a second "knows" would close the first.
//   - something is actually open for this subject and predicate at the new
//     fact's start. Superseding nothing is harmless but pointless.
//   - the open fact points somewhere else. Re-stating that Leo still lives in
//     Chengdu is not a contradiction, and closing and reopening the interval
//     would shred one continuous fact into a chain of fragments — which then
//     reads as "moved house every time we mentioned it".
//
// openFactStart returns when an identical fact already open at `at` began, or
// nil when there is none. Identical means the same subject, predicate and
// object: a different object is a contradiction, not a restatement.
func openFactStart(ctx context.Context, db *cortexdb.DB, fact TemporalFact, typ string, at time.Time) *time.Time {
	open, err := QueryFactsAsOf(ctx, db, at, TemporalFilter{From: fact.From, Type: typ})
	if err != nil {
		return nil
	}
	for _, existing := range open {
		if existing.To == fact.To && existing.ValidFrom != nil {
			return existing.ValidFrom
		}
	}
	return nil
}

func shouldAutoSupersede(ctx context.Context, db *cortexdb.DB, fact TemporalFact, typ string, validFrom time.Time) bool {
	single, known := db.LinkSingleValued(ctx, typ)
	if !known || !single {
		return false
	}
	open, err := QueryFactsAsOf(ctx, db, validFrom, TemporalFilter{From: fact.From, Type: typ})
	if err != nil || len(open) == 0 {
		return false
	}
	for _, existing := range open {
		if existing.To != fact.To {
			return true
		}
	}
	return false
}

func SupersedeFact(ctx context.Context, db *cortexdb.DB, from, typ string, asOf time.Time) (int, error) {
	return SupersedeFactWith(ctx, db, from, typ, asOf, SupersedeOptions{})
}

// SupersedeOptions says what replaced the facts SupersedeFactWith closes.
type SupersedeOptions struct {
	// SupersededBy is the id of the fact that replaces the closed ones, when
	// there is one. SaveTemporalFact fills it with the new fact's edge id.
	SupersededBy string
	// Producer names the caller; empty records ProducerTemporal.
	Producer string
}

// ProducerTemporal is recorded on history rows closed by supersession.
const ProducerTemporal = "graphflow.temporal"

// factSupersededByKey is written into a closed fact's properties beside
// valid_to, so a read of the fact as of a time it was still valid can say
// what later replaced it.
const factSupersededByKey = "superseded_by"

// SupersedeFactWith is SupersedeFact that records what replaced the closed
// facts.
//
// Each closed fact is rewritten through the graph store, not by an UPDATE in
// place: the version that was open moves to history with reason
// "superseded", superseded_by and producer set, so "why did this stop being
// true, and what replaced it" is answerable from the history — which an
// in-place UPDATE, the previous implementation, left with no row at all.
func SupersedeFactWith(ctx context.Context, db *cortexdb.DB, from, typ string, asOf time.Time, opts SupersedeOptions) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("graphflow: temporal: nil db")
	}
	fromID := cortexdb.EntityNodeID(from)
	if fromID == "" {
		return 0, fmt.Errorf("graphflow: supersede: empty subject")
	}
	typ = firstNonEmptyTemporal(typ, "related_to")
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return 0, fmt.Errorf("graphflow: supersede: %w", err)
	}

	d := db.Dialect()
	validFromCol := d.JSONTextGuarded("properties", factValidFromKey)
	validToCol := d.JSONTextGuarded("properties", factValidToKey)
	rows, err := db.SQL().QueryContext(ctx, d.Rebind(`SELECT id FROM graph_edges
		WHERE from_node_id = ? AND edge_type = ?
		  AND `+validFromCol+` IS NOT NULL
		  AND `+validToCol+` IS NULL`), fromID, typ)
	if err != nil {
		return 0, fmt.Errorf("graphflow: supersede: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("graphflow: supersede: %w", err)
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if len(ids) == 0 {
		return 0, nil
	}

	producer := strings.TrimSpace(opts.Producer)
	if producer == "" {
		producer = ProducerTemporal
	}
	wctx := graph.WithInvalidation(ctx, graph.Invalidation{
		Reason:       graph.ReasonSuperseded,
		SupersededBy: strings.TrimSpace(opts.SupersededBy),
		Producer:     producer,
	})
	edges, err := db.Graph().GetEdgesBatch(ctx, ids)
	if err != nil {
		return 0, fmt.Errorf("graphflow: supersede: %w", err)
	}
	closed := 0
	for _, e := range edges {
		if e == nil {
			continue
		}
		if e.Properties == nil {
			e.Properties = map[string]interface{}{}
		}
		e.Properties[factValidToKey] = asOf.UTC().Format(time.RFC3339)
		if opts.SupersededBy != "" {
			e.Properties[factSupersededByKey] = opts.SupersededBy
		}
		// The rewrite opens its version now; carrying the old graph-level
		// valid_from would close the previous version at the instant it opened.
		e.ValidFrom = time.Time{}
		if err := db.Graph().UpsertEdge(wctx, e); err != nil {
			return closed, fmt.Errorf("graphflow: supersede: %w", err)
		}
		closed++
	}
	return closed, nil
}

// QueryFactsAsOf returns the temporal facts whose validity interval contains the
// instant `at` — i.e. valid_from <= at AND (valid_to IS NULL OR at < valid_to)
// — optionally scoped by subject and/or predicate. Endpoint node ids are
// resolved to entity display names (falling back to the id suffix), matching the
// community.go loadEntityDisplayNames pattern.
// ensureTemporalIndex creates the expression index the as-of filter needs.
//
// Now that the instant is compared in SQL, an index on the extracted
// valid_from turns "what was true on this date" from a scan of every edge that
// carries a validity into a range lookup. Both databases index expressions, so
// this is one statement rather than two.
//
// Idempotent and non-fatal: an index that cannot be created costs speed, not
// correctness, and refusing to answer would be the worse trade.
func ensureTemporalIndex(ctx context.Context, db *cortexdb.DB) error {
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return fmt.Errorf("graphflow: temporal: init graph schema: %w", err)
	}
	// Guarded, and under a new name. The first version indexed the bare
	// json_extract, and SQLite evaluates an expression index on every insert:
	// once any as-of query had run, writing an edge whose properties were
	// empty — every chunk edge IngestDocument writes — failed with
	// "malformed JSON". The old index is dropped so a brain that already has
	// it stops failing those writes.
	_, _ = db.SQL().ExecContext(ctx, `DROP INDEX IF EXISTS idx_graph_edges_valid_from`)
	stmt := `CREATE INDEX IF NOT EXISTS idx_graph_edges_valid_from_guarded ON graph_edges(` +
		db.Dialect().JSONTextGuarded("properties", factValidFromKey) + `)`
	_, _ = db.SQL().ExecContext(ctx, stmt)
	return nil
}

// asOfQuery builds the "what was true at this instant" query for a dialect.
//
// Extracted so it can be checked against a real PostgreSQL instance without a
// PostgreSQL cortexdb.DB, which does not exist yet: the query is the part that
// has to be right on both, and a form that is only ever built and never run is
// a form nobody has tested.
func asOfQuery(d sqldialect.Dialect, at time.Time, filter TemporalFilter) (string, []any) {
	// Asked of the dialect rather than written out: json_extract is SQLite's
	// and PostgreSQL has no such function, so a hardcoded query is a feature
	// that cannot cross backends.
	validFromCol := d.JSONTextGuarded("properties", factValidFromKey)
	validToCol := d.JSONTextGuarded("properties", factValidToKey)
	recordedCol := d.JSONTextGuarded("properties", factRecordedAtKey)
	supersededCol := d.JSONTextGuarded("properties", factSupersededByKey)

	// The instant is compared in SQL, not in Go.
	//
	// This used to read every temporal fact for the subject and filter them in
	// the caller's loop, which is a scan of the whole history to answer a
	// question about one moment of it. String comparison is exact because
	// SaveTemporalFact writes .UTC().Format(time.RFC3339): every stored
	// instant is Zulu and fixed-width, so lexicographic order is chronological
	// order.
	//
	// The interval is half-open, [valid_from, valid_to): a fact starting
	// exactly at `at` is current, one ending exactly at `at` is not.
	atStr := at.UTC().Format(time.RFC3339)
	query := `SELECT from_node_id, to_node_id, COALESCE(edge_type, ''),
	                 ` + validFromCol + `,
	                 ` + validToCol + `,
	                 ` + recordedCol + `,
	                 ` + supersededCol + `
	          FROM graph_edges
	          WHERE ` + validFromCol + ` IS NOT NULL
	            AND ` + validFromCol + ` <= ?
	            AND (` + validToCol + ` IS NULL OR ` + validToCol + ` = '' OR ` + validToCol + ` > ?)`
	args := []any{atStr, atStr}

	if f := strings.TrimSpace(filter.From); f != "" {
		query += ` AND from_node_id = ?`
		args = append(args, cortexdb.EntityNodeID(f))
	}
	if t := strings.TrimSpace(filter.Type); t != "" {
		query += ` AND edge_type = ?`
		args = append(args, t)
	}
	query += ` ORDER BY from_node_id, ` + validFromCol
	return d.Rebind(query), args
}

func QueryFactsAsOf(ctx context.Context, db *cortexdb.DB, at time.Time, filter TemporalFilter) ([]TemporalFact, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: temporal: nil db")
	}
	// Creates the graph tables too, so querying a brand-new brain that never
	// wrote a graph does not hit "no such table: graph_edges".
	if err := ensureTemporalIndex(ctx, db); err != nil {
		return nil, err
	}
	names := loadEntityDisplayNames(ctx, db)

	query, args := asOfQuery(db.Dialect(), at, filter)

	rows, err := db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("graphflow: query facts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]TemporalFact, 0)
	for rows.Next() {
		var fromID, toID, etype string
		var validFromStr, validToStr, recordedStr, supersededStr sql.NullString
		if err := rows.Scan(&fromID, &toID, &etype, &validFromStr, &validToStr, &recordedStr, &supersededStr); err != nil {
			return nil, fmt.Errorf("graphflow: query facts: scan: %w", err)
		}

		validFrom, err := time.Parse(time.RFC3339, validFromStr.String)
		if err != nil {
			continue // malformed valid_from — not a well-formed temporal fact
		}
		if validFrom.After(at) {
			continue // interval starts after the queried instant
		}

		var validTo *time.Time
		if validToStr.Valid && validToStr.String != "" {
			vt, err := time.Parse(time.RFC3339, validToStr.String)
			if err != nil {
				continue
			}
			if !at.Before(vt) {
				continue // at >= valid_to — interval already closed
			}
			validTo = &vt
		}

		fact := TemporalFact{
			From:         displayNameFor(names, fromID),
			To:           displayNameFor(names, toID),
			Type:         etype,
			ValidFrom:    &validFrom,
			ValidTo:      validTo,
			SupersededBy: supersededStr.String,
		}
		if recordedStr.Valid && recordedStr.String != "" {
			if ra, err := time.Parse(time.RFC3339, recordedStr.String); err == nil {
				fact.RecordedAt = &ra
			}
		}
		out = append(out, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graphflow: query facts: %w", err)
	}
	return out, nil
}

// displayNameFor resolves an entity node id to its display name, falling back to
// the id suffix when the node has no stored content (mirrors community.go).
func displayNameFor(names map[string]string, nodeID string) string {
	if name, ok := names[nodeID]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	return trimEntityPrefix(nodeID)
}

// firstNonEmptyTemporal returns the first non-blank string, else the fallback.
func firstNonEmptyTemporal(s, fallback string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}
