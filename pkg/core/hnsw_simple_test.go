package core

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestHNSWSimple(t *testing.T) {
	// Create temporary database
	dbPath := fmt.Sprintf("/tmp/test_hnsw_simple_%d.db", time.Now().UnixNano())
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 4 // Simple 4D vectors
	config.HNSW.Enabled = true
	config.HNSW.M = 16
	config.HNSW.EfConstruction = 200
	config.HNSW.EfSearch = 50

	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	// Insert simple test vectors
	vectors := []struct {
		id  string
		vec []float32
	}{
		{"vec1", []float32{1.0, 0.0, 0.0, 0.0}},
		{"vec2", []float32{0.0, 1.0, 0.0, 0.0}},
		{"vec3", []float32{0.0, 0.0, 1.0, 0.0}},
		{"vec4", []float32{0.5, 0.5, 0.0, 0.0}},
		{"vec5", []float32{0.5, 0.0, 0.5, 0.0}},
	}

	for _, v := range vectors {
		emb := &Embedding{
			ID:      v.id,
			Vector:  v.vec,
			Content: fmt.Sprintf("Test vector %s", v.id),
		}
		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to insert %s: %v", v.id, err)
		}
		t.Logf("Inserted %s", v.id)
	}

	// Check HNSW index stats
	if store.hnswIndex != nil {
		stats := store.hnswIndex.Stats()
		t.Logf("HNSW stats after insert: %+v", stats)
	}

	// Simple search
	queryVec := []float32{0.9, 0.1, 0.0, 0.0}
	t.Logf("Searching with query: %v", queryVec)
	
	// Test HNSW index directly
	if store.hnswIndex != nil {
		ids, dists := store.hnswIndex.Search(queryVec, 3, 50)
		t.Logf("Direct HNSW search returned %d results", len(ids))
		for i, id := range ids {
			t.Logf("  %d. %s (dist: %.4f)", i+1, id, dists[i])
		}
	}
	
	// Test fetchEmbeddingsByIDs directly
	if store.hnswIndex != nil {
		ids, _ := store.hnswIndex.Search(queryVec, 3, 50)
		candidates, err := store.fetchEmbeddingsByIDs(ctx, ids)
		if err != nil {
			t.Logf("fetchEmbeddingsByIDs error: %v", err)
		} else {
			t.Logf("fetchEmbeddingsByIDs returned %d candidates for IDs %v", len(candidates), ids)
		}
	}
	
	// Test through store Search
	results, err := store.Search(ctx, queryVec, SearchOptions{
		TopK: 3,
	})
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}

	t.Logf("Store search returned %d results", len(results))
	for i, res := range results {
		t.Logf("  %d. %s (score: %.4f)", i+1, res.ID, res.Score)
	}

	if len(results) == 0 {
		t.Fatal("No results returned from search")
	}
}