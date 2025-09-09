// Package index provides various indexing implementations for vector search
package index

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
)

// LSHSearchResult represents a search result from LSH index
type LSHSearchResult struct {
	ID       string
	Distance float32
}

// LSHIndex implements Locality Sensitive Hashing for fast approximate nearest neighbor search
type LSHIndex struct {
	mu sync.RWMutex
	
	// LSH parameters
	numTables      int                    // Number of hash tables (L)
	numHashFuncs   int                    // Number of hash functions per table (K)
	dimension      int                    // Vector dimension
	hashFunctions  [][][]float32          // Random projections for each table
	hashTables     []map[uint64][]string  // Hash tables mapping hash -> vector IDs
	
	// Vector storage
	vectors        map[string][]float32   // ID -> vector mapping
	
	// Distance function
	distFunc       func([]float32, []float32) float32
}

// lshEuclideanDistance calculates Euclidean distance between two vectors
func lshEuclideanDistance(a, b []float32) float32 {
	var sum float32
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return float32(math.Sqrt(float64(sum)))
}

// LSHConfig contains configuration for LSH index
type LSHConfig struct {
	NumTables    int  // Number of hash tables (more = better recall, more memory)
	NumHashFuncs int  // Number of hash functions per table (more = more selective)
	Dimension    int  // Vector dimension
	Seed         int64 // Random seed for reproducibility
}

// NewLSHIndex creates a new LSH index
func NewLSHIndex(config LSHConfig) *LSHIndex {
	if config.NumTables <= 0 {
		config.NumTables = 10 // Default number of tables
	}
	if config.NumHashFuncs <= 0 {
		config.NumHashFuncs = 8 // Default number of hash functions
	}
	
	rng := rand.New(rand.NewSource(config.Seed))
	
	// Initialize hash functions (random projections)
	hashFunctions := make([][][]float32, config.NumTables)
	for i := 0; i < config.NumTables; i++ {
		hashFunctions[i] = make([][]float32, config.NumHashFuncs)
		for j := 0; j < config.NumHashFuncs; j++ {
			hashFunctions[i][j] = make([]float32, config.Dimension)
			for k := 0; k < config.Dimension; k++ {
				// Random Gaussian projection
				hashFunctions[i][j][k] = float32(rng.NormFloat64())
			}
		}
	}
	
	// Initialize hash tables
	hashTables := make([]map[uint64][]string, config.NumTables)
	for i := 0; i < config.NumTables; i++ {
		hashTables[i] = make(map[uint64][]string)
	}
	
	return &LSHIndex{
		numTables:     config.NumTables,
		numHashFuncs:  config.NumHashFuncs,
		dimension:     config.Dimension,
		hashFunctions: hashFunctions,
		hashTables:    hashTables,
		vectors:       make(map[string][]float32),
		distFunc:      lshEuclideanDistance,
	}
}

// Insert adds a vector to the LSH index
func (lsh *LSHIndex) Insert(id string, vector []float32) error {
	if len(vector) != lsh.dimension {
		return fmt.Errorf("dimension mismatch: expected %d, got %d", lsh.dimension, len(vector))
	}
	
	lsh.mu.Lock()
	defer lsh.mu.Unlock()
	
	// Store the vector
	lsh.vectors[id] = vector
	
	// Add to all hash tables
	for tableIdx := 0; tableIdx < lsh.numTables; tableIdx++ {
		hash := lsh.computeHash(vector, tableIdx)
		lsh.hashTables[tableIdx][hash] = append(lsh.hashTables[tableIdx][hash], id)
	}
	
	return nil
}

// Search finds approximate nearest neighbors
func (lsh *LSHIndex) Search(query []float32, k int) ([]LSHSearchResult, error) {
	if len(query) != lsh.dimension {
		return nil, fmt.Errorf("dimension mismatch: expected %d, got %d", lsh.dimension, len(query))
	}
	
	lsh.mu.RLock()
	defer lsh.mu.RUnlock()
	
	// Collect candidates from all hash tables
	candidateSet := make(map[string]bool)
	for tableIdx := 0; tableIdx < lsh.numTables; tableIdx++ {
		hash := lsh.computeHash(query, tableIdx)
		if candidates, exists := lsh.hashTables[tableIdx][hash]; exists {
			for _, id := range candidates {
				candidateSet[id] = true
			}
		}
	}
	
	// Score all candidates
	results := make([]LSHSearchResult, 0, len(candidateSet))
	for id := range candidateSet {
		if vector, exists := lsh.vectors[id]; exists {
			dist := lsh.distFunc(query, vector)
			results = append(results, LSHSearchResult{
				ID:       id,
				Distance: dist,
			})
		}
	}
	
	// Sort by distance
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})
	
	// Return top k
	if len(results) > k {
		results = results[:k]
	}
	
	return results, nil
}

// SearchWithMultiProbe uses multi-probe LSH for better recall
func (lsh *LSHIndex) SearchWithMultiProbe(query []float32, k int, numProbes int) ([]LSHSearchResult, error) {
	if len(query) != lsh.dimension {
		return nil, fmt.Errorf("dimension mismatch: expected %d, got %d", lsh.dimension, len(query))
	}
	
	lsh.mu.RLock()
	defer lsh.mu.RUnlock()
	
	candidateSet := make(map[string]bool)
	
	for tableIdx := 0; tableIdx < lsh.numTables; tableIdx++ {
		// Get base hash and nearby hashes
		baseHash := lsh.computeHash(query, tableIdx)
		probeHashes := lsh.getProbeHashes(query, tableIdx, numProbes)
		
		// Collect candidates from all probed buckets
		for _, hash := range probeHashes {
			if candidates, exists := lsh.hashTables[tableIdx][hash]; exists {
				for _, id := range candidates {
					candidateSet[id] = true
				}
			}
		}
		
		// Also include base hash
		if candidates, exists := lsh.hashTables[tableIdx][baseHash]; exists {
			for _, id := range candidates {
				candidateSet[id] = true
			}
		}
	}
	
	// Score and sort candidates
	results := make([]LSHSearchResult, 0, len(candidateSet))
	for id := range candidateSet {
		if vector, exists := lsh.vectors[id]; exists {
			dist := lsh.distFunc(query, vector)
			results = append(results, LSHSearchResult{
				ID:       id,
				Distance: dist,
			})
		}
	}
	
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})
	
	if len(results) > k {
		results = results[:k]
	}
	
	return results, nil
}

