// Package core provides advanced search capabilities
package core

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/liliang-cn/sqvect/v2/internal/encoding"
)

// HybridSearchOptions for combined vector + keyword search
type HybridSearchOptions struct {
	SearchOptions
	// Fusion parameter for RRF (default 60)
	RRFK float64
}

// SearchWithACL performs vector search with access control filtering
func (s *SQLiteStore) SearchWithACL(ctx context.Context, query []float32, acl []string, opts SearchOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("search_acl", ErrStoreClosed)
	}

	// If ACL is empty, only return public documents (acl IS NULL)
	// If ACL is provided, return public OR matching acl
	
	whereClause := "json_extract(acl, '$') IS NULL" // Public
	params := []interface{}{}

	if len(acl) > 0 {
		placeholders := make([]string, len(acl))
		for i, id := range acl {
			placeholders[i] = "?"
			params = append(params, id)
		}
		// Check if any provided ACL ID exists in the acl JSON array
		whereClause += fmt.Sprintf(" OR EXISTS (SELECT 1 FROM json_each(acl) WHERE value IN (%s))", strings.Join(placeholders, ","))
	}

	// Fetch candidates with ACL filter
	candidates, err := s.fetchCandidatesWithSQL(ctx, whereClause, params, opts)
	if err != nil {
		return nil, err
	}

	// Score candidates
	return s.scoreAndSort(query, candidates, opts)
}

// HybridSearch performs combined vector and keyword search using RRF fusion
func (s *SQLiteStore) HybridSearch(ctx context.Context, vectorQuery []float32, textQuery string, opts HybridSearchOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("hybrid_search", ErrStoreClosed)
	}

	// 1. Vector Search (HNSW or Linear)
	var vectorResults []ScoredEmbedding
	var err error
	if len(vectorQuery) > 0 {
		vectorResults, err = s.Search(ctx, vectorQuery, opts.SearchOptions)
		if err != nil {
			return nil, fmt.Errorf("vector search failed: %w", err)
		}
	}

	// 2. Keyword Search (FTS5)
	ftsRanks := make(map[int64]int)
	if textQuery != "" {
		ftsQuery := `
			SELECT rowid, rank 
			FROM chunks_fts 
			WHERE chunks_fts MATCH ? 
			ORDER BY rank 
			LIMIT ?
		`
		// Fetch more than topK to have a better pool for fusion
		limit := opts.TopK * 3
		if limit <= 0 {
			limit = 30
		}
		
		rows, err := s.db.QueryContext(ctx, ftsQuery, textQuery, limit)
		if err == nil {
			defer rows.Close()
			rank := 1
			for rows.Next() {
				var rowid int64
				var score float64
				if err := rows.Scan(&rowid, &score); err == nil {
					ftsRanks[rowid] = rank
					rank++
				}
			}
		}
	}

	// 3. Reciprocal Rank Fusion (RRF)
	k := opts.RRFK
	if k == 0 {
		k = 60
	}

	// Map to store combined scores
	fusedScores := make(map[string]float64)
	// Map to store full embedding data for results
	embeddingsMap := make(map[string]ScoredEmbedding)

	// Process Vector Ranks
	for i, res := range vectorResults {
		score := 1.0 / (k + float64(i+1))
		fusedScores[res.ID] = score
		embeddingsMap[res.ID] = res
	}

	// Process FTS Ranks
	// First, we need to map FTS rowids back to IDs
	if len(ftsRanks) > 0 {
		rowids := make([]int64, 0, len(ftsRanks))
		for rid := range ftsRanks {
			rowids = append(rowids, rid)
		}

		placeholders := make([]string, len(rowids))
		args := make([]interface{}, len(rowids))
		for i, rid := range rowids {
			placeholders[i] = "?"
			args[i] = rid
		}

		query := fmt.Sprintf(
			"SELECT e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata, e.rowid "+
				"FROM embeddings e "+
				"LEFT JOIN collections c ON e.collection_id = c.id "+
				"WHERE e.rowid IN (%s)",
			strings.Join(placeholders, ","),
		)

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id, content, metadataJSON string
				var docID sql.NullString
				var collectionName sql.NullString
				var collectionID int
				var vectorBytes []byte
				var rowid int64

				if err := rows.Scan(&id, &collectionID, &collectionName, &vectorBytes, &content, &docID, &metadataJSON, &rowid); err != nil {
					continue
				}

				// Calculate FTS score contribution
				if rank, ok := ftsRanks[rowid]; ok {
					score := 1.0 / (k + float64(rank))
					fusedScores[id] += score

					// If not already in map (from vector search), add it
					if _, exists := embeddingsMap[id]; !exists {
						vec, _ := encoding.DecodeVector(vectorBytes)
						meta, _ := encoding.DecodeMetadata(metadataJSON)
						
						embeddingsMap[id] = ScoredEmbedding{
							Embedding: Embedding{
								ID:         id,
								Collection: collectionName.String,
								Vector:     vec,
								Content:    content,
								DocID:      docID.String,
								Metadata:   meta,
							},
						}
					}
				}
			}
		}
	}

	// Final results construction
	var results []ScoredEmbedding
	for id, score := range fusedScores {
		res := embeddingsMap[id]
		res.Score = score
		results = append(results, res)
	}

	// Sort by fused score
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Apply TopK
	if opts.TopK > 0 && len(results) > opts.TopK {
		results = results[:opts.TopK]
	}

	return results, nil
}

