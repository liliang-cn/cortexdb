package cortexdb

import (
	"context"
	"database/sql"
	"sort"
	"time"
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
//
// Two listing modes live here, not one flag on one:
//
//   - No cursor, no Order → the existing behaviour, unchanged. The
//     most-connected core, truncated, no NextCursor. Degree ranking has no
//     stable page boundary, so it cannot also be resumed.
//   - Cursor supplied, or Order "id" → an id-ordered walk with NextCursor, no
//     degree ranking. This is what lets a caller read a graph too big for one
//     response, page by page, without missing or repeating a node.

// GraphListAllRequest asks for the whole meaningful entity graph.
type GraphListAllRequest struct {
	// Limit caps how many nodes come back (0 = defaultGraphListLimit).
	//
	// In degree-ranked mode (no Cursor, no Order) edges are then restricted to
	// those between returned nodes, so the result is always a self-consistent
	// subgraph rather than one with dangling ends. In id-walk mode (Cursor set,
	// or Order "id") that guarantee is weaker — see Order.
	Limit int `json:"limit,omitempty"`
	// Cursor resumes an id-ordered walk. Supplying it implies Order "id".
	Cursor string `json:"cursor,omitempty"`
	// Order selects what a page means. "" (default) keeps the most-connected
	// core, which is what makes a large graph renderable. "id" walks the graph
	// in a stable order so a caller can read all of it.
	//
	// These are different operations, not a flag on one: degree ranking and a
	// resumable walk cannot share a page boundary.
	//
	// A single id-walk page is not self-consistent the way a degree-ranked one
	// is: edges are kept when their `from` endpoint is on the page, regardless
	// of where `to` landed, so one page routinely carries edges whose `to` node
	// is on a different page (or not yet fetched). The subgraph is only
	// complete — every node once, every edge once, no dangling ends — once the
	// walk has been read to completion.
	Order string `json:"order,omitempty"`
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
	// NextCursor is the resume point for the next page. Only set in id order —
	// the degree-ranked core has no stable boundary to resume from.
	NextCursor string `json:"next_cursor,omitempty"`
}

const defaultGraphListLimit = 2000

// has_chunk carries document layout, never meaning between entities, so it is
// skipped by name. "next" is not: it is the obvious name for one thing
// following another, and skipping the type outright meant a caller who modelled
// a sequence got its nodes back with every link between them missing. Chunk
// endpoints are what make an edge structural, so those are filtered instead.

// ListGraphAll returns the meaningful entity graph: every non-chunk node and
// the edges between them, excluding edges that only wire chunks. When the
// node count exceeds the limit it keeps the most-connected core, which is what
// makes a large graph readable rather than an arbitrary slice of it.
//
// Supplying a cursor, or asking for Order "id", switches to an id-ordered walk
// instead: see listGraphPageByID.
func (db *DB) ListGraphAll(ctx context.Context, req GraphListAllRequest) (*GraphListAllResponse, error) {
	if req.Cursor != "" || req.Order == "id" {
		return db.listGraphPageByID(ctx, req)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultGraphListLimit
	}
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, err
	}

	// Edges first: they give every node its degree.
	raw, degree, err := db.listGraphEdgesAndDegree(ctx)
	if err != nil {
		return nil, err
	}

	nodeRows, err := db.query(ctx,
		// Ordered so the listing is the same graph twice, not just the same
		// nodes: this is what a caller renders, and an unordered read gives
		// PostgreSQL licence to hand back a different arrangement each time.
		`SELECT id, COALESCE(content,''), COALESCE(node_type,'')
		 FROM graph_nodes WHERE node_type != 'chunk'
		 ORDER BY id`)
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

// listGraphPageByID walks the entity graph in id order so a caller can read all
// of it. Each page carries the edges whose `from` endpoint is on that page, so
// a complete walk yields every node once and every edge once.
func (db *DB) listGraphPageByID(ctx context.Context, req GraphListAllRequest) (*GraphListAllResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultGraphListLimit
	}
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, err
	}

	afterID := ""
	if req.Cursor != "" {
		_, id, err := decodeListingCursor(listingCursorKindGraph, req.Cursor)
		if err != nil {
			return nil, err
		}
		afterID = id
	}

	// Degree still comes from the whole edge set: a node's degree is a property
	// of the graph, not of the page it landed on.
	raw, degree, err := db.listGraphEdgesAndDegree(ctx)
	if err != nil {
		return nil, err
	}

	var (
		rows *sql.Rows
		qErr error
	)
	const nodeBase = `SELECT id, COALESCE(content,''), COALESCE(node_type,'')
		 FROM graph_nodes WHERE node_type != 'chunk'`
	if afterID != "" {
		rows, qErr = db.query(ctx, nodeBase+` AND id > ? ORDER BY id LIMIT ?`, afterID, limit+1)
	} else {
		rows, qErr = db.query(ctx, nodeBase+` ORDER BY id LIMIT ?`, limit+1)
	}
	if qErr != nil {
		return nil, qErr
	}
	defer func() { _ = rows.Close() }()

	page := make([]GraphListAllNode, 0, limit)
	more := false
	for rows.Next() {
		var id, content, ntype string
		if err := rows.Scan(&id, &content, &ntype); err != nil {
			return nil, err
		}
		if len(page) == limit {
			more = true
			break
		}
		label := content
		if label == "" {
			label = trimGraphNodePrefix(id)
		}
		page = append(page, GraphListAllNode{ID: id, Label: label, Type: ntype, Degree: degree[id]})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	onPage := make(map[string]struct{}, len(page))
	for _, n := range page {
		onPage[n.ID] = struct{}{}
	}
	edges := make([]GraphListAllEdge, 0)
	for _, e := range raw {
		if _, ok := onPage[e.from]; !ok {
			continue
		}
		edges = append(edges, GraphListAllEdge{From: e.from, To: e.to, Type: e.etype})
	}

	total, err := db.countGraphEntityNodes(ctx)
	if err != nil {
		return nil, err
	}
	resp := &GraphListAllResponse{Nodes: page, Edges: edges, TotalNodes: total}
	if more && len(page) > 0 {
		resp.Truncated = true
		resp.NextCursor = encodeListingCursor(listingCursorKindGraph, time.Time{}, page[len(page)-1].ID)
	}
	return resp, nil
}

