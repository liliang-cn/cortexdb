# CortexDB

[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2) [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Pure-Go, single-file AI memory and knowledge graph library. SQLite is the kernel — one file holds vectors, lexical/RAG search, scoped agent memory, an RDF/SPARQL/RDFS/SHACL knowledge graph, and MCP tools. Built for local-first agents that need durable memory without a separate vector DB, graph DB, or MCP stack. Works with no embedder (lexical mode) or any OpenAI-compatible embeddings endpoint.

## Why CortexDB?

Use CortexDB when you want an agent memory layer that is embedded, inspectable, and graph-aware without standing up more infrastructure.

| If you were considering... | CortexDB gives you... | Trade-off |
| --- | --- | --- |
| `chromem-go` or a small embedded vector store | Vectors plus lexical search, durable knowledge, scoped memory, RDF/SPARQL, and MCP tools in one SQLite file | More surface area if all you need is a tiny vector collection |
| `sqlite-vec` or raw SQLite extensions | A Go facade for RAG, memory, hybrid retrieval, graph facts, and agent tools | Less low-level SQL control than wiring extensions yourself |
| Chroma, Qdrant, LanceDB, or a hosted vector DB | No service to run, no separate storage plane, and lexical mode with no API key | Not trying to be a distributed vector database |
| Fuseki, GraphDB, Stardog, or a standalone graph DB | Enough RDF/SPARQL/RDFS/SHACL for local-first agent workflows, next to the text and memory store | Not a full enterprise RDF server |
| Custom memory tables for Claude Code/Codex | A packaged plugin, MCP server, auto-recall path, and reusable memory/KG tools | Bring your own product-specific memory policy |

Planning a launch or community post? See [docs/LAUNCH_KIT.md](docs/LAUNCH_KIT.md) for ready-to-edit Show HN, Reddit, and demo scripts.

## Install & Quick Start

```bash
go get github.com/liliang-cn/cortexdb/v2
```

```go
db, _ := cortexdb.Open(cortexdb.DefaultConfig("KnowledgeMemory.db"))
defer db.Close()

q := db.Quick()
_, _ = q.Add(ctx, []float32{0.1, 0.2, 0.9}, "SQLite is a single-file database.")
hits, _ := q.Search(ctx, []float32{0.1, 0.2, 0.8}, 1)

// No-embedder RAG (lexical):
_, _ = db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
    KnowledgeID: "apollo", Content: "Alice owns Apollo. Apollo ships Friday."})
resp, _ := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
    Query: "Who owns Apollo?", RetrievalMode: cortexdb.RetrievalModeLexical, TopK: 3})
```

## Layers — pick the right one

```text
pkg/cortexdb   Main facade: vectors, text/RAG search, knowledge, memory, KG, tools, MCP.  ← start here
pkg/memoryflow Agent memory workflow: transcript ingest, recall, wake-up layers, promotion.
pkg/graphflow  Corpus → extract → build → analyze → report → export (HTML).
pkg/importflow Import CSV / SQL dumps / live Postgres-MySQL into RAG + KG (DDL → graph).
pkg/connector  Privacy gate over importflow: PII masking, signed plan, reversible vault, CDC sync.
pkg/graph      Low-level RDF/SPARQL/RDFS/SHACL + property graph.
pkg/core       SQLite storage, embeddings, FTS5, vector indexes (HNSW/IVF/Flat).
```

## Knowledge Graph

Embedded RDF on the same file: triples/quads, namespaces, N-Triples/Turtle/TriG I/O, a practical SPARQL subset (SELECT/ASK/CONSTRUCT/DESCRIBE, updates, OPTIONAL/UNION/MINUS/VALUES/BIND/FILTER, aggregates, subqueries, property paths `^p p|q p+ p*`), RDFS-lite materialized inference, and SHACL-lite validation.

```go
db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: triples})
res, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
    Query: `SELECT ?name WHERE { <https://example.com/alice> <https://schema.org/name> ?name }`})
```

## Tools, MCP & Plugin

```go
tools := db.GraphRAGTools()                             // in-process tool calling
server := db.NewMCPServer(cortexdb.MCPServerOptions{})  // MCP server
```

Tool groups: GraphRAG (`ingest_document`, `search_text`, `build_context`), knowledge/memory (`knowledge_save`, `memory_search`, …), KG (`knowledge_graph_query`, `_shacl_validate`), KnowledgeMemory (`knowledge_memory_recall`, `_reflect`). `memoryflow`/`graphflow`/`importflow`/`connector` expose their own toolboxes too.

Install as a Claude Code / Codex plugin (bundles skill + MCP server, lexical mode, no API key):

```bash
# Claude Code — run each as a slash command:
/plugin marketplace add liliang-cn/cortexdb
/plugin install cortexdb@cortexdb

# Codex:
codex plugin marketplace add liliang-cn/cortexdb && codex plugin install cortexdb@cortexdb
```

## Other languages (gRPC sidecar)

`cortexdb-grpc` serves the full facade over gRPC, with typed clients for Rust/Python/Node:

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=my.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc   # 127.0.0.1:47821
cargo add cortexdb-client   # pip install cortexdb-client   # npm install cortexdb-client
```

## Examples & Status

`examples/01_core` … `15_cortex_query` are small and architecture-oriented (`go run ./examples/01_core`); 01-07/09/15 run standalone, others need an LLM/embeddings/live DB — see [examples/README.md](examples/README.md).

An embedded local-first AI memory/KG library — not a drop-in replacement for Fuseki/GraphDB/Stardog. One file, Go APIs, tool/MCP surfaces, and enough RDF/SPARQL/RDFS/SHACL to build real memory workflows.
