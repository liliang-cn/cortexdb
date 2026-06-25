package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestLexicalSearchHandlesFTSOperatorChars guards against a regression where a
// query containing FTS5 syntax (notably a ':' column filter, e.g. "user:")
// reached MATCH verbatim and failed with "no such column: user". Both memory
// and knowledge lexical paths flow through lexicalSearchQueries, so both must
// tolerate arbitrary natural-language input.
func TestLexicalSearchHandlesFTSOperatorChars(t *testing.T) {
	dbPath := fmt.Sprintf("test_fts_sanitize_%d.db", time.Now().UnixNano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
	})

	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID:  "m1",
		UserID:    "alice",
		Scope:     MemoryScopeUser,
		Namespace: "assistant",
		Content:   "The user prefers dark mode and port 3076.",
	}); err != nil {
		t.Fatalf("save memory: %v", err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "k1",
		Content:     "Deployment wiring: the BACKEND_URL pitfall and auth login flow.",
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}

	// Each of these contains FTS5-significant punctuation that previously broke
	// the bare-MATCH path.
	queries := []string{
		"user: prefers",
		"deployment: wiring",
		"BACKEND_URL: the pitfall",
		"auth/login flow",
		"port (3076)",
		"-dark mode",
	}
	for _, q := range queries {
		if _, err := db.SearchMemory(ctx, MemorySearchRequest{
			Query:     q,
			UserID:    "alice",
			Scope:     MemoryScopeUser,
			Namespace: "assistant",
			TopK:      5,
		}); err != nil {
			t.Errorf("SearchMemory(%q) returned error: %v", q, err)
		}
		if _, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
			Query:         q,
			RetrievalMode: RetrievalModeLexical,
			TopK:          5,
		}); err != nil {
			t.Errorf("SearchKnowledge(%q) returned error: %v", q, err)
		}
	}

	// Sanity: a colon query that targets the stored memory still matches it.
	resp, err := db.SearchMemory(ctx, MemorySearchRequest{
		Query:     "user: dark mode",
		UserID:    "alice",
		Scope:     MemoryScopeUser,
		Namespace: "assistant",
		TopK:      5,
	})
	if err != nil {
		t.Fatalf("SearchMemory colon query: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Errorf("expected the colon query to still match the stored memory, got 0 results")
	}
}

func TestSanitizeFTSQuery(t *testing.T) {
	cases := map[string]string{
		"user: prefers":   `"user:" "prefers"`,
		"hello world":     `"hello" "world"`,
		"  spaced  out  ": `"spaced" "out"`,
		":":               "",
		"":                "",
		`say "hi"`:        `"say" """hi"""`,
		"port (3076)":     `"port" "(3076)"`,
	}
	for in, want := range cases {
		if got := sanitizeFTSQuery(in); got != want {
			t.Errorf("sanitizeFTSQuery(%q) = %q, want %q", in, got, want)
		}
	}
}
