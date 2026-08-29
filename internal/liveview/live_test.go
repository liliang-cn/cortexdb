package liveview

import (
	"encoding/json"
	"testing"
)

func snap(nodes []Node, edges []Edge) Snapshot {
	return Snapshot{Nodes: nodes, Edges: edges}
}

func TestDiffGraphReportsAddedNodesAndEdges(t *testing.T) {
	prev := snap(
		[]Node{{ID: "entity:a", Label: "A", Type: "entity"}},
		nil)
	next := snap(
		[]Node{
			{ID: "entity:a", Label: "A", Type: "entity"},
			{ID: "entity:b", Label: "B", Type: "entity"},
		},
		[]Edge{{Source: "entity:a", Target: "entity:b", Label: "knows"}})

	d := Diff(prev, next)
	if d.Empty() {
		t.Fatal("delta reported empty for an added node and edge")
	}
	if len(d.AddedNodes) != 1 || d.AddedNodes[0].ID != "entity:b" {
		t.Errorf("added nodes = %+v, want just entity:b", d.AddedNodes)
	}
	if len(d.AddedEdges) != 1 || d.AddedEdges[0].Label != "knows" {
		t.Errorf("added edges = %+v, want the knows edge", d.AddedEdges)
	}
	if len(d.RemovedNodes) != 0 || len(d.RemovedEdges) != 0 {
		t.Errorf("nothing was removed, got %v / %v", d.RemovedNodes, d.RemovedEdges)
	}
	if d.Nodes != 2 || d.Edges != 1 {
		t.Errorf("totals = %d nodes / %d edges, want 2 / 1", d.Nodes, d.Edges)
	}
}

func TestDiffGraphReportsRemovals(t *testing.T) {
	prev := snap(
		[]Node{{ID: "entity:a"}, {ID: "entity:b"}},
		[]Edge{{Source: "entity:a", Target: "entity:b", Label: "knows"}})
	next := snap([]Node{{ID: "entity:a"}}, nil)

	d := Diff(prev, next)
	if len(d.RemovedNodes) != 1 || d.RemovedNodes[0] != "entity:b" {
		t.Errorf("removed nodes = %v, want entity:b", d.RemovedNodes)
	}
	if len(d.RemovedEdges) != 1 {
		t.Errorf("removed edges = %+v, want the knows edge", d.RemovedEdges)
	}
}

// A node whose label or type changed must be re-sent, or the view keeps
// showing a name the brain no longer uses.
func TestDiffGraphResendsRelabelledNode(t *testing.T) {
	prev := snap([]Node{{ID: "entity:a", Label: "A", Type: "entity"}}, nil)
	next := snap([]Node{{ID: "entity:a", Label: "Alpha", Type: "concept"}}, nil)

	d := Diff(prev, next)
	if len(d.AddedNodes) != 1 || d.AddedNodes[0].Label != "Alpha" {
		t.Fatalf("added nodes = %+v, want the relabelled entity:a", d.AddedNodes)
	}
	if len(d.RemovedNodes) != 0 {
		t.Errorf("a relabelled node must not be reported as removed, got %v", d.RemovedNodes)
	}
}

func TestDiffGraphOfIdenticalSnapshotsIsEmpty(t *testing.T) {
	s := snap(
		[]Node{{ID: "entity:a", Label: "A"}},
		[]Edge{{Source: "entity:a", Target: "entity:a", Label: "self"}})
	if d := Diff(s, s); !d.Empty() {
		t.Errorf("identical snapshots produced a delta: %+v", d)
	}
}

// Two edges between the same pair differ only by their type; keying on the
// pair alone would hide one of them.
func TestDiffGraphDistinguishesParallelEdgesByType(t *testing.T) {
	prev := snap(nil, []Edge{{Source: "a", Target: "b", Label: "knows"}})
	next := snap(nil, []Edge{
		{Source: "a", Target: "b", Label: "knows"},
		{Source: "a", Target: "b", Label: "manages"},
	})
	d := Diff(prev, next)
	if len(d.AddedEdges) != 1 || d.AddedEdges[0].Label != "manages" {
		t.Errorf("added edges = %+v, want just the manages edge", d.AddedEdges)
	}
}

