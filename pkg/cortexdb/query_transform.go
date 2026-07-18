package cortexdb

import (
	"context"
	"strings"
)

// QueryTransform is the result of pre-retrieval query transformation. It lets a
// caller-supplied model rewrite a raw user query into signals that retrieve
// better than the literal question:
//
//   - AlternateQueries — paraphrases / sub-questions fused into the lexical and
//     graph seed expansion (multi-query retrieval).
//   - Keywords — salient terms, synonyms, aliases, and multilingual variants
//     that seed lexical recall.
//   - HypotheticalDocument — a HyDE passage: a plausible answer to the query.
//     When an embedder is present, the *semantic* query vector is derived from
//     this passage instead of the raw question, so vector search matches
//     passages by content rather than by question phrasing.
type QueryTransform struct {
	AlternateQueries     []string
	Keywords             []string
	HypotheticalDocument string
}

// QueryTransformer rewrites a raw query before retrieval. CortexDB never imports
// a model SDK — a transformer is supplied via WithQueryTransformer (e.g. an
// OpenAI-compatible chat endpoint that returns the JSON shape above). It stays
// optional: without one, retrieval uses the raw query and caller-provided
// keywords/alternates unchanged.
type QueryTransformer interface {
	// TransformQuery returns rewrite signals for query. Returning a nil result
	// (or an error) leaves retrieval on the raw query — it must never make
	// retrieval fail.
	TransformQuery(ctx context.Context, query string) (*QueryTransform, error)
}

// applyQueryTransform runs the optional query transformer and merges its
// rewrite signals into req: AlternateQueries and Keywords are folded in
// (caller-provided values are kept and take precedence in order; duplicates are
// dropped). It returns the HyDE hypothetical-document text for the embedding
// step, or "" when there is none.
//
// It is best-effort throughout: no transformer, an empty query, a transformer
// error, or a nil result all leave req untouched and return "" — retrieval must
// never fail because an optional transformer did.
func (db *DB) applyQueryTransform(ctx context.Context, req *KnowledgeSearchRequest) string {
	if db.queryTransformer == nil || req == nil {
		return ""
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return ""
	}
	out, err := db.queryTransformer.TransformQuery(ctx, query)
	if err != nil || out == nil {
		return ""
	}
	if len(out.AlternateQueries) > 0 {
		req.AlternateQueries = orderedUniqueNonEmptyStrings(append(append([]string{}, req.AlternateQueries...), out.AlternateQueries...))
	}
	if len(out.Keywords) > 0 {
		req.Keywords = orderedUniqueNonEmptyStrings(append(append([]string{}, req.Keywords...), out.Keywords...))
	}
	return strings.TrimSpace(out.HypotheticalDocument)
}
