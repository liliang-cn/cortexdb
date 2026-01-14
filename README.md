# sqvect

[![CI/CD](https://github.com/liliang-cn/sqvect/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/sqvect/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/liliang-cn/sqvect/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/sqvect)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/sqvect)](https://goreportcard.com/report/github.com/liliang-cn/sqvect)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/sqvect.svg)](https://pkg.go.dev/github.com/liliang-cn/sqvect)
[![GitHub release](https://img.shields.io/github/release/liliang-cn/sqvect.svg)](https://github.com/liliang-cn/sqvect/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**A lightweight, embeddable vector database LIBRARY for Go AI projects.**

sqvect is a **100% pure Go library** designed to be the storage kernel for your RAG applications. It provides vector storage, keyword search (FTS5), graph relationships, and chat memory management in a single SQLite file.

## ✨ Features

- 🪶 **Lightweight** – Single SQLite file, zero external dependencies.
- 🚀 **RAG-Ready** – Built-in tables for **Documents**, **Chat Sessions**, and **Messages**.
- 🔍 **Hybrid Search** – Combine **Vector Search (HNSW)** + **Keyword Search (FTS5)** with RRF fusion.
- 🛡️ **Secure** – Row-Level Security (RLS) via **ACL** fields and query filtering.
- 🧠 **Memory Efficient** – **SQ8 Quantization** reduces RAM usage by 75%.
- ⚡ **High Performance** – Optimized WAL mode, SIMD-ready distance calcs.
- 🎯 **Zero Config** – Works out of the box.

## 🚀 Quick Start

```bash
go get github.com/liliang-cn/sqvect
```

```go
package main

import (
    "context"
    "fmt"
    "github.com/liliang-cn/sqvect/pkg/sqvect"
)

func main() {
    // 1. Open DB (auto-creates tables for vectors, docs, chat)
    db, _ := sqvect.Open(sqvect.DefaultConfig("rag.db"))
    defer db.Close()
    ctx := context.Background()

    // 2. Add a Document & Vector
    // sqvect manages the relationship between docs and chunks
    db.Vector().CreateDocument(ctx, &core.Document{ID: "doc1", Title: "Go Guide"})
    
    db.Quick().Add(ctx, []float32{0.1, 0.2, 0.9}, "Go is awesome")

    // 3. Search
    results, _ := db.Quick().Search(ctx, []float32{0.1, 0.2, 0.8}, 1)
    fmt.Printf("Found: %s\n", results[0].Content)
}
```

## 🏗 Enterprise RAG Capabilities

sqvect goes beyond simple vector storage. It provides the schema and APIs needed for complex RAG apps.

### 1. Hybrid Search (Vector + Keyword)
Combine semantic understanding with precise keyword matching using Reciprocal Rank Fusion (RRF).

```go
// Search for "apple" (keyword) AND vector similarity
results, _ := db.Vector().HybridSearch(ctx, queryVec, "apple", core.HybridSearchOptions{
    TopK: 5,
    RRFK: 60, // Fusion parameter
})
```

### 2. Chat Memory Management
Store conversation history directly alongside your data.

```go
// 1. Create a session
db.Vector().CreateSession(ctx, &core.Session{ID: "sess_1", UserID: "user_123"})

// 2. Add messages (User & Assistant)
db.Vector().AddMessage(ctx, &core.Message{
    SessionID: "sess_1",
    Role:      "user",
    Content:   "What is sqvect?",
})

// 3. Retrieve history for context window
history, _ := db.Vector().GetSessionHistory(ctx, "sess_1", 10)
```

### 3. Row-Level Security (ACL)
Enforce permissions at the database level.

```go
// Insert restricted document
db.Vector().Upsert(ctx, &core.Embedding{
    ID: "secret_doc", 
    Vector: vec, 
    ACL: []string{"group:admin", "user:alice"}, // Only admins and Alice
})

// Search with user context (auto-filters results)
results, _ := db.Vector().SearchWithACL(ctx, queryVec, []string{"user:bob"}, opts)
// Returns nothing for Bob!
```

### 4. Document Management
Track source files, versions, and metadata. Deleting a document automatically deletes all its vector chunks (Cascading Delete).

```go
db.Vector().CreateDocument(ctx, &core.Document{
    ID: "manual_v1", 
    Title: "User Manual",
    Version: 1,
})
// ... add embeddings linked to "manual_v1" ...

// Delete document and ALL its embeddings in one call
db.Vector().DeleteDocument(ctx, "manual_v1")
```

## 📚 Database Schema

sqvect manages these tables for you:

| Table | Description | 
| :--- | :--- |
| `embeddings` | Vectors, content, JSON metadata, ACLs. |
| `documents` | Parent records for embeddings (Title, URL, Version). |
| `sessions` | Chat sessions/threads. |
| `messages` | Chat logs (Role, Content, Timestamp). |
| `collections` | Logical namespaces (Multi-tenancy). |
| `chunks_fts` | **FTS5** virtual table for keyword search. |

## 📊 Performance (128-dim)

| Index Type | Insert Speed | Search QPS | Memory (1M vecs) |
| :--- | :--- | :--- | :--- |
| **HNSW** | ~580 ops/s | ~720 QPS | ~1.2 GB (SQ8) |
| **IVF** | ~14,500 ops/s | ~1,230 QPS | ~1.0 GB (SQ8) |

*Tested on Apple M2 Pro.*

## ⚖️ License

MIT License. See [LICENSE](LICENSE) file.
