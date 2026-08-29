package graph

import (
	"context"
	"fmt"
	"strings"
)

// What the graph actually contains, by type.
//
// These answer the questions an ontology or drift check asks: which types are
// really in the graph, and do the edges run between the ends the vocabulary
// declared. Until now the library offered counts of everything and nothing in
// between — GetGraphStatistics reports how many nodes there are, never what
// kind — so callers that needed the breakdown wrote SQL against graph_nodes
// and graph_edges themselves.
//
// That is the thing worth removing. A caller spelling out its own three-table
// JOIN is pinned to this schema and to one database's dialect, and neither is
// promised to it. When the graph layer learned to run on PostgreSQL as well as
// SQLite, nothing in such a caller would have failed loudly: the query still
// parses, still returns rows, and simply stops describing the graph the moment
// the two drift apart. Everything below goes through the same dialect-aware
// path as the rest of the package, so a caller using it moves when the library
// moves.

// EdgeShape is one (edge type, from-node type, to-node type) combination
// present in the graph, and how many edges have it.
type EdgeShape struct {
	EdgeType string `json:"edge_type"`
	FromType string `json:"from_type"`
	ToType   string `json:"to_type"`
	Count    int    `json:"count"`
}

// EdgeEndpoint identifies one end of an edge, with enough about the node to
// name it in a report without a second round trip.
type EdgeEndpoint struct {
	ID       string `json:"id"`
	NodeType string `json:"node_type"`
	Content  string `json:"content"`
}

// EdgeEndpointPair is one (edge type, from node, to node) combination and how
// many edges join exactly those two nodes with that type.
type EdgeEndpointPair struct {
	EdgeType string       `json:"edge_type"`
	From     EdgeEndpoint `json:"from"`
	To       EdgeEndpoint `json:"to"`
	Count    int          `json:"count"`
}

// NodeTypeCounts returns how many nodes carry each node_type.
//
// Untyped nodes are counted under the empty string rather than dropped. A
// caller that sums these and compares against GetGraphStatistics should get
// the same number, and "there are 400 nodes nobody typed" is a finding in its
// own right — silently omitting them turns it into a discrepancy the caller
// has to explain.
func (g *GraphStore) NodeTypeCounts(ctx context.Context) (map[string]int, error) {
	return g.typeCounts(ctx, `
		SELECT COALESCE(node_type, ''), COUNT(*)
		  FROM graph_nodes
		 GROUP BY COALESCE(node_type, '')
		 ORDER BY COALESCE(node_type, '')`)
}

// EdgeTypeCounts returns how many edges carry each edge_type, with untyped
// edges under the empty string for the same reason as NodeTypeCounts.
func (g *GraphStore) EdgeTypeCounts(ctx context.Context) (map[string]int, error) {
	return g.typeCounts(ctx, `
		SELECT COALESCE(edge_type, ''), COUNT(*)
		  FROM graph_edges
		 GROUP BY COALESCE(edge_type, '')
		 ORDER BY COALESCE(edge_type, '')`)
}

// typeCounts runs a two-column "value, count" query into a map.
func (g *GraphStore) typeCounts(ctx context.Context, query string) (map[string]int, error) {
	rows, err := g.query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("count types: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]int)
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, fmt.Errorf("count types: %w", err)
		}
		out[name] = n
	}
	return out, rows.Err()
}

