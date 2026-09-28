package graph

import (
	"context"
	"testing"
)

// buildPathFixture writes a small entity graph:
//
//	alice -works_at-> borealis -built-> kestrel
//	alice -co_occurs-> noise1 -co_occurs-> kestrel
//	alice -mentions-> chunk1 <-mentions- kestrel   (bookkeeping)
//	bob -works_at-> borealis
func buildPathFixture(t *testing.T, g *GraphStore) {
	t.Helper()
	ctx := context.Background()
	for _, n := range []struct{ id, typ string }{
		{"alice", "person"}, {"bob", "person"}, {"borealis", "org"}, {"kestrel", "satellite"},
		{"noise1", "entity"}, {"chunk1", "chunk"},
	} {
		if err := g.UpsertNode(ctx, &GraphNode{ID: n.id, NodeType: n.typ, Content: n.id, Vector: []float32{1, 0, 0}}); err != nil {
			t.Fatalf("node %s: %v", n.id, err)
		}
	}
	for _, e := range []struct{ id, from, to, typ string }{
		{"e1", "alice", "borealis", "works_at"},
		{"e2", "borealis", "kestrel", "built"},
		{"e3", "alice", "noise1", "co_occurs"},
		{"e4", "noise1", "kestrel", "co_occurs"},
		{"e5", "chunk1", "alice", "mentions"},
		{"e6", "chunk1", "kestrel", "mentions"},
		{"e7", "bob", "borealis", "works_at"},
	} {
		if err := g.UpsertEdge(ctx, &GraphEdge{ID: e.id, FromNodeID: e.from, ToNodeID: e.to, EdgeType: e.typ, Weight: 1}); err != nil {
			t.Fatalf("edge %s: %v", e.id, err)
		}
	}
}

func edgeIDs(p *ScoredPath) []string {
	out := make([]string, 0, len(p.Edges))
	for _, e := range p.Edges {
		out = append(out, e.ID)
	}
	return out
}

