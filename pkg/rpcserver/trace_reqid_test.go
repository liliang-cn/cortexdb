package rpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// The record carries the id the client sent, the client's own description
// and the size of what was sent back, so a reply a host replaced can be
// matched to the record of what the server sent.
func TestTheRecordCarriesTheRequestIDAndReplySize(t *testing.T) {
	var buf, file bytes.Buffer
	ic := RequestLogInterceptor(TraceOptions{All: true, Log: slog.New(slog.NewTextHandler(&buf, nil)), File: &file})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		observability.RequestIDHeader, "mod-42",
		observability.ClientHostHeader, observability.EncodeClientValue("Pro"),
		observability.ClientProjectHeader, observability.EncodeClientValue("大脑"),
	))
	result := strings.Repeat("x", 5000)
	var seen string
	_, err := ic(ctx, &rpcv1.CallToolRequest{Name: "knowledge_memory_recall"}, &grpc.UnaryServerInfo{FullMethod: callToolMethod},
		func(ctx context.Context, _ any) (any, error) {
			seen = observability.RequestID(ctx)
			observability.NoteRecall(ctx, observability.RecallNote{Hits: 2, Degraded: []string{"embedder_unreachable"}})
			return &rpcv1.CallToolResponse{ResultJson: result}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if seen != "mod-42" {
		t.Fatalf("handler saw request id %q", seen)
	}
	var rec traceRecord
	if err := json.Unmarshal(bytes.TrimSpace(file.Bytes()), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.ReqID != "mod-42" || rec.Host != "Pro" || rec.Project != "大脑" || rec.ResultBytes != 5000 || rec.RespBytes < 5000 {
		t.Fatalf("record: %+v", rec)
	}
	if rec.Recall == nil || rec.Recall.Hits != 2 || rec.Recall.Degraded[0] != "embedder_unreachable" {
		t.Fatalf("recall part: %+v", rec.Recall)
	}
	line := buf.String()
	for _, want := range []string{"req_id=mod-42", "resp_bytes=", "hits=2", "degraded=embedder_unreachable"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line lacks %q: %s", want, line)
		}
	}
}
