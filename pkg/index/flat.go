package index

import (
	"container/heap"
	"fmt"
	"math"
	"sync"
)

// FlatIndex implements a brute-force exact search index
// This guarantees finding the exact nearest neighbors but with O(n) complexity
type FlatIndex struct {
	mu         sync.RWMutex
	vectors    map[string][]float32
	dimension  int
	distFunc   func([]float32, []float32) float32
	normalized bool // For cosine similarity, vectors should be normalized
}

// NewFlatIndex creates a new brute-force index
func NewFlatIndex(dimension int, distFunc func([]float32, []float32) float32) *FlatIndex {
	if distFunc == nil {
		distFunc = EuclideanDistance
	}
	return &FlatIndex{
		vectors:   make(map[string][]float32),
		dimension: dimension,
		distFunc:  distFunc,
	}
}

// NewFlatIndexCosine creates a flat index optimized for cosine similarity
func NewFlatIndexCosine(dimension int) *FlatIndex {
	return &FlatIndex{
		vectors:    make(map[string][]float32),
		dimension:  dimension,
		distFunc:   CosineDistance,
		normalized: true,
	}
}

// Insert adds a vector to the index
func (f *FlatIndex) Insert(id string, vector []float32) error {
	if len(vector) != f.dimension {
		return fmt.Errorf("dimension mismatch: expected %d, got %d", f.dimension, len(vector))
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Normalize vector for cosine similarity
	if f.normalized {
		vector = normalize(vector)
	}

	// Store a copy to avoid external modifications
	v := make([]float32, len(vector))
	copy(v, vector)
	f.vectors[id] = v

	return nil
}

// Search performs exact brute-force search
func (f *FlatIndex) Search(query []float32, k int) ([]string, []float32) {
	if len(query) != f.dimension {
		return nil, nil
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(f.vectors) == 0 {
		return []string{}, []float32{}
	}

	// Normalize query for cosine similarity
	if f.normalized {
		query = normalize(query)
	}

	// Use a max heap to keep track of k nearest neighbors
	h := &flatMaxHeap{}
	heap.Init(h)

	for id, vector := range f.vectors {
		dist := f.distFunc(query, vector)
		
		if h.Len() < k {
			heap.Push(h, flatHeapItem{id: id, distance: dist})
		} else if dist < (*h)[0].distance {
			heap.Pop(h)
			heap.Push(h, flatHeapItem{id: id, distance: dist})
		}
	}

	// Extract results in order
	results := make([]flatHeapItem, h.Len())
	for i := len(results) - 1; i >= 0; i-- {
		results[i] = heap.Pop(h).(flatHeapItem)
	}

	ids := make([]string, len(results))
	distances := make([]float32, len(results))
	for i, item := range results {
		ids[i] = item.id
		distances[i] = item.distance
	}

	return ids, distances
}

// RangeSearch finds all vectors within a specified distance from the query
func (f *FlatIndex) RangeSearch(query []float32, radius float32) ([]string, []float32) {
	if len(query) != f.dimension {
		return nil, nil
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.normalized {
		query = normalize(query)
	}

	var ids []string
	var distances []float32

	for id, vector := range f.vectors {
		dist := f.distFunc(query, vector)
		if dist <= radius {
			ids = append(ids, id)
			distances = append(distances, dist)
		}
	}

	// Sort results by distance
	if len(ids) > 1 {
		quickSortResults(ids, distances, 0, len(ids)-1)
	}

	return ids, distances
}

// Delete removes a vector from the index
func (f *FlatIndex) Delete(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.vectors[id]; exists {
		delete(f.vectors, id)
		return true
	}
	return false
}

// Size returns the number of vectors in the index
func (f *FlatIndex) Size() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.vectors)
}

// Clear removes all vectors from the index
func (f *FlatIndex) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vectors = make(map[string][]float32)
}

// GetVector returns a copy of the vector for the given ID
func (f *FlatIndex) GetVector(id string) ([]float32, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if vector, exists := f.vectors[id]; exists {
		v := make([]float32, len(vector))
		copy(v, vector)
		return v, true
	}
	return nil, false
}

// BatchInsert adds multiple vectors efficiently
func (f *FlatIndex) BatchInsert(ids []string, vectors [][]float32) error {
	if len(ids) != len(vectors) {
		return fmt.Errorf("ids and vectors length mismatch: %d != %d", len(ids), len(vectors))
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	for i, id := range ids {
		if len(vectors[i]) != f.dimension {
			return fmt.Errorf("dimension mismatch at index %d: expected %d, got %d", i, f.dimension, len(vectors[i]))
		}

		v := make([]float32, f.dimension)
		copy(v, vectors[i])
		
		if f.normalized {
			v = normalize(v)
		}
		
		f.vectors[id] = v
	}

	return nil
}

// Stats returns statistics about the index
func (f *FlatIndex) Stats() map[string]interface{} {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return map[string]interface{}{
		"type":       "flat",
		"size":       len(f.vectors),
		"dimension":  f.dimension,
		"normalized": f.normalized,
	}
}

// normalize returns a normalized copy of the vector
func normalize(v []float32) []float32 {
	var sum float32
	for _, val := range v {
		sum += val * val
	}
	
	if sum == 0 {
		return v
	}
	
	norm := float32(math.Sqrt(float64(sum)))
	result := make([]float32, len(v))
	for i, val := range v {
		result[i] = val / norm
	}
	return result
}

// quickSortResults sorts ids and distances arrays by distance
func quickSortResults(ids []string, distances []float32, low, high int) {
	if low < high {
		pi := partition(ids, distances, low, high)
		quickSortResults(ids, distances, low, pi-1)
		quickSortResults(ids, distances, pi+1, high)
	}
}

func partition(ids []string, distances []float32, low, high int) int {
	pivot := distances[high]
	i := low - 1

	for j := low; j < high; j++ {
		if distances[j] <= pivot {
			i++
			distances[i], distances[j] = distances[j], distances[i]
			ids[i], ids[j] = ids[j], ids[i]
		}
	}

	distances[i+1], distances[high] = distances[high], distances[i+1]
	ids[i+1], ids[high] = ids[high], ids[i+1]
	return i + 1
}

// flatHeapItem represents an item in the max heap for flat index
type flatHeapItem struct {
	id       string
	distance float32
}

// flatMaxHeap implements heap.Interface for a max heap
type flatMaxHeap []flatHeapItem

func (h flatMaxHeap) Len() int           { return len(h) }
func (h flatMaxHeap) Less(i, j int) bool { return h[i].distance > h[j].distance }
func (h flatMaxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *flatMaxHeap) Push(x interface{}) {
	*h = append(*h, x.(flatHeapItem))
}

func (h *flatMaxHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[0 : n-1]
	return item
}