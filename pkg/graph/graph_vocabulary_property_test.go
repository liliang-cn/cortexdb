package graph

import (
	"context"
	"reflect"
	"testing"
)

// Grouping and listing by what a writer stamped on a record, rather than by
// type. These run on both backends for the reason the type-count tests give:
// the guarded JSON read is spelled differently per dialect, and a filter that
// silently matches nothing on one of them looks exactly like an empty graph.

// seedStampedGraph writes one of each case the property queries have to get
// right: two records sharing a value, one carrying a different value, one
// carrying the key with an empty value, and one never stamped at all. Edges as
// well as nodes, because a shelf's assertions are mostly edges and the API
// that only saw nodes is the one this replaces.
func seedStampedGraph(t *testing.T, g *GraphStore) {
	t.Helper()
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	nodes := []*GraphNode{
		{ID: "a", Vector: []float32{1, 0, 0, 0}, NodeType: "doc", Content: "held one",
			Properties: map[string]interface{}{"_grade": "held", "_why": "a person must look"}},
		{ID: "b", Vector: []float32{0, 1, 0, 0}, NodeType: "doc", Content: "held two",
			Properties: map[string]interface{}{"_grade": "held", "_why": "likewise"}},
		{ID: "c", Vector: []float32{0, 0, 1, 0}, NodeType: "doc", Content: "verified",
			Properties: map[string]interface{}{"_grade": "verified"}},
		// Carries the key, says nothing with it. Different from not carrying
		// it, and the store has to keep them apart.
		{ID: "d", Vector: []float32{0, 0, 0, 1}, NodeType: "doc", Content: "blank grade",
			Properties: map[string]interface{}{"_grade": ""}},
		// Never stamped. On a real shelf this is the biggest group.
		{ID: "e", Vector: []float32{1, 1, 0, 0}, NodeType: "doc", Content: "nobody stamped this"},
	}
	for _, n := range nodes {
		if err := g.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}

	edges := []*GraphEdge{
		{ID: "e1", FromNodeID: "a", ToNodeID: "c", EdgeType: "cites", Weight: 1,
			Properties: map[string]interface{}{"_grade": "held", "_why": "the edge is doubted too"}},
		{ID: "e2", FromNodeID: "b", ToNodeID: "c", EdgeType: "cites", Weight: 1,
			Properties: map[string]interface{}{"_grade": "refused", "_why": "the ontology would not have it"}},
		{ID: "e3", FromNodeID: "c", ToNodeID: "d", EdgeType: "cites", Weight: 1},
	}
	for _, e := range edges {
		if err := g.UpsertEdge(ctx, e); err != nil {
			t.Fatalf("UpsertEdge %s: %v", e.ID, err)
		}
	}
}

// The count nobody asks for and everybody needs: how much of the graph carries
// no such property at all. A breakdown drawn without it describes a fraction of
// the store while looking like all of it.
func TestPropertyCountsReportsTheUnstampedToo(t *testing.T) {
	want := map[string]PropertyCount{
		"held":     {Nodes: 2, Edges: 1},
		"verified": {Nodes: 1, Edges: 0},
		"refused":  {Nodes: 0, Edges: 1},
		// The blank-valued node and the never-stamped node land together, which
		// is the right answer to "what does this record say its grade is":
		// neither says one.
		"": {Nodes: 2, Edges: 1},
	}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			seedStampedGraph(t, b.store)
			got, err := b.store.PropertyCounts(context.Background(), "_grade")
			if err != nil {
				t.Fatalf("PropertyCounts: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("PropertyCounts = %v, want %v", got, want)
			}
		})
	}
}

// A property nothing carries is not an error and not an empty map: every
// record is unstamped, and saying so is the answer.
func TestPropertyCountsOnAKeyNobodyWrote(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			seedStampedGraph(t, b.store)
			got, err := b.store.PropertyCounts(context.Background(), "_nobody_writes_this")
			if err != nil {
				t.Fatalf("PropertyCounts: %v", err)
			}
			want := map[string]PropertyCount{"": {Nodes: 5, Edges: 3}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("PropertyCounts = %v, want %v", got, want)
			}
		})
	}
}

