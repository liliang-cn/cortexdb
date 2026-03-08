package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
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

type fixtureExtractor struct{}

func (fixtureExtractor) Extract(_ context.Context, text string) (*GraphExtraction, error) {
	lower := strings.ToLower(text)
	extraction := &GraphExtraction{}

	if strings.Contains(lower, "alice") {
		extraction.Entities = append(extraction.Entities, GraphEntity{Name: "Alice"})
	}
	if strings.Contains(lower, "acme") {
		extraction.Entities = append(extraction.Entities, GraphEntity{Name: "Acme"})
	}
	if strings.Contains(lower, "graph") {
		extraction.Entities = append(extraction.Entities, GraphEntity{Name: "GraphRAG"})
	}
	if strings.Contains(lower, "research") {
		extraction.Entities = append(extraction.Entities, GraphEntity{Name: "Research"})
	}
	if strings.Contains(lower, "alice works at acme") {
		extraction.Relationships = append(extraction.Relationships, GraphRelationship{
			From: "Alice",
			To:   "Acme",
			Type: "works_at",
		})
	}

	return extraction, nil
}

func TestGraphRAGInsertAndSearch(t *testing.T) {
	dbPath := fmt.Sprintf("test_graphrag_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath), WithEmbedder(newKeywordEmbedder(
		"alice", "acme", "graphrag", "research", "works", "company", "retrieval",
	)))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	doc := GraphRAGDocument{
		ID:    "doc-1",
		Title: "Alice at Acme",
		Content: strings.Join([]string{
			"Alice works at Acme and leads GraphRAG research.",
			"Acme uses GraphRAG retrieval to organize research knowledge.",
		}, "\n\n"),
	}

	ingest, err := db.InsertGraphDocument(ctx, doc, GraphRAGIngestOptions{
		ChunkSize: 32,
		Extractor: fixtureExtractor{},
	})
	if err != nil {
		t.Fatalf("insert graph document: %v", err)
	}

	if ingest.DocumentNodeID != "doc:doc-1" {
		t.Fatalf("unexpected document node id: %s", ingest.DocumentNodeID)
	}
	if len(ingest.ChunkNodeIDs) != 2 {
		t.Fatalf("expected 2 chunk nodes, got %d", len(ingest.ChunkNodeIDs))
	}
	if len(ingest.EntityNodeIDs) < 3 {
		t.Fatalf("expected entity nodes to be created, got %d", len(ingest.EntityNodeIDs))
	}

	docNode, err := db.Graph().GetNode(ctx, ingest.DocumentNodeID)
	if err != nil {
		t.Fatalf("get document node: %v", err)
	}
	if docNode.NodeType != "document" {
		t.Fatalf("expected document node, got %s", docNode.NodeType)
	}

	chunkEdges, err := db.Graph().GetEdges(ctx, ingest.DocumentNodeID, "out")
	if err != nil {
		t.Fatalf("get document edges: %v", err)
	}
	if len(chunkEdges) != 2 {
		t.Fatalf("expected 2 document->chunk edges, got %d", len(chunkEdges))
	}

	results, err := db.SearchGraphRAG(ctx, "Where does Alice work?", GraphRAGQueryOptions{
		TopK:             2,
		MaxHops:          2,
		MaxRelatedChunks: 2,
		MaxContextChunks: 4,
	})
	if err != nil {
		t.Fatalf("search graphrag: %v", err)
	}

	if len(results.Chunks) == 0 {
		t.Fatal("expected chunk results")
	}
	if !strings.Contains(strings.ToLower(results.Context), "alice works at acme") {
		t.Fatalf("expected context to include seed chunk, got %q", results.Context)
	}
	if len(results.Entities) == 0 {
		t.Fatal("expected graph entities in retrieval result")
	}
	if !containsString(results.Entities, "Alice") || !containsString(results.Entities, "Acme") {
		t.Fatalf("expected Alice and Acme in entities, got %v", results.Entities)
	}
	if len(results.Chunks) > 0 && results.Chunks[0].RerankScore <= 0 {
		t.Fatalf("expected rerank score to be populated, got %+v", results.Chunks[0])
	}
}

