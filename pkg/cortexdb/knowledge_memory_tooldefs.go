package cortexdb

import "github.com/liliang-cn/cortexdb/v2/pkg/graph"

func KnowledgeMemoryToolDefinitions() []ToolDefinition {
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
			Description: "Search durable knowledge. Prefer sending a structured retrieval plan. When no embedder is available, first expand the user's goal into keywords, aliases, synonyms, abbreviations, and multilingual variants.",
			InputSchema: toolObjectSchema(
				[]string{"query"},
				map[string]any{
					"query":                  toolStringSchema("User goal or natural-language question."),
					"collection":             toolStringSchema("Optional chunk collection."),
					"top_k":                  toolIntegerSchema("Seed chunk count."),
					"max_hops":               toolIntegerSchema("Graph expansion depth."),
					"max_related_chunks":     toolIntegerSchema("Maximum graph-expanded chunks."),
					"max_context_chunks":     toolIntegerSchema("Maximum chunks in final context."),
					"max_context_chars":      toolIntegerSchema("Maximum context character budget."),
					"per_document_limit":     toolIntegerSchema("Maximum chunks per document."),
					"diversity_lambda":       toolNumberSchema("Rerank diversity weight between 0 and 1."),
					"entity_names":           toolStringArraySchema("Optional entities from structured planning."),
					"keywords":               toolStringArraySchema("LLM-generated keyword bank derived from the goal."),
					"alternate_queries":      toolStringArraySchema("Alternate phrasings generated from the same goal."),
					"retrieval_mode":         toolEnumSchema("Preferred retrieval strategy.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":          toolBooleanSchema("Legacy alias. Set true to force lexical-only retrieval."),
					"graph_light":            toolBooleanSchema("Enable lighter graph traversal defaults for lower latency."),
					"max_expansion_seeds":    toolIntegerSchema("Optional cap on how many seed chunks will be expanded through the graph."),
					"max_traversal_nodes":    toolIntegerSchema("Optional cap on how many graph nodes will be inspected during expansion."),
					"max_entities_per_chunk": toolIntegerSchema("Optional cap on graph-derived entities attached to each chunk."),
					"plan":                   toolRetrievalPlanSchema("Preferred structured retrieval plan produced by the external LLM before search."),
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
			Name:        "knowledge_graph_namespace_upsert",
			Description: "Register or update a namespace prefix used by the RDF knowledge graph.",
			InputSchema: toolObjectSchema(
				[]string{"prefix", "uri"},
				map[string]any{
					"prefix": toolStringSchema("Namespace prefix such as schema or ex."),
					"uri":    toolStringSchema("Namespace base URI."),
				},
			),
		},
		{
			Name:        "knowledge_graph_namespace_list",
			Description: "List visible RDF namespaces, including built-ins and user-defined prefixes.",
			InputSchema: toolObjectSchema(nil, map[string]any{}),
		},
		{
			Name:        "knowledge_graph_upsert",
			Description: "Insert or update RDF triples/quads in the embedded knowledge graph.",
			InputSchema: toolObjectSchema(
				[]string{"triples"},
				map[string]any{
					"triples": toolKnowledgeGraphTripleArraySchema(),
				},
			),
		},
		{
			Name:        "knowledge_graph_find",
			Description: "Find RDF triples/quads by subject, predicate, object, and/or graph pattern.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"pattern": toolKnowledgeGraphTriplePatternSchema(),
				},
			),
		},
		{
			Name:        "knowledge_graph_delete",
			Description: "Delete RDF triples/quads by IDs, explicit triples, or a pattern.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"triple_ids": toolStringArraySchema("Optional triple IDs to delete."),
					"triples":    toolKnowledgeGraphTripleArraySchema(),
					"pattern":    toolKnowledgeGraphTriplePatternSchema(),
				},
			),
		},
		{
			Name:        "knowledge_graph_import",
			Description: "Import RDF content into the embedded knowledge graph.",
			InputSchema: toolObjectSchema(
				[]string{"content"},
				map[string]any{
					"format":  toolEnumSchema("RDF import format.", KnowledgeGraphFormatNTriples, KnowledgeGraphFormatNQuads, KnowledgeGraphFormatTurtle, KnowledgeGraphFormatTriG),
					"content": toolStringSchema("RDF payload to import."),
				},
			),
		},
		{
			Name:        "knowledge_graph_export",
			Description: "Export the embedded knowledge graph as RDF text.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"format": toolEnumSchema("RDF export format.", KnowledgeGraphFormatNTriples, KnowledgeGraphFormatNQuads, KnowledgeGraphFormatTurtle, KnowledgeGraphFormatTriG),
				},
			),
		},
		{
			Name:        "knowledge_graph_query",
			Description: "Execute a SPARQL SELECT/ASK/CONSTRUCT/DESCRIBE subset over the embedded knowledge graph.",
			InputSchema: toolObjectSchema(
				[]string{"query"},
				map[string]any{
					"query": toolStringSchema("SPARQL query text. Supports PREFIX, SELECT, ASK, CONSTRUCT, DESCRIBE, INSERT DATA, INSERT ... WHERE, DELETE DATA, DELETE WHERE, DELETE ... INSERT ... WHERE, WITH, USING, GRAPH, OPTIONAL, UNION, MINUS, VALUES, BIND, FILTER, EXISTS, NOT EXISTS, REGEX, LANG, DATATYPE, COALESCE, IF, arithmetic, GROUP BY, HAVING, COUNT, SUM, AVG, MIN, MAX, SAMPLE, GROUP_CONCAT, ORDER BY, LIMIT, and OFFSET."),
				},
			),
		},
		{
			Name:        "knowledge_graph_shacl_validate",
			Description: "Validate the embedded knowledge graph with supplied SHACL-lite shape triples.",
			InputSchema: toolObjectSchema(
				[]string{"shapes"},
				map[string]any{
					"shapes": toolKnowledgeGraphTripleArraySchema(),
				},
			),
		},
		{
			Name:        "knowledge_graph_infer_refresh",
			Description: "Recompute persisted RDFS-lite inferred triples. Defaults to a full rebuild; can also run incrementally for affected triples, IDs, or a pattern.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"mode":       toolEnumSchema("Inference refresh mode.", KnowledgeGraphInferenceRefreshModeFull, KnowledgeGraphInferenceRefreshModeIncremental),
					"triple_ids": toolStringArraySchema("Optional changed triple IDs for incremental refresh."),
					"triples":    toolKnowledgeGraphTripleArraySchema(),
					"pattern":    toolKnowledgeGraphTriplePatternSchema(),
				},
			),
		},
		{
			Name:        "knowledge_graph_infer_summary",
			Description: "Return explicit/inferred triple counts and a breakdown by inference rule.",
			InputSchema: toolObjectSchema(nil, map[string]any{}),
		},
		{
			Name:        "knowledge_graph_infer_explain",
			Description: "Explain why a triple exists by returning whether it is explicit or inferred, plus its immediate support chain.",
			InputSchema: toolObjectSchema(
				[]string{"triple_id"},
				map[string]any{
					"triple_id": toolStringSchema("Stable triple ID to explain."),
					"depth":     toolIntegerSchema("Optional recursive explanation depth."),
				},
			),
		},
		{
			Name:        "knowledge_graph_infer_explain_match",
			Description: "Explain every triple matched by a pattern, optionally including recursive support traces.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"pattern": toolKnowledgeGraphTriplePatternSchema(),
					"depth":   toolIntegerSchema("Optional recursive explanation depth."),
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
			Name:        "memory_list_all",
			Description: "List every stored memory. For dashboards and exports that need the whole set rather than a search result; use memory_search to find specific memories.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"limit": map[string]any{"type": "integer", "description": "Maximum records to return (default 5000). The response says whether it was truncated."},
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
			Description: "Search memories in a resolved memory bucket. Prefer sending a structured retrieval plan. Expand the goal into keywords, aliases, and alternate phrasings before lexical retrieval.",
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
					"retrieval_mode":    toolEnumSchema("Preferred retrieval strategy. Auto uses semantic session search when an embedder is available.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"plan":              toolRetrievalPlanSchema("Preferred structured retrieval plan produced by the external LLM before search."),
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
				"from":             toolStringSchema("Source entity name or entity node ID."),
				"to":               toolStringSchema("Target entity name or entity node ID."),
				"type":             toolStringSchema("Optional relation type."),
				"weight":           toolNumberSchema("Optional edge weight."),
				"chunk_ids":        toolStringArraySchema("Optional supporting chunk IDs."),
				"metadata":         toolMapSchema("Optional metadata."),
				"inferred":         toolBooleanSchema("Set true when this relation is inferred rather than explicit."),
				"provenance":       toolStringSchema("Optional provenance source such as rule or llm."),
				"rule_id":          toolStringSchema("Optional rule or inference identifier."),
				"support_edge_ids": toolStringArraySchema("Optional supporting relation edge IDs for provenance."),
			},
		),
	}
}

