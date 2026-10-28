package rpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpc"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

const (
	flowTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	flowSpanID  = "00f067aa0ba902b7"
	flowParent  = "00-" + flowTraceID + "-" + flowSpanID + "-01"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// tracedBrain runs a server with the request log over a brain that keeps a
// recall log, and returns a client dialled with rpc.DialOptions, as the
// MCP bridge dials.
func tracedBrain(t *testing.T) (*grpc.ClientConn, *cortexdb.DB, *lockedBuffer, *lockedBuffer) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "trace.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath), cortexdb.WithRecallLog(cortexdb.RecallLogOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logs, file := &lockedBuffer{}, &lockedBuffer{}
	srv, err := NewWithPolicy(db, Options{DBPath: dbPath, Interceptors: []grpc.UnaryServerInterceptor{
		MetricsInterceptor(observability.NewRegistry()),
		RequestLogInterceptor(TraceOptions{All: true, Log: slog.New(slog.NewTextHandler(logs, nil)), File: file}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	opts := append(rpc.DialOptions(),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient("passthrough:///bufnet", opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, db, logs, file
}

// A client that sends its flow (here a library ctx, as SuperLeo's would be)
// gets back the server's span, a child of its own; the log line, the trace
// file and the recall log all carry the flow's trace id.
func TestAGRPCCallJoinsTheFlowItsTraceparentNames(t *testing.T) {
	conn, db, logs, file := tracedBrain(t)
	ctx := observability.WithTraceparent(context.Background(), flowParent, "agentgo=1")
	var header metadata.MD
	_, err := rpcv1.NewToolsServiceClient(conn).CallTool(ctx, &rpcv1.CallToolRequest{
		Name: "knowledge_memory_recall", ArgsJson: `{"query":"drbd quorum"}`,
	}, grpc.Header(&header))
	if err != nil {
		t.Fatal(err)
	}
	got := header.Get(observability.TraceparentHeader)
	if len(got) != 1 {
		t.Fatalf("response header: %v", header)
	}
	span, ok := observability.ParseTraceparent(got[0])
	if !ok || span.TraceID != flowTraceID || span.SpanID == flowSpanID {
		t.Fatalf("server span %q", got[0])
	}
	reqID := header.Get(observability.RequestIDHeader)[0]

	line := logs.String()
	for _, want := range []string{"req_id=" + reqID, "trace_id=" + flowTraceID, "span_id=" + span.SpanID, "parent_span_id=" + flowSpanID} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line lacks %q: %s", want, line)
		}
	}
	var rec traceRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(file.String())), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.TraceID != flowTraceID || rec.SpanID != span.SpanID || rec.ParentSpanID != flowSpanID || rec.ReqID != reqID {
		t.Fatalf("trace record: %+v", rec)
	}
	list, err := db.ListRecalls(context.Background(), cortexdb.RecallLogListRequest{TraceID: flowTraceID})
	if err != nil || len(list.Recalls) != 1 || list.Recalls[0].SpanID != span.SpanID || list.Recalls[0].RequestID != reqID {
		t.Fatalf("recall log: %+v %v", list, err)
	}
}

// A call with no traceparent, or one that does not parse, starts a trace of
// its own; the malformed value is never written to the log.
func TestAGRPCCallWithoutAValidTraceparentStartsATrace(t *testing.T) {
	conn, _, logs, _ := tracedBrain(t)
	client := rpcv1.NewToolsServiceClient(conn)
	const forged = "00-" + flowTraceID + "-" + flowSpanID + "-01 msg=forged"
	for _, ctx := range []context.Context{
		context.Background(),
		metadata.AppendToOutgoingContext(context.Background(), observability.TraceparentHeader, forged),
	} {
		var header metadata.MD
		if _, err := client.ListTools(ctx, &rpcv1.ListToolsRequest{}, grpc.Header(&header)); err != nil {
			t.Fatal(err)
		}
		span, ok := observability.ParseTraceparent(header.Get(observability.TraceparentHeader)[0])
		if !ok || span.TraceID == flowTraceID {
			t.Fatalf("server span %+v", span)
		}
	}
	if line := logs.String(); strings.Contains(line, "forged") || strings.Contains(line, "parent_span_id") || strings.Contains(line, flowTraceID) {
		t.Fatalf("log carries the malformed value: %s", line)
	}
}

// A streaming call gets the same: a child span in its context and the span
// in its response header.
func TestAStreamingCallJoinsTheFlow(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(observability.TraceparentHeader, flowParent))
	ss := &headerStream{ctx: ctx}
	var seen observability.SpanContext
	err := TraceStreamInterceptor()(nil, ss, &grpc.StreamServerInfo{FullMethod: "/x/Y"}, func(_ any, s grpc.ServerStream) error {
		seen, _ = observability.SpanFrom(s.Context())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.TraceID != flowTraceID || seen.SpanID == flowSpanID || observability.CallFrom(ss.ctx) != nil {
		t.Fatalf("stream span %+v", seen)
	}
	if got := ss.header.Get(observability.TraceparentHeader); len(got) != 1 || got[0] != seen.Traceparent() {
		t.Fatalf("stream header %v", ss.header)
	}
}

type headerStream struct {
	grpc.ServerStream
	ctx    context.Context
	header metadata.MD
}

func (h *headerStream) Context() context.Context { return h.ctx }
func (h *headerStream) SetHeader(md metadata.MD) error {
	h.header = metadata.Join(h.header, md)
	return nil
}

// The client's stream interceptor sends the flow too.
func TestTheClientSendsTheFlowOnStreams(t *testing.T) {
	ctx := observability.WithTraceparent(context.Background(), flowParent, "")
	var sent metadata.MD
	_, _ = rpc.CallStreamInterceptor()(ctx, &grpc.StreamDesc{}, nil, "/x/Y", func(ctx context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
		sent, _ = metadata.FromOutgoingContext(ctx)
		return nil, nil
	})
	if got := sent.Get(observability.TraceparentHeader); len(got) != 1 || got[0] != flowParent {
		t.Fatalf("sent %v", sent)
	}
}
