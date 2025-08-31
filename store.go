package sqvect

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fogfish/hnsw"
	"github.com/fogfish/hnsw/vector"
	surface "github.com/kshard/vector"
	_ "modernc.org/sqlite" // SQLite driver
)

// SQLiteStore implements the Store interface using SQLite as backend
type SQLiteStore struct {
	db             *sql.DB
	config         Config
	mu             sync.RWMutex
	closed         bool
	similarityFn   SimilarityFunc
	hnswIndex      *hnsw.HNSW[vector.VF32] // HNSW index for fast search
	idToKey        map[string]uint32       // Maps string IDs to uint32 keys
	keyToID        map[uint32]string       // Maps uint32 keys to string IDs
	nextKey        uint32                  // Next available key
	adapter        *DimensionAdapter       // Dimension adaptation handler
	textSimilarity *TextSimilarity         // Text similarity calculator
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

	store := &SQLiteStore{
		config:       config,
		similarityFn: config.SimilarityFn,
		idToKey:      make(map[string]uint32),
		keyToID:      make(map[uint32]string),
		nextKey:      1,
		adapter:      NewDimensionAdapter(config.AutoDimAdapt),
	}
	
	// Initialize text similarity if enabled
	if config.TextSimilarity.Enabled {
		store.textSimilarity = NewTextSimilarity()
	}

	return store, nil
}

// getOrCreateKey gets or creates a uint32 key for a string ID
func (s *SQLiteStore) getOrCreateKey(id string) uint32 {
	if key, exists := s.idToKey[id]; exists {
		return key
	}
	key := s.nextKey
	s.nextKey++
	s.idToKey[id] = key
	s.keyToID[key] = id
	return key
}

// getIDFromKey gets string ID from uint32 key
func (s *SQLiteStore) getIDFromKey(key uint32) (string, bool) {
	id, exists := s.keyToID[key]
	return id, exists
}

// Init initializes the SQLite database and creates necessary tables
func (s *SQLiteStore) Init(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return wrapError("init", ErrStoreClosed)
	}

	// Open database connection
	db, err := sql.Open("sqlite", s.config.Path+"?_journal_mode=WAL&_synchronous=NORMAL&_cache_size=10000")
	if err != nil {
		return wrapError("init", fmt.Errorf("failed to open database: %w", err))
	}

	// Configure connection pool with sensible defaults
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	s.db = db

	// Create tables
	if err := s.createTables(ctx); err != nil {
		return wrapError("init", err)
	}

	// Initialize HNSW index if enabled
	if err := s.initHNSWIndex(ctx); err != nil {
		return wrapError("init", err)
	}

	return nil
}

