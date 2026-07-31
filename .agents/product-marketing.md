# Product Marketing Context

*Last updated: 2026-06-24*

## Product Overview

**One-liner:** CortexDB is a pure-Go, single-file AI memory and knowledge graph library for local-first agents.

**What it does:** CortexDB stores vectors, lexical/RAG search data, scoped agent memory, RDF/SPARQL/RDFS/SHACL graph data, and MCP tools in one SQLite-backed file. It can already serve as cross-session memory storage for Claude Code and Codex. It can run without an embedder in lexical mode, or use any OpenAI-compatible embeddings endpoint for semantic retrieval. It also ships a Claude Code/Codex plugin and a gRPC sidecar for Rust, Python, and Node clients.

**Product category:** Embedded agent memory, local-first RAG, knowledge graph, MCP tooling, Go developer infrastructure.

**Product type:** Open-source developer library and agent infrastructure.

**Business model:** Open-source project. No paid plan defined in the repository.

## Target Audience

**Target companies:** Developer-tool builders, AI agent teams, local-first app builders, internal platform teams, and solo hackers building coding agents or copilots.

**Decision-makers:** Founders, staff/principal engineers, AI infrastructure engineers, Go developers, agent framework maintainers, and technical developer advocates.

**Primary use case:** Give Claude Code, Codex, or a custom AI agent durable cross-session local memory and graph-aware retrieval without deploying a separate vector database, graph database, or MCP service stack.

**Jobs to be done:**

- Add cross-session project memory to Claude Code, Codex, or a custom agent.
- Build local RAG over documents, transcripts, structured data, or operational data.
- Represent extracted facts as a queryable knowledge graph.
- Import and desensitize existing data before making it available to an agent.

**Use cases:**

- Coding-agent memory over project decisions and facts.
- Local-first RAG inside a Go application.
- Knowledge graph extraction and QA over a repo or corpus.
- Support/copilot memory over Postgres/MySQL data with PII masking.
- Hybrid vector + lexical + graph retrieval without multiple services.

## Personas

| Persona | Cares about | Challenge | Value we promise |
| --- | --- | --- | --- |
| Go agent builder | Simple embedded APIs, reliability, local development | Does not want to operate a separate vector DB or graph DB | One Go library and one SQLite file for memory, RAG, KG, and tools |
| Claude Code/Codex power user | Project memory that survives sessions | Agents forget context unless it is pasted repeatedly | Plugin + MCP tools for durable cross-session memory and graph queries |
| AI infrastructure engineer | Control, privacy, auditability | Hosted memory APIs and opaque vector stores are hard to inspect | Local file, explicit tools, lexical mode, and graph queries |
| Data-heavy prototype builder | Bootstrapping from existing data | Operational data contains PII and lives across databases | Importflow/connector paths for mapping, masking, and CDC |

## Problems & Pain Points

**Core problem:** Agents need durable memory and structured facts, but common setups require stitching together a vector database, graph database, prompt memory layer, and tool server.

**Why alternatives fall short:**

- Embedded vector stores are simple but usually stop at vector search.
- Raw SQLite extensions are flexible but leave the RAG, memory, graph, and tool layers to the application.
- Hosted or standalone vector databases add operational weight for local-first agents.
- Full graph databases are powerful but heavy when the goal is agent memory.
- Custom memory tables often lack recall policy, graph semantics, and agent-tool surfaces.

**What it costs them:** More services to run, more glue code, harder local setup, less inspectable memory, and repeated context loss across agent sessions.

**Emotional tension:** Developers want agent memory to feel dependable, but many memory stacks feel either too toy-like or too operationally heavy.

## Competitive Landscape

**Direct:** Embedded vector/memory libraries such as chromem-go and custom SQLite memory layers.

**Secondary:** sqlite-vec/raw SQLite extensions, Chroma, Qdrant, LanceDB, local vector DBs, LangChain/LlamaIndex memory or retrieval layers.

**Indirect:** Full graph databases such as Fuseki, GraphDB, Stardog, Neo4j, or building bespoke project memory into an agent.

## Differentiation

**Key differentiators:**

