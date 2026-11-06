package graph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// livesIn is the rule an ontology hands the graph when it says a person lives
// in one place: an edge upsert of lives_in from a person closes the open one.
var livesIn = Supersession{
	SingleValued:  func(edgeType, subjectType string) bool { return edgeType == "lives_in" && subjectType == "person" },
	SchemaID:      "people",
	SchemaVersion: 3,
}

func explainWorld(t *testing.T, g *GraphStore) {
	t.Helper()
	ctx := context.Background()
	vec := []float32{0.1, 0.2, 0.3, 0.4}
	for _, n := range []*GraphNode{
		{ID: "leo", Vector: vec, NodeType: "person", Content: "Leo"},
		{ID: "beijing", Vector: vec, NodeType: "city", Content: "Beijing"},
		{ID: "chengdu", Vector: vec, NodeType: "city", Content: "Chengdu"},
	} {
		if err := g.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
}

func livesInEdge(id, to string) *GraphEdge {
	return &GraphEdge{ID: id, FromNodeID: "leo", ToNodeID: to, EdgeType: "lives_in", Weight: 1}
}

// relate writes one relation through the batch writer, the path that applies
// a Supersession rule (and the one the facade's relation upserts take).
func relate(t *testing.T, g *GraphStore, ctx context.Context, e *GraphEdge) {
	t.Helper()
	res, err := g.UpsertEdgesBatch(ctx, []*GraphEdge{e})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		t.Fatalf("relate %s: %v", e.ID, err)
	}
}

// explained reads every event after a cursor, explained, oldest first.
func explained(t *testing.T, g *GraphStore, after int64) *ChangesPage {
	t.Helper()
	page, err := g.ReadChanges(context.Background(), ChangesQuery{After: after, Explain: true})
	if err != nil {
		t.Fatalf("ReadChanges: %v", err)
	}
	return page
}

func edgeEvents(page *ChangesPage, id string) []ChangeEvent {
	var out []ChangeEvent
	for _, ev := range page.Events {
		if ev.Kind == ChangeKindEdge && ev.ID == id {
			out = append(out, ev)
		}
	}
	return out
}

func refString(r *ChangeRef) string {
	if r == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s(%s -%s-> %s)", r.ID, r.From, r.Type, r.To)
}

func TestAnExplainedSupersessionSaysWhatReplacedTheOldValueAndWhatTheNewOneReplaced(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			explainWorld(t, g)
			head, _ := g.ChangesHead(ctx)
			sctx := WithSupersession(ctx, livesIn)
			relate(t, g, sctx, livesInEdge("e-bj", "beijing"))
			relate(t, g, sctx, livesInEdge("e-cd", "chengdu"))

			page := explained(t, g, head)
			bj := edgeEvents(page, "e-bj")
			if len(bj) != 2 {
				t.Fatalf("events of e-bj: %v", summarize(bj))
			}
			if bj[0].Op != ChangeOpInsert || bj[0].Replaces != nil {
				t.Errorf("the first value replaced nothing: %s replaces %s", bj[0].Op, refString(bj[0].Replaces))
			}
			closed := bj[1]
			if got := refString(closed.SupersededBy); got != "e-cd(leo -lives_in-> chengdu)" {
				t.Errorf("the closed value is superseded by %s, want the new edge with its ends", got)
			}
			if closed.SchemaID != "people" || closed.SchemaVersion != 3 {
				t.Errorf("the closure was decided under %q v%d, want people v3", closed.SchemaID, closed.SchemaVersion)
			}

			cd := edgeEvents(page, "e-cd")
			if len(cd) != 1 || cd[0].Op != ChangeOpInsert {
				t.Fatalf("events of e-cd: %v", summarize(cd))
			}
			if got := refString(cd[0].Replaces); got != "e-bj(leo -lives_in-> beijing)" {
				t.Errorf("the new value replaces %s, want the old edge with its ends", got)
			}
			if cd[0].SchemaID != "people" || cd[0].SchemaVersion != 3 {
				t.Errorf("the insert carries %q v%d, want the schema of the closure it caused", cd[0].SchemaID, cd[0].SchemaVersion)
			}

			for id, want := range map[string]string{"leo": "Leo", "beijing": "Beijing", "chengdu": "Chengdu"} {
				if page.Labels[id] != want {
					t.Errorf("label of %s = %q, want %q (labels %v)", id, page.Labels[id], want, page.Labels)
				}
			}
		})
	}
}

