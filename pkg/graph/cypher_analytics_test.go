package graph

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph/cypher"
)

// What trend and distribution questions need from Cypher: statistical
// aggregates, numeric helpers, and dates that bucket. Every expectation here
// is worked out by hand; where openCypher leaves a choice (an empty group, an
// unreadable date, two spellings of one instant) the test states which one
// was taken.

// analyticsCase is one query and the row it must return, or the kind of
// error it must fail with (and a word the message must carry).
type analyticsCase struct {
	q       string
	want    []any
	errKind cypher.ErrorKind
	errHas  string
}

// runAnalytics checks single-row queries. Values are compared with
// reflect.DeepEqual, so 29 (integer) and 29.0 (float) are different answers.
func runAnalytics(t *testing.T, g *GraphStore, cases []analyticsCase) {
	t.Helper()
	for _, c := range cases {
		res, err := g.QueryCypher(context.Background(), CypherRequest{Query: c.q})
		if c.errKind != "" {
			var ce *cypher.Error
			if err == nil || !errors.As(err, &ce) || ce.Kind != c.errKind {
				t.Errorf("%s\n  want a %s error, got %v (res=%v)", c.q, c.errKind, err, res)
				continue
			}
			if c.errHas != "" && !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("%s\n  error should mention %q: %v", c.q, c.errHas, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s\n  error: %v", c.q, err)
			continue
		}
		if len(res.Rows) != 1 {
			t.Errorf("%s\n  want one row, got %v", c.q, res.Rows)
			continue
		}
		if !sameValues(res.Rows[0], c.want) {
			t.Errorf("%s\n  got  %#v\n  want %#v", c.q, res.Rows[0], c.want)
		}
	}
}

// sameValues is DeepEqual with floats compared to a relative 1e-12: the
// expected values are written as decimal literals.
func sameValues(got, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		gf, gok := got[i].(float64)
		wf, wok := want[i].(float64)
		if gok && wok {
			if gf != wf && math.Abs(gf-wf) > 1e-12*math.Max(1, math.Abs(wf)) {
				return false
			}
			continue
		}
		if !reflect.DeepEqual(got[i], want[i]) {
			return false
		}
	}
	return true
}

func i64(v int64) any { return v }

func TestCypherStandardDeviationAndPercentilesAgreeWithHandComputedValues(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	runAnalytics(t, g, []analyticsCase{
		// Mean 5; squared deviations sum to 32. Population: sqrt(32/8) = 2.
		// Sample: sqrt(32/7).
		{q: "UNWIND [2, 4, 4, 4, 5, 5, 7, 9] AS x RETURN stDevP(x), stDev(x)", want: r(2.0, 2.138089935299395)},
		// Integers and floats mix; the answer is always a float.
		{q: "UNWIND [1, 2.0, 3] AS x RETURN stDevP(x), stDev(x)", want: r(0.816496580927726, 1.0)},
		// Nulls are skipped, not counted as zero: {2, 4} → sqrt(2).
		{q: "UNWIND [2, null, 4, null] AS x RETURN stDev(x), stDevP(x)", want: r(1.4142135623730951, 1.0)},
		// Too few values to deviate: 0.0, not null.
		{q: "UNWIND [7] AS x RETURN stDev(x), stDevP(x)", want: r(0.0, 0.0)},
		{q: "UNWIND [] AS x RETURN stDev(x), stDevP(x)", want: r(0.0, 0.0)},
		{q: "UNWIND [null, null] AS x RETURN stDev(x), stDevP(x)", want: r(0.0, 0.0)},
		// DISTINCT: {1, 3} instead of {1, 1, 1, 3}.
		{q: "UNWIND [1, 1, 1, 3] AS x RETURN stDevP(x), stDevP(DISTINCT x), stDev(DISTINCT x)", want: r(0.8660254037844386, 1.0, 1.4142135623730951)},
		// Large values close together, where a sum of squares loses the answer.
		{q: "UNWIND [1000000001, 1000000002, 1000000003] AS x RETURN stDev(x)", want: r(1.0)},

		// percentileCont interpolates: position p·(n-1) in the sorted values.
		// {13, 33, 44}, p 0.4 → position 0.8 → 13 + 0.8·20.
		{q: "UNWIND [44, 13, 33] AS x RETURN percentileCont(x, 0.4), percentileCont(x, 0.5), percentileCont(x, 0), percentileCont(x, 1)", want: r(29.0, 33.0, 13.0, 44.0)},
		// Even count: the median is between the middle two.
		{q: "UNWIND [4, 1, 3, 2] AS x RETURN percentileCont(x, 0.5), percentileCont(x, 0.25), percentileCont(x, 0.0), percentileCont(x, 1.0)", want: r(2.5, 1.75, 1.0, 4.0)},
		{q: "UNWIND [5] AS x RETURN percentileCont(x, 0.3), percentileDisc(x, 0.3)", want: r(5.0, i64(5))},

		// percentileDisc is nearest rank, ceil(p·n), and returns a value that
		// is in the group, as the type it has there.
		{q: "UNWIND [44, 13, 33] AS x RETURN percentileDisc(x, 0.5), percentileDisc(x, 0), percentileDisc(x, 1), percentileDisc(x, 0.34)", want: r(i64(33), i64(13), i64(44), i64(33))},
		{q: "UNWIND [4, 1, 3, 2] AS x RETURN percentileDisc(x, 0.5), percentileDisc(x, 0.51), percentileDisc(x, 0.25), percentileDisc(x, 0.75), percentileDisc(x, 0.0), percentileDisc(x, 1.0)", want: r(i64(2), i64(3), i64(1), i64(3), i64(1), i64(4))},
		{q: "UNWIND [2.5, 1.5] AS x RETURN percentileDisc(x, 0.5), percentileDisc(x, 0.9)", want: r(1.5, 2.5)},
		// 0.1 × 30 is 3.0000000000000004 in floating point; the rank is 3.
		{q: "UNWIND range(1, 30) AS x RETURN percentileDisc(x, 0.1), percentileDisc(x, 0.7)", want: r(i64(3), i64(21))},

		// Nulls are skipped; a group with nothing left has no percentile.
		{q: "UNWIND [null, 10, null, 20] AS x RETURN percentileCont(x, 0.5), percentileDisc(x, 0.5)", want: r(15.0, i64(10))},
		{q: "UNWIND [] AS x RETURN percentileCont(x, 0.5), percentileDisc(x, 0.5)", want: r(nil, nil)},
		{q: "UNWIND [null] AS x RETURN percentileCont(x, 0.5), percentileDisc(x, 0.5)", want: r(nil, nil)},
		// DISTINCT: {1, 3} → 2.0; all four → 1.0.
		{q: "UNWIND [1, 1, 1, 3] AS x RETURN percentileCont(x, 0.5), percentileCont(DISTINCT x, 0.5), percentileDisc(DISTINCT x, 1.0)", want: r(1.0, 2.0, i64(3))},
		// The percentile can be an expression; it is read once per group.
		{q: "UNWIND [1, 2, 3, 4, 5] AS x RETURN percentileCont(x, 1.0 / 4), percentileDisc(x, 9 / 10.0)", want: r(2.0, i64(5))},

		// A percentile outside [0, 1] is an error, on an empty group too.
		{q: "UNWIND [1, 2] AS x RETURN percentileCont(x, 1.5)", errKind: cypher.ErrRuntime, errHas: "0.0 to 1.0"},
		{q: "UNWIND [1, 2] AS x RETURN percentileDisc(x, -0.1)", errKind: cypher.ErrRuntime, errHas: "0.0 to 1.0"},
		{q: "UNWIND [1, 2] AS x RETURN percentileDisc(x, 50)", errKind: cypher.ErrRuntime, errHas: "0.0 to 1.0"},
		{q: "UNWIND [] AS x RETURN percentileCont(x, 2)", errKind: cypher.ErrRuntime, errHas: "0.0 to 1.0"},
		{q: "UNWIND [1, 2] AS x RETURN percentileCont(x, '0.5')", errKind: cypher.ErrRuntime, errHas: "string"},
		{q: "UNWIND [1, 2] AS x RETURN percentileCont(x, null)", errKind: cypher.ErrRuntime, errHas: "null"},
		// Not numbers.
		{q: "UNWIND [1, 'a'] AS x RETURN stDev(x)", errKind: cypher.ErrRuntime, errHas: "stDev() needs numbers"},
		{q: "UNWIND [1, 'a'] AS x RETURN stDevP(x)", errKind: cypher.ErrRuntime, errHas: "stDevP() needs numbers"},
		{q: "UNWIND ['a', 'b'] AS x RETURN percentileCont(x, 0.5)", errKind: cypher.ErrRuntime, errHas: "percentileCont() needs numbers"},
		{q: "UNWIND [true] AS x RETURN percentileDisc(x, 0.5)", errKind: cypher.ErrRuntime, errHas: "percentileDisc() needs numbers"},
		// Wrong shape, caught before any row is read.
		{q: "UNWIND [1] AS x RETURN percentileCont(x)", errKind: cypher.ErrSemantic, errHas: "two arguments"},
		{q: "UNWIND [1] AS x RETURN stDev(x, 1)", errKind: cypher.ErrSemantic, errHas: "one argument"},
		{q: "UNWIND [1] AS x WITH x WHERE stDev(x) > 0 RETURN x", errKind: cypher.ErrSemantic},
		{q: "UNWIND [1] AS x RETURN stDev(count(x))", errKind: cypher.ErrSemantic},
	})

	// Per group, with ORDER BY on the aggregate.
	res, err := g.QueryCypher(context.Background(), CypherRequest{Query: `
		UNWIND [{g: 'a', v: 1}, {g: 'a', v: 3}, {g: 'b', v: 10}, {g: 'a', v: 5}, {g: 'b', v: 20}, {g: 'c', v: null}] AS row
		RETURN row.g AS g, percentileCont(row.v, 0.5) AS median, stDev(row.v) AS sd, percentileDisc(row.v, 1) AS top
		ORDER BY median DESC`})
	if err != nil {
		t.Fatal(err)
	}
	// null sorts first when descending.
	want := [][]any{r("c", nil, 0.0, nil), r("b", 15.0, 7.0710678118654755, 20), r("a", 3.0, 2.0, 5)}
	if got, w := canonicalRows(res.Rows, true), canonicalRows(want, true); !reflect.DeepEqual(got, w) {
		t.Fatalf("grouped percentiles\n  got  %v\n  want %v", got, w)
	}
}

