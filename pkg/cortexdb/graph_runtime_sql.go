package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type graphNodeSummary struct {
	ID         string
	Content    string
	NodeType   string
	DocumentID string
}

type graphEdgeSummary struct {
	FromNodeID string
	ToNodeID   string
}

type embeddingSummary struct {
	ID         string
	DocumentID string
	Content    string
	Metadata   map[string]string
}

func (db *DB) graphNodeSummariesByIDs(ctx context.Context, nodeIDs []string) (map[string]graphNodeSummary, error) {
	if len(nodeIDs) == 0 {
		return map[string]graphNodeSummary{}, nil
	}

	summaries := make(map[string]graphNodeSummary, len(nodeIDs))
	for _, chunk := range stringChunks(nodeIDs, 1) {
		placeholders, args := sqlPlaceholders(chunk)
		// The document_id lives in the properties JSON, and how a JSON field is
		// read is the one thing the two databases will not agree on — this
		// query was the reason graph-mode retrieval returned an error on
		// PostgreSQL while every unit test passed on both.
		rows, err := db.query(ctx, fmt.Sprintf(`
			SELECT id,
			       content,
			       node_type,
			       COALESCE(%s, '') AS document_id
			FROM graph_nodes
			WHERE id IN (%s)
		`, db.dialect.JSONTextGuarded("properties", "document_id"), placeholders), args...)
		if err != nil {
			return nil, fmt.Errorf("query graph node summaries: %w", err)
		}

		for rows.Next() {
			var summary graphNodeSummary
			if err := rows.Scan(&summary.ID, &summary.Content, &summary.NodeType, &summary.DocumentID); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan graph node summary: %w", err)
			}
			summaries[summary.ID] = summary
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate graph node summaries: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close graph node summary rows: %w", err)
		}
	}
	return summaries, nil
}

func (db *DB) graphEdgesByNodeIDs(ctx context.Context, nodeIDs []string, direction string) (map[string][]graphEdgeSummary, error) {
	result := make(map[string][]graphEdgeSummary, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return result, nil
	}

	nodeSet := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		nodeSet[nodeID] = struct{}{}
	}

	for _, chunk := range stringChunks(nodeIDs, max(1, map[string]int{"out": 1, "in": 1, "both": 2, "": 2}[direction])) {
		placeholders, args := sqlPlaceholders(chunk)
		query := ""
		switch direction {
		case "out":
			query = fmt.Sprintf(`SELECT from_node_id, to_node_id FROM graph_edges WHERE from_node_id IN (%s) ORDER BY id`, placeholders)
		case "in":
			query = fmt.Sprintf(`SELECT from_node_id, to_node_id FROM graph_edges WHERE to_node_id IN (%s) ORDER BY id`, placeholders)
		case "both", "":
			query = fmt.Sprintf(`SELECT from_node_id, to_node_id FROM graph_edges WHERE from_node_id IN (%s) OR to_node_id IN (%s) ORDER BY id`, placeholders, placeholders)
			args = append(args, args[:len(chunk)]...)
		default:
			return nil, fmt.Errorf("invalid direction: %s", direction)
		}

		rows, err := db.query(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("query graph edges by nodes: %w", err)
		}

		for rows.Next() {
			var edge graphEdgeSummary
			if err := rows.Scan(&edge.FromNodeID, &edge.ToNodeID); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan graph edge summary: %w", err)
			}
			switch direction {
			case "out":
				result[edge.FromNodeID] = append(result[edge.FromNodeID], edge)
			case "in":
				result[edge.ToNodeID] = append(result[edge.ToNodeID], edge)
			default:
				if _, ok := nodeSet[edge.FromNodeID]; ok {
					result[edge.FromNodeID] = append(result[edge.FromNodeID], edge)
				}
				if _, ok := nodeSet[edge.ToNodeID]; ok && edge.ToNodeID != edge.FromNodeID {
					result[edge.ToNodeID] = append(result[edge.ToNodeID], edge)
				}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate graph edge summaries: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close graph edge summary rows: %w", err)
		}
	}
	return result, nil
}

func (db *DB) chunkEntityNamesBatch(ctx context.Context, chunkIDs []string, limitPerChunk int) (map[string][]string, error) {
	result := make(map[string][]string, len(chunkIDs))
	if len(chunkIDs) == 0 {
		return result, nil
	}

	for _, chunk := range stringChunks(chunkIDs, 2) {
		placeholders, args := sqlPlaceholders(chunk)
		unionArgs := append([]any{}, args...)
		unionArgs = append(unionArgs, args...)
		rows, err := db.query(ctx, fmt.Sprintf(`
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

func (db *DB) embeddingsByIDs(ctx context.Context, embeddingIDs []string) (map[string]embeddingSummary, error) {
	if len(embeddingIDs) == 0 {
		return map[string]embeddingSummary{}, nil
	}

	result := make(map[string]embeddingSummary, len(embeddingIDs))
	for _, chunk := range stringChunks(embeddingIDs, 1) {
		placeholders, args := sqlPlaceholders(chunk)
		rows, err := db.query(ctx, fmt.Sprintf(`
			SELECT e.id, COALESCE(e.doc_id, ''), e.content, e.metadata
			FROM embeddings e
			WHERE e.id IN (%s)
		`, placeholders), args...)
		if err != nil {
			return nil, fmt.Errorf("query embeddings by ids: %w", err)
		}

		for rows.Next() {
			var summary embeddingSummary
			var metadataJSON sql.NullString
			if err := rows.Scan(&summary.ID, &summary.DocumentID, &summary.Content, &metadataJSON); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan embedding summary: %w", err)
			}
			if metadataJSON.Valid && strings.TrimSpace(metadataJSON.String) != "" {
				if err := json.Unmarshal([]byte(metadataJSON.String), &summary.Metadata); err != nil {
					_ = rows.Close()
					return nil, fmt.Errorf("decode embedding metadata: %w", err)
				}
			}
			if summary.Metadata == nil {
				summary.Metadata = make(map[string]string)
			}
			result[summary.ID] = summary
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate embedding summaries: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close embedding rows: %w", err)
		}
	}
	return result, nil
}

func (db *DB) orphanNodeIDs(ctx context.Context, nodeIDs []string) ([]string, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}

	connected := make(map[string]struct{}, len(nodeIDs))
	for _, chunk := range stringChunks(nodeIDs, 2) {
		placeholders, args := sqlPlaceholders(chunk)
		unionArgs := append([]any{}, args...)
		unionArgs = append(unionArgs, args...)
		rows, err := db.query(ctx, fmt.Sprintf(`
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

func sqlPlaceholders(values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, value := range values {
		placeholders[i] = "?"
		args[i] = value
	}
	return strings.Join(placeholders, ","), args
}
