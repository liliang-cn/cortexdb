package cortexdb

import "testing"

// Rerank must promote a candidate whose text actually covers the query terms
// above a higher-base-score but off-topic candidate.
func TestRerankPromotesTermOverlap(t *testing.T) {
	// Equal base scores → only the joint query/text overlap signal decides, so
	// the term-overlapping candidate must surface first.
	items := []RerankItem{
		{ID: "off", Text: "completely unrelated content about gardening", Score: 1.0},
		{ID: "hit", Text: "the quarterly revenue report and revenue trends", Score: 1.0},
	}
	out := Rerank("revenue report", items, RerankOptions{})
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	if out[0].ID != "hit" {
		t.Errorf("expected term-overlapping 'hit' first, got %q (scores: %v/%v)",
			out[0].ID, out[0].RerankScore, out[1].RerankScore)
	}
}

// TopN truncates; the result carries RerankScore.
func TestRerankTopNAndScore(t *testing.T) {
	items := []RerankItem{
		{ID: "a", Text: "alpha revenue", Score: 0.9},
		{ID: "b", Text: "beta revenue", Score: 0.5},
		{ID: "c", Text: "gamma cost", Score: 0.1},
	}
	out := Rerank("revenue", items, RerankOptions{TopN: 2})
	if len(out) != 2 {
		t.Fatalf("TopN=2 should yield 2, got %d", len(out))
	}
	if out[0].RerankScore == 0 {
		t.Error("RerankScore should be populated")
	}
}

// MMR diversity: with two near-duplicates sharing a GroupKey, the second copy is
// penalized so a distinct item is preferred for the #2 slot.
func TestRerankMMRDiversity(t *testing.T) {
	items := []RerankItem{
		{ID: "dup1", Text: "revenue revenue revenue numbers", Score: 1.0, GroupKey: "doc1"},
		{ID: "dup2", Text: "revenue revenue revenue numbers", Score: 0.99, GroupKey: "doc1"},
		{ID: "div", Text: "revenue by region breakdown", Score: 0.5, GroupKey: "doc2"},
	}
	out := Rerank("revenue", items, RerankOptions{DiversityLambda: 0.5})
	if out[0].ID != "dup1" {
		t.Fatalf("strongest should lead, got %q", out[0].ID)
	}
	if out[1].ID != "div" {
		t.Errorf("MMR should prefer the diverse 'div' over near-duplicate 'dup2' at #2, got %q", out[1].ID)
	}
}

func TestRerankEmpty(t *testing.T) {
	if out := Rerank("q", nil, RerankOptions{}); out != nil {
		t.Errorf("empty input should return nil, got %v", out)
	}
}
