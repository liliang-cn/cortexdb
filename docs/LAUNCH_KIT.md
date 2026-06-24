# CortexDB Launch Kit

*Last updated: 2026-06-24*

This is the copy bank for launching CortexDB in developer communities. Keep the tone concrete, technical, and demo-led: one file, pure Go, local-first AI memory, knowledge graph, MCP tools, and no required vector database service.

## Core Positioning

**One-liner:** CortexDB is a pure-Go, single-file AI memory and knowledge graph library for local-first agents.

**Longer hook:** SQLite is the kernel: one file holds vectors, lexical/RAG search, scoped agent memory, RDF/SPARQL/RDFS/SHACL knowledge graph data, and MCP tools. It runs without an embedder in lexical mode, or with any OpenAI-compatible embeddings endpoint when semantic search is needed.

**Best launch angle:** CortexDB already works as cross-session memory storage for Claude Code and Codex: durable local memory plus a queryable knowledge graph, without running a separate vector DB, graph DB, or MCP service stack.

**What to show first:**

- `go get github.com/liliang-cn/cortexdb/v2`
- A 10-line Go snippet that saves/searches knowledge in lexical mode.
- Plugin install commands for Claude Code and Codex.
- A 30-second demo of import -> desensitize -> query -> graph reasoning.

## Show HN

### Title Options

1. `Show HN: CortexDB - a single-file AI memory and knowledge graph for Go agents`
2. `Show HN: CortexDB - cross-session memory for Claude Code/Codex in one file`
3. `Show HN: I built a pure-Go SQLite-backed memory + knowledge graph for AI agents`

Use option 2 if the launch is centered on the Claude Code/Codex plugin and cross-session memory. Use option 1 for the broader Go-agent launch.

### Post Body

Hi HN,

I built CortexDB, a pure-Go embedded library that I am now using as cross-session memory storage for Claude Code and Codex.

The idea is simple: instead of running a vector database, a graph database, and a separate MCP service, keep the agent's durable memory in one SQLite file. That file can hold vectors, lexical/RAG search, scoped agent memory, RDF/SPARQL/RDFS/SHACL graph data, and MCP tools.

It works in two modes:

- no embedder required: lexical retrieval with SQLite/FTS-style search
- semantic mode: bring any OpenAI-compatible embeddings endpoint

The part I am most excited about is the Claude Code/Codex plugin. It packages the MCP server and skill so an agent can save/search memory, query the knowledge graph, and build context from a local project database across sessions. For Claude Code, there is also an optional auto-recall hook that injects relevant stored memories into prompts when a real CortexDB file exists.

Useful links:

- Repo: https://github.com/liliang-cn/cortexdb
- Plugin docs: https://github.com/liliang-cn/cortexdb/tree/main/plugins/cortexdb
- Examples: https://github.com/liliang-cn/cortexdb/tree/main/examples

Things it is not trying to be: a distributed vector database, a full enterprise RDF server, or a hosted memory API. The target is embedded agent infrastructure: one file, inspectable storage, Go APIs, and enough graph semantics to build real workflows.

I would love feedback on the API shape, the plugin install flow, and which demo would make the value clearest.

### First Comment

Some implementation details that may be interesting:

- The top-level `pkg/cortexdb` facade exposes vectors, text/RAG search, knowledge, memory, KG, tools, and MCP.
- `pkg/graph` includes RDF triples/quads, import/export, a practical SPARQL subset, RDFS-lite materialized inference, and SHACL-lite validation.
- `pkg/memoryflow` handles transcript ingest, recall, wake-up layers, and promotion for agent memory.
- `pkg/importflow` and `pkg/connector` can import CSV/SQL/live Postgres/MySQL data into RAG + KG with PII masking and CDC sync paths.
- `cortexdb-grpc` exposes the facade to Rust, Python, and Node clients.

The easiest thing to try is the lexical path because it needs no API key:

```bash
go get github.com/liliang-cn/cortexdb/v2
go run ./examples/02_rag
```

For agent-tool use:

```bash
/plugin marketplace add liliang-cn/cortexdb
/plugin install cortexdb@cortexdb
```

or in Codex:

```bash
codex plugin marketplace add liliang-cn/cortexdb
codex plugin install cortexdb@cortexdb
```

## Reddit: r/LocalLLaMA

### Title

`I built a local-first AI memory + knowledge graph in one SQLite file`