// scoreAndSort helper
func (s *SQLiteStore) scoreAndSort(query []float32, candidates []ScoredEmbedding, opts SearchOptions) ([]ScoredEmbedding, error) {
	results := make([]ScoredEmbedding, 0, len(candidates))
	for _, candidate := range candidates {
		score := s.similarityFn(query, candidate.Vector)
		candidate.Score = score
		results = append(results, candidate)
	}
	
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	
	if opts.TopK > 0 && len(results) > opts.TopK {
		results = results[:opts.TopK]
	}
	
	return results, nil
}

// NegativeSearchOptions for "not like this" queries
type NegativeSearchOptions struct {
	// Positive examples (find similar to these)
	PositiveVectors [][]float32
	
	// Negative examples (avoid similar to these)
	NegativeVectors [][]float32
	
	// Weight for negative examples (higher = stronger avoidance)
	NegativeWeight float32
	
	// Base search options
	SearchOptions
}

// DiversitySearchOptions for diverse result sampling
type DiversitySearchOptions struct {
	// Lambda parameter for MMR (0 = max diversity, 1 = max relevance)
	Lambda float32
	
	// Diversity method
	Method DiversityMethod
	
	// Minimum distance between results
	MinDistance float32
	
	// Base search options
	SearchOptions
}

// DiversityMethod for result diversification
type DiversityMethod string

const (
	// Maximal Marginal Relevance
	DiversityMMR DiversityMethod = "mmr"
	
	// Determinantal Point Process
	DiversityDPP DiversityMethod = "dpp"
	
	// Simple distance-based
	DiversityDistance DiversityMethod = "distance"
	
	// Random sampling
	DiversityRandom DiversityMethod = "random"
)

// SearchWithNegatives performs search with negative examples
func (s *SQLiteStore) SearchWithNegatives(ctx context.Context, opts NegativeSearchOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, wrapError("search_negatives", ErrStoreClosed)
	}
	
	// Get all candidates
	candidates, err := s.fetchCandidates(ctx, opts.SearchOptions)
	if err != nil {
		return nil, wrapError("search_negatives", err)
	}
	
	// Score candidates with positive and negative influences
	for i := range candidates {
		positiveScore := float32(0)
		negativeScore := float32(0)
		
		// Calculate positive scores
		if len(opts.PositiveVectors) > 0 {
			for _, posVec := range opts.PositiveVectors {
				score := float32(s.similarityFn(posVec, candidates[i].Vector))
				if score > positiveScore {
					positiveScore = score
				}
			}
		}
		
		// Calculate negative scores
		if len(opts.NegativeVectors) > 0 {
			for _, negVec := range opts.NegativeVectors {
				score := float32(s.similarityFn(negVec, candidates[i].Vector))
				if score > negativeScore {
					negativeScore = score
				}
			}
		}
		
		// Combine scores (positive - weighted negative)
		weight := opts.NegativeWeight
		if weight == 0 {
			weight = 0.5 // Default weight
		}
		
		finalScore := positiveScore - (weight * negativeScore)
		candidates[i].Score = float64(finalScore)
	}
	
	// Sort by final score
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	
	// Apply TopK
	if opts.TopK > 0 && len(candidates) > opts.TopK {
		candidates = candidates[:opts.TopK]
	}
	
	return candidates, nil
}

