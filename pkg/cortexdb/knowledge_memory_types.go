package cortexdb

import "time"

const (
	defaultMemoryNamespace = "default"
	defaultMemoryRole      = "memory"

	// MemoryScopeGlobal stores memories in a shared global bucket.
	MemoryScopeGlobal = "global"
	// MemoryScopeUser stores memories in a per-user bucket.
	MemoryScopeUser = "user"
	// MemoryScopeSession stores memories in a per-session bucket.
	MemoryScopeSession = "session"
)

// KnowledgeRecord is the high-level durable knowledge object returned by the library and tools.
type KnowledgeRecord struct {
	ID         string            `json:"id"`
	Title      string            `json:"title,omitempty"`
	Content    string            `json:"content,omitempty"`
	SourceURL  string            `json:"source_url,omitempty"`
	Author     string            `json:"author,omitempty"`
	Collection string            `json:"collection,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	ChunkIDs   []string          `json:"chunk_ids,omitempty"`
	Entities   []string          `json:"entities,omitempty"`
	CreatedAt  time.Time         `json:"created_at,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at,omitempty"`
}

// KnowledgeSaveRequest stores or replaces a durable knowledge item.
type KnowledgeSaveRequest struct {
	KnowledgeID  string              `json:"knowledge_id"`
	Title        string              `json:"title,omitempty"`
	Content      string              `json:"content"`
	SourceURL    string              `json:"source_url,omitempty"`
	Author       string              `json:"author,omitempty"`
	Collection   string              `json:"collection,omitempty"`
	ChunkSize    int                 `json:"chunk_size,omitempty"`
	ChunkOverlap int                 `json:"chunk_overlap,omitempty"`
	Metadata     map[string]string   `json:"metadata,omitempty"`
	Entities     []ToolEntityInput   `json:"entities,omitempty"`
	Relations    []ToolRelationInput `json:"relations,omitempty"`
	// ExpiresAt is an RFC 3339 instant after which the item is left out of
	// every search and removed by the sweep. Empty removes the expiry;
	// absent keeps the one stored, as knowledge_update does.
	ExpiresAt *string `json:"expires_at,omitempty"`
	// ReviewStatus, ReviewedBy and ReviewedAt decide the review: proposed,
	// accepted or rejected, who decided (the calling key when there is one),
	// and when (now when left out). Left out, the stored decision is kept,
	// whatever the write does with metadata: a decision is never cleared.
	ReviewStatus string `json:"review_status,omitempty"`
	ReviewedBy   string `json:"reviewed_by,omitempty"`
	ReviewedAt   string `json:"reviewed_at,omitempty"`
}

func (r KnowledgeSaveRequest) review() reviewFields {
	return reviewFields{Status: r.ReviewStatus, By: r.ReviewedBy, At: r.ReviewedAt}
}

// KnowledgeSaveResponse summarizes a knowledge write.
type KnowledgeSaveResponse struct {
	Knowledge       KnowledgeRecord `json:"knowledge"`
	DocumentNodeID  string          `json:"document_node_id,omitempty"`
	EntityNodeIDs   []string        `json:"entity_node_ids,omitempty"`
	RelationEdgeIDs []string        `json:"relation_edge_ids,omitempty"`
}

// KnowledgeUpdateRequest updates a durable knowledge item.
type KnowledgeUpdateRequest struct {
	KnowledgeID  string              `json:"knowledge_id"`
	Title        *string             `json:"title,omitempty"`
	Content      *string             `json:"content,omitempty"`
	SourceURL    *string             `json:"source_url,omitempty"`
	Author       *string             `json:"author,omitempty"`
	Collection   *string             `json:"collection,omitempty"`
	ChunkSize    *int                `json:"chunk_size,omitempty"`
	ChunkOverlap *int                `json:"chunk_overlap,omitempty"`
	Metadata     map[string]string   `json:"metadata,omitempty"`
	Entities     []ToolEntityInput   `json:"entities,omitempty"`
	Relations    []ToolRelationInput `json:"relations,omitempty"`
	// ExpiresAt sets the expiry, removes it when empty, and keeps the stored
	// one when absent, whatever the update does with metadata.
	ExpiresAt *string `json:"expires_at,omitempty"`
	// ReviewStatus, ReviewedBy and ReviewedAt decide the review: proposed,
	// accepted or rejected, who decided (the calling key when there is one),
	// and when (now when left out). Left out, the stored decision is kept,
	// whatever the write does with metadata: a decision is never cleared.
	ReviewStatus string `json:"review_status,omitempty"`
	ReviewedBy   string `json:"reviewed_by,omitempty"`
	ReviewedAt   string `json:"reviewed_at,omitempty"`
}

func (r KnowledgeUpdateRequest) review() reviewFields {
	return reviewFields{Status: r.ReviewStatus, By: r.ReviewedBy, At: r.ReviewedAt}
}

// KnowledgeGetRequest fetches a knowledge item by ID.
type KnowledgeGetRequest struct {
	// KnowledgeID is omitempty so that the MCP server, which infers the
	// tool's schema from this struct, does not require it beside the list.
	KnowledgeID string `json:"knowledge_id,omitempty"`
	// KnowledgeIDs fetches several at once, in the order given. An id that
	// names no document, or one the caller's scope does not reach, is listed
	// in Missing rather than failing the call.
	KnowledgeIDs []string `json:"knowledge_ids,omitempty"`
}

// KnowledgeGetResponse returns one knowledge item, or with KnowledgeIDs the
// ones found and the ids missing; Knowledge is then the first one found.
type KnowledgeGetResponse struct {
	Knowledge  KnowledgeRecord   `json:"knowledge"`
	Knowledges []KnowledgeRecord `json:"knowledges,omitempty"`
	Missing    []string          `json:"missing,omitempty"`
}

// KnowledgeDeleteRequest deletes a knowledge item by ID.
type KnowledgeDeleteRequest struct {
	KnowledgeID string `json:"knowledge_id"`
}

// KnowledgeDeleteResponse confirms a delete operation.
type KnowledgeDeleteResponse struct {
	KnowledgeID string `json:"knowledge_id"`
	Deleted     bool   `json:"deleted"`
}

// KnowledgeSearchRequest searches durable knowledge with vector GraphRAG when available or lexical GraphRAG otherwise.
type KnowledgeSearchRequest struct {
	Query               string         `json:"query"`
	Collection          string         `json:"collection,omitempty"`
	TopK                int            `json:"top_k,omitempty"`
	MaxHops             int            `json:"max_hops,omitempty"`
	MaxRelatedChunks    int            `json:"max_related_chunks,omitempty"`
	MaxContextChunks    int            `json:"max_context_chunks,omitempty"`
	MaxContextChars     int            `json:"max_context_chars,omitempty"`
	PerDocumentLimit    int            `json:"per_document_limit,omitempty"`
	ChunkWindow         int            `json:"chunk_window,omitempty"`
	DiversityLambda     float64        `json:"diversity_lambda,omitempty"`
	DisableRerank       bool           `json:"disable_rerank,omitempty"`
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
	// PPR tunes retrieval_mode "ppr"; ignored by every other mode.
	PPR *PPRRetrievalOptions `json:"ppr,omitempty"`
}

// KnowledgeSearchHit is a document-shaped search result aggregated from chunk retrieval.
type KnowledgeSearchHit struct {
	KnowledgeID string  `json:"knowledge_id"`
	Title       string  `json:"title,omitempty"`
	SourceURL   string  `json:"source_url,omitempty"`
	Author      string  `json:"author,omitempty"`
	Snippet     string  `json:"snippet,omitempty"`
	Score       float64 `json:"score"`
	// Rank is this hit's 1-based position in Results. Score is a fusion
	// value that is right to order by and wrong to read (see
	// GraphRAGChunkResult); Rank is what a caller comparing two runs wants.
	Rank int `json:"rank"`
	// VectorScore and LexicalScore are the best each retriever gave any chunk
	// of this document, zero when that retriever did not run or did not
	// return it. Populated only by hybrid retrieval.
	VectorScore  float64           `json:"vector_score,omitempty"`
	LexicalScore float64           `json:"lexical_score,omitempty"`
	ChunkIDs     []string          `json:"chunk_ids,omitempty"`
	Entities     []string          `json:"entities,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// KnowledgeSearchResponse contains grouped knowledge hits and the packed GraphRAG context.
type KnowledgeSearchResponse struct {
	Query    string                `json:"query"`
	Plan     RetrievalPlan         `json:"plan"`
	Decision RetrievalDecision     `json:"decision"`
	Results  []KnowledgeSearchHit  `json:"results"`
	Chunks   []GraphRAGChunkResult `json:"chunks,omitempty"`
	Entities []string              `json:"entities,omitempty"`
	Context  string                `json:"context,omitempty"`
	// Degraded names what did not run as it should have (see
	// DegradedEmbedderUnreachable); empty when the search ran whole.
	Degraded []string `json:"degraded,omitempty"`
}

// MemoryRecord is a high-level memory object stored in a dedicated memory bucket.
type MemoryRecord struct {
	ID         string         `json:"id"`
	UserID     string         `json:"user_id,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	Scope      string         `json:"scope,omitempty"`
	Namespace  string         `json:"namespace,omitempty"`
	Role       string         `json:"role,omitempty"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Importance float64        `json:"importance,omitempty"`
	TTLSeconds int            `json:"ttl_seconds,omitempty"`
	ExpiresAt  *time.Time     `json:"expires_at,omitempty"`
	// ReviewStatus and ReviewedBy mirror the metadata keys _review_status
	// and _reviewed_by (see review.go). Under the review gate, a proposed or
	// rejected memory is left out of search, recall and the graph walks from
	// a node.
	ReviewStatus string    `json:"review_status,omitempty"`
	ReviewedBy   string    `json:"reviewed_by,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
}

// MemorySaveRequest stores a memory in a dedicated memory bucket.
type MemorySaveRequest struct {
	MemoryID   string         `json:"memory_id"`
	UserID     string         `json:"user_id,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	Scope      string         `json:"scope,omitempty"`
	Namespace  string         `json:"namespace,omitempty"`
	Role       string         `json:"role,omitempty"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Importance float64        `json:"importance,omitempty"`
	TTLSeconds int            `json:"ttl_seconds,omitempty"`
	// Supersedes names memories this one replaces. They stay stored and
	// exported, but recall stops presenting them as current — without this the
	// old wording keeps answering with stale facts in a confident voice, and
	// the only fix was hunting down its id and deleting history.
	Supersedes []string `json:"supersedes,omitempty"`
	// Entities and Relations let a caller record what this memory is ABOUT in
	// the same call that stores it, the way SaveKnowledge already can. Without
	// them the graph could only be filled by a separate extraction pass, which
	// is what made agent-written memories arrive with no graph presence.
	Entities  []ToolEntityInput   `json:"entities,omitempty"`
	Relations []ToolRelationInput `json:"relations,omitempty"`
	// ReviewStatus, ReviewedBy and ReviewedAt decide the review: proposed,
	// accepted or rejected, who decided (the calling key when there is one),
	// and when (now when left out). Left out, the stored decision is kept,
	// whatever the write does with metadata: a decision is never cleared.
	ReviewStatus string `json:"review_status,omitempty"`
	ReviewedBy   string `json:"reviewed_by,omitempty"`
	ReviewedAt   string `json:"reviewed_at,omitempty"`
}

func (r MemorySaveRequest) review() reviewFields {
	return reviewFields{Status: r.ReviewStatus, By: r.ReviewedBy, At: r.ReviewedAt}
}

// MemorySaveResponse returns the stored memory.
type MemorySaveResponse struct {
	Memory MemoryRecord `json:"memory"`
}

// MemoryUpdateRequest updates a memory item.
type MemoryUpdateRequest struct {
	MemoryID   string         `json:"memory_id"`
	Content    *string        `json:"content,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Importance *float64       `json:"importance,omitempty"`
	TTLSeconds *int           `json:"ttl_seconds,omitempty"`
	// ReviewStatus, ReviewedBy and ReviewedAt decide the review: proposed,
	// accepted or rejected, who decided (the calling key when there is one),
	// and when (now when left out). Left out, the stored decision is kept,
	// whatever the write does with metadata: a decision is never cleared.
	ReviewStatus string `json:"review_status,omitempty"`
	ReviewedBy   string `json:"reviewed_by,omitempty"`
	ReviewedAt   string `json:"reviewed_at,omitempty"`
}

func (r MemoryUpdateRequest) review() reviewFields {
	return reviewFields{Status: r.ReviewStatus, By: r.ReviewedBy, At: r.ReviewedAt}
}

// MemoryGetRequest fetches a memory by ID.
type MemoryGetRequest struct {
	// MemoryID is omitempty for the same reason as KnowledgeGetRequest's.
	MemoryID string `json:"memory_id,omitempty"`
	// MemoryIDs fetches several at once, in the order given: the second step
	// after a recall with detail "index". An id that does not exist is listed
	// in Missing rather than failing the call.
	MemoryIDs []string `json:"memory_ids,omitempty"`
}

// MemoryGetResponse returns one memory.
type MemoryGetResponse struct {
	Memory   MemoryRecord   `json:"memory"`
	Memories []MemoryRecord `json:"memories,omitempty"`
	Missing  []string       `json:"missing,omitempty"`
}

// MemoryDeleteRequest deletes a memory by ID.
type MemoryDeleteRequest struct {
	MemoryID string `json:"memory_id"`
}

// MemoryDeleteResponse confirms a memory delete.
type MemoryDeleteResponse struct {
	MemoryID string `json:"memory_id"`
	Deleted  bool   `json:"deleted"`
	// GraphNodeRetracted is true when the memory had a node in the graph and
	// that node went with it. A memory saved without entities never had one.
	GraphNodeRetracted bool `json:"graph_node_retracted,omitempty"`
}

// MemorySearchRequest searches memories inside a resolved memory bucket.
type MemorySearchRequest struct {
	Query            string   `json:"query"`
	UserID           string   `json:"user_id,omitempty"`
	SessionID        string   `json:"session_id,omitempty"`
	Scope            string   `json:"scope,omitempty"`
	Namespace        string   `json:"namespace,omitempty"`
	TopK             int      `json:"top_k,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	AlternateQueries []string `json:"alternate_queries,omitempty"`
	// EntityNames let recall reach a memory through the graph when the memory
	// never spells the entity the way the question does. Lexical search cannot
	// do that, so before memories had graph nodes this hint had nowhere to go.
	EntityNames   []string       `json:"entity_names,omitempty"`
	RetrievalMode string         `json:"retrieval_mode,omitempty"`
	Plan          *RetrievalPlan `json:"plan,omitempty"`
	// PPR tunes retrieval_mode "ppr"; ignored by every other mode.
	PPR *PPRRetrievalOptions `json:"ppr,omitempty"`
}

// MemorySearchHit is one scored memory result.
type MemorySearchHit struct {
	Memory MemoryRecord `json:"memory"`
	Score  float64      `json:"score"`
	// Similarity is the query's raw vector similarity to the memory, before
	// the importance and recency boosts that make Score; zero when keyword
	// search found the hit. Score orders the list, Similarity is the number
	// that says how close the memory actually is.
	Similarity float64 `json:"similarity,omitempty"`
	// Coverage is the share of the question a keyword hit contains, each
	// term weighted by how rare it is in the store (see lexical_rarity.go);
	// zero when keyword search did not find the hit. It is to a keyword hit
	// what Similarity is to a semantic one.
	Coverage float64 `json:"coverage,omitempty"`
	// Via names the retrievers that found the memory: "semantic", "keyword",
	// "graph". A recall that fused several says which one each hit owes its
	// place to, so a person can tell a close meaning from a shared word.
	Via []string `json:"via,omitempty"`
}

// MemorySearchResponse contains retrieved memories.
type MemorySearchResponse struct {
	Query    string            `json:"query"`
	Plan     RetrievalPlan     `json:"plan"`
	Decision RetrievalDecision `json:"decision"`
	Results  []MemorySearchHit `json:"results"`
	// Degraded names what did not run as it should have (see
	// DegradedEmbedderUnreachable); empty when the search ran whole.
	Degraded []string `json:"degraded,omitempty"`
}
