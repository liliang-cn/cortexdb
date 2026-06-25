package cortexdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// TestSearchTextOnlyAuthorize verifies the retrieval-layer security gate:
// only rows the Authorize predicate accepts count toward TopK, and the search
// over-fetches internally so authorized results still fill TopK.
func TestSearchTextOnlyAuthorize(t *testing.T) {
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "auth.db"))
	cfg.Dimensions = 1
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	docs := []*core.Embedding{
		{ID: "pub-1", Vector: []float32{0}, Content: "alpha public report", Metadata: map[string]string{"level": "public"}},
		{ID: "sec-1", Vector: []float32{0}, Content: "alpha secret report", Metadata: map[string]string{"level": "secret"}},
		{ID: "pub-2", Vector: []float32{0}, Content: "alpha public memo", Metadata: map[string]string{"level": "public"}},
		{ID: "sec-2", Vector: []float32{0}, Content: "alpha secret memo", Metadata: map[string]string{"level": "secret"}},
	}
	for _, d := range docs {
		if err := db.Vector().Upsert(ctx, d); err != nil {
			t.Fatalf("upsert %s: %v", d.ID, err)
		}
	}

	// Without a gate: secret docs are reachable.
	all, err := db.SearchTextOnly(ctx, "alpha", TextSearchOptions{TopK: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("want 4 unfiltered results, got %d", len(all))
	}

	// With a gate that allows only public: secret docs must never appear.
	publicOnly, err := db.SearchTextOnly(ctx, "alpha", TextSearchOptions{
		TopK:      10,
		Authorize: func(e core.ScoredEmbedding) bool { return e.Metadata["level"] == "public" },
	})
	if err != nil {
		t.Fatalf("gated search: %v", err)
	}
	if len(publicOnly) != 2 {
		t.Fatalf("want 2 authorized results, got %d", len(publicOnly))
	}
	for _, r := range publicOnly {
		if r.Metadata["level"] != "public" {
			t.Fatalf("security gate leaked %s (level=%s)", r.ID, r.Metadata["level"])
		}
	}

	// Over-fetch: even with TopK=1, the gate must still return an authorized row
	// rather than dropping to empty because the top raw hit was unauthorized.
	one, err := db.SearchTextOnly(ctx, "alpha", TextSearchOptions{
		TopK:      1,
		Authorize: func(e core.ScoredEmbedding) bool { return e.Metadata["level"] == "public" },
	})
	if err != nil {
		t.Fatalf("topk1 gated search: %v", err)
	}
	if len(one) != 1 || one[0].Metadata["level"] != "public" {
		t.Fatalf("want 1 public result, got %+v", one)
	}
}

// TestHybridSearchTextWithOptionsRerankAndThreshold verifies the
// retrieve→authorize→rerank→MinScore→TopK pipeline: a reranker reorders by a
// supplied score, MinScore drops weak tail, TopK truncates.
func TestHybridSearchTextWithOptionsRerankAndThreshold(t *testing.T) {
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "rerank.db"))
	cfg.Dimensions = 1
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	docs := []*core.Embedding{
		{ID: "d1", Vector: []float32{0}, Content: "alpha one"},
		{ID: "d2", Vector: []float32{0}, Content: "alpha two"},
		{ID: "d3", Vector: []float32{0}, Content: "alpha three"},
	}
	for _, d := range docs {
		if err := db.Vector().Upsert(ctx, d); err != nil {
			t.Fatalf("upsert %s: %v", d.ID, err)
		}
	}

	// Reranker assigns d3 highest, d1 mid, d2 below the MinScore floor.
	scores := map[string]float64{"d1": 0.4, "d2": 0.05, "d3": 0.9}
	reranker := core.RerankerFunc(func(_ context.Context, _ string, results []core.ScoredEmbedding) ([]core.ScoredEmbedding, error) {
		out := make([]core.ScoredEmbedding, len(results))
		copy(out, results)
		for i := range out {
			out[i].Score = scores[out[i].ID]
		}
		// emulate a reranker returning best-first
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if out[j].Score > out[i].Score {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		return out, nil
	})

	got, err := db.HybridSearchTextWithOptions(ctx, "alpha", TextSearchOptions{
		TopK:     5,
		Reranker: reranker,
		MinScore: 0.1, // drops d2 (0.05)
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 results after MinScore filter, got %d: %+v", len(got), got)
	}
	if got[0].ID != "d3" || got[1].ID != "d1" {
		t.Fatalf("want rerank order [d3 d1], got [%s %s]", got[0].ID, got[1].ID)
	}
}
