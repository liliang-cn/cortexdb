package core

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"testing"
)

// TestHNSWSearchHonoursLargeTopK pins the bound that made deep retrieval
// impossible: ef is the size of the candidate list the graph walk keeps, so an
// ef below k truncates the result to ef and reports nothing. With the
// configured default of 50, every vector search returned ~50 rows no matter
// what TopK the caller asked for — a request for 2000 came back with 50, and
// everything ranked past it was unreachable by any caller.
func TestHNSWSearchHonoursLargeTopK(t *testing.T) {
	const (
		dim   = 16
		total = 400
		topK  = 150
	)
	config := DefaultConfig()
	config.Path = filepath.Join(t.TempDir(), "hnsw_topk.db")
	config.VectorDim = dim
	config.HNSW.Enabled = true
	config.HNSW.M = 16
	config.HNSW.EfConstruction = 200
	config.HNSW.EfSearch = 50 // the shipped default, and the whole point of the test

	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	rng := rand.New(rand.NewSource(7))
	batch := make([]*Embedding, 0, total)
	for i := 0; i < total; i++ {
		vec := make([]float32, dim)
		var norm float64
		for d := range vec {
			vec[d] = float32(rng.NormFloat64())
			norm += float64(vec[d]) * float64(vec[d])
		}
		norm = math.Sqrt(norm)
		for d := range vec {
			vec[d] /= float32(norm)
		}
		batch = append(batch, &Embedding{
			ID:      fmt.Sprintf("v%04d", i),
			Vector:  vec,
			Content: fmt.Sprintf("row %d", i),
		})
	}
	if err := store.UpsertBatch(ctx, batch); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	query := make([]float32, dim)
	query[0] = 1
	got, err := store.Search(ctx, query, SearchOptions{TopK: topK})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// Allowing a margin: HNSW is approximate, and the store may prune by score.
	// The regression this guards was absolute — exactly ~ef rows, whatever the
	// TopK — so anything near TopK proves ef is no longer the ceiling.
	if len(got) < topK*3/4 {
		t.Fatalf("TopK=%d returned %d results; ef is still capping the search", topK, len(got))
	}
}
