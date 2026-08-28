package cortexdb

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// TestPropertyDistinctiveTokenIsFindable asserts a core retrieval invariant
// across many generated documents: a document carrying a unique distinctive
// token is retrieved by lexical search for that token. This guards tokenizer /
// FTS indexing regressions (including CJK and punctuation-adjacent content).
func TestPropertyDistinctiveTokenIsFindable(t *testing.T) {
	dbPath := fmt.Sprintf("test_prop_%d.db", testname.Nano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	ctx := context.Background()

	filler := []string{
		"deployment", "vector", "graph", "memory", "token", "search", "index",
		"关系", "记忆", "图谱", "auth", "port", "config", "pipeline", "service",
	}
	rng := rand.New(rand.NewSource(42)) // deterministic

	const n = 60
	markers := make([]string, n)
	for i := 0; i < n; i++ {
		marker := fmt.Sprintf("zmarker%04d", i) // unique, distinctive
		markers[i] = marker
		var sb strings.Builder
		sb.WriteString(marker)
		for j := 0; j < 8; j++ {
			sb.WriteByte(' ')
			sb.WriteString(filler[rng.Intn(len(filler))])
		}
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
			KnowledgeID: fmt.Sprintf("doc-%d", i),
			Content:     sb.String(),
		}); err != nil {
			t.Fatalf("save doc-%d: %v", i, err)
		}
	}

	misses := 0
	for i, marker := range markers {
		resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
			Query:         marker,
			RetrievalMode: RetrievalModeLexical,
			TopK:          5,
		})
		if err != nil {
			t.Fatalf("search %q: %v", marker, err)
		}
		want := fmt.Sprintf("doc-%d", i)
		found := false
		for _, hit := range resp.Results {
			if hit.KnowledgeID == want {
				found = true
				break
			}
		}
		if !found {
			misses++
			if misses <= 5 {
				t.Errorf("distinctive token %q did not retrieve %q (got %d results)", marker, want, len(resp.Results))
			}
		}
	}
	if misses > 0 {
		t.Fatalf("%d/%d distinctive tokens were not findable", misses, n)
	}
}
