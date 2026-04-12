package graphflow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

type exportBundle struct {
	Nodes    []ExtractionNode `json:"nodes"`
	Edges    []ExtractionEdge `json:"edges"`
	Analysis *AnalysisReport  `json:"analysis,omitempty"`
}

// Export writes a minimal graphflow bundle to disk.
func Export(ctx context.Context, db *cortexdb.DB, req ExportRequest) (*ExportResult, error) {
	if db == nil {
		return nil, fmt.Errorf("db is required")
	}
	if req.OutputDir == "" {
		return nil, fmt.Errorf("output_dir is required")
	}
	nodes, edges, err := loadGraphflowSubgraph(ctx, db.Graph())
	if err != nil {
		return nil, err
	}
	bundle := exportBundle{
		Nodes:    make([]ExtractionNode, 0, len(nodes)),
		Edges:    make([]ExtractionEdge, 0, len(edges)),
		Analysis: req.Analysis,
	}
	for _, node := range nodes {
		bundle.Nodes = append(bundle.Nodes, ExtractionNode{
			ID:             stringValue(node.Properties["node_id"], node.ID),
			Label:          node.Content,
			Type:           node.NodeType,
			Summary:        stringValue(node.Properties["summary"], ""),
			SourceFile:     stringValue(node.Properties["source_file"], ""),
			SourceLocation: stringValue(node.Properties["source_location"], ""),
		})
	}
	for _, edge := range edges {
		bundle.Edges = append(bundle.Edges, ExtractionEdge{
			Source:         stringsTrimPrefix(edge.FromNodeID, "graphflow:node:"),
			Target:         stringsTrimPrefix(edge.ToNodeID, "graphflow:node:"),
			Relation:       edge.EdgeType,
			Confidence:     Confidence(stringValue(edge.Properties["confidence"], string(ConfidenceExtracted))),
			Directed:       boolValue(edge.Properties["directed"]),
			SourceFile:     stringValue(edge.Properties["source_file"], ""),
			SourceLocation: stringValue(edge.Properties["source_location"], ""),
		})
	}

	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return nil, err
	}
	graphJSON := filepath.Join(req.OutputDir, "graph.json")
	reportMD := filepath.Join(req.OutputDir, "GRAPH_REPORT.md")

	payload, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(graphJSON, append(payload, '\n'), 0o644); err != nil {
		return nil, err
	}
	if req.Report == "" && req.Analysis != nil {
		if req.Report, err = RenderReport(ctx, req.Analysis); err != nil {
			return nil, err
		}
	}
	if req.Report == "" {
		req.Report = "# GRAPHFLOW_REPORT\n"
	}
	if err := os.WriteFile(reportMD, []byte(req.Report), 0o644); err != nil {
		return nil, err
	}

	return &ExportResult{
		OutputDir:      req.OutputDir,
		GraphJSON:      graphJSON,
		ReportMarkdown: reportMD,
	}, nil
}

func stringValue(raw any, fallback string) string {
	if value, ok := raw.(string); ok && value != "" {
		return value
	}
	return fallback
}

func boolValue(raw any) bool {
	value, _ := raw.(bool)
	return value
}

func stringsTrimPrefix(value, prefix string) string {
	if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
		return value[len(prefix):]
	}
	return value
}

