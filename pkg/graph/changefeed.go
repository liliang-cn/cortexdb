package graph

// The change feed: every committed write to the graph, the triple store, memory
// and knowledge, in commit order, readable by cursor.
//
// # Why triggers
//
// A write reaches these tables through dozens of paths — UpsertNode, the batch
// upserts, MergeEntities, the retraction and purge paths, the RDF writers, the
// SPARQL Update executor, pkg/cortexdb's own transactions for memory, documents
// and ontology objects, and any older binary that still opens the same file.
// An event emitted from Go would have to be threaded through every one of them
// and would silently miss the next path someone adds. A row trigger is attached
// to the table instead, so it fires for every statement that changes a row, and
// it runs inside the statement's own transaction: the event commits with the
// write or not at all. A rolled-back transaction leaves no event because its
// event rows are rolled back with it, not because some code remembered to
// discard them.
//
// # Ordering
//
// The cursor is change_log.seq, and its contract is that a reader which has
// seen seq N will never later be shown an event with a seq below N. That is
// stronger than "seq increases": it says the visible prefix only ever grows at
// the end. Each backend earns it differently.
//
//   - SQLite serializes writers. A transaction takes the write lock at its first
//     write and holds it to commit, so the AUTOINCREMENT value its trigger
//     takes is allocated after every earlier writer committed and before any
//     later one starts. seq order is commit order by construction.
//   - PostgreSQL runs writers concurrently, so a seq taken at insert time would
//     let a transaction that drew 10 commit after one that drew 11, and a reader
//     that had already moved past 11 would never see 10. The seq is therefore
//     not taken at insert. Each event row is written with seq NULL, and a
//     DEFERRED constraint trigger — which PostgreSQL runs at COMMIT — takes a
//     transaction-scoped advisory lock and only then draws the seq. The lock is
//     held until the transaction is fully committed and visible, so the next
//     committer draws its seq strictly after this one became visible.
//
// # What is logged
//
// One event per changed row, with a summary of the row before and after rather
// than the row itself: the vectors and the full text of a memory are what the
// other tables are for, and a log that copied them would double the database.
// An UPDATE that changes nothing a reader can see — an upsert of identical
// content, a re-embedding — is not an event. The graph rows that merely mirror
// a stored triple (see upsertPreparedTripleTx) are not events either: the
// triple is, once.
//
// # Retention
//
// See ChangeFeedRetention. Pruning is explicit (PruneChanges) and is what
// pkg/cortexdb's janitor calls; a reader whose cursor fell behind what was
// pruned gets ErrChangesPruned rather than a silently shortened history.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// Kinds of change.
const (
	ChangeKindNode      = "node"
	ChangeKindEdge      = "edge"
	ChangeKindTriple    = "triple"
	ChangeKindMemory    = "memory"
	ChangeKindKnowledge = "knowledge"
	// ChangeKindOntologySchema is a saved, activated or deleted ontology
	// definition. Ontology objects themselves are graph nodes and arrive as
	// ChangeKindNode.
	ChangeKindOntologySchema = "ontology_schema"
)

// Operations.
//
// insert, update and delete are what happened to the row. supersede and merge
// refine update and delete when the graph's history says why: the temporal
// layer archives every version it replaces with a reason (see
// temporal_reason.go), and the log reads it back instead of guessing.
const (
	ChangeOpInsert    = "insert"
	ChangeOpUpdate    = "update"
	ChangeOpDelete    = "delete"
	ChangeOpSupersede = "supersede"
	ChangeOpMerge     = "merge"
)

// changeFeedVersion names the trigger definitions below. Bump it whenever a
// trigger changes, so an existing database replaces its triggers instead of
// keeping the ones an older binary created.
const changeFeedVersion = "3"

