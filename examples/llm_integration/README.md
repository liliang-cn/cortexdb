# Using sqvect with Large Language Models (LLMs)

sqvect is perfect for building LLM-powered applications like RAG systems, semantic search, and AI agents. This guide shows how to integrate sqvect with popular LLM providers.

## Table of Contents
1. [Quick Start](#quick-start)
2. [RAG (Retrieval Augmented Generation)](#rag-retrieval-augmented-generation)
3. [Semantic Search](#semantic-search)
4. [Chat with Memory](#chat-with-memory)
5. [Document Q&A](#document-qa)
6. [Multi-Modal Search](#multi-modal-search)

## Quick Start

### Installation
```bash
go get github.com/liliang-cn/sqvect
```

### Basic Setup with OpenAI
```go
package main

import (
    "context"
    "github.com/liliang-cn/sqvect/pkg/sqvect"
    "github.com/sashabaranov/go-openai"
)

func main() {
    // Initialize sqvect
    db, err := sqvect.Open("vectors.db", sqvect.Config{
        VectorDim: 1536, // OpenAI ada-002 dimension
    })
    if err != nil {
        panic(err)
    }
    defer db.Close()
    
    // Initialize OpenAI client
    client := openai.NewClient("your-api-key")
    
    // Generate embedding
    resp, err := client.CreateEmbeddings(context.Background(), 
        openai.EmbeddingRequest{
            Input: []string{"Hello, world!"},
            Model: openai.AdaEmbeddingV2,
        })
    
    // Store in sqvect
    embedding := resp.Data[0].Embedding
    db.Add("doc1", embedding, map[string]string{
        "text": "Hello, world!",
        "type": "greeting",
    })
}
```

## RAG (Retrieval Augmented Generation)

RAG combines vector search with LLM generation for accurate, context-aware responses.

### Complete RAG Example
```go
package main

import (
    "context"
    "fmt"
    "strings"
    
    "github.com/liliang-cn/sqvect/pkg/core"
    "github.com/sashabaranov/go-openai"
)

type RAGSystem struct {
    vectorDB *core.SQLiteStore
    llm      *openai.Client
}

func NewRAGSystem(dbPath string) (*RAGSystem, error) {
    // Initialize vector store
    config := core.DefaultConfig()
    config.Path = dbPath
    config.VectorDim = 1536 // OpenAI embedding dimension
    
    store, err := core.NewWithConfig(config)
    if err != nil {
        return nil, err
    }
    
    if err := store.Init(context.Background()); err != nil {
        return nil, err
    }
    
    return &RAGSystem{
        vectorDB: store,
        llm:      openai.NewClient("your-api-key"),
    }, nil
}

// IndexDocument splits and indexes a document
func (r *RAGSystem) IndexDocument(docID string, content string) error {
    // Split into chunks (simple version - use better chunking in production)
    chunks := splitIntoChunks(content, 500)
    
    for i, chunk := range chunks {
        // Generate embedding
        embedding, err := r.generateEmbedding(chunk)
        if err != nil {
            return err
        }
        
        // Store in vector DB
        err = r.vectorDB.Upsert(context.Background(), &core.Embedding{
            ID:     fmt.Sprintf("%s_chunk_%d", docID, i),
            Vector: embedding,
            Metadata: map[string]string{
                "doc_id": docID,
                "chunk":  chunk,
                "index":  fmt.Sprintf("%d", i),
            },
        })
        if err != nil {
            return err
        }
    }
    
    return nil
}

// Query performs RAG search and generation
func (r *RAGSystem) Query(question string) (string, error) {
    // Generate query embedding
    queryEmbedding, err := r.generateEmbedding(question)
    if err != nil {
        return "", err
    }
    
    // Search for relevant chunks
    results, err := r.vectorDB.Search(context.Background(), queryEmbedding, 
        core.SearchOptions{
            TopK: 5,
        })
    if err != nil {
        return "", err
    }
    
    // Build context from results
    var contextParts []string
    for _, result := range results {
        chunk := result.Metadata["chunk"]
        contextParts = append(contextParts, chunk)
    }
    context := strings.Join(contextParts, "\n\n")
    
    // Generate response with LLM
    response, err := r.llm.CreateChatCompletion(
        context.Background(),
        openai.ChatCompletionRequest{
            Model: openai.GPT4,
            Messages: []openai.ChatCompletionMessage{
                {
                    Role: openai.ChatMessageRoleSystem,
                    Content: "Answer questions based on the provided context. " +
                             "If the answer cannot be found in the context, say so.",
                },
                {
                    Role: openai.ChatMessageRoleUser,
                    Content: fmt.Sprintf("Context:\n%s\n\nQuestion: %s", 
                                       context, question),
                },
            },
        },
    )
    
    if err != nil {
        return "", err
    }
    
    return response.Choices[0].Message.Content, nil
}

func (r *RAGSystem) generateEmbedding(text string) ([]float32, error) {
    resp, err := r.llm.CreateEmbeddings(context.Background(),
        openai.EmbeddingRequest{
            Input: []string{text},
            Model: openai.AdaEmbeddingV2,
        })
    if err != nil {
        return nil, err
    }
    
    // Convert to float32
    embedding := make([]float32, len(resp.Data[0].Embedding))
    for i, v := range resp.Data[0].Embedding {
        embedding[i] = v
    }
    
    return embedding, nil
}

func splitIntoChunks(text string, chunkSize int) []string {
    words := strings.Fields(text)
    var chunks []string
    
    for i := 0; i < len(words); i += chunkSize {
        end := i + chunkSize
        if end > len(words) {
            end = len(words)
        }
        chunk := strings.Join(words[i:end], " ")
        chunks = append(chunks, chunk)
    }
    
    return chunks
}
```

## Semantic Search

Build powerful semantic search with sqvect's advanced features:

```go
// Semantic search with filtering and streaming
func SemanticSearchWithFilters(store *core.SQLiteStore, query string) {
    // Generate query embedding
    embedding := generateEmbedding(query)
    
    // Parse user filters
    filter, _ := core.ParseFilterString("category:tech AND date>2024")
    
    // Stream results for real-time UI updates
    stream, err := store.StreamSearch(context.Background(), embedding,
        core.StreamingOptions{
            SearchOptions: core.SearchOptions{
                TopK: 20,
            },
            BatchSize:        5,
            EarlyTerminate:   true,
            QualityThreshold: 0.8,
            ProgressCallback: func(processed, total int) {
                fmt.Printf("Searching: %d/%d\n", processed, total)
            },
        })
    
    // Process streaming results
    for result := range stream {
        fmt.Printf("Found: %s (score: %.2f)\n", 
                  result.Metadata["title"], result.Score)
        
        // Update UI in real-time
        updateSearchResults(result)
    }
}
```

## Chat with Memory

Build conversational AI with vector-based memory:

```go
type ChatMemory struct {
    store      *core.SQLiteStore
    llm        *openai.Client
    sessionID  string
    maxMemory  int
}

func (cm *ChatMemory) AddMessage(role, content string) error {
    // Generate embedding for the message
    embedding, _ := cm.generateEmbedding(content)
    
    // Store with metadata
    return cm.store.Upsert(context.Background(), &core.Embedding{
        ID:     fmt.Sprintf("%s_%d", cm.sessionID, time.Now().Unix()),
        Vector: embedding,
        Metadata: map[string]string{
            "session": cm.sessionID,
            "role":    role,
            "content": content,
            "time":    time.Now().Format(time.RFC3339),
        },
    })
}

func (cm *ChatMemory) GetRelevantMemory(query string, limit int) ([]string, error) {
    // Search for relevant past conversations
    embedding, _ := cm.generateEmbedding(query)
    
    results, err := cm.store.Search(context.Background(), embedding,
        core.SearchOptions{
            TopK: limit,
            Filter: map[string]string{
                "session": cm.sessionID,
            },
        })
    
    var memories []string
    for _, r := range results {
        memories = append(memories, 
            fmt.Sprintf("%s: %s", r.Metadata["role"], r.Metadata["content"]))
    }
    
    return memories, err
}

func (cm *ChatMemory) Chat(userInput string) (string, error) {
    // Add user message to memory
    cm.AddMessage("user", userInput)
    
    // Get relevant context from memory
    memories, _ := cm.GetRelevantMemory(userInput, 5)
    
    // Build conversation with context
    messages := []openai.ChatCompletionMessage{
        {
            Role:    openai.ChatMessageRoleSystem,
            Content: "You are a helpful assistant with memory of past conversations.",
        },
    }
    
    // Add relevant memories as context
    if len(memories) > 0 {
        messages = append(messages, openai.ChatCompletionMessage{
            Role:    openai.ChatMessageRoleSystem,
            Content: fmt.Sprintf("Relevant past conversation:\n%s", 
                               strings.Join(memories, "\n")),
        })
    }
    
    messages = append(messages, openai.ChatCompletionMessage{
        Role:    openai.ChatMessageRoleUser,
        Content: userInput,
    })
    
    // Generate response
    response, err := cm.llm.CreateChatCompletion(context.Background(),
        openai.ChatCompletionRequest{
            Model:    openai.GPT4,
            Messages: messages,
        })
    
    if err != nil {
        return "", err
    }
    
    assistantReply := response.Choices[0].Message.Content
    
    // Add assistant reply to memory
    cm.AddMessage("assistant", assistantReply)
    
    return assistantReply, nil
}
```

## Document Q&A

Advanced document Q&A with sqvect's features:

```go
type DocumentQA struct {
    store *core.SQLiteStore
    lsh   *index.LSHIndex  // For fast approximate search
}

func (qa *DocumentQA) IndexPDFDocument(pdfPath string) error {
    // Extract text from PDF
    text := extractPDFText(pdfPath)
    
    // Smart chunking with overlap
    chunks := smartChunking(text, 512, 50) // 512 tokens, 50 overlap
    
    for i, chunk := range chunks {
        embedding := generateEmbedding(chunk.text)
        
        // Store in both exact and approximate index
        qa.store.Upsert(context.Background(), &core.Embedding{
            ID:     fmt.Sprintf("pdf_%d", i),
            Vector: embedding,
            Metadata: map[string]string{
                "source": pdfPath,
                "page":   fmt.Sprintf("%d", chunk.page),
                "text":   chunk.text,
            },
        })
        
        // Add to LSH for fast search
        qa.lsh.Insert(fmt.Sprintf("pdf_%d", i), embedding)
    }
    
    return nil
}

func (qa *DocumentQA) AnswerWithSources(question string) (Answer, error) {
    queryEmb := generateEmbedding(question)
    
    // Fast approximate search with LSH
    candidates, _ := qa.lsh.SearchWithMultiProbe(queryEmb, 20, 3)
    
    // Refine with exact search
    var candidateIDs []string
    for _, c := range candidates {
        candidateIDs = append(candidateIDs, c.ID)
    }
    
    // Get full results with metadata
    results := qa.store.GetByIDs(context.Background(), candidateIDs)
    
    // Build answer with citations
    answer := generateAnswerWithCitations(question, results)
    
    return answer, nil
}
```

## Multi-Modal Search

Combine text and image embeddings:

```go
type MultiModalSearch struct {
    textStore  *core.SQLiteStore
    imageStore *core.SQLiteStore
    geoIndex   *geo.GeoIndex  // For location-based search
}

func (mm *MultiModalSearch) SearchMultiModal(query Query) ([]Result, error) {
    var allResults []Result
    
    // Text search if query has text
    if query.Text != "" {
        textEmb := generateTextEmbedding(query.Text)
        textResults, _ := mm.textStore.Search(context.Background(), 
            textEmb, core.SearchOptions{TopK: 10})
        allResults = append(allResults, convertResults(textResults, "text")...)
    }
    
    // Image search if query has image
    if query.Image != nil {
        imageEmb := generateImageEmbedding(query.Image)
        imageResults, _ := mm.imageStore.Search(context.Background(),
            imageEmb, core.SearchOptions{TopK: 10})
        allResults = append(allResults, convertResults(imageResults, "image")...)
    }
    
    // Location search if query has location
    if query.Location != nil {
        geoResults, _ := mm.geoIndex.SearchRadius(
            geo.Coordinate{
                Lat: query.Location.Lat,
                Lng: query.Location.Lng,
            }, 
            query.RadiusKM, 
            geo.Kilometers,
        )
        allResults = append(allResults, convertGeoResults(geoResults)...)
    }
    
    // Combine and rank results
    rankedResults := rankMultiModalResults(allResults, query.Weights)
    
    return rankedResults, nil
}
```

## Best Practices

### 1. Embedding Generation
- Cache embeddings to avoid re-generation
- Batch embedding requests for efficiency
- Use appropriate models (ada-002 for search, text-embedding-3 for quality)

### 2. Chunking Strategies
- Use overlapping chunks for better context
- Consider semantic chunking (by paragraphs/sections)
- Keep chunk size consistent with LLM context window

### 3. Performance Optimization
- Use LSH index for large datasets (>100K vectors)
- Enable streaming for real-time UX
- Pre-filter with metadata before vector search

### 4. Quality Improvements
- Implement hybrid search (keyword + vector)
- Use re-ranking with cross-encoders
- Add feedback loops to improve results

## Integration with Other LLM Providers

### Anthropic Claude
```go
// Similar pattern, just different client
claude := anthropic.NewClient("api-key")
response, _ := claude.CreateMessage(...)
```

### Google Gemini
```go
gemini := genai.NewClient(ctx, option.WithAPIKey("api-key"))
model := gemini.GenerativeModel("gemini-pro")
```

### Local Models (Ollama)
```go
// Use Ollama for local embeddings
resp, _ := http.Post("http://localhost:11434/api/embeddings", 
    "application/json", 
    bytes.NewBuffer([]byte(`{"model": "nomic-embed-text", "prompt": "text"}`)))
```

## Example Applications

1. **Customer Support Bot**: Index support docs, use RAG for accurate answers
2. **Code Assistant**: Index codebase, help with code questions
3. **Research Assistant**: Index papers, provide summaries and insights
4. **Personal Knowledge Base**: Index notes, emails, documents for personal AI
5. **E-commerce Search**: Semantic product search with filters

## Resources

- [sqvect Documentation](https://github.com/liliang-cn/sqvect)
- [OpenAI Embeddings Guide](https://platform.openai.com/docs/guides/embeddings)
- [RAG Best Practices](https://www.pinecone.io/learn/retrieval-augmented-generation/)
- [Vector Database Benchmarks](https://ann-benchmarks.com/)