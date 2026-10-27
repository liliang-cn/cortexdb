package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// When the embedding host sleeps or drops off the network, recall quietly
// gets worse — keyword answers instead of semantic ones — and nothing used to
// say so. Three things say so now: every call to the embedder is measured
// (meteredEmbedder), the embedder is probed while nobody is recalling
// (probeEmbedderEvery), and /healthz reports it beside the process.

// Embedder metric names.
const (
	MetricEmbedderRequests = "cortexdb_embedder_requests_total"
	MetricEmbedderDuration = "cortexdb_embedder_request_duration_seconds"
	MetricEmbedderUp       = "cortexdb_embedder_up"
	MetricEmbedderProbe    = "cortexdb_embedder_probe_duration_seconds"
)

// meteredEmbedder counts and times every call that reaches the embedding
// endpoint. It is the innermost wrapper, under the facade's query cache, so a
// cache hit — which never reaches the network — is not counted.
type meteredEmbedder struct {
	cortexdb.Embedder
	requests *observability.CounterVec
	duration *observability.HistogramVec
	up       *observability.Gauge
}

func newMeteredEmbedder(e cortexdb.Embedder, reg *observability.Registry) *meteredEmbedder {
	return &meteredEmbedder{
		Embedder: e,
		requests: reg.NewCounter(MetricEmbedderRequests,
			"Calls to the embedding endpoint, by outcome (ok or error) and kind (single or batch).", "outcome", "kind"),
		duration: reg.NewHistogram(MetricEmbedderDuration,
			"Embedding endpoint latency in seconds, by kind.", nil, "kind"),
		up: reg.NewGauge(MetricEmbedderUp,
			"1 when the last call to the embedder succeeded, 0 when it failed.").With(),
	}
}

func (m *meteredEmbedder) observe(kind string, start time.Time, err error) {
	// A caller that gave up says nothing about the endpoint.
	if err != nil && (errors.Is(err, context.Canceled) || strings.Contains(err.Error(), context.Canceled.Error())) {
		return
	}
	m.duration.Observe(time.Since(start).Seconds(), kind)
	if err != nil {
		m.requests.Inc("error", kind)
		m.up.Set(0)
		return
	}
	m.requests.Inc("ok", kind)
	m.up.Set(1)
}

func (m *meteredEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	start := time.Now()
	v, err := m.Embedder.Embed(ctx, text)
	m.observe("single", start, err)
	return v, err
}

func (m *meteredEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	start := time.Now()
	v, err := m.Embedder.EmbedBatch(ctx, texts)
	m.observe("batch", start, err)
	return v, err
}

// probeEmbedderEvery probes the embedder every interval until ctx is done,
// recording the probe's latency and logging a change of state.
func probeEmbedderEvery(ctx context.Context, db *cortexdb.DB, reg *observability.Registry, interval, timeout time.Duration) {
	probe := reg.NewHistogram(MetricEmbedderProbe, "Latency of the periodic embedder probe in seconds.", nil).With()
	healthy := true
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		took, err := db.ProbeEmbedder(ctx, timeout)
		if ctx.Err() != nil {
			return
		}
		probe.Observe(took.Seconds())
		switch {
		case err != nil && healthy:
			slog.Warn("embedder unreachable; recall answers by keyword until it is back", "err", err.Error())
		case err == nil && !healthy:
			slog.Info("embedder reachable again", "took_ms", took.Milliseconds())
		}
		healthy = err == nil
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// healthReport is what /healthz answers.
type healthReport struct {
	// Status is "ok", or "degraded" when the process serves but the embedder
	// is failing: saves and recalls work, recalls by keyword only.
	Status   string                  `json:"status"`
	Version  string                  `json:"version"`
	DB       string                  `json:"db"`
	Embedder cortexdb.EmbedderStatus `json:"embedder"`
	Error    string                  `json:"error,omitempty"`
}

// healthHandler answers /healthz. It is 200 while the process serves —
// degraded included, because a load balancer that pulled a brain whose
// embedder is down would leave callers with no answer instead of a keyword
// one — unless ?strict=1 asks for 503 on degraded too. It reads no request
// data and names no row, so it needs no key.
func healthHandler(db *cortexdb.DB, dbPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		rep := healthReport{Status: "ok", Version: cortexdbroot.Version, DB: dbPath}
		st, err := db.EmbedderStatus(ctx)
		rep.Embedder = st
		code := http.StatusOK
		if err != nil {
			rep.Status, rep.Error, code = "error", err.Error(), http.StatusServiceUnavailable
		} else if st.Configured && !st.Healthy {
			rep.Status = "degraded"
			if r.URL.Query().Get("strict") == "1" {
				code = http.StatusServiceUnavailable
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(rep)
	})
}