// ChangeEvent is one committed change.
type ChangeEvent struct {
	// Seq is the cursor: strictly increasing in commit order. Gaps are
	// possible (a PostgreSQL sequence does not hand back numbers a rolled-back
	// transaction drew); order is not.
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Op   string    `json:"op"`
	// ID is the changed row's id: node, edge, triple, message or document id.
	ID string `json:"id"`
	// Reason is the graph history's invalidation reason for an update or
	// delete of a node or edge (superseded, merged, retracted, ...), when one
	// was recorded.
	Reason string `json:"reason,omitempty"`
	// Producer names what made the change, when that is known: the history
	// row's producer for nodes and edges, and on PostgreSQL the session's
	// cortexdb.producer setting for everything else.
	Producer string `json:"producer,omitempty"`
	// Before and After summarize the row. Before is absent for an insert and
	// After for a delete. JSON-valued columns (properties, metadata) are
	// embedded as JSON, not as strings.
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

// ErrChangesPruned is returned for a cursor older than what retention kept.
// The events between the cursor and PrunedThrough are gone; a consumer that
// needs them must rebuild from current state and resume at the head.
var ErrChangesPruned = errors.New("change cursor is older than the retained change log")

// ChangesPrunedError carries how far the log was pruned.
type ChangesPrunedError struct {
	After         int64
	PrunedThrough int64
}

func (e *ChangesPrunedError) Error() string {
	return fmt.Sprintf("%v: cursor %d, pruned through %d", ErrChangesPruned, e.After, e.PrunedThrough)
}

// Unwrap lets errors.Is(err, ErrChangesPruned) match.
func (e *ChangesPrunedError) Unwrap() error { return ErrChangesPruned }

// ChangeFeedRetention bounds the log. Whichever limit is reached first wins.
//
// The default keeps seven days and at most 500,000 events. Seven days because
// the consumers this log exists for — inference maintenance, ontology
// triggers, a replica catching up — run continuously, and the case to survive
// is one of them being down over a long weekend, not for a month; a consumer
// gone longer than that rebuilds from current state, which every consumer must
// be able to do anyway. The row cap is there for the other failure: a bulk
// import writes in an hour what normal use writes in a year, and an event is a
// few hundred bytes, so 500,000 of them is on the order of 100–250 MB — large
// next to a brain of tens of MB, but bounded, and only reached by an import big
// enough that a rebuild is cheaper than replaying it anyway.
type ChangeFeedRetention struct {
	MaxAge  time.Duration `json:"max_age"`
	MaxRows int64         `json:"max_rows"`
}

// DefaultChangeFeedRetention is the retention pkg/cortexdb applies unless told
// otherwise.
var DefaultChangeFeedRetention = ChangeFeedRetention{MaxAge: 7 * 24 * time.Hour, MaxRows: 500_000}

// changeFeedState is the per-store bookkeeping: whether the triggers are known
// to be installed, and the channel that wakes pollers early.
type changeFeedState struct {
	mu        sync.Mutex
	installed bool
	wake      chan struct{}
	// maintainer is this process's inference maintainer, if one runs, so
	// that WaitForInference can wait on it rather than poll the store.
	maintainer *InferenceMaintainer
}

// wakeChan returns the channel the next NotifyChanges closes.
func (s *changeFeedState) wakeChan() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{})
	}
	return s.wake
}

// NotifyChanges wakes every in-process poller of this store's change log —
// subscribers and the inference maintainer — so they look now rather than at
// their next tick. It is an optimization only: pollers find every event on
// their own, and a write that forgets to call this is merely seen a few
// milliseconds later.
func (g *GraphStore) NotifyChanges() {
	g.feed.mu.Lock()
	defer g.feed.mu.Unlock()
	if g.feed.wake != nil {
		close(g.feed.wake)
		g.feed.wake = nil
	}
}

// feedTable describes one logged table.
type feedTable struct {
	table   string
	kind    string
	columns [][2]string // summary: json key, column
	// preview columns are summarized as their first 200 characters.
	preview [][2]string
	// changed are the columns an UPDATE must touch to be an event.
	changed []string
	// mirror, when set, is the predicate (over the row alias) that marks a
	// row as an RDF mirror and so not an event.
	mirror func(d sqldialect.Dialect, row string) string
	// history names the graph history table whose invalidation reason and
	// producer explain an update or delete.
	history string
	// blobSafe marks a table some writer fills through []byte parameters,
	// whose text columns must be CAST before SQLite's json_object will take
	// them; see summaryArgs.
	blobSafe bool
}

var feedTables = []feedTable{
	{
		table: "graph_nodes", kind: ChangeKindNode,
		columns: [][2]string{{"node_type", "node_type"}, {"properties", "properties"}, {"valid_from", "valid_from"}},
		preview: [][2]string{{"content", "content"}},
		changed: []string{"id", "node_type", "properties", "content"},
		mirror: func(_ sqldialect.Dialect, row string) string {
			return fmt.Sprintf("(%[1]s.id LIKE 'rdf:%%' AND COALESCE(%[1]s.node_type, '') IN ('%[2]s'))",
				row, strings.Join(projectionRDFMirrorKind, "', '"))
		},
		history: "graph_node_history",
	},
	{
		table: "graph_edges", kind: ChangeKindEdge,
		columns: [][2]string{{"from", "from_node_id"}, {"to", "to_node_id"}, {"edge_type", "edge_type"},
			{"weight", "weight"}, {"properties", "properties"}, {"valid_from", "valid_from"}},
		changed: []string{"id", "from_node_id", "to_node_id", "edge_type", "weight", "properties"},
		mirror: func(d sqldialect.Dialect, row string) string {
			if d.Kind() == sqldialect.Postgres {
				return "change_log_rdf_flag(" + row + ".properties)"
			}
			return "(" + d.JSONFlag(row+".properties", "rdf") + ") = 1"
		},
		history: "graph_edge_history",
	},
	{
		table: "kg_triples", kind: ChangeKindTriple,
		columns: [][2]string{
			{"subject_kind", "subject_kind"}, {"subject", "subject_value"}, {"predicate", "predicate_value"},
			{"object_kind", "object_kind"}, {"object", "object_value"}, {"datatype", "object_datatype"},
			{"language", "object_language"}, {"graph_kind", "graph_kind"}, {"graph", "graph_value"},
			{"inferred", "inferred"}, {"rule", "inference_rule"},
		},
		changed: []string{"id", "subject_kind", "subject_value", "predicate_value", "object_kind", "object_value",
			"object_datatype", "object_language", "graph_kind", "graph_value", "inferred", "inference_rule"},
	},
	{
		table: "messages", kind: ChangeKindMemory, blobSafe: true,
		columns: [][2]string{{"session_id", "session_id"}, {"role", "role"}, {"metadata", "metadata"}},
		preview: [][2]string{{"content", "content"}},
		changed: []string{"id", "session_id", "role", "content", "metadata"},
	},
	{
		table: "documents", kind: ChangeKindKnowledge, blobSafe: true,
		columns: [][2]string{{"title", "title"}, {"source_url", "source_url"}, {"version", "version"},
			{"author", "author"}, {"metadata", "metadata"}},
		changed: []string{"id", "title", "source_url", "version", "author", "metadata", "content"},
	},
	{
		table: "ontology_schemas_v2", kind: ChangeKindOntologySchema, blobSafe: true,
		columns: [][2]string{{"name", "name"}, {"version", "version"}, {"is_active", "is_active"}},
		changed: []string{"id", "name", "version", "is_active", "definition", "description"},
	},
}

