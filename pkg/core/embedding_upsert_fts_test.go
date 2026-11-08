package core

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// Rewriting a chunk must leave one entry in each full-text index. INSERT OR
// REPLACE removes the old row without firing the delete triggers (SQLite does
// that only under recursive_triggers), so every rewrite used to leave a ghost:
// an index entry whose row is gone, which skews BM25 and grows the index.

type embeddingWriter struct {
	name  string
	id    string
	write func(ctx context.Context, s *SQLiteStore, content string) error
}

func embeddingWriters() []embeddingWriter {
	emb := func(id, content string) *Embedding {
		return &Embedding{ID: id, Vector: []float32{1, 2, 3}, Content: content, DocID: "doc-upsert"}
	}
	return []embeddingWriter{
		{"Upsert", "one", func(ctx context.Context, s *SQLiteStore, c string) error {
			return s.Upsert(ctx, emb("one", c))
		}},
		{"UpsertBatch", "batch", func(ctx context.Context, s *SQLiteStore, c string) error {
			return s.UpsertBatch(ctx, []*Embedding{emb("batch", c)})
		}},
		{"UpsertBatchTx", "tx", func(ctx context.Context, s *SQLiteStore, c string) error {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			if err := s.UpsertBatchTx(ctx, tx, []*Embedding{emb("tx", c)}); err != nil {
				_ = tx.Rollback()
				return err
			}
			return tx.Commit()
		}},
		{"UpsertMultiVector", "doc-upsert___title", func(ctx context.Context, s *SQLiteStore, c string) error {
			return s.UpsertMultiVector(ctx, &MultiVectorEntity{
				ID: "doc-upsert", Vectors: map[string][]float32{"title": {1, 2, 3}}, Content: c,
			})
		}},
	}
}

func newUpsertStore(t *testing.T) (*SQLiteStore, context.Context) {
	t.Helper()
	store, ctx := newCJKStore(t)
	if err := createDummyDoc(ctx, store, "doc-upsert"); err != nil {
		t.Fatalf("failed to create doc: %v", err)
	}
	return store, ctx
}

func rowidOf(t *testing.T, db *sql.DB, id string) int64 {
	t.Helper()
	var rowid int64
	if err := db.QueryRow(`SELECT rowid FROM embeddings WHERE id = ?`, id).Scan(&rowid); err != nil {
		t.Fatalf("rowid of %s: %v", id, err)
	}
	return rowid
}

func TestRewritingAChunkLeavesNoGhostInTheFullTextIndexes(t *testing.T) {
	for _, w := range embeddingWriters() {
		t.Run(w.name, func(t *testing.T) {
			store, ctx := newUpsertStore(t)
			if err := w.write(ctx, store, "marmalade first"); err != nil {
				t.Fatalf("first write: %v", err)
			}
			if err := w.write(ctx, store, "marmalade second"); err != nil {
				t.Fatalf("second write: %v", err)
			}
			for _, index := range []string{"chunks_fts", "chunks_fts_cjk"} {
				if got := ftsHits(t, store.db, index, "marmalade"); got != 1 {
					t.Errorf("%s: %d entries for the rewritten chunk, want 1", index, got)
				}
				if got := ftsHits(t, store.db, index, "first"); got != 0 {
					t.Errorf("%s: the old text still matches (%d)", index, got)
				}
				if got := ftsHits(t, store.db, index, "second"); got != 1 {
					t.Errorf("%s: the new text matches %d times, want 1", index, got)
				}
			}
		})
	}
}

func TestRewritingAChunkKeepsItsRowid(t *testing.T) {
	for _, w := range embeddingWriters() {
		t.Run(w.name, func(t *testing.T) {
			store, ctx := newUpsertStore(t)
			if err := w.write(ctx, store, "first"); err != nil {
				t.Fatalf("first write: %v", err)
			}
			// A later row, so that deleting then inserting could not reuse the rowid.
			if err := store.Upsert(ctx, &Embedding{ID: "later", Vector: []float32{1, 2, 3}, Content: "later", DocID: "doc-upsert"}); err != nil {
				t.Fatalf("later row: %v", err)
			}
			before := rowidOf(t, store.db, w.id)
			if err := w.write(ctx, store, "second"); err != nil {
				t.Fatalf("second write: %v", err)
			}
			if after := rowidOf(t, store.db, w.id); after != before {
				t.Errorf("rowid moved from %d to %d", before, after)
			}
		})
	}
}

