package core

// The six BrainStore methods PostgresStore gained, tested against a real
// PostgreSQL.
//
// Not a parity suite — store_parity_test.go is that — but the assertions are
// written from the SQLite behaviour they have to match: a missing id is an
// error rather than a nil embedding, a missing document is an empty result
// rather than an error, a caller's rollback takes the whole batch with it, and
// a vector of the wrong width lands at the store's width or not at all.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// The schema these tests own. Other packages' suites run against the same
// database, so the tables live somewhere nothing else will drop.
const pgReadTestSchema = "cortexdb_read_test"

// openPGReadStore returns a PostgresStore on a schema of its own, or skips
// loudly. Skipping quietly would let a run report success having tested none
// of this.
func openPGReadStore(t *testing.T, cfg Config) *PostgresStore {
	t.Helper()

	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset — the PostgreSQL BrainStore reads are NOT covered by this run")
	}

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open (admin): %v", err)
	}
	defer admin.Close()

	ctx := context.Background()
	if _, err := admin.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+pgReadTestSchema+` CASCADE`); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+pgReadTestSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", dsn+sep+"search_path="+pgReadTestSchema)
	if err != nil {
		t.Fatalf("open (scoped): %v", err)
	}

	// The extension is database-wide; creating it from the scoped connection
	// would try to put it in the test schema and lose it on the drop.
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatalf("create extension: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SET search_path TO `+pgReadTestSchema+`, public`); err != nil {
		t.Fatalf("search_path: %v", err)
	}

	store := NewPostgresStore(db, cfg)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		cleanup, err := sql.Open("pgx", dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		if _, err := cleanup.Exec(`DROP SCHEMA IF EXISTS ` + pgReadTestSchema + ` CASCADE`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	return store
}

func pgReadConfig() Config {
	cfg := DefaultConfig()
	cfg.VectorDim = 4
	return cfg
}

// seedDoc creates the document row embeddings.doc_id points at. The foreign
// key is deliberate — SQLite has always had it — so a chunk with no document
// is rejected on both backends.
func seedDoc(t *testing.T, s *PostgresStore, id string) {
	t.Helper()
	if err := s.CreateDocument(context.Background(), &Document{ID: id, Title: id}); err != nil {
		t.Fatalf("CreateDocument(%s): %v", id, err)
	}
}

func TestPostgresGetByIDReturnsWhatWasWritten(t *testing.T) {
	s := openPGReadStore(t, pgReadConfig())
	ctx := context.Background()
	seedDoc(t, s, "doc-1")

	want := &Embedding{
		ID:       "emb-1",
		Vector:   []float32{0.25, 0.5, 0.75, 1},
		Content:  "the content that was written",
		DocID:    "doc-1",
		Metadata: map[string]string{"source": "test", "lang": "en"},
		ACL:      []string{"alice", "team:eng"},
	}
	if err := s.Upsert(ctx, want); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.GetByID(ctx, "emb-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != want.ID || got.Content != want.Content || got.DocID != want.DocID {
		t.Fatalf("GetByID returned %+v, want id/content/docID of %+v", got, want)
	}
	if len(got.Vector) != len(want.Vector) {
		t.Fatalf("vector width %d, want %d", len(got.Vector), len(want.Vector))
	}
	for i := range want.Vector {
		if got.Vector[i] != want.Vector[i] {
			t.Fatalf("vector[%d] = %v, want %v", i, got.Vector[i], want.Vector[i])
		}
	}
	if got.Metadata["source"] != "test" || got.Metadata["lang"] != "en" {
		t.Fatalf("metadata = %v, want the two keys written", got.Metadata)
	}
	if len(got.ACL) != 2 || got.ACL[0] != "alice" || got.ACL[1] != "team:eng" {
		t.Fatalf("acl = %v, want [alice team:eng]", got.ACL)
	}
	// The join, which is what makes the SQLite GetByID an eight-column query.
	if got.Collection != "default" {
		t.Fatalf("collection = %q, want %q", got.Collection, "default")
	}
}

func TestPostgresGetByIDIsNotFoundNotNilNil(t *testing.T) {
	s := openPGReadStore(t, pgReadConfig())

	got, err := s.GetByID(context.Background(), "no-such-embedding")
	if err == nil {
		t.Fatalf("GetByID of a missing id returned (%+v, nil) — SQLite returns ErrNotFound", got)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID error = %v, want it to wrap ErrNotFound", err)
	}
	if got != nil {
		t.Fatalf("GetByID returned an embedding alongside its error: %+v", got)
	}
}

func TestPostgresGetByDocIDReturnsEveryChunk(t *testing.T) {
	s := openPGReadStore(t, pgReadConfig())
	ctx := context.Background()
	seedDoc(t, s, "doc-many")
	seedDoc(t, s, "doc-other")

	chunks := []*Embedding{
		{ID: "c1", Vector: []float32{1, 0, 0, 0}, Content: "first", DocID: "doc-many"},
		{ID: "c2", Vector: []float32{0, 1, 0, 0}, Content: "second", DocID: "doc-many"},
		{ID: "c3", Vector: []float32{0, 0, 1, 0}, Content: "third", DocID: "doc-many"},
		{ID: "elsewhere", Vector: []float32{0, 0, 0, 1}, Content: "another document", DocID: "doc-other"},
	}
	if err := s.UpsertBatch(ctx, chunks); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	got, err := s.GetByDocID(ctx, "doc-many")
	if err != nil {
		t.Fatalf("GetByDocID: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetByDocID returned %d chunks, want 3", len(got))
	}
	seen := map[string]string{}
	for _, e := range got {
		seen[e.ID] = e.Content
		if e.DocID != "doc-many" {
			t.Fatalf("chunk %s has docID %q", e.ID, e.DocID)
		}
		if len(e.Vector) != 4 {
			t.Fatalf("chunk %s vector width %d, want 4", e.ID, len(e.Vector))
		}
	}
	for id, content := range map[string]string{"c1": "first", "c2": "second", "c3": "third"} {
		if seen[id] != content {
			t.Fatalf("chunk %s content = %q, want %q", id, seen[id], content)
		}
	}
}

func TestPostgresGetByDocIDOfAnUnknownDocumentIsEmptyNotAnError(t *testing.T) {
	s := openPGReadStore(t, pgReadConfig())

	got, err := s.GetByDocID(context.Background(), "never-ingested")
	if err != nil {
		t.Fatalf("GetByDocID of an unknown document errored: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("GetByDocID of an unknown document returned %d rows", len(got))
	}

	// An empty id is the other case, and it is a caller bug rather than an
	// answer — SQLite says so and this must too.
	if _, err := s.GetByDocID(context.Background(), ""); err == nil {
		t.Fatal("GetByDocID(\"\") returned no error — SQLite rejects an empty doc ID")
	}
}

func TestPostgresUpsertBatchTxRollsBackWithTheCaller(t *testing.T) {
	s := openPGReadStore(t, pgReadConfig())
	ctx := context.Background()
	seedDoc(t, s, "doc-tx")

	batch := []*Embedding{
		{ID: "tx-1", Vector: []float32{1, 1, 1, 1}, Content: "written inside a transaction", DocID: "doc-tx"},
		{ID: "tx-2", Vector: []float32{2, 2, 2, 2}, Content: "and so was this", DocID: "doc-tx"},
	}

	tx, err := s.GetDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := s.UpsertBatchTx(ctx, tx, batch); err != nil {
		t.Fatalf("UpsertBatchTx: %v", err)
	}
	// Visible inside the transaction: the write really did happen, it just
	// has not been decided yet.
	var inTx int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM embeddings WHERE doc_id = $1`, "doc-tx").Scan(&inTx); err != nil {
		t.Fatalf("count inside tx: %v", err)
	}
	if inTx != 2 {
		t.Fatalf("inside the transaction there are %d rows, want 2", inTx)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// And gone, because the transaction was the caller's to abandon.
	after, err := s.GetByDocID(ctx, "doc-tx")
	if err != nil {
		t.Fatalf("GetByDocID after rollback: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("rollback left %d rows behind — UpsertBatchTx committed on its own", len(after))
	}
	if _, err := s.GetByID(ctx, "tx-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID after rollback = %v, want ErrNotFound", err)
	}

	// The same batch through a committed transaction, so the rollback above
	// is evidence about rollback and not about UpsertBatchTx writing nothing.
	tx2, err := s.GetDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx (commit path): %v", err)
	}
	if err := s.UpsertBatchTx(ctx, tx2, batch); err != nil {
		t.Fatalf("UpsertBatchTx (commit path): %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	committed, err := s.GetByDocID(ctx, "doc-tx")
	if err != nil {
		t.Fatalf("GetByDocID after commit: %v", err)
	}
	if len(committed) != 2 {
		t.Fatalf("after commit there are %d rows, want 2", len(committed))
	}
}

func TestPostgresUpsertBatchWithAdaptStoresAtTheStoresWidth(t *testing.T) {
	cfg := pgReadConfig()
	cfg.AutoDimAdapt = AutoTruncate
	s := openPGReadStore(t, cfg)
	ctx := context.Background()

	wide := []*Embedding{
		{ID: "wide", Vector: []float32{1, 2, 3, 4, 5, 6}, Content: "six wide, four expected"},
	}
	if err := s.UpsertBatchWithAdapt(ctx, wide); err != nil {
		t.Fatalf("UpsertBatchWithAdapt: %v", err)
	}

	got, err := s.GetByID(ctx, "wide")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Vector) != 4 {
		t.Fatalf("stored vector is %d wide, want the store's 4", len(got.Vector))
	}
	// Compared against the adapter rather than against [1,2,3,4]: AutoTruncate
	// renormalizes what it keeps, and the assertion that matters is that the
	// store wrote the adapter's answer rather than some rounding of its own.
	want, err := NewDimensionAdapter(AutoTruncate).AdaptVector([]float32{1, 2, 3, 4, 5, 6}, 6, 4)
	if err != nil {
		t.Fatalf("AdaptVector: %v", err)
	}
	for i := range want {
		if diff := got.Vector[i] - want[i]; diff > 1e-6 || diff < -1e-6 {
			t.Fatalf("vector[%d] = %v, want %v (what the adapter produced)", i, got.Vector[i], want[i])
		}
	}

	// A narrow vector too: padding is the other half of the same policy.
	cfgPad := pgReadConfig()
	cfgPad.AutoDimAdapt = AutoPad
	narrow := []*Embedding{{ID: "narrow", Vector: []float32{7, 8}, Content: "two wide"}}
	sPad := NewPostgresStore(s.GetDB(), cfgPad)
	if err := sPad.UpsertBatchWithAdapt(ctx, narrow); err != nil {
		t.Fatalf("UpsertBatchWithAdapt (pad): %v", err)
	}
	padded, err := sPad.GetByID(ctx, "narrow")
	if err != nil {
		t.Fatalf("GetByID (pad): %v", err)
	}
	if len(padded.Vector) != 4 {
		t.Fatalf("padded vector is %d wide, want 4", len(padded.Vector))
	}
}

func TestPostgresUpsertBatchWithAdaptRefusesUnderStrictMode(t *testing.T) {
	// The default policy, and the reason adapting is a separate method: it
	// only adapts when it was told it may.
	s := openPGReadStore(t, pgReadConfig())

	err := s.UpsertBatchWithAdapt(context.Background(), []*Embedding{
		{ID: "strict", Vector: []float32{1, 2, 3, 4, 5, 6}, Content: "wrong width"},
	})
	if err == nil {
		t.Fatal("UpsertBatchWithAdapt reshaped a vector under StrictMode")
	}
	if !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("error = %v, want it to name the dimension mismatch", err)
	}
}

func TestPostgresConfigAndGetDBAreTheStoresOwn(t *testing.T) {
	cfg := pgReadConfig()
	s := openPGReadStore(t, cfg)

	if got := s.Config().VectorDim; got != 4 {
		t.Fatalf("Config().VectorDim = %d, want 4", got)
	}
	db := s.GetDB()
	if db == nil {
		t.Fatal("GetDB returned nil")
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("the handle GetDB returned is not usable: %v", err)
	}
}
