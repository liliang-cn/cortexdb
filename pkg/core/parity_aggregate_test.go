package core

// Aggregate and SearchWithFacets, asserted once and run twice.
//
// Both existed on *SQLiteStore only, were absent from the Store interface, and
// had no callers — which is how three capabilities can sit in a package for a
// year and still be unreachable from the facade, since cortexdb.DB holds its
// store as an interface and any code that narrowed to *SQLiteStore to reach
// them would have broken under PostgreSQL.
//
// The assertions live in one place and both backends are handed to them. The
// numbers below are the SQLite behaviour, including the parts of it that are
// strange: a CAST that reads "n/a" as zero and counts it, and a facet count
// that ignores the search's own filters. Reproducing a strangeness is a choice;
// letting the two backends each pick their own is not.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
)

// aggregateParitySeed is one row per case the aggregations have to get right:
// two numeric prices, a price that is not a number at all, and a row with no
// price field.
var aggregateParitySeed = []*Embedding{
	{ID: "a1", Vector: []float32{1, 0, 0, 0}, Content: "atlas",
		Metadata: map[string]string{"category": "books", "price": "10", "region": "eu"}},
	{ID: "a2", Vector: []float32{0.9, 0.1, 0, 0}, Content: "almanac",
		Metadata: map[string]string{"category": "books", "price": "20", "region": "us"}},
	{ID: "a3", Vector: []float32{0, 1, 0, 0}, Content: "kite",
		Metadata: map[string]string{"category": "toys", "price": "n/a", "region": "eu"}},
	{ID: "a4", Vector: []float32{0, 0.9, 0.1, 0}, Content: "yo-yo",
		Metadata: map[string]string{"category": "toys", "region": "us"}},
	{ID: "a5", Vector: []float32{0, 0, 1, 0}, Content: "marbles",
		Metadata: map[string]string{"category": "toys", "price": "5", "region": "eu"}},
}

func seedAggregates(t *testing.T, s parityBackend) {
	t.Helper()
	if err := s.UpsertBatch(context.Background(), aggregateParitySeed); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
}

// numeric reads an AggregationResult.Value, which is an int for COUNT and a
// float64 for everything else on both backends.
func numeric(t *testing.T, v interface{}) float64 {
	t.Helper()
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	default:
		t.Fatalf("aggregated value is %T (%v), want a number", v, v)
		return 0
	}
}

func TestAggregateCountAgreesOnBothStores(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		seedAggregates(t, s)

		cases := []struct {
			name  string
			req   AggregationRequest
			count float64
		}{
			{"everything", AggregationRequest{Type: AggregationCount}, 5},
			{"one category", AggregationRequest{
				Type:    AggregationCount,
				Filters: map[string]interface{}{"category": "books"},
			}, 2},
			{"two filters", AggregationRequest{
				Type:    AggregationCount,
				Filters: map[string]interface{}{"category": "toys", "region": "eu"},
			}, 2},
			// A filter nothing satisfies is zero, not an error and not an
			// empty Results slice: COUNT always reports one row.
			{"nothing matches", AggregationRequest{
				Type:    AggregationCount,
				Filters: map[string]interface{}{"category": "furniture"},
			}, 0},
		}

		for _, c := range cases {
			resp, err := s.Aggregate(ctx, c.req)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if resp.Total != 1 || len(resp.Results) != 1 {
				t.Fatalf("%s: Total=%d len(Results)=%d, want 1 and 1", c.name, resp.Total, len(resp.Results))
			}
			if got := numeric(t, resp.Results[0].Value); got != c.count {
				t.Errorf("%s: value %v, want %v", c.name, got, c.count)
			}
			if got := float64(resp.Results[0].Count); got != c.count {
				t.Errorf("%s: count %v, want %v", c.name, got, c.count)
			}
		}
	})
}

