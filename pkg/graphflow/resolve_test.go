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

func openResolveTestDB(t *testing.T) (*cortexdb.DB, context.Context) {
	t.Helper()
	dbPath := fmt.Sprintf("test_resolve_%d.db", time.Now().UnixNano())
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

// TestResolveEntitiesDeterministicMerge verifies a separator variant
// ("Cortex DB" vs "CortexDB", which get distinct node ids but the same
// canonical key) collapses into one node with its edge repointed and deduped.
func TestResolveEntitiesDeterministicMerge(t *testing.T) {
	db, ctx := openResolveTestDB(t)
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		{Name: "CortexDB", Type: "project"}, // -> entity:cortexdb
		{Name: "Cortex DB", Type: "project"}, // -> entity:cortex_db (same key)
		{Name: "SQLite", Type: "tool"},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: []cortexdb.ToolRelationInput{
		{From: "CortexDB", To: "SQLite", Type: "uses"},
		{From: "Cortex DB", To: "SQLite", Type: "uses"},
	}}); err != nil {
		t.Fatalf("upsert relations: %v", err)
	}

	report, err := ResolveEntities(ctx, db, ResolveOptions{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if report.EntitiesMerged != 1 {
		t.Fatalf("expected 1 alias merged, got %d (groups=%v)", report.EntitiesMerged, report.Groups)
	}

	// Exactly one node remains in the cortexdb family.
	var cortexNodes int
	rows, _ := db.SQL().QueryContext(ctx, `SELECT content FROM graph_nodes WHERE id LIKE 'entity:%'`)
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		if canonicalKey(c) == "cortexdb" {
			cortexNodes++
		}
	}
	_ = rows.Close()
	if cortexNodes != 1 {
		t.Fatalf("expected 1 canonical cortexdb node, got %d", cortexNodes)
	}

	// The two uses→SQLite edges dedupe to one.
	var usesEdges int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE edge_type='uses' AND from_node_id LIKE 'entity:%' AND to_node_id LIKE 'entity:%'`).
		Scan(&usesEdges); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if usesEdges != 1 {
		t.Fatalf("expected repointed uses edge to dedupe to 1, got %d", usesEdges)
	}
}

// TestResolveEntitiesDryRun verifies dry-run reports without mutating.
func TestResolveEntitiesDryRun(t *testing.T) {
	db, ctx := openResolveTestDB(t)
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		{Name: "Data Base"}, // entity:data_base
		{Name: "Database"},  // entity:database, same key "database"
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	report, err := ResolveEntities(ctx, db, ResolveOptions{DryRun: true})
	if err != nil {
		t.Fatalf("resolve dry: %v", err)
	}
	if report.EntitiesMerged != 1 || len(report.Groups) != 1 {
		t.Fatalf("dry run should report 1 merge, got merged=%d groups=%d", report.EntitiesMerged, len(report.Groups))
	}
	var n int
	_ = db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_nodes WHERE id LIKE 'entity:%'`).Scan(&n)
	if n != 2 {
		t.Fatalf("dry run must not mutate; expected 2 nodes, got %d", n)
	}
}

// resolveFakeLLM returns a canned alias grouping for the acronym case.
type resolveFakeLLM struct{ resp string }

func (f resolveFakeLLM) GenerateJSON(context.Context, string, string) ([]byte, error) {
	return []byte(f.resp), nil
}

// TestResolveEntitiesLLMAcronym verifies the LLM path merges an acronym whose
// normalized key differs from its full form (K8s vs Kubernetes).
func TestResolveEntitiesLLMAcronym(t *testing.T) {
	db, ctx := openResolveTestDB(t)
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		{Name: "K8s"}, {Name: "Kubernetes"},
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	llm := resolveFakeLLM{resp: `{"groups":[{"canonical":"Kubernetes","aliases":["K8s"]}]}`}
	report, err := ResolveEntities(ctx, db, ResolveOptions{LLM: llm})
	if err != nil {
		t.Fatalf("resolve llm: %v", err)
	}
	if report.EntitiesMerged != 1 || len(report.Groups) != 1 || !strings.EqualFold(report.Groups[0].Canonical, "Kubernetes") {
		t.Fatalf("expected K8s merged into Kubernetes, got %+v", report.Groups)
	}
	// K8s node is gone; Kubernetes remains.
	if node, _ := db.Graph().GetNode(ctx, "entity:k8s"); node != nil {
		t.Fatalf("expected entity:k8s to be merged away")
	}
	if node, _ := db.Graph().GetNode(ctx, "entity:kubernetes"); node == nil {
		t.Fatalf("expected entity:kubernetes to survive")
	}
}

// A graph that holds more than one kind of thing cannot be resolved whole.
//
// Entity ids are derived from the name, but a code graph gives each symbol an
// id built from its path while leaving the bare name as the content — so seven
// packages each holding a main.go are seven nodes whose canonical key is the
// same "maingo". Resolution reads the content as the name, groups them, and
// merges seven distinct files into one with every edge repointed. The prose
// entities in the same store genuinely do want merging, so the answer is not
// to skip resolution but to say which types it applies to.
//
// Measured on a live base: 3309 nodes, of which 1043 were code symbols sharing
// 40-odd bare names against 8 genuine prose duplicate pairs.
func TestResolveEntitiesOnlyTouchesTheTypesAsked(t *testing.T) {
	db, ctx := openResolveTestDB(t)
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		// Two spellings of one concept — these should merge.
		{Name: "DRBD resource", Type: "DRBDResource"},
		{Name: "DRBDResource", Type: "DRBDResource"},
		// Two files that merely share a base name — these must not.
		{ID: "repo:cmd/cli/main.go", Name: "main.go", Type: "file"},
		{ID: "repo:cmd/agent/main.go", Name: "main.go", Type: "file"},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}

	report, err := ResolveEntities(ctx, db, ResolveOptions{NodeTypes: []string{"DRBDResource"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if report.EntitiesMerged != 1 {
		t.Fatalf("merged = %d (groups=%v), want only the prose pair", report.EntitiesMerged, report.Groups)
	}

	var files int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_nodes WHERE node_type = 'file'`).Scan(&files); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if files != 2 {
		t.Errorf("file nodes = %d, want both left alone", files)
	}
}

// Without the option, everything participates — the behaviour every existing
// caller already has.
func TestResolveEntitiesWithoutTypesConsidersEverything(t *testing.T) {
	db, ctx := openResolveTestDB(t)
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		{Name: "DRBD resource", Type: "DRBDResource"},
		{Name: "DRBDResource", Type: "DRBDResource"},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
	report, err := ResolveEntities(ctx, db, ResolveOptions{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if report.EntitiesMerged != 1 {
		t.Errorf("merged = %d, want the pair merged as before", report.EntitiesMerged)
	}
}
