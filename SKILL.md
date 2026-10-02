---
name: cortexdb
description: Use CortexDB for local-first AI memory, vector search, RAG, knowledge graphs, SPARQL/RDFS/SHACL, corpus-to-graph workflows, external structured-data import (CSV / SQL dumps), and MCP/tool calling. Use when working with CortexDB, embeddings, memory, RAG, GraphRAG, knowledge graph, RDF, SPARQL, SHACL, data import, CSV/SQL dump import, memoryflow, graphflow, importflow, or MCP tools.
---

# CortexDB Skill

CortexDB is a pure-Go, single-file AI memory and knowledge graph library built on SQLite.

## Current Architecture

Use the right layer:

```text
pkg/cortexdb
  Main public DB facade: vectors, text search, knowledge, memory, KnowledgeMemory, KG, tools, MCP.

pkg/memoryflow
  Agent memory workflow: transcript ingest, recall, wake-up layers, diary, promotion.

pkg/graphflow
  Corpus-to-graph workflow: extraction schema, build, analyze, report, export, HTML.

pkg/importflow
  External structured-data import (CSV / MySQL-PG dumps) into RAG + knowledge-graph foundations, AI-assisted mapping optional.

pkg/graph
  Low-level graph engine: property graph, RDF triples/quads, SPARQL, RDFS, SHACL.

pkg/core
  Storage engine (SQLite by default, PostgreSQL + pgvector by DSN), embeddings, FTS5,
  vector indexes, chat/session primitives.
```

Default recommendation:

- Use `pkg/cortexdb` for application code.
- Use `pkg/memoryflow` for chat/session/agent memory workflows.
- Use `pkg/graphflow` for document/corpus-to-graph extraction and report/export workflows.
- Use `pkg/graph` only for low-level RDF/SPARQL/RDFS/SHACL or property graph control.

## API Selection Cheat Sheet

Choose by the job to be done:

| Scenario | Start with | What it really does |
| --- | --- | --- |
| Store vectors, FTS5, or run simple TopK retrieval | `pkg/cortexdb` `Quick()`; drop to `pkg/core` only if needed | Thinnest layer for pure retrieval without knowledge or memory semantics. |
| Document RAG or knowledge-base QA | `SaveKnowledge` + `SearchKnowledge` | Ingest chunks, builds retrieval artifacts, can attach entities and relations, then runs semantic or lexical GraphRAG retrieval. |
| User preferences, session memory, or long-term memory | `SaveMemory` + `SearchMemory` | Resolves a memory bucket from `scope` / `user` / `session` / `namespace`, stores memory, then uses semantic retrieval or lexical fallback. |
| Retrieve both memory and knowledge and assemble prompt context | `db.KnowledgeMemory().Recall()` or `BuildContextPack()` | Runs memory recall and knowledge recall, then fuses entities, chunks, and context into one packed response. |
| Full chat-assistant memory workflow | `pkg/memoryflow` | Turns transcripts into episodic memories, supports recall, wake-up layers, and end-of-session promotion. |
| RDF, SPARQL, RDFS, or SHACL work | Knowledge graph APIs in `pkg/cortexdb` | Standard graph surface for upsert, find, query, import, export, validation, inference, and explanation. |
| Build a graph from documents, code, or other corpora and analyze it | `pkg/graphflow` | Runs the fixed pipeline `detect -> extract -> build -> analyze -> report -> export`. |
| Import external CSV or MySQL/PG SQL dumps into RAG + knowledge graph | `pkg/importflow` `New().Run()` / `AutoImport()` | Parses a source into records, routes columns via a `MappingPlan` to RAG content and KG entities/relations, with optional AI mapping inference and triple extraction. |
| Typed objects, governed writes, or composable object sets | Ontology APIs in `pkg/cortexdb` | Activates a schema that validates every write, then exposes actions, object sets, typed tool generation and schema diffing. |
| Expose CortexDB to an agent or MCP client | `db.GraphRAGTools()` or `db.NewMCPServer()`; `importflow.NewMCPServer()` for import tools | Wraps the high-level APIs as tool definitions or an MCP server rather than introducing a second implementation path. |

## How To Choose

- If the input is a document and the goal is QA or retrieval, use `SaveKnowledge` / `SearchKnowledge`.
- If the input is dialogue, user preference, or session state, use `SaveMemory` / `SearchMemory`.
- If you do not want to hand-assemble prompt context, use `KnowledgeMemory`.
- If you need startup context or multi-layer wake-up memory for an assistant, use `memoryflow`.
- If you need standard RDF semantics, SPARQL, inference, or SHACL validation, use the knowledge graph APIs.
- If you need to transform a corpus into a graph and then analyze or export it, use `graphflow`.
- If you only need to expose the same capabilities to another agent, use tools or MCP instead of rebuilding the logic.

## Short Mental Model

- Engine layer: `pkg/core` + `pkg/graph`
- Application facade: `pkg/cortexdb`
- Higher-level workflows: `KnowledgeMemory`, `memoryflow`, `graphflow`
- Agent exposure layer: `GraphRAGTools`, MCP

## Common Compositions

- Chat assistant: `memoryflow.IngestTranscript -> memoryflow.WakeUpLayers -> memoryflow.CloseSession`
- Enterprise knowledge-base QA: `SaveKnowledge -> SearchKnowledge`
- Personal assistant memory: `SaveMemory -> KnowledgeMemory.Recall`
- Graph question answering: `UpsertKnowledgeGraph -> QueryKnowledgeGraph -> ExplainKnowledgeGraphInference`
- Codebase knowledge graph: `graphflow.Pipeline.Run`

## Source Pointers

- Architecture and API selection overview: `README.md`, `README_CN.md`
- Knowledge APIs: `pkg/cortexdb/knowledge_api.go`
- Memory APIs: `pkg/cortexdb/memory_api.go`
- GraphRAG query path: `pkg/cortexdb/graphrag.go`
- KnowledgeMemory facade: `pkg/cortexdb/brain.go`
- Memory workflow service: `pkg/memoryflow/service.go`
- Corpus-to-graph pipeline: `pkg/graphflow/pipeline.go`
- External-data import: `pkg/importflow/importer.go`, `pkg/importflow/source_dump.go`, `pkg/importflow/mcp.go`
- Tool and MCP exposure: `pkg/cortexdb/graphrag_tool_defs.go`, `pkg/cortexdb/mcp.go`

## Install

```go
import "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
```

## Core DB Usage

```go
db, err := cortexdb.Open(cortexdb.DefaultConfig("KnowledgeMemory.db"))
if err != nil {
    return err
}
defer db.Close()

quick := db.Quick()
_, _ = quick.Add(ctx, []float32{0.1, 0.2, 0.9}, "SQLite is a single-file database.")
hits, _ := quick.Search(ctx, []float32{0.1, 0.2, 0.8}, 3)
_ = hits
```

## Storage Backend (SQLite or PostgreSQL)

The DSN chooses the backend. Nothing else changes:

```go
db, _ := cortexdb.Open(cortexdb.DefaultConfig("/var/lib/cortexdb/brain.db"))        // SQLite (default)
db, _ := cortexdb.Open(cortexdb.DefaultConfig("postgres://u:pw@host:5432/cortex"))  // PostgreSQL + pgvector
```

- A bare path still means a SQLite file, so existing configuration is unaffected.
- `PostgresStore` satisfies the same `BrainStore` contract as `SQLiteStore`:
  vectors, hybrid search, memory, RDF graph, SPARQL, ontology and the tool
  surface all run on either. `db.Dialect().Kind()` reports which.
- The registry (`core.RegisterStore` / `core.OpenBrainStore`) is compile-time,
  not a plugin system — storage is the hot path, and a process boundary would
  add an IPC round trip per search and make transactions impossible.
- Real differences: lexical search maps FTS5 `unicode61` → `tsvector @@
  plainto_tsquery('simple')` and the CJK trigram companion → `LIKE` + `pg_trgm`
  (`pkg/core/fts_postgres.go` carries the table); pgvector refuses to index past
  2000 dimensions, so a wider model falls back to an exact scan and says so;
  pgvector itself is optional and a missing extension degrades to the in-Go scan.
- PostgreSQL tests are opt-in and loudly skipped:
  `CORTEXDB_TEST_POSTGRES="postgres://..." go test ./...` turns on 104 tests
  across `pkg/core`, `pkg/graph`, `pkg/cortexdb`, `pkg/agentmem`; without it the
  run prints 59 skips naming what is uncovered. Most are parity tests — one body,
  both databases, same answer required — because the failure mode is silent.
