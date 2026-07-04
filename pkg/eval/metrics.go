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

// RecallAtK is the fraction of relevant documents retrieved within the top k.
// Returns 0 when there are no relevant documents.
func RecallAtK(retrieved, relevant []string, k int) float64 {
	rel := relevantSet(relevant)
	if len(rel) == 0 {
		return 0
	}
	hits := 0
	for i, id := range retrieved {
		if i >= k {
			break
		}
		if _, ok := rel[id]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(rel))
}

// PrecisionAtK is the fraction of the top k retrieved documents that are
// relevant. Returns 0 when k <= 0.
func PrecisionAtK(retrieved, relevant []string, k int) float64 {
	if k <= 0 {
		return 0
	}
	rel := relevantSet(relevant)
	hits := 0
	n := 0
	for i, id := range retrieved {
		if i >= k {
			break
		}
		n++
		if _, ok := rel[id]; ok {
			hits++
		}
	}
	if n == 0 {
		return 0
	}
	return float64(hits) / float64(k)
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
	for i, id := range retrieved {
		if i >= k {
			break
		}
		if _, ok := rel[id]; ok {
			dcg += 1.0 / math.Log2(float64(i+2)) // gain 1, discount log2(rank+1)
		}
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
