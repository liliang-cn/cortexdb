package cortexdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func (db *DB) cleanupKnowledgeGraphArtifacts(ctx context.Context, knowledgeID string, chunks []*core.Embedding) error {
	entityNodeSet := make(map[string]struct{})
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
	}
	entityNamesByChunk, err := db.chunkEntityNamesBatch(ctx, chunkIDs, 64)
	if err != nil {
		return fmt.Errorf("get chunk entities: %w", err)
	}
	for _, chunk := range chunks {
		for _, entityName := range entityNamesByChunk[chunk.ID] {
			entityNodeSet[graphEntityNodeID(entityName)] = struct{}{}
		}
	}

	edgeIDs, err := db.graphEdgeIDsByDocument(ctx, knowledgeID)
	if err != nil {
		return err
	}
	if len(edgeIDs) > 0 {
		if _, err := db.graph.DeleteEdgesBatch(ctx, edgeIDs); err != nil {
			return fmt.Errorf("delete document graph edges: %w", err)
		}
	}

	nodeIDs := make([]string, 0, len(chunks)+1)
	nodeIDs = append(nodeIDs, graphDocumentNodeID(knowledgeID))
	for _, chunk := range chunks {
		nodeIDs = append(nodeIDs, chunk.ID)
	}
	if _, err := db.graph.DeleteNodesBatch(ctx, nodeIDs); err != nil {
		return fmt.Errorf("delete graph nodes: %w", err)
	}

	if err := db.deleteOrphanEntityNodes(ctx, sortedKeysFromSet(entityNodeSet)); err != nil {
		return err
	}
	return nil
}

func (db *DB) deleteOrphanEntityNodes(ctx context.Context, nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return nil
	}

	orphanIDs, err := db.orphanNodeIDs(ctx, nodeIDs)
	if err != nil {
		return fmt.Errorf("get orphan entity nodes: %w", err)
	}
	if len(orphanIDs) == 0 {
		return nil
	}
	if _, err := db.graph.DeleteNodesBatch(ctx, orphanIDs); err != nil {
		return fmt.Errorf("delete orphan entity nodes: %w", err)
	}
	return nil
}

func (db *DB) graphEdgeIDsByDocument(ctx context.Context, documentID string) ([]string, error) {
	if strings.TrimSpace(documentID) == "" {
		return nil, nil
	}

	rows, err := db.store.GetDB().QueryContext(ctx, `
		SELECT id
		FROM graph_edges
		WHERE CASE
		        WHEN json_valid(properties) = 1 THEN json_extract(properties, '$.document_id')
		        ELSE NULL
		      END = ?
		ORDER BY id ASC
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query document graph edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	edgeIDs := make([]string, 0)
	for rows.Next() {
		var edgeID string
		if err := rows.Scan(&edgeID); err != nil {
			return nil, fmt.Errorf("scan document graph edge id: %w", err)
		}
		edgeIDs = append(edgeIDs, edgeID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate document graph edges: %w", err)
	}
	return edgeIDs, nil
}
