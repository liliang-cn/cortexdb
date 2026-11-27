package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// vectorGraphStore is a store where the passage that answers "who owns the
// mobile push service" shares no word with the question: the mobile app's
// page names Beacon, and only Beacon's page names its owner. Three more
// pages share the question's words and nothing else.
func vectorGraphStore(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	embedder := newKeywordEmbedder("mobile", "push", "app", "notifications", "pager", "duty", "invoices")
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "walk.db"))
	cfg.Dimensions = embedder.Dim()
	db, err := Open(cfg, WithEmbedder(embedder))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	docs := []KnowledgeSaveRequest{
		{KnowledgeID: "ember", Title: "Ember", Content: "Ember is the mobile app. Its push notifications go through Beacon.",
			Entities:  []ToolEntityInput{{Name: "Ember", Type: "Product"}, {Name: "Beacon", Type: "Product"}},
			Relations: []ToolRelationInput{{From: "Ember", Type: "depends_on", To: "Beacon"}}},
		{KnowledgeID: "beacon-owner", Title: "Beacon", Content: "Beacon is run by Clara Weiss, who carries its pager duty.",
			Entities:  []ToolEntityInput{{Name: "Beacon", Type: "Product"}, {Name: "Clara Weiss", Type: "Person"}},
			Relations: []ToolRelationInput{{From: "Clara Weiss", Type: "owns", To: "Beacon"}}},
		{KnowledgeID: "mobile-style", Title: "Mobile style", Content: "The mobile style guide covers app icons and push copy."},
		{KnowledgeID: "mobile-qa", Title: "Mobile QA", Content: "Mobile QA tests every app build on four phones."},
		{KnowledgeID: "mobile-hiring", Title: "Mobile hiring", Content: "Two open mobile roles on the app team."},
	}
	for _, d := range docs {
		if _, err := db.SaveKnowledge(ctx, d); err != nil {
			t.Fatalf("save %s: %v", d.KnowledgeID, err)
		}
	}
	return db, ctx
}

func knowledgeIDs(resp *KnowledgeSearchResponse) []string {
	ids := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		ids = append(ids, r.KnowledgeID)
	}
	return ids
}

func TestKnowledgeSearchWalksTheGraphFromVectorHits(t *testing.T) {
	db, ctx := vectorGraphStore(t)
	const q = "who owns the mobile app push notifications"

	vectorOnly, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: q, TopK: 3, DisableGraph: true})
	if err != nil {
		t.Fatal(err)
	}
	if containsString(knowledgeIDs(vectorOnly), "beacon-owner") {
		t.Fatalf("the vector stage alone found beacon-owner, so the store does not test the walk: %v", knowledgeIDs(vectorOnly))
	}
	if len(vectorOnly.GraphEntities) != 0 {
		t.Fatalf("disable_graph still walked: %v", vectorOnly.GraphEntities)
	}

	walked, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: q, TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	ids := knowledgeIDs(walked)
	if !containsString(ids, "beacon-owner") {
		t.Fatalf("the walk from Ember's page did not reach Beacon's owner: %v (%s)", ids, walked.Decision.Reason)
	}
	if ids[0] != "ember" {
		t.Fatalf("the passage both rankings agree on should lead, got %v", ids)
	}
	if !containsString(walked.GraphEntities, "Beacon") {
		t.Fatalf("graph entities = %v, want Beacon among them", walked.GraphEntities)
	}
	if !walked.Decision.UseGraph || !strings.Contains(walked.Decision.Reason, "walked the entity graph") {
		t.Fatalf("decision does not say it walked: %+v", walked.Decision)
	}
	if len(walked.Results) > 3 {
		t.Fatalf("the walk grew the list past top_k: %v", ids)
	}
}

func TestKnowledgeSearchWithoutEmbedderDoesNotWalkFromHits(t *testing.T) {
	ctx := context.Background()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "lexical.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "a", Content: "Ember push notifications go through Beacon.",
		Entities: []ToolEntityInput{{Name: "Ember"}, {Name: "Beacon"}}}); err != nil {
		t.Fatal(err)
	}
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "push notifications", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resp.Decision.Reason, "walked the entity graph from") && strings.Contains(resp.Decision.Reason, "the hits mention") {
		t.Fatalf("lexical search took the embedder path: %s", resp.Decision.Reason)
	}
}

