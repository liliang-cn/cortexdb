package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/liveview"
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
// graphHTMLResult is what a render produced: where it landed, and how much of
// the brain it shows.
type graphHTMLResult struct {
	Path   string `json:"path"`
	Nodes  int    `json:"nodes"`
	Edges  int    `json:"edges"`
	Source string `json:"source"`
}

// renderGraphHTML builds the view and returns where it wrote it.
//
// Errors are returned rather than fatal so the same code can serve both the
// one-shot CLI and the render_graph_html MCP tool; an agent asking for a view
// must get a message it can act on, not a dead process.
func renderGraphHTML(ctx context.Context, outDir string, organize bool) (*graphHTMLResult, error) {
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
		nodes, edges, err = fetchGraphRemote(ctx, addr, token, 0, false)
		if err != nil {
			return nil, err
		}
		source = "shared brain " + addr
		if outDir == "" {
			outDir = graphViewDir()
		}
	} else {
		dbPath := os.Getenv("CORTEXDB_PATH")
		if dbPath == "" {
			dbPath = cortexdb.DefaultDBPath()
		}
		db, err := openBrainDB(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", dbPath, err)
		}
		defer func() { _ = db.Close() }()
		source = dbPath
		if outDir == "" {
			outDir = filepath.Join(filepath.Dir(dbPath), "graph")
		}

		// Organizing writes to the graph, so rendering a view no longer does it by
		// default: the deterministic extractor types everything it finds as a
		// generic "entity", and running it on every view slowly filled the graph
		// with capitalized words lifted out of prose. Ask for it with --organize,
		// or let an agent write typed entities through memory_save/knowledge_save.
		if organize {
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
		}

		nodes, edges, err = loadBrainGraph(ctx, db.SQL())
		if err != nil {
			return nil, fmt.Errorf("read graph: %w", err)
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", outDir, err)
	}
	htmlPath := filepath.Join(outDir, "graph.html")
	if err := writeBrainGraphHTML(htmlPath, nodes, edges); err != nil {
		return nil, fmt.Errorf("write html: %w", err)
	}
	if abs, aerr := filepath.Abs(htmlPath); aerr == nil {
		htmlPath = abs
	}
	return &graphHTMLResult{Path: htmlPath, Nodes: len(nodes), Edges: len(edges), Source: source}, nil
}

// runGraphHTML organizes the brain (extract entities/relations from memories +
// knowledge) and renders the resulting knowledge graph to a self-contained,
// interactive HTML file, printing its path. One-shot mode behind `--graph-html`,
// used by /cortexdb-graph.
//
// The renderer reads the actual GraphRAG graph (graph_nodes / graph_edges) — not
// graphflow's separate analysis namespace — so the view reflects the real brain.
// Chunk nodes and structural edges (has_chunk/next) are filtered out for a clean
// entity graph.
func runGraphHTML(outDir string, organize bool) {
	res, err := renderGraphHTML(context.Background(), outDir, organize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "cortexdb: read from %s\n", res.Source)
	fmt.Fprintf(os.Stderr, "cortexdb: graph has %d nodes, %d edges\n", res.Nodes, res.Edges)
	fmt.Println(res.Path)
}

// The live view owns the graph types and the query that fills them, and the
// static renderer borrows both. One reader, one shape: two copies of "what
// counts as a node" would drift, and the first symptom would be two pictures of
// the same brain that disagree.
type graphNodeView = liveview.Node
type graphEdgeView = liveview.Edge

func loadBrainGraph(ctx context.Context, sqlDB *sql.DB) ([]graphNodeView, []graphEdgeView, error) {
	return liveview.LoadLocal(ctx, sqlDB)
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
  #loading{position:fixed;inset:0;display:flex;align-items:center;justify-content:center;
    background:#ffffff;z-index:10;transition:opacity .3s;flex-direction:column;gap:14px}
  #loading.done{opacity:0;pointer-events:none}
  .spin{width:28px;height:28px;border:3px solid #e2e8f0;border-top-color:#2563eb;border-radius:50%;animation:sp .8s linear infinite}
  @keyframes sp{to{transform:rotate(360deg)}}
  #ltext{color:#64748b;font-size:13px}
  #lbar{width:220px;height:4px;background:#e2e8f0;border-radius:2px;overflow:hidden}
  #lbar>span{display:block;height:100%;width:0;background:#2563eb;transition:width .2s}
</style></head>
<body>
<div id="hud">CortexDB knowledge graph — <b id="ncount">0</b> nodes · <b id="ecount">0</b> edges. Drag to pan, scroll to zoom.</div>
<div id="net"></div>
<div id="loading">
  <div class="spin"></div>
  <div id="ltext">Loading graph…</div>
  <div id="lbar"><span id="lfill"></span></div>
</div>
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
  var loadingEl=document.getElementById("loading"), fillEl=document.getElementById("lfill"), textEl=document.getElementById("ltext");
  textEl.textContent = "Laying out "+nodes.length+" nodes…";
  function doneLoading(){ loadingEl.classList.add("done"); setTimeout(function(){loadingEl.style.display="none";},350); }

  var network = new vis.Network(document.getElementById("net"),
    {nodes:new vis.DataSet(nodes),edges:new vis.DataSet(edges)},
    {physics:{stabilization:true,barnesHut:{gravitationalConstant:-8000,springLength:120}},
     interaction:{hover:true,tooltipDelay:120}});

  // Physics stabilization is the slow part on a large graph, and it reports
  // real progress — show it rather than a blank canvas.
  network.on("stabilizationProgress", function(p){
    if(!p || !p.total) return;
    fillEl.style.width = Math.round(p.iterations/p.total*100)+"%";
  });
  network.once("stabilizationIterationsDone", function(){ fillEl.style.width="100%"; doneLoading(); });
  // Safety net: never leave the overlay up if the event never arrives.
  setTimeout(doneLoading, 20000);
</script>
</body></html>`))
