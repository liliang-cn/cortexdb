package liveview

// Exploring past what the scene draws.
//
// The scene is the store's most-connected core — 600 nodes from a local file,
// 2,000 from a shared brain — and on a real brain that is a few percent of it.
// Everything outside the core used to be unreachable from the page: the search
// box highlighted names already on screen, and a node at the edge of the core
// had no way to show what it connects to beyond it. A picture of a brain that
// can only be looked at, not asked, is the weak part of this page.
//
// So the page can now ask, through four routes that each answer one question:
//
//   - /api/find — where is a thing called X, anywhere in the store?
//   - /api/expand — what does this node connect to, including what the core
//     left out?
//   - /api/ask — what does the brain know about this question, and which of
//     its things does the answer touch?
//   - /api/cypher — the reader's own question, in a graph query language.
//
// Each is answered by one of the brain's own tools, run through
// [Source.Call] — the toolbox for a local file, the gRPC ToolsService for a
// shared brain — so the page asks the same questions an agent asks, with the
// same answers, and a shared brain needs nothing new on the server.
//
// Read-only is the page's contract, and it is held here rather than trusted to
// the routes: [ExploreTools] is the whole list of tools a Source may be asked
// to run on the page's behalf, every one of them is a read in the toolbox's
// own catalogue (a test holds the list against ToolDefinitions' Mutates), and
// the callers this package builds refuse any other name before the request
// leaves the process. SPARQL is not on it: knowledge_graph_query also runs
// SPARQL Update, so the catalogue counts it as a write, and a read-only page
// must not be one argument away from writing.
//
// The tools answer in their own shapes — nodes with vectors, chunks with
// scores — which is more than a browser should be sent and less than it can
// draw. Every route here reshapes its answer into the page's own [Node] and
// [Edge], filtered of the store's bookkeeping the way the scene is, so what
// arrives can be merged into the scene without a second vocabulary.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// ExploreTools is every tool a Source may be asked to run on the page's
// behalf. All of them read; see the package comment on explore.go.
var ExploreTools = []string{
	"find_nodes",
	"get_nodes",
	"expand_graph",
	"graph_cypher_query",
	"knowledge_memory_recall",
	"get_chunks",
	"fact_provenance",
}

// Caller runs one of the brain's tools with JSON arguments and returns its
// JSON answer. [Source.Call] is one.
type Caller func(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error)

const (
	// exploreTimeout bounds one question. A query the reader typed can be as
	// slow as they made it; the page should hear that it was, not hang.
	exploreTimeout = 20 * time.Second
	// exploreBodyLimit bounds a posted question. A question, not a document.
	exploreBodyLimit = 16 << 10
	// Result bounds: what one answer may add to a scene already holding
	// thousands of nodes.
	findLimit      = 25
	expandDefault  = 80
	expandMax      = 300
	cypherMaxRows  = 200
	askMemories    = 6
	askKnowledge   = 6
	askTextRunes   = 360
	cypherCellRune = 120
)

// errNotExplorable is the answer of a source that cannot be asked.
var errNotExplorable = errors.New("this source cannot be explored")

func exploreAllowed(tool string) bool { return slices.Contains(ExploreTools, tool) }

// guardCaller refuses any tool not on ExploreTools before call runs.
func guardCaller(call Caller) Caller {
	return func(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
		if !exploreAllowed(tool) {
			return nil, fmt.Errorf("%s is not a tool the view may run", tool)
		}
		return call(ctx, tool, args)
	}
}

// localCaller runs tools through an open brain's toolbox.
func localCaller(db *cortexdb.DB) Caller {
	return guardCaller(func(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
		out, err := db.GraphRAGTools().Call(ctx, tool, args)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	})
}

// remoteCaller runs tools on a shared brain over its ToolsService.
func remoteCaller(addr, token string) Caller {
	return guardCaller(func(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
		conn, err := dial(addr, token)
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", addr, err)
		}
		defer func() { _ = conn.Close() }()
		resp, err := rpcv1.NewToolsServiceClient(conn).CallTool(ctx, &rpcv1.CallToolRequest{Name: tool, ArgsJson: string(args)})
		if err != nil {
			return nil, err
		}
		return json.RawMessage(resp.GetResultJson()), nil
	})
}

// callInto runs tool with args and decodes its answer into out.
func callInto(ctx context.Context, call Caller, tool string, args, out any) error {
	if call == nil {
		return errNotExplorable
	}
	body, err := json.Marshal(args)
	if err != nil {
		return err
	}
	raw, err := call(ctx, tool, body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s: %w", tool, err)
	}
	return nil
}

