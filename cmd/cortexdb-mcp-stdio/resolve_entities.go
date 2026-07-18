package main

import (
	"context"
	"fmt"
	"os"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runResolveEntities merges duplicate/alias entity nodes into canonical ones.
// One-shot mode behind `--resolve-entities [--dry-run]`. Deterministic by
// default (case/space/punctuation variants); when CORTEXDB_LLM_* is set it also
// merges acronyms/synonyms (K8s ↔ Kubernetes).
func runResolveEntities(args []string) {
	dryRun := false
	for _, a := range args {
		if a == "--dry-run" || a == "-n" {
			dryRun = true
		}
	}

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	db, err := openBrainDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	llm := newOrganizeLLM()
	if llm != nil {
		fmt.Fprintln(os.Stderr, "cortexdb: resolving entities with LLM acronym/synonym detection (CORTEXDB_LLM_*)")
	}

	report, err := graphflow.ResolveEntities(context.Background(), db, graphflow.ResolveOptions{
		LLM:    llm,
		DryRun: dryRun,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: resolve entities: %v\n", err)
		os.Exit(1)
	}

	verb := "merged"
	if dryRun {
		verb = "would merge"
	}
	fmt.Printf("%s %d alias entities into %d canonical (of %d entities) in %s\n",
		verb, report.EntitiesMerged, len(report.Groups), report.EntitiesBefore, dbPath)
	for _, g := range report.Groups {
		fmt.Printf("  %s  ←  %v\n", g.Canonical, g.Aliases)
	}
}
