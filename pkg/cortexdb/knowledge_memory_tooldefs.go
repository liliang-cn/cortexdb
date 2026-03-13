package cortexdb

func knowledgeMemoryToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "knowledge_save",
			Description: "Store or replace a durable knowledge item. This is the preferred high-level write API for documents, notes, facts, and structured knowledge.",
			InputSchema: toolObjectSchema(
				[]string{"knowledge_id", "content"},
				map[string]any{
					"knowledge_id":  toolStringSchema("Stable knowledge/document ID."),
					"title":         toolStringSchema("Optional title."),
					"content":       toolStringSchema("Full knowledge content."),
					"source_url":    toolStringSchema("Optional source URL."),
					"author":        toolStringSchema("Optional author."),
					"collection":    toolStringSchema("Optional chunk collection."),
					"chunk_size":    toolIntegerSchema("Optional chunk size in words."),
					"chunk_overlap": toolIntegerSchema("Optional chunk overlap in words."),
					"metadata":      toolMapSchema("Optional metadata."),
					"entities":      toolEntityArraySchema(),
					"relations":     toolRelationArraySchema(),
				},
			),
		},
		{
			Name:        "knowledge_update",
			Description: "Update a durable knowledge item. If content, title, collection, or metadata changes, the underlying chunks and graph artifacts are refreshed.",
			InputSchema: toolObjectSchema(
				[]string{"knowledge_id"},
				map[string]any{
					"knowledge_id":  toolStringSchema("Stable knowledge/document ID."),
					"title":         toolStringSchema("Optional new title."),
					"content":       toolStringSchema("Optional full replacement content."),
					"source_url":    toolStringSchema("Optional new source URL."),
					"author":        toolStringSchema("Optional new author."),
					"collection":    toolStringSchema("Optional new chunk collection."),
					"chunk_size":    toolIntegerSchema("Optional chunk size for refreshed content."),
					"chunk_overlap": toolIntegerSchema("Optional chunk overlap for refreshed content."),
					"metadata":      toolMapSchema("Optional replacement metadata."),
					"entities":      toolEntityArraySchema(),
					"relations":     toolRelationArraySchema(),
				},
			),
		},
		{
			Name:        "knowledge_get",
			Description: "Fetch one durable knowledge item by ID.",
			InputSchema: toolObjectSchema(
				[]string{"knowledge_id"},
				map[string]any{
					"knowledge_id": toolStringSchema("Stable knowledge/document ID."),
				},
			),
		},
		{
			Name:        "knowledge_search",
			Description: "Search durable knowledge. When no embedder is available, first expand the user's goal into keywords, aliases, synonyms, abbreviations, and multilingual variants.",
			InputSchema: toolObjectSchema(
				[]string{"query"},
				map[string]any{
					"query":              toolStringSchema("User goal or natural-language question."),
					"collection":         toolStringSchema("Optional chunk collection."),
					"top_k":              toolIntegerSchema("Seed chunk count."),
					"max_hops":           toolIntegerSchema("Graph expansion depth."),
					"max_related_chunks": toolIntegerSchema("Maximum graph-expanded chunks."),
					"max_context_chunks": toolIntegerSchema("Maximum chunks in final context."),
					"max_context_chars":  toolIntegerSchema("Maximum context character budget."),
					"per_document_limit": toolIntegerSchema("Maximum chunks per document."),
					"diversity_lambda":   toolNumberSchema("Rerank diversity weight between 0 and 1."),
					"entity_names":       toolStringArraySchema("Optional entities from structured planning."),
					"keywords":           toolStringArraySchema("LLM-generated keyword bank derived from the goal."),
					"alternate_queries":  toolStringArraySchema("Alternate phrasings generated from the same goal."),
					"retrieval_mode":     toolEnumSchema("Preferred retrieval strategy.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":      toolBooleanSchema("Legacy alias. Set true to force lexical-only retrieval."),
				},
			),
		},
		{
			Name:        "knowledge_delete",
			Description: "Delete a durable knowledge item and its chunk/document graph artifacts.",
			InputSchema: toolObjectSchema(
				[]string{"knowledge_id"},
				map[string]any{
					"knowledge_id": toolStringSchema("Stable knowledge/document ID."),
				},
			),
		},
		{
			Name:        "memory_save",
			Description: "Store a memory item in a dedicated memory bucket. Use scope=user/session/global to control where the memory lives.",
			InputSchema: toolObjectSchema(
				[]string{"memory_id", "content"},
				map[string]any{
					"memory_id":   toolStringSchema("Stable memory ID."),
					"user_id":     toolStringSchema("Optional user ID for user-scoped memory."),
					"session_id":  toolStringSchema("Optional session ID for session-scoped memory."),
					"scope":       toolEnumSchema("Memory scope.", MemoryScopeGlobal, MemoryScopeUser, MemoryScopeSession),
					"namespace":   toolStringSchema("Optional memory namespace."),
					"role":        toolStringSchema("Optional message role. Defaults to memory."),
					"content":     toolStringSchema("Memory text content."),
					"metadata":    toolMapSchema("Optional metadata."),
					"importance":  toolNumberSchema("Optional importance score."),
					"ttl_seconds": toolIntegerSchema("Optional TTL in seconds."),
				},
			),
		},
		{
			Name:        "memory_update",
			Description: "Update a stored memory item.",
			InputSchema: toolObjectSchema(
				[]string{"memory_id"},
				map[string]any{
					"memory_id":   toolStringSchema("Stable memory ID."),
					"content":     toolStringSchema("Optional replacement content."),
					"metadata":    toolMapSchema("Optional metadata fields to merge."),
					"importance":  toolNumberSchema("Optional updated importance score."),
					"ttl_seconds": toolIntegerSchema("Optional updated TTL in seconds."),
				},
			),
		},
		{
			Name:        "memory_get",
			Description: "Fetch one memory item by ID.",
			InputSchema: toolObjectSchema(
				[]string{"memory_id"},
				map[string]any{
					"memory_id": toolStringSchema("Stable memory ID."),
				},
			),
		},
		{
			Name:        "memory_search",
			Description: "Search memories in a resolved memory bucket. Expand the goal into keywords, aliases, and alternate phrasings before lexical retrieval.",
			InputSchema: toolObjectSchema(
				[]string{"query"},
				map[string]any{
					"query":             toolStringSchema("User goal or natural-language question."),
					"user_id":           toolStringSchema("Optional user ID for user-scoped memory."),
					"session_id":        toolStringSchema("Optional session ID for session-scoped memory."),
					"scope":             toolEnumSchema("Memory scope.", MemoryScopeGlobal, MemoryScopeUser, MemoryScopeSession),
					"namespace":         toolStringSchema("Optional memory namespace."),
					"top_k":             toolIntegerSchema("Maximum number of memories to return."),
					"keywords":          toolStringArraySchema("LLM-generated keyword bank derived from the goal."),
					"alternate_queries": toolStringArraySchema("Alternate phrasings generated from the same goal."),
					"retrieval_mode":    toolEnumSchema("Preferred retrieval strategy. Auto uses semantic session search when an embedder is available.", RetrievalModeAuto, RetrievalModeLexical),
				},
			),
		},
		{
			Name:        "memory_delete",
			Description: "Delete one memory item by ID.",
			InputSchema: toolObjectSchema(
				[]string{"memory_id"},
				map[string]any{
					"memory_id": toolStringSchema("Stable memory ID."),
				},
			),
		},
	}
}

func toolEntityArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": toolObjectSchema(
			[]string{"name"},
			map[string]any{
				"id":          toolStringSchema("Optional explicit entity node ID."),
				"name":        toolStringSchema("Entity display name."),
				"type":        toolStringSchema("Optional entity type."),
				"description": toolStringSchema("Optional entity description."),
				"chunk_ids":   toolStringArraySchema("Chunk IDs that mention this entity."),
				"metadata":    toolMapSchema("Optional metadata."),
			},
		),
	}
}

func toolRelationArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": toolObjectSchema(
			[]string{"from", "to"},
			map[string]any{
				"from":      toolStringSchema("Source entity name or entity node ID."),
				"to":        toolStringSchema("Target entity name or entity node ID."),
				"type":      toolStringSchema("Optional relation type."),
				"weight":    toolNumberSchema("Optional edge weight."),
				"chunk_ids": toolStringArraySchema("Optional supporting chunk IDs."),
				"metadata":  toolMapSchema("Optional metadata."),
			},
		),
	}
}
