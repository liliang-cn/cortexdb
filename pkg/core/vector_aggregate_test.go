package core

// Tests for aggregating the vectors themselves.
//
// Split in two on purpose. The maths is pure and gets tested directly, because
// that is where the interesting behaviour lives and a bug there is a wrong
// number rather than a failed query. The store methods get tested for the
// things only they can get wrong: which rows are selected, which group they
// land in, and whether the answer is the same one twice running.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// --- the maths ---------------------------------------------------------------

// The test this feature exists for.
//
// A tight cluster plus one distant member: the centroid is dragged most of the
// way to the outlier, and the geometric median does not move at all. If these
// two ever agree on this input, one of them is not what it claims to be.
func TestGeometricMedianIgnoresTheOutlierThatCapturesTheCentroid(t *testing.T) {
	// Nine points whose own mean is exactly the origin, so any drift in the
	// result is attributable to the tenth and to nothing else.
	vecs := [][]float32{
		{0, 0},
		{0.1, 0}, {-0.1, 0}, {0, 0.1}, {0, -0.1},
		{0.05, 0.05}, {-0.05, -0.05}, {0.05, -0.05}, {-0.05, 0.05},
		{100, 0}, // the outlier
	}

	centroid, err := centroidOf(vecs)
	if err != nil {
		t.Fatalf("centroidOf: %v", err)
	}
	median, err := geometricMedianOf(vecs)
	if err != nil {
		t.Fatalf("geometricMedianOf: %v", err)
	}

	origin := []float32{0, 0}
	centroidDrift := distance32(centroid, origin)
	medianDrift := distance32(median, origin)

	// One tenth of the way to a point 100 units away.
	if math.Abs(centroidDrift-10) > 1e-4 {
		t.Fatalf("centroid should sit 10 units from the cluster, sits %v (%v)", centroidDrift, centroid)
	}
	// The cluster holds a strict majority and its pull cancels, so the median
	// stays on the origin exactly.
	if medianDrift > 1e-4 {
		t.Fatalf("geometric median should stay on the cluster, drifted %v (%v)", medianDrift, median)
	}
	if centroidDrift <= 100*medianDrift+1 {
		t.Fatalf("the two aggregations did not visibly diverge: centroid %v, median %v", centroidDrift, medianDrift)
	}
}

// The degenerate case the Weiszfeld guard exists for: the answer is an input
// point, so the iteration has to survive landing on it rather than dividing by
// a zero distance. Three collinear points put the centroid on the middle one
// at the very first step.
func TestGeometricMedianSurvivesLandingOnAnInputPoint(t *testing.T) {
	vecs := [][]float32{{0, 0}, {10, 0}, {20, 0}}

	median, err := geometricMedianOf(vecs)
	if err != nil {
		t.Fatalf("geometricMedianOf: %v", err)
	}
	for _, x := range median {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			t.Fatalf("the coincidence guard did not hold: %v", median)
		}
	}
	if d := distance32(median, []float32{10, 0}); d > 1e-5 {
		t.Fatalf("median of three collinear points should be the middle one, got %v (%v away)", median, d)
	}
}

// The other half of the guard: duplicated mass at a point outweighs a single
// distant pull, so the iteration has to converge onto that point and stop
// there rather than oscillate around it.
func TestGeometricMedianConvergesOntoDuplicatedMass(t *testing.T) {
	vecs := [][]float32{{0, 0}, {0, 0}, {0, 0}, {9, 0}}

	median, err := geometricMedianOf(vecs)
	if err != nil {
		t.Fatalf("geometricMedianOf: %v", err)
	}
	if d := distance32(median, []float32{0, 0}); d > 1e-4 {
		t.Fatalf("three coincident points should hold the median, it sits %v away (%v)", d, median)
	}
}