// countGraphEntityNodes counts the non-chunk nodes, so a paged caller can say
// what fraction it is holding.
func (db *DB) countGraphEntityNodes(ctx context.Context) (int, error) {
	row := db.queryRow(ctx, `SELECT COUNT(*) FROM graph_nodes WHERE node_type != 'chunk'`)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// rawEdge is one meaningful edge, before it is narrowed to a page.
type rawEdge struct{ from, etype, to string }

// listGraphEdgesAndDegree reads every meaningful edge and the degree it gives
// each node. Degree is a property of the graph, not of the page a node lands
// on, so both listing modes compute it over the whole edge set.
func (db *DB) listGraphEdgesAndDegree(ctx context.Context) ([]rawEdge, map[string]int, error) {
	edgeRows, err := db.query(ctx,
		`SELECT e.from_node_id, COALESCE(e.edge_type,''), e.to_node_id
		 FROM graph_edges e
		 JOIN graph_nodes f ON f.id = e.from_node_id
		 JOIN graph_nodes t ON t.id = e.to_node_id
		 WHERE e.edge_type != 'has_chunk'
		   AND COALESCE(f.node_type,'') != 'chunk'
		   AND COALESCE(t.node_type,'') != 'chunk'
		 ORDER BY e.from_node_id, e.to_node_id, e.edge_type`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = edgeRows.Close() }()

	raw := make([]rawEdge, 0)
	degree := make(map[string]int)
	for edgeRows.Next() {
		var e rawEdge
		if err := edgeRows.Scan(&e.from, &e.etype, &e.to); err != nil {
			return nil, nil, err
		}
		raw = append(raw, e)
		degree[e.from]++
		degree[e.to]++
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, err
	}
	return raw, degree, nil
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
