package main

import (
	"context"
	"fmt"
	"os"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runFactsAsOf prints the temporal facts valid at a given instant — an "as-of"
// view of the bitemporal knowledge graph. One-shot mode behind
// `--facts-as-of [RFC3339] [--from NAME] [--type TYPE]`. The timestamp defaults
// to now; --from / --type scope the query to a subject and/or predicate.
func runFactsAsOf(args []string) {
	at := time.Now().UTC()
	var filter graphflow.TemporalFilter
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			if i+1 < len(args) {
				i++
				filter.From = args[i]
			}
		case "--type":
			if i+1 < len(args) {
				i++
				filter.Type = args[i]
			}
		default:
			if t, err := time.Parse(time.RFC3339, args[i]); err == nil {
				at = t.UTC()
			} else {
				fmt.Fprintf(os.Stderr, "cortexdb: ignoring unparseable timestamp %q (want RFC3339)\n", args[i])
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

	facts, err := graphflow.QueryFactsAsOf(context.Background(), db, at, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: facts as-of: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%d fact(s) valid as of %s in %s\n", len(facts), at.Format(time.RFC3339), dbPath)
	for _, f := range facts {
		validTo := "open"
		if f.ValidTo != nil {
			validTo = f.ValidTo.Format(time.RFC3339)
		}
		validFrom := "?"
		if f.ValidFrom != nil {
			validFrom = f.ValidFrom.Format(time.RFC3339)
		}
		fmt.Printf("  %s -%s-> %s  [%s .. %s]\n", f.From, f.Type, f.To, validFrom, validTo)
	}
}
