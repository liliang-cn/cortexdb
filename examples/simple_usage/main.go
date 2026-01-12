package main

import (
	"context"
	"fmt"
	"log"

	"github.com/liliang-cn/sqvect/pkg/sqvect"
)

func main() {
	// Example: Simple vector database usage for AI applications
	
	// 1. Open database with simple config
	config := sqvect.DefaultConfig("embeddings.db")
	config.Dimensions = 384 // Dimension for models like all-MiniLM-L6-v2 (OpenAI is 1536)
	
	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer func() { _ = db.Close() }()
	
	ctx := context.Background()
	
	// 2. Use the Quick interface for simple operations
	quick := db.Quick()
	
	// Add some sample embeddings (simulated OpenAI embeddings)
	embeddings := []struct {
		text   string
		vector []float32
	}{
		{"The cat sat on the mat", createSampleEmbedding(384, 1)},
		{"Dogs are loyal animals", createSampleEmbedding(384, 2)},
		{"Machine learning is powerful", createSampleEmbedding(384, 3)},
		{"Artificial intelligence revolution", createSampleEmbedding(384, 4)},
		{"Natural language processing", createSampleEmbedding(384, 5)},
	}
	
	// Store embeddings
	fmt.Println("Adding embeddings...")
	for _, emb := range embeddings {
		id, err := quick.Add(ctx, emb.vector, emb.text)
		if err != nil {
			log.Printf("Failed to add embedding: %v", err)
			continue
		}
		// Store ID for reference if needed later
		_ = id
		fmt.Printf("Added: %s -> %s\n", id, emb.text)
	}
	
	// Search for similar content
	fmt.Println("\nSearching for content similar to 'animals'...")
	queryVector := createSampleEmbedding(384, 2) // Similar to "Dogs are loyal animals"
	
	results, err := quick.Search(ctx, queryVector, 3)
	if err != nil {
		log.Fatal("Search failed:", err)
	}
	
	fmt.Printf("Found %d similar results:\n", len(results))
	for i, result := range results {
		fmt.Printf("%d. %s (score: %.4f) - %s\n", 
			i+1, result.ID, result.Score, result.Content)
	}
	
	// Example with collections for multi-tenant applications
	fmt.Println("\n--- Collections Example ---")
	
	// Create collections for different document types
	_, err = db.Vector().CreateCollection(ctx, "documents", 384)
	if err != nil {
		log.Printf("Collection might already exist: %v", err)
	}
	
	_, err = db.Vector().CreateCollection(ctx, "code", 384)
	if err != nil {
		log.Printf("Collection might already exist: %v", err)
	}
	
	// Add to specific collections
	docID, _ := quick.AddToCollection(ctx, "documents", createSampleEmbedding(384, 10), "Important business document")
	codeID, _ := quick.AddToCollection(ctx, "code", createSampleEmbedding(384, 11), "func main() { fmt.Println(\"Hello\") }")
	
	fmt.Printf("Added to documents: %s\n", docID)
	fmt.Printf("Added to code: %s\n", codeID)
	
	// Search within specific collection
	docResults, _ := quick.SearchInCollection(ctx, "documents", createSampleEmbedding(384, 10), 5)
	fmt.Printf("Documents collection has %d items\n", len(docResults))
	
	// Show library stats
	stats, err := db.Vector().Stats(ctx)
	if err == nil {
		fmt.Printf("\nDatabase stats: %d embeddings, %d dimensions\n", 
			stats.Count, stats.Dimensions)
	}
}

// createSampleEmbedding creates a sample vector for demonstration
// In real applications, you'd get this from your embedding model (OpenAI, HuggingFace, etc.)
func createSampleEmbedding(dim int, seed int) []float32 {
	vector := make([]float32, dim)
	for i := 0; i < dim; i++ {
		// Simple deterministic "embedding" for testing
		vector[i] = float32(seed*i%100) / 100.0
	}
	// Normalize vector for cosine similarity
	var norm float32
	for _, v := range vector {
		norm += v * v
	}
	norm = float32(1.0 / (1e-8 + float64(norm)))
	for i := range vector {
		vector[i] *= norm
	}
	return vector
}