package graphflow

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// TestOrganizeFromBrainBuildsEntityGraphFromMemories verifies that free-text
// memories (which have no graph presence) gain entity nodes and entity↔entity
// co-occurrence relations after an organize pass — deterministically, no LLM.
func TestOrganizeFromBrainBuildsEntityGraphFromMemories(t *testing.T) {
	dbPath := fmt.Sprintf("test_organize_%d.db", testname.Nano())
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
	ctx := context.Background()

	for i, content := range []string{
		"Apollo and Zephyr are projects at Acme.",
		"Acme migrated Apollo to Kubernetes last quarter.",
	} {
		if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
			MemoryID: fmt.Sprintf("m%d", i),
			Scope:    cortexdb.MemoryScopeGlobal,
			Content:  content,
		}); err != nil {
			t.Fatalf("save memory %d: %v", i, err)
		}
	}

	report, err := OrganizeFromBrain(ctx, db, OrganizeOptions{})
	if err != nil {
		t.Fatalf("organize: %v", err)
	}
	if report.DocumentsScanned < 2 {
		t.Fatalf("expected >=2 memories scanned, got %d", report.DocumentsScanned)
	}
	if report.EntityCount == 0 {
		t.Fatalf("expected entities to be created, got 0")
	}

	// Apollo should be an entity node now.
	node, err := db.Graph().GetNode(ctx, "entity:apollo")
	if err != nil || node == nil {
		t.Fatalf("expected entity:apollo node, err=%v node=%v", err, node)
	}

	// And it should have at least one entity↔entity relation.
	edges, err := db.Graph().GetEdges(ctx, "entity:apollo", "both")
	if err != nil {
		t.Fatalf("get edges: %v", err)
	}
	entityEdge := false
	for _, e := range edges {
		if strings.HasPrefix(e.FromNodeID, "entity:") && strings.HasPrefix(e.ToNodeID, "entity:") {
			entityEdge = true
		}
	}
	if !entityEdge {
		t.Fatalf("expected an entity↔entity relation incident to entity:apollo, got %d edges", len(edges))
	}

	// Idempotent: a second pass must not error.
	if _, err := OrganizeFromBrain(ctx, db, OrganizeOptions{}); err != nil {
		t.Fatalf("second organize pass errored: %v", err)
	}
}
