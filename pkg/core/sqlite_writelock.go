package core

// Writers in one process take turns, first come first served.
//
// SQLite has one writer at a time, and a connection that finds the write lock
// taken waits in its busy handler: it sleeps and retries, the sleeps growing
// to 100ms, until busy_timeout (5s here) runs out. That is a poll, not a
// queue. With several goroutines writing through the pool, a writer that has
// waited longest sleeps longest, wakes after the lock was freed and taken
// again by one that only just started waiting, and can lose every round until
// its five seconds are gone. Measured with eight goroutines batch-upserting an
// agent's execution graph: four or five writes in fifteen thousand failed
// with "database is locked" after waiting 5–11s, while the median write took
// about a millisecond. The change feed's triggers lengthen every write
// transaction, so it showed with the feed on and not with it off — but it is
// the poll that starves, not the feed.
//
// Writers in one process cannot write at the same time anyway, so making them
// queue costs no throughput, and a queue has no starvation. Every connection
// the store opens is wrapped: a transaction that may write, and a statement
// outside a transaction that writes, first takes its turn on a channel shared
// by every pool open on the same file in this process — Go hands a channel's
// blocked senders the value in the order they arrived — and gives it back
// when the transaction ends or the statement finishes. Reads never queue.
// Other processes still meet this one at SQLite's lock, as before; this only
// stops the process from starving itself.
//
// A turn is waited for at most writeTurnWait. Waiting in a queue is not
// starvation, so that is longer than busy_timeout; it bounds what used to be
// a deadlock-shaped mistake — a goroutine holding a write transaction and
// writing through another connection — which SQLite turned into a "database
// is locked" after five seconds and which this turns into the same error,
// later.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// writeTurnWait bounds how long a writer queues for its turn. A variable so a
// test can see the timeout without waiting it out.
var writeTurnWait = 30 * time.Second

// writeTurn is one file's queue: a channel with room for the writer whose
// turn it is.
type writeTurn struct{ ch chan struct{} }

func newWriteTurn() *writeTurn { return &writeTurn{ch: make(chan struct{}, 1)} }

func (w *writeTurn) take(ctx context.Context) error {
	t := time.NewTimer(writeTurnWait)
	defer t.Stop()
	select {
	case w.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		// Worded so the callers that already recognise SQLite's busy error
		// (retry loops look for "database is locked") treat it as one.
		return fmt.Errorf("cortexdb: no turn to write within %s, another writer in this process held it: database is locked (SQLITE_BUSY)", writeTurnWait)
	}
}

func (w *writeTurn) give() { <-w.ch }

var (
	writeTurnsMu sync.Mutex
	writeTurns   = map[string]*writeTurn{}
)

// writeTurnFor returns the queue shared by every pool on path in this
// process. An in-memory database is private to its pool, so it gets its own.
func writeTurnFor(path string) *writeTurn {
	if isMemoryPath(path) {
		return newWriteTurn()
	}
	key := path
	if abs, err := filepath.Abs(path); err == nil {
		key = filepath.Clean(abs)
	}
	writeTurnsMu.Lock()
	defer writeTurnsMu.Unlock()
	w := writeTurns[key]
	if w == nil {
		w = newWriteTurn()
		writeTurns[key] = w
	}
	return w
}

// openQueuedSQLite opens dsn through the registered "sqlite" driver with every
// connection taking turns to write on path's queue.
func openQueuedSQLite(dsn, path string) (*sql.DB, error) {
	probe, err := sql.Open("sqlite", "") // no connection is made; this is how the driver is reached
	if err != nil {
		return nil, err
	}
	d := probe.Driver()
	_ = probe.Close()
	return sql.OpenDB(&queuedConnector{dsn: dsn, driver: d, turn: writeTurnFor(path)}), nil
}

type queuedConnector struct {
	dsn    string
	driver driver.Driver
	turn   *writeTurn
}

func (c *queuedConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &queuedConn{Conn: conn, turn: c.turn}, nil
}

func (c *queuedConnector) Driver() driver.Driver { return c.driver }

// queuedConn forwards everything to the driver's connection, taking a turn
// around writes. inTx is the connection's own transaction holding the turn:
// statements inside it must not queue behind themselves. database/sql uses a
// connection from one goroutine at a time, so it needs no lock of its own.
type queuedConn struct {
	driver.Conn
	turn *writeTurn
	inTx bool
}

func (c *queuedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if opts.ReadOnly {
		return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	}
	if err := c.turn.take(ctx); err != nil {
		return nil, err
	}
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		c.turn.give()
		return nil, err
	}
	c.inTx = true
	return &queuedTx{Tx: tx, conn: c}, nil
}

func (c *queuedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *queuedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	st, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &queuedStmt{Stmt: st, conn: c, writes: writesSQL(query)}, nil
}

func (c *queuedConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *queuedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	release, err := c.turnFor(ctx, true)
	if err != nil {
		return nil, err
	}
	defer release()
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *queuedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	release, err := c.turnFor(ctx, writesSQL(query))
	if err != nil {
		return nil, err
	}
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil {
		release()
		return nil, err
	}
	return &queuedRows{Rows: rows, release: release}, nil
}

// turnFor takes a turn for a statement that writes and runs outside a
// transaction, and returns what gives it back.
func (c *queuedConn) turnFor(ctx context.Context, writes bool) (func(), error) {
	if !writes || c.inTx {
		return func() {}, nil
	}
	if err := c.turn.take(ctx); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(c.turn.give) }, nil
}

func (c *queuedConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *queuedConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *queuedConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *queuedConn) Close() error {
	if c.inTx { // a connection dropped mid-transaction must not keep the turn
		c.inTx = false
		c.turn.give()
	}
	return c.Conn.Close()
}

type queuedTx struct {
	driver.Tx
	conn *queuedConn
	once sync.Once
}

func (t *queuedTx) end() {
	t.once.Do(func() {
		if t.conn.inTx {
			t.conn.inTx = false
			t.conn.turn.give()
		}
	})
}

func (t *queuedTx) Commit() error {
	defer t.end()
	return t.Tx.Commit()
}

func (t *queuedTx) Rollback() error {
	defer t.end()
	return t.Tx.Rollback()
}

type queuedStmt struct {
	driver.Stmt
	conn   *queuedConn
	writes bool
}

func (s *queuedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	release, err := s.conn.turnFor(ctx, true)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (s *queuedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	release, err := s.conn.turnFor(ctx, s.writes)
	if err != nil {
		return nil, err
	}
	rows, err := s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		release()
		return nil, err
	}
	return &queuedRows{Rows: rows, release: release}, nil
}

// queuedRows holds a writing query's turn until its rows are closed: the
// statement is still running while they are read.
type queuedRows struct {
	driver.Rows
	release func()
}

func (r *queuedRows) Close() error {
	defer r.release()
	return r.Rows.Close()
}

// writesSQL reports whether a statement run through Query writes: a DML
// statement with RETURNING, or a WAL checkpoint, which takes the write lock
// and holds new writers off while it waits for readers. Anything else read
// through Query is a read. Exec always counts as a write.
func writesSQL(query string) bool {
	q := strings.ToUpper(strings.TrimSpace(query))
	for _, p := range []string{"INSERT", "UPDATE", "DELETE", "REPLACE", "PRAGMA WAL_CHECKPOINT"} {
		if strings.HasPrefix(q, p) {
			return true
		}
	}
	return false
}