// SearchWithDiversity performs search with result diversification
func (s *SQLiteStore) SearchWithDiversity(ctx context.Context, query []float32, opts DiversitySearchOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, wrapError("search_diversity", ErrStoreClosed)
	}
	
	// Get initial candidates (more than needed for diversity)
	searchOpts := opts.SearchOptions
	searchOpts.TopK = opts.TopK * 3 // Get 3x candidates for diversity selection
	
	candidates, err := s.Search(ctx, query, searchOpts)
	if err != nil {
		return nil, wrapError("search_diversity", err)
	}
	
	// Apply diversity method
	switch opts.Method {
	case DiversityMMR:
		return s.diversifyMMR(candidates, query, opts), nil
	case DiversityDistance:
		return s.diversifyDistance(candidates, opts), nil
	case DiversityRandom:
		return s.diversifyRandom(candidates, opts), nil
	case DiversityDPP:
		return s.diversifyDPP(candidates, opts), nil
	default:
		return s.diversifyMMR(candidates, query, opts), nil
	}
}

// diversifyMMR implements Maximal Marginal Relevance
func (s *SQLiteStore) diversifyMMR(candidates []ScoredEmbedding, query []float32, opts DiversitySearchOptions) []ScoredEmbedding {
	if len(candidates) == 0 {
		return candidates
	}
	
	lambda := opts.Lambda
	if lambda == 0 {
		lambda = 0.5 // Balance relevance and diversity
	}
	
	selected := []ScoredEmbedding{}
	remaining := make([]ScoredEmbedding, len(candidates))
	copy(remaining, candidates)
	
	// Select first item (highest relevance)
	selected = append(selected, remaining[0])
	remaining = remaining[1:]
	
	// Iteratively select diverse items
	for len(selected) < opts.TopK && len(remaining) > 0 {
		bestIdx := -1
		bestScore := float32(-math.MaxFloat32)
		
		for i, candidate := range remaining {
			// Relevance score (similarity to query)
			relevance := float32(candidate.Score)
			
			// Diversity score (minimum similarity to selected items)
			maxSim := float32(0)
			for _, sel := range selected {
				sim := float32(s.similarityFn(candidate.Vector, sel.Vector))
				if sim > maxSim {
					maxSim = sim
				}
			}
			
			// MMR score
			mmrScore := lambda*relevance - (1-lambda)*maxSim
			
			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
			}
		}
		
		if bestIdx >= 0 {
			selected = append(selected, remaining[bestIdx])
			// Remove selected item
			remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
		} else {
			break
		}
	}
	
	return selected
}

// diversifyDistance ensures minimum distance between results
func (s *SQLiteStore) diversifyDistance(candidates []ScoredEmbedding, opts DiversitySearchOptions) []ScoredEmbedding {
	if len(candidates) == 0 {
		return candidates
	}
	
	selected := []ScoredEmbedding{}
	minDist := opts.MinDistance
	if minDist == 0 {
		minDist = 0.1 // Default minimum distance
	}
	
	for _, candidate := range candidates {
		// Check if candidate is far enough from all selected
		tooClose := false
		for _, sel := range selected {
			dist := float32(1.0 - s.similarityFn(candidate.Vector, sel.Vector))
			if dist < minDist {
				tooClose = true
				break
			}
		}
		
		if !tooClose {
			selected = append(selected, candidate)
			if len(selected) >= opts.TopK {
				break
			}
		}
	}
	
	return selected
}

// diversifyRandom randomly samples from top candidates
func (s *SQLiteStore) diversifyRandom(candidates []ScoredEmbedding, opts DiversitySearchOptions) []ScoredEmbedding {
	if len(candidates) <= opts.TopK {
		return candidates
	}
	
	// Take top 2*K candidates
	poolSize := opts.TopK * 2
	if poolSize > len(candidates) {
		poolSize = len(candidates)
	}
	pool := candidates[:poolSize]
	
	// Randomly sample K from pool
	selected := make([]ScoredEmbedding, 0, opts.TopK)
	indices := rand.Perm(len(pool))
	
	for i := 0; i < opts.TopK && i < len(indices); i++ {
		selected = append(selected, pool[indices[i]])
	}
	
	// Sort by score
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Score > selected[j].Score
	})
	
	return selected
}