func TestCypherNumericHelpers(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	runAnalytics(t, g, []analyticsCase{
		{q: "RETURN log(1), exp(0), log(exp(2)), log10(1000), exp(1)", want: r(0.0, 1.0, 2.0, 3.0, math.E)},
		{q: "RETURN log(null), log10(null), exp(null)", want: r(nil, nil, nil)},
		{q: "RETURN log(0) < -1000000, exp(1000) > 1000000", want: r(true, true)},
		// A result cell that is not a finite number leaves as null: JSON cannot carry it.
		{q: "RETURN log(0), exp(1000), sqrt(-1), 1.5", want: r(nil, nil, nil, 1.5)},
		{q: "RETURN log('a')", errKind: cypher.ErrRuntime, errHas: "log()"},
		{q: "RETURN exp(true)", errKind: cypher.ErrRuntime},
		{q: "RETURN log(1, 2)", errKind: cypher.ErrSemantic},
		// What was already there keeps its meaning: round(x) rounds half up.
		{q: "RETURN abs(-3), abs(-2.5), sign(-2.5), sign(0), floor(2.7), ceil(2.1), sqrt(16), round(2.5), round(-2.5), round(2.4)", want: r(i64(3), 2.5, i64(-1), i64(0), 2.0, 3.0, 4.0, 3.0, -2.0, 2.0)},
		// round(x, precision) rounds the decimal as written, ties away from zero.
		{q: "RETURN round(3.14159, 2), round(2.675, 2), round(1234.5678, 1), round(2.5, 0), round(-2.5, 0), round(-1.005, 2)", want: r(3.14, 2.68, 1234.6, 3.0, -3.0, -1.01)},
		{q: "RETURN round(3, 2), round(0.5, 3), round(9.999, 2), round(-0.001, 2), round(0.0000001, 3)", want: r(3.0, 0.5, 10.0, 0.0, 0.0)},
		{q: "RETURN round(null, 2), round(1.5, null)", want: r(nil, nil)},
		{q: "RETURN round(1.5, -1)", errKind: cypher.ErrRuntime, errHas: "precision"},
		{q: "RETURN round(1.5, 0.5)", errKind: cypher.ErrRuntime, errHas: "precision"},
		{q: "RETURN round(1.5, 1, 'UP')", errKind: cypher.ErrSemantic},
	})
}

