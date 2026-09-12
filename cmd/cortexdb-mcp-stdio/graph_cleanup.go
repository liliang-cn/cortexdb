package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// runGraphCleanup prunes junk entity nodes and backfills memory graph presence.
// Local-only like --reembed-memories: it needs direct DB access and should run
// on the machine where the database lives, beside the running service.
//
// `--graph-cleanup [--dry-run] [--prune-only|--reindex-only|--dangling-only] [--limit N]`
func runGraphCleanup(args []string) {
	opts := cortexdb.GraphMaintenanceOptions{}
	pruneOnly, reindexOnly, danglingOnly := false, false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			opts.DryRun = true
		case "--prune-only":
			pruneOnly = true
		case "--reindex-only":
			reindexOnly = true
		case "--dangling-only":
			// Only the memory:<id> nodes whose row is gone. Separate from the
			// junk-entity prune so a brain can be repaired after a bulk memory
			// delete without also deciding what to do about its entities.
			danglingOnly = true
		case "--limit":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &opts.Limit)
			}
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
	ctx := context.Background()

	if !reindexOnly && !danglingOnly {
		report, err := db.PruneJunkEntities(ctx, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: prune: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("prune: scanned=%d pruned=%d edges_removed=%d dryRun=%v\n",
			report.Scanned, report.Pruned, report.EdgesRemoved, report.DryRun)
		if len(report.Names) > 0 {
			// Named, always: a deletion approved on a count alone is not approved.
			fmt.Printf("  %s\n", strings.Join(report.Names, ", "))
		}
	}
	if !reindexOnly {
		report, err := db.PruneDanglingMemoryNodes(ctx, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: prune dangling: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("dangling: scanned=%d pruned=%d edges_removed=%d dryRun=%v\n",
			report.Scanned, report.Pruned, report.EdgesRemoved, report.DryRun)
		if n := len(report.Names); n > 0 {
			shown := report.Names
			if n > 20 {
				shown = append(append([]string{}, report.Names[:20]...), fmt.Sprintf("… and %d more", n-20))
			}
			fmt.Printf("  %s\n", strings.Join(shown, ", "))
		}
	}
	if !pruneOnly && !danglingOnly {
		report, err := db.ReindexMemoryGraph(ctx, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: reindex: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("reindex: memories=%d indexed=%d skipped=%d entities=%d dryRun=%v\n",
			report.Memories, report.Indexed, report.Skipped, report.EntitiesSeen, report.DryRun)
	}
}
