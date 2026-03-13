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
}

// KnowledgeGetRequest fetches a knowledge item by ID.
type KnowledgeGetRequest struct {
	KnowledgeID string `json:"knowledge_id"`
}

// KnowledgeGetResponse returns one knowledge item.
type KnowledgeGetResponse struct {
	Knowledge KnowledgeRecord `json:"knowledge"`
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

// KnowledgeSearchHit is a document-shaped search result aggregated from chunk retrieval.
type KnowledgeSearchHit struct {
	KnowledgeID string            `json:"knowledge_id"`
	Title       string            `json:"title,omitempty"`
	SourceURL   string            `json:"source_url,omitempty"`
	Author      string            `json:"author,omitempty"`
	Snippet     string            `json:"snippet,omitempty"`
	Score       float64           `json:"score"`
	ChunkIDs    []string          `json:"chunk_ids,omitempty"`
	Entities    []string          `json:"entities,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// KnowledgeSearchResponse contains grouped knowledge hits and the packed GraphRAG context.
type KnowledgeSearchResponse struct {
	Query    string                `json:"query"`
	Results  []KnowledgeSearchHit  `json:"results"`
	Chunks   []GraphRAGChunkResult `json:"chunks,omitempty"`
	Entities []string              `json:"entities,omitempty"`
	Context  string                `json:"context,omitempty"`
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
	CreatedAt  time.Time      `json:"created_at,omitempty"`
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
}

// MemoryGetRequest fetches a memory by ID.
type MemoryGetRequest struct {
	MemoryID string `json:"memory_id"`
}

// MemoryGetResponse returns one memory.
type MemoryGetResponse struct {
	Memory MemoryRecord `json:"memory"`
}

// MemoryDeleteRequest deletes a memory by ID.
type MemoryDeleteRequest struct {
	MemoryID string `json:"memory_id"`
}

// MemoryDeleteResponse confirms a memory delete.
type MemoryDeleteResponse struct {
	MemoryID string `json:"memory_id"`
	Deleted  bool   `json:"deleted"`
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
	RetrievalMode    string   `json:"retrieval_mode,omitempty"`
}

// MemorySearchHit is one scored memory result.
type MemorySearchHit struct {
	Memory MemoryRecord `json:"memory"`
	Score  float64      `json:"score"`
}

// MemorySearchResponse contains retrieved memories.
type MemorySearchResponse struct {
	Query   string            `json:"query"`
	Results []MemorySearchHit `json:"results"`
}