func TestCypherReadsDatesAndDatetimes(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	runAnalytics(t, g, []analyticsCase{
		// A date comes back as its ISO string.
		{q: "RETURN date('2026-03-05'), date('20260305'), date('2026-03'), date('2026')", want: r("2026-03-05", "2026-03-05", "2026-03-01", "2026-01-01")},
		// ISO week dates and ordinal dates. 2026-W01 begins on Monday 29 Dec 2025.
		{q: "RETURN date('2026-W01'), date('2026-W01-4'), date('2026-W53-7'), date('2026-060'), date('2024-366')", want: r("2025-12-29", "2026-01-01", "2027-01-03", "2026-03-01", "2024-12-31")},
		// A timestamp gives the date as written, in its own offset — not the UTC date.
		{q: "RETURN date('2026-03-05T23:30:00+08:00'), date('2026-03-05T00:30:00-05:00'), date('2026-03-05 10:00:00'), date(datetime('2026-03-05T23:30:00+08:00'))", want: r("2026-03-05", "2026-03-05", "2026-03-05", "2026-03-05")},
		{q: "RETURN date({year: 2026, month: 3, day: 5}), date({year: 2026, month: 3}), date({year: 2026}), date(date('2024-02-29'))", want: r("2026-03-05", "2026-03-01", "2026-01-01", "2024-02-29")},
		{q: "RETURN date(null), datetime(null), date.truncate('month', null), date.truncate(null, date('2026-01-01')), duration(null), duration.between(null, date('2026-01-01'))", want: r(nil, nil, nil, nil, nil, nil)},

		// A datetime keeps its offset; without one it is UTC.
		{q: "RETURN datetime('2026-03-05T10:00:00+08:00'), datetime('2026-03-05T10:00:00Z'), datetime('2026-03-05T10:00'), datetime('2026-03-05')", want: r("2026-03-05T10:00:00+08:00", "2026-03-05T10:00:00Z", "2026-03-05T10:00:00Z", "2026-03-05T00:00:00Z")},
		{q: "RETURN datetime('2026-03-05T10:00:00.5Z'), datetime('20260305T100000+0800'), datetime('2026-03-05 10:00:00.123456-03'), datetime(date('2026-03-05'))", want: r("2026-03-05T10:00:00.5Z", "2026-03-05T10:00:00+08:00", "2026-03-05T10:00:00.123456-03:00", "2026-03-05T00:00:00Z")},
		{q: "RETURN datetime({year: 2026, month: 3, day: 5, hour: 10, minute: 20, second: 30, millisecond: 5, timezone: '+08:00'}), datetime({epochSeconds: 0}), datetime({epochMillis: 1500}), datetime({epochSeconds: -1})", want: r("2026-03-05T10:20:30.005+08:00", "1970-01-01T00:00:00Z", "1970-01-01T00:00:01.5Z", "1969-12-31T23:59:59Z")},
		{q: "RETURN datetime.fromEpochMillis(1772704800000), datetime.fromEpoch(1772704800, 0), datetime({epochSeconds: 1772704800, timezone: '+08:00'})", want: r("2026-03-05T10:00:00Z", "2026-03-05T10:00:00Z", "2026-03-05T18:00:00+08:00")},
		{q: "RETURN toString(date('2026-03-05')), toString(datetime('2026-03-05T10:00:00+08:00')), toString(duration('P1D'))", want: r("2026-03-05", "2026-03-05T10:00:00+08:00", "P1D")},
		// Inside lists and maps too, and through collect.
		{q: "RETURN [date('2026-03-05'), 1] AS l, {d: datetime('2026-03-05')} AS m", want: r(l("2026-03-05", int64(1)), map[string]any{"d": "2026-03-05T00:00:00Z"})},
		{q: "UNWIND ['2026-01-02', '2026-01-01'] AS s RETURN collect(date(s)), min(date(s)), max(date(s))", want: r(l("2026-01-02", "2026-01-01"), "2026-01-01", "2026-01-02")},

		// Text that is not a date is an error naming the text — not null,
		// which would drop the row from every bucket without a word.
		{q: "RETURN date('soon')", errKind: cypher.ErrRuntime, errHas: `"soon"`},
		{q: "RETURN date('2026-02-30')", errKind: cypher.ErrRuntime, errHas: "2026-02-30"},
		{q: "RETURN date('2026-13-01')", errKind: cypher.ErrRuntime},
		{q: "RETURN date('2025-02-29')", errKind: cypher.ErrRuntime},
		{q: "RETURN date('2025-W53')", errKind: cypher.ErrRuntime}, // 2025 has 52 ISO weeks
		{q: "RETURN date('2025-366')", errKind: cypher.ErrRuntime},
		{q: "RETURN date('05/03/2026')", errKind: cypher.ErrRuntime},
		{q: "RETURN date('')", errKind: cypher.ErrRuntime},
		{q: "RETURN datetime('2026-03-05T25:00:00Z')", errKind: cypher.ErrRuntime, errHas: "time of day"},
		{q: "RETURN datetime('2026-03-05T10:00:00+25:00')", errKind: cypher.ErrRuntime, errHas: "offset"},
		{q: "RETURN datetime('2026-03-05T10:00:00[Europe/Berlin]')", errKind: cypher.ErrRuntime, errHas: "named time zones"},
		{q: "RETURN date(20260305)", errKind: cypher.ErrRuntime, errHas: "integer"},
		{q: "RETURN datetime(1772704800)", errKind: cypher.ErrRuntime, errHas: "epochSeconds"},
		{q: "RETURN date({year: 2026, month: 2, day: 30})", errKind: cypher.ErrRuntime},
		{q: "RETURN date({month: 2})", errKind: cypher.ErrRuntime, errHas: "year"},
		{q: "RETURN date({year: 2026, hour: 2})", errKind: cypher.ErrRuntime, errHas: "hour"},
		{q: "RETURN datetime({year: 2026, hour: 24})", errKind: cypher.ErrRuntime, errHas: "hour"},
		{q: "RETURN date('2026-01-01', 'x')", errKind: cypher.ErrSemantic},
		{q: "RETURN date(DISTINCT '2026-01-01')", errKind: cypher.ErrSemantic},
		// Namespaced functions outside the subset are still refused by name.
		{q: "RETURN date.statement()", errKind: cypher.ErrUnsupported, errHas: "date.statement"},
		{q: "RETURN apoc.date.format(1)", errKind: cypher.ErrUnsupported},
		{q: "RETURN localdatetime('2026-01-01T00:00:00')", errKind: cypher.ErrUnsupported},
	})
}

