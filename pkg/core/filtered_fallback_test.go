package core

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// heldWhileScoring measures what a search holds while it scores its rows.
// At the first comparison of a vector with the query, and at every eighth
// after it, the heap is collected and what is still reachable is read; most
// is the largest reading above what was reachable when start was called.
// The readings are taken at the same points of the search at every run:
// nothing is sampled from another goroutine.
type heldWhileScoring struct {
	base, most uint64
	scored     int
}

func (h *heldWhileScoring) reachable() uint64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func (h *heldWhileScoring) start() {
	h.most, h.scored = 0, 0
	h.base = h.reachable()
}

// similarity is the store's similarity function, which counts and reads.
func (h *heldWhileScoring) similarity(a, b []float32) float64 {
	h.scored++
	if h.scored%8 == 1 {
		if now := h.reachable(); now > h.base && now-h.base > h.most {
			h.most = now - h.base
		}
	}
	return CosineSimilarity(a, b)
}

// seedTwoRegions fills a store with a small collection near +x and a large
// one near -x whose rows carry rowBytes of metadata each: the full-text
// index reads the content of a row, not its metadata, so the rows are heavy
// to hold and cheap to write.
func seedTwoRegions(t *testing.T, store *SQLiteStore, farRows, rowBytes int) {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{"near", "far"} {
		if _, err := store.CreateCollection(ctx, name, 4); err != nil {
			t.Fatal(err)
		}
	}
	near := make([]*Embedding, 40)
	for i := range near {
		near[i] = &Embedding{ID: fmt.Sprintf("near%03d", i), Collection: "near", Vector: []float32{1, float32(i) / 1000, 0, 0}, Content: "near"}
	}
	if err := store.UpsertBatch(ctx, near); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", rowBytes)
	for start := 0; start < farRows; start += 50 {
		far := make([]*Embedding, 0, 50)
		for i := start; i < start+50 && i < farRows; i++ {
			far = append(far, &Embedding{
				ID: fmt.Sprintf("far%04d", i), Collection: "far",
				Vector:   []float32{-1, float32(i) / 10000, 0, 0},
				Content:  "far",
				Metadata: map[string]string{"parity": fmt.Sprint(i % 2), "body": body},
			})
		}
		if err := store.UpsertBatch(ctx, far); err != nil {
			t.Fatal(err)
		}
	}
}

// The index is asked for the nearest rows of the whole store. When too few of
// them pass the collection or metadata filter, the search falls back to a
// scan of the rows that do. That scan keeps the best TopK as it goes: it used
// to collect every matching row first, vectors, content and metadata, and a
// search scoped to a large collection held that collection in memory.
func TestFilteredFallbackHoldsOnlyTheBestRows(t *testing.T) {
	const farRows, rowBytes = 96, 32 << 10 // 3 MiB of metadata
	for _, indexed := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"hnsw", nil},
		{"binary", func(c *Config) { c.IndexType = IndexTypeBinary }},
	} {
		t.Run(indexed.name, func(t *testing.T) {
			var held heldWhileScoring
			store := openResourceStore(t, filepath.Join(t.TempDir(), "s.db"), func(c *Config) {
				c.VectorDim = 4
				c.SimilarityFn = held.similarity
				if indexed.mutate != nil {
					indexed.mutate(c)
				}
			})
			defer func() { _ = store.Close() }()
			seedTwoRegions(t, store, farRows, rowBytes)
			ctx := context.Background()

			// selected is the rows the options select, which the scan scores
			// one by one. The binary index shortlists enough far rows for a
			// TopK of 9 and does not fall back: it holds its shortlist.
			for _, searched := range []struct {
				opts     SearchOptions
				selected int
			}{
				{SearchOptions{TopK: 5, Collection: "far"}, farRows},
				{SearchOptions{TopK: 5, Filter: map[string]string{"parity": "1"}}, farRows / 2},
				{SearchOptions{TopK: 9, Collection: "far"}, farRows},
			} {
				opts := searched.opts
				if indexed.name == "binary" && opts.TopK == 9 {
					searched.selected = 0
				}
				held.start()
				hits, err := store.Search(ctx, []float32{1, 0, 0, 0}, opts)
				if err != nil {
					t.Fatal(err)
				}
				if len(hits) != opts.TopK {
					t.Fatalf("%+v: %d results, want %d", opts, len(hits), opts.TopK)
				}
				for _, hit := range hits {
					if hit.Collection != "far" {
						t.Fatalf("%+v: got %s of collection %q", opts, hit.ID, hit.Collection)
					}
				}
				// The old scan held the whole collection while it scored it;
				// this one holds TopK rows and the one it reads. Half the
				// collection tells them apart.
				if held.scored < searched.selected {
					t.Fatalf("%+v: %d vectors scored, the search did not score the %d rows the options select", opts, held.scored, searched.selected)
				}
				if limit := uint64(farRows * rowBytes / 2); held.most > limit {
					t.Fatalf("%+v: the search held %d KiB while it scored, half the collection is %d KiB", opts, held.most>>10, limit>>10)
				}
			}
		})
	}
}

