package observability

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// render is the shape every exposition test starts from.
func render(t *testing.T, r *Registry) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.WriteText(&buf); err != nil {
		t.Fatalf("write exposition: %v", err)
	}
	return buf.String()
}

// parsed is a minimal reader for the text format: enough to assert the things a
// scraper would reject, without pulling in a parser dependency to test a
// package whose whole point is not having one.
type parsed struct {
	help    map[string]string
	typ     map[string]string
	samples map[string]float64 // full sample line up to the space -> value
	order   []string
}

func parseExposition(t *testing.T, text string) parsed {
	t.Helper()
	p := parsed{
		help:    map[string]string{},
		typ:     map[string]string{},
		samples: map[string]float64{},
	}
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# HELP "):
			rest := strings.TrimPrefix(line, "# HELP ")
			name, help, _ := strings.Cut(rest, " ")
			p.help[name] = help
		case strings.HasPrefix(line, "# TYPE "):
			rest := strings.TrimPrefix(line, "# TYPE ")
			name, typ, ok := strings.Cut(rest, " ")
			if !ok {
				t.Fatalf("malformed TYPE line: %q", line)
			}
			p.typ[name] = typ
		case strings.HasPrefix(line, "#"):
			t.Fatalf("unexpected comment line: %q", line)
		default:
			// A sample is "<name>[{labels}] <value>", and the value is the last
			// space-separated field. Label values may contain spaces, so the
			// split has to come from the right.
			idx := strings.LastIndex(line, " ")
			if idx < 0 {
				t.Fatalf("malformed sample line: %q", line)
			}
			key, raw := line[:idx], line[idx+1:]
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				t.Fatalf("sample %q has unparseable value %q: %v", key, raw, err)
			}
			if _, dup := p.samples[key]; dup {
				t.Fatalf("duplicate sample line %q — a scraper rejects the whole scrape for this", key)
			}
			p.samples[key] = v
			p.order = append(p.order, key)
		}
	}
	return p
}

func TestAnEmptyRegistryRendersNothing(t *testing.T) {
	if got := render(t, NewRegistry()); got != "" {
		t.Fatalf("want empty exposition, got %q", got)
	}
}

func TestEveryFamilyCarriesHelpAndType(t *testing.T) {
	r := NewRegistry()
	r.NewCounter("cortexdb_requests_total", "Requests received.", "method").Inc("Search")
	r.NewGauge("cortexdb_open_connections", "Connections currently open.").With().Set(3)
	r.NewHistogram("cortexdb_latency_seconds", "Handler latency.", []float64{0.1}, "method").Observe(0.05, "Search")

	p := parseExposition(t, render(t, r))
	for name, wantType := range map[string]string{
		"cortexdb_requests_total":   "counter",
		"cortexdb_open_connections": "gauge",
		"cortexdb_latency_seconds":  "histogram",
	} {
		if p.typ[name] != wantType {
			t.Errorf("# TYPE %s = %q, want %q", name, p.typ[name], wantType)
		}
		if p.help[name] == "" {
			t.Errorf("# HELP %s is missing", name)
		}
	}
}

func TestAFamilyWithNoSeriesStillAnnouncesItself(t *testing.T) {
	// An operator checking whether a build exports a metric should not have to
	// generate traffic first to find out.
	r := NewRegistry()
	r.NewCounter("cortexdb_requests_total", "Requests received.", "method")

	text := render(t, r)
	if !strings.Contains(text, "# TYPE cortexdb_requests_total counter") {
		t.Fatalf("want a TYPE line for an unused family, got:\n%s", text)
	}
	if strings.Contains(text, "cortexdb_requests_total{") {
		t.Fatalf("want no samples for an unused family, got:\n%s", text)
	}
}

func TestAHistogramsInfBucketEqualsItsCount(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogram("cortexdb_latency_seconds", "Handler latency.", []float64{0.01, 0.1, 1}, "method")
	for _, v := range []float64{0.005, 0.05, 0.5, 5, 50} {
		h.Observe(v, "Search")
	}

	p := parseExposition(t, render(t, r))
	inf := p.samples[`cortexdb_latency_seconds_bucket{method="Search",le="+Inf"}`]
	count := p.samples[`cortexdb_latency_seconds_count{method="Search"}`]
	if inf != count {
		t.Fatalf("+Inf bucket %v != _count %v", inf, count)
	}
	if count != 5 {
		t.Fatalf("_count = %v, want 5", count)
	}
}