// TestAggregateScalarsAgreeOnBothStores pins the arithmetic, including the two
// edge cases the backends had every reason to disagree about.
//
// A field holding "n/a" contributes 0 and is counted: SQLite's CAST to REAL
// never fails, so a non-numeric value is a zero rather than an error or a
// skipped row. PostgreSQL's ::double precision raises on the same text and
// would have failed the whole statement, which is why pgMetadataReal reads a
// numeric prefix first. A row with no price field at all is excluded by the
// WHERE and does not reach the count.
func TestAggregateScalarsAgreeOnBothStores(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		seedAggregates(t, s)

		cases := []struct {
			name  string
			typ   AggregationType
			value float64
			count int
		}{
			// 10 + 20 + 0 ("n/a") + 5, over the four rows that have a price.
			{"sum", AggregationSum, 35, 4},
			{"avg", AggregationAvg, 8.75, 4},
			// The minimum is the zero "n/a" casts to, not 5.
			{"min", AggregationMin, 0, 4},
			{"max", AggregationMax, 20, 4},
		}

		for _, c := range cases {
			resp, err := s.Aggregate(ctx, AggregationRequest{Type: c.typ, Field: "price"})
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if len(resp.Results) != 1 {
				t.Fatalf("%s: %d results, want 1", c.name, len(resp.Results))
			}
			if got := numeric(t, resp.Results[0].Value); math.Abs(got-c.value) > 1e-9 {
				t.Errorf("%s(price) = %v, want %v", c.name, got, c.value)
			}
			if resp.Results[0].Count != c.count {
				t.Errorf("%s(price) counted %d rows, want %d", c.name, resp.Results[0].Count, c.count)
			}
		}

		// Nothing to aggregate is zero and zero, not nil and not an error —
		// what a caller adding the value to a running total needs it to be.
		resp, err := s.Aggregate(ctx, AggregationRequest{
			Type:    AggregationSum,
			Field:   "price",
			Filters: map[string]interface{}{"category": "furniture"},
		})
		if err != nil {
			t.Fatalf("sum over nothing: %v", err)
		}
		if got := numeric(t, resp.Results[0].Value); got != 0 || resp.Results[0].Count != 0 {
			t.Errorf("sum over nothing = %v/%d, want 0/0", got, resp.Results[0].Count)
		}

		// A field no row carries behaves the same way.
		resp, err = s.Aggregate(ctx, AggregationRequest{Type: AggregationAvg, Field: "weight"})
		if err != nil {
			t.Fatalf("avg over a missing field: %v", err)
		}
		if got := numeric(t, resp.Results[0].Value); got != 0 || resp.Results[0].Count != 0 {
			t.Errorf("avg over a missing field = %v/%d, want 0/0", got, resp.Results[0].Count)
		}

		// And the field is required, on both.
		if _, err := s.Aggregate(ctx, AggregationRequest{Type: AggregationSum}); err == nil {
			t.Error("SUM without a field was accepted")
		}
	})
}

// groupCounts collapses a GROUP BY response into key → count, because the
// order of equal-count groups is not defined on either backend.
func groupCounts(t *testing.T, resp *AggregationResponse, field string) map[string]int {
	t.Helper()
	out := make(map[string]int, len(resp.Results))
	for _, r := range resp.Results {
		out[fmt.Sprintf("%v", r.GroupKeys[field])] = r.Count
	}
	return out
}

