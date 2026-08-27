// Package sqldialect is the thin layer that lets one body of SQL run on both
// SQLite and PostgreSQL.
//
// CortexDB's storage is SQLite: fast, embedded, no daemon, which is exactly
// right for a brain that lives on one machine. It is also exactly wrong for the
// other place this belongs — a deployment someone has to procure, audit, back
// up and replicate. Those places run PostgreSQL, and a bespoke engine is not
// something a bank installs no matter how good it is.
//
// So both, chosen by DSN. What stands between them is smaller than it looks:
// a survey of pkg/graph found no SQLite-only syntax at all — no INSERT OR
// REPLACE, no AUTOINCREMENT, no PRAGMA, no json_extract, no FTS5. The queries
// already use ON CONFLICT and RETURNING, which are standard. What actually
// differs is this file: how a parameter is spelled, what a byte string is
// called, and what the database says when a column is already there.
//
// Deliberately not a query builder. The SQL stays readable and stays in the
// files that use it; this only translates the handful of things that cannot be
// written once.
package sqldialect

import (
	"strings"
)

// Kind names a supported database.
type Kind string

const (
	SQLite   Kind = "sqlite"
	Postgres Kind = "postgres"
)

// Dialect is the per-database half of a query.
type Dialect interface {
	// Kind names the database, for logs, errors and capability decisions.
	Kind() Kind

	// Rebind turns `?` placeholders into whatever this database expects.
	// SQLite returns the query untouched; PostgreSQL numbers them $1, $2, …
	Rebind(query string) string

	// BlobType is the column type for opaque bytes — a serialized vector.
	BlobType() string

	// IsDuplicateColumn reports whether err is "this column already exists",
	// which an idempotent ALTER TABLE ADD COLUMN must swallow.
	//
	// A method rather than a string match at the call site: SQLite says
	// "duplicate column name: x" and PostgreSQL says `column "x" of relation
	// "y" already exists`. The original code matched the SQLite wording
	// inline, so the same migration would have failed on its second start
	// against PostgreSQL — the kind of thing that only shows up on the second
	// start, in production.
	IsDuplicateColumn(err error) bool
}

// For returns the dialect for a kind, defaulting to SQLite.
func For(kind Kind) Dialect {
	if kind == Postgres {
		return postgresDialect{}
	}
	return sqliteDialect{}
}

// KindForDSN reads the database out of a connection string.
//
// Anything that is not recognisably a PostgreSQL URL is a SQLite path, because
// that is what a bare path has always meant here and an existing config must
// keep working.
func KindForDSN(dsn string) Kind {
	s := strings.ToLower(strings.TrimSpace(dsn))
	switch {
	case strings.HasPrefix(s, "postgres://"), strings.HasPrefix(s, "postgresql://"):
		return Postgres
	case strings.Contains(s, "host=") && strings.Contains(s, "dbname="):
		// libpq keyword/value form, which pgx also accepts.
		return Postgres
	default:
		return SQLite
	}
}

type sqliteDialect struct{}

func (sqliteDialect) Kind() Kind             { return SQLite }
func (sqliteDialect) Rebind(q string) string { return q }
func (sqliteDialect) BlobType() string       { return "BLOB" }
func (sqliteDialect) BoolType() string       { return "INTEGER" }
func (sqliteDialect) IsDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
}

type postgresDialect struct{}

func (postgresDialect) Kind() Kind       { return Postgres }
func (postgresDialect) BlobType() string { return "BYTEA" }

func (postgresDialect) IsDuplicateColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// The SQLSTATE for duplicate_column is 42701; the text is checked too so
	// this works whether or not the driver surfaces a *pgconn.PgError.
	return strings.Contains(msg, "42701") || strings.Contains(msg, "already exists")
}

// Rebind numbers the placeholders, leaving anything inside a string literal
// alone.
//
// A naive replacement corrupts a query whose text contains a question mark —
// `WHERE label = 'why?'` becomes `'why$1'` and the argument count no longer
// matches. Quotes are cheap to track and getting this wrong produces a
// mismatch that only fires on the rows that happen to contain the character.
func (postgresDialect) Rebind(q string) string {
	var b strings.Builder
	b.Grow(len(q) + 8)

	n := 0
	inSingle, inDouble := false, false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'' && !inDouble:
			// '' inside a literal is an escaped quote, not the end of one.
			if inSingle && i+1 < len(q) && q[i+1] == '\'' {
				b.WriteString("''")
				i++
				continue
			}
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '?' && !inSingle && !inDouble:
			n++
			b.WriteByte('$')
			b.WriteString(itoa(n))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// itoa without strconv, which would be the only import this file needs.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	var digits [8]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
