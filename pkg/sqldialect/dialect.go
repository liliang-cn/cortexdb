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
	"fmt"
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

	// JSONText reads a top-level string field out of a JSON column.
	//
	// The fourth thing the two databases genuinely disagree about, and the one
	// that keeps a whole feature from crossing: pkg/graphflow stores a
	// temporal fact's validity inside an edge's `properties` JSON and reads it
	// back with json_extract, which PostgreSQL does not have. Written once
	// here, the same query works on both.
	JSONText(column, key string) string

	// JSONTextGuarded reads a top-level string field and yields NULL when the
	// column holds no JSON at all.
	//
	// The guard is not decoration. `properties` is a TEXT column and an edge
	// written without any carries the empty string; SQLite's json_extract
	// raises "malformed JSON" on it and PostgreSQL's ::jsonb raises "invalid
	// input syntax". Both fail the whole query over one such row, so every
	// call site used to wrap the read in `CASE WHEN json_valid(...)`, which is
	// SQLite-only syntax and the reason graph retrieval did not run on
	// PostgreSQL at all.
	//
	// What the two guards test is not identical: SQLite asks whether the text
	// parses, PostgreSQL only whether it is non-empty. They coincide for every
	// row this codebase can write — properties come from json.Marshal — and a
	// genuinely malformed row is corruption that should surface as an error
	// rather than be silently read as NULL.
	JSONTextGuarded(column, key string) string

	// JSONFlag reads a boolean-ish field as 1 or 0, guarded the same way.
	//
	// Separate from JSONTextGuarded because the two databases disagree about
	// what a JSON `true` reads back as: SQLite's json_extract gives the
	// integer 1, PostgreSQL's ->> gives the text 'true'. A call site comparing
	// the raw read against 1 is correct on SQLite and quietly false on
	// PostgreSQL — inferred edges would have looked explicit, and every
	// inference rule would have re-derived them on top of themselves.
	JSONFlag(column, key string) string

	// JSONArrayContains tests whether a JSON array field contains a value,
	// as an expression carrying exactly one `?` placeholder for it.
	//
	// SQLite reaches for json_each and a correlated subquery; PostgreSQL has
	// a containment operator. Neither spelling survives on the other, and the
	// SQLite one failed on PostgreSQL with "syntax error at end of input" —
	// an error that names nothing, because the parser gave up at `json_each`.
	JSONArrayContains(column, key string) string

	// AutoIncrementPK is the column definition for a surrogate integer key
	// the database assigns.
	//
	// SQLite spells it INTEGER PRIMARY KEY AUTOINCREMENT, which PostgreSQL
	// rejects at the parser. The one place this appears is the ontology action
	// audit table, created lazily on the first ontology_action_apply — so on
	// PostgreSQL that DDL failed, the table never existed, and every action
	// apply failed with it. Nothing caught it because the action tests run on
	// SQLite and the PostgreSQL tool coverage only listed action types.
	AutoIncrementPK() string

	// JSONSet writes a JSON value into a top-level field, as an expression
	// carrying one `?` placeholder for the new value (itself JSON text).
	//
	// SQLite's json_set and PostgreSQL's jsonb_set differ in name, in how the
	// path is spelled, and in whether the result needs casting back to text.
	JSONSet(column, key string) string

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

// jsonKey makes a key safe to embed in a SQL string literal.
//
// Every caller passes a constant today, which is exactly when this is easy to
// leave out and hard to notice missing later.
func jsonKey(key string) string {
	return strings.ReplaceAll(key, "'", "''")
}

type sqliteDialect struct{}

func (sqliteDialect) Kind() Kind { return SQLite }

func (sqliteDialect) JSONText(column, key string) string {
	return fmt.Sprintf("json_extract(%s, '$.%s')", column, jsonKey(key))
}
func (sqliteDialect) JSONTextGuarded(column, key string) string {
	return fmt.Sprintf("CASE WHEN json_valid(%s) = 1 THEN json_extract(%s, '$.%s') ELSE NULL END",
		column, column, jsonKey(key))
}

func (sqliteDialect) JSONFlag(column, key string) string {
	return fmt.Sprintf(
		"CASE WHEN json_valid(%s) = 1 AND json_extract(%s, '$.%s') IN (1, 'true') THEN 1 ELSE 0 END",
		column, column, jsonKey(key))
}

