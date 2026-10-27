package rpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func runThrough(t *testing.T, o TraceOptions, work func(ctx context.Context)) string {
	t.Helper()
	var buf bytes.Buffer
	o.Log = slog.New(slog.NewTextHandler(&buf, nil))
	ic := RequestLogInterceptor(o)
	_, _ = ic(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/cortexdb.v1.MemoryService/SearchMemory"},
		func(ctx context.Context, _ any) (any, error) { work(ctx); return nil, nil })
	return buf.String()
}

// A slow call is logged with where its time went, without anyone turning
// tracing on: the 24-second recall that started this left no line at all.
func TestASlowCallIsLoggedWithItsStages(t *testing.T) {
	var file bytes.Buffer
	got := runThrough(t, TraceOptions{Slow: 20 * time.Millisecond, File: &file}, func(ctx context.Context) {
		done := observability.Time(ctx, "embed")
		time.Sleep(30 * time.Millisecond)
		done()
		observability.Time(ctx, "lexical")()
	})
	for _, want := range []string{"rpc=SearchMemory", "stages_ms.embed=3", "stages_ms.lexical=", "slow=true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log line lacks %q: %q", want, got)
		}
	}
	var rec traceRecord
	if err := json.Unmarshal(bytes.TrimSpace(file.Bytes()), &rec); err != nil || rec.Stages["embed"] < 30 {
		t.Fatalf("trace file record: %v %+v", err, rec)
	}
}

// The record names the key that spoke and the tool a CallTool ran, on a
// served call and on a denied one alike. Without this a trace file over a
// shared brain said what happened and when, and nothing about who: no answer
// to "which key wrote this" or "which key is being refused all morning".
func TestTheRecordNamesTheKeyAndTheTool(t *testing.T) {
	var buf, file bytes.Buffer
	chain := []grpc.UnaryServerInterceptor{
		RequestLogInterceptor(TraceOptions{All: true, Log: slog.New(slog.NewTextHandler(&buf, nil)), File: &file}),
		authInterceptor(readerAndWriter(t), nil),
	}
	served := func(ctx context.Context, _ any) (any, error) { return nil, nil }
	info := &grpc.UnaryServerInfo{FullMethod: callToolMethod}
	call := func(secret, tool string) error {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+secret))
		req := &rpcv1.CallToolRequest{Name: tool}
		_, err := chain[0](ctx, req, info, func(ctx context.Context, req any) (any, error) {
			return chain[1](ctx, req, info, served)
		})
		return err
	}

	if err := call("op-secret", "knowledge_search"); err != nil {
		t.Fatalf("operator refused: %v", err)
	}
	if err := call("ro-secret", "ingest_document"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a read-only key ingested: %v", err)
	}
	if err := call("not-a-key", "knowledge_search"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("an unknown secret was served: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 log lines, got %d: %q", len(lines), buf.String())
	}
	for i, want := range [][]string{
		{"rpc=CallTool", "key=operator", "tool=knowledge_search"},
		{"rpc=CallTool", "key=reader", "tool=ingest_document", "err="},
		{"rpc=CallTool", "tool=knowledge_search", "err="},
	} {
		for _, w := range want {
			if !strings.Contains(lines[i], w) {
				t.Fatalf("line %d lacks %q: %q", i, w, lines[i])
			}
		}
	}
	if strings.Contains(lines[2], "key=") {
		t.Fatalf("an unauthenticated call must carry no key: %q", lines[2])
	}
	for _, line := range lines {
		if strings.Contains(line, "op-secret") || strings.Contains(line, "ro-secret") {
			t.Fatalf("a secret reached the log: %q", line)
		}
	}

	var recs []traceRecord
	for _, raw := range bytes.Split(bytes.TrimSpace(file.Bytes()), []byte("\n")) {
		var rec traceRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatalf("trace file record %q: %v", raw, err)
		}
		recs = append(recs, rec)
	}
	if len(recs) != 3 || recs[0].Key != "operator" || recs[0].Tool != "knowledge_search" ||
		recs[1].Key != "reader" || recs[1].Tool != "ingest_document" || recs[1].Err == "" ||
		recs[2].Key != "" || recs[2].Tool != "knowledge_search" {
		t.Fatalf("trace file records: %+v", recs)
	}
}

func TestAFastCallIsQuietUnlessTracingIsOn(t *testing.T) {
	quick := func(context.Context) {}
	if got := runThrough(t, TraceOptions{Slow: time.Second}, quick); got != "" {
		t.Fatalf("a fast call was logged: %q", got)
	}
	if got := runThrough(t, TraceOptions{All: true, Slow: time.Second}, quick); !strings.Contains(got, "rpc=SearchMemory") || strings.Contains(got, "slow=true") {
		t.Fatalf("with tracing on every call is logged: %q", got)
	}
}

// The tool name is the caller's and is logged before the key is checked, so
// a name that is not a plain word is quoted: one call with a wrong key
// cannot write a line of its own, or a field such as key=operator, into the
// log.
func TestACallerCannotWriteFieldsOrLinesIntoTheLog(t *testing.T) {
	for _, name := range []string{
		"knowledge_get\ncortexdb-trace rpc=CallTool dur=0.00s key=operator tool=knowledge_delete",
		"knowledge_get key=operator",
	} {
		var buf, file bytes.Buffer
		logged := RequestLogInterceptor(TraceOptions{All: true, Log: slog.New(slog.NewTextHandler(&buf, nil)), File: &file})
		auth := authInterceptor(readerAndWriter(t), nil)
		info := &grpc.UnaryServerInfo{FullMethod: callToolMethod}
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer wrong"))
		req := &rpcv1.CallToolRequest{Name: name, ArgsJson: `{"knowledge_id":"k1\nkey=operator"}`}
		_, _ = logged(ctx, req, info, func(ctx context.Context, req any) (any, error) {
			return auth(ctx, req, info, func(context.Context, any) (any, error) { return nil, nil })
		})
		line := strings.TrimSpace(buf.String())
		if strings.Contains(line, "\n") || !strings.Contains(line, " tool="+strconv.Quote(name)+" ") {
			t.Errorf("tool %q is not one quoted field of one line:\n%s", name, line)
		}
		if strings.Count(strings.TrimSpace(file.String()), "\n") > 0 {
			t.Errorf("tool %q: the trace file got more than one record", name)
		}
	}
}
