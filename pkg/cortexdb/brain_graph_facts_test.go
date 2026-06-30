package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRecallSurfacesGraphFactsForRelationalQuery covers the lexical-recall
// weakness for relational questions: without an embedder, "who uses Apollo"
// must be answered from the graph edge (Alice -uses-> Apollo), not from a
// lexically-similar but relationless chunk. Recall should expose the edge as a
// graph fact and in the context pack.
func TestRecallSurfacesGraphFactsForRelationalQuery(t *testing.T) {
	dbPath := fmt.Sprintf("test_graph_facts_%d.db", time.Now().UnixNano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	ctx := context.Background()

	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "apollo-plan",
		Content:     "Apollo is a project. Alice is the owner.",
		ChunkSize:   64,
		Entities: []ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{"chunk:apollo-plan:000"}},
			{Name: "Apollo", Type: "project", ChunkIDs: []string{"chunk:apollo-plan:000"}},
		},
		Relations: []ToolRelationInput{{From: "Alice", To: "Apollo", Type: "uses"}},
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}
	// Lexically-similar noise with no relation — the kind of chunk pure lexical
	// recall wrongly surfaces for "who uses ...".
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "noise",
		Content:     "A directory listing of who uses which internal tools and projects.",
	}); err != nil {
		t.Fatalf("save noise: %v", err)
	}

	resp, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{
		Query:         "who uses Apollo?",
		RetrievalMode: RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}

	found := false
	for _, f := range resp.GraphFacts {
		if strings.EqualFold(f.Predicate, "uses") &&
			strings.Contains(strings.ToLower(f.Subject), "alice") &&
			strings.Contains(strings.ToLower(f.Object), "apollo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected graph fact Alice -uses-> Apollo, got %+v", resp.GraphFacts)
	}
	if !strings.Contains(resp.ContextPack.Text, "[GRAPH_FACTS]") {
		t.Errorf("context pack should include a GRAPH_FACTS section, got:\n%s", resp.ContextPack.Text)
	}
	// Structural edges (chunk -mentions-> entity) must not leak in as facts.
	for _, f := range resp.GraphFacts {
		if strings.EqualFold(f.Predicate, "mentions") {
			t.Errorf("structural edge leaked into graph facts: %+v", f)
		}
	}
}