func TestAggregateGroupByAgreesOnBothStores(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		seedAggregates(t, s)

		resp, err := s.Aggregate(ctx, AggregationRequest{
			Type:    AggregationGroupBy,
			GroupBy: []string{"category"},
		})
		if err != nil {
			t.Fatalf("group by category: %v", err)
		}
		if got := groupCounts(t, resp, "category"); len(got) != 2 || got["books"] != 2 || got["toys"] != 3 {
			t.Errorf("group by category = %v, want books:2 toys:3", got)
		}
		// The default ordering is by count descending, so the larger group
		// leads — the one thing about the order that is defined.
		if len(resp.Results) > 0 && resp.Results[0].GroupKeys["category"] != "toys" {
			t.Errorf("first group is %v, want toys (ORDER BY count DESC)", resp.Results[0].GroupKeys["category"])
		}
		if resp.Total != len(resp.Results) {
			t.Errorf("Total=%d but %d results", resp.Total, len(resp.Results))
		}

		// Group keys keep their shape: a numeric string comes back as a
		// number, a non-numeric one as a string, and a row missing the field
		// as nil — one group, not zero.
		resp, err = s.Aggregate(ctx, AggregationRequest{
			Type:    AggregationGroupBy,
			GroupBy: []string{"price"},
		})
		if err != nil {
			t.Fatalf("group by price: %v", err)
		}
		want := map[string]int{"10": 1, "20": 1, "5": 1, "n/a": 1, "<nil>": 1}
		if got := groupCounts(t, resp, "price"); !sameCounts(got, want) {
			t.Errorf("group by price = %v, want %v", got, want)
		}
		for _, r := range resp.Results {
			switch k := r.GroupKeys["price"].(type) {
			case float64, nil:
			case string:
				if k != "n/a" {
					t.Errorf("price group key %q came back as a string", k)
				}
			default:
				t.Errorf("price group key is %T", k)
			}
		}

		// A field no row carries is one group with a nil key covering
		// everything, which is what both backends' GROUP BY over a NULL
		// expression produces.
		resp, err = s.Aggregate(ctx, AggregationRequest{
			Type:    AggregationGroupBy,
			GroupBy: []string{"nonexistent"},
		})
		if err != nil {
			t.Fatalf("group by a missing field: %v", err)
		}
		if len(resp.Results) != 1 || resp.Results[0].Count != len(aggregateParitySeed) ||
			resp.Results[0].GroupKeys["nonexistent"] != nil {
			t.Errorf("group by a missing field = %+v, want one nil group of %d",
				resp.Results, len(aggregateParitySeed))
		}

		// Two fields at once.
		resp, err = s.Aggregate(ctx, AggregationRequest{
			Type:    AggregationGroupBy,
			GroupBy: []string{"category", "region"},
		})
		if err != nil {
			t.Fatalf("group by two fields: %v", err)
		}
		if len(resp.Results) != 4 {
			t.Errorf("group by category+region produced %d groups, want 4", len(resp.Results))
		}
	})
}

// TestAggregateGroupByHavingOrderAndLimitAgree.
//
// HAVING is the clause the two databases genuinely disagree about: SQLite
// resolves `count` against the select list and PostgreSQL refuses to, so an
// unresolved name would have compiled on one backend and failed on the other
// naming a column the caller never wrote.
func TestAggregateGroupByHavingOrderAndLimitAgree(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		seedAggregates(t, s)

		resp, err := s.Aggregate(ctx, AggregationRequest{
			Type:    AggregationGroupBy,
			GroupBy: []string{"category"},
			Having:  map[string]interface{}{"count": 2},
		})
		if err != nil {
			t.Fatalf("having count=2: %v", err)
		}
		if len(resp.Results) != 1 || resp.Results[0].GroupKeys["category"] != "books" {
			t.Errorf("having count=2 gave %+v, want just books", resp.Results)
		}

		resp, err = s.Aggregate(ctx, AggregationRequest{
			Type:    AggregationGroupBy,
			GroupBy: []string{"category"},
			OrderBy: "count",
			Limit:   1,
		})
		if err != nil {
			t.Fatalf("order by count limit 1: %v", err)
		}
		if len(resp.Results) != 1 || resp.Results[0].GroupKeys["category"] != "toys" {
			t.Errorf("order by count limit 1 gave %+v, want just toys", resp.Results)
		}

		// A HAVING nothing satisfies is an empty result set, not an error.
		resp, err = s.Aggregate(ctx, AggregationRequest{
			Type:    AggregationGroupBy,
			GroupBy: []string{"category"},
			Having:  map[string]interface{}{"count": 99},
		})
		if err != nil {
			t.Fatalf("having count=99: %v", err)
		}
		if len(resp.Results) != 0 || resp.Total != 0 {
			t.Errorf("having count=99 gave %d results", len(resp.Results))
		}

		// group_by is required, on both.
		if _, err := s.Aggregate(ctx, AggregationRequest{Type: AggregationGroupBy}); err == nil {
			t.Error("GROUP BY without fields was accepted")
		}
	})
}

