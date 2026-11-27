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

	vectorOnly, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: q, TopK: 4, DisableGraph: true})
	if err != nil {
		t.Fatal(err)
	}
	if containsString(knowledgeIDs(vectorOnly), "beacon-owner") {
		t.Fatalf("the vector stage alone found beacon-owner, so the store does not test the walk: %v", knowledgeIDs(vectorOnly))
	}
	if len(vectorOnly.GraphEntities) != 0 {
		t.Fatalf("disable_graph still walked: %v", vectorOnly.GraphEntities)
	}

	walked, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: q, TopK: 4})
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
	if len(walked.Results) > 4 {
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

// incidentMemoryStore holds an incident whose root-cause change shares no
// word with the question; only the walk from the incident reaches it.
func incidentMemoryStore(t *testing.T, opts ...Option) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	embedder := newKeywordEmbedder("mobile", "push", "app", "late")
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "mem.db"))
	cfg.Dimensions = embedder.Dim()
	db, err := Open(cfg, append([]Option{WithEmbedder(embedder)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

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

	return db, ctx
}

func TestMemorySearchWalksTheGraphFromVectorHits(t *testing.T) {
	db, ctx := incidentMemoryStore(t)
	const q = "why was mobile app push late"
	vectorOnly, err := db.SearchMemory(ctx, MemorySearchRequest{Query: q, Scope: MemoryScopeGlobal, TopK: 4, DisableGraph: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range vectorOnly.Results {
		if h.Memory.ID == "chg-1042" {
			t.Fatalf("the vector stage alone found the cause, so the store does not test the walk")
		}
	}
	walked, err := db.SearchMemory(ctx, MemorySearchRequest{Query: q, Scope: MemoryScopeGlobal, TopK: 4})
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
	if len(ids) > 4 {
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
	// The walk's share of the knowledge ranking is a third of the vector
	// search's, so the page both rankings agree on leads.
	if len(rec.Knowledge) == 0 || rec.Knowledge[0].KnowledgeID != "ember" {
		t.Fatalf("recall's knowledge should lead with Ember's page: %v", rec.Knowledge)
	}
}

// A memory search made for a caller, as SuperLeo's over gRPC, is in the
// recall log with the walk on its record: where the walk started, the walk's
// own ranking and the fused list. A search CortexDB makes for itself is not.
func TestAMemorySearchForACallerIsLoggedWithItsWalk(t *testing.T) {
	db, ctx := incidentMemoryStore(t, WithRecallLog(RecallLogOptions{}))
	const q = "why was mobile app push late"
	if _, err := db.SearchMemory(ctx, MemorySearchRequest{Query: q, Scope: MemoryScopeGlobal, TopK: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SearchMemoryForCaller(ctx, "memory_search", MemorySearchRequest{Query: q, Scope: MemoryScopeGlobal, TopK: 4}); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListRecalls(ctx, RecallLogListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Recalls) != 1 || list.Recalls[0].Tool != "memory_search" {
		t.Fatalf("want the caller's search alone, logged as memory_search: %+v", list.Recalls)
	}
	rec, err := db.GetRecall(ctx, RecallLogGetRequest{RecallID: list.Recalls[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Explain == nil || rec.Explain.Memory == nil {
		t.Fatalf("no explain record for the memory side: %+v", rec.Explain)
	}
	side := rec.Explain.Memory
	var seeds []string
	for _, w := range side.WalkSeeds {
		seeds = append(seeds, w.Name)
		if w.Weight <= 0 {
			t.Fatalf("seed %s has no weight", w.Name)
		}
	}
	// CHG-1042 is mentioned by two memories; INC-77 by its own alone, with
	// no relation, so it does not seed the walk.
	if !containsString(seeds, "CHG-1042") || containsString(seeds, "INC-77") {
		t.Fatalf("walk seeds = %v, want CHG-1042 and not INC-77", seeds)
	}
	if len(side.WalkEntities) == 0 {
		t.Fatal("the entities the walk ranked highest are missing")
	}
	var walked []string
	for _, c := range side.Candidates[explainPPR] {
		walked = append(walked, c.ID)
	}
	if !containsString(walked, "chg-1042") {
		t.Fatalf("the walk's own ranking = %v, want chg-1042 in it", walked)
	}
	for i, c := range side.Candidates[explainPPR] {
		if c.Rank != i+1 {
			t.Fatalf("the walk's ranking holds another list too: %+v", side.Candidates[explainPPR])
		}
	}
	if len(side.Fused) == 0 || len(side.Candidates[explainVector]) == 0 {
		t.Fatalf("want the vector ranking and the fused list beside the walk's: %+v", side)
	}
}
