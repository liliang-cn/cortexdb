# CortexDB Examples

The examples are organized by the current public architecture:

```bash
go run ./examples/01_core
go run ./examples/02_rag
go run ./examples/03_memoryflow
go run ./examples/04_knowledge_graph
go run ./examples/05_graphflow
go run ./examples/06_tools_mcp
go run ./examples/07_importflow
go run ./examples/08_self_knowledge_graph
go run ./examples/09_connector
go run ./examples/10_support_brain
go run ./examples/11_unified_brain
go run ./examples/12_incident_agent
go run ./examples/13_scale_analytics
go run ./examples/14_semantic_rag
go run ./examples/15_cortex_query
go run ./examples/16_ontology
```

## 01_core

Core embedded vector storage through `pkg/cortexdb`.

- Opens a single-file DB
- Uses `Quick()` for vector add/search
- Uses collections for namespacing

Use this when you want the smallest CortexDB integration surface.

## 02_rag

High-level durable knowledge APIs through `pkg/cortexdb`.

- `SaveKnowledge`
- no-embedder lexical `SearchKnowledge`
- entity/relation metadata for graph-aware retrieval

Use this for RAG and app knowledge storage.

## 03_memoryflow

Agent memory workflow through `pkg/memoryflow`.

- transcript ingest
- recall
- layered wake-up context
- diary write/read
- transcript reconstruction
- optional Hindsight recall strategy plugin

Use this for chat/session/agent memory.

## 04_knowledge_graph

Embedded RDF/KG workflow through `pkg/cortexdb` and `pkg/graph`.

- RDF triples
- SPARQL property paths and subqueries
- incremental RDFS inference
- SHACL-lite validation

Use this when you need raw knowledge graph semantics.

## 05_graphflow

Corpus-to-graph workflow through `pkg/graphflow`.

- canonical extraction schema
- deterministic build/analyze/report
- `graph.json`, `GRAPH_REPORT.md`, and HTML export

Use this when you need to turn documents or model extraction output into an inspectable graph.

## 06_tools_mcp

Toolbox/MCP-aligned APIs through `db.GraphRAGTools()`.

- lists the available tool definitions
- calls tools in-process with JSON payloads
- demonstrates optional lexical semantic-router tool selection
- shows the same shapes that can be exposed over MCP

Use this for agents and external LLM orchestration.

## 07_importflow

Structured-data import through `pkg/importflow`.

- parses a CSV (or MySQL/PostgreSQL dump) source
- builds a MappingPlan routing columns to RAG and knowledge-graph sinks
- Plan/Run/AutoImport flow with optional AI-assisted mapping

Use this to bootstrap a CortexDB file from existing structured data.

## 08_self_knowledge_graph

Dogfooding demo: CortexDB maps itself.

- splits `docs/PROJECT_OVERVIEW.md` into sections
- extracts entities/relations with an LLM (local Ollama or any
  OpenAI-compatible endpoint via `EXTRACT_BASE_URL`/`EXTRACT_API_KEY`)
- builds, analyzes, and exports the graph (JSON, report, HTML viz)
- `qa_test.go` answers natural-language questions deterministically
  from graph edges

Use this as the end-to-end graphflow reference.

## 09_connector

Privacy-preserving import through `pkg/connector`.

- classifies and masks sensitive CSV data
- imports the safe view into RAG / KG sinks
- demonstrates the reversible token vault boundary

Use this before connecting live operational data to an agent memory layer.

## 10_support_brain

Live support-brain workflow.

- connects to Postgres or MySQL
- desensitizes rows before RAG/KG ingestion
- answers with masked evidence and optional unmasking

Use this for a support/copilot memory prototype over live business data.

## 11_unified_brain

Multi-source unified brain.

- combines Postgres and MySQL sources
- streams CDC into one knowledge graph
- demonstrates SPARQL aggregates, RDFS inference, and SHACL checks

Use this when the agent needs one view across several operational systems.

## 12_incident_agent

LLM-assisted incident analysis.

- extracts a KG from unstructured incident reports
- exposes deterministic search / graph tools
- lets an agent decide which tool to call before answering

Use this for tool-using agents over unstructured operational text.

## 13_scale_analytics

Larger-volume analytics harness.

- timed bulk ingest
- CDC under load
- graph analytics and validation over larger fixtures

Use this when you want a rough performance and scale sanity check.

## 14_semantic_rag

Embedding-backed semantic RAG.

- plugs in an OpenAI-compatible embedding model
- demonstrates paraphrase search by meaning
- uses an LLM answer grounded on retrieved context

Use this when you need true semantic vector retrieval.

## 15_cortex_query

Composable Cortex Query API.

- runs dense vector, lexical FTS5, and graph/entity prefetches
- fuses candidates with weighted RRF
- shows payload filters, formula boosts, and source-rank debugging
- runs fully offline with one local CortexDB file

Use this when an agent needs vector + keyword + graph retrieval without a separate vector database or graph database service.

## 16_ontology

Palantir-style ontology, end to end and fully offline.

- activates a schema with interfaces, typed object types and per-side link cardinality
- writes through action types with submission criteria, `validate_only` and returned edits
- composes object sets: interface base, filter, intersect, subtract, `search_around`
- generates typed MCP tool definitions from the schema (capped, and not auto-registered)
- diffs a candidate schema and flags the changes that would invalidate stored data
- closes the generic write path with `strict_actions`

Use this when you want typed, governed, auditable writes rather than free-form entity upserts.

## 17_query_source

An external search engine as one retrieval lane, without becoming the storage.

- implements `cortexdb.QuerySource` against Meilisearch with nothing but `net/http`
- registers it with `WithQuerySource` and asks for it as a `source` prefetch
- fuses it with CortexDB's own lexical lane; every result names the lanes that found it
- shows what the lane actually buys: a typo (`rollbak`) that FTS5 correctly cannot match
- shows the limit: after a row is deleted here, the stale external index still names it
  and CortexDB drops it — a source names candidates, it never supplies content

Needs a Meilisearch (the file's header has the one-line `docker run`). Use this when a
team already has a search cluster whose recall you want, and you are deciding whether
that means replacing CortexDB's storage. It does not.

## Rule of Thumb

```text
Need raw vectors / collections?       -> 01_core
Need RAG knowledge storage/search?    -> 02_rag
Need chat/session memory?             -> 03_memoryflow
Need RDF/SPARQL/RDFS/SHACL?           -> 04_knowledge_graph
Need corpus-to-graph/report/export?   -> 05_graphflow
Need agent tool/MCP integration?      -> 06_tools_mcp
Need to import CSV / SQL dumps?       -> 07_importflow
Want the full graphflow E2E demo?     -> 08_self_knowledge_graph
Need privacy-gated live data import?  -> 09_connector / 10_support_brain / 11_unified_brain
Need an LLM tool-using graph agent?    -> 12_incident_agent
Need scale/CDC analytics checks?       -> 13_scale_analytics
Need real semantic vector retrieval?   -> 14_semantic_rag
Need vector + lexical + graph fusion?  -> 15_cortex_query
Need typed, governed, audited writes?   -> 16_ontology
Have a search cluster to fuse in?       -> 17_query_source
```