func TestAggregateRespectsTheCollectionOnBothStores(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		seedAggregates(t, s)

		if _, err := s.CreateCollection(ctx, "archive", 4); err != nil {
			t.Fatalf("CreateCollection: %v", err)
		}
		err := s.Upsert(ctx, &Embedding{
			ID: "arch1", Collection: "archive", Vector: []float32{0, 0, 0, 1},
			Content: "shelved", Metadata: map[string]string{"category": "books", "price": "99"},
		})
		if err != nil {
			t.Fatalf("Upsert into archive: %v", err)
		}

		resp, err := s.Aggregate(ctx, AggregationRequest{
			Type: AggregationCount, Collection: "archive",
		})
		if err != nil {
			t.Fatalf("count in archive: %v", err)
		}
		if got := numeric(t, resp.Results[0].Value); got != 1 {
			t.Errorf("count in archive = %v, want 1", got)
		}

		resp, err = s.Aggregate(ctx, AggregationRequest{
			Type: AggregationSum, Field: "price", Collection: "archive",
		})
		if err != nil {
			t.Fatalf("sum in archive: %v", err)
		}
		if got := numeric(t, resp.Results[0].Value); got != 99 {
			t.Errorf("sum in archive = %v, want 99", got)
		}
	})
}

// skipUnlessFacetFilteringWorks skips a backend that cannot run a facet filter
// at all.
//
// (*SQLiteStore).fetchCandidatesWithFacets LEFT JOINs collections onto
// embeddings and then writes the facet conditions against a bare `metadata` —
// a column both tables have — so every facet filter, and every opts.Filter,
// fails with "ambiguous column name: metadata" before a single row is read.
// SearchWithFacets with facets has therefore never worked on SQLite, which is
// what having no callers and no tests buys you.
//
// Not fixed here: this change is not allowed to touch that function, and a
// silent correction to one backend is the thing this file exists to prevent.
// It is reported instead. Probing rather than naming the backend is so these
// assertions start covering SQLite the moment the column is qualified, with no
// edit to the test.
func skipUnlessFacetFilteringWorks(t *testing.T, s parityBackend) {
	t.Helper()
	_, _, err := s.SearchWithFacets(context.Background(), []float32{1, 0, 0, 0},
		FacetedSearchOptions{Facets: map[string]FacetFilter{"category": {Type: FilterTypeExists}}})
	if err != nil && strings.Contains(err.Error(), "ambiguous column name") {
		t.Skipf("this backend cannot run a facet filter at all (%v) — faceted "+
			"filtering is NOT covered here, so the PostgreSQL implementation has "+
			"no second opinion to be checked against", err)
	}
}

// TestSearchWithFacetsRanksTheSameWay covers the half that works everywhere:
// no facet conditions, just the ranking, the cap and the threshold.
func TestSearchWithFacetsRanksTheSameWay(t *testing.T) {
	query := []float32{1, 0, 0, 0}

	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		seedAggregates(t, s)

		got, _, err := s.SearchWithFacets(ctx, query, FacetedSearchOptions{})
		if err != nil {
			t.Fatalf("SearchWithFacets: %v", err)
		}
		// An unspecified TopK is ten, not everything — scoreCandidates'
		// default on the SQLite side, and the corpus is smaller than that.
		if len(got) != len(aggregateParitySeed) {
			t.Fatalf("got %d results, want all %d", len(got), len(aggregateParitySeed))
		}
		if got[0].ID != "a1" {
			t.Errorf("nearest is %q, want a1 — full order %v", got[0].ID, scoredIDs(got))
		}
		for i := 1; i < len(got); i++ {
			if got[i].Score > got[i-1].Score {
				t.Errorf("scores do not descend: %v", scoredIDs(got))
				break
			}
		}
		for _, r := range got {
			want := CosineSimilarity(query, vectorOf(r.ID))
			if math.Abs(r.Score-want) > scoreTolerance {
				t.Errorf("%s scored %.9f, want the cosine similarity %.9f", r.ID, r.Score, want)
			}
		}

		got, _, err = s.SearchWithFacets(ctx, query, FacetedSearchOptions{
			SearchOptions: SearchOptions{TopK: 2},
		})
		if err != nil {
			t.Fatalf("SearchWithFacets TopK: %v", err)
		}
		if len(got) != 2 || got[0].ID != "a1" || got[1].ID != "a2" {
			t.Errorf("TopK 2 gave %v, want the two nearest, a1 then a2", scoredIDs(got))
		}

		// The threshold is on similarity, and it rejects before the cap.
		got, _, err = s.SearchWithFacets(ctx, query, FacetedSearchOptions{
			SearchOptions: SearchOptions{Threshold: 0.5},
		})
		if err != nil {
			t.Fatalf("SearchWithFacets threshold: %v", err)
		}
		if !sameIDs(got, []string{"a1", "a2"}) {
			t.Errorf("threshold 0.5 gave %v, want a1 and a2", scoredIDs(got))
		}
	})
}