// EnsureChangeFeed creates the change log and attaches its triggers to every
// logged table that exists. It is idempotent and cheap after the first call on
// a store.
//
// Tables are attached only if they exist, because the graph store can be used
// without the core tables (the PostgreSQL graph tests do exactly that). A
// table created later is attached by the next EnsureChangeFeed on a fresh
// store; pkg/cortexdb creates every logged table before calling it.
func (g *GraphStore) EnsureChangeFeed(ctx context.Context) error {
	g.feed.mu.Lock()
	installed := g.feed.installed
	g.feed.mu.Unlock()
	if installed {
		return nil
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	if err := g.installChangeFeed(ctx); err != nil {
		return fmt.Errorf("cortexdb/graph: install change feed: %w", err)
	}
	g.feed.mu.Lock()
	g.feed.installed = true
	g.feed.mu.Unlock()
	return nil
}

// DisableChangeFeed removes the triggers, leaving the log and its cursor in
// place. Writes stop being logged until EnsureChangeFeed runs again, and a
// consumer resuming across that window cannot know what it missed — which is
// why this is a deliberate call and not a configuration default.
func (g *GraphStore) DisableChangeFeed(ctx context.Context) error {
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, t := range feedTables {
		for _, stmt := range g.dropFeedTriggerSQL(t) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("drop change trigger on %s: %w", t.table, err)
			}
		}
	}
	if _, err := g.txExec(ctx, tx, `DELETE FROM change_log_meta WHERE meta_key = 'version'`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	g.feed.mu.Lock()
	g.feed.installed = false
	g.feed.mu.Unlock()
	return nil
}

func (g *GraphStore) isPostgres() bool { return g.dialect.Kind() == sqldialect.Postgres }

func (g *GraphStore) installChangeFeed(ctx context.Context) error {
	// Read first. Every Open calls this, and on SQLite a write — even one
	// that changes nothing — is what makes a concurrent read-then-write
	// transaction elsewhere fail; an installed feed needs no write at all.
	if current, err := g.changeFeedCurrent(ctx); err != nil || current {
		return err
	}
	if g.isPostgres() {
		return g.installChangeFeedPostgres(ctx)
	}
	return g.installChangeFeedSQLite(ctx)
}

