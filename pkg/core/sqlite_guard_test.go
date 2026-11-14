package core

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openGuardTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guard.db")
	db, err := openQueuedSQLite(sqliteDSN(path, ResourceConfig{}), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE messages (id INTEGER, content TEXT)`,
		`INSERT INTO messages VALUES (1, 'private')`,
		`CREATE TABLE imported_tbl_1 (region TEXT, total REAL)`,
		`INSERT INTO imported_tbl_1 VALUES ('eu', 10), ('eu', 20), ('us', 5)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func guardedRead(db *sql.DB, query string) (*GuardedRows, error) {
	return RunGuardedSQLiteRead(context.Background(), db, GuardedRead{
		SQL:     query,
		Tables:  map[string]string{"orders": "imported_tbl_1"},
		MaxRows: 10,
		Timeout: 2 * time.Second,
	})
}

// The guard reads two unexported fields of the driver's connection. A driver
// release that moves them fails here, by name, before it fails in a query.
func TestGuardedReadReachesTheDriverHandle(t *testing.T) {
	db := openGuardTestDB(t)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(dc any) error {
		if queued, ok := dc.(*queuedConn); ok {
			dc = queued.Conn
		}
		_, err := sqliteHandleOf(dc)
		return err
	})
	if err != nil {
		t.Fatalf("modernc.org/sqlite's connection no longer has the db and tls fields the guard reads: %v", err)
	}
	if _, err := sqliteHandleOf(struct{ db uintptr }{1}); err == nil {
		t.Fatal("a value that is not the driver's connection was accepted")
	}
}

func TestGuardedReadAnswersAndConfines(t *testing.T) {
	db := openGuardTestDB(t)
	res, err := guardedRead(db, `SELECT region, sum(total) FROM orders GROUP BY region ORDER BY region`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Rows[0][0] != "eu" || res.Rows[0][1] != float64(30) || res.Rows[1][1] != float64(5) {
		t.Fatalf("rows = %v", res.Rows)
	}
	for _, query := range []string{
		`SELECT * FROM main.messages`,
		`SELECT count(*) FROM main.messages`,
		`SELECT count(*) FROM messages`,
		`WITH messages AS (SELECT 1) SELECT count(*) FROM messages`,
		`SELECT count(*) FROM sqlite_master`,
		`INSERT INTO imported_tbl_1 VALUES ('x', 1)`,
		`SELECT 1; SELECT 2`,
	} {
		_, err := guardedRead(db, query)
		if !errors.Is(err, ErrGuardedReadRefused) {
			t.Errorf("%s: err = %v, want a refusal", query, err)
		}
	}
	// A bare name that is nothing in the database is a CTE and reads freely.
	if res, err := guardedRead(db, `WITH big AS (SELECT * FROM orders WHERE total > 5) SELECT count(*) FROM big`); err != nil || res.Rows[0][0] != int64(2) {
		t.Fatalf("CTE count = %v, %v", res, err)
	}
}

// The controls live on a pooled connection for one query. With a pool of one
// connection, anything left behind would break the statements that follow.
func TestGuardedReadLeavesTheConnectionAsItFoundIt(t *testing.T) {
	db := openGuardTestDB(t)
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		`SELECT * FROM orders`,
		`DROP TABLE imported_tbl_1`,
		`SELECT * FROM main.messages`,
		`SELECT nonsense FROM`,
	} {
		_, _ = guardedRead(db, query)
	}
	_, err := RunGuardedSQLiteRead(context.Background(), db, GuardedRead{
		SQL:     `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT count(*) FROM n`,
		Tables:  map[string]string{"orders": "imported_tbl_1"},
		MaxRows: 10,
		Timeout: 50 * time.Millisecond,
	})
	if !errors.Is(err, ErrGuardedReadTimeout) {
		t.Fatalf("endless query: err = %v, want a timeout", err)
	}

	if _, err := db.Exec(`INSERT INTO messages VALUES (2, 'still writable')`); err != nil {
		t.Fatalf("write after guarded reads: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE later (a)`); err != nil {
		t.Fatalf("DDL after guarded reads: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM messages`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("internal table after guarded reads: %d, %v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_temp_master`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("temp objects left behind: %d, %v", n, err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE; ROLLBACK`); err != nil {
		t.Fatalf("a transaction was left open: %v", err)
	}
}
