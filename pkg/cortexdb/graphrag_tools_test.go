package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestGraphRAGToolsTypedFlowWithoutEmbedder(t *testing.T) {
	dbPath := fmt.Sprintf("test_graphrag_tools_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	if db.HasEmbedder() {
		t.Fatal("expected no embedder")
	}

	tools := db.GraphRAGTools()
	ctx := context.Background()

	ingestResp, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "doc-tools",
		Title:      "Alice at Acme",
		Content:    "Alice works at Acme on GraphRAG research.\n\nAcme ships a beta release for research tools.",
		ChunkSize:  12,
	})
	if err != nil {
		t.Fatalf("ingest document: %v", err)
	}
	if len(ingestResp.ChunkNodeIDs) == 0 {
		t.Fatal("expected chunk IDs")
	}

	entityResp, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "doc-tools",
		Entities: []ToolEntityInput{
			{Name: "Alice", ChunkIDs: []string{ingestResp.ChunkNodeIDs[0]}},
			{Name: "Acme", ChunkIDs: ingestResp.ChunkNodeIDs},
		},
	})
	if err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
	if len(entityResp.EntityNodeIDs) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(entityResp.EntityNodeIDs))
	}

	relationResp, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "doc-tools",
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at", ChunkIDs: []string{ingestResp.ChunkNodeIDs[0]}},
		},
	})
	if err != nil {
		t.Fatalf("upsert relations: %v", err)
	}
	if len(relationResp.EdgeIDs) != 1 {
		t.Fatalf("expected 1 relation edge, got %d", len(relationResp.EdgeIDs))
	}

	searchResp, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query: "Alice Acme research",
		TopK:  3,
	})
	if err != nil {
		t.Fatalf("search text: %v", err)
	}
	if len(searchResp.Chunks) == 0 {
		t.Fatal("expected lexical search results")
	}

	questionSearchResp, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query: "Where does Alice work?",
		TopK:  3,
	})
	if err != nil {
		t.Fatalf("search text natural-language fallback: %v", err)
	}
	if len(questionSearchResp.Chunks) == 0 {
		t.Fatal("expected lexical fallback results for natural-language query")
	}

	plannedSearchResp, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query:            "Find the employer",
		TopK:             3,
		Keywords:         []string{"Alice", "Acme", "employer", "works"},
		AlternateQueries: []string{"Alice employer", "Alice works at Acme"},
	})
	if err != nil {
		t.Fatalf("search text with planned keywords: %v", err)
	}
	if len(plannedSearchResp.Chunks) == 0 {
		t.Fatal("expected lexical results from planner-provided keywords")
	}

	lexicalOnlySearchResp, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query:         "Alice Acme research",
		TopK:          3,
		RetrievalMode: RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("search text disable graph: %v", err)
	}
	if len(lexicalOnlySearchResp.Chunks) == 0 {
		t.Fatal("expected lexical-only search results")
	}
	for _, chunk := range lexicalOnlySearchResp.Chunks {
		if len(chunk.Entities) != 0 {
			t.Fatalf("expected no chunk entities when graph is disabled, got %+v", chunk)
		}
	}

	entitySearchResp, err := tools.SearchChunksByEntities(ctx, ToolSearchChunksByEntitiesRequest{
		EntityNames: []string{"Alice", "Acme"},
		TopK:        3,
	})
	if err != nil {
		t.Fatalf("search chunks by entities: %v", err)
	}
	if len(entitySearchResp.Chunks) == 0 {
		t.Fatal("expected entity-linked chunks")
	}

	graphResp, err := tools.ExpandGraph(ctx, ToolExpandGraphRequest{
		NodeIDs: []string{ingestResp.DocumentNodeID},
		MaxHops: 2,
	})
	if err != nil {
		t.Fatalf("expand graph: %v", err)
	}
	if len(graphResp.Nodes) == 0 || len(graphResp.Edges) == 0 {
		t.Fatalf("expected subgraph nodes and edges, got %d nodes / %d edges", len(graphResp.Nodes), len(graphResp.Edges))
	}

	chunkResp, err := tools.GetChunks(ctx, ToolGetChunksRequest{ChunkIDs: ingestResp.ChunkNodeIDs})
	if err != nil {
		t.Fatalf("get chunks: %v", err)
	}
	if len(chunkResp.Chunks) != len(ingestResp.ChunkNodeIDs) {
		t.Fatalf("expected %d chunks, got %d", len(ingestResp.ChunkNodeIDs), len(chunkResp.Chunks))
	}

	chunkRespNoGraph, err := tools.GetChunks(ctx, ToolGetChunksRequest{
		ChunkIDs:      ingestResp.ChunkNodeIDs,
		RetrievalMode: RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("get chunks disable graph: %v", err)
	}
	for _, chunk := range chunkRespNoGraph.Chunks {
		if len(chunk.Entities) != 0 {
			t.Fatalf("expected no chunk entities when graph is disabled, got %+v", chunk)
		}
	}

	contextResp, err := tools.BuildContext(ctx, ToolBuildContextRequest{
		ChunkIDs:         ingestResp.ChunkNodeIDs,
		MaxContextChunks: 1,
		MaxContextChars:  120,
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(contextResp.Chunks) != 1 {
		t.Fatalf("expected packed context to select 1 chunk, got %d", len(contextResp.Chunks))
	}
	if contextResp.Context == "" {
		t.Fatal("expected non-empty packed context")
	}

	graphragResp, err := tools.SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
		Query:            "Where does Alice work?",
		TopK:             2,
		MaxHops:          2,
		MaxRelatedChunks: 2,
		MaxContextChunks: 3,
		MaxContextChars:  220,
		PerDocumentLimit: 2,
		EntityNames:      []string{"Alice", "Acme"},
	})
	if err != nil {
		t.Fatalf("search graphrag lexical: %v", err)
	}
	if len(graphragResp.Chunks) == 0 {
		t.Fatal("expected graphrag lexical results")
	}
	if graphragResp.Context == "" {
		t.Fatal("expected graphrag lexical context")
	}

	entityFallbackResp, err := tools.SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
		Query:            "Which employer is connected to her?",
		TopK:             2,
		MaxHops:          2,
		MaxRelatedChunks: 2,
		MaxContextChunks: 3,
		MaxContextChars:  220,
		PerDocumentLimit: 2,
		EntityNames:      []string{"Alice", "Acme"},
		Keywords:         []string{"Alice", "Acme", "employer", "works"},
		AlternateQueries: []string{"Alice works at Acme"},
	})
	if err != nil {
		t.Fatalf("search graphrag lexical entity fallback: %v", err)
	}
	if len(entityFallbackResp.Chunks) == 0 {
		t.Fatal("expected graphrag lexical entity fallback results")
	}

	lexicalOnlyGraphRAGResp, err := tools.SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
		Query:            "Find Alice's employer",
		TopK:             2,
		MaxContextChunks: 2,
		MaxContextChars:  220,
		RetrievalMode:    RetrievalModeLexical,
		Keywords:         []string{"Alice", "Acme", "employer", "works"},
		AlternateQueries: []string{"Alice works at Acme"},
		EntityNames:      []string{"Alice", "Acme"},
	})
	if err != nil {
		t.Fatalf("search graphrag lexical disable graph: %v", err)
	}
	if len(lexicalOnlyGraphRAGResp.Chunks) == 0 {
		t.Fatal("expected lexical-only graphrag results")
	}
	if len(lexicalOnlyGraphRAGResp.Entities) != 0 {
		t.Fatalf("expected no graph entities when graph is disabled, got %v", lexicalOnlyGraphRAGResp.Entities)
	}
	for _, chunk := range lexicalOnlyGraphRAGResp.Chunks {
		if len(chunk.Entities) != 0 {
			t.Fatalf("expected no chunk entities when graph is disabled, got %+v", chunk)
		}
	}

	autoGraphRAGResp, err := tools.SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
		Query:            "Where does Alice work?",
		TopK:             2,
		MaxHops:          2,
		MaxRelatedChunks: 2,
		MaxContextChunks: 3,
		MaxContextChars:  220,
		PerDocumentLimit: 2,
		RetrievalMode:    RetrievalModeAuto,
	})
	if err != nil {
		t.Fatalf("search graphrag lexical auto mode: %v", err)
	}
	if len(autoGraphRAGResp.Entities) == 0 {
		t.Fatal("expected auto mode to use graph when entity signal exists")
	}
}

func TestGraphRAGToolsDispatcherAndDefinitions(t *testing.T) {
	dbPath := fmt.Sprintf("test_graphrag_tools_dispatch_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	tools := db.GraphRAGTools()
	defs := tools.Definitions()
	if len(defs) < 10 {
		t.Fatalf("expected at least 10 tool definitions, got %d", len(defs))
	}

	ctx := context.Background()
	payload, err := json.Marshal(ToolIngestDocumentRequest{
		DocumentID: "doc-dispatch",
		Title:      "Dispatch Test",
		Content:    "Acme uses lexical tools for GraphRAG orchestration.",
		ChunkSize:  16,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	resp, err := tools.Call(ctx, "ingest_document", payload)
	if err != nil {
		t.Fatalf("dispatch ingest_document: %v", err)
	}
	typedResp, ok := resp.(*ToolIngestDocumentResponse)
	if !ok {
		t.Fatalf("expected typed ingest response, got %T", resp)
	}
	if typedResp.DocumentNodeID == "" {
		t.Fatal("expected non-empty document node ID")
	}

	if _, err := tools.Call(ctx, "unknown_tool", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected unknown tool error")
	}
}
