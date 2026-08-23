package agentmem_test

import (
	"context"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/agentmem"
)

// The text component of the score is derived from bm25(), which is negative and
// falls as the match improves. Feeding |rank| through 1/(1+|rank|) reversed that,
// so applyBoosts ranked the weakest match highest. Importance is held equal here
// so the assertion is about the text term alone.
func TestSearchByTextRanksBestMatchFirst(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "alice"}
	filler := strings.Repeat("unrelated prose about deployment pipelines and dashboards. ", 60)
	for _, m := range []struct{ id, content string }{
		{"strong", "gateway gateway gateway gateway gateway"},
		{"weak", filler + " gateway " + filler},
		{"noise1", filler},
		{"noise2", filler},
		{"noise3", filler},
	} {
		if err := store.Save(ctx, &agentmem.Memory{
			ID:         m.id,
			Scope:      scope,
			Type:       agentmem.TypeFact,
			Content:    m.content,
			Importance: 0.5,
		}); err != nil {
			t.Fatalf("save %s: %v", m.id, err)
		}
	}

	hits, err := store.SearchByText(ctx, "gateway", agentmem.SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected the two memories containing the term, got %d", len(hits))
	}
	if got := hits[0].Memory.ID; got != "strong" {
		t.Errorf("best match ranked second: got %q first, want %q", got, "strong")
	}
	if hits[0].Score <= hits[1].Score {
		t.Errorf("scores do not follow bm25: strong=%v weak=%v", hits[0].Score, hits[1].Score)
	}
}
