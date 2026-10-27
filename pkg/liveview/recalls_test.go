package liveview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Every recall-log tool the page may run exists, and only recall_feedback
// writes — a mark in the recall log, sent from the page's own origin.
func TestTheRecallToolsReadButTheMark(t *testing.T) {
	defs := map[string]cortexdb.ToolDefinition{}
	for _, d := range cortexdb.ToolDefinitions() {
		defs[d.Name] = d
	}
	for _, name := range RecallTools {
		d, ok := defs[name]
		if !ok {
			t.Errorf("%s is on RecallTools but no tool has that name", name)
			continue
		}
		if d.Mutates != (name == "recall_feedback") {
			t.Errorf("%s: Mutates=%v", name, d.Mutates)
		}
	}
}

func recallsServer(t *testing.T, opts ...cortexdb.Option) (*cortexdb.DB, http.Handler) {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "recalls.db")), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	src := &Source{
		Describe: "recalls <test>",
		Read:     func(ctx context.Context) ([]Node, []Edge, error) { return LoadLocal(ctx, db.SQL()) },
		Call:     localCaller(db),
	}
	sv, err := New(context.Background(), src, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	return db, sv.Handler()
}

func get(t *testing.T, h http.Handler, path string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if out != nil {
		_ = json.Unmarshal(rec.Body.Bytes(), out)
	}
	return rec.Code
}

func TestTheRecallsPageReadsTheLogAndTakesMarksFromItself(t *testing.T) {
	db, h := recallsServer(t, cortexdb.WithRecallLog(cortexdb.RecallLogOptions{}))
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "m1", Scope: "global", Content: "the brain's VIP is 192.168.123.253"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.KnowledgeMemory().Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{Query: "brain VIP"}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/recalls", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"recalls `+"\\u003c"+`test`+"\\u003e"+`"`) || strings.Contains(rec.Body.String(), "__SOURCE__") {
		t.Fatalf("page: %d, source not injected safely: %q", rec.Code, rec.Body.String()[:200])
	}

	var list cortexdb.RecallLogListResponse
	if code := get(t, h, "/api/recalls", &list); code != 200 || len(list.Recalls) != 1 || list.Recalls[0].Hits[0].ID != "m1" {
		t.Fatalf("timeline: %d %+v", code, list)
	}
	id := list.Recalls[0].ID
	var one cortexdb.RecallLogEntry
	if code := get(t, h, "/api/recalls/get?id="+id, &one); code != 200 || one.Explain == nil {
		t.Fatalf("explain: %d %+v", code, one)
	}
	var heat RecallHeat
	if code := get(t, h, "/api/recalls/heat", &heat); code != 200 || heat.Nodes["memory:m1"] != 1 || heat.Max != 1 {
		t.Fatalf("heat: %d %+v", code, heat)
	}

	mark := func(origin, contentType string) int {
		req := httptest.NewRequest("POST", "/api/recalls/feedback", strings.NewReader(`{"recall_id":"`+id+`","record_id":"m1","verdict":"useful"}`))
		req.Header.Set("Content-Type", contentType)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := mark("https://evil.example", "application/json"); code != http.StatusForbidden {
		t.Fatalf("a mark from another origin: %d", code)
	}
	if code := mark("", "application/x-www-form-urlencoded"); code != http.StatusBadRequest {
		t.Fatalf("a form post: %d", code)
	}
	if code := mark("http://example.com", "application/json"); code != 200 {
		t.Fatalf("a mark from the page: %d", code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/recalls/golden", nil))
	if !strings.Contains(rec.Body.String(), `"expected":["m1"]`) || !strings.Contains(rec.Header().Get("Content-Disposition"), "golden") {
		t.Fatalf("golden: %q", rec.Body.String())
	}
}

// A brain without the log says so; the page turns that into words.
func TestTheRecallsRoutesSayWhenTheLogIsOff(t *testing.T) {
	_, h := recallsServer(t)
	var out map[string]string
	if code := get(t, h, "/api/recalls", &out); code == 200 || !strings.Contains(out["error"], "recall log is off") {
		t.Fatalf("an off log: %d %v", code, out)
	}
}
