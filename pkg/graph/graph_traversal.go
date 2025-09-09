package graph

import (
	"github.com/liliang-cn/sqvect/internal/encoding"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// TraversalOptions defines options for graph traversal
type TraversalOptions struct {
	MaxDepth   int      `json:"max_depth"`
	EdgeTypes  []string `json:"edge_types,omitempty"`
	NodeTypes  []string `json:"node_types,omitempty"`
	Direction  string   `json:"direction"` // "out", "in", "both"
	Limit      int      `json:"limit"`
}

// PathResult represents a path in the graph
type PathResult struct {
	Nodes    []*GraphNode `json:"nodes"`
	Edges    []*GraphEdge `json:"edges"`
	Distance int          `json:"distance"`
	Weight   float64      `json:"weight"`
}

// Neighbors performs a breadth-first search to find neighboring nodes
func (g *GraphStore) Neighbors(ctx context.Context, nodeID string, opts TraversalOptions) ([]*GraphNode, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 1
	}
	if opts.Direction == "" {
		opts.Direction = "both"
	}

	visited := make(map[string]bool)
	queue := []struct {
		nodeID string
		depth  int
	}{{nodeID, 0}}
	
	var neighbors []*GraphNode
	visited[nodeID] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= opts.MaxDepth {
			continue
		}

		// Get edges for current node
		edges, err := g.GetEdges(ctx, current.nodeID, opts.Direction)
		if err != nil {
			return nil, fmt.Errorf("failed to get edges: %w", err)
		}

		for _, edge := range edges {
			// Filter by edge type if specified
			if len(opts.EdgeTypes) > 0 && !contains(opts.EdgeTypes, edge.EdgeType) {
				continue
			}

			// Determine the neighbor node ID
			var neighborID string
			if edge.FromNodeID == current.nodeID {
				neighborID = edge.ToNodeID
			} else {
				neighborID = edge.FromNodeID
			}

			// Skip if already visited
			if visited[neighborID] {
				continue
			}

			// Mark as visited
			visited[neighborID] = true

			// Get the neighbor node
			node, err := g.GetNode(ctx, neighborID)
			if err != nil {
				continue // Skip if node not found
			}

			// Filter by node type if specified
			if len(opts.NodeTypes) > 0 && !contains(opts.NodeTypes, node.NodeType) {
				continue
			}

			neighbors = append(neighbors, node)

			// Add to queue for further traversal
			if current.depth+1 < opts.MaxDepth {
				queue = append(queue, struct {
					nodeID string
					depth  int
				}{neighborID, current.depth + 1})
			}

			// Check limit
			if opts.Limit > 0 && len(neighbors) >= opts.Limit {
				return neighbors, nil
			}
		}
	}

	return neighbors, nil
}

// ShortestPath finds the shortest path between two nodes using BFS
func (g *GraphStore) ShortestPath(ctx context.Context, fromID, toID string) (*PathResult, error) {
	if fromID == toID {
		node, err := g.GetNode(ctx, fromID)
		if err != nil {
			return nil, err
		}
		return &PathResult{
			Nodes:    []*GraphNode{node},
			Edges:    []*GraphEdge{},
			Distance: 0,
			Weight:   0,
		}, nil
	}

	type queueItem struct {
		nodeID   string
		path     []string
		edges    []string
		distance int
		weight   float64
	}

	visited := make(map[string]bool)
	queue := []queueItem{{
		nodeID:   fromID,
		path:     []string{fromID},
		edges:    []string{},
		distance: 0,
		weight:   0,
	}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.nodeID == toID {
			// Found the target, reconstruct the path
			result := &PathResult{
				Nodes:    make([]*GraphNode, 0, len(current.path)),
				Edges:    make([]*GraphEdge, 0, len(current.edges)),
				Distance: current.distance,
				Weight:   current.weight,
			}

			// Get all nodes in the path
			for _, nodeID := range current.path {
				node, err := g.GetNode(ctx, nodeID)
				if err != nil {
					return nil, err
				}
				result.Nodes = append(result.Nodes, node)
			}

			// Get all edges in the path
			for _, edgeID := range current.edges {
				edge, err := g.getEdgeByID(ctx, edgeID)
				if err != nil {
					return nil, err
				}
				result.Edges = append(result.Edges, edge)
			}

			return result, nil
		}

		if visited[current.nodeID] {
			continue
		}
		visited[current.nodeID] = true

		// Get edges from current node
		edges, err := g.GetEdges(ctx, current.nodeID, "out")
		if err != nil {
			return nil, err
		}

		for _, edge := range edges {
			if !visited[edge.ToNodeID] {
				newPath := append([]string{}, current.path...)
				newPath = append(newPath, edge.ToNodeID)
				
				newEdges := append([]string{}, current.edges...)
				newEdges = append(newEdges, edge.ID)

				queue = append(queue, queueItem{
					nodeID:   edge.ToNodeID,
					path:     newPath,
					edges:    newEdges,
					distance: current.distance + 1,
					weight:   current.weight + edge.Weight,
				})
			}
		}
	}

	return nil, fmt.Errorf("no path found from %s to %s", fromID, toID)
}

