package cortexdb

// graph_history over MCP: the timeline of one entity and its relations, and
// which version is in effect. Read-only; vacuum_graph stays the only call that
// destroys history. See graph_history.go for the model.

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GraphHistoryToolRequest is graph_history's input.
type GraphHistoryToolRequest struct {
	Entity      string `json:"entity"`
	Relation    string `json:"relation,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	AsOf        string `json:"as_of,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	MaxVersions int    `json:"max_versions,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
}

func graphHistoryToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name: "graph_history",
		Description: "The history of one entity and its relations, oldest first, like git log with git blame attached. " +
			"Returns the entity's own versions and its edges grouped into lines by subject and predicate, so " +
			"'Leo lives_in Beijing' then 'Leo lives_in Chengdu' reads as ONE line with two versions even though they are two " +
			"edge ids. Each version has its valid interval, recorded_at/retracted_at, status (current, superseded, retracted, " +
			"merged, ...), reason, superseded_by, producer, the ontology schema id and version that decided its closure " +
			"(recorded when it happened, not looked up now) and its provenance (document, chunks, rule, source). Each version " +
			"in effect carries in_effect: why it is the one that holds — only_version, replaced (what it took over from and by " +
			"which rule: single-valued link in schema vN, explicit supersede, retraction), revised (rewritten in place: " +
			"correction or document re-ingest), merge_survivor, or several_in_effect (several live values for a link; says " +
			"what the schema thinks of that and never names a winner). Pass as_of to ask which version was in effect at an " +
			"instant, and why. The entity is a node id or a name (exact or normalised match only). Paged by line; pass " +
			"next_cursor back for more. Reads only.",
		InputSchema: toolObjectSchema(
			[]string{"entity"},
			map[string]any{
				"entity":       toolStringSchema("Node id or entity name."),
				"relation":     toolStringSchema("Only this edge type (predicate)."),
				"from":         toolStringSchema("Only versions whose valid interval overlaps [from, to). RFC 3339."),
				"to":           toolStringSchema("End of the window, RFC 3339."),
				"as_of":        toolStringSchema("Mark the versions in effect at this instant (RFC 3339) and explain them. Default now."),
				"limit":        toolIntegerSchema("Lines per page (default 50, at most 500)."),
				"max_versions": toolIntegerSchema("Versions loaded per line, newest kept (default 100, at most 1000)."),
				"cursor":       toolStringSchema("Continue a previous page: the next_cursor it returned."),
			},
		),
	}
}

// GraphHistoryTool answers graph_history.
func (db *DB) GraphHistoryTool(ctx context.Context, req GraphHistoryToolRequest) (GraphHistoryResult, error) {
	from, err := parseTemporalInstant("from", req.From, true)
	if err != nil {
		return GraphHistoryResult{}, err
	}
	to, err := parseTemporalInstant("to", req.To, true)
	if err != nil {
		return GraphHistoryResult{}, err
	}
	asOf, err := parseTemporalInstant("as_of", req.AsOf, true)
	if err != nil {
		return GraphHistoryResult{}, err
	}
	res, err := db.GraphHistory(ctx, GraphHistoryRequest{
		Entity: req.Entity, Relation: req.Relation, From: from, To: to, AsOf: asOf,
		Limit: req.Limit, MaxVersions: req.MaxVersions, Cursor: req.Cursor,
	})
	if err != nil {
		return GraphHistoryResult{}, err
	}
	if res == nil {
		return GraphHistoryResult{}, fmt.Errorf("cortexdb: graph history: no result")
	}
	return *res, nil
}

func addGraphHistoryMCPTool(server *mcp.Server, definitions map[string]ToolDefinition, db *DB) {
	addGraphRAGMCPTool(server, definitions["graph_history"], func(ctx context.Context, req GraphHistoryToolRequest) (GraphHistoryResult, error) {
		return db.GraphHistoryTool(ctx, req)
	})
}
