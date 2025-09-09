package index

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestLSHIndexBasic(t *testing.T) {
	config := LSHConfig{
		NumTables:    5,
		NumHashFuncs: 4,
		Dimension:    4,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	
	// Insert test vectors
	vectors := map[string][]float32{
		"vec1": {1, 0, 0, 0},
		"vec2": {0, 1, 0, 0},
		"vec3": {0, 0, 1, 0},
		"vec4": {1, 1, 0, 0},
		"vec5": {1, 0, 1, 0},
	}
	
	for id, vec := range vectors {
		if err := lsh.Insert(id, vec); err != nil {
			t.Fatalf("Failed to insert %s: %v", id, err)
		}
	}
	
	// Search for similar vectors
	query := []float32{0.9, 0.1, 0, 0}
	results, err := lsh.Search(query, 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	
	t.Logf("LSH search results:")
	for i, r := range results {
		t.Logf("  %d. %s (dist: %.4f)", i+1, r.ID, r.Distance)
	}
	
	// vec1 should be the closest
	if len(results) == 0 || results[0].ID != "vec1" {
		t.Errorf("Expected vec1 to be the closest match")
	}
}

func TestLSHMultiProbe(t *testing.T) {
	config := LSHConfig{
		NumTables:    8,
		NumHashFuncs: 6,
		Dimension:    8,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	
	// Insert more vectors
	numVectors := 100
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, 8)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if err := lsh.Insert(fmt.Sprintf("vec%d", i), vec); err != nil {
			t.Fatalf("Failed to insert vec%d: %v", i, err)
		}
	}
	
	// Create a query
	query := make([]float32, 8)
	for i := range query {
		query[i] = rand.Float32()
	}
	
	// Search without multi-probe
	results1, err := lsh.Search(query, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	
	// Search with multi-probe
	results2, err := lsh.SearchWithMultiProbe(query, 10, 3)
	if err != nil {
		t.Fatalf("Multi-probe search failed: %v", err)
	}
	
	t.Logf("Regular search found %d results", len(results1))
	t.Logf("Multi-probe search found %d results", len(results2))
	
	// Multi-probe should generally find more or equal candidates
	if len(results2) < len(results1) {
		t.Errorf("Multi-probe should find at least as many results")
	}
}

func TestLSHDelete(t *testing.T) {
	config := LSHConfig{
		NumTables:    3,
		NumHashFuncs: 4,
		Dimension:    4,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	
	// Insert vectors
	_ = lsh.Insert("vec1", []float32{1, 0, 0, 0})
	_ = lsh.Insert("vec2", []float32{0, 1, 0, 0})
	_ = lsh.Insert("vec3", []float32{0, 0, 1, 0})
	
	if lsh.Size() != 3 {
		t.Errorf("Expected size 3, got %d", lsh.Size())
	}
	
	// Delete a vector
	if !lsh.Delete("vec2") {
		t.Errorf("Failed to delete vec2")
	}
	
	if lsh.Size() != 2 {
		t.Errorf("Expected size 2 after deletion, got %d", lsh.Size())
	}
	
	// Search should not return deleted vector
	results, _ := lsh.Search([]float32{0, 1, 0, 0}, 3)
	for _, r := range results {
		if r.ID == "vec2" {
			t.Errorf("Deleted vector vec2 should not be in results")
		}
	}
}

func TestLSHStats(t *testing.T) {
	config := LSHConfig{
		NumTables:    5,
		NumHashFuncs: 4,
		Dimension:    8,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	
	// Insert vectors
	for i := 0; i < 50; i++ {
		vec := make([]float32, 8)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		_ = lsh.Insert(fmt.Sprintf("vec%d", i), vec)
	}
	
	stats := lsh.Stats()
	t.Logf("LSH Stats: %+v", stats)
	
	if stats["num_vectors"].(int) != 50 {
		t.Errorf("Expected 50 vectors, got %d", stats["num_vectors"].(int))
	}
	
	if stats["num_tables"].(int) != 5 {
		t.Errorf("Expected 5 tables, got %d", stats["num_tables"].(int))
	}
}

func TestLSHDimensionMismatch(t *testing.T) {
	config := LSHConfig{
		NumTables:    3,
		NumHashFuncs: 4,
		Dimension:    4,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	
	// Try to insert wrong dimension
	err := lsh.Insert("bad", []float32{1, 2})
	if err == nil {
		t.Errorf("Expected dimension mismatch error")
	}
	
	// Insert correct dimension
	_ = lsh.Insert("good", []float32{1, 0, 0, 0})
	
	// Try to search with wrong dimension
	_, err = lsh.Search([]float32{1, 2}, 1)
	if err == nil {
		t.Errorf("Expected dimension mismatch error for search")
	}
}

func TestLSHClear(t *testing.T) {
	config := LSHConfig{
		NumTables:    3,
		NumHashFuncs: 4,
		Dimension:    4,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	
	// Insert vectors
	_ = lsh.Insert("vec1", []float32{1, 0, 0, 0})
	_ = lsh.Insert("vec2", []float32{0, 1, 0, 0})
	
	if lsh.Size() != 2 {
		t.Errorf("Expected size 2, got %d", lsh.Size())
	}
	
	lsh.Clear()
	
	if lsh.Size() != 0 {
		t.Errorf("Expected size 0 after clear, got %d", lsh.Size())
	}
	
	// Should be able to insert again
	err := lsh.Insert("vec3", []float32{0, 0, 1, 0})
	if err != nil {
		t.Errorf("Failed to insert after clear: %v", err)
	}
}

func BenchmarkLSHInsert(b *testing.B) {
	config := LSHConfig{
		NumTables:    10,
		NumHashFuncs: 8,
		Dimension:    128,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	vec := make([]float32, 128)
	for i := range vec {
		vec[i] = rand.Float32()
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = lsh.Insert(fmt.Sprintf("vec%d", i), vec)
	}
}

func BenchmarkLSHSearch(b *testing.B) {
	config := LSHConfig{
		NumTables:    10,
		NumHashFuncs: 8,
		Dimension:    128,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	
	// Pre-insert vectors
	for i := 0; i < 10000; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		_ = lsh.Insert(fmt.Sprintf("vec%d", i), vec)
	}
	
	query := make([]float32, 128)
	for i := range query {
		query[i] = rand.Float32()
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lsh.Search(query, 10)
	}
}

func BenchmarkLSHMultiProbe(b *testing.B) {
	config := LSHConfig{
		NumTables:    10,
		NumHashFuncs: 8,
		Dimension:    128,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	
	// Pre-insert vectors
	for i := 0; i < 10000; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		_ = lsh.Insert(fmt.Sprintf("vec%d", i), vec)
	}
	
	query := make([]float32, 128)
	for i := range query {
		query[i] = rand.Float32()
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lsh.SearchWithMultiProbe(query, 10, 3)
	}
}

func TestLSHRecallAccuracy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping recall test in short mode")
	}
	
	dimension := 64
	numVectors := 1000
	numQueries := 100
	k := 10
	
	// Create ground truth with flat index
	flat := NewFlatIndex(dimension, EuclideanDistance)
	vectors := make(map[string][]float32)
	
	for i := 0; i < numVectors; i++ {
		id := fmt.Sprintf("vec%d", i)
		vec := make([]float32, dimension)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		vectors[id] = vec
		_ = flat.Insert(id, vec)
	}
	
	// Create LSH index
	config := LSHConfig{
		NumTables:    12,
		NumHashFuncs: 8,
		Dimension:    dimension,
		Seed:         42,
	}
	lsh := NewLSHIndex(config)
	
	for id, vec := range vectors {
		_ = lsh.Insert(id, vec)
	}
	
	// Test recall
	totalRecall := float64(0)
	
	for q := 0; q < numQueries; q++ {
		query := make([]float32, dimension)
		for i := range query {
			query[i] = rand.Float32()
		}
		
		// Get ground truth
		groundTruthIDs, _ := flat.Search(query, k)
		groundTruthSet := make(map[string]bool)
		for _, id := range groundTruthIDs {
			groundTruthSet[id] = true
		}
		
		// Get LSH results with multi-probe
		lshResults, _ := lsh.SearchWithMultiProbe(query, k*2, 5) // Get more candidates
		if len(lshResults) > k {
			lshResults = lshResults[:k]
		}
		
		// Calculate recall
		hits := 0
		for _, r := range lshResults {
			if groundTruthSet[r.ID] {
				hits++
			}
		}
		
		recall := float64(hits) / float64(k)
		totalRecall += recall
	}
	
	avgRecall := totalRecall / float64(numQueries)
	t.Logf("Average recall@%d: %.2f%%", k, avgRecall*100)
	
	// Should achieve reasonable recall
	if avgRecall < 0.5 {
		t.Errorf("Recall too low: %.2f%%, expected at least 50%%", avgRecall*100)
	}
}

func TestLSHPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}
	
	dimension := 128
	numVectors := 50000
	
	// Create LSH index
	config := LSHConfig{
		NumTables:    10,
		NumHashFuncs: 8,
		Dimension:    dimension,
		Seed:         42,
	}
	lsh := NewLSHIndex(config)
	
	// Create flat index for comparison
	flat := NewFlatIndex(dimension, EuclideanDistance)
	
	// Insert vectors
	t.Logf("Inserting %d vectors...", numVectors)
	start := time.Now()
	for i := 0; i < numVectors; i++ {
		id := fmt.Sprintf("vec%d", i)
		vec := make([]float32, dimension)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		_ = lsh.Insert(id, vec)
		_ = flat.Insert(id, vec)
	}
	t.Logf("Insertion took: %v", time.Since(start))
	
	// Create query
	query := make([]float32, dimension)
	for i := range query {
		query[i] = rand.Float32()
	}
	
	// Benchmark LSH search
	start = time.Now()
	lshResults, _ := lsh.SearchWithMultiProbe(query, 10, 3)
	lshTime := time.Since(start)
	
	// Benchmark flat search
	start = time.Now()
	flatIDs, _ := flat.Search(query, 10)
	flatTime := time.Since(start)
	
	t.Logf("LSH search time: %v", lshTime)
	t.Logf("Flat search time: %v", flatTime)
	t.Logf("Speedup: %.2fx", float64(flatTime)/float64(lshTime))
	
	// Calculate overlap
	flatSet := make(map[string]bool)
	for _, id := range flatIDs {
		flatSet[id] = true
	}
	
	overlap := 0
	for _, r := range lshResults {
		if flatSet[r.ID] {
			overlap++
		}
	}
	
	t.Logf("Result overlap: %d/%d", overlap, len(lshResults))
	
	// LSH should be significantly faster
	if lshTime > flatTime/10 {
		t.Logf("Warning: LSH not achieving expected speedup")
	}
}

func TestLSHHashDistribution(t *testing.T) {
	config := LSHConfig{
		NumTables:    5,
		NumHashFuncs: 4,
		Dimension:    8,
		Seed:         42,
	}
	
	lsh := NewLSHIndex(config)
	
	// Insert vectors and track hash distribution
	hashCounts := make(map[uint64]int)
	numVectors := 1000
	
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, 8)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		
		// Compute hash for first table
		hash := lsh.computeHash(vec, 0)
		hashCounts[hash]++
		
		_ = lsh.Insert(fmt.Sprintf("vec%d", i), vec)
	}
	
	// Check distribution
	maxCount := 0
	minCount := numVectors
	totalBuckets := len(hashCounts)
	
	for _, count := range hashCounts {
		if count > maxCount {
			maxCount = count
		}
		if count < minCount {
			minCount = count
		}
	}
	
	avgCount := float64(numVectors) / float64(totalBuckets)
	
	t.Logf("Hash distribution:")
	t.Logf("  Total buckets: %d", totalBuckets)
	t.Logf("  Min bucket size: %d", minCount)
	t.Logf("  Max bucket size: %d", maxCount)
	t.Logf("  Avg bucket size: %.2f", avgCount)
	
	// Check if distribution is reasonably uniform
	maxExpected := int(math.Pow(2, float64(config.NumHashFuncs)))
	if totalBuckets > maxExpected {
		t.Errorf("More buckets than expected: %d > %d", totalBuckets, maxExpected)
	}
}