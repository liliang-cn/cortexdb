package cortexdb

import "github.com/liliang-cn/cortexdb/v2/pkg/graph"

func KnowledgeMemoryToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "knowledge_save",
			Mutates:     true,
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
			Mutates:     true,
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
					"chunk_window":           toolIntegerSchema("Widen each retrieved chunk to this many neighbouring chunks either side, in the same document, so an answer split across a chunk boundary arrives whole. 0 (default) is off. Neighbours are context, not matches: they are never scored and never displace a hit."),
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
			Mutates:     true,
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
			Mutates:     true,
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
			Mutates:     true,
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
			Mutates:     true,
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
			Mutates:     true,
			Description: "Import RDF content into the embedded knowledge graph.",
			InputSchema: toolObjectSchema(
				[]string{"content"},
				map[string]any{
					"format":  toolEnumSchema("RDF import format.", KnowledgeGraphFormatNTriples, KnowledgeGraphFormatNQuads, KnowledgeGraphFormatTurtle, KnowledgeGraphFormatTriG, KnowledgeGraphFormatJSONLD),
					"content": toolStringSchema("RDF payload to import. For jsonld, a remote @context URL is never fetched: schema.org's is known, any other must be inlined as an object."),
				},
			),
		},
		{
			Name:        "knowledge_graph_export",
			Description: "Export the embedded knowledge graph as RDF text.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"format": toolEnumSchema("RDF export format.", KnowledgeGraphFormatNTriples, KnowledgeGraphFormatNQuads, KnowledgeGraphFormatTurtle, KnowledgeGraphFormatTriG, KnowledgeGraphFormatJSONLD),
				},
			),
		},
		{
			Name: "knowledge_graph_query",
			// The one entry in this file whose name is actively misleading.
			// CortexDB's SPARQL subset is not query-only: INSERT DATA,
			// DELETE DATA, DELETE WHERE and DELETE/INSERT/WHERE all execute
			// here, as the description below says in full. Reading the name
			// and classifying it as a read would hand every read-only key a
			// way to rewrite the graph.
			Mutates:     true,
			Description: "Execute a SPARQL SELECT/ASK/CONSTRUCT/DESCRIBE subset over the embedded knowledge graph. The property graph that extraction and upsert_entities/upsert_relations write is readable here too, as read-only triples in graph <urn:cortexdb:graph:property>: nodes are cxn:<id>, node types cxt:<type> (via rdf:type / a), relations cxr:<edge_type>, node properties cxp:<key>, and a node's name (or title) is rdfs:label. Ids and type names are percent-encoded exactly (entity:abc → cxn:entity%3Aabc); non-ASCII ids need the full <urn:cortexdb:node:…> form. Example — who depends on CortexDB: SELECT ?who WHERE { ?x cxr:depends_on ?y . ?y rdfs:label \"CortexDB\" . ?x rdfs:label ?who }. Call graph_schema first to learn which types and relations exist. Deleting or inserting projected triples is refused; change the property graph through its own tools.",
			InputSchema: toolObjectSchema(
				[]string{"query"},
				map[string]any{
					"query": toolStringSchema("SPARQL 1.1 query text. Supports PREFIX; SELECT (DISTINCT, REDUCED, (expr AS ?v)), ASK, CONSTRUCT (GRAPH blocks in the template produce quads), DESCRIBE; FROM and FROM NAMED; INSERT DATA, INSERT ... WHERE, DELETE DATA, DELETE WHERE, DELETE ... INSERT ... WHERE, WITH, USING, USING NAMED; GRAPH, OPTIONAL, UNION, MINUS, VALUES, BIND, FILTER, EXISTS, NOT EXISTS, subqueries; property paths ^p, p|q, p+, p*; IN, NOT IN, arithmetic, && || !; functions BOUND, IF, COALESCE, sameTerm, isIRI, isBlank, isLiteral, isNumeric, STR, LANG, LANGMATCHES, DATATYPE, IRI, BNODE, STRDT, STRLANG, UUID, STRUUID, STRLEN, SUBSTR, UCASE, LCASE, STRSTARTS, STRENDS, CONTAINS, STRBEFORE, STRAFTER, ENCODE_FOR_URI, CONCAT, REGEX, REPLACE, ABS, ROUND, CEIL, FLOOR, RAND, NOW, YEAR, MONTH, DAY, HOURS, MINUTES, SECONDS, TIMEZONE, TZ, MD5, SHA1, SHA256, SHA384, SHA512; aggregates COUNT, SUM, AVG, MIN, MAX, SAMPLE, GROUP_CONCAT (all with DISTINCT); GROUP BY, HAVING, ORDER BY ASC/DESC on expressions and aliases, LIMIT, OFFSET. Without FROM the default graph is the unnamed graph plus the property-graph projection. Not supported: XSD casts, / ? ! paths, BASE, SERVICE, LOAD/CLEAR/CREATE/DROP."),
				},
			),
		},
		{
			Name:        "knowledge_graph_shacl_validate",
			Description: "Validate the embedded knowledge graph (including the property graph as cxt:/cxr:/cxp: triples) against supplied SHACL shape triples. Supported: sh:targetClass (with subclasses), sh:targetNode, sh:targetSubjectsOf, sh:targetObjectsOf; sh:property with sh:path (predicate or sh:inversePath); sh:class, sh:datatype, sh:nodeKind; sh:minCount, sh:maxCount; sh:minInclusive, sh:maxInclusive, sh:minExclusive, sh:maxExclusive; sh:minLength, sh:maxLength, sh:pattern with sh:flags, sh:languageIn, sh:uniqueLang; sh:in, sh:hasValue; sh:node, sh:not, sh:and, sh:or, sh:xone; sh:closed with sh:ignoredProperties; sh:severity and sh:message. Recursive shapes and other path forms are refused with an error. Any result makes conforms false, whatever its severity.",
			InputSchema: toolObjectSchema(
				[]string{"shapes"},
				map[string]any{
					"shapes": toolKnowledgeGraphTripleArraySchema(),
				},
			),
		},
		{
			Name: "knowledge_graph_infer_refresh",
			// Recomputes and persists the RDFS-lite inferred triples. The
			// _summary and _explain tools beside it only read what this wrote.
			Mutates:     true,
			Description: "Recompute persisted inferred triples: RDFS (rdfs:subClassOf, rdfs:subPropertyOf, rdfs:domain, rdfs:range) and an OWL 2 RL subset (owl:inverseOf, owl:SymmetricProperty, owl:TransitiveProperty, owl:equivalentClass, owl:equivalentProperty, owl:sameAs, owl:FunctionalProperty and owl:InverseFunctionalProperty as keys that derive owl:sameAs, owl:propertyChainAxiom over an RDF list of two or more properties). Declare axioms with knowledge_graph_query INSERT DATA first — they apply to the property graph too, e.g. cxt:host owl:equivalentClass cxt:Host, cxr:depends_on owl:inverseOf cxr:depended_on_by, cxn:entity%3Anode_e owl:sameAs cxn:entity%3Asds_e to merge an entity stored twice, cxr:deployed_on a owl:FunctionalProperty to merge the duplicate names of one host, or cxr:ultimately_hosted_on owl:propertyChainAxiom (cxr:runs_on cxr:hostedOn). Contradictions are reported in inconsistencies (with inconsistency_count), never resolved: owl:disjointWith (cax-dw), owl:propertyDisjointWith (prp-pdw), two different literal values of a functional property (prp-fp), and owl:differentFrom between terms that sameAs reasoning would merge (eq-diff1; that class's sameAs reasoning is suspended). Each lists the conflicting triples with their ids. Every inferred triple is explainable with knowledge_graph_infer_explain. An owl:sameAs class larger than max_same_as_class_size is reported in oversized_same_as_classes, not materialized. Defaults to a full rebuild; can also run incrementally for affected triples, IDs, or a pattern.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"mode":                   toolEnumSchema("Inference refresh mode.", KnowledgeGraphInferenceRefreshModeFull, KnowledgeGraphInferenceRefreshModeIncremental),
					"triple_ids":             toolStringArraySchema("Optional changed triple IDs for incremental refresh."),
					"triples":                toolKnowledgeGraphTripleArraySchema(),
					"pattern":                toolKnowledgeGraphTriplePatternSchema(),
					"max_same_as_class_size": toolIntegerSchema("Largest owl:sameAs class to materialize; larger classes are reported instead. Default 32."),
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
			Mutates:     true,
			Description: "Store a memory item in a dedicated memory bucket. Use scope=user/session/global to control where the memory lives. Pass entities and relations to record what the memory is about in the same call, so it lands in the knowledge graph too.",
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
					"supersedes":  toolStringArraySchema("Memory IDs this memory replaces. They stay stored and exported, but recall stops returning them as current."),
					"entities":    toolEntityArraySchema(),
					"relations":   toolRelationArraySchema(),
				},
			),
		},
		{
			Name:        "memory_update",
			Mutates:     true,
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
			Description: "List stored memories, newest first, one page at a time. When the response sets truncated it also returns next_cursor — pass it back to get the rest. For dashboards and exports that need the whole set; use memory_search to find specific memories.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"limit":  map[string]any{"type": "integer", "description": "Maximum records in this page (default 500)."},
					"cursor": map[string]any{"type": "string", "description": "Resume point from a previous page's next_cursor. Omit for the first page."},
				},
			),
		},
		{
			Name:        "graph_list_all",
			Description: "List the entity knowledge graph — every non-chunk node and the edges between them. By default returns the most-connected core, which is what makes a large graph renderable; pass order \"id\" with a cursor to walk all of it. Use expand_graph or find_nodes when you already know where to start.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"limit":  map[string]any{"type": "integer", "description": "Maximum nodes in this page (default 2000)."},
					"cursor": map[string]any{"type": "string", "description": "Resume point from a previous page's next_cursor. Supplying it implies order \"id\"."},
					"order":  toolEnumSchema("\"\" (default) returns the most-connected core, best for rendering. \"id\" walks the whole graph in a stable order, resumable with cursor; a page's edges may reference nodes from a later page, so the subgraph is complete only once the walk finishes. Any other value is rejected.", "", "id"),
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
			Description: "Search memories in a resolved memory bucket. Prefer sending a structured retrieval plan. Expand the goal into keywords, aliases, and alternate phrasings before lexical retrieval. Pass entity_names when you know what the question is about: memories saved with entities are reachable through the graph even when their wording shares nothing with the query.",
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
					"entity_names":      toolStringArraySchema("Entities the question is about. Routes the search through the graph, which finds memories whose wording does not match the query."),
					"retrieval_mode":    toolEnumSchema("Preferred retrieval strategy. Auto uses semantic session search when an embedder is available, and the graph when entity_names are given.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"plan":              toolRetrievalPlanSchema("Preferred structured retrieval plan produced by the external LLM before search."),
				},
			),
		},
		{
			Name:        "memory_delete",
			Mutates:     true,
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
