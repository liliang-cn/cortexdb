package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func (db *DB) applyInferenceRules(ctx context.Context, req ApplyInferenceRequest) (*ApplyInferenceResponse, error) {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	ruleIDs := make([]string, 0, len(req.Rules))
	relationTypeSet := make(map[string]struct{}, len(req.Rules)*2)
	for _, rule := range req.Rules {
		if strings.TrimSpace(rule.RuleID) == "" {
			return nil, fmt.Errorf("rule_id is required")
		}
		if strings.TrimSpace(rule.LeftRelationType) == "" || strings.TrimSpace(rule.RightRelationType) == "" || strings.TrimSpace(rule.ResultRelationType) == "" {
			return nil, fmt.Errorf("left_relation_type, right_relation_type, and result_relation_type are required")
		}
		ruleIDs = append(ruleIDs, rule.RuleID)
		relationTypeSet[normalizeOntologyLabel(rule.LeftRelationType)] = struct{}{}
		relationTypeSet[normalizeOntologyLabel(rule.RightRelationType)] = struct{}{}
	}

	resp := &ApplyInferenceResponse{}
	if req.DeleteExisting {
		deletedIDs, err := db.deleteInferredEdges(ctx, req.DocumentID, ruleIDs)
		if err != nil {
			return nil, err
		}
		resp.DeletedEdgeIDs = deletedIDs
	}

	explicitEdges, err := db.loadExplicitRelationEdges(ctx, sortedKeysFromSet(relationTypeSet), req.DocumentID)
	if err != nil {
		return nil, err
	}
	if len(explicitEdges) == 0 {
		return resp, nil
	}

	leftByType := make(map[string][]*graph.GraphEdge, len(relationTypeSet))
	rightByTypeAndFrom := make(map[string]map[string][]*graph.GraphEdge, len(relationTypeSet))
	for _, edge := range explicitEdges {
		relationType := normalizeOntologyLabel(edge.EdgeType)
		leftByType[relationType] = append(leftByType[relationType], edge)
		if rightByTypeAndFrom[relationType] == nil {
			rightByTypeAndFrom[relationType] = make(map[string][]*graph.GraphEdge)
		}
		rightByTypeAndFrom[relationType][edge.FromNodeID] = append(rightByTypeAndFrom[relationType][edge.FromNodeID], edge)
	}

	inferredEdges := make(map[string]*graph.GraphEdge)
	inferredRelations := make([]ToolRelationInput, 0)
	for _, rule := range req.Rules {
		leftType := normalizeOntologyLabel(rule.LeftRelationType)
		rightType := normalizeOntologyLabel(rule.RightRelationType)
		resultType := normalizeOntologyLabel(rule.ResultRelationType)

		for _, left := range leftByType[leftType] {
			matches := rightByTypeAndFrom[rightType][left.ToNodeID]
			for _, right := range matches {
				edgeID := inferredEdgeID(rule.RuleID, left.FromNodeID, right.ToNodeID, resultType)
				if _, exists := inferredEdges[edgeID]; exists {
					continue
				}

				weight := rule.Weight
				if weight == 0 {
					weight = (left.Weight + right.Weight) / 2
				}

				properties := map[string]any{
					"inferred":               true,
					"provenance":             "rule",
					"rule_id":                rule.RuleID,
					"support_edge_ids":       []string{left.ID, right.ID},
					"support_relation_types": []string{left.EdgeType, right.EdgeType},
				}
				documentID := strings.TrimSpace(req.DocumentID)
				if documentID == "" {
					if sharedDocumentID, ok := sharedInferenceDocumentID(left, right); ok {
						documentID = sharedDocumentID
					}
				}
				if documentID != "" {
					properties["document_id"] = documentID
				}
				for key, value := range rule.Metadata {
					switch key {
					case "inferred", "provenance", "rule_id", "support_edge_ids", "support_relation_types", "document_id":
						continue
					}
					properties[key] = value
				}

				inferredEdges[edgeID] = &graph.GraphEdge{
					ID:         edgeID,
					FromNodeID: left.FromNodeID,
					ToNodeID:   right.ToNodeID,
					EdgeType:   resultType,
					Weight:     weight,
					Properties: properties,
				}
				inferredRelations = append(inferredRelations, ToolRelationInput{
					From:           left.FromNodeID,
					To:             right.ToNodeID,
					Type:           resultType,
					Inferred:       true,
					Provenance:     "rule",
					RuleID:         rule.RuleID,
					SupportEdgeIDs: []string{left.ID, right.ID},
					Metadata:       cloneStringMap(rule.Metadata),
				})
			}
		}
	}

	if len(inferredEdges) == 0 {
		return resp, nil
	}
	if err := db.validateRelationInputs(ctx, inferredRelations); err != nil {
		return nil, err
	}

	orderedIDs := sortedKeysFromSet(edgeIDSet(inferredEdges))
	edges := make([]*graph.GraphEdge, 0, len(orderedIDs))
	for _, edgeID := range orderedIDs {
		edges = append(edges, inferredEdges[edgeID])
		resp.CreatedEdgeIDs = append(resp.CreatedEdgeIDs, edgeID)
	}
	edgeResult, err := db.graph.UpsertEdgesBatch(ctx, edges)
	if err != nil {
		return nil, fmt.Errorf("upsert inferred edges: %w", err)
	}
	// Rejected rows live in the result, not in err. CreatedEdgeIDs would otherwise
	// list edges that were never written.
	if err := edgeResult.Err(); err != nil {
		return nil, fmt.Errorf("upsert inferred edges: %w", err)
	}
	return resp, nil
}

