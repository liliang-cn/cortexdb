package sqvect

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/liliang-cn/sqvect/pkg/core"
	"github.com/liliang-cn/sqvect/pkg/graph"
)

func TestDefaultConfig(t *testing.T) {
	path := "test.db"
	config := DefaultConfig(path)
	
	if config.Path != path {
		t.Errorf("Expected path %s, got %s", path, config.Path)
	}
	
	if config.Dimensions != 0 {
		t.Errorf("Expected dimensions 0 (auto-detect), got %d", config.Dimensions)
	}
	
	if config.SimilarityFn == nil {
		t.Error("Expected non-nil similarity function")
	}
}

func TestOpen(t *testing.T) {
	dbPath := fmt.Sprintf("test_sqvect_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	config := DefaultConfig(dbPath)
	db, err := Open(config)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	if db.store == nil {
		t.Error("Expected non-nil store")
	}

	if db.graph == nil {
		t.Error("Expected non-nil graph")
	}
}

func TestOpenInvalidPath(t *testing.T) {
	// Test with invalid path
	config := Config{
		Path:       "/invalid/path/that/does/not/exist/test.db",
		Dimensions: 128,
	}
	
	_, err := Open(config)
	if err == nil {
		t.Error("Expected error when opening with invalid path")
	}
}

func TestDBInterfaces(t *testing.T) {
	dbPath := fmt.Sprintf("test_interfaces_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	config := DefaultConfig(dbPath)
	db, err := Open(config)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	t.Run("VectorInterface", func(t *testing.T) {
		vectorStore := db.Vector()
		if vectorStore == nil {
			t.Error("Expected non-nil vector store")
		}
	})

	t.Run("GraphInterface", func(t *testing.T) {
		graphStore := db.Graph()
		if graphStore == nil {
			t.Error("Expected non-nil graph store")
		}
	})

	t.Run("QuickInterface", func(t *testing.T) {
		quick := db.Quick()
		if quick == nil {
			t.Error("Expected non-nil quick interface")
		}
		if quick.db != db {
			t.Error("Quick interface should reference the same DB")
		}
	})
}

func TestQuickAdd(t *testing.T) {
	dbPath := fmt.Sprintf("test_quick_add_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	config := Config{
		Path:       dbPath,
		Dimensions: 3,
	}
	db, err := Open(config)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	quick := db.Quick()
	ctx := context.Background()

	t.Run("AddVector", func(t *testing.T) {
		vector := []float32{1.0, 2.0, 3.0}
		content := "Test content"
		
		id, err := quick.Add(ctx, vector, content)
		if err != nil {
			t.Errorf("Failed to add vector: %v", err)
		}
		
		if id == "" {
			t.Error("Expected non-empty ID")
		}
	})

	t.Run("AddToCollection", func(t *testing.T) {
		vector := []float32{4.0, 5.0, 6.0}
		content := "Collection content"
		collection := "test_collection"
		
		// Create collection first
		_, err := db.store.CreateCollection(ctx, collection, 3)
		if err != nil {
			t.Fatalf("Failed to create collection: %v", err)
		}
		
		id, err := quick.AddToCollection(ctx, collection, vector, content)
		if err != nil {
			t.Errorf("Failed to add vector to collection: %v", err)
		}
		
		if id == "" {
			t.Error("Expected non-empty ID")
		}
	})

	t.Run("SmartPadding", func(t *testing.T) {
		// Test smart padding feature - system automatically adjusts dimensions
		vector := []float32{1.0, 2.0} // Will be padded to 3 dimensions
		content := "Padded vector"
		
		_, err := quick.Add(ctx, vector, content)
		if err != nil {
			t.Errorf("Smart padding should handle dimension mismatch, got error: %v", err)
		}
		
		// Test with empty vector - will be padded with zeros
		emptyVector := []float32{}
		_, err = quick.Add(ctx, emptyVector, "Empty vector padded with zeros")
		if err != nil {
			t.Errorf("Smart padding should handle empty vector, got error: %v", err)
		}
	})
}

func TestQuickSearch(t *testing.T) {
	dbPath := fmt.Sprintf("test_quick_search_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	config := Config{
		Path:       dbPath,
		Dimensions: 3,
	}
	db, err := Open(config)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	quick := db.Quick()
	ctx := context.Background()

	// Add test data
	vectors := [][]float32{
		{1.0, 0.0, 0.0},
		{0.9, 0.1, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}
	
	for i, vector := range vectors {
		_, err := quick.Add(ctx, vector, fmt.Sprintf("Content %d", i))
		if err != nil {
			t.Fatalf("Failed to add test vector %d: %v", i, err)
		}
	}

	t.Run("BasicSearch", func(t *testing.T) {
		query := []float32{1.0, 0.0, 0.0}
		results, err := quick.Search(ctx, query, 2)
		if err != nil {
			t.Errorf("Failed to search: %v", err)
		}
		
		if len(results) == 0 {
			t.Error("Expected search results")
		}
		
		if len(results) > 2 {
			t.Errorf("Expected at most 2 results, got %d", len(results))
		}
		
		// Results should be ordered by similarity (highest first)
		if len(results) >= 2 {
			if results[0].Score < results[1].Score {
				t.Error("Results should be ordered by similarity (highest first)")
			}
		}
	})

	t.Run("SearchInCollection", func(t *testing.T) {
		// Create collection and add data
		collection := "search_collection"
		_, err := db.store.CreateCollection(ctx, collection, 3)
		if err != nil {
			t.Fatalf("Failed to create collection: %v", err)
		}
		
		collectionVector := []float32{2.0, 2.0, 2.0}
		_, err = quick.AddToCollection(ctx, collection, collectionVector, "Collection content")
		if err != nil {
			t.Fatalf("Failed to add to collection: %v", err)
		}
		
		query := []float32{2.0, 2.0, 2.0}
		results, err := quick.SearchInCollection(ctx, collection, query, 5)
		if err != nil {
			t.Errorf("Failed to search in collection: %v", err)
		}
		
		if len(results) != 1 {
			t.Errorf("Expected 1 result in collection, got %d", len(results))
		}
		
		if results[0].Collection != collection {
			t.Errorf("Expected result from collection %s, got %s", collection, results[0].Collection)
		}
	})

	t.Run("SearchEmptyResults", func(t *testing.T) {
		// Search in non-existent collection should return empty results
		query := []float32{1.0, 1.0, 1.0}
		results, err := quick.SearchInCollection(ctx, "nonexistent", query, 5)
		if err != nil {
			t.Errorf("Search in non-existent collection should not error: %v", err)
		}
		
		if len(results) != 0 {
			t.Errorf("Expected 0 results from non-existent collection, got %d", len(results))
		}
	})
}

func TestGenerateID(t *testing.T) {
	// Reset counter for consistent testing
	idCounter = 0
	
	id1 := generateID()
	id2 := generateID()
	
	if id1 == id2 {
		t.Error("Generated IDs should be unique")
	}
	
	if id1 != "emb_1" {
		t.Errorf("Expected first ID to be 'emb_1', got %s", id1)
	}
	
	if id2 != "emb_2" {
		t.Errorf("Expected second ID to be 'emb_2', got %s", id2)
	}
}

func TestGenerateCounter(t *testing.T) {
	// Reset counter
	idCounter = 0
	
	count1 := generateCounter()
	count2 := generateCounter()
	count3 := generateCounter()
	
	if count1 != 1 {
		t.Errorf("Expected first count to be 1, got %d", count1)
	}
	
	if count2 != 2 {
		t.Errorf("Expected second count to be 2, got %d", count2)
	}
	
	if count3 != 3 {
		t.Errorf("Expected third count to be 3, got %d", count3)
	}
}

func TestDBClose(t *testing.T) {
	dbPath := fmt.Sprintf("test_close_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	config := DefaultConfig(dbPath)
	db, err := Open(config)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	err = db.Close()
	if err != nil {
		t.Errorf("Failed to close database: %v", err)
	}

	// Try to use after close - this should fail
	quick := db.Quick()
	ctx := context.Background()
	_, err = quick.Add(ctx, []float32{1, 2, 3}, "test")
	if err == nil {
		t.Error("Expected error when using database after close")
	}
}

func TestConfigVariations(t *testing.T) {
	dbPath := fmt.Sprintf("test_config_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	t.Run("CustomDimensions", func(t *testing.T) {
		config := Config{
			Path:         dbPath,
			Dimensions:   128,
			SimilarityFn: core.CosineSimilarity,
		}
		
		db, err := Open(config)
		if err != nil {
			t.Fatalf("Failed to open database with custom dimensions: %v", err)
		}
		defer db.Close()
	})

	t.Run("DotProductSimilarity", func(t *testing.T) {
		config := Config{
			Path:         dbPath + "_dot",
			Dimensions:   64,
			SimilarityFn: core.DotProduct,
		}
		
		db, err := Open(config)
		if err != nil {
			t.Fatalf("Failed to open database with dot product similarity: %v", err)
		}
		defer db.Close()
		defer os.Remove(dbPath + "_dot")
	})

	t.Run("EuclideanDistance", func(t *testing.T) {
		config := Config{
			Path:         dbPath + "_euclidean",
			Dimensions:   32,
			SimilarityFn: core.EuclideanDist,
		}
		
		db, err := Open(config)
		if err != nil {
			t.Fatalf("Failed to open database with Euclidean distance: %v", err)
		}
		defer db.Close()
		defer os.Remove(dbPath + "_euclidean")
	})
}

func TestIntegrationWorkflow(t *testing.T) {
	dbPath := fmt.Sprintf("test_integration_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	// Open database
	config := DefaultConfig(dbPath)
	db, err := Open(config)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	quick := db.Quick()
	ctx := context.Background()

	// Step 1: Add documents
	documents := []struct {
		vector  []float32
		content string
	}{
		{[]float32{1.0, 0.0, 0.0}, "Machine learning basics"},
		{[]float32{0.9, 0.1, 0.0}, "Deep learning introduction"},
		{[]float32{0.0, 1.0, 0.0}, "Database systems"},
		{[]float32{0.0, 0.9, 0.1}, "SQL fundamentals"},
		{[]float32{0.0, 0.0, 1.0}, "Vector databases"},
	}

	var docIDs []string
	for _, doc := range documents {
		id, err := quick.Add(ctx, doc.vector, doc.content)
		if err != nil {
			t.Fatalf("Failed to add document: %v", err)
		}
		docIDs = append(docIDs, id)
	}

	// Step 2: Search for similar documents
	query := []float32{0.95, 0.05, 0.0} // Similar to ML documents
	results, err := quick.Search(ctx, query, 3)
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 search results, got %d", len(results))
	}

	// First result should be most similar (ML or Deep Learning)
	firstResult := results[0]
	if firstResult.Content != "Machine learning basics" && firstResult.Content != "Deep learning introduction" {
		t.Errorf("Expected first result to be ML-related, got: %s", firstResult.Content)
	}

	// Step 3: Test graph functionality
	graphStore := db.Graph()
	
	// Initialize graph schema
	if err := graphStore.InitGraphSchema(ctx); err != nil {
		t.Fatalf("Failed to init graph schema: %v", err)
	}

	// Add nodes to graph
	for i, doc := range documents {
		node := &graph.GraphNode{
			ID:       docIDs[i],
			Vector:   doc.vector,
			Content:  doc.content,
			NodeType: "document",
		}
		
		if err := graphStore.UpsertNode(ctx, node); err != nil {
			t.Fatalf("Failed to add graph node: %v", err)
		}
	}

	// Test graph retrieval
	node, err := graphStore.GetNode(ctx, docIDs[0])
	if err != nil {
		t.Fatalf("Failed to get graph node: %v", err)
	}
	
	if node.Content != documents[0].content {
		t.Errorf("Expected node content %s, got %s", documents[0].content, node.Content)
	}
}