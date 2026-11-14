package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

// measureSeed is six rows under two kinds. score has a value that is not a
// number and a row without the field; latency has a number with a unit on
// it; at has a timestamp with an offset, a bare date and something that is
// not a time.
var measureSeed = []*Embedding{
	{ID: "r1", Vector: []float32{1, 0, 0, 0}, Content: "r1",
		Metadata: map[string]string{"kind": "incident", "score": "0.5", "latency": "100", "at": "2026-09-30T23:30:00Z"}},
	{ID: "r2", Vector: []float32{1, 0, 0, 0}, Content: "r2",
		Metadata: map[string]string{"kind": "incident", "score": "0.9", "latency": "200", "at": "2026-10-01T08:00:00+08:00"}},
	{ID: "r3", Vector: []float32{1, 0, 0, 0}, Content: "r3",
		Metadata: map[string]string{"kind": "incident", "score": "n/a", "latency": "300", "at": "2026-10-15"}},
	{ID: "r4", Vector: []float32{1, 0, 0, 0}, Content: "r4",
		Metadata: map[string]string{"kind": "note", "score": "0.2", "latency": "400", "at": "2026-10-20T10:00:00Z"}},
	{ID: "r5", Vector: []float32{1, 0, 0, 0}, Content: "r5",
		Metadata: map[string]string{"kind": "note", "score": "0.4", "latency": "12ms", "at": "2026-11-02T00:00:00Z"}},
	{ID: "r6", Vector: []float32{1, 0, 0, 0}, Content: "r6",
		Metadata: map[string]string{"kind": "note", "latency": "500", "at": "not a date"}},
}

func seedMeasures(t *testing.T, s parityBackend) {
	t.Helper()
	if err := s.UpsertBatch(context.Background(), measureSeed); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
}

func approx(t *testing.T, what string, got interface{}, want float64) {
	t.Helper()
	n, ok := got.(float64)
	if !ok {
		if i, isInt := got.(int); isInt {
			n, ok = float64(i), true
		}
	}
	if !ok || math.Abs(n-want) > 1e-9 {
		t.Errorf("%s = %v (%T), want %v", what, got, got, want)
	}
}

// groupNamed finds the result whose group key holds the value.
func groupNamed(t *testing.T, res *AggregationResponse, key string, value interface{}) AggregationResult {
	t.Helper()
	for _, r := range res.Results {
		if r.GroupKeys[key] == value {
			return r
		}
	}
	t.Fatalf("no group with %s = %v in %+v", key, value, res.Results)
	return AggregationResult{}
}

