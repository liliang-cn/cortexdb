package graph

// Point-in-time reads: the graph as it was.
//
// Until this file, a graph read answered exactly one question — "what is true
// now" — and a delete was the end of the record. Nothing said when a node or
// edge started being true, when it stopped, or when this store learned either,
// so "what did we believe about sds-meta before the failover?" had no answer at
// all. Not a slow answer or an approximate one: no query could express it.
//
// The shape here is bitemporal, which is two axes and not one:
//
//	valid time        valid_from … valid_to        when the fact was true
//	transaction time  recorded_at … retracted_at   when this store believed it
//
// They are different questions and confusing them is the usual way a "time
// travel" feature turns out to be a lie. "The runbook said to fail over to
// sds-meta" can stop being true in the world on Tuesday and stop being believed
// here on Friday, and an audit needs both.
//
// # Where a past row lives
//
// The live tables — graph_nodes, graph_edges — hold exactly the rows that are
// current: recorded, not retracted, still valid. Superseded and retracted rows
// move to graph_node_history / graph_edge_history, which carry the same columns.
//
// This is the load-bearing decision in the file and it was not the obvious one.
// Keeping a retracted row in place with retracted_at set reads better in a
// design document, and it is what a from-scratch design would do. It cannot be
// done here: something like thirty files across pkg/liveview, pkg/graphflow,
// pkg/hindsight and pkg/cortexdb query graph_nodes and graph_edges with their
// own SQL, and a retracted row left in place is a deleted node those queries
// would start returning. The filter cannot be added to all of them — some are
// not ours to change, and the ones that are would each become a place the
// filter can be forgotten. Moving the row instead means every existing query in
// the module keeps its exact meaning with no edit at all, which is also the only
// way "every existing test passes untouched" is true rather than hoped for.
//
// The cost is honest and worth stating: an as-of read is a UNION of the live
// table and the history table, so it is slower than a current read, and history
// is storage that only Purge reclaims.
//
// # Invariant
//
// A row in a live table has valid_to IS NULL and retracted_at IS NULL. Nothing
// writes an ended or retracted row back into a live table; that is what keeps
// the unedited queries correct. ValidTo, RecordedAt and RetractedAt on the
// structs are therefore output-only, in the same way CreatedAt already was.
//
// # Timestamps
//
// All four columns are TIMESTAMP and are written only from Go, as UTC truncated
// to microseconds — never from CURRENT_TIMESTAMP. Three reasons, each of which
// was a live hazard:
//
//   - SQLite's CURRENT_TIMESTAMP has one-second resolution, so two upserts in
//     the same second would produce a zero-length version interval that no
//     as-of read can ever land inside.
//   - modernc's SQLite driver stores a time.Time with Go's default layout
//     ("2020-01-02 03:04:05.123456 +0000 UTC") and compares it as text. That
//     orders correctly only if every value in the column has the same layout
//     and the same zone, which is true exactly as long as Go writes all of them
//     in UTC. A local-zone time in one of these columns would sort wrong and
//     nothing would report an error.
//   - PostgreSQL truncates to microseconds and SQLite does not, so a value
//     written on one backend and compared on the other would differ in the
//     seventh digit. Truncating in Go makes the two agree.
//
// The clock is also monotonic per store (see stamp): two writes in the same
// microsecond get consecutive instants rather than the same one.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ReadOptions is how much of the past a read is allowed to see.
//
// Carried on the context rather than passed as an argument, which is the
// unusual choice and a deliberate one. The alternative is a parameter on every
// read, and the read surface here is not flat: Neighbors calls GetEdges, which
// calls nothing; ShortestPath calls GetEdges and getEdgeByID; ExpandGraph in
// pkg/cortexdb calls Neighbors; HybridSearch calls its own scan. Threading a
// parameter means changing the signature of every one of them — breaking every
// caller in and outside this module — or shipping an As-Of twin of each and
// letting the two drift. An as-of read is also not really an argument to one
// query: it is the epoch the whole traversal happens in, and a traversal that
// read some hops at one instant and some at another would produce a graph that
// never existed.
//
// The risk of an ambient setting is that it leaks into a write. That is closed
// rather than documented away: every write path calls errIfAsOf and refuses.
type ReadOptions struct {
	// AsOf reads the graph as it stood at this instant. The zero time means
	// now, which is what every caller before this field got and still gets.
	AsOf time.Time
}

