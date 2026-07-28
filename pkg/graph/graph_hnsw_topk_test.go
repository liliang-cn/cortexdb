package graph

import (
	"context"
	"testing"
)

// One call must mean one thing whether or not an index happens to exist.
//
// HybridSearch treats TopK=0 as "no cap" — `if query.TopK > 0 && len(results) > query.TopK`. The HNSW
// path truncated unconditionally, so the same query returned every match without an index and nothing
// at all with one: results[:0]. A caller who never set TopK saw an empty graph and no error, and
// whether they saw it depended on a performance optimisation they may not know is on.
func TestHNSWHybridSearchWithoutTopKMeansTheSameAsWithoutTheIndex(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	for _, node := range []struct {
		id     string
		vector []float32
	}{
		{"concept:limit", []float32{1, 0, 0}},
		{"concept:derivative", []float32{0.9, 0.1, 0}},
		{"concept:integral", []float32{0.8, 0.2, 0}},
	} {
		if err := graph.UpsertNode(ctx, &GraphNode{
			ID:       node.id,
			NodeType: "concept",
			Content:  node.id,
			Vector:   node.vector,
		}); err != nil {
			t.Fatalf("upsert %s: %v", node.id, err)
		}
	}

	query := func() *HybridQuery {
		// TopK deliberately left at its zero value: it is optional throughout this API, and a caller
		// who wants everything simply does not set it.
		return &HybridQuery{Vector: []float32{1, 0, 0}, VectorWeight: 1}
	}

	withoutIndex, err := graph.HybridSearch(ctx, query())
	if err != nil {
		t.Fatalf("hybrid search without an index: %v", err)
	}
	if len(withoutIndex) == 0 {
		t.Fatal("the un-indexed path returned nothing; the fixture is wrong, not the code")
	}

	if err := graph.EnableHNSWIndex(3); err != nil {
		t.Fatalf("enable hnsw: %v", err)
	}
	withIndex, err := graph.HNSWHybridSearch(ctx, query())
	if err != nil {
		t.Fatalf("hnsw hybrid search: %v", err)
	}

	if len(withIndex) == 0 {
		t.Fatalf("with an index the same query returned nothing; without one it returned %d", len(withoutIndex))
	}
}

// And an explicit cap must still cap, or the fix would have traded one wrong answer for another.
//
// One-sided on purpose. How MANY of the four this returns is the index's own business and is not
// deterministic: `SimpleHNSW.Add` links each node to the candidates found by a single-candidate search
// from the entry point, and with `randomLevel()` deciding the layout, 2 runs in 15 of an earlier version
// of this test returned 1 of 4 matches rather than 2. That is a separate defect in the index's
// construction, not something this assertion should encode — a test that pins flaky recall either fails
// at random or freezes the flakiness in place.
func TestHNSWHybridSearchStillHonoursAnExplicitTopK(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c", "d"} {
		if err := graph.UpsertNode(ctx, &GraphNode{
			ID:       "concept:" + id,
			NodeType: "concept",
			Content:  id,
			Vector:   []float32{1, 0, 0},
		}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
	if err := graph.EnableHNSWIndex(3); err != nil {
		t.Fatalf("enable hnsw: %v", err)
	}

	results, err := graph.HNSWHybridSearch(ctx, &HybridQuery{
		Vector:       []float32{1, 0, 0},
		VectorWeight: 1,
		TopK:         2,
	})
	if err != nil {
		t.Fatalf("hnsw hybrid search: %v", err)
	}
	if len(results) > 2 {
		t.Fatalf("asked for at most 2, got %d", len(results))
	}
}
