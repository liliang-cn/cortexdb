package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// FuzzLexicalSearchNeverErrors throws arbitrary text at the lexical memory and
// knowledge search paths. Both must tolerate any input — no FTS5 syntax error
// (the "no such column" class), no SQL error, no panic. Seeded with the
// operator characters that historically broke MATCH.
func FuzzLexicalSearchNeverErrors(f *testing.F) {
	dbPath := fmt.Sprintf("fuzz_fts_%d.db", time.Now().UnixNano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		f.Fatalf("open: %v", err)
	}
	f.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "m1", UserID: "u", Scope: MemoryScopeUser, Namespace: "assistant",
		Content: "The user prefers dark mode and port 3076. auth login flow.",
	}); err != nil {
		f.Fatalf("save memory: %v", err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "k1", Content: "Deployment wiring: BACKEND_URL pitfall and auth login.",
	}); err != nil {
		f.Fatalf("save knowledge: %v", err)
	}

	for _, seed := range []string{
		"", "user:", "a:b:c", "col: term", "\"unterminated", "AND OR NOT",
		"foo*", "-bar", "^caret", "(paren)", "a AND (b OR c)", "NEAR(x y)",
		"路径/斜杠", "emoji 🚀 test", "tab\tnewline\n", "  ", "'; DROP TABLE x;--",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, query string) {
		if _, err := db.SearchMemory(ctx, MemorySearchRequest{
			Query: query, UserID: "u", Scope: MemoryScopeUser, Namespace: "assistant", TopK: 5,
		}); err != nil && !isAcceptableSearchErr(err) {
			t.Fatalf("SearchMemory(%q) errored: %v", query, err)
		}
		if _, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
			Query: query, RetrievalMode: RetrievalModeLexical, TopK: 5,
		}); err != nil && !isAcceptableSearchErr(err) {
			t.Fatalf("SearchKnowledge(%q) errored: %v", query, err)
		}
	})
}

// isAcceptableSearchErr allows the well-defined empty-query error; anything else
// (SQL/FTS errors, panics) is a real defect.
func isAcceptableSearchErr(err error) bool {
	return err == ErrEmptyText || strings.Contains(err.Error(), "empty")
}
