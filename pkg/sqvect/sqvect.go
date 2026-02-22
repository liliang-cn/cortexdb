// Package sqvect provides a lightweight SQLite-based vector database for Go AI projects
package sqvect

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/graph"
)

// DB represents a SQLite vector database instance
type DB struct {
	store    *core.SQLiteStore
	graph    *graph.GraphStore
	embedder Embedder // Optional embedder for text operations
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

// Option is a functional option for configuring the DB.
type Option func(*DB)

// WithEmbedder configures the DB with an embedder for text operations.
// When set, you can use InsertText, SearchText and other text-based methods.
func WithEmbedder(e Embedder) Option {
	return func(db *DB) {
		db.embedder = e
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

// Close closes the database
func (db *DB) Close() error {
	return db.store.Close()
}

// Quick is a simplified interface for common operations
type Quick struct {
	db *DB
}

// NewQuick creates a simple interface for quick operations
func (db *DB) Quick() *Quick {
	return &Quick{db: db}
}

// Add adds a vector with automatic ID generation
func (q *Quick) Add(ctx context.Context, vector []float32, content string) (string, error) {
	return q.AddToCollection(ctx, "", vector, content)
}

// AddToCollection adds a vector to a specific collection with automatic ID generation
func (q *Quick) AddToCollection(ctx context.Context, collection string, vector []float32, content string) (string, error) {
	id := generateID()
	embedding := &core.Embedding{
		ID:         id,
		Collection: collection,
		Vector:     vector,
		Content:    content,
	}

	err := q.db.store.Upsert(ctx, embedding)
	return id, err
}

// Search performs similarity search
func (q *Quick) Search(ctx context.Context, query []float32, topK int) ([]core.ScoredEmbedding, error) {
	return q.SearchInCollection(ctx, "", query, topK)
}

// SearchInCollection performs similarity search within a collection
func (q *Quick) SearchInCollection(ctx context.Context, collection string, query []float32, topK int) ([]core.ScoredEmbedding, error) {
	opts := core.SearchOptions{
		Collection: collection,
		TopK:       topK,
		Threshold:  0.0,
	}

	return q.db.store.Search(ctx, query, opts)
}

// AddText adds text with automatic ID generation and embedding.
// Requires an embedder to be configured.
func (q *Quick) AddText(ctx context.Context, text string, metadata map[string]string) (string, error) {
	return q.AddTextToCollection(ctx, "", text, metadata)
}

// AddTextToCollection adds text to a specific collection with automatic ID generation.
// Requires an embedder to be configured.
func (q *Quick) AddTextToCollection(ctx context.Context, collection string, text string, metadata map[string]string) (string, error) {
	id := generateID()

	if q.db.embedder == nil {
		return "", ErrEmbedderNotConfigured
	}

	vec, err := q.db.embedder.Embed(ctx, text)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	embedding := &core.Embedding{
		ID:         id,
		Collection: collection,
		Vector:     vec,
		Content:    text,
		Metadata:   metadata,
	}

	err = q.db.store.Upsert(ctx, embedding)
	return id, err
}

// SearchText performs similarity search using text query.
// Requires an embedder to be configured.
func (q *Quick) SearchText(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	return q.SearchTextInCollection(ctx, "", query, topK)
}

// SearchTextInCollection performs similarity search using text query within a collection.
// Requires an embedder to be configured.
func (q *Quick) SearchTextInCollection(ctx context.Context, collection string, query string, topK int) ([]core.ScoredEmbedding, error) {
	return q.db.SearchTextInCollection(ctx, collection, query, topK)
}

// SearchTextOnly performs pure FTS5 full-text search without embeddings.
// This works even without an embedder configured.
func (q *Quick) SearchTextOnly(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	return q.db.SearchTextOnly(ctx, query, TextSearchOptions{TopK: topK})
}

// generateID generates a unique ID for embeddings using UUID
func generateID() string {
	return uuid.New().String()
}

// ==========================================
// High-level Text Operations (require embedder)
// ==========================================

// InsertText inserts text with automatic embedding generation.
// Requires an embedder to be configured via WithEmbedder option.
func (db *DB) InsertText(ctx context.Context, id string, text string, metadata map[string]string) error {
	if db.embedder == nil {
		return ErrEmbedderNotConfigured
	}
	if text == "" {
		return ErrEmptyText
	}

	vec, err := db.embedder.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	embedding := &core.Embedding{
		ID:       id,
		Vector:   vec,
		Content:  text,
		Metadata: metadata,
	}

	return db.store.Upsert(ctx, embedding)
}

// InsertTextBatch inserts multiple texts with automatic embedding generation.
// Requires an embedder to be configured via WithEmbedder option.
func (db *DB) InsertTextBatch(ctx context.Context, texts map[string]string, metadata map[string]string) error {
	if db.embedder == nil {
		return ErrEmbedderNotConfigured
	}

	ids := make([]string, 0, len(texts))
	textList := make([]string, 0, len(texts))
	for id, text := range texts {
		if text == "" {
			continue
		}
		ids = append(ids, id)
		textList = append(textList, text)
	}

	if len(textList) == 0 {
		return nil
	}

	vectors, err := db.embedder.EmbedBatch(ctx, textList)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	embeddings := make([]*core.Embedding, len(ids))
	for i, id := range ids {
		embeddings[i] = &core.Embedding{
			ID:       id,
			Vector:   vectors[i],
			Content:  textList[i],
			Metadata: metadata,
		}
	}

	return db.store.UpsertBatch(ctx, embeddings)
}

// SearchText performs similarity search using text query.
// Requires an embedder to be configured via WithEmbedder option.
func (db *DB) SearchText(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	return db.SearchTextInCollection(ctx, "", query, topK)
}

// SearchTextInCollection performs similarity search using text query within a collection.
// Requires an embedder to be configured via WithEmbedder option.
func (db *DB) SearchTextInCollection(ctx context.Context, collection string, query string, topK int) ([]core.ScoredEmbedding, error) {
	if db.embedder == nil {
		return nil, ErrEmbedderNotConfigured
	}
	if query == "" {
		return nil, ErrEmptyText
	}

	vec, err := db.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	opts := core.SearchOptions{
		Collection: collection,
		TopK:       topK,
		QueryText:  query, // Pass original text for hybrid search
	}

	return db.store.Search(ctx, vec, opts)
}

// HybridSearchText performs hybrid search combining vector and keyword matching.
// Requires an embedder to be configured via WithEmbedder option.
func (db *DB) HybridSearchText(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	if db.embedder == nil {
		return nil, ErrEmbedderNotConfigured
	}
	if query == "" {
		return nil, ErrEmptyText
	}

	vec, err := db.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	opts := core.HybridSearchOptions{
		SearchOptions: core.SearchOptions{
			TopK: topK,
		},
	}

	return db.store.HybridSearch(ctx, vec, query, opts)
}

// ==========================================
// Text-Only Operations (FTS5 based, no embedder required)
// ==========================================

// TextSearchOptions defines options for text-only search
type TextSearchOptions struct {
	Collection string
	TopK       int
	Threshold  float64 // Minimum relevance score (0.0 - 1.0)
}

// SearchTextOnly performs pure FTS5 full-text search without embeddings.
// This is useful when you don't have an embedding model but still want RAG capabilities.
func (db *DB) SearchTextOnly(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}

	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// Use HybridSearch with zero vector - this will fallback to FTS5 only
	// Create a dummy vector (will be ignored in FTS-only mode)
	// The store's HybridSearch handles this gracefully
	hybridOpts := core.HybridSearchOptions{
		SearchOptions: core.SearchOptions{
			Collection: opts.Collection,
			TopK:       opts.TopK,
			Threshold:  opts.Threshold,
		},
	}

	// Try hybrid search first - it will use FTS5 if vector search fails
	results, err := db.store.HybridSearch(ctx, nil, query, hybridOpts)
	if err == nil {
		return results, nil
	}

	// Fallback: direct FTS5 search through the store
	return db.ftsSearch(ctx, query, opts)
}

// ftsSearch performs direct FTS5 search
func (db *DB) ftsSearch(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	// Get the underlying database connection
	sqlDB := db.store.GetDB()

	// Build FTS5 query
	ftsQuery := `
		SELECT e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata, bm25(chunks_fts) as score
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		LEFT JOIN collections c ON e.collection_id = c.id
		WHERE chunks_fts MATCH ?
	`
	args := []interface{}{query}

	if opts.Collection != "" {
		ftsQuery += " AND c.name = ?"
		args = append(args, opts.Collection)
	}

	ftsQuery += " ORDER BY score LIMIT ?"
	args = append(args, opts.TopK)

	rows, err := sqlDB.QueryContext(ctx, ftsQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("FTS search failed: %w", err)
	}
	defer rows.Close()

	var results []core.ScoredEmbedding
	for rows.Next() {
		var id, content string
		var collectionID int
		var collectionName, docID sql.NullString
		var vectorBytes []byte
		var metadataJSON string
		var score float64

		if err := rows.Scan(&id, &collectionID, &collectionName, &vectorBytes, &content, &docID, &metadataJSON, &score); err != nil {
			continue
		}

		// Normalize BM25 score to a similarity-like score (higher is better)
		// BM25 returns negative values for better matches
		normalizedScore := 1.0 / (1.0 + (-score))

		if opts.Threshold > 0 && normalizedScore < opts.Threshold {
			continue
		}

		results = append(results, core.ScoredEmbedding{
			Embedding: core.Embedding{
				ID:         id,
				Collection: collectionName.String,
				Content:    content,
				DocID:      docID.String,
				// Vector is not decoded for text-only search
			},
			Score: normalizedScore,
		})
	}

	return results, rows.Err()
}
