package liveview

// ontologyHTML is the second page: the ontology, not the instances.
//
// WHY THIS IS NOT THE 3D SCENE NEXT DOOR.
//
// The instance page is force-directed and rendered in WebGL because of what it
// is drawing: four thousand nodes with no inherent order, where the only
// structure worth showing is emergent — which clumps are dense, what sits at
// the centre. A force layout is the right tool for that and a hand-placed one
// would be impossible.
//
// An ontology is the opposite graph on every axis that decides a layout:
//
//   - It is tens of nodes, not thousands. Every one of them has a name that
//     must be read, and text in a rotating 3D scene is either billboarded and
//     unreadable at depth or readable and no longer placed where the thing is.
//   - It has real structure, declared rather than emergent. An interface is a
//     shape several object types share; a link type has a direction and a
//     cardinality per side, and the ONE side is the one carrying the foreign
//     key. None of those are distances, and a force simulation can only
//     express distance — asked to draw "Airport implements Locatable" it puts
//     the two near each other, which is also what it does for two types that
//     merely have a lot of links.
//   - Its numbers are read by comparison. The gap this page exists for is a
//     quantity per box, and comparing quantities across refreshes needs the
//     boxes to be in the same place both times. A force layout settles
//     somewhere different on every load, by design.
//
// So: a deterministic 2D SVG, laid out in lanes. One lane per interface, its
// implementors beside it — which is what "these object types share a shape"
// looks like when it is drawn rather than annotated, and it needs no crossing
// lines because the grouping IS the layout. Object types implementing nothing
// get the last lane. Link types are drawn as curves between the boxes, under
// them, with the multiplicity glyph at each end and the foreign key marked on
// the side that declares it. Types the store uses but the schema never
// declared sit outside the diagram entirely, in a band that is visibly not
// part of the model — because that is exactly what they are.
//
// Nothing here decides what is true. Which of the four findings this page is
// showing is [OntologyReport.State], computed in Go; the page picks a sentence
// by it and never infers one from an empty list.
const ontologyHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CortexDB — ontology</title>
<style>
  *{box-sizing:border-box;margin:0;padding:0}
  html,body{min-height:100%;background:#04060d;color:#c7d2e5;
    font:13px/1.5 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',sans-serif}
  body{padding:14px 14px 40px}
  .bare .panel,.bare #boot{display:none!important}
  .panel,.card{background:rgba(8,13,26,.86);border:1px solid #1b2740;border-radius:10px;
    backdrop-filter:blur(10px);box-shadow:0 8px 30px rgba(0,0,0,.5);position:relative}
  /* The distinction .bare turns on: a panel is chrome an embedder can do
     without, a card is the answer itself. ?panels=0 on the scene page means
     "the graph alone", and the equivalent here is the diagram and what it
     found — not a blank page. */
  /* Same chrome as the scene page, and the same fold: a reader moving between
     the two should not have to learn a second set of conventions. */
  .fold{position:absolute;top:5px;right:5px;z-index:1;width:22px;height:22px;flex:none;padding:0;
    background:none;border:none;color:#3f4d66;font-size:15px;line-height:20px;cursor:pointer}
  .fold:hover{color:#cbd5e1}
  .fold::before{content:"–"}
  #head.folded>.fold::before,#legend.folded>.fold::before{content:"+"}
  #head.folded>.bd,#legend.folded>.bd{display:none}
  #head.folded,#legend.folded{display:block;width:auto;min-width:0;padding:5px 30px 5px 11px}
  #head.folded::before,#legend.folded::before{content:attr(data-label);font-size:10px;
    text-transform:uppercase;letter-spacing:.06em;color:#4b5b76;white-space:nowrap}

  #head{padding:11px 14px;margin-bottom:12px}
  #head h1{font-size:13px;font-weight:600;color:#e8eefc;letter-spacing:.2px;padding-right:16px}
  #head h1 a{color:#7dd3fc;text-decoration:none;font-weight:500}
  #head h1 a:hover{text-decoration:underline}
  #sub{font-size:11px;color:#64748b;margin-top:5px}
  #sub b{color:#7dd3fc;font-weight:600}
  .pill{display:inline-block;font-size:10px;padding:1px 7px;border-radius:20px;border:1px solid #1e3a5f;
    color:#7dd3fc;margin-left:6px;text-transform:uppercase;letter-spacing:.05em}
  .pill.cold{border-color:#334155;color:#64748b}
  .pill.warn{border-color:#7c2d12;color:#fbbf24}
  #headrow{display:flex;gap:6px;margin-top:9px;flex-wrap:wrap}
  button{padding:5px 10px;background:#141f36;border:1px solid #23324f;border-radius:6px;
    color:#a9bcd8;font-size:11px;cursor:pointer;font-family:inherit;transition:.15s}
  button:hover{background:#1d2c49;color:#e2ecfb}
  button.on{background:#1d4ed8;border-color:#3b82f6;color:#fff;box-shadow:0 0 14px rgba(59,130,246,.45)}
  /* A switch that cannot be flipped must not look flipped. The gap has nothing
     to turn off on a draft, and leaving it lit would be the page showing a
     lever it is ignoring. */
  button:disabled{opacity:.35;cursor:not-allowed;box-shadow:none}
  button:disabled:hover{background:#141f36;color:#a9bcd8}
  /* The one control that changes what the whole page is about wears the
     draft's own colour rather than the declarations' blue. */
  #draftbtn.on{background:#a16207;border-color:#d97706;color:#fffbeb;
    box-shadow:0 0 14px rgba(217,119,6,.45)}

  /* The finding, before the picture. On three of the four states this is the
     whole answer and the diagram below it is empty or beside the point, so it
     is the first thing on the page rather than a footnote under a drawing. */
  #say{padding:11px 14px;margin-bottom:12px;font-size:12px;line-height:1.6;color:#94a3b8}
  #say b{color:#e2e8f0;font-weight:600}
  #say .foot{font-size:10px;color:#3f4d66;margin-top:6px;line-height:1.5}
  #say.absent{border-color:#3f2d0a}
  #say.unused{border-color:#3f2d0a}
  #say.unreadable{border-color:#3b1d1d}

  #canvas{padding:8px;overflow-x:auto}
  svg{display:block}
  text{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif}
  .ob{cursor:pointer}
  .ob:hover .obbg{stroke:#60a5fa}
  .dim{opacity:.16}

  #strays{margin-top:12px;padding:11px 14px;border:1px dashed #3f2d0a;border-radius:10px;
    background:rgba(20,13,4,.5)}
  #strays h3{font-size:10px;text-transform:uppercase;letter-spacing:.06em;color:#a16207;margin-bottom:4px}
  #strays .say{font-size:11px;color:#94a3b8;margin-bottom:8px;line-height:1.55}
  #strays .grp{margin-bottom:10px}
  #strays .grp>span{font-size:10px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76}
  .chips{display:flex;flex-wrap:wrap;gap:5px;margin-top:5px}
  /* Outside the model, and drawn so: dashed, unlit, in the band's own hue
     rather than any colour the diagram uses for something declared. */
  .chip{font-size:11px;padding:2px 8px;border-radius:5px;border:1px dashed #57534e;
    color:#d6d3d1;background:rgba(41,37,36,.6);white-space:nowrap}
  .chip b{color:#a8a29e;font-weight:500;font-variant-numeric:tabular-nums}
  .chip.blank{font-style:italic;color:#78716c}
  #strays .foot{font-size:10px;color:#3f4d66;margin-top:6px}

  /* THE DRAFT SURFACE.

     Everything a draft adds is in the band's amber and never in the blue the
     declarations wear, because colour is the fastest of the signals that say
     "nobody signed this" and the one a reader takes in before any sentence.
     A draft that borrowed the saved page's palette would be arguing, in the
     only language a glance understands, that somebody had. */
  .pill.draft{border-color:#a16207;color:#fbbf24;background:rgba(41,26,4,.55)}
  #say.drafted,#say.nothing-to-draft{border-color:#3f2d0a}
  #say.undraftable{border-color:#3b1d1d}
  #say .notes{font-size:10px;color:#3f4d66;margin-top:6px;line-height:1.55}
  #say .notes div{margin-top:2px}

  #decisions,#against{margin-top:12px;padding:11px 14px;border:1px solid #3f2d0a;border-radius:10px;
    background:rgba(20,13,4,.38)}
  #decisions h3,#against h3{font-size:10px;text-transform:uppercase;letter-spacing:.06em;
    color:#a16207;margin-bottom:4px}
  #decisions .say,#against .say{font-size:11px;color:#94a3b8;margin-bottom:8px;line-height:1.55}
  /* The glance, before the reading: how much of each kind of question there
     is. Seven merges and one guessed key are two different afternoons, and a
     flat list of a hundred and seventeen communicates neither. */
  #decisions .tally{display:flex;flex-wrap:wrap;gap:5px;margin-bottom:4px}
  #decisions .tally span{font-size:10.5px;padding:2px 8px;border-radius:5px;border:1px solid #57534e;
    color:#d6d3d1;background:rgba(41,37,36,.55);white-space:nowrap;cursor:pointer}
  #decisions .tally span b{color:#fbbf24;font-weight:600;font-variant-numeric:tabular-nums}
  #decisions .grp{border-top:1px solid #292524;padding-top:9px;margin-top:9px}
  #decisions .gh{display:flex;gap:8px;align-items:baseline;cursor:pointer}
  #decisions .gh b{color:#fbbf24;font-size:12px;font-weight:600}
  #decisions .gh .n{font-size:10px;color:#1c1917;background:#a16207;border-radius:20px;padding:0 7px;
    font-variant-numeric:tabular-nums;font-weight:600}
  #decisions .gh .kd{font-size:10px;color:#57534e;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
  #decisions .gh::before{content:"–";color:#57534e;width:9px;flex:none}
  #decisions .grp.shut .gh::before{content:"+"}
  #decisions .grp.shut .q,#decisions .grp.shut .d,#decisions .grp.shut .more{display:none}
  #decisions .q{font-size:11px;color:#94a3b8;margin:5px 0 3px;line-height:1.55}
  #decisions .d{margin-top:6px;padding-left:9px;border-left:2px solid #292524}
  #decisions .d .tg{font-size:11px;color:#e8eefc;font-weight:600;
    font-family:ui-monospace,SFMono-Regular,Menlo,monospace;word-break:break-word}
  #decisions .d .dt{font-size:11px;color:#a8a29e;line-height:1.5}
  #decisions .d .ev{font-size:10.5px;color:#78716c;margin-top:2px;font-variant-numeric:tabular-nums}
  #decisions .more{font-size:10.5px;color:#78716c;margin-top:6px}
  #against .ch{display:flex;gap:8px;align-items:baseline;font-size:11px;margin-top:4px}
  #against .ch i{flex:none;font-style:normal;font-size:9px;text-transform:uppercase;letter-spacing:.05em;
    color:#57534e;width:150px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
  #against .ch b{color:#e8eefc;font-weight:600}
  #against .ch.brk i{color:#f87171}
  #against .ch span{color:#94a3b8}

  #legend{margin-top:12px;padding:10px 12px;font-size:11px;color:#64748b}
  #legend h3{font-size:10px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76;margin-bottom:7px}
  #legend .li{display:flex;gap:8px;align-items:baseline;margin-bottom:3px}
  #legend .li i{flex:none;width:22px;color:#94a3b8;font-style:normal;text-align:center}

  #detail{position:fixed;top:14px;right:14px;width:320px;max-height:calc(100vh - 28px);
    overflow:auto;padding:12px;z-index:20;display:none}
  #detail.on{display:block}
  #detail .t{font-size:14px;font-weight:600;color:#e8eefc;word-break:break-word;padding-right:16px}
  #detail .x{position:absolute;top:8px;right:11px;cursor:pointer;color:#475569;font-size:17px;line-height:1}
  #detail .x:hover{color:#cbd5e1}
  #detail .k{font-size:9px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76;margin-top:9px}
  #detail .v{font-size:12px;color:#cbd5e1;word-break:break-word}
  #detail .desc{font-size:11px;color:#94a3b8;margin-top:5px}
  #detail table{width:100%;border-collapse:collapse;margin-top:4px;font-size:11px}
  #detail td{padding:2px 0;vertical-align:top;border-bottom:1px solid rgba(30,41,59,.5)}
  #detail td.p{color:#cbd5e1;word-break:break-word}
  #detail td.k2{color:#64748b;text-align:right;white-space:nowrap;padding-left:8px}
  #detail .req{color:#fbbf24}
  #detail .flag{color:#4b5b76;font-size:10px}
  #boot{position:fixed;inset:0;z-index:40;display:flex;align-items:center;justify-content:center;
    gap:12px;background:#04060d;transition:opacity .45s}
  #boot.gone{opacity:0;pointer-events:none}
  .spin{width:22px;height:22px;border:2px solid #1b2740;border-top-color:#3b82f6;border-radius:50%;
    animation:sp .8s linear infinite}
  @keyframes sp{to{transform:rotate(360deg)}}
  #boot span{color:#475569;font-size:12px}
</style>
</head>
<body>

<div class="panel" id="head" data-label="Ontology">
  <button class="fold" type="button" title="Collapse"></button>
  <div class="bd">
    <h1>CortexDB — ontology &nbsp;<a href="." title="The instance graph: what is actually in this brain">← live brain</a></h1>
    <div id="sub">reading…</div>
    <div id="headrow">
      <button id="gapbtn" class="on" title="Count what the store actually holds and hold the declarations against it">Declared vs actual</button>
      <button id="straybtn" class="on" title="Show the types the store uses that no schema declares">Undeclared</button>
      <button id="draftbtn" title="Derive an ontology from what this store already holds. Nothing is saved: the page reviews, a person saves.">Draft</button>
      <button id="reload" title="Read it again now">Refresh</button>
      <span id="schemas"></span>
    </div>
  </div>
</div>

<div class="card" id="say">Reading the ontology…</div>
<div class="card" id="canvas"></div>
<div id="strays"></div>
<div id="decisions"></div>
<div id="against"></div>

<div class="panel" id="legend" data-label="Key">
  <button class="fold" type="button" title="Collapse"></button>
  <div class="bd"><h3>Key</h3>
    <div class="li"><i>1</i><span>this end reaches exactly one object — and it is the end that carries the foreign key</span></div>
    <div class="li"><i>&#8727;</i><span>this end reaches many</span></div>
    <div class="li"><i>&#9679;</i><span>a foreign key property is declared on this side</span></div>
    <div class="li"><i>&#9482;</i><span>dashed box: declared, and nothing in the store is of this type</span></div>
    <div class="li"><i>&#9633;</i><span>a lane groups the object types implementing one interface</span></div>
  </div>
</div>

<div class="panel" id="detail"><span class="x" id="dx">&times;</span>
  <div class="t" id="dt"></div><div id="db"></div></div>

<div id="boot"><div class="spin"></div><span>Reading the ontology…</span></div>

<script>
/* ---------- options from the URL ----------

   The same rule the scene page follows: every switch the buttons flip is also
   a query parameter, because an embedder cannot press a button.

     ?gap=0      the declarations alone. Nothing is read for the overlay —
                 this is the switch that changes the server's work, not just
                 the page's paint, so an embedder that only wants the model
                 costs the store nothing for the part it is not showing
     ?strays=0   no band of undeclared types. On a store with no schema that
                 band is the entire page, which is the point; an embedder
                 showing the model alone turns it off
     ?schema=ID  which saved schema to draw, when more than one is saved
     ?panels=0   no chrome, the diagram alone
     ?bg=RRGGBB  background colour, for a page shown through a shape

   And the draft's own, which change the verb rather than the paint:

     ?draft=1    derive an ontology from what the store holds and draw THAT,
                 through these same lanes and this same overlay. Nothing is
                 saved and nothing here can save: the page reviews, a person
                 saves, through ontology_save. A flag rather than a
                 ?schema=draft sentinel, because "draft" is a perfectly legal
                 schema id — it is the deriver's own default — and a store
                 that had saved its draft could then never be shown it again
     ?min=N      keep node and edge types with fewer records than N out of the
                 *drawing*. Defaults to 3: a real brain's unthresholded draft
                 is 124 object types and 233 link types, which is not
                 something a person reviews. What the threshold kept out is
                 counted in the finding above the diagram, so a pruned drawing
                 is never mistaken for a small vocabulary
     ?min_nodes=N
     ?min_edges=N  one side of that threshold on its own                     */
var OPTS = (function(){
  var q = {};
  try { new URLSearchParams(location.search).forEach(function(v, k){ q[k] = v; }); } catch(e){}
  return q;
})();
var gapOn = OPTS.gap !== "0";
var straysOn = OPTS.strays !== "0";
var schemaWant = OPTS.schema || "";
var draftOn = OPTS.draft === "1";
var minWant = OPTS.min == null ? "" : OPTS.min;
var minNodesWant = OPTS.min_nodes == null ? "" : OPTS.min_nodes;
var minEdgesWant = OPTS.min_edges == null ? "" : OPTS.min_edges;
var BG = /^[0-9a-fA-F]{6}$/.test(OPTS.bg || "") ? "#" + OPTS.bg : "#04060d";
if(OPTS.panels === "0") document.documentElement.classList.add("bare");
if(OPTS.bg && BG !== "#04060d") document.body.style.background = BG;

/* ONTOLOGY_MS mirrors liveview.OntologyInterval; a test keeps them equal. A
   schema moves when somebody redesigns the model, so this is already far
   faster than the thing it is watching. */
var ONTOLOGY_MS = 30000;

var REP = null;
var SEL = null;      // {kind:"object"|"link"|"interface", name}
var HILITE = null;   // interface api key whose implementors are lit

function esc(s){return String(s==null?"":s).replace(/[&<>"]/g,function(c){
  return {"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;"}[c];});}
function num(n){ try { return Number(n||0).toLocaleString(); } catch(e){ return String(n||0); } }
function key(s){ return String(s==null?"":s).trim().toLowerCase(); }
function clip(s, n){ s = String(s==null?"":s); return s.length > n ? s.slice(0, n-1) + "…" : s; }
function plural(n, one, many){ return n === 1 ? one : many; }

/* Lane accents. Deliberately not the scene page's node-type palette: these
   colour interfaces, which that page has no concept of, and reusing its hues
   would suggest a correspondence that does not exist. */
var LANE = ["#38bdf8","#a78bfa","#34d399","#fbbf24","#f472b6","#2dd4bf","#fb923c","#60a5fa"];
function laneColor(i){ return LANE[i % LANE.length]; }

/* ---------- the sentence ----------
   Chosen by rep.state, which Go decided. Four states, four sentences, and no
   branch here that reads a length and guesses: an empty list of object types
   is produced by a source that cannot be asked, by a store with no schema and
   by a schema drawn with ?gap=0, and telling a reader the wrong one of those
   is worse than telling them nothing. */
/* The draft's three states, kept entirely apart from the saved four.

   Read off rep.draft rather than off the state word, so that a state this
   build has never heard of still arrives wearing a draft's clothes. The
   alternative — inferring draftness from an enum match — makes the first
   unrecognised state render as a saved schema, which is the one mistake this
   whole surface is arranged to prevent. */
function draftSay(rep, u){
  if(rep.state === "undraftable"){
    return "<b>This source cannot draft an ontology.</b> That is not the same as this store having " +
      "nothing to draft — nobody could ask it." +
      "<div class='foot'>" + esc(rep.reason || "no reason given") + "</div>";
  }
  if(rep.state === "nothing-to-draft"){
    return "<b>There is nothing here to draft from.</b> The store was read and holds no nodes, so " +
      "there is no vocabulary to write down. That is an answer about this store, not about the deriver.";
  }

  if(rep.state !== "drafted"){
    /* A draft state this build has never heard of. Saying so is the only
       honest thing left: the alternative is to fall through into the drafted
       sentence and describe a derivation that may not have happened. */
    return "<b>This draft arrived in a state this page does not know how to read.</b>" +
      "<div class='foot'>" + esc(rep.state || "no state given") + " &middot; " +
      esc(rep.reason || "no reason given") + "</div>";
  }

  var objects = (rep.object_types||[]).length, links = (rep.link_types||[]).length;
  var html = "<b>Nobody has saved this.</b> It is a first schema derived from what this store already " +
    "contains: <b>" + num(objects) + "</b> object " + plural(objects, "type", "types") + " and <b>" +
    num(links) + "</b> link " + plural(links, "type", "types") + " proposed over the <b>" +
    num(rep.source_nodes) + "</b> " + plural(rep.source_nodes, "node", "nodes") + " and <b>" +
    num(rep.source_edges) + "</b> " + plural(rep.source_edges, "edge", "edges") + " that were read. " +
    "Every type is experimental, every primary key is a guess, every cardinality is MANY, and " +
    "enforcement is vocabulary. Saving it — or a corrected version of it — is a person's act and " +
    "happens elsewhere, through <b>ontology_save</b>.";

  if(rep.decisions_total){
    html += " <b>" + num(rep.decisions_total) + "</b> " +
      plural(rep.decisions_total, "question is", "questions are") + " waiting for a decision below.";
  }

  /* The threshold, stated whether or not it bit. A drawing of thirty boxes
     with a hundred types behind it reads as a small brain unless the page
     says otherwise, and "nothing was pruned" is the other half of the same
     sentence — without it, silence would mean either. */
  html += "<div class='foot'>Drawn at <b>min_nodes=" + num(rep.min_nodes) + "</b>, <b>min_edges=" +
    num(rep.min_edges) + "</b>. ";
  if(rep.pruned_node_types || rep.pruned_edge_types){
    html += "That kept <b>" + num(rep.pruned_node_types) + "</b> node " +
      plural(rep.pruned_node_types, "type", "types") + " (" + num(rep.pruned_nodes) + " nodes) and <b>" +
      num(rep.pruned_edge_types) + "</b> edge " + plural(rep.pruned_edge_types, "type", "types") +
      " (" + num(rep.pruned_edges) + " edges) out of the <em>drawing</em> — not out of the counts. " +
      "They are in the band below, and a lower threshold draws them.";
  } else {
    html += "Nothing was kept out of the drawing by the threshold; everything absent below was " +
      "excluded by the deriver's own rules.";
  }
  html += " The store carries <b>" + num(rep.source_node_types) + "</b> node types and <b>" +
    num(rep.source_edge_types) + "</b> edge types in all.</div>";

  /* The deriver's own caveats, in its words. It knows what it did not do, and
     a view paraphrasing that would be a second opinion pretending to be the
     first. */
  if((rep.notes||[]).length){
    html += "<div class='notes'>" + rep.notes.map(function(n){
      return "<div>" + esc(n) + "</div>";
    }).join("") + "</div>";
  }
  return html;
}

function renderSay(rep){
  var el = document.getElementById("say");
  el.className = "card " + esc(rep.state || "");
  var u = rep.usage || {};
  var html = "";
  if(rep.draft){ el.innerHTML = draftSay(rep, u); return; }
  if(rep.state === "unreadable"){
    html = "<b>This view cannot be asked for an ontology.</b> That is not the same as this store " +
      "having none — nobody asked it." +
      "<div class='foot'>" + esc(rep.reason || "no reason given") + "</div>";
  } else if(rep.state === "absent"){
    html = "<b>No ontology is saved in this store.</b> Nothing here declares what may be talked " +
      "about, so nothing written to it was ever checked against a model.";
    if(u.available){
      html += " Its <b>" + num(u.nodes) + "</b> " + plural(u.nodes, "node", "nodes") + " carry <b>" +
        num(u.node_types) + "</b> distinct " + plural(u.node_types, "type", "types") + " and its <b>" +
        num(u.edges) + "</b> " + plural(u.edges, "edge", "edges") + " carry <b>" + num(u.edge_types) +
        "</b> — a vocabulary that exists and was never written down.";
    }
  } else if(rep.state === "unused"){
    html = "<b>An ontology is saved, and nothing in the store uses it.</b> Every one of its <b>" +
      num((rep.object_types||[]).length) + "</b> object " +
      plural((rep.object_types||[]).length, "type", "types") + " and <b>" +
      num((rep.link_types||[]).length) + "</b> link " +
      plural((rep.link_types||[]).length, "type", "types") +
      " is at zero. The model below describes intent; nothing here is evidence of it.";
  } else if(rep.state === "live" && u.available){
    var unusedT = rep.declared_unused_types || 0, unusedL = rep.declared_unused_links || 0;
    html = "<b>" + num((rep.object_types||[]).length) + "</b> object types and <b>" +
      num((rep.link_types||[]).length) + "</b> link types declared, against <b>" + num(u.nodes) +
      "</b> nodes and <b>" + num(u.edges) + "</b> edges in the store.";
    if(unusedT || unusedL){
      html += " <b>" + num(unusedT + unusedL) + "</b> " +
        plural(unusedT + unusedL, "declaration is", "declarations are") + " at zero — declared, never used.";
    }
    if(u.undeclared_node_types || u.undeclared_edge_types){
      html += " <b>" + num(u.undeclared_node_types + u.undeclared_edge_types) + "</b> " +
        plural(u.undeclared_node_types + u.undeclared_edge_types, "type is", "types are") +
        " in the data that the schema does not describe.";
    }
    if(!unusedT && !unusedL && !u.undeclared_node_types && !u.undeclared_edge_types){
      html += " Everything declared is in use and nothing in the store is outside the model.";
    }
  } else {
    /* Saved, and the second reading was not taken. Every count on the diagram
       is zero for a reason that has nothing to do with the data, so this says
       so instead of letting the boxes imply otherwise. */
    html = "<b>" + num((rep.object_types||[]).length) + "</b> object types and <b>" +
      num((rep.link_types||[]).length) + "</b> link types declared. " +
      "<b>The gap was not measured</b>, so no count below means anything." +
      "<div class='foot'>" + esc(u.reason || "declared vs actual is switched off") + "</div>";
  }
  if(u.available && u.scope){
    html += "<div class='foot'>" + esc(u.scope) + "</div>";
  }
  el.innerHTML = html;
}

function renderHead(rep){
  var s = document.getElementById("sub");
  /* A draft never wears a saved schema's chips. No "active", no "inactive",
     no strict-actions badge: those describe a schema that is deciding what
     writes are allowed, and this one is deciding nothing. */
  if(rep.draft){
    if(rep.state === "undraftable") s.innerHTML = "no draft from this source";
    else if(rep.state === "nothing-to-draft") s.innerHTML = "nothing to draft from";
    else {
      s.innerHTML = "<b>" + esc(rep.schema_id || "draft") + "</b> &middot; derived, not saved" +
        "<span class='pill draft'>draft &middot; nobody signed this</span>" +
        "<span class='pill cold'>" + esc(rep.enforcement || "vocabulary") + "</span>" +
        "<span class='pill cold'>min " + num(rep.min_nodes) + " / " + num(rep.min_edges) + "</span>" +
        (rep.against ? "<span class='pill warn'>vs " + esc(rep.against) + "</span>" : "");
    }
    document.getElementById("schemas").innerHTML = "";
    return;
  }
  if(!rep.saved){
    s.innerHTML = rep.state === "unreadable" ? "no answer from this source" : "no schema saved";
  } else {
    s.innerHTML = "<b>" + esc(rep.name || rep.schema_id) + "</b> &middot; v" + num(rep.version) +
      "<span class='pill " + (rep.active ? "" : "cold") + "'>" + (rep.active ? "active" : "inactive") + "</span>" +
      "<span class='pill " + (rep.enforcement === "strict" ? "" : "warn") + "'>" + esc(rep.enforcement) + "</span>" +
      (rep.strict_actions ? "<span class='pill warn'>strict actions</span>" : "") +
      (rep.action_types ? "<span class='pill cold'>" + num(rep.action_types) + " actions</span>" : "") +
      (rep.object_sets ? "<span class='pill cold'>" + num(rep.object_sets) + " object sets</span>" : "");
  }
  /* Only when there is a choice to make. One saved schema needs no picker,
     and drawing one would suggest the page is showing a slice of something. */
  var pick = document.getElementById("schemas");
  var list = rep.schemas || [];
  if(list.length < 2){ pick.innerHTML = ""; return; }
  pick.innerHTML = list.map(function(r){
    var on = key(r.schema_id) === key(rep.schema_id);
    return "<button data-schema=\"" + esc(r.schema_id) + "\" class='" + (on ? "on" : "") + "'>" +
      esc(r.schema_id) + (r.active ? " ●" : "") + "</button>";
  }).join(" ");
  Array.prototype.forEach.call(pick.querySelectorAll("button"), function(b){
    b.addEventListener("click", function(){ schemaWant = b.getAttribute("data-schema"); load(); });
  });
}

/* ---------- the diagram ----------

   Lanes, computed here and stable: the same report draws the same picture at
   the same width every time, which is what lets a reader compare a count
   against the one they saw a minute ago. Ordering comes off the schema as
   declared, because that order was authored and re-sorting it would throw
   away the only grouping the modeller expressed. */
var BOX_W = 176, BOX_H = 66, GAP_X = 18, GAP_Y = 16;
var LANE_W = 156, LANE_PAD = 16, PAD = 10, LANE_GAP = 34, TOP_ROOM = 54;

function buildLanes(rep){
  var lanes = [], byName = {};
  (rep.interfaces||[]).forEach(function(it, i){
    var lane = {name: it.api_name, iface: it, color: laneColor(i), boxes: []};
    lanes.push(lane);
    byName[key(it.api_name)] = lane;
  });
  /* Every object type lands in exactly one lane — its first declared
     interface — so no box is drawn twice. The others it implements travel
     with the box as chips, and clicking an interface lights all of them
     wherever they sit, which is the part a single lane cannot show. */
  var loose = {name: null, iface: null, color: "#475569", boxes: []};
  (rep.object_types||[]).forEach(function(ot){
    var lane = null;
    (ot.implements||[]).forEach(function(n){ if(!lane && byName[key(n)]) lane = byName[key(n)]; });
    (lane || loose).boxes.push(ot);
  });
  if(loose.boxes.length) lanes.push(loose);
  return lanes;
}

function layout(rep, width){
  var lanes = buildLanes(rep);
  var bodyX = PAD + LANE_W + LANE_PAD;
  var cols = Math.max(1, Math.floor((width - bodyX - PAD + GAP_X) / (BOX_W + GAP_X)));
  var pos = {}, y = TOP_ROOM;
  lanes.forEach(function(lane){
    var rows = Math.max(1, Math.ceil(lane.boxes.length / cols));
    lane.y = y;
    lane.h = rows * BOX_H + (rows - 1) * GAP_Y;
    lane.boxes.forEach(function(ot, i){
      var c = i % cols, r = Math.floor(i / cols);
      var box = {x: bodyX + c * (BOX_W + GAP_X), y: y + r * (BOX_H + GAP_Y), ot: ot, lane: lane};
      pos[key(ot.api_name)] = box;
    });
    y += lane.h + LANE_GAP;
  });
  return {lanes: lanes, pos: pos, height: y + PAD, width: Math.max(width, bodyX + cols * (BOX_W + GAP_X) + PAD)};
}

/* A quadratic point, for placing the multiplicity glyphs a little way in from
   each end rather than under the box they touch. */
function qpoint(p0, c, p1, t){
  var m = 1 - t;
  return {x: m*m*p0.x + 2*m*t*c.x + t*t*p1.x, y: m*m*p0.y + 2*m*t*c.y + t*t*p1.y};
}
// Where a ray from a box's centre leaves the box, so a curve starts on the
// border instead of under the label.
function edgePoint(box, tx, ty){
  var cx = box.x + BOX_W/2, cy = box.y + BOX_H/2;
  var dx = tx - cx, dy = ty - cy;
  if(dx === 0 && dy === 0) return {x: cx, y: cy};
  var sx = dx === 0 ? Infinity : (BOX_W/2) / Math.abs(dx);
  var sy = dy === 0 ? Infinity : (BOX_H/2) / Math.abs(dy);
  var s = Math.min(sx, sy);
  return {x: cx + dx*s, y: cy + dy*s};
}

function svgEl(name, attrs){
  var e = document.createElementNS("http://www.w3.org/2000/svg", name);
  for(var k in attrs) if(attrs[k] != null) e.setAttribute(k, attrs[k]);
  return e;
}
function svgText(x, y, s, attrs){
  var t = svgEl("text", attrs || {});
  t.setAttribute("x", x); t.setAttribute("y", y);
  t.textContent = s;
  return t;
}

function renderDiagram(rep){
  var host = document.getElementById("canvas");
  host.innerHTML = "";
  if(!(rep.object_types||[]).length){
    /* Nothing to draw. The sentence above has already said which of the
       reasons that is; drawing an empty frame under it would add one more
       claim nobody made.

       Gated on the boxes alone, and deliberately no longer on rep.saved: a
       draft has object types and nothing saved, and a guard that asked
       whether somebody had saved this would hide the whole point of drafting
       it. For a saved report the two conditions coincide anyway — a report
       with no schema has no object types either. */
    host.style.display = "none";
    return;
  }
  host.style.display = "";

  var L = layout(rep, Math.max(560, host.clientWidth - 16));
  var counted = !!(rep.usage && rep.usage.available);
  var maxN = 1;
  (rep.object_types||[]).forEach(function(ot){ maxN = Math.max(maxN, ot.instances||0); });

  var needW = L.width;
  var svg = svgEl("svg", {width: L.width, height: L.height,
    viewBox: "0 0 " + L.width + " " + L.height});

  // Lane bands first, then links, then boxes: the curves pass behind the
  // labels they would otherwise cross out.
  L.lanes.forEach(function(lane){
    svg.appendChild(svgEl("rect", {"data-band": "1", x: PAD, y: lane.y - 8, width: L.width - PAD*2, height: lane.h + 16,
      rx: 10, fill: lane.iface ? "rgba(14,22,40,.55)" : "rgba(10,14,24,.4)",
      stroke: lane.iface ? "#16243c" : "#131c2e"}));
    var hx = PAD + 10, hy = lane.y;
    var g = svgEl("g", lane.iface ? {class: "ob", "data-iface": lane.iface.api_name} : {});
    g.appendChild(svgEl("rect", {x: hx, y: hy, width: LANE_W - 12, height: 54, rx: 8,
      fill: "rgba(4,6,13,.7)", stroke: lane.color, "stroke-opacity": lane.iface ? ".7" : ".35",
      "stroke-dasharray": lane.iface ? null : "4 3"}));
    if(lane.iface){
      g.appendChild(svgText(hx + 11, hy + 20, clip(lane.iface.api_name, 20),
        {fill: lane.color, "font-size": "12", "font-weight": "600"}));
      var extra = (lane.iface.extends || []).length
        ? "extends " + clip((lane.iface.extends||[]).join(", "), 16)
        : (lane.iface.properties||[]).length + " shared properties";
      g.appendChild(svgText(hx + 11, hy + 35, clip(extra, 24), {fill: "#64748b", "font-size": "10"}));
      g.appendChild(svgText(hx + 11, hy + 47,
        (lane.iface.implementors||[]).length + " implementors", {fill: "#4b5b76", "font-size": "10"}));
    } else {
      g.appendChild(svgText(hx + 11, hy + 26, "implements nothing",
        {fill: "#64748b", "font-size": "11", "font-style": "italic"}));
      g.appendChild(svgText(hx + 11, hy + 41, lane.boxes.length + " object types",
        {fill: "#4b5b76", "font-size": "10"}));
    }
    svg.appendChild(g);
  });

  // Link types. Parallel links between one pair are bowed by increasing
  // amounts so they stay countable rather than stacking into one line.
  //
  // Their names are held back and drawn last, over the boxes. A curve can
  // usually be bowed clear of everything; a curve between two boxes one row
  // apart sometimes cannot, and a name half-covered by a box reads as a
  // different name ("bookedBy" arriving as "y") rather than as an overlap.
  var seen = {}, labels = [];
  (rep.link_types||[]).forEach(function(lt){
    var a = L.pos[key(lt.a.object_type)], b = L.pos[key(lt.b.object_type)];
    if(!a || !b) return;   // a side pointing at a type this schema does not declare
    var pk = key(lt.a.object_type) + "|" + key(lt.b.object_type);
    var n = (seen[pk] = (seen[pk] || 0) + 1);
    var g = svgEl("g", {class: "ob", "data-link": lt.api_name});

    var live = counted && (lt.instances || 0) > 0;
    var stroke = live ? "#3b6ea5" : "#334155";
    // The label is measured before the curve is routed, because on a vertical
    // link it is the label — not the line — that decides how far the bow has
    // to reach: a curve that steps just clear of the box still parks its name
    // across it.
    var label = clip(lt.api_name, 22) + (counted ? "  " + num(lt.instances || 0) : "");
    var lw = label.length * 6.1 + 10;
    var p0, c, p1;
    if(a === b){
      // A link from a type to itself. Out to the right of the box rather than
      // over the top of it: a lane is only as tall as its boxes, so a loop
      // drawn above one lands inside the lane above it and reads as a link
      // between two types that have none.
      var rx = a.x + BOX_W, by = a.y + BOX_H;
      p0 = {x: rx - 30, y: by}; p1 = {x: rx, y: by - 22};
      c = {x: rx + 40 + (n - 1) * 22, y: by + 34 + (n - 1) * 14};
    } else {
      var ac = {x: a.x + BOX_W/2, y: a.y + BOX_H/2}, bc = {x: b.x + BOX_W/2, y: b.y + BOX_H/2};
      var mx = (ac.x + bc.x)/2, my = (ac.y + bc.y)/2;
      var dx = bc.x - ac.x, dy = bc.y - ac.y;
      var len = Math.max(1, Math.hypot(dx, dy));
      var px = -dy/len, py = dx/len;
      // Lanes stack vertically and a lane with one implementor leaves the
      // whole width beside it empty, so a link between two lanes is close to
      // vertical and its natural bow is sideways. Sent rightwards on purpose:
      // leftwards is where the interface headers are, and an arc through them
      // crosses out the labels that say which lane is which. Only for the
      // near-vertical ones — a horizontal link inside one lane has no such
      // free side and keeps the bow the geometry gives it.
      var vertical = Math.abs(px) > Math.abs(py);
      if(vertical && px < 0){ px = -px; py = -py; }
      // A bow shorter than half a box leaves the curve, and the label riding
      // at its apex, inside the column it was meant to step out of.
      var bow = (vertical ? BOX_W/2 + 26 + lw : 46) + (n - 1) * 30;
      c = {x: mx + px*bow, y: my + py*bow};
      p0 = edgePoint(a, c.x, c.y);
      p1 = edgePoint(b, c.x, c.y);
    }
    g.appendChild(svgEl("path", {d: "M" + p0.x + "," + p0.y + " Q" + c.x + "," + c.y + " " + p1.x + "," + p1.y,
      fill: "none", stroke: stroke, "stroke-width": live ? 1.6 : 1.2,
      "stroke-dasharray": live ? null : "5 4", "stroke-opacity": ".85"}));

    // The multiplicity glyph at each end, and a filled dot on the side that
    // declares a foreign key. Per side, because that is how the schema models
    // it: a ONE side is the side carrying the key, and a reader given only
    // "one-to-many" cannot tell which end holds the column.
    [[qpoint(p0, c, p1, 0.12), lt.a], [qpoint(p0, c, p1, 0.88), lt.b]].forEach(function(pair){
      var pt = pair[0], side = pair[1];
      g.appendChild(svgEl("circle", {cx: pt.x, cy: pt.y, r: 8,
        fill: side.foreign_key ? "#1d4ed8" : "#0a1120", stroke: stroke}));
      g.appendChild(svgText(pt.x, pt.y + 4, side.cardinality === "ONE" ? "1" :
        (side.cardinality === "MANY" ? "∗" : "?"),
        {fill: side.foreign_key ? "#dbeafe" : "#94a3b8", "font-size": "11", "text-anchor": "middle"}));
    });

    var mid = qpoint(p0, c, p1, 0.5);
    // Whatever the arcs needed, the canvas gives them: a lane with one
    // implementor leaves most of its width free and the bows use it, which is
    // the point — but an arc past the right edge would simply be cut off.
    needW = Math.max(needW, c.x + 24, mid.x + lw/2 + 12);
    var lg = svgEl("g", {class: "ob", "data-link": lt.api_name});
    lg.appendChild(svgEl("rect", {x: mid.x - lw/2, y: mid.y - 9, width: lw, height: 17, rx: 4,
      fill: "#04060d", "fill-opacity": ".96", stroke: stroke, "stroke-opacity": ".5"}));
    lg.appendChild(svgText(mid.x, mid.y + 3.5, label,
      {fill: live ? "#cbd5e1" : "#64748b", "font-size": "10.5", "text-anchor": "middle"}));
    labels.push(lg);
    svg.appendChild(g);
  });

  // Object types.
  (rep.object_types||[]).forEach(function(ot){
    var box = L.pos[key(ot.api_name)];
    if(!box) return;
    var unused = counted && !(ot.instances || 0);
    var g = svgEl("g", {class: "ob", "data-object": ot.api_name});
    // Dashed and unlit: declared, and nothing in the store is of this type.
    // Only ever drawn when the count was actually taken — see renderSay.
    g.appendChild(svgEl("rect", {x: box.x, y: box.y, width: BOX_W, height: BOX_H, rx: 8,
      class: "obbg", fill: unused ? "rgba(8,13,26,.35)" : "rgba(10,17,32,.95)",
      stroke: unused ? "#3f4d66" : box.lane.color, "stroke-opacity": unused ? ".7" : ".55",
      "stroke-dasharray": unused ? "5 4" : null}));
    g.appendChild(svgText(box.x + 11, box.y + 20, clip(ot.api_name, 21),
      {fill: unused ? "#8296b3" : "#e8eefc", "font-size": "12", "font-weight": "600"}));
    g.appendChild(svgText(box.x + 11, box.y + 34,
      clip("pk " + (ot.primary_key || "—"), 26), {fill: "#64748b", "font-size": "10"}));
    if(ot.status && ot.status !== "active"){
      g.appendChild(svgText(box.x + BOX_W - 11, box.y + 20, ot.status,
        {fill: "#fbbf24", "font-size": "9", "text-anchor": "end"}));
    }
    // The extra interfaces this type implements, as dots in its lane's
    // colours — the part a single lane cannot show.
    // Down the left border, not along the bottom: the bottom-right corner is
    // where the instance count goes, and a dot sitting on the number is a
    // count nobody can read.
    (ot.implements||[]).forEach(function(n, i){
      var idx = -1;
      (rep.interfaces||[]).forEach(function(it, j){ if(key(it.api_name) === key(n)) idx = j; });
      if(idx < 0) return;
      g.appendChild(svgEl("circle", {cx: box.x + 5, cy: box.y + 15 + i*9, r: 3,
        fill: laneColor(idx), "data-impl": key(n)}));
    });

    if(counted){
      // Square-rooted: on a real store one type holds thousands and the rest
      // hold single figures, and a linear bar would draw every one of them as
      // nothing at all.
      var frac = Math.sqrt(ot.instances || 0) / Math.sqrt(maxN);
      g.appendChild(svgEl("rect", {x: box.x + 11, y: box.y + BOX_H - 17, width: BOX_W - 62, height: 6,
        rx: 3, fill: "#0a1120"}));
      if(ot.instances > 0){
        g.appendChild(svgEl("rect", {x: box.x + 11, y: box.y + BOX_H - 17,
          width: Math.max(3, (BOX_W - 62) * frac), height: 6, rx: 3, fill: box.lane.color,
          "fill-opacity": ".8"}));
      }
      g.appendChild(svgText(box.x + BOX_W - 11, box.y + BOX_H - 11,
        unused ? "unused" : num(ot.instances), {fill: unused ? "#8296b3" : "#94a3b8",
        "font-size": "10", "text-anchor": "end", "font-style": unused ? "italic" : null}));
    } else {
      // Not "0". A count nobody took and a count that came back zero are the
      // same glyph on any page that prints the number either way.
      g.appendChild(svgText(box.x + 11, box.y + BOX_H - 11, "not counted",
        {fill: "#4b5b76", "font-size": "10", "font-style": "italic"}));
    }
    svg.appendChild(g);
  });
  labels.forEach(function(lg){ svg.appendChild(lg); });

  if(needW > L.width){
    svg.setAttribute("width", needW);
    svg.setAttribute("viewBox", "0 0 " + needW + " " + L.height);
    // The lane bands were sized to the grid, not to the arcs that ended up
    // reaching past it; a band that stopped short would read as the lane
    // ending there.
    Array.prototype.forEach.call(svg.querySelectorAll("rect[data-band]"), function(r){
      r.setAttribute("width", needW - PAD*2);
    });
  }

  svg.addEventListener("click", function(ev){
    var g = ev.target.closest ? ev.target.closest("g[data-object],g[data-link],g[data-iface]") : null;
    if(!g){ closeDetail(); setHilite(null); return; }
    if(g.hasAttribute("data-object")) showObject(g.getAttribute("data-object"));
    else if(g.hasAttribute("data-link")) showLink(g.getAttribute("data-link"));
    else showInterface(g.getAttribute("data-iface"));
  });
  host.appendChild(svg);
  applyHilite();
}

/* Lighting an interface's implementors is how the page answers "which object
   types share this shape" for a type sitting in another interface's lane. */
function setHilite(k){ HILITE = k; applyHilite(); }
function applyHilite(){
  var boxes = document.querySelectorAll("#canvas g[data-object]");
  if(!HILITE || !REP){
    Array.prototype.forEach.call(boxes, function(g){ g.classList.remove("dim"); });
    return;
  }
  var lit = {};
  (REP.interfaces||[]).forEach(function(it){
    if(key(it.api_name) !== HILITE) return;
    (it.implementors||[]).forEach(function(n){ lit[key(n)] = true; });
  });
  Array.prototype.forEach.call(boxes, function(g){
    g.classList.toggle("dim", !lit[key(g.getAttribute("data-object"))]);
  });
}

/* ---------- the detail panel ---------- */
function propTable(props){
  if(!props || !props.length) return "<div class='v' style='color:#4b5b76'>no properties declared</div>";
  return "<table>" + props.map(function(p){
    var flags = [];
    if(p.searchable) flags.push("searchable");
    if(p.vectorized) flags.push("vectorized");
    return "<tr><td class='p'>" + esc(p.api_name) +
      (p.required ? " <span class='req'>required</span>" : "") +
      (flags.length ? " <span class='flag'>" + esc(flags.join(" · ")) + "</span>" : "") +
      "</td><td class='k2'>" + esc(p.kind || "?") + "</td></tr>";
  }).join("") + "</table>";
}
function openDetail(title, html){
  document.getElementById("dt").textContent = title;
  document.getElementById("db").innerHTML = html;
  document.getElementById("detail").classList.add("on");
}
function closeDetail(){ document.getElementById("detail").classList.remove("on"); SEL = null; }

function showObject(name){
  var ot = (REP.object_types||[]).filter(function(o){ return key(o.api_name) === key(name); })[0];
  if(!ot) return;
  var counted = !!(REP.usage && REP.usage.available);
  var html = "";
  if(ot.display_name) html += "<div class='desc'>" + esc(ot.display_name) + "</div>";
  if(ot.description) html += "<div class='desc'>" + esc(ot.description) + "</div>";
  html += "<div class='k'>In the store</div><div class='v'>" +
    (counted ? (ot.instances ? num(ot.instances) + " nodes carry this type"
                             : "nothing in the store is of this type")
             : "not counted — the gap was not measured") + "</div>";
  html += "<div class='k'>Primary key</div><div class='v'>" + esc(ot.primary_key || "—") + "</div>";
  if(ot.title_property) html += "<div class='k'>Title property</div><div class='v'>" + esc(ot.title_property) + "</div>";
  if((ot.implements||[]).length) html += "<div class='k'>Implements</div><div class='v'>" + esc(ot.implements.join(", ")) + "</div>";
  if((ot.aliases||[]).length) html += "<div class='k'>Aliases</div><div class='v'>" + esc(ot.aliases.join(", ")) + "</div>";
  if(ot.status) html += "<div class='k'>Status</div><div class='v'>" + esc(ot.status) +
    (ot.visibility ? " · " + esc(ot.visibility) : "") + "</div>";
  html += "<div class='k'>Properties (" + (ot.properties||[]).length + ")</div>" + propTable(ot.properties);
  SEL = {kind: "object", name: ot.api_name};
  setHilite(null);
  openDetail(ot.api_name, html);
}

function showLink(name){
  var lt = (REP.link_types||[]).filter(function(l){ return key(l.api_name) === key(name); })[0];
  if(!lt) return;
  var counted = !!(REP.usage && REP.usage.available);
  var side = function(label, s){
    return "<div class='k'>" + label + " · " + esc(s.api_name || "—") + "</div><div class='v'>" +
      esc(s.object_type) + " &rarr; reaches <b>" +
      (s.cardinality === "ONE" ? "one" : s.cardinality === "MANY" ? "many" : "?") + "</b>" +
      (s.foreign_key
        ? "<div class='desc'>carries the foreign key: " + esc(s.foreign_key) + "</div>"
        // A ONE side with no key property named is not an error, but it is the
        // half of the declaration that says where the column lives, and its
        // absence is worth seeing rather than reading as "no key here".
        : (s.cardinality === "ONE"
            ? "<div class='desc' style='color:#fbbf24'>single-valued, but no foreign key property is declared</div>"
            : "")) +
      "</div>";
  };
  var html = "";
  if(lt.description) html += "<div class='desc'>" + esc(lt.description) + "</div>";
  html += "<div class='k'>Multiplicity</div><div class='v'>" + esc(lt.multiplicity || "not declared on both sides") + "</div>";
  html += "<div class='k'>In the store</div><div class='v'>" +
    (counted ? (lt.instances ? num(lt.instances) + " edges carry this type"
                             : "no edge in the store carries this type")
             : "not counted — the gap was not measured") + "</div>";
  html += side("Side A", lt.a) + side("Side B", lt.b);
  if(lt.status) html += "<div class='k'>Status</div><div class='v'>" + esc(lt.status) + "</div>";
  SEL = {kind: "link", name: lt.api_name};
  openDetail(lt.api_name, html);
}

function showInterface(name){
  var it = (REP.interfaces||[]).filter(function(i){ return key(i.api_name) === key(name); })[0];
  if(!it) return;
  var html = "";
  if(it.description) html += "<div class='desc'>" + esc(it.description) + "</div>";
  if((it.extends||[]).length) html += "<div class='k'>Extends</div><div class='v'>" + esc(it.extends.join(", ")) + "</div>";
  html += "<div class='k'>Implemented by (" + (it.implementors||[]).length + ")</div><div class='v'>" +
    ((it.implementors||[]).length ? esc(it.implementors.join(", "))
      : "<span style='color:#fbbf24'>nothing implements this interface</span>") + "</div>";
  html += "<div class='k'>Shared properties (" + (it.properties||[]).length + ")</div>" + propTable(it.properties);
  SEL = {kind: "interface", name: it.api_name};
  setHilite(key(it.api_name));
  openDetail(it.api_name, html);
}

/* ---------- the band outside the model ----------

   Types the store's records actually carry that no object or link type
   describes. On a brain with no schema this is the whole page, and it is the
   finding: a vocabulary that exists and was never declared. Drawn outside the
   diagram, dashed and in another hue, because that is what it is — not part
   of the model, and never to be mistaken for something the schema said. */
function renderStrays(rep){
  var el = document.getElementById("strays");
  var u = rep.usage || {};
  if(!straysOn || !u.available ||
     (!(u.undeclared_nodes||[]).length && !(u.undeclared_edges||[]).length)){
    el.style.display = "none";
    return;
  }
  el.style.display = "";
  var chips = function(list, truncated, total, kind){
    return "<div class='chips'>" + list.map(function(s){
      return "<span class='chip" + (s.name ? "" : " blank") + "'>" +
        (s.name ? esc(s.name) : "(no type at all)") + " <b>" + num(s.count) + "</b></span>";
    }).join("") +
    (truncated ? "<span class='chip' style='border-style:solid;color:#78716c'>and " +
      num(total - list.length) + " more</span>" : "") + "</div>";
  };
  var html = "<h3>Outside the model</h3>";
  html += "<div class='say'>" +
    (rep.draft
      /* On a draft this band is not "what nobody declared" — it is the
         deriver's own exclusions, drawn where a reader already knows to look
         for what a model leaves out. Naming the reasons matters: a type here
         may be a record, the unrecognised majority, a minority spelling that
         was deliberately not merged, a name that cannot be an API name, or
         simply small — and only the last of those is the reader's own doing. */
      ? "The draft is silent about these. They are what the deriver bucketed out — record kinds and " +
        "the writes that named no type — or withheld: a minority spelling it would not merge, a name " +
        "that cannot be an API name, or a type under the threshold."
      : rep.saved
        ? "These are the types the store's own records carry, which this schema never declares."
        : "There is no schema, so every type the store uses is here. This is the vocabulary the brain " +
          "already has and has never written down.") +
    " <b>" + num(u.undeclared_node_count) + "</b> of <b>" + num(u.nodes) + "</b> nodes and <b>" +
    num(u.undeclared_edge_count) + "</b> of <b>" + num(u.edges) + "</b> edges are under one of them.</div>";
  if((u.undeclared_nodes||[]).length){
    html += "<div class='grp'><span>node types &middot; " + num(u.undeclared_node_types) + "</span>" +
      chips(u.undeclared_nodes, u.node_list_truncated, u.undeclared_node_types) + "</div>";
  }
  if((u.undeclared_edges||[]).length){
    html += "<div class='grp'><span>edge types &middot; " + num(u.undeclared_edge_types) + "</span>" +
      chips(u.undeclared_edges, u.edge_list_truncated, u.undeclared_edge_types) + "</div>";
  }
  el.innerHTML = html;
}

/* ---------- the review half ----------

   A draft rendered as a picture alone is the picture with the work hidden
   behind it. Every one of these is a question the data cannot answer — which
   is why they are a list beside the draft rather than a value inside it — and
   nothing here answers one: this page reviews, and a person saves.

   Two readings, in this order. The tally is the glance: how much of each kind
   of question there is, because seven merge candidates and one guessed key
   are two different afternoons. The groups under it are the reading, open by
   default and foldable, because a review that starts folded is a review
   somebody skips. */
function renderDecisions(rep){
  var el = document.getElementById("decisions");
  var groups = (rep.draft && rep.decisions) || [];
  if(!groups.length){ el.style.display = "none"; el.innerHTML = ""; return; }
  el.style.display = "";

  var html = "<h3>To decide &middot; " + num(rep.decisions_total) + "</h3>";
  html += "<div class='say'>The half of a draft that is not a picture. Nothing here is decided by " +
    "this page or by the deriver that raised it — a schema is saved by a person, through " +
    "<b>ontology_save</b>.</div>";
  html += "<div class='tally'>" + groups.map(function(g, i){
    return "<span data-jump='" + i + "'>" + esc(g.title) + " <b>" + num(g.count) + "</b></span>";
  }).join("") + "</div>";

  html += groups.map(function(g, i){
    var shown = g.decisions || [];
    return "<div class='grp' data-grp='" + i + "'>" +
      "<div class='gh'><b>" + esc(g.title) + "</b><span class='n'>" + num(g.count) + "</span>" +
      "<span class='kd'>" + esc(g.kind) + "</span></div>" +
      "<div class='q'>" + esc(g.question) + "</div>" +
      shown.map(function(d){
        return "<div class='d'><div class='tg'>" + esc(d.target) + "</div>" +
          "<div class='dt'>" + esc(d.detail) + "</div>" +
          (d.evidence ? "<div class='ev'>" + esc(d.evidence) + "</div>" : "") + "</div>";
      }).join("") +
      /* The cap is on the list, never on the count — the same bargain the
         band above makes. A reader is told how many there are and shown as
         many as a page can hold, rather than shown a list that stops. */
      (g.truncated ? "<div class='more'>and " + num(g.count - shown.length) +
        " more of this kind, not listed</div>" : "") +
      "</div>";
  }).join("");
  el.innerHTML = html;

  Array.prototype.forEach.call(el.querySelectorAll(".grp .gh"), function(h){
    h.addEventListener("click", function(){ h.parentNode.classList.toggle("shut"); });
  });
  Array.prototype.forEach.call(el.querySelectorAll(".tally span"), function(t){
    t.addEventListener("click", function(){
      var g = el.querySelector(".grp[data-grp='" + t.getAttribute("data-jump") + "']");
      if(!g) return;
      g.classList.remove("shut");
      g.scrollIntoView({block: "start"});
    });
  });
}

/* ---------- and against a schema somebody did save ----------

   Only when there is one. A diff against nothing renders as "every type
   added", which reads as a change somebody made rather than as a store that
   has never been modelled — and on a real brain, never modelled is the case.

   The vocabulary here is ontology_diff's, and it is the right one exactly
   here and nowhere else on this page: both sides are schemas, so "added",
   "removed" and "breaking" describe what they say. Held against data instead
   — which is what the rest of this page does — those words would be claiming
   somebody changed something. */
function renderAgainst(rep){
  var el = document.getElementById("against");
  if(!rep.draft || !rep.against){ el.style.display = "none"; el.innerHTML = ""; return; }
  el.style.display = "";
  var changes = rep.changes || [];
  var breaking = changes.filter(function(c){ return c.breaking; }).length;

  var html = "<h3>Against the saved schema &middot; " + esc(rep.against) +
    " v" + num(rep.against_version) + "</h3>";
  html += "<div class='say'>This store already has a schema, and nothing here is applied to it. " +
    "This is what would differ if the draft replaced it, taken through the same comparison a version " +
    "bump goes through: <b>" + num(rep.changes_total) + "</b> " +
    plural(rep.changes_total, "change", "changes") +
    (rep.breaking
      ? ", of which <b>" + num(breaking) + "</b> listed here would stop something already stored from validating."
      : ", none of which would invalidate anything already stored.") + "</div>";
  html += changes.map(function(c){
    return "<div class='ch" + (c.breaking ? " brk" : "") + "'><i>" + esc(c.kind) + "</i>" +
      "<b>" + esc(c.target) + "</b><span>" + esc(c.detail) + "</span></div>";
  }).join("");
  if(rep.changes_total > changes.length){
    html += "<div class='ch'><i></i><span>and " + num(rep.changes_total - changes.length) +
      " more, not listed</span></div>";
  }
  el.innerHTML = html;
}

/* ---------- reading it ----------

   Relative, for the same reason the scene page's stream is: this page is
   mounted behind other applications' proxies, and an absolute path would leave
   the mount point and land on whatever the host serves at /api.

   gap=0 goes to the server, not just to the paint. It is the one option that
   changes what is read — the store's own vocabulary is two aggregate scans —
   and sending it means an embedder showing the model alone costs the store
   nothing for the half it is not showing. Same bargain the contract panel
   makes by not fetching while it is folded. */
function load(){
  var q = [];
  if(draftOn){
    /* One endpoint, two verbs. The draft is the same question about the same
       store asked as "what could be declared", so it arrives through the same
       fetch at the same instant rather than through a second one that could
       disagree with this one about what the store held.

       gap has nothing to switch off here: deriving a draft reads the type
       counts as part of deriving it, so the overlay is already paid for and
       turning it off would buy nothing. */
    q.push("draft=1");
    if(minWant !== "") q.push("min=" + encodeURIComponent(minWant));
    if(minNodesWant !== "") q.push("min_nodes=" + encodeURIComponent(minNodesWant));
    if(minEdgesWant !== "") q.push("min_edges=" + encodeURIComponent(minEdgesWant));
  } else if(!gapOn) q.push("gap=0");
  if(schemaWant) q.push("schema=" + encodeURIComponent(schemaWant));
  fetch("api/ontology" + (q.length ? "?" + q.join("&") : ""), {cache: "no-store"})
    .then(function(r){ return r.json(); })
    .then(render)
    .catch(function(err){
      if(draftOn){
        render({draft: true, state: "undraftable", reason: String(err), schemas: [],
          object_types: [], link_types: [], interfaces: [], usage: {}, decisions: [], notes: []});
        return;
      }
      render({state: "unreadable", reason: String(err), schemas: [],
        object_types: [], link_types: [], interfaces: [], usage: {}});
    });
}

function render(rep){
  REP = rep;
  renderHead(rep);
  renderSay(rep);
  renderDiagram(rep);
  renderStrays(rep);
  renderDecisions(rep);
  renderAgainst(rep);
  var b = document.getElementById("boot");
  b.classList.add("gone");
  setTimeout(function(){ b.style.display = "none"; }, 500);
  // A selection survives a refresh, or every reload would close the panel a
  // reader had open on the type they were studying.
  if(SEL){
    if(SEL.kind === "object") showObject(SEL.name);
    else if(SEL.kind === "link") showLink(SEL.name);
    else showInterface(SEL.name);
  }
}

/* ---------- chrome ---------- */
var FOLD_KEY = "cortexdb.ontology.folded";
function foldable(){ return Array.prototype.slice.call(document.querySelectorAll(".panel[data-label]")); }
function saveFolds(){
  var ids = foldable().filter(function(p){ return p.classList.contains("folded"); })
                      .map(function(p){ return p.id; });
  try { localStorage.setItem(FOLD_KEY, ids.join(",")); } catch(e){}
}
function restoreFolds(){
  var stored = null;
  try { stored = localStorage.getItem(FOLD_KEY); } catch(e){}
  var ids = stored ? stored.split(",") : [];
  foldable().forEach(function(p){
    var folded = ids.indexOf(p.id) >= 0;
    p.classList.toggle("folded", folded);
    var b = p.querySelector(".fold");
    if(b) b.title = folded ? "Expand" : "Collapse";
  });
}
foldable().forEach(function(p){
  p.querySelector(".fold").addEventListener("click", function(ev){
    ev.stopPropagation();
    var folded = p.classList.toggle("folded");
    this.title = folded ? "Expand" : "Collapse";
    saveFolds();
  });
});
restoreFolds();

document.getElementById("dx").addEventListener("click", closeDetail);
document.getElementById("reload").addEventListener("click", load);
document.getElementById("gapbtn").addEventListener("click", function(){
  gapOn = !gapOn;
  this.classList.toggle("on", gapOn);
  load();
});
document.getElementById("straybtn").addEventListener("click", function(){
  straysOn = !straysOn;
  this.classList.toggle("on", straysOn);
  if(REP) renderStrays(REP);
});
document.getElementById("draftbtn").addEventListener("click", function(){
  draftOn = !draftOn;
  this.classList.toggle("on", draftOn);
  syncDraftChrome();
  // A selection is a type in the schema that was being drawn, and the draft's
  // schema is a different one. Kept, and the panel would reopen on a name
  // that now means something else.
  SEL = null;
  closeDetail();
  setHilite(null);
  load();
});
// The gap switch is meaningless on a draft — see load() — so it is disabled
// rather than left looking like a lever that does nothing.
function syncDraftChrome(){
  document.getElementById("draftbtn").classList.toggle("on", draftOn);
  var g = document.getElementById("gapbtn");
  g.disabled = draftOn;
  // Unlit while it is inert, and back to whatever it was when the draft is
  // switched off — the switch keeps its position, it just stops claiming to
  // be holding one.
  g.classList.toggle("on", gapOn && !draftOn);
  g.title = draftOn
    ? "A draft is derived from the store's own counts, so the overlay is already taken"
    : "Count what the store actually holds and hold the declarations against it";
}
document.getElementById("gapbtn").classList.toggle("on", gapOn);
document.getElementById("straybtn").classList.toggle("on", straysOn);
syncDraftChrome();

// The layout depends on the width, and an SVG does not reflow the way the
// panels around it do — left alone it keeps whatever width the window had when
// the page loaded, so widening the window grows the frame around a diagram
// that stays put.
var resizeTimer = null;
window.addEventListener("resize", function(){
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(function(){ if(REP) renderDiagram(REP); }, 160);
});

load();
// Nothing is read while nobody is looking. The gap is two aggregate scans over
// the store, and a tab left open in the background has no reader to be stale
// to; it catches up the moment it comes forward.
setInterval(function(){ if(!document.hidden) load(); }, ONTOLOGY_MS);
document.addEventListener("visibilitychange", function(){ if(!document.hidden) load(); });
</script>
</body>
</html>`