// EdgeShapes reports the type shapes the graph's edges actually have.
//
// This is what makes a declared relation checkable. An extracted `backs`
// asserted from the resource to the pool is stored without complaint, reads as
// a fact, and is walked as one; nothing in the answer it produces says the
// arrow points the wrong way. Comparing these shapes against the declared ends
// is the only thing that can disagree with it.
//
// Passing edge types narrows the scan to those; passing none reports every
// shape in the graph. They are matched exactly, as stored — the same rule the
// traversal filters use, since the type in the graph is whatever the extracting
// model emitted and this package nowhere decides what a near-miss means.
//
// Edges whose endpoints are missing are not reported: both endpoints are
// declared foreign keys, so on a store this package opened they cannot exist.
func (g *GraphStore) EdgeShapes(ctx context.Context, edgeTypes ...string) ([]EdgeShape, error) {
	where, args := edgeTypeFilter("e.edge_type", edgeTypes)
	rows, err := g.query(ctx, `
		SELECT COALESCE(e.edge_type, ''),
		       COALESCE(f.node_type, ''),
		       COALESCE(t.node_type, ''),
		       COUNT(*)
		  FROM graph_edges e
		  JOIN graph_nodes f ON f.id = e.from_node_id
		  JOIN graph_nodes t ON t.id = e.to_node_id`+where+`
		 GROUP BY COALESCE(e.edge_type, ''), COALESCE(f.node_type, ''), COALESCE(t.node_type, '')
		 ORDER BY COALESCE(e.edge_type, ''), COALESCE(f.node_type, ''), COALESCE(t.node_type, '')`, args...)
	if err != nil {
		return nil, fmt.Errorf("edge shapes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EdgeShape
	for rows.Next() {
		var s EdgeShape
		if err := rows.Scan(&s.EdgeType, &s.FromType, &s.ToType, &s.Count); err != nil {
			return nil, fmt.Errorf("edge shapes: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// EdgeEndpointPairs reports which nodes the edges of each type actually run
// between.
//
// EdgeShapes groups by type, which answers "how is this relation wrong" and
// not "what is wrong". On a live base eighteen non-conforming edges came from
// thirteen nodes, two of which carried six between them — six edges, two
// places to look. Getting from one answer to the other needs the nodes, and
// getting the nodes needs their identity, which is why this returns content
// and type alongside the id rather than a list of ids to look up afterwards.
//
// Ordering, filtering, and the treatment of missing endpoints are the same as
// EdgeShapes. This scans one row per distinct (type, from, to) triple, so on a
// large graph pass the edge types the caller actually cares about.
func (g *GraphStore) EdgeEndpointPairs(ctx context.Context, edgeTypes ...string) ([]EdgeEndpointPair, error) {
	where, args := edgeTypeFilter("e.edge_type", edgeTypes)
	rows, err := g.query(ctx, `
		SELECT COALESCE(e.edge_type, ''),
		       f.id, COALESCE(f.node_type, ''), COALESCE(f.content, ''),
		       t.id, COALESCE(t.node_type, ''), COALESCE(t.content, ''),
		       COUNT(*)
		  FROM graph_edges e
		  JOIN graph_nodes f ON f.id = e.from_node_id
		  JOIN graph_nodes t ON t.id = e.to_node_id`+where+`
		 GROUP BY COALESCE(e.edge_type, ''), f.id, COALESCE(f.node_type, ''), COALESCE(f.content, ''),
		          t.id, COALESCE(t.node_type, ''), COALESCE(t.content, '')
		 ORDER BY COALESCE(e.edge_type, ''), f.id, t.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("edge endpoint pairs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EdgeEndpointPair
	for rows.Next() {
		var p EdgeEndpointPair
		if err := rows.Scan(&p.EdgeType,
			&p.From.ID, &p.From.NodeType, &p.From.Content,
			&p.To.ID, &p.To.NodeType, &p.To.Content,
			&p.Count); err != nil {
			return nil, fmt.Errorf("edge endpoint pairs: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// edgeTypeFilter builds an optional IN clause and its arguments.
//
// The placeholders are written as ? and left for the dialect to rebind, like
// every other query in this package: a hand-numbered $1 would work on
// PostgreSQL and break on SQLite, which is the mistake this indirection exists
// to make impossible.
func edgeTypeFilter(column string, edgeTypes []string) (string, []any) {
	if len(edgeTypes) == 0 {
		return "", nil
	}
	args := make([]any, 0, len(edgeTypes))
	for _, t := range edgeTypes {
		args = append(args, t)
	}
	return " WHERE " + column + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")", args
}
