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

Planning a launch or community post? See [docs/LAUNCH_KIT.md](LAUNCH_KIT.md) for ready-to-edit Show HN, Reddit, and demo scripts.

## Feature reference

- **KnowledgeMemory brain facade** — `Recall` / `Remember` / `Reflect` / `Consolidate` / `PromoteToKnowledge` / context packs; fused retrieval across episodic memory, durable knowledge, and GraphRAG chunks; relational answers returned as **graph facts** (`Alice —uses→ Apollo`) read from edges, reliable even with no embedder; deterministic no-LLM `extract_conversation`; memories can carry inline entities/relations so one call stores and graphs them.
- **Composable retrieval** — `cortex_query`: vector / lexical / hybrid / graph prefetch lanes fused by RRF, weighted RRF, or DBSF, with metadata filters and per-source score debugging; an `Authorize` callback gates every candidate (RBAC/ABAC at the retrieval layer); pluggable reranker.
- **Vector + lexical engine** — FTS5, HNSW / IVF / Flat indexes, scalar and product quantization, and `IndexTypeBinary`: 1-bit codes (1/32 of the memory) searched by Hamming distance, then the shortlist rescored exactly — recall@10 0.979–0.996 on real 768-d embeddings at the default 8× oversample, ~110× faster than a flat scan at 100k vectors; on PostgreSQL it uses pgvector's `binary_quantize`. For small devices, `cortexdb.EmbeddedConfig` holds the HNSW index as SQ8 codes, bounds SQLite's caches and connections (`core.ResourceConfig`) and saves the index snapshot periodically, written and read in 4 MiB chunks: 100k 768-d vectors open from the snapshot in 0.28 s with a 193 MB heap and a 297 MB peak RSS. Geospatial indexing, semantic query routing.
- **External retrieval lanes** — a search cluster you already run (Meilisearch, Weaviate, …) can be one fused lane via `QuerySource`, without becoming the storage: it names candidate ids, the brain still owns the content, and a stale id is dropped rather than fabricated.
- **The knowledge contract** — every record can say *how it knows* and *how sure*: `_source`, `_chunk`, `_producer`, and a `_grade` from a closed set — `verified` (a named person kept it), `self_consistent` (derived from something that stated it), `asserted` (a model or a person said so, unchecked), `held`, `refused` (the vocabulary declined it, with why). `contract_tally` answers what the whole shelf stands on, counting the untagged rows too; `contract_needs_attention` lists what wants a person — including the `possiblySame` links entity resolution writes when two names *might* be one entity, instead of merging on a guess; `fact_provenance` cites the text a fact came from; `verify_claims` checks (subject, relation, object) triples against the graph — supported, contradicted (a single-valued link holds another value, the interval ended, or a `_contradicts` record denies it) or absent — each verdict with its provenance, no model involved. Producers call `ValidateContract` before they write. [alchemy](https://github.com/liliang-cn/alchemy) writes the contract for every graph it loads here — the one sink of its six that does.
- **Swappable storage** — SQLite by default; a `postgres://` DSN moves the same brain to **PostgreSQL + pgvector**, with vectors, hybrid search, memory and the RDF graph all running on either. Compile-time backend registry, not a plugin system (storage is the hot path). 104 opt-in PostgreSQL tests, mostly parity: one test body, both databases, same answer required.
- **Knowledge graph** — RDF triples/quads on the same file, **and the property graph readable as RDF**: everything extraction and `upsert_entities` write answers SPARQL, inference and SHACL as read-only triples (`cxn:` nodes, `cxt:` types, `cxr:` relations, `cxp:` properties) with no copy to keep in sync. **RDF 1.2**: triple terms `<<( s p o )>>`, reified triples and `{| |}` annotations, so a statement about a fact — who said it, how sure — is itself queryable; base-directed literals. SPARQL 1.1 and 1.2 — queries, updates with graph management (`ADD`/`COPY`/`MOVE`/`CLEAR`/`DROP`), every property path, results as JSON/XML/CSV/TSV, and `SERVICE` through a handler the application sets — passing the W3C test suites on both backends: RDF 1.2 and RDF/XML in full, SPARQL 1.2 269/269, SPARQL 1.1 query 329/331 (the two are a deliberate leniency: `"1" + "2"` is 3), update 157/157, results formats and federation in full. Semi-naive materialized inference over RDFS and the OWL 2 RL rules — `inverseOf`, symmetric, transitive, `equivalentClass`/`equivalentProperty`, `sameAs`, **keys** (`FunctionalProperty` / `InverseFunctionalProperty` / `hasKey` derive `sameAs`: the same e-mail is the same person), **property chains**, and class expressions (`someValuesFrom`, `allValuesFrom`, `hasValue`, `intersectionOf`, `unionOf`, `oneOf`, max cardinality) — kept current **automatically** as facts change (delete-and-rederive, asynchronous, `WaitForInference` to read your own writes), every inferred triple explainable. Contradictions (`disjointWith` and `AllDisjointClasses`, disjoint properties, two values of a functional property, `differentFrom` and `AllDifferent` against a derived `sameAs`, irreflexive and asymmetric properties, negative property assertions, `complementOf`, `owl:Nothing`, a cardinality of 0) are reported with the conflicting triples, never resolved by guessing. **SHACL Core in full** — every property path, qualified value shapes, `sh:lessThan`, implicit class targets; the W3C Core suite passes 98/98 — and **SHACL-SPARQL** (`sh:sparql` constraints and SPARQL-based constraint components, with pre-binding; 21 of 22, the optional `$shapesGraph` aside), and **SHACL-AF rules** (`sh:TripleRule` with conditions, order and node expressions, run to a fixpoint, results explainable). **Read-only Cypher / GQL** over the property graph (`graph_cypher_query`: MATCH / OPTIONAL MATCH / WITH / variable-length paths up to 6 hops / aggregation; 0 wrong answers across 3,897 openCypher TCK scenarios — what it cannot do, it refuses). N-Triples/N-Quads/Turtle/TriG/JSON-LD I/O and RDF/XML import (how most OWL ontologies are published), where JSON-LD never fetches a remote context; property-graph `apply_inference` materializes two-hop relation compositions with provenance; entities track asserting documents, and `delete_document_graph` is deletion shaped like ingest.
- **Ontology (Palantir-style)** — typed object/link/interface types with primary keys and cardinality, an object-set algebra (union / intersect / filter / `search_around`), governed **action types** with audit trail, generated typed agent tools, and a breaking-change schema diff; `strict` or `vocabulary` enforcement.
- **Pipelines** — `memoryflow` (transcript → recall → wake-up → promotion), `graphflow` (corpus → graph → HTML report), `importflow` (CSV / SQL dumps / live Postgres-MySQL → RAG + KG), `connector` (PII masking, signed plans, reversible vault, CDC sync).
- **Tools & MCP** — 100+ tools with the same names in-process and over MCP, plus `render_graph_html`, an interactive graph view, `serve_graph_3d`, a live 3D view you can search across the whole store, ask, query with Cypher and expand node by node (read-only, phone-friendly, every view a shareable link: `?focus=`, `?ask=`, `?cypher=`) or walk as a library of memories shelved by project, `import_agent_memory`, which brings in what Claude Code and Codex already remember (memory notes, `CLAUDE.md` / `AGENTS.md`, Codex's memories, and past sessions distilled on request), and paged bulk listings (`memory_list_all`, `graph_list_all`) — `graph_list_all` defaults to the most-connected core; `order: "id"` walks the whole graph instead, returning `next_cursor` page by page like `memory_list_all` does.
- **Questions about the whole store** — retrieval says what is relevant to a query; these say what is there. A *range* search returns everything within a distance of the query and nothing outside it, which is the honest answer when a top-K would invent a K (`search_vector_range`, and the response says when a cap hid matches). `Aggregate` counts, sums and groups by a metadata field (`aggregate_metadata`). And `VectorAggregate` reduces the vectors themselves — centroid, geometric median, and the **medoid**, the stored record closest to all the others in its group, which answers "which of these near-duplicates is canonical" and "what is this cluster about" arithmetically, with no model in the loop (`representative_records`). All identical on both backends, with one test body per behaviour run against each.
- **The graph describing itself** — `graph_schema` reports the *observed* schema (which node and edge types exist, which type pairs each edge really connects, which property keys each type carries) and `graph_property_values` reports what values a key actually takes, because a filter written from a key's name — `color == "black"` against rows that say `BLK` — returns nothing and reads as a fact. A declared ontology is opt-in and absent on exactly the graphs whose shape nobody knows; this is measured from the rows, and comes with a rendered form meant to be pasted into a prompt. Alongside them `rank_graph_nodes` (what the brain is structurally about — cached with its computation time and recomputed only when the graph changed), `graph_statistics` (a component count far above 1 means entities were written and never linked), `graph_health` (growth spikes per producer, hub nodes in the degree tail, supersession churn per day, and single-valued links holding two values at once) and `predict_graph_edges` (a missing fact, or one entity stored twice).
- **Whole-corpus questions** — `global_search` is GraphRAG global search as a tool the caller chooses: `build_community_hierarchy` runs Leiden — every community connected at every level, which Louvain does not guarantee — with every level kept and writes community reports bottom-up, and `global_search` map-reduces over the reports of the `level` asked for. Works with a model, and without one (deterministic reports, lexically ranked, unsynthesised).
- **Retrieval that walks the graph** — `retrieval_mode: "ppr"` runs Personalized PageRank (HippoRAG 2 style) from the entities a question names, rank-fused with the lexical or hybrid first stage. Without an embedder, `auto` takes that walk whenever the question names an entity specific enough to start from — not one most passages mention, like a conversation's speaker — and stays lexical otherwise. `SaveKnowledge` builds the entity graph it walks with or without an embedder, and a title that reads as a name links every chunk of its document. Recall@5 through `SaveKnowledge`/`SearchKnowledge`, lexical → `auto`, no embedder (`cortexdb-bench`): 2WikiMultiHopQA 0.657 → 0.807, MuSiQue 0.457 → 0.542, LoCoMo 0.493 → 0.495, LongMemEval 0.861 → 0.862; p95 under 30 ms. `graph` mode fuses lexical, entity-linked and expanded chunks by rank, so an exact lexical match is never buried under graph neighbours.
- **A change feed** — every committed write to nodes, edges, triples, memories, knowledge and ontology schemas is appended to `change_log` in the same transaction: in commit order, exactly once, nothing from a rolled-back transaction. Read it by cursor (`changes_since`, `db.Changes`) or subscribe in-process; 7 days / 500k events retained by default. It is what keeps inference current, and what a trigger or sync builds on.
- **Disambiguation against the graph** — `disambiguate_mentions` resolves an ambiguous name using the names given with it: shortest paths between their candidates, each rendered as a sentence with its edge ids. The deterministic four steps are the library's; the choice is the caller's, so it works with no model at all and can always say why. A mention nothing connects to is unresolved, never the nearest string.
- **Path retrieval for multi-hop questions** — `search_paths` walks the graph between the entities a question names and returns the chains of facts that connect them, scored by length and by relation type, every edge citing the chunk it came from; `return_paths` adds them to a GraphRAG query. `relation_policies` (per relation type: weight and max depth) on `search_paths`, `expand_graph` and `HybridSearch` stops a `co_occurs_with` edge counting as much as `works_at`. On a 10-question multi-hop fixture with look-alike distractors, full-evidence hit rate at 5 chunks went from 0.50 (chunk retrieval) to 0.80, distractor share from 0.36–0.43 to 0.20.
- **A hit that arrives whole** — `chunk_window` returns each hit's neighbouring chunks as context, marked as context, never as matches. For the failure chunking guarantees: a real retrieval returned a hit beginning `ance.` — *importance*, cut at a boundary.
- **Quality, measured** — `pkg/eval` runs a labeled query set through the real retrieval path with recall@k / nDCG regression floors in CI; FTS5 / SPARQL / SQL-dump parsers are fuzz-tested.

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

Supporting packages: `pkg/eval` (retrieval-quality harness), `pkg/rpcserver` (the gRPC facade behind `cmd/cortexdb-grpc`), `pkg/agentmem` + `pkg/hindsight` (a standalone SQL-backed agent memory bank with disposition-weighted reflection), `pkg/semantic-router` (query routing: embedding-based, sparse/lexical, or hybrid), `pkg/quantization` (scalar/binary vector compression), `pkg/geo` (geospatial indexing).

## Storage backends — SQLite or PostgreSQL

SQLite is the default and the reason this library exists: one file, no service.
When one file stops being the right shape — several application servers sharing
a brain, a database your operations team already backs up and replicates — the
same brain runs on PostgreSQL + pgvector. The DSN is the whole switch:

```go
db, _ := cortexdb.Open(cortexdb.DefaultConfig("/var/lib/cortexdb/brain.db"))          // SQLite
db, _ = cortexdb.Open(cortexdb.DefaultConfig("postgres://u:pw@host:5432/cortex"))     // or PostgreSQL + pgvector
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

That turns on the PostgreSQL tests in `pkg/core`, `pkg/graph`, `pkg/cortexdb`,
`pkg/agentmem`, `pkg/graphflow`, `pkg/sqldialect` and `internal/pgtest`; without
the variable the same run skips them explicitly, naming what is not covered.
(`pkg/connector`'s PostgreSQL and MySQL source/CDC tests take their own DSNs,
`CONNECTOR_PG_DSN`, `CONNECTOR_PG_REPL_DSN` and the `CONNECTOR_MYSQL_*` ones.)
Most of them are parity tests: one
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
`knowledge_graph_export` emits N-Triples/N-Quads/Turtle/TriG/JSON-LD — and keep CortexDB as the
system of record.

### The seam that does fit: external retrieval lanes

A search engine you already run — Meilisearch, Weaviate, anything that can rank
text — cannot be a brain, but it does have recall this one lacks. That is a
different question from storage, and it has its own interface:

```go
type QuerySource interface {
    Name() string
    Search(ctx context.Context, req QuerySourceRequest) ([]QuerySourceHit, error)
}

db, _ := cortexdb.Open(cfg, cortexdb.WithQuerySource(mySource))
resp, _ := db.Query(ctx, cortexdb.QueryRequest{
    Query: "rollbak",
    Prefetch: []cortexdb.QueryPrefetch{
        {Name: "lexical", Kind: cortexdb.QueryPrefetchLexical, Query: "rollbak"},
        {Kind: cortexdb.QueryPrefetchSource, Source: "meilisearch"},
    },
})
```

A source returns **ids and scores**. Content, metadata, collection and doc id
come from the brain, and three things follow from that, each of them a test:

- A source cannot inject text CortexDB never stored.
- An id the brain does not have is dropped, not fabricated into a result. Stale
  external indexes are the normal case, not an exception.
- The lane's candidates still pass the request's filter and still go through the
  same fusion as the built-in lanes, carrying the lane's name in `source_scores`.

A lane that errors fails the query rather than quietly shrinking the result set —
a retrieval path that disappears silently changes what a search means without
saying so.

`examples/17_query_source` implements this against a real Meilisearch using only
`net/http` — no SDK enters `pkg/`, the same rule that keeps LLM clients out. It
also shows the honest reason to do it: a typo, `rollbak`, which FTS5 correctly
does not match and a typo-tolerant engine does.

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

`MemorySaveRequest` can carry `entities`/`relations` inline, so an agent-written memory lands in the graph in the same call that stores it. Everything above is also exposed as MCP tools: the `knowledge_memory_*` family plus `extract_conversation`.

## Composable retrieval

`db.Query` (tool: `cortex_query`) is one universal retrieval call: named **prefetch lanes** (`vector`, `lexical`, `hybrid`, `graph`) fused by **RRF / weighted RRF / DBSF**, with metadata filters, an optional score formula, and per-source rank/score debugging output.

Underneath, text search takes an `Authorize` callback — a retrieval-layer security gate (RBAC/ABAC applied to every candidate; the search widens its recall until it can return TopK *authorized* rows) — and a pluggable `Reranker` for the recall→precision second stage.

## The knowledge contract — how a record says how it knows

A brain shared by several producers — an extraction pipeline, an agent's
memory tools, a schema import — used to answer "how do I know this" in each
producer's own words, which is the same as not answering. The contract is one
vocabulary, carried in the metadata every record already has, and it is what
makes an answer from this store auditable rather than merely confident.

Keys, all under the `_` prefix (`pkg/cortexdb/contract.go` is the executable
form; the normative text is `docs/superpowers/specs/2026-09-05-knowledge-contract-design.md`):

| key | meaning |
| --- | --- |
| `_source` | where it came from — a URL, a file, a job, an engagement. Never a DSN or a path with credentials: the store is shared and a source string is read by everyone who can read the record |
| `_chunk` | the chunk index within the source, `-1` when the producer did not chunk |
| `_producer` | how it was made: `llm-extract`, `ddl`, `tabular`, `graph-import`, `human`, `measured`, `compiled` |
| `_grade` | by what kind of thing its truth is established — the closed set below |
| `_state`, `_why` | the producer's own word for where the record stands, and the reason a refusal carries |
| `_by`, `_at` | who asserted it and when, required when `_producer` is `human` |
| `_contradicts` | ids of the records this one cannot both-be-true with, written on both |

The grades, weakest last:

- **`verified`** — a named person's review kept it. Outranks a violation: a
  reviewer who kept a record with the complaint in front of them made a
  decision, and grading it `refused` would let a vocabulary overrule the human
  it was escalated to.
- **`self_consistent`** — derived deterministically from something that already
  stated it (a `CREATE TABLE`, an existing graph, a table's own rows) and
  checked against nothing outside itself.
- **`asserted`** — a model or a person said so and nobody checked. The default
  for a producer that says nothing, because arriving unchecked is the safe
  direction to be wrong in.
- **`held`** — waiting on a person.
- **`refused`** — the vocabulary declined it; `_why` says which rule.

Producers call `ValidateContract(meta)` before writing; a record that would
fail it is refused at write, not discovered in a database browser later.

Reading it back is three questions, each a method and a tool:

```go
tally, _ := db.ContractTally(ctx)              // contract_tally
rows, _  := db.NeedsAttention(ctx, 20)         // contract_needs_attention
prov, _  := db.FactProvenanceFor(ctx, edgeID, true) // fact_provenance, with the cited text
```

A generated answer can be checked against the same records before it is
repeated. `VerifyClaims` (tool `verify_claims`) takes (subject, relation,
object) triples by name or id and returns, for each, `supported`,
`contradicted` or `absent` with the edges behind the verdict and their
provenance. Contradicted means a single-valued link (the ontology gives it a
ONE side, or the caller lists it in `SingleValued`) currently holds another
value, the fact's `valid_to` has passed, or a current edge names it in
`_contradicts`. Deterministic; no model.

```go
res, _ := db.VerifyClaims(ctx, []cortexdb.Claim{{Subject: "Leo", Relation: "lives_in", Object: "Beijing"}},
	cortexdb.VerifyClaimsOptions{SingleValued: []string{"born_in"}})
// res.Verdicts[0].Status == "contradicted", Reason == "single_valued_conflict"
```

`CheckAgainstSources` (tool `check_against_sources`) checks prose instead of
triples: each sentence names the documents or chunks it cites, and the call
lists the facts of the sentence (numbers, and names or acronyms written in
Latin script) that none of those sources contains. A number is found only
whole, a name only as a word of the source (APIs holds API; metadata does
not hold Meta); case and accents are folded, and `equivalents` lets a
caller say that "48 kHz" also stands for "48kHz". Han, kana and hangul runs
are not checked. Deterministic; no model.

`GraphHealth` (tool `graph_health`) is the shelf-wide counterpart: growth per
producer with spike detection, the high in/out-degree tail, supersessions
(closed facts, replaced versions, retractions) per day, and temporal
invariants — a single-valued link with overlapping intervals, or an interval
that ends before it starts. Each check reports whether it fired.

`ContractTally` counts nodes and edges alike — a graph's assertions are mostly
edges, and a tally over nodes alone would report a shelf far better established
than it is — and it reports `untagged` beside the five grades. On a shelf that
predates the contract, or that one producer writes and another does not, that
is the largest number, and a chart drawn without it describes 3% of the data
while looking like all of it.

`alchemy` writes the contract for every graph it loads into CortexDB, grading
each record from its provenance and the ontology's findings, and writing
`_contradicts` from the conflicts a reviewer resolved. It is the one of its six
sinks that does; the other five store the graph and lose the reason.

### Memories say how they are known

A lesson is worth sharing only when a reader can tell what was checked from
what was a guess. A memory carries the contract's grade too: `memory_save`,
`knowledge_memory_remember` and `memory_update` take `grade` and `grade_why`
(stored as `_grade` / `_why`, so they can also arrive in `metadata`).

- `verified` — something outside the writer checked it: a test that ran, a
  measurement, a reproduced bug, a person. Needs `grade_why` naming the check.
- `asserted` — said, not checked: a suspected root cause, an untested fix.
- `self_consistent`, `held`, `refused` as for any record (`held` and
  `refused` need `grade_why`).
- No grade is **untagged**: nobody said. It is kept apart from `asserted`, so
  a store written before grades existed reads as it always did.

Recall returns each memory's `grade` and `grade_why`, and so do the explain
record and the recall log. `min_grade` on `memory_search` and
`knowledge_memory_recall` leaves out what is known less well (verified >
self_consistent > asserted = untagged > held > refused; on recall it applies
to documents' `_grade` too) — `min_grade: "verified"` when the answer will be
acted on. The context pack sets guesses apart: memories graded `asserted` or
`held` go to an `unverified_memories` section titled "Unverified — said, not
checked", and a verified memory's line names its check. The auto-recall
block marks a guess first in its bracket (`UNVERIFIED (asserted)`) and a
checked one by its evidence (`verified: …`).

Auto-capture grades what it writes: `verified` only when the transcript shows
the check, `asserted` for a guess (and the content says so), nothing for a
preference or a decision, which records what someone said or chose. A guess
later confirmed is saved as a new verified memory that `supersedes` it, so the
history keeps both. `--export-memory` writes `grade` and `grade_why` into each
file, `--sync-memory` reads them back, and `--export-memory <dir> --min-grade
verified` exports only what was checked, for sharing lessons without the
guesses.

## Knowledge Graph

Embedded RDF on the same file: triples/quads, namespaces, N-Triples/N-Quads/Turtle/TriG/JSON-LD I/O and RDF/XML import (`RDFFormatRDFXML`), SPARQL 1.1 and 1.2 (SELECT/ASK/CONSTRUCT/DESCRIBE, FROM/FROM NAMED, updates and graph management, OPTIONAL/UNION/MINUS/VALUES/BIND/FILTER, the function library and XSD casts, aggregates, subqueries, every property path `^p p/q p|q p+ p* p? !(p|^q)`, `SERVICE`), materialized RDFS + OWL 2 RL inference, and SHACL Core and SHACL-SPARQL validation.

Conformance is measured against the W3C test suites, on SQLite and PostgreSQL alike: the RDF 1.2 syntax suites (N-Triples, N-Quads, Turtle, TriG, RDF/XML) and RDF 1.1 RDF/XML pass in full; SPARQL 1.2 269/269; SPARQL 1.1 query 329/331, update 157/157, CSV/TSV/JSON results 10/10, federated query 10/10; SHACL Core 98/98 and SHACL-SPARQL 21/22. The two SPARQL failures are a deliberate leniency — a plain string that looks like a number takes part in arithmetic, because data written through the non-SPARQL APIs commonly stores numbers that way — and the SHACL one is `$shapesGraph`, which the spec makes optional. A representative subset of each suite runs in CI; the full suites run with `CORTEXDB_W3C_TESTS` (a w3c/rdf-tests checkout) and `CORTEXDB_SHACL_TESTS` (data-shapes-test-suite/tests of w3c/data-shapes) set.

`SPARQLResult.WriteResults` writes a result as SPARQL JSON, XML, CSV or TSV. `SERVICE` never reaches the network on its own: `GraphStore.SetSPARQLServiceHandler` decides which endpoints exist, and `HTTPSPARQLService` is a SPARQL 1.1 Protocol client to use as, or inside, that handler; without one, `SERVICE` fails and `SERVICE SILENT` is skipped. `LOAD` is refused for the same reason — read the document and use `ImportRDF`.

The property graph — what extraction, `upsert_entities` and `upsert_relations` write — is part of it. `FindTriples`, and so SPARQL, inference and SHACL, also return the triples it implies, read-only, in graph `<urn:cortexdb:graph:property>`: node `X` is `cxn:X`, its type `T` is `cxn:X a cxt:T`, an edge of type `R` is `cxr:R`, a property `k` is `cxp:k`, and a node's `name` (or `title`) is its `rdfs:label`. Ids are percent-encoded exactly (`entity:abc` → `cxn:entity%3Aabc`). Nothing is copied, so nothing goes stale; `GraphStore.SetPropertyGraphProjection(false)` turns it off, and writes to it are refused.

```sparql
SELECT ?who WHERE { ?x cxr:depends_on ?y . ?y rdfs:label "CortexDB" . ?x rdfs:label ?who }
```

Inference is semi-naive (each round joins only what is new) and covers `rdfs:subClassOf`, `rdfs:subPropertyOf`, `rdfs:domain`, `rdfs:range`, `owl:inverseOf`, `owl:SymmetricProperty`, `owl:TransitiveProperty`, `owl:equivalentClass`, `owl:equivalentProperty` and `owl:sameAs`. Declared over the projection, these fix what LLM extraction gets wrong: `cxt:host owl:equivalentClass cxt:Host` unifies type spellings, `cxr:depends_on owl:inverseOf cxr:depended_on_by` answers the reverse question, and `owl:sameAs` merges an entity stored under two names. A `sameAs` class above a cap (default 32) is reported, not materialized.

Keys (`owl:FunctionalProperty` / `owl:InverseFunctionalProperty` / `owl:hasKey`) and max-cardinality-1 restrictions derive `sameAs`, and `owl:propertyChainAxiom` derives chains. Restrictions (`owl:someValuesFrom`, `owl:allValuesFrom`, `owl:hasValue` over `owl:onProperty`), `owl:intersectionOf`, `owl:unionOf` and `owl:oneOf` classify individuals, and the schema rules relate restrictions to each other. Contradictions — `owl:disjointWith`, `owl:AllDisjointClasses`, `owl:propertyDisjointWith`, `owl:AllDisjointProperties`, two values of a functional property, `owl:differentFrom` or `owl:AllDifferent` against a derived `sameAs`, irreflexive and asymmetric properties, negative property assertions, `owl:complementOf`, `owl:Nothing`, a max cardinality of 0 — are reported with their triples, never resolved. The rules left out are the ones that state the obvious about every term (`x sameAs x`, every class a subclass of itself and of `owl:Thing`), RDFS-only schema restatements whose instance consequences RDFS already derives, `sameAs` rewriting predicates, and the datatype rules. Inference is maintained automatically from the change feed (delete-and-rederive, asynchronous; `db.WaitForInference` reads your own writes; `WithAutoInference(false)` turns it off), and SHACL-AF `sh:TripleRule` rules run through `knowledge_graph_shacl_rules`.

RDF 1.2 triple terms (`<<( s p o )>>`, reifiers, `{| |}` annotations) and SPARQL 1.2 make statements about a fact queryable. `graph_cypher_query` answers read-only openCypher / GQL over the property graph.

Node properties are JSON, so a filter on one reads every node unless the property is indexed. `GraphStore.IndexNodeProperty(ctx, "run_id")` adds an expression index (opt-in per key, paid for on every write; `NodePropertyIndexes`, `DropNodePropertyIndex`). Equality and `IN` filters on an indexed key — `GraphFilter.Properties`, and Cypher `WHERE s.run_id = $run` or `{run_id: $run}` — become index lookups, and Cypher starts its joins from them as it does from `id(n) = …`; on SQLite numeric ranges (`s.latency_ms > 3000`) use the index too. A node needs no vector: structural nodes (an agent's execution steps, runs) are written without one and never take part in vector search. `examples/19_execution_graph` puts these together into an agent's execution record.

The execution graph has an API of its own. `db.StartRun` opens a run (an `AgentRun` node); `BeginStep` writes a step as running, with `TRIGGERED` edges from the steps whose output it consumes, before the work happens, and `EndStep` rewrites it with the output or error, confidence, latency (measured from the begin call if not given), tokens and cost — `RecordStep` does both for a step logged after the fact — and `FinishRun` closes the run. A step's kind (`LLMCall`, `ToolCall`, `Retrieval`, `DecisionPoint`, `Validation`, or one of your own) is its node type, so Cypher reads `(l:LLMCall)-[:TRIGGERED]->(t:ToolCall)` directly. `SummarizeRun` adds a run up (steps by kind and status, open and failed steps, tokens, cost, the critical path, the least confident step), `StepLineage` walks upstream to everything an output depended on or downstream to everything it fed, and `ReplayRun` reads the run as it stood at any instant from the bitemporal history. A step of one run can start another (`RunStart.ParentStep`, a `SPAWNED` edge) for sub-agents. The same nine operations are MCP tools, `execution_run_start` … `execution_run_replay`.

Retrieval: `retrieval_mode: "ppr"` is Personalized PageRank from the entities a question names, rank-fused with the first stage; without an embedder `auto` uses it when the question names an entity. Every committed write is also appended to a change feed (`changes_since`, `db.Changes`).

```go
db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: triples})
res, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
    Query: `SELECT ?name WHERE { <https://example.com/alice> <https://schema.org/name> ?name }`})