func TestHistogramBucketsAreCumulativeAndSumIsExact(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogram("cortexdb_latency_seconds", "Handler latency.", []float64{0.01, 0.1, 1}, "method")
	for _, v := range []float64{0.005, 0.05, 0.5, 5, 50} {
		h.Observe(v, "Search")
	}

	p := parseExposition(t, render(t, r))
	want := map[string]float64{
		`cortexdb_latency_seconds_bucket{method="Search",le="0.01"}`: 1,
		`cortexdb_latency_seconds_bucket{method="Search",le="0.1"}`:  2,
		`cortexdb_latency_seconds_bucket{method="Search",le="1"}`:    3,
		`cortexdb_latency_seconds_bucket{method="Search",le="+Inf"}`: 5,
	}
	for line, wantVal := range want {
		got, ok := p.samples[line]
		if !ok {
			t.Fatalf("missing sample %q", line)
		}
		if got != wantVal {
			t.Errorf("%s = %v, want %v", line, got, wantVal)
		}
	}
	// The sum is a float64 accumulation, so it is compared with a tolerance
	// rather than to a decimal literal that has no exact binary form.
	if sum := p.samples[`cortexdb_latency_seconds_sum{method="Search"}`]; math.Abs(sum-55.555) > 1e-9 {
		t.Errorf("_sum = %v, want 55.555", sum)
	}
	// Buckets must never decrease going up, or a scraper computes negative
	// counts between adjacent buckets and quantile estimates go nonsense.
	prev := 0.0
	for _, le := range []string{"0.01", "0.1", "1", "+Inf"} {
		v := p.samples[fmt.Sprintf(`cortexdb_latency_seconds_bucket{method="Search",le=%q}`, le)]
		if v < prev {
			t.Fatalf("bucket le=%s (%v) is below the previous one (%v)", le, v, prev)
		}
		prev = v
	}
}

func TestTheLeLabelComesLastAndBoundsAreDeduplicatedAndSorted(t *testing.T) {
	r := NewRegistry()
	r.NewHistogram("cortexdb_latency_seconds", "Handler latency.", []float64{1, 0.01, 1, 0.1}, "method").
		Observe(0.5, "Search")

	p := parseExposition(t, render(t, r))
	var les []string
	for _, key := range p.order {
		if strings.HasPrefix(key, "cortexdb_latency_seconds_bucket") {
			_, after, _ := strings.Cut(key, "le=\"")
			le, _, _ := strings.Cut(after, "\"")
			les = append(les, le)
			if !strings.HasSuffix(key, fmt.Sprintf("le=%q}", le)) {
				t.Fatalf("le must be the last label in %q", key)
			}
		}
	}
	want := []string{"0.01", "0.1", "1", "+Inf"}
	if strings.Join(les, ",") != strings.Join(want, ",") {
		t.Fatalf("bucket bounds = %v, want %v", les, want)
	}
}

func TestAnExplicitInfBucketIsDroppedRatherThanDuplicated(t *testing.T) {
	r := NewRegistry()
	r.NewHistogram("cortexdb_latency_seconds", "Handler latency.",
		[]float64{0.1, math.Inf(1)}, "method").Observe(0.5, "Search")

	// parseExposition fails the test on a duplicate sample line, which is what
	// a second le="+Inf" would produce.
	p := parseExposition(t, render(t, r))
	if _, ok := p.samples[`cortexdb_latency_seconds_bucket{method="Search",le="+Inf"}`]; !ok {
		t.Fatal("missing the implicit +Inf bucket")
	}
}

func TestALabelValueWithQuotesBackslashesAndNewlinesIsEscaped(t *testing.T) {
	r := NewRegistry()
	r.NewCounter("cortexdb_requests_total", "Requests received.", "method").
		Inc("a\"b\\c\nd")

	text := render(t, r)
	wantLine := `cortexdb_requests_total{method="a\"b\\c\nd"} 1`
	if !strings.Contains(text, wantLine) {
		t.Fatalf("want escaped line %q, got:\n%s", wantLine, text)
	}
	// The whole family must still be two comment lines and exactly one sample:
	// an unescaped newline would silently split it into two.
	if n := strings.Count(strings.TrimRight(text, "\n"), "\n"); n != 2 {
		t.Fatalf("want 3 lines total, got %d newlines in:\n%s", n, text)
	}
	parseExposition(t, text)
}

func TestAHelpStringWithBackslashesAndNewlinesIsEscapedButKeepsQuotes(t *testing.T) {
	r := NewRegistry()
	r.NewCounter("cortexdb_requests_total", "a\\b\nc \"quoted\"")

	text := render(t, r)
	want := `# HELP cortexdb_requests_total a\\b\nc "quoted"`
	if !strings.Contains(text, want) {
		t.Fatalf("want %q, got:\n%s", want, text)
	}
}

