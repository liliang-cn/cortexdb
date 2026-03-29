# CortexDB Examples

This directory contains comprehensive examples demonstrating the full capabilities of the CortexDB SQLite vector database library.

## 🚀 Quick Start

All examples are self-contained and runnable. To run any example:

```bash
# Run directly
go run examples/simple_usage/main.go

# Or build and run
go build -o simple_example examples/simple_usage/main.go
./simple_example
```

## 📚 Examples Overview

### 1. **simple_usage** - Getting Started
- Basic vector operations (add, search, delete)
- Collections management
- Similarity search fundamentals
- **Perfect for:** First-time users

### 2. **semantic_search** - Text Search with Embeddings
- Document indexing with metadata
- Semantic similarity search
- Collection-based filtering
- Similarity threshold filtering
- Performance metrics
- **Perfect for:** Search applications, document retrieval

### 3. **document_clustering** - K-means Clustering
- K-means clustering implementation
- Intra-cluster similarity analysis
- Outlier detection
- Cross-cluster analysis
- Cluster models for classification
- **Perfect for:** Data analysis, content organization

### 4. **hybrid_search** - Vector + Graph Search
- Knowledge graph construction
- Citation network analysis with PageRank
- Research path finding
- Combined vector and graph search
- Performance comparison
- **Perfect for:** Research papers, knowledge bases

### 5. **multi_collection** - Multi-tenant Data Management
- Multiple collections with different dimensions
- E-commerce products, users, reviews
- Cross-collection search
- Personalized recommendations
- Collaborative filtering
- **Perfect for:** E-commerce, multi-tenant applications

### 6. **image_search** - Multi-modal Search
- CLIP-like image embeddings
- Text-to-image search
- Image-to-image similarity
- Tag-based filtering
- Duplicate detection
- **Perfect for:** Image galleries, visual search

### 7. **knowledge_graph** - Graph-based Knowledge Management
- Entity nodes with embeddings
- Relationship edges
- Graph traversal algorithms
- Community detection
- PageRank for importance
- **Perfect for:** Knowledge graphs, recommendation systems

### 8. **rag_system** - Retrieval-Augmented Generation
- Document graph construction
- Hybrid retrieval strategies
- Context-aware search
- Graph-enhanced retrieval
- **Perfect for:** RAG applications, Q&A systems

### 9. **benchmark** - Performance Testing
- Insert performance testing
- Search performance benchmarking
- Batch operations
- Large-scale testing
- **Perfect for:** Performance optimization, capacity planning

### 10. **chat_memory** - AI Chat Memory
- Session management
- Message storage
- Context retrieval
- Semantic memory search
- **Perfect for:** Chatbots, AI Agents

### 11. **benchmark_ivf** - Index Comparison
- Compare HNSW vs IVF performance
- Index training demo
- **Perfect for:** Performance tuning

### 12. **text_api** - High-Level Text & Knowledge APIs
- Automatic text embedding
- `SaveKnowledge` for GraphRAG ingestion
- `SaveMemory` for agentic persistence
- Hybrid and FTS5 search
- **Perfect for:** Modern LLM-based applications

### 13. **graphrag_embedder** - Embedder-Backed GraphRAG
- Active ontology schema setup
- Knowledge ingestion with typed entities and relations
- Deterministic inference with provenance
- Graph-expanded knowledge search
- **Perfect for:** Embedder-backed GraphRAG applications

### 14. **llm_tool_calling** - No-Embedder Tool Calling
- High-level `knowledge_*` and `memory_*` tool usage
- External-LLM-style retrieval planning
- Lexical GraphRAG without an embedding model
- In-process tool calling that matches the MCP surface
- **Perfect for:** MCP clients and external LLM orchestration

## 💻 API Usage Patterns

All examples use the latest CortexDB API:

```go
import (
    "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
    "github.com/liliang-cn/cortexdb/v2/pkg/core"
    "github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Initialize database
config := cortexdb.DefaultConfig("mydb.db")
config.Dimensions = 384 // or 0 for auto-detect

db, err := cortexdb.Open(config)
defer db.Close()

// 1. High-Level Knowledge & Memory APIs
// Ingest a full document for GraphRAG
db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
    KnowledgeID: "doc_1",
    Title:       "CortexDB Overview",
    Content:     "CortexDB is an embedded cognitive memory...",
})

// Store an agent memory
db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
    MemoryID:  "mem_1",
    UserID:    "user_123",
    Content:   "Alice likes Go programming.",
})

// 2. Quick API for simple vector operations
quick := db.Quick()
id, err := quick.Add(ctx, vector, content)
results, err := quick.Search(ctx, queryVector, topK)

// 3. Vector store for advanced operations
vectorStore := db.Vector()
// Creating collections
vectorStore.CreateCollection(ctx, "products", 256)

// Advanced search with SQL-like filtering
results, err := vectorStore.Search(ctx, query, core.SearchOptions{
    Collection: "products",
    TopK:       10,
    Threshold:  0.7,
})

// 4. Graph store for graph operations
graphStore := db.Graph()
err := graphStore.UpsertNode(ctx, &graph.GraphNode{...})
err := graphStore.UpsertEdge(ctx, &graph.GraphEdge{...})
```

## 🎯 Key Features Demonstrated

- **Knowledge Ingestion**: Automated chunking and GraphRAG artifact creation
- **Ontology Validation**: Active schema enforcement for entity and relation writes
- **Inference**: Deterministic inferred edges with provenance
- **Agent Memory**: Scoped memory persistence (User/Session/Global)
- **Vector Operations**: Add, search, update, delete embeddings
- **Collections**: Multi-tenant support with isolated namespaces
- **Similarity Metrics**: Cosine, dot product, Euclidean distance
- **Graph Operations**: Nodes, edges, traversal, PageRank
- **Hybrid Search**: Combine vector similarity with graph relationships
- **Tool Calling**: MCP-aligned `knowledge_*`, `memory_*`, and GraphRAG tools
- **Performance**: Benchmarking and optimization techniques

## 📊 Performance Tips

1. **Batch Operations**: Use batch inserts for better performance
2. **Collection Isolation**: Use collections to separate different data types
3. **Dimension Consistency**: Keep vector dimensions consistent within collections
4. **Index Optimization**: HNSW/IVF indexes are automatically managed
5. **Connection Pooling**: Reuse database connections

## 🔧 Requirements

- Go 1.24.0 or higher
- Pure Go implementation (no CGO required!)
- SQLite driver included (pure Go)
- No external dependencies

## 📝 Notes

- All examples create temporary databases that are cleaned up after execution
- Examples use simulated embeddings for demonstration (in production, use real embedding models)
- Each example is self-contained and can be run independently
- Database files are created in the current directory and removed after execution

## 🤝 Contributing

Feel free to add more examples! Make sure to:
1. Use the latest CortexDB API
2. Include comprehensive comments
3. Provide realistic use cases
4. Clean up resources after execution
5. Test your example thoroughly

## 📖 Further Reading

- [CortexDB Documentation](../README.md)
- [API Reference](../doc.go)
- [Feature Comparison](../FEATURE_COMPARISON.md)
