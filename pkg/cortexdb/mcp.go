package cortexdb

import (
	"context"
	"log/slog"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMCPServerName  = "cortexdb-graphrag"
	defaultMCPServerTitle = "CortexDB GraphRAG"
)

// MCPServerOptions configures the CortexDB MCP server wrapper.
type MCPServerOptions struct {
	Implementation *mcp.Implementation
	Instructions   string
	Logger         *slog.Logger
}

// NewMCPServer returns an MCP server that exposes the GraphRAG tool surface.
func (db *DB) NewMCPServer(opts MCPServerOptions) *mcp.Server {
	impl := opts.Implementation
	if impl == nil {
		impl = &mcp.Implementation{
			Name:    defaultMCPServerName,
			Title:   defaultMCPServerTitle,
			Version: cortexdbroot.Version,
		}
	}

	instructions := opts.Instructions
	if instructions == "" {
		instructions = defaultMCPInstructions
	}

	server := mcp.NewServer(impl, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       opts.Logger,
	})

	toolbox := db.GraphRAGTools()
	definitions := make(map[string]ToolDefinition, len(toolbox.Definitions()))
	for _, definition := range toolbox.Definitions() {
		definitions[definition.Name] = definition
	}

	addGraphRAGMCPTool(server, definitions["ingest_document"], func(ctx context.Context, req ToolIngestDocumentRequest) (ToolIngestDocumentResponse, error) {
		resp, err := toolbox.IngestDocument(ctx, req)
		if err != nil {
			return ToolIngestDocumentResponse{}, err
		}
		if resp == nil {
			return ToolIngestDocumentResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["upsert_entities"], func(ctx context.Context, req ToolUpsertEntitiesRequest) (ToolUpsertEntitiesResponse, error) {
		resp, err := toolbox.UpsertEntities(ctx, req)
		if err != nil {
			return ToolUpsertEntitiesResponse{}, err
		}
		if resp == nil {
			return ToolUpsertEntitiesResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["upsert_relations"], func(ctx context.Context, req ToolUpsertRelationsRequest) (ToolUpsertRelationsResponse, error) {
		resp, err := toolbox.UpsertRelations(ctx, req)
		if err != nil {
			return ToolUpsertRelationsResponse{}, err
		}
		if resp == nil {
			return ToolUpsertRelationsResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["search_text"], func(ctx context.Context, req ToolSearchTextRequest) (ToolSearchTextResponse, error) {
		resp, err := toolbox.SearchText(ctx, req)
		if err != nil {
			return ToolSearchTextResponse{}, err
		}
		if resp == nil {
			return ToolSearchTextResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["search_chunks_by_entities"], func(ctx context.Context, req ToolSearchChunksByEntitiesRequest) (ToolSearchChunksByEntitiesResponse, error) {
		resp, err := toolbox.SearchChunksByEntities(ctx, req)
		if err != nil {
			return ToolSearchChunksByEntitiesResponse{}, err
		}
		if resp == nil {
			return ToolSearchChunksByEntitiesResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["expand_graph"], func(ctx context.Context, req ToolExpandGraphRequest) (ToolExpandGraphResponse, error) {
		resp, err := toolbox.ExpandGraph(ctx, req)
		if err != nil {
			return ToolExpandGraphResponse{}, err
		}
		if resp == nil {
			return ToolExpandGraphResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["get_nodes"], func(ctx context.Context, req ToolGetNodesRequest) (ToolGetNodesResponse, error) {
		resp, err := toolbox.GetNodes(ctx, req)
		if err != nil {
			return ToolGetNodesResponse{}, err
		}
		if resp == nil {
			return ToolGetNodesResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["get_chunks"], func(ctx context.Context, req ToolGetChunksRequest) (ToolGetChunksResponse, error) {
		resp, err := toolbox.GetChunks(ctx, req)
		if err != nil {
			return ToolGetChunksResponse{}, err
		}
		if resp == nil {
			return ToolGetChunksResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["build_context"], func(ctx context.Context, req ToolBuildContextRequest) (ToolBuildContextResponse, error) {
		resp, err := toolbox.BuildContext(ctx, req)
		if err != nil {
			return ToolBuildContextResponse{}, err
		}
		if resp == nil {
			return ToolBuildContextResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["search_graphrag_lexical"], func(ctx context.Context, req ToolSearchGraphRAGLexicalRequest) (GraphRAGQueryResult, error) {
		resp, err := toolbox.SearchGraphRAGLexical(ctx, req)
		if err != nil {
			return GraphRAGQueryResult{}, err
		}
		if resp == nil {
			return GraphRAGQueryResult{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_save"], func(ctx context.Context, req KnowledgeSaveRequest) (KnowledgeSaveResponse, error) {
		resp, err := toolbox.SaveKnowledge(ctx, req)
		if err != nil {
			return KnowledgeSaveResponse{}, err
		}
		if resp == nil {
			return KnowledgeSaveResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_update"], func(ctx context.Context, req KnowledgeUpdateRequest) (KnowledgeSaveResponse, error) {
		resp, err := toolbox.UpdateKnowledge(ctx, req)
		if err != nil {
			return KnowledgeSaveResponse{}, err
		}
		if resp == nil {
			return KnowledgeSaveResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_get"], func(ctx context.Context, req KnowledgeGetRequest) (KnowledgeGetResponse, error) {
		resp, err := toolbox.GetKnowledge(ctx, req)
		if err != nil {
			return KnowledgeGetResponse{}, err
		}
		if resp == nil {
			return KnowledgeGetResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_search"], func(ctx context.Context, req KnowledgeSearchRequest) (KnowledgeSearchResponse, error) {
		resp, err := toolbox.SearchKnowledge(ctx, req)
		if err != nil {
			return KnowledgeSearchResponse{}, err
		}
		if resp == nil {
			return KnowledgeSearchResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_delete"], func(ctx context.Context, req KnowledgeDeleteRequest) (KnowledgeDeleteResponse, error) {
		resp, err := toolbox.DeleteKnowledge(ctx, req)
		if err != nil {
			return KnowledgeDeleteResponse{}, err
		}
		if resp == nil {
			return KnowledgeDeleteResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["memory_save"], func(ctx context.Context, req MemorySaveRequest) (MemorySaveResponse, error) {
		resp, err := toolbox.SaveMemory(ctx, req)
		if err != nil {
			return MemorySaveResponse{}, err
		}
		if resp == nil {
			return MemorySaveResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["memory_update"], func(ctx context.Context, req MemoryUpdateRequest) (MemorySaveResponse, error) {
		resp, err := toolbox.UpdateMemory(ctx, req)
		if err != nil {
			return MemorySaveResponse{}, err
		}
		if resp == nil {
			return MemorySaveResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["memory_get"], func(ctx context.Context, req MemoryGetRequest) (MemoryGetResponse, error) {
		resp, err := toolbox.GetMemory(ctx, req)
		if err != nil {
			return MemoryGetResponse{}, err
		}
		if resp == nil {
			return MemoryGetResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["memory_search"], func(ctx context.Context, req MemorySearchRequest) (MemorySearchResponse, error) {
		resp, err := toolbox.SearchMemory(ctx, req)
		if err != nil {
			return MemorySearchResponse{}, err
		}
		if resp == nil {
			return MemorySearchResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["memory_delete"], func(ctx context.Context, req MemoryDeleteRequest) (MemoryDeleteResponse, error) {
		resp, err := toolbox.DeleteMemory(ctx, req)
		if err != nil {
			return MemoryDeleteResponse{}, err
		}
		if resp == nil {
			return MemoryDeleteResponse{}, nil
		}
		return *resp, nil
	})

	return server
}

// RunMCPStdio runs the CortexDB MCP server over stdin/stdout.
func (db *DB) RunMCPStdio(ctx context.Context, opts MCPServerOptions) error {
	return db.NewMCPServer(opts).Run(ctx, &mcp.StdioTransport{})
}

func addGraphRAGMCPTool[In, Out any](server *mcp.Server, definition ToolDefinition, handler func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        definition.Name,
		Description: definition.Description,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := handler(ctx, input)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, output, nil
	})
}

const defaultMCPInstructions = "Use the CortexDB high-level knowledge_* and memory_* tools for durable storage, retrieval, and memory management; fall back to the lower-level GraphRAG tools only when you need finer control. When searching, first expand the user's goal into many keywords, aliases, synonyms, abbreviations, and multilingual variants, then pass them through the keywords and alternate_queries fields. Supply entity_names when known so graph expansion can recover results even if lexical seeds are sparse. Prefer retrieval_mode=lexical|graph|auto to control graph cost; disable_graph remains only as a legacy compatibility alias."