// The rewrite replaces every column, as INSERT OR REPLACE did: a field the new
// write leaves out does not survive from the old row.
func TestRewritingAChunkReplacesEveryColumn(t *testing.T) {
	store, ctx := newUpsertStore(t)
	if err := createDummyDoc(ctx, store, "doc-other"); err != nil {
		t.Fatalf("failed to create doc: %v", err)
	}
	if _, err := store.CreateCollection(ctx, "moved", 3); err != nil {
		t.Fatalf("collection: %v", err)
	}
	if err := store.Upsert(ctx, &Embedding{
		ID: "c", Vector: []float32{1, 2, 3}, Content: "old", DocID: "doc-upsert",
		Metadata: map[string]string{"k": "old"}, ACL: []string{"alice"},
	}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE embeddings SET created_at = '2000-01-01 00:00:00' WHERE id = 'c'`); err != nil {
		t.Fatalf("age the row: %v", err)
	}
	if err := store.Upsert(ctx, &Embedding{
		ID: "c", Vector: []float32{4, 5, 6}, Content: "new", DocID: "doc-other", Collection: "moved",
	}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	var content, docID, created, collection string
	var metadata, acl sql.NullString
	var vector []byte
	if err := store.db.QueryRow(`
		SELECT e.content, e.doc_id, e.metadata, e.acl, e.vector, e.created_at, c.name
		FROM embeddings e JOIN collections c ON c.id = e.collection_id WHERE e.id = 'c'`,
	).Scan(&content, &docID, &metadata, &acl, &vector, &created, &collection); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if content != "new" || docID != "doc-other" || collection != "moved" {
		t.Errorf("content %q, doc %q, collection %q", content, docID, collection)
	}
	if strings.Contains(metadata.String, "old") {
		t.Errorf("old metadata survived: %q", metadata.String)
	}
	if acl.Valid {
		t.Errorf("old acl survived: %q", acl.String)
	}
	if created[:4] == "2000" {
		t.Errorf("created_at kept from the old row: %s", created)
	}
	got, err := store.GetByID(ctx, "c")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Vector[0] != 4 {
		t.Errorf("old vector survived: %v", got.Vector)
	}
}

// A rewrite that leaves a field empty clears it: nothing is kept from the old row.
func TestRewritingAChunkClearsEmptyFields(t *testing.T) {
	store, ctx := newUpsertStore(t)
	if err := store.Upsert(ctx, &Embedding{ID: "c", Vector: []float32{1, 2, 3}, Content: "marmalade", DocID: "doc-upsert"}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := store.Upsert(ctx, &Embedding{ID: "c", Vector: []float32{1, 2, 3}}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	var content string
	var docID sql.NullString
	if err := store.db.QueryRow(`SELECT content, doc_id FROM embeddings WHERE id = 'c'`).Scan(&content, &docID); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if content != "" || docID.Valid {
		t.Errorf("kept from the old row: content %q, doc %q", content, docID.String)
	}
	if got := ftsHits(t, store.db, "chunks_fts", "marmalade"); got != 0 {
		t.Errorf("the old text still matches (%d)", got)
	}
}

// UpsertMultiVector has no ACL to give: its rewrite clears one, as before.
func TestRewritingAMultiVectorChunkClearsItsACL(t *testing.T) {
	store, ctx := newUpsertStore(t)
	entity := &MultiVectorEntity{ID: "doc-upsert", Vectors: map[string][]float32{"title": {1, 2, 3}}, Content: "x"}
	if err := store.UpsertMultiVector(ctx, entity); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE embeddings SET acl = '["alice"]' WHERE id = 'doc-upsert___title'`); err != nil {
		t.Fatalf("set acl: %v", err)
	}
	if err := store.UpsertMultiVector(ctx, entity); err != nil {
		t.Fatalf("second write: %v", err)
	}
	var acl sql.NullString
	if err := store.db.QueryRow(`SELECT acl FROM embeddings WHERE id = 'doc-upsert___title'`).Scan(&acl); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if acl.Valid {
		t.Errorf("old acl survived: %q", acl.String)
	}
}
