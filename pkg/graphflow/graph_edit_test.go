package graphflow

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func openEditTestDB(t *testing.T) (*cortexdb.DB, context.Context) {
	t.Helper()
	dbPath := fmt.Sprintf("test_gedit_%d.db", time.Now().UnixNano())
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	return db, context.Background()
}

func seedEditGraph(t *testing.T, db *cortexdb.DB, ctx context.Context) {
	t.Helper()
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		{Name: "CortexDB", Type: "project", Description: "old summary"},
		{Name: "SQLite", Type: "tool"},
		{Name: "Redis", Type: "tool"},
	}}); err != nil {
		t.Fatalf("seed entities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: []cortexdb.ToolRelationInput{
		{From: "CortexDB", To: "SQLite", Type: "uses"},
		{From: "CortexDB", To: "Redis", Type: "uses"},
	}}); err != nil {
		t.Fatalf("seed relations: %v", err)
	}
}

func countEdges(t *testing.T, db *cortexdb.DB, ctx context.Context, from, to string) int {
	t.Helper()
	var n int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE from_node_id = ? AND to_node_id = ?`,
		cortexdb.EntityNodeID(from), cortexdb.EntityNodeID(to)).Scan(&n); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	return n
}

// TestApplyGraphEditsAddUpdateDelete covers the full CRUD surface in one pass.
func TestApplyGraphEditsAddUpdateDelete(t *testing.T) {
	db, ctx := openEditTestDB(t)
	seedEditGraph(t, db, ctx)

	plan := GraphEditPlan{Edits: []GraphEdit{
		{Op: EditOpAdd, Kind: EditKindEntity, Name: "Go", Type: "technology"},
		{Op: EditOpUpdate, Kind: EditKindEntity, Name: "CortexDB", Type: "project", Summary: "new summary"},
		{Op: EditOpAdd, Kind: EditKindRelation, From: "CortexDB", To: "Go", RelType: "written_in"},
		{Op: EditOpDelete, Kind: EditKindRelation, From: "CortexDB", To: "Redis", RelType: "uses"},
		{Op: EditOpDelete, Kind: EditKindEntity, Name: "Redis"},
	}}

	report, err := ApplyGraphEdits(ctx, db, plan, GraphEditOptions{AllowDelete: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if report.EntitiesAdded != 1 || report.EntitiesUpdated != 1 {
		t.Fatalf("expected 1 add + 1 update, got added=%d updated=%d", report.EntitiesAdded, report.EntitiesUpdated)
	}
	if report.RelationsAdded != 1 || report.RelationsDeleted != 1 {
		t.Fatalf("expected 1 relation add + 1 delete, got +%d -%d", report.RelationsAdded, report.RelationsDeleted)
	}
	if report.EntitiesDeleted != 1 {
		t.Fatalf("expected 1 entity deleted, got %d", report.EntitiesDeleted)
	}

	// Redis is gone, Go exists, the new relation exists, the deleted one does not.
	if node, _ := db.Graph().GetNode(ctx, cortexdb.EntityNodeID("Redis")); node != nil {
		t.Fatalf("expected Redis to be deleted")
	}
	if node, _ := db.Graph().GetNode(ctx, cortexdb.EntityNodeID("Go")); node == nil {
		t.Fatalf("expected Go to be added")
	}
	if n := countEdges(t, db, ctx, "CortexDB", "Go"); n != 1 {
		t.Fatalf("expected the written_in edge, got %d", n)
	}
	if n := countEdges(t, db, ctx, "CortexDB", "Redis"); n != 0 {
		t.Fatalf("expected the CortexDB->Redis edge gone, got %d", n)
	}
	// The update took effect on the stored summary.
	node, err := db.Graph().GetNode(ctx, cortexdb.EntityNodeID("CortexDB"))
	if err != nil || node == nil {
		t.Fatalf("CortexDB should still exist: %v", err)
	}
	if desc, _ := node.Properties["description"].(string); desc != "new summary" {
		t.Fatalf("expected updated summary, got %q", desc)
	}
}

// TestApplyGraphEditsDeleteGated verifies deletes are refused unless opted in.
func TestApplyGraphEditsDeleteGated(t *testing.T) {
	db, ctx := openEditTestDB(t)
	seedEditGraph(t, db, ctx)

	plan := GraphEditPlan{Edits: []GraphEdit{
		{Op: EditOpDelete, Kind: EditKindEntity, Name: "Redis"},
	}}
	report, err := ApplyGraphEdits(ctx, db, plan, GraphEditOptions{}) // AllowDelete false
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if report.EntitiesDeleted != 0 || len(report.Skipped) == 0 {
		t.Fatalf("expected the delete to be skipped, got deleted=%d skipped=%v", report.EntitiesDeleted, report.Skipped)
	}
	if node, _ := db.Graph().GetNode(ctx, cortexdb.EntityNodeID("Redis")); node == nil {
		t.Fatalf("Redis must survive when AllowDelete is false")
	}
}

// TestApplyGraphEditsDryRunDoesNotMutate verifies dry runs only report.
func TestApplyGraphEditsDryRunDoesNotMutate(t *testing.T) {
	db, ctx := openEditTestDB(t)
	seedEditGraph(t, db, ctx)

	plan := GraphEditPlan{Edits: []GraphEdit{
		{Op: EditOpAdd, Kind: EditKindEntity, Name: "Kubernetes"},
		{Op: EditOpDelete, Kind: EditKindEntity, Name: "Redis"},
	}}
	report, err := ApplyGraphEdits(ctx, db, plan, GraphEditOptions{DryRun: true, AllowDelete: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if report.EntitiesAdded != 1 || report.EntitiesDeleted != 1 {
		t.Fatalf("dry run should report 1 add + 1 delete, got +%d -%d", report.EntitiesAdded, report.EntitiesDeleted)
	}
	if node, _ := db.Graph().GetNode(ctx, cortexdb.EntityNodeID("Kubernetes")); node != nil {
		t.Fatalf("dry run must not create Kubernetes")
	}
	if node, _ := db.Graph().GetNode(ctx, cortexdb.EntityNodeID("Redis")); node == nil {
		t.Fatalf("dry run must not delete Redis")
	}
}

// TestApplyGraphEditsDeleteCap verifies the delete cap protects the graph.
func TestApplyGraphEditsDeleteCap(t *testing.T) {
	db, ctx := openEditTestDB(t)
	seedEditGraph(t, db, ctx)

	plan := GraphEditPlan{Edits: []GraphEdit{
		{Op: EditOpDelete, Kind: EditKindEntity, Name: "Redis"},
		{Op: EditOpDelete, Kind: EditKindEntity, Name: "SQLite"},
	}}
	report, err := ApplyGraphEdits(ctx, db, plan, GraphEditOptions{AllowDelete: true, MaxDeletes: 1})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if report.EntitiesDeleted != 1 {
		t.Fatalf("expected the cap to allow exactly 1 delete, got %d", report.EntitiesDeleted)
	}
	if len(report.Skipped) == 0 {
		t.Fatalf("expected the capped delete to be reported as skipped")
	}
}

// editFakeLLM captures the prompt so the test can assert the model was shown
// the existing graph, and replies with a fixed edit plan.
type editFakeLLM struct {
	lastUser string
	resp     string
}

func (f *editFakeLLM) GenerateJSON(_ context.Context, _ string, user string) ([]byte, error) {
	f.lastUser = user
	return []byte(f.resp), nil
}

// TestUpdateGraphFromTextShowsExistingGraph is the no-embedder path: relevant
// existing entities must be found lexically and shown to the model, and the
// returned edits applied.
func TestUpdateGraphFromTextShowsExistingGraph(t *testing.T) {
	db, ctx := openEditTestDB(t)
	seedEditGraph(t, db, ctx)

	llm := &editFakeLLM{resp: `{"edits":[
		{"op":"delete","kind":"relation","from":"CortexDB","to":"Redis","rel_type":"uses","reason":"text says it no longer uses Redis"},
		{"op":"add","kind":"entity","name":"DuckDB","type":"tool"}
	]}`}

	text := "CortexDB no longer uses Redis. It now also supports DuckDB."
	report, err := UpdateGraphFromText(ctx, db, text, GraphEditOptions{LLM: llm, AllowDelete: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// The prompt must have carried the existing graph, including the relation
	// the text contradicts — that is what makes delete/update possible.
	if !strings.Contains(llm.lastUser, "CURRENT GRAPH") {
		t.Fatalf("prompt lacked the current-graph section: %q", llm.lastUser)
	}
	if !strings.Contains(llm.lastUser, "CortexDB") || !strings.Contains(llm.lastUser, "Redis") {
		t.Fatalf("prompt did not include the mentioned existing entities: %q", llm.lastUser)
	}
	if !strings.Contains(llm.lastUser, "-uses-> Redis") {
		t.Fatalf("prompt did not include the existing relation: %q", llm.lastUser)
	}

	if report.RelationsDeleted != 1 {
		t.Fatalf("expected the contradicted relation deleted, got %d", report.RelationsDeleted)
	}
	if n := countEdges(t, db, ctx, "CortexDB", "Redis"); n != 0 {
		t.Fatalf("CortexDB->Redis should be gone, got %d", n)
	}
	if node, _ := db.Graph().GetNode(ctx, cortexdb.EntityNodeID("DuckDB")); node == nil {
		t.Fatalf("expected DuckDB to be added")
	}
}

// TestUpdateGraphFromTextRequiresLLM verifies the guard.
func TestUpdateGraphFromTextRequiresLLM(t *testing.T) {
	db, ctx := openEditTestDB(t)
	if _, err := UpdateGraphFromText(ctx, db, "some text", GraphEditOptions{}); err == nil {
		t.Fatalf("expected an error without an LLM")
	}
}