- External retrieval lanes are a *different* seam from storage:
  `cortexdb.QuerySource` (`Name()` + `Search()`) registered with
  `WithQuerySource`, asked for as `QueryPrefetch{Kind: QueryPrefetchSource,
  Source: "<name>"}`. A source returns **ids and scores only** — content is
  hydrated from the brain, ids the brain lacks are dropped, candidates still
  pass the filter and the normal fusion, and a source error fails the query
  rather than silently shrinking the result set. `db.QuerySources()` lists the
  registered ones. Adapters live outside `pkg/` (see
  `examples/17_query_source`, Meilisearch over plain `net/http`).
- **Not** storage backends: Neo4j, Qdrant and similar. The graph is
  `graph_nodes`/`graph_edges` in the same database and the same transaction as
  the vectors, with SPARQL/RDFS/SHACL implemented over that SQL. `pkg/sqldialect`
  is dialect adaptation (placeholders, BLOB→BYTEA, error text, JSON accessors),
  not a query builder — it does not reach Cypher. Export with
  `knowledge_graph_export` instead and keep CortexDB as the system of record.

## Knowledge and Memory

```go
_, _ = db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
    KnowledgeID: "apollo-plan",
    Title:       "Apollo launch plan",
    Content:     "Alice owns Apollo. Apollo ships on Friday.",
    ChunkSize:   24,
    Entities: []cortexdb.ToolEntityInput{
        {Name: "Alice", Type: "person", ChunkIDs: []string{"chunk:apollo-plan:000"}},
        {Name: "Apollo", Type: "project", ChunkIDs: []string{"chunk:apollo-plan:000"}},
    },
    Relations: []cortexdb.ToolRelationInput{
        {From: "Alice", To: "Apollo", Type: "owns"},
    },
})

resp, _ := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
    Query:         "Who owns Apollo?",
    Keywords:      []string{"Apollo", "Alice", "owns"},
    RetrievalMode: cortexdb.RetrievalModeLexical,
    TopK:          3,
})
_ = resp.Context

_, _ = db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
    MemoryID:  "style",
    UserID:    "user-1",
    Scope:     cortexdb.MemoryScopeUser,
    Namespace: "assistant",
    Content:   "User prefers concise status updates.",
})
```

No-embedder mode is supported. Use lexical retrieval plus LLM-planned `Keywords`, `AlternateQueries`, `EntityNames`, and `RetrievalMode`.

Retrieval mode `ppr` runs Personalized PageRank (HippoRAG 2 style) from the entities a question names, rank-fused with the first stage; tune it with `ppr: {fusion, damping, passage_seed_weight, edge_type_weights}`. Without an embedder, `auto` takes that walk whenever a knowledge search names an entity specific enough to start from (an entity more than 10% of passages mention, like a conversation's speaker, is not) and stays lexical otherwise — recall@5 through the public API, lexical → auto: 2WikiMultiHopQA 0.657 → 0.807, MuSiQue 0.457 → 0.542, LoCoMo 0.493 → 0.495, LongMemEval 0.861 → 0.862. `SaveKnowledge` builds the entity graph without an embedder too, and a title that reads as a name links every chunk of its document. With an embedder `auto` stays hybrid; pass `retrieval_mode: "ppr"` for multi-hop questions.

Chinese (and other CJK) questions work in lexical mode as written. A sentence such as `微信机器人为什么从来不主动给我发提醒` is cut at function words (的, 了, 为什么, 那个, …), broken into character bigrams, matched by substring and ranked with BM25, then merged beside the word-index results — for memory, `SearchKnowledge`, `SearchTextOnly` and the `search_text` tool alike, on both backends, and inside the authorization gate. A row is returned only when it matches more than one word of the question (two separate stretches, or one of four characters or more, or the whole of a one-word question) and at least 30% of its content characters, so a question the store knows nothing about still returns nothing.

## Knowledge Graph APIs

High-level APIs live in `pkg/cortexdb`:

- `UpsertKnowledgeGraph`
- `FindKnowledgeGraph`
- `DeleteKnowledgeGraph`
- `ImportKnowledgeGraph`
- `ExportKnowledgeGraph`
- `QueryKnowledgeGraph`
- `ValidateKnowledgeGraphSHACL`
- `RefreshKnowledgeGraphInference`
- `SummarizeKnowledgeGraphInference`
- `ExplainKnowledgeGraphInference`
- `ExplainKnowledgeGraphInferenceMatch`

```go
_, _ = db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
    Triples: []cortexdb.KnowledgeGraphTriple{
        {
            Subject:   graph.NewIRI("https://example.com/alice"),
            Predicate: graph.NewIRI(graph.RDFType),
            Object:    graph.NewIRI("https://example.com/Person"),
        },
    },
})

result, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
    Query: `SELECT ?o WHERE { <https://example.com/alice> ?p ?o . }`,
})
_ = result
```

SPARQL is an embedded SPARQL 1.1 subset: SELECT (DISTINCT, REDUCED, `(expr AS ?v)`), ASK, CONSTRUCT (GRAPH blocks in templates produce quads), DESCRIBE; FROM / FROM NAMED; update forms (INSERT/DELETE DATA, DELETE WHERE, DELETE…INSERT…WHERE, WITH, USING, USING NAMED); GRAPH, OPTIONAL, UNION, MINUS, VALUES, BIND, FILTER, EXISTS, NOT EXISTS, subqueries; property paths `^pred`, `p|q`, `p+`, `p*`; the SPARQL 1.1 function library (term tests, strings with character positions, numerics, dates, hashes); aggregates with DISTINCT, GROUP BY, HAVING; ORDER BY by value on expressions and aliases. A per-row type error drops the row in FILTER and leaves the variable unbound in BIND. Without FROM the default graph is the unnamed graph plus the property-graph projection. Not supported: XSD casts, `/` `?` `!` paths, BASE, SERVICE, LOAD/CLEAR/CREATE/DROP.

RDF 1.2 and SPARQL 1.2: triple terms `<<( s p o )>>` (object position only), reified triples `<< s p o ~ r >>`, `{| |}` annotations and base-directed literals (`"x"@ar--rtl`) in N-Triples / N-Quads / Turtle / TriG, and SPARQL triple-term patterns with `TRIPLE`, `isTRIPLE`, `SUBJECT`, `PREDICATE`, `OBJECT`, `LANGDIR`, `hasLANG`, `hasLANGDIR`, `STRLANGDIR`. Use them to say things about a fact — its source, its confidence — and query that back:

```sparql
INSERT DATA { _:r rdf:reifies <<( :alice :worksFor :acme )>> ; :source <doc1> ; :confidence 0.9 }
```

JSON-LD export refuses triple terms rather than flattening them.

**Cypher.** `graph_cypher_query` / `db.QueryCypher` runs a read-only openCypher / GQL subset over the property graph: MATCH, OPTIONAL MATCH, WITH, UNWIND, RETURN, UNION; labels are node types, relationship types are edge types; variable-length paths `*m..n` capped at 6 hops; aggregates, about 45 functions, `$params`. `n.name` falls back to `title`. Write clauses are refused by design, as are CALL, shortestPath, pattern predicates and temporal functions. Call `graph_schema` first.

```cypher
MATCH (p:project)-[:depends_on]->(c {name: 'CortexDB'}) RETURN p.name
```

**The property graph is readable as RDF.** Everything extraction, `upsert_entities` and `upsert_relations` write also answers SPARQL, inference and SHACL, as read-only triples in graph `<urn:cortexdb:graph:property>` — nothing is copied, so nothing goes stale:

| Property graph | Triple |
|---|---|
| node `X` | `cxn:X` (id percent-encoded exactly: `entity:abc` → `cxn:entity%3Aabc`) |
| node type `T` | `cxn:X a cxt:T` |
| edge of type `R` | `cxn:A cxr:R cxn:B` |
| scalar property `k` | `cxn:X cxp:k "value"` (JSON numbers and booleans keep their XSD type) |
| `name`, else `title` | `cxn:X rdfs:label "…"` |

```sparql
SELECT ?who WHERE { ?x cxr:depends_on ?y . ?y rdfs:label "CortexDB" . ?x rdfs:label ?who }
```

Call `graph_schema` first to learn which types and relations exist. Non-ASCII ids need the full `<urn:cortexdb:node:…>` form. Deleting or inserting projected triples is refused; change the property graph through its own APIs. `GraphStore.SetPropertyGraphProjection(false)` turns the projection off; exports leave it out.

Import and export speak N-Triples, N-Quads, Turtle, TriG and JSON-LD 1.1 (`jsonld`, also accepted as `json-ld`). JSON-LD import never fetches a remote `@context`: schema.org's is answered from memory, any other URL is refused with an error naming it, so inline the context instead.

Inference is semi-naive materialization over RDFS (`rdfs:subClassOf`, `rdfs:subPropertyOf`, `rdfs:domain`, `rdfs:range`) and an OWL-RL subset (`owl:inverseOf`, `owl:SymmetricProperty`, `owl:TransitiveProperty`, `owl:equivalentClass`, `owl:equivalentProperty`, `owl:sameAs`). Every inferred triple records its rule and supports, so `knowledge_graph_infer_explain` traces it back to explicit triples. Declared over the projection, the OWL rules fix what extraction gets wrong: `cxt:host owl:equivalentClass cxt:Host` unifies spellings, `cxr:depends_on owl:inverseOf cxr:depended_on_by` answers the reverse question, `cxn:entity%3Anode_e owl:sameAs cxn:entity%3Asds_e` merges one machine stored under two names. A sameAs class larger than `MaxSameAsClassSize` (default 32) is reported in `OversizedSameAsClasses`, never half-materialized. OWL 2 RL keys and chains too: `owl:FunctionalProperty` / `owl:InverseFunctionalProperty` derive `sameAs` (the same e-mail is the same person), and `owl:propertyChainAxiom` (an RDF list, length ≥ 2) derives the chain. Contradictions — `owl:disjointWith`, `owl:propertyDisjointWith`, two different values of a functional property, `owl:differentFrom` against a derived `sameAs` — are reported in `inconsistencies` / `inconsistency_count` with the conflicting triple ids, never resolved; a `sameAs` class contradicted by `differentFrom` is not materialized.

Inference stays current by itself (on by default; `WithAutoInference(false)` turns it off). Every committed change is read from the change feed and applied with delete-and-rederive, asynchronously, so writers pay nothing; `db.WaitForInference(ctx)` waits until your own writes' consequences are in. With no RDFS/OWL axioms declared it is dormant and costs nothing. It never touches triples a SHACL rule produced; re-run the rules after a manual full refresh, which clears them.

**Change feed.** Every committed write to nodes, edges, triples, memories, knowledge and ontology schemas is appended to `change_log` in the same transaction — in commit order, exactly once, nothing from a rolled-back transaction. Read it by cursor with `changes_since` / `db.Changes(ctx, after, limit)` (resume from the last `seq` you saw; `pruned` says the cursor fell behind retention, so rebuild from current state), or `db.SubscribeChanges` in-process. Retention defaults to 7 days or 500k events.

```go
_, _ = db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
    Query: `INSERT DATA { cxr:depends_on owl:inverseOf cxr:depended_on_by }`,
})
_, _ = db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{})
```

Incremental refresh:

```go
refresh, _ := db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{
    Mode: cortexdb.KnowledgeGraphInferenceRefreshModeIncremental,
    Triples: []cortexdb.KnowledgeGraphTriple{
        {
            Subject:   graph.NewIRI("https://example.com/Employee"),
            Predicate: graph.NewIRI("http://www.w3.org/2000/01/rdf-schema#subClassOf"),
            Object:    graph.NewIRI("https://example.com/Person"),
        },
    },
})
_ = refresh
```

SHACL supports targets `sh:targetClass` (with subclasses), `sh:targetNode`, `sh:targetSubjectsOf`, `sh:targetObjectsOf`; `sh:property` with `sh:path` (a predicate or `sh:inversePath`); `sh:class`, `sh:datatype`, `sh:nodeKind`, `sh:minCount`, `sh:maxCount`, `sh:min/maxInclusive`, `sh:min/maxExclusive`, `sh:minLength`, `sh:maxLength`, `sh:pattern` + `sh:flags`, `sh:languageIn`, `sh:uniqueLang`, `sh:in`, `sh:hasValue`, `sh:equals`, `sh:disjoint`, `sh:node`, `sh:not`, `sh:and`, `sh:or`, `sh:xone`, `sh:closed` + `sh:ignoredProperties`, `sh:severity`, `sh:message`, on node and property shapes alike. Results carry the constraint component IRI. Recursive shapes and other path forms are refused with an error; any result makes `conforms` false, whatever its severity. Over the projection it is a quality gate for extracted graphs, e.g. every `cxr:runs_on` must point at an `sh:class cxt:host`. SHACL-AF rules: `knowledge_graph_shacl_rules` / `db.ApplyKnowledgeGraphSHACLRules` runs `sh:TripleRule` — with `sh:condition`, `sh:order`, `sh:deactivated` and node expressions `sh:this`, constants, `sh:path` (incl. `sh:inversePath`), `sh:filterShape`, `sh:intersection`, `sh:union` — to a fixpoint. Results are explainable inferred triples named `shacl_triple_rule:*`, and the shapes passed are the whole rule set: a rule left out is retracted on the next run.

```go
report, _ := db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{
    Shapes: []cortexdb.KnowledgeGraphTriple{
        {Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(graph.SHACLNodeShape)},
        {Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.SHACLTargetClass), Object: graph.NewIRI("https://example.com/Person")},
    },
})
_ = report
```

### What is actually in the graph, by type

`db.Graph()` reports the graph's own vocabulary, so an ontology or drift check
never has to query `graph_nodes` / `graph_edges` directly. Raw SQL against those
tables is pinned to one schema and one dialect and fails silently when either
moves; these go through the same dialect-aware path as the rest of the package
and are covered by the SQLite/PostgreSQL parity suite.

```go
nodeTypes, _ := db.Graph().NodeTypeCounts(ctx) // map[string]int, untyped under ""
edgeTypes, _ := db.Graph().EdgeTypeCounts(ctx)

