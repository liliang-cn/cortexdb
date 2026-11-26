package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

const dshSession = "session-3d09536c-89dd-4d52-863b-e218dc67efb9"

func dshHeader(id string) map[string]any {
	return map[string]any{"type": "session", "version": 4, "id": id, "cwd": "/src/app"}
}

func dshUser(kind, text string) map[string]any {
	return map[string]any{"type": "user/message", "data": map[string]any{
		"role": "user", "source": map[string]any{"kind": kind},
		"content": []map[string]any{{"type": "text", "text": text}},
	}}
}

func dshAssistant(text string) map[string]any {
	return map[string]any{"type": "assistant/message", "data": map[string]any{"turn": 1, "message": map[string]any{
		"role": "assistant", "content": []map[string]any{{"type": "text", "text": text}},
	}}}
}

// dshConversation is three turns of a person talking to dsh, with what dsh
// and its plugins add to the log around them.
func dshConversation(t *testing.T) string {
	t.Helper()
	return jsonl(t,
		dshHeader(dshSession),
		map[string]any{"type": "turn/start", "data": map[string]any{"turn": 1}},
		dshUser("user", "set up the venv"),
		dshUser("runtime-context", "Current DSH file policy: workspace-write."),
		dshUser("cortexdb-memory", "[CortexDB recall] The user prefers uv."),
		map[string]any{"type": "system/message", "data": map[string]any{"message": map[string]any{"role": "system", "content": []map[string]any{{"type": "text", "text": "You are an AI agent."}}}}},
		dshAssistant("Using uv."),
		dshUser("user", "why not pip?"),
		dshAssistant("Debian splits venv out."),
		dshUser("user", "ok, remember that"),
	)
}

func zstdFrame(t *testing.T, s string) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	return enc.EncodeAll([]byte(s), nil)
}

func TestDigestDshSessionKeepsOnlyWhatThePersonSaid(t *testing.T) {
	path := filepath.Join(t.TempDir(), dshSession, "session.v4.jsonl.zstd")
	write(t, path, string(zstdFrame(t, dshConversation(t))), time.Time{})

	digest, turns, id, err := digestDshSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if turns != 3 || id != "3d09536c-89dd-4d52-863b-e218dc67efb9" {
		t.Fatalf("turns=%d id=%q", turns, id)
	}
	want := "USER: set up the venv\nASSISTANT: Using uv.\nUSER: why not pip?\nASSISTANT: Debian splits venv out.\nUSER: ok, remember that\n"
	if digest != want {
		t.Errorf("digest:\n%s\nwant:\n%s", digest, want)
	}
}

// dsh writes a live session a frame at a time; the frame being written is cut
// short on disk. What came before it is the conversation so far.
func TestDigestDshSessionReadsAnOpenLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), dshSession, "session.v4.jsonl.zstd")
	first := zstdFrame(t, jsonl(t, dshHeader(dshSession), dshUser("user", "first question"), dshAssistant("first answer")))
	second := zstdFrame(t, jsonl(t, dshUser("user", "second question")))
	write(t, path, string(append(first, second[:len(second)/2]...)), time.Time{})

	digest, turns, _, err := digestDshSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if turns != 1 || !strings.Contains(digest, "first answer") {
		t.Errorf("turns=%d digest=%q", turns, digest)
	}
}

func TestDigestDshSessionReadsAPlainLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), dshSession, "session.v4.jsonl")
	write(t, path, dshConversation(t), time.Time{})
	if _, turns, _, err := digestDshSession(path); err != nil || turns != 3 {
		t.Fatalf("turns=%d err=%v", turns, err)
	}
}

func TestListDshSessionsTakesTheNewestGeneration(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "--src-app--", dshSession)
	write(t, filepath.Join(dir, "session.jsonl"), "", time.Time{})
	write(t, filepath.Join(dir, "session.v3.jsonl.zstd"), "", time.Time{})
	write(t, filepath.Join(dir, "session.v4.jsonl.zstd"), "", time.Time{})
	write(t, filepath.Join(dir, "session.lock"), "", time.Time{})
	write(t, filepath.Join(dir, "attachments", "session.v9.jsonl"), "", time.Time{})

	got := listDshSessions(root)
	if len(got) != 1 {
		t.Fatalf("sessions: %+v", got)
	}
	if got[0].ID != "3d09536c-89dd-4d52-863b-e218dc67efb9" || filepath.Base(got[0].Path) != "session.v4.jsonl.zstd" {
		t.Errorf("session: %+v", got[0])
	}
	if !isDshSessionLog(got[0].Path) || isDshSessionLog("/x/projects/-a/aaaa1111.jsonl") {
		t.Error("isDshSessionLog misreads a path")
	}
}

func TestImportAgentMemoryDshSessions(t *testing.T) {
	t.Setenv("DSH_HOME", "")
	now := time.Now()
	home := t.TempDir()
	path := filepath.Join(home, ".dsh", "sessions", "--src-app--", dshSession, "session.v4.jsonl.zstd")
	write(t, path, string(zstdFrame(t, dshConversation(t))), now.Add(-48*time.Hour))

	brain := &fakeBrain{}
	run := newRun(home, brain, now)
	run.llm = &fakeLLM{}
	out, err := run.run(context.Background(), importAgentMemoryIn{Sources: []string{srcDshSessions}})
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(out)[srcDshSessions]; got.Found != 1 || got.Imported != 1 {
		t.Fatalf("dsh sessions: %+v", got)
	}
	if len(brain.memories) != 1 {
		t.Fatalf("memories: %+v", brain.memories)
	}
	m := brain.memories[0]
	if m.MemoryID != "auto:dsh-3d09536c:uses-uv" || m.Metadata["agent"] != "dsh" || m.Metadata["session"] != "3d09536c-89dd-4d52-863b-e218dc67efb9" {
		t.Errorf("memory: %s %v", m.MemoryID, m.Metadata)
	}
}

// DSH_HOME moves dsh's data, and the import follows it.
func TestDshHomeFollowsDSHHOME(t *testing.T) {
	t.Setenv("DSH_HOME", "/data/dsh")
	if got := dshHome("/home/u"); got != "/data/dsh" {
		t.Errorf("dshHome = %q", got)
	}
	t.Setenv("DSH_HOME", "  ")
	if got := dshHome("/home/u"); got != filepath.Join("/home/u", ".dsh") {
		t.Errorf("blank DSH_HOME: dshHome = %q", got)
	}
}
