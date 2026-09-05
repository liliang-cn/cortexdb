package cortexdb

// The decision ledger over MCP.
//
// Three tools, matching the three things an agent does with a ledger: write
// one entry, read back why one entry was made, and ask what was decided this
// way before. DecisionsBy is a library call and not a fourth tool for the
// reason GradedRecords is not one either — the questions an agent actually
// asks are the named ones, and a filter language invites questions nobody
// needed.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DecisionRecordToolRequest records a decision. It is DecisionRecordRequest
// with the time as a string, because a tool argument arrives as JSON from a
// model and an RFC 3339 string is what a model writes; the zero value still
// means now.
type DecisionRecordToolRequest struct {
	ID         string   `json:"id,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Actor      string   `json:"actor"`
	Note       string   `json:"note"`
	Verdict    string   `json:"verdict,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	Premises   []string `json:"premises,omitempty"`
	Supersedes string   `json:"supersedes,omitempty"`
	At         string   `json:"at,omitempty"`

	Source   string `json:"source,omitempty"`
	Producer string `json:"producer,omitempty"`
	Grade    string `json:"grade,omitempty"`
	State    string `json:"state,omitempty"`
	Why      string `json:"why,omitempty"`
}

// DecisionRecordToolResponse is the entry as it was written.
type DecisionRecordToolResponse struct {
	Decision DecisionRecord `json:"decision"`
}

// DecisionChainToolRequest asks why one decision was made.
type DecisionChainToolRequest struct {
	ID       string `json:"id"`
	MaxDepth int    `json:"max_depth,omitempty"`
}

