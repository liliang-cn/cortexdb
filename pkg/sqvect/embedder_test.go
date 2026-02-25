package sqvect

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"
)

// MockEmbedder for testing
type MockEmbedder struct {
	dim int
}

func NewMockEmbedder(dim int) *MockEmbedder {
	return &MockEmbedder{dim: dim}
}

func (m *MockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, m.dim)
	// Deterministic vector based on first char
	if len(text) > 0 {
		val := float32(text[0]) / 255.0
		for i := range vec {
			vec[i] = val
		}
	}
	return m.normalize(vec), nil
}

func (m *MockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, t := range texts {
		vec, _ := m.Embed(ctx, t)
		results[i] = vec
	}
	return results, nil
}

func (m *MockEmbedder) Dim() int {
	return m.dim
}

func (m *MockEmbedder) normalize(vec []float32) []float32 {
	var sum float32
	for _, v := range vec {
		sum += v * v
	}
	norm := float32(math.Sqrt(float64(sum)))
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}

func TestEmbedderIntegration(t *testing.T) {
	dbPath := fmt.Sprintf("test_embedder_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	dim := 4
	embedder := NewMockEmbedder(dim)
	
	db, err := Open(DefaultConfig(dbPath), WithEmbedder(embedder))
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	t.Run("InsertText", func(t *testing.T) {
		err := db.InsertText(ctx, "text1", "Apple", map[string]string{"type": "fruit"})
		if err != nil {
			t.Errorf("InsertText failed: %v", err)
		}

		emb, err := db.store.GetByID(ctx, "text1")
		if err != nil {
			t.Errorf("Failed to retrieve inserted text: %v", err)
		}
		if emb.Content != "Apple" {
			t.Errorf("Content mismatch: expected Apple, got %s", emb.Content)
		}
	})

	t.Run("InsertTextBatch", func(t *testing.T) {
		texts := map[string]string{
			"text2": "Banana",
			"text3": "Cherry",
		}
		err := db.InsertTextBatch(ctx, texts, map[string]string{"cat": "fruit"})
		if err != nil {
			t.Errorf("InsertTextBatch failed: %v", err)
		}

		emb, _ := db.store.GetByID(ctx, "text2")
		if emb == nil || emb.Content != "Banana" {
			t.Error("Failed to retrieve batch text2")
		}
	})

	t.Run("SearchText", func(t *testing.T) {
		results, err := db.SearchText(ctx, "Apple", 5)
		if err != nil {
			t.Errorf("SearchText failed: %v", err)
		}
		if len(results) == 0 {
			t.Error("SearchText returned no results")
		} else if results[0].ID != "text1" {
			t.Errorf("Expected text1 as top result, got %s", results[0].ID)
		}
	})

	t.Run("HybridSearchText", func(t *testing.T) {
		results, err := db.HybridSearchText(ctx, "Apple", 5)
		if err != nil {
			t.Errorf("HybridSearchText failed: %v", err)
		}
		if len(results) == 0 {
			t.Error("HybridSearchText returned no results")
		}
	})

	t.Run("QuickAddText", func(t *testing.T) {
		quick := db.Quick()
		id, err := quick.AddText(ctx, "Date", nil)
		if err != nil {
			t.Errorf("Quick AddText failed: %v", err)
		}
		if id == "" {
			t.Error("Quick AddText returned empty ID")
		}
	})
}

func TestSearchTextOnly(t *testing.T) {
	dbPath := fmt.Sprintf("test_textonly_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	quick := db.Quick()

	// Insert some data manually with vectors
	_, _ = quick.Add(ctx, []float32{1, 0, 0, 0}, "The quick brown fox")
	_, _ = quick.Add(ctx, []float32{0, 1, 0, 0}, "Jumps over the lazy dog")
	_, _ = quick.Add(ctx, []float32{0, 0, 1, 0}, "SQLite is awesome")

	t.Run("SearchTextOnly", func(t *testing.T) {
		results, err := db.SearchTextOnly(ctx, "fox", TextSearchOptions{TopK: 5})
		if err != nil {
			t.Fatalf("SearchTextOnly failed: %v", err)
		}
		if len(results) == 0 {
			t.Error("Expected results for 'fox', got none")
		} else if results[0].Content != "The quick brown fox" {
			t.Errorf("Expected 'The quick brown fox', got '%s'", results[0].Content)
		}
	})

	t.Run("SearchTextOnly_NoMatch", func(t *testing.T) {
		results, err := db.SearchTextOnly(ctx, "nonexistentword", TextSearchOptions{TopK: 5})
		if err != nil {
			t.Errorf("SearchTextOnly (no match) should not error: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("Expected 0 results, got %d", len(results))
		}
	})

	t.Run("QuickSearchTextOnly", func(t *testing.T) {
		results, err := quick.SearchTextOnly(ctx, "awesome", 5)
		if err != nil {
			t.Errorf("Quick.SearchTextOnly failed: %v", err)
		}
		if len(results) == 0 {
			t.Error("Quick.SearchTextOnly returned no results")
		}
	})
}

func TestEmbedderErrors(t *testing.T) {
	db, _ := Open(DefaultConfig(":memory:"))
	defer db.Close()
	ctx := context.Background()

	t.Run("EmbedderNotConfigured", func(t *testing.T) {
		err := db.InsertText(ctx, "id", "text", nil)
		if err != ErrEmbedderNotConfigured {
			t.Errorf("Expected ErrEmbedderNotConfigured, got %v", err)
		}

		_, err = db.SearchText(ctx, "text", 5)
		if err != ErrEmbedderNotConfigured {
			t.Errorf("Expected ErrEmbedderNotConfigured, got %v", err)
		}
	})
}
