package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// recallEvalCase is one line of a golden file: a query somebody would really
// ask, and the memory ids a correct recall would surface.
type recallEvalCase struct {
	ID          string   `json:"id"`
	Category    string   `json:"category,omitempty"`
	Query       string   `json:"query"`
	EntityNames []string `json:"entity_names,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	// Expected are memory ids that count as a hit. Empty marks a negative case:
	// a query the store holds nothing about, reported but not scored.
	Expected []string `json:"expected,omitempty"`
	// Forbidden are ids that must NOT appear in the top K — stale facts a
	// correct ranking keeps out.
	Forbidden []string `json:"forbidden,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// runRecallEval measures recall quality against a golden set, so a change to
// ranking, extraction or retrieval is judged by the same yardstick as the one
// before it.
//
// `--recall-eval <golden.jsonl> [--top-k N] [--out results.json]`. Evaluates
// the shared brain when CORTEXDB_REMOTE is set, the local database otherwise —
// the same resolution every other one-shot mode uses.
func runRecallEval(args []string) {
	goldenPath := ""
	topK := 5
	outPath := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--top-k":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &topK)
			}
		case "--out":
			if i+1 < len(args) {
				i++
				outPath = args[i]
			}
		default:
			if !strings.HasPrefix(args[i], "-") && goldenPath == "" {
				goldenPath = args[i]
			}
		}
	}
	if goldenPath == "" {
		fmt.Fprintln(os.Stderr, "usage: cortexdb-mcp-stdio --recall-eval <golden.jsonl> [--top-k N] [--out results.json]")
		os.Exit(2)
	}

	cases, err := loadRecallEvalCases(goldenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
		os.Exit(1)
	}
	if len(cases) == 0 {
		fmt.Fprintln(os.Stderr, "cortexdb: golden file holds no cases")
		os.Exit(1)
	}

	ctx := context.Background()
	search, source, closeFn := recallEvalSearcher(ctx)
	defer closeFn()
	fmt.Printf("evaluating %d cases against %s (top-%d)\n\n", len(cases), source, topK)

	type caseResult struct {
		Case      recallEvalCase `json:"case"`
		Returned  []string       `json:"returned"`
		Scores    []float64      `json:"scores"`
		Recall    float64        `json:"recall_at_k"`
		FirstRank int            `json:"first_rank"` // 1-based rank of first expected hit; 0 = miss
		Forbidden []string       `json:"forbidden_hits,omitempty"`
		Err       string         `json:"error,omitempty"`
	}
	results := make([]caseResult, 0, len(cases))

	for _, c := range cases {
		res := caseResult{Case: c}
		hits, err := search(ctx, c, topK)
		if err != nil {
			res.Err = err.Error()
			results = append(results, res)
			fmt.Printf("  [%-12s] %-28s ERROR: %v\n", c.Category, c.ID, err)
			continue
		}
		for _, h := range hits {
			res.Returned = append(res.Returned, h.Memory.ID)
			res.Scores = append(res.Scores, h.Score)
		}

		if len(c.Expected) > 0 {
			found := 0
			for _, want := range c.Expected {
				for rank, got := range res.Returned {
					if got == want {
						found++
						if res.FirstRank == 0 || rank+1 < res.FirstRank {
							res.FirstRank = rank + 1
						}
						break
					}
				}
			}
			res.Recall = float64(found) / float64(len(c.Expected))
		}
		for _, bad := range c.Forbidden {
			for _, got := range res.Returned {
				if got == bad {
					res.Forbidden = append(res.Forbidden, bad)
				}
			}
		}
		results = append(results, res)

		status := "MISS"
		switch {
		case len(c.Expected) == 0:
			status = fmt.Sprintf("neg: %d returned", len(res.Returned))
		case res.FirstRank == 1:
			status = "HIT@1"
		case res.FirstRank > 0:
			status = fmt.Sprintf("HIT@%d", res.FirstRank)
		}
		if len(res.Forbidden) > 0 {
			status += " FORBIDDEN:" + strings.Join(res.Forbidden, ",")
		}
		fmt.Printf("  [%-12s] %-28s %s\n", c.Category, c.ID, status)
	}

	// Aggregate, overall and per category — the categories map to the stages of
	// the improvement plan, so each stage reads its own number.
	type bucket struct {
		n, hits, misses, errs int
		recallSum, mrrSum     float64
		forbidden             int
		spreadSum             float64
		spreadN               int
	}
	buckets := map[string]*bucket{}
	get := func(name string) *bucket {
		if buckets[name] == nil {
			buckets[name] = &bucket{}
		}
		return buckets[name]
	}
	for _, r := range results {
		for _, name := range []string{"TOTAL", firstNonEmptyStr(r.Case.Category, "uncategorised")} {
			b := get(name)
			if r.Err != "" {
				b.errs++
				continue
			}
			if len(r.Scores) > 1 {
				b.spreadSum += r.Scores[0] - r.Scores[len(r.Scores)-1]
				b.spreadN++
			}
			b.forbidden += len(r.Forbidden)
			if len(r.Case.Expected) == 0 {
				continue // negative cases: reported, not scored
			}
			b.n++
			b.recallSum += r.Recall
			if r.FirstRank > 0 {
				b.hits++
				b.mrrSum += 1 / float64(r.FirstRank)
			} else {
				b.misses++
			}
		}
	}

	names := make([]string, 0, len(buckets))
	for name := range buckets {
		if name != "TOTAL" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	names = append(names, "TOTAL")

	fmt.Printf("\n%-14s %6s %9s %7s %7s %10s %8s\n", "category", "cases", "recall@k", "MRR", "hit@k", "forbidden", "spread")
	for _, name := range names {
		b := buckets[name]
		if b.n == 0 && b.forbidden == 0 && b.errs == 0 && name != "TOTAL" {
			continue
		}
		recall, mrr, hitRate := 0.0, 0.0, 0.0
		if b.n > 0 {
			recall = b.recallSum / float64(b.n)
			mrr = b.mrrSum / float64(b.n)
			hitRate = float64(b.hits) / float64(b.n)
		}
		spread := 0.0
		if b.spreadN > 0 {
			spread = b.spreadSum / float64(b.spreadN)
		}
		fmt.Printf("%-14s %6d %9.3f %7.3f %7.3f %10d %8.4f\n", name, b.n, recall, mrr, hitRate, b.forbidden, spread)
	}

	if outPath != "" {
		payload := map[string]any{"source": source, "top_k": topK, "results": results}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err == nil {
			err = os.WriteFile(outPath, data, 0o644)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: write %s: %v\n", outPath, err)
			os.Exit(1)
		}
		fmt.Printf("\nwrote %s\n", outPath)
	}
}

