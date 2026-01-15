package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/liliang-cn/sqvect/v2/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/v2/pkg/sqvect"
)

const (
	VectorDim    = 128
	NumVectors   = 5000
	NumQueries   = 100
	TopK         = 10
	NumCentroids = 50 // Roughly sqrt(NumVectors)
)

func main() {
	fmt.Println("SQLite Vector - Index Comparison Benchmark")
	fmt.Println("==========================================")
	fmt.Printf("Vectors: %d, Dimensions: %d\n", NumVectors, VectorDim)

	// Generate dataset
	fmt.Println("\nGenerating dataset...")
	vectors := make([][]float32, NumVectors)
	ids := make([]string, NumVectors)
	for i := 0; i < NumVectors; i++ {
		vectors[i] = createRandomEmbedding(VectorDim)
		ids[i] = fmt.Sprintf("vec_%d", i)
	}
	queries := make([][]float32, NumQueries)
	for i := 0; i < NumQueries; i++ {
		queries[i] = createRandomEmbedding(VectorDim)
	}

	// Benchmark Flat (Linear)
	runBenchmark("Flat (Linear)", core.IndexTypeFlat, vectors, ids, queries)

	// Benchmark HNSW
	runBenchmark("HNSW", core.IndexTypeHNSW, vectors, ids, queries)

	// Benchmark IVF
	runBenchmark("IVF", core.IndexTypeIVF, vectors, ids, queries)
}

func runBenchmark(name string, indexType core.IndexType, vectors [][]float32, ids []string, queries [][]float32) {
	fmt.Printf("\n--- %s ---\n", name)
	dbPath := fmt.Sprintf("bench_%s.db", name)
	os.Remove(dbPath)
	defer os.Remove(dbPath)

	config := sqvect.DefaultConfig(dbPath)
	config.Dimensions = VectorDim
	config.IndexType = indexType

	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	store := db.Vector()

	// 1. Insertion
	start := time.Now()
	// Batch insert for speed
	batchSize := 100
	for i := 0; i < len(vectors); i += batchSize {
		end := i + batchSize
		if end > len(vectors) {
			end = len(vectors)
		}
		
embs := make([]*core.Embedding, end-i)
		for j := 0; j < end-i; j++ {
			embs[j] = &core.Embedding{
				ID:      ids[i+j],
				Vector:  vectors[i+j],
				Content: fmt.Sprintf("Content %d", i+j),
			}
		}
		if err := store.UpsertBatch(ctx, embs); err != nil {
			log.Fatalf("Upsert failed: %v", err)
		}
	}
	duration := time.Since(start)
	fmt.Printf("Insertion time: %.2fs (%.0f ops/sec)\n", duration.Seconds(), float64(len(vectors))/duration.Seconds())

	// 2. Training (IVF only)
	if indexType == core.IndexTypeIVF {
		start = time.Now()
		fmt.Printf("Training IVF index with %d centroids...\n", NumCentroids)
		if err := store.TrainIndex(ctx, NumCentroids); err != nil {
			log.Fatalf("Training failed: %v", err)
		}
		fmt.Printf("Training time: %.2fs\n", time.Since(start).Seconds())
	}

	// 3. Search
	start = time.Now()
	foundCount := 0
	for _, query := range queries {
		results, err := store.Search(ctx, query, core.SearchOptions{TopK: TopK})
		if err != nil {
			log.Printf("Search failed: %v", err)
			continue
		}
		foundCount += len(results)
	}
	duration = time.Since(start)
	qps := float64(len(queries)) / duration.Seconds()
	avgLatency := float64(duration.Milliseconds()) / float64(len(queries))
	
	fmt.Printf("Search QPS: %.1f\n", qps)
	fmt.Printf("Avg Latency: %.2f ms\n", avgLatency)
	fmt.Printf("Avg Results: %.1f\n", float64(foundCount)/float64(len(queries)))
}

func createRandomEmbedding(dim int) []float32 {
	vector := make([]float32, dim)
	var norm float64
	for i := 0; i < dim; i++ {
		val := rand.Float32()*2.0 - 1.0
		vector[i] = val
		norm += float64(val * val)
	}
	norm = math.Sqrt(norm)
	for i := 0; i < dim; i++ {
		vector[i] /= float32(norm)
	}
	return vector
}
