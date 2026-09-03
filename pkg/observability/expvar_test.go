package observability

import (
	"encoding/json"
	"expvar"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExpvarShowsTheSameNumbersAsTheScrape(t *testing.T) {
	r := NewRegistry()
	r.NewCounter("cortexdb_requests_total", "Requests received.", "method").Inc("Search")
	r.NewCounter("cortexdb_requests_total", "Requests received.", "method").Inc("Search")
	r.NewGauge("cortexdb_open_connections", "Connections currently open.").With().Set(3)
	h := r.NewHistogram("cortexdb_latency_seconds", "Handler latency.", []float64{0.01, 0.1}, "method")
	h.Observe(0.005, "Search")
	h.Observe(0.5, "Search")

	snap := r.Snapshot()

	counters, ok := snap["cortexdb_requests_total"].(map[string]any)
	if !ok {
		t.Fatalf("counter snapshot has type %T, want a map keyed by labels", snap["cortexdb_requests_total"])
	}
	if got := counters[`{method="Search"}`]; got != 2.0 {
		t.Errorf("counter = %v, want 2", got)
	}
	// A family with no labels is not wrapped in a one-entry map: nobody reading
	// /debug/vars by eye wants to unwrap {} to find a number.
	if got := snap["cortexdb_open_connections"]; got != 3.0 {
		t.Errorf("gauge = %v, want 3", got)
	}

	hist := snap["cortexdb_latency_seconds"].(map[string]any)[`{method="Search"}`].(map[string]any)
	if hist["count"].(uint64) != 2 {
		t.Errorf("histogram count = %v, want 2", hist["count"])
	}
	buckets := hist["buckets"].(map[string]uint64)
	if buckets["0.01"] != 1 || buckets["0.1"] != 1 || buckets["+Inf"] != 2 {
		t.Errorf("buckets = %v, want cumulative 1/1/2", buckets)
	}
	// The same invariant the exposition guarantees must hold here too, or the
	// two views disagree and an operator trusts the wrong one.
	if buckets["+Inf"] != hist["count"].(uint64) {
		t.Errorf("+Inf bucket %v != count %v", buckets["+Inf"], hist["count"])
	}
}

func TestTheExpvarViewIsJSONAndReadsLive(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("cortexdb_requests_total", "Requests received.", "method")
	v := r.ExpvarVar()

	c.Inc("Search")
	first := v.String()

	// expvar.Func is evaluated on read, so a second increment must show up
	// without republishing anything.
	c.Inc("Search")
	second := v.String()
	if first == second {
		t.Fatalf("expvar value did not change after an increment: %s", first)
	}

	var decoded map[string]map[string]float64
	if err := json.Unmarshal([]byte(second), &decoded); err != nil {
		t.Fatalf("expvar value is not valid JSON: %v (%s)", err, second)
	}
	if got := decoded["cortexdb_requests_total"][`{method="Search"}`]; got != 2 {
		t.Fatalf("decoded counter = %v, want 2", got)
	}
}

func TestPublishingTheSameExpvarNameTwiceIsAnErrorNotAPanic(t *testing.T) {
	// expvar.Publish panics on a duplicate, which would turn a second server in
	// one process — the normal shape of a test binary — into a crash.
	r := NewRegistry()
	const name = "cortexdb_observability_test"
	if err := r.PublishExpvar(name); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if err := NewRegistry().PublishExpvar(name); err == nil {
		t.Fatal("want an error on a duplicate publish")
	}
}

func TestPublishedMetricsAppearOnDebugVars(t *testing.T) {
	r := NewRegistry()
	r.NewCounter("cortexdb_requests_total", "Requests received.", "method").Inc("Search")
	const name = "cortexdb_debugvars_test"
	if err := r.PublishExpvar(name); err != nil {
		t.Fatalf("publish: %v", err)
	}

	rec := httptest.NewRecorder()
	expvar.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/vars", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /debug/vars = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, name) || !strings.Contains(body, "cortexdb_requests_total") {
		t.Fatalf("/debug/vars does not carry the registry:\n%s", body)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("/debug/vars is not valid JSON: %v", err)
	}
}
