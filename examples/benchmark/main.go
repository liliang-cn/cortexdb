package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/liliang-cn/sqvect/v2/v2/pkg/sqvect"
)

func main() {
	// Benchmark SQLite Vector performance for embedding use cases
	
	// Clean start
	_ = os.Remove("benchmark.db")
	
	config := sqvect.DefaultConfig("benchmark.db")
	config.Dimensions = 384 // Common embedding dimension
	
	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer func() { _ = db.Close() }()
	defer func() { _ = os.Remove("benchmark.db") }()
	
	ctx := context.Background()
	quick := db.Quick()
	
	fmt.Println("SQLite Vector Performance Benchmark")
	fmt.Println("===================================")
	
	// Benchmark 1: Insertion Performance
	fmt.Println("1. Testing insertion performance...")
	
	sizes := []int{100, 1000, 5000}
	for _, size := range sizes {
		start := time.Now()
		
		for i := 0; i < size; i++ {
			vector := createRandomEmbedding(384)
			content := fmt.Sprintf("Document %d content here", i)
			
			_, err := quick.Add(ctx, vector, content)
			if err != nil {
				log.Printf("Failed to add document %d: %v", i, err)
			}
		}
		
		duration := time.Since(start)
		rate := float64(size) / duration.Seconds()
		
		fmt.Printf("   %d embeddings: %.2fs (%.1f/sec)\n", size, duration.Seconds(), rate)
	}
	
	// Benchmark 2: Search Performance
	fmt.Println("\n2. Testing search performance...")
	
	// Get current stats
	stats, _ := db.Vector().Stats(ctx)
	fmt.Printf("   Database contains %d embeddings\n", stats.Count)
	
	searchSizes := []int{1, 10, 50, 100}
	for _, topK := range searchSizes {
		// Run multiple searches and average
		totalTime := time.Duration(0)
		searchCount := 10
		
		for i := 0; i < searchCount; i++ {
			queryVector := createRandomEmbedding(384)
			
			start := time.Now()
			results, err := quick.Search(ctx, queryVector, topK)
			duration := time.Since(start)
			
			if err != nil {
				log.Printf("Search failed: %v", err)
				continue
			}
			
			totalTime += duration
			
			// Verify we got results
			if len(results) == 0 {
				fmt.Printf("   Warning: No results for search %d\n", i)
			}
		}
		
		avgTime := totalTime / time.Duration(searchCount)
		fmt.Printf("   Top-%d search: %.2fms avg\n", topK, float64(avgTime.Nanoseconds())/1000000)
	}
	
	// Benchmark 3: Memory Usage
	fmt.Println("\n3. Memory and storage efficiency...")
	
	dbSize := stats.Size
	embeddingSize := float64(dbSize) / float64(stats.Count)
	
	fmt.Printf("   Database size: %.1f KB\n", float64(dbSize)/1024)
	fmt.Printf("   Avg per embedding: %.1f bytes\n", embeddingSize)
	fmt.Printf("   Vector dimensions: %d\n", stats.Dimensions)
	fmt.Printf("   Storage efficiency: %.1fx (vs raw float32)\n", 
		float64(stats.Dimensions*4)/embeddingSize)
	
	// Benchmark 4: Concurrent Performance
	fmt.Println("\n4. Testing concurrent operations...")
	
	concurrentSearches := 10
	start := time.Now()
	
	done := make(chan bool, concurrentSearches)
	
	for i := 0; i < concurrentSearches; i++ {
		go func(id int) {
			queryVector := createRandomEmbedding(384)
			_, err := quick.Search(ctx, queryVector, 10)
			if err != nil {
				log.Printf("Concurrent search %d failed: %v", id, err)
			}
			done <- true
		}(i)
	}
	
	// Wait for all to complete
	for i := 0; i < concurrentSearches; i++ {
		<-done
	}
	
	concurrentDuration := time.Since(start)
	concurrentRate := float64(concurrentSearches) / concurrentDuration.Seconds()
	
	fmt.Printf("   %d concurrent searches: %.2fs (%.1f/sec)\n", 
		concurrentSearches, concurrentDuration.Seconds(), concurrentRate)
	
	// Benchmark 5: Collection Performance
	fmt.Println("\n5. Testing collection performance...")
	
	// Create collections
	collections := []string{"docs", "code", "emails"}
	for _, name := range collections {
		_, err := db.Vector().CreateCollection(ctx, name, 384)
		if err != nil {
			log.Printf("Failed to create collection %s: %v", name, err)
		}
	}
	
	// Add to different collections
	start = time.Now()
	for i := 0; i < 300; i++ {
		collection := collections[i%len(collections)]
		vector := createRandomEmbedding(384)
		content := fmt.Sprintf("Content for %s collection #%d", collection, i)
		
		_, err := quick.AddToCollection(ctx, collection, vector, content)
		if err != nil {
			log.Printf("Failed to add to collection %s: %v", collection, err)
		}
	}
	collectionInsertTime := time.Since(start)
	
	// Search within collections
	start = time.Now()
	for _, collection := range collections {
		queryVector := createRandomEmbedding(384)
		_, err := quick.SearchInCollection(ctx, collection, queryVector, 10)
		if err != nil {
			log.Printf("Failed to search in collection %s: %v", collection, err)
		}
	}
	collectionSearchTime := time.Since(start)
	
	fmt.Printf("   Insert 300 items across 3 collections: %.2fs\n", collectionInsertTime.Seconds())
	fmt.Printf("   Search in 3 collections: %.2fms\n", float64(collectionSearchTime.Nanoseconds())/1000000)
	
	// Final stats
	finalStats, _ := db.Vector().Stats(ctx)
	fmt.Printf("\nFinal database stats:\n")
	fmt.Printf("   Total embeddings: %d\n", finalStats.Count)
	fmt.Printf("   Database size: %.1f KB\n", float64(finalStats.Size)/1024)
	
	collections_list, _ := db.Vector().ListCollections(ctx)
	fmt.Printf("   Collections: %d\n", len(collections_list))
	
	fmt.Println("\nBenchmark completed! 🚀")
	fmt.Println("SQLite Vector is ready for production use in your Go AI projects.")
}

func createRandomEmbedding(dim int) []float32 {
	vector := make([]float32, dim)
	for i := 0; i < dim; i++ {
		vector[i] = rand.Float32()*2.0 - 1.0 // Random values between -1 and 1
	}
	
	// Normalize for cosine similarity
	var norm float32
	for _, v := range vector {
		norm += v * v
	}
	if norm > 0 {
		norm = float32(1.0 / math.Sqrt(float64(norm)))
		for i := range vector {
			vector[i] *= norm
		}
	}
	
	return vector
}