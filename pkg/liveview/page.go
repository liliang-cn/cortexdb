package liveview

// pageHTML is the view itself.
//
// Three deliberate choices, in the order they matter:
//
//   - Glow is a bloom pass, not a painted halo. Bloom reads the rendered frame,
//     so brightness earns the glow: a hub with forty edges is drawn bigger and
//     therefore blooms wider, and a node the brain just touched blooms because
//     it went white, not because something drew a ring around it. A halo
//     texture would have to be told all of that.
//   - Paths carry moving light. A static line says two things are related; a
//     line with something travelling along it says which way, and turns the
//     graph from a diagram into something with a direction of flow.
//   - The camera is never taken away from you. Auto-orbit is a toggle that
//     yields the moment you drag, because a view that keeps spinning while
//     someone is trying to look at one node is a view they have to fight.
//
// The bloom pass is loaded as an ES module from a CDN, pinned to the same three
// revision the graph library bundles, and its failure is survivable: if it does
// not arrive the scene still draws, still moves and still answers, only flatter.
// Everything that makes the page *work* is in the classic script that runs
// first, and everything in the module is decoration.
const pageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CortexDB — live brain</title>
<script src="https://unpkg.com/3d-force-graph@1.73.4/dist/3d-force-graph.min.js"></script>
<style>
  *{box-sizing:border-box;margin:0;padding:0}
  html,body{height:100%;overflow:hidden;background:#04060d;color:#c7d2e5;
    font:13px/1.5 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',sans-serif}
  #scene{position:absolute;inset:0}
  .panel{position:fixed;z-index:10;background:rgba(8,13,26,.86);border:1px solid #1b2740;
    border-radius:10px;backdrop-filter:blur(10px);box-shadow:0 8px 30px rgba(0,0,0,.5)}
  #head{top:14px;left:14px;padding:11px 14px;min-width:250px}
  #head h1{font-size:13px;font-weight:600;color:#e8eefc;letter-spacing:.2px}
  #counts{font-size:11px;color:#64748b;margin-top:5px}
  #counts b{color:#7dd3fc;font-weight:600}
  .badge{display:inline-flex;align-items:center;gap:5px;font-size:10px;padding:2px 7px;border-radius:20px;
    border:1px solid #1e3a5f;color:#7dd3fc;margin-top:7px;text-transform:uppercase;letter-spacing:.05em}
  .badge .led{width:6px;height:6px;border-radius:50%;background:#22d3ee;box-shadow:0 0 8px #22d3ee}
  .badge.cold{border-color:#334155;color:#64748b}
  .badge.cold .led{background:#475569;box-shadow:none;animation:none}
  .led{animation:blink 2s ease-in-out infinite}
  @keyframes blink{0%,100%{opacity:1}50%{opacity:.35}}
  #tools{top:14px;right:14px;padding:11px;width:222px;display:flex;flex-direction:column;gap:8px}
  #tools h3{font-size:10px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76;font-weight:600}
  #tools input,#tools select{width:100%;padding:6px 9px;background:#060b16;border:1px solid #23324f;
    border-radius:6px;color:#dbe6f7;font-size:12px;font-family:inherit}
  #tools input:focus,#tools select:focus{outline:none;border-color:#3b82f6}
  .row{display:flex;gap:6px}
  button{flex:1;padding:6px 8px;background:#141f36;border:1px solid #23324f;border-radius:6px;
    color:#a9bcd8;font-size:11px;cursor:pointer;font-family:inherit;transition:.15s}
  button:hover{background:#1d2c49;color:#e2ecfb}
  button.on{background:#1d4ed8;border-color:#3b82f6;color:#fff;box-shadow:0 0 14px rgba(59,130,246,.45)}
  #pathinfo{font-size:11px;color:#64748b;min-height:16px}
  #pathinfo b{color:#fbbf24}
  #legend{bottom:14px;left:14px;padding:10px 12px;max-height:38vh;overflow:auto;font-size:11px}
  #legend h3{font-size:10px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76;margin-bottom:7px}
  .li{display:flex;align-items:center;gap:7px;margin-bottom:4px;color:#94a3b8}
  .dot{width:9px;height:9px;border-radius:50%;flex:none}
  #feed{bottom:14px;right:14px;width:330px;max-height:40vh;padding:10px 12px;display:flex;flex-direction:column}
  #feed h3{font-size:10px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76;margin-bottom:7px;flex:none}
  #feeditems{overflow:auto;display:flex;flex-direction:column-reverse;gap:1px}
  .ev{display:flex;gap:8px;align-items:baseline;padding:3px 0;font-size:11px;
    animation:slide .35s ease-out;border-bottom:1px solid rgba(30,41,59,.5)}
  @keyframes slide{from{opacity:0;transform:translateX(10px)}to{opacity:1;transform:none}}
  .ev .k{flex:none;width:9px;height:9px;border-radius:2px;margin-top:4px}
  .ev .body{flex:1;min-width:0}
  .ev .t{color:#cbd5e1;word-break:break-word}
  .ev .m{color:#475569;font-size:10px}
  .ev.failed .t{color:#f87171;text-decoration:line-through}
  #detail{top:14px;right:250px;width:280px;padding:12px;display:none}
  #detail.on{display:block}
  #detail .t{font-size:14px;font-weight:600;color:#e8eefc;word-break:break-word;padding-right:16px}
  #detail .x{position:absolute;top:8px;right:11px;cursor:pointer;color:#475569;font-size:17px;line-height:1}
  #detail .x:hover{color:#cbd5e1}
  #detail .r{margin-top:9px}
  #detail .k{font-size:9px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76}
  #detail .v{font-size:12px;color:#cbd5e1;word-break:break-word}
  #boot{position:fixed;inset:0;z-index:40;display:flex;align-items:center;justify-content:center;
    flex-direction:column;gap:14px;background:#04060d;transition:opacity .45s}
  #boot.gone{opacity:0;pointer-events:none}
  .spin{width:26px;height:26px;border:2px solid #1b2740;border-top-color:#3b82f6;border-radius:50%;
    animation:sp .8s linear infinite}
  @keyframes sp{to{transform:rotate(360deg)}}
  #boot span{color:#475569;font-size:12px}
</style>
</head>
<body>
<div id="scene"></div>

<div class="panel" id="head">
  <h1>CortexDB — live brain</h1>
  <div id="counts"><b id="n">0</b> nodes · <b id="e">0</b> edges</div>
  <div class="badge" id="live"><span class="led"></span><span id="livetext">connecting</span></div>
</div>

<div class="panel" id="tools">
  <h3>Find</h3>
  <input id="q" placeholder="Highlight by name…" autocomplete="off">
  <h3>Type</h3>
  <select id="type"><option value="">All types</option></select>
  <h3>Path</h3>
  <div class="row"><button id="pathbtn">Trace path</button><button id="clearpath">Clear</button></div>
  <div id="pathinfo"></div>
  <h3>View</h3>
  <div class="row"><button id="spin">Orbit</button><button id="fit">Fit</button></div>
  <div class="row"><button id="glow" class="on">Glow</button><button id="flow" class="on">Flow</button></div>
</div>

<div class="panel" id="legend"><h3>Node types</h3><div id="legenditems"></div></div>

<div class="panel" id="feed"><h3>Activity</h3><div id="feeditems"></div></div>

<div class="panel" id="detail"><span class="x" onclick="closeDetail()">&times;</span>
  <div class="t" id="dt"></div><div id="db"></div></div>

<div id="boot"><div class="spin"></div><span id="boottext">Reading the brain…</span></div>

<script>
/* ---------- state ---------- */
var G = null;                 // the force graph
var byId = {};                // id -> node object (the live ones the layout owns)
var linkKeys = {};            // "from|type|to" -> link object
var adj = {};                 // id -> [{to, link}]  undirected, for path tracing
var flash = {};               // id -> {until, color} nodes lit by activity
var pathNodes = {}, pathLinks = {};
var pickMode = false, pickA = null;
var query = "";
var linkTotal = 0;
var glowOn = true, flowOn = true;
var spinning = false, spinAngle = 0, spinTimer = null;
var activityLive = false;
var userMovedCamera = false;

var NAMED = {entity:"#38bdf8", concept:"#a78bfa", memory:"#34d399", knowledge:"#fbbf24",
  document:"#fb923c", person:"#f472b6", project:"#60a5fa", organization:"#2dd4bf",
  location:"#f59e0b", event:"#f87171", chunk:"#475569"};
function colorOf(t){
  if(!t) return "#7c8ba1";
  if(NAMED[t]) return NAMED[t];
  var h=0; for(var i=0;i<t.length;i++) h=(h*31+t.charCodeAt(i))%360;
  return "hsl("+h+",70%,65%)";
}
function keyOf(l){
  var s=typeof l.source==="object"?l.source.id:l.source;
  var t=typeof l.target==="object"?l.target.id:l.target;
  return s+"|"+(l.label||"")+"|"+t;
}
function esc(s){return String(s==null?"":s).replace(/[&<>"]/g,function(c){
  return {"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;"}[c];});}

/* ---------- the scene ---------- */
G = ForceGraph3D()(document.getElementById("scene"))
  .backgroundColor("#04060d")
  .showNavInfo(false)
  .nodeLabel(function(n){
    return "<div style='background:#0a1120;border:1px solid #23324f;border-radius:6px;padding:4px 9px;" +
      "font:12px system-ui;color:#e2e8f0'>" + esc(n.label) +
      (n.type ? " <span style='color:#64748b'>" + esc(n.type) + "</span>" : "") + "</div>";
  })
  .nodeColor(nodeColor)
  .nodeVal(nodeVal)
  .nodeOpacity(0.95)
  .nodeResolution(8)
  .linkColor(linkColor)
  .linkWidth(linkWidth)
  .linkOpacity(0.32)
  .linkDirectionalParticles(linkParticles)
  .linkDirectionalParticleWidth(1.8)
  .linkDirectionalParticleSpeed(0.006)
  .linkDirectionalParticleColor(linkParticleColor)
  .linkDirectionalArrowLength(linkArrow)
  .linkDirectionalArrowRelPos(1)
  .linkDirectionalArrowColor(function(){ return "#3b6ea5"; })
  .onNodeClick(onNodeClick)
  .onBackgroundClick(function(){ closeDetail(); });

// The layout is allowed to cool. Leaving it warm forever costs a full force
// simulation over every node and link on every frame, which on a real brain —
// thousands of both — is most of the frame budget, and buys nothing while
// nothing is arriving. Instead it settles, and a delta that brings new nodes
// reheats it just enough for them to find a place among the ones already there.
G.d3VelocityDecay(0.32);
// Repulsion with no maximum range is what flings a brain apart. Every node
// pushes every other one however far away it is, so the handful with no edges
// have nothing pulling back and drift until the interesting part is a speck in
// the middle of empty space. Capping the range lets neighbourhoods spread
// without the whole graph inflating, and it is cheaper besides.
G.d3Force("charge").strength(-42).distanceMax(340);

/* ---------- accessors ---------- */
// Degree drives size, so hubs are physically bigger and therefore bloom wider.
function degreeOf(id){ return (adj[id]||[]).length; }
function nodeVal(n){
  var base = 1.1 + Math.min(6, degreeOf(n.id)*0.42);
  var f = flash[n.id];
  if(f){
    var k = (f.until - Date.now())/f.span;      // 1 at the strike, 0 at the end
    if(k>0) base *= 1 + 2.2*k*k;
  }
  if(pathNodes[n.id]) base *= 1.5;
  return base;
}
function nodeColor(n){
  var f = flash[n.id];
  if(f && f.until > Date.now()) return f.color;
  if(Object.keys(pathNodes).length) return pathNodes[n.id] ? "#fde68a" : "#131c2e";
  if(query) return matches(n) ? colorOf(n.type) : "#121a2b";
  return colorOf(n.type);
}
function matches(n){
  return (n.label||"").toLowerCase().indexOf(query) >= 0 ||
         (n.id||"").toLowerCase().indexOf(query) >= 0;
}
function linkColor(l){
  if(Object.keys(pathLinks).length) return pathLinks[keyOf(l)] ? "#fbbf24" : "#0d1526";
  return l.__hot && l.__hot > Date.now() ? "#93c5fd" : "#2b5289";
}
function linkWidth(l){
  if(pathLinks[keyOf(l)]) return 2.4;
  return l.__hot && l.__hot > Date.now() ? 1.6 : 0.5;
}
// Particles are the flow. The path gets a dense stream, a link that was just
// written gets a burst, everything else gets a trickle — so the eye is drawn
// to what is happening without the rest going dead.
// Arrowheads and particles are each a separate object the renderer draws, and
// on a real brain there are thousands of links. Measured on this page against a
// 2000-node graph: an arrowhead on every link cost 17fps, and dropping them to
// only the links that matter gave 65 — the difference between a scene you can
// turn and one you fight. So past a threshold both are spent where they say
// something: the traced path, and links the brain just wrote. Under it, every
// link gets both, because a small graph can afford to show its whole shape.
var DETAIL_EVERYWHERE_UNDER = 900;
function detailed(l){
  return pathLinks[keyOf(l)] || (l.__hot && l.__hot > Date.now()) ||
    linkTotal <= DETAIL_EVERYWHERE_UNDER;
}
function linkArrow(l){ return detailed(l) ? 2.6 : 0; }
function linkParticles(l){
  if(!flowOn) return 0;
  if(pathLinks[keyOf(l)]) return 6;
  if(l.__hot && l.__hot > Date.now()) return 5;
  return linkTotal <= DETAIL_EVERYWHERE_UNDER ? 1 : 0;
}
function linkParticleColor(l){
  if(pathLinks[keyOf(l)]) return "#fde68a";
  if(l.__hot && l.__hot > Date.now()) return "#bfdbfe";
  return "#3b82f6";
}

/* ---------- repaint ----------
   Accessors are re-set rather than the data replaced: handing the graph the
   same arrays back tells it to re-read colours and sizes while leaving every
   node exactly where the layout put it. Replacing graphData() would restart
   the simulation and throw the whole picture in the air on every heartbeat. */
var repaintQueued = false;
function repaint(){
  if(repaintQueued) return;
  repaintQueued = true;
  requestAnimationFrame(function(){
    repaintQueued = false;
    G.nodeColor(nodeColor).nodeVal(nodeVal)
     .linkColor(linkColor).linkWidth(linkWidth)
     .linkDirectionalArrowLength(linkArrow)
     .linkDirectionalParticles(linkParticles).linkDirectionalParticleColor(linkParticleColor);
  });
}
// Flashes decay, so the scene has to be repainted while any are alive — but
// only while they are, not forever on a timer.
setInterval(function(){
  var now = Date.now(), live = false;
  for(var id in flash){ if(flash[id].until > now) live = true; else delete flash[id]; }
  if(live) repaint();
}, 90);

/* ---------- data ---------- */
function applySnapshot(p){
  byId = {}; linkKeys = {};
  var nodes = p.nodes.map(function(n){ return {id:n.id, label:n.label||n.id, type:n.type||""}; });
  nodes.forEach(function(n){ byId[n.id]=n; });
  var links = (p.edges||[]).filter(function(e){ return byId[e.source] && byId[e.target]; })
    .map(function(e){ return {source:e.source, target:e.target, label:e.label||""}; });
  links.forEach(function(l){ linkKeys[keyOf(l)] = l; });
  rebuildAdj(nodes, links);
  linkTotal = links.length;
  G.graphData({nodes:nodes, links:links});
  setCounts(nodes.length, links.length);
  rebuildLegend(nodes);
}

// A delta is applied to the arrays the graph is already simulating, so nodes
// that did not change keep their position and their velocity. Only what is new
// has to find a place.
function applyDelta(d){
  var data = G.graphData();
  var nodes = data.nodes, links = data.links;
  var fresh = [];

  (d.added_nodes||[]).forEach(function(n){
    var ex = byId[n.id];
    if(ex){ ex.label = n.label||ex.label; ex.type = n.type||ex.type; return; }
    var node = {id:n.id, label:n.label||n.id, type:n.type||""};
    // Newcomers enter near the middle rather than at the origin, so they drift
    // into the structure instead of erupting from one point.
    node.x = (Math.random()-0.5)*60; node.y = (Math.random()-0.5)*60; node.z = (Math.random()-0.5)*60;
    byId[n.id] = node; nodes.push(node); fresh.push(node);
  });

  if((d.removed_nodes||[]).length){
    var gone = {};
    d.removed_nodes.forEach(function(id){ gone[id]=true; delete byId[id]; });
    nodes = nodes.filter(function(n){ return !gone[n.id]; });
    links = links.filter(function(l){
      var s=typeof l.source==="object"?l.source.id:l.source, t=typeof l.target==="object"?l.target.id:l.target;
      var drop = gone[s]||gone[t];
      if(drop) delete linkKeys[keyOf(l)];
      return !drop;
    });
  }

  (d.added_edges||[]).forEach(function(e){
    if(!byId[e.source] || !byId[e.target]) return;
    var k = e.source+"|"+(e.label||"")+"|"+e.target;
    if(linkKeys[k]) return;
    var link = {source:e.source, target:e.target, label:e.label||"", __hot:Date.now()+6000};
    linkKeys[k] = link; links.push(link);
  });
  (d.removed_edges||[]).forEach(function(e){
    var k = e.source+"|"+(e.label||"")+"|"+e.target;
    if(!linkKeys[k]) return;
    delete linkKeys[k];
    links = links.filter(function(l){ return keyOf(l) !== k; });
  });

  rebuildAdj(nodes, links);
  linkTotal = links.length;
  G.graphData({nodes:nodes, links:links});
  // Only a real arrival is worth disturbing a settled layout for.
  if(fresh.length) G.d3ReheatSimulation();
  setCounts(typeof d.nodes==="number"?d.nodes:nodes.length, typeof d.edges==="number"?d.edges:links.length);
  rebuildLegend(nodes);
  // Anything that just arrived announces itself in white and cools to its own
  // colour — the visual difference between "this is here" and "this just
  // happened", which a static graph cannot make.
  fresh.forEach(function(n){ strike(n.id, "#ffffff", 4200); });
  if(fresh.length) pushEvent({kind:"structure", text:fresh.length===1 ?
    fresh[0].label + " appeared" : fresh.length + " nodes appeared", tool:"graph", at:Date.now()});
}

function rebuildAdj(nodes, links){
  adj = {};
  nodes.forEach(function(n){ adj[n.id] = []; });
  links.forEach(function(l){
    var s=typeof l.source==="object"?l.source.id:l.source, t=typeof l.target==="object"?l.target.id:l.target;
    if(adj[s]) adj[s].push({to:t, link:l});
    if(adj[t]) adj[t].push({to:s, link:l});
  });
}

function setCounts(n,e){
  document.getElementById("n").textContent = n;
  document.getElementById("e").textContent = e;
}

function rebuildLegend(nodes){
  var seen = {}, types = [];
  nodes.forEach(function(n){ var t=n.type||"(untyped)"; if(!seen[t]){seen[t]=true;types.push(t);} });
  types.sort();
  document.getElementById("legenditems").innerHTML = types.map(function(t){
    return "<div class='li'><span class='dot' style='background:" +
      colorOf(t==="(untyped)"?"":t) + "'></span>" + esc(t) + "</div>";
  }).join("");
  var sel = document.getElementById("type"), keep = sel.value;
  sel.innerHTML = "<option value=''>All types</option>" + types.map(function(t){
    return "<option value='" + esc(t) + "'>" + esc(t) + "</option>"; }).join("");
  sel.value = keep;
}

/* ---------- activity ---------- */
// strike lights one node for a while. Colour carries the kind: a query is cyan,
// a write is green, a relation is amber, a new node is white.
function strike(id, color, ms){
  if(!byId[id]) return;
  flash[id] = {until: Date.now()+ms, span: ms, color: color};
  repaint();
}

var KIND_COLOR = {query:"#22d3ee", write:"#34d399", relate:"#fbbf24", structure:"#e2e8f0"};

// Terms come from a tool's arguments, which name things the way a person does;
// the graph keys them the way a database does. So the match cannot be an exact
// join — but it must not be a free-for-all either. A loose substring rule on a
// common word lights half the graph, and a pulse that covers half the graph
// says nothing about where the brain actually looked.
//
// So exact name matches are taken first and always; substring matches only fill
// in when a term named nothing exactly, and are capped hard. A term that hits
// nothing simply lights nothing, which is the right failure for a highlight.
var TERM_FUZZY_CAP = 6, TERM_TOTAL_CAP = 24;
function lightTerms(terms, color){
  if(!terms) return;
  var total = 0;
  terms.forEach(function(term){
    if(total >= TERM_TOTAL_CAP) return;
    var t = String(term).toLowerCase();
    if(t.length < 2) return;
    var exact = [], fuzzy = [];
    for(var id in byId){
      var lbl = (byId[id].label||"").toLowerCase();
      if(lbl === t){ exact.push(id); continue; }
      if(fuzzy.length < TERM_FUZZY_CAP && t.length >= 4 &&
         (lbl.indexOf(t) >= 0 || id.toLowerCase().indexOf(t) >= 0)) fuzzy.push(id);
    }
    var hits = exact.length ? exact : fuzzy;
    hits.slice(0, TERM_TOTAL_CAP - total).forEach(function(id){ strike(id, color, 3200); total++; });
  });
}

function onActivity(ev){
  var color = KIND_COLOR[ev.kind] || "#94a3b8";
  lightTerms(ev.terms, color);
  // A relation names both ends, so its edge can be found and set flowing even
  // before the poller has seen it in the database.
  (ev.links||[]).forEach(function(pair){
    var from = findNodeByName(pair[0]), to = findNodeByName(pair[1]);
    if(!from || !to) return;
    (adj[from]||[]).forEach(function(a){ if(a.to===to) a.link.__hot = Date.now()+6000; });
  });
  pushEvent(ev);
  repaint();
}

function findNodeByName(name){
  var t = String(name).toLowerCase();
  for(var id in byId){
    if((byId[id].label||"").toLowerCase() === t) return id;
  }
  for(var id2 in byId){
    if(id2.toLowerCase().indexOf(t) >= 0) return id2;
  }
  return null;
}

var feedCount = 0;
function pushEvent(ev){
  var box = document.getElementById("feeditems");
  var el = document.createElement("div");
  el.className = "ev" + (ev.failed ? " failed" : "");
  var t = new Date(ev.at || Date.now());
  var hh = ("0"+t.getHours()).slice(-2)+":"+("0"+t.getMinutes()).slice(-2)+":"+("0"+t.getSeconds()).slice(-2);
  el.innerHTML = "<span class='k' style='background:" + (KIND_COLOR[ev.kind]||"#475569") + "'></span>" +
    "<span class='body'><span class='t'>" + esc(ev.text || ev.tool) + "</span>" +
    "<div class='m'>" + hh + " · " + esc(ev.tool||ev.kind) + "</div></span>";
  box.insertBefore(el, box.firstChild);
  if(++feedCount > 80){ box.removeChild(box.lastChild); feedCount--; }
}

/* ---------- paths ---------- */
// Breadth-first on the undirected graph: the question people actually ask of a
// knowledge graph is "how are these two connected", and direction is rarely
// part of it.
function tracePath(a, b){
  var prev = {}, seen = {}; seen[a] = true;
  var queue = [a];
  while(queue.length){
    var cur = queue.shift();
    if(cur === b) break;
    var next = adj[cur] || [];
    for(var i=0;i<next.length;i++){
      if(seen[next[i].to]) continue;
      seen[next[i].to] = true;
      prev[next[i].to] = {from: cur, link: next[i].link};
      queue.push(next[i].to);
    }
  }
  if(a !== b && !prev[b]) return null;
  var chain = [b], links = [], walk = b;
  while(walk !== a){
    var step = prev[walk];
    links.push(step.link);
    walk = step.from;
    chain.push(walk);
  }
  chain.reverse();
  return {nodes: chain, links: links};
}

function showPath(a, b){
  var found = tracePath(a, b);
  var info = document.getElementById("pathinfo");
  if(!found){
    info.innerHTML = "no path between them";
    clearPath(true);
    return;
  }
  pathNodes = {}; pathLinks = {};
  found.nodes.forEach(function(id){ pathNodes[id] = true; });
  found.links.forEach(function(l){ pathLinks[keyOf(l)] = true; });
  // Labels are clipped again here. They are already short by the time they
  // arrive, but a six-hop chain of them is still a paragraph, and the summary
  // has one narrow column to say the shape of the route in.
  info.innerHTML = "<b>" + (found.nodes.length-1) + "</b> hops · " +
    found.nodes.map(function(id){
      var l = byId[id].label || id;
      return esc(l.length > 20 ? l.slice(0,20) + "…" : l);
    }).join(" → ");
  repaint();
}

function clearPath(keepInfo){
  pathNodes = {}; pathLinks = {};
  pickA = null; pickMode = false;
  document.getElementById("pathbtn").classList.remove("on");
  if(!keepInfo) document.getElementById("pathinfo").innerHTML = "";
  repaint();
}

/* ---------- interaction ---------- */
function onNodeClick(n){
  if(pickMode){
    if(!pickA){
      pickA = n.id;
      strike(n.id, "#fbbf24", 60000);
      document.getElementById("pathinfo").innerHTML = "from <b>" + esc(n.label) + "</b> — pick the other end";
      return;
    }
    showPath(pickA, n.id);
    pickA = null; pickMode = false;
    document.getElementById("pathbtn").classList.remove("on");
    return;
  }
  // Fly to it rather than jump: keeping the motion continuous is what lets
  // someone keep track of where they were.
  var r = Math.hypot(n.x, n.y, n.z) || 1, k = 1 + 90/r;
  G.cameraPosition({x:n.x*k, y:n.y*k, z:n.z*k}, n, 900);
  showDetail(n);
}

function showDetail(n){
  document.getElementById("dt").textContent = n.label;
  var rows = "";
  function row(k,v){ return v ? "<div class='r'><div class='k'>"+esc(k)+"</div><div class='v'>"+esc(v)+"</div></div>" : ""; }
  rows += row("Type", n.type);
  rows += row("ID", n.id);
  rows += row("Connections", String(degreeOf(n.id)));
  var nb = (adj[n.id]||[]).slice(0,8).map(function(a){ return byId[a.to] ? byId[a.to].label : a.to; });
  rows += row("Linked to", nb.join(", "));
  document.getElementById("db").innerHTML = rows;
  document.getElementById("detail").classList.add("on");
}
function closeDetail(){ document.getElementById("detail").classList.remove("on"); }

document.getElementById("q").addEventListener("input", function(){
  query = this.value.trim().toLowerCase(); repaint();
});

/* An embedder driving the search.
   The page is mounted in an application that has its own search box and its own
   reason to search — recalling a memory is asking about the same things these
   nodes are. Rather than make it reach into this document (which it cannot, on
   a different origin behind a proxy), it sends a message and this puts the
   query in the same box a person would type into.
   Unknown messages are ignored: an embedder that speaks a later dialect than
   this page understands should get a page that works, not one that throws. */
window.addEventListener("message", function(ev){
  var msg = ev.data;
  if(!msg || msg.type !== "cortexdb:highlight") return;
  var q = typeof msg.query === "string" ? msg.query : "";
  document.getElementById("q").value = q;
  query = q.trim().toLowerCase();
  repaint();
});
document.getElementById("type").addEventListener("change", function(){
  var t = this.value;
  G.nodeVisibility(function(n){ return !t || (n.type||"(untyped)") === t; });
});
document.getElementById("fit").addEventListener("click", function(){ fitCore(700); });

// fitCore frames the graph, not its outliers.
//
// A knowledge graph almost always has a few nodes way out on their own, and
// framing everything means framing mostly the gap between them and the rest —
// the part worth looking at ends up a speck. So the far tail is left out of the
// calculation. It is still drawn, and still there when you pull back; it just
// does not get to decide where the camera goes.
function fitCore(ms){
  var ns = G.graphData().nodes.filter(function(n){ return typeof n.x === "number"; });
  if(ns.length < 12){ G.zoomToFit(ms, 70); return; }
  var radii = ns.map(function(n){ return Math.hypot(n.x, n.y, n.z); }).sort(function(a,b){ return a-b; });
  var cut = radii[Math.floor(radii.length * 0.93)];
  G.zoomToFit(ms, 70, function(n){ return Math.hypot(n.x, n.y, n.z) <= cut; });
}
document.getElementById("pathbtn").addEventListener("click", function(){
  pickMode = !pickMode; pickA = null;
  this.classList.toggle("on", pickMode);
  document.getElementById("pathinfo").innerHTML = pickMode ? "pick the first node" : "";
});
document.getElementById("clearpath").addEventListener("click", function(){ clearPath(false); });
document.getElementById("flow").addEventListener("click", function(){
  flowOn = !flowOn; this.classList.toggle("on", flowOn); repaint();
});

// Orbit. It yields to the pointer: the moment someone drags, the camera is
// theirs again, because a view that keeps spinning under your hand is a view
// you are fighting.
var spinBtn = document.getElementById("spin");
spinBtn.addEventListener("click", function(){ setSpin(!spinning); });
function setSpin(on){
  spinning = on;
  spinBtn.classList.toggle("on", on);
  if(spinTimer){ clearInterval(spinTimer); spinTimer = null; }
  if(!on) return;
  var cam = G.cameraPosition();
  var radius = Math.hypot(cam.x, cam.z) || 400;
  spinAngle = Math.atan2(cam.x, cam.z);
  spinTimer = setInterval(function(){
    spinAngle += 0.0022;
    var c = G.cameraPosition();
    G.cameraPosition({x: radius*Math.sin(spinAngle), y: c.y, z: radius*Math.cos(spinAngle)});
  }, 16);
}
document.getElementById("scene").addEventListener("pointerdown", function(){
  userMovedCamera = true;
  if(spinning) setSpin(false);
});
document.getElementById("scene").addEventListener("wheel", function(){ userMovedCamera = true; }, {passive:true});

/* ---------- the stream ---------- */
var booted = false;
function boot(){
  if(booted) return;
  booted = true;
  var el = document.getElementById("boot");
  el.classList.add("gone");
  setTimeout(function(){ el.style.display = "none"; }, 500);
  // Fitting once is always wrong: the force layout is still expanding when the
  // first frame arrives, so an early fit frames a graph that is about to grow
  // out of shot. Fit again as it settles — and stop as soon as the view is
  // touched, because re-framing under someone's hand is worse than a loose fit.
  [400, 1600, 3400, 6000].forEach(function(ms){
    setTimeout(function(){ if(!userMovedCamera) fitCore(700); }, ms);
  });
}

function setLive(connected, activity){
  var b = document.getElementById("live"), t = document.getElementById("livetext");
  activityLive = activity;
  b.classList.toggle("cold", !connected);
  // Said plainly, because a ticker that never moves is otherwise indistinguishable
  // from one that is broken.
  t.textContent = !connected ? "reconnecting" : (activity ? "live · watching calls" : "live · structure only");
}

var retry = 800;
function connect(){
  // Relative, not "/api/stream". The page is embedded behind reverse proxies —
  // an application putting an authenticated front door on a view that has none
  // of its own — and an absolute path would leave the mount point and land on
  // whatever the host serves at /api. Relative means the stream is always found
  // next to the page that asked for it, wherever that page is mounted.
  var es = new EventSource("api/stream");
  es.addEventListener("snapshot", function(m){
    var p = JSON.parse(m.data);
    retry = 800;
    applySnapshot(p);
    (p.events||[]).forEach(pushEvent);
    setLive(true, !!p.activity);
    document.getElementById("head").title = "reading " + p.source;
    boot();
  });
  es.addEventListener("delta", function(m){ applyDelta(JSON.parse(m.data)); });
  es.addEventListener("activity", function(m){ onActivity(JSON.parse(m.data)); });
  es.onerror = function(){
    es.close();
    setLive(false, activityLive);
    // Backing off matters: the server is this machine's MCP process, and a page
    // left open overnight after it exited should not spend the night hammering
    // a closed port.
    setTimeout(connect, retry);
    retry = Math.min(retry*2, 15000);
  };
}
connect();
// If the stream never arrives at all, show the scene anyway rather than a
// spinner that spins forever.
setTimeout(boot, 12000);

/* ---------- following the container ----------
   The HUD is positioned in CSS and reflows on its own; the WebGL canvas is a
   fixed pixel buffer and does not. Left alone it keeps whatever size the window
   had when the page loaded, so maximising the window grows the frame around a
   picture that stays put in the corner.

   Observed on the element rather than listening for window resize, because the
   two are not the same event. This page is embedded — in a panel that shares a
   window with a sidebar someone can collapse — and that changes the container's
   width with no window resize to hear. */
var resizeQueued = false;
function fitToContainer(){
  if(resizeQueued) return;
  resizeQueued = true;
  requestAnimationFrame(function(){
    resizeQueued = false;
    var el = document.getElementById("scene");
    var w = el.clientWidth, h = el.clientHeight;
    if(w < 2 || h < 2) return;   // hidden panel: nothing to size to
    G.width(w).height(h);
    // The bloom pass keeps its own render targets and is sized once at
    // construction, so it has to be told too — otherwise the glow stays at the
    // old resolution and smears across the new one.
    if(window.__bloom && window.__bloom.setSize) window.__bloom.setSize(w, h);
  });
}
if(window.ResizeObserver){
  new ResizeObserver(fitToContainer).observe(document.getElementById("scene"));
} else {
  window.addEventListener("resize", fitToContainer);
}
</script>

<script type="importmap">
{"imports":{
  "three":"https://unpkg.com/three@0.168.0/build/three.module.js",
  "three/addons/":"https://unpkg.com/three@0.168.0/examples/jsm/"
}}
</script>
<script type="module">
// Glow. Pinned to the same three revision the graph library bundles, so the
// pass and the composer agree on what a render target is. Everything here is
// decoration: if the import fails the page above has already drawn itself.
try {
  const { UnrealBloomPass } = await import("three/addons/postprocessing/UnrealBloomPass.js");
  const { Vector2 } = await import("three");
  // Tuned down hard from the defaults. At full strength with a low threshold
  // every node blooms, the halos merge, and the background lifts to the colour
  // of whatever is most common — the picture goes to fog and the type colours
  // stop being readable. A high threshold means only what is genuinely bright
  // glows: the hubs, and whatever the brain just touched.
  // Sized to the container, not the window: this page is embedded, and its
  // frame is routinely a fraction of the window it sits in.
  const scene = document.getElementById("scene");
  const bloom = new UnrealBloomPass(
    new Vector2(scene.clientWidth || window.innerWidth, scene.clientHeight || window.innerHeight),
    1.15, 0.5, 0.18);
  const composer = G.postProcessingComposer();
  composer.addPass(bloom);
  // Handed to the resize path above, which runs in the classic script and
  // cannot see this module's scope.
  window.__bloom = bloom;
  fitToContainer();
  const btn = document.getElementById("glow");
  btn.addEventListener("click", () => {
    glowOn = !glowOn;
    bloom.enabled = glowOn;
    btn.classList.toggle("on", glowOn);
  });
} catch (err) {
  // Offline, or a CDN that will not serve modules. Say so on the button rather
  // than leaving one that looks enabled and does nothing.
  const btn = document.getElementById("glow");
  btn.classList.remove("on");
  btn.textContent = "No glow";
  btn.disabled = true;
  btn.title = String(err);
}
</script>
</body>
</html>`
