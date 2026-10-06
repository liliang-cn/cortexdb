package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

type fakeBrain struct {
	knowledge []cortexdb.KnowledgeSaveRequest
	memories  []cortexdb.MemorySaveRequest
	existing  []cortexdb.MemoryRecord
	searches  []cortexdb.MemorySearchRequest
}

// SearchMemory returns every existing memory: which ones a real search would
// find is the brain's business, what is done with them is the test's.
func (f *fakeBrain) SearchMemory(_ context.Context, req cortexdb.MemorySearchRequest) ([]cortexdb.MemorySearchHit, error) {
	f.searches = append(f.searches, req)
	var hits []cortexdb.MemorySearchHit
	for _, m := range f.existing {
		hits = append(hits, cortexdb.MemorySearchHit{Memory: m, Score: 1})
	}
	return hits, nil
}

func (f *fakeBrain) SaveKnowledge(_ context.Context, req cortexdb.KnowledgeSaveRequest) error {
	f.knowledge = append(f.knowledge, req)
	return nil
}

func (f *fakeBrain) SaveMemory(_ context.Context, req cortexdb.MemorySaveRequest) error {
	f.memories = append(f.memories, req)
	return nil
}

type fakeLLM struct {
	calls   int
	prompts []string
	// supersede answers the supersede check; empty retires nothing.
	supersede       string
	supersedePrompt string
}

func (f *fakeLLM) GenerateJSON(_ context.Context, system, user string) ([]byte, error) {
	if system == supersedeSystemPrompt {
		f.supersedePrompt = user
		return []byte(firstNonEmptyStr(f.supersede, `{"retire":[],"outdated":[]}`)), nil
	}
	f.calls++
	f.prompts = append(f.prompts, user)
	return []byte(`{"memories":[{"slug":"uses-uv","content":"The user sets up Python with uv.","importance":0.7,"type":"preference"}]}`), nil
}

const codexThread = "01a100fe-6a61-7c21-8ac2-0bf674ca16e6"

func write(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func jsonl(t *testing.T, lines ...any) string {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		data, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

func claudeTurn(role, text string) map[string]any {
	return map[string]any{"type": role, "message": map[string]any{"role": role, "content": text}}
}

func codexTurn(role, text string) map[string]any {
	return map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "message", "role": role, "content": []map[string]any{{"type": "input_text", "text": text}},
	}}
}

