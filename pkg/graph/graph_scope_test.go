package graph

import (
	"context"
	"testing"
)

// The failure these tests are about.
//
// This store is shared: everything on the machine writes into the same
// graph_nodes. Before GraphFilter carried Properties, node_type was the only
// way to narrow a read, and a type name is not a batch — an importer that
// wrote Person rows on Tuesday and more on Friday could ask for all of them or
// none. Alchemy's connector had been stamping a "run" onto every node it wrote
// since it was written, and nothing could read it back, which is why cortexdb
// was the one store of four that could not implement alchemy's read interface
// at all.

// seedTwoRuns writes two batches under one node type, plus one node that
// belongs to neither and carries no properties whatsoever.
func seedTwoRuns(t *testing.T, ctx context.Context, g *GraphStore) {
	t.Helper()
	seed := []GraphNode{
		{ID: "a1", Vector: []float32{1, 0, 0}, NodeType: "Person", Properties: map[string]interface{}{"run": "tuesday", "src": "hr.csv"}},
		{ID: "a2", Vector: []float32{0, 1, 0}, NodeType: "Person", Properties: map[string]interface{}{"run": "tuesday", "src": "hr.csv"}},
		{ID: "a3", Vector: []float32{0, 0, 1}, NodeType: "Person", Properties: map[string]interface{}{"run": "tuesday", "src": "badges.csv"}},
		{ID: "b1", Vector: []float32{1, 1, 0}, NodeType: "Person", Properties: map[string]interface{}{"run": "friday", "src": "hr.csv"}},
		{ID: "b2", Vector: []float32{0, 1, 1}, NodeType: "Person", Properties: map[string]interface{}{"run": "friday", "src": "hr.csv"}},
		// Somebody else's row. It has no properties at all, which is the case
		// that used to take the whole query down rather than simply not match:
		// properties is a TEXT column, an unset one is the empty string, and
		// SQLite's json_extract calls that malformed JSON while PostgreSQL's
		// ::jsonb calls it invalid input syntax.
		{ID: "stray", Vector: []float32{2, 2, 2}, NodeType: "Person"},
	}
	for i := range seed {
		if err := g.UpsertNode(ctx, &seed[i]); err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}
}

func ids(nodes []*GraphNode) map[string]bool {
	out := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		out[n.ID] = true
	}
	return out
}

func TestOneRunIsReadableOutOfASharedStore(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	seedTwoRuns(t, ctx, g)

	got, err := g.GetAllNodes(ctx, &GraphFilter{Properties: map[string]string{"run": "tuesday"}})
	if err != nil {
		t.Fatalf("GetAllNodes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d nodes for run tuesday, want 3 — %v", len(got), ids(got))
	}
	for _, want := range []string{"a1", "a2", "a3"} {
		if !ids(got)[want] {
			t.Errorf("run tuesday is missing %s", want)
		}
	}
	// The point of the whole change: the other batch and the unrelated row are
	// not in the answer. Without the filter all six come back, and a caller
	// reporting "3 people in this import" would have said six.
	for _, unwanted := range []string{"b1", "b2", "stray"} {
		if ids(got)[unwanted] {
			t.Errorf("run tuesday wrongly includes %s", unwanted)
		}
	}
}

func TestANodeWithNoPropertiesDoesNotFailTheQuery(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	seedTwoRuns(t, ctx, g)

	// The guarded read is what this asserts. An unguarded json_extract over
	// the "stray" row errors, and the error is not about that row — it fails
	// the whole statement, so a caller asking about their own batch is told
	// nothing exists because somebody else wrote a row without properties.
	got, err := g.GetAllNodes(ctx, &GraphFilter{Properties: map[string]string{"run": "friday"}})
	if err != nil {
		t.Fatalf("a row with no properties broke the query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 — %v", len(got), ids(got))
	}
}

func TestPropertiesNarrowTogether(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	seedTwoRuns(t, ctx, g)

	got, err := g.GetAllNodes(ctx, &GraphFilter{
		NodeTypes:  []string{"Person"},
		Properties: map[string]string{"run": "tuesday", "src": "hr.csv"},
	})
	if err != nil {
		t.Fatalf("GetAllNodes: %v", err)
	}
	// a3 is in the same run and came from a different file. ORed, it would be
	// here; ANDed, it is not — and ANDing is the only reading under which
	// "this run, this source" means anything.
	if len(got) != 2 || !ids(got)["a1"] || !ids(got)["a2"] {
		t.Fatalf("got %v, want exactly a1 and a2", ids(got))
	}
}

func TestACapSaysHowMuchItCutOff(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	seedTwoRuns(t, ctx, g)

	scope := map[string]string{"run": "tuesday"}
	page, err := g.GetAllNodes(ctx, &GraphFilter{Properties: scope, Limit: 2})
	if err != nil {
		t.Fatalf("GetAllNodes: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("Limit 2 returned %d rows", len(page))
	}

	// CountNodes ignores Limit on purpose. A count that stopped at the cap
	// would always equal the cap, and "2 of 2" is exactly the confident wrong
	// answer this method exists to replace.
	total, err := g.CountNodes(ctx, &GraphFilter{Properties: scope, Limit: 2})
	if err != nil {
		t.Fatalf("CountNodes: %v", err)
	}
	if total != 3 {
		t.Fatalf("CountNodes = %d, want 3; a caller cannot say '2 of 3' without it", total)
	}
}

func TestACappedReadIsTheSameWindowTwice(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	seedTwoRuns(t, ctx, g)

	f := &GraphFilter{Properties: map[string]string{"run": "tuesday"}, Limit: 2}
	first, err := g.GetAllNodes(ctx, f)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := g.GetAllNodes(ctx, f)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("row %d was %s then %s; an unordered window cannot be paged through",
				i, first[i].ID, second[i].ID)
		}
	}
}

func TestCountNodesWithNoFilterCountsEverything(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	seedTwoRuns(t, ctx, g)

	total, err := g.CountNodes(ctx, nil)
	if err != nil {
		t.Fatalf("CountNodes: %v", err)
	}
	if total != 6 {
		t.Fatalf("CountNodes(nil) = %d, want 6", total)
	}
}

func TestAnUnfilteredReadIsUnchanged(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	seedTwoRuns(t, ctx, g)

	// Every caller that existed before these fields passes a filter whose
	// Properties and Limit are zero, and must get exactly what it got before.
	all, err := g.GetAllNodes(ctx, &GraphFilter{})
	if err != nil {
		t.Fatalf("GetAllNodes: %v", err)
	}
	if len(all) != 6 {
		t.Fatalf("an empty filter returned %d of 6 nodes", len(all))
	}
	byType, err := g.GetAllNodes(ctx, &GraphFilter{NodeTypes: []string{"Person"}})
	if err != nil {
		t.Fatalf("GetAllNodes by type: %v", err)
	}
	if len(byType) != 6 {
		t.Fatalf("the type filter returned %d of 6 nodes", len(byType))
	}
}
