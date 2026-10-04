package cortexdb

// The execution graph: an agent's own record of what it did.
//
// Agentic GraphRAG (Alcaraz, ch. 7) calls it the agent's autobiography: one
// node per atomic operation — an LLM call, a tool call, a retrieval, a
// decision point, a validation — carrying what went in, what came out and
// what it cost, joined by TRIGGERED edges that say whose output became whose
// input. It is the record a later agent, an evaluator or a person reads to
// answer "what did the agent do, and why did it go wrong".
//
// It is stored as graph records rather than a side table, for the decision
// ledger's reason: a run is a node, a step is a node whose type is its kind,
// so Cypher reads (l:LLMCall)-[:TRIGGERED]->(t:ToolCall) the way the book
// draws it, and expand_graph, the live view, SPARQL over the projection,
// RecordDecision's premises and the change feed all see a run without a line
// of new code.
//
// Three properties of the store are what make it more than a log:
//
//   - Two-phase writes. BeginStep writes the step as running, with its causal
//     edges, before the work happens; EndStep rewrites it with the result. A
//     crash mid-step leaves the structure, and the running version stays in
//     the bitemporal history.
//   - Time travel. ReplayRun reads the run as it stood at any instant, from
//     that history — which steps had started, which were still running.
//   - Lineage. StepLineage walks TRIGGERED edges back to everything an
//     output depended on, or forward to everything it fed.
//
// Steps need no vector and never take part in vector search. A brain that
// keeps runs alongside knowledge can; a deployment that records many runs
// usually keeps them in a file of their own, so step nodes never show up in a
// knowledge graph's schema or statistics.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// RunNodeType is the node type of a run.
const RunNodeType = "AgentRun"

// The step kinds the book names. The vocabulary is open — a kind is any name
// that can be a Cypher label — but these are what tools and examples use.
const (
	StepKindLLMCall       = "LLMCall"
	StepKindToolCall      = "ToolCall"
	StepKindRetrieval     = "Retrieval"
	StepKindDecisionPoint = "DecisionPoint"
	StepKindValidation    = "Validation"
	// StepKindStep is the kind of a step recorded without one.
	StepKindStep = "Step"
)

// The edge types of an execution graph.
const (
	// ExecutionEdgeHasStep joins a run to each of its steps.
	ExecutionEdgeHasStep = "HAS_STEP"
	// ExecutionEdgeTriggered joins a step to a step that consumed its output.
	ExecutionEdgeTriggered = "TRIGGERED"
	// ExecutionEdgeSpawned joins a step to a run it started — a tool call
	// that hands work to a sub-agent.
	ExecutionEdgeSpawned = "SPAWNED"
)

// The statuses the API writes. A run's final status is the caller's word
// (FinishRun defaults it to done); a step ends done or failed.
const (
	ExecutionRunning = "running"
	ExecutionDone    = "done"
	ExecutionFailed  = "failed"
)

// The properties a run or step node carries. Attributes may not reuse them.
const (
	execPropRunID      = "run_id"
	execPropSeq        = "seq"
	execPropName       = "name"
	execPropStatus     = "status"
	execPropTask       = "task"
	execPropAgent      = "agent"
	execPropOutcome    = "outcome"
	execPropParents    = "parents"
	execPropParentStep = "parent_step"
	execPropInput      = "input"
	execPropOutput     = "output"
	execPropError      = "error"
	execPropConfidence = "confidence"
	execPropLatency    = "latency_ms"
	execPropTokens     = "tokens"
	execPropCost       = "cost_usd"
	execPropStartedAt  = "started_at"
	execPropEndedAt    = "ended_at"
)

var execReserved = map[string]bool{
	execPropRunID: true, execPropSeq: true, execPropName: true, execPropStatus: true, execPropTask: true,
	execPropAgent: true, execPropOutcome: true, execPropParents: true, execPropParentStep: true,
	execPropInput: true, execPropOutput: true, execPropError: true, execPropConfidence: true,
	execPropLatency: true, execPropTokens: true, execPropCost: true, execPropStartedAt: true, execPropEndedAt: true,
}

