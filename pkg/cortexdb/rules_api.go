package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Rules a caller declares, on top of the one shape CortexDB used to ship.
//
// apply_inference materialises a two-hop composition and nothing else. This is
// the general form behind it: a rule is any Horn clause over graph edges — any
// number of premises, variables bound across them — forward-chained to a
// bounded fixpoint. The derived edges carry the provenance apply_inference
// always wrote, which is why inference_explain can explain either kind.

// RuleDefinition is a rule as a caller supplies it.
//
// Two ways in, one rule: Text is the written form
// ("IF p(?x, ?y) AND q(?y, ?z) THEN r(?x, ?z)"), When/Then the structured form
// a program builds. Supplying Text is what a person does; supplying both is an
// error rather than a silent preference, because the two disagreeing is exactly
// the confusion this would otherwise create.
type RuleDefinition struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Text string `json:"text,omitempty"`
	// When and Then are the structured form. Ignored when Text is set.
	When []graph.Atom `json:"when,omitempty"`
	Then *graph.Atom  `json:"then,omitempty"`
	// Confidence multiplies into every derived edge's confidence. Zero is 1.0.
	Confidence float64 `json:"confidence,omitempty"`
	// Weight overrides the derived edge weight. Zero means the mean of the
	// premise weights.
	Weight   float64           `json:"weight,omitempty"`
	Note     string            `json:"note,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	// Enabled defaults to true on save. A disabled rule stays in the table and
	// out of rules_apply's default set.
	Enabled *bool `json:"enabled,omitempty"`
}

// Rule converts a definition into the engine's rule, or says why it cannot.
func (d RuleDefinition) Rule() (graph.Rule, error) {
	hasText := strings.TrimSpace(d.Text) != ""
	hasStructure := len(d.When) > 0 || d.Then != nil
	switch {
	case hasText && hasStructure:
		return graph.Rule{}, fmt.Errorf("rule %s: supply either text or when/then, not both", d.ID)
	case !hasText && !hasStructure:
		return graph.Rule{}, fmt.Errorf("rule %s: text or when/then is required", d.ID)
	}

	var rule graph.Rule
	if hasText {
		parsed, err := graph.ParseRuleText(d.Text)
		if err != nil {
			return graph.Rule{}, fmt.Errorf("rule %s: %w", d.ID, err)
		}
		rule = parsed
	} else {
		if d.Then == nil {
			return graph.Rule{}, fmt.Errorf("rule %s: then is required", d.ID)
		}
		rule.When = d.When
		rule.Then = *d.Then
	}
	rule.ID = d.ID
	rule.Name = d.Name
	rule.Confidence = d.Confidence
	rule.Weight = d.Weight
	rule.Note = d.Note
	rule.Metadata = cloneStringMap(d.Metadata)
	if err := rule.Validate(); err != nil {
		return graph.Rule{}, err
	}
	return rule, nil
}

func (d RuleDefinition) enabled() bool {
	if d.Enabled == nil {
		return true
	}
	return *d.Enabled
}

// RulesSaveRequest stores rules under their ids, replacing any already there.
type RulesSaveRequest struct {
	Rules []RuleDefinition `json:"rules"`
}

// RulesSaveResponse returns the rules as stored, rendered back into text.
type RulesSaveResponse struct {
	Rules []graph.StoredRule `json:"rules"`
}

// RulesListRequest lists the declared rules.
type RulesListRequest struct {
	OnlyEnabled bool `json:"only_enabled,omitempty"`
}

// RulesListResponse returns the stored rules in id order.
type RulesListResponse struct {
	Rules []graph.StoredRule `json:"rules"`
}

// RulesDeleteRequest removes stored rules by id.
type RulesDeleteRequest struct {
	RuleIDs []string `json:"rule_ids"`
}

// RulesDeleteResponse separates what was there from what was not, because
// "deleted 0 of 3" is the answer a caller needs and a bare success is not.
type RulesDeleteResponse struct {
	DeletedRuleIDs []string `json:"deleted_rule_ids"`
	MissingRuleIDs []string `json:"missing_rule_ids,omitempty"`
}

// RulesApplyRequest forward-chains rules over the graph.
type RulesApplyRequest struct {
	// RuleIDs selects stored rules. Rules supplies ad-hoc ones that are not
	// saved. With neither, every enabled stored rule runs.
	RuleIDs []string         `json:"rule_ids,omitempty"`
	Rules   []RuleDefinition `json:"rules,omitempty"`
	// DocumentID scopes which edges take part and is stamped onto what is
	// derived.
	DocumentID string `json:"document_id,omitempty"`
	// DryRun reports what would be derived and writes nothing.
	DryRun bool `json:"dry_run,omitempty"`
	// DeleteExisting removes the edges these rules derived before rerunning.
	DeleteExisting bool `json:"delete_existing,omitempty"`
	MaxIterations  int  `json:"max_iterations,omitempty"`
	MaxDerived     int  `json:"max_derived,omitempty"`
}

// RuleDerivedEdge is one derived edge with the provenance it carries.
type RuleDerivedEdge struct {
	EdgeID         string   `json:"edge_id"`
	FromNodeID     string   `json:"from_node_id"`
	ToNodeID       string   `json:"to_node_id"`
	EdgeType       string   `json:"edge_type"`
	Weight         float64  `json:"weight,omitempty"`
	Confidence     float64  `json:"confidence,omitempty"`
	RuleID         string   `json:"rule_id,omitempty"`
	RuleText       string   `json:"rule_text,omitempty"`
	SupportEdgeIDs []string `json:"support_edge_ids,omitempty"`
}

// RulesApplyResponse reports one forward-chaining run.
type RulesApplyResponse struct {
	RuleIDs          []string          `json:"rule_ids"`
	Iterations       int               `json:"iterations"`
	CandidateEdges   int               `json:"candidate_edges"`
	CreatedEdgeIDs   []string          `json:"created_edge_ids,omitempty"`
	UnchangedEdgeIDs []string          `json:"unchanged_edge_ids,omitempty"`
	DeletedEdgeIDs   []string          `json:"deleted_edge_ids,omitempty"`
	Edges            []RuleDerivedEdge `json:"edges,omitempty"`
	UnresolvedTerms  []string          `json:"unresolved_terms,omitempty"`
	DryRun           bool              `json:"dry_run,omitempty"`
}

// InferenceExplainRequest asks why an edge is in the graph.
type InferenceExplainRequest struct {
	EdgeID string `json:"edge_id"`
	Depth  int    `json:"depth,omitempty"`
}

// InferenceExplainResponse is the derivation of one edge: the edge itself, and
// the premise chain under it flattened in preorder.
type InferenceExplainResponse struct {
	Explanation graph.RuleEdgeExplanation  `json:"explanation"`
	Trace       []graph.RuleEdgeTraceEntry `json:"trace,omitempty"`
}

// SaveRules stores rules under their ids.
func (db *DB) SaveRules(ctx context.Context, req RulesSaveRequest) (*RulesSaveResponse, error) {
	if len(req.Rules) == 0 {
		return nil, fmt.Errorf("rules are required")
	}
	resp := &RulesSaveResponse{Rules: make([]graph.StoredRule, 0, len(req.Rules))}
	for _, definition := range req.Rules {
		rule, err := definition.Rule()
		if err != nil {
			return nil, err
		}
		stored, err := db.graph.SaveRule(ctx, rule, definition.enabled())
		if err != nil {
			return nil, err
		}
		resp.Rules = append(resp.Rules, *stored)
	}
	return resp, nil
}

// ListRules returns the declared rules in id order.
func (db *DB) ListRules(ctx context.Context, req RulesListRequest) (*RulesListResponse, error) {
	rules, err := db.graph.ListRules(ctx, req.OnlyEnabled)
	if err != nil {
		return nil, err
	}
	return &RulesListResponse{Rules: rules}, nil
}

// DeleteRules removes stored rules. The edges they derived stay: deleting a
// rule says what will not be derived next time, not that what was derived was
// wrong.
func (db *DB) DeleteRules(ctx context.Context, req RulesDeleteRequest) (*RulesDeleteResponse, error) {
	if len(req.RuleIDs) == 0 {
		return nil, fmt.Errorf("rule_ids are required")
	}
	resp := &RulesDeleteResponse{DeletedRuleIDs: make([]string, 0, len(req.RuleIDs))}
	for _, ruleID := range req.RuleIDs {
		deleted, err := db.graph.DeleteRule(ctx, ruleID)
		if err != nil {
			return nil, err
		}
		if deleted {
			resp.DeletedRuleIDs = append(resp.DeletedRuleIDs, ruleID)
			continue
		}
		resp.MissingRuleIDs = append(resp.MissingRuleIDs, ruleID)
	}
	return resp, nil
}

// ApplyRules forward-chains rules over the graph to a fixpoint and writes what
// they derive, with the provenance inference_explain reads back.
func (db *DB) ApplyRules(ctx context.Context, req RulesApplyRequest) (*RulesApplyResponse, error) {
	rules, err := db.resolveRulesToApply(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("no rules to apply: pass rule_ids or rules, or declare rules with rules_save")
	}

	resp := &RulesApplyResponse{DryRun: req.DryRun}
	for _, rule := range rules {
		resp.RuleIDs = append(resp.RuleIDs, rule.ID)
	}
	if req.DeleteExisting && !req.DryRun {
		deleted, err := db.deleteInferredEdges(ctx, req.DocumentID, resp.RuleIDs)
		if err != nil {
			return nil, err
		}
		resp.DeletedEdgeIDs = deleted
	}

	result, err := db.graph.ApplyRules(ctx, rules, graph.RuleOptions{
		DocumentID:    req.DocumentID,
		DryRun:        req.DryRun,
		MaxIterations: req.MaxIterations,
		MaxDerived:    req.MaxDerived,
		Validate:      db.validateRuleDerivedEdges,
	})
	if err != nil {
		return nil, err
	}
	resp.Iterations = result.Iterations
	resp.CandidateEdges = result.CandidateEdges
	resp.CreatedEdgeIDs = result.CreatedEdgeIDs
	resp.UnchangedEdgeIDs = result.UnchangedEdgeIDs
	resp.UnresolvedTerms = result.UnresolvedTerms
	for _, edge := range result.Edges {
		ruleID, _ := edge.Properties["rule_id"].(string)
		ruleText, _ := edge.Properties["rule_text"].(string)
		confidence, _ := edge.Properties["confidence"].(float64)
		supports, _ := edge.Properties["support_edge_ids"].([]string)
		resp.Edges = append(resp.Edges, RuleDerivedEdge{
			EdgeID:         edge.ID,
			FromNodeID:     edge.FromNodeID,
			ToNodeID:       edge.ToNodeID,
			EdgeType:       edge.EdgeType,
			Weight:         edge.Weight,
			Confidence:     confidence,
			RuleID:         ruleID,
			RuleText:       ruleText,
			SupportEdgeIDs: supports,
		})
	}
	return resp, nil
}

// ExplainInference returns the derivation of one edge: whether it was inferred,
// by which rule, and the premise edges under it, each explained in turn.
func (db *DB) ExplainInference(ctx context.Context, req InferenceExplainRequest) (*InferenceExplainResponse, error) {
	if strings.TrimSpace(req.EdgeID) == "" {
		return nil, fmt.Errorf("edge_id is required")
	}
	explanation, err := db.graph.ExplainEdge(ctx, req.EdgeID)
	if err != nil {
		return nil, err
	}
	trace, err := db.graph.ExplainEdgeTrace(ctx, req.EdgeID, req.Depth)
	if err != nil {
		return nil, err
	}
	return &InferenceExplainResponse{Explanation: *explanation, Trace: trace}, nil
}

// resolveRulesToApply assembles the rule set for one run: the named stored
// rules, the ad-hoc ones, or — with neither — every enabled stored rule.
func (db *DB) resolveRulesToApply(ctx context.Context, req RulesApplyRequest) ([]graph.Rule, error) {
	rules := make([]graph.Rule, 0, len(req.RuleIDs)+len(req.Rules))
	seen := make(map[string]struct{}, len(rules))

	for _, ruleID := range req.RuleIDs {
		stored, err := db.graph.GetRule(ctx, ruleID)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, fmt.Errorf("rule not found: %s", ruleID)
		}
		if _, dup := seen[stored.ID]; dup {
			continue
		}
		seen[stored.ID] = struct{}{}
		rules = append(rules, stored.Rule)
	}
	for _, definition := range req.Rules {
		rule, err := definition.Rule()
		if err != nil {
			return nil, err
		}
		if _, dup := seen[rule.ID]; dup {
			return nil, fmt.Errorf("rule %s is supplied twice", rule.ID)
		}
		seen[rule.ID] = struct{}{}
		rules = append(rules, rule)
	}
	if len(rules) > 0 {
		return rules, nil
	}

	stored, err := db.graph.ListRules(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, rule := range stored {
		rules = append(rules, rule.Rule)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	return rules, nil
}

// SaveRules stores rules through the tool surface.
func (t *GraphRAGToolbox) SaveRules(ctx context.Context, req RulesSaveRequest) (*RulesSaveResponse, error) {
	return t.db.SaveRules(ctx, req)
}

// ListRules lists rules through the tool surface.
func (t *GraphRAGToolbox) ListRules(ctx context.Context, req RulesListRequest) (*RulesListResponse, error) {
	return t.db.ListRules(ctx, req)
}

// DeleteRules removes rules through the tool surface.
func (t *GraphRAGToolbox) DeleteRules(ctx context.Context, req RulesDeleteRequest) (*RulesDeleteResponse, error) {
	return t.db.DeleteRules(ctx, req)
}

// ApplyRules forward-chains rules through the tool surface.
func (t *GraphRAGToolbox) ApplyRules(ctx context.Context, req RulesApplyRequest) (*RulesApplyResponse, error) {
	return t.db.ApplyRules(ctx, req)
}

// ExplainInference explains one edge through the tool surface.
func (t *GraphRAGToolbox) ExplainInference(ctx context.Context, req InferenceExplainRequest) (*InferenceExplainResponse, error) {
	return t.db.ExplainInference(ctx, req)
}
