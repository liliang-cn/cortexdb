package sqldialect_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/pgtest"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// The defect, stated as a test: many connections creating one extension at
// once, on a database that does not have it yet.
//
// It runs in a database of its own because CREATE EXTENSION is database-wide,
// and because the race only exists while the extension is absent — which is
// why it almost never fired in a suite whose database had been set up once by
// hand, and fired reliably the moment someone started from a clean one.
func TestConcurrentCreationDoesNotFail(t *testing.T) {
	dsn := pgtest.Database(t, "ensure_ext")
	if dsn == "" {
		t.Skip(pgtest.EnvDSN + " unset — the CREATE EXTENSION race is NOT covered by this run")
	}
	ctx := context.Background()

	const racers = 12
	errs := make(chan error, racers)
	var wg, start sync.WaitGroup
	start.Add(1)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			start.Wait() // all of them past the guard together
			errs <- sqldialect.EnsureExtension(ctx, db, "vector")
		}()
	}
	start.Done()
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("EnsureExtension lost the race: %v", err)
		}
	}

	// And it really is installed, rather than every caller having quietly
	// decided the failure was somebody else's success.
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pg_extension WHERE extname = 'vector'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("vector extension installed %d times, want 1", n)
	}
}

// A refusal must still be a refusal: succeeding on any error would turn "this
// account may not install extensions" into a store that starts and then fails
// at the first query.
func TestARealFailureIsStillReported(t *testing.T) {
	dsn := pgtest.Database(t, "ensure_missing")
	if dsn == "" {
		t.Skip(pgtest.EnvDSN + " unset — the failure path is NOT covered by this run")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	err = sqldialect.EnsureExtension(context.Background(), db, "no_such_extension_exists")
	if err == nil {
		t.Fatal("an extension that cannot be installed must report the error, not be assumed present")
	}
}
