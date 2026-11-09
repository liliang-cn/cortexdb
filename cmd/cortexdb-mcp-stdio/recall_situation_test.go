package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestAPromptIsAskedWithTheProjectAndTheLastReply(t *testing.T) {
	transcript := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"user","message":{"content":"linstor-server: resize leaves the old size"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Root cause: VolumeDefinition resize skips the satellite update.\nI suggest a fix plus a regression test."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"package a"}]}}`,
		`{"type":"user","message":{"content":"按建议 · 这个修复前后可以验证吗"}}`,
	}
	writeTranscript(t, transcript, lines)
	prompt := "按建议 · 这个修复前后可以验证吗"
	s := situationFor(hookInput{Prompt: prompt, Cwd: "/Users/a/Works/linstor-server", TranscriptPath: transcript})

	got := s.query(prompt)
	want := "按建议 · 这个修复前后可以验证吗\n\nProject: linstor-server\nContext: Root cause: VolumeDefinition resize skips the satellite update. I suggest a fix plus a regression test."
	if got != want {
		t.Fatalf("query =\n%q\nwant\n%q", got, want)
	}
	kw := s.keywords(prompt)
	if kw[len(kw)-1] != "linstor-server" {
		t.Fatalf("keywords %v do not name the project", kw)
	}
	if e := s.entities(); len(e) != 1 || e[0] != "linstor-server" {
		t.Fatalf("entities = %v", e)
	}
	if s.Image {
		t.Fatal("a prompt without a picture was seen as carrying one")
	}
}

// The reply a prompt follows is the one before the prompt's own entry when
// Claude Code has written it already, and the last one when it has not.
func TestTheLastReplyIsTheOneBeforeThePrompt(t *testing.T) {
	transcript := filepath.Join(t.TempDir(), "s.jsonl")
	writeTranscript(t, transcript, []string{
		`{"type":"user","message":{"content":"ok"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"first reply"}]}}`,
		`{"type":"user","message":{"content":"ok"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"second reply"}]}}`,
	})
	// "ok" was said before, but something answered it: this "ok" is not
	// written yet, and it follows the second reply.
	if s := situationFor(hookInput{Prompt: "ok", TranscriptPath: transcript}); s.Previous != "second reply" {
		t.Fatalf("previous = %q, want the last reply", s.Previous)
	}
}

// Turn B of #142: a screenshot of a DRBD resync and "what happened？". The
// CLI writes the picture into the prompt as [Image #1]; the desktop app does
// not, and only the prompt's own transcript entry has the image block.
func TestAPromptWithAnImageIsSeenFromEitherApp(t *testing.T) {
	for _, p := range []string{"[Image #1] what happened？", "看下这个 [Image #2]", "[Image #1][Image #2] 对比一下"} {
		if !carriesImage(p) {
			t.Errorf("%q not seen as carrying an image", p)
		}
	}
	for _, p := range []string{"what happened?", "the [Image] tag in markdown", "Image #1 of the series"} {
		if carriesImage(p) {
			t.Errorf("%q seen as carrying an image", p)
		}
	}

	reply := `{"type":"assistant","message":{"content":[{"type":"text","text":"The controller's AutoHTTPS answers :3370 with a 302."}]}}`
	desktop := `{"type":"user","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBO"}},{"type":"text","text":"what happened？"}]}}`
	dir := t.TempDir()

	written := filepath.Join(dir, "written.jsonl")
	writeTranscript(t, written, []string{reply, desktop})
	s := situationFor(hookInput{Prompt: "what happened？", TranscriptPath: written})
	if !s.Image {
		t.Fatal("a desktop prompt with an image block was not seen as carrying one")
	}
	if !strings.Contains(s.Previous, "AutoHTTPS") {
		t.Fatalf("previous = %q, want the reply before the prompt", s.Previous)
	}

	// An older message with a picture is not this one.
	answered := filepath.Join(dir, "answered.jsonl")
	writeTranscript(t, answered, []string{reply, desktop, `{"type":"assistant","message":{"content":[{"type":"text","text":"It is resyncing."}]}}`})
	if s := situationFor(hookInput{Prompt: "what happened？", TranscriptPath: answered}); s.Image {
		t.Fatal("an answered message's image was taken for this prompt's")
	}

	if s := situationFor(hookInput{Prompt: "[Image #1] what happened？"}); !s.Image {
		t.Fatal("a CLI prompt with [Image #1] was not seen as carrying an image")
	}
}

