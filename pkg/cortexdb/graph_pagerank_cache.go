package cortexdb

// A PageRank that is computed once and read many times.
//
// GraphPageRank scans every node and every edge and runs a power iteration on
// each call. That is the right cost for a question asked once; it is the wrong
// cost for rank_graph_nodes, which agents call to orient themselves, often at
// the start of every session, on a graph that has not changed since the last
// time anyone asked. So the scores are written to a side table with the moment
// they were computed and a fingerprint of the topology they were computed on,
// and a cached read is one ORDER BY ... LIMIT over that table.
//
// Why a side table rather than a property on each node: writing a score into
// graph_nodes.properties would be a write to every node, which the temporal
// layer records as a new version of every node — a ranking refresh would
// masquerade as the whole graph having changed — and it would also move the
// very fingerprint that decides whether the ranking is stale.
//
// Staleness is detected, not assumed. Graph writes reach graph_nodes and
// graph_edges through many paths (the facade, graphflow, entity resolution,
// temporal retraction, other processes on the same file, a gRPC peer), so
// hooking each write path in Go would miss some. The hook lives in the
// database instead:
//
//   - On SQLite the fingerprint is the highest rowid of each table plus a
//     one-row counter bumped by triggers. An insert always takes a rowid
//     above the current maximum, so inserts need no trigger and bulk loads
//     pay nothing; the triggers cover what a maximum cannot see — a delete
//     from either table (after which the next insert could reuse the freed
//     maximum) and an update that actually changes an edge's endpoints
//     (entity merges do this). Triggers are part of the file, so every writer
//     fires them, including another process and a binary older than this one.
//     Checking staleness is three index lookups. Node content and edge weight
//     changes do not count: PageRank reads neither.
//   - On PostgreSQL the triggers are not installed, and staleness is a
//     fingerprint of the topology instead — node and edge counts, the edge
//     weight sum, and the latest node update and edge creation, recording and
//     retraction stamps — one aggregate over each table. It can miss an
//     in-place rewrite of an edge's endpoints that leaves count, weight and
//     every stamp unchanged; a caller that knows it did that can call
//     InvalidatePageRankCache or pass Refresh.
//
// Measured on a 2000-node, 8000-edge SQLite graph: the aggregate fingerprint
// cost 4.8ms, most of a PageRank run; the rowid+counter form costs
// microseconds. A first version bumped the counter on every insert too, and
// that made a bulk load of 8000 relations 25% slower — hence rowids for
// inserts and triggers only for the rare writes.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// PageRankCacheMode says whether and how GraphPageRank uses the stored scores.
type PageRankCacheMode string

const (
	// PageRankCacheOff computes the ranking fresh and neither reads nor writes
	// the cache. The zero value, so existing callers see no change.
	PageRankCacheOff PageRankCacheMode = ""
	// PageRankCacheAuto serves the cache when it is fresh and recomputes (and
	// rewrites it) when it is stale or missing. What rank_graph_nodes uses.
	PageRankCacheAuto PageRankCacheMode = "auto"
	// PageRankCacheRefresh always recomputes and rewrites the cache.
	PageRankCacheRefresh PageRankCacheMode = "refresh"
	// PageRankCacheAllowStale serves whatever the cache holds, stale or not,
	// and reports the staleness; it computes only when there is no cache at
	// all. For a caller that would rather have a slightly old answer now.
	PageRankCacheAllowStale PageRankCacheMode = "allow_stale"
)

// PageRankCacheInfo describes the ranking a GraphPageRank answer came from.
type PageRankCacheInfo struct {
	// Cached is true when the answer was read from the cache rather than
	// computed by this call.
	Cached bool `json:"cached"`
	// ComputedAt is when the scores were computed — now, for a fresh
	// computation.
	ComputedAt time.Time `json:"computed_at"`
	// Stale is true when a cached answer no longer matches the graph (or the
	// requested parameters, or MaxAge). Only possible under AllowStale.
	Stale bool `json:"stale,omitempty"`
	// StaleReason says why the cache was judged stale, whether it was then
	// recomputed or served anyway: "graph_changed", "parameters_changed",
	// "max_age", "invalidated", "missing", or "refresh_requested".
	StaleReason string `json:"stale_reason,omitempty"`
}