// Delete removes a vector from the index
func (lsh *LSHIndex) Delete(id string) bool {
	lsh.mu.Lock()
	defer lsh.mu.Unlock()
	
	vector, exists := lsh.vectors[id]
	if !exists {
		return false
	}
	
	// Remove from all hash tables
	for tableIdx := 0; tableIdx < lsh.numTables; tableIdx++ {
		hash := lsh.computeHash(vector, tableIdx)
		if bucket, exists := lsh.hashTables[tableIdx][hash]; exists {
			// Remove id from bucket
			newBucket := make([]string, 0, len(bucket))
			for _, vecID := range bucket {
				if vecID != id {
					newBucket = append(newBucket, vecID)
				}
			}
			if len(newBucket) > 0 {
				lsh.hashTables[tableIdx][hash] = newBucket
			} else {
				delete(lsh.hashTables[tableIdx], hash)
			}
		}
	}
	
	// Remove from vector storage
	delete(lsh.vectors, id)
	return true
}

// Clear removes all vectors from the index
func (lsh *LSHIndex) Clear() {
	lsh.mu.Lock()
	defer lsh.mu.Unlock()
	
	lsh.vectors = make(map[string][]float32)
	for i := 0; i < lsh.numTables; i++ {
		lsh.hashTables[i] = make(map[uint64][]string)
	}
}

// Size returns the number of vectors in the index
func (lsh *LSHIndex) Size() int {
	lsh.mu.RLock()
	defer lsh.mu.RUnlock()
	return len(lsh.vectors)
}

// Stats returns statistics about the LSH index
func (lsh *LSHIndex) Stats() map[string]interface{} {
	lsh.mu.RLock()
	defer lsh.mu.RUnlock()
	
	totalBuckets := 0
	totalItems := 0
	maxBucketSize := 0
	
	for tableIdx := 0; tableIdx < lsh.numTables; tableIdx++ {
		totalBuckets += len(lsh.hashTables[tableIdx])
		for _, bucket := range lsh.hashTables[tableIdx] {
			bucketSize := len(bucket)
			totalItems += bucketSize
			if bucketSize > maxBucketSize {
				maxBucketSize = bucketSize
			}
		}
	}
	
	avgBucketSize := float64(0)
	if totalBuckets > 0 {
		avgBucketSize = float64(totalItems) / float64(totalBuckets)
	}
	
	return map[string]interface{}{
		"num_vectors":      len(lsh.vectors),
		"num_tables":       lsh.numTables,
		"num_hash_funcs":   lsh.numHashFuncs,
		"total_buckets":    totalBuckets,
		"avg_bucket_size":  avgBucketSize,
		"max_bucket_size":  maxBucketSize,
		"memory_overhead":  totalItems - len(lsh.vectors), // Duplicate entries across tables
	}
}

// computeHash computes the hash for a vector in a specific table
func (lsh *LSHIndex) computeHash(vector []float32, tableIdx int) uint64 {
	hash := uint64(0)
	projections := lsh.hashFunctions[tableIdx]
	
	for i, projection := range projections {
		// Compute dot product
		dotProduct := float32(0)
		for j := 0; j < len(vector); j++ {
			dotProduct += vector[j] * projection[j]
		}
		
		// Binary hash based on sign
		if dotProduct > 0 {
			hash |= (1 << uint(i))
		}
	}
	
	return hash
}

// getProbeHashes generates nearby hash values for multi-probe LSH
func (lsh *LSHIndex) getProbeHashes(vector []float32, tableIdx int, numProbes int) []uint64 {
	baseHash := lsh.computeHash(vector, tableIdx)
	probes := make([]uint64, 0, numProbes)
	
	// Calculate projection values and their distances from threshold
	projections := lsh.hashFunctions[tableIdx]
	flipDistances := make([]struct {
		bit      int
		distance float32
	}, lsh.numHashFuncs)
	
	for i, projection := range projections {
		dotProduct := float32(0)
		for j := 0; j < len(vector); j++ {
			dotProduct += vector[j] * projection[j]
		}
		flipDistances[i] = struct {
			bit      int
			distance float32
		}{
			bit:      i,
			distance: float32(math.Abs(float64(dotProduct))),
		}
	}
	
	// Sort by distance (closest to threshold first)
	sort.Slice(flipDistances, func(i, j int) bool {
		return flipDistances[i].distance < flipDistances[j].distance
	})
	
	// Generate probes by flipping bits
	for i := 0; i < numProbes && i < len(flipDistances); i++ {
		// Flip the i-th closest bit
		probeHash := baseHash ^ (1 << uint(flipDistances[i].bit))
		probes = append(probes, probeHash)
	}
	
	return probes
}

// SetDistanceFunc sets the distance function
func (lsh *LSHIndex) SetDistanceFunc(distFunc func([]float32, []float32) float32) {
	lsh.mu.Lock()
	defer lsh.mu.Unlock()
	lsh.distFunc = distFunc
}