package rpcserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// methodInfo lives in auth_test.go — same package, same one-line helper. Two
// agents wrote it independently; one copy is enough.

func failing(code codes.Code, msg string) grpc.UnaryHandler {
	return func(context.Context, any) (any, error) { return nil, status.Error(code, msg) }
}

// counterValue reads one series out of the registry through its public
// snapshot, which is what a scraper would see — not through an internal field.
func counterValue(t *testing.T, r *observability.Registry, metric, labels string) float64 {
	t.Helper()
	snap := r.Snapshot()
	family, ok := snap[metric].(map[string]any)
	if !ok {
		return 0
	}
	v, ok := family[labels].(float64)
	if !ok {
		return 0
	}
	return v
}

func TestTheInterceptorCountsASuccessAndAnErrorSeparately(t *testing.T) {
	r := observability.NewRegistry()
	ic := MetricsInterceptor(r)
	const method = "/cortexdb.v1.MemoryService/Save"

	if _, err := ic(context.Background(), nil, methodInfo(method), passthrough); err != nil {
		t.Fatalf("success path returned %v", err)
	}
	if _, err := ic(context.Background(), nil, methodInfo(method), failing(codes.NotFound, "gone")); err == nil {
		t.Fatal("error path returned no error")
	}

	labels := fmt.Sprintf("{grpc_method=%q}", method)
	if got := counterValue(t, r, MetricRequestsTotal, labels); got != 2 {
		t.Errorf("%s = %v, want 2 — both calls are requests", MetricRequestsTotal, got)
	}
	errLabels := fmt.Sprintf("{grpc_method=%q,grpc_code=%q}", method, "NotFound")
	if got := counterValue(t, r, MetricErrorsTotal, errLabels); got != 1 {
		t.Errorf("%s%s = %v, want 1", MetricErrorsTotal, errLabels, got)
	}
	// A success must not create an errors series at all: an errors_total that is
	// present and zero for every method is noise on every dashboard.
	okLabels := fmt.Sprintf("{grpc_method=%q,grpc_code=%q}", method, "OK")
	if _, present := r.Snapshot()[MetricErrorsTotal].(map[string]any)[okLabels]; present {
		t.Errorf("a grpc_code=\"OK\" series was created")
	}
}

func TestEachStatusCodeGetsItsOwnErrorSeries(t *testing.T) {
	r := observability.NewRegistry()
	ic := MetricsInterceptor(r)
	const method = "/cortexdb.v1.KnowledgeService/Search"

	for _, code := range []codes.Code{codes.NotFound, codes.NotFound, codes.Unauthenticated, codes.Internal} {
		_, _ = ic(context.Background(), nil, methodInfo(method), failing(code, "x"))
	}

	for code, want := range map[string]float64{
		"NotFound":        2,
		"Unauthenticated": 1,
		"Internal":        1,
		"Unavailable":     0,
	} {
		labels := fmt.Sprintf("{grpc_method=%q,grpc_code=%q}", method, code)
		if got := counterValue(t, r, MetricErrorsTotal, labels); got != want {
			t.Errorf("errors for %s = %v, want %v", code, got, want)
		}
	}
}

func TestAPlainErrorIsRecordedAsUnknown(t *testing.T) {
	// A handler that returns a bare error, not a status.Error, is what most bugs
	// look like. gRPC maps it to Unknown on the wire, and the metric has to
	// agree with the wire or an operator correlating the two goes in circles.
	r := observability.NewRegistry()
	ic := MetricsInterceptor(r)
	const method = "/cortexdb.v1.ToolsService/Call"

	handler := func(context.Context, any) (any, error) { return nil, errors.New("boom") }
	if _, err := ic(context.Background(), nil, methodInfo(method), handler); err == nil {
		t.Fatal("want the error passed through")
	}

	labels := fmt.Sprintf("{grpc_method=%q,grpc_code=%q}", method, "Unknown")
	if got := counterValue(t, r, MetricErrorsTotal, labels); got != 1 {
		t.Fatalf("errors for Unknown = %v, want 1", got)
	}
}

func TestTheInterceptorRecordsLatencyAndLeavesNothingInFlight(t *testing.T) {
	r := observability.NewRegistry()
	ic := MetricsInterceptor(r)
	const method = "/cortexdb.v1.AdminService/Health"

	_, _ = ic(context.Background(), nil, methodInfo(method), passthrough)
	_, _ = ic(context.Background(), nil, methodInfo(method), failing(codes.Internal, "x"))

	labels := fmt.Sprintf("{grpc_method=%q}", method)
	hist, ok := r.Snapshot()[MetricRequestDuration].(map[string]any)[labels].(map[string]any)
	if !ok {
		t.Fatalf("no latency histogram for %s: %v", method, r.Snapshot()[MetricRequestDuration])
	}
	if hist["count"].(uint64) != 2 {
		t.Errorf("latency count = %v, want 2 — a failed RPC still took time", hist["count"])
	}
	// The failure path must be timed too, and the in-flight gauge must come back
	// to zero whichever way the handler returned.
	if got := counterValue(t, r, MetricRequestsInFlight, labels); got != 0 {
		t.Errorf("%s = %v, want 0", MetricRequestsInFlight, got)
	}
}

