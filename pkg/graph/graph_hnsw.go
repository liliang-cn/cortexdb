package graph

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"github.com/liliang-cn/sqvect/pkg/core"
	"github.com/liliang-cn/sqvect/internal/encoding"
)

// SimpleHNSW implements a simplified HNSW-like index for vector search
type SimpleHNSW struct {
	dimensions int
	maxLevels  int
	maxConns   int
	mL         float64
	vectors    map[string][]float32
	graph      map[int]map[string][]string  // level -> nodeID -> connections
	entryPoint string
	mutex      sync.RWMutex
	similarity func([]float32, []float32) float64
}

// HNSWGraphIndex provides HNSW-accelerated graph searches
type HNSWGraphIndex struct {
	index *SimpleHNSW
}

// NewSimpleHNSW creates a new simplified HNSW index
func NewSimpleHNSW(dimensions int, maxConns int) *SimpleHNSW {
	return &SimpleHNSW{
		dimensions: dimensions,
		maxLevels:  16,
		maxConns:   maxConns,
		mL:         1.0 / math.Log(2.0),
		vectors:    make(map[string][]float32),
		graph:      make(map[int]map[string][]string),
		similarity: core.CosineSimilarity,
	}
}

// randomLevel generates a random level for a new node
func (h *SimpleHNSW) randomLevel() int {
	level := 0
	for rand.Float64() < 0.5 && level < h.maxLevels-1 {
		level++
	}
	return level
}

// Add inserts a vector into the HNSW index
func (h *SimpleHNSW) Add(id string, vector []float32) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.vectors[id] = vector
	level := h.randomLevel()

	// Initialize graph levels if needed
	for l := 0; l <= level; l++ {
		if h.graph[l] == nil {
			h.graph[l] = make(map[string][]string)
		}
		if h.graph[l][id] == nil {
			h.graph[l][id] = make([]string, 0, h.maxConns)
		}
	}

	// Set entry point if this is the first node or higher level
	if h.entryPoint == "" || level > h.getNodeLevel(h.entryPoint) {
		h.entryPoint = id
	}

	// Connect to existing nodes at each level
	for l := level; l >= 0; l-- {
		candidates := h.searchLevel(vector, h.entryPoint, 1, l)
		maxConnections := h.maxConns
		if l == 0 {
			maxConnections = h.maxConns * 2 // More connections at level 0
		}

		// Connect to best candidates
		connected := 0
		for _, candidate := range candidates {
			if candidate.nodeID != id && connected < maxConnections {
				h.addConnection(id, candidate.nodeID, l)
				connected++
			}
		}
	}

	return nil
}

// searchLevel searches for candidates at a specific level
func (h *SimpleHNSW) searchLevel(query []float32, entryPoint string, ef int, level int) []searchCandidate {
	visited := make(map[string]bool)
	candidates := make([]searchCandidate, 0, ef*2)
	
	if entryPoint == "" {
		return candidates
	}

	// Start with entry point
	if vec, exists := h.vectors[entryPoint]; exists {
		score := h.similarity(query, vec)
		candidates = append(candidates, searchCandidate{
			nodeID: entryPoint,
			score:  score,
		})
		visited[entryPoint] = true
	}

	// Beam search
	for len(candidates) < ef*2 {
		if len(candidates) == 0 {
			break
		}

		// Find best unvisited candidate
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].score > candidates[j].score
		})

		expanded := false
		for _, candidate := range candidates {
			if !visited[candidate.nodeID+"_expanded"] {
				visited[candidate.nodeID+"_expanded"] = true
				
				// Explore connections
				if connections, exists := h.graph[level][candidate.nodeID]; exists {
					for _, connID := range connections {
						if !visited[connID] && connID != candidate.nodeID {
							if vec, exists := h.vectors[connID]; exists {
								score := h.similarity(query, vec)
								candidates = append(candidates, searchCandidate{
									nodeID: connID,
									score:  score,
								})
								visited[connID] = true
							}
						}
					}
				}
				expanded = true
				break
			}
		}

		if !expanded {
			break
		}
	}

	// Sort and return top candidates
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	if len(candidates) > ef {
		candidates = candidates[:ef]
	}

	return candidates
}

type searchCandidate struct {
	nodeID string
	score  float64
}

// addConnection adds a bidirectional connection between two nodes at a level
func (h *SimpleHNSW) addConnection(nodeID1, nodeID2 string, level int) {
	if h.graph[level] == nil {
		h.graph[level] = make(map[string][]string)
	}

	// Add connection from node1 to node2
	if h.graph[level][nodeID1] == nil {
		h.graph[level][nodeID1] = make([]string, 0, h.maxConns)
	}
	if len(h.graph[level][nodeID1]) < h.maxConns {
		// Check if already connected
		for _, conn := range h.graph[level][nodeID1] {
			if conn == nodeID2 {
				return
			}
		}
		h.graph[level][nodeID1] = append(h.graph[level][nodeID1], nodeID2)
	}

	// Add connection from node2 to node1
	if h.graph[level][nodeID2] == nil {
		h.graph[level][nodeID2] = make([]string, 0, h.maxConns)
	}
	if len(h.graph[level][nodeID2]) < h.maxConns {
		// Check if already connected
		for _, conn := range h.graph[level][nodeID2] {
			if conn == nodeID1 {
				return
			}
		}
		h.graph[level][nodeID2] = append(h.graph[level][nodeID2], nodeID1)
	}
}

// getNodeLevel returns the highest level for a node
func (h *SimpleHNSW) getNodeLevel(nodeID string) int {
	for level := h.maxLevels - 1; level >= 0; level-- {
		if connections, exists := h.graph[level][nodeID]; exists && len(connections) > 0 {
			return level
		}
	}
	return 0
}

