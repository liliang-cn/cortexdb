package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A relation saved with a document gets the time its sentence states, and
// a caller's own time wins (#252).
func TestKnowledgeSaveWritesTheTimeARelationsSentenceStates(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "event.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	text := "Sal Khan worked at Oracle from 2005 until 2008. In 2009 Sal Khan founded Khan Academy. Sal Khan joined Connected Ventures."
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "khan", Title: "khan", Content: text,
		Entities: []ToolEntityInput{{Name: "Sal Khan"}, {Name: "Oracle"}, {Name: "Khan Academy"}, {Name: "Connected Ventures"}},
		Relations: []ToolRelationInput{
			{From: "Sal Khan", To: "Oracle", Type: "worked_at"},
			{From: "Sal Khan", To: "Khan Academy", Type: "founded"},
			{From: "Sal Khan", To: "Connected Ventures", Type: "worked_at", At: "2004-06"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"entity:oracle":             {"2005-01-01T00:00:00Z", "2008-01-01T00:00:00Z"},
		"entity:khan_academy":       {"2009-01-01T00:00:00Z", ""},
		"entity:connected_ventures": {"2004-06-01T00:00:00Z", "2004-07-01T00:00:00Z"},
	}
	edges, err := db.Graph().GetEdges(ctx, "entity:sal_khan", "out")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range edges {
		w, ok := want[e.ToNodeID]
		if !ok || isWeakPredicate(e.EdgeType) {
			continue
		}
		seen++
		from, _ := e.Properties[EventValidFromKey].(string)
		to, _ := e.Properties[EventValidToKey].(string)
		if from != w[0] || to != w[1] {
			t.Errorf("%s -%s-> %s: valid %q..%q, want %q..%q", e.FromNodeID, e.EdgeType, e.ToNodeID, from, to, w[0], w[1])
		}
	}
	if seen != len(want) {
		t.Fatalf("%d of %d relations found", seen, len(want))
	}

	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "bad", Content: "x y", Relations: []ToolRelationInput{{From: "A", To: "B", Type: "r", At: "last spring"}}}); err == nil {
		t.Fatal("an unreadable time was accepted")
	}
}

// Recall shows each fact's time, a subject's facts of one kind in the order
// they became true, and with as_of none the store learnt later (#252).
func TestRecallGraphFactsCarryTimeInOrder(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "order.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Ada"}, {Name: "Acme"}, {Name: "Initech"}, {Name: "Globex"}}}); err != nil {
		t.Fatal(err)
	}
	// Written out of time order.
	for _, r := range []ToolRelationInput{
		{From: "Ada", To: "Globex", Type: "worked_at", ValidFrom: "2019"},
		{From: "Ada", To: "Acme", Type: "worked_at", ValidFrom: "2010", ValidTo: "2014"},
		{From: "Ada", To: "Initech", Type: "worked_at", ValidFrom: "2014", ValidTo: "2019"},
	} {
		if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{r}}); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Where has Ada worked?", EntityNames: []string{"Ada"}})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, f := range resp.GraphFacts {
		if f.Predicate == "worked_at" {
			order = append(order, f.Object)
		}
	}
	if strings.Join(order, ",") != "Acme,Initech,Globex" {
		t.Fatalf("worked_at facts in order %v, want Acme,Initech,Globex", order)
	}
	if !strings.Contains(resp.ContextPack.Text, "Ada —worked_at→ Acme (2010 until 2014)") || !strings.Contains(resp.ContextPack.Text, "Ada —worked_at→ Globex (from 2019)") {
		t.Fatalf("the pack does not show the facts' times:\n%s", resp.ContextPack.Text)
	}

	// Recorded before now, so as_of a minute ago sees none of them.
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	before, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Where has Ada worked?", EntityNames: []string{"Ada"}, AsOf: past})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range before.GraphFacts {
		if f.Predicate == "worked_at" {
			t.Fatalf("as_of %s sees a fact recorded after it: %+v", past, f)
		}
	}
}

// Recall with as_of leaves out a document saved after it (#252).
func TestRecallAsOfLeavesOutLaterKnowledge(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "asof.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for _, id := range []string{"old", "new"} {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: "The quarterly zebrafish report for " + id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SQL().Exec(`UPDATE documents SET created_at = '2020-01-01 00:00:00' WHERE id = 'old'`); err != nil {
		t.Fatal(err)
	}
	resp, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "zebrafish report", AsOf: "2021-01-01T00:00:00Z", DisableMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, k := range resp.Knowledge {
		ids = append(ids, k.KnowledgeID)
	}
	if strings.Join(ids, ",") != "old" {
		t.Fatalf("as_of 2021 recalls %v, want only old", ids)
	}
	now, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "zebrafish report", DisableMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(now.Knowledge) != 2 {
		t.Fatalf("recall without as_of found %d documents, want 2", len(now.Knowledge))
	}
}
