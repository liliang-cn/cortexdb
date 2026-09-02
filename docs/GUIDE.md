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
pkg/cortexdb   Main facade: vectors, text/RAG search, knowledge, memory, KG, ontology, tools, MCP.  ← start here
               KnowledgeMemory sits on top of it: Recall / Remember / Reflect / Consolidate / context packs.
pkg/memoryflow Agent memory workflow: transcript ingest, recall, wake-up layers, promotion.
pkg/graphflow  Corpus → extract → build → analyze → report → export (HTML).
pkg/importflow Import CSV / SQL dumps / live Postgres-MySQL into RAG + KG (DDL → graph).
pkg/connector  Privacy gate over importflow: PII masking, signed plan, reversible vault, CDC sync.
pkg/graph      Low-level RDF/SPARQL/RDFS/SHACL + property graph.
pkg/core       Storage engine (SQLite by default, PostgreSQL + pgvector by DSN), embeddings, FTS5,
               vector indexes (HNSW/IVF/Flat).
```

Supporting packages: `pkg/eval` (retrieval-quality harness), `pkg/rpcserver` (the gRPC facade behind `cmd/cortexdb-grpc`), `pkg/agentmem` + `pkg/hindsight` (a standalone SQL-backed agent memory bank with disposition-weighted reflection), `pkg/semantic-router` (embedding-based query routing), `pkg/quantization` (scalar/binary vector compression), `pkg/geo` (geospatial indexing).

## Storage backends — SQLite or PostgreSQL

SQLite is the default and the reason this library exists: one file, no service.
When one file stops being the right shape — several application servers sharing
a brain, a database your operations team already backs up and replicates — the
same brain runs on PostgreSQL + pgvector. The DSN is the whole switch:

```go
db, _ := cortexdb.Open(cortexdb.DefaultConfig("/var/lib/cortexdb/brain.db"))          // SQLite
db, _ := cortexdb.Open(cortexdb.DefaultConfig("postgres://u:pw@host:5432/cortex"))    // PostgreSQL + pgvector
```

A bare path has always meant a SQLite file, so it still does — an existing
configuration keeps working without being told any of this. Everything above the
store is unchanged: vectors, hybrid search, memory, the RDF graph, SPARQL,
ontology and the tool surface all run on either backend, and `PostgresStore`
satisfies the same `BrainStore` contract the SQLite one does.

The registry (`core.RegisterStore`) is deliberately **compile-time, not a plugin
system**. Storage is the hot path — every recall goes through it — and a process
boundary would cost an IPC round trip and serialization per search, on top of
making transactions impossible. It is the same shape as agent-go's
`RegisterMemoryStore`, so a host that already knows how to swap a memory store
there has nothing new to learn: only the DSN changes.

### What actually differs

Three things, and they are in the code rather than in a footnote:

| Query | SQLite | PostgreSQL |
| --- | --- | --- |
| non-CJK full text | FTS5 `unicode61` MATCH | `tsvector @@ plainto_tsquery('simple')` |
| CJK, 3+ characters | FTS5 trigram companion | `LIKE`, accelerated by `pg_trgm` |
| CJK, 1–2 characters | `LIKE`, unindexed | `LIKE`, unindexed |

The last row is not a PostgreSQL limitation — a two-character word produces no
trigrams on either side. Same weakness in the same place, which is what makes it
predictable. `'simple'` rather than `'english'` on purpose: `unicode61` does not
stem or drop stopwords, and a backend that quietly stemmed would return
different rows for the same query depending on where it ran.

- **pgvector will not index past 2000 dimensions.** A 4096-dimensional model
  still works and still returns correct results — as an exact scan, linear in
  table size. The store says so in its log rather than leaving you to infer it
  from a latency graph.
- **pgvector is optional.** The account CortexDB connects with may not be
  allowed to `CREATE EXTENSION`. A missing extension degrades the graph's vector
  search to the in-Go scan that was already there and says so once, rather than
  refusing to start.

### Proving it, rather than believing it

PostgreSQL coverage is opt-in and *loudly* skipped, so a green run can never be
mistaken for coverage it does not have:

```bash
docker run -d --name cortexpg -e POSTGRES_PASSWORD=cortex -e POSTGRES_DB=cortex \
  -p 127.0.0.1:43516:5432 pgvector/pgvector:pg16
CORTEXDB_TEST_POSTGRES="postgres://postgres:cortex@127.0.0.1:43516/cortex?sslmode=disable" \
  go test ./...
