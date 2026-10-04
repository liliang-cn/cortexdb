// Execution graph — an agent's step-by-step record stored in CortexDB.
//
// Agentic GraphRAG (Alcaraz, ch. 7) calls the execution graph the agent's
// autobiography: one node per atomic operation (LLM call, tool invocation,
// retrieval, decision point) carrying its input, output and cost, joined by
// TRIGGERED edges that say whose output became whose input. This example
// writes one simulated DevOps-agent run that way, through the execution-graph
// API (StartRun, BeginStep/EndStep, FinishRun — also the execution_* MCP
// tools), and then reads it back the ways the book asks for:
//
//  1. two-phase step writes: BeginStep writes a step as running before it
//     executes and EndStep as done after, so a crash still leaves the causal
//     structure; the bitemporal store keeps the running version in history;
//     SummarizeRun adds up tokens, cost and the critical path;
//  2. a decision ledger entry (RecordDecision) whose premises are steps;
//  3. a structural Cypher query: tool calls that followed a low-confidence
//     LLM call and then ran slow;
//  4. lineage (StepLineage): every step upstream of the action, and a filter
//     on indexed step properties (IndexNodeProperty);
//  5. an as-of replay (ReplayRun) of the run as it stood mid-flight;
//  6. the change feed a downstream evaluator would subscribe to;
//  7. promotion of the lesson into a separate long-term brain (agentmem).
//
// The run lives in its own file (runs.db), apart from the brain, so step
// nodes never surface in the brain's recall. No model and no embedder needed:
//
//	go run ./examples/19_execution_graph
//	go run ./examples/19_execution_graph -bench   # write/read measurements
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/agentmem"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Node and edge types of the execution graph, as the API writes them; the
// bench writes the same shapes by hand to measure the store underneath. The
// node type is the Cypher label, so (l:LLMCall)-[:TRIGGERED]->(t:ToolCall)
// reads as the book draws it.
const (
	typeRun       = cortexdb.RunNodeType
	typeLLMCall   = cortexdb.StepKindLLMCall
	typeToolCall  = cortexdb.StepKindToolCall
	typeRetrieval = cortexdb.StepKindRetrieval

	edgeHasStep   = cortexdb.ExecutionEdgeHasStep
	edgeTriggered = cortexdb.ExecutionEdgeTriggered
)