// Several measures come back together for every group, a value that is not a
// number is left out and counted, and a percentile is the interpolated rank.
func TestMeasuresAreComputedTogetherPerGroup(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		seedMeasures(t, s)
		res, err := s.Aggregate(context.Background(), AggregationRequest{
			GroupBy: []string{"kind"},
			Measures: []AggregationMeasure{
				{Type: "count"},
				{Type: "count", Field: "score", Alias: "with_score"},
				{Type: "avg", Field: "score"},
				{Type: "sum", Field: "score"},
				{Type: "min", Field: "score"},
				{Type: "max", Field: "score"},
				{Type: "p95", Field: "latency"},
				{Type: "percentile", Field: "latency", Percentile: 25, Alias: "q1"},
				{Type: "median", Field: "latency"},
				{Type: "stddev", Field: "latency"},
				{Type: "stddev_pop", Field: "latency"},
			},
		})
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		if res.Total != 2 {
			t.Fatalf("total = %d, want the two kinds: %+v", res.Total, res.Results)
		}

		inc := groupNamed(t, res, "kind", "incident")
		if inc.Count != 3 {
			t.Errorf("incident count = %d, want 3", inc.Count)
		}
		approx(t, "incident value (the first measure)", inc.Value, 3)
		approx(t, "incident count", inc.Values["count"], 3)
		approx(t, "incident with_score", inc.Values["with_score"], 3)
		approx(t, "incident avg_score", inc.Values["avg_score"], 0.7)
		approx(t, "incident sum_score", inc.Values["sum_score"], 1.4)
		approx(t, "incident min_score", inc.Values["min_score"], 0.5)
		approx(t, "incident max_score", inc.Values["max_score"], 0.9)
		approx(t, "incident p95_latency", inc.Values["p95_latency"], 290)
		approx(t, "incident q1", inc.Values["q1"], 150)
		approx(t, "incident median_latency", inc.Values["median_latency"], 200)
		approx(t, "incident stddev_latency", inc.Values["stddev_latency"], 100)
		approx(t, "incident stddev_pop_latency", inc.Values["stddev_pop_latency"], math.Sqrt(20000.0/3))
		// "n/a" is in neither the average nor the sum, and the result says so.
		for _, alias := range []string{"avg_score", "sum_score", "min_score", "max_score"} {
			if inc.Skipped[alias] != 1 {
				t.Errorf("incident skipped[%s] = %d, want the one n/a row", alias, inc.Skipped[alias])
			}
		}
		if _, counted := inc.Skipped["with_score"]; counted {
			t.Errorf("a count reported skipped rows: %+v", inc.Skipped)
		}

		note := groupNamed(t, res, "kind", "note")
		approx(t, "note with_score", note.Values["with_score"], 2)
		approx(t, "note avg_score", note.Values["avg_score"], 0.3)
		approx(t, "note p95_latency", note.Values["p95_latency"], 495)
		approx(t, "note median_latency", note.Values["median_latency"], 450)
		if note.Skipped["p95_latency"] != 1 || note.Skipped["avg_score"] != 0 {
			t.Errorf("note skipped = %+v, want the 12ms row under the latency measures only", note.Skipped)
		}
	})
}

// A measure over no number is null, not zero.
func TestAMeasureOverNoNumberIsNull(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		seedMeasures(t, s)
		res, err := s.Aggregate(context.Background(), AggregationRequest{
			Filters: map[string]interface{}{"score": "n/a"},
			Measures: []AggregationMeasure{
				{Type: "sum", Field: "score"}, {Type: "avg", Field: "score"}, {Type: "median", Field: "score"},
				{Type: "stddev", Field: "latency"}, {Type: "sum", Field: "absent"},
			},
		})
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		if len(res.Results) != 1 || res.Results[0].Count != 1 {
			t.Fatalf("results = %+v, want one ungrouped row over the n/a record", res.Results)
		}
		got := res.Results[0]
		for alias, v := range got.Values {
			if v != nil {
				t.Errorf("%s = %v, want null: there is no number (or one number, for stddev) to compute it from", alias, v)
			}
		}
		if got.Skipped["sum_score"] != 1 || got.Skipped["sum_absent"] != 0 {
			t.Errorf("skipped = %+v, want the n/a row under the score measures", got.Skipped)
		}
		encoded, err := json.Marshal(got)
		if err != nil || !strings.Contains(string(encoded), `"sum_score":null`) {
			t.Errorf("encoded as %s (%v), want an explicit null", encoded, err)
		}

		// Nothing matched at all: still one row, saying zero.
		empty, err := s.Aggregate(context.Background(), AggregationRequest{
			Filters:  map[string]interface{}{"kind": "nothing"},
			Measures: []AggregationMeasure{{Type: "count"}, {Type: "avg", Field: "score"}},
		})
		if err != nil || len(empty.Results) != 1 || empty.Results[0].Count != 0 || empty.Results[0].Values["avg_score"] != nil {
			t.Errorf("no rows matched: %+v (%v), want one row with count 0 and a null average", empty, err)
		}
	})
}

