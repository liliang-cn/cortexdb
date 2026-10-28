package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

const (
	flowTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	flowSpanID  = "00f067aa0ba902b7"
	flowParent  = "00-" + flowTraceID + "-" + flowSpanID + "-01"
)

// A request that sends its flow runs as a child span of it: the response's
// traceparent names that span and the recall it made is logged under the
// flow. One with a malformed traceparent starts a trace of its own.
func TestAnHTTPRequestJoinsTheFlowItsTraceparentNames(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "trace.db")), cortexdb.WithRecallLog(cortexdb.RecallLogOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler, err := NewWithPolicy(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	send := func(traceparent string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/recall", strings.NewReader(`{"query":"drbd quorum"}`))
		req.Header.Set("Content-Type", "application/json")
		if traceparent != "" {
			req.Header.Set(observability.TraceparentHeader, traceparent)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		return resp
	}

	resp := send(flowParent)
	span, ok := observability.ParseTraceparent(resp.Header.Get(observability.TraceparentHeader))
	if !ok || span.TraceID != flowTraceID || span.SpanID == flowSpanID {
		t.Fatalf("response traceparent %q", resp.Header.Get(observability.TraceparentHeader))
	}
	list, err := db.ListRecalls(context.Background(), cortexdb.RecallLogListRequest{TraceID: flowTraceID})
	if err != nil || len(list.Recalls) != 1 || list.Recalls[0].SpanID != span.SpanID ||
		list.Recalls[0].RequestID != resp.Header.Get(observability.RequestIDHeader) {
		t.Fatalf("recall log: %+v %v", list, err)
	}

	for _, bad := range []string{"", "00-" + flowTraceID + "-" + strings.ToUpper(flowSpanID) + "-01"} {
		resp := send(bad)
		fresh, ok := observability.ParseTraceparent(resp.Header.Get(observability.TraceparentHeader))
		if !ok || fresh.TraceID == flowTraceID {
			t.Fatalf("traceparent %q gave %q", bad, resp.Header.Get(observability.TraceparentHeader))
		}
	}
}