```

That turns on **104 PostgreSQL tests** across `pkg/core`, `pkg/graph`,
`pkg/cortexdb` and `pkg/agentmem`; without the variable the same run prints 59
explicit skips naming what is not covered. Most of them are parity tests: one
body, run against both databases, asserting they answer the same. That is the
guard that matters, because the failure mode here is silent — portable SQL that
still parses and still returns rows, just not the right ones.

### What is not a storage backend

Neo4j, Qdrant and friends are not on this list, and not because nobody got to
them. The graph here is two tables — `graph_nodes` and `graph_edges` — in the
same database as the vectors, the chunks and the agent memory, inside one
transaction boundary. SPARQL, RDFS inference and SHACL validation are
implemented on top of that SQL. Moving the graph to Neo4j would split it from
the vectors with no transaction across the two, turn GraphRAG's
retrieve-then-expand into two network round trips joined in application code,
and discard the SPARQL/RDFS/SHACL implementation for a Cypher rewrite.
`pkg/sqldialect` does not help there either: it handles four real differences
(placeholder rebinding, BLOB→BYTEA, "duplicate column" error text, JSON
accessors) and leaves the SQL text in place — it is dialect adaptation, not a
query builder, and it does not reach as far as Cypher.

If you want a graph database's query language over this data, export to it —
`knowledge_graph_export` emits N-Triples/Turtle/TriG — and keep CortexDB as the
system of record.

## KnowledgeMemory — the brain facade

`db.KnowledgeMemory()` is the highest-level API: one call fuses episodic memory, durable knowledge, and the knowledge graph, and returns a paste-ready context pack with source attribution.

```go
brain := db.KnowledgeMemory()
_, _ = brain.Remember(ctx, cortexdb.KnowledgeMemoryRememberRequest{
    Content: "Alice prefers tabs over spaces.", Scope: "user"})
rec, _ := brain.Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{
    Query: "what does Alice prefer?", EntityNames: []string{"Alice"}})
fmt.Println(rec.ContextPack.Text) // sections + memory/knowledge/chunk IDs + entities
```

- **`Recall` / `BuildContextPack`** — fused retrieval across memory, knowledge, and GraphRAG chunks. Relational answers come back as **graph facts** (`Alice —uses→ Apollo`) read from graph edges rather than lexical chunk matching, so "who uses X" is answered reliably even with no embedder. Requests accept a structured retrieval plan: `keywords`, `alternate_queries`, `entity_names`, `retrieval_mode`.
- **`Reflect` / `Consolidate`** — reflect over a recall (pluggable `KnowledgeMemoryReflector`, deterministic fallback) and write the summary back as a consolidated memory.
- **`PromoteToKnowledge`** — turn episodic memories into durable, chunked knowledge.
- **`ExpandEntityContext` / `Neighbors` / `ShortestPath`** — graph exploration around entities.
- **`extract_conversation`** — deterministic (no-LLM) extraction of entities, co-occurrence relations, and a summary from a conversation transcript or stored session; `persist` writes them into the KG and durable knowledge.

`MemorySaveRequest` can carry `entities`/`relations` inline, so an agent-written memory lands in the graph in the same call that stores it. Everything above is also exposed as `knowledge_memory_*` MCP tools.

## Composable retrieval

`db.Query` (tool: `cortex_query`) is one universal retrieval call: named **prefetch lanes** (`vector`, `lexical`, `hybrid`, `graph`) fused by **RRF / weighted RRF / DBSF**, with metadata filters, an optional score formula, and per-source rank/score debugging output.

Underneath, text search takes an `Authorize` callback — a retrieval-layer security gate (RBAC/ABAC applied to every candidate; the search widens its recall until it can return TopK *authorized* rows) — and a pluggable `Reranker` for the recall→precision second stage.

## Knowledge Graph

Embedded RDF on the same file: triples/quads, namespaces, N-Triples/Turtle/TriG I/O, a practical SPARQL subset (SELECT/ASK/CONSTRUCT/DESCRIBE, updates, OPTIONAL/UNION/MINUS/VALUES/BIND/FILTER, aggregates, subqueries, property paths `^p p|q p+ p*`), RDFS-lite materialized inference, and SHACL-lite validation.

```go
db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: triples})
res, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
    Query: `SELECT ?name WHERE { <https://example.com/alice> <https://schema.org/name> ?name }`})
