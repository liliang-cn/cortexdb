package cortexdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// MemorySyncOptions configures PlanMemorySync.
type MemorySyncOptions struct {
	// Dir holds one Markdown file per memory, in the shape --export-memory
	// writes: YAML frontmatter carrying metadata.id, then the body. MEMORY.md is
	// the index and is skipped.
	Dir string
	// Prune deletes memories that the directory no longer contains. Off by
	// default: an import that only ever adds cannot lose anything, whereas a
	// prune against the wrong directory would empty the brain.
	Prune bool
}

// MemorySyncPlan is what a sync would do, computed before anything is written.
//
// Planning is separated from applying because the brain is not always local: the
// CLI applies the same plan over gRPC when CORTEXDB_REMOTE is set. Keeping the
// decision pure also makes it testable without a database.
type MemorySyncPlan struct {
	// Scanned counts memory files read from the directory.
	Scanned int
	// Create are memories in the directory that the store does not have — a file
	// written by hand, or one restored from a backup.
	Create []MemorySaveRequest
	// Update are memories whose file body differs from the stored content.
	Update []MemorySaveRequest
	// Delete are ids the store has and the directory does not. Empty unless
	// Prune is set.
	Delete []string
	// Unchanged counts files whose body already matches the store.
	Unchanged int
}

// Empty reports whether applying this plan would change nothing.
func (p *MemorySyncPlan) Empty() bool {
	return p == nil || (len(p.Create) == 0 && len(p.Update) == 0 && len(p.Delete) == 0)
}

// MemorySyncReport is the outcome of applying a plan.
type MemorySyncReport struct {
	Created   int      `json:"created"`
	Updated   int      `json:"updated"`
	Deleted   int      `json:"deleted"`
	Unchanged int      `json:"unchanged"`
	IDs       []string `json:"ids,omitempty"`
}

// PlanMemorySync diffs a directory of memory Markdown files against the memories
// a store currently holds.
//
// The directory is the source of truth, which is the whole point: editing a
// memory means editing a file, and deleting one means deleting a file. Without
// this the only way to remove a wrong memory was to call the delete tool with an
// id nobody has written down.
func PlanMemorySync(current []MemoryRecord, opts MemorySyncOptions) (*MemorySyncPlan, error) {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		return nil, fmt.Errorf("cortexdb: sync memory: no directory given")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: sync memory: read %s: %w", dir, err)
	}

	byID := make(map[string]MemoryRecord, len(current))
	for _, m := range current {
		byID[m.ID] = m
	}

	plan := &MemorySyncPlan{}
	seen := make(map[string]struct{}, len(entries))

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		if strings.EqualFold(e.Name(), "MEMORY.md") {
			continue // the index of pointers, not a memory itself
		}
		files = append(files, e.Name())
	}
	// Stable order so a dry run reads the same twice and tests do not flake.
	sort.Strings(files)

	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("cortexdb: sync memory: read %s: %w", name, err)
		}
		meta, body := parseAgentMemoryFrontmatter(string(raw))
		body = strings.TrimSpace(body)
		if body == "" {
			continue // an empty file says nothing; deleting means removing the file
		}
		plan.Scanned++

		// metadata.id is what --export-memory wrote, so a round trip lands on the
		// same record. A hand-written file has none and gets one from its name.
		id := firstNonEmpty(meta["id"], meta["name"], strings.TrimSuffix(name, filepath.Ext(name)))
		seen[id] = struct{}{}

		req := MemorySaveRequest{
			MemoryID:   id,
			Content:    body,
			Scope:      meta["scope"],
			Namespace:  meta["namespace"],
			Importance: parseFloatOrZero(meta["importance"]),
		}
		existing, ok := byID[id]
		switch {
		case !ok:
			plan.Create = append(plan.Create, req)
		case strings.TrimSpace(existing.Content) != body:
			// Keep the record where it already lives unless the file overrides it,
			// so editing a body cannot silently move a memory to another bucket.
			req.Scope = firstNonEmpty(req.Scope, existing.Scope)
			req.Namespace = firstNonEmpty(req.Namespace, existing.Namespace)
			if req.Importance == 0 {
				req.Importance = existing.Importance
			}
			plan.Update = append(plan.Update, req)
		default:
			plan.Unchanged++
		}
	}

	if opts.Prune {
		// A directory with no memory files is far more likely to be the wrong path
		// than a genuine instruction to delete everything.
		if plan.Scanned == 0 {
			return nil, fmt.Errorf("cortexdb: sync memory: refusing to prune from %s: it holds no memory files", dir)
		}
		for _, m := range current {
			if _, ok := seen[m.ID]; !ok {
				plan.Delete = append(plan.Delete, m.ID)
			}
		}
		sort.Strings(plan.Delete)
	}
	return plan, nil
}

// ApplyMemorySync writes a plan to a local store. The CLI applies the same plan
// over gRPC when the brain is remote.
func ApplyMemorySync(ctx context.Context, db *DB, plan *MemorySyncPlan) (*MemorySyncReport, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: sync memory: nil db")
	}
	if plan == nil {
		return nil, fmt.Errorf("cortexdb: sync memory: nil plan")
	}
	report := &MemorySyncReport{Unchanged: plan.Unchanged}
	for _, req := range plan.Create {
		if _, err := db.SaveMemory(ctx, req); err != nil {
			return report, fmt.Errorf("cortexdb: sync memory: create %q: %w", req.MemoryID, err)
		}
		report.Created++
		report.IDs = append(report.IDs, req.MemoryID)
	}
	for _, req := range plan.Update {
		if _, err := db.SaveMemory(ctx, req); err != nil {
			return report, fmt.Errorf("cortexdb: sync memory: update %q: %w", req.MemoryID, err)
		}
		report.Updated++
		report.IDs = append(report.IDs, req.MemoryID)
	}
	for _, id := range plan.Delete {
		if _, err := db.DeleteMemory(ctx, MemoryDeleteRequest{MemoryID: id}); err != nil {
			return report, fmt.Errorf("cortexdb: sync memory: delete %q: %w", id, err)
		}
		report.Deleted++
	}
	return report, nil
}

// SyncMemoryDir plans and applies in one call against a local store.
func SyncMemoryDir(ctx context.Context, db *DB, opts MemorySyncOptions) (*MemorySyncReport, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: sync memory: nil db")
	}
	current, err := db.ListAllMemories(ctx)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: sync memory: list memories: %w", err)
	}
	plan, err := PlanMemorySync(current, opts)
	if err != nil {
		return nil, err
	}
	return ApplyMemorySync(ctx, db, plan)
}

func parseFloatOrZero(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}
