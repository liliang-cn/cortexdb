package graph

import (
	"github.com/liliang-cn/sqvect/internal/encoding"
	"github.com/liliang-cn/sqvect/pkg/core"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
		totalWeight = 1.0
	} else if math.Abs(totalWeight-1.0) > 0.001 {
		// Normalize to sum to 1.0
		query.Weights.VectorWeight /= totalWeight
		query.Weights.GraphWeight /= totalWeight
		query.Weights.EdgeWeight /= totalWeight
	}

	// Phase 1: Vector similarity search if vector provided
	var vectorResults map[string]float64
	if len(query.Vector) > 0 {
		vectorResults = make(map[string]float64)
		
		// Search all nodes by vector similarity
		allNodes, err := g.GetAllNodes(ctx, query.GraphFilter)
		if err != nil {
			return nil, fmt.Errorf("failed to get nodes: %w", err)
		}

		for _, node := range allNodes {
			score := g.store.GetSimilarityFunc()(query.Vector, node.Vector)
			if query.Threshold == 0 || score >= query.Threshold {
				vectorResults[node.ID] = score
			}
		}
	}

	// Phase 2: Graph traversal if start node provided
	var graphResults map[string]*graphDistance
	if query.StartNodeID != "" {
		graphResults = make(map[string]*graphDistance)
		
		// BFS from start node
		visited := make(map[string]int)
		queue := []struct {
			nodeID   string
			distance int
			weight   float64
		}{{query.StartNodeID, 0, 1.0}}
		
		maxDepth := 3 // Default max depth
		if query.GraphFilter != nil && query.GraphFilter.MaxDepth > 0 {
			maxDepth = query.GraphFilter.MaxDepth
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

			// Get edges
			edges, err := g.GetEdges(ctx, current.nodeID, "out")
			if err != nil {
				continue
			}

			for _, edge := range edges {
				// Filter by edge type
				if query.GraphFilter != nil && len(query.GraphFilter.EdgeTypes) > 0 {
					if !contains(query.GraphFilter.EdgeTypes, edge.EdgeType) {
						continue
					}
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
	}

	// Phase 3: Combine scores
	nodeScores := make(map[string]*HybridResult)

	// Add vector search results
	if vectorResults != nil {
		for nodeID, vectorScore := range vectorResults {
			node, err := g.GetNode(ctx, nodeID)
			if err != nil {
				continue
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
	}

	// Add graph traversal results not in vector results
	if graphResults != nil {
		for nodeID, gd := range graphResults {
			if _, exists := nodeScores[nodeID]; !exists {
				node, err := g.GetNode(ctx, nodeID)
				if err != nil {
					continue
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
	defer rows.Close()

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