package cli

// `--file-context` answers the PreToolUse hook on Read, Edit and Write with
// what the brain remembers about the file the agent is about to open: the
// memories a capture linked to it (files.go), newest first, one line each.
//
// The agent gets "this is where the race was fixed on the 12th" before it
// reads the code, not after it has re-derived it. Each file is looked up once
// per session — the hook fires on every read, and the second read of a file
// learns nothing the first did not — and a file the brain knows nothing about
// costs one query and adds nothing to the context.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// fileContextMax and fileContextBudget keep the block small: it arrives in
// the middle of a task, on every first read of a known file.
const (
	fileContextMax    = 8
	fileContextBudget = 3000
)

func runFileContext() {
	var in struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		} `json:"tool_input"`
	}
	if json.NewDecoder(os.Stdin).Decode(&in) != nil {
		return
	}
	path := firstNonEmptyStr(in.ToolInput.FilePath, in.ToolInput.NotebookPath)
	if path == "" {
		return
	}
	name := repoRelative(path, in.Cwd)
	if !firstLookup(in.SessionID, name) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	call, closeFn, ok := sessionToolCaller()
	if !ok {
		return
	}
	defer closeFn()
	block := fileIndex(ctx, call, name)
	if block == "" {
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(hookOutput{HookSpecificOutput: hookSpecificOutput{
		HookEventName:     "PreToolUse",
		AdditionalContext: block,
	}})
}

// fileIndex is the block for one file, or "" when the brain has no current
// memory linked to it.
func fileIndex(ctx context.Context, call toolCaller, name string) string {
	rows, err := cypherQuery(ctx, call, "MATCH (f:file) WHERE f.name = $name RETURN id(f)", map[string]any{"name": name}, 10)
	if err != nil {
		return ""
	}
	var memories []string
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		id, _ := row[0].(string)
		for _, line := range indexLines(currentMemoriesAbout(ctx, call, id), fileContextMax) {
			if !seen[line] {
				seen[line] = true
				memories = append(memories, line)
			}
		}
	}
	if len(memories) == 0 {
		return ""
	}
	header := fmt.Sprintf("CortexDB memories about %s, newest first. Fetch any in full with memory_get memory_ids.", name)
	return fitContext(header, memories, "", fileContextBudget)
}

// firstLookup reports whether this session has not looked this file up yet,
// and records that it now has. Without a session id every lookup is a first.
func firstLookup(sessionID, name string) bool {
	if sessionID == "" {
		return true
	}
	dir := filepath.Join(cacheRoot(), "file-context")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return true
	}
	pruneOld(dir, 7*24*time.Hour)
	path := filepath.Join(dir, safeName.ReplaceAllString(sessionID, "_"))
	if f, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if scanner.Text() == name {
				_ = f.Close()
				return false
			}
		}
		_ = f.Close()
	}
	if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = fmt.Fprintln(f, name)
		_ = f.Close()
	}
	return true
}

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func cacheRoot() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "cortexdb")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "cortexdb")
}

// pruneOld removes the per-session lists of sessions long over.
func pruneOld(dir string, age time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-age)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