// An edge id is not a version: Beijing, then Chengdu, then Beijing again
// inserts e-bj twice, and only the second insert closed anything.
func TestOnlyTheInsertThatClosedAValueSaysItReplacedIt(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			explainWorld(t, g)
			head, _ := g.ChangesHead(ctx)
			sctx := WithSupersession(ctx, livesIn)
			for _, e := range []*GraphEdge{livesInEdge("e-bj", "beijing"), livesInEdge("e-cd", "chengdu"), livesInEdge("e-bj", "beijing")} {
				// Distinct instants, so each version begins where the last ended
				// rather than all at one tick of the clock.
				time.Sleep(5 * time.Millisecond)
				relate(t, g, sctx, e)
			}

			var inserts []string
			for _, ev := range explained(t, g, head).Events {
				if ev.Kind == ChangeKindEdge && ev.Op == ChangeOpInsert {
					inserts = append(inserts, ev.ID+" replaces "+refString(ev.Replaces))
				}
			}
			want := []string{
				"e-bj replaces <nil>",
				"e-cd replaces e-bj(leo -lives_in-> beijing)",
				"e-bj replaces e-cd(leo -lives_in-> chengdu)",
			}
			if fmt.Sprint(inserts) != fmt.Sprint(want) {
				t.Fatalf("inserts:\n%s\nwant:\n%s", strings.Join(inserts, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

func TestAnExplainedMergeNamesTheSurvivorByItsContent(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			vec := []float32{0.1, 0.2, 0.3, 0.4}
			for _, n := range []*GraphNode{
				{ID: "canon", Vector: vec, NodeType: "Thing", Content: "Shanghai"},
				{ID: "alias", Vector: vec, NodeType: "Thing", Content: "SH"},
			} {
				if err := g.UpsertNode(ctx, n); err != nil {
					t.Fatal(err)
				}
			}
			head, _ := g.ChangesHead(ctx)
			if err := g.MergeEntities(ctx, "canon", []string{"alias"}); err != nil {
				t.Fatal(err)
			}
			var merged *ChangeEvent
			page := explained(t, g, head)
			for i, ev := range page.Events {
				if ev.Kind == ChangeKindNode && ev.ID == "alias" {
					merged = &page.Events[i]
				}
			}
			if merged == nil || merged.Op != ChangeOpMerge {
				t.Fatalf("no merge event for the alias: %v", summarize(page.Events))
			}
			if merged.SupersededBy == nil || merged.SupersededBy.ID != "canon" || merged.SupersededBy.Content != "Shanghai" {
				t.Errorf("the alias was merged into %+v, want canon named Shanghai", merged.SupersededBy)
			}
		})
	}
}

func TestARetractionIsExplainedWithoutAReplacementAndAGoneEndpointKeepsItsName(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			explainWorld(t, g)
			if err := g.UpsertEdge(ctx, livesInEdge("e-bj", "beijing")); err != nil {
				t.Fatal(err)
			}
			head, _ := g.ChangesHead(ctx)
			// Deleting the city retracts the relation with it.
			if err := g.DeleteNode(ctx, "beijing"); err != nil {
				t.Fatal(err)
			}
			page := explained(t, g, head)
			gone := edgeEvents(page, "e-bj")
			if len(gone) != 1 || gone[0].Op != ChangeOpDelete {
				t.Fatalf("events of e-bj: %v", summarize(page.Events))
			}
			if gone[0].SupersededBy != nil || gone[0].SchemaID != "" {
				t.Errorf("a retraction was given a replacement %s or a schema %q", refString(gone[0].SupersededBy), gone[0].SchemaID)
			}
			if page.Labels["beijing"] != "Beijing" {
				t.Errorf("the deleted endpoint's label is %q, want it read from its last version", page.Labels["beijing"])
			}
		})
	}
}

