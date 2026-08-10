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
	if len(toolList.Tools) < 20 {
		t.Fatalf("expected at least 20 tools, got %d", len(toolList.Tools))
	}

	var searchTool *mcp.Tool
	var cortexQueryTool *mcp.Tool
	var knowledgeSearchTool *mcp.Tool
	var memorySearchTool *mcp.Tool
	var ontologySaveTool *mcp.Tool
	var inferenceTool *mcp.Tool
	var KnowledgeMemoryRecallTool *mcp.Tool
	var KnowledgeMemoryConsolidateTool *mcp.Tool
	for _, tool := range toolList.Tools {
		if tool.Name == "search_graphrag_lexical" {
			searchTool = tool
		}
		if tool.Name == "cortex_query" {
			cortexQueryTool = tool
		}
		if tool.Name == "knowledge_search" {
			knowledgeSearchTool = tool
		}
		if tool.Name == "memory_search" {
			memorySearchTool = tool
		}
		if tool.Name == "ontology_save" {
			ontologySaveTool = tool
		}
		if tool.Name == "apply_inference" {
			inferenceTool = tool
		}
		if tool.Name == "knowledge_memory_recall" {
			KnowledgeMemoryRecallTool = tool
		}
		if tool.Name == "knowledge_memory_consolidate" {
			KnowledgeMemoryConsolidateTool = tool
		}
	}
	if searchTool == nil {
		t.Fatal("expected search_graphrag_lexical tool")
	}
	if cortexQueryTool == nil {
		t.Fatal("expected cortex_query tool")
	}
	if knowledgeSearchTool == nil {
		t.Fatal("expected knowledge_search tool")
	}
	if memorySearchTool == nil {
		t.Fatal("expected memory_search tool")
	}
	if ontologySaveTool == nil {
		t.Fatal("expected ontology_save tool")
	}
	if inferenceTool == nil {
		t.Fatal("expected apply_inference tool")
	}
	if KnowledgeMemoryRecallTool == nil {
		t.Fatal("expected knowledge_memory_recall tool")
	}
	if KnowledgeMemoryConsolidateTool == nil {
		t.Fatal("expected knowledge_memory_consolidate tool")
	}
	if !strings.Contains(searchTool.Description, "keywords") {
		t.Fatalf("expected keyword guidance in tool description, got %q", searchTool.Description)
	}
	if !strings.Contains(cortexQueryTool.Description, "composable Query API") {
		t.Fatalf("expected Query API guidance in cortex_query description, got %q", cortexQueryTool.Description)
	}
	if !strings.Contains(knowledgeSearchTool.Description, "keywords") {
		t.Fatalf("expected keyword guidance in knowledge_search description, got %q", knowledgeSearchTool.Description)
	}
	if !strings.Contains(ontologySaveTool.Description, "schema") {
		t.Fatalf("expected ontology schema guidance in ontology_save description, got %q", ontologySaveTool.Description)
	}
	if !strings.Contains(KnowledgeMemoryRecallTool.Description, "episodic memory") {
		t.Fatalf("expected KnowledgeMemory guidance in knowledge_memory_recall description, got %q", KnowledgeMemoryRecallTool.Description)
	}

	ontologySaveResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "ontology_save",
		Arguments: map[string]any{
			// Saved but not activated: this test covers the MCP tool surface,
			// and the free-form entities it writes further down deliberately do
			// not conform to this schema. Activation is exercised where it
			// belongs, in the ontology storage and write-validation tests.
			"activate": false,
			"schema": map[string]any{
				"schema_id": "mcp-ontology",
				"object_types": []map[string]any{
					{
						"api_name":    "Person",
						"primary_key": "email",
						"properties": []map[string]any{
							{"api_name": "email", "data_type": map[string]any{"kind": "string"}, "required": true},
							{"api_name": "fullName", "data_type": map[string]any{"kind": "string"}},
						},
					},
					{
						"api_name":    "Organization",
						"primary_key": "orgId",
						"properties": []map[string]any{
							{"api_name": "orgId", "data_type": map[string]any{"kind": "string"}, "required": true},
						},
					},
				},
				"link_types": []map[string]any{
					{
						"api_name": "worksAt",
						"a":        map[string]any{"api_name": "employees", "object_type_api_name": "Organization", "cardinality": "MANY"},
						"b":        map[string]any{"api_name": "employer", "object_type_api_name": "Person", "cardinality": "ONE"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("call ontology_save: %v", err)
	}
	if ontologySaveResult.IsError {
		t.Fatalf("ontology_save returned tool error: %v", ontologySaveResult.GetError())
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

	cortexQueryResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "cortex_query",
		Arguments: map[string]any{
			"query":        "Alice Acme research",
			"query_vector": []float64{1, 0, 0, 0},
			"fusion":       QueryFusionWeightedRRF,
			"limit":        2,
			"include_raw":  true,
			"prefetch": []map[string]any{
				{"name": "dense", "kind": QueryPrefetchVector, "weight": 1, "limit": 4},
				{"name": "lexical", "kind": QueryPrefetchLexical, "weight": 1, "limit": 4},
				{"name": "graph", "kind": QueryPrefetchGraph, "entity_names": []string{"Alice", "Acme"}, "weight": 1, "limit": 4, "max_hops": 1},
			},
		},
	})
	if err != nil {
		t.Fatalf("call cortex_query: %v", err)
	}
	if cortexQueryResult.IsError {
		t.Fatalf("cortex_query returned tool error: %v", cortexQueryResult.GetError())
	}
	var cortexQueryResp QueryResponse
	cortexQueryPayload, err := json.Marshal(cortexQueryResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal cortex_query structured content: %v", err)
	}
	if err := json.Unmarshal(cortexQueryPayload, &cortexQueryResp); err != nil {
		t.Fatalf("unmarshal cortex_query structured content: %v", err)
	}
	if len(cortexQueryResp.Results) == 0 {
		t.Fatal("expected cortex_query results from MCP")
	}
	if len(cortexQueryResp.Results[0].SourceRanks) == 0 {
		t.Fatalf("expected cortex_query raw source ranks from MCP, got %+v", cortexQueryResp.Results[0])
	}
	if cortexQueryResp.Results[0].SourceRanks["graph"] == 0 {
		t.Fatalf("expected cortex_query graph source rank from MCP, got %+v", cortexQueryResp.Results[0])
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
			"plan": map[string]any{
				"keywords":          []string{"Alice", "Acme", "employer", "works"},
				"alternate_queries": []string{"Alice works at Acme"},
				"entity_names":      []string{"Alice", "Acme"},
				"retrieval_mode":    RetrievalModeAuto,
			},
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
	if graphragResp.Decision.EffectiveMode == "" || graphragResp.Plan.Query == "" {
		t.Fatalf("expected graphrag planning metadata from MCP search, got %+v", graphragResp)
	}

	knowledgeSaveResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_save",
		Arguments: map[string]any{
			"knowledge_id": "knowledge-mcp",
			"title":        "Bob at Beta Labs",
			"content":      "Bob works at Beta Labs on retrieval systems.",
			"chunk_size":   16,
			"entities": []map[string]any{
				{"name": "Bob", "chunk_ids": []string{"chunk:knowledge-mcp:000"}},
				{"name": "Beta Labs", "chunk_ids": []string{"chunk:knowledge-mcp:000"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_save: %v", err)
	}
	if knowledgeSaveResult.IsError {
		t.Fatalf("knowledge_save returned tool error: %v", knowledgeSaveResult.GetError())
	}

	knowledgeSearchResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_search",
		Arguments: map[string]any{
			"query":              "Where does Bob work?",
			"top_k":              2,
			"max_hops":           2,
			"max_related_chunks": 2,
			"max_context_chunks": 2,
			"max_context_chars":  240,
			"per_document_limit": 1,
			"keywords":           []string{"Bob", "Beta Labs", "employer", "works"},
			"alternate_queries":  []string{"Bob works at Beta Labs"},
			"entity_names":       []string{"Bob", "Beta Labs"},
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_search: %v", err)
	}
	if knowledgeSearchResult.IsError {
		t.Fatalf("knowledge_search returned tool error: %v", knowledgeSearchResult.GetError())
	}
	var knowledgeSearchResp KnowledgeSearchResponse
	knowledgeSearchPayload, err := json.Marshal(knowledgeSearchResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal knowledge search structured content: %v", err)
	}
	if err := json.Unmarshal(knowledgeSearchPayload, &knowledgeSearchResp); err != nil {
		t.Fatalf("unmarshal knowledge search structured content: %v", err)
	}
	if len(knowledgeSearchResp.Results) == 0 {
		t.Fatal("expected knowledge search results from MCP")
	}
	if knowledgeSearchResp.Decision.EffectiveMode == "" || knowledgeSearchResp.Plan.Query == "" {
		t.Fatalf("expected knowledge planning metadata from MCP, got %+v", knowledgeSearchResp)
	}

	memorySaveResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "memory_save",
		Arguments: map[string]any{
			"memory_id":  "memory-mcp",
			"user_id":    "user-mcp",
			"scope":      MemoryScopeUser,
			"namespace":  "assistant",
			"content":    "Bob likes concise factual replies.",
			"importance": 0.7,
		},
	})
	if err != nil {
		t.Fatalf("call memory_save: %v", err)
	}
	if memorySaveResult.IsError {
		t.Fatalf("memory_save returned tool error: %v", memorySaveResult.GetError())
	}

	memorySearchResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "memory_search",
		Arguments: map[string]any{
			"query":             "How should I answer Bob?",
			"user_id":           "user-mcp",
			"scope":             MemoryScopeUser,
			"namespace":         "assistant",
			"top_k":             3,
			"keywords":          []string{"Bob", "concise", "factual", "replies"},
			"alternate_queries": []string{"Bob likes concise factual replies"},
			"retrieval_mode":    RetrievalModeLexical,
		},
	})
	if err != nil {
		t.Fatalf("call memory_search: %v", err)
	}
	if memorySearchResult.IsError {
		t.Fatalf("memory_search returned tool error: %v", memorySearchResult.GetError())
	}
	var memorySearchResp MemorySearchResponse
	memorySearchPayload, err := json.Marshal(memorySearchResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal memory search structured content: %v", err)
	}
	if err := json.Unmarshal(memorySearchPayload, &memorySearchResp); err != nil {
		t.Fatalf("unmarshal memory search structured content: %v", err)
	}
	if len(memorySearchResp.Results) == 0 {
		t.Fatal("expected memory search results from MCP")
	}

	KnowledgeMemoryRememberResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_memory_remember",
		Arguments: map[string]any{
			"memory_id":  "KnowledgeMemory-memory-mcp",
			"session_id": "KnowledgeMemory-session-mcp",
			"scope":      MemoryScopeSession,
			"content":    "Alice meets Acme at Wanda Plaza tomorrow.",
			"importance": 0.9,
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_memory_remember: %v", err)
	}
	if KnowledgeMemoryRememberResult.IsError {
		t.Fatalf("knowledge_memory_remember returned tool error: %v", KnowledgeMemoryRememberResult.GetError())
	}

	KnowledgeMemoryRecallResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_memory_recall",
		Arguments: map[string]any{
			"query":             "What is happening with Alice tomorrow?",
			"session_id":        "KnowledgeMemory-session-mcp",
			"scope":             MemoryScopeSession,
			"disable_knowledge": true,
			"top_k_memories":    3,
			"max_memory_items":  3,
			"max_memory_chars":  240,
			"keywords":          []string{"Alice", "tomorrow", "Wanda Plaza"},
			"alternate_queries": []string{"Alice meets Acme at Wanda Plaza tomorrow"},
			"retrieval_mode":    RetrievalModeLexical,
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_memory_recall: %v", err)
	}
	if KnowledgeMemoryRecallResult.IsError {
		t.Fatalf("knowledge_memory_recall returned tool error: %v", KnowledgeMemoryRecallResult.GetError())
	}
	var KnowledgeMemoryRecallResp KnowledgeMemoryRecallResponse
	KnowledgeMemoryRecallPayload, err := json.Marshal(KnowledgeMemoryRecallResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal KnowledgeMemory recall structured content: %v", err)
	}
	if err := json.Unmarshal(KnowledgeMemoryRecallPayload, &KnowledgeMemoryRecallResp); err != nil {
		t.Fatalf("unmarshal KnowledgeMemory recall structured content: %v", err)
	}
	if len(KnowledgeMemoryRecallResp.Memories) == 0 || KnowledgeMemoryRecallResp.ContextPack.Text == "" {
		t.Fatalf("expected KnowledgeMemory recall results and context pack, got %+v", KnowledgeMemoryRecallResp)
	}

	KnowledgeMemoryPromoteResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_memory_promote_to_knowledge",
		Arguments: map[string]any{
			"memory_ids":   []string{"KnowledgeMemory-memory-mcp"},
			"knowledge_id": "KnowledgeMemory-knowledge-mcp",
			"title":        "Alice and Acme",
			"entities": []map[string]any{
				{"name": "Alice", "chunk_ids": []string{"chunk:KnowledgeMemory-knowledge-mcp:000"}},
				{"name": "Acme", "chunk_ids": []string{"chunk:KnowledgeMemory-knowledge-mcp:000"}},
			},
			"relations": []map[string]any{
				{"from": "Alice", "to": "Acme", "type": "meets", "chunk_ids": []string{"chunk:KnowledgeMemory-knowledge-mcp:000"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_memory_promote_to_knowledge: %v", err)
	}
	if KnowledgeMemoryPromoteResult.IsError {
		var contentText string
		if len(KnowledgeMemoryPromoteResult.Content) > 0 {
			if text, ok := KnowledgeMemoryPromoteResult.Content[0].(*mcp.TextContent); ok {
				contentText = text.Text
			}
		}
		t.Fatalf("knowledge_memory_promote_to_knowledge returned tool error: err=%v structured=%#v content_text=%q content=%#v", KnowledgeMemoryPromoteResult.GetError(), KnowledgeMemoryPromoteResult.StructuredContent, contentText, KnowledgeMemoryPromoteResult.Content)
	}
	var KnowledgeMemoryPromoteResp KnowledgeMemoryPromoteToKnowledgeResponse
	KnowledgeMemoryPromotePayload, err := json.Marshal(KnowledgeMemoryPromoteResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal KnowledgeMemory promote structured content: %v", err)
	}
	if err := json.Unmarshal(KnowledgeMemoryPromotePayload, &KnowledgeMemoryPromoteResp); err != nil {
		t.Fatalf("unmarshal KnowledgeMemory promote structured content: %v", err)
	}
	if KnowledgeMemoryPromoteResp.Knowledge.ID != "KnowledgeMemory-knowledge-mcp" {
		t.Fatalf("unexpected KnowledgeMemory promoted knowledge: %+v", KnowledgeMemoryPromoteResp)
	}

	KnowledgeMemoryExpandResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_memory_expand_entity_context",
		Arguments: map[string]any{
			"entity_names": []string{"Alice"},
			"max_hops":     2,
			"top_k_chunks": 4,
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_memory_expand_entity_context: %v", err)
	}
	if KnowledgeMemoryExpandResult.IsError {
		t.Fatalf("knowledge_memory_expand_entity_context returned tool error: %v", KnowledgeMemoryExpandResult.GetError())
	}
	var KnowledgeMemoryExpandResp KnowledgeMemoryExpandEntityContextResponse
	KnowledgeMemoryExpandPayload, err := json.Marshal(KnowledgeMemoryExpandResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal KnowledgeMemory expand structured content: %v", err)
	}
	if err := json.Unmarshal(KnowledgeMemoryExpandPayload, &KnowledgeMemoryExpandResp); err != nil {
		t.Fatalf("unmarshal KnowledgeMemory expand structured content: %v", err)
	}
	if len(KnowledgeMemoryExpandResp.Nodes) == 0 {
		t.Fatalf("expected KnowledgeMemory expand nodes, got %+v", KnowledgeMemoryExpandResp)
	}

	KnowledgeMemoryNeighborsResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_memory_neighbors",
		Arguments: map[string]any{
			"entity_name": "Alice",
			"max_depth":   1,
			"direction":   "both",
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_memory_neighbors: %v", err)
	}
	if KnowledgeMemoryNeighborsResult.IsError {
		t.Fatalf("knowledge_memory_neighbors returned tool error: %v", KnowledgeMemoryNeighborsResult.GetError())
	}
	var KnowledgeMemoryNeighborsResp KnowledgeMemoryNeighborsResponse
	KnowledgeMemoryNeighborsPayload, err := json.Marshal(KnowledgeMemoryNeighborsResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal KnowledgeMemory neighbors structured content: %v", err)
	}
	if err := json.Unmarshal(KnowledgeMemoryNeighborsPayload, &KnowledgeMemoryNeighborsResp); err != nil {
		t.Fatalf("unmarshal KnowledgeMemory neighbors structured content: %v", err)
	}
	if len(KnowledgeMemoryNeighborsResp.Neighbors) == 0 {
		t.Fatalf("expected KnowledgeMemory neighbors, got %+v", KnowledgeMemoryNeighborsResp)
	}

	KnowledgeMemoryPathResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_memory_shortest_path",
		Arguments: map[string]any{
			"from_entity_name": "Alice",
			"to_entity_name":   "Acme",
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_memory_shortest_path: %v", err)
	}
	if KnowledgeMemoryPathResult.IsError {
		t.Fatalf("knowledge_memory_shortest_path returned tool error: %v", KnowledgeMemoryPathResult.GetError())
	}
	var KnowledgeMemoryPathResp KnowledgeMemoryShortestPathResponse
	KnowledgeMemoryPathPayload, err := json.Marshal(KnowledgeMemoryPathResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal KnowledgeMemory path structured content: %v", err)
	}
	if err := json.Unmarshal(KnowledgeMemoryPathPayload, &KnowledgeMemoryPathResp); err != nil {
		t.Fatalf("unmarshal KnowledgeMemory path structured content: %v", err)
	}
	if KnowledgeMemoryPathResp.Path == nil || KnowledgeMemoryPathResp.Path.Distance != 1 {
		t.Fatalf("expected direct KnowledgeMemory shortest path, got %+v", KnowledgeMemoryPathResp)
	}

	KnowledgeMemoryContextResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_memory_build_context_pack",
		Arguments: map[string]any{
			"query":             "Summarize Alice and Acme",
			"session_id":        "KnowledgeMemory-session-mcp",
			"scope":             MemoryScopeSession,
			"top_k_memories":    3,
			"top_k_knowledge":   2,
			"entity_names":      []string{"Alice", "Acme"},
			"alternate_queries": []string{"Alice meets Acme at Wanda Plaza tomorrow"},
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_memory_build_context_pack: %v", err)
	}
	if KnowledgeMemoryContextResult.IsError {
		t.Fatalf("knowledge_memory_build_context_pack returned tool error: %v", KnowledgeMemoryContextResult.GetError())
	}
	var KnowledgeMemoryContextResp KnowledgeMemoryBuildContextPackResponse
	KnowledgeMemoryContextPayload, err := json.Marshal(KnowledgeMemoryContextResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal KnowledgeMemory context structured content: %v", err)
	}
	if err := json.Unmarshal(KnowledgeMemoryContextPayload, &KnowledgeMemoryContextResp); err != nil {
		t.Fatalf("unmarshal KnowledgeMemory context structured content: %v", err)
	}
	if KnowledgeMemoryContextResp.ContextPack.Text == "" {
		t.Fatalf("expected KnowledgeMemory context pack text, got %+v", KnowledgeMemoryContextResp)
	}

	KnowledgeMemoryReflectResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_memory_reflect",
		Arguments: map[string]any{
			"recall": map[string]any{
				"query":           "What matters about Alice and Acme?",
				"session_id":      "KnowledgeMemory-session-mcp",
				"scope":           MemoryScopeSession,
				"top_k_memories":  3,
				"top_k_knowledge": 2,
				"entity_names":    []string{"Alice", "Acme"},
			},
			"max_summary_chars": 180,
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_memory_reflect: %v", err)
	}
	if KnowledgeMemoryReflectResult.IsError {
		t.Fatalf("knowledge_memory_reflect returned tool error: %v", KnowledgeMemoryReflectResult.GetError())
	}
	var KnowledgeMemoryReflectResp KnowledgeMemoryReflectResponse
	KnowledgeMemoryReflectPayload, err := json.Marshal(KnowledgeMemoryReflectResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal KnowledgeMemory reflect structured content: %v", err)
	}
	if err := json.Unmarshal(KnowledgeMemoryReflectPayload, &KnowledgeMemoryReflectResp); err != nil {
		t.Fatalf("unmarshal KnowledgeMemory reflect structured content: %v", err)
	}
	if strings.TrimSpace(KnowledgeMemoryReflectResp.Reflection.Summary) == "" {
		t.Fatalf("expected non-empty KnowledgeMemory reflection summary, got %+v", KnowledgeMemoryReflectResp)
	}

	KnowledgeMemoryConsolidateResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "knowledge_memory_consolidate",
		Arguments: map[string]any{
			"reflect": map[string]any{
				"recall": map[string]any{
					"query":           "Summarize Alice and Acme",
					"session_id":      "KnowledgeMemory-session-mcp",
					"scope":           MemoryScopeSession,
					"top_k_memories":  3,
					"top_k_knowledge": 2,
					"entity_names":    []string{"Alice", "Acme"},
				},
				"max_summary_chars": 180,
			},
			"memory_id":            "KnowledgeMemory-summary-mcp",
			"session_id":           "KnowledgeMemory-session-mcp",
			"scope":                MemoryScopeSession,
			"promote_to_knowledge": true,
			"promotion": map[string]any{
				"knowledge_id": "KnowledgeMemory-summary-knowledge-mcp",
				"title":        "KnowledgeMemory Summary MCP",
			},
		},
	})
	if err != nil {
		t.Fatalf("call knowledge_memory_consolidate: %v", err)
	}
	if KnowledgeMemoryConsolidateResult.IsError {
		t.Fatalf("knowledge_memory_consolidate returned tool error: %v", KnowledgeMemoryConsolidateResult.GetError())
	}
	var KnowledgeMemoryConsolidateResp KnowledgeMemoryConsolidateResponse
	KnowledgeMemoryConsolidatePayload, err := json.Marshal(KnowledgeMemoryConsolidateResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal KnowledgeMemory consolidate structured content: %v", err)
	}
	if err := json.Unmarshal(KnowledgeMemoryConsolidatePayload, &KnowledgeMemoryConsolidateResp); err != nil {
		t.Fatalf("unmarshal KnowledgeMemory consolidate structured content: %v", err)
	}
	if strings.TrimSpace(KnowledgeMemoryConsolidateResp.Memory.Content) == "" {
		t.Fatalf("expected saved summary memory from knowledge_memory_consolidate, got %+v", KnowledgeMemoryConsolidateResp)
	}
	if KnowledgeMemoryConsolidateResp.Knowledge == nil || KnowledgeMemoryConsolidateResp.Knowledge.ID != "KnowledgeMemory-summary-knowledge-mcp" {
		t.Fatalf("expected promoted summary knowledge from knowledge_memory_consolidate, got %+v", KnowledgeMemoryConsolidateResp)
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
