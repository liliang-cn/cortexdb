// Package pgtest gives a test its own PostgreSQL schema.
//
// `go test ./...` runs package binaries in parallel, and every PostgreSQL test
// in this module reads one DSN from CORTEXDB_TEST_POSTGRES. Sharing a schema
// across them is the arrangement that produces failures nobody can reproduce:
// one package's DROP TABLE ... CASCADE lands while another is mid-test, and the
// second fails naming a table it never wrote. Re-running that package alone
// passes, which is the worst way for a defect to present — it reads as
// flakiness in the test rather than as a shared resource nobody claimed.
//
// Two packages already worked around it by hand. This is that workaround, in
// one place, so a new PostgreSQL test gets the isolation by default rather than
// by remembering.
package pgtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// EnvDSN is the environment variable every PostgreSQL test opts in through.
const EnvDSN = "CORTEXDB_TEST_POSTGRES"

// extensionLock serialises CREATE EXTENSION across packages.
//
// CREATE EXTENSION IF NOT EXISTS is not atomic: two connections running it at
// the same moment race inside the catalogue and one loses with "duplicate key
// value violates unique constraint pg_type_typname_nsp_index", which names
// nothing a reader could act on. IF NOT EXISTS reads as protection against
// exactly this and is not.
const extensionLock = 0x0C0DE_DB

// extensions are what this module's tests need present, created here rather
// than assumed.
//
// pg_trgm was the assumption that cost the most: nothing in the module creates
// it, and the trigram index in the lexical tests only ever worked because some
// earlier run had installed it by hand. The database looked healthy, the tests
// passed, and the dependency was invisible until a database without it — a
// fresh container, or one whose public schema had been reset — failed with
// `operator class "gin_trgm_ops" does not exist`, which names the index and not
// the missing extension.
var extensions = []string{"vector", "pg_trgm"}

// DSN returns a DSN scoped to a schema created for this test, and "" when
// EnvDSN is unset — a caller that gets "" should skip, and say in the skip
// message what is therefore not covered.
//
// The schema is named for the caller and dropped when the test ends. public
// stays on the search_path so the vector type still resolves: the extension is
// installed once, in the shared schema, under a lock.
func DSN(t *testing.T, prefix string) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(EnvDSN))
	if raw == "" {
		return ""
	}
	ctx := context.Background()

	admin, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatalf("pgtest: open %s: %v", EnvDSN, err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("pgtest: ping: %v", err)
	}

	if _, err := admin.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, extensionLock); err != nil {
		admin.Close()
		t.Fatalf("pgtest: lock: %v", err)
	}
	var extErr error
	for _, ext := range extensions {
		if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS `+ext); err != nil {
			extErr = fmt.Errorf("%s: %w", ext, err)
			break
		}
	}
	if _, err := admin.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, extensionLock); err != nil {
		admin.Close()
		t.Fatalf("pgtest: unlock: %v", err)
	}
	if extErr != nil {
		admin.Close()
		t.Fatalf("pgtest: create extension: %v", extErr)
	}

	schema := fmt.Sprintf("%s_test_%d", sanitize(prefix), testname.Nano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("pgtest: create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})

	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + "search_path=" + schema + ",public"
}

// Open is DSN with the connection already made. It returns nil when EnvDSN is
// unset, so a caller can branch on that alone.
func Open(t *testing.T, prefix string) *sql.DB {
	t.Helper()
	dsn := DSN(t, prefix)
	if dsn == "" {
		return nil
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("pgtest: open scoped: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// sanitize keeps a caller's prefix to what can go in an unquoted identifier.
// The prefix is written by whoever adds a test, not by input, so this is here
// to turn a typo into a readable schema name rather than a syntax error.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "pgtest"
	}
	return b.String()
}

// Database gives the calling test a PostgreSQL database of its own, and returns
// its DSN — "" when EnvDSN is unset.
//
// A schema is enough to isolate tables; it is not enough for anything
// database-wide. CREATE and DROP EXTENSION are database-wide: a test that
// removes an extension to see what happens without it would strip the type off
// every other package's tables mid-run. No extension is installed here, because
// what such a test is usually studying is the installing.
func Database(t *testing.T, prefix string) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(EnvDSN))
	if raw == "" {
		return ""
	}
	ctx := context.Background()

	admin, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatalf("pgtest: open %s: %v", EnvDSN, err)
	}
	name := fmt.Sprintf("%s_db_%d", sanitize(prefix), testname.Nano())
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Fatalf("pgtest: create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
		admin.Close()
	})
	return swapDatabase(raw, name)
}

// swapDatabase replaces the database in a DSN, keeping the query string.
func swapDatabase(dsn, name string) string {
	i := strings.Index(dsn, "://")
	if i < 0 {
		return dsn + " dbname=" + name
	}
	rest := dsn[i+3:]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return dsn + "/" + name
	}
	tail := ""
	if q := strings.Index(rest[slash:], "?"); q >= 0 {
		tail = rest[slash+q:]
	}
	return dsn[:i+3] + rest[:slash] + "/" + name + tail
}