// AsOf reports whether these options ask for a past read.
func (r ReadOptions) IsAsOf() bool { return !r.AsOf.IsZero() }

type readOptionsKey struct{}

// WithReadOptions returns a context whose graph reads answer at opts.AsOf.
func WithReadOptions(ctx context.Context, opts ReadOptions) context.Context {
	if !opts.IsAsOf() {
		return ctx
	}
	return context.WithValue(ctx, readOptionsKey{}, ReadOptions{AsOf: stamp(opts.AsOf)})
}

// AsOf is WithReadOptions for the one field it has.
//
//	nodes, err := store.ListNodes(graph.AsOf(ctx, before), nil)
func AsOf(ctx context.Context, at time.Time) context.Context {
	return WithReadOptions(ctx, ReadOptions{AsOf: at})
}

// ReadOptionsFrom reads back what WithReadOptions put on the context. The zero
// value — read now — is returned for a context that carries nothing.
func ReadOptionsFrom(ctx context.Context) ReadOptions {
	if ctx == nil {
		return ReadOptions{}
	}
	opts, _ := ctx.Value(readOptionsKey{}).(ReadOptions)
	return opts
}

// errIfAsOf refuses a write issued under an as-of context.
//
// Silently writing "now" while the caller believes it is working at another
// instant is the failure mode an ambient setting invites, and it corrupts the
// record rather than the answer: the write lands, dated wrong, and nothing
// says so. A caller that genuinely means to write while holding an as-of
// context can strip it.
func errIfAsOf(ctx context.Context) error {
	if ro := ReadOptionsFrom(ctx); ro.IsAsOf() {
		return fmt.Errorf("cortexdb/graph: this is a write and the context asks to read as of %s; "+
			"strip the as-of before writing", ro.AsOf.Format(time.RFC3339Nano))
	}
	return nil
}