```

The RDFS materializer is driven through `knowledge_graph_infer_refresh` / `_summary` / `_explain` / `_explain_match`. On the property-graph side, `ApplyInferenceRules` (tool: `apply_inference`) materializes deterministic two-hop relation compositions — `A works_on B` + `B part_of C` ⇒ `A contributes_to C` — as queryable edges with provenance.

GraphRAG entities carry provenance: `upsert_entities` records every asserting document in `source_document_ids`, and `delete_document_graph` is deletion shaped like ingest — it removes a document's chunk/document nodes, its relation edges, and the entities it alone asserted (shared entities are detached, not deleted), with a `dry_run` mode.

## Ontology

CortexDB models a Palantir-style ontology on the same file: typed object types with a mandatory primary key, link types with per-side cardinality, interfaces for polymorphic retrieval, a composable object set algebra, and governed writes through action types. Runnable end to end in [`examples/16_ontology`](examples/16_ontology).

```go
_, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{
    Schema: cortexdb.OntologySchema{
        SchemaID: "aviation",
        InterfaceTypes: []cortexdb.OntologyInterfaceType{{APIName: "Facility"}},
        ObjectTypes: []cortexdb.OntologyObjectType{{
            APIName:       "Airport",
            PrimaryKey:    "iataCode",     // mandatory: it is what gives an object identity
            TitleProperty: "facilityName",
            Implements:    []string{"Facility"},
            Properties: []cortexdb.OntologyProperty{
                {APIName: "iataCode", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
            },
        }},
    },
    Activate: true,
})
```

One schema at a time is **active**. What activation does depends on the schema's `enforcement`:

- `"strict"` (the default) validates every write: unknown object types, unknown properties, missing required values and values that do not parse are rejected. Nodes written under it are identified as `entity:<objectType>:<primaryKey>`; with no active schema the older name-derived IDs still apply.
- `"vocabulary"` keeps the schema as a shared vocabulary without gating writes: declared type spellings are canonicalized and interfaces expand for retrieval, but an entity that cannot state its primary key — the normal case for LLM extraction from prose — falls back to the name-derived ID instead of being refused, and undeclared types and link types pass through. Use this for extraction pipelines; strict enforcement would force them to choose between activating the schema and keeping their entities.

`strict_actions` and `enforcement: "vocabulary"` are mutually exclusive — one closes the generic write path, the other promises never to.

**Object types** carry `api_name`, `display_name`, `plural_display_name`, `description`, `status`, `visibility`, `primary_key` (required), `title_property`, `implements` and typed `properties`. Data types: string, integer, long, double, decimal, boolean, date, timestamp, geopoint, geoshape, vector, array, struct, marking. A property may be marked `searchable` (routed into FTS5) or `vectorized`. `shared_properties` lets one definition be declared once and reused by name across object types and interfaces.

**Link types** are bidirectional, with two sides that each carry their own `api_name` and a `cardinality` of `ONE` or `MANY`. A one-to-many link is one `ONE` side and one `MANY` side; only the `ONE` side may name a `foreign_key_property`.

**Interfaces** give polymorphism: an object set or `find_nodes` query against `Facility` returns every implementing object type. Interfaces may extend other interfaces, an object type may implement several, and inheritance cycles are rejected at save time. An interface may not share a name with an object type — names resolve case-insensitively in one namespace, so `Gateway` the interface and `Gateway` the object type would be one ambiguous lookup; `SaveOntologySchema` rejects the collision at save time.

**Object sets** compose retrieval — vector search, full-text search and graph traversal as peers in one expression rather than three APIs:

```go
resolved, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
    ObjectSet: cortexdb.ObjectSet{
        Kind:     cortexdb.ObjectSetIntersect,
        Operands: []cortexdb.ObjectSet{largeFacilities, airportsNearLondon},
    },
})
```

Kinds: `base`, `interface_base`, `static`, `reference` (a saved set on the schema), `filter`, `search_around`, `union`, `intersect`, `subtract`. Filter predicates: `eq`, `lt`, `lte`, `gt`, `gte`, `in`, `is_null`, `contains`, `starts_with`, `contains_all_terms`, `contains_any_term`, `nearest_neighbors`, and the boolean operators `and`, `or`, `not`. At most three chained `search_around` hops, matching Foundry's limit.

**Action types** are governed, auditable writes: typed parameters, edit rules (`create_object`, `modify_object`, `create_or_modify_object`, `delete_object`, `create_link`, `delete_link`), and submission criteria. Set `validate_only` to check parameters and criteria without writing, or `return_edits` to get the graph edits back — the two are mutually exclusive. Validation never consults the graph, so it cannot report a primary-key collision. Every applied action is recorded in an audit trail. Setting `strict_actions: true` on the schema closes the generic upsert tools, making actions the only write path.

**Typed tools** turn the schema into an agent-callable surface — one tool per action type, optionally one list tool per object type, with real JSON Schema types instead of a free-text blob:

```go
tools, err := db.GenerateOntologyTools(ctx, cortexdb.OntologyToolGenOptions{IncludeObjectTypes: true})
```

The result is capped (32 by default) and is deliberately **not** registered with `NewMCPServer`. OSDK 1.x grew generated code with the ontology; here the same growth would land on the agent's context window on every request, so exposing these is the caller's explicit decision.

**Schema diff** answers what applying a new version would invalidate, before it is applied:

```go
diff, err := db.DiffOntologySchema(ctx, cortexdb.OntologyDiffRequest{SchemaID: "aviation", Candidate: candidate})
```

Breaking: a removed object or link type, a removed property, a changed property data type, a property that became required, a new required property, a changed primary key, a retargeted link side, and a cardinality tightened from `MANY` to `ONE`. Non-breaking additions and relaxations are reported too, flagged as safe. Both sides are expanded through their shared properties first, so retyping a shared property is visible.

Tools: `ontology_save`, `ontology_get`, `ontology_list`, `ontology_delete`, `ontology_diff`, `ontology_action_list`, `ontology_action_apply`, `object_set_resolve`.

### Current limitations

- **`vectorized` is declarative only.** The flag is stored and validated, but no write path embeds those properties. `upsert_entities` writes a lexical FNV hash vector into the node regardless of whether an embedder is configured, so a `nearest_neighbors` predicate over a *text* query compares across two different vector spaces. Object-set vector predicates are meaningful today only when you pass an explicit query `vector`.
- **An active ontology constrains `SaveKnowledge`.** It always runs its built-in heuristic extractor, whose entities are untyped, and write-path validation rejects them. If you want both, declare a catch-all `entity` object type (primary key `name`) and a `related_to` link type in the schema.
- **`modify_object` does not rewrite the node's display title.** Changing the title property through a modify rule updates the property but leaves the stored title, so name-based endpoint resolution still finds the pre-rename name.
- **Deliberately not modelled:** Foundry's function runtime, branches and proposals, dynamic row-level security, and backing datasources. Those need a platform CortexDB is not trying to be.

## Tools, MCP & Plugin

```go
tools := db.GraphRAGTools()                             // in-process tool calling
server := db.NewMCPServer(cortexdb.MCPServerOptions{})  // MCP server
```

Tool groups (60+ tools, same names in-process and over MCP): GraphRAG (`ingest_document`, `search_text`, `build_context`, `expand_graph`, `find_nodes`, `delete_document_graph`), unified retrieval (`cortex_query`), knowledge/memory (`knowledge_save`, `memory_search`, …), KnowledgeMemory (`knowledge_memory_recall`, `_reflect`, `_consolidate`, `extract_conversation`), KG (`knowledge_graph_query`, `_shacl_validate`, `apply_inference`), ontology (`ontology_save`, `ontology_action_apply`, `object_set_resolve`), and maintenance (`vector_dimension_repair`). The MCP server adds `render_graph_html`, an interactive knowledge-graph view. `memoryflow`/`graphflow`/`importflow`/`connector` expose their own toolboxes too.

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
same brain the tools write to, as do `--memory-html` and `--export-memory`.

Transport is plaintext by design — run it over loopback, a trusted LAN, or
Tailscale. **The token is the access control**: anyone holding it has full
read/write access. Embedder and LLM settings live on the server, not the
clients. `--graph-html` reads the shared brain too; the remaining one-shot modes
(`--export-memory`, `--learn-path`) still act on a local database.

Running it by hand is fine for a trial; to keep it up, [`deploy/`](../deploy/)
has a hardened systemd unit, a container image whose healthcheck is the server
binary itself (`cortexdb-grpc -health`), and a compose file — plus the backup,
upgrade and port-override notes that go with them. Every port has a default and
every default is overridable: `CORTEXDB_GRPC_ADDR` (or `-addr`) moves the
server, `CORTEXDB_LIVE_PORT` moves the live graph view.

The graph view is also an MCP tool, `render_graph_html`. It is the one tool that
is **not** proxied to the shared brain: the graph is read remotely, but the HTML
is rendered and written where the MCP server runs, because the caller needs the
file on its own filesystem to open or attach it — a server-side render would
land it on the brain's host, out of reach of whatever asked. Set
`CORTEXDB_VIEW_DIR` to choose where renders go.

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

`examples/01_core` … `16_ontology` are small and architecture-oriented (`go run ./examples/01_core`); 01-07/09/15/16 run standalone, others need an LLM/embeddings/live DB — see [examples/README.md](examples/README.md).

An embedded local-first AI memory/KG library — not a drop-in replacement for Fuseki/GraphDB/Stardog. One file, Go APIs, tool/MCP surfaces, and enough RDF/SPARQL/RDFS/SHACL to build real memory workflows.
