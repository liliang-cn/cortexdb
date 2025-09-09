package core

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"
)

func TestRangeSearch(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_range_search_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 3
	config.SimilarityFn = EuclideanDist // Use Euclidean distance for this test
	config.HNSW.Enabled = false // Test without HNSW for simplicity

	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	t.Run("BasicRangeSearch", func(t *testing.T) {
		// Insert test vectors in 3D space
		vectors := []*Embedding{
			{ID: "origin", Vector: []float32{0, 0, 0}},
			{ID: "p1", Vector: []float32{1, 0, 0}},
			{ID: "p2", Vector: []float32{0, 1, 0}},
			{ID: "p3", Vector: []float32{0, 0, 1}},
			{ID: "p4", Vector: []float32{1, 1, 0}}, // distance sqrt(2) from origin
			{ID: "p5", Vector: []float32{1, 0, 1}}, // distance sqrt(2) from origin
			{ID: "p6", Vector: []float32{0, 1, 1}}, // distance sqrt(2) from origin
			{ID: "p7", Vector: []float32{1, 1, 1}}, // distance sqrt(3) from origin
			{ID: "far", Vector: []float32{3, 3, 3}}, // distance 3*sqrt(3) from origin
		}

		for _, emb := range vectors {
			if err := store.Upsert(ctx, emb); err != nil {
				t.Fatalf("Failed to upsert %s: %v", emb.ID, err)
			}
		}

		// Search from origin with radius 1.0
		query := []float32{0, 0, 0}
		radius := float32(1.0)

		results, err := store.RangeSearch(ctx, query, radius, SearchOptions{})
		if err != nil {
			t.Fatalf("Range search failed: %v", err)
		}

		t.Logf("Range search (radius=%.1f) found %d points:", radius, len(results))
		for _, r := range results {
			t.Logf("  %s (score: %.4f)", r.ID, r.Score)
		}

		// Should find origin, p1, p2, p3 (all at distance <= 1.0)
		expectedIDs := map[string]bool{
			"origin": true,
			"p1":     true,
			"p2":     true,
			"p3":     true,
		}

		if len(results) != len(expectedIDs) {
			t.Errorf("Expected %d results, got %d", len(expectedIDs), len(results))
		}

		for _, r := range results {
			if !expectedIDs[r.ID] {
				t.Errorf("Unexpected result: %s", r.ID)
			}
		}
	})

	t.Run("LargerRadius", func(t *testing.T) {
		query := []float32{0, 0, 0}
		radius := float32(1.5) // Should include sqrt(2) ≈ 1.414

		results, err := store.RangeSearch(ctx, query, radius, SearchOptions{})
		if err != nil {
			t.Fatalf("Range search failed: %v", err)
		}

		t.Logf("Range search (radius=%.1f) found %d points:", radius, len(results))

		// Should find origin, p1-p6 (p7 is at sqrt(3) ≈ 1.732)
		if len(results) != 7 {
			t.Errorf("Expected 7 results with radius %.1f, got %d", radius, len(results))
		}
	})

	t.Run("VeryLargeRadius", func(t *testing.T) {
		query := []float32{0, 0, 0}
		radius := float32(10.0) // Should include all points

		results, err := store.RangeSearch(ctx, query, radius, SearchOptions{})
		if err != nil {
			t.Fatalf("Range search failed: %v", err)
		}

		// Should find all 9 points
		if len(results) != 9 {
			t.Errorf("Expected all 9 points with large radius, got %d", len(results))
		}
	})

	t.Run("NonZeroQueryPoint", func(t *testing.T) {
		// Search from p1 (1,0,0)
		query := []float32{1, 0, 0}
		radius := float32(1.0)

		results, err := store.RangeSearch(ctx, query, radius, SearchOptions{})
		if err != nil {
			t.Fatalf("Range search failed: %v", err)
		}

		t.Logf("Range search from p1 (radius=%.1f) found %d points:", radius, len(results))
		for _, r := range results {
			t.Logf("  %s", r.ID)
		}

		// Should find p1 itself and origin (distance 1)
		foundP1 := false
		foundOrigin := false
		for _, r := range results {
			if r.ID == "p1" {
				foundP1 = true
			}
			if r.ID == "origin" {
				foundOrigin = true
			}
		}

		if !foundP1 {
			t.Error("Should find p1 (query point itself)")
		}
		if !foundOrigin {
			t.Error("Should find origin (distance 1 from p1)")
		}
	})

	t.Run("InvalidRadius", func(t *testing.T) {
		query := []float32{0, 0, 0}

		// Test with zero radius
		_, err := store.RangeSearch(ctx, query, 0, SearchOptions{})
		if err == nil {
			t.Error("Expected error for zero radius")
		}

		// Test with negative radius
		_, err = store.RangeSearch(ctx, query, -1.0, SearchOptions{})
		if err == nil {
			t.Error("Expected error for negative radius")
		}
	})

	t.Run("EmptyDatabase", func(t *testing.T) {
		// Create a new store with no data
		dbPath2 := fmt.Sprintf("/tmp/test_range_empty_%d.db", time.Now().UnixNano())
		defer func() { _ = os.Remove(dbPath2) }()

		store2, _ := NewWithConfig(Config{
			Path:       dbPath2,
			VectorDim:  3,
		})
		defer func() { _ = store2.Close() }()
		_ = store2.Init(ctx)

		query := []float32{0, 0, 0}
		results, err := store2.RangeSearch(ctx, query, 1.0, SearchOptions{})
		if err != nil {
			t.Fatalf("Range search on empty DB failed: %v", err)
		}

		if len(results) != 0 {
			t.Errorf("Expected 0 results from empty database, got %d", len(results))
		}
	})
}