```

The RDFS materializer is driven through `knowledge_graph_infer_refresh` / `_summary` / `_explain` / `_explain_match`. On the property-graph side, `ApplyInferenceRules` (tool: `apply_inference`) materializes deterministic two-hop relation compositions — `A works_on B` + `B part_of C` ⇒ `A contributes_to C` — as queryable edges with provenance.

GraphRAG entities carry provenance: `upsert_entities` records every asserting document in `source_document_ids`, and `delete_document_graph` is deletion shaped like ingest — it removes a document's chunk/document nodes, its relation edges, and the entities it alone asserted (shared entities are detached, not deleted), with a `dry_run` mode.

## Ontology

CortexDB models a Palantir-style ontology on the same file: typed object types with a mandatory primary key, link types with per-side cardinality, interfaces for polymorphic retrieval, a composable object set algebra, and governed writes through action types. Runnable end to end in [`examples/16_ontology`](../examples/16_ontology).

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

- `"strict"` (the default) validates every write a caller makes: unknown object types, unknown properties, missing required values and values that do not parse are rejected. It does not gate the library's own bookkeeping — what the built-in extractor produces is untyped and exempt, so an ontology does not make `SaveKnowledge` unusable. That exemption keys on the type name, so an extractor you supply yourself can also write a bare name-keyed node. Nodes written under it are identified as `entity:<objectType>:<primaryKey>`; with no active schema the older name-derived IDs still apply.
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

Tools: `ontology_save`, `ontology_get`, `ontology_list`, `ontology_delete`, `ontology_diff`, `ontology_draft`, `ontology_action_list`, `ontology_action_apply`, `object_set_resolve`.

### Current limitations

- **`vectorized` needs an embedder to mean anything.** With one configured, an object whose type declares a vectorized property gets its node vector from the embedding of that text and `nearest_neighbors` answers by meaning. Without one the node keeps a lexical hash vector, and a `nearest_neighbors` predicate over a *text* query then compares across two different vector spaces — pass an explicit query `vector` in that case, or configure an embedder.
- **A property is removed only by deleting the object.** An upsert updates the properties it names and leaves the rest alone — which is what stops a document that merely mentions an object from erasing it — so omitting a property does not clear it. Use `delete_entities` and write it again. A property an ontology revision no longer declares is refused on input but survives on rows written before the change.
- **`modify_object` does not rewrite the node's display title.** Changing the title property through a modify rule updates the property but leaves the stored title, so name-based endpoint resolution still finds the pre-rename name.
- **Deliberately not modelled:** Foundry's function runtime, branches and proposals, dynamic row-level security, and backing datasources. Those need a platform CortexDB is not trying to be.

## Tools, MCP & Plugin

```go
tools := db.GraphRAGTools()                             // in-process tool calling
server := db.NewMCPServer(cortexdb.MCPServerOptions{})  // MCP server
```

Tool groups (100+ tools, same names in-process and over MCP): GraphRAG (`ingest_document`, `search_text`, `build_context`, `expand_graph`, `search_paths`, `find_nodes`, `delete_document_graph`), unified retrieval (`cortex_query`), knowledge/memory (`knowledge_save`, `memory_search`, …), KnowledgeMemory (`knowledge_memory_recall`, `_reflect`, `_consolidate`, `extract_conversation`), KG (`knowledge_graph_query`, `_shacl_validate`, `apply_inference`), ontology (`ontology_save`, `ontology_action_apply`, `object_set_resolve`), and maintenance (`vector_dimension_repair`, `sweep_expired`). The `cortexdb-mcp-stdio` binary adds `render_graph_html` (an interactive knowledge-graph view), `serve_graph_3d`, `side_graph_*`, `import_agent_memory`, and in local mode `global_search` / `build_community_hierarchy`; `db.NewMCPServer` serves the facade's tools only. `memoryflow`/`graphflow`/`importflow`/`connector` expose their own toolboxes too.

### Expiry and the sweep

`ingest_document`, `knowledge_save` and `knowledge_update` take `expires_at`, an
RFC 3339 instant. Past it, the document is left out of every search, every chunk
tool, the graph walks and the aggregates; it stays readable by id
(`knowledge_get`). Absent, the field keeps the stored expiry, so a sync job that
re-ingests from the source does not lift a retention rule; empty, it removes it.
The engine keeps it under its own metadata key, `_expires_at`; a metadata key
named `expires_at` is yours and is never read.

Memories expire through `ttl_seconds`, as before. Nothing removed expired rows
until now: `sweep_expired` (tool), `DB.SweepExpired` (library) and
`cortexdb-mcp-stdio --sweep-expired [--dry-run] [--json]` delete every expired
document (record, chunks, graph nodes) and every expired memory, each checked
again just before its delete, so a row renewed after the listing is kept. With
`CORTEXDB_REMOTE` set, the command sweeps the shared brain through the tool.
`sweep_expired` is a write tool: any read-write key can run it.

If the store used the metadata keys `_expires_at` or `_review_status` before
upgrading, run `--sweep-expired --dry-run` before the first real sweep to see
what it would delete.

### Review status and the review gate

`ingest_document`, `knowledge_save`, `knowledge_update`, `memory_save` and
`memory_update` (and `knowledge_memory_remember`, which takes the request of
`memory_save`) take `review_status` (`proposed`, `accepted` or `rejected`),
with `reviewed_by` and `reviewed_at` on an acceptance or a rejection. When the
server runs with keys, the reviewer is the calling key, and a `reviewed_by` that
names someone else is refused; a proposal takes neither field. A write that
does not decide keeps the stored decision. The engine keeps it under its own
keys, `_review_status`, `_reviewed_by` and `_reviewed_at`, on the record only; a
metadata key named `review_status` is yours and is never read.

With `CORTEXDB_REVIEW_GATE=1` on the server (or on `cortexdb-mcp-stdio`), a
proposed or rejected document or memory is left out of every search and recall,
every chunk tool, the aggregates and the graph walks from a node
(`knowledge_memory_neighbors`, `knowledge_memory_expand_entity_context`,
`expand_graph`, `get_nodes`, `search_paths`, `knowledge_memory_shortest_path`).
The tools that read the stored graph as it is (`find_nodes`,
`disambiguate_mentions`, `predict_graph_edges`, `rank_graph_nodes`,
`graph_list_all`, `graph_cypher_query`) still serve it, as they serve an expired
row until the sweep. Without the gate, the decision is stored and nothing is
hidden. The gate is a curation filter, not access
control: `knowledge_get` and `memory_get` still read a hidden row by id, any
writer can save `accepted`, and `plan.filters.review_status` lists the documents
of a status to any reader (memories ignore that filter). A curator lists what
waits with `aggregate_metadata` filtered on `_review_status`, which reads the
decision on the record. A filter on `accepted` takes the documents with no
status too, in a plan and on the aggregates. Under the gate, a proposed or
rejected memory is not promoted to knowledge, and a conversation summary leaves
it out. The other writes the engine makes by itself (consolidation, conversation
extraction, imports) carry no status.

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
Tailscale, or publish it as `https://host` behind a TLS-terminating proxy.
With a single token, **the token is the access control**: anyone holding it has
full read/write access. For finer control, give the server a key file
(`CORTEXDB_KEY_FILE` or `-keys`): each key is `read-only` or `read-write` and can
be confined to user ids, memory scopes, namespaces and collections. The server
reads the file again when it changes, so a key added or removed applies without
a restart; a change that does not load leaves the keys in force and is logged.
Embedder and LLM settings live on the server, not the clients. `--graph-html` and
`--export-memory` read the shared brain too; `--learn-path` still acts on a
local database.