// bookkeeping reports whether a node or edge type is the store's own filing,
// which the scene leaves out and so must every answer merged into it.
func bookkeepingNode(nodeType string) bool {
	return slices.Contains(graph.BookkeepingNodeTypes, nodeType)
}
func bookkeepingEdge(edgeType string) bool {
	return slices.Contains(graph.BookkeepingEdgeTypes, edgeType)
}

// viewNode reshapes a stored node into the page's.
func viewNode(n *graph.GraphNode) Node {
	label := n.Content
	if name, ok := n.Properties["name"].(string); ok && strings.TrimSpace(name) != "" {
		label = name
	}
	if strings.TrimSpace(label) == "" {
		label = trimNodePrefix(n.ID)
	}
	grade, _ := n.Properties[cortexdb.KeyGrade].(string)
	return Node{ID: n.ID, Label: ClipLabel(label), Type: n.NodeType, Grade: grade}
}

// viewGraph reshapes stored nodes and edges, dropping the bookkeeping and
// every edge whose ends were dropped or not returned.
func viewGraph(nodes []*graph.GraphNode, edges []*graph.GraphEdge) ([]Node, []Edge) {
	kept := make(map[string]bool, len(nodes))
	outN := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || bookkeepingNode(n.NodeType) || kept[n.ID] {
			continue
		}
		kept[n.ID] = true
		outN = append(outN, viewNode(n))
	}
	outE := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if e == nil || bookkeepingEdge(e.EdgeType) || !kept[e.FromNodeID] || !kept[e.ToNodeID] {
			continue
		}
		grade, _ := e.Properties[cortexdb.KeyGrade].(string)
		outE = append(outE, Edge{ID: e.ID, Source: e.FromNodeID, Target: e.ToNodeID, Label: e.EdgeType, Grade: grade})
	}
	return outN, outE
}

// FindResult is one node /api/find found, and how its name matched.
type FindResult struct {
	Node
	Match string `json:"match,omitempty"`
}

// Find looks a name up across the whole store, not only the drawn core.
func Find(ctx context.Context, call Caller, q string) ([]FindResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("find: nothing to look for")
	}
	var resp cortexdb.ToolFindNodesResponse
	if err := callInto(ctx, call, "find_nodes", cortexdb.ToolFindNodesRequest{Names: []string{q}, Limit: findLimit}, &resp); err != nil {
		return nil, err
	}
	out := make([]FindResult, 0)
	seen := map[string]bool{}
	for _, m := range resp.Matches {
		for _, n := range m.Nodes {
			if n == nil || bookkeepingNode(n.NodeType) || seen[n.ID] {
				continue
			}
			seen[n.ID] = true
			out = append(out, FindResult{Node: viewNode(n), Match: m.Match})
			if len(out) == findLimit {
				return out, nil
			}
		}
	}
	return out, nil
}

// Neighbourhood is what /api/expand answers: a node, what it connects to,
// and the relations between them.
type Neighbourhood struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Expand reads one node's neighbourhood, one hop out, whatever the core drew.
//
// Asked as Cypher, not through expand_graph, because of what a hub on a real
// brain is connected to. expand_graph spends its limit on whatever it meets
// first, and for a project the shared brain talks about constantly that is
// eighty memories linked by mentions — bookkeeping the scene does not draw —
// so the answer was eighty dots and no relations at all. The query names the
// bookkeeping it skips, so the limit is spent on relations. expand_graph is
// the fallback for a store too old to run Cypher.
func Expand(ctx context.Context, call Caller, id string, limit int) (Neighbourhood, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Neighbourhood{}, errors.New("expand: no node")
	}
	if limit <= 0 {
		limit = expandDefault
	}
	limit = min(limit, expandMax)
	var cy cortexdb.CypherQueryResponse
	err := callInto(ctx, call, "graph_cypher_query", cortexdb.CypherQueryRequest{
		Query:     fmt.Sprintf("MATCH (n)-[r]-(m) WHERE id(n) = $id AND NOT type(r) IN $skip RETURN n, r, m LIMIT %d", limit),
		Params:    map[string]any{"id": id, "skip": graph.BookkeepingEdgeTypes},
		MaxRows:   limit,
		TimeoutMS: int(exploreTimeout / time.Millisecond),
	}, &cy)
	if err == nil {
		nb := neighbourhoodFromRows(cy.Rows)
		if len(nb.Nodes) == 0 {
			// A node with no relations still exists, and the page asked for it
			// to fly there: without it the neighbourhood is empty and the page
			// can only say there is no such node.
			nb.Nodes = anchorNode(ctx, call, id)
		}
		return nb, nil
	}
	var resp cortexdb.ToolExpandGraphResponse
	if err := callInto(ctx, call, "expand_graph", cortexdb.ToolExpandGraphRequest{NodeIDs: []string{id}, MaxHops: 1, Limit: limit}, &resp); err != nil {
		return Neighbourhood{}, err
	}
	nodes, edges := viewGraph(resp.Nodes, resp.Edges)
	if len(nodes) == 0 {
		nodes = anchorNode(ctx, call, id)
	}
	return Neighbourhood{Nodes: nodes, Edges: edges}, nil
}

