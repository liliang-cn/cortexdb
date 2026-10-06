package main

// import_agent_memory — bring in the memory a person already has.
//
// Someone installing CortexDB rarely starts from nothing. Claude Code has been
// keeping notes under ~/.claude/projects/*/memory, Codex has its own memory
// database, both have CLAUDE.md / AGENTS.md, and months of sessions sit on disk
// as transcripts. All of it is on the machine the agent runs on, which is why
// this is a tool of the MCP process rather than of the brain: a shared brain on
// another host cannot read this laptop's files, and the process that can is the
// one the agent is talking to.
//
// So the files are read here, and what is written goes to whichever brain this
// process uses — the local file, or the shared brain over its own tools. The
// earlier one-shot (`--import-agent-memory`) opened the local file whatever
// CORTEXDB_REMOTE said, so on a shared brain an import landed somewhere nobody
// would read it, and said it had succeeded.
//
// Five sources, three by default:
//
//   - claude_memory: the file-based notes, saved as knowledge (as before).
//   - instructions: CLAUDE.md / AGENTS.md at each root.
//   - codex_memory: Codex's own memory — the per-session memories in
//     memories_1.sqlite (stage1_outputs) and the notes under ~/.codex/memories.
//   - claude_sessions / codex_sessions: past transcripts, distilled into
//     memories the way a session is captured when it ends (same prompt, same
//     ids). Opt-in: each session is a model call, so a call does a few
//     (max_sessions), remembers which it has done, and says how many are left.
//
// Every id is stable, so running it again refreshes what it wrote instead of
// adding a second copy, and dry_run counts everything before anything is
// written.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

const (
	srcClaudeMemory   = "claude_memory"
	srcInstructions   = "instructions"
	srcCodexMemory    = "codex_memory"
	srcClaudeSessions = "claude_sessions"
	srcCodexSessions  = "codex_sessions"

	// defaultMaxSessions is how many transcripts one call distils. Each is a
	// model call of up to a minute; a tool call that runs for an hour is one
	// the agent gives up on.
	defaultMaxSessions = 5
	// liveSessionAge is how recent a transcript may be and still be left
	// alone: a session written to minutes ago is probably still going, and the
	// SessionEnd hook or the cortexdb-live mod will capture it when it is done.
	liveSessionAge = 10 * time.Minute
	// codexNoteLimit bounds a note read from ~/.codex/memories; anything larger
	// is not a note.
	codexNoteLimit = 256 << 10
)

var defaultImportSources = []string{srcClaudeMemory, srcInstructions, srcCodexMemory}

// brainClient is what an import or a capture needs from whichever brain this
// process uses: to write, and to find what is already there.
type brainClient interface {
	SaveKnowledge(ctx context.Context, req cortexdb.KnowledgeSaveRequest) error
	SaveMemory(ctx context.Context, req cortexdb.MemorySaveRequest) error
	SearchMemory(ctx context.Context, req cortexdb.MemorySearchRequest) ([]cortexdb.MemorySearchHit, error)
}

type localBrain struct{ db *cortexdb.DB }

func (l localBrain) SaveKnowledge(ctx context.Context, req cortexdb.KnowledgeSaveRequest) error {
	_, err := l.db.SaveKnowledge(ctx, req)
	return err
}

func (l localBrain) SaveMemory(ctx context.Context, req cortexdb.MemorySaveRequest) error {
	_, err := l.db.SaveMemory(ctx, req)
	return err
}

func (l localBrain) SearchMemory(ctx context.Context, req cortexdb.MemorySearchRequest) ([]cortexdb.MemorySearchHit, error) {
	resp, err := l.db.SearchMemory(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// remoteBrain writes through the shared brain's own tools, so a record saved
// from here is exactly the record knowledge_save / memory_save would have made.
type remoteBrain struct{ client rpcv1.ToolsServiceClient }

func (r remoteBrain) call(ctx context.Context, tool string, req any) (string, error) {
	args, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteDialTimeout)
	defer cancel()
	resp, err := r.client.CallTool(ctx, &rpcv1.CallToolRequest{Name: tool, ArgsJson: string(args)})
	if err != nil {
		return "", fmt.Errorf("%s: %w", tool, err)
	}
	return resp.GetResultJson(), nil
}

func (r remoteBrain) SaveKnowledge(ctx context.Context, req cortexdb.KnowledgeSaveRequest) error {
	_, err := r.call(ctx, "knowledge_save", req)
	return err
}

func (r remoteBrain) SaveMemory(ctx context.Context, req cortexdb.MemorySaveRequest) error {
	_, err := r.call(ctx, "memory_save", req)
	return err
}

func (r remoteBrain) SearchMemory(ctx context.Context, req cortexdb.MemorySearchRequest) ([]cortexdb.MemorySearchHit, error) {
	raw, err := r.call(ctx, "memory_search", req)
	if err != nil {
		return nil, err
	}
	var resp cortexdb.MemorySearchResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("decode memory_search: %w", err)
	}
	return resp.Results, nil
}

