package core

// Aggregation for analysis questions: several measures per group, time
// buckets, range and set filters, percentiles.
//
// aggregations.go answers one aggregation per call in SQL and stays as it is:
// a request that uses none of the options below takes that path and returns
// what it always returned. A request that uses any of them — Measures,
// TimeBucket, CreatedSince / CreatedBefore, Order, or a filter whose value is
// an operator object — is answered here.
//
// The division of work is the same on both backends, which is the point of
// it. SQL selects the rows (collection, the plain equality filters, the
// document exclusions) and projects the handful of metadata fields the request
// names, as text. Everything that would need a second dialect — truncating a
// timestamp, deciding whether "12.5" is a number, a percentile SQLite has no
// function for — happens once, in Go, over that stream. So the two stores
// cannot disagree about what a bucket or a number is.
//
// What it costs: the statement reads every row the plain filters leave, and
// operator filters are applied after the read rather than by an index. Memory
// is one small accumulator per group and field, plus — only when a median or
// percentile is asked for — the field's numeric values, which an exact
// percentile needs in full. Both are capped (maxAggregationGroups,
// maxPercentileValues) and a request past a cap fails by name instead of
// returning an approximation.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// maxAggregationGroups is how many distinct groups one request may
	// produce. A group is a few hundred bytes, so this is tens of megabytes
	// at the outside; a group_by on a field that is unique per row is the
	// usual way to reach it, and is not a question this API answers.
	maxAggregationGroups = 100_000

	// maxPercentileValues is how many numeric values one request may hold
	// for its median / percentile measures, summed over every group and
	// field: 2,000,000 float64s, 16 MB. The percentile is exact — the values
	// are sorted and the rank interpolated — so they have to be held; past
	// this the request is refused and the caller narrows it with filters.
	// count, sum, avg, min, max and stddev are streamed and hold nothing.
	maxPercentileValues = 2_000_000
)

// The measure types. A percentile may also be written pNN ("p95", "p99.9").
const (
	MeasureCount      = "count"
	MeasureSum        = "sum"
	MeasureAvg        = "avg"
	MeasureMin        = "min"
	MeasureMax        = "max"
	MeasureMedian     = "median"
	MeasurePercentile = "percentile"
	// MeasureStddev is the sample standard deviation (n-1), null under two
	// values; MeasureStddevPop is the population one (n).
	MeasureStddev    = "stddev"
	MeasureStddevPop = "stddev_pop"
)

// The time bucket units.
const (
	BucketDay   = "day"
	BucketWeek  = "week"
	BucketMonth = "month"
	BucketYear  = "year"
)

// AggregationMeasure is one value computed per group.
//
// Every type but count reads Field as a number. A value is a number when it
// is a JSON number or a JSON string that is a decimal number in full ("12.5",
// " 3 ", "1e3"); anything else that is present ("n/a", "12ms", "") is left out
// of the measure and counted in the result's Skipped under the measure's
// alias. Nothing is read as zero. A measure over no numeric value is null.
type AggregationMeasure struct {
	// Type is count, sum, avg, min, max, median, percentile, stddev,
	// stddev_pop, or pNN as a short form of percentile.
	Type string `json:"type"`
	// Field is the metadata field. Required except for count, where it is
	// optional: count without a field is the group's rows, count with one is
	// the rows that carry the field.
	Field string `json:"field,omitempty"`
	// Alias names the value in the result. Defaults to type_field
	// ("avg_score", "p95_latency"), or "count".
	Alias string `json:"alias,omitempty"`
	// Percentile is the rank for type percentile, above 0 and up to 100.
	// Linear interpolation between the two nearest values, as PostgreSQL's
	// percentile_cont and numpy's default do.
	Percentile float64 `json:"percentile,omitempty"`
}

