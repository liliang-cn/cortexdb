package sqldialect

import (
	"context"
	"database/sql"
	"fmt"
)

// Creating a PostgreSQL extension when something else may be creating it too.
//
// CREATE EXTENSION IF NOT EXISTS reads as protection against a concurrent
// creation and is not. The guard and the insert are not one atomic step, so two
// connections running it at the same moment both pass the guard and one loses
// in the catalogue with "duplicate key value violates unique constraint
// pg_type_typname_nsp_index" — an error that names a system index and nothing
// the reader did.
//
// This is reachable in production, not only under a parallel test run: opening
// two brains on one database at once is an ordinary thing to do, and every one
// of them initialises its extensions on the way up. What it cost in each place
// was different and none of it was correct. The store's Init returned "pgvector
// is required by the PostgreSQL store" and refused to start. The graph's
// initPgVector reported "pgvector unavailable" and spent the rest of that
// store's life doing exact scans, having recorded a momentary race as a
// deployment fact. The lexical DDL lost its trigram index and left CJK search
// linear, silently, which is the one nobody would have noticed.

// Execer is the part of *sql.DB or *sql.Tx this needs.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// EnsureExtension creates a PostgreSQL extension if it is absent, and succeeds
// when someone else created it first.
//
// The race is not distinguished by its error text or SQLSTATE — a lost race and
// a genuine refusal are told apart by asking the catalogue afterwards, which is
// the only question that actually matters and the only one whose answer does
// not depend on a server's wording or version. A managed instance that refuses
// CREATE EXTENSION to this account still returns an error, because there the
// extension really is absent.
func EnsureExtension(ctx context.Context, db Execer, name string) error {
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS `+name); err != nil {
		if installed(ctx, db, name) {
			return nil
		}
		return fmt.Errorf("create extension %s: %w", name, err)
	}
	return nil
}

func installed(ctx context.Context, db Execer, name string) bool {
	var one int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM pg_extension WHERE extname = $1`, name).Scan(&one)
	return err == nil && one == 1
}