func TestRangeSearchWithCosineDistance(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_range_cosine_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 3
	config.SimilarityFn = CosineSimilarity
	config.HNSW.Enabled = false

	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Normalize helper
	normalize := func(v []float32) []float32 {
		var sum float32
		for _, val := range v {
			sum += val * val
		}
		norm := float32(math.Sqrt(float64(sum)))
		result := make([]float32, len(v))
		for i, val := range v {
			result[i] = val / norm
		}
		return result
	}

	// Insert normalized vectors
	vectors := []*Embedding{
		{ID: "v1", Vector: normalize([]float32{1, 0, 0})},
		{ID: "v2", Vector: normalize([]float32{1, 1, 0})},
		{ID: "v3", Vector: normalize([]float32{0, 1, 0})},
		{ID: "v4", Vector: normalize([]float32{-1, 0, 0})}, // Opposite of v1
		{ID: "v5", Vector: normalize([]float32{1, 0.1, 0})}, // Very similar to v1
	}

	for _, emb := range vectors {
		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to upsert %s: %v", emb.ID, err)
		}
	}

	// Search for vectors similar to v1
	query := normalize([]float32{1, 0, 0})
	radius := float32(0.2) // Small cosine distance

	results, err := store.RangeSearch(ctx, query, radius, SearchOptions{})
	if err != nil {
		t.Fatalf("Range search failed: %v", err)
	}

	t.Logf("Cosine range search (radius=%.2f) found %d vectors:", radius, len(results))
	for _, r := range results {
		t.Logf("  %s (score: %.4f)", r.ID, r.Score)
	}

	// Should find v1 (identical) and v5 (very similar)
	foundV1 := false
	foundV5 := false
	for _, r := range results {
		if r.ID == "v1" {
			foundV1 = true
		}
		if r.ID == "v5" {
			foundV5 = true
		}
	}

	if !foundV1 {
		t.Error("Should find v1 (identical to query)")
	}
	if !foundV5 {
		t.Error("Should find v5 (very similar to query)")
	}

	// v4 should NOT be found (opposite direction, cosine distance ≈ 2)
	for _, r := range results {
		if r.ID == "v4" {
			t.Error("Should not find v4 (opposite direction)")
		}
	}
}

func TestRangeSearchSortOrder(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_range_sort_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, _ := NewWithConfig(Config{
		Path:       dbPath,
		VectorDim:  2,
	})
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	_ = store.Init(ctx)

	// Insert vectors at different distances
	vectors := []*Embedding{
		{ID: "close", Vector: []float32{0.1, 0}},
		{ID: "medium", Vector: []float32{0.5, 0}},
		{ID: "far", Vector: []float32{0.9, 0}},
	}

	for _, emb := range vectors {
		_ = store.Upsert(ctx, emb)
	}

	// Range search from origin
	results, _ := store.RangeSearch(ctx, []float32{0, 0}, 1.0, SearchOptions{})

	// Results should be sorted by score (highest first)
	if len(results) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(results))
	}

	// Check order: closest should have highest score
	if results[0].ID != "close" {
		t.Errorf("Expected 'close' to be first, got %s", results[0].ID)
	}
	if results[1].ID != "medium" {
		t.Errorf("Expected 'medium' to be second, got %s", results[1].ID)
	}
	if results[2].ID != "far" {
		t.Errorf("Expected 'far' to be third, got %s", results[2].ID)
	}

	// Verify scores are in descending order
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("Scores not in descending order: %.4f > %.4f",
				results[i].Score, results[i-1].Score)
		}
	}
}