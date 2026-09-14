package core

// Aggregating the vectors, not the metadata beside them.
//
// aggregations.go already answers "what is the average price of the rows in
// this collection" — it reads a number out of the metadata JSON and lets the
// database add it up. What it cannot answer is "what does this set of vectors
// look like as one vector", which is the question every clustering, dedup and
// summarisation job actually asks. There was no way to get a centroid out of
// this store without pulling every row into the caller and writing the loop
// again, which is how three copies of the same loop start.
//
// Three answers, in increasing order of how much they cost and how much they
// are worth:
//
//   - centroid: the componentwise mean. One pass, closed form, and pulled off
//     course by a single distant member, because minimising the sum of
//     *squared* distances is what the mean does.
//   - geometric median: the point minimising the sum of plain distances.
//     Dropping the square is the whole difference, and it is what makes the
//     answer survive an outlier. No closed form; Weiszfeld's iteration below.
//   - medoid: the member of the set that is closest to all the others. Unlike
//     the first two it is a row that exists, so it comes back as an id. That
//     is the one with day-to-day value here — "which of these near-duplicate
//     memories is the canonical one", "give me the single record that stands
//     for this cluster" — and it answers it without an LLM in the loop.
//
// The maths is one set of pure functions over [][]float32 at the bottom of
// this file. Each backend contributes only "fetch the rows, hand them over":
// two implementations of an averaging loop that drifted apart would be a
// silent correctness bug, since nothing about a centroid is backend-specific.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// VectorAggregateKind names one of the three reductions.
type VectorAggregateKind string

const (
	VectorCentroid        VectorAggregateKind = "centroid"
	VectorGeometricMedian VectorAggregateKind = "geometric_median"
	VectorMedoid          VectorAggregateKind = "medoid"
)

// These three carry snake_case JSON tags because they cross the tool and MCP
// surface, where every neighbouring type is snake_case (see AggregationRequest
// in aggregations.go). Without them Go's default field names would leak out as
// "GroupBy" and "MemberID" beside a "group_by" from the metadata aggregations,
// and a caller would have to know which of the two aggregate families it was
// talking to in order to spell a field.

// VectorAggregateRequest selects the rows and says what to do with them.
type VectorAggregateRequest struct {
	Kind        VectorAggregateKind `json:"kind"`
	Collection  string              `json:"collection,omitempty"`
	Filter      map[string]string   `json:"filter,omitempty"`        // metadata equality filter
	GroupBy     string              `json:"group_by,omitempty"`      // metadata field; empty means one unnamed group over everything matched
	MaxPerGroup int                 `json:"max_per_group,omitempty"` // 0 = no cap
}

// VectorAggregateGroup is the answer for one group.
type VectorAggregateGroup struct {
	Group    string    `json:"group"`               // the GroupBy value; "" for the single ungrouped case
	Count    int       `json:"count"`               // vectors that went into this group
	Vector   []float32 `json:"vector,omitempty"`    // centroid / geometric_median; nil for medoid
	MemberID string    `json:"member_id,omitempty"` // medoid only: the id of the representative record
	Score    float64   `json:"score,omitempty"`     // medoid only: its mean similarity to the rest of the group
}

// VectorAggregateResponse echoes the request beside the groups, so a result
// travelling on its own still says what produced it.
type VectorAggregateResponse struct {
	Request VectorAggregateRequest `json:"request"`
	Groups  []VectorAggregateGroup `json:"groups"`
}

// Weiszfeld's iteration has no natural stopping point, so both of its limits
// are named here rather than left as literals in the loop — a reader has to be
// able to tell what "converged" was taken to mean without reading the body.
const (
	// weiszfeldMaxIterations bounds the work when the iteration crawls. It is
	// the guarantee: convergence is linear and slows near a solution, so the
	// epsilon below is the fast exit and this is the one that always fires.
	// 256 steps on a set that has not settled means the set is degenerate
	// (near-collinear, or spread so wide that every step is tiny), and the
	// last iterate is already far better than the centroid we started from.
	weiszfeldMaxIterations = 256

	// weiszfeldEpsilon stops the iteration once a step moves the estimate less
	// than this in Euclidean norm. It is 1e-7 because the result is handed
	// back as []float32, whose relative precision is about 1e-7: iterating
	// past the point where the change survives the cast buys nothing.
	weiszfeldEpsilon = 1e-7

	// weiszfeldCoincidenceEpsilon decides when an iterate has landed *on* an
	// input point. That is the degenerate case of the plain iteration — the
	// 1/distance weight divides by zero — and it is not rare, because the
	// median of a set with a heavy duplicate genuinely sits on that duplicate.
	// The threshold is far below weiszfeldEpsilon so that ordinary
	// near-convergence is never mistaken for a coincidence.
	weiszfeldCoincidenceEpsilon = 1e-12
)

