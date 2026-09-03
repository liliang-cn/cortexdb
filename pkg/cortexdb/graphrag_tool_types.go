package cortexdb

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const defaultLexicalVectorDim = 64

// ToolDefinition describes a tool/function that an external LLM can call.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	// Mutates reports whether calling this tool changes the brain.
	//
	// It exists so authorization can tell a read from a write through the
	// generic CallTool entry point, which otherwise dispatches on an opaque
	// name and forces every tool to be treated as a write — making a read-only
	// key unable to call anything at all, including search.
	//
	// The declaration lives here, next to the tool, rather than in a table
	// beside the policy: a second list would have to be hand-synced with this
	// one, and the failure mode of that drifting is a write classified as a
	// read. The zero value is false, so a definition that forgets to say is
	// claimed to be a read — which is why TestEveryToolDeclaresWhetherItWrites
	// exists to make forgetting impossible rather than merely unlikely.
	Mutates bool `json:"mutates,omitempty"`
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
	From           string            `json:"from"`
	To             string            `json:"to"`
	Type           string            `json:"type,omitempty"`
	Weight         float64           `json:"weight,omitempty"`
	ChunkIDs       []string          `json:"chunk_ids,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Inferred       bool              `json:"inferred,omitempty"`
	Provenance     string            `json:"provenance,omitempty"`
	RuleID         string            `json:"rule_id,omitempty"`
	SupportEdgeIDs []string          `json:"support_edge_ids,omitempty"`
}

// ToolUpsertRelationsRequest writes graph edges between entities.
type ToolUpsertRelationsRequest struct {
	DocumentID string              `json:"document_id,omitempty"`
	Relations  []ToolRelationInput `json:"relations"`
}

// ToolUpsertRelationsResponse summarizes written relation edges.
type ToolUpsertRelationsResponse struct {
	EdgeIDs []string `json:"edge_ids"`
	// Written is how many edges reached the store. It is reported because it is not always
	// len(EdgeIDs): an edge whose endpoints do not exist as nodes is rejected by the store, and the
	// batch result carrying that news used to be discarded — a call that wrote nothing returned the
	// ids of everything it had been asked to write.
	Written int `json:"written"`
	// Rejected names the edges the store would not take, so a caller can see which relation had an
	// endpoint that was never created rather than discovering later that the graph has no edges.
	Rejected []string `json:"rejected,omitempty"`
}

// ToolSearchTextRequest performs lexical seed retrieval.
type ToolSearchTextRequest struct {
	Query               string         `json:"query"`
	Collection          string         `json:"collection,omitempty"`
	TopK                int            `json:"top_k,omitempty"`
	Threshold           float64        `json:"threshold,omitempty"`
	Keywords            []string       `json:"keywords,omitempty"`
	AlternateQueries    []string       `json:"alternate_queries,omitempty"`
	RetrievalMode       string         `json:"retrieval_mode,omitempty"`
	DisableGraph        bool           `json:"disable_graph,omitempty"`
	GraphLight          bool           `json:"graph_light,omitempty"`
	MaxEntitiesPerChunk int            `json:"max_entities_per_chunk,omitempty"`
	Plan                *RetrievalPlan `json:"plan,omitempty"`
}

// ToolSearchTextResponse returns chunk hits from lexical retrieval.
type ToolSearchTextResponse struct {
	Plan     RetrievalPlan     `json:"plan"`
	Decision RetrievalDecision `json:"decision"`
	Chunks   []ToolChunk       `json:"chunks"`
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

// ToolFindNodesRequest looks graph nodes up by what they are called.
type ToolFindNodesRequest struct {
	Names []string `json:"names"`
	// Optional filter, e.g. []string{"Concept"}. Empty means any type.
	NodeTypes []string `json:"node_types,omitempty"`
	// Per-name cap. Zero means the default.
	Limit int `json:"limit,omitempty"`
}

// ToolFindNodesResponse returns what each requested name resolved to.
type ToolFindNodesResponse struct {
	Matches []ToolNodeNameMatch `json:"matches"`
}

// ToolNodeNameMatch is one requested name and the nodes it found, best first.
type ToolNodeNameMatch struct {
	Name  string             `json:"name"`
	Nodes []*graph.GraphNode `json:"nodes"`
	// How the best node was found: "exact", "fold" (case/space/punctuation
	// collapsed) or "contains". Reported because the three carry very different
	// confidence, and a caller acting on a "contains" hit should be able to
	// choose not to.
	Match string `json:"match,omitempty"`
}

// ToolGetChunksRequest fetches chunk records by chunk ID.
type ToolGetChunksRequest struct {
	ChunkIDs            []string `json:"chunk_ids"`
	RetrievalMode       string   `json:"retrieval_mode,omitempty"`
	DisableGraph        bool     `json:"disable_graph,omitempty"`
	GraphLight          bool     `json:"graph_light,omitempty"`
	MaxEntitiesPerChunk int      `json:"max_entities_per_chunk,omitempty"`
}

// ToolGetChunksResponse returns chunk records.
type ToolGetChunksResponse struct {
	Chunks []ToolChunk `json:"chunks"`
}

// ToolBuildContextRequest packs chunk text into a prompt context budget.
type ToolBuildContextRequest struct {
	ChunkIDs            []string `json:"chunk_ids"`
	MaxContextChunks    int      `json:"max_context_chunks,omitempty"`
	MaxContextChars     int      `json:"max_context_chars,omitempty"`
	PerDocumentLimit    int      `json:"per_document_limit,omitempty"`
	RetrievalMode       string   `json:"retrieval_mode,omitempty"`
	DisableGraph        bool     `json:"disable_graph,omitempty"`
	GraphLight          bool     `json:"graph_light,omitempty"`
	MaxEntitiesPerChunk int      `json:"max_entities_per_chunk,omitempty"`
}

// ToolBuildContextResponse returns packed chunks and the assembled context.
type ToolBuildContextResponse struct {
	Chunks  []GraphRAGChunkResult `json:"chunks"`
	Context string                `json:"context"`
}

// ToolSearchGraphRAGLexicalRequest performs no-embedder GraphRAG retrieval.
type ToolSearchGraphRAGLexicalRequest struct {
	Query               string         `json:"query"`
	Collection          string         `json:"collection,omitempty"`
	TopK                int            `json:"top_k,omitempty"`
	MaxHops             int            `json:"max_hops,omitempty"`
	MaxRelatedChunks    int            `json:"max_related_chunks,omitempty"`
	MaxContextChunks    int            `json:"max_context_chunks,omitempty"`
	MaxContextChars     int            `json:"max_context_chars,omitempty"`
	PerDocumentLimit    int            `json:"per_document_limit,omitempty"`
	DisableRerank       bool           `json:"disable_rerank,omitempty"`
	DiversityLambda     float64        `json:"diversity_lambda,omitempty"`
	EntityNames         []string       `json:"entity_names,omitempty"`
	Keywords            []string       `json:"keywords,omitempty"`
	AlternateQueries    []string       `json:"alternate_queries,omitempty"`
	RetrievalMode       string         `json:"retrieval_mode,omitempty"`
	DisableGraph        bool           `json:"disable_graph,omitempty"`
	GraphLight          bool           `json:"graph_light,omitempty"`
	MaxExpansionSeeds   int            `json:"max_expansion_seeds,omitempty"`
	MaxTraversalNodes   int            `json:"max_traversal_nodes,omitempty"`
	MaxEntitiesPerChunk int            `json:"max_entities_per_chunk,omitempty"`
	Plan                *RetrievalPlan `json:"plan,omitempty"`
}

// HasEmbedder reports whether the DB has an in-process embedder configured.
func (db *DB) HasEmbedder() bool {
	return db.embedder != nil
}

// GraphRAGTools returns the tool/function surface intended for external LLM orchestration.
func (db *DB) GraphRAGTools() *GraphRAGToolbox {
	return &GraphRAGToolbox{db: db}
}

// Query runs the composable CortexDB Query API through the toolbox surface.
func (t *GraphRAGToolbox) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
	return t.db.Query(ctx, req)
}
