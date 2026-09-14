package core

// Range search, asserted once and run twice.
//
// RangeSearch was in the Store interface and implemented on both backends and
// still returned opposite things: SQLite put the distance in Score and sorted
// ascending, PostgreSQL put a similarity there, sorted descending, and capped
// the result set at a thousand rows without saying so. Two implementations, no
// test that ran against both, and no caller — so the same interface call meant
// two different things for as long as nobody looked.
//
// The assertions below are written once and handed each backend in turn. That
// is the point: a divergence has to fail, and a suite that was copied and
// pasted per backend can be repaired on one side alone by whoever is in a
// hurry.

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/liliang-cn/cortexdb/v2/internal/pgtest"
	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// parityBackend is the surface these two files exercise.
//
// Store rather than a bare list of methods, so the suite can seed through the
// same API a caller would — but Aggregate, SearchWithFacets and
// BatchRangeSearch are spelled out because they are not in Store yet. That is
// the other half of what was wrong: a capability reachable only by narrowing
// to *SQLiteStore is one cortexdb.DB cannot call at all, since it holds its
// store as an interface.
type parityBackend interface {
	Store
	Aggregate(ctx context.Context, req AggregationRequest) (*AggregationResponse, error)
	SearchWithFacets(ctx context.Context, query []float32, opts FacetedSearchOptions) ([]ScoredEmbedding, []FacetResult, error)
	BatchRangeSearch(ctx context.Context, queries [][]float32, radius float32, opts SearchOptions) ([][]ScoredEmbedding, error)
}

// runOnBothStores runs one test body against SQLite and against PostgreSQL,
// each with a store of its own.
//
// The PostgreSQL subtest exists whether or not the DSN does. When it does not,
// it skips saying exactly which methods went unchecked — a suite that quietly
// contains one fewer test reports green for a backend it never opened, and the
// person reading the output has no way to tell the difference.
func runOnBothStores(t *testing.T, dim int, body func(t *testing.T, s parityBackend)) {
	t.Helper()

	t.Run("sqlite", func(t *testing.T) {
		body(t, newParitySQLite(t, dim))
	})

	t.Run("postgres", func(t *testing.T) {
		if strings.TrimSpace(os.Getenv(pgtest.EnvDSN)) == "" {
			t.Skip(pgtest.EnvDSN + " unset — RangeSearch, BatchRangeSearch, Aggregate and " +
				"SearchWithFacets on *PostgresStore are NOT covered by this run, so a " +
				"divergence from the SQLite store would go unreported")
		}
		body(t, newParityPostgres(t, dim))
	})
}

func newParitySQLite(t *testing.T, dim int) parityBackend {
	t.Helper()
	path := fmt.Sprintf("test_parity_range_%d.db", testname.Nano())
	cfg := DefaultConfig()
	cfg.Path = path
	cfg.VectorDim = dim
	// Cosine on both sides, and no ANN index: pgvector's <=> is cosine and
	// nothing here is measuring index recall, only what the numbers mean.
	cfg.SimilarityFn = CosineSimilarity
	cfg.HNSW.Enabled = false

	store, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("sqlite init: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = os.Remove(path)
	})
	return store
}

func newParityPostgres(t *testing.T, dim int) parityBackend {
	t.Helper()
	db := pgtest.Open(t, "core_range_parity")
	if db == nil {
		t.Fatalf("pgtest returned no database with %s set", pgtest.EnvDSN)
	}
	cfg := DefaultConfig()
	cfg.VectorDim = dim
	cfg.SimilarityFn = CosineSimilarity
	store := NewPostgresStore(db, cfg)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("postgres init: %v", err)
	}
	return store
}

// scoreTolerance is wide enough for the two engines to disagree in the last
// bits and narrow enough that a similarity could never be mistaken for a
// distance. SQLite sums in float64 over float32 components; pgvector sums in
// its own order over float4.
const scoreTolerance = 1e-5

