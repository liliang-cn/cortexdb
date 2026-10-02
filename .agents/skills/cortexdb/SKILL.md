---
name: cortexdb
description: Use CortexDB for local-first AI memory, vector search, RAG, knowledge graphs, SPARQL/RDFS/SHACL, corpus-to-graph workflows, and MCP/tool calling. Use when working with CortexDB, embeddings, memory, RAG, GraphRAG, knowledge graph, RDF, SPARQL, SHACL, memoryflow, graphflow, or MCP tools.
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

pkg/graph
  Low-level graph engine: property graph, RDF triples/quads, SPARQL, RDFS, SHACL.

pkg/core
  SQLite storage, embeddings, FTS5, vector indexes, chat/session primitives.
```

Default recommendation:

- Use `pkg/cortexdb` for application code.
- Use `pkg/memoryflow` for chat/session/agent memory workflows.
- Use `pkg/graphflow` for document/corpus-to-graph extraction and report/export workflows.
- Use `pkg/graph` only for low-level RDF/SPARQL/RDFS/SHACL or property graph control.

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

The live view (`serve_graph_3d`) can be asked, not only looked at: find any node in the store, ask a question and see what the answer names, run read-only Cypher, expand a node's neighbours past the drawn core. It is read-only, works on a phone, has light and dark themes, and opens wherever its link says (`?focus=`, `?ask=`, `?cypher=`, `?find=`, `?type=`, `?edge=`, `?theme=`, `?mode=`).

Chinese (and other CJK) questions work in lexical mode as written: a sentence is cut at function words, broken into character bigrams and ranked with BM25 beside the word-index results. A row is returned only when it matches more than one word of the question, so a question the store knows nothing about still returns nothing.

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

- GraphRAG: `ingest_document`, `search_text`, `expand_graph`, `build_context`, `search_paths` (multi-hop: chains of facts between named entities, each edge citing its chunk)
- Knowledge/memory: `knowledge_save`, `knowledge_search`, `memory_save`, `memory_search`
- Knowledge graph: `knowledge_graph_upsert`, `knowledge_graph_query`, `knowledge_graph_shacl_validate`, `knowledge_graph_infer_refresh`
- KnowledgeMemory: `knowledge_memory_recall`, `knowledge_memory_build_context_pack`, `knowledge_memory_reflect`, `knowledge_memory_consolidate`
- Ontology/inference: `ontology_save`, `apply_inference`

Separate workflow toolboxes:

- memoryflow: `memoryflow_ingest_transcript`, `memoryflow_recall`, `memoryflow_wake_up_layers`, `memoryflow_prepare_reply`
- graphflow: `graphflow_build`, `graphflow_analyze`, `graphflow_report`, `graphflow_export`, `graphflow_run`, `global_search`, `build_community_hierarchy` (the last two are also on the `cortexdb-mcp-stdio` server)

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
```

Use `examples/05_graphflow` to verify OpenAI-compatible LLM graph extraction with structured output.

## Checks

When changing CortexDB, run:

```bash
go build ./...
go test ./...
```
