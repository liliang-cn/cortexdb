package main

import (
	"context"
	"fmt"
	"log"

	"github.com/liliang-cn/sqvect"
)

func main() {
	// Create a simple vector store
	store, err := sqvect.New("basic.db", 3)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("Warning: failed to close store: %v", err)
		}
	}()

	ctx := context.Background()
	
	// Initialize the store
	if err := store.Init(ctx); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Basic sqvect example")
	fmt.Println("===================")

	// Create some simple 3D vectors
	embeddings := []sqvect.Embedding{
		{
			ID:      "point_a",
			Vector:  []float32{1.0, 0.0, 0.0},
			Content: "Point A at (1,0,0)",
		},
		{
			ID:      "point_b", 
			Vector:  []float32{0.0, 1.0, 0.0},
			Content: "Point B at (0,1,0)",
		},
		{
			ID:      "point_c",
			Vector:  []float32{0.0, 0.0, 1.0}, 
			Content: "Point C at (0,0,1)",
		},
		{
			ID:      "point_d",
			Vector:  []float32{0.707, 0.707, 0.0},
			Content: "Point D at (0.707,0.707,0)",
		},
	}

	// Insert embeddings
	fmt.Println("\n1. Inserting embeddings...")
	for _, emb := range embeddings {
		if err := store.Upsert(ctx, &emb); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("   Inserted: %s\n", emb.Content)
	}

	// Get statistics
	fmt.Println("\n2. Store statistics:")
	stats, err := store.Stats(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   Count: %d embeddings\n", stats.Count)
	fmt.Printf("   Dimensions: %d\n", stats.Dimensions)

	// Search for vectors similar to (1,0,0)
	fmt.Println("\n3. Searching for vectors similar to (1,0,0):")
	query := []float32{1.0, 0.0, 0.0}
	results, err := store.Search(ctx, query, sqvect.SearchOptions{
		TopK: 3,
	})
	if err != nil {
		log.Fatal(err)
	}

	for i, result := range results {
		fmt.Printf("   %d. %s (similarity: %.4f)\n", 
			i+1, result.Content, result.Score)
	}

	// Search for vectors similar to (0.5, 0.5, 0)
	fmt.Println("\n4. Searching for vectors similar to (0.5,0.5,0):")
	query2 := []float32{0.5, 0.5, 0.0}
	results2, err := store.Search(ctx, query2, sqvect.SearchOptions{
		TopK: 3,
	})
	if err != nil {
		log.Fatal(err)
	}

	for i, result := range results2 {
		fmt.Printf("   %d. %s (similarity: %.4f)\n",
			i+1, result.Content, result.Score)
	}

	fmt.Println("\n✓ Basic example completed!")
}