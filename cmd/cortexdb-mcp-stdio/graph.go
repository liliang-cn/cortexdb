package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// runGraphHTML organizes the brain (extract entities/relations from memories +
// knowledge) and renders the resulting knowledge graph to a self-contained,
// interactive HTML file, printing its path. One-shot mode behind `--graph-html`,
// used by /cortexdb-graph.
//
// The renderer reads the actual GraphRAG graph (graph_nodes / graph_edges) — not
// graphflow's separate analysis namespace — so the view reflects the real brain.
// Chunk nodes and structural edges (has_chunk/next) are filtered out for a clean
// entity graph.
func runGraphHTML(outDir string) {
	ctx := context.Background()
	var (
		nodes  []graphNodeView
		edges  []graphEdgeView
		source string
	)

	if addr, token, ok := remoteConfigured(); ok {
		// Shared brain: read the graph over gRPC. Organizing is deliberately
		// skipped — it rewrites the graph, and a read-only view of someone
		// else's brain should not mutate it from whichever machine rendered it.
		var err error
		nodes, edges, err = fetchGraphRemote(ctx, addr, token, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
			os.Exit(1)
		}
		source = "shared brain " + addr
		if outDir == "" {
			outDir = defaultViewDir("graph")
		}
	} else {
		dbPath := os.Getenv("CORTEXDB_PATH")
		if dbPath == "" {
			dbPath = cortexdb.DefaultDBPath()
		}
		db, err := openBrainDB(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: open %s: %v\n", dbPath, err)
			os.Exit(1)
		}
		defer func() { _ = db.Close() }()
		source = dbPath
		if outDir == "" {
			outDir = filepath.Join(filepath.Dir(dbPath), "graph")
		}

		// Organize first: extract entities + relations from the brain's memories and
		// knowledge into the graph, so the view reflects an organized brain rather
		// than only whatever was explicitly tagged. With CORTEXDB_LLM_* set, an LLM
		// distills clean, typed entities and relations; otherwise it is deterministic.
		llm := newOrganizeLLM()
		if llm != nil {
			fmt.Fprintln(os.Stderr, "cortexdb: organizing graph with LLM distillation (CORTEXDB_LLM_*)")
		}
		if rep, oerr := graphflow.OrganizeFromBrain(ctx, db, graphflow.OrganizeOptions{
			IncludeMemories:  true,
			IncludeKnowledge: true,
			LLM:              llm,
		}); oerr != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: organize graph: %v\n", oerr)
		} else if rep != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: organized %d texts -> %d new entities, %d relations (kept %d/%d candidates)\n",
				rep.DocumentsScanned, rep.EntityCount, rep.RelationCount, rep.CandidatesKept, rep.CandidatesSeen)
		}

		nodes, edges, err = loadBrainGraph(ctx, db.SQL())
		if err != nil {
			fmt.Fprintf(os.Stderr, "cortexdb: read graph: %v\n", err)
			os.Exit(1)
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: create %s: %v\n", outDir, err)
		os.Exit(1)
	}
	htmlPath := filepath.Join(outDir, "graph.html")
	if err := writeBrainGraphHTML(htmlPath, nodes, edges); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: write html: %v\n", err)
		os.Exit(1)
	}
	if abs, aerr := filepath.Abs(htmlPath); aerr == nil {
		htmlPath = abs
	}
	fmt.Fprintf(os.Stderr, "cortexdb: read from %s\n", source)
	fmt.Fprintf(os.Stderr, "cortexdb: graph has %d nodes, %d edges\n", len(nodes), len(edges))
	fmt.Println(htmlPath)
}

type graphNodeView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type graphEdgeView struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Label  string `json:"label"`
}