func TestTheInterceptorLabelsOnlyMethodAndCode(t *testing.T) {
	// Cardinality is the property that decides whether this endpoint is safe in
	// production, so it is asserted rather than left to review: the exposition
	// must contain no label name beyond the two bounded ones.
	r := observability.NewRegistry()
	ic := MetricsInterceptor(r)
	_, _ = ic(context.Background(), "a request body full of user text", methodInfo("/cortexdb.v1.MemoryService/Save"), passthrough)
	_, _ = ic(context.Background(), nil, methodInfo("/cortexdb.v1.MemoryService/Save"), failing(codes.Internal, "secret detail"))

	var buf strings.Builder
	if err := r.WriteText(&buf); err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	for _, line := range strings.Split(text, "\n") {
		open := strings.IndexByte(line, '{')
		if strings.HasPrefix(line, "#") || open < 0 {
			continue
		}
		labels := line[open+1 : strings.LastIndexByte(line, '}')]
		for _, pair := range strings.Split(labels, `","`) {
			name, _, _ := strings.Cut(pair, "=")
			name = strings.TrimLeft(name, `,"`)
			switch name {
			case LabelMethod, LabelCode, "le":
			default:
				t.Errorf("unexpected label %q in %q", name, line)
			}
		}
	}
	if strings.Contains(text, "secret detail") || strings.Contains(text, "user text") {
		t.Errorf("request or error content leaked into the exposition:\n%s", text)
	}
}

func TestTheInterceptorTracesEveryRPCAndReportsTheStatusCode(t *testing.T) {
	tr := &captureTracer{}
	ic := MetricsInterceptorWithTracer(observability.NewRegistry(), tr)

	_, _ = ic(context.Background(), nil, methodInfo("/cortexdb.v1.MemoryService/Save"), passthrough)
	_, _ = ic(context.Background(), nil, methodInfo("/cortexdb.v1.MemoryService/Save"), failing(codes.PermissionDenied, "no"))

	if len(tr.spans) != 2 {
		t.Fatalf("started %d spans, want 2", len(tr.spans))
	}
	// The leading slash is dropped: tracing backends name gRPC spans
	// service/method, and a stray slash shows up in every trace search.
	if tr.spans[0].name != "cortexdb.v1.MemoryService/Save" {
		t.Errorf("span name = %q", tr.spans[0].name)
	}
	if tr.spans[0].attrs["rpc.grpc.status_code"] != "OK" || tr.spans[0].err != nil {
		t.Errorf("success span = %+v", tr.spans[0])
	}
	if tr.spans[1].attrs["rpc.grpc.status_code"] != "PermissionDenied" || tr.spans[1].err == nil {
		t.Errorf("failure span = %+v", tr.spans[1])
	}
	for i, s := range tr.spans {
		if !s.ended {
			t.Errorf("span %d was never ended", i)
		}
	}
}

func TestTheInterceptorPassesTheTracersContextToTheHandler(t *testing.T) {
	tr := &captureTracer{}
	ic := MetricsInterceptorWithTracer(observability.NewRegistry(), tr)

	var seen any
	handler := func(ctx context.Context, _ any) (any, error) {
		seen = ctx.Value(captureKey{})
		return "ok", nil
	}
	_, _ = ic(context.Background(), nil, methodInfo("/cortexdb.v1.AdminService/Health"), handler)

	if seen != "cortexdb.v1.AdminService/Health" {
		t.Fatalf("handler saw context value %v — the tracer's context was not threaded through, so a backend could not link spans", seen)
	}
}

func TestAnUnsetTracerCostsTheInterceptorNothing(t *testing.T) {
	ic := MetricsInterceptor(observability.NewRegistry())
	info := methodInfo("/cortexdb.v1.AdminService/Health")
	// No panic and no tracer is the point; the interceptor must not need one.
	for i := 0; i < 3; i++ {
		if _, err := ic(context.Background(), nil, info, passthrough); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

func TestConcurrentRPCsAreCountedExactly(t *testing.T) {
	const (
		goroutines = 32
		iterations = 100
	)
	r := observability.NewRegistry()
	ic := MetricsInterceptor(r)
	info := methodInfo("/cortexdb.v1.KnowledgeService/Search")

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if (g+i)%2 == 0 {
					_, _ = ic(context.Background(), nil, info, passthrough)
				} else {
					_, _ = ic(context.Background(), nil, info, failing(codes.Internal, "x"))
				}
			}
		}(g)
	}
	wg.Wait()

	labels := `{grpc_method="/cortexdb.v1.KnowledgeService/Search"}`
	if got, want := counterValue(t, r, MetricRequestsTotal, labels), float64(goroutines*iterations); got != want {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	errs := counterValue(t, r, MetricErrorsTotal, `{grpc_method="/cortexdb.v1.KnowledgeService/Search",grpc_code="Internal"}`)
	if errs != float64(goroutines*iterations/2) {
		t.Fatalf("errors = %v, want %v", errs, goroutines*iterations/2)
	}
	if got := counterValue(t, r, MetricRequestsInFlight, labels); got != 0 {
		t.Fatalf("in flight = %v, want 0", got)
	}
}

// captureTracer records what the interceptor did, in the shape an OTel adapter
// would see it.
type captureTracer struct {
	mu    sync.Mutex
	spans []*captureSpan
}

type captureKey struct{}

type captureSpan struct {
	name  string
	attrs map[string]string
	err   error
	ended bool
}

func (c *captureTracer) Start(ctx context.Context, name string) (context.Context, observability.Span) {
	s := &captureSpan{name: name, attrs: map[string]string{}}
	c.mu.Lock()
	c.spans = append(c.spans, s)
	c.mu.Unlock()
	return context.WithValue(ctx, captureKey{}, name), s
}

func (s *captureSpan) SetAttribute(k, v string) { s.attrs[k] = v }

func (s *captureSpan) End(err error) {
	s.err = err
	s.ended = true
}