// homeWithAgents lays out what a person who has used both agents for a while
// has on disk.
func homeWithAgents(t *testing.T, now time.Time) string {
	t.Helper()
	home := t.TempDir()
	claude, codex := filepath.Join(home, ".claude"), filepath.Join(home, ".codex")

	write(t, filepath.Join(claude, "projects", "-src-app", "memory", "uv.md"),
		"---\nname: python-uses-uv\ndescription: set up Python with uv\nmetadata:\n  type: feedback\n---\nUse uv, never pip into the system.\n", time.Time{})
	write(t, filepath.Join(claude, "projects", "-src-app", "memory", "MEMORY.md"), "- [uv](uv.md)\n", time.Time{})
	write(t, filepath.Join(claude, "CLAUDE.md"), "No co-author lines.\n", time.Time{})
	write(t, filepath.Join(codex, "AGENTS.md"), "Answer in Chinese or English.\n", time.Time{})

	db, err := sql.Open("sqlite", filepath.Join(codex, "memories_1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE stage1_outputs (thread_id TEXT PRIMARY KEY, source_updated_at INTEGER NOT NULL, raw_memory TEXT NOT NULL, rollout_summary TEXT NOT NULL, rollout_slug TEXT, generated_at INTEGER NOT NULL)`,
		`INSERT INTO stage1_outputs VALUES ('` + codexThread + `', 1759395600, 'The deploy host is node-a.', 'Moved the deploy to node-a.', 'deploy-node-a', 1759395600)`,
		`INSERT INTO stage1_outputs VALUES ('empty', 1759395600, '  ', '', NULL, 1759395600)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	write(t, filepath.Join(codex, "memories", "MEMORY.md"), "# Codex memory\nPrefers small commits.\n", time.Time{})
	write(t, filepath.Join(codex, "memories", "go-mod-cache", "README.md"), "a cache, not a note\n", time.Time{})

	old := now.Add(-48 * time.Hour)
	write(t, filepath.Join(claude, "projects", "-src-app", "aaaa1111-0000-0000-0000-000000000000.jsonl"), jsonl(t,
		claudeTurn("user", "set up the venv"), claudeTurn("assistant", "Using uv."),
		claudeTurn("user", "<system-reminder>noise</system-reminder>why not pip?"), claudeTurn("assistant", "Debian splits venv out."),
		claudeTurn("user", "ok, remember that"),
	), old)
	write(t, filepath.Join(claude, "projects", "-src-app", "bbbb2222-0000-0000-0000-000000000000.jsonl"), jsonl(t,
		claudeTurn("user", "hi"), claudeTurn("user", "still"), claudeTurn("user", "going"),
	), now.Add(-time.Minute))
	write(t, filepath.Join(claude, "projects", "-src-app", "cccc3333-0000-0000-0000-000000000000.jsonl"), jsonl(t,
		claudeTurn("user", "thanks"),
	), old)
	write(t, filepath.Join(codex, "sessions", "2026", "10", "03", "rollout-2026-10-03T17-00-35-"+codexThread+".jsonl"), jsonl(t,
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": codexThread, "cwd": "/src/app"}},
		codexTurn("user", "# AGENTS.md instructions for /src/app\n\n<INSTRUCTIONS>\nAnswer in Chinese.\n</INSTRUCTIONS>"),
		codexTurn("user", "<environment_context>\n  <cwd>/src/app</cwd>\n</environment_context>"),
		codexTurn("developer", "<permissions instructions>sandboxed</permissions instructions>"),
		codexTurn("user", "move the deploy to node-a"), codexTurn("assistant", "Done."),
		codexTurn("user", "and the cron"), codexTurn("user", "thanks"),
	), now.Add(-72*time.Hour))
	return home
}

func newRun(home string, w brainClient, now time.Time) agentImport {
	return agentImport{w: w, brain: "test", home: home, state: loadSessionState(sessionStatePath(home)), now: now}
}

func bySource(out importAgentMemoryOut) map[string]importSourceOut {
	m := map[string]importSourceOut{}
	for _, s := range out.Sources {
		m[s.Source] = s
	}
	return m
}

func TestImportAgentMemoryDefaultSources(t *testing.T) {
	now := time.Now()
	home := homeWithAgents(t, now)
	brain := &fakeBrain{}

	dry, err := newRun(home, brain, now).run(context.Background(), importAgentMemoryIn{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(brain.knowledge)+len(brain.memories) != 0 {
		t.Fatalf("dry run wrote %d knowledge, %d memories", len(brain.knowledge), len(brain.memories))
	}
	got := bySource(dry)
	if got[srcClaudeMemory].Found != 1 || got[srcInstructions].Found != 2 || got[srcCodexMemory].Found != 2 {
		t.Fatalf("dry run counts: %+v", dry.Sources)
	}
	if got[srcCodexMemory].Skipped != 1 {
		t.Errorf("an empty Codex memory should be skipped, got %+v", got[srcCodexMemory])
	}
	if _, ok := got[srcClaudeSessions]; ok {
		t.Error("sessions are opt-in")
	}

	out, err := newRun(home, brain, now).run(context.Background(), importAgentMemoryIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(brain.knowledge) != 4 || len(brain.memories) != 1 {
		t.Fatalf("wrote %d knowledge, %d memories; want 4 and 1 (%+v)", len(brain.knowledge), len(brain.memories), out.Sources)
	}
	mem := brain.memories[0]
	if mem.MemoryID != "codex:"+codexThread || mem.Content != "The deploy host is node-a." || mem.Metadata["source"] != "codex-memory" {
		t.Errorf("codex memory: %+v", mem)
	}
	ids := map[string]bool{}
	for _, k := range brain.knowledge {
		ids[k.KnowledgeID] = true
	}
	if !ids["agentmem:python-uses-uv"] || !ids["codexmem:memory"] {
		t.Errorf("knowledge ids: %v", ids)
	}
}

func TestImportAgentMemorySessions(t *testing.T) {
	now := time.Now()
	home := homeWithAgents(t, now)
	brain := &fakeBrain{}
	in := importAgentMemoryIn{Sources: []string{srcClaudeSessions, srcCodexSessions}}

	// Without a model, sessions are counted and left for later.
	out, err := newRun(home, brain, now).run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	claude := bySource(out)[srcClaudeSessions]
	if claude.Found != 2 || claude.Pending != 2 || claude.Note == "" || len(brain.memories) != 0 {
		t.Fatalf("no-model run: %+v, %d memories", claude, len(brain.memories))
	}

	llm := &fakeLLM{}
	run := newRun(home, brain, now)
	run.llm = llm
	out, err = run.run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	got := bySource(out)
	// The one-turn session is read and marked, not distilled; the live one is
	// left to the hook that will capture it.
	if got[srcClaudeSessions].Imported != 1 || got[srcClaudeSessions].Skipped != 1 || got[srcClaudeSessions].Pending != 0 {
		t.Errorf("claude sessions: %+v", got[srcClaudeSessions])
	}
	if got[srcCodexSessions].Imported != 1 || llm.calls != 2 {
		t.Errorf("codex sessions: %+v, %d model calls", got[srcCodexSessions], llm.calls)
	}
	var seen []string
	for _, m := range brain.memories {
		seen = append(seen, m.MemoryID)
		if m.Metadata["source"] != "imported-session" {
			t.Errorf("%s source = %v", m.MemoryID, m.Metadata["source"])
		}
	}
	if strings.Join(seen, ",") != "auto:aaaa1111:uses-uv,auto:codex-74ca16e6:uses-uv" {
		t.Errorf("memory ids: %v", seen)
	}
	// The model is told the day the session happened, not the day of import.
	for _, p := range llm.prompts {
		if strings.Contains(p, now.Format("2006-01-02")) {
			t.Errorf("prompt dated to the import day: %q", clip(p, 80))
		}
	}

	// A second run finds nothing left.
	out, err = run.run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(out); got[srcClaudeSessions].Found != 0 || got[srcCodexSessions].Found != 0 || llm.calls != 2 {
		t.Errorf("re-run should find nothing: %+v (%d model calls)", out.Sources, llm.calls)
	}
}

func TestImportAgentMemorySessionsBudget(t *testing.T) {
	now := time.Now()
	home := homeWithAgents(t, now)
	run := newRun(home, &fakeBrain{}, now)
	run.llm = &fakeLLM{}
	out, err := run.run(context.Background(), importAgentMemoryIn{Sources: []string{srcClaudeSessions}, MaxSessions: 1})
	if err != nil {
		t.Fatal(err)
	}
	if s := bySource(out)[srcClaudeSessions]; s.Pending != 1 || out.Next == "" {
		t.Errorf("one of two sessions should be left with a hint: %+v next=%q", s, out.Next)
	}
	out, err = run.run(context.Background(), importAgentMemoryIn{Sources: []string{srcClaudeSessions}, Since: "1d"})
	if err != nil {
		t.Fatal(err)
	}
	if s := bySource(out)[srcClaudeSessions]; s.Found != 0 {
		t.Errorf("since 1d should leave out a two-day-old session: %+v", s)
	}
}

func TestImportAgentMemoryUnknownSource(t *testing.T) {
	_, err := newRun(t.TempDir(), &fakeBrain{}, time.Now()).run(context.Background(), importAgentMemoryIn{Sources: []string{"cursor"}})
	if err == nil {
		t.Fatal("an unknown source should be an error, not a silent no-op")
	}
}

func TestDigestCodexRolloutKeepsOnlyTheConversation(t *testing.T) {
	home := homeWithAgents(t, time.Now())
	paths, _ := filepath.Glob(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*.jsonl"))
	if len(paths) != 1 {
		t.Fatalf("rollouts: %v", paths)
	}
	digest, turns, err := digestCodexRollout(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if turns != 3 {
		t.Errorf("user turns = %d, want 3:\n%s", turns, digest)
	}
	for _, noise := range []string{"AGENTS.md", "Answer in Chinese", "<cwd>", "sandboxed"} {
		if strings.Contains(digest, noise) {
			t.Errorf("digest keeps injected %q:\n%s", noise, digest)
		}
	}
	if !strings.Contains(digest, "USER: move the deploy to node-a\nASSISTANT: Done.") {
		t.Errorf("digest:\n%s", digest)
	}
}

func TestReadCodexMemoriesWithoutTheFeature(t *testing.T) {
	codex := t.TempDir()
	if mems, err := readCodexMemories(codex); err != nil || mems != nil {
		t.Errorf("no database: %v, %v", mems, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(codex, "memories_1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE other (x)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if mems, err := readCodexMemories(codex); err != nil || mems != nil {
		t.Errorf("no table: %v, %v", mems, err)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	cases := map[string]time.Time{
		"":           {},
		"30d":        now.AddDate(0, 0, -30),
		"12h":        now.Add(-12 * time.Hour),
		"2026-09-01": time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local),
	}
	for in, want := range cases {
		got, err := parseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseSince("last week", now); err == nil {
		t.Error("an unreadable since should be an error")
	}
}

func TestImportedSessionRetiresWhatItMadeUntrue(t *testing.T) {
	now := time.Now()
	home := homeWithAgents(t, now)
	sessionDay := now.Add(-48 * time.Hour)
	brain := &fakeBrain{existing: []cortexdb.MemoryRecord{
		{ID: "auto:old11111:pip-ok", Content: "The user installs Python packages with pip.", Metadata: map[string]any{"date": sessionDay.AddDate(0, -1, 0).Format("2006-01-02")}},
		{ID: "auto:aaaa1111:uses-uv", Content: "own session, rewritten by slug", Metadata: map[string]any{"date": sessionDay.Format("2006-01-02")}},
	}}
	llm := &fakeLLM{supersede: `{"retire":[{"new":"uses-uv","old":["auto:old11111:pip-ok","auto:aaaa1111:uses-uv","made-up"],"reason":"switched to uv"}]}`}
	run := newRun(home, brain, now)
	run.llm = llm
	out, err := run.run(context.Background(), importAgentMemoryIn{Sources: []string{srcClaudeSessions}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(llm.supersedePrompt, "own session") {
		t.Error("a session's own memories are rewritten by slug, not offered for retiring")
	}
	if s := bySource(out)[srcClaudeSessions]; s.Retired != 1 {
		t.Errorf("retired = %d, want 1: %+v", s.Retired, s)
	}
	saved := brain.memories[0]
	if strings.Join(saved.Supersedes, ",") != "auto:old11111:pip-ok" || saved.Metadata["supersede_reason"] != "switched to uv" {
		t.Errorf("saved %s supersedes %v (%v)", saved.MemoryID, saved.Supersedes, saved.Metadata["supersede_reason"])
	}
}

func TestPlanSupersedesLetsTheLaterDateWin(t *testing.T) {
	session := time.Date(2026, 9, 1, 12, 0, 0, 0, time.Local)
	facts := []capturedFact{
		{Slug: "deploy-node-a", Content: "Deploys go to node-a."},
		{Slug: "db-pg16", Content: "The database is Postgres 16."},
	}
	candidates := []supersedeCandidate{
		{ID: "older", Content: "Deploys go to node-c.", Date: "2026-08-01"},
		{ID: "newer", Content: "Deploys go to node-b.", Date: "2026-09-20"},
		{ID: "undated", Content: "The database is Postgres 15."},
	}
	llm := &fakeLLM{supersede: `{"retire":[{"new":"deploy-node-a","old":["older","newer"]},{"new":"db-pg16","old":["undated"]}],"outdated":[{"new":"deploy-node-a","by":"newer (2026-09-20), which says node-b"},{"new":"db-pg16","by":"older"}]}`}
	plan, err := planSupersedes(context.Background(), llm, facts, candidates, session)
	if err != nil {
		t.Fatal(err)
	}
	// node-b was recorded after the session: the session's node-a is the
	// stale one, and must not retire anything.
	if plan.Outdated["deploy-node-a"] != "newer" || len(plan.Retire["deploy-node-a"]) != 0 {
		t.Errorf("deploy: %+v", plan)
	}
	// An older memory cannot make a newer fact outdated, and an undated one
	// is never retired: there is no telling which came first.
	if _, ok := plan.Outdated["db-pg16"]; ok || len(plan.Retire["db-pg16"]) != 0 {
		t.Errorf("db: %+v", plan)
	}

	var req cortexdb.MemorySaveRequest
	if plan.apply("deploy-node-a", &req) {
		t.Error("an outdated fact should not be saved")
	}
}

func TestPlanSupersedesBudget(t *testing.T) {
	session := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	var candidates []supersedeCandidate
	var ids []string
	for i := range 9 {
		id := fmt.Sprintf("m%d", i)
		ids = append(ids, `"`+id+`"`)
		candidates = append(candidates, supersedeCandidate{ID: id, Content: "x", Date: "2026-08-01"})
	}
	llm := &fakeLLM{supersede: `{"retire":[{"new":"a","old":[` + strings.Join(ids, ",") + `]}]}`}
	plan, err := planSupersedes(context.Background(), llm, []capturedFact{{Slug: "a", Content: "a"}}, candidates, session)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(plan.Retire["a"]); n != maxSupersedesPerSession {
		t.Errorf("one session retired %d memories; the cap is %d", n, maxSupersedesPerSession)
	}
}

// The whole step against a real brain: the older memory is found by the new
// fact, marked superseded, and no longer recalled; the new one is.
func TestCaptureRetiresOnARealBrain(t *testing.T) {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "brain.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	brain := localBrain{db}
	old := cortexdb.MemorySaveRequest{
		MemoryID: "auto:11111111:deploy-node-a", Scope: "global",
		Content:  "CortexDB production deploys go to node-a.",
		Metadata: map[string]any{"date": "2026-09-01"},
		Entities: []cortexdb.ToolEntityInput{{Name: "CortexDB", Type: "project"}},
	}
	if err := brain.SaveMemory(ctx, old); err != nil {
		t.Fatal(err)
	}

	facts := []capturedFact{{Slug: "deploy-node-b", Content: "CortexDB production deploys go to node-b.", Entities: []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}{{Name: "CortexDB", Type: "project"}}}}
	llm := &fakeLLM{supersede: `{"retire":[{"new":"deploy-node-b","old":["auto:11111111:deploy-node-a"],"reason":"moved"}]}`}
	session := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	plan, err := reconcileFacts(ctx, llm, brain, facts, capturedIDPrefix("22222222-session"), session)
	if err != nil {
		t.Fatal(err)
	}
	req := capturedRequest("22222222-session", "2026-10-06", facts[0])
	if !plan.apply(facts[0].Slug, &req) || len(req.Supersedes) != 1 {
		t.Fatalf("plan %+v gave %+v", plan, req.Supersedes)
	}
	if err := brain.SaveMemory(ctx, req); err != nil {
		t.Fatal(err)
	}

	hits, err := brain.SearchMemory(ctx, cortexdb.MemorySearchRequest{Query: "CortexDB production deploys", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, h := range hits {
		ids = append(ids, h.Memory.ID)
	}
	if strings.Join(ids, ",") != "auto:22222222:deploy-node-b" {
		t.Errorf("recall after retiring: %v", ids)
	}
}

// The ways a model was seen to name things: as given, embellished, quoted.
// A date or a name that was never given resolves to nothing.
func TestNameIn(t *testing.T) {
	names := []string{"deploy", "deploy-node-a", "proto"}
	for in, want := range map[string]string{
		"deploy-node-a":                         "deploy-node-a",
		"deploy-node-a (2026-09-01)":            "deploy-node-a",
		"later: \"proto\", recorded 2026-10-05": "proto",
		"2026-09-01":                            "",
		"deploy-node-z":                         "",
	} {
		if got := nameIn(in, names); got != want {
			t.Errorf("nameIn(%q) = %q, want %q", in, got, want)
		}
	}
}
