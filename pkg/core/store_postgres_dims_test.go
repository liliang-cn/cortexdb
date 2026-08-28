package core

// Dimension bookkeeping on PostgreSQL, against a real PostgreSQL.
//
// Opt-in through CORTEXDB_TEST_POSTGRES so `go test ./...` on a laptop with no
// database still passes, and loudly skipped when it is unset so nobody reads a
// green run as coverage it does not have.
//
//	docker run -d --name pgdims -e POSTGRES_PASSWORD=cortex -e POSTGRES_DB=cortex \
//	  -p 47833:5432 pgvector/pgvector:pg16
//	CORTEXDB_TEST_POSTGRES='postgres://postgres:cortex@localhost:47833/cortex?sslmode=disable' \
//	  go test ./pkg/core/ -run Postgres.*Dim -v
//
// Every test gets its own PostgreSQL schema, created up front and dropped
// afterwards. Other packages' suites run against the same database and create
// tables with the same names; a schema is what keeps them from being the same
// tables.

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// newPostgresDimStore returns an initialised store in a schema of its own.
// vectorDim 0 leaves the vector column unconstrained, which is what lets rows
// of different widths coexist; a positive one gives `vector(N)`.
func newPostgresDimStore(t *testing.T, vectorDim int) (*PostgresStore, *sql.DB) {
	t.Helper()
	raw := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if raw == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset — PostgreSQL dimension bookkeeping is NOT covered by this run")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("cortexdb_dims_%d", testname.Nano())

	admin, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	// The extension is database-wide and lands in public; every schema below
	// keeps public on its search_path so the `vector` type resolves.
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		admin.Close()
		t.Fatalf("create extension vector: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
		admin.Close()
	})

	db, err := sql.Open("pgx", pgDimsDSN(t, raw, schema))
	if err != nil {
		t.Fatalf("open scoped connection: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := DefaultConfig()
	cfg.VectorDim = vectorDim
	store := NewPostgresStore(db, cfg)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Init seeds collections(id=1,'default') with an explicit id, which does
	// not advance the SERIAL sequence — so the next CreateCollection would ask
	// for id 1 again and collide. Nudging the sequence here keeps these tests
	// about dimensions rather than about that; it is a real edge in
	// store_postgres.go, and not this file's to fix.
	if _, err := db.ExecContext(ctx, `
		SELECT setval(pg_get_serial_sequence('collections', 'id'),
		               GREATEST((SELECT max(id) FROM collections), 1))`); err != nil {
		t.Fatalf("advance collections sequence: %v", err)
	}
	return store, db
}

func pgDimsDSN(t *testing.T, raw, schema string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("CORTEXDB_TEST_POSTGRES is not a URL: %v", err)
	}
	q := u.Query()
	// public stays on the path for the vector type itself.
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	return u.String()
}

// seedCollection creates a collection with a declared dimension and returns it.
func seedCollection(t *testing.T, s *PostgresStore, name string, declared int) *Collection {
	t.Helper()
	c, err := s.CreateCollection(context.Background(), name, declared)
	if err != nil {
		t.Fatalf("CreateCollection(%q, %d): %v", name, declared, err)
	}
	return c
}

func vecOfDim(dim int, first float32) []float32 {
	v := make([]float32, dim)
	v[0] = first
	for i := 1; i < dim; i++ {
		v[i] = 0.1
	}
	return v
}

func mustUpsert(t *testing.T, s *PostgresStore, emb *Embedding) {
	t.Helper()
	if err := s.Upsert(context.Background(), emb); err != nil {
		t.Fatalf("Upsert(%s): %v", emb.ID, err)
	}
}

