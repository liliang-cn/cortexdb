package liveview

// Reading the brain's entity graph.
//
// This lives with the live view rather than beside the static renderer because
// both need it and only one of them can own it. The static renderer keeps the
// call by alias, so there is one query and one set of types behind both
// pictures — two copies would drift, and the first symptom would be two views
// of the same brain that disagree.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// dialTimeout bounds a single read of a shared brain. The poller runs on a
// timer, so a read that hangs past the next tick is worse than one that fails.
const dialTimeout = 15 * time.Second

// RemoteConfigured reports whether this process reads a shared brain.
func RemoteConfigured() (addr, token string, ok bool) {
	addr = strings.TrimSpace(os.Getenv("CORTEXDB_REMOTE"))
	return addr, os.Getenv("CORTEXDB_GRPC_TOKEN"), addr != ""
}

// dial opens a connection to a shared brain, attaching the bearer token when
// one is set.
func dial(addr, token string) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if token != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(
			func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
				invoker grpc.UnaryInvoker, callOpts ...grpc.CallOption) error {
				ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
				return invoker(ctx, method, req, reply, cc, callOpts...)
			}))
	}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect to cortexdb at %s: %w", addr, err)
	}
	return conn, nil
}

type Node struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Label  string `json:"label"`
}

// LoadLocal reads meaningful nodes/edges from the live GraphRAG graph
// (everything except chunk nodes, and the edges between them).
// For large graphs it keeps the most-connected core: nodes are ranked by degree
// and capped, so the view shows the densely-linked hub instead of an arbitrary
// truncation with dangling edges.
func LoadLocal(ctx context.Context, sqlDB *sql.DB) ([]Node, []Edge, error) {
	const (
		maxNodes = 600
		maxScan  = 50000
	)

	// 1. Meaningful edges, and degree per node.
	edgeRows, err := sqlDB.QueryContext(ctx,
		// "next" is dropped only where it wires one chunk to the next, which is
		// document layout. It is also the most natural name for one step
		// following another, and blanket-skipping the type meant a caller who
		// modelled a sequence got its nodes drawn and every link between them
		// silently missing. The endpoints decide, not the label.
		`SELECT e.from_node_id, COALESCE(e.edge_type,''), e.to_node_id
		 FROM graph_edges e
		 JOIN graph_nodes f ON f.id = e.from_node_id
		 JOIN graph_nodes t ON t.id = e.to_node_id
		 WHERE e.edge_type != 'has_chunk'
		   AND COALESCE(f.node_type,'') != 'chunk'
		   AND COALESCE(t.node_type,'') != 'chunk'`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = edgeRows.Close() }()
	type rawEdge struct{ from, etype, to string }
	rawEdges := make([]rawEdge, 0)
	degree := make(map[string]int)
	for edgeRows.Next() {
		var e rawEdge
		if err := edgeRows.Scan(&e.from, &e.etype, &e.to); err != nil {
			return nil, nil, err
		}
		rawEdges = append(rawEdges, e)
		degree[e.from]++
		degree[e.to]++
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, err
	}

	// 2. All non-chunk nodes (bounded), tagged with degree.
	nodeRows, err := sqlDB.QueryContext(ctx,
		`SELECT id, COALESCE(content,''), COALESCE(node_type,'') FROM graph_nodes WHERE node_type != 'chunk' LIMIT ?`, maxScan)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = nodeRows.Close() }()
	type rawNode struct {
		view Node
		deg  int
	}
	all := make([]rawNode, 0)
	for nodeRows.Next() {
		var id, content, ntype string
		if err := nodeRows.Scan(&id, &content, &ntype); err != nil {
			return nil, nil, err
		}
		label := content
		if strings.TrimSpace(label) == "" {
			label = trimNodePrefix(id)
		}
		label = ClipLabel(label)
		all = append(all, rawNode{view: Node{ID: id, Label: label, Type: ntype}, deg: degree[id]})
	}
	if err := nodeRows.Err(); err != nil {
		return nil, nil, err
	}

	// 3. Keep the most-connected nodes.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].deg != all[j].deg {
			return all[i].deg > all[j].deg
		}
		return all[i].view.ID < all[j].view.ID
	})
	if len(all) > maxNodes {
		all = all[:maxNodes]
	}
	shown := make(map[string]struct{}, len(all))
	nodes := make([]Node, 0, len(all))
	for _, n := range all {
		shown[n.view.ID] = struct{}{}
		nodes = append(nodes, n.view)
	}

	// 4. Edges among the shown nodes.
	edges := make([]Edge, 0)
	for _, e := range rawEdges {
		if _, ok := shown[e.from]; !ok {
			continue
		}
		if _, ok := shown[e.to]; !ok {
			continue
		}
		edges = append(edges, Edge{Source: e.from, Target: e.to, Label: e.etype})
	}
	return nodes, edges, nil
}

func trimNodePrefix(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// LoadRemote pulls the whole entity graph from the shared brain.
//
// quiet suppresses the truncation note. A one-shot render should say when it
// only drew part of the brain; the live view calls this every couple of seconds
// and would repeat the same line forever, which turns a useful notice into the
// only thing in the log.
func LoadRemote(ctx context.Context, addr, token string, limit int, quiet bool) ([]Node, []Edge, error) {
	conn, err := dial(addr, token)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	args, err := json.Marshal(cortexdb.GraphListAllRequest{Limit: limit})
	if err != nil {
		return nil, nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	resp, err := rpcv1.NewToolsServiceClient(conn).CallTool(callCtx, &rpcv1.CallToolRequest{
		Name:     "graph_list_all",
		ArgsJson: string(args),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("graph_list_all on %s: %w (is the server new enough?)", addr, err)
	}

	var out cortexdb.GraphListAllResponse
	if err := json.Unmarshal([]byte(resp.GetResultJson()), &out); err != nil {
		return nil, nil, fmt.Errorf("decode graph_list_all: %w", err)
	}
	if out.Truncated && !quiet {
		fmt.Fprintf(os.Stderr, "cortexdb: note: showing the %d most-connected of %d nodes; pass a higher limit for more\n",
			len(out.Nodes), out.TotalNodes)
	}

	nodes := make([]Node, 0, len(out.Nodes))
	for _, n := range out.Nodes {
		nodes = append(nodes, Node{ID: n.ID, Label: ClipLabel(n.Label), Type: n.Type})
	}
	edges := make([]Edge, 0, len(out.Edges))
	for _, e := range out.Edges {
		edges = append(edges, Edge{Source: e.From, Target: e.To, Label: e.Type})
	}
	return nodes, edges, nil
}

// ClipLabel shortens a node label for display, matching the local renderer.
//
// Whitespace is collapsed before clipping. A node's label is often the first
// line of whatever text it came from, and that text has newlines in it — which
// survive into every view that prints the label as a string, breaking the
// layout of whatever panel is showing it.
func ClipLabel(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 48 {
		return string(r[:48]) + "…"
	}
	return s
}