const (
	pageRankScoresTable = "graph_pagerank_scores"
	pageRankStateTable  = "graph_pagerank_state"
	topologyVersionTbl  = "graph_topology_version"
	pageRankInsertBatch = 200
)

type pageRankState struct {
	iterations  int
	damping     float64
	computedAt  time.Time
	nodeCount   int
	fingerprint string
}

func (db *DB) ensurePageRankCacheSchema(ctx context.Context) error {
	db.pageRankMu.Lock()
	ready := db.pageRankSchemaReady
	db.pageRankMu.Unlock()
	if ready {
		return nil
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS ` + pageRankScoresTable + ` (
			node_id TEXT PRIMARY KEY,
			score DOUBLE PRECISION NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_pagerank_scores_score ON ` + pageRankScoresTable + `(score DESC, node_id)`,
		`CREATE TABLE IF NOT EXISTS ` + pageRankStateTable + ` (
			id INTEGER PRIMARY KEY,
			iterations INTEGER NOT NULL,
			damping DOUBLE PRECISION NOT NULL,
			computed_at TEXT NOT NULL,
			node_count INTEGER NOT NULL,
			fingerprint TEXT NOT NULL
		)`,
	} {
		if _, err := db.exec(ctx, stmt); err != nil {
			return fmt.Errorf("pagerank cache schema: %w", err)
		}
	}
	if db.Dialect().Kind() == sqldialect.SQLite {
		stmts := []string{
			`CREATE TABLE IF NOT EXISTS ` + topologyVersionTbl + ` (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)`,
			`INSERT OR IGNORE INTO ` + topologyVersionTbl + ` (id, version) VALUES (1, 0)`,
		}
		bump := `BEGIN UPDATE ` + topologyVersionTbl + ` SET version = version + 1 WHERE id = 1; END`
		for _, trig := range [][2]string{
			{"graph_topology_nodes_ad", "AFTER DELETE ON graph_nodes"},
			{"graph_topology_edges_ad", "AFTER DELETE ON graph_edges"},
			{"graph_topology_edges_au", "AFTER UPDATE OF from_node_id, to_node_id ON graph_edges " +
				"WHEN OLD.from_node_id IS NOT NEW.from_node_id OR OLD.to_node_id IS NOT NEW.to_node_id"},
		} {
			stmts = append(stmts, `CREATE TRIGGER IF NOT EXISTS `+trig[0]+` `+trig[1]+` `+bump)
		}
		for _, stmt := range stmts {
			if _, err := db.exec(ctx, stmt); err != nil {
				return fmt.Errorf("graph topology version: %w", err)
			}
		}
	}
	db.pageRankMu.Lock()
	db.pageRankSchemaReady = true
	db.pageRankMu.Unlock()
	return nil
}

// graphTopologyFingerprint names the current topology: the trigger-kept
// version counter on SQLite, an aggregate over both tables elsewhere. See the
// file comment for what each catches.
func (db *DB) graphTopologyFingerprint(ctx context.Context) (string, error) {
	if db.Dialect().Kind() == sqldialect.SQLite {
		var v, nodeMax, edgeMax int64
		if err := db.queryRow(ctx, `SELECT
			(SELECT version FROM `+topologyVersionTbl+` WHERE id = 1),
			(SELECT COALESCE(MAX(rowid), 0) FROM graph_nodes),
			(SELECT COALESCE(MAX(rowid), 0) FROM graph_edges)`).Scan(&v, &nodeMax, &edgeMax); err != nil {
			return "", fmt.Errorf("graph topology version: %w", err)
		}
		return fmt.Sprintf("v=%d|n=%d|e=%d", v, nodeMax, edgeMax), nil
	}
	return db.graphTopologyAggregate(ctx)
}

// graphTopologyAggregate summarises graph_nodes and graph_edges in one
// aggregate each.
func (db *DB) graphTopologyAggregate(ctx context.Context) (string, error) {
	var nodeCount int64
	var nodeUpdated sql.NullString
	if err := db.queryRow(ctx, `SELECT COUNT(*), CAST(MAX(updated_at) AS TEXT) FROM graph_nodes`).Scan(&nodeCount, &nodeUpdated); err != nil {
		return "", fmt.Errorf("fingerprint nodes: %w", err)
	}
	var edgeCount int64
	var weightSum sql.NullFloat64
	var created, recorded, retracted sql.NullString
	if err := db.queryRow(ctx, `SELECT COUNT(*), SUM(weight), CAST(MAX(created_at) AS TEXT),
		CAST(MAX(recorded_at) AS TEXT), CAST(MAX(retracted_at) AS TEXT) FROM graph_edges`).
		Scan(&edgeCount, &weightSum, &created, &recorded, &retracted); err != nil {
		return "", fmt.Errorf("fingerprint edges: %w", err)
	}
	return fmt.Sprintf("n=%d|nu=%s|e=%d|w=%.9g|ec=%s|er=%s|ex=%s",
		nodeCount, nodeUpdated.String, edgeCount, weightSum.Float64,
		created.String, recorded.String, retracted.String), nil
}

func (db *DB) readPageRankState(ctx context.Context) (*pageRankState, error) {
	var st pageRankState
	var computed string
	err := db.queryRow(ctx, `SELECT iterations, damping, computed_at, node_count, fingerprint FROM `+pageRankStateTable+` WHERE id = 1`).
		Scan(&st.iterations, &st.damping, &computed, &st.nodeCount, &st.fingerprint)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pagerank cache state: %w", err)
	}
	st.computedAt, _ = time.Parse(time.RFC3339Nano, computed)
	return &st, nil
}

// normalisePageRankParams mirrors the engine's defaults so the cache key is
// the parameters actually used, not the zero values that selected them.
func normalisePageRankParams(iterations int, damping float64) (int, float64) {
	if iterations <= 0 {
		iterations = 100
	}
	if damping <= 0 || damping > 1 {
		damping = 0.85
	}
	return iterations, damping
}

// stalenessOf compares a stored state with the graph and the request.
func stalenessOf(st *pageRankState, fingerprint string, iterations int, damping float64, maxAge time.Duration, now time.Time) string {
	switch {
	case st == nil:
		return "missing"
	case st.fingerprint == "":
		return "invalidated"
	case st.fingerprint != fingerprint:
		return "graph_changed"
	case st.iterations != iterations || st.damping != damping:
		return "parameters_changed"
	case maxAge > 0 && now.Sub(st.computedAt) > maxAge:
		return "max_age"
	}
	return ""
}

// computeAndCachePageRank runs the engine and replaces the cache with its
// answer, all rows and the state in one transaction.
func (db *DB) computeAndCachePageRank(ctx context.Context, iterations int, damping float64, fingerprint string) (int, time.Time, error) {
	scores, err := db.graph.PageRank(ctx, iterations, damping)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("graph pagerank: %w", err)
	}
	now := time.Now().UTC()

	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := db.txExec(ctx, tx, `DELETE FROM `+pageRankScoresTable); err != nil {
		return 0, time.Time{}, fmt.Errorf("clear pagerank cache: %w", err)
	}
	for start := 0; start < len(scores); start += pageRankInsertBatch {
		end := start + pageRankInsertBatch
		if end > len(scores) {
			end = len(scores)
		}
		var b strings.Builder
		b.WriteString(`INSERT INTO ` + pageRankScoresTable + ` (node_id, score) VALUES `)
		args := make([]any, 0, 2*(end-start))
		for i, s := range scores[start:end] {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("(?, ?)")
			args = append(args, s.NodeID, s.Score)
		}
		if _, err := db.txExec(ctx, tx, b.String(), args...); err != nil {
			return 0, time.Time{}, fmt.Errorf("write pagerank cache: %w", err)
		}
	}
	if _, err := db.txExec(ctx, tx, `DELETE FROM `+pageRankStateTable+` WHERE id = 1`); err != nil {
		return 0, time.Time{}, err
	}
	if _, err := db.txExec(ctx, tx, `INSERT INTO `+pageRankStateTable+` (id, iterations, damping, computed_at, node_count, fingerprint) VALUES (1, ?, ?, ?, ?, ?)`,
		iterations, damping, now.Format(time.RFC3339Nano), len(scores), fingerprint); err != nil {
		return 0, time.Time{}, fmt.Errorf("write pagerank cache state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, time.Time{}, err
	}
	return len(scores), now, nil
}

// readCachedPageRank reads the top of the cached ranking, ordered exactly as
// GraphPageRank orders a fresh one (score descending, id ascending).
func (db *DB) readCachedPageRank(ctx context.Context, topN int) ([]rankedScore, error) {
	q := `SELECT node_id, score FROM ` + pageRankScoresTable + ` ORDER BY score DESC, node_id ASC`
	var args []any
	if topN > 0 {
		q += ` LIMIT ?`
		args = append(args, topN)
	}
	rows, err := db.query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("read pagerank cache: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []rankedScore
	for rows.Next() {
		var s rankedScore
		if err := rows.Scan(&s.id, &s.score); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type rankedScore struct {
	id    string
	score float64
}

// graphPageRankCached is GraphPageRank's cache-aware path. It returns the
// head of the ranking (already cut to TopN), the total scored, and how the
// answer was obtained.
func (db *DB) graphPageRankCached(ctx context.Context, opts GraphPageRankOptions) ([]rankedScore, int, PageRankCacheInfo, error) {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, 0, PageRankCacheInfo{}, err
	}
	if err := db.ensurePageRankCacheSchema(ctx); err != nil {
		return nil, 0, PageRankCacheInfo{}, err
	}
	iterations, damping := normalisePageRankParams(opts.Iterations, opts.DampingFactor)

	// One refresh at a time per DB: two callers finding the cache stale at
	// once should not both run the iteration and race to write it.
	db.pageRankRefreshMu.Lock()
	defer db.pageRankRefreshMu.Unlock()

	fingerprint, err := db.graphTopologyFingerprint(ctx)
	if err != nil {
		return nil, 0, PageRankCacheInfo{}, err
	}
	st, err := db.readPageRankState(ctx)
	if err != nil {
		return nil, 0, PageRankCacheInfo{}, err
	}
	reason := stalenessOf(st, fingerprint, iterations, damping, opts.MaxAge, time.Now())
	if opts.Cache == PageRankCacheRefresh {
		reason = "refresh_requested"
	}

	serveCache := reason == "" || (opts.Cache == PageRankCacheAllowStale && st != nil)
	if serveCache {
		head, err := db.readCachedPageRank(ctx, opts.TopN)
		if err != nil {
			return nil, 0, PageRankCacheInfo{}, err
		}
		return head, st.nodeCount, PageRankCacheInfo{
			Cached: true, ComputedAt: st.computedAt, Stale: reason != "", StaleReason: reason,
		}, nil
	}

	total, computedAt, err := db.computeAndCachePageRank(ctx, iterations, damping, fingerprint)
	if err != nil {
		return nil, 0, PageRankCacheInfo{}, err
	}
	head, err := db.readCachedPageRank(ctx, opts.TopN)
	if err != nil {
		return nil, 0, PageRankCacheInfo{}, err
	}
	return head, total, PageRankCacheInfo{ComputedAt: computedAt, StaleReason: reason}, nil
}

// RefreshPageRankCache recomputes the cached ranking if (and only if) it is
// stale or missing, and reports what it found. force recomputes regardless.
// It is the unit a scheduler calls; see StartPageRankRefresher.
func (db *DB) RefreshPageRankCache(ctx context.Context, opts GraphPageRankOptions, force bool) (PageRankCacheInfo, error) {
	mode := PageRankCacheAuto
	if force {
		mode = PageRankCacheRefresh
	}
	opts.Cache = mode
	opts.TopN = 1
	_, _, info, err := db.graphPageRankCached(ctx, opts)
	return info, err
}

// InvalidatePageRankCache marks the cached ranking stale, so the next cached
// read recomputes it. For a caller that changed the graph in a way the
// fingerprint cannot see.
func (db *DB) InvalidatePageRankCache(ctx context.Context) error {
	if err := db.ensurePageRankCacheSchema(ctx); err != nil {
		return err
	}
	_, err := db.exec(ctx, `UPDATE `+pageRankStateTable+` SET fingerprint = '' WHERE id = 1`)
	return err
}

// StartPageRankRefresher keeps the cached ranking warm: every interval it
// checks the fingerprint and recomputes only when the graph has changed. It
// returns a stop function; stopping, or cancelling ctx, ends it. Errors are
// passed to onError when it is non-nil and otherwise dropped — a failed
// background refresh leaves the next rank_graph_nodes call to recompute.
func (db *DB) StartPageRankRefresher(ctx context.Context, interval time.Duration, opts GraphPageRankOptions, onError func(error)) (stop func()) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := db.RefreshPageRankCache(ctx, opts, false); err != nil && onError != nil && ctx.Err() == nil {
					onError(err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