// stepKindPattern is what a kind must look like: a Cypher label.
var stepKindPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// executionRuntime is the per-DB state of the recorder: the next sequence
// number of each run this process is recording, and whether the run_id index
// exists. Sequence numbers are allocated here, so two goroutines recording
// one run in parallel get distinct ones; two processes recording one run at
// once should pass step ids of their own.
type executionRuntime struct {
	mu      sync.Mutex
	nextSeq map[string]int
	indexed bool
}

// RunStart opens a run.
type RunStart struct {
	// ID names the run. Empty mints one from the agent and the moment.
	ID string `json:"id,omitempty"`
	// Task is what the run was asked to do.
	Task string `json:"task"`
	// Agent is who runs it.
	Agent string `json:"agent,omitempty"`
	// ParentStep is the step of another run that started this one, for a
	// sub-agent. It must exist.
	ParentStep string `json:"parent_step,omitempty"`
	// Attributes are the caller's own properties.
	Attributes map[string]any `json:"attributes,omitempty"`
}

// RunEnd closes a run.
type RunEnd struct {
	// Status is the run's final status: done (the default), failed,
	// cancelled, or a word of the caller's own.
	Status string `json:"status,omitempty"`
	// Outcome is the result in words.
	Outcome    string         `json:"outcome,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Run is a run read back.
type Run struct {
	ID         string         `json:"id"`
	Task       string         `json:"task,omitempty"`
	Agent      string         `json:"agent,omitempty"`
	ParentStep string         `json:"parent_step,omitempty"`
	Status     string         `json:"status"`
	Outcome    string         `json:"outcome,omitempty"`
	StartedAt  time.Time      `json:"started_at,omitzero"`
	EndedAt    time.Time      `json:"ended_at,omitzero"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// StepStart opens a step.
type StepStart struct {
	// RunID is the run the step belongs to. It must exist.
	RunID string `json:"run_id"`
	// ID names the step. Empty gives "<run>/step-<seq>".
	ID string `json:"id,omitempty"`
	// Kind is the step's kind and node type: LLMCall, ToolCall, Retrieval,
	// DecisionPoint, Validation, or a name of the caller's own. Default Step.
	Kind string `json:"kind,omitempty"`
	// Name says which call: the model prompt's purpose, the tool's name.
	Name string `json:"name"`
	// Parents are the steps whose output this step consumes. Each must
	// exist; each becomes a TRIGGERED edge.
	Parents []string `json:"parents,omitempty"`
	// Input is what went in, as text.
	Input      string         `json:"input,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// StepEnd closes a step.
type StepEnd struct {
	// Status is done (the default) or failed.
	Status string `json:"status,omitempty"`
	Output string `json:"output,omitempty"`
	// Error is why a failed step failed.
	Error string `json:"error,omitempty"`
	// Confidence is the step's own confidence in its output, 0..1, when it
	// has one — an LLM call's, a classifier's.
	Confidence *float64 `json:"confidence,omitempty"`
	// LatencyMS is how long the step took. Zero measures it from BeginStep.
	LatencyMS int64   `json:"latency_ms,omitempty"`
	Tokens    int64   `json:"tokens,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	// Attributes are merged into the ones BeginStep wrote.
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Step is a step read back.
type Step struct {
	ID         string         `json:"id"`
	RunID      string         `json:"run_id"`
	Seq        int            `json:"seq"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	Parents    []string       `json:"parents,omitempty"`
	Input      string         `json:"input,omitempty"`
	Output     string         `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	Confidence *float64       `json:"confidence,omitempty"`
	LatencyMS  int64          `json:"latency_ms,omitempty"`
	Tokens     int64          `json:"tokens,omitempty"`
	CostUSD    float64        `json:"cost_usd,omitempty"`
	StartedAt  time.Time      `json:"started_at,omitzero"`
	EndedAt    time.Time      `json:"ended_at,omitzero"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// StartRun opens a run: an AgentRun node in status running.
func (db *DB) StartRun(ctx context.Context, req RunStart) (Run, error) {
	if db == nil {
		return Run{}, fmt.Errorf("cortexdb: start run: nil db")
	}
	task := strings.TrimSpace(req.Task)
	if task == "" {
		return Run{}, fmt.Errorf("cortexdb: start run: task is required")
	}
	if err := checkAttributes(req.Attributes); err != nil {
		return Run{}, fmt.Errorf("cortexdb: start run: %w", err)
	}
	if err := db.prepareExecution(ctx); err != nil {
		return Run{}, fmt.Errorf("cortexdb: start run: %w", err)
	}
	now := time.Now().UTC()
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = fmt.Sprintf("run:%s-%s", firstNonEmpty(slugish(req.Agent), "agent"), now.Format("20060102T150405.000000000"))
	}
	if existing, err := db.graph.GetNode(ctx, id); err == nil {
		return Run{}, fmt.Errorf("cortexdb: start run: %q already exists (a %s)", id, existing.NodeType)
	}
	parentStep := strings.TrimSpace(req.ParentStep)
	if parentStep != "" {
		if _, err := db.getStepNode(ctx, parentStep); err != nil {
			return Run{}, fmt.Errorf("cortexdb: start run: parent step: %w", err)
		}
	}
	props := copyAttributes(req.Attributes)
	props[execPropName] = id
	props[execPropTask] = task
	props[execPropStatus] = ExecutionRunning
	props[execPropStartedAt] = now.Format(time.RFC3339Nano)
	if a := strings.TrimSpace(req.Agent); a != "" {
		props[execPropAgent] = a
	}
	if parentStep != "" {
		props[execPropParentStep] = parentStep
	}
	node := &graph.GraphNode{ID: id, NodeType: RunNodeType, Content: task, Properties: props}
	if err := db.graph.UpsertNode(ctx, node); err != nil {
		return Run{}, fmt.Errorf("cortexdb: start run: %w", err)
	}
	if parentStep != "" {
		if err := db.graph.UpsertEdge(ctx, &graph.GraphEdge{ID: parentStep + "->" + id, FromNodeID: parentStep, ToNodeID: id,
			EdgeType: ExecutionEdgeSpawned, Weight: 1}); err != nil {
			return Run{}, fmt.Errorf("cortexdb: start run: link parent step: %w", err)
		}
	}
	return runFromNode(node), nil
}

// FinishRun closes a run with its final status and outcome. Steps still
// running are left as they are, and RunSummary counts them as open.
func (db *DB) FinishRun(ctx context.Context, runID string, req RunEnd) (Run, error) {
	if err := checkAttributes(req.Attributes); err != nil {
		return Run{}, fmt.Errorf("cortexdb: finish run: %w", err)
	}
	node, err := db.getRunNode(ctx, runID)
	if err != nil {
		return Run{}, fmt.Errorf("cortexdb: finish run: %w", err)
	}
	if s, _ := node.Properties[execPropStatus].(string); s != ExecutionRunning {
		return Run{}, fmt.Errorf("cortexdb: finish run: %q has already finished (%s)", runID, s)
	}
	status := firstNonEmpty(strings.TrimSpace(req.Status), ExecutionDone)
	for k, v := range req.Attributes {
		node.Properties[k] = v
	}
	node.Properties[execPropStatus] = status
	node.Properties[execPropEndedAt] = time.Now().UTC().Format(time.RFC3339Nano)
	if o := strings.TrimSpace(req.Outcome); o != "" {
		node.Properties[execPropOutcome] = o
	}
	// A state change happens now: GetNode's ValidFrom written back would
	// record the run as finished since it started.
	node.ValidFrom = time.Time{}
	if err := db.graph.UpsertNode(ctx, node); err != nil {
		return Run{}, fmt.Errorf("cortexdb: finish run: %w", err)
	}
	return runFromNode(node), nil
}

// GetRun reads one run.
func (db *DB) GetRun(ctx context.Context, runID string) (Run, error) {
	node, err := db.getRunNode(ctx, runID)
	if err != nil {
		return Run{}, fmt.Errorf("cortexdb: get run: %w", err)
	}
	return runFromNode(node), nil
}

// RunQuery filters ListRuns.
type RunQuery struct {
	Agent  string `json:"agent,omitempty"`
	Status string `json:"status,omitempty"`
	// Limit caps the list; zero is 50.
	Limit int `json:"limit,omitempty"`
}

// ListRuns lists runs, newest first.
func (db *DB) ListRuns(ctx context.Context, q RunQuery) ([]Run, error) {
	filter := &graph.GraphFilter{NodeTypes: []string{RunNodeType}, Properties: map[string]string{}}
	if a := strings.TrimSpace(q.Agent); a != "" {
		filter.Properties[execPropAgent] = a
	}
	if s := strings.TrimSpace(q.Status); s != "" {
		filter.Properties[execPropStatus] = s
	}
	nodes, err := db.graph.ListNodes(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: list runs: %w", err)
	}
	runs := make([]Run, 0, len(nodes))
	for _, n := range nodes {
		runs = append(runs, runFromNode(n))
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if !runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].StartedAt.After(runs[j].StartedAt)
		}
		return runs[i].ID < runs[j].ID
	})
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

// BeginStep is phase one of a step: the node, in status running, with its
// HAS_STEP and TRIGGERED edges, written before the work happens. It fails
// before writing anything if the run is not running, a parent does not exist,
// or the kind cannot be a label.
func (db *DB) BeginStep(ctx context.Context, req StepStart) (Step, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return Step{}, fmt.Errorf("cortexdb: begin step: name is required")
	}
	kind := firstNonEmpty(strings.TrimSpace(req.Kind), StepKindStep)
	if !stepKindPattern.MatchString(kind) || kind == RunNodeType || kind == DecisionNodeType {
		return Step{}, fmt.Errorf("cortexdb: begin step: kind %q is not a step kind (a name of letters, digits and _, not %s or %s)", kind, RunNodeType, DecisionNodeType)
	}
	if err := checkAttributes(req.Attributes); err != nil {
		return Step{}, fmt.Errorf("cortexdb: begin step: %w", err)
	}
	run, err := db.getRunNode(ctx, req.RunID)
	if err != nil {
		return Step{}, fmt.Errorf("cortexdb: begin step: %w", err)
	}
	if s, _ := run.Properties[execPropStatus].(string); s != ExecutionRunning {
		return Step{}, fmt.Errorf("cortexdb: begin step: run %q has finished (%s)", run.ID, s)
	}
	parents := make([]string, 0, len(req.Parents))
	seen := map[string]bool{}
	for _, p := range req.Parents {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if _, err := db.getStepNode(ctx, p); err != nil {
			return Step{}, fmt.Errorf("cortexdb: begin step: parent: %w", err)
		}
		parents = append(parents, p)
	}
	seq, err := db.allocateSeq(ctx, run.ID)
	if err != nil {
		return Step{}, fmt.Errorf("cortexdb: begin step: %w", err)
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = fmt.Sprintf("%s/step-%04d", run.ID, seq)
	}
	if existing, err := db.graph.GetNode(ctx, id); err == nil {
		return Step{}, fmt.Errorf("cortexdb: begin step: %q already exists (a %s)", id, existing.NodeType)
	}

	props := copyAttributes(req.Attributes)
	props[execPropRunID] = run.ID
	props[execPropSeq] = seq
	props[execPropName] = name
	props[execPropStatus] = ExecutionRunning
	props[execPropStartedAt] = time.Now().UTC().Format(time.RFC3339Nano)
	if len(parents) > 0 {
		props[execPropParents] = parents
	}
	if in := req.Input; in != "" {
		props[execPropInput] = in
	}
	node := &graph.GraphNode{ID: id, NodeType: kind, Content: name, Properties: props}
	if err := db.graph.UpsertNode(ctx, node); err != nil {
		return Step{}, fmt.Errorf("cortexdb: begin step: %w", err)
	}
	edges := []*graph.GraphEdge{{ID: run.ID + "->" + id, FromNodeID: run.ID, ToNodeID: id, EdgeType: ExecutionEdgeHasStep, Weight: 1}}
	for _, p := range parents {
		edges = append(edges, &graph.GraphEdge{ID: p + "->" + id, FromNodeID: p, ToNodeID: id, EdgeType: ExecutionEdgeTriggered, Weight: 1})
	}
	result, err := db.graph.UpsertEdgesBatch(ctx, edges)
	if err != nil {
		return Step{}, fmt.Errorf("cortexdb: begin step: link: %w", err)
	}
	if result != nil && result.FailedCount > 0 && len(result.Errors) > 0 {
		return Step{}, fmt.Errorf("cortexdb: begin step: link: %w", result.Errors[0])
	}
	return stepFromNode(node), nil
}

// EndStep is phase two: the step rewritten with its result. The running
// version moves to the node's history, where ReplayRun finds it. A step ends
// once.
func (db *DB) EndStep(ctx context.Context, stepID string, req StepEnd) (Step, error) {
	if err := checkAttributes(req.Attributes); err != nil {
		return Step{}, fmt.Errorf("cortexdb: end step: %w", err)
	}
	status := firstNonEmpty(strings.TrimSpace(req.Status), ExecutionDone)
	if status != ExecutionDone && status != ExecutionFailed {
		return Step{}, fmt.Errorf("cortexdb: end step: status %q is not done or failed", status)
	}
	if req.Confidence != nil && (*req.Confidence < 0 || *req.Confidence > 1) {
		return Step{}, fmt.Errorf("cortexdb: end step: confidence %v is outside 0..1", *req.Confidence)
	}
	node, err := db.getStepNode(ctx, stepID)
	if err != nil {
		return Step{}, fmt.Errorf("cortexdb: end step: %w", err)
	}
	if s, _ := node.Properties[execPropStatus].(string); s != ExecutionRunning {
		return Step{}, fmt.Errorf("cortexdb: end step: %q has already ended (%s)", stepID, s)
	}
	now := time.Now().UTC()
	for k, v := range req.Attributes {
		node.Properties[k] = v
	}
	node.Properties[execPropStatus] = status
	node.Properties[execPropEndedAt] = now.Format(time.RFC3339Nano)
	latency := req.LatencyMS
	if latency <= 0 {
		if started := propTime(node.Properties, execPropStartedAt); !started.IsZero() {
			latency = now.Sub(started).Milliseconds()
		}
	}
	node.Properties[execPropLatency] = latency
	if req.Output != "" {
		node.Properties[execPropOutput] = req.Output
	}
	if req.Error != "" {
		node.Properties[execPropError] = req.Error
	}
	if req.Confidence != nil {
		node.Properties[execPropConfidence] = *req.Confidence
	}
	if req.Tokens > 0 {
		node.Properties[execPropTokens] = req.Tokens
	}
	if req.CostUSD > 0 {
		node.Properties[execPropCost] = req.CostUSD
	}
	node.ValidFrom = time.Time{}
	if err := db.graph.UpsertNode(ctx, node); err != nil {
		return Step{}, fmt.Errorf("cortexdb: end step: %w", err)
	}
	return stepFromNode(node), nil
}

// RecordStep writes a step that has already happened: BeginStep and EndStep
// in one call, for an agent that logs after the fact.
func (db *DB) RecordStep(ctx context.Context, start StepStart, end StepEnd) (Step, error) {
	step, err := db.BeginStep(ctx, start)
	if err != nil {
		return Step{}, err
	}
	return db.EndStep(ctx, step.ID, end)
}

// GetStep reads one step.
func (db *DB) GetStep(ctx context.Context, stepID string) (Step, error) {
	node, err := db.getStepNode(ctx, stepID)
	if err != nil {
		return Step{}, fmt.Errorf("cortexdb: get step: %w", err)
	}
	return stepFromNode(node), nil
}

// RunSteps reads a run's steps in the order they began.
func (db *DB) RunSteps(ctx context.Context, runID string) ([]Step, error) {
	if _, err := db.getRunNode(ctx, runID); err != nil {
		return nil, fmt.Errorf("cortexdb: run steps: %w", err)
	}
	return db.runSteps(ctx, runID)
}

func (db *DB) runSteps(ctx context.Context, runID string) ([]Step, error) {
	nodes, err := db.graph.ListNodes(ctx, &graph.GraphFilter{Properties: map[string]string{execPropRunID: runID}})
	if err != nil {
		return nil, fmt.Errorf("cortexdb: run steps: %w", err)
	}
	steps := make([]Step, 0, len(nodes))
	for _, n := range nodes {
		if n.NodeType == RunNodeType || n.NodeType == DecisionNodeType {
			continue
		}
		steps = append(steps, stepFromNode(n))
	}
	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].Seq != steps[j].Seq {
			return steps[i].Seq < steps[j].Seq
		}
		return steps[i].ID < steps[j].ID
	})
	return steps, nil
}

// LineageStep is one step reached by StepLineage, with how many TRIGGERED
// hops away it is.
type LineageStep struct {
	Step
	Depth int `json:"depth"`
}

// The two directions StepLineage walks.
const (
	LineageUpstream   = "upstream"
	LineageDownstream = "downstream"
)

// StepLineage walks TRIGGERED edges from a step: upstream to every step its
// output depended on, or downstream to every step it fed, nearest first.
// maxDepth zero is 32.
func (db *DB) StepLineage(ctx context.Context, stepID, direction string, maxDepth int) ([]LineageStep, error) {
	if _, err := db.getStepNode(ctx, stepID); err != nil {
		return nil, fmt.Errorf("cortexdb: step lineage: %w", err)
	}
	dir := "in"
	switch strings.TrimSpace(direction) {
	case "", LineageUpstream:
	case LineageDownstream:
		dir = "out"
	default:
		return nil, fmt.Errorf("cortexdb: step lineage: direction %q is not upstream or downstream", direction)
	}
	if maxDepth <= 0 {
		maxDepth = 32
	}
	seen := map[string]bool{stepID: true}
	frontier := []string{stepID}
	var out []LineageStep
	for depth := 1; depth <= maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, id := range frontier {
			edges, err := db.graph.GetEdges(ctx, id, dir)
			if err != nil {
				return nil, fmt.Errorf("cortexdb: step lineage: %w", err)
			}
			for _, e := range edges {
				if e.EdgeType != ExecutionEdgeTriggered {
					continue
				}
				other := e.FromNodeID
				if dir == "out" {
					other = e.ToNodeID
				}
				if !seen[other] {
					seen[other] = true
					next = append(next, other)
				}
			}
		}
		sort.Strings(next)
		if len(next) > 0 {
			nodes, err := db.graph.GetNodesBatch(ctx, next)
			if err != nil {
				return nil, fmt.Errorf("cortexdb: step lineage: %w", err)
			}
			level := make([]LineageStep, 0, len(nodes))
			for _, n := range nodes {
				level = append(level, LineageStep{Step: stepFromNode(n), Depth: depth})
			}
			sort.SliceStable(level, func(i, j int) bool { return level[i].ID < level[j].ID })
			out = append(out, level...)
		}
		frontier = next
	}
	return out, nil
}

// RunReplay is a run as it stood at one instant.
type RunReplay struct {
	AsOf  time.Time `json:"as_of"`
	Run   Run       `json:"run"`
	Steps []Step    `json:"steps"`
}

// ReplayRun reads a run as it stood at asOf: the steps that had begun by
// then, each in the state it was in — running if it had not yet ended. It
// reads the store's bitemporal history, so it answers for any instant since
// the run started, not only for the ones somebody thought to snapshot.
func (db *DB) ReplayRun(ctx context.Context, runID string, asOf time.Time) (RunReplay, error) {
	if asOf.IsZero() {
		return RunReplay{}, fmt.Errorf("cortexdb: replay run: as_of is required")
	}
	at := graph.AsOf(ctx, asOf)
	node, err := db.graph.GetNode(at, strings.TrimSpace(runID))
	if err != nil || node.NodeType != RunNodeType {
		return RunReplay{}, fmt.Errorf("cortexdb: replay run: %q was not a run at %s", runID, asOf.UTC().Format(time.RFC3339Nano))
	}
	steps, err := db.runSteps(at, node.ID)
	if err != nil {
		return RunReplay{}, err
	}
	return RunReplay{AsOf: asOf.UTC(), Run: runFromNode(node), Steps: steps}, nil
}

// RunSummary is what a run cost and where it went wrong.
type RunSummary struct {
	Run   Run `json:"run"`
	Steps int `json:"steps"`
	// Open counts steps still running: begun and never ended. On a finished
	// run they are the steps a crash or a missing EndStep left behind.
	Open     int            `json:"open"`
	Failed   []string       `json:"failed,omitempty"`
	ByKind   map[string]int `json:"by_kind"`
	ByStatus map[string]int `json:"by_status"`
	Tokens   int64          `json:"tokens"`
	CostUSD  float64        `json:"cost_usd"`
	// LatencyMS sums every step's latency; CriticalPathMS is the slowest
	// chain of TRIGGERED steps, which is what the run took end to end when
	// independent steps ran in parallel.
	LatencyMS      int64    `json:"latency_ms"`
	CriticalPathMS int64    `json:"critical_path_ms"`
	CriticalPath   []string `json:"critical_path,omitempty"`
	// LowestConfidence is the step with the least confidence in its output,
	// the first place to look when a run's answer is wrong.
	LowestConfidence string `json:"lowest_confidence,omitempty"`
}

// SummarizeRun adds a run up.
func (db *DB) SummarizeRun(ctx context.Context, runID string) (RunSummary, error) {
	node, err := db.getRunNode(ctx, runID)
	if err != nil {
		return RunSummary{}, fmt.Errorf("cortexdb: summarize run: %w", err)
	}
	steps, err := db.runSteps(ctx, node.ID)
	if err != nil {
		return RunSummary{}, err
	}
	return summarizeSteps(runFromNode(node), steps), nil
}

func summarizeSteps(run Run, steps []Step) RunSummary {
	s := RunSummary{Run: run, Steps: len(steps), ByKind: map[string]int{}, ByStatus: map[string]int{}}
	byID := make(map[string]Step, len(steps))
	lowest := 2.0
	for _, st := range steps {
		byID[st.ID] = st
		s.ByKind[st.Kind]++
		s.ByStatus[st.Status]++
		switch st.Status {
		case ExecutionRunning:
			s.Open++
		case ExecutionFailed:
			s.Failed = append(s.Failed, st.ID)
		}
		s.Tokens += st.Tokens
		s.CostUSD += st.CostUSD
		s.LatencyMS += st.LatencyMS
		if st.Confidence != nil && *st.Confidence < lowest {
			lowest, s.LowestConfidence = *st.Confidence, st.ID
		}
	}
	// The critical path: steps are in begin order, and a step's parents
	// began before it, so one pass in that order sees every parent first.
	best := make(map[string]int64, len(steps))
	prev := make(map[string]string, len(steps))
	var end string
	for _, st := range steps {
		var base int64
		from := ""
		for _, p := range st.Parents {
			if v, ok := best[p]; ok && (from == "" || v > base) {
				base, from = v, p
			}
		}
		if from != "" {
			prev[st.ID] = from
		}
		best[st.ID] = base + st.LatencyMS
		if end == "" || best[st.ID] > best[end] {
			end = st.ID
		}
	}
	if end != "" {
		s.CriticalPathMS = best[end]
		for at := end; at != ""; at = prev[at] {
			s.CriticalPath = append([]string{at}, s.CriticalPath...)
		}
	}
	return s
}

// --- internals --------------------------------------------------------------

// prepareExecution makes sure the run_id property index exists, once per DB.
// Every read of a run's steps filters on it.
func (db *DB) prepareExecution(ctx context.Context) error {
	db.execution.mu.Lock()
	defer db.execution.mu.Unlock()
	if db.execution.indexed {
		return nil
	}
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return err
	}
	if err := db.graph.IndexNodeProperty(ctx, execPropRunID); err != nil {
		return err
	}
	db.execution.indexed = true
	return nil
}

// allocateSeq hands out the next sequence number of a run, starting after
// the highest the store already holds.
func (db *DB) allocateSeq(ctx context.Context, runID string) (int, error) {
	db.execution.mu.Lock()
	defer db.execution.mu.Unlock()
	if db.execution.nextSeq == nil {
		db.execution.nextSeq = map[string]int{}
	}
	next, ok := db.execution.nextSeq[runID]
	if !ok {
		steps, err := db.runSteps(ctx, runID)
		if err != nil {
			return 0, err
		}
		next = 1
		for _, st := range steps {
			if st.Seq >= next {
				next = st.Seq + 1
			}
		}
	}
	db.execution.nextSeq[runID] = next + 1
	return next, nil
}

func (db *DB) getRunNode(ctx context.Context, runID string) (*graph.GraphNode, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run id is required")
	}
	node, err := db.graph.GetNode(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("run %q does not exist", runID)
	}
	if node.NodeType != RunNodeType {
		return nil, fmt.Errorf("%q is a %s, not a run", runID, node.NodeType)
	}
	if node.Properties == nil {
		node.Properties = map[string]any{}
	}
	return node, nil
}

func (db *DB) getStepNode(ctx context.Context, stepID string) (*graph.GraphNode, error) {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return nil, fmt.Errorf("step id is required")
	}
	node, err := db.graph.GetNode(ctx, stepID)
	if err != nil {
		return nil, fmt.Errorf("step %q does not exist", stepID)
	}
	if _, ok := node.Properties[execPropRunID]; !ok || node.NodeType == RunNodeType {
		return nil, fmt.Errorf("%q is a %s, not a step", stepID, node.NodeType)
	}
	return node, nil
}

func checkAttributes(attrs map[string]any) error {
	for k := range attrs {
		if execReserved[k] {
			return fmt.Errorf("attribute %q is a property the execution graph writes itself", k)
		}
		if strings.HasPrefix(k, ContractPrefix) {
			return fmt.Errorf("attribute %q is a knowledge-contract key", k)
		}
	}
	return nil
}

func copyAttributes(attrs map[string]any) map[string]any {
	out := make(map[string]any, len(attrs)+12)
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

func attributesOf(props map[string]any) map[string]any {
	var out map[string]any
	for k, v := range props {
		if execReserved[k] {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		out[k] = v
	}
	return out
}

func runFromNode(n *graph.GraphNode) Run {
	p := n.Properties
	return Run{
		ID:         n.ID,
		Task:       propString(p, execPropTask),
		Agent:      propString(p, execPropAgent),
		ParentStep: propString(p, execPropParentStep),
		Status:     propString(p, execPropStatus),
		Outcome:    propString(p, execPropOutcome),
		StartedAt:  propTime(p, execPropStartedAt),
		EndedAt:    propTime(p, execPropEndedAt),
		Attributes: attributesOf(p),
	}
}

func stepFromNode(n *graph.GraphNode) Step {
	p := n.Properties
	st := Step{
		ID:         n.ID,
		RunID:      propString(p, execPropRunID),
		Seq:        int(propNumber(p, execPropSeq)),
		Kind:       n.NodeType,
		Name:       propString(p, execPropName),
		Status:     propString(p, execPropStatus),
		Input:      propString(p, execPropInput),
		Output:     propString(p, execPropOutput),
		Error:      propString(p, execPropError),
		LatencyMS:  int64(propNumber(p, execPropLatency)),
		Tokens:     int64(propNumber(p, execPropTokens)),
		CostUSD:    propNumber(p, execPropCost),
		StartedAt:  propTime(p, execPropStartedAt),
		EndedAt:    propTime(p, execPropEndedAt),
		Attributes: attributesOf(p),
	}
	if _, ok := p[execPropConfidence]; ok {
		c := propNumber(p, execPropConfidence)
		st.Confidence = &c
	}
	switch parents := p[execPropParents].(type) {
	case []string:
		st.Parents = append(st.Parents, parents...)
	case []any:
		for _, x := range parents {
			if s, ok := x.(string); ok {
				st.Parents = append(st.Parents, s)
			}
		}
	}
	return st
}

func propNumber(p map[string]any, key string) float64 {
	switch v := p[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	}
	return 0
}

func propTime(p map[string]any, key string) time.Time {
	s, _ := p[key].(string)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// slugish keeps the characters of an agent name that read well in an id.
func slugish(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.' || r == '/':
			b.WriteByte('-')
		}
	}
	return b.String()
}