type importAgentMemoryIn struct {
	Sources     []string `json:"sources,omitempty" jsonschema:"what to import: claude_memory, instructions, codex_memory (the default three), claude_sessions, codex_sessions"`
	Since       string   `json:"since,omitempty" jsonschema:"sessions only: those modified after this, an RFC 3339 date (2026-09-01) or a span back from now (30d, 12h)"`
	MaxSessions int      `json:"max_sessions,omitempty" jsonschema:"sessions only: how many transcripts to distil in this call (default 5); call again for the rest"`
	DryRun      bool     `json:"dry_run,omitempty" jsonschema:"count what would be imported and write nothing"`
	Roots       []string `json:"roots,omitempty" jsonschema:"homes to scan instead of ~/.claude and ~/.codex"`
}

type importSourceOut struct {
	Source   string `json:"source"`
	Found    int    `json:"found"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped,omitempty"`
	Pending  int    `json:"pending,omitempty"`
	// Retired counts older memories a distilled session made untrue; they
	// are kept, marked superseded, and no longer recalled as current.
	Retired int `json:"retired,omitempty"`
	// Outdated counts facts from an old session that a later memory already
	// contradicts; they are not saved.
	Outdated int      `json:"outdated,omitempty"`
	Note     string   `json:"note,omitempty"`
	Sample   []string `json:"sample,omitempty"`
}

type importAgentMemoryOut struct {
	Brain   string            `json:"brain"`
	DryRun  bool              `json:"dry_run,omitempty"`
	Sources []importSourceOut `json:"sources"`
	// Next says what calling again would do, when anything is left.
	Next string `json:"next,omitempty"`
}

// agentImport is one run, with what it needs from the outside named.
type agentImport struct {
	w     brainClient
	brain string
	llm   graphflow.JSONGenerator // nil: sessions are counted, not distilled
	home  string
	state *sessionState
	now   time.Time
}

func (a agentImport) run(ctx context.Context, in importAgentMemoryIn) (importAgentMemoryOut, error) {
	out := importAgentMemoryOut{Brain: a.brain, DryRun: in.DryRun}
	sources := in.Sources
	if len(sources) == 0 {
		sources = defaultImportSources
	}
	claudeHome, codexHome := filepath.Join(a.home, ".claude"), filepath.Join(a.home, ".codex")
	roots := in.Roots
	if len(roots) == 0 {
		roots = []string{claudeHome, codexHome}
	}
	since, err := parseSince(in.Since, a.now)
	if err != nil {
		return out, err
	}
	maxSessions := in.MaxSessions
	if maxSessions <= 0 {
		maxSessions = defaultMaxSessions
	}

	for _, src := range sources {
		var res importSourceOut
		switch src {
		case srcClaudeMemory, srcInstructions:
			res, err = a.importNotes(ctx, src, roots, in.DryRun)
		case srcCodexMemory:
			res, err = a.importCodexMemory(ctx, codexHome, in.DryRun)
		case srcClaudeSessions:
			res, err = a.importSessions(ctx, src, listClaudeSessions(claudeHome), since, maxSessions, in.DryRun)
		case srcCodexSessions:
			res, err = a.importSessions(ctx, src, listCodexSessions(codexHome), since, maxSessions, in.DryRun)
		default:
			return out, fmt.Errorf("unknown source %q: want claude_memory, instructions, codex_memory, claude_sessions or codex_sessions", src)
		}
		if err != nil {
			return out, err
		}
		out.Sources = append(out.Sources, res)
		if res.Pending > 0 && !in.DryRun && res.Note == "" {
			out.Next = "call again with the same sources to distil the next sessions"
		}
	}
	return out, nil
}

func sample(ids []string) []string {
	if len(ids) > 8 {
		return ids[:8]
	}
	return ids
}

