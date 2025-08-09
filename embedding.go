package sqvect

import "context"

// Embedding represents a vector embedding with associated metadata
type Embedding struct {
	ID       string            `json:"id"`
	Vector   []float32         `json:"vector"`
	Content  string            `json:"content"`
	DocID    string            `json:"docId,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ScoredEmbedding represents an embedding with similarity score
type ScoredEmbedding struct {
	Embedding
	Score float64 `json:"score"`
}

// SearchOptions defines options for vector search
type SearchOptions struct {
	TopK      int               `json:"topK"`
	Filter    map[string]string `json:"filter,omitempty"`
	Threshold float64           `json:"threshold,omitempty"`
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

// Config represents configuration options for the vector store
type Config struct {
	Path         string         `json:"path"`
	VectorDim    int            `json:"vectorDim"`
	MaxConns     int            `json:"maxConns,omitempty"`
	BatchSize    int            `json:"batchSize,omitempty"`
	SimilarityFn SimilarityFunc `json:"-"`
	HNSW         HNSWConfig     `json:"hnsw,omitempty"`
}

// DefaultConfig returns a default configuration
func DefaultConfig() Config {
	return Config{
		MaxConns:     10,
		BatchSize:    100,
		SimilarityFn: CosineSimilarity,
		HNSW:         DefaultHNSWConfig(),
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

	// Delete removes an embedding by ID
	Delete(ctx context.Context, id string) error

	// DeleteByDocID removes all embeddings for a document
	DeleteByDocID(ctx context.Context, docID string) error

	// Close closes the store and releases resources
	Close() error

	// Stats returns statistics about the store
	Stats(ctx context.Context) (StoreStats, error)
}