func TestClassifyToolCallKinds(t *testing.T) {
	for _, tc := range []struct {
		tool string
		want string
	}{
		{"knowledge_memory_recall", KindQuery},
		{"memory_search", KindQuery},
		{"search_text", KindQuery},
		{"find_nodes", KindQuery},
		{"expand_graph", KindQuery},
		{"memory_save", KindWrite},
		{"knowledge_save", KindWrite},
		{"upsert_entities", KindWrite},
		{"ingest_document", KindWrite},
		{"memory_delete", KindWrite},
		{"upsert_relations", KindRelate},
	} {
		ev, ok := ClassifyToolCall(tc.tool, nil, false)
		if !ok {
			t.Errorf("%s produced no event", tc.tool)
			continue
		}
		if ev.Kind != tc.want {
			t.Errorf("%s classified as %q, want %q", tc.tool, ev.Kind, tc.want)
		}
	}
}

// Rendering the view is not brain activity — it would make the ticker report
// itself every time someone opened the page.
func TestClassifyToolCallIgnoresViewTools(t *testing.T) {
	for _, name := range []string{"render_graph_html", "serve_graph_3d"} {
		if _, ok := ClassifyToolCall(name, nil, false); ok {
			t.Errorf("%s should not produce an activity event", name)
		}
	}
}

func TestClassifyToolCallLiftsQueryTerms(t *testing.T) {
	args := json.RawMessage(`{"query":"postgres backend","keywords":["pgvector"],"limit":5}`)
	ev, ok := ClassifyToolCall("knowledge_memory_recall", args, false)
	if !ok {
		t.Fatal("recall produced no event")
	}
	if !containsString(ev.Terms, "postgres backend") {
		t.Errorf("terms %v missing the query text", ev.Terms)
	}
	if !containsString(ev.Terms, "pgvector") {
		t.Errorf("terms %v missing the keyword", ev.Terms)
	}
	if ev.Text == "" {
		t.Error("event has no line for the ticker")
	}
}

// The whole point of calling a relation event out: the view must know which
// two nodes to draw the pulse between.
func TestClassifyToolCallLiftsRelationEndpoints(t *testing.T) {
	args := json.RawMessage(`{"relations":[{"from":"CortexDB","to":"pgvector","type":"uses"}]}`)
	ev, ok := ClassifyToolCall("upsert_relations", args, false)
	if !ok {
		t.Fatal("upsert_relations produced no event")
	}
	if len(ev.Links) != 1 || ev.Links[0][0] != "CortexDB" || ev.Links[0][1] != "pgvector" {
		t.Fatalf("links = %v, want one CortexDB->pgvector pair", ev.Links)
	}
	if !containsString(ev.Terms, "CortexDB") || !containsString(ev.Terms, "pgvector") {
		t.Errorf("terms %v should include both endpoints so they light up", ev.Terms)
	}
}

// A relation's type is the edge's label, not a node, so it must not light one.
func TestClassifyToolCallIgnoresRelationType(t *testing.T) {
	args := json.RawMessage(`{"relations":[{"from":"CortexDB","to":"pgvector","type":"uses"}]}`)
	ev, ok := ClassifyToolCall("upsert_relations", args, false)
	if !ok {
		t.Fatal("upsert_relations produced no event")
	}
	if containsString(ev.Terms, "uses") {
		t.Errorf("terms %v include the edge label, which is not a node", ev.Terms)
	}
}

func TestClassifyToolCallMarksFailure(t *testing.T) {
	ev, ok := ClassifyToolCall("memory_save", json.RawMessage(`{"content":"x"}`), true)
	if !ok {
		t.Fatal("failed save produced no event")
	}
	if !ev.Failed {
		t.Error("event not marked failed")
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
