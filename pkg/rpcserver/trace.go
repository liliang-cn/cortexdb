package rpcserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// The request log: one line per call, with where its time went.
//
// CORTEXDB_TRACE=1 logs every call. Without it only slow calls are logged —
// CORTEXDB_SLOW_MS, 1000 by default, 0 to log none — because a server that
// says nothing about a 24-second recall leaves its clients guessing which of
// three machines to blame. CORTEXDB_TRACE_FILE appends the same records as
// JSON lines, cut at CORTEXDB_TRACE_MAX_MB (100) with CORTEXDB_TRACE_KEEP (5)
// old files kept for at most CORTEXDB_TRACE_MAX_AGE_DAYS (30). A CallTool's
// record also names the rows its arguments name (ids), so a trace file says
// what a key reached for, not only which tool.
//
// Every call carries a request id (observability.RequestIDHeader): the
// client's when it sent a valid one, a fresh one otherwise. The server
// returns it in the response header, and the record keeps it with the size
// of the reply, so a client that failed on a reply the server thought it
// sent can name the record — and an oversized reply shows up here before a
// host truncates it. A recall's record also carries its explain record (see
// cortexdb.RecallExplain): the plan, each retriever's candidates, what each
// floor dropped, and the hits.

// TraceOptions configures RequestLogInterceptor.
type TraceOptions struct {
	All  bool          // log every call
	Slow time.Duration // log calls at least this slow; 0 logs none
	File io.Writer     // JSON lines, optional
	// Log receives the line per call; defaults to slog.Default().
	Log *slog.Logger
}

// Trace file rotation defaults.
const (
	defaultTraceMaxMB      = 100
	defaultTraceKeep       = 5
	defaultTraceMaxAgeDays = 30
)

// TraceOptionsFromEnv reads CORTEXDB_TRACE, CORTEXDB_SLOW_MS,
// CORTEXDB_TRACE_FILE and its rotation settings.
func TraceOptionsFromEnv() TraceOptions {
	o := TraceOptions{Slow: time.Second}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CORTEXDB_TRACE"))) {
	case "1", "true", "on", "yes":
		o.All = true
	}
	if v := strings.TrimSpace(os.Getenv("CORTEXDB_SLOW_MS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			o.Slow = time.Duration(n) * time.Millisecond
		}
	}
	if p := strings.TrimSpace(os.Getenv("CORTEXDB_TRACE_FILE")); p != "" {
		maxMB := envInt("CORTEXDB_TRACE_MAX_MB", defaultTraceMaxMB)
		keep := envInt("CORTEXDB_TRACE_KEEP", defaultTraceKeep)
		days := envInt("CORTEXDB_TRACE_MAX_AGE_DAYS", defaultTraceMaxAgeDays)
		f, err := observability.OpenRotatingFile(p, int64(maxMB)<<20, keep, time.Duration(days)*24*time.Hour)
		if err == nil {
			o.File = f
		} else {
			slog.Error("cortexdb-trace: cannot open trace file", "path", p, "err", err)
		}
	}
	return o
}