// The single-aggregation spelling still reads a non-numeric value as zero,
// and still returns only value and count.
func TestTheSingleAggregationKeepsItsOldReading(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		seedMeasures(t, s)
		ctx := context.Background()
		sum, err := s.Aggregate(ctx, AggregationRequest{Type: AggregationSum, Field: "score", Filters: map[string]interface{}{"kind": "incident"}})
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		approx(t, "sum", sum.Results[0].Value, 1.4)
		if sum.Results[0].Count != 3 || sum.Results[0].Values != nil || sum.Results[0].Skipped != nil {
			t.Errorf("single sum = %+v, want count 3 (n/a read as 0) and no values/skipped", sum.Results[0])
		}
		grouped, err := s.Aggregate(ctx, AggregationRequest{Type: AggregationGroupBy, GroupBy: []string{"kind"}})
		if err != nil || len(grouped.Results) != 2 || grouped.Results[0].Values != nil {
			t.Errorf("single group_by = %+v (%v), want two groups with no values map", grouped, err)
		}
		encoded, _ := json.Marshal(grouped.Results[0])
		if strings.Contains(string(encoded), "values") || strings.Contains(string(encoded), "skipped") {
			t.Errorf("a single aggregation's result gained fields: %s", encoded)
		}
	})
}

func TestTimeBucketOnAMetadataField(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		seedMeasures(t, s)
		ctx := context.Background()

		buckets := func(bucket AggregationTimeBucket) []string {
			t.Helper()
			res, err := s.Aggregate(ctx, AggregationRequest{TimeBucket: &bucket, Measures: []AggregationMeasure{{Type: "count"}}})
			if err != nil {
				t.Fatalf("Aggregate %+v: %v", bucket, err)
			}
			name := bucket.Alias
			if name == "" {
				name = bucket.Unit
			}
			var out []string
			for _, r := range res.Results {
				out = append(out, fmt.Sprintf("%v=%d", r.GroupKeys[name], r.Count))
			}
			return out
		}
		for _, c := range []struct {
			bucket AggregationTimeBucket
			want   string
		}{
			// Oldest first; the row whose value is not a time is its own
			// group with a null key. r2 is 00:00 UTC on the first of October.
			{AggregationTimeBucket{Unit: "month", Field: "at"}, "<nil>=1 2026-09=1 2026-10=3 2026-11=1"},
			{AggregationTimeBucket{Unit: "year", Field: "at", Alias: "y"}, "<nil>=1 2026=5"},
			{AggregationTimeBucket{Unit: "day", Field: "at"}, "<nil>=1 2026-09-30=1 2026-10-01=1 2026-10-15=1 2026-10-20=1 2026-11-02=1"},
			// Weeks start on Monday: 2026-09-28, 2026-10-12, 2026-10-19, 2026-11-02.
			{AggregationTimeBucket{Unit: "week", Field: "at"}, "<nil>=1 2026-09-28=2 2026-10-12=1 2026-10-19=1 2026-11-02=1"},
			// 23:30 UTC on 30 September is already October in Shanghai.
			{AggregationTimeBucket{Unit: "month", Field: "at", Timezone: "Asia/Shanghai"}, "<nil>=1 2026-10=4 2026-11=1"},
		} {
			if got := strings.Join(buckets(c.bucket), " "); got != c.want {
				t.Errorf("%+v:\n got %s\nwant %s", c.bucket, got, c.want)
			}
		}

		// A year comes back as the string "2026", not the number 2026.
		res, err := s.Aggregate(ctx, AggregationRequest{TimeBucket: &AggregationTimeBucket{Unit: "year", Field: "at"}, Filters: map[string]interface{}{"at": map[string]interface{}{"gte": "2026-01-01"}}})
		if err != nil || len(res.Results) != 1 || res.Results[0].GroupKeys["year"] != "2026" {
			t.Errorf("year bucket = %+v (%v), want the string 2026", res, err)
		}

		// Bucket and field together, newest first, capped.
		res, err = s.Aggregate(ctx, AggregationRequest{
			TimeBucket: &AggregationTimeBucket{Unit: "month", Field: "at"},
			GroupBy:    []string{"kind"},
			Measures:   []AggregationMeasure{{Type: "count"}, {Type: "avg", Field: "score"}},
			Order:      "desc",
			Limit:      2,
		})
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		if len(res.Results) != 2 || res.Results[0].GroupKeys["month"] != "2026-11" || res.Results[1].GroupKeys["month"] != "2026-10" || res.Results[1].GroupKeys["kind"] != "note" {
			t.Errorf("newest first, limit 2 = %+v", res.Results)
		}

		// Ordered by a measure alias, ascending.
		res, err = s.Aggregate(ctx, AggregationRequest{
			GroupBy: []string{"kind"}, Measures: []AggregationMeasure{{Type: "avg", Field: "score", Alias: "mean"}},
			OrderBy: "mean", Order: "asc",
		})
		if err != nil || len(res.Results) != 2 || res.Results[0].GroupKeys["kind"] != "note" {
			t.Errorf("order_by mean asc = %+v (%v), want note (0.3) before incident (0.7)", res, err)
		}
	})
}

