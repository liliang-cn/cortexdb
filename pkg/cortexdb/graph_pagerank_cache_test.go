package cortexdb

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
)

func rankTool(t *testing.T, db *DB, req ToolRankGraphNodesRequest) *ToolRankGraphNodesResponse {
	t.Helper()
	res, err := db.GraphRAGTools().RankGraphNodes(context.Background(), req)
	if err != nil {
		t.Fatalf("rank_graph_nodes: %v", err)
	}
	return res
}

func TestRankGraphNodesCachesAndMatchesAFreshRanking(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()

	first := rankTool(t, db, ToolRankGraphNodesRequest{})
	if first.Cached || first.StaleReason != "missing" {
		t.Fatalf("first call should compute (missing cache), got cached=%v reason=%q", first.Cached, first.StaleReason)
	}
	if first.ComputedAt == "" {
		t.Fatalf("computed_at must be set")
	}
	second := rankTool(t, db, ToolRankGraphNodesRequest{})
	if !second.Cached || second.Stale || second.StaleReason != "" {
		t.Fatalf("second call on an unchanged graph should be a fresh cache hit, got %+v", second)
	}
	if second.ComputedAt != first.ComputedAt {
		t.Fatalf("cache hit must report the original computation time")
	}

	// The cached answer is the fresh answer: same nodes, same order, same scores.
	fresh, err := db.GraphPageRank(ctx, GraphPageRankOptions{TopN: 20})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fresh.Nodes, second.Nodes) || fresh.TotalNodes != second.TotalNodes {
		t.Fatalf("cached ranking differs from a fresh one:\ncached %+v\nfresh  %+v", second.Nodes, fresh.Nodes)
	}
	if fresh.Cache != nil {
		t.Fatalf("Cache off must not report cache info")
	}

	// Truncation is reported from the cache the same way.
	top2 := rankTool(t, db, ToolRankGraphNodesRequest{TopN: 2})
	if !top2.Cached || top2.Count != 2 || !top2.Truncated || top2.TotalNodes != 5 {
		t.Fatalf("top 2 from cache: %+v", top2)
	}
}

func TestRankGraphNodesDetectsGraphChanges(t *testing.T) {
	db := hubBrain(t)
	rankTool(t, db, ToolRankGraphNodesRequest{})

	// A new edge makes erin a hub-adjacent node; the cache must notice.
	addAnalyticsEdge(t, db, "entity:bob", "entity:erin")
	res := rankTool(t, db, ToolRankGraphNodesRequest{})
	if res.Cached || res.StaleReason != "graph_changed" {
		t.Fatalf("edge insert: expected recompute for graph_changed, got cached=%v reason=%q", res.Cached, res.StaleReason)
	}
	if again := rankTool(t, db, ToolRankGraphNodesRequest{}); !again.Cached {
		t.Fatalf("recompute must refill the cache")
	}

	// A deleted node is a change too.
	if err := db.Graph().DeleteNode(context.Background(), "entity:erin"); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	stale := rankTool(t, db, ToolRankGraphNodesRequest{AllowStale: true})
	if !stale.Cached || !stale.Stale || stale.StaleReason != "graph_changed" {
		t.Fatalf("allow_stale should serve the old ranking flagged stale, got %+v", stale)
	}
	if stale.TotalNodes != 5 {
		t.Fatalf("a stale answer describes the graph it was computed on: total %d", stale.TotalNodes)
	}
	res = rankTool(t, db, ToolRankGraphNodesRequest{})
	if res.Cached || res.TotalNodes != 4 {
		t.Fatalf("node delete: expected a recompute over 4 nodes, got %+v", res)
	}
}

func TestRankGraphNodesRefreshParametersAgeAndInvalidate(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()
	rankTool(t, db, ToolRankGraphNodesRequest{})

	if res := rankTool(t, db, ToolRankGraphNodesRequest{Refresh: true}); res.Cached || res.StaleReason != "refresh_requested" {
		t.Fatalf("refresh: %+v", res)
	}
	if res := rankTool(t, db, ToolRankGraphNodesRequest{DampingFactor: 0.5}); res.Cached || res.StaleReason != "parameters_changed" {
		t.Fatalf("damping change: %+v", res)
	}
	// Default parameters are normalised, so 0 and the explicit default share a cache.
	rankTool(t, db, ToolRankGraphNodesRequest{})
	if res := rankTool(t, db, ToolRankGraphNodesRequest{Iterations: 100, DampingFactor: 0.85}); !res.Cached {
		t.Fatalf("explicit defaults should hit the cache: %+v", res)
	}

	if err := db.InvalidatePageRankCache(ctx); err != nil {
		t.Fatal(err)
	}
	if res := rankTool(t, db, ToolRankGraphNodesRequest{}); res.Cached || res.StaleReason != "invalidated" {
		t.Fatalf("invalidate: %+v", res)
	}

	time.Sleep(1100 * time.Millisecond)
	if res := rankTool(t, db, ToolRankGraphNodesRequest{MaxAgeSeconds: 1}); res.Cached || res.StaleReason != "max_age" {
		t.Fatalf("max_age: %+v", res)
	}
}