func TestAReadWithoutExplainLeavesTheEventsAsTheLogHasThem(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			explainWorld(t, g)
			head, _ := g.ChangesHead(ctx)
			sctx := WithSupersession(ctx, livesIn)
			relate(t, g, sctx, livesInEdge("e-bj", "beijing"))
			relate(t, g, sctx, livesInEdge("e-cd", "chengdu"))
			page, err := g.ReadChanges(ctx, ChangesQuery{After: head})
			if err != nil {
				t.Fatal(err)
			}
			if page.Labels != nil {
				t.Errorf("labels without explain: %v", page.Labels)
			}
			for _, ev := range page.Events {
				if ev.SupersededBy != nil || ev.Replaces != nil || ev.SchemaID != "" {
					t.Errorf("%s %s %s was explained without being asked", ev.Kind, ev.Op, ev.ID)
				}
			}
		})
	}
}

func TestABackwardReadPagesNewestFirstDownToItsLowerBound(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			vec := []float32{0.1, 0.2, 0.3, 0.4}
			start, _ := g.ChangesHead(ctx)
			for i := range 5 {
				if err := g.UpsertNode(ctx, &GraphNode{ID: fmt.Sprintf("n%d", i), Vector: vec, NodeType: "Thing"}); err != nil {
					t.Fatal(err)
				}
				if i > 0 {
					e := &GraphEdge{ID: fmt.Sprintf("e%d", i), FromNodeID: fmt.Sprintf("n%d", i-1), ToNodeID: fmt.Sprintf("n%d", i), EdgeType: "next", Weight: 1}
					if err := g.UpsertEdge(ctx, e); err != nil {
						t.Fatal(err)
					}
				}
			}
			// n0, n1, e1, n2, e2, n3, e3, n4, e4: nine events.
			all := allChanges(t, g, start)
			if len(all) != 9 {
				t.Fatalf("setup wrote %v", summarize(all))
			}

			var seen []string
			before := int64(-1)
			pages := 0
			for {
				page, err := g.ReadChanges(ctx, ChangesQuery{After: start, Before: before, Limit: 4})
				if err != nil {
					t.Fatal(err)
				}
				pages++
				for i := 1; i < len(page.Events); i++ {
					if page.Events[i].Seq >= page.Events[i-1].Seq {
						t.Fatalf("page %d is not newest first: %v", pages, summarize(page.Events))
					}
				}
				for _, ev := range page.Events {
					if ev.Seq <= start {
						t.Fatalf("seq %d read below the lower bound %d", ev.Seq, start)
					}
					seen = append(seen, ev.Kind+" "+ev.ID)
				}
				if !page.More {
					break
				}
				before = page.LastScanned
				if pages > 5 {
					t.Fatal("the backward read does not end")
				}
			}
			if pages != 3 || len(seen) != 9 {
				t.Fatalf("%d pages, %d events: %v", pages, len(seen), seen)
			}
			// Newest first, all of it, once each.
			for i, ev := range all {
				if got, want := seen[len(seen)-1-i], ev.Kind+" "+ev.ID; got != want {
					t.Fatalf("event %d from the end is %q, want %q", i, got, want)
				}
			}

			// A kind filter applies in the query: a page of edges is four edges.
			page, err := g.ReadChanges(ctx, ChangesQuery{After: start, Before: -1, Limit: 4, Kinds: []string{ChangeKindEdge}})
			if err != nil {
				t.Fatal(err)
			}
			if got := summarize(page.Events); fmt.Sprint(got) != "[edge insert e4 edge insert e3 edge insert e2 edge insert e1]" || page.More {
				t.Fatalf("edges newest first: %v (more %v)", got, page.More)
			}
		})
	}
}

