package liveview

// The analysis page, at /analysis: numbers about the brain, drawn as charts.
//
// The other pages show what is in the brain and what was done to it, one
// record at a time. None of them says how much there is or which way it is
// going, and those are the first questions the owner of a brain has. This page
// answers them in two parts.
//
//   - /api/analysis/overview — a fixed set of charts that needs no
//     configuration: memories written, documents added, recalls and how many
//     came back empty, recall latency, writes by kind, and the graph's nodes
//     and edges by type.
//   - /api/analysis/fields and /api/analysis/query — a small builder over
//     aggregate_metadata: a measure of a metadata field, grouped by another
//     field, a time bucket, or both.
//
// Nothing here is a new question to the brain. Every number comes through a
// door another page already uses — [Source.Call] for the tools on
// [AnalysisTools] and the recall log, [Source.ChangeLog] for the feed,
// [Source.Ontology] for the type counts — so a shared brain answers the same
// way a local file does, and a Source a host built itself gets exactly the
// charts its hooks can fill. A chart whose hook is nil, or whose tool the
// brain refuses, says so in its own place; the others are still drawn.
//
// What each chart can and cannot see is reported with it, because every one of
// them is a bounded read:
//
//   - Memories are a table aggregate_metadata does not reach, so they are
//     counted from memory_list_all, newest first, up to analysisMemoryPages
//     pages. A brain with more than that says how far back the count goes.
//   - Recalls come from the recall log, which is off unless the brain was
//     started with it, and which keeps a retention window.
//   - Writes come from the change feed, which keeps seven days by default.
//   - Documents are counted as chunks whose chunk_index is 0, one per
//     document, because aggregate_metadata counts chunk rows.
//
// Every value a caller sends is checked here before it is put into a request:
// names against the identifier shape the aggregation itself enforces, units
// and measures against fixed lists, dates as dates. Nothing a caller typed
// reaches SQL from this file; the aggregation binds or refuses it again.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// AnalysisTools are the tools the analysis page may run beside [ExploreTools]
// and [RecallTools]. All of them read.
var AnalysisTools = []string{
	"aggregate_metadata",
	"knowledge_get",
	"memory_list_all",
}

const (
	// analysisTimeout bounds one answer. The overview reads several things at
	// once and each is paged, so it gets a little longer than one question.
	analysisTimeout = 30 * time.Second

	// The scans behind the overview. Each is newest first and stops at the
	// start of the range asked for, at the end of what the brain holds, or
	// here, whichever comes first.
	analysisMemoryPages = 20 // of 500 memories
	analysisMemoryPage  = 500
	analysisRecallPages = 25 // of 200 recalls
	analysisRecallPage  = 200
	analysisChangePages = 20 // of 1000 events
	analysisChangePage  = 1000

	// analysisScanTTL is how long a scan is reused. Switching the range, or a
	// second reader opening the page, does not page through the brain again.
	analysisScanTTL = 30 * time.Second

	// analysisMaxDays is the longest range that can be asked for by number;
	// 0 asks for everything.
	analysisMaxDays = 730

	// analysisBucketRows bounds the documents series and analysisQueryRows a
	// builder answer: the groups one chart or table is sent.
	analysisBucketRows = 5000
	analysisQueryRows  = 2000

	// analysisTypeBars is how many types a bar list names before the rest
	// become one "other" bar.
	analysisTypeBars = 8

	// The field sample: up to analysisFieldDocs documents, picked across the
	// months of up to analysisFieldGroups (month, document) groups.
	analysisFieldDocs     = 40
	analysisFieldGroups   = 400
	analysisFieldExamples = 6
	analysisFieldLimit    = 60

	// The aliases the builder's requests use. A metadata field of the same
	// name cannot be grouped by in the same request, and is refused by name.
	analysisBucketAlias = "cx_bucket"
	analysisValueAlias  = "cx_value"
	analysisRowsAlias   = "cx_rows"
)

// analysisName is the shape of a metadata field name the page may send: the
// aggregation's own rule (a letter or underscore, then letters, digits,
// underscore, dot or hyphen), with a length.
var analysisName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

var analysisMeasures = map[string]bool{
	"count": true, "sum": true, "avg": true, "median": true, "p95": true, "min": true, "max": true,
}

var analysisUnits = map[string]bool{
	core.BucketDay: true, core.BucketWeek: true, core.BucketMonth: true, core.BucketYear: true,
}

// --- the range ---------------------------------------------------------------

// analysisRange is the span the overview's time charts cover, and the unit
// they are bucketed in.
type analysisRange struct {
	days int // 0 is everything
	unit string
	loc  *time.Location
	now  time.Time
	// since is the start of the first bucket; zero when days is 0.
	since time.Time
}

// newAnalysisRange picks the unit from the length — days up to a month, weeks
// up to four months, months past that — and starts the range on a bucket
// boundary, so the first bar is a whole day, week or month like the others.
func newAnalysisRange(days int, loc *time.Location, now time.Time) analysisRange {
	r := analysisRange{days: days, loc: loc, now: now.In(loc)}
	switch {
	case days == 0 || days > 120:
		r.unit = core.BucketMonth
	case days > 31:
		r.unit = core.BucketWeek
	default:
		r.unit = core.BucketDay
	}
	if days > 0 {
		y, m, d := r.now.Date()
		first := time.Date(y, m, d-(days-1), 0, 0, 0, 0, loc)
		r.since = analysisBucketStart(first, r.unit, loc)
	}
	return r
}

func (r analysisRange) includes(at time.Time) bool {
	return r.since.IsZero() || !at.Before(r.since)
}

func (r analysisRange) key(at time.Time) string { return analysisBucketKey(at, r.unit, r.loc) }

