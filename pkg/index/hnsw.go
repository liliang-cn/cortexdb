// Package index provides vector indexing implementations
package index

import (
	"container/heap"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"sync"
	"time"
)

// Quantizer defines interface for vector quantization
type Quantizer interface {
	Encode(vec []float32) ([]byte, error)
	Decode(encoded []byte) ([]float32, error)
}

// HNSWNode represents a node in the HNSW graph
type HNSWNode struct {
	ID         string
	Vector     []float32
	Quantized  []byte     // Quantized vector data (optional)
	Level      int
	Neighbors  [][]string // Neighbors at each level
	Deleted    bool
}

// HNSW implements Hierarchical Navigable Small World index
type HNSW struct {
	// Parameters
	M              int     // Max number of bi-directional links per node
	MaxM           int     // Max number of links for layer 0
	EfConstruction int     // Size of dynamic candidate list
	ML             float64 // Level assignment probability
	Seed           int64   // Random seed
	
	// Index data
	Nodes      map[string]*HNSWNode
	EntryPoint string
	
	// Distance function
	DistFunc func(a, b []float32) float32
	
	// Quantization
	Quantizer Quantizer
	
	// Thread safety
	mu sync.RWMutex
	rng *rand.Rand
}

// NewHNSW creates a new HNSW index
func NewHNSW(M, efConstruction int, distFunc func(a, b []float32) float32) *HNSW {
	seed := time.Now().UnixNano()
	return &HNSW{
		M:              M,
		MaxM:           M * 2, // MaxM = 2*M for layer 0
		EfConstruction: efConstruction,
		ML:             1.0 / math.Log(2.0), // This is approximately 1.44
		Seed:           seed,
		Nodes:          make(map[string]*HNSWNode),
		DistFunc:       distFunc,
		rng:            rand.New(rand.NewSource(seed)),
	}
}

// SetQuantizer sets the quantizer for the index
func (h *HNSW) SetQuantizer(q Quantizer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Quantizer = q
}

// calculateDistance computes distance between query and node, handling quantization
func (h *HNSW) calculateDistance(query []float32, node *HNSWNode) float32 {
	if node.Vector != nil {
		return h.DistFunc(query, node.Vector)
	}
	
	if node.Quantized != nil && h.Quantizer != nil {
		// Dequantize on the fly
		// Note: For better performance, we should implement distance in compressed domain,
		// but that requires changing the Quantizer interface to support distance calc.
		// For now, this saves memory at the cost of CPU.
		vec, err := h.Quantizer.Decode(node.Quantized)
		if err == nil {
			return h.DistFunc(query, vec)
		}
	}
	
	// Fallback or error case (shouldn't happen if managed correctly)
	return math.MaxFloat32
}

// Save serializes the index to a writer
func (h *HNSW) Save(w io.Writer) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	enc := gob.NewEncoder(w)

	// Encode parameters
	if err := enc.Encode(h.M); err != nil { return err }
	if err := enc.Encode(h.EfConstruction); err != nil { return err }
	if err := enc.Encode(h.EntryPoint); err != nil { return err }

	// Encode nodes count
	if err := enc.Encode(len(h.Nodes)); err != nil { return err }

	// Encode nodes
	for _, node := range h.Nodes {
		if err := enc.Encode(node); err != nil {
			return err
		}
	}
	
	// Note: We don't save the Quantizer itself here because it's an interface
	// and might require specific type handling. The store should handle Quantizer persistence.

	return nil
}

// Load deserializes the index from a reader
func (h *HNSW) Load(r io.Reader) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	dec := gob.NewDecoder(r)

	// Decode parameters
	if err := dec.Decode(&h.M); err != nil { return err }
	h.MaxM = h.M * 2
	h.ML = 1.0 / math.Log(2.0)
	
	if err := dec.Decode(&h.EfConstruction); err != nil { return err }
	if err := dec.Decode(&h.EntryPoint); err != nil { return err }

	// Decode nodes count
	var count int
	if err := dec.Decode(&count); err != nil { return err }

	// Decode nodes
	h.Nodes = make(map[string]*HNSWNode, count)
	for i := 0; i < count; i++ {
		var node HNSWNode
		if err := dec.Decode(&node); err != nil {
			return err
		}
		h.Nodes[node.ID] = &node
	}

	return nil
}