func TestInstantsFromEitherBackendCompareByTheInstant(t *testing.T) {
	same := [][2]string{
		{"2026-11-06T04:44:21.457Z", "2026-11-06 04:44:21.457+00:00"},
		{"2026-11-06T12:44:21.457+08:00", "2026-11-06 04:44:21.457+00"},
		{"2026-11-06T04:44:21Z", "2026-11-06 04:44:21 +0000 UTC"},
		{"2026-11-06 04:44:21.5 +0000 UTC m=+12.345", "2026-11-06T04:44:21.5Z"},
		{"2026-11-06T04:44:21.000001", "2026-11-06 04:44:21.000001"},
	}
	for _, p := range same {
		if !sameInstant(p[0], p[1]) {
			t.Errorf("%q and %q are one instant", p[0], p[1])
		}
	}
	for _, p := range [][2]string{
		{"2026-11-06T04:44:21.457Z", "2026-11-06T04:44:21.458Z"},
		{"2026-11-06T04:44:21Z", "not a time"},
		{"", "2026-11-06T04:44:21Z"},
	} {
		if sameInstant(p[0], p[1]) {
			t.Errorf("%q and %q are not one instant", p[0], p[1])
		}
	}
}

func TestExplainReadsIdsInBoundedDistinctChunks(t *testing.T) {
	var ids []string
	for i := range 2*explainChunk + 50 {
		ids = append(ids, fmt.Sprintf("id-%04d", i), fmt.Sprintf("id-%04d", i)) // each twice
	}
	var sizes []int
	seen := map[string]bool{}
	err := forChunks(ids, func(chunk []string) error {
		sizes = append(sizes, len(chunk))
		for _, id := range chunk {
			if seen[id] {
				t.Errorf("%s read twice", id)
			}
			seen[id] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(sizes) != fmt.Sprint([]int{explainChunk, explainChunk, 50}) {
		t.Errorf("chunk sizes %v", sizes)
	}
	if len(seen) != 2*explainChunk+50 {
		t.Errorf("%d distinct ids read", len(seen))
	}
	stop := fmt.Errorf("stop")
	if err := forChunks(ids, func([]string) error { return stop }); err != stop {
		t.Errorf("a failing chunk's error is %v, want it returned", err)
	}
}

func TestALabelIsClippedOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("成都", labelClip)
	got := clipRunes(long, labelClip)
	if n := len([]rune(got)); n != labelClip+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("clipped to %d runes: %q", n, got)
	}
	if clipRunes("Leo", labelClip) != "Leo" {
		t.Error("a short label was changed")
	}
}

// An id closed twice has two versions in the history; each closing event is
// explained by the version it closed, not by whichever the history lists first.
func TestEachClosureOfAReusedIdIsExplainedByTheVersionItClosed(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			explainWorld(t, g)
			head, _ := g.ChangesHead(ctx)
			v4 := livesIn
			v4.SchemaVersion = 4
			for _, step := range []struct {
				rule Supersession
				e    *GraphEdge
			}{
				{livesIn, livesInEdge("e-bj", "beijing")},
				{livesIn, livesInEdge("e-cd", "chengdu")}, // closes e-bj under v3
				{livesIn, livesInEdge("e-bj", "beijing")}, // closes e-cd under v3
				{v4, livesInEdge("e-cd", "chengdu")},      // closes e-bj again, under v4
			} {
				time.Sleep(5 * time.Millisecond)
				relate(t, g, WithSupersession(ctx, step.rule), step.e)
			}
			var closures []string
			for _, ev := range edgeEvents(explained(t, g, head), "e-bj") {
				if len(ev.Before) > 0 {
					closures = append(closures, fmt.Sprintf("%s v%d by %s", ev.SchemaID, ev.SchemaVersion, refString(ev.SupersededBy)))
				}
			}
			want := []string{
				"people v3 by e-cd(leo -lives_in-> chengdu)",
				"people v4 by e-cd(leo -lives_in-> chengdu)",
			}
			if fmt.Sprint(closures) != fmt.Sprint(want) {
				t.Fatalf("closures of e-bj:\n%s\nwant:\n%s", strings.Join(closures, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
