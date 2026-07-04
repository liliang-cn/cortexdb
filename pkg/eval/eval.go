package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Retriever returns document ids ranked most-relevant first for a query. It is
// the single seam the harness needs; wire any CortexDB retrieval path (lexical,
// vector, graph, hybrid) behind it.
type Retriever interface {
	Retrieve(ctx context.Context, query string, k int) ([]string, error)
}

// RetrieverFunc adapts a function to Retriever.
type RetrieverFunc func(ctx context.Context, query string, k int) ([]string, error)

func (f RetrieverFunc) Retrieve(ctx context.Context, query string, k int) ([]string, error) {
	return f(ctx, query, k)
}

// Report holds aggregate metrics over a query set, plus per-query detail.
type Report struct {
	Dataset   string             `json:"dataset"`
	NumQueries int               `json:"num_queries"`
	Ks        []int              `json:"ks"`
	RecallAtK  map[int]float64   `json:"recall_at_k"`
	PrecisionAtK map[int]float64 `json:"precision_at_k"`
	NDCGAtK   map[int]float64    `json:"ndcg_at_k"`
	MRR       float64            `json:"mrr"`
	PerQuery  []QueryResult      `json:"per_query,omitempty"`
}

// QueryResult is a single query's retrieval and its reciprocal rank.
type QueryResult struct {
	QueryID   string   `json:"query_id"`
	Retrieved []string `json:"retrieved"`
	RR        float64  `json:"rr"`
}

// Run evaluates a retriever over the dataset at the given cutoffs. It retrieves
// max(ks) results per query once and computes every metric from that ranking.
func Run(ctx context.Context, ds *Dataset, r Retriever, ks ...int) (*Report, error) {
	if ds == nil {
		return nil, fmt.Errorf("eval: nil dataset")
	}
	if len(ks) == 0 {
		ks = []int{1, 3, 5, 10}
	}
	ks = sortedInts(ks)
	maxK := ks[len(ks)-1]

	rep := &Report{
		Dataset:      ds.Name,
		NumQueries:   len(ds.Queries),
		Ks:           ks,
		RecallAtK:    map[int]float64{},
		PrecisionAtK: map[int]float64{},
		NDCGAtK:      map[int]float64{},
	}
	recall := map[int][]float64{}
	precision := map[int][]float64{}
	ndcg := map[int][]float64{}
	var rrs []float64

	for _, q := range ds.Queries {
		retrieved, err := r.Retrieve(ctx, q.Text, maxK)
		if err != nil {
			return nil, fmt.Errorf("eval: retrieve %q: %w", q.ID, err)
		}
		for _, k := range ks {
			recall[k] = append(recall[k], RecallAtK(retrieved, q.Relevant, k))
			precision[k] = append(precision[k], PrecisionAtK(retrieved, q.Relevant, k))
			ndcg[k] = append(ndcg[k], NDCGAtK(retrieved, q.Relevant, k))
		}
		rr := ReciprocalRank(retrieved, q.Relevant)
		rrs = append(rrs, rr)
		rep.PerQuery = append(rep.PerQuery, QueryResult{QueryID: q.ID, Retrieved: retrieved, RR: rr})
	}

	for _, k := range ks {
		rep.RecallAtK[k] = mean(recall[k])
		rep.PrecisionAtK[k] = mean(precision[k])
		rep.NDCGAtK[k] = mean(ndcg[k])
	}
	rep.MRR = mean(rrs)
	return rep, nil
}

// Summary renders the aggregate metrics as a stable, human-readable block.
func (r *Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dataset=%s queries=%d\n", r.Dataset, r.NumQueries)
	fmt.Fprintf(&b, "MRR=%.3f\n", r.MRR)
	ks := append([]int(nil), r.Ks...)
	sort.Ints(ks)
	for _, k := range ks {
		fmt.Fprintf(&b, "@%-2d  recall=%.3f  precision=%.3f  ndcg=%.3f\n",
			k, r.RecallAtK[k], r.PrecisionAtK[k], r.NDCGAtK[k])
	}
	return b.String()
}