func TestMedoidPicksTheMemberClosestToTheRest(t *testing.T) {
	// c is the interior of the little square; e is far away and drags nothing,
	// because the medoid is chosen among the members rather than between them.
	vecs := [][]float32{
		{0, 0}, // a
		{1, 0}, // b
		{1, 1}, // c
		{0, 1}, // d
		{9, 9}, // e
	}
	idx, err := medoidOf(vecs)
	if err != nil {
		t.Fatalf("medoidOf: %v", err)
	}
	if idx != 2 {
		t.Fatalf("medoid should be index 2, got %d", idx)
	}
}

// Ties go to the lowest index, which callers see as "the lowest id wins".
// Two points are exactly as central as each other; the answer must still be
// the same one every time.
func TestMedoidBreaksTiesTowardsTheLowestIndex(t *testing.T) {
	vecs := [][]float32{{0, 0}, {1, 0}}
	for i := 0; i < 16; i++ {
		idx, err := medoidOf(vecs)
		if err != nil {
			t.Fatalf("medoidOf: %v", err)
		}
		if idx != 0 {
			t.Fatalf("tie should resolve to index 0, got %d on run %d", idx, i)
		}
	}
}

func TestVectorMathsRejectsDimensionMismatch(t *testing.T) {
	mixed := [][]float32{{1, 2, 3}, {1, 2}}

	if _, err := centroidOf(mixed); err == nil {
		t.Fatal("centroidOf accepted vectors of different widths")
	}
	if _, err := geometricMedianOf(mixed); err == nil {
		t.Fatal("geometricMedianOf accepted vectors of different widths")
	}
	if _, err := medoidOf(mixed); err == nil {
		t.Fatal("medoidOf accepted vectors of different widths")
	}
}

// A mismatch inside a group has to surface as an error from the request, not
// just from the maths — a store holding vectors from two models is exactly how
// this happens in practice, and silently dropping the odd one out would give a
// centroid of something the caller never asked for.
func TestRunVectorAggregateReportsDimensionMismatchInAGroup(t *testing.T) {
	rows := []vectorAggregateRow{
		{id: "a", vector: []float32{1, 0, 0}},
		{id: "b", vector: []float32{0, 1}},
	}
	_, err := runVectorAggregate(VectorAggregateRequest{Kind: VectorCentroid}, rows, CosineSimilarity)
	if err == nil {
		t.Fatal("expected a dimension mismatch error")
	}
	if !strings.Contains(err.Error(), "dimension mismatch") {
		t.Fatalf("error should name the mismatch, got %v", err)
	}
}

// --- the SQLite store --------------------------------------------------------

func newVectorAggregateStore(t *testing.T) *SQLiteStore {
	t.Helper()

	config := DefaultConfig()
	config.Path = filepath.Join(t.TempDir(), "vector_aggregate.db")
	config.VectorDim = 3

	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return store
}

func seedVectorAggregate(t *testing.T, store *SQLiteStore, embs ...*Embedding) {
	t.Helper()
	for _, e := range embs {
		if e.Content == "" {
			e.Content = e.ID
		}
		if err := store.Upsert(context.Background(), e); err != nil {
			t.Fatalf("Upsert(%s): %v", e.ID, err)
		}
	}
}

func TestSQLiteVectorAggregateCentroidAndMedian(t *testing.T) {
	store := newVectorAggregateStore(t)
	ctx := context.Background()

	// The same shape as the pure test, in the three dimensions the store is
	// configured for: a cluster around the origin and one distant member.
	seedVectorAggregate(t, store,
		&Embedding{ID: "c1", Vector: []float32{0, 0, 0}},
		&Embedding{ID: "c2", Vector: []float32{0.1, 0, 0}},
		&Embedding{ID: "c3", Vector: []float32{-0.1, 0, 0}},
		&Embedding{ID: "c4", Vector: []float32{0, 0.1, 0}},
		&Embedding{ID: "c5", Vector: []float32{0, -0.1, 0}},
		&Embedding{ID: "far", Vector: []float32{60, 0, 0}},
	)

	centroid, err := store.VectorAggregate(ctx, VectorAggregateRequest{Kind: VectorCentroid})
	if err != nil {
		t.Fatalf("centroid: %v", err)
	}
	if len(centroid.Groups) != 1 {
		t.Fatalf("expected one ungrouped result, got %d", len(centroid.Groups))
	}
	g := centroid.Groups[0]
	if g.Group != "" || g.Count != 6 || g.MemberID != "" {
		t.Fatalf("unexpected ungrouped centroid group: %+v", g)
	}
	if want := float32(10); math.Abs(float64(g.Vector[0]-want)) > 1e-4 {
		t.Fatalf("centroid x should be %v, got %v", want, g.Vector[0])
	}

	median, err := store.VectorAggregate(ctx, VectorAggregateRequest{Kind: VectorGeometricMedian})
	if err != nil {
		t.Fatalf("geometric median: %v", err)
	}
	if d := distance32(median.Groups[0].Vector, []float32{0, 0, 0}); d > 1e-3 {
		t.Fatalf("geometric median should stay in the cluster, sits %v away (%v)", d, median.Groups[0].Vector)
	}
}

