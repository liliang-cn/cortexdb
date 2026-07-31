package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// openLearningDB is the shared open path for the learning one-shots.
func openLearningDB() (*cortexdb.DB, string) {
	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	db, err := openBrainDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	return db, dbPath
}

// runImportLearningGraph ingests a learning-graph JSON (concepts + prerequisite
// relations) produced by an extractor — typically a Claude Code agent reading
// study material. One-shot behind `--import-learning-graph [file.json]`
// (stdin when omitted).
func runImportLearningGraph(args []string) {
	path := "-"
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		path = args[0]
	}
	var raw []byte
	var err error
	if path == "-" {
		raw, err = readAllStdin()
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: read learning graph: %v\n", err)
		os.Exit(1)
	}
	var lg graphflow.LearningGraph
	if err := json.Unmarshal(stripBOM(raw), &lg); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: parse learning graph json: %v\n", err)
		os.Exit(1)
	}

	db, dbPath := openLearningDB()
	defer func() { _ = db.Close() }()

	rep, err := graphflow.ImportLearningGraph(context.Background(), db, lg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: import learning graph: %v\n", err)
		os.Exit(1)
	}
	subject := lg.Subject
	if subject == "" {
		subject = "learning"
	}
	fmt.Printf("imported %s graph: %d concepts, %d relations into %s\n", subject, rep.Concepts, rep.Relations, dbPath)
}

// runLearnPath prints an ordered study plan for a target concept: its
// prerequisite closure, topologically sorted, minus what is already mastered.
// One-shot behind `--learn-path "<concept>"`.
func runLearnPath(args []string) {
	target := strings.TrimSpace(strings.Join(args, " "))
	if target == "" {
		fmt.Fprintln(os.Stderr, `usage: cortexdb-mcp --learn-path "<concept>"`)
		os.Exit(2)
	}
	db, _ := openLearningDB()
	defer func() { _ = db.Close() }()

	path, err := graphflow.LearningPath(context.Background(), db, target, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: learn path: %v\n", err)
		os.Exit(1)
	}
	if path.Missing {
		fmt.Printf("concept %q is not in the graph yet — import study material first (--import-learning-graph)\n", target)
		return
	}
	if len(path.Steps) == 0 {
		fmt.Printf("nothing left to study: %q and all its prerequisites are already mastered\n", path.Target)
		return
	}
	fmt.Printf("study plan for %s (%d step(s)):\n", path.Target, len(path.Steps))
	for i, s := range path.Steps {
		line := fmt.Sprintf("  %d. %s", i+1, s.Name)
		if s.Type != "" && s.Type != "concept" {
			line += " [" + s.Type + "]"
		}
		if s.Difficulty > 0 {
			line += fmt.Sprintf(" (difficulty %d)", s.Difficulty)
		}
		fmt.Println(line)
	}
	if len(path.Known) > 0 {
		fmt.Printf("already mastered (skipped): %s\n", strings.Join(path.Known, ", "))
	}
	if len(path.Cycles) > 0 {
		fmt.Fprintf(os.Stderr, "warning: prerequisite cycle among: %s\n", strings.Join(path.Cycles, ", "))
	}
}

// runLearnNext prints the learnable frontier — concepts whose prerequisites are
// all mastered. One-shot behind `--learn-next [limit]`.
func runLearnNext(args []string) {
	limit := 10
	if len(args) > 0 {
		if n := envIntFromArg(args[0]); n > 0 {
			limit = n
		}
	}
	db, _ := openLearningDB()
	defer func() { _ = db.Close() }()

	next, err := graphflow.NextConcepts(context.Background(), db, nil, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: learn next: %v\n", err)
		os.Exit(1)
	}
	if len(next) == 0 {
		fmt.Println("no concepts are ready to study (import study material, or everything is mastered)")
		return
	}
	fmt.Printf("ready to study now (%d):\n", len(next))
	for _, c := range next {
		line := "  - " + c.Name
		if c.Subject != "" {
			line += " [" + c.Subject + "]"
		}
		if c.Difficulty > 0 {
			line += fmt.Sprintf(" (difficulty %d)", c.Difficulty)
		}
		fmt.Println(line)
	}
}

// runLearnMastered marks concepts as mastered. One-shot behind
// `--learn-mastered <concept> [concept...]`.
func runLearnMastered(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cortexdb-mcp --learn-mastered <concept> [concept...]")
		os.Exit(2)
	}
	db, _ := openLearningDB()
	defer func() { _ = db.Close() }()

	marked, unknown, err := graphflow.MarkMastered(context.Background(), db, args, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: mark mastered: %v\n", err)
		os.Exit(1)
	}
	if len(marked) > 0 {
		fmt.Printf("marked mastered: %s\n", strings.Join(marked, ", "))
	}
	if len(unknown) > 0 {
		fmt.Printf("not found in the graph (check spelling): %s\n", strings.Join(unknown, ", "))
	}
}

// envIntFromArg parses a positive integer argument, returning 0 when invalid.
func envIntFromArg(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
