package cortexdb

import (
	"context"
	"sort"
)

// Bulk graph listing, for the view that needs the whole entity graph rather
// than a neighborhood: the interactive HTML graph.
//
// The existing graph tools all start from something you already know —
// expand_graph needs seed ids, find_nodes needs a name. Rendering the brain
// means reading it whole, and without this the HTML view can only open a local
// database file, which on a machine pointed at a shared brain is the wrong one.
//
// Mirrors memory_list_all: bounded by default, and says so when it truncates.

// GraphListAllRequest asks for the whole meaningful entity graph.
type GraphListAllRequest struct {
	// Limit caps how many nodes come back (0 = defaultGraphListLimit). Edges are
	// then restricted to those between returned nodes, so the result is always a
	// self-consistent subgraph rather than one with dangling ends.
	Limit int `json:"limit,omitempty"`
}

// GraphListAllNode is one node in a bulk listing.
type GraphListAllNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
	// Degree is how many meaningful edges touch this node. The caller ranks by
	// it when it has to show only part of a large graph.
	Degree int `json:"degree"`
}

// GraphListAllEdge is one edge in a bulk listing.
type GraphListAllEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type,omitempty"`
}

// GraphListAllResponse carries the subgraph and whether it was cut short.
type GraphListAllResponse struct {
	Nodes []GraphListAllNode `json:"nodes"`
	Edges []GraphListAllEdge `json:"edges"`
	// Truncated is true when Limit dropped nodes. A view that silently showed
	// part of a graph would look like the whole one.
	Truncated bool `json:"truncated,omitempty"`
	// TotalNodes is how many meaningful nodes exist, so a truncated caller can
	// report what it is not showing.
	TotalNodes int `json:"total_nodes"`
}

const defaultGraphListLimit = 2000

// structural edge types carry document layout, not meaning between entities.
var graphListSkipEdgeTypes = []any{"has_chunk", "next"}

// ListGraphAll returns the meaningful entity graph: every non-chunk node and
// the edges between them, excluding structural (has_chunk/next) edges. When the
// node count exceeds the limit it keeps the most-connected core, which is what
// makes a large graph readable rather than an arbitrary slice of it.
func (db *DB) ListGraphAll(ctx context.Context, req GraphListAllRequest) (*GraphListAllResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultGraphListLimit
	}
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, err
	}

	// Edges first: they give every node its degree.
	edgeRows, err := db.query(ctx,
		`SELECT from_node_id, COALESCE(edge_type,''), to_node_id
		 FROM graph_edges WHERE edge_type NOT IN (?, ?)`,
		graphListSkipEdgeTypes...)
	if err != nil {
		return nil, err
	}
	type rawEdge struct{ from, etype, to string }
	raw := make([]rawEdge, 0)
	degree := make(map[string]int)
	for edgeRows.Next() {
		var e rawEdge
		if err := edgeRows.Scan(&e.from, &e.etype, &e.to); err != nil {
			_ = edgeRows.Close()
			return nil, err
		}
		raw = append(raw, e)
		degree[e.from]++
		degree[e.to]++
	}
	if err := edgeRows.Err(); err != nil {
		_ = edgeRows.Close()
		return nil, err
	}
	_ = edgeRows.Close()

	nodeRows, err := db.query(ctx,
		`SELECT id, COALESCE(content,''), COALESCE(node_type,'')
		 FROM graph_nodes WHERE node_type != 'chunk'`)
	if err != nil {
		return nil, err
	}
	all := make([]GraphListAllNode, 0)
	for nodeRows.Next() {
		var id, content, ntype string
		if err := nodeRows.Scan(&id, &content, &ntype); err != nil {
			_ = nodeRows.Close()
			return nil, err
		}
		label := content
		if label == "" {
			label = trimGraphNodePrefix(id)
		}
		all = append(all, GraphListAllNode{ID: id, Label: label, Type: ntype, Degree: degree[id]})
	}
	if err := nodeRows.Err(); err != nil {
		_ = nodeRows.Close()
		return nil, err
	}
	_ = nodeRows.Close()

	resp := &GraphListAllResponse{TotalNodes: len(all)}

	// Keep the most-connected core when the graph is larger than the limit.
	if len(all) > limit {
		sortGraphNodesByDegree(all)
		all = all[:limit]
		resp.Truncated = true
	}

	kept := make(map[string]struct{}, len(all))
	for _, n := range all {
		kept[n.ID] = struct{}{}
	}
	edges := make([]GraphListAllEdge, 0, len(raw))
	for _, e := range raw {
		if _, ok := kept[e.from]; !ok {
			continue
		}
		if _, ok := kept[e.to]; !ok {
			continue
		}
		edges = append(edges, GraphListAllEdge{From: e.from, To: e.to, Type: e.etype})
	}

	resp.Nodes = all
	resp.Edges = edges
	return resp, nil
}

// sortGraphNodesByDegree orders most-connected first, breaking ties by id so a
// truncated listing is stable across calls.
func sortGraphNodesByDegree(nodes []GraphListAllNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Degree != nodes[j].Degree {
			return nodes[i].Degree > nodes[j].Degree
		}
		return nodes[i].ID < nodes[j].ID
	})
}

func trimGraphNodePrefix(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' && i+1 < len(id) {
			return id[i+1:]
		}
	}
	return id
}
