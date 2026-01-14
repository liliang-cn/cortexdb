package core

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestQuantizationIntegration(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_quant_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	ctx := context.Background()
	dim := 128
	numVectors := 50

	// 1. Create store with Quantization enabled
	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = dim
	config.HNSW.Enabled = true
	config.Quantization.Enabled = true
	config.Quantization.NBits = 8 // SQ8

	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// 2. Insert vectors
	vectors := generateTestVectors(numVectors, dim)
	embs := make([]*Embedding, numVectors)
	for i, vec := range vectors {
		embs[i] = &Embedding{
			ID:     fmt.Sprintf("vec_%d", i),
			Vector: vec,
		}
	}

	if err := store.UpsertBatch(ctx, embs); err != nil {
		t.Fatalf("Upsert batch failed: %v", err)
	}

	// 3. Verify Quantizer is active in HNSW
	if store.quantizer == nil {
		t.Fatal("Quantizer should not be nil")
	}
	
	// Check memory: HNSW nodes should have Vector == nil and Quantized != nil
	nodeCount := 0
	for _, node := range store.hnswIndex.Nodes {
		nodeCount++
		if node.Vector != nil {
			t.Errorf("Node %s still has raw vector, should be dropped for memory efficiency", node.ID)
		}
		if node.Quantized == nil {
			t.Errorf("Node %s has no quantized data", node.ID)
		}
	}
	if nodeCount != numVectors {
		t.Errorf("Expected %d nodes, got %d", numVectors, nodeCount)
	}

	// 4. Test search accuracy (should be high for SQ8)
	query := generateTestVectors(1, dim)[0]
	results, err := store.Search(ctx, query, SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}

	t.Logf("Top result score with SQ8: %f", results[0].Score)
	
	// 5. Test Persistence with Quantization
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Reopen
	store2, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to reopen store: %v", err)
	}
	if err := store2.Init(ctx); err != nil {
		t.Fatalf("Failed to init reopened store: %v", err)
	}
	defer store2.Close()

	if store2.quantizer == nil {
		t.Fatal("Quantizer should be restored from snapshot")
	}

	results2, err := store2.Search(ctx, query, SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("Search after reopen failed: %v", err)
	}

	if len(results2) != len(results) {
		t.Errorf("Expected same number of results after reopen, got %d vs %d", len(results2), len(results))
	}
	
	if results2[0].ID != results[0].ID {
		t.Errorf("Top result ID mismatch after reopen: %s vs %s", results2[0].ID, results[0].ID)
	}
}