// Which type shapes the edges really have — how a declared relation is wrong.
shapes, _ := db.Graph().EdgeShapes(ctx, "uses")
// []EdgeShape{{EdgeType, FromType, ToType, Count}}

// Which nodes those edges run through — what is wrong.
pairs, _ := db.Graph().EdgeEndpointPairs(ctx, "uses")
// []EdgeEndpointPair{{EdgeType, From, To EdgeEndpoint{ID, NodeType, Content}, Count}}
```

Both edge queries take optional edge types, matched exactly as stored; passing
none reports the whole graph.

## Decision ledger

`fact_provenance` and the knowledge contract say how a *fact* is known. The
ledger says why an *action* was taken: which facts it rested on, who took it,
what it replaced, and what else was decided the same way.

A decision is a graph record, not a side table. It is a node `decision:<id>`
typed `Decision`, whose content is the note and whose properties are `kind`,
`actor`, `at`, `verdict`, `subject` plus the knowledge contract keys — so
`contract_tally` counts a decision without being told about decisions, and
`expand_graph`, `render_graph_html`, the live view and SPARQL all see it. Three
edge types explain it: `based_on` (a premise), `about` (the subject) and
`supersedes` (the decision it replaces).

```go
rec, err := db.RecordDecision(ctx, cortexdb.DecisionRecordRequest{
    Kind:     cortexdb.DecisionKindReview, // load | review | action | assert (open vocabulary)
    Actor:    "liliang",
    Note:     "Held the ledger-svc release: riskd's rule source is unverified.",
    Verdict:  "hold",
    Subject:  "svc:ledger",
    Premises: []string{"svc:riskd", "fact:ledger-depends-on-riskd"}, // node ids and edge ids
})

