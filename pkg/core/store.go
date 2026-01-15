package core

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/liliang-cn/sqvect/v2/pkg/index"

	_ "modernc.org/sqlite" // SQLite driver
)

// SQLiteStore implements the Store interface using SQLite as backend
type SQLiteStore struct {
	db             *sql.DB
	config         Config
	mu             sync.RWMutex
	closed         bool
	similarityFn   SimilarityFunc
	hnswIndex      *index.HNSW            // HNSW index for fast search
	ivfIndex       *index.IVFIndex        // IVF index for partitioned search
	quantizer      index.Quantizer        // Vector quantizer
	adapter        *DimensionAdapter      // Dimension adaptation handler
	textSimilarity TextSimilarity         // Text similarity calculator
	logger         Logger                 // Logger instance
}

// New creates a new SQLite vector store with the given configuration
func New(path string, vectorDim int) (*SQLiteStore, error) {
	config := DefaultConfig()
	config.Path = path
	config.VectorDim = vectorDim

	return NewWithConfig(config)
}

// NewWithConfig creates a new SQLite vector store with custom configuration
func NewWithConfig(config Config) (*SQLiteStore, error) {
	if config.Path == "" {
		return nil, wrapError("init", fmt.Errorf("database path cannot be empty"))
	}

	// Allow VectorDim = 0 for auto-detection
	if config.VectorDim < 0 {
		return nil, wrapError("init", fmt.Errorf("vector dimension must be non-negative"))
	}

	if config.SimilarityFn == nil {
		config.SimilarityFn = CosineSimilarity
	}

	// Use nop logger by default, can be replaced with SetLogger
	logger := NopLogger()
	if config.Logger != nil {
		logger = config.Logger
	}

	store := &SQLiteStore{
		config:       config,
		similarityFn: config.SimilarityFn,
		adapter:      NewDimensionAdapter(config.AutoDimAdapt),
		logger:       logger,
	}

	store.logger = logger.With("component", "store")

	return store, nil
}

// SetLogger sets the logger for the store
func (s *SQLiteStore) SetLogger(logger Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger.With("component", "store")
}

// SetAutoDimAdapt sets the dimension adaptation policy
func (s *SQLiteStore) SetAutoDimAdapt(policy AdaptPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.AutoDimAdapt = policy
}

// GetDB returns the underlying database connection
func (s *SQLiteStore) GetDB() *sql.DB {
	return s.db
}

// GetSimilarityFunc returns the similarity function
func (s *SQLiteStore) GetSimilarityFunc() SimilarityFunc {
	return s.similarityFn
}

// Stats returns statistics about the store
func (s *SQLiteStore) Stats(ctx context.Context) (StoreStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return StoreStats{}, wrapError("stats", ErrStoreClosed)
	}

	var count int64
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM embeddings").Scan(&count)
	if err != nil {
		return StoreStats{}, wrapError("stats", fmt.Errorf("failed to get count: %w", err))
	}

	// Get database file size (approximate)
	var size int64
	err = s.db.QueryRowContext(ctx, "SELECT page_count * page_size as size FROM pragma_page_count(), pragma_page_size()").Scan(&size)
	if err != nil {
		size = 0 // Continue without size info
	}

	return StoreStats{
		Count:      count,
		Dimensions: s.config.VectorDim,
		Size:       size,
	}, nil
}
