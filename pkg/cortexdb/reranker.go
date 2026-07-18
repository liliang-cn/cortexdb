package cortexdb

import "context"

// Reranker is a cross-encoder that scores how relevant each document is to the
// query. It is the semantic upgrade to the built-in lexical-overlap rerank:
// where the heuristic Rerank blends term/entity overlap, a Reranker jointly
// encodes (query, document) with a model and returns a true relevance score.
//
// CortexDB never imports a model SDK — a Reranker is supplied via WithReranker
// (e.g. an OpenAI-compatible /rerank endpoint, Cohere, Jina, or a local
// text-embeddings-inference server running a bge-reranker). It stays optional:
// without one, retrieval uses the dependency-free heuristic rerank.
type Reranker interface {
	// Rerank returns one relevance score per document, in the same order as
	// documents (score[i] corresponds to documents[i]). Higher is more relevant;
	// the scale is arbitrary (the retrieval path normalizes before blending).
	Rerank(ctx context.Context, query string, documents []string) ([]float64, error)
}

// applySemanticRerank replaces each item's base Score with the reranker's
// query-document relevance, so the downstream heuristic blend + MMR runs on a
// semantic signal instead of lexical overlap. It is best-effort: a reranker
// error or a length mismatch leaves the original scores untouched (retrieval
// must never fail because an optional reranker did).
func (db *DB) applySemanticRerank(ctx context.Context, query string, items []RerankItem) {
	if db.reranker == nil || len(items) == 0 {
		return
	}
	docs := make([]string, len(items))
	for i := range items {
		docs[i] = items[i].Text
	}
	scores, err := db.reranker.Rerank(ctx, query, docs)
	if err != nil || len(scores) != len(items) {
		return
	}
	for i := range items {
		items[i].Score = scores[i]
	}
}
