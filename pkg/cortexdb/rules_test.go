package cortexdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func openRuleTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	dbPath := fmt.Sprintf("test_rules_%d.db", testname.Nano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	})
	return db, context.Background()
}

// seedSocrates writes the classic through the public knowledge path, so the
// rules run over the same nodes and edges a real ingest produces.
func seedSocrates(t *testing.T, ctx context.Context, db *DB) {
	t.Helper()
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "socrates",
		Title:       "Socrates",
		Content:     "Socrates is a human. Humans are mortal.",
		ChunkSize:   32,
		Entities: []ToolEntityInput{
			{Name: "Socrates", Type: "person", ChunkIDs: []string{"chunk:socrates:000"}},
			{Name: "Human", Type: "class", ChunkIDs: []string{"chunk:socrates:000"}},
			{Name: "Mortal", Type: "class", ChunkIDs: []string{"chunk:socrates:000"}},
		},
		Relations: []ToolRelationInput{
			{From: "Socrates", To: "Human", Type: "instance_of"},
			{From: "Human", To: "Mortal", Type: "subclass_of"},
		},
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}
}

func TestRulesApplyDerivesTheSocratesConclusion(t *testing.T) {
	db, ctx := openRuleTestDB(t)
	seedSocrates(t, ctx, db)

	resp, err := db.ApplyRules(ctx, RulesApplyRequest{
		Rules: []RuleDefinition{{
			ID:   "mortality",
			Text: "IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)",
		}},
	})
	if err != nil {
		t.Fatalf("apply rules: %v", err)
	}
	if len(resp.CreatedEdgeIDs) != 1 {
		t.Fatalf("derived %v, want exactly one edge", resp.CreatedEdgeIDs)
	}
	edge := resp.Edges[0]
	if edge.FromNodeID != graphEntityNodeID("Socrates") || edge.ToNodeID != graphEntityNodeID("Mortal") {
		t.Fatalf("derived %s -> %s", edge.FromNodeID, edge.ToNodeID)
	}
	if edge.EdgeType != "instance_of" || edge.RuleID != "mortality" {
		t.Fatalf("derived edge: %+v", edge)
	}
	if !strings.HasPrefix(edge.RuleText, "IF instance_of(?x, ?c)") {
		t.Errorf("rule text %q", edge.RuleText)
	}

	// And the second run adds nothing: the rule is a statement, not a command
	// to append.
	again, err := db.ApplyRules(ctx, RulesApplyRequest{
		Rules: []RuleDefinition{{
			ID:   "mortality",
			Text: "IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)",
		}},
	})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(again.CreatedEdgeIDs) != 0 {
		t.Fatalf("second run derived %v", again.CreatedEdgeIDs)
	}
	if len(again.UnchangedEdgeIDs) != 1 {
		t.Fatalf("second run reported unchanged %v, want the one edge", again.UnchangedEdgeIDs)
	}
}

func TestInferenceExplainShowsTheRuleAndThePremises(t *testing.T) {
	db, ctx := openRuleTestDB(t)
	seedSocrates(t, ctx, db)

	applied, err := db.ApplyRules(ctx, RulesApplyRequest{
		Rules: []RuleDefinition{{
			ID:         "mortality",
			Text:       "IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)",
			Confidence: 0.8,
		}},
	})
	if err != nil {
		t.Fatalf("apply rules: %v", err)
	}

	explained, err := db.ExplainInference(ctx, InferenceExplainRequest{EdgeID: applied.CreatedEdgeIDs[0]})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	explanation := explained.Explanation
	if !explanation.Inferred || explanation.RuleID != "mortality" {
		t.Fatalf("explanation: %+v", explanation)
	}
	if !strings.Contains(explanation.RuleText, "THEN instance_of(?x, ?d)") {
		t.Errorf("explanation does not carry the rule text: %q", explanation.RuleText)
	}
	if explanation.Confidence != 0.8 {
		t.Errorf("confidence %v, want 0.8", explanation.Confidence)
	}
	if len(explanation.SupportEdgeIDs) != 2 {
		t.Fatalf("explanation names %d premises, want 2", len(explanation.SupportEdgeIDs))
	}
	// The trace is the chain, flattened: the conclusion at depth 0 and its two
	// premises under it, in the order the rule states them.
	if len(explained.Trace) != 3 {
		t.Fatalf("trace has %d entries, want the conclusion and its two premises: %+v", len(explained.Trace), explained.Trace)
	}
	types := []string{explained.Trace[1].Explanation.EdgeType, explained.Trace[2].Explanation.EdgeType}
	if types[0] != "instance_of" || types[1] != "subclass_of" {
		t.Errorf("premise chain %v, want the rule's premises in order", types)
	}
	for _, entry := range explained.Trace[1:] {
		if entry.Depth != 1 || entry.ParentEdgeID != explanation.EdgeID {
			t.Errorf("premise entry %+v is not attached to the conclusion", entry)
		}
		if entry.Explanation.Inferred {
			t.Errorf("premise %s should be explicit", entry.EdgeID)
		}
	}
}

