package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runGlobalSearch answers a whole-corpus question with GraphRAG-style global
// search: detect entity communities, write an LLM report per community (once,
// then reused), and map-reduce over those reports. One-shot mode behind
// `--global-search "<question>"`. Requires an LLM (CORTEXDB_LLM_*).
func runGlobalSearch(args []string) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: cortexdb-mcp --global-search \"<question>\"")
		os.Exit(2)
	}

	llm := newOrganizeLLM()
	if llm == nil {
		fmt.Fprintln(os.Stderr, "cortexdb: global search needs an LLM — set CORTEXDB_LLM_BASE_URL (e.g. http://localhost:11434) and CORTEXDB_LLM_MODEL")
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
	fmt.Fprintln(os.Stderr, "cortexdb: building community summaries (first run) / reusing existing …")
	result, err := graphflow.GlobalSearch(ctx, db, query, graphflow.GlobalSearchOptions{
		LLM:          llm,
		BuildIfEmpty: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: global search: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "cortexdb: answered from %d communities\n", result.CommunitiesUsed)
	fmt.Println(result.Answer)
}
