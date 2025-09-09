package index

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestIVFIndex(t *testing.T) {
	dim := 128
	nCentroids := 10
	
	ivf := NewIVFIndex(dim, nCentroids)
	
	if ivf.Dimension != dim {
		t.Errorf("Expected dimension %d, got %d", dim, ivf.Dimension)
	}
	
	if ivf.NCentroids != nCentroids {
		t.Errorf("Expected %d centroids, got %d", nCentroids, ivf.NCentroids)
	}
	
	if ivf.NProbe <= 0 || ivf.NProbe > nCentroids {
		t.Errorf("Invalid NProbe value: %d", ivf.NProbe)
	}
}

func TestIVFIndexTrain(t *testing.T) {
	dim := 64
	nCentroids := 5
	ivf := NewIVFIndex(dim, nCentroids)
	
	// Generate training data
	vectors := generateTestVectorsIVF(100, dim)
	
	// Train
	err := ivf.Train(vectors)
	if err != nil {
		t.Fatalf("Failed to train: %v", err)
	}
	
	if !ivf.Trained {
		t.Error("IVF should be trained")
	}
	
	if len(ivf.Centroids) != nCentroids {
		t.Errorf("Expected %d centroids, got %d", nCentroids, len(ivf.Centroids))
	}
	
	// Check centroids have correct dimension
	for i, centroid := range ivf.Centroids {
		if len(centroid) != dim {
			t.Errorf("Centroid %d has dimension %d, expected %d", i, len(centroid), dim)
		}
	}
}

func TestIVFIndexTrainInsufficientData(t *testing.T) {
	dim := 64
	nCentroids := 10
	ivf := NewIVFIndex(dim, nCentroids)
	
	// Too few vectors for training
	vectors := generateTestVectorsIVF(5, dim)
	
	err := ivf.Train(vectors)
	if err == nil {
		t.Error("Expected error when training with insufficient data")
	}
}

func TestIVFIndexAddSearch(t *testing.T) {
	dim := 32
	nCentroids := 4
	ivf := NewIVFIndex(dim, nCentroids)
	
	// Train
	trainVectors := generateTestVectorsIVF(50, dim)
	if err := ivf.Train(trainVectors); err != nil {
		t.Fatalf("Train failed: %v", err)
	}
	
	// Add vectors
	testVectors := generateTestVectorsIVF(20, dim)
	for i, vec := range testVectors {
		id := fmt.Sprintf("vec_%d", i)
		err := ivf.Add(id, vec)
		if err != nil {
			t.Fatalf("Failed to add vector %s: %v", id, err)
		}
	}
	
	// Check inverted lists are populated
	totalVectors := 0
	for _, invlist := range ivf.Invlists {
		totalVectors += len(invlist)
	}
	if totalVectors != len(testVectors) {
		t.Errorf("Expected %d vectors in invlists, got %d", len(testVectors), totalVectors)
	}
	
	// Search
	query := testVectors[0]
	ids, distances, err := ivf.Search(query, 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	
	if len(ids) != 5 {
		t.Errorf("Expected 5 results, got %d", len(ids))
	}
	
	// First result should be the query itself
	if ids[0] != "vec_0" {
		t.Errorf("Expected first result to be vec_0, got %s", ids[0])
	}
	
	// Distances should be in ascending order
	for i := 1; i < len(distances); i++ {
		if distances[i] < distances[i-1] {
			t.Error("Distances not in ascending order")
		}
	}
}

func TestIVFIndexSetNProbe(t *testing.T) {
	ivf := NewIVFIndex(128, 10)
	
	// Test setting valid nprobe
	ivf.SetNProbe(5)
	if ivf.NProbe != 5 {
		t.Errorf("Expected NProbe 5, got %d", ivf.NProbe)
	}
	
	// Test setting nprobe > ncentroids
	ivf.SetNProbe(20)
	if ivf.NProbe != 10 {
		t.Errorf("NProbe should be capped at NCentroids (10), got %d", ivf.NProbe)
	}
}

func TestIVFIndexStats(t *testing.T) {
	dim := 32
	nCentroids := 4
	ivf := NewIVFIndex(dim, nCentroids)
	
	// Train and add vectors
	trainVectors := generateTestVectorsIVF(50, dim)
	if err := ivf.Train(trainVectors); err != nil {
		t.Fatalf("Train failed: %v", err)
	}
	
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("vec_%d", i)
		if err := ivf.Add(id, trainVectors[i]); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}
	
	// Get stats
	stats := ivf.Stats()
	
	if stats["ncentroids"] != nCentroids {
		t.Errorf("Expected ncentroids %d, got %v", nCentroids, stats["ncentroids"])
	}
	
	if stats["nvectors"] != 20 {
		t.Errorf("Expected nvectors 20, got %v", stats["nvectors"])
	}
	
	if stats["trained"] != true {
		t.Error("Expected trained to be true")
	}
	
	// Check cluster size distribution
	if _, ok := stats["min_cluster_size"]; !ok {
		t.Error("Stats missing min_cluster_size")
	}
	if _, ok := stats["max_cluster_size"]; !ok {
		t.Error("Stats missing max_cluster_size")
	}
	if _, ok := stats["avg_cluster_size"]; !ok {
		t.Error("Stats missing avg_cluster_size")
	}
}

