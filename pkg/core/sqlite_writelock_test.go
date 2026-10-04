package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTurnStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "turns.db"), 3)
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
	return s
}

// Eight writers whose transactions each hold the lock for a while. Under
// SQLite's busy poll the longest waiter lost round after round — seconds, and
// past busy_timeout a "database is locked" — while queued, nobody waits much
// longer than the seven transactions ahead of it.
func TestWritersInOneProcessTakeTurns(t *testing.T) {
	s := openTurnStore(t)
	const writers, rounds = 8, 25
	const hold = 15 * time.Millisecond

	var mu sync.Mutex
	var worst time.Duration
	var errs []error
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				start := time.Now()
				tx, err := s.db.BeginTx(context.Background(), nil)
				if err == nil {
					_, err = tx.Exec(`INSERT INTO turns (writer, n) VALUES (?, ?)`, w, i)
				}
				waited := time.Since(start)
				if err == nil {
					time.Sleep(hold) // the transaction's own work
					err = tx.Commit()
				} else if tx != nil {
					_ = tx.Rollback()
				}
				mu.Lock()
				if err != nil {
					errs = append(errs, err)
				}
				if waited > worst {
					worst = waited
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d writes failed, first: %v", len(errs), errs[0])
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM turns`).Scan(&n); err != nil || n != writers*rounds {
		t.Fatalf("rows = %d (%v), want %d", n, err, writers*rounds)
	}
	// Seven transactions of 15ms ahead is ~105ms; a second leaves room for a
	// slow machine and still fails the poll, which starved for seconds.
	if worst > time.Second {
		t.Errorf("the longest wait for a write was %s", worst)
	}
}

// Reads never queue: a read, and a read-only transaction, run while a write
// transaction holds the turn.
func TestReadsDoNotWaitForTheWriteTurn(t *testing.T) {
	s := openTurnStore(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO turns VALUES (1, 1)`); err != nil {
		t.Fatalf("write: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM turns`).Scan(&n); err != nil {
			done <- err
			return
		}
		ro, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = ro.Rollback() }()
		done <- ro.QueryRowContext(ctx, `SELECT COUNT(*) FROM turns`).Scan(&n)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read during a write transaction: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a read waited for the write turn")
	}
}

// The turn comes back however a write ends — commit, rollback, a failed
// statement outside a transaction — and a writer that cannot get it in time
// is told so as SQLite's busy error.
func TestTheWriteTurnIsAlwaysGivenBack(t *testing.T) {
	s := openTurnStore(t)
	ctx := context.Background()

	for i, end := range []func(*sql.Tx) error{(*sql.Tx).Commit, (*sql.Tx).Rollback} {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin %d: %v", i, err)
		}
		if _, err := tx.Exec(`INSERT INTO turns VALUES (?, 0)`, i); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if err := end(tx); err != nil {
			t.Fatalf("end %d: %v", i, err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO no_such_table VALUES (1)`); err == nil {
		t.Fatal("a write to a missing table succeeded")
	}
	writeWithin(t, s.db, time.Second)

	// Holding the turn, a second writer gives up with the busy error.
	old := writeTurnWait
	writeTurnWait = 200 * time.Millisecond
	t.Cleanup(func() { writeTurnWait = old })
	held, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, err = s.db.Exec(`INSERT INTO turns VALUES (9, 9)`)
	if err == nil || !strings.Contains(err.Error(), "database is locked") {
		t.Errorf("a write that never got its turn returned %v, want a database-is-locked error", err)
	}
	_ = held.Rollback()
	writeWithin(t, s.db, time.Second)
}

func writeWithin(t *testing.T, db *sql.DB, limit time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if _, err := db.ExecContext(ctx, `INSERT INTO turns VALUES (0, 0)`); err != nil {
		t.Fatalf("the turn was not given back: %v", err)
	}
}

func TestWhichQueriesWrite(t *testing.T) {
	for q, want := range map[string]bool{
		"SELECT 1": false,
		"  insert into t values (1) returning id": true,
		"UPDATE t SET a = 1 RETURNING a":          true,
		"DELETE FROM t RETURNING *":               true,
		"REPLACE INTO t VALUES (1)":               true,
		"PRAGMA wal_checkpoint(TRUNCATE)":         true,
		"PRAGMA table_info(t)":                    false,
		"WITH x AS (SELECT 1) SELECT * FROM x":    false,
		"\n\tSELECT * FROM turns":                 false,
	} {
		if got := writesSQL(q); got != want {
			t.Errorf("writesSQL(%q) = %v, want %v", q, got, want)
		}
	}
}
