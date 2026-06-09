# CortexDB

[![CI/CD](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/cortexdb/v2)](https://goreportcard.com/report/github.com/liliang-cn/cortexdb/v2)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Crates.io](https://img.shields.io/crates/v/cortexdb-client.svg?label=cortexdb-client)](https://crates.io/crates/cortexdb-client)

CortexDB is a pure-Go, single-file AI memory and knowledge graph library. It uses SQLite as the storage kernel and exposes vector search, lexical search, RAG knowledge storage, agent memory workflows, RDF/SPARQL/RDFS/SHACL knowledge graph features, corpus-to-graph workflows, and MCP-aligned tool APIs.

It is designed for local-first AI agents that need durable memory without running a separate vector database, graph database, or MCP service stack.

## Architecture

```text
pkg/cortexdb
  Core public DB facade: vectors, text search, knowledge, memory, KnowledgeMemory, KG, tools, MCP.

pkg/memoryflow
  Agent memory workflow: transcript ingest, recall, wake-up context, diary, promotion.

pkg/graphflow
  Corpus-to-graph workflow: extraction schema, build, analyze, report, export, HTML.

pkg/importflow
  External structured-data import (CSV / MySQL-PG dumps) into RAG + knowledge-graph foundations, AI-assisted mapping optional.

pkg/graph
  Low-level graph engine: property graph, RDF triples/quads, SPARQL, RDFS, SHACL.

pkg/core
  SQLite storage, embeddings, FTS5, vector indexes, chat/session primitives.
```

Use `pkg/cortexdb` first. Reach for `pkg/memoryflow` when building agent memory UX, `pkg/graphflow` when building graph extraction/report pipelines, and `pkg/graph` only when you need low-level RDF or property graph control.

## Install

```bash
go get github.com/liliang-cn/cortexdb/v2
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	db, err := cortexdb.Open(cortexdb.DefaultConfig("KnowledgeMemory.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	quick := db.Quick()

	_, _ = quick.Add(ctx, []float32{0.1, 0.2, 0.9}, "SQLite is a single-file database.")
	results, _ := quick.Search(ctx, []float32{0.1, 0.2, 0.8}, 1)

	if len(results) > 0 {
		fmt.Println(results[0].Content)
	}
}
```

## Choose the Right Layer

```text
Need vectors / collections / FTS5?      -> pkg/cortexdb / pkg/core
Need RAG knowledge storage/search?      -> pkg/cortexdb SaveKnowledge/SearchKnowledge
Need chat/session memory workflow?      -> pkg/memoryflow
Need RDF/SPARQL/RDFS/SHACL?             -> pkg/cortexdb knowledge graph APIs
Need corpus-to-graph/report/export?     -> pkg/graphflow
Need agent tools or MCP server?         -> db.GraphRAGTools() / db.NewMCPServer()
Need low-level graph control?           -> pkg/graph
```

## High-Level Knowledge and Memory

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
```

Without an embedder, CortexDB uses lexical retrieval and planner-provided keywords. With an embedder, the same high-level APIs can use semantic or hybrid retrieval.

RAG benchmark coverage is available in `pkg/cortexdb`:

```bash
go test ./pkg/cortexdb -run '^$' -bench 'BenchmarkRAG' -benchmem
```

Reference run on Apple M2 Pro, `-benchtime=3x`:

| Benchmark | Fixture | Time/op | Approx Throughput | Alloc/op |
|---|---:|---:|---:|---:|
| SaveKnowledge | 1 document, 3 entities, 2 relations | ~3.26 ms | ~306 ops/s | ~75 KB |
| SearchKnowledge lexical | 500 docs, keyword plan, graph off | ~4.43 ms | ~226 QPS | ~234 KB |
| SearchKnowledge graph-light | 500 docs, entity plan, bounded graph expansion | ~8.40 ms | ~119 QPS | ~1.7 MB |
| BuildContext | chunk pack with graph-light enrichment | ~0.41 ms | ~2,463 ops/s | ~94 KB |

## MemoryFlow

`pkg/memoryflow` is the agent memory workflow layer. It stores raw transcript exchanges, recalls relevant context, assembles wake-up layers, appends diary entries, reconstructs transcripts, and optionally promotes durable facts to knowledge.

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

LLM-dependent behavior is interface-based:

```go
type QueryPlanner interface {
	Plan(ctx context.Context, query string, state memoryflow.SessionState) (*cortexdb.RetrievalPlan, error)
}

type SessionExtractor interface {
	Extract(ctx context.Context, transcript memoryflow.Transcript, state memoryflow.SessionState) ([]memoryflow.PromotionCandidate, error)
}
```

MemoryFlow can also be wrapped with optional recall strategies. `pkg/hindsight`
now provides a compatibility strategy plugin that enriches recall with
bank/entity/keyword cues while leaving MemoryFlow as the default workflow:

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

## Knowledge Graph

CortexDB has an embedded RDF/KG layer on top of the same SQLite file:

- RDF terms, triples, and quads
- namespaces
- N-Triples / N-Quads / Turtle / TriG import and export
- practical SPARQL subset
- RDFS-lite materialized inference with provenance
- incremental RDFS inference refresh
- SHACL-lite validation

```go
_, _ = db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
	Triples: []cortexdb.KnowledgeGraphTriple{
		{
			Subject:   graph.NewIRI("https://example.com/alice"),
			Predicate: graph.NewIRI(graph.RDFType),
			Object:    graph.NewIRI("https://example.com/Person"),
		},
		{
			Subject:   graph.NewIRI("https://example.com/alice"),
			Predicate: graph.NewIRI("https://schema.org/name"),
			Object:    graph.NewLiteral("Alice"),
		},
	},
})

result, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
	Query: `
PREFIX schema: <https://schema.org/>
SELECT ?name WHERE {
	<https://example.com/alice> schema:name ?name .
}
`,
})
_ = result
```

SPARQL support is a practical embedded subset. It includes `SELECT`, `ASK`, `CONSTRUCT`, `DESCRIBE`, `INSERT DATA`, `INSERT ... WHERE`, `DELETE DATA`, `DELETE WHERE`, `DELETE ... INSERT ... WHERE`, `WITH`, `USING`, `GRAPH`, `OPTIONAL`, `UNION`, `MINUS`, `VALUES`, `BIND`, `FILTER`, `EXISTS`, `NOT EXISTS`, `REGEX`, `LANG`, `DATATYPE`, `COALESCE`, `IF`, arithmetic, `GROUP BY`, `HAVING`, `COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, `SAMPLE`, `GROUP_CONCAT`, `ORDER BY`, `LIMIT`, `OFFSET`, subqueries, and a constrained property path subset: `^pred`, `p|q`, `p+`, `p*`.

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

SHACL-lite currently supports `sh:targetClass`, `sh:targetNode`, `sh:datatype`, `sh:minCount`, `sh:maxCount`, `sh:minInclusive`, `sh:maxInclusive`, `sh:pattern`, `sh:class`, `sh:nodeKind`, `sh:in`, and `sh:message`.

Knowledge graph benchmark coverage is available in `pkg/graph`:

```bash
go test ./pkg/graph -run '^$' -bench 'BenchmarkKnowledgeGraph' -benchmem
```

Reference run on Apple M2 Pro, `-benchtime=3x`:

| Benchmark | Fixture | Time/op | Approx Throughput | Alloc/op |
|---|---:|---:|---:|---:|
| RDF upsert | unique person/name triple | ~0.97 ms | ~1,028 ops/s | ~37 KB |
| RDF find by predicate | 1,000 name triples, limit 20 | ~0.45 ms | ~2,242 QPS | ~49 KB |
| SPARQL select | direct lookup over 1,000 people | ~0.56 ms | ~1,802 QPS | ~26 KB |
| SPARQL property path | `ex:knows+` over 500-node chain | ~2.21 ms | ~453 QPS | ~2.5 MB |
| SPARQL subquery | grouped friend counts over 500 people | ~74.45 ms | ~13 QPS | ~185 MB |
| RDFS full refresh | 25 class/type closure fixture | ~805.94 ms | ~1.2 ops/s | ~40 MB |
| RDFS incremental refresh | changed subclass triple fixture | ~859.85 ms | ~1.2 ops/s | ~46 MB |
| SHACL-lite validation | 500 people age constraints | ~139.24 ms | ~7.2 ops/s | ~6.6 MB |

