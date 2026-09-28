package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runResolveEntities resolves duplicate/alias entity nodes.
// One-shot mode behind `--resolve-entities [--dry-run] [--types T,U] [--merge-all]`.
//
// Link-first by default: a candidate pair is scored on name similarity,
// shared neighbours and type agreement; at 0.95 or above it is merged, from
// 0.85 it gets a possiblySame edge graded held for a person
// (contract_needs_attention lists them). When CORTEXDB_LLM_* is set the model
// also proposes acronyms/synonyms (K8s ↔ Kubernetes) as candidates.
// --merge-all restores the earlier behaviour: merge every normalized-key group
// and every model group without scoring.
//
// --types restricts the merge to entities of those node types, which a store
// holding more than one kind of graph needs: a code graph leaves each symbol's
// bare name as its content, so every package's main.go shares a canonical key
// and would otherwise be merged into one file.
func runResolveEntities(args []string) {
	dryRun := false
	mode := graphflow.ResolveLinkFirst
	var nodeTypes []string
	for i, a := range args {
		switch {
		case a == "--dry-run" || a == "-n":
			dryRun = true
		case a == "--merge-all":
			mode = graphflow.ResolveMergeAll
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
		Mode:      mode,
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
	if mode == graphflow.ResolveLinkFirst {
		linkVerb := "linked"
		if dryRun {
			linkVerb = "would link"
		}
		fmt.Printf("%s %d possible alias pairs for review (possiblySame, held — see contract_needs_attention)\n",
			linkVerb, report.EntitiesLinked)
		for _, l := range report.Links {
			fmt.Printf("  %s  ~  %s  score %.2f (name %.2f, shared neighbours %d, type %.1f)\n",
				l.A, l.B, l.Score, l.NameSimilarity, l.SharedNeighbours, l.TypeAgreement)
		}
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
