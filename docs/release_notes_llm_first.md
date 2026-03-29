# LLM-First Release Notes

This release completes the repositioning of CortexDB as an LLM-first retrieval and knowledge engine.

## What Changed

- Added dual-mode GraphRAG:
  - embedder-backed GraphRAG when vectors are available
  - lexical/tool-calling GraphRAG when only an external LLM is available
- Added high-level durable APIs:
  - `knowledge_save`, `knowledge_update`, `knowledge_get`, `knowledge_search`, `knowledge_delete`
  - `memory_save`, `memory_update`, `memory_get`, `memory_search`, `memory_delete`
- Added ontology-lite:
  - active schema registration
  - entity/relation validation
  - typed source/target constraints
- Added deterministic inference:
  - `apply_inference`
  - provenance fields on inferred relations
  - cleanup on knowledge refresh and delete
- Added retrieval planning:
  - shared `plan` contract
  - `retrieval_mode=auto|lexical|graph`
  - graph cost controls such as `graph_light`, `max_expansion_seeds`, and `max_traversal_nodes`
- Added MCP stdio support through the official MCP Go SDK.
- Added evaluation fixtures, benchmarks, and release gates.

## Product Boundary

External LLMs are expected to handle:

- user-intent interpretation
- keyword, alias, synonym, and multilingual expansion
- entity and relation extraction
- multi-step tool orchestration
- final answer generation

CortexDB is expected to handle:

- storage
- chunking and indexing
- FTS/vector retrieval
- graph persistence and expansion
- schema validation
- deterministic inference
- context packing

## Recommended Usage

### If You Have An Embedder

- use `SaveKnowledge`
- use `SearchKnowledge` or `SearchGraphRAG`
- set `retrieval_mode=graph` when graph expansion is desired
- add ontology and inference when graph quality matters

### If You Do Not Have An Embedder

- expose `GraphRAGTools()` directly or use MCP stdio
- let the external LLM create a structured retrieval plan
- prefer lexical retrieval first, then selectively enable graph expansion
- use `knowledge_*`, `memory_*`, `ontology_*`, and `apply_inference`

## New Examples

- `examples/graphrag_embedder`
- `examples/llm_tool_calling`

## Validation Checklist

Before tagging a release, run:

```bash
go test ./pkg/cortexdb
go test -race ./pkg/cortexdb
go test -run 'TestEvaluationRetrievalModes|TestEvaluationSchemaValidationAndInference' ./pkg/cortexdb
go test -run '^$' -bench 'BenchmarkEvaluationGraphRAGSearchModes|BenchmarkEvaluationBuildContext' ./pkg/cortexdb
go build ./...
```

See also:

- `docs/retrieval_planning.md`
- `docs/evaluation.md`
