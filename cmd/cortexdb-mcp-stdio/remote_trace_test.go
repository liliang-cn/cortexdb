package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

const (
	flowTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	flowSpanID  = "00f067aa0ba902b7"
	flowParent  = "00-" + flowTraceID + "-" + flowSpanID + "-01"
)

// The bridge carries a tools/call's params._meta.traceparent to the brain:
// the MCP client's span is the bridge's parent, the bridge's span (returned
// in the result _meta) is the brain's parent, and the brain's recall log
// finds the recall by the flow's trace id. The brain's embedder is called
// with the flow too.
func TestTheRemoteBridgeCarriesTheFlowToTheBrain(t *testing.T) {
	var embedMu sync.Mutex
	var embedTraceparents []string
	embedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embedMu.Lock()
		embedTraceparents = append(embedTraceparents, r.Header.Get(observability.TraceparentHeader))
		embedMu.Unlock()
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 0, 0, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer embedSrv.Close()

	dbPath := filepath.Join(t.TempDir(), "trace.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath),
		cortexdb.WithEmbedder(newOpenAIEmbedder(embedSrv.URL, "", "test", 4)),
		cortexdb.WithRecallLog(cortexdb.RecallLogOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := rpcserver.New(db, rpcserver.Options{DBPath: dbPath})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	ctx := context.Background()
	conn, err := dialCortexDB(lis.Addr().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := rpcv1.NewToolsServiceClient(conn)
	list, err := client.ListTools(ctx, &rpcv1.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "bridge-test"}, nil)
	server.AddReceivingMiddleware(cortexdb.MCPCallMiddleware(cortexdb.LocalClientInfo()))
	for _, def := range list.GetTools() {
		if def.GetName() == recallToolName {
			addRemoteTool(server, client, def)
		}
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "agent-go"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Meta:      mcp.Meta{cortexdb.MCPTraceparentMetaKey: flowParent},
		Name:      recallToolName,
		Arguments: map[string]any{"query": "drbd quorum"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool failed: %+v", res.Content)
	}
	tp, _ := res.Meta[cortexdb.MCPResultTraceparentMetaKey].(string)
	bridge, ok := observability.ParseTraceparent(tp)
	if !ok || bridge.TraceID != flowTraceID || bridge.SpanID == flowSpanID {
		t.Fatalf("bridge span %q", tp)
	}

	recalls, err := db.ListRecalls(ctx, cortexdb.RecallLogListRequest{TraceID: flowTraceID})
	if err != nil || len(recalls.Recalls) != 1 {
		t.Fatalf("brain recalls of the flow: %+v %v", recalls, err)
	}
	brain := recalls.Recalls[0]
	if brain.SpanID == bridge.SpanID || brain.SpanID == flowSpanID || !observability.ValidTraceID(brain.TraceID) {
		t.Fatalf("brain span %q, bridge %q", brain.SpanID, bridge.SpanID)
	}
	if brain.RequestID != res.Meta[cortexdb.MCPRequestIDMetaKey] {
		t.Fatalf("request id %q vs %v", brain.RequestID, res.Meta[cortexdb.MCPRequestIDMetaKey])
	}

	embedMu.Lock()
	defer embedMu.Unlock()
	want := "00-" + flowTraceID + "-" + brain.SpanID + "-01"
	found := false
	for _, got := range embedTraceparents {
		if got == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("embedder saw %v, want %s", embedTraceparents, want)
	}
}
