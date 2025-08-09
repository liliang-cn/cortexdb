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
	fmt.Println("sqvect Benchmark Suite")
	fmt.Println("======================")

	// Test configurations
	configs := []struct {
		name      string
		dim       int
		count     int
		batchSize int
	}{
		{"Small vectors", 64, 1000, 100},
		{"Medium vectors", 384, 5000, 500}, 
		{"Large vectors", 768, 10000, 1000},
		{"XL vectors", 1536, 20000, 2000},
	}

	for _, cfg := range configs {
		fmt.Printf("\n%s (%dd, %d vectors)\n", cfg.name, cfg.dim, cfg.count)
		fmt.Println(repeat("-", len(cfg.name)+20))
		
		fmt.Println("  Linear Search (Original):")
		benchmarkConfiguration(cfg.dim, cfg.count, cfg.batchSize, false)
		
		fmt.Println("  HNSW Search (Optimized):")
		benchmarkConfiguration(cfg.dim, cfg.count, cfg.batchSize, true)
	}

	// Similarity function benchmarks
	fmt.Println("\nSimilarity Function Benchmarks")
	fmt.Println("==============================")
	benchmarkSimilarityFunctions()

	fmt.Println("\n✓ Benchmark suite completed!")
}

func benchmarkConfiguration(dim, count, batchSize int, enableHNSW bool) {
	suffix := "linear"
	if enableHNSW {
		suffix = "hnsw"
	}
	dbPath := fmt.Sprintf("benchmark_%s_%dd_%d.db", suffix, dim, count)
	
	var store *sqvect.SQLiteStore
	var err error
	
	if enableHNSW {
		config := sqvect.DefaultConfig()
		config.Path = dbPath
		config.VectorDim = dim
		config.HNSW.Enabled = true
		config.HNSW.M = 16
		config.HNSW.EfConstruction = 200
		config.HNSW.EfSearch = 50
		store, err = sqvect.NewWithConfig(config)
	} else {
		store, err = sqvect.New(dbPath, dim)
	}
	
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

	// Generate test data
	fmt.Print("  Generating test data... ")
	start := time.Now()
	embeddings := generateTestEmbeddings(dim, count)
	genDuration := time.Since(start)
	fmt.Printf("%.2fs\n", genDuration.Seconds())

	// Benchmark batch insert
	fmt.Print("  Batch insert... ")
	start = time.Now()
	
	for i := 0; i < count; i += batchSize {
		end := i + batchSize
		if end > count {
			end = count
		}
		
		// Convert slice to pointers for batch upsert
		batch := make([]*sqvect.Embedding, end-i)
		for j := i; j < end; j++ {
			batch[j-i] = &embeddings[j]
		}
		
		if err := store.UpsertBatch(ctx, batch); err != nil {
			log.Fatal(err)
		}
	}
	
	insertDuration := time.Since(start)
	insertRate := float64(count) / insertDuration.Seconds()
	fmt.Printf("%.2fs (%.0f ops/sec)\n", insertDuration.Seconds(), insertRate)

	// Benchmark single inserts
	fmt.Print("  Single insert (100 samples)... ")
	start = time.Now()
	for i := 0; i < 100; i++ {
		emb := generateTestEmbeddings(dim, 1)[0]
		emb.ID = fmt.Sprintf("single_%d", i)
		if err := store.Upsert(ctx, &emb); err != nil {
			log.Fatal(err)
		}
	}
	singleDuration := time.Since(start)
	singleRate := 100.0 / singleDuration.Seconds()
	fmt.Printf("%.2fs (%.0f ops/sec)\n", singleDuration.Seconds(), singleRate)

	// Benchmark search
	fmt.Print("  Search (1000 queries)... ")
	start = time.Now()
	for i := 0; i < 1000; i++ {
		query := embeddings[i%100].Vector
		_, err := store.Search(ctx, query, sqvect.SearchOptions{TopK: 10})
		if err != nil {
			log.Fatal(err)
		}
	}
	searchDuration := time.Since(start)
	searchRate := 1000.0 / searchDuration.Seconds()
	fmt.Printf("%.2fs (%.0f searches/sec)\n", searchDuration.Seconds(), searchRate)

	// Memory usage
	stats, err := store.Stats(ctx)
	if err != nil {
		log.Fatal(err)
	}
	
	fmt.Printf("  Database size: %.2f MB\n", float64(stats.Size)/(1024*1024))
	fmt.Printf("  Avg bytes per vector: %.0f\n", float64(stats.Size)/float64(stats.Count))
}

func benchmarkSimilarityFunctions() {
	dimensions := []int{64, 128, 384, 768, 1536}
	iterations := 100000
	
	for _, dim := range dimensions {
		fmt.Printf("\n%d dimensions (%d iterations):\n", dim, iterations)
		
		// Generate test vectors
		vec1 := generateRandomVector(dim)
		vec2 := generateRandomVector(dim)
		
		// Benchmark cosine similarity
		start := time.Now()
		for i := 0; i < iterations; i++ {
			_ = sqvect.CosineSimilarity(vec1, vec2)
		}
		cosineDuration := time.Since(start)
		cosineRate := float64(iterations) / cosineDuration.Seconds()
		
		// Benchmark dot product
		start = time.Now()
		for i := 0; i < iterations; i++ {
			_ = sqvect.DotProduct(vec1, vec2)
		}
		dotDuration := time.Since(start)
		dotRate := float64(iterations) / dotDuration.Seconds()
		
		// Benchmark Euclidean distance
		start = time.Now()
		for i := 0; i < iterations; i++ {
			_ = sqvect.EuclideanDist(vec1, vec2)
		}
		euclideanDuration := time.Since(start)
		euclideanRate := float64(iterations) / euclideanDuration.Seconds()
		
		fmt.Printf("  Cosine:    %.2fs (%.0fK ops/sec)\n", 
			cosineDuration.Seconds(), cosineRate/1000)
		fmt.Printf("  Dot:       %.2fs (%.0fK ops/sec)\n", 
			dotDuration.Seconds(), dotRate/1000)
		fmt.Printf("  Euclidean: %.2fs (%.0fK ops/sec)\n", 
			euclideanDuration.Seconds(), euclideanRate/1000)
	}
}

func generateTestEmbeddings(dim, count int) []sqvect.Embedding {
	embeddings := make([]sqvect.Embedding, count)
	
	for i := 0; i < count; i++ {
		embeddings[i] = sqvect.Embedding{
			ID:      fmt.Sprintf("bench_%d", i),
			Vector:  generateRandomVector(dim),
			Content: fmt.Sprintf("Benchmark document %d", i),
			DocID:   fmt.Sprintf("collection_%d", i/1000),
			Metadata: map[string]string{
				"type":  "benchmark",
				"batch": fmt.Sprintf("%d", i/100),
			},
		}
	}
	
	return embeddings
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

// strings.Repeat equivalent
func repeat(s string, count int) string {
	if count <= 0 {
		return ""
	}
	result := make([]byte, 0, len(s)*count)
	for i := 0; i < count; i++ {
		result = append(result, s...)
	}
	return string(result)
}