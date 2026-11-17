package liveview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func saveStandingFixture(t *testing.T, db *cortexdb.DB) {
	t.Helper()
	typed := func(typ string) map[string]any { return map[string]any{"type": typ} }
	for _, m := range []cortexdb.MemorySaveRequest{
		{MemoryID: "r1", Scope: "global", Content: "Dev server port: never 8080.", Importance: 0.95, Metadata: typed("preference")},
		{MemoryID: "r2", Scope: "global", Content: "Reply in English or Chinese.", Importance: 0.9, Metadata: typed("rule")},
		{MemoryID: "r3", Scope: "global", Content: "Athanor's UI is a separate frontend.", Importance: 0.85, Metadata: map[string]any{"type": "preference", "project": "athanor"}},
		{MemoryID: "low", Scope: "global", Content: "Tabs over spaces.", Importance: 0.3, Metadata: typed("preference")},
		{MemoryID: "untyped", Scope: "global", Content: "No co-author lines in commits.", Importance: 0.9},
		{MemoryID: "fact", Scope: "global", Content: "The dev server port of Athanor is 3076.", Importance: 0.9, Metadata: typed("fact")},
	} {
		if _, err := db.SaveMemory(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheRulesRouteListsEveryRuleAndTheNearMisses(t *testing.T) {
	db, h := recallsServer(t)
	saveStandingFixture(t, db)

	var v StandingRulesView
	if code := get(t, h, "/api/recalls/rules", &v); code != http.StatusOK {
		t.Fatalf("rules: %d", code)
	}
	var rows []string
	for _, r := range v.Rules {
		rows = append(rows, r.ID+":"+r.Status)
	}
	if got, want := strings.Join(rows, " "), "r1:injected r2:injected r3:other_project"; got != want {
		t.Fatalf("rules %q, want %q", got, want)
	}
	if v.Rules[0].NodeID != "memory:r1" || v.Rules[0].Content != "Dev server port: never 8080." {
		t.Fatalf("row %+v", v.Rules[0])
	}
	rows = rows[:0]
	for _, r := range v.NearMisses {
		rows = append(rows, r.ID+":"+strings.Join(r.Missing, ","))
	}
	if got, want := strings.Join(rows, " "), "low:importance"; got != want {
		t.Fatalf("near misses %q, want %q", got, want)
	}
	if v.Injected != 2 || v.MaxChars != 2400 || v.MaxRules != 16 || v.Chars == 0 || v.Partial || v.NearMissTotal != 1 {
		t.Fatalf("view %+v", v)
	}
	if strings.Join(v.Projects, ",") != "athanor" {
		t.Fatalf("projects %v", v.Projects)
	}

	// In the project, its rule is injected too.
	if code := get(t, h, "/api/recalls/rules?project=Athanor", &v); code != http.StatusOK || v.Injected != 3 || v.Project != "Athanor" {
		t.Fatalf("in the project: %d %+v", code, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/recalls/rules", strings.NewReader("{}")))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
}

// A brain older than the all option answers the injected rules alone; the
// page is told the list is partial rather than shown it as complete.
func TestAnOldBrainsRulesArePartial(t *testing.T) {
	v := standingRulesView("", cortexdb.StandingRulesResponse{
		Rules: []cortexdb.StandingRule{{ID: "r1", Content: "never 8080", Importance: 0.9}},
		Total: 3, Text: "Standing rules from CortexDB:\n- [r1] never 8080",
	})
	if !v.Partial || len(v.Rules) != 1 || v.Rules[0].Status != cortexdb.StandingRuleInjected || v.Injected != 1 || v.Chars == 0 {
		t.Fatalf("view %+v", v)
	}
}

// A Source whose caller does not run the tool is a failed read, not an empty
// list of rules.
func TestASourceWithoutTheToolFailsTheRulesRead(t *testing.T) {
	src := &Source{
		Describe: "no rules",
		Read:     func(context.Context) ([]Node, []Edge, error) { return nil, nil, nil },
		Call: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("unknown tool memory_standing_rules")
		},
	}
	sv, err := New(context.Background(), src, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	var out map[string]string
	if code := get(t, sv.Handler(), "/api/recalls/rules", &out); code == http.StatusOK || out["error"] == "" {
		t.Fatalf("a refused tool: %d %v", code, out)
	}
}

// A recall logged now carries its rule marks; one logged before is marked by
// what its memories are now, and says so.
func TestARecallOpensWithItsRulesMarked(t *testing.T) {
	db, h := recallsServer(t, cortexdb.WithRecallLog(cortexdb.RecallLogOptions{}))
	saveStandingFixture(t, db)
	ctx := context.Background()
	if _, err := db.KnowledgeMemory().Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{Query: "dev server port", Keywords: []string{"dev", "server", "port"}}); err != nil {
		t.Fatal(err)
	}
	var list cortexdb.RecallLogListResponse
	if code := get(t, h, "/api/recalls", &list); code != 200 || len(list.Recalls) != 1 {
		t.Fatalf("timeline: %d %+v", code, list)
	}
	var one RecallWithRules
	if code := get(t, h, "/api/recalls/get?id="+list.Recalls[0].ID, &one); code != 200 || one.RulesNow {
		t.Fatalf("recall: %d %+v", code, one)
	}
	marks := map[string]bool{}
	for _, hit := range one.Hits {
		if hit.Rule != nil {
			marks[hit.ID] = *hit.Rule
		}
	}
	if !marks["r1"] || marks["fact"] {
		t.Fatalf("marks %v in %+v", marks, one.Hits)
	}

	// The same recall as a log from before the marks would hold it.
	old := one.RecallLogEntry
	old.Hits = append([]cortexdb.RecallLogHit(nil), one.Hits...)
	for i := range old.Hits {
		old.Hits[i].Rule = nil
	}
	sv := &Server{src: &Source{Call: localCaller(db)}}
	got := sv.markRecallRules(ctx, old)
	if !got.RulesNow {
		t.Fatal("an unmarked recall is not said to be marked now")
	}
	for _, hit := range got.Hits {
		if hit.Kind == "memory" && (hit.Rule == nil || *hit.Rule != marks[hit.ID]) {
			t.Fatalf("hit %s marked %v, want %v", hit.ID, hit.Rule, marks[hit.ID])
		}
	}
	if old.Hits[0].Rule != nil {
		t.Fatal("marking changed the log's own entry")
	}

	// Memories that cannot be read leave the recall unmarked, and answered.
	refuse := &Server{src: &Source{Call: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("refused")
	}}}
	if got := refuse.markRecallRules(ctx, old); got.RulesNow || len(got.Hits) != len(old.Hits) {
		t.Fatalf("refused memory_get: %+v", got)
	}
}
