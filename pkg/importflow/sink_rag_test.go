package importflow

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func testDB(t *testing.T) *cortexdb.DB {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "test.db")))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRAGSinkFlush(t *testing.T) {
	db := testDB(t)
	sink := newRAGSink(db, 2)
	ctx := context.Background()

	chunks := []ragChunk{
		{id: "1", content: "Ada Lovelace wrote the first algorithm", metadata: map[string]string{"_table": "people"}},
		{id: "2", content: "Alan Turing broke the Enigma code", metadata: map[string]string{"_table": "people"}},
	}
	for _, c := range chunks {
		if err := sink.add(ctx, c); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	if err := sink.flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sink.count() != 2 {
		t.Fatalf("count = %d; want 2", sink.count())
	}

	// No embedder configured -> lexical/FTS5 retrieval must still find it.
	res, err := db.SearchTextOnly(ctx, "Enigma", cortexdb.TextSearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected at least one FTS5 hit for 'Enigma'")
	}
}