func TestCypherDateComponentsFollowTheISOCalendar(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	runAnalytics(t, g, []analyticsCase{
		// Thursday 5 March 2026: day 64 of the year, ISO week 10.
		{q: "WITH date('2026-03-05') AS d RETURN d.year, d.quarter, d.month, d.week, d.weekYear, d.day, d.ordinalDay, d.dayOfWeek, d.weekDay, d.dayOfQuarter",
			want: r(i64(2026), i64(1), i64(3), i64(10), i64(2026), i64(5), i64(64), i64(4), i64(4), i64(64))},
		// The ISO week-year is not the calendar year at the edges:
		// Fri 1 Jan 2027 is in 2026-W53; Mon 30 Dec 2024 opens 2025-W01;
		// Sun 3 Jan 2021 closes 2020-W53; Mon 4 Jan 2021 opens 2021-W01.
		{q: "RETURN date('2027-01-01').week, date('2027-01-01').weekYear, date('2027-01-01').year", want: r(i64(53), i64(2026), i64(2027))},
		{q: "RETURN date('2024-12-30').week, date('2024-12-30').weekYear, date('2024-12-29').week, date('2024-12-29').weekYear", want: r(i64(1), i64(2025), i64(52), i64(2024))},
		{q: "RETURN date('2021-01-03').week, date('2021-01-03').weekYear, date('2021-01-03').dayOfWeek, date('2021-01-04').week, date('2021-01-04').weekYear", want: r(i64(53), i64(2020), i64(7), i64(1), i64(2021))},
		{q: "RETURN date('2026-12-31').quarter, date('2026-10-01').dayOfQuarter, date('2024-12-31').ordinalDay, date('2026-04-01').quarter, date('2026-06-30').dayOfQuarter", want: r(i64(4), i64(1), i64(366), i64(2), i64(91))},
		{q: "WITH datetime('2026-03-05T10:20:30.123456789+08:00') AS t RETURN t.year, t.month, t.day, t.hour, t.minute, t.second, t.millisecond, t.microsecond, t.nanosecond, t.timezone, t.offset, t.offsetMinutes, t.offsetSeconds, t.week, t.dayOfWeek",
			want: r(i64(2026), i64(3), i64(5), i64(10), i64(20), i64(30), i64(123), i64(123456), i64(123456789), "+08:00", "+08:00", i64(480), i64(28800), i64(10), i64(4))},
		// The components are those of the local clock; the epoch is the instant.
		{q: "WITH datetime('2026-03-05T02:00:00+08:00') AS t RETURN t.day, t.hour, t.epochSeconds, t.epochMillis, datetime('2026-03-04T18:00:00Z').epochSeconds, datetime('2026-03-05T10:00:00Z').timezone",
			want: r(i64(5), i64(2), i64(1772647200), i64(1772647200000), i64(1772647200), "Z")},
		{q: "RETURN date(null).year", want: r(nil)},
		// A misspelt component is an error, not a missing property.
		{q: "RETURN date('2026-03-05').hour", errKind: cypher.ErrRuntime, errHas: "no component hour"},
		{q: "RETURN date('2026-03-05').Year", errKind: cypher.ErrRuntime, errHas: "no component Year"},
		{q: "RETURN datetime('2026-03-05').weekday", errKind: cypher.ErrRuntime},
		{q: "RETURN '2026-03-05'.year", errKind: cypher.ErrRuntime, errHas: "string"},
	})
}

func TestCypherTruncatesDatesToBuckets(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	runAnalytics(t, g, []analyticsCase{
		{q: "WITH date('2026-11-14') AS d RETURN date.truncate('day', d), date.truncate('week', d), date.truncate('month', d), date.truncate('quarter', d), date.truncate('year', d)",
			want: r("2026-11-14", "2026-11-09", "2026-11-01", "2026-10-01", "2026-01-01")},
		{q: "WITH date('2026-11-14') AS d RETURN date.truncate('decade', d), date.truncate('century', d), date.truncate('millennium', d), date.truncate('MONTH', d), date.truncate('weekYear', d)",
			want: r("2020-01-01", "2000-01-01", "2000-01-01", "2026-11-01", "2025-12-29")},
		// Weeks start on Monday and do not stop at a year boundary:
		// Thu 1 Jan 2026 → Mon 29 Dec 2025; Sun 3 Jan 2027 → Mon 28 Dec 2026;
		// a Monday is its own week start.
		{q: "RETURN date.truncate('week', date('2026-01-01')), date.truncate('week', date('2027-01-03')), date.truncate('week', date('2026-03-02')), date.truncate('week', date('2026-03-08'))",
			want: r("2025-12-29", "2026-12-28", "2026-03-02", "2026-03-02")},
		// The ISO week-year starts on the Monday of week 1.
		{q: "RETURN date.truncate('weekYear', date('2027-01-01')), date.truncate('weekYear', date('2024-12-30')), date.truncate('weekYear', date('2021-01-03'))",
			want: r("2025-12-29", "2024-12-30", "2019-12-30")},
		// Months and quarters across the year boundary.
		{q: "RETURN date.truncate('month', date('2025-12-31')), date.truncate('month', date('2026-01-01')), date.truncate('quarter', date('2025-12-31')), date.truncate('quarter', date('2026-01-01')), date.truncate('quarter', date('2026-03-31')), date.truncate('quarter', date('2026-04-01'))",
			want: r("2025-12-01", "2026-01-01", "2025-10-01", "2026-01-01", "2026-01-01", "2026-04-01")},
		{q: "RETURN date.truncate('month', date('2024-02-29')), date.truncate('year', date('2024-12-31'))", want: r("2024-02-01", "2024-01-01")},
		// A datetime truncates on its own clock and keeps its offset.
		{q: "WITH datetime('2026-03-05T10:20:30.5+08:00') AS t RETURN datetime.truncate('month', t), datetime.truncate('week', t), datetime.truncate('day', t), datetime.truncate('hour', t), datetime.truncate('minute', t), datetime.truncate('second', t)",
			want: r("2026-03-01T00:00:00+08:00", "2026-03-02T00:00:00+08:00", "2026-03-05T00:00:00+08:00", "2026-03-05T10:00:00+08:00", "2026-03-05T10:20:00+08:00", "2026-03-05T10:20:30+08:00")},
		{q: "WITH datetime('2026-03-05T10:20:30.123456789Z') AS t RETURN datetime.truncate('millisecond', t), datetime.truncate('microsecond', t), datetime.truncate('quarter', t), datetime.truncate('year', t)",
			want: r("2026-03-05T10:20:30.123Z", "2026-03-05T10:20:30.123456Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")},
		// Each takes the other kind: the local date of a datetime; midnight UTC of a date.
		{q: "RETURN date.truncate('month', datetime('2026-03-31T23:30:00+08:00')), date.truncate('day', datetime('2026-03-31T23:30:00+08:00')), datetime.truncate('month', date('2026-03-05'))",
			want: r("2026-03-01", "2026-03-31", "2026-03-01T00:00:00Z")},

		{q: "RETURN date.truncate('hour', date('2026-03-05'))", errKind: cypher.ErrRuntime, errHas: "unit"},
		{q: "RETURN date.truncate('fortnight', date('2026-03-05'))", errKind: cypher.ErrRuntime, errHas: "fortnight"},
		{q: "RETURN datetime.truncate('nanosecond', datetime('2026-03-05'))", errKind: cypher.ErrRuntime, errHas: "unit"},
		// A string is not truncated as if it were a date; the error says how.
		{q: "RETURN date.truncate('month', '2026-03-05')", errKind: cypher.ErrRuntime, errHas: "date(x)"},
		{q: "RETURN date.truncate('month', 5)", errKind: cypher.ErrRuntime, errHas: "integer"},
		{q: "RETURN date.truncate(date('2026-03-05'), 'month')", errKind: cypher.ErrRuntime, errHas: "unit first"},
		{q: "RETURN date.truncate('month')", errKind: cypher.ErrSemantic},
		{q: "RETURN date.truncate('month', date('2026-03-05'), {day: 2})", errKind: cypher.ErrSemantic},
	})
}