// A store filled by two different models holds two widths, and the report says
// so — per collection, smallest width first, with a collection that never
// declared a dimension counted but never blamed.
func TestPostgresDimensionReportGroupsByCollectionAndWidth(t *testing.T) {
	s, _ := newPostgresDimStore(t, 0)
	ctx := context.Background()

	mixed := seedCollection(t, s, "alpha_mixed", 4)
	undeclared := seedCollection(t, s, "beta_undeclared", 0)

	mustUpsert(t, s, &Embedding{ID: "m1", CollectionID: mixed.ID, Vector: vecOfDim(4, 1), Content: "four one"})
	mustUpsert(t, s, &Embedding{ID: "m2", CollectionID: mixed.ID, Vector: vecOfDim(4, 2), Content: "four two"})
	mustUpsert(t, s, &Embedding{ID: "m3", CollectionID: mixed.ID, Vector: vecOfDim(8, 3), Content: "eight, from the newer model"})
	mustUpsert(t, s, &Embedding{ID: "u1", CollectionID: undeclared.ID, Vector: vecOfDim(8, 4), Content: "nobody declared a width here"})

	report, err := s.DimensionReport(ctx)
	if err != nil {
		t.Fatalf("DimensionReport: %v", err)
	}
	if !report.NeedsRepair() {
		t.Fatal("NeedsRepair() = false, but one row is 8 wide in a collection declared 4")
	}
	if report.Mismatched != 1 {
		t.Errorf("total mismatched = %d, want 1", report.Mismatched)
	}
	if len(report.Collections) != 2 {
		t.Fatalf("report covers %d collections, want 2 (%+v)", len(report.Collections), report.Collections)
	}
	// ORDER BY name: alpha before beta.
	got := report.Collections[0]
	if got.Collection != "alpha_mixed" {
		t.Fatalf("first collection = %q, want alpha_mixed", got.Collection)
	}
	if got.Declared != 4 || got.Rows != 3 || got.Mismatched != 1 {
		t.Errorf("alpha_mixed = declared %d rows %d mismatched %d, want 4/3/1", got.Declared, got.Rows, got.Mismatched)
	}
	if len(got.Dimensions) != 2 || got.Dimensions[0].Dim != 4 || got.Dimensions[1].Dim != 8 {
		t.Errorf("alpha_mixed dimensions = %+v, want [{4 2} {8 1}] smallest first", got.Dimensions)
	}
	if got.RowsWithDim(4) != 2 || got.RowsWithDim(8) != 1 {
		t.Errorf("alpha_mixed RowsWithDim: 4 -> %d, 8 -> %d; want 2 and 1", got.RowsWithDim(4), got.RowsWithDim(8))
	}

	// Declared 0 means "never recorded". Its rows are counted, and none of
	// them is drift, because there is nothing for them to disagree with.
	undecl := report.Collections[1]
	if undecl.Collection != "beta_undeclared" {
		t.Fatalf("second collection = %q, want beta_undeclared", undecl.Collection)
	}
	if undecl.Rows != 1 {
		t.Errorf("beta_undeclared rows = %d, want 1", undecl.Rows)
	}
	if undecl.Mismatched != 0 {
		t.Errorf("beta_undeclared mismatched = %d, want 0 — an undeclared collection cannot be wrong", undecl.Mismatched)
	}
}

// The rows a repair pass needs: wrong width, with their text, newest first.
func TestPostgresMismatchedEmbeddingsFindsTheOldWidth(t *testing.T) {
	s, _ := newPostgresDimStore(t, 0)
	ctx := context.Background()

	c := seedCollection(t, s, "corpus", 8)
	mustUpsert(t, s, &Embedding{ID: "new1", CollectionID: c.ID, Vector: vecOfDim(8, 1), Content: "already the new width"})
	mustUpsert(t, s, &Embedding{ID: "old1", CollectionID: c.ID, Vector: vecOfDim(4, 1), Content: "written by the old model"})
	mustUpsert(t, s, &Embedding{ID: "old2", CollectionID: c.ID, Vector: vecOfDim(4, 2), Content: "also the old model"})
	// No text means nothing to re-embed, so it is not a candidate even though
	// its width is wrong — the same exclusion SQLite makes.
	mustUpsert(t, s, &Embedding{ID: "old3", CollectionID: c.ID, Vector: vecOfDim(4, 3), Content: ""})

	stale, err := s.MismatchedEmbeddings(ctx, 8, 0)
	if err != nil {
		t.Fatalf("MismatchedEmbeddings: %v", err)
	}
	if len(stale) != 2 {
		t.Fatalf("got %d candidates, want 2 (%s)", len(stale), embIDs(stale))
	}
	seen := map[string]bool{}
	for _, e := range stale {
		seen[e.ID] = true
		if e.Content == "" {
			t.Errorf("row %s came back without its text; a caller cannot re-embed that", e.ID)
		}
		if e.Collection != "corpus" {
			t.Errorf("row %s reports collection %q, want corpus", e.ID, e.Collection)
		}
	}
	if !seen["old1"] || !seen["old2"] {
		t.Errorf("candidates = %s, want old1 and old2", embIDs(stale))
	}
	if seen["new1"] {
		t.Error("a row of the wanted width was listed as needing repair")
	}
	if seen["old3"] {
		t.Error("an empty-content row was listed; there is no text to re-embed")
	}

	// limit caps the batch; 0 and below mean no cap.
	capped, err := s.MismatchedEmbeddings(ctx, 8, 1)
	if err != nil {
		t.Fatalf("MismatchedEmbeddings(limit 1): %v", err)
	}
	if len(capped) != 1 {
		t.Errorf("limit 1 returned %d rows", len(capped))
	}

	// Asking the other way round inverts the answer: against wantDim 4 it is
	// the 8-wide row that is stale. old3 stays out of both lists — its width
	// is wrong for 8 and right for 4, but it has no text either way.
	other, err := s.MismatchedEmbeddings(ctx, 4, 0)
	if err != nil {
		t.Fatalf("MismatchedEmbeddings(4): %v", err)
	}
	if len(other) != 1 || other[0].ID != "new1" {
		t.Errorf("wantDim 4 returned %s, want [new1]", embIDs(other))
	}

	if _, err := s.MismatchedEmbeddings(ctx, 0, 0); err == nil {
		t.Error("wantDim 0 should be refused: there is no such thing as a zero-width vector")
	}
}

