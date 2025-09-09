package core

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"
)

func TestHNSWIntegration(t *testing.T) {
	// Create temporary database
	dbPath := fmt.Sprintf("/tmp/test_hnsw_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	// Test with HNSW enabled
	t.Run("with_hnsw", func(t *testing.T) {
		config := DefaultConfig()
		config.Path = dbPath
		config.VectorDim = 128
		config.HNSW.Enabled = true
		config.HNSW.M = 16
		config.HNSW.EfConstruction = 200
		config.HNSW.EfSearch = 50

		store, err := NewWithConfig(config)
		if err != nil {
			t.Fatalf("Failed to create store with HNSW: %v", err)
		}

		ctx := context.Background()
		if err := store.Init(ctx); err != nil {
			t.Fatalf("Failed to initialize store: %v", err)
		}
		defer store.Close()

		// Insert test vectors
		vectors := generateTestVectors(100, 128)
		for i, vec := range vectors {
			emb := &Embedding{
				ID:      fmt.Sprintf("vec_%d", i),
				Vector:  vec,
				Content: fmt.Sprintf("Test vector %d", i),
			}
			if err := store.Upsert(ctx, emb); err != nil {
				t.Fatalf("Failed to insert vector %d: %v", i, err)
			}
		}

		// Verify HNSW index is built
		if store.hnswIndex == nil {
			t.Fatal("HNSW index should not be nil when enabled")
		}

		// Search using HNSW
		queryVec := generateTestVectors(1, 128)[0]
		results, err := store.Search(ctx, queryVec, SearchOptions{
			TopK: 10,
		})
		if err != nil {
			t.Fatalf("Failed to search with HNSW: %v", err)
		}

		// Debug: Check HNSW index stats
		if store.hnswIndex != nil {
			stats := store.hnswIndex.Stats()
			t.Logf("HNSW stats: %+v", stats)
		}

		if len(results) == 0 {
			t.Fatal("HNSW search returned no results")
		}

		// Results should be sorted by score
		for i := 1; i < len(results); i++ {
			if results[i-1].Score < results[i].Score {
				t.Error("Results not properly sorted by score")
			}
		}
	})

	// Test with HNSW disabled (linear search fallback)
	t.Run("without_hnsw", func(t *testing.T) {
		dbPath2 := fmt.Sprintf("/tmp/test_no_hnsw_%d.db", time.Now().UnixNano())
		defer os.Remove(dbPath2)

		config := DefaultConfig()
		config.Path = dbPath2
		config.VectorDim = 128
		config.HNSW.Enabled = false

		store, err := NewWithConfig(config)
		if err != nil {
			t.Fatalf("Failed to create store without HNSW: %v", err)
		}

		ctx := context.Background()
		if err := store.Init(ctx); err != nil {
			t.Fatalf("Failed to initialize store: %v", err)
		}
		defer store.Close()

		// Insert test vectors
		vectors := generateTestVectors(100, 128)
		for i, vec := range vectors {
			emb := &Embedding{
				ID:      fmt.Sprintf("vec_%d", i),
				Vector:  vec,
				Content: fmt.Sprintf("Test vector %d", i),
			}
			if err := store.Upsert(ctx, emb); err != nil {
				t.Fatalf("Failed to insert vector %d: %v", i, err)
			}
		}

		// Verify HNSW index is NOT built
		if store.hnswIndex != nil {
			t.Fatal("HNSW index should be nil when disabled")
		}

		// Search using linear search
		queryVec := generateTestVectors(1, 128)[0]
		results, err := store.Search(ctx, queryVec, SearchOptions{
			TopK: 10,
		})
		if err != nil {
			t.Fatalf("Failed to search without HNSW: %v", err)
		}

		if len(results) == 0 {
			t.Fatal("Linear search returned no results")
		}
	})
}

func TestHNSWRebuild(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_hnsw_rebuild_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	ctx := context.Background()

	// First, create database with HNSW disabled and insert vectors
	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 64
	config.HNSW.Enabled = false

	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}

	// Insert vectors with HNSW disabled
	vectors := generateTestVectors(50, 64)
	for i, vec := range vectors {
		emb := &Embedding{
			ID:      fmt.Sprintf("vec_%d", i),
			Vector:  vec,
			Content: fmt.Sprintf("Test vector %d", i),
		}
		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to insert vector %d: %v", i, err)
		}
	}
	store.Close()

	// Now reopen with HNSW enabled - should rebuild index from existing data
	config.HNSW.Enabled = true
	store2, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to recreate store with HNSW: %v", err)
	}

	if err := store2.Init(ctx); err != nil {
		t.Fatalf("Failed to reinitialize store: %v", err)
	}
	defer store2.Close()

	// Verify HNSW index was built from existing data
	if store2.hnswIndex == nil {
		t.Fatal("HNSW index should be built on init when enabled")
	}

	// Search should work with rebuilt index
	queryVec := generateTestVectors(1, 64)[0]
	results, err := store2.Search(ctx, queryVec, SearchOptions{
		TopK: 5,
	})
	if err != nil {
		t.Fatalf("Failed to search with rebuilt HNSW: %v", err)
	}

	if len(results) != 5 {
		t.Errorf("Expected 5 results, got %d", len(results))
	}
}

func TestHNSWPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	dbPath := fmt.Sprintf("/tmp/test_hnsw_perf_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	ctx := context.Background()
	numVectors := 10000
	dim := 256
	numQueries := 100

	// Test with HNSW
	configHNSW := DefaultConfig()
	configHNSW.Path = dbPath
	configHNSW.VectorDim = dim
	configHNSW.HNSW.Enabled = true

	storeHNSW, _ := NewWithConfig(configHNSW)
	storeHNSW.Init(ctx)

	// Insert vectors
	vectors := generateTestVectors(numVectors, dim)
	for i, vec := range vectors {
		emb := &Embedding{
			ID:     fmt.Sprintf("vec_%d", i),
			Vector: vec,
		}
		storeHNSW.Upsert(ctx, emb)
	}

	// Benchmark HNSW search
	queries := generateTestVectors(numQueries, dim)
	startHNSW := time.Now()
	for _, q := range queries {
		storeHNSW.Search(ctx, q, SearchOptions{TopK: 10})
	}
	hnswTime := time.Since(startHNSW)
	storeHNSW.Close()

	// Clean up and test without HNSW
	os.Remove(dbPath)
	
	configLinear := DefaultConfig()
	configLinear.Path = dbPath
	configLinear.VectorDim = dim
	configLinear.HNSW.Enabled = false

	storeLinear, _ := NewWithConfig(configLinear)
	storeLinear.Init(ctx)

	// Insert same vectors
	for i, vec := range vectors {
		emb := &Embedding{
			ID:     fmt.Sprintf("vec_%d", i),
			Vector: vec,
		}
		storeLinear.Upsert(ctx, emb)
	}

	// Benchmark linear search
	startLinear := time.Now()
	for _, q := range queries {
		storeLinear.Search(ctx, q, SearchOptions{TopK: 10})
	}
	linearTime := time.Since(startLinear)
	storeLinear.Close()

	// HNSW should be significantly faster
	speedup := float64(linearTime) / float64(hnswTime)
	t.Logf("HNSW search time: %v", hnswTime)
	t.Logf("Linear search time: %v", linearTime)
	t.Logf("Speedup: %.2fx", speedup)

	if speedup < 2.0 {
		t.Logf("Warning: HNSW speedup is less than 2x (%.2fx)", speedup)
	}
}

func generateTestVectors(n, dim int) [][]float32 {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		vec := make([]float32, dim)
		for j := 0; j < dim; j++ {
			vec[j] = rng.Float32()*2 - 1 // Random values between -1 and 1
		}
		// Normalize vector
		var sum float32
		for _, v := range vec {
			sum += v * v
		}
		if sum > 0 {
			norm := float32(1.0) / float32(math.Sqrt(float64(sum)))
			for j := range vec {
				vec[j] *= norm
			}
		}
		vectors[i] = vec
	}
	return vectors
}

// Benchmark tests
func BenchmarkHNSWSearch(b *testing.B) {
	dbPath := fmt.Sprintf("/tmp/bench_hnsw_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 512
	config.HNSW.Enabled = true

	store, _ := NewWithConfig(config)
	ctx := context.Background()
	store.Init(ctx)
	defer store.Close()

	// Insert vectors
	vectors := generateTestVectors(5000, 512)
	for i, vec := range vectors {
		emb := &Embedding{
			ID:     fmt.Sprintf("vec_%d", i),
			Vector: vec,
		}
		store.Upsert(ctx, emb)
	}

	query := generateTestVectors(1, 512)[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Search(ctx, query, SearchOptions{TopK: 10})
	}
}

func BenchmarkLinearSearch(b *testing.B) {
	dbPath := fmt.Sprintf("/tmp/bench_linear_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 512
	config.HNSW.Enabled = false

	store, _ := NewWithConfig(config)
	ctx := context.Background()
	store.Init(ctx)
	defer store.Close()

	// Insert vectors
	vectors := generateTestVectors(5000, 512)
	for i, vec := range vectors {
		emb := &Embedding{
			ID:     fmt.Sprintf("vec_%d", i),
			Vector: vec,
		}
		store.Upsert(ctx, emb)
	}

	query := generateTestVectors(1, 512)[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Search(ctx, query, SearchOptions{TopK: 10})
	}
}