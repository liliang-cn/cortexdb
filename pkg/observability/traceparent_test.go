package observability

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const (
	testTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	testParentSpan  = "00f067aa0ba902b7"
	testTraceparent = "00-" + testTraceID + "-" + testParentSpan + "-01"
)

func TestParseTraceparentAcceptsW3CValues(t *testing.T) {
	s, ok := ParseTraceparent(testTraceparent)
	if !ok || s.TraceID != testTraceID || s.SpanID != testParentSpan || s.Flags != 1 {
		t.Fatalf("parse: %+v %v", s, ok)
	}
	if s.Traceparent() != testTraceparent {
		t.Fatalf("round trip: %q", s.Traceparent())
	}
	// A later version may carry more fields; they are ignored.
	if s, ok := ParseTraceparent("cc-" + testTraceID + "-" + testParentSpan + "-00-whatever"); !ok || s.Flags != 0 {
		t.Fatalf("future version: %+v %v", s, ok)
	}
}

func TestParseTraceparentRefusesMalformedValues(t *testing.T) {
	for _, v := range []string{
		"",
		"garbage",
		"00-" + strings.ToUpper(testTraceID) + "-" + testParentSpan + "-01",
		"00-00000000000000000000000000000000-" + testParentSpan + "-01",
		"00-" + testTraceID + "-0000000000000000-01",
		"ff-" + testTraceID + "-" + testParentSpan + "-01",
		"00-" + testTraceID + "-" + testParentSpan + "-01-extra",
		"00-" + testTraceID + "-" + testParentSpan + "-zz",
		"00_" + testTraceID + "-" + testParentSpan + "-01",
		"00-" + testTraceID + "-" + testParentSpan + "-01\nlevel=error msg=forged",
		"cc-" + testTraceID + "-" + testParentSpan + "-01x",
	} {
		if s, ok := ParseTraceparent(v); ok {
			t.Errorf("accepted %q as %+v", v, s)
		}
	}
}

func TestACallIsAChildOfTheIncomingSpan(t *testing.T) {
	ctx := WithTraceparent(context.Background(), testTraceparent, "vendor=abc")
	ctx, call := WithCall(ctx, "req-1")
	if call.TraceID() != testTraceID || call.ParentSpanID() != testParentSpan {
		t.Fatalf("call trace %s parent %s", call.TraceID(), call.ParentSpanID())
	}
	if call.SpanID() == testParentSpan || !validHexID(call.SpanID(), 16) {
		t.Fatalf("call span %q", call.SpanID())
	}
	if TraceID(ctx) != testTraceID || SpanID(ctx) != call.SpanID() {
		t.Fatalf("ctx accessors: %s %s", TraceID(ctx), SpanID(ctx))
	}
	if want := "00-" + testTraceID + "-" + call.SpanID() + "-01"; Traceparent(ctx) != want || call.Traceparent() != want {
		t.Fatalf("outgoing traceparent %q, want %q", Traceparent(ctx), want)
	}
	if Tracestate(ctx) != "vendor=abc" {
		t.Fatalf("tracestate %q", Tracestate(ctx))
	}
	h := http.Header{}
	InjectHTTP(ctx, h)
	if h.Get(TraceparentHeader) != call.Traceparent() || h.Get(TracestateHeader) != "vendor=abc" {
		t.Fatalf("injected %v", h)
	}
}

func TestAMalformedTraceparentStartsAFreshTrace(t *testing.T) {
	ctx := WithTraceparent(context.Background(), "00-not-a-trace-01", "x")
	if _, ok := ParentSpanFrom(ctx); ok {
		t.Fatal("malformed value kept")
	}
	_, call := WithCall(ctx, "")
	if !ValidTraceID(call.TraceID()) || call.ParentSpanID() != "" {
		t.Fatalf("fresh trace: %s parent %q", call.TraceID(), call.ParentSpanID())
	}
	_, other := WithCall(context.Background(), "")
	if other.TraceID() == call.TraceID() {
		t.Fatal("two root calls share a trace")
	}
}

func TestATracestateOutsideTheAlphabetIsDropped(t *testing.T) {
	ctx := WithTraceparent(context.Background(), testTraceparent, "a=b\r\nc")
	if Tracestate(ctx) != "" {
		t.Fatalf("kept %q", Tracestate(ctx))
	}
	if CleanTracestate(strings.Repeat("a", MaxTracestateBytes+1)) != "" {
		t.Fatal("kept an oversized tracestate")
	}
}

// A library caller with a trace but no call: its ids are the caller's span.
func TestALibraryContextCarriesTheCallersTrace(t *testing.T) {
	ctx := WithTraceparent(context.Background(), testTraceparent, "")
	if TraceID(ctx) != testTraceID || SpanID(ctx) != testParentSpan {
		t.Fatalf("library ctx: %s %s", TraceID(ctx), SpanID(ctx))
	}
	args := LogArgs(ctx, "err", "x")
	if len(args) != 4 {
		t.Fatalf("log args: %v", args)
	}
	if InjectHTTP(context.Background(), http.Header{}); TraceID(context.Background()) != "" {
		t.Fatal("empty ctx has a trace")
	}
}