Running it by hand is fine for a trial; to keep it up, [`deploy/`](../deploy/)
has a hardened systemd unit, a container image whose healthcheck is the server
binary itself (`cortexdb-grpc -health`), and a compose file — plus the backup,
upgrade and port-override notes that go with them. Every port has a default and
every default is overridable: `CORTEXDB_GRPC_ADDR` (or `-addr`) moves the
server, `CORTEXDB_LIVE_PORT` moves the live graph view.

The graph view is also an MCP tool, `render_graph_html`. It is one of the few
tools that are **not** proxied to the shared brain — with `serve_graph_3d`,
`side_graph_*` and `import_agent_memory`, it runs where the MCP server runs. The
graph is read remotely, but the HTML is rendered and written locally, because the caller needs the
file on its own filesystem to open or attach it — a server-side render would
land it on the brain's host, out of reach of whatever asked. Set
`CORTEXDB_VIEW_DIR` to choose where renders go.

### Observability — what the brain did, and why

Every call carries a **request id**, from the MCP server through gRPC to the
server's trace record, and back: in `x-request-id` (gRPC metadata and the REST
header), in an MCP tool result's `_meta` as `cortexdb/request_id`, and in the
text of a failed tool result. A client may send its own (`x-request-id`, up to
64 letters, digits, `.`, `_`, `:`, `-`); otherwise the first server to see the
call mints one. The Node and Python clients send one per call and put it on
errors (`err.requestId`, `e.request_id`); the Rust crate reads it from the
response or status metadata. When a reply is not JSON, the plugins and the
`cortexdb-live` mod report what arrived — its size and first bytes, and the
request id — instead of the parser's message.

