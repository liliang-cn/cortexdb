package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Reranker defines the interface for re-ranking search results
// A reranker takes the initial search results and reorders them based on
// additional relevance signals beyond vector similarity
type Reranker interface {
	// Rerank reorders the scored embeddings based on the query
	// Returns a new slice with the same embeddings, but potentially different scores/order
	Rerank(ctx context.Context, query string, results []ScoredEmbedding) ([]ScoredEmbedding, error)
}

// RerankerFunc is a function adapter that implements Reranker interface
type RerankerFunc func(ctx context.Context, query string, results []ScoredEmbedding) ([]ScoredEmbedding, error)

// Rerank implements the Reranker interface
func (f RerankerFunc) Rerank(ctx context.Context, query string, results []ScoredEmbedding) ([]ScoredEmbedding, error) {
	return f(ctx, query, results)
}

// RerankOptions defines options for reranking
type RerankOptions struct {
	// TopK is the number of results to return after reranking
	TopK int
	// Threshold is the minimum score threshold after reranking
	Threshold float64
	// PreserveOriginalScore keeps the original vector similarity score
	// If false, the score is replaced by the reranker's score
	PreserveOriginalScore bool
}

// DefaultRerankOptions returns default reranking options
func DefaultRerankOptions() RerankOptions {
	return RerankOptions{
		TopK:                 10,
		Threshold:            0.0,
		PreserveOriginalScore: false,
	}
}

// SearchWithReranker performs vector search and then reranks the results
func (s *SQLiteStore) SearchWithReranker(ctx context.Context, queryVec []float32, queryText string, reranker Reranker, opts RerankOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("search_rerank", ErrStoreClosed)
	}

	if reranker == nil {
		// No reranker, return normal search results
		return s.Search(ctx, queryVec, SearchOptions{TopK: opts.TopK})
	}

	// Perform initial search with more candidates than needed
	candidateMultiplier := 5
	initialTopK := opts.TopK * candidateMultiplier
	if initialTopK < 50 {
		initialTopK = 50
	}

	initialResults, err := s.Search(ctx, queryVec, SearchOptions{TopK: initialTopK})
	if err != nil {
		return nil, wrapError("search_rerank", fmt.Errorf("initial search failed: %w", err))
	}

	if len(initialResults) == 0 {
		return initialResults, nil
	}

	// Apply reranking
	reranked, err := reranker.Rerank(ctx, queryText, initialResults)
	if err != nil {
		return nil, wrapError("search_rerank", fmt.Errorf("reranking failed: %w", err))
	}

	// Apply options
	results := reranked

	// Filter by threshold
	if opts.Threshold > 0 {
		filtered := make([]ScoredEmbedding, 0)
		for _, r := range results {
			if r.Score >= opts.Threshold {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	// Apply TopK
	if opts.TopK > 0 && len(results) > opts.TopK {
		results = results[:opts.TopK]
	}

	return results, nil
}

// ==================== Built-in Rerankers ====================

// ScoreNormalizationReranker normalizes scores to a specific range
type ScoreNormalizationReranker struct {
	MinScore float64
	MaxScore float64
}

// NewScoreNormalizationReranker creates a new score normalizer
func NewScoreNormalizationReranker(min, max float64) *ScoreNormalizationReranker {
	return &ScoreNormalizationReranker{
		MinScore: min,
		MaxScore: max,
	}
}

// Rerank normalizes all scores to the specified range
func (norm *ScoreNormalizationReranker) Rerank(ctx context.Context, query string, results []ScoredEmbedding) ([]ScoredEmbedding, error) {
	if len(results) == 0 {
		return results, nil
	}

	// Find min and max scores
	minVal := results[0].Score
	maxVal := results[0].Score
	for _, r := range results {
		if r.Score < minVal {
			minVal = r.Score
		}
		if r.Score > maxVal {
			maxVal = r.Score
		}
	}

	// Avoid division by zero
	rangeVal := maxVal - minVal
	if rangeVal == 0 {
		rangeVal = 1
	}

	// Normalize scores
	normalized := make([]ScoredEmbedding, len(results))
	for i, r := range results {
		normalized[i] = r
		// Normalize to [0, 1] then scale to [norm.MinScore, norm.MaxScore]
		normalizedScore := (r.Score-minVal)/rangeVal
		normalized[i].Score = normalizedScore*(norm.MaxScore-norm.MinScore) + norm.MinScore
	}

	// Re-sort to ensure order is maintained/restored
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Score > normalized[j].Score
	})

	return normalized, nil
}

