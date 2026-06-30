package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
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
	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	if outDir == "" {
		outDir = filepath.Join(filepath.Dir(dbPath), "graph")
	}
	ctx := context.Background()

	// Organize first: extract entities + co-occurrence relations from the brain's
	// memories and knowledge into the graph, so the view reflects an organized
	// brain rather than only whatever was explicitly tagged.
	if rep, oerr := graphflow.OrganizeFromBrain(ctx, db, graphflow.OrganizeOptions{
		IncludeMemories:  true,
		IncludeKnowledge: true,
	}); oerr != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: organize graph: %v\n", oerr)
	} else if rep != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: organized %d texts -> %d new entities, %d relations (kept %d/%d candidates)\n",
			rep.DocumentsScanned, rep.EntityCount, rep.RelationCount, rep.CandidatesKept, rep.CandidatesSeen)
	}

	nodes, edges, err := loadBrainGraph(ctx, db.SQL())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: read graph: %v\n", err)
		os.Exit(1)
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

// loadBrainGraph reads meaningful nodes/edges from the live GraphRAG graph:
// everything except chunk nodes, and edges excluding structural has_chunk/next,
// limited to edges whose endpoints are both shown.
func loadBrainGraph(ctx context.Context, sqlDB *sql.DB) ([]graphNodeView, []graphEdgeView, error) {
	const maxNodes = 800

	nodeRows, err := sqlDB.QueryContext(ctx,
		`SELECT id, COALESCE(content,''), COALESCE(node_type,'') FROM graph_nodes WHERE node_type != 'chunk' LIMIT ?`, maxNodes)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = nodeRows.Close() }()

	nodes := make([]graphNodeView, 0)
	shown := make(map[string]struct{})
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
		nodes = append(nodes, graphNodeView{ID: id, Label: label, Type: ntype})
		shown[id] = struct{}{}
	}
	if err := nodeRows.Err(); err != nil {
		return nil, nil, err
	}

	edgeRows, err := sqlDB.QueryContext(ctx,
		`SELECT from_node_id, COALESCE(edge_type,''), to_node_id FROM graph_edges WHERE edge_type NOT IN ('has_chunk','next')`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = edgeRows.Close() }()

	edges := make([]graphEdgeView, 0)
	for edgeRows.Next() {
		var from, etype, to string
		if err := edgeRows.Scan(&from, &etype, &to); err != nil {
			return nil, nil, err
		}
		if _, ok := shown[from]; !ok {
			continue
		}
		if _, ok := shown[to]; !ok {
			continue
		}
		edges = append(edges, graphEdgeView{Source: from, Target: to, Label: etype})
	}
	return nodes, edges, edgeRows.Err()
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
  html,body{margin:0;height:100%;background:#0d1424;color:#e2e8f0;font:14px system-ui,sans-serif}
  #net{width:100%;height:100vh}
  #hud{position:fixed;top:10px;left:12px;background:rgba(13,20,36,.85);padding:8px 12px;border:1px solid #334155;border-radius:8px}
  #hud b{color:#7dd3fc}
</style></head>
<body>
<div id="hud">CortexDB knowledge graph — <b id="ncount">0</b> nodes · <b id="ecount">0</b> edges. Drag to pan, scroll to zoom.</div>
<div id="net"></div>
<script>
  var rawNodes = {{.Nodes}};
  var rawEdges = {{.Edges}};
  var palette = {project:"#38bdf8",entity:"#a78bfa",document:"#f59e0b",person:"#34d399",workspace:"#fb7185",binary:"#94a3b8",component:"#22d3ee",resource:"#facc15"};
  var nodes = rawNodes.map(function(n){return {id:n.id,label:n.label,group:n.type,color:palette[n.type]||"#64748b",
     font:{color:"#e2e8f0",size:13},shape:"dot",size:14};});
  var edges = rawEdges.map(function(e){return {from:e.source,to:e.target,label:e.label,
     font:{color:"#94a3b8",size:10,strokeWidth:0},color:{color:"#475569",opacity:.6},arrows:"to",smooth:{type:"continuous"}};});
  document.getElementById("ncount").textContent = nodes.length;
  document.getElementById("ecount").textContent = edges.length;
  new vis.Network(document.getElementById("net"),
    {nodes:new vis.DataSet(nodes),edges:new vis.DataSet(edges)},
    {physics:{stabilization:true,barnesHut:{gravitationalConstant:-8000,springLength:120}},
     interaction:{hover:true,tooltipDelay:120}});
</script>
</body></html>`))