The trace (`CORTEXDB_TRACE=1`, or slow calls past `CORTEXDB_SLOW_MS`) is
structured: `log/slog`, logfmt by default or JSON with `CORTEXDB_LOG_FORMAT=json`,
`CORTEXDB_LOG_LEVEL` for the level. Each record has the request id, key, tool,
the client's host and project, the time per stage and the reply size in bytes.
`CORTEXDB_TRACE_FILE` keeps the same records as JSON lines, cut at
`CORTEXDB_TRACE_MAX_MB` (100) with `CORTEXDB_TRACE_KEEP` (5) old files kept for
at most `CORTEXDB_TRACE_MAX_AGE_DAYS` (30).

A recall explains itself. Its trace record — and its response, when it is
asked with `explain: true` — carries the plan (query, keywords, entities, mode,
and `planner`: how the caller made the plan, e.g. `fallback: no answer from the
planner`), each retriever's top candidates with their raw scores (vector,
keyword, graph), the fused list, what each floor or filter dropped and why
(`memory_semantic_floor`, `memory_keyword_floor`, `keyword_one_word`,
`knowledge_vector_floor`, `knowledge_keyword_floor`, `expired`, `superseded`,
`unreviewed`), and the final hits. A degraded answer says so in the response:
`degraded: ["embedder_unreachable"]` when the query could not be embedded and
the answer is keyword-only (also `vector_search_failed`, `keyword_search_failed`).