// AggregationTimeBucket groups rows by a timestamp truncated to a unit.
type AggregationTimeBucket struct {
	// Unit is day, week, month or year.
	Unit string `json:"unit"`
	// Field is a metadata field holding an RFC 3339 timestamp or a date.
	// Empty means the row's own created-at time.
	Field string `json:"field,omitempty"`
	// Alias is the group key the bucket is reported under. Defaults to Unit.
	Alias string `json:"alias,omitempty"`
	// Timezone is an IANA zone name the timestamp is truncated in. Defaults
	// to UTC. A timestamp without a zone is read as UTC.
	Timezone string `json:"timezone,omitempty"`
}

// usesMeasurePath says whether a request asks for anything aggregations.go
// does not do.
func (req AggregationRequest) usesMeasurePath() bool {
	if len(req.Measures) > 0 || req.TimeBucket != nil || req.CreatedSince != "" || req.CreatedBefore != "" || req.Order != "" {
		return true
	}
	for _, v := range req.Filters {
		if _, ok := v.(map[string]interface{}); ok {
			return true
		}
	}
	return false
}

// plainFilters is the request with only the filters SQL applies: the plain
// equality ones, exactly as aggregations.go applies them.
func (req AggregationRequest) plainFilters() AggregationRequest {
	out := req
	out.Filters = make(map[string]interface{}, len(req.Filters))
	for k, v := range req.Filters {
		if _, ok := v.(map[string]interface{}); !ok {
			out.Filters[k] = v
		}
	}
	return out
}

// --- the plan ---------------------------------------------------------------

type operandKind uint8

const (
	operandText operandKind = iota
	operandNumber
	operandTime
)

// filterOperand is one side of a comparison, with the kind decided by what
// the caller wrote and never by what a row holds.
type filterOperand struct {
	kind operandKind
	num  float64
	at   time.Time
	text string
}

type filterTest struct {
	field    int // index into measurePlan.fields
	op       string
	operands []filterOperand // one, or several for in
	exists   bool
}

type measureSpec struct {
	kind       string // a Measure* constant, pNN already resolved
	alias      string
	field      int // index into measurePlan.fields, -1 for count without a field
	percentile float64
}

// measurePlan is a request checked and resolved: which metadata fields to
// project, and what to do with each row's values.
type measurePlan struct {
	fields []string // metadata fields, in projection order

	groupFields []int // indexes into fields, one per GroupBy entry
	groupNames  []string

	bucket      *AggregationTimeBucket
	bucketField int // index into fields, -1 for the row's created-at
	bucketAlias string
	bucketZone  *time.Location

	filters []filterTest
	since   *time.Time
	before  *time.Time

	measures   []measureSpec
	keepValues []bool // per field: a percentile needs its values held

	orderBy   string
	ascending bool
	limit     int
}

func (p *measurePlan) fieldIndex(name string) int {
	for i, f := range p.fields {
		if f == name {
			return i
		}
	}
	p.fields = append(p.fields, name)
	p.keepValues = append(p.keepValues, false)
	return len(p.fields) - 1
}

