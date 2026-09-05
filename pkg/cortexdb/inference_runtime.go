package cortexdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// apply_inference is now one shape of rule, not a separate engine.
//
// It used to own a join: load the explicit edges of two relation types, match
// the target of one against the source of the other, write the composition.
// That is exactly what
//
//	IF left(?x, ?y) AND right(?y, ?z) THEN result(?x, ?z)
//
// says, so the two-hop request is translated into that rule and handed to
// ApplyRules. The tool keeps its name, its arguments and the provenance it
// wrote; what it no longer keeps is a second derivation path that could drift
// away from the one inference_explain knows how to explain.
func (db *DB) applyInferenceRules(ctx context.Context, req ApplyInferenceRequest) (*ApplyInferenceResponse, error) {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	ruleIDs := make([]string, 0, len(req.Rules))
	rules := make([]graph.Rule, 0, len(req.Rules))
	for _, rule := range req.Rules {
		if strings.TrimSpace(rule.RuleID) == "" {
			return nil, fmt.Errorf("rule_id is required")
		}
		if strings.TrimSpace(rule.LeftRelationType) == "" || strings.TrimSpace(rule.RightRelationType) == "" || strings.TrimSpace(rule.ResultRelationType) == "" {
			return nil, fmt.Errorf("left_relation_type, right_relation_type, and result_relation_type are required")
		}
		ruleIDs = append(ruleIDs, rule.RuleID)
		rules = append(rules, graph.Rule{
			ID:   rule.RuleID,
			Name: rule.Description,
			When: []graph.Atom{
				{Predicate: rule.LeftRelationType, Subject: "?x", Object: "?y"},
				{Predicate: rule.RightRelationType, Subject: "?y", Object: "?z"},
			},
			Then:     graph.Atom{Predicate: rule.ResultRelationType, Subject: "?x", Object: "?z"},
			Weight:   rule.Weight,
			Metadata: cloneStringMap(rule.Metadata),
		})
	}

	resp := &ApplyInferenceResponse{}
	if req.DeleteExisting {
		deletedIDs, err := db.deleteInferredEdges(ctx, req.DocumentID, ruleIDs)
		if err != nil {
			return nil, err
		}
		resp.DeletedEdgeIDs = deletedIDs
	}

	result, err := db.graph.ApplyRules(ctx, rules, graph.RuleOptions{
		DocumentID: req.DocumentID,
		Validate:   db.validateRuleDerivedEdges,
	})
	if err != nil {
		return nil, err
	}
	resp.CreatedEdgeIDs = result.CreatedEdgeIDs
	resp.UnchangedEdgeIDs = result.UnchangedEdgeIDs
	return resp, nil
}

// validateRuleDerivedEdges puts derived edges through the same ontology gate a
// hand-written relation goes through. A rule is not a licence to write a
// relation the schema forbids.
func (db *DB) validateRuleDerivedEdges(ctx context.Context, edges []*graph.GraphEdge) error {
	relations := make([]ToolRelationInput, 0, len(edges))
	for _, edge := range edges {
		ruleID, _ := edge.Properties["rule_id"].(string)
		supports, _ := edge.Properties["support_edge_ids"].([]string)
		relations = append(relations, ToolRelationInput{
			From:           edge.FromNodeID,
			To:             edge.ToNodeID,
			Type:           edge.EdgeType,
			Weight:         edge.Weight,
			Inferred:       true,
			Provenance:     graph.RuleProvenance,
			RuleID:         ruleID,
			SupportEdgeIDs: supports,
		})
	}
	return db.validateRelationInputs(ctx, relations)
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