With `CORTEXDB_HTTP_ADDR`, `/metrics` adds:

| Metric | What it answers |
| --- | --- |
| `cortexdb_embedder_up`, `cortexdb_embedder_requests_total{outcome,kind}`, `cortexdb_embedder_request_duration_seconds`, `cortexdb_embedder_probe_duration_seconds` | Is the embedder reachable, and how fast |
| `cortexdb_recall_degraded_total{reason}` | Fallbacks to keyword search (`embedder_unreachable`) and other partial answers |
| `cortexdb_recalls_total{tool,key}`, `cortexdb_recalls_empty_total{tool,key}` | Recalls per tool and key, and the ones that returned nothing |
| `cortexdb_recall_floor_dropped_total{floor}` | Candidates each relevance floor removed |
| `cortexdb_grpc_response_bytes{grpc_method}` | Reply sizes, before an MCP host starts truncating them |

`/healthz` reports the process and the embedder: `ok`, or `degraded` while the
embedder fails (200 either way — the brain still answers by keyword — or 503
with `?strict=1`). The server probes the embedder every
`CORTEXDB_EMBED_PROBE_SECONDS` (60; `0` off), so an embedding host that went to
sleep shows up without anybody recalling.

**The recall log** is opt-in, because it stores queries:
`CORTEXDB_RECALL_LOG=1` on the server (or on `cortexdb-mcp-stdio` for a local
brain), kept `CORTEXDB_RECALL_LOG_DAYS` (30) and at most
`CORTEXDB_RECALL_LOG_MAX_ROWS` (100000) recalls. It is a table in the database
file. Each recall is kept with who asked (key, host, project, agent), latency,
its hits with how each was found, score, writer and source, and its explain
record. The tools `recall_log_list`, `recall_log_get`, `recall_log_stats`,
`recall_feedback` and `recall_feedback_export` read it, mark a hit as useful or
noise, and export the marks as recall-eval golden cases (the JSONL format of
`--recall-eval`). A key with a scope sees and marks only its own recalls.

