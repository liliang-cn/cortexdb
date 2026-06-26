package cortexdb

import "math"

// Reranking — a generic, dependency-free cross-encoder reorder over any
// retrieval results. CortexDB has always reranked GraphRAG chunks internally;
// this exposes the exact same logic as a public API so callers running their own
// retrieval (BM25, hybrid, external) can reuse it instead of reinventing the
// blend. The internal GraphRAG path delegates here, so behavior stays in lockstep.

// RerankItem is one candidate to rerank. Only ID, Text, and Score are required;
// Entities and GroupKey enrich the entity-overlap and diversity signals.
type RerankItem struct {
	ID       string   // caller's identifier (carried through, opaque to Rerank)
	Text     string   // primary content scored against the query
	Score    float64  // base retrieval score (any scale; min-max normalized internally)
	Entities []string // optional: entity strings for the entity-overlap signal
	GroupKey string   // optional: diversity/dedup group (e.g. a document id)

	// RerankScore is the blended relevance, filled in by Rerank.
	RerankScore float64
}

// RerankOptions tunes the relevance blend and MMR diversity. Zero values fall
// back to the defaults CortexDB uses internally (0.6/0.25/0.15, lambda 0.75).
type RerankOptions struct {
	TopN            int     // keep at most N results (0 = keep all)
	DiversityLambda float64 // 0..1: relevance vs. novelty in MMR selection (default 0.75)
	BaseWeight      float64 // weight of the normalized base score (default 0.60)
	TermWeight      float64 // weight of query/text term overlap (default 0.25)
	EntityWeight    float64 // weight of query/item entity overlap (default 0.15)
}

func (o *RerankOptions) withDefaults() {
	if o.DiversityLambda <= 0 || o.DiversityLambda > 1 {
		o.DiversityLambda = 0.75
	}
	if o.BaseWeight == 0 && o.TermWeight == 0 && o.EntityWeight == 0 {
		o.BaseWeight, o.TermWeight, o.EntityWeight = 0.60, 0.25, 0.15
	}
}

// Rerank re-scores candidates jointly with the query — normalized base score +
// query/text term overlap + query/item entity overlap — then selects with
// Maximal Marginal Relevance so the head is both relevant and non-redundant.
// Items whose GroupKey matches an already-selected item are penalized as
// near-duplicates. The returned slice is ordered best-first with RerankScore set.
func Rerank(query string, items []RerankItem, opts RerankOptions) []RerankItem {
	if len(items) == 0 {
		return nil
	}
	opts.withDefaults()

	queryTerms := tokenSet(query)
	queryEntities := tokenSet(joinStrings(extractEntityNames(extractTitleEntities(query))))

	normalized := normalizeScores(items)
	scored := make([]RerankItem, len(items))
	for i, it := range items {
		termOverlap := overlapScore(queryTerms, tokenSet(it.Text))
		entityOverlap := overlapScore(queryEntities, tokenSet(joinStrings(it.Entities)))
		it.RerankScore = normalized[i]*opts.BaseWeight + termOverlap*opts.TermWeight + entityOverlap*opts.EntityWeight
		scored[i] = it
	}

	limit := opts.TopN
	if limit <= 0 || limit > len(scored) {
		limit = len(scored)
	}

	// MMR: greedily pick the item maximizing λ·relevance − (1−λ)·redundancy.
	selected := make([]RerankItem, 0, limit)
	remaining := append([]RerankItem(nil), scored...)
	for len(remaining) > 0 && len(selected) < limit {
		bestIdx := 0
		bestScore := -math.MaxFloat64
		for i := range remaining {
			redundancy := maxItemRedundancy(remaining[i], selected)
			score := opts.DiversityLambda*remaining[i].RerankScore - (1-opts.DiversityLambda)*redundancy
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	return selected
}

// normalizeScores min-max normalizes the base scores to 0..1 (all-equal → all 1).
func normalizeScores(items []RerankItem) []float64 {
	out := make([]float64, len(items))
	minS, maxS := items[0].Score, items[0].Score
	for _, it := range items[1:] {
		if it.Score < minS {
			minS = it.Score
		}
		if it.Score > maxS {
			maxS = it.Score
		}
	}
	if maxS-minS < 1e-9 {
		for i := range out {
			out[i] = 1
		}
		return out
	}
	for i, it := range items {
		out[i] = (it.Score - minS) / (maxS - minS)
	}
	return out
}

// maxItemRedundancy is the strongest text overlap between a candidate and any
// already-selected item; a shared GroupKey floors it at 0.85 (near-duplicate).
func maxItemRedundancy(candidate RerankItem, selected []RerankItem) float64 {
	if len(selected) == 0 {
		return 0
	}
	candTerms := tokenSet(candidate.Text)
	worst := 0.0
	for _, s := range selected {
		score := overlapScore(candTerms, tokenSet(s.Text))
		if candidate.GroupKey != "" && candidate.GroupKey == s.GroupKey {
			score = math.Max(score, 0.85)
		}
		if score > worst {
			worst = score
		}
	}
	return worst
}

func joinStrings(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
