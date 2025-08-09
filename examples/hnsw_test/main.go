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
	fmt.Println("Testing HNSW Integration")
	fmt.Println("========================")

	// Test with HNSW enabled
	testWithHNSW()
	
	// Test with HNSW disabled (fallback to linear search)
	testWithoutHNSW()
}

func testWithHNSW() {
	fmt.Println("\n--- Testing with HNSW enabled ---")
	
	config := sqvect.DefaultConfig()
	config.Path = "test_hnsw.db"
	config.VectorDim = 384
	config.HNSW.Enabled = true
	config.HNSW.M = 16
	config.HNSW.EfConstruction = 200
	config.HNSW.EfSearch = 50
	
	store, err := sqvect.NewWithConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// Generate and insert test vectors
	fmt.Print("  Inserting 1000 test vectors... ")
	start := time.Now()
	for i := 0; i < 1000; i++ {
		emb := &sqvect.Embedding{
			ID:      fmt.Sprintf("test_%d", i),
			Vector:  generateRandomVector(384),
			Content: fmt.Sprintf("Test document %d", i),
			DocID:   fmt.Sprintf("doc_%d", i/100),
			Metadata: map[string]string{
				"type": "test",
			},
		}
		if err := store.Upsert(ctx, emb); err != nil {
			log.Fatal(err)
		}
	}
	insertTime := time.Since(start)
	fmt.Printf("%.2fs\n", insertTime.Seconds())

	// Test search performance
	fmt.Print("  Performing 100 search queries... ")
	query := generateRandomVector(384)
	start = time.Now()
	
	for i := 0; i < 100; i++ {
		results, err := store.Search(ctx, query, sqvect.SearchOptions{
			TopK: 10,
		})
		if err != nil {
			log.Fatal(err)
		}
		if len(results) == 0 {
			fmt.Printf("Warning: No results for query %d\n", i)
		}
	}
	
	searchTime := time.Since(start)
	searchRate := 100.0 / searchTime.Seconds()
	fmt.Printf("%.2fs (%.0f queries/sec)\n", searchTime.Seconds(), searchRate)

	// Get stats
	stats, err := store.Stats(ctx)
	if err != nil {
		log.Fatal(err)
	}
	
	fmt.Printf("  Database size: %.2f MB\n", float64(stats.Size)/(1024*1024))
	fmt.Printf("  Total vectors: %d\n", stats.Count)
}

func testWithoutHNSW() {
	fmt.Println("\n--- Testing with HNSW disabled (linear search) ---")
	
	config := sqvect.DefaultConfig()
	config.Path = "test_linear.db"
	config.VectorDim = 384
	config.HNSW.Enabled = false // Disabled for comparison
	
	store, err := sqvect.NewWithConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// Generate and insert test vectors
	fmt.Print("  Inserting 1000 test vectors... ")
	start := time.Now()
	for i := 0; i < 1000; i++ {
		emb := &sqvect.Embedding{
			ID:      fmt.Sprintf("test_%d", i),
			Vector:  generateRandomVector(384),
			Content: fmt.Sprintf("Test document %d", i),
			DocID:   fmt.Sprintf("doc_%d", i/100),
			Metadata: map[string]string{
				"type": "test",
			},
		}
		if err := store.Upsert(ctx, emb); err != nil {
			log.Fatal(err)
		}
	}
	insertTime := time.Since(start)
	fmt.Printf("%.2fs\n", insertTime.Seconds())

	// Test search performance
	fmt.Print("  Performing 100 search queries... ")
	query := generateRandomVector(384)
	start = time.Now()
	
	for i := 0; i < 100; i++ {
		results, err := store.Search(ctx, query, sqvect.SearchOptions{
			TopK: 10,
		})
		if err != nil {
			log.Fatal(err)
		}
		if len(results) == 0 {
			fmt.Printf("Warning: No results for query %d\n", i)
		}
	}
	
	searchTime := time.Since(start)
	searchRate := 100.0 / searchTime.Seconds()
	fmt.Printf("%.2fs (%.0f queries/sec)\n", searchTime.Seconds(), searchRate)
}

func generateRandomVector(dim int) []float32 {
	vector := make([]float32, dim)
	var norm float32
	
	for i := 0; i < dim; i++ {
		vector[i] = rand.Float32()*2 - 1
		norm += vector[i] * vector[i]
	}
	
	// Normalize to unit length
	norm = 1.0 / float32(sqrt64(float64(norm)))
	for i := 0; i < dim; i++ {
		vector[i] *= norm
	}
	
	return vector
}

func sqrt64(x float64) float64 {
	if x < 0 {
		return 0
	}
	
	// Newton's method for square root
	guess := x
	for i := 0; i < 10; i++ {
		guess = 0.5 * (guess + x/guess)
	}
	return guess
}