These numbers are a local reference point, not a portability guarantee. The RDFS chain fixture and SPARQL subquery benchmark are intentionally stress-heavy and useful for tracking optimizer work. The suite also includes `BenchmarkKnowledgeGraphRDFSIncrementalLocalRefresh` to measure a more realistic localized change on a multi-component graph.

## GraphFlow

`pkg/graphflow` is the corpus-to-graph workflow layer:

- canonical extraction schema: `ExtractionResult`, `ExtractionNode`, `ExtractionEdge`
- deterministic `HeuristicExtractor`
- LLM-backed extraction through `JSONGenerator`
- `Build`, `Analyze`, `RenderReport`
- `Export` to JSON/Markdown and `ExportHTML`

The library keeps model integration as an interface:

```go
type JSONGenerator interface {
	GenerateJSON(ctx context.Context, systemPrompt string, userPrompt string) ([]byte, error)
}
```

The example `examples/05_graphflow` demonstrates `openai-go/v3` with JSON Schema structured output. Configure it with `.env`:

```env
OPENAI_API_KEY=...
OPENAI_BASE_URL=http://43.167.167.6:8080/v1
OPENAI_MODEL=gpt-5.4
```

Then run:

```bash
go run ./examples/05_graphflow
```

## Tools and MCP

For in-process tool calling:

```go
tools := db.GraphRAGTools()
defs := tools.Definitions()
resp, err := tools.Call(ctx, "knowledge_graph_query", payload)
_ = defs
_ = resp
_ = err
```

For MCP:

```go
server := db.NewMCPServer(cortexdb.MCPServerOptions{})
_ = server
```

Tool groups include:

- GraphRAG: `ingest_document`, `search_text`, `expand_graph`, `build_context`
- Knowledge/memory: `knowledge_save`, `knowledge_search`, `memory_save`, `memory_search`
- Knowledge graph: `knowledge_graph_upsert`, `knowledge_graph_query`, `knowledge_graph_shacl_validate`, `knowledge_graph_infer_refresh`
- KnowledgeMemory: `knowledge_memory_recall`, `knowledge_memory_build_context_pack`, `knowledge_memory_reflect`, `knowledge_memory_consolidate`
- Ontology/inference: `ontology_save`, `apply_inference`

`memoryflow` and `graphflow` also expose their own toolbox/MCP surfaces:

- memoryflow: `memoryflow_ingest_transcript`, `memoryflow_recall`, `memoryflow_wake_up_layers`, `memoryflow_prepare_reply`
- graphflow: `graphflow_build`, `graphflow_analyze`, `graphflow_report`, `graphflow_export`, `graphflow_run`

## Use from other languages (gRPC sidecar)

The full facade is also served over gRPC by the `cortexdb-grpc` sidecar, with
typed clients published for **Rust, Python, and Node** — give a Hermes (Python)
or OpenClaw (Node) agent durable memory plus a SPARQL knowledge graph:

```bash
cargo add cortexdb-client    # Rust  (crates.io)
pip install cortexdb-client  # Python (PyPI)   — e.g. Hermes
npm install cortexdb-client  # Node   (npm)    — e.g. OpenClaw
```

All three clients mirror the same sub-client layout (`knowledge`, `memory`,
`graph`, `graphrag`, `tools`, `admin`) and bearer-token auth.

Start the sidecar:

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=my.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
# listening on 127.0.0.1:47821
```

| Env / flag | Default | Meaning |
|---|---|---|
| `CORTEXDB_PATH` / `-db` | `cortexdb.db` | SQLite file |
| `CORTEXDB_GRPC_ADDR` / `-addr` | `127.0.0.1:47821` | listen address (localhost-only) |
| `CORTEXDB_GRPC_TOKEN` / `-token` | empty (auth off) | bearer token for every RPC |
| `OPENAI_BASE_URL` | empty (lexical mode) | OpenAI-compatible embeddings endpoint |
| `OPENAI_API_KEY` | empty | embeddings API key |
| `CORTEXDB_EMBED_MODEL` | `text-embedding-3-small` | embedding model |
| `CORTEXDB_EMBED_DIM` | `1536` | embedding dimension |

Works with any OpenAI-compatible endpoint, e.g. Ollama:
`OPENAI_BASE_URL=http://localhost:11434/v1 CORTEXDB_EMBED_MODEL=embeddinggemma CORTEXDB_EMBED_DIM=768`.

