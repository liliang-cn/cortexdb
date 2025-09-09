# Quick Start: Using sqvect with LLMs

## 1. Basic Setup (5 minutes)

```go
package main

import (
    "github.com/liliang-cn/sqvect/pkg/sqvect"
    // Your favorite LLM client
)

func main() {
    // Open database
    db, _ := sqvect.Open("vectors.db")
    defer db.Close()
    
    // Store embedding
    db.Add("doc1", embedding, map[string]string{
        "text": "Your content here",
    })
    
    // Search
    results := db.Search(queryEmbedding, 5)
}
```

## 2. RAG in 50 Lines

```go
func SimpleRAG(question string) string {
    // 1. Generate query embedding
    queryEmb := generateEmbedding(question)
    
    // 2. Search for relevant docs
    results := db.Search(queryEmb, 5)
    
    // 3. Build context
    context := ""
    for _, r := range results {
        context += r.Metadata["text"] + "\n"
    }
    
    // 4. Generate answer with LLM
    prompt := fmt.Sprintf(`
        Context: %s
        Question: %s
        Answer:`, context, question)
    
    return callLLM(prompt)
}
```

## 3. Key Features for LLM Apps

### Fast Approximate Search (LSH)
```go
// 100x faster for large datasets
lsh := index.NewLSHIndex(config)
lsh.Insert(id, embedding)
results := lsh.Search(query, k)
```

### Streaming Results
```go
// Real-time UI updates
stream := db.StreamSearch(query, opts)
for result := range stream {
    updateUI(result)
}
```

### Advanced Filtering
```go
// Complex queries
filter := "category:tech AND date>2024"
results := db.SearchWithFilter(query, filter)
```

### Geo-Spatial Search
```go
// Location-aware AI
geo := geo.NewGeoIndex()
nearby := geo.SearchRadius(location, 10, geo.Kilometers)
```

## 4. Common Patterns

### Chat with Memory
```go
// Store conversation
db.Add(msgID, embedding, map[string]string{
    "role": "user",
    "content": message,
    "session": sessionID,
})

// Retrieve relevant context
relevantMemory := db.Search(queryEmb, 5)
```

### Document Q&A
```go
// Index documents
for _, chunk := range splitDocument(doc) {
    db.Add(chunkID, getEmbedding(chunk), map[string]string{
        "source": doc.Name,
        "page": chunk.Page,
        "text": chunk.Text,
    })
}

// Answer with sources
results := db.Search(questionEmb, 5)
answer := generateWithSources(question, results)
```

### Semantic Search
```go
// Multi-modal search
textResults := textDB.Search(textQuery, 10)
imageResults := imageDB.Search(imageQuery, 10)
combined := rankResults(textResults, imageResults)
```

## 5. Production Tips

### Embedding Caching
```go
cache := make(map[string][]float32)
func getEmbedding(text string) []float32 {
    if emb, ok := cache[text]; ok {
        return emb
    }
    emb := generateEmbedding(text)
    cache[text] = emb
    return emb
}
```

### Batch Processing
```go
// Process multiple queries efficiently
streams := db.ParallelStreamSearch(queries, opts)
for i, stream := range streams {
    go processStream(i, stream)
}
```

### Performance Monitoring
```go
// Track search performance
start := time.Now()
results := db.Search(query, k)
metrics.RecordLatency(time.Since(start))
```

## 6. Example: Complete RAG System

See [examples/llm_integration/rag_example.go](examples/llm_integration/rag_example.go) for a full working example with:
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

### Anthropic Claude
```go
client := anthropic.NewClient(apiKey)
// Use Claude for generation, OpenAI for embeddings
```

### Local Models (Ollama)
```go
// Generate embeddings locally
resp := ollama.Embed("nomic-embed-text", text)
embedding := resp.Embedding
```

## Next Steps

1. Check out the [full LLM integration guide](examples/llm_integration/README.md)
2. Explore [advanced features](FEATURE_DEVELOPMENT.md)
3. See [benchmarks and performance](examples/benchmark/)
4. Join the community and share your use cases!

## Why sqvect for LLM Apps?

- **Pure Go**: No CGO, deploys anywhere
- **Embedded**: No separate service to manage
- **Fast**: LSH index, streaming, parallel search
- **Feature-rich**: Geo-spatial, aggregations, advanced filtering
- **Production-ready**: Thread-safe, tested, benchmarked

Start building your LLM application with sqvect today!