// anchorNode is the expanded node on its own, for one with no relations to
// expand: nil if the brain has no such node.
func anchorNode(ctx context.Context, call Caller, id string) []Node {
	var got cortexdb.ToolGetNodesResponse
	if err := callInto(ctx, call, "get_nodes", cortexdb.ToolGetNodesRequest{NodeIDs: []string{id}}, &got); err != nil {
		return nil
	}
	nodes, _ := viewGraph(got.Nodes, nil)
	return nodes
}

// neighbourhoodFromRows reads the nodes and relations out of Cypher rows,
// keeping only relations whose two ends both came back and are drawable.
func neighbourhoodFromRows(rows [][]any) Neighbourhood {
	out := Neighbourhood{Nodes: []Node{}, Edges: []Edge{}}
	kept, seenEdge := map[string]bool{}, map[string]bool{}
	var rels []Edge
	for _, row := range rows {
		for _, v := range row {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if n, ok := cypherNode(m); ok && !kept[n.ID] && !bookkeepingNode(n.Type) {
				kept[n.ID] = true
				out.Nodes = append(out.Nodes, n)
			} else if e, ok := cypherRel(m); ok && !seenEdge[e.ID] && !bookkeepingEdge(e.Label) {
				seenEdge[e.ID] = true
				rels = append(rels, e)
			}
		}
	}
	for _, e := range rels {
		if kept[e.Source] && kept[e.Target] {
			out.Edges = append(out.Edges, e)
		}
	}
	return out
}

// AskAnswer is what /api/ask answers: what the brain recalled, as text with
// its ids, and the nodes in the scene the answer touches.
type AskAnswer struct {
	Memories []AskText                           `json:"memories"`
	Passages []AskText                           `json:"passages"`
	Facts    []cortexdb.KnowledgeMemoryGraphFact `json:"facts"`
	// Touched is every node id the answer names — fact ends and the entities
	// recall resolved — for the page to light up and, when outside the core,
	// to fetch.
	Touched []string `json:"touched"`
	Mode    string   `json:"mode,omitempty"`
}

// AskText is one recalled item.
type AskText struct {
	ID    string  `json:"id"`
	Title string  `json:"title,omitempty"`
	Text  string  `json:"text"`
	Score float64 `json:"score,omitempty"`
}

// Ask recalls what the brain knows about a question.
func Ask(ctx context.Context, call Caller, q string) (AskAnswer, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return AskAnswer{}, errors.New("ask: no question")
	}
	var resp cortexdb.KnowledgeMemoryRecallResponse
	if err := callInto(ctx, call, "knowledge_memory_recall", cortexdb.KnowledgeMemoryRecallRequest{
		Query: q, TopKMemories: askMemories, TopKKnowledge: askKnowledge,
	}, &resp); err != nil {
		return AskAnswer{}, err
	}
	out := AskAnswer{Memories: []AskText{}, Passages: []AskText{}, Facts: []cortexdb.KnowledgeMemoryGraphFact{}, Touched: []string{},
		Mode: resp.MemoryDecision.EffectiveMode}
	for _, h := range resp.Memories {
		out.Memories = append(out.Memories, AskText{ID: h.Memory.ID, Text: clipRunes(h.Memory.Content, askTextRunes), Score: h.Score})
	}
	for _, h := range resp.Knowledge {
		out.Passages = append(out.Passages, AskText{ID: h.KnowledgeID, Title: h.Title, Text: clipRunes(h.Snippet, askTextRunes), Score: h.Score})
	}
	seen := map[string]bool{}
	touch := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out.Touched = append(out.Touched, id)
		}
	}
	for _, f := range resp.GraphFacts {
		out.Facts = append(out.Facts, f)
		touch(f.SubjectID)
		touch(f.ObjectID)
	}
	for _, name := range resp.Entities {
		touch(cortexdb.EntityNodeID(name))
	}
	return out, nil
}

