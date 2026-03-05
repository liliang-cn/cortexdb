package main

import (
	"context"
	"fmt"
	"log"
	"math"

	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

// DummyEmbedder implements sqvect.Embedder for demonstration purposes.
// In a real application, you would use an embedder that calls OpenAI, Ollama, HuggingFace, etc.
type DummyEmbedder struct {
	dim int
}

func (d *DummyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, d.dim)
	// Create a deterministic mock vector based on string length and characters
	for i := 0; i < d.dim; i++ {
		if len(text) > 0 {
			vec[i] = float32(text[i%len(text)]) / 255.0
		} else {
			vec[i] = 0.0
		}
	}
	
	// Normalize
	var sum float32
	for _, v := range vec {
		sum += v * v
	}
	if sum > 0 {
		norm := float32(math.Sqrt(float64(sum)))
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec, nil
}

func (d *DummyEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		vec, _ := d.Embed(ctx, text)
		results[i] = vec
	}
	return results, nil
}

func (d *DummyEmbedder) Dim() int {
	return d.dim
}

func main() {
	fmt.Println("--- sqvect High-Level Text APIs Example ---")

	// 1. Initialize the database with an Embedder
	embedder := &DummyEmbedder{dim: 384}
	
	// Create the DB instance and pass the embedder
	db, err := sqvect.Open(
		sqvect.DefaultConfig("text_api_demo.db"), 
		sqvect.WithEmbedder(embedder),
	)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 2. Check Database Information
	info := db.Info()
	fmt.Printf("Database Info:\n- Path: %s\n- Dimensions: %d\n- Index: %s\n- Embedder Configured: %t\n\n", 
		info.Path, info.Dimensions, info.IndexType, info.Embedder != "")

	// 3. Inserting Text Directly (No manual vectors needed!)
	fmt.Println("Adding documents via InsertTextBatch...")
	docs := map[string]string{
		"doc1": "Go is an open source programming language supported by Google.",
		"doc2": "Python is a high-level, general-purpose programming language.",
		"doc3": "Rust is blazing fast and memory-efficient.",
		"doc4": "Go makes it easy to build simple, reliable, and efficient software.",
	}
	
	err = db.InsertTextBatch(ctx, docs, map[string]string{"source": "demo"})
	if err != nil {
		log.Fatalf("Insert failed: %v", err)
	}
	
	// Insert single text
	err = db.InsertText(ctx, "doc5", "SQLite is a C-language library that implements a small, fast, SQL database engine.", nil)
	if err != nil {
		log.Fatalf("Single insert failed: %v", err)
	}

	// You can also use the Quick interface
	quick := db.Quick()
	doc6ID, _ := quick.AddText(ctx, "Vector databases are essential for RAG applications.", map[string]string{"tag": "AI"})
	fmt.Printf("Quickly added text with auto-ID: %s\n\n", doc6ID)

	// 4. Searching Text
	query := "Which language is backed by Google?"
	
	fmt.Printf("--- Standard Vector SearchText ---\nQuery: '%s'\n", query)
	// Uses the embedder to convert query to vector and performs semantic search
	results, err := db.SearchText(ctx, query, 2)
	if err != nil {
		log.Fatal(err)
	}
	for i, res := range results {
		fmt.Printf("%d. ID: %s, Score: %.4f, Content: %s\n", i+1, res.ID, res.Score, res.Content)
	}
	fmt.Println()

	// 5. Hybrid Search (Vector Semantic + FTS5 Keyword)
	keywordQuery := "fast database engine"
	fmt.Printf("--- Hybrid SearchText (Vector + Keyword) ---\nQuery: '%s'\n", keywordQuery)
	// This combines semantic search with exact keyword matches using Reciprocal Rank Fusion (RRF)
	hybridResults, err := db.HybridSearchText(ctx, keywordQuery, 2)
	if err != nil {
		log.Fatal(err)
	}
	for i, res := range hybridResults {
		fmt.Printf("%d. ID: %s, Score: %.4f, Content: %s\n", i+1, res.ID, res.Score, res.Content)
	}
	fmt.Println()

	// 6. Text-Only Search (FTS5 Keyword Only)
	ftsQuery := "programming language"
	fmt.Printf("--- FTS5 Keyword SearchTextOnly ---\nQuery: '%s'\n", ftsQuery)
	// This bypasses the embedder and vectors entirely, running a pure SQLite FTS5 MATCH query.
	// It's incredibly fast and useful for exact matching or when you don't have an embedding model.
	textResults, err := db.SearchTextOnly(ctx, ftsQuery, sqvect.TextSearchOptions{TopK: 3})
	if err != nil {
		log.Fatal(err)
	}
	for i, res := range textResults {
		fmt.Printf("%d. ID: %s, Score: %.4f, Content: %s\n", i+1, res.ID, res.Score, res.Content)
	}
	
	// Clean up for this demo
	_ = db.Vector().DeleteBatch(ctx, []string{"doc1", "doc2", "doc3", "doc4", "doc5", doc6ID})
}
