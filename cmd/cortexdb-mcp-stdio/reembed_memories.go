package main

import (
	"context"
	"fmt"
	"os"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// runReembedMemories fills in vectors for memories saved while no embedder was
// configured. Local-only on purpose: it needs the embedder env and direct DB
// access, and on a shared brain it should run where the database lives.
func runReembedMemories(args []string) {
	opts := cortexdb.ReembedOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			opts.DryRun = true
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

	report, err := db.ReembedMemoryVectors(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("dim=%d candidates=%d reembedded=%d failed=%d dryRun=%v\n",
		report.TargetDim, report.Candidates, report.Reembedded, report.Failed, report.DryRun)
	for _, e := range report.Errors {
		fmt.Fprintf(os.Stderr, "  %s\n", e)
	}
	if report.Failed > 0 {
		os.Exit(1)
	}
}