func TestAMetricWithNoLabelsRendersWithoutBraces(t *testing.T) {
	r := NewRegistry()
	r.NewGauge("cortexdb_open_connections", "Connections currently open.").With().Set(7)

	text := render(t, r)
	if !strings.Contains(text, "cortexdb_open_connections 7\n") {
		t.Fatalf("want a braceless sample, got:\n%s", text)
	}
}

func TestSpecialFloatsRenderAsPrometheusSpellsThem(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{math.Inf(1), "+Inf"},
		{math.Inf(-1), "-Inf"},
		{math.NaN(), "NaN"},
		{0.0001, "0.0001"},
		{1, "1"},
		{1e21, "1e+21"},
	} {
		if got := formatValue(tc.in); got != tc.want {
			t.Errorf("formatValue(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOutputIsStableAcrossScrapes(t *testing.T) {
	// Map iteration order must not leak into the exposition: unstable output
	// makes a diff of two scrapes useless for a human debugging a live server.
	r := NewRegistry()
	c := r.NewCounter("cortexdb_requests_total", "Requests received.", "method")
	for _, m := range []string{"Search", "Save", "Health", "Recall"} {
		c.Inc(m)
	}
	r.NewGauge("cortexdb_open_connections", "Connections currently open.").With().Set(1)

	first := render(t, r)
	for i := 0; i < 20; i++ {
		if got := render(t, r); got != first {
			t.Fatalf("scrape %d differs:\n%s\n---\n%s", i, first, got)
		}
	}
	// Families are ordered by name, so the gauge (c…o) precedes the counter (c…r).
	if strings.Index(first, "cortexdb_open_connections") > strings.Index(first, "cortexdb_requests_total") {
		t.Fatalf("families are not sorted by name:\n%s", first)
	}
}

func TestInvalidMetricAndLabelNamesArePreventedAtDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func()
	}{
		{"metric name with a dash", func() { NewRegistry().NewCounter("cortexdb-requests", "h") }},
		{"metric name starting with a digit", func() { NewRegistry().NewCounter("1_requests", "h") }},
		{"empty metric name", func() { NewRegistry().NewCounter("", "h") }},
		{"label name with a dot", func() { NewRegistry().NewCounter("ok_total", "h", "grpc.method") }},
		{"reserved __ label prefix", func() { NewRegistry().NewCounter("ok_total", "h", "__name__") }},
		{"le on a non-histogram", func() { NewRegistry().NewCounter("ok_total", "h", "le") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("want a panic at declaration time")
				}
			}()
			tc.fn()
		})
	}
}

func TestRedeclaringAFamilyWithADifferentShapeIsRefused(t *testing.T) {
	r := NewRegistry()
	a := r.NewCounter("cortexdb_requests_total", "Requests received.", "method")
	b := r.NewCounter("cortexdb_requests_total", "Requests received.", "method")
	if a.f != b.f {
		t.Fatal("an identical redeclaration should return the same family")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("want a panic when the label names differ")
		}
	}()
	r.NewCounter("cortexdb_requests_total", "Requests received.", "method", "code")
}

func TestPassingTheWrongNumberOfLabelValuesIsRefused(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic on label arity mismatch")
		}
	}()
	NewRegistry().NewCounter("cortexdb_requests_total", "h", "method").Inc()
}

func TestACounterRefusesToGoBackwards(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic on a negative counter delta")
		}
	}()
	NewRegistry().NewCounter("cortexdb_requests_total", "h").With().Add(-1)
}

func TestAGaugeMovesBothWays(t *testing.T) {
	g := NewRegistry().NewGauge("cortexdb_in_flight", "In flight.").With()
	g.Inc()
	g.Inc()
	g.Dec()
	if got := g.Value(); got != 1 {
		t.Fatalf("gauge = %v, want 1", got)
	}
	g.Set(42)
	g.Add(-2)
	if got := g.Value(); got != 40 {
		t.Fatalf("gauge = %v, want 40", got)
	}
}

func TestTheHandlerServesTextFormatAndRefusesWrites(t *testing.T) {
	r := NewRegistry()
	r.NewCounter("cortexdb_requests_total", "Requests received.", "method").Inc("Search")
	h := r.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != contentType {
		t.Fatalf("Content-Type = %q, want %q", ct, contentType)
	}
	parseExposition(t, rec.Body.String())

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /metrics = %d, want 405", rec.Code)
	}
}
