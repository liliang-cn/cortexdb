package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// leoMoves writes the history the live view's Changes page exists to explain:
// a person lives in one city, then in another, under a schema that makes
// lives_in single-valued (examples/21_history does the same).
func leoMoves(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	str := OntologyDataType{Kind: OntologyDataString}
	people := OntologySchema{
		SchemaID: "people", Name: "People",
		ObjectTypes: []OntologyObjectType{
			{APIName: "Person", PrimaryKey: "personId", Properties: []OntologyProperty{{APIName: "personId", DataType: str, Required: true}}},
			{APIName: "City", PrimaryKey: "cityId", Properties: []OntologyProperty{{APIName: "cityId", DataType: str, Required: true}}},
		},
		LinkTypes: []OntologyLinkType{{
			APIName: "lives_in",
			A:       OntologyLinkSide{APIName: "city", ObjectTypeAPIName: "Person", Cardinality: OntologyCardinalityOne},
			B:       OntologyLinkSide{APIName: "residents", ObjectTypeAPIName: "City", Cardinality: OntologyCardinalityMany},
		}},
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: people, Activate: true}); err != nil {
		t.Fatalf("save schema: %v", err)
	}
	tools := db.GraphRAGTools()
	entity := func(name, typ, pk string) ToolEntityInput {
		return ToolEntityInput{Name: name, Type: typ, Metadata: map[string]string{pk: name}}
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		entity("Leo", "Person", "personId"), entity("Beijing", "City", "cityId"), entity("Chengdu", "City", "cityId"),
	}}); err != nil {
		t.Fatalf("entities: %v", err)
	}
	relate := func(to string) {
		if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
			Relations: []ToolRelationInput{{From: "Leo", To: to, Type: "lives_in"}},
		}); err != nil {
			t.Fatalf("relate %s: %v", to, err)
		}
	}
	relate("Beijing")
	time.Sleep(20 * time.Millisecond)
	relate("Chengdu")
}

func eventByOp(t *testing.T, events []graph.ChangeEvent, kind, op string) graph.ChangeEvent {
	t.Helper()
	for _, ev := range events {
		if ev.Kind == kind && ev.Op == op {
			return ev
		}
	}
	t.Fatalf("no %s %s event among %d", kind, op, len(events))
	return graph.ChangeEvent{}
}

// Explain answers, in a fixed number of queries per page, what the closed
// edge was replaced by and under which schema version, and what the new edge
// replaced, with the labels of the nodes named.
func TestChangesSinceExplainsASupersededEdgeAndTheEdgeThatReplacedIt(t *testing.T) {
	for _, b := range changeBrains(t) {
		t.Run(b.name, func(t *testing.T) {
			leoMoves(t, b.db)
			resp := callChangesSince(t, b.db, ChangesSinceRequest{After: 0, Limit: 1000, Explain: true, Kinds: []string{graph.ChangeKindEdge}})

			closed := eventByOp(t, resp.Events, graph.ChangeKindEdge, graph.ChangeOpDelete)
			if closed.Reason != graph.ReasonSuperseded {
				t.Fatalf("reason = %q", closed.Reason)
			}
			if closed.SupersededBy == nil || closed.SupersededBy.To != "entity:city:chengdu" || closed.SupersededBy.Type != "lives_in" {
				t.Errorf("superseded_by = %+v, want the Chengdu edge", closed.SupersededBy)
			}
			if closed.SchemaID != "people" || closed.SchemaVersion != 1 {
				t.Errorf("schema = %s v%d, want people v1", closed.SchemaID, closed.SchemaVersion)
			}

			var added *graph.ChangeEvent
			for i, ev := range resp.Events {
				if ev.Op == graph.ChangeOpInsert && ev.Replaces != nil {
					added = &resp.Events[i]
				}
			}
			if added == nil || added.Replaces.To != "entity:city:beijing" || added.SchemaID != "people" {
				t.Fatalf("the Chengdu edge does not name the Beijing edge it closed: %+v", added)
			}
			if first := resp.Events[0]; first.Replaces != nil || first.SupersededBy != nil {
				t.Errorf("the first edge replaced nothing: %+v", first)
			}
			for id, want := range map[string]string{"entity:person:leo": "Leo", "entity:city:beijing": "Beijing", "entity:city:chengdu": "Chengdu"} {
				if resp.Labels[id] != want {
					t.Errorf("label of %s = %q, want %q", id, resp.Labels[id], want)
				}
			}

			// Without Explain nothing is looked up.
			plain := callChangesSince(t, b.db, ChangesSinceRequest{After: 0, Limit: 1000})
			for _, ev := range plain.Events {
				if ev.SupersededBy != nil || ev.Replaces != nil || ev.SchemaID != "" || plain.Labels != nil {
					t.Fatalf("an unexplained page carries explanations: %+v", ev)
				}
			}
		})
	}
}

