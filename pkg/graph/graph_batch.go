package graph

import (
	"github.com/liliang-cn/sqvect/internal/encoding"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// BatchNodeOperation represents a batch operation for nodes
type BatchNodeOperation struct {
	Nodes []*GraphNode
}

// BatchEdgeOperation represents a batch operation for edges
type BatchEdgeOperation struct {
	Edges []*GraphEdge
}

// BatchResult contains the results of a batch operation
type BatchResult struct {
	SuccessCount int
	FailedCount  int
	Errors       []error
}

// UpsertNodesBatch inserts or updates multiple nodes in a single transaction
func (g *GraphStore) UpsertNodesBatch(ctx context.Context, nodes []*GraphNode) (*BatchResult, error) {
	if len(nodes) == 0 {
		return &BatchResult{}, nil
	}
	
	result := &BatchResult{
		Errors: make([]error, 0),
	}
	
	// Start transaction
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	
	// Prepare statement for batch insert
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO graph_nodes (id, vector, content, node_type, properties, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			vector = excluded.vector,
			content = excluded.content,
			node_type = excluded.node_type,
			properties = excluded.properties,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()
	
	// Process each node
	for _, node := range nodes {
		if node == nil || node.ID == "" {
			result.Errors = append(result.Errors, fmt.Errorf("invalid node: missing ID"))
			result.FailedCount++
			continue
		}
		
		if len(node.Vector) == 0 {
			result.Errors = append(result.Errors, fmt.Errorf("invalid node %s: missing vector", node.ID))
			result.FailedCount++
			continue
		}
		
		// Encode vector
		vectorBytes, err := encoding.EncodeVector(node.Vector)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to encode vector for %s: %w", node.ID, err))
			result.FailedCount++
			continue
		}
		
		// Encode properties
		var propertiesJSON []byte
		if node.Properties != nil {
			propertiesJSON, err = json.Marshal(node.Properties)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("failed to encode properties for %s: %w", node.ID, err))
				result.FailedCount++
				continue
			}
		}
		
		// Execute insert
		_, err = stmt.ExecContext(ctx,
			node.ID,
			vectorBytes,
			node.Content,
			node.NodeType,
			string(propertiesJSON),
		)
		
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to insert node %s: %w", node.ID, err))
			result.FailedCount++
		} else {
			result.SuccessCount++
		}
	}
	
	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	
	return result, nil
}

// DeleteNodesBatch deletes multiple nodes in a single transaction
func (g *GraphStore) DeleteNodesBatch(ctx context.Context, nodeIDs []string) (*BatchResult, error) {
	if len(nodeIDs) == 0 {
		return &BatchResult{}, nil
	}
	
	result := &BatchResult{
		Errors: make([]error, 0),
	}
	
	// Start transaction
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	
	// Build batch delete query with placeholders
	placeholders := make([]string, len(nodeIDs))
	args := make([]interface{}, len(nodeIDs))
	for i, id := range nodeIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	
	query := fmt.Sprintf("DELETE FROM graph_nodes WHERE id IN (%s)", strings.Join(placeholders, ","))
	
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to delete nodes: %w", err)
	}
	
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	
	result.SuccessCount = int(rowsAffected)
	result.FailedCount = len(nodeIDs) - result.SuccessCount
	
	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	
	return result, nil
}

// UpsertEdgesBatch inserts or updates multiple edges in a single transaction
func (g *GraphStore) UpsertEdgesBatch(ctx context.Context, edges []*GraphEdge) (*BatchResult, error) {
	if len(edges) == 0 {
		return &BatchResult{}, nil
	}
	
	result := &BatchResult{
		Errors: make([]error, 0),
	}
	
	// Start transaction
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	
	// Prepare statement for batch insert
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, weight, properties, vector)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			from_node_id = excluded.from_node_id,
			to_node_id = excluded.to_node_id,
			edge_type = excluded.edge_type,
			weight = excluded.weight,
			properties = excluded.properties,
			vector = excluded.vector
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()
	
	// Process each edge
	for _, edge := range edges {
		if edge == nil || edge.ID == "" {
			result.Errors = append(result.Errors, fmt.Errorf("invalid edge: missing ID"))
			result.FailedCount++
			continue
		}
		
		if edge.FromNodeID == "" || edge.ToNodeID == "" {
			result.Errors = append(result.Errors, fmt.Errorf("invalid edge %s: missing node IDs", edge.ID))
			result.FailedCount++
			continue
		}
		
		// Set default weight
		if edge.Weight == 0 {
			edge.Weight = 1.0
		}
		
		// Encode properties
		var propertiesJSON []byte
		if edge.Properties != nil {
			propertiesJSON, err = json.Marshal(edge.Properties)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("failed to encode properties for edge %s: %w", edge.ID, err))
				result.FailedCount++
				continue
			}
		}
		
		// Encode vector if present
		var vectorBytes []byte
		if len(edge.Vector) > 0 {
			vectorBytes, err = encoding.EncodeVector(edge.Vector)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("failed to encode vector for edge %s: %w", edge.ID, err))
				result.FailedCount++
				continue
			}
		}
		
		// Execute insert
		_, err = stmt.ExecContext(ctx,
			edge.ID,
			edge.FromNodeID,
			edge.ToNodeID,
			edge.EdgeType,
			edge.Weight,
			string(propertiesJSON),
			vectorBytes,
		)
		
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to insert edge %s: %w", edge.ID, err))
			result.FailedCount++
		} else {
			result.SuccessCount++
		}
	}
	
	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	
	return result, nil
}

