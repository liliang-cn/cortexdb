package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ruleToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name: "rules_save",
			// Writes to the rule table. It derives nothing by itself — that is
			// rules_apply — but a saved rule is what rules_apply runs by
			// default, so saving one is a change to what the brain will infer.
			Mutates:     true,
			Description: "Declare inference rules. A rule is written as \"IF p(?x, ?y) AND q(?y, ?z) THEN r(?x, ?z)\": any number of premises, variables bound across them, one conclusion. Terms starting with ? are variables; anything else is a literal matched against a node id, or against Type:Name. Saving a rule stores it; run it with rules_apply.",
			InputSchema: toolObjectSchema(
				[]string{"rules"},
				map[string]any{
					"rules": map[string]any{
						"type":  "array",
						"items": toolRuleDefinitionSchema(),
					},
				},
			),
		},
		{
			Name:        "rules_list",
			Description: "List the declared inference rules, each with its written form and whether it is enabled.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"only_enabled": toolBooleanSchema("List only enabled rules."),
				},
			),
		},
		{
			Name:    "rules_delete",
			Mutates: true,
			// Deleting a rule leaves the edges it derived in place, which is
			// why this is not a retraction tool. Say so where an agent reads it.
			Description: "Delete declared inference rules by id. Edges those rules already derived stay in the graph — they carry the rule text with them and remain explicable. To remove them, run rules_apply with delete_existing.",
			InputSchema: toolObjectSchema(
				[]string{"rule_ids"},
				map[string]any{
					"rule_ids": toolStringArraySchema("Rule IDs to delete."),
				},
			),
		},
		{
			Name: "rules_apply",
			// dry_run makes one argument value read-only, which authorization
			// never sees. A tool that can write is a write.
			Mutates:     true,
			Description: "Forward-chain inference rules over the graph to a fixpoint and materialize what they derive. Every derived edge carries inferred=true, the rule id and text, the exact premise edge ids, and a confidence that is the minimum premise confidence times the rule's. Re-running derives nothing new. With neither rule_ids nor rules, every enabled declared rule runs. Use dry_run first.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"rule_ids": toolStringArraySchema("Declared rule IDs to run. Omit both this and rules to run every enabled rule."),
					"rules": map[string]any{
						"type":        "array",
						"description": "Ad-hoc rules to run without saving them.",
						"items":       toolRuleDefinitionSchema(),
					},
					"document_id":     toolStringSchema("Optional document scope. When set, only relations for that document participate, and derived edges are stamped with it."),
					"dry_run":         toolBooleanSchema("Report what would be derived without writing it."),
					"delete_existing": toolBooleanSchema("Delete the edges these rules previously derived before rerunning."),
					"max_iterations":  toolIntegerSchema("Cap on chaining rounds (default 16). Hitting it is an error that writes nothing, not a partial result."),
					"max_derived":     toolIntegerSchema("Cap on edges one run may derive (default 50000). Hitting it is an error that writes nothing."),
				},
			),
		},
		{
			Name:        "inference_explain",
			Description: "Explain why a graph edge exists: whether it was inferred, by which rule and with what text, and the exact premise edges under it, each explained in turn. Works for edges derived by rules_apply and by apply_inference.",
			InputSchema: toolObjectSchema(
				[]string{"edge_id"},
				map[string]any{
					"edge_id": toolStringSchema("Edge ID to explain."),
					"depth":   toolIntegerSchema("How far to follow the premise chain (default 4)."),
				},
			),
		},
	}
}

func toolRuleDefinitionSchema() map[string]any {
	return toolObjectSchema(
		[]string{"id"},
		map[string]any{
			"id":   toolStringSchema("Stable rule ID, written into the provenance of every edge it derives."),
			"name": toolStringSchema("Optional human label."),
			"text": toolStringSchema("The rule in written form, e.g. \"IF works_at(?x, ?y) AND located_in(?y, ?z) THEN works_in_city(?x, ?z)\". Supply this or when/then, not both."),
			"when": map[string]any{
				"type":        "array",
				"description": "Premises, as structured atoms. Supply this with then, or supply text instead.",
				"items":       toolRuleAtomSchema(),
			},
			"then":       toolRuleAtomSchema(),
			"confidence": toolNumberSchema("Rule confidence in [0,1]. Multiplies into every derived edge's confidence. Defaults to 1."),
			"weight":     toolNumberSchema("Optional derived edge weight override. Defaults to the mean of the premise weights."),
			"note":       toolStringSchema("Optional free-text note stored with the rule."),
			"metadata":   toolMapSchema("Optional metadata written onto derived edges. Provenance keys cannot be overridden."),
			"enabled":    toolBooleanSchema("Whether rules_apply runs this rule by default. Defaults to true."),
		},
	)
}

func toolRuleAtomSchema() map[string]any {
	return toolObjectSchema(
		[]string{"predicate", "subject", "object"},
		map[string]any{
			"predicate": toolStringSchema("Relation type. Must be literal — predicates cannot be variables."),
			"subject":   toolStringSchema("A variable (?x) or a literal: an exact node id, or Type:Name matched case-insensitively against node_type and the node's name."),
			"object":    toolStringSchema("A variable (?x) or a literal, matched the same way as subject."),
		},
	)
}

func (t *GraphRAGToolbox) callRuleTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case "rules_save":
		var req RulesSaveRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.SaveRules(ctx, req)
		return resp, true, err
	case "rules_list":
		var req RulesListRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.ListRules(ctx, req)
		return resp, true, err
	case "rules_delete":
		var req RulesDeleteRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.DeleteRules(ctx, req)
		return resp, true, err
	case "rules_apply":
		var req RulesApplyRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.ApplyRules(ctx, req)
		return resp, true, err
	case "inference_explain":
		var req InferenceExplainRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.ExplainInference(ctx, req)
		return resp, true, err
	default:
		return nil, false, nil
	}
}

func addRuleMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, toolbox *GraphRAGToolbox) {
	addGraphRAGMCPTool(server, definitions["rules_save"], func(ctx context.Context, req RulesSaveRequest) (RulesSaveResponse, error) {
		return derefToolResponse(toolbox.SaveRules(ctx, req))
	})
	addGraphRAGMCPTool(server, definitions["rules_list"], func(ctx context.Context, req RulesListRequest) (RulesListResponse, error) {
		return derefToolResponse(toolbox.ListRules(ctx, req))
	})
	addGraphRAGMCPTool(server, definitions["rules_delete"], func(ctx context.Context, req RulesDeleteRequest) (RulesDeleteResponse, error) {
		return derefToolResponse(toolbox.DeleteRules(ctx, req))
	})
	addGraphRAGMCPTool(server, definitions["rules_apply"], func(ctx context.Context, req RulesApplyRequest) (RulesApplyResponse, error) {
		return derefToolResponse(toolbox.ApplyRules(ctx, req))
	})
	addGraphRAGMCPTool(server, definitions["inference_explain"], func(ctx context.Context, req InferenceExplainRequest) (InferenceExplainResponse, error) {
		return derefToolResponse(toolbox.ExplainInference(ctx, req))
	})
}

// derefToolResponse turns the (*T, error) the facade returns into the (T, error)
// the MCP adapter wants, without every handler repeating the nil check.
func derefToolResponse[T any](resp *T, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	if resp == nil {
		return zero, nil
	}
	return *resp, nil
}
