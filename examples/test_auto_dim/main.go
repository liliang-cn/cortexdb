package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/sqvect"
)

func main() {
	dbPath := "test_auto_dim.db"
	defer os.Remove(dbPath)

	// Test 1: Create store with dimension = 0 (auto-detect)
	fmt.Println("=== Test 1: Auto-detect dimension (VectorDim = 0) ===")
	store, err := sqvect.New(dbPath, 0)  // 0 should mean auto-detect
	if err != nil {
		log.Fatal("Failed to create store:", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		log.Fatal("Failed to init:", err)
	}

	// Try to insert a 5-dimensional vector
	err = store.Upsert(ctx, &sqvect.Embedding{
		ID:       "test1",
		Vector:   []float32{1.0, 2.0, 3.0, 4.0, 5.0},  // 5 dimensions
		Content:  "First document with 5D vector",
		Metadata: map[string]string{},
	})
	if err != nil {
		fmt.Printf("❌ Failed to insert 5D vector: %v\n", err)
	} else {
		fmt.Println("✓ Successfully inserted 5D vector")
	}

	// Try to insert another 5D vector
	err = store.Upsert(ctx, &sqvect.Embedding{
		ID:       "test2",
		Vector:   []float32{2.0, 3.0, 4.0, 5.0, 6.0},  // Also 5 dimensions
		Content:  "Second document with 5D vector",
		Metadata: map[string]string{},
	})
	if err != nil {
		fmt.Printf("❌ Failed to insert second 5D vector: %v\n", err)
	} else {
		fmt.Println("✓ Successfully inserted second 5D vector")
	}

	// Try to insert a different dimension (should fail or adapt)
	err = store.Upsert(ctx, &sqvect.Embedding{
		ID:       "test3",
		Vector:   []float32{1.0, 2.0, 3.0},  // 3 dimensions
		Content:  "Document with 3D vector",
		Metadata: map[string]string{},
	})
	if err != nil {
		fmt.Printf("❌ Expected: dimension mismatch error: %v\n", err)
	} else {
		fmt.Println("✓ Somehow accepted 3D vector (adapted?)")
	}

	// Search with 5D vector
	results, err := store.Search(ctx, []float32{1.0, 2.0, 3.0, 4.0, 5.0}, sqvect.SearchOptions{TopK: 10})
	if err != nil {
		fmt.Printf("❌ Search failed: %v\n", err)
	} else {
		fmt.Printf("✓ Search succeeded, found %d results\n", len(results))
		for i, r := range results {
			fmt.Printf("  %d. ID: %s, Score: %.6f\n", i+1, r.ID, r.Score)
		}
	}

	fmt.Println("\n=== Test 2: Check with config ===")
	os.Remove("test_config.db")
	defer os.Remove("test_config.db")
	
	config := sqvect.DefaultConfig()
	config.Path = "test_config.db"
	config.VectorDim = 0  // Auto-detect
	config.AutoDimAdapt = sqvect.SmartAdapt  // Use smart adaptation
	
	store2, err := sqvect.NewWithConfig(config)
	if err != nil {
		log.Fatal("Failed to create store with config:", err)
	}
	defer store2.Close()
	
	if err := store2.Init(ctx); err != nil {
		log.Fatal("Failed to init store2:", err)
	}

	// Insert 768-dim vector (like all-mpnet-base-v2)
	vec768 := make([]float32, 768)
	for i := range vec768 {
		vec768[i] = float32(i) * 0.001
	}
	
	err = store2.Upsert(ctx, &sqvect.Embedding{
		ID:       "doc_768",
		Vector:   vec768,
		Content:  "Document with 768D vector",
		Metadata: map[string]string{},
	})
	if err != nil {
		fmt.Printf("❌ Failed to insert 768D vector: %v\n", err)
	} else {
		fmt.Println("✓ Successfully auto-detected and stored 768D vector!")
	}

	// Verify with search
	results2, err := store2.Search(ctx, vec768, sqvect.SearchOptions{TopK: 1})
	if err != nil {
		fmt.Printf("❌ Search failed: %v\n", err)
	} else if len(results2) > 0 && results2[0].Score > 0.999 {
		fmt.Println("✓ Perfect match found with auto-detected dimensions!")
	}
}