// rangeParitySeed spans the cases the conversion has to get right: an exact
// match, two ordinary neighbours, an orthogonal vector, and two whose cosine
// similarity is negative — the half of the range the old sign-based conversion
// folded back on top of the near half.
var rangeParitySeed = []*Embedding{
	{ID: "identical", Vector: []float32{1, 0, 0, 0}, Content: "identical"},
	{ID: "near", Vector: []float32{1, 0.25, 0, 0}, Content: "near"},
	{ID: "mid", Vector: []float32{1, 1, 0, 0}, Content: "mid"},
	{ID: "orthogonal", Vector: []float32{0, 1, 0, 0}, Content: "orthogonal"},
	{ID: "anti", Vector: []float32{-1, 2, 0, 0}, Content: "anti-correlated"},
	{ID: "opposite", Vector: []float32{-1, 0, 0, 0}, Content: "opposite"},
}

// TestRangeSearchAgreesOnScoreAndOrder is the test that would have caught the
// original defect on either backend.
//
// Same vectors, same radius, and the expected numbers computed here rather
// than copied from whatever a backend happened to return: Score must be the
// cosine similarity, the order must descend, and a vector identical to the
// query must score ~1 rather than ~0. SQLite returned the distance and sorted
// ascending, so it failed the score check on the first row and the order check
// on the last; PostgreSQL turned the radius into a similarity threshold, so it
// dropped every row whose similarity was below 1 - radius.
func TestRangeSearchAgreesOnScoreAndOrder(t *testing.T) {
	query := []float32{1, 0, 0, 0}
	const radius = float32(0.3)

	// What the decided semantics say the answer is: keep a vector when
	// rangeDistance of its similarity is within the radius, and rank by the
	// similarity itself.
	type want struct {
		id    string
		score float64
	}
	var expected []want
	self := CosineSimilarity(query, query)
	for _, e := range rangeParitySeed {
		score := CosineSimilarity(query, e.Vector)
		if rangeDistance(self, score) <= float64(radius) {
			expected = append(expected, want{e.ID, score})
		}
	}
	for i := 1; i < len(expected); i++ {
		if expected[i].score > expected[i-1].score {
			t.Fatalf("the seed's expectations are not in descending order — fix the seed, not the store")
		}
	}

	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		if err := s.UpsertBatch(ctx, rangeParitySeed); err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}

		got, err := s.RangeSearch(ctx, query, radius, SearchOptions{})
		if err != nil {
			t.Fatalf("RangeSearch: %v", err)
		}

		if len(got) != len(expected) {
			t.Fatalf("got %d results %v, want %d %v", len(got), scoredIDs(got), len(expected), expected)
		}
		for i, w := range expected {
			if got[i].ID != w.id {
				t.Fatalf("result %d is %q, want %q — full order %v", i, got[i].ID, w.id, scoredIDs(got))
			}
			if math.Abs(got[i].Score-w.score) > scoreTolerance {
				t.Errorf("%s scored %.9f, want the cosine similarity %.9f", w.id, got[i].Score, w.score)
			}
		}

		// Said separately because it is the whole defect in one line: an exact
		// match is the highest score, not the lowest. Score is a similarity
		// here for the same reason it is one everywhere else — the rerankers
		// and the RRF fusion in pkg/cortexdb sort it descending, and a
		// distance would have ranked the nearest vectors last.
		if got[0].ID != "identical" || got[0].Score < 0.99 {
			t.Errorf("nearest result is %s scoring %.6f; Score is a distance, not a similarity",
				got[0].ID, got[0].Score)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Score > got[i-1].Score {
				t.Errorf("scores do not descend: %s %.6f follows %s %.6f",
					got[i].ID, got[i].Score, got[i-1].ID, got[i-1].Score)
			}
		}

		// Content and metadata come back on both, not just the id and score.
		if got[0].Content != "identical" {
			t.Errorf("content = %q, want %q", got[0].Content, "identical")
		}
	})
}

// TestRangeDistanceUsesTheMetricsFixedPoint pins the conversion itself, with
// no store in the way.
//
// The numbers in the "cosine" rows are the ones the old sign-based conversion
// got wrong: it returned 0 for an orthogonal pair — ranked as identical — and
// 0.5 for a similarity of -0.5, folding the whole negative half of cosine back
// on top of the near half.
func TestRangeDistanceUsesTheMetricsFixedPoint(t *testing.T) {
	cases := []struct {
		name string
		self float64
		// score is what the metric returned for the candidate.
		score float64
		want  float64
	}{
		{"cosine, identical", 1, 1, 0},
		{"cosine, similar", 1, 0.75, 0.25},
		{"cosine, orthogonal", 1, 0, 1},
		{"cosine, anti-correlated", 1, -0.5, 1.5},
		{"cosine, opposite", 1, -1, 2},
		// EuclideanDist returns the negated distance, so its fixed point is 0
		// and the conversion is the negation it always was.
		{"euclidean, identical", 0, 0, 0},
		{"euclidean, three away", 0, -3, 3},
		// DotProduct's fixed point is the query's squared norm.
		{"dot product", 4, 1.5, 2.5},
	}
	for _, c := range cases {
		if got := rangeDistance(c.self, c.score); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("%s: rangeDistance(%v, %v) = %v, want %v", c.name, c.self, c.score, got, c.want)
		}
	}
}

