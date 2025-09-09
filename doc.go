// Package sqvect provides a lightweight, embeddable vector store using SQLite.
//
// sqvect is a 100% pure Go library designed for AI applications that need fast,
// reliable vector storage without external dependencies. Built on SQLite using
// modernc.org/sqlite (pure Go implementation - NO CGO REQUIRED!), it's perfect
// for RAG (Retrieval-Augmented Generation) systems, semantic search, knowledge
// graphs, and any Go AI project that needs embedding storage.
//
// # Features
//
//   - 100% Pure Go - No CGO dependencies, easy cross-compilation
//   - SQLite-based storage with single .db file
//   - Multiple similarity functions (cosine, dot product, Euclidean distance)
//   - Collections support for multi-tenant namespacing
//   - Knowledge graphs with advanced graph operations
//   - Batch operations for efficient data loading
//   - Thread-safe operations with concurrent read/write support
//   - Rich metadata support with JSON storage
//   - Automatic dimension adaptation for any embedding model
//   - HNSW indexing for high-performance search
//
// # Quick Start
//
// Create a new vector store and perform basic operations:
//
//	package main
//
//	import (
//	    "context"
//	    "log"
//	    "github.com/liliang-cn/sqvect/pkg/sqvect"
//	)
//
//	func main() {
//	    // Initialize database
//	    config := sqvect.Config{
//	        Path:       "embeddings.db",
//	        Dimensions: 768, // or 0 for auto-detect
//	    }
//	    
//	    db, err := sqvect.Open(config)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    defer db.Close()
//	    
//	    ctx := context.Background()
//	    quick := db.Quick()
//	    
//	    // Add an embedding
//	    vector := []float32{0.1, 0.2, 0.3, ...} // 768 dimensions
//	    id, err := quick.Add(ctx, vector, "Sample text content")
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    
//	    // Search for similar vectors
//	    query := []float32{0.1, 0.25, 0.28, ...} // 768 dimensions
//	    results, err := quick.Search(ctx, query, 5)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    
//	    for _, result := range results {
//	        log.Printf("ID: %s, Score: %.3f, Content: %s\n",
//	            result.ID, result.Score, result.Content)
//	    }
//	}
//
// # Advanced Usage
//
// Using collections and vector store directly:
//
//	import (
//	    "github.com/liliang-cn/sqvect/pkg/core"
//	    "github.com/liliang-cn/sqvect/pkg/sqvect"
//	)
//
//	// Create collections for different data types
//	vectorStore := db.Vector()
//	
//	_, err := vectorStore.CreateCollection(ctx, "products", 256)
//	_, err = vectorStore.CreateCollection(ctx, "users", 128)
//	
//	// Add to specific collection
//	emb := &core.Embedding{
//	    ID:         "product_123",
//	    Collection: "products",
//	    Vector:     productVector,
//	    Content:    "Product description",
//	    Metadata: map[string]string{
//	        "category": "electronics",
//	        "price":    "99.99",
//	    },
//	}
//	err = vectorStore.Upsert(ctx, emb)
//	
//	// Search within collection
//	results, err := vectorStore.Search(ctx, queryVector, core.SearchOptions{
//	    Collection: "products",
//	    TopK:       10,
//	    Threshold:  0.7,
//	})
//
// # Graph Operations
//
// Using the graph store for knowledge graphs:
//
//	import "github.com/liliang-cn/sqvect/pkg/graph"
//
//	graphStore := db.Graph()
//	err := graphStore.InitGraphSchema(ctx)
//	
//	// Create nodes
//	node := &graph.GraphNode{
//	    ID:       "doc_1",
//	    Vector:   docVector,
//	    Content:  "Document content",
//	    NodeType: "document",
//	}
//	err = graphStore.UpsertNode(ctx, node)
//	
//	// Create relationships
//	edge := &graph.GraphEdge{
//	    ID:         "edge_1",
//	    FromNodeID: "doc_1",
//	    ToNodeID:   "doc_2",
//	    EdgeType:   "references",
//	    Weight:     0.8,
//	}
//	err = graphStore.UpsertEdge(ctx, edge)
//	
//	// Hybrid search (vector + graph)
//	results, err := graphStore.HybridSearch(ctx, &graph.HybridQuery{
//	    Vector:      queryVector,
//	    StartNodeID: "doc_1",
//	    TopK:        5,
//	    Weights: graph.HybridWeights{
//	        VectorWeight: 0.5,
//	        GraphWeight:  0.3,
//	        EdgeWeight:   0.2,
//	    },
//	})
//
// # Similarity Functions
//
// sqvect provides three built-in similarity functions:
//
//   - CosineSimilarity: Best for text embeddings (default)
//   - DotProduct: Fast computation for normalized vectors
//   - EuclideanDist: Good for spatial data and image embeddings
//
// Configure via:
//
//	config := sqvect.Config{
//	    Path:         "data.db",
//	    Dimensions:   384,
//	    SimilarityFn: core.CosineSimilarity, // or core.DotProduct, core.EuclideanDist
//	}
//
// # Performance
//
// sqvect is optimized for common vector operations:
//
//   - Cosine similarity: ~1.2M operations/second
//   - Vector encoding/decoding: ~38K operations/second
//   - Single upsert: ~20K operations/second
//   - Batch search (1K vectors): ~60 operations/second
//   - Pure Go implementation enables easy deployment and cross-compilation
//
// # Thread Safety
//
// All operations are thread-safe. Multiple goroutines can safely read and write
// to the same store instance concurrently.
//
// # Error Handling
//
// sqvect uses wrapped errors with operation context. Check for specific errors:
//
//	err := vectorStore.Delete(ctx, "non-existent")
//	if errors.Is(err, core.ErrNotFound) {
//	    // Handle not found case
//	}
//
// Common errors include:
//   - ErrInvalidDimension: Vector dimension mismatch
//   - ErrInvalidVector: Invalid vector data (nil, empty, NaN, Inf)
//   - ErrNotFound: Embedding not found
//   - ErrStoreClosed: Operation on closed store
//
// # Why Pure Go?
//
// sqvect uses modernc.org/sqlite, a pure Go SQLite implementation, which means:
//   - No CGO required - simplifies builds and deployments
//   - Cross-compilation to any platform Go supports
//   - Single binary distribution
//   - Better compatibility with serverless and container environments
//   - Easier debugging and profiling
//
// # Examples
//
// See the examples/ directory for comprehensive examples:
//   - semantic_search: Full-text semantic search
//   - document_clustering: K-means clustering
//   - hybrid_search: Combined vector + graph search
//   - multi_collection: Multi-tenant data management
//   - image_search: Multi-modal CLIP-like search
//   - knowledge_graph: Graph-based knowledge management
//   - rag_system: Retrieval-augmented generation
//   - benchmark: Performance testing
package sqvect