// htmlVisualization embeds graph data in the HTML document and loads visualization
// libraries from a CDN at runtime.
const htmlVisualization = `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>CortexDB GraphFlow Visualization</title>
	<script src="https://unpkg.com/cytoscape@3.28.1/dist/cytoscape.min.js"></script>
	<script src="https://unpkg.com/dagre@0.8.5/dist/dagre.min.js"></script>
	<script src="https://unpkg.com/cytoscape-dagre@2.5.0/cytoscape-dagre.js"></script>
	<style>
		*{box-sizing:border-box;margin:0;padding:0}
		body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;display:flex;height:100vh;background:#f5f7fa}
		#toolbar{position:fixed;top:12px;left:12px;z-index:100;display:flex;flex-direction:column;gap:8px;background:white;padding:12px;border-radius:8px;box-shadow:0 2px 12px rgba(0,0,0,0.15);min-width:200px}
		#toolbar h3{font-size:13px;color:#666;margin-bottom:4px}
		#toolbar input,#toolbar select{padding:6px 10px;border:1px solid #ddd;border-radius:4px;font-size:13px;width:100%}
		#toolbar .btn-row{display:flex;gap:6px}
		#toolbar button{flex:1;padding:6px 10px;border:none;border-radius:4px;cursor:pointer;font-size:12px;transition:background 0.2s}
		#toolbar .btn-primary{background:#4f46e5;color:white}
		#toolbar .btn-primary:hover{background:#4338ca}
		#toolbar .btn-secondary{background:#e5e7eb;color:#374151}
		#toolbar .btn-secondary:hover{background:#d1d5db}
		#stats{font-size:11px;color:#888;margin-top:4px}
		#sidebar{position:fixed;top:12px;right:12px;width:300px;max-height:calc(100vh - 24px);background:white;border-radius:8px;box-shadow:0 2px 12px rgba(0,0,0,0.15);display:none;overflow:hidden;flex-direction:column}
		#sidebar.visible{display:flex}
		#sidebar-header{padding:12px;border-bottom:1px solid #eee;display:flex;justify-content:space-between;align-items:center}
		#sidebar-header h3{font-size:14px}
		#sidebar-close{background:none;border:none;font-size:20px;cursor:pointer;color:#999}
		#sidebar-close:hover{color:#333}
		#sidebar-content{padding:12px;overflow-y:auto;flex:1}
		.detail-section{margin-bottom:16px}
		.detail-label{font-size:10px;color:#888;text-transform:uppercase;margin-bottom:4px}
		.detail-value{font-size:13px;color:#333;word-break:break-word}
		.detail-value.type-node{color:#4f46e5;font-weight:500}
		.detail-value.type-edge{color:#059669;font-weight:500}
		#legend{position:fixed;bottom:12px;left:12px;background:white;padding:10px 14px;border-radius:6px;box-shadow:0 2px 8px rgba(0,0,0,0.12);font-size:11px;z-index:100}
		#legend-title{font-weight:600;margin-bottom:6px;color:#444}
		.legend-item{display:flex;align-items:center;gap:8px;margin-bottom:3px}
		.legend-color{width:12px;height:12px;border-radius:3px}
		#cy{width:100vw;height:100vh}
		.filter-section{margin-top:8px}
		.filter-section label{font-size:11px;color:#666}
	</style>
</head>
<body>
	<div id="toolbar">
		<h3>Search</h3>
		<input type="text" id="searchInput" placeholder="Search nodes...">
		<h3>Layout</h3>
		<select id="layoutSelect">
			<option value="cose">Force Directed (COSE)</option>
			<option value="dagre">Hierarchical (Dagre)</option>
			<option value="circle">Circle</option>
			<option value="grid">Grid</option>
			<option value="breadthfirst">Tree</option>
		</select>
		<div class="btn-row">
			<button class="btn-primary" onclick="applyLayout()">Apply</button>
			<button class="btn-secondary" onclick="fitToScreen()">Fit</button>
		</div>
		<div class="filter-section"><label>Filter:</label>
			<select id="typeFilter"><option value="">All types</option></select>
		</div>
		<button class="btn-secondary" style="margin-top:8px" onclick="exportPNG()">Export PNG</button>
		<div id="stats"></div>
	</div>
	<div id="sidebar">
		<div id="sidebar-header"><h3 id="sidebar-title">Details</h3><button id="sidebar-close" onclick="closeSidebar()">&times;</button></div>
		<div id="sidebar-content"></div>
	</div>
	<div id="legend"><div id="legend-title">Node Types</div><div id="legend-items"></div></div>
	<div id="cy"></div>
	<script>
		var NC={'Person':'#4f46e5','Organization':'#059669','Location':'#d97706','Concept':'#7c3aed','Event':'#dc2626','Document':'#0891b2','default':'#6b7280'};
		var cy;
		function gc(t){return t&&NC[t]?NC[t]:NC.default}
		function initCy(nodes,edges){
			var els=[];
			nodes.forEach(function(n){els.push({data:{id:n.id,label:n.label||n.id,type:n.type||'default',summary:n.summary||'',source:n.source_file||''}})});
			edges.forEach(function(e){els.push({data:{id:e.Metadata&&e.Metadata.edge_id||(e.source+'-'+e.target),source:e.source,target:e.target,label:e.relation||'',confidence:e.confidence||'EXTRACTED'}})});
			cy=cytoscape({container:document.getElementById('cy'),elements:els,
				style:[
					{selector:'node',style:{label:'data(label)','background-color':'data(type)',color:'#333','font-size':'11px','text-valign':'bottom','text-margin-y':6,width:30,height:30,'border-width':2,'border-color':'white'}},
					{selector:'edge',style:{width:1.5,'line-color':'#9ca3af','target-arrow-color':'#9ca3af','target-arrow-shape':'triangle','curve-style':'bezier',label:'data(label)','font-size':'9px','text-background-color':'#f5f7fa','text-background-opacity':0.8}},
					{selector:'node:selected',style:{'border-width':3,'border-color':'#4f46e5'}},
					{selector:'edge:selected',style:{'line-color':'#4f46e5','target-arrow-color':'#4f46e5'}},
					{selector:'.highlighted',style:{'border-width':3,'border-color':'#f59e0b'}},
					{selector:'.dimmed',style:{opacity:0.3}}
				],
				layout:{name:'cose',animate:false,padding:50}
			});
			cy.on('tap','node',function(e){var n=e.target;document.getElementById('sidebar-title').textContent=n.data('label');
				var html='<div class="detail-section"><div class="detail-label">Type</div><div class="detail-value type-node">'+n.data('type')+'</div></div><div class="detail-section"><div class="detail-label">ID</div><div class="detail-value">'+n.id()+'</div></div>';
				if(n.data('summary'))html+='<div class="detail-section"><div class="detail-label">Summary</div><div class="detail-value">'+n.data('summary')+'</div></div>';
				if(n.data('source'))html+='<div class="detail-section"><div class="detail-label">Source</div><div class="detail-value">'+n.data('source')+'</div></div>';
				html+='<div class="detail-section"><div class="detail-label">Connections</div><div class="detail-value">'+n.degree()+'</div></div>';
				document.getElementById('sidebar-content').innerHTML=html;document.getElementById('sidebar').classList.add('visible');
				cy.elements().removeClass('dimmed highlighted');n.connectedEdges().addClass('highlighted');n.neighborhood('node').addClass('highlighted');cy.elements().not('.highlighted').not(n).not(n.neighborhood()).addClass('dimmed')});
			cy.on('tap','edge',function(e){var ed=e.target;document.getElementById('sidebar-title').textContent=ed.data('label')||'Edge';
				document.getElementById('sidebar-content').innerHTML='<div class="detail-section"><div class="detail-label">Relation</div><div class="detail-value type-edge">'+ed.data('label')+'</div></div><div class="detail-section"><div class="detail-label">From</div><div class="detail-value">'+ed.source().id()+'</div></div><div class="detail-section"><div class="detail-label">To</div><div class="detail-value">'+ed.target().id()+'</div></div><div class="detail-section"><div class="detail-label">Confidence</div><div class="detail-value">'+ed.data('confidence')+'</div></div>';document.getElementById('sidebar').classList.add('visible')});
			cy.on('tap',function(e){if(e.target===cy)closeSidebar()});
			document.getElementById('stats').textContent=nodes.length+' nodes, '+edges.length+' edges';
			var types=[...new Set(nodes.map(function(n){return n.type||'default'}))];
			document.getElementById('legend-items').innerHTML=types.map(function(t){return'<div class="legend-item"><div class="legend-color" style="background:'+gc(t)+'"></div>'+t+'</div>'}).join('');
			document.getElementById('typeFilter').innerHTML='<option value="">All types</option>'+types.map(function(t){return'<option value="'+t+'">'+t+'</option>'}).join('')}
		function closeSidebar(){document.getElementById('sidebar').classList.remove('visible');cy.elements().removeClass('dimmed highlighted')}
		function applyLayout(){var ln=document.getElementById('layoutSelect').value;var l=ln==='dagre'?{name:'dagre',animate:true,animationDuration:500,nodeSep:80,rankSep:100,padding:50}:{name:ln,animate:true,animationDuration:500,padding:50};cy.layout(l).run()}
		function fitToScreen(){cy.fit(50)}
		function exportPNG(){var png=cy.png({full:true,scale:2,bg:'#f5f7fa'});var a=document.createElement('a');a.href=png;a.download='graph.png';a.click()}
		document.getElementById('searchInput').addEventListener('input',function(e){var q=e.target.value.toLowerCase();if(!q){cy.elements().removeClass('dimmed highlighted');return}cy.elements().addClass('dimmed');cy.nodes().forEach(function(n){if(n.data('label').toLowerCase().includes(q)||n.id().toLowerCase().includes(q)){n.removeClass('dimmed').addClass('highlighted')}})});
		document.getElementById('typeFilter').addEventListener('change',function(e){var t=e.target.value;if(!t){cy.nodes().removeClass('dimmed');return}cy.nodes().addClass('dimmed');cy.nodes('[type="'+t+'"]').removeClass('dimmed')});
		var GRAPH_DATA=JSON.parse(atob('GRAPH_JSON_PLACEHOLDER'));
		initCy(GRAPH_DATA.nodes,GRAPH_DATA.edges);
	</script>
</body>
</html>`

