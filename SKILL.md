---
name: cortexdb
description: Use CortexDB for vector search, hybrid search, knowledge graphs, and RAG. Use when working with embeddings, similarity search, semantic search, AI knowledge bases, or when the user mentions vectors, embeddings, RAG, knowledge graph, or CortexDB.
---

# CortexDB

CortexDB is a lightweight SQLite-based vector database for Go AI projects. It supports vector similarity search, hybrid search (vector + keyword), full-text search (FTS5/BM25), knowledge graphs, and memory APIs.

## Core Concepts

### Vector Search
Store and search embeddings (vector representations of data). CortexDB supports multiple index types (HNSW, IVF, Flat) and similarity functions (cosine, dot product, euclidean).

### Hybrid Search
Combines vector search with FTS5 keyword search using Reciprocal Rank Fusion (RRF).

### Lexical Fallback (v2.13.0+)
When no embedder is configured, `SearchText` and `HybridSearchText` automatically fall back to FTS5/BM25 full-text search.

### LLM-Assisted Retrieval (v2.13.0+)
Query expansion using LLM-generated keywords and alternate query phrasings for improved FTS5 recall.

### External Vector Support (v2.13.0+)
APIs for ingesting pre-computed vectors from external pipelines without requiring an embedder.

## Installation

```go
import "github.com/liliang-cn/cortexdb/v2"
```

## Quick Start

```go
import "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"

// Open database
db, err := cortexdb.Open(cortexdb.DefaultConfig("/path/to/db"))
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// Quick operations
q := db.Quick()

// Add vector with auto ID
id, err := q.Add(ctx, []float32{0.1, 0.2, 0.3}, "document content")

// Search
results, err := q.Search(ctx, []float32{0.1, 0.2, 0.3}, 10)

// Text search (requires embedder)
results, err = q.SearchText(ctx, "query text", 10)

// Text-only search (no embedder, uses FTS5)
results, err = q.SearchTextOnly(ctx, "query", 10)
```

## Key APIs

### DB.Open / DB.Quick

```go
// Open with defaults
db, _ := cortexdb.Open(cortexdb.DefaultConfig("db.sqlite"))

// Open with custom config
db, _ := cortexdb.Open(cortexdb.Config{
    Path:         "db.sqlite",
    Dimensions:   384,           // Vector dimension
    IndexType:    core.IndexTypeHNSW,
    SimilarityFn: core.CosineSimilarity,
})

// Quick interface for simple operations
q := db.Quick()
```

### With Embedder

```go
// Create embedder (example with OpenAI-compatible API)
embedder := ollama.NewEmbedder("http://localhost:11434/api/embed", "nomic-embed-text")

// Open with embedder
db, _ := cortexdb.Open(
    cortexdb.DefaultConfig("db.sqlite"),
    cortexdb.WithEmbedder(embedder),
)

// Now text operations work
id, _ := db.InsertText(ctx, "doc1", "Hello world", nil)
results, _ := db.SearchText(ctx, "greeting", 10)
```

### Vector Operations

```go
// Single insert
err := db.Vector().Upsert(ctx, &core.Embedding{
    ID:      "doc1",
    Vector:  []float32{0.1, 0.2, 0.3},
    Content: "document text",
    Metadata: map[string]string{"key": "value"},
})

// Batch insert
embeddings := []*core.Embedding{
    {ID: "a", Vector: []float32{0.1, 0.2}, Content: "doc A"},
    {ID: "b", Vector: []float32{0.3, 0.4}, Content: "doc B"},
}
db.Vector().UpsertBatch(ctx, embeddings)

// Search
results, _ := db.Vector().Search(ctx, queryVector, core.SearchOptions{TopK: 10})

// Delete
db.Vector().Delete(ctx, "doc1")
```

### Text Operations (v2.13.0+)

```go
// Text with pre-computed vector (no embedder needed)
db.InsertTextWithVector(ctx, "id1", "content", []float32{0.1, 0.2}, nil)

// Batch with pre-computed vectors
texts := map[string]string{"id1": "text1", "id2": "text2"}
vectors := [][]float32{{0.1, 0.2}, {0.3, 0.4}}
db.InsertTextBatchWithVectors(ctx, texts, vectors, nil)

// Quick interface
id, _ := q.AddWithVector(ctx, []float32{0.1, 0.2}, "content", nil)
ids, _ := q.AddBatchWithVectors(ctx, vectors, contents, nil)
```