func toolKnowledgeGraphTermSchema() map[string]any {
	return toolObjectSchema(
		[]string{"kind", "value"},
		map[string]any{
			"kind":     toolEnumSchema("RDF term kind.", graph.RDFTermIRI, graph.RDFTermBlankNode, graph.RDFTermLiteral),
			"value":    toolStringSchema("RDF term value."),
			"datatype": toolStringSchema("Optional literal datatype IRI."),
			"language": toolStringSchema("Optional literal language tag."),
		},
	)
}

func toolKnowledgeGraphTripleSchema() map[string]any {
	return toolObjectSchema(
		[]string{"subject", "predicate", "object"},
		map[string]any{
			"id":        toolStringSchema("Optional stable triple ID."),
			"subject":   toolKnowledgeGraphTermSchema(),
			"predicate": toolKnowledgeGraphTermSchema(),
			"object":    toolKnowledgeGraphTermSchema(),
			"graph":     toolKnowledgeGraphTermSchema(),
		},
	)
}

func toolKnowledgeGraphTripleArraySchema() map[string]any {
	return map[string]any{
		"type":  "array",
		"items": toolKnowledgeGraphTripleSchema(),
	}
}

func toolKnowledgeGraphTriplePatternSchema() map[string]any {
	return toolObjectSchema(
		nil,
		map[string]any{
			"subject":   toolKnowledgeGraphTermSchema(),
			"predicate": toolKnowledgeGraphTermSchema(),
			"object":    toolKnowledgeGraphTermSchema(),
			"graph":     toolKnowledgeGraphTermSchema(),
			"limit":     toolIntegerSchema("Optional result limit."),
		},
	)
}
