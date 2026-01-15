// Package sqvect provides a lightweight, embeddable vector database for Go AI projects.
//
// sqvect is a 100% pure Go library designed to be the storage kernel for RAG (Retrieval-Augmented Generation)
// systems. Built on SQLite using modernc.org/sqlite (NO CGO REQUIRED!), it provides vector storage,
// full-text search (FTS5), knowledge graphs, and chat memory management in a single database file.
//
// # Key Features
//
//   - 🚀 RAG-Ready - Built-in support for Documents, Chat Sessions, and Messages.
//   - 🔍 Hybrid Search - Combine Vector Search (HNSW/IVF) with Keyword Search (FTS5) using RRF fusion.
//   - 🧠 Memory Efficient - Built-in SQ8 quantization reduces RAM usage by 75%.
//   - 🛡️ Secure - Row-Level Security via ACL fields and SQL-level push-down filtering.
//   - 🔧 100% Pure Go - Easy cross-compilation and zero-dependency deployment.
//   - 🕸️ GraphRAG - Advanced graph operations for complex relationship-based retrieval.
//
// # Quick Start
//
//	import (
//	    "context"
//	    "github.com/liliang-cn/sqvect/v2/pkg/sqvect"
//	)
//
//	func main() {
//	    // 1. Open database with default configuration
//	    config := sqvect.DefaultConfig("vectors.db")
//	    db, _ := sqvect.Open(config)
//	    defer db.Close()
//
//	    // 2. Use the Quick interface for simple operations
//	    ctx := context.Background()
//	    quick := db.Quick()
//	    
//	    // Add vector
//	    quick.Add(ctx, []float32{0.1, 0.2, 0.3}, "Go is awesome")
//
//	    // Search
//	    results, _ := quick.Search(ctx, []float32{0.1, 0.2, 0.28}, 5)
//	}
//
// # RAG and Hybrid Search
//
// sqvect provides high-level APIs for building RAG applications:
//
//	// Perform hybrid search (Vector + Full-Text Search)
//	results, err := db.Vector().HybridSearch(ctx, queryVec, "search term", core.HybridSearchOptions{
//	    TopK: 5,
//	})
//
// # Chat Memory
//
// Built-in conversation history management:
//
//	// Add a message to a session
//	db.Vector().AddMessage(ctx, &core.Message{
//	    SessionID: "session_123",
//	    Role:      "user",
//	    Content:   "How do I use sqvect?",
//	})
//
// # Advanced Configuration
//
// Configure indexing and quantization for production:
//
//	config := sqvect.DefaultConfig("data.db")
//	config.IndexType = core.IndexTypeHNSW // or core.IndexTypeIVF
//	config.Quantization.Enabled = true    // Enable SQ8 quantization
//
//	db, err := sqvect.Open(config)
//
// # Oservability
//
// sqvect v2.0.0+ supports structured logging:
//
//	config.Logger = myCustomLogger
//	db, _ := sqvect.Open(config)
//
// For more detailed examples, see the examples/ directory.
package sqvect
