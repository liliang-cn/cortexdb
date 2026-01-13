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

// Store defines the core interface for vector storage operations
type Store interface {
	// Init initializes the store and creates necessary tables
	Init(ctx context.Context) error

	// Upsert inserts or updates a single embedding
	Upsert(ctx context.Context, emb *Embedding) error

	// UpsertBatch inserts or updates multiple embeddings in a batch
	UpsertBatch(ctx context.Context, embs []*Embedding) error

	// Search performs vector similarity search
	Search(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error)

	// RangeSearch finds all vectors within a specified distance from the query
	RangeSearch(ctx context.Context, query []float32, radius float32, opts SearchOptions) ([]ScoredEmbedding, error)

	// Delete removes an embedding by ID
	Delete(ctx context.Context, id string) error

	// DeleteByDocID removes all embeddings for a document
	DeleteByDocID(ctx context.Context, docID string) error

	// Close closes the store and releases resources
	Close() error

	// Stats returns statistics about the store
	Stats(ctx context.Context) (StoreStats, error)

	// Collection operations
	CreateCollection(ctx context.Context, name string, dimensions int) (*Collection, error)
	GetCollection(ctx context.Context, name string) (*Collection, error)
	ListCollections(ctx context.Context) ([]*Collection, error)
	DeleteCollection(ctx context.Context, name string) error
	GetCollectionStats(ctx context.Context, name string) (*CollectionStats, error)

	// Index operations
	TrainIndex(ctx context.Context, numCentroids int) error
}
