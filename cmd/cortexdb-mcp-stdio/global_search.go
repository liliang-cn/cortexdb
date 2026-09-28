package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runGlobalSearch answers a whole-corpus question with GraphRAG-style global
// search: detect the entity community hierarchy, write a report per community
// (once, then reused), and map-reduce over the reports of one level. One-shot
// mode behind `--global-search [--level N] "<question>"`. With CORTEXDB_LLM_*
// set the reports and the answer are the model's; without it the reports are
// assembled deterministically and the most relevant are printed unsynthesised.
func runGlobalSearch(args []string) {
	var level *int
	var words []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--level" && i+1 < len(args) {
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "cortexdb: --level wants an integer, got %q\n", args[i+1])
				os.Exit(2)
			}
			level = &n
			i++
			continue
		}
		words = append(words, args[i])
	}
	query := strings.TrimSpace(strings.Join(words, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: cortexdb-mcp --global-search [--level N] \"<question>\"")
		os.Exit(2)
	}

	llm := newOrganizeLLM()
	if llm == nil {
		fmt.Fprintln(os.Stderr, "cortexdb: no LLM configured (CORTEXDB_LLM_BASE_URL / CORTEXDB_LLM_MODEL) — reports are deterministic and the answer is not synthesised")
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
		Level:        level,
		BuildIfEmpty: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: global search: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "cortexdb: answered from %d communities at level %d (levels with reports: %v, mode %s)\n",
		result.CommunitiesUsed, result.Level, result.LevelsAvailable, result.Mode)
	fmt.Println(result.Answer)
	if result.Mode == "no_model" {
		for _, p := range result.SupportingPoints {
			fmt.Println("- " + p)
		}
	}
}