// importNotes saves Claude Code's file-based notes or the instruction files.
func (a agentImport) importNotes(ctx context.Context, src string, roots []string, dry bool) (importSourceOut, error) {
	res := importSourceOut{Source: src}
	items, skipped, err := cortexdb.ScanAgentMemory(cortexdb.ImportAgentMemoryOptions{Roots: roots, IncludeInstructions: true})
	if err != nil {
		return res, err
	}
	res.Skipped = skipped
	var ids []string
	for _, it := range items {
		if (it.Type == "instructions") != (src == srcInstructions) {
			continue
		}
		res.Found++
		if !dry {
			if err := a.w.SaveKnowledge(ctx, it.KnowledgeRequest("")); err != nil {
				return res, fmt.Errorf("save %s: %w", it.ID, err)
			}
			res.Imported++
		}
		ids = append(ids, it.ID)
	}
	res.Sample = sample(ids)
	return res, nil
}

// codexMemory is one memory Codex wrote for one of its sessions.
type codexMemory struct {
	Thread  string
	Memory  string
	Summary string
	Slug    string
	Updated time.Time
}

// readCodexMemories reads Codex's memory database read-only. A missing file or
// a database without the table is "nothing here", not an error: most people
// have not turned Codex's memory on.
func readCodexMemories(codexHome string) ([]codexMemory, error) {
	path := filepath.Join(codexHome, "memories_1.sqlite")
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	// Read-only first, so a write Codex has in its WAL is seen. Where the
	// lock files beside the database cannot be made (a sandbox, a read-only
	// home), read the file as it stands instead.
	var rows *sql.Rows
	var err error
	for _, uri := range []string{"file:" + path + "?mode=ro", "file:" + path + "?mode=ro&immutable=1"} {
		db, oerr := sql.Open("sqlite", uri)
		if oerr != nil {
			return nil, oerr
		}
		defer func() { _ = db.Close() }()
		rows, err = db.Query(`SELECT thread_id, raw_memory, rollout_summary, COALESCE(rollout_slug, ''), source_updated_at FROM stage1_outputs`)
		if err == nil || strings.Contains(err.Error(), "no such table") {
			break
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = rows.Close() }()
	var out []codexMemory
	for rows.Next() {
		var m codexMemory
		var updated int64
		if err := rows.Scan(&m.Thread, &m.Memory, &m.Summary, &m.Slug, &updated); err != nil {
			return nil, err
		}
		// Codex stamps seconds; accept milliseconds too rather than guess wrong.
		if updated > 1e12 {
			m.Updated = time.UnixMilli(updated)
		} else {
			m.Updated = time.Unix(updated, 0)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// readCodexNotes reads the markdown notes at the top of ~/.codex/memories
// (MEMORY.md, memory_summary.md). Its folders are left alone: the rollout
// summaries there repeat what memories_1.sqlite already holds, and other
// tools park caches beside them.
func readCodexNotes(codexHome string) []cortexdb.AgentMemoryItem {
	paths, _ := filepath.Glob(filepath.Join(codexHome, "memories", "*.md"))
	var items []cortexdb.AgentMemoryItem
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() || info.Size() > codexNoteLimit {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil || strings.TrimSpace(string(data)) == "" {
			continue
		}
		name := filepath.Base(p)
		items = append(items, cortexdb.AgentMemoryItem{
			ID: "codexmem:" + slugify(strings.TrimSuffix(name, filepath.Ext(name))), Title: "Codex memory: " + name,
			Content: strings.TrimSpace(string(data)), Type: "codex-memory", Path: p,
		})
	}
	return items
}

func (a agentImport) importCodexMemory(ctx context.Context, codexHome string, dry bool) (importSourceOut, error) {
	res := importSourceOut{Source: srcCodexMemory}
	mems, err := readCodexMemories(codexHome)
	if err != nil {
		return res, err
	}
	notes := readCodexNotes(codexHome)
	var ids []string
	for _, m := range mems {
		content := strings.TrimSpace(m.Memory)
		if content == "" {
			res.Skipped++
			continue
		}
		res.Found++
		req := cortexdb.MemorySaveRequest{
			MemoryID:   "codex:" + m.Thread,
			Scope:      "global",
			Content:    content,
			Importance: 0.6,
			Metadata: map[string]any{
				"source": "codex-memory", "thread": m.Thread, "slug": m.Slug,
				"summary": clip(strings.TrimSpace(m.Summary), 600), "date": m.Updated.Format("2006-01-02"),
			},
		}
		if !dry {
			if err := a.w.SaveMemory(ctx, req); err != nil {
				return res, fmt.Errorf("save %s: %w", req.MemoryID, err)
			}
			res.Imported++
		}
		ids = append(ids, req.MemoryID)
	}
	for _, it := range notes {
		res.Found++
		if !dry {
			if err := a.w.SaveKnowledge(ctx, it.KnowledgeRequest("")); err != nil {
				return res, fmt.Errorf("save %s: %w", it.ID, err)
			}
			res.Imported++
		}
		ids = append(ids, it.ID)
	}
	if res.Found == 0 && res.Skipped == 0 {
		res.Note = "Codex keeps no memories on this machine (its memory feature is off, or nothing has been consolidated yet)"
	}
	res.Sample = sample(ids)
	return res, nil
}

// transcript is one past session on disk.
type transcript struct {
	Kind     string // claude or codex
	ID       string
	Path     string
	Modified time.Time
}

func (t transcript) key() string { return t.Kind + ":" + t.ID }

// listClaudeSessions finds Claude Code's transcripts. Subagent transcripts live
// in folders beside them and are left out: they are a session's own working,
// not a conversation with a person.
func listClaudeSessions(claudeHome string) []transcript {
	paths, _ := filepath.Glob(filepath.Join(claudeHome, "projects", "*", "*.jsonl"))
	var out []transcript
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			out = append(out, transcript{Kind: "claude", ID: strings.TrimSuffix(filepath.Base(p), ".jsonl"), Path: p, Modified: info.ModTime()})
		}
	}
	return out
}

// codexRolloutID is the session id at the end of a rollout's file name.
var codexRolloutID = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

// listCodexSessions finds Codex's transcripts (sessions/YYYY/MM/DD/rollout-*.jsonl).
func listCodexSessions(codexHome string) []transcript {
	var out []transcript
	_ = filepath.WalkDir(filepath.Join(codexHome, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") {
			return nil
		}
		m := codexRolloutID.FindStringSubmatch(d.Name())
		if m == nil {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			out = append(out, transcript{Kind: "codex", ID: m[1], Path: p, Modified: info.ModTime()})
		}
		return nil
	})
	return out
}

// codexNoise is what Codex puts into a user turn that the person did not type:
// the AGENTS.md it injects, and the environment it describes.
var codexNoise = regexp.MustCompile(`(?s)<(?:INSTRUCTIONS|environment_context|user_instructions|permissions instructions)>.*?</(?:INSTRUCTIONS|environment_context|user_instructions|permissions instructions)>|^# AGENTS\.md instructions[^\n]*\n?`)

// digestCodexRollout reduces a Codex rollout to the conversation, the way
// digestTranscript does for Claude Code.
func digestCodexRollout(path string) (string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	var b strings.Builder
	userTurns := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Type != "response_item" || entry.Payload.Type != "message" {
			continue
		}
		var parts []string
		for _, c := range entry.Payload.Content {
			if t := strings.TrimSpace(c.Text); t != "" {
				parts = append(parts, t)
			}
		}
		text := strings.Join(parts, "\n")
		switch entry.Payload.Role {
		case "user":
			text = strings.TrimSpace(codexNoise.ReplaceAllString(stripTranscriptNoise(text), ""))
			if text == "" {
				continue
			}
			userTurns++
			fmt.Fprintf(&b, "USER: %s\n", clip(text, 2000))
		case "assistant":
			if text != "" {
				fmt.Fprintf(&b, "ASSISTANT: %s\n", clip(text, 1200))
			}
		}
	}
	return budgetDigest(b.String()), userTurns, nil
}

