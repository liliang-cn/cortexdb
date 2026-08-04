package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// runExportMemory writes every stored memory to a directory of Markdown files —
// one file per memory with YAML frontmatter, plus a MEMORY.md index — mirroring
// Claude Code's file-based memory layout. One-shot mode behind
// `--export-memory [outdir]` (default ~/.cortexdb/memory-export).
func runExportMemory(args []string) {
	outDir := ""
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		outDir = args[0]
	}

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	if outDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			outDir = filepath.Join(home, ".cortexdb", "memory-export")
		} else {
			outDir = filepath.Join(filepath.Dir(dbPath), "memory-export")
		}
	}

	memories, source := loadAllMemories(context.Background())
	if len(memories) == 0 {
		fmt.Printf("no memories to export in %s\n", source)
		return
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: create %s: %v\n", outDir, err)
		os.Exit(1)
	}

	usedSlugs := make(map[string]int, len(memories))
	type indexEntry struct{ file, title, hook string }
	index := make([]indexEntry, 0, len(memories))

	for _, m := range memories {
		title := memoryTitle(m)
		slug := uniqueSlug(slugify(firstNonEmptyStr(title, m.ID)), usedSlugs)
		fileName := slug + ".md"
		if err := os.WriteFile(filepath.Join(outDir, fileName), []byte(renderMemoryMarkdown(m, title)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: write %s: %v\n", fileName, err)
			os.Exit(1)
		}
		index = append(index, indexEntry{file: fileName, title: title, hook: memoryHook(m)})
	}

	// MEMORY.md index — one line per memory, mirroring Claude Code's index.
	var b strings.Builder
	b.WriteString("# Memory Index\n\n")
	b.WriteString(fmt.Sprintf("_Exported %d memories from %s._\n\n", len(memories), dbPath))
	for _, e := range index {
		b.WriteString(fmt.Sprintf("- [%s](%s)", e.title, e.file))
		if e.hook != "" {
			b.WriteString(" — " + e.hook)
		}
		b.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(outDir, "MEMORY.md"), []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: write MEMORY.md: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("exported %d memories to %s\n", len(memories), outDir)
	fmt.Println(filepath.Join(outDir, "MEMORY.md"))
}

// renderMemoryMarkdown renders one memory as a Markdown file with YAML
// frontmatter (Claude Code memory-file shape: name/description/metadata).
func renderMemoryMarkdown(m cortexdb.MemoryRecord, title string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + slugify(firstNonEmptyStr(title, m.ID)) + "\n")
	b.WriteString("description: " + yamlInline(memoryHook(m)) + "\n")
	b.WriteString("metadata:\n")
	b.WriteString("  id: " + yamlInline(m.ID) + "\n")
	if m.Scope != "" {
		b.WriteString("  scope: " + yamlInline(m.Scope) + "\n")
	}
	if m.Namespace != "" {
		b.WriteString("  namespace: " + yamlInline(m.Namespace) + "\n")
	}
	if m.Importance != 0 {
		b.WriteString(fmt.Sprintf("  importance: %g\n", m.Importance))
	}
	if !m.CreatedAt.IsZero() {
		b.WriteString("  created_at: " + m.CreatedAt.UTC().Format(time.RFC3339) + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(m.Content))
	b.WriteString("\n")
	return b.String()
}

// memoryTitle picks a human title: an explicit metadata title, else the first
// line of the content, else the id.
func memoryTitle(m cortexdb.MemoryRecord) string {
	if m.Metadata != nil {
		for _, k := range []string{"title", "name"} {
			if v, ok := m.Metadata[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	line := firstLine(m.Content)
	if line != "" {
		return clip(line, 80)
	}
	return m.ID
}

// memoryHook is a short one-line summary for the index / description.
func memoryHook(m cortexdb.MemoryRecord) string {
	return clip(collapseWS(m.Content), 120)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func collapseWS(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

func firstNonEmptyStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// yamlInline quotes a scalar for a YAML value when it could be misparsed.
func yamlInline(s string) string {
	s = collapseWS(s)
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#\"'{}[],&*?|<>=!%@`") || strings.HasPrefix(s, "-") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

var slugNonWord = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a title into a filesystem-safe kebab slug. Non-ASCII (e.g. CJK)
// is stripped; if nothing survives, the caller falls back to the id.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugNonWord.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "memory"
	}
	return clipSlug(s, 60)
}

func clipSlug(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	return strings.Trim(s, "-")
}

// uniqueSlug disambiguates colliding slugs with a numeric suffix.
func uniqueSlug(slug string, used map[string]int) string {
	n := used[slug]
	used[slug] = n + 1
	if n == 0 {
		return slug
	}
	return fmt.Sprintf("%s-%d", slug, n+1)
}
