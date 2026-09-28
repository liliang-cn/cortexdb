package graph

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Why a row ended, on both backends.

// Every path that moves a row to history says why, with a default when the
// caller says nothing and the caller's words when it does.
func TestHistoryRowsCarryAReason(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			mustNode(t, g, ctx, "r:a", "a")
			mustNode(t, g, ctx, "r:b", "b")
			mustNode(t, g, ctx, "r:c", "c")
			mustEdge(t, g, ctx, "r:e1", "r:a", "r:b", "knows", nil)
			mustEdge(t, g, ctx, "r:e2", "r:a", "r:c", "knows", nil)

			// An upsert that changes content closes the old version.
			mustNode(t, g, ctx, "r:a", "a, corrected")
			// A retraction with nothing said.
			if err := g.RetractEdgeAt(ctx, "r:e1", g.Now()); err != nil {
				t.Fatalf("retract: %v", err)
			}
			// A retraction whose caller says why.
			said := WithInvalidation(ctx, Invalidation{Reason: "contradicted", SupersededBy: "r:e9", Producer: "test"})
			if err := g.RetractEdgeAt(said, "r:e2", g.Now()); err != nil {
				t.Fatalf("retract with reason: %v", err)
			}

			nh, err := g.NodeHistory(ctx, "r:a")
			if err != nil || len(nh) != 1 {
				t.Fatalf("node history: %v %v", nh, err)
			}
			if nh[0].Reason != ReasonSuperseded || nh[0].SupersededBy != "r:a" || nh[0].Producer != ProducerGraphUpsert {
				t.Errorf("superseded version: %+v", nh[0].Invalidation)
			}
			if nh[0].Content != "a" {
				t.Errorf("history content = %q", nh[0].Content)
			}

			e1, _ := g.EdgeHistory(ctx, "r:e1")
			if len(e1) != 1 || e1[0].Reason != ReasonRetracted || e1[0].Producer != ProducerGraphRetract || e1[0].SupersededBy != "" {
				t.Errorf("default retraction: %+v", e1)
			}
			e2, _ := g.EdgeHistory(ctx, "r:e2")
			if len(e2) != 1 || e2[0].Reason != "contradicted" || e2[0].SupersededBy != "r:e9" || e2[0].Producer != "test" {
				t.Errorf("stated retraction: %+v", e2)
			}
		})
	}
}

// A merge is not a delete: the alias and the edges it had are in history,
// naming the survivor.
func TestMergeEntitiesRecordsWhatItFolded(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			mustNode(t, g, ctx, "m:canon", "Kubernetes")
			mustNode(t, g, ctx, "m:alias", "K8s")
			mustNode(t, g, ctx, "m:x", "x")
			mustEdge(t, g, ctx, "m:e1", "m:alias", "m:x", "runs", nil)
			mustEdge(t, g, ctx, "m:e2", "m:canon", "m:alias", "same", nil)
			before := g.Now()

			if err := g.MergeEntities(ctx, "m:canon", []string{"m:alias"}); err != nil {
				t.Fatalf("merge: %v", err)
			}

			nh, _ := g.NodeHistory(ctx, "m:alias")
			if len(nh) != 1 || nh[0].Reason != ReasonMerged || nh[0].SupersededBy != "m:canon" || nh[0].Producer != ProducerGraphMerge {
				t.Fatalf("alias history: %+v", nh)
			}
			eh, _ := g.EdgeHistory(ctx, "m:e1")
			if len(eh) != 1 || eh[0].From != "m:alias" || eh[0].Reason != ReasonMerged {
				t.Fatalf("repointed edge history: %+v", eh)
			}
			// The repointed edge is live and starts where its old version ended.
			live, err := g.GetEdges(ctx, "m:canon", "out")
			if err != nil || len(live) != 1 || live[0].ID != "m:e1" {
				t.Fatalf("live edges after merge: %v %v", live, err)
			}
			if !live[0].ValidFrom.Equal(eh[0].ValidTo) {
				t.Errorf("repointed edge opens at %v, old version closed at %v", live[0].ValidFrom, eh[0].ValidTo)
			}
			// The self-loop the merge created is gone now and in history.
			if lh, _ := g.EdgeHistory(ctx, "m:e2"); len(lh) == 0 || lh[len(lh)-1].Reason != ReasonMerged {
				t.Errorf("self-loop history: %+v", lh)
			}
			// And the alias is still there before the merge.
			if _, err := g.GetNode(AsOf(ctx, before), "m:alias"); err != nil {
				t.Errorf("alias as of before the merge: %v", err)
			}

			// graph_diff says why each thing changed.
			diff, err := g.GraphDiff(ctx, before, g.Now(), DiffOptions{})
			if err != nil {
				t.Fatalf("diff: %v", err)
			}
			var sawNode bool
			for _, c := range diff.Nodes {
				if c.ID == "m:alias" {
					sawNode = c.Kind == DiffRetracted && c.Invalidation != nil &&
						c.Invalidation.Reason == ReasonMerged && c.Invalidation.SupersededBy == "m:canon"
				}
			}
			if !sawNode {
				t.Errorf("diff does not carry the merge: %+v", diff.Nodes)
			}
			for _, c := range diff.Edges {
				if c.ID == "m:e1" && (c.Kind != DiffChanged || c.Invalidation == nil || c.Invalidation.Reason != ReasonMerged) {
					t.Errorf("diff edge m:e1: %+v", c)
				}
			}
		})
	}
}

