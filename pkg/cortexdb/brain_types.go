package cortexdb

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// KnowledgeMemoryRememberRequest stores one episodic memory item.
type KnowledgeMemoryRememberRequest = MemorySaveRequest

// KnowledgeMemoryRememberResponse returns the stored memory item.
type KnowledgeMemoryRememberResponse = MemorySaveResponse

// KnowledgeMemoryRecallRequest retrieves a fused view across episodic memory and durable knowledge.
type KnowledgeMemoryRecallRequest struct {
	Query               string         `json:"query"`
	UserID              string         `json:"user_id,omitempty"`
	SessionID           string         `json:"session_id,omitempty"`
	Scope               string         `json:"scope,omitempty"`
	Namespace           string         `json:"namespace,omitempty"`
	Collection          string         `json:"collection,omitempty"`
	TopKMemories        int            `json:"top_k_memories,omitempty"`
	TopKKnowledge       int            `json:"top_k_knowledge,omitempty"`
	MaxMemoryItems      int            `json:"max_memory_items,omitempty"`
	MaxMemoryChars      int            `json:"max_memory_chars,omitempty"`
	Keywords            []string       `json:"keywords,omitempty"`
	AlternateQueries    []string       `json:"alternate_queries,omitempty"`
	EntityNames         []string       `json:"entity_names,omitempty"`
	RetrievalMode       string         `json:"retrieval_mode,omitempty"`
	DisableMemory       bool           `json:"disable_memory,omitempty"`
	DisableKnowledge    bool           `json:"disable_knowledge,omitempty"`
	DisableGraph        bool           `json:"disable_graph,omitempty"`
	GraphLight          bool           `json:"graph_light,omitempty"`
	MaxHops             int            `json:"max_hops,omitempty"`
	MaxRelatedChunks    int            `json:"max_related_chunks,omitempty"`
	MaxContextChunks    int            `json:"max_context_chunks,omitempty"`
	MaxContextChars     int            `json:"max_context_chars,omitempty"`
	PerDocumentLimit    int            `json:"per_document_limit,omitempty"`
	MaxExpansionSeeds   int            `json:"max_expansion_seeds,omitempty"`
	MaxTraversalNodes   int            `json:"max_traversal_nodes,omitempty"`
	MaxEntitiesPerChunk int            `json:"max_entities_per_chunk,omitempty"`
	Plan                *RetrievalPlan `json:"plan,omitempty"`
}

// KnowledgeMemoryContextSection is one debug-friendly part of a built context pack.
type KnowledgeMemoryContextSection struct {
	Kind      string   `json:"kind"`
	Title     string   `json:"title,omitempty"`
	Text      string   `json:"text"`
	SourceIDs []string `json:"source_ids,omitempty"`
}

// KnowledgeMemoryContextPack is the assembled prompt/context payload plus source attribution.
type KnowledgeMemoryContextPack struct {
	Query        string                          `json:"query"`
	Text         string                          `json:"text"`
	Sections     []KnowledgeMemoryContextSection `json:"sections,omitempty"`
	MemoryIDs    []string                        `json:"memory_ids,omitempty"`
	KnowledgeIDs []string                        `json:"knowledge_ids,omitempty"`
	ChunkIDs     []string                        `json:"chunk_ids,omitempty"`
	Entities     []string                        `json:"entities,omitempty"`
}

// KnowledgeMemoryRecallResponse returns fused memory, knowledge, graph chunks, and packed context.
type KnowledgeMemoryRecallResponse struct {
	Query             string                     `json:"query"`
	MemoryPlan        RetrievalPlan              `json:"memory_plan"`
	MemoryDecision    RetrievalDecision          `json:"memory_decision"`
	KnowledgePlan     RetrievalPlan              `json:"knowledge_plan"`
	KnowledgeDecision RetrievalDecision          `json:"knowledge_decision"`
	Memories          []MemorySearchHit          `json:"memories,omitempty"`
	Knowledge         []KnowledgeSearchHit       `json:"knowledge,omitempty"`
	Chunks            []GraphRAGChunkResult      `json:"chunks,omitempty"`
	Entities          []string                   `json:"entities,omitempty"`
	ContextPack       KnowledgeMemoryContextPack `json:"context_pack"`
}

