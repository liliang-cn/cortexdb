package cortexdb

// Point-in-time reads over MCP.
//
// Three tools, and the split between them is the same one the storage makes:
// graph_snapshot and graph_diff read the past, vacuum_graph is the single
// operation that destroys any of it. Classifying vacuum_graph as a write is not
// bookkeeping — pkg/authz derives what a read-only key may call from exactly
// this flag, and a purge reachable with a read-only key would be the worst
// mislabelling in the toolbox.
//
// Instants are RFC 3339 strings and a malformed one is refused rather than read
// as "now". A time-travel tool that silently answered about the present when it
// could not parse the past would give a confidently wrong answer to the one
// question it exists for.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GraphSnapshotToolRequest asks what the graph looked like at an instant.
type GraphSnapshotToolRequest struct {
	AsOf      string   `json:"as_of"`
	NodeTypes []string `json:"node_types,omitempty"`
	EdgeTypes []string `json:"edge_types,omitempty"`
	Sample    int      `json:"sample,omitempty"`
}

// GraphDiffToolRequest asks what changed between two instants.
type GraphDiffToolRequest struct {
	From             string   `json:"from"`
	To               string   `json:"to,omitempty"`
	NodeTypes        []string `json:"node_types,omitempty"`
	EdgeTypes        []string `json:"edge_types,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	Cursor           string   `json:"cursor,omitempty"`
	MaxIntervalPairs int      `json:"max_interval_pairs,omitempty"`
}

// VacuumGraphToolRequest reclaims history closed before a cutoff.
type VacuumGraphToolRequest struct {
	Before string `json:"before"`
	DryRun bool   `json:"dry_run,omitempty"`
}

func temporalToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name: "graph_snapshot",
			Description: "Count the graph as it stood at an instant — nodes, edges and orphans — optionally listing some of it. " +
				"Use it to answer 'what did we believe about X before the failover': retracted facts and superseded versions are " +
				"still readable at any instant before they went. Reads only.",
			InputSchema: toolObjectSchema(
				[]string{"as_of"},
				map[string]any{
					"as_of":      toolStringSchema("The instant to read, RFC 3339 (e.g. 2026-06-01T12:00:00Z)."),
					"node_types": toolStringArraySchema("Only count and list nodes of these types."),
					"edge_types": toolStringArraySchema("Only count and list edges of these types."),
					"sample":     toolIntegerSchema("List up to this many nodes and edges alongside the counts. 0 lists none."),
				},
			),
		},
		{
			Name: "graph_diff",
			Description: "Report what the graph gained, lost and changed between two instants: added, retracted and changed nodes " +
				"and edges, each with what it said before and after. Also names how the changed facts about one subject sit in " +
				"time using Allen's interval relations, so 'this claim ended where that one began' is stated rather than left to " +
				"be read off two timestamps. Paged; pass the returned cursor back for the next page. Reads only.",
			InputSchema: toolObjectSchema(
				[]string{"from"},
				map[string]any{
					"from":               toolStringSchema("The earlier instant, RFC 3339."),
					"to":                 toolStringSchema("The later instant, RFC 3339. Defaults to now."),
					"node_types":         toolStringArraySchema("Only diff nodes of these types."),
					"edge_types":         toolStringArraySchema("Only diff edges of these types."),
					"limit":              toolIntegerSchema("Maximum changes per list (default 100)."),
					"cursor":             toolStringSchema("Continue a previous page: the next_cursor it returned."),
					"max_interval_pairs": toolIntegerSchema("Maximum interval relations to report (default 50)."),
				},
			),
		},
		{
			Name:    "vacuum_graph",
			Mutates: true,
			Description: "Permanently remove graph history that closed before a cutoff — retracted rows and superseded versions. " +
				"This is the only hard delete: after it, those instants can no longer be read. Nothing a current read can see is " +
				"touched. Run with dry_run first. A cutoff is required.",
			InputSchema: toolObjectSchema(
				[]string{"before"},
				map[string]any{
					"before":  toolStringSchema("Remove history that closed before this instant, RFC 3339."),
					"dry_run": toolBooleanSchema("Report what would be removed without removing it."),
				},
			),
		},
	}
}

// GraphSnapshotTool answers graph_snapshot.
func (db *DB) GraphSnapshotTool(ctx context.Context, req GraphSnapshotToolRequest) (GraphSnapshot, error) {
	at, err := parseTemporalInstant("as_of", req.AsOf, false)
	if err != nil {
		return GraphSnapshot{}, err
	}
	snap, err := db.GraphSnapshotAt(ctx, at, SnapshotOptions{
		NodeTypes: req.NodeTypes,
		EdgeTypes: req.EdgeTypes,
		Sample:    req.Sample,
	})
	if err != nil {
		return GraphSnapshot{}, err
	}
	return *snap, nil
}

// GraphDiffTool answers graph_diff.
//
// `to` defaults to now because the overwhelmingly common ask is "what has
// changed since", and making the caller name the current instant would be an
// invitation to name it slightly wrong.
func (db *DB) GraphDiffTool(ctx context.Context, req GraphDiffToolRequest) (graph.GraphDiffResult, error) {
	from, err := parseTemporalInstant("from", req.From, false)
	if err != nil {
		return graph.GraphDiffResult{}, err
	}
	to, err := parseTemporalInstant("to", req.To, true)
	if err != nil {
		return graph.GraphDiffResult{}, err
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	result, err := db.GraphDiff(ctx, from, to, graph.DiffOptions{
		Limit:            req.Limit,
		Cursor:           req.Cursor,
		NodeTypes:        req.NodeTypes,
		EdgeTypes:        req.EdgeTypes,
		MaxIntervalPairs: req.MaxIntervalPairs,
	})
	if err != nil {
		return graph.GraphDiffResult{}, err
	}
	return *result, nil
}

// VacuumGraphTool answers vacuum_graph.
func (db *DB) VacuumGraphTool(ctx context.Context, req VacuumGraphToolRequest) (graph.PurgeReport, error) {
	before, err := parseTemporalInstant("before", req.Before, false)
	if err != nil {
		return graph.PurgeReport{}, err
	}
	report, err := db.VacuumGraph(ctx, before, req.DryRun)
	if err != nil {
		return graph.PurgeReport{}, err
	}
	return *report, nil
}

// parseTemporalInstant reads the RFC 3339 string a model writes.
//
// Optional means an empty string is the zero time and the caller decides what
// that means; anything non-empty that does not parse is an error. Never a
// silent fallback to now: the whole point of these tools is that the caller
// meant a particular moment, and answering about a different one looks exactly
// like a correct answer.
func parseTemporalInstant(field, value string, optional bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if optional {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("cortexdb: %s is required, as an RFC 3339 instant", field)
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("cortexdb: %s must be RFC 3339 (e.g. 2026-06-01T12:00:00Z): %w", field, err)
	}
	return t.UTC(), nil
}

// callTemporalTool dispatches the three from JSON, the shape callDecisionTool
// and callOntologyTool already use — the wiring lives with the feature.
func (t *GraphRAGToolbox) callTemporalTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case "graph_snapshot":
		var req GraphSnapshotToolRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.db.GraphSnapshotTool(ctx, req)
		return resp, true, err
	case "graph_diff":
		var req GraphDiffToolRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.db.GraphDiffTool(ctx, req)
		return resp, true, err
	case "vacuum_graph":
		var req VacuumGraphToolRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.db.VacuumGraphTool(ctx, req)
		return resp, true, err
	}
	return nil, false, nil
}

// addTemporalMCPTools registers the three, in the same file as their
// definitions: the two lists are hand-kept, and a tool in Definitions() but not
// in the server is absent from every MCP host while the release notes say it
// shipped.
func addTemporalMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, db *DB) {
	addGraphRAGMCPTool(server, definitions["graph_snapshot"], func(ctx context.Context, req GraphSnapshotToolRequest) (GraphSnapshot, error) {
		return db.GraphSnapshotTool(ctx, req)
	})
	addGraphRAGMCPTool(server, definitions["graph_diff"], func(ctx context.Context, req GraphDiffToolRequest) (graph.GraphDiffResult, error) {
		return db.GraphDiffTool(ctx, req)
	})
	addGraphRAGMCPTool(server, definitions["vacuum_graph"], func(ctx context.Context, req VacuumGraphToolRequest) (graph.PurgeReport, error) {
		return db.VacuumGraphTool(ctx, req)
	})
}