// KeywordMatchReranker boosts results that contain the query keywords
type KeywordMatchReranker struct {
	// Boost is the score multiplier for keyword matches
	Boost float64
	// CaseSensitive enables case-sensitive matching
	CaseSensitive bool
}

// NewKeywordMatchReranker creates a new keyword match reranker
func NewKeywordMatchReranker(boost float64) *KeywordMatchReranker {
	return &KeywordMatchReranker{
		Boost:         boost,
		CaseSensitive: false,
	}
}

// Rerank boosts results containing query keywords
func (r *KeywordMatchReranker) Rerank(ctx context.Context, query string, results []ScoredEmbedding) ([]ScoredEmbedding, error) {
	reranked := make([]ScoredEmbedding, len(results))

	queryStr := query
	if !r.CaseSensitive {
		queryStr = strings.ToLower(query)
	}

	// Extract keywords from query (simple tokenization)
	keywords := strings.Fields(queryStr)

	for i, result := range results {
		content := result.Content
		if !r.CaseSensitive {
			content = strings.ToLower(content)
		}

		// Count keyword matches
		matchCount := 0
		for _, kw := range keywords {
			if strings.Contains(content, kw) {
				matchCount++
			}
		}

		// Boost score based on matches
		reranked[i] = result
		if matchCount > 0 {
			boostFactor := 1.0 + (float64(matchCount) * r.Boost)
			reranked[i].Score = result.Score * boostFactor
		}
	}

	// Re-sort by boosted scores
	sort.Slice(reranked, func(i, j int) bool {
		return reranked[i].Score > reranked[j].Score
	})

	return reranked, nil
}

// ReciprocalRankFusionReranker combines multiple ranking signals using RRF
type ReciprocalRankFusionReranker struct {
	// K is the RRF constant (typically 60)
	K float64
	// VectorWeight is the weight for vector similarity scores
	VectorWeight float64
	// TextWeight is the weight for text match scores
	TextWeight float64
}

// NewReciprocalRankFusionReranker creates a new RRF reranker
func NewReciprocalRankFusionReranker(k float64) *ReciprocalRankFusionReranker {
	return &ReciprocalRankFusionReranker{
		K:            k,
		VectorWeight: 0.5,
		TextWeight:   0.5,
	}
}

// Rerank combines vector and text ranking using RRF
func (r *ReciprocalRankFusionReranker) Rerank(ctx context.Context, query string, results []ScoredEmbedding) ([]ScoredEmbedding, error) {
	if len(results) == 0 {
		return results, nil
	}

	reranked := make([]ScoredEmbedding, len(results))
	queryLower := strings.ToLower(query)

	// Calculate text match scores
	textScores := make([]float64, len(results))
	for i, result := range results {
		content := strings.ToLower(result.Content)
		// Simple text similarity based on word overlap
		queryWords := strings.Fields(queryLower)
		contentWords := strings.Fields(content)

		matchCount := 0
		for _, qw := range queryWords {
			for _, cw := range contentWords {
				if qw == cw {
					matchCount++
					break
				}
			}
		}

		if len(queryWords) > 0 {
			textScores[i] = float64(matchCount) / float64(len(queryWords))
		}
	}

		// Calculate RRF scores
		for i, result := range results {
			reranked[i] = result
			// RRF formula: 1/(k+rank)
			// Higher score = better rank = lower rank value (1, 2, 3...)
			vectorRank := float64(i + 1)
			textRank := float64(len(results)) // Default to last rank

			// Find text rank
			type scoredItem struct {
				idx   int
				score float64
			}
			textRanked := make([]scoredItem, len(results))
			for j := range results {
				textRanked[j] = scoredItem{j, textScores[j]}
			}
			// Sort text candidates by score descending
			sort.Slice(textRanked, func(a, b int) bool {
				return textRanked[a].score > textRanked[b].score
			})
			for j, item := range textRanked {
				if item.idx == i {
					textRank = float64(j + 1) // 1-indexed rank
					break
				}
			}

			// Combine using RRF
			rrfScore := (r.VectorWeight / (r.K + vectorRank)) + (r.TextWeight / (r.K + textRank))
			reranked[i].Score = rrfScore
		}

	// Sort by combined score
	sort.Slice(reranked, func(i, j int) bool {
		return reranked[i].Score > reranked[j].Score
	})

	return reranked, nil
}

// DiversityReranker promotes diverse results using Maximal Marginal Relevance (MMR)
type DiversityReranker struct {
	// Lambda controls the diversity vs relevance trade-off
	// 0.0 = maximum diversity, 1.0 = maximum relevance
	Lambda float64
	// SimilarityFunc computes similarity between two embeddings
	SimilarityFunc SimilarityFunc
}

