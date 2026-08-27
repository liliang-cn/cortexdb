package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A vector index is asked for the globally nearest rows; the collection and
// metadata filters are applied to what it returns. That is a post-filter, and
// a post-filter loses rows silently: a collection holding one chunk out of
// twelve was simply not among the nearest ten, so a search scoped to it came
// back empty while the same query unscoped returned plenty.
//
// It reads as a retrieval that found nothing — the honest-looking failure —
// and it gets worse as the store grows, which is the opposite of what a test
// on a four-row fixture will show.
func TestSearchScopedToACollectionDoesNotLoseItToTheIndex(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scoped.db")

	cfg := DefaultConfig()
	cfg.Path = path
	cfg.VectorDim = 4
	cfg.IndexType = IndexTypeHNSW
	cfg.HNSW = DefaultHNSWConfig()
	cfg.HNSW.Enabled = true

	store, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() {
		store.Close()
		os.Remove(path)
	}()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, name := range []string{"bulk", "rare"} {
		if _, err := store.CreateCollection(ctx, name, 4); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	// Forty rows crowded around the query, and one row in another collection
	// that is further away than all of them. Nothing about the far row is
	// wrong — it is simply not in the global top-k, which is the whole point.
	query := []float32{1, 0, 0, 0}
	for i := 0; i < 40; i++ {
		drift := float32(i) / 1000
		if err := store.Upsert(ctx, &Embedding{
			ID:         fmt.Sprintf("bulk-%02d", i),
			Vector:     []float32{1 - drift, drift, 0, 0},
			Content:    fmt.Sprintf("bulk %d", i),
			Collection: "bulk",
		}); err != nil {
			t.Fatalf("upsert bulk-%02d: %v", i, err)
		}
	}
	if err := store.Upsert(ctx, &Embedding{
		ID:         "rare-01",
		Vector:     []float32{0, 0, 1, 0},
		Content:    "the only row in its collection",
		Collection: "rare",
	}); err != nil {
		t.Fatalf("upsert rare-01: %v", err)
	}

	hits, err := store.Search(ctx, query, SearchOptions{TopK: 5, Collection: "rare"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("a search scoped to a non-empty collection returned nothing")
	}
	for _, h := range hits {
		if h.ID != "rare-01" {
			t.Errorf("collection filter leaked %q", h.ID)
		}
	}
}
