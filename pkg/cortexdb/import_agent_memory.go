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

// AgentMemoryItem is one note found on disk by ScanAgentMemory, shaped the way
// ImportAgentMemory stores it: as durable knowledge under a stable id.
type AgentMemoryItem struct {
	ID      string
	Title   string
	Content string
	Type    string
	Path    string
}

// KnowledgeRequest is the knowledge record ImportAgentMemory saves for this
// item. Exported so a caller writing somewhere other than a local *DB (a
// shared brain, over its tools) stores exactly the same record.
func (it AgentMemoryItem) KnowledgeRequest(collection string) KnowledgeSaveRequest {
	if collection == "" {
		collection = "agent_memory"
	}
	return KnowledgeSaveRequest{
		KnowledgeID: it.ID,
		Title:       it.Title,
		Content:     it.Content,
		Collection:  collection,
		Metadata:    map[string]string{"source": "agent_memory", "type": it.Type, "path": it.Path},
	}
}

// ScanAgentMemory finds Claude Code / Codex memory on disk without writing
// anything: the file-based memory store (<root>/projects/*/memory/*.md, each a
// markdown note with YAML frontmatter) and, when asked, the CLAUDE.md /
// AGENTS.md instruction files at each root. Empty notes are left out and
// counted in skipped; an id met twice is kept once.
func ScanAgentMemory(opts ImportAgentMemoryOptions) (items []AgentMemoryItem, skipped int, err error) {
	roots := opts.Roots
	if len(roots) == 0 {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return nil, 0, fmt.Errorf("cortexdb: resolve home: %w", herr)
		}
		roots = []string{filepath.Join(home, ".claude")}
	}
	seen := make(map[string]struct{})
	add := func(it AgentMemoryItem) {
		it.Content = strings.TrimSpace(it.Content)
		if it.Content == "" {
			skipped++
			return
		}
		if _, ok := seen[it.ID]; ok {
			return
		}
		seen[it.ID] = struct{}{}
		items = append(items, it)
	}
	for _, root := range roots {
		memFiles, _ := filepath.Glob(filepath.Join(root, "projects", "*", "memory", "*.md"))
		for _, f := range memFiles {
			if strings.EqualFold(filepath.Base(f), "MEMORY.md") {
				continue // the index of pointers, not a memory itself
			}
			data, rerr := os.ReadFile(f)
			if rerr != nil {
				continue
			}
			meta, body := parseAgentMemoryFrontmatter(string(data))
			name := firstNonEmpty(meta["name"], strings.TrimSuffix(filepath.Base(f), ".md"))
			add(AgentMemoryItem{
				ID:      "agentmem:" + agentMemorySlug(name),
				Title:   firstNonEmpty(meta["description"], name),
				Content: body,
				Type:    firstNonEmpty(meta["type"], "reference"),
				Path:    f,
			})
		}
		if opts.IncludeInstructions {
			for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
				p := filepath.Join(root, name)
				data, rerr := os.ReadFile(p)
				if rerr != nil {
					continue
				}
				add(AgentMemoryItem{
					ID:      "agentmem:instructions:" + agentMemorySlug(root+"-"+name),
					Title:   name + " (" + root + ")",
					Content: string(data),
					Type:    "instructions",
					Path:    p,
				})
			}
		}
	}
	return items, skipped, nil
}

// ImportAgentMemory ingests Claude Code / Codex memory into CortexDB so it is
// searchable through knowledge_memory_recall. It saves everything
// ScanAgentMemory finds as durable knowledge. Re-running is idempotent (stable
// ids).
func ImportAgentMemory(ctx context.Context, db *DB, opts ImportAgentMemoryOptions) (*ImportAgentMemoryReport, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: import agent memory: nil db")
	}
	items, skipped, err := ScanAgentMemory(opts)
	if err != nil {
		return nil, err
	}
	report := &ImportAgentMemoryReport{Skipped: skipped}
	for _, it := range items {
		report.FilesScanned++
		if !opts.DryRun {
			if _, err := db.SaveKnowledge(ctx, it.KnowledgeRequest(opts.Collection)); err != nil {
				return report, fmt.Errorf("cortexdb: save %q: %w", it.ID, err)
			}
		}
		report.Imported++
		report.IDs = append(report.IDs, it.ID)
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
