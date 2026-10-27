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
