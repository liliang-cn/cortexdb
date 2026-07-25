package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// The default FTS5 tokenizer (unicode61) does not segment CJK: a run of Han characters
// becomes one token, so lexical search returns nothing for any Chinese query while
// English queries work. These tests pin the trigram tokenizer that fixes it, and the
// migration that repairs databases created before the fix.

func ftsHits(t *testing.T, db *sql.DB, index, query string) int {
	t.Helper()
	var hits int
	sqlText := "SELECT count(*) FROM " + index + " WHERE " + index + " MATCH ?"
	if err := db.QueryRow(sqlText, `"`+query+`"`).Scan(&hits); err != nil {
		t.Fatalf("%s MATCH %q failed: %v", index, query, err)
	}
	return hits
}

func newCJKStore(t *testing.T) (*SQLiteStore, context.Context) {
	t.Helper()
	store, err := New(filepath.Join(t.TempDir(), "cjk.db"), 3)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	return store, ctx
}

func TestChunkLexicalSearchFindsChineseText(t *testing.T) {
	store, ctx := newCJKStore(t)
	if err := createDummyDoc(ctx, store, "doc-cjk"); err != nil {
		t.Fatalf("failed to create doc: %v", err)
	}
	err := store.Upsert(ctx, &Embedding{
		ID:      "chunk-cjk",
		Vector:  []float32{1, 2, 3},
		Content: "复合函数是指由两个函数复合而成的函数，其定义域需要满足条件。",
		DocID:   "doc-cjk",
	})
	if err != nil {
		t.Fatalf("failed to add embedding: %v", err)
	}

	db := store.GetDB()
	// Terms of three characters or more — the common case for a natural-language query.
	for _, query := range []string{"复合函数", "定义域", "满足条件"} {
		if hits := ftsHits(t, db, CJKAwareIndex("chunks_fts", query), query); hits == 0 {
			t.Errorf("lexical search found nothing for %q; CJK text is not indexed", query)
		}
	}
	// A term that is absent must still not match.
	if hits := ftsHits(t, db, CJKAwareIndex("chunks_fts", "微分方程"), "微分方程"); hits != 0 {
		t.Errorf("lexical search matched absent term 微分方程 (%d hits)", hits)
	}
	// English keeps working.
	if hits := ftsHits(t, db, CJKAwareIndex("chunks_fts", "function"), "function"); hits != 0 {
		t.Errorf("expected no English hits in a Chinese-only chunk, got %d", hits)
	}
}

func TestMessageLexicalSearchFindsChineseText(t *testing.T) {
	store, ctx := newCJKStore(t)
	db := store.GetDB()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id) VALUES ('s1', 'u1')`); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO messages (id, session_id, role, content) VALUES ('m1', 's1', 'memory', ?)`,
		"学习进度：计划完成度 27%，错题掌握率 20%。"); err != nil {
		t.Fatalf("failed to insert message: %v", err)
	}
	for _, query := range []string{"学习进度", "掌握率", "计划完成度"} {
		if hits := ftsHits(t, db, CJKAwareIndex("messages_fts", query), query); hits == 0 {
			t.Errorf("memory search found nothing for %q", query)
		}
	}
}

func TestCJKIndexBackfillsExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	store, err := New(path, 3)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	if err := createDummyDoc(ctx, store, "doc-legacy"); err != nil {
		t.Fatalf("failed to create doc: %v", err)
	}
	if err := store.Upsert(ctx, &Embedding{
		ID:      "chunk-legacy",
		Vector:  []float32{1, 2, 3},
		Content: "极限与连续是微积分的基础概念。",
		DocID:   "doc-legacy",
	}); err != nil {
		t.Fatalf("failed to add embedding: %v", err)
	}

	// Simulate a database written before the companion index existed: drop it and the
	// triggers that keep it in sync, exactly as an older CortexDB would have left things.
	db := store.GetDB()
	for _, statement := range []string{
		`DROP TRIGGER embeddings_cjk_ai`,
		`DROP TRIGGER embeddings_cjk_ad`,
		`DROP TRIGGER embeddings_cjk_au`,
		`DROP TABLE chunks_fts_cjk`,
		// A genuine pre-fix database also predates the schema version that records the
		// build, so reset it — otherwise this only tests the "already migrated" path.
		`PRAGMA user_version = 0`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("failed to stage legacy schema (%s): %v", statement, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}

	// Re-opening creates the companion index and backfills the rows already present.
	reopened, err := New(path, 3)
	if err != nil {
		t.Fatalf("failed to reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Init(ctx); err != nil {
		t.Fatalf("failed to re-init store: %v", err)
	}
	db = reopened.GetDB()
	if hits := ftsHits(t, db, "chunks_fts_cjk", "微积分"); hits == 0 {
		t.Error("backfill did not make existing Chinese content searchable")
	}
	if hits := ftsHits(t, db, "chunks_fts_cjk", "基础概念"); hits == 0 {
		t.Error("companion index was created but content is missing")
	}
	// The build is recorded, so a second pass is a no-op that leaves the index intact.
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("failed to read schema version: %v", err)
	}
	if version < schemaVersionCJKIndexes {
		t.Errorf("schema version %d did not record the CJK index build", version)
	}
	if err := reopened.backfillCJKIndexes(ctx); err != nil {
		t.Fatalf("second backfill pass failed: %v", err)
	}
	if hits := ftsHits(t, db, "chunks_fts_cjk", "微积分"); hits == 0 {
		t.Error("second backfill pass lost the index")
	}
}

func TestCJKAwareIndexRoutesByScript(t *testing.T) {
	cases := map[string]string{
		"复合函数的定义":                  "chunks_fts_cjk",
		"composition of functions": "chunks_fts",
		"函数 function":              "chunks_fts_cjk",
		"":                         "chunks_fts",
		"ひらがな":                     "chunks_fts_cjk",
		"한글":                       "chunks_fts_cjk",
		"BM25 ranking":             "chunks_fts",
	}
	for query, want := range cases {
		if got := CJKAwareIndex("chunks_fts", query); got != want {
			t.Errorf("CJKAwareIndex(%q) = %q, want %q", query, got, want)
		}
	}
}
