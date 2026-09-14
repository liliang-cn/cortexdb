package core

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// injectionFixture is three rows under two kinds, so a filter that is working
// returns two and a filter that has been turned into a tautology returns three.
func injectionFixture(t *testing.T) *SQLiteStore {
	t.Helper()
	path := fmt.Sprintf("/tmp/aggregation_identifier_%d.db", testname.Nano())
	t.Cleanup(func() { _ = os.Remove(path) })

	config := DefaultConfig()
	config.Path = path
	config.VectorDim = 3
	config.HNSW.Enabled = false
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	for i, kind := range []string{"note", "note", "secret"} {
		if err := store.Upsert(ctx, &Embedding{
			ID:       fmt.Sprintf("e%d", i),
			Vector:   []float32{1, 0, 0},
			Metadata: map[string]string{"kind": kind, "team.name": "core"},
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	return store
}

// TestAggregationRefusesNamesThatAreNotNames covers the two injections that
// were reachable before metadata names were checked.
//
// Both were plain-caller reachable, and neither raised an error — that is what
// makes them worth a test rather than a note. The filter case is the one that
// matters most: the request asks for a value nothing has, so the honest answer
// is zero, and before the fix it was the entire table including the row the
// filter existed to exclude.
func TestAggregationRefusesNamesThatAreNotNames(t *testing.T) {
	store := injectionFixture(t)
	ctx := context.Background()

	t.Run("the honest request still works", func(t *testing.T) {
		got, err := store.Aggregate(ctx, AggregationRequest{
			Type:    AggregationCount,
			Filters: map[string]interface{}{"kind": "note"},
		})
		if err != nil {
			t.Fatalf("a legitimate aggregation was refused: %v", err)
		}
		if want := 2; !numericEquals(got.Results[0].Value, want) {
			t.Fatalf("count(kind=note) = %v, want %d", got.Results[0].Value, want)
		}
	})

	// A dotted or hyphenated key is legal where it stays inside a JSON path,
	// and refused where it would become a SQL identifier. Both halves are
	// asserted, because narrowing the first would silently break metadata keys
	// like content-type that work today.
	t.Run("a dotted key is legal inside a path", func(t *testing.T) {
		if _, err := store.Aggregate(ctx, AggregationRequest{
			Type: AggregationCount, Filters: map[string]interface{}{"team.name": "core"},
		}); err != nil {
			t.Fatalf("a nested metadata path was refused as a filter: %v", err)
		}
	})

	t.Run("a dotted key is refused where it becomes an alias", func(t *testing.T) {
		_, err := store.Aggregate(ctx, AggregationRequest{Type: AggregationGroupBy, GroupBy: []string{"team.name"}})
		if err == nil {
			t.Fatal("accepted: GROUP BY aliases by the raw name, and this one is not a SQL identifier")
		}
		if !strings.Contains(err.Error(), "group_by is not a valid metadata name") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})

	t.Run("the aggregate aliases stay legal for order_by", func(t *testing.T) {
		if _, err := store.Aggregate(ctx, AggregationRequest{
			Type: AggregationGroupBy, GroupBy: []string{"kind"}, OrderBy: "count",
		}); err != nil {
			t.Fatalf("the count alias was refused: %v", err)
		}
	})

	// Each of these ran, or would have run, as SQL.
	for _, tc := range []struct {
		name string
		req  AggregationRequest
		role string
	}{
		{
			// Pasted raw after ORDER BY. This one executed: it returned two
			// groups and no error.
			name: "order_by carrying a subquery",
			req:  AggregationRequest{Type: AggregationGroupBy, GroupBy: []string{"kind"}, OrderBy: "(SELECT COUNT(*) FROM embeddings)"},
			role: "order_by",
		},
		{
			// Closes its own json_extract and reopens the next one, so the
			// predicate becomes "IS NOT NULL OR <the real test>". Asked for a
			// value nothing has and returned all three rows.
			name: "filter key that reopens the predicate",
			req: AggregationRequest{Type: AggregationCount, Filters: map[string]interface{}{
				"kind') IS NOT NULL OR json_extract(metadata,'$.kind": "matches-nothing",
			}},
			role: "filter",
		},
		{
			name: "field escaping its json path",
			req:  AggregationRequest{Type: AggregationSum, Field: "kind') AS REAL)) as agg_value, (SELECT COUNT(*) FROM embeddings) as leaked, MAX(CAST(json_extract(metadata, '$.kind"},
			role: "field",
		},
		{
			name: "group_by naming an expression",
			req:  AggregationRequest{Type: AggregationGroupBy, GroupBy: []string{"kind') as k, (SELECT COUNT(*) FROM embeddings"}},
			role: "group_by",
		},
		{
			name: "having key naming an expression",
			req:  AggregationRequest{Type: AggregationGroupBy, GroupBy: []string{"kind"}, Having: map[string]interface{}{"1=1 OR count": 0}},
			role: "having",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Aggregate(ctx, tc.req)
			if err == nil {
				t.Fatalf("accepted: the name reached the statement text")
			}
			if !strings.Contains(err.Error(), tc.role+" is not a valid metadata name") {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
		})
	}
}

// The faceted path interpolates its names the same way and is checked by the
// same rule, on both backends.
func TestFacetedSearchRefusesNamesThatAreNotNames(t *testing.T) {
	store := injectionFixture(t)
	ctx := context.Background()

	if _, _, err := store.SearchWithFacets(ctx, []float32{1, 0, 0}, FacetedSearchOptions{
		SearchOptions: SearchOptions{TopK: 5},
		Facets:        map[string]FacetFilter{"kind": {Type: FilterTypeEquals, Values: []interface{}{"note"}}},
	}); err != nil {
		t.Fatalf("a legitimate facet was refused: %v", err)
	}

	for role, opts := range map[string]FacetedSearchOptions{
		"facet": {
			SearchOptions: SearchOptions{TopK: 5},
			Facets:        map[string]FacetFilter{"kind') IS NOT NULL OR json_extract(e.metadata,'$.kind": {Type: FilterTypeEquals, Values: []interface{}{"x"}}},
		},
		"filter": {
			SearchOptions: SearchOptions{TopK: 5, Filter: map[string]string{"kind') IS NOT NULL OR json_extract(e.metadata,'$.kind": "x"}},
		},
	} {
		t.Run(role, func(t *testing.T) {
			_, _, err := store.SearchWithFacets(ctx, []float32{1, 0, 0}, opts)
			if err == nil {
				t.Fatalf("accepted: the name reached the statement text")
			}
			if !strings.Contains(err.Error(), role+" is not a valid metadata name") {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
		})
	}
}

// numericEquals compares an aggregation value against an int without caring
// which numeric type the driver chose for it.
func numericEquals(got interface{}, want int) bool {
	switch v := got.(type) {
	case int:
		return v == want
	case int64:
		return v == int64(want)
	case float64:
		return v == float64(want)
	default:
		return false
	}
}