// loadBrainGraph reads meaningful nodes/edges from the live GraphRAG graph
// (everything except chunk nodes; edges excluding structural has_chunk/next).
// For large graphs it keeps the most-connected core: nodes are ranked by degree
// and capped, so the view shows the densely-linked hub instead of an arbitrary
// truncation with dangling edges.
func loadBrainGraph(ctx context.Context, sqlDB *sql.DB) ([]graphNodeView, []graphEdgeView, error) {
	const (
		maxNodes = 600
		maxScan  = 50000
	)

	// 1. Meaningful edges, and degree per node.
	edgeRows, err := sqlDB.QueryContext(ctx,
		`SELECT from_node_id, COALESCE(edge_type,''), to_node_id FROM graph_edges WHERE edge_type NOT IN ('has_chunk','next')`)
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
		view graphNodeView
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
		if r := []rune(label); len(r) > 48 {
			label = string(r[:48]) + "…"
		}
		all = append(all, rawNode{view: graphNodeView{ID: id, Label: label, Type: ntype}, deg: degree[id]})
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
	nodes := make([]graphNodeView, 0, len(all))
	for _, n := range all {
		shown[n.view.ID] = struct{}{}
		nodes = append(nodes, n.view)
	}

	// 4. Edges among the shown nodes.
	edges := make([]graphEdgeView, 0)
	for _, e := range rawEdges {
		if _, ok := shown[e.from]; !ok {
			continue
		}
		if _, ok := shown[e.to]; !ok {
			continue
		}
		edges = append(edges, graphEdgeView{Source: e.from, Target: e.to, Label: e.etype})
	}
	return nodes, edges, nil
}

func trimNodePrefix(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

func writeBrainGraphHTML(path string, nodes []graphNodeView, edges []graphEdgeView) error {
	nodesJSON, err := json.Marshal(nodes)
	if err != nil {
		return err
	}
	edgesJSON, err := json.Marshal(edges)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return brainGraphTemplate.Execute(f, map[string]template.JS{
		"Nodes": template.JS(nodesJSON),
		"Edges": template.JS(edgesJSON),
	})
}

// brainGraphTemplate renders an interactive force-directed graph with
// vis-network (loaded from a CDN). Self-contained otherwise.
var brainGraphTemplate = template.Must(template.New("graph").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>CortexDB knowledge graph</title>
<script src="https://unpkg.com/vis-network@9.1.9/standalone/umd/vis-network.min.js"></script>
<style>
  html,body{margin:0;height:100%;background:#ffffff;color:#0f172a;font:14px system-ui,-apple-system,sans-serif}
  #net{width:100%;height:100vh}
  #hud{position:fixed;top:10px;left:12px;background:rgba(255,255,255,.94);padding:8px 12px;border:1px solid #e2e8f0;border-radius:8px;box-shadow:0 1px 3px rgba(15,23,42,.08)}
  #hud b{color:#2563eb}
</style></head>
<body>
<div id="hud">CortexDB knowledge graph — <b id="ncount">0</b> nodes · <b id="ecount">0</b> edges. Drag to pan, scroll to zoom.</div>
<div id="net"></div>
<script>
  var rawNodes = {{.Nodes}};
  var rawEdges = {{.Edges}};
  var palette = {project:"#2563eb",entity:"#7c3aed",document:"#d97706",person:"#059669",workspace:"#e11d48",binary:"#64748b",component:"#0891b2",resource:"#ca8a04"};
  var nodes = rawNodes.map(function(n){return {id:n.id,label:n.label,group:n.type,color:palette[n.type]||"#475569",
     font:{color:"#0f172a",size:13},shape:"dot",size:14};});
  var edges = rawEdges.map(function(e){return {from:e.source,to:e.target,label:e.label,
     font:{color:"#64748b",size:10,strokeWidth:3,strokeColor:"#ffffff"},color:{color:"#cbd5e1",opacity:.9},arrows:"to",smooth:{type:"continuous"}};});
  document.getElementById("ncount").textContent = nodes.length;
  document.getElementById("ecount").textContent = edges.length;
  new vis.Network(document.getElementById("net"),
    {nodes:new vis.DataSet(nodes),edges:new vis.DataSet(edges)},
    {physics:{stabilization:true,barnesHut:{gravitationalConstant:-8000,springLength:120}},
     interaction:{hover:true,tooltipDelay:120}});
</script>
</body></html>`))
