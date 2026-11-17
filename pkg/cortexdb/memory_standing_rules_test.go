package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
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
