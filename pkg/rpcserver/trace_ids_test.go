package rpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// The record names the rows a CallTool names in its arguments, at the paths
// the allowlist declares for the tool: an id the guard loads, a list the
// facade answers with missing, a search pinned to documents. A call that is
// denied keeps them, so a refused attempt says what it reached for. The text
// line says how many, and the trace file which. A call that names no row,
// and a tool off the allowlist, have no ids.
func TestTheRecordNamesTheRowsTheCallNames(t *testing.T) {
	var buf, file bytes.Buffer
	chain := []grpc.UnaryServerInterceptor{
		RequestLogInterceptor(TraceOptions{All: true, Log: slog.New(slog.NewTextHandler(&buf, nil)), File: &file}),
		authInterceptor(readerAndWriter(t), nil),
	}
	served := func(ctx context.Context, _ any) (any, error) { return nil, nil }
	info := &grpc.UnaryServerInfo{FullMethod: callToolMethod}
	call := func(secret, tool, args string) {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+secret))
		req := &rpcv1.CallToolRequest{Name: tool, ArgsJson: args}
		_, _ = chain[0](ctx, req, info, func(ctx context.Context, req any) (any, error) {
			return chain[1](ctx, req, info, served)
		})
	}
	cases := []struct {
		secret, tool, args string
		want               map[string]any
		count              string
	}{
		{"op-secret", "knowledge_get", `{"knowledge_id":"k1"}`, map[string]any{"knowledge_id": []any{"k1"}}, " ids=1"},
		{"op-secret", "memory_get", `{"memory_ids":["m2","m1"]}`, map[string]any{"memory_ids": []any{"m1", "m2"}}, " ids=2"},
		{"op-secret", "memory_save", `{"memory_id":"m3","content":"x","supersedes":["m1"]}`, map[string]any{"memory_id": []any{"m3"}, "supersedes": []any{"m1"}}, " ids=2"},
		{"op-secret", "knowledge_search", `{"query":"x","plan":{"filters":{"document_ids":["d1"]}}}`, map[string]any{"plan.filters.document_ids": []any{"d1"}}, " ids=1"},
		{"ro-secret", "ingest_document", `{"document_id":"d9","content":"x"}`, map[string]any{"document_id": []any{"d9"}}, " ids=1"},
		{"op-secret", "knowledge_search", `{"query":"x"}`, nil, ""},
		{"op-secret", "graph_cypher_query", `{"query":"MATCH (n) RETURN n","document_id":"d1"}`, nil, ""},
	}
	for _, c := range cases {
		call(c.secret, c.tool, c.args)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	raws := bytes.Split(bytes.TrimSpace(file.Bytes()), []byte("\n"))
	if len(lines) != len(cases) || len(raws) != len(cases) {
		t.Fatalf("want %d lines and records, got %d and %d", len(cases), len(lines), len(raws))
	}
	for i, c := range cases {
		var rec map[string]any
		if err := json.Unmarshal(raws[i], &rec); err != nil {
			t.Fatalf("record %q: %v", raws[i], err)
		}
		got, has := rec["ids"]
		if c.want == nil {
			if has {
				t.Errorf("%s %s: the record has ids %v, want none", c.tool, c.args, got)
			}
			if strings.Contains(lines[i], " ids=") {
				t.Errorf("%s %s: the line counts ids: %q", c.tool, c.args, lines[i])
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s: ids %v, want %v", c.tool, c.args, got, c.want)
		}
		if !strings.Contains(lines[i], " tool="+c.tool+c.count) {
			t.Errorf("%s %s: the line %q lacks %q after the tool", c.tool, c.args, lines[i], c.count)
		}
	}
	if !strings.Contains(lines[4], "err=") {
		t.Errorf("the denied call carries no error: %q", lines[4])
	}
}

// Only CallTool names rows in its arguments: another method has no ids.
func TestAnotherMethodLogsNoIDs(t *testing.T) {
	var file bytes.Buffer
	runThrough(t, TraceOptions{All: true, File: &file}, func(context.Context) {})
	if strings.Contains(file.String(), `"ids"`) {
		t.Fatalf("a SearchMemory record has ids: %s", file.String())
	}
}