### LLM-Assisted Retrieval (v2.13.0+)

```go
// Generate keywords and alternate queries via LLM, then use in search
opts := cortexdb.TextSearchOptions{
    TopK:            10,
    Keywords:        []string{"expanded", "terms"},        // LLM-generated
    AlternateQueries: []string{"alternative phrasing"},   // LLM-generated
}
results, _ := db.SearchTextOnly(ctx, "original query", opts)
```

### Hybrid Search

```go
// Combines vector + keyword search
results, _ := db.HybridSearchText(ctx, "query", 10)

// With embedder = vector + FTS5 fusion
// Without embedder = pure FTS5/BM25
```

### Collections

```go
// Create collection
db.Vector().CreateCollection(ctx, "documents", nil)

// List collections
collections, _ := db.Vector().ListCollections(ctx)

// Search in collection
results, _ := db.Vector().Search(ctx, vec, core.SearchOptions{
    Collection: "documents",
    TopK:       10,
})
```

### Knowledge API

```go
// Store knowledge
db.SaveKnowledge(ctx, knowledge_api.KnowledgeSaveRequest{
    Content: "Paris is the capital of France",
    Namespace: "geography",
    Metadata: map[string]string{"category": "facts"},
})

// Retrieve
response, _ := db.RecallKnowledge(ctx, knowledge_api.KnowledgeSearchRequest{
    Query:     "capital cities",
    Namespace: "geography",
})
```

### Memory API

```go
// Save memory
db.SaveMemory(ctx, memory_api.MemorySaveRequest{
    Content:   "User prefers dark mode",
    SessionID: "user-123",
    Role:      "user",
})

// Search memories
results, _ := db.SearchMemory(ctx, memory_api.MemorySearchRequest{
    Query:     "theme preferences",
    SessionID: "user-123",
})
```

### GraphRAG Tools (MCP Server)

```go
// Ingest document for GraphRAG
tools := cortexdb.NewGraphRAGToolbox(db)
tools.IngestDocument(ctx, graphrag_tools.IngestDocumentRequest{
    Content:   "Article content...",
    Filename:  "article.txt",
    ChunkSize: 512,
})

// Search with graph expansion
results, _ := tools.SearchDocuments(ctx, graphrag_tools.SearchRequest{
    Query:    "topic",
    TopK:     10,
    UseGraph: true,
})
```

## Error Handling

```go
import "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"

_, err := db.InsertText(ctx, "id", "text", nil)
if err != nil {
    if errors.Is(err, cortexdb.ErrEmbedderNotConfigured) {
        // No embedder configured - use vector methods or InsertTextWithVector
    }
    if errors.Is(err, core.ErrNotFound) {
        // Document not found
    }
}
```

## Configuration Options

```go
cortexdb.Config{
    Path:         "db.sqlite",      // Database path
    Dimensions:   0,                 // 0 = auto-detect
    IndexType:    core.IndexTypeHNSW, // HNSW, IVF, Flat
    SimilarityFn: core.CosineSimilarity, // Cosine, DotProduct, Euclidean
    AutoDimAdapt: core.SmartAdapt,  // Smart, Truncate, Pad, Strict
    HNSW:         core.DefaultHNSWConfig(),
    IVF:          core.DefaultIVFConfig(),
}
```

## When to Use What

| Task | API |
|------|-----|
| Simple vector CRUD | `Quick.Add()`, `Quick.Search()` |
| With text + embedder | `InsertText()`, `SearchText()` |
| Text-only FTS5 | `SearchTextOnly()` |
| Vector + keyword hybrid | `HybridSearchText()` |
| Pre-computed vectors | `InsertTextWithVector()`, `AddWithVector()` |
| Knowledge base | `SaveKnowledge()`, `RecallKnowledge()` |
| Chat memory | `SaveMemory()`, `SearchMemory()` |
| GraphRAG | `GraphRAGToolbox` |
| MCP server | `cortexdb.NewMCPServer()` |

## Dependencies

- Go 1.24+
- SQLite (via modernc.org/sqlite)
- No external database required - fully embedded
