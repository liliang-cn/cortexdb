package graph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/internal/pgtest"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// Point-in-time reads, on both backends.
//
// Every test here runs through `backends(t)`, the parity harness, because the
// whole feature is timestamp comparison and the two databases store and compare
// timestamps differently — SQLite as text in Go's layout, PostgreSQL as a real
// timestamp truncated to microseconds. A suite that only ran on SQLite would
// prove nothing about the deployment that matters.

func vec() []float32 { return []float32{1, 0, 0, 0} }

func mustNode(t *testing.T, g *GraphStore, ctx context.Context, id, content string) {
	t.Helper()
	if err := g.UpsertNode(ctx, &GraphNode{ID: id, Vector: vec(), Content: content, NodeType: "host"}); err != nil {
		t.Fatalf("UpsertNode %s: %v", id, err)
	}
}

func mustEdge(t *testing.T, g *GraphStore, ctx context.Context, id, from, to, edgeType string, props map[string]any) {
	t.Helper()
	if err := g.UpsertEdge(ctx, &GraphEdge{
		ID: id, FromNodeID: from, ToNodeID: to, EdgeType: edgeType, Weight: 1, Properties: props,
	}); err != nil {
		t.Fatalf("UpsertEdge %s: %v", id, err)
	}
}

// A brain written before this release must read exactly as it did.
//
// The migration is the part of a storage change that is only exercised once, on
// somebody else's data, in production. So the old DDL is written out here by
// hand — no temporal columns, no history tables — rows are inserted through it,
// and only then is the current schema created over the top.
func TestABrainFromBeforeThisReleaseReadsIdentically(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			blob := b.store.dialect.BlobType()

			// The 2.98 shape, verbatim in the columns that matter.
			if _, err := b.store.db.ExecContext(ctx, fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS graph_nodes (
					id TEXT PRIMARY KEY,
					vector %[1]s NOT NULL,
					content TEXT,
					node_type TEXT,
					properties TEXT,
					created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
					updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
				);
				CREATE TABLE IF NOT EXISTS graph_edges (
					id TEXT PRIMARY KEY,
					from_node_id TEXT NOT NULL,
					to_node_id TEXT NOT NULL,
					edge_type TEXT,
					weight REAL DEFAULT 1.0,
					properties TEXT,
					vector %[1]s,
					created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (from_node_id) REFERENCES graph_nodes(id) ON DELETE CASCADE,
					FOREIGN KEY (to_node_id) REFERENCES graph_nodes(id) ON DELETE CASCADE
				);`, blob)); err != nil {
				t.Fatalf("old-shape DDL: %v", err)
			}

			enc := func(id, content string) {
				t.Helper()
				// Written with raw SQL, the way the old code would have: no
				// valid_from, no recorded_at, because those columns do not
				// exist yet.
				if _, err := b.store.exec(ctx,
					`INSERT INTO graph_nodes (id, vector, content, node_type) VALUES (?, ?, ?, ?)`,
					id, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, content, "legacy"); err != nil {
					t.Fatalf("legacy insert %s: %v", id, err)
				}
			}
			enc("old:a", "written before the upgrade")
			enc("old:b", "also written before the upgrade")
			if _, err := b.store.exec(ctx,
				`INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, weight) VALUES (?, ?, ?, ?, ?)`,
				"old:e", "old:a", "old:b", "knows", 1.0); err != nil {
				t.Fatalf("legacy edge: %v", err)
			}

			// The migration.
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("migration: %v", err)
			}

			node, err := b.store.GetNode(ctx, "old:a")
			if err != nil {
				t.Fatalf("GetNode after migration: %v", err)
			}
			if node.Content != "written before the upgrade" {
				t.Errorf("content = %q after migration", node.Content)
			}
			if !node.ValidFrom.IsZero() || !node.RecordedAt.IsZero() {
				t.Errorf("a migrated row claims a validity it does not have: from=%v recorded=%v",
					node.ValidFrom, node.RecordedAt)
			}

			edges, err := b.store.GetEdges(ctx, "old:a", "both")
			if err != nil || len(edges) != 1 {
				t.Fatalf("GetEdges after migration: %d edges, %v", len(edges), err)
			}

			// And the rows are current at every instant, because NULL is
			// unbounded: a migrated brain read as of last year must not look
			// empty.
			past := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
			if _, err := b.store.GetNode(AsOf(ctx, past), "old:a"); err != nil {
				t.Errorf("as-of read of a migrated row: %v", err)
			}
			if got, err := b.store.CountNodes(AsOf(ctx, past), nil); err != nil || got != 2 {
				t.Errorf("CountNodes as of %s = %d (%v), want 2", past, got, err)
			}
		})
	}
}

// Retraction is the whole point: a deleted fact has to remain readable as it
// was, and everything that reads the present has to stop seeing it.
func TestRetractedEdgeIsGoneNowAndThereBefore(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "sds-meta", "the metadata host")
			mustNode(t, b.store, ctx, "sds-a", "the first node")
			mustEdge(t, b.store, ctx, "e:failover", "sds-meta", "sds-a", "fails_over_to",
				map[string]any{"document_id": "runbook-7", "chunk_ids": []string{"c1"}})

			before := b.store.Now()

			if err := b.store.DeleteEdge(ctx, "e:failover"); err != nil {
				t.Fatalf("DeleteEdge: %v", err)
			}

			// Now: gone.
			edges, err := b.store.GetEdges(ctx, "sds-meta", "both")
			if err != nil {
				t.Fatalf("GetEdges: %v", err)
			}
			if len(edges) != 0 {
				t.Fatalf("a retracted edge is still in the current graph: %d edges", len(edges))
			}

			// Then: there, with its provenance intact.
			past, err := b.store.GetEdges(AsOf(ctx, before), "sds-meta", "both")
			if err != nil {
				t.Fatalf("GetEdges as-of: %v", err)
			}
			if len(past) != 1 {
				t.Fatalf("as of %s the edge should still be there; got %d", before, len(past))
			}
			if past[0].EdgeType != "fails_over_to" {
				t.Errorf("edge type = %q as-of", past[0].EdgeType)
			}
			if past[0].Properties["document_id"] != "runbook-7" {
				t.Errorf("the retracted edge lost its provenance: %v", past[0].Properties)
			}
			if past[0].RetractedAt.IsZero() {
				t.Error("the archived row does not say when it was retracted")
			}

			// And after the retraction it is gone from an as-of read too.
			if got, err := b.store.GetEdges(AsOf(ctx, b.store.Now()), "sds-meta", "both"); err != nil || len(got) != 0 {
				t.Errorf("as of after the retraction: %d edges (%v), want 0", len(got), err)
			}
		})
	}
}

// A retracted node takes its edges with it, and both come back as-of.
func TestRetractedNodeTakesItsEdgesAndBothReadAsOf(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "n:doomed", "about to go")
			mustNode(t, b.store, ctx, "n:survivor", "stays")
			mustEdge(t, b.store, ctx, "e:touches", "n:doomed", "n:survivor", "mentions", nil)

			before := b.store.Now()
			if err := b.store.DeleteNode(ctx, "n:doomed"); err != nil {
				t.Fatalf("DeleteNode: %v", err)
			}

			if _, err := b.store.GetNode(ctx, "n:doomed"); err == nil {
				t.Fatal("a retracted node is still in the current graph")
			}
			if edges, err := b.store.GetEdges(ctx, "n:survivor", "both"); err != nil || len(edges) != 0 {
				t.Fatalf("the retracted node left %d edges behind (%v)", len(edges), err)
			}

			node, err := b.store.GetNode(AsOf(ctx, before), "n:doomed")
			if err != nil {
				t.Fatalf("GetNode as-of a retracted node: %v", err)
			}
			if node.Content != "about to go" {
				t.Errorf("content = %q as-of", node.Content)
			}
			if edges, err := b.store.GetEdges(AsOf(ctx, before), "n:survivor", "both"); err != nil || len(edges) != 1 {
				t.Errorf("the edge of a retracted node is not readable as-of: %d (%v)", len(edges), err)
			}

			// The traversal that reads through GetEdges follows: as-of is the
			// epoch of the whole walk, not of one query.
			neighbours, err := b.store.Neighbors(AsOf(ctx, before), "n:survivor", TraversalOptions{MaxDepth: 1})
			if err != nil {
				t.Fatalf("Neighbors as-of: %v", err)
			}
			if len(neighbours) != 1 || neighbours[0].ID != "n:doomed" {
				t.Errorf("neighbours as-of = %v, want [n:doomed]", neighbours)
			}
			if now, err := b.store.Neighbors(ctx, "n:survivor", TraversalOptions{MaxDepth: 1}); err != nil || len(now) != 0 {
				t.Errorf("neighbours now = %d (%v), want 0", len(now), err)
			}
		})
	}
}

// Changed content opens a new version and closes the old one. This is the test
// that would catch versioning-in-place: the store would report the new content
// at both instants.
func TestChangedContentIsTwoVersions(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "v:host", "primary is sds-meta")
			between := b.store.Now()
			mustNode(t, b.store, ctx, "v:host", "primary is sds-a")

			old, err := b.store.GetNode(AsOf(ctx, between), "v:host")
			if err != nil {
				t.Fatalf("GetNode as-of: %v", err)
			}
			if old.Content != "primary is sds-meta" {
				t.Errorf("as of %s the node said %q, want the old content", between, old.Content)
			}
			if old.ValidTo.IsZero() {
				t.Error("the superseded version has no end; its interval is still open")
			}

			now, err := b.store.GetNode(ctx, "v:host")
			if err != nil {
				t.Fatalf("GetNode: %v", err)
			}
			if now.Content != "primary is sds-a" {
				t.Errorf("current content = %q", now.Content)
			}
			if now.ValidFrom.IsZero() {
				t.Error("the current version does not say when it began")
			}
			// Half-open and adjacent: the old version ends exactly where the
			// new one begins, so there is no instant at which both are visible
			// and none at which neither is.
			if !old.ValidTo.Equal(now.ValidFrom) {
				t.Errorf("versions are not adjacent: old ends %s, new begins %s", old.ValidTo, now.ValidFrom)
			}
			// The id is unchanged, which is the whole reason history is a
			// separate table: graph_edges has foreign keys onto graph_nodes(id)
			// that both backends enforce.
			if now.ID != old.ID {
				t.Errorf("the id changed across versions: %q then %q", old.ID, now.ID)
			}
		})
	}
}

// Re-writing a node with what it already says must not open a version. Without
// this, re-ingesting an unchanged corpus would restamp the whole graph and every
// as-of read would report that nothing existed before the last ingest.
func TestAnUnchangedUpsertIsNotAVersion(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "u:same", "unchanged")
			first, err := b.store.GetNode(ctx, "u:same")
			if err != nil {
				t.Fatalf("GetNode: %v", err)
			}
			mustNode(t, b.store, ctx, "u:same", "unchanged")
			second, err := b.store.GetNode(ctx, "u:same")
			if err != nil {
				t.Fatalf("GetNode: %v", err)
			}
			if !first.ValidFrom.Equal(second.ValidFrom) {
				t.Errorf("an unchanged rewrite restamped valid_from: %s then %s",
					first.ValidFrom, second.ValidFrom)
			}
			var history int
			if err := b.store.queryRow(ctx,
				`SELECT COUNT(*) FROM graph_node_history WHERE id = ?`, "u:same").Scan(&history); err != nil {
				t.Fatalf("count history: %v", err)
			}
			if history != 0 {
				t.Errorf("an unchanged rewrite wrote %d history rows", history)
			}
		})
	}
}

func TestGraphDiffReportsExactlyWhatChanged(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "d:kept", "unchanged throughout")
			mustNode(t, b.store, ctx, "d:changed", "before")
			mustNode(t, b.store, ctx, "d:retracted", "will go")

			t1 := b.store.Now()

			mustNode(t, b.store, ctx, "d:changed", "after")
			mustNode(t, b.store, ctx, "d:added", "new")
			if err := b.store.DeleteNode(ctx, "d:retracted"); err != nil {
				t.Fatalf("DeleteNode: %v", err)
			}

			t2 := b.store.Now()

			diff, err := b.store.GraphDiff(ctx, t1, t2, DiffOptions{
				NodeTypes: []string{"host"},
			})
			if err != nil {
				t.Fatalf("GraphDiff: %v", err)
			}

			byID := map[string]NodeChange{}
			for _, c := range diff.Nodes {
				byID[c.ID] = c
			}
			if len(byID) != 3 {
				t.Fatalf("diff reported %d node changes (%v), want 3", len(byID), byID)
			}
			if c := byID["d:added"]; c.Kind != DiffAdded || c.After == nil || c.After.Content != "new" {
				t.Errorf("d:added = %+v", c)
			}
			if c := byID["d:retracted"]; c.Kind != DiffRetracted || c.Before == nil || c.Before.Content != "will go" {
				t.Errorf("d:retracted = %+v", c)
			}
			if c := byID["d:changed"]; c.Kind != DiffChanged ||
				c.Before == nil || c.Before.Content != "before" ||
				c.After == nil || c.After.Content != "after" {
				t.Errorf("d:changed = %+v", c)
			}
			if _, listed := byID["d:kept"]; listed {
				t.Error("the diff listed a node that did not change")
			}
		})
	}
}

// A diff over a graph larger than the limit must be complete across pages,
// which is the property a naive cursor breaks: it either skips rows or loops.
func TestGraphDiffPagesWithoutSkippingOrRepeating(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			t1 := b.store.Now()
			const n = 25
			for i := 0; i < n; i++ {
				mustNode(t, b.store, ctx, fmt.Sprintf("p:%02d", i), "added")
			}
			t2 := b.store.Now()

			seen := map[string]int{}
			cursor := ""
			for pages := 0; ; pages++ {
				if pages > n+5 {
					t.Fatal("paging did not terminate")
				}
				diff, err := b.store.GraphDiff(ctx, t1, t2, DiffOptions{Limit: 7, Cursor: cursor})
				if err != nil {
					t.Fatalf("GraphDiff page %d: %v", pages, err)
				}
				for _, c := range diff.Nodes {
					seen[c.ID]++
				}
				if !diff.Truncated {
					break
				}
				if diff.NextCursor == cursor {
					t.Fatalf("cursor did not advance past %q", cursor)
				}
				cursor = diff.NextCursor
			}
			if len(seen) != n {
				t.Errorf("paging saw %d distinct ids, want %d", len(seen), n)
			}
			for id, count := range seen {
				if count != 1 {
					t.Errorf("%s was reported %d times across pages", id, count)
				}
			}
		})
	}
}

// The Allen relations reach the diff: two claims about one subject, one ending
// where the other begins.
func TestGraphDiffNamesHowTwoClaimsSitInTime(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "a:sds-meta", "the metadata host")
			mustNode(t, b.store, ctx, "a:runbook", "the runbook")
			mustNode(t, b.store, ctx, "a:incident", "the incident")

			// The runbook's claim exists at t1; the failover happens between
			// the two instants, ending it and beginning the incident's.
			mustEdge(t, b.store, ctx, "a:claim-runbook", "a:sds-meta", "a:runbook", "recommended_by", nil)
			t1 := b.store.Now()

			boundary := b.store.Now()
			if err := b.store.RetractEdgeAt(ctx, "a:claim-runbook", boundary); err != nil {
				t.Fatalf("RetractEdgeAt: %v", err)
			}
			if err := b.store.UpsertEdge(ctx, &GraphEdge{
				ID: "a:claim-incident", FromNodeID: "a:sds-meta", ToNodeID: "a:incident",
				EdgeType: "failed_during", Weight: 1, ValidFrom: boundary,
			}); err != nil {
				t.Fatalf("UpsertEdge: %v", err)
			}

			diff, err := b.store.GraphDiff(ctx, t1, b.store.Now(), DiffOptions{})
			if err != nil {
				t.Fatalf("GraphDiff: %v", err)
			}
			var found *IntervalRelation
			for i := range diff.IntervalRelations {
				r := diff.IntervalRelations[i]
				if r.Subject == "a:sds-meta" {
					found = &diff.IntervalRelations[i]
					_ = r
					break
				}
			}
			if found == nil {
				t.Fatalf("no interval relation about a:sds-meta in %+v", diff.IntervalRelations)
			}
			if found.Relation != AllenMeets && found.Relation != AllenMetBy {
				t.Errorf("relation = %q, want meets/met_by: the runbook's claim ended where the incident's began",
					found.Relation)
			}
		})
	}
}

func TestPurgeRemovesOnlyWhatClosedBeforeTheCutoffAndRefusesWithoutOne(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "g:old", "retracted early")
			mustNode(t, b.store, ctx, "g:new", "retracted late")
			if err := b.store.DeleteNode(ctx, "g:old"); err != nil {
				t.Fatalf("DeleteNode: %v", err)
			}
			cutoff := b.store.Now()
			if err := b.store.DeleteNode(ctx, "g:new"); err != nil {
				t.Fatalf("DeleteNode: %v", err)
			}

			if _, err := b.store.Purge(ctx, time.Time{}, false); err == nil {
				t.Fatal("Purge accepted a zero cutoff; that would erase the whole history")
			} else if !strings.Contains(err.Error(), "cutoff") {
				t.Errorf("the refusal does not mention the cutoff: %v", err)
			}

			dry, err := b.store.Purge(ctx, cutoff, true)
			if err != nil {
				t.Fatalf("Purge dry run: %v", err)
			}
			if dry.Nodes != 1 {
				t.Errorf("dry run would remove %d node rows, want 1", dry.Nodes)
			}

			report, err := b.store.Purge(ctx, cutoff, false)
			if err != nil {
				t.Fatalf("Purge: %v", err)
			}
			if report.Nodes != 1 {
				t.Errorf("purged %d node rows, want 1 (only the one retracted before the cutoff)", report.Nodes)
			}

			// The early one is unreadable at any instant now; the late one is
			// still there.
			early := cutoff.Add(-time.Hour)
			if _, err := b.store.GetNode(AsOf(ctx, early), "g:old"); err == nil {
				t.Error("a purged node is still readable as-of")
			}
			if _, err := b.store.GetNode(AsOf(ctx, cutoff), "g:new"); err != nil {
				t.Errorf("Purge took a row that closed after the cutoff: %v", err)
			}
		})
	}
}

// The ambient as-of must never reach a write. It is a context value, which is
// exactly the shape that leaks, so the refusal is the control.
func TestWritesRefuseAnAsOfContext(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			past := AsOf(ctx, time.Now().Add(-time.Hour))

			if err := b.store.UpsertNode(past, &GraphNode{ID: "w:x", Vector: vec(), Content: "no"}); err == nil {
				t.Error("UpsertNode wrote under an as-of context")
			}
			if err := b.store.UpsertEdge(past, &GraphEdge{ID: "w:e", FromNodeID: "a", ToNodeID: "b"}); err == nil {
				t.Error("UpsertEdge wrote under an as-of context")
			}
			if err := b.store.DeleteNode(past, "w:x"); err == nil {
				t.Error("DeleteNode wrote under an as-of context")
			}
			if _, err := b.store.Purge(past, time.Now(), false); err == nil {
				t.Error("Purge ran under an as-of context")
			}
		})
	}
}

// Retracting into the past: belief that ended last Tuesday, discovered today.
func TestRetractionCanBeBackdated(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "bd:a", "a")
			mustNode(t, b.store, ctx, "bd:b", "b")
			mustEdge(t, b.store, ctx, "bd:e", "bd:a", "bd:b", "believed", nil)

			opened := b.store.Now()
			ended := opened.Add(time.Hour)
			if err := b.store.RetractEdgeAt(ctx, "bd:e", ended); err != nil {
				t.Fatalf("RetractEdgeAt: %v", err)
			}
			if got, err := b.store.GetEdges(AsOf(ctx, opened), "bd:a", "out"); err != nil || len(got) != 1 {
				t.Errorf("before the stated end: %d edges (%v), want 1", len(got), err)
			}
			if got, err := b.store.GetEdges(AsOf(ctx, ended.Add(time.Second)), "bd:a", "out"); err != nil || len(got) != 0 {
				t.Errorf("after the stated end: %d edges (%v), want 0", len(got), err)
			}
		})
	}
}

// The whole read surface honours as-of, not just the two getters: a caller that
// counted the past graph with a current count would report a number that was
// never true.
func TestTheReadSurfaceHonoursAsOf(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "s:a", "first")
			mustNode(t, b.store, ctx, "s:b", "second")
			mustEdge(t, b.store, ctx, "s:e", "s:a", "s:b", "knows", nil)
			before := b.store.Now()

			if _, _, err := b.store.RetractNodes(ctx, []string{"s:b"}); err != nil {
				t.Fatalf("RetractNodes: %v", err)
			}

			past := AsOf(ctx, before)
			filter := &GraphFilter{NodeTypes: []string{"host"}}

			if n, err := b.store.CountNodes(past, filter); err != nil || n != 2 {
				t.Errorf("CountNodes as-of = %d (%v), want 2", n, err)
			}
			if n, err := b.store.CountNodes(ctx, filter); err != nil || n != 1 {
				t.Errorf("CountNodes now = %d (%v), want 1", n, err)
			}
			if list, err := b.store.ListNodes(past, filter); err != nil || len(list) != 2 {
				t.Errorf("ListNodes as-of = %d (%v), want 2", len(list), err)
			}
			if labels, err := b.store.NodeLabels(past, NodeLabelQuery{IDPrefix: "s:"}); err != nil || len(labels) != 2 {
				t.Errorf("NodeLabels as-of = %d (%v), want 2", len(labels), err)
			}
			if batch, err := b.store.GetNodesBatch(past, []string{"s:a", "s:b"}); err != nil || len(batch) != 2 {
				t.Errorf("GetNodesBatch as-of = %d (%v), want 2", len(batch), err)
			}
			if batch, err := b.store.GetEdgesBatch(past, []string{"s:e"}); err != nil || len(batch) != 1 {
				t.Errorf("GetEdgesBatch as-of = %d (%v), want 1", len(batch), err)
			}
			// Connectivity names the node source in the FROM and the edge
			// source inside a correlated subquery, so it is the one query whose
			// as-of arguments are bound out of clause order.
			conn, err := b.store.Connectivity(past)
			if err != nil {
				t.Fatalf("Connectivity as-of: %v", err)
			}
			if conn.Nodes != 2 || conn.Orphans != 0 {
				t.Errorf("Connectivity as-of = %+v, want {Nodes:2 Orphans:0}", conn)
			}
			nowConn, err := b.store.Connectivity(ctx)
			if err != nil {
				t.Fatalf("Connectivity: %v", err)
			}
			if nowConn.Nodes != 1 || nowConn.Orphans != 1 {
				t.Errorf("Connectivity now = %+v, want {Nodes:1 Orphans:1}", nowConn)
			}
		})
	}
}

// The history tables have to exist on a store that never ran the old schema, on
// both backends, and creating them twice has to be harmless — the same property
// the rest of the schema is asserted to have.
func TestTemporalSchemaIsIdempotent(t *testing.T) {
	db := pgtest.Open(t, "graph_temporal")
	if db == nil {
		t.Skip(pgtest.EnvDSN + " unset — the PostgreSQL temporal migration is NOT covered by this run")
	}
	cfg := core.DefaultConfig()
	cfg.VectorDim = 4
	g := NewGraphStoreOn(db, sqldialect.For(sqldialect.Postgres), testHost{cfg: cfg})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := g.createGraphSchema(ctx); err != nil {
			t.Fatalf("createGraphSchema call %d: %v", i+1, err)
		}
	}
}
