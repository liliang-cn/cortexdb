package cortexdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// A recalled standing rule goes into a rules section of its own, before the
// graph facts and the other memories, in the full pack and in the index.
func TestRecallPutsStandingRulesFirst(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "rules.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	project := []ToolEntityInput{{Name: "Athanor", Type: "project"}}
	for _, m := range []MemorySaveRequest{
		{MemoryID: "fact-port", Scope: MemoryScopeGlobal, Content: "Athanor's dev server port is 3076.", Importance: 0.9, Metadata: map[string]any{"type": "fact"}, Entities: project},
		{MemoryID: "rule-port", Scope: MemoryScopeGlobal, Content: "Dev server port: never a common one such as 8080.", Importance: 0.9, Metadata: map[string]any{"type": "preference"}, Entities: project},
		{MemoryID: "weak-port", Scope: MemoryScopeGlobal, Content: "Dev server port preference: no strong view.", Importance: 0.3, Metadata: map[string]any{"type": "preference"}, Entities: project},
	} {
		if _, err := db.SaveMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	req := KnowledgeMemoryRecallRequest{Query: "dev server port", Keywords: []string{"dev", "server", "port"}, EntityNames: []string{"Athanor"}, TopKMemories: 5}
	resp, err := db.KnowledgeMemory().Recall(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Memories) != 3 {
		t.Fatalf("recalled %d memories", len(resp.Memories))
	}
	sections := resp.ContextPack.Sections
	if len(sections) == 0 || sections[0].Kind != "rules" || strings.Join(sections[0].SourceIDs, ",") != "rule-port" {
		t.Fatalf("first section %+v", sections)
	}
	for _, s := range sections[1:] {
		if strings.Contains(s.Text, "rule-port") {
			t.Fatalf("rule repeated in %s", s.Kind)
		}
	}
	if !strings.HasPrefix(resp.ContextPack.Text, "[RULES]\n[rule-port]") {
		t.Fatalf("pack text:\n%s", resp.ContextPack.Text)
	}

	req.Detail = RecallDetailIndex
	index, err := db.KnowledgeMemory().Recall(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(index.ContextPack.Text, "Standing rules (fetch with memory_get memory_ids):\n- rule-port ") {
		t.Fatalf("index text:\n%s", index.ContextPack.Text)
	}
	if s := index.ContextPack.Sections; len(s) != 1 || s[0].Kind != "rules" || s[0].SourceIDs[0] != "rule-port" {
		t.Fatalf("index sections %+v", s)
	}
}

func TestIsStandingRule(t *testing.T) {
	rule := func(mut func(*MemoryRecord)) MemoryRecord {
		m := MemoryRecord{Scope: MemoryScopeGlobal, Importance: 0.85, Metadata: map[string]any{"type": "Preference"}}
		if mut != nil {
			mut(&m)
		}
		return m
	}
	if !IsStandingRule(rule(nil)) {
		t.Fatal("a global preference at 0.85 is a rule")
	}
	if !IsStandingRule(rule(func(m *MemoryRecord) { m.Metadata["type"] = "rule" })) {
		t.Fatal("type rule is a rule")
	}
	for name, m := range map[string]MemoryRecord{
		"session scope": rule(func(m *MemoryRecord) { m.Scope = MemoryScopeSession }),
		"decision":      rule(func(m *MemoryRecord) { m.Metadata["type"] = "decision" }),
		"untyped":       rule(func(m *MemoryRecord) { delete(m.Metadata, "type") }),
		"low":           rule(func(m *MemoryRecord) { m.Importance = 0.6 }),
		"superseded":    rule(func(m *MemoryRecord) { m.Metadata["superseded_by"] = "newer" }),
		"rejected":      rule(func(m *MemoryRecord) { m.ReviewStatus = ReviewRejected }),
	} {
		if IsStandingRule(m) {
			t.Errorf("%s counted as a rule", name)
		}
	}
}

// An All request lists every rule with where it stands against the session
// start's caps, and the global memories one field short of a rule; without
// it the answer is what it was.
func TestStandingRulesAllListsEveryRuleAndTheNearMisses(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "rules.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	typed := func(typ string) map[string]any { return map[string]any{"type": typ} }
	for _, m := range []MemorySaveRequest{
		{MemoryID: "r1", Scope: MemoryScopeGlobal, Content: "Never use port 8080.", Importance: 0.95, Metadata: typed("preference")},
		{MemoryID: "r2", Scope: MemoryScopeGlobal, Content: "Reply in English or Chinese.", Importance: 0.9, Metadata: typed("rule")},
		{MemoryID: "r3", Scope: MemoryScopeGlobal, Content: "Light theme by default.", Importance: 0.85, Metadata: typed("preference")},
		{MemoryID: "r4", Scope: MemoryScopeGlobal, Content: "Athanor's UI is a separate frontend.", Importance: 0.88, Metadata: map[string]any{"type": "preference", "project": "athanor"}},
		{MemoryID: "low", Scope: MemoryScopeGlobal, Content: "Tabs over spaces, maybe.", Importance: 0.3, Metadata: typed("preference")},
		{MemoryID: "untyped-high", Scope: MemoryScopeGlobal, Content: "No co-author lines in commits.", Importance: 0.9},
		{MemoryID: "untyped-low", Scope: MemoryScopeGlobal, Content: "The build took four minutes.", Importance: 0.5},
		{MemoryID: "decision", Scope: MemoryScopeGlobal, Content: "We chose SQLite.", Importance: 0.9, Metadata: typed("decision")},
		{MemoryID: "gone", Scope: MemoryScopeGlobal, Content: "Old rule.", Importance: 0.9, Metadata: map[string]any{"superseded_by": "r1"}},
	} {
		if _, err := db.SaveMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	plain, err := db.StandingRules(ctx, StandingRulesRequest{MaxRules: 2})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plain)
	for _, field := range []string{"ranked", "near_misses", "max_chars", "chars"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Fatalf("a plain answer carries %s: %s", field, raw)
		}
	}

	all, err := db.StandingRules(ctx, StandingRulesRequest{MaxRules: 2, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Rules) != len(plain.Rules) || all.Text != plain.Text || all.Total != plain.Total {
		t.Fatalf("All changed the block: %+v vs %+v", all, plain)
	}
	var ranked []string
	for _, e := range all.Ranked {
		ranked = append(ranked, e.ID+":"+e.Status)
	}
	if got, want := strings.Join(ranked, " "), "r1:injected r2:injected r3:over_budget r4:other_project"; got != want {
		t.Fatalf("ranked %q, want %q", got, want)
	}
	var near []string
	for _, e := range all.NearMisses {
		near = append(near, e.ID+":"+strings.Join(e.Missing, ","))
	}
	if got, want := strings.Join(near, " "), "low:importance"; got != want {
		t.Fatalf("near misses %q, want %q", got, want)
	}
	if all.NearMissTotal != 1 || all.MaxRules != 2 || all.MaxChars != 2400 || all.Chars != utf8.RuneCountInString(all.Text) || all.Chars == 0 {
		t.Fatalf("budget fields %+v", all)
	}
	if e := all.Ranked[0]; e.Content != "Never use port 8080." || e.Type != "preference" || e.CreatedAt == "" || e.Date == "" {
		t.Fatalf("entry %+v", e)
	}

	// In its project, the project's rule is the session's.
	in, err := db.StandingRules(ctx, StandingRulesRequest{Project: "Athanor", All: true})
	if err != nil {
		t.Fatal(err)
	}
	ranked = ranked[:0]
	for _, e := range in.Ranked {
		ranked = append(ranked, e.ID+":"+e.Status)
	}
	if got, want := strings.Join(ranked, " "), "r1:injected r2:injected r4:injected r3:injected"; got != want {
		t.Fatalf("in the project: ranked %q, want %q", got, want)
	}
}

// The recall log keeps which memory hits were standing rules.
func TestRecallLogMarksStandingRules(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "rules.db")), WithRecallLog(RecallLogOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, m := range []MemorySaveRequest{
		{MemoryID: "rule-port", Scope: MemoryScopeGlobal, Content: "Dev server port: never 8080.", Importance: 0.9, Metadata: map[string]any{"type": "preference"}},
		{MemoryID: "fact-port", Scope: MemoryScopeGlobal, Content: "Athanor's dev server port is 3076.", Importance: 0.9, Metadata: map[string]any{"type": "fact"}},
	} {
		if _, err := db.SaveMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "dev server port", Keywords: []string{"dev", "server", "port"}, Detail: RecallDetailIndex}); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListRecalls(ctx, RecallLogListRequest{})
	if err != nil || len(list.Recalls) != 1 {
		t.Fatalf("list %+v %v", list, err)
	}
	entry, err := db.GetRecall(ctx, RecallLogGetRequest{RecallID: list.Recalls[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, h := range entry.Hits {
		if h.Rule == nil {
			t.Fatalf("hit %s carries no rule mark", h.ID)
		}
		got[h.ID] = *h.Rule
	}
	if !got["rule-port"] || got["fact-port"] || len(got) != 2 {
		t.Fatalf("rule marks %v", got)
	}
}
