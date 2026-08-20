# CortexDB

[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2) [![CI](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml) [![codecov](https://codecov.io/gh/liliang-cn/cortexdb/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/cortexdb) [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A pure-Go, single-file AI memory and knowledge graph. One SQLite file holds vectors, hybrid RAG search, scoped agent memory, an RDF/SPARQL knowledge graph, a Palantir-style ontology, and 60+ agent tools — embedded in your Go program, or installed as a shared brain for Claude Code / Codex. Works with **no embedder** (lexical mode, no API key) or any OpenAI-compatible embeddings endpoint. No service to run.

```bash
go get github.com/liliang-cn/cortexdb/v2
```

```go
db, _ := cortexdb.Open(cortexdb.DefaultConfig("brain.db"))
defer db.Close()
brain := db.KnowledgeMemory()
_, _ = brain.Remember(ctx, cortexdb.KnowledgeMemoryRememberRequest{Content: "Alice prefers tabs.", Scope: "user"})
rec, _ := brain.Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{Query: "what does Alice prefer?"})
fmt.Println(rec.ContextPack.Text) // paste-ready context pack with source attribution
```

## What's inside

- **KnowledgeMemory brain facade** — `Recall` / `Remember` / `Reflect` / `Consolidate` / `PromoteToKnowledge` / context packs; fused retrieval across episodic memory, durable knowledge, and GraphRAG chunks; relational answers returned as **graph facts** (`Alice —uses→ Apollo`) read from edges, reliable even with no embedder; deterministic no-LLM `extract_conversation`; memories can carry inline entities/relations so one call stores and graphs them.
- **Composable retrieval** — `cortex_query`: vector / lexical / hybrid / graph prefetch lanes fused by RRF, weighted RRF, or DBSF, with metadata filters and per-source score debugging; an `Authorize` callback gates every candidate (RBAC/ABAC at the retrieval layer); pluggable reranker.
- **Vector + lexical engine** — FTS5, HNSW / IVF / Flat indexes, scalar & binary quantization, geospatial indexing, semantic query routing.
- **Knowledge graph** — RDF triples/quads on the same file: a practical SPARQL subset (updates, OPTIONAL/UNION/VALUES, aggregates, subqueries, property paths), RDFS-lite materialized inference, SHACL-lite validation, N-Triples/Turtle/TriG I/O; property-graph `apply_inference` materializes two-hop relation compositions with provenance; entities track asserting documents, and `delete_document_graph` is deletion shaped like ingest.
- **Ontology (Palantir-style)** — typed object/link/interface types with primary keys and cardinality, an object-set algebra (union / intersect / filter / `search_around`), governed **action types** with audit trail, generated typed agent tools, and a breaking-change schema diff; `strict` or `vocabulary` enforcement.
- **Pipelines** — `memoryflow` (transcript → recall → wake-up → promotion), `graphflow` (corpus → graph → HTML report), `importflow` (CSV / SQL dumps / live Postgres-MySQL → RAG + KG), `connector` (PII masking, signed plans, reversible vault, CDC sync).
- **Tools & MCP** — 60+ tools with the same names in-process and over MCP, plus `render_graph_html`, an interactive graph view.
- **Quality, measured** — `pkg/eval` runs a labeled query set through the real retrieval path with recall@k / nDCG regression floors in CI; FTS5 / SPARQL / SQL-dump parsers are fuzz-tested.

## Claude Code / Codex plugin & shared brain

```text
/plugin marketplace add liliang-cn/cortexdb   →   /plugin install cortexdb@cortexdb      (Claude Code)
codex plugin marketplace add liliang-cn/cortexdb && codex plugin add cortexdb@cortexdb   (Codex)
```

Lexical mode by default, one global brain at `~/.cortexdb/cortexdb.db`, slash commands (`/remember`, `/recall`, `/cortexdb-graph`) and an auto-recall hook. Point many agents and machines at one `cortexdb-grpc` (`CORTEXDB_REMOTE=host:port` + token) and Claude Code, Codex, [OpenClaw](https://github.com/liliang-cn/openclaw-cortexdb-memory) and [Hermes](https://github.com/liliang-cn/hermes-cortexdb-memory) share the **same** memory and graph. Typed clients: `cargo add cortexdb-client` · `pip install cortexdb-client` · `npm install cortexdb-client`.

## More

Full guide (layers, ontology details, shared-brain ops): [docs/GUIDE.md](docs/GUIDE.md) · 16 runnable [examples](examples/README.md) (`go run ./examples/01_core` … `16_ontology`) · launch kit: [docs/LAUNCH_KIT.md](docs/LAUNCH_KIT.md) · 中文: [README_CN.md](README_CN.md)

Embedded, inspectable, local-first — not a distributed vector database, not an enterprise RDF server.