func TestGraphRAGRequiresEmbedder(t *testing.T) {
	dbPath := fmt.Sprintf("test_graphrag_no_embedder_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	_, err = db.InsertGraphDocument(ctx, GraphRAGDocument{
		ID:      "doc-1",
		Content: "Alice works at Acme.",
	}, GraphRAGIngestOptions{})
	if err != ErrEmbedderNotConfigured {
		t.Fatalf("expected ErrEmbedderNotConfigured, got %v", err)
	}

	_, err = db.SearchGraphRAG(ctx, "Alice", GraphRAGQueryOptions{})
	if err != ErrEmbedderNotConfigured {
		t.Fatalf("expected ErrEmbedderNotConfigured, got %v", err)
	}
}

func TestGraphRAGDefaultExtractor(t *testing.T) {
	dbPath := fmt.Sprintf("test_graphrag_default_extractor_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath), WithEmbedder(newKeywordEmbedder("alice", "acme", "graph")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	ingest, err := db.InsertGraphDocument(ctx, GraphRAGDocument{
		ID:      "doc-2",
		Title:   "Graph Note",
		Content: "Alice presented Graph ideas at Acme.",
	}, GraphRAGIngestOptions{ChunkSize: 32})
	if err != nil {
		t.Fatalf("insert graph document: %v", err)
	}

	if !containsString(ingest.EntityNodeIDs, "entity:alice") {
		t.Fatalf("expected default extractor to create Alice entity, got %v", ingest.EntityNodeIDs)
	}
	if !containsString(ingest.EntityNodeIDs, "entity:acme") {
		t.Fatalf("expected default extractor to create Acme entity, got %v", ingest.EntityNodeIDs)
	}
}

func TestGraphRAGContextPackingAndDiversity(t *testing.T) {
	dbPath := fmt.Sprintf("test_graphrag_packing_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath), WithEmbedder(newKeywordEmbedder(
		"alice", "acme", "graphrag", "research", "retrieval", "pricing", "beta", "launch",
	)))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	docs := []GraphRAGDocument{
		{
			ID:    "doc-a",
			Title: "Alice Research",
			Content: strings.Join([]string{
				"Alice works at Acme and leads GraphRAG research for retrieval quality.",
				"Alice works at Acme and leads GraphRAG research for retrieval quality again.",
				"Acme pricing and launch plans are separate from GraphRAG research.",
			}, "\n\n"),
		},
		{
			ID:      "doc-b",
			Title:   "Acme Launch",
			Content: "Acme plans a beta launch after research milestones are complete.",
		},
	}

	for _, doc := range docs {
		if _, err := db.InsertGraphDocument(ctx, doc, GraphRAGIngestOptions{
			ChunkSize: 16,
			Extractor: fixtureExtractor{},
		}); err != nil {
			t.Fatalf("insert graph document %s: %v", doc.ID, err)
		}
	}

	result, err := db.SearchGraphRAG(ctx, "What is Alice doing at Acme research?", GraphRAGQueryOptions{
		TopK:             4,
		MaxHops:          2,
		MaxRelatedChunks: 4,
		MaxContextChunks: 2,
		MaxContextChars:  220,
		PerDocumentLimit: 1,
		DiversityLambda:  0.7,
	})
	if err != nil {
		t.Fatalf("search graphrag: %v", err)
	}

	if len(result.Chunks) == 0 {
		t.Fatal("expected packed chunks")
	}
	if len(result.Chunks) > 2 {
		t.Fatalf("expected at most 2 packed chunks, got %d", len(result.Chunks))
	}
	if len(result.Context) > 260 {
		t.Fatalf("expected packed context under budget, got %d chars", len(result.Context))
	}

	docSeen := make(map[string]struct{})
	for _, chunk := range result.Chunks {
		if chunk.DocumentID == "" {
			t.Fatalf("expected document id on packed chunk: %+v", chunk)
		}
		if _, exists := docSeen[chunk.DocumentID]; exists {
			t.Fatalf("expected per-document diversity, got duplicate doc in %v", result.Chunks)
		}
		docSeen[chunk.DocumentID] = struct{}{}
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
