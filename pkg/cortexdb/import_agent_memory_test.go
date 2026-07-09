package cortexdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImportAgentMemory(t *testing.T) {
	// Build a fake Claude Code home: <root>/projects/<slug>/memory/*.md + CLAUDE.md
	root := t.TempDir()
	memDir := filepath.Join(root, "projects", "-Users-me-proj", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(dir, name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(memDir, "MEMORY.md", "# Memory Index\n- [x](x.md) — hook")
	write(memDir, "ports.md", "---\nname: port-preference\ndescription: user prefers uncommon ports\nmetadata:\n  type: user\n---\n\nUser prefers random ports above 3000 like 3076, not 8080.")
	write(memDir, "graph.md", "---\nname: relational-queries\ndescription: use graph for relational questions\nmetadata:\n  type: reference\n---\n\nFor who-uses-X questions use expand_graph, not lexical recall.")
	write(root, "CLAUDE.md", "Global rules: no co-author. Use ports above 3000.")

	dbPath := fmt.Sprintf("test_import_mem_%d.db", time.Now().UnixNano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	ctx := context.Background()

	rep, err := ImportAgentMemory(ctx, db, ImportAgentMemoryOptions{
		Roots:               []string{root},
		IncludeInstructions: true,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	// 2 memory files (MEMORY.md excluded) + 1 CLAUDE.md = 3.
	if rep.Imported != 3 {
		t.Fatalf("expected 3 imported, got %d (ids=%v)", rep.Imported, rep.IDs)
	}

	// The imported memory must be retrievable.
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
		Query:         "what ports does the user prefer",
		RetrievalMode: RetrievalModeLexical,
		TopK:          5,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	found := false
	for _, hit := range resp.Results {
		if hit.KnowledgeID == "agentmem:port-preference" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected imported port-preference memory to be retrievable, got %+v", resp.Results)
	}

	// Idempotent: a second import must not error or duplicate ids within a run.
	rep2, err := ImportAgentMemory(ctx, db, ImportAgentMemoryOptions{Roots: []string{root}, IncludeInstructions: true})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if rep2.Imported != 3 {
		t.Errorf("second import should re-save the same 3, got %d", rep2.Imported)
	}
}

func TestParseAgentMemoryFrontmatter(t *testing.T) {
	meta, body := parseAgentMemoryFrontmatter("---\nname: foo\ndescription: a thing\nmetadata:\n  type: reference\n---\n\nThe body text.")
	if meta["name"] != "foo" || meta["description"] != "a thing" || meta["type"] != "reference" {
		t.Errorf("bad meta: %+v", meta)
	}
	if body != "The body text." && body != "The body text.\n" {
		t.Errorf("bad body: %q", body)
	}
	// No frontmatter: whole thing is body.
	m2, b2 := parseAgentMemoryFrontmatter("just text")
	if len(m2) != 0 || b2 != "just text" {
		t.Errorf("expected passthrough, got meta=%v body=%q", m2, b2)
	}
}
