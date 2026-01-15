package core

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/liliang-cn/sqvect/v2/internal/encoding"
)

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

	// Use IVF index if available and enabled
	if s.config.IndexType == IndexTypeIVF && s.ivfIndex != nil && s.ivfIndex.Trained {
		return s.searchWithIVF(ctx, query, opts)
	}

	// Fallback to linear search
	candidates, err := s.fetchCandidates(ctx, opts)
	if err != nil {
		return nil, wrapError("search", err)
	}

	results := s.scoreCandidates(query, candidates, opts)
	return results, nil
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
	} else if s.config.IndexType == IndexTypeIVF && s.ivfIndex != nil && s.ivfIndex.Trained {
		// Use IVF index
		candidates, err = s.searchWithIVF(ctx, query, opts)
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

// searchWithHNSW performs vector search using HNSW index
func (s *SQLiteStore) searchWithHNSW(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// Search HNSW index for nearest neighbors
	candidateIDs, _ := s.hnswIndex.Search(
		query,
		opts.TopK*2, // Get more candidates to account for filtering
		s.config.HNSW.EfSearch,
	)

	if len(candidateIDs) == 0 {
		// If no candidates found from HNSW, fallback to linear search
		return s.searchLinear(ctx, query, opts)
	}

	// Fetch full embedding data from database for the candidate IDs
	candidates, err := s.fetchEmbeddingsByIDs(ctx, candidateIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch candidates: %w", err)
	}

	return s.processCandidates(query, candidates, opts)
}

// searchWithIVF performs vector search using IVF index
func (s *SQLiteStore) searchWithIVF(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// Search IVF index
	// Fetch 4x candidates to allow for filtering
	candidateIDs, _, err := s.ivfIndex.Search(query, opts.TopK*4)
	if err != nil {
		s.logger.Warn("IVF search failed, falling back to linear search", "error", err)
		return s.searchLinear(ctx, query, opts)
	}

	if len(candidateIDs) == 0 {
		return s.searchLinear(ctx, query, opts)
	}

	// Fetch full embeddings
	candidates, err := s.fetchEmbeddingsByIDs(ctx, candidateIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch candidates: %w", err)
	}

	return s.processCandidates(query, candidates, opts)
}

// searchLinear performs linear vector search without HNSW index
func (s *SQLiteStore) searchLinear(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	candidates, err := s.fetchCandidates(ctx, opts)
	if err != nil {
		return nil, err
	}

	results := s.scoreCandidates(query, candidates, opts)
	return results, nil
}

// processCandidates applies scoring and filtering to candidates
func (s *SQLiteStore) processCandidates(query []float32, candidates []ScoredEmbedding, opts SearchOptions) ([]ScoredEmbedding, error) {
	textWeight := s.getTextWeight(opts)
	vectorWeight := 1.0 - textWeight

	var results []ScoredEmbedding
	for _, candidate := range candidates {
		// Apply collection filter
		if opts.Collection != "" && candidate.Collection != opts.Collection {
			continue
		}

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
		"SELECT e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata "+
			"FROM embeddings e "+
			"LEFT JOIN collections c ON e.collection_id = c.id "+
			"WHERE e.id IN (%s)",
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query embeddings by IDs: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows during fetch embeddings by IDs", "error", closeErr)
		}
	}()

	var candidates []ScoredEmbedding
	for rows.Next() {
		candidate, err := s.scanEmbedding(rows)
		if err != nil {
			s.logger.Warn("failed to scan embedding during fetch by IDs", "error", err)
			continue // Skip invalid embeddings
		}
		candidates = append(candidates, candidate)
	}

	return candidates, rows.Err()
}

// validateSearchInput validates search input parameters
func (s *SQLiteStore) validateSearchInput(query []float32, opts SearchOptions) error {
	if err := encoding.ValidateVector(query); err != nil {
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
	querySQL := "SELECT e.id, e.collection_id, c.name as collection_name, e.vector, e.content, e.doc_id, e.metadata FROM embeddings e LEFT JOIN collections c ON e.collection_id = c.id"
	args := []interface{}{}

	var conditions []string

	// Filter by collection if specified
	if opts.Collection != "" {
		conditions = append(conditions, "collection_id = (SELECT id FROM collections WHERE name = ?)")
		args = append(args, opts.Collection)
	}

	// Handle other filters
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
			s.logger.Warn("failed to close rows during fetch candidates", "error", closeErr)
		}
	}()

	var candidates []ScoredEmbedding

	for rows.Next() {
		candidate, err := s.scanEmbedding(rows)
		if err != nil {
			s.logger.Warn("failed to scan embedding during fetch candidates", "error", err)
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
	var id, content, metadataJSON string
	var docID sql.NullString
	var collectionName sql.NullString
	var collectionID int
	var vectorBytes []byte

	if err := rows.Scan(&id, &collectionID, &collectionName, &vectorBytes, &content, &docID, &metadataJSON); err != nil {
		return ScoredEmbedding{}, fmt.Errorf("failed to scan row: %w", err)
	}

	vector, err := encoding.DecodeVector(vectorBytes)
	if err != nil {
		return ScoredEmbedding{}, fmt.Errorf("failed to decode vector: %w", err)
	}

	metadata, err := encoding.DecodeMetadata(metadataJSON)
	if err != nil {
		metadata = nil // Continue with nil metadata
	}

	var collection string
	if collectionName.Valid {
		collection = collectionName.String
	}

	return ScoredEmbedding{
		Embedding: Embedding{
			ID:         id,
			Collection: collection,
			Vector:     vector,
			Content:    content,
			DocID:      docID.String, // Will be empty if invalid
			Metadata:   metadata,
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

// sortByScore sorts embeddings by score in descending order
func (s *SQLiteStore) sortByScore(candidates []ScoredEmbedding) {
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
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
			if parsedFloat, err := fmt.Sscanf(expectedStr, "%f", &actualFloat); err == nil && parsedFloat == 1 {
				return true
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
			if parsedInt, err := fmt.Sscanf(expectedStr, "%d", &actualInt); err == nil && parsedInt == 1 {
				return true
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
			if parsedBool, err := fmt.Sscanf(expectedStr, "%t", &actualBool); err == nil && parsedBool == 1 {
				return true
			}
		}
	}

	// Fallback to direct comparison
	return actual == expected
}
