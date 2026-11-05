package cortexdb

// The history of RDF triples at the facade: the as-of reads of the knowledge
// graph tools, and knowledge_graph_history, which lists the versions of a
// subject's triples.
//
// pkg/graph holds the storage half (rdf_history.go): kg_triples_history, the
// as-of union, the rule for inferred triples. This is the half a caller sees.
// An optional as_of on knowledge_graph_query, knowledge_graph_find,
// graph_cypher_query and expand_graph reads the graph as it stood at that
// instant; a malformed instant is refused, never read as now, for the reason
// parseTemporalInstant gives. Reading the past is read-only, so a SPARQL update
// under as_of is refused by the store, whatever it says.
//
// knowledge_graph_history is a read: it lists, never changes. vacuum_graph
// remains the one call that destroys history.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// asOfContext returns ctx reading as of the RFC 3339 instant `value`, or ctx
// unchanged when value is empty.
func asOfContext(ctx context.Context, value string) (context.Context, error) {
	at, err := parseTemporalInstant("as_of", value, true)
	if err != nil {
		return ctx, err
	}
	if at.IsZero() {
		return ctx, nil
	}
	return graph.AsOf(ctx, at), nil
}

// KnowledgeGraphHistoryRequest asks for the versions of the triples of one
// subject.
type KnowledgeGraphHistoryRequest struct {
	// Subject is required.
	Subject   *KnowledgeGraphTerm `json:"subject"`
	Predicate *KnowledgeGraphTerm `json:"predicate,omitempty"`
	Object    *KnowledgeGraphTerm `json:"object,omitempty"`
	// Inferred narrows to inferred (true) or asserted (false) triples.
	Inferred *bool `json:"inferred,omitempty"`
	// From and To bound the window, RFC 3339, [From, To).
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// KnowledgeGraphHistoryResponse is one page of versions, oldest first.
type KnowledgeGraphHistoryResponse struct {
	Versions   []graph.TripleHistoryEntry `json:"versions"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

// KnowledgeGraphHistory lists every version of the triples of a subject —
// those in effect now and those that ended, each with when it was recorded,
// when it ended and why (reason, superseded_by, producer) — oldest first and
// paged.
func (db *DB) KnowledgeGraphHistory(ctx context.Context, req KnowledgeGraphHistoryRequest) (*KnowledgeGraphHistoryResponse, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: knowledge graph history: nil db")
	}
	from, err := parseTemporalInstant("from", req.From, true)
	if err != nil {
		return nil, err
	}
	to, err := parseTemporalInstant("to", req.To, true)
	if err != nil {
		return nil, err
	}
	page, err := db.graph.TripleHistory(ctx, graph.TripleHistoryQuery{
		Subject:   req.Subject,
		Predicate: req.Predicate,
		Object:    req.Object,
		Inferred:  req.Inferred,
		From:      from,
		To:        to,
		Limit:     req.Limit,
		Cursor:    req.Cursor,
	})
	if err != nil {
		return nil, err
	}
	return &KnowledgeGraphHistoryResponse{Versions: page.Entries, NextCursor: page.NextCursor}, nil
}

func knowledgeGraphHistoryToolDefinitions() []ToolDefinition {
	return []ToolDefinition{{
		Name: "knowledge_graph_history",
		Description: "List every version of the RDF triples of one subject, oldest first: the ones in effect now (status current) and the ones that ended " +
			"(status ended), each with when the store recorded it (recorded_at), when it stopped (retracted_at) and why " +
			"(reason such as retracted or underived, superseded_by, producer such as rdf.delete, sparql.update or inference.maintain). " +
			"Narrow by predicate and object; from/to bound the window [from, to). Inferred triples are listed with their rule and supports; " +
			"pass inferred=false for asserted triples only. Paged: pass the returned next_cursor back. " +
			"To read the whole graph as it stood at an instant instead, give knowledge_graph_query or knowledge_graph_find an as_of. Reads only.",
		InputSchema: toolObjectSchema(
			[]string{"subject"},
			map[string]any{
				"subject":   toolKnowledgeGraphTermSchema(),
				"predicate": toolKnowledgeGraphTermSchema(),
				"object":    toolKnowledgeGraphTermSchema(),
				"inferred":  toolBooleanSchema("true for inferred triples only, false for asserted only. Omit for both."),
				"from":      toolStringSchema("Only versions that held at or after this instant, RFC 3339."),
				"to":        toolStringSchema("Only versions recorded before this instant, RFC 3339."),
				"limit":     toolIntegerSchema("Versions per page (default 100, at most 1000)."),
				"cursor":    toolStringSchema("Continue a previous page: the next_cursor it returned."),
			},
		),
	}}
}

// callKnowledgeGraphHistoryTool dispatches knowledge_graph_history from JSON.
func (t *GraphRAGToolbox) callKnowledgeGraphHistoryTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	if name != "knowledge_graph_history" {
		return nil, false, nil
	}
	var req KnowledgeGraphHistoryRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, true, fmt.Errorf("decode %s: %w", name, err)
	}
	resp, err := t.db.KnowledgeGraphHistory(ctx, req)
	return resp, true, err
}

// addKnowledgeGraphHistoryMCPTool registers it, beside its definition.
func addKnowledgeGraphHistoryMCPTool(server *mcp.Server, definitions map[string]ToolDefinition, db *DB) {
	addGraphRAGMCPTool(server, definitions["knowledge_graph_history"], func(ctx context.Context, req KnowledgeGraphHistoryRequest) (KnowledgeGraphHistoryResponse, error) {
		resp, err := db.KnowledgeGraphHistory(ctx, req)
		if err != nil {
			return KnowledgeGraphHistoryResponse{}, err
		}
		return *resp, nil
	})
}