func TestOperatorFilters(t *testing.T) {
	runOnBothStores(t, 4, func(t *testing.T, s parityBackend) {
		seedMeasures(t, s)
		ctx := context.Background()
		ops := func(pairs ...interface{}) map[string]interface{} {
			m := map[string]interface{}{}
			for i := 0; i < len(pairs); i += 2 {
				m[pairs[i].(string)] = pairs[i+1]
			}
			return m
		}
		for _, c := range []struct {
			name    string
			filters map[string]interface{}
			want    int
		}{
			{"gte keeps numbers at or above, and never n/a", map[string]interface{}{"score": ops("gte", 0.4)}, 3},
			{"gt and lt together", map[string]interface{}{"score": ops("gt", 0.4, "lt", 0.9)}, 1},
			{"lte", map[string]interface{}{"score": ops("lte", 0.4)}, 2},
			{"a numeric string bound compares as a number", map[string]interface{}{"latency": ops("gte", "300")}, 3},
			{"in", map[string]interface{}{"kind": ops("in", []interface{}{"note", "other"})}, 3},
			{"in over numbers", map[string]interface{}{"latency": ops("in", []interface{}{100.0, 500.0})}, 2},
			{"neq", map[string]interface{}{"kind": ops("neq", "incident")}, 3},
			{"ne is neq", map[string]interface{}{"kind": ops("ne", "incident")}, 3},
			{"neq a number keeps the non-numeric value and drops the missing one", map[string]interface{}{"score": ops("ne", 0.5)}, 4},
			{"eq a number compares by value", map[string]interface{}{"score": ops("eq", 0.50)}, 1},
			{"eq a string compares as text", map[string]interface{}{"score": ops("eq", "0.50")}, 0},
			{"exists true", map[string]interface{}{"score": ops("exists", true)}, 5},
			{"exists false", map[string]interface{}{"score": ops("exists", false)}, 1},
			{"a date bound compares as a time", map[string]interface{}{"at": ops("gte", "2026-10-01")}, 4},
			{"a time range", map[string]interface{}{"at": ops("gte", "2026-10-01T00:00:00Z", "lt", "2026-11-01")}, 3},
			{"before a date leaves out what is not a time", map[string]interface{}{"at": ops("lt", "2026-10-01")}, 1},
			{"a text bound compares as text", map[string]interface{}{"kind": ops("gt", "incident")}, 3},
			{"a plain equality beside an operator", map[string]interface{}{"kind": "note", "latency": ops("gte", 450)}, 1},
		} {
			res, err := s.Aggregate(ctx, AggregationRequest{Type: AggregationCount, Filters: c.filters})
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
				continue
			}
			if got := res.Results[0].Count; got != c.want {
				t.Errorf("%s: counted %d, want %d", c.name, got, c.want)
			}
		}

		// The single-aggregation types work under an operator filter, with
		// the strict reading of numbers.
		avg, err := s.Aggregate(ctx, AggregationRequest{Type: AggregationAvg, Field: "score", Filters: map[string]interface{}{"kind": ops("eq", "incident")}})
		if err != nil {
			t.Fatalf("avg: %v", err)
		}
		approx(t, "avg under an operator filter", avg.Results[0].Value, 0.7)
		if avg.Results[0].Skipped["avg_score"] != 1 {
			t.Errorf("skipped = %+v, want the n/a row reported", avg.Results[0].Skipped)
		}
	})
}

