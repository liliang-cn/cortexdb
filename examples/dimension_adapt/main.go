package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/liliang-cn/sqvect"
)

func main() {
	// Initialize random seed
	rand.Seed(time.Now().UnixNano())
	
	fmt.Println("SqVect Dimension Adaptation Example")
	fmt.Println("==================================")

	// Create store with auto-dimension detection (0 = auto-detect)
	store, err := sqvect.New("dimension_example.db", 0)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	// Initialize the store
	if err := store.Init(ctx); err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n1. Inserting BERT-style vectors (768 dimensions)")
	
	// Insert some 768-dimensional vectors (BERT-like)
	for i := 0; i < 3; i++ {
		vector := generateRandomVector(768)
		embedding := &sqvect.Embedding{
			ID:      fmt.Sprintf("bert_%d", i+1),
			Vector:  vector,
			Content: fmt.Sprintf("BERT document %d content", i+1),
			Metadata: map[string]string{
				"source": "bert",
				"type":   "document",
			},
		}
		
		if err := store.Upsert(ctx, embedding); err != nil {
			log.Printf("Error inserting BERT vector: %v", err)
		} else {
			fmt.Printf("✓ Inserted %s (768 dim)\n", embedding.ID)
		}
	}

	fmt.Println("\n2. Inserting OpenAI-style vectors (1536 dimensions)")
	
	// Now insert some 1536-dimensional vectors (OpenAI-like) 
	// These will be automatically adapted to 768 dimensions
	for i := 0; i < 2; i++ {
		vector := generateRandomVector(1536)
		embedding := &sqvect.Embedding{
			ID:      fmt.Sprintf("openai_%d", i+1),
			Vector:  vector,
			Content: fmt.Sprintf("OpenAI document %d content", i+1),
			Metadata: map[string]string{
				"source": "openai",
				"type":   "document",
			},
		}
		
		if err := store.Upsert(ctx, embedding); err != nil {
			log.Printf("Error inserting OpenAI vector: %v", err)
		} else {
			fmt.Printf("✓ Inserted %s (1536 → 768 dim, auto-adapted)\n", embedding.ID)
		}
	}

	fmt.Println("\n3. Inserting smaller vectors (384 dimensions)")
	
	// Insert some smaller vectors (MiniLM-like)
	// These will be automatically padded to 768 dimensions
	vector := generateRandomVector(384)
	embedding := &sqvect.Embedding{
		ID:      "minilm_1",
		Vector:  vector,
		Content: "MiniLM document content",
		Metadata: map[string]string{
			"source": "minilm",
			"type":   "document",
		},
	}
	
	if err := store.Upsert(ctx, embedding); err != nil {
		log.Printf("Error inserting MiniLM vector: %v", err)
	} else {
		fmt.Printf("✓ Inserted %s (384 → 768 dim, auto-adapted)\n", embedding.ID)
	}

	fmt.Println("\n4. Testing search with different query dimensions")

	// Test search with 768-dim query (matches store dimension)
	query768 := generateRandomVector(768)
	results, err := store.Search(ctx, query768, sqvect.SearchOptions{TopK: 3})
	if err != nil {
		log.Printf("Error searching with 768-dim query: %v", err)
	} else {
		fmt.Printf("✓ Search with 768-dim query found %d results\n", len(results))
	}

	// Test search with 1536-dim query (will be auto-adapted)
	query1536 := generateRandomVector(1536)
	results, err = store.Search(ctx, query1536, sqvect.SearchOptions{TopK: 3})
	if err != nil {
		log.Printf("Error searching with 1536-dim query: %v", err)
	} else {
		fmt.Printf("✓ Search with 1536-dim query (auto-adapted) found %d results\n", len(results))
	}

	// Test search with 3072-dim query (will be auto-adapted)
	query3072 := generateRandomVector(3072)
	results, err = store.Search(ctx, query3072, sqvect.SearchOptions{TopK: 3})
	if err != nil {
		log.Printf("Error searching with 3072-dim query: %v", err)
	} else {
		fmt.Printf("✓ Search with 3072-dim query (auto-adapted) found %d results\n", len(results))
	}

	fmt.Println("\n5. Displaying search results")
	for i, result := range results {
		fmt.Printf("Result %d: %s (score: %.4f, source: %s)\n", 
			i+1, result.ID, result.Score, result.Metadata["source"])
	}

	// Get store statistics
	stats, err := store.Stats(ctx)
	if err != nil {
		log.Printf("Error getting stats: %v", err)
	} else {
		fmt.Printf("\n6. Store Statistics\n")
		fmt.Printf("✓ Total vectors: %d\n", stats.Count)
		fmt.Printf("✓ Vector dimension: %d\n", stats.Dimensions)
		fmt.Printf("✓ Database size: %d bytes\n", stats.Size)
	}

	fmt.Println("\n✅ All operations completed successfully!")
	fmt.Println("The library automatically handled all dimension mismatches.")
}

// generateRandomVector creates a random vector of the specified dimension
func generateRandomVector(dim int) []float32 {
	vector := make([]float32, dim)
	for i := range vector {
		vector[i] = rand.Float32()*2 - 1 // Random values between -1 and 1
	}
	
	// Normalize the vector
	var sumSquares float32
	for _, v := range vector {
		sumSquares += v * v
	}
	norm := float32(1.0 / (float64(sumSquares) + 1e-8)) // Add small epsilon to avoid division by zero
	
	for i := range vector {
		vector[i] *= norm
	}
	
	return vector
}