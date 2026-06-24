# CortexDB — Claude Code & Codex plugin

Local-first AI memory and knowledge graph, packaged for both **Claude Code** and **Codex** from one plugin directory. It bundles:

- **Skill** (`skills/cortexdb`) — guidance on CortexDB's layered API (`pkg/cortexdb`, `pkg/memoryflow`, `pkg/graphflow`, `pkg/importflow`, `pkg/connector`, `pkg/graph`, `pkg/core`), RAG, knowledge-graph, SPARQL/RDFS/SHACL, and MCP usage.
- **MCP server** — runs `cmd/cortexdb-mcp-stdio`, exposing live tools: `knowledge_save`, `knowledge_search`, `memory_save`, `memory_search`, `knowledge_graph_upsert`, `knowledge_graph_query`, `knowledge_graph_shacl_validate`, `knowledge_memory_recall`, `build_context`, and more.

The single directory carries two manifests — `.claude-plugin/plugin.json` (Claude Code) and `.codex-plugin/plugin.json` (Codex) — and a per-host MCP config (`.mcp.json` for Claude Code, `.mcp.codex.json` for Codex). The skill is shared.

**No Go toolchain required.** Both MCP configs invoke a launcher (`bin/cortexdb-mcp`) that, on first run, downloads the prebuilt MCP server binary for your OS/arch from the GitHub release, caches it in the plugin data dir, and execs it. Prebuilt targets: macOS (amd64, arm64), Windows (amd64, arm64), Linux (amd64, arm64).

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

None to install — the launcher fetches a static, prebuilt binary (no CGO, no external services; storage is a single SQLite file). It needs `curl` or `wget` on macOS/Linux (both ship by default) and PowerShell on Windows (built in). If a download is impossible and a Go toolchain is present, the launcher falls back to `go run`.

### Windows

`.mcp.json` points at `bin/cortexdb-mcp` (the POSIX launcher), which covers macOS and Linux out of the box. On Windows, set the MCP server command to the bundled batch launcher instead:

```
${CLAUDE_PLUGIN_ROOT}/bin/cortexdb-mcp.cmd
```

## Configuration

| Env var | Default | Purpose |
| --- | --- | --- |
| `CORTEXDB_PATH` | `cortexdb.db` | Path to the SQLite database file the MCP server opens. Export it in your environment to use a different file — it is inherited by the launched server. |
| `CORTEXDB_MCP_BIN` | _(unset)_ | Path to a local MCP server binary to run instead of downloading (e.g. a dev build). |

The server runs in **no-embedder (lexical) mode** by default — no API key required. RAG, knowledge graph, and memory tools all work without an embedder via lexical retrieval.

## Usage

Once installed, the skill auto-activates when you work with CortexDB, embeddings, RAG, knowledge graphs, SPARQL, or MCP memory tools. The MCP tools appear under the `cortexdb` server and are callable directly by Claude.
