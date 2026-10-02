package graph

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A transaction holding the pool's only connection still writes: preparing
// the cached statement needs a connection of its own, and the cache must give
// up waiting for one rather than wait for the transaction it is part of.
func TestACachedStatementDoesNotWaitForTheTransactionItRunsIn(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	g.db.SetMaxOpenConns(1)
	defer g.db.SetMaxOpenConns(25)

	done := make(chan error, 1)
	go func() {
		tx, err := g.db.BeginTx(ctx, nil)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := g.txExec(ctx, tx, `DELETE FROM graph_nodes WHERE id = ?`, "solo"); err != nil {
			done <- err
			return
		}
		done <- tx.Commit()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the write waited on its own transaction's connection")
	}
}

// The same statement run again is the one prepared the first time, and SQL
// built per call cannot grow the cache past its bound.
func TestTheStatementCacheReusesAndStaysBounded(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	const q = `DELETE FROM graph_nodes WHERE id = ?`
	first := g.preparedStmt(ctx, g.dialect.Rebind(q))
	if first == nil {
		t.Fatal("a plain statement was not prepared")
	}
	if again := g.preparedStmt(ctx, g.dialect.Rebind(q)); again != first {
		t.Fatal("the second use prepared the statement again")
	}
	for i := 0; i < stmtCacheSize+50; i++ {
		tx, err := g.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		// A distinct statement per call, as a batch of i rows would be.
		if _, err := g.txExec(ctx, tx, fmt.Sprintf(`DELETE FROM graph_nodes WHERE id = ? AND %d = %d`, i, i), "none"); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	g.stmts.mu.Lock()
	n := g.stmts.order.Len()
	g.stmts.mu.Unlock()
	if n > stmtCacheSize {
		t.Fatalf("cache holds %d statements, bound is %d", n, stmtCacheSize)
	}
}

// With the feed installed, a graph_nodes upsert must not build an ephemeral
// table: SQLite builds one for an IN list of constants, and in a trigger's
// WHEN that is a b-tree per write (see the graph_nodes mirror predicate).
func TestTheFeedTriggersBuildNoEphemeralTables(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	if err := g.EnsureChangeFeed(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := g.db.QueryContext(ctx, `EXPLAIN INSERT INTO graph_nodes (id, content, node_type, properties) VALUES ('x', 'c', 't', '{}')
		ON CONFLICT(id) DO UPDATE SET content = excluded.content, node_type = excluded.node_type, properties = excluded.properties`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		if op := fmt.Sprint(vals[1]); op == "OpenEphemeral" {
			t.Fatal("a graph_nodes upsert opens an ephemeral table with the feed installed")
		}
	}
}
