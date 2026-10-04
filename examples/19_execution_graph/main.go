// Execution graph — an agent's step-by-step record stored in CortexDB.
//
// Agentic GraphRAG (Alcaraz, ch. 7) calls the execution graph the agent's
// autobiography: one node per atomic operation (LLM call, tool invocation,
// retrieval, decision point) carrying its input, output and cost, joined by
// TRIGGERED edges that say whose output became whose input. This example
// writes one simulated DevOps-agent run that way and then reads it back the
// ways the book asks for:
//
//  1. two-phase step writes: a step is upserted as "running" before it
//     executes and again as "done" after, so a crash still leaves the causal
//     structure; the bitemporal store keeps the running version in history;
//  2. a decision ledger entry (RecordDecision) whose premises are steps;
//  3. a structural Cypher query: tool calls that followed a low-confidence
//     LLM call and then ran slow;
//  4. lineage: every step upstream of the action;
//  5. an as-of replay of the run as it stood mid-flight;
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

// Node and edge types of the execution graph. The node type is the Cypher
// label, so (l:LLMCall)-[:TRIGGERED]->(t:ToolCall) reads as the book draws it.
const (
	typeRun        = "AgentRun"
	typeLLMCall    = "LLMCall"
	typeToolCall   = "ToolCall"
	typeRetrieval  = "Retrieval"
	typeValidation = "Validation"

	edgeHasStep   = "HAS_STEP"
	edgeTriggered = "TRIGGERED"
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
	head, err := g.ChangesHead(ctx)
	if err != nil {
		return err
	}

	rec := &recorder{g: g, runID: "run:alert-4711", runningAt: map[string]time.Time{}}
	if err := rec.start(ctx, "checkout p99 latency alert"); err != nil {
		return err
	}

	// The workflow: plan, then two independent lookups, a diagnosis that
	// reads both, an action, and a validation of the action.
	plan, err := rec.step(ctx, typeLLMCall, "plan", nil, stepResult{
		output: "check service deps and live metrics", confidence: 0.92, latencyMS: 850, tokens: 640, costUSD: 0.004,
	})
	if err != nil {
		return err
	}
	deps, err := rec.step(ctx, typeRetrieval, "kg_dependencies", []string{plan}, stepResult{
		output: "checkout -> payment-gateway -> db-primary", latencyMS: 40,
	})
	if err != nil {
		return err
	}
	metrics, err := rec.step(ctx, typeToolCall, "query_metrics", []string{plan}, stepResult{
		output: "db-primary connections 498/500", latencyMS: 310,
	})
	if err != nil {
		return err
	}
	diagnose, err := rec.step(ctx, typeLLMCall, "diagnose", []string{deps, metrics}, stepResult{
		output: "probably connection-pool exhaustion on db-primary", confidence: 0.62, latencyMS: 1900, tokens: 2100, costUSD: 0.013,
	})
	if err != nil {
		return err
	}
	restart, err := rec.step(ctx, typeToolCall, "restart_pool", []string{diagnose}, stepResult{
		output: "pool recycled", latencyMS: 4200,
	})
	if err != nil {
		return err
	}
	if _, err := rec.step(ctx, typeValidation, "verify_latency", []string{restart}, stepResult{
		output: "p99 back to 180ms", latencyMS: 600,
	}); err != nil {
		return err
	}
	if err := rec.finish(ctx, "resolved"); err != nil {
		return err
	}
	fmt.Printf("recorded %s: %d steps\n", rec.runID, len(rec.steps))

	// 2. Why the agent restarted the pool, on the same graph as the steps.
	decision, err := runs.RecordDecision(ctx, cortexdb.DecisionRecordRequest{
		ID:       "decision:alert-4711-restart",
		Kind:     cortexdb.DecisionKindAction,
		Actor:    "devops-agent",
		Note:     "Recycle the db-primary pool: the pool is saturated and checkout depends on it.",
		Verdict:  "restart",
		Subject:  rec.runID,
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
	if err := printCypher(ctx, g, "\nlineage of restart_pool:", graph.CypherRequest{
		Query: `MATCH (s)-[:TRIGGERED*1..6]->(t {name: 'restart_pool'})
		        RETURN DISTINCT labels(s)[0] AS type, s.name AS step ORDER BY step`,
	}); err != nil {
		return err
	}

	// 5. The run as it stood while the action was executing.
	midRun := rec.runningAt[restart]
	if err := printCypher(ctx, g, "\nrun state as of "+midRun.Format("15:04:05.000")+" (while restart_pool ran):", graph.CypherRequest{
		Query: `MATCH (r:AgentRun)-[:HAS_STEP]->(s) WHERE id(r) = $run
		        RETURN s.seq AS seq, s.name AS step, s.status AS status ORDER BY seq`,
		Params: map[string]any{"run": rec.runID},
		AsOf:   midRun,
	}); err != nil {
		return err
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
		EvidenceIDs: []string{rec.runID, diagnose, restart, decision.ID},
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

// recorder writes one run's execution graph.
type recorder struct {
	g     *graph.GraphStore
	runID string
	steps []string
	// runningAt is when each step's phase-one write had committed: an
	// instant at which the step was in flight.
	runningAt map[string]time.Time
}

type stepResult struct {
	output     string
	confidence float64
	latencyMS  int
	tokens     int
	costUSD    float64
}

func (r *recorder) start(ctx context.Context, task string) error {
	return r.g.UpsertNode(ctx, &graph.GraphNode{
		ID: r.runID, NodeType: typeRun, Content: task,
		Properties: map[string]any{"name": r.runID, "task": task, "status": "running"},
	})
}

func (r *recorder) finish(ctx context.Context, outcome string) error {
	n, err := r.g.GetNode(ctx, r.runID)
	if err != nil {
		return err
	}
	// GetNode hands back the version's ValidFrom, and a ValidFrom on a write
	// means "this was true since then": written back as is, the run would be
	// recorded as a correction — done all along, from the moment it started.
	// A state change happens now, so let the store date it.
	n.ValidFrom = time.Time{}
	n.Properties["status"] = "done"
	n.Properties["outcome"] = outcome
	n.Properties["steps"] = len(r.steps)
	return r.g.UpsertNode(ctx, n)
}

// step is the two-phase write from the book's Example 7-1. Phase one makes
// the node and its causal edges exist before the work runs; phase two
// replaces the node with its result. The store moves the running version to
// graph_node_history, which is what the as-of replay reads.
func (r *recorder) step(ctx context.Context, nodeType, name string, parents []string, res stepResult) (string, error) {
	id := fmt.Sprintf("%s/step-%02d-%s", r.runID, len(r.steps)+1, name)
	props := map[string]any{
		"name": name, "run_id": r.runID, "seq": len(r.steps) + 1, "status": "running",
		"parents": strings.Join(parents, ","),
	}
	node := &graph.GraphNode{ID: id, NodeType: nodeType, Content: name, Properties: props}
	if err := r.g.UpsertNode(ctx, node); err != nil {
		return "", err
	}
	edges := []*graph.GraphEdge{{ID: r.runID + "->" + id, FromNodeID: r.runID, ToNodeID: id, EdgeType: edgeHasStep, Weight: 1}}
	for _, p := range parents {
		edges = append(edges, &graph.GraphEdge{ID: p + "->" + id, FromNodeID: p, ToNodeID: id, EdgeType: edgeTriggered, Weight: 1})
	}
	if _, err := r.g.UpsertEdgesBatch(ctx, edges); err != nil {
		return "", err
	}
	r.steps = append(r.steps, id)
	r.runningAt[id] = time.Now()

	time.Sleep(5 * time.Millisecond) // the step's work

	props["status"] = "done"
	props["output"] = res.output
	props["latency_ms"] = res.latencyMS
	if res.confidence > 0 {
		props["confidence"] = res.confidence
	}
	if res.tokens > 0 {
		props["tokens"] = res.tokens
		props["cost_usd"] = res.costUSD
	}
	return id, r.g.UpsertNode(ctx, node)
}

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