func TestMemorySearchWalksTheGraphFromVectorHits(t *testing.T) {
	ctx := context.Background()
	embedder := newKeywordEmbedder("mobile", "push", "app", "late")
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "mem.db"))
	cfg.Dimensions = embedder.Dim()
	db, err := Open(cfg, WithEmbedder(embedder))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	save := func(id, content string, ents []ToolEntityInput) {
		t.Helper()
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Scope: MemoryScopeGlobal, Content: content, Entities: ents}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	save("inc-77", "INC-77: mobile app push was late for forty minutes; root cause CHG-1042.",
		[]ToolEntityInput{{Name: "INC-77"}, {Name: "CHG-1042"}})
	save("chg-1042", "CHG-1042 renamed the device token field in the event schema.",
		[]ToolEntityInput{{Name: "CHG-1042"}})
	save("style", "The mobile app style guide was updated.", nil)
	save("qa", "Mobile app QA moved to Thursdays.", nil)
	save("icons", "New app icons for the mobile release.", nil)

	const q = "why was mobile app push late"
	vectorOnly, err := db.SearchMemory(ctx, MemorySearchRequest{Query: q, Scope: MemoryScopeGlobal, TopK: 3, DisableGraph: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range vectorOnly.Results {
		if h.Memory.ID == "chg-1042" {
			t.Fatalf("the vector stage alone found the cause, so the store does not test the walk")
		}
	}
	walked, err := db.SearchMemory(ctx, MemorySearchRequest{Query: q, Scope: MemoryScopeGlobal, TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	var via []string
	for _, h := range walked.Results {
		ids = append(ids, h.Memory.ID)
		if h.Memory.ID == "chg-1042" {
			via = h.Via
		}
	}
	if !containsString(ids, "chg-1042") {
		t.Fatalf("the walk from INC-77 did not reach CHG-1042: %v (%s)", ids, walked.Decision.Reason)
	}
	if ids[0] != "inc-77" {
		t.Fatalf("the hit both rankings agree on should lead, got %v", ids)
	}
	if !containsString(via, "graph") {
		t.Fatalf("the walked memory does not say it came by the graph: via=%v", via)
	}
	if len(ids) > 3 {
		t.Fatalf("the walk grew the list past top_k: %v", ids)
	}
}

func TestRecallWalksOnceFromBothStores(t *testing.T) {
	db, ctx := vectorGraphStore(t)
	// The memory names Clara's successor and shares no word with the
	// question; only the knowledge hit's entities lead to it.
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "handover", Scope: MemoryScopeGlobal,
		Content:   "Clara Weiss handed Beacon over to Omar Haddad on 5 November.",
		Entities:  []ToolEntityInput{{Name: "Clara Weiss", Type: "Person"}, {Name: "Beacon", Type: "Product"}, {Name: "Omar Haddad", Type: "Person"}},
		Relations: []ToolRelationInput{{From: "Omar Haddad", Type: "owns", To: "Beacon"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "mobile-note", Scope: MemoryScopeGlobal, Content: "The mobile app team meets on Mondays."}); err != nil {
		t.Fatal(err)
	}
	const q = "who owns the mobile app push notifications"

	before, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: q, Scope: MemoryScopeGlobal, TopKMemories: 3, TopKKnowledge: 3, DisableGraph: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range before.Memories {
		if m.Memory.ID == "handover" {
			t.Fatalf("without the graph the handover was found anyway, so the store does not test the walk")
		}
	}
	rec, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: q, Scope: MemoryScopeGlobal, TopKMemories: 3, TopKKnowledge: 3})
	if err != nil {
		t.Fatal(err)
	}
	var mem []string
	for _, m := range rec.Memories {
		mem = append(mem, m.Memory.ID)
	}
	if !containsString(mem, "handover") {
		t.Fatalf("recall's walk did not cross from the knowledge hits to the memory: %v (%s)", mem, rec.MemoryDecision.Reason)
	}
	if !containsString(rec.GraphEntities, "Beacon") {
		t.Fatalf("graph entities = %v, want Beacon", rec.GraphEntities)
	}
	var kn []string
	for _, k := range rec.Knowledge {
		kn = append(kn, k.KnowledgeID)
	}
	if !containsString(kn, "beacon-owner") {
		t.Fatalf("recall's knowledge did not reach Beacon's page: %v", kn)
	}
}
