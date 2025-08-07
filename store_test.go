package sqvect

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"
)

func TestSimilarityFunctions(t *testing.T) {
	tests := []struct {
		name     string
		vectorA  []float32
		vectorB  []float32
		expected map[string]float64
		epsilon  float64
	}{
		{
			name:    "identical vectors",
			vectorA: []float32{1.0, 0.0, 0.0},
			vectorB: []float32{1.0, 0.0, 0.0},
			expected: map[string]float64{
				"cosine": 1.0,
				"dot":    1.0,
			},
			epsilon: 1e-6,
		},
		{
			name:    "orthogonal vectors",
			vectorA: []float32{1.0, 0.0},
			vectorB: []float32{0.0, 1.0},
			expected: map[string]float64{
				"cosine": 0.0,
				"dot":    0.0,
			},
			epsilon: 1e-6,
		},
		{
			name:    "opposite vectors",
			vectorA: []float32{1.0, 0.0},
			vectorB: []float32{-1.0, 0.0},
			expected: map[string]float64{
				"cosine": -1.0,
				"dot":    -1.0,
			},
			epsilon: 1e-6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cosine := CosineSimilarity(tt.vectorA, tt.vectorB)
			if math.Abs(cosine-tt.expected["cosine"]) > tt.epsilon {
				t.Errorf("CosineSimilarity() = %v, want %v", cosine, tt.expected["cosine"])
			}

			dot := DotProduct(tt.vectorA, tt.vectorB)
			if math.Abs(dot-tt.expected["dot"]) > tt.epsilon {
				t.Errorf("DotProduct() = %v, want %v", dot, tt.expected["dot"])
			}
		})
	}
}

func TestVectorEncoding(t *testing.T) {
	tests := []struct {
		name   string
		vector []float32
	}{
		{
			name:   "simple vector",
			vector: []float32{1.0, 2.0, 3.0},
		},
		{
			name:   "empty vector",
			vector: []float32{},
		},
		{
			name:   "single element",
			vector: []float32{42.0},
		},
		{
			name:   "large vector",
			vector: make([]float32, 1000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Initialize large vector with test data
			if len(tt.vector) == 1000 {
				for i := range tt.vector {
					tt.vector[i] = float32(i) * 0.1
				}
			}

			encoded, err := encodeVector(tt.vector)
			if tt.vector == nil {
				if err == nil {
					t.Error("Expected error for nil vector")
				}
				return
			}

			if err != nil {
				t.Fatalf("encodeVector() error = %v", err)
			}

			decoded, err := decodeVector(encoded)
			if err != nil {
				t.Fatalf("decodeVector() error = %v", err)
			}

			if len(decoded) != len(tt.vector) {
				t.Errorf("Decoded vector length = %d, want %d", len(decoded), len(tt.vector))
			}

			for i, v := range decoded {
				if v != tt.vector[i] {
					t.Errorf("Decoded vector[%d] = %v, want %v", i, v, tt.vector[i])
				}
			}
		})
	}
}