func (a agentImport) importSessions(ctx context.Context, src string, all []transcript, since time.Time, max int, dry bool) (importSourceOut, error) {
	res := importSourceOut{Source: src}
	var todo []transcript
	for _, t := range all {
		if !since.IsZero() && t.Modified.Before(since) {
			continue
		}
		if a.now.Sub(t.Modified) < liveSessionAge || a.state.done(t.key()) {
			continue
		}
		todo = append(todo, t)
	}
	// Newest first: the sessions most likely to still matter.
	sort.Slice(todo, func(i, j int) bool { return todo[i].Modified.After(todo[j].Modified) })
	res.Found = len(todo)
	res.Pending = len(todo)
	if dry || len(todo) == 0 {
		return res, nil
	}
	if a.llm == nil {
		res.Note = "distilling a past session needs a model: set CORTEXDB_LLM_BASE_URL and CORTEXDB_LLM_MODEL (any OpenAI-compatible endpoint, or Ollama) for this MCP server"
		return res, nil
	}
	var ids []string
	for _, t := range todo {
		if max <= 0 {
			break
		}
		max--
		digest, turns := "", 0
		var derr error
		if t.Kind == "codex" {
			digest, turns, derr = digestCodexRollout(t.Path)
		} else {
			digest, turns, derr = digestTranscript(t.Path)
		}
		if derr != nil {
			return res, fmt.Errorf("read %s: %w", t.Path, derr)
		}
		// A short exchange holds nothing worth keeping; it is marked done so
		// it is not read again every call.
		if turns >= 3 {
			facts, err := extractSessionFacts(ctx, a.llm, digest, t.Modified)
			if err != nil {
				return res, fmt.Errorf("distil %s: %w", t.key(), err)
			}
			prefix := capturedIDPrefix(t.ID)
			if t.Kind == "codex" {
				prefix = codexIDPrefix(t.ID)
			}
			// A failed check retires nothing; the facts are still saved.
			plan, _ := reconcileFacts(ctx, a.llm, a.w, facts, prefix, t.Modified)
			date := t.Modified.Format("2006-01-02")
			for _, f := range facts {
				req := capturedRequest(t.ID, date, f)
				req.MemoryID = prefix + f.Slug
				if req.Content == "" {
					continue
				}
				if !plan.apply(f.Slug, &req) {
					res.Outdated++
					continue
				}
				req.Metadata["source"] = "imported-session"
				req.Metadata["agent"] = t.Kind
				if err := a.w.SaveMemory(ctx, req); err != nil {
					return res, fmt.Errorf("save %s: %w", req.MemoryID, err)
				}
				res.Imported++
				res.Retired += len(req.Supersedes)
				ids = append(ids, req.MemoryID)
			}
		} else {
			res.Skipped++
		}
		if err := a.state.mark(t.key(), a.now); err != nil {
			return res, err
		}
		res.Pending--
	}
	res.Sample = sample(ids)
	return res, nil
}