func main() {
	bench := flag.Bool("bench", false, "run the write/read measurements instead of the demo")
	steps := flag.Int("steps", 2000, "bench: steps per configuration")
	payload := flag.Int("payload", 512, "bench: bytes of output stored on each step")
	flag.Parse()

	dir, err := os.MkdirTemp("", "cortexdb-exec-graph-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ctx := context.Background()
	if *bench {
		if err := runBench(ctx, dir, *steps, *payload); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := runDemo(ctx, dir); err != nil {
		log.Fatal(err)
	}
}

func runDemo(ctx context.Context, dir string) error {
	runs, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "runs.db")))
	if err != nil {
		return err
	}
	defer func() { _ = runs.Close() }()

	g := runs.Graph()
	// Steps are filtered by run, state and cost far more than by anything
	// else; index those properties so such a question is a lookup, not a
	// scan of every step ever recorded.
	for _, key := range []string{"run_id", "status", "latency_ms"} {
		if err := g.IndexNodeProperty(ctx, key); err != nil {
			return err
		}
	}
	head, err := g.ChangesHead(ctx)
	if err != nil {
		return err
	}

	run, err := runs.StartRun(ctx, cortexdb.RunStart{ID: "run:alert-4711", Task: "checkout p99 latency alert", Agent: "devops-agent"})
	if err != nil {
		return err
	}
	rec := &recorder{db: runs, runID: run.ID, runningAt: map[string]time.Time{}}

	// The workflow: plan, then two independent lookups, a diagnosis that
	// reads both, an action, and a validation of the action.
	plan, err := rec.step(ctx, cortexdb.StepKindLLMCall, "plan", nil, cortexdb.StepEnd{
		Output: "check service deps and live metrics", Confidence: conf(0.92), LatencyMS: 850, Tokens: 640, CostUSD: 0.004,
	})
	if err != nil {
		return err
	}
	deps, err := rec.step(ctx, cortexdb.StepKindRetrieval, "kg_dependencies", []string{plan}, cortexdb.StepEnd{
		Output: "checkout -> payment-gateway -> db-primary", LatencyMS: 40,
	})
	if err != nil {
		return err
	}
	metrics, err := rec.step(ctx, cortexdb.StepKindToolCall, "query_metrics", []string{plan}, cortexdb.StepEnd{
		Output: "db-primary connections 498/500", LatencyMS: 310,
	})
	if err != nil {
		return err
	}
	diagnose, err := rec.step(ctx, cortexdb.StepKindLLMCall, "diagnose", []string{deps, metrics}, cortexdb.StepEnd{
		Output: "probably connection-pool exhaustion on db-primary", Confidence: conf(0.62), LatencyMS: 1900, Tokens: 2100, CostUSD: 0.013,
	})
	if err != nil {
		return err
	}
	restart, err := rec.step(ctx, cortexdb.StepKindToolCall, "restart_pool", []string{diagnose}, cortexdb.StepEnd{
		Output: "pool recycled", LatencyMS: 4200,
	})
	if err != nil {
		return err
	}
	if _, err := rec.step(ctx, cortexdb.StepKindValidation, "verify_latency", []string{restart}, cortexdb.StepEnd{
		Output: "p99 back to 180ms", LatencyMS: 600,
	}); err != nil {
		return err
	}
	if _, err := runs.FinishRun(ctx, run.ID, cortexdb.RunEnd{Outcome: "resolved"}); err != nil {
		return err
	}
	summary, err := runs.SummarizeRun(ctx, run.ID)
	if err != nil {
		return err
	}
	fmt.Printf("recorded %s: %d steps, %d tokens, $%.3f, critical path %dms over %d steps, least confident: %s\n",
		run.ID, summary.Steps, summary.Tokens, summary.CostUSD, summary.CriticalPathMS, len(summary.CriticalPath), summary.LowestConfidence)

	// 2. Why the agent restarted the pool, on the same graph as the steps.
	decision, err := runs.RecordDecision(ctx, cortexdb.DecisionRecordRequest{
		ID:       "decision:alert-4711-restart",
		Kind:     cortexdb.DecisionKindAction,
		Actor:    "devops-agent",
		Note:     "Recycle the db-primary pool: the pool is saturated and checkout depends on it.",
		Verdict:  "restart",
		Subject:  run.ID,
		Premises: []string{deps, metrics, diagnose},
		Producer: cortexdb.ProducerLLMExtract,
		Grade:    cortexdb.GradeAsserted,
	})
	if err != nil {
		return err
	}
	chain, err := runs.DecisionChain(ctx, decision.ID, 3)
	if err != nil {
		return err
	}
	root := chain.Decisions[0]
	fmt.Printf("\ndecision %s (%s, grade %s) rests on %d premises:\n", root.ID, root.Verdict, root.Grade, len(root.Premises))
	for _, p := range root.Premises {
		fmt.Printf("  - %-28s %s\n", p.ID, p.Type)
	}

	// 3. The book's example query, verbatim in spirit.
	if err := printCypher(ctx, g, "\ntool calls after a low-confidence LLM call that then ran slow:", graph.CypherRequest{
		Query: `MATCH (l:LLMCall)-[:TRIGGERED]->(t:ToolCall)
		        WHERE l.confidence < 0.7 AND t.latency_ms > 3000
		        RETURN l.name AS llm, l.confidence AS confidence, t.name AS tool, t.latency_ms AS latency_ms`,
	}); err != nil {
		return err
	}

	// 4. Everything upstream of the action, however many hops back.
	lineage, err := runs.StepLineage(ctx, restart, cortexdb.LineageUpstream, 0)
	if err != nil {
		return err
	}
	fmt.Println("\nlineage of restart_pool:")
	for _, s := range lineage {
		fmt.Printf("  %d hop(s) back: %-10s %s\n", s.Depth, s.Kind, s.Name)
	}

	// 4b. By indexed properties: this run's steps that took over a second.
	if err := printCypher(ctx, g, "\nsteps of this run slower than 1s (indexed run_id, latency_ms):", graph.CypherRequest{
		Query: `MATCH (s) WHERE s.run_id = $run AND s.latency_ms > 1000
		        RETURN s.name AS step, s.latency_ms AS latency_ms ORDER BY latency_ms DESC`,
		Params: map[string]any{"run": run.ID},
	}); err != nil {
		return err
	}

	// 5. The run as it stood while the action was executing.
	midRun := rec.runningAt[restart]
	replay, err := runs.ReplayRun(ctx, run.ID, midRun)
	if err != nil {
		return err
	}
	fmt.Printf("\nrun state as of %s (while restart_pool ran):\n", midRun.Format("15:04:05.000"))
	for _, s := range replay.Steps {
		fmt.Printf("  %d %-16s %s\n", s.Seq, s.Name, s.Status)
	}
	versions, err := g.NodeHistory(ctx, restart)
	if err != nil {
		return err
	}
	fmt.Printf("\n%s: %d closed version(s) in history", restart, len(versions))
	for _, v := range versions {
		fmt.Printf(" [%s]", statusOf(v.Properties))
	}
	fmt.Println(", current row is done")

	// 6. What an evaluator subscribed to the feed would have received.
	events, err := g.Changes(ctx, head, 1000)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, e := range events {
		counts[e.Kind+"/"+e.Op]++
	}
	fmt.Printf("\nchange feed since the run started: %d events %v\n", len(events), counts)

	// 7. Distil the run into the long-term brain, citing the steps.
	brainDB, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "brain.db")))
	if err != nil {
		return err
	}
	defer func() { _ = brainDB.Close() }()
	brain, err := agentmem.New(brainDB)
	if err != nil {
		return err
	}
	lesson := &agentmem.Memory{
		Scope:       agentmem.Scope{Type: agentmem.ScopeAgent, ID: "devops-agent"},
		Type:        agentmem.TypePattern,
		Content:     "Checkout p99 alerts with db-primary connections near the cap resolve by recycling the db-primary pool; verify p99 afterwards.",
		Importance:  0.8,
		Confidence:  0.62,
		Tags:        []string{"checkout", "db-primary", "connection-pool"},
		EvidenceIDs: []string{run.ID, diagnose, restart, decision.ID},
	}
	if err := brain.Save(ctx, lesson); err != nil {
		return err
	}
	hits, err := brain.SearchByText(ctx, "db-primary connection pool", agentmem.SearchOptions{TopK: 3})
	if err != nil {
		return err
	}
	fmt.Printf("\nbrain recall for %q:\n", "db-primary connection pool")
	for _, h := range hits {
		fmt.Printf("  [%s] %s\n      evidence: %s\n", h.Memory.Type, h.Memory.Content, strings.Join(h.Memory.EvidenceIDs, ", "))
	}
	return nil
}

