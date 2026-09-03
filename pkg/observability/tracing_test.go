package observability

import (
	"context"
	"errors"
	"testing"
)

func TestTheNopTracerAllocatesNothing(t *testing.T) {
	// The seam only earns its place if a deployment that never configures a
	// tracer pays nothing for it. An allocation per RPC here would be a
	// measurable tax on a feature nobody switched on.
	ctx := context.Background()
	allocs := testing.AllocsPerRun(1000, func() {
		c, span := NopTracer.Start(ctx, "cortexdb.v1.MemoryService/Save")
		span.SetAttribute("rpc.system", "grpc")
		span.End(nil)
		_ = c
	})
	if allocs != 0 {
		t.Fatalf("no-op tracing allocated %v times per run, want 0", allocs)
	}
}

func TestTheNopTracerPassesTheContextThroughUnchanged(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "brain")

	got, span := NopTracer.Start(ctx, "anything")
	if got.Value(key{}) != "brain" {
		t.Fatal("the no-op tracer dropped the context")
	}
	// Every method must be safe on the zero span, including End with an error:
	// the interceptor calls them on the failure path too.
	span.SetAttribute("k", "v")
	span.End(errors.New("boom"))
}

func TestAnUnsetTracerBecomesTheNoOpOne(t *testing.T) {
	if TracerOrNop(nil) != NopTracer {
		t.Fatal("a nil tracer should resolve to NopTracer")
	}
	// A nil Tracer variable is what a caller that never wired one passes, and
	// calling Start on it directly would panic — TracerOrNop is the guard that
	// keeps the call sites free of nil checks.
	var unset Tracer
	ctx, span := TracerOrNop(unset).Start(context.Background(), "x")
	span.End(nil)
	if ctx == nil {
		t.Fatal("want a usable context")
	}
}

// recordingTracer is the shape an OTel adapter would take: it wraps a backend's
// tracer, returns a context the backend can propagate, and turns End(err) into
// whatever the backend calls a failed span.
type recordingTracer struct {
	names []string
	attrs map[string]string
	errs  []error
}

type recordingSpan struct{ t *recordingTracer }

func (r *recordingTracer) Start(ctx context.Context, name string) (context.Context, Span) {
	r.names = append(r.names, name)
	return context.WithValue(ctx, recordingSpan{}, name), recordingSpan{t: r}
}

func (s recordingSpan) SetAttribute(k, v string) {
	if s.t.attrs == nil {
		s.t.attrs = map[string]string{}
	}
	s.t.attrs[k] = v
}

func (s recordingSpan) End(err error) { s.t.errs = append(s.t.errs, err) }

func TestAConfiguredTracerSeesTheSpanAndCanPropagateContext(t *testing.T) {
	tr := &recordingTracer{}
	ctx, span := TracerOrNop(tr).Start(context.Background(), "cortexdb.v1.MemoryService/Save")
	span.SetAttribute("rpc.system", "grpc")
	span.End(errors.New("nope"))

	if len(tr.names) != 1 || tr.names[0] != "cortexdb.v1.MemoryService/Save" {
		t.Fatalf("span names = %v", tr.names)
	}
	if ctx.Value(recordingSpan{}) != "cortexdb.v1.MemoryService/Save" {
		t.Fatal("the tracer's context was not returned to the caller, so a backend could not link parent to child")
	}
	if len(tr.errs) != 1 || tr.errs[0] == nil {
		t.Fatalf("End did not carry the error: %v", tr.errs)
	}
	if tr.attrs["rpc.system"] != "grpc" {
		t.Fatalf("attributes = %v", tr.attrs)
	}
}
