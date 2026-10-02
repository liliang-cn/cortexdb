// Command cortexdb-bench measures CortexDB retrieval on standard benchmarks —
// LongMemEval, LoCoMo, MuSiQue and 2WikiMultiHopQA — through the public
// pkg/cortexdb facade, and writes the numbers as JSON. Every later change to
// retrieval is judged against what it records, so a run is pinned down
// completely by its flags: the dataset files are verified against a manifest of
// SHA-256 digests, sampling is seeded, and each corpus is indexed into a fresh
// temporary database.
//
//	go run ./cmd/cortexdb-bench --fetch --dataset musique --mode lexical --out musique-lexical.json
//	go run ./cmd/cortexdb-bench --dataset longmemeval --mode hybrid --embedder embeddinggemma --limit 100
//	go run ./cmd/cortexdb-bench --merge --out baselines.json run1.json run2.json
//
// Datasets live outside the repository, under ~/.cortexdb/bench by default.
// Their content is never written to a result: results hold ids and numbers.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/eval"
)

// options are the command's flags.
type options struct {
	dataset   string
	mode      string
	embedder  string
	ollamaURL string
	limit     int
	seed      uint64
	ks        []int
	depth     int
	dataDir   string
	out       string
	fetch     bool
	perQuery  bool
	noCache   bool
	merge     bool
}

func main() {
	opts, args, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "cortexdb-bench:", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if opts.merge {
		err = runMerge(opts.out, args)
	} else {
		err = run(ctx, opts)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cortexdb-bench:", err)
		os.Exit(1)
	}
}

func parseFlags(argv []string) (options, []string, error) {
	var o options
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("cortexdb-bench", flag.ContinueOnError)
	fs.StringVar(&o.dataset, "dataset", "", "benchmark: "+strings.Join(eval.BenchNames(), " | "))
	fs.StringVar(&o.mode, "mode", modeLexical, "retrieval mode: "+strings.Join(allModes, " | "))
	fs.StringVar(&o.embedder, "embedder", "", "Ollama embedding model (required for vector and hybrid; optional for graph and ppr)")
	fs.StringVar(&o.ollamaURL, "ollama-url", "http://localhost:11434", "Ollama base URL")
	fs.IntVar(&o.limit, "limit", 0, "score at most this many questions, chosen by --seed (0 = all)")
	fs.Uint64Var(&o.seed, "seed", 1, "seed for choosing questions when --limit is set")
	ksFlag := fs.String("ks", "", "comma-separated cutoffs (default per dataset: 1,5,10 for conversations; 2,5,10 for multi-hop)")
	fs.IntVar(&o.depth, "depth", 0, "results to retrieve per query (default: the largest cutoff)")
	fs.StringVar(&o.dataDir, "data-dir", filepath.Join(home, ".cortexdb", "bench"), "where benchmark files live")
	fs.StringVar(&o.out, "out", "", "write the result JSON here (default: stdout)")
	fs.BoolVar(&o.fetch, "fetch", false, "download missing dataset files from their pinned official URLs first")
	fs.BoolVar(&o.perQuery, "per-query", false, "include every query's ranked ids in the result")
	fs.BoolVar(&o.noCache, "no-embed-cache", false, "embed every document afresh instead of using the on-disk cache")
	fs.BoolVar(&o.merge, "merge", false, "merge result files given as arguments into one JSON array at --out")
	if err := fs.Parse(argv); err != nil {
		return o, nil, err
	}
	if o.merge {
		if o.out == "" || fs.NArg() == 0 {
			return o, nil, fmt.Errorf("--merge needs --out and at least one result file")
		}
		return o, fs.Args(), nil
	}
	if o.dataset == "" {
		return o, nil, fmt.Errorf("--dataset is required (%s)", strings.Join(eval.BenchNames(), ", "))
	}
	switch o.mode {
	case modeLexical:
		if o.embedder != "" {
			return o, nil, fmt.Errorf("lexical mode runs without an embedder; drop --embedder")
		}
	case modeVector, modeHybrid:
		if o.embedder == "" {
			return o, nil, fmt.Errorf("%s mode needs --embedder", o.mode)
		}
	case modeGraph, modePPR:
	default:
		return o, nil, fmt.Errorf("unknown --mode %q (%s)", o.mode, strings.Join(allModes, ", "))
	}
	if *ksFlag != "" {
		for _, s := range strings.Split(*ksFlag, ",") {
			k, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || k <= 0 {
				return o, nil, fmt.Errorf("bad --ks value %q", s)
			}
			o.ks = append(o.ks, k)
		}
	}
	return o, nil, nil
}

