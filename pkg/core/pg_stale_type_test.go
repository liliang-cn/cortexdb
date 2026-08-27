package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestIsStaleTypeCacheIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not a pg error", errors.New("cache lookup failed for type 91164"), false},
		{"the parameter-side failure", &pgconn.PgError{
			Code: "XX000", Message: "cache lookup failed for type 91164"}, true},
		{"the result-side failure", &pgconn.PgError{
			Code: "0A000", Message: "cached plan must not change result type"}, true},
		{"wrapped", fmt.Errorf("search graphrag seeds: %w",
			&pgconn.PgError{Code: "XX000", Message: "cache lookup failed for type 91164"}), true},
		// XX000 is PostgreSQL's catch-all internal error and 0A000 covers
		// every unsupported feature; matching on the code alone would swallow
		// unrelated failures that have to reach the caller.
		{"another XX000", &pgconn.PgError{
			Code: "XX000", Message: "could not open file: No such file or directory"}, false},
		{"another 0A000", &pgconn.PgError{
			Code: "0A000", Message: "cannot insert into view"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsStaleTypeCache(tc.err); got != tc.want {
				t.Fatalf("IsStaleTypeCache(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRetryOnStaleTypeCacheTriesOnceMore(t *testing.T) {
	stale := &pgconn.PgError{Code: "XX000", Message: "cache lookup failed for type 91164"}

	calls := 0
	if err := retryOnStaleTypeCache(func() error {
		calls++
		if calls == 1 {
			return stale
		}
		return nil
	}); err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}

	// Once, not in a loop: the first failure is what clears the cache, so a
	// second one means something else and must be reported rather than spun on.
	calls = 0
	err := retryOnStaleTypeCache(func() error { calls++; return stale })
	if !errors.Is(err, error(stale)) {
		t.Fatalf("expected the second failure to be returned, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want exactly 2", calls)
	}

	calls = 0
	other := errors.New("something else")
	if err := retryOnStaleTypeCache(func() error { calls++; return other }); !errors.Is(err, other) {
		t.Fatalf("unrelated error should pass straight through, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("unrelated error must not be retried, calls = %d", calls)
	}
}

// The real event: the pgvector extension replaced under a live connection.
//
// This is what took a search down mid-run and reported an integer. Reproduced
// rather than simulated, because the interesting part is not the error value —
// it is that the connection is holding a cached statement naming the old OID,
// which no fake can arrange.
//
// It runs in a database of its own. `DROP EXTENSION vector` is database-wide,
// so doing it in the shared test database would strip the vector column off
// every other package's tables mid-run.
func TestSearchSurvivesTheVectorTypeBeingReplaced(t *testing.T) {
	base := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if base == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset — recovery from a replaced vector type is NOT covered by this run")
	}
	ctx := context.Background()

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	defer admin.Close()

	dbName := fmt.Sprintf("cortexdb_staletype_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+dbName); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`)
	})

	dsn := swapDatabase(base, dbName)
	store, err := OpenBrainStore(dsn, Config{Path: dsn, VectorDim: 4, SimilarityFn: CosineSimilarity})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := store.Upsert(ctx, &Embedding{
		ID: "e1", Content: "ledger-svc", Vector: []float32{1, 0, 0, 0},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Warms this connection's statement cache with a descriptor naming the
	// current vector OID.
	if hits, err := store.Search(ctx, []float32{1, 0, 0, 0}, SearchOptions{TopK: 1}); err != nil {
		t.Fatalf("warm-up search: %v", err)
	} else if len(hits) != 1 {
		t.Fatalf("warm-up search returned %d hits, want 1", len(hits))
	}

	// The event. A second connection replaces the type; CASCADE takes the
	// vector column with it, so the column is put back too — an extension
	// reinstall that a careful operator would follow with a restore.
	side, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open side connection: %v", err)
	}
	defer side.Close()
	for _, stmt := range []string{
		`DROP EXTENSION vector CASCADE`,
		`CREATE EXTENSION vector`,
		`ALTER TABLE embeddings ADD COLUMN vector vector(4)`,
		`UPDATE embeddings SET vector = '[1,0,0,0]' WHERE id = 'e1'`,
	} {
		if _, err := side.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("replace the type (%s): %v", stmt, err)
		}
	}

	// The same search, on a pool still holding the stale descriptor. Without
	// the retry this is "cache lookup failed for type N" and the caller sees
	// an integer.
	hits, err := store.Search(ctx, []float32{1, 0, 0, 0}, SearchOptions{TopK: 1})
	if err != nil {
		if IsStaleTypeCache(err) {
			t.Fatalf("the stale-type failure reached the caller — the retry did not run: %v", err)
		}
		t.Fatalf("search after the type was replaced: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "e1" {
		t.Fatalf("search returned %d hits, want the one row back", len(hits))
	}
}

// swapDatabase points a libpq/pgx URL at another database on the same server.
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
