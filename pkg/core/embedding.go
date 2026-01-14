package core

import "context"

// Embedding represents a vector embedding with associated metadata
type Embedding struct {
	ID           string            `json:"id"`
	CollectionID int               `json:"collection_id,omitempty"`
	Collection   string            `json:"collection,omitempty"`
	Vector       []float32         `json:"vector"`
	Content      string            `json:"content"`
	DocID        string            `json:"docId,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	ACL          []string          `json:"acl,omitempty"` // Allowed user IDs or groups
}

// ScoredEmbedding represents an embedding with similarity score
type ScoredEmbedding struct {
	Embedding
	Score float64 `json:"score"`
}

// SearchOptions defines options for vector search
type SearchOptions struct {
	Collection string            `json:"collection,omitempty"` // Collection name to search in
	TopK       int               `json:"topK"`
	Filter     map[string]string `json:"filter,omitempty"`
	Threshold  float64           `json:"threshold,omitempty"`
	QueryText  string            `json:"queryText,omitempty"`  // Optional query text for enhanced matching
	TextWeight float64           `json:"textWeight,omitempty"` // Weight for text similarity (0.0-1.0, default 0.3)
}

// StoreStats provides statistics about the vector store
type StoreStats struct {
	Count      int64 `json:"count"`
	Dimensions int   `json:"dimensions"`
	Size       int64 `json:"size"`
}

// DocumentInfo provides information about a document in the store
type DocumentInfo struct {
	DocID          string  `json:"docId"`
	EmbeddingCount int     `json:"embeddingCount"`
	FirstCreated   *string `json:"firstCreated,omitempty"`
	LastUpdated    *string `json:"lastUpdated,omitempty"`
}

// HNSWConfig represents configuration options for HNSW indexing
type HNSWConfig struct {
	Enabled        bool `json:"enabled"`
	M              int  `json:"m"`              // Maximum connections per node (default: 16)
	EfConstruction int  `json:"efConstruction"` // Candidates during construction (default: 200)
	EfSearch       int  `json:"efSearch"`       // Candidates during search (default: 50)
}

// DefaultHNSWConfig returns default HNSW configuration
func DefaultHNSWConfig() HNSWConfig {
	return HNSWConfig{
		Enabled:        false,
		M:              16,
		EfConstruction: 200,
		EfSearch:       50,
	}
}

// IVFConfig represents configuration options for IVF indexing
type IVFConfig struct {
	Enabled    bool `json:"enabled"`
	NCentroids int  `json:"nCentroids"` // Number of centroids (default: 100)
	NProbe     int  `json:"nProbe"`     // Number of clusters to search (default: 10)
}

// DefaultIVFConfig returns default IVF configuration
func DefaultIVFConfig() IVFConfig {
	return IVFConfig{
		Enabled:    false,
		NCentroids: 100,
		NProbe:     10,
	}
}

// TextSimilarityConfig represents configuration for text-based similarity
type TextSimilarityConfig struct {
	Enabled       bool    `json:"enabled"`       // Enable text similarity matching
	DefaultWeight float64 `json:"defaultWeight"` // Default weight for text similarity (0.0-1.0)
}

// DefaultTextSimilarityConfig returns default text similarity configuration
func DefaultTextSimilarityConfig() TextSimilarityConfig {
	return TextSimilarityConfig{
		Enabled:       true, // Enabled by default
		DefaultWeight: 0.3,  // 30% text similarity, 70% vector similarity
	}
}

// QuantizationConfig represents configuration for vector quantization
type QuantizationConfig struct {
	Enabled bool   `json:"enabled"` // Enable quantization
	Type    string `json:"type"`    // "scalar" (SQ8) or "binary" (BQ)
	NBits   int    `json:"nBits"`   // Bits per component (default 8 for SQ8)
}

// DefaultQuantizationConfig returns default quantization configuration
func DefaultQuantizationConfig() QuantizationConfig {
	return QuantizationConfig{
		Enabled: false,
		Type:    "scalar",
		NBits:   8,
	}
}

// AdaptPolicy defines how to handle vector dimension mismatches
type AdaptPolicy int

const (
	StrictMode   AdaptPolicy = iota // Error on dimension mismatch (default)
	SmartAdapt                      // Intelligent adaptation based on data distribution
	AutoTruncate                    // Always truncate to smaller dimension
	AutoPad                         // Always pad to larger dimension
	WarnOnly                        // Only warn, don't auto-adapt
)

// IndexType defines the type of index to use
type IndexType int

const (
	IndexTypeHNSW IndexType = iota
	IndexTypeIVF
	IndexTypeFlat
)

// Config represents configuration options for the vector store
type Config struct {
	Path           string               `json:"path"`                    // Database file path
	VectorDim      int                  `json:"vectorDim"`               // Expected vector dimension, 0 = auto-detect
	AutoDimAdapt   AdaptPolicy          `json:"autoDimAdapt"`            // How to handle dimension mismatches
	SimilarityFn   SimilarityFunc       `json:"-"`                       // Similarity function
	IndexType      IndexType            `json:"indexType"`               // Index type to use
	HNSW           HNSWConfig           `json:"hnsw,omitempty"`          // HNSW index configuration
	IVF            IVFConfig            `json:"ivf,omitempty"`           // IVF index configuration
	TextSimilarity TextSimilarityConfig `json:"textSimilarity,omitempty"` // Text similarity configuration
	Quantization   QuantizationConfig   `json:"quantization,omitempty"`   // Quantization configuration
	Logger         Logger               `json:"-"`                       // Logger instance (defaults to nop logger)
}

// DefaultConfig returns a default configuration
func DefaultConfig() Config {
	return Config{
		VectorDim:      0,                              // Auto-detect dimension
		AutoDimAdapt:   StrictMode,                     // Strict by default
		SimilarityFn:   CosineSimilarity,               // Cosine similarity
		IndexType:      IndexTypeHNSW,                  // Default to HNSW
		HNSW:           DefaultHNSWConfig(),            // HNSW configuration
		IVF:            DefaultIVFConfig(),             // IVF configuration
		TextSimilarity: DefaultTextSimilarityConfig(),  // Text similarity configuration
		Quantization:   DefaultQuantizationConfig(),    // Quantization configuration
	}
}

// Store defines the core interface for vector storage operations.
// It provides a high-level API for managing embeddings, documents, chat history, and collections.
type Store interface {
	// Init initializes the store, creates necessary tables, and builds/loads indexes.
	// It must be called before any other operation.
	Init(ctx context.Context) error

	// Upsert inserts or updates a single embedding.
	// If the vector dimension doesn't match the store's dimension, it applies the adaptation policy.
	Upsert(ctx context.Context, emb *Embedding) error

	// UpsertBatch inserts or updates multiple embeddings in a single database transaction.
	// This is significantly faster than calling Upsert multiple times.
	UpsertBatch(ctx context.Context, embs []*Embedding) error

	// Search performs a vector similarity search.
	// It uses the configured index (HNSW or IVF) if available, otherwise falls back to linear search.
	Search(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error)

	// RangeSearch finds all vectors within a specified distance (radius) from the query.
	RangeSearch(ctx context.Context, query []float32, radius float32, opts SearchOptions) ([]ScoredEmbedding, error)

	// Delete removes an embedding by its unique ID.
	Delete(ctx context.Context, id string) error

	// DeleteByDocID removes all embeddings associated with a specific document ID.
	DeleteByDocID(ctx context.Context, docID string) error

	// DeleteBatch removes multiple embeddings by their IDs in a single operation.
	DeleteBatch(ctx context.Context, ids []string) error

	// DeleteByFilter removes embeddings matching the given metadata filter criteria.
	DeleteByFilter(ctx context.Context, filter *MetadataFilter) error

	// Close closes the store, releases database connections, and persists memory indexes.
	Close() error

	// Stats returns global statistics about the vector store (count, dimensions, size).
	Stats(ctx context.Context) (StoreStats, error)

	// CreateCollection creates a new named collection for multi-tenant isolation.
	CreateCollection(ctx context.Context, name string, dimensions int) (*Collection, error)
	// GetCollection retrieves collection information by name.
	GetCollection(ctx context.Context, name string) (*Collection, error)
	// ListCollections lists all available collections.
	ListCollections(ctx context.Context) ([]*Collection, error)
	// DeleteCollection deletes a collection and all its associated data.
	DeleteCollection(ctx context.Context, name string) error
	// GetCollectionStats returns statistics for a specific collection.
	GetCollectionStats(ctx context.Context, name string) (*CollectionStats, error)

	// TrainIndex learns cluster centroids for IVF indexes from existing data.
	TrainIndex(ctx context.Context, numCentroids int) error
	// TrainQuantizer learns value ranges for scalar quantization from existing data.
	TrainQuantizer(ctx context.Context) error

	// CreateDocument creates a document record for source tracking and versioning.
	CreateDocument(ctx context.Context, doc *Document) error
	// GetDocument retrieves a document record by its ID.
	GetDocument(ctx context.Context, id string) (*Document, error)
	// DeleteDocument deletes a document and all its linked embeddings (cascading).
	DeleteDocument(ctx context.Context, id string) error
	// ListDocumentsWithFilter lists documents matching specific criteria like author.
	ListDocumentsWithFilter(ctx context.Context, author string, limit int) ([]*Document, error)

	// CreateSession starts a new conversation thread for chat memory.
	CreateSession(ctx context.Context, session *Session) error
	// GetSession retrieves a chat session by its ID.
	GetSession(ctx context.Context, id string) (*Session, error)
	// AddMessage appends a new message (user or assistant) to a session.
	AddMessage(ctx context.Context, msg *Message) error
	// GetSessionHistory returns the chronological message history for a session.
	GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]*Message, error)
	// SearchChatHistory performs semantic search over previous messages in a session.
	SearchChatHistory(ctx context.Context, queryVec []float32, sessionID string, limit int) ([]*Message, error)

	// SearchWithACL performs vector search while enforcing access control rules.
	SearchWithACL(ctx context.Context, query []float32, acl []string, opts SearchOptions) ([]ScoredEmbedding, error)
	// HybridSearch combines vector similarity with FTS5 keyword matching using RRF fusion.
	HybridSearch(ctx context.Context, vectorQuery []float32, textQuery string, opts HybridSearchOptions) ([]ScoredEmbedding, error)
	// SearchWithAdvancedFilter performs vector search with complex boolean and range metadata filters.
	SearchWithAdvancedFilter(ctx context.Context, query []float32, opts AdvancedSearchOptions) ([]ScoredEmbedding, error)
}
