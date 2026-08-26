package cortexdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func openMaintDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "maint.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func putTypedNode(t *testing.T, db *DB, name, nodeType string) string {
	t.Helper()
	id := EntityNodeID(name)
	res, err := db.graph.UpsertNodesBatch(context.Background(), []*graph.GraphNode{{
		ID: id, Vector: []float32{0.1, 0.2}, Content: name, NodeType: nodeType,
	}})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		t.Fatalf("put node %s: %v", name, err)
	}
	return id
}

// Every junk name here sat in a real store. The prune must take the grammar and
// leave both the real entities and anything somebody typed — a declared type
// means a person said what the thing is, and no name rule overrides a person.
func TestPruneJunkEntitiesTakesGrammarLeavesNamesAndTyped(t *testing.T) {
	db := openMaintDB(t)
	ctx := context.Background()

	junk1 := putTypedNode(t, db, "This", "entity")
	junk2 := putTypedNode(t, db, "Only", "entity")
	junk3 := putTypedNode(t, db, "Requires", "entity")
	keepReal := putTypedNode(t, db, "CortexDB", "entity")
	keepTyped := putTypedNode(t, db, "Next", "milestone") // declared, junk-looking name
	// Edges hanging off a junk node must go with it.
	putEdge(t, db, "edge:co:junk", junk1, keepReal, "co_occurs")

	dry, err := db.PruneJunkEntities(ctx, GraphMaintenanceOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Pruned != 3 {
		t.Fatalf("dry run should name 3 victims, got %+v", dry)
	}

	report, err := db.PruneJunkEntities(ctx, GraphMaintenanceOptions{})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if report.Pruned != 3 || report.EdgesRemoved != 1 {
		t.Fatalf("expected 3 nodes and 1 edge removed, got %+v", report)
	}

	// GetNodesBatch returns found rows in store order, not aligned to the
	// input, so survival is a set question.
	remaining, err := db.graph.GetNodesBatch(ctx, []string{junk1, junk2, junk3, keepReal, keepTyped})
	if err != nil {
		t.Fatal(err)
	}
	alive := map[string]bool{}
	for _, n := range remaining {
		if n != nil {
			alive[n.ID] = true
		}
	}
	for id, want := range map[string]bool{junk1: false, junk2: false, junk3: false, keepReal: true, keepTyped: true} {
		if alive[id] != want {
			t.Errorf("node %s survival = %v, want %v", id, alive[id], want)
		}
	}
}

// The backlog: memories saved before graph presence existed. After the reindex
// they must be reachable through entity_names exactly like a new save.
func TestReindexMemoryGraphMakesBacklogReachable(t *testing.T) {
	db := openMaintDB(t)
	ctx := context.Background()

	// Saved without entities — no graph presence, like every pre-2.74 memory.
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "old-1", Scope: "global",
		Content: "We deployed CortexDB behind the DRBD cluster and the failover held.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "old-2", Scope: "global",
		Content: "笔记里只有中文没有任何实体样的词。",
	}); err != nil {
		t.Fatal(err)
	}

	dry, err := db.ReindexMemoryGraph(ctx, GraphMaintenanceOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Indexed != 1 || dry.Skipped != 1 {
		t.Fatalf("dry run: want 1 indexed 1 skipped, got %+v", dry)
	}

	report, err := db.ReindexMemoryGraph(ctx, GraphMaintenanceOptions{})
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if report.Indexed != 1 {
		t.Fatalf("expected 1 memory indexed, got %+v", report)
	}

	res, err := db.SearchMemory(ctx, MemorySearchRequest{
		Query:       "what happened with that project",
		Scope:       "global",
		EntityNames: []string{"CortexDB"},
		TopK:        3,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Results) == 0 || res.Results[0].Memory.ID != "old-1" {
		t.Fatalf("backlog memory not reachable through its entity: %+v", res.Results)
	}

	// Second pass changes nothing it already wrote — upserts on stable ids.
	again, err := db.ReindexMemoryGraph(ctx, GraphMaintenanceOptions{})
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if again.Indexed != 1 {
		t.Fatalf("second pass should reindex the same memory idempotently, got %+v", again)
	}
}
