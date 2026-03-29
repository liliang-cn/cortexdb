# Retrieval Planning Contract

## Goal

Provide one stable contract for external LLM planners and internal CortexDB search APIs.

The preferred search flow is:

1. The external LLM turns the user's goal into a structured `plan`.
2. CortexDB executes that plan deterministically.
3. CortexDB returns both the resolved `plan` and the final `decision`.

This keeps natural-language interpretation outside the database while making retrieval behavior auditable.

## Plan Shape

```json
{
  "query": "Find Alice's employer",
  "keywords": ["Alice", "employer", "works", "Acme"],
  "alternate_queries": ["Alice works at Acme"],
  "entity_names": ["Alice", "Acme"],
  "retrieval_mode": "auto",
  "filters": {
    "collection": "graphrag_chunks",
    "document_ids": ["knowledge-1"]
  }
}
```

Fields:

- `query`: canonical query text
- `keywords`: aliases, synonyms, abbreviations, multilingual terms, and domain terms
- `alternate_queries`: alternate phrasings derived from the same goal
- `entity_names`: structured entity hints for graph-aware retrieval
- `retrieval_mode`: `lexical`, `graph`, or `auto`
- `filters`: structured scope constraints such as `collection`, `document_ids`, `user_id`, `session_id`, `scope`, and `namespace`
- `graph_light`: optional lower-latency graph mode
- `max_expansion_seeds`: optional cap on how many seed chunks will be expanded through the graph
- `max_traversal_nodes`: optional cap on how many graph nodes CortexDB will inspect during expansion
- `max_entities_per_chunk`: optional cap on graph-derived entities attached to each chunk

## Mode Semantics

- `lexical`: skip graph expansion and graph entity enrichment
- `graph`: force graph expansion when the API supports it
- `auto`: let CortexDB decide based on entity hints and query entity signal

Every search response returns a `decision`:

```json
{
  "requested_mode": "graph",
  "effective_mode": "graph",
  "use_graph": true,
  "reason": "graph mode requested explicitly"
}
```

This lets callers distinguish what was requested from what CortexDB actually executed.

## Recommended LLM Workflow

For `knowledge_search`, `search_text`, and `search_graphrag_lexical`:

1. Read the user's goal.
2. Expand it into many `keywords` and `alternate_queries`.
3. Extract likely `entity_names`.
4. Choose `retrieval_mode`.
5. Add `filters` only when the scope is known.
6. Call the search tool with both top-level compatibility fields and the preferred `plan` object if needed.

Recommended defaults:

- Use `auto` when the planner is uncertain.
- Use `lexical` when speed matters more than graph recall.
- Use `graph` only when the planner has explicit entity hints or knows graph expansion is necessary.
- Use `graph_light=true` when graph recall still matters but latency must stay bounded.

## Memory Search Notes

`memory_search` accepts the same `plan` contract, but it does not perform graph expansion.

- With an embedder, `auto` can still use semantic session retrieval.
- Without an embedder, memory search falls back to lexical retrieval.
- The returned `decision` will explicitly say that graph expansion was not used.

## Compatibility

Legacy top-level search fields still work:

- `keywords`
- `alternate_queries`
- `entity_names`
- `retrieval_mode`
- `disable_graph`

The `plan` object is preferred, but callers do not need to migrate all at once.