// Values within one key are ORed, and the result spans both tables. A reader
// asking "held or refused" wants one list.
func TestRecordsWithPropertiesSpansNodesAndEdges(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			got, err := seedAndQuery(t, b.store, PropertyRecordQuery{
				Where: map[string][]string{"_grade": {"held", "refused"}},
				Fetch: []string{"_grade", "_why"},
			})
			if err != nil {
				t.Fatalf("RecordsWithProperties: %v", err)
			}
			ids := map[string]bool{}
			var edges int
			for _, r := range got {
				ids[r.ID] = true
				if r.Edge {
					edges++
					if r.From == "" || r.To == "" {
						t.Errorf("edge %s came back without its ends: %+v", r.ID, r)
					}
					if r.Content != "" {
						t.Errorf("edge %s has content %q; edges are named by type and ends", r.ID, r.Content)
					}
				}
			}
			for _, want := range []string{"a", "b", "e1", "e2"} {
				if !ids[want] {
					t.Errorf("%s missing from %v", want, ids)
				}
			}
			if len(got) != 4 {
				t.Errorf("got %d records, want 4: %+v", len(got), got)
			}
			if edges != 2 {
				t.Errorf("got %d edges, want 2 — a node-only answer hides most of a graph's assertions", edges)
			}
		})
	}
}

// Every record here was asked for by grade, so every one has to come back with
// the reason its writer gave. A held record with no why is the failure the
// contract exists to prevent, and a reader must be able to see it.
func TestFetchedPropertiesComeBackAndMissingOnesStayMissing(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			got, err := seedAndQuery(t, b.store, PropertyRecordQuery{
				Where: map[string][]string{"_grade": {"held", "verified"}},
				Fetch: []string{"_grade", "_why"},
			})
			if err != nil {
				t.Fatalf("RecordsWithProperties: %v", err)
			}
			for _, r := range got {
				switch r.ID {
				case "a":
					if r.Properties["_why"] != "a person must look" {
						t.Errorf("a: _why = %q", r.Properties["_why"])
					}
				case "c":
					// Verified carries no why, and absent must stay absent —
					// an empty string here would read as "the reason is blank".
					if _, ok := r.Properties["_why"]; ok {
						t.Errorf("c: _why present as %q; it was never written", r.Properties["_why"])
					}
					if r.Properties["_grade"] != "verified" {
						t.Errorf("c: _grade = %q", r.Properties["_grade"])
					}
				}
			}
		})
	}
}

// The empty string asks for the records nothing was stamped on, and it has to
// find the never-stamped ones as well as the blank-valued ones — the same two
// PropertyCounts reports together.
func TestTheEmptyValueFindsWhatWasNeverStamped(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			got, err := seedAndQuery(t, b.store, PropertyRecordQuery{
				Where: map[string][]string{"_grade": {""}},
			})
			if err != nil {
				t.Fatalf("RecordsWithProperties: %v", err)
			}
			ids := map[string]bool{}
			for _, r := range got {
				ids[r.ID] = true
			}
			for _, want := range []string{"d", "e", "e3"} {
				if !ids[want] {
					t.Errorf("%s missing from %v", want, ids)
				}
			}
			if len(got) != 3 {
				t.Errorf("got %d, want 3: %+v", len(got), got)
			}
		})
	}
}

// An unfiltered read of both tables is never what a caller of this API meant,
// and returning the graph makes the mistake look like it worked.
func TestAnUnfilteredQueryIsRefused(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			seedStampedGraph(t, b.store)
			if _, err := b.store.RecordsWithProperties(context.Background(), PropertyRecordQuery{}); err == nil {
				t.Fatal("an empty query returned the whole graph")
			}
			if _, err := b.store.PropertyCounts(context.Background(), ""); err == nil {
				t.Fatal("counting by no key at all was accepted")
			}
		})
	}
}

// Limit applies to the merged result, not to each table: a cap that filled up
// on nodes because they sort first would hide every matching edge.
func TestLimitDoesNotHideOneTableBehindTheOther(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			got, err := seedAndQuery(t, b.store, PropertyRecordQuery{
				Where: map[string][]string{"_grade": {"held", "refused"}},
				Limit: 3,
			})
			if err != nil {
				t.Fatalf("RecordsWithProperties: %v", err)
			}
			if len(got) != 3 {
				t.Fatalf("got %d records, want 3", len(got))
			}
			var edges int
			for _, r := range got {
				if r.Edge {
					edges++
				}
			}
			if edges == 0 {
				t.Error("a capped answer returned no edges at all — the cap ate one whole table")
			}
		})
	}
}

func seedAndQuery(t *testing.T, g *GraphStore, q PropertyRecordQuery) ([]PropertyRecord, error) {
	t.Helper()
	seedStampedGraph(t, g)
	return g.RecordsWithProperties(context.Background(), q)
}
