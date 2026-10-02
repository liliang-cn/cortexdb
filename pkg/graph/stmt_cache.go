package graph

import (
	"container/list"
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// stmtCache keeps SQLite statements prepared across transactions.
//
// database/sql prepares, runs and finalizes a statement for every Exec that
// carries arguments, and SQLite compiles a table's triggers into every
// statement that can fire them. With the change feed installed a graph upsert
// compiles to some 220 instructions instead of 70, and compiling them — parse,
// plan, an 8 KiB program allocated and freed — was paid again by every write.
// A statement prepared once runs from then on without any of it: Tx.StmtContext
// reuses the parent's statement already prepared on the transaction's
// connection, and prepares it there the first time.
//
// PostgreSQL does not use it: pgx already keeps a per-connection statement
// cache.
//
// The cache is bounded, least recently used out, because some writers build
// their SQL — a batch insert of n rows, an IN list of n ids — and would
// otherwise add a statement per distinct n forever.
type stmtCache struct {
	mu      sync.Mutex
	byQuery map[string]*list.Element
	order   *list.List // front: most recently used
	// failed remembers queries that cannot be prepared on the pool, so a
	// failure is not retried by every call.
	failed map[string]bool
}

type cachedStmt struct {
	query string
	stmt  *sql.Stmt
}

const (
	// stmtCacheSize is the most statements kept prepared.
	stmtCacheSize = 256
	// stmtPrepareWait bounds the one wait the cache adds. The parent
	// statement is prepared on the pool, not on the transaction's connection
	// — database/sql has no other way to make one — and a pool whose every
	// connection is held, by transactions each waiting to prepare, would
	// otherwise wait forever. Past this the write runs unprepared, as before.
	stmtPrepareWait = 50 * time.Millisecond
)

// txExec runs a statement inside a transaction, prepared once and reused on
// SQLite (see stmtCache).
func (g *GraphStore) txExec(ctx context.Context, tx *sql.Tx, q string, args ...any) (sql.Result, error) {
	q = g.dialect.Rebind(q)
	if stmt := g.preparedStmt(ctx, q); stmt != nil {
		s := tx.StmtContext(ctx, stmt)
		defer s.Close()
		return s.ExecContext(ctx, args...)
	}
	return tx.ExecContext(ctx, q, args...)
}

// preparedStmt returns the cached statement for q, preparing it on first use,
// or nil when q is to run unprepared.
func (g *GraphStore) preparedStmt(ctx context.Context, q string) *sql.Stmt {
	if g.isPostgres() {
		return nil
	}
	c := &g.stmts
	c.mu.Lock()
	if c.byQuery == nil {
		c.byQuery = make(map[string]*list.Element)
		c.order = list.New()
		c.failed = make(map[string]bool)
	}
	if el, ok := c.byQuery[q]; ok {
		c.order.MoveToFront(el)
		stmt := el.Value.(*cachedStmt).stmt
		c.mu.Unlock()
		return stmt
	}
	if c.failed[q] {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	prepareCtx, cancel := context.WithTimeout(ctx, stmtPrepareWait)
	defer cancel()
	stmt, err := g.db.PrepareContext(prepareCtx, q)
	if err != nil {
		// A timeout says only that the pool was busy this time.
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			c.mu.Lock()
			if len(c.failed) < stmtCacheSize {
				c.failed[q] = true
			}
			c.mu.Unlock()
		}
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byQuery[q]; ok { // prepared concurrently
		_ = stmt.Close()
		c.order.MoveToFront(el)
		return el.Value.(*cachedStmt).stmt
	}
	c.byQuery[q] = c.order.PushFront(&cachedStmt{query: q, stmt: stmt})
	if c.order.Len() > stmtCacheSize {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		evicted := oldest.Value.(*cachedStmt)
		delete(c.byQuery, evicted.query)
		// Safe while a transaction still runs it: database/sql finalizes a
		// statement only after the last statement derived from it closes.
		_ = evicted.stmt.Close()
	}
	return stmt
}
