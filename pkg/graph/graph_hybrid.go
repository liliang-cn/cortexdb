package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"math"
	"sort"
)

// HybridSearch performs a combined vector and graph search
func (g *GraphStore) HybridSearch(ctx context.Context, query *HybridQuery) ([]*HybridResult, error) {
	if query == nil {
		return nil, fmt.Errorf("query cannot be nil")
	}

	// Normalize weights if needed
	totalWeight := query.Weights.VectorWeight + query.Weights.GraphWeight + query.Weights.EdgeWeight
	if totalWeight == 0 {
		// Default weights if none specified
		query.Weights.VectorWeight = 0.5
		query.Weights.GraphWeight = 0.3
		query.Weights.EdgeWeight = 0.2
	} else if math.Abs(totalWeight-1.0) > 0.001 {
		// Normalize to sum to 1.0
		query.Weights.VectorWeight /= totalWeight
		query.Weights.GraphWeight /= totalWeight
		query.Weights.EdgeWeight /= totalWeight
	}

	// Phase 1: Vector similarity search if vector provided
	vectorResults := make(map[string]float64)
	nodeCache := make(map[string]*GraphNode)
	if len(query.Vector) > 0 {
		nodes, err := g.vectorCandidates(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to get vector candidates: %w", err)
		}

		for _, node := range nodes {
			nodeCache[node.ID] = node
			score := g.store.GetSimilarityFunc()(query.Vector, node.Vector)
			if query.Threshold == 0 || score >= query.Threshold {
				vectorResults[node.ID] = score
			}
		}
	}

	// Phase 2: Graph traversal if start node provided
	var graphResults map[string]*graphDistance
	if query.StartNodeID != "" {
		var err error
		graphResults, err = g.collectGraphDistances(ctx, query.StartNodeID, query.GraphFilter)
		if err != nil {
			return nil, fmt.Errorf("failed to traverse graph: %w", err)
		}
	}

	// Phase 3: Combine scores
	nodeScores := make(map[string]*HybridResult)

	// Add vector search results
	for nodeID, vectorScore := range vectorResults {
		node, ok := nodeCache[nodeID]
		if !ok {
			var err error
			node, err = g.GetNode(ctx, nodeID)
			if err != nil {
				continue
			}
			nodeCache[nodeID] = node
		}

		result := &HybridResult{
			Node:        node,
			VectorScore: vectorScore,
			GraphScore:  0,
			Distance:    -1,
		}

		// Add graph score if available
		if graphResults != nil {
			if gd, exists := graphResults[nodeID]; exists {
				result.GraphScore = 1.0 / float64(gd.distance+1) // Inverse distance
				result.Distance = gd.distance
			}
		}

		result.CombinedScore = result.VectorScore*query.Weights.VectorWeight +
			result.GraphScore*query.Weights.GraphWeight

		nodeScores[nodeID] = result
	}

	// Add graph traversal results not in vector results
	for nodeID, gd := range graphResults {
		if _, exists := nodeScores[nodeID]; !exists {
			node, ok := nodeCache[nodeID]
			if !ok {
				var err error
				node, err = g.GetNode(ctx, nodeID)
				if err != nil {
					continue
				}
				nodeCache[nodeID] = node
			}

			result := &HybridResult{
				Node:        node,
				VectorScore: 0,
				GraphScore:  1.0 / float64(gd.distance+1),
				Distance:    gd.distance,
			}

			// Calculate vector score if query vector provided
			if len(query.Vector) > 0 {
				result.VectorScore = g.store.GetSimilarityFunc()(query.Vector, node.Vector)
			}

			result.CombinedScore = result.VectorScore*query.Weights.VectorWeight +
				result.GraphScore*query.Weights.GraphWeight +
				gd.weight*query.Weights.EdgeWeight

			nodeScores[nodeID] = result
		}
	}

	// Convert to slice and sort by combined score
	results := make([]*HybridResult, 0, len(nodeScores))
	for _, result := range nodeScores {
		results = append(results, result)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].CombinedScore > results[j].CombinedScore
	})

	// Apply TopK limit
	if query.TopK > 0 && len(results) > query.TopK {
		results = results[:query.TopK]
	}

	return results, nil
}

