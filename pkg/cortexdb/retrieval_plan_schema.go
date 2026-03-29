package cortexdb

func toolRetrievalPlanSchema(description string) map[string]any {
	return map[string]any{
		"type":        "object",
		"description": description,
		"properties": map[string]any{
			"query":             toolStringSchema("Canonical query text for this search plan."),
			"keywords":          toolStringArraySchema("Expanded keywords, aliases, synonyms, abbreviations, and multilingual terms."),
			"alternate_queries": toolStringArraySchema("Alternate phrasings generated from the same goal."),
			"entity_names":      toolStringArraySchema("Structured entity hints to enable graph-aware retrieval."),
			"retrieval_mode":    toolEnumSchema("Preferred retrieval strategy.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
			"filters":           toolRetrievalFiltersSchema(),
		},
	}
}

func toolRetrievalFiltersSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection":   toolStringSchema("Optional collection override for this search plan."),
			"document_ids": toolStringArraySchema("Optional list of document IDs to keep in scope."),
			"user_id":      toolStringSchema("Optional user ID filter for memory search."),
			"session_id":   toolStringSchema("Optional session ID filter for memory search."),
			"scope":        toolEnumSchema("Optional memory scope.", MemoryScopeGlobal, MemoryScopeUser, MemoryScopeSession),
			"namespace":    toolStringSchema("Optional memory namespace filter."),
		},
	}
}
