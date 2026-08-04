# CortexDB

[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2) [![CI](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml) [![codecov](https://codecov.io/gh/liliang-cn/cortexdb/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/cortexdb) [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A pure-Go, single-file AI memory and knowledge graph library and plugin. Use CortexDB as an embedded memory/KG layer in your own Go agent projects, or install it as a shared memory brain for Claude Code and Codex. SQLite is the kernel — one file holds vectors, lexical/RAG search, scoped agent memory, an RDF/SPARQL/RDFS/SHACL knowledge graph, and MCP tools. Works with no embedder (lexical mode) or any OpenAI-compatible embeddings endpoint.

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

## Claude Code and Codex plugin

Give Claude Code (and Codex) durable memory + a knowledge graph as a plugin. It bundles the `cortexdb` skill plus a live MCP server, runs in no-embedder **lexical mode** by default (no API key, no Go toolchain — the server binary is fetched from the matching release), and stores everything in one **global** SQLite file shared by every project.

**Install — Claude Code** — run each as a slash command:

```text
/plugin marketplace add liliang-cn/cortexdb
/plugin install cortexdb@cortexdb
/reload-plugins
```

**Install — Codex** — run in your shell:

```bash
codex plugin marketplace add liliang-cn/cortexdb
codex plugin add cortexdb@cortexdb
```

Codex uses the same default global brain at `~/.cortexdb/cortexdb.db`.

**Use** — just talk to Claude; it calls the MCP tools for you ("remember that I prefer …", "what do you know about X?"). Or use the slash commands: `/remember <text>`, `/recall <query>`, `/cortexdb-graph` (interactive knowledge-graph view), or `/cortexdb` for the skill. Key tools: `memory_save` / `memory_search`, `knowledge_save` / `knowledge_search`, `knowledge_graph_query`, and the unified `knowledge_memory_recall`. When enabled, a `SessionStart` directive + `UserPromptSubmit` auto-recall hook make Claude recall and save proactively (it asks once, per machine).

**Where data lives** — `~/.cortexdb/cortexdb.db` by default, so memory follows you across projects (multiple sessions share it safely via SQLite WAL). Override per project:

```bash
export CORTEXDB_PATH=.cortexdb/cortexdb.db   # inherited by the launched server
```

To force the same global brain explicitly, set:

```bash
export CORTEXDB_PATH="$HOME/.cortexdb/cortexdb.db"
```

To upgrade: `/plugin update cortexdb` then `/reload-plugins` — the server binary auto-refreshes (version-pinned cache). See `plugins/cortexdb/README.md` for all env vars.

### Shared brain — one CortexDB, many agents and machines

By default every agent opens its own SQLite file. Point them at one central
`cortexdb-grpc` instead and Claude Code, Codex, OpenClaw and agents in other VMs
read and write the **same** memory and knowledge graph.

On the host that owns the database:

```bash
CORTEXDB_PATH=$HOME/.cortexdb/cortexdb.db \
CORTEXDB_GRPC_ADDR=10.0.0.5:47821 \
CORTEXDB_GRPC_TOKEN=<token> cortexdb-grpc
```

On every client:

```bash
export CORTEXDB_REMOTE="10.0.0.5:47821"
export CORTEXDB_GRPC_TOKEN="<the same token>"
```

That is the whole change. The MCP server then opens no local database: it
discovers the tool surface from the server at startup and proxies every call, so
all tools — current and future — work identically. The `UserPromptSubmit`
auto-recall hook follows the same remote, so injected memories come from the
same brain the tools write to.

Transport is plaintext by design — run it over loopback, a trusted LAN, or
Tailscale. **The token is the access control**: anyone holding it has full
read/write access. Embedder and LLM settings live on the server, not the
clients. The remaining one-shot modes (`--graph-html`, `--export-memory`,
`--learn-path`) still act on a local database.

## OpenClaw and Hermes memory plugins

CortexDB also ships native memory-layer adapters for agents that expose a memory
lifecycle API. Both use the existing gRPC sidecar and unified
`knowledge_memory_recall`; they do not add a parallel storage path.

- OpenClaw: [`liliang-cn/openclaw-cortexdb-memory`](https://github.com/liliang-cn/openclaw-cortexdb-memory)
  registers the exclusive `memory` capability plus recall/store/delete tools.
- Hermes Agent: [`liliang-cn/hermes-cortexdb-memory`](https://github.com/liliang-cn/hermes-cortexdb-memory)
  registers a `MemoryProvider` with automatic pre-turn recall and completed-turn capture.

```bash
# OpenClaw
openclaw plugins install npm:cortexdb-openclaw-memory@2.57.1
openclaw config set plugins.slots.memory cortexdb-memory
openclaw gateway restart

# Hermes Agent
hermes plugins install liliang-cn/hermes-cortexdb-memory --enable
hermes config set memory.provider cortexdb
hermes gateway restart
```

Run `cortexdb-grpc`, then install the adapter. The existing
[`skills/`](skills/) remain useful for explicit tool instructions and helper
functions, but a skill alone does not replace the host agent's native memory
backend.

## Other languages (gRPC sidecar)

`cortexdb-grpc` serves the full facade over gRPC, with typed clients for Rust/Python/Node:

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=my.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc   # 127.0.0.1:47821
cargo add cortexdb-client   # pip install cortexdb-client   # npm install cortexdb-client
```

## Quality

Retrieval quality is measured, not assumed: `pkg/eval` runs a labeled query set through the real retrieval path and reports recall@k / precision@k / MRR / nDCG, with regression floors in CI (`go test ./pkg/eval -run TestLexicalRetrievalQuality -v`). Parser/search surfaces (FTS5, SPARQL, SQL-dump import) have Go fuzz tests (`go test ./... -run Fuzz`); their saved corpora are permanent regression seeds.

## Examples & Status

`examples/01_core` … `15_cortex_query` are small and architecture-oriented (`go run ./examples/01_core`); 01-07/09/15 run standalone, others need an LLM/embeddings/live DB — see [examples/README.md](examples/README.md).

An embedded local-first AI memory/KG library — not a drop-in replacement for Fuseki/GraphDB/Stardog. One file, Go APIs, tool/MCP surfaces, and enough RDF/SPARQL/RDFS/SHACL to build real memory workflows.
