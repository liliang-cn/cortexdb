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
codex plugin add cortexdb@cortexdb
```

## Requirements

**No Go toolchain — ever.** Both the Claude Code and Codex configs invoke the same launcher (`bin/cortexdb-mcp`), which fetches a static, prebuilt binary from the GitHub release (no CGO, no external services; storage is a single SQLite file). It needs only `curl` or `wget` on macOS/Linux (both ship by default) and PowerShell on Windows (built in). If neither is present, it prints the exact manual-download command and exits — it never requires or invokes Go.

### Windows

`.mcp.json` points at `bin/cortexdb-mcp` (the POSIX launcher), which covers macOS and Linux out of the box. On Windows, set the MCP server command to the bundled batch launcher instead:

```
${CLAUDE_PLUGIN_ROOT}/bin/cortexdb-mcp.cmd
```

## Configuration

| Env var | Default | Purpose |
| --- | --- | --- |
| `CORTEXDB_PATH` | `~/.cortexdb/cortexdb.db` | Path to the SQLite database file the MCP server opens. Defaults to a single **global** store shared by every project on the machine (the directory is created automatically; SQLite WAL makes concurrent sessions safe). Export it — e.g. to a project-local `.cortexdb/cortexdb.db` — to use a different file; it is inherited by the launched server. |
| `CORTEXDB_MCP_BIN` | _(unset)_ | Path to a local MCP server binary to run instead of downloading (e.g. a dev build). |
| `CORTEXDB_RECALL_TOPK` | `3` | How many matched memories the auto-recall hook injects per prompt (see below). |
| `CORTEXDB_EMBED_BASE_URL` | _(unset)_ | OpenAI-compatible `/embeddings` base URL. **Setting it turns on semantic retrieval** (hybrid vector + lexical). Point it at any provider, including a local Ollama: `http://localhost:11434/v1`. `OPENAI_BASE_URL` is accepted as a fallback (parity with `cortexdb-grpc`). |
| `CORTEXDB_EMBED_MODEL` | `text-embedding-3-small` | Embedding model name (e.g. `embeddinggemma` for a local Ollama). |
| `CORTEXDB_EMBED_DIM` | `1536` | Embedding dimension — must match the model (e.g. `768` for `embeddinggemma`). |
| `CORTEXDB_EMBED_API_KEY` | _(unset)_ | API key for the embeddings endpoint (Ollama accepts any value). `OPENAI_API_KEY` is accepted as a fallback. |
| `CORTEXDB_LLM_BASE_URL` | _(unset)_ | Chat endpoint for **LLM graph distillation**. When set, `/cortexdb-graph` and `/cortexdb-import-memory` use an LLM to extract clean, typed entities and only explicitly-stated relations, instead of the deterministic heuristic (which can surface noisy candidates). A bare Ollama host (`http://localhost:11434`) uses the native `/api/chat` with `think:false` — fast and well-formed for reasoning models like `qwen3.5`; a `/v1` base uses OpenAI-compatible `/chat/completions`. |
| `CORTEXDB_LLM_MODEL` | `qwen3.5` | Chat model for graph distillation. |
| `CORTEXDB_LLM_API_KEY` | _(unset)_ | Bearer token for the OpenAI-compatible chat path (Ollama needs none). |

The server runs in **no-embedder (lexical) mode** by default — no API key required. RAG, knowledge graph, and memory tools all work without an embedder via lexical retrieval. Set `CORTEXDB_EMBED_BASE_URL` (plus model/dim) to enable semantic hybrid retrieval — e.g. against a local Ollama:

```bash
export CORTEXDB_EMBED_BASE_URL="http://localhost:11434/v1"
export CORTEXDB_EMBED_MODEL="embeddinggemma"
export CORTEXDB_EMBED_DIM="768"
export CORTEXDB_EMBED_API_KEY="ollama"   # any placeholder
```

Add an LLM to distill a **clean, typed knowledge graph** (used by `/cortexdb-graph` and `/cortexdb-import-memory`) instead of the deterministic extractor — e.g. `qwen3.5` on a local Ollama:

```bash
export CORTEXDB_LLM_BASE_URL="http://localhost:11434"   # native /api/chat, think:false
export CORTEXDB_LLM_MODEL="qwen3.5"
```

To make the global store explicit in a shell or MCP config, use:

```bash
export CORTEXDB_PATH="$HOME/.cortexdb/cortexdb.db"
```

## Slash commands

| Command | What it does |
| --- | --- |
| `/remember <text>` | Save a fact / preference / decision to the brain (`memory_save`, or `knowledge_save` for reference knowledge). |
| `/recall <query>` | Search the brain for relevant memories + knowledge (`knowledge_memory_recall`) and summarize. |
| `/cortexdb-graph` | Render the brain's knowledge graph (entities, relations, documents) to an interactive HTML page and open it. Exports to `~/.cortexdb/graph/`; backed by `cortexdb-mcp --graph-html`. |
| `/cortexdb-import-memory` | Import local agent memory (memory files + `CLAUDE.md`/`AGENTS.md`) into the brain and organize it into the graph. Backed by `cortexdb-mcp --import-agent-memory`. |
| `/cortexdb-import-code` | Turn a codebase (**any language** — you are the extractor) into a code knowledge graph in its own isolated database, then open an interactive view. Answers "who implements X / who calls Y / what depends on Z / any dependency cycle?" as graph queries. Backed by `cortexdb-mcp --import-code-graph`. |

## Proactive memory (SessionStart)

When auto-recall is enabled, a `SessionStart` hook injects a short standing directive each session so the assistant uses the brain **proactively** — recall relevant context before answering, and save durable preferences/decisions/facts without being asked. It respects the same on/off switch as auto-recall, so it stays silent on machines where you declined.

## Auto-recall (Claude Code only)

The plugin ships a `UserPromptSubmit` hook (`hooks/hooks.json` → `bin/cortexdb-recall`) that, on every prompt, searches the project's CortexDB for memories relevant to what you just typed and injects the top matches into context — so stored knowledge is used automatically, not only when a tool is called explicitly.

It is built to stay out of the way:

- **Scoped to real databases.** It looks for `$CORTEXDB_PATH` (else the global `~/.cortexdb/cortexdb.db`). If no database exists yet, the hook does nothing — zero overhead, and it never creates a database.
- **Asked once.** The first time it runs on a machine with a database present, it asks (via Claude) whether you want auto-recall, and remembers your answer in `${XDG_CACHE_HOME:-~/.cache}/cortexdb/autorecall`.
- **Never blocks.** Any error — missing binary, query failure, no matches — exits silently so your prompt is never delayed.
- **Bounded.** Injects at most `CORTEXDB_RECALL_TOPK` (default 3) snippets.

Toggle it any time:

```bash
${CLAUDE_PLUGIN_ROOT}/bin/cortexdb-recall --enable    # turn on
${CLAUDE_PLUGIN_ROOT}/bin/cortexdb-recall --disable   # turn off
```

## Usage

Once installed, the skill auto-activates when you work with CortexDB, embeddings, RAG, knowledge graphs, SPARQL, or MCP memory tools. The MCP tools appear under the `cortexdb` server and are callable directly by Claude.