// KnowledgeMemoryBuildContextPackRequest retrieves and assembles a context pack from the KnowledgeMemory.
type KnowledgeMemoryBuildContextPackRequest = KnowledgeMemoryRecallRequest

// KnowledgeMemoryBuildContextPackResponse returns the assembled context pack plus source diagnostics.
type KnowledgeMemoryBuildContextPackResponse = KnowledgeMemoryRecallResponse

// KnowledgeMemoryPromoteToKnowledgeRequest promotes one or more memories into durable knowledge.
type KnowledgeMemoryPromoteToKnowledgeRequest struct {
	MemoryIDs    []string            `json:"memory_ids,omitempty"`
	KnowledgeID  string              `json:"knowledge_id,omitempty"`
	Title        string              `json:"title,omitempty"`
	SourceURL    string              `json:"source_url,omitempty"`
	Author       string              `json:"author,omitempty"`
	Collection   string              `json:"collection,omitempty"`
	ChunkSize    int                 `json:"chunk_size,omitempty"`
	ChunkOverlap int                 `json:"chunk_overlap,omitempty"`
	JoinWith     string              `json:"join_with,omitempty"`
	Metadata     map[string]string   `json:"metadata,omitempty"`
	Entities     []ToolEntityInput   `json:"entities,omitempty"`
	Relations    []ToolRelationInput `json:"relations,omitempty"`
}

// KnowledgeMemoryPromoteToKnowledgeResponse returns the saved knowledge plus source memories.
type KnowledgeMemoryPromoteToKnowledgeResponse struct {
	Knowledge       KnowledgeRecord `json:"knowledge"`
	Memories        []MemoryRecord  `json:"memories,omitempty"`
	DocumentNodeID  string          `json:"document_node_id,omitempty"`
	EntityNodeIDs   []string        `json:"entity_node_ids,omitempty"`
	RelationEdgeIDs []string        `json:"relation_edge_ids,omitempty"`
}

// KnowledgeMemoryExpandEntityContextRequest expands graph context around one or more entities.
type KnowledgeMemoryExpandEntityContextRequest struct {
	EntityIDs           []string `json:"entity_ids,omitempty"`
	EntityNames         []string `json:"entity_names,omitempty"`
	MaxHops             int      `json:"max_hops,omitempty"`
	EdgeTypes           []string `json:"edge_types,omitempty"`
	NodeTypes           []string `json:"node_types,omitempty"`
	Limit               int      `json:"limit,omitempty"`
	TopKChunks          int      `json:"top_k_chunks,omitempty"`
	MaxContextChunks    int      `json:"max_context_chunks,omitempty"`
	MaxContextChars     int      `json:"max_context_chars,omitempty"`
	PerDocumentLimit    int      `json:"per_document_limit,omitempty"`
	RetrievalMode       string   `json:"retrieval_mode,omitempty"`
	DisableGraph        bool     `json:"disable_graph,omitempty"`
	GraphLight          bool     `json:"graph_light,omitempty"`
	MaxEntitiesPerChunk int      `json:"max_entities_per_chunk,omitempty"`
}

// KnowledgeMemoryExpandEntityContextResponse returns the expanded subgraph and packed chunk context.
type KnowledgeMemoryExpandEntityContextResponse struct {
	EntityNodeIDs []string                   `json:"entity_node_ids,omitempty"`
	Nodes         []*graph.GraphNode         `json:"nodes,omitempty"`
	Edges         []*graph.GraphEdge         `json:"edges,omitempty"`
	Chunks        []GraphRAGChunkResult      `json:"chunks,omitempty"`
	ContextPack   KnowledgeMemoryContextPack `json:"context_pack"`
}

