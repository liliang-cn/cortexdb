package cli

// Memories about files.
//
// A capture names the files a session worked on, and the model attaches the
// ones each memory is about as entities of type "file". The PreToolUse hook
// then answers "what happened to this file before" when an agent is about to
// read or edit it (file_context.go).
//
// A file is named by its path from the root of its repository, so the same
// file has the same name on every machine and in every checkout:
// "pkg/cortexdb/brain.go", not "/Users/someone/src/CortexDB/pkg/cortexdb/brain.go".

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// repoRelative names path by its place in its repository: relative to the
// nearest directory above it that holds .git, else to cwd when it is under
// cwd, else as given. cwd resolves a relative path.
func repoRelative(path, cwd string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			if rel, err := filepath.Rel(dir, path); err == nil {
				return filepath.ToSlash(rel)
			}
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}

// fileEditTools are the Claude Code tools that change the file they name.
var fileEditTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

// fileTools are the tools whose input names a file, and the field it is in.
var fileTools = map[string]bool{"Read": true, "Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

// touchedFiles collects the files a session read and modified, in the order
// first touched, bounded so a session that walked a whole tree does not
// crowd out the conversation.
type touchedFiles struct {
	modified, read []string
	seen           map[string]bool
}

const maxTouchedFiles = 40

func (t *touchedFiles) add(tool, path, cwd string) {
	if !fileTools[tool] || path == "" {
		return
	}
	name := repoRelative(path, cwd)
	if t.seen == nil {
		t.seen = map[string]bool{}
	}
	key := name
	if fileEditTools[tool] {
		key = "w:" + name
	}
	if t.seen[key] || len(t.modified)+len(t.read) >= maxTouchedFiles {
		return
	}
	t.seen[key] = true
	if fileEditTools[tool] {
		t.modified = append(t.modified, name)
	} else {
		t.read = append(t.read, name)
	}
}

// section is the FILES block a digest starts with, or "" when no file was
// touched. A file both read and modified is listed as modified.
func (t *touchedFiles) section() string {
	if len(t.modified)+len(t.read) == 0 {
		return ""
	}
	modified := map[string]bool{}
	for _, f := range t.modified {
		modified[f] = true
	}
	var read []string
	for _, f := range t.read {
		if !modified[f] {
			read = append(read, f)
		}
	}
	sort.Strings(read)
	var b strings.Builder
	b.WriteString("FILES (paths from the repository root)\n")
	if len(t.modified) > 0 {
		b.WriteString("modified: " + strings.Join(t.modified, ", ") + "\n")
	}
	if len(read) > 0 {
		b.WriteString("read: " + strings.Join(read, ", ") + "\n")
	}
	return b.String() + "\n"
}