// A history table from before this release gains the columns, its rows read
// with an empty reason, and new rows get one.
func TestHistoryFromBeforeReasonsMigrates(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			if err := g.InitGraphSchema(ctx); err != nil {
				t.Fatalf("init: %v", err)
			}
			if _, err := g.db.ExecContext(ctx, `DROP TABLE graph_edge_history`); err != nil {
				t.Fatalf("drop: %v", err)
			}
			// The 2.112 shape: temporal columns, no reason columns.
			if _, err := g.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE graph_edge_history (
				id TEXT NOT NULL, from_node_id TEXT NOT NULL, to_node_id TEXT NOT NULL,
				edge_type TEXT, weight REAL, properties TEXT, vector %s, created_at TIMESTAMP,
				valid_from TIMESTAMP, valid_to TIMESTAMP, recorded_at TIMESTAMP, retracted_at TIMESTAMP)`,
				g.dialect.BlobType())); err != nil {
				t.Fatalf("old shape: %v", err)
			}
			old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if _, err := g.exec(ctx, `INSERT INTO graph_edge_history (id, from_node_id, to_node_id, edge_type, retracted_at)
				VALUES (?, ?, ?, ?, ?)`, "old:e", "old:a", "old:b", "knows", old); err != nil {
				t.Fatalf("old row: %v", err)
			}

			// Twice: the second run must find the columns already there.
			for i := 0; i < 2; i++ {
				if err := g.createTemporalSchema(ctx); err != nil {
					t.Fatalf("migration %d: %v", i, err)
				}
			}

			hist, err := g.EdgeHistory(ctx, "old:e")
			if err != nil || len(hist) != 1 || hist[0].Reason != "" {
				t.Fatalf("old row after migration: %+v %v", hist, err)
			}
			mustNode(t, g, ctx, "new:a", "a")
			mustNode(t, g, ctx, "new:b", "b")
			mustEdge(t, g, ctx, "new:e", "new:a", "new:b", "knows", nil)
			if err := g.DeleteEdge(ctx, "new:e"); err != nil {
				t.Fatalf("delete: %v", err)
			}
			counts, err := g.HistoryReasons(ctx)
			if err != nil {
				t.Fatalf("reasons: %v", err)
			}
			if counts.Edges[""] != 1 || counts.Edges[ReasonRetracted] != 1 {
				t.Fatalf("edge reasons = %v", counts.Edges)
			}
		})
	}
}
