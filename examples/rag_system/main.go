package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/liliang-cn/sqvect/pkg/core"
	"github.com/liliang-cn/sqvect/pkg/sqvect"
)

func main() {
	dbPath := "rag_demo.db"
	_ = os.Remove(dbPath) // Clean start

	// 1. Initialize RAG Database
	fmt.Println("Initializing RAG Database...")
	config := sqvect.DefaultConfig(dbPath)
	config.Dimensions = 4 // Small dim for demo
	// Enable HNSW for vectors
	config.IndexType = core.IndexTypeHNSW 
	
db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		db.Close()
		os.Remove(dbPath)
	}()

	ctx := context.Background()
	store := db.Vector()

	// 2. Ingest a Document (Hybrid: Vector + Keyword)
	fmt.Println("\nIngesting documents...")
	
docID := "tech_manual_v1"
	err = store.CreateDocument(ctx, &core.Document{
		ID:    docID,
		Title: "Sqvect Technical Manual",
		Author: "Engineering Team",
		Metadata: map[string]interface{}{"type": "manual"},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Add chunks (simulated)
	// Chunk 1: Mentions "Hybrid Search" explicitly
	chunk1Vec := []float32{0.9, 0.1, 0.0, 0.0} 
	store.Upsert(ctx, &core.Embedding{
		ID:      "chunk_1",
		DocID:   docID,
		Vector:  chunk1Vec,
		Content: "Hybrid search combines vector similarity with keyword matching using FTS5.",
	})

	// Chunk 2: Mentions "Performance" explicitly
	chunk2Vec := []float32{0.0, 0.9, 0.1, 0.0}
	store.Upsert(ctx, &core.Embedding{
		ID:      "chunk_2",
		DocID:   docID,
		Vector:  chunk2Vec,
		Content: "Performance is optimized using SQ8 quantization and HNSW indexing.",
	})

	// Chunk 3: Mentions "Installation"
	chunk3Vec := []float32{0.0, 0.0, 0.1, 0.9}
	store.Upsert(ctx, &core.Embedding{
		ID:      "chunk_3",
		DocID:   docID,
		Vector:  chunk3Vec,
		Content: "To install sqvect, simply run go get.",
	})

	// 3. Perform Hybrid Search
	// Scenario: User asks "How does search work?"
	// - Vector query might be close to chunk1 (semantic)
	// - Keyword "search" appears in chunk1
	
	fmt.Println("\nPerforming Hybrid Search for 'search'...")
	
	queryVec := []float32{0.8, 0.2, 0.0, 0.0} // Semantically close to chunk 1
	results, err := store.HybridSearch(ctx, queryVec, "search", core.HybridSearchOptions{
		SearchOptions: core.SearchOptions{TopK: 3},
		RRFK:          60,
	})
	if err != nil {
		log.Printf("Hybrid search warning (FTS might be missing): %v", err)
	} else {
		for i, res := range results {
			fmt.Printf("%d. [Score: %.4f] %s\n", i+1, res.Score, res.Content)
		}
	}

	// 4. Secure Search (ACL)
	fmt.Println("\nDemonstrating Row-Level Security (ACL)...")
	
	// Add a secret chunk
	store.Upsert(ctx, &core.Embedding{
		ID:      "secret_chunk",
		DocID:   docID,
		Vector:  chunk1Vec,
		Content: "SECRET: The launch code is 1234.",
		ACL:     []string{"role:admin"},
	})

	// Search as "role:user" (Should NOT see secret)
	fmt.Println("Searching as 'role:user':")
	resultsUser, _ := store.SearchWithACL(ctx, chunk1Vec, []string{"role:user"}, core.SearchOptions{TopK: 5})
	for _, res := range resultsUser {
		fmt.Printf("- %s\n", res.Content)
	}

	// Search as "role:admin" (Should see secret)
	fmt.Println("Searching as 'role:admin':")
	resultsAdmin, _ := store.SearchWithACL(ctx, chunk1Vec, []string{"role:admin"}, core.SearchOptions{TopK: 5})
	for _, res := range resultsAdmin {
		fmt.Printf("- %s\n", res.Content)
	}
}

func randomVec(dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = rand.Float32()
	}
	return v
}
