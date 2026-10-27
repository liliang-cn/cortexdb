package main

import (
	"context"
	"encoding/json"
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
	// --min-grade G writes only the memories known at least that well — the
	// lessons worth handing to someone else, without the guesses.
	outDir, minGrade := "", ""
	for i := 0; i < len(args); i++ {
		switch a := strings.TrimSpace(args[i]); {
		case a == "--min-grade" && i+1 < len(args):
			minGrade = strings.ToLower(strings.TrimSpace(args[i+1]))
			i++
		case strings.HasPrefix(a, "--min-grade="):
			minGrade = strings.ToLower(strings.TrimPrefix(a, "--min-grade="))
		case a != "" && outDir == "":
			outDir = a
		}
	}
	if minGrade != "" && !cortexdb.IsGrade(minGrade) {
		fmt.Fprintf(os.Stderr, "cortexdb: --min-grade %q is not one of verified, self_consistent, asserted, held, refused\n", minGrade)
		os.Exit(2)
	}
	// A filtered export into the directory --sync-memory reads back would read
	// every memory it left out as deleted.
	if minGrade != "" && outDir == "" {
		fmt.Fprintln(os.Stderr, "cortexdb: --min-grade writes part of the brain: name an output directory other than the one you sync back")
		os.Exit(2)
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
	if minGrade != "" {
		kept := memories[:0]
		for _, m := range memories {
			if cortexdb.GradeAtLeast(m.Grade, minGrade) {
				kept = append(kept, m)
			}
		}
		memories = kept
	}
	if len(memories) == 0 {
		fmt.Printf("no memories to export in %s\n", source)
		return
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: create %s: %v\n", outDir, err)
		os.Exit(1)
	}

	usedSlugs := make(map[string]int, len(memories))
	// The index owns MEMORY.md, and macOS and Windows do not distinguish it from
	// memory.md. A memory whose title is entirely CJK used to slug to exactly
	// that, so writing the index destroyed it — silently, and the memory then
	// looked deleted to anything reading the directory back.
	usedSlugs[strings.ToLower(memoryIndexSlug)] = 1
	type indexEntry struct{ file, title, hook, mark string }
	index := make([]indexEntry, 0, len(memories))

	for _, m := range memories {
		title := memoryTitle(m)
		// The id is the better fallback than a placeholder: it is always present
		// and, unlike a CJK title, survives slugging.
		slug := uniqueSlug(firstNonEmptyStr(slugify(title), firstNonEmptyStr(slugify(m.ID), "untitled")), usedSlugs)
		fileName := slug + ".md"
		if err := os.WriteFile(filepath.Join(outDir, fileName), []byte(renderMemoryMarkdown(m, title, slug)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: write %s: %v\n", fileName, err)
			os.Exit(1)
		}
		index = append(index, indexEntry{file: fileName, title: title, hook: memoryHook(m), mark: gradeMark(m.Grade, "")})
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
		if e.mark != "" {
			b.WriteString(" [" + e.mark + "]")
		}
		b.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(outDir, "MEMORY.md"), []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: write MEMORY.md: %v\n", err)
		os.Exit(1)
	}

	// Counting is the difference between an export and a claim. Two collisions
	// used to leave the directory a memory short while this line still reported
	// the full number — and anything syncing the directory back read the gap as
	// a deletion.
	written, err := countExportedFiles(outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: verify export: %v\n", err)
		os.Exit(1)
	}
	if written != len(memories) {
		fmt.Fprintf(os.Stderr, "cortexdb: export wrote %d files for %d memories in %s — refusing to report success, since syncing this directory back would read the difference as deletions\n",
			written, len(memories), outDir)
		os.Exit(1)
	}

	fmt.Printf("exported %d memories to %s\n", len(memories), outDir)
	fmt.Println(filepath.Join(outDir, "MEMORY.md"))
}

// renderMemoryMarkdown renders one memory as a Markdown file with YAML
// frontmatter (Claude Code memory-file shape: name/description/metadata).
func renderMemoryMarkdown(m cortexdb.MemoryRecord, title, slug string) string {
	var b strings.Builder
	b.WriteString("---\n")
	// The slug the file was actually given, so name and filename agree even when
	// the title slugs to nothing.
	b.WriteString("name: " + slug + "\n")
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
	// Who wrote it and when it dies. Without `source` an export cannot tell a
	// verbatim transcript turn from a distilled experience — both carry the
	// same harness-<timestamp>-<n> id — and a backup that cannot say which
	// rows a cleanup may take is not a backup for that cleanup.
	if src, ok := m.Metadata["source"].(string); ok && strings.TrimSpace(src) != "" {
		b.WriteString("  source: " + yamlInline(src) + "\n")
	}
	if m.ExpiresAt != nil && !m.ExpiresAt.IsZero() {
		b.WriteString("  expires_at: " + m.ExpiresAt.UTC().Format(time.RFC3339) + "\n")
	}
	// How it is known, so a lesson shared as a file keeps saying whether it
	// was checked; --sync-memory reads both back.
	if m.Grade != "" {
		b.WriteString("  grade: " + yamlInline(m.Grade) + "\n")
		if why := strings.TrimSpace(m.GradeWhy); why != "" {
			b.WriteString("  grade_why: " + yamlInline(why) + "\n")
		}
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
		// A JSON string is a valid YAML double-quoted scalar. HTML escaping
		// is off: the frontmatter is read by people, and "<" written as
		// "\u003c" is correct YAML that nobody can read.
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(s)
		return strings.TrimSuffix(b.String(), "\n")
	}
	return s
}

var slugNonWord = regexp.MustCompile(`[^a-z0-9]+`)

// memoryIndexSlug is the name the index file takes, reserved so no memory can
// be given it.
const memoryIndexSlug = "MEMORY"

// slugify turns a title into a filesystem-safe kebab slug. Non-ASCII (e.g. CJK)
// is stripped; it returns "" when nothing survives so the caller can fall back
// to the id. It used to return "memory" instead, which collided with MEMORY.md
// on every case-insensitive filesystem.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugNonWord.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return ""
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

// uniqueSlug disambiguates colliding slugs with a numeric suffix, checking each
// candidate rather than assuming the suffixed form is free.
//
// It was not: "situation" seen nine times produced "situation-9", which is also
// what a memory actually titled "situation 9" slugs to, and the second write
// replaced the first. Silently — the file count was never compared to the number
// of memories.
func uniqueSlug(slug string, used map[string]int) string {
	for n := 1; ; n++ {
		candidate := slug
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d", slug, n)
		}
		if _, taken := used[strings.ToLower(candidate)]; !taken {
			used[strings.ToLower(candidate)] = 1
			return candidate
		}
	}
}

// countExportedFiles counts memory files in a directory, excluding the index.
func countExportedFiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		if strings.EqualFold(e.Name(), memoryIndexSlug+".md") {
			continue
		}
		n++
	}
	return n, nil
}
