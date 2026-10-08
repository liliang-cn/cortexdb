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
| Claude Code mod (optional) | `/plugin install cortexdb-live@cortexdb` |
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

## Claude Code mod

`cortexdb-live` is an optional [mod](https://code.claude.com/docs/en/plugins/mods/overview) that runs inside Claude Code on top of the `cortexdb` plugin. Install it after the plugin:

```
/plugin install cortexdb-live@cortexdb
```

The mod calls three of the plugin's tools itself, and a hook gets no permission prompt, so allow them once in `~/.claude/settings.json` (not needed in bypass mode):

```json
"permissions": {
  "allow": [
    "mcp__plugin_cortexdb_cortexdb__knowledge_memory_recall",
    "mcp__plugin_cortexdb_cortexdb__graph_statistics",
    "mcp__plugin_cortexdb_cortexdb__memory_save"
  ]
}
```

Without them the status line names the missing rules.

- **Planned recall.** Before the brain is searched, haiku turns each prompt into keywords in Chinese and English, aliases, entity names and a retrieval mode. A bare "ok" or "go ahead" searches nothing. The band above the prompt shows what was recalled; expand it to see the plan and every hit. Hide lasts until the next recall; `/cortexdb-show` brings the hidden one back.
- **Status line.** Which brain the session uses, its node count and the last recall's time, or why the brain can't be reached.
- **Capture.** 90 seconds after a session goes idle, haiku distils the new part of the conversation into durable memories (`auto:<session>:<slug>`). Each later pass replaces a memory under the same slug rather than adding a copy. Older memories a new one makes untrue (a host moved, a decision reversed) are marked superseded: kept, but no longer recalled as current.

Haiku runs on Claude Code's own model access, so no API key is needed. Language: `/config` → `cortexdb-live` → `language` (`auto`, `zh`, `en`). The mod honours the plugin's own switches: `cortexdb-recall --disable` and `cortexdb-session-end --disable` turn recall and capture off for both. While the mod runs, the plugin's shell recall and capture hooks stand down, so nothing is done twice. Codex keeps those hooks. Mods are an early-access Claude Code feature (built against 2.1.290).

## What's inside

- Vectors (HNSW, IVF, flat, binary codes), FTS5 full-text search, hybrid and graph retrieval
- `EmbeddedConfig` for small devices: an SQ8 index, bounded SQLite caches, snapshots that survive a power cut — 100k 768-d vectors open in 0.28 s with a 193 MB heap
- RAG knowledge, scoped agent memory, context packs with sources
- RDF 1.2 knowledge graph: SPARQL 1.1/1.2, RDFS + OWL 2 RL inference, SHACL Core + SHACL-SPARQL, RDF/XML import — passing the W3C test suites — and read-only Cypher
- Palantir-style ontology with governed actions
- 80+ tools, in-process or over MCP
- `serve_graph_3d`: a live 3D view to find, ask, query and expand, on desktop or phone — or the same brain as a library, each memory a book on its project's shelf
- `import_agent_memory`: bring in what Claude Code and Codex already remember — memory notes, `CLAUDE.md` / `AGENTS.md`, Codex's memories, and optionally past sessions distilled into memories — into a local or shared brain
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
