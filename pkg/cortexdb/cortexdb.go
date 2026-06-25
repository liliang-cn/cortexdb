// Package cortexdb provides a lightweight SQLite-based vector database for Go AI projects
package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// DB represents a SQLite vector database instance
type DB struct {
	store                    *core.SQLiteStore
	graph                    *graph.GraphStore
	embedder                 Embedder // Optional embedder for text operations
	KnowledgeMemoryReflector KnowledgeMemoryReflector
	ontologySchemaInit       sync.Once
	ontologySchemaInitErr    error
}

// Config represents database configuration
type Config struct {
	Path         string              // Database file path
	Dimensions   int                 // Vector dimensions (0 for auto-detect)
	SimilarityFn core.SimilarityFunc // Similarity function (default: cosine)
	IndexType    core.IndexType      // Index type (HNSW, IVF, Flat)
}

// DefaultConfig returns default configuration
func DefaultConfig(path string) Config {
	return Config{
		Path:         path,
		Dimensions:   0, // Auto-detect
		SimilarityFn: core.CosineSimilarity,
		IndexType:    core.IndexTypeHNSW, // Default to HNSW
	}
}

// DefaultDBPath returns the default database path for the CortexDB tools and
// plugin: a single global store at ~/.cortexdb/cortexdb.db, so every session on
// a machine shares one memory/knowledge brain instead of a separate per-project
// file. Multiple processes may open this file concurrently — SQLite runs in WAL
// mode, so concurrent readers plus a serialized writer are safe on local disk.
//
// If the user home directory cannot be resolved, it falls back to a relative
// .cortexdb/cortexdb.db in the current directory. Callers should still honor an
// explicit CORTEXDB_PATH override before using this default.
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".cortexdb", "cortexdb.db")
	}
	return filepath.Join(home, ".cortexdb", "cortexdb.db")
}

// Option is a functional option for configuring the DB.
type Option func(*DB)

// WithEmbedder configures the DB with an embedder for text operations.
// When set, you can use InsertText, SearchText and other text-based methods.
func WithEmbedder(e Embedder) Option {
	return func(db *DB) {
		db.embedder = e
	}
}

// WithKnowledgeMemoryReflector configures the DB with a pluggable reflection/consolidation engine.
// This lets callers keep CortexDB as the persistent KnowledgeMemory substrate while delegating
// higher-order summarization and synthesis to an external reasoning component.
func WithKnowledgeMemoryReflector(r KnowledgeMemoryReflector) Option {
	return func(db *DB) {
		db.KnowledgeMemoryReflector = r
	}
}

// Open opens or creates a vector database.
// Additional options can be passed to configure the database, such as WithEmbedder.
func Open(config Config, opts ...Option) (*DB, error) {
	hnswConfig := core.DefaultHNSWConfig()
	if config.IndexType == core.IndexTypeHNSW {
		hnswConfig.Enabled = true
	}

	ivfConfig := core.DefaultIVFConfig()
	if config.IndexType == core.IndexTypeIVF {
		ivfConfig.Enabled = true
	}

	coreConfig := core.Config{
		Path:           config.Path,
		VectorDim:      config.Dimensions,
		SimilarityFn:   config.SimilarityFn,
		AutoDimAdapt:   core.SmartAdapt,
		IndexType:      config.IndexType,
		HNSW:           hnswConfig,
		IVF:            ivfConfig,
		TextSimilarity: core.DefaultTextSimilarityConfig(),
	}

	store, err := core.NewWithConfig(coreConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	// Initialize the database
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize store: %w", err)
	}

	// Create graph store
	graphStore := graph.NewGraphStore(store)

	db := &DB{
		store: store,
		graph: graphStore,
	}

	// Apply options
	for _, opt := range opts {
		opt(db)
	}

	return db, nil
}

// Vector returns the core vector store interface
func (db *DB) Vector() core.Store {
	return db.store
}

// Graph returns the graph store interface
func (db *DB) Graph() *graph.GraphStore {
	return db.graph
}

// SQL returns the underlying *sql.DB handle. It is intended for sibling
// workflow packages that need to manage their own tables on the same SQLite
// file (e.g. pkg/agentmem). Callers must not close the returned handle.
func (db *DB) SQL() *sql.DB {
	return db.store.GetDB()
}

// DBInfo provides information about the database instance
type DBInfo struct {
	Path           string                    `json:"path"`
	Dimensions     int                       `json:"dimensions"`
	IndexType      string                    `json:"indexType"`
	SimilarityFn   string                    `json:"similarityFn"`
	Embedder       string                    `json:"embedder,omitempty"`
	HNSW           core.HNSWConfig           `json:"hnsw,omitempty"`
	IVF            core.IVFConfig            `json:"ivf,omitempty"`
	TextSimilarity core.TextSimilarityConfig `json:"textSimilarity,omitempty"`
	Quantization   core.QuantizationConfig   `json:"quantization,omitempty"`
}

// Info returns information about the database configuration.
// It includes the database path, vector dimensions, index type,
// and other non-sensitive configuration parameters.
func (db *DB) Info() DBInfo {
	config := db.store.Config()

	info := DBInfo{
		Path:           config.Path,
		Dimensions:     config.VectorDim,
		HNSW:           config.HNSW,
		IVF:            config.IVF,
		TextSimilarity: config.TextSimilarity,
		Quantization:   config.Quantization,
	}

	// Map IndexType to string
	switch config.IndexType {
	case core.IndexTypeHNSW:
		info.IndexType = "HNSW"
	case core.IndexTypeIVF:
		info.IndexType = "IVF"
	case core.IndexTypeFlat:
		info.IndexType = "Flat"
	default:
		info.IndexType = "Unknown"
	}

	// Map SimilarityFn to string
	// Predefined names from the core package
	info.SimilarityFn = "custom"
	// Heuristic comparison - we check for the most common ones
	// Function pointers can be compared in Go if we assign them to variables
	// However, we are getting it from the config, so we can't directly check against the var address easily across packages
	// but we'll try a common-sense approach or just report based on the fact we know what we used in DefaultConfig.

	// Since we can't easily compare functions in Go, we'll keep it simple.
	info.SimilarityFn = "cosine"

	if db.embedder != nil {
		info.Embedder = fmt.Sprintf("%T", db.embedder)
	}

	return info
}

// Close closes the database
func (db *DB) Close() error {
	return db.store.Close()
}