// CypherAnswer is what /api/cypher answers: the table, rendered for reading,
// and the nodes and relations it returned, for drawing.
type CypherAnswer struct {
	Columns   []string   `json:"columns"`
	Rows      [][]string `json:"rows"`
	RowCount  int        `json:"row_count"`
	Truncated bool       `json:"truncated,omitempty"`
	Nodes     []Node     `json:"nodes"`
	Edges     []Edge     `json:"edges"`
}

// Cypher runs a read-only Cypher query.
func Cypher(ctx context.Context, call Caller, query string) (CypherAnswer, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return CypherAnswer{}, errors.New("cypher: no query")
	}
	var resp cortexdb.CypherQueryResponse
	if err := callInto(ctx, call, "graph_cypher_query", cortexdb.CypherQueryRequest{
		Query: query, MaxRows: cypherMaxRows, TimeoutMS: int(exploreTimeout / time.Millisecond),
	}, &resp); err != nil {
		return CypherAnswer{}, err
	}
	out := CypherAnswer{Columns: resp.Columns, Rows: make([][]string, 0, len(resp.Rows)), RowCount: resp.RowCount,
		Truncated: resp.Truncated, Nodes: []Node{}, Edges: []Edge{}}
	nodeSeen, edgeSeen := map[string]bool{}, map[string]bool{}
	var collect func(v any)
	collect = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if n, ok := cypherNode(x); ok && !nodeSeen[n.ID] && !bookkeepingNode(n.Type) {
				nodeSeen[n.ID] = true
				out.Nodes = append(out.Nodes, n)
			}
			if e, ok := cypherRel(x); ok && !edgeSeen[e.ID] && !bookkeepingEdge(e.Label) {
				edgeSeen[e.ID] = true
				out.Edges = append(out.Edges, e)
			}
			for _, inner := range x {
				collect(inner)
			}
		case []any:
			for _, inner := range x {
				collect(inner)
			}
		}
	}
	for _, row := range resp.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = clipRunes(cypherCell(v), cypherCellRune)
			collect(v)
		}
		out.Rows = append(out.Rows, cells)
	}
	return out, nil
}

// cypherNode reads a result cell as a node: {id, labels, content, properties}.
func cypherNode(m map[string]any) (Node, bool) {
	id, _ := m["id"].(string)
	labels, hasLabels := m["labels"].([]any)
	if id == "" || !hasLabels {
		return Node{}, false
	}
	n := Node{ID: id}
	if len(labels) > 0 {
		n.Type, _ = labels[0].(string)
	}
	props, _ := m["properties"].(map[string]any)
	label, _ := props["name"].(string)
	if strings.TrimSpace(label) == "" {
		label, _ = m["content"].(string)
	}
	if strings.TrimSpace(label) == "" {
		label = trimNodePrefix(id)
	}
	n.Label = ClipLabel(label)
	n.Grade, _ = props[cortexdb.KeyGrade].(string)
	return n, true
}

// cypherRel reads a result cell as a relation: {id, type, start, end}.
func cypherRel(m map[string]any) (Edge, bool) {
	id, _ := m["id"].(string)
	start, _ := m["start"].(string)
	end, _ := m["end"].(string)
	typ, hasType := m["type"].(string)
	if id == "" || start == "" || end == "" || !hasType {
		return Edge{}, false
	}
	return Edge{ID: id, Source: start, Target: end, Label: typ}, true
}