// The medoid is the one kind that answers with a record rather than a
// synthetic point, which is the whole reason it is here.
func TestSQLiteVectorAggregateMedoidReturnsARealRecord(t *testing.T) {
	store := newVectorAggregateStore(t)
	ctx := context.Background()

	seedVectorAggregate(t, store,
		&Embedding{ID: "a", Vector: []float32{0, 0, 0}},
		&Embedding{ID: "b", Vector: []float32{1, 0, 0}},
		&Embedding{ID: "c", Vector: []float32{1, 1, 0}},
		&Embedding{ID: "d", Vector: []float32{0, 1, 0}},
		&Embedding{ID: "e", Vector: []float32{9, 9, 0}},
	)

	resp, err := store.VectorAggregate(ctx, VectorAggregateRequest{Kind: VectorMedoid})
	if err != nil {
		t.Fatalf("medoid: %v", err)
	}
	g := resp.Groups[0]
	if g.MemberID != "c" {
		t.Fatalf("medoid should be c, got %q", g.MemberID)
	}
	if g.Vector != nil {
		t.Fatalf("medoid must not invent a vector, got %v", g.Vector)
	}
	if g.Count != 5 {
		t.Fatalf("count should be 5, got %d", g.Count)
	}
	// The store is configured for cosine, so the score is a mean cosine
	// similarity and has to land inside its range.
	if g.Score < -1 || g.Score > 1 {
		t.Fatalf("score %v is outside the configured metric's range", g.Score)
	}

	// And it names a row that is actually there.
	if _, err := store.GetByID(ctx, g.MemberID); err != nil {
		t.Fatalf("medoid named a record that does not exist: %v", err)
	}
}

// A group of one is trivially its own centroid, median and medoid, and the
// medoid's score is the best the metric can produce rather than zero.
func TestSQLiteVectorAggregateSingleMemberGroup(t *testing.T) {
	store := newVectorAggregateStore(t)
	ctx := context.Background()

	only := []float32{0.3, 0.4, 0}
	seedVectorAggregate(t, store, &Embedding{ID: "solo", Vector: only})

	for _, kind := range []VectorAggregateKind{VectorCentroid, VectorGeometricMedian} {
		resp, err := store.VectorAggregate(ctx, VectorAggregateRequest{Kind: kind})
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if d := distance32(resp.Groups[0].Vector, only); d > 1e-6 {
			t.Fatalf("%s of one vector should be that vector, got %v", kind, resp.Groups[0].Vector)
		}
	}

	resp, err := store.VectorAggregate(ctx, VectorAggregateRequest{Kind: VectorMedoid})
	if err != nil {
		t.Fatalf("medoid: %v", err)
	}
	if resp.Groups[0].MemberID != "solo" {
		t.Fatalf("medoid of one vector should be that vector's id, got %q", resp.Groups[0].MemberID)
	}
	// Cosine similarity of a vector with itself: the maximum the metric gives.
	if math.Abs(resp.Groups[0].Score-1) > 1e-6 {
		t.Fatalf("single-member medoid score should be self-similarity (1 under cosine), got %v", resp.Groups[0].Score)
	}
}

