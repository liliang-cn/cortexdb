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
// Optimized to load only topology (IDs and Edges) instead of full node objects.
func (g *GraphStore) PageRank(ctx context.Context, iterations int, dampingFactor float64) ([]PageRankResult, error) {
	if iterations <= 0 {
		iterations = 100
	}
	if dampingFactor <= 0 || dampingFactor > 1 {
		dampingFactor = 0.85
	}

	// 1. Load Topology (IDs)
	rows, err := g.db.QueryContext(ctx, "SELECT id FROM graph_nodes")
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	
	var nodes []string
	nodeToIndex := make(map[string]int)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		nodeToIndex[id] = len(nodes)
		nodes = append(nodes, id)
	}
	rows.Close()

	if len(nodes) == 0 {
		return []PageRankResult{}, nil
	}

	// 2. Load Topology (Edges)
	edgeRows, err := g.db.QueryContext(ctx, "SELECT from_node_id, to_node_id FROM graph_edges")
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	
	outDegree := make([]int, len(nodes))
	inLinks := make([][]int, len(nodes))

	for edgeRows.Next() {
		var from, to string
		if err := edgeRows.Scan(&from, &to); err != nil {
			edgeRows.Close()
			return nil, err
		}
		
		u, ok1 := nodeToIndex[from]
		v, ok2 := nodeToIndex[to]
		if ok1 && ok2 {
			outDegree[u]++
			inLinks[v] = append(inLinks[v], u)
		}
	}
	edgeRows.Close()

	// 3. Compute PageRank
	nodeCount := float64(len(nodes))
	scores := make([]float64, len(nodes))
	newScores := make([]float64, len(nodes))
	initialScore := 1.0 / nodeCount

	for i := range scores {
		scores[i] = initialScore
	}

	for iter := 0; iter < iterations; iter++ {
		maxDiff := 0.0
		
		for i := 0; i < len(nodes); i++ {
			rank := (1.0 - dampingFactor) / nodeCount
			
			// Sum contributions from incoming links
			for _, inIdx := range inLinks[i] {
				outDeg := outDegree[inIdx]
				if outDeg > 0 {
					rank += dampingFactor * scores[inIdx] / float64(outDeg)
				}
			}
			
			newScores[i] = rank
			diff := math.Abs(newScores[i] - scores[i])
			if diff > maxDiff {
				maxDiff = diff
			}
		}

		copy(scores, newScores)
		if maxDiff < 1e-6 {
			break
		}
	}

	// 4. Convert to results
	results := make([]PageRankResult, len(nodes))
	for i, id := range nodes {
		results[i] = PageRankResult{
			NodeID: id,
			Score:  scores[i],
		}
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
// Optimized to reduce DB queries.
func (g *GraphStore) CommunityDetection(ctx context.Context) ([]Community, error) {
	// 1. Load Nodes
	rows, err := g.db.QueryContext(ctx, "SELECT id FROM graph_nodes")
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}

	var nodes []string
	nodeToIndex := make(map[string]int)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		nodeToIndex[id] = len(nodes)
		nodes = append(nodes, id)
	}
	rows.Close()

	if len(nodes) == 0 {
		return []Community{}, nil
	}

	// 2. Load Edges (Weighted)
	// We need an adjacency map for weights: u -> v -> weight
	adj := make([]map[int]float64, len(nodes))
	for i := range adj {
		adj[i] = make(map[int]float64)
	}

	edgeRows, err := g.db.QueryContext(ctx, "SELECT from_node_id, to_node_id, weight FROM graph_edges")
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}

	for edgeRows.Next() {
		var from, to string
		var weight float64
		if err := edgeRows.Scan(&from, &to, &weight); err != nil {
			edgeRows.Close()
			return nil, err
		}
		
		u, ok1 := nodeToIndex[from]
		v, ok2 := nodeToIndex[to]
		if ok1 && ok2 {
			// Directed to Undirected (or sum weights)
			adj[u][v] += weight
			adj[v][u] += weight // Treat as undirected for community detection often works better
		}
	}
	edgeRows.Close()

	// 3. Louvain Algorithm (Simplified)
	communities := make([]int, len(nodes))
	for i := range communities {
		communities[i] = i
	}

	changed := true
	iterations := 0
	maxIterations := 100

	for changed && iterations < maxIterations {
		changed = false
		iterations++

		for i := 0; i < len(nodes); i++ {
			currentComm := communities[i]
			bestComm := currentComm
			bestGain := 0.0

			// Calculate connection strength to each neighboring community
			commWeights := make(map[int]float64)
			for neighbor, weight := range adj[i] {
				commWeights[communities[neighbor]] += weight
			}

			// Find best community
			for comm, weight := range commWeights {
				if comm != currentComm {
					// Simplified gain: just raw weight connection
					if weight > bestGain {
						bestGain = weight
						bestComm = comm
					}
				}
			}

			if bestComm != currentComm {
				communities[i] = bestComm
				changed = true
			}
		}
	}

	// 4. Group results
	commGroups := make(map[int][]string)
	for i, commID := range communities {
		commGroups[commID] = append(commGroups[commID], nodes[i])
	}

	results := make([]Community, 0, len(commGroups))
	idCounter := 0
	for _, groupNodes := range commGroups {
		results = append(results, Community{
			ID:    idCounter,
			Nodes: groupNodes,
			Score: float64(len(groupNodes)) / float64(len(nodes)),
		})
		idCounter++
	}

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

	// Get target node
	node, err := g.GetNode(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get node: %w", err)
	}

	// Pre-fetch all edges to avoid N+1 queries
	// We only need edges connected to 'nodeID' (already got via GetNode -> GetEdges usually, 
	// but here we need 2-hop neighbors or all edges for 'common neighbors')
	// For "common neighbors", we really need the full graph topology or at least 
	// the neighbors of my neighbors.
	
	// Let's rely on GetAllNodes for vectors (needed for similarity)
	// But optimize the structural check.
	
	allNodes, err := g.GetAllNodes(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	// Load topology efficiently
	edgeRows, err := g.db.QueryContext(ctx, "SELECT from_node_id, to_node_id FROM graph_edges")
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer edgeRows.Close()

	// Adjacency map: node -> set of neighbors
	adj := make(map[string]map[string]bool)
	for edgeRows.Next() {
		var u, v string
		if err := edgeRows.Scan(&u, &v); err == nil {
			if adj[u] == nil { adj[u] = make(map[string]bool) }
			if adj[v] == nil { adj[v] = make(map[string]bool) }
			adj[u][v] = true
			adj[v][u] = true // Treat as undirected for common neighbors
		}
	}

	existingConnections := adj[nodeID]
	if existingConnections == nil {
		existingConnections = make(map[string]bool)
	}

	predictions := make([]EdgePrediction, 0)

	for _, otherNode := range allNodes {
		if otherNode.ID == nodeID || existingConnections[otherNode.ID] {
			continue
		}

		// Method 1: Vector Similarity
		similarity := g.store.GetSimilarityFunc()(node.Vector, otherNode.Vector)
		
		// Method 2: Common Neighbors
		commonNeighbors := 0
		for neighbor := range existingConnections {
			if adj[otherNode.ID][neighbor] {
				commonNeighbors++
			}
		}

		score := similarity
		method := "vector_similarity"

		if commonNeighbors > 0 {
			cnScore := float64(commonNeighbors) / float64(len(existingConnections)+1)
			score = (similarity + cnScore) / 2
			method = "combined"
		}

		if score > 0.5 {
			predictions = append(predictions, EdgePrediction{
				FromNodeID: nodeID,
				ToNodeID:   otherNode.ID,
				Score:      score,
				Method:     method,
			})
		}
	}

	sort.Slice(predictions, func(i, j int) bool {
		return predictions[i].Score > predictions[j].Score
	})

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
	stats := &GraphStatistics{}

	// 1. Counts via SQL (Fast)
	err := g.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_nodes").Scan(&stats.NodeCount)
	if err != nil {
		return nil, err
	}
	if stats.NodeCount == 0 {
		return stats, nil
	}

	err = g.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_edges").Scan(&stats.EdgeCount)
	if err != nil {
		return nil, err
	}

	stats.AverageDegree = 2.0 * float64(stats.EdgeCount) / float64(stats.NodeCount)
	
	maxEdges := float64(stats.NodeCount) * float64(stats.NodeCount - 1)
	if maxEdges > 0 {
		stats.Density = float64(stats.EdgeCount) / maxEdges
	}

	// 2. Connected Components (BFS)
	// Need topology for this
	edgeRows, err := g.db.QueryContext(ctx, "SELECT from_node_id, to_node_id FROM graph_edges")
	if err != nil {
		return nil, err
	}
	
	adj := make(map[string][]string)
	for edgeRows.Next() {
		var u, v string
		edgeRows.Scan(&u, &v)
		adj[u] = append(adj[u], v)
		adj[v] = append(adj[v], u) // Undirected traversal
	}
	edgeRows.Close()

	// Get all IDs for traversal
	idRows, err := g.db.QueryContext(ctx, "SELECT id FROM graph_nodes")
	if err != nil {
		return nil, err
	}
	var allIDs []string
	for idRows.Next() {
		var id string
		idRows.Scan(&id)
		allIDs = append(allIDs, id)
	}
	idRows.Close()

	visited := make(map[string]bool)
	components := 0

	for _, id := range allIDs {
		if !visited[id] {
			components++
			// BFS
			queue := []string{id}
			visited[id] = true
			
			for len(queue) > 0 {
				curr := queue[0]
				queue = queue[1:]
				
				for _, neighbor := range adj[curr] {
					if !visited[neighbor] {
						visited[neighbor] = true
						queue = append(queue, neighbor)
					}
				}
			}
		}
	}
	
	stats.ConnectedComponents = components

	return stats, nil
}
