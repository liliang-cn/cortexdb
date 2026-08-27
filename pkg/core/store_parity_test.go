package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// One suite, both stores.
//
// "A second implementation" is only worth having if it answers the same
// questions the same way. What keeps that true is not the interface — Go
// checks the shape, not the behaviour — but a suite that runs twice.

type storeUnderTest struct {
	name  string
	store Store
}

func storesUnderTest(t *testing.T) []storeUnderTest {
	t.Helper()

	path := fmt.Sprintf("test_parity_%d.db", time.Now().UnixNano())
	lite, err := New(path, 4)
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := lite.Init(context.Background()); err != nil {
		t.Fatalf("sqlite init: %v", err)
	}
	t.Cleanup(func() {
		lite.Close()
		os.Remove(path)
	})
	out := []storeUnderTest{{name: "sqlite", store: lite}}

	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Log("CORTEXDB_TEST_POSTGRES unset — the PostgreSQL store is NOT covered by this run")
		return out
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	for _, table := range []string{"embeddings", "collections"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE"); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}
	cfg := DefaultConfig()
	cfg.VectorDim = 4
	pg := NewPostgresStore(db, cfg)
	if err := pg.Init(context.Background()); err != nil {
		t.Fatalf("postgres init: %v", err)
	}
	if indexed, why := pg.Indexed(); !indexed {
		t.Logf("PostgreSQL search is exact, not indexed: %s", why)
	}
	t.Cleanup(func() { db.Close() })
	return append(out, storeUnderTest{name: "postgres", store: pg})
}

func TestBothStoresRankTheSameWay(t *testing.T) {
	// Four points on a line: "nearest to [1,0,0,0]" has one right answer.
	seed := []*Embedding{
		{ID: "near", Vector: []float32{1, 0, 0, 0}, Content: "nearest"},
		{ID: "close", Vector: []float32{0.9, 0.1, 0, 0}, Content: "close by"},
		{ID: "middle", Vector: []float32{0.5, 0.5, 0, 0}, Content: "halfway"},
		{ID: "far", Vector: []float32{0, 1, 0, 0}, Content: "orthogonal"},
	}
	want := []string{"near", "close", "middle", "far"}

	for _, s := range storesUnderTest(t) {
		t.Run(s.name, func(t *testing.T) {
			ctx := context.Background()
			if err := s.store.UpsertBatch(ctx, seed); err != nil {
				t.Fatalf("UpsertBatch: %v", err)
			}

			got, err := s.store.Search(ctx, []float32{1, 0, 0, 0}, SearchOptions{TopK: 4})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("got %d results, want %d", len(got), len(want))
			}
			for i, w := range want {
				if got[i].ID != w {
					ids := make([]string, len(got))
					for j := range got {
						ids[j] = got[j].ID
					}
					t.Fatalf("ranking = %v, want %v", ids, want)
				}
			}
			// The score has to mean the same thing on both, or a threshold
			// tuned on one backend is wrong on the other.
			if got[0].Score < 0.99 {
				t.Errorf("an exact match scored %.4f — cosine similarity should be ~1", got[0].Score)
			}
			if got[0].Score < got[len(got)-1].Score {
				t.Error("scores do not descend with distance")
			}
			// Content must survive the round trip.
			if got[0].Content != "nearest" {
				t.Errorf("content = %q, want %q", got[0].Content, "nearest")
			}
		})
	}
}

