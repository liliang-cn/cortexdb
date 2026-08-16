package cortexdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func openLexicalTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "authorize.db")
	db, err := Open(DefaultConfig(dbPath), WithEmbedder(NewMockEmbedder(8)))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	return db
}

// seedTwoSourceCorpus writes `major` rows of one kind and `minor` of another,
// all on the same topic so they score alike — the shape that breaks a fixed
// over-fetch. The minority rows are written last so they rank no higher.
func seedTwoSourceCorpus(t *testing.T, db *DB, major, minor int) {
	t.Helper()
	ctx := context.Background()
	texts := make(map[string]string, major+minor)
	for i := 0; i < major; i++ {
		texts[fmt.Sprintf("major-%03d", i)] = fmt.Sprintf("quorum promoter failover replication detail %d", i)
	}
	if err := db.InsertTextBatch(ctx, texts, map[string]string{"kind": "major"}); err != nil {
		t.Fatalf("seed major: %v", err)
	}
	texts = make(map[string]string, minor)
	for i := 0; i < minor; i++ {
		texts[fmt.Sprintf("minor-%03d", i)] = fmt.Sprintf("quorum promoter failover replication runbook %d", i)
	}
	if err := db.InsertTextBatch(ctx, texts, map[string]string{"kind": "minor"}); err != nil {
		t.Fatalf("seed minor: %v", err)
	}
}

// TestAuthorizeFillsTopKWhenPredicateIsSelective pins the documented contract:
// "the search over-fetches internally so the caller still receives up to TopK
// authorized results". A single fixed over-fetch (max(TopK*5, 50)) broke it —
// with 200 majority rows scoring alike, no minority row reached rank 50 and the
// caller got an empty result that read as "nothing matched".
func TestAuthorizeFillsTopKWhenPredicateIsSelective(t *testing.T) {
	db := openLexicalTestDB(t)
	seedTwoSourceCorpus(t, db, 200, 8)

	got, err := db.HybridSearchTextWithOptions(context.Background(), "quorum promoter failover", TextSearchOptions{
		TopK: 5,
		Authorize: func(e core.ScoredEmbedding) bool {
			return strings.HasPrefix(e.ID, "minor-")
		},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) < 5 {
		t.Fatalf("got %d authorized results, want 5 — the widening did not reach the minority rows", len(got))
	}
	for _, r := range got {
		if !strings.HasPrefix(r.ID, "minor-") {
			t.Fatalf("unauthorized row %q passed the gate", r.ID)
		}
	}
}

// A predicate matching fewer rows than TopK returns what exists rather than
// looping to the fetch ceiling.
func TestAuthorizeReturnsWhatExistsWhenCorpusIsSmaller(t *testing.T) {
	db := openLexicalTestDB(t)
	seedTwoSourceCorpus(t, db, 60, 2)

	got, err := db.HybridSearchTextWithOptions(context.Background(), "quorum promoter failover", TextSearchOptions{
		TopK: 10,
		Authorize: func(e core.ScoredEmbedding) bool {
			return strings.HasPrefix(e.ID, "minor-")
		},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want the 2 that exist", len(got))
	}
}

// The common case — a generous predicate — must not pay for the widening.
func TestAuthorizeGenerousPredicateStillFillsTopK(t *testing.T) {
	db := openLexicalTestDB(t)
	seedTwoSourceCorpus(t, db, 60, 20)

	got, err := db.HybridSearchTextWithOptions(context.Background(), "quorum promoter failover", TextSearchOptions{
		TopK:      5,
		Authorize: func(core.ScoredEmbedding) bool { return true },
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d results, want 5", len(got))
	}
}