// The explained fields survive the tool's wire form, which is how a shared
// brain's view receives them, and the tool's output schema accepts them.
func TestExplainedEventsSurviveTheToolsWireForm(t *testing.T) {
	for _, b := range changeBrains(t) {
		t.Run(b.name, func(t *testing.T) {
			leoMoves(t, b.db)
			resp, err := b.db.ChangesSince(context.Background(), ChangesSinceRequest{Before: -1, Limit: 100, Explain: true})
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(newChangesSinceMCPResponse(resp))
			if err != nil {
				t.Fatal(err)
			}
			var back ChangesSinceResponse
			if err := json.Unmarshal(wire, &back); err != nil {
				t.Fatal(err)
			}
			closed := eventByOp(t, back.Events, graph.ChangeKindEdge, graph.ChangeOpDelete)
			if closed.SupersededBy == nil || closed.SchemaVersion != 1 || back.Labels["entity:person:leo"] != "Leo" {
				t.Errorf("lost on the wire: %+v labels %v", closed, back.Labels)
			}

			schema, err := jsonschema.For[changesSinceMCPResponse](nil)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			var instance map[string]any
			if err := json.Unmarshal(wire, &instance); err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(instance); err != nil {
				t.Fatalf("output schema rejects an explained page: %v\n%s", err, wire)
			}
		})
	}
}

func TestChangesSinceReadsBackwardsNewestFirstInPagesThatMeet(t *testing.T) {
	for _, b := range changeBrains(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			leoMoves(t, b.db)
			if _, err := b.db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m1", UserID: "u1", Content: "prefers tea"}); err != nil {
				t.Fatal(err)
			}
			forward := callChangesSince(t, b.db, ChangesSinceRequest{After: 0, Limit: 1000})

			var back []graph.ChangeEvent
			req := ChangesSinceRequest{Before: -1, Limit: 3}
			for pages := 0; ; pages++ {
				if pages > len(forward.Events) {
					t.Fatal("backward paging never ended")
				}
				page := callChangesSince(t, b.db, req)
				if len(page.Events) > 3 {
					t.Fatalf("a page of %d, limit 3", len(page.Events))
				}
				back = append(back, page.Events...)
				if !page.More {
					break
				}
				if page.NextCursor != page.Events[len(page.Events)-1].Seq {
					t.Fatalf("next_cursor %d is not the oldest seq of the page", page.NextCursor)
				}
				req.Before = page.NextCursor
			}
			if len(back) != len(forward.Events) {
				t.Fatalf("backward read %d events, forward %d", len(back), len(forward.Events))
			}
			for i, ev := range back {
				if want := forward.Events[len(forward.Events)-1-i]; ev.Seq != want.Seq || ev.Op != want.Op {
					t.Fatalf("backward event %d is seq %d %s, want seq %d %s", i, ev.Seq, ev.Op, want.Seq, want.Op)
				}
			}

			// Kinds are applied in the query: a page of the newest edges is
			// full of edges even when other kinds are newer.
			edges := callChangesSince(t, b.db, ChangesSinceRequest{Before: -1, Limit: 2, Kinds: []string{graph.ChangeKindEdge}})
			if len(edges.Events) != 2 || edges.Events[0].Kind != graph.ChangeKindEdge || edges.Events[1].Kind != graph.ChangeKindEdge || !edges.More {
				t.Errorf("edges: %d events, more %v", len(edges.Events), edges.More)
			}

			// After is a lower bound.
			cut := forward.Events[len(forward.Events)-3].Seq
			newer := callChangesSince(t, b.db, ChangesSinceRequest{Before: -1, After: cut, Limit: 10})
			if fmt.Sprint(seqs(newer.Events)) != fmt.Sprint([]int64{forward.Events[len(forward.Events)-1].Seq, forward.Events[len(forward.Events)-2].Seq}) || newer.More {
				t.Errorf("after %d: %v more %v", cut, seqs(newer.Events), newer.More)
			}

			// Before below everything: nothing, and nothing more.
			none := callChangesSince(t, b.db, ChangesSinceRequest{Before: 1, Limit: 10})
			if len(none.Events) != 0 || none.More {
				t.Errorf("before 1: %+v", none)
			}
		})
	}
}

