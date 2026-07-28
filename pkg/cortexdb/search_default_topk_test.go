package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// top_k is optional in the tool schema, and an MCP caller — a model deciding which arguments to
// fill in — routinely leaves it out. Left unnormalised it reached the final truncation as 0 and cut
// the whole result set away, so search_text answered "nothing in this corpus" while the FTS index
// held dozens of matches. Every other test in this package passes TopK explicitly, which is exactly
// why nothing caught it.
func TestSearchTextWithoutTopKStillReturnsWhatMatched(t *testing.T) {
	dbPath := fmt.Sprintf("test_default_topk_%d.db", time.Now().UnixNano())
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

	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "book-calculus",
		Title:      "The Calculus Lifesaver",
		Content: "Continuity at a point means the limit equals the value. " +
			"A derivative is the limit of a difference quotient. " +
			"The chain rule differentiates a composition of functions.",
		Collection: "edumind-book-calculus",
		ChunkSize:  40,
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	withoutTopK, err := tools.SearchText(ctx, ToolSearchTextRequest{Query: "limit"})
	if err != nil {
		t.Fatalf("search without top_k: %v", err)
	}
	if len(withoutTopK.Chunks) == 0 {
		t.Fatal("top_k omitted and every match was dropped; the corpus does contain the term")
	}

	// Scoped the same way, because the collection filter is applied after the truncation.
	scoped, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query:      "limit",
		Collection: "edumind-book-calculus",
	})
	if err != nil {
		t.Fatalf("scoped search without top_k: %v", err)
	}
	if len(scoped.Chunks) == 0 {
		t.Fatal("top_k omitted with a collection named; every match was dropped")
	}

	// An explicit top_k must still cap, or the fix would have traded one wrong answer for another.
	capped, err := tools.SearchText(ctx, ToolSearchTextRequest{Query: "limit", TopK: 1})
	if err != nil {
		t.Fatalf("capped search: %v", err)
	}
	if len(capped.Chunks) != 1 {
		t.Fatalf("asked for 1 chunk, got %d", len(capped.Chunks))
	}
}