func TestSQLiteVectorAggregateNoMatchesIsEmptyNotAnError(t *testing.T) {
	store := newVectorAggregateStore(t)
	ctx := context.Background()

	seedVectorAggregate(t, store, &Embedding{ID: "a", Vector: []float32{1, 0, 0}})

	resp, err := store.VectorAggregate(ctx, VectorAggregateRequest{
		Kind:   VectorCentroid,
		Filter: map[string]string{"topic": "nothing here"},
	})
	if err != nil {
		t.Fatalf("a filter that matches nothing should not be an error: %v", err)
	}
	if len(resp.Groups) != 0 {
		t.Fatalf("expected no groups, got %+v", resp.Groups)
	}
}

// Groups come back sorted by key, and rows with no value for the grouping
// field belong to no group at all.
func TestSQLiteVectorAggregateGroupsAreOrderedAndSelective(t *testing.T) {
	store := newVectorAggregateStore(t)
	ctx := context.Background()

	seedVectorAggregate(t, store,
		&Embedding{ID: "z1", Vector: []float32{1, 0, 0}, Metadata: map[string]string{"topic": "zeta"}},
		&Embedding{ID: "a1", Vector: []float32{0, 1, 0}, Metadata: map[string]string{"topic": "alpha"}},
		&Embedding{ID: "a2", Vector: []float32{0, 3, 0}, Metadata: map[string]string{"topic": "alpha"}},
		&Embedding{ID: "m1", Vector: []float32{0, 0, 1}, Metadata: map[string]string{"topic": "mu"}},
		&Embedding{ID: "none", Vector: []float32{5, 5, 5}, Metadata: map[string]string{"other": "x"}},
	)

	resp, err := store.VectorAggregate(ctx, VectorAggregateRequest{
		Kind:    VectorCentroid,
		GroupBy: "topic",
	})
	if err != nil {
		t.Fatalf("grouped centroid: %v", err)
	}

	var got []string
	for _, g := range resp.Groups {
		got = append(got, fmt.Sprintf("%s:%d", g.Group, g.Count))
	}
	want := []string{"alpha:2", "mu:1", "zeta:1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("groups should be %v, got %v", want, got)
	}

	// alpha's two members average to (0, 2, 0); the ungrouped "none" row is
	// nowhere in the answer.
	if d := distance32(resp.Groups[0].Vector, []float32{0, 2, 0}); d > 1e-6 {
		t.Fatalf("alpha centroid should be (0,2,0), got %v", resp.Groups[0].Vector)
	}
}

// MaxPerGroup has to take a defined subset. The rows are inserted in an order
// that is not their id order, so a cap that followed the database's own
// ordering would be visible here.
func TestSQLiteVectorAggregateMaxPerGroupIsDeterministic(t *testing.T) {
	store := newVectorAggregateStore(t)
	ctx := context.Background()

	seedVectorAggregate(t, store,
		&Embedding{ID: "d", Vector: []float32{40, 0, 0}},
		&Embedding{ID: "b", Vector: []float32{20, 0, 0}},
		&Embedding{ID: "a", Vector: []float32{10, 0, 0}},
		&Embedding{ID: "c", Vector: []float32{30, 0, 0}},
	)

	// Ascending id order is a, b, c, d — so the first two are 10 and 20, and
	// their centroid is 15 no matter how the rows came off disk.
	for i := 0; i < 8; i++ {
		resp, err := store.VectorAggregate(ctx, VectorAggregateRequest{
			Kind:        VectorCentroid,
			MaxPerGroup: 2,
		})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if resp.Groups[0].Count != 2 {
			t.Fatalf("run %d: cap should leave 2 members, got %d", i, resp.Groups[0].Count)
		}
		if math.Abs(float64(resp.Groups[0].Vector[0])-15) > 1e-5 {
			t.Fatalf("run %d: capped centroid should be 15, got %v", i, resp.Groups[0].Vector[0])
		}
	}
}