// diversifyDPP implements Determinantal Point Process for diversity
func (s *SQLiteStore) diversifyDPP(candidates []ScoredEmbedding, opts DiversitySearchOptions) []ScoredEmbedding {
	if len(candidates) <= opts.TopK {
		return candidates
	}
	
	// Build similarity kernel matrix
	n := len(candidates)
	if n > 100 {
		n = 100 // Limit for computational efficiency
	}
	
	kernel := make([][]float32, n)
	for i := 0; i < n; i++ {
		kernel[i] = make([]float32, n)
		for j := 0; j < n; j++ {
			if i == j {
				// Diagonal: quality score
				kernel[i][j] = float32(candidates[i].Score)
			} else {
				// Off-diagonal: similarity * quality
				sim := float32(s.similarityFn(candidates[i].Vector, candidates[j].Vector))
				kernel[i][j] = sim * float32(math.Sqrt(float64(candidates[i].Score*candidates[j].Score)))
			}
		}
	}
	
	// Greedy DPP selection
	selected := []ScoredEmbedding{}
	selectedIndices := make(map[int]bool)
	
	for len(selected) < opts.TopK && len(selected) < n {
		bestIdx := -1
		bestGain := float32(0)
		
		for i := 0; i < n; i++ {
			if selectedIndices[i] {
				continue
			}
			
			// Calculate marginal gain
			gain := kernel[i][i]
			for j := range selectedIndices {
				gain -= kernel[i][j] * kernel[i][j] / kernel[j][j]
			}
			
			if gain > bestGain {
				bestGain = gain
				bestIdx = i
			}
		}
		
		if bestIdx >= 0 {
			selected = append(selected, candidates[bestIdx])
			selectedIndices[bestIdx] = true
		} else {
			break
		}
	}
	
	return selected
}

// RecommendSimilar finds items similar to given examples
func (s *SQLiteStore) RecommendSimilar(ctx context.Context, positiveIDs []string, negativeIDs []string, opts SearchOptions) ([]ScoredEmbedding, error) {
	// Fetch vectors for positive and negative examples
	positiveVectors := [][]float32{}
	for _, id := range positiveIDs {
		emb, err := s.GetByID(ctx, id)
		if err == nil && emb != nil {
			positiveVectors = append(positiveVectors, emb.Vector)
		}
	}
	
	negativeVectors := [][]float32{}
	for _, id := range negativeIDs {
		emb, err := s.GetByID(ctx, id)
		if err == nil && emb != nil {
			negativeVectors = append(negativeVectors, emb.Vector)
		}
	}
	
	// Perform negative search
	return s.SearchWithNegatives(ctx, NegativeSearchOptions{
		PositiveVectors: positiveVectors,
		NegativeVectors: negativeVectors,
		NegativeWeight:  0.5,
		SearchOptions:   opts,
	})
}

// FindAnomalies finds vectors that are outliers
func (s *SQLiteStore) FindAnomalies(ctx context.Context, opts SearchOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, wrapError("find_anomalies", ErrStoreClosed)
	}
	
	// Get all vectors
	candidates, err := s.fetchCandidates(ctx, opts)
	if err != nil {
		return nil, wrapError("find_anomalies", err)
	}
	
	// Calculate average distance to k nearest neighbors for each vector
	k := 5 // Number of neighbors to consider
	anomalyScores := make([]float64, len(candidates))
	
	for i, candidate := range candidates {
		distances := []float32{}
		
		// Calculate distance to all other vectors
		for j, other := range candidates {
			if i != j {
				dist := float32(1.0 - s.similarityFn(candidate.Vector, other.Vector))
				distances = append(distances, dist)
			}
		}
		
		// Sort distances
		sort.Slice(distances, func(a, b int) bool {
			return distances[a] < distances[b]
		})
		
		// Average distance to k nearest neighbors
		avgDist := float32(0)
		limit := k
		if limit > len(distances) {
			limit = len(distances)
		}
		
		for j := 0; j < limit; j++ {
			avgDist += distances[j]
		}
		if limit > 0 {
			avgDist /= float32(limit)
		}
		
		anomalyScores[i] = float64(avgDist)
		candidates[i].Score = anomalyScores[i]
	}
	
	// Sort by anomaly score (higher = more anomalous)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	
	// Return top anomalies
	if opts.TopK > 0 && len(candidates) > opts.TopK {
		candidates = candidates[:opts.TopK]
	}
	
	return candidates, nil
}