func writeTranscript(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A prompt with a picture is not recalled for: the hook cannot see it, and
// the brain would answer the words beside it, or the turn before.
func TestAPromptWithAnImageRecallsNothing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "brain.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(context.Background(), cortexdb.MemorySaveRequest{
		MemoryID: "drbd-resync", Scope: cortexdb.MemoryScopeGlobal,
		Content: "what happened when the DRBD resource stuck resyncing: the peer lost its disk",
	}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	t.Setenv("CORTEXDB_REMOTE", "")
	t.Setenv("CORTEXDB_PATH", dbPath)

	if out := recallFor(t, "what happened resyncing"); !strings.Contains(out, "drbd-resync") {
		t.Fatalf("without an image the memory is recalled, got %q", out)
	}
	if out := recallFor(t, "[Image #1] what happened resyncing"); out != "" {
		t.Fatalf("a prompt with an image recalled %q", out)
	}
}

// recallFor runs the hook on a prompt and returns what it printed.
func recallFor(t *testing.T, prompt string) string {
	t.Helper()
	payload, _ := json.Marshal(hookInput{Prompt: prompt})
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdin, stdout
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	runRecall()
	out, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestASessionInTheHomeDirectoryNamesNoProject(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	s := situationFor(hookInput{Cwd: home})
	if got := s.query("what port?"); got != "what port?" {
		t.Fatalf("query = %q, want the prompt as written", got)
	}
}

func TestEachHitCarriesWhereItCameFromAndWhyItCameBack(t *testing.T) {
	hits, err := parseRecallPayload(`{
		"memory_decision": {"effective_mode": "auto"},
		"knowledge_decision": {"effective_mode": "hybrid"},
		"memories": [{"score": 0.9312, "similarity": 0.8037, "via": ["semantic", "keyword"], "memory": {"id": "auto:d0b123ed:verify-fix", "content": "verify by reverting the fix",
			"created_at": "2026-10-09T02:25:42Z",
			"metadata": {"date": "2026-10-09", "source": "auto-capture", "model": "haiku", "agent": "claude-code",
				"host": "mac", "project": "linstor-server", "session": "d0b123ed-e6d6", "written_by": "legacy-token"}}}],
		"knowledge": [{"knowledge_id": "noted:1", "title": "Resize runbook", "snippet": "Resize, then verify.",
			"author": "noted", "source_url": "noted://note/1", "vector_score": 0.3026, "lexical_score": 0.7809,
			"metadata": {"updated": "2026-10-18T09:00:28Z", "written_by": "hermes"}}]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	got := formatRecallHits(hits)
	for _, want := range []string{
		"- auto:d0b123ed:verify-fix [2026-10-09 · claude-code auto-capture (haiku) · host mac · project linstor-server · session d0b123ed · semantic 0.80 + keyword]: verify by reverting the fix",
		"- Resize runbook [2026-10-18 · noted · key hermes · noted://note/1 · semantic 0.30 + keyword 0.78]: Resize, then verify.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "legacy-token") {
		t.Errorf("the shared token was shown as a writer:\n%s", got)
	}
}

func TestAMemoryFoundByKeywordsAloneSaysSo(t *testing.T) {
	hits, err := parseRecallPayload(`{"memory_decision": {"effective_mode": "lexical"},
		"memories": [{"score": 0.42, "memory": {"id": "m1", "content": "x"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := hits[0].Trail; got != "keyword" {
		t.Fatalf("trail = %q, want the keyword retriever named", got)
	}
}