func loadRecallEvalCases(path string) ([]recallEvalCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open golden file: %w", err)
	}
	defer f.Close()
	var cases []recallEvalCase
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "//") || strings.HasPrefix(text, "#") {
			continue
		}
		var c recallEvalCase
		if err := json.Unmarshal([]byte(text), &c); err != nil {
			return nil, fmt.Errorf("golden line %d: %w", line, err)
		}
		if strings.TrimSpace(c.Query) == "" {
			return nil, fmt.Errorf("golden line %d: empty query", line)
		}
		if c.ID == "" {
			c.ID = fmt.Sprintf("case-%d", line)
		}
		cases = append(cases, c)
	}
	return cases, scanner.Err()
}

type recallSearchFn func(ctx context.Context, c recallEvalCase, topK int) ([]cortexdb.MemorySearchHit, error)

// recallEvalSearcher returns a memory_search runner for whichever brain this
// process is pointed at. The eval goes through memory_search rather than the
// fused recall because that is the path the ranking and retrieval stages
// change; the fused pack wraps it.
func recallEvalSearcher(ctx context.Context) (recallSearchFn, string, func()) {
	if addr, token, ok := remoteConfigured(); ok {
		conn, err := dialCortexDB(addr, token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: connect to %s: %v\n", addr, err)
			os.Exit(1)
		}
		client := rpcv1.NewToolsServiceClient(conn)
		search := func(ctx context.Context, c recallEvalCase, topK int) ([]cortexdb.MemorySearchHit, error) {
			args, err := json.Marshal(cortexdb.MemorySearchRequest{
				Query:       c.Query,
				Scope:       "global",
				TopK:        topK,
				Keywords:    c.Keywords,
				EntityNames: c.EntityNames,
			})
			if err != nil {
				return nil, err
			}
			callCtx, cancel := context.WithTimeout(ctx, remoteDialTimeout)
			defer cancel()
			resp, err := client.CallTool(callCtx, &rpcv1.CallToolRequest{Name: "memory_search", ArgsJson: string(args)})
			if err != nil {
				return nil, err
			}
			var out cortexdb.MemorySearchResponse
			if err := json.Unmarshal([]byte(resp.GetResultJson()), &out); err != nil {
				return nil, err
			}
			return out.Results, nil
		}
		return search, "shared brain " + addr, func() { _ = conn.Close() }
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
	search := func(ctx context.Context, c recallEvalCase, topK int) ([]cortexdb.MemorySearchHit, error) {
		resp, err := db.SearchMemory(ctx, cortexdb.MemorySearchRequest{
			Query:       c.Query,
			Scope:       "global",
			TopK:        topK,
			Keywords:    c.Keywords,
			EntityNames: c.EntityNames,
		})
		if err != nil {
			return nil, err
		}
		return resp.Results, nil
	}
	return search, dbPath, func() { _ = db.Close() }
}
