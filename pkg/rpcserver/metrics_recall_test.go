package rpcserver

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// What a recall reports about itself reaches the metrics: recalls per tool
// and key, the empty ones, fallbacks by reason, floor drops, reply sizes.
func TestRecallsAreCountedByWhatTheyReturned(t *testing.T) {
	reg := observability.NewRegistry()
	ic := MetricsInterceptor(reg)
	info := &grpc.UnaryServerInfo{FullMethod: callToolMethod}
	recall := func(note observability.RecallNote) {
		_, err := ic(context.Background(), &rpcv1.CallToolRequest{Name: note.Tool}, info, func(ctx context.Context, _ any) (any, error) {
			observability.CallFrom(ctx).SetKey("reader")
			observability.NoteRecall(ctx, note)
			return &rpcv1.CallToolResponse{ResultJson: strings.Repeat("x", 3000)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	recall(observability.RecallNote{Tool: "knowledge_memory_recall", Hits: 3, FloorDropped: map[string]int{"memory_semantic_floor": 4}})
	recall(observability.RecallNote{Tool: "knowledge_memory_recall", Hits: 0, Degraded: []string{"embedder_unreachable"}})

	var out bytes.Buffer
	if err := reg.WriteText(&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		`cortexdb_recalls_total{tool="knowledge_memory_recall",key="reader"} 2`,
		`cortexdb_recalls_empty_total{tool="knowledge_memory_recall",key="reader"} 1`,
		`cortexdb_recall_degraded_total{reason="embedder_unreachable"} 1`,
		`cortexdb_recall_floor_dropped_total{floor="memory_semantic_floor"} 4`,
		`cortexdb_grpc_response_bytes_bucket{grpc_method="/cortexdb.v1.ToolsService/CallTool",le="4096"} 2`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics lack %s", want)
		}
	}
	if t.Failed() {
		t.Log(text)
	}
}