func (db *DB) loadExplicitRelationEdges(ctx context.Context, relationTypes []string, documentID string) ([]*graph.GraphEdge, error) {
	if len(relationTypes) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(relationTypes))
	args := make([]any, 0, len(relationTypes)+1)
	for i, relationType := range relationTypes {
		placeholders[i] = "?"
		args = append(args, relationType)
	}

	query := fmt.Sprintf(`
		SELECT id, from_node_id, to_node_id, edge_type, weight, properties
		FROM graph_edges
		WHERE edge_type IN (%s)
		  AND %s != 1
	`, strings.Join(placeholders, ","), db.dialect.JSONFlag("properties", "inferred"))
	if strings.TrimSpace(documentID) != "" {
		query += ` AND ` + db.dialect.JSONTextGuarded("properties", "document_id") + ` = ?`
		args = append(args, documentID)
	}
	query += ` ORDER BY id ASC`

	rows, err := db.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query explicit relation edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	edges := make([]*graph.GraphEdge, 0)
	for rows.Next() {
		var edge graph.GraphEdge
		var propertiesJSON sql.NullString
		if err := rows.Scan(&edge.ID, &edge.FromNodeID, &edge.ToNodeID, &edge.EdgeType, &edge.Weight, &propertiesJSON); err != nil {
			return nil, fmt.Errorf("scan explicit relation edge: %w", err)
		}
		if propertiesJSON.Valid && propertiesJSON.String != "" {
			if err := json.Unmarshal([]byte(propertiesJSON.String), &edge.Properties); err != nil {
				return nil, fmt.Errorf("decode explicit relation edge properties: %w", err)
			}
		}
		edges = append(edges, &edge)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate explicit relation edges: %w", err)
	}
	return edges, nil
}

func (db *DB) deleteInferredEdges(ctx context.Context, documentID string, ruleIDs []string) ([]string, error) {
	edgeIDs, err := db.inferredEdgeIDs(ctx, documentID, ruleIDs)
	if err != nil {
		return nil, err
	}
	if len(edgeIDs) == 0 {
		return nil, nil
	}
	if _, err := db.graph.DeleteEdgesBatch(ctx, edgeIDs); err != nil {
		return nil, fmt.Errorf("delete inferred edges: %w", err)
	}
	return edgeIDs, nil
}

func (db *DB) inferredEdgeIDs(ctx context.Context, documentID string, ruleIDs []string) ([]string, error) {
	query := `
		SELECT id
		FROM graph_edges
		WHERE ` + db.dialect.JSONFlag("properties", "inferred") + ` = 1
		  AND ` + db.dialect.JSONTextGuarded("properties", "provenance") + ` = 'rule'
	`
	args := make([]any, 0, len(ruleIDs)+1)
	if strings.TrimSpace(documentID) != "" {
		query += ` AND ` + db.dialect.JSONTextGuarded("properties", "document_id") + ` = ?`
		args = append(args, documentID)
	}
	if len(ruleIDs) > 0 {
		placeholders := make([]string, len(ruleIDs))
		for i, ruleID := range ruleIDs {
			placeholders[i] = "?"
			args = append(args, ruleID)
		}
		query += fmt.Sprintf(` AND %s IN (%s)`,
			db.dialect.JSONTextGuarded("properties", "rule_id"), strings.Join(placeholders, ","))
	}
	query += ` ORDER BY id ASC`

	rows, err := db.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query inferred edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	edgeIDs := make([]string, 0)
	for rows.Next() {
		var edgeID string
		if err := rows.Scan(&edgeID); err != nil {
			return nil, fmt.Errorf("scan inferred edge id: %w", err)
		}
		edgeIDs = append(edgeIDs, edgeID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inferred edge ids: %w", err)
	}
	return edgeIDs, nil
}

func inferredEdgeID(ruleID, fromNodeID, toNodeID, edgeType string) string {
	return fmt.Sprintf("edge:inferred:%s:%s:%s:%s", normalizeOntologyLabel(ruleID), fromNodeID, toNodeID, normalizeOntologyLabel(edgeType))
}

func sharedInferenceDocumentID(left, right *graph.GraphEdge) (string, bool) {
	leftDocumentID, ok := stringProperty(left.Properties, "document_id")
	if !ok {
		return "", false
	}
	rightDocumentID, ok := stringProperty(right.Properties, "document_id")
	if !ok || rightDocumentID != leftDocumentID {
		return "", false
	}
	return leftDocumentID, true
}

func edgeIDSet(edges map[string]*graph.GraphEdge) map[string]struct{} {
	set := make(map[string]struct{}, len(edges))
	for edgeID := range edges {
		set[edgeID] = struct{}{}
	}
	return set
}
