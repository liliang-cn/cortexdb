package cortexdb

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// Every tool result names its request id in _meta, and a failed one says it
// in its text too, which is all some hosts show.
func TestMCPToolResultsCarryTheRequestID(t *testing.T) {
	db, _, ctx := checkBrain(t, "mcp-request-id")
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := db.NewMCPServer(MCPServerOptions{}).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "reqid-test"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	ok, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "graph_statistics", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := ok.Meta[MCPRequestIDMetaKey].(string)
	if !observability.ValidRequestID(id) {
		t.Fatalf("no request id in _meta: %#v", ok.Meta)
	}

	failed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "memory_get", Arguments: map[string]any{"memory_id": "no-such-memory"}})
	if err != nil {
		if !strings.Contains(err.Error(), "request_id ") {
			t.Fatalf("protocol error without request id: %v", err)
		}
		return
	}
	if !failed.IsError {
		t.Skip("memory_get of a missing id did not fail")
	}
	text := failed.Content[0].(*mcp.TextContent).Text
	other, _ := failed.Meta[MCPRequestIDMetaKey].(string)
	if other == "" || other == id || !strings.Contains(text, "(request_id "+other+")") {
		t.Fatalf("failed result: meta=%#v text=%q", failed.Meta, text)
	}
}

// A tools/call that names its flow in params._meta.traceparent runs as a
// child span of it: the result's _meta returns that span, and the recall it
// made is logged under the flow's trace id. A malformed traceparent starts a
// trace of its own.
func TestMCPToolCallsJoinTheFlowTheirMetaNames(t *testing.T) {
	db, _, ctx := checkBrain(t, "mcp-trace", WithRecallLog(RecallLogOptions{}))
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := db.NewMCPServer(MCPServerOptions{}).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "trace-test"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Meta:      mcp.Meta{MCPTraceparentMetaKey: flowParent, MCPTracestateMetaKey: "agentgo=1"},
		Name:      "knowledge_memory_recall",
		Arguments: map[string]any{"query": "drbd quorum"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tp, _ := res.Meta[MCPResultTraceparentMetaKey].(string)
	span, ok := observability.ParseTraceparent(tp)
	if !ok || span.TraceID != flowTraceID || span.SpanID == flowSpanID {
		t.Fatalf("result traceparent %q", tp)
	}
	list, err := db.ListRecalls(ctx, RecallLogListRequest{TraceID: flowTraceID})
	if err != nil || len(list.Recalls) != 1 || list.Recalls[0].SpanID != span.SpanID {
		t.Fatalf("recalls of the flow: %+v %v", list, err)
	}
	if list.Recalls[0].RequestID != res.Meta[MCPRequestIDMetaKey] {
		t.Fatalf("request id %q vs %v", list.Recalls[0].RequestID, res.Meta[MCPRequestIDMetaKey])
	}

	bad, err := session.CallTool(ctx, &mcp.CallToolParams{
		Meta:      mcp.Meta{MCPTraceparentMetaKey: "00-" + flowTraceID + "-zzzz-01"},
		Name:      "graph_statistics",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	tp, _ = bad.Meta[MCPResultTraceparentMetaKey].(string)
	if fresh, ok := observability.ParseTraceparent(tp); !ok || fresh.TraceID == flowTraceID {
		t.Fatalf("malformed traceparent gave %q", tp)
	}
}
