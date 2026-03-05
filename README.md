# sqvect

[![CI/CD](https://github.com/liliang-cn/sqvect/v2/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/sqvect/v2/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/liliang-cn/sqvect/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/sqvect)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/sqvect/v2)](https://goreportcard.com/report/github.com/liliang-cn/sqvect/v2)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/sqvect/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/sqvect/v2)
[![GitHub release](https://img.shields.io/github/release/liliang-cn/sqvect.svg)](https://github.com/liliang-cn/sqvect/v2/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**A lightweight, embeddable vector database library for Go AI projects.**

sqvect is a **100% pure Go library** that bundles vector storage, keyword search (FTS5), knowledge graph relationships, and a Hindsight-inspired AI Agent memory system into a **single SQLite file** — no external services required.

## ✨ Features

- 🪶 **Lightweight** – Single SQLite file, zero external dependencies.
- 🚀 **RAG-Ready** – Built-in tables for **Documents**, **Chat Sessions**, and **Messages**.
- 🔍 **Hybrid Search** – **Vector (HNSW/IVF)** + **Keyword (FTS5)** with RRF fusion.
- 🧠 **AI Agent Memory** – Full `retain → recall → reflect` lifecycle with TEMPR retrieval.
- 🛡️ **Secure** – Row-Level Security via **ACL** fields and query filtering.
- 📦 **Memory Efficient** – **SQ8 Quantization** reduces RAM by 75%.
- ⚡ **High Performance** – WAL mode, HNSW index, concurrent-safe.
- 🎯 **Zero Config** – Works out of the box.

## 🚀 Quick Start

```bash
go get github.com/liliang-cn/sqvect/v2
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

func main() {
	// Open database (auto-creates tables for vectors, docs, chat)
	db, err := sqvect.Open(sqvect.DefaultConfig("app.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// Add a vector with content
	db.Quick().Add(ctx, []float32{0.1, 0.2, 0.9}, "Go is a statically typed language")

	// Search for similar vectors
	results, _ := db.Quick().Search(ctx, []float32{0.1, 0.2, 0.8}, 1)
	if len(results) > 0 {
		fmt.Println(results[0].Content)
	}
}
```

## 🏗 Capabilities

### 1. Hybrid Search (Vector + Keyword)

Combine semantic understanding with precise keyword matching using Reciprocal Rank Fusion (RRF).

```go
results, _ := db.Vector().HybridSearch(ctx, queryVec, "apple", core.HybridSearchOptions{
	TopK: 5,
	RRFK: 60,
})
```

### 2. Knowledge Graph

Store entities and relationships alongside vector embeddings.

```go
db.Graph().InitGraphSchema(ctx)

db.Graph().UpsertNode(ctx, &graph.GraphNode{
	ID:       "alice",
	NodeType: "person",
	Content:  "Alice is a software engineer",
	Vector:   []float32{0.1, 0.2, 0.3}, // Example vector
})

db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
	FromNodeID: "alice",
	ToNodeID:   "google",
	EdgeType:   "works_at",
	Weight:     1.0,
})
```

### 3. AI Agent Memory (Hindsight-style)

`pkg/hindsight` implements the full **retain → recall → reflect** lifecycle with a four-channel
TEMPR retrieval pipeline and RRF fusion — all over SQLite, zero external services.

#### Architecture

```
retain()   →  sqvect embeddings collection ("memories")
                ├── WorldMemory      (objective facts about the world)
                ├── BankMemory       (agent's own past actions)
                ├── OpinionMemory    (formed beliefs with confidence)
                └── ObservationMemory (insights derived from reflection)

recall()   →  TEMPR × 4 channels (concurrent)
                ├── T Temporal  — time-range filtered search
                ├── E Entity    — graph-based entity relationships
                ├── M Memory    — semantic vector similarity
                └── P Priming   — BM25 FTS5 keyword search
              ↓
              RRF fusion  →  optional RerankerFn hook  →  ranked results

observe()  →  Disposition (Skepticism / Literalism / Empathy)
              ↓ derives new Observations from patterns in recalled memories

reflect()  →  formatted context string (ready for LLM injection)
```

#### Basic Usage

```go
import "github.com/liliang-cn/sqvect/v2/pkg/hindsight"

sys, _ := hindsight.New(&hindsight.Config{
	DBPath: "agent.db",
})
defer sys.Close()

// Create a memory bank with personality traits
bank := hindsight.NewBank("agent-1", "Travel Assistant")
bank.Empathy = 4
sys.CreateBank(ctx, bank)

// Retain: store a structured fact
sys.Retain(ctx, &hindsight.Memory{
	ID:      "home_city",
	BankID:  "agent-1",
	Type:    hindsight.WorldMemory,
	Content: "Alice lives in Berlin",
	Vector:  []float32{0.1, 0.2, 0.3},
})

// Recall: four-channel TEMPR retrieval + RRF fusion
results, _ := sys.Recall(ctx, &hindsight.RecallRequest{
	BankID:      "agent-1",
	Query:       "Where does Alice live?",
	QueryVector: queryVec,
	Strategy:    hindsight.DefaultStrategy(),
	TopK:        5,
})

// Reflect: get LLM-ready formatted context
ctxResp, _ := sys.Reflect(ctx, &hindsight.ContextRequest{
	BankID:      "agent-1",
	Query:       "Where does Alice live?",
	QueryVector: queryVec,
	TopK:        4,
})
// ctxResp.Context – ready for LLM system message injection
```

#### Extensibility Hooks

Two injection points let you plug in any LLM or model without coupling to a specific provider.

**Hook 1 — `FactExtractorFn`: automatic fact extraction**

```go
sys.SetFactExtractor(func(ctx context.Context, bankID string, msgs []*core.Message) ([]hindsight.ExtractedFact, error) {
	// Call your LLM / model to extract structured facts + compute embeddings
	return []hindsight.ExtractedFact{
		{
			ID:      "lang_pref",
			Type:    hindsight.WorldMemory,
			Content: "Alice prefers Go",
			Vector:  []float32{0.1, 0.2, 0.3},
		},
	}, nil
})

// Feed raw conversation messages – extraction + retention happens automatically
result, err := sys.RetainFromText(ctx, "agent-1", messages)
// result.Retained / result.Skipped / result.Err()
```

**Hook 2 — `RerankerFn`: cross-encoder reranking after RRF**

```go
sys.SetReranker(func(ctx context.Context, query string, candidates []*hindsight.RecallResult) ([]*hindsight.RecallResult, error) {
	// Call your cross-encoder / Cohere Rerank / LLM scorer
	scores := crossEncoder.Score(query, texts(candidates))
	sort.Slice(candidates, func(i, j int) bool {
		return scores[i] > scores[j]
	})
	return candidates, nil
})
// Recall() applies reranking automatically. Errors silently fall back to RRF order.
```

**Deriving observations via `Observe`**

```go
resp, _ := sys.Observe(ctx, &hindsight.ReflectRequest{
	BankID:      "agent-1",
	Query:       "What patterns can we infer about Alice?",
	QueryVector: queryVec,
	Strategy:    hindsight.DefaultStrategy(),
})
// resp.Observations – new insights auto-derived from recalled memories
```

### 4. Text & Structured Data APIs

sqvect provides high-level APIs for working directly with text (auto-embedding) and structured data.

```go
// Configure an embedder
db, _ := sqvect.Open(config, sqvect.WithEmbedder(myOpenAIEmbedder))

// Check DB Configuration
info := db.Info()
fmt.Println("Dimensions:", info.Dimensions)

// Auto-embed and insert text
db.InsertText(ctx, "doc_1", "SQLite is awesome", map[string]string{"type": "database"})

// Search directly with text
results, _ := db.SearchText(ctx, "fast database", 5)

// FTS5 Keyword-only search (no embeddings needed!)
textResults, _ := db.SearchTextOnly(ctx, "fast database", sqvect.TextSearchOptions{TopK: 5})
```

*See `examples/structured_data` and `examples/text_api` for advanced RAG patterns (Textification, GraphRAG, SQL-entity memory).*

### 5. Row-Level Security (ACL)

```go
db.Vector().Upsert(ctx, &core.Embedding{
	ID:     "secret",
	Vector: vec,
	ACL:    []string{"group:admin", "user:alice"},
})

results, _ := db.Vector().SearchWithACL(ctx, queryVec, []string{"user:bob"}, opts)
// Returns nothing for Bob
```

### 6. Document Management

```go
db.Vector().CreateDocument(ctx, &core.Document{
	ID:    "manual_v1",
	Title: "User Manual",
	Version: 1,
})
// ... add embeddings linked to manual_v1 ...
db.Vector().DeleteDocument(ctx, "manual_v1") // cascades to all chunks
```

## 📚 Database Schema

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
