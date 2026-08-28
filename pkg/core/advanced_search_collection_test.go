package core

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// A collection is how a caller says "only this book". Hybrid search runs a vector arm and a
// keyword arm; the keyword arm queries the shared FTS index, which spans every collection, so
// unless it is restricted the collection asked for is silently ignored and the caller is handed
// another tenant's text as if it were their own.
func TestHybridSearchKeywordArmStaysInsideTheCollection(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_hybrid_collection_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	store, err := New(dbPath, 4)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer func() { _ = store.Close() }()

	for _, collection := range []string{"book-calculus", "book-chinese"} {
		if _, err := store.CreateCollection(ctx, collection, 4); err != nil {
			t.Fatalf("create collection %s: %v", collection, err)
		}
	}
	for _, embedding := range []*Embedding{
		{ID: "calc-1", Collection: "book-calculus", Vector: []float32{1, 0, 0, 0}, Content: "continuity and differentiability"},
		{ID: "calc-2", Collection: "book-calculus", Vector: []float32{0, 1, 0, 0}, Content: "continuity at a point"},
		{ID: "chinese-1", Collection: "book-chinese", Vector: []float32{0, 0, 1, 0}, Content: "语文园地 continuity of the narrative"},
	} {
		if err := store.Upsert(ctx, embedding); err != nil {
			t.Fatalf("upsert %s: %v", embedding.ID, err)
		}
	}

	opts := HybridSearchOptions{}
	opts.Collection = "book-chinese"
	opts.TopK = 10

	// No vector: this is the shape SearchTextOnly uses, so the keyword arm is the only arm.
	results, err := store.HybridSearch(ctx, nil, "continuity", opts)
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("the collection's own matching chunk was not returned")
	}
	for _, result := range results {
		if result.Collection != "book-chinese" {
			t.Errorf("asked for book-chinese, got %s from %s: %q", result.ID, result.Collection, result.Content)
		}
	}

	// With a vector both arms run, and the keyword arm must not smuggle rows past the filter.
	results, err = store.HybridSearch(ctx, []float32{1, 0, 0, 0}, "continuity", opts)
	if err != nil {
		t.Fatalf("hybrid search with vector: %v", err)
	}
	for _, result := range results {
		if result.Collection != "book-chinese" {
			t.Errorf("with a vector: asked for book-chinese, got %s from %s", result.ID, result.Collection)
		}
	}

	// An unfiltered search still reaches everything, so the fix restricts rather than breaks.
	opts.Collection = ""
	results, err = store.HybridSearch(ctx, nil, "continuity", opts)
	if err != nil {
		t.Fatalf("unfiltered hybrid search: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("unfiltered search should span collections, got %d results", len(results))
	}
}
