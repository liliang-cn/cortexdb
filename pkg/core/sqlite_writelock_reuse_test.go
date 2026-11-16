package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
)

// openReuseStore is a store pinned to one connection, so that whatever one
// caller leaves on it is what the next caller gets, and the path of its file.
func openReuseStore(t *testing.T) (*SQLiteStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reuse.db")
	s, err := New(path, 3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.db.Exec(`CREATE TABLE turns (writer INTEGER, n INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO turns VALUES (1, 1), (2, 2), (3, 3)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s.db.SetMaxOpenConns(1)
	return s, path
}

// openOtherPool opens a second pool on the file: another writer.
func openOtherPool(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := openQueuedSQLite(path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(1000)", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// leaveAReadRunning steps a SELECT on the connection to its first row and
// leaves it there, as the driver does with the rows of a query whose context
// ends just as the first row arrives. The statement is never finalized.
func leaveAReadRunning(t *testing.T, conn *sql.Conn) sqliteHandle {
	t.Helper()
	var h sqliteHandle
	err := conn.Raw(func(dc any) error {
		qc, ok := dc.(*queuedConn)
		if !ok || qc.handle == nil {
			t.Fatalf("connection %T has no SQLite handle", dc)
		}
		h = *qc.handle
		st, _, msg, err := h.prepare(`SELECT writer FROM turns`)
		if err != nil || st == 0 {
			t.Fatalf("prepare: %v %s", err, msg)
		}
		if rc := sqlite3.Xsqlite3_step(h.tls, st); rc != sqlite3.SQLITE_ROW {
			t.Fatalf("step: rc %d", rc)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	return h
}

// A read left running pins the snapshot it started on. The connection used
// to go back to the pool with it, and the next transaction there read the
// old snapshot and failed its first write with SQLITE_BUSY_SNAPSHOT (517)
// once another writer had committed (#199).
func TestAConnectionWithAReadStillRunningIsNotReused(t *testing.T) {
	s, path := openReuseStore(t)
	ctx := context.Background()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	leaveAReadRunning(t, conn)
	_ = conn.Close()

	if _, err := openOtherPool(t, path).Exec(`INSERT INTO turns VALUES (4, 4)`); err != nil {
		t.Fatalf("other writer: %v", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM turns`).Scan(&n); err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != 4 {
		t.Errorf("the transaction read %d rows, want the 4 committed", n)
	}
	if _, err := tx.Exec(`INSERT INTO turns VALUES (5, 5)`); err != nil {
		t.Fatalf("write after the read: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// A ROLLBACK can fail: the driver's interrupt for a context that ended can
// land on it, and SQLite keeps the connection interrupted while a statement
// on it still runs. The transaction stayed open, the connection went back to
// the pool with it, and a transaction that had written kept the write lock:
// every other writer failed with "database is locked" (#199).
func TestATransactionWhoseRollbackFailsLeavesNoLockBehind(t *testing.T) {
	s, path := openReuseStore(t)
	ctx := context.Background()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO turns VALUES (9, 9)`); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := leaveAReadRunning(t, conn)
	sqlite3.Xsqlite3_interrupt(h.tls, h.db)
	if err := tx.Rollback(); err == nil {
		t.Fatal("the ROLLBACK succeeded; the test no longer sets up a failing one")
	}
	_ = conn.Close()

	other := openOtherPool(t, path)
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := other.ExecContext(wctx, `INSERT INTO turns VALUES (4, 4)`); err != nil {
		t.Fatalf("another writer after the failed rollback: %v", err)
	}
	var n int
	if err := other.QueryRow(`SELECT COUNT(*) FROM turns WHERE writer = 9`).Scan(&n); err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != 0 {
		t.Errorf("the rolled-back row is there %d times", n)
	}
	if _, err := s.db.ExecContext(wctx, `INSERT INTO turns VALUES (5, 5)`); err != nil {
		t.Fatalf("a write through the same pool: %v", err)
	}
}
