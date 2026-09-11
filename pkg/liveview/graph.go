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
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
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
	// Grade is the record's _grade: by what kind of thing its truth is
	// established. One of the knowledge contract's five closed values, or
	// empty for a record no producer stamped — which on a real shelf is most
	// of them, and is a finding rather than a gap.
	//
	// omitempty on purpose: a source that reports no grades puts the same
	// bytes on the wire it did before this field existed.
	Grade string `json:"grade,omitempty"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Label  string `json:"label"`
	// ID is the edge's own row id, carried so the inspector can be asked about
	// it. An edge named only by its two ends and a label is not a record
	// anything can look up — and it is the record that has the source, the
	// chunk and the grade on it.
	//
	// Not part of the edge's identity for diffing: two nodes can be joined by
	// several relations and edgeKey tells those apart by type, which is a
	// question about the graph rather than about the row.
	ID string `json:"id,omitempty"`
	// Grade is the edge's own _grade. An edge is an assertion about two
	// things and carries the contract exactly as a node does — a picture that
	// graded only the nodes would report a shelf far better established than
	// it is, which is the argument graph.PropertyCount already makes.
	Grade string `json:"grade,omitempty"`
}

// gradeExpr is the one spelling of "read this record's _grade".
//
// pkg/cortexdb reads the contract through pkg/graph's property primitives, and
// both of those — PropertyCounts behind ContractTally, RecordsWithProperties
// behind GradedRecords — build the read as
// dialect.JSONTextGuarded("properties", cortexdb.KeyGrade). This is that, with
// the column qualified because the edge query has three tables in its FROM.
// A second spelling would be a second thing to keep in step with the contract,
// and the first symptom would be a page colouring by a grade the panel beneath
// it does not count.
func gradeExpr(d sqldialect.Dialect, column string) string {
	return d.JSONTextGuarded(column, cortexdb.KeyGrade)
}

// graphSource names the rows one read of the entity graph sees: a fragment to
// put in a FROM clause and the arguments it binds ahead of the query's own.
//
// It exists so the present and the past are read by one query rather than two.
// A past read is the same question asked of graph.GraphStore.NodeSource /
// EdgeSource instead of the bare tables — see readAsOfLocal — and two copies
// of this query would be two places for the degree ranking, the chunk filter
// and the grade read to drift apart.
type graphSource struct {
	dialect  sqldialect.Dialect
	nodes    string
	nodeArgs []any
	edges    string
	edgeArgs []any
}

// liveSource reads the tables as they stand now, which is byte-for-byte the
// query this package issued before point-in-time reads existed.
//
// SQLite's dialect, because LoadLocal takes a bare *sql.DB and has always
// bound `?` without rebinding — this names what that already assumed rather
// than narrowing anything. A caller holding a *cortexdb.DB goes through
// readAsOfLocal, which uses the database's own dialect.
func liveSource() graphSource {
	return graphSource{
		dialect: sqldialect.For(sqldialect.SQLite),
		nodes:   "graph_nodes",
		edges:   "graph_edges",
	}
}

// rowQuerier is whatever can run a read — the database handle, or a
// transaction. Both loaders take one rather than a *sql.DB so a caller that
// already has a narrower handle is not forced to widen it.
type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// LoadLocal reads meaningful nodes/edges from the live GraphRAG graph
// (everything except chunk nodes, and the edges between them).
// For large graphs it keeps the most-connected core: nodes are ranked by degree
// and capped, so the view shows the densely-linked hub instead of an arbitrary
// truncation with dangling edges.
func LoadLocal(ctx context.Context, sqlDB *sql.DB) ([]Node, []Edge, error) {
	return loadGraph(ctx, sqlDB, liveSource())
}

// loadGraph is LoadLocal's body, over whichever rows graphSource names.
func loadGraph(ctx context.Context, q rowQuerier, src graphSource) ([]Node, []Edge, error) {
	const (
		maxNodes = 600
		maxScan  = 50000
	)
	d := src.dialect

	// 1. Meaningful edges, their grade, and degree per node.
	edgeArgs := append([]any{}, src.edgeArgs...)
	edgeArgs = append(edgeArgs, src.nodeArgs...)
	edgeArgs = append(edgeArgs, src.nodeArgs...)
	edgeRows, err := q.QueryContext(ctx, d.Rebind(
		// "next" is dropped only where it wires one chunk to the next, which is
		// document layout. It is also the most natural name for one step
		// following another, and blanket-skipping the type meant a caller who
		// modelled a sequence got its nodes drawn and every link between them
		// silently missing. The endpoints decide, not the label.
		`SELECT e.id, e.from_node_id, COALESCE(e.edge_type,''), e.to_node_id, `+gradeExpr(d, "e.properties")+`
		 FROM `+src.edges+` AS e
		 JOIN `+src.nodes+` AS f ON f.id = e.from_node_id
		 JOIN `+src.nodes+` AS t ON t.id = e.to_node_id
		 WHERE e.edge_type != 'has_chunk'
		   AND COALESCE(f.node_type,'') != 'chunk'
		   AND COALESCE(t.node_type,'') != 'chunk'`), edgeArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = edgeRows.Close() }()
	type rawEdge struct{ id, from, etype, to, grade string }
	rawEdges := make([]rawEdge, 0)
	degree := make(map[string]int)
	for edgeRows.Next() {
		var e rawEdge
		// Nullable: the guarded read yields NULL both for a record with no
		// properties and for one whose JSON lacks the key, and those are the
		// same answer — nobody stamped this.
		var grade sql.NullString
		if err := edgeRows.Scan(&e.id, &e.from, &e.etype, &e.to, &grade); err != nil {
			return nil, nil, err
		}
		e.grade = grade.String
		rawEdges = append(rawEdges, e)
		degree[e.from]++
		degree[e.to]++
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, err
	}

	// 2. All non-chunk nodes (bounded), tagged with degree.
	nodeArgs := append([]any{}, src.nodeArgs...)
	nodeArgs = append(nodeArgs, maxScan)
	nodeRows, err := q.QueryContext(ctx, d.Rebind(
		`SELECT n.id, COALESCE(n.content,''), COALESCE(n.node_type,''), `+gradeExpr(d, "n.properties")+
			` FROM `+src.nodes+` AS n WHERE n.node_type != 'chunk' LIMIT ?`), nodeArgs...)
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
		var grade sql.NullString
		if err := nodeRows.Scan(&id, &content, &ntype, &grade); err != nil {
			return nil, nil, err
		}
		label := content
		if strings.TrimSpace(label) == "" {
			label = trimNodePrefix(id)
		}
		label = ClipLabel(label)
		all = append(all, rawNode{
			view: Node{ID: id, Label: label, Type: ntype, Grade: grade.String},
			deg:  degree[id],
		})
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
		edges = append(edges, Edge{Source: e.from, Target: e.to, Label: e.etype, ID: e.id, Grade: e.grade})
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
//
// The nodes and edges come back without a grade: graph_list_all carries an id,
// a label and a type and nothing else, and adding a field to that response is
// a change to the shared brain's wire, not to this page. So a remote source
// declares Grades false and the page says the grades cannot be read here —
// which must not be confused with a shelf on which nothing is graded.
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