func TestSQLiteVectorAggregateHonoursCollectionAndFilter(t *testing.T) {
	store := newVectorAggregateStore(t)
	ctx := context.Background()

	if _, err := store.CreateCollection(ctx, "notes", 3); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	seedVectorAggregate(t, store,
		&Embedding{ID: "n1", Collection: "notes", Vector: []float32{2, 0, 0}, Metadata: map[string]string{"lang": "en"}},
		&Embedding{ID: "n2", Collection: "notes", Vector: []float32{4, 0, 0}, Metadata: map[string]string{"lang": "en"}},
		&Embedding{ID: "n3", Collection: "notes", Vector: []float32{99, 0, 0}, Metadata: map[string]string{"lang": "zh"}},
		&Embedding{ID: "d1", Vector: []float32{99, 0, 0}, Metadata: map[string]string{"lang": "en"}},
	)

	resp, err := store.VectorAggregate(ctx, VectorAggregateRequest{
		Kind:       VectorCentroid,
		Collection: "notes",
		Filter:     map[string]string{"lang": "en"},
	})
	if err != nil {
		t.Fatalf("filtered centroid: %v", err)
	}
	if len(resp.Groups) != 1 || resp.Groups[0].Count != 2 {
		t.Fatalf("filter should have left n1 and n2, got %+v", resp.Groups)
	}
	if math.Abs(float64(resp.Groups[0].Vector[0])-3) > 1e-5 {
		t.Fatalf("centroid of (2,0,0) and (4,0,0) should be 3, got %v", resp.Groups[0].Vector[0])
	}
}

func TestSQLiteVectorAggregateRejectsAnUnknownKind(t *testing.T) {
	store := newVectorAggregateStore(t)

	_, err := store.VectorAggregate(context.Background(), VectorAggregateRequest{Kind: "median"})
	if err == nil {
		t.Fatal("expected an error for an unsupported kind")
	}
	var storeErr *StoreError
	if !errors.As(err, &storeErr) {
		t.Fatalf("error should be a StoreError, got %T: %v", err, err)
	}
}

// --- the PostgreSQL store ----------------------------------------------------

// The schema these tests own, so they cannot collide with the other suites
// running against the same database.
const pgVectorAggregateTestSchema = "cortexdb_vecagg_test"

