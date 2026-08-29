package graph

import (
	"context"
	"reflect"
	"testing"
)

// These run on every backend the harness can reach, because a breakdown of the
// graph by type is exactly the kind of query that keeps returning rows after it
// has stopped being true. The callers this API exists to replace were counting
// with SQL of their own; replacing one unverified query with another would not
// be an improvement.

// seedVocabularyGraph builds a small graph with one of everything the checks
// below care about: two node types that are used, one node nobody typed, a
// relation that runs both ways, and a pair joined twice by the same relation.
func seedVocabularyGraph(t *testing.T, g *GraphStore) {
	t.Helper()
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	// Lowercase ASCII ids on purpose: ORDER BY runs under each database's own
	// collation, and a test that sorted "Pool" against "svc" would be asserting
	// on the collation rather than on the query.
	nodes := []*GraphNode{
		{ID: "pool", Vector: []float32{1, 0, 0, 0}, NodeType: "resource", Content: "connection pool"},
		{ID: "svc", Vector: []float32{0, 1, 0, 0}, NodeType: "service", Content: "api service"},
		{ID: "flag", Vector: []float32{0, 0, 1, 0}, NodeType: "command", Content: "--verbose"},
		{ID: "loose", Vector: []float32{0, 0, 0, 1}, Content: "nobody typed this"},
	}
	for _, n := range nodes {
		if err := g.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}

	edges := []*GraphEdge{
		{ID: "e1", FromNodeID: "svc", ToNodeID: "pool", EdgeType: "uses", Weight: 1},
		{ID: "e2", FromNodeID: "pool", ToNodeID: "svc", EdgeType: "uses", Weight: 1}, // backwards
		{ID: "e3", FromNodeID: "svc", ToNodeID: "flag", EdgeType: "accepts", Weight: 1},
		{ID: "e4", FromNodeID: "svc", ToNodeID: "pool", EdgeType: "uses", Weight: 1}, // same pair again
	}
	for _, e := range edges {
		if err := g.UpsertEdge(ctx, e); err != nil {
			t.Fatalf("UpsertEdge %s: %v", e.ID, err)
		}
	}
}

func TestTypeCountsOnBothBackends(t *testing.T) {
	wantNodes := map[string]int{"resource": 1, "service": 1, "command": 1, "": 1}
	wantEdges := map[string]int{"uses": 3, "accepts": 1}

	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)

			gotNodes, err := b.store.NodeTypeCounts(ctx)
			if err != nil {
				t.Fatalf("NodeTypeCounts: %v", err)
			}
			if !reflect.DeepEqual(gotNodes, wantNodes) {
				t.Errorf("NodeTypeCounts = %v, want %v", gotNodes, wantNodes)
			}

			gotEdges, err := b.store.EdgeTypeCounts(ctx)
			if err != nil {
				t.Fatalf("EdgeTypeCounts: %v", err)
			}
			if !reflect.DeepEqual(gotEdges, wantEdges) {
				t.Errorf("EdgeTypeCounts = %v, want %v", gotEdges, wantEdges)
			}
		})
	}
}

// The untyped node is the one worth a test of its own: dropping it would make
// the counts sum to less than the node count, and the caller would have to
// discover that by arithmetic rather than being told.
func TestUntypedNodesAreCountedNotDropped(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)

			counts, err := b.store.NodeTypeCounts(ctx)
			if err != nil {
				t.Fatalf("NodeTypeCounts: %v", err)
			}
			if counts[""] != 1 {
				t.Errorf("untyped nodes counted as %d, want 1", counts[""])
			}

			total := 0
			for _, n := range counts {
				total += n
			}
			stats, err := b.store.GetGraphStatistics(ctx)
			if err != nil {
				t.Fatalf("GetGraphStatistics: %v", err)
			}
			if total != stats.NodeCount {
				t.Errorf("counts sum to %d but the graph has %d nodes", total, stats.NodeCount)
			}
		})
	}
}

func TestEdgeShapesOnBothBackends(t *testing.T) {
	want := []EdgeShape{
		{EdgeType: "accepts", FromType: "service", ToType: "command", Count: 1},
		{EdgeType: "uses", FromType: "resource", ToType: "service", Count: 1},
		{EdgeType: "uses", FromType: "service", ToType: "resource", Count: 2},
	}

	shapes := map[string][]EdgeShape{}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)

			got, err := b.store.EdgeShapes(ctx)
			if err != nil {
				t.Fatalf("EdgeShapes: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("EdgeShapes = %+v, want %+v", got, want)
			}
			shapes[b.name] = got
		})
	}

	if len(shapes) == 2 && !reflect.DeepEqual(shapes["sqlite"], shapes["postgres"]) {
		t.Errorf("backends disagree: sqlite %+v vs postgres %+v", shapes["sqlite"], shapes["postgres"])
	}
}

