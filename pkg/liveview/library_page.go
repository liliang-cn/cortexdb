package liveview

// libraryHTML is the library page: the brain as a reading room (see library.go
// for what is shelved where and why).
//
// The scene is plain three.js rather than the graph page's 3d-force-graph,
// because nothing on it moves under a simulation: every case, book and plaque
// has a place that follows from the data, laid out once per snapshot version.
// The page is built so the books can carry meaning:
//
//   - One InstancedMesh draws every book, so a brain of thousands of memories
//     is one draw call, and a click on a book is one raycast.
//   - A book is read where it stands. Opening one carries it to the lectern and
//     turns the camera to it; closing it puts it back. A call that recalls
//     books (reported over the activity stream) lights them in place and runs
//     a thread from each to the reading table — nothing is taken off a shelf.
//   - The scene is rebuilt, not patched, when the snapshot changes or the mode
//     flips. A library of a few thousand boxes builds in tens of milliseconds,
//     and a rebuild cannot drift from the data the way an incremental patch can.
//
// Every request is relative, so the page works wherever the view is mounted.
// The day/night setting is the graph page's own (same storage key, same ?mode=),
// so switching pages does not switch the lights.
const libraryHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>CortexDB — library</title>
<style>
  :root{--ink:#2a1d12;--ink2:#5b4632;--mute:#8a7258;--paper:#fbf3e1;--panel:rgba(251,243,225,.93);--line:#e3d2ae;
    --brass:#b8862f;--green:#1f6b52;--bg:#e9dcc0;
    --serif:"Iowan Old Style","Palatino Linotype",Palatino,Georgia,"Songti SC","Noto Serif SC","Source Han Serif SC",serif;
    --sans:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif}
  [data-mode="dark"]{--ink:#f4e6c8;--ink2:#d9c39a;--mute:#a68e6c;--paper:#2a1f15;--panel:rgba(36,26,18,.92);--line:#4a3826;
    --brass:#d0a24a;--green:#7fd1ae;--bg:#1b1712}
  *{box-sizing:border-box}
  html,body{margin:0;height:100%;overflow:hidden;background:var(--bg);color:var(--ink);font-family:var(--sans);-webkit-text-size-adjust:100%}
  #stage{position:fixed;inset:0;touch-action:none}
  .labels{position:absolute;inset:0;pointer-events:none}
  .panel{position:fixed;background:var(--panel);border:1.5px solid var(--line);border-radius:14px;
    box-shadow:0 18px 40px -18px rgba(40,22,8,.45);backdrop-filter:blur(6px);-webkit-backdrop-filter:blur(6px)}
  #head{left:calc(16px + env(safe-area-inset-left));top:calc(16px + env(safe-area-inset-top));padding:14px 16px 12px;width:330px;max-width:calc(100vw - 32px)}
  #head h1{margin:0;font-family:var(--serif);font-size:26px;line-height:1.1}
  #head .src{font-size:11.5px;color:var(--mute);margin-top:3px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  #head nav{display:flex;gap:6px;margin-top:10px;align-items:center}
  #head nav a,#head nav button{font:inherit;font-size:12.5px;color:var(--ink2);text-decoration:none;border:1px solid var(--line);
    background:transparent;border-radius:999px;padding:3px 10px;cursor:pointer}
  #head nav .on{background:var(--ink);color:var(--paper);border-color:var(--ink)}
  #head nav #modebtn{margin-left:auto;min-width:34px}
  .stats{display:grid;grid-template-columns:repeat(4,1fr);gap:6px;margin-top:10px}
  .stats div{border-top:1px solid var(--line);padding-top:6px}
  .stats b{display:block;font-family:var(--serif);font-size:20px;line-height:1.1}
  .stats span{font-size:10.5px;color:var(--mute)}
  #find{left:calc(16px + env(safe-area-inset-left));top:calc(208px + env(safe-area-inset-top));width:330px;max-width:calc(100vw - 32px);padding:8px}
  #find input{width:100%;font:inherit;font-size:14px;color:var(--ink);background:transparent;border:0;outline:0;padding:6px 8px}
  #results{max-height:42vh;overflow:auto}
  #results .h{font-size:10.5px;letter-spacing:.1em;color:var(--mute);padding:8px 8px 2px}
  #results a{display:block;padding:6px 8px;border-radius:8px;color:var(--ink);text-decoration:none;font-size:13px;cursor:pointer}
  #results a:hover{background:rgba(184,134,47,.14)}
  #results a small{display:block;color:var(--mute);font-size:11px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  #side{right:calc(16px + env(safe-area-inset-right));top:calc(16px + env(safe-area-inset-top));width:360px;max-height:calc(100vh - 140px);overflow:auto;padding:14px 16px;display:none}
  #side.open{display:block}
  #side h2{margin:0 0 2px;font-family:var(--serif);font-size:20px;line-height:1.25;word-break:break-word}
  #side .sub{font-size:12px;color:var(--mute)}
  #side .sec{font-size:10.5px;letter-spacing:.1em;color:var(--brass);margin:14px 0 6px;font-weight:600}
  #side a{color:var(--green);cursor:pointer;text-decoration:underline;text-underline-offset:3px}
  #side .chips{display:flex;flex-wrap:wrap;gap:6px}
  #side .chips a{text-decoration:none;font-size:12px;padding:3px 9px;border-radius:999px;border:1px solid var(--line);color:var(--ink2)}
  #side .chips a.p{border-color:var(--green);color:var(--green);font-weight:600}
  #side .rel{font-size:12.5px;padding:3px 0;display:flex;gap:8px}
  #side .rel code{font-size:11px;color:var(--mute);min-width:96px}
  #side .x{position:absolute;right:10px;top:8px;border:0;background:transparent;font-size:18px;color:var(--mute);cursor:pointer}
  #feed{right:calc(16px + env(safe-area-inset-right));bottom:calc(16px + env(safe-area-inset-bottom));width:360px;max-width:calc(100vw - 32px);padding:10px 14px}
  #feed .h{font-size:10.5px;letter-spacing:.1em;color:var(--mute);display:flex;gap:6px;align-items:center}
  #feed .led{width:7px;height:7px;border-radius:50%;background:#9ca3af}
  #feed .led.live{background:#22c55e}
  #feed div.l{font-size:12.5px;padding:5px 0;border-top:1px solid var(--line);display:flex;gap:8px;color:var(--ink2)}
  #feed div.l i{font-style:normal;color:var(--mute);margin-left:auto;white-space:nowrap}
  #pager{left:50%;bottom:calc(18px + env(safe-area-inset-bottom));transform:translateX(-50%);padding:6px;display:none;gap:4px;font-size:13.5px}
  #pager.open{display:flex}
  #pager button{font:inherit;border:0;background:transparent;color:var(--ink);padding:7px 12px;border-radius:9px;cursor:pointer}
  #pager .on{background:var(--ink);color:var(--paper)}
  #pager .close{border:1.5px solid var(--brass)}
  #tip{position:fixed;pointer-events:none;display:none;max-width:320px;padding:7px 10px;border-radius:9px;background:var(--panel);
    border:1px solid var(--line);font-size:12px;color:var(--ink2);box-shadow:0 10px 24px -12px rgba(40,22,8,.5)}
  #tip b{display:block;color:var(--ink);font-size:12.5px;word-break:break-all}
  #note{left:50%;top:calc(16px + env(safe-area-inset-top));transform:translateX(-50%);padding:8px 14px;font-size:13px;display:none}
  .plaque{pointer-events:auto;cursor:pointer;background:linear-gradient(#3e2717,#2c1b10);color:#f3dfb0;border:2px solid #c99a3b;border-radius:8px;
    padding:5px 11px 6px;text-align:center;font-family:var(--serif);box-shadow:0 8px 18px -8px rgba(30,16,6,.7);white-space:nowrap}
  .plaque b{display:block;font-size:15px}
  .plaque span{display:block;font-family:var(--sans);font-size:10.5px;color:#d6b97c;margin-top:1px}
  body.reading .lect{display:none}
  .plaque.sel{border-color:#ffe08a;box-shadow:0 0 0 3px rgba(255,224,138,.35),0 8px 18px -8px rgba(30,16,6,.7)}
  .shelf{font-size:11px;font-weight:600;color:#f6e8c8;background:rgba(40,24,12,.8);border:1px solid rgba(201,154,59,.6);padding:2px 7px;border-radius:5px;white-space:nowrap}
  .shelf em{font-style:normal;color:#e9c46a;margin-left:5px;font-weight:500}
  .tag{pointer-events:auto;cursor:pointer;font-size:11.5px;color:#2a1d12;background:#fbf3e1;border:1px solid #b8862f;border-radius:6px;
    padding:3px 8px;text-align:center;white-space:nowrap;box-shadow:0 4px 10px -6px rgba(30,16,6,.6)}
  .tag small{display:block;font-size:9.5px;color:#1f6b52;font-family:ui-monospace,Menlo,monospace}
  .tag.center{background:#1f5e4b;color:#fbf3e1;border-color:#c99a3b;padding:5px 12px;font-size:14px;font-weight:600}
  .tag.center small{color:#bfe3d3}
  .read{font-size:11.5px;font-weight:700;color:#5a3a12;background:#ffe7a8;border:1.5px solid #d9a33a;padding:3px 10px;border-radius:999px;
    white-space:nowrap;box-shadow:0 0 18px 2px rgba(242,184,75,.55);max-width:360px;overflow:hidden;text-overflow:ellipsis}
  @media (max-width:760px){
    #head{width:auto;right:16px}
    #head .stats{display:none}
    #find{top:auto;bottom:calc(16px + env(safe-area-inset-bottom));right:16px;width:auto}
    #feed{display:none}
    #side{top:auto;bottom:calc(76px + env(safe-area-inset-bottom));left:16px;right:16px;width:auto;max-height:46vh}
    #pager{bottom:calc(76px + env(safe-area-inset-bottom))}
    #side.open ~ #pager.open{bottom:calc(46vh + 90px)}
    .plaque{padding:3px 7px 4px;border-width:1.5px}.plaque b{font-size:11px}.plaque span{display:none}
    .shelf,.tag{font-size:10px}
  }
</style>
<script type="importmap">{"imports":{
  "three":"https://unpkg.com/three@0.168.0/build/three.module.js",
  "three/addons/":"https://unpkg.com/three@0.168.0/examples/jsm/"
}}</script>
</head>
<body>
<div id="stage"></div>
<div class="panel" id="head">
  <h1 id="title">CortexDB — library</h1>
  <div class="src" id="src"></div>
  <div class="stats" id="stats"></div>
  <nav><a href="./" id="tograph">graph</a><a class="on" id="tolib">library</a><a href="ontology" id="toonto">ontology</a>
    <button id="modebtn" type="button" title="Day / night">☾</button></nav>
</div>
<div class="panel" id="find"><input id="q" type="search" autocomplete="off" spellcheck="false"><div id="results"></div></div>
<div class="panel" id="side"><button class="x" id="sidex" type="button" aria-label="close">×</button><div id="sidebody"></div></div>
<div class="panel" id="feed"><div class="h"><span class="led" id="led"></span><span id="feedh"></span></div><div id="feedlist"></div></div>
<div class="panel" id="pager"><button id="prev" type="button">‹</button><button class="on" id="pageno" type="button"></button><button id="next" type="button">›</button><button class="close" id="close" type="button"></button></div>
<div class="panel" id="note"></div>
<div id="tip"></div>
<script type="module">
import * as THREE from "three";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";
import { RoundedBoxGeometry } from "three/addons/geometries/RoundedBoxGeometry.js";
import { RoomEnvironment } from "three/addons/environments/RoomEnvironment.js";
import { CSS2DRenderer, CSS2DObject } from "three/addons/renderers/CSS2DRenderer.js";

var Q = new URLSearchParams(location.search);
var LANG = ((Q.get("lang") || navigator.language || "en").toLowerCase().indexOf("zh") === 0) ? "zh" : "en";
var STR = {
  en: {title:"CortexDB — library", books:"books", shelves:"shelves", cards:"index cards", docs:"documents",
    graph:"graph", library:"library", ontology:"ontology", find:"Find a book or an index card…", hBooks:"BOOKS", hCards:"INDEX CARDS",
    stacks:"General stacks", stacksSub:"memories that name no project", annex:"Annex", annexSub:"small projects",
    reference:"Reference", catalog:"Card catalogue", lectern:"Lectern", wingSub:function(p,b){return p+" shelves · "+b+" books";},
    feed:"WHAT IS HAPPENING", quiet:"Nothing has happened yet.", reading:"reading", close:"Close, put it back",
    page:function(a,b){return "page "+a+" / "+b;}, shelf:"shelf", index:"Index · what this book mentions",
    seeAlso:"See also · books sharing the most index terms", shared:function(n){return n+" shared";},
    provenance:"Provenance", noRecords:"This view's source cannot open a book; it can only show where it stands.",
    cardBooks:function(n){return n+" books mention it";}, rels:"Relations", more:function(n){return "and "+n+" more";},
    whereBooks:"WHERE ITS BOOKS ARE", none:"nothing", notFound:"Not in this snapshot.", day:"Day", night:"Night",
    findOff:"This source cannot be searched for index cards.", docsOff:"documents: the source cannot be asked",
    grade:"grade", written:"written", source:"source", producer:"producer", relTable:"relation table"},
  zh: {title:"CortexDB · 记忆图书馆", books:"本记忆", shelves:"个书架", cards:"张索引卡", docs:"篇文档",
    graph:"图谱", library:"图书馆", ontology:"本体", find:"找一本书，或一张索引卡…", hBooks:"书", hCards:"索引卡",
    stacks:"总书库", stacksSub:"没写明项目的记忆", annex:"小项目回廊", annexSub:"零散的小项目",
    reference:"参考书区", catalog:"卡片目录", lectern:"讲台", wingSub:function(p,b){return p+" 个书架 · "+b+" 本";},
    feed:"正在发生", quiet:"还没有动静。", reading:"正在读", close:"合上，放回书架",
    page:function(a,b){return "第 "+a+" / "+b+" 页";}, shelf:"书架", index:"书末索引 · 这本书提到的",
    seeAlso:"参见 · 共用索引词最多的书", shared:function(n){return "共 "+n+" 个索引词";},
    provenance:"来历", noRecords:"这个数据源不能打开书，只能看到书在哪个书架上。",
    cardBooks:function(n){return n+" 本书提到它";}, rels:"关系", more:function(n){return "另有 "+n+" 条";},
    whereBooks:"提到它的书在哪", none:"没有", notFound:"这次快照里没有它。", day:"白天", night:"夜晚",
    findOff:"这个数据源不能搜索索引卡。", docsOff:"文档：数据源无法查询",
    grade:"等级", written:"写入", source:"出处", producer:"写入者", relTable:"关系桌"}
}[LANG];
document.documentElement.lang = LANG === "zh" ? "zh-CN" : "en";

// ---- day and night: the graph page's own setting, so changing pages keeps the lights
var MODE_KEY = "cortexdb.liveview.mode", MODES = ["auto", "light", "dark"];
function stored(k){ try { return localStorage.getItem(k) || ""; } catch(e){ return ""; } }
var modeSetting = MODES.indexOf(Q.get("mode")) >= 0 ? Q.get("mode") : (MODES.indexOf(stored(MODE_KEY)) >= 0 ? stored(MODE_KEY) : "auto");
var SYS_DARK = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;
function night(){ return modeSetting === "dark" || (modeSetting === "auto" && !!(SYS_DARK && SYS_DARK.matches)); }
var NIGHT = night();
document.documentElement.setAttribute("data-mode", NIGHT ? "dark" : "light");
// links keep the page's query (mode, lang, an embedder's theme) when switching pages
["tograph", "toonto"].forEach(function(id){ var a = document.getElementById(id); a.href = a.getAttribute("href") + location.search; });

var $ = function(id){ return document.getElementById(id); };
$("title").textContent = STR.title; document.title = STR.title;
$("tograph").textContent = STR.graph; $("tolib").textContent = STR.library; $("toonto").textContent = STR.ontology;
$("q").placeholder = STR.find; $("feedh").textContent = STR.feed; $("close").textContent = STR.close;

function getJSON(url){ return fetch(url, {cache:"no-store"}).then(function(r){ if(!r.ok) throw new Error(r.status + ""); return r.json(); }); }
function esc(s){ return String(s == null ? "" : s).replace(/[&<>"]/g, function(c){ return {"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;"}[c]; }); }
function hash(s){ var h = 2166136261; for(var i = 0; i < s.length; i++){ h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); } return (h >>> 0) / 4294967296; }
function short(id){ var i = id.indexOf(":"); return i >= 0 ? id.slice(i + 1) : id; }
function note(text, ms){ var n = $("note"); n.textContent = text; n.style.display = "block"; clearTimeout(note.t); note.t = setTimeout(function(){ n.style.display = "none"; }, ms || 3500); }

// ---- renderer, camera, controls
var renderer = new THREE.WebGLRenderer({antialias:true});
renderer.setPixelRatio(Math.min(2, window.devicePixelRatio || 1));
renderer.setSize(innerWidth, innerHeight);
renderer.shadowMap.enabled = true;
renderer.shadowMap.type = THREE.PCFSoftShadowMap;
renderer.toneMapping = THREE.NeutralToneMapping;
$("stage").appendChild(renderer.domElement);
var labels = new CSS2DRenderer();
labels.setSize(innerWidth, innerHeight);
labels.domElement.className = "labels";
$("stage").appendChild(labels.domElement);
// near at 1, not lower: depth precision is spent close to the camera, and the
// hall is seen from tens of metres away
var camera = new THREE.PerspectiveCamera(32, innerWidth / innerHeight, 1, 900);
var controls = new OrbitControls(camera, renderer.domElement);
controls.enableDamping = true; controls.dampingFactor = 0.08;
controls.minPolarAngle = 0.2; controls.maxPolarAngle = 1.32;
controls.screenSpacePanning = false;
var scene = new THREE.Scene();
var envTex = new THREE.PMREMGenerator(renderer).fromScene(new RoomEnvironment(), 0.04).texture;

// ---- palette: walnut, oak, parchment, brass and leathers. No violet anywhere.
var LEATHER = [
  ["#1f5e4b","#2f7a5f","#17483a","#3b8a6c","#245f4f"],
  ["#8a2f2f","#a3443a","#6e2424","#b0533f","#7d3328"],
  ["#2c4a6b","#3b5f86","#23395a","#4a6f96","#30506f"],
  ["#b8862f","#9c6f22","#c99a3b","#a87a2a","#d0a24a"],
  ["#b5552b","#9a4524","#c86a3a","#a85230"],
  ["#2f6f73","#3c8287","#245a5e","#4a9196"],
  ["#5e6b2a","#717f33","#4c5722","#86944a"]
];
var BROWN = ["#6b4a32","#845c3e","#5a3c28","#93694a","#7a553a","#a07a55"];
var CREAM = ["#e3d3ad","#d6c299","#cdb68a","#e8dbbb"];
var EARTH = ["#7a553a","#8a2f2f","#2c4a6b","#b8862f","#1f5e4b","#b5552b"];

var geoCache = {};
function rbox(w, h, d, r){ r = r == null ? 0.04 : r; var k = w+":"+h+":"+d+":"+r;
  if(!geoCache[k]) geoCache[k] = r > 0 ? new RoundedBoxGeometry(w, h, d, 2, Math.min(r, w/2, h/2, d/2) - 0.001) : new THREE.BoxGeometry(w, h, d);
  return geoCache[k]; }
function mat(c, o){ var p = {color:c, roughness:0.7, metalness:0}; if(o) for(var k in o) p[k] = o[k]; return new THREE.MeshStandardMaterial(p); }
var M = {};
function materials(){
  M = {walnut:mat("#5b3a26",{roughness:0.55}), walnutDark:mat("#43291a",{roughness:0.6}), oak:mat("#a7764a",{roughness:0.6}),
    wall:mat(NIGHT ? "#3a3026" : "#efe2c6",{roughness:0.9}), wainscot:mat("#5a3a26",{roughness:0.6}),
    brass:mat("#c99a3b",{roughness:0.3, metalness:0.7}), carpet:mat("#1f5e4b",{roughness:0.95}), parchment:mat("#f1e4c4",{roughness:0.85}),
    glass:mat(NIGHT ? "#1d2c45" : "#fdf2d6",{roughness:0.1, emissive:NIGHT ? "#0e1828" : "#fff3d0", emissiveIntensity:NIGHT ? 0.6 : 0.9}),
    lamp:mat("#1f6b4f",{roughness:0.25, metalness:0.2, emissive:"#1f6b4f", emissiveIntensity:NIGHT ? 0.5 : 0}),
    wax:mat("#f4ead2",{roughness:0.5, emissive:"#ffcf7a", emissiveIntensity:NIGHT ? 0.35 : 0.05}),
    flame:new THREE.MeshBasicMaterial({color:"#ffd27a"}),
    glow:new THREE.MeshBasicMaterial({color:"#ffd46e", transparent:true, opacity:NIGHT ? 0.4 : 0.28, depthWrite:false}),
    find:new THREE.MeshBasicMaterial({color:"#45d3a0", transparent:true, opacity:0.34, depthWrite:false}),
    thread:new THREE.MeshBasicMaterial({color:"#f2b84b", transparent:true, opacity:NIGHT ? 0.8 : 0.65, depthWrite:false}),
    ink:new THREE.MeshBasicMaterial({color:"#1f8a6a", transparent:true, opacity:0.7, depthWrite:false}),
    line:new THREE.MeshBasicMaterial({color:"#5a3a26"})};
}
function add(parent, geo, m, x, y, z, noCast){ var o = new THREE.Mesh(geo, m); o.position.set(x, y, z); o.castShadow = !noCast; o.receiveShadow = true; parent.add(o); return o; }
function blk(parent, m, w, h, d, x, y, z, r){ return add(parent, rbox(w, h, d, r), m, x, y + h/2, z); }
function label(parent, html, cls, x, y, z, onclick){
  var el = document.createElement("div"); el.className = cls; el.innerHTML = html;
  if(onclick){ el.addEventListener("pointerdown", function(e){ e.stopPropagation(); }); el.addEventListener("click", function(e){ e.stopPropagation(); onclick(); }); }
  var o = new CSS2DObject(el); o.position.set(x, y, z); parent.add(o); return o;
}

// ---- state
var LIB = null, WORLD = null, BOOKS = [], BYID = {}, MESH = null, PLAN = null;
var focusWing = -1, openCardId = "", openBookState = null, fx = null;

// ---- layout: where every case, book and plaque goes. Pure function of the library.
// STACK_STEP keeps neighbouring stacks' cornices (case width + 0.4) apart
var LEVEL_H = 0.58, BOOK_D = 0.34, STACK_STEP = 3.95;
function bookWidth(terms){ return 0.07 + Math.min(1, (terms || 0) / 12) * 0.13; }
function packCases(shelves, perCase){
  var cases = [], cur = [], starts = [];
  shelves.forEach(function(sh){
    var rest = sh.books.slice(), first = true;
    while(rest.length){
      var room = perCase - cur.length;
      if(room <= 0){ cases.push({books:cur, starts:starts}); cur = []; starts = []; continue; }
      if(first) starts.push({name:sh.project, at:cur.length, n:sh.books.length, id:sh.id});
      first = false;
      cur = cur.concat(rest.slice(0, room)); rest = rest.slice(room);
    }
  });
  if(cur.length) cases.push({books:cur, starts:starts});
  return cases;
}
function plan(lib){
  var P = {cases:[], plaques:[], wings:[]};
  var GW = 3.4, GAP = 0.45, SIDEW = 3.3;
  // the largest wing: grand cases at the back, centred
  var main = lib.wings[0], grand = main ? packCases(main.shelves, 54) : [];
  var grandW = grand.length * (GW + GAP) - GAP;
  // the general stacks, along the back wall either side of the grand cases
  var stackCases = []; for(var i = 0; i < lib.stacks.length; i += 84) stackCases.push(lib.stacks.slice(i, i + 84));
  var stL = stackCases.slice(0, Math.ceil(stackCases.length / 2)), stR = stackCases.slice(Math.ceil(stackCases.length / 2));
  // side wings: every other wing, and the annex as one more
  var side = lib.wings.slice(1).map(function(w, i){ return {name:w.name, sub:STR.wingSub(w.shelves.length, w.books), shelves:w.shelves, pal:LEATHER[(i + 1) % LEATHER.length], wing:i + 1}; });
  if(lib.annex.length){ var nb = 0; lib.annex.forEach(function(s){ nb += s.books.length; });
    side.push({name:STR.annex, sub:STR.wingSub(lib.annex.length, nb), shelves:lib.annex, pal:EARTH, wing:-2}); }
  side.forEach(function(w){ w.cases = packCases(w.shelves, 72); w.width = w.cases.length * (SIDEW + GAP) - GAP; });
  // Each side of the nave is rows of up to ROW cases, nearest the nave first.
  // Small wings share a row: a wing of one case does not need a row to itself,
  // and a hall laid out one wing per row is mostly floor.
  var ROW = 3, NAVE = 3.2;
  var sides = [[], []], fill = [[], []];
  side.forEach(function(w, i){
    var s = fill[0].length <= fill[1].length ? 0 : 1, rows = fill[s], last = rows[rows.length - 1];
    var used = last ? last.reduce(function(n, x){ return n + x.cases.length; }, 0) : ROW;
    if(!last || used + w.cases.length > ROW) rows.push([w]); else last.push(w);
  });
  var rows = Math.max(fill[0].length, fill[1].length, 1);
  var REF = lib.docs.length ? 4.5 : 0;
  var halfW = Math.max(14, grandW / 2 + (Math.max(stL.length, stR.length)) * STACK_STEP + 2.5, NAVE + ROW * (SIDEW + GAP) + 4 + REF);
  var back = -(8 + rows * 6.4), front = 12.5;
  P.hall = {x0:-halfW, x1:halfW, z0:back - 2.4, z1:front};
  // grand
  grand.forEach(function(c, i){ P.cases.push({x:-grandW/2 + GW/2 + i * (GW + GAP), z:back, rot:0, levels:9, width:GW, books:c.books, starts:c.starts, pal:LEATHER[0], wing:0}); });
  if(main){ P.plaques.push({x:0, z:back + 3.4, rot:0, title:main.name, sub:STR.wingSub(main.shelves.length, main.books), wing:0}); }
  P.wings[0] = {x:0, z:back + 1, w:Math.max(grandW, 6)};
  // stacks
  stL.forEach(function(b, i){ P.cases.push({x:-grandW/2 - 2.3 - i * STACK_STEP, z:back - 0.2, rot:0, levels:8, width:3.4, books:b, starts:[], pal:BROWN, wing:-1}); });
  stR.forEach(function(b, i){ P.cases.push({x:grandW/2 + 2.3 + i * STACK_STEP, z:back - 0.2, rot:0, levels:8, width:3.4, books:b, starts:[], pal:BROWN, wing:-1}); });
  if(lib.stacks.length){ P.plaques.push({x:(stR.length ? grandW/2 + 2.3 + (stR.length - 1) * STACK_STEP / 2 : -grandW/2 - 2.3), z:back + 3.2, rot:0, title:STR.stacks, sub:lib.stacks.length + " · " + STR.stacksSub, wing:-1}); }
  [-1, 1].forEach(function(sign, si){
    fill[si].forEach(function(row, r){
      var z = back + 7.4 + r * 6.4, slot = 0;
      row.forEach(function(w){
        var x0 = NAVE + 1.8 + SIDEW / 2 + slot * (SIDEW + GAP);
        w.cases.forEach(function(c, i){ P.cases.push({x:sign * (x0 + i * (SIDEW + GAP)), z:z, rot:0, levels:5, width:SIDEW, books:c.books, starts:c.starts, pal:w.pal, wing:w.wing}); });
        var mid = sign * (x0 + (w.cases.length - 1) * (SIDEW + GAP) / 2);
        P.plaques.push({x:mid, z:z + 2.3, rot:0, title:w.name, sub:w.sub, wing:w.wing});
        var key = w.wing === -2 ? side.length : w.wing;
        P.wings[key] = {x:mid, z:z, w:w.width};
        if(w.wing === -2) P.annexIndex = side.length;
        slot += w.cases.length;
      });
    });
  });
  // reference room on the left wall
  P.docs = [];
  if(lib.docs.length){ for(var k = 0; k < lib.docs.length; k += 40) P.docs.push(lib.docs.slice(k, k + 40)); }
  P.desk = new THREE.Vector3(0, 1.2, front - 5.2);
  P.lectern = new THREE.Vector3(0, 1.32, front - 8.6);
  P.catalog = new THREE.Vector3(NAVE + 6.5, 0, front - 4);
  P.table = new THREE.Vector3(-NAVE - 8.5, 0, front - 6.0);
  return P;
}

// ---- building the room
function parquet(){
  var c = document.createElement("canvas"); c.width = c.height = 512; var g = c.getContext("2d");
  var A = NIGHT ? "#6e4c32" : "#c49a68", B = NIGHT ? "#5f402a" : "#b48858";
  for(var i = 0; i < 16; i++) for(var j = 0; j < 4; j++){
    g.fillStyle = (i + j) % 2 ? A : B; g.fillRect(j * 128 + (i % 2) * 64 - 64, i * 32, 128, 32); g.fillRect(j * 128 + (i % 2) * 64 + 64, i * 32, 128, 32);
    g.strokeStyle = "rgba(60,35,18,0.25)"; g.lineWidth = 2; g.strokeRect(j * 128 + (i % 2) * 64, i * 32, 128, 32);
  }
  var t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace; t.wrapS = t.wrapT = THREE.RepeatWrapping; t.anisotropy = 8; return t;
}
function walls(W, H){
  var WALL_H = 11;
  // cap is the cornice's height. It stands a little above the wall it sits on
  // (whose top is exactly WALL_H), and the two walls get different ones, so no
  // two faces ever share a plane: coplanar tops z-fight and the edge flickers.
  function wall(len, n, cap){
    // n windows between n+1 piers, laid end to end: no two boxes overlap and no
    // two faces share a plane, because coplanar faces z-fight and flicker.
    var g = new THREE.Group(), seg = len / (n + 0.42), pierW = seg * 0.42, winW = seg - pierW;
    var gh = WALL_H - 3.8 - winW / 2;
    for(var i = 0; i < n; i++){
      var x0 = -len/2 + i * seg, wc = x0 + pierW + winW/2;
      blk(g, M.wall, pierW, WALL_H, 0.8, x0 + pierW/2, 0, 0, 0.02);
      blk(g, M.wall, winW, 2.2, 0.8, wc, 0, 0, 0.02);
      blk(g, M.wall, winW, 1.6, 0.8, wc, WALL_H - 1.6, 0, 0.02);
      // the glass sits back from the wall's faces, the arch above the pane
      add(g, new THREE.BoxGeometry(winW - 0.02, gh, 0.12), M.glass, wc, 2.2 + gh/2, 0, true);
      var arch = new THREE.Shape(); arch.moveTo(-winW/2 + 0.01, 0); arch.absarc(0, 0, winW/2 - 0.01, Math.PI, 0, true); arch.lineTo(-winW/2 + 0.01, 0);
      add(g, new THREE.ShapeGeometry(arch, 24), M.glass, wc, 2.2 + gh + 0.001, 0.03, true);
      for(var k = 1; k < 3; k++) blk(g, M.walnutDark, 0.08, gh - 0.02, 0.2, wc - winW/2 + winW * k / 3, 2.21, 0, 0.01);
      for(var k2 = 1; k2 < 4; k2++) blk(g, M.walnutDark, winW - 0.04, 0.08, 0.17, wc, 2.2 + gh * k2 / 4, 0, 0.01);
    }
    blk(g, M.wall, pierW, WALL_H, 0.8, len/2 - pierW/2, 0, 0, 0.02);
    // the wainscot stops short of the ends and the cornice runs past them, so
    // neither shares an end face with the wall
    // and stands proud of the wall's room side only: its back stays inside the
    // wall, so the two never share the face seen from outside the hall
    blk(g, M.wainscot, len - 0.04, 1.2, 0.84, 0, 0, 0.08, 0.02);
    blk(g, M.walnutDark, len + 0.06, 0.4 + cap, 1.0, 0, WALL_H - 0.4, 0.05, 0.02);
    return g;
  }
  var back = wall(W.x1 - W.x0, Math.max(4, Math.round((W.x1 - W.x0) / 7.5)), 0.06); back.position.set(0, 0, W.z0 + 0.4); WORLD.add(back);
  // the left wall starts where the back wall ends, so the corner is one wall's,
  // not two walls overlapping (their faces would share planes there)
  var left = wall(W.z1 - W.z0 - 1.0, Math.max(3, Math.round((W.z1 - W.z0) / 7.5)), 0.1); left.rotation.y = Math.PI / 2; left.position.set(W.x0 + 0.4, 0, (W.z0 + W.z1) / 2 + 0.5); WORLD.add(left);
}
function caseMesh(c){
  var g = new THREE.Group(); g.position.set(c.x, 0, c.z); g.rotation.y = c.rot; WORLD.add(g);
  var H = c.levels * LEVEL_H + 0.5, D = 0.62, w = c.width;
  blk(g, M.walnut, 0.14, H, D, -w/2 - 0.07, 0, 0); blk(g, M.walnut, 0.14, H, D, w/2 + 0.07, 0, 0);
  blk(g, M.walnutDark, w + 0.3, 0.12, D + 0.08, 0, H - 0.04, 0, 0.03); blk(g, M.walnutDark, w + 0.4, 0.2, D + 0.12, 0, H + 0.08, 0, 0.04);
  // the back panel stands a hair proud of the sides and the shelves stop short
  // of it, so nothing shares the case's back plane (coplanar faces flicker)
  blk(g, M.walnutDark, w, H, 0.06, 0, 0, -D/2 + 0.01, 0.01);
  for(var l = 0; l <= c.levels; l++) blk(g, M.walnut, w - 0.01, 0.07, D - 0.08, 0, 0.25 + l * LEVEL_H - 0.07, 0.04, 0.01);
  // the books, left to right, bottom shelf up
  var lvl = 0, cx = -w/2 + 0.04, perRow = [];
  c.books.forEach(function(b){
    var bw = c.docs ? 0.16 + Math.min(1, b.chunks / 20) * 0.16 : bookWidth(b.terms);
    if(cx + bw > w/2 - 0.04){ lvl++; cx = -w/2 + 0.04; }
    if(lvl >= c.levels) return;
    var h0 = hash(b.id), h = LEVEL_H * (0.62 + h0 * 0.26) * (c.docs ? 1.08 : 1);
    var local = new THREE.Vector3(cx + bw/2, 0.25 + lvl * LEVEL_H + h/2, 0.02);
    var world = local.clone().applyAxisAngle(new THREE.Vector3(0, 1, 0), c.rot).add(new THREE.Vector3(c.x, 0, c.z));
    var rec = {id:b.id, label:b.label || b.title || b.id, terms:b.terms, doc:!!c.docs, w:bw, h:h, p:world, rot:c.rot,
      color:c.pal[Math.floor(h0 * 997) % c.pal.length], wing:c.wing, shelf:"", gold:!!c.docs};
    BOOKS.push(rec); BYID[b.id] = rec; cx += bw + 0.012;
  });
  // which shelf each book is on, for the tooltip and the open book's header
  var idx = 0;
  c.starts.forEach(function(s, k){
    var upto = k + 1 < c.starts.length ? c.starts[k + 1].at : c.books.length;
    for(var i = s.at; i < upto; i++){ var r = BYID[c.books[i] && c.books[i].id]; if(r) r.shelf = s.name; }
    idx = upto;
  });
  return H;
}
function plaqueMesh(p){
  var g = new THREE.Group(); g.position.set(p.x, 0, p.z); g.rotation.y = p.rot; WORLD.add(g);
  add(g, new THREE.CylinderGeometry(0.06, 0.09, 1.3, 12), M.brass, 0, 0.65, 0);
  add(g, new THREE.CylinderGeometry(0.35, 0.4, 0.08, 20), M.brass, 0, 0.04, 0);
  blk(g, M.walnutDark, 1.5, 0.75, 0.08, 0, 1.25, 0, 0.03);
  var o = label(g, "<b>" + esc(p.title) + "</b><span>" + esc(p.sub) + "</span>", "plaque", 0, 2.55, 0,
    p.wing === -1 ? null : function(){ selectWing(p.wing === -2 ? PLAN.annexIndex : p.wing); });
  o.element.dataset.wing = p.wing === -2 ? PLAN.annexIndex : p.wing;
}
function furniture(P){
  // lectern
  var L = P.lectern;
  add(WORLD, new THREE.CylinderGeometry(0.25, 0.45, 1.3, 16), M.walnut, L.x, 0.65, L.z);
  var top = blk(WORLD, M.walnutDark, 1.6, 0.1, 1.1, L.x, 1.3, L.z, 0.03); top.rotation.x = 0.35;
  label(WORLD, STR.lectern, "shelf lect", L.x, 2.3, L.z);
  // the reading table: recalled books send their threads here
  var D = P.desk;
  blk(WORLD, M.walnut, 5.0, 0.14, 1.8, D.x, 1.0, D.z, 0.05);
  [-2.2, 2.2].forEach(function(dx){ blk(WORLD, M.walnutDark, 0.3, 1.0, 1.4, D.x + dx, 0, D.z, 0.04); });
  [-1.4, 1.4].forEach(function(dx){
    add(WORLD, new THREE.CylinderGeometry(0.06, 0.06, 0.5, 10), M.brass, D.x + dx, 1.37, D.z - 0.3);
    add(WORLD, new THREE.CylinderGeometry(0.12, 0.32, 0.22, 20, 1, true), M.lamp, D.x + dx, 1.68, D.z - 0.3);
    if(NIGHT){ var l = new THREE.PointLight("#ffd08a", 6, 7, 1.6); l.position.set(D.x + dx, 1.55, D.z - 0.3); WORLD.add(l); }
  });
  // card catalogue: click to search
  var C = P.catalog, cg = new THREE.Group(); cg.position.copy(C); WORLD.add(cg);
  blk(cg, M.oak, 4.2, 2.6, 1.3, 0, 0, 0, 0.05);
  for(var r = 0; r < 6; r++) for(var c = 0; c < 8; c++){
    blk(cg, M.walnut, 0.44, 0.32, 0.06, -1.82 + c * 0.52, 0.2 + r * 0.4, 0.66, 0.02);
    add(cg, new THREE.BoxGeometry(0.14, 0.04, 0.04), M.brass, -1.82 + c * 0.52, 0.36 + r * 0.4, 0.7);
  }
  label(cg, "<b>" + STR.catalog + "</b><span>" + LIB.cards.toLocaleString() + " " + STR.cards + "</span>", "plaque", 0, 3.4, 0, function(){ $("q").focus(); });
  // floating candles over the nave
  for(var i = 0; i < 40; i++){
    var x = (hash("cx" + i) - 0.5) * 16, z = P.hall.z0 + 4 + hash("cz" + i) * (P.hall.z1 - P.hall.z0 - 6), y = 8.4 + hash("cy" + i) * 1.6;
    add(WORLD, new THREE.CylinderGeometry(0.07, 0.07, 0.45, 10), M.wax, x, y, z, true);
    add(WORLD, new THREE.SphereGeometry(0.06, 8, 6), M.flame, x, y + 0.3, z, true);
    if(NIGHT && i % 7 === 0){ var pl = new THREE.PointLight("#ffc46b", 9, 12, 1.8); pl.position.set(x, y + 0.3, z); WORLD.add(pl); }
  }
}

function build(keepCamera){
  if(WORLD){ scene.remove(WORLD); WORLD.traverse(function(o){ if(o.element && o.element.parentNode) o.element.parentNode.removeChild(o.element); }); }
  NIGHT = night(); document.documentElement.setAttribute("data-mode", NIGHT ? "dark" : "light");
  $("modebtn").textContent = NIGHT ? "☀" : "☾"; $("modebtn").title = NIGHT ? STR.day : STR.night;
  materials();
  WORLD = new THREE.Group(); scene.add(WORLD);
  BOOKS = []; BYID = {}; fx = null;
  var bg = NIGHT ? "#1b1712" : "#e9dcc0";
  scene.background = new THREE.Color(bg);
  scene.environment = envTex; scene.environmentIntensity = NIGHT ? 0.12 : 0.45;
  scene.fog = NIGHT ? new THREE.Fog(bg, 120, 220) : null;
  renderer.toneMappingExposure = NIGHT ? 1.1 : 1.05;

  var P = PLAN = plan(LIB), W = P.hall;
  var floor = new THREE.MeshStandardMaterial({map:parquet(), roughness:0.55});
  floor.map.repeat.set((W.x1 - W.x0) / 5, (W.z1 - W.z0) / 5);
  add(WORLD, new THREE.BoxGeometry(W.x1 - W.x0, 0.4, W.z1 - W.z0), floor, (W.x0 + W.x1) / 2, -0.2, (W.z0 + W.z1) / 2, true);
  blk(WORLD, M.carpet, 6, 0.04, W.z1 - W.z0 - 3, 0, 0, (W.z0 + W.z1) / 2 + 1.5, 0.01);
  walls(W);
  P.cases.forEach(function(c){
    var H = caseMesh(c);
    c.starts.forEach(function(s, k){
      var o = label(WORLD, esc(s.name) + "<em>" + s.n + "</em>", "shelf", c.x - c.width/2 + 0.5 + (s.at % 14) / 14 * (c.width - 1), H + 0.35 + (k % 2) * 0.75, c.z + 0.3);
      o.userData.wing = c.wing; o.visible = false;
    });
  });
  P.plaques.forEach(plaqueMesh);
  // reference room: documents as thick books along the left wall
  P.docs.forEach(function(docs, k){
    caseMesh({x:W.x0 + 1.2, z:W.z0 + 5 + k * 4.2, rot:Math.PI / 2, levels:6, width:3.8, docs:true,
      books:docs.map(function(d){ return {id:d.id, label:d.title, chunks:d.chunks}; }), starts:[], pal:CREAM, wing:-3});
  });
  if(P.docs.length) plaqueMesh({x:W.x0 + 3.1, z:W.z0 + 5 + P.docs.length * 2.1 + 1.5, rot:Math.PI / 2, title:STR.reference, sub:LIB.docs.length + " " + STR.docs, wing:-1});
  furniture(P);

  // every book in one instanced mesh; a ring of gilt on the reference volumes
  var geo = new RoundedBoxGeometry(1, 1, 1, 1, 0.08);
  MESH = new THREE.InstancedMesh(geo, new THREE.MeshStandardMaterial({roughness:0.62, metalness:0.02}), Math.max(1, BOOKS.length));
  var o = new THREE.Object3D(), gilt = BOOKS.filter(function(b){ return b.gold; });
  var gm = new THREE.InstancedMesh(new THREE.BoxGeometry(1, 1, 1), M.brass, Math.max(1, gilt.length * 2)), gi = 0;
  BOOKS.forEach(function(b, i){
    o.position.copy(b.p); o.rotation.set(0, b.rot, 0); o.scale.set(b.w, b.h, BOOK_D); o.updateMatrix();
    MESH.setMatrixAt(i, o.matrix); MESH.setColorAt(i, new THREE.Color(b.color)); b.i = i;
    if(b.gold) [0.28, -0.3].forEach(function(f){
      var q = new THREE.Vector3(0, b.h * f, BOOK_D / 2 + 0.004).applyAxisAngle(new THREE.Vector3(0, 1, 0), b.rot).add(b.p);
      o.position.copy(q); o.scale.set(b.w * 1.01, 0.025, 0.01); o.updateMatrix(); gm.setMatrixAt(gi++, o.matrix);
    });
  });
  MESH.count = BOOKS.length; gm.count = gi;
  MESH.castShadow = MESH.receiveShadow = true; WORLD.add(MESH); WORLD.add(gm);

  // lights
  WORLD.add(new THREE.HemisphereLight(NIGHT ? "#6a5a44" : "#fff6e6", NIGHT ? "#1c140c" : "#c8b08a", NIGHT ? 0.75 : 1.25));
  var sun = new THREE.DirectionalLight(NIGHT ? "#9fb3d6" : "#fff0d0", NIGHT ? 0.25 : 2.4);
  var span = Math.max(W.x1 - W.x0, W.z1 - W.z0) * 0.75;
  sun.position.set(W.x0 - 4, 40, W.z0 - 14); sun.castShadow = true;
  sun.shadow.camera.left = -span; sun.shadow.camera.right = span; sun.shadow.camera.top = span; sun.shadow.camera.bottom = -span; sun.shadow.camera.far = 220;
  sun.shadow.mapSize.set(4096, 4096); sun.shadow.bias = -0.0004; sun.shadow.normalBias = 0.03;
  WORLD.add(sun);
  var fill = new THREE.DirectionalLight("#ffe7c2", NIGHT ? 0.15 : 0.6); fill.position.set(30, 20, 40); WORLD.add(fill);

  // stats
  var books = LIB.stacks.length, shelves = LIB.annex.length;
  LIB.wings.forEach(function(w){ books += w.books; shelves += w.shelves.length; });
  LIB.annex.forEach(function(s){ books += s.books.length; });
  $("stats").innerHTML = [[books, STR.books], [shelves, STR.shelves], [LIB.cards, STR.cards], [LIB.docs_known ? LIB.docs.length : "—", STR.docs]]
    .map(function(s){ return "<div><b>" + (typeof s[0] === "number" ? s[0].toLocaleString() : s[0]) + "</b><span>" + s[1] + "</span></div>"; }).join("");
  $("src").textContent = LIB.source + (LIB.docs_known ? "" : " · " + STR.docsOff);
  if(!keepCamera) home();
  if(focusWing >= 0) selectWing(focusWing, true);
  if(openCardId) openCard(openCardId, true);
}

// ---- camera
var tween = null;
function fly(pos, target, ms){
  var p0 = camera.position.clone(), t0 = controls.target.clone(), start = performance.now(); ms = ms || 900;
  tween = function(now){ var k = Math.min(1, (now - start) / ms), e = k < 0.5 ? 2 * k * k : 1 - Math.pow(-2 * k + 2, 2) / 2;
    camera.position.lerpVectors(p0, pos, e); controls.target.lerpVectors(t0, target, e); if(k >= 1) tween = null; };
}
function home(instant){
  var W = PLAN.hall, cx = (W.x0 + W.x1) / 2, cz = (W.z0 + W.z1) / 2, r = Math.max(W.x1 - W.x0, W.z1 - W.z0);
  // far enough that the hall's width fits the horizontal field of view, which
  // on a portrait phone is the narrow one
  var aspect = innerWidth / innerHeight, vf = camera.fov * Math.PI / 180, hf = 2 * Math.atan(Math.tan(vf / 2) * aspect);
  var d = Math.max(r * 1.25, ((W.x1 - W.x0) * 0.62) / Math.tan(hf / 2));
  var target = new THREE.Vector3(cx, 0, cz - 1), pos = new THREE.Vector3(cx + d * 0.55, d * 0.62, cz + d * 0.62);
  if(instant !== false && !camera.userData.placed){ camera.position.copy(pos); controls.target.copy(target); camera.userData.placed = true; }
  else fly(pos, target);
  controls.maxDistance = d * 2.2; controls.minDistance = 3;
}

// ---- a wing: fly to it and show its shelves' names
function selectWing(i, quiet){
  focusWing = i;
  WORLD.traverse(function(o){ if(o.isCSS2DObject && o.userData.wing !== undefined) o.visible = o.userData.wing === i || (i === PLAN.annexIndex && o.userData.wing === -2); });
  document.querySelectorAll(".plaque").forEach(function(el){ el.classList.toggle("sel", +el.dataset.wing === i); });
  var w = PLAN.wings[i]; if(!w || quiet) return;
  var target = new THREE.Vector3(w.x, 1.5, w.z), d = Math.max(9, w.w * 1.3);
  fly(new THREE.Vector3(w.x + d * 0.35, d * 0.55, w.z + d * 0.9), target);
}

// ---- effects: glowing books, threads, the relation table. One group, replaced whole.
function clearFx(){ if(fx){ WORLD.remove(fx); fx.traverse(function(o){ if(o.element && o.element.parentNode) o.element.parentNode.removeChild(o.element); }); } fx = new THREE.Group(); WORLD.add(fx); return fx; }
function glowBook(g, b, m){
  var front = new THREE.Vector3(0, 0, 0.2).applyAxisAngle(new THREE.Vector3(0, 1, 0), b.rot), p = b.p.clone().add(front);
  add(g, rbox(1, 1, 1, 0.05), m, p.x, p.y, p.z, true).scale.set(b.w * 2.4, b.h * 1.3, 0.5);
  return p;
}
function thread(g, a, b, m, lift){
  var mid = a.clone().lerp(b, 0.5); mid.y += lift;
  add(g, new THREE.TubeGeometry(new THREE.QuadraticBezierCurve3(a, mid, b), 48, 0.03, 5, false), m, 0, 0, 0, true);
  return mid;
}

// ---- reading in place: a call that recalls books lights them and threads them to the table
var readTimer = null;
function readInPlace(ev){
  var terms = (ev.terms || []).map(function(t){ return String(t).toLowerCase(); }).filter(function(t){ return t.length >= 3; });
  if(!terms.length || openBookState || openCardId) return;
  var hit = BOOKS.filter(function(b){ var s = (b.label + " " + b.id).toLowerCase(); return terms.some(function(t){ return s.indexOf(t) >= 0; }); }).slice(0, 12);
  if(!hit.length) return;
  var g = clearFx();
  hit.forEach(function(b, i){ var p = glowBook(g, b, M.glow); thread(g, p, PLAN.desk, M.thread, 5 + (i % 4) * 0.6); });
  label(g, esc(STR.reading + " · " + (ev.text || ev.tool)), "read", PLAN.desk.x, PLAN.desk.y + 2.2, PLAN.desk.z);
  clearTimeout(readTimer); readTimer = setTimeout(function(){ if(!openCardId && !openBookState) clearFx(); }, 9000);
}

// ---- an index card: every book that mentions it, and its relations on the table
function openCard(id, quiet){
  openCardId = id;
  return getJSON("api/library/card?id=" + encodeURIComponent(id)).then(function(card){
    if(openCardId !== id) return;
    var g = clearFx();
    var C = PLAN.catalog.clone().add(new THREE.Vector3(0, 3.0, 0.7));
    var where = {};
    card.books.forEach(function(bid, i){
      var b = BYID[bid]; if(!b) return;
      var p = glowBook(g, b, M.find); thread(g, C, p, M.ink, 4 + (i % 5) * 0.5);
      var k = b.wing === -1 ? STR.stacks : (b.wing === -2 || b.wing === PLAN.annexIndex ? STR.annex : (LIB.wings[b.wing] ? LIB.wings[b.wing].name : "?"));
      where[k] = (where[k] || 0) + 1;
    });
    // the table: the card in the middle, what it relates to around it
    var T = PLAN.table, R = Math.min(card.relations.length, 12);
    add(g, new THREE.CylinderGeometry(6.2, 6.2, 0.16, 64), M.walnut, T.x, 1.0, T.z);
    add(g, new THREE.CylinderGeometry(0.6, 1.0, 1.0, 24), M.walnutDark, T.x, 0.5, T.z);
    add(g, new THREE.CylinderGeometry(5.9, 5.9, 0.02, 64), M.parchment, T.x, 1.09, T.z, true);
    label(g, esc(card.label) + "<small>" + esc(card.type) + "</small>", "tag center", T.x, 1.5, T.z);
    for(var i = 0; i < R; i++){
      var rel = card.relations[i], a = (i / R) * Math.PI * 2 + 0.3;
      var q = new THREE.Vector3(T.x + Math.cos(a) * 4.9, 1.1, T.z + Math.sin(a) * 4.1);
      blk(g, M.parchment, 1.15, 0.05, 0.75, q.x, 1.1, q.z, 0.02);
      add(g, new THREE.TubeGeometry(new THREE.LineCurve3(new THREE.Vector3(T.x, 1.16, T.z), q.clone().setY(1.16)), 1, 0.025, 4, false), M.line, 0, 0, 0, true);
      (function(rel){ label(g, "<small>" + esc(rel.out ? rel.rel : "← " + rel.rel) + "</small>" + esc(rel.label), "tag", q.x, 1.45, q.z,
        function(){ if(BYID[rel.id]) openBook(rel.id); else openCard(rel.id); }); })(rel);
    }
    label(g, STR.relTable, "shelf", T.x, 2.2, T.z - 5.4);
    // the side panel says the same in words, and lists what the table cannot hold
    var html = "<h2>" + esc(card.label) + "</h2><div class='sub'>" + esc(card.type) + " · " + esc(STR.cardBooks(card.books.length)) + "</div>";
    if(!card.found) html += "<p>" + STR.notFound + "</p>";
    var ws = Object.keys(where).sort(function(a, b){ return where[b] - where[a]; });
    html += "<div class='sec'>" + STR.whereBooks + "</div><div>" + (ws.length ? ws.map(function(k){ return esc(k) + " ×" + where[k]; }).join(" · ") : STR.none) + "</div>";
    html += "<div class='sec'>" + STR.rels + " · " + (card.relations.length + (card.more || 0)) + "</div>";
    html += card.relations.map(function(r, i){ return "<div class='rel'><code>" + esc(r.out ? r.rel : "← " + r.rel) + "</code><a data-k='" + i + "'>" + esc(r.label) + "</a></div>"; }).join("");
    if(card.more) html += "<div class='sub'>" + STR.more(card.more) + "</div>";
    side(html, function(el){ el.querySelectorAll("a[data-k]").forEach(function(a){ a.onclick = function(){ var r = card.relations[+a.dataset.k]; if(BYID[r.id]) openBook(r.id); else openCard(r.id); }; }); });
    if(!quiet){ var T2 = PLAN.table; fly(new THREE.Vector3(T2.x + 9, 30, T2.z + 24), new THREE.Vector3(T2.x + 6, 0, T2.z - 9)); }
  }).catch(function(){ note(STR.notFound); });
}

// ---- a book: off its shelf, open on the lectern, read page by page
var INK = "#2a1d12", MUTE = "#7a634a", GREEN = "#1f6b52", BRASS = "#9c6f22";
var SERIF = getComputedStyle(document.documentElement).getPropertyValue("--serif");
function wrap(g, text, maxW){
  var out = [];
  String(text || "").split("\n").forEach(function(para){
    var line = "", toks = para.match(/[⺀-鿿＀-￯　-〿]|[^\s⺀-鿿＀-￯　-〿]+|\s+/g) || [""];
    toks.forEach(function(t){ if(g.measureText(line + t).width > maxW && line.trim()){ out.push(line); line = /^\s+$/.test(t) ? "" : t; } else line += t; });
    out.push(line);
  });
  return out;
}
function pageCanvas(draw){
  var W = 1200, H = 1640, c = document.createElement("canvas"); c.width = W; c.height = H;
  var g = c.getContext("2d"), grad = g.createRadialGradient(W/2, H/2, 200, W/2, H/2, 1100);
  grad.addColorStop(0, "#fbf2dc"); grad.addColorStop(1, "#ead9b4"); g.fillStyle = grad; g.fillRect(0, 0, W, H);
  var hits = []; draw(g, W, H, hits);
  var t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace; t.anisotropy = 16;
  return {tex:t, hits:hits, W:W, H:H};
}
function bodyPages(book){
  var c = document.createElement("canvas").getContext("2d"); c.font = "36px " + SERIF;
  var text = book.record && book.record.content ? book.record.content : (book.record && !book.record.available ? STR.noRecords : book.label);
  var lines = wrap(c, text, 990), first = 21, rest = 26, pages = [lines.slice(0, first)];
  for(var i = first; i < lines.length; i += rest) pages.push(lines.slice(i, i + rest));
  return pages;
}
function leftPage(book, pages, n){
  var b = BYID[book.id] || {};
  return pageCanvas(function(g, W, H){
    var x = 110, y = 130;
    g.fillStyle = BRASS; g.font = "600 30px " + SERIF;
    g.fillText(((b.wing === -1 ? STR.stacks : (b.shelf || "")) + " · " + STR.shelf).trim(), x, y);
    if(n === 0){
      g.fillStyle = INK; g.font = "700 50px " + SERIF; y = 210;
      wrap(g, short(book.id), W - 220).slice(0, 2).forEach(function(l){ g.fillText(l, x, y); y += 64; });
      var r = book.record || {}, meta = [r.at ? STR.written + " " + r.at.slice(0, 10) : "", r.grade ? STR.grade + " " + r.grade : "", r.type || ""].filter(Boolean).join(" · ");
      g.fillStyle = MUTE; g.font = "30px " + SERIF; g.fillText(meta, x, y); y += 30;
      g.strokeStyle = BRASS; g.lineWidth = 2; g.beginPath(); g.moveTo(x, y + 14); g.lineTo(W - x, y + 14); g.stroke(); y += 76;
    } else y = 210;
    g.fillStyle = INK; g.font = "36px " + SERIF;
    (pages[n] || []).forEach(function(l){ g.fillText(l, x, y); y += 56; });
    g.fillStyle = MUTE; g.font = "italic 30px " + SERIF; g.textAlign = "center"; g.fillText("— " + (n + 1) + " / " + pages.length + " —", W / 2, H - 80);
  });
}
function rightPage(book){
  return pageCanvas(function(g, W, H, hits){
    var x = 100, y = 130, maxW = W - 190;
    g.fillStyle = BRASS; g.font = "600 30px " + SERIF; g.fillText(STR.index, x, y); y = 200;
    book.index.slice(0, 24).forEach(function(t, i){
      var cx = x + (i % 2) * (maxW / 2), cy = y + Math.floor(i / 2) * 56;
      g.fillStyle = t.project ? GREEN : INK; g.font = (t.project ? "700 " : "") + "34px " + SERIF;
      var name = t.label.length > 22 ? t.label.slice(0, 21) + "…" : t.label;
      g.fillText(name, cx + 30, cy);
      g.fillStyle = t.project ? GREEN : BRASS; g.beginPath(); g.arc(cx + 10, cy - 11, t.project ? 8 : 5, 0, 7); g.fill();
      hits.push({x:cx, y:cy - 40, w:maxW / 2, h:52, go:function(){ openCard(t.id); }});
    });
    y += Math.ceil(Math.min(24, book.index.length) / 2) * 56 + 40;
    g.strokeStyle = BRASS; g.lineWidth = 2; g.beginPath(); g.moveTo(x, y - 30); g.lineTo(W - x, y - 30); g.stroke();
    g.fillStyle = BRASS; g.font = "600 30px " + SERIF; g.fillText(STR.seeAlso, x, y + 24); y += 92;
    book.see_also.forEach(function(s){
      var t = short(s.id); if(t.length > 44) t = t.slice(0, 43) + "…";
      g.fillStyle = GREEN; g.font = "34px " + SERIF; g.fillText(t, x, y); g.fillRect(x, y + 8, g.measureText(t).width, 2.5);
      var o = BYID[s.id] || {}; g.fillStyle = MUTE; g.font = "26px " + SERIF;
      g.fillText(((o.wing === -1 ? STR.stacks : (o.shelf || "")) + " · " + STR.shared(s.shared)).replace(/^ · /, ""), x, y + 44);
      hits.push({x:x, y:y - 40, w:maxW, h:96, go:function(){ openBook(s.id); }});
      y += 102;
    });
    var r = book.record || {}, prov = [r.source ? STR.source + ": " + r.source : "", r.producer ? STR.producer + ": " + r.producer : "", r.at ? STR.written + ": " + r.at : ""].filter(Boolean);
    if(prov.length){ y += 10; g.strokeStyle = BRASS; g.beginPath(); g.moveTo(x, y - 30); g.lineTo(W - x, y - 30); g.stroke();
      g.fillStyle = BRASS; g.font = "600 30px " + SERIF; g.fillText(STR.provenance, x, y + 24); y += 80;
      g.fillStyle = INK; g.font = "30px " + SERIF; prov.forEach(function(l){ g.fillText(l.length > 52 ? l.slice(0, 51) + "…" : l, x, y); y += 46; }); }
  });
}
function bentPage(tex, side, PW, PH){
  var geo = new THREE.PlaneGeometry(PW, PH, 40, 1), pos = geo.attributes.position;
  for(var i = 0; i < pos.count; i++){ var u = (pos.getX(i) + PW/2) / PW, t = side < 0 ? 1 - u : u;
    pos.setZ(i, 0.11 * Math.sin(Math.min(1, t * 1.6) * Math.PI / 2) - 0.035 * t * t); }
  geo.computeVertexNormals();
  var m = new THREE.Mesh(geo, new THREE.MeshStandardMaterial({map:tex, roughness:0.92}));
  m.rotation.x = -Math.PI / 2; m.position.set(side * (PW/2 + 0.008), 0.03, 0); m.receiveShadow = true;
  return m;
}
function openBook(id){
  var b = BYID[id];
  if(!b){ note(STR.notFound); return; }
  getJSON("api/library/book?id=" + encodeURIComponent(id)).then(function(book){
    closeBook(true);
    openCardId = "";
    var g = clearFx(); glowBook(g, b, M.glow);
    var pages = bodyPages(book), L = PLAN.lectern, PW = 1.15, PH = 1.57;
    var grp = new THREE.Group(); grp.position.set(L.x, L.y + 0.12, L.z); grp.rotation.x = 0.35; g.add(grp);
    blk(grp, mat(b.color, {roughness:0.55}), PW * 2 + 0.12, 0.05, PH + 0.1, 0, -0.085, 0, 0.02);
    blk(grp, mat("#efe2c1"), PW - 0.02, 0.07, PH - 0.02, -PW/2, -0.045, 0, 0.01);
    blk(grp, mat("#efe2c1"), PW - 0.02, 0.07, PH - 0.02, PW/2, -0.045, 0, 0.01);
    var right = rightPage(book), rightMesh = bentPage(right.tex, 1, PW, PH); grp.add(rightMesh);
    var st = openBookState = {id:id, book:book, pages:pages, n:0, grp:grp, left:null, right:right, rightMesh:rightMesh, PW:PW, PH:PH};
    showLeft(st);
    // the thread from the shelf to the lectern says where it came from
    thread(g, b.p.clone(), L.clone(), M.thread, 6);
    fly(new THREE.Vector3(L.x + 0.6, L.y + 3.1, L.z + 2.9), new THREE.Vector3(L.x + 0.1, L.y - 0.1, L.z - 0.3), 1100);
    $("pager").classList.add("open"); document.body.classList.add("reading");
    var b2 = BYID[id], where = b2.wing === -1 ? STR.stacks : (b2.shelf || "");
    side("<h2>" + esc(short(id)) + "</h2><div class='sub'>" + esc(where) + "</div><p style='font-size:13px;line-height:1.6;color:var(--ink2)'>" + esc(b2.label) + "</p>" +
      "<div class='sec'>" + STR.index + "</div><div class='chips'>" + book.index.map(function(t, i){ return "<a class='" + (t.project ? "p" : "") + "' data-t='" + i + "'>" + esc(t.label) + "</a>"; }).join("") + "</div>" +
      "<div class='sec'>" + STR.seeAlso + "</div>" + book.see_also.map(function(s, i){ return "<div class='rel'><a data-s='" + i + "'>" + esc(short(s.id)) + "</a><code>" + esc(STR.shared(s.shared)) + "</code></div>"; }).join(""),
      function(el){
        el.querySelectorAll("a[data-t]").forEach(function(a){ a.onclick = function(){ openCard(book.index[+a.dataset.t].id); }; });
        el.querySelectorAll("a[data-s]").forEach(function(a){ a.onclick = function(){ openBook(book.see_also[+a.dataset.s].id); }; });
      });
  }).catch(function(){ note(STR.notFound); });
}
function showLeft(st){
  if(st.leftMesh){ st.grp.remove(st.leftMesh); st.left.tex.dispose(); }
  st.left = leftPage(st.book, st.pages, st.n); st.leftMesh = bentPage(st.left.tex, -1, st.PW, st.PH); st.grp.add(st.leftMesh);
  $("pageno").textContent = STR.page(st.n + 1, st.pages.length);
  $("prev").disabled = st.n === 0; $("next").disabled = st.n >= st.pages.length - 1;
}
function closeBook(silent){
  if(!openBookState) return;
  openBookState = null; $("pager").classList.remove("open"); document.body.classList.remove("reading");
  if(!silent){ clearFx(); closeSide(); home(false); }
}
$("prev").onclick = function(){ var st = openBookState; if(st && st.n > 0){ st.n--; showLeft(st); } };
$("next").onclick = function(){ var st = openBookState; if(st && st.n < st.pages.length - 1){ st.n++; showLeft(st); } };
$("close").onclick = function(){ closeBook(); };

// ---- side panel
function side(html, wire){ var s = $("side"); $("sidebody").innerHTML = html; s.classList.add("open"); s.scrollTop = 0; if(wire) wire($("sidebody")); }
function closeSide(){ $("side").classList.remove("open"); }
$("sidex").onclick = function(){ if(openBookState) closeBook(); else { openCardId = ""; clearFx(); closeSide(); } };

// ---- picking: hover a book for its title, click to open it; click a page link to follow it
var ray = new THREE.Raycaster(), ndc = new THREE.Vector2(), down = null;
function pick(e){
  var r = renderer.domElement.getBoundingClientRect();
  ndc.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
  ray.setFromCamera(ndc, camera);
}
renderer.domElement.addEventListener("pointerdown", function(e){ down = {x:e.clientX, y:e.clientY}; });
renderer.domElement.addEventListener("pointermove", function(e){
  if(!MESH || e.pointerType === "touch") return;
  pick(e); var h = ray.intersectObject(MESH)[0], tip = $("tip");
  if(h && h.instanceId != null && !openBookState){
    var b = BOOKS[h.instanceId];
    tip.innerHTML = "<b>" + esc(short(b.id)) + "</b>" + esc((b.wing === -1 ? STR.stacks : b.shelf) || "") + (b.label && !b.doc ? "<br>" + esc(b.label.slice(0, 140)) : "");
    tip.style.display = "block"; tip.style.left = Math.min(innerWidth - 330, e.clientX + 14) + "px"; tip.style.top = (e.clientY + 14) + "px";
    renderer.domElement.style.cursor = "pointer";
  } else { tip.style.display = "none"; renderer.domElement.style.cursor = ""; }
});
renderer.domElement.addEventListener("pointerup", function(e){
  if(!down || Math.hypot(e.clientX - down.x, e.clientY - down.y) > 6) return;
  pick(e);
  var st = openBookState;
  if(st){
    var hp = ray.intersectObject(st.rightMesh)[0];
    if(hp && hp.uv){ var px = hp.uv.x * st.right.W, py = (1 - hp.uv.y) * st.right.H;
      for(var i = 0; i < st.right.hits.length; i++){ var z = st.right.hits[i]; if(px >= z.x && px <= z.x + z.w && py >= z.y && py <= z.y + z.h){ z.go(); return; } } }
    return;
  }
  var h = ray.intersectObject(MESH)[0];
  if(h && h.instanceId != null){ var b = BOOKS[h.instanceId]; if(b.doc){ note(b.label); return; } openBook(b.id); }
});

// ---- finding: books by their title as soon as you type, index cards through the source
var findTimer = null, findSeq = 0;
$("q").addEventListener("input", function(){
  clearTimeout(findTimer); var q = this.value.trim();
  if(!q){ $("results").innerHTML = ""; if(!openCardId && !openBookState) clearFx(); return; }
  findTimer = setTimeout(function(){ find(q); }, 220);
});
$("q").addEventListener("keydown", function(e){ if(e.key === "Escape"){ this.value = ""; $("results").innerHTML = ""; clearFx(); this.blur(); } });
function find(q){
  var seq = ++findSeq, lq = q.toLowerCase();
  var books = BOOKS.filter(function(b){ return !b.doc && (b.id.toLowerCase().indexOf(lq) >= 0 || (b.label || "").toLowerCase().indexOf(lq) >= 0); }).slice(0, 8);
  var g = clearFx(); openCardId = "";
  books.forEach(function(b){ glowBook(g, b, M.find); });
  function render(cards, off){
    if(seq !== findSeq) return;
    var html = "";
    if(books.length) html += "<div class='h'>" + STR.hBooks + "</div>" + books.map(function(b, i){ return "<a data-b='" + i + "'>" + esc(short(b.id)) + "<small>" + esc((b.wing === -1 ? STR.stacks : b.shelf) || "") + " · " + esc((b.label || "").slice(0, 80)) + "</small></a>"; }).join("");
    if(cards.length) html += "<div class='h'>" + STR.hCards + "</div>" + cards.map(function(c, i){ return "<a data-c='" + i + "'>" + esc(c.label) + "<small>" + esc(c.type) + "</small></a>"; }).join("");
    if(off) html += "<div class='h'>" + STR.findOff + "</div>";
    $("results").innerHTML = html;
    $("results").querySelectorAll("a[data-b]").forEach(function(a){ a.onclick = function(){ openBook(books[+a.dataset.b].id); }; });
    $("results").querySelectorAll("a[data-c]").forEach(function(a){ a.onclick = function(){ openCard(cards[+a.dataset.c].id); }; });
  }
  render([], false);
  getJSON("api/find?q=" + encodeURIComponent(q)).then(function(r){
    render((r.results || []).filter(function(n){ return n.type !== "memory"; }).slice(0, 8), false);
  }).catch(function(){ render([], true); });
}

// ---- live: the stream the graph page listens to
var feed = [], refetch = null;
function feedLine(ev){
  feed.unshift(ev); feed = feed.slice(0, 4);
  $("feedlist").innerHTML = feed.map(function(f){ var t = new Date(f.at || Date.now());
    return "<div class='l'>" + esc(f.text || f.tool) + "<i>" + t.toLocaleTimeString([], {hour:"2-digit", minute:"2-digit"}) + "</i></div>"; }).join("");
}
function stream(){
  var es = new EventSource("api/stream");
  es.addEventListener("open", function(){ $("led").classList.add("live"); });
  es.addEventListener("error", function(){ $("led").classList.remove("live"); });
  es.addEventListener("activity", function(m){ var ev = JSON.parse(m.data); feedLine(ev); readInPlace(ev); });
  es.addEventListener("delta", function(){ clearTimeout(refetch); refetch = setTimeout(load, 1500); });
  es.addEventListener("snapshot", function(m){ var p = JSON.parse(m.data); (p.events || []).slice(-4).forEach(feedLine); });
}
$("feedlist").innerHTML = "<div class='l'>" + STR.quiet + "</div>";

// ---- mode switch
$("modebtn").onclick = function(){
  modeSetting = night() ? "light" : "dark";
  try { localStorage.setItem(MODE_KEY, modeSetting); } catch(e){}
  build(true);
};
if(SYS_DARK && SYS_DARK.addEventListener) SYS_DARK.addEventListener("change", function(){ if(modeSetting === "auto") build(true); });

function load(){
  return getJSON("api/library").then(function(lib){
    if(LIB && lib.version === LIB.version) return;
    var first = !LIB; LIB = lib; build(!first);
    window.__libraryReady = true;
  }).catch(function(e){ note(String(e)); });
}
addEventListener("resize", function(){ camera.aspect = innerWidth / innerHeight; camera.updateProjectionMatrix(); renderer.setSize(innerWidth, innerHeight); labels.setSize(innerWidth, innerHeight); });
renderer.setAnimationLoop(function(now){ if(tween) tween(now); controls.update(); renderer.render(scene, camera); labels.render(scene, camera); });
load().then(function(){
  stream();
  if(Q.get("book")) openBook(Q.get("book"));
  else if(Q.get("card")) openCard(Q.get("card"));
});
</script>
</body>
</html>
`
