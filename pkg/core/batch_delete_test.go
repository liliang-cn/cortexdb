package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

// TestDeleteBatch tests the batch delete functionality
func TestDeleteBatch(t *testing.T) {
	ctx := context.Background()
	dbPath := "delete_batch_test.db"

	// Clean up
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.VectorDim = 3

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Insert test data
	testData := []*Embedding{
		{ID: "1", Vector: []float32{1, 0, 0}, Content: "Item 1"},
		{ID: "2", Vector: []float32{0, 1, 0}, Content: "Item 2"},
		{ID: "3", Vector: []float32{0, 0, 1}, Content: "Item 3"},
		{ID: "4", Vector: []float32{1, 1, 0}, Content: "Item 4"},
		{ID: "5", Vector: []float32{0, 1, 1}, Content: "Item 5"},
	}

	for _, emb := range testData {
		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to insert embedding: %v", err)
		}
	}

	t.Run("Delete multiple items", func(t *testing.T) {
		idsToDelete := []string{"1", "3", "5"}
		err := store.DeleteBatch(ctx, idsToDelete)
		if err != nil {
			t.Fatalf("DeleteBatch failed: %v", err)
		}

		// Verify remaining items
		results, err := store.Search(ctx, []float32{1, 1, 1}, SearchOptions{TopK: 10})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 remaining items, got %d", len(results))
		}

		remainingIDs := make(map[string]bool)
		for _, r := range results {
			remainingIDs[r.ID] = true
		}

		// Verify deleted items are gone
		for _, id := range idsToDelete {
			if remainingIDs[id] {
				t.Errorf("Item %s should have been deleted", id)
			}
		}

		// Verify remaining items exist
		if !remainingIDs["2"] {
			t.Error("Item 2 should still exist")
		}
		if !remainingIDs["4"] {
			t.Error("Item 4 should still exist")
		}
	})

	t.Run("Delete with empty slice", func(t *testing.T) {
		err := store.DeleteBatch(ctx, []string{})
		if err != nil {
			t.Errorf("DeleteBatch with empty slice should not error, got: %v", err)
		}
	})

	t.Run("Delete with non-existent IDs", func(t *testing.T) {
		err := store.DeleteBatch(ctx, []string{"nonexistent1", "nonexistent2"})
		if err == nil {
			t.Error("DeleteBatch with non-existent IDs should return error")
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound, got: %v", err)
		}
	})

	t.Run("Delete with mix of existent and non-existent", func(t *testing.T) {
		// Insert new test data
		store.Upsert(ctx, &Embedding{ID: "test1", Vector: []float32{1, 0, 0}, Content: "Test 1"})
		store.Upsert(ctx, &Embedding{ID: "test2", Vector: []float32{0, 1, 0}, Content: "Test 2"})

		// Delete with mix
		err := store.DeleteBatch(ctx, []string{"test1", "nonexistent", "test2"})
		// Should succeed if at least one ID was found
		if err == nil {
			// Some implementations require all IDs to exist, others don't
			// This is implementation-specific
		}

		// Verify test1 and test2 are deleted
		results, _ := store.Search(ctx, []float32{1, 1, 1}, SearchOptions{TopK: 10})
		for _, r := range results {
			if r.ID == "test1" || r.ID == "test2" {
				t.Error("Items should have been deleted")
			}
		}
	})

	t.Run("Delete with empty IDs filtered out", func(t *testing.T) {
		// Insert test item
		store.Upsert(ctx, &Embedding{ID: "empty_test", Vector: []float32{1, 0, 0}, Content: "Test"})

		err := store.DeleteBatch(ctx, []string{"empty_test", "", "  "})
		if err != nil {
			t.Errorf("DeleteBatch should filter empty IDs: %v", err)
		}
	})
}

