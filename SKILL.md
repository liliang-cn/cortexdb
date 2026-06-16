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
| Import external CSV or MySQL/PG SQL dumps into RAG + knowledge graph | `pkg/importflow` `New().Run()` / `AutoImport()` | Parses a source into records, routes columns via a `MappingPlan` to RAG content and KG entities/relations, with optional AI mapping inference and triple extraction. |
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
idempotent upserts (and hard deletes) to RAG + KG. Two change sources feed it.

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

Precondition (both routes): every RAG table's `MappingPlan` must key on the
primary key (`RAGPlan.IDColumn`) so updates/deletes hit the right chunk —
`NewWatcher` errors otherwise. Privacy is unchanged: every streamed row still
passes the signed `MaskingPlan`, raw PII never enters RAG/KG, and pseudonymized
keys become the same deterministic tokens so KG edges survive. **Route B-MySQL**
(binlog) is the only remaining CDC piece — still planned, not yet implemented.

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
- importflow: `importflow_plan`, `importflow_run` (via `importflow.NewToolbox(im)` or `importflow.NewMCPServer(im, opts)` / `importflow.RunMCPStdio(ctx, im, opts)`)
- connector: `connector_introspect`, `connector_plan`, `connector_run`, `connector_unmask` — privacy gate over a live DB/source feeding RAG + KG. Wire via `connector.NewMCPServer(tb, opts)` / `connector.RunMCPStdio(ctx, tb, opts)`, `connector.RegisterMCPTools(server, tb)` to ride another server, or run `cmd/cortexdb-connector-mcp`.

## gRPC Sidecar + Rust / Python / Node clients

The full facade is also served over gRPC for non-Go callers:

- Server binary: `cmd/cortexdb-grpc` (env/flags: `CORTEXDB_PATH`,
  `CORTEXDB_GRPC_ADDR` default `127.0.0.1:47821`, `CORTEXDB_GRPC_TOKEN` enables
  bearer auth, `OPENAI_BASE_URL`/`OPENAI_API_KEY`/`CORTEXDB_EMBED_MODEL`/
  `CORTEXDB_EMBED_DIM` wire an OpenAI-compatible embedder; unset → lexical mode).
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
```

Use `examples/05_graphflow` to verify OpenAI-compatible LLM graph extraction with structured output.

## Checks

When changing CortexDB, run:

```bash
go build ./...
go test ./...
```
