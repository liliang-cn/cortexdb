package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runGraphHTML renders the brain's knowledge graph to an interactive HTML file
// and prints its absolute path on stdout. It is a one-shot mode behind the
// `--graph-html` flag, used by the /cortexdb-graph command.
//
// It reads the existing graph (entities, relations, and document/chunk nodes)
// from the active database — no LLM or embedder required — and reuses
// graphflow's Cytoscape renderer. With an empty graph it still writes a valid
// (empty) page and reports that there is nothing to show yet.
func runGraphHTML(outDir string) {
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

	if outDir == "" {
		// Default next to the database: <db dir>/graph.
		outDir = filepath.Join(filepath.Dir(dbPath), "graph")
	}

	ctx := context.Background()
	analysis, err := graphflow.Analyze(ctx, db, graphflow.AnalyzeRequest{TopN: 25})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: analyze graph: %v\n", err)
		os.Exit(1)
	}

	res, err := graphflow.ExportHTML(ctx, db, graphflow.ExportRequest{
		OutputDir: outDir,
		Analysis:  analysis,
		View:      "2d",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: export graph html: %v\n", err)
		os.Exit(1)
	}

	htmlPath := filepath.Join(outDir, "index.html")
	if res != nil && res.GraphHTML != "" {
		htmlPath = res.GraphHTML
	}
	if abs, aerr := filepath.Abs(htmlPath); aerr == nil {
		htmlPath = abs
	}
	fmt.Println(htmlPath)
}
