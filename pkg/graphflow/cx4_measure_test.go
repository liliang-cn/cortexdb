package graphflow

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// TestMeasureGraphAnalyticsAt2000Nodes is the measurement behind the
// PageRank cache and the community hierarchy, not a regression gate: it
// prints numbers and asserts only that the paths ran. Opt in with
// CORTEXDB_MEASURE=1.
//
// The graph is 2000 entities planted as 4 groups × 5 subgroups × 100, with
// random edges dense inside a subgroup, sparser across subgroups of one group,
// sparse across groups — seeded, so every run measures the same graph.
func TestMeasureGraphAnalyticsAt2000Nodes(t *testing.T) {
	if os.Getenv("CORTEXDB_MEASURE") == "" {
		t.Skip("set CORTEXDB_MEASURE=1 to run the measurement")
	}
	ctx := context.Background()
	db := openHierarchyDB(t)
	tools := db.GraphRAGTools()

	const groups, subs, size = 4, 5, 100
	name := func(g, s, i int) string { return fmt.Sprintf("E%d_%d_%03d", g, s, i) }
	var ents []cortexdb.ToolEntityInput
	for g := 0; g < groups; g++ {
		for s := 0; s < subs; s++ {
			for i := 0; i < size; i++ {
				ents = append(ents, cortexdb.ToolEntityInput{Name: name(g, s, i), Type: "concept"})
			}
		}
	}
	for start := 0; start < len(ents); start += 500 {
		if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: ents[start:min(start+500, len(ents))]}); err != nil {
			t.Fatal(err)
		}
	}
	rng := rand.New(rand.NewSource(42))
	var rels []cortexdb.ToolRelationInput
	type key struct{ a, b string }
	seen := map[key]bool{}
	add := func(a, b string) {
		if a == b || seen[key{a, b}] || seen[key{b, a}] {
			return
		}
		seen[key{a, b}] = true
		rels = append(rels, cortexdb.ToolRelationInput{From: a, To: b, Type: "related_to"})
	}
	all := func(fn func(g, s, i int)) {
		for g := 0; g < groups; g++ {
			for s := 0; s < subs; s++ {
				for i := 0; i < size; i++ {
					fn(g, s, i)
				}
			}
		}
	}
	all(func(g, s, i int) {
		for k := 0; k < 4; k++ { // ~4 edges out inside the subgroup
			add(name(g, s, i), name(g, s, rng.Intn(size)))
		}
		if rng.Float64() < 0.5 { // one edge to another subgroup of the same group, half the time
			add(name(g, s, i), name(g, (s+1+rng.Intn(subs-1))%subs, rng.Intn(size)))
		}
		if rng.Float64() < 0.05 { // rare edge to another group
			add(name(g, s, i), name((g+1+rng.Intn(groups-1))%groups, rng.Intn(subs), rng.Intn(size)))
		}
	})
	for start := 0; start < len(rels); start += 1000 {
		if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: rels[start:min(start+1000, len(rels))]}); err != nil {
			t.Fatal(err)
		}
	}
	stats, _ := db.Graph().GetGraphStatistics(ctx)
	t.Logf("graph: %d entities requested, %d relations requested; store has %+v", len(ents), len(rels), stats)

	// --- PageRank: uncached (the old path), recompute+write, cached read.
	timeIt := func(n int, fn func()) (p50, p95 time.Duration) {
		d := make([]time.Duration, n)
		for i := range d {
			start := time.Now()
			fn()
			d[i] = time.Since(start)
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[n/2], d[(n*95+99)/100-1]
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	u50, u95 := timeIt(30, func() {
		_, err := db.GraphPageRank(ctx, cortexdb.GraphPageRankOptions{TopN: 20})
		must(err)
	})
	r50, r95 := timeIt(30, func() {
		res, err := tools.RankGraphNodes(ctx, cortexdb.ToolRankGraphNodesRequest{Refresh: true})
		must(err)
		if res.Cached {
			t.Fatal("refresh served cache")
		}
	})
	c50, c95 := timeIt(200, func() {
		res, err := tools.RankGraphNodes(ctx, cortexdb.ToolRankGraphNodesRequest{})
		must(err)
		if !res.Cached {
			t.Fatal("expected cache hit")
		}
	})
	t.Logf("rank_graph_nodes top20 uncached (Cache off, pre-change path): p50=%v p95=%v (n=30)", u50, u95)
	t.Logf("rank_graph_nodes refresh (compute + write cache):             p50=%v p95=%v (n=30)", r50, r95)
	t.Logf("rank_graph_nodes cached (fingerprint + LIMIT read + labels):   p50=%v p95=%v (n=200)", c50, c95)

	// --- Hierarchical communities.
	start := time.Now()
	rep, err := BuildCommunityHierarchy(ctx, db, HierarchyOptions{})
	must(err)
	t.Logf("hierarchy (no model) built in %v over %d entities / %d edges; %d reports", time.Since(start), rep.EntityCount, rep.EdgeCount, len(rep.Communities))
	for _, l := range rep.Levels {
		t.Logf("  level %d: %d communities, %d summarized (>=3 entities), largest %d, modularity %.4f", l.Level, l.Communities, l.Summarized, l.LargestCommunity, l.Modularity)
	}

	// --- GlobalSearch end to end through the tool, deterministic generator.
	llm := &countingLLM{}
	built, err := callBuildHierarchy(ctx, db, llm, BuildHierarchyToolRequest{})
	must(err)
	t.Logf("build_community_hierarchy with mock model: %d reports, %d model calls (leaf %d, parent %d)", built.ReportsWritten, built.ModelCalls, llm.leaf, llm.parent)
	for _, lvl := range built.Levels {
		l := lvl.Level
		before := llm.mapCalls
		start := time.Now()
		res, err := callGlobalSearch(ctx, db, llm, GlobalSearchToolRequest{Query: "What are the main themes of this knowledge base?", Level: &l})
		must(err)
		t.Logf("global_search level %d: %d reports -> %d map batches, 1 reduce, %d supporting points, %v; answer=%q",
			l, res.CommunitiesUsed, llm.mapCalls-before, len(res.SupportingPoints), time.Since(start), res.Answer)
	}
	res, err := callGlobalSearch(ctx, db, nil, GlobalSearchToolRequest{Query: "E2_3_050"})
	must(err)
	t.Logf("global_search no model (default level %d): %d reports, top point %.80q", res.Level, res.CommunitiesUsed, res.SupportingPoints[0])
}
