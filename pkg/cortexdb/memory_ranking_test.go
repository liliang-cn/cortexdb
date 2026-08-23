package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// FTS5 bm25() is negative and a better match is more negative. Turning it into a
// score with 1/(1+|rank|) inverted the ordering, and because the sort is
// descending the store answered every recall with its weakest matches: a long
// memory that mentioned the term once beat a short one that was about nothing
// else. Recall looked like it was returning noise because it was.
func TestSearchMemoryRanksBestMatchFirst(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "rank.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Documents without the term give the term an IDF worth discriminating on;
	// without them bm25 is ~0 for every row and the test proves nothing.
	filler := strings.Repeat("unrelated prose about deployment pipelines and dashboards. ", 60)
	for _, m := range []struct{ id, content string }{
		{"strong", "gateway gateway gateway gateway gateway"},
		{"weak", filler + " gateway " + filler},
		{"noise1", filler},
		{"noise2", filler},
		{"noise3", filler},
	} {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{
			MemoryID: m.id, Scope: "global", Content: m.content,
		}); err != nil {
			t.Fatalf("save %s: %v", m.id, err)
		}
	}

	res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "gateway", Scope: "global", TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("expected the two memories containing the term, got %d", len(res.Results))
	}
	if got := res.Results[0].Memory.ID; got != "strong" {
		t.Errorf("best match ranked second: got %q first, want %q", got, "strong")
	}
	if res.Results[0].Score <= res.Results[1].Score {
		t.Errorf("scores do not follow bm25: strong=%v weak=%v",
			res.Results[0].Score, res.Results[1].Score)
	}
}
