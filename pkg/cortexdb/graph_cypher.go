package cortexdb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph/cypher"
)

// Cypher over the property graph.
//
// The graph could already be asked structural questions three ways —
// expand_graph, search_paths and SPARQL over the RDF projection — and models
// were bad at all three for the same reason: none is a language they have
// read much of. Cypher is. "Who depends on CortexDB" is one line a model
// writes correctly on the first try, against the same rows the other tools
// read, with no IRI encoding to get wrong.
//
// Read-only by design, not by omission: the brain is shared, writes go
// through tools that validate and record provenance, and a query language
// that could also write would be a second, unaudited write path. CREATE,
// MERGE, SET, DELETE and REMOVE are refused by name.

// CypherQueryRequest is one read-only openCypher/GQL query.
type CypherQueryRequest struct {
	// Query is the query text, in the subset documented on pkg/graph/cypher.
	Query string `json:"query"`
	// Params binds $name parameters. Prefer them to inlining values.
	Params map[string]any `json:"params,omitempty"`
	// MaxRows caps the rows returned (default 1000, at most 10000);
	// Truncated says when more existed.
	MaxRows int `json:"max_rows,omitempty"`
	// TimeoutMS bounds the query (default 10000, at most 60000).
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// AsOf reads the graph as it stood at an RFC 3339 instant.
	AsOf string `json:"as_of,omitempty"`
	// IncludeEndedFacts also matches temporal facts whose valid_to has
	// passed (or whose valid_from has not arrived).
	IncludeEndedFacts bool `json:"include_ended_facts,omitempty"`
}

// CypherQueryResponse is a query's answer: one row per match, values in
// column order. Nodes come back as {id, labels, content, properties},
// relationships as {id, type, start, end, weight, properties}, paths as
// {nodes, relationships}.
type CypherQueryResponse struct {
	Columns   []string     `json:"columns"`
	Rows      [][]any      `json:"rows"`
	RowCount  int          `json:"row_count"`
	Truncated bool         `json:"truncated,omitempty"`
	Stats     cypher.Stats `json:"stats"`
}

// QueryCypher runs a read-only openCypher/GQL MATCH query over the property
// graph. Errors name the construct that is outside the subset.
func (db *DB) QueryCypher(ctx context.Context, req CypherQueryRequest) (*CypherQueryResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("graph_cypher_query: query is required")
	}
	greq := graph.CypherRequest{
		Query:             req.Query,
		Params:            req.Params,
		MaxRows:           req.MaxRows,
		IncludeEndedFacts: req.IncludeEndedFacts,
	}
	if req.TimeoutMS > 0 {
		greq.Timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	if s := strings.TrimSpace(req.AsOf); s != "" {
		at, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("graph_cypher_query: as_of must be RFC 3339: %w", err)
		}
		greq.AsOf = at
	}
	res, err := db.graph.QueryCypher(ctx, greq)
	if err != nil {
		return nil, err
	}
	return &CypherQueryResponse{
		Columns:   res.Columns,
		Rows:      res.Rows,
		RowCount:  len(res.Rows),
		Truncated: res.Truncated,
		Stats:     res.Stats,
	}, nil
}

// GraphCypherQuery is the toolbox face of QueryCypher.
func (t *GraphRAGToolbox) GraphCypherQuery(ctx context.Context, req CypherQueryRequest) (*CypherQueryResponse, error) {
	if req.MaxRows <= 0 {
		// A tool answer is read by a model; a thousand rows is a context
		// window, not an answer.
		req.MaxRows = defaultCypherToolRows
	}
	return t.db.QueryCypher(ctx, req)
}

const defaultCypherToolRows = 200

const graphCypherQueryDescription = "Run a READ-ONLY openCypher / GQL MATCH query over the knowledge graph's property graph and get rows back. " +
	"Call graph_schema first: labels are node types and relationship types are edge types exactly as stored (case-sensitive, often lower_snake like depends_on), and a query naming a type that does not exist returns a confident empty answer. Call graph_property_values before comparing a property to a value. " +
	"Mapping: (n:T) matches node_type T; -[:R]-> matches edge_type R; n.key reads the node's properties JSON; n.name falls back to the title property; n.content is the node's text; id(n) is the node id; labels(n) is [node_type]; type(r) is the edge type; r.weight is the edge weight. " +
	"Supported: MATCH and OPTIONAL MATCH with comma-separated patterns, directions ->, <-, -, type alternation [:A|B], inline property maps {name: 'x'}, label alternation (n:A|B), GQL (n IS A) and WHERE n IS LABELED A, named paths p = (...), variable-length -[*1..3]- (upper bound at most 6; relationships are never reused within a match); " +
	"WHERE with = <> < > <= >=, AND OR XOR NOT, IS [NOT] NULL, IN [...], STARTS WITH, ENDS WITH, CONTAINS, =~ (RE2 regex, whole string); " +
	"WITH (projection, aggregation, WHERE, ORDER BY, SKIP, LIMIT), UNWIND, UNION [ALL], RETURN [DISTINCT] with AS aliases, ORDER BY ... DESC, SKIP, LIMIT; " +
	"aggregates count(*) count(x) count(DISTINCT x) collect min max sum avg; functions id labels type keys properties size length nodes relationships startNode endNode coalesce head last tail range toLower toUpper trim replace substring split toString toInteger toFloat; CASE; list comprehensions and any/all/none/single; parameters $name (pass values in params). " +
	"Refused with an error naming the construct: CREATE, MERGE, SET, DELETE, REMOVE (read-only by design — use the upsert/delete tools), CALL, shortestPath, pattern predicates in WHERE, EXISTS/COUNT subqueries, map projections, unbounded * that would exceed 6 hops. " +
	"By default only current edges are matched (temporal facts whose valid_to has passed are hidden; set include_ended_facts or as_of). Without ORDER BY, rows come out ordered by the matched elements' ids; add ORDER BY when order matters. Results are capped at max_rows (default 200) and flagged truncated. " +
	"Examples: MATCH (p:project)-[:depends_on]->(c {name: 'CortexDB'}) RETURN p.name ORDER BY p.name. " +
	"MATCH (s)-[r:runs_on|deployed_on]->(h:host {name: 'node-e'}) RETURN s.name, labels(s), type(r). " +
	"MATCH (n) RETURN labels(n)[0] AS type, count(*) AS c ORDER BY c DESC LIMIT 10."

func graphCypherQueryToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        "graph_cypher_query",
		Mutates:     false,
		Description: graphCypherQueryDescription,
		InputSchema: toolObjectSchema(
			[]string{"query"},
			map[string]any{
				"query":               toolStringSchema("The Cypher/GQL query, e.g. MATCH (p:project)-[:depends_on]->(c {name: $name}) RETURN p.name"),
				"params":              toolMapSchema("Values for $name parameters."),
				"max_rows":            toolIntegerSchema("Cap on rows returned. Default 200, at most 10000."),
				"timeout_ms":          toolIntegerSchema("Time budget in milliseconds. Default 10000, at most 60000."),
				"as_of":               toolStringSchema("Read the graph as it stood at this RFC 3339 instant."),
				"include_ended_facts": toolBooleanSchema("Also match temporal facts that have ended or not yet begun."),
			},
		),
	}
}