func (sqliteDialect) JSONArrayContains(column, key string) string {
	return fmt.Sprintf(
		"EXISTS (SELECT 1 FROM json_each(json_extract(%s, '$.%s')) je WHERE je.value = ?)",
		column, jsonKey(key))
}

func (sqliteDialect) AutoIncrementPK() string {
	return "INTEGER PRIMARY KEY AUTOINCREMENT"
}

func (sqliteDialect) JSONSet(column, key string) string {
	return fmt.Sprintf("json_set(%s, '$.%s', json(?))", column, jsonKey(key))
}

func (sqliteDialect) Rebind(q string) string { return q }
func (sqliteDialect) BlobType() string       { return "BLOB" }
func (sqliteDialect) BoolType() string       { return "INTEGER" }
func (sqliteDialect) IsDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
}

type postgresDialect struct{}

func (postgresDialect) Kind() Kind       { return Postgres }
func (postgresDialect) BlobType() string { return "BYTEA" }

// ->> rather than -> so the result is text and not a quoted JSON scalar.
//
// The ::text before NULLIF is not redundant. These helpers are pointed at two
// different column types: graph_nodes.properties is TEXT on both backends,
// while messages.metadata is TEXT on SQLite and jsonb on PostgreSQL. NULLIF of
// a jsonb against ” is an error, not a mismatch — and the one caller that hit
// it discards its errors by design, so recall accounting on PostgreSQL wrote
// nothing and reported nothing for as long as it existed. Casting to text
// first makes the helper indifferent to which of the two it was given.
//
// NULLIF guards the cast. A row whose properties column is the empty string —
// which is what an edge written without any gets — makes ”::jsonb an error in
// PostgreSQL, while SQLite's json_extract(”) quietly returns NULL. Without
// this, one such row anywhere in the table fails the whole query, and the
// error ("invalid input syntax for type json") names neither the column nor
// the row.
func (postgresDialect) JSONText(column, key string) string {
	return fmt.Sprintf("(NULLIF(%s::text, '')::jsonb ->> '%s')", column, jsonKey(key))
}

func (postgresDialect) JSONTextGuarded(column, key string) string {
	return fmt.Sprintf("CASE WHEN NULLIF(%s::text, '') IS NOT NULL THEN (NULLIF(%s::text, '')::jsonb ->> '%s') ELSE NULL END",
		column, column, jsonKey(key))
}

func (postgresDialect) JSONFlag(column, key string) string {
	return fmt.Sprintf(
		"CASE WHEN NULLIF(%s::text, '') IS NOT NULL AND (NULLIF(%s::text, '')::jsonb ->> '%s') IN ('true', '1') THEN 1 ELSE 0 END",
		column, column, jsonKey(key))
}

func (postgresDialect) JSONArrayContains(column, key string) string {
	// to_jsonb(?::text) rather than a bare literal: the argument arrives as a
	// Go string, and @> on jsonb needs a jsonb operand, not text.
	return fmt.Sprintf(
		"(NULLIF(%s::text, '') IS NOT NULL AND (NULLIF(%s::text, '')::jsonb -> '%s') @> to_jsonb(?::text))",
		column, column, jsonKey(key))
}

func (postgresDialect) AutoIncrementPK() string {
	return "BIGSERIAL PRIMARY KEY"
}

func (postgresDialect) JSONSet(column, key string) string {
	// COALESCE, because jsonb_set of NULL is NULL: a row whose properties are
	// empty would silently lose the field being written rather than gain it.
	//
	// The result is left as jsonb rather than cast back to text, because the
	// two columns this is aimed at have different types on PostgreSQL —
	// graph_nodes.properties is TEXT, messages.metadata is jsonb — and only
	// one direction has an assignment cast. jsonb into a text column is
	// allowed; text into a jsonb column is not, and it failed with "column is
	// of type jsonb but expression is of type text" at the one call site that
	// discards its errors, so recall accounting on PostgreSQL wrote nothing
	// and reported nothing.
	return fmt.Sprintf("jsonb_set(COALESCE(NULLIF(%s::text, '')::jsonb, '{}'::jsonb), '{%s}', ?::jsonb)",
		column, jsonKey(key))
}

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