chain, _ := db.DecisionChain(ctx, rec.ID, 0)     // premises with each one's grade and source
prior, _ := db.Precedents(ctx, cortexdb.PrecedentsQuery{Kind: "review", Subject: "svc:ledger"})
mine, _ := db.DecisionsBy(ctx, "liliang", 20)
_, _, _ = chain, prior, err
```

It fails closed and it fails before writing anything: an empty actor, an empty
note, a premise or subject that does not exist, a supersedes target that is not
a decision, or metadata `ValidateContract` refuses. By default a decision is
stamped `_grade=verified`, `_producer=human`, `_by=<actor>` — a named actor
signed it — and an agent recording its own decision says so with `Producer` and
`Grade`.

A premise that is a **fact** is an edge, and `graph_edges` declares a foreign
key on `to_node_id`, enforced on both backends. So the `based_on` edge is
anchored on the fact's subject node and names the fact in its own
`premise_edge_id` property; `DecisionChain` resolves it back and reports the
**edge's** grade, not the anchor node's.

Tools (in-process and MCP), and the mirroring `cortexdb.v1.DecisionService`:

- `decision_record` — writes (read-only keys are refused it)
- `decision_chain` — reads
- `decision_precedents` — reads

## Declared inference rules

`apply_inference` composes two hops. That is one rule shape; the engine behind
it takes any Horn clause over graph edges — any number of premises, variables
bound across them — forward-chained to a bounded fixpoint. `apply_inference`
keeps its name and behaviour and now runs through that engine, so there is one
derivation path and one explanation for both.

```go
// Written the way a person writes it. Keywords are case-insensitive; a term
// starting with ? is a variable.
_, _ = db.SaveRules(ctx, cortexdb.RulesSaveRequest{Rules: []cortexdb.RuleDefinition{{
    ID:         "mortality",
    Text:       "IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)",
    Confidence: 0.9,
}}})

// Dry run first; with neither rule_ids nor rules, every enabled rule runs.
plan, _ := db.ApplyRules(ctx, cortexdb.RulesApplyRequest{DryRun: true})
applied, _ := db.ApplyRules(ctx, cortexdb.RulesApplyRequest{})
_ = plan

// Why is that edge there?
why, _ := db.ExplainInference(ctx, cortexdb.InferenceExplainRequest{EdgeID: applied.CreatedEdgeIDs[0]})
// why.Explanation names the rule and its text; why.Trace is the premise chain,
// flattened in preorder.
```

**Literal terms.** A term starting with `?` is a variable. Anything else is a
literal, resolved against the stored nodes in this order: an exact node id
first; otherwise `Type:Name`, matching every node whose `node_type` equals
`Type` and whose name (the `name` property, or the node content) equals `Name`,
both case-insensitively; otherwise a bare name matched the same way across all
types. A literal matching several nodes matches all of them; one matching none
is returned in `unresolved_terms`, so a rule that can never fire says so.

**Derived edges** carry `inferred=true`, `provenance=rule`, `rule_id`,
`rule_text`, the exact `support_edge_ids`, and `confidence` = the minimum
premise confidence times the rule's own. Re-running derives nothing new: an edge
already there from the same rule is reported in `unchanged_edge_ids`, not
rewritten.

**Caps.** Chaining stops at 16 rounds and 50 000 derived edges by default
(`max_iterations`, `max_derived`). Hitting either is an error —
`graph.ErrRuleCapExceeded` — and **nothing is written**, because a half-built
closure in the graph is worse than none.

Rules persist in their own `kg_rules` table, not as graph nodes: a rule is
configuration for the engine, not a fact about the world the graph describes,
and a `rule:` node would show up in `find_nodes`, `expand_graph` and every
export. The table is created the first time a rule is saved.

Tools: `rules_save`, `rules_list`, `rules_delete`, `rules_apply` (with
`dry_run`), `inference_explain`. Engine: `pkg/graph/rules*.go`; facade:
`pkg/cortexdb/rules*.go`.

## Asking about the whole store

Retrieval answers "what is relevant to this query". Three APIs answer questions
about the collection instead, and all three work identically on SQLite and
PostgreSQL.

**A threshold instead of a K.** `RangeSearchVector` / `RangeSearchText` return
every row within a distance of the query, and nothing outside it. Top-K always
returns K rows however weak the last ones are; a range query returns what
clears the bar and nothing when nothing does. That is the honest answer for
"find all the near-duplicates of this", "does the store hold anything like this
at all", and any threshold-driven decision.

```go
near, err := db.RangeSearchText(ctx, "the backup vault is offline",
    cortexdb.VectorRangeOptions{Radius: 0.15})
```

`Radius` is a distance in the store's metric, not a similarity: under the
default cosine it is `1 - similarity`, so roughly 0.1 for near-identical text
and 0.5 for loosely related. `MaxResults` 0 means every match. Tool:
`search_vector_range`, which caps at 100 by default and reports `truncated`
when the radius reached further than the cap — a trimmed range answer is
otherwise indistinguishable from a complete one, which would make it an
unlabelled top-K.

**Counting.** `Aggregate` runs count, sum, avg, min, max and group_by over a
metadata field, optionally filtered and scoped to a collection. A `group_by` is
also how to ask what values a field takes and how often. Tool:
`aggregate_metadata`.

```go
byKind, err := db.Aggregate(ctx, core.AggregationRequest{
    Type: core.AggregationGroupBy, GroupBy: []string{"kind"}, OrderBy: "count",
})
```

Metadata names in these requests are column references, not values, and cannot
be bound as parameters — so they are checked against a conservative identifier
shape and refused otherwise, rather than escaped. A name inside a JSON path may
carry dots and hyphens (`user.id`, `content-type`); one that becomes bare SQL —
`GroupBy`, which is aliased by its own text, plus `OrderBy` and the `Having`
keys — may not.

**Aggregating the vectors.** `VectorAggregate` reduces the vectors themselves:

| Kind | Returns | Use it for |
| --- | --- | --- |
| `centroid` | a computed point | the group's average direction; dragged by outliers |
| `geometric_median` | a computed point | the same idea, robust — it minimises the sum of distances rather than of squared distances, so one far member barely moves it |
| `medoid` | **a stored record** | "which of these near-duplicates is the canonical one", "give me the one record that represents this cluster" |

The medoid is the one worth reaching for: it names a row that exists, with its
text, so the answer can be read and traced rather than only measured. It
answers the consolidation question arithmetically, with no model in the loop.
Tool: `representative_records` — medoid only, because a synthetic point can only
reach a model as several hundred floats it can do nothing with.

**Facets.** `SearchWithFacets` filters a vector search by typed, nestable
metadata facets and can return the per-value counts beside the hits. It is
program-only on purpose: the filter language is a lot of schema for a model to
get right, and the facet question a model actually has — what values, how many
— is `aggregate_metadata` with a `group_by`.

## Asking the graph about itself

Retrieval says what is relevant to a query; these say what the graph *is*. All
work on both backends.

**Its observed schema.** `GraphSchema` reports which node and edge types exist
and how many of each, which node-type pairs every edge type actually connects,
and which property keys each node type carries. `GraphPropertyValues` reports
what values one key actually takes. Call both before writing a traversal or a
SPARQL query against a graph you have not inspected: a query naming a type the
graph does not have still runs, matches nothing, and reads as an answer — and
stored values are routinely codes, so a filter written from a key's *name*
(`color == "black"` against rows that say `BLK`) returns nothing and looks like
a fact. `ontology_get` answers the *declared* schema, which is opt-in and
usually absent on graphs built by extraction; this is measured from the rows.
The response carries a rendered `text` field, because this is read in a prompt.
Tools: `graph_schema`, `graph_property_values`.

**Its shape and its centres.** `GraphPageRank` ranks nodes by structural
importance — what a knowledge base is about, rather than what it says most
often — with each node's label and type beside the score. `GraphStatistics`
reports node and edge counts, average degree, density and connected components;
a component count far above 1 means entities were written and never linked.
`GraphPredictEdges` lists nodes one node is not connected to but arguably should
be, which is two findings in one shape: a missing fact, or one entity stored
twice. Read `tied_at_top` first — several unrelated nodes tied at exactly 1.0
means this graph's vectors cannot tell them apart, which is what short lexical
hashes do to short entity names. Tools: `rank_graph_nodes`, `graph_statistics`,
`predict_graph_edges`.

`rank_graph_nodes` reads a cached ranking. Scores live in a side table
(`graph_pagerank_scores`) with the time they were computed; a call on an
unchanged graph is an indexed `LIMIT` read (on a 2000-node graph: 0.11ms p50
against 16ms recomputing), and a change to the graph's nodes or edges is
detected in the database — on SQLite by rowid maxima plus triggers on deletes
and endpoint rewrites, so writes from another process count; on PostgreSQL by
a topology aggregate — and triggers a recompute. The answer carries `cached`,
`computed_at`, `stale`, `stale_reason`; `refresh` forces a recompute,
`allow_stale` serves an out-of-date ranking without paying for one,
`max_age_seconds` bounds its age. From Go: `GraphPageRankOptions.Cache`
(`PageRankCacheAuto` / `Refresh` / `AllowStale`; the zero value computes fresh
as before), `RefreshPageRankCache`, `InvalidatePageRankCache`, and
`StartPageRankRefresher(ctx, interval, …)` to keep it warm on a timer.

**Whole-corpus questions (GraphRAG global search).** `global_search` answers
what retrieval cannot — "what are the main themes", "what does this brain
cover" — by map-reducing over community reports. The caller chooses it; no
query is routed to it by its wording. `build_community_hierarchy` detects the
entity graph's communities with Leiden — every community connected at every level, which Louvain does not guarantee; `HierarchyOptions.Algorithm` selects Louvain — and keeps every level (0 = finest,
each level up merges the one below), then writes a report per community
bottom-up: level 0 from its entities and relations, higher levels from their
children's reports. `global_search` takes a `level` (default: the coarsest,
fewest reports, cheapest map) and builds the hierarchy on first use. With a
model (`graphflow.JSONGenerator`; in the MCP binary `CORTEXDB_LLM_*`) reports
and answer are the model's; without one reports are assembled
deterministically and `global_search` returns the lexically most relevant
reports unsynthesised (`mode: "no_model"`). Go: `graphflow.BuildCommunityHierarchy`,
`graphflow.GlobalSearch(…, GlobalSearchOptions{Level: &l})`,
`graph.GraphStore.HierarchicalCommunities`. Registered by
`graphflow.AddGlobalSearchMCPTools` in `cortexdb-mcp-stdio` (local mode) and in
`graphflow.NewMCPServer`.

**Disambiguating a name against the graph.** `DisambiguateMentions` resolves an
ambiguous name using the other names given with it: it finds the shortest paths
between this mention's candidates and the others', renders each as a sentence,
and ranks by that support. Steps one to four are deterministic and live here;
the choice is the caller's, so a caller with no model takes the top rank and one
with a model reads the evidence — and either way can say afterwards why. A
mention nothing connects to is `unresolved`, never the nearest string match, and
a bound that stopped the search is reported apart from an absence.

```go
res, err := db.DisambiguateMentions(ctx, []string{"Claude", "MCP", "OpenClaw"},
    cortexdb.DisambiguationOptions{NodeTypes: []string{"entity", "project", "protocol", "tool"}})
