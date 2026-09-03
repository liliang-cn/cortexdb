package observability

import "context"

// Tracer is the seam a tracing backend plugs into.
//
// It is deliberately an interface this repo owns rather than an import of
// go.opentelemetry.io/otel, for the same reason cortexdb.Embedder is an
// interface rather than an import of an LLM SDK: the dependency belongs to
// whoever wants the feature, not to everyone who links the library. OTel's
// module graph is large and versions on its own schedule; a CortexDB embedded
// in someone else's binary should not drag it in to record a counter.
//
// An adapter is a dozen lines and belongs outside pkg/ — in the caller's
// binary, or in examples/, where the openai-go client already lives:
//
//	type otelTracer struct{ t trace.Tracer }
//
//	func (o otelTracer) Start(ctx context.Context, name string) (context.Context, observability.Span) {
//		ctx, span := o.t.Start(ctx, name)
//		return ctx, otelSpan{span}
//	}
//
//	type otelSpan struct{ s trace.Span }
//
//	func (o otelSpan) SetAttribute(k, v string) { o.s.SetAttributes(attribute.String(k, v)) }
//	func (o otelSpan) End(err error) {
//		if err != nil {
//			o.s.RecordError(err)
//			o.s.SetStatus(codes.Error, err.Error())
//		}
//		o.s.End()
//	}
//
// The returned context must be the one passed down to the handler, so a backend
// that propagates span context through context.Context keeps its parent-child
// links. A tracer that needs nothing from the context returns it unchanged.
type Tracer interface {
	Start(ctx context.Context, name string) (context.Context, Span)
}

// Span is one unit of work. Every Start must be matched by exactly one End.
//
// Attribute values are strings rather than any, because the only attributes
// worth attaching here are the same bounded ones that make acceptable metric
// labels — a method name, a status code — and a typed variant would exist
// mostly to invite unbounded ones.
type Span interface {
	// SetAttribute records a bounded, low-cardinality attribute.
	SetAttribute(key, value string)
	// End finishes the span. A non-nil err marks it failed.
	End(err error)
}

// NopTracer is the default when no tracer is configured. Its Start returns the
// context unchanged and a zero-size span, so an unconfigured tracer allocates
// nothing and the call is a candidate for inlining — the cost of the seam when
// nobody uses it is a nil check.
var NopTracer Tracer = nopTracer{}

type nopTracer struct{}

func (nopTracer) Start(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, nopSpan{}
}

type nopSpan struct{}

func (nopSpan) SetAttribute(string, string) {}
func (nopSpan) End(error)                   {}

// TracerOrNop turns an unset tracer into the no-op one, so call sites can
// unconditionally call Start instead of guarding every span with a nil check
// and getting one of them wrong.
func TracerOrNop(t Tracer) Tracer {
	if t == nil {
		return NopTracer
	}
	return t
}
