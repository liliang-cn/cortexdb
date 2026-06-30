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

	// Organize first: extract entities + co-occurrence relations from the
	// brain's memories and knowledge into the graph, so the view reflects an
	// organized brain rather than only whatever was explicitly tagged.
	if rep, oerr := graphflow.OrganizeFromBrain(ctx, db, graphflow.OrganizeOptions{
		IncludeMemories:  true,
		IncludeKnowledge: true,
	}); oerr != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: organize graph: %v\n", oerr)
	} else if rep != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: organized %d texts -> %d entities, %d relations\n",
			rep.DocumentsScanned, rep.EntityCount, rep.RelationCount)
	}

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