// TestRangeSearchDoesNotAdmitAntiCorrelatedVectors is the regression the
// fixed-point conversion exists for.
//
// Under the old sign test, a cosine similarity of -0.447 converted to a
// distance of 0.447 and an orthogonal pair converted to 0, so both sat inside
// a radius of 0.5 alongside genuinely near vectors — anti-correlated results
// returned as near ones, with no error to notice. The expectations here are
// written out rather than derived from rangeDistance, so the test says what
// the answer is instead of agreeing with whatever the conversion currently
// does.
func TestRangeSearchDoesNotAdmitAntiCorrelatedVectors(t *testing.T) {
	query := []float32{1, 0, 0, 0}

	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		if err := s.UpsertBatch(ctx, rangeParitySeed); err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}

		// cosine similarities against [1,0,0,0]: identical 1, near 0.970,
		// mid 0.707, orthogonal 0, anti -0.447, opposite -1. With the fixed
		// point at 1 the distances are 0, 0.030, 0.293, 1, 1.447 and 2, so a
		// radius of 0.5 reaches the first three and nothing else.
		near, err := s.RangeSearch(ctx, query, 0.5, SearchOptions{})
		if err != nil {
			t.Fatalf("RangeSearch: %v", err)
		}
		if !sameIDs(near, []string{"identical", "near", "mid"}) {
			t.Errorf("radius 0.5 returned %v, want identical, near and mid — anything "+
				"anti-correlated in there is being scored as close", scoredIDs(near))
		}

		// Orthogonal sits at distance 1 exactly, so it arrives only once the
		// radius passes 1 — and it arrives scoring 0, not 1.
		wide, err := s.RangeSearch(ctx, query, 1.5, SearchOptions{})
		if err != nil {
			t.Fatalf("RangeSearch wide: %v", err)
		}
		if !sameIDs(wide, []string{"identical", "near", "mid", "orthogonal", "anti"}) {
			t.Errorf("radius 1.5 returned %v, want everything but the opposite vector", scoredIDs(wide))
		}
		for _, r := range wide {
			if r.ID != "orthogonal" {
				continue
			}
			if math.Abs(r.Score) > scoreTolerance {
				t.Errorf("orthogonal scored %.9f, want ~0", r.Score)
			}
		}

		// The opposite vector is two away and needs a radius to match.
		everything, err := s.RangeSearch(ctx, query, 2.5, SearchOptions{})
		if err != nil {
			t.Fatalf("RangeSearch everything: %v", err)
		}
		if len(everything) != len(rangeParitySeed) {
			t.Errorf("radius 2.5 returned %d of %d vectors", len(everything), len(rangeParitySeed))
		}
	})
}

