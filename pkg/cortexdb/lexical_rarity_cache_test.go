package cortexdb

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"
)

// forgetChunkStats empties the keyword floor's counts, so the next question
// counts again.
func forgetChunkStats(db *DB) {
	db.chunkStats.mu.Lock()
	defer db.chunkStats.mu.Unlock()
	db.chunkStats.sets = map[int64]*cachedChunkStats{}
}

// A question the trigram index cannot count is counted by the scan of the
// collection once; asked again over the same chunks, its counts are served
// as they were, with the same rows and scores.
func TestARepeatedQuestionIsNotCountedAgain(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	for _, query := range []string{"is WAL checkpoint ok", "why does the WAL checkpoint file keep growing"} {
		first := searchCountingStages(t, db, query)
		if first.trigram+first.scan != 1 {
			t.Fatalf("%q: first question counted %d times by the index and %d by the scan, want once", query, first.trigram, first.scan)
		}
		again := searchCountingStages(t, db, query)
		if again.trigram != 0 || again.scan != 0 {
			t.Errorf("%q: asked again over the same chunks, counted %d times by the index and %d by the scan", query, again.trigram, again.scan)
		}
		if !slices.Equal(first.ids, again.ids) || !slices.Equal(first.scores, again.scores) {
			t.Errorf("%q: first %v %v, again %v %v: want the same rows and scores", query, first.ids, first.scores, again.ids, again.scores)
		}
	}
}

// The counts served are those a fresh count gives, after every kind of write
// that changes the chunks of a collection, made through this handle or
// through another one on the same file.
func TestTheCachedCountsFollowEveryWriteToTheChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.db")
	db, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	other, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	saveTrigramFloorDocs(t, db)
	ctx := context.Background()
	patterns := []string{"checkpoint", "wal", "pool", "ok"}

	scan := func(collection string, missing []string) (corpusStats, error) {
		from, where, args := "embeddings e", "", []any(nil)
		if collection != "" {
			from, where, args = "embeddings e LEFT JOIN collections c ON e.collection_id = c.id", "c.name = ?", []any{collection}
		}
		return db.scanCorpusStats(ctx, "e.content", from, where, args, missing)
	}
	served := func(collection string) corpusStats {
		t.Helper()
		stats, err := db.cachedChunkStatsFor(ctx, collection, patterns, func(missing []string) (corpusStats, error) { return scan(collection, missing) })
		if err != nil {
			t.Fatal(err)
		}
		return stats
	}
	fresh := func(collection string) corpusStats {
		t.Helper()
		stats, err := scan(collection, patterns)
		if err != nil {
			t.Fatal(err)
		}
		return stats
	}
	same := func(a, b corpusStats) bool { return a.n == b.n && maps.Equal(a.df, b.df) }

	for _, collection := range []string{"kb", ""} {
		served(collection)
	}
	exec := func(h *DB, query string) func() error {
		return func() error { _, err := h.SQL().Exec(query); return err }
	}
	writes := []struct {
		name  string
		write func() error
	}{
		{"insert", func() error {
			_, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "more", Collection: "kb", Content: "Another checkpoint note, ok."})
			return err
		}},
		{"content updated", exec(db, `UPDATE embeddings SET content = 'nothing to see' WHERE content LIKE '%thin pool%'`)},
		{"moved to another collection", exec(db, `UPDATE embeddings SET collection_id = (SELECT id FROM collections WHERE name = 'other') WHERE content LIKE '%Unbounded growth%'`)},
		{"deleted", exec(db, `DELETE FROM embeddings WHERE content LIKE '%SQLite WAL grew%'`)},
		{"deleted then inserted at the same rowid", func() error {
			if err := exec(db, `DELETE FROM embeddings WHERE rowid = (SELECT MAX(rowid) FROM embeddings)`)(); err != nil {
				return err
			}
			_, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "again", Collection: "kb", Content: "A pool, a pool."})
			return err
		}},
		{"rewritten under its id", func() error {
			_, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "tagit", Collection: "kb", Content: "TagIt keeps a WAL checkpoint, ok."})
			return err
		}},
		{"deleted by another handle", func() error {
			_, err := other.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "tagit"})
			return err
		}},
		{"inserted by another handle", func() error {
			_, err := other.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "elsewhere", Collection: "kb", Content: "checkpoint pool wal ok"})
			return err
		}},
	}
	for _, w := range writes {
		before := map[string]corpusStats{"kb": fresh("kb"), "": fresh("")}
		if err := w.write(); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		for _, collection := range []string{"kb", ""} {
			want := fresh(collection)
			if collection == "kb" && same(want, before[collection]) {
				t.Fatalf("%s: the write changed no count of kb, so it tests nothing", w.name)
			}
			if got := served(collection); !same(got, want) {
				t.Errorf("%s, collection %q: served n=%d %v, a fresh count gives n=%d %v", w.name, collection, got.n, got.df, want.n, want.df)
			}
		}
	}
}

