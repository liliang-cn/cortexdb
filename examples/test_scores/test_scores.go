package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/sqvect"
)

func main() {
	// Create a temporary test database
	dbPath := "test_scores.db"
	defer os.Remove(dbPath)

	// Create store with cosine similarity
	store, err := sqvect.New(dbPath, 3)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	
	// Initialize the store
	if err := store.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// Test vectors
	vectors := []struct {
		id     string
		vector []float32
		desc   string
	}{
		{"exact", []float32{1.0, 2.0, 3.0}, "Exact match"},
		{"similar", []float32{1.1, 2.1, 3.1}, "Very similar"},
		{"orthogonal", []float32{0.0, 0.0, 1.0}, "Somewhat orthogonal"},
		{"opposite", []float32{-1.0, -2.0, -3.0}, "Opposite direction"},
	}

	// Insert vectors
	for _, v := range vectors {
		err := store.Upsert(ctx, &sqvect.Embedding{
			ID:       v.id,
			Vector:   v.vector,
			Content:  v.desc,
			Metadata: map[string]string{},
		})
		if err != nil {
			log.Printf("Failed to insert %s: %v", v.id, err)
		}
	}

	// Search with the query vector (same as "exact")
	query := []float32{1.0, 2.0, 3.0}
	results, err := store.Search(ctx, query, sqvect.SearchOptions{TopK: 10})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Search Results (query: [1.0, 2.0, 3.0]):")
	fmt.Println("==========================================")
	for i, result := range results {
		fmt.Printf("%d. ID: %-12s Score: %.6f  Content: %s\n", 
			i+1, result.ID, result.Score, result.Content)
	}

	// Verify perfect match has score ~1.0
	if len(results) > 0 && results[0].ID == "exact" {
		if results[0].Score >= 0.999999 {
			fmt.Println("\n✓ Perfect match score is correct!")
		} else {
			fmt.Printf("\n✗ Perfect match score is too low: %.6f (expected ~1.0)\n", results[0].Score)
		}
	}
}