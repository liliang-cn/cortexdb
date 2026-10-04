package cortexdb

// The execution graph over MCP.
//
// Nine tools, in two groups. Five write — open a run, begin a step, end it,
// record one after the fact, finish the run — and are what an agent calls as
// it works, so its own run is on the record while it runs. Four read — a
// run with its steps and totals, the list of runs, a step's lineage, and the
// run as it stood at an instant — and are what the next agent or an
// evaluator calls to learn what happened.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExecutionStepRecordRequest is execution_step_record: a step's start and
// end in one argument object.
type ExecutionStepRecordRequest struct {
	StepStart
	Status     string   `json:"status,omitempty"`
	Output     string   `json:"output,omitempty"`
	Error      string   `json:"error,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	LatencyMS  int64    `json:"latency_ms,omitempty"`
	Tokens     int64    `json:"tokens,omitempty"`
	CostUSD    float64  `json:"cost_usd,omitempty"`
}

// ExecutionStepEndRequest is execution_step_end.
type ExecutionStepEndRequest struct {
	StepID string `json:"step_id"`
	StepEnd
}

// ExecutionRunFinishRequest is execution_run_finish.
type ExecutionRunFinishRequest struct {
	RunID string `json:"run_id"`
	RunEnd
}

// ExecutionRunGetRequest is execution_run_get.
type ExecutionRunGetRequest struct {
	RunID string `json:"run_id"`
	// OmitSteps leaves the step list out and returns the summary alone.
	OmitSteps bool `json:"omit_steps,omitempty"`
}

// ExecutionRunGetResponse is a run with its totals and steps.
type ExecutionRunGetResponse struct {
	Summary RunSummary `json:"summary"`
	Steps   []Step     `json:"steps,omitempty"`
}

// ExecutionRunsListResponse lists runs, newest first.
type ExecutionRunsListResponse struct {
	Runs      []Run `json:"runs"`
	Count     int   `json:"count"`
	Truncated bool  `json:"truncated,omitempty"`
}

// ExecutionLineageRequest is execution_step_lineage.
type ExecutionLineageRequest struct {
	StepID    string `json:"step_id"`
	Direction string `json:"direction,omitempty"`
	MaxDepth  int    `json:"max_depth,omitempty"`
}

// ExecutionLineageResponse is the steps reached, nearest first.
type ExecutionLineageResponse struct {
	Steps []LineageStep `json:"steps"`
}

// ExecutionReplayRequest is execution_run_replay.
type ExecutionReplayRequest struct {
	RunID string `json:"run_id"`
	// AsOf is RFC 3339, nanoseconds allowed.
	AsOf string `json:"as_of"`
}

func toolAttributesSchema() map[string]any {
	return toolMapSchema("Your own properties for the record. Names the execution graph writes itself (run_id, status, output, …) and _-prefixed contract keys are refused.")
}

func toolStepStartProperties() map[string]any {
	return map[string]any{
		"run_id":     toolStringSchema("The run the step belongs to. Must exist and still be running."),
		"id":         toolStringSchema("Id for the step. Omit for <run>/step-<seq>."),
		"kind":       toolStringSchema("Step kind, which is also its Cypher label: LLMCall, ToolCall, Retrieval, DecisionPoint, Validation, or a name of your own (letters, digits, _). Default Step."),
		"name":       toolStringSchema("Which call: the tool's name, the prompt's purpose. Required."),
		"parents":    toolStringArraySchema("Ids of the steps whose output this step consumes. Each must exist; each becomes a TRIGGERED edge."),
		"input":      toolStringSchema("What went in, as text."),
		"attributes": toolAttributesSchema(),
	}
}

func toolStepEndProperties() map[string]any {
	return map[string]any{
		"status":     toolEnumSchema("done (default) or failed.", ExecutionDone, ExecutionFailed),
		"output":     toolStringSchema("What came out, as text."),
		"error":      toolStringSchema("Why a failed step failed."),
		"confidence": toolNumberSchema("The step's own confidence in its output, 0..1."),
		"latency_ms": toolIntegerSchema("How long it took. Omit to measure from the begin call."),
		"tokens":     toolIntegerSchema("Tokens the step used."),
		"cost_usd":   toolNumberSchema("What the step cost."),
		"attributes": toolAttributesSchema(),
	}
}

func executionToolDefinitions() []ToolDefinition {
	recordProps := toolStepStartProperties()
	for k, v := range toolStepEndProperties() {
		if k != "attributes" {
			recordProps[k] = v
		}
	}
	endProps := toolStepEndProperties()
	endProps["step_id"] = toolStringSchema("The step to end. Must be running.")
	return []ToolDefinition{
		{
			Name:    "execution_run_start",
			Mutates: true,
			Description: "Open an execution-graph run: the record of one task an agent carries out, to which its steps are added as it works. " +
				"Returns the run; pass its id to execution_step_begin. Set parent_step when a step of another run started this one (a sub-agent).",
			InputSchema: toolObjectSchema([]string{"task"}, map[string]any{
				"task":        toolStringSchema("What the run was asked to do. Required."),
				"agent":       toolStringSchema("Who runs it."),
				"id":          toolStringSchema("Id for the run. Omit to mint one from the agent and the moment."),
				"parent_step": toolStringSchema("The step of another run that started this one. Must exist."),
				"attributes":  toolAttributesSchema(),
			}),
		},
		{
			Name:    "execution_step_begin",
			Mutates: true,
			Description: "Begin a step of a run — an LLM call, a tool call, a retrieval, a decision point, a validation — before doing the work. " +
				"The step is written as running with TRIGGERED edges from its parents, so a crash still leaves what was attempted and why. " +
				"Call execution_step_end with the result.",
			InputSchema: toolObjectSchema([]string{"run_id", "name"}, toolStepStartProperties()),
		},
		{
			Name:    "execution_step_end",
			Mutates: true,
			Description: "End a running step with its result: done or failed, the output or the error, confidence, latency (measured from the begin call if omitted), tokens and cost. " +
				"The running version stays in history for execution_run_replay. A step ends once.",
			InputSchema: toolObjectSchema([]string{"step_id"}, endProps),
		},
		{
			Name:        "execution_step_record",
			Mutates:     true,
			Description: "Record a step that has already happened, in one call: execution_step_begin and execution_step_end together.",
			InputSchema: toolObjectSchema([]string{"run_id", "name"}, recordProps),
		},
		{
			Name:    "execution_run_finish",
			Mutates: true,
			Description: "Finish a run with its final status (done by default; failed, cancelled or your own word) and outcome. " +
				"Steps still running stay as they are and are counted as open.",
			InputSchema: toolObjectSchema([]string{"run_id"}, map[string]any{
				"run_id":     toolStringSchema("The run to finish."),
				"status":     toolStringSchema("Final status: done (default), failed, cancelled, or your own word."),
				"outcome":    toolStringSchema("The result in words."),
				"attributes": toolAttributesSchema(),
			}),
		},
		{
			Name: "execution_run_get",
			Description: "Read a run: its steps in order and its totals — steps by kind and status, open and failed steps, tokens, cost, summed latency, " +
				"the critical path (the slowest chain of TRIGGERED steps) and the step with the lowest confidence. " +
				"Use it to learn what an earlier run did before repeating its task.",
			InputSchema: toolObjectSchema([]string{"run_id"}, map[string]any{
				"run_id":     toolStringSchema("The run."),
				"omit_steps": toolBooleanSchema("Return the summary without the step list."),
			}),
		},
		{
			Name:        "execution_runs_list",
			Description: "List runs, newest first, optionally by agent or status.",
			InputSchema: toolObjectSchema(nil, map[string]any{
				"agent":  toolStringSchema("Runs by this agent."),
				"status": toolStringSchema("Runs in this status: running, done, failed, …"),
				"limit":  toolIntegerSchema("Maximum runs to return (default 50)."),
			}),
		},
		{
			Name: "execution_step_lineage",
			Description: "Walk a step's TRIGGERED edges: upstream to every step its output depended on, or downstream to every step it fed, nearest first. " +
				"Upstream is what to read when an output is wrong; downstream is what a bad output contaminated.",
			InputSchema: toolObjectSchema([]string{"step_id"}, map[string]any{
				"step_id":   toolStringSchema("The step."),
				"direction": toolEnumSchema("upstream (default) or downstream.", LineageUpstream, LineageDownstream),
				"max_depth": toolIntegerSchema("How many hops to follow (default 32)."),
			}),
		},
		{
			Name: "execution_run_replay",
			Description: "Read a run as it stood at an instant: the steps that had begun by then, each in the state it was in — running if it had not ended. " +
				"Answers for any moment since the run started, from the store's history.",
			InputSchema: toolObjectSchema([]string{"run_id", "as_of"}, map[string]any{
				"run_id": toolStringSchema("The run."),
				"as_of":  toolStringSchema("The instant, RFC 3339 (fractional seconds allowed)."),
			}),
		},
	}
}

// RecordStepTool answers execution_step_record.
func (db *DB) RecordStepTool(ctx context.Context, req ExecutionStepRecordRequest) (Step, error) {
	return db.RecordStep(ctx, req.StepStart, StepEnd{
		Status: req.Status, Output: req.Output, Error: req.Error, Confidence: req.Confidence,
		LatencyMS: req.LatencyMS, Tokens: req.Tokens, CostUSD: req.CostUSD,
	})
}

// RunGetTool answers execution_run_get.
func (db *DB) RunGetTool(ctx context.Context, req ExecutionRunGetRequest) (ExecutionRunGetResponse, error) {
	node, err := db.getRunNode(ctx, req.RunID)
	if err != nil {
		return ExecutionRunGetResponse{}, fmt.Errorf("cortexdb: get run: %w", err)
	}
	steps, err := db.runSteps(ctx, node.ID)
	if err != nil {
		return ExecutionRunGetResponse{}, err
	}
	resp := ExecutionRunGetResponse{Summary: summarizeSteps(runFromNode(node), steps)}
	if !req.OmitSteps {
		resp.Steps = steps
	}
	return resp, nil
}

// RunsListTool answers execution_runs_list, saying when the limit cut the
// list short.
func (db *DB) RunsListTool(ctx context.Context, q RunQuery) (ExecutionRunsListResponse, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	q.Limit = limit + 1
	runs, err := db.ListRuns(ctx, q)
	if err != nil {
		return ExecutionRunsListResponse{}, err
	}
	truncated := len(runs) > limit
	if truncated {
		runs = runs[:limit]
	}
	return ExecutionRunsListResponse{Runs: runs, Count: len(runs), Truncated: truncated}, nil
}

// ReplayRunTool answers execution_run_replay.
func (db *DB) ReplayRunTool(ctx context.Context, req ExecutionReplayRequest) (RunReplay, error) {
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.AsOf))
	if err != nil {
		return RunReplay{}, fmt.Errorf("cortexdb: replay run: as_of must be RFC 3339: %w", err)
	}
	return db.ReplayRun(ctx, req.RunID, at)
}

// addExecutionMCPTools exposes the nine over MCP, beside their definitions
// for the reason addDecisionMCPTools gives.
func addExecutionMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, db *DB) {
	addGraphRAGMCPTool(server, definitions["execution_run_start"], func(ctx context.Context, req RunStart) (Run, error) {
		return db.StartRun(ctx, req)
	})
	addGraphRAGMCPTool(server, definitions["execution_step_begin"], func(ctx context.Context, req StepStart) (Step, error) {
		return db.BeginStep(ctx, req)
	})
	addGraphRAGMCPTool(server, definitions["execution_step_end"], func(ctx context.Context, req ExecutionStepEndRequest) (Step, error) {
		return db.EndStep(ctx, req.StepID, req.StepEnd)
	})
	addGraphRAGMCPTool(server, definitions["execution_step_record"], func(ctx context.Context, req ExecutionStepRecordRequest) (Step, error) {
		return db.RecordStepTool(ctx, req)
	})
	addGraphRAGMCPTool(server, definitions["execution_run_finish"], func(ctx context.Context, req ExecutionRunFinishRequest) (Run, error) {
		return db.FinishRun(ctx, req.RunID, req.RunEnd)
	})
	addGraphRAGMCPTool(server, definitions["execution_run_get"], func(ctx context.Context, req ExecutionRunGetRequest) (ExecutionRunGetResponse, error) {
		return db.RunGetTool(ctx, req)
	})
	addGraphRAGMCPTool(server, definitions["execution_runs_list"], func(ctx context.Context, req RunQuery) (ExecutionRunsListResponse, error) {
		return db.RunsListTool(ctx, req)
	})
	addGraphRAGMCPTool(server, definitions["execution_step_lineage"], func(ctx context.Context, req ExecutionLineageRequest) (ExecutionLineageResponse, error) {
		steps, err := db.StepLineage(ctx, req.StepID, req.Direction, req.MaxDepth)
		return ExecutionLineageResponse{Steps: steps}, err
	})
	addGraphRAGMCPTool(server, definitions["execution_run_replay"], func(ctx context.Context, req ExecutionReplayRequest) (RunReplay, error) {
		return db.ReplayRunTool(ctx, req)
	})
}

// callExecutionTool dispatches the nine from JSON.
func (t *GraphRAGToolbox) callExecutionTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	if !strings.HasPrefix(name, "execution_") {
		return nil, false, nil
	}
	decode := func(v any) error {
		if err := json.Unmarshal(input, v); err != nil {
			return fmt.Errorf("decode %s: %w", name, err)
		}
		return nil
	}
	switch name {
	case "execution_run_start":
		var req RunStart
		if err := decode(&req); err != nil {
			return nil, true, err
		}
		resp, err := t.db.StartRun(ctx, req)
		return resp, true, err
	case "execution_step_begin":
		var req StepStart
		if err := decode(&req); err != nil {
			return nil, true, err
		}
		resp, err := t.db.BeginStep(ctx, req)
		return resp, true, err
	case "execution_step_end":
		var req ExecutionStepEndRequest
		if err := decode(&req); err != nil {
			return nil, true, err
		}
		resp, err := t.db.EndStep(ctx, req.StepID, req.StepEnd)
		return resp, true, err
	case "execution_step_record":
		var req ExecutionStepRecordRequest
		if err := decode(&req); err != nil {
			return nil, true, err
		}
		resp, err := t.db.RecordStepTool(ctx, req)
		return resp, true, err
	case "execution_run_finish":
		var req ExecutionRunFinishRequest
		if err := decode(&req); err != nil {
			return nil, true, err
		}
		resp, err := t.db.FinishRun(ctx, req.RunID, req.RunEnd)
		return resp, true, err
	case "execution_run_get":
		var req ExecutionRunGetRequest
		if err := decode(&req); err != nil {
			return nil, true, err
		}
		resp, err := t.db.RunGetTool(ctx, req)
		return resp, true, err
	case "execution_runs_list":
		var req RunQuery
		if err := decode(&req); err != nil {
			return nil, true, err
		}
		resp, err := t.db.RunsListTool(ctx, req)
		return resp, true, err
	case "execution_step_lineage":
		var req ExecutionLineageRequest
		if err := decode(&req); err != nil {
			return nil, true, err
		}
		steps, err := t.db.StepLineage(ctx, req.StepID, req.Direction, req.MaxDepth)
		return ExecutionLineageResponse{Steps: steps}, true, err
	case "execution_run_replay":
		var req ExecutionReplayRequest
		if err := decode(&req); err != nil {
			return nil, true, err
		}
		resp, err := t.db.ReplayRunTool(ctx, req)
		return resp, true, err
	}
	return nil, false, nil
}
