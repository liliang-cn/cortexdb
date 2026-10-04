package main

// -bench: what an execution graph costs to keep in CortexDB.
//
// Each configuration writes the same workload into a fresh SQLite file:
// runs of ten steps, each step two-phase (running, then done) with a HAS_STEP
// edge and a TRIGGERED edge from the previous step, the output padded to
// -payload bytes. It reports throughput, per-step write latency, how many rows
// the store kept for it (live, history, change feed) and the file size, then
// times the reads the demo makes against the largest graph.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const benchStepsPerRun = 10

type benchConfig struct {
	name     string
	writers  int  // goroutines writing concurrently
	handles  int  // separate *cortexdb.DB opened on the file (1 = shared)
	twoPhase bool // running then done, or done only
	noFeed   bool // change feed disabled
	batched  bool // a whole run buffered and written as two batch calls
}

type benchResult struct {
	cfg        benchConfig
	steps      int
	elapsed    time.Duration
	p50, p99   time.Duration
	errors     int64
	firstErr   string
	rows       map[string]int64
	fileBytes  int64
	dbPath     string
	sampleRun  string
	sampleStep string
	sampleAt   time.Time
}

func runBench(ctx context.Context, dir string, steps, payload int) error {
	configs := []benchConfig{
		{name: "two-phase, 1 writer", writers: 1, handles: 1, twoPhase: true},
		{name: "final write only, 1 writer", writers: 1, handles: 1},
		{name: "two-phase, change feed off", writers: 1, handles: 1, twoPhase: true, noFeed: true},
		{name: "final only, batched per run", writers: 1, handles: 1, batched: true},
		{name: "final only, batched, 4 writers", writers: 4, handles: 1, batched: true},
		{name: "two-phase, 4 writers", writers: 4, handles: 1, twoPhase: true},
		{name: "two-phase, 16 writers", writers: 16, handles: 1, twoPhase: true},
		{name: "two-phase, 4 writers on 4 DB handles", writers: 4, handles: 4, twoPhase: true},
	}
	fmt.Printf("workload: %d steps per configuration, %d steps per run, %d-byte output per step\n\n", steps, benchStepsPerRun, payload)

	var results []benchResult
	for i, cfg := range configs {
		res, err := benchWrite(ctx, filepath.Join(dir, fmt.Sprintf("bench-%d.db", i)), cfg, steps, payload)
		if err != nil {
			return fmt.Errorf("%s: %w", cfg.name, err)
		}
		results = append(results, res)
	}

	fmt.Printf("%-38s %9s %9s %9s %7s %7s %8s %8s %9s %10s\n",
		"configuration", "steps/s", "p50", "p99", "errors", "nodes", "history", "feed", "file", "bytes/step")
	for _, r := range results {
		fmt.Printf("%-38s %9.0f %9s %9s %7d %7d %8d %8d %8.1fM %10.0f\n",
			r.cfg.name, float64(r.steps)/r.elapsed.Seconds(), round(r.p50), round(r.p99), r.errors,
			r.rows["graph_nodes"], r.rows["graph_node_history"], r.rows["change_log"],
			float64(r.fileBytes)/(1<<20), float64(r.fileBytes)/float64(r.steps))
		if r.firstErr != "" {
			fmt.Printf("    first error: %s\n", r.firstErr)
		}
	}
	return benchReads(ctx, results[0])
}