// compileMeasurePlan checks every name and option of a measure-path request.
// Field names are held to the same two rules as the rest of the request
// (validateMetadataIdentifier, validateSQLIdentifier): on SQLite a projected
// field still lands inside a JSON path in the statement text.
func compileMeasurePlan(req AggregationRequest) (*measurePlan, error) {
	if len(req.Having) > 0 {
		return nil, fmt.Errorf("having is not supported together with measures, time_bucket, created_since, created_before, order or operator filters")
	}
	p := &measurePlan{bucketField: -1, limit: req.Limit}

	taken := map[string]bool{}
	for _, g := range req.GroupBy {
		if taken[g] {
			return nil, fmt.Errorf("group_by names the same field twice")
		}
		taken[g] = true
		p.groupFields = append(p.groupFields, p.fieldIndex(g))
		p.groupNames = append(p.groupNames, g)
	}

	if b := req.TimeBucket; b != nil {
		switch b.Unit {
		case BucketDay, BucketWeek, BucketMonth, BucketYear:
		default:
			return nil, fmt.Errorf("time_bucket unit must be one of day, week, month, year")
		}
		p.bucket = b
		p.bucketAlias = b.Unit
		if b.Alias != "" {
			if err := validateSQLIdentifier("time_bucket alias", b.Alias); err != nil {
				return nil, err
			}
			p.bucketAlias = b.Alias
		}
		if taken[p.bucketAlias] {
			return nil, fmt.Errorf("time_bucket alias is also a group_by field; give the bucket another alias")
		}
		taken[p.bucketAlias] = true
		if b.Field != "" {
			if err := validateMetadataIdentifier("time_bucket field", b.Field); err != nil {
				return nil, err
			}
			p.bucketField = p.fieldIndex(b.Field)
		}
		p.bucketZone = time.UTC
		if b.Timezone != "" {
			zone, err := time.LoadLocation(b.Timezone)
			if err != nil {
				return nil, fmt.Errorf("time_bucket timezone is not an IANA zone name this system knows")
			}
			p.bucketZone = zone
		}
	}

	for name, bound := range map[string]string{"created_since": req.CreatedSince, "created_before": req.CreatedBefore} {
		if bound == "" {
			continue
		}
		at, ok := parseTimestamp(bound)
		if !ok {
			return nil, fmt.Errorf("%s must be an RFC 3339 timestamp or a YYYY-MM-DD date", name)
		}
		if name == "created_since" {
			p.since = &at
		} else {
			p.before = &at
		}
	}

	// Sorted, so the same request always compiles to the same plan.
	filterKeys := make([]string, 0, len(req.Filters))
	for k := range req.Filters {
		filterKeys = append(filterKeys, k)
	}
	sort.Strings(filterKeys)
	for _, key := range filterKeys {
		ops, ok := req.Filters[key].(map[string]interface{})
		if !ok {
			continue
		}
		tests, err := compileFilterOps(p.fieldIndex(key), ops)
		if err != nil {
			return nil, err
		}
		p.filters = append(p.filters, tests...)
	}

	measures := req.Measures
	if len(measures) == 0 {
		// The single-aggregation spelling, carried over.
		switch req.Type {
		case "", AggregationCount, AggregationGroupBy:
			// No type beside a bucket or a bound is a count of each group.
			measures = []AggregationMeasure{{Type: MeasureCount}}
		case AggregationSum, AggregationAvg, AggregationMin, AggregationMax:
			measures = []AggregationMeasure{{Type: string(req.Type), Field: req.Field}}
		default:
			return nil, fmt.Errorf("unsupported aggregation type: %s", req.Type)
		}
	}
	aliases := map[string]bool{}
	for _, m := range measures {
		spec, err := compileMeasure(p, m)
		if err != nil {
			return nil, err
		}
		if aliases[spec.alias] {
			return nil, fmt.Errorf("two measures share an alias; give each its own")
		}
		if taken[spec.alias] {
			return nil, fmt.Errorf("a measure alias is also a group key; give the measure another alias")
		}
		aliases[spec.alias] = true
		p.measures = append(p.measures, spec)
	}

	switch req.Order {
	case "", "desc":
	case "asc":
		p.ascending = true
	default:
		return nil, fmt.Errorf("order must be asc or desc")
	}
	p.orderBy = req.OrderBy
	if p.orderBy != "" && p.orderBy != "count" && p.orderBy != "agg_value" && !aliases[p.orderBy] && !taken[p.orderBy] {
		return nil, fmt.Errorf("order_by must be count, agg_value, a measure alias or a group key")
	}
	if p.orderBy == "" && req.Order == "" && p.bucket != nil {
		// A time series reads oldest first unless told otherwise.
		p.ascending = true
	}
	return p, nil
}