// analysisBucketStart truncates an instant to its day, week (Monday), month
// or year in a zone.
func analysisBucketStart(at time.Time, unit string, loc *time.Location) time.Time {
	at = at.In(loc)
	y, m, d := at.Date()
	switch unit {
	case core.BucketYear:
		return time.Date(y, 1, 1, 0, 0, 0, 0, loc)
	case core.BucketMonth:
		return time.Date(y, m, 1, 0, 0, 0, 0, loc)
	case core.BucketWeek:
		back := (int(at.Weekday()) + 6) % 7 // Monday is 0
		return time.Date(y, m, d-back, 0, 0, 0, 0, loc)
	}
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// analysisBucketKey writes a bucket the way aggregate_metadata does:
// 2026-10-03 for a day, the Monday's date for a week, 2026-10 for a month,
// 2026 for a year. The page reads one spelling whichever side bucketed.
func analysisBucketKey(at time.Time, unit string, loc *time.Location) string {
	start := analysisBucketStart(at, unit, loc)
	switch unit {
	case core.BucketYear:
		return start.Format("2006")
	case core.BucketMonth:
		return start.Format("2006-01")
	}
	return start.Format("2006-01-02")
}

// analysisZone reads the zone the reader's browser named. One that is not a
// zone this system knows falls back to UTC, and the answer says which zone
// was used, so a page is never bucketed in a zone it does not name.
func analysisZone(raw string) *time.Location {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 64 {
		return time.UTC
	}
	loc, err := time.LoadLocation(raw)
	if err != nil {
		return time.UTC
	}
	return loc
}

// analysisDays reads the range: a number of days, or 0 for everything. What
// is not a number in range is the default month.
func analysisDays(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 30
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 30
	}
	return min(n, analysisMaxDays)
}

// --- what the overview answers -----------------------------------------------

// AnalysisPoint is one bar or point: a bucket, the series it belongs to when
// the chart has several, and the value.
type AnalysisPoint struct {
	Bucket string  `json:"bucket"`
	Series string  `json:"series,omitempty"`
	Value  float64 `json:"value"`
}

// AnalysisSeries is one time chart of counts. Points holds only the buckets
// something was counted in; a bucket that is absent is zero, which is true of
// a count and is why the page may fill it.
type AnalysisSeries struct {
	// Available and Reason separate a chart that counted nothing from one
	// that could not count.
	Available bool            `json:"available"`
	Reason    string          `json:"reason,omitempty"`
	Points    []AnalysisPoint `json:"points"`
	// Total is the count over the range.
	Total int `json:"total"`
	// Unit is the bucket this chart uses when it is not the overview's: the
	// change feed holds about a week, and a week is not drawn as one bar.
	Unit string `json:"unit,omitempty"`
	// All is the count over everything the brain holds, when the read that
	// filled this chart learned it; absent otherwise.
	All *int `json:"all,omitempty"`
	// Truncated says the scan stopped at its cap before the start of the
	// range, and CoveredSince how far back it got (RFC 3339): the chart is
	// complete from there on and says nothing about before.
	Truncated    bool   `json:"truncated,omitempty"`
	CoveredSince string `json:"covered_since,omitempty"`
}

// AnalysisRecallPoint is one bucket of the recall log.
type AnalysisRecallPoint struct {
	Bucket  string `json:"bucket"`
	Recalls int    `json:"recalls"`
	// Empty is how many of them returned nothing.
	Empty int `json:"empty"`
	// P50 and P95 are the latency percentiles of the bucket's recalls, in
	// milliseconds.
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
}

