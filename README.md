# sqvect

[![CI/CD](https://github.com/liliang-cn/sqvect/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/sqvect/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/liliang-cn/sqvect/v2/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/sqvect/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/sqvect/v2)](https://goreportcard.com/report/github.com/liliang-cn/sqvect/v2)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/sqvect/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/sqvect/v2)
[![GitHub release](https://img.shields.io/github/release/liliang-cn/sqvect.svg)](https://github.com/liliang-cn/sqvect/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**A lightweight, embeddable vector database LIBRARY for Go AI projects.**

sqvect is a **100% pure Go library** designed to be the storage kernel for your RAG applications. It provides vector storage, keyword search (FTS5), graph relationships, and chat memory management in a single SQLite file.

## ✨ Features

- 🪶 **Lightweight** – Single SQLite file, zero external dependencies.
- 🚀 **RAG-Ready** – Built-in tables for **Documents**, **Chat Sessions**, and **Messages**.
- 🔍 **Hybrid Search** – Combine **Vector Search (HNSW)** + **Keyword Search (FTS5)** with RRF fusion.
- 🧠 **AI Agent Memory** – **Hindsight** system for long-term agent memory (World, Bank, Opinion, Observation).
- 🛡️ **Secure** – Row-Level Security (RLS) via **ACL** fields and query filtering.
- 🕸️ **Graph Storage** – Built-in knowledge graph with entity relationships.
- 📊 **Quantization** – **SQ8 Quantization** reduces RAM usage by 75%.
- ⚡ **High Performance** – Optimized WAL mode, concurrent access.
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
    "github.com/liliang-cn/sqvect/v2/pkg/sqvect"
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

## 💡 Why sqvect?

### Key Advantages

**🎯 All-in-One RAG Storage**
- Stop managing separate databases for vectors, documents, and chat history
- Single SQLite file = easy backup, migration, and version control
- Perfect for edge deployment and local-first applications

**🚀 Developer Experience**
- Zero configuration - works out of the box
- Type-safe Go API with full IntelliSense support
- Built-in RAG schemas (no ORM/SQL required)
- Comprehensive examples for common use cases

**⚡ Performance & Efficiency**
- SQ8 quantization reduces memory by 75% (1M vectors ~1GB)
- Multiple index types (HNSW, IVF, LSH) for different workloads
- WAL mode + connection pooling for concurrent access
- Efficient distance calculations

**🔒 Security First**
- Row-Level Security (ACL) built into the core
- User-scoped queries enforce permission boundaries
- No data leakage between tenants

**🧪 Production Ready**
- 93% test coverage on core APIs
- Battle-tested algorithms (HNSW, RRF, PQ)
- CI/CD + Codecov + Go Report Card badges
- MIT license for easy integration

## 🧠 Hindsight: AI Agent Memory System

sqvect includes **Hindsight**, a biomimetic memory system for AI agents that mirrors how human memory works. Inspired by [vectorize-io/hindsight](https://github.com/vectorize-io/hindsight), it enables agents to learn and improve over time.

### Three Core Operations

```go
import "github.com/liliang-cn/sqvect/v2/pkg/hindsight"

sys, _ := hindsight.New(&hindsight.Config{DBPath: "agent_memory.db"})

// RETAIN: Store memories (caller provides embeddings)
sys.Retain(ctx, &hindsight.Memory{
    Type:     hindsight.WorldMemory,
    Content:  "Alice works at Google as a senior engineer",
    Vector:   embedding,
    Entities: []string{"Alice", "Google"},
})

// RECALL: Search using TEMPR strategies (Temporal, Entity, Memory, Priming)
results, _ := sys.Recall(ctx, &hindsight.RecallRequest{
    BankID:      "agent-1",
    QueryVector: queryEmbedding,
    Strategy:    hindsight.DefaultStrategy(),
})

// OBSERVE: Reflect on memories to generate new insights
resp, _ := sys.Observe(ctx, &hindsight.ReflectRequest{
    BankID:      "agent-1",
    Query:       "What does Alice prefer?",
    QueryVector: queryEmbedding,
})
// resp.Observations contains newly generated insights
```

### Four Memory Types

| Type | Description | Example |
|:---|:---|:---|
| **World** | Objective facts about the world | "Alice works at Google" |
| **Bank** | Agent's own experiences | "I recommended Python to Bob" |
| **Opinion** | Beliefs with confidence scores | "Python is best for ML" (0.85) |
| **Observation** | Insights derived from reflection | "Users prefer concise answers" |

### TEMPR Retrieval Strategies

Hindsight runs four search strategies in parallel and fuses results with RRF:

- **T**emporal – Time-range filtered search
- **E**ntity – Graph-based entity relationships
- **M**emory – Semantic vector similarity
- **P**riming – Keyword/BM25 exact matching
- **R**ecall – RRF fusion for ranked results

### Memory Banks & Disposition

```go
// Create a memory bank with personality traits
bank := hindsight.NewBank("agent-1", "Assistant Agent")
bank.Skepticism = 3  // 1=Trusting, 5=Skeptical
bank.Literalism = 3  // 1=Flexible, 5=Literal
bank.Empathy = 4     // 1=Detached, 5=Empathetic
sys.CreateBank(ctx, bank)
```

**Why Hindsight Matters**
- Agents form **opinions** with confidence scores (not just retrieve facts)
- **Disposition traits** influence how observations are generated
- Agents **learn from experience** – observations persist across sessions
- Pure memory system – no LLM dependency (caller handles embeddings)

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
| `graph_nodes` | Graph nodes for entity relationships. |
| `graph_edges` | Directed edges between nodes (with weights). |
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

## 🎯 Best Use Cases

### Perfect For ✅

| Use Case | Why sqvect? |
|:---|:---|
| **Local-First RAG Apps** | Single file, no server, works offline |
| **AI Agent Memory** | Hindsight system with TEMPR retrieval |
| **Edge AI Devices** | Low memory (SQ8), no external deps, pure Go |
| **Personal Knowledge Bases** | Simple backup (copy file), easy to query |
| **Internal Tools** | Fast setup, no DevOps overhead |
| **Chat Memory Systems** | Built-in sessions/messages tables |
| **Multi-Tenant SaaS** | ACL + Collections for isolation |
| **Document Clustering** | Graph algorithms (PageRank, community detection) |
| **Hybrid Search Apps** | Vector + FTS5 with RRF fusion |
| **Prototype to Production** | Same code from dev to prod (just scale up) |

### Not Recommended For ❌

| Scenario | Better Alternative |
|:---|:---|
| >100M vectors | Milvus, Qdrant (distributed) |
| <10ms latency requirements | Redis-based vector DB |
| Multi-region HA | Cloud-native vector DB (Pinecone) |
| Non-Go teams | Chroma (Python), Weaviate |

### Real-World Examples

- **AI Agent Memory**: Long-term memory for agents using Hindsight (World, Bank, Opinion, Observation)
- **Legal Document Analysis**: Store contracts, clauses, and case law with metadata filters
- **Customer Support Chatbot**: Persistent conversation history + knowledge base search
- **Code Search Engine**: Semantic code search + syntax-aware filtering
- **Research Paper Graph**: Citation network + vector similarity
- **E-commerce Recommendations**: User embeddings + product graph

## 📊 Comparison with Alternatives

### Vector Database Comparison

| Feature | sqvect | Chroma | Weaviate | Milvus | Qdrant |
|:---|:---:|:---:|:---:|:---:|:---:|
| **Architecture** | Embedded | Server | Server | Distributed | Server |
| **Language** | Go | Python | Go | Go | Rust |
| **Dependencies** | SQLite only | DuckDB | Vector+Obj | Many | Many |
| **Setup Time** | ~1 sec | ~5 min | ~10 min | ~30 min | ~10 min |
| **Vector Search** | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Keyword Search** | ✅ FTS5 | ❌ | ⚠️ | ❌ | ❌ |
| **Graph DB** | ✅ Built-in | ❌ | ❌ | ❌ | ❌ |
| **RAG Tables** | ✅ Ready | ❌ DIY | ❌ DIY | ❌ DIY | ❌ DIY |
| **ACL/Security** | ✅ Row-level | ❌ | ⚠️ | ⚠️ | ⚠️ |
| **Quantization** | SQ8/PQ/Binary | ❌ | ✅ | ✅ | ✅ |
| **Scalability** | <10M | <100M | <1B | >1B | <1B |
| **Backup** | Copy file | Export | Snapshot | Complex | Snapshot |
| **Ideal For** | Edge/Local | Python ML | Enterprise | Big Data | Production |

### When to Choose sqvect?

**Choose sqvect if:**
- ✅ You want a **single-file** database (no separate services)
- ✅ You're building **local-first** or **edge AI** applications
- ✅ You need **built-in RAG schemas** (docs, sessions, messages)
- ✅ You want **graph algorithms** without Neo4j
- ✅ You value **simplicity** over horizontal scalability
- ✅ You're targeting **<10 million vectors**

**Choose alternatives if:**
- ❌ You need **distributed** deployment across multiple nodes
- ❌ You have **>100M vectors** and need horizontal scaling
- ❌ You require **sub-10ms** query latency
- ❌ Your team doesn't use Go (prefer Python/TypeScript SDKs)

### Unique Differentiators

🎯 **No other vector DB combines:**
1. Vector + Graph + Document + Chat + **Agent Memory (Hindsight)** in ONE file
2. Built-in RAG schemas (zero design work)
3. **Hindsight**: biomimetic memory system for AI agents (TEMPR retrieval)
4. Row-Level Security without external auth
5. Edge deployment ready (no network/containers)
6. Pure Go (cross-compile to any platform)

## ⚖️ License

MIT License. See [LICENSE](LICENSE) file.
