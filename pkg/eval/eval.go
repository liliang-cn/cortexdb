package eval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
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
	Dataset      string          `json:"dataset"`
	NumQueries   int             `json:"num_queries"`
	Ks           []int           `json:"ks"`
	RecallAtK    map[int]float64 `json:"recall_at_k"`
	PrecisionAtK map[int]float64 `json:"precision_at_k"`
	NDCGAtK      map[int]float64 `json:"ndcg_at_k"`
	// HitAtK and AllAtK are the any-evidence and all-evidence variants of
	// recall (LongMemEval's recall_any and recall_all). They differ from
	// RecallAtK only on queries with more than one relevant document.
	HitAtK  map[int]float64 `json:"hit_at_k"`
	AllAtK  map[int]float64 `json:"all_at_k"`
	MRR     float64         `json:"mrr"`
	Latency *LatencyStats   `json:"latency_ms,omitempty"`
	// PerQuery is the ranking each query got. It holds ids only, never the
	// text behind them, so a report can be kept next to the code even when
	// the dataset it came from cannot.
	PerQuery []QueryResult `json:"per_query,omitempty"`
}

// QueryResult is a single query's retrieval and its reciprocal rank.
type QueryResult struct {
	QueryID   string   `json:"query_id"`
	Retrieved []string `json:"retrieved"`
	RR        float64  `json:"rr"`
}

// LatencyStats summarises how long Retrieve took per query, in milliseconds.
// Percentiles use the nearest-rank method, so P95 is a latency some query
// actually had rather than an interpolation between two.
type LatencyStats struct {
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	Mean float64 `json:"mean"`
	Max  float64 `json:"max"`
}

// Accumulator scores queries one at a time and folds them into a Report. Run
// uses it for a single corpus; a benchmark whose every question carries its own
// haystack (LongMemEval) builds a fresh index per question and feeds the
// results into one Accumulator, so both shapes share one definition of every
// metric.
type Accumulator struct {
	name                                   string
	ks                                     []int
	recall, precision, ndcg, hit, complete map[int][]float64
	rrs                                    []float64
	latencies                              []float64
	perQuery                               []QueryResult
}

// NewAccumulator starts an empty report at the given cutoffs; with none it uses
// 1, 3, 5 and 10.
func NewAccumulator(name string, ks ...int) *Accumulator {
	if len(ks) == 0 {
		ks = []int{1, 3, 5, 10}
	}
	return &Accumulator{
		name:      name,
		ks:        sortedInts(ks),
		recall:    map[int][]float64{},
		precision: map[int][]float64{},
		ndcg:      map[int][]float64{},
		hit:       map[int][]float64{},
		complete:  map[int][]float64{},
	}
}

// MaxK is the deepest cutoff, which is how many results a retriever must be
// asked for.
func (a *Accumulator) MaxK() int { return a.ks[len(a.ks)-1] }

// Add scores one query. A negative latency means "not measured" and is left out
// of the latency statistics rather than counted as zero.
func (a *Accumulator) Add(queryID string, retrieved, relevant []string, latency time.Duration) {
	for _, k := range a.ks {
		a.recall[k] = append(a.recall[k], RecallAtK(retrieved, relevant, k))
		a.precision[k] = append(a.precision[k], PrecisionAtK(retrieved, relevant, k))
		a.ndcg[k] = append(a.ndcg[k], NDCGAtK(retrieved, relevant, k))
		a.hit[k] = append(a.hit[k], HitAtK(retrieved, relevant, k))
		a.complete[k] = append(a.complete[k], AllAtK(retrieved, relevant, k))
	}
	rr := ReciprocalRank(retrieved, relevant)
	a.rrs = append(a.rrs, rr)
	if latency >= 0 {
		a.latencies = append(a.latencies, float64(latency)/float64(time.Millisecond))
	}
	a.perQuery = append(a.perQuery, QueryResult{QueryID: queryID, Retrieved: retrieved, RR: rr})
}