// ExportHTML generates an HTML visualization of the graph.
// It embeds the graph data directly in the HTML file and loads Cytoscape/Dagre
// from a CDN at runtime. Open the HTML file in any modern browser to view it.
func ExportHTML(ctx context.Context, db *cortexdb.DB, req ExportRequest) (*ExportResult, error) {
	if db == nil {
		return nil, fmt.Errorf("db is required")
	}
	if req.OutputDir == "" {
		return nil, fmt.Errorf("output_dir is required")
	}

	// Load graph data
	nodes, edges, err := loadGraphflowSubgraph(ctx, db.Graph())
	if err != nil {
		return nil, err
	}

	// Build extraction nodes and edges for export
	exportNodes := make([]ExtractionNode, 0, len(nodes))
	exportEdges := make([]ExtractionEdge, 0, len(edges))

	nodeIDs := make(map[string]bool)
	for _, node := range nodes {
		exportNodes = append(exportNodes, ExtractionNode{
			ID:             stringValue(node.Properties["node_id"], node.ID),
			Label:          node.Content,
			Type:           node.NodeType,
			Summary:        stringValue(node.Properties["summary"], ""),
			SourceFile:     stringValue(node.Properties["source_file"], ""),
			SourceLocation: stringValue(node.Properties["source_location"], ""),
		})
		nodeIDs[node.ID] = true
	}

	var edgeID int
	for _, edge := range edges {
		src := stringsTrimPrefix(edge.FromNodeID, "graphflow:node:")
		tgt := stringsTrimPrefix(edge.ToNodeID, "graphflow:node:")
		if !nodeIDs[edge.FromNodeID] && !nodeIDs["graphflow:node:"+src] {
			src = edge.FromNodeID
		}
		if !nodeIDs[edge.ToNodeID] && !nodeIDs["graphflow:node:"+tgt] {
			tgt = edge.ToNodeID
		}
		edgeID++
		eID := fmt.Sprintf("e%d", edgeID)
		exportEdges = append(exportEdges, ExtractionEdge{
			Source:         src,
			Target:         tgt,
			Relation:       edge.EdgeType,
			Confidence:     Confidence(stringValue(edge.Properties["confidence"], string(ConfidenceExtracted))),
			Directed:       boolValue(edge.Properties["directed"]),
			SourceFile:     stringValue(edge.Properties["source_file"], ""),
			SourceLocation: stringValue(edge.Properties["source_location"], ""),
			Metadata:       map[string]string{"edge_id": eID},
		})
	}

	// Marshal graph data
	graphData := map[string]interface{}{
		"nodes": exportNodes,
		"edges": exportEdges,
	}
	dataJSON, err := json.MarshalIndent(graphData, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal graph data: %w", err)
	}

	// Create output directory
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return nil, err
	}

	// Generate HTML with embedded data - use base64 encoding for safety
	b64Data := base64.StdEncoding.EncodeToString(dataJSON)
	htmlContent := strings.Replace(htmlVisualization, "GRAPH_JSON_PLACEHOLDER", b64Data, 1)

	htmlPath := filepath.Join(req.OutputDir, "graph.html")
	if err := os.WriteFile(htmlPath, []byte(htmlContent), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write HTML: %w", err)
	}

	// Also write separate graph.json for programmatic access
	graphJSONPath := filepath.Join(req.OutputDir, "graph.json")
	if err := os.WriteFile(graphJSONPath, append(dataJSON, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write graph.json: %w", err)
	}

	result := &ExportResult{
		OutputDir: req.OutputDir,
		GraphJSON: graphJSONPath,
		GraphHTML: htmlPath,
	}
	return result, nil
}