// result is one run's record. It holds the run's full configuration next to
// its numbers, so a figure copied out of it can always be traced to the
// command that produced it.
type result struct {
	Benchmark       string         `json:"benchmark"`
	Protocol        string         `json:"protocol"`
	Mode            string         `json:"mode"`
	Embedder        string         `json:"embedder,omitempty"`
	EmbedDim        int            `json:"embed_dim,omitempty"`
	CortexDBVersion string         `json:"cortexdb_version"`
	Revision        string         `json:"revision,omitempty"`
	Limit           int            `json:"limit"`
	Seed            uint64         `json:"seed"`
	Depth           int            `json:"depth"`
	NumQueries      int            `json:"num_queries"`
	NumCorpora      int            `json:"num_corpora"`
	NumDocuments    int            `json:"num_documents"`
	Skipped         map[string]int `json:"skipped,omitempty"`
	// Metrics is keyed by granularity: "document" for what the benchmark
	// indexes, plus "session" for LoCoMo, whose turns roll up to sessions.
	Metrics map[string]*eval.Report `json:"metrics"`
	// ByCategory breaks the document-level score down by the benchmark's own
	// question categories. Latency and per-query detail are left out of it.
	ByCategory     map[string]*eval.Report `json:"by_category,omitempty"`
	IngestSeconds  float64                 `json:"ingest_seconds"`
	QuerySeconds   float64                 `json:"query_seconds"`
	EmbedCacheHits int                     `json:"embed_cache_hits,omitempty"`
	EmbedComputed  int                     `json:"embed_computed,omitempty"`
	Machine        string                  `json:"machine"`
	StartedAt      string                  `json:"started_at"`
	Command        string                  `json:"command"`
}

