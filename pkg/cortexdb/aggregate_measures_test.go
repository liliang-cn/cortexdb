package cortexdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// openAnalysisBrain holds seven records under two kinds, stored in
// September, October and November 2026, with a score and a latency each —
// one score is not a number.
func openAnalysisBrain(t *testing.T) *DB {
	t.Helper()
	config := DefaultConfig(filepath.Join(t.TempDir(), "analysis.db"))
	config.Dimensions = 3
	db, err := Open(config)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	c := context.Background()
	for _, r := range []struct{ id, kind, score, latency, created string }{
		{"a1", "incident", "0.9", "900", "2026-09-12 10:00:00"},
		{"a2", "incident", "0.8", "120", "2026-10-03 09:00:00"},
		{"a3", "incident", "0.6", "480", "2026-10-21 17:30:00"},
		{"a4", "incident", "n/a", "300", "2026-10-28 08:00:00"},
		{"a5", "note", "0.4", "40", "2026-10-05 12:00:00"},
		{"a6", "note", "0.2", "60", "2026-11-02 12:00:00"},
		{"a7", "note", "0.3", "80", "2026-11-09 12:00:00"},
	} {
		if err := db.Vector().Upsert(c, &core.Embedding{
			ID: r.id, Vector: []float32{1, 0, 0}, Content: r.id,
			Metadata: map[string]string{"kind": r.kind, "score": r.score, "latency_ms": r.latency},
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if _, err := db.SQL().ExecContext(c, `UPDATE embeddings SET created_at = ? WHERE id = ?`, r.created, r.id); err != nil {
			t.Fatalf("set created_at: %v", err)
		}
	}
	return db
}

// "Per month, per kind: count, average score and p95 latency, for records
// created since October" is one call, and its answer is what the arithmetic
// says.
func TestAggregateMetadataAnswersAnAnalysisQuestionInOneCall(t *testing.T) {
	db := openAnalysisBrain(t)
	raw, err := db.GraphRAGTools().Call(context.Background(), "aggregate_metadata", json.RawMessage(`{
		"measures": [
			{"type": "count"},
			{"type": "avg", "field": "score"},
			{"type": "p95", "field": "latency_ms"}
		],
		"time_bucket": {"unit": "month"},
		"group_by": ["kind"],
		"created_since": "2026-10-01"
	}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	// avg over 0.8 and 0.6 is 0.7 with the n/a row left out and reported;
	// p95 over 120, 300, 480 is 300 + 0.9*180.
	want := `{"results":[` +
		`{"keys":{"kind":"incident","month":"2026-10"},"value":3,"count":3,"values":{"avg_score":0.7,"count":3,"p95_latency_ms":462},"skipped":{"avg_score":1}},` +
		`{"keys":{"kind":"note","month":"2026-10"},"value":1,"count":1,"values":{"avg_score":0.4,"count":1,"p95_latency_ms":40}},` +
		`{"keys":{"kind":"note","month":"2026-11"},"value":2,"count":2,"values":{"avg_score":0.25,"count":2,"p95_latency_ms":79}}` +
		`],"total":3}`
	if string(encoded) != want {
		t.Fatalf("answer:\n got %s\nwant %s", encoded, want)
	}
}

// The request shapes that existed before answer as they did: the same fields,
// and no new ones.
func TestAggregateMetadataOldShapesAnswerAsBefore(t *testing.T) {
	db := openAnalysisBrain(t)
	tools := db.GraphRAGTools()
	for args, want := range map[string]string{
		`{"type":"count"}`:                                           `{"results":[{"value":7,"count":7}],"total":1}`,
		`{"type":"count","filters":{"kind":"note"}}`:                 `{"results":[{"value":3,"count":3}],"total":1}`,
		`{"type":"max","field":"latency_ms"}`:                        `{"results":[{"value":900,"count":7}],"total":1}`,
		`{"type":"group_by","group_by":["kind"],"order_by":"count"}`: `{"results":[{"keys":{"kind":"incident"},"value":4,"count":4},{"keys":{"kind":"note"},"value":3,"count":3}],"total":2}`,
		`{"type":"group_by","group_by":["kind"],"limit":1}`:          `{"results":[{"keys":{"kind":"incident"},"value":4,"count":4}],"total":1}`,
	} {
		raw, err := tools.Call(context.Background(), "aggregate_metadata", json.RawMessage(args))
		if err != nil {
			t.Errorf("%s: %v", args, err)
			continue
		}
		got, _ := json.Marshal(raw)
		if string(got) != want {
			t.Errorf("%s:\n got %s\nwant %s", args, got, want)
		}
	}

	// The single sum still reads the n/a score as 0 and counts its row.
	sum, err := tools.AggregateMetadata(context.Background(), ToolAggregateMetadataRequest{Type: "sum", Field: "score", Filters: map[string]any{"kind": "incident"}})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := sum.Results[0].Value.(float64); v < 2.2999 || v > 2.3001 || sum.Results[0].Count != 4 || sum.Results[0].Values != nil || sum.Results[0].Skipped != nil {
		t.Errorf("single sum = %+v, want 2.3 over 4 rows and nothing else", sum.Results[0])
	}
}

// A filter value may be a string, an operator object, or a bare number,
// which compares by value.
func TestAggregateMetadataFilterValues(t *testing.T) {
	db := openAnalysisBrain(t)
	tools := db.GraphRAGTools()
	for args, want := range map[string]int{
		`{"type":"count","filters":{"score":{"gte":0.5}}}`:                                 3,
		`{"type":"count","filters":{"kind":"incident","latency_ms":{"gt":120,"lte":480}}}`: 2,
		`{"type":"count","filters":{"kind":{"in":["note"]},"score":{"lt":0.4}}}`:           2,
		`{"type":"count","filters":{"kind":{"ne":"note"}}}`:                                4,
		`{"type":"count","filters":{"latency_ms":900}}`:                                    1,
		`{"type":"count","filters":{"latency_ms":"900"}}`:                                  1,
		`{"type":"count","filters":{"missing":{"exists":false}}}`:                          7,
		`{"type":"count","created_before":"2026-10-01"}`:                                   1,
		`{"type":"count","created_since":"2026-10-01","created_before":"2026-11-01"}`:      4,
	} {
		raw, err := tools.Call(context.Background(), "aggregate_metadata", json.RawMessage(args))
		if err != nil {
			t.Errorf("%s: %v", args, err)
			continue
		}
		if got := raw.(*ToolAggregateMetadataResponse).Results[0].Count; got != want {
			t.Errorf("%s: counted %d, want %d", args, got, want)
		}
	}
	for args, want := range map[string]string{
		`{"type":"count","filters":{"kind":["note"]}}`:                      "a filter value must be",
		`{"type":"count","filters":{"kind":{"like":"n%"}}}`:                 "unknown filter operator",
		`{"measures":[{"type":"avg","field":"score); DROP TABLE x"}]}`:      "measure field is not a valid metadata name",
		`{"time_bucket":{"unit":"month; DROP TABLE x"}}`:                    "time_bucket unit must be one of",
		`{"type":"count","filters":{"_review_status":{"in":["proposed"]}}}`: "takes a plain status value",
	} {
		if _, err := tools.Call(context.Background(), "aggregate_metadata", json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want a refusal saying %q", args, err, want)
		}
	}
}

// The measure path counts visible rows only, as the single aggregation does:
// not an expired document's chunks, not a proposed one's under the review
// gate unless asked for, and a filter on _review_status still lists what
// waits.
func TestMeasurePathCountsVisibleRowsOnly(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		db, tools, c := openEmbeddedBrain(t, "measure-expired")
		content := "The zebra ledger reconciles the quarterly accounts."
		if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "old", Content: content, Collection: "ops", ExpiresAt: strPtr(pastInstant())}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "live", Content: content, Collection: "ops"}); err != nil {
			t.Fatal(err)
		}
		for name, req := range map[string]ToolAggregateMetadataRequest{
			"measures":      {Collection: "ops", Measures: []core.AggregationMeasure{{Type: "count"}}},
			"time bucket":   {Collection: "ops", TimeBucket: &core.AggregationTimeBucket{Unit: "year"}},
			"created bound": {Type: "count", Collection: "ops", CreatedSince: "2000-01-01"},
			"operator":      {Type: "count", Collection: "ops", Filters: map[string]any{"no_such_field": map[string]any{"exists": false}}},
		} {
			resp, err := tools.AggregateMetadata(c, req)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if len(resp.Results) != 1 || resp.Results[0].Count != 1 {
				t.Errorf("%s counted %+v, want the live chunk alone", name, resp.Results)
			}
		}
	})

	t.Run("review gate", func(t *testing.T) {
		db := openReviewBrain(t, "measure-review", WithReviewGate())
		saveReviewed(t, db) // plain, proposed, rejected, accepted
		tools := db.GraphRAGTools()
		measures := []core.AggregationMeasure{{Type: "count"}}
		count := func(req ToolAggregateMetadataRequest) int {
			t.Helper()
			req.Measures = measures
			resp, err := tools.AggregateMetadata(ctx(), req)
			if err != nil {
				t.Fatal(err)
			}
			return resp.Results[0].Count
		}
		if got := count(ToolAggregateMetadataRequest{}); got != 2 {
			t.Errorf("under the gate the measure path counted %d, want the plain and the accepted chunk", got)
		}
		if got := count(ToolAggregateMetadataRequest{IncludeProposed: true}); got != 3 {
			t.Errorf("with include_proposed it counted %d, want 3", got)
		}
		if got := count(ToolAggregateMetadataRequest{Filters: map[string]any{reviewStatusKey: ReviewProposed}}); got != 1 {
			t.Errorf("filtered on proposed it counted %d, want the one that waits", got)
		}
		if got := count(ToolAggregateMetadataRequest{Filters: map[string]any{reviewStatusKey: ReviewProposed, "document_id": map[string]any{"ne": "k-prop"}}}); got != 0 {
			t.Errorf("proposed and not k-prop counted %d, want 0", got)
		}
	})
}

// Over MCP, which checks each call against a schema of its own before the
// handler sees it, the new arguments get through.
func TestAggregateMetadataMeasuresOverMCP(t *testing.T) {
	db := openAnalysisBrain(t)
	c := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := db.NewMCPServer(MCPServerOptions{}).Connect(c, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "measures-test"}, nil).Connect(c, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.CallTool(c, &mcp.CallToolParams{Name: "aggregate_metadata", Arguments: map[string]any{
		"measures":      []any{map[string]any{"type": "count"}, map[string]any{"type": "percentile", "field": "latency_ms", "percentile": 50, "alias": "median_ms"}},
		"time_bucket":   map[string]any{"unit": "month", "timezone": "UTC"},
		"filters":       map[string]any{"kind": "incident", "score": map[string]any{"gte": 0.5}},
		"created_since": "2026-10-01",
		"order":         "desc",
	}})
	if err != nil || result.IsError {
		t.Fatalf("aggregate_metadata over MCP: %v %+v", err, result)
	}
	payload, _ := json.Marshal(result.StructuredContent)
	want := `{"results":[{"count":2,"keys":{"month":"2026-10"},"value":2,"values":{"count":2,"median_ms":300}}],"total":1}`
	if string(payload) != want {
		t.Fatalf("over MCP:\n got %s\nwant %s", payload, want)
	}
}