The live view reads it at `/recalls`: a timeline of recalls with their explain
records and a useful/noise mark per hit, a per-record view (most recalled,
never recalled, the queries that reached a record), and a `golden.jsonl`
download. On the graph page, **Colour by → Recalls** (`?color=recalls`) shades
nodes by how often recalls reached them, and `?replay=<recall id>` lights the
nodes one recall returned.

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
openclaw plugins install npm:cortexdb-openclaw-memory@2.57.2
openclaw config set plugins.slots.memory cortexdb-memory
openclaw gateway restart

# Hermes Agent
hermes plugins install liliang-cn/hermes-cortexdb-memory --enable
hermes config set memory.provider cortexdb
hermes gateway restart
```

Run `cortexdb-grpc`, then install the adapter. The existing
[`skills/`](../skills/) remain useful for explicit tool instructions and helper
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

`examples/01_core` … `19_execution_graph` are small and architecture-oriented (`go run ./examples/01_core`); 01-07/09/15/16/18/19 run standalone, others need an LLM/embeddings/live DB — see [examples/README.md](../examples/README.md).

An embedded local-first AI memory/KG library — not a drop-in replacement for Fuseki/GraphDB/Stardog. One file, Go APIs, tool/MCP surfaces, and enough RDF/SPARQL/RDFS/SHACL to build real memory workflows.
