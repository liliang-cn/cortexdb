package graph

import (
	"context"
	"testing"
)

// A read under WithReadTx sees what its transaction wrote and has not
// committed; an as-of read follows the transaction too.
func TestWithReadTxReadsInsideTheTransaction(t *testing.T) {
	_, store, done := setupTestGraph(t)
	defer done()
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if err := store.UpsertNode(ctx, &GraphNode{ID: id, NodeType: "entity", Content: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertEdge(ctx, &GraphEdge{ID: "a-b", FromNodeID: "a", ToNodeID: "b", EdgeType: "knows"}); err != nil {
		t.Fatal(err)
	}
	before := store.Now()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := store.deleteNodesBatchTx(ctx, tx, []string{"b"}); err != nil {
		t.Fatal(err)
	}
	inTx := WithReadTx(ctx, tx)
	if _, err := store.GetNode(inTx, "b"); err == nil {
		t.Fatal("the transaction still reads the node it deleted")
	}
	if nodes, err := store.GetNodesBatch(inTx, []string{"a", "b"}); err != nil || len(nodes) != 1 {
		t.Fatalf("the transaction reads %d nodes (%v), want the one it left", len(nodes), err)
	}
	if edges, err := store.GetEdges(inTx, "a", "both"); err != nil || len(edges) != 0 {
		t.Fatalf("the transaction reads %d edges of a (%v), want none", len(edges), err)
	}
	if edges, err := store.GetEdgesBatch(inTx, []string{"a-b"}); err != nil || len(edges) != 0 {
		t.Fatalf("the transaction reads %d edges by id (%v), want none", len(edges), err)
	}
	if _, err := store.GetNode(AsOf(inTx, before), "b"); err != nil {
		t.Fatalf("the transaction does not read the node as it stood: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetNode(ctx, "b"); err != nil {
		t.Fatalf("after the rollback the node is gone: %v", err)
	}
}

// MergeEntities is one transaction: a fold that fails, a commit that fails
// and a store that cannot begin are reported, and none leaves an alias
// folded.
func TestMergeEntitiesReportsEachFailureAndFoldsNothing(t *testing.T) {
	ctx := context.Background()
	brain := func(t *testing.T) *GraphStore {
		_, store, done := setupTestGraph(t)
		t.Cleanup(done)
		for _, id := range []string{"canon", "alias", "other"} {
			if err := store.UpsertNode(ctx, &GraphNode{ID: id, NodeType: "entity", Content: id}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.UpsertEdge(ctx, &GraphEdge{ID: "e", FromNodeID: "alias", ToNodeID: "other", EdgeType: "knows"}); err != nil {
			t.Fatal(err)
		}
		return store
	}
	for name, stmts := range map[string][]string{
		"a fold":                 {`CREATE TRIGGER no_fold BEFORE INSERT ON graph_edge_history BEGIN SELECT RAISE(ABORT, 'blocked'); END`},
		"the alias's own record": {`CREATE TRIGGER no_record BEFORE INSERT ON graph_node_history BEGIN SELECT RAISE(ABORT, 'blocked'); END`},
		"the commit": {
			`CREATE TABLE zz_orphans (parent TEXT REFERENCES graph_nodes(id) DEFERRABLE INITIALLY DEFERRED)`,
			`CREATE TRIGGER zz_orphan AFTER DELETE ON graph_nodes BEGIN INSERT INTO zz_orphans VALUES ('nobody'); END`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := brain(t)
			for _, stmt := range stmts {
				if _, err := store.db.ExecContext(ctx, stmt); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.MergeEntities(ctx, "canon", []string{"alias"}); err == nil {
				t.Fatal("the merge reported no failure")
			}
			if _, err := store.GetNode(ctx, "alias"); err != nil {
				t.Fatalf("the failed merge folded the alias: %v", err)
			}
			if edges, err := store.GetEdges(ctx, "alias", "out"); err != nil || len(edges) != 1 {
				t.Fatalf("the failed merge moved the alias's edge: %v, %v", edges, err)
			}
		})
	}
	t.Run("the begin", func(t *testing.T) {
		store := brain(t)
		held, cancel := context.WithCancel(ctx)
		cancel()
		if err := store.MergeEntities(held, "canon", []string{"alias"}); err == nil {
			t.Fatal("a merge under a cancelled context reported no failure")
		}
	})
}
