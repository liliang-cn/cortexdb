package rpcserver

import (
	"context"
	"encoding/json"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func TestToolsListAndCall(t *testing.T) {
	conn := newTestConn(t, false, "")
	client := rpcv1.NewToolsServiceClient(conn)
	ctx := context.Background()

	list, err := client.ListTools(ctx, &rpcv1.ListToolsRequest{})
	if err != nil || len(list.GetTools()) == 0 {
		t.Fatalf("list: %v n=%d", err, len(list.GetTools()))
	}
	names := map[string]bool{}
	for _, d := range list.GetTools() {
		names[d.GetName()] = true
		if d.GetInputSchemaJson() == "" {
			t.Fatalf("tool %s has empty schema", d.GetName())
		}
	}
	if !names["ingest_document"] || !names["knowledge_search"] {
		t.Fatalf("expected core tools present, got %v", names)
	}

	res, err := client.CallTool(ctx, &rpcv1.CallToolRequest{
		Name:     "ingest_document",
		ArgsJson: `{"document_id":"d1","content":"hello tools world"}`,
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.GetResultJson()), &out); err != nil {
		t.Fatalf("result not JSON: %v", err)
	}

	if _, err := client.CallTool(ctx, &rpcv1.CallToolRequest{Name: "nope", ArgsJson: "{}"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown tool: want NotFound, got %v", err)
	}
}
