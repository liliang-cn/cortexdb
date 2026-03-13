package cortexdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPServerToolFlow(t *testing.T) {
	dbPath := fmt.Sprintf("test_mcp_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	server := db.NewMCPServer(MCPServerOptions{})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "v1.0.0",
	}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = session.Close() }()

	toolList, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolList.Tools) < 10 {
		t.Fatalf("expected at least 10 tools, got %d", len(toolList.Tools))
	}

	var searchTool *mcp.Tool
	for _, tool := range toolList.Tools {
		if tool.Name == "search_graphrag_lexical" {
			searchTool = tool
			break
		}
	}
	if searchTool == nil {
		t.Fatal("expected search_graphrag_lexical tool")
	}
	if !strings.Contains(searchTool.Description, "keywords") {
		t.Fatalf("expected keyword guidance in tool description, got %q", searchTool.Description)
	}

	ingestResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "ingest_document",
		Arguments: map[string]any{
			"document_id": "doc-mcp",
			"title":       "Alice at Acme",
			"content":     "Alice works at Acme on GraphRAG research.",
			"chunk_size":  16,
		},
	})
	if err != nil {
		t.Fatalf("call ingest_document: %v", err)
	}
	if ingestResult.IsError {
		t.Fatalf("ingest_document returned tool error: %v", ingestResult.GetError())
	}

	entityResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "upsert_entities",
		Arguments: map[string]any{
			"document_id": "doc-mcp",
			"entities": []map[string]any{
				{"name": "Alice", "chunk_ids": []string{"chunk:doc-mcp:000"}},
				{"name": "Acme", "chunk_ids": []string{"chunk:doc-mcp:000"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("call upsert_entities: %v", err)
	}
	if entityResult.IsError {
		t.Fatalf("upsert_entities returned tool error: %v", entityResult.GetError())
	}

	searchResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "search_graphrag_lexical",
		Arguments: map[string]any{
			"query":              "Find Alice's employer",
			"top_k":              2,
			"max_hops":           2,
			"max_related_chunks": 2,
			"max_context_chunks": 3,
			"max_context_chars":  240,
			"per_document_limit": 2,
			"keywords":           []string{"Alice", "Acme", "employer", "works"},
			"alternate_queries":  []string{"Alice works at Acme"},
			"entity_names":       []string{"Alice", "Acme"},
		},
	})
	if err != nil {
		t.Fatalf("call search_graphrag_lexical: %v", err)
	}
	if searchResult.IsError {
		t.Fatalf("search_graphrag_lexical returned tool error: %v", searchResult.GetError())
	}

	var graphragResp GraphRAGQueryResult
	searchPayload, err := json.Marshal(searchResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(searchPayload, &graphragResp); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	if len(graphragResp.Chunks) == 0 {
		t.Fatal("expected graphrag chunks from MCP search")
	}
	if graphragResp.Context == "" {
		t.Fatal("expected graphrag context from MCP search")
	}

	if closeErr := session.Close(); closeErr != nil {
		t.Fatalf("close session: %v", closeErr)
	}
	cancel()

	runErr := <-errCh
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, mcp.ErrConnectionClosed) {
		t.Fatalf("server run returned error: %v", runErr)
	}
}
