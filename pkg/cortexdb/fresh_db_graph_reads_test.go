package cortexdb

import (
	"context"
	"path/filepath"
	"testing"
)

// TestFreshDBGraphReadsReturnEmpty is the regression test for the class of
// bug where graph tables were only created lazily by write paths
// (InitGraphSchema), so any read-only tool hitting a fresh database failed
// with "SQL logic error: no such table: graph_nodes" (or graph_edges)
// instead of returning an empty result. A downstream fetcher scanning an
// empty database must get an empty set, not an error.
func TestFreshDBGraphReadsReturnEmpty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open fresh db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	tools := db.GraphRAGTools()

	t.Run("find_nodes", func(t *testing.T) {
		resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"anything"}})
		if err != nil {
			t.Fatalf("FindNodes on fresh db: %v", err)
		}
		for _, match := range resp.Matches {
			if len(match.Nodes) != 0 {
				t.Fatalf("FindNodes on fresh db returned nodes: %+v", match.Nodes)
			}
		}
	})

	t.Run("object_set_resolve", func(t *testing.T) {
		resolved, err := db.ResolveObjectSet(ctx, ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"})
		if err != nil {
			t.Fatalf("ResolveObjectSet on fresh db: %v", err)
		}
		if len(resolved) != 0 {
			t.Fatalf("ResolveObjectSet on fresh db returned %d ids, want 0", len(resolved))
		}
	})

	t.Run("expand_graph", func(t *testing.T) {
		resp, err := tools.ExpandGraph(ctx, ToolExpandGraphRequest{NodeIDs: []string{"entity:person:nobody"}})
		if err != nil {
			t.Fatalf("ExpandGraph on fresh db: %v", err)
		}
		if len(resp.Edges) != 0 {
			t.Fatalf("ExpandGraph on fresh db returned %d edges, want 0", len(resp.Edges))
		}
	})

	t.Run("knowledge_memory_recall", func(t *testing.T) {
		resp, err := tools.KnowledgeMemoryRecall(ctx, KnowledgeMemoryRecallRequest{Query: "who uses apollo"})
		if err != nil {
			t.Fatalf("KnowledgeMemoryRecall on fresh db: %v", err)
		}
		if len(resp.GraphFacts) != 0 {
			t.Fatalf("KnowledgeMemoryRecall on fresh db returned %d graph facts, want 0", len(resp.GraphFacts))
		}
	})
}