// NewDiversityReranker creates a new diversity-based reranker
func NewDiversityReranker(lambda float64, simFunc SimilarityFunc) *DiversityReranker {
	return &DiversityReranker{
		Lambda:        lambda,
		SimilarityFunc: simFunc,
	}
}

// Rerank applies MMR to promote diverse results
func (r *DiversityReranker) Rerank(ctx context.Context, query string, results []ScoredEmbedding) ([]ScoredEmbedding, error) {
	if len(results) <= 1 {
		return results, nil
	}

	simFunc := r.SimilarityFunc
	if simFunc == nil {
		simFunc = CosineSimilarity
	}

	// Extract query vector (use first result's vector as approximation)
	queryVec := results[0].Vector

	selected := make([]ScoredEmbedding, 0, len(results))
	remaining := make([]ScoredEmbedding, len(results))
	copy(remaining, results)

	for len(remaining) > 0 {
		// Find the item with maximum MMR score
		bestIdx := 0
		bestScore := -1.0

		for i, item := range remaining {
			// Relevance to query
			relevance := simFunc(queryVec, item.Vector)

			// Similarity to already selected items (max similarity)
			maxSim := 0.0
			for _, selected := range selected {
				sim := simFunc(item.Vector, selected.Vector)
				if sim > maxSim {
					maxSim = sim
				}
			}

			// MMR score
			mmrScore := r.Lambda*relevance - (1-r.Lambda)*maxSim

			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
			}
		}

		// Move best item from remaining to selected
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}

	return selected, nil
}

// CustomReranker allows users to provide a custom scoring function
type CustomReranker struct {
	// ScoreFunc computes a custom score for each result
	ScoreFunc func(ctx context.Context, query string, result ScoredEmbedding) float64
}

// NewCustomReranker creates a new custom reranker
func NewCustomReranker(scoreFunc func(ctx context.Context, query string, result ScoredEmbedding) float64) *CustomReranker {
	return &CustomReranker{
		ScoreFunc: scoreFunc,
	}
}

// Rerank applies custom scoring function
func (r *CustomReranker) Rerank(ctx context.Context, query string, results []ScoredEmbedding) ([]ScoredEmbedding, error) {
	reranked := make([]ScoredEmbedding, len(results))

	for i, result := range results {
		reranked[i] = result
		if r.ScoreFunc != nil {
			reranked[i].Score = r.ScoreFunc(ctx, query, result)
		}
	}

	// Sort by new scores
	sort.Slice(reranked, func(i, j int) bool {
		return reranked[i].Score > reranked[j].Score
	})

	return reranked, nil
}

// ==================== Hybrid Reranker ====================

// HybridReranker combines multiple rerankers with weighted scores
type HybridReranker struct {
	Rerankers []Reranker
	Weights   []float64
}

// NewHybridReranker creates a new hybrid reranker
func NewHybridReranker(rerankers []Reranker, weights []float64) *HybridReranker {
	return &HybridReranker{
		Rerankers: rerankers,
		Weights:   weights,
	}
}

// Rerank combines multiple rerankers
func (r *HybridReranker) Rerank(ctx context.Context, query string, results []ScoredEmbedding) ([]ScoredEmbedding, error) {
	if len(r.Rerankers) == 0 {
		return results, nil
	}

	// Apply each reranker and collect scores
	allScores := make([][]float64, len(r.Rerankers))
	for i, reranker := range r.Rerankers {
		reranked, err := reranker.Rerank(ctx, query, results)
		if err != nil {
			return nil, err
		}
		scores := make([]float64, len(reranked))
		for _, r := range reranked {
			// Find original index
			for k, orig := range results {
				if orig.ID == r.ID {
					scores[k] = r.Score
					break
				}
			}
		}
		allScores[i] = scores
	}

	// Combine weighted scores
	reranked := make([]ScoredEmbedding, len(results))
	copy(reranked, results)

	for i := range reranked {
		combinedScore := 0.0
		totalWeight := 0.0
		for j := range allScores {
			if j < len(r.Weights) {
				combinedScore += allScores[j][i] * r.Weights[j]
				totalWeight += r.Weights[j]
			}
		}
		if totalWeight > 0 {
			reranked[i].Score = combinedScore / totalWeight
		}
	}

	// Sort by combined score
	sort.Slice(reranked, func(i, j int) bool {
		return reranked[i].Score > reranked[j].Score
	})

	return reranked, nil
}
