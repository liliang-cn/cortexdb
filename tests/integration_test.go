package sqvect_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/liliang-cn/sqvect"
)

// TestIntegrationFullWorkflow tests a complete end-to-end workflow
func TestIntegrationFullWorkflow(t *testing.T) {
	// Create temporary database
	dbPath := "integration_test_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	// Test with different configurations
	configs := []struct {
		name         string
		vectorDim    int
		similarityFn sqvect.SimilarityFunc
	}{
		{"cosine_similarity", 128, sqvect.CosineSimilarity},
		{"dot_product", 128, sqvect.DotProduct},
		{"euclidean_distance", 128, sqvect.EuclideanDist},
	}

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			testConfig := sqvect.DefaultConfig()
			testConfig.Path = cfg.name + "_" + dbPath
			testConfig.VectorDim = cfg.vectorDim
			testConfig.SimilarityFn = cfg.similarityFn
			testConfig.BatchSize = 50

			defer func() {
				if err := os.Remove(testConfig.Path); err != nil {
					// Ignore cleanup errors in tests
					_ = err
				}
			}()

			store, err := sqvect.NewWithConfig(testConfig)
			if err != nil {
				t.Fatalf("Failed to create store: %v", err)
			}
			defer func() {
				if err := store.Close(); err != nil {
					t.Logf("Warning: failed to close store: %v", err)
				}
			}()

			ctx := context.Background()

			// Initialize
			if err := store.Init(ctx); err != nil {
				t.Fatalf("Failed to initialize store: %v", err)
			}

			// Create test data
			embeddings := createTestEmbeddings(cfg.vectorDim, 100)

			// Test batch insert
			// Convert slice to pointers for batch upsert
			embPtrs := make([]*sqvect.Embedding, len(embeddings))
			for i := range embeddings {
				embPtrs[i] = &embeddings[i]
			}
			if err := store.UpsertBatch(ctx, embPtrs); err != nil {
				t.Fatalf("Failed to batch upsert: %v", err)
			}

			// Verify count
			stats, err := store.Stats(ctx)
			if err != nil {
				t.Fatalf("Failed to get stats: %v", err)
			}
			if stats.Count != 100 {
				t.Errorf("Expected 100 embeddings, got %d", stats.Count)
			}

			// Test search
			query := embeddings[0].Vector
			results, err := store.Search(ctx, query, sqvect.SearchOptions{
				TopK: 10,
			})
			if err != nil {
				t.Fatalf("Failed to search: %v", err)
			}

			if len(results) == 0 {
				t.Error("Expected search results, got none")
			}

			// Test filtering
			filteredResults, err := store.Search(ctx, query, sqvect.SearchOptions{
				TopK: 5,
				Filter: map[string]string{
					"doc_id": "doc_1",
				},
			})
			if err != nil {
				t.Fatalf("Failed to search with filter: %v", err)
			}

			// Should have fewer results due to filtering
			if len(filteredResults) > len(results) {
				t.Error("Filtered results should not exceed unfiltered results")
			}

			// Test deletion
			if err := store.Delete(ctx, embeddings[0].ID); err != nil {
				t.Fatalf("Failed to delete embedding: %v", err)
			}

			// Test delete by doc ID
			if err := store.DeleteByDocID(ctx, "doc_2"); err != nil {
				t.Fatalf("Failed to delete by doc ID: %v", err)
			}

			// Verify deletion
			finalStats, err := store.Stats(ctx)
			if err != nil {
				t.Fatalf("Failed to get final stats: %v", err)
			}

			if finalStats.Count >= stats.Count {
				t.Error("Expected fewer embeddings after deletion")
			}
		})
	}
}