// Only the terms the cache lacks are counted; a collection that does not
// exist is counted every time and kept nowhere; past chunkStatsCacheTerms a
// collection's counts start over; and a store that cannot be read reports it.
func TestTheCacheCountsOnlyWhatItLacks(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	ctx := context.Background()
	var asked [][]string
	count := func(missing []string) (corpusStats, error) {
		asked = append(asked, append([]string(nil), missing...))
		stats := corpusStats{n: 7, df: map[string]int{}}
		for i, p := range missing {
			stats.df[p] = i + 1
		}
		return stats, nil
	}
	if _, err := db.cachedChunkStatsFor(ctx, "kb", []string{"wal", "pool"}, count); err != nil {
		t.Fatal(err)
	}
	got, err := db.cachedChunkStatsFor(ctx, "kb", []string{"pool", "growth", "wal"}, count)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked[1], []string{"growth"}) {
		t.Errorf("second question counted %v, want only the new term", asked[1])
	}
	if got.n != 7 || !maps.Equal(got.df, map[string]int{"wal": 1, "pool": 2, "growth": 1}) {
		t.Errorf("served n=%d %v, want the cached counts with the new one", got.n, got.df)
	}

	asked = nil
	for range 2 {
		if _, err := db.cachedChunkStatsFor(ctx, "nowhere", []string{"wal"}, count); err != nil {
			t.Fatal(err)
		}
	}
	if len(asked) != 2 {
		t.Errorf("a collection that does not exist was counted %d times in 2 questions, want 2", len(asked))
	}

	many := make([]string, chunkStatsCacheTerms+1)
	for i := range many {
		many[i] = fmt.Sprintf("term%d", i)
	}
	if _, err := db.cachedChunkStatsFor(ctx, "kb", many, count); err != nil {
		t.Fatal(err)
	}
	asked = nil
	if _, err := db.cachedChunkStatsFor(ctx, "kb", []string{"wal"}, count); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 {
		t.Errorf("past %d terms the counts were kept", chunkStatsCacheTerms)
	}

	failing := func([]string) (corpusStats, error) { return corpusStats{}, fmt.Errorf("count failed") }
	if _, err := db.cachedChunkStatsFor(ctx, "kb", []string{"never-counted"}, failing); err == nil {
		t.Error("a failed count was served")
	}

	_ = db.Close()
	for _, collection := range []string{"kb", ""} {
		if _, err := db.cachedChunkStatsFor(ctx, collection, []string{"wal"}, count); err == nil {
			t.Errorf("collection %q: a closed store served counts", collection)
		}
	}
}

// A store that cannot hold the version counter is counted at every
// question, as before, and the next question tries the counter again.
func TestAStoreWithoutTheCounterIsCountedEveryTime(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	ctx := context.Background()
	// A view holds the counter table's name: it cannot take the counter's row.
	if _, err := db.SQL().Exec(`CREATE VIEW embeddings_content_version AS SELECT 1 AS id, 0 AS version`); err != nil {
		t.Fatal(err)
	}
	counted := 0
	count := func([]string) (corpusStats, error) {
		counted++
		return corpusStats{n: 1, df: map[string]int{"wal": 1}}, nil
	}
	for range 2 {
		if _, err := db.cachedChunkStatsFor(ctx, "kb", []string{"wal"}, count); err != nil {
			t.Fatal(err)
		}
	}
	if counted != 2 {
		t.Fatalf("without the counter, 2 questions were counted %d times", counted)
	}
	if _, err := db.SQL().Exec(`DROP VIEW embeddings_content_version`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := db.cachedChunkStatsFor(ctx, "kb", []string{"wal"}, count); err != nil {
			t.Fatal(err)
		}
	}
	if counted != 3 {
		t.Fatalf("once the counter can be installed, 2 more questions made %d counts, want 1", counted-2)
	}
}
