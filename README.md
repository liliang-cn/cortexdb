# CortexDB

[![CI/CD](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/cortexdb/v2)](https://goreportcard.com/report/github.com/liliang-cn/cortexdb/v2)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

CortexDB is a pure-Go, single-file AI memory and knowledge graph library. It uses SQLite as the storage kernel and exposes vector search, lexical search, RAG knowledge storage, agent memory workflows, RDF/SPARQL/RDFS/SHACL knowledge graph features, corpus-to-graph workflows, and MCP-aligned tool APIs.

It is designed for local-first AI agents that need durable memory without running a separate vector database, graph database, or MCP service stack.

## Architecture

```text
pkg/cortexdb
  Core public DB facade: vectors, text search, knowledge, memory, brain, KG, tools, MCP.

pkg/memoryflow
  Agent memory workflow: transcript ingest, recall, wake-up context, diary, promotion.

pkg/graphflow
  Corpus-to-graph workflow: extraction schema, build, analyze, report, export, HTML.

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
	db, err := cortexdb.Open(cortexdb.DefaultConfig("brain.db"))
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
- Brain: `brain_recall`, `brain_build_context_pack`, `brain_reflect`, `brain_consolidate`
- Ontology/inference: `ontology_save`, `apply_inference`

`memoryflow` and `graphflow` also expose their own toolbox/MCP surfaces:

- memoryflow: `memoryflow_ingest_transcript`, `memoryflow_recall`, `memoryflow_wake_up_layers`, `memoryflow_prepare_reply`
- graphflow: `graphflow_build`, `graphflow_analyze`, `graphflow_report`, `graphflow_export`, `graphflow_run`

## Examples

The examples are intentionally small and architecture-oriented:

```bash
go run ./examples/01_core
go run ./examples/02_rag
go run ./examples/03_memoryflow
go run ./examples/04_knowledge_graph
go run ./examples/05_graphflow
go run ./examples/06_tools_mcp
```

See [examples/README.md](examples/README.md) for the selection guide.

## Status

CortexDB is an embedded AI memory/KG library, not a drop-in replacement for full graph database products such as Fuseki, GraphDB, or Stardog. The goal is practical local-first storage and reasoning for agents: one file, Go APIs, tool/MCP surfaces, and enough RDF/SPARQL/RDFS/SHACL functionality to build useful memory and knowledge workflows.
