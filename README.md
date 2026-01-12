# sqvect

[![CI/CD](https://github.com/liliang-cn/sqvect/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/sqvect/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/liliang-cn/sqvect/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/sqvect)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/sqvect)](https://goreportcard.com/report/github.com/liliang-cn/sqvect)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/sqvect.svg)](https://pkg.go.dev/github.com/liliang-cn/sqvect)
[![GitHub release](https://img.shields.io/github/release/liliang-cn/sqvect.svg)](https://github.com/liliang-cn/sqvect/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**A lightweight, embeddable vector database LIBRARY for Go AI projects.**

sqvect is **NOT** a standalone service. It is a **100% pure Go library** designed to be directly embedded into your application. It provides fast, reliable vector storage backed by SQLite without any external dependencies or complex infrastructure.

Perfect for RAG systems, semantic search, local AI agents, and desktop applications where deploying a dedicated vector DB (like Milvus or Qdrant) is overkill.

## ✨ Why sqvect?

### 1. Minimalist Library Design

sqvect is designed to be imported and used like any other Go package.

*   **Zero Configuration**: Just `import`, `Open()`, and `Upsert()`. No Docker containers, no config files, no ports to manage.
*   **Pure Go**: Built on `modernc.org/sqlite`, meaning **NO CGO** required. Cross-compilation works out of the box.
*   **Single File DB**: All data (vectors, metadata, indexes) lives in a single `.db` file. Easy to backup, move, or delete.

### 2. Dual-Layer API

We offer two levels of abstraction to balance ease of use and flexibility:

```text
         ┌─────────────────┐
         │   sqvect.Open() │
         └────────┬────────┘
                  │
      ┌───────────┴───────────┐
      ▼                       ▼
┌──────────┐          ┌──────────────┐
│ Quick    │          │ Full API     │
│ (Facade) │          │ (Power User) │
└──────────┘          └──────────────┘
```

*   **Quick API**: For 90% of use cases (add, search, collections).
*   **Full API**: For advanced control (indexing tuning, graph operations, batching).

### 3. Production Ready Features

*   **Thread Safe**: Fully concurrent-safe for high-throughput web services.
*   **HNSW & IVF Indexing**: Choose between HNSW for speed or IVF for write-heavy workloads.
*   **GraphRAG Ready**: Built-in support for knowledge graphs and hybrid search.
*   **Auto Dimension Adaptation**: Seamlessly switch embedding models without migration.

## 🚀 Quick Start

### Installation

```bash
go get github.com/liliang-cn/sqvect
```

### 3-Line "Hello World"

```go
package main

import (
    "context"
    "fmt"
    "github.com/liliang-cn/sqvect/pkg/sqvect"
)

func main() {
    // 1. Open the database (creates 'vectors.db' if missing)
    db, _ := sqvect.Open(sqvect.DefaultConfig("vectors.db"))
    defer db.Close()

    // 2. Add a vector
    ctx := context.Background()
    quick := db.Quick()
    quick.Add(ctx, []float32{0.1, 0.2, 0.9}, "Go is awesome")

    // 3. Search
    results, _ := quick.Search(ctx, []float32{0.1, 0.2, 0.8}, 1)
    fmt.Printf("Found: %s\n", results[0].Content)
}
```

## 📚 Library Usage Patterns

### 1. Embed in a Web Service

sqvect is thread-safe, making it ideal for embedding directly into HTTP handlers.

```go
func main() {
    // Initialize once at startup
    db, _ := sqvect.Open(sqvect.DefaultConfig("app_data.db"))
    quick := db.Quick()
    defer db.Close()

    // Use safely in concurrent handlers
    http.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
        query := parseQueryVector(r)
        
        // Thread-safe search
        results, _ := quick.Search(r.Context(), query, 10)
        
        json.NewEncoder(w).Encode(results)
    })
    
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

### 2. Concurrent Batch Processing

For high-throughput ingestion, use Go routines. sqvect handles the locking internally.

```go
var wg sync.WaitGroup
for i := 0; i < 10; i++ {
    wg.Add(1)
    go func(workerID int) {
        defer wg.Done()
        // Concurrent writes are safe and serialized by SQLite WAL mode
        quick.Add(ctx, vectors[workerID], fmt.Sprintf("Data %d", workerID))
    }(i)
}
wg.Wait()
```

### 3. Choosing Your Index Type

sqvect supports different indexing strategies for different needs.

#### HNSW (Hierarchical Navigable Small World)
*   **Best for**: Real-time search, high recall, incremental updates.
*   **Default**: Yes.

```go
config := sqvect.DefaultConfig("vectors.db")
config.IndexType = core.IndexTypeHNSW // Default
```

#### IVF (Inverted File Index)
*   **Best for**: Bulk loading, write-heavy scenarios, faster indexing than HNSW.
*   **Note**: Requires training (or auto-training on sufficient data).

```go
config := sqvect.DefaultConfig("vectors.db")
config.IndexType = core.IndexTypeIVF
db, _ := sqvect.Open(config)

// ... insert data ...

// Train the index for optimal performance
db.Vector().TrainIndex(ctx, 100) // 100 centroids
```

## 📖 API Documentation

### Core Interface

The `Store` interface is the heart of the library. You can wrap or mock this for testing.

```go
type Store interface {
    Init(ctx context.Context) error
    Upsert(ctx context.Context, emb Embedding) error
    UpsertBatch(ctx context.Context, embs []Embedding) error
    Search(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error)
    Delete(ctx context.Context, id string) error
    Close() error
    // ... collections, stats, etc.
}
```

### Advanced: Collections (Multi-Tenancy)

Keep data for different users or types separate within the same DB file.

```go
// Create collections
db.Vector().CreateCollection(ctx, "users", 768)
db.Vector().CreateCollection(ctx, "products", 768)

// Target specific collections
quick.AddToCollection(ctx, "users", userVec, "User Profile")
quick.SearchInCollection(ctx, "products", queryVec, 5)
```

### Advanced: Knowledge Graph RAG

Combine vector similarity with graph relationships for smarter RAG.

```go
graphStore := db.Graph()

// 1. Add nodes (documents/entities)
graphStore.UpsertNode(ctx, &graph.GraphNode{ID: "doc1", Vector: vec1})

// 2. Link them (relationships)
graphStore.UpsertEdge(ctx, &graph.GraphEdge{
    FromNodeID: "doc1", 
    ToNodeID: "doc2", 
    EdgeType: "references"
})

// 3. Hybrid Search (Vector Similarity + Graph Proximity)
results, _ := graphStore.HybridSearch(ctx, &graph.HybridQuery{
    Vector: queryVec,
    StartNodeID: "doc1", // Start exploration here
    GraphWeight: 0.3,    // 30% importance on graph structure
})
```

## 📊 Performance

Performance on Apple M2 Pro (128-dim vectors):

| Index Type | Insert Speed | Search QPS | Build Time |
| :--- | :--- | :--- | :--- |
| **Flat** | ~15,800 ops/s | ~30 QPS | Instant |
| **HNSW** | ~580 ops/s | ~720 QPS | Incremental |
| **IVF** | ~14,500 ops/s | ~1,230 QPS | Fast (Post-training) |

*Results from `examples/benchmark_ivf`.*

## ⚖️ License

MIT License. See [LICENSE](LICENSE) file.

## 🙏 Acknowledgments

- [modernc.org/sqlite](https://modernc.org/sqlite) for the amazing pure Go SQLite port.
- The Go community.

---