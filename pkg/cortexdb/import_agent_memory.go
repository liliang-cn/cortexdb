package cortexdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ImportAgentMemoryOptions configures ImportAgentMemory.
type ImportAgentMemoryOptions struct {
	// Roots to scan for agent memory. When empty, defaults to the Claude Code
	// home (~/.claude). Add ~/.codex or a project dir to include those.
	Roots []string
	// Collection to store imported knowledge under (default "agent_memory").
	Collection string
	// IncludeInstructions also imports CLAUDE.md / AGENTS.md instruction files,
	// not just the file-based memory store.
	IncludeInstructions bool
	// DryRun scans and reports without writing anything.
	DryRun bool
}

// ImportAgentMemoryReport summarizes an import pass.
type ImportAgentMemoryReport struct {
	FilesScanned int      `json:"files_scanned"`
	Imported     int      `json:"imported"`
	Skipped      int      `json:"skipped"`
	IDs          []string `json:"ids,omitempty"`
}

// ImportAgentMemory ingests Claude Code / Codex memory into CortexDB so it is
// searchable through knowledge_memory_recall. It reads the file-based memory
// store (<root>/projects/*/memory/*.md, each a markdown note with YAML
// frontmatter) and, optionally, the CLAUDE.md / AGENTS.md instruction files,
// saving each as durable knowledge. Re-running is idempotent (stable ids).
func ImportAgentMemory(ctx context.Context, db *DB, opts ImportAgentMemoryOptions) (*ImportAgentMemoryReport, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: import agent memory: nil db")
	}
	roots := opts.Roots
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cortexdb: resolve home: %w", err)
		}
		roots = []string{filepath.Join(home, ".claude")}
	}
	collection := opts.Collection
	if collection == "" {
		collection = "agent_memory"
	}

	report := &ImportAgentMemoryReport{}
	seen := make(map[string]struct{})
	save := func(id, title, content, typ, srcPath string) error {
		content = strings.TrimSpace(content)
		if content == "" {
			report.Skipped++
			return nil
		}
		if _, ok := seen[id]; ok {
			return nil
		}
		seen[id] = struct{}{}
		report.FilesScanned++
		if opts.DryRun {
			report.Imported++
			report.IDs = append(report.IDs, id)
			return nil
		}
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
			KnowledgeID: id,
			Title:       title,
			Content:     content,
			Collection:  collection,
			Metadata:    map[string]string{"source": "agent_memory", "type": typ, "path": srcPath},
		}); err != nil {
			return fmt.Errorf("cortexdb: save %q: %w", id, err)
		}
		report.Imported++
		report.IDs = append(report.IDs, id)
		return nil
	}

	for _, root := range roots {
		// File-based memory: <root>/projects/*/memory/*.md
		memFiles, _ := filepath.Glob(filepath.Join(root, "projects", "*", "memory", "*.md"))
		for _, f := range memFiles {
			if strings.EqualFold(filepath.Base(f), "MEMORY.md") {
				continue // the index of pointers, not a memory itself
			}
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			meta, body := parseAgentMemoryFrontmatter(string(data))
			name := firstNonEmpty(meta["name"], strings.TrimSuffix(filepath.Base(f), ".md"))
			id := "agentmem:" + agentMemorySlug(name)
			title := firstNonEmpty(meta["description"], name)
			typ := firstNonEmpty(meta["type"], "reference")
			if err := save(id, title, body, typ, f); err != nil {
				return report, err
			}
		}

		if opts.IncludeInstructions {
			for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
				p := filepath.Join(root, name)
				data, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				id := "agentmem:instructions:" + agentMemorySlug(root+"-"+name)
				if err := save(id, name+" ("+root+")", string(data), "instructions", p); err != nil {
					return report, err
				}
			}
		}
	}
	return report, nil
}

// parseAgentMemoryFrontmatter splits leading YAML frontmatter (--- … ---) from
// the body and returns a flat key->value map (nested keys like metadata.type
// are flattened to their leaf key, e.g. "type"). Values are unquoted.
func parseAgentMemoryFrontmatter(raw string) (map[string]string, string) {
	meta := map[string]string{}
	s := strings.TrimLeft(strings.TrimPrefix(raw, "\ufeff"), " \t\r\n")
	if !strings.HasPrefix(s, "---") {
		return meta, raw
	}
	rest := s[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, raw
	}
	front := rest[:end]
	body := rest[end+4:]
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	}
	body = strings.TrimLeft(body, "\r\n")
	for _, line := range strings.Split(front, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		i := strings.IndexByte(t, ':')
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(t[:i])
		val := strings.TrimSpace(t[i+1:])
		val = strings.Trim(val, `"'`)
		if val != "" {
			meta[key] = val
		}
	}
	return meta, body
}

// agentMemorySlug normalizes a name into a stable id fragment.
func agentMemorySlug(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "memory"
	}
	return slug
}
