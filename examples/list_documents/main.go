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
	store, err := sqvect.New("list_documents_example.db", 3)
	if err != nil {
		log.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Clean up database file when done
	defer func() {
		if err := os.Remove("list_documents_example.db"); err != nil {
			log.Printf("Failed to remove database file: %v", err)
		}
	}()

	ctx := context.Background()

	// Initialize the store
	if err := store.Init(ctx); err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}

	// Insert some sample embeddings with different document IDs
	embeddings := []*sqvect.Embedding{
		{
			ID:      "emb1",
			Vector:  []float32{1.0, 0.0, 0.0},
			Content: "This is content from document 1",
			DocID:   "document_1",
		},
		{
			ID:      "emb2",
			Vector:  []float32{0.0, 1.0, 0.0},
			Content: "More content from document 1",
			DocID:   "document_1",
		},
		{
			ID:      "emb3",
			Vector:  []float32{0.0, 0.0, 1.0},
			Content: "This is content from document 2",
			DocID:   "document_2",
		},
		{
			ID:      "emb4",
			Vector:  []float32{0.5, 0.5, 0.0},
			Content: "Content from document 3",
			DocID:   "document_3",
		},
		{
			ID:      "emb5",
			Vector:  []float32{0.2, 0.3, 0.5},
			Content: "Another piece from document 2",
			DocID:   "document_2",
		},
	}

	// Batch insert the embeddings
	if err := store.UpsertBatch(ctx, embeddings); err != nil {
		log.Fatalf("Failed to insert embeddings: %v", err)
	}

	fmt.Println("=== Inserted sample embeddings ===")
	for _, emb := range embeddings {
		fmt.Printf("ID: %s, DocID: %s, Content: %s\n", emb.ID, emb.DocID, emb.Content)
	}

	// List all documents
	fmt.Println("\n=== Listing all documents ===")
	docIDs, err := store.ListDocuments(ctx)
	if err != nil {
		log.Fatalf("Failed to list documents: %v", err)
	}

	fmt.Printf("Found %d unique documents:\n", len(docIDs))
	for i, docID := range docIDs {
		fmt.Printf("%d. %s\n", i+1, docID)
	}

	// Get statistics about the store
	fmt.Println("\n=== Store Statistics ===")
	stats, err := store.Stats(ctx)
	if err != nil {
		log.Fatalf("Failed to get stats: %v", err)
	}

	fmt.Printf("Total embeddings: %d\n", stats.Count)
	fmt.Printf("Vector dimensions: %d\n", stats.Dimensions)
	fmt.Printf("Database size: %d bytes\n", stats.Size)

	// Demonstrate deleting a document and listing again
	fmt.Println("\n=== Deleting document_2 ===")
	if err := store.DeleteByDocID(ctx, "document_2"); err != nil {
		log.Fatalf("Failed to delete document_2: %v", err)
	}

	docIDs, err = store.ListDocuments(ctx)
	if err != nil {
		log.Fatalf("Failed to list documents after deletion: %v", err)
	}

	fmt.Printf("Documents remaining after deletion (%d):\n", len(docIDs))
	for i, docID := range docIDs {
		fmt.Printf("%d. %s\n", i+1, docID)
	}

	// Final statistics
	stats, err = store.Stats(ctx)
	if err != nil {
		log.Fatalf("Failed to get final stats: %v", err)
	}
	fmt.Printf("\nFinal embedding count: %d\n", stats.Count)
}