// selectLevel randomly selects level for a new node
func (h *HNSW) selectLevel() int {
	// Standard HNSW level assignment with exponential decay
	// Probability of level l is: ML^l * (1-ML)
	level := 0
	for h.rng.Float64() < 0.5 { // 50% chance to go to next level
		level++
		if level > 16 { // Cap at reasonable maximum
			break
		}
	}
	return level
}

// Insert adds a new vector to the index
func (h *HNSW) Insert(id string, vector []float32) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if _, exists := h.Nodes[id]; exists {
		return fmt.Errorf("node %s already exists", id)
	}
	
	// Prepare node data
	var quantized []byte
	var storedVector []float32 = vector
	
	if h.Quantizer != nil {
		var err error
		quantized, err = h.Quantizer.Encode(vector)
		if err == nil {
			// If quantization successful, we can choose to drop the raw vector to save memory
			// However, HNSW construction needs accurate distances.
			// Ideally, we keep vector during construction if we are building incrementally,
			// or we accept the accuracy loss.
			// Let's drop it to fulfill the "memory efficiency" requirement.
			storedVector = nil
		}
	}
	
	// Create new node
	level := h.selectLevel()
	node := &HNSWNode{
		ID:        id,
		Vector:    storedVector,
		Quantized: quantized,
		Level:     level,
		Neighbors: make([][]string, level+1),
	}
	
	// Initialize neighbor lists
	for i := 0; i <= level; i++ {
		node.Neighbors[i] = make([]string, 0)
	}
	
	h.Nodes[id] = node
	
	// If this is the first node, set as entry point
	if h.EntryPoint == "" {
		h.EntryPoint = id
		return nil
	}
	
	// Search for closest points at all levels
	currNearest := []string{h.EntryPoint}
	
	// Search from top layer to target layer
	entryNode := h.Nodes[h.EntryPoint]
	for lc := entryNode.Level; lc > level; lc-- {
		currNearest = h.searchLayerClosest(vector, currNearest, 1, lc)
	}
	
	// Insert into all layers from level to 0
	for lc := level; lc >= 0; lc-- {
		m := h.M
		if lc == 0 {
			m = h.MaxM
		}
		
		candidates := h.searchLayer(vector, currNearest, h.EfConstruction, lc)
		neighbors := h.selectNeighborsHeuristic(vector, candidates, m, lc)
		
		// Add bidirectional links
		node.Neighbors[lc] = neighbors
		for _, neighbor := range neighbors {
			h.addConnection(neighbor, id, lc)
			
			// Prune connections of neighbors if needed
			neighborNode := h.Nodes[neighbor]
			maxConn := h.M
			if lc == 0 {
				maxConn = h.MaxM
			}
			
			// Check if neighbor has this layer
			if lc < len(neighborNode.Neighbors) && len(neighborNode.Neighbors[lc]) > maxConn {
				// To prune, we need the neighbor's vector.
				// If neighbor is quantized, we need to decode it or use stored vector.
				// This implies expensive decoding during pruning if we only store quantized.
				
				// Fetch neighbor vector (might involve decoding)
				neighborVec := neighborNode.Vector
				if neighborVec == nil && neighborNode.Quantized != nil && h.Quantizer != nil {
					neighborVec, _ = h.Quantizer.Decode(neighborNode.Quantized)
				}
				
				if neighborVec != nil {
					// Prune the connections
					newNeighbors := h.selectNeighborsHeuristic(
						neighborVec,
						neighborNode.Neighbors[lc],
						maxConn,
						lc,
					)
					neighborNode.Neighbors[lc] = newNeighbors
				}
			}
		}
		
		currNearest = neighbors
	}
	
	// Update entry point if necessary
	if level > h.Nodes[h.EntryPoint].Level {
		h.EntryPoint = id
	}
	
	return nil
}

