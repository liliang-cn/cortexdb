// Package core provides faceted search capabilities
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// FacetedSearchOptions extends SearchOptions with faceted filtering
type FacetedSearchOptions struct {
	SearchOptions
	
	// Facets to filter by
	Facets map[string]FacetFilter
	
	// Whether to return facet counts
	ReturnFacets bool
	
	// Maximum number of facet values to return
	MaxFacetValues int
}

// FacetFilter defines filtering for a specific facet
type FacetFilter struct {
	// Type of filter
	Type FacetFilterType
	
	// Values for equality/inclusion filters
	Values []interface{}
	
	// Range for numeric filters
	Min interface{}
	Max interface{}
	
	// Pattern for text filters
	Pattern string
	
	// Nested filters for complex conditions
	Nested []FacetFilter
	
	// Logical operator for nested filters
	Operator LogicalOperator
}

// FacetFilterType defines the type of facet filter
type FacetFilterType string

const (
	FilterTypeEquals   FacetFilterType = "equals"
	FilterTypeIn       FacetFilterType = "in"
	FilterTypeRange    FacetFilterType = "range"
	FilterTypeContains FacetFilterType = "contains"
	FilterTypePrefix   FacetFilterType = "prefix"
	FilterTypeExists   FacetFilterType = "exists"
	FilterTypeNested   FacetFilterType = "nested"
)

// LogicalOperator for combining filters
type LogicalOperator string

const (
	OperatorAND LogicalOperator = "AND"
	OperatorOR  LogicalOperator = "OR"
	OperatorNOT LogicalOperator = "NOT"
)

// FacetResult contains facet counts
type FacetResult struct {
	Field  string
	Values map[string]int
	Total  int
}

// SearchWithFacets performs vector search with faceted filtering
func (s *SQLiteStore) SearchWithFacets(ctx context.Context, query []float32, opts FacetedSearchOptions) ([]ScoredEmbedding, []FacetResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, nil, wrapError("search_faceted", ErrStoreClosed)
	}
	
	// Build faceted query
	whereClause, args := s.buildFacetedWhereClause(opts.Facets)
	
	// Get candidates with facet filtering
	candidates, err := s.fetchCandidatesWithFacets(ctx, whereClause, args, opts)
	if err != nil {
		return nil, nil, wrapError("search_faceted", err)
	}
	
	// Score and sort candidates
	results := s.scoreCandidates(query, candidates, opts.SearchOptions)
	
	// Get facet counts if requested
	var facetResults []FacetResult
	if opts.ReturnFacets {
		facetResults, err = s.computeFacetCounts(ctx, opts)
		if err != nil {
			// Don't fail search if facet counting fails
			facetResults = []FacetResult{}
		}
	}
	
	return results, facetResults, nil
}

// buildFacetedWhereClause builds SQL WHERE clause from facet filters
func (s *SQLiteStore) buildFacetedWhereClause(facets map[string]FacetFilter) (string, []interface{}) {
	if len(facets) == 0 {
		return "", nil
	}
	
	var conditions []string
	var args []interface{}
	
	for field, filter := range facets {
		condition, filterArgs := s.buildFilterCondition(field, filter)
		if condition != "" {
			conditions = append(conditions, condition)
			args = append(args, filterArgs...)
		}
	}
	
	if len(conditions) == 0 {
		return "", nil
	}
	
	return strings.Join(conditions, " AND "), args
}

// buildFilterCondition builds SQL condition for a single filter
func (s *SQLiteStore) buildFilterCondition(field string, filter FacetFilter) (string, []interface{}) {
	switch filter.Type {
	case FilterTypeEquals:
		if len(filter.Values) > 0 {
			return fmt.Sprintf("json_extract(metadata, '$.%s') = ?", field), filter.Values[:1]
		}
		
	case FilterTypeIn:
		if len(filter.Values) > 0 {
			placeholders := make([]string, len(filter.Values))
			for i := range placeholders {
				placeholders[i] = "?"
			}
			return fmt.Sprintf("json_extract(metadata, '$.%s') IN (%s)", field, strings.Join(placeholders, ",")), filter.Values
		}
		
	case FilterTypeRange:
		conditions := []string{}
		args := []interface{}{}
		
		if filter.Min != nil {
			conditions = append(conditions, fmt.Sprintf("CAST(json_extract(metadata, '$.%s') AS REAL) >= ?", field))
			args = append(args, filter.Min)
		}
		if filter.Max != nil {
			conditions = append(conditions, fmt.Sprintf("CAST(json_extract(metadata, '$.%s') AS REAL) <= ?", field))
			args = append(args, filter.Max)
		}
		
		if len(conditions) > 0 {
			return strings.Join(conditions, " AND "), args
		}
		
	case FilterTypeContains:
		if filter.Pattern != "" {
			return fmt.Sprintf("json_extract(metadata, '$.%s') LIKE ?", field), []interface{}{"%" + filter.Pattern + "%"}
		}
		
	case FilterTypePrefix:
		if filter.Pattern != "" {
			return fmt.Sprintf("json_extract(metadata, '$.%s') LIKE ?", field), []interface{}{filter.Pattern + "%"}
		}
		
	case FilterTypeExists:
		return fmt.Sprintf("json_extract(metadata, '$.%s') IS NOT NULL", field), nil
		
	case FilterTypeNested:
		return s.buildNestedCondition(field, filter)
	}
	
	return "", nil
}

