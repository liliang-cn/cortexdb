package core

import (
	"context"
	"errors"
	"testing"
)

// Two things a caller reads off a store that PostgreSQL was not returning, and
// that no test asked for because the SQLite store had always supplied them.

// SaveKnowledge asks for the document to find out whether this is a create or
// an update, and reads the answer with errors.Is(err, ErrNotFound). PostgreSQL
// spelled the miss as a bare fmt.Errorf, so "this is new" was indistinguishable
// from a real failure and saving any new knowledge returned an error instead of
// writing it.
func TestMissingDocumentIsErrNotFoundEverywhere(t *testing.T) {
	ctx := context.Background()
	for _, s := range storesUnderTest(t) {
		t.Run(s.name, func(t *testing.T) {
			doc, err := s.store.GetDocument(ctx, "no-such-document")
			if err == nil {
				t.Fatalf("got document %+v, want a not-found error", doc)
			}
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("errors.Is(err, ErrNotFound) is false for %v — a caller "+
					"cannot tell a miss from a failure", err)
			}
		})
	}
}

// Search results carry the collection NAME, not just its id. Callers route on
// the name: cortex_query reports it and retrieval filters by it. PostgreSQL had
// three hand-written projections and one of them had dropped the column, so a
// plain vector search returned rows whose Collection was empty while the same
// rows from the keyword arm carried it.
func TestSearchResultsCarryTheCollectionName(t *testing.T) {
	ctx := context.Background()
	for _, s := range storesUnderTest(t) {
		t.Run(s.name, func(t *testing.T) {
			if _, err := s.store.CreateCollection(ctx, "named", 4); err != nil {
				t.Fatalf("create collection: %v", err)
			}
			if err := s.store.Upsert(ctx, &Embedding{
				ID: "row-1", Vector: []float32{1, 0, 0, 0},
				Content: "in a named collection", Collection: "named",
			}); err != nil {
				t.Fatalf("upsert: %v", err)
			}

			hits, err := s.store.Search(ctx, []float32{1, 0, 0, 0}, SearchOptions{TopK: 5})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			found := false
			for _, h := range hits {
				if h.ID != "row-1" {
					continue
				}
				found = true
				if h.Collection != "named" {
					t.Errorf("Collection = %q, want %q", h.Collection, "named")
				}
			}
			if !found {
				t.Fatal("the row just written was not returned")
			}
		})
	}
}
