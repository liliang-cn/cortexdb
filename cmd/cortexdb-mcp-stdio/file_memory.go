package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Claude Code keeps a file memory per project: one Markdown file per memory
// under ~/.claude/projects/<slug>/memory/, with MEMORY.md as the index it loads
// into every session. A session's capture that restates one of them, in other
// words, adds nothing at recall time: the file is already in context, says
// more, and the restatement takes a top-k slot something new could have had
// (#142). So the capture is shown what the files hold and told to leave it.

// fileMemoryRunes bounds what the files add to the capture prompt. Past it the
// rest of the files are shown by their name and description alone.
const fileMemoryRunes = 24000

// projectSlugChars is what Claude Code turns into "-" when it names a
// project's directory after its path: /Users/a/my.app is -Users-a-my-app.
var projectSlugChars = regexp.MustCompile(`[^A-Za-z0-9]`)

// fileMemoryDir is the memory directory of the project a Claude Code session
// ran in, or "" when there is none. The transcript lives in the project's
// directory, so its sibling is the first place looked; the slug of cwd is the
// second, for a transcript read from elsewhere.
func fileMemoryDir(transcriptPath, cwd string) string {
	var candidates []string
	if transcriptPath != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(transcriptPath), "memory"))
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		if home, err := os.UserHomeDir(); err == nil {
			slug := projectSlugChars.ReplaceAllString(filepath.Clean(cwd), "-")
			candidates = append(candidates, filepath.Join(home, ".claude", "projects", slug, "memory"))
		}
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

// fileMemory is one memory file: what its frontmatter names it and says it
// is, and its body.
type fileMemory struct {
	File, Name, Description, Body string
}

// readFileMemory reads every memory file in dir but the index, sorted by file
// name. A file that cannot be read is left out.
func readFileMemory(dir string) []fileMemory {
	if dir == "" {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(paths)
	var out []fileMemory
	for _, p := range paths {
		if filepath.Base(p) == "MEMORY.md" {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		m := parseFileMemory(string(raw))
		m.File = filepath.Base(p)
		if m.Name == "" {
			m.Name = strings.TrimSuffix(m.File, ".md")
		}
		if m.Description == "" && m.Body == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// parseFileMemory splits a memory file into its frontmatter's name and
// description and the body after it. A file without frontmatter is all body.
func parseFileMemory(raw string) fileMemory {
	var m fileMemory
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if front, body, ok := strings.Cut(rest, "\n---"); ok {
			for _, line := range strings.Split(front, "\n") {
				key, value, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				value = strings.Trim(strings.TrimSpace(value), `"'`)
				switch strings.TrimSpace(key) {
				case "name":
					m.Name = value
				case "description":
					m.Description = value
				}
			}
			// The closing fence's own line ends after "---".
			_, body, _ = strings.Cut(body, "\n")
			text = body
		}
	}
	m.Body = strings.TrimSpace(text)
	return m
}

// recordedInFiles is the file memory as the capture prompt shows it: every
// file by its name and description, which is what the index in context says,
// and as many bodies as fit in fileMemoryRunes beside them, in file order.
// "" when there are none.
func recordedInFiles(memories []fileMemory) string {
	if len(memories) == 0 {
		return ""
	}
	heads := make([]string, len(memories))
	used := 0
	for i, m := range memories {
		heads[i] = "- " + m.Name
		if m.Description != "" {
			heads[i] += ": " + m.Description
		}
		used += len([]rune(heads[i])) + 1
	}
	if used > fileMemoryRunes {
		return clip(strings.Join(heads, "\n"), fileMemoryRunes)
	}
	lines := make([]string, len(memories))
	for i, m := range memories {
		lines[i] = heads[i]
		body := indent(m.Body)
		if body == "" {
			continue
		}
		if n := len([]rune(body)) + 1; used+n <= fileMemoryRunes {
			lines[i] += "\n" + body
			used += n
		}
	}
	return strings.Join(lines, "\n")
}

func indent(s string) string {
	if s == "" {
		return ""
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

// recorded is the file memory of the project a transcript's session ran in,
// as the capture prompt shows it. Only a Claude Code session has one; for the
// others, and for a project without one, it is "" and capture runs as before.
func (t transcript) recorded(cwd string) string {
	if t.Kind != "claude" {
		return ""
	}
	return recordedInFiles(readFileMemory(fileMemoryDir(t.Path, cwd)))
}
