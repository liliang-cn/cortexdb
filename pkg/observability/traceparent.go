package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
)

// The flow a call belongs to.
//
// A request id names one call. One thing a person asks for in SuperLeo
// becomes many calls — the app to agent-go, agent-go to a model and to
// tools, a tool to this brain, the brain to its embedder — and a request id
// joins none of them to the others. The flow is a W3C Trace Context: one
// trace id for all of it, a span id per step, each step naming the span it
// was called from. It travels as the traceparent header (gRPC metadata key,
// HTTP header, or an MCP request's params._meta.traceparent), with
// tracestate passed through untouched.
//
// A call that arrives with a valid traceparent becomes a child span of it: a
// span id of its own, the caller's span as its parent. A call that arrives
// with none, or with one that does not parse, starts a trace of its own, so a
// caller that knows nothing of tracing still gets a trace id it can look up.
// A value that does not parse is dropped, never logged: like a request id, it
// is read before the caller is authenticated.

// TraceparentHeader and TracestateHeader carry a flow across a hop: gRPC
// metadata keys and HTTP headers both ways (a server returns the traceparent
// of the span it ran the call in), and MCP request _meta keys.
const (
	TraceparentHeader = "traceparent"
	TracestateHeader  = "tracestate"
)

// MaxTracestateBytes bounds a tracestate passed through. W3C allows 512
// characters; a longer one, or one outside printable ASCII, is dropped.
const MaxTracestateBytes = 512

// SpanContext is one span of a flow: the trace it belongs to, its own id,
// the flags it was called with and the vendor state passed along with it.
type SpanContext struct {
	// TraceID is 32 lowercase hex characters, not all zero.
	TraceID string
	// SpanID is 16 lowercase hex characters, not all zero.
	SpanID string
	// Flags are the W3C trace-flags; bit 0 is "sampled".
	Flags byte
	// State is the tracestate header, passed on untouched.
	State string
}

// Valid reports whether s has a trace id and a span id.
func (s SpanContext) Valid() bool {
	return validHexID(s.TraceID, 32) && validHexID(s.SpanID, 16)
}

// Traceparent is s as a traceparent header value, or "" when s is not
// Valid.
func (s SpanContext) Traceparent() string {
	if !s.Valid() {
		return ""
	}
	const hexdigits = "0123456789abcdef"
	return "00-" + s.TraceID + "-" + s.SpanID + "-" + string([]byte{hexdigits[s.Flags>>4], hexdigits[s.Flags&0x0f]})
}

// ParseTraceparent reads a traceparent header value strictly, per W3C Trace
// Context: version 00 is exactly version-traceid-spanid-flags; a later
// version may carry more fields after a dash, which are ignored; version ff,
// an all-zero id, upper-case hex or anything else is refused.
func ParseTraceparent(v string) (SpanContext, bool) {
	v = strings.TrimSpace(v)
	if len(v) < 55 {
		return SpanContext{}, false
	}
	version := v[0:2]
	if !isLowerHex(version) || version == "ff" {
		return SpanContext{}, false
	}
	if version == "00" && len(v) != 55 {
		return SpanContext{}, false
	}
	if len(v) > 55 && v[55] != '-' {
		return SpanContext{}, false
	}
	if v[2] != '-' || v[35] != '-' || v[52] != '-' {
		return SpanContext{}, false
	}
	s := SpanContext{TraceID: v[3:35], SpanID: v[36:52]}
	flags := v[53:55]
	if !s.Valid() || !isLowerHex(flags) {
		return SpanContext{}, false
	}
	b, _ := hex.DecodeString(flags)
	s.Flags = b[0]
	return s, true
}

// CleanTracestate returns v when it may be passed on — at most
// MaxTracestateBytes of printable ASCII — and "" otherwise.
func CleanTracestate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > MaxTracestateBytes {
		return ""
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return ""
		}
	}
	return v
}

// ValidTraceID reports whether id is a trace id: 32 lowercase hex, not all
// zero. Readers that take a trace id from a caller (a recall-log filter)
// check it with this.
func ValidTraceID(id string) bool { return validHexID(id, 32) }

// NewTraceID returns a fresh trace id.
func NewTraceID() string { return randomHexID(16) }

// NewSpanID returns a fresh span id.
func NewSpanID() string { return randomHexID(8) }

// newRootSpan starts a trace of its own, sampled.
func newRootSpan() SpanContext {
	return SpanContext{TraceID: NewTraceID(), SpanID: NewSpanID(), Flags: 0x01}
}

// childOf is a new span in parent's trace.
func childOf(parent SpanContext) SpanContext {
	return SpanContext{TraceID: parent.TraceID, SpanID: NewSpanID(), Flags: parent.Flags, State: parent.State}
}

type remoteSpanKey struct{}

