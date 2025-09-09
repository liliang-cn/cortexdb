package index

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestHNSWBasic(t *testing.T) {
	// Create HNSW index
	hnsw := NewHNSW(16, 200, EuclideanDistance)
	
	// Insert test vectors
	vectors := []struct {
		id  string
		vec []float32
	}{
		{"vec1", []float32{1.0, 0.0, 0.0, 0.0}},
		{"vec2", []float32{0.0, 1.0, 0.0, 0.0}},
		{"vec3", []float32{0.0, 0.0, 1.0, 0.0}},
		{"vec4", []float32{0.5, 0.5, 0.0, 0.0}},
		{"vec5", []float32{0.5, 0.0, 0.5, 0.0}},
	}
	
	for _, v := range vectors {
		err := hnsw.Insert(v.id, v.vec)
		if err != nil {
			t.Fatalf("Failed to insert %s: %v", v.id, err)
		}
	}
	
	// Test size
	if hnsw.Size() != 5 {
		t.Errorf("Expected size 5, got %d", hnsw.Size())
	}
	
	// Search for nearest neighbors
	query := []float32{0.9, 0.1, 0.0, 0.0}
	ids, distances := hnsw.Search(query, 3, 50)
	
	if len(ids) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(ids))
	}
	
	// First result should be vec1 (closest to query)
	if ids[0] != "vec1" {
		t.Errorf("Expected first result to be vec1, got %s", ids[0])
	}
	
	// Distances should be in ascending order
	for i := 1; i < len(distances); i++ {
		if distances[i] < distances[i-1] {
			t.Error("Distances not in ascending order")
		}
	}
	
	t.Logf("Search results:")
	for i, id := range ids {
		t.Logf("  %d. %s (dist: %.4f)", i+1, id, distances[i])
	}
}

func TestHNSWCosineDistance(t *testing.T) {
	// Test with cosine distance
	hnsw := NewHNSW(16, 200, CosineDistance)
	
	// Normalized vectors for cosine similarity
	normalize := func(v []float32) []float32 {
		var sum float32
		for _, val := range v {
			sum += val * val
		}
		norm := float32(math.Sqrt(float64(sum)))
		result := make([]float32, len(v))
		for i, val := range v {
			result[i] = val / norm
		}
		return result
	}
	
	vectors := []struct {
		id  string
		vec []float32
	}{
		{"doc1", normalize([]float32{1.0, 0.0, 0.0, 0.0})},
		{"doc2", normalize([]float32{1.0, 1.0, 0.0, 0.0})},
		{"doc3", normalize([]float32{0.0, 1.0, 0.0, 0.0})},
		{"doc4", normalize([]float32{1.0, 0.0, 1.0, 0.0})},
		{"doc5", normalize([]float32{1.0, 1.0, 1.0, 1.0})},
	}
	
	for _, v := range vectors {
		err := hnsw.Insert(v.id, v.vec)
		if err != nil {
			t.Fatalf("Failed to insert %s: %v", v.id, err)
		}
	}
	
	// Search with normalized query
	query := normalize([]float32{1.0, 0.5, 0.0, 0.0})
	ids, distances := hnsw.Search(query, 3, 50)
	
	if len(ids) == 0 {
		t.Fatal("No results returned")
	}
	
	t.Logf("Cosine distance results:")
	for i, id := range ids {
		t.Logf("  %d. %s (dist: %.4f)", i+1, id, distances[i])
	}
}