// recorder notes when each step was in flight, so the demo can replay the
// run at that instant. The writes are the execution-graph API's.
type recorder struct {
	db    *cortexdb.DB
	runID string
	// runningAt is when each step's begin write had committed: an instant
	// at which the step was in flight.
	runningAt map[string]time.Time
}

// step is the two-phase write from the book's Example 7-1: BeginStep makes
// the node and its causal edges exist before the work runs, EndStep
// replaces it with the result, and the running version stays in history.
func (r *recorder) step(ctx context.Context, kind, name string, parents []string, end cortexdb.StepEnd) (string, error) {
	st, err := r.db.BeginStep(ctx, cortexdb.StepStart{RunID: r.runID, Kind: kind, Name: name, Parents: parents})
	if err != nil {
		return "", err
	}
	r.runningAt[st.ID] = time.Now()
	time.Sleep(5 * time.Millisecond) // the step's work
	if _, err := r.db.EndStep(ctx, st.ID, end); err != nil {
		return "", err
	}
	return st.ID, nil
}

func conf(f float64) *float64 { return &f }

func printCypher(ctx context.Context, g *graph.GraphStore, title string, req graph.CypherRequest) error {
	res, err := g.QueryCypher(ctx, req)
	if err != nil {
		return fmt.Errorf("%s %w", title, err)
	}
	fmt.Println(title)
	fmt.Printf("  %s\n", strings.Join(res.Columns, " | "))
	for _, row := range res.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = fmt.Sprint(v)
		}
		fmt.Printf("  %s\n", strings.Join(cells, " | "))
	}
	return nil
}

// statusOf reads the status out of a history row's properties JSON.
func statusOf(propertiesJSON string) string {
	const key = `"status":"`
	i := strings.Index(propertiesJSON, key)
	if i < 0 {
		return "?"
	}
	rest := propertiesJSON[i+len(key):]
	if j := strings.IndexByte(rest, '"'); j >= 0 {
		return rest[:j]
	}
	return "?"
}