// The reversed edge is the finding this API exists to make possible: `uses`
// runs service->resource three times out of four, and once the other way.
func TestEdgeShapesExposeARelationRunBackwards(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)

			got, err := b.store.EdgeShapes(ctx, "uses")
			if err != nil {
				t.Fatalf("EdgeShapes: %v", err)
			}
			if len(got) != 2 {
				t.Fatalf("EdgeShapes(uses) returned %d shapes (%+v), want 2", len(got), got)
			}
			var forward, backward int
			for _, s := range got {
				switch {
				case s.FromType == "service" && s.ToType == "resource":
					forward = s.Count
				case s.FromType == "resource" && s.ToType == "service":
					backward = s.Count
				default:
					t.Errorf("unexpected shape %+v", s)
				}
			}
			if forward != 2 || backward != 1 {
				t.Errorf("uses ran forward %d / backward %d, want 2 / 1", forward, backward)
			}
		})
	}
}

func TestEdgeShapesFilterNarrowsToTheTypesAsked(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)

			got, err := b.store.EdgeShapes(ctx, "accepts")
			if err != nil {
				t.Fatalf("EdgeShapes: %v", err)
			}
			want := []EdgeShape{{EdgeType: "accepts", FromType: "service", ToType: "command", Count: 1}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("EdgeShapes(accepts) = %+v, want %+v", got, want)
			}

			// A filter that matches nothing returns nothing, not everything —
			// the failure mode of a WHERE clause built from an empty list.
			none, err := b.store.EdgeShapes(ctx, "no-such-relation")
			if err != nil {
				t.Fatalf("EdgeShapes: %v", err)
			}
			if len(none) != 0 {
				t.Errorf("filtering on an absent type returned %d shapes", len(none))
			}
		})
	}
}

func TestEdgeEndpointPairsNameTheNodesBehindAShape(t *testing.T) {
	want := []EdgeEndpointPair{
		{
			EdgeType: "uses",
			From:     EdgeEndpoint{ID: "pool", NodeType: "resource", Content: "connection pool"},
			To:       EdgeEndpoint{ID: "svc", NodeType: "service", Content: "api service"},
			Count:    1,
		},
		{
			EdgeType: "uses",
			From:     EdgeEndpoint{ID: "svc", NodeType: "service", Content: "api service"},
			To:       EdgeEndpoint{ID: "pool", NodeType: "resource", Content: "connection pool"},
			Count:    2,
		},
	}

	pairs := map[string][]EdgeEndpointPair{}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)

			got, err := b.store.EdgeEndpointPairs(ctx, "uses")
			if err != nil {
				t.Fatalf("EdgeEndpointPairs: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("EdgeEndpointPairs = %+v, want %+v", got, want)
			}
			pairs[b.name] = got
		})
	}

	if len(pairs) == 2 && !reflect.DeepEqual(pairs["sqlite"], pairs["postgres"]) {
		t.Errorf("backends disagree: sqlite %+v vs postgres %+v", pairs["sqlite"], pairs["postgres"])
	}
}

// An empty graph is not an error condition, and the difference between "no
// types" and "the query failed" is the whole value of a drift report.
func TestVocabularyQueriesOnAnEmptyGraph(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}

			nodes, err := b.store.NodeTypeCounts(ctx)
			if err != nil {
				t.Fatalf("NodeTypeCounts: %v", err)
			}
			if len(nodes) != 0 {
				t.Errorf("NodeTypeCounts on an empty graph = %v", nodes)
			}

			shapes, err := b.store.EdgeShapes(ctx)
			if err != nil {
				t.Fatalf("EdgeShapes: %v", err)
			}
			if len(shapes) != 0 {
				t.Errorf("EdgeShapes on an empty graph = %+v", shapes)
			}

			endpoints, err := b.store.EdgeEndpointPairs(ctx)
			if err != nil {
				t.Fatalf("EdgeEndpointPairs: %v", err)
			}
			if len(endpoints) != 0 {
				t.Errorf("EdgeEndpointPairs on an empty graph = %+v", endpoints)
			}
		})
	}
}

// The seed graph has one node — `loose` — that no edge touches.
func TestConnectivityCountsTheNodesNoEdgeTouches(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)

			got, err := b.store.Connectivity(ctx)
			if err != nil {
				t.Fatalf("Connectivity: %v", err)
			}
			if want := (Connectivity{Nodes: 4, Orphans: 1}); got != want {
				t.Errorf("Connectivity = %+v, want %+v", got, want)
			}
		})
	}
}