// Excluded and only-these documents hold on the measure path as they do on
// the single-aggregation one.
func TestMeasurePathKeepsTheDocumentConditions(t *testing.T) {
	for _, s := range storesUnderTest(t) {
		t.Run(s.name, func(t *testing.T) {
			ctx := context.Background()
			for _, doc := range []string{"hidden", "shown"} {
				if err := s.store.CreateDocument(ctx, &Document{ID: doc, Title: doc}); err != nil {
					t.Fatalf("CreateDocument: %v", err)
				}
				for i := 0; i < 2; i++ {
					if err := s.store.Upsert(ctx, &Embedding{
						ID: fmt.Sprintf("%s-%d", doc, i), Vector: []float32{1, 0, 0, 0}, Content: doc, DocID: doc,
						Metadata: map[string]string{"kind": "chunk", "n": "10"},
					}); err != nil {
						t.Fatalf("Upsert: %v", err)
					}
				}
			}
			if err := s.store.Upsert(ctx, &Embedding{ID: "loose", Vector: []float32{0, 1, 0, 0}, Content: "loose", Metadata: map[string]string{"kind": "chunk", "n": "1"}}); err != nil {
				t.Fatalf("Upsert loose: %v", err)
			}
			req := AggregationRequest{
				GroupBy:  []string{"kind"},
				Measures: []AggregationMeasure{{Type: "sum", Field: "n"}, {Type: "median", Field: "n"}},
				Filters:  map[string]interface{}{"n": map[string]interface{}{"gte": 1}},
			}

			excluded := req
			excluded.ExcludeDocuments, excluded.ExcludeDocumentsArgs = "id = ?", []interface{}{"hidden"}
			res, err := s.store.Aggregate(ctx, excluded)
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if len(res.Results) != 1 || res.Results[0].Count != 3 || res.Results[0].Values["sum_n"] != 21.0 {
				t.Errorf("with hidden excluded = %+v, want 3 rows summing to 21", res.Results)
			}

			only := req
			only.OnlyDocuments, only.OnlyDocumentsArgs = "id = ?", []interface{}{"shown"}
			res, err = s.store.Aggregate(ctx, only)
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if len(res.Results) != 1 || res.Results[0].Count != 2 || res.Results[0].Values["sum_n"] != 20.0 {
				t.Errorf("only shown = %+v, want its 2 rows summing to 20", res.Results)
			}
		})
	}
}

