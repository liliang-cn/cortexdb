package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// Two collections, one query. The collection a caller names must be the only one they get back,
// and a caller who names none must not be quietly confined to whichever collection ingest writes
// into by default.
func TestSearchHonoursTheCollectionAskedForAndNothingMore(t *testing.T) {
	dbPath := fmt.Sprintf("test_collection_scope_%d.db", testname.Nano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	ctx := context.Background()
	tools := db.GraphRAGTools()

	for _, book := range []struct{ id, collection, content string }{
		{"book-calculus", "edumind-book-calculus", "Continuity at a point means the limit equals the value."},
		{"book-chinese", "edumind-book-chinese", "Continuity of a narrative keeps a reader inside the story."},
	} {
		if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
			DocumentID: book.id,
			Title:      book.id,
			Content:    book.content,
			Collection: book.collection,
			ChunkSize:  40,
		}); err != nil {
			t.Fatalf("ingest %s: %v", book.id, err)
		}
	}

	// Scoped: a learner bound to one book must never be quoted the other one's text.
	scoped, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query:      "continuity",
		Collection: "edumind-book-chinese",
		TopK:       10,
	})
	if err != nil {
		t.Fatalf("scoped search: %v", err)
	}
	if len(scoped.Chunks) == 0 {
		t.Fatal("the book's own chunk was not returned")
	}
	for _, chunk := range scoped.Chunks {
		if chunk.DocumentID != "book-chinese" {
			t.Errorf("asked for edumind-book-chinese, got a chunk of %s: %q", chunk.DocumentID, chunk.Content)
		}
	}

	// Unscoped: reaches both books.
	unscoped, err := tools.SearchText(ctx, ToolSearchTextRequest{Query: "continuity", TopK: 10})
	if err != nil {
		t.Fatalf("unscoped search: %v", err)
	}
	seen := map[string]bool{}
	for _, chunk := range unscoped.Chunks {
		seen[chunk.DocumentID] = true
	}
	if !seen["book-calculus"] || !seen["book-chinese"] {
		t.Errorf("an unscoped search should span collections, reached %v", seen)
	}

	// The same, through SearchKnowledge — the path that used to substitute a default collection
	// when the caller named none, confining every unscoped search to the one collection ingest
	// writes into. Neither book lives there, so a reinstated default shows up here as no hits.
	knowledge, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
		Query:         "continuity",
		RetrievalMode: RetrievalModeLexical,
		TopK:          10,
	})
	if err != nil {
		t.Fatalf("unscoped knowledge search: %v", err)
	}
	found := map[string]bool{}
	for _, hit := range knowledge.Results {
		found[hit.KnowledgeID] = true
	}
	if !found["book-calculus"] || !found["book-chinese"] {
		t.Errorf("an unscoped knowledge search should span collections, reached %v", found)
	}
}