// An edge arriving at a node makes it reachable just as much as one leaving:
// the check is whether anything joins it, not which way.
func TestConnectivityCountsAnIncomingEdgeAsReaching(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)
			// `flag` is reached only by e3, and only as the target.
			if err := b.store.UpsertEdge(ctx, &GraphEdge{
				ID: "e5", FromNodeID: "svc", ToNodeID: "loose", EdgeType: "uses", Weight: 1,
			}); err != nil {
				t.Fatalf("UpsertEdge: %v", err)
			}
			got, err := b.store.Connectivity(ctx)
			if err != nil {
				t.Fatalf("Connectivity: %v", err)
			}
			if got.Orphans != 0 {
				t.Errorf("orphans = %d, want 0 — every node is now joined", got.Orphans)
			}
		})
	}
}

// An empty graph must answer zero rather than fail: SUM over no rows is NULL,
// and a health check that errors on a fresh store reports a defect instead of
// an empty graph.
func TestConnectivityOnAnEmptyGraph(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			got, err := b.store.Connectivity(ctx)
			if err != nil {
				t.Fatalf("Connectivity: %v", err)
			}
			if got != (Connectivity{}) {
				t.Errorf("Connectivity = %+v, want zero", got)
			}
		})
	}
}

func TestNodeLabelsReturnTypeAndLabelForEveryNode(t *testing.T) {
	want := []NodeLabel{
		{ID: "flag", NodeType: "command", Content: "--verbose"},
		{ID: "loose", NodeType: "", Content: "nobody typed this"},
		{ID: "pool", NodeType: "resource", Content: "connection pool"},
		{ID: "svc", NodeType: "service", Content: "api service"},
	}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)

			got, err := b.store.NodeLabels(ctx, NodeLabelQuery{})
			if err != nil {
				t.Fatalf("NodeLabels: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("NodeLabels =\n %+v\nwant\n %+v", got, want)
			}
		})
	}
}

// The filters are what let a caller ask for one writer's nodes, and for labels
// long enough to be worth matching text against.
func TestNodeLabelsNarrowByPrefixAndLength(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)
			if err := b.store.UpsertNode(ctx, &GraphNode{
				ID: "entity:tank", Vector: []float32{1, 1, 0, 0}, NodeType: "pool", Content: "tank",
			}); err != nil {
				t.Fatalf("UpsertNode: %v", err)
			}

			got, err := b.store.NodeLabels(ctx, NodeLabelQuery{IDPrefix: "entity:"})
			if err != nil {
				t.Fatalf("NodeLabels: %v", err)
			}
			if len(got) != 1 || got[0].ID != "entity:tank" {
				t.Fatalf("prefix = %+v, want only the entity node", got)
			}

			// "tank" and "--verbose" are shorter than "connection pool".
			long, err := b.store.NodeLabels(ctx, NodeLabelQuery{MinContentLength: 15})
			if err != nil {
				t.Fatalf("NodeLabels: %v", err)
			}
			for _, l := range long {
				if len([]rune(l.Content)) < 15 {
					t.Errorf("%q is shorter than the minimum asked for", l.Content)
				}
			}
			if len(long) != 2 {
				t.Errorf("long labels = %+v, want the two 15-character ones", long)
			}
		})
	}
}

// An underscore is LIKE's single-character wildcard, so a prefix carrying one
// would match ids that merely resemble it — a caller passing an id convention
// is passing a literal, not a pattern.
func TestNodeLabelsTreatAPrefixAsLiteralNotAPattern(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)
			for _, n := range []*GraphNode{
				{ID: "a_b:real", Vector: []float32{1, 0, 0, 0}, Content: "the literal one"},
				{ID: "axb:other", Vector: []float32{0, 1, 0, 0}, Content: "the wildcard one"},
			} {
				if err := b.store.UpsertNode(ctx, n); err != nil {
					t.Fatalf("UpsertNode %s: %v", n.ID, err)
				}
			}

			got, err := b.store.NodeLabels(ctx, NodeLabelQuery{IDPrefix: "a_b:"})
			if err != nil {
				t.Fatalf("NodeLabels: %v", err)
			}
			if len(got) != 1 || got[0].ID != "a_b:real" {
				t.Errorf("prefix a_b: matched %+v, want only the literal id", got)
			}
		})
	}
}

func TestNodeLabelsOnAnEmptyGraph(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			got, err := b.store.NodeLabels(ctx, NodeLabelQuery{IDPrefix: "entity:"})
			if err != nil {
				t.Fatalf("NodeLabels: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("NodeLabels = %+v, want none", got)
			}
		})
	}
}
