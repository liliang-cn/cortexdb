package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runMultiHop answers a complex question with iterative agentic retrieval:
// retrieve → reason → retrieve, chaining evidence across hops until the LLM
// judges it sufficient (or the hop budget is spent). One-shot mode behind
// `--multi-hop "<question>"`. Requires an LLM (CORTEXDB_LLM_*). The answer is
// printed to stdout; the hop trace goes to stderr.
func runMultiHop(args []string) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: cortexdb-mcp --multi-hop \"<question>\"")
		os.Exit(2)
	}

	llm := newOrganizeLLM()
	if llm == nil {
		fmt.Fprintln(os.Stderr, "cortexdb: multi-hop search needs an LLM — set CORTEXDB_LLM_BASE_URL (e.g. http://localhost:11434) and CORTEXDB_LLM_MODEL")
		os.Exit(1)
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
	fmt.Fprintln(os.Stderr, "cortexdb: multi-hop retrieval (retrieve → reason → retrieve) …")
	result, err := graphflow.MultiHopSearch(ctx, db, query, graphflow.MultiHopOptions{LLM: llm})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: multi-hop search: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "cortexdb: answered in %d hop(s)\n", result.Hops)
	for i, step := range result.Steps {
		fmt.Fprintf(os.Stderr, "  hop %d: %q (+%d snippets)\n", i+1, step.Query, len(step.Snippets))
	}
	fmt.Println(result.Answer)
}
