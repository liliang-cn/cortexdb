package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func (db *DB) cleanupKnowledgeGraphArtifactsTx(ctx context.Context, tx *sql.Tx, knowledgeID string, chunks []*core.Embedding) ([]string, error) {
	entityNodeSet := make(map[string]struct{})
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
	}
	entityNamesByChunk, err := db.chunkEntityNamesBatchTx(ctx, tx, chunkIDs, 0)
	if err != nil {
		return nil, fmt.Errorf("get chunk entities: %w", err)
	}
	for _, chunk := range chunks {
		for _, entityName := range entityNamesByChunk[chunk.ID] {
			entityNodeSet[graphEntityNodeID(entityName)] = struct{}{}
		}
	}

	edgeIDs, err := db.graphEdgeIDsByDocumentTx(ctx, tx, knowledgeID)
	if err != nil {
		return nil, err
	}
	// A document's graph is retracted, not erased: the rows move to history
	// with retracted_at set before they leave the live tables, in this
	// transaction, so delete_document_graph keeps its name and its current
	// behaviour while "what did this document say before we removed it" gains
	// an answer. ArchiveNodesTx takes the edges of each node with it, because
	// the DELETE below cascades to them whether or not they were listed.
	at := time.Now().UTC()
	if err := db.graph.ArchiveEdgesTx(ctx, tx, edgeIDs, at); err != nil {
		return nil, fmt.Errorf("record document graph edges: %w", err)
	}
	if err := deleteStringIDsTx(ctx, db.Dialect(), tx, "graph_edges", "id", edgeIDs); err != nil {
		return nil, fmt.Errorf("delete document graph edges: %w", err)
	}

	nodeIDs := make([]string, 0, len(chunks)+1)
	nodeIDs = append(nodeIDs, graphDocumentNodeID(knowledgeID))
	for _, chunk := range chunks {
		nodeIDs = append(nodeIDs, chunk.ID)
	}
	if err := db.graph.ArchiveNodesTx(ctx, tx, nodeIDs, at); err != nil {
		return nil, fmt.Errorf("record graph nodes: %w", err)
	}
	if err := deleteStringIDsTx(ctx, db.Dialect(), tx, "graph_nodes", "id", nodeIDs); err != nil {
		return nil, fmt.Errorf("delete graph nodes: %w", err)
	}

	orphanIDs, err := db.orphanNodeIDsTx(ctx, tx, sortedKeysFromSet(entityNodeSet))
	if err != nil {
		return nil, fmt.Errorf("get orphan entity nodes: %w", err)
	}
	if err := db.graph.ArchiveNodesTx(ctx, tx, orphanIDs, at); err != nil {
		return nil, fmt.Errorf("record orphan entity nodes: %w", err)
	}
	if err := deleteStringIDsTx(ctx, db.Dialect(), tx, "graph_nodes", "id", orphanIDs); err != nil {
		return nil, fmt.Errorf("delete orphan entity nodes: %w", err)
	}
	return append(nodeIDs, orphanIDs...), nil
}

func (db *DB) graphEdgeIDsByDocument(ctx context.Context, documentID string) ([]string, error) {
	return db.graphEdgeIDsByDocumentTx(ctx, db.store.GetDB(), documentID)
}