// apply_inference is a rule now, so an edge it derived has to explain itself
// through the same path as one rules_apply derived. That is the whole point of
// having one engine.
func TestApplyInferenceEdgesExplainThroughTheSamePath(t *testing.T) {
	db, ctx := openRuleTestDB(t)
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "acme",
		Content:     "Alice works at Acme. Acme is located in Berlin.",
		ChunkSize:   24,
		Entities: []ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{"chunk:acme:000"}},
			{Name: "Acme", Type: "organization", ChunkIDs: []string{"chunk:acme:000"}},
			{Name: "Berlin", Type: "city", ChunkIDs: []string{"chunk:acme:000"}},
		},
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at"},
			{From: "Acme", To: "Berlin", Type: "located_in"},
		},
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}

	resp, err := db.ApplyInferenceRules(ctx, ApplyInferenceRequest{
		DocumentID: "acme",
		Rules: []InferenceRule{{
			RuleID:             "employment_city",
			LeftRelationType:   "works_at",
			RightRelationType:  "located_in",
			ResultRelationType: "works_in_city",
		}},
	})
	if err != nil {
		t.Fatalf("apply inference: %v", err)
	}
	if len(resp.CreatedEdgeIDs) != 1 {
		t.Fatalf("created %v", resp.CreatedEdgeIDs)
	}

	explained, err := db.ExplainInference(ctx, InferenceExplainRequest{EdgeID: resp.CreatedEdgeIDs[0]})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if explained.Explanation.RuleID != "employment_city" {
		t.Fatalf("explanation: %+v", explained.Explanation)
	}
	want := "IF works_at(?x, ?y) AND located_in(?y, ?z) THEN works_in_city(?x, ?z)"
	if explained.Explanation.RuleText != want {
		t.Errorf("rule text %q, want %q", explained.Explanation.RuleText, want)
	}
	if len(explained.Trace) != 3 {
		t.Fatalf("trace %+v, want the conclusion and its two premises", explained.Trace)
	}
}