### Body

I have been working on CortexDB, a pure-Go embedded memory/KG library for local agents.

The pitch for this community: it gives an agent durable local memory without requiring a separate vector DB service. In lexical mode it needs no embeddings API at all. If you do want semantic retrieval, it can use any OpenAI-compatible embeddings endpoint, including local providers.

What is in the same SQLite file:

- vector collections
- lexical/RAG search
- scoped agent memory
- RDF/SPARQL/RDFS/SHACL knowledge graph data
- MCP tools for agent access

The most practical path is the Claude Code/Codex plugin: install it, point it at a project-local CortexDB file, and the agent can save/search memory and query graph facts through MCP tools.

Repo: https://github.com/liliang-cn/cortexdb

I am looking for feedback from people running local agents: would you rather see a demo around personal coding memory, project documentation Q&A, or importing a small operational database into a graph-backed support brain?

## Reddit: r/golang

### Title

`CortexDB: pure-Go embedded AI memory and knowledge graph on SQLite`

### Body

I built CortexDB, a Go library for embedding AI memory, RAG search, and a knowledge graph in a single SQLite-backed file.

It is meant for Go developers building local-first agents or copilots who do not want to run a separate vector database, graph database, and MCP server stack.

Highlights:

- pure Go public API
- one local database file
- lexical mode works without an embedder/API key
- optional OpenAI-compatible embeddings
- RDF triples/quads, SPARQL subset, RDFS-lite inference, SHACL-lite validation
- tool/MCP surfaces for agent integrations
- gRPC sidecar plus Rust/Python/Node clients

Repo: https://github.com/liliang-cn/cortexdb

The API I want feedback on most is the top-level facade in `pkg/cortexdb`: whether it feels idiomatic enough for Go while still covering memory, RAG, and KG workflows.

## Reddit: r/ClaudeAI

### Title

`I made a Claude Code plugin for local memory + a queryable knowledge graph`

### Body

I built a CortexDB plugin for Claude Code and Codex.

It packages a local MCP server plus a shared skill, so Claude/Codex can save/search project memory, query a knowledge graph, and build context from a single SQLite-backed database file. It runs in lexical mode by default, so there is no API key required.

The Claude Code version also has an optional auto-recall hook: when a real CortexDB file exists in the project, it can search for relevant memories on each prompt and inject a small bounded set into context.

Install:

```bash
/plugin marketplace add liliang-cn/cortexdb
/plugin install cortexdb@cortexdb
```

Repo/plugin docs: https://github.com/liliang-cn/cortexdb/tree/main/plugins/cortexdb

I would love feedback on whether this should feel more like "project memory", "agent knowledge graph", or "local RAG for coding agents" in the docs.

## 30-Second Demo Script

**Goal:** Make the value obvious without asking viewers to understand the whole architecture.

1. Open with the problem: an agent forgets project facts unless they are pasted into context.
2. Install the plugin for Claude Code or Codex.
3. Save two or three project facts into CortexDB through MCP tools.
4. Query them back with lexical search, no API key.
5. Add graph facts, then run a SPARQL query that returns a relationship the agent can cite.
6. Show the `.db` file in the project directory.
7. Close with the line: "Local agent memory and a queryable knowledge graph, in one SQLite file."

## Launch Sequence

1. Tighten README first 200 lines and add a demo GIF.
2. Publish Show HN with the broad single-file AI memory/KG angle.
3. Reply to every technical comment for the first 12 hours.
4. Two or three days later, post the Claude Code/Codex plugin angle to r/ClaudeAI.
5. Post the local-first/no-vector-DB angle to r/LocalLLaMA.
6. Post the Go API angle to r/golang after the README has concrete Go snippets and examples.
7. Turn the best questions into README FAQ entries and comparison docs.

## Launch Checklist

- [ ] README has the positioning table above the install section.
- [ ] README includes a short demo GIF or terminal recording.
- [ ] `examples/02_rag` and `examples/06_tools_mcp` run cleanly from a fresh clone.
- [ ] Plugin install instructions work for Claude Code and Codex.
- [ ] A short pinned issue or discussion exists for launch feedback.
- [ ] The first HN comment includes implementation details and honest non-goals.
- [ ] Reddit posts are spaced out, not cross-posted all at once.
- [ ] Follow-up README changes are made from real community questions.
