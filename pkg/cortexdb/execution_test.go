package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The execution graph on both backends: the two-phase write, lineage, the
// summary, replay from history, and the refusals that keep a run's record
// from saying something that did not happen.

func ptr(f float64) *float64 { return &f }

// recordDevOpsRun writes the book's example run: a plan, two lookups that
// read it, a diagnosis that reads both, an action, and a check of the action.
func recordDevOpsRun(t *testing.T, db *DB) (Run, map[string]Step) {
	t.Helper()
	ctx := context.Background()
	run, err := db.StartRun(ctx, RunStart{ID: "run:alert-4711", Task: "checkout p99 latency alert", Agent: "devops-agent",
		Attributes: map[string]any{"ticket": "INC-4711"}})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	steps := map[string]Step{}
	rec := func(kind, name string, parents []string, end StepEnd) Step {
		t.Helper()
		var ids []string
		for _, p := range parents {
			ids = append(ids, steps[p].ID)
		}
		st, err := db.RecordStep(ctx, StepStart{RunID: run.ID, Kind: kind, Name: name, Parents: ids, Input: "in:" + name}, end)
		if err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
		steps[name] = st
		return st
	}
	rec(StepKindLLMCall, "plan", nil, StepEnd{Output: "check deps and metrics", Confidence: ptr(0.92), LatencyMS: 850, Tokens: 640, CostUSD: 0.004})
	rec(StepKindRetrieval, "deps", []string{"plan"}, StepEnd{Output: "checkout -> db-primary", LatencyMS: 40})
	rec(StepKindToolCall, "metrics", []string{"plan"}, StepEnd{Output: "498/500 connections", LatencyMS: 310})
	rec(StepKindLLMCall, "diagnose", []string{"deps", "metrics"}, StepEnd{Output: "pool exhaustion", Confidence: ptr(0.62), LatencyMS: 1900, Tokens: 2100, CostUSD: 0.013})
	rec(StepKindToolCall, "restart", []string{"diagnose"}, StepEnd{Output: "pool recycled", LatencyMS: 4200})
	rec(StepKindValidation, "verify", []string{"restart"}, StepEnd{Status: ExecutionFailed, Error: "p99 still 900ms", LatencyMS: 600})
	return run, steps
}

func TestExecutionGraphRecordsAndSummarizesARun(t *testing.T) {
	for _, b := range decisionBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			db := b.open(t)
			ctx := context.Background()
			run, steps := recordDevOpsRun(t, db)

			got, err := db.RunSteps(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for i, st := range got {
				names = append(names, st.Name)
				if st.Seq != i+1 || st.RunID != run.ID || st.StartedAt.IsZero() || st.EndedAt.IsZero() {
					t.Errorf("step %d: %+v", i, st)
				}
			}
			if strings.Join(names, ",") != "plan,deps,metrics,diagnose,restart,verify" {
				t.Fatalf("steps in order: %v", names)
			}
			if d := steps["diagnose"]; d.Kind != StepKindLLMCall || len(d.Parents) != 2 || d.Confidence == nil || *d.Confidence != 0.62 || d.Input != "in:diagnose" {
				t.Fatalf("diagnose read back: %+v", d)
			}

			sum, err := db.SummarizeRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if sum.Steps != 6 || sum.Open != 0 || sum.ByKind[StepKindLLMCall] != 2 || sum.ByStatus[ExecutionFailed] != 1 ||
				len(sum.Failed) != 1 || sum.Failed[0] != steps["verify"].ID {
				t.Fatalf("summary counts: %+v", sum)
			}
			if sum.Tokens != 2740 || sum.LatencyMS != 7900 || fmt.Sprintf("%.3f", sum.CostUSD) != "0.017" {
				t.Fatalf("summary totals: tokens %d latency %d cost %v", sum.Tokens, sum.LatencyMS, sum.CostUSD)
			}
			// plan 850 → metrics 310 (slower than deps) → diagnose 1900 → restart 4200 → verify 600
			if sum.CriticalPathMS != 7860 || len(sum.CriticalPath) != 5 || sum.CriticalPath[1] != steps["metrics"].ID {
				t.Fatalf("critical path %d %v", sum.CriticalPathMS, sum.CriticalPath)
			}
			if sum.LowestConfidence != steps["diagnose"].ID {
				t.Fatalf("lowest confidence: %s", sum.LowestConfidence)
			}

			finished, err := db.FinishRun(ctx, run.ID, RunEnd{Status: ExecutionFailed, Outcome: "latency persists"})
			if err != nil || finished.Status != ExecutionFailed || finished.EndedAt.IsZero() || finished.Attributes["ticket"] != "INC-4711" {
				t.Fatalf("finish: %+v %v", finished, err)
			}
			if _, err := db.FinishRun(ctx, run.ID, RunEnd{}); err == nil {
				t.Error("a run finished twice")
			}
			if _, err := db.BeginStep(ctx, StepStart{RunID: run.ID, Name: "late"}); err == nil {
				t.Error("a step began on a finished run")
			}
			runs, err := db.ListRuns(ctx, RunQuery{Agent: "devops-agent"})
			if err != nil || len(runs) != 1 || runs[0].Outcome != "latency persists" {
				t.Fatalf("list runs: %+v %v", runs, err)
			}
			if runs, _ := db.ListRuns(ctx, RunQuery{Status: ExecutionRunning}); len(runs) != 0 {
				t.Fatalf("running runs: %+v", runs)
			}

			// The steps are ordinary graph records, so Cypher reads them.
			res, err := db.Graph().QueryCypher(ctx, graph.CypherRequest{
				Query: `MATCH (l:LLMCall)-[:TRIGGERED]->(t:ToolCall) WHERE l.confidence < 0.7 RETURN t.name AS tool`,
			})
			if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != "restart" {
				t.Fatalf("cypher over the run: %+v %v", res, err)
			}
		})
	}
}

