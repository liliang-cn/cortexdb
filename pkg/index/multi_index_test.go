package index

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func euclideanDistance(a, b []float32) float32 {
	var sum float32
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return float32(math.Sqrt(float64(sum)))
}

func TestMultiIndexNew(t *testing.T) {
	config := MultiIndexConfig{
		PrimaryIndex:     IndexTypeHNSW,
		CombineStrategy:  StrategyMergeAll,
		Parallel:         false,
	}
	
	mi := NewMultiIndex(config)
	
	if mi.config.PrimaryIndex != IndexTypeHNSW {
		t.Errorf("Expected primary index HNSW, got %s", mi.config.PrimaryIndex)
	}
	
	if mi.config.CombineStrategy != StrategyMergeAll {
		t.Errorf("Expected merge strategy, got %s", mi.config.CombineStrategy)
	}
	
	if len(mi.indices) != 0 {
		t.Error("Expected empty indices")
	}
}

func TestMultiIndexAddIndex(t *testing.T) {
	config := MultiIndexConfig{
		PrimaryIndex:    IndexTypeHNSW,
		CombineStrategy: StrategyMergeAll,
	}
	mi := NewMultiIndex(config)
	
	// Add HNSW index
	hnsw := NewHNSWAdapter(16, 8, euclideanDistance)
	mi.AddIndex(IndexTypeHNSW, hnsw)
	
	// Add IVF index  
	ivf := NewIVFAdapter(16, 4)
	trainVectors := generateTestVectorsMulti(30, 16)
	ivf.Train(trainVectors)
	mi.AddIndex(IndexTypeIVF, ivf)
	
	if len(mi.indices) != 2 {
		t.Errorf("Expected 2 indices, got %d", len(mi.indices))
	}
	
	if mi.indices[IndexTypeHNSW] == nil {
		t.Error("HNSW index not added correctly")
	}
	
	if mi.indices[IndexTypeIVF] == nil {
		t.Error("IVF index not added correctly") 
	}
}

func TestMultiIndexPrimaryOnlyStrategy(t *testing.T) {
	config := MultiIndexConfig{
		PrimaryIndex:    IndexTypeHNSW,
		CombineStrategy: StrategyPrimaryOnly,
	}
	mi := NewMultiIndex(config)
	
	// Add primary HNSW index
	hnsw := NewHNSWAdapter(32, 8, euclideanDistance)
	mi.AddIndex(IndexTypeHNSW, hnsw)
	
	// Train and add vectors to HNSW
	vectors := generateTestVectorsMulti(20, 32)
	for i, vec := range vectors {
		id := fmt.Sprintf("vec_%d", i)
		hnsw.Insert(id, vec)
	}
	
	// Search using primary only
	query := vectors[0]
	ids, distances := mi.Search(query, 5)
	
	if len(ids) != 5 {
		t.Errorf("Expected 5 results, got %d", len(ids))
	}
	
	// Should return vec_0 as first result (exact match)
	if ids[0] != "vec_0" {
		t.Errorf("Expected vec_0 as first result, got %s", ids[0])
	}
	
	// Distances should be sorted
	for i := 1; i < len(distances); i++ {
		if distances[i] < distances[i-1] {
			t.Error("Distances not sorted")
		}
	}
}

func TestMultiIndexMergeAllStrategy(t *testing.T) {
	config := MultiIndexConfig{
		PrimaryIndex:    IndexTypeHNSW,
		CombineStrategy: StrategyMergeAll,
	}
	mi := NewMultiIndex(config)
	
	// Add HNSW index
	hnsw := NewHNSWAdapter(16, 8, euclideanDistance)
	mi.AddIndex(IndexTypeHNSW, hnsw)
	
	// Add IVF index
	ivf := NewIVFAdapter(16, 3)
	trainVectors := generateTestVectorsMulti(30, 16)
	ivf.Train(trainVectors)
	mi.AddIndex(IndexTypeIVF, ivf)
	
	// Insert vectors into both indices
	testVectors := generateTestVectorsMulti(15, 16)
	for i, vec := range testVectors {
		id := fmt.Sprintf("vec_%d", i)
		mi.Insert(id, vec)
	}
	
	// Search with merge strategy
	query := testVectors[0]
	ids, _ := mi.Search(query, 3)
	
	if len(ids) == 0 {
		t.Error("No results returned")
	}
	
	// Should find the exact match
	found := false
	for _, id := range ids {
		if id == "vec_0" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Exact match not found in merged results")
	}
}

func TestMultiIndexInsertDelete(t *testing.T) {
	config := MultiIndexConfig{
		PrimaryIndex:    IndexTypeHNSW,
		CombineStrategy: StrategyMergeAll,
	}
	mi := NewMultiIndex(config)
	
	// Add indices
	hnsw := NewHNSWAdapter(16, 8, euclideanDistance)
	mi.AddIndex(IndexTypeHNSW, hnsw)
	
	// Insert vector
	vec := generateVector(16)
	mi.Insert("test_vec", vec)
	
	// Verify it was inserted
	ids, _ := mi.Search(vec, 1)
	if len(ids) == 0 || ids[0] != "test_vec" {
		t.Error("Vector not inserted correctly")
	}
	
	// Delete vector
	mi.Delete("test_vec")
	
	// Verify it was deleted
	ids, _ = mi.Search(vec, 5)
	for _, id := range ids {
		if id == "test_vec" {
			t.Error("Vector not deleted correctly")
		}
	}
}

func TestHybridIndex(t *testing.T) {
	hybrid := NewHybridIndex(32, 8, 4)
	
	// Test initial state
	if hybrid.hnsw == nil {
		t.Error("HNSW not initialized")
	}
	
	if hybrid.ivf == nil {
		t.Error("IVF not initialized")
	}
	
	if hybrid.alpha != 0.5 {
		t.Errorf("Expected alpha=0.5, got %f", hybrid.alpha)
	}
}

