package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The memory directory is the transcript's sibling, or else the one Claude
// Code names after the session's cwd.
func TestTheFileMemoryIsFoundBesideTheTranscriptOrByTheProjectPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	project := filepath.Join(home, ".claude", "projects", "-Users-a-Works-my-app")
	writeFile(t, filepath.Join(project, "memory", "MEMORY.md"), "- index\n")
	transcript := filepath.Join(project, "s1.jsonl")
	writeFile(t, transcript, "")

	if got := fileMemoryDir(transcript, ""); got != filepath.Join(project, "memory") {
		t.Fatalf("beside the transcript: %q", got)
	}
	if got := fileMemoryDir(filepath.Join(t.TempDir(), "elsewhere.jsonl"), "/Users/a/Works/my.app"); got != filepath.Join(project, "memory") {
		t.Fatalf("by the project path: %q", got)
	}
	if got := fileMemoryDir(filepath.Join(t.TempDir(), "elsewhere.jsonl"), "/Users/a/other"); got != "" {
		t.Fatalf("a project with no file memory: %q", got)
	}
}

func TestTheFileMemoryIsShownWholeOrByItsIndex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "MEMORY.md"), "- [Deploy](deploy.md) — the index, not a memory\n")
	writeFile(t, filepath.Join(dir, "deploy.md"), "---\nname: deploy-all-nodes\ndescription: deploy to all three nodes, then evict the controller\nmetadata:\n  type: project\n---\n\nCopy to a, b and e.\nBack up to /var/backups/gui first.\n")
	writeFile(t, filepath.Join(dir, "plain.md"), "AutoHTTPS turns :3370 into a 302 to :3371.\n")

	memories := readFileMemory(dir)
	if len(memories) != 2 {
		t.Fatalf("memories = %+v, want two (MEMORY.md is the index)", memories)
	}
	got := recordedInFiles(memories)
	for _, want := range []string{
		"- deploy-all-nodes: deploy to all three nodes, then evict the controller\n  Copy to a, b and e.\n  Back up to /var/backups/gui first.",
		"- plain\n  AutoHTTPS turns :3370 into a 302 to :3371.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "type: project") || strings.Contains(got, "the index, not a memory") {
		t.Errorf("frontmatter or index shown:\n%s", got)
	}

	// A body that does not fit is left out; its name and description stay,
	// and the bodies that fit are still shown.
	memories[0].Body = strings.Repeat("long ", fileMemoryRunes)
	brief := recordedInFiles(memories)
	if strings.Contains(brief, "long long") || !strings.Contains(brief, "- deploy-all-nodes: deploy to all three nodes") {
		t.Errorf("over the budget, got:\n%s", clip(brief, 300))
	}
	if !strings.Contains(brief, "- plain\n  AutoHTTPS turns :3370 into a 302 to :3371.") {
		t.Errorf("a body that fits was dropped:\n%s", clip(brief, 300))
	}
	if recordedInFiles(nil) != "" {
		t.Error("no files, yet something to show")
	}
}

type promptRecorder struct{ user string }

func (p *promptRecorder) GenerateJSON(_ context.Context, _, user string) ([]byte, error) {
	p.user = user
	return []byte(`{"memories":[]}`), nil
}

// The extractor is told what the file memory already says; without one, the
// prompt is what it was.
func TestTheCaptureIsToldWhatTheFileMemoryAlreadySays(t *testing.T) {
	llm := &promptRecorder{}
	day := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	if _, err := extractSessionFacts(context.Background(), llm, "USER: deploy it", "- deploy-all-nodes: deploy to all three nodes", day, cortexdb.ExtractorTypeVocabulary{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.user, "ALREADY RECORDED") || !strings.Contains(llm.user, "- deploy-all-nodes: deploy to all three nodes") {
		t.Fatalf("prompt does not carry the file memory:\n%s", llm.user)
	}
	if strings.Index(llm.user, "ALREADY RECORDED") > strings.Index(llm.user, "Transcript digest:") {
		t.Fatalf("the file memory comes after the transcript:\n%s", llm.user)
	}
	if _, err := extractSessionFacts(context.Background(), llm, "USER: deploy it", "", day, cortexdb.ExtractorTypeVocabulary{}); err != nil {
		t.Fatal(err)
	}
	if want := "Today is 2026-11-09.\n\nTranscript digest:\nUSER: deploy it"; llm.user != want {
		t.Fatalf("prompt without a file memory = %q, want %q", llm.user, want)
	}
	if !strings.Contains(captureSystemPrompt, "ALREADY RECORDED") {
		t.Fatal("the system prompt does not say what ALREADY RECORDED is")
	}

	// Only a Claude Code session has a file memory.
	if got := (transcript{Kind: "dsh", Path: "/x/session.v1.jsonl"}).recorded("/Users/a/Works/my.app"); got != "" {
		t.Fatalf("a dsh session got a file memory: %q", got)
	}
}