// parseSince reads "2026-09-01", an RFC 3339 instant, or a span back from now
// ("30d", "12h").
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n >= 0 {
			return now.AddDate(0, 0, -n), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("since %q: want a date (2026-09-01), an RFC 3339 time, or a span like 30d or 12h", s)
}

// sessionState remembers which transcripts have been distilled, so a session
// is a model call once rather than every time the import runs. It is a file of
// this machine's: the transcripts are this machine's too.
type sessionState struct {
	mu   sync.Mutex
	path string
	Done map[string]string `json:"done"`
}

func loadSessionState(path string) *sessionState {
	st := &sessionState{path: path, Done: map[string]string{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, st)
		if st.Done == nil {
			st.Done = map[string]string{}
		}
	}
	return st
}

func (s *sessionState) done(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.Done[key]
	return ok
}

func (s *sessionState) mark(key string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Done[key] = at.Format(time.RFC3339)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func sessionStatePath(home string) string {
	return filepath.Join(home, ".cortexdb", "imported-sessions.json")
}

const importAgentMemoryDescription = "Import the memory a person already has from Claude Code and Codex into this brain, so recall finds it: " +
	"Claude Code's memory notes (~/.claude/projects/*/memory/*.md), CLAUDE.md / AGENTS.md, and Codex's own memories " +
	"(~/.codex/memories_1.sqlite and ~/.codex/memories) — the default sources. With claude_sessions / codex_sessions it " +
	"also distils past session transcripts into memories (a model call each, so a few per call: run it again until " +
	"nothing is pending). Reads files on the machine this MCP server runs on and writes to the brain it is connected " +
	"to, local or shared. Run with dry_run first to show the person what would be imported. Every id is stable, so " +
	"running it again refreshes rather than duplicates."

func addImportAgentMemoryTool(server *mcp.Server, w brainClient, brain string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "import_agent_memory",
		Description: importAgentMemoryDescription,
		Annotations: &mcp.ToolAnnotations{Title: "Import existing Claude Code / Codex memory"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in importAgentMemoryIn) (*mcp.CallToolResult, importAgentMemoryOut, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, importAgentMemoryOut{}, fmt.Errorf("resolve home: %w", err)
		}
		run := agentImport{w: w, brain: brain, llm: newOrganizeLLM(), home: home, state: loadSessionState(sessionStatePath(home)), now: time.Now()}
		out, err := run.run(ctx, in)
		return nil, out, err
	})
}