func TestIVFIndexClear(t *testing.T) {
	dim := 32
	ivf := NewIVFIndex(dim, 4)
	
	// Train and add vectors
	vectors := generateTestVectorsIVF(50, dim)
	if err := ivf.Train(vectors); err != nil {
		t.Fatalf("Train failed: %v", err)
	}
	
	for i := 0; i < 10; i++ {
		if err := ivf.Add(fmt.Sprintf("vec_%d", i), vectors[i]); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}
	
	// Clear
	ivf.Clear()
	
	// Check everything is empty
	for _, invlist := range ivf.Invlists {
		if len(invlist) != 0 {
			t.Error("Inverted list not cleared")
		}
	}
	
	if len(ivf.Vectors) != 0 {
		t.Error("Vectors not cleared")
	}
	
	if len(ivf.IDs) != 0 {
		t.Error("IDs not cleared")
	}
}

func TestIVFIndexNotTrained(t *testing.T) {
	ivf := NewIVFIndex(32, 4)
	
	vec := make([]float32, 32)
	
	// Add should fail
	err := ivf.Add("test", vec)
	if err == nil {
		t.Error("Expected error when adding to untrained index")
	}
	
	// Search should fail
	_, _, err = ivf.Search(vec, 5)
	if err == nil {
		t.Error("Expected error when searching untrained index")
	}
}

func TestIVFIndexDimensionMismatch(t *testing.T) {
	dim := 32
	ivf := NewIVFIndex(dim, 4)
	
	// Train with correct dimension
	vectors := generateTestVectorsIVF(50, dim)
	if err := ivf.Train(vectors); err != nil {
		t.Fatalf("Train failed: %v", err)
	}
	
	// Try to add wrong dimension
	wrongVec := make([]float32, 64)
	err := ivf.Add("wrong", wrongVec)
	if err == nil {
		t.Error("Expected error when adding vector with wrong dimension")
	}
	
	// Try to search wrong dimension
	_, _, err = ivf.Search(wrongVec, 5)
	if err == nil {
		t.Error("Expected error when searching with wrong dimension")
	}
}

func generateTestVectorsIVF(n, dim int) [][]float32 {
	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		vec := make([]float32, dim)
		// Create some clusters in the data
		cluster := i % 3
		for j := 0; j < dim; j++ {
			vec[j] = rng.Float32() + float32(cluster)*0.5
		}
		vectors[i] = vec
	}
	return vectors
}

func BenchmarkIVFAdd(b *testing.B) {
	ivf := NewIVFIndex(128, 100)
	vectors := generateTestVectorsIVF(1000, 128)
	if err := ivf.Train(vectors); err != nil {
		b.Fatalf("Train failed: %v", err)
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("vec_%d", i)
		if err := ivf.Add(id, vectors[i%len(vectors)]); err != nil {
			b.Fatalf("Add failed: %v", err)
		}
	}
}

func BenchmarkIVFSearch(b *testing.B) {
	ivf := NewIVFIndex(128, 100)
	vectors := generateTestVectorsIVF(10000, 128)
	if err := ivf.Train(vectors); err != nil {
		b.Fatalf("Train failed: %v", err)
	}
	
	// Add vectors
	for i, vec := range vectors {
		_ = ivf.Add(fmt.Sprintf("vec_%d", i), vec)
	}
	
	query := vectors[0]
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		if _, _, err := ivf.Search(query, 10); err != nil {
			b.Fatalf("Search failed: %v", err)
		}
	}
}