func seqs(events []graph.ChangeEvent) []int64 {
	out := make([]int64, len(events))
	for i, ev := range events {
		out[i] = ev.Seq
	}
	return out
}

// Reading the past is not a cursor that can fall behind: what retention
// pruned is not listed, and the page says how far it pruned.
func TestChangesSinceBackwardsListsWhatRetentionKeptAndSaysWhereItPruned(t *testing.T) {
	for _, b := range changeBrains(t, WithChangeFeed(ChangeFeedOptions{Retention: graph.ChangeFeedRetention{MaxRows: 2}})) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			for i := 0; i < 5; i++ {
				if _, err := b.db.SaveMemory(ctx, MemorySaveRequest{MemoryID: fmt.Sprintf("m%d", i), UserID: "u", Content: "x"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := b.db.PruneChanges(ctx); err != nil {
				t.Fatal(err)
			}
			resp := callChangesSince(t, b.db, ChangesSinceRequest{Before: -1, Limit: 10})
			if resp.Pruned || len(resp.Events) != 2 || resp.PrunedThrough == 0 || resp.Events[1].Seq <= resp.PrunedThrough {
				t.Errorf("pruned %v, %d events, pruned through %d", resp.Pruned, len(resp.Events), resp.PrunedThrough)
			}
		})
	}
}

// The live view drops a memory update when only these metadata keys moved.
// This holds the list to what a recall writes, so a new counter added to the
// writer without the list is a failure here rather than a flood of "memory
// updated" lines on the Changes page.
func TestWhatARecallWritesIsExactlyTheBookkeepingKeys(t *testing.T) {
	for _, b := range changeBrains(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := b.db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m1", UserID: "u1", Content: "prefers light UI"}); err != nil {
				t.Fatal(err)
			}
			if _, err := b.db.SearchMemory(ctx, MemorySearchRequest{UserID: "u1", Query: "light UI"}); err != nil {
				t.Fatal(err)
			}
			resp := callChangesSince(t, b.db, ChangesSinceRequest{After: 0, Limit: 100, Kinds: []string{graph.ChangeKindMemory}})
			update := eventByOp(t, resp.Events, graph.ChangeKindMemory, graph.ChangeOpUpdate)
			var before, after struct {
				Metadata map[string]any `json:"metadata"`
			}
			if err := json.Unmarshal(update.Before, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(update.After, &after); err != nil {
				t.Fatal(err)
			}
			bookkeeping := map[string]bool{}
			for _, k := range MemoryBookkeepingKeys {
				bookkeeping[k] = true
			}
			for k, v := range after.Metadata {
				if before.Metadata[k] != v && !bookkeeping[k] {
					t.Errorf("a recall changed metadata %q, which is not in MemoryBookkeepingKeys", k)
				}
			}
			for k := range bookkeeping {
				if _, ok := after.Metadata[k]; !ok {
					t.Errorf("a recall no longer writes %q", k)
				}
			}
		})
	}
}

var _ = time.Second
