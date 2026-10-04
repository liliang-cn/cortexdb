# CortexDB

[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2) [![CI](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml) [![codecov](https://codecov.io/gh/liliang-cn/cortexdb/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/cortexdb) [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

AI memory and a knowledge graph in one SQLite file. Pure Go, no service to run, works without an embedding model.

![The CortexDB live view](docs/assets/img/live-view-dark-1600.webp)

## Install

| | |
| --- | --- |
| Go library | `go get github.com/liliang-cn/cortexdb/v2` |
| Claude Code | `/plugin marketplace add liliang-cn/cortexdb`, then `/plugin install cortexdb@cortexdb` |
| Codex | `codex plugin marketplace add liliang-cn/cortexdb && codex plugin add cortexdb@cortexdb` |
| Shared-brain server | `go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest` |
| Clients | `cargo add cortexdb-client` · `pip install cortexdb-client` · `npm install cortexdb-client` |

## Use

```go
db, _ := cortexdb.Open(cortexdb.DefaultConfig("brain.db"))
defer db.Close()
brain := db.KnowledgeMemory()
_, _ = brain.Remember(ctx, cortexdb.KnowledgeMemoryRememberRequest{Content: "Alice prefers tabs.", Scope: "user"})
rec, _ := brain.Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{Query: "what does Alice prefer?"})
fmt.Println(rec.ContextPack.Text)
```

The plugin gives Claude Code and Codex one global brain at `~/.cortexdb/cortexdb.db`, `/remember`, `/recall` and an auto-recall hook. Point several agents or machines at one `cortexdb-grpc` and they share the same memory and graph.

In Codex, review and trust the plugin hooks in `/hooks` to activate automatic
recall. See the [plugin guide](plugins/cortexdb/README.md) for setup and
`cortexdb-mcp --doctor --self-test` diagnostics (v2.119.0+).

## What's inside

- Vectors (HNSW, IVF, flat, binary codes), FTS5 full-text search, hybrid and graph retrieval
- RAG knowledge, scoped agent memory, context packs with sources
- RDF 1.2 knowledge graph: SPARQL 1.1/1.2, RDFS + OWL 2 RL inference, SHACL Core + SHACL-SPARQL, RDF/XML import — passing the W3C test suites — and read-only Cypher
- Palantir-style ontology with governed actions
- 80+ tools, in-process or over MCP
- `serve_graph_3d`: a live 3D view to find, ask, query and expand, on desktop or phone
- Change feed: every committed write, in order, exactly once
- SQLite by default, PostgreSQL + pgvector with a `postgres://` DSN

## Environment

| Variable | Meaning |
| --- | --- |
| `CORTEXDB_PATH` | Database file (default `~/.cortexdb/cortexdb.db`) |
| `CORTEXDB_REMOTE` | Use a shared `cortexdb-grpc` at `host:port` instead of a local file |
| `CORTEXDB_GRPC_TOKEN` | Bearer token for that server |
| `CORTEXDB_GRPC_ADDR` | Server listen address (default `127.0.0.1:47821`) |
| `CORTEXDB_EMBED_BASE_URL` | OpenAI-compatible embeddings endpoint; unset = lexical mode |
| `CORTEXDB_EMBED_MODEL` / `CORTEXDB_EMBED_DIM` | Embedding model and dimension |

## Docs

[Website](https://liliang-cn.github.io/cortexdb/) · [Guide](docs/GUIDE.md) (full feature reference) · [Examples](examples/README.md) · [Changelog](CHANGELOG.md) · [中文](README_CN.md)

## License

MIT
