package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// DefaultRuleExplainDepth is how far ExplainEdge follows the support chain when
// the caller does not say. Derived edges can support other derived edges, so an
// explanation is a tree, and an unbounded one over a transitive closure is a
// wall of text nobody reads.
const DefaultRuleExplainDepth = 4

// RuleEdgeExplanation says why one edge is in the graph.
//
// An explicit edge explains itself: Inferred is false and it names no support.
// A derived edge names the rule that derived it — including the rule text,
// which is stored on the edge rather than looked up, so an edge stays
// explicable after its rule is deleted or edited — and the exact premise edges
// under it.
//
// It is deliberately one level deep. The chain is returned separately, by
// ExplainEdgeTrace, as a flat list: a self-referential struct cannot be given a
// JSON schema, so a nested explanation could never have crossed the MCP
// boundary most callers reach this through.
type RuleEdgeExplanation struct {
	EdgeID     string  `json:"edge_id"`
	EdgeType   string  `json:"edge_type,omitempty"`
	FromNodeID string  `json:"from_node_id,omitempty"`
	ToNodeID   string  `json:"to_node_id,omitempty"`
	Inferred   bool    `json:"inferred"`
	Provenance string  `json:"provenance,omitempty"`
	RuleID     string  `json:"rule_id,omitempty"`
	RuleText   string  `json:"rule_text,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	// SupportEdgeIDs are the exact premise edges, in the order of the rule's
	// premises.
	SupportEdgeIDs []string `json:"support_edge_ids,omitempty"`
	// Missing marks a premise edge that is no longer in the graph — somebody
	// deleted the evidence without retracting the conclusion.
	Missing bool `json:"missing,omitempty"`
}

// RuleEdgeTraceEntry is one node of a flattened derivation, in preorder: the
// edge asked about at depth 0, then its premises, then theirs.
type RuleEdgeTraceEntry struct {
	EdgeID       string              `json:"edge_id"`
	ParentEdgeID string              `json:"parent_edge_id,omitempty"`
	Depth        int                 `json:"depth"`
	Explanation  RuleEdgeExplanation `json:"explanation"`
	// Truncated marks an inferred edge whose own premises were not followed,
	// because the depth ran out or because the chain looped. Without it a leaf
	// of a truncated trace reads as an explicit edge.
	Truncated bool `json:"truncated,omitempty"`
}

// ExplainEdge returns the immediate derivation of one edge: whether it was
// inferred, by which rule, and which edges it stands on.
func (g *GraphStore) ExplainEdge(ctx context.Context, edgeID string) (*RuleEdgeExplanation, error) {
	if edgeID == "" {
		return nil, fmt.Errorf("edge id is required")
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}
	explanation, err := g.explainOneEdge(ctx, edgeID)
	if err != nil {
		return nil, err
	}
	if explanation == nil {
		return nil, fmt.Errorf("edge not found: %s", edgeID)
	}
	return explanation, nil
}

// ExplainEdgeTrace follows the premise chain up to depth levels and returns it
// flattened in preorder. Depth zero means DefaultRuleExplainDepth.
func (g *GraphStore) ExplainEdgeTrace(ctx context.Context, edgeID string, depth int) ([]RuleEdgeTraceEntry, error) {
	root, err := g.ExplainEdge(ctx, edgeID)
	if err != nil {
		return nil, err
	}
	if depth <= 0 {
		depth = DefaultRuleExplainDepth
	}
	entries := make([]RuleEdgeTraceEntry, 0, 1)
	seen := map[string]bool{}
	if err := g.appendRuleTrace(ctx, *root, "", 0, depth, seen, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (g *GraphStore) appendRuleTrace(ctx context.Context, explanation RuleEdgeExplanation, parentEdgeID string, depth, remaining int, seen map[string]bool, entries *[]RuleEdgeTraceEntry) error {
	entry := RuleEdgeTraceEntry{
		EdgeID:       explanation.EdgeID,
		ParentEdgeID: parentEdgeID,
		Depth:        depth,
		Explanation:  explanation,
	}
	follow := explanation.Inferred && len(explanation.SupportEdgeIDs) > 0
	// A cycle in the support chain should not be possible — a fact is derived
	// only once, from facts that already existed — but an edited graph can
	// contain one, and a trace that recursed forever would take the process
	// with it.
	if follow && (remaining <= 1 || seen[explanation.EdgeID]) {
		entry.Truncated = true
		follow = false
	}
	*entries = append(*entries, entry)
	if !follow {
		return nil
	}
	seen[explanation.EdgeID] = true
	defer delete(seen, explanation.EdgeID)

	for _, supportID := range explanation.SupportEdgeIDs {
		support, err := g.explainOneEdge(ctx, supportID)
		if err != nil {
			return err
		}
		if support == nil {
			support = &RuleEdgeExplanation{EdgeID: supportID, Missing: true}
		}
		if err := g.appendRuleTrace(ctx, *support, explanation.EdgeID, depth+1, remaining-1, seen, entries); err != nil {
			return err
		}
	}
	return nil
}

func (g *GraphStore) explainOneEdge(ctx context.Context, edgeID string) (*RuleEdgeExplanation, error) {
	edge, err := g.lookupRuleEdge(ctx, edgeID)
	if err != nil {
		return nil, err
	}
	if edge == nil {
		return nil, nil
	}
	explanation := &RuleEdgeExplanation{
		EdgeID:     edge.ID,
		EdgeType:   edge.EdgeType,
		FromNodeID: edge.FromNodeID,
		ToNodeID:   edge.ToNodeID,
	}
	if inferred, ok := edge.Properties["inferred"].(bool); ok {
		explanation.Inferred = inferred
	}
	if provenance, ok := edge.Properties["provenance"].(string); ok {
		explanation.Provenance = provenance
	}
	if ruleID, ok := edge.Properties["rule_id"].(string); ok {
		explanation.RuleID = ruleID
	}
	if ruleText, ok := edge.Properties["rule_text"].(string); ok {
		explanation.RuleText = ruleText
	}
	explanation.Confidence = ruleFloatProperty(edge.Properties, "confidence", 0)
	explanation.SupportEdgeIDs = stringSliceProperty(edge.Properties, "support_edge_ids")
	return explanation, nil
}

func (g *GraphStore) lookupRuleEdge(ctx context.Context, edgeID string) (*GraphEdge, error) {
	var (
		edge          GraphEdge
		edgeType      sql.NullString
		propertiesRaw sql.NullString
	)
	err := g.queryRow(ctx, `
		SELECT id, from_node_id, to_node_id, edge_type, weight, properties
		FROM graph_edges
		WHERE id = ?
		ORDER BY id ASC
	`, edgeID).Scan(&edge.ID, &edge.FromNodeID, &edge.ToNodeID, &edgeType, &edge.Weight, &propertiesRaw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read edge %s: %w", edgeID, err)
	}
	edge.EdgeType = edgeType.String
	if propertiesRaw.Valid && propertiesRaw.String != "" {
		if err := json.Unmarshal([]byte(propertiesRaw.String), &edge.Properties); err != nil {
			return nil, fmt.Errorf("decode edge %s properties: %w", edgeID, err)
		}
	}
	return &edge, nil
}

func stringSliceProperty(properties map[string]any, key string) []string {
	switch value := properties[key].(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}
