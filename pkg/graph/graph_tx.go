package graph

import (
	"context"
	"database/sql"
	"fmt"
)

// ExecuteBatchTx applies a batch of graph operations inside an existing transaction.
func (g *GraphStore) ExecuteBatchTx(ctx context.Context, tx *sql.Tx, ops *BatchGraphOperation) (*BatchResult, error) {
	if ops == nil {
		return &BatchResult{}, nil
	}

	result := &BatchResult{Errors: make([]error, 0)}

	if len(ops.NodeUpserts) > 0 {
		nodeResult, err := g.upsertNodesBatchTx(ctx, tx, ops.NodeUpserts)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert nodes: %w", err)
		}
		result.SuccessCount += nodeResult.SuccessCount
		result.FailedCount += nodeResult.FailedCount
		result.Errors = append(result.Errors, nodeResult.Errors...)
	}

	if len(ops.NodeDeletes) > 0 {
		deleteResult, err := g.deleteNodesBatchTx(ctx, tx, ops.NodeDeletes)
		if err != nil {
			return nil, fmt.Errorf("failed to delete nodes: %w", err)
		}
		result.SuccessCount += deleteResult.SuccessCount
		result.FailedCount += deleteResult.FailedCount
	}

	if len(ops.EdgeUpserts) > 0 {
		edgeResult, err := g.upsertEdgesBatchTx(ctx, tx, ops.EdgeUpserts)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert edges: %w", err)
		}
		result.SuccessCount += edgeResult.SuccessCount
		result.FailedCount += edgeResult.FailedCount
		result.Errors = append(result.Errors, edgeResult.Errors...)
	}

	if len(ops.EdgeDeletes) > 0 {
		deleteResult, err := g.deleteEdgesBatchTx(ctx, tx, ops.EdgeDeletes)
		if err != nil {
			return nil, fmt.Errorf("failed to delete edges: %w", err)
		}
		result.SuccessCount += deleteResult.SuccessCount
		result.FailedCount += deleteResult.FailedCount
	}

	return result, nil
}

// SyncUpsertedNodes updates the optional graph HNSW index after nodes were committed through ExecuteBatchTx.
func (g *GraphStore) SyncUpsertedNodes(_ context.Context, nodes []*GraphNode) {
	if g.hnswIndex == nil || len(nodes) == 0 {
		return
	}

	for _, node := range nodes {
		if node == nil || node.ID == "" || len(node.Vector) == 0 {
			continue
		}
		g.hnswIndex.index.Remove(node.ID)
		_ = g.hnswIndex.index.Add(node.ID, node.Vector)
	}
}
