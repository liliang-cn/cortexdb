package agentmem

// Every statement this package runs, spelled for the database it is running on.
//
// agentmem writes its ~90 queries with `?`, which is SQLite's spelling and a
// syntax error in PostgreSQL. Same shape as pkg/graph and pkg/cortexdb: the
// SQL stays where it is and stays readable, and one layer rebinds on the way
// past. Not a query builder.

import (
	"context"
	"database/sql"
)

func (s *Store) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.dialect.Rebind(q), args...)
}

func (s *Store) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.dialect.Rebind(q), args...)
}

func (s *Store) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.dialect.Rebind(q), args...)
}

// The same inside a transaction the caller opened.
func (s *Store) txExec(ctx context.Context, tx *sql.Tx, q string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, s.dialect.Rebind(q), args...)
}

func (s *Store) txQueryRow(ctx context.Context, tx *sql.Tx, q string, args ...any) *sql.Row {
	return tx.QueryRowContext(ctx, s.dialect.Rebind(q), args...)
}

// txPrepare is the same for a statement prepared once and executed many times.
//
// The one that is easy to miss, and expensive when it is: rebinding exec and
// query but leaving PrepareContext alone means every single-row write works on
// PostgreSQL and every *batched* one fails, which reads as something far
// stranger than a missing rebind. agentmem prepares in two places —
// replaceStringSet and replaceRevisions — so a memory with no tags saves fine
// and a memory with tags does not.
func (s *Store) txPrepare(ctx context.Context, tx *sql.Tx, q string) (*sql.Stmt, error) {
	return tx.PrepareContext(ctx, s.dialect.Rebind(q))
}
