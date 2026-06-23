package cortexdb

func toolQueryRequestSchema() map[string]any {
	return toolObjectSchema(
		nil,
		map[string]any{
			"collection":   toolStringSchema("Optional collection name to search within."),
			"query":        toolStringSchema("Natural-language query text for lexical or embedder-backed vector prefetches."),
			"query_vector": toolNumberArraySchema("Optional precomputed dense query vector."),
			"entity_names": toolStringArraySchema("Entity names used by graph prefetch. When present, default query planning includes a graph lane."),
			"prefetch": map[string]any{
				"type":        "array",
				"description": "Retrieval lanes to run before fusion. Omit to let CortexDB choose vector+lexical or hybrid defaults.",
				"items":       toolQueryPrefetchSchema(),
			},
			"fusion":      toolEnumSchema("Candidate fusion method.", QueryFusionRRF, QueryFusionWeightedRRF, QueryFusionDBSF),
			"filter":      toolQueryFilterSchema(),
			"formula":     toolQueryScoreFormulaSchema(),
			"limit":       toolIntegerSchema("Maximum final results to return."),
			"rrf_k":       toolNumberSchema("RRF smoothing parameter. Defaults to 60."),
			"include_raw": toolBooleanSchema("Return source ranks and raw source scores for debugging."),
		},
	)
}

func toolQueryPrefetchSchema() map[string]any {
	return toolObjectSchema(
		nil,
		map[string]any{
			"name":              toolStringSchema("Optional stable source name, e.g. dense, lexical, title_vector."),
			"kind":              toolEnumSchema("Retrieval lane type.", QueryPrefetchVector, QueryPrefetchLexical, QueryPrefetchHybrid, QueryPrefetchGraph),
			"query":             toolStringSchema("Optional query text override for this prefetch."),
			"query_vector":      toolNumberArraySchema("Optional precomputed vector override for this prefetch."),
			"entity_names":      toolStringArraySchema("Entity names for graph prefetch."),
			"weight":            toolNumberSchema("Fusion weight. Used by weighted_rrf and dbsf."),
			"limit":             toolIntegerSchema("Candidate count for this prefetch before filtering and fusion."),
			"max_hops":          toolIntegerSchema("Graph traversal depth for graph prefetch."),
			"keywords":          toolStringArraySchema("Expanded keywords for lexical prefetch."),
			"alternate_queries": toolStringArraySchema("Alternate lexical phrasings for this prefetch."),
		},
	)
}

func toolQueryFilterSchema() map[string]any {
	conditionArray := map[string]any{
		"type":  "array",
		"items": toolQueryConditionSchema(),
	}
	return map[string]any{
		"type":        "object",
		"description": "Payload filter. Fields can be metadata keys or top-level id, doc_id, collection, content.",
		"properties": map[string]any{
			"must":     conditionArray,
			"should":   conditionArray,
			"must_not": conditionArray,
		},
	}
}

func toolQueryConditionSchema() map[string]any {
	return toolObjectSchema(
		[]string{"field"},
		map[string]any{
			"field":      toolStringSchema("Metadata key or top-level field: id, doc_id, collection, content."),
			"op":         toolEnumSchema("Comparison operator.", QueryFilterEqual, QueryFilterNotEqual, QueryFilterIn, QueryFilterContains, QueryFilterGTE, QueryFilterLTE),
			"value":      toolStringSchema("String comparison value, or numeric value encoded as a string."),
			"values":     toolStringArraySchema("Allowed values for op=in."),
			"number":     toolNumberSchema("Numeric comparison value for gte/lte."),
			"has_number": toolBooleanSchema("Set true when number should be used instead of value for gte/lte."),
		},
	)
}

func toolQueryScoreFormulaSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Formula rerank layer applied after fusion.",
		"properties": map[string]any{
			"base_weight": toolNumberSchema("Multiplier for the fused base score. Defaults to 1."),
			"field_boosts": map[string]any{
				"type":  "array",
				"items": toolQueryFieldBoostSchema(),
			},
			"numeric_boosts": map[string]any{
				"type":  "array",
				"items": toolQueryNumericBoostSchema(),
			},
		},
	}
}

func toolQueryFieldBoostSchema() map[string]any {
	return toolObjectSchema(
		[]string{"field", "weight"},
		map[string]any{
			"field":    toolStringSchema("Metadata key or top-level field to inspect."),
			"equals":   toolStringSchema("Add weight when field exactly equals this value."),
			"contains": toolStringSchema("Add weight when field contains this value, case-insensitive."),
			"weight":   toolNumberSchema("Score contribution to add when matched."),
		},
	)
}

func toolQueryNumericBoostSchema() map[string]any {
	return toolObjectSchema(
		[]string{"field", "weight"},
		map[string]any{
			"field":     toolStringSchema("Metadata key or top-level numeric field to inspect."),
			"weight":    toolNumberSchema("Score multiplier for the numeric value."),
			"max_value": toolNumberSchema("Optional maximum used to normalize the numeric value to 0..1."),
		},
	)
}