// createTables creates the necessary database tables
func (s *SQLiteStore) createTables(ctx context.Context) error {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS embeddings (
		id TEXT PRIMARY KEY,
		vector BLOB NOT NULL,
		content TEXT NOT NULL,
		doc_id TEXT,
		metadata TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE INDEX IF NOT EXISTS idx_embeddings_doc_id ON embeddings(doc_id);
	CREATE INDEX IF NOT EXISTS idx_embeddings_created_at ON embeddings(created_at);
	`

	_, err := s.db.ExecContext(ctx, createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	return nil
}

// initHNSWIndex initializes the HNSW index if enabled in configuration
func (s *SQLiteStore) initHNSWIndex(ctx context.Context) error {
	if !s.config.HNSW.Enabled {
		return nil
	}

	// Create HNSW index with cosine similarity (default)
	s.hnswIndex = hnsw.New(
		vector.SurfaceVF32(surface.Cosine()),
		hnsw.WithM(s.config.HNSW.M),
		hnsw.WithEfConstruction(s.config.HNSW.EfConstruction),
	)

	// Load existing vectors into HNSW index
	return s.rebuildHNSWIndex(ctx)
}

// rebuildHNSWIndex rebuilds the HNSW index from existing vectors in the database
func (s *SQLiteStore) rebuildHNSWIndex(ctx context.Context) error {
	if s.hnswIndex == nil {
		return nil
	}

	// Query all vectors from database
	rows, err := s.db.QueryContext(ctx, "SELECT id, vector FROM embeddings")
	if err != nil {
		return fmt.Errorf("failed to query existing vectors: %w", err)
	}
	defer rows.Close()

	// Insert each vector into HNSW index
	for rows.Next() {
		var id string
		var vectorBytes []byte
		
		if err := rows.Scan(&id, &vectorBytes); err != nil {
			continue // Skip invalid entries
		}

		vec, err := decodeVector(vectorBytes)
		if err != nil {
			continue // Skip invalid vectors
		}

		// Get or create key for this ID and insert into HNSW index
		key := s.getOrCreateKey(id)
		s.hnswIndex.Insert(vector.VF32{
			Key: key,
			Vec: vec,
		})
	}

	return rows.Err()
}

// Upsert inserts or updates a single embedding
func (s *SQLiteStore) Upsert(ctx context.Context, emb *Embedding) error {
	s.mu.RLock()
	currentDim := s.config.VectorDim
	s.mu.RUnlock()

	if s.closed {
		return wrapError("upsert", ErrStoreClosed)
	}

	incomingDim := len(emb.Vector)

	// Auto-detect dimension on first insert
	if currentDim == 0 {
		s.mu.Lock()
		if s.config.VectorDim == 0 { // Double-check after acquiring write lock
			s.config.VectorDim = incomingDim
			currentDim = incomingDim
		} else {
			currentDim = s.config.VectorDim
		}
		s.mu.Unlock()
	}

	// Handle dimension mismatch
	if incomingDim != currentDim {
		adaptedVector, err := s.adapter.AdaptVector(emb.Vector, incomingDim, currentDim)
		if err != nil {
			return wrapError("upsert", err)
		}
		s.adapter.logDimensionEvent("adapt", incomingDim, currentDim, emb.ID)
		emb.Vector = adaptedVector
	}

	// Validate adapted embedding
	if err := validateEmbedding(*emb, currentDim); err != nil {
		return wrapError("upsert", err)
	}

	// Re-acquire read lock for database operations
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Encode vector and metadata
	vectorBytes, err := encodeVector(emb.Vector)
	if err != nil {
		return wrapError("upsert", err)
	}

	metadataJSON, err := encodeMetadata(emb.Metadata)
	if err != nil {
		return wrapError("upsert", err)
	}

	// Insert or replace
	query := `
	INSERT OR REPLACE INTO embeddings (id, vector, content, doc_id, metadata, created_at)
	VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`

	_, err = s.db.ExecContext(ctx, query, emb.ID, vectorBytes, emb.Content, emb.DocID, metadataJSON)
	if err != nil {
		return wrapError("upsert", fmt.Errorf("failed to insert embedding: %w", err))
	}

	// Update HNSW index if enabled
	if s.config.HNSW.Enabled && s.hnswIndex != nil {
		key := s.getOrCreateKey(emb.ID)
		s.hnswIndex.Insert(vector.VF32{
			Key: key,
			Vec: emb.Vector,
		})
	}

	return nil
}

// UpsertBatch inserts or updates multiple embeddings in a transaction
func (s *SQLiteStore) UpsertBatch(ctx context.Context, embs []*Embedding) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("upsert_batch", ErrStoreClosed)
	}

	if len(embs) == 0 {
		return nil
	}

	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError("upsert_batch", fmt.Errorf("failed to begin transaction: %w", err))
	}
	defer func() {
		if rollErr := tx.Rollback(); rollErr != nil {
			// Rollback failed, but we're already returning an error
			// Log could be added here if needed
			_ = rollErr // Explicitly ignore error to satisfy staticcheck
		}
	}()

	// Prepare statement
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO embeddings (id, vector, content, doc_id, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		return wrapError("upsert_batch", fmt.Errorf("failed to prepare statement: %w", err))
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil {
			// Statement close failed, but we're likely in an error path already
			// Log could be added here if needed
			_ = closeErr // Explicitly ignore error to satisfy staticcheck
		}
	}()

	// Execute for each embedding
	for i, emb := range embs {
		if err := validateEmbedding(*emb, s.config.VectorDim); err != nil {
			return wrapError("upsert_batch", fmt.Errorf("invalid embedding at index %d: %w", i, err))
		}

		vectorBytes, err := encodeVector(emb.Vector)
		if err != nil {
			return wrapError("upsert_batch", fmt.Errorf("failed to encode vector at index %d: %w", i, err))
		}

		metadataJSON, err := encodeMetadata(emb.Metadata)
		if err != nil {
			return wrapError("upsert_batch", fmt.Errorf("failed to encode metadata at index %d: %w", i, err))
		}

		_, err = stmt.ExecContext(ctx, emb.ID, vectorBytes, emb.Content, emb.DocID, metadataJSON)
		if err != nil {
			return wrapError("upsert_batch", fmt.Errorf("failed to insert embedding at index %d: %w", i, err))
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return wrapError("upsert_batch", fmt.Errorf("failed to commit transaction: %w", err))
	}

	// Update HNSW index if enabled
	if s.config.HNSW.Enabled && s.hnswIndex != nil {
		for _, emb := range embs {
			key := s.getOrCreateKey(emb.ID)
			s.hnswIndex.Insert(vector.VF32{
				Key: key,
				Vec: emb.Vector,
			})
		}
	}

	return nil
}

// Search performs vector similarity search
func (s *SQLiteStore) Search(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	storeDim := s.config.VectorDim
	s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("search", ErrStoreClosed)
	}

	queryDim := len(query)

	// Auto-adapt query vector if dimensions don't match
	if storeDim > 0 && queryDim != storeDim {
		adaptedQuery, err := s.adapter.AdaptVector(query, queryDim, storeDim)
		if err != nil {
			return nil, wrapError("search", fmt.Errorf("query adaptation failed: %w", err))
		}
		s.adapter.logDimensionEvent("search_adapt", queryDim, storeDim, "query_vector")
		query = adaptedQuery
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.validateSearchInput(query, opts); err != nil {
		return nil, wrapError("search", err)
	}

	// Use HNSW index if available and enabled
	if s.config.HNSW.Enabled && s.hnswIndex != nil {
		return s.searchWithHNSW(ctx, query, opts)
	}

	// Fallback to linear search
	candidates, err := s.fetchCandidates(ctx, opts)
	if err != nil {
		return nil, wrapError("search", err)
	}

	results := s.scoreCandidates(query, candidates, opts)
	return results, nil
}

// searchWithHNSW performs vector search using HNSW index
func (s *SQLiteStore) searchWithHNSW(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// Search HNSW index for nearest neighbors
	neighbors := s.hnswIndex.Search(
		vector.VF32{Key: 0, Vec: query}, // Key doesn't matter for search query
		opts.TopK*2, // Get more candidates to account for filtering
		s.config.HNSW.EfSearch,
	)

	var candidateIDs []string
	for _, neighbor := range neighbors {
		if id, exists := s.getIDFromKey(neighbor.Key); exists {
			candidateIDs = append(candidateIDs, id)
		}
	}

	if len(candidateIDs) == 0 {
		return []ScoredEmbedding{}, nil
	}

	// Fetch full embedding data from database for the candidate IDs
	candidates, err := s.fetchEmbeddingsByIDs(ctx, candidateIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch candidates: %w", err)
	}

	// Apply filters and calculate final scores using hybrid approach
	textWeight := s.getTextWeight(opts)
	vectorWeight := 1.0 - textWeight
	
	var results []ScoredEmbedding
	for _, candidate := range candidates {
		// Apply metadata filters
		if !s.matchesFilter(candidate.Embedding, opts.Filter) {
			continue
		}

		// Calculate vector similarity score
		vectorScore := s.similarityFn(query, candidate.Vector)
		
		// Calculate text similarity score (if enabled and query text provided)
		textScore := 0.0
		if s.textSimilarity != nil && opts.QueryText != "" {
			textScore = s.textSimilarity.CalculateSimilarity(opts.QueryText, candidate.Content)
		}
		
		// Combine scores
		finalScore := vectorScore
		if textWeight > 0 && textScore > 0 {
			finalScore = vectorScore*vectorWeight + textScore*textWeight
		}
		
		// Apply threshold filter
		if opts.Threshold > 0 && finalScore < opts.Threshold {
			continue
		}

		results = append(results, ScoredEmbedding{
			Embedding: candidate.Embedding,
			Score:     finalScore,
		})
	}

	// Sort by score (descending)
	s.sortByScore(results)

	// Return top-k results
	if len(results) > opts.TopK {
		results = results[:opts.TopK]
	}

	return results, nil
}

// fetchEmbeddingsByIDs fetches embeddings by their IDs
func (s *SQLiteStore) fetchEmbeddingsByIDs(ctx context.Context, ids []string) ([]ScoredEmbedding, error) {
	if len(ids) == 0 {
		return []ScoredEmbedding{}, nil
	}

	// Build IN clause for SQL query
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		"SELECT id, vector, content, doc_id, metadata FROM embeddings WHERE id IN (%s)",
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query embeddings by IDs: %w", err)
	}
	defer rows.Close()

	var candidates []ScoredEmbedding
	for rows.Next() {
		candidate, err := s.scanEmbedding(rows)
		if err != nil {
			continue // Skip invalid embeddings
		}
		candidates = append(candidates, candidate)
	}

	return candidates, rows.Err()
}

// validateSearchInput validates search input parameters
func (s *SQLiteStore) validateSearchInput(query []float32, opts SearchOptions) error {
	if err := validateVector(query); err != nil {
		return fmt.Errorf("invalid query vector: %w", err)
	}

	// Skip dimension check in auto-detect mode when database is empty
	if s.config.VectorDim == 0 {
		return nil
	}

	if len(query) != s.config.VectorDim {
		return fmt.Errorf("query vector dimension mismatch: expected %d, got %d",
			s.config.VectorDim, len(query))
	}

	return nil
}

// buildSearchQuery builds SQL query with filtering
func (s *SQLiteStore) buildSearchQuery(opts SearchOptions) (string, []interface{}) {
	querySQL := "SELECT id, vector, content, doc_id, metadata FROM embeddings"
	args := []interface{}{}

	if len(opts.Filter) == 0 {
		return querySQL, args
	}

	var conditions []string
	for key, value := range opts.Filter {
		if key == "doc_id" {
			conditions = append(conditions, "doc_id = ?")
			args = append(args, value)
		}
		// Note: Non-doc_id metadata filtering is done post-query
	}

	if len(conditions) > 0 {
		querySQL += " WHERE " + conditions[0]
		for i := 1; i < len(conditions); i++ {
			querySQL += " AND " + conditions[i]
		}
	}

	return querySQL, args
}

// fetchCandidates retrieves candidate embeddings from database
func (s *SQLiteStore) fetchCandidates(ctx context.Context, opts SearchOptions) ([]ScoredEmbedding, error) {
	querySQL, args := s.buildSearchQuery(opts)

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query embeddings: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			// Rows close failed, but we're likely in an error path already
			// Log could be added here if needed
			_ = closeErr // Explicitly ignore error to satisfy staticcheck
		}
	}()

	var candidates []ScoredEmbedding

	for rows.Next() {
		candidate, err := s.scanEmbedding(rows)
		if err != nil {
			continue // Skip invalid embeddings
		}

		if s.matchesFilter(candidate.Embedding, opts.Filter) {
			candidates = append(candidates, candidate)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return candidates, nil
}

// scanEmbedding scans a row into an embedding
func (s *SQLiteStore) scanEmbedding(rows *sql.Rows) (ScoredEmbedding, error) {
	var id, content, docID, metadataJSON string
	var vectorBytes []byte

	if err := rows.Scan(&id, &vectorBytes, &content, &docID, &metadataJSON); err != nil {
		return ScoredEmbedding{}, fmt.Errorf("failed to scan row: %w", err)
	}

	vector, err := decodeVector(vectorBytes)
	if err != nil {
		return ScoredEmbedding{}, fmt.Errorf("failed to decode vector: %w", err)
	}

	metadata, err := decodeMetadata(metadataJSON)
	if err != nil {
		metadata = nil // Continue with nil metadata
	}

	return ScoredEmbedding{
		Embedding: Embedding{
			ID:       id,
			Vector:   vector,
			Content:  content,
			DocID:    docID,
			Metadata: metadata,
		},
		Score: 0, // Will be set later
	}, nil
}

// matchesFilter checks if embedding matches the filter criteria
func (s *SQLiteStore) matchesFilter(emb Embedding, filter map[string]string) bool {
	for key, value := range filter {
		if key == "doc_id" {
			continue // Already filtered in SQL
		}
		if emb.Metadata == nil || emb.Metadata[key] != value {
			return false
		}
	}
	return true
}

// scoreCandidates scores and sorts candidate embeddings
func (s *SQLiteStore) scoreCandidates(query []float32, candidates []ScoredEmbedding, opts SearchOptions) []ScoredEmbedding {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// Calculate similarity scores - hybrid approach
	textWeight := s.getTextWeight(opts)
	vectorWeight := 1.0 - textWeight
	
	for i := range candidates {
		// Vector similarity score
		vectorScore := s.similarityFn(query, candidates[i].Vector)
		
		// Text similarity score (if enabled and query text provided)
		textScore := 0.0
		if s.textSimilarity != nil && opts.QueryText != "" {
			textScore = s.textSimilarity.CalculateSimilarity(opts.QueryText, candidates[i].Content)
		}
		
		// Combine scores
		if textWeight > 0 && textScore > 0 {
			candidates[i].Score = vectorScore*vectorWeight + textScore*textWeight
		} else {
			candidates[i].Score = vectorScore // Fall back to vector-only scoring
		}
	}

	// Filter by threshold
	if opts.Threshold > 0 {
		filtered := candidates[:0]
		for _, candidate := range candidates {
			if candidate.Score >= opts.Threshold {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}

	// Sort by score (descending)
	s.sortByScore(candidates)

	// Return top-k results
	if len(candidates) > opts.TopK {
		candidates = candidates[:opts.TopK]
	}

	return candidates
}

// getTextWeight determines the text similarity weight from options or config
func (s *SQLiteStore) getTextWeight(opts SearchOptions) float64 {
	// Use weight from SearchOptions if provided
	if opts.TextWeight > 0 {
		return math.Min(opts.TextWeight, 1.0) // Clamp to [0, 1]
	}
	
	// Fall back to config default weight
	if s.textSimilarity != nil && s.config.TextSimilarity.Enabled {
		return s.config.TextSimilarity.DefaultWeight
	}
	
	return 0.0 // No text similarity
}

// SearchWithFilter performs vector similarity search with advanced metadata filtering
func (s *SQLiteStore) SearchWithFilter(ctx context.Context, query []float32, opts SearchOptions, metadataFilters map[string]interface{}) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	storeDim := s.config.VectorDim
	s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("searchWithFilter", ErrStoreClosed)
	}

	queryDim := len(query)

	// Auto-adapt query vector if dimensions don't match
	if storeDim > 0 && queryDim != storeDim {
		adaptedQuery, err := s.adapter.AdaptVector(query, queryDim, storeDim)
		if err != nil {
			return nil, wrapError("searchWithFilter", fmt.Errorf("query adaptation failed: %w", err))
		}
		s.adapter.logDimensionEvent("search_adapt", queryDim, storeDim, "query_vector")
		query = adaptedQuery
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.validateSearchInput(query, opts); err != nil {
		return nil, wrapError("searchWithFilter", err)
	}

	// First perform the standard search
	var candidates []ScoredEmbedding
	var err error

	// Use HNSW index if available and enabled
	if s.config.HNSW.Enabled && s.hnswIndex != nil {
		candidates, err = s.searchWithHNSW(ctx, query, opts)
	} else {
		// Fallback to linear search
		candidates, err = s.fetchCandidates(ctx, opts)
		if err != nil {
			return nil, wrapError("searchWithFilter", err)
		}
		candidates = s.scoreCandidates(query, candidates, opts)
	}

	if err != nil {
		return nil, wrapError("searchWithFilter", err)
	}

	// Apply advanced metadata filtering
	if len(metadataFilters) > 0 {
		filtered, err := s.filterByMetadata(candidates, metadataFilters)
		if err != nil {
			return nil, wrapError("searchWithFilter", err)
		}
		candidates = filtered
	}

	return candidates, nil
}

// filterByMetadata filters candidates based on metadata criteria
func (s *SQLiteStore) filterByMetadata(candidates []ScoredEmbedding, filters map[string]interface{}) ([]ScoredEmbedding, error) {
	if len(filters) == 0 {
		return candidates, nil
	}

	var filtered []ScoredEmbedding
	for _, candidate := range candidates {
		if candidate.Metadata == nil {
			continue
		}

		match := true
		for key, expectedValue := range filters {
			actualValue, exists := candidate.Metadata[key]
			if !exists {
				match = false
				break
			}

			// Type-safe comparison
			if !s.compareMetadataValues(actualValue, expectedValue) {
				match = false
				break
			}
		}

		if match {
			filtered = append(filtered, candidate)
		}
	}

	return filtered, nil
}

// compareMetadataValues compares two metadata values with type checking
func (s *SQLiteStore) compareMetadataValues(actual, expected interface{}) bool {
	if actual == nil && expected == nil {
		return true
	}
	if actual == nil || expected == nil {
		return false
	}

	// Handle string comparison (the primary case since metadata is stored as map[string]string)
	if actualStr, ok := actual.(string); ok {
		// Compare with another string
		if expectedStr, ok := expected.(string); ok {
			return actualStr == expectedStr
		}
		
		// Handle numeric comparisons by converting expected value to string
		if expectedInt, ok := expected.(int); ok {
			return actualStr == fmt.Sprintf("%d", expectedInt)
		}
		
		if expectedFloat, ok := expected.(float64); ok {
			return actualStr == fmt.Sprintf("%g", expectedFloat)
		}
		
		// Handle boolean comparison by converting expected value to string
		if expectedBool, ok := expected.(bool); ok {
			return actualStr == fmt.Sprintf("%t", expectedBool)
		}
	}

	// Handle numeric comparisons when actual is numeric
	if actualFloat, ok := actual.(float64); ok {
		if expectedFloat, ok := expected.(float64); ok {
			return actualFloat == expectedFloat
		}
		if expectedInt, ok := expected.(int); ok {
			return actualFloat == float64(expectedInt)
		}
		// Try to parse expected string as float
		if expectedStr, ok := expected.(string); ok {
			if parsedFloat, err := strconv.ParseFloat(expectedStr, 64); err == nil {
				return actualFloat == parsedFloat
			}
		}
	}

	if actualInt, ok := actual.(int); ok {
		if expectedInt, ok := expected.(int); ok {
			return actualInt == expectedInt
		}
		if expectedFloat, ok := expected.(float64); ok {
			return float64(actualInt) == expectedFloat
		}
		// Try to parse expected string as int
		if expectedStr, ok := expected.(string); ok {
			if parsedInt, err := strconv.Atoi(expectedStr); err == nil {
				return actualInt == parsedInt
			}
		}
	}

	// Handle boolean comparison when actual is boolean
	if actualBool, ok := actual.(bool); ok {
		if expectedBool, ok := expected.(bool); ok {
			return actualBool == expectedBool
		}
		// Try to parse expected string as bool
		if expectedStr, ok := expected.(string); ok {
			if parsedBool, err := strconv.ParseBool(expectedStr); err == nil {
				return actualBool == parsedBool
			}
		}
	}

	// Fallback to direct comparison
	return actual == expected
}

// sortByScore sorts embeddings by score in descending order
func (s *SQLiteStore) sortByScore(candidates []ScoredEmbedding) {
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
}

// Delete removes an embedding by ID
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("delete", ErrStoreClosed)
	}

	if id == "" {
		return wrapError("delete", fmt.Errorf("ID cannot be empty"))
	}

	result, err := s.db.ExecContext(ctx, "DELETE FROM embeddings WHERE id = ?", id)
	if err != nil {
		return wrapError("delete", fmt.Errorf("failed to delete embedding: %w", err))
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return wrapError("delete", fmt.Errorf("failed to get rows affected: %w", err))
	}

	if rowsAffected == 0 {
		return wrapError("delete", ErrNotFound)
	}

	return nil
}

// DeleteByDocID removes all embeddings for a document
func (s *SQLiteStore) DeleteByDocID(ctx context.Context, docID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("delete_by_doc_id", ErrStoreClosed)
	}

	if docID == "" {
		return wrapError("delete_by_doc_id", fmt.Errorf("doc ID cannot be empty"))
	}

	_, err := s.db.ExecContext(ctx, "DELETE FROM embeddings WHERE doc_id = ?", docID)
	if err != nil {
		return wrapError("delete_by_doc_id", fmt.Errorf("failed to delete embeddings: %w", err))
	}

	return nil
}

// ListDocuments returns all unique document IDs in the store
func (s *SQLiteStore) ListDocuments(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("list_documents", ErrStoreClosed)
	}

	query := "SELECT DISTINCT doc_id FROM embeddings WHERE doc_id IS NOT NULL AND doc_id != '' ORDER BY doc_id"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, wrapError("list_documents", fmt.Errorf("failed to query documents: %w", err))
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr // Explicitly ignore error to satisfy staticcheck
		}
	}()

	var docIDs []string
	for rows.Next() {
		var docID string
		if err := rows.Scan(&docID); err != nil {
			return nil, wrapError("list_documents", fmt.Errorf("failed to scan doc_id: %w", err))
		}
		docIDs = append(docIDs, docID)
	}

	if err := rows.Err(); err != nil {
		return nil, wrapError("list_documents", fmt.Errorf("error iterating rows: %w", err))
	}

	return docIDs, nil
}

// GetByDocID returns all embeddings for a specific document ID
func (s *SQLiteStore) GetByDocID(ctx context.Context, docID string) ([]*Embedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("get_by_doc_id", ErrStoreClosed)
	}

	if docID == "" {
		return nil, wrapError("get_by_doc_id", fmt.Errorf("doc ID cannot be empty"))
	}

	query := "SELECT id, vector, content, doc_id, metadata FROM embeddings WHERE doc_id = ? ORDER BY created_at"
	rows, err := s.db.QueryContext(ctx, query, docID)
	if err != nil {
		return nil, wrapError("get_by_doc_id", fmt.Errorf("failed to query embeddings: %w", err))
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr // Explicitly ignore error to satisfy staticcheck
		}
	}()

	var embeddings []*Embedding
	for rows.Next() {
		embedding, err := s.scanEmbeddingForGet(rows)
		if err != nil {
			continue // Skip invalid embeddings
		}
		embeddings = append(embeddings, embedding)
	}

	if err := rows.Err(); err != nil {
		return nil, wrapError("get_by_doc_id", fmt.Errorf("error iterating rows: %w", err))
	}

	return embeddings, nil
}

// scanEmbeddingForGet scans a row into an embedding for Get methods
func (s *SQLiteStore) scanEmbeddingForGet(rows *sql.Rows) (*Embedding, error) {
	var id, content, docID, metadataJSON string
	var vectorBytes []byte

	if err := rows.Scan(&id, &vectorBytes, &content, &docID, &metadataJSON); err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	vector, err := decodeVector(vectorBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decode vector: %w", err)
	}

	metadata, err := decodeMetadata(metadataJSON)
	if err != nil {
		metadata = nil // Continue with nil metadata
	}

	return &Embedding{
		ID:       id,
		Vector:   vector,
		Content:  content,
		DocID:    docID,
		Metadata: metadata,
	}, nil
}

// GetDocumentsByType returns documents filtered by metadata type
func (s *SQLiteStore) GetDocumentsByType(ctx context.Context, docType string) ([]*Embedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("get_documents_by_type", ErrStoreClosed)
	}

	if docType == "" {
		return nil, wrapError("get_documents_by_type", fmt.Errorf("doc type cannot be empty"))
	}

	query := "SELECT id, vector, content, doc_id, metadata FROM embeddings ORDER BY created_at"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, wrapError("get_documents_by_type", fmt.Errorf("failed to query embeddings: %w", err))
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr // Explicitly ignore error to satisfy staticcheck
		}
	}()

	var embeddings []*Embedding
	for rows.Next() {
		embedding, err := s.scanEmbeddingForGet(rows)
		if err != nil {
			continue // Skip invalid embeddings
		}

		// Filter by type in metadata
		if embedding.Metadata != nil && embedding.Metadata["type"] == docType {
			embeddings = append(embeddings, embedding)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, wrapError("get_documents_by_type", fmt.Errorf("error iterating rows: %w", err))
	}

	return embeddings, nil
}

// Clear removes all embeddings from the store
func (s *SQLiteStore) Clear(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("clear", ErrStoreClosed)
	}

	_, err := s.db.ExecContext(ctx, "DELETE FROM embeddings")
	if err != nil {
		return wrapError("clear", fmt.Errorf("failed to clear embeddings: %w", err))
	}

	return nil
}

// ClearByDocID removes all embeddings for specific document IDs
func (s *SQLiteStore) ClearByDocID(ctx context.Context, docIDs []string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("clear_by_doc_id", ErrStoreClosed)
	}

	if len(docIDs) == 0 {
		return nil // Nothing to clear
	}

	// Start transaction for batch deletion
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError("clear_by_doc_id", fmt.Errorf("failed to begin transaction: %w", err))
	}
	defer func() {
		if rollErr := tx.Rollback(); rollErr != nil {
			_ = rollErr // Explicitly ignore error to satisfy staticcheck
		}
	}()

	// Prepare statement for deletion
	stmt, err := tx.PrepareContext(ctx, "DELETE FROM embeddings WHERE doc_id = ?")
	if err != nil {
		return wrapError("clear_by_doc_id", fmt.Errorf("failed to prepare statement: %w", err))
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil {
			_ = closeErr // Explicitly ignore error to satisfy staticcheck
		}
	}()

	// Execute deletion for each doc ID
	for _, docID := range docIDs {
		if docID == "" {
			continue // Skip empty doc IDs
		}
		_, err = stmt.ExecContext(ctx, docID)
		if err != nil {
			return wrapError("clear_by_doc_id", fmt.Errorf("failed to delete embeddings for doc_id %s: %w", docID, err))
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return wrapError("clear_by_doc_id", fmt.Errorf("failed to commit transaction: %w", err))
	}

	return nil
}

// ListDocumentsWithInfo returns detailed information about documents
func (s *SQLiteStore) ListDocumentsWithInfo(ctx context.Context) ([]DocumentInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("list_documents_with_info", ErrStoreClosed)
	}

	query := `
	SELECT 
		doc_id,
		COUNT(*) as embedding_count,
		MIN(created_at) as first_created,
		MAX(created_at) as last_updated
	FROM embeddings 
	WHERE doc_id IS NOT NULL AND doc_id != '' 
	GROUP BY doc_id 
	ORDER BY doc_id
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, wrapError("list_documents_with_info", fmt.Errorf("failed to query document info: %w", err))
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr // Explicitly ignore error to satisfy staticcheck
		}
	}()

	var documents []DocumentInfo
	for rows.Next() {
		var docID, firstCreated, lastUpdated string
		var embeddingCount int

		if err := rows.Scan(&docID, &embeddingCount, &firstCreated, &lastUpdated); err != nil {
			return nil, wrapError("list_documents_with_info", fmt.Errorf("failed to scan row: %w", err))
		}

		docInfo := DocumentInfo{
			DocID:          docID,
			EmbeddingCount: embeddingCount,
			FirstCreated:   &firstCreated,
			LastUpdated:    &lastUpdated,
		}

		documents = append(documents, docInfo)
	}

	if err := rows.Err(); err != nil {
		return nil, wrapError("list_documents_with_info", fmt.Errorf("error iterating rows: %w", err))
	}

	return documents, nil
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

// Close closes the database connection and releases resources
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	if s.db != nil {
		return s.db.Close()
	}

	return nil
}
