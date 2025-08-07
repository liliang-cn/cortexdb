package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/sqvect"
)

func main() {
	// Create a new vector store
	store, err := sqvect.New("advanced_operations_example.db", 3)
	if err != nil {
		log.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Clean up database file when done
	defer func() {
		if err := os.Remove("advanced_operations_example.db"); err != nil {
			log.Printf("Failed to remove database file: %v", err)
		}
	}()

	ctx := context.Background()

	// Initialize the store
	if err := store.Init(ctx); err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}

	// Insert sample embeddings with different document types
	embeddings := []*sqvect.Embedding{
		{
			ID:      "article1_chunk1",
			Vector:  []float32{1.0, 0.0, 0.0},
			Content: "Introduction to machine learning",
			DocID:   "article_1",
			Metadata: map[string]string{
				"type":    "article",
				"section": "introduction",
			},
		},
		{
			ID:      "article1_chunk2",
			Vector:  []float32{0.8, 0.2, 0.0},
			Content: "Machine learning algorithms overview",
			DocID:   "article_1",
			Metadata: map[string]string{
				"type":    "article",
				"section": "content",
			},
		},
		{
			ID:      "book1_chapter1",
			Vector:  []float32{0.0, 1.0, 0.0},
			Content: "Chapter 1: Getting started with AI",
			DocID:   "book_1",
			Metadata: map[string]string{
				"type":    "book",
				"chapter": "1",
			},
		},
		{
			ID:      "book1_chapter2",
			Vector:  []float32{0.0, 0.8, 0.2},
			Content: "Chapter 2: Deep learning fundamentals",
			DocID:   "book_1",
			Metadata: map[string]string{
				"type":    "book",
				"chapter": "2",
			},
		},
		{
			ID:      "article2_chunk1",
			Vector:  []float32{0.0, 0.0, 1.0},
			Content: "Natural language processing basics",
			DocID:   "article_2",
			Metadata: map[string]string{
				"type":    "article",
				"section": "introduction",
			},
		},
	}

	// Batch insert the embeddings
	if err := store.UpsertBatch(ctx, embeddings); err != nil {
		log.Fatalf("Failed to insert embeddings: %v", err)
	}

	fmt.Println("=== Advanced Vector Store Operations Demo ===")
	fmt.Println()

	// 1. Get all embeddings for a specific document
	fmt.Println("1. Getting all embeddings for 'article_1':")
	articleEmbs, err := store.GetByDocID(ctx, "article_1")
	if err != nil {
		log.Fatalf("Failed to get embeddings by doc ID: %v", err)
	}

	for _, emb := range articleEmbs {
		fmt.Printf("   - ID: %s, Content: %s\n", emb.ID, emb.Content)
		fmt.Printf("     Metadata: %v\n", emb.Metadata)
	}

	// 2. Get documents by type
	fmt.Println("\n2. Getting all articles:")
	articles, err := store.GetDocumentsByType(ctx, "article")
	if err != nil {
		log.Fatalf("Failed to get documents by type: %v", err)
	}

	for _, emb := range articles {
		fmt.Printf("   - %s (%s): %s\n", emb.ID, emb.DocID, emb.Content)
	}

	fmt.Println("\n3. Getting all books:")
	books, err := store.GetDocumentsByType(ctx, "book")
	if err != nil {
		log.Fatalf("Failed to get documents by type: %v", err)
	}

	for _, emb := range books {
		fmt.Printf("   - %s (%s): %s\n", emb.ID, emb.DocID, emb.Content)
	}

	// 3. List documents with detailed information
	fmt.Println("\n4. Document statistics:")
	docInfos, err := store.ListDocumentsWithInfo(ctx)
	if err != nil {
		log.Fatalf("Failed to list documents with info: %v", err)
	}

	for _, info := range docInfos {
		fmt.Printf("   - Document: %s\n", info.DocID)
		fmt.Printf("     Embeddings: %d\n", info.EmbeddingCount)
		if info.FirstCreated != nil {
			fmt.Printf("     First created: %s\n", *info.FirstCreated)
		}
		if info.LastUpdated != nil {
			fmt.Printf("     Last updated: %s\n", *info.LastUpdated)
		}
		fmt.Println()
	}

	// 4. Current store statistics
	stats, err := store.Stats(ctx)
	if err != nil {
		log.Fatalf("Failed to get stats: %v", err)
	}
	fmt.Printf("Current store stats: %d embeddings, %d dimensions\n\n", stats.Count, stats.Dimensions)

	// 5. Clear specific documents
	fmt.Println("5. Clearing specific documents (article_1 and book_1):")
	if err := store.ClearByDocID(ctx, []string{"article_1", "book_1"}); err != nil {
		log.Fatalf("Failed to clear by doc IDs: %v", err)
	}

	// Check remaining documents
	remainingDocs, err := store.ListDocuments(ctx)
	if err != nil {
		log.Fatalf("Failed to list remaining documents: %v", err)
	}
	fmt.Printf("Remaining documents: %v\n", remainingDocs)

	// Final stats
	finalStats, err := store.Stats(ctx)
	if err != nil {
		log.Fatalf("Failed to get final stats: %v", err)
	}
	fmt.Printf("Final stats: %d embeddings remaining\n\n", finalStats.Count)

	// 6. Clear all remaining data
	fmt.Println("6. Clearing all remaining data:")
	if err := store.Clear(ctx); err != nil {
		log.Fatalf("Failed to clear store: %v", err)
	}

	finalStats, err = store.Stats(ctx)
	if err != nil {
		log.Fatalf("Failed to get final stats: %v", err)
	}
	fmt.Printf("Store cleared. Final count: %d embeddings\n", finalStats.Count)
}