- Pure Go, embedded, single-file storage.
- Lexical mode works without an embedder or API key.
- Combines vector search, lexical/RAG, agent memory, RDF/SPARQL/RDFS/SHACL, and MCP tools.
- Claude Code/Codex plugin is a concrete distribution wedge and already works for cross-session memory storage.
- Importflow/connector paths support structured data, PII masking, and CDC-style workflows.
- gRPC sidecar opens the same facade to Rust, Python, and Node.

**How we do it differently:** CortexDB treats agent memory, RAG, and KG as layers over the same local SQLite file instead of separate services.

**Why that's better:** The stack is easier to run locally, easier to inspect, and easier to ship inside developer tools.

**Why customers choose us:** They want useful agent memory and graph-aware retrieval without operating multiple databases.

## Objections

| Objection | Response |
| --- | --- |
| "Why not just use a vector DB?" | Use one if you need distributed vector scale. CortexDB is for embedded local-first agent memory where vectors, lexical search, graph facts, and tools should live together. |
| "Is this a full graph database?" | No. It provides enough RDF/SPARQL/RDFS/SHACL for practical agent workflows, not a replacement for enterprise RDF servers. |
| "Do I need embeddings?" | No. Lexical mode works without an embedder or API key; embeddings are optional for semantic retrieval. |
| "Is it Go-only?" | The core is Go, but the gRPC sidecar provides Rust, Python, and Node clients. |

**Anti-persona:** Teams needing a managed multi-tenant vector database, enterprise RDF server, or massive distributed graph analytics engine.

## Switching Dynamics

**Push:** Current memory stacks require too many moving parts or lose context across sessions.

**Pull:** One file, pure Go, plugin-ready, graph-aware, and no API key required for lexical retrieval.

**Habit:** Developers already know Chroma/Qdrant/LangChain/LlamaIndex or have custom tables.

**Anxiety:** Concern that an embedded library will be too narrow, not scalable enough, or too opinionated.

## Customer Language

**How they describe the problem:**

- "My coding agent keeps forgetting project decisions."
- "I do not want to run a vector database for a local tool."
- "I need memory I can inspect and query."
- "RAG alone is not enough; I need relationships."

**How they describe us:**

- "Single-file AI memory and knowledge graph."
- "Cross-session memory for Claude Code and Codex."
- "SQLite-backed agent memory with SPARQL."

**Words to use:** pure-Go, single-file, local-first, embedded, inspectable, durable memory, knowledge graph, lexical mode, no API key, MCP tools, Claude Code, Codex.

**Words to avoid:** magical, autonomous brain, enterprise graph database replacement, hosted memory platform, drop-in vector DB replacement.

**Glossary:**

| Term | Meaning |
| --- | --- |
| lexical mode | Retrieval path that does not require embeddings |
| memoryflow | Agent memory workflow layer for transcript ingest, recall, wake-up context, and promotion |
| graphflow | Corpus-to-graph workflow for extraction, analysis, reports, and export |
| importflow | Import layer for CSV, SQL dumps, and live databases |
| connector | Privacy gate over importflow with PII masking and sync paths |
| MCP | Model Context Protocol tools exposed to agents |

## Brand Voice

**Tone:** Technical, direct, honest, practical.

**Style:** Developer-first, demo-led, specific about trade-offs.

**Personality:** Useful, local-first, transparent, systems-minded.

## Proof Points

**Metrics:** User-provided snapshot on 2026-06-24: 41 GitHub stars, 3 forks, repository created on 2025-08-07.

**Customers:** None documented.

**Testimonials:** None documented.

**Value themes:**

| Theme | Proof |
| --- | --- |
| Local-first simplicity | One SQLite-backed file and lexical mode with no API key |
| Agent-tool readiness | Claude Code/Codex plugin and MCP server |
| Graph-aware memory | RDF/SPARQL/RDFS/SHACL support in the same storage layer |
| Multi-language reach | gRPC sidecar with Rust/Python/Node clients |

## Goals

**Business goal:** Grow awareness and adoption among Go, local AI, and coding-agent communities.

**Conversion action:** Star the repo, run an example, install the Claude Code/Codex plugin, and open feedback issues/discussions.

**Current metrics:** User-provided snapshot on 2026-06-24: 41 stars and 3 forks.