func run(ctx context.Context, o options) error {
	started := time.Now()
	if o.fetch {
		if err := fetchBench(ctx, o.dataDir, o.dataset); err != nil {
			return err
		}
	}
	logf("verifying and loading %s from %s", o.dataset, o.dataDir)
	suite, err := eval.LoadBench(o.dataDir, o.dataset)
	if err != nil {
		return err
	}
	suite.Sample(o.limit, o.seed)
	if suite.NumQueries() == 0 {
		return fmt.Errorf("%s: no questions to score", o.dataset)
	}

	ks := o.ks
	if len(ks) == 0 {
		ks = defaultKs(o.dataset)
	}
	depth := o.depth
	if depth == 0 {
		depth = defaultDepth(ks)
	}
	if depth < maxInt(ks) {
		return fmt.Errorf("--depth %d is shallower than the largest cutoff %d", depth, maxInt(ks))
	}

	var embedder cortexdb.Embedder
	var cache *cachedEmbedder
	dim := 0
	if o.embedder != "" {
		inner, err := newOllamaEmbedder(ctx, o.ollamaURL, o.embedder)
		if err != nil {
			return err
		}
		dim = inner.Dim()
		embedder = inner
		if !o.noCache {
			path := filepath.Join(o.dataDir, o.dataset, "embeddings-"+safeName(o.embedder)+".bin")
			cache, err = openCachedEmbedder(inner, path)
			if err != nil {
				return fmt.Errorf("embedding cache: %w", err)
			}
			defer cache.Close()
			embedder = cache
		}
	}

	logf("%s: %d questions over %d corpora, %d documents; mode=%s embedder=%q ks=%v depth=%d",
		suite.Name, suite.NumQueries(), len(suite.Corpora), suite.NumDocuments(), o.mode, o.embedder, ks, depth)

	docAcc := eval.NewAccumulator(suite.Name, ks...)
	var groupAcc *eval.Accumulator
	byCategory := map[string]*eval.Accumulator{}
	var ingest, querying time.Duration

	for i, corpus := range suite.Corpora {
		if err := ctx.Err(); err != nil {
			return err
		}
		groupOf := map[string]string{}
		for _, d := range corpus.Documents {
			if d.Group != "" {
				groupOf[d.ID] = d.Group
			}
		}
		if len(groupOf) > 0 && groupAcc == nil {
			groupAcc = eval.NewAccumulator(suite.Name+"/session", ks...)
		}

		db, cleanup, err := openTempDB(embedder)
		if err != nil {
			return err
		}
		t0 := time.Now()
		if cache != nil {
			contents := make([]string, len(corpus.Documents))
			for j, d := range corpus.Documents {
				contents[j] = d.Content
			}
			if err := cache.Warm(ctx, contents); err != nil {
				cleanup()
				return fmt.Errorf("%s: warm embeddings: %w", corpus.ID, err)
			}
		}
		for _, d := range corpus.Documents {
			if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
				KnowledgeID: d.ID,
				Title:       d.Title,
				Content:     d.Content,
			}); err != nil {
				cleanup()
				return fmt.Errorf("%s: save %s: %w", corpus.ID, d.ID, err)
			}
		}
		ingest += time.Since(t0)

		for _, q := range corpus.Queries {
			t := time.Now()
			ids, err := retrieve(ctx, db, o.mode, q.Text, depth)
			lat := time.Since(t)
			if err != nil {
				cleanup()
				return fmt.Errorf("%s: query %s: %w", corpus.ID, q.ID, err)
			}
			querying += lat
			docAcc.Add(q.ID, ids, q.Relevant, lat)
			if q.Category != "" {
				if byCategory[q.Category] == nil {
					byCategory[q.Category] = eval.NewAccumulator(q.Category, ks...)
				}
				byCategory[q.Category].Add(q.ID, ids, q.Relevant, -1)
			}
			if groupAcc != nil {
				group := func(id string) string { return groupOf[id] }
				groupAcc.Add(q.ID, eval.CollapseIDs(ids, group), eval.CollapseIDs(q.Relevant, group), lat)
			}
		}
		cleanup()
		if len(suite.Corpora) > 1 && ((i+1)%10 == 0 || i+1 == len(suite.Corpora)) {
			logf("  %d/%d corpora; ingest %s, query %s", i+1, len(suite.Corpora), ingest.Round(time.Second), querying.Round(time.Millisecond))
		}
	}

	res := result{
		Benchmark:       suite.Name,
		Protocol:        suite.Protocol,
		Mode:            o.mode,
		Embedder:        o.embedder,
		EmbedDim:        dim,
		CortexDBVersion: cortexdbroot.Version,
		Revision:        vcsRevision(),
		Limit:           o.limit,
		Seed:            o.seed,
		Depth:           depth,
		NumQueries:      suite.NumQueries(),
		NumCorpora:      len(suite.Corpora),
		NumDocuments:    suite.NumDocuments(),
		Skipped:         suite.Skipped,
		Metrics:         map[string]*eval.Report{"document": docAcc.Report()},
		ByCategory:      map[string]*eval.Report{},
		IngestSeconds:   ingest.Seconds(),
		QuerySeconds:    querying.Seconds(),
		Machine:         runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(),
		StartedAt:       started.UTC().Format(time.RFC3339),
		Command:         commandLine(os.Args[1:]),
	}
	if groupAcc != nil {
		res.Metrics["session"] = groupAcc.Report()
	}
	for name, acc := range byCategory {
		rep := acc.Report()
		rep.PerQuery, rep.Latency = nil, nil
		res.ByCategory[name] = rep
	}
	if !o.perQuery {
		for _, rep := range res.Metrics {
			rep.PerQuery = nil
		}
	}
	if cache != nil {
		res.EmbedCacheHits, res.EmbedComputed = cache.Stats()
	}
	for _, g := range sortedKeys(res.Metrics) {
		fmt.Fprintf(os.Stderr, "\n[%s] %s", g, res.Metrics[g].Summary())
	}
	fmt.Fprintf(os.Stderr, "ingest %.1fs, query %.1fs\n", res.IngestSeconds, res.QuerySeconds)
	return writeJSON(o.out, res)
}