func TestCypherComparesOrdersAndGroupsTemporalValues(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	runAnalytics(t, g, []analyticsCase{
		{q: "WITH date('2025-12-31') AS a, date('2026-01-01') AS b RETURN a < b, a > b, a <= a, a = date('20251231'), a <> b, a = b", want: r(true, false, true, true, true, false)},
		// As text '10:00+08:00' is after '03:00Z'; as instants it is before.
		{q: "WITH datetime('2026-03-05T10:00:00+08:00') AS a, datetime('2026-03-05T03:00:00Z') AS b RETURN a < b, a > b, a = b", want: r(true, false, false)},
		// One instant written at two offsets: not equal, and not the same group.
		{q: "WITH datetime('2026-03-05T10:00:00+08:00') AS a, datetime('2026-03-05T02:00:00Z') AS b RETURN a = b, a <> b, a = datetime('2026-03-05T10:00:00+08:00'), a.epochSeconds = b.epochSeconds", want: r(false, true, true, true)},
		// A date is not a datetime and not a string: unequal, and unordered (null).
		{q: "WITH date('2026-03-05') AS d RETURN d = datetime('2026-03-05'), d < datetime('2026-03-06'), d = '2026-03-05', d < '2026-03-06', d = null, d < null", want: r(false, nil, false, nil, nil, nil)},
		{q: "RETURN date('2026-03-05') IN [date('2026-03-04'), date('2026-03-05')], date('2026-03-05') IN ['2026-03-05']", want: r(true, false)},
		{q: "RETURN duration('P7D') = duration('P1W'), duration('P1D') = duration('PT24H'), duration('P1D') < duration('P2D')", want: r(true, false, nil)},
		// date() and datetime() with no argument are the query's one instant.
		{q: "RETURN date() = date(datetime()), datetime() = datetime(), date() > date('2026-01-01'), date().year >= 2026, datetime().timezone", want: r(true, true, true, true, "Z")},
		{q: "RETURN date('2026-03-05') + 1", errKind: cypher.ErrRuntime},
		{q: "RETURN date('2026-03-05') - date('2026-03-01')", errKind: cypher.ErrRuntime, errHas: "duration.between"},
		{q: "RETURN date('2026-03-05') * 2", errKind: cypher.ErrRuntime},
	})

	// A Go caller may pass a time.Time parameter; it is a datetime.
	at := time.Date(2026, 3, 5, 10, 0, 0, 0, time.FixedZone("", 8*3600))
	res, err := g.QueryCypher(context.Background(), CypherRequest{
		Query:  "RETURN $at, $at.hour, date($at), datetime.truncate('month', $at), $at < datetime('2026-03-05T03:00:00Z')",
		Params: map[string]any{"at": at},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := r("2026-03-05T10:00:00+08:00", int64(10), "2026-03-05", "2026-03-01T00:00:00+08:00", true); !reflect.DeepEqual(res.Rows[0], want) {
		t.Errorf("time.Time parameter\n  got  %#v\n  want %#v", res.Rows[0], want)
	}

	rows := func(q string) []string {
		t.Helper()
		res, err := g.QueryCypher(context.Background(), CypherRequest{Query: q})
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return canonicalRows(res.Rows, true)
	}
	check := func(q string, want ...[]any) {
		t.Helper()
		if got, w := rows(q), canonicalRows(want, true); !reflect.DeepEqual(got, w) {
			t.Errorf("%s\n  got  %v\n  want %v", q, got, w)
		}
	}
	// Dates sort by day, nulls last ascending and first descending.
	check("UNWIND ['2026-03-01', null, '2025-12-31', '2026-01-15', '2025-02-01'] AS s RETURN date(s) AS d ORDER BY d",
		r("2025-02-01"), r("2025-12-31"), r("2026-01-15"), r("2026-03-01"), r(nil))
	check("UNWIND ['2026-03-01', null, '2025-12-31', '2026-01-15'] AS s RETURN date(s) AS d ORDER BY d DESC",
		r(nil), r("2026-03-01"), r("2026-01-15"), r("2025-12-31"))
	// Datetimes sort by instant whatever offset each is written in; two
	// spellings of one instant (02:00Z here) sort by offset.
	check("UNWIND ['2026-03-05T10:00:00+08:00', '2026-03-05T01:00:00Z', '2026-03-05T04:00:00+02:00', '2026-03-04T23:00:00-05:00'] AS s RETURN datetime(s) AS t ORDER BY t",
		r("2026-03-05T01:00:00Z"), r("2026-03-05T04:00:00+02:00"), r("2026-03-05T10:00:00+08:00"), r("2026-03-04T23:00:00-05:00"))
	// Across types: datetimes, then dates, then durations, before strings and numbers.
	check("UNWIND [1, 'a', date('2026-01-01'), null, duration('P1D'), datetime('2027-01-01'), date('2025-01-01')] AS v RETURN v ORDER BY v",
		r("2027-01-01T00:00:00Z"), r("2025-01-01"), r("2026-01-01"), r("P1D"), r("a"), r(1), r(nil))
	check("UNWIND ['P1M', 'P29D', 'PT1H', 'P1Y'] AS s RETURN duration(s) AS d ORDER BY d", r("PT1H"), r("P29D"), r("P1M"), r("P1Y"))
	// Three spellings of one day are one group; DISTINCT agrees.
	check("UNWIND ['2026-03-05', '2026-03-05T10:00:00Z', '20260305', '2026-03-06'] AS s RETURN date(s) AS d, count(*) AS c ORDER BY d",
		r("2026-03-05", 3), r("2026-03-06", 1))
	check("UNWIND ['2026-03-05', '2026-03-05T10:00:00Z', '20260305', '2026-03-06'] AS s RETURN DISTINCT date(s) AS d ORDER BY d DESC",
		r("2026-03-06"), r("2026-03-05"))
	check("UNWIND ['2026-03-05T10:00:00+08:00', '2026-03-05T02:00:00Z', '2026-03-05T02:00:00+00:00'] AS s RETURN datetime(s) AS t, count(*) AS c ORDER BY t",
		r("2026-03-05T02:00:00Z", 2), r("2026-03-05T10:00:00+08:00", 1))
	check("UNWIND ['2026-03-05T10:00:00+08:00', '2026-03-05T02:00:00Z'] AS s RETURN count(DISTINCT datetime(s)), count(DISTINCT date(s)), count(DISTINCT datetime(s).epochSeconds)", r(2, 1, 1))
	check("RETURN date('2026-03-05') AS d UNION RETURN date('20260305') AS d UNION RETURN date('2026-03-06') AS d", r("2026-03-05"), r("2026-03-06"))
	// Week buckets that straddle New Year, with the ISO week-year beside them.
	check(`UNWIND ['2025-12-28', '2025-12-29', '2025-12-31', '2026-01-01', '2026-01-04', '2026-01-05'] AS s
		WITH date(s) AS d
		RETURN date.truncate('week', d) AS wk, d.weekYear AS wy, d.week AS w, count(*) AS c ORDER BY wk`,
		r("2025-12-22", 2025, 52, 1), r("2025-12-29", 2026, 1, 4), r("2026-01-05", 2026, 2, 1))
	check(`UNWIND ['2025-09-30', '2025-10-01', '2025-12-31', '2026-01-01', '2026-03-31'] AS s
		RETURN date.truncate('quarter', date(s)) AS q, count(*) AS c ORDER BY q`,
		r("2025-07-01", 1), r("2025-10-01", 2), r("2026-01-01", 2))
}

func TestCypherDurations(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	runAnalytics(t, g, []analyticsCase{
		{q: "RETURN duration('P1Y2M3DT4H5M6.5S'), duration('P1W'), duration('PT90M'), duration('PT0S'), duration('P14M'), duration('PT-1.5S'), duration('PT0.25S')",
			want: r("P1Y2M3DT4H5M6.5S", "P7D", "PT1H30M", "PT0S", "P1Y2M", "PT-1.5S", "PT0.25S")},
		{q: "RETURN duration({days: 30}), duration({years: 1, months: 2, weeks: 1, hours: 25}), duration({days: -1}), duration({milliseconds: 1500}), duration({quarters: 2})",
			want: r("P30D", "P1Y2M7DT25H", "P-1D", "PT1.5S", "P6M")},
		// 1984-10-11 → 2015-06-24: 30 years 8 months 13 days; 11213 days; 368 months.
		{q: "WITH date('1984-10-11') AS a, date('2015-06-24') AS b RETURN duration.between(a, b), duration.inDays(a, b), duration.inDays(a, b).days, duration.inMonths(a, b), duration.inMonths(a, b).months, duration.between(a, b).years",
			want: r("P30Y8M13D", "P11213D", i64(11213), "P30Y8M", i64(368), i64(30))},
		// Backwards is negative; a month is whole only when the day comes round.
		{q: "RETURN duration.between(date('2026-03-01'), date('2026-01-31')), duration.inDays(date('2026-03-01'), date('2026-01-31')).days, duration.between(date('2026-01-31'), date('2026-02-28')), duration.between(date('2026-01-31'), date('2026-03-01'))",
			want: r("P-1M-1D", i64(-29), "P28D", "P1M1D")},
		// A day is whole only when the time of day comes round.
		{q: "WITH datetime('2026-01-01T12:00:00Z') AS a, datetime('2026-01-03T06:00:00Z') AS b RETURN duration.between(a, b), duration.inDays(a, b).days, duration.inSeconds(a, b), duration.inSeconds(a, b).seconds, duration.inSeconds(a, b).hours, duration.inSeconds(b, a).seconds",
			want: r("P1DT18H", i64(1), "PT42H", i64(151200), i64(42), i64(-151200))},
		// The two sides are read on one clock: 10:00+08:00 is 02:00Z.
		{q: "RETURN duration.inSeconds(datetime('2026-03-05T02:00:00Z'), datetime('2026-03-05T10:00:00+08:00')), duration.inSeconds(datetime('2026-01-01T00:00:00Z'), datetime('2026-01-01T01:00:01.5Z')), duration.between(date('2026-03-05'), datetime('2026-03-06T06:00:00+08:00'))",
			want: r("PT0S", "PT1H1.5S", "P1DT6H")},
		{q: "WITH duration('P1Y2M10DT3H4M5.006S') AS d RETURN d.years, d.months, d.monthsOfYear, d.weeks, d.days, d.hours, d.minutes, d.minutesOfHour, d.seconds, d.secondsOfMinute, d.milliseconds, d.millisecondsOfSecond",
			want: r(i64(1), i64(14), i64(2), i64(1), i64(10), i64(3), i64(184), i64(4), i64(11045), i64(5), i64(11045006), i64(6))},
		// Adding months keeps the day where the month has it, its last day where not.
		{q: "RETURN date('2026-01-31') + duration('P1M'), date('2024-01-31') + duration({months: 1}), date('2026-03-31') - duration('P1M'), date('2025-12-31') + duration('P1D'), date('2026-01-01') - duration('P1W'), duration('P1Y') + date('2026-02-28')",
			want: r("2026-02-28", "2024-02-29", "2026-02-28", "2026-01-01", "2025-12-25", "2027-02-28")},
		// A date has no time of day: hours do not move it. A datetime moves.
		{q: "RETURN date('2026-03-05') + duration('PT36H'), datetime('2026-03-05T10:00:00+08:00') + duration('PT36H'), datetime('2026-03-05T10:00:00Z') - duration('P1MT30M'), duration('P1D') + duration('PT12H'), duration('P1D') - duration('P2D')",
			want: r("2026-03-05", "2026-03-06T22:00:00+08:00", "2026-02-05T09:30:00Z", "P1DT12H", "P-1D")},
		// The usual trailing window.
		{q: "RETURN date('2026-03-05') >= date('2026-03-31') - duration('P30D'), date('2026-02-28') >= date('2026-03-31') - duration('P30D')", want: r(true, false)},

		{q: "RETURN duration('a week')", errKind: cypher.ErrRuntime, errHas: "a week"},
		{q: "RETURN duration('P')", errKind: cypher.ErrRuntime},
		{q: "RETURN duration('P1.5D')", errKind: cypher.ErrRuntime},
		{q: "RETURN duration(5)", errKind: cypher.ErrRuntime},
		{q: "RETURN duration({fortnights: 1})", errKind: cypher.ErrRuntime, errHas: "fortnights"},
		{q: "RETURN duration({days: 1.5})", errKind: cypher.ErrRuntime, errHas: "integer"},
		{q: "RETURN duration.inDays('2026-01-01', date('2026-01-02'))", errKind: cypher.ErrRuntime, errHas: "date(x)"},
		{q: "RETURN duration.between(date('2026-01-01'))", errKind: cypher.ErrSemantic},
		{q: "RETURN duration('P1D').fortnights", errKind: cypher.ErrRuntime},
		{q: "RETURN date('9999-12-31') + duration('P1D')", errKind: cypher.ErrRuntime, errHas: "0000 to 9999"},
		{q: "RETURN duration('P1D') * 2", errKind: cypher.ErrRuntime},
	})
}

// orderFixture is a handful of orders whose dates are written the ways real
// properties are: RFC 3339 with and without an offset, a bare day, absent.
func loadOrderFixture(t testing.TB, g *GraphStore) {
	t.Helper()
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	orders := []struct {
		id      string
		created any
		amount  any
	}{
		{"o1", "2025-12-30T23:30:00Z", "10.5"},
		{"o2", "2025-12-31", 20},
		{"o3", "2026-01-01T00:10:00+08:00", 30}, // 31 Dec in UTC; 1 Jan where it was written
		{"o4", "2026-01-15", 40},
		{"o5", "2026-02-28T12:00:00Z", 50.0},
		{"o6", "2026-03-01", "60"},
		{"o7", nil, 70},
	}
	for _, o := range orders {
		props := map[string]any{"name": o.id, "amount": o.amount}
		if o.created != nil {
			props["created"] = o.created
		}
		if err := g.UpsertNode(ctx, &GraphNode{ID: "order:" + o.id, NodeType: "Order", Properties: props, Vector: []float32{1, 0, 0, 0}}); err != nil {
			t.Fatalf("node %s: %v", o.id, err)
		}
	}
	extra := []*GraphNode{
		{ID: "dirty:1", NodeType: "Dirty", Properties: map[string]any{"name": "d1", "created": "soon"}},
		{ID: "dirty:2", NodeType: "Dirty", Properties: map[string]any{"name": "d2", "created": "2026-01-01"}},
		{ID: "stamped:1", NodeType: "Stamped", Properties: map[string]any{"name": "s1", "created_at": "2020-01-02T03:04:05Z", "updated_at": "2021-01-02"}},
	}
	for _, n := range extra {
		n.Vector = []float32{1, 0, 0, 0}
		if err := g.UpsertNode(ctx, n); err != nil {
			t.Fatalf("node %s: %v", n.ID, err)
		}
	}
	for _, e := range []*GraphEdge{
		{ID: "follows:1", FromNodeID: "order:o1", ToNodeID: "order:o2", EdgeType: "FOLLOWS"},
		{ID: "follows:2", FromNodeID: "order:o2", ToNodeID: "order:o3", EdgeType: "FOLLOWS", Properties: map[string]any{"created_at": "2019-05-06T00:00:00Z"}},
	} {
		if err := g.UpsertEdge(ctx, e); err != nil {
			t.Fatalf("edge %s: %v", e.ID, err)
		}
	}
}

func TestCypherBucketsNodesByMonthOnBothBackends(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadOrderFixture(t, b.store)
			check := func(q string, want ...[]any) {
				t.Helper()
				res, err := b.store.QueryCypher(context.Background(), CypherRequest{Query: q})
				if err != nil {
					t.Fatalf("%s: %v", q, err)
				}
				if got, w := canonicalRows(res.Rows, true), canonicalRows(want, true); !reflect.DeepEqual(got, w) {
					t.Errorf("%s\n  got  %v\n  want %v", q, got, w)
				}
			}
			// Dec: o1 (10.5), o2 (20). Jan: o3 (30, its own offset's date), o4 (40).
			// The order with no date is its own bucket, null, sorted last.
			check(`MATCH (n:Order) RETURN date.truncate('month', date(n.created)) AS m, count(*) AS c, avg(toFloat(n.amount)) AS a ORDER BY m`,
				r("2025-12-01", 2, 15.25), r("2026-01-01", 2, 35.0), r("2026-02-01", 1, 50.0), r("2026-03-01", 1, 60.0), r(nil, 1, 70.0))
			check(`MATCH (n:Order) WHERE n.created IS NOT NULL
				RETURN date.truncate('month', date(n.created)) AS m, count(*) AS c ORDER BY m DESC LIMIT 2`,
				r("2026-03-01", 1), r("2026-02-01", 1))
			check(`MATCH (n:Order) WITH date(n.created) AS d, toFloat(n.amount) AS amt WHERE d IS NOT NULL
				RETURN d.year AS y, d.quarter AS q, count(*) AS c, sum(amt) AS total, percentileCont(amt, 0.5) AS median, round(stDevP(amt), 6) AS sd ORDER BY y, q`,
				r(2025, 4, 2, 30.5, 15.25, 4.75), r(2026, 1, 4, 180.0, 45.0, 11.18034))
			check(`MATCH (n:Order) WHERE n.created IS NOT NULL
				RETURN date.truncate('week', date(n.created)) AS wk, collect(n.name) AS names ORDER BY wk`,
				r("2025-12-29", l("o1", "o2", "o3")), r("2026-01-12", l("o4")), r("2026-02-23", l("o5", "o6")))
			// A filter on the date, not on the text: o3's text sorts after
			// '2026-01-01' and its instant is before it.
			check(`MATCH (n:Order) WHERE datetime(n.created) < datetime('2026-01-01T00:00:00Z') RETURN n.name ORDER BY n.name`, r("o1"), r("o2"), r("o3"))
			check(`MATCH (n:Order) WHERE date(n.created) >= date('2026-01-01') AND date(n.created) < date('2026-03-01') RETURN n.name ORDER BY date(n.created) DESC`, r("o5"), r("o4"), r("o3"))
			check(`MATCH (a:Order {name: 'o1'}), (b:Order {name: 'o6'}) RETURN duration.inDays(date(a.created), date(b.created)).days`, r(61))
			check(`MATCH (n:Order) RETURN percentileDisc(toFloat(n.amount), 0.5), max(date(n.created)), min(datetime(n.created))`, r(40, "2026-03-01", "2025-12-30T23:30:00Z"))

			// One unreadable date fails the query and says which; a guard buckets the rest.
			_, err := b.store.QueryCypher(context.Background(), CypherRequest{Query: "MATCH (n:Dirty) RETURN date(n.created)"})
			var ce *cypher.Error
			if err == nil || !errors.As(err, &ce) || ce.Kind != cypher.ErrRuntime || !strings.Contains(err.Error(), `"soon"`) {
				t.Errorf("want a runtime error naming the text, got %v", err)
			}
			check(`MATCH (n:Dirty) RETURN n.name, CASE WHEN n.created =~ '\\d{4}-\\d{2}-\\d{2}.*' THEN date(n.created) END AS d ORDER BY n.name`,
				r("d1", nil), r("d2", "2026-01-01"))
		})
	}
}