// Nothing a caller names reaches the statement text unchecked, and an option
// that is not one of the listed values is refused rather than guessed at.
func TestMeasurePathRefusesNamesAndOptionsThatAreNotValid(t *testing.T) {
	store := injectionFixture(t)
	ctx := context.Background()
	count := []AggregationMeasure{{Type: "count"}}

	for _, c := range []struct {
		name string
		req  AggregationRequest
		want string
	}{
		{"a measure field that closes the JSON path",
			AggregationRequest{Measures: []AggregationMeasure{{Type: "sum", Field: "x') OR 1=1 --"}}}, "measure field is not a valid metadata name"},
		{"a measure field holding a subquery",
			AggregationRequest{Measures: []AggregationMeasure{{Type: "avg", Field: "(SELECT 1)"}}}, "measure field is not a valid metadata name"},
		{"a measure alias that is SQL",
			AggregationRequest{Measures: []AggregationMeasure{{Type: "count", Alias: "n; DROP TABLE embeddings"}}}, "measure alias is not a valid metadata name"},
		{"a bucket unit that is SQL",
			AggregationRequest{Measures: count, TimeBucket: &AggregationTimeBucket{Unit: "month'); DROP TABLE embeddings; --"}}, "time_bucket unit must be one of"},
		{"a bucket unit that is not listed",
			AggregationRequest{Measures: count, TimeBucket: &AggregationTimeBucket{Unit: "quarter"}}, "time_bucket unit must be one of"},
		{"a bucket field that closes the JSON path",
			AggregationRequest{Measures: count, TimeBucket: &AggregationTimeBucket{Unit: "day", Field: "at') OR ('1'='1"}}, "time_bucket field is not a valid metadata name"},
		{"a bucket alias that is SQL",
			AggregationRequest{Measures: count, TimeBucket: &AggregationTimeBucket{Unit: "day", Alias: "d, (SELECT 1)"}}, "time_bucket alias is not a valid metadata name"},
		{"a bucket timezone that is not a zone",
			AggregationRequest{Measures: count, TimeBucket: &AggregationTimeBucket{Unit: "day", Timezone: "'; DROP TABLE x"}}, "timezone is not an IANA zone"},
		{"a filter key that closes the JSON path, under an operator",
			AggregationRequest{Measures: count, Filters: map[string]interface{}{"kind') OR ('1'='1": map[string]interface{}{"eq": "x"}}}, "filter is not a valid metadata name"},
		{"a group_by that is SQL",
			AggregationRequest{Measures: count, GroupBy: []string{"kind, (SELECT 1)"}}, "group_by is not a valid metadata name"},
		{"an operator that is not one",
			AggregationRequest{Measures: count, Filters: map[string]interface{}{"kind": map[string]interface{}{"$where": "1=1"}}}, "unknown filter operator"},
		{"an operator object with nothing in it",
			AggregationRequest{Type: AggregationCount, Filters: map[string]interface{}{"kind": map[string]interface{}{}}}, "operator object is empty"},
		{"an in that is not a list",
			AggregationRequest{Measures: count, Filters: map[string]interface{}{"kind": map[string]interface{}{"in": "note"}}}, "non-empty list"},
		{"an exists that is not a boolean",
			AggregationRequest{Measures: count, Filters: map[string]interface{}{"kind": map[string]interface{}{"exists": "yes"}}}, "takes true or false"},
		{"a measure type that is not one",
			AggregationRequest{Measures: []AggregationMeasure{{Type: "group_concat", Field: "kind"}}}, "measure type must be one of"},
		{"a pNN that is not a number",
			AggregationRequest{Measures: []AggregationMeasure{{Type: "p95); DROP", Field: "kind"}}}, "measure type must be one of"},
		{"a percentile out of range",
			AggregationRequest{Measures: []AggregationMeasure{{Type: "p101", Field: "kind"}}}, "percentile must be above 0"},
		{"a percentile with no rank",
			AggregationRequest{Measures: []AggregationMeasure{{Type: "percentile", Field: "kind"}}}, "percentile must be above 0"},
		{"a measure with no field",
			AggregationRequest{Measures: []AggregationMeasure{{Type: "avg"}}}, "field is required"},
		{"two measures under one alias",
			AggregationRequest{Measures: []AggregationMeasure{{Type: "avg", Field: "a"}, {Type: "avg", Field: "a"}}}, "share an alias"},
		{"an order that is not a direction",
			AggregationRequest{Measures: count, Order: "desc, (SELECT 1)"}, "order must be asc or desc"},
		{"an order_by that names nothing in the result",
			AggregationRequest{Measures: count, GroupBy: []string{"kind"}, OrderBy: "sqlite_version"}, "order_by must be count"},
		{"a created_since that is not a time",
			AggregationRequest{Measures: count, CreatedSince: "' OR 1=1 --"}, "created_since must be an RFC 3339"},
		{"a created_before that is not a time",
			AggregationRequest{Type: AggregationCount, CreatedBefore: "yesterday"}, "created_before must be an RFC 3339"},
		{"having beside measures",
			AggregationRequest{Measures: count, GroupBy: []string{"kind"}, Having: map[string]interface{}{"count": 2}}, "having is not supported"},
		{"no type and no measures",
			AggregationRequest{GroupBy: []string{"kind"}}, "aggregation type is required"},
	} {
		_, err := store.Aggregate(ctx, c.req)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refused for the wrong reason: %v", c.name, err)
		}
		// A refusal does not carry the text it refused.
		for _, echoed := range []string{"DROP", "SELECT", "1=1", "$where"} {
			if strings.Contains(err.Error(), echoed) {
				t.Errorf("%s: the refusal echoes the rejected text: %v", c.name, err)
			}
		}
	}

	// A filter scoped to a value nothing has stays at zero, and the table
	// is still there.
	res, err := store.Aggregate(ctx, AggregationRequest{Measures: count, Filters: map[string]interface{}{"kind": map[string]interface{}{"eq": "x' OR '1'='1"}}})
	if err != nil || res.Results[0].Count != 0 {
		t.Errorf("a hostile filter value counted %+v (%v), want 0: values are compared, never executed", res, err)
	}
	all, err := store.Aggregate(ctx, AggregationRequest{Type: AggregationCount})
	if err != nil || all.Results[0].Count != 3 {
		t.Errorf("after the refusals the table holds %+v (%v), want its 3 rows", all, err)
	}
	// A dotted and a hyphenated metadata name are still names.
	if _, err := store.Aggregate(ctx, AggregationRequest{Measures: []AggregationMeasure{{Type: "count", Field: "team.name"}, {Type: "max", Field: "content-length"}}}); err != nil {
		t.Errorf("a dotted or hyphenated field was refused: %v", err)
	}
}

