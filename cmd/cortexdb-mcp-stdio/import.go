package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runImportAgentMemory imports Claude Code / Codex memory (the file-based memory
// store plus CLAUDE.md / AGENTS.md) into the CortexDB brain, then prints a
// report. One-shot mode behind `--import-agent-memory`, used by
// /cortexdb-import-memory. Extra args are treated as additional roots to scan.
func runImportAgentMemory(extraRoots []string) {
	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	roots := extraRoots
	if len(roots) == 0 {
		if home, herr := os.UserHomeDir(); herr == nil {
			roots = []string{filepath.Join(home, ".claude"), filepath.Join(home, ".codex")}
		}
	}

	ctx := context.Background()
	rep, err := cortexdb.ImportAgentMemory(ctx, db, cortexdb.ImportAgentMemoryOptions{
		Roots:               roots,
		IncludeInstructions: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: import agent memory: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("imported %d agent-memory items (scanned %d, skipped %d) into %s\n",
		rep.Imported, rep.FilesScanned, rep.Skipped, dbPath)

	// Organize the imported memory into the knowledge graph (entities +
	// co-occurrence relations) so it is graph-queryable, not just lexically
	// searchable. Deterministic — no LLM. Non-fatal.
	if org, oerr := graphflow.OrganizeFromBrain(ctx, db, graphflow.OrganizeOptions{
		IncludeMemories:  true,
		IncludeKnowledge: true,
	}); oerr != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: organize into graph: %v\n", oerr)
	} else if org != nil {
		fmt.Printf("organized into graph: %d new entities, %d relations (from %d texts)\n",
			org.EntityCount, org.RelationCount, org.DocumentsScanned)
	}
	for _, id := range rep.IDs {
		fmt.Printf("  %s\n", id)
	}
}