func TestCypherReachesStoredCreationAndUpdateTimes(t *testing.T) {
	rfc3339UTC := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadOrderFixture(t, b.store)
			ctx := context.Background()
			one := func(req CypherRequest) []any {
				t.Helper()
				res, err := b.store.QueryCypher(ctx, req)
				if err != nil {
					t.Fatalf("%s: %v", req.Query, err)
				}
				if len(res.Rows) != 1 {
					t.Fatalf("%s: want one row, got %v", req.Query, res.Rows)
				}
				return res.Rows[0]
			}
			// The columns, as RFC 3339 UTC strings, when the properties have no such key.
			row := one(CypherRequest{Query: "MATCH (n:Order {name: 'o1'})-[r:FOLLOWS]->() RETURN n.created_at, n.updated_at, r.created_at, r.updated_at"})
			for i := 0; i < 3; i++ {
				s, ok := row[i].(string)
				if !ok || !rfc3339UTC.MatchString(s) {
					t.Fatalf("column %d: want an RFC 3339 UTC string, got %#v", i, row[i])
				}
			}
			if row[3] != nil {
				t.Errorf("an edge has no updated_at column: got %#v", row[3])
			}
			created := row[0].(string)
			if b.name == "sqlite" {
				// Written a moment ago, on the same clock the query reads.
				age := one(CypherRequest{Query: "MATCH (n:Order {name: 'o1'}) RETURN duration.inSeconds(datetime(n.created_at), datetime()).seconds"})[0].(int64)
				if age < -5 || age > 300 {
					t.Errorf("created_at is %d seconds old", age)
				}
			}
			// They are values like any other: date(), bucketing, comparison.
			got := one(CypherRequest{Query: "MATCH (n:Order) RETURN count(*), count(n.created_at), count(DISTINCT date.truncate('year', date(n.created_at))), min(n.created_at) <= max(n.updated_at)"})
			if !reflect.DeepEqual(got, r(int64(7), int64(7), int64(1), true)) {
				t.Errorf("bucketing by created_at: %#v", got)
			}
			// A filter on them is decided in Go, on the same string that is returned.
			for _, q := range []string{
				"MATCH (n:Order) WHERE n.created_at = $at RETURN count(*) >= 1",
				"MATCH (n:Order {created_at: $at}) RETURN count(*) >= 1",
				"MATCH (n:Order) WHERE n.created_at IN [$at] RETURN count(*) >= 1",
				"MATCH (n:Order) WHERE n.created_at STARTS WITH left($at, 10) RETURN count(*) >= 1",
				"MATCH (n:Order) WHERE n.updated_at >= $at RETURN count(*) >= 1",
				"MATCH (:Order)-[r:FOLLOWS]->() WHERE r.created_at >= $at RETURN count(*) >= 1",
			} {
				if got := one(CypherRequest{Query: q, Params: map[string]any{"at": created}}); got[0] != true {
					t.Errorf("%s: got %v", q, got)
				}
			}
			// A stored property of that name wins, as with n.content.
			got = one(CypherRequest{Query: "MATCH (n:Stamped) RETURN n.created_at, n.updated_at, date(n.created_at).year"})
			if !reflect.DeepEqual(got, r("2020-01-02T03:04:05Z", "2021-01-02", int64(2020))) {
				t.Errorf("stored created_at: %#v", got)
			}
			got = one(CypherRequest{Query: "MATCH ()-[r:FOLLOWS {created_at: '2019-05-06T00:00:00Z'}]->(b) RETURN r.created_at, b.name"})
			if !reflect.DeepEqual(got, r("2019-05-06T00:00:00Z", "o3")) {
				t.Errorf("stored edge created_at: %#v", got)
			}
			got = one(CypherRequest{Query: "MATCH (n:Stamped) WHERE n.created_at = '2020-01-02T03:04:05Z' RETURN n.name"})
			if !reflect.DeepEqual(got, r("s1")) {
				t.Errorf("filter on stored created_at: %#v", got)
			}
			// They are not properties: keys() and the returned node do not grow.
			got = one(CypherRequest{Query: "MATCH (n:Order {name: 'o7'}) RETURN keys(n), n"})
			if !reflect.DeepEqual(got[0], l("amount", "name")) {
				t.Errorf("keys(n): %#v", got[0])
			}
			if js, _ := json.Marshal(got[1]); strings.Contains(string(js), "created_at") {
				t.Errorf("a returned node should not carry created_at: %s", js)
			}
			// Along a variable-length path and from startNode(), which load rows by id.
			got = one(CypherRequest{Query: "MATCH p = (a:Order {name: 'o1'})-[rs:FOLLOWS*2]->(c) RETURN size([n IN nodes(p) WHERE n.created_at IS NOT NULL]), size([r IN rs WHERE r.created_at IS NOT NULL]), startNode(rs[0]).created_at IS NOT NULL"})
			if !reflect.DeepEqual(got, r(int64(3), int64(2), true)) {
				t.Errorf("created_at along a path: %#v", got)
			}
			// And through the as-of read, which selects from a union with history.
			got = one(CypherRequest{Query: "MATCH (n:Order {name: 'o1'})-[r:FOLLOWS]->() RETURN n.created_at, r.created_at IS NOT NULL", AsOf: time.Now().Add(time.Hour)})
			if !reflect.DeepEqual(got, r(created, true)) {
				t.Errorf("as-of created_at: %#v (live read gave %q)", got, created)
			}
		})
	}
}
