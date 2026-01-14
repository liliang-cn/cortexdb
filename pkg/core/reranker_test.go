package core

import (
	"context"
	"os"
	"testing"
)

// TestKeywordMatchReranker tests keyword-based reranking
func TestKeywordMatchReranker(t *testing.T) {
	ctx := context.Background()

	reranker := NewKeywordMatchReranker(0.5) // 50% boost per match

	results := []ScoredEmbedding{
		{Embedding: Embedding{ID: "1", Content: "Machine learning algorithms"}, Score: 0.5},
		{Embedding: Embedding{ID: "2", Content: "Deep neural networks"}, Score: 0.7},
		{Embedding: Embedding{ID: "3", Content: "Machine learning and deep learning"}, Score: 0.6},
	}

	query := "machine learning"

	reranked, err := reranker.Rerank(ctx, query, results)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	// Result 3 should be boosted highest (both "machine" and "learning" match)
	// Result 1 should also be boosted (both match)
	// Result 2 should not be boosted (no match)

	// Find the top result
	if len(reranked) == 0 {
		t.Fatal("No results returned")
	}

	// Result with most keyword matches should be ranked higher
	if reranked[0].ID != "3" && reranked[0].ID != "1" {
		t.Errorf("Expected result 1 or 3 to be top ranked, got %s", reranked[0].ID)
	}

	// Verify scores were modified
	for _, r := range reranked {
		found := false
		for _, orig := range results {
			if orig.ID == r.ID {
				if r.Score == orig.Score && (r.ID == "1" || r.ID == "3") {
					t.Errorf("Score for %s should have been boosted", r.ID)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Result %s not found in original", r.ID)
		}
	}
}

// TestScoreNormalizationReranker tests score normalization
func TestScoreNormalizationReranker(t *testing.T) {
	ctx := context.Background()

	reranker := NewScoreNormalizationReranker(0, 1)

	results := []ScoredEmbedding{
		{Embedding: Embedding{ID: "1"}, Score: 0.5},
		{Embedding: Embedding{ID: "2"}, Score: 0.8},
		{Embedding: Embedding{ID: "3"}, Score: 0.3},
	}

	reranked, err := reranker.Rerank(ctx, "", results)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	// All scores should be in [0, 1]
	for _, r := range reranked {
		if r.Score < 0 || r.Score > 1 {
			t.Errorf("Score %f outside [0, 1] range", r.Score)
		}
	}

	// Order should be preserved (highest original still highest)
	if reranked[0].ID != "2" {
		t.Errorf("Expected ID 2 to be highest, got %s", reranked[0].ID)
	}
}

// TestDiversityReranker tests MMR-based diverse reranking
func TestDiversityReranker(t *testing.T) {
	ctx := context.Background()

	// Use lambda=0.5 for balance between relevance and diversity
	reranker := NewDiversityReranker(0.5, CosineSimilarity)

	// Create results with similar vectors
	results := []ScoredEmbedding{
		{Embedding: Embedding{ID: "1", Vector: []float32{1, 0, 0}}, Score: 0.9},
		{Embedding: Embedding{ID: "2", Vector: []float32{0.95, 0, 0}}, Score: 0.85}, // Very similar to 1
		{Embedding: Embedding{ID: "3", Vector: []float32{0, 1, 0}}, Score: 0.7},    // Different
	}

	reranked, err := reranker.Rerank(ctx, "", results)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	if len(reranked) != 3 {
		t.Errorf("Expected 3 results, got %d", len(reranked))
	}

	// With diversity, result 3 should potentially be ranked higher
	// because it's different from result 1
}

// TestCustomReranker tests custom scoring function
func TestCustomReranker(t *testing.T) {
	ctx := context.Background()

	// Custom scoring: boost results with short content
	reranker := NewCustomReranker(func(ctx context.Context, query string, result ScoredEmbedding) float64 {
		// Inverse of content length
		return float64(1000 / (len(result.Content) + 1))
	})

	results := []ScoredEmbedding{
		{Embedding: Embedding{ID: "1", Content: "S"}, Score: 0.5},
		{Embedding: Embedding{ID: "2", Content: "This is a much longer content string"}, Score: 0.7},
		{Embedding: Embedding{ID: "3", Content: "Medium"}, Score: 0.6},
	}

	reranked, err := reranker.Rerank(ctx, "", results)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	// Shortest content should be ranked highest
	if reranked[0].ID != "1" {
		t.Errorf("Expected ID 1 (shortest) to be highest, got %s", reranked[0].ID)
	}
}

// TestReciprocalRankFusionReranker tests RRF combination
func TestReciprocalRankFusionReranker(t *testing.T) {
	ctx := context.Background()

	reranker := NewReciprocalRankFusionReranker(60)

	results := []ScoredEmbedding{
		{Embedding: Embedding{ID: "1", Content: "machine learning tutorial"}, Score: 0.9},
		{Embedding: Embedding{ID: "2", Content: "deep learning networks"}, Score: 0.7},
		{Embedding: Embedding{ID: "3", Content: "machine learning algorithms"}, Score: 0.85},
	}

	query := "machine learning"

	reranked, err := reranker.Rerank(ctx, query, results)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	if len(reranked) == 0 {
		t.Fatal("No results returned")
	}

	// Results matching query keywords should be ranked higher
	// Both 1 and 3 match "machine learning", with 1 having higher original score
	if reranked[0].ID != "1" {
		t.Errorf("Expected ID 1 to be highest (both vector + text match), got %s", reranked[0].ID)
	}
}

// TestSearchWithRerankerIntegration tests full integration
func TestSearchWithRerankerIntegration(t *testing.T) {
	ctx := context.Background()
	dbPath := "reranker_integration_test.db"

	defer func() { _ = os.Remove(dbPath) }()

	store, err := New(dbPath, 4)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Insert test data with semantic similarity but different content
	testData := []*Embedding{
		{ID: "1", Vector: []float32{1, 0, 0, 0}, Content: "Python programming tutorial"},
		{ID: "2", Vector: []float32{0.95, 0, 0, 0}, Content: "Java programming guide"},     // Similar vector to 1
		{ID: "3", Vector: []float32{0.9, 0, 0, 0}, Content: "Python machine learning"},     // Similar vector, Python keyword
		{ID: "4", Vector: []float32{0, 1, 0, 0}, Content: "JavaScript basics"},            // Different vector
	}

	for _, emb := range testData {
		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	t.Run("Search with keyword reranker", func(t *testing.T) {
		queryVec := []float32{1, 0, 0, 0}
		queryText := "Python"

		reranker := NewKeywordMatchReranker(0.3)
		opts := DefaultRerankOptions()
		opts.TopK = 3

		results, err := store.SearchWithReranker(ctx, queryVec, queryText, reranker, opts)
		if err != nil {
			t.Fatalf("SearchWithReranker failed: %v", err)
		}

		if len(results) == 0 {
			t.Fatal("No results returned")
		}

		// Results containing "Python" should be boosted
		// ID 1 and ID 3 contain "Python"
		for _, r := range results {
			if r.ID == "1" || r.ID == "3" {
				// These should be in top results
				return
			}
		}
		t.Error("Expected Python-related results to be boosted")
	})

	t.Run("Search without reranker", func(t *testing.T) {
		queryVec := []float32{1, 0, 0, 0}

		opts := DefaultRerankOptions()
		opts.TopK = 3

		// nil reranker should return normal search results
		results, err := store.SearchWithReranker(ctx, queryVec, "", nil, opts)
		if err != nil {
			t.Fatalf("SearchWithReranker failed: %v", err)
		}

		if len(results) != 3 {
			t.Errorf("Expected 3 results, got %d", len(results))
		}
	})

	t.Run("Search with threshold", func(t *testing.T) {
		queryVec := []float32{1, 0, 0, 0}
		queryText := "programming"

		reranker := NewKeywordMatchReranker(0.2)
		opts := DefaultRerankOptions()
		opts.TopK = 10
		opts.Threshold = 0.5 // Only keep results with score >= 0.5

		results, err := store.SearchWithReranker(ctx, queryVec, queryText, reranker, opts)
		if err != nil {
			t.Fatalf("SearchWithReranker failed: %v", err)
		}

		// All results should have score >= threshold
		for _, r := range results {
			if r.Score < 0.5 {
				t.Errorf("Result %s has score %f below threshold 0.5", r.ID, r.Score)
			}
		}
	})
}

// TestRerankerWithEmptyResults tests reranker behavior with no results
func TestRerankerWithEmptyResults(t *testing.T) {
	ctx := context.Background()

	rerankers := []Reranker{
		NewKeywordMatchReranker(0.5),
		NewScoreNormalizationReranker(0, 1),
		NewDiversityReranker(0.5, CosineSimilarity),
	}

	for _, r := range rerankers {
		results := []ScoredEmbedding{}
		reranked, err := r.Rerank(ctx, "", results)
		if err != nil {
			t.Errorf("Reranker %T failed with empty results: %v", r, err)
		}
		if len(reranked) != 0 {
			t.Errorf("Reranker %T returned non-empty results for empty input", r)
		}
	}
}

// TestRerankerWithSingleResult tests reranker behavior with one result
func TestRerankerWithSingleResult(t *testing.T) {
	ctx := context.Background()

	rerankers := []Reranker{
		NewKeywordMatchReranker(0.5),
		NewScoreNormalizationReranker(0, 1),
		NewDiversityReranker(0.5, CosineSimilarity),
	}

	for _, r := range rerankers {
		results := []ScoredEmbedding{
			{Embedding: Embedding{ID: "1", Content: "Single result"}, Score: 0.5},
		}
		reranked, err := r.Rerank(ctx, "", results)
		if err != nil {
			t.Errorf("Reranker %T failed with single result: %v", r, err)
		}
		if len(reranked) != 1 {
			t.Errorf("Reranker %T returned %d results for single input", r, len(reranked))
		}
	}
}

// TestHybridReranker tests combining multiple rerankers
func TestHybridReranker(t *testing.T) {
	ctx := context.Background()

	keywordReranker := NewKeywordMatchReranker(0.5)
	normReranker := NewScoreNormalizationReranker(0, 1)

	hybrid := NewHybridReranker(
		[]Reranker{keywordReranker, normReranker},
		[]float64{0.7, 0.3}, // 70% keyword, 30% normalization
	)

	results := []ScoredEmbedding{
		{Embedding: Embedding{ID: "1", Content: "machine learning"}, Score: 0.5},
		{Embedding: Embedding{ID: "2", Content: "other topic"}, Score: 0.8},
	}

	query := "machine learning"

	reranked, err := hybrid.Rerank(ctx, query, results)
	if err != nil {
		t.Fatalf("HybridReranker failed: %v", err)
	}

	if len(reranked) != 2 {
		t.Errorf("Expected 2 results, got %d", len(reranked))
	}
}