// stamp normalises an instant to what these columns store: UTC, microseconds.
func stamp(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// temporalClock hands out strictly increasing instants.
//
// Wall clocks repeat: two upserts of the same node inside one microsecond would
// otherwise close a version interval at the instant it opened, and a
// zero-length interval is a version no as-of read can ever see. Monotonic here
// costs a mutex on the write path and removes the whole class.
type temporalClock struct {
	mu   sync.Mutex
	last time.Time
}

func (c *temporalClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := stamp(time.Now())
	if !t.After(c.last) {
		t = c.last.Add(time.Microsecond)
	}
	c.last = t
	return t
}

// Now reserves and returns an instant from this store's clock.
//
// Every write this store makes afterwards is stamped strictly later, which is
// what makes "the state before that change" expressible without sleeping:
//
//	before := store.Now()
//	store.UpsertNode(ctx, changed)
//	store.GetNode(graph.AsOf(ctx, before), id) // the old content
func (g *GraphStore) Now() time.Time { return g.clock.now() }

// --- schema ------------------------------------------------------------------

// nodeColumns and edgeColumns are the full column lists the live table and its
// history share, in one order, so the two can be UNIONed without naming them
// again at each call site. Every as-of read selects from that union, so a
// column added to one table and not the other breaks every past read at once —
// which is the failure it is worth having in one place.
const nodeColumns = `id, vector, content, node_type, properties, created_at, updated_at, ` +
	`valid_from, valid_to, recorded_at, retracted_at`

const edgeColumns = `id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at, ` +
	`valid_from, valid_to, recorded_at, retracted_at`

// temporalColumns is the migration for a brain that predates this release.
//
// Added with no DEFAULT, so every existing row gets NULL in all four — and NULL
// is read as "unbounded" on both ends of both axes: always been true, always
// been known, never ended, never retracted. That is exactly "current", so a
// brain written before this file reads identically after it, and an as-of read
// at any instant still finds those rows rather than reporting that the graph
// was empty before the upgrade.
//
// A DEFAULT CURRENT_TIMESTAMP would have been the tempting alternative and
// SQLite rejects it outright: ALTER TABLE ADD COLUMN takes only constant
// defaults there. Backfilling from created_at was the other option; it is a
// full table scan on upgrade, and it would claim a recording instant the store
// does not actually know.
var temporalColumns = []string{
	`ALTER TABLE graph_nodes ADD COLUMN valid_from TIMESTAMP`,
	`ALTER TABLE graph_nodes ADD COLUMN valid_to TIMESTAMP`,
	`ALTER TABLE graph_nodes ADD COLUMN recorded_at TIMESTAMP`,
	`ALTER TABLE graph_nodes ADD COLUMN retracted_at TIMESTAMP`,
	`ALTER TABLE graph_edges ADD COLUMN valid_from TIMESTAMP`,
	`ALTER TABLE graph_edges ADD COLUMN valid_to TIMESTAMP`,
	`ALTER TABLE graph_edges ADD COLUMN recorded_at TIMESTAMP`,
	`ALTER TABLE graph_edges ADD COLUMN retracted_at TIMESTAMP`,
}

// createTemporalSchema adds the bitemporal columns and the history tables.
// Callers must hold schemaMu; createGraphSchema calls it last.
func (g *GraphStore) createTemporalSchema(ctx context.Context) error {
	for _, stmt := range temporalColumns {
		if _, err := g.exec(ctx, stmt); err != nil && !g.dialect.IsDuplicateColumn(err) {
			return fmt.Errorf("cortexdb/graph: temporal migration: %w", err)
		}
	}

	blob := g.dialect.BlobType()
	// No primary key on either history table, on purpose. (id, valid_from)
	// reads like the right key and is not one: a caller may state the same
	// valid_from twice for the same id — backdating two corrections to one
	// moment is a legitimate thing to do — and a key would turn that into a
	// failed write rather than two recorded beliefs. Version intervals are kept
	// disjoint by the code that writes them, not by the schema.
	//
	// The history tables also deliberately carry no foreign key to
	// graph_nodes: the whole point of a retracted node is that its row is no
	// longer in graph_nodes, and its edges' history has to outlive it.
	schema := fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS graph_node_history (
		id TEXT NOT NULL,
		vector %[1]s,
		content TEXT,
		node_type TEXT,
		properties TEXT,
		created_at TIMESTAMP,
		updated_at TIMESTAMP,
		valid_from TIMESTAMP,
		valid_to TIMESTAMP,
		recorded_at TIMESTAMP,
		retracted_at TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS graph_edge_history (
		id TEXT NOT NULL,
		from_node_id TEXT NOT NULL,
		to_node_id TEXT NOT NULL,
		edge_type TEXT,
		weight REAL,
		properties TEXT,
		vector %[1]s,
		created_at TIMESTAMP,
		valid_from TIMESTAMP,
		valid_to TIMESTAMP,
		recorded_at TIMESTAMP,
		retracted_at TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_node_history_id ON graph_node_history(id);
	CREATE INDEX IF NOT EXISTS idx_node_history_window ON graph_node_history(valid_from, valid_to);
	CREATE INDEX IF NOT EXISTS idx_edge_history_id ON graph_edge_history(id);
	CREATE INDEX IF NOT EXISTS idx_edge_history_from ON graph_edge_history(from_node_id);
	CREATE INDEX IF NOT EXISTS idx_edge_history_to ON graph_edge_history(to_node_id);
	CREATE INDEX IF NOT EXISTS idx_edge_history_window ON graph_edge_history(valid_from, valid_to);
	`, blob)

	if _, err := g.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("cortexdb/graph: history tables: %w", err)
	}
	return nil
}

// --- as-of read sources ------------------------------------------------------

// visibleAt is the predicate both axes reduce to.
//
// NULL is unbounded everywhere, which is what makes a migrated row current: it
// has been true since before the beginning, was known before the beginning, has
// not ended and has not been retracted.
//
// valid_to and retracted_at are strict (> asOf) while valid_from and
// recorded_at are not (<= asOf), so intervals are half-open [from, to). That is
// what lets one version end at the exact instant the next begins with no gap
// and no instant at which both are visible.
const visibleAt = `(valid_from IS NULL OR valid_from <= ?) AND ` +
	`(valid_to IS NULL OR valid_to > ?) AND ` +
	`(recorded_at IS NULL OR recorded_at <= ?) AND ` +
	`(retracted_at IS NULL OR retracted_at > ?)`

// NodeSource and EdgeSource name the rows a read should see: a table name to
// put in a FROM clause, and the arguments it binds ahead of the query's own.
//
// With no as-of on the context they return the bare table name, so the query
// the caller writes is byte-for-byte the query it was before this file existed
// — no subquery, no extra predicate, nothing for the planner to reconsider.
// Only a past read pays for the union of the live table and its history.
//
// The caller appends its own alias and puts these arguments first, in the order
// the sources appear in the SQL text:
//
//	src, args := g.EdgeSource(ctx)
//	rows, err := db.Query(`SELECT id FROM `+src+` AS e WHERE edge_type = ?`,
//	    append(args, "mentions")...)
//
// Exported because pkg/cortexdb writes a good deal of its own SQL against
// graph_nodes and graph_edges, and a read there that ignored the as-of would be
// a read that silently answered a different question from the one beside it.
func (g *GraphStore) NodeSource(ctx context.Context) (string, []any) {
	return temporalSource(ctx, "graph_nodes", "graph_node_history", nodeColumns)
}

func (g *GraphStore) EdgeSource(ctx context.Context) (string, []any) {
	return temporalSource(ctx, "graph_edges", "graph_edge_history", edgeColumns)
}

func (g *GraphStore) nodeSource(ctx context.Context) (string, []any) { return g.NodeSource(ctx) }
func (g *GraphStore) edgeSource(ctx context.Context) (string, []any) { return g.EdgeSource(ctx) }

func temporalSource(ctx context.Context, live, history, columns string) (string, []any) {
	ro := ReadOptionsFrom(ctx)
	if !ro.IsAsOf() {
		return live, nil
	}
	t := ro.AsOf
	src := "(SELECT " + columns + " FROM " + live + " WHERE " + visibleAt +
		" UNION ALL SELECT " + columns + " FROM " + history + " WHERE " + visibleAt + ")"
	return src, []any{t, t, t, t, t, t, t, t}
}

// --- writing history ---------------------------------------------------------

// nodeContentChanged and edgeContentChanged say when a version ends.
//
// Content, type and properties — not the vector. Re-embedding a corpus with a
// newer model rewrites every vector in the graph and changes nothing anybody
// believes; versioning on it would double the store and fill the history with
// rows whose only difference is 768 floats. A vector is derived from the
// content that is versioned here, so nothing is lost that cannot be recomputed.
//
// COALESCE on both sides because these columns are nullable in rows written
// before this file and `NULL <> 'x'` is NULL, not true — an unguarded
// comparison would decide that a node whose content was NULL had not changed.
const nodeContentChanged = `(COALESCE(content, '') <> COALESCE(?, '') OR ` +
	`COALESCE(node_type, '') <> COALESCE(?, '') OR ` +
	`COALESCE(properties, '') <> COALESCE(?, ''))`

const edgeContentChanged = `(COALESCE(from_node_id, '') <> COALESCE(?, '') OR ` +
	`COALESCE(to_node_id, '') <> COALESCE(?, '') OR ` +
	`COALESCE(edge_type, '') <> COALESCE(?, '') OR ` +
	`COALESCE(weight, 0) <> COALESCE(?, 0) OR ` +
	`COALESCE(properties, '') <> COALESCE(?, ''))`

// nodeChangedFromExcluded and edgeChangedFromExcluded are the same test written
// against the row an upsert is about to overwrite, for the ON CONFLICT DO
// UPDATE clause: only a change of content opens a new version, so an upsert
// that rewrites a node with what it already said leaves valid_from and
// recorded_at where they were. Without this, re-ingesting an unchanged document
// would restamp every node in it and every as-of read would report that the
// whole graph began at the last ingest.
//
// The table is named rather than aliased because that is the only spelling both
// databases accept inside DO UPDATE.
const nodeChangedFromExcluded = `(COALESCE(graph_nodes.content, '') <> COALESCE(excluded.content, '') OR ` +
	`COALESCE(graph_nodes.node_type, '') <> COALESCE(excluded.node_type, '') OR ` +
	`COALESCE(graph_nodes.properties, '') <> COALESCE(excluded.properties, ''))`

const edgeChangedFromExcluded = `(COALESCE(graph_edges.from_node_id, '') <> COALESCE(excluded.from_node_id, '') OR ` +
	`COALESCE(graph_edges.to_node_id, '') <> COALESCE(excluded.to_node_id, '') OR ` +
	`COALESCE(graph_edges.edge_type, '') <> COALESCE(excluded.edge_type, '') OR ` +
	`COALESCE(graph_edges.weight, 0) <> COALESCE(excluded.weight, 0) OR ` +
	`COALESCE(graph_edges.properties, '') <> COALESCE(excluded.properties, ''))`

// execer is whatever can run a statement — the store or a transaction. The
// archive statements below are called from both, and from batch paths that must
// archive and delete atomically.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// archiveNodeVersion copies the current row of nodeID into history with its
// validity closed at `at`, but only when the content it is about to be replaced
// with actually differs.
//
// One statement, no read-back: the comparison happens in the database, so an
// upsert that changes nothing writes nothing and an upsert of a node that does
// not exist yet matches no row. That is what keeps versioning affordable on the
// ingest path, where the overwhelming majority of upserts are one or the other.
// archiveNodeVersionSQL and archiveEdgeVersionSQL are the statement text, kept
// as constants because the batch paths prepare them once and execute them per
// row rather than rebuilding the string for each.
const archiveNodeVersionSQL = `
	INSERT INTO graph_node_history (` + nodeColumns + `)
	SELECT id, vector, content, node_type, properties, created_at, updated_at,
	       valid_from, COALESCE(valid_to, ?), recorded_at, retracted_at
	FROM graph_nodes
	WHERE id = ? AND ` + nodeContentChanged

const archiveEdgeVersionSQL = `
	INSERT INTO graph_edge_history (` + edgeColumns + `)
	SELECT id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at,
	       valid_from, COALESCE(valid_to, ?), recorded_at, retracted_at
	FROM graph_edges
	WHERE id = ? AND ` + edgeContentChanged

// upsertNodeSQL and upsertEdgeSQL are the write, shared by the single-row and
// batch paths so the two cannot version differently. The CASE guards are what
// keep an unchanged rewrite from restamping the row; see
// nodeChangedFromExcluded.
const upsertNodeSQL = `
	INSERT INTO graph_nodes (id, vector, content, node_type, properties, updated_at, valid_from, recorded_at)
	VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		vector = excluded.vector,
		content = excluded.content,
		node_type = excluded.node_type,
		properties = excluded.properties,
		updated_at = CURRENT_TIMESTAMP,
		valid_from = CASE WHEN ` + nodeChangedFromExcluded + ` THEN excluded.valid_from ELSE graph_nodes.valid_from END,
		recorded_at = CASE WHEN ` + nodeChangedFromExcluded + ` THEN excluded.recorded_at ELSE graph_nodes.recorded_at END
	`

const upsertEdgeSQL = `
	INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, weight, properties, vector, valid_from, recorded_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		from_node_id = excluded.from_node_id,
		to_node_id = excluded.to_node_id,
		edge_type = excluded.edge_type,
		weight = excluded.weight,
		properties = excluded.properties,
		vector = excluded.vector,
		valid_from = CASE WHEN ` + edgeChangedFromExcluded + ` THEN excluded.valid_from ELSE graph_edges.valid_from END,
		recorded_at = CASE WHEN ` + edgeChangedFromExcluded + ` THEN excluded.recorded_at ELSE graph_edges.recorded_at END
	`

func (g *GraphStore) archiveNodeVersion(ctx context.Context, ex execer, nodeID string, at time.Time, content, nodeType, properties string) error {
	_, err := ex.ExecContext(ctx, g.dialect.Rebind(archiveNodeVersionSQL), at, nodeID, content, nodeType, properties)
	if err != nil {
		return fmt.Errorf("cortexdb/graph: archive node version %s: %w", nodeID, err)
	}
	return nil
}

func (g *GraphStore) archiveEdgeVersion(ctx context.Context, ex execer, edgeID string, at time.Time, from, to, edgeType string, weight float64, properties string) error {
	_, err := ex.ExecContext(ctx, g.dialect.Rebind(archiveEdgeVersionSQL), at, edgeID, from, to, edgeType, weight, properties)
	if err != nil {
		return fmt.Errorf("cortexdb/graph: archive edge version %s: %w", edgeID, err)
	}
	return nil
}

// versionStamps picks the instant a write opens its new version at, and the
// instant the store records having learned it. They differ only when the caller
// stated a ValidFrom of its own — backdating what was true, never backdating
// what was known.
func (g *GraphStore) versionStamps(validFrom time.Time) (at time.Time, recorded time.Time) {
	recorded = g.clock.now()
	at = recorded
	if !validFrom.IsZero() {
		at = stamp(validFrom)
	}
	return at, recorded
}

// --- retraction --------------------------------------------------------------

// ArchiveNodesTx moves nodes and every edge touching them into history with
// retracted_at set, without deleting anything.
//
// Exported and transaction-taking for the one caller shape that cannot use
// RetractNodes: pkg/cortexdb deletes a document's whole graph inside a
// transaction of its own, and splitting the archive out of that transaction
// would make a crash between them lose the history of rows that did get
// deleted. Call it immediately before the DELETE, in the same transaction.
//
// Edges go with the node because a retracted node's edges are retracted too —
// graph_edges has ON DELETE CASCADE on both ends, so the DELETE that follows
// removes them whether or not the caller listed them, and an edge deleted with
// no history row is a fact that vanished.
func (g *GraphStore) ArchiveNodesTx(ctx context.Context, tx *sql.Tx, nodeIDs []string, at time.Time) error {
	return g.archiveNodes(ctx, tx, nodeIDs, stamp(at))
}

// ArchiveEdgesTx is ArchiveNodesTx for edges the caller deletes itself.
func (g *GraphStore) ArchiveEdgesTx(ctx context.Context, tx *sql.Tx, edgeIDs []string, at time.Time) error {
	return g.archiveEdges(ctx, tx, edgeIDs, stamp(at), "id")
}

func (g *GraphStore) archiveNodes(ctx context.Context, ex execer, nodeIDs []string, at time.Time) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	for _, chunk := range idChunks(nodeIDs) {
		// The node's edges first: after the node row is gone the cascade has
		// already taken them, and after this statement they are recorded.
		if err := g.archiveEdges(ctx, ex, chunk, at, "endpoint"); err != nil {
			return err
		}
		holes, args := placeholderList(chunk)
		q := g.dialect.Rebind(`
			INSERT INTO graph_node_history (` + nodeColumns + `)
			SELECT id, vector, content, node_type, properties, created_at, updated_at,
			       valid_from, COALESCE(valid_to, ?), recorded_at, ?
			FROM graph_nodes WHERE id IN (` + holes + `)`)
		if _, err := ex.ExecContext(ctx, q, append([]any{at, at}, args...)...); err != nil {
			return fmt.Errorf("cortexdb/graph: archive nodes: %w", err)
		}
	}
	return nil
}

// archiveEdges matches by id, or by either endpoint when match is "endpoint" —
// which is how a node's retraction reaches the edges the cascade is about to
// take.
func (g *GraphStore) archiveEdges(ctx context.Context, ex execer, ids []string, at time.Time, match string) error {
	if len(ids) == 0 {
		return nil
	}
	for _, chunk := range idChunks(ids) {
		holes, args := placeholderList(chunk)
		where := "id IN (" + holes + ")"
		params := append([]any{at, at}, args...)
		if match == "endpoint" {
			where = "from_node_id IN (" + holes + ") OR to_node_id IN (" + holes + ")"
			params = append(append([]any{at, at}, args...), args...)
		}
		q := g.dialect.Rebind(`
			INSERT INTO graph_edge_history (` + edgeColumns + `)
			SELECT id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at,
			       valid_from, COALESCE(valid_to, ?), recorded_at, ?
			FROM graph_edges WHERE ` + where)
		if _, err := ex.ExecContext(ctx, q, params...); err != nil {
			return fmt.Errorf("cortexdb/graph: archive edges: %w", err)
		}
	}
	return nil
}

// RetractNodeAt retracts a node, and every edge touching it, as of `at`.
//
// DeleteNode is this with `at` taken from the store's clock. The instant is a
// parameter because a retraction is a claim about when belief ended, and that
// is not always the moment somebody got round to running the delete: a fact
// discovered on Friday to have been wrong since Tuesday is retracted as of
// Tuesday, and every as-of read after Tuesday then agrees.
func (g *GraphStore) RetractNodeAt(ctx context.Context, nodeID string, at time.Time) error {
	if err := errIfAsOf(ctx); err != nil {
		return err
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := g.archiveNodes(ctx, tx, []string{nodeID}, stamp(at)); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, g.dialect.Rebind(`DELETE FROM graph_nodes WHERE id = ?`), nodeID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("node not found: %s", nodeID)
	}
	// Edges the cascade will not have taken, because SQLite only enforces the
	// foreign key with foreign_keys=ON and a graph store handed a bare *sql.DB
	// may not have it. Archived above either way; this makes the live table
	// agree with the history in both configurations.
	if _, err := tx.ExecContext(ctx,
		g.dialect.Rebind(`DELETE FROM graph_edges WHERE from_node_id = ? OR to_node_id = ?`),
		nodeID, nodeID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if g.hnswIndex != nil {
		g.hnswIndex.index.Remove(nodeID)
	}
	return nil
}

// RetractEdgeAt retracts one edge as of `at`. DeleteEdge is this, now.
func (g *GraphStore) RetractEdgeAt(ctx context.Context, edgeID string, at time.Time) error {
	if err := errIfAsOf(ctx); err != nil {
		return err
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := g.archiveEdges(ctx, tx, []string{edgeID}, stamp(at), "id"); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, g.dialect.Rebind(`DELETE FROM graph_edges WHERE id = ?`), edgeID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("edge not found: %s", edgeID)
	}
	return tx.Commit()
}

// RetractNodes retracts many nodes in one transaction, reporting how many rows
// it found. Ids that match nothing are not an error — "not there" is not a
// failure to retract — so the count is what the caller should report.
func (g *GraphStore) RetractNodes(ctx context.Context, nodeIDs []string) (nodes int, edges int, err error) {
	if err := errIfAsOf(ctx); err != nil {
		return 0, 0, err
	}
	if len(nodeIDs) == 0 {
		return 0, 0, nil
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return 0, 0, err
	}
	at := g.clock.now()
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := g.archiveNodes(ctx, tx, nodeIDs, at); err != nil {
		return 0, 0, err
	}
	for _, chunk := range idChunks(nodeIDs) {
		holes, args := placeholderList(chunk)
		res, err := tx.ExecContext(ctx, g.dialect.Rebind(
			`DELETE FROM graph_edges WHERE from_node_id IN (`+holes+`) OR to_node_id IN (`+holes+`)`),
			append(append([]any{}, args...), args...)...)
		if err != nil {
			return 0, 0, err
		}
		if n, err := res.RowsAffected(); err == nil {
			edges += int(n)
		}
		res, err = tx.ExecContext(ctx, g.dialect.Rebind(
			`DELETE FROM graph_nodes WHERE id IN (`+holes+`)`), args...)
		if err != nil {
			return 0, 0, err
		}
		if n, err := res.RowsAffected(); err == nil {
			nodes += int(n)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	if g.hnswIndex != nil {
		for _, id := range nodeIDs {
			g.hnswIndex.index.Remove(id)
		}
	}
	return nodes, edges, nil
}

// --- purge -------------------------------------------------------------------

// PurgeReport is what Purge reclaimed.
type PurgeReport struct {
	Nodes  int       `json:"nodes"`
	Edges  int       `json:"edges"`
	Before time.Time `json:"before"`
	DryRun bool      `json:"dry_run,omitempty"`
}

// Purge physically removes history rows that closed before `before`.
//
// The only hard delete in this file, and the reason the rest of it can be
// additive: history is storage that grows with every correction and every
// retraction, and an operator has to be able to get it back. It is a write.
//
// "Closed" is retracted_at when the row was retracted and valid_to when it was
// superseded; every history row has one of them by construction. Both are past
// belief, and an operator reclaiming space wants both — a purge that left every
// superseded version behind would reclaim almost nothing on a graph that is
// corrected more often than it is deleted from.
//
// Purge never touches a live table, so nothing a current read can see is at
// risk. It refuses a zero cutoff rather than reading it as "everything": the
// zero time is what an unset field looks like, and a caller that forgot to set
// one must not thereby erase the whole record.
func (g *GraphStore) Purge(ctx context.Context, before time.Time, dryRun bool) (*PurgeReport, error) {
	if err := errIfAsOf(ctx); err != nil {
		return nil, err
	}
	if before.IsZero() {
		return nil, fmt.Errorf("cortexdb/graph: purge: a cutoff is required; " +
			"an unset cutoff would purge the entire history and that is never what a caller meant")
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	cutoff := stamp(before)
	report := &PurgeReport{Before: cutoff, DryRun: dryRun}

	const closedBefore = `COALESCE(retracted_at, valid_to) IS NOT NULL AND COALESCE(retracted_at, valid_to) < ?`
	for _, t := range []struct {
		table string
		count *int
	}{
		{"graph_node_history", &report.Nodes},
		{"graph_edge_history", &report.Edges},
	} {
		if dryRun {
			if err := g.queryRow(ctx,
				`SELECT COUNT(*) FROM `+t.table+` WHERE `+closedBefore, cutoff).Scan(t.count); err != nil {
				return nil, fmt.Errorf("cortexdb/graph: purge: count %s: %w", t.table, err)
			}
			continue
		}
		res, err := g.exec(ctx, `DELETE FROM `+t.table+` WHERE `+closedBefore, cutoff)
		if err != nil {
			return nil, fmt.Errorf("cortexdb/graph: purge: %s: %w", t.table, err)
		}
		if n, err := res.RowsAffected(); err == nil {
			*t.count = int(n)
		}
	}
	return report, nil
}

// --- helpers -----------------------------------------------------------------

// idChunks keeps an IN list under SQLite's variable ceiling. The endpoint form
// of archiveEdges binds each chunk twice, so the chunk is a quarter of the
// 999-parameter limit rather than half of it.
func idChunks(ids []string) [][]string {
	const size = 200
	if len(ids) <= size {
		return [][]string{ids}
	}
	var out [][]string
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[i:end])
	}
	return out
}

func placeholderList(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// --- scanning ----------------------------------------------------------------

// temporalScan reads the four columns, any of which is NULL on a row written
// before this release or never closed.
type temporalScan struct {
	validFrom, validTo, recordedAt, retractedAt sql.NullTime
}

func nullTime(v sql.NullTime) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return v.Time.UTC()
}

func (t temporalScan) applyNode(n *GraphNode) {
	n.ValidFrom, n.ValidTo = nullTime(t.validFrom), nullTime(t.validTo)
	n.RecordedAt, n.RetractedAt = nullTime(t.recordedAt), nullTime(t.retractedAt)
}

func (t temporalScan) applyEdge(e *GraphEdge) {
	e.ValidFrom, e.ValidTo = nullTime(t.validFrom), nullTime(t.validTo)
	e.RecordedAt, e.RetractedAt = nullTime(t.recordedAt), nullTime(t.retractedAt)
}

// temporalSelect is the suffix every read that wants these columns appends to
// its projection, so the order matches temporalScan.dest.
const temporalSelect = `, valid_from, valid_to, recorded_at, retracted_at`

// prepareNodeUpsert and prepareEdgeUpsert give a batch its two statements: the
// versioned upsert and the archive that closes the version it replaces.
//
// Prepared together, and closed together by the caller, because a batch that
// prepared only one of them would write rows with no history and nothing would
// report it — the graph would simply have no past for whatever came in through
// that path.
func (g *GraphStore) prepareNodeUpsert(ctx context.Context, tx *sql.Tx) (upsert, archive *sql.Stmt, err error) {
	if upsert, err = g.txPrepare(ctx, tx, upsertNodeSQL); err != nil {
		return nil, nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	if archive, err = g.txPrepare(ctx, tx, archiveNodeVersionSQL); err != nil {
		_ = upsert.Close()
		return nil, nil, fmt.Errorf("failed to prepare node history statement: %w", err)
	}
	return upsert, archive, nil
}

func (g *GraphStore) prepareEdgeUpsert(ctx context.Context, tx *sql.Tx) (upsert, archive *sql.Stmt, err error) {
	if upsert, err = g.txPrepare(ctx, tx, upsertEdgeSQL); err != nil {
		return nil, nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	if archive, err = g.txPrepare(ctx, tx, archiveEdgeVersionSQL); err != nil {
		_ = upsert.Close()
		return nil, nil, fmt.Errorf("failed to prepare edge history statement: %w", err)
	}
	return upsert, archive, nil
}