// KnowledgeMemoryNeighborsRequest expands neighbors around one node or entity.
type KnowledgeMemoryNeighborsRequest struct {
	NodeID     string   `json:"node_id,omitempty"`
	EntityName string   `json:"entity_name,omitempty"`
	MaxDepth   int      `json:"max_depth,omitempty"`
	EdgeTypes  []string `json:"edge_types,omitempty"`
	NodeTypes  []string `json:"node_types,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

// KnowledgeMemoryNeighborsResponse returns a resolved node ID and its neighbors.
type KnowledgeMemoryNeighborsResponse struct {
	ResolvedNodeID string             `json:"resolved_node_id"`
	Neighbors      []*graph.GraphNode `json:"neighbors,omitempty"`
}

// KnowledgeMemoryShortestPathRequest resolves and traverses a shortest path between two nodes/entities.
type KnowledgeMemoryShortestPathRequest struct {
	FromNodeID     string `json:"from_node_id,omitempty"`
	ToNodeID       string `json:"to_node_id,omitempty"`
	FromEntityName string `json:"from_entity_name,omitempty"`
	ToEntityName   string `json:"to_entity_name,omitempty"`
}

// KnowledgeMemoryShortestPathResponse contains the resolved IDs and path result.
type KnowledgeMemoryShortestPathResponse struct {
	FromNodeID string            `json:"from_node_id"`
	ToNodeID   string            `json:"to_node_id"`
	Path       *graph.PathResult `json:"path,omitempty"`
}

// KnowledgeMemoryReflectRequest collects sources and synthesizes a reflection.
type KnowledgeMemoryReflectRequest struct {
	Recall          KnowledgeMemoryRecallRequest `json:"recall"`
	Instructions    string                       `json:"instructions,omitempty"`
	MaxSummaryChars int                          `json:"max_summary_chars,omitempty"`
	MaxFacts        int                          `json:"max_facts,omitempty"`
	MaxThemes       int                          `json:"max_themes,omitempty"`
}

// KnowledgeMemoryReflection is a synthesized summary over retrieved memories and knowledge.
type KnowledgeMemoryReflection struct {
	Summary            string                     `json:"summary"`
	Themes             []string                   `json:"themes,omitempty"`
	Entities           []string                   `json:"entities,omitempty"`
	Facts              []string                   `json:"facts,omitempty"`
	SourceMemoryIDs    []string                   `json:"source_memory_ids,omitempty"`
	SourceKnowledgeIDs []string                   `json:"source_knowledge_ids,omitempty"`
	SourceChunkIDs     []string                   `json:"source_chunk_ids,omitempty"`
	ContextPack        KnowledgeMemoryContextPack `json:"context_pack"`
}

// KnowledgeMemoryReflectInput is the structured input passed to a pluggable reflector.
type KnowledgeMemoryReflectInput struct {
	Recall KnowledgeMemoryRecallResponse `json:"recall"`
}

// KnowledgeMemoryReflector synthesizes higher-order reflections on top of CortexDB retrieval results.
type KnowledgeMemoryReflector interface {
	Reflect(ctx context.Context, req KnowledgeMemoryReflectRequest, input KnowledgeMemoryReflectInput) (*KnowledgeMemoryReflection, error)
}

// KnowledgeMemoryReflectResponse contains the reflection and its retrieval inputs.
type KnowledgeMemoryReflectResponse struct {
	Reflection KnowledgeMemoryReflection     `json:"reflection"`
	Recall     KnowledgeMemoryRecallResponse `json:"recall"`
}

// KnowledgeMemoryConsolidateRequest reflects over sources, persists a summary memory, and can optionally promote it to knowledge.
type KnowledgeMemoryConsolidateRequest struct {
	Reflect            KnowledgeMemoryReflectRequest             `json:"reflect"`
	MemoryID           string                                    `json:"memory_id,omitempty"`
	UserID             string                                    `json:"user_id,omitempty"`
	SessionID          string                                    `json:"session_id,omitempty"`
	Scope              string                                    `json:"scope,omitempty"`
	Namespace          string                                    `json:"namespace,omitempty"`
	Role               string                                    `json:"role,omitempty"`
	Metadata           map[string]any                            `json:"metadata,omitempty"`
	Importance         float64                                   `json:"importance,omitempty"`
	TTLSeconds         int                                       `json:"ttl_seconds,omitempty"`
	PromoteToKnowledge bool                                      `json:"promote_to_knowledge,omitempty"`
	Promotion          *KnowledgeMemoryPromoteToKnowledgeRequest `json:"promotion,omitempty"`
}

// KnowledgeMemoryConsolidateResponse contains the saved summary memory and optional promoted knowledge.
type KnowledgeMemoryConsolidateResponse struct {
	Reflection KnowledgeMemoryReflection     `json:"reflection"`
	Recall     KnowledgeMemoryRecallResponse `json:"recall"`
	Memory     MemoryRecord                  `json:"memory"`
	Knowledge  *KnowledgeRecord              `json:"knowledge,omitempty"`
}
