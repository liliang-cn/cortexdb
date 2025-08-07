// Package sqvect provides a lightweight, embeddable vector store using SQLite.
//
// sqvect is designed for applications that need to store and search high-dimensional
// vectors (embeddings) with associated metadata. It's perfect for local RAG
// (Retrieval-Augmented Generation) applications, semantic search, and similarity
// matching without the complexity of external vector databases.
//
// # Features
//
//   - SQLite-based storage with single .db file
//   - Multiple similarity functions (cosine, dot product, Euclidean distance)
//   - Batch operations for efficient data loading
//   - Thread-safe operations with concurrent read/write support
//   - Rich metadata support with JSON storage
//   - Pure Go implementation with minimal dependencies
//
// # Quick Start
//
// Create a new vector store and perform basic operations:
//
//	store, err := sqvect.New("embeddings.db", 768)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer store.Close()
//
//	ctx := context.Background()
//	if err := store.Init(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Insert an embedding
//	embedding := sqvect.Embedding{
//	    ID:      "doc_1",
//	    Vector:  []float32{0.1, 0.2, 0.3, ...}, // 768 dimensions
//	    Content: "Sample text content",
//	    Metadata: map[string]string{"type": "document"},
//	}
//
//	if err := store.Upsert(ctx, embedding); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Search for similar vectors
//	query := []float32{0.1, 0.25, 0.28, ...} // 768 dimensions
//	results, err := store.Search(ctx, query, sqvect.SearchOptions{
//	    TopK: 5,
//	    Threshold: 0.7,
//	})
//
// # Configuration
//
// Advanced configuration with custom similarity function:
//
//	config := sqvect.Config{
//	    Path:         "data.db",
//	    VectorDim:    384,
//	    MaxConns:     20,
//	    BatchSize:    1000,
//	    SimilarityFn: sqvect.CosineSimilarity,
//	}
//
//	store, err := sqvect.NewWithConfig(config)
//
// # Similarity Functions
//
// sqvect provides three built-in similarity functions:
//
//   - CosineSimilarity: Best for text embeddings (default)
//   - DotProduct: Fast computation for normalized vectors
//   - EuclideanDist: Good for spatial data and image embeddings
//
// # Performance
//
// sqvect is optimized for common vector operations:
//
//   - Cosine similarity: ~1.2M operations/second
//   - Vector encoding/decoding: ~38K operations/second
//   - Single upsert: ~20K operations/second
//   - Batch search (1K vectors): ~60 operations/second
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
//	err := store.Delete(ctx, "non-existent")
//	if errors.Is(err, sqvect.ErrNotFound) {
//	    // Handle not found case
//	}
//
// Common errors include:
//   - ErrInvalidDimension: Vector dimension mismatch
//   - ErrInvalidVector: Invalid vector data (nil, empty, NaN, Inf)
//   - ErrNotFound: Embedding not found
//   - ErrStoreClosed: Operation on closed store
package sqvect