func TestExecutionLineage(t *testing.T) {
	db := decisionBackends(t)[0].open(t)
	ctx := context.Background()
	_, steps := recordDevOpsRun(t, db)
	up, err := db.StepLineage(ctx, steps["restart"].ID, LineageUpstream, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, s := range up {
		lines = append(lines, fmt.Sprintf("%d:%s", s.Depth, s.Name))
	}
	if strings.Join(lines, " ") != "1:diagnose 2:deps 2:metrics 3:plan" {
		t.Fatalf("upstream of restart: %v", lines)
	}
	down, err := db.StepLineage(ctx, steps["deps"].ID, LineageDownstream, 2)
	if err != nil || len(down) != 2 || down[0].Name != "diagnose" || down[1].Name != "restart" {
		t.Fatalf("downstream of deps, two hops: %+v %v", down, err)
	}
	if _, err := db.StepLineage(ctx, steps["deps"].ID, "sideways", 0); err == nil {
		t.Error("an unknown direction was accepted")
	}
}

func TestExecutionReplayReadsTheRunAsItStood(t *testing.T) {
	for _, b := range decisionBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			db := b.open(t)
			ctx := context.Background()
			run, err := db.StartRun(ctx, RunStart{Task: "deploy", Agent: "Release Bot"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(run.ID, "run:release-bot-") {
				t.Errorf("minted id %q", run.ID)
			}
			first, err := db.RecordStep(ctx, StepStart{RunID: run.ID, Kind: StepKindToolCall, Name: "build"}, StepEnd{LatencyMS: 10})
			if err != nil {
				t.Fatal(err)
			}
			second, err := db.BeginStep(ctx, StepStart{RunID: run.ID, Kind: StepKindToolCall, Name: "push", Parents: []string{first.ID}})
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(5 * time.Millisecond)
			midRun := time.Now()
			time.Sleep(5 * time.Millisecond)
			ended, err := db.EndStep(ctx, second.ID, StepEnd{Output: "pushed"})
			if err != nil {
				t.Fatal(err)
			}
			if ended.LatencyMS < 10 {
				t.Errorf("latency measured from begin: %d", ended.LatencyMS)
			}
			if _, err := db.EndStep(ctx, second.ID, StepEnd{}); err == nil {
				t.Error("a step ended twice")
			}

			replay, err := db.ReplayRun(ctx, run.ID, midRun)
			if err != nil {
				t.Fatal(err)
			}
			if len(replay.Steps) != 2 || replay.Steps[0].Status != ExecutionDone || replay.Steps[1].Status != ExecutionRunning || replay.Steps[1].Output != "" {
				t.Fatalf("replay mid-run: %+v", replay.Steps)
			}
			now, err := db.ReplayRun(ctx, run.ID, time.Now())
			if err != nil || now.Steps[1].Status != ExecutionDone {
				t.Fatalf("replay now: %+v %v", now, err)
			}
			if _, err := db.ReplayRun(ctx, run.ID, time.Now().Add(-time.Hour)); err == nil {
				t.Error("a run was replayed from before it existed")
			}
		})
	}
}