// DeleteEdgesBatch deletes multiple edges in a single transaction
func (g *GraphStore) DeleteEdgesBatch(ctx context.Context, edgeIDs []string) (*BatchResult, error) {
	if len(edgeIDs) == 0 {
		return &BatchResult{}, nil
	}
	
	result := &BatchResult{
		Errors: make([]error, 0),
	}
	
	// Start transaction
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	
	// Build batch delete query
	placeholders := make([]string, len(edgeIDs))
	args := make([]interface{}, len(edgeIDs))
	for i, id := range edgeIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	
	query := fmt.Sprintf("DELETE FROM graph_edges WHERE id IN (%s)", strings.Join(placeholders, ","))
	
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to delete edges: %w", err)
	}
	
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	
	result.SuccessCount = int(rowsAffected)
	result.FailedCount = len(edgeIDs) - result.SuccessCount
	
	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	
	return result, nil
}

// GetNodesBatch retrieves multiple nodes by their IDs
func (g *GraphStore) GetNodesBatch(ctx context.Context, nodeIDs []string) ([]*GraphNode, error) {
	if len(nodeIDs) == 0 {
		return []*GraphNode{}, nil
	}
	
	// Build query with placeholders
	placeholders := make([]string, len(nodeIDs))
	args := make([]interface{}, len(nodeIDs))
	for i, id := range nodeIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	
	query := fmt.Sprintf(`
		SELECT id, vector, content, node_type, properties, created_at, updated_at
		FROM graph_nodes
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))
	
	rows, err := g.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}
	defer rows.Close()
	
	nodes := make([]*GraphNode, 0, len(nodeIDs))
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
			return nil, fmt.Errorf("failed to scan node: %w", err)
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

// GetEdgesBatch retrieves multiple edges by their IDs
func (g *GraphStore) GetEdgesBatch(ctx context.Context, edgeIDs []string) ([]*GraphEdge, error) {
	if len(edgeIDs) == 0 {
		return []*GraphEdge{}, nil
	}
	
	// Build query with placeholders
	placeholders := make([]string, len(edgeIDs))
	args := make([]interface{}, len(edgeIDs))
	for i, id := range edgeIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	
	query := fmt.Sprintf(`
		SELECT id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at
		FROM graph_edges
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))
	
	rows, err := g.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query edges: %w", err)
	}
	defer rows.Close()
	
	edges := make([]*GraphEdge, 0, len(edgeIDs))
	for rows.Next() {
		var edge GraphEdge
		var propertiesJSON sql.NullString
		var vectorBytes []byte
		
		err := rows.Scan(
			&edge.ID,
			&edge.FromNodeID,
			&edge.ToNodeID,
			&edge.EdgeType,
			&edge.Weight,
			&propertiesJSON,
			&vectorBytes,
			&edge.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan edge: %w", err)
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
		
		edges = append(edges, &edge)
	}
	
	return edges, rows.Err()
}

// BatchGraphOperation allows multiple graph operations in a single transaction
type BatchGraphOperation struct {
	NodeUpserts   []*GraphNode
	NodeDeletes   []string
	EdgeUpserts   []*GraphEdge
	EdgeDeletes   []string
}

// ExecuteBatch executes multiple graph operations in a single transaction
func (g *GraphStore) ExecuteBatch(ctx context.Context, ops *BatchGraphOperation) (*BatchResult, error) {
	if ops == nil {
		return &BatchResult{}, nil
	}
	
	result := &BatchResult{
		Errors: make([]error, 0),
	}
	
	// Start transaction
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	
	// Process node upserts
	if len(ops.NodeUpserts) > 0 {
		nodeResult, err := g.upsertNodesBatchTx(ctx, tx, ops.NodeUpserts)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert nodes: %w", err)
		}
		result.SuccessCount += nodeResult.SuccessCount
		result.FailedCount += nodeResult.FailedCount
		result.Errors = append(result.Errors, nodeResult.Errors...)
	}
	
	// Process node deletes
	if len(ops.NodeDeletes) > 0 {
		deleteResult, err := g.deleteNodesBatchTx(ctx, tx, ops.NodeDeletes)
		if err != nil {
			return nil, fmt.Errorf("failed to delete nodes: %w", err)
		}
		result.SuccessCount += deleteResult.SuccessCount
		result.FailedCount += deleteResult.FailedCount
	}
	
	// Process edge upserts
	if len(ops.EdgeUpserts) > 0 {
		edgeResult, err := g.upsertEdgesBatchTx(ctx, tx, ops.EdgeUpserts)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert edges: %w", err)
		}
		result.SuccessCount += edgeResult.SuccessCount
		result.FailedCount += edgeResult.FailedCount
		result.Errors = append(result.Errors, edgeResult.Errors...)
	}
	
	// Process edge deletes
	if len(ops.EdgeDeletes) > 0 {
		deleteResult, err := g.deleteEdgesBatchTx(ctx, tx, ops.EdgeDeletes)
		if err != nil {
			return nil, fmt.Errorf("failed to delete edges: %w", err)
		}
		result.SuccessCount += deleteResult.SuccessCount
		result.FailedCount += deleteResult.FailedCount
	}
	
	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	
	return result, nil
}