func compileMeasure(p *measurePlan, m AggregationMeasure) (measureSpec, error) {
	spec := measureSpec{kind: m.Type, field: -1, percentile: m.Percentile}
	label := m.Type
	switch m.Type {
	case MeasureCount, MeasureSum, MeasureAvg, MeasureMin, MeasureMax, MeasureStddev, MeasureStddevPop:
	case MeasureMedian:
		spec.percentile = 50
	case MeasurePercentile:
		label = "p" + strings.ReplaceAll(strconv.FormatFloat(m.Percentile, 'f', -1, 64), ".", "_")
	default:
		// pNN: a spelling of percentile, read as a number and nothing else.
		rank, err := strconv.ParseFloat(strings.TrimPrefix(m.Type, "p"), 64)
		if !strings.HasPrefix(m.Type, "p") || err != nil {
			return spec, fmt.Errorf("measure type must be one of count, sum, avg, min, max, median, percentile, stddev, stddev_pop, or pNN such as p95")
		}
		spec.kind, spec.percentile = MeasurePercentile, rank
		label = "p" + strings.ReplaceAll(strconv.FormatFloat(rank, 'f', -1, 64), ".", "_")
	}
	if spec.kind == MeasureMedian {
		spec.kind = MeasurePercentile
	}
	if spec.kind == MeasurePercentile && !(spec.percentile > 0 && spec.percentile <= 100) {
		return spec, fmt.Errorf("a percentile must be above 0 and at most 100")
	}

	if m.Field != "" {
		if err := validateMetadataIdentifier("measure field", m.Field); err != nil {
			return spec, err
		}
		spec.field = p.fieldIndex(m.Field)
		if spec.kind == MeasurePercentile {
			p.keepValues[spec.field] = true
		}
	} else if spec.kind != MeasureCount {
		return spec, fmt.Errorf("field is required for a %s measure", m.Type)
	}

	spec.alias = m.Alias
	if spec.alias == "" {
		spec.alias = label
		if m.Field != "" {
			spec.alias = label + "_" + strings.NewReplacer(".", "_", "-", "_").Replace(m.Field)
		}
	}
	if err := validateSQLIdentifier("measure alias", spec.alias); err != nil {
		return spec, err
	}
	return spec, nil
}

// compileFilterOps reads one operator object: {"gte": 0.5, "lt": 1}.
//
// The spelling is the one the composable query API (cortex_query) and the
// object-set predicates already use — eq, neq, in, gt, gte, lt, lte — with ne
// accepted for neq, and exists. Several operators on one field all have to
// hold.
//
// What an ordering compares is decided by the bound: a number, or a string
// that is a number, compares numerically and a row whose value is not a
// number does not match; a string that is a timestamp or date compares as a
// time; any other string compares as text. eq, neq and in compare numerically
// for a number and as text for a string. A row without the field matches
// nothing except exists: false.
func compileFilterOps(field int, ops map[string]interface{}) ([]filterTest, error) {
	if len(ops) == 0 {
		return nil, fmt.Errorf("a filter operator object is empty; use one of eq, neq, gt, gte, lt, lte, in, exists")
	}
	names := make([]string, 0, len(ops))
	for name := range ops {
		names = append(names, name)
	}
	sort.Strings(names)

	var tests []filterTest
	for _, name := range names {
		raw := ops[name]
		test := filterTest{field: field, op: name}
		switch name {
		case "exists":
			want, ok := raw.(bool)
			if !ok {
				return nil, fmt.Errorf("the exists filter operator takes true or false")
			}
			test.exists = want
		case "in":
			list, ok := raw.([]interface{})
			if !ok || len(list) == 0 {
				return nil, fmt.Errorf("the in filter operator takes a non-empty list of values")
			}
			for _, item := range list {
				operand, err := equalityOperand(item)
				if err != nil {
					return nil, err
				}
				test.operands = append(test.operands, operand)
			}
		case "ne":
			test.op = "neq"
			fallthrough
		case "eq", "neq":
			operand, err := equalityOperand(raw)
			if err != nil {
				return nil, err
			}
			test.operands = []filterOperand{operand}
		case "gt", "gte", "lt", "lte":
			operand, err := orderingOperand(raw)
			if err != nil {
				return nil, err
			}
			test.operands = []filterOperand{operand}
		default:
			return nil, fmt.Errorf("unknown filter operator; use one of eq, neq, gt, gte, lt, lte, in, exists")
		}
		tests = append(tests, test)
	}
	return tests, nil
}

