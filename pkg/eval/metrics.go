// Package eval is a retrieval-quality evaluation harness for CortexDB. It runs a
// labeled query set against a retriever and reports standard information-
// retrieval metrics (recall@k, precision@k, MRR, nDCG@k), so retrieval quality
// is measured and regression-guarded rather than assumed.
package eval

import (
	"math"
	"sort"
)

// relevantSet builds a lookup of relevant document ids.
func relevantSet(relevant []string) map[string]struct{} {
	m := make(map[string]struct{}, len(relevant))
	for _, id := range relevant {
		m[id] = struct{}{}
	}
	return m
}

// relevantRanks returns the 0-based positions, within the top k, of the first
// occurrence of each relevant id. A retriever that returns one document twice —
// two chunks of the same session collapsed to its id, say — must not be paid
// twice for it: counted naively, recall could pass 1 and nDCG could beat the
// ideal ranking.
func relevantRanks(retrieved []string, rel map[string]struct{}, k int) []int {
	var ranks []int
	seen := make(map[string]struct{}, len(rel))
	for i, id := range retrieved {
		if i >= k {
			break
		}
		if _, ok := rel[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ranks = append(ranks, i)
	}
	return ranks
}

// RecallAtK is the fraction of relevant documents retrieved within the top k.
// Returns 0 when there are no relevant documents.
func RecallAtK(retrieved, relevant []string, k int) float64 {
	rel := relevantSet(relevant)
	if len(rel) == 0 {
		return 0
	}
	return float64(len(relevantRanks(retrieved, rel, k))) / float64(len(rel))
}

// HitAtK is 1 when at least one relevant document is within the top k and 0
// otherwise. LongMemEval calls this recall_any@k; it is what a reader who needs
// one piece of evidence experiences.
func HitAtK(retrieved, relevant []string, k int) float64 {
	rel := relevantSet(relevant)
	if len(rel) == 0 {
		return 0
	}
	if len(relevantRanks(retrieved, rel, k)) > 0 {
		return 1
	}
	return 0
}

// AllAtK is 1 when every relevant document is within the top k and 0
// otherwise. LongMemEval calls this recall_all@k; a multi-session question is
// only answerable when all of its evidence arrives.
func AllAtK(retrieved, relevant []string, k int) float64 {
	rel := relevantSet(relevant)
	if len(rel) == 0 {
		return 0
	}
	if len(relevantRanks(retrieved, rel, k)) == len(rel) {
		return 1
	}
	return 0
}

// PrecisionAtK is the fraction of the top k retrieved documents that are
// relevant. Returns 0 when k <= 0.
func PrecisionAtK(retrieved, relevant []string, k int) float64 {
	if k <= 0 || len(retrieved) == 0 {
		return 0
	}
	return float64(len(relevantRanks(retrieved, relevantSet(relevant), k))) / float64(k)
}

// ReciprocalRank is 1/rank of the first relevant document (rank starting at 1),
// or 0 if none is retrieved. Averaged across queries this is MRR.
func ReciprocalRank(retrieved, relevant []string) float64 {
	rel := relevantSet(relevant)
	for i, id := range retrieved {
		if _, ok := rel[id]; ok {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// NDCGAtK is the normalized discounted cumulative gain at k, with binary
// relevance. Returns 0 when there are no relevant documents.
func NDCGAtK(retrieved, relevant []string, k int) float64 {
	rel := relevantSet(relevant)
	if len(rel) == 0 {
		return 0
	}
	dcg := 0.0
	for _, i := range relevantRanks(retrieved, rel, k) {
		dcg += 1.0 / math.Log2(float64(i+2)) // gain 1, discount log2(rank+1)
	}
	// Ideal DCG: all relevant docs ranked first, capped at k.
	ideal := len(rel)
	if ideal > k {
		ideal = k
	}
	idcg := 0.0
	for i := 0; i < ideal; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// mean returns the average of xs, or 0 for an empty slice.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// sortedInts returns ks sorted ascending (stable, deduped).
func sortedInts(ks []int) []int {
	out := append([]int(nil), ks...)
	sort.Ints(out)
	dedup := out[:0]
	prev := -1
	for _, k := range out {
		if k != prev {
			dedup = append(dedup, k)
			prev = k
		}
	}
	return dedup
}
