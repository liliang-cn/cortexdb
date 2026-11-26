package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestRepoRelativeNamesAFileFromItsRepositoryRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CortexDB")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg", "cortexdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := repoRelative(filepath.Join(root, "pkg", "cortexdb", "brain.go"), "/elsewhere"); got != "pkg/cortexdb/brain.go" {
		t.Fatalf("absolute: %q", got)
	}
	if got := repoRelative("pkg/cortexdb/brain.go", root); got != "pkg/cortexdb/brain.go" {
		t.Fatalf("relative to cwd: %q", got)
	}
}

func assistantToolUse(tool, path string) map[string]any {
	return map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []map[string]any{
		{"type": "tool_use", "name": tool, "input": map[string]any{"file_path": path}},
	}}}
}

func TestDigestListsTheFilesASessionTouched(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, jsonl(t,
		claudeTurn("user", "fix the race"),
		assistantToolUse("Read", filepath.Join(root, "router.go")),
		assistantToolUse("Read", filepath.Join(root, "README.md")),
		assistantToolUse("Edit", filepath.Join(root, "router.go")),
		assistantToolUse("Bash", "/bin/ls"),
		claudeTurn("assistant", "Fixed it."),
	), time.Time{})
	digest, _, err := digestTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "FILES (paths from the repository root)\nmodified: router.go\nread: README.md\n\nUSER: fix the race"
	if !strings.HasPrefix(digest, want) {
		t.Fatalf("digest:\n%s", digest)
	}
}

func TestFileIndexListsTheFilesMemoriesOncePerSession(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "brain.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	file := []cortexdb.ToolEntityInput{{Name: "pkg/semantic-router/embedder.go", Type: "file"}}
	for _, m := range []cortexdb.MemorySaveRequest{
		{MemoryID: "race", Scope: "global", Content: "CachedEmbedder needs its mutex: concurrent Embed raced on the map (fixed 2026-10-13).", Entities: file},
		{MemoryID: "other", Scope: "global", Content: "The router threshold defaults to 0.5."},
	} {
		if _, err := db.SaveMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	call := localToolCaller(db)
	block := fileIndex(ctx, call, "pkg/semantic-router/embedder.go")
	if !strings.Contains(block, "- race ") || !strings.Contains(block, "CachedEmbedder needs its mutex") || strings.Contains(block, "threshold") {
		t.Fatalf("block:\n%s", block)
	}
	if got := fileIndex(ctx, call, "pkg/unknown.go"); got != "" {
		t.Fatalf("unknown file: %q", got)
	}

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if !firstLookup("s1", "a.go") || firstLookup("s1", "a.go") || !firstLookup("s2", "a.go") || !firstLookup("s1", "b.go") {
		t.Fatal("a file is looked up once per session")
	}
}

func TestDuplicateFactsAreNotSavedTwice(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "brain.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "auto:aaaa:uv", Scope: "global", Content: "The user sets up Python with uv."}); err != nil {
		t.Fatal(err)
	}
	brain := localBrain{db}
	same := cortexdb.MemorySaveRequest{MemoryID: "auto:bbbb:uv", Scope: "global", Content: "the user sets up python with UV"}
	if got := duplicateOf(ctx, brain, same); got != "auto:aaaa:uv" {
		t.Fatalf("duplicate not found: %q", got)
	}
	// Its own id is an update, not a duplicate; different words are a new fact.
	if got := duplicateOf(ctx, brain, cortexdb.MemorySaveRequest{MemoryID: "auto:aaaa:uv", Scope: "global", Content: "The user sets up Python with uv."}); got != "" {
		t.Fatalf("own id: %q", got)
	}
	if got := duplicateOf(ctx, brain, cortexdb.MemorySaveRequest{MemoryID: "auto:cccc:pip", Scope: "global", Content: "The user sets up Python with pip."}); got != "" {
		t.Fatalf("different fact: %q", got)
	}
}