// The row's own created-at time: bucketing by it and bounding it. SQLite
// only, because the test has to set the column to known times.
func TestTimeBucketAndBoundsOnTheCreatedAtColumn(t *testing.T) {
	store := injectionFixture(t) // e0, e1 (note), e2 (secret)
	ctx := context.Background()
	for id, at := range map[string]string{
		"e0": "2026-09-30 23:59:59",
		"e1": "2026-10-01 00:00:00",
		"e2": "2026-11-15 12:00:00",
	} {
		if _, err := store.db.ExecContext(ctx, `UPDATE embeddings SET created_at = ? WHERE id = ?`, at, id); err != nil {
			t.Fatalf("set created_at: %v", err)
		}
	}

	series := func(req AggregationRequest) string {
		t.Helper()
		req.TimeBucket = &AggregationTimeBucket{Unit: "month"}
		req.Measures = []AggregationMeasure{{Type: "count"}}
		res, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Aggregate %+v: %v", req, err)
		}
		var out []string
		for _, r := range res.Results {
			out = append(out, fmt.Sprintf("%v=%d", r.GroupKeys["month"], r.Count))
		}
		return strings.Join(out, " ")
	}
	if got := series(AggregationRequest{}); got != "2026-09=1 2026-10=1 2026-11=1" {
		t.Errorf("by month = %s", got)
	}
	if got := series(AggregationRequest{CreatedSince: "2026-10-01"}); got != "2026-10=1 2026-11=1" {
		t.Errorf("since October = %s, want the bound inclusive", got)
	}
	if got := series(AggregationRequest{CreatedSince: "2026-10-01T00:00:00Z", CreatedBefore: "2026-11-15T12:00:00Z"}); got != "2026-10=1" {
		t.Errorf("October to mid November = %s, want the upper bound exclusive", got)
	}
	// 08:00 in Shanghai on 1 October is 00:00 UTC.
	if got := series(AggregationRequest{CreatedBefore: "2026-10-01T08:00:00+08:00"}); got != "2026-09=1" {
		t.Errorf("before an offset bound = %s", got)
	}

	// A bound alone, with the single-aggregation spelling.
	res, err := store.Aggregate(ctx, AggregationRequest{Type: AggregationGroupBy, GroupBy: []string{"kind"}, CreatedSince: "2026-10-01"})
	if err != nil || len(res.Results) != 2 || res.Results[0].Count != 1 {
		t.Errorf("group_by since October = %+v (%v), want one note and one secret", res, err)
	}

	// A row just written carries a created-at the bucket can read.
	if err := store.Upsert(ctx, &Embedding{ID: "fresh", Vector: []float32{1, 0, 0}, Metadata: map[string]string{"kind": "fresh"}}); err != nil {
		t.Fatal(err)
	}
	res, err = store.Aggregate(ctx, AggregationRequest{TimeBucket: &AggregationTimeBucket{Unit: "year"}, Filters: map[string]interface{}{"kind": "fresh"}})
	if err != nil || len(res.Results) != 1 || res.Results[0].GroupKeys["year"] == nil {
		t.Errorf("a fresh row's year = %+v (%v), want a bucket, not a null key", res, err)
	}
}

