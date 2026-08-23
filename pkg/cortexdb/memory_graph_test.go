package cortexdb

import (
	"context"
	"path/filepath"
	"testing"
)

// The point of putting memories in the graph: reaching one through an entity it
// was saved with, when its text shares no word with the question. Lexical search
// cannot do this, and until memories had nodes neither could graph search —
// saveMemoryGraph wrote entity nodes that nothing pointed at.
func TestSearchMemoryFindsByEntityWithNoLexicalOverlap(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "memgraph.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "port-rule",
		Scope:    "global",
		Content:  "prefer high random ports, never the usual ones",
		Entities: []ToolEntityInput{{Name: "CortexDB", Type: "project"}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A second memory with no graph presence, so a hit cannot come from there
	// being only one memory to return.
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "unrelated",
		Scope:    "global",
		Content:  "an unrelated note about breakfast",
	}); err != nil {
		t.Fatalf("save unrelated: %v", err)
	}

	res, err := db.SearchMemory(ctx, MemorySearchRequest{
		Query:       "what do I know about this project",
		Scope:       "global",
		EntityNames: []string{"CortexDB"},
		TopK:        3,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !res.Decision.UseGraph || res.Decision.EffectiveMode != RetrievalModeGraph {
		t.Fatalf("entity_names should route to graph, got %+v", res.Decision)
	}
	if len(res.Results) == 0 {
		t.Fatal("expected the memory reached through its entity")
	}
	if res.Results[0].Memory.ID != "port-rule" {
		t.Errorf("graph hit should rank first, got %q", res.Results[0].Memory.ID)
	}
}

// Memory graph presence is opt-in, so a capitalised word in the query must not
// send an ordinary search down a graph that has nothing to say.
func TestSearchMemoryDoesNotGuessGraphFromQueryTerms(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "memguess.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "m1", Scope: "global", Content: "Alice prefers short answers",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "How should I answer Alice?", Scope: "global", TopK: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.Decision.UseGraph {
		t.Errorf("no entity_names were given, graph should not be chosen: %+v", res.Decision)
	}
	if len(res.Results) == 0 {
		t.Error("lexical search should still answer")
	}
}

// A memory saved with entities has to be reachable from the graph at all; this
// pins the node and the mention edge that make that possible.
func TestSaveMemoryLinksEntitiesToTheMemoryNode(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "memnode.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "m1",
		Scope:    "global",
		Content:  "the gateway fronts the store",
		Entities: []ToolEntityInput{{Name: "Gateway", Type: "component"}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	nodeID := memoryGraphNodeID("m1")
	nodes, err := db.graph.GetNodesBatch(ctx, []string{nodeID})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if len(nodes) == 0 || nodes[0] == nil {
		t.Fatalf("no graph node for the memory (%s)", nodeID)
	}
	if nodes[0].NodeType != "memory" {
		t.Errorf("memory node typed %q, want %q", nodes[0].NodeType, "memory")
	}

	var edges int
	if err := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE edge_type = 'mentions' AND from_node_id = ?`,
		nodeID).Scan(&edges); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edges == 0 {
		t.Error("entity was written with no edge back to the memory that asserted it")
	}
}
