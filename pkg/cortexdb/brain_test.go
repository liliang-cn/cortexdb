package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

type stubKnowledgeMemoryReflector struct {
	summary string
}

func (s stubKnowledgeMemoryReflector) Reflect(_ context.Context, _ KnowledgeMemoryReflectRequest, input KnowledgeMemoryReflectInput) (*KnowledgeMemoryReflection, error) {
	return &KnowledgeMemoryReflection{
		Summary:            s.summary,
		Themes:             []string{"plans", "coordination"},
		Entities:           input.Recall.Entities,
		Facts:              []string{"custom reflection"},
		SourceMemoryIDs:    input.Recall.ContextPack.MemoryIDs,
		SourceKnowledgeIDs: input.Recall.ContextPack.KnowledgeIDs,
		SourceChunkIDs:     input.Recall.ContextPack.ChunkIDs,
		ContextPack:        input.Recall.ContextPack,
	}, nil
}

func TestKnowledgeMemoryRememberRecallAndContextPack(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_memory_recall_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	KnowledgeMemory := db.KnowledgeMemory()

	for _, req := range []KnowledgeMemoryRememberRequest{
		{
			MemoryID:  "memory-1",
			SessionID: "session-1",
			Scope:     MemoryScopeSession,
			Content:   "Dinner with Alice at Wanda Plaza tomorrow at 17:00.",
			Metadata: map[string]any{
				"kind": "reminder",
			},
			Importance: 0.9,
		},
		{
			MemoryID:   "memory-2",
			SessionID:  "session-1",
			Scope:      MemoryScopeSession,
			Content:    "Bring the project notes for the Wanda dinner.",
			Importance: 0.7,
		},
	} {
		if _, err := KnowledgeMemory.Remember(ctx, req); err != nil {
			t.Fatalf("remember %s: %v", req.MemoryID, err)
		}
	}

	recallResp, err := KnowledgeMemory.Recall(ctx, KnowledgeMemoryRecallRequest{
		Query:            "Wanda dinner tomorrow",
		SessionID:        "session-1",
		Scope:            MemoryScopeSession,
		DisableKnowledge: true,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(recallResp.Memories) == 0 {
		t.Fatal("expected recalled memories")
	}
	if !strings.Contains(recallResp.ContextPack.Text, "Wanda Plaza") {
		t.Fatalf("expected context pack to include memory text, got %q", recallResp.ContextPack.Text)
	}
	if len(recallResp.ContextPack.MemoryIDs) != len(recallResp.Memories) {
		t.Fatalf("expected context pack memory IDs to match memories, got %+v", recallResp.ContextPack.MemoryIDs)
	}

	contextResp, err := KnowledgeMemory.BuildContextPack(ctx, KnowledgeMemoryBuildContextPackRequest{
		Query:            "project notes for dinner",
		SessionID:        "session-1",
		Scope:            MemoryScopeSession,
		DisableKnowledge: true,
	})
	if err != nil {
		t.Fatalf("build context pack: %v", err)
	}
	if contextResp.ContextPack.Text == "" {
		t.Fatal("expected built context pack text")
	}
}

func TestKnowledgeMemoryPromoteExpandAndTraverse(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_memory_graph_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	KnowledgeMemory := db.KnowledgeMemory()

	if _, err := KnowledgeMemory.Remember(ctx, KnowledgeMemoryRememberRequest{
		MemoryID:  "memory-graph",
		SessionID: "session-graph",
		Scope:     MemoryScopeSession,
		Content:   "Alice works at Acme and leads the graph platform.",
	}); err != nil {
		t.Fatalf("remember graph memory: %v", err)
	}

	promoteResp, err := KnowledgeMemory.PromoteToKnowledge(ctx, KnowledgeMemoryPromoteToKnowledgeRequest{
		MemoryIDs:   []string{"memory-graph"},
		KnowledgeID: "knowledge-graph",
		Title:       "Alice at Acme",
		Entities: []ToolEntityInput{
			{Name: "Alice", ChunkIDs: []string{"chunk:knowledge-graph:000"}},
			{Name: "Acme", ChunkIDs: []string{"chunk:knowledge-graph:000"}},
		},
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at", ChunkIDs: []string{"chunk:knowledge-graph:000"}},
		},
	})
	if err != nil {
		t.Fatalf("promote to knowledge: %v", err)
	}
	if promoteResp.Knowledge.ID != "knowledge-graph" {
		t.Fatalf("unexpected promoted knowledge ID: %s", promoteResp.Knowledge.ID)
	}

	expandResp, err := KnowledgeMemory.ExpandEntityContext(ctx, KnowledgeMemoryExpandEntityContextRequest{
		EntityNames: []string{"Alice"},
		MaxHops:     2,
		TopKChunks:  4,
	})
	if err != nil {
		t.Fatalf("expand entity context: %v", err)
	}
	if len(expandResp.Nodes) == 0 {
		t.Fatal("expected expanded graph nodes")
	}
	if expandResp.ContextPack.Text == "" {
		t.Fatal("expected graph context pack text")
	}

	neighborsResp, err := KnowledgeMemory.Neighbors(ctx, KnowledgeMemoryNeighborsRequest{
		EntityName: "Alice",
		MaxDepth:   1,
		Direction:  "both",
		Limit:      8,
	})
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(neighborsResp.Neighbors) == 0 {
		t.Fatal("expected graph neighbors")
	}

	pathResp, err := KnowledgeMemory.ShortestPath(ctx, KnowledgeMemoryShortestPathRequest{
		FromEntityName: "Alice",
		ToEntityName:   "Acme",
	})
	if err != nil {
		t.Fatalf("shortest path: %v", err)
	}
	if pathResp.Path == nil || pathResp.Path.Distance != 1 {
		t.Fatalf("expected direct path between Alice and Acme, got %+v", pathResp.Path)
	}
}

func TestKnowledgeMemoryReflectAndConsolidate(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_memory_reflect_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath), WithKnowledgeMemoryReflector(stubKnowledgeMemoryReflector{summary: "Focus on the Acme launch and dinner logistics."}))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	KnowledgeMemory := db.KnowledgeMemory()

	for _, req := range []KnowledgeMemoryRememberRequest{
		{
			MemoryID:  "memory-reflect-1",
			SessionID: "session-reflect",
			Scope:     MemoryScopeSession,
			Content:   "Prepare the Acme launch checklist before dinner.",
		},
		{
			MemoryID:  "memory-reflect-2",
			SessionID: "session-reflect",
			Scope:     MemoryScopeSession,
			Content:   "Dinner with Alice is at Wanda Plaza tomorrow.",
		},
	} {
		if _, err := KnowledgeMemory.Remember(ctx, req); err != nil {
			t.Fatalf("remember %s: %v", req.MemoryID, err)
		}
	}

	reflectResp, err := KnowledgeMemory.Reflect(ctx, KnowledgeMemoryReflectRequest{
		Recall: KnowledgeMemoryRecallRequest{
			Query:            "What should I keep in mind for Acme tomorrow?",
			SessionID:        "session-reflect",
			Scope:            MemoryScopeSession,
			DisableKnowledge: true,
		},
	})
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if reflectResp.Reflection.Summary != "Focus on the Acme launch and dinner logistics." {
		t.Fatalf("unexpected reflection summary: %q", reflectResp.Reflection.Summary)
	}

	consolidateResp, err := KnowledgeMemory.Consolidate(ctx, KnowledgeMemoryConsolidateRequest{
		Reflect: KnowledgeMemoryReflectRequest{
			Recall: KnowledgeMemoryRecallRequest{
				Query:            "Summarize the Acme dinner plan",
				SessionID:        "session-reflect",
				Scope:            MemoryScopeSession,
				DisableKnowledge: true,
			},
		},
		MemoryID:           "memory-summary",
		SessionID:          "session-reflect",
		Scope:              MemoryScopeSession,
		Importance:         0.95,
		PromoteToKnowledge: true,
		Promotion: &KnowledgeMemoryPromoteToKnowledgeRequest{
			KnowledgeID: "knowledge-summary",
			Title:       "Acme Dinner Plan",
		},
	})
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if consolidateResp.Memory.Content != "Focus on the Acme launch and dinner logistics." {
		t.Fatalf("unexpected consolidated memory content: %q", consolidateResp.Memory.Content)
	}
	if consolidateResp.Knowledge == nil || consolidateResp.Knowledge.ID != "knowledge-summary" {
		t.Fatalf("expected promoted knowledge, got %+v", consolidateResp.Knowledge)
	}

	getMemoryResp, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "memory-summary"})
	if err != nil {
		t.Fatalf("get consolidated memory: %v", err)
	}
	if flagged, ok := getMemoryResp.Memory.Metadata["knowledge_memory_summary"].(bool); !ok || !flagged {
		t.Fatalf("expected consolidated memory to be marked as KnowledgeMemory summary, got %+v", getMemoryResp.Memory.Metadata)
	}
}
