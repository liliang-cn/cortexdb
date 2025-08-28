# sqvect

[![CI/CD](https://github.com/liliang-cn/sqvect/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/sqvect/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/liliang-cn/sqvect/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/sqvect)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/sqvect)](https://goreportcard.com/report/github.com/liliang-cn/sqvect)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/sqvect.svg)](https://pkg.go.dev/github.com/liliang-cn/sqvect)
[![GitHub release](https://img.shields.io/github/release/liliang-cn/sqvect.svg)](https://github.com/liliang-cn/sqvect/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**A lightweight, embeddable vector store in Go using SQLite.**

sqvect is a pure Go library that provides a simple, efficient vector storage solution using SQLite as the backend. Perfect for local RAG (Retrieval-Augmented Generation) applications, semantic search, and similarity matching without the complexity of external vector databases.

## ✨ Features

- 🚀 **No server required** – Single `.db` file storage
- 🔍 **Vector similarity search** with cosine, dot product, and Euclidean distance
- 📦 **Batch operations** for efficient data loading
- 🔒 **Thread-safe** operations with concurrent read/write support
- 🧩 **Pure Go implementation** – No external dependencies except SQLite driver
- 🎯 **Optimized for embeddings** – Built for AI/ML workflows
- 📊 **Rich metadata support** with JSON storage
- ⚡ **High performance** – Optimized for common vector operations
- 🔄 **Auto dimension adaptation** – Seamlessly handle vectors of different dimensions
- 🤖 **Zero configuration** – Works out of the box with any embedding model

## 🚀 Quick Start

### Installation

```bash
go get github.com/liliang-cn/sqvect
```

### Basic Usage

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/liliang-cn/sqvect"
)

func main() {
    // Create a new vector store with auto-dimension detection
    store, err := sqvect.New("embeddings.db", 0) // 0 = auto-detect
    if err != nil {
        log.Fatal(err)
    }
    defer store.Close()

    // Initialize the store
    ctx := context.Background()
    if err := store.Init(ctx); err != nil {
        log.Fatal(err)
    }

    // Insert BERT embeddings (768 dimensions)
    bertEmb := &sqvect.Embedding{
        ID:      "doc_1_chunk_1",
        Vector:  make([]float32, 768), // BERT dimensions
        Content: "This is BERT encoded text",
        DocID:   "document_1",
        Metadata: map[string]string{
            "source": "bert",
            "type":   "text",
        },
    }

    if err := store.Upsert(ctx, bertEmb); err != nil {
        log.Fatal(err)
    }

    // Insert OpenAI embeddings (1536 dimensions) - automatically adapted!
    openaiEmb := &sqvect.Embedding{
        ID:      "doc_2_chunk_1", 
        Vector:  make([]float32, 1536), // OpenAI dimensions
        Content: "This is OpenAI encoded text",
        DocID:   "document_2",
        Metadata: map[string]string{
            "source": "openai",
            "type":   "text",
        },
    }

    if err := store.Upsert(ctx, openaiEmb); err != nil {
        log.Fatal(err)
    }

    // Search with any dimension query - automatically adapted!
    query := make([]float32, 3072) // Even larger dimension works
    results, err := store.Search(ctx, query, sqvect.SearchOptions{
        TopK:      5,
        Threshold: 0.7,
    })
    if err != nil {
        log.Fatal(err)
    }

    // Process results
    for _, result := range results {
        fmt.Printf("Score: %.4f | Content: %s | Source: %s\n", 
            result.Score, result.Content, result.Metadata["source"])
    }
}
```

## 🔄 Automatic Dimension Adaptation

sqvect automatically handles vectors of different dimensions, making it easy to:

- **Switch between embedding models** (e.g., BERT 768D → OpenAI 1536D)
- **Mix different embedding sources** in the same database
- **Query with any dimension** without worrying about compatibility

### Adaptation Strategies

```go
config := sqvect.DefaultConfig()
config.Path = "vectors.db"
config.AutoDimAdapt = sqvect.SmartAdapt    // Default: intelligent adaptation
// config.AutoDimAdapt = sqvect.AutoTruncate // Always truncate to smaller
// config.AutoDimAdapt = sqvect.AutoPad      // Always pad to larger  
// config.AutoDimAdapt = sqvect.WarnOnly     // Only warn, no adaptation

store, err := sqvect.NewWithConfig(config)
```

### Zero Configuration Example

```go
// Just works - no dimension management needed!
store, _ := sqvect.New("vectors.db", 0)
store.Init(ctx)

// Insert any dimension
store.Upsert(ctx, &sqvect.Embedding{Vector: make([]float32, 384)})   // MiniLM
store.Upsert(ctx, &sqvect.Embedding{Vector: make([]float32, 768)})   // BERT  
store.Upsert(ctx, &sqvect.Embedding{Vector: make([]float32, 1536)})  // OpenAI
store.Upsert(ctx, &sqvect.Embedding{Vector: make([]float32, 3072)})  // Large models

// Search with any dimension
results, _ := store.Search(ctx, make([]float32, 2500), sqvect.SearchOptions{TopK: 10})
```
```

## 📖 API Documentation

### Core Interface

```go
type Store interface {
    Init(ctx context.Context) error
    Upsert(ctx context.Context, emb Embedding) error
    UpsertBatch(ctx context.Context, embs []Embedding) error
    Search(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error)
    Delete(ctx context.Context, id string) error
    DeleteByDocID(ctx context.Context, docID string) error
    Close() error
    Stats(ctx context.Context) (StoreStats, error)
}
```

### Data Structures

#### Embedding

```go
type Embedding struct {
    ID       string            // Unique identifier
    Vector   []float32         // Vector data
    Content  string            // Associated text content
    DocID    string            // Document identifier (optional)
    Metadata map[string]string // Additional metadata (optional)
}
```

#### SearchOptions

```go
type SearchOptions struct {
    TopK      int               // Number of results to return
    Filter    map[string]string // Metadata filtering
    Threshold float64           // Minimum similarity score
}
```

#### ScoredEmbedding

```go
type ScoredEmbedding struct {
    Embedding
    Score float64 // Similarity score
}
```

### Configuration

#### Basic Configuration

```go
// Auto-detect dimensions (recommended)
store, err := sqvect.New("data.db", 0)

// Fixed dimensions
store, err := sqvect.New("data.db", 768)
```

#### Advanced Configuration

```go
config := sqvect.Config{
    Path:         "embeddings.db",
    VectorDim:    0,                       // 0 = auto-detect, >0 = fixed
    AutoDimAdapt: sqvect.SmartAdapt,       // Dimension adaptation strategy
    SimilarityFn: sqvect.CosineSimilarity, // Similarity function
    HNSW: sqvect.HNSWConfig{               // Optional HNSW indexing
        Enabled:        true,
        M:              16,
        EfConstruction: 200,
        EfSearch:       50,
    },
}

store, err := sqvect.NewWithConfig(config)
```

#### Dimension Adaptation Policies

```go
type AdaptPolicy int

const (
    SmartAdapt   AdaptPolicy = iota // Intelligent adaptation (default)
    AutoTruncate                    // Always truncate to smaller dimension
    AutoPad                         // Always pad to larger dimension  
    WarnOnly                        // Only warn, don't auto-adapt
)
```

## 🔍 Similarity Functions

sqvect provides three built-in similarity functions:

### Cosine Similarity (Default)

```go
store := sqvect.New("data.db", 768)
// Uses cosine similarity by default
```

Best for:

- Text embeddings
- When vector magnitude doesn't matter
- Most embedding models (OpenAI, Sentence Transformers, etc.)

### Dot Product

```go
config := sqvect.DefaultConfig()
config.SimilarityFn = sqvect.DotProduct
store, _ := sqvect.NewWithConfig(config)
```

Best for:

- When vectors are already normalized
- Faster computation than cosine similarity

### Euclidean Distance

```go
config := sqvect.DefaultConfig()
config.SimilarityFn = sqvect.EuclideanDist
store, _ := sqvect.NewWithConfig(config)
```

Best for:

- When vector magnitude matters
- Image embeddings
- Spatial data

## 📊 Performance

Performance on Apple M2 Pro with 768-dimensional vectors:

| Operation                 | Performance   |
| ------------------------- | ------------- |
| Cosine Similarity         | ~1.2M ops/sec |
| Vector Encoding/Decoding  | ~38K ops/sec  |
| Single Upsert             | ~20K ops/sec  |
| Batch Search (1K vectors) | ~60 ops/sec   |

### Optimization Tips

1. **Use batch operations** for inserting multiple embeddings
2. **Set appropriate connection pool size** for concurrent workloads
3. **Use filtering** to reduce search space
4. **Normalize vectors** when using dot product similarity

## 🔧 Advanced Usage

### Batch Operations

```go
embeddings := []sqvect.Embedding{
    {ID: "1", Vector: vec1, Content: "Content 1"},
    {ID: "2", Vector: vec2, Content: "Content 2"},
    // ... more embeddings
}

// Much faster than individual Upserts
if err := store.UpsertBatch(ctx, embeddings); err != nil {
    log.Fatal(err)
}
```

### Filtering

```go
results, err := store.Search(ctx, query, sqvect.SearchOptions{
    TopK: 10,
    Filter: map[string]string{
        "doc_id": "specific_document",
        "category": "technical",
    },
})
```

### Document Management

```go
// List all documents
docIDs, err := store.ListDocuments(ctx)

// Get all embeddings for a specific document
embeddings, err := store.GetByDocID(ctx, "document_123")

// Get documents by type
articles, err := store.GetDocumentsByType(ctx, "article")

// Get detailed document information
docInfos, err := store.ListDocumentsWithInfo(ctx)
for _, info := range docInfos {
    fmt.Printf("Document %s has %d embeddings\n", info.DocID, info.EmbeddingCount)
}
```

### Bulk Operations

```go
// Clear specific documents
err := store.ClearByDocID(ctx, []string{"doc1", "doc2", "doc3"})

// Clear entire store
err := store.Clear(ctx)
```

### Statistics

```go
stats, err := store.Stats(ctx)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Embeddings: %d\n", stats.Count)
fmt.Printf("Dimensions: %d\n", stats.Dimensions)
fmt.Printf("DB Size: %d bytes\n", stats.Size)
```

## 📋 Examples

### Basic Usage

See [examples/basic](examples/basic) for a simple 3D vector example.

### Dimension Adaptation

See [examples/dimension_adapt](examples/dimension_adapt) for automatic dimension handling with different embedding models.

For detailed documentation on dimension adaptation, see [DIMENSION.md](DIMENSION.md).

### Advanced Features

See [examples/advanced](examples/advanced) for batch operations, metadata filtering, and performance comparisons.

### Benchmarks

See [examples/benchmark](examples/benchmark) for comprehensive performance testing across different vector dimensions.

## 🏗️ Architecture

sqvect is built with the following architectural principles:

- **Single Responsibility**: Each component has a focused purpose
- **Interface-Driven**: Core functionality exposed through clean interfaces
- **Concurrent Safe**: All operations are thread-safe using read-write mutexes
- **Resource Management**: Proper resource cleanup and connection pooling
- **Error Handling**: Comprehensive error wrapping with context

### Database Schema

```sql
CREATE TABLE embeddings (
    id TEXT PRIMARY KEY,
    vector BLOB NOT NULL,        -- Encoded float32 array
    content TEXT NOT NULL,
    doc_id TEXT,
    metadata TEXT,               -- JSON encoded
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_embeddings_doc_id ON embeddings(doc_id);
CREATE INDEX idx_embeddings_created_at ON embeddings(created_at);
```

## 🧪 Testing

Run the test suite:

```bash
# Run all tests
go test ./vectorstore -v

# Run with coverage
go test ./vectorstore -cover

# Run benchmarks
go test ./vectorstore -bench=.
```

## 🤝 Contributing

Contributions are welcome! Please ensure:

1. All tests pass: `go test ./...`
2. Code is formatted: `go fmt ./...`
3. Code is linted: `go vet ./...`
4. Add tests for new functionality
5. Update documentation as needed

## 📚 Use Cases

### RAG Applications

```go
// Store document chunks with embeddings
store.UpsertBatch(ctx, documentChunks)

// Find relevant context for user query
results, _ := store.Search(ctx, queryEmbedding, sqvect.SearchOptions{TopK: 3})
```

### Semantic Search

```go
// Index product descriptions
store.Upsert(ctx, sqvect.Embedding{
    ID: "product_123",
    Vector: productEmbedding,
    Content: "Wireless bluetooth headphones with noise cancellation",
    Metadata: map[string]string{"category": "electronics", "price": "99.99"},
})

// Search with natural language
results, _ := store.Search(ctx, searchQueryEmbedding, sqvect.SearchOptions{TopK: 10})
```

### Document Clustering

```go
// Find similar documents
allEmbeddings := getAllDocumentEmbeddings()
for _, emb := range allEmbeddings {
    similar, _ := store.Search(ctx, emb.Vector, sqvect.SearchOptions{
        TopK: 5,
        Threshold: 0.8,
    })
    // Process similar documents
}
```

## ⚖️ License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- SQLite for providing an excellent embedded database
- The Go community for excellent tooling and libraries
- Vector database research that inspired this implementation

---