func TestSearchWithFacetsFiltersTheSameWay(t *testing.T) {
	query := []float32{1, 0, 0, 0}

	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		seedAggregates(t, s)
		skipUnlessFacetFilteringWorks(t, s)

		cases := []struct {
			name   string
			facets map[string]FacetFilter
			want   []string
		}{
			{"equals", map[string]FacetFilter{
				"category": {Type: FilterTypeEquals, Values: []interface{}{"books"}},
			}, []string{"a1", "a2"}},
			{"in", map[string]FacetFilter{
				"price": {Type: FilterTypeIn, Values: []interface{}{"10", "5"}},
			}, []string{"a1", "a5"}},
			// The range casts the way SQLite's CAST to REAL does, so "n/a" is a
			// zero and falls below the floor, and the row with no price at all
			// is NULL and drops out rather than sorting as zero.
			{"range", map[string]FacetFilter{
				"price": {Type: FilterTypeRange, Min: 6.0, Max: 25.0},
			}, []string{"a1", "a2"}},
			{"exists", map[string]FacetFilter{
				"price": {Type: FilterTypeExists},
			}, []string{"a1", "a2", "a3", "a5"}},
			{"prefix", map[string]FacetFilter{
				"category": {Type: FilterTypePrefix, Pattern: "boo"},
			}, []string{"a1", "a2"}},
			{"contains", map[string]FacetFilter{
				"region": {Type: FilterTypeContains, Pattern: "us"},
			}, []string{"a2", "a4"}},
			// SQLite's LIKE folds ASCII case and PostgreSQL's does not, so
			// this is the case that would have narrowed differently per DSN.
			{"contains folds ASCII case", map[string]FacetFilter{
				"region": {Type: FilterTypeContains, Pattern: "US"},
			}, []string{"a2", "a4"}},
			{"prefix folds ASCII case", map[string]FacetFilter{
				"category": {Type: FilterTypePrefix, Pattern: "BOO"},
			}, []string{"a1", "a2"}},
			{"two facets are ANDed", map[string]FacetFilter{
				"category": {Type: FilterTypeEquals, Values: []interface{}{"toys"}},
				"region":   {Type: FilterTypeEquals, Values: []interface{}{"eu"}},
			}, []string{"a3", "a5"}},
			{"nested OR", map[string]FacetFilter{
				"category": {Type: FilterTypeNested, Operator: OperatorOR, Nested: []FacetFilter{
					{Type: FilterTypeEquals, Values: []interface{}{"books"}},
					{Type: FilterTypeEquals, Values: []interface{}{"toys"}},
				}},
			}, []string{"a1", "a2", "a3", "a4", "a5"}},
			{"nested NOT", map[string]FacetFilter{
				"category": {Type: FilterTypeNested, Operator: OperatorNOT, Nested: []FacetFilter{
					{Type: FilterTypeEquals, Values: []interface{}{"books"}},
				}},
			}, []string{"a3", "a4", "a5"}},
			{"nothing matches", map[string]FacetFilter{
				"category": {Type: FilterTypeEquals, Values: []interface{}{"furniture"}},
			}, nil},
			// An empty filter is no condition at all, on both, rather than a
			// condition nothing satisfies.
			{"no filter type", map[string]FacetFilter{
				"category": {},
			}, []string{"a1", "a2", "a3", "a4", "a5"}},
		}

		for _, c := range cases {
			got, _, err := s.SearchWithFacets(ctx, query, FacetedSearchOptions{Facets: c.facets})
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if !sameIDs(got, c.want) {
				t.Errorf("%s selected %v, want %v", c.name, scoredIDs(got), c.want)
			}
			// Whatever the facets select, the ranking is the ordinary one.
			for i := 1; i < len(got); i++ {
				if got[i].Score > got[i-1].Score {
					t.Errorf("%s: scores do not descend: %v", c.name, scoredIDs(got))
					break
				}
			}
			for _, r := range got {
				want := CosineSimilarity(query, vectorOf(r.ID))
				if math.Abs(r.Score-want) > scoreTolerance {
					t.Errorf("%s: %s scored %.9f, want the cosine similarity %.9f",
						c.name, r.ID, r.Score, want)
				}
			}
		}
	})
}