// searchLayer performs a greedy search in a specific layer
func (h *HNSW) searchLayer(query []float32, entryPoints []string, ef int, layer int) []string {
	visited := make(map[string]bool)
	candidates := &distHeap{}
	dynamicList := &distHeap{} // max heap for nearest
	
	for _, point := range entryPoints {
		dist := h.calculateDistance(query, h.Nodes[point])
		
		heap.Push(candidates, &heapItem{id: point, dist: dist})
		heap.Push(dynamicList, &heapItem{id: point, dist: -dist}) // negative for max heap
		visited[point] = true
	}
	
	for candidates.Len() > 0 {
		if dynamicList.Len() > 0 {
			lowerBound := (*candidates)[0].dist
			if lowerBound > -(*dynamicList)[0].dist {
				break
			}
		}
		
		current := heap.Pop(candidates).(*heapItem)
		currentNode := h.Nodes[current.id]
		
		if layer >= len(currentNode.Neighbors) {
			continue
		}
		
		for _, neighbor := range currentNode.Neighbors[layer] {
			if !visited[neighbor] {
				visited[neighbor] = true
				
				dist := h.calculateDistance(query, h.Nodes[neighbor])
				
				if dist < -(*dynamicList)[0].dist || dynamicList.Len() < ef {
					heap.Push(candidates, &heapItem{id: neighbor, dist: dist})
					heap.Push(dynamicList, &heapItem{id: neighbor, dist: -dist})
					
					if dynamicList.Len() > ef {
						heap.Pop(dynamicList)
					}
				}
			}
		}
	}
	
	// Extract result
	result := make([]string, 0, dynamicList.Len())
	for dynamicList.Len() > 0 {
		item := heap.Pop(dynamicList).(*heapItem)
		result = append(result, item.id)
	}
	
	// Reverse to get closest first
	for i := 0; i < len(result)/2; i++ {
		result[i], result[len(result)-1-i] = result[len(result)-1-i], result[i]
	}
	
	return result
}

// searchLayerClosest finds the closest point in a layer
func (h *HNSW) searchLayerClosest(query []float32, entryPoints []string, num int, layer int) []string {
	candidates := h.searchLayer(query, entryPoints, num, layer)
	if len(candidates) > num {
		return candidates[:num]
	}
	return candidates
}

// selectNeighborsHeuristic selects m neighbors using a heuristic
func (h *HNSW) selectNeighborsHeuristic(query []float32, candidates []string, m int, _ int) []string {
	if len(candidates) <= m {
		return candidates
	}
	
	// Create distance pairs
	type distPair struct {
		id   string
		dist float32
	}
	
	pairs := make([]distPair, len(candidates))
	for i, candidate := range candidates {
		var dist float32
		if candidate == "query" {
			// Should not happen in standard usage, logic specific
			dist = 0
		} else {
			dist = h.calculateDistance(query, h.Nodes[candidate])
		}
		pairs[i] = distPair{
			id:   candidate,
			dist: dist,
		}
	}
	
	// Sort by distance
	for i := 0; i < len(pairs)-1; i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].dist < pairs[i].dist {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
	
	// Select top m
	result := make([]string, 0, m)
	for i := 0; i < m && i < len(pairs); i++ {
		result = append(result, pairs[i].id)
	}
	
	return result
}

// addConnection adds a connection between two nodes
func (h *HNSW) addConnection(from, to string, layer int) {
	fromNode, exists := h.Nodes[from]
	if !exists || layer >= len(fromNode.Neighbors) {
		return
	}
	
	// Check if connection already exists
	for _, neighbor := range fromNode.Neighbors[layer] {
		if neighbor == to {
			return
		}
	}
	
	fromNode.Neighbors[layer] = append(fromNode.Neighbors[layer], to)
}

