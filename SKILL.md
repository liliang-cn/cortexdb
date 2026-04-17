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
| Expose CortexDB to an agent or MCP client | `db.GraphRAGTools()` or `db.NewMCPServer()` | Wraps the high-level APIs as tool definitions or an MCP server rather than introducing a second implementation path. |

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

SPARQL is a practical embedded subset. It includes SELECT, ASK, CONSTRUCT, DESCRIBE, update forms, GRAPH, OPTIONAL, UNION, MINUS, VALUES, BIND, FILTER, EXISTS, NOT EXISTS, aggregates, subqueries, and constrained property paths: `^pred`, `p|q`, `p+`, `p*`.

RDFS-lite:

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

SHACL-lite:

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

- GraphRAG: `ingest_document`, `search_text`, `expand_graph`, `build_context`
- Knowledge/memory: `knowledge_save`, `knowledge_search`, `memory_save`, `memory_search`
- Knowledge graph: `knowledge_graph_upsert`, `knowledge_graph_query`, `knowledge_graph_shacl_validate`, `knowledge_graph_infer_refresh`
- KnowledgeMemory: `knowledge_memory_recall`, `knowledge_memory_build_context_pack`, `knowledge_memory_reflect`, `knowledge_memory_consolidate`
- Ontology/inference: `ontology_save`, `apply_inference`

Separate workflow toolboxes:

- memoryflow: `memoryflow_ingest_transcript`, `memoryflow_recall`, `memoryflow_wake_up_layers`, `memoryflow_prepare_reply`
- graphflow: `graphflow_build`, `graphflow_analyze`, `graphflow_report`, `graphflow_export`, `graphflow_run`

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