// WithTraceparent makes the span traceparent names the parent of what runs
// under ctx: the next WithCall becomes its child, and a library call made
// with ctx records its trace id. A value that does not parse is ignored and
// ctx returned unchanged. tracestate is kept to be passed on when it is
// clean (CleanTracestate).
//
// This is how a process that embeds CortexDB as a library joins it to the
// flow it is serving:
//
//	ctx = observability.WithTraceparent(ctx, r.Header.Get("traceparent"), r.Header.Get("tracestate"))
//	resp, err := db.KnowledgeMemory().Recall(ctx, req)
func WithTraceparent(ctx context.Context, traceparent, tracestate string) context.Context {
	s, ok := ParseTraceparent(traceparent)
	if !ok {
		return ctx
	}
	s.State = CleanTracestate(tracestate)
	return WithParentSpan(ctx, s)
}

// WithParentSpan is WithTraceparent for a span already parsed. An invalid
// span leaves ctx unchanged.
func WithParentSpan(ctx context.Context, parent SpanContext) context.Context {
	if !parent.Valid() {
		return ctx
	}
	return context.WithValue(ctx, remoteSpanKey{}, parent)
}

// ParentSpanFrom is the span WithTraceparent put in ctx, if any.
func ParentSpanFrom(ctx context.Context) (SpanContext, bool) {
	if ctx == nil {
		return SpanContext{}, false
	}
	s, ok := ctx.Value(remoteSpanKey{}).(SpanContext)
	return s, ok
}

// SpanFrom is the span work under ctx runs in: the call's own span when ctx
// has a call, else the parent span WithTraceparent put there (a library
// call runs in its caller's span), else none.
func SpanFrom(ctx context.Context) (SpanContext, bool) {
	if c := CallFrom(ctx); c != nil {
		return c.span, true
	}
	return ParentSpanFrom(ctx)
}

// TraceID is the trace id of the flow ctx belongs to, or "".
func TraceID(ctx context.Context) string {
	s, _ := SpanFrom(ctx)
	return s.TraceID
}

// SpanID is the id of the span ctx runs in, or "".
func SpanID(ctx context.Context) string {
	s, _ := SpanFrom(ctx)
	return s.SpanID
}

// Traceparent is the traceparent to send with a call made under ctx — the
// span ctx runs in, which the callee takes as its parent — or "".
func Traceparent(ctx context.Context) string {
	s, _ := SpanFrom(ctx)
	return s.Traceparent()
}

// Tracestate is the tracestate to send with a call made under ctx, or "".
func Tracestate(ctx context.Context) string {
	s, _ := SpanFrom(ctx)
	return s.State
}

// InjectHTTP sets traceparent (and tracestate, when there is one) on h for
// an outgoing HTTP request made under ctx. A ctx with no flow sets nothing.
func InjectHTTP(ctx context.Context, h http.Header) {
	s, ok := SpanFrom(ctx)
	if !ok || !s.Valid() {
		return
	}
	h.Set(TraceparentHeader, s.Traceparent())
	if s.State != "" {
		h.Set(TracestateHeader, s.State)
	}
}

// LogAttrs are the ids a log line about work under ctx carries: request_id,
// trace_id, span_id and parent_span_id, each when known.
func LogAttrs(ctx context.Context) []slog.Attr {
	var out []slog.Attr
	if c := CallFrom(ctx); c != nil {
		out = append(out, slog.String("request_id", c.id), slog.String("trace_id", c.span.TraceID), slog.String("span_id", c.span.SpanID))
		if c.parentSpanID != "" {
			out = append(out, slog.String("parent_span_id", c.parentSpanID))
		}
		return out
	}
	if s, ok := ParentSpanFrom(ctx); ok {
		out = append(out, slog.String("trace_id", s.TraceID), slog.String("span_id", s.SpanID))
	}
	return out
}

// LogArgs is LogAttrs as slog key-value arguments, followed by args:
//
//	slog.Warn("cortexdb: recall log", observability.LogArgs(ctx, "err", err)...)
func LogArgs(ctx context.Context, args ...any) []any {
	attrs := LogAttrs(ctx)
	out := make([]any, 0, len(attrs)+len(args))
	for _, a := range attrs {
		out = append(out, a)
	}
	return append(out, args...)
}

func randomHexID(n int) string {
	b := make([]byte, n)
	for {
		if _, err := rand.Read(b); err != nil {
			// crypto/rand does not fail on supported platforms; an id of
			// ones is still a valid one.
			for i := range b {
				b[i] = 0x11
			}
		}
		for _, c := range b {
			if c != 0 {
				return hex.EncodeToString(b)
			}
		}
	}
}

func validHexID(s string, n int) bool {
	if len(s) != n || !isLowerHex(s) {
		return false
	}
	return strings.Trim(s, "0") != ""
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
