package cortexdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// lexicalBackends opens the same empty brain on each backend this run covers,
// with no embedder, so a lexical test states one behaviour and runs it twice.
func lexicalBackends(t *testing.T) map[string]*DB {
	t.Helper()
	out := map[string]*DB{}
	sqlite, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "lexical.db")))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	out["sqlite"] = sqlite
	// Checked here rather than left to openPostgresBrain, whose Skip would
	// skip the SQLite half as well.
	if os.Getenv("CORTEXDB_TEST_POSTGRES") != "" {
		out["postgres"] = openPostgresBrain(t, 1536)
	} else {
		t.Log("CORTEXDB_TEST_POSTGRES unset — PostgreSQL parity NOT covered by this run")
	}
	for name, db := range out {
		if db.HasEmbedder() {
			t.Fatalf("%s: a lexical test must run without an embedder", name)
		}
	}
	return out
}

// A keyword search ranks the document about the term above the document that
// mentions it in passing.
//
// ftsSearch turned FTS5's bm25() — negative, and lower for a better match —
// into a score with 1/(1+(-bm25)), which is highest for the weakest match. The
// ORDER BY in SQL handed rows back best first and the merge then re-sorted
// them worst first. The memory path had the same inversion and was fixed; this
// path was not, and it is the one a caller reaches by passing keywords, which
// is what every tool description tells an agent to do when there is no
// embedder.
func TestAKeywordSearchRanksTheDocumentAboutTheTermFirst(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			docs := map[string]string{
				"about-raft":      "Raft is a consensus algorithm. Raft elects a leader, and the Raft log is replicated by the Raft leader.",
				"passing-mention": "A long note about gardening, cooking, travel, rivers and many other hobbies, which mentions raft once near the end of a paragraph about building a wooden boat for the summer.",
			}
			for i := 0; i < 20; i++ {
				docs[fmt.Sprintf("filler-%02d", i)] = fmt.Sprintf("Unrelated note %d about postgres indexes and query plans.", i)
			}
			for id, content := range docs {
				if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Title: id, Content: content}); err != nil {
					t.Fatalf("save %s: %v", id, err)
				}
			}

			res, err := db.SearchTextOnly(ctx, "raft", TextSearchOptions{TopK: 5, Keywords: []string{"raft"}})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(res) < 2 {
				t.Fatalf("got %d results, want both documents that mention raft", len(res))
			}
			if res[0].DocID != "about-raft" {
				t.Fatalf("first = %s (%.4f), second = %s (%.4f); the document about raft must rank first",
					res[0].DocID, res[0].Score, res[1].DocID, res[1].Score)
			}
			if res[0].Score <= res[1].Score {
				t.Fatalf("scores %.4f then %.4f; a better match must score higher, not only sort earlier", res[0].Score, res[1].Score)
			}
		})
	}
}