// commandLine reconstructs the invocation without its --out and --data-dir
// values: where one machine wrote a file says nothing about the run, and the
// recorded command should paste unchanged on another.
func commandLine(args []string) string {
	var kept []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := strings.TrimLeft(a, "-")
		if name == "out" || name == "data-dir" {
			i++
			continue
		}
		if strings.HasPrefix(name, "out=") || strings.HasPrefix(name, "data-dir=") {
			continue
		}
		kept = append(kept, a)
	}
	return strings.TrimSpace("go run ./cmd/cortexdb-bench " + strings.Join(kept, " "))
}

// defaultKs are the cutoffs each benchmark's own papers report.
func defaultKs(dataset string) []int {
	switch dataset {
	case "musique", "2wikimultihopqa":
		return []int{2, 5, 10}
	}
	return []int{1, 5, 10}
}

// defaultDepth is how many results each query retrieves: the largest cutoff.
//
// LoCoMo's session-level score ranks the sessions of those turns, so ten turns
// from four sessions score session recall@10 on a list of four. Retrieving
// deeper would fill the list, but the facade's MMR rerank costs roughly the
// cube of the depth at v2.114.1 — about 10 ms a query at depth 10, 55 ms at 20
// and 800 ms at 50 on LoCoMo — and session numbers that only a 20-minute run
// can produce would not get rerun. --depth raises it when they are wanted.
func defaultDepth(ks []int) int {
	return maxInt(ks)
}

func maxInt(xs []int) int {
	m := 0
	for _, x := range xs {
		m = max(m, x)
	}
	return m
}

// openTempDB opens a fresh database in a new temporary directory. Every corpus
// gets its own, so nothing indexed for one question can answer another.
func openTempDB(embedder cortexdb.Embedder) (*cortexdb.DB, func(), error) {
	dir, err := os.MkdirTemp("", "cortexdb-bench-*")
	if err != nil {
		return nil, nil, err
	}
	var opts []cortexdb.Option
	if embedder != nil {
		opts = append(opts, cortexdb.WithEmbedder(embedder))
	}
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "bench.db")), opts...)
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	return db, func() {
		_ = db.Close()
		_ = os.RemoveAll(dir)
	}, nil
}

// vcsRevision is the commit the binary was built from, when the toolchain
// stamped one (go build in a clean checkout does; go run does not).
func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev != "" && dirty {
		rev += "-dirty"
	}
	return rev
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', ':', '\\', ' ':
			return '_'
		}
		return r
	}, s)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path == "" {
		_, err = os.Stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// runMerge collects result files into one JSON array, ordered by benchmark and
// then mode, dropping per-query detail: the merged file is the recorded
// baseline, and it holds numbers only.
func runMerge(out string, files []string) error {
	var all []map[string]any
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var r map[string]any
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		if metrics, ok := r["metrics"].(map[string]any); ok {
			for _, m := range metrics {
				if mm, ok := m.(map[string]any); ok {
					delete(mm, "per_query")
				}
			}
		}
		all = append(all, r)
	}
	modeOrder := map[string]int{}
	for i, m := range allModes {
		modeOrder[m] = i
	}
	str := func(r map[string]any, k string) string { s, _ := r[k].(string); return s }
	num := func(r map[string]any, k string) float64 { n, _ := r[k].(float64); return n }
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if str(a, "benchmark") != str(b, "benchmark") {
			return str(a, "benchmark") < str(b, "benchmark")
		}
		if num(a, "limit") != num(b, "limit") {
			return num(a, "limit") < num(b, "limit")
		}
		return modeOrder[str(a, "mode")] < modeOrder[str(b, "mode")]
	})
	return writeJSON(out, all)
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05")}, args...)...)
}
