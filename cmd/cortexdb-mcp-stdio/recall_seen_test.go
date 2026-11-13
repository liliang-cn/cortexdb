package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seenIn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := recallSeenDir
	recallSeenDir = func() string { return dir }
	t.Cleanup(func() { recallSeenDir = old })
	return dir
}

func ids(hits []recallHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.KnowledgeID
	}
	return out
}

func sameIDs(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The guide that matches every prompt of a session is injected once; the hit
// that is new on the second prompt still comes through.
func TestAHitTheSessionWasGivenIsNotGivenAgain(t *testing.T) {
	seenIn(t)
	guide, a, b := recallHit{KnowledgeID: "guide", Snippet: "g"}, recallHit{KnowledgeID: "a", Snippet: "a"}, recallHit{KnowledgeID: "b", Snippet: "b"}

	if got := ids(unseenHits("s1", []recallHit{guide, a})); !sameIDs(got, "guide", "a") {
		t.Fatalf("first prompt: %v", got)
	}
	if got := ids(unseenHits("s1", []recallHit{guide, b})); !sameIDs(got, "b") {
		t.Fatalf("second prompt: %v", got)
	}
	if got := ids(unseenHits("s1", []recallHit{guide, a, b})); len(got) != 0 {
		t.Fatalf("third prompt: %v", got)
	}
	// Another session has been given nothing.
	if got := ids(unseenHits("s2", []recallHit{guide})); !sameIDs(got, "guide") {
		t.Fatalf("another session: %v", got)
	}
}

// After recallRepeatAfter prompts the first copy may be compacted away, so the
// hit comes back.
func TestAHitComesBackOnceTheFirstCopyIsFarBehind(t *testing.T) {
	seenIn(t)
	guide := recallHit{KnowledgeID: "guide", Snippet: "g"}
	unseenHits("s1", []recallHit{guide})
	for i := 1; i < recallRepeatAfter; i++ {
		if got := unseenHits("s1", []recallHit{guide}); len(got) != 0 {
			t.Fatalf("prompt %d repeated the hit", i+1)
		}
	}
	if got := ids(unseenHits("s1", []recallHit{guide})); !sameIDs(got, "guide") {
		t.Fatalf("after %d prompts: %v", recallRepeatAfter, got)
	}
}

// With no session id or no cache directory the hook repeats itself rather
// than going quiet.
func TestWithoutASessionEveryHitIsKept(t *testing.T) {
	dir := seenIn(t)
	guide := recallHit{KnowledgeID: "guide", Snippet: "g"}
	for i := 0; i < 2; i++ {
		if got := unseenHits("", []recallHit{guide}); len(got) != 1 {
			t.Fatalf("no session id: %v", got)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("wrote %d records without a session", len(entries))
	}
	recallSeenDir = func() string { return "" }
	for i := 0; i < 2; i++ {
		if got := unseenHits("s1", []recallHit{guide}); len(got) != 1 {
			t.Fatalf("no cache dir: %v", got)
		}
	}
}

// A session id is somebody else's string: it never names a path outside the
// directory, and a finished session's record is removed.
func TestSessionRecordsStayInTheirDirectoryAndExpire(t *testing.T) {
	dir := seenIn(t)
	unseenHits("../../escape", []recallHit{{KnowledgeID: "x", Snippet: "x"}})
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("records: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.json")); err == nil {
		t.Fatal("the session id escaped the directory")
	}
	stale := filepath.Join(dir, entries[0].Name())
	long := time.Now().Add(-recallSeenKeep - time.Hour)
	if err := os.Chtimes(stale, long, long); err != nil {
		t.Fatal(err)
	}
	unseenHits("fresh", []recallHit{{KnowledgeID: "y", Snippet: "y"}})
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("a finished session's record was kept")
	}
}
