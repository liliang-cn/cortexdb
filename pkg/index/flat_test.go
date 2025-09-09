package index

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestFlatIndexBasic(t *testing.T) {
	index := NewFlatIndex(4, EuclideanDistance)

	// Insert test vectors
	vectors := map[string][]float32{
		"vec1": {1.0, 0.0, 0.0, 0.0},
		"vec2": {0.0, 1.0, 0.0, 0.0},
		"vec3": {0.0, 0.0, 1.0, 0.0},
		"vec4": {0.5, 0.5, 0.0, 0.0},
		"vec5": {0.5, 0.0, 0.5, 0.0},
	}

	for id, vec := range vectors {
		if err := index.Insert(id, vec); err != nil {
			t.Fatalf("Failed to insert %s: %v", id, err)
		}
	}

	// Test size
	if index.Size() != 5 {
		t.Errorf("Expected size 5, got %d", index.Size())
	}

	// Search for nearest neighbors
	query := []float32{0.9, 0.1, 0.0, 0.0}
	ids, distances := index.Search(query, 3)

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

func TestFlatIndexCosine(t *testing.T) {
	index := NewFlatIndexCosine(4)

	// Insert normalized vectors
	vectors := map[string][]float32{
		"doc1": {1.0, 0.0, 0.0, 0.0},
		"doc2": {1.0, 1.0, 0.0, 0.0}, // Will be normalized
		"doc3": {0.0, 1.0, 0.0, 0.0},
		"doc4": {1.0, 0.0, 1.0, 0.0}, // Will be normalized
		"doc5": {1.0, 1.0, 1.0, 1.0}, // Will be normalized
	}

	for id, vec := range vectors {
		if err := index.Insert(id, vec); err != nil {
			t.Fatalf("Failed to insert %s: %v", id, err)
		}
	}

	// Search with a query that will be normalized
	query := []float32{1.0, 0.5, 0.0, 0.0}
	ids, distances := index.Search(query, 3)

	if len(ids) == 0 {
		t.Fatal("No results returned")
	}

	t.Logf("Cosine distance results:")
	for i, id := range ids {
		t.Logf("  %d. %s (dist: %.4f)", i+1, id, distances[i])
	}

	// Verify normalization works
	if vec, ok := index.GetVector("doc2"); ok {
		var sum float32
		for _, v := range vec {
			sum += v * v
		}
		if math.Abs(float64(sum-1.0)) > 0.001 {
			t.Errorf("Vector not properly normalized, magnitude: %f", sum)
		}
	}
}

func TestFlatIndexRangeSearch(t *testing.T) {
	index := NewFlatIndex(2, EuclideanDistance)

	// Create a grid of points
	points := []struct {
		id  string
		vec []float32
	}{
		{"origin", []float32{0.0, 0.0}},
		{"p1", []float32{1.0, 0.0}},
		{"p2", []float32{0.0, 1.0}},
		{"p3", []float32{1.0, 1.0}},
		{"p4", []float32{2.0, 0.0}},
		{"p5", []float32{0.0, 2.0}},
		{"p6", []float32{2.0, 2.0}},
	}

	for _, p := range points {
		if err := index.Insert(p.id, p.vec); err != nil {
			t.Fatalf("Failed to insert %s: %v", p.id, err)
		}
	}

	// Range search from origin with radius 1.5
	query := []float32{0.0, 0.0}
	radius := float32(1.5)

	ids, distances := index.RangeSearch(query, radius)

	t.Logf("Range search (radius=%.1f) found %d points:", radius, len(ids))
	for i, id := range ids {
		t.Logf("  %s (dist: %.4f)", id, distances[i])
	}

	// Should find origin, p1, p2, and p3 (distance = sqrt(2) ≈ 1.414)
	expectedCount := 4
	if len(ids) != expectedCount {
		t.Errorf("Expected %d points within radius %.1f, got %d", expectedCount, radius, len(ids))
	}

	// Verify all distances are within radius
	for i, dist := range distances {
		if dist > radius {
			t.Errorf("Point %s has distance %.4f, exceeds radius %.1f", ids[i], dist, radius)
		}
	}

	// Test with larger radius
	radius2 := float32(2.5)
	ids2, _ := index.RangeSearch(query, radius2)
	if len(ids2) != 6 { // All except p6 (distance = 2*sqrt(2) ≈ 2.828)
		t.Errorf("Expected 6 points within radius %.1f, got %d", radius2, len(ids2))
	}
}

func TestFlatIndexBatchInsert(t *testing.T) {
	index := NewFlatIndex(3, EuclideanDistance)

	// Prepare batch data
	ids := []string{"batch1", "batch2", "batch3", "batch4"}
	vectors := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
		{1.0, 1.0, 1.0},
	}

	// Batch insert
	if err := index.BatchInsert(ids, vectors); err != nil {
		t.Fatalf("Batch insert failed: %v", err)
	}

	if index.Size() != 4 {
		t.Errorf("Expected size 4 after batch insert, got %d", index.Size())
	}

	// Verify all vectors were inserted correctly
	for i, id := range ids {
		vec, ok := index.GetVector(id)
		if !ok {
			t.Errorf("Vector %s not found after batch insert", id)
			continue
		}
		for j, v := range vec {
			if v != vectors[i][j] {
				t.Errorf("Vector %s has incorrect value at index %d: expected %f, got %f",
					id, j, vectors[i][j], v)
			}
		}
	}
}