func equalityOperand(v interface{}) (filterOperand, error) {
	if n, ok := operandNumberOf(v); ok {
		return filterOperand{kind: operandNumber, num: n}, nil
	}
	switch s := v.(type) {
	case string:
		return filterOperand{kind: operandText, text: s}, nil
	case bool:
		return filterOperand{kind: operandText, text: strconv.FormatBool(s)}, nil
	}
	return filterOperand{}, fmt.Errorf("a filter operator compares against a string, a number or a boolean")
}

func orderingOperand(v interface{}) (filterOperand, error) {
	if n, ok := operandNumberOf(v); ok {
		return filterOperand{kind: operandNumber, num: n}, nil
	}
	s, ok := v.(string)
	if !ok {
		return filterOperand{}, fmt.Errorf("gt, gte, lt and lte compare against a number or a string")
	}
	if n, ok := parseStrictNumber(s); ok {
		return filterOperand{kind: operandNumber, num: n}, nil
	}
	if at, ok := parseTimestamp(s); ok {
		return filterOperand{kind: operandTime, at: at}, nil
	}
	return filterOperand{kind: operandText, text: s}, nil
}

func operandNumberOf(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func (t filterTest) matches(value sql.NullString) bool {
	if t.op == "exists" {
		return value.Valid == t.exists
	}
	if !value.Valid {
		return false
	}
	switch t.op {
	case "in":
		for _, operand := range t.operands {
			if c, ok := operand.compare(value.String); ok && c == 0 {
				return true
			}
		}
		return false
	case "neq":
		c, ok := t.operands[0].compare(value.String)
		return !ok || c != 0
	}
	c, ok := t.operands[0].compare(value.String)
	if !ok {
		return false
	}
	switch t.op {
	case "eq":
		return c == 0
	case "gt":
		return c > 0
	case "gte":
		return c >= 0
	case "lt":
		return c < 0
	default: // lte
		return c <= 0
	}
}

// compare orders a row's value against the operand: negative when the value
// is smaller. ok is false when the value is not of the operand's kind.
func (o filterOperand) compare(value string) (int, bool) {
	switch o.kind {
	case operandNumber:
		n, ok := parseStrictNumber(value)
		if !ok {
			return 0, false
		}
		switch {
		case n < o.num:
			return -1, true
		case n > o.num:
			return 1, true
		}
		return 0, true
	case operandTime:
		at, ok := parseTimestamp(value)
		if !ok {
			return 0, false
		}
		return at.Compare(o.at), true
	}
	return strings.Compare(value, o.text), true
}

// --- reading values ---------------------------------------------------------

// parseStrictNumber reads a decimal number that is the whole of the text,
// surrounding whitespace aside. "12.5", "-3", "1e3" are numbers; "12ms",
// "n/a", "", "0x10", "Inf" and "NaN" are not.
func parseStrictNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9', c == '+', c == '-', c == '.', c == 'e', c == 'E':
		default:
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// timestampLayouts are the shapes a timestamp is read in: RFC 3339, the same
// without a zone, the space-separated form SQLite's CURRENT_TIMESTAMP writes,
// and a bare date. Go reads fractional seconds whether or not the layout
// shows them. A value with no zone is UTC.
var timestampLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseTimestamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range timestampLayouts {
		if at, err := time.Parse(layout, s); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// rowTimestamp reads a created_at column, which arrives as a time from a
// driver that knows the column's type and as text from one that does not.
func rowTimestamp(v interface{}) (time.Time, bool) {
	switch at := v.(type) {
	case time.Time:
		return at, !at.IsZero()
	case string:
		return parseTimestamp(at)
	case []byte:
		return parseTimestamp(string(at))
	}
	return time.Time{}, false
}

// bucketOf truncates a time to the unit in the zone and writes it as a
// string that sorts in time order: 2026-10-03 for a day, the Monday's date
// for a week (ISO 8601 weeks), 2026-10 for a month, 2026 for a year.
func bucketOf(at time.Time, unit string, zone *time.Location) string {
	at = at.In(zone)
	switch unit {
	case BucketYear:
		return at.Format("2006")
	case BucketMonth:
		return at.Format("2006-01")
	case BucketWeek:
		back := (int(at.Weekday()) + 6) % 7 // Monday is 0
		return at.AddDate(0, 0, -back).Format("2006-01-02")
	}
	return at.Format("2006-01-02")
}

// --- accumulating -----------------------------------------------------------

// fieldStats is what one group knows about one field. Mean and spread are
// kept by Welford's update, so a long column does not lose precision to a
// running sum of squares.
type fieldStats struct {
	present int // rows that carry the field at all
	skipped int // of those, the ones whose value is not a number
	n       int
	sum     float64
	min     float64
	max     float64
	mean    float64
	m2      float64
	values  []float64 // held only for a percentile
	sorted  bool
}

func (f *fieldStats) add(v float64, keep bool) {
	if f.n == 0 || v < f.min {
		f.min = v
	}
	if f.n == 0 || v > f.max {
		f.max = v
	}
	f.n++
	f.sum += v
	delta := v - f.mean
	f.mean += delta / float64(f.n)
	f.m2 += delta * (v - f.mean)
	if keep {
		f.values = append(f.values, v)
	}
}

// percentile is the exact rank by linear interpolation over the sorted
// values: rank p/100*(n-1), between the two values it falls between.
func (f *fieldStats) percentile(p float64) interface{} {
	if len(f.values) == 0 {
		return nil
	}
	if !f.sorted {
		sort.Float64s(f.values)
		f.sorted = true
	}
	rank := p / 100 * float64(len(f.values)-1)
	low := int(math.Floor(rank))
	high := int(math.Ceil(rank))
	if low == high {
		return f.values[low]
	}
	return f.values[low] + (rank-float64(low))*(f.values[high]-f.values[low])
}

type measureGroup struct {
	keys  []interface{} // bucket first when there is one, then the group_by fields
	count int
	stats []fieldStats
}

// measureAggregator consumes the projected rows and holds the groups.
type measureAggregator struct {
	plan   *measurePlan
	groups map[string]*measureGroup
	order  []*measureGroup
	held   int // values held for percentiles, over all groups
}

func newMeasureAggregator(plan *measurePlan) *measureAggregator {
	return &measureAggregator{plan: plan, groups: map[string]*measureGroup{}}
}

// add takes one row: its created-at column and the projected fields as text,
// in plan.fields order.
func (a *measureAggregator) add(createdAt interface{}, values []sql.NullString) error {
	p := a.plan
	for _, test := range p.filters {
		if !test.matches(values[test.field]) {
			return nil
		}
	}

	var created time.Time
	var hasCreated bool
	if p.since != nil || p.before != nil || (p.bucket != nil && p.bucketField < 0) {
		created, hasCreated = rowTimestamp(createdAt)
	}
	if p.since != nil && (!hasCreated || created.Before(*p.since)) {
		return nil
	}
	if p.before != nil && (!hasCreated || !created.Before(*p.before)) {
		return nil
	}

	// The key is every group value with its presence, so a missing field and
	// an empty string are two groups, as they are in SQL.
	var key strings.Builder
	keys := make([]interface{}, 0, len(p.groupFields)+1)
	if p.bucket != nil {
		at, ok := created, hasCreated
		if p.bucketField >= 0 {
			at, ok = time.Time{}, false
			if v := values[p.bucketField]; v.Valid {
				at, ok = parseTimestamp(v.String)
			}
		}
		if ok {
			bucket := bucketOf(at, p.bucket.Unit, p.bucketZone)
			keys = append(keys, bucket)
			key.WriteString("1" + bucket)
		} else {
			// No timestamp, or one that cannot be read: its own group, with a
			// null key, rather than a row that vanishes from the totals.
			keys = append(keys, nil)
			key.WriteString("0")
		}
		key.WriteByte(0)
	}
	for _, idx := range p.groupFields {
		if v := values[idx]; v.Valid {
			// Numbers come back as numbers, as the single-aggregation path
			// returns them.
			if num, ok := parseStrictNumber(v.String); ok {
				keys = append(keys, num)
			} else {
				keys = append(keys, v.String)
			}
			key.WriteString("1" + v.String)
		} else {
			keys = append(keys, nil)
			key.WriteString("0")
		}
		key.WriteByte(0)
	}

	group, ok := a.groups[key.String()]
	if !ok {
		if len(a.groups) >= maxAggregationGroups {
			return fmt.Errorf("the request produces more than %d groups; group by a field with fewer values or narrow it with filters", maxAggregationGroups)
		}
		group = &measureGroup{keys: keys, stats: make([]fieldStats, len(p.fields))}
		a.groups[key.String()] = group
		a.order = append(a.order, group)
	}
	group.count++

	for i, v := range values {
		if !v.Valid {
			continue
		}
		stats := &group.stats[i]
		stats.present++
		num, isNumber := parseStrictNumber(v.String)
		if !isNumber {
			stats.skipped++
			continue
		}
		if p.keepValues[i] {
			if a.held >= maxPercentileValues {
				return fmt.Errorf("a median or percentile here would hold more than %d values; narrow the request with filters", maxPercentileValues)
			}
			a.held++
		}
		stats.add(num, p.keepValues[i])
	}
	return nil
}

func (a *measureAggregator) results() []AggregationResult {
	p := a.plan
	groups := a.order
	if len(groups) == 0 && p.bucket == nil && len(p.groupFields) == 0 {
		// Ungrouped, nothing matched: one row saying so, as a COUNT over no
		// rows is still an answer.
		groups = []*measureGroup{{stats: make([]fieldStats, len(p.fields))}}
	}

	type row struct {
		group  *measureGroup
		result AggregationResult
	}
	rows := make([]row, 0, len(groups))
	for _, g := range groups {
		res := AggregationResult{Count: g.count, Values: make(map[string]interface{}, len(p.measures))}
		if len(g.keys) > 0 {
			res.GroupKeys = make(map[string]interface{}, len(g.keys))
			names := p.groupNames
			if p.bucket != nil {
				names = append([]string{p.bucketAlias}, names...)
			}
			for i, name := range names {
				res.GroupKeys[name] = g.keys[i]
			}
		}
		for i, m := range p.measures {
			value := measureValue(m, g)
			res.Values[m.alias] = value
			if i == 0 {
				res.Value = value
			}
			if m.field >= 0 && m.kind != MeasureCount && g.stats[m.field].skipped > 0 {
				if res.Skipped == nil {
					res.Skipped = map[string]int{}
				}
				res.Skipped[m.alias] = g.stats[m.field].skipped
			}
		}
		rows = append(rows, row{g, res})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		c := 0
		switch {
		case p.orderBy == "count", p.orderBy == "" && p.bucket == nil:
			c = compareSortValues(rows[i].result.Count, rows[j].result.Count)
		case p.orderBy == "agg_value":
			c = compareSortValues(rows[i].result.Value, rows[j].result.Value)
		case p.orderBy != "":
			if v, ok := rows[i].result.Values[p.orderBy]; ok {
				c = compareSortValues(v, rows[j].result.Values[p.orderBy])
			} else {
				c = compareSortValues(rows[i].result.GroupKeys[p.orderBy], rows[j].result.GroupKeys[p.orderBy])
			}
		}
		if c != 0 {
			if p.ascending {
				return c < 0
			}
			return c > 0
		}
		// Ties, and the default order of a time series: by the group keys.
		for k := range rows[i].group.keys {
			if c := compareSortValues(rows[i].group.keys[k], rows[j].group.keys[k]); c != 0 {
				if p.orderBy == "" && p.bucket != nil && !p.ascending {
					return c > 0
				}
				return c < 0
			}
		}
		return false
	})

	out := make([]AggregationResult, 0, len(rows))
	for _, r := range rows {
		if p.limit > 0 && len(out) == p.limit {
			break
		}
		out = append(out, r.result)
	}
	return out
}

func measureValue(m measureSpec, g *measureGroup) interface{} {
	if m.field < 0 {
		return g.count
	}
	stats := &g.stats[m.field]
	if m.kind == MeasureCount {
		return stats.present
	}
	if stats.n == 0 {
		return nil
	}
	switch m.kind {
	case MeasureSum:
		return stats.sum
	case MeasureAvg:
		return stats.mean
	case MeasureMin:
		return stats.min
	case MeasureMax:
		return stats.max
	case MeasureStddevPop:
		return math.Sqrt(stats.m2 / float64(stats.n))
	case MeasureStddev:
		if stats.n < 2 {
			return nil
		}
		return math.Sqrt(stats.m2 / float64(stats.n-1))
	}
	return stats.percentile(m.percentile)
}

// compareSortValues orders what a result can hold: null first, then numbers,
// then text.
func compareSortValues(a, b interface{}) int {
	rank := func(v interface{}) (int, float64, string) {
		switch x := v.(type) {
		case nil:
			return 0, 0, ""
		case int:
			return 1, float64(x), ""
		case float64:
			return 1, x, ""
		case string:
			return 2, 0, x
		}
		return 3, 0, fmt.Sprint(v)
	}
	ra, na, sa := rank(a)
	rb, nb, sb := rank(b)
	switch {
	case ra != rb:
		return ra - rb
	case na < nb:
		return -1
	case na > nb:
		return 1
	}
	return strings.Compare(sa, sb)
}

// consumeMeasureRows runs a projection's rows through the aggregator: the
// created-at column first, then one text column per plan field.
func consumeMeasureRows(rows *sql.Rows, req AggregationRequest, plan *measurePlan) (*AggregationResponse, error) {
	agg := newMeasureAggregator(plan)
	var createdAt interface{}
	values := make([]sql.NullString, len(plan.fields))
	dest := make([]interface{}, 0, len(values)+1)
	dest = append(dest, &createdAt)
	for i := range values {
		dest = append(dest, &values[i])
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		if err := agg.add(createdAt, values); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	results := agg.results()
	return &AggregationResponse{Request: req, Results: results, Total: len(results)}, nil
}

// aggregateMeasures is the measure path on SQLite. A JSON true or false is
// projected as that word, as PostgreSQL's ->> writes it, and everything else
// as its text.
func (s *SQLiteStore) aggregateMeasures(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	plan, err := compileMeasurePlan(req)
	if err != nil {
		return nil, err
	}

	query := "SELECT created_at"
	for _, field := range plan.fields {
		// Every name in plan.fields has passed validateMetadataIdentifier.
		query += fmt.Sprintf(", CASE json_type(metadata, '$.%[1]s') WHEN 'true' THEN 'true' WHEN 'false' THEN 'false' ELSE CAST(json_extract(metadata, '$.%[1]s') AS TEXT) END", field)
	}
	query += " FROM embeddings WHERE 1=1"
	args := []interface{}{}
	if req.Collection != "" {
		query += ` AND collection_id = (SELECT id FROM collections WHERE name = ?)`
		args = append(args, req.Collection)
	}
	query, args = addAggregateFilters(query, args, req.plainFilters())

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return consumeMeasureRows(rows, req, plan)
}

// aggregateMeasures is the measure path on PostgreSQL: the same projection,
// with the field names bound.
func (s *PostgresStore) aggregateMeasures(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	plan, err := compileMeasurePlan(req)
	if err != nil {
		return nil, err
	}

	args := &pgArgs{}
	sel := []string{"created_at"}
	for _, field := range plan.fields {
		sel = append(sel, pgMetadataText(field, args))
	}
	where := append([]string{"1=1"}, pgAggregateWhere(req.plainFilters(), args)...)
	q := "SELECT " + strings.Join(sel, ", ") + " FROM embeddings WHERE " + strings.Join(where, " AND ")

	rows, err := s.db.QueryContext(ctx, q, args.vals...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return consumeMeasureRows(rows, req, plan)
}