```

Pass `NodeTypes` on any brain that keeps documents and chunks as graph nodes.
Without it, one document title holding two of the mentions matches both by
substring and scores a perfect 1.0 for each — a coincidence of two words that
ties with the real entity and resolves nothing. Tool: `disambiguate_mentions`.

**Resolving duplicate entities: link first, merge later.**
`graphflow.ResolveEntities` scores each candidate pair on name similarity,
shared neighbours and type agreement (plus the model's grouping, if an LLM is
given). Only a score of 0.95 or more merges; 0.85–0.95 writes a `possiblySame`
edge carrying that evidence, graded `held`, which `contract_needs_attention`
lists. Grade the edge `verified` and the next pass merges it; `refused` keeps
the pair apart. Spelling alone never merges two differently spelled names, and
two entities with the same name and no neighbour in common (two `main.go`
files, Mercury the planet and the element) are linked, not merged.
`ResolveOptions{Mode: graphflow.ResolveMergeAll}` is the old unscored merge.

**Why a fact ended.** Every row that leaves the live graph — superseded,
retracted, merged, removed with its document — lands in
`graph_node_history`/`graph_edge_history` with `reason`, `superseded_by` and
`producer`. Read it with `db.NodeHistory` / `db.EdgeHistory`, or from the
`invalidation` on each retracted or changed row of `graph_diff`. A caller that
knows more than the default says so with `graph.WithInvalidation(ctx, …)`.

**Path retrieval for multi-hop questions.** Chunk search ranks passages by how
much they look like the question; for "how is Alice Chen connected to Kestrel?"
the passage stating the middle link ("Borealis Labs built Kestrel") does not
look like it, and one that merely shares its words does. `SearchPaths` walks
the graph between the entities the question names — bounded breadth-first
search, at most `MaxDepth` edges (default 3), scored `decay^(hops-1)` times the
product of relation weights — and returns each chain as edges that cite the
chunk they were extracted from (`chunk_ids` / `source_chunk_id` on the edge).
With one seed it returns the paths leaving it, which is the shape of "the city
of the company Alice works for". Paths never run through chunk or document
nodes unless `IncludeBookkeeping` is set: a shared chunk is a co-mention, not a
relation.

```go
resp, err := db.SearchPaths(ctx, cortexdb.ToolSearchPathsRequest{
    EntityNames:      []string{"Alice Chen", "Kestrel"},
    RelationPolicies: graph.RelationPolicies{"co_occurs_with": {Weight: 0.2, MaxDepth: 1}},
    WithText:         true,
})
// resp.Paths[i].Edges[j].ChunkIDs — the evidence, link by link; resp.ChunkIDs — all of it, in path order
```

`GraphRAGQueryOptions.ReturnPaths` (`return_paths` on `search_graphrag_lexical`)
adds the same chains to a GraphRAG result, seeded by the plan's entity names or,
without any, by the entities the top three chunks mention; chunks and context
are unchanged. Tool: `search_paths`.

**Per-relation-type weight and depth.** `graph.RelationPolicies` maps a relation
type to `{Weight, MaxDepth}` — weight multiplies a path's score (0 = 1.0,
negative = never follow), max depth is the farthest hop from the start at which
the type is still followed — with `"*"` for every unlisted type. It is accepted
by `SearchPaths`, `expand_graph` (`relation_policies`; the response then carries
a score per node and `limit` keeps the best), `Neighbors`/`ScoredNeighbors`
(`TraversalOptions.Relations`) and `HybridSearch` (`GraphFilter.Relations`,
where `GraphScore` becomes the relation-weight product over `distance+1`).
Unset, every one of them behaves exactly as before.

## Widening a hit to its neighbours

`GraphRAGQueryOptions.ChunkWindow` (and `chunk_window` on the search tools)
returns each hit's neighbouring chunks in the same document alongside it. Off by
default, and the neighbours are context rather than hits: never scored, never
reranked, never displacing a match, and tagged `+context` in the assembled text
so a reader can tell what matched from what came along.

It is for the failure chunking guarantees. A real retrieval over a book chapter
returned a hit whose first word was `ance.` — *importance*, cut at a chunk
boundary — and the question, about two steps of a process, was only answerable
at `ChunkWindow: 2`, when both steps were finally in the context. Overlapping
windows merge into one span, and the existing `MaxContextChars` budget is
respected: hits are charged first and never refused.

## Ontology

`pkg/cortexdb` models a Palantir-style ontology on the same file. One schema is
active at a time and validates every write.

```go
_, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{
    Schema: cortexdb.OntologySchema{
        SchemaID: "aviation",
        InterfaceTypes: []cortexdb.OntologyInterfaceType{{APIName: "Facility"}},
        ObjectTypes: []cortexdb.OntologyObjectType{{
            APIName:       "Airport",
            PrimaryKey:    "iataCode",   // mandatory
            TitleProperty: "facilityName",
            Implements:    []string{"Facility"},
            Properties: []cortexdb.OntologyProperty{
                {APIName: "iataCode", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
                {APIName: "facilityName", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true, Searchable: true},
            },
        }},
        LinkTypes: []cortexdb.OntologyLinkType{{
            APIName: "flightDeparture",
            A: cortexdb.OntologyLinkSide{APIName: "departures", ObjectTypeAPIName: "Airport", Cardinality: cortexdb.OntologyCardinalityMany},
            B: cortexdb.OntologyLinkSide{APIName: "origin", ObjectTypeAPIName: "Flight", Cardinality: cortexdb.OntologyCardinalityOne, ForeignKeyProperty: "originIata"},
        }},
    },
    Activate: true,
})
```

Key points:

- Every object type needs a `primary_key`. Node identity becomes `entity:<objectType>:<primaryKey>`; with no active schema the older name-derived IDs still apply.
- Link cardinality is per side (`ONE` / `MANY`); only a `ONE` side may carry a `foreign_key_property`.
- Interfaces give polymorphic retrieval — querying `Facility` returns every implementor. Multiple inheritance is allowed; cycles are rejected.
- `shared_properties` are declared once and reused by name; storage keeps them unexpanded, so read paths that care about types expand them first.

Object sets compose retrieval (`db.ResolveObjectSetObjects`): kinds `base`,
`interface_base`, `static`, `reference`, `filter`, `search_around`, `union`,
`intersect`, `subtract`; predicates `eq`/`lt`/`lte`/`gt`/`gte`/`in`/`is_null`/
`contains`/`starts_with`/`contains_all_terms`/`contains_any_term`/
`nearest_neighbors` plus `and`/`or`/`not`. Vector, full-text and graph
traversal are peers in one expression. At most 3 chained `search_around` hops.

Action types are governed, audited writes (`db.ApplyAction`): typed parameters,
edit rules, submission criteria, `validate_only` / `return_edits` (mutually
exclusive). `strict_actions: true` on the schema closes the generic upsert
tools so actions are the only write path.

Two schema-lifecycle helpers:

```go
tools, _ := db.GenerateOntologyTools(ctx, cortexdb.OntologyToolGenOptions{IncludeObjectTypes: true})
diff, _ := db.DiffOntologySchema(ctx, cortexdb.OntologyDiffRequest{SchemaID: "aviation", Candidate: candidate})
```

`GenerateOntologyTools` emits typed tool definitions from the active schema —
one per action type, optionally one list tool per object type. The result is
capped (32 by default) and is **not** registered with `NewMCPServer`: every
extra tool is paid for in the agent's context window on every request, so
exposing them is an explicit caller decision.

`DiffOntologySchema` reports what a candidate would change and flags the
breaking classes: removed object/link type, removed property, changed property
data type, property became required, new required property, changed primary
key, retargeted link side, cardinality tightened `MANY` -> `ONE`.

Writes MERGE: an upsert updates the properties it names and leaves the rest
alone, so naming an object a document merely mentions no longer erases what a
fuller write established. There is no way to remove a property by omitting it;
use `delete_entities`.

The extractor's untyped output is bookkeeping and is exempt from schema
validation, so an active strict ontology no longer blocks `SaveKnowledge` with
an embedder. A schema that itself declares an object type named `entity` keeps
its own validation. The exemption keys on the type name alone, so a CALLER-
supplied extractor can write a bare name-keyed node under strict enforcement;
the `upsert_entities` and `SaveKnowledge` entity doors are unaffected.

A property declared `vectorized` is embedded on write when an embedder is
configured: the object's node vector becomes the embedding of its vectorized
text (name first, then each vectorized property), so the `nearest_neighbors`
object-set predicate answers by meaning. With no embedder the node keeps the
lexical hash vector and the write still goes through. A search hit names the
objects its chunks mention whatever their object type, in auto mode as well as
graph mode; an explicit lexical mode or `disable_graph` still leaves the graph
untouched. Hybrid results carry each retriever's score and rank beside the
fused score, which is a reciprocal-rank value that is right to order by and
wrong to read.

Known limitation: `modify_object` does not rewrite a node's stored display
title. Foundry's
function runtime, branches/proposals, dynamic row-level security and backing
datasources are deliberately not modelled.

## MemoryFlow

Use `pkg/memoryflow` for agent memory workflows:

```go
flow, _ := memoryflow.New(db, planner, extractor)