// Subgraph extracts a subgraph containing specified nodes and their connections
func (g *GraphStore) Subgraph(ctx context.Context, nodeIDs []string) (*GraphResult, error) {
	nodeSet := make(map[string]bool)
	for _, id := range nodeIDs {
		nodeSet[id] = true
	}

	nodes := make([]*GraphNode, 0, len(nodeIDs))
	edges := make([]*GraphEdge, 0)

	// Get all nodes
	for _, nodeID := range nodeIDs {
		node, err := g.GetNode(ctx, nodeID)
		if err != nil {
			continue // Skip if node not found
		}
		nodes = append(nodes, node)
	}

	// Get edges between nodes in the subgraph
	for _, nodeID := range nodeIDs {
		nodeEdges, err := g.GetEdges(ctx, nodeID, "out")
		if err != nil {
			continue
		}

		for _, edge := range nodeEdges {
			// Only include edges where both nodes are in the subgraph
			if nodeSet[edge.ToNodeID] {
				edges = append(edges, edge)
			}
		}
	}

	return &GraphResult{
		Nodes: nodes,
		Edges: edges,
	}, nil
}

// GraphResult represents a subgraph or query result
type GraphResult struct {
	Nodes []*GraphNode `json:"nodes"`
	Edges []*GraphEdge `json:"edges"`
}

// Connected checks if two nodes are connected within a given depth
func (g *GraphStore) Connected(ctx context.Context, nodeID1, nodeID2 string, maxDepth int) (bool, error) {
	if nodeID1 == nodeID2 {
		return true, nil
	}

	if maxDepth <= 0 {
		maxDepth = 6 // Default to 6 degrees of separation
	}

	visited := make(map[string]bool)
	queue := []struct {
		nodeID string
		depth  int
	}{{nodeID1, 0}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.nodeID == nodeID2 {
			return true, nil
		}

		if current.depth >= maxDepth {
			continue
		}

		if visited[current.nodeID] {
			continue
		}
		visited[current.nodeID] = true

		edges, err := g.GetEdges(ctx, current.nodeID, "both")
		if err != nil {
			return false, err
		}

		for _, edge := range edges {
			var nextNodeID string
			if edge.FromNodeID == current.nodeID {
				nextNodeID = edge.ToNodeID
			} else {
				nextNodeID = edge.FromNodeID
			}

			if !visited[nextNodeID] {
				queue = append(queue, struct {
					nodeID string
					depth  int
				}{nextNodeID, current.depth + 1})
			}
		}
	}

	return false, nil
}

// getEdgeByID retrieves an edge by its ID
func (g *GraphStore) getEdgeByID(ctx context.Context, edgeID string) (*GraphEdge, error) {
	query := `
	SELECT id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at
	FROM graph_edges
	WHERE id = ?
	`

	var edge GraphEdge
	var propertiesJSON sql.NullString
	var vectorBytes []byte

	err := g.db.QueryRowContext(ctx, query, edgeID).Scan(
		&edge.ID,
		&edge.FromNodeID,
		&edge.ToNodeID,
		&edge.EdgeType,
		&edge.Weight,
		&propertiesJSON,
		&vectorBytes,
		&edge.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("edge not found: %s", edgeID)
	}
	if err != nil {
		return nil, err
	}

	// Decode properties
	if propertiesJSON.Valid && propertiesJSON.String != "" {
		err = json.Unmarshal([]byte(propertiesJSON.String), &edge.Properties)
		if err != nil {
			return nil, fmt.Errorf("failed to decode properties: %w", err)
		}
	}

	// Decode vector if present
	if len(vectorBytes) > 0 {
		edge.Vector, err = encoding.DecodeVector(vectorBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to decode vector: %w", err)
		}
	}

	return &edge, nil
}

// contains checks if a string slice contains a value
func contains(slice []string, value string) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
	}
	return false
}