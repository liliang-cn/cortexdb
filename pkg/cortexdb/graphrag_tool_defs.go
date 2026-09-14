package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolDefinitions returns the same catalogue as GraphRAGToolbox.Definitions
// without needing an open database.
//
// The catalogue is static — names, descriptions, schemas and Mutates are
// literals, and none of them is read off the store. Authorization needs it that
// way: pkg/authz decides whether a key may run a named tool before any handler
// runs, and a policy that could only answer once a database was open would be a
// policy that fails open on the path where there is none.
func ToolDefinitions() []ToolDefinition {
	return (&GraphRAGToolbox{}).Definitions()
}

// Definitions returns the JSON-schema-like definitions for the available tools.
func (t *GraphRAGToolbox) Definitions() []ToolDefinition {
	definitions := []ToolDefinition{
		{
			Name:        "ingest_document",
			Mutates:     true,
			Description: "Store a document, split it into chunks, index it lexically, and create document/chunk graph nodes.",
			InputSchema: toolObjectSchema(
				[]string{"document_id", "content"},
				map[string]any{
					"document_id":   toolStringSchema("Stable document ID."),
					"title":         toolStringSchema("Optional human-readable title."),
					"content":       toolStringSchema("Raw document content."),
					"collection":    toolStringSchema("Optional chunk collection name."),
					"chunk_size":    toolIntegerSchema("Optional chunk size in words."),
					"chunk_overlap": toolIntegerSchema("Optional chunk overlap in words."),
					"metadata":      toolMapSchema("Optional document metadata."),
				},
			),
		},
		{
			Name:        "upsert_entities",
			Mutates:     true,
			Description: "Create entity nodes and connect chunks to those entities with mention edges.",
			InputSchema: toolObjectSchema(
				[]string{"entities"},
				map[string]any{
					"document_id": toolStringSchema("Optional source document ID."),
					"entities": map[string]any{
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
					},
				},
			),
		},
		{
			Name: "delete_entities",
			// dry_run makes this a read for one particular argument, which is
			// exactly the thing authorization cannot see: it decides from the
			// tool name, before the JSON is parsed. A tool that can write is a
			// write.
			Mutates:     true,
			Description: "Delete entity nodes and every edge touching them. The counterpart to upsert_entities, for removing wrong or junk entities; use dry_run to see what would go first. Not reversible.",
			InputSchema: toolObjectSchema(
				[]string{"names"},
				map[string]any{
					"names":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Entity names or full node ids to delete."},
					"dry_run": map[string]any{"type": "boolean", "description": "Report what would be deleted without deleting it."},
				},
			),
		},
		{
			Name:        "delete_document_graph",
			Mutates:     true,
			Description: "Delete everything one document put in the graph: its chunk and document nodes, its relation edges, and the entities it alone asserted (entities other documents also assert are detached, not deleted). The counterpart to a document-scoped ingest; use dry_run to see what would go first. Not reversible.",
			InputSchema: toolObjectSchema(
				[]string{"document_id"},
				map[string]any{
					"document_id": toolStringSchema("Document ID whose graph footprint should be removed."),
					"dry_run":     map[string]any{"type": "boolean", "description": "Report what would be deleted without deleting it."},
				},
			),
		},
		{
			Name:        "upsert_relations",
			Mutates:     true,
			Description: "Create relation edges between entity nodes.",
			InputSchema: toolObjectSchema(
				[]string{"relations"},
				map[string]any{
					"document_id": toolStringSchema("Optional source document ID."),
					"relations": map[string]any{
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
					},
				},
			),
		},
		{
			Name:        "search_text",
			Description: "Run lexical BM25/FTS5 retrieval over stored chunks. Prefer sending a structured plan, and before calling, expand the user goal into many keywords, aliases, synonyms, and multilingual variants.",
			InputSchema: toolObjectSchema(
				[]string{"query"},
				map[string]any{
					"query":                  toolStringSchema("User goal or natural-language question."),
					"collection":             toolStringSchema("Optional collection name."),
					"top_k":                  toolIntegerSchema("Maximum number of chunks to return."),
					"threshold":              toolNumberSchema("Optional minimum normalized score."),
					"keywords":               toolStringArraySchema("LLM-generated keyword bank derived from the goal. Include aliases, synonyms, abbreviations, translations, and domain terms."),
					"alternate_queries":      toolStringArraySchema("Alternate phrasings generated by the LLM planner from the same goal."),
					"retrieval_mode":         toolEnumSchema("Preferred retrieval strategy.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":          toolBooleanSchema("Legacy alias. Set true to avoid graph-based entity enrichment and force lexical-only retrieval."),
					"graph_light":            toolBooleanSchema("Enable lighter graph enrichment defaults for lower latency."),
					"max_entities_per_chunk": toolIntegerSchema("Optional cap on graph-derived entities attached to each chunk."),
					"plan":                   toolRetrievalPlanSchema("Preferred structured retrieval plan produced by the external LLM before search."),
				},
			),
		},
		{
			Name:        "cortex_query",
			Description: "Run CortexDB's composable Query API: dense vector, lexical, or hybrid prefetches fused with RRF, weighted RRF, or DBSF, then optionally filtered and formula-reranked.",
			InputSchema: toolQueryRequestSchema(),
		},
		{
			Name:        "search_vector_range",
			Description: "Return every stored row whose embedding is within a distance radius of the query — everything above a bar, and nothing below it. Use this instead of search_text or cortex_query whenever a top-K would lie: finding all near-duplicates of a passage, asking whether the store holds anything like this at all, or any decision made against a similarity threshold. Those tools always hand back their K best rows however weak the last ones are; this one returns only what clears the bar, returns nothing when nothing does, and says when a cap hid matches that did. radius is a distance, not a similarity: with the default cosine metric it is 1 minus the similarity, so roughly 0.1 for near-identical text, 0.3 for closely related, 0.5 and up for loose. Requires an embedder.",
			InputSchema: toolObjectSchema(
				[]string{"query", "radius"},
				map[string]any{
					"query":       toolStringSchema("Text whose neighbourhood should be returned."),
					"radius":      toolNumberSchema("Distance bar. Must be positive. With cosine, 1 minus the similarity you would accept."),
					"collection":  toolStringSchema("Optional collection name."),
					"filter":      toolMapSchema("Optional metadata equality filter."),
					"max_results": toolIntegerSchema("Maximum matches to return, closest first. Defaults to 100; the response reports whether the radius matched more than that."),
				},
			),
		},
		{
			Name:        "aggregate_metadata",
			Description: "Count, sum, average or group the stored records by a metadata field — a question about the collection as a whole, not about one query's neighbourhood. Use it for \"how many records of each kind are there\", \"what values does this field take\", \"what is the average score across this source\": questions a search cannot answer, because a search returns the top few and this returns the totals. type is one of count, sum, avg, min, max, group_by; sum/avg/min/max need a field, group_by needs group_by. A group_by is also how to ask what values a field takes and how often, which is what facet counts are for. Field names must be plain identifiers.",
			InputSchema: toolObjectSchema(
				[]string{"type"},
				map[string]any{
					"type":       toolEnumSchema("Which aggregation to run.", "count", "sum", "avg", "min", "max", "group_by"),
					"field":      toolStringSchema("Metadata field to aggregate. Required for sum, avg, min and max."),
					"group_by":   toolStringArraySchema("Metadata fields to group by. Required for group_by."),
					"filters":    toolMapSchema("Optional metadata equality filter, applied before aggregating."),
					"collection": toolStringSchema("Optional collection name."),
					"order_by":   toolStringSchema("Optional ordering: count, agg_value, or a group-by field name."),
					"limit":      toolIntegerSchema("Optional maximum number of groups."),
				},
			),
		},
		{
			Name:        "representative_records",
			Description: "Return the one stored record that best represents each group — its medoid, the member closest to all the others. Use it to answer \"what is this cluster actually about\" or \"which of these near-duplicates is the canonical one\" without reading every member and without asking a model to summarise them: the answer is a real record, so it can be quoted and traced. Group with group_by on a metadata field, or omit it to get one representative for everything matched. score is how close that record sits to the rest, so a low score means the group has no real centre and the answer is representative of nothing.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"group_by":      toolStringSchema("Metadata field to group by. Omit for a single group over everything matched."),
					"filter":        toolMapSchema("Optional metadata equality filter."),
					"collection":    toolStringSchema("Optional collection name."),
					"max_per_group": toolIntegerSchema("Optional cap on members considered per group, taken in id order."),
				},
			),
		},
		{
			Name:        "graph_schema",
			Description: "Report the knowledge graph's observed schema — which node types and edge types actually exist, how many of each, which node-type pairs every edge type really connects (Person -[WORKS_AT]-> Company (412)), and which property keys each node type carries. Call this before writing any traversal, SPARQL query or entity filter against a graph you have not already inspected in this conversation: a query naming a type or key the graph does not have still runs, matches nothing, and returns a confident wrong negative. This is measured from the stored rows, so it works on graphs built by extraction that have no declared ontology — ontology_get returns a declared schema, which is opt-in and usually absent on exactly the graphs whose shape nobody knows. It does not list property values: once this says a key exists, call graph_property_values before filtering on one, because stored values are often codes. Read the text field; it is the same answer written to be read.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"node_types":        toolStringArraySchema("Only describe these node types, matched exactly as stored. Omit for all."),
					"edge_types":        toolStringArraySchema("Only describe these edge types, matched exactly as stored. Omit for all."),
					"max_node_types":    toolIntegerSchema("Cap the node types listed, most numerous first. Default 40."),
					"max_edge_types":    toolIntegerSchema("Cap the edge types listed, most numerous first. Default 40."),
					"max_edge_shapes":   toolIntegerSchema("Cap the endpoint-type pairs listed per edge type. Default 6."),
					"max_property_keys": toolIntegerSchema("Cap the property keys listed per node type. Default 12."),
				},
			),
		},
		{
			Name:        "graph_property_values",
			Description: "List the values one property key actually takes in the knowledge graph, most frequent first, with how many nodes and edges carry each. Call this before writing any filter, WHERE clause or SPARQL literal that compares a property to a value. Stored values are routinely codes, abbreviations, or a spelling nobody would guess from the key's name — asked for color == \"black\", a graph whose rows all say BLK answers \"none\", and that reads like a fact. This is the only way to see the real strings. Get the key name from graph_schema first. The response also reports how many records do not carry the key at all, which is the context a distribution is meaningless without. Match the returned strings exactly; do not substitute the word you expected.",
			InputSchema: toolObjectSchema(
				[]string{"key"},
				map[string]any{
					"key":   toolStringSchema("The top-level property key to break down, for example \"color\"."),
					"limit": toolIntegerSchema("Cap the values listed, most frequent first. Default 50; the response says when the list was trimmed."),
				},
			),
		},
		{
			Name:        "rank_graph_nodes",
			Description: "Rank the knowledge graph's nodes by PageRank: structural importance, not keyword frequency. Answers \"what is this knowledge base actually about\" and \"which entities matter here\" by finding the nodes the graph's connections converge on. Each result carries the node's id, label and type beside its score, so the ranking is readable without a second lookup, and it is the honest way to choose what to show first when the graph is too large to show whole. Returns the top 20 by default (maximum 200), highest first, ties broken by id so repeated calls agree; total_nodes says how many were scored and truncated says whether the list was cut. An empty graph returns an empty ranking, not an error. Every call scores every node, so call it once and read the answer rather than in a loop.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"top_n":          toolIntegerSchema("How many of the highest-ranked nodes to return. Default 20, maximum 200."),
					"iterations":     toolIntegerSchema("Ceiling on the power-iteration count. Default 100. The run stops early once scores settle, so raising this rarely changes the answer."),
					"damping_factor": toolNumberSchema("Probability the random walk follows an edge rather than restarting, between 0 and 1. Default 0.85. Lower weights local structure more heavily."),
				},
			),
		},
		{
			Name:        "predict_graph_edges",
			Description: "Link prediction: given one node, list the nodes it is NOT connected to but arguably should be, best first. Two different findings come back in the same shape and only the labels tell them apart — a genuinely missing fact worth checking against the sources, or the same entity stored twice under different names, which is a duplicate to merge. Both endpoints are returned named and typed, with the score and the method behind it (\"vector_similarity\" when the two merely look alike, \"combined\" when they also share neighbours; shared neighbours can carry a pair on their own). Requires an existing node id, so use find_nodes first if you only have a name: an unknown id is an error, while a node with no plausible partners is simply an empty list. On a graph whose nodes carry only lexical vectors most of what surfaces will be near-duplicates. It compares the node against every other node, so call it one node at a time and deliberately.",
			InputSchema: toolObjectSchema(
				[]string{"node_id"},
				map[string]any{
					"node_id":     toolStringSchema("Id of the node to predict connections for, exactly as returned by find_nodes or get_nodes."),
					"max_results": toolIntegerSchema("How many predictions to return. Default 10, maximum 100."),
				},
			),
		},
		{
			Name:        "graph_statistics",
			Description: "Report the size and shape of the whole knowledge graph: node count, edge count, average degree, density, and the number of connected components. Use it before a large graph operation to know what you are dealing with, to confirm an ingest actually landed, or to diagnose a graph that answers badly — a component count far above 1 usually means entities were written without ever being linked to anything. Takes no arguments and reads the whole graph. An empty graph reports zeros, not an error.",
			InputSchema: toolObjectSchema(nil, map[string]any{}),
		},
		{
			Name:        "disambiguate_mentions",
			Description: "Decide which graph entity each ambiguous name refers to, using the other names given alongside it. Returns each mention's candidates ranked by how well the graph connects them to the other mentions' candidates, every connecting path written out as a sentence, and the edge ids behind it — so you can take the top candidate or read the evidence and choose, and either way say afterwards why. Pass every mention from the same sentence or passage in one call: co-occurrence is the entire signal, and a mention sent on its own has nothing to disambiguate against. A mention whose candidates connect to nothing comes back marked unresolved rather than falling back to the closest string match; before reading that as \"not connected\", check search_incomplete and connected_beyond_max_length, which mean a bound stopped the search rather than the graph having no answer. Pass node_types on any brain that keeps documents and chunks as graph nodes: a long document title containing two of the mentions matches both by substring and then scores a perfect 1.0 for each, which ties with the real entity and resolves nothing. Naming the entity-like types — entity, project, protocol, tool, software, person — removes that and is also the cheapest bound there is.",
			InputSchema: toolObjectSchema(
				[]string{"mentions"},
				map[string]any{
					"mentions":                   toolStringArraySchema("The names to resolve, all from the same sentence or passage. Each one is also the context for the others."),
					"node_types":                 toolStringArraySchema("Optional candidate type filter. The cheapest bound there is: it cuts candidates before they multiply into pairs."),
					"max_candidates_per_mention": toolIntegerSchema("Cap on nodes each mention may resolve to. Default 5."),
					"max_path_length":            toolIntegerSchema("Longest path, in edges, that still counts as support. Default 3. A connection found beyond it is reported, not silently dropped."),
					"max_path_searches":          toolIntegerSchema("Cap on candidate pairs examined. Default 200. This is the bound that controls runtime."),
				},
			),
		},
		{
			Name:        "search_chunks_by_entities",
			Description: "Find chunks linked to specific entity nodes.",
			InputSchema: toolObjectSchema(
				[]string{"entity_names"},
				map[string]any{
					"entity_names": toolStringArraySchema("Entity names or node IDs."),
					"top_k":        toolIntegerSchema("Maximum number of chunks to return."),
					"max_hops":     toolIntegerSchema("Traversal depth from entities."),
				},
			),
		},
		{
			Name:        "expand_graph",
			Description: "Expand a graph neighborhood and return a subgraph.",
			InputSchema: toolObjectSchema(
				[]string{"node_ids"},
				map[string]any{
					"node_ids":   toolStringArraySchema("Starting node IDs."),
					"max_hops":   toolIntegerSchema("Traversal depth."),
					"edge_types": toolStringArraySchema("Optional edge type filter."),
					"node_types": toolStringArraySchema("Optional node type filter."),
					"limit":      toolIntegerSchema("Optional node result limit."),
				},
			),
		},
		{
			Name:        "get_nodes",
			Description: "Fetch graph nodes by ID.",
			InputSchema: toolObjectSchema(
				[]string{"node_ids"},
				map[string]any{
					"node_ids": toolStringArraySchema("Node IDs to load."),
				},
			),
		},
		{
			Name:        "find_nodes",
			Description: "Resolve names to graph node IDs, so a caller holding a name can enter the graph. Matches exactly, then ignoring case and punctuation, then by containment; each match says which. Use before expand_graph when you have a name rather than an ID.",
			InputSchema: toolObjectSchema(
				[]string{"names"},
				map[string]any{
					"names":      toolStringArraySchema("Names to look up. Batch them: one scan serves all."),
					"node_types": toolStringArraySchema("Optional node type filter, e.g. Concept."),
					"limit":      toolIntegerSchema("Optional cap on nodes returned per name."),
				},
			),
		},
		{
			Name:        "get_chunks",
			Description: "Fetch chunk records by chunk ID.",
			InputSchema: toolObjectSchema(
				[]string{"chunk_ids"},
				map[string]any{
					"chunk_ids":              toolStringArraySchema("Chunk IDs to load."),
					"retrieval_mode":         toolEnumSchema("Preferred retrieval strategy for entity enrichment.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":          toolBooleanSchema("Legacy alias. Set true to skip graph-derived entity lookups while loading chunks."),
					"graph_light":            toolBooleanSchema("Enable lighter graph enrichment defaults while loading chunks."),
					"max_entities_per_chunk": toolIntegerSchema("Optional cap on graph-derived entities attached to each chunk."),
				},
			),
		},
		{
			Name:        "fact_provenance",
			Description: "Answer \"says who?\" for one relationship edge: the document and chunks behind it, whether it was inferred and by which rule, and optionally the supporting text itself. A chunk that no longer exists is reported as missing rather than dropped, because a citation pointing at deleted text is a finding.",
			InputSchema: toolObjectSchema(
				[]string{"edge_id"},
				map[string]any{
					"edge_id":   toolStringSchema("The graph edge to trace back to its source."),
					"with_text": toolBooleanSchema("Load the supporting chunk text as well. Costs a second query and the chunk bodies; leave it off to ask only whether the fact is cited at all."),
				},
			),
		},
		{
			Name:        "uncited_facts",
			Description: "List relationship edges that cannot say where they came from — no supporting chunks, no document, and no rule for a derived fact. This reports what is missing, not whether a citation is any good: checking that the text still says what the fact says needs fact_provenance with_text.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"limit": toolIntegerSchema("Maximum edges to return. Defaults to 100."),
				},
			),
		},
		{
			Name:        "build_context",
			Description: "Pack chunk text into a bounded context window.",
			InputSchema: toolObjectSchema(
				[]string{"chunk_ids"},
				map[string]any{
					"chunk_ids":              toolStringArraySchema("Ordered chunk IDs."),
					"max_context_chunks":     toolIntegerSchema("Maximum number of chunks to include."),
					"max_context_chars":      toolIntegerSchema("Maximum total character budget."),
					"per_document_limit":     toolIntegerSchema("Maximum chunks per document."),
					"chunk_window":           toolIntegerSchema("Widen each retrieved chunk to this many neighbouring chunks either side, in the same document, so an answer split across a chunk boundary arrives whole. 0 (default) is off. Neighbours are context, not matches: they are never scored and never displace a hit."),
					"retrieval_mode":         toolEnumSchema("Preferred retrieval strategy for entity enrichment.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":          toolBooleanSchema("Legacy alias. Set true to skip graph-derived entity lookups while packing context."),
					"graph_light":            toolBooleanSchema("Enable lighter graph enrichment defaults while packing context."),
					"max_entities_per_chunk": toolIntegerSchema("Optional cap on graph-derived entities attached to each chunk."),
				},
			),
		},
		{
			Name: "extract_conversation",
			// Extraction itself is pure, but persist=true writes entities and
			// relations into the graph and the summary into knowledge. Same
			// rule as delete_entities: the argument that decides is invisible
			// to the caller doing the authorizing.
			Mutates:     true,
			Description: "Extract key information from conversation text (or a stored session's messages): a summary, themes, entities, and co-occurrence relations. Deterministic (no LLM). Set persist=true to also write entities/relations into the graph and the summary into knowledge, making the conversation recallable and graph-queryable.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"text":         toolStringSchema("Conversation text to analyze."),
					"session_id":   toolStringSchema("Load this session's messages when text is empty."),
					"persist":      toolBooleanSchema("Also write entities/relations to the graph and the summary to knowledge."),
					"collection":   toolStringSchema("Collection for the persisted summary (default \"conversations\")."),
					"max_entities": toolIntegerSchema("Cap on extracted entities (default 30)."),
				},
			),
		},
		{
			Name:        "search_graphrag_lexical",
			Description: "Perform lexical GraphRAG retrieval using FTS5 seeds, graph expansion, rerank, and context packing. Prefer sending a structured plan, and first expand the user goal into many keywords, aliases, synonyms, and multilingual variants.",
			InputSchema: toolObjectSchema(
				[]string{"query"},
				map[string]any{
					"query":                  toolStringSchema("User goal or natural-language question."),
					"collection":             toolStringSchema("Optional chunk collection name."),
					"top_k":                  toolIntegerSchema("Seed chunk count."),
					"max_hops":               toolIntegerSchema("Graph expansion depth."),
					"max_related_chunks":     toolIntegerSchema("Maximum graph-expanded chunks."),
					"max_context_chunks":     toolIntegerSchema("Maximum chunks in final context."),
					"max_context_chars":      toolIntegerSchema("Maximum context character budget."),
					"per_document_limit":     toolIntegerSchema("Maximum chunks per document."),
					"chunk_window":           toolIntegerSchema("Widen each retrieved chunk to this many neighbouring chunks either side, in the same document, so an answer split across a chunk boundary arrives whole. 0 (default) is off. Neighbours are context, not matches: they are never scored and never displace a hit."),
					"disable_rerank":         toolBooleanSchema("Disable reranking and keep chunk order close to retrieval order."),
					"diversity_lambda":       toolNumberSchema("Rerank diversity weight between 0 and 1."),
					"entity_names":           toolStringArraySchema("Optional entities from structured LLM planning."),
					"keywords":               toolStringArraySchema("LLM-generated keyword bank derived from the goal. Include aliases, synonyms, abbreviations, translations, and domain terms."),
					"alternate_queries":      toolStringArraySchema("Alternate phrasings generated by the LLM planner from the same goal."),
					"retrieval_mode":         toolEnumSchema("Preferred retrieval strategy. Use lexical for speed, graph for full expansion, or auto for heuristic selection.", RetrievalModeAuto, RetrievalModeLexical, RetrievalModeGraph),
					"disable_graph":          toolBooleanSchema("Legacy alias. Set true to disable graph traversal and force lexical-only retrieval."),
					"graph_light":            toolBooleanSchema("Enable lighter graph traversal defaults for lower latency."),
					"max_expansion_seeds":    toolIntegerSchema("Optional cap on how many seed chunks will be expanded through the graph."),
					"max_traversal_nodes":    toolIntegerSchema("Optional cap on how many graph nodes will be inspected during expansion."),
					"max_entities_per_chunk": toolIntegerSchema("Optional cap on graph-derived entities attached to each chunk."),
					"plan":                   toolRetrievalPlanSchema("Preferred structured retrieval plan produced by the external LLM before search."),
				},
			),
		},
		{
			Name: "vector_dimension_repair",
			// Reads like a report, and with dry_run (its default) that is all
			// it is. Without it, it re-embeds rows and rewrites their vectors.
			Mutates:     true,
			Description: "Report vector-dimension drift and optionally re-embed rows whose vectors came from an older embedding model. Such rows cannot enter the vector index, so they silently stop being retrievable by similarity while lexical search still finds them. Run with dry_run first.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"dry_run":    toolBooleanSchema("Report what would change without writing. Defaults to true."),
					"limit":      toolIntegerSchema("Maximum rows to re-embed (0 = all)."),
					"batch_size": toolIntegerSchema("Texts per embedding request (default 16)."),
				},
			),
		},
	}
	definitions = append(definitions, inferenceToolDefinitions()...)
	// --- declared inference rules (pkg/cortexdb/rules_*.go) ---
	definitions = append(definitions, ruleToolDefinitions()...)
	// --- end declared inference rules ---
	definitions = append(definitions, ontologyToolDefinitions()...)
	definitions = append(definitions, KnowledgeMemoryToolDefinitions()...)
	definitions = append(definitions, contractToolDefinitions()...)
	// --- decision ledger (pkg/cortexdb/decision_tooldefs.go) ---
	definitions = append(definitions, decisionToolDefinitions()...)
	// --- end decision ledger ---
	// --- point-in-time reads (pkg/cortexdb/temporal_tooldefs.go) ---
	definitions = append(definitions, temporalToolDefinitions()...)
	// --- end point-in-time reads ---
	return append(definitions, KnowledgeMemoryFacadeToolDefinitions()...)
}