// changeFeedCurrent reports, by reading only, whether the installed triggers
// are this version's and cover every logged table that exists.
func (g *GraphStore) changeFeedCurrent(ctx context.Context) (bool, error) {
	var metaExists bool
	if g.isPostgres() {
		if err := g.db.QueryRowContext(ctx, `SELECT to_regclass(quote_ident(current_schema()) || '.change_log_meta') IS NOT NULL`).Scan(&metaExists); err != nil {
			return false, err
		}
	} else {
		var n int
		if err := g.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'change_log_meta'`).Scan(&n); err != nil {
			return false, err
		}
		metaExists = n > 0
	}
	if !metaExists {
		return false, nil
	}
	tables, err := g.feedTablesPresent(ctx, g.db)
	if err != nil {
		return false, err
	}
	var have string
	err = g.queryRow(ctx, `SELECT meta_value FROM change_log_meta WHERE meta_key = 'version'`).Scan(&have)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return have == changeFeedVersion+":"+feedTableNames(tables), nil
}

// feedTablesPresent reports which logged tables exist.
func (g *GraphStore) feedTablesPresent(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) ([]feedTable, error) {
	out := make([]feedTable, 0, len(feedTables))
	for _, t := range feedTables {
		var present bool
		var err error
		if g.isPostgres() {
			err = q.QueryRowContext(ctx, `SELECT to_regclass(quote_ident(current_schema()) || '.' || $1) IS NOT NULL`, t.table).Scan(&present)
		} else {
			var n int
			err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, t.table).Scan(&n)
			present = n > 0
		}
		if err != nil {
			return nil, err
		}
		if present {
			out = append(out, t)
		}
	}
	return out, nil
}

func (g *GraphStore) installChangeFeedSQLite(ctx context.Context) error {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// The table first: it is a write when the log is new, which takes the
	// write lock before anything is read, so two processes installing at once
	// queue rather than both deciding to install.
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS change_log (
			-- The rowid itself, not AUTOINCREMENT: the writer lock already
			-- makes it increase in commit order, and AUTOINCREMENT's extra
			-- sqlite_sequence write was a page per transaction for nothing.
			-- Pruning never removes the newest row, which is what keeps a
			-- rowid from being handed out twice.
			seq INTEGER PRIMARY KEY,
			at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			kind TEXT NOT NULL,
			op TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			reason TEXT,
			producer TEXT,
			before_state TEXT,
			after_state TEXT
		);
		CREATE TABLE IF NOT EXISTS change_log_meta (
			meta_key TEXT PRIMARY KEY,
			meta_value TEXT NOT NULL
		);
		INSERT INTO change_log_meta (meta_key, meta_value) VALUES ('pruned_through', '0')
			ON CONFLICT(meta_key) DO NOTHING;
	`); err != nil {
		return err
	}
	tables, err := g.feedTablesPresent(ctx, tx)
	if err != nil {
		return err
	}
	want := changeFeedVersion + ":" + feedTableNames(tables)
	var have string
	switch err := tx.QueryRowContext(ctx, `SELECT meta_value FROM change_log_meta WHERE meta_key = 'version'`).Scan(&have); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	}
	if have == want {
		return tx.Commit()
	}
	for _, t := range tables {
		for _, stmt := range g.dropFeedTriggerSQL(t) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		for _, stmt := range sqliteFeedTriggers(g.dialect, t) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("trigger on %s: %w", t.table, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO change_log_meta (meta_key, meta_value) VALUES ('version', ?)
		ON CONFLICT(meta_key) DO UPDATE SET meta_value = excluded.meta_value`, want); err != nil {
		return err
	}
	return tx.Commit()
}

func feedTableNames(tables []feedTable) string {
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = t.table
	}
	return strings.Join(names, ",")
}

func (g *GraphStore) dropFeedTriggerSQL(t feedTable) []string {
	if g.isPostgres() {
		return []string{fmt.Sprintf(`DROP TRIGGER IF EXISTS change_log_%[1]s ON %[1]s`, t.table)}
	}
	return []string{
		fmt.Sprintf(`DROP TRIGGER IF EXISTS change_log_%s_ins`, t.table),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS change_log_%s_upd`, t.table),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS change_log_%s_del`, t.table),
	}
}

// numericFeedColumns are summarized as numbers; every other column as text.
var numericFeedColumns = map[string]bool{"weight": true, "inferred": true, "version": true, "is_active": true}

// summaryArgs is the key/value list a JSON object builder takes for one row.
//
// On SQLite the text columns of a blobSafe table are CAST to TEXT first. A
// column's declared type does not bind what a row holds there, and core writes
// metadata through a []byte parameter, which SQLite stores as a BLOB;
// json_object refuses a BLOB outright ("JSON cannot hold BLOB values"), and
// since the trigger runs inside the write, the refusal would fail the write
// itself. The graph tables are written only through strings and skip the
// CAST: every expression in a trigger is compiled into every statement that
// can fire it, so the graph's hot write paths pay for each one.
func (t feedTable) summaryArgs(d sqldialect.Dialect, row string) string {
	text := func(col string) string {
		if d.Kind() == sqldialect.Postgres || numericFeedColumns[col] || !t.blobSafe {
			return row + "." + col
		}
		return "CAST(" + row + "." + col + " AS TEXT)"
	}
	parts := make([]string, 0, 2*(len(t.columns)+len(t.preview)))
	for _, c := range t.columns {
		parts = append(parts, "'"+c[0]+"'", text(c[1]))
	}
	for _, c := range t.preview {
		parts = append(parts, "'"+c[0]+"'", "substr("+text(c[1])+", 1, 200)")
	}
	return strings.Join(parts, ", ")
}

func sqliteFeedTriggers(d sqldialect.Dialect, t feedTable) []string {
	notMirror := func(row string) string {
		if t.mirror == nil {
			return ""
		}
		return "NOT " + t.mirror(d, row)
	}
	when := func(conds ...string) string {
		kept := make([]string, 0, len(conds))
		for _, c := range conds {
			if c != "" {
				kept = append(kept, "("+c+")")
			}
		}
		if len(kept) == 0 {
			return ""
		}
		return " WHEN " + strings.Join(kept, " AND ")
	}
	changed := make([]string, len(t.changed))
	for i, c := range t.changed {
		changed[i] = "OLD." + c + " IS NOT NEW." + c
	}
	// The reason and producer of a node or edge change are not looked up
	// here, though the history row that holds them was written moments
	// before in the same transaction: SQLite compiles a trigger's body into
	// every statement that can fire it, each time that statement is
	// prepared, and two correlated subqueries made every graph upsert pay
	// for them whether or not it changed anything. Changes reads them back
	// from the history instead; see lookupInvalidations.
	reason, producer := "NULL", "NULL"
	return []string{
		fmt.Sprintf(`CREATE TRIGGER change_log_%[1]s_ins AFTER INSERT ON %[1]s%[2]s BEGIN
			INSERT INTO change_log (kind, op, entity_id, after_state)
			VALUES ('%[3]s', 'insert', NEW.id, json_object(%[4]s));
		END`, t.table, when(notMirror("NEW")), t.kind, t.summaryArgs(d, "NEW")),
		fmt.Sprintf(`CREATE TRIGGER change_log_%[1]s_upd AFTER UPDATE ON %[1]s%[2]s BEGIN
			INSERT INTO change_log (kind, op, entity_id, reason, producer, before_state, after_state)
			VALUES ('%[3]s', 'update', NEW.id, %[4]s, %[5]s, json_object(%[6]s), json_object(%[7]s));
		END`, t.table, when(notMirror("NEW"), strings.Join(changed, " OR ")), t.kind, reason, producer,
			t.summaryArgs(d, "OLD"), t.summaryArgs(d, "NEW")),
		fmt.Sprintf(`CREATE TRIGGER change_log_%[1]s_del AFTER DELETE ON %[1]s%[2]s BEGIN
			INSERT INTO change_log (kind, op, entity_id, reason, producer, before_state)
			VALUES ('%[3]s', 'delete', OLD.id, %[4]s, %[5]s, json_object(%[6]s));
		END`, t.table, when(notMirror("OLD")), t.kind, reason, producer, t.summaryArgs(d, "OLD")),
	}
}

// pgLockKeys are the two int4 keys of the advisory lock that orders commits.
// The second is the schema, so two brains in one database — the test suites
// run one per test — do not serialize each other's commits.
func pgLockKeys(schema string) (int32, int32) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(schema))
	return 0x43445846, int32(h.Sum32()) // "CDXF"
}

func pgIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func (g *GraphStore) installChangeFeedPostgres(ctx context.Context) error {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var schema string
	if err := tx.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	k1, k2 := pgLockKeys(schema)
	// Installation is serialized per schema: CREATE OR REPLACE FUNCTION and
	// CREATE TRIGGER race each other across sessions ("tuple concurrently
	// updated"), and a process opening the brain while another does is the
	// normal case, not an edge.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, k1, k2+1); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS change_log (
			rid BIGSERIAL PRIMARY KEY,
			seq BIGINT UNIQUE,
			at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
			kind TEXT NOT NULL,
			op TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			reason TEXT,
			producer TEXT,
			before_state TEXT,
			after_state TEXT
		)`); err != nil {
		return err
	}
	for _, stmt := range []string{
		`CREATE SEQUENCE IF NOT EXISTS change_log_seq`,
		`CREATE INDEX IF NOT EXISTS idx_change_log_at ON change_log(at)`,
		`CREATE TABLE IF NOT EXISTS change_log_meta (meta_key TEXT PRIMARY KEY, meta_value TEXT NOT NULL)`,
		`INSERT INTO change_log_meta (meta_key, meta_value) VALUES ('pruned_through', '0') ON CONFLICT (meta_key) DO NOTHING`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	tables, err := g.feedTablesPresent(ctx, tx)
	if err != nil {
		return err
	}
	want := changeFeedVersion + ":" + feedTableNames(tables)
	var have string
	switch err := tx.QueryRowContext(ctx, `SELECT meta_value FROM change_log_meta WHERE meta_key = 'version'`).Scan(&have); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	}
	if have == want {
		return tx.Commit()
	}

	s := pgIdent(schema)
	stmts := []string{
		// Returns false for anything that is not a JSON object with a true
		// "rdf" flag, including text that is not JSON at all: a malformed
		// properties column must cost its row an event, never fail the write.
		fmt.Sprintf(`CREATE OR REPLACE FUNCTION %[1]s.change_log_rdf_flag(p text) RETURNS boolean
			LANGUAGE plpgsql IMMUTABLE AS $f$
			BEGIN
				IF p IS NULL OR position('"rdf"' in p) = 0 THEN RETURN false; END IF;
				RETURN COALESCE((p::jsonb ->> 'rdf') IN ('true', '1'), false);
			EXCEPTION WHEN others THEN
				RETURN false;
			END $f$`, s),
		fmt.Sprintf(`CREATE OR REPLACE FUNCTION %[1]s.change_log_assign_seq() RETURNS trigger
			LANGUAGE plpgsql AS $f$
			BEGIN
				PERFORM pg_advisory_xact_lock(%[2]d, %[3]d);
				UPDATE %[1]s.change_log SET seq = nextval('%[4]s.change_log_seq') WHERE rid = NEW.rid;
				RETURN NULL;
			END $f$`, s, k1, k2, strings.ReplaceAll(s, `'`, `''`)),
		`DROP TRIGGER IF EXISTS change_log_assign_seq ON change_log`,
		`CREATE CONSTRAINT TRIGGER change_log_assign_seq AFTER INSERT ON change_log
			DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION change_log_assign_seq()`,
	}
	for _, t := range tables {
		stmts = append(stmts, postgresFeedFunction(g.dialect, s, t))
		stmts = append(stmts, g.dropFeedTriggerSQL(t)...)
		stmts = append(stmts, fmt.Sprintf(`CREATE TRIGGER change_log_%[1]s AFTER INSERT OR UPDATE OR DELETE ON %[1]s
			FOR EACH ROW EXECUTE FUNCTION change_log_%[1]s()`, t.table))
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w: %s", err, firstLine(stmt))
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO change_log_meta (meta_key, meta_value) VALUES ('version', $1)
		ON CONFLICT (meta_key) DO UPDATE SET meta_value = excluded.meta_value`, want); err != nil {
		return err
	}
	return tx.Commit()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func postgresFeedFunction(d sqldialect.Dialect, schema string, t feedTable) string {
	filter := func(row string) string {
		if t.mirror == nil {
			return ""
		}
		cond := strings.ReplaceAll(t.mirror(d, row), "change_log_rdf_flag(", schema+".change_log_rdf_flag(")
		return fmt.Sprintf("IF %s THEN RETURN NULL; END IF;", cond)
	}
	oldCols := make([]string, len(t.changed))
	newCols := make([]string, len(t.changed))
	for i, c := range t.changed {
		oldCols[i] = "OLD." + c
		newCols[i] = "NEW." + c
	}
	producerGUC := "NULLIF(current_setting('cortexdb.producer', true), '')"
	history := ""
	if t.history != "" {
		history = fmt.Sprintf(`SELECT h.reason, h.producer INTO r, p FROM %s.%s h
			WHERE h.id = OLD.id AND h.valid_from IS NOT DISTINCT FROM OLD.valid_from LIMIT 1;`, schema, t.history)
	}
	return fmt.Sprintf(`CREATE OR REPLACE FUNCTION %[1]s.change_log_%[2]s() RETURNS trigger
		LANGUAGE plpgsql AS $f$
		DECLARE
			r text;
			p text;
		BEGIN
			IF TG_OP = 'INSERT' THEN
				%[4]s
				INSERT INTO %[1]s.change_log (kind, op, entity_id, producer, after_state)
				VALUES ('%[3]s', 'insert', NEW.id, %[7]s, json_build_object(%[8]s)::text);
			ELSIF TG_OP = 'UPDATE' THEN
				%[4]s
				IF ROW(%[5]s) IS NOT DISTINCT FROM ROW(%[6]s) THEN RETURN NULL; END IF;
				%[10]s
				INSERT INTO %[1]s.change_log (kind, op, entity_id, reason, producer, before_state, after_state)
				VALUES ('%[3]s', 'update', NEW.id, r, COALESCE(p, %[7]s), json_build_object(%[9]s)::text, json_build_object(%[8]s)::text);
			ELSE
				%[11]s
				%[10]s
				INSERT INTO %[1]s.change_log (kind, op, entity_id, reason, producer, before_state)
				VALUES ('%[3]s', 'delete', OLD.id, r, COALESCE(p, %[7]s), json_build_object(%[9]s)::text);
			END IF;
			RETURN NULL;
		END $f$`,
		schema, t.table, t.kind, filter("NEW"),
		strings.Join(oldCols, ", "), strings.Join(newCols, ", "),
		producerGUC, t.summaryArgs(d, "NEW"), t.summaryArgs(d, "OLD"), history, filter("OLD"))
}

// --- reading ---------------------------------------------------------------

// ChangesHead is the highest seq committed so far, including events since
// pruned. A consumer that wants only what happens from now on starts here.
func (g *GraphStore) ChangesHead(ctx context.Context) (int64, error) {
	if err := g.EnsureChangeFeed(ctx); err != nil {
		return 0, err
	}
	var head int64
	if err := g.queryRow(ctx, `SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&head); err != nil {
		return 0, err
	}
	pruned, err := g.prunedThrough(ctx, g.db)
	if err != nil {
		return 0, err
	}
	if pruned > head {
		head = pruned
	}
	return head, nil
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (g *GraphStore) prunedThrough(ctx context.Context, q rowQuerier) (int64, error) {
	var raw string
	err := q.QueryRowContext(ctx, g.dialect.Rebind(`SELECT meta_value FROM change_log_meta WHERE meta_key = 'pruned_through'`)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
}

// Changes returns up to limit events committed after the cursor, in commit
// order. Pass the Seq of the last event handled as the next cursor; zero
// starts from the beginning of what is retained.
//
// The events and the prune mark are read in one snapshot, so a prune racing
// the read cannot remove events between the check and the read and leave a
// hole the caller would never know about.
func (g *GraphStore) Changes(ctx context.Context, after int64, limit int) ([]ChangeEvent, error) {
	if err := g.EnsureChangeFeed(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1000
	}
	opts := &sql.TxOptions{ReadOnly: true}
	if g.isPostgres() {
		opts.Isolation = sql.LevelRepeatableRead
	}
	tx, err := g.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	at := "at"
	if g.isPostgres() {
		at = `to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')`
	}
	rows, err := tx.QueryContext(ctx, g.dialect.Rebind(`SELECT seq, `+at+`, kind, op, entity_id,
		COALESCE(reason, ''), COALESCE(producer, ''), before_state, after_state
		FROM change_log WHERE seq > ? ORDER BY seq LIMIT ?`), after, limit)
	if err != nil {
		return nil, err
	}
	events := make([]ChangeEvent, 0)
	for rows.Next() {
		var ev ChangeEvent
		var atText string
		var before, afterState sql.NullString
		if err := rows.Scan(&ev.Seq, &atText, &ev.Kind, &ev.Op, &ev.ID, &ev.Reason, &ev.Producer, &before, &afterState); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ev.At, _ = time.Parse(time.RFC3339Nano, atText)
		ev.Before = normalizeChangeState(before)
		ev.After = normalizeChangeState(afterState)
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	if !g.isPostgres() {
		if err := g.lookupInvalidations(ctx, tx, events); err != nil {
			return nil, err
		}
	}
	for i := range events {
		events[i].Op = refineChangeOp(events[i].Op, events[i].Reason)
	}

	pruned, err := g.prunedThrough(ctx, tx)
	if err != nil {
		return nil, err
	}
	if after < pruned {
		return nil, &ChangesPrunedError{After: after, PrunedThrough: pruned}
	}
	return events, nil
}

// lookupInvalidations fills in the reason and producer of node and edge
// updates and deletes from the graph history, which the SQLite triggers leave
// to the reader. The archived copy of the version a change replaced carries
// that version's own valid_from, which is unique per id, so it identifies the
// history row the change wrote. A row since purged from history leaves the
// event with no reason, which is what the store then knows.
func (g *GraphStore) lookupInvalidations(ctx context.Context, tx *sql.Tx, events []ChangeEvent) error {
	for i := range events {
		ev := &events[i]
		var history string
		switch ev.Kind {
		case ChangeKindNode:
			history = "graph_node_history"
		case ChangeKindEdge:
			history = "graph_edge_history"
		default:
			continue
		}
		if ev.Reason != "" || len(ev.Before) == 0 || (ev.Op != ChangeOpUpdate && ev.Op != ChangeOpDelete) {
			continue
		}
		var before struct {
			ValidFrom *string `json:"valid_from"`
		}
		if err := json.Unmarshal(ev.Before, &before); err != nil {
			continue
		}
		var validFrom any
		if before.ValidFrom != nil {
			validFrom = *before.ValidFrom
		}
		var reason, producer string
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(reason, ''), COALESCE(producer, '') FROM `+history+
			` WHERE id = ? AND valid_from IS ? ORDER BY rowid DESC LIMIT 1`, ev.ID, validFrom).Scan(&reason, &producer)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		ev.Reason = reason
		if ev.Producer == "" {
			ev.Producer = producer
		}
	}
	return nil
}

// refineChangeOp names an update or delete by the reason the graph history
// recorded for it.
func refineChangeOp(op, reason string) string {
	switch {
	case reason == ReasonMerged && (op == ChangeOpUpdate || op == ChangeOpDelete):
		return ChangeOpMerge
	case reason == ReasonSuperseded && op == ChangeOpUpdate:
		return ChangeOpSupersede
	}
	return op
}

// normalizeChangeState decodes a summary and re-embeds the JSON-valued
// columns, which SQLite's json_object stores as strings, as JSON.
func normalizeChangeState(raw sql.NullString) json.RawMessage {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	var fields map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw.String))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return json.RawMessage(raw.String)
	}
	for _, key := range []string{"properties", "metadata"} {
		text, ok := fields[key].(string)
		if !ok {
			continue
		}
		var embedded any
		inner := json.NewDecoder(strings.NewReader(text))
		inner.UseNumber()
		if err := inner.Decode(&embedded); err == nil {
			fields[key] = embedded
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return json.RawMessage(raw.String)
	}
	return out
}

// SubscribeOptions tunes SubscribeChanges.
type SubscribeOptions struct {
	// BatchSize caps the events handed to one call of the handler. Default 500.
	BatchSize int
	// PollInterval is how long an idle subscription waits before looking
	// again. Default 25ms. NotifyChanges cuts the wait short.
	PollInterval time.Duration
}

// SubscribeChanges delivers every event committed after the cursor, in commit
// order, to handle, and keeps doing so as new writes commit — until ctx is
// done or handle returns an error, which is returned.
//
// Delivery is after commit by construction: the subscription reads the
// committed log, so there is nothing to deliver for a write that has not
// committed and never anything for one that rolled back. It is at-least-once
// across restarts and exactly-once within one subscription: the cursor
// advances past a batch only after handle returned nil for it, so a consumer
// that persists the last Seq it handled resumes exactly where it stopped.
func (g *GraphStore) SubscribeChanges(ctx context.Context, after int64, opts SubscribeOptions, handle func([]ChangeEvent) error) error {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 25 * time.Millisecond
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	cursor := after
	for {
		wake := g.feed.wakeChan()
		events, err := g.Changes(ctx, cursor, opts.BatchSize)
		if err != nil {
			return err
		}
		if len(events) > 0 {
			if err := handle(events); err != nil {
				return err
			}
			cursor = events[len(events)-1].Seq
			if len(events) == opts.BatchSize {
				continue
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(opts.PollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		case <-wake:
		}
	}
}

// PruneChangesReport says what PruneChanges removed.
type PruneChangesReport struct {
	Removed       int64 `json:"removed"`
	PrunedThrough int64 `json:"pruned_through"`
}

// PruneChanges applies a retention policy: it removes events older than
// MaxAge and any beyond the newest MaxRows. A zero field is no limit.
func (g *GraphStore) PruneChanges(ctx context.Context, retention ChangeFeedRetention) (*PruneChangesReport, error) {
	if err := g.EnsureChangeFeed(ctx); err != nil {
		return nil, err
	}
	// Decide by reading whether there is anything to prune, and write only
	// if there is; see installChangeFeed for why a needless write costs more
	// than its own time on SQLite.
	pruned, cutoff, err := g.pruneCutoff(ctx, g.db, retention)
	if err != nil {
		return nil, err
	}
	if cutoff <= pruned {
		return &PruneChangesReport{PrunedThrough: pruned}, nil
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Taking the write lock first on SQLite: the prune mark is read and then
	// written, and a deferred transaction that reads before its first write
	// can be refused the upgrade.
	if _, err := g.txExec(ctx, tx, `UPDATE change_log_meta SET meta_value = meta_value WHERE meta_key = 'pruned_through'`); err != nil {
		return nil, err
	}
	pruned, cutoff, err = g.pruneCutoff(ctx, tx, retention)
	if err != nil {
		return nil, err
	}
	report := &PruneChangesReport{PrunedThrough: pruned}
	if cutoff <= pruned {
		return report, tx.Commit()
	}
	res, err := g.txExec(ctx, tx, `DELETE FROM change_log WHERE seq <= ?`, cutoff)
	if err != nil {
		return nil, err
	}
	report.Removed, _ = res.RowsAffected()
	report.PrunedThrough = cutoff
	if _, err := g.txExec(ctx, tx, `UPDATE change_log_meta SET meta_value = ? WHERE meta_key = 'pruned_through'`,
		strconv.FormatInt(cutoff, 10)); err != nil {
		return nil, err
	}
	return report, tx.Commit()
}

// pruneCutoff is the prune mark now and the seq retention would prune through.
func (g *GraphStore) pruneCutoff(ctx context.Context, q rowQuerier, retention ChangeFeedRetention) (int64, int64, error) {
	pruned, err := g.prunedThrough(ctx, q)
	if err != nil {
		return 0, 0, err
	}
	cutoff := pruned
	if retention.MaxAge > 0 {
		var bound sql.NullInt64
		limit := any(time.Now().Add(-retention.MaxAge).UTC())
		if !g.isPostgres() {
			limit = time.Now().Add(-retention.MaxAge).UTC().Format("2006-01-02T15:04:05.000Z")
		}
		if err := q.QueryRowContext(ctx, g.dialect.Rebind(`SELECT MAX(seq) FROM change_log WHERE at < ?`), limit).Scan(&bound); err != nil {
			return 0, 0, err
		}
		if bound.Valid && bound.Int64 > cutoff {
			cutoff = bound.Int64
		}
	}
	if retention.MaxRows > 0 {
		var bound sql.NullInt64
		if err := q.QueryRowContext(ctx, g.dialect.Rebind(`SELECT seq FROM change_log WHERE seq IS NOT NULL ORDER BY seq DESC LIMIT 1 OFFSET ?`),
			retention.MaxRows).Scan(&bound); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, 0, err
		}
		if bound.Valid && bound.Int64 > cutoff {
			cutoff = bound.Int64
		}
	}
	// The newest event always stays. On SQLite seq is the rowid, which the
	// database hands out as one more than the largest present: emptying the
	// table would start it again at 1, under every cursor already issued.
	var newest sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MAX(seq) FROM change_log`).Scan(&newest); err != nil {
		return 0, 0, err
	}
	if newest.Valid && cutoff >= newest.Int64 {
		cutoff = newest.Int64 - 1
	}
	return pruned, cutoff, nil
}