func benchWrite(ctx context.Context, path string, cfg benchConfig, steps, payload int) (benchResult, error) {
	var opts []cortexdb.Option
	if cfg.noFeed {
		opts = append(opts, cortexdb.WithChangeFeed(cortexdb.ChangeFeedOptions{Disabled: true}))
	}
	handles := make([]*cortexdb.DB, cfg.handles)
	for i := range handles {
		db, err := cortexdb.Open(cortexdb.DefaultConfig(path), opts...)
		if err != nil {
			return benchResult{}, err
		}
		// Create the schema before the writers race to.
		if err := db.Graph().InitGraphSchema(ctx); err != nil {
			return benchResult{}, err
		}
		handles[i] = db
	}

	res := benchResult{cfg: cfg, dbPath: path, steps: steps}
	runs := steps / benchStepsPerRun
	var next atomic.Int64
	var mu sync.Mutex
	var latencies []time.Duration
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < cfg.writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			g := handles[w%len(handles)].Graph()
			rng := rand.New(rand.NewSource(int64(w)))
			out := strings.Repeat("x", payload)
			var local []time.Duration
			for {
				run := int(next.Add(1)) - 1
				if run >= runs {
					break
				}
				var lats []time.Duration
				var err error
				if cfg.batched {
					lats, err = benchRunBatched(ctx, g, rng, run, out)
				} else {
					lats, err = benchRun(ctx, g, rng, run, cfg.twoPhase, out, &res, &mu)
				}
				local = append(local, lats...)
				if err != nil {
					if atomic.AddInt64(&res.errors, 1) == 1 {
						mu.Lock()
						res.firstErr = err.Error()
						mu.Unlock()
					}
				}
			}
			mu.Lock()
			latencies = append(latencies, local...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	res.elapsed = time.Since(start)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if n := len(latencies); n > 0 {
		res.p50, res.p99 = latencies[n/2], latencies[n*99/100]
	}
	res.rows = map[string]int64{}
	for _, table := range []string{"graph_nodes", "graph_edges", "graph_node_history", "change_log"} {
		var n int64
		if err := handles[0].SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err == nil {
			res.rows[table] = n
		}
	}
	for _, db := range handles {
		if err := db.Close(); err != nil {
			return res, err
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if fi, err := os.Stat(path + suffix); err == nil {
			res.fileBytes += fi.Size()
		}
	}
	return res, nil
}

// benchRun writes one run and returns each step's write latency.
func benchRun(ctx context.Context, g *graph.GraphStore, rng *rand.Rand, run int, twoPhase bool, out string, res *benchResult, mu *sync.Mutex) ([]time.Duration, error) {
	runID := fmt.Sprintf("run:%06d", run)
	if err := g.UpsertNode(ctx, &graph.GraphNode{
		ID: runID, NodeType: typeRun, Content: "bench run", Vector: stepVector("bench run"),
		Properties: map[string]any{"name": runID, "status": "running"},
	}); err != nil {
		return nil, err
	}
	types := []string{typeLLMCall, typeRetrieval, typeToolCall}
	var lats []time.Duration
	prev := ""
	for i := 1; i <= benchStepsPerRun; i++ {
		t0 := time.Now()
		id := fmt.Sprintf("%s/step-%02d", runID, i)
		nodeType := types[(i-1)%len(types)]
		props := map[string]any{"name": fmt.Sprintf("step-%02d", i), "run_id": runID, "seq": i, "status": "running"}
		node := &graph.GraphNode{ID: id, NodeType: nodeType, Content: nodeType, Vector: stepVector(nodeType), Properties: props}
		if twoPhase {
			if err := g.UpsertNode(ctx, node); err != nil {
				return lats, err
			}
		}
		done := func() error {
			props["status"] = "done"
			props["output"] = out
			props["latency_ms"] = 50 + rng.Intn(5000)
			if nodeType == typeLLMCall {
				props["confidence"] = float64(rng.Intn(100)) / 100
				props["tokens"] = 200 + rng.Intn(3000)
			}
			return g.UpsertNode(ctx, node)
		}
		if !twoPhase {
			if err := done(); err != nil {
				return lats, err
			}
		}
		edges := []*graph.GraphEdge{{ID: runID + "->" + id, FromNodeID: runID, ToNodeID: id, EdgeType: edgeHasStep, Weight: 1}}
		if prev != "" {
			edges = append(edges, &graph.GraphEdge{ID: prev + "->" + id, FromNodeID: prev, ToNodeID: id, EdgeType: edgeTriggered, Weight: 1})
		}
		br, err := g.UpsertEdgesBatch(ctx, edges)
		if err != nil {
			return lats, err
		}
		if br != nil && len(br.Errors) > 0 {
			return lats, br.Errors[0]
		}
		var at time.Time // an instant at which step 5 of the first run is in flight
		if run == 0 && i == 5 {
			at = time.Now()
		}
		if twoPhase {
			if err := done(); err != nil {
				return lats, err
			}
		}
		lats = append(lats, time.Since(t0))
		if !at.IsZero() {
			mu.Lock()
			res.sampleRun, res.sampleStep, res.sampleAt = runID, id, at
			mu.Unlock()
		}
		prev = id
	}
	return lats, nil
}

// benchRunBatched buffers a finished run and writes it as one node batch and
// one edge batch — the shape of an OpenTelemetry batch span processor. Each
// step's latency is its share of the run's write time.
func benchRunBatched(ctx context.Context, g *graph.GraphStore, rng *rand.Rand, run int, out string) ([]time.Duration, error) {
	t0 := time.Now()
	runID := fmt.Sprintf("run:%06d", run)
	nodes := []*graph.GraphNode{{
		ID: runID, NodeType: typeRun, Content: "bench run", Vector: stepVector("bench run"),
		Properties: map[string]any{"name": runID, "status": "done"},
	}}
	var edges []*graph.GraphEdge
	types := []string{typeLLMCall, typeRetrieval, typeToolCall}
	prev := ""
	for i := 1; i <= benchStepsPerRun; i++ {
		id := fmt.Sprintf("%s/step-%02d", runID, i)
		nodeType := types[(i-1)%len(types)]
		props := map[string]any{"name": fmt.Sprintf("step-%02d", i), "run_id": runID, "seq": i, "status": "done",
			"output": out, "latency_ms": 50 + rng.Intn(5000)}
		if nodeType == typeLLMCall {
			props["confidence"] = float64(rng.Intn(100)) / 100
			props["tokens"] = 200 + rng.Intn(3000)
		}
		nodes = append(nodes, &graph.GraphNode{ID: id, NodeType: nodeType, Content: nodeType, Vector: stepVector(nodeType), Properties: props})
		edges = append(edges, &graph.GraphEdge{ID: runID + "->" + id, FromNodeID: runID, ToNodeID: id, EdgeType: edgeHasStep, Weight: 1})
		if prev != "" {
			edges = append(edges, &graph.GraphEdge{ID: prev + "->" + id, FromNodeID: prev, ToNodeID: id, EdgeType: edgeTriggered, Weight: 1})
		}
		prev = id
	}
	for _, write := range []func() (*graph.BatchResult, error){
		func() (*graph.BatchResult, error) { return g.UpsertNodesBatch(ctx, nodes) },
		func() (*graph.BatchResult, error) { return g.UpsertEdgesBatch(ctx, edges) },
	} {
		br, err := write()
		if err == nil {
			err = br.Err()
		}
		if err != nil {
			return nil, err
		}
	}
	per := time.Since(t0) / benchStepsPerRun
	lats := make([]time.Duration, benchStepsPerRun)
	for i := range lats {
		lats[i] = per
	}
	return lats, nil
}

// benchReads times the demo's queries against a finished bench file.
func benchReads(ctx context.Context, r benchResult) error {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(r.dbPath))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	g := db.Graph()

	queries := []struct {
		name string
		req  graph.CypherRequest
	}{
		{"low-confidence LLM -> slow tool, *1..2 hops", graph.CypherRequest{
			Query:   `MATCH (l:LLMCall)-[:TRIGGERED*1..2]->(t:ToolCall) WHERE l.confidence < 0.3 AND t.latency_ms > 4500 RETURN count(t)`,
			MaxRows: 10000, Timeout: 60 * time.Second,
		}},
		{"same, *1..2 hops, seeded inside one run", graph.CypherRequest{
			Query:  `MATCH (l:LLMCall {run_id: $run})-[:TRIGGERED*1..2]->(t:ToolCall) WHERE l.confidence < 0.3 AND t.latency_ms > 4500 RETURN count(t)`,
			Params: map[string]any{"run": "run:000000"},
		}},
		{"low-confidence LLM -> slow tool, one hop", graph.CypherRequest{
			Query:   `MATCH (l:LLMCall)-[:TRIGGERED]->(t:Retrieval) WHERE l.confidence < 0.3 AND t.latency_ms > 4500 RETURN count(t)`,
			MaxRows: 10000, Timeout: 60 * time.Second,
		}},
		{"slow tool calls (label + property only)", graph.CypherRequest{
			Query:   `MATCH (t:ToolCall) WHERE t.latency_ms > 4500 RETURN count(t)`,
			MaxRows: 10000, Timeout: 60 * time.Second,
		}},
		{"lineage of one step (6 hops back)", graph.CypherRequest{
			Query:  `MATCH (s)-[:TRIGGERED*1..6]->(t) WHERE id(t) = $id RETURN count(DISTINCT s)`,
			Params: map[string]any{"id": "run:000000/step-10"},
		}},
		{"one run's steps, now", graph.CypherRequest{
			Query:  `MATCH (r:AgentRun)-[:HAS_STEP]->(s) WHERE id(r) = $run RETURN s.seq, s.status`,
			Params: map[string]any{"run": "run:000000"},
		}},
		{"one run's steps, as of mid-run", graph.CypherRequest{
			Query:  `MATCH (r:AgentRun)-[:HAS_STEP]->(s) WHERE id(r) = $run RETURN s.seq, s.status`,
			Params: map[string]any{"run": r.sampleRun}, AsOf: r.sampleAt,
		}},
	}
	fmt.Printf("\nreads against %q (%d steps):\n", r.cfg.name, r.steps)
	for _, q := range queries {
		var times []time.Duration
		var res any
		var failed error
		for i := 0; i < 5 && failed == nil; i++ {
			t0 := time.Now()
			out, err := g.QueryCypher(ctx, q.req)
			if err != nil {
				// A blown time budget is a result, not a reason to stop.
				failed = fmt.Errorf("after %s: %w", round(time.Since(t0)), err)
				break
			}
			times = append(times, time.Since(t0))
			res = out.Rows
			if len(out.Rows) > 3 {
				res = fmt.Sprintf("%d rows", len(out.Rows))
			}
		}
		if failed != nil {
			fmt.Printf("  %-46s FAILED %v\n", q.name, failed)
			continue
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		fmt.Printf("  %-46s median %9s  -> %v\n", q.name, round(times[len(times)/2]), res)
	}

	t0 := time.Now()
	events, err := g.Changes(ctx, 0, 10000)
	if err != nil {
		return err
	}
	fmt.Printf("  %-46s        %9s  -> %d events\n", "change feed, first page of 10000", round(time.Since(t0)), len(events))
	return nil
}

func round(d time.Duration) time.Duration {
	switch {
	case d > time.Second:
		return d.Round(10 * time.Millisecond)
	case d > time.Millisecond:
		return d.Round(10 * time.Microsecond)
	}
	return d.Round(time.Microsecond)
}