func TestHNSWLargeScale(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large scale test in short mode")
	}
	
	// Create index
	hnsw := NewHNSW(16, 200, EuclideanDistance)
	
	// Insert 1000 random vectors
	numVectors := 1000
	dim := 128
	vectors := make([][]float32, numVectors)
	
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, dim)
		for j := 0; j < dim; j++ {
			vec[j] = rng.Float32()*2 - 1
		}
		vectors[i] = vec
		
		id := fmt.Sprintf("vec_%d", i)
		err := hnsw.Insert(id, vec)
		if err != nil {
			t.Fatalf("Failed to insert vector %d: %v", i, err)
		}
	}
	
	// Test search
	query := vectors[0] // Use first vector as query
	ids, distances := hnsw.Search(query, 10, 100)
	
	if len(ids) != 10 {
		t.Errorf("Expected 10 results, got %d", len(ids))
	}
	
	// First result should be the query itself with distance ~0
	if ids[0] != "vec_0" {
		t.Errorf("Expected first result to be vec_0, got %s", ids[0])
	}
	
	if distances[0] > 0.001 {
		t.Errorf("Expected first distance to be ~0, got %.4f", distances[0])
	}
	
	// Check stats
	stats := hnsw.Stats()
	t.Logf("Index stats after %d insertions:", numVectors)
	t.Logf("  Active nodes: %v", stats["active_nodes"])
	t.Logf("  Total edges: %v", stats["total_edges"])
	t.Logf("  Avg edges per node: %.2f", stats["avg_edges_per_node"])
	t.Logf("  Max level: %v", stats["max_level"])
}

func TestHNSWDelete(t *testing.T) {
	hnsw := NewHNSW(16, 200, EuclideanDistance)
	
	// Insert vectors
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("vec_%d", i)
		vec := make([]float32, 4)
		vec[0] = float32(i)
		err := hnsw.Insert(id, vec)
		if err != nil {
			t.Fatalf("Failed to insert %s: %v", id, err)
		}
	}
	
	// Delete a vector
	err := hnsw.Delete("vec_2")
	if err != nil {
		t.Fatalf("Failed to delete vec_2: %v", err)
	}
	
	// Size should be 4 now
	if hnsw.Size() != 4 {
		t.Errorf("Expected size 4 after deletion, got %d", hnsw.Size())
	}
	
	// Search should not return deleted vector
	query := []float32{2.0, 0, 0, 0}
	ids, _ := hnsw.Search(query, 5, 50)
	
	for _, id := range ids {
		if id == "vec_2" {
			t.Error("Deleted vector vec_2 appeared in search results")
		}
	}
}

func TestHNSWDuplicateInsert(t *testing.T) {
	hnsw := NewHNSW(16, 200, EuclideanDistance)
	
	vec := []float32{1.0, 0.0, 0.0, 0.0}
	
	// First insert should succeed
	err := hnsw.Insert("vec1", vec)
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}
	
	// Second insert with same ID should fail
	err = hnsw.Insert("vec1", vec)
	if err == nil {
		t.Error("Expected error for duplicate insert, got nil")
	}
}

func TestHNSWEmptyIndex(t *testing.T) {
	hnsw := NewHNSW(16, 200, EuclideanDistance)
	
	// Search on empty index should return empty results
	query := []float32{1.0, 0.0, 0.0, 0.0}
	ids, distances := hnsw.Search(query, 5, 50)
	
	if len(ids) != 0 {
		t.Errorf("Expected 0 results from empty index, got %d", len(ids))
	}
	
	if len(distances) != 0 {
		t.Errorf("Expected 0 distances from empty index, got %d", len(distances))
	}
}

func BenchmarkHNSWInsert(b *testing.B) {
	hnsw := NewHNSW(16, 200, EuclideanDistance)
	dim := 128
	
	// Pre-generate vectors
	vectors := make([][]float32, b.N)
	for i := 0; i < b.N; i++ {
		vec := make([]float32, dim)
		for j := 0; j < dim; j++ {
			vec[j] = rand.Float32()
		}
		vectors[i] = vec
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := hnsw.Insert(fmt.Sprintf("vec_%d", i), vectors[i]); err != nil {
			b.Fatalf("Insert failed: %v", err)
		}
	}
}

func BenchmarkHNSWSearch(b *testing.B) {
	hnsw := NewHNSW(16, 200, EuclideanDistance)
	dim := 128
	numVectors := 10000
	
	// Build index
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, dim)
		for j := 0; j < dim; j++ {
			vec[j] = rand.Float32()
		}
		if err := hnsw.Insert(fmt.Sprintf("vec_%d", i), vec); err != nil {
			b.Fatalf("Insert failed: %v", err)
		}
	}
	
	// Generate query
	query := make([]float32, dim)
	for j := 0; j < dim; j++ {
		query[j] = rand.Float32()
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hnsw.Search(query, 10, 50)
	}
}