package cortexdb

import (
	"context"
	"testing"
)

// scriptedReranker returns a preset relevance per document text, so a test can
// force a specific semantic ordering independent of lexical overlap.
type scriptedReranker struct {
	scores map[string]float64
	calls  int
}

func (r *scriptedReranker) Rerank(_ context.Context, _ string, docs []string) ([]float64, error) {
	r.calls++
	out := make([]float64, len(docs))
	for i, d := range docs {
		out[i] = r.scores[d]
	}
	return out, nil
}

// TestApplySemanticRerankOverridesBaseScore verifies a configured reranker's
// scores replace the base retrieval scores in place.
func TestApplySemanticRerankOverridesBaseScore(t *testing.T) {
	db := &DB{reranker: &scriptedReranker{scores: map[string]float64{
		"alpha": 0.1,
		"beta":  0.9,
	}}}
	items := []RerankItem{
		{ID: "1", Text: "alpha", Score: 0.8},
		{ID: "2", Text: "beta", Score: 0.2},
	}
	db.applySemanticRerank(context.Background(), "q", items)
	if items[0].Score != 0.1 || items[1].Score != 0.9 {
		t.Fatalf("expected reranker scores applied in place, got %v / %v", items[0].Score, items[1].Score)
	}
}

// TestApplySemanticRerankNoRerankerNoop verifies scores are untouched when no
// reranker is configured (the default path).
func TestApplySemanticRerankNoRerankerNoop(t *testing.T) {
	db := &DB{}
	items := []RerankItem{{ID: "1", Text: "x", Score: 0.5}}
	db.applySemanticRerank(context.Background(), "q", items)
	if items[0].Score != 0.5 {
		t.Fatalf("expected score untouched without a reranker, got %v", items[0].Score)
	}
}

// TestApplySemanticRerankToleratesBadLength verifies a reranker returning the
// wrong number of scores leaves the base scores intact (retrieval never fails
// because of an optional reranker).
func TestApplySemanticRerankToleratesBadLength(t *testing.T) {
	db := &DB{reranker: rerankerFunc(func(context.Context, string, []string) ([]float64, error) {
		return []float64{0.1}, nil // wrong length for 2 docs
	})}
	items := []RerankItem{
		{ID: "1", Text: "a", Score: 0.4},
		{ID: "2", Text: "b", Score: 0.6},
	}
	db.applySemanticRerank(context.Background(), "q", items)
	if items[0].Score != 0.4 || items[1].Score != 0.6 {
		t.Fatalf("expected scores untouched on length mismatch, got %v / %v", items[0].Score, items[1].Score)
	}
}

type rerankerFunc func(context.Context, string, []string) ([]float64, error)

func (f rerankerFunc) Rerank(ctx context.Context, q string, d []string) ([]float64, error) {
	return f(ctx, q, d)
}