// envInt reads a non-negative integer, or def.
func envInt(name string, def int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

type traceRecord struct {
	TS    string `json:"ts"`
	ReqID string `json:"req_id"`
	// TraceID, SpanID and ParentSpanID place the call in its flow (see
	// observability.WithTraceparent).
	TraceID      string `json:"trace_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id,omitempty"`
	Method       string `json:"method"`
	Key          string `json:"key,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Host         string `json:"host,omitempty"`
	Project      string `json:"project,omitempty"`
	Agent        string `json:"agent,omitempty"`
	// IDs are the rows a CallTool names in its arguments, by path
	// (authz.RowIDsOf): the ids, distinct and sorted before the cut, at
	// most maxTraceIDs per path, each cut at maxTraceIDBytes. IDsOmitted
	// counts the ones left out.
	IDs        map[string][]string `json:"ids,omitempty"`
	IDsOmitted int                 `json:"ids_omitted,omitempty"`
	DurMs      int64               `json:"dur_ms"`
	Stages     map[string]int64    `json:"stages_ms,omitempty"`
	// RespBytes is the size of the reply as sent, and ResultBytes the size
	// of a CallTool's result JSON inside it — the text an MCP host gets.
	RespBytes   int          `json:"resp_bytes"`
	ResultBytes int          `json:"result_bytes,omitempty"`
	Recall      *traceRecall `json:"recall,omitempty"`
	Err         string       `json:"err,omitempty"`
}

// traceRecall is a recall's own part of the record.
type traceRecall struct {
	Hits         int            `json:"hits"`
	Degraded     []string       `json:"degraded,omitempty"`
	FloorDropped map[string]int `json:"floor_dropped,omitempty"`
	Explain      any            `json:"explain,omitempty"`
}

// maxTraceIDs bounds the ids a record keeps per path, and maxTraceIDBytes
// the length of each. The log runs before authentication, so without them
// any caller could write a list of any length to the trace file.
const (
	maxTraceIDs     = 100
	maxTraceIDBytes = 256
)

// tagCaller records the id of the key a call authenticated as. The log runs
// outside the policy gate (see Options.Interceptors), so the key is not known
// when the log begins; the call record the log planted carries it back up.
//
// Only the key's id is kept, never its secret: the record is for an operator
// asking "who wrote this", and a trace file is not a place to keep a
// credential. Whether the call was then denied is already in err.
func tagCaller(ctx context.Context, keyID string) {
	observability.CallFrom(ctx).SetKey(keyID)
}

// callToolMethod is the one RPC whose request names a tool, which the record
// keeps beside the method: "CallTool" alone says nothing about what ran.
const callToolMethod = "/cortexdb.v1.ToolsService/CallTool"

// incomingCall starts the call record from the request metadata: the
// client's request id, the span it was called from (traceparent) and what it
// says about itself.
func incomingCall(ctx context.Context) (context.Context, *observability.Call) {
	if call := observability.CallFrom(ctx); call != nil {
		return ctx, call
	}
	md, _ := metadata.FromIncomingContext(ctx)
	first := func(k string) string {
		if v := md.Get(k); len(v) > 0 {
			return v[0]
		}
		return ""
	}
	ctx = observability.WithTraceparent(ctx, first(observability.TraceparentHeader), first(observability.TracestateHeader))
	ctx, call := observability.WithCall(ctx, first(observability.RequestIDHeader))
	call.SetClient(observability.ClientInfo{
		Host:    observability.DecodeClientValue(first(observability.ClientHostHeader)),
		Project: observability.DecodeClientValue(first(observability.ClientProjectHeader)),
		Agent:   observability.DecodeClientValue(first(observability.ClientAgentHeader)),
	})
	return ctx, call
}

// RequestLogInterceptor gives every call a request id and a stage collector,
// and logs the call when the options say to.
func RequestLogInterceptor(o TraceOptions) grpc.UnaryServerInterceptor {
	var fileMu sync.Mutex
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, call := incomingCall(ctx)
		// Sent before the handler runs, so a call that fails still says
		// which record to look for. No stream in ctx (a test) is no error.
		ctx = sendCallHeader(ctx, call)
		ctx, stages := observability.WithStages(ctx)
		start := time.Now()
		resp, err := handler(ctx, req)
		dur := time.Since(start)
		if !o.All && (o.Slow <= 0 || dur < o.Slow) {
			return resp, err
		}
		logger := o.Log
		if logger == nil {
			logger = slog.Default()
		}
		method := info.FullMethod
		if i := strings.LastIndexByte(method, '/'); i >= 0 {
			method = method[i+1:]
		}
		key := call.Key()
		client := call.Client()
		tool := ""
		var ids map[string][]string
		if info.FullMethod == callToolMethod {
			tool, _ = requestToolName(req)()
			call, _ := req.(*rpcv1.CallToolRequest)
			ids = authz.RowIDsOf(tool, []byte(call.GetArgsJson()))
		}
		respBytes, resultBytes := 0, 0
		if m, ok := resp.(proto.Message); ok && m != nil {
			respBytes = proto.Size(m)
		}
		if r, ok := resp.(*rpcv1.CallToolResponse); ok {
			resultBytes = len(r.GetResultJson())
		}
		recall := call.Recall()

		attrs := []slog.Attr{
			slog.String("req_id", call.ID()),
			slog.String("trace_id", call.TraceID()),
			slog.String("span_id", call.SpanID()),
		}
		if p := call.ParentSpanID(); p != "" {
			attrs = append(attrs, slog.String("parent_span_id", p))
		}
		attrs = append(attrs,
			slog.String("rpc", method),
			slog.Float64("dur_s", roundSeconds(dur)),
		)
		if key != "" {
			attrs = append(attrs, slog.String("key", key))
		}
		if tool != "" {
			attrs = append(attrs, slog.String("tool", tool))
		}
		if client.Host != "" {
			attrs = append(attrs, slog.String("host", client.Host))
		}
		if client.Project != "" {
			attrs = append(attrs, slog.String("project", client.Project))
		}
		// The count only: the ids themselves go to the trace file.
		if n := idCount(ids); n > 0 {
			attrs = append(attrs, slog.Int("ids", n))
		}
		attrs = append(attrs, slog.Int("resp_bytes", respBytes))
		if recall != nil {
			attrs = append(attrs, slog.Int("hits", recall.Hits))
			if len(recall.Degraded) > 0 {
				attrs = append(attrs, slog.String("degraded", strings.Join(recall.Degraded, ",")))
			}
		}
		if s := stages.Millis(); len(s) > 0 {
			stageAttrs := make([]any, 0, len(s))
			for _, name := range stages.Names() {
				stageAttrs = append(stageAttrs, slog.Int64(name, s[name]))
			}
			attrs = append(attrs, slog.Group("stages_ms", stageAttrs...))
		}
		level := slog.LevelInfo
		if !o.All || (o.Slow > 0 && dur >= o.Slow) {
			attrs = append(attrs, slog.Bool("slow", true))
			level = slog.LevelWarn
		}
		if err != nil {
			attrs = append(attrs, slog.String("err", err.Error()))
		}
		logger.LogAttrs(ctx, level, "cortexdb-trace", attrs...)
		if o.File != nil {
			rec := traceRecord{
				TS: start.UTC().Format(time.RFC3339Nano), ReqID: call.ID(),
				TraceID: call.TraceID(), SpanID: call.SpanID(), ParentSpanID: call.ParentSpanID(), Method: method, Key: key, Tool: tool,
				Host: client.Host, Project: client.Project, Agent: client.Agent,
				DurMs: dur.Milliseconds(), Stages: stages.Millis(), RespBytes: respBytes, ResultBytes: resultBytes,
			}
			rec.IDs, rec.IDsOmitted = boundIDs(ids)
			if recall != nil {
				rec.Recall = &traceRecall{Hits: recall.Hits, Degraded: recall.Degraded, FloorDropped: recall.FloorDropped, Explain: recall.Explain}
			}
			if err != nil {
				rec.Err = err.Error()
			}
			if b, jerr := json.Marshal(rec); jerr == nil {
				fileMu.Lock()
				_, _ = o.File.Write(append(b, '\n'))
				fileMu.Unlock()
			}
		}
		return resp, err
	}
}

// callHeader is what a server returns about the call: its request id and
// the traceparent of the span it ran in, which the caller links as a child.
func callHeader(call *observability.Call) metadata.MD {
	return metadata.Pairs(observability.RequestIDHeader, call.ID(), observability.TraceparentHeader, call.Traceparent())
}

type headerSentKey struct{}

// sendCallHeader returns the call's header once per call, however many
// interceptors that start a call record are chained. No stream in ctx (a
// test) is no error.
func sendCallHeader(ctx context.Context, call *observability.Call) context.Context {
	if ctx.Value(headerSentKey{}) != nil {
		return ctx
	}
	_ = grpc.SetHeader(ctx, callHeader(call))
	return context.WithValue(ctx, headerSentKey{}, true)
}

// CallServerInterceptor starts every unary call's record — request id,
// client, and its span in the flow the client's traceparent names — and
// returns the request id and traceparent in the response header. New and
// NewWithPolicy chain it first, so a server built without the request log
// still records each call in its flow; the request log and the metrics
// further in find the record and keep it.
func CallServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, call := incomingCall(ctx)
		return handler(sendCallHeader(ctx, call), req)
	}
}

// TraceStreamInterceptor gives a streaming call what RequestLogInterceptor
// gives a unary one: a call record in the stream's context, a child span of
// the client's traceparent, and the request id and traceparent returned in
// the response header.
func TraceStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, call := incomingCall(ss.Context())
		_ = ss.SetHeader(callHeader(call))
		return handler(srv, &tracedStream{ServerStream: ss, ctx: ctx})
	}
}

type tracedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *tracedStream) Context() context.Context { return s.ctx }

// roundSeconds is d in seconds to the millisecond.
func roundSeconds(d time.Duration) float64 {
	return float64(d.Milliseconds()) / 1000
}

func idCount(ids map[string][]string) int {
	n := 0
	for _, v := range ids {
		n += len(v)
	}
	return n
}

// boundIDs keeps the first maxTraceIDs ids of each path, cuts each at
// maxTraceIDBytes on a character boundary with a mark, and counts the ids
// left out.
func boundIDs(ids map[string][]string) (map[string][]string, int) {
	omitted := 0
	for p, v := range ids {
		if len(v) > maxTraceIDs {
			omitted += len(v) - maxTraceIDs
			v = v[:maxTraceIDs]
		}
		for i, id := range v {
			if len(id) > maxTraceIDBytes {
				v[i] = strings.ToValidUTF8(id[:maxTraceIDBytes], "") + "…"
			}
		}
		ids[p] = v
	}
	return ids, omitted
}