// openPGVectorAggregateStore returns a PostgresStore on a schema of its own,
// or skips loudly. A quiet skip would let a green run claim this backend was
// covered when none of it ran.
func openPGVectorAggregateStore(t *testing.T) *PostgresStore {
	t.Helper()

	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset — PostgresStore.VectorAggregate is NOT covered by this run: " +
			"neither the centroid, the geometric median nor the medoid was exercised against pgvector, " +
			"and the PostgreSQL metadata filter and grouping paths were not run at all")
	}

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open (admin): %v", err)
	}
	defer admin.Close()

	ctx := context.Background()
	if _, err := admin.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+pgVectorAggregateTestSchema+` CASCADE`); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+pgVectorAggregateTestSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	// The extension is database-wide; creating it from the scoped connection
	// would put it in the test schema and lose it on the drop.
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatalf("create extension: %v", err)
	}

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", dsn+sep+"search_path="+pgVectorAggregateTestSchema)
	if err != nil {
		t.Fatalf("open (scoped): %v", err)
	}
	if _, err := db.ExecContext(ctx, `SET search_path TO `+pgVectorAggregateTestSchema+`, public`); err != nil {
		t.Fatalf("search_path: %v", err)
	}

	cfg := DefaultConfig()
	cfg.VectorDim = 3
	store := NewPostgresStore(db, cfg)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		cleanup, err := sql.Open("pgx", dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		if _, err := cleanup.Exec(`DROP SCHEMA IF EXISTS ` + pgVectorAggregateTestSchema + ` CASCADE`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	return store
}

// The same assertions the SQLite suite makes, against pgvector. The maths is
// shared, so what is really under test is the fetch: the vector literal, the
// jsonb metadata read, and the numbered placeholders.
func TestPostgresVectorAggregate(t *testing.T) {
	store := openPGVectorAggregateStore(t)
	ctx := context.Background()

	embs := []*Embedding{
		{ID: "c1", Vector: []float32{0, 0, 0}, Content: "c1", Metadata: map[string]string{"topic": "alpha"}},
		{ID: "c2", Vector: []float32{0.1, 0, 0}, Content: "c2", Metadata: map[string]string{"topic": "alpha"}},
		{ID: "c3", Vector: []float32{-0.1, 0, 0}, Content: "c3", Metadata: map[string]string{"topic": "alpha"}},
		{ID: "c4", Vector: []float32{0, 0.1, 0}, Content: "c4", Metadata: map[string]string{"topic": "alpha"}},
		{ID: "c5", Vector: []float32{0, -0.1, 0}, Content: "c5", Metadata: map[string]string{"topic": "alpha"}},
		{ID: "far", Vector: []float32{60, 0, 0}, Content: "far", Metadata: map[string]string{"topic": "alpha"}},
		{ID: "z1", Vector: []float32{7, 7, 7}, Content: "z1", Metadata: map[string]string{"topic": "zeta"}},
	}
	if err := store.UpsertBatch(ctx, embs); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	centroid, err := store.VectorAggregate(ctx, VectorAggregateRequest{
		Kind:   VectorCentroid,
		Filter: map[string]string{"topic": "alpha"},
	})
	if err != nil {
		t.Fatalf("centroid: %v", err)
	}
	if len(centroid.Groups) != 1 || centroid.Groups[0].Count != 6 {
		t.Fatalf("the jsonb filter should have left six rows, got %+v", centroid.Groups)
	}
	if math.Abs(float64(centroid.Groups[0].Vector[0])-10) > 1e-4 {
		t.Fatalf("centroid x should be 10, got %v", centroid.Groups[0].Vector[0])
	}

	median, err := store.VectorAggregate(ctx, VectorAggregateRequest{
		Kind:   VectorGeometricMedian,
		Filter: map[string]string{"topic": "alpha"},
	})
	if err != nil {
		t.Fatalf("geometric median: %v", err)
	}
	if d := distance32(median.Groups[0].Vector, []float32{0, 0, 0}); d > 1e-3 {
		t.Fatalf("geometric median should stay in the cluster, sits %v away (%v)", d, median.Groups[0].Vector)
	}

	grouped, err := store.VectorAggregate(ctx, VectorAggregateRequest{
		Kind:    VectorMedoid,
		GroupBy: "topic",
	})
	if err != nil {
		t.Fatalf("grouped medoid: %v", err)
	}
	if len(grouped.Groups) != 2 {
		t.Fatalf("expected alpha and zeta, got %+v", grouped.Groups)
	}
	if grouped.Groups[0].Group != "alpha" || grouped.Groups[1].Group != "zeta" {
		t.Fatalf("groups should be sorted by key, got %q then %q",
			grouped.Groups[0].Group, grouped.Groups[1].Group)
	}
	if grouped.Groups[0].Vector != nil {
		t.Fatalf("medoid must not invent a vector, got %v", grouped.Groups[0].Vector)
	}
	if grouped.Groups[1].MemberID != "z1" {
		t.Fatalf("the one-member group's medoid should be z1, got %q", grouped.Groups[1].MemberID)
	}
	if math.Abs(grouped.Groups[1].Score-1) > 1e-6 {
		t.Fatalf("single-member medoid score should be self-similarity (1 under cosine), got %v",
			grouped.Groups[1].Score)
	}

	// The medoid of the cluster-plus-outlier group has to be one of the five
	// cluster members, never the outlier.
	if grouped.Groups[0].MemberID == "far" {
		t.Fatal("the outlier cannot be the medoid of a group that contains the cluster")
	}

	// And a filter that matches nothing is an empty answer, not a failure.
	empty, err := store.VectorAggregate(ctx, VectorAggregateRequest{
		Kind:   VectorCentroid,
		Filter: map[string]string{"topic": "nothing here"},
	})
	if err != nil {
		t.Fatalf("empty filter: %v", err)
	}
	if len(empty.Groups) != 0 {
		t.Fatalf("expected no groups, got %+v", empty.Groups)
	}
}
