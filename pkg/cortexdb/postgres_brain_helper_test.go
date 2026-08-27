package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// openPostgresBrain gives a test its own brain on PostgreSQL, or skips it.
//
// Its own SCHEMA, not just its own tables. `go test ./...` runs packages in
// parallel against one database, and CREATE EXTENSION / CREATE TABLE IF NOT
// EXISTS are not atomic in PostgreSQL: two packages initialising at the same
// moment race, and one loses with "duplicate key value violates unique
// constraint pg_type_typname_nsp_index" — an error that names nothing a reader
// could act on. public stays in the search_path so the vector type resolves.
func openPostgresBrain(t *testing.T, dims int) *DB {
	t.Helper()
	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset — PostgreSQL is NOT covered by this run")
	}
	ctx := context.Background()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		admin.Close()
		t.Fatalf("extension: %v", err)
	}
	schema := fmt.Sprintf("brain_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	cfg := DefaultConfig(dsn + sep + "search_path=" + schema + ",public")
	cfg.Dimensions = dims
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open on a postgres DSN: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