func (db *DB) graphEdgeIDsByDocumentTx(ctx context.Context, querier graphStringQuerier, documentID string) ([]string, error) {
	if strings.TrimSpace(documentID) == "" {
		return nil, nil
	}

	rows, err := db.querierQuery(ctx, querier, `
		SELECT id
		FROM graph_edges
		WHERE `+db.dialect.JSONTextGuarded("properties", "document_id")+` = ?
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

func (db *DB) knowledgeChunkRefsTx(ctx context.Context, tx *sql.Tx, knowledgeID string) ([]*core.Embedding, error) {
	rows, err := db.txQuery(ctx, tx, `
		SELECT id
		FROM embeddings
		WHERE doc_id = ?
		ORDER BY created_at ASC
	`, knowledgeID)
	if err != nil {
		return nil, fmt.Errorf("query knowledge chunks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	chunks := make([]*core.Embedding, 0)
	for rows.Next() {
		var chunkID string
		if err := rows.Scan(&chunkID); err != nil {
			return nil, fmt.Errorf("scan knowledge chunk id: %w", err)
		}
		chunks = append(chunks, &core.Embedding{ID: chunkID})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge chunks: %w", err)
	}
	return chunks, nil
}

func (db *DB) chunkEntityNamesBatchTx(ctx context.Context, querier graphStringQuerier, chunkIDs []string, limitPerChunk int) (map[string][]string, error) {
	result := make(map[string][]string, len(chunkIDs))
	if len(chunkIDs) == 0 {
		return result, nil
	}

	for _, chunk := range stringChunks(chunkIDs, 2) {
		placeholders, args := sqlPlaceholders(chunk)
		unionArgs := append([]any{}, args...)
		unionArgs = append(unionArgs, args...)
		rows, err := db.querierQuery(ctx, querier, fmt.Sprintf(`
			SELECT chunk_id, entity_name
			FROM (
				SELECT e.from_node_id AS chunk_id, n.content AS entity_name
				FROM graph_edges e
				JOIN graph_nodes n ON n.id = e.to_node_id
				WHERE e.from_node_id IN (%s) AND n.node_type = 'entity'
				UNION
				SELECT e.to_node_id AS chunk_id, n.content AS entity_name
				FROM graph_edges e
				JOIN graph_nodes n ON n.id = e.from_node_id
				WHERE e.to_node_id IN (%s) AND n.node_type = 'entity'
			)
			ORDER BY chunk_id ASC, entity_name ASC
		`, placeholders, placeholders), unionArgs...)
		if err != nil {
			return nil, fmt.Errorf("query chunk entity names: %w", err)
		}

		for rows.Next() {
			var chunkID, entityName string
			if err := rows.Scan(&chunkID, &entityName); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan chunk entity name: %w", err)
			}
			if limitPerChunk > 0 && len(result[chunkID]) >= limitPerChunk {
				continue
			}
			result[chunkID] = append(result[chunkID], entityName)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate chunk entity names: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close chunk entity rows: %w", err)
		}
	}
	return result, nil
}

func (db *DB) orphanNodeIDsTx(ctx context.Context, querier graphStringQuerier, nodeIDs []string) ([]string, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}

	connected := make(map[string]struct{}, len(nodeIDs))
	for _, chunk := range stringChunks(nodeIDs, 2) {
		placeholders, args := sqlPlaceholders(chunk)
		unionArgs := append([]any{}, args...)
		unionArgs = append(unionArgs, args...)
		rows, err := db.querierQuery(ctx, querier, fmt.Sprintf(`
			SELECT DISTINCT node_id
			FROM (
				SELECT from_node_id AS node_id FROM graph_edges WHERE from_node_id IN (%s)
				UNION
				SELECT to_node_id AS node_id FROM graph_edges WHERE to_node_id IN (%s)
			)
		`, placeholders, placeholders), unionArgs...)
		if err != nil {
			return nil, fmt.Errorf("query connected node ids: %w", err)
		}

		for rows.Next() {
			var nodeID string
			if err := rows.Scan(&nodeID); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan connected node id: %w", err)
			}
			connected[nodeID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate connected node ids: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close connected node rows: %w", err)
		}
	}

	orphanIDs := make([]string, 0)
	for _, nodeID := range nodeIDs {
		if _, ok := connected[nodeID]; ok {
			continue
		}
		orphanIDs = append(orphanIDs, nodeID)
	}
	return orphanIDs, nil
}

// deleteStringIDsTx takes the dialect explicitly because it has no receiver to
// ask. The placeholders it generates are `?`; rebinding turns them into
// whatever this database expects.
func deleteStringIDsTx(ctx context.Context, d sqldialect.Dialect, tx *sql.Tx, table string, column string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for _, chunk := range stringChunks(ids, 1) {
		placeholders, args := sqlPlaceholders(chunk)
		stmt := d.Rebind(fmt.Sprintf("DELETE FROM %s WHERE %s IN (%s)", table, column, placeholders))
		if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
			return err
		}
	}
	return nil
}

type graphStringQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