// TestIntegrationConcurrency tests concurrent operations
func TestIntegrationConcurrency(t *testing.T) {
	dbPath := "concurrent_test_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	store, err := sqvect.New(dbPath, 64)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}

	// Insert initial data
	embeddings := createTestEmbeddings(64, 50)
	// Convert slice to pointers for batch upsert
	embPtrs := make([]*sqvect.Embedding, len(embeddings))
	for i := range embeddings {
		embPtrs[i] = &embeddings[i]
	}
	if err := store.UpsertBatch(ctx, embPtrs); err != nil {
		t.Fatalf("Failed to insert initial data: %v", err)
	}

	// Test concurrent reads
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			
			query := embeddings[id%len(embeddings)].Vector
			_, err := store.Search(ctx, query, sqvect.SearchOptions{TopK: 5})
			if err != nil {
				t.Errorf("Concurrent search %d failed: %v", id, err)
			}
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Test concurrent writes
	for i := 0; i < 5; i++ {
		go func(id int) {
			defer func() { done <- true }()
			
			emb := sqvect.Embedding{
				ID:      "concurrent_" + string(rune('A'+id)),
				Vector:  createRandomVector(64),
				Content: "Concurrent content",
				DocID:   "concurrent_doc",
			}
			
			if err := store.Upsert(ctx, &emb); err != nil {
				t.Errorf("Concurrent upsert %d failed: %v", id, err)
			}
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 5; i++ {
		<-done
	}

	// Verify final state
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get final stats: %v", err)
	}

	if stats.Count < 50 {
		t.Errorf("Expected at least 50 embeddings, got %d", stats.Count)
	}
}

// TestIntegrationLargeDataset tests performance with larger datasets
func TestIntegrationLargeDataset(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large dataset test in short mode")
	}

	dbPath := "large_test_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	store, err := sqvect.New(dbPath, 768)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}

	// Insert 10K embeddings
	const batchSize = 500
	const totalCount = 10000

	for i := 0; i < totalCount; i += batchSize {
		end := i + batchSize
		if end > totalCount {
			end = totalCount
		}

		embeddings := createTestEmbeddings(768, end-i)
		for j := range embeddings {
			embeddings[j].ID = embeddings[j].ID + "_batch_" + string(rune(i/batchSize+'A'))
		}

		// Convert slice to pointers for batch upsert
		embPtrs := make([]*sqvect.Embedding, len(embeddings))
		for j := range embeddings {
			embPtrs[j] = &embeddings[j]
		}

		if err := store.UpsertBatch(ctx, embPtrs); err != nil {
			t.Fatalf("Failed to insert batch %d: %v", i/batchSize, err)
		}
	}

	// Verify count
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.Count != totalCount {
		t.Errorf("Expected %d embeddings, got %d", totalCount, stats.Count)
	}

	// Test search performance
	query := createRandomVector(768)
	start := time.Now()
	results, err := store.Search(ctx, query, sqvect.SearchOptions{TopK: 100})
	searchTime := time.Since(start)

	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}

	if len(results) != 100 {
		t.Errorf("Expected 100 results, got %d", len(results))
	}

	// Search should complete within reasonable time (adjust as needed)
	if searchTime > time.Second*5 {
		t.Errorf("Search took too long: %v", searchTime)
	}

	t.Logf("Search of %d embeddings took %v", totalCount, searchTime)
	t.Logf("Database size: %d bytes", stats.Size)
}

// Helper functions
func createTestEmbeddings(dim, count int) []sqvect.Embedding {
	embeddings := make([]sqvect.Embedding, count)
	
	for i := 0; i < count; i++ {
		docID := "doc_" + string(rune((i%5)+1+'0'))
		embeddings[i] = sqvect.Embedding{
			ID:      "test_emb_" + string(rune(i+'A')),
			Vector:  createRandomVector(dim),
			Content: "Test content " + string(rune(i+'A')),
			DocID:   docID,
			Metadata: map[string]string{
				"type":  "test",
				"batch": string(rune((i/10)+'A')),
				"index": string(rune(i+'0')),
			},
		}
	}
	
	return embeddings
}

func createRandomVector(dim int) []float32 {
	vector := make([]float32, dim)
	for i := range vector {
		vector[i] = float32((i+1)*dim) * 0.001 // Simple deterministic "random" values
	}
	
	// Normalize to unit length for better similarity testing
	var norm float32
	for _, v := range vector {
		norm += v * v
	}
	norm = 1.0 / norm
	for i := range vector {
		vector[i] *= norm
	}
	
	return vector
}