// GraphVectorSearch performs vector search within a graph neighborhood
func (g *GraphStore) GraphVectorSearch(ctx context.Context, startNodeID string, vector []float32, opts TraversalOptions) ([]*HybridResult, error) {
	// First, get neighbors within specified depth
	neighbors, err := g.Neighbors(ctx, startNodeID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get neighbors: %w", err)
	}

	// Add the start node itself
	startNode, err := g.GetNode(ctx, startNodeID)
	if err == nil {
		neighbors = append([]*GraphNode{startNode}, neighbors...)
	}

	// Calculate vector similarity for each neighbor
	results := make([]*HybridResult, 0, len(neighbors))
	for _, node := range neighbors {
		score := g.store.GetSimilarityFunc()(vector, node.Vector)

		results = append(results, &HybridResult{
			Node:          node,
			VectorScore:   score,
			GraphScore:    0, // Not used in this search type
			CombinedScore: score,
			Distance:      -1, // Could be calculated if needed
		})
	}

	// Sort by similarity score
	sort.Slice(results, func(i, j int) bool {
		return results[i].VectorScore > results[j].VectorScore
	})

	// Apply limit if specified
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

// SimilarityInGraph finds nodes similar to a given node within the graph
func (g *GraphStore) SimilarityInGraph(ctx context.Context, nodeID string, opts core.SearchOptions) ([]*HybridResult, error) {
	// Get the node's vector
	node, err := g.GetNode(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get node: %w", err)
	}

	// Get all nodes
	allNodes, err := g.GetAllNodes(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	// Calculate similarity scores
	results := make([]*HybridResult, 0, len(allNodes))
	for _, otherNode := range allNodes {
		if otherNode.ID == nodeID {
			continue // Skip self
		}

		score := g.store.GetSimilarityFunc()(node.Vector, otherNode.Vector)

		if opts.Threshold == 0 || score >= opts.Threshold {
			results = append(results, &HybridResult{
				Node:          otherNode,
				VectorScore:   score,
				GraphScore:    0,
				CombinedScore: score,
				Distance:      -1,
			})
		}
	}

	// Sort by similarity
	sort.Slice(results, func(i, j int) bool {
		return results[i].VectorScore > results[j].VectorScore
	})

	// Apply TopK limit
	if opts.TopK > 0 && len(results) > opts.TopK {
		results = results[:opts.TopK]
	}

	return results, nil
}

// GetAllNodes retrieves all nodes with optional filtering
func (g *GraphStore) GetAllNodes(ctx context.Context, filter *GraphFilter) ([]*GraphNode, error) {
	query := `SELECT id, vector, content, node_type, properties, created_at, updated_at FROM graph_nodes`
	args := []interface{}{}

	if filter != nil && len(filter.NodeTypes) > 0 {
		query += ` WHERE node_type IN (`
		for i := range filter.NodeTypes {
			if i > 0 {
				query += `,`
			}
			query += `?`
			args = append(args, filter.NodeTypes[i])
		}
		query += `)`
	}

	rows, err := g.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var nodes []*GraphNode
	for rows.Next() {
		var node GraphNode
		var vectorBytes []byte
		var propertiesJSON sql.NullString

		err := rows.Scan(
			&node.ID,
			&vectorBytes,
			&node.Content,
			&node.NodeType,
			&propertiesJSON,
			&node.CreatedAt,
			&node.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		// Decode vector
		node.Vector, err = encoding.DecodeVector(vectorBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to decode vector: %w", err)
		}

		// Decode properties
		if propertiesJSON.Valid && propertiesJSON.String != "" {
			err = json.Unmarshal([]byte(propertiesJSON.String), &node.Properties)
			if err != nil {
				return nil, fmt.Errorf("failed to decode properties: %w", err)
			}
		}

		nodes = append(nodes, &node)
	}

	return nodes, rows.Err()
}

type graphDistance struct {
	distance int
	weight   float64
}

func (g *GraphStore) vectorCandidates(ctx context.Context, query *HybridQuery) ([]*GraphNode, error) {
	if g.hnswIndex != nil && query.TopK > 0 {
		candidateLimit := query.TopK * 5
		if candidateLimit < 50 {
			candidateLimit = 50
		}

		candidates := g.hnswIndex.index.Search(query.Vector, candidateLimit)
		nodeIDs := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			nodeIDs = append(nodeIDs, candidate.nodeID)
		}

		nodesByID, err := g.getNodesByIDs(ctx, nodeIDs)
		if err != nil {
			return nil, err
		}

		nodes := make([]*GraphNode, 0, len(candidates))
		for _, candidate := range candidates {
			node, ok := nodesByID[candidate.nodeID]
			if !ok {
				continue
			}
			if query.GraphFilter != nil && len(query.GraphFilter.NodeTypes) > 0 && !contains(query.GraphFilter.NodeTypes, node.NodeType) {
				continue
			}
			nodes = append(nodes, node)
		}
		if len(nodes) > 0 {
			return nodes, nil
		}
	}

	return g.GetAllNodes(ctx, query.GraphFilter)
}

func (g *GraphStore) collectGraphDistances(ctx context.Context, startNodeID string, filter *GraphFilter) (map[string]*graphDistance, error) {
	graphResults := make(map[string]*graphDistance)

	visited := make(map[string]int)
	queue := []struct {
		nodeID   string
		distance int
		weight   float64
	}{{startNodeID, 0, 1.0}}

	maxDepth := 3
	if filter != nil && filter.MaxDepth > 0 {
		maxDepth = filter.MaxDepth
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.distance > maxDepth {
			continue
		}

		if prevDist, exists := visited[current.nodeID]; exists && prevDist <= current.distance {
			continue
		}
		visited[current.nodeID] = current.distance

		graphResults[current.nodeID] = &graphDistance{
			distance: current.distance,
			weight:   current.weight,
		}

		edges, err := g.GetEdges(ctx, current.nodeID, "out")
		if err != nil {
			return nil, err
		}

		for _, edge := range edges {
			if filter != nil && len(filter.EdgeTypes) > 0 && !contains(filter.EdgeTypes, edge.EdgeType) {
				continue
			}

			queue = append(queue, struct {
				nodeID   string
				distance int
				weight   float64
			}{
				nodeID:   edge.ToNodeID,
				distance: current.distance + 1,
				weight:   current.weight * edge.Weight,
			})
		}
	}

	if filter != nil && len(filter.NodeTypes) > 0 {
		nodeIDs := make([]string, 0, len(graphResults))
		for nodeID := range graphResults {
			nodeIDs = append(nodeIDs, nodeID)
		}
		nodesByID, err := g.getNodesByIDs(ctx, nodeIDs)
		if err != nil {
			return nil, err
		}
		for nodeID := range graphResults {
			node, ok := nodesByID[nodeID]
			if !ok || !contains(filter.NodeTypes, node.NodeType) {
				delete(graphResults, nodeID)
			}
		}
	}

	return graphResults, nil
}
