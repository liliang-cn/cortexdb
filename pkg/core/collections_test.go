package core

import (
	"context"
	"os"
	"testing"
)

func TestCollections(t *testing.T) {
	// Create temporary database
	dbPath := "test_collections.db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	// Create store
	store, err := New(dbPath, 0)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}

	t.Run("CreateCollection", func(t *testing.T) {
		collection, err := store.CreateCollection(ctx, "test_collection", 384)
		if err != nil {
			t.Fatalf("Failed to create collection: %v", err)
		}

		if collection.Name != "test_collection" {
			t.Errorf("Expected collection name 'test_collection', got %s", collection.Name)
		}

		if collection.Dimensions != 384 {
			t.Errorf("Expected dimensions 384, got %d", collection.Dimensions)
		}
	})

	t.Run("CreateDuplicateCollection", func(t *testing.T) {
		_, err := store.CreateCollection(ctx, "test_collection", 384)
		if err == nil {
			t.Error("Expected error when creating duplicate collection")
		}
	})

	t.Run("GetCollection", func(t *testing.T) {
		collection, err := store.GetCollection(ctx, "test_collection")
		if err != nil {
			t.Fatalf("Failed to get collection: %v", err)
		}

		if collection.Name != "test_collection" {
			t.Errorf("Expected collection name 'test_collection', got %s", collection.Name)
		}
	})

	t.Run("GetNonExistentCollection", func(t *testing.T) {
		_, err := store.GetCollection(ctx, "nonexistent")
		if err == nil {
			t.Error("Expected error when getting nonexistent collection")
		}
	})

	t.Run("ListCollections", func(t *testing.T) {
		collections, err := store.ListCollections(ctx)
		if err != nil {
			t.Fatalf("Failed to list collections: %v", err)
		}

		if len(collections) < 2 { // default + test_collection
			t.Errorf("Expected at least 2 collections, got %d", len(collections))
		}

		found := false
		for _, col := range collections {
			if col.Name == "test_collection" {
				found = true
				break
			}
		}
		if !found {
			t.Error("test_collection not found in collections list")
		}
	})

	t.Run("GetCollectionStats", func(t *testing.T) {
		// Add an embedding to the collection first
		emb := &Embedding{
			ID:         "stats_test",
			Collection: "test_collection",
			Vector:     make([]float32, 384),
			Content:    "Test content",
		}
		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to add embedding: %v", err)
		}

		stats, err := store.GetCollectionStats(ctx, "test_collection")
		if err != nil {
			t.Fatalf("Failed to get collection stats: %v", err)
		}

		if stats.Name != "test_collection" {
			t.Errorf("Expected stats name 'test_collection', got %s", stats.Name)
		}

		if stats.Count == 0 {
			t.Error("Expected non-zero embedding count")
		}
	})

	t.Run("DeleteCollection", func(t *testing.T) {
		// Create a collection to delete
		_, err := store.CreateCollection(ctx, "to_delete", 256)
		if err != nil {
			t.Fatalf("Failed to create collection to delete: %v", err)
		}

		// Delete it
		if err := store.DeleteCollection(ctx, "to_delete"); err != nil {
			t.Fatalf("Failed to delete collection: %v", err)
		}

		// Verify it's gone
		_, err = store.GetCollection(ctx, "to_delete")
		if err == nil {
			t.Error("Expected error when getting deleted collection")
		}
	})

	t.Run("DeleteNonExistentCollection", func(t *testing.T) {
		err := store.DeleteCollection(ctx, "nonexistent")
		if err == nil {
			t.Error("Expected error when deleting nonexistent collection")
		}
	})
}

func TestCollectionEmbeddings(t *testing.T) {
	// Create temporary database
	dbPath := "test_collection_embeddings.db"
	defer func() {
		if err := os.Remove(dbPath); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	// Create store
	store, err := New(dbPath, 0)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			// Ignore cleanup errors in tests
			_ = err
		}
	}()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}

	// Create test collections
	_, err = store.CreateCollection(ctx, "docs", 384)
	if err != nil {
		t.Fatalf("Failed to create docs collection: %v", err)
	}

	_, err = store.CreateCollection(ctx, "images", 512)
	if err != nil {
		t.Fatalf("Failed to create images collection: %v", err)
	}

	t.Run("AddEmbeddingToCollection", func(t *testing.T) {
		emb := &Embedding{
			ID:         "doc1",
			Collection: "docs",
			Vector:     make([]float32, 384),
			Content:    "Document content",
		}

		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to add embedding to collection: %v", err)
		}
	})

	t.Run("SearchInCollection", func(t *testing.T) {
		// Add more embeddings
		embeddings := []*Embedding{
			{
				ID:         "doc2",
				Collection: "docs",
				Vector:     make([]float32, 384),
				Content:    "Another document",
			},
			{
				ID:         "img1",
				Collection: "images",
				Vector:     make([]float32, 512),
				Content:    "Image description",
			},
		}

		for _, emb := range embeddings {
			if err := store.Upsert(ctx, emb); err != nil {
				t.Fatalf("Failed to add embedding: %v", err)
			}
		}

		// Search in docs collection
		query := make([]float32, 384)
		results, err := store.Search(ctx, query, SearchOptions{
			Collection: "docs",
			TopK:       10,
		})
		if err != nil {
			t.Fatalf("Failed to search in collection: %v", err)
		}

		// Should only return docs collection results
		for _, result := range results {
			if result.Collection != "docs" {
				t.Errorf("Expected result from docs collection, got %s", result.Collection)
			}
		}

		if len(results) != 2 { // doc1, doc2
			t.Errorf("Expected 2 results from docs collection, got %d", len(results))
		}
	})

	t.Run("SearchInNonExistentCollection", func(t *testing.T) {
		query := make([]float32, 384)
		results, err := store.Search(ctx, query, SearchOptions{
			Collection: "nonexistent",
			TopK:       10,
		})
		if err != nil {
			t.Fatalf("Search in nonexistent collection should not error: %v", err)
		}

		if len(results) != 0 {
			t.Errorf("Expected 0 results from nonexistent collection, got %d", len(results))
		}
	})
}