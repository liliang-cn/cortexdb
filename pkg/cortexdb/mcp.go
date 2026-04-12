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
	addInferenceMCPTools(server, definitions, toolbox)
	addOntologyMCPTools(server, definitions, toolbox)

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
	addGraphRAGMCPTool(server, definitions["knowledge_graph_namespace_upsert"], func(ctx context.Context, req KnowledgeGraphNamespaceUpsertRequest) (KnowledgeGraphNamespaceUpsertResponse, error) {
		resp, err := toolbox.UpsertKnowledgeNamespace(ctx, req)
		if err != nil {
			return KnowledgeGraphNamespaceUpsertResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphNamespaceUpsertResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_namespace_list"], func(ctx context.Context, _ struct{}) (KnowledgeGraphNamespaceListResponse, error) {
		resp, err := toolbox.ListKnowledgeNamespaces(ctx)
		if err != nil {
			return KnowledgeGraphNamespaceListResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphNamespaceListResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_upsert"], func(ctx context.Context, req KnowledgeGraphUpsertRequest) (KnowledgeGraphUpsertResponse, error) {
		resp, err := toolbox.UpsertKnowledgeGraph(ctx, req)
		if err != nil {
			return KnowledgeGraphUpsertResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphUpsertResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_find"], func(ctx context.Context, req KnowledgeGraphFindRequest) (KnowledgeGraphFindResponse, error) {
		resp, err := toolbox.FindKnowledgeGraph(ctx, req)
		if err != nil {
			return KnowledgeGraphFindResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphFindResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_delete"], func(ctx context.Context, req KnowledgeGraphDeleteRequest) (KnowledgeGraphDeleteResponse, error) {
		resp, err := toolbox.DeleteKnowledgeGraph(ctx, req)
		if err != nil {
			return KnowledgeGraphDeleteResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphDeleteResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_import"], func(ctx context.Context, req KnowledgeGraphImportRequest) (KnowledgeGraphImportResponse, error) {
		resp, err := toolbox.ImportKnowledgeGraph(ctx, req)
		if err != nil {
			return KnowledgeGraphImportResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphImportResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_export"], func(ctx context.Context, req KnowledgeGraphExportRequest) (KnowledgeGraphExportResponse, error) {
		resp, err := toolbox.ExportKnowledgeGraph(ctx, req)
		if err != nil {
			return KnowledgeGraphExportResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphExportResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_query"], func(ctx context.Context, req KnowledgeGraphQueryRequest) (KnowledgeGraphQueryResponse, error) {
		resp, err := toolbox.QueryKnowledgeGraph(ctx, req)
		if err != nil {
			return KnowledgeGraphQueryResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphQueryResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_shacl_validate"], func(ctx context.Context, req KnowledgeGraphSHACLValidateRequest) (KnowledgeGraphSHACLValidateResponse, error) {
		resp, err := toolbox.ValidateKnowledgeGraphSHACL(ctx, req)
		if err != nil {
			return KnowledgeGraphSHACLValidateResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphSHACLValidateResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_infer_refresh"], func(ctx context.Context, req KnowledgeGraphInferenceRefreshRequest) (KnowledgeGraphInferenceRefreshResponse, error) {
		resp, err := toolbox.RefreshKnowledgeGraphInference(ctx, req)
		if err != nil {
			return KnowledgeGraphInferenceRefreshResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphInferenceRefreshResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_infer_summary"], func(ctx context.Context, req KnowledgeGraphInferenceSummaryRequest) (KnowledgeGraphInferenceSummaryResponse, error) {
		resp, err := toolbox.SummarizeKnowledgeGraphInference(ctx, req)
		if err != nil {
			return KnowledgeGraphInferenceSummaryResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphInferenceSummaryResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_infer_explain"], func(ctx context.Context, req KnowledgeGraphInferenceExplainRequest) (KnowledgeGraphInferenceExplainResponse, error) {
		resp, err := toolbox.ExplainKnowledgeGraphInference(ctx, req)
		if err != nil {
			return KnowledgeGraphInferenceExplainResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphInferenceExplainResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["knowledge_graph_infer_explain_match"], func(ctx context.Context, req KnowledgeGraphInferenceExplainMatchRequest) (KnowledgeGraphInferenceExplainMatchResponse, error) {
		resp, err := toolbox.ExplainKnowledgeGraphInferenceMatch(ctx, req)
		if err != nil {
			return KnowledgeGraphInferenceExplainMatchResponse{}, err
		}
		if resp == nil {
			return KnowledgeGraphInferenceExplainMatchResponse{}, nil
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
	addGraphRAGMCPTool(server, definitions["brain_remember"], func(ctx context.Context, req BrainRememberRequest) (BrainRememberResponse, error) {
		resp, err := toolbox.BrainRemember(ctx, req)
		if err != nil {
			return BrainRememberResponse{}, err
		}
		if resp == nil {
			return BrainRememberResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["brain_recall"], func(ctx context.Context, req BrainRecallRequest) (BrainRecallResponse, error) {
		resp, err := toolbox.BrainRecall(ctx, req)
		if err != nil {
			return BrainRecallResponse{}, err
		}
		if resp == nil {
			return BrainRecallResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["brain_build_context_pack"], func(ctx context.Context, req BrainBuildContextPackRequest) (BrainBuildContextPackResponse, error) {
		resp, err := toolbox.BrainBuildContextPack(ctx, req)
		if err != nil {
			return BrainBuildContextPackResponse{}, err
		}
		if resp == nil {
			return BrainBuildContextPackResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["brain_promote_to_knowledge"], func(ctx context.Context, req BrainPromoteToKnowledgeRequest) (BrainPromoteToKnowledgeResponse, error) {
		resp, err := toolbox.BrainPromoteToKnowledge(ctx, req)
		if err != nil {
			return BrainPromoteToKnowledgeResponse{}, err
		}
		if resp == nil {
			return BrainPromoteToKnowledgeResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["brain_expand_entity_context"], func(ctx context.Context, req BrainExpandEntityContextRequest) (BrainExpandEntityContextResponse, error) {
		resp, err := toolbox.BrainExpandEntityContext(ctx, req)
		if err != nil {
			return BrainExpandEntityContextResponse{}, err
		}
		if resp == nil {
			return BrainExpandEntityContextResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["brain_neighbors"], func(ctx context.Context, req BrainNeighborsRequest) (BrainNeighborsResponse, error) {
		resp, err := toolbox.BrainNeighbors(ctx, req)
		if err != nil {
			return BrainNeighborsResponse{}, err
		}
		if resp == nil {
			return BrainNeighborsResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["brain_shortest_path"], func(ctx context.Context, req BrainShortestPathRequest) (BrainShortestPathResponse, error) {
		resp, err := toolbox.BrainShortestPath(ctx, req)
		if err != nil {
			return BrainShortestPathResponse{}, err
		}
		if resp == nil {
			return BrainShortestPathResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["brain_reflect"], func(ctx context.Context, req BrainReflectRequest) (BrainReflectResponse, error) {
		resp, err := toolbox.BrainReflect(ctx, req)
		if err != nil {
			return BrainReflectResponse{}, err
		}
		if resp == nil {
			return BrainReflectResponse{}, nil
		}
		return *resp, nil
	})
	addGraphRAGMCPTool(server, definitions["brain_consolidate"], func(ctx context.Context, req BrainConsolidateRequest) (BrainConsolidateResponse, error) {
		resp, err := toolbox.BrainConsolidate(ctx, req)
		if err != nil {
			return BrainConsolidateResponse{}, err
		}
		if resp == nil {
			return BrainConsolidateResponse{}, nil
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

const defaultMCPInstructions = "Prefer the high-level brain_* tools when you want CortexDB to act as a unified brain across episodic memory, durable knowledge, graph expansion, reflection, and context packing. Use knowledge_* and memory_* when you need lower-level direct control over those individual stores. Use ontology_* to define the active entity and relation schema when you want validated knowledge-graph writes; use apply_inference to materialize deterministic inferred edges with provenance; fall back to the lower-level GraphRAG tools only when you need finer control. For search and recall, prefer sending a structured plan object that includes query, keywords, alternate_queries, entity_names, retrieval_mode, and filters. First expand the user's goal into many keywords, aliases, synonyms, abbreviations, and multilingual variants, then pass them through the plan or the legacy keywords and alternate_queries fields. Supply entity_names when known so graph expansion can recover results even if lexical seeds are sparse. Prefer retrieval_mode=lexical|graph|auto to control graph cost. When latency matters, set graph_light=true and optionally cap max_expansion_seeds, max_traversal_nodes, and max_entities_per_chunk. disable_graph remains only as a legacy compatibility alias."