// Helper functions for transaction-based operations

func (g *GraphStore) upsertNodesBatchTx(ctx context.Context, tx *sql.Tx, nodes []*GraphNode) (*BatchResult, error) {
	result := &BatchResult{Errors: make([]error, 0)}
	
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO graph_nodes (id, vector, content, node_type, properties, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			vector = excluded.vector,
			content = excluded.content,
			node_type = excluded.node_type,
			properties = excluded.properties,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	
	for _, node := range nodes {
		if node == nil || node.ID == "" || len(node.Vector) == 0 {
			result.FailedCount++
			continue
		}
		
		vectorBytes, _ := encoding.EncodeVector(node.Vector)
		var propertiesJSON []byte
		if node.Properties != nil {
			propertiesJSON, _ = json.Marshal(node.Properties)
		}
		
		_, err = stmt.ExecContext(ctx, node.ID, vectorBytes, node.Content, node.NodeType, string(propertiesJSON))
		if err != nil {
			result.FailedCount++
			result.Errors = append(result.Errors, err)
		} else {
			result.SuccessCount++
		}
	}
	
	return result, nil
}

func (g *GraphStore) deleteNodesBatchTx(ctx context.Context, tx *sql.Tx, nodeIDs []string) (*BatchResult, error) {
	result := &BatchResult{}
	
	if len(nodeIDs) == 0 {
		return result, nil
	}
	
	placeholders := make([]string, len(nodeIDs))
	args := make([]interface{}, len(nodeIDs))
	for i, id := range nodeIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	
	query := fmt.Sprintf("DELETE FROM graph_nodes WHERE id IN (%s)", strings.Join(placeholders, ","))
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	
	rowsAffected, _ := res.RowsAffected()
	result.SuccessCount = int(rowsAffected)
	result.FailedCount = len(nodeIDs) - result.SuccessCount
	
	return result, nil
}

func (g *GraphStore) upsertEdgesBatchTx(ctx context.Context, tx *sql.Tx, edges []*GraphEdge) (*BatchResult, error) {
	result := &BatchResult{Errors: make([]error, 0)}
	
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, weight, properties, vector)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			from_node_id = excluded.from_node_id,
			to_node_id = excluded.to_node_id,
			edge_type = excluded.edge_type,
			weight = excluded.weight,
			properties = excluded.properties,
			vector = excluded.vector
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	
	for _, edge := range edges {
		if edge == nil || edge.ID == "" || edge.FromNodeID == "" || edge.ToNodeID == "" {
			result.FailedCount++
			continue
		}
		
		if edge.Weight == 0 {
			edge.Weight = 1.0
		}
		
		var propertiesJSON []byte
		if edge.Properties != nil {
			propertiesJSON, _ = json.Marshal(edge.Properties)
		}
		
		var vectorBytes []byte
		if len(edge.Vector) > 0 {
			vectorBytes, _ = encoding.EncodeVector(edge.Vector)
		}
		
		_, err = stmt.ExecContext(ctx, edge.ID, edge.FromNodeID, edge.ToNodeID, edge.EdgeType, edge.Weight, string(propertiesJSON), vectorBytes)
		if err != nil {
			result.FailedCount++
			result.Errors = append(result.Errors, err)
		} else {
			result.SuccessCount++
		}
	}
	
	return result, nil
}

func (g *GraphStore) deleteEdgesBatchTx(ctx context.Context, tx *sql.Tx, edgeIDs []string) (*BatchResult, error) {
	result := &BatchResult{}
	
	if len(edgeIDs) == 0 {
		return result, nil
	}
	
	placeholders := make([]string, len(edgeIDs))
	args := make([]interface{}, len(edgeIDs))
	for i, id := range edgeIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	
	query := fmt.Sprintf("DELETE FROM graph_edges WHERE id IN (%s)", strings.Join(placeholders, ","))
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	
	rowsAffected, _ := res.RowsAffected()
	result.SuccessCount = int(rowsAffected)
	result.FailedCount = len(edgeIDs) - result.SuccessCount
	
	return result, nil
}