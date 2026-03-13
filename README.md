# CortexDB

[![CI/CD](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/liliang-cn/cortexdb/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/cortexdb)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/cortexdb/v2)](https://goreportcard.com/report/github.com/liliang-cn/cortexdb/v2)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**An embedded cognitive memory and graph database for AI Agents.**

CortexDB is a **100% pure Go library** that transforms a single SQLite file into a powerful AI storage engine. It blends **Hybrid Vector Search**, **GraphRAG**, **MCP Tool Calling**, and a **Hindsight-inspired Agent Memory System**, giving AI applications a structured, persistent, and intelligent brain without complex external infrastructure.

## ✨ Why CortexDB?

- 🧠 **Agent Memory (Hindsight)** – Full `retain → recall → reflect` lifecycle with multi-channel TEMPR retrieval.
- 🕸️ **Dual-Mode GraphRAG** – Use embedder-backed GraphRAG when vectors are available, or lexical/tool-calling GraphRAG when only an LLM is available.
- 🔍 **Hybrid Search** – Combines Vector similarity (HNSW) and precise Keyword matching (FTS5) using RRF fusion.
- 🔌 **MCP Stdio Server** – Expose CortexDB tools to external LLMs through the official Model Context Protocol Go SDK.
- 🏗️ **Structured Data Friendly** – Easily map SQL/CSV rows to natural language + metadata for advanced `PreFilter` querying.
- 🪶 **Ultra Lightweight** – Single SQLite file, zero external dependencies. Pure Go.
- 🛡️ **Secure** – Row-Level Security via **ACL** fields to isolate multi-tenant data.

## 🚀 Quick Start

```bash
go get github.com/liliang-cn/cortexdb/v2
```

Run the built-in MCP stdio server:

```bash
go run ./cmd/cortexdb-mcp-stdio
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	// Initialize CortexDB (auto-creates tables for vectors, docs, memory, graph)
	db, err := cortexdb.Open(cortexdb.DefaultConfig("brain.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// 1. Store a memory/fact
	db.Quick().Add(ctx, []float32{0.1, 0.2, 0.9}, "Go is a statically typed, compiled language.")

	// 2. Recall similar concepts
	results, _ := db.Quick().Search(ctx, []float32{0.1, 0.2, 0.8}, 1)
	if len(results) > 0 {
		fmt.Println("Recalled:", results[0].Content)
	}
}
```

## 🏗 Core Capabilities

### 1. The Agent Memory System (Hindsight)

CortexDB includes `hindsight`, a dedicated bionic memory module for Agents. It doesn't just store logs; it categorizes memories (Facts, Beliefs, Observations) and recalls them dynamically.

```go
import "github.com/liliang-cn/cortexdb/v2/pkg/hindsight"

sys, _ := hindsight.New(&hindsight.Config{DBPath: "agent_memory.db"})
defer sys.Close()

// Create an Agent profile with personality traits
bank := hindsight.NewBank("travel-agent-1", "Travel Assistant")
bank.Empathy = 4      // 1-5 scale
bank.Skepticism = 2   // 1-5 scale
sys.CreateBank(ctx, bank)

// RETAIN: Store structured observations about the user
sys.Retain(ctx, &hindsight.Memory{
	BankID:   "travel-agent-1",
	Type:     hindsight.WorldMemory,
	Content:  "Alice prefers window seats and vegetarian meals.",
	Vector:   embed("Alice prefers window seats..."),
	Entities: []string{"user:alice", "preference:flight", "preference:food"},
})

// RECALL: Multi-channel TEMPR retrieval (Temporal, Entity, Memory, Priming, Recall)
results, _ := sys.Recall(ctx, &hindsight.RecallRequest{
	BankID:      "travel-agent-1",
	QueryVector: embed("What food should I order for Alice?"),
	Strategy:    hindsight.DefaultStrategy(), // Uses all channels + RRF Fusion
})
```

### 2. High-Level Text & Structured Data APIs

Stop dealing with raw `[]float32` arrays manually. Hook up your Embedder and let CortexDB handle the rest.

```go
// 1. Inject your embedding model (OpenAI, Ollama, etc.)
db, _ := cortexdb.Open(config, cortexdb.WithEmbedder(myOpenAIEmbedder))

// 2. Insert raw text directly
db.InsertText(ctx, "doc_1", "The new iPhone 15 Pro features a titanium body.", map[string]string{
	"category": "electronics",
	"price":    "999",
})

// 3. Search purely by text
results, _ := db.SearchText(ctx, "latest Apple phones", 5)

// 4. Hybrid Search (Semantic + Exact Keyword)
hybridRes, _ := db.HybridSearchText(ctx, "titanium body", 5)

// 5. FTS5 Only (No vectors needed, super fast!)
ftsRes, _ := db.SearchTextOnly(ctx, "iPhone", cortexdb.TextSearchOptions{TopK: 5})
```
*See `examples/text_api` for the full code.*

### 3. GraphRAG (Knowledge Graph)

Transform relational data (like SQL tables) into a Knowledge Graph for multi-hop reasoning.

```go
// 1. Insert Nodes
db.Graph().UpsertNode(ctx, &graph.GraphNode{
	ID: "dept_eng", NodeType: "department", Content: "Engineering Department", Vector: vec1,
})
db.Graph().UpsertNode(ctx, &graph.GraphNode{
	ID: "emp_alice", NodeType: "employee", Content: "Alice (Senior Go Dev)", Vector: vec2,
})

// 2. Create Relationships (Edges)
db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
	FromNodeID: "emp_alice",
	ToNodeID:   "dept_eng",
	EdgeType:   "BELONGS_TO",
	Weight:     1.0,
})

// 3. Traverse the Graph automatically during RAG
neighbors, _ := db.Graph().Neighbors(ctx, "emp_alice", graph.TraversalOptions{
	EdgeTypes: []string{"BELONGS_TO"},
	MaxDepth:  1,
})
// Finds that Alice belongs to the Engineering Department without complex SQL JOINs!
```
*See `examples/structured_data` for the full code.*

Higher-level GraphRAG retrieval is also available when an embedder is configured:

```go
result, _ := db.SearchGraphRAG(ctx, "Where does Alice work?", cortexdb.GraphRAGQueryOptions{
	TopK:          4,
	RetrievalMode: cortexdb.RetrievalModeAuto, // auto | lexical | graph
})
```

`RetrievalMode` is useful because graph expansion can be expensive on large graphs:

- `lexical` keeps retrieval fast by skipping graph expansion.
- `graph` always expands the graph.
- `auto` uses lightweight entity heuristics to decide whether graph expansion is worth the cost.

If you already have external LLM orchestration, the no-embedder tool surface exposes the same idea through `retrieval_mode` and `keywords` / `alternate_queries`.

### 4. MCP Tool Calling / No-Embedder Mode

If you do not have an embedding model but you do have an LLM, CortexDB can still be used as an MCP tool server. In this mode:

- the LLM expands the user goal into many `keywords`, aliases, synonyms, abbreviations, and multilingual variants
- CortexDB uses `FTS5/BM25` for seed retrieval
- graph expansion is optional through `retrieval_mode=lexical|graph|auto`

Run the stdio MCP server:

```bash
go run ./cmd/cortexdb-mcp-stdio
```

Or embed it directly in your own Go process:

```go
if err := db.RunMCPStdio(context.Background(), cortexdb.MCPServerOptions{}); err != nil {
	log.Fatal(err)
}
```

High-level Go APIs:

```go
knowledge, _ := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
	KnowledgeID: "doc-1",
	Title:       "Alice at Acme",
	Content:     "Alice works at Acme on GraphRAG.",
})

memory, _ := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
	MemoryID:  "mem-1",
	UserID:    "user-1",
	Scope:     cortexdb.MemoryScopeUser,
	Namespace: "assistant",
	Content:   "Alice prefers concise answers.",
})

_, _ = knowledge, memory
```

Main MCP tools:

- `knowledge_save`
- `knowledge_update`
- `knowledge_get`
- `knowledge_search`
- `knowledge_delete`
- `memory_save`
- `memory_update`
- `memory_get`
- `memory_search`
- `memory_delete`
- `ingest_document`
- `upsert_entities`
- `upsert_relations`
- `search_text`
- `search_chunks_by_entities`
- `expand_graph`
- `get_nodes`
- `get_chunks`
- `build_context`
- `search_graphrag_lexical`

### 5. Advanced Metadata Filtering

Filter large datasets before performing vector search using a SQL-like expression builder.

```go
// Find laptops under $2000 that are currently in stock
filter := core.NewMetadataFilter().
	And(core.NewMetadataFilter().Equal("category", "laptop")).
	And(core.NewMetadataFilter().LessThan("price", 2000.0)).
	And(core.NewMetadataFilter().GreaterThan("stock", 0)).
	Build()

opts := core.AdvancedSearchOptions{
	SearchOptions: core.SearchOptions{TopK: 5},
	PreFilter:     filter, 
}
results, _ := db.Vector().SearchWithAdvancedFilter(ctx, queryVec, opts)
```

## 📚 Database Schema

CortexDB manages the following tables automatically inside your single `.db` file:

| Table          | Description                                                   |
| :------------- | :------------------------------------------------------------ |
| `embeddings`   | Vectors, content, JSON metadata, ACLs.                        |
| `documents`    | Parent records for embeddings (Title, URL, Version).          |
| `sessions`     | Chat sessions / threads.                                      |
| `messages`     | Chat logs (Role, Content, Vector, Timestamp).                 |
| `messages_fts` | **FTS5** virtual table for BM25 keyword search over messages. |
| `collections`  | Logical namespaces for multi-tenancy.                         |
| `chunks_fts`   | **FTS5** virtual table for keyword search over embeddings.    |
| `graph_nodes`  | Knowledge graph nodes with vector embeddings.                 |
| `graph_edges`  | Directed relationships between graph nodes.                   |

## 📊 Performance (128-dim, Apple M2 Pro)

| Index Type | Insert Speed  | Search QPS | Memory (1 M vecs) |
| :--------- | :------------ | :--------- | :---------------- |
| **HNSW**   | ~580 ops/s    | ~720 QPS   | ~1.2 GB (SQ8)     |
| **IVF**    | ~14,500 ops/s | ~1,230 QPS | ~1.0 GB (SQ8)     |

## ⚖️ License

MIT License. See [LICENSE](LICENSE) file.