func embIDs(embs []*Embedding) string {
	ids := make([]string, len(embs))
	for i, e := range embs {
		ids[i] = e.ID
	}
	return "[" + strings.Join(ids, " ") + "]"
}

// A collection's declared dimension is only corrected once every row agrees.
func TestPostgresReconcileCollectionDimensions(t *testing.T) {
	s, db := newPostgresDimStore(t, 0)
	ctx := context.Background()

	repaired := seedCollection(t, s, "repaired", 4) // all rows now 8: should move
	stillMixed := seedCollection(t, s, "still_mixed", 4)
	already := seedCollection(t, s, "already_right", 8)

	mustUpsert(t, s, &Embedding{ID: "r1", CollectionID: repaired.ID, Vector: vecOfDim(8, 1), Content: "re-embedded"})
	mustUpsert(t, s, &Embedding{ID: "r2", CollectionID: repaired.ID, Vector: vecOfDim(8, 2), Content: "re-embedded"})
	mustUpsert(t, s, &Embedding{ID: "x1", CollectionID: stillMixed.ID, Vector: vecOfDim(8, 1), Content: "half done"})
	mustUpsert(t, s, &Embedding{ID: "x2", CollectionID: stillMixed.ID, Vector: vecOfDim(4, 2), Content: "not yet"})
	mustUpsert(t, s, &Embedding{ID: "a1", CollectionID: already.ID, Vector: vecOfDim(8, 1), Content: "fine all along"})

	updated, err := s.ReconcileCollectionDimensions(ctx, 8)
	if err != nil {
		t.Fatalf("ReconcileCollectionDimensions: %v", err)
	}
	if updated != 1 {
		t.Errorf("updated %d collections, want 1 — only the uniformly-8 one had drifted metadata", updated)
	}

	assertDeclared(t, ctx, db, "repaired", 8)
	// Left alone on purpose: a collection that still holds two widths is not
	// repaired yet, and moving its declared dimension would hide that.
	assertDeclared(t, ctx, db, "still_mixed", 4)
	assertDeclared(t, ctx, db, "already_right", 8)

	// Idempotent: a second pass has nothing left to do.
	again, err := s.ReconcileCollectionDimensions(ctx, 8)
	if err != nil {
		t.Fatalf("second ReconcileCollectionDimensions: %v", err)
	}
	if again != 0 {
		t.Errorf("second pass updated %d collections, want 0", again)
	}

	if _, err := s.ReconcileCollectionDimensions(ctx, 0); err == nil {
		t.Error("dimension 0 should be refused")
	}

	// And the report agrees afterwards: the repaired collection is clean.
	report, err := s.DimensionReport(ctx)
	if err != nil {
		t.Fatalf("DimensionReport: %v", err)
	}
	for _, c := range report.Collections {
		if c.Collection == "repaired" && c.Mismatched != 0 {
			t.Errorf("repaired still reports %d mismatched rows after reconciling", c.Mismatched)
		}
	}
}