func TestRefreshPageRankCacheOnlyRecomputesWhenStale(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()
	info, err := db.RefreshPageRankCache(ctx, GraphPageRankOptions{}, false)
	if err != nil || info.Cached {
		t.Fatalf("first refresh should compute: %+v %v", info, err)
	}
	info, err = db.RefreshPageRankCache(ctx, GraphPageRankOptions{}, false)
	if err != nil || !info.Cached {
		t.Fatalf("second refresh on an unchanged graph should not recompute: %+v %v", info, err)
	}
	info, err = db.RefreshPageRankCache(ctx, GraphPageRankOptions{}, true)
	if err != nil || info.Cached {
		t.Fatalf("forced refresh should recompute: %+v %v", info, err)
	}

	stop := db.StartPageRankRefresher(ctx, 20*time.Millisecond, GraphPageRankOptions{}, func(err error) { t.Errorf("refresher: %v", err) })
	addAnalyticsEdge(t, db, "entity:bob", "entity:erin")
	deadline := time.Now().Add(3 * time.Second)
	for {
		res := rankTool(t, db, ToolRankGraphNodesRequest{AllowStale: true})
		if res.Cached && !res.Stale {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background refresher never caught up: %+v", res)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
}

func TestRankGraphNodesConcurrentCallsAgree(t *testing.T) {
	db := hubBrain(t)
	var wg sync.WaitGroup
	results := make([]*ToolRankGraphNodesResponse, 8)
	errs := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = db.GraphRAGTools().RankGraphNodes(context.Background(), ToolRankGraphNodesRequest{})
		}(i)
	}
	wg.Wait()
	computed := 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if !reflect.DeepEqual(r.Nodes, results[0].Nodes) {
			t.Fatalf("concurrent calls disagree")
		}
		if !r.Cached {
			computed++
		}
	}
	if computed != 1 {
		t.Fatalf("expected exactly one computation among concurrent callers, got %d", computed)
	}
}

func TestRankGraphNodesOnEmptyGraphCaches(t *testing.T) {
	db := openGraphAnalyticsBrain(t)
	res := rankTool(t, db, ToolRankGraphNodesRequest{})
	if len(res.Nodes) != 0 || res.TotalNodes != 0 {
		t.Fatalf("empty graph: %+v", res)
	}
	if again := rankTool(t, db, ToolRankGraphNodesRequest{}); !again.Cached {
		t.Fatalf("empty ranking should cache too: %+v", again)
	}
}

// A write that bypasses every Go API — raw SQL, as another process or an
// older binary would do it — still marks the cache stale on SQLite: inserts
// move the maximum rowid, and deletes and endpoint rewrites fire triggers kept
// in the file itself.
func TestRankGraphNodesSeesRawSQLWrites(t *testing.T) {
	db := hubBrain(t)
	exec := func(q string) {
		t.Helper()
		if _, err := db.SQL().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	expectRecompute := func(what string) {
		t.Helper()
		if res := rankTool(t, db, ToolRankGraphNodesRequest{}); res.Cached || res.StaleReason != "graph_changed" {
			t.Fatalf("%s: expected a recompute, got cached=%v reason=%q", what, res.Cached, res.StaleReason)
		}
	}
	expectCached := func(what string) {
		t.Helper()
		if res := rankTool(t, db, ToolRankGraphNodesRequest{}); !res.Cached {
			t.Fatalf("%s: expected the cache to hold, got reason=%q", what, res.StaleReason)
		}
	}
	rankTool(t, db, ToolRankGraphNodesRequest{})

	exec(`UPDATE graph_edges SET to_node_id = 'entity:erin' WHERE from_node_id = 'entity:alice' AND to_node_id = 'entity:bob'`)
	expectRecompute("raw endpoint rewrite")

	// Delete the newest edge and insert another: the new row takes the freed
	// maximum rowid, so only the delete trigger can see this.
	exec(`DELETE FROM graph_edges WHERE rowid = (SELECT MAX(rowid) FROM graph_edges)`)
	exec(`INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, weight) VALUES ('raw', 'entity:erin', 'entity:dave', 'knows', 1)`)
	expectRecompute("raw delete + insert")

	// Neither content nor weight is read by PageRank, so neither invalidates it.
	exec(`UPDATE graph_nodes SET content = 'Robert' WHERE id = 'entity:bob'`)
	exec(`UPDATE graph_edges SET weight = 5 WHERE id = 'raw'`)
	exec(`UPDATE graph_edges SET from_node_id = from_node_id WHERE id = 'raw'`)
	expectCached("content, weight and no-op endpoint updates")
}

// The aggregate fingerprint is what PostgreSQL uses; it is exercised here on
// SQLite so its change detection is tested on every run.
func TestGraphTopologyAggregateMovesOnChanges(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()
	fp := func() string {
		s, err := db.graphTopologyAggregate(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	a := fp()
	if b := fp(); a != b {
		t.Fatalf("aggregate unstable: %q vs %q", a, b)
	}
	addAnalyticsEdge(t, db, "entity:bob", "entity:erin")
	b := fp()
	if a == b {
		t.Fatalf("edge insert not seen")
	}
	if _, err := db.SQL().Exec(`UPDATE graph_edges SET weight = 3 WHERE from_node_id = 'entity:bob' AND to_node_id = 'entity:erin'`); err != nil {
		t.Fatal(err)
	}
	if c := fp(); c == b {
		t.Fatalf("reweight not seen")
	}
}