// DecisionPrecedentsToolRequest asks what was decided this way before.
type DecisionPrecedentsToolRequest struct {
	Kind    string `json:"kind,omitempty"`
	Subject string `json:"subject,omitempty"`
	Exclude string `json:"exclude,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// DecisionPrecedentsToolResponse lists them, newest first.
type DecisionPrecedentsToolResponse struct {
	Decisions []DecisionRecord `json:"decisions"`
	Count     int              `json:"count"`
	// Truncated says the limit cut the list short, so "3 precedents" is never
	// mistaken for "only 3 precedents".
	Truncated bool `json:"truncated,omitempty"`
}

func decisionToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:    "decision_record",
			Mutates: true,
			Description: "Record why an action was taken: who decided, the decision in words, the facts it rested on, what it was about and what it replaces. " +
				"Premises are node ids or edge ids that must already exist — a decision resting on a fact that is not in the graph is refused, nothing is written. " +
				"Use it whenever something is chosen, held, loaded or refused, so the next agent can read the reason instead of guessing it.",
			InputSchema: toolObjectSchema(
				[]string{"actor", "note"},
				map[string]any{
					"actor":      toolStringSchema("Who decided. Required."),
					"note":       toolStringSchema("The decision in words. Required."),
					"kind":       toolStringSchema("Shape of the decision: load, review, action, assert, or a word of your own. Precedents groups by it."),
					"verdict":    toolStringSchema("The outcome in your own word: hold, ship, rejected."),
					"subject":    toolStringSchema("Node id the decision is about. Must exist."),
					"premises":   toolStringArraySchema("Node ids and edge ids the decision rested on. Every one must exist."),
					"supersedes": toolStringSchema("Id of the decision this one replaces. Must exist and must be a decision."),
					"id":         toolStringSchema("Id for this decision. Omit to mint one; supply it to make re-recording an update instead of a second entry."),
					"at":         toolStringSchema("When it was decided, RFC 3339. Omit for now."),
					"source":     toolStringSchema("Contract _source. Defaults to decision-ledger."),
					"producer":   toolEnumSchema("Contract _producer. Defaults to human — set it when an agent, not a person, is deciding.", ProducerHuman, ProducerLLMExtract, ProducerCompiled, ProducerMeasured, ProducerDDL, ProducerGraphImport, ProducerTabular),
					"grade":      toolEnumSchema("Contract _grade. Defaults to verified, which is what a decision a named actor signed is.", GradeVerified, GradeSelfConsistent, GradeAsserted, GradeHeld, GradeRefused),
					"state":      toolStringSchema("Contract _state: your own word for where the decision stands. Never interpreted."),
					"why":        toolStringSchema("Contract _why. Required when grade is held or refused."),
				},
			),
		},
		{
			Name: "decision_chain",
			Description: "Read why a decision was made: the decision, the premises it rested on with each premise's contract grade and source, and the decisions it replaced — walked back through supersedes and through premises that are themselves decisions. " +
				"Use it before repeating or reversing something: the chain says what the last decision knew.",
			InputSchema: toolObjectSchema(
				[]string{"id"},
				map[string]any{
					"id":        toolStringSchema("Decision id, with or without the decision: prefix."),
					"max_depth": toolIntegerSchema("How many decision hops to follow (default 5, capped at 32)."),
				},
			),
		},
		{
			Name: "decision_precedents",
			Description: "List earlier decisions of the same kind or about the same subject, newest first. " +
				"Use it before deciding: 'we have done this four times and held it twice' is the answer, and 'we have no precedent' is a different answer from silence.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"kind":    toolStringSchema("Decisions of this kind."),
					"subject": toolStringSchema("Decisions about this node id."),
					"exclude": toolStringSchema("A decision id to leave out — the one being decided now is not its own precedent."),
					"limit":   toolIntegerSchema("Maximum decisions to return (default 20)."),
				},
			),
		},
	}
}

// RecordDecisionTool answers decision_record.
func (db *DB) RecordDecisionTool(ctx context.Context, req DecisionRecordToolRequest) (DecisionRecordToolResponse, error) {
	at, err := parseDecisionTime(req.At)
	if err != nil {
		return DecisionRecordToolResponse{}, err
	}
	rec, err := db.RecordDecision(ctx, DecisionRecordRequest{
		ID:         req.ID,
		Kind:       req.Kind,
		Actor:      req.Actor,
		Note:       req.Note,
		Verdict:    req.Verdict,
		Subject:    req.Subject,
		Premises:   req.Premises,
		Supersedes: req.Supersedes,
		At:         at,
		Source:     req.Source,
		Producer:   req.Producer,
		Grade:      req.Grade,
		State:      req.State,
		Why:        req.Why,
	})
	if err != nil {
		return DecisionRecordToolResponse{}, err
	}
	return DecisionRecordToolResponse{Decision: rec}, nil
}

// DecisionChainTool answers decision_chain.
func (db *DB) DecisionChainTool(ctx context.Context, req DecisionChainToolRequest) (DecisionChain, error) {
	return db.DecisionChain(ctx, req.ID, req.MaxDepth)
}

// PrecedentsTool answers decision_precedents.
//
// It asks for one more than the caller wanted, which is how it reports that it
// truncated without a second query — the same trick NeedsAttentionTool uses,
// and for the same reason: a capped list that does not say it is capped reads
// as the whole answer.
func (db *DB) PrecedentsTool(ctx context.Context, req DecisionPrecedentsToolRequest) (DecisionPrecedentsToolResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultPrecedentsLimit
	}
	recs, err := db.Precedents(ctx, PrecedentsQuery{
		Kind:    req.Kind,
		Subject: req.Subject,
		Exclude: req.Exclude,
		Limit:   limit + 1,
	})
	if err != nil {
		return DecisionPrecedentsToolResponse{}, err
	}
	truncated := len(recs) > limit
	if truncated {
		recs = recs[:limit]
	}
	return DecisionPrecedentsToolResponse{Decisions: recs, Count: len(recs), Truncated: truncated}, nil
}

// addDecisionMCPTools exposes the three over MCP.
//
// A registrar of its own, called from NewMCPServer in one line, because the
// two lists — what Definitions() says exists and what the server registers —
// are hand-kept and drift silently: a tool in the first and not the second is
// absent from every MCP host while the release notes say it shipped. Keeping
// both halves in this file is what makes that a one-file review.
func addDecisionMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, db *DB) {
	addGraphRAGMCPTool(server, definitions["decision_record"], func(ctx context.Context, req DecisionRecordToolRequest) (DecisionRecordToolResponse, error) {
		return db.RecordDecisionTool(ctx, req)
	})
	addGraphRAGMCPTool(server, definitions["decision_chain"], func(ctx context.Context, req DecisionChainToolRequest) (DecisionChain, error) {
		return db.DecisionChainTool(ctx, req)
	})
	addGraphRAGMCPTool(server, definitions["decision_precedents"], func(ctx context.Context, req DecisionPrecedentsToolRequest) (DecisionPrecedentsToolResponse, error) {
		return db.PrecedentsTool(ctx, req)
	})
}

// parseDecisionTime reads the RFC 3339 string a model writes. Empty is the
// zero time, which RecordDecision reads as now; anything else that is not RFC
// 3339 is refused rather than silently becoming now, because a decision
// stamped with the wrong moment is worse than one that failed to record.
func parseDecisionTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("cortexdb: record decision: at must be RFC 3339: %w", err)
	}
	return t, nil
}

// callDecisionTool dispatches the three decision tools from JSON.
//
// A helper hooked into GraphRAGToolbox.Call rather than three more cases in
// its switch, so the ledger's wiring lives with the ledger — the same shape
// callInferenceTool and callOntologyTool already use.
func (t *GraphRAGToolbox) callDecisionTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case "decision_record":
		var req DecisionRecordToolRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.db.RecordDecisionTool(ctx, req)
		return resp, true, err
	case "decision_chain":
		var req DecisionChainToolRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.db.DecisionChainTool(ctx, req)
		return resp, true, err
	case "decision_precedents":
		var req DecisionPrecedentsToolRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.db.PrecedentsTool(ctx, req)
		return resp, true, err
	}
	return nil, false, nil
}