func TestRulesApplyDryRunWritesNothing(t *testing.T) {
	db, ctx := openRuleTestDB(t)
	seedSocrates(t, ctx, db)

	resp, err := db.ApplyRules(ctx, RulesApplyRequest{
		DryRun: true,
		Rules: []RuleDefinition{{
			ID:   "mortality",
			Text: "IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)",
		}},
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(resp.CreatedEdgeIDs) != 1 || !resp.DryRun {
		t.Fatalf("dry run should report what it would write: %+v", resp)
	}
	edges, err := db.Graph().GetEdges(ctx, graphEntityNodeID("Socrates"), "out")
	if err != nil {
		t.Fatalf("get edges: %v", err)
	}
	for _, edge := range edges {
		if inferred, _ := edge.Properties["inferred"].(bool); inferred {
			t.Fatalf("dry run wrote %s", edge.ID)
		}
	}
}

func TestRulesApplyCapIsAnErrorNotAPartialGraph(t *testing.T) {
	db, ctx := openRuleTestDB(t)
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "cycle",
		Content:     "A reaches B, B reaches C, C reaches A.",
		ChunkSize:   32,
		Entities: []ToolEntityInput{
			{Name: "A", Type: "node", ChunkIDs: []string{"chunk:cycle:000"}},
			{Name: "B", Type: "node", ChunkIDs: []string{"chunk:cycle:000"}},
			{Name: "C", Type: "node", ChunkIDs: []string{"chunk:cycle:000"}},
		},
		Relations: []ToolRelationInput{
			{From: "A", To: "B", Type: "reaches"},
			{From: "B", To: "C", Type: "reaches"},
			{From: "C", To: "A", Type: "reaches"},
		},
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}

	rule := RuleDefinition{ID: "reach", Text: "IF reaches(?x, ?y) AND reaches(?y, ?z) THEN reaches(?x, ?z)"}
	_, err := db.ApplyRules(ctx, RulesApplyRequest{Rules: []RuleDefinition{rule}, MaxDerived: 2})
	if !errors.Is(err, graph.ErrRuleCapExceeded) {
		t.Fatalf("error is %v, want ErrRuleCapExceeded", err)
	}
	if !strings.Contains(err.Error(), "more than 2 edges") {
		t.Errorf("the error does not say which cap: %v", err)
	}

	// Nothing was written, so the same rule set still reaches its fixpoint on
	// the next attempt rather than resuming from a half-built closure.
	ok, err := db.ApplyRules(ctx, RulesApplyRequest{Rules: []RuleDefinition{rule}})
	if err != nil {
		t.Fatalf("apply after cap: %v", err)
	}
	if len(ok.CreatedEdgeIDs) != 6 {
		t.Fatalf("derived %d edges after the capped run, want the full closure of 6", len(ok.CreatedEdgeIDs))
	}
}

func TestRulesSaveListDeleteAndApplyByID(t *testing.T) {
	db, ctx := openRuleTestDB(t)
	seedSocrates(t, ctx, db)

	saved, err := db.SaveRules(ctx, RulesSaveRequest{Rules: []RuleDefinition{
		{
			ID:   "mortality",
			Name: "Mortality",
			When: []graph.Atom{
				{Predicate: "instance_of", Subject: "?x", Object: "?c"},
				{Predicate: "subclass_of", Subject: "?c", Object: "?d"},
			},
			Then: &graph.Atom{Predicate: "instance_of", Subject: "?x", Object: "?d"},
		},
		{
			ID:      "disabled",
			Text:    "IF instance_of(?x, ?c) THEN member_of(?x, ?c)",
			Enabled: boolPtr(false),
		},
	}})
	if err != nil {
		t.Fatalf("save rules: %v", err)
	}
	if len(saved.Rules) != 2 {
		t.Fatalf("saved %d rules", len(saved.Rules))
	}
	if saved.Rules[0].Text != "IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)" {
		t.Errorf("a rule built as a struct did not render into the written form: %q", saved.Rules[0].Text)
	}

	listed, err := db.ListRules(ctx, RulesListRequest{OnlyEnabled: true})
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(listed.Rules) != 1 || listed.Rules[0].ID != "mortality" {
		t.Fatalf("enabled rules %+v", listed.Rules)
	}

	// With no rules named, every enabled rule runs — so the disabled one must
	// not fire.
	applied, err := db.ApplyRules(ctx, RulesApplyRequest{})
	if err != nil {
		t.Fatalf("apply declared rules: %v", err)
	}
	if fmt.Sprint(applied.RuleIDs) != "[mortality]" {
		t.Fatalf("ran %v, want only the enabled rule", applied.RuleIDs)
	}
	if len(applied.CreatedEdgeIDs) != 1 {
		t.Fatalf("derived %v", applied.CreatedEdgeIDs)
	}

	deleted, err := db.DeleteRules(ctx, RulesDeleteRequest{RuleIDs: []string{"disabled", "never_declared"}})
	if err != nil {
		t.Fatalf("delete rules: %v", err)
	}
	if fmt.Sprint(deleted.DeletedRuleIDs) != "[disabled]" || fmt.Sprint(deleted.MissingRuleIDs) != "[never_declared]" {
		t.Fatalf("delete reported %+v", deleted)
	}

	// A deleted rule leaves its edges, and they stay explicable because they
	// carry the rule text rather than a pointer to a row.
	if _, err := db.DeleteRules(ctx, RulesDeleteRequest{RuleIDs: []string{"mortality"}}); err != nil {
		t.Fatalf("delete mortality: %v", err)
	}
	explained, err := db.ExplainInference(ctx, InferenceExplainRequest{EdgeID: applied.CreatedEdgeIDs[0]})
	if err != nil {
		t.Fatalf("explain after the rule was deleted: %v", err)
	}
	if !strings.Contains(explained.Explanation.RuleText, "THEN instance_of") {
		t.Errorf("the edge stopped explaining itself once its rule was gone: %+v", explained.Explanation)
	}
}

func TestRuleDefinitionRefusesAmbiguousInput(t *testing.T) {
	both := RuleDefinition{
		ID:   "both",
		Text: "IF p(?x, ?y) THEN q(?x, ?y)",
		Then: &graph.Atom{Predicate: "q", Subject: "?x", Object: "?y"},
	}
	if _, err := both.Rule(); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("a definition with two disagreeing forms was accepted: %v", err)
	}
	empty := RuleDefinition{ID: "empty"}
	if _, err := empty.Rule(); err == nil {
		t.Error("a definition with no rule in it was accepted")
	}
}

func TestRuleToolsDispatchThroughTheToolbox(t *testing.T) {
	db, ctx := openRuleTestDB(t)
	seedSocrates(t, ctx, db)
	tools := db.GraphRAGTools()

	if _, err := tools.Call(ctx, "rules_save", json.RawMessage(`{
		"rules": [{"id": "mortality", "text": "IF instance_of(?x, ?c) AND subclass_of(?c, ?d) THEN instance_of(?x, ?d)"}]
	}`)); err != nil {
		t.Fatalf("rules_save: %v", err)
	}

	raw, err := tools.Call(ctx, "rules_apply", json.RawMessage(`{"rule_ids": ["mortality"], "dry_run": true}`))
	if err != nil {
		t.Fatalf("rules_apply: %v", err)
	}
	applied, ok := raw.(*RulesApplyResponse)
	if !ok {
		t.Fatalf("rules_apply returned %T", raw)
	}
	if len(applied.CreatedEdgeIDs) != 1 || !applied.DryRun {
		t.Fatalf("rules_apply dry run: %+v", applied)
	}

	listed, err := tools.Call(ctx, "rules_list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("rules_list: %v", err)
	}
	if got := listed.(*RulesListResponse); len(got.Rules) != 1 {
		t.Fatalf("rules_list: %+v", got.Rules)
	}

	// A bad rule reaches the caller as a parse error naming the position, not
	// as a rule that silently matches nothing.
	_, err = tools.Call(ctx, "rules_save", json.RawMessage(`{"rules": [{"id": "bad", "text": "IF p(?x, ?y THEN q(?x, ?y)"}]}`))
	if err == nil || !strings.Contains(err.Error(), "offset") {
		t.Fatalf("rules_save accepted or mis-reported a broken rule: %v", err)
	}
}

func boolPtr(v bool) *bool { return &v }
