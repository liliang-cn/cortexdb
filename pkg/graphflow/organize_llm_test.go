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

// organizeFakeLLM returns a canned payload, capturing the prompts it saw so a
// test can assert the brain text was actually forwarded.
type organizeFakeLLM struct {
	response string
	calls    int
	lastUser string
}

func (f *organizeFakeLLM) GenerateJSON(_ context.Context, _ string, userPrompt string) ([]byte, error) {
	f.calls++
	f.lastUser = userPrompt
	return []byte(f.response), nil
}

// TestOrganizeFromBrainLLMDistillsTypedGraph verifies the LLM path: it forwards
// brain text to the generator, parses the distilled entities/relations (even
// wrapped in a <think> block and prose), writes typed entity nodes, and creates
// the stated relation edge with the model's relation type.
func TestOrganizeFromBrainLLMDistillsTypedGraph(t *testing.T) {
	dbPath := fmt.Sprintf("test_organize_llm_%d.db", time.Now().UnixNano())
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

	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
		MemoryID: "m0",
		Scope:    cortexdb.MemoryScopeGlobal,
		Content:  "CortexDB uses SQLite as its storage kernel.",
	}); err != nil {
		t.Fatalf("save memory: %v", err)
	}

	// Reasoning-model-style output: a <think> block, prose, then the JSON. A
	// relation endpoint ("SQLite") is intentionally omitted from entities to
	// verify endpoints are backfilled. Duplicate relation to verify dedup.
	gen := &organizeFakeLLM{response: `<think>let me extract</think>
Here is the graph:
{"entities":[{"name":"CortexDB","type":"project"}],
 "relations":[{"from":"CortexDB","to":"SQLite","type":"uses"},
              {"from":"CortexDB","to":"SQLite","type":"uses"}]}`}

	report, err := OrganizeFromBrain(ctx, db, OrganizeOptions{LLM: gen})
	if err != nil {
		t.Fatalf("organize (llm): %v", err)
	}
	if gen.calls == 0 {
		t.Fatalf("expected the LLM generator to be called")
	}
	if !strings.Contains(gen.lastUser, "CortexDB uses SQLite") {
		t.Fatalf("expected brain text forwarded to the model, got: %q", gen.lastUser)
	}
	// Two distinct entities: CortexDB (from entities) + SQLite (backfilled endpoint).
	if report.EntityCount != 2 {
		t.Fatalf("expected 2 entities, got %d", report.EntityCount)
	}
	// Duplicate relation collapses to one.
	if report.RelationCount != 1 {
		t.Fatalf("expected 1 relation after dedup, got %d", report.RelationCount)
	}

	// CortexDB keeps its typed label; SQLite falls back to "entity".
	cnode, err := db.Graph().GetNode(ctx, "entity:cortexdb")
	if err != nil || cnode == nil {
		t.Fatalf("expected entity:cortexdb node, err=%v node=%v", err, cnode)
	}
	if cnode.NodeType != "project" {
		t.Fatalf("expected CortexDB typed as project, got %q", cnode.NodeType)
	}
	if snode, _ := db.Graph().GetNode(ctx, "entity:sqlite"); snode == nil {
		t.Fatalf("expected backfilled entity:sqlite node")
	}

	// The "uses" relation edge exists between them.
	edges, err := db.Graph().GetEdges(ctx, "entity:cortexdb", "both")
	if err != nil {
		t.Fatalf("get edges: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.EdgeType == "uses" && e.ToNodeID == "entity:sqlite" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 'uses' edge cortexdb->sqlite, got %d edges", len(edges))
	}
}

func TestNormalizeTypesFallBack(t *testing.T) {
	if got := normalizeEntityType("Situation"); got != "entity" {
		t.Fatalf("garbage entity type should fall back to entity, got %q", got)
	}
	if got := normalizeEntityType("Person"); got != "person" {
		t.Fatalf("expected person, got %q", got)
	}
	if got := normalizeRelationType("Depends On"); got != "depends_on" {
		t.Fatalf("expected depends_on, got %q", got)
	}
	if got := normalizeRelationType("!!!"); got != "related_to" {
		t.Fatalf("unusable relation type should fall back, got %q", got)
	}
}