// VectorAggregate reduces the matching vectors to one vector (or one member)
// per group.
func (s *SQLiteStore) VectorAggregate(ctx context.Context, req VectorAggregateRequest) (*VectorAggregateResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("vector_aggregate", ErrStoreClosed)
	}
	if err := validateVectorAggregateRequest(req); err != nil {
		return nil, wrapError("vector_aggregate", err)
	}

	// `vector` is the BLOB column; encoding.DecodeVector reads it back.
	query, args := buildVectorAggregateQuery(sqldialect.For(sqldialect.SQLite), "vector", req)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError("vector_aggregate", fmt.Errorf("failed to query embeddings: %w", err))
	}
	defer func() { _ = rows.Close() }()

	var fetched []vectorAggregateRow
	for rows.Next() {
		var (
			id          string
			vectorBytes []byte
			metadata    []byte
		)
		if err := rows.Scan(&id, &vectorBytes, &metadata); err != nil {
			return nil, wrapError("vector_aggregate", fmt.Errorf("failed to scan row: %w", err))
		}
		// A row that will not decode is fatal here, unlike in the search and
		// index paths that skip it. Those return a ranking, which one missing
		// candidate only makes slightly worse; this returns a mean, which one
		// silently dropped member makes quietly wrong.
		vec, err := encoding.DecodeVector(vectorBytes)
		if err != nil {
			return nil, wrapError("vector_aggregate", fmt.Errorf("failed to decode vector of %q: %w", id, err))
		}
		fetched = append(fetched, vectorAggregateRow{
			id:     id,
			vector: vec,
			meta:   decodeAggregateMetadata(metadata),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError("vector_aggregate", fmt.Errorf("error iterating rows: %w", err))
	}

	resp, err := runVectorAggregate(req, fetched, s.GetSimilarityFunc())
	if err != nil {
		return nil, wrapError("vector_aggregate", err)
	}
	return resp, nil
}

// --- request plumbing --------------------------------------------------------

// vectorAggregateRow is one fetched record, in the only three parts the maths
// and the grouping need. Both backends produce this; nothing below here knows
// which database it came from.
type vectorAggregateRow struct {
	id     string
	vector []float32
	meta   map[string]string
}

// vectorAggregateGroupInput is a group after selection and capping, before any
// arithmetic. ids and vecs are parallel, both in ascending id order.
type vectorAggregateGroupInput struct {
	key  string
	ids  []string
	vecs [][]float32
}

func validateVectorAggregateRequest(req VectorAggregateRequest) error {
	switch req.Kind {
	case VectorCentroid, VectorGeometricMedian, VectorMedoid:
	case "":
		return fmt.Errorf("vector aggregate kind is required")
	default:
		return fmt.Errorf("unsupported vector aggregate kind: %s", req.Kind)
	}
	if req.MaxPerGroup < 0 {
		return fmt.Errorf("max per group cannot be negative: %d", req.MaxPerGroup)
	}
	return nil
}

// buildVectorAggregateQuery writes the one SELECT both backends run.
//
// The only thing the two disagree about that matters here is how a vector
// column is read back — a BLOB on SQLite, `vector::text` on pgvector — so that
// is the one parameter. Everything else goes through sqldialect: the metadata
// read, which is json_extract on one side and ->> on the other, and the
// placeholders, which Rebind numbers for PostgreSQL.
//
// Filter keys are visited in sorted order so the bound arguments line up with
// the numbered placeholders in a fixed sequence — a map range would build a
// different (still correct) statement every call, which makes the query
// unloggable and untestable for no gain.
func buildVectorAggregateQuery(d sqldialect.Dialect, vectorExpr string, req VectorAggregateRequest) (string, []interface{}) {
	query := "SELECT id, " + vectorExpr + ", metadata FROM embeddings WHERE 1=1"
	args := []interface{}{}

	if req.Collection != "" {
		query += " AND collection_id = (SELECT id FROM collections WHERE name = ?)"
		args = append(args, req.Collection)
	}

	keys := make([]string, 0, len(req.Filter))
	for k := range req.Filter {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		query += " AND " + d.JSONTextGuarded("metadata", k) + " = ?"
		args = append(args, req.Filter[k])
	}

	// The real ordering guarantee is applied in Go below; this only keeps the
	// scan itself stable, which makes a query plan and a log line reproducible.
	query += " ORDER BY id"

	return d.Rebind(query), args
}

// decodeAggregateMetadata reads the metadata sidecar the way every other read
// path in this package does: a column that will not decode costs its own value
// and nothing else. A row with unreadable metadata still has a usable vector,
// and for an ungrouped, unfiltered aggregate it is a perfectly good member.
func decodeAggregateMetadata(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// groupVectorAggregateRows turns fetched rows into the groups the maths runs
// over, in the order they will be returned.
//
// Three orderings are pinned here, all for the same reason — a result that
// depends on what the database happened to hand back first is not a result:
//
//   - members within a group are sorted by id, so MaxPerGroup takes a defined
//     subset rather than an arbitrary one;
//   - groups are sorted by key;
//   - sorting is by raw byte order rather than the database's collation, which
//     differs between SQLite and a locale-configured PostgreSQL, so the same
//     data gives the same capped subset on both.
//
// A row whose metadata has no value for GroupBy joins no group. Folding those
// into the empty-string key would invent a group the metadata never asserted,
// and it would be indistinguishable from the ungrouped case's own "" key.
func groupVectorAggregateRows(rows []vectorAggregateRow, req VectorAggregateRequest) []vectorAggregateGroupInput {
	buckets := map[string][]vectorAggregateRow{}
	for _, r := range rows {
		key := ""
		if req.GroupBy != "" {
			v, ok := r.meta[req.GroupBy]
			if !ok {
				continue
			}
			key = v
		}
		buckets[key] = append(buckets[key], r)
	}

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	groups := make([]vectorAggregateGroupInput, 0, len(keys))
	for _, k := range keys {
		members := buckets[k]
		sort.Slice(members, func(i, j int) bool { return members[i].id < members[j].id })
		if req.MaxPerGroup > 0 && len(members) > req.MaxPerGroup {
			members = members[:req.MaxPerGroup]
		}

		g := vectorAggregateGroupInput{
			key:  k,
			ids:  make([]string, len(members)),
			vecs: make([][]float32, len(members)),
		}
		for i, m := range members {
			g.ids[i] = m.id
			g.vecs[i] = m.vector
		}
		groups = append(groups, g)
	}
	return groups
}

// runVectorAggregate is the whole backend-independent half: group, reduce,
// answer. Both store methods end here.
func runVectorAggregate(req VectorAggregateRequest, rows []vectorAggregateRow, sim SimilarityFunc) (*VectorAggregateResponse, error) {
	if sim == nil {
		// A store built by hand rather than through the constructors can have
		// no similarity function; the default everywhere else is cosine.
		sim = CosineSimilarity
	}

	// No matches is an empty answer, not a failure: "there are no vectors
	// under this filter" is a fact about the data, and a caller looping over
	// filters should not have to special-case it.
	groups := make([]VectorAggregateGroup, 0, 4)

	for _, g := range groupVectorAggregateRows(rows, req) {
		if len(g.vecs) == 0 {
			continue
		}

		out := VectorAggregateGroup{Group: g.key, Count: len(g.vecs)}
		switch req.Kind {
		case VectorCentroid:
			v, err := centroidOf(g.vecs)
			if err != nil {
				return nil, fmt.Errorf("group %q: %w", g.key, err)
			}
			out.Vector = v
		case VectorGeometricMedian:
			v, err := geometricMedianOf(g.vecs)
			if err != nil {
				return nil, fmt.Errorf("group %q: %w", g.key, err)
			}
			out.Vector = v
		case VectorMedoid:
			idx, err := medoidOf(g.vecs)
			if err != nil {
				return nil, fmt.Errorf("group %q: %w", g.key, err)
			}
			out.MemberID = g.ids[idx]
			out.Score = medoidScore(g.vecs, idx, sim)
		default:
			return nil, fmt.Errorf("unsupported vector aggregate kind: %s", req.Kind)
		}
		groups = append(groups, out)
	}

	return &VectorAggregateResponse{Request: req, Groups: groups}, nil
}

// medoidScore reports how well the medoid stands for its group.
//
// The distances that *chose* the medoid are Euclidean, because that is what
// the medoid is defined against, but the number reported here is computed with
// the store's configured similarityFn. Every other score this codebase hands a
// caller means "similarity under this store's metric", and a score that
// silently meant something else in this one place would be compared against
// those and quietly mis-read.
//
// A single-member group has no "rest of the group" to average over, so the
// answer is the member's similarity to itself. That is the maximum the metric
// can produce (1 for cosine, 0 for negative Euclidean distance), which is the
// honest reading: a group of one is perfectly represented by its one member.
// Reporting 0 instead would rank a perfect singleton below a mediocre cluster
// under cosine, and above one under Euclidean — wrong in opposite directions
// depending on the metric, which is the worst kind of wrong.
func medoidScore(vecs [][]float32, idx int, sim SimilarityFunc) float64 {
	if len(vecs) == 1 {
		return sim(vecs[idx], vecs[idx])
	}
	var total float64
	for i, v := range vecs {
		if i == idx {
			continue
		}
		total += sim(vecs[idx], v)
	}
	return total / float64(len(vecs)-1)
}

// --- the maths ---------------------------------------------------------------
//
// Pure functions over [][]float32, shared by every backend. Distances are
// Euclidean throughout: the geometric median and the medoid are both defined
// as minimisers of a sum of Euclidean distances, and substituting another
// metric would not make them a different flavour of the same thing, it would
// make them not those quantities.

// uniformDim returns the shared dimension of a non-empty set, or an error.
//
// A mismatch is an error rather than a skip. The alternative — drop the odd
// one out — turns "your store has vectors from two different models in it"
// into a centroid that is subtly not the centroid of what the caller asked
// for, and nothing anywhere says so.
func uniformDim(vecs [][]float32) (int, error) {
	if len(vecs) == 0 {
		return 0, fmt.Errorf("no vectors to aggregate")
	}
	dim := len(vecs[0])
	if dim == 0 {
		return 0, fmt.Errorf("cannot aggregate zero-dimensional vectors")
	}
	for i, v := range vecs[1:] {
		if len(v) != dim {
			return 0, fmt.Errorf("dimension mismatch: vector 0 has %d dimensions, vector %d has %d", dim, i+1, len(v))
		}
	}
	return dim, nil
}

// centroidOf returns the componentwise mean.
//
// Accumulated in float64 even though the inputs and the result are float32: a
// group of a few thousand 1024-dimensional vectors sums to numbers where
// float32's 24-bit mantissa starts losing the small addends, and the cost of
// the wider accumulator is nothing.
func centroidOf(vecs [][]float32) ([]float32, error) {
	dim, err := uniformDim(vecs)
	if err != nil {
		return nil, err
	}

	sums := make([]float64, dim)
	for _, v := range vecs {
		for i, x := range v {
			sums[i] += float64(x)
		}
	}

	out := make([]float32, dim)
	n := float64(len(vecs))
	for i, s := range sums {
		out[i] = float32(s / n)
	}
	return out, nil
}

// geometricMedianOf returns the point minimising the sum of Euclidean
// distances to the inputs, by Weiszfeld's algorithm.
//
// The plain iteration is a distance-weighted average, y ← Σ(xᵢ/dᵢ) / Σ(1/dᵢ),
// and it breaks the moment an iterate lands on an input point, where dᵢ is
// zero. The fix used here is the Vardi–Zhang step: the coincident points are
// pulled out of the average, and the step towards the average of the rest is
// damped by γ = min(1, η/R), where η is how many inputs the iterate sits on
// and R is the length of the summed unit pull of the others. When that pull is
// weaker than the mass sitting under the iterate, γ is 1 and the iterate stays
// put — which is correct, because that is exactly the condition for an input
// point to be the median. Simply returning the coincident point instead, which
// is the shortcut this is often written with, gets the common case right and
// the case where the median has moved past a duplicate wrong.
//
// Started from the centroid: it is free, it is already the answer when the set
// is symmetric, and it is inside the convex hull, where the median also is.
func geometricMedianOf(vecs [][]float32) ([]float32, error) {
	dim, err := uniformDim(vecs)
	if err != nil {
		return nil, err
	}

	start, err := centroidOf(vecs)
	if err != nil {
		return nil, err
	}
	y := make([]float64, dim)
	for i, x := range start {
		y[i] = float64(x)
	}

	weighted := make([]float64, dim)
	next := make([]float64, dim)

	for iter := 0; iter < weiszfeldMaxIterations; iter++ {
		for i := range weighted {
			weighted[i] = 0
		}
		var invSum float64
		var coincident float64

		for _, v := range vecs {
			d := distanceFloat64To32(y, v)
			if d <= weiszfeldCoincidenceEpsilon {
				coincident++
				continue
			}
			inv := 1 / d
			invSum += inv
			for i, x := range v {
				weighted[i] += float64(x) * inv
			}
		}

		// Every input sits on the iterate: the set is a single repeated point
		// and that point is the median.
		if invSum == 0 {
			break
		}

		// T(y), the distance-weighted average of the non-coincident points.
		for i := range next {
			next[i] = weighted[i] / invSum
		}

		if coincident > 0 {
			// R is the norm of Σ(xᵢ − y)/dᵢ over the non-coincident points,
			// which is invSum·(T(y) − y) — the same sum, already computed.
			var r float64
			for i := range next {
				diff := (next[i] - y[i]) * invSum
				r += diff * diff
			}
			r = math.Sqrt(r)
			if r == 0 {
				// No pull at all away from the point we are sitting on.
				break
			}
			gamma := coincident / r
			if gamma > 1 {
				gamma = 1
			}
			for i := range next {
				next[i] = (1-gamma)*next[i] + gamma*y[i]
			}
		}

		var moved float64
		for i := range next {
			d := next[i] - y[i]
			moved += d * d
		}
		copy(y, next)
		if math.Sqrt(moved) < weiszfeldEpsilon {
			break
		}
	}

	out := make([]float32, dim)
	for i, v := range y {
		out[i] = float32(v)
	}
	return out, nil
}

// medoidOf returns the index of the input vector minimising the sum of
// Euclidean distances to all the others.
//
// O(n²) in distance computations, which is the definition and not an
// implementation shortcut — there is no cheaper exact medoid. MaxPerGroup on
// the request is the lever for a group large enough to care.
//
// Ties go to the lowest index. Callers see members in ascending id order, so
// that reads as "the lowest id wins", which is a rule someone can rely on
// rather than a coin flip between two equally central duplicates.
func medoidOf(vecs [][]float32) (int, error) {
	if _, err := uniformDim(vecs); err != nil {
		return 0, err
	}
	if len(vecs) == 1 {
		return 0, nil
	}

	sums := make([]float64, len(vecs))
	for i := 0; i < len(vecs); i++ {
		for j := i + 1; j < len(vecs); j++ {
			d := distance32(vecs[i], vecs[j])
			sums[i] += d
			sums[j] += d
		}
	}

	best := 0
	for i, s := range sums {
		if s < sums[best] {
			best = i
		}
	}
	return best, nil
}

// distance32 is the Euclidean distance between two equal-length float32
// vectors, accumulated in float64. Callers have already checked the lengths
// through uniformDim.
func distance32(a, b []float32) float64 {
	var sum float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return math.Sqrt(sum)
}

// distanceFloat64To32 is the same distance with the running estimate on the
// left, which Weiszfeld carries in float64 so the iteration is not quantised
// by the width of the stored vectors.
func distanceFloat64To32(a []float64, b []float32) float64 {
	var sum float64
	for i := range a {
		d := a[i] - float64(b[i])
		sum += d * d
	}
	return math.Sqrt(sum)
}