From Rust:

```rust
use cortexdb_client::{proto, CortexClient};

let client = CortexClient::builder("http://127.0.0.1:47821")
    .token("s3cret")
    .connect()
    .await?;
client.knowledge().save_knowledge(proto::SaveKnowledgeRequest {
    knowledge_id: "k1".into(),
    content: "CortexDB from Rust over gRPC.".into(),
    ..Default::default()
}).await?;
let hits = client.knowledge().search_knowledge(proto::SearchKnowledgeRequest {
    query: "rust grpc".into(), top_k: 3, ..Default::default()
}).await?.into_inner();
```

Services: `knowledge()`, `memory()`, `graph()` (SPARQL/RDF/SHACL/inference/ontology),
`graphrag()`, `tools()` (generic tool dispatch, same surface as MCP), `admin()`.

From Python (Hermes-style agent memory):

```python
from cortexdb_client import CortexClient, proto

with CortexClient.connect("127.0.0.1:47821", token="s3cret") as client:
    client.memory.SaveMemory(proto.SaveMemoryRequest(
        memory_id="m1", user_id="alice", scope="user",
        content="Alice prefers Python and dislikes heavy frameworks."))
    hits = client.memory.SearchMemory(proto.SearchMemoryRequest(
        query="what does the user like?", user_id="alice", scope="user", top_k=3))
```

From Node (OpenClaw-style agent memory):

```js
const { CortexClient } = require('cortexdb-client');

const client = CortexClient.connect('127.0.0.1:47821', { token: 's3cret' });
await client.memory.SaveMemory({
  memoryId: 'm1', userId: 'alice', scope: 'user',
  content: 'Alice runs OpenClaw locally and prefers TypeScript.' });
const hits = await client.memory.SearchMemory({
  query: 'what stack does the user prefer?', userId: 'alice', scope: 'user', topK: 3 });
```

With the `managed-server` feature the **Rust** crate resolves (env → PATH → GitHub
Releases download with sha256 verification) and spawns the sidecar itself on a
random port with a fresh random token:

```rust
use cortexdb_client::sidecar::Sidecar;

let running = Sidecar::ensure().await?.spawn("my.db").await?;
let client = running.client().await?;
```

Notes: token auth rides plaintext gRPC — fine on localhost; add TLS or a
reverse proxy for cross-machine use. v1 is single-node: one sidecar owns one
SQLite file (multi-user apps use memory scopes / KG namespaces / collections
inside the file). Proto contract lives in `proto/cortexdb/v1/`; clients live
under `clients/{rust,python,node}/`.

## Optional Semantic Router

`pkg/semantic-router` remains available as an optional utility for routing user input to handlers or CortexDB tools before retrieval. It is not required by the main CortexDB, MemoryFlow, or GraphFlow paths.

For no-embedder setups, use the lexical router:

```go
router, _ := semanticrouter.NewLexicalRouter(semanticrouter.WithSparseThreshold(0.1))
_ = router.Add(&semanticrouter.SparseRoute{
	Name:       "memory_save",
	Utterances: []string{"remember this", "save to memory"},
})
route, _ := router.Route(ctx, "please remember this preference")
_ = route.RouteName
```

## Examples

The examples are intentionally small and architecture-oriented:

```bash
go run ./examples/01_core
go run ./examples/02_rag
go run ./examples/03_memoryflow
go run ./examples/04_knowledge_graph
go run ./examples/05_graphflow
go run ./examples/06_tools_mcp
go run ./examples/07_importflow
go run ./examples/08_self_knowledge_graph   # builds a KG of this project itself; qa_test.go answers questions from it
```

See [examples/README.md](examples/README.md) for the selection guide.

## Status

CortexDB is an embedded AI memory/KG library, not a drop-in replacement for full graph database products such as Fuseki, GraphDB, or Stardog. The goal is practical local-first storage and reasoning for agents: one file, Go APIs, tool/MCP surfaces, and enough RDF/SPARQL/RDFS/SHACL functionality to build useful memory and knowledge workflows.