func TestExecutionRefusesWhatDidNotHappen(t *testing.T) {
	db := decisionBackends(t)[0].open(t)
	ctx := context.Background()
	if _, err := db.StartRun(ctx, RunStart{}); err == nil {
		t.Error("a run without a task")
	}
	run, err := db.StartRun(ctx, RunStart{ID: "run:r", Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.StartRun(ctx, RunStart{ID: "run:r", Task: "t"}); err == nil {
		t.Error("a run id reused")
	}
	for name, req := range map[string]StepStart{
		"no name":        {RunID: run.ID},
		"no run":         {RunID: "run:nope", Name: "x"},
		"missing parent": {RunID: run.ID, Name: "x", Parents: []string{"run:r/step-9999"}},
		"run as parent":  {RunID: run.ID, Name: "x", Parents: []string{run.ID}},
		"bad kind":       {RunID: run.ID, Name: "x", Kind: "Tool Call"},
		"reserved kind":  {RunID: run.ID, Name: "x", Kind: RunNodeType},
		"reserved attr":  {RunID: run.ID, Name: "x", Attributes: map[string]any{"status": "done"}},
		"contract attr":  {RunID: run.ID, Name: "x", Attributes: map[string]any{KeyGrade: "verified"}},
		"step as a run":  {RunID: "run:r/step-0001", Name: "x"},
		"decision kind":  {RunID: run.ID, Name: "x", Kind: DecisionNodeType},
	} {
		if _, err := db.BeginStep(ctx, req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	steps, err := db.RunSteps(ctx, run.ID)
	if err != nil || len(steps) != 0 {
		t.Fatalf("a refused step left %d steps behind (%v)", len(steps), err)
	}
	st, err := db.BeginStep(ctx, StepStart{RunID: run.ID, Name: "x", Attributes: map[string]any{"model": "m1"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.ID != "run:r/step-0001" || st.Attributes["model"] != "m1" {
		t.Fatalf("first step: %+v", st)
	}
	for name, end := range map[string]StepEnd{
		"status":     {Status: "skipped"},
		"confidence": {Confidence: ptr(1.5)},
	} {
		if _, err := db.EndStep(ctx, st.ID, end); err == nil {
			t.Errorf("end with bad %s accepted", name)
		}
	}
	if _, err := db.EndStep(ctx, run.ID, StepEnd{}); err == nil {
		t.Error("a run was ended as a step")
	}
	if _, err := db.GetStep(ctx, run.ID); err == nil {
		t.Error("a run was read as a step")
	}
	if _, err := db.GetRun(ctx, st.ID); err == nil {
		t.Error("a step was read as a run")
	}
	sum, err := db.SummarizeRun(ctx, run.ID)
	if err != nil || sum.Open != 1 {
		t.Fatalf("an unended step is open: %+v %v", sum, err)
	}
}

func TestExecutionStepsBegunInParallelGetDistinctSequenceNumbers(t *testing.T) {
	db := decisionBackends(t)[0].open(t)
	ctx := context.Background()
	run, err := db.StartRun(ctx, RunStart{ID: "run:fanout", Task: "fan out"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := db.RecordStep(ctx, StepStart{RunID: run.ID, Name: "plan"}, StepEnd{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := db.RecordStep(ctx, StepStart{RunID: run.ID, Kind: StepKindToolCall, Name: fmt.Sprintf("probe-%d", i), Parents: []string{root.ID}}, StepEnd{})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	steps, err := db.RunSteps(ctx, run.ID)
	if err != nil || len(steps) != 9 {
		t.Fatalf("steps: %d %v", len(steps), err)
	}
	var seqs []int
	for _, s := range steps {
		seqs = append(seqs, s.Seq)
	}
	sort.Ints(seqs)
	for i, s := range seqs {
		if s != i+1 {
			t.Fatalf("sequence numbers %v", seqs)
		}
	}
	// A recorder that did not allocate them carries on after the highest.
	db.execution.mu.Lock()
	db.execution.nextSeq = nil
	db.execution.mu.Unlock()
	next, err := db.RecordStep(ctx, StepStart{RunID: run.ID, Name: "after"}, StepEnd{})
	if err != nil || next.Seq != 10 {
		t.Fatalf("after a restart: %+v %v", next, err)
	}
}

func TestExecutionSubRunAndTools(t *testing.T) {
	db := decisionBackends(t)[0].open(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	call := func(name string, args any, out any) {
		t.Helper()
		raw, _ := json.Marshal(args)
		resp, err := tools.Call(ctx, name, raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, _ := json.Marshal(resp)
		if out != nil {
			if err := json.Unmarshal(b, out); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
	var run Run
	call("execution_run_start", map[string]any{"task": "triage", "agent": "lead", "id": "run:lead"}, &run)
	var delegate Step
	call("execution_step_begin", map[string]any{"run_id": run.ID, "kind": "ToolCall", "name": "delegate"}, &delegate)
	var sub Run
	call("execution_run_start", map[string]any{"task": "look at logs", "agent": "helper", "parent_step": delegate.ID}, &sub)
	var subStep Step
	call("execution_step_record", map[string]any{"run_id": sub.ID, "kind": "Retrieval", "name": "grep", "output": "3 errors", "confidence": 0.8, "latency_ms": 12}, &subStep)
	if subStep.Status != ExecutionDone || subStep.LatencyMS != 12 || subStep.Confidence == nil {
		t.Fatalf("record tool: %+v", subStep)
	}
	call("execution_run_finish", map[string]any{"run_id": sub.ID, "outcome": "found it"}, nil)
	call("execution_step_end", map[string]any{"step_id": delegate.ID, "output": "helper found it"}, nil)
	call("execution_run_finish", map[string]any{"run_id": run.ID}, nil)

	var got ExecutionRunGetResponse
	call("execution_run_get", map[string]any{"run_id": run.ID}, &got)
	if got.Summary.Steps != 1 || len(got.Steps) != 1 || got.Summary.Run.Status != ExecutionDone {
		t.Fatalf("run_get: %+v", got)
	}
	var list ExecutionRunsListResponse
	call("execution_runs_list", map[string]any{"limit": 1}, &list)
	if list.Count != 1 || !list.Truncated {
		t.Fatalf("runs_list: %+v", list)
	}
	var lineage ExecutionLineageResponse
	call("execution_step_lineage", map[string]any{"step_id": delegate.ID, "direction": "downstream"}, &lineage)
	if len(lineage.Steps) != 0 {
		t.Fatalf("lineage does not cross SPAWNED: %+v", lineage)
	}
	var replay RunReplay
	call("execution_run_replay", map[string]any{"run_id": run.ID, "as_of": time.Now().Format(time.RFC3339Nano)}, &replay)
	if replay.Run.ID != run.ID || len(replay.Steps) != 1 {
		t.Fatalf("replay tool: %+v", replay)
	}
	if _, err := tools.Call(ctx, "execution_run_replay", json.RawMessage(`{"run_id":"run:lead","as_of":"yesterday"}`)); err == nil {
		t.Error("a non-RFC-3339 as_of was accepted")
	}
	edges, err := db.Graph().GetEdges(ctx, sub.ID, "in")
	if err != nil {
		t.Fatal(err)
	}
	var spawned bool
	for _, e := range edges {
		spawned = spawned || (e.EdgeType == ExecutionEdgeSpawned && e.FromNodeID == delegate.ID)
	}
	if !spawned {
		t.Fatalf("no SPAWNED edge from the delegating step: %+v", edges)
	}
}