// Search performs k-NN search
func (h *HNSW) Search(query []float32, k int, ef int) ([]string, []float32) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	if h.EntryPoint == "" {
		return []string{}, []float32{}
	}
	
	// Search from top layer to layer 0
	entryNode := h.Nodes[h.EntryPoint]
	currNearest := []string{h.EntryPoint}
	
	for layer := entryNode.Level; layer > 0; layer-- {
		currNearest = h.searchLayerClosest(query, currNearest, 1, layer)
	}
	
	// Search at layer 0 with ef
	candidates := h.searchLayer(query, currNearest, ef, 0)
	
	// Return top k
	type result struct {
		id   string
		dist float32
	}
	
	results := make([]result, 0, len(candidates))
	for _, candidate := range candidates {
		if node, exists := h.Nodes[candidate]; exists && !node.Deleted {
			results = append(results, result{
				id:   candidate,
				dist: h.calculateDistance(query, node),
			})
		}
	}
	
	// Sort by distance
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].dist < results[i].dist {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	
	// Extract top k
	limit := k
	if limit > len(results) {
		limit = len(results)
	}
	
	ids := make([]string, limit)
	distances := make([]float32, limit)
	for i := 0; i < limit; i++ {
		ids[i] = results[i].id
		distances[i] = results[i].dist
	}
	
	return ids, distances
}

// Delete marks a node as deleted (soft delete)
func (h *HNSW) Delete(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	node, exists := h.Nodes[id]
	if !exists {
		return errors.New("node not found")
	}
	
	node.Deleted = true
	
	// If this was the entry point, find a new one
	if h.EntryPoint == id {
		for nodeID, node := range h.Nodes {
			if !node.Deleted {
				h.EntryPoint = nodeID
				break
			}
		}
	}
	
	return nil
}

// Size returns the number of nodes in the index
func (h *HNSW) Size() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	count := 0
	for _, node := range h.Nodes {
		if !node.Deleted {
			count++
		}
	}
	return count
}

// Stats returns index statistics
func (h *HNSW) Stats() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	totalNodes := len(h.Nodes)
	activeNodes := 0
	totalEdges := 0
	maxLevel := 0
	
	levelDistribution := make(map[int]int)
	
	for _, node := range h.Nodes {
		if !node.Deleted {
			activeNodes++
			if node.Level > maxLevel {
				maxLevel = node.Level
			}
			levelDistribution[node.Level]++
			
			for _, neighbors := range node.Neighbors {
				totalEdges += len(neighbors)
			}
		}
	}
	
	avgEdges := float64(0)
	if activeNodes > 0 {
		avgEdges = float64(totalEdges) / float64(activeNodes)
	}
	
	return map[string]interface{}{
		"total_nodes":        totalNodes,
		"active_nodes":       activeNodes,
		"deleted_nodes":      totalNodes - activeNodes,
		"total_edges":        totalEdges,
		"avg_edges_per_node": avgEdges,
		"max_level":          maxLevel,
		"level_distribution": levelDistribution,
		"entry_point":        h.EntryPoint,
		"M":                  h.M,
		"ef_construction":    h.EfConstruction,
	}
}

// heapItem for priority queue
type heapItem struct {
	id   string
	dist float32
}

// distHeap implements heap.Interface
type distHeap []*heapItem

func (h distHeap) Len() int           { return len(h) }
func (h distHeap) Less(i, j int) bool { return h[i].dist < h[j].dist }
func (h distHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *distHeap) Push(x interface{}) {
	*h = append(*h, x.(*heapItem))
}

func (h *distHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[0 : n-1]
	return item
}

// Distance functions

// EuclideanDistance computes Euclidean distance
func EuclideanDistance(a, b []float32) float32 {
	var sum float32
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return float32(math.Sqrt(float64(sum)))
}

// CosineDistance computes cosine distance (1 - cosine similarity)
func CosineDistance(a, b []float32) float32 {
	var dotProduct, normA, normB float32
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	
	if normA == 0 || normB == 0 {
		return 1.0
	}
	
	similarity := dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
	return 1.0 - similarity
}

// DotProductDistance computes negative dot product (for similarity)
func DotProductDistance(a, b []float32) float32 {
	var dotProduct float32
	for i := range a {
		dotProduct += a[i] * b[i]
	}
	return -dotProduct // Negative so smaller is better
}

// VectorIndex interface compatibility methods

// SearchVectorIndex provides VectorIndex-compatible search with default ef
func (h *HNSW) SearchVectorIndex(query []float32, k int) ([]string, []float32) {
	// Use a reasonable default ef value
	ef := 50
	if ef < k {
		ef = k * 2
	}
	return h.Search(query, k, ef)
}