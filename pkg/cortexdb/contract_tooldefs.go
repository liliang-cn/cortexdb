package cortexdb

import "context"

// The knowledge contract over MCP.
//
// contract_query.go made the shelf readable in Go. These make it readable by
// the agents already on this brain, which is the difference between a contract
// the producers honour and one anybody can check. Two tools, because there are
// two questions: how much of what is on the shelf stands on what, and what on
// it is waiting for a person.
//
// Deliberately not a general query tool. GradedRecords takes grades and
// sources and would map onto MCP in ten lines, and an agent given a filter
// language uses it to ask questions nobody has needed yet. When something
// needs one, the library call is already here and tested.

// ContractTallyRequest takes nothing: the tally is over the whole store, and
// narrowing it to a collection would answer a different question than the one
// a reader opens with ("what is on this shelf").
type ContractTallyRequest struct{}

// NeedsAttentionRequest caps the list.
type NeedsAttentionRequest struct {
	// Limit caps the records returned. 0 uses defaultNeedsAttentionLimit.
	Limit int `json:"limit,omitempty"`
}

// NeedsAttentionResponse is the work waiting on the shelf.
type NeedsAttentionResponse struct {
	Records []GradedRecord `json:"records"`
	// Truncated and Total let a capped caller say what it is not showing. A
	// list that quietly stopped at fifty reads as "fifty things need a person",
	// and the number is the half a reader acts on.
	Truncated bool `json:"truncated,omitempty"`
	Total     int  `json:"total"`
}

// defaultNeedsAttentionLimit keeps an unbounded ask from returning a whole
// shelf into a model's context. It is a cap on the answer, not on the work:
// Total still reports everything.
const defaultNeedsAttentionLimit = 50

func contractToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "contract_tally",
			Description: "Count everything on the shelf by how well established it is: verified, self_consistent, asserted, held, refused — plus how much carries no grade at all and any grade the contract does not define. Nodes and edges are counted separately. Use it to say how much of an answer rests on something checked.",
			InputSchema: toolObjectSchema(nil, map[string]any{}),
		},
		{
			Name:        "contract_needs_attention",
			Description: "List records a person has to deal with: held (nobody has looked yet) and refused (somebody looked and said no), each with the reason its producer gave. Use it before reporting that knowledge is missing — 'we refused to record this' and 'we have nothing' are different answers.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"limit": toolIntegerSchema("Maximum records to return (default 50). The response says the true total when it truncates."),
				},
			),
		},
	}
}

// ContractTallyTool answers contract_tally.
func (db *DB) ContractTallyTool(ctx context.Context, _ ContractTallyRequest) (ContractTally, error) {
	return db.ContractTally(ctx)
}

// NeedsAttentionTool answers contract_needs_attention.
//
// It asks for one more record than the caller wanted, which is how it knows it
// truncated without a second count over the same predicate — and then reports
// the true total from the tally, because "50 shown of 50" and "50 shown of
// 900" are different situations and the cap alone cannot tell them apart.
func (db *DB) NeedsAttentionTool(ctx context.Context, req NeedsAttentionRequest) (NeedsAttentionResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultNeedsAttentionLimit
	}
	recs, err := db.NeedsAttention(ctx, limit+1)
	if err != nil {
		return NeedsAttentionResponse{}, err
	}
	truncated := len(recs) > limit
	if truncated {
		recs = recs[:limit]
	}
	total := len(recs)
	if truncated {
		t, err := db.ContractTally(ctx)
		if err != nil {
			return NeedsAttentionResponse{}, err
		}
		total = t.Held.Nodes + t.Held.Edges + t.Refused.Nodes + t.Refused.Edges
	}
	return NeedsAttentionResponse{Records: recs, Truncated: truncated, Total: total}, nil
}