func assertDeclared(t *testing.T, ctx context.Context, db *sql.DB, name string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, `SELECT dimensions FROM collections WHERE name = $1`, name).Scan(&got); err != nil {
		t.Fatalf("read declared dimension of %q: %v", name, err)
	}
	if got != want {
		t.Errorf("collection %q declares %d, want %d", name, got, want)
	}
}

// The case a fixed-width column changes: rows of two widths cannot coexist, so
// the drift is between the table and the model rather than inside the table.
// This is a claim about PostgreSQL, so it is worth having a test say it out
// loud — if a future pgvector relaxes the column type, this is what notices.
func TestPostgresFixedWidthColumnCannotHoldTwoWidths(t *testing.T) {
	s, _ := newPostgresDimStore(t, 4) // vector(4)
	ctx := context.Background()

	c := seedCollection(t, s, "fixed", 4)
	mustUpsert(t, s, &Embedding{ID: "f1", CollectionID: c.ID, Vector: vecOfDim(4, 1), Content: "right width"})

	err := s.Upsert(ctx, &Embedding{ID: "f2", CollectionID: c.ID, Vector: vecOfDim(8, 1), Content: "wrong width"})
	if err == nil {
		t.Fatal("a vector(4) column accepted an 8-wide vector; the width is supposed to be enforced by the type")
	}

	// So nothing is mismatched against the column's own width...
	stale, err := s.MismatchedEmbeddings(ctx, 4, 0)
	if err != nil {
		t.Fatalf("MismatchedEmbeddings(4): %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("got %s, want nothing: every row in a vector(4) column is 4 wide", embIDs(stale))
	}

	// ...but a model that now emits 8 makes every row stale at once, which is
	// the form drift takes on this backend and the thing an operator needs to
	// be told, because repairing it needs the column widened first.
	stale, err = s.MismatchedEmbeddings(ctx, 8, 0)
	if err != nil {
		t.Fatalf("MismatchedEmbeddings(8): %v", err)
	}
	if len(stale) != 1 || stale[0].ID != "f1" {
		t.Errorf("got %s, want [f1]: a wider model makes the whole table stale", embIDs(stale))
	}
}

// The two Sync methods are no-ops, and the point of testing a no-op is that it
// is safely one: it must not panic, and it must not change what the store
// answers, because pgvector's index is already correct without them.
func TestPostgresSyncMethodsAreHarmlessNoOps(t *testing.T) {
	s, _ := newPostgresDimStore(t, 4)
	ctx := context.Background()

	embs := []*Embedding{
		{ID: "s1", Vector: []float32{1, 0, 0, 0}, Content: "one"},
		{ID: "s2", Vector: []float32{0, 1, 0, 0}, Content: "two"},
	}
	if err := s.UpsertBatch(ctx, embs); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	before, err := s.Search(ctx, []float32{1, 0, 0, 0}, SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Every shape a caller might hand them, including the empty and nil ones.
	s.SyncUpsertedEmbeddings(ctx, embs)
	s.SyncUpsertedEmbeddings(ctx, nil)
	s.SyncUpsertedEmbeddings(ctx, []*Embedding{nil, {ID: ""}})
	s.SyncDeletedEmbeddingIDs(ctx, []string{"s1"})
	s.SyncDeletedEmbeddingIDs(ctx, nil)
	s.SyncDeletedEmbeddingIDs(ctx, []string{""})

	// Telling the store "s1 was deleted" must not have deleted s1: these
	// methods inform an index, they do not write rows.
	after, err := s.Search(ctx, []float32{1, 0, 0, 0}, SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("Search after sync: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("search returned %d rows after the sync calls, %d before", len(after), len(before))
	}
	for i := range after {
		if after[i].ID != before[i].ID {
			t.Errorf("result %d changed from %s to %s across a pair of no-ops", i, before[i].ID, after[i].ID)
		}
	}

	// And a real delete still takes effect without any sync call, because
	// PostgreSQL maintains the index itself.
	if err := s.Delete(ctx, "s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := s.Search(ctx, []float32{1, 0, 0, 0}, SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	for _, e := range gone {
		if e.ID == "s1" {
			t.Error("a deleted row was still returned; the database's index did not follow the delete")
		}
	}
}
