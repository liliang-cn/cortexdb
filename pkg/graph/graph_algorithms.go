package graph

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// PageRankResult represents the PageRank score for a node
type PageRankResult struct {
	NodeID string  `json:"node_id"`
	Score  float64 `json:"score"`
}

// PageRank calculates PageRank scores for all nodes in the graph
func (g *GraphStore) PageRank(ctx context.Context, iterations int, dampingFactor float64) ([]PageRankResult, error) {
	if iterations <= 0 {
		iterations = 100
	}
	if dampingFactor <= 0 || dampingFactor > 1 {
		dampingFactor = 0.85
	}

	// Get all nodes
	allNodes, err := g.GetAllNodes(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	if len(allNodes) == 0 {
		return []PageRankResult{}, nil
	}

	// Initialize PageRank scores
	scores := make(map[string]float64)
	newScores := make(map[string]float64)
	nodeCount := float64(len(allNodes))
	initialScore := 1.0 / nodeCount

	for _, node := range allNodes {
		scores[node.ID] = initialScore
	}

	// Build adjacency lists
	outLinks := make(map[string][]string)
	inLinks := make(map[string][]string)

	for _, node := range allNodes {
		edges, err := g.GetEdges(ctx, node.ID, "out")
		if err != nil {
			continue
		}

		for _, edge := range edges {
			outLinks[node.ID] = append(outLinks[node.ID], edge.ToNodeID)
			inLinks[edge.ToNodeID] = append(inLinks[edge.ToNodeID], node.ID)
		}
	}

	// Iterative PageRank calculation
	for iter := 0; iter < iterations; iter++ {
		// Calculate new scores
		for _, node := range allNodes {
			rank := (1.0 - dampingFactor) / nodeCount
			
			// Sum contributions from incoming links
			for _, inNode := range inLinks[node.ID] {
				outCount := len(outLinks[inNode])
				if outCount > 0 {
					rank += dampingFactor * scores[inNode] / float64(outCount)
				}
			}
			
			newScores[node.ID] = rank
		}

		// Check convergence
		maxDiff := 0.0
		for nodeID := range scores {
			diff := math.Abs(newScores[nodeID] - scores[nodeID])
			if diff > maxDiff {
				maxDiff = diff
			}
			scores[nodeID] = newScores[nodeID]
		}

		// Early termination if converged
		if maxDiff < 1e-6 {
			break
		}
	}

	// Convert to sorted results
	results := make([]PageRankResult, 0, len(scores))
	for nodeID, score := range scores {
		results = append(results, PageRankResult{
			NodeID: nodeID,
			Score:  score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// Community represents a detected community of nodes
type Community struct {
	ID    int      `json:"id"`
	Nodes []string `json:"nodes"`
	Score float64  `json:"score"` // Modularity score
}

// CommunityDetection performs community detection using the Louvain method
func (g *GraphStore) CommunityDetection(ctx context.Context) ([]Community, error) {
	// Get all nodes
	allNodes, err := g.GetAllNodes(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	if len(allNodes) == 0 {
		return []Community{}, nil
	}

	// Initialize each node in its own community
	communities := make(map[string]int)
	for i, node := range allNodes {
		communities[node.ID] = i
	}

	// Build weighted adjacency matrix
	weights := make(map[string]map[string]float64)
	totalWeight := 0.0

	for _, node := range allNodes {
		weights[node.ID] = make(map[string]float64)
		edges, err := g.GetEdges(ctx, node.ID, "out")
		if err != nil {
			continue
		}

		for _, edge := range edges {
			weights[node.ID][edge.ToNodeID] = edge.Weight
			totalWeight += edge.Weight
		}
	}

	// Simple community detection based on edge density
	// (Simplified version - real Louvain is more complex)
	changed := true
	iterations := 0
	maxIterations := 100

	for changed && iterations < maxIterations {
		changed = false
		iterations++

		for _, node := range allNodes {
			currentCommunity := communities[node.ID]
			bestCommunity := currentCommunity
			bestGain := 0.0

			// Check neighboring communities
			neighbors := make(map[int]float64)
			edges, _ := g.GetEdges(ctx, node.ID, "both")
			
			for _, edge := range edges {
				var neighborID string
				if edge.FromNodeID == node.ID {
					neighborID = edge.ToNodeID
				} else {
					neighborID = edge.FromNodeID
				}
				
				neighborComm := communities[neighborID]
				neighbors[neighborComm] += edge.Weight
			}

			// Find best community to move to
			for comm, weight := range neighbors {
				if comm != currentCommunity {
					gain := weight // Simplified gain calculation
					if gain > bestGain {
						bestGain = gain
						bestCommunity = comm
					}
				}
			}

			// Move to best community if gain is positive
			if bestCommunity != currentCommunity {
				communities[node.ID] = bestCommunity
				changed = true
			}
		}
	}

	// Group nodes by community
	communityGroups := make(map[int][]string)
	for nodeID, commID := range communities {
		communityGroups[commID] = append(communityGroups[commID], nodeID)
	}

	// Convert to Community structs
	results := make([]Community, 0, len(communityGroups))
	communityID := 0
	for _, nodes := range communityGroups {
		results = append(results, Community{
			ID:    communityID,
			Nodes: nodes,
			Score: float64(len(nodes)) / float64(len(allNodes)), // Simple score
		})
		communityID++
	}

	// Sort by community size
	sort.Slice(results, func(i, j int) bool {
		return len(results[i].Nodes) > len(results[j].Nodes)
	})

	return results, nil
}

// EdgePrediction represents a predicted edge with confidence score
type EdgePrediction struct {
	FromNodeID string  `json:"from_node_id"`
	ToNodeID   string  `json:"to_node_id"`
	Score      float64 `json:"score"`
	Method     string  `json:"method"`
}

// PredictEdges predicts potential edges using various methods
func (g *GraphStore) PredictEdges(ctx context.Context, nodeID string, topK int) ([]EdgePrediction, error) {
	if topK <= 0 {
		topK = 10
	}

	// Get the node
	node, err := g.GetNode(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get node: %w", err)
	}

	// Get existing connections
	existingEdges, err := g.GetEdges(ctx, nodeID, "both")
	if err != nil {
		return nil, fmt.Errorf("failed to get edges: %w", err)
	}

	existingConnections := make(map[string]bool)
	for _, edge := range existingEdges {
		if edge.FromNodeID == nodeID {
			existingConnections[edge.ToNodeID] = true
		} else {
			existingConnections[edge.FromNodeID] = true
		}
	}

	// Get all nodes
	allNodes, err := g.GetAllNodes(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	predictions := make([]EdgePrediction, 0)

	// Method 1: Vector similarity
	for _, otherNode := range allNodes {
		if otherNode.ID == nodeID || existingConnections[otherNode.ID] {
			continue
		}

		similarity := g.store.GetSimilarityFunc()(node.Vector, otherNode.Vector)
		if similarity > 0.5 { // Threshold
			predictions = append(predictions, EdgePrediction{
				FromNodeID: nodeID,
				ToNodeID:   otherNode.ID,
				Score:      similarity,
				Method:     "vector_similarity",
			})
		}
	}

	// Method 2: Common neighbors (for existing connections)
	for _, otherNode := range allNodes {
		if otherNode.ID == nodeID || existingConnections[otherNode.ID] {
			continue
		}

		// Count common neighbors
		otherEdges, err := g.GetEdges(ctx, otherNode.ID, "both")
		if err != nil {
			continue
		}

		commonNeighbors := 0
		for _, edge := range otherEdges {
			var neighborID string
			if edge.FromNodeID == otherNode.ID {
				neighborID = edge.ToNodeID
			} else {
				neighborID = edge.FromNodeID
			}

			if existingConnections[neighborID] {
				commonNeighbors++
			}
		}

		if commonNeighbors > 0 {
			score := float64(commonNeighbors) / float64(len(existingConnections)+1)
			
			// Check if we already have a prediction for this pair
			found := false
			for i, pred := range predictions {
				if pred.ToNodeID == otherNode.ID {
					// Combine scores
					predictions[i].Score = (pred.Score + score) / 2
					predictions[i].Method = "combined"
					found = true
					break
				}
			}

			if !found {
				predictions = append(predictions, EdgePrediction{
					FromNodeID: nodeID,
					ToNodeID:   otherNode.ID,
					Score:      score,
					Method:     "common_neighbors",
				})
			}
		}
	}

	// Sort by score
	sort.Slice(predictions, func(i, j int) bool {
		return predictions[i].Score > predictions[j].Score
	})

	// Return top K
	if len(predictions) > topK {
		predictions = predictions[:topK]
	}

	return predictions, nil
}

// GraphStatistics represents overall graph statistics
type GraphStatistics struct {
	NodeCount        int     `json:"node_count"`
	EdgeCount        int     `json:"edge_count"`
	AverageDegree    float64 `json:"average_degree"`
	Density          float64 `json:"density"`
	ConnectedComponents int  `json:"connected_components"`
}

// GetGraphStatistics computes statistics about the graph
func (g *GraphStore) GetGraphStatistics(ctx context.Context) (*GraphStatistics, error) {
	// Get all nodes
	allNodes, err := g.GetAllNodes(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	nodeCount := len(allNodes)
	if nodeCount == 0 {
		return &GraphStatistics{}, nil
	}

	// Count edges and degrees
	edgeCount := 0
	totalDegree := 0

	for _, node := range allNodes {
		edges, err := g.GetEdges(ctx, node.ID, "out")
		if err != nil {
			continue
		}
		edgeCount += len(edges)
		totalDegree += len(edges)
		
		// Also count in-edges to get total degree
		inEdges, err := g.GetEdges(ctx, node.ID, "in")
		if err != nil {
			continue
		}
		totalDegree += len(inEdges)
	}

	// Calculate statistics
	stats := &GraphStatistics{
		NodeCount:     nodeCount,
		EdgeCount:     edgeCount,
		AverageDegree: float64(totalDegree) / float64(nodeCount),
	}

	// Calculate density (for directed graph)
	maxPossibleEdges := nodeCount * (nodeCount - 1)
	if maxPossibleEdges > 0 {
		stats.Density = float64(edgeCount) / float64(maxPossibleEdges)
	}

	// Count connected components (simplified - treats as undirected)
	visited := make(map[string]bool)
	components := 0

	for _, node := range allNodes {
		if !visited[node.ID] {
			components++
			// BFS to mark all connected nodes
			queue := []string{node.ID}
			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]
				
				if visited[current] {
					continue
				}
				visited[current] = true

				edges, _ := g.GetEdges(ctx, current, "both")
				for _, edge := range edges {
					var nextID string
					if edge.FromNodeID == current {
						nextID = edge.ToNodeID
					} else {
						nextID = edge.FromNodeID
					}
					
					if !visited[nextID] {
						queue = append(queue, nextID)
					}
				}
			}
		}
	}

	stats.ConnectedComponents = components

	return stats, nil
}