// Report aggregates everything added so far.
func (a *Accumulator) Report() *Report {
	rep := &Report{
		Dataset:      a.name,
		NumQueries:   len(a.rrs),
		Ks:           append([]int(nil), a.ks...),
		RecallAtK:    map[int]float64{},
		PrecisionAtK: map[int]float64{},
		NDCGAtK:      map[int]float64{},
		HitAtK:       map[int]float64{},
		AllAtK:       map[int]float64{},
		MRR:          mean(a.rrs),
		Latency:      latencyStats(a.latencies),
		PerQuery:     append([]QueryResult(nil), a.perQuery...),
	}
	for _, k := range a.ks {
		rep.RecallAtK[k] = mean(a.recall[k])
		rep.PrecisionAtK[k] = mean(a.precision[k])
		rep.NDCGAtK[k] = mean(a.ndcg[k])
		rep.HitAtK[k] = mean(a.hit[k])
		rep.AllAtK[k] = mean(a.complete[k])
	}
	return rep
}

// latencyStats summarises millisecond samples, or returns nil when there are
// none, so a report built without timing does not claim a latency of zero.
func latencyStats(ms []float64) *LatencyStats {
	if len(ms) == 0 {
		return nil
	}
	sorted := append([]float64(nil), ms...)
	sort.Float64s(sorted)
	return &LatencyStats{
		P50:  Percentile(sorted, 50),
		P95:  Percentile(sorted, 95),
		Mean: mean(sorted),
		Max:  sorted[len(sorted)-1],
	}
}

// Percentile returns the nearest-rank p-th percentile of an ascending slice:
// the smallest sample with at least p percent of samples at or below it.
func Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// Run evaluates a retriever over the dataset at the given cutoffs. It retrieves
// max(ks) results per query once and computes every metric from that ranking.
func Run(ctx context.Context, ds *Dataset, r Retriever, ks ...int) (*Report, error) {
	if ds == nil {
		return nil, fmt.Errorf("eval: nil dataset")
	}
	acc := NewAccumulator(ds.Name, ks...)
	for _, q := range ds.Queries {
		start := time.Now()
		retrieved, err := r.Retrieve(ctx, q.Text, acc.MaxK())
		if err != nil {
			return nil, fmt.Errorf("eval: retrieve %q: %w", q.ID, err)
		}
		acc.Add(q.ID, retrieved, q.Relevant, time.Since(start))
	}
	return acc.Report(), nil
}

// Summary renders the aggregate metrics as a stable, human-readable block.
func (r *Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dataset=%s queries=%d\n", r.Dataset, r.NumQueries)
	fmt.Fprintf(&b, "MRR=%.3f\n", r.MRR)
	ks := append([]int(nil), r.Ks...)
	sort.Ints(ks)
	for _, k := range ks {
		fmt.Fprintf(&b, "@%-2d  recall=%.3f  precision=%.3f  ndcg=%.3f  hit=%.3f  all=%.3f\n",
			k, r.RecallAtK[k], r.PrecisionAtK[k], r.NDCGAtK[k], r.HitAtK[k], r.AllAtK[k])
	}
	if r.Latency != nil {
		fmt.Fprintf(&b, "latency ms  p50=%.1f  p95=%.1f  mean=%.1f  max=%.1f\n",
			r.Latency.P50, r.Latency.P95, r.Latency.Mean, r.Latency.Max)
	}
	return b.String()
}

// CollapseIDs maps each id through group and drops repeats, keeping the first
// position of each group. It turns a ranking of fine-grained units into a
// ranking of what they belong to — LoCoMo dialog turns into sessions — with
// each group ranked where its best member was. Ids that map to "" are dropped.
func CollapseIDs(ids []string, group func(string) string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		g := group(id)
		if g == "" {
			continue
		}
		if _, ok := seen[g]; ok {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	return out
}