// The threshold reaches the scan: of the rows of the collection, only those
// whose score passes it come back.
func TestFilteredFallbackAppliesTheThreshold(t *testing.T) {
	for _, indexed := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"hnsw", nil},
		{"binary", func(c *Config) { c.IndexType = IndexTypeBinary }},
	} {
		t.Run(indexed.name, func(t *testing.T) {
			store := openResourceStore(t, filepath.Join(t.TempDir(), "s.db"), func(c *Config) {
				c.VectorDim = 4
				if indexed.mutate != nil {
					indexed.mutate(c)
				}
			})
			defer func() { _ = store.Close() }()
			seedTwoRegions(t, store, 60, 16)
			ctx := context.Background()
			// A third collection, on the side of the query but behind the
			// near rows: its scores are positive and all differ.
			if _, err := store.CreateCollection(ctx, "mid", 4); err != nil {
				t.Fatal(err)
			}
			mid := make([]*Embedding, 20)
			for i := range mid {
				mid[i] = &Embedding{ID: fmt.Sprintf("mid%02d", i), Collection: "mid", Vector: []float32{1, 1 + float32(i)/10, 0, 0}, Content: "mid"}
			}
			if err := store.UpsertBatch(ctx, mid); err != nil {
				t.Fatal(err)
			}
			query := []float32{1, 0, 0, 0}
			best, err := store.Search(ctx, query, SearchOptions{TopK: 5, Collection: "mid"})
			if err != nil || len(best) != 5 {
				t.Fatalf("%d mid rows, %v, want 5", len(best), err)
			}
			// A threshold between the third and the fourth score keeps three.
			threshold := (best[2].Score + best[3].Score) / 2
			hits, err := store.Search(ctx, query, SearchOptions{TopK: 5, Collection: "mid", Threshold: threshold})
			if err != nil {
				t.Fatal(err)
			}
			if threshold <= 0 || len(hits) != 3 {
				t.Fatalf("threshold %v keeps %d rows, want a positive threshold and 3 rows", threshold, len(hits))
			}
		})
	}
}

// What the scan sets aside before it reads a row does not grow with TopK: a
// caller that asks for every row of a small collection, with a TopK as large
// as it likes, gets them. Sized by TopK, that room was 648 MiB for a TopK of
// five million, and a panic for the largest int.
func TestFilteredFallbackTakesAnyTopK(t *testing.T) {
	for _, indexed := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"hnsw", nil},
		{"binary", func(c *Config) { c.IndexType = IndexTypeBinary }},
		{"none", func(c *Config) { c.IndexType = IndexTypeFlat; c.HNSW.Enabled = false }},
	} {
		t.Run(indexed.name, func(t *testing.T) {
			var held heldWhileScoring
			store := openResourceStore(t, filepath.Join(t.TempDir(), "s.db"), func(c *Config) {
				c.VectorDim = 4
				c.SimilarityFn = held.similarity
				if indexed.mutate != nil {
					indexed.mutate(c)
				}
			})
			defer func() { _ = store.Close() }()
			seedTwoRegions(t, store, 60, 16)
			ctx := context.Background()
			for _, topK := range []int{5_000_000, math.MaxInt} {
				held.start()
				hits, err := store.Search(ctx, []float32{1, 0, 0, 0}, SearchOptions{TopK: topK, Collection: "far"})
				if err != nil {
					t.Fatal(err)
				}
				if len(hits) != 60 {
					t.Fatalf("TopK %d: %d results, want the 60 far rows", topK, len(hits))
				}
				if held.scored < 60 {
					t.Fatalf("TopK %d: %d vectors scored, the search did not score the 60 rows", topK, held.scored)
				}
				if held.most > 8<<20 {
					t.Fatalf("TopK %d: the search held %d MiB while it scored 60 small rows", topK, held.most>>20)
				}
			}
		})
	}
}