func TestSearchPathsBetweenSeeds(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	buildPathFixture(t, g)
	ctx := context.Background()

	res, err := g.SearchPaths(ctx, []string{"alice", "kestrel"}, PathSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 2 {
		t.Fatalf("want 2 paths (bookkeeping excluded), got %d: %+v", len(res.Paths), res.Paths)
	}
	// Equal length, equal weight: tie broken by edge ids, so e1|e2 first.
	if got := edgeIDs(res.Paths[0]); len(got) != 2 || got[0] != "e1" || got[1] != "e2" {
		t.Fatalf("first path = %v", got)
	}
	if res.Paths[0].Score != 0.5 || res.Paths[0].From != "alice" || res.Paths[0].To != "kestrel" {
		t.Fatalf("path = %+v", res.Paths[0])
	}

	// A relation policy demotes the statistical edge.
	res, err = g.SearchPaths(ctx, []string{"alice", "kestrel"}, PathSearchOptions{
		Relations: RelationPolicies{"co_occurs": {Weight: 0.1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Paths[0].Score != 0.5 || res.Paths[1].Score >= 0.01 {
		t.Fatalf("scores = %v, %v", res.Paths[0].Score, res.Paths[1].Score)
	}

	// Per-type max depth: co_occurs only at hop 1 cuts the noise path.
	res, err = g.SearchPaths(ctx, []string{"alice", "kestrel"}, PathSearchOptions{
		Relations: RelationPolicies{"co_occurs": {MaxDepth: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 1 {
		t.Fatalf("want 1 path, got %d", len(res.Paths))
	}

	// Bookkeeping included: the co-mention through chunk1 appears.
	res, err = g.SearchPaths(ctx, []string{"alice", "kestrel"}, PathSearchOptions{IncludeBookkeeping: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 3 {
		t.Fatalf("want 3 paths with bookkeeping, got %d", len(res.Paths))
	}

	// Directed: nothing points into alice, so "in" from alice finds nothing to kestrel.
	res, err = g.SearchPaths(ctx, []string{"alice", "kestrel"}, PathSearchOptions{Direction: "out"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 2 {
		t.Fatalf("out: want 2, got %d", len(res.Paths))
	}
	if _, err := g.SearchPaths(ctx, []string{"alice"}, PathSearchOptions{Direction: "sideways"}); err == nil {
		t.Fatal("bad direction accepted")
	}
}

func TestSearchPathsSingleSeedAndBounds(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	buildPathFixture(t, g)
	ctx := context.Background()

	res, err := g.SearchPaths(ctx, []string{"alice"}, PathSearchOptions{MaxDepth: 2, MaxPaths: 100})
	if err != nil {
		t.Fatal(err)
	}
	// hop1: e1, e3; hop2: e1-e2, e1-e7, e3-e4
	if len(res.Paths) != 5 {
		t.Fatalf("want 5 open paths, got %d", len(res.Paths))
	}
	if res.Paths[0].Hops != 1 {
		t.Fatalf("shortest first: %+v", res.Paths[0])
	}

	res, err = g.SearchPaths(ctx, []string{"alice"}, PathSearchOptions{MaxDepth: 3, MaxExpansions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Expansions != 2 {
		t.Fatalf("want truncated at 2, got %+v", res)
	}

	res, err = g.SearchPaths(ctx, nil, PathSearchOptions{})
	if err != nil || len(res.Paths) != 0 {
		t.Fatalf("empty seeds: %v %v", res, err)
	}
}

func TestRelationPoliciesAllow(t *testing.T) {
	p := RelationPolicies{"a": {Weight: 0.5, MaxDepth: 1}, "x": {Weight: -1}, "*": {Weight: 0.25}}
	if w, ok := p.Allow("a", 1); !ok || w != 0.5 {
		t.Fatal(w, ok)
	}
	if _, ok := p.Allow("a", 2); ok {
		t.Fatal("depth cap ignored")
	}
	if _, ok := p.Allow("x", 1); ok {
		t.Fatal("negative weight not excluded")
	}
	if w, _ := p.Allow("other", 3); w != 0.25 {
		t.Fatal("default key ignored", w)
	}
	if w, ok := RelationPolicies(nil).Allow("any", 9); !ok || w != 1 {
		t.Fatal("nil policy must be neutral")
	}
	if w, _ := (RelationPolicies{"a": {MaxDepth: 2}}).Allow("a", 1); w != 1 {
		t.Fatal("zero weight must mean 1", w)
	}
}

func TestWeightedNeighborsAndHybridSearch(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	buildPathFixture(t, g)
	ctx := context.Background()

	// Unweighted Neighbors is unchanged: breadth-first, no policy.
	plain, err := g.Neighbors(ctx, "alice", TraversalOptions{MaxDepth: 2, Direction: "out"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 3 {
		t.Fatalf("plain neighbors = %d", len(plain))
	}

	weighted, err := g.Neighbors(ctx, "alice", TraversalOptions{
		MaxDepth: 2, Direction: "out",
		Relations: RelationPolicies{"co_occurs": {Weight: 0.1, MaxDepth: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// borealis (1/2), kestrel via works_at+built (1/3), noise1 (0.1/2).
	if len(weighted) != 3 || weighted[0].ID != "borealis" || weighted[1].ID != "kestrel" || weighted[2].ID != "noise1" {
		ids := []string{}
		for _, n := range weighted {
			ids = append(ids, n.ID)
		}
		t.Fatalf("weighted order = %v", ids)
	}

	results, err := g.HybridSearch(ctx, &HybridQuery{
		StartNodeID: "alice",
		GraphFilter: &GraphFilter{MaxDepth: 2, Relations: RelationPolicies{"co_occurs": {Weight: 0.1}}},
		Weights:     HybridWeights{GraphWeight: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	var noise, kestrel float64
	for _, r := range results {
		switch r.Node.ID {
		case "noise1":
			noise = r.GraphScore
		case "kestrel":
			kestrel = r.GraphScore
		}
	}
	if !(kestrel > noise) || noise == 0 {
		t.Fatalf("kestrel %.3f should outrank noise1 %.3f", kestrel, noise)
	}
}