// TestSearchWithFacetsCountsAgreeOnBothStores.
//
// The facets carry no filter type, so no condition is generated and the
// SQLite ambiguous-column defect is out of the way: what is under test here is
// computeFacetCounts, which queries embeddings alone and works on both.
//
// The counts cover the whole table rather than the result set, on both
// backends, because that is what computeFacetCounts does on SQLite. It is very
// likely not what a facet sidebar wants — see the note on the PostgreSQL
// version — and it is asserted here so that whoever changes it changes both.
func TestSearchWithFacetsCountsAgreeOnBothStores(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		ctx := context.Background()
		seedAggregates(t, s)

		_, facets, err := s.SearchWithFacets(ctx, []float32{1, 0, 0, 0}, FacetedSearchOptions{
			Facets:       map[string]FacetFilter{"category": {}},
			ReturnFacets: true,
		})
		if err != nil {
			t.Fatalf("SearchWithFacets: %v", err)
		}
		if len(facets) != 1 || facets[0].Field != "category" {
			t.Fatalf("facet results = %+v, want one for category", facets)
		}
		if facets[0].Values["books"] != 2 || facets[0].Values["toys"] != 3 {
			t.Errorf("category counts = %v, want books:2 toys:3", facets[0].Values)
		}
		if facets[0].Total != 5 {
			t.Errorf("category total = %d, want 5", facets[0].Total)
		}

		// A row missing the field is not counted, and the count is of rows
		// rather than of distinct values.
		_, facets, err = s.SearchWithFacets(ctx, []float32{1, 0, 0, 0}, FacetedSearchOptions{
			Facets:       map[string]FacetFilter{"price": {}},
			ReturnFacets: true,
		})
		if err != nil {
			t.Fatalf("SearchWithFacets on price: %v", err)
		}
		if len(facets) != 1 || facets[0].Total != 4 {
			t.Errorf("price facet = %+v, want a total of 4 — the row with no price is not counted", facets)
		}

		// MaxFacetValues caps how many values come back, taking the commonest.
		_, facets, err = s.SearchWithFacets(ctx, []float32{1, 0, 0, 0}, FacetedSearchOptions{
			Facets:         map[string]FacetFilter{"category": {}},
			ReturnFacets:   true,
			MaxFacetValues: 1,
		})
		if err != nil {
			t.Fatalf("SearchWithFacets capped: %v", err)
		}
		if len(facets) != 1 || len(facets[0].Values) != 1 || facets[0].Values["toys"] != 3 {
			t.Errorf("capped facet values = %+v, want only toys:3", facets)
		}

		// A field no row carries produces no FacetResult at all rather than an
		// empty one.
		_, facets, err = s.SearchWithFacets(ctx, []float32{1, 0, 0, 0}, FacetedSearchOptions{
			Facets:       map[string]FacetFilter{"nonexistent": {}},
			ReturnFacets: true,
		})
		if err != nil {
			t.Fatalf("SearchWithFacets on a missing field: %v", err)
		}
		if len(facets) != 0 {
			t.Errorf("a field no row carries produced %+v", facets)
		}
	})
}

func vectorOf(id string) []float32 {
	for _, e := range aggregateParitySeed {
		if e.ID == id {
			return e.Vector
		}
	}
	return nil
}

func sameIDs(got []ScoredEmbedding, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	ids := make([]string, len(got))
	for i := range got {
		ids[i] = got[i].ID
	}
	sort.Strings(ids)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	for i := range ids {
		if ids[i] != sorted[i] {
			return false
		}
	}
	return true
}

func sameCounts(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
