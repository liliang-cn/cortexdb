package cortexdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

const (
	flowTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	flowSpanID  = "00f067aa0ba902b7"
	flowParent  = "00-" + flowTraceID + "-" + flowSpanID + "-01"
)

// A library caller that puts its flow in ctx (SuperLeo embedding the brain)
// finds the recalls it caused by that flow's trace id; a recall made inside a
// call is recorded with the call's span; another flow's recalls stay out.
func TestRecallsAreLoggedWithTheirFlowAndFoundByIt(t *testing.T) {
	db := openRecallLogBrain(t, WithRecallLog(RecallLogOptions{}))
	bg := context.Background()
	saveRecallLogMemories(t, db, bg, map[string]string{"m-quorum": "drbd quorum lost on node-c"})

	lib := observability.WithTraceparent(bg, flowParent, "")
	if _, err := db.KnowledgeMemory().Recall(lib, KnowledgeMemoryRecallRequest{Query: "drbd quorum"}); err != nil {
		t.Fatal(err)
	}
	callCtx, call := observability.WithCall(observability.WithTraceparent(bg, flowParent, ""), "req-flow")
	if _, err := db.KnowledgeMemory().Recall(callCtx, KnowledgeMemoryRecallRequest{Query: "quorum node-c"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.KnowledgeMemory().Recall(bg, KnowledgeMemoryRecallRequest{Query: "unrelated"}); err != nil {
		t.Fatal(err)
	}

	all, err := db.ListRecalls(bg, RecallLogListRequest{})
	if err != nil || len(all.Recalls) != 3 {
		t.Fatalf("all: %+v %v", all, err)
	}
	flow, err := db.ListRecalls(bg, RecallLogListRequest{TraceID: flowTraceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.Recalls) != 2 {
		t.Fatalf("flow recalls: %+v", flow.Recalls)
	}
	inCall, inLib := flow.Recalls[0], flow.Recalls[1]
	if inLib.TraceID != flowTraceID || inLib.SpanID != flowSpanID || inLib.RequestID != "" {
		t.Fatalf("library recall: %+v", inLib)
	}
	if inCall.TraceID != flowTraceID || inCall.SpanID != call.SpanID() || inCall.RequestID != "req-flow" {
		t.Fatalf("call recall: %+v", inCall)
	}
	// The explain record comes with it, by the id the list gave.
	got, err := db.GetRecall(bg, RecallLogGetRequest{RecallID: inCall.ID})
	if err != nil || got.Explain == nil || got.TraceID != flowTraceID {
		t.Fatalf("get: %+v %v", got, err)
	}

	// The tool takes the same filter, which is how a remote caller asks.
	raw, err := db.GraphRAGTools().Call(bg, "recall_log_list", json.RawMessage(`{"trace_id":"`+flowTraceID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(raw)
	var viaTool RecallLogListResponse
	if err := json.Unmarshal(b, &viaTool); err != nil || len(viaTool.Recalls) != 2 {
		t.Fatalf("tool: %s %v", b, err)
	}

	// A library recall made outside any flow names none; a surface
	// (gRPC, HTTP, MCP) would have started one for it.
	if other := all.Recalls[0]; other.TraceID != "" {
		t.Fatalf("untraced recall: %+v", other)
	}
}

func TestARecallLogFilterThatIsNotATraceIDIsRefused(t *testing.T) {
	db := openRecallLogBrain(t, WithRecallLog(RecallLogOptions{}))
	for _, bad := range []string{"xyz", "4BF92F3577B34DA6A3CE929D0E0E4736", "' OR 1=1 --"} {
		if _, err := db.ListRecalls(context.Background(), RecallLogListRequest{TraceID: bad}); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// A recall log made before the trace columns existed gains them on open.
func TestAnOlderRecallLogGainsTheTraceColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-recall-log.db")
	old, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := old.exec(ctx, `CREATE TABLE recall_log (
		id TEXT PRIMARY KEY, request_id TEXT NOT NULL DEFAULT '', ts_ms BIGINT NOT NULL,
		key_id TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', project TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT '', tool TEXT NOT NULL DEFAULT '', query TEXT NOT NULL,
		planner TEXT NOT NULL DEFAULT '', latency_ms BIGINT NOT NULL DEFAULT 0, hit_count INTEGER NOT NULL DEFAULT 0,
		degraded TEXT NOT NULL DEFAULT '', hits TEXT NOT NULL DEFAULT '[]', explain_json TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.exec(ctx, `INSERT INTO recall_log (id, ts_ms, query) VALUES ('old-1', 1, 'before traces')`); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()

	db, err := Open(DefaultConfig(path), WithRecallLog(RecallLogOptions{Retention: 1 << 62}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	lib := observability.WithTraceparent(ctx, flowParent, "")
	if _, err := db.KnowledgeMemory().Recall(lib, KnowledgeMemoryRecallRequest{Query: "after traces"}); err != nil {
		t.Fatal(err)
	}
	all, err := db.ListRecalls(ctx, RecallLogListRequest{})
	if err != nil || len(all.Recalls) != 2 {
		t.Fatalf("list: %+v %v", all, err)
	}
	flow, err := db.ListRecalls(ctx, RecallLogListRequest{TraceID: flowTraceID})
	if err != nil || len(flow.Recalls) != 1 || flow.Recalls[0].Query != "after traces" {
		t.Fatalf("flow: %+v %v", flow, err)
	}
}