func TestFlatIndexDelete(t *testing.T) {
	index := NewFlatIndex(2, EuclideanDistance)

	// Insert vectors
	_ = index.Insert("v1", []float32{1.0, 0.0})
	_ = index.Insert("v2", []float32{0.0, 1.0})
	_ = index.Insert("v3", []float32{1.0, 1.0})

	if index.Size() != 3 {
		t.Errorf("Expected size 3, got %d", index.Size())
	}

	// Delete a vector
	if !index.Delete("v2") {
		t.Error("Delete returned false for existing vector")
	}

	if index.Size() != 2 {
		t.Errorf("Expected size 2 after delete, got %d", index.Size())
	}

	// Try to delete non-existent vector
	if index.Delete("v2") {
		t.Error("Delete returned true for non-existent vector")
	}

	// Verify v2 is not in search results
	ids, _ := index.Search([]float32{0.0, 1.0}, 3)
	for _, id := range ids {
		if id == "v2" {
			t.Error("Deleted vector v2 still appears in search results")
		}
	}
}

func TestFlatIndexEdgeCases(t *testing.T) {
	index := NewFlatIndex(3, EuclideanDistance)

	// Test empty index search
	ids, distances := index.Search([]float32{1.0, 0.0, 0.0}, 5)
	if len(ids) != 0 || len(distances) != 0 {
		t.Error("Empty index should return empty results")
	}

	// Test dimension mismatch
	err := index.Insert("v1", []float32{1.0, 0.0}) // Wrong dimension
	if err == nil {
		t.Error("Expected dimension mismatch error")
	}

	// Test search with wrong dimension
	_ = index.Insert("v1", []float32{1.0, 0.0, 0.0})
	ids, distances = index.Search([]float32{1.0, 0.0}, 1) // Wrong dimension
	if ids != nil || distances != nil {
		t.Error("Search with wrong dimension should return nil")
	}

	// Test k larger than index size
	_ = index.Insert("v2", []float32{0.0, 1.0, 0.0})
	ids, _ = index.Search([]float32{0.5, 0.5, 0.0}, 10)
	if len(ids) != 2 {
		t.Errorf("Expected 2 results (all vectors), got %d", len(ids))
	}
}

func TestFlatIndexClear(t *testing.T) {
	index := NewFlatIndex(2, EuclideanDistance)

	// Insert some vectors
	_ = index.Insert("v1", []float32{1.0, 0.0})
	_ = index.Insert("v2", []float32{0.0, 1.0})

	if index.Size() != 2 {
		t.Errorf("Expected size 2, got %d", index.Size())
	}

	// Clear the index
	index.Clear()

	if index.Size() != 0 {
		t.Errorf("Expected size 0 after clear, got %d", index.Size())
	}

	// Verify search returns empty
	ids, _ := index.Search([]float32{1.0, 0.0}, 5)
	if len(ids) != 0 {
		t.Error("Cleared index should return empty search results")
	}
}

func TestFlatIndexStats(t *testing.T) {
	index := NewFlatIndexCosine(4)

	_ = index.Insert("v1", []float32{1.0, 0.0, 0.0, 0.0})
	_ = index.Insert("v2", []float32{0.0, 1.0, 0.0, 0.0})

	stats := index.Stats()

	if stats["type"] != "flat" {
		t.Errorf("Expected type 'flat', got %v", stats["type"])
	}

	if stats["size"] != 2 {
		t.Errorf("Expected size 2, got %v", stats["size"])
	}

	if stats["dimension"] != 4 {
		t.Errorf("Expected dimension 4, got %v", stats["dimension"])
	}

	if stats["normalized"] != true {
		t.Errorf("Expected normalized true for cosine index, got %v", stats["normalized"])
	}
}

func BenchmarkFlatIndexInsert(b *testing.B) {
	index := NewFlatIndex(128, EuclideanDistance)
	vector := make([]float32, 128)
	for i := range vector {
		vector[i] = rand.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("vec_%d", i)
		_ = index.Insert(id, vector)
	}
}

func BenchmarkFlatIndexSearch(b *testing.B) {
	index := NewFlatIndex(128, EuclideanDistance)

	// Insert 1000 vectors
	for i := 0; i < 1000; i++ {
		vector := make([]float32, 128)
		for j := range vector {
			vector[j] = rand.Float32()
		}
		_ = index.Insert(fmt.Sprintf("vec_%d", i), vector)
	}

	query := make([]float32, 128)
	for i := range query {
		query[i] = rand.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		index.Search(query, 10)
	}
}

func BenchmarkFlatIndexRangeSearch(b *testing.B) {
	index := NewFlatIndex(128, EuclideanDistance)

	// Insert 1000 vectors
	for i := 0; i < 1000; i++ {
		vector := make([]float32, 128)
		for j := range vector {
			vector[j] = rand.Float32()
		}
		_ = index.Insert(fmt.Sprintf("vec_%d", i), vector)
	}

	query := make([]float32, 128)
	for i := range query {
		query[i] = rand.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		index.RangeSearch(query, 0.5)
	}
}