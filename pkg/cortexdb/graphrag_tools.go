package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const defaultLexicalVectorDim = 64

// ToolDefinition describes a tool/function that an external LLM can call.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// GraphRAGToolbox exposes no-embedder-safe functions for external LLM orchestration.
type GraphRAGToolbox struct {
	db *DB
}

// ToolChunk is a chunk-shaped response used by tool APIs.
type ToolChunk struct {
	ID         string            `json:"id"`
	DocumentID string            `json:"document_id,omitempty"`
	Content    string            `json:"content"`
	Score      float64           `json:"score,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Entities   []string          `json:"entities,omitempty"`
}

// ToolIngestDocumentRequest stores a document and its chunks without requiring an embedder.
type ToolIngestDocumentRequest struct {
	DocumentID   string            `json:"document_id"`
	Title        string            `json:"title,omitempty"`
	Content      string            `json:"content"`
	Collection   string            `json:"collection,omitempty"`
	ChunkSize    int               `json:"chunk_size,omitempty"`
	ChunkOverlap int               `json:"chunk_overlap,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ToolIngestDocumentResponse summarizes lexical ingestion output.
type ToolIngestDocumentResponse struct {
	DocumentNodeID string   `json:"document_node_id"`
	ChunkNodeIDs   []string `json:"chunk_node_ids"`
	Collection     string   `json:"collection"`
}

// ToolEntityInput represents an extracted entity and where it was mentioned.
type ToolEntityInput struct {
	ID          string            `json:"id,omitempty"`
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"`
	Description string            `json:"description,omitempty"`
	ChunkIDs    []string          `json:"chunk_ids,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ToolUpsertEntitiesRequest writes entity nodes and mention edges.
type ToolUpsertEntitiesRequest struct {
	DocumentID string            `json:"document_id,omitempty"`
	Entities   []ToolEntityInput `json:"entities"`
}

// ToolUpsertEntitiesResponse summarizes entity writes.
type ToolUpsertEntitiesResponse struct {
	EntityNodeIDs    []string `json:"entity_node_ids"`
	MentionEdgeCount int      `json:"mention_edge_count"`
}

// ToolRelationInput represents a relation extracted by an external LLM.
type ToolRelationInput struct {
	From     string            `json:"from"`
	To       string            `json:"to"`
	Type     string            `json:"type,omitempty"`
	Weight   float64           `json:"weight,omitempty"`
	ChunkIDs []string          `json:"chunk_ids,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ToolUpsertRelationsRequest writes graph edges between entities.
type ToolUpsertRelationsRequest struct {
	DocumentID string              `json:"document_id,omitempty"`
	Relations  []ToolRelationInput `json:"relations"`
}

// ToolUpsertRelationsResponse summarizes written relation edges.
type ToolUpsertRelationsResponse struct {
	EdgeIDs []string `json:"edge_ids"`
}

// ToolSearchTextRequest performs lexical seed retrieval.
type ToolSearchTextRequest struct {
	Query            string   `json:"query"`
	Collection       string   `json:"collection,omitempty"`
	TopK             int      `json:"top_k,omitempty"`
	Threshold        float64  `json:"threshold,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	AlternateQueries []string `json:"alternate_queries,omitempty"`
	RetrievalMode    string   `json:"retrieval_mode,omitempty"`
	DisableGraph     bool     `json:"disable_graph,omitempty"`
}

// ToolSearchTextResponse returns chunk hits from lexical retrieval.
type ToolSearchTextResponse struct {
	Chunks []ToolChunk `json:"chunks"`
}

// ToolSearchChunksByEntitiesRequest finds chunks connected to the given entities.
type ToolSearchChunksByEntitiesRequest struct {
	EntityNames []string `json:"entity_names"`
	TopK        int      `json:"top_k,omitempty"`
	MaxHops     int      `json:"max_hops,omitempty"`
}

// ToolSearchChunksByEntitiesResponse returns chunks linked to entity nodes.
type ToolSearchChunksByEntitiesResponse struct {
	Chunks []ToolChunk `json:"chunks"`
}

// ToolExpandGraphRequest expands a graph neighborhood.
type ToolExpandGraphRequest struct {
	NodeIDs   []string `json:"node_ids"`
	MaxHops   int      `json:"max_hops,omitempty"`
	EdgeTypes []string `json:"edge_types,omitempty"`
	NodeTypes []string `json:"node_types,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

// ToolExpandGraphResponse returns a subgraph around the requested nodes.
type ToolExpandGraphResponse struct {
	Nodes []*graph.GraphNode `json:"nodes"`
	Edges []*graph.GraphEdge `json:"edges"`
}

// ToolGetNodesRequest fetches graph nodes by ID.
type ToolGetNodesRequest struct {
	NodeIDs []string `json:"node_ids"`
}

// ToolGetNodesResponse returns graph nodes.
type ToolGetNodesResponse struct {
	Nodes []*graph.GraphNode `json:"nodes"`
}

// ToolGetChunksRequest fetches chunk records by chunk ID.
type ToolGetChunksRequest struct {
	ChunkIDs      []string `json:"chunk_ids"`
	RetrievalMode string   `json:"retrieval_mode,omitempty"`
	DisableGraph  bool     `json:"disable_graph,omitempty"`
}

// ToolGetChunksResponse returns chunk records.
type ToolGetChunksResponse struct {
	Chunks []ToolChunk `json:"chunks"`
}

// ToolBuildContextRequest packs chunk text into a prompt context budget.
type ToolBuildContextRequest struct {
	ChunkIDs         []string `json:"chunk_ids"`
	MaxContextChunks int      `json:"max_context_chunks,omitempty"`
	MaxContextChars  int      `json:"max_context_chars,omitempty"`
	PerDocumentLimit int      `json:"per_document_limit,omitempty"`
	RetrievalMode    string   `json:"retrieval_mode,omitempty"`
	DisableGraph     bool     `json:"disable_graph,omitempty"`
}

// ToolBuildContextResponse returns packed chunks and the assembled context.
type ToolBuildContextResponse struct {
	Chunks  []GraphRAGChunkResult `json:"chunks"`
	Context string                `json:"context"`
}

// ToolSearchGraphRAGLexicalRequest performs no-embedder GraphRAG retrieval.
type ToolSearchGraphRAGLexicalRequest struct {
	Query            string   `json:"query"`
	Collection       string   `json:"collection,omitempty"`
	TopK             int      `json:"top_k,omitempty"`
	MaxHops          int      `json:"max_hops,omitempty"`
	MaxRelatedChunks int      `json:"max_related_chunks,omitempty"`
	MaxContextChunks int      `json:"max_context_chunks,omitempty"`
	MaxContextChars  int      `json:"max_context_chars,omitempty"`
	PerDocumentLimit int      `json:"per_document_limit,omitempty"`
	DiversityLambda  float64  `json:"diversity_lambda,omitempty"`
	EntityNames      []string `json:"entity_names,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	AlternateQueries []string `json:"alternate_queries,omitempty"`
	RetrievalMode    string   `json:"retrieval_mode,omitempty"`
	DisableGraph     bool     `json:"disable_graph,omitempty"`
}

// HasEmbedder reports whether the DB has an in-process embedder configured.
func (db *DB) HasEmbedder() bool {
	return db.embedder != nil
}

// GraphRAGTools returns the tool/function surface intended for external LLM orchestration.
func (db *DB) GraphRAGTools() *GraphRAGToolbox {
	return &GraphRAGToolbox{db: db}
}

// Definitions returns the JSON-schema-like definitions for the available tools.
func (t *GraphRAGToolbox) Definitions() []ToolDefinition {
	definitions := []ToolDefinition{
		{
			Name:        "ingest_document",
			Description: "Store a document, split it into chunks, index it lexically, and create document/chunk graph nodes.",
			InputSchema: toolObjectSchema(
				[]string{"document_id", "content"},
				map[string]any{
					"document_id":   toolStringSchema("Stable document ID."),
					"title":         toolStringSchema("Optional human-readable title."),
					"content":       toolStringSchema("Raw document content."),
					"collection":    toolStringSchema("Optional chunk collection name."),
					"chunk_size":    toolIntegerSchema("Optional chunk size in words."),
					"chunk_overlap": toolIntegerSchema("Optional chunk overlap in words."),
					"metadata":      toolMapSchema("Optional document metadata."),
				},
			),
		},
		{
			Name:        "upsert_entities",
			Description: "Create entity nodes and connect chunks to those entities with mention edges.",
			InputSchema: toolObjectSchema(
				[]string{"entities"},
				map[string]any{
					"document_id": toolStringSchema("Optional source document ID."),
					"entities": map[string]any{
						"type": "array",
						"items": toolObjectSchema(
							[]string{"name"},
							map[string]any{
								"id":          toolStringSchema("Optional explicit entity node ID."),
								"name":        toolStringSchema("Entity display name."),
								"type":        toolStringSchema("Optional entity type."),
								"description": toolStringSchema("Optional entity description."),
								"chunk_ids":   toolStringArraySchema("Chunk IDs that mention this entity."),
								"metadata":    toolMapSchema("Optional metadata."),
							},
						),
					},
				},
			),
		},
		{
			Name:        "upsert_relations",
			Description: "Create relation edges between entity nodes.",
			InputSchema: toolObjectSchema(
				[]string{"relations"},
				map[string]any{
					"document_id": toolStringSchema("Optional source document ID."),
					"relations": map[string]any{
						"type": "array",
						"items": toolObjectSchema(
							[]string{"from", "to"},
							map[string]any{
								"from":      toolStringSchema("Source entity name or entity node ID."),
								"to":        toolStringSchema("Target entity name or entity node ID."),
								"type":      toolStringSchema("Optional relation type."),
								"weight":    toolNumberSchema("Optional edge weight."),
								"chunk_ids": toolStringArraySchema("Optional supporting chunk IDs."),
								"metadata":  toolMapSchema("Optional metadata."),
							},
						),
					},
				},
			),
		},
		{
			Name:        "search_text",
			Description: "Run lexical BM25/FTS5 retrieval over stored chunks. Before calling, the LLM should expand the user goal into many keywords, aliases, synonyms, and multilingual variants, then pass them via keywords or alternate_queries.",
			InputSchema: toolObjectSchema(
				[]string{"query"},
				map[string]any{
					"query":             toolStringSchema("User goal or natural-language question."),
					"collection":        toolStringSchema("Optional collection name."),
					"top_k":             toolIntegerSchema("Maximum number of chunks to return."),
					"threshold":         toolNumberSchema("Optional minimum normalized score."),
					"keywords":          toolStringArraySchema("LLM-generated keyword bank derived from the goal. Include aliases, synonyms, abbreviations, translations, and domain terms."),
					"alternate_queries": toolStringArraySchema("Alternate phrasings generated by the LLM planner from the same goal."),
					"retrieval_mode":    toolEnumSchema("Preferred retrieval strategy.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":     toolBooleanSchema("Legacy alias. Set true to avoid graph-based entity enrichment and force lexical-only retrieval."),
				},
			),
		},
		{
			Name:        "search_chunks_by_entities",
			Description: "Find chunks linked to specific entity nodes.",
			InputSchema: toolObjectSchema(
				[]string{"entity_names"},
				map[string]any{
					"entity_names": toolStringArraySchema("Entity names or node IDs."),
					"top_k":        toolIntegerSchema("Maximum number of chunks to return."),
					"max_hops":     toolIntegerSchema("Traversal depth from entities."),
				},
			),
		},
		{
			Name:        "expand_graph",
			Description: "Expand a graph neighborhood and return a subgraph.",
			InputSchema: toolObjectSchema(
				[]string{"node_ids"},
				map[string]any{
					"node_ids":   toolStringArraySchema("Starting node IDs."),
					"max_hops":   toolIntegerSchema("Traversal depth."),
					"edge_types": toolStringArraySchema("Optional edge type filter."),
					"node_types": toolStringArraySchema("Optional node type filter."),
					"limit":      toolIntegerSchema("Optional node result limit."),
				},
			),
		},
		{
			Name:        "get_nodes",
			Description: "Fetch graph nodes by ID.",
			InputSchema: toolObjectSchema(
				[]string{"node_ids"},
				map[string]any{
					"node_ids": toolStringArraySchema("Node IDs to load."),
				},
			),
		},
		{
			Name:        "get_chunks",
			Description: "Fetch chunk records by chunk ID.",
			InputSchema: toolObjectSchema(
				[]string{"chunk_ids"},
				map[string]any{
					"chunk_ids":      toolStringArraySchema("Chunk IDs to load."),
					"retrieval_mode": toolEnumSchema("Preferred retrieval strategy for entity enrichment.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":  toolBooleanSchema("Legacy alias. Set true to skip graph-derived entity lookups while loading chunks."),
				},
			),
		},
		{
			Name:        "build_context",
			Description: "Pack chunk text into a bounded context window.",
			InputSchema: toolObjectSchema(
				[]string{"chunk_ids"},
				map[string]any{
					"chunk_ids":          toolStringArraySchema("Ordered chunk IDs."),
					"max_context_chunks": toolIntegerSchema("Maximum number of chunks to include."),
					"max_context_chars":  toolIntegerSchema("Maximum total character budget."),
					"per_document_limit": toolIntegerSchema("Maximum chunks per document."),
					"retrieval_mode":     toolEnumSchema("Preferred retrieval strategy for entity enrichment.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":      toolBooleanSchema("Legacy alias. Set true to skip graph-derived entity lookups while packing context."),
				},
			),
		},
		{
			Name:        "search_graphrag_lexical",
			Description: "Perform lexical GraphRAG retrieval using FTS5 seeds, graph expansion, rerank, and context packing. The LLM should first expand the user goal into many keywords, aliases, synonyms, and multilingual variants, then pass them via keywords or alternate_queries.",
			InputSchema: toolObjectSchema(
				[]string{"query"},
				map[string]any{
					"query":              toolStringSchema("User goal or natural-language question."),
					"collection":         toolStringSchema("Optional chunk collection name."),
					"top_k":              toolIntegerSchema("Seed chunk count."),
					"max_hops":           toolIntegerSchema("Graph expansion depth."),
					"max_related_chunks": toolIntegerSchema("Maximum graph-expanded chunks."),
					"max_context_chunks": toolIntegerSchema("Maximum chunks in final context."),
					"max_context_chars":  toolIntegerSchema("Maximum context character budget."),
					"per_document_limit": toolIntegerSchema("Maximum chunks per document."),
					"diversity_lambda":   toolNumberSchema("Rerank diversity weight between 0 and 1."),
					"entity_names":       toolStringArraySchema("Optional entities from structured LLM planning."),
					"keywords":           toolStringArraySchema("LLM-generated keyword bank derived from the goal. Include aliases, synonyms, abbreviations, translations, and domain terms."),
					"alternate_queries":  toolStringArraySchema("Alternate phrasings generated by the LLM planner from the same goal."),
					"retrieval_mode":     toolEnumSchema("Preferred retrieval strategy. Use lexical for speed, graph for full expansion, or auto for heuristic selection.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":      toolBooleanSchema("Legacy alias. Set true to disable graph traversal and force lexical-only retrieval."),
				},
			),
		},
	}
	return append(definitions, knowledgeMemoryToolDefinitions()...)
}

// Call dispatches a tool request from JSON input to a typed implementation.
func (t *GraphRAGToolbox) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	switch name {
	case "ingest_document":
		var req ToolIngestDocumentRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.IngestDocument(ctx, req)
	case "upsert_entities":
		var req ToolUpsertEntitiesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpsertEntities(ctx, req)
	case "upsert_relations":
		var req ToolUpsertRelationsRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpsertRelations(ctx, req)
	case "search_text":
		var req ToolSearchTextRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchText(ctx, req)
	case "search_chunks_by_entities":
		var req ToolSearchChunksByEntitiesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchChunksByEntities(ctx, req)
	case "expand_graph":
		var req ToolExpandGraphRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.ExpandGraph(ctx, req)
	case "get_nodes":
		var req ToolGetNodesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GetNodes(ctx, req)
	case "get_chunks":
		var req ToolGetChunksRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GetChunks(ctx, req)
	case "build_context":
		var req ToolBuildContextRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.BuildContext(ctx, req)
	case "search_graphrag_lexical":
		var req ToolSearchGraphRAGLexicalRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchGraphRAGLexical(ctx, req)
	case "knowledge_save":
		var req KnowledgeSaveRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SaveKnowledge(ctx, req)
	case "knowledge_update":
		var req KnowledgeUpdateRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpdateKnowledge(ctx, req)
	case "knowledge_get":
		var req KnowledgeGetRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GetKnowledge(ctx, req)
	case "knowledge_search":
		var req KnowledgeSearchRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchKnowledge(ctx, req)
	case "knowledge_delete":
		var req KnowledgeDeleteRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.DeleteKnowledge(ctx, req)
	case "memory_save":
		var req MemorySaveRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SaveMemory(ctx, req)
	case "memory_update":
		var req MemoryUpdateRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpdateMemory(ctx, req)
	case "memory_get":
		var req MemoryGetRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GetMemory(ctx, req)
	case "memory_search":
		var req MemorySearchRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchMemory(ctx, req)
	case "memory_delete":
		var req MemoryDeleteRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.DeleteMemory(ctx, req)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// IngestDocument stores lexical chunks and graph nodes without requiring an embedder.
func (t *GraphRAGToolbox) IngestDocument(ctx context.Context, req ToolIngestDocumentRequest) (*ToolIngestDocumentResponse, error) {
	if req.DocumentID == "" {
		return nil, fmt.Errorf("document_id is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, ErrEmptyText
	}

	ingestOpts := GraphRAGIngestOptions{
		Collection:   req.Collection,
		ChunkSize:    req.ChunkSize,
		ChunkOverlap: req.ChunkOverlap,
	}
	applyGraphRAGIngestDefaults(&ingestOpts)

	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	vectorDim, err := t.lexicalVectorDim(ctx, ingestOpts.Collection)
	if err != nil {
		return nil, err
	}
	if err := t.ensureLexicalCollection(ctx, ingestOpts.Collection, vectorDim); err != nil {
		return nil, err
	}

	chunks := splitGraphRAGText(req.Content, ingestOpts.ChunkSize, ingestOpts.ChunkOverlap)
	if len(chunks) == 0 {
		return nil, ErrEmptyText
	}

	docRecord := &core.Document{
		ID:      req.DocumentID,
		Title:   req.Title,
		Content: req.Content,
		Version: 1,
	}
	if err := t.db.upsertGraphRAGDocumentRecord(ctx, docRecord); err != nil {
		return nil, err
	}

	docNodeID := graphDocumentNodeID(req.DocumentID)
	docNode := &graph.GraphNode{
		ID:       docNodeID,
		Vector:   lexicalVectorForText(firstNonEmpty(req.Title, req.Content), vectorDim),
		Content:  firstNonEmpty(req.Title, req.Content),
		NodeType: "document",
		Properties: map[string]interface{}{
			"document_id": req.DocumentID,
			"title":       req.Title,
		},
	}
	if err := t.db.graph.UpsertNode(ctx, docNode); err != nil {
		return nil, fmt.Errorf("upsert document node: %w", err)
	}

	embeddings := make([]*core.Embedding, 0, len(chunks))
	chunkNodes := make([]*graph.GraphNode, 0, len(chunks))
	edges := make([]*graph.GraphEdge, 0, len(chunks)*2)
	chunkIDs := make([]string, 0, len(chunks))

	for i, chunk := range chunks {
		chunkID := graphChunkNodeID(req.DocumentID, i)
		chunkIDs = append(chunkIDs, chunkID)

		metadata := map[string]string{
			"graph_kind":  "chunk",
			"document_id": req.DocumentID,
			"chunk_index": fmt.Sprintf("%d", i),
			"title":       req.Title,
		}
		for k, v := range req.Metadata {
			metadata[k] = v
		}

		chunkVector := lexicalVectorForText(chunk, vectorDim)
		embeddings = append(embeddings, &core.Embedding{
			ID:         chunkID,
			Collection: ingestOpts.Collection,
			Vector:     chunkVector,
			Content:    chunk,
			DocID:      req.DocumentID,
			Metadata:   metadata,
		})
		chunkNodes = append(chunkNodes, &graph.GraphNode{
			ID:       chunkID,
			Vector:   chunkVector,
			Content:  chunk,
			NodeType: "chunk",
			Properties: map[string]interface{}{
				"document_id": req.DocumentID,
				"chunk_index": i,
				"title":       req.Title,
			},
		})
		edges = append(edges, &graph.GraphEdge{
			ID:         fmt.Sprintf("edge:doc_chunk:%s:%d", req.DocumentID, i),
			FromNodeID: docNodeID,
			ToNodeID:   chunkID,
			EdgeType:   "has_chunk",
			Weight:     1.0,
		})
		if i > 0 {
			edges = append(edges, &graph.GraphEdge{
				ID:         fmt.Sprintf("edge:chunk_next:%s:%d", req.DocumentID, i),
				FromNodeID: graphChunkNodeID(req.DocumentID, i-1),
				ToNodeID:   chunkID,
				EdgeType:   "next",
				Weight:     1.0,
			})
		}
	}

	if err := t.db.store.UpsertBatch(ctx, embeddings); err != nil {
		return nil, fmt.Errorf("upsert lexical chunks: %w", err)
	}
	if _, err := t.db.graph.UpsertNodesBatch(ctx, chunkNodes); err != nil {
		return nil, fmt.Errorf("upsert chunk nodes: %w", err)
	}
	if _, err := t.db.graph.UpsertEdgesBatch(ctx, edges); err != nil {
		return nil, fmt.Errorf("upsert chunk edges: %w", err)
	}

	return &ToolIngestDocumentResponse{
		DocumentNodeID: docNodeID,
		ChunkNodeIDs:   chunkIDs,
		Collection:     ingestOpts.Collection,
	}, nil
}

// UpsertEntities writes entity nodes and mention edges for caller-supplied structured extraction.
func (t *GraphRAGToolbox) UpsertEntities(ctx context.Context, req ToolUpsertEntitiesRequest) (*ToolUpsertEntitiesResponse, error) {
	if len(req.Entities) == 0 {
		return &ToolUpsertEntitiesResponse{}, nil
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	vectorDim, err := t.lexicalVectorDim(ctx, defaultGraphRAGCollection)
	if err != nil {
		return nil, err
	}

	nodes := make([]*graph.GraphNode, 0, len(req.Entities))
	edges := make([]*graph.GraphEdge, 0)
	entityIDs := make([]string, 0, len(req.Entities))

	for _, entity := range req.Entities {
		if strings.TrimSpace(entity.Name) == "" && strings.TrimSpace(entity.ID) == "" {
			continue
		}
		entityID := resolveEntityNodeID(entity.ID, entity.Name)
		entityIDs = append(entityIDs, entityID)

		properties := map[string]interface{}{}
		if entity.Name != "" {
			properties["name"] = entity.Name
		}
		if entity.Description != "" {
			properties["description"] = entity.Description
		}
		for k, v := range entity.Metadata {
			properties[k] = v
		}

		nodes = append(nodes, &graph.GraphNode{
			ID:         entityID,
			Vector:     lexicalVectorForText(strings.TrimSpace(entity.Name+" "+entity.Description), vectorDim),
			Content:    firstNonEmpty(entity.Name, entity.ID),
			NodeType:   firstNonEmpty(entity.Type, "entity"),
			Properties: properties,
		})

		for _, chunkID := range entity.ChunkIDs {
			if chunkID == "" {
				continue
			}
			edges = append(edges, &graph.GraphEdge{
				ID:         fmt.Sprintf("edge:mention:%s:%s", chunkID, entityID),
				FromNodeID: chunkID,
				ToNodeID:   entityID,
				EdgeType:   "mentions",
				Weight:     1.0,
			})
		}
	}

	if len(nodes) > 0 {
		if _, err := t.db.graph.UpsertNodesBatch(ctx, nodes); err != nil {
			return nil, fmt.Errorf("upsert entity nodes: %w", err)
		}
	}
	if len(edges) > 0 {
		if _, err := t.db.graph.UpsertEdgesBatch(ctx, edges); err != nil {
			return nil, fmt.Errorf("upsert mention edges: %w", err)
		}
	}

	return &ToolUpsertEntitiesResponse{
		EntityNodeIDs:    entityIDs,
		MentionEdgeCount: len(edges),
	}, nil
}

// UpsertRelations writes relation edges between entities.
func (t *GraphRAGToolbox) UpsertRelations(ctx context.Context, req ToolUpsertRelationsRequest) (*ToolUpsertRelationsResponse, error) {
	if len(req.Relations) == 0 {
		return &ToolUpsertRelationsResponse{}, nil
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	edges := make([]*graph.GraphEdge, 0, len(req.Relations))
	edgeIDs := make([]string, 0, len(req.Relations))
	for i, rel := range req.Relations {
		fromID := resolveEntityNodeID("", rel.From)
		toID := resolveEntityNodeID("", rel.To)
		if fromID == "" || toID == "" {
			continue
		}
		edgeType := firstNonEmpty(rel.Type, "related_to")
		edgeID := fmt.Sprintf("edge:relation:%s:%s:%s:%d", fromID, toID, edgeType, i)
		edgeIDs = append(edgeIDs, edgeID)

		properties := map[string]interface{}{}
		if req.DocumentID != "" {
			properties["document_id"] = req.DocumentID
		}
		if len(rel.ChunkIDs) > 0 {
			properties["chunk_ids"] = rel.ChunkIDs
		}
		for k, v := range rel.Metadata {
			properties[k] = v
		}

		weight := rel.Weight
		if weight == 0 {
			weight = 1.0
		}
		edges = append(edges, &graph.GraphEdge{
			ID:         edgeID,
			FromNodeID: fromID,
			ToNodeID:   toID,
			EdgeType:   edgeType,
			Weight:     weight,
			Properties: properties,
		})
	}

	if len(edges) > 0 {
		if _, err := t.db.graph.UpsertEdgesBatch(ctx, edges); err != nil {
			return nil, fmt.Errorf("upsert relation edges: %w", err)
		}
	}

	return &ToolUpsertRelationsResponse{EdgeIDs: edgeIDs}, nil
}

// SearchText runs lexical retrieval over chunk content.
func (t *GraphRAGToolbox) SearchText(ctx context.Context, req ToolSearchTextRequest) (*ToolSearchTextResponse, error) {
	results, err := t.searchTextCandidates(ctx, req)
	if err != nil {
		return nil, err
	}
	chunks, err := t.loadToolChunks(ctx, scoredEmbeddingsToMap(results), scoredEmbeddingsOrder(results), shouldLoadChunkEntities(req.RetrievalMode, req.DisableGraph, req.Query))
	if err != nil {
		return nil, err
	}
	return &ToolSearchTextResponse{Chunks: chunks}, nil
}

func (t *GraphRAGToolbox) searchTextCandidates(ctx context.Context, req ToolSearchTextRequest) ([]core.ScoredEmbedding, error) {
	searchOpts := TextSearchOptions{
		Collection: req.Collection,
		TopK:       req.TopK,
		Threshold:  req.Threshold,
	}

	queries := lexicalSearchQueries(req.Query, req.Keywords, req.AlternateQueries)
	if len(queries) == 0 {
		return nil, ErrEmptyText
	}

	merged := make(map[string]core.ScoredEmbedding)
	var firstErr error
	for idx, query := range queries {
		results, err := t.db.SearchTextOnly(ctx, query, searchOpts)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(results) == 0 {
			continue
		}

		scoreWeight := 1.0 - float64(idx)*0.05
		if scoreWeight < 0.8 {
			scoreWeight = 0.8
		}
		for _, result := range results {
			result.Score *= scoreWeight
			if existing, ok := merged[result.ID]; !ok || result.Score > existing.Score {
				merged[result.ID] = result
			}
		}
		if idx == 0 && len(merged) >= searchOpts.TopK {
			break
		}
	}

	if len(merged) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, nil
	}

	ordered := make([]core.ScoredEmbedding, 0, len(merged))
	for _, result := range merged {
		ordered = append(ordered, result)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Score == ordered[j].Score {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Score > ordered[j].Score
	})
	if len(ordered) > searchOpts.TopK {
		ordered = ordered[:searchOpts.TopK]
	}
	return ordered, nil
}

// SearchChunksByEntities finds chunks that are linked to the requested entities.
func (t *GraphRAGToolbox) SearchChunksByEntities(ctx context.Context, req ToolSearchChunksByEntitiesRequest) (*ToolSearchChunksByEntitiesResponse, error) {
	if len(req.EntityNames) == 0 {
		return &ToolSearchChunksByEntitiesResponse{}, nil
	}
	if req.TopK <= 0 {
		req.TopK = 10
	}
	if req.MaxHops <= 0 {
		req.MaxHops = 1
	}

	scoreMap := make(map[string]float64)
	for _, entityName := range req.EntityNames {
		entityID := resolveEntityNodeID("", entityName)
		neighbors, err := t.db.graph.Neighbors(ctx, entityID, graph.TraversalOptions{
			MaxDepth:  req.MaxHops,
			Direction: "both",
			NodeTypes: []string{"chunk"},
			Limit:     req.TopK * 8,
		})
		if err != nil {
			continue
		}
		for _, node := range neighbors {
			scoreMap[node.ID] += 1.0
		}
	}

	ordered := sortIDsByScore(scoreMap)
	if len(ordered) > req.TopK {
		ordered = ordered[:req.TopK]
	}
	chunks, err := t.loadToolChunks(ctx, scoreMap, ordered, true)
	if err != nil {
		return nil, err
	}
	return &ToolSearchChunksByEntitiesResponse{Chunks: chunks}, nil
}

// ExpandGraph expands a graph neighborhood and returns a materialized subgraph.
func (t *GraphRAGToolbox) ExpandGraph(ctx context.Context, req ToolExpandGraphRequest) (*ToolExpandGraphResponse, error) {
	if len(req.NodeIDs) == 0 {
		return &ToolExpandGraphResponse{}, nil
	}
	if req.MaxHops <= 0 {
		req.MaxHops = 1
	}

	nodeSet := make(map[string]struct{}, len(req.NodeIDs))
	for _, nodeID := range req.NodeIDs {
		if nodeID == "" {
			continue
		}
		nodeSet[nodeID] = struct{}{}
		neighbors, err := t.db.graph.Neighbors(ctx, nodeID, graph.TraversalOptions{
			MaxDepth:  req.MaxHops,
			Direction: "both",
			EdgeTypes: req.EdgeTypes,
			NodeTypes: req.NodeTypes,
			Limit:     req.Limit,
		})
		if err != nil {
			return nil, err
		}
		for _, node := range neighbors {
			nodeSet[node.ID] = struct{}{}
		}
	}

	nodeIDs := sortedKeysFromSet(nodeSet)
	subgraph, err := t.db.graph.Subgraph(ctx, nodeIDs)
	if err != nil {
		return nil, err
	}
	return &ToolExpandGraphResponse{Nodes: subgraph.Nodes, Edges: subgraph.Edges}, nil
}

// GetNodes fetches graph nodes by ID.
func (t *GraphRAGToolbox) GetNodes(ctx context.Context, req ToolGetNodesRequest) (*ToolGetNodesResponse, error) {
	nodes, err := t.db.graph.GetNodesBatch(ctx, req.NodeIDs)
	if err != nil {
		return nil, err
	}
	return &ToolGetNodesResponse{Nodes: nodes}, nil
}

// GetChunks fetches chunk records by ID.
func (t *GraphRAGToolbox) GetChunks(ctx context.Context, req ToolGetChunksRequest) (*ToolGetChunksResponse, error) {
	chunks, err := t.loadToolChunks(ctx, nil, req.ChunkIDs, shouldLoadChunkEntities(req.RetrievalMode, req.DisableGraph, ""))
	if err != nil {
		return nil, err
	}
	return &ToolGetChunksResponse{Chunks: chunks}, nil
}

// BuildContext packs chunk text into a bounded context string.
func (t *GraphRAGToolbox) BuildContext(ctx context.Context, req ToolBuildContextRequest) (*ToolBuildContextResponse, error) {
	chunks, err := t.loadToolChunks(ctx, nil, req.ChunkIDs, shouldLoadChunkEntities(req.RetrievalMode, req.DisableGraph, ""))
	if err != nil {
		return nil, err
	}

	queryOpts := GraphRAGQueryOptions{
		MaxContextChunks: req.MaxContextChunks,
		MaxContextChars:  req.MaxContextChars,
		PerDocumentLimit: req.PerDocumentLimit,
		Rerank:           false,
	}
	applyGraphRAGQueryDefaults(&queryOpts)

	graphChunks := make([]GraphRAGChunkResult, 0, len(chunks))
	for i, chunk := range chunks {
		graphChunks = append(graphChunks, GraphRAGChunkResult{
			ID:          chunk.ID,
			DocumentID:  chunk.DocumentID,
			Content:     chunk.Content,
			Score:       float64(len(chunks) - i),
			BaseScore:   float64(len(chunks) - i),
			RerankScore: float64(len(chunks) - i),
			Entities:    chunk.Entities,
		})
	}

	packed := packGraphRAGContext(graphChunks, queryOpts)
	return &ToolBuildContextResponse{
		Chunks:  packed,
		Context: buildGraphRAGContext(packed),
	}, nil
}

// SearchGraphRAGLexical performs no-embedder GraphRAG retrieval for external LLM orchestration.
func (t *GraphRAGToolbox) SearchGraphRAGLexical(ctx context.Context, req ToolSearchGraphRAGLexicalRequest) (*GraphRAGQueryResult, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, ErrEmptyText
	}

	opts := GraphRAGQueryOptions{
		Collection:       req.Collection,
		TopK:             req.TopK,
		MaxHops:          req.MaxHops,
		MaxRelatedChunks: req.MaxRelatedChunks,
		MaxContextChunks: req.MaxContextChunks,
		MaxContextChars:  req.MaxContextChars,
		PerDocumentLimit: req.PerDocumentLimit,
		DiversityLambda:  req.DiversityLambda,
		Rerank:           true,
		RetrievalMode:    req.RetrievalMode,
		DisableGraph:     req.DisableGraph,
	}
	applyGraphRAGQueryDefaults(&opts)

	seedResp, err := t.SearchText(ctx, ToolSearchTextRequest{
		Query:            req.Query,
		Collection:       opts.Collection,
		TopK:             opts.TopK,
		Keywords:         req.Keywords,
		AlternateQueries: req.AlternateQueries,
		RetrievalMode:    req.RetrievalMode,
		DisableGraph:     req.DisableGraph,
	})
	if err != nil {
		return nil, err
	}

	result := &GraphRAGQueryResult{Query: req.Query}
	useGraph := shouldUseGraphRetrieval(req.RetrievalMode, req.DisableGraph, req.Query, req.EntityNames)
	entityNames := req.EntityNames
	if useGraph && len(entityNames) == 0 {
		entityNames = extractEntityNames(extractTitleEntities(req.Query))
	}

	chunkResults := make(map[string]*GraphRAGChunkResult)
	seedIDs := make(map[string]struct{})
	entitySet := make(map[string]struct{})
	seedOrder := make([]string, 0, len(seedResp.Chunks))

	addChunk := func(chunk ToolChunk, seed bool) {
		existing, ok := chunkResults[chunk.ID]
		if !ok {
			existing = &GraphRAGChunkResult{
				ID:         chunk.ID,
				DocumentID: chunk.DocumentID,
				Content:    chunk.Content,
				Score:      chunk.Score,
				BaseScore:  chunk.Score,
			}
			chunkResults[chunk.ID] = existing
		} else if chunk.Score > existing.Score {
			existing.Score = chunk.Score
			existing.BaseScore = chunk.Score
		}
		if seed {
			if _, exists := seedIDs[chunk.ID]; !exists {
				seedIDs[chunk.ID] = struct{}{}
				seedOrder = append(seedOrder, chunk.ID)
			}
		}
	}

	for _, chunk := range seedResp.Chunks {
		addChunk(chunk, true)
	}

	if useGraph && len(entityNames) > 0 {
		entityResp, err := t.SearchChunksByEntities(ctx, ToolSearchChunksByEntitiesRequest{
			EntityNames: entityNames,
			TopK:        opts.TopK,
			MaxHops:     opts.MaxHops,
		})
		if err != nil {
			return nil, err
		}
		for _, chunk := range entityResp.Chunks {
			if chunk.Score < 0.75 {
				chunk.Score = 0.75
			}
			addChunk(chunk, true)
		}
	}

	if len(seedOrder) == 0 {
		if useGraph {
			result.Entities = sortedKeys(entitySet)
		}
		return result, nil
	}

	if !useGraph {
		allChunks := make([]GraphRAGChunkResult, 0, len(seedOrder))
		for _, seedID := range seedOrder {
			if chunk := chunkResults[seedID]; chunk != nil {
				allChunks = append(allChunks, *chunk)
			}
		}
		allChunks = rerankGraphRAGChunks(req.Query, allChunks, opts)
		allChunks = packGraphRAGContext(allChunks, opts)
		result.Chunks = allChunks
		result.Context = buildGraphRAGContext(allChunks)
		return result, nil
	}

	for _, seedID := range seedOrder {
		neighbors, err := t.db.graph.Neighbors(ctx, seedID, graph.TraversalOptions{
			MaxDepth:  opts.MaxHops,
			Direction: "both",
			Limit:     opts.TopK * 12,
		})
		if err != nil {
			return nil, err
		}
		for _, node := range neighbors {
			switch node.NodeType {
			case "entity":
				entitySet[node.Content] = struct{}{}
			case "chunk":
				chunk := ToolChunk{
					ID:      node.ID,
					Content: node.Content,
					Score:   0.5,
				}
				if documentID, ok := stringProperty(node.Properties, "document_id"); ok {
					chunk.DocumentID = documentID
				}
				addChunk(chunk, false)
			}
		}
	}

	for chunkID, chunk := range chunkResults {
		entities, err := t.getChunkEntityNames(ctx, chunkID)
		if err != nil {
			return nil, err
		}
		chunk.Entities = entities
		for _, entityName := range entities {
			entitySet[entityName] = struct{}{}
		}
	}

	seedChunks := make([]GraphRAGChunkResult, 0, len(seedOrder))
	for _, seedID := range seedOrder {
		if chunk := chunkResults[seedID]; chunk != nil {
			seedChunks = append(seedChunks, *chunk)
		}
	}

	relatedChunks := make([]GraphRAGChunkResult, 0, len(chunkResults))
	for chunkID, chunk := range chunkResults {
		if _, ok := seedIDs[chunkID]; ok {
			continue
		}
		relatedChunks = append(relatedChunks, *chunk)
	}
	sort.Slice(relatedChunks, func(i, j int) bool { return relatedChunks[i].Score > relatedChunks[j].Score })
	if len(relatedChunks) > opts.MaxRelatedChunks {
		relatedChunks = relatedChunks[:opts.MaxRelatedChunks]
	}

	allChunks := append(seedChunks, relatedChunks...)
	allChunks = rerankGraphRAGChunks(req.Query, allChunks, opts)
	allChunks = packGraphRAGContext(allChunks, opts)

	result.Chunks = allChunks
	result.Entities = sortedKeys(entitySet)
	result.Context = buildGraphRAGContext(allChunks)
	return result, nil
}

func (t *GraphRAGToolbox) lexicalVectorDim(ctx context.Context, collection string) (int, error) {
	if collection != "" {
		existing, err := t.db.store.GetCollection(ctx, collection)
		if err == nil && existing.Dimensions > 0 {
			return existing.Dimensions, nil
		}
	}
	if dim := t.db.store.Config().VectorDim; dim > 0 {
		return dim, nil
	}
	return defaultLexicalVectorDim, nil
}

func (t *GraphRAGToolbox) ensureLexicalCollection(ctx context.Context, name string, dim int) error {
	if name == "" {
		name = defaultGraphRAGCollection
	}
	_, err := t.db.store.GetCollection(ctx, name)
	if err == nil {
		return nil
	}
	_, err = t.db.store.CreateCollection(ctx, name, dim)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("ensure lexical collection: %w", err)
	}
	return nil
}

func lexicalVectorForText(text string, dim int) []float32 {
	if dim <= 0 {
		dim = defaultLexicalVectorDim
	}
	vector := make([]float32, dim)
	for _, token := range strings.Fields(strings.ToLower(text)) {
		token = normalizeToolToken(token)
		if token == "" {
			continue
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(token))
		index := int(h.Sum64() % uint64(dim))
		vector[index] += 1
	}
	if isAllZero(vector) {
		vector[0] = 1
	}
	return vector
}

func (t *GraphRAGToolbox) loadToolChunks(ctx context.Context, scoreMap map[string]float64, orderedIDs []string, includeEntities bool) ([]ToolChunk, error) {
	chunks := make([]ToolChunk, 0, len(orderedIDs))
	for _, chunkID := range orderedIDs {
		emb, err := t.db.store.GetByID(ctx, chunkID)
		if err != nil {
			continue
		}
		var entities []string
		if includeEntities {
			entities, err = t.getChunkEntityNames(ctx, chunkID)
			if err != nil {
				return nil, err
			}
		}
		score := 0.0
		if scoreMap != nil {
			score = scoreMap[chunkID]
		}
		chunks = append(chunks, ToolChunk{
			ID:         emb.ID,
			DocumentID: emb.DocID,
			Content:    emb.Content,
			Score:      score,
			Metadata:   emb.Metadata,
			Entities:   entities,
		})
	}
	return chunks, nil
}

func (t *GraphRAGToolbox) getChunkEntityNames(ctx context.Context, chunkID string) ([]string, error) {
	neighbors, err := t.db.graph.Neighbors(ctx, chunkID, graph.TraversalOptions{
		MaxDepth:  1,
		Direction: "both",
		NodeTypes: []string{"entity"},
		Limit:     32,
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(neighbors))
	for _, node := range neighbors {
		names = append(names, node.Content)
	}
	sort.Strings(names)
	return names, nil
}

func resolveEntityNodeID(id string, name string) string {
	if strings.HasPrefix(id, "entity:") {
		return id
	}
	if strings.HasPrefix(name, "entity:") {
		return name
	}
	if strings.TrimSpace(id) != "" {
		return graphEntityNodeID(id)
	}
	if strings.TrimSpace(name) != "" {
		return graphEntityNodeID(name)
	}
	return ""
}

func normalizeToolToken(token string) string {
	return strings.Trim(token, " \t\r\n.,!?;:\"'()[]{}<>")
}

func lexicalSearchQueries(query string, keywords []string, alternateQueries []string) []string {
	trimmed := strings.TrimSpace(query)

	queries := make([]string, 0, 5)
	seen := make(map[string]struct{}, 5)
	addQuery := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		queries = append(queries, value)
	}

	if trimmed != "" {
		addQuery(trimmed)
	}

	for _, alternateQuery := range alternateQueries {
		addQuery(alternateQuery)
	}

	plannedKeywords := lexicalQueryKeywords(strings.Join(keywords, " "))
	autoKeywords := lexicalQueryKeywords(trimmed)
	if len(autoKeywords) > 0 {
		addQuery(strings.Join(autoKeywords, " OR "))
	}

	allKeywords := mergeLexicalKeywords(plannedKeywords, autoKeywords)
	if len(allKeywords) > 0 {
		addQuery(strings.Join(formatFTSKeywords(allKeywords, false), " OR "))
		addQuery(strings.Join(formatFTSKeywords(allKeywords, true), " OR "))
	}

	return queries
}

func lexicalQueryKeywords(query string) []string {
	rawTokens := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	keywords := make([]string, 0, len(rawTokens))
	seen := make(map[string]struct{}, len(rawTokens))
	for _, token := range rawTokens {
		token = normalizeToolToken(token)
		if token == "" || len(token) < 2 {
			continue
		}
		if _, skip := lexicalQueryStopwords[token]; skip {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		keywords = append(keywords, token)
	}
	return keywords
}

func mergeLexicalKeywords(groups ...[]string) []string {
	merged := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, keyword := range group {
			if keyword == "" {
				continue
			}
			if _, ok := seen[keyword]; ok {
				continue
			}
			seen[keyword] = struct{}{}
			merged = append(merged, keyword)
		}
	}
	return merged
}

func formatFTSKeywords(keywords []string, prefix bool) []string {
	terms := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		if keyword == "" {
			continue
		}
		term := keyword
		if prefix && isASCIIAlphaNum(keyword) && len(keyword) > 2 {
			term += "*"
		}
		terms = append(terms, term)
	}
	return terms
}

var lexicalQueryStopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {},
	"does": {}, "for": {}, "from": {}, "how": {}, "in": {}, "into": {}, "is": {}, "it": {},
	"of": {}, "on": {}, "or": {}, "that": {}, "the": {}, "their": {}, "there": {}, "these": {},
	"this": {}, "to": {}, "was": {}, "were": {}, "what": {}, "when": {}, "where": {}, "which": {},
	"who": {}, "why": {}, "with": {},
}

func isASCIIAlphaNum(value string) bool {
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return value != ""
}

func isAllZero(values []float32) bool {
	for _, value := range values {
		if value != 0 {
			return false
		}
	}
	return true
}

func scoredEmbeddingsToMap(results []core.ScoredEmbedding) map[string]float64 {
	scoreMap := make(map[string]float64, len(results))
	for _, result := range results {
		scoreMap[result.ID] = result.Score
	}
	return scoreMap
}

func scoredEmbeddingsOrder(results []core.ScoredEmbedding) []string {
	ordered := make([]string, 0, len(results))
	for _, result := range results {
		ordered = append(ordered, result.ID)
	}
	return ordered
}

func sortIDsByScore(scoreMap map[string]float64) []string {
	ids := make([]string, 0, len(scoreMap))
	for id := range scoreMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scoreMap[ids[i]] == scoreMap[ids[j]] {
			return ids[i] < ids[j]
		}
		return scoreMap[ids[i]] > scoreMap[ids[j]]
	})
	return ids
}

func sortedKeysFromSet(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

func toolObjectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

func toolStringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func toolIntegerSchema(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func toolNumberSchema(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func toolBooleanSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func toolEnumSchema(description string, values ...string) map[string]any {
	enumValues := make([]any, 0, len(values))
	for _, value := range values {
		enumValues = append(enumValues, value)
	}
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        enumValues,
	}
}

func toolMapSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          description,
		"additionalProperties": true,
	}
}

func toolStringArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}
