---
name: cortexdb
description: Use CortexDB for vector search, hybrid search, knowledge graphs, and RAG. Use when working with embeddings, similarity search, semantic search, AI knowledge bases, or when the user mentions vectors, embeddings, RAG, knowledge graph, or CortexDB.
---

# CortexDB

CortexDB is a lightweight SQLite-based vector database for Go AI projects. No external database required — fully embedded via SQLite.

## When to Use What

| Task | API |
|------|-----|
| Simple vector CRUD | `Quick.Add()`, `Quick.Search()` |
| With text + embedder | `InsertText()`, `SearchText()` |
| Text-only FTS5 (no embedder) | `SearchTextOnly()` |
| Vector + keyword hybrid | `HybridSearchText()` |
| Pre-computed vectors | `InsertTextWithVector()`, `AddWithVector()` |
| Knowledge base | `SaveKnowledge()`, `RecallKnowledge()` |
| Chat memory | `SaveMemory()`, `SearchMemory()` |
| GraphRAG / MCP | `GraphRAGToolbox`, `NewMCPServer()` |

## End-to-End Workflow

```go
import (
    "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
    "github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// Step 1: Open database
db, err := cortexdb.Open(cortexdb.Config{
    Path:         "db.sqlite",
    Dimensions:   384,
    IndexType:    core.IndexTypeHNSW,
    SimilarityFn: core.CosineSimilarity,
})
if err != nil {
    log.Fatal("failed to open db:", err)
}
defer db.Close()

// Step 2: (Optional) Attach embedder for text operations
embedder := ollama.NewEmbedder("http://localhost:11434/api/embed", "nomic-embed-text")
db, err = cortexdb.Open(
    cortexdb.DefaultConfig("db.sqlite"),
    cortexdb.WithEmbedder(embedder),
)
if err != nil {
    log.Fatal("failed to open db with embedder:", err)
}

// Step 3: Insert a document — validate embedder is configured
id, err := db.InsertText(ctx, "doc1", "Hello world", nil)
if err != nil {
    if errors.Is(err, cortexdb.ErrEmbedderNotConfigured) {
        // No embedder: switch to InsertTextWithVector or Quick.Add
        log.Fatal("embedder not configured — use vector-based insert instead")
    }
    log.Fatal("insert failed:", err)
}
log.Printf("inserted doc id=%s", id)

// Step 4: Verify document count before searching
count, err := db.Vector().Count(ctx)
if err != nil || count == 0 {
    log.Fatal("no documents indexed — check insert step before searching")
}

// Step 5: Search and validate results
results, err := db.SearchText(ctx, "greeting", 10)
if err != nil {
    log.Fatal("search failed:", err)
}
if len(results) == 0 {
    log.Println("no results — try broadening query or checking embedder config")
}
for _, r := range results {
    fmt.Printf("id=%s score=%.4f content=%s\n", r.ID, r.Score, r.Content)
}
```

## Quick Interface

```go
q := db.Quick()

id, err := q.Add(ctx, []float32{0.1, 0.2, 0.3}, "document content")
results, err := q.Search(ctx, []float32{0.1, 0.2, 0.3}, 10)

// Text search — falls back to FTS5 if no embedder
results, err = q.SearchText(ctx, "query text", 10)
results, err = q.SearchTextOnly(ctx, "query", 10) // FTS5 always

// Pre-computed vectors (v2.13.0+)
id, err = q.AddWithVector(ctx, []float32{0.1, 0.2}, "content", nil)
ids, err := q.AddBatchWithVectors(ctx, vectors, contents, nil)
```

## Configuration

```go
cortexdb.Config{
    Path:         "db.sqlite",
    Dimensions:   0,                      // 0 = auto-detect
    IndexType:    core.IndexTypeHNSW,     // HNSW, IVF, Flat
    SimilarityFn: core.CosineSimilarity,  // Cosine, DotProduct, Euclidean
    AutoDimAdapt: core.SmartAdapt,        // Smart, Truncate, Pad, Strict
    HNSW:         core.DefaultHNSWConfig(),
    IVF:          core.DefaultIVFConfig(),
}
```

## Error Handling

```go
_, err := db.InsertText(ctx, "id", "text", nil)
if err != nil {
    switch {
    case errors.Is(err, cortexdb.ErrEmbedderNotConfigured):
        // Use InsertTextWithVector or Quick.Add instead
    case errors.Is(err, core.ErrNotFound):
        // Document does not exist
    default:
        log.Fatal(err)
    }
}
```

## Detailed API Reference

See [docs/](docs/) for full API details on:
- Vector Operations (`Upsert`, `UpsertBatch`, `Search`, `Delete`, Collections)
- Text Operations with pre-computed vectors (`InsertTextWithVector`, `InsertTextBatchWithVectors`)
- Hybrid Search and LLM-Assisted Retrieval (`HybridSearchText`, `TextSearchOptions`)
- Knowledge API (`SaveKnowledge`, `RecallKnowledge`)
- Memory API (`SaveMemory`, `SearchMemory`)
- GraphRAG Tools and MCP Server (`GraphRAGToolbox`, `NewMCPServer`)

## Requirements

- Go 1.24+
- `modernc.org/sqlite` (no external database needed)