// Call dispatches a tool request from JSON input to a typed implementation.
func (t *GraphRAGToolbox) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	if resp, handled, err := t.callInferenceTool(ctx, name, input); handled {
		return resp, err
	}
	// --- decision ledger (pkg/cortexdb/decision_tooldefs.go) ---
	if resp, handled, err := t.callDecisionTool(ctx, name, input); handled {
		return resp, err
	}
	// --- end decision ledger ---
	// --- point-in-time reads (pkg/cortexdb/temporal_tooldefs.go) ---
	if resp, handled, err := t.callTemporalTool(ctx, name, input); handled {
		return resp, err
	}
	// --- end point-in-time reads ---
	// --- declared rules (pkg/cortexdb/rules_tooldefs.go) ---
	if resp, handled, err := t.callRuleTool(ctx, name, input); handled {
		return resp, err
	}
	// --- end declared rules ---
	if resp, handled, err := t.callOntologyTool(ctx, name, input); handled {
		return resp, err
	}
	switch name {
	case "ingest_document":
		var req ToolIngestDocumentRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.IngestDocument(ctx, req)
	case "upsert_entities":
		var req ToolUpsertEntitiesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpsertEntities(ctx, req)
	case "delete_entities":
		var req ToolDeleteEntitiesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.DeleteEntities(ctx, req)
	case "delete_document_graph":
		var req ToolDeleteDocumentGraphRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.DeleteDocumentGraph(ctx, req)
	case "upsert_relations":
		var req ToolUpsertRelationsRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpsertRelations(ctx, req)
	case "search_text":
		var req ToolSearchTextRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchText(ctx, req)
	case "cortex_query":
		var req QueryRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.Query(ctx, req)
	case "graph_schema":
		var req GraphSchemaRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GraphSchema(ctx, req)
	case "graph_property_values":
		var req GraphPropertyValuesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GraphPropertyValues(ctx, req)
	case "rank_graph_nodes":
		var req ToolRankGraphNodesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.RankGraphNodes(ctx, req)
	case "predict_graph_edges":
		var req ToolPredictGraphEdgesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.PredictGraphEdges(ctx, req)
	case "graph_statistics":
		var req ToolGraphStatisticsRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GraphStatistics(ctx, req)
	case "disambiguate_mentions":
		var req ToolDisambiguateMentionsRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.DisambiguateMentions(ctx, req)
	case "aggregate_metadata":
		var req ToolAggregateMetadataRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.AggregateMetadata(ctx, req)
	case "representative_records":
		var req ToolRepresentativeRecordsRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.RepresentativeRecords(ctx, req)
	case "search_vector_range":
		var req ToolSearchVectorRangeRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchVectorRange(ctx, req)
	case "search_chunks_by_entities":
		var req ToolSearchChunksByEntitiesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchChunksByEntities(ctx, req)
	case "expand_graph":
		var req ToolExpandGraphRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.ExpandGraph(ctx, req)
	case "get_nodes":
		var req ToolGetNodesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GetNodes(ctx, req)
	case "find_nodes":
		var req ToolFindNodesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.FindNodes(ctx, req)
	case "get_chunks":
		var req ToolGetChunksRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GetChunks(ctx, req)
	case "build_context":
		var req ToolBuildContextRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.BuildContext(ctx, req)
	case "extract_conversation":
		var req ToolExtractConversationRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.ExtractConversation(ctx, req)
	case "search_graphrag_lexical":
		var req ToolSearchGraphRAGLexicalRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchGraphRAGLexical(ctx, req)
	case "knowledge_save":
		var req KnowledgeSaveRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SaveKnowledge(ctx, req)
	case "knowledge_update":
		var req KnowledgeUpdateRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpdateKnowledge(ctx, req)
	case "knowledge_get":
		var req KnowledgeGetRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GetKnowledge(ctx, req)
	case "knowledge_search":
		var req KnowledgeSearchRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchKnowledge(ctx, req)
	case "knowledge_delete":
		var req KnowledgeDeleteRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.DeleteKnowledge(ctx, req)
	case "knowledge_graph_namespace_upsert":
		var req KnowledgeGraphNamespaceUpsertRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpsertKnowledgeNamespace(ctx, req)
	case "knowledge_graph_namespace_list":
		return t.ListKnowledgeNamespaces(ctx)
	case "knowledge_graph_upsert":
		var req KnowledgeGraphUpsertRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpsertKnowledgeGraph(ctx, req)
	case "knowledge_graph_find":
		var req KnowledgeGraphFindRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.FindKnowledgeGraph(ctx, req)
	case "knowledge_graph_delete":
		var req KnowledgeGraphDeleteRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.DeleteKnowledgeGraph(ctx, req)
	case "knowledge_graph_import":
		var req KnowledgeGraphImportRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.ImportKnowledgeGraph(ctx, req)
	case "knowledge_graph_export":
		var req KnowledgeGraphExportRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.ExportKnowledgeGraph(ctx, req)
	case "knowledge_graph_query":
		var req KnowledgeGraphQueryRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.QueryKnowledgeGraph(ctx, req)
	case "knowledge_graph_shacl_validate":
		var req KnowledgeGraphSHACLValidateRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.ValidateKnowledgeGraphSHACL(ctx, req)
	case "knowledge_graph_infer_refresh":
		var req KnowledgeGraphInferenceRefreshRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.RefreshKnowledgeGraphInference(ctx, req)
	case "knowledge_graph_infer_summary":
		var req KnowledgeGraphInferenceSummaryRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SummarizeKnowledgeGraphInference(ctx, req)
	case "knowledge_graph_infer_explain":
		var req KnowledgeGraphInferenceExplainRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.ExplainKnowledgeGraphInference(ctx, req)
	case "knowledge_graph_infer_explain_match":
		var req KnowledgeGraphInferenceExplainMatchRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.ExplainKnowledgeGraphInferenceMatch(ctx, req)
	case "memory_save":
		var req MemorySaveRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SaveMemory(ctx, req)
	case "memory_update":
		var req MemoryUpdateRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.UpdateMemory(ctx, req)
	case "memory_list_all":
		var req MemoryListAllRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.db.ListAllMemoriesPaged(ctx, req)
	case "contract_tally":
		var req ContractTallyRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.db.ContractTallyTool(ctx, req)
	case "contract_needs_attention":
		var req NeedsAttentionRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.db.NeedsAttentionTool(ctx, req)
	case "graph_list_all":
		var req GraphListAllRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.db.ListGraphAll(ctx, req)
	case "memory_get":
		var req MemoryGetRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.GetMemory(ctx, req)
	case "memory_search":
		var req MemorySearchRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.SearchMemory(ctx, req)
	case "memory_delete":
		var req MemoryDeleteRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.DeleteMemory(ctx, req)
	case "knowledge_memory_remember":
		var req KnowledgeMemoryRememberRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.KnowledgeMemoryRemember(ctx, req)
	case "knowledge_memory_recall":
		var req KnowledgeMemoryRecallRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.KnowledgeMemoryRecall(ctx, req)
	case "knowledge_memory_build_context_pack":
		var req KnowledgeMemoryBuildContextPackRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.KnowledgeMemoryBuildContextPack(ctx, req)
	case "knowledge_memory_promote_to_knowledge":
		var req KnowledgeMemoryPromoteToKnowledgeRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.KnowledgeMemoryPromoteToKnowledge(ctx, req)
	case "knowledge_memory_expand_entity_context":
		var req KnowledgeMemoryExpandEntityContextRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.KnowledgeMemoryExpandEntityContext(ctx, req)
	case "knowledge_memory_neighbors":
		var req KnowledgeMemoryNeighborsRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.KnowledgeMemoryNeighbors(ctx, req)
	case "knowledge_memory_shortest_path":
		var req KnowledgeMemoryShortestPathRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.KnowledgeMemoryShortestPath(ctx, req)
	case "knowledge_memory_reflect":
		var req KnowledgeMemoryReflectRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.KnowledgeMemoryReflect(ctx, req)
	case "knowledge_memory_consolidate":
		var req KnowledgeMemoryConsolidateRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return t.KnowledgeMemoryConsolidate(ctx, req)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func toolObjectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	// Only emit "required" when there is at least one required field. A nil
	// slice serializes as "required": null, which strict JSON Schema
	// validators (e.g. OpenAI / DeepSeek function-calling) reject with
	// "null is not of type array".
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func toolStringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func toolIntegerSchema(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func toolNumberSchema(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func toolBooleanSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func toolEnumSchema(description string, values ...string) map[string]any {
	enumValues := make([]any, 0, len(values))
	for _, value := range values {
		enumValues = append(enumValues, value)
	}
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        enumValues,
	}
}

func toolMapSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          description,
		"additionalProperties": true,
	}
}

func toolStringArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

func toolNumberArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "number"},
	}
}
