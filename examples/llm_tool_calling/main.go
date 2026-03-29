package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	ctx := context.Background()
	dbPath := fmt.Sprintf("llm_tool_calling_example_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	tools := db.GraphRAGTools()

	if _, err := tools.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{
		SchemaID: "tool-schema",
		Activate: true,
		EntityTypes: []cortexdb.OntologyEntityType{
			{Name: "entity"},
			{Name: "person"},
			{Name: "organization"},
			{Name: "city"},
		},
		RelationTypes: []cortexdb.OntologyRelationType{
			{Name: "works_at", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"organization"}},
			{Name: "located_in", AllowedFromTypes: []string{"organization"}, AllowedToTypes: []string{"city"}},
			{Name: "works_in_city", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"city"}},
		},
	}); err != nil {
		log.Fatalf("save ontology schema: %v", err)
	}

	mustSaveKnowledge(ctx, tools, cortexdb.KnowledgeSaveRequest{
		KnowledgeID: "alice-employment",
		Title:       "Alice at Acme",
		Content:     "Alice works at Acme.",
		ChunkSize:   24,
		Entities: []cortexdb.ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{chunkID("alice-employment")}},
			{Name: "Acme", Type: "organization", ChunkIDs: []string{chunkID("alice-employment")}},
		},
		Relations: []cortexdb.ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at"},
		},
	})

	mustSaveKnowledge(ctx, tools, cortexdb.KnowledgeSaveRequest{
		KnowledgeID: "acme-berlin",
		Title:       "Acme in Berlin",
		Content:     "Acme is headquartered in Berlin.",
		ChunkSize:   24,
		Entities: []cortexdb.ToolEntityInput{
			{Name: "Acme", Type: "organization", ChunkIDs: []string{chunkID("acme-berlin")}},
			{Name: "Berlin", Type: "city", ChunkIDs: []string{chunkID("acme-berlin")}},
		},
		Relations: []cortexdb.ToolRelationInput{
			{From: "Acme", To: "Berlin", Type: "located_in"},
		},
	})

	mustSaveKnowledge(ctx, tools, cortexdb.KnowledgeSaveRequest{
		KnowledgeID: "alice-berlin-proof",
		Title:       "Alice, Acme, and Berlin",
		Content:     "Alice works at Acme. Acme is headquartered in Berlin.",
		ChunkSize:   24,
		Entities: []cortexdb.ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{chunkID("alice-berlin-proof")}},
			{Name: "Acme", Type: "organization", ChunkIDs: []string{chunkID("alice-berlin-proof")}},
			{Name: "Berlin", Type: "city", ChunkIDs: []string{chunkID("alice-berlin-proof")}},
		},
		Relations: []cortexdb.ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at"},
			{From: "Acme", To: "Berlin", Type: "located_in"},
		},
	})

	if _, err := tools.ApplyInferenceRules(ctx, cortexdb.ApplyInferenceRequest{
		DocumentID:     "alice-berlin-proof",
		DeleteExisting: true,
		Rules: []cortexdb.InferenceRule{
			{
				RuleID:             "employment_city",
				LeftRelationType:   "works_at",
				RightRelationType:  "located_in",
				ResultRelationType: "works_in_city",
			},
		},
	}); err != nil {
		log.Fatalf("apply inference rules: %v", err)
	}

	if _, err := tools.SaveMemory(ctx, cortexdb.MemorySaveRequest{
		MemoryID:  "memory-style",
		UserID:    "alice",
		Scope:     cortexdb.MemoryScopeUser,
		Namespace: "assistant",
		Content:   "Alice prefers concise answers with bullet points only when needed.",
		Metadata: map[string]any{
			"kind": "response_style",
		},
		Importance: 0.9,
	}); err != nil {
		log.Fatalf("save memory: %v", err)
	}

	knowledgePlan := cortexdb.RetrievalPlan{
		Query:            "Where does Alice work?",
		Keywords:         []string{"Alice", "Acme", "works", "employer"},
		AlternateQueries: []string{"Alice works at Acme", "Acme headquarters"},
		EntityNames:      []string{"Alice", "Acme"},
		RetrievalMode:    cortexdb.RetrievalModeGraph,
	}

	knowledgeResp, err := tools.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
		TopK:                1,
		MaxHops:             2,
		MaxRelatedChunks:    2,
		MaxContextChunks:    4,
		MaxContextChars:     600,
		PerDocumentLimit:    2,
		GraphLight:          true,
		MaxExpansionSeeds:   1,
		MaxTraversalNodes:   4,
		MaxEntitiesPerChunk: 2,
		Plan:                &knowledgePlan,
	})
	if err != nil {
		log.Fatalf("search knowledge via tools: %v", err)
	}

	memoryPlan := cortexdb.RetrievalPlan{
		Query:            "How should I answer Alice?",
		Keywords:         []string{"Alice", "concise", "answers"},
		AlternateQueries: []string{"preferred response style", "assistant tone"},
		RetrievalMode:    cortexdb.RetrievalModeLexical,
	}

	memoryResp, err := tools.SearchMemory(ctx, cortexdb.MemorySearchRequest{
		UserID:    "alice",
		Scope:     cortexdb.MemoryScopeUser,
		Namespace: "assistant",
		TopK:      1,
		Plan:      &memoryPlan,
	})
	if err != nil {
		log.Fatalf("search memory via tools: %v", err)
	}

	fmt.Println("=== No-Embedder Tool-Calling Example ===")
	fmt.Printf("Available tool definitions: %d\n", len(tools.Definitions()))
	fmt.Printf("Knowledge retrieval decision: %+v\n", knowledgeResp.Decision)
	fmt.Println("Knowledge hits:")
	for _, hit := range knowledgeResp.Results {
		fmt.Printf("- %s | score=%.3f | entities=%v\n", hit.Title, hit.Score, hit.Entities)
	}
	fmt.Printf("Knowledge context:\n%s\n", knowledgeResp.Context)
	fmt.Println("Memory hits:")
	for _, hit := range memoryResp.Results {
		fmt.Printf("- %s | score=%.3f\n", hit.Memory.Content, hit.Score)
	}
	fmt.Println("The same tool surface can also be exposed over MCP stdio with `db.RunMCPStdio(...)`.")
}

func mustSaveKnowledge(ctx context.Context, tools *cortexdb.GraphRAGToolbox, req cortexdb.KnowledgeSaveRequest) {
	if _, err := tools.SaveKnowledge(ctx, req); err != nil {
		log.Fatalf("save knowledge %s: %v", req.KnowledgeID, err)
	}
}

func chunkID(knowledgeID string) string {
	return fmt.Sprintf("chunk:%s:%03d", knowledgeID, 0)
}
