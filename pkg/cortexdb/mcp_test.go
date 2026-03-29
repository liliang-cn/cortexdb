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
	var knowledgeSearchTool *mcp.Tool
	var memorySearchTool *mcp.Tool
	var ontologySaveTool *mcp.Tool
	var inferenceTool *mcp.Tool
	var brainRecallTool *mcp.Tool
	var brainConsolidateTool *mcp.Tool
	for _, tool := range toolList.Tools {
		if tool.Name == "search_graphrag_lexical" {
			searchTool = tool
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
		if tool.Name == "brain_recall" {
			brainRecallTool = tool
		}
		if tool.Name == "brain_consolidate" {
			brainConsolidateTool = tool
		}
	}
	if searchTool == nil {
		t.Fatal("expected search_graphrag_lexical tool")
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
	if brainRecallTool == nil {
		t.Fatal("expected brain_recall tool")
	}
	if brainConsolidateTool == nil {
		t.Fatal("expected brain_consolidate tool")
	}
	if !strings.Contains(searchTool.Description, "keywords") {
		t.Fatalf("expected keyword guidance in tool description, got %q", searchTool.Description)
	}
	if !strings.Contains(knowledgeSearchTool.Description, "keywords") {
		t.Fatalf("expected keyword guidance in knowledge_search description, got %q", knowledgeSearchTool.Description)
	}
	if !strings.Contains(ontologySaveTool.Description, "schema") {
		t.Fatalf("expected ontology schema guidance in ontology_save description, got %q", ontologySaveTool.Description)
	}
	if !strings.Contains(brainRecallTool.Description, "episodic memory") {
		t.Fatalf("expected brain guidance in brain_recall description, got %q", brainRecallTool.Description)
	}

	ontologySaveResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "ontology_save",
		Arguments: map[string]any{
			"schema_id": "mcp-ontology",
			"activate":  true,
			"entity_types": []map[string]any{
				{"name": "entity"},
				{"name": "person"},
				{"name": "organization"},
			},
			"relation_types": []map[string]any{
				{"name": "works_at", "allowed_from_types": []string{"person"}, "allowed_to_types": []string{"organization"}},
				{"name": "meets", "allowed_from_types": []string{"entity", "person"}, "allowed_to_types": []string{"entity", "organization"}},
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

	brainRememberResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "brain_remember",
		Arguments: map[string]any{
			"memory_id":  "brain-memory-mcp",
			"session_id": "brain-session-mcp",
			"scope":      MemoryScopeSession,
			"content":    "Alice meets Acme at Wanda Plaza tomorrow.",
			"importance": 0.9,
		},
	})
	if err != nil {
		t.Fatalf("call brain_remember: %v", err)
	}
	if brainRememberResult.IsError {
		t.Fatalf("brain_remember returned tool error: %v", brainRememberResult.GetError())
	}

	brainRecallResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "brain_recall",
		Arguments: map[string]any{
			"query":             "What is happening with Alice tomorrow?",
			"session_id":        "brain-session-mcp",
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
		t.Fatalf("call brain_recall: %v", err)
	}
	if brainRecallResult.IsError {
		t.Fatalf("brain_recall returned tool error: %v", brainRecallResult.GetError())
	}
	var brainRecallResp BrainRecallResponse
	brainRecallPayload, err := json.Marshal(brainRecallResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal brain recall structured content: %v", err)
	}
	if err := json.Unmarshal(brainRecallPayload, &brainRecallResp); err != nil {
		t.Fatalf("unmarshal brain recall structured content: %v", err)
	}
	if len(brainRecallResp.Memories) == 0 || brainRecallResp.ContextPack.Text == "" {
		t.Fatalf("expected brain recall results and context pack, got %+v", brainRecallResp)
	}

	brainPromoteResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "brain_promote_to_knowledge",
		Arguments: map[string]any{
			"memory_ids":   []string{"brain-memory-mcp"},
			"knowledge_id": "brain-knowledge-mcp",
			"title":        "Alice and Acme",
			"entities": []map[string]any{
				{"name": "Alice", "chunk_ids": []string{"chunk:brain-knowledge-mcp:000"}},
				{"name": "Acme", "chunk_ids": []string{"chunk:brain-knowledge-mcp:000"}},
			},
			"relations": []map[string]any{
				{"from": "Alice", "to": "Acme", "type": "meets", "chunk_ids": []string{"chunk:brain-knowledge-mcp:000"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("call brain_promote_to_knowledge: %v", err)
	}
	if brainPromoteResult.IsError {
		var contentText string
		if len(brainPromoteResult.Content) > 0 {
			if text, ok := brainPromoteResult.Content[0].(*mcp.TextContent); ok {
				contentText = text.Text
			}
		}
		t.Fatalf("brain_promote_to_knowledge returned tool error: err=%v structured=%#v content_text=%q content=%#v", brainPromoteResult.GetError(), brainPromoteResult.StructuredContent, contentText, brainPromoteResult.Content)
	}
	var brainPromoteResp BrainPromoteToKnowledgeResponse
	brainPromotePayload, err := json.Marshal(brainPromoteResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal brain promote structured content: %v", err)
	}
	if err := json.Unmarshal(brainPromotePayload, &brainPromoteResp); err != nil {
		t.Fatalf("unmarshal brain promote structured content: %v", err)
	}
	if brainPromoteResp.Knowledge.ID != "brain-knowledge-mcp" {
		t.Fatalf("unexpected brain promoted knowledge: %+v", brainPromoteResp)
	}

	brainExpandResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "brain_expand_entity_context",
		Arguments: map[string]any{
			"entity_names": []string{"Alice"},
			"max_hops":     2,
			"top_k_chunks": 4,
		},
	})
	if err != nil {
		t.Fatalf("call brain_expand_entity_context: %v", err)
	}
	if brainExpandResult.IsError {
		t.Fatalf("brain_expand_entity_context returned tool error: %v", brainExpandResult.GetError())
	}
	var brainExpandResp BrainExpandEntityContextResponse
	brainExpandPayload, err := json.Marshal(brainExpandResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal brain expand structured content: %v", err)
	}
	if err := json.Unmarshal(brainExpandPayload, &brainExpandResp); err != nil {
		t.Fatalf("unmarshal brain expand structured content: %v", err)
	}
	if len(brainExpandResp.Nodes) == 0 {
		t.Fatalf("expected brain expand nodes, got %+v", brainExpandResp)
	}

	brainNeighborsResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "brain_neighbors",
		Arguments: map[string]any{
			"entity_name": "Alice",
			"max_depth":   1,
			"direction":   "both",
		},
	})
	if err != nil {
		t.Fatalf("call brain_neighbors: %v", err)
	}
	if brainNeighborsResult.IsError {
		t.Fatalf("brain_neighbors returned tool error: %v", brainNeighborsResult.GetError())
	}
	var brainNeighborsResp BrainNeighborsResponse
	brainNeighborsPayload, err := json.Marshal(brainNeighborsResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal brain neighbors structured content: %v", err)
	}
	if err := json.Unmarshal(brainNeighborsPayload, &brainNeighborsResp); err != nil {
		t.Fatalf("unmarshal brain neighbors structured content: %v", err)
	}
	if len(brainNeighborsResp.Neighbors) == 0 {
		t.Fatalf("expected brain neighbors, got %+v", brainNeighborsResp)
	}

	brainPathResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "brain_shortest_path",
		Arguments: map[string]any{
			"from_entity_name": "Alice",
			"to_entity_name":   "Acme",
		},
	})
	if err != nil {
		t.Fatalf("call brain_shortest_path: %v", err)
	}
	if brainPathResult.IsError {
		t.Fatalf("brain_shortest_path returned tool error: %v", brainPathResult.GetError())
	}
	var brainPathResp BrainShortestPathResponse
	brainPathPayload, err := json.Marshal(brainPathResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal brain path structured content: %v", err)
	}
	if err := json.Unmarshal(brainPathPayload, &brainPathResp); err != nil {
		t.Fatalf("unmarshal brain path structured content: %v", err)
	}
	if brainPathResp.Path == nil || brainPathResp.Path.Distance != 1 {
		t.Fatalf("expected direct brain shortest path, got %+v", brainPathResp)
	}

	brainContextResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "brain_build_context_pack",
		Arguments: map[string]any{
			"query":             "Summarize Alice and Acme",
			"session_id":        "brain-session-mcp",
			"scope":             MemoryScopeSession,
			"top_k_memories":    3,
			"top_k_knowledge":   2,
			"entity_names":      []string{"Alice", "Acme"},
			"alternate_queries": []string{"Alice meets Acme at Wanda Plaza tomorrow"},
		},
	})
	if err != nil {
		t.Fatalf("call brain_build_context_pack: %v", err)
	}
	if brainContextResult.IsError {
		t.Fatalf("brain_build_context_pack returned tool error: %v", brainContextResult.GetError())
	}
	var brainContextResp BrainBuildContextPackResponse
	brainContextPayload, err := json.Marshal(brainContextResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal brain context structured content: %v", err)
	}
	if err := json.Unmarshal(brainContextPayload, &brainContextResp); err != nil {
		t.Fatalf("unmarshal brain context structured content: %v", err)
	}
	if brainContextResp.ContextPack.Text == "" {
		t.Fatalf("expected brain context pack text, got %+v", brainContextResp)
	}

	brainReflectResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "brain_reflect",
		Arguments: map[string]any{
			"recall": map[string]any{
				"query":           "What matters about Alice and Acme?",
				"session_id":      "brain-session-mcp",
				"scope":           MemoryScopeSession,
				"top_k_memories":  3,
				"top_k_knowledge": 2,
				"entity_names":    []string{"Alice", "Acme"},
			},
			"max_summary_chars": 180,
		},
	})
	if err != nil {
		t.Fatalf("call brain_reflect: %v", err)
	}
	if brainReflectResult.IsError {
		t.Fatalf("brain_reflect returned tool error: %v", brainReflectResult.GetError())
	}
	var brainReflectResp BrainReflectResponse
	brainReflectPayload, err := json.Marshal(brainReflectResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal brain reflect structured content: %v", err)
	}
	if err := json.Unmarshal(brainReflectPayload, &brainReflectResp); err != nil {
		t.Fatalf("unmarshal brain reflect structured content: %v", err)
	}
	if strings.TrimSpace(brainReflectResp.Reflection.Summary) == "" {
		t.Fatalf("expected non-empty brain reflection summary, got %+v", brainReflectResp)
	}

	brainConsolidateResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "brain_consolidate",
		Arguments: map[string]any{
			"reflect": map[string]any{
				"recall": map[string]any{
					"query":           "Summarize Alice and Acme",
					"session_id":      "brain-session-mcp",
					"scope":           MemoryScopeSession,
					"top_k_memories":  3,
					"top_k_knowledge": 2,
					"entity_names":    []string{"Alice", "Acme"},
				},
				"max_summary_chars": 180,
			},
			"memory_id":            "brain-summary-mcp",
			"session_id":           "brain-session-mcp",
			"scope":                MemoryScopeSession,
			"promote_to_knowledge": true,
			"promotion": map[string]any{
				"knowledge_id": "brain-summary-knowledge-mcp",
				"title":        "Brain Summary MCP",
			},
		},
	})
	if err != nil {
		t.Fatalf("call brain_consolidate: %v", err)
	}
	if brainConsolidateResult.IsError {
		t.Fatalf("brain_consolidate returned tool error: %v", brainConsolidateResult.GetError())
	}
	var brainConsolidateResp BrainConsolidateResponse
	brainConsolidatePayload, err := json.Marshal(brainConsolidateResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal brain consolidate structured content: %v", err)
	}
	if err := json.Unmarshal(brainConsolidatePayload, &brainConsolidateResp); err != nil {
		t.Fatalf("unmarshal brain consolidate structured content: %v", err)
	}
	if strings.TrimSpace(brainConsolidateResp.Memory.Content) == "" {
		t.Fatalf("expected saved summary memory from brain_consolidate, got %+v", brainConsolidateResp)
	}
	if brainConsolidateResp.Knowledge == nil || brainConsolidateResp.Knowledge.ID != "brain-summary-knowledge-mcp" {
		t.Fatalf("expected promoted summary knowledge from brain_consolidate, got %+v", brainConsolidateResp)
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