_, _ = flow.IngestTranscript(ctx, memoryflow.IngestTranscriptRequest{
    Transcript: memoryflow.Transcript{
        SessionID: "session-1",
        UserID:    "user-1",
        Source:    "chat",
        Turns: []memoryflow.TranscriptTurn{
            {Role: "user", Content: "Apollo ships on Friday."},
            {Role: "assistant", Content: "Captured."},
        },
    },
    Scope:     cortexdb.MemoryScopeSession,
    Namespace: "assistant",
})

layers, _ := flow.WakeUpLayers(ctx, memoryflow.WakeUpLayersRequest{
    Identity: "You are the Apollo project assistant.",
    Recall: memoryflow.RecallRequest{
        Query:     "startup context",
        SessionID: "session-1",
        Scope:     cortexdb.MemoryScopeSession,
        Namespace: "assistant",
    },
})
_ = layers
```

LLM-dependent interfaces:

- `QueryPlanner`
- `SessionExtractor`
- `PromotionPolicy`

Optional Hindsight recall strategy plugin:

```go
flow, _ := memoryflow.New(
    db,
    planner,
    extractor,
    memoryflow.WithRecallStrategy(hindsight.NewStrategy(db, hindsight.StrategyOptions{
        BankID:      "apollo-agent",
        EntityNames: []string{"Apollo"},
        Keywords:    []string{"deadline"},
        UseKG:       true,
    })),
)
```

## GraphFlow

Use `pkg/graphflow` for corpus-to-graph workflows:

```go
extraction := graphflow.ExtractionResult{ /* nodes + edges */ }
_, _ = graphflow.Build(ctx, db, []graphflow.ExtractionResult{extraction}, graphflow.BuildOptions{})
analysis, _ := graphflow.Analyze(ctx, db, graphflow.AnalyzeRequest{TopN: 10})
report, _ := graphflow.RenderReport(ctx, analysis)
_, _ = graphflow.Export(ctx, db, graphflow.ExportRequest{OutputDir: "graphflow-out", Analysis: analysis, Report: report})
_, _ = graphflow.ExportHTML(ctx, db, graphflow.ExportRequest{OutputDir: "graphflow-out", Analysis: analysis})
```

LLM extraction uses only this interface:

```go
type JSONGenerator interface {
    GenerateJSON(ctx context.Context, systemPrompt string, userPrompt string) ([]byte, error)
}
```

The example `examples/05_graphflow` uses `github.com/openai/openai-go/v3` with JSON Schema structured output:

```env
OPENAI_API_KEY=...
OPENAI_BASE_URL=http://43.167.167.6:8080/v1
OPENAI_MODEL=gpt-5.4
```

## ImportFlow

Use `pkg/importflow` to import external structured data (CSV, MySQL/PostgreSQL dumps)
into RAG + knowledge-graph foundations in one pass:

```go
src, _ := importflow.NewSQLDumpSource(strings.NewReader(dump), importflow.DumpOptions{Dialect: importflow.DialectMySQL})
defer src.Close()
// or importflow.NewCSVSource(reader, importflow.CSVOptions{Table: "people"})

