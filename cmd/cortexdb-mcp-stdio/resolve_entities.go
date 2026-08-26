package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runResolveEntities merges duplicate/alias entity nodes into canonical ones.
// One-shot mode behind `--resolve-entities [--dry-run] [--types T,U]`.
// Deterministic by default (case/space/punctuation variants); when
// CORTEXDB_LLM_* is set it also merges acronyms/synonyms (K8s ↔ Kubernetes).
//
// --types restricts the merge to entities of those node types, which a store
// holding more than one kind of graph needs: a code graph leaves each symbol's
// bare name as its content, so every package's main.go shares a canonical key
// and would otherwise be merged into one file.
func runResolveEntities(args []string) {
	dryRun := false
	var nodeTypes []string
	for i, a := range args {
		switch {
		case a == "--dry-run" || a == "-n":
			dryRun = true
		case a == "--types" && i+1 < len(args):
			nodeTypes = splitTypes(args[i+1])
		case strings.HasPrefix(a, "--types="):
			nodeTypes = splitTypes(strings.TrimPrefix(a, "--types="))
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
		LLM:       llm,
		DryRun:    dryRun,
		NodeTypes: nodeTypes,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: resolve entities: %v\n", err)
		os.Exit(1)
	}

	verb := "merged"
	if dryRun {
		verb = "would merge"
	}
	scope := "all entities"
	if len(nodeTypes) > 0 {
		scope = strings.Join(nodeTypes, ", ")
	}
	fmt.Printf("scope: %s\n", scope)
	fmt.Printf("%s %d alias entities into %d canonical (of %d entities) in %s\n",
		verb, report.EntitiesMerged, len(report.Groups), report.EntitiesBefore, dbPath)
	for _, g := range report.Groups {
		fmt.Printf("  %s  ←  %v\n", g.Canonical, g.Aliases)
	}
}

// splitTypes parses a comma-separated --types value, ignoring blanks so
// "--types=A,,B" and a trailing comma both behave.
func splitTypes(v string) []string {
	var out []string
	for _, t := range strings.Split(v, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
