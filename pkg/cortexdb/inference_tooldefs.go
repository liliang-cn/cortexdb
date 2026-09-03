package cortexdb

func inferenceToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name: "apply_inference",
			// Inference sounds like a question about what is already there. It
			// is not: the inferred edges are materialized into the graph, and
			// delete_existing removes the previous ones first.
			Mutates:     true,
			Description: "Apply deterministic two-hop inference rules to explicit relation edges and materialize inferred edges with provenance.",
			InputSchema: toolObjectSchema(
				[]string{"rules"},
				map[string]any{
					"document_id":     toolStringSchema("Optional document scope. When set, only relations for that document participate."),
					"delete_existing": toolBooleanSchema("When true, delete existing rule-derived inferred edges for this scope before reapplying."),
					"rules": map[string]any{
						"type": "array",
						"items": toolObjectSchema(
							[]string{"rule_id", "left_relation_type", "right_relation_type", "result_relation_type"},
							map[string]any{
								"rule_id":              toolStringSchema("Stable rule ID used in inferred-edge provenance."),
								"description":          toolStringSchema("Optional rule description."),
								"left_relation_type":   toolStringSchema("First hop relation type."),
								"right_relation_type":  toolStringSchema("Second hop relation type whose source matches the first hop target."),
								"result_relation_type": toolStringSchema("Relation type to materialize for the inferred edge."),
								"weight":               toolNumberSchema("Optional inferred edge weight override. Defaults to the average support-edge weight."),
								"metadata":             toolMapSchema("Optional metadata to write onto inferred edges."),
							},
						),
					},
				},
			),
		},
	}
}
