package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// `collection` is a top-level parameter of the same call and also lives at plan.filters.collection,
// so a model reaches for plan.collection — and with additionalProperties:false that was a hard
// schema rejection, costing a whole model round trip on a scoped search before it guessed again.
func TestPlanCollectionShorthandIsAccepted(t *testing.T) {
	dbPath := fmt.Sprintf("test_plan_collection_%d.db", testname.Nano())
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
		{"book-ddia", "book-ddia", "Linearizability is the strongest consistency model in common use."},
		{"book-calculus", "book-calculus", "Linearizability of a differential operator is a separate idea entirely."},
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

	// The shorthand alone scopes the search.
	shorthand, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Plan: &RetrievalPlan{Query: "linearizability", Collection: "book-ddia"},
		TopK: 10,
	})
	if err != nil {
		t.Fatalf("shorthand search: %v", err)
	}
	if len(shorthand.Chunks) == 0 {
		t.Fatal("plan.collection did not scope to a collection that has the term")
	}
	for _, chunk := range shorthand.Chunks {
		if chunk.DocumentID != "book-ddia" {
			t.Errorf("plan.collection was ignored: got a chunk of %s", chunk.DocumentID)
		}
	}

	// It is shorthand for the filter, so the resolved plan reports it where a filter would be.
	if shorthand.Plan.Filters == nil || shorthand.Plan.Filters.Collection != "book-ddia" {
		t.Errorf("expected the shorthand folded into filters, got %+v", shorthand.Plan.Filters)
	}

	// The explicit filter is the more specific spelling and wins over the shorthand.
	specific, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Plan: &RetrievalPlan{
			Query:      "linearizability",
			Collection: "book-ddia",
			Filters:    &RetrievalFilters{Collection: "book-calculus"},
		},
		TopK: 10,
	})
	if err != nil {
		t.Fatalf("specific search: %v", err)
	}
	for _, chunk := range specific.Chunks {
		if chunk.DocumentID != "book-calculus" {
			t.Errorf("plan.filters.collection must win over plan.collection: got %s", chunk.DocumentID)
		}
	}

	// And the request's own collection still outranks both, as it did before.
	outer, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Collection: "book-calculus",
		Plan:       &RetrievalPlan{Query: "linearizability", Collection: "book-ddia"},
		TopK:       10,
	})
	if err != nil {
		t.Fatalf("outer search: %v", err)
	}
	for _, chunk := range outer.Chunks {
		if chunk.DocumentID != "book-calculus" {
			t.Errorf("the request's collection must win: got %s", chunk.DocumentID)
		}
	}
}
