package liveview

import (
	"context"
	"path/filepath"
	"testing"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The picture and the tally have to agree about what a fact is.
//
// A Decision node with based_on edges is how the store remembers that somebody
// answered a review; a document and its mentions are how a fact cites the page
// it came from. Drawn beside the people they are about, they make the same
// claim the untagged column used to make — that the ledger is knowledge — and
// the reader cannot tell by looking which half of the hairball is which. They
// are read by the walk that answers "what was decided", not by the picture
// that answers "what does this brain hold".
//
// This walks graph.BookkeepingNodeTypes / BookkeepingEdgeTypes rather than
// listing them again, so a sixth kind of filing added to the vocabulary fails
// here until it is left out of the picture too.
func TestTheLiveGraphDrawsKnowledgeAndNotTheStoresOwnFiling(t *testing.T) {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "mixed.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	// Two facts about the world, joined by an edge that is a claim.
	knowledge := []*graph.GraphNode{
		{ID: "person:ada", Vector: []float32{1, 0, 0, 0}, NodeType: "Person", Content: "Ada"},
		{ID: "company:acme", Vector: []float32{0, 1, 0, 0}, NodeType: "Company", Content: "Acme"},
	}
	// One node of every kind of filing the store does, each wired to a fact so
	// that a picture drawing them would draw them connected rather than as
	// orphans the degree cap might drop on its own.
	filing := []*graph.GraphNode{
		{ID: "decision:review:1", Vector: []float32{0, 0, 1, 0}, NodeType: "Decision", Content: "accepted by liliang"},
		{ID: "doc:roster", Vector: []float32{0, 0, 0, 1}, NodeType: "document", Content: "roster.md"},
		{ID: "chunk:roster:0", Vector: []float32{1, 1, 0, 0}, NodeType: "chunk", Content: "Ada works at Acme."},
	}
	for _, n := range append(append([]*graph.GraphNode{}, knowledge...), filing...) {
		if err := db.Graph().UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}
	for _, e := range []*graph.GraphEdge{
		{ID: "e:works", FromNodeID: "person:ada", ToNodeID: "company:acme", EdgeType: "works_at", Weight: 1},
		{ID: "e:based", FromNodeID: "decision:review:1", ToNodeID: "person:ada", EdgeType: "based_on", Weight: 1},
		{ID: "e:mentions", FromNodeID: "doc:roster", ToNodeID: "person:ada", EdgeType: "mentions", Weight: 1},
		{ID: "e:chunk", FromNodeID: "doc:roster", ToNodeID: "chunk:roster:0", EdgeType: "has_chunk", Weight: 1},
	} {
		if err := db.Graph().UpsertEdge(ctx, e); err != nil {
			t.Fatalf("UpsertEdge %s: %v", e.ID, err)
		}
	}

	nodes, edges, err := LoadLocal(ctx, db.SQL())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}

	drawnTypes := map[string]string{}
	for _, n := range nodes {
		drawnTypes[n.Type] = n.ID
	}
	for _, bookkeeping := range graph.BookkeepingNodeTypes {
		if id, drawn := drawnTypes[bookkeeping]; drawn {
			t.Errorf("the picture draws %s %q, which is the store's own filing and not a fact about the world", bookkeeping, id)
		}
	}
	for _, want := range []string{"person:ada", "company:acme"} {
		if _, ok := func() (Node, bool) {
			for _, n := range nodes {
				if n.ID == want {
					return n, true
				}
			}
			return Node{}, false
		}(); !ok {
			t.Errorf("the picture lost %s, which is a fact about the world", want)
		}
	}

	drawnEdges := map[string]string{}
	for _, e := range edges {
		drawnEdges[e.Label] = e.ID
	}
	for _, bookkeeping := range graph.BookkeepingEdgeTypes {
		if id, drawn := drawnEdges[bookkeeping]; drawn {
			t.Errorf("the picture draws a %s edge (%s), which holds the filing together and asserts nothing", bookkeeping, id)
		}
	}
	if _, ok := drawnEdges["works_at"]; !ok {
		t.Error("the picture lost the works_at edge, which is the one claim in this store")
	}
	if len(edges) != 1 {
		t.Errorf("the picture drew %d edges, want only the one claim", len(edges))
	}
}
