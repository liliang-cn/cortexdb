// Package core provides advanced search capabilities
package core

import (
	"context"
	"math"
	"math/rand"
	"sort"
)

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