func TestBothStoresCountAndDeleteTheSame(t *testing.T) {
	for _, s := range storesUnderTest(t) {
		t.Run(s.name, func(t *testing.T) {
			ctx := context.Background()
			// A divergence worth stating rather than working around: SQLite
			// declares doc_id a foreign key into documents and enforces it,
			// while the PostgreSQL store has no documents table yet, so there
			// doc_id is a free tag. The same insert is therefore rejected by
			// one and accepted by the other — found by this suite, which is
			// what it is for. Create the row where it is required.
			err := s.store.CreateDocument(ctx, &Document{ID: "shared-doc", Title: "shared"})
			if err != nil && !errors.Is(err, ErrPostgresStoreUnimplemented) {
				t.Fatalf("CreateDocument: %v", err)
			}
			for i := 0; i < 5; i++ {
				err = s.store.Upsert(ctx, &Embedding{
					ID:      fmt.Sprintf("e%d", i),
					Vector:  []float32{float32(i), 1, 0, 0},
					Content: fmt.Sprintf("doc %d", i),
					DocID:   "shared-doc",
				})
				if err != nil {
					t.Fatalf("Upsert %d: %v", i, err)
				}
			}

			stats, err := s.store.Stats(ctx)
			if err != nil {
				t.Fatalf("Stats: %v", err)
			}
			if stats.Count != 5 {
				t.Fatalf("Stats.Count = %d, want 5", stats.Count)
			}

			if err := s.store.Delete(ctx, "e0"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if err := s.store.DeleteBatch(ctx, []string{"e1", "e2"}); err != nil {
				t.Fatalf("DeleteBatch: %v", err)
			}
			if stats, _ = s.store.Stats(ctx); stats.Count != 2 {
				t.Fatalf("after deleting 3 of 5, count = %d, want 2", stats.Count)
			}

			if err := s.store.DeleteByDocID(ctx, "shared-doc"); err != nil {
				t.Fatalf("DeleteByDocID: %v", err)
			}
			if stats, _ = s.store.Stats(ctx); stats.Count != 0 {
				t.Fatalf("after deleting the doc, count = %d, want 0", stats.Count)
			}
		})
	}
}

// Upsert means upsert: the second write of an id replaces the first rather
// than adding a row or failing.
func TestUpsertReplacesOnBothStores(t *testing.T) {
	for _, s := range storesUnderTest(t) {
		t.Run(s.name, func(t *testing.T) {
			ctx := context.Background()
			e := &Embedding{ID: "same", Vector: []float32{1, 0, 0, 0}, Content: "first"}
			if err := s.store.Upsert(ctx, e); err != nil {
				t.Fatalf("first: %v", err)
			}
			e.Content = "second"
			e.Vector = []float32{0, 1, 0, 0}
			if err := s.store.Upsert(ctx, e); err != nil {
				t.Fatalf("second: %v", err)
			}

			stats, _ := s.store.Stats(ctx)
			if stats.Count != 1 {
				t.Fatalf("count = %d after two upserts of one id, want 1", stats.Count)
			}
			got, err := s.store.Search(ctx, []float32{0, 1, 0, 0}, SearchOptions{TopK: 1})
			if err != nil || len(got) == 0 {
				t.Fatalf("Search: %v (%d results)", err, len(got))
			}
			if got[0].Content != "second" {
				t.Errorf("content = %q, want the second write", got[0].Content)
			}
		})
	}
}

// What PostgreSQL does not do yet, it says by name. An empty result would be
// indistinguishable from "nothing matched", which is how a missing backend
// becomes a silent wrong answer.
func TestUnimplementedPartsNameThemselves(t *testing.T) {
	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	pg := NewPostgresStore(db, DefaultConfig())
	ctx := context.Background()

	for name, call := range map[string]func() error{
		"CreateDocument": func() error { return pg.CreateDocument(ctx, &Document{}) },
		"AddMessage":     func() error { return pg.AddMessage(ctx, &Message{}) },
		"TrainQuantizer": func() error { return pg.TrainQuantizer(ctx) },
		"DeleteByFilter": func() error { return pg.DeleteByFilter(ctx, nil) },
		"SearchWithACL":  func() error { _, err := pg.SearchWithACL(ctx, nil, nil, SearchOptions{}); return err },
		"HybridSearch":   func() error { _, err := pg.HybridSearch(ctx, nil, "", HybridSearchOptions{}); return err },
	} {
		err := call()
		if !errors.Is(err, ErrPostgresStoreUnimplemented) {
			t.Errorf("%s returned %v, want ErrPostgresStoreUnimplemented", name, err)
		}
		if err != nil && !contains(err.Error(), name) {
			t.Errorf("%s's error does not name it: %v", name, err)
		}
	}

	// TrainIndex is a genuine no-op, not a gap: pgvector maintains its own.
	if err := pg.TrainIndex(ctx, 100); err != nil {
		t.Errorf("TrainIndex should be a no-op on pgvector, got %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOfStr(haystack, needle) >= 0)
}

func indexOfStr(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