// cypherCell renders one result value for a table.
func cypherCell(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case map[string]any:
		if n, ok := cypherNode(x); ok {
			if n.Type != "" {
				return "(" + n.Label + ":" + n.Type + ")"
			}
			return "(" + n.Label + ")"
		}
		if e, ok := cypherRel(x); ok {
			return "-[:" + e.Label + "]->"
		}
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func clipRunes(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// callerRecord answers the inspector through tools, for a source that cannot
// read the tables — a shared brain. The node's own properties carry the
// contract, so this is the same reading localRecord does, from get_nodes; a
// relation's provenance comes from fact_provenance, which reports its ends and
// the text it was drawn from but not its properties, so a relation's grade is
// not shown and the panel says so.
func callerRecord(call Caller) func(context.Context, string) (RecordDetail, error) {
	return func(ctx context.Context, id string) (RecordDetail, error) {
		id = strings.TrimSpace(id)
		if id == "" {
			return RecordDetail{}, fmt.Errorf("record: no id")
		}
		out := RecordDetail{Available: true, ID: id}

		var nodes cortexdb.ToolGetNodesResponse
		if err := callInto(ctx, call, "get_nodes", cortexdb.ToolGetNodesRequest{NodeIDs: []string{id}}, &nodes); err != nil {
			return RecordDetail{}, fmt.Errorf("record: %w", err)
		}
		if len(nodes.Nodes) == 1 && nodes.Nodes[0] != nil {
			n := nodes.Nodes[0]
			out.Found = true
			out.Type = n.NodeType
			out.Content = n.Content
			out.ValidFrom = rfc3339OrEmpty(n.ValidFrom)
			readContractKeys(&out, n.Properties)
			out.DocumentID = propText(n.Properties, "document_id")
			out.ChunkIDs = propStrings(n.Properties, "chunk_ids")
			if out.DocumentID == "" {
				if docs := propStrings(n.Properties, "source_document_ids"); len(docs) > 0 {
					out.DocumentID = docs[0]
				}
			}
			callerRecordChunks(ctx, call, &out)
			return out, nil
		}

		var prov cortexdb.ToolFactProvenanceResponse
		if err := callInto(ctx, call, "fact_provenance", cortexdb.ToolFactProvenanceRequest{EdgeID: id, WithText: true}, &prov); err != nil || prov.Provenance.EdgeID == "" {
			return notFoundRecord(id), nil
		}
		p := prov.Provenance
		out.Found, out.Edge = true, true
		out.Type, out.From, out.To = p.Type, p.From, p.To
		out.DocumentID, out.ChunkIDs, out.MissingChunks = p.DocumentID, p.ChunkIDs, p.Missing
		out.Inferred, out.Rule, out.Source = p.Inferred, p.Rule, p.Source
		for _, c := range p.Chunks {
			if len(out.Text) >= recordChunkLimit {
				break
			}
			out.Text = append(out.Text, RecordText{ChunkID: c.ID, DocumentID: c.DocumentID, Content: c.Content})
		}
		out.Notes = append(out.Notes, "read through the shared brain's tools, which do not return a relation's properties: its grade is not shown")
		return out, nil
	}
}

// callerRecordChunks fetches a node's supporting text through get_chunks.
func callerRecordChunks(ctx context.Context, call Caller, out *RecordDetail) {
	if len(out.ChunkIDs) == 0 {
		return
	}
	want := out.ChunkIDs
	if len(want) > recordChunkLimit {
		want = want[:recordChunkLimit]
	}
	var got cortexdb.ToolGetChunksResponse
	if err := callInto(ctx, call, "get_chunks", cortexdb.ToolGetChunksRequest{ChunkIDs: want, DisableGraph: true}, &got); err != nil {
		out.Notes = append(out.Notes, "supporting text: "+err.Error())
		return
	}
	for _, c := range got.Chunks {
		out.Text = append(out.Text, RecordText{ChunkID: c.ID, DocumentID: c.DocumentID, Content: c.Content})
	}
}

// ---------- routes ----------

func (s *Server) exploreContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), exploreTimeout)
}

// writeExplore answers a route: the value, or the error as a sentence.
func writeExplore(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errNotExplorable) {
			code = http.StatusNotImplemented
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// readQuestion reads a POSTed {"q": ...} or {"query": ...}.
func readQuestion(r *http.Request) (string, error) {
	if r.Method != http.MethodPost {
		return "", fmt.Errorf("POST a JSON body")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, exploreBodyLimit+1))
	if err != nil {
		return "", err
	}
	if len(body) > exploreBodyLimit {
		return "", fmt.Errorf("question longer than %d bytes", exploreBodyLimit)
	}
	var in struct {
		Q     string `json:"q"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return "", fmt.Errorf("body: %w", err)
	}
	if in.Q != "" {
		return in.Q, nil
	}
	return in.Query, nil
}

func (s *Server) handleFind(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	res, err := Find(ctx, s.src.Call, r.URL.Query().Get("q"))
	writeExplore(w, map[string]any{"results": res}, err)
}

func (s *Server) handleExpand(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	res, err := Expand(ctx, s.src.Call, r.URL.Query().Get("id"), limit)
	writeExplore(w, res, err)
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	q, err := readQuestion(r)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	res, err := Ask(ctx, s.src.Call, q)
	writeExplore(w, res, err)
}

func (s *Server) handleCypher(w http.ResponseWriter, r *http.Request) {
	q, err := readQuestion(r)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	res, err := Cypher(ctx, s.src.Call, q)
	writeExplore(w, res, err)
}

func writeBadRequest(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