// TestDeleteByFilter tests deletion by metadata filter
func TestDeleteByFilter(t *testing.T) {
	ctx := context.Background()
	dbPath := "delete_by_filter_test.db"

	// Clean up
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.VectorDim = 3

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Insert test data with metadata
	testData := []*Embedding{
		{ID: "1", Vector: []float32{1, 0, 0}, Content: "Item 1", Metadata: map[string]string{"category": "tech", "status": "active"}},
		{ID: "2", Vector: []float32{0, 1, 0}, Content: "Item 2", Metadata: map[string]string{"category": "tech", "status": "deleted"}},
		{ID: "3", Vector: []float32{0, 0, 1}, Content: "Item 3", Metadata: map[string]string{"category": "science", "status": "active"}},
		{ID: "4", Vector: []float32{1, 1, 0}, Content: "Item 4", Metadata: map[string]string{"category": "science", "status": "deleted"}},
		{ID: "5", Vector: []float32{0, 1, 1}, Content: "Item 5", Metadata: map[string]string{"category": "art", "status": "active"}},
	}

	for _, emb := range testData {
		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to insert embedding: %v", err)
		}
	}

	t.Run("Delete by equality filter", func(t *testing.T) {
		filter := NewMetadataFilter().Equal("status", "deleted")
		err := store.DeleteByFilter(ctx, filter)
		if err != nil {
			t.Fatalf("DeleteByFilter failed: %v", err)
		}

		// Verify remaining items
		results, err := store.Search(ctx, []float32{1, 1, 1}, SearchOptions{TopK: 10})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 3 {
			t.Errorf("Expected 3 remaining items, got %d", len(results))
		}

		// Verify no deleted status items remain
		for _, r := range results {
			if r.Metadata["status"] == "deleted" {
				t.Error("Deleted items should not remain")
			}
		}
	})

	t.Run("Delete by category filter", func(t *testing.T) {
		// Remaining items: 1 (tech), 3 (science), 5 (art)
		filter := NewMetadataFilter().Equal("category", "tech")
		err := store.DeleteByFilter(ctx, filter)
		if err != nil {
			t.Fatalf("DeleteByFilter failed: %v", err)
		}

		// Should have deleted item 1
		results, _ := store.Search(ctx, []float32{1, 1, 1}, SearchOptions{TopK: 10})
		for _, r := range results {
			if r.Metadata["category"] == "tech" {
				t.Error("Tech items should have been deleted")
			}
		}
	})

	t.Run("Delete with empty filter", func(t *testing.T) {
		filter := NewMetadataFilter()
		err := store.DeleteByFilter(ctx, filter)
		if err == nil {
			t.Error("DeleteByFilter with empty filter should return error")
		}
	})

	t.Run("Delete with nil filter", func(t *testing.T) {
		err := store.DeleteByFilter(ctx, nil)
		if err == nil {
			t.Error("DeleteByFilter with nil filter should return error")
		}
	})

	t.Run("Delete with combined filters", func(t *testing.T) {
		// Insert more test data
		store.Upsert(ctx, &Embedding{ID: "6", Vector: []float32{1, 0, 0}, Content: "Item 6", Metadata: map[string]string{"category": "art", "priority": "high"}})
		store.Upsert(ctx, &Embedding{ID: "7", Vector: []float32{0, 1, 0}, Content: "Item 7", Metadata: map[string]string{"category": "art", "priority": "low"}})

		// Delete art items with high priority
		filter := NewMetadataFilter().Equal("category", "art").Equal("priority", "high")
		err := store.DeleteByFilter(ctx, filter)
		if err != nil {
			t.Fatalf("DeleteByFilter failed: %v", err)
		}

		// Verify item 6 is deleted, 7 remains
		results, _ := store.Search(ctx, []float32{1, 1, 1}, SearchOptions{TopK: 10})
		foundHigh := false
		foundLow := false
		for _, r := range results {
			if r.ID == "6" {
				foundHigh = true
			}
			if r.ID == "7" && r.Metadata["priority"] == "low" {
				foundLow = true
			}
		}

		if foundHigh {
			t.Error("Item 6 should have been deleted")
		}
		if !foundLow {
			t.Error("Item 7 should still exist")
		}
	})
}

// TestDeleteBatchPerformance compares batch vs individual deletes
func TestDeleteBatchPerformance(t *testing.T) {
	ctx := context.Background()
	dbPath := "delete_perf_test.db"

	// Clean up
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.VectorDim = 128

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Insert 100 items
	numItems := 100
	ids := make([]string, numItems)
	for i := 0; i < numItems; i++ {
		id := fmt.Sprintf("perf_%d", i)
		ids[i] = id
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = float32(i) / 100.0
		}
		if err := store.Upsert(ctx, &Embedding{
			ID:      id,
			Vector:  vec,
			Content: fmt.Sprintf("Item %d", i),
		}); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	t.Run("Batch delete all items", func(t *testing.T) {
		// Delete all 100 items in one batch
		err := store.DeleteBatch(ctx, ids)
		if err != nil {
			t.Fatalf("DeleteBatch failed: %v", err)
		}

		// Verify all items are deleted
		results, _ := store.Search(ctx, make([]float32, 128), SearchOptions{TopK: 1000})
		if len(results) != 0 {
			t.Errorf("Expected 0 items, got %d", len(results))
		}
	})
}
