package observability

import (
	"context"
	"strings"
	"testing"
)

// A caller's id is adopted only when it is plain and short: the id is
// logged before the caller is authenticated.
func TestACallAdoptsOnlyAPlainRequestID(t *testing.T) {
	for id, adopted := range map[string]bool{
		"7f3a-recall.1":           true,
		"":                        false,
		"has space":               false,
		"line\nbreak":             false,
		strings.Repeat("a", 65):   false,
		strings.Repeat("a", 64):   true,
		"key=operator":            false,
		"mod:1730000000000-abcde": true,
	} {
		_, call := WithCall(context.Background(), id)
		if got := call.ID() == id; got != adopted {
			t.Errorf("WithCall(%q) adopted=%v, want %v (id %q)", id, got, adopted, call.ID())
		}
		if !ValidRequestID(call.ID()) {
			t.Errorf("minted id %q is not valid", call.ID())
		}
	}
}

// A call already in the context keeps its id: the MCP bridge makes one, and
// the layers it calls in process must not mint another.
func TestWithCallKeepsTheCallAlreadyThere(t *testing.T) {
	ctx, first := WithCall(context.Background(), "outer-1")
	_, second := WithCall(ctx, "inner-2")
	if second != first || RequestID(ctx) != "outer-1" {
		t.Fatalf("inner call replaced the outer one: %q", second.ID())
	}
}

// What a client says about itself crosses gRPC metadata, which is ASCII
// only, so a project named in Chinese is escaped and read back, and control
// characters never reach a log line.
func TestClientValuesSurviveTheWire(t *testing.T) {
	for in, want := range map[string]string{
		"CortexDB":      "CortexDB",
		"大脑项目":          "大脑项目",
		"evil\nkey=op":  "evilkey=op",
		"with space/ok": "with space/ok",
	} {
		enc := EncodeClientValue(in)
		for i := 0; i < len(enc); i++ {
			if enc[i] < 0x20 || enc[i] > 0x7e {
				t.Fatalf("%q encodes to non-ASCII %q", in, enc)
			}
		}
		if got := DecodeClientValue(enc); got != want {
			t.Errorf("%q round-trips to %q, want %q", in, got, want)
		}
	}
	if got := CleanClientValue(strings.Repeat("x", 500)); len(got) != maxClientInfoBytes {
		t.Fatalf("unbounded client value: %d bytes", len(got))
	}
}

func TestNoteRecallIsFreeWithoutACall(t *testing.T) {
	NoteRecall(context.Background(), RecallNote{Hits: 3})
	ctx, call := WithCall(context.Background(), "")
	NoteRecall(ctx, RecallNote{Hits: 3, Degraded: []string{"embedder_unreachable"}})
	if r := call.Recall(); r == nil || r.Hits != 3 || r.Degraded[0] != "embedder_unreachable" {
		t.Fatalf("recall note lost: %+v", r)
	}
}
