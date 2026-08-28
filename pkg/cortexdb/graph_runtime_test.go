package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

func TestGraphRAGGraphCostControls(t *testing.T) {
	dbPath := fmt.Sprintf("test_graph_runtime_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath), WithEmbedder(newKeywordEmbedder(
		"alice", "works", "research", "acme", "beta", "labs", "roadmap", "milestone",
	)))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	docs := []GraphRAGDocument{
		{
			ID:      "doc-a",
			Title:   "Alice at Acme",
			Content: "Alice works at Acme research. Alice leads the alpha roadmap. The alpha roadmap has a milestone. Bob reviews the milestone with Alice.",
		},
		{
			ID:      "doc-b",
			Title:   "Alice at Beta Labs",
			Content: "Alice works at Beta Labs research. Beta Labs funds the beta roadmap. The beta roadmap milestone ships soon. Carol tracks the milestone with Alice.",
		},
	}

	for _, doc := range docs {
		if _, err := db.InsertGraphDocument(ctx, doc, GraphRAGIngestOptions{
			ChunkSize: 8,
			Extractor: fixtureExtractor{},
		}); err != nil {
			t.Fatalf("insert graph document %s: %v", doc.ID, err)
		}
	}

	full, err := db.SearchGraphRAG(ctx, "Alice works research", GraphRAGQueryOptions{
		TopK:              2,
		MaxHops:           2,
		MaxRelatedChunks:  4,
		MaxContextChunks:  8,
		MaxContextChars:   1200,
		RetrievalMode:     RetrievalModeGraph,
		MaxExpansionSeeds: 2,
		MaxTraversalNodes: 12,
	})
	if err != nil {
		t.Fatalf("full graph search: %v", err)
	}

	limited, err := db.SearchGraphRAG(ctx, "Alice works research", GraphRAGQueryOptions{
		TopK:                2,
		MaxHops:             2,
		MaxRelatedChunks:    4,
		MaxContextChunks:    8,
		MaxContextChars:     1200,
		RetrievalMode:       RetrievalModeGraph,
		GraphLight:          true,
		MaxExpansionSeeds:   1,
		MaxTraversalNodes:   3,
		MaxEntitiesPerChunk: 1,
	})
	if err != nil {
		t.Fatalf("limited graph search: %v", err)
	}

	if len(full.Chunks) <= len(limited.Chunks) {
		t.Fatalf("expected full graph search to return more chunks than limited mode, got full=%d limited=%d", len(full.Chunks), len(limited.Chunks))
	}
	for _, chunk := range limited.Chunks {
		if len(chunk.Entities) > 1 {
			t.Fatalf("expected graph cost controls to cap entities per chunk, got %+v", chunk)
		}
	}
}

func TestSearchTextMaxEntitiesPerChunk(t *testing.T) {
	dbPath := fmt.Sprintf("test_graph_runtime_text_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	tools := db.GraphRAGTools()
	ingest, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "doc-entities",
		Title:      "Entity Dense",
		Content:    "Alice works with Bob and Carol at Acme.",
		ChunkSize:  16,
	})
	if err != nil {
		t.Fatalf("ingest document: %v", err)
	}

	_, err = tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "doc-entities",
		Entities: []ToolEntityInput{
			{Name: "Alice", ChunkIDs: ingest.ChunkNodeIDs},
			{Name: "Bob", ChunkIDs: ingest.ChunkNodeIDs},
			{Name: "Carol", ChunkIDs: ingest.ChunkNodeIDs},
			{Name: "Acme", ChunkIDs: ingest.ChunkNodeIDs},
		},
	})
	if err != nil {
		t.Fatalf("upsert entities: %v", err)
	}

	resp, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query:               "Alice Acme",
		TopK:                2,
		RetrievalMode:       RetrievalModeGraph,
		MaxEntitiesPerChunk: 2,
	})
	if err != nil {
		t.Fatalf("search text: %v", err)
	}
	if len(resp.Chunks) == 0 {
		t.Fatal("expected chunk results")
	}
	if len(resp.Chunks[0].Entities) > 2 {
		t.Fatalf("expected entity limit to be enforced, got %+v", resp.Chunks[0])
	}
}
