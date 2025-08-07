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
	fmt.Println("Advanced sqvect example")
	fmt.Println("======================")

	// Advanced configuration
	config := sqvect.Config{
		Path:         "advanced.db", 
		VectorDim:    128,
		MaxConns:     5,
		BatchSize:    100,
		SimilarityFn: sqvect.CosineSimilarity,
	}

	store, err := sqvect.NewWithConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("Warning: failed to close store: %v", err)
		}
	}()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// Generate random high-dimensional vectors
	fmt.Println("\n1. Generating 1000 random 128-dimensional vectors...")
	embeddings := generateRandomEmbeddings(128, 1000)

	// Batch insert for efficiency
	fmt.Println("2. Batch inserting embeddings...")
	start := time.Now()
	// Convert slice to pointers for batch upsert
	embPtrs := make([]*sqvect.Embedding, len(embeddings))
	for i := range embeddings {
		embPtrs[i] = &embeddings[i]
	}
	if err := store.UpsertBatch(ctx, embPtrs); err != nil {
		log.Fatal(err)
	}
	insertDuration := time.Since(start)
	fmt.Printf("   Inserted 1000 embeddings in %v (%.0f ops/sec)\n", 
		insertDuration, 1000/insertDuration.Seconds())

	// Advanced search with filtering
	fmt.Println("\n3. Advanced search with metadata filtering...")
	query := embeddings[0].Vector
	
	results, err := store.Search(ctx, query, sqvect.SearchOptions{
		TopK:      5,
		Threshold: 0.5,
		Filter: map[string]string{
			"category": "technical",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	
	fmt.Printf("   Found %d results with category=technical and similarity > 0.5:\n", len(results))
	for i, result := range results {
		fmt.Printf("   %d. %s (similarity: %.4f)\n",
			i+1, result.Content[:50]+"...", result.Score)
	}

	// Compare similarity functions
	fmt.Println("\n4. Comparing similarity functions...")
	vec1 := embeddings[0].Vector
	vec2 := embeddings[1].Vector
	
	cosine := sqvect.CosineSimilarity(vec1, vec2)
	dotProd := sqvect.DotProduct(vec1, vec2)
	euclidean := sqvect.EuclideanDist(vec1, vec2)
	
	fmt.Printf("   Cosine similarity: %.6f\n", cosine)
	fmt.Printf("   Dot product: %.6f\n", dotProd)
	fmt.Printf("   Euclidean distance: %.6f\n", euclidean)

	// Benchmark search performance
	fmt.Println("\n5. Benchmarking search performance...")
	searchStart := time.Now()
	for i := 0; i < 100; i++ {
		testQuery := embeddings[i%10].Vector
		_, err := store.Search(ctx, testQuery, sqvect.SearchOptions{TopK: 10})
		if err != nil {
			log.Fatal(err)
		}
	}
	searchDuration := time.Since(searchStart)
	fmt.Printf("   100 searches completed in %v (%.0f searches/sec)\n",
		searchDuration, 100/searchDuration.Seconds())

	// Cleanup by category
	fmt.Println("\n6. Deleting technical documents...")
	deletedCount := 0
	for _, emb := range embeddings {
		if emb.Metadata["category"] == "technical" {
			if err := store.Delete(ctx, emb.ID); err == nil {
				deletedCount++
			}
		}
	}
	fmt.Printf("   Deleted %d technical documents\n", deletedCount)

	// Final statistics
	fmt.Println("\n7. Final statistics:")
	finalStats, err := store.Stats(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   Remaining embeddings: %d\n", finalStats.Count)
	fmt.Printf("   Database size: %.2f KB\n", float64(finalStats.Size)/1024)

	fmt.Println("\n✓ Advanced example completed!")
}

func generateRandomEmbeddings(dim, count int) []sqvect.Embedding {
	rand.Seed(time.Now().UnixNano())
	
	categories := []string{"technical", "business", "research", "general"}
	topics := []string{"machine learning", "database design", "web development", 
		"data science", "cloud computing", "artificial intelligence"}

	embeddings := make([]sqvect.Embedding, count)
	
	for i := 0; i < count; i++ {
		// Generate normalized random vector
		vector := make([]float32, dim)
		var norm float32
		for j := 0; j < dim; j++ {
			vector[j] = rand.Float32()*2 - 1 // Random between -1 and 1
			norm += vector[j] * vector[j]
		}
		
		// Normalize to unit length
		norm = float32(1.0) / float32(norm)
		for j := 0; j < dim; j++ {
			vector[j] *= norm
		}
		
		embeddings[i] = sqvect.Embedding{
			ID:      fmt.Sprintf("doc_%04d", i),
			Vector:  vector,
			Content: fmt.Sprintf("Document %d about %s in the field of %s technology", 
				i, topics[i%len(topics)], categories[i%len(categories)]),
			DocID:   fmt.Sprintf("collection_%d", i/100),
			Metadata: map[string]string{
				"category":  categories[i%len(categories)],
				"topic":     topics[i%len(topics)],
				"timestamp": time.Now().Add(-time.Duration(i)*time.Hour).Format(time.RFC3339),
				"author":    fmt.Sprintf("author_%d", (i%10)+1),
			},
		}
	}
	
	return embeddings
}