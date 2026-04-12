# CortexDB Examples

The examples are organized by the current public architecture. There are six runnable folders:

```bash
go run ./examples/01_core
go run ./examples/02_rag
go run ./examples/03_memoryflow
go run ./examples/04_knowledge_graph
go run ./examples/05_graphflow
go run ./examples/06_tools_mcp
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

## Rule of Thumb

```text
Need raw vectors / collections?       -> 01_core
Need RAG knowledge storage/search?    -> 02_rag
Need chat/session memory?             -> 03_memoryflow
Need RDF/SPARQL/RDFS/SHACL?           -> 04_knowledge_graph
Need corpus-to-graph/report/export?   -> 05_graphflow
Need agent tool/MCP integration?      -> 06_tools_mcp
```