// buildNestedCondition builds SQL for nested filter conditions
func (s *SQLiteStore) buildNestedCondition(field string, filter FacetFilter) (string, []interface{}) {
	if len(filter.Nested) == 0 {
		return "", nil
	}
	
	var conditions []string
	var args []interface{}
	
	for _, nested := range filter.Nested {
		condition, nestedArgs := s.buildFilterCondition(field, nested)
		if condition != "" {
			conditions = append(conditions, "("+condition+")")
			args = append(args, nestedArgs...)
		}
	}
	
	if len(conditions) == 0 {
		return "", nil
	}
	
	var operator string
	switch filter.Operator {
	case OperatorOR:
		operator = " OR "
	case OperatorNOT:
		return "NOT (" + strings.Join(conditions, " AND ") + ")", args
	default:
		operator = " AND "
	}
	
	return strings.Join(conditions, operator), args
}

// fetchCandidatesWithFacets fetches candidates with facet filtering
func (s *SQLiteStore) fetchCandidatesWithFacets(ctx context.Context, whereClause string, args []interface{}, opts FacetedSearchOptions) ([]ScoredEmbedding, error) {
	query := `
		SELECT e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata
		FROM embeddings e
		LEFT JOIN collections c ON e.collection_id = c.id
	`
	
	conditions := []string{}
	
	// Add facet conditions
	if whereClause != "" {
		conditions = append(conditions, whereClause)
	}
	
	// Add collection filter
	if opts.Collection != "" {
		conditions = append(conditions, "c.name = ?")
		args = append(args, opts.Collection)
	}
	
	// Add metadata filter
	if opts.Filter != nil {
		for key, value := range opts.Filter {
			conditions = append(conditions, fmt.Sprintf("json_extract(metadata, '$.%s') = ?", key))
			args = append(args, value)
		}
	}
	
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	
	// Add limit for performance
	if opts.TopK > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.TopK*10) // Get more candidates for scoring
	}
	
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query with facets: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			// Log error but don't override the main error
			_ = err
		}
	}()
	
	var candidates []ScoredEmbedding
	for rows.Next() {
		candidate, err := s.scanEmbedding(rows)
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate)
	}
	
	return candidates, rows.Err()
}

// computeFacetCounts computes facet value counts
func (s *SQLiteStore) computeFacetCounts(ctx context.Context, opts FacetedSearchOptions) ([]FacetResult, error) {
	results := []FacetResult{}
	
	// For each facet field, count distinct values
	for field := range opts.Facets {
		query := fmt.Sprintf(`
			SELECT 
				json_extract(metadata, '$.%s') as value,
				COUNT(*) as count
			FROM embeddings
			WHERE json_extract(metadata, '$.%s') IS NOT NULL
			GROUP BY value
			ORDER BY count DESC
			LIMIT ?
		`, field, field)
		
		limit := opts.MaxFacetValues
		if limit <= 0 {
			limit = 10
		}
		
		rows, err := s.db.QueryContext(ctx, query, limit)
		if err != nil {
			continue
		}
		
		facetResult := FacetResult{
			Field:  field,
			Values: make(map[string]int),
		}
		
		for rows.Next() {
			var value string
			var count int
			if err := rows.Scan(&value, &count); err == nil {
				facetResult.Values[value] = count
				facetResult.Total += count
			}
		}
		if err := rows.Close(); err != nil {
			// Log error but don't override the main error
			_ = err
		}
		
		if len(facetResult.Values) > 0 {
			results = append(results, facetResult)
		}
	}
	
	return results, nil
}

// RangeSearch performs range-based vector search
func (s *SQLiteStore) RangeSearch(ctx context.Context, query []float32, radius float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, wrapError("range_search", ErrStoreClosed)
	}
	
	// Get all candidates (no topK limit for range search)
	candidates, err := s.fetchCandidates(ctx, opts)
	if err != nil {
		return nil, wrapError("range_search", err)
	}
	
	// Filter by radius
	var results []ScoredEmbedding
	for _, candidate := range candidates {
		score := s.similarityFn(query, candidate.Vector)
		
		// For range search, we consider score as distance
		// Lower score = closer distance for most metrics
		distance := float32(1.0 - score)
		
		if distance <= radius {
			candidate.Score = score
			results = append(results, candidate)
		}
	}
	
	// Sort by score (descending - higher score is better)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	
	// Apply TopK limit if specified
	if opts.TopK > 0 && len(results) > opts.TopK {
		results = results[:opts.TopK]
	}
	
	return results, nil
}

// BatchRangeSearch performs range search for multiple queries
func (s *SQLiteStore) BatchRangeSearch(ctx context.Context, queries [][]float32, radius float32, opts SearchOptions) ([][]ScoredEmbedding, error) {
	results := make([][]ScoredEmbedding, len(queries))
	
	for i, query := range queries {
		res, err := s.RangeSearch(ctx, query, radius, opts)
		if err != nil {
			return nil, err
		}
		results[i] = res
	}
	
	return results, nil
}

