package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// Two characters is an ordinary Chinese word, and a lesson is usually about one: 乘法, 分数, 面积.
// The trigram index cannot hold a token that short, so MATCH finds none of them however many
// chunks contain the term — the search reports nothing rather than reporting it cannot look.
func TestShortCJKQueryStillFindsTheTermInTheCorpus(t *testing.T) {
	dbPath := fmt.Sprintf("test_short_cjk_%d.db", testname.Nano())
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
		{"book-maths", "book-maths", "两位数乘法的竖式计算，先算个位再算十位。"},
		{"book-chinese", "book-chinese", "语文园地里有识字加油站和日积月累。"},
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

	found, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query:      "乘法",
		Collection: "book-maths",
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("short query search: %v", err)
	}
	if len(found.Chunks) == 0 {
		t.Fatal("乘法 appears in the collection but the search returned nothing")
	}
	for _, chunk := range found.Chunks {
		if chunk.DocumentID != "book-maths" {
			t.Errorf("the short-query path must respect the collection too, got %s", chunk.DocumentID)
		}
	}

	// A term that is genuinely absent must still come back empty: the fallback widens how a term
	// is looked up, not what counts as a match.
	absent, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query:      "乘法",
		Collection: "book-chinese",
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("absent term search: %v", err)
	}
	if len(absent.Chunks) != 0 {
		t.Errorf("乘法 is not in the Chinese book, got %d hits", len(absent.Chunks))
	}

	// Three characters and up still go through the trigram index, unchanged.
	viaIndex, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query:      "语文园地",
		Collection: "book-chinese",
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("trigram search: %v", err)
	}
	if len(viaIndex.Chunks) == 0 {
		t.Error("语文园地 should still be found through the trigram index")
	}
}