func TestHybridIndexTrainInsertSearch(t *testing.T) {
	hybrid := NewHybridIndex(16, 8, 3)
	
	// Train
	trainVectors := generateTestVectorsMulti(30, 16)
	err := hybrid.Train(trainVectors)
	if err != nil {
		t.Fatalf("Training failed: %v", err)
	}
	
	// Insert vectors
	testVectors := generateTestVectorsMulti(10, 16)
	for i, vec := range testVectors {
		id := fmt.Sprintf("vec_%d", i)
		hybrid.Insert(id, vec)
	}
	
	// Search
	query := testVectors[0]
	ids, distances := hybrid.Search(query, 3)
	
	if len(ids) != 3 {
		t.Errorf("Expected 3 results, got %d", len(ids))
	}
	
	// First result should be exact match
	if ids[0] != "vec_0" {
		t.Errorf("Expected vec_0 as first result, got %s", ids[0])
	}
	
	// Check distance ordering
	for i := 1; i < len(distances); i++ {
		if distances[i] < distances[i-1] {
			t.Error("Results not sorted by distance")
		}
	}
}

func TestHybridIndexAlpha(t *testing.T) {
	hybrid := NewHybridIndex(16, 8, 3)
	
	// Train
	trainVectors := generateTestVectorsMulti(20, 16) 
	hybrid.Train(trainVectors)
	
	// Insert vectors
	for i := 0; i < 10; i++ {
		vec := generateVector(16)
		hybrid.Insert(fmt.Sprintf("vec_%d", i), vec)
	}
	
	query := generateVector(16)
	
	// Test different alpha values
	alphaValues := []float32{0.0, 0.3, 0.7, 1.0}
	
	for _, alpha := range alphaValues {
		hybrid.alpha = alpha
		ids, _ := hybrid.Search(query, 3)
		
		if len(ids) == 0 {
			t.Errorf("No results with alpha=%.1f", alpha)
		}
	}
}

func TestMultiIndexSize(t *testing.T) {
	config := MultiIndexConfig{
		PrimaryIndex:    IndexTypeHNSW,
		CombineStrategy: StrategyMergeAll,
	}
	mi := NewMultiIndex(config)
	
	// Add index
	hnsw := NewHNSWAdapter(16, 8, euclideanDistance)
	mi.AddIndex(IndexTypeHNSW, hnsw)
	
	// Initially empty
	if mi.Size() != 0 {
		t.Errorf("Expected size 0, got %d", mi.Size())
	}
	
	// Insert vectors
	for i := 0; i < 5; i++ {
		vec := generateVector(16)
		mi.Insert(fmt.Sprintf("vec_%d", i), vec)
	}
	
	if mi.Size() != 5 {
		t.Errorf("Expected size 5, got %d", mi.Size())
	}
}

func TestMultiIndexParallelOperations(t *testing.T) {
	config := MultiIndexConfig{
		PrimaryIndex:    IndexTypeHNSW,
		CombineStrategy: StrategyMergeAll,
		Parallel:        true,
	}
	mi := NewMultiIndex(config)
	
	// Add multiple indices
	hnsw := NewHNSWAdapter(16, 8, euclideanDistance)
	mi.AddIndex(IndexTypeHNSW, hnsw)
	
	ivf := NewIVFAdapter(16, 3)
	trainVectors := generateTestVectorsMulti(20, 16)
	ivf.Train(trainVectors)
	mi.AddIndex(IndexTypeIVF, ivf)
	
	// Insert with parallel operations enabled
	for i := 0; i < 10; i++ {
		vec := generateVector(16)
		err := mi.Insert(fmt.Sprintf("vec_%d", i), vec)
		if err != nil {
			t.Errorf("Parallel insert failed: %v", err)
		}
	}
	
	// Search should still work
	query := generateVector(16)
	ids, _ := mi.Search(query, 5)
	
	if len(ids) == 0 {
		t.Error("No results from parallel multi-index")
	}
}

// Helper functions
func generateTestVectorsMulti(n, dim int) [][]float32 {
	rand.Seed(42)
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		vec := make([]float32, dim)
		for j := 0; j < dim; j++ {
			vec[j] = rand.Float32()*2 - 1
		}
		vectors[i] = vec
	}
	return vectors
}

func generateVector(dim int) []float32 {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = rand.Float32()*2 - 1
	}
	return vec
}

func BenchmarkMultiIndexSearch(b *testing.B) {
	config := MultiIndexConfig{
		PrimaryIndex:    IndexTypeHNSW,
		CombineStrategy: StrategyMergeAll,
	}
	mi := NewMultiIndex(config)
	
	// Add HNSW index
	hnsw := NewHNSWAdapter(128, 16, euclideanDistance)
	mi.AddIndex(IndexTypeHNSW, hnsw)
	
	// Build index
	vectors := generateTestVectorsMulti(5000, 128)
	for i, vec := range vectors {
		mi.Insert(fmt.Sprintf("vec_%d", i), vec)
	}
	
	query := vectors[0]
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		mi.Search(query, 10)
	}
}

func BenchmarkHybridIndexSearch(b *testing.B) {
	hybrid := NewHybridIndex(128, 16, 20)
	
	// Train and build
	vectors := generateTestVectorsMulti(5000, 128)
	hybrid.Train(vectors[:1000])
	
	for i, vec := range vectors {
		hybrid.Insert(fmt.Sprintf("vec_%d", i), vec)
	}
	
	query := vectors[0]
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		hybrid.Search(query, 10)
	}
}