package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runGraphUpdate reconciles new text against the existing knowledge graph with
// an LLM, producing and applying add/update/delete edits. One-shot behind
// `--graph-update [file] [--dry-run] [--allow-delete]` (stdin when no file).
//
// This is the LLM-driven maintenance path: unlike ingestion (which only adds),
// it can correct and retract. It needs no embedder — relevant existing entities
// are found by lexical mention — so it works with only CORTEXDB_LLM_* set.
func runGraphUpdate(args []string) {
	dryRun := false
	allowDelete := false
	path := "-"
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			dryRun = true
		case "--allow-delete":
			allowDelete = true
		default:
			if strings.TrimSpace(a) != "" && !strings.HasPrefix(a, "-") {
				path = a
			}
		}
	}

	var raw []byte
	var err error
	if path == "-" {
		raw, err = readAllStdin()
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: read text: %v\n", err)
		os.Exit(1)
	}
	text := strings.TrimSpace(string(stripBOM(raw)))
	if text == "" {
		fmt.Fprintln(os.Stderr, "cortexdb: graph update: no input text (pass a file or pipe text on stdin)")
		os.Exit(2)
	}

	llm := newOrganizeLLM()
	if llm == nil {
		fmt.Fprintln(os.Stderr, "cortexdb: graph update needs an LLM — set CORTEXDB_LLM_BASE_URL (e.g. http://localhost:11434) and CORTEXDB_LLM_MODEL")
		os.Exit(1)
	}

	db, dbPath := openLearningDB()
	defer func() { _ = db.Close() }()

	opts := graphflow.GraphEditOptions{LLM: llm, DryRun: dryRun, AllowDelete: allowDelete}
	report, err := graphflow.UpdateGraphFromText(context.Background(), db, text, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: graph update: %v\n", err)
		os.Exit(1)
	}

	verb := "applied"
	if dryRun {
		verb = "would apply"
	}
	fmt.Printf("%s to %s: +%d entities, ~%d updated, -%d entities, +%d relations, -%d relations\n",
		verb, dbPath, report.EntitiesAdded, report.EntitiesUpdated, report.EntitiesDeleted,
		report.RelationsAdded, report.RelationsDeleted)
	for _, e := range report.Applied {
		desc := e.Name
		if e.Kind == graphflow.EditKindRelation {
			desc = fmt.Sprintf("%s -%s-> %s", e.From, e.RelType, e.To)
		}
		line := fmt.Sprintf("  %-6s %-8s %s", e.Op, e.Kind, desc)
		if e.Reason != "" {
			line += "  — " + e.Reason
		}
		fmt.Println(line)
	}
	for _, s := range report.Skipped {
		fmt.Fprintf(os.Stderr, "  skipped: %s\n", s)
	}
	if !allowDelete && hasDeleteIntent(report) {
		fmt.Fprintln(os.Stderr, "note: deletes were proposed but not applied — re-run with --allow-delete to accept them")
	}
	if dryRun {
		fmt.Fprintln(os.Stderr, "note: dry run — nothing was written. Re-run without --dry-run to apply.")
	}
	if b, mErr := json.Marshal(report); mErr == nil && os.Getenv("CORTEXDB_JSON") != "" {
		fmt.Println(string(b))
	}
}

// hasDeleteIntent reports whether any edit was skipped because deletes were off.
func hasDeleteIntent(report *graphflow.GraphEditReport) bool {
	for _, s := range report.Skipped {
		if strings.Contains(s, "AllowDelete") {
			return true
		}
	}
	return false
}
