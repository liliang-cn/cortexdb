// Package index provides advanced indexing structures for vector search
package index

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
)

// IVFIndex implements Inverted File Index for partitioned vector search
type IVFIndex struct {
	NCentroids   int           // Number of cluster centroids
	Dimension    int           // Vector dimension
	Centroids    [][]float32   // Cluster centroids
	Invlists     [][]int       // Inverted lists (vector IDs per cluster)
	Vectors      [][]float32   // Original vectors (optional, for reranking)
	IDs          []string      // Vector IDs
	Trained      bool
	NProbe       int           // Number of clusters to search
	mu           sync.RWMutex
}

// NewIVFIndex creates a new IVF index
func NewIVFIndex(dimension, nCentroids int) *IVFIndex {
	return &IVFIndex{
		NCentroids: nCentroids,
		Dimension:  dimension,
		NProbe:     min(nCentroids, 10), // Default: search 10 clusters
		Invlists:   make([][]int, nCentroids),
		IDs:        []string{},
		Vectors:    [][]float32{},
	}
}

// Train learns cluster centroids from training data
func (ivf *IVFIndex) Train(vectors [][]float32) error {
	if len(vectors) < ivf.NCentroids {
		return fmt.Errorf("need at least %d vectors for training, got %d", ivf.NCentroids, len(vectors))
	}
	
	// Run k-means to find centroids
	centroids, err := kMeansIVF(vectors, ivf.NCentroids, 20)
	if err != nil {
		return fmt.Errorf("k-means training failed: %w", err)
	}
	
	ivf.Centroids = centroids
	ivf.Trained = true
	
	// Initialize inverted lists
	for i := range ivf.Invlists {
		ivf.Invlists[i] = []int{}
	}
	
	return nil
}

// Add adds a vector to the index
func (ivf *IVFIndex) Add(id string, vector []float32) error {
	ivf.mu.Lock()
	defer ivf.mu.Unlock()
	
	if !ivf.Trained {
		return errors.New("index not trained")
	}
	
	if len(vector) != ivf.Dimension {
		return fmt.Errorf("vector dimension %d doesn't match index dimension %d", len(vector), ivf.Dimension)
	}
	
	// Find nearest centroid
	centroidIdx := ivf.findNearestCentroid(vector)
	
	// Add to inverted list
	vectorIdx := len(ivf.Vectors)
	ivf.Invlists[centroidIdx] = append(ivf.Invlists[centroidIdx], vectorIdx)
	
	// Store vector and ID
	ivf.Vectors = append(ivf.Vectors, vector)
	ivf.IDs = append(ivf.IDs, id)
	
	return nil
}

// Search performs approximate nearest neighbor search
func (ivf *IVFIndex) Search(query []float32, k int) ([]string, []float32, error) {
	ivf.mu.RLock()
	defer ivf.mu.RUnlock()
	
	if !ivf.Trained {
		return nil, nil, errors.New("index not trained")
	}
	
	if len(query) != ivf.Dimension {
		return nil, nil, fmt.Errorf("query dimension %d doesn't match index dimension %d", len(query), ivf.Dimension)
	}
	
	// Find nprobe nearest centroids
	centroidDists := make([]struct {
		idx  int
		dist float32
	}, ivf.NCentroids)
	
	for i, centroid := range ivf.Centroids {
		centroidDists[i] = struct {
			idx  int
			dist float32
		}{i, euclideanDistanceIVF(query, centroid)}
	}
	
	sort.Slice(centroidDists, func(i, j int) bool {
		return centroidDists[i].dist < centroidDists[j].dist
	})
	
	// Search in nprobe nearest clusters
	nprobe := min(ivf.NProbe, ivf.NCentroids)
	candidates := []struct {
		idx  int
		dist float32
	}{}
	
	for i := 0; i < nprobe; i++ {
		centroidIdx := centroidDists[i].idx
		
		// Search all vectors in this cluster
		for _, vectorIdx := range ivf.Invlists[centroidIdx] {
			dist := euclideanDistanceIVF(query, ivf.Vectors[vectorIdx])
			candidates = append(candidates, struct {
				idx  int
				dist float32
			}{vectorIdx, dist})
		}
	}
	
	// Sort candidates by distance
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].dist < candidates[j].dist
	})
	
	// Return top-k results
	topK := min(k, len(candidates))
	ids := make([]string, topK)
	distances := make([]float32, topK)
	
	for i := 0; i < topK; i++ {
		ids[i] = ivf.IDs[candidates[i].idx]
		distances[i] = candidates[i].dist
	}
	
	return ids, distances, nil
}

// SetNProbe sets the number of clusters to search
func (ivf *IVFIndex) SetNProbe(nprobe int) {
	ivf.mu.Lock()
	defer ivf.mu.Unlock()
	ivf.NProbe = min(nprobe, ivf.NCentroids)
}

// findNearestCentroid finds the nearest cluster centroid for a vector
func (ivf *IVFIndex) findNearestCentroid(vector []float32) int {
	minDist := float32(math.MaxFloat32)
	minIdx := 0
	
	for i, centroid := range ivf.Centroids {
		dist := euclideanDistanceIVF(vector, centroid)
		if dist < minDist {
			minDist = dist
			minIdx = i
		}
	}
	
	return minIdx
}

