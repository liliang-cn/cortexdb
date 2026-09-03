package rpcserver

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// Metric names. They are constants because a dashboard, an alert rule and a
// scrape config all name them from the outside: renaming one is a breaking
// change to somebody's paging setup, not a refactor.
const (
	MetricRequestsTotal    = "cortexdb_grpc_requests_total"
	MetricErrorsTotal      = "cortexdb_grpc_errors_total"
	MetricRequestDuration  = "cortexdb_grpc_request_duration_seconds"
	MetricRequestsInFlight = "cortexdb_grpc_requests_in_flight"
)

// Label names.
//
// Cardinality discipline: these two are the whole list, and the line is that a
// label may only take values from a set fixed at compile time. grpc_method is
// bounded by the service definitions in proto/ — a call to a method the server
// does not implement is rejected by grpc-go before any interceptor runs, so an
// attacker cannot mint new series by dialling nonsense. grpc_code is bounded by
// the seventeen codes in google.golang.org/grpc/codes.
//
// Nothing derived from a request body ever becomes a label. Not a user id, not
// a scope, not a collection name, not a memory id, and above all not query
// text. Each distinct value of a label is a permanent time series in the
// scraper: one endpoint labelled by query text takes down the Prometheus that
// scrapes it, not the server that emits it, which is why this mistake is
// usually discovered by somebody else's on-call.
const (
	LabelMethod = "grpc_method"
	LabelCode   = "grpc_code"
)

// grpcMetrics is the set of instruments the interceptor records into. Declaring
// them once at construction keeps the per-RPC path to map lookups on already
// validated names.
type grpcMetrics struct {
	requests *observability.CounterVec
	errors   *observability.CounterVec
	duration *observability.HistogramVec
	inFlight *observability.GaugeVec
}

func newGRPCMetrics(reg *observability.Registry) *grpcMetrics {
	return &grpcMetrics{
		requests: reg.NewCounter(MetricRequestsTotal,
			"Total unary gRPC requests received, by method.", LabelMethod),
		errors: reg.NewCounter(MetricErrorsTotal,
			"Total unary gRPC requests that returned a non-OK status, by method and status code.", LabelMethod, LabelCode),
		duration: reg.NewHistogram(MetricRequestDuration,
			"Unary gRPC handler latency in seconds, by method.", nil, LabelMethod),
		inFlight: reg.NewGauge(MetricRequestsInFlight,
			"Unary gRPC requests currently being handled, by method.", LabelMethod),
	}
}

// MetricsInterceptor returns a unary interceptor recording request counts,
// errors by gRPC status code, in-flight requests and handler latency into reg.
//
// It is exported rather than wired into New so a caller can chain it with the
// auth interceptor in whatever order it wants. Putting metrics outermost counts
// requests that auth rejects, which is usually what an operator wants to see;
// putting it innermost measures only work actually done.
func MetricsInterceptor(reg *observability.Registry) grpc.UnaryServerInterceptor {
	return MetricsInterceptorWithTracer(reg, nil)
}

// MetricsInterceptorWithTracer is MetricsInterceptor plus a span per RPC. A nil
// tracer means no tracing at all: observability.NopTracer returns the context
// unchanged and a zero-size span, so an unconfigured tracer costs a nil check
// and no allocation.
func MetricsInterceptorWithTracer(reg *observability.Registry, tracer observability.Tracer) grpc.UnaryServerInterceptor {
	m := newGRPCMetrics(reg)
	tr := observability.TracerOrNop(tracer)

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		method := info.FullMethod
		m.requests.Inc(method)
		inFlight := m.inFlight.With(method)
		inFlight.Inc()

		ctx, span := tr.Start(ctx, spanName(method))
		span.SetAttribute("rpc.system", "grpc")
		span.SetAttribute("rpc.method", method)

		start := time.Now()
		// The deferred close runs on the normal path only: grpc-go does not
		// recover a panicking handler, so a panic takes the process with it and
		// there is no sample left to record correctly.
		defer func() {
			inFlight.Dec()
			m.duration.Observe(time.Since(start).Seconds(), method)
			code := status.Code(err)
			if code != codes.OK {
				m.errors.Inc(method, code.String())
			}
			span.SetAttribute("rpc.grpc.status_code", code.String())
			span.End(err)
		}()

		return handler(ctx, req)
	}
}

// spanName drops the leading slash from a gRPC method, which is the form
// tracing backends expect: cortexdb.v1.MemoryService/Save.
func spanName(fullMethod string) string {
	return strings.TrimPrefix(fullMethod, "/")
}