// TestRangeSearchReturnsEveryMatchNotTheFirstThousand pins the truncation.
//
// PostgreSQL's RangeSearch defaulted TopK to 1000 and delegated to Search, so a
// query matching more than that returned a thousand rows and no error. There
// is nothing in the result to tell a caller the other two hundred existed.
func TestRangeSearchReturnsEveryMatchNotTheFirstThousand(t *testing.T) {
	const total = 1200
	query := []float32{1, 0, 0, 0}
	const radius = float32(0.05)

	seed := make([]*Embedding, 0, total)
	for i := 0; i < total; i++ {
		// A fan of vectors close to the query: the widest is 0.12 off axis, a
		// cosine distance of about 0.007, so every one of them is inside the
		// radius and the count is the only thing under test.
		seed = append(seed, &Embedding{
			ID:      fmt.Sprintf("v%04d", i),
			Vector:  []float32{1, float32(i) / 10000, 0, 0},
			Content: fmt.Sprintf("vector %d", i),
		})
	}

	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		if err := s.UpsertBatch(ctx, seed); err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}

		all, err := s.RangeSearch(ctx, query, radius, SearchOptions{})
		if err != nil {
			t.Fatalf("RangeSearch: %v", err)
		}
		if len(all) != total {
			t.Errorf("TopK 0 returned %d of %d matches — a range search that caps itself "+
				"silently is indistinguishable from a corpus that stops there", len(all), total)
		}

		// And TopK still caps when the caller asks for one, taking the best.
		capped, err := s.RangeSearch(ctx, query, radius, SearchOptions{TopK: 500})
		if err != nil {
			t.Fatalf("RangeSearch TopK: %v", err)
		}
		if len(capped) != 500 {
			t.Fatalf("TopK 500 returned %d results", len(capped))
		}
		if len(all) >= 500 && math.Abs(capped[499].Score-all[499].Score) > scoreTolerance {
			t.Errorf("TopK 500 kept a different 500: last score %.9f, want %.9f",
				capped[499].Score, all[499].Score)
		}
	})
}

// TestRangeSearchRefusesANonPositiveRadius: SQLite always has, and PostgreSQL
// used to turn a radius of 0 into a similarity threshold of 1 and a negative
// one into a threshold that matched everything.
func TestRangeSearchRefusesANonPositiveRadius(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		if err := s.UpsertBatch(ctx, rangeParitySeed); err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}
		for _, radius := range []float32{0, -1} {
			got, err := s.RangeSearch(ctx, []float32{1, 0, 0, 0}, radius, SearchOptions{})
			if err == nil {
				t.Errorf("radius %v was accepted and returned %d results", radius, len(got))
				continue
			}
			if !strings.Contains(err.Error(), "radius must be positive") {
				t.Errorf("radius %v failed with %q, want the same message both backends give", radius, err)
			}
		}
	})
}

// TestBatchRangeSearchKeepsEachResultWithItsQuery.
//
// The outer index is the only thing tying a result back to the query that
// produced it, so it has to be the input index — including for a query that
// matches nothing, which must be an empty slot rather than a missing one.
func TestBatchRangeSearchKeepsEachResultWithItsQuery(t *testing.T) {
	seed := []*Embedding{
		{ID: "x", Vector: []float32{1, 0, 0, 0}, Content: "x axis"},
		{ID: "y", Vector: []float32{0, 1, 0, 0}, Content: "y axis"},
		{ID: "z", Vector: []float32{0, 0, 1, 0}, Content: "z axis"},
	}
	queries := [][]float32{
		{1, 0, 0, 0},
		{0, 0, 1, 0},
		{0, 1, 0, 0},
	}
	wantFirst := []string{"x", "z", "y"}
	const radius = float32(0.1)

	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		if err := s.UpsertBatch(ctx, seed); err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}

		got, err := s.BatchRangeSearch(ctx, queries, radius, SearchOptions{})
		if err != nil {
			t.Fatalf("BatchRangeSearch: %v", err)
		}
		if len(got) != len(queries) {
			t.Fatalf("got %d result groups for %d queries", len(got), len(queries))
		}
		for i, want := range wantFirst {
			if len(got[i]) == 0 {
				t.Errorf("query %d matched nothing; its own vector is in the corpus", i)
				continue
			}
			if got[i][0].ID != want {
				t.Errorf("query %d's nearest is %q, want %q — the groups are shuffled",
					i, got[i][0].ID, want)
			}
			for j := 1; j < len(got[i]); j++ {
				if got[i][j].Score > got[i][j-1].Score {
					t.Errorf("query %d's group does not descend by score", i)
				}
			}
		}

		// No queries is an empty batch, not a nil one and not an error.
		empty, err := s.BatchRangeSearch(ctx, nil, radius, SearchOptions{})
		if err != nil {
			t.Fatalf("BatchRangeSearch(nil): %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("BatchRangeSearch(nil) returned %d groups", len(empty))
		}
	})
}

func scoredIDs(results []ScoredEmbedding) []string {
	ids := make([]string, len(results))
	for i := range results {
		ids[i] = fmt.Sprintf("%s=%.4f", results[i].ID, results[i].Score)
	}
	return ids
}
