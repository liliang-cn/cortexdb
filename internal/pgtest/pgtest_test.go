package pgtest

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
)

func skipUnlessPostgres(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(EnvDSN)) == "" {
		t.Skip(EnvDSN + " unset — the isolation this package provides is NOT covered by this run")
	}
}

// The property the whole package exists for: one caller's tables are invisible
// to another's, so a DROP cannot reach across.
func TestTwoCallersCannotSeeEachOthersTables(t *testing.T) {
	skipUnlessPostgres(t)
	ctx := context.Background()

	a, b := Open(t, "iso"), Open(t, "iso")
	if _, err := a.ExecContext(ctx, `CREATE TABLE shared_name (id int)`); err != nil {
		t.Fatalf("a create: %v", err)
	}
	// Same unqualified name, and it must not collide.
	if _, err := b.ExecContext(ctx, `CREATE TABLE shared_name (id int)`); err != nil {
		t.Fatalf("b create: %v — the two callers are sharing a schema", err)
	}

	if _, err := a.ExecContext(ctx, `INSERT INTO shared_name VALUES (1)`); err != nil {
		t.Fatalf("a insert: %v", err)
	}
	var n int
	if err := b.QueryRowContext(ctx, `SELECT COUNT(*) FROM shared_name`).Scan(&n); err != nil {
		t.Fatalf("b count: %v", err)
	}
	if n != 0 {
		t.Errorf("b sees %d rows a wrote — the schemas are not separate", n)
	}

	// And the failure this replaces: a DROP from one must leave the other's
	// table standing.
	if _, err := b.ExecContext(ctx, `DROP TABLE shared_name CASCADE`); err != nil {
		t.Fatalf("b drop: %v", err)
	}
	if err := a.QueryRowContext(ctx, `SELECT COUNT(*) FROM shared_name`).Scan(&n); err != nil {
		t.Fatalf("a's table did not survive b's DROP: %v", err)
	}
	if n != 1 {
		t.Errorf("a's row count = %d, want 1", n)
	}
}

// public stays on the search_path, or the vector type does not resolve and
// every store this helper hands out fails at schema creation.
func TestTheVectorTypeStillResolves(t *testing.T) {
	skipUnlessPostgres(t)
	db := Open(t, "vec")
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE has_vector (id int, v vector(4))`); err != nil {
		t.Fatalf("vector type unavailable in a scoped schema: %v", err)
	}
}

// CREATE EXTENSION IF NOT EXISTS races itself; the lock is what makes many
// callers starting at once safe. Without it this is the "duplicate key value
// violates unique constraint pg_type_typname_nsp_index" that names nothing.
func TestConcurrentCallersDoNotRaceOnTheExtension(t *testing.T) {
	skipUnlessPostgres(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// t.Fatalf is not legal off the test goroutine; DSN uses it, so a
			// failure here surfaces as the test binary failing, which is what
			// we want and is why this asserts nothing itself.
			_ = DSN(t, "race")
		}()
	}
	wg.Wait()
}

// A schema is dropped when its test ends, or a long run leaves the database
// full of them.
func TestTheSchemaIsGoneAfterwards(t *testing.T) {
	skipUnlessPostgres(t)
	ctx := context.Background()
	admin := Open(t, "observer")

	var schema string
	t.Run("inner", func(t *testing.T) {
		dsn := DSN(t, "ephemeral")
		i := strings.Index(dsn, "search_path=")
		schema = strings.SplitN(dsn[i+len("search_path="):], ",", 2)[0]
	})

	var found int
	if err := admin.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = $1`, schema).Scan(&found); err != nil {
		t.Fatalf("look for %s: %v", schema, err)
	}
	if found != 0 {
		t.Errorf("schema %s outlived its test", schema)
	}
}