// Search performs HNSW search
func (h *SimpleHNSW) Search(query []float32, k int) []searchCandidate {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	if h.entryPoint == "" {
		return nil
	}

	// Start from top level and work down
	currentNodes := []searchCandidate{{nodeID: h.entryPoint, score: 0}}
	
	// Search from top to level 1
	for level := h.getNodeLevel(h.entryPoint); level > 0; level-- {
		candidates := h.searchLevel(query, h.entryPoint, 1, level)
		if len(candidates) > 0 {
			currentNodes = candidates[:1] // Keep best candidate
		}
	}

	// Search at level 0 with desired ef
	ef := k * 2
	if ef < 50 {
		ef = 50
	}
	
	entryPoint := h.entryPoint
	if len(currentNodes) > 0 {
		entryPoint = currentNodes[0].nodeID
	}
	
	results := h.searchLevel(query, entryPoint, ef, 0)
	
	// Return top k results
	if len(results) > k {
		results = results[:k]
	}
	
	return results
}

// Remove removes a node from the index
func (h *SimpleHNSW) Remove(nodeID string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	delete(h.vectors, nodeID)

	// Remove from all levels
	for level := 0; level < h.maxLevels; level++ {
		if connections, exists := h.graph[level][nodeID]; exists {
			// Remove connections from other nodes
			for _, connID := range connections {
				if otherConns, exists := h.graph[level][connID]; exists {
					for i, conn := range otherConns {
						if conn == nodeID {
							h.graph[level][connID] = append(otherConns[:i], otherConns[i+1:]...)
							break
						}
					}
				}
			}
			delete(h.graph[level], nodeID)
		}
	}

	// Update entry point if needed
	if h.entryPoint == nodeID {
		h.entryPoint = ""
		// Find new entry point
		for id := range h.vectors {
			if h.entryPoint == "" || h.getNodeLevel(id) > h.getNodeLevel(h.entryPoint) {
				h.entryPoint = id
			}
		}
	}
}

// EnableHNSWIndex enables HNSW indexing for the graph store
func (g *GraphStore) EnableHNSWIndex(dimensions int) error {
	g.hnswIndex = &HNSWGraphIndex{
		index: NewSimpleHNSW(dimensions, 16),
	}
	
	// Index existing nodes with vectors
	ctx := context.Background()
	rows, err := g.db.QueryContext(ctx, `
		SELECT id, vector FROM graph_nodes WHERE vector IS NOT NULL
	`)
	if err != nil {
		return fmt.Errorf("failed to query existing nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var nodeID string
		var vectorBytes []byte
		if err := rows.Scan(&nodeID, &vectorBytes); err != nil {
			continue
		}

		vector, err := encoding.DecodeVector(vectorBytes)
		if err != nil {
			continue
		}

		_ = g.hnswIndex.index.Add(nodeID, vector)
	}

	return nil
}

// HNSWSearch performs HNSW-accelerated vector search
func (g *GraphStore) HNSWSearch(ctx context.Context, query []float32, k int, threshold float64) ([]*HybridResult, error) {
	if g.hnswIndex == nil {
		return nil, fmt.Errorf("HNSW index not enabled")
	}

	candidates := g.hnswIndex.index.Search(query, k*2)
	results := make([]*HybridResult, 0, k)

	for _, candidate := range candidates {
		if candidate.score >= threshold && len(results) < k {
			// Get full node data
			node, err := g.GetNode(ctx, candidate.nodeID)
			if err != nil {
				continue
			}

			results = append(results, &HybridResult{
				Node:        node,
				VectorScore: candidate.score,
				GraphScore:  0.0,
				TotalScore:  candidate.score,
			})
		}
	}

	return results, nil
}

// HNSWHybridSearch combines HNSW search with graph proximity
func (g *GraphStore) HNSWHybridSearch(ctx context.Context, query *HybridQuery) ([]*HybridResult, error) {
	if g.hnswIndex == nil {
		// Fall back to regular hybrid search
		return g.HybridSearch(ctx, query)
	}

	// Get HNSW candidates
	candidates := g.hnswIndex.index.Search(query.Vector, query.TopK*3)
	results := make([]*HybridResult, 0, query.TopK)

	// Combine with graph scoring
	for _, candidate := range candidates {
		if candidate.score >= query.VectorThreshold {
			node, err := g.GetNode(ctx, candidate.nodeID)
			if err != nil {
				continue
			}

			// Calculate graph score if center nodes provided
			graphScore := 0.0
			if len(query.CenterNodes) > 0 {
				for _, centerID := range query.CenterNodes {
					distance, err := g.ShortestPath(ctx, centerID, candidate.nodeID)
					if err == nil && distance != nil && len(distance.Nodes) > 0 {
						// Convert path length to score (shorter = higher score)
						pathScore := 1.0 / float64(len(distance.Nodes))
						if pathScore > graphScore {
							graphScore = pathScore
						}
					}
				}
			}

			totalScore := query.VectorWeight*candidate.score + query.GraphWeight*graphScore
			
			if totalScore >= query.TotalThreshold {
				results = append(results, &HybridResult{
					Node:        node,
					VectorScore: candidate.score,
					GraphScore:  graphScore,
					TotalScore:  totalScore,
				})
			}
		}
	}

	// Sort by total score and limit results
	sort.Slice(results, func(i, j int) bool {
		return results[i].TotalScore > results[j].TotalScore
	})

	if len(results) > query.TopK {
		results = results[:query.TopK]
	}

	return results, nil
}

