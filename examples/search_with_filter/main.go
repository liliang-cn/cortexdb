package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/sqvect"
)

func main() {
	// Clean up any existing database
	dbPath := "search_filter_example.db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors
			_ = err
		}
	}()

	// Create a new SQLite vector store
	store, err := sqvect.New(dbPath, 3)
	if err != nil {
		log.Fatalf("Failed to create store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("Failed to close store: %v", err)
		}
	}()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}

	// Insert sample embeddings with different metadata
	embeddings := []*sqvect.Embedding{
		{
			ID:      "doc1",
			Vector:  []float32{1.0, 0.0, 0.0},
			Content: "Python tutorial for beginners",
			Metadata: map[string]string{
				"category":   "tutorial",
				"language":   "python",
				"difficulty": "beginner",
				"published":  "true",
			},
		},
		{
			ID:      "doc2",
			Vector:  []float32{0.0, 1.0, 0.0},
			Content: "Advanced Go programming",
			Metadata: map[string]string{
				"category":   "tutorial",
				"language":   "go",
				"difficulty": "advanced",
				"published":  "true",
			},
		},
		{
			ID:      "doc3",
			Vector:  []float32{0.0, 0.0, 1.0},
			Content: "JavaScript basics draft",
			Metadata: map[string]string{
				"category":   "tutorial",
				"language":   "javascript",
				"difficulty": "beginner",
				"published":  "false",
			},
		},
		{
			ID:      "doc4",
			Vector:  []float32{0.5, 0.5, 0.0},
			Content: "API documentation",
			Metadata: map[string]string{
				"category":   "documentation",
				"language":   "english",
				"difficulty": "intermediate",
				"published":  "true",
			},
		},
	}

	if err := store.UpsertBatch(ctx, embeddings); err != nil {
		log.Fatalf("Failed to insert embeddings: %v", err)
	}

	fmt.Println("=== SearchWithFilter Examples ===")
	fmt.Println()

	// Example 1: Filter by category
	fmt.Println("1. Search for tutorials:")
	query := []float32{1.0, 0.0, 0.0}
	filters := map[string]interface{}{
		"category": "tutorial",
	}

	results, err := store.SearchWithFilter(ctx, query, sqvect.SearchOptions{TopK: 10}, filters)
	if err != nil {
		log.Fatalf("Search failed: %v", err)
	}

	for _, result := range results {
		fmt.Printf("  ID: %s, Score: %.3f, Content: %s\n", result.ID, result.Score, result.Content)
		fmt.Printf("    Metadata: %+v\n", result.Metadata)
	}

	// Example 2: Filter by multiple criteria
	fmt.Println("\n2. Search for beginner tutorials:")
	filters = map[string]interface{}{
		"category":   "tutorial",
		"difficulty": "beginner",
	}

	results, err = store.SearchWithFilter(ctx, query, sqvect.SearchOptions{TopK: 10}, filters)
	if err != nil {
		log.Fatalf("Search failed: %v", err)
	}

	for _, result := range results {
		fmt.Printf("  ID: %s, Score: %.3f, Content: %s\n", result.ID, result.Score, result.Content)
		fmt.Printf("    Metadata: %+v\n", result.Metadata)
	}

	// Example 3: Filter by published status (boolean as string)
	fmt.Println("\n3. Search for published documents:")
	filters = map[string]interface{}{
		"published": "true",
	}

	results, err = store.SearchWithFilter(ctx, query, sqvect.SearchOptions{TopK: 10}, filters)
	if err != nil {
		log.Fatalf("Search failed: %v", err)
	}

	for _, result := range results {
		fmt.Printf("  ID: %s, Score: %.3f, Content: %s\n", result.ID, result.Score, result.Content)
		fmt.Printf("    Metadata: %+v\n", result.Metadata)
	}

	// Example 4: Filter by programming language
	fmt.Println("\n4. Search for Python content:")
	filters = map[string]interface{}{
		"language": "python",
	}

	results, err = store.SearchWithFilter(ctx, query, sqvect.SearchOptions{TopK: 10}, filters)
	if err != nil {
		log.Fatalf("Search failed: %v", err)
	}

	for _, result := range results {
		fmt.Printf("  ID: %s, Score: %.3f, Content: %s\n", result.ID, result.Score, result.Content)
		fmt.Printf("    Metadata: %+v\n", result.Metadata)
	}

	// Example 5: Complex filter with no results
	fmt.Println("\n5. Search for unpublished Go tutorials:")
	filters = map[string]interface{}{
		"language":  "go",
		"published": "false",
	}

	results, err = store.SearchWithFilter(ctx, query, sqvect.SearchOptions{TopK: 10}, filters)
	if err != nil {
		log.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		fmt.Println("  No results found matching the criteria")
	} else {
		for _, result := range results {
			fmt.Printf("  ID: %s, Score: %.3f, Content: %s\n", result.ID, result.Score, result.Content)
			fmt.Printf("    Metadata: %+v\n", result.Metadata)
		}
	}

	fmt.Println("\n=== Example completed successfully! ===")
}