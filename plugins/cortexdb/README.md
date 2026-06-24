# CortexDB — Claude Code & Codex plugin

Local-first AI memory and knowledge graph, packaged for both **Claude Code** and **Codex** from one plugin directory. It bundles:

- **Skill** (`skills/cortexdb`) — guidance on CortexDB's layered API (`pkg/cortexdb`, `pkg/memoryflow`, `pkg/graphflow`, `pkg/importflow`, `pkg/connector`, `pkg/graph`, `pkg/core`), RAG, knowledge-graph, SPARQL/RDFS/SHACL, and MCP usage.
- **MCP server** — runs `cmd/cortexdb-mcp-stdio`, exposing live tools: `knowledge_save`, `knowledge_search`, `memory_save`, `memory_search`, `knowledge_graph_upsert`, `knowledge_graph_query`, `knowledge_graph_shacl_validate`, `knowledge_memory_recall`, `build_context`, and more.

The single directory carries two manifests — `.claude-plugin/plugin.json` (Claude Code) and `.codex-plugin/plugin.json` (Codex) — and a per-host MCP config (`.mcp.json` for Claude Code, `.mcp.codex.json` for Codex). The skill is shared.

## Install — Claude Code

From the marketplace (this repository):

```shell
/plugin marketplace add liliang-cn/cortexdb
/plugin install cortexdb@cortexdb
```

Or test locally without installing:

```bash
claude --plugin-dir ./plugins/cortexdb
```

## Install — Codex

The repo also ships a Codex marketplace at `.agents/plugins/marketplace.json`:

```bash
codex plugin marketplace add liliang-cn/cortexdb
codex plugin install cortexdb@cortexdb
```

## Requirements

The MCP server launches via `go run github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-mcp-stdio@latest`, so a **Go toolchain (1.25+)** must be on `PATH`. No CGO, no external services — storage is a single SQLite file.

## Configuration

| Env var | Default | Purpose |
| --- | --- | --- |
| `CORTEXDB_PATH` | `cortexdb.db` | Path to the SQLite database file the MCP server opens. Export it in your environment before launching to use a different file — the MCP config sets no `env` block, so the value is inherited. |

The server runs in **no-embedder (lexical) mode** by default — no API key required. RAG, knowledge graph, and memory tools all work without an embedder via lexical retrieval.

## Usage

Once installed, the skill auto-activates when you work with CortexDB, embeddings, RAG, knowledge graphs, SPARQL, or MCP memory tools. The MCP tools appear under the `cortexdb` server and are callable directly by Claude.