func TestMetadataEncoding(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
	}{
		{
			name:     "nil metadata",
			metadata: nil,
		},
		{
			name:     "empty metadata",
			metadata: map[string]string{},
		},
		{
			name: "simple metadata",
			metadata: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		},
		{
			name: "metadata with special characters",
			metadata: map[string]string{
				"unicode": "test_unicode",
				"symbols": "!@#$%^&*()",
				"json":    `{"nested": "value"}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := encodeMetadata(tt.metadata)
			if err != nil {
				t.Fatalf("encodeMetadata() error = %v", err)
			}

			decoded, err := decodeMetadata(encoded)
			if err != nil {
				t.Fatalf("decodeMetadata() error = %v", err)
			}

			if tt.metadata == nil && decoded != nil {
				t.Error("Expected nil decoded metadata for nil input")
			}

			if tt.metadata != nil {
				if len(decoded) != len(tt.metadata) {
					t.Errorf("Decoded metadata length = %d, want %d", len(decoded), len(tt.metadata))
				}

				for k, v := range tt.metadata {
					if decoded[k] != v {
						t.Errorf("Decoded metadata[%s] = %v, want %v", k, decoded[k], v)
					}
				}
			}
		})
	}
}

func TestSQLiteStore(t *testing.T) {
	// Create temporary database file
	dbPath := "test_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	store, err := New(dbPath, 3)
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

	// Test initialization
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}

	// Test embedding validation
	validEmb := Embedding{
		ID:      "test1",
		Vector:  []float32{1.0, 2.0, 3.0},
		Content: "Test content",
		DocID:   "doc1",
		Metadata: map[string]string{
			"type": "test",
		},
	}

	// Test upsert
	if err := store.Upsert(ctx, &validEmb); err != nil {
		t.Fatalf("Failed to upsert embedding: %v", err)
	}

	// Test stats
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.Count != 1 {
		t.Errorf("Expected count = 1, got %d", stats.Count)
	}
	if stats.Dimensions != 3 {
		t.Errorf("Expected dimensions = 3, got %d", stats.Dimensions)
	}

	// Test search
	searchResults, err := store.Search(ctx, []float32{1.0, 2.0, 3.0}, SearchOptions{TopK: 10})
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}
	if len(searchResults) != 1 {
		t.Errorf("Expected 1 search result, got %d", len(searchResults))
	}
	if searchResults[0].ID != "test1" {
		t.Errorf("Expected ID = test1, got %s", searchResults[0].ID)
	}

	// The exact same vector should have perfect similarity
	if math.Abs(searchResults[0].Score-1.0) > 1e-6 {
		t.Errorf("Expected perfect similarity ~1.0, got %f", searchResults[0].Score)
	}
}

func TestBatchOperations(t *testing.T) {
	dbPath := "test_batch_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	store, err := New(dbPath, 2)
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

	// Create batch of embeddings
	embeddings := []Embedding{
		{ID: "batch1", Vector: []float32{1.0, 0.0}, Content: "Content 1", DocID: "doc1"},
		{ID: "batch2", Vector: []float32{0.0, 1.0}, Content: "Content 2", DocID: "doc1"},
		{ID: "batch3", Vector: []float32{1.0, 1.0}, Content: "Content 3", DocID: "doc2"},
	}

	// Test batch upsert
	embPtrs := make([]*Embedding, len(embeddings))
	for i := range embeddings {
		embPtrs[i] = &embeddings[i]
	}
	if err := store.UpsertBatch(ctx, embPtrs); err != nil {
		t.Fatalf("Failed to batch upsert: %v", err)
	}

	// Verify all embeddings were inserted
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.Count != 3 {
		t.Errorf("Expected count = 3, got %d", stats.Count)
	}

	// Test search with filtering
	results, err := store.Search(ctx, []float32{1.0, 0.0}, SearchOptions{
		TopK: 10,
		Filter: map[string]string{
			"doc_id": "doc1",
		},
	})
	if err != nil {
		t.Fatalf("Failed to search with filter: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("Expected 2 filtered results, got %d", len(results))
	}
}

func TestDeleteOperations(t *testing.T) {
	dbPath := "test_delete_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	store, err := New(dbPath, 2)
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

	// Insert test data
	embeddings := []Embedding{
		{ID: "del1", Vector: []float32{1.0, 0.0}, Content: "Content 1", DocID: "doc1"},
		{ID: "del2", Vector: []float32{0.0, 1.0}, Content: "Content 2", DocID: "doc1"},
		{ID: "del3", Vector: []float32{1.0, 1.0}, Content: "Content 3", DocID: "doc2"},
	}

	embPtrs := make([]*Embedding, len(embeddings))
	for i := range embeddings {
		embPtrs[i] = &embeddings[i]
	}
	if err := store.UpsertBatch(ctx, embPtrs); err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	// Test single delete
	if err := store.Delete(ctx, "del1"); err != nil {
		t.Fatalf("Failed to delete embedding: %v", err)
	}

	// Verify deletion
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.Count != 2 {
		t.Errorf("Expected count = 2 after delete, got %d", stats.Count)
	}

	// Test delete by doc ID
	if err := store.DeleteByDocID(ctx, "doc1"); err != nil {
		t.Fatalf("Failed to delete by doc ID: %v", err)
	}

	// Verify doc deletion
	stats, err = store.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.Count != 1 {
		t.Errorf("Expected count = 1 after doc delete, got %d", stats.Count)
	}

	// Test delete non-existent embedding
	err = store.Delete(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error when deleting non-existent embedding")
	}
}

func TestListDocuments(t *testing.T) {
	dbPath := "test_list_docs_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	store, err := New(dbPath, 2)
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

	// Test with empty store
	docs, err := store.ListDocuments(ctx)
	if err != nil {
		t.Fatalf("Failed to list documents: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("Expected 0 documents in empty store, got %d", len(docs))
	}

	// Insert test data with various doc IDs
	embeddings := []Embedding{
		{ID: "emb1", Vector: []float32{1.0, 0.0}, Content: "Content 1", DocID: "doc1"},
		{ID: "emb2", Vector: []float32{0.0, 1.0}, Content: "Content 2", DocID: "doc1"}, // Same doc
		{ID: "emb3", Vector: []float32{1.0, 1.0}, Content: "Content 3", DocID: "doc2"},
		{ID: "emb4", Vector: []float32{0.5, 0.5}, Content: "Content 4", DocID: "doc3"},
		{ID: "emb5", Vector: []float32{0.2, 0.8}, Content: "Content 5", DocID: ""}, // Empty doc ID
		{ID: "emb6", Vector: []float32{0.8, 0.2}, Content: "Content 6"},            // No doc ID
	}

	embPtrs := make([]*Embedding, len(embeddings))
	for i := range embeddings {
		embPtrs[i] = &embeddings[i]
	}
	if err := store.UpsertBatch(ctx, embPtrs); err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	// Test listing documents
	docs, err = store.ListDocuments(ctx)
	if err != nil {
		t.Fatalf("Failed to list documents: %v", err)
	}

	expectedDocs := []string{"doc1", "doc2", "doc3"}
	if len(docs) != len(expectedDocs) {
		t.Errorf("Expected %d unique documents, got %d", len(expectedDocs), len(docs))
	}

	// Check if all expected docs are present and sorted
	for i, expectedDoc := range expectedDocs {
		if i >= len(docs) || docs[i] != expectedDoc {
			t.Errorf("Expected doc[%d] = %s, got %s", i, expectedDoc, docs[i])
		}
	}

	// Test after deleting a document
	if err := store.DeleteByDocID(ctx, "doc2"); err != nil {
		t.Fatalf("Failed to delete doc2: %v", err)
	}

	docs, err = store.ListDocuments(ctx)
	if err != nil {
		t.Fatalf("Failed to list documents after deletion: %v", err)
	}

	expectedDocsAfterDelete := []string{"doc1", "doc3"}
	if len(docs) != len(expectedDocsAfterDelete) {
		t.Errorf("Expected %d unique documents after delete, got %d", len(expectedDocsAfterDelete), len(docs))
	}

	for i, expectedDoc := range expectedDocsAfterDelete {
		if i >= len(docs) || docs[i] != expectedDoc {
			t.Errorf("Expected doc[%d] = %s after delete, got %s", i, expectedDoc, docs[i])
		}
	}
}

func TestErrorHandling(t *testing.T) {
	// Test invalid configuration
	_, err := New("", 128)
	if err == nil {
		t.Error("Expected error for empty database path")
	}

	_, err = New("test.db", 0)
	if err == nil {
		t.Error("Expected error for zero vector dimension")
	}

	_, err = New("test.db", -1)
	if err == nil {
		t.Error("Expected error for negative vector dimension")
	}

	// Test operations on closed store
	dbPath := "test_error_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	store, err := New(dbPath, 3)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}

	// Close the store
	if err := store.Close(); err != nil {
		t.Fatalf("Failed to close store: %v", err)
	}

	// Test operations on closed store
	emb := Embedding{ID: "test", Vector: []float32{1, 2, 3}, Content: "test"}

	err = store.Upsert(ctx, &emb)
	if err == nil {
		t.Error("Expected error when upserting to closed store")
	}

	_, err = store.Search(ctx, []float32{1, 2, 3}, SearchOptions{TopK: 1})
	if err == nil {
		t.Error("Expected error when searching closed store")
	}

	_, err = store.Stats(ctx)
	if err == nil {
		t.Error("Expected error when getting stats from closed store")
	}

	_, err = store.ListDocuments(ctx)
	if err == nil {
		t.Error("Expected error when listing documents from closed store")
	}
}

func TestVectorValidation(t *testing.T) {
	tests := []struct {
		name    string
		vector  []float32
		wantErr bool
	}{
		{
			name:    "valid vector",
			vector:  []float32{1.0, 2.0, 3.0},
			wantErr: false,
		},
		{
			name:    "nil vector",
			vector:  nil,
			wantErr: true,
		},
		{
			name:    "empty vector",
			vector:  []float32{},
			wantErr: true,
		},
		{
			name:    "vector with NaN",
			vector:  []float32{1.0, float32(math.NaN()), 3.0},
			wantErr: true,
		},
		{
			name:    "vector with infinity",
			vector:  []float32{1.0, float32(math.Inf(1)), 3.0},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVector(tt.vector)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVector() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func BenchmarkCosineSimilarity(b *testing.B) {
	vector1 := make([]float32, 768)
	vector2 := make([]float32, 768)

	for i := range vector1 {
		vector1[i] = float32(i) * 0.1
		vector2[i] = float32(i) * 0.2
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CosineSimilarity(vector1, vector2)
	}
}

func BenchmarkVectorEncoding(b *testing.B) {
	vector := make([]float32, 768)
	for i := range vector {
		vector[i] = float32(i) * 0.1
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded, _ := encodeVector(vector)
		_, _ = decodeVector(encoded)
	}
}

func BenchmarkUpsert(b *testing.B) {
	dbPath := "benchmark_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	store, err := New(dbPath, 768)
	if err != nil {
		b.Fatalf("Failed to create store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		b.Fatalf("Failed to initialize store: %v", err)
	}

	vector := make([]float32, 768)
	for i := range vector {
		vector[i] = float32(i) * 0.1
	}

	emb := Embedding{
		Vector:  vector,
		Content: "Benchmark content",
		DocID:   "doc1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		emb.ID = fmt.Sprintf("bench_%d", i)
		if err := store.Upsert(ctx, &emb); err != nil {
			b.Fatalf("Failed to upsert: %v", err)
		}
	}
}

func BenchmarkSearch(b *testing.B) {
	dbPath := "search_bench_" + time.Now().Format("20060102_150405") + ".db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	store, err := New(dbPath, 768)
	if err != nil {
		b.Fatalf("Failed to create store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		b.Fatalf("Failed to initialize store: %v", err)
	}

	// Insert test data
	embeddings := make([]*Embedding, 1000)
	for i := range embeddings {
		vector := make([]float32, 768)
		for j := range vector {
			vector[j] = float32(i*j) * 0.001
		}
		embeddings[i] = &Embedding{
			ID:      fmt.Sprintf("search_bench_%d", i),
			Vector:  vector,
			Content: fmt.Sprintf("Content %d", i),
		}
	}

	if err := store.UpsertBatch(ctx, embeddings); err != nil {
		b.Fatalf("Failed to insert test data: %v", err)
	}

	queryVector := make([]float32, 768)
	for i := range queryVector {
		queryVector[i] = 0.5
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.Search(ctx, queryVector, SearchOptions{TopK: 10})
		if err != nil {
			b.Fatalf("Search failed: %v", err)
		}
	}
}
