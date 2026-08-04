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
| `CORTEXDB_REMOTE` | _(unset)_ | `host:port` of a central `cortexdb-grpc`. **Setting it switches the MCP server into shared-brain client mode**: it opens no local database and forwards every tool call to that one server, so Claude Code, Codex, VMs and other machines all use the same brain. See [Shared brain](#shared-brain-one-cortexdb-many-agents-and-machines). |
| `CORTEXDB_GRPC_TOKEN` | _(unset)_ | Bearer token for `CORTEXDB_REMOTE`; must match the token the server was started with. |
| `CORTEXDB_RECALL_TOPK` | `3` | How many matched memories the auto-recall hook injects per prompt (see below). |
| `CORTEXDB_EMBED_BASE_URL` | _(unset)_ | OpenAI-compatible `/embeddings` base URL. **Setting it turns on semantic retrieval** (hybrid vector + lexical). Point it at any provider, including a local Ollama: `http://localhost:11434/v1`. `OPENAI_BASE_URL` is accepted as a fallback (parity with `cortexdb-grpc`). |
| `CORTEXDB_EMBED_MODEL` | `text-embedding-3-small` | Embedding model name (e.g. `embeddinggemma` for a local Ollama). |
| `CORTEXDB_EMBED_DIM` | `1536` | Embedding dimension — must match the model (e.g. `768` for `embeddinggemma`). |
| `CORTEXDB_EMBED_API_KEY` | _(unset)_ | API key for the embeddings endpoint (Ollama accepts any value). `OPENAI_API_KEY` is accepted as a fallback. |
| `CORTEXDB_LLM_BASE_URL` | _(unset)_ | Chat endpoint for **LLM graph distillation**. When set, `/cortexdb-graph` and `/cortexdb-import-memory` use an LLM to extract clean, typed entities and only explicitly-stated relations, instead of the deterministic heuristic (which can surface noisy candidates). A bare Ollama host (`http://localhost:11434`) uses the native `/api/chat` with `think:false` — fast and well-formed for reasoning models like `qwen3.5`; a `/v1` base uses OpenAI-compatible `/chat/completions`. |
| `CORTEXDB_LLM_MODEL` | `qwen3.5` | Chat model for graph distillation. |
| `CORTEXDB_LLM_API_KEY` | _(unset)_ | Bearer token for the OpenAI-compatible chat path (Ollama needs none). |
| `CORTEXDB_RERANK_BASE_URL` | _(unset)_ | Cross-encoder **reranker** endpoint. When set, retrieval (SearchKnowledge / GraphRAG / hybrid) reorders candidates with a real cross-encoder before the built-in MMR diversity pass — a semantic upgrade to the default lexical-overlap rerank. Calls the `/rerank` shape shared by Cohere, Jina, vLLM, and Hugging Face TEI, so a local `bge-reranker` via TEI (`http://localhost:8080`) or a hosted reranker both work. |
| `CORTEXDB_RERANK_MODEL` | _(unset)_ | Reranker model name (sent when set; TEI ignores it). |
| `CORTEXDB_RERANK_API_KEY` | _(unset)_ | Bearer token for the reranker endpoint (local TEI needs none). |
| `CORTEXDB_QUERY_REWRITE` | _(unset)_ | Set to `1`/`true` to enable **pre-retrieval query transformation** (multi-query rewrite + HyDE). When on **and** a chat LLM is configured (`CORTEXDB_LLM_BASE_URL`), `SearchKnowledge` expands the raw query into alternate phrasings and keywords (fused into lexical + graph recall) plus a hypothetical answer passage; with an embedder set, the semantic query vector is derived from that passage (HyDE) instead of the literal question. Best-effort: an LLM error silently falls back to the raw query. |

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

## Shared brain — one CortexDB, many agents and machines

By default the MCP server opens a **local SQLite file**. Several processes on *one* machine (Claude Code + Codex + a script) share that file safely — SQLite runs in WAL mode. That does **not** extend across machines: a SQLite file on a network mount (NFS/SMB/virtiofs) is a corruption hazard, not a shared brain.

To give **Claude Code, Codex, a Lima VM, and agents in other VMs one shared brain**, run the database in exactly one place and let everyone else talk to it:

```
        ┌──────────────────────────────────────────────┐
        │  host that owns the file (e.g. your Mac)      │
        │  cortexdb-grpc  ──►  ~/.cortexdb/cortexdb.db  │
        └───────────────────────┬──────────────────────┘
                    gRPC (bearer token, over Tailscale/LAN/loopback)
        ┌───────────┬───────────┼───────────┬─────────────┐
   Claude Code    Codex     Lima VM    VM on Proxmox   any client
   (mcp --remote) (--remote) (--remote)  (OpenClaw)    (Rust/Py/Node)
```

**1. On the host that owns the database**, start the server (bind to a private address — loopback, LAN, or a Tailscale IP; never a public one):

```bash
export CORTEXDB_PATH="$HOME/.cortexdb/cortexdb.db"
export CORTEXDB_GRPC_ADDR="100.x.y.z:47821"   # Tailscale IP, or 127.0.0.1 for same-host only
export CORTEXDB_GRPC_TOKEN="$(openssl rand -hex 32)"   # keep this; every client needs it
cortexdb-grpc
```

**2. On every agent/machine**, point the MCP server at it instead of a local file:

```bash
export CORTEXDB_REMOTE="100.x.y.z:47821"
export CORTEXDB_GRPC_TOKEN="…the same token…"
```

That is the whole change — no other config. In this mode the MCP server opens no local database; it discovers the tool surface from the server at startup and proxies every call, so **all tools (current and future) work identically**, and every agent reads and writes the same memory and knowledge graph.

Notes:
- **Transport is plaintext**, so run it over loopback, a trusted LAN, or Tailscale — the token is what stops others on that network from reading the brain. Anyone with the token has full read/write access.
- **Embedder/LLM config lives on the server**, not the clients: set `CORTEXDB_EMBED_*` / `CORTEXDB_LLM_*` where `cortexdb-grpc` runs.
- **Auto-recall follows the shared brain.** The `UserPromptSubmit` hook (`--recall`) queries the remote when `CORTEXDB_REMOTE` is set, so injected memories come from the same brain the tools write to. Before this it always opened the local file — a silent failure that looks like working software: memories are injected, they are just frozen at whenever the machine switched over.
- **`--memory-html` and `--export-memory` follow the shared brain too** — they fetch every record over gRPC, so the dashboard and the Markdown export show what the tools actually write.
- The remaining one-shot modes (`--graph-html`, `--learn-path`, …) still act on a **local** database; run them on the host that owns the file, or point `CORTEXDB_PATH` at it there.
- Other languages can join the same brain directly with the Rust/Python/Node `cortexdb-client` packages.

## Slash commands

| Command | What it does |
| --- | --- |
| `/remember <text>` | Save a fact / preference / decision to the brain (`memory_save`, or `knowledge_save` for reference knowledge). |
| `/recall <query>` | Search the brain for relevant memories + knowledge (`knowledge_memory_recall`) and summarize. |
| `/cortexdb-graph` | Render the brain's knowledge graph (entities, relations, documents) to an interactive HTML page and open it. Exports to `~/.cortexdb/graph/`; backed by `cortexdb-mcp --graph-html`. |
| `/cortexdb-import-memory` | Import local agent memory (memory files + `CLAUDE.md`/`AGENTS.md`) into the brain and organize it into the graph. Backed by `cortexdb-mcp --import-agent-memory`. |
| `/cortexdb-import-code` | Turn a codebase (**any language** — you are the extractor) into a code knowledge graph in its own isolated database, then open an interactive view. Answers "who implements X / who calls Y / what depends on Z / any dependency cycle?" as graph queries. Backed by `cortexdb-mcp --import-code-graph`. |
| `/cortexdb-export-memory` | Export all memories to Markdown files (one per memory with frontmatter + a `MEMORY.md` index), mirroring Claude Code's memory layout — human-readable, diffable, backup-friendly. Backed by `cortexdb-mcp --export-memory`. |
| `/cortexdb-memory-view` | Render all memory **records** to an interactive HTML dashboard — cards grouped by scope, newest first, with importance/expiry and live search — and open it. Complements `/cortexdb-graph` (entity graph) and `/cortexdb-export-memory` (Markdown). Backed by `cortexdb-mcp --memory-html`. |
| `/cortexdb-learn` | Turn study material (physics / chemistry / math / a foreign language, **any subject** — you are the extractor) into a **prerequisite** knowledge graph, then get an ordered study plan: "what must I learn before X?", "what can I study now?", "why am I stuck?". Backed by `cortexdb-mcp --import-learning-graph` / `--learn-path` / `--learn-next` / `--learn-mastered`. |
| `/cortexdb-graph-update` | Reconcile new text against the existing graph with an LLM — **add, correct and retract** facts, not just append. Shows the model the relevant existing subgraph (found lexically, **no embedder needed**) and applies the resulting edits. Always dry-run first; deletes need `--allow-delete`. Backed by `cortexdb-mcp --graph-update`. |
| `/cortexdb-global-search` | Answer whole-corpus / thematic questions ("what are the main themes?") via GraphRAG **global search**: detect entity communities, write an LLM report per community, then map-reduce them into an answer. Needs `CORTEXDB_LLM_*`. Backed by `cortexdb-mcp --global-search`. |
| `/cortexdb-resolve-entities` | Merge duplicate/alias entities into canonical nodes (case/spacing/punctuation variants deterministically; acronyms/synonyms with `CORTEXDB_LLM_*`). Use `--dry-run` to preview. Backed by `cortexdb-mcp --resolve-entities`. |
| `/cortexdb-multi-hop` | Answer complex, multi-step questions that need chaining evidence ("who leads the team that owns X?") via **multi-hop retrieval**: retrieve → reason → retrieve, where the LLM decides each hop whether it has enough or issues a focused follow-up query. Needs `CORTEXDB_LLM_*`. Backed by `cortexdb-mcp --multi-hop`. |
| `/cortexdb-facts-asof` | Query the temporal knowledge graph "as of" a point in time: shows relation facts whose validity interval (`valid_from`..`valid_to`) contains the given RFC3339 instant (default now), optionally scoped by `--from` subject / `--type` predicate. Superseded/expired facts drop out. Backed by `cortexdb-mcp --facts-as-of`. |

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
