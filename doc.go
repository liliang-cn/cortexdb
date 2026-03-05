// Package cortexdb provides a lightweight, embeddable vector database for Go AI projects.
//
// cortexdb is a 100% pure Go library designed to be the storage kernel for RAG (Retrieval-Augmented Generation)
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
//   - 🧭 Semantic Router - Intent routing with configurable similarity and thresholds.
//   - 🧠 Hindsight - Biomimetic agent memory with TEMPR retrieval.
//
// # Quick Start
//
//	import (
//	    "context"
//	    "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
//	)
//
//	func main() {
//	    // 1. Open database with default configuration
//	    config := cortexdb.DefaultConfig("vectors.db")
//	    db, _ := cortexdb.Open(config)
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
// cortexdb provides high-level APIs for building RAG applications:
//
//	import "github.com/liliang-cn/cortexdb/v2/pkg/core"
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
//	    Content:   "How do I use cortexdb?",
//	})
//
// # Advanced Configuration
//
// Configure indexing and quantization for production:
//
//	config := cortexdb.DefaultConfig("data.db")
//	config.IndexType = core.IndexTypeHNSW // or core.IndexTypeIVF
//	config.SimilarityFn = core.CosineSimilarity
//	config.Dimensions = 1536
//
//	db, err := cortexdb.Open(config)
//
// For deeper control (quantization, logger, text similarity), use core.Config with core.NewWithConfig.
//
// # Observability
//
// cortexdb v2.0.0+ supports structured logging via core.Config.Logger when using core.NewWithConfig.
//
// # Semantic Router
//
// Route user intent before expensive LLM calls:
//
//	import (
//	    "context"
//	    "github.com/liliang-cn/cortexdb/v2/pkg/core"
//	    semanticrouter "github.com/liliang-cn/cortexdb/v2/pkg/semantic-router"
//	)
//
//	router, _ := semanticrouter.NewRouter(
//	    semanticrouter.NewMockEmbedder(1536),
//	    semanticrouter.WithThreshold(0.82),
//	    semanticrouter.WithSimilarityFunc(core.CosineSimilarity),
//	)
//
//	_ = router.Add(&semanticrouter.Route{
//	    Name: "refund",
//	    Utterances: []string{"我要退款", "申请退款"},
//	})
//
//	result, _ := router.Route(context.Background(), "我要退款")
//	_ = result
//
// # Hindsight Memory
//
// Long-term agent memory with TEMPR retrieval:
//
//	import "github.com/liliang-cn/cortexdb/v2/pkg/hindsight"
//
//	sys, _ := hindsight.New(&hindsight.Config{DBPath: "agent_memory.db"})
//	_ = sys
//
// For more detailed examples, see the examples/ directory.
package cortexdb
