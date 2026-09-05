package graph

import (
	"context"
	"reflect"
	"testing"
)

// Which keys the records of one type carry, on how many of them, with how many
// distinct values.
//
// The other property queries here start from a key the caller already knows.
// This one starts from nothing, which is the only place to start when the
// question is what shape the stored records have — deriving a schema from data
// begins by asking what the data says about itself, and until now that could
// only be answered by a caller enumerating JSON keys in its own SQL, in one
// database's spelling.
//
// Both backends, for the reason the rest of this file gives twice over: the
// enumeration is a join written differently per dialect, and a join that
// silently produces no rows on one of them looks exactly like a store whose
// records carry no properties at all.

// seedTypedGraph writes records whose property keys differ by type, plus the
// three shapes that break a naive enumeration: a record with no properties, a
// record whose properties column holds something that is not a JSON object,
// and two records of one type sharing a value.
func seedTypedGraph(t *testing.T, g *GraphStore) {
	t.Helper()
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	nodes := []*GraphNode{
		{ID: "h1", Vector: []float32{1, 0, 0, 0}, NodeType: "host", Content: "dell",
			Properties: map[string]interface{}{"name": "dell", "arch": "x86_64"}},
		{ID: "h2", Vector: []float32{0, 1, 0, 0}, NodeType: "host", Content: "hp",
			Properties: map[string]interface{}{"name": "hp", "arch": "x86_64"}},
		// Same type, one key fewer: coverage is per key, not per type.
		{ID: "h3", Vector: []float32{0, 0, 1, 0}, NodeType: "host", Content: "mac",
			Properties: map[string]interface{}{"name": "mac"}},
		{ID: "c1", Vector: []float32{0, 0, 0, 1}, NodeType: "crate", Content: "serde",
			Properties: map[string]interface{}{"name": "serde"}},
		// Carries nothing. On a real store this is most of a legacy shelf, and
		// it must contribute no keys rather than an empty one.
		{ID: "c2", Vector: []float32{1, 1, 0, 0}, NodeType: "crate", Content: "tokio"},
	}
	for _, n := range nodes {
		if err := g.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}
}

// The primitive a schema deriver needs: per type, per key, how many records
// and how many distinct values. Distinctness is the half that matters — a key
// on every record with a different value on each is the only evidence data can
// offer about identity, and coverage alone cannot tell "name" from "arch".
func TestNodePropertyKeysCountRecordsAndDistinctValues(t *testing.T) {
	want := []PropertyKeyUsage{
		{NodeType: "crate", Key: "name", Records: 1, Distinct: 1},
		{NodeType: "host", Key: "arch", Records: 2, Distinct: 1},
		{NodeType: "host", Key: "name", Records: 3, Distinct: 3},
	}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			seedTypedGraph(t, b.store)
			got, err := b.store.NodePropertyKeys(context.Background())
			if err != nil {
				t.Fatalf("NodePropertyKeys: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("NodePropertyKeys =\n %+v\nwant\n %+v", got, want)
			}
		})
	}
}

// Narrowing works the way EdgeShapes' does — exactly as stored, no folding —
// because the type in the graph is whatever wrote it and this package nowhere
// decides what a near-miss means.
func TestNodePropertyKeysNarrowsToTheTypesAsked(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			seedTypedGraph(t, b.store)
			got, err := b.store.NodePropertyKeys(context.Background(), "crate")
			if err != nil {
				t.Fatalf("NodePropertyKeys: %v", err)
			}
			if len(got) != 1 || got[0].NodeType != "crate" {
				t.Fatalf("asking for crate returned %+v", got)
			}
			// A type nobody wrote is an empty answer, not an error: a deriver
			// asking about a declared type with no instances is asking a fair
			// question and the answer is "none".
			none, err := b.store.NodePropertyKeys(context.Background(), "nobody_writes_this")
			if err != nil {
				t.Fatalf("NodePropertyKeys: %v", err)
			}
			if len(none) != 0 {
				t.Errorf("a type nobody wrote returned %+v", none)
			}
		})
	}
}

// A properties column holding something that is not a JSON object takes the
// whole statement down on both databases unless the read is guarded, and the
// error names neither the column nor the row. One such record must cost its
// own keys and nothing else.
func TestOneUnparseableRecordDoesNotFailTheScan(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			seedTypedGraph(t, b.store)
			ctx := context.Background()
			// Written under the store's own SQL rather than through
			// UpsertNode, because UpsertNode marshals and so cannot produce
			// the row this is about.
			if _, err := b.store.exec(ctx,
				`UPDATE graph_nodes SET properties = ? WHERE id = ?`, `"not an object"`, "h1"); err != nil {
				t.Fatalf("write a malformed row: %v", err)
			}
			got, err := b.store.NodePropertyKeys(ctx, "host")
			if err != nil {
				t.Fatalf("NodePropertyKeys over a malformed row: %v", err)
			}
			// h1 contributed nothing; h2 and h3 still counted.
			want := []PropertyKeyUsage{
				{NodeType: "host", Key: "arch", Records: 1, Distinct: 1},
				{NodeType: "host", Key: "name", Records: 2, Distinct: 2},
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

// Untyped nodes are counted under the empty string, for the reason
// NodeTypeCounts gives: "there are 400 nodes nobody typed, and here is what
// they carry" is a finding, and dropping it turns a caller's sum into a
// discrepancy it has to explain.
func TestUntypedNodesKeepTheirKeys(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			seedTypedGraph(t, b.store)
			ctx := context.Background()
			if err := b.store.UpsertNode(ctx, &GraphNode{
				ID: "u1", Vector: []float32{0, 1, 1, 0}, Content: "nobody typed this",
				Properties: map[string]interface{}{"name": "orphan"},
			}); err != nil {
				t.Fatalf("UpsertNode: %v", err)
			}
			got, err := b.store.NodePropertyKeys(ctx, "")
			if err != nil {
				t.Fatalf("NodePropertyKeys: %v", err)
			}
			want := []PropertyKeyUsage{{NodeType: "", Key: "name", Records: 1, Distinct: 1}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}