im := importflow.New(db)
plan := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
    "people": {
        RAG: &importflow.RAGPlan{ContentTmpl: "{name}: {bio}", IDColumn: "id"},
        KG:  &importflow.KGPlan{Entities: []importflow.EntityMap{{Ref: "p", Type: "Person", IDTmpl: "{id}", LabelTmpl: "{name}"}}},
    },
}}
rep, _ := im.Run(ctx, src, plan) // rep.RowsRead/ChunksIndexed/TriplesCreated/UnparsedStatements
```

AI is optional and injected via interfaces — `WithMappingInferer` (infers the plan from
the schema), `WithTextRefiner`, `WithExtractor` (graphflow triple extraction). With an
inferer, `im.AutoImport(ctx, src, importflow.Goal{BuildRAG: true, BuildKG: true})` does
plan + run in one step. RAG works with no embedder (lexical FTS5). The mapping inferer
reuses the same `graphflow.JSONGenerator` interface.

`importflow.MappingFromDDL(ddl, importflow.DDLMappingOptions{})` turns a `CREATE TABLE`
DDL subset (Postgres/MySQL) into a reviewable `MappingPlan` **deterministically, no LLM** —
tables→entity classes, PK→entity id, FK→relations, columns→RAG content + props (no
`ALTER`/views). `importflow.ParseDDL` returns the raw parsed tables. Review the plan, then
feed it to `im.Run` / the connector to build the graph.

`importflow.MappingFromDDLWithLLM(ctx, ddl, gen, opts)` is the LLM-enhanced sibling:
it computes that deterministic baseline, then has the `graphflow.JSONGenerator` refine it
(semantic relation names, implicit relations, free-text→`text_extract`, junction-table
collapse), with graceful fallback to the baseline on any LLM error and a deterministic
executability guard (a refined table is adopted only when its relations bind to declared
entities; dropped tables are unioned back from the baseline). It returns
`(plan, tables, llmUsed, err)`.

## Data connector (privacy / desensitization)

`pkg/connector` is a privacy gate in front of ImportFlow: connect to a live
PostgreSQL/MySQL DB (or wrap any `importflow.Source`), introspect schema, classify PII
(rule-based + optional LLM), and apply a **human-signed** `MaskingPlan` before any bulk
data moves (schema-first, data-second):

```go
src, _ := connector.NewPostgresSource(dsn, connector.SourceOptions{}) // or NewCSVSource, NewMySQLSource, ...
plan, _ := connector.BuildMaskingPlan(ctx, src, connector.NewRuleClassifier(), connector.PlanOptions{ScanTextColumns: true})
plan.Sign("you", time.Now()) // Run refuses an unsigned plan
vault, _ := connector.OpenSQLiteVault("tenant.vault.db")
d, _ := connector.NewDesensitizer(plan, connector.DesensitizerOptions{Tenant: "acme", KeyProvider: kp, Vault: vault})
rep, _ := importflow.New(db).Run(ctx, connector.Desensitized(src, d), mapping)
```

Default-deny: a column not covered by the signed plan is dropped, never silently passed
through (the desensitizer fails closed). Per-column actions are
`drop`/`redact`/`mask`/`generalize`/`hash`/`pseudonymize`, plus in-place free-text PII
redaction. Pseudonyms are reversible via a *separate* AES-256-GCM token vault keyed per
tenant; the RAG/LLM path never reads the vault and `connector.Unmask` is the only reverse
path. Quasi-identifier re-identification risk is reduced, not zero.

The desensitizer is an `importflow.Source` decorator, so the same masked records feed
**both RAG and the knowledge graph** — pseudonymized join keys become deterministic tokens,
so KG entity IRIs and `bought`/etc. edges survive while the original PII never enters the
graph. See `examples/09_connector` (RAG) and the `e2e_test.go` KG case.

Agent surface: `connector.NewToolbox(db, ToolboxOptions{Vault, KeyProvider, Tenant})` →
`connector.NewMCPServer(tb, opts)` / `connector.RunMCPStdio(ctx, tb, opts)`, or
`connector.RegisterMCPTools(server, tb)` to ride an existing MCP server (e.g. importflow's).
The `cmd/cortexdb-connector-mcp` binary runs the four tools over stdio (config via
`CORTEXDB_PATH`, `CONNECTOR_VAULT_PATH`, `CONNECTOR_TENANT`, `CONNECTOR_KEY_FILE`).

### Near-real-time sync (CDC)

A `Watcher` keeps a knowledge base continuously in sync with a live DB **through
the same privacy gate** — it consumes row-level `ChangeEvent`s and applies
idempotent upserts (and hard deletes) to RAG + KG. Three change sources feed
it; true CDC (hard-delete capture, continuous streaming) is available for **both
Postgres and MySQL**.

**Route A (polling, `NewPollingChangeSource`):** polls
`WHERE <cursor> > <watermark>` per table on demand, is DB-agnostic
(PG/MySQL/Neon), resumes from a checkpoint in the knowledge DB. **Library API
only** — `w.Run(ctx)` does one pass and the caller schedules it (loop / cron).
Needs a monotonic cursor column (e.g. `updated_at`) and **cannot see hard
deletes**.

```go
src, _ := connector.NewPollingChangeSource("postgres", dsn, connector.PollingOptions{
    Tables: []connector.TableCursor{{Table: "orders", CursorColumn: "updated_at", KeyColumns: []string{"id"}}},
})
w, _ := connector.NewWatcher(db, src, connector.WatcherOptions{
    SourceKey: "orders", Desensitizer: d, Checkpoint: cp,
    Mapping: importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
        "orders": {RAG: &importflow.RAGPlan{ContentTmpl: "{name}", IDColumn: "id"}},
    }},
})
_ = w.Run(ctx) // schedule on an interval; resumes from the checkpoint
```

**Route B-PG (Postgres logical replication, `NewPostgresCDCSource`):** a true
CDC source over `pgoutput` that **captures hard deletes** and **streams
continuously** — `w.Run(ctx)` blocks until ctx is cancelled and resumes by LSN
from the checkpoint (`Checkpoint.Position`). Prerequisites: `wal_level=logical`,
a publication (`CREATE PUBLICATION cdc_pub FOR TABLE ...`), and a replication
slot (auto-created); default `REPLICA IDENTITY` (PK) carries the delete key.

```go
src, _ := connector.NewPostgresCDCSource(dsn, connector.PostgresCDCOptions{
    Publication: "cdc_pub", Slot: "cdc_slot",
    Tables: map[string][]string{"orders": {"id"}},
})
w, _ := connector.NewWatcher(db, src, connector.WatcherOptions{
    SourceKey: "orders-cdc", Desensitizer: d, Checkpoint: cp,
    Mapping: importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
        "orders": {RAG: &importflow.RAGPlan{ContentTmpl: "{name}", IDColumn: "id"}},
    }},
})
go w.Run(ctx) // streams insert/update/delete continuously; resumes by LSN
```

**Route B-MySQL (MySQL binlog, `NewMySQLBinlogSource`):** a true CDC source
reading the ROW-format binlog (via go-mysql canal). Like Route B-PG it
**captures hard deletes** and **streams continuously** — `w.Run(ctx)` blocks
until ctx is cancelled and resumes by binlog position (`Checkpoint.Position`).
Prerequisites: MySQL `binlog_format=ROW`, `binlog_row_image=FULL` (defaults in
MySQL 8), a user with `REPLICATION SLAVE, REPLICATION CLIENT`, and a unique
`ServerID`.

```go
src, _ := connector.NewMySQLBinlogSource(dsn, connector.MySQLBinlogOptions{
    ServerID: 1101, Tables: map[string][]string{"orders": {"id"}},
})
w, _ := connector.NewWatcher(db, src, connector.WatcherOptions{
    SourceKey: "orders-binlog", Desensitizer: d, Checkpoint: cp,
    Mapping: importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
        "orders": {RAG: &importflow.RAGPlan{ContentTmpl: "{name}", IDColumn: "id"}},
    }},
})
go w.Run(ctx) // streams insert/update/delete; resumes by binlog position
```

Precondition (all routes): every RAG table's `MappingPlan` must key on the
primary key (`RAGPlan.IDColumn`) so updates/deletes hit the right chunk —
`NewWatcher` errors otherwise. Privacy is unchanged: every streamed row still
passes the signed `MaskingPlan`, raw PII never enters RAG/KG, and pseudonymized
keys become the same deterministic tokens so KG edges survive.

## Tools and MCP

In-process tool calls:

```go
tools := db.GraphRAGTools()
defs := tools.Definitions()
resp, err := tools.Call(ctx, "knowledge_graph_query", payload)
_, _, _ = defs, resp, err
```

MCP server:

```go
server := db.NewMCPServer(cortexdb.MCPServerOptions{})
_ = server
```

Important tools:

- GraphRAG: `ingest_document`, `search_text`, `expand_graph`, `build_context`
- Knowledge/memory: `knowledge_save`, `knowledge_search`, `memory_save`, `memory_search`
- Knowledge graph: `knowledge_graph_upsert`, `knowledge_graph_query`, `knowledge_graph_shacl_validate`, `knowledge_graph_infer_refresh`
- KnowledgeMemory: `knowledge_memory_recall`, `knowledge_memory_build_context_pack`, `knowledge_memory_reflect`, `knowledge_memory_consolidate`
- Ontology: `ontology_save`, `ontology_get`, `ontology_list`, `ontology_delete`, `ontology_diff`, `ontology_action_list`, `ontology_action_apply`, `object_set_resolve`
- Inference: `apply_inference`, `rules_save`, `rules_list`, `rules_delete`, `rules_apply`, `inference_explain`
- Decision ledger: `decision_record`, `decision_chain`, `decision_precedents`
- Aggregates and thresholds: `aggregate_metadata`, `representative_records`, `search_vector_range`
- Graph introspection: `graph_schema`, `graph_property_values`, `graph_statistics`, `graph_health`
- Claim checking: `verify_claims`, `fact_provenance`, `uncited_facts`
- Graph analytics: `rank_graph_nodes`, `predict_graph_edges`
- Disambiguation: `disambiguate_mentions`
- Path retrieval: `search_paths` (and `return_paths` on `search_graphrag_lexical`, `relation_policies` on `expand_graph`)

Separate workflow toolboxes:

- memoryflow: `memoryflow_ingest_transcript`, `memoryflow_recall`, `memoryflow_wake_up_layers`, `memoryflow_prepare_reply`
- graphflow: `graphflow_build`, `graphflow_analyze`, `graphflow_report`, `graphflow_export`, `graphflow_run`, `global_search`, `build_community_hierarchy`

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
same brain the tools write to. So do `--memory-html`, `--export-memory`,
`--memory-usage` and `--sync-memory`: all four route through one shared
remote-fetch path that walks the brain a `memory_list_all` page at a time
rather than asking for everything in one call, so none of them stop working
once a brain no longer fits in one gRPC message.

Transport is plaintext by design — run it over loopback, a trusted LAN, or
Tailscale. **The token is the access control**: anyone holding it has full
read/write access. Embedder and LLM settings live on the server, not the
clients. `--graph-html`, `--recall-eval` and `--capture-session` follow the
remote too. Some one-shot modes are local-only by design — `--learn-path`,
`--reembed-memories` and `--graph-cleanup` among them; the latter two need
direct database access, so they run where the database lives.

The graph view is also an MCP tool, `render_graph_html`. It is the one tool that
is **not** proxied to the shared brain: the graph is read remotely, but the HTML
is rendered and written where the MCP server runs, because the caller needs the
file on its own filesystem to open or attach it — a server-side render would
land it on the brain's host, out of reach of whatever asked. Set
`CORTEXDB_VIEW_DIR` to choose where renders go.

`serve_graph_3d` serves the live 3D view, and the view can be asked, not only
looked at. Its Explore bar finds a node by name anywhere in the store (not only
in the drawn core), asks the brain a question and lights up what the answer
names, and runs read-only Cypher; any node can be expanded to its neighbours,
including ones outside the core, and the inspector works on a shared brain too.
Everything it runs is on a fixed list of read-only tools (`liveview.ExploreTools`,
held against the catalogue's `Mutates` by a test); SPARQL is not on it, because
`knowledge_graph_query` also runs updates. A view opens wherever its link says —
`?focus=ID|name&hops=N`, `?find=`, `?ask=`, `?cypher=`, `?q=`, `?type=`,
`?edge=`, `?explore=0`, `?theme=space|ember|mono`, `?mode=light|dark|auto` — and embedders can drive it with `postMessage`
(`cortexdb:focus`, `cortexdb:find`, `cortexdb:ask`, `cortexdb:cypher`). On a
phone the inspector is a bottom sheet and the other panels start folded.

Both bulk listings behind these views are paged: `memory_list_all` (default
limit 500) and `graph_list_all` (default limit 2000) each take `limit` and
`cursor`. `memory_list_all` always pairs `truncated` with `next_cursor` —
never one without the other — so a caller resumes exactly where it left off
instead of restarting the walk. `graph_list_all` has two modes, and only one of
them makes that same pairing: with no `cursor` and no `order` it keeps the
most-connected core, which is what makes a large graph renderable, and sets
`truncated` alone — degree ranking has no stable page boundary to resume from,
so there is no `next_cursor` to give. `order: "id"` — or simply supplying a
`cursor` — switches to a stable, resumable walk of the whole graph instead,
where `truncated` and `next_cursor` pair the same way `memory_list_all`'s do; a
page's edges may reference nodes that land on a later page, so the subgraph is
complete only once the walk finishes. Cursors are opaque and tagged with the
listing that produced them, so handing a `memory_list_all` cursor to
`graph_list_all`, or the reverse, fails loudly rather than silently returning
the wrong page.

## OpenClaw and Hermes Plugins

Native host-memory adapters live under `plugins/`:

- `plugins/openclaw-cortexdb-memory` registers OpenClaw's exclusive `memory`
  capability and CortexDB recall/store/delete tools.
- `plugins/hermes-cortexdb-memory` registers Hermes Agent's `MemoryProvider`
  with automatic prefetch and completed-turn synchronization.

Both adapters call the existing gRPC `ToolsService` and prefer
`knowledge_memory_recall`; do not implement a separate retrieval or storage path
inside an agent plugin. The `skills/cortexdb-memory-*` directories remain the
lighter explicit-tool integration.
- importflow: `importflow_plan`, `importflow_run`, `importflow_ddl_plan` (deterministic `CREATE TABLE` DDL → reviewable knowledge-graph MappingPlan, no LLM), `importflow_ddl_plan_ai` (LLM-refined DDL→KG plan; returns refined plan + deterministic baseline + `llm_used`; needs an `LLMInferer`) (via `importflow.NewToolbox(im)` or `importflow.NewMCPServer(im, opts)` / `importflow.RunMCPStdio(ctx, im, opts)`)
- connector: `connector_introspect`, `connector_plan`, `connector_run`, `connector_unmask` — privacy gate over a live DB/source feeding RAG + KG. Wire via `connector.NewMCPServer(tb, opts)` / `connector.RunMCPStdio(ctx, tb, opts)`, `connector.RegisterMCPTools(server, tb)` to ride another server, or run `cmd/cortexdb-connector-mcp`.

## gRPC Sidecar + Rust / Python / Node clients

The full facade is also served over gRPC for non-Go callers:

- Server binary: `cmd/cortexdb-grpc` (env/flags: `CORTEXDB_PATH`,
  `CORTEXDB_GRPC_ADDR` default `127.0.0.1:47821`, `CORTEXDB_GRPC_TOKEN` enables
  bearer auth, `OPENAI_BASE_URL`/`OPENAI_API_KEY`/`CORTEXDB_EMBED_MODEL`/
  `CORTEXDB_EMBED_DIM` wire an OpenAI-compatible embedder; unset → lexical mode).
  Flags override the environment, which overrides the defaults. `-health`
  probes a server already running at `-addr` and exits non-zero when it is not
  serving, so the binary is its own liveness check; a wildcard listen address is
  probed on loopback. Running it as a service: `deploy/` has the systemd unit,
  the container image (whose `HEALTHCHECK` is `cortexdb-grpc -health`) and the
  compose file, plus backup/upgrade notes.
- Proto contract: `proto/cortexdb/v1/` — services `KnowledgeService`,
  `MemoryService`, `KnowledgeGraphService`, `GraphRagService`, `ToolsService`
  (generic `CallTool(name, json)` over the same toolbox as MCP), `AdminService`.
- Go layers: generated stubs in `pkg/rpc/v1`, conversion-only handlers in
  `pkg/rpcserver` (no business logic there). Regenerate via `scripts/gen-proto.sh`.
- Clients (all published under the name `cortexdb-client`, mirroring the same
  `knowledge`/`memory`/`graph`/`graphrag`/`tools`/`admin` sub-clients + bearer token):
  - Rust — `clients/rust/` (crates.io): `cargo add cortexdb-client`;
    `CortexClient::builder(endpoint).token(t).connect()`; optional
    `managed-server` feature downloads/spawns the sidecar with an auto token
    (`Sidecar::ensure().spawn(db)`). Generated code committed (`src/gen/`).
  - Python — `clients/python/` (PyPI): `pip install cortexdb-client`;
    `CortexClient.connect(endpoint, token=t)`. Target for Python agents (Hermes).
  - Node — `clients/node/` (npm): `npm install cortexdb-client`;
    `CortexClient.connect(endpoint, { token })`, promise-based. Target for Node
    agents (OpenClaw); protos loaded at runtime, no build step.
- Single-node by design: one sidecar process owns one SQLite file; isolate
  multi-user data with memory scopes / KG namespaces / collections.
- Agent Skills: `skills/cortexdb-memory-hermes` (Python) and
  `skills/cortexdb-memory-openclaw` (Node) are agentskills.io-format skills that
  wire CortexDB in as agent memory + KG. Published to ClawHub
  (`clawhub skill install cortexdb-agent-memory`) and installable from git /
  skills.sh (`hermes skills install git:liliang-cn/cortexdb@main`,
  `npx skills add liliang-cn/cortexdb`).

## Optional Semantic Router

`pkg/semantic-router` is optional. Use it before CortexDB tools when you need intent routing.

No-embedder lexical router:

```go
router, _ := semanticrouter.NewLexicalRouter(semanticrouter.WithSparseThreshold(0.1))
_ = router.Add(&semanticrouter.SparseRoute{Name: "memory_save", Utterances: []string{"remember this", "save to memory"}})
route, _ := router.Route(ctx, "please remember this")
_ = route.RouteName
```

## Examples

The examples are architecture-oriented:

```bash
go run ./examples/01_core
go run ./examples/02_rag
go run ./examples/03_memoryflow
go run ./examples/04_knowledge_graph
go run ./examples/05_graphflow
go run ./examples/06_tools_mcp
go run ./examples/07_importflow
go run ./examples/08_self_knowledge_graph   # docs -> graphflow -> KG of this project; qa_test.go answers from graph edges
go run ./examples/16_ontology               # typed object types, actions, object sets, typed tools, schema diff
```

Use `examples/05_graphflow` to verify OpenAI-compatible LLM graph extraction with structured output.

## Checks

When changing CortexDB, run:

```bash
go build ./...
go test ./...
```