// A number stored as a JSON number and one stored as a JSON string are the
// same number to a measure and to an operator filter; a JSON boolean is not
// a number. No writer in this module stores anything but strings, so the
// rows are written directly.
func TestJSONNumbersAndNumericStringsAreBothNumbers(t *testing.T) {
	store := injectionFixture(t)
	ctx := context.Background()
	for id, metadata := range map[string]string{
		"e0": `{"kind":"m","v":12.5}`,
		"e1": `{"kind":"m","v":"12.5"}`,
		"e2": `{"kind":"m","v":true}`,
	} {
		if _, err := store.db.ExecContext(ctx, `UPDATE embeddings SET metadata = ? WHERE id = ?`, metadata, id); err != nil {
			t.Fatalf("set metadata: %v", err)
		}
	}
	res, err := store.Aggregate(ctx, AggregationRequest{Measures: []AggregationMeasure{{Type: "sum", Field: "v"}, {Type: "count", Field: "v", Alias: "present"}}})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	got := res.Results[0]
	approx(t, "sum over a JSON number and a numeric string", got.Values["sum_v"], 25)
	approx(t, "rows carrying the field", got.Values["present"], 3)
	if got.Skipped["sum_v"] != 1 {
		t.Errorf("skipped = %+v, want the boolean left out and counted", got.Skipped)
	}
	filtered, err := store.Aggregate(ctx, AggregationRequest{Type: AggregationCount, Filters: map[string]interface{}{"v": map[string]interface{}{"gte": 12.5}}})
	if err != nil || filtered.Results[0].Count != 2 {
		t.Errorf("gte 12.5 counted %+v (%v), want both spellings of the number", filtered, err)
	}
	isTrue, err := store.Aggregate(ctx, AggregationRequest{Type: AggregationCount, Filters: map[string]interface{}{"v": map[string]interface{}{"eq": true}}})
	if err != nil || isTrue.Results[0].Count != 1 {
		t.Errorf("eq true counted %+v (%v), want the boolean row", isTrue, err)
	}
}

func TestParseStrictNumber(t *testing.T) {
	for text, want := range map[string]float64{"12.5": 12.5, " 3 ": 3, "-0.25": -0.25, "1e3": 1000, "+7": 7, ".5": 0.5} {
		if got, ok := parseStrictNumber(text); !ok || got != want {
			t.Errorf("parseStrictNumber(%q) = %v, %v; want %v", text, got, ok, want)
		}
	}
	for _, text := range []string{"", " ", "n/a", "12ms", "0x10", "Inf", "-inf", "NaN", "1e999", "1_000", "1,5", "--1", "true"} {
		if got, ok := parseStrictNumber(text); ok {
			t.Errorf("parseStrictNumber(%q) = %v, want it refused", text, got)
		}
	}
}

// The percentile is the exact interpolated rank, whatever order the values
// arrive in.
func TestPercentileIsExact(t *testing.T) {
	stats := fieldStats{}
	for _, v := range []float64{9, 1, 7, 3, 5, 10, 2, 8, 4, 6} {
		stats.add(v, true)
	}
	for p, want := range map[float64]float64{50: 5.5, 95: 9.55, 100: 10, 25: 3.25, 1: 1.09} {
		approx(t, fmt.Sprintf("p%v of 1..10", p), stats.percentile(p), want)
	}
	one := fieldStats{}
	one.add(42, true)
	approx(t, "p95 of one value", one.percentile(95), 42)
	if (&fieldStats{}).percentile(50) != nil {
		t.Error("a percentile of no values is not null")
	}
}
