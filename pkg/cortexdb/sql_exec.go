package cortexdb

// Every query this package runs, spelled for the database it is running on.
//
// pkg/cortexdb builds a lot of SQL directly against the shared handle —
// ontology storage, identity resolution, re-embedding, text search — all of it
// written with `?` placeholders, which is SQLite's spelling and a syntax error
// in PostgreSQL. The failure is not subtle when it happens (`syntax error at
// or near ","`), but it happens at the first query of the first ingest, which
// is to say: the whole package worked on one backend and none of it on the
// other.
//
// The same shape pkg/graph uses: the queries stay where they are and stay
// readable, and one layer rebinds on the way past. Not a query builder.

import (
	"context"
	"database/sql"
)

// exec, query and queryRow run a statement in this DB's placeholder style.
func (db *DB) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return db.SQL().ExecContext(ctx, db.Dialect().Rebind(q), args...)
}

func (db *DB) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return db.SQL().QueryContext(ctx, db.Dialect().Rebind(q), args...)
}

func (db *DB) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return db.SQL().QueryRowContext(ctx, db.Dialect().Rebind(q), args...)
}

// The same inside a transaction the caller opened.
func (db *DB) txExec(ctx context.Context, tx *sql.Tx, q string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, db.Dialect().Rebind(q), args...)
}

func (db *DB) txQuery(ctx context.Context, tx *sql.Tx, q string, args ...any) (*sql.Rows, error) {
	return tx.QueryContext(ctx, db.Dialect().Rebind(q), args...)
}

// Two narrow interfaces rather than one wide one, because the package already
// has callers that can only do half — graphStringQuerier reads rows and
// ontologyRowQuerier reads a row, and neither should have to grow a method it
// has no use for just to be rebound.
type queryContexter interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type queryRowContexter interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (db *DB) querierQuery(ctx context.Context, q queryContexter, query string, args ...any) (*sql.Rows, error) {
	return q.QueryContext(ctx, db.Dialect().Rebind(query), args...)
}

func (db *DB) querierQueryRow(ctx context.Context, q queryRowContexter, query string, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, db.Dialect().Rebind(query), args...)
}