// AnalysisRecalls is the recall log over the range. A bucket with no recalls
// is absent: zero recalls is a fact the page may fill in, a latency for it is
// not, and the latency chart leaves the gap.
type AnalysisRecalls struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Off says the brain is fine and keeps no recall log, which is the
	// default and not a fault.
	Off    bool                  `json:"off,omitempty"`
	Points []AnalysisRecallPoint `json:"points"`
	Total  int                   `json:"total"`
	Empty  int                   `json:"empty"`
	// P50 and P95 are over every recall in the range; absent when there were
	// none.
	P50          *float64 `json:"p50_ms,omitempty"`
	P95          *float64 `json:"p95_ms,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
	CoveredSince string   `json:"covered_since,omitempty"`
}

// AnalysisTypeCount is one type and how many records carry it.
type AnalysisTypeCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// AnalysisGraph is the graph by type: the largest node types and edge types,
// with the rest as one number. The store's own kinds (a memory, a document,
// a chunk, the mention that ties one to an entity) are left out, as the graph
// page leaves them out.
type AnalysisGraph struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Counted is "store" or "graph", as [OntologyUsage.Counted] says it.
	Counted string `json:"counted,omitempty"`

	Nodes     int                 `json:"nodes"`
	Edges     int                 `json:"edges"`
	NodeTypes []AnalysisTypeCount `json:"node_types"`
	EdgeTypes []AnalysisTypeCount `json:"edge_types"`
	// OtherNodes is the nodes of the OtherNodeTypes types not listed, and
	// likewise for edges.
	OtherNodes     int `json:"other_nodes"`
	OtherNodeTypes int `json:"other_node_types"`
	OtherEdges     int `json:"other_edges"`
	OtherEdgeTypes int `json:"other_edge_types"`
}

// AnalysisOverview is everything the overview draws, read at one time.
type AnalysisOverview struct {
	// Available is false when this view's source has none of the hooks any
	// chart reads, and Reason says so.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Explore says the builder can be used: the source runs tools.
	Explore bool `json:"explore"`

	// Days is the range asked for, 0 for everything; Unit the bucket the
	// time charts use (day, week or month); Since and Until the first and
	// last bucket of the range, Since absent when Days is 0; Timezone the
	// zone the buckets were cut in.
	Days     int    `json:"days"`
	Unit     string `json:"unit"`
	Since    string `json:"since,omitempty"`
	Until    string `json:"until"`
	Timezone string `json:"timezone"`

	Memories  AnalysisSeries  `json:"memories"`
	Documents AnalysisSeries  `json:"documents"`
	Recalls   AnalysisRecalls `json:"recalls"`
	Writes    AnalysisSeries  `json:"writes"`
	Graph     AnalysisGraph   `json:"graph"`

	// At is when this was read, unix milliseconds.
	At int64 `json:"at"`
}

func noSeries(reason string) AnalysisSeries {
	return AnalysisSeries{Reason: reason, Points: []AnalysisPoint{}}
}

const (
	analysisNoTools = "this view's source cannot run the brain's tools"
	analysisOldTool = "this brain's aggregate_metadata does not answer measures and time buckets; it is older than this page"
)

// --- scans -------------------------------------------------------------------

// analysisScan is one newest-first read of a list, kept for analysisScanTTL.
type analysisScan[T any] struct {
	items []T
	// covered is the instant the scan reaches back to: everything at or after
	// it is in items. Zero when the scan reached the end of the list.
	covered time.Time
	// capped says the scan stopped at its page cap.
	capped bool
	taken  time.Time
}

// covers reports whether the scan holds everything from since on.
func (s *analysisScan[T]) covers(since time.Time, now time.Time) bool {
	if s == nil || now.Sub(s.taken) > analysisScanTTL {
		return false
	}
	if s.covered.IsZero() || s.capped {
		// The end of the list, or as far as a scan is allowed to go: asking
		// again would read the same pages.
		return true
	}
	return !since.IsZero() && !since.Before(s.covered)
}

type analysisMemory struct {
	at    time.Time
	scope string
}

type analysisRecall struct {
	at      time.Time
	latency float64
	empty   bool
}

type analysisWrite struct {
	at   time.Time
	kind string
}

// analysisState is the scans a view holds between requests.
type analysisState struct {
	mu       sync.Mutex
	memories *analysisScan[analysisMemory]
	recalls  *analysisScan[analysisRecall]
	writes   *analysisScan[analysisWrite]
}

// scanned returns the kept scan when it covers since, or runs read and keeps
// what it returns. The lock is held across the read on purpose: two readers
// opening the page together page through the brain once, not twice.
func scanned[T any](mu *sync.Mutex, slot **analysisScan[T], since time.Time, read func() (*analysisScan[T], error)) (*analysisScan[T], error) {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	if (*slot).covers(since, now) {
		return *slot, nil
	}
	got, err := read()
	if err != nil {
		return nil, err
	}
	got.taken = now
	*slot = got
	return got, nil
}

// pageScan pages a newest-first list. next returns one page's items, the
// instant of its oldest item, and whether another page follows.
func pageScan[T any](pages int, since time.Time, next func() (items []T, oldest time.Time, more bool, err error)) (*analysisScan[T], error) {
	out := &analysisScan[T]{}
	for page := 0; ; page++ {
		items, oldest, more, err := next()
		if err != nil {
			return nil, err
		}
		out.items = append(out.items, items...)
		if !more {
			return out, nil
		}
		if !since.IsZero() && !oldest.IsZero() && oldest.Before(since) {
			out.covered = since
			return out, nil
		}
		if page+1 >= pages {
			out.covered, out.capped = oldest, true
			return out, nil
		}
	}
}

func (s *Server) scanMemories(ctx context.Context, since time.Time) (*analysisScan[analysisMemory], error) {
	return scanned(&s.analysis.mu, &s.analysis.memories, since, func() (*analysisScan[analysisMemory], error) {
		cursor := ""
		return pageScan(analysisMemoryPages, since, func() ([]analysisMemory, time.Time, bool, error) {
			var resp cortexdb.MemoryListAllResponse
			err := callInto(ctx, s.src.Call, "memory_list_all", cortexdb.MemoryListAllRequest{Limit: analysisMemoryPage, Cursor: cursor}, &resp)
			if err != nil {
				return nil, time.Time{}, false, err
			}
			items := make([]analysisMemory, 0, len(resp.Memories))
			var oldest time.Time
			for _, m := range resp.Memories {
				items = append(items, analysisMemory{at: m.CreatedAt, scope: m.Scope})
				oldest = m.CreatedAt
			}
			cursor = resp.NextCursor
			return items, oldest, resp.Truncated && cursor != "", nil
		})
	})
}

func (s *Server) scanRecalls(ctx context.Context, since time.Time) (*analysisScan[analysisRecall], error) {
	return scanned(&s.analysis.mu, &s.analysis.recalls, since, func() (*analysisScan[analysisRecall], error) {
		var before int64
		return pageScan(analysisRecallPages, since, func() ([]analysisRecall, time.Time, bool, error) {
			var resp cortexdb.RecallLogListResponse
			err := callInto(ctx, s.src.Call, "recall_log_list", cortexdb.RecallLogListRequest{Limit: analysisRecallPage, Before: before}, &resp)
			if err != nil {
				return nil, time.Time{}, false, err
			}
			items := make([]analysisRecall, 0, len(resp.Recalls))
			var oldest time.Time
			for _, r := range resp.Recalls {
				items = append(items, analysisRecall{at: r.At, latency: float64(r.LatencyMs), empty: r.HitCount == 0})
				oldest = r.At
			}
			before = resp.NextBefore
			return items, oldest, before > 0, nil
		})
	})
}

func (s *Server) scanWrites(ctx context.Context, since time.Time) (*analysisScan[analysisWrite], error) {
	return scanned(&s.analysis.mu, &s.analysis.writes, since, func() (*analysisScan[analysisWrite], error) {
		before := int64(-1)
		return pageScan(analysisChangePages, since, func() ([]analysisWrite, time.Time, bool, error) {
			resp, err := s.src.ChangeLog(ctx, cortexdb.ChangesSinceRequest{Before: before, Limit: analysisChangePage})
			if err != nil {
				return nil, time.Time{}, false, err
			}
			items := make([]analysisWrite, 0, len(resp.Events))
			var oldest time.Time
			for _, ev := range resp.Events {
				items = append(items, analysisWrite{at: ev.At, kind: ev.Kind})
				oldest = ev.At
			}
			more := resp.More && len(resp.Events) > 0 && resp.NextCursor > 1
			before = resp.NextCursor
			return items, oldest, more, nil
		})
	})
}

// --- the overview's sections -------------------------------------------------

// countedSeries buckets a scan's items in range into a series of counts.
func countedSeries[T any](scan *analysisScan[T], r analysisRange, at func(T) time.Time, series func(T) string) AnalysisSeries {
	counts := map[[2]string]int{}
	out := AnalysisSeries{Available: true}
	for _, item := range scan.items {
		when := at(item)
		if when.IsZero() || !r.includes(when) {
			continue
		}
		counts[[2]string{r.key(when), series(item)}]++
		out.Total++
	}
	out.Points = make([]AnalysisPoint, 0, len(counts))
	for k, n := range counts {
		out.Points = append(out.Points, AnalysisPoint{Bucket: k[0], Series: k[1], Value: float64(n)})
	}
	sort.Slice(out.Points, func(i, j int) bool {
		if out.Points[i].Bucket != out.Points[j].Bucket {
			return out.Points[i].Bucket < out.Points[j].Bucket
		}
		return out.Points[i].Series < out.Points[j].Series
	})
	out.Truncated, out.CoveredSince = scanShort(scan.capped, scan.covered, r)
	if scan.covered.IsZero() {
		all := len(scan.items)
		out.All = &all
	}
	return out
}

// scanShort says whether a capped scan stopped inside the range, and where.
func scanShort(capped bool, covered time.Time, r analysisRange) (bool, string) {
	if !capped || covered.IsZero() || (!r.since.IsZero() && !covered.After(r.since)) {
		return false, ""
	}
	return true, covered.UTC().Format(time.RFC3339)
}

func (s *Server) analysisMemories(ctx context.Context, r analysisRange) AnalysisSeries {
	if s.src.Call == nil {
		return noSeries(analysisNoTools)
	}
	scan, err := s.scanMemories(ctx, r.since)
	if err != nil {
		return noSeries(err.Error())
	}
	return countedSeries(scan, r, func(m analysisMemory) time.Time { return m.at }, func(m analysisMemory) string { return m.scope })
}

// analysisWritesInDays is the span of writes still drawn a day at a time.
const analysisWritesInDays = 45 * 24 * time.Hour

func (s *Server) analysisWrites(ctx context.Context, r analysisRange) AnalysisSeries {
	if s.src.ChangeLog == nil {
		return noSeries("this view's source has no change feed")
	}
	scan, err := s.scanWrites(ctx, r.since)
	if err != nil {
		return noSeries(err.Error())
	}
	// The feed is pruned to about a week whatever range was asked for, so a
	// long range's weeks or months would put all of it in one bar. What it
	// holds is drawn by day unless it really does reach back further.
	oldest := r.now
	for _, w := range scan.items {
		if r.includes(w.at) && w.at.Before(oldest) {
			oldest = w.at
		}
	}
	unit := ""
	if r.unit != core.BucketDay && r.now.Sub(oldest) <= analysisWritesInDays {
		unit = core.BucketDay
		r.unit = core.BucketDay
	}
	out := countedSeries(scan, r, func(w analysisWrite) time.Time { return w.at }, func(w analysisWrite) string { return w.kind })
	out.Unit = unit
	// The feed is pruned, so the end of it is not the beginning of the brain.
	out.All = nil
	return out
}

func (s *Server) analysisRecalls(ctx context.Context, r analysisRange) AnalysisRecalls {
	none := func(reason string) AnalysisRecalls {
		return AnalysisRecalls{Reason: reason, Points: []AnalysisRecallPoint{}}
	}
	if s.src.Call == nil {
		return none(analysisNoTools)
	}
	scan, err := s.scanRecalls(ctx, r.since)
	if err != nil {
		out := none(err.Error())
		out.Off = errors.Is(err, cortexdb.ErrRecallLogOff) || status.Code(err) == codes.FailedPrecondition
		return out
	}

	type bucket struct {
		empty     int
		latencies []float64
	}
	buckets := map[string]*bucket{}
	var all []float64
	out := AnalysisRecalls{Available: true}
	for _, rec := range scan.items {
		if !r.includes(rec.at) {
			continue
		}
		key := r.key(rec.at)
		b := buckets[key]
		if b == nil {
			b = &bucket{}
			buckets[key] = b
		}
		b.latencies = append(b.latencies, rec.latency)
		all = append(all, rec.latency)
		out.Total++
		if rec.empty {
			b.empty++
			out.Empty++
		}
	}
	out.Points = make([]AnalysisRecallPoint, 0, len(buckets))
	for key, b := range buckets {
		sort.Float64s(b.latencies)
		out.Points = append(out.Points, AnalysisRecallPoint{
			Bucket: key, Recalls: len(b.latencies), Empty: b.empty,
			P50: percentileOf(b.latencies, 50), P95: percentileOf(b.latencies, 95),
		})
	}
	sort.Slice(out.Points, func(i, j int) bool { return out.Points[i].Bucket < out.Points[j].Bucket })
	if len(all) > 0 {
		sort.Float64s(all)
		p50, p95 := percentileOf(all, 50), percentileOf(all, 95)
		out.P50, out.P95 = &p50, &p95
	}
	out.Truncated, out.CoveredSince = scanShort(scan.capped, scan.covered, r)
	return out
}

// percentileOf is the rank by linear interpolation over sorted values, as
// aggregate_metadata computes it, so the two never disagree about a p95.
func percentileOf(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := p / 100 * float64(len(sorted)-1)
	low, high := int(math.Floor(rank)), int(math.Ceil(rank))
	if low == high {
		return sorted[low]
	}
	return sorted[low] + (rank-float64(low))*(sorted[high]-sorted[low])
}

// analysisDocuments counts documents per bucket: chunks whose chunk_index is
// 0, which every document has exactly one of.
func (s *Server) analysisDocuments(ctx context.Context, r analysisRange) AnalysisSeries {
	if s.src.Call == nil {
		return noSeries(analysisNoTools)
	}
	req := cortexdb.ToolAggregateMetadataRequest{
		Measures:   []core.AggregationMeasure{{Type: core.MeasureCount, Alias: analysisRowsAlias}},
		TimeBucket: &core.AggregationTimeBucket{Unit: r.unit, Alias: analysisBucketAlias, Timezone: r.loc.String()},
		Filters:    map[string]any{"chunk_index": "0"},
		OrderBy:    analysisBucketAlias,
		Order:      "desc",
		Limit:      analysisBucketRows + 1,
	}
	if !r.since.IsZero() {
		req.CreatedSince = r.since.Format(time.RFC3339)
	}
	var resp cortexdb.ToolAggregateMetadataResponse
	if err := callInto(ctx, s.src.Call, "aggregate_metadata", req, &resp); err != nil {
		return noSeries(err.Error())
	}
	out := AnalysisSeries{Available: true, Points: make([]AnalysisPoint, 0, len(resp.Results))}
	for i, g := range resp.Results {
		if _, ok := g.Values[analysisRowsAlias]; !ok {
			return noSeries(analysisOldTool)
		}
		if i == analysisBucketRows {
			out.Truncated = true
			break
		}
		bucket, ok := g.Keys[analysisBucketAlias].(string)
		if !ok {
			// A row whose stored time cannot be read: counted, in no bucket.
			out.Total += g.Count
			continue
		}
		out.Points = append(out.Points, AnalysisPoint{Bucket: bucket, Value: float64(g.Count)})
		out.Total += g.Count
	}
	sort.Slice(out.Points, func(i, j int) bool { return out.Points[i].Bucket < out.Points[j].Bucket })

	// Everything the brain holds, for the headline. One COUNT, and a failure
	// leaves the headline out, not the chart.
	var total cortexdb.ToolAggregateMetadataResponse
	err := callInto(ctx, s.src.Call, "aggregate_metadata", cortexdb.ToolAggregateMetadataRequest{
		Type: string(core.AggregationCount), Filters: map[string]any{"chunk_index": "0"},
	}, &total)
	if err == nil && len(total.Results) == 1 {
		all := total.Results[0].Count
		if n, ok := total.Results[0].Value.(float64); ok {
			all = int(n)
		}
		out.All = &all
	}
	return out
}

// analysisGraph reads the graph by type through the ontology hook, which
// already counts every node and edge type for the ontology page.
func (s *Server) analysisGraph(ctx context.Context) AnalysisGraph {
	none := func(reason string) AnalysisGraph {
		return AnalysisGraph{Reason: reason, NodeTypes: []AnalysisTypeCount{}, EdgeTypes: []AnalysisTypeCount{}}
	}
	if s.src.Ontology == nil {
		return none("this view's source cannot be asked what its records are typed as")
	}
	rep, err := s.src.Ontology(ctx, OntologyQuery{Usage: true})
	if err != nil {
		return none(err.Error())
	}
	if !rep.Usage.Available {
		return none(firstText(rep.Usage.Reason, rep.Reason, "the store's types could not be counted"))
	}
	u := rep.Usage
	out := AnalysisGraph{Available: true, Counted: u.Counted}

	var nodes, edges []AnalysisTypeCount
	nodeTypes, edgeTypes := u.UndeclaredNodeTypes, u.UndeclaredEdgeTypes
	out.Nodes, out.Edges = u.UndeclaredNodeCount, u.UndeclaredEdgeCount
	for _, t := range rep.ObjectTypes {
		if t.Instances > 0 {
			nodes = append(nodes, AnalysisTypeCount{Name: t.APIName, Count: t.Instances})
			nodeTypes++
			out.Nodes += t.Instances
		}
	}
	for _, t := range rep.LinkTypes {
		if t.Instances > 0 {
			edges = append(edges, AnalysisTypeCount{Name: t.APIName, Count: t.Instances})
			edgeTypes++
			out.Edges += t.Instances
		}
	}
	// With no schema saved the undeclared lists are the whole vocabulary, the
	// store's own kinds included; they are taken out here, as a saved schema
	// takes them out of its own lists.
	for _, t := range u.UndeclaredNodes {
		if storeNodeKinds[ontologyKey(t.Name)] || bookkeepingNode(t.Name) {
			out.Nodes -= t.Count
			nodeTypes--
			continue
		}
		nodes = append(nodes, AnalysisTypeCount(t))
	}
	for _, t := range u.UndeclaredEdges {
		if storeEdgeKinds[ontologyKey(t.Name)] || bookkeepingEdge(t.Name) {
			out.Edges -= t.Count
			edgeTypes--
			continue
		}
		edges = append(edges, AnalysisTypeCount(t))
	}
	out.NodeTypes, out.OtherNodes, out.OtherNodeTypes = topTypes(nodes, out.Nodes, nodeTypes)
	out.EdgeTypes, out.OtherEdges, out.OtherEdgeTypes = topTypes(edges, out.Edges, edgeTypes)
	return out
}

// topTypes keeps the largest analysisTypeBars types and folds the rest into
// one count, taken from the totals so types a capped list never named are in
// it too.
func topTypes(types []AnalysisTypeCount, total, distinct int) ([]AnalysisTypeCount, int, int) {
	sort.SliceStable(types, func(i, j int) bool {
		if types[i].Count != types[j].Count {
			return types[i].Count > types[j].Count
		}
		return types[i].Name < types[j].Name
	})
	if len(types) > analysisTypeBars {
		types = types[:analysisTypeBars]
	}
	if types == nil {
		types = []AnalysisTypeCount{}
	}
	listed := 0
	for _, t := range types {
		listed += t.Count
	}
	return types, max(total-listed, 0), max(distinct-len(types), 0)
}

func firstText(texts ...string) string {
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			return t
		}
	}
	return ""
}

// Overview reads everything the overview draws. The sections are read side
// by side, and one that fails says so without holding up the others.
func (s *Server) analysisOverview(ctx context.Context, days int, loc *time.Location) AnalysisOverview {
	r := newAnalysisRange(days, loc, time.Now())
	out := AnalysisOverview{
		Explore:  s.src.Call != nil,
		Days:     r.days,
		Unit:     r.unit,
		Until:    r.key(r.now),
		Timezone: loc.String(),
		At:       time.Now().UnixMilli(),
	}
	if !r.since.IsZero() {
		out.Since = r.key(r.since)
	}
	if s.src.Call == nil && s.src.ChangeLog == nil && s.src.Ontology == nil {
		out.Reason = "this view's source can draw the graph and nothing else: it runs no tools, has no change feed and cannot count its types"
		out.Memories, out.Documents, out.Writes = noSeries(out.Reason), noSeries(out.Reason), noSeries(out.Reason)
		out.Recalls = AnalysisRecalls{Reason: out.Reason, Points: []AnalysisRecallPoint{}}
		out.Graph = AnalysisGraph{Reason: out.Reason, NodeTypes: []AnalysisTypeCount{}, EdgeTypes: []AnalysisTypeCount{}}
		return out
	}
	out.Available = true

	var wg sync.WaitGroup
	for _, read := range []func(){
		func() { out.Memories = s.analysisMemories(ctx, r) },
		func() { out.Documents = s.analysisDocuments(ctx, r) },
		func() { out.Recalls = s.analysisRecalls(ctx, r) },
		func() { out.Writes = s.analysisWrites(ctx, r) },
		func() { out.Graph = s.analysisGraph(ctx) },
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			read()
		}()
	}
	wg.Wait()
	return out
}

func (s *Server) handleAnalysisOverview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), analysisTimeout)
	defer cancel()
	writeExplore(w, s.analysisOverview(ctx, analysisDays(q.Get("days")), analysisZone(q.Get("tz"))), nil)
}

// --- the builder -------------------------------------------------------------

// AnalysisQuery is one question the builder asks, as the query string of
// /api/analysis/query spells it.
type AnalysisQuery struct {
	// Of is what a row is: "documents" (one row per document, its first
	// chunk) or "chunks" (every chunk row).
	Of         string `json:"of"`
	Collection string `json:"collection,omitempty"`
	// Measure is count, sum, avg, median, p95, min or max; every one but
	// count reads Field as a number.
	Measure string `json:"measure"`
	Field   string `json:"field,omitempty"`
	// Group is a metadata field to group by, and Bucket a unit (day, week,
	// month, year) to bucket time by; either, both or neither.
	Group  string `json:"group,omitempty"`
	Bucket string `json:"bucket,omitempty"`
	// BucketField is the metadata field holding the time to bucket; empty is
	// the time the record was stored.
	BucketField string `json:"bucket_field,omitempty"`
	// Since and Until are dates (YYYY-MM-DD) in Timezone, both inclusive, on
	// the time the record was stored.
	Since    string `json:"since,omitempty"`
	Until    string `json:"until,omitempty"`
	Timezone string `json:"timezone"`
}

// AnalysisRow is one group of the answer.
type AnalysisRow struct {
	// Bucket is the time bucket, absent when the question has none and null
	// when the row's time could not be read.
	Bucket *string `json:"bucket,omitempty"`
	// Group is the group field's value as text, null for the rows that do
	// not carry the field.
	Group *string `json:"group,omitempty"`
	// Value is the measure; null when no row of the group held a number.
	Value *float64 `json:"value"`
	// Rows is how many rows the group has, and Skipped how many of them
	// carried the field with something that is not a number.
	Rows    int `json:"rows"`
	Skipped int `json:"skipped,omitempty"`
}

// AnalysisResult answers the builder.
type AnalysisResult struct {
	Query AnalysisQuery `json:"query"`
	Rows  []AnalysisRow `json:"rows"`
	// Truncated says the question has more than analysisQueryRows groups and
	// only that many are here: the newest buckets of a time question, the
	// largest groups of any other.
	Truncated bool `json:"truncated,omitempty"`
}

// parseAnalysisQuery checks everything a caller sent and refuses what is not
// a question this page asks.
func parseAnalysisQuery(q url.Values) (AnalysisQuery, error) {
	get := func(name string) string { return strings.TrimSpace(q.Get(name)) }
	out := AnalysisQuery{
		Of: get("of"), Collection: get("collection"), Measure: get("measure"), Field: get("field"),
		Group: get("group"), Bucket: get("bucket"), BucketField: get("bucket_field"),
		Since: get("since"), Until: get("until"), Timezone: analysisZone(get("tz")).String(),
	}
	if out.Of == "" {
		out.Of = "documents"
	}
	if out.Of != "documents" && out.Of != "chunks" {
		return out, errors.New("of must be documents or chunks")
	}
	if len(out.Collection) > 128 {
		return out, errors.New("collection is longer than 128 characters")
	}
	if out.Measure == "" {
		out.Measure = "count"
	}
	if !analysisMeasures[out.Measure] {
		return out, errors.New("measure must be one of count, sum, avg, median, p95, min, max")
	}
	if out.Measure == "count" {
		out.Field = ""
	} else if out.Field == "" {
		return out, fmt.Errorf("a %s needs a field to read numbers from", out.Measure)
	}
	for role, name := range map[string]string{"field": out.Field, "group": out.Group, "bucket_field": out.BucketField} {
		if name == "" {
			continue
		}
		if !analysisName.MatchString(name) {
			return out, fmt.Errorf("%s is not a metadata field name: a letter or underscore, then letters, digits, underscore, dot or hyphen", role)
		}
		if name == analysisBucketAlias || name == analysisValueAlias || name == analysisRowsAlias {
			return out, fmt.Errorf("%s names a field this page keeps for its own columns", role)
		}
	}
	if out.Bucket != "" && !analysisUnits[out.Bucket] {
		return out, errors.New("bucket must be one of day, week, month, year")
	}
	if out.Bucket == "" {
		out.BucketField = ""
	}
	for role, date := range map[string]string{"since": out.Since, "until": out.Until} {
		if date == "" {
			continue
		}
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return out, fmt.Errorf("%s must be a date, YYYY-MM-DD", role)
		}
	}
	if out.Since != "" && out.Until != "" && out.Until < out.Since {
		return out, errors.New("until is before since")
	}
	return out, nil
}

// request turns a checked question into the one aggregate_metadata call that
// answers it.
func (q AnalysisQuery) request() cortexdb.ToolAggregateMetadataRequest {
	loc := analysisZone(q.Timezone)
	req := cortexdb.ToolAggregateMetadataRequest{
		Collection: q.Collection,
		Measures: []core.AggregationMeasure{
			{Type: q.Measure, Field: q.Field, Alias: analysisValueAlias},
			{Type: core.MeasureCount, Alias: analysisRowsAlias},
		},
		Limit: analysisQueryRows + 1,
	}
	if q.Of == "documents" {
		req.Filters = map[string]any{"chunk_index": "0"}
	}
	if q.Group != "" {
		req.GroupBy = []string{q.Group}
	}
	if q.Bucket != "" {
		req.TimeBucket = &core.AggregationTimeBucket{Unit: q.Bucket, Field: q.BucketField, Alias: analysisBucketAlias, Timezone: loc.String()}
		// Newest first, so a question with too many buckets keeps the recent
		// ones; the answer is put back in time order.
		req.OrderBy, req.Order = analysisBucketAlias, "desc"
	} else {
		req.OrderBy, req.Order = analysisRowsAlias, "desc"
	}
	if q.Since != "" {
		day, _ := time.ParseInLocation(time.DateOnly, q.Since, loc)
		req.CreatedSince = day.Format(time.RFC3339)
	}
	if q.Until != "" {
		day, _ := time.ParseInLocation(time.DateOnly, q.Until, loc)
		req.CreatedBefore = day.AddDate(0, 0, 1).Format(time.RFC3339)
	}
	return req
}

// AnalysisAsk answers one builder question through a brain's tools.
func AnalysisAsk(ctx context.Context, call Caller, q AnalysisQuery) (AnalysisResult, error) {
	out := AnalysisResult{Query: q, Rows: []AnalysisRow{}}
	var resp cortexdb.ToolAggregateMetadataResponse
	if err := callInto(ctx, call, "aggregate_metadata", q.request(), &resp); err != nil {
		return out, err
	}
	for i, g := range resp.Results {
		raw, ok := g.Values[analysisValueAlias]
		if !ok {
			return out, errors.New(analysisOldTool)
		}
		if i == analysisQueryRows {
			out.Truncated = true
			break
		}
		row := AnalysisRow{Rows: g.Count, Skipped: g.Skipped[analysisValueAlias]}
		if n, ok := raw.(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
			row.Value = &n
		}
		if q.Bucket != "" {
			if b, ok := g.Keys[analysisBucketAlias].(string); ok {
				row.Bucket = &b
			}
		}
		if q.Group != "" {
			if v, present := g.Keys[q.Group]; present && v != nil {
				text := analysisText(v)
				row.Group = &text
			}
		}
		out.Rows = append(out.Rows, row)
	}
	if q.Bucket != "" {
		// Time order, rows with no readable time last; the groups of one
		// bucket keep the order the brain gave them.
		sort.SliceStable(out.Rows, func(i, j int) bool {
			a, b := out.Rows[i].Bucket, out.Rows[j].Bucket
			switch {
			case a == nil || b == nil:
				return a != nil && b == nil
			}
			return *a < *b
		})
	}
	return out, nil
}

// analysisText writes a group key as the page shows it: a whole number
// without a fraction, anything else as it is.
func analysisText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprint(v)
}

func (s *Server) handleAnalysisQuery(w http.ResponseWriter, r *http.Request) {
	q, err := parseAnalysisQuery(r.URL.Query())
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), analysisTimeout)
	defer cancel()
	out, err := AnalysisAsk(ctx, s.src.Call, q)
	writeExplore(w, out, err)
}

// --- the fields the builder offers -------------------------------------------

// AnalysisField is one metadata field found in the sample.
type AnalysisField struct {
	Name string `json:"name"`
	// Kind is "number" when every sampled value is a number, "date" when
	// every one is a date or a timestamp, and "text" otherwise.
	Kind string `json:"kind"`
	// Documents is how many of the sampled documents carry the field, and
	// Distinct how many different values they hold.
	Documents int      `json:"documents"`
	Distinct  int      `json:"distinct"`
	Examples  []string `json:"examples,omitempty"`
}

// AnalysisFields is what the builder's pickers are filled from: the metadata
// fields of a sample of documents. It is a sample, so a field only a few
// documents carry can be missing, and the page lets a name be typed.
type AnalysisFields struct {
	// Sampled is how many documents were read.
	Sampled     int             `json:"sampled"`
	Fields      []AnalysisField `json:"fields"`
	Collections []string        `json:"collections"`
}

// AnalysisFieldSample reads the metadata fields of up to analysisFieldDocs
// documents, taken across the months the brain was written in, so a field
// that only old or only new documents carry is still found.
//
// Two calls, both bounded: aggregate_metadata lists (month, document) groups,
// and knowledge_get reads the picked documents' metadata. There is no tool
// that lists metadata keys, and these two together are the walk
// aggregate_metadata's own description sends a caller on.
func AnalysisFieldSample(ctx context.Context, call Caller, collection string) (AnalysisFields, error) {
	out := AnalysisFields{Fields: []AnalysisField{}, Collections: []string{}}
	var groups cortexdb.ToolAggregateMetadataResponse
	err := callInto(ctx, call, "aggregate_metadata", cortexdb.ToolAggregateMetadataRequest{
		Collection: collection,
		Measures:   []core.AggregationMeasure{{Type: core.MeasureCount, Alias: analysisRowsAlias}},
		GroupBy:    []string{"document_id"},
		TimeBucket: &core.AggregationTimeBucket{Unit: core.BucketMonth, Alias: analysisBucketAlias},
		Filters:    map[string]any{"chunk_index": "0"},
		OrderBy:    analysisBucketAlias,
		Order:      "desc",
		Limit:      analysisFieldGroups,
	}, &groups)
	if err != nil {
		return out, err
	}

	// One queue of documents per month, newest month first; taking from each
	// in turn spreads the sample over time.
	var months []string
	byMonth := map[string][]string{}
	for _, g := range groups.Results {
		if _, ok := g.Values[analysisRowsAlias]; !ok {
			return out, errors.New(analysisOldTool)
		}
		id, _ := g.Keys["document_id"].(string)
		if id == "" {
			continue
		}
		month, _ := g.Keys[analysisBucketAlias].(string)
		if _, seen := byMonth[month]; !seen {
			months = append(months, month)
		}
		byMonth[month] = append(byMonth[month], id)
	}
	var ids []string
	for len(ids) < analysisFieldDocs {
		took := false
		for _, m := range months {
			if len(byMonth[m]) == 0 || len(ids) == analysisFieldDocs {
				continue
			}
			ids = append(ids, byMonth[m][0])
			byMonth[m] = byMonth[m][1:]
			took = true
		}
		if !took {
			break
		}
	}
	if len(ids) == 0 {
		return out, nil
	}

	var docs cortexdb.KnowledgeGetResponse
	if err := callInto(ctx, call, "knowledge_get", cortexdb.KnowledgeGetRequest{KnowledgeIDs: ids}, &docs); err != nil {
		return out, err
	}
	records := docs.Knowledges
	if len(records) == 0 && docs.Knowledge.ID != "" {
		records = []cortexdb.KnowledgeRecord{docs.Knowledge}
	}
	out.Sampled = len(records)

	type seen struct {
		documents      int
		values         map[string]bool
		examples       []string
		numbers, dates int
	}
	fields := map[string]*seen{}
	collections := map[string]bool{}
	for _, rec := range records {
		if rec.Collection != "" {
			collections[rec.Collection] = true
		}
		for name, value := range rec.Metadata {
			// The engine's own keys start with an underscore; they are not
			// what a reader groups by, and some are not on the chunk rows.
			if strings.HasPrefix(name, "_") || !analysisName.MatchString(name) {
				continue
			}
			f := fields[name]
			if f == nil {
				f = &seen{values: map[string]bool{}}
				fields[name] = f
			}
			f.documents++
			if _, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && strings.TrimSpace(value) != "" {
				f.numbers++
			} else if analysisIsTime(value) {
				f.dates++
			}
			if !f.values[value] {
				f.values[value] = true
				if len(f.examples) < analysisFieldExamples && value != "" {
					f.examples = append(f.examples, clipRunes(value, 40))
				}
			}
		}
	}
	for name, f := range fields {
		kind := "text"
		switch f.documents {
		case f.numbers:
			kind = "number"
		case f.dates:
			kind = "date"
		}
		sort.Strings(f.examples)
		out.Fields = append(out.Fields, AnalysisField{Name: name, Kind: kind, Documents: f.documents, Distinct: len(f.values), Examples: f.examples})
	}
	// The fields most documents carry first; a picker's first screen is the
	// fields a question is most likely about.
	sort.Slice(out.Fields, func(i, j int) bool {
		if out.Fields[i].Documents != out.Fields[j].Documents {
			return out.Fields[i].Documents > out.Fields[j].Documents
		}
		return out.Fields[i].Name < out.Fields[j].Name
	})
	if len(out.Fields) > analysisFieldLimit {
		out.Fields = out.Fields[:analysisFieldLimit]
	}
	for c := range collections {
		out.Collections = append(out.Collections, c)
	}
	sort.Strings(out.Collections)
	return out, nil
}

// analysisIsTime reports whether a value reads as a timestamp or a date, in
// the shapes a time bucket reads.
func analysisIsTime(value string) bool {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", time.DateOnly} {
		if _, err := time.Parse(layout, value); err == nil {
			return true
		}
	}
	return false
}

func (s *Server) handleAnalysisFields(w http.ResponseWriter, r *http.Request) {
	collection := strings.TrimSpace(r.URL.Query().Get("collection"))
	if len(collection) > 128 {
		writeBadRequest(w, errors.New("collection is longer than 128 characters"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), analysisTimeout)
	defer cancel()
	out, err := AnalysisFieldSample(ctx, s.src.Call, collection)
	writeExplore(w, out, err)
}
