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
	"strings"
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
	where, args := g.nodeWhere(filter)
	query := `SELECT id, vector, content, node_type, properties, created_at, updated_at FROM graph_nodes` + where
	if filter != nil && filter.Limit > 0 {
		// Ordered only when capped. A window into a table has to be the same
		// window twice or paging through it is a lie, and SQLite's unordered
		// row order is not a promise. An uncapped scan keeps the order — and
		// the cost — it had before this field existed, because every caller
		// of it reads the whole table anyway and a sort would be pure tax.
		query += ` ORDER BY id LIMIT ?`
		args = append(args, filter.Limit)
	}

	rows, err := g.query(ctx, query, args...)
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
	// In-database first when it is available: the alternative below this is
	// GetAllNodes, which reads the whole table into memory and scores it in
	// Go. That is the difference this backend exists to make.
	if g.vecCap.Enabled && len(query.Vector) > 0 {
		limit := query.TopK * 5
		if limit < 50 {
			limit = 50
		}
		nodes, err := g.pgVectorCandidates(ctx, query, limit)
		if err != nil {
			return nil, err
		}
		if len(nodes) > 0 {
			return nodes, nil
		}
	}

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

// nodeWhere builds the WHERE clause GetAllNodes and CountNodes share.
//
// They share it because the two answers have to be about the same rows. A
// count computed from a different predicate than the list it accompanies is
// worse than no count: it reads as authoritative and disagrees with what the
// caller can see.
//
// The property keys are sorted so the same filter produces the same SQL text
// twice — map iteration order is random in Go, and a query string that varies
// run to run defeats every statement cache the drivers keep.
func (g *GraphStore) nodeWhere(filter *GraphFilter) (string, []interface{}) {
	if filter == nil {
		return "", nil
	}
	var clauses []string
	var args []interface{}

	if len(filter.NodeTypes) > 0 {
		holes := make([]string, len(filter.NodeTypes))
		for i, t := range filter.NodeTypes {
			holes[i] = "?"
			args = append(args, t)
		}
		clauses = append(clauses, "node_type IN ("+strings.Join(holes, ",")+")")
	}

	keys := make([]string, 0, len(filter.Properties))
	for k := range filter.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Guarded rather than raw: properties is a TEXT column and a node
		// written without any carries the empty string, on which SQLite's
		// json_extract raises "malformed JSON" and PostgreSQL's ::jsonb
		// raises "invalid input syntax". Either kills the whole query over
		// one unrelated row. Guarded, such a row yields NULL, which fails the
		// comparison and is exactly right — a node with no properties is not
		// a node in anybody's batch.
		clauses = append(clauses, g.dialect.JSONTextGuarded("properties", k)+" = ?")
		args = append(args, filter.Properties[k])
	}

	ckeys := make([]string, 0, len(filter.Contains))
	for k, v := range filter.Contains {
		if v != "" {
			ckeys = append(ckeys, k)
		}
	}
	sort.Strings(ckeys)
	for _, k := range ckeys {
		// LOWER on both sides rather than relying on LIKE: SQLite's LIKE folds
		// ASCII case by default and PostgreSQL's does not, so the same filter
		// would be case-insensitive on one database and case-sensitive on the
		// other — a difference that shows up as a search finding nothing in
		// production and everything in the tests.
		clauses = append(clauses,
			"LOWER("+g.dialect.JSONTextGuarded("properties", k)+") LIKE LOWER(?) ESCAPE '\\'")
		args = append(args, "%"+likeLiteral(filter.Contains[k])+"%")
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// likeLiteral escapes the three characters LIKE reads as syntax.
//
// The text being matched is whatever a person typed into a search box. A name
// with an underscore in it is ordinary — column names and identifiers are full
// of them — and unescaped it is LIKE's single-character wildcard, so searching
// for "user_id" would also match "userxid". The backslash goes first, or it
// would escape the escapes added after it.
func likeLiteral(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return r.Replace(s)
}

// CountNodes reports how many nodes match a filter, ignoring its Limit.
//
// It is the other half of GraphFilter.Limit and it is not optional decoration.
// A caller that asks for 100 nodes and is handed 100 has learned nothing about
// whether there were 100 or 4000, and the honest failure — "showing 100 of
// 4000" — is unavailable without this. Callers that report a total to a person
// or to a model should ask for both.
//
// Limit is ignored on purpose: a count that stopped at the cap would always
// equal the cap, which is the exact non-answer this method exists to replace.
func (g *GraphStore) CountNodes(ctx context.Context, filter *GraphFilter) (int, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return 0, err
	}
	where, args := g.nodeWhere(filter)
	var n int
	if err := g.queryRow(ctx, `SELECT COUNT(*) FROM graph_nodes`+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("cortexdb: count nodes: %w", err)
	}
	return n, nil
}

// ListNodes is GetAllNodes without the vectors.
//
// The projection is the whole of it, and it is not a micro-optimisation. A
// vector is the largest column on a node — 768 floats is three kilobytes — and
// GetAllNodes selects it, decodes it, and hands it to a caller that mostly
// wants to know what things are called. Enumerating the types in a
// four-hundred-thousand-node import through GetAllNodes moves a gigabyte of
// embeddings across the driver and decodes every one of them to count names.
//
// It shares nodeWhere with GetAllNodes and CountNodes, so all three are always
// about the same rows: a list, a count and a filtered read that disagreed
// about what matched would be worse than any one of them alone.
//
// Nodes come back with a nil Vector. That is the honest shape — a zero-length
// vector read from a store that holds one would be a lie about the record, and
// a caller who needs it has GetAllNodes or GetNode.
func (g *GraphStore) ListNodes(ctx context.Context, filter *GraphFilter) ([]*GraphNode, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	where, args := g.nodeWhere(filter)
	query := `SELECT id, content, node_type, properties, created_at, updated_at FROM graph_nodes` + where
	if filter != nil && filter.Limit > 0 {
		query += ` ORDER BY id LIMIT ?`
		args = append(args, filter.Limit)
	}
	rows, err := g.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: list nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var nodes []*GraphNode
	for rows.Next() {
		var node GraphNode
		var propertiesJSON sql.NullString
		if err := rows.Scan(&node.ID, &node.Content, &node.NodeType, &propertiesJSON,
			&node.CreatedAt, &node.UpdatedAt); err != nil {
			return nil, err
		}
		if propertiesJSON.Valid && propertiesJSON.String != "" {
			if err := json.Unmarshal([]byte(propertiesJSON.String), &node.Properties); err != nil {
				return nil, fmt.Errorf("cortexdb: list nodes: node %s: decode properties: %w", node.ID, err)
			}
		}
		nodes = append(nodes, &node)
	}
	return nodes, rows.Err()
}
