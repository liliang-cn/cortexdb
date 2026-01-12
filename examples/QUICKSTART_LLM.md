# Quick Start: Using sqvect with LLMs

## 1. Basic Setup (5 minutes)

```go
package main

import (
    "context"
    "fmt"
    "github.com/liliang-cn/sqvect/pkg/sqvect"
    // Your favorite LLM client
)

func main() {
    // Open database
    config := sqvect.DefaultConfig("vectors.db")
    db, _ := sqvect.Open(config)
    defer db.Close()
    
    ctx := context.Background()
    quick := db.Quick()
    
    // Store embedding
    // In production, generate this with your LLM
    embedding := []float32{0.1, 0.2, 0.3} 
    
    quick.Add(ctx, embedding, "Your content here")
    
    // Search
    queryEmbedding := []float32{0.1, 0.2, 0.3}
    results, _ := quick.Search(ctx, queryEmbedding, 5)
    
    for _, r := range results {
        fmt.Println(r.Content)
    }
}
```

## 2. RAG in 50 Lines

```go
func SimpleRAG(ctx context.Context, db *sqvect.DB, question string) string {
    quick := db.Quick()

    // 1. Generate query embedding (mock implementation)
    queryEmb := generateEmbedding(question)
    
    // 2. Search for relevant docs
    results, _ := quick.Search(ctx, queryEmb, 5)
    
    // 3. Build context
    contextStr := ""
    for _, r := range results {
        contextStr += r.Content + "\n"
    }
    
    // 4. Generate answer with LLM (mock implementation)
    prompt := fmt.Sprintf(`
        Context: %s
        Question: %s
        Answer:`, contextStr, question)
    
    return callLLM(prompt)
}
```

## 3. Key Features for LLM Apps

### Indexing Strategies (HNSW vs IVF)
```go
config := sqvect.DefaultConfig("vectors.db")

// HNSW (Default): Good for real-time, incremental updates
config.IndexType = core.IndexTypeHNSW 

// IVF: Good for bulk loading and training
// config.IndexType = core.IndexTypeIVF

db, _ := sqvect.Open(config)
```

### Advanced Filtering
```go
// Use the core store interface for advanced options
store := db.Vector()

opts := core.SearchOptions{
    TopK: 5,
    Filter: map[string]string{
        "category": "tech",
        "author": "alice",
    },
}
results, _ := store.Search(ctx, query, opts)
```

### Knowledge Graph RAG
```go
// Combine vector search with graph relationships
graphStore := db.Graph()

// Add nodes and edges
graphStore.UpsertNode(ctx, &graph.GraphNode{ID: "doc1", Vector: vec1})
graphStore.UpsertEdge(ctx, &graph.GraphEdge{FromNodeID: "doc1", ToNodeID: "doc2"})

// Hybrid Search
results, _ := graphStore.HybridSearch(ctx, &graph.HybridQuery{
    Vector: queryVec,
    StartNodeID: "doc1",
    GraphWeight: 0.3,
})
```

## 4. Common Patterns

### Chat with Memory
```go
// Store conversation
// Using AddToCollection to keep memory separate
quick.AddToCollection(ctx, "chat_memory", embedding, "User message here")

// Retrieve relevant context
results, _ := quick.SearchInCollection(ctx, "chat_memory", queryEmb, 5)
```

### Document Q&A
```go
// Index documents
store := db.Vector()
for i, chunk := range splitDocument(doc) {
    emb := &core.Embedding{
        ID:      fmt.Sprintf("chunk_%d", i),
        Vector:  getEmbedding(chunk.Text),
        Content: chunk.Text,
        Metadata: map[string]string{
            "source": doc.Name,
            "page":   fmt.Sprintf("%d", chunk.Page),
        },
    }
    store.Upsert(ctx, emb)
}

// Answer with sources
results, _ := store.Search(ctx, questionEmb, core.SearchOptions{TopK: 5})
```

## 5. Production Tips

### Embedding Caching
```go
cache := make(map[string][]float32)
func getEmbedding(text string) []float32 {
    if emb, ok := cache[text]; ok {
        return emb
    }
    emb := generateEmbedding(text) // Call OpenAI/etc
    cache[text] = emb
    return emb
}
```

### Batch Processing
```go
// Process multiple inserts efficiently using UpsertBatch
batch := make([]*core.Embedding, 100)
for i := 0; i < 100; i++ {
    batch[i] = &core.Embedding{...}
}
// Single transaction for 100 items
db.Vector().UpsertBatch(ctx, batch)
```

## 6. Example: Complete RAG System

See [examples/llm_integration/rag_example.go](llm_integration/rag_example.go) for a full working example with:
- Document chunking and indexing
- LSH for fast search
- Streaming results
- Advanced filtering
- Mock LLM integration

## 7. Integration Examples

### OpenAI
```go
client := openai.NewClient(apiKey)
resp, _ := client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
    Input: []string{text},
    Model: openai.AdaEmbeddingV2,
})
embedding := resp.Data[0].Embedding
```

### Local Models (Ollama)
```go
// Generate embeddings locally
// pseudocode for ollama integration
resp := ollama.Embed("nomic-embed-text", text)
embedding := resp.Embedding
```

## Why sqvect for LLM Apps?

- **Pure Go**: No CGO, deploys anywhere
- **Embedded**: No separate service to manage
- **Fast**: HNSW & IVF indexes supported
- **Feature-rich**: GraphRAG, aggregations, advanced filtering
- **Production-ready**: Thread-safe, tested, benchmarked

Start building your LLM application with sqvect today!