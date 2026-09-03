// Package observability gives CortexDB the three things an operator needs from
// a process that calls itself a service: counters, gauges and histograms that
// can be scraped, the same numbers on /debug/vars for a human with curl, and a
// tracing seam that some other module can fill in.
//
// It deliberately depends on nothing outside the standard library. The
// Prometheus text exposition format is a few dozen lines to emit correctly, and
// this repo would rather carry those lines than carry a client library and its
// transitive tree — the same trade already made for LLM clients, which live
// behind interfaces (cortexdb.Embedder, graphflow.JSONGenerator) so their SDKs
// stay out of pkg/.
//
// Nothing here starts a goroutine or a timer. A Registry that is never scraped
// costs one map and a few atomics; a Registry that is never constructed costs
// nothing at all.
package observability

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// DefaultLatencyBuckets covers the range an RPC against a local SQLite brain
// actually spends: sub-millisecond for a cached lookup, up to ten seconds for a
// pathological SPARQL query. Buckets are the one histogram decision that cannot
// be changed later without discarding recorded history, so they are declared
// once here rather than guessed at each call site.
var DefaultLatencyBuckets = []float64{
	0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

type kind uint8

const (
	kindCounter kind = iota
	kindGauge
	kindHistogram
)

func (k kind) String() string {
	switch k {
	case kindCounter:
		return "counter"
	case kindGauge:
		return "gauge"
	default:
		return "histogram"
	}
}

// Registry holds every metric family a process exports.
//
// It is safe for concurrent use. Finding the series for a set of label values
// takes a read lock; moving the value itself is a plain atomic, so goroutines
// recording against different series never serialize on each other. Only the
// first sighting of a label combination takes the write lock.
type Registry struct {
	mu       sync.RWMutex
	families map[string]*family
}

// NewRegistry returns an empty registry. It starts nothing.
func NewRegistry() *Registry {
	return &Registry{families: make(map[string]*family)}
}

type family struct {
	name       string
	help       string
	kind       kind
	labelNames []string
	bounds     []float64 // histogram only, sorted ascending, without +Inf

	mu     sync.RWMutex
	series map[string]*series
}

type series struct {
	labelValues []string

	// counter and gauge
	value atomicFloat

	// histogram: one counter per bucket plus a final overflow bucket, which is
	// the +Inf bucket. Buckets are non-cumulative here and summed at render
	// time, which is what makes _count and the +Inf bucket agree by
	// construction instead of by hoping two atomics move together.
	buckets []atomic.Uint64
	sum     atomicFloat
}

// declare registers a family, or returns the existing one when the declaration
// is identical. A conflicting redeclaration panics.
//
// Panicking is deliberate, and is the regexp.MustCompile trade: a metric
// declared twice with different label names is a static bug in the wiring, and
// the alternatives — a detached handle, or an error every call site ignores —
// produce a scrape that silently reports the wrong shape. That failure is
// invisible until a dashboard has been lying for a month. This one shows up the
// first time anything constructs the server.
func (r *Registry) declare(name, help string, k kind, bounds []float64, labelNames []string) *family {
	if err := validMetricName(name); err != nil {
		panic("observability: " + err.Error())
	}
	for _, ln := range labelNames {
		if err := validLabelName(ln); err != nil {
			panic("observability: " + err.Error())
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.families[name]; ok {
		if existing.kind != k || !equalStrings(existing.labelNames, labelNames) {
			panic(fmt.Sprintf("observability: metric %q already declared as %s%v, redeclared as %s%v",
				name, existing.kind, existing.labelNames, k, labelNames))
		}
		return existing
	}
	f := &family{
		name:       name,
		help:       help,
		kind:       k,
		labelNames: append([]string(nil), labelNames...),
		bounds:     append([]float64(nil), bounds...),
		series:     make(map[string]*series),
	}
	r.families[name] = f
	return f
}

// get finds or creates the series for a set of label values.
func (f *family) get(labelValues []string) *series {
	if len(labelValues) != len(f.labelNames) {
		panic(fmt.Sprintf("observability: metric %q takes %d label values %v, got %d %v",
			f.name, len(f.labelNames), f.labelNames, len(labelValues), labelValues))
	}
	key := seriesKey(labelValues)

	f.mu.RLock()
	s, ok := f.series[key]
	f.mu.RUnlock()
	if ok {
		return s
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok = f.series[key]; ok {
		return s
	}
	s = &series{labelValues: append([]string(nil), labelValues...)}
	if f.kind == kindHistogram {
		s.buckets = make([]atomic.Uint64, len(f.bounds)+1)
	}
	f.series[key] = s
	return s
}

// seriesKey joins label values with a byte that cannot appear in valid UTF-8,
// so no two distinct label sets can collide on one key.
func seriesKey(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.Join(values, "\xff")
}

// snapshotSeries returns the family's series sorted by label values, so both
// the exposition and the expvar output are stable across scrapes. Prometheus
// does not require ordering within a family, but unstable output makes diffs
// and tests useless.
func (f *family) snapshotSeries() []*series {
	f.mu.RLock()
	out := make([]*series, 0, len(f.series))
	for _, s := range f.series {
		out = append(out, s)
	}
	f.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return seriesKey(out[i].labelValues) < seriesKey(out[j].labelValues)
	})
	return out
}

func (r *Registry) snapshotFamilies() []*family {
	r.mu.RLock()
	out := make([]*family, 0, len(r.families))
	for _, f := range r.families {
		out = append(out, f)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// ---------- counters ----------

// CounterVec is a counter family. Counters only go up; a process restart
// resetting one to zero is expected, and is what rate() is built to absorb.
type CounterVec struct{ f *family }

// NewCounter declares a counter family. Calling it again with the same name,
// kind and label names returns the same family.
func (r *Registry) NewCounter(name, help string, labelNames ...string) *CounterVec {
	return &CounterVec{f: r.declare(name, help, kindCounter, nil, labelNames)}
}

// With returns the counter for one label combination, creating it on first use.
func (c *CounterVec) With(labelValues ...string) *Counter {
	return &Counter{s: c.f.get(labelValues)}
}

// Inc adds one to the counter for these label values.
func (c *CounterVec) Inc(labelValues ...string) { c.f.get(labelValues).value.add(1) }

// Counter is a single counter series.
type Counter struct{ s *series }

// Inc adds one.
func (c *Counter) Inc() { c.s.value.add(1) }

// Add adds delta, which must not be negative.
func (c *Counter) Add(delta float64) {
	if delta < 0 {
		panic("observability: counter cannot decrease")
	}
	c.s.value.add(delta)
}

// Value returns the current count.
func (c *Counter) Value() float64 { return c.s.value.load() }

// ---------- gauges ----------

// GaugeVec is a gauge family: a value that may move both ways, such as the
// number of RPCs in flight.
type GaugeVec struct{ f *family }

// NewGauge declares a gauge family.
func (r *Registry) NewGauge(name, help string, labelNames ...string) *GaugeVec {
	return &GaugeVec{f: r.declare(name, help, kindGauge, nil, labelNames)}
}

// With returns the gauge for one label combination, creating it on first use.
func (g *GaugeVec) With(labelValues ...string) *Gauge {
	return &Gauge{s: g.f.get(labelValues)}
}

// Gauge is a single gauge series.
type Gauge struct{ s *series }

// Set replaces the value.
func (g *Gauge) Set(v float64) { g.s.value.store(v) }

// Add moves the value by delta, which may be negative.
func (g *Gauge) Add(delta float64) { g.s.value.add(delta) }

// Inc adds one.
func (g *Gauge) Inc() { g.s.value.add(1) }

// Dec subtracts one.
func (g *Gauge) Dec() { g.s.value.add(-1) }

// Value returns the current value.
func (g *Gauge) Value() float64 { return g.s.value.load() }

// ---------- histograms ----------

// HistogramVec is a histogram family.
type HistogramVec struct{ f *family }

// NewHistogram declares a histogram family. Passing nil buckets uses
// DefaultLatencyBuckets. Bounds are sorted and de-duplicated; the +Inf bound is
// implicit and must not be passed.
func (r *Registry) NewHistogram(name, help string, buckets []float64, labelNames ...string) *HistogramVec {
	bounds := normalizeBuckets(buckets)
	return &HistogramVec{f: r.declare(name, help, kindHistogram, bounds, labelNames)}
}

// With returns the histogram for one label combination, creating it on first use.
func (h *HistogramVec) With(labelValues ...string) *Histogram {
	return &Histogram{f: h.f, s: h.f.get(labelValues)}
}

// Observe records one value against these label values.
func (h *HistogramVec) Observe(v float64, labelValues ...string) {
	h.With(labelValues...).Observe(v)
}

// Histogram is a single histogram series.
type Histogram struct {
	f *family
	s *series
}

// Observe records one value.
func (h *Histogram) Observe(v float64) {
	// Linear scan. With a dozen bounds this beats a binary search, and it keeps
	// the hot path branch-predictable: RPC latencies cluster in the low buckets.
	i := 0
	for ; i < len(h.f.bounds); i++ {
		if v <= h.f.bounds[i] {
			break
		}
	}
	h.s.buckets[i].Add(1)
	h.s.sum.add(v)
}

// Count returns the number of observations.
func (h *Histogram) Count() uint64 { return h.s.count() }

// Sum returns the sum of all observed values.
func (h *Histogram) Sum() float64 { return h.s.sum.load() }

func (s *series) count() uint64 {
	var total uint64
	for i := range s.buckets {
		total += s.buckets[i].Load()
	}
	return total
}

func normalizeBuckets(buckets []float64) []float64 {
	if len(buckets) == 0 {
		buckets = DefaultLatencyBuckets
	}
	out := make([]float64, 0, len(buckets))
	for _, b := range buckets {
		if math.IsNaN(b) || math.IsInf(b, 1) {
			// +Inf is always appended at render time; an explicit one here would
			// emit a duplicate le="+Inf" line, which a scraper reads as a
			// malformed histogram.
			continue
		}
		out = append(out, b)
	}
	sort.Float64s(out)
	dedup := out[:0]
	for i, b := range out {
		if i == 0 || b != out[i-1] {
			dedup = append(dedup, b)
		}
	}
	return dedup
}

// ---------- atomics ----------

// atomicFloat is a float64 behind a CAS loop. sync/atomic has no float64, and a
// mutex per series would put every RPC on the same lock.
type atomicFloat struct{ bits atomic.Uint64 }

func (f *atomicFloat) add(delta float64) {
	for {
		old := f.bits.Load()
		next := math.Float64bits(math.Float64frombits(old) + delta)
		if f.bits.CompareAndSwap(old, next) {
			return
		}
	}
}

func (f *atomicFloat) store(v float64) { f.bits.Store(math.Float64bits(v)) }

func (f *atomicFloat) load() float64 { return math.Float64frombits(f.bits.Load()) }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
