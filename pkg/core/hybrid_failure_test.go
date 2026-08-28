package core

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// A lexical-only search whose one arm cannot run must fail, not return nothing.
//
// `HybridSearch` used to build the keyword query, hand it to SQLite, and ignore the error: `if err ==
// nil { … }`. With no vector supplied the keyword arm is the only arm, so any error there produced an
// empty result set and a nil error — indistinguishable from a corpus that genuinely contains nothing.
// That is the shape of failure that cost a day of hunting through databases for a bug that was in the
// query, and it is unfalsifiable from outside: every caller sees success.
func TestLexicalOnlySearchFailsLoudlyWhenItsOnlyArmCannotRun(t *testing.T) {
	dbPath := fmt.Sprintf("test_hybrid_failure_%d.db", testname.Nano())
	store, err := New(dbPath, 3)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})

	if err := store.Upsert(ctx, &Embedding{
		ID:      "chunk:1",
		Vector:  []float32{1, 0, 0},
		Content: "Continuity at a point means the limit equals the value.",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// A control first: the same call over an intact index does find the row, so the failure below is
	// about the broken index and not about the query or the fixture.
	found, err := store.HybridSearch(ctx, nil, "continuity", HybridSearchOptions{
		SearchOptions: SearchOptions{TopK: 5},
	})
	if err != nil {
		t.Fatalf("control search: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("the control found nothing; the fixture is wrong, not the code")
	}

	// Break the FTS index out from under the query — what a corrupted or half-migrated database looks
	// like to this code path.
	if _, err := store.GetDB().ExecContext(ctx, "DROP TABLE chunks_fts"); err != nil {
		t.Fatalf("drop fts: %v", err)
	}

	_, err = store.HybridSearch(ctx, nil, "continuity", HybridSearchOptions{
		SearchOptions: SearchOptions{TopK: 5},
	})
	if err == nil {
		t.Fatal("the only arm of the search could not run, and it reported success with no results")
	}
}

// With a vector arm still answering, the same breakage degrades instead of failing: half an answer beats
// none. It is logged rather than silent, because vector-only results look exactly like ordinary ones.
func TestHybridSearchDegradesWhenTheKeywordArmBreaksButTheVectorArmAnswers(t *testing.T) {
	dbPath := fmt.Sprintf("test_hybrid_degrade_%d.db", testname.Nano())
	store, err := New(dbPath, 3)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})

	if err := store.Upsert(ctx, &Embedding{
		ID:      "chunk:1",
		Vector:  []float32{1, 0, 0},
		Content: "Continuity at a point means the limit equals the value.",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := store.GetDB().ExecContext(ctx, "DROP TABLE chunks_fts"); err != nil {
		t.Fatalf("drop fts: %v", err)
	}

	results, err := store.HybridSearch(ctx, []float32{1, 0, 0}, "continuity", HybridSearchOptions{
		SearchOptions: SearchOptions{TopK: 5},
	})
	if err != nil {
		t.Fatalf("a working vector arm should still answer: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("the vector arm found nothing, so the degraded path returned nothing")
	}
}