// Stats returns index statistics
func (ivf *IVFIndex) Stats() map[string]interface{} {
	ivf.mu.RLock()
	defer ivf.mu.RUnlock()
	
	stats := map[string]interface{}{
		"ncentroids":   ivf.NCentroids,
		"dimension":    ivf.Dimension,
		"nvectors":     len(ivf.Vectors),
		"nprobe":       ivf.NProbe,
		"trained":      ivf.Trained,
	}
	
	// Cluster size distribution
	clusterSizes := make([]int, ivf.NCentroids)
	for i, invlist := range ivf.Invlists {
		clusterSizes[i] = len(invlist)
	}
	
	// Find min, max, avg cluster size
	if len(clusterSizes) > 0 {
		minSize, maxSize := clusterSizes[0], clusterSizes[0]
		totalSize := 0
		for _, size := range clusterSizes {
			if size < minSize {
				minSize = size
			}
			if size > maxSize {
				maxSize = size
			}
			totalSize += size
		}
		
		stats["min_cluster_size"] = minSize
		stats["max_cluster_size"] = maxSize
		stats["avg_cluster_size"] = float64(totalSize) / float64(len(clusterSizes))
	}
	
	return stats
}

// Clear removes all vectors from the index
func (ivf *IVFIndex) Clear() {
	ivf.mu.Lock()
	defer ivf.mu.Unlock()
	
	for i := range ivf.Invlists {
		ivf.Invlists[i] = []int{}
	}
	ivf.Vectors = [][]float32{}
	ivf.IDs = []string{}
}

// kMeansIVF performs k-means clustering for IVF
func kMeansIVF(vectors [][]float32, k int, maxIters int) ([][]float32, error) {
	if len(vectors) < k {
		return nil, fmt.Errorf("need at least %d vectors, got %d", k, len(vectors))
	}
	
	dim := len(vectors[0])
	
	// Initialize centroids with k-means++
	centroids := make([][]float32, k)
	
	// Choose first centroid randomly
	centroids[0] = make([]float32, dim)
	copy(centroids[0], vectors[rand.Intn(len(vectors))])
	
	// Choose remaining centroids with probability proportional to squared distance
	for i := 1; i < k; i++ {
		distances := make([]float32, len(vectors))
		totalDist := float32(0)
		
		for j, vec := range vectors {
			minDist := float32(math.MaxFloat32)
			for c := 0; c < i; c++ {
				dist := euclideanDistanceIVF(vec, centroids[c])
				if dist < minDist {
					minDist = dist
				}
			}
			distances[j] = minDist * minDist
			totalDist += distances[j]
		}
		
		// Select next centroid
		r := rand.Float32() * totalDist
		cumSum := float32(0)
		for j, dist := range distances {
			cumSum += dist
			if cumSum >= r {
				centroids[i] = make([]float32, dim)
				copy(centroids[i], vectors[j])
				break
			}
		}
	}
	
	// Run k-means iterations
	assignments := make([]int, len(vectors))
	
	for iter := 0; iter < maxIters; iter++ {
		// Assign vectors to nearest centroid
		changed := false
		for i, vec := range vectors {
			minDist := float32(math.MaxFloat32)
			minIdx := 0
			
			for j, centroid := range centroids {
				dist := euclideanDistanceIVF(vec, centroid)
				if dist < minDist {
					minDist = dist
					minIdx = j
				}
			}
			
			if assignments[i] != minIdx {
				changed = true
				assignments[i] = minIdx
			}
		}
		
		if !changed {
			break
		}
		
		// Update centroids
		counts := make([]int, k)
		for i := range centroids {
			centroids[i] = make([]float32, dim)
		}
		
		for i, vec := range vectors {
			cluster := assignments[i]
			counts[cluster]++
			for j := 0; j < dim; j++ {
				centroids[cluster][j] += vec[j]
			}
		}
		
		for i := range centroids {
			if counts[i] > 0 {
				for j := 0; j < dim; j++ {
					centroids[i][j] /= float32(counts[i])
				}
			}
		}
	}
	
	return centroids, nil
}

// euclideanDistanceIVF computes Euclidean distance
func euclideanDistanceIVF(a, b []float32) float32 {
	sum := float32(0)
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return float32(math.Sqrt(float64(sum)))
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Delete removes a vector from the index
func (ivf *IVFIndex) Delete(id string) error {
	if !ivf.Trained {
		return errors.New("index not trained")
	}
	
	// Find the vector index in IDs
	vectorIndex := -1
	for i, storedID := range ivf.IDs {
		if storedID == id {
			vectorIndex = i
			break
		}
	}
	
	if vectorIndex == -1 {
		return errors.New("vector not found")
	}
	
	// Remove from IDs
	ivf.IDs = append(ivf.IDs[:vectorIndex], ivf.IDs[vectorIndex+1:]...)
	
	// Remove from Vectors
	if vectorIndex < len(ivf.Vectors) {
		ivf.Vectors = append(ivf.Vectors[:vectorIndex], ivf.Vectors[vectorIndex+1:]...)
	}
	
	// Remove from inverted lists
	for i := range ivf.Invlists {
		for j, idx := range ivf.Invlists[i] {
			if idx == vectorIndex {
				ivf.Invlists[i] = append(ivf.Invlists[i][:j], ivf.Invlists[i][j+1:]...)
				break
			} else if idx > vectorIndex {
				// Update indices after deletion
				ivf.Invlists[i][j] = idx - 1
			}
		}
	}
	
	return nil
}

// Size returns the number of vectors in the index
func (ivf *IVFIndex) Size() int {
	return len(ivf.Vectors)
}