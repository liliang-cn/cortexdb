package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

type keywordEmbedder struct {
	vocab map[string]int
}

func newKeywordEmbedder(words ...string) *keywordEmbedder {
	vocab := make(map[string]int, len(words))
	for i, word := range words {
		vocab[word] = i
	}
	return &keywordEmbedder{vocab: vocab}
}

func (k *keywordEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, len(k.vocab))
	for _, token := range strings.Fields(strings.ToLower(text)) {
		token = strings.Trim(token, ".,!?;:\"'()")
		if idx, ok := k.vocab[token]; ok {
			vec[idx]++
		}
	}
	return vec, nil
}

func (k *keywordEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := k.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		vectors[i] = vec
	}
	return vectors, nil
}

func (k *keywordEmbedder) Dim() int {
	return len(k.vocab)
}

func main() {
	ctx := context.Background()
	dbPath := fmt.Sprintf("graphrag_embedder_example_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(
		cortexdb.DefaultConfig(dbPath),
		cortexdb.WithEmbedder(newKeywordEmbedder(
			"alice", "acme", "works", "berlin", "headquartered", "research", "graphrag", "city",
		)),
	)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{
		SchemaID: "company-graph",
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

	mustSaveKnowledge(ctx, db, cortexdb.KnowledgeSaveRequest{
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

	mustSaveKnowledge(ctx, db, cortexdb.KnowledgeSaveRequest{
		KnowledgeID: "acme-berlin",
		Title:       "Acme in Berlin",
		Content:     "Acme is headquartered in Berlin and runs GraphRAG research there.",
		ChunkSize:   24,
		Entities: []cortexdb.ToolEntityInput{
			{Name: "Acme", Type: "organization", ChunkIDs: []string{chunkID("acme-berlin")}},
			{Name: "Berlin", Type: "city", ChunkIDs: []string{chunkID("acme-berlin")}},
		},
		Relations: []cortexdb.ToolRelationInput{
			{From: "Acme", To: "Berlin", Type: "located_in"},
		},
	})

	mustSaveKnowledge(ctx, db, cortexdb.KnowledgeSaveRequest{
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

	inferenceResp, err := db.ApplyInferenceRules(ctx, cortexdb.ApplyInferenceRequest{
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
	})
	if err != nil {
		log.Fatalf("apply inference rules: %v", err)
	}

	searchResp, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
		Query:               "Where does Alice work?",
		TopK:                1,
		MaxHops:             2,
		MaxRelatedChunks:    2,
		MaxContextChunks:    4,
		MaxContextChars:     600,
		PerDocumentLimit:    2,
		RetrievalMode:       cortexdb.RetrievalModeGraph,
		MaxExpansionSeeds:   2,
		MaxTraversalNodes:   8,
		MaxEntitiesPerChunk: 3,
	})
	if err != nil {
		log.Fatalf("search knowledge: %v", err)
	}

	fmt.Println("=== Embedder-Backed GraphRAG Example ===")
	fmt.Printf("Ontology active: %s\n", "company-graph")
	fmt.Printf("Inference created edges: %v\n", inferenceResp.CreatedEdgeIDs)
	fmt.Printf("Retrieval decision: %+v\n", searchResp.Decision)
	fmt.Println("Top knowledge hits:")
	for _, hit := range searchResp.Results {
		fmt.Printf("- %s | score=%.3f | entities=%v\n", hit.Title, hit.Score, hit.Entities)
	}
	fmt.Printf("Expanded entities: %v\n", searchResp.Entities)
	fmt.Printf("Context:\n%s\n", searchResp.Context)
}

func mustSaveKnowledge(ctx context.Context, db *cortexdb.DB, req cortexdb.KnowledgeSaveRequest) {
	if _, err := db.SaveKnowledge(ctx, req); err != nil {
		log.Fatalf("save knowledge %s: %v", req.KnowledgeID, err)
	}
}

func chunkID(knowledgeID string) string {
	return fmt.Sprintf("chunk:%s:%03d", knowledgeID, 0)
}
