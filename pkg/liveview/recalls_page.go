package liveview

// recallsHTML is the recalls page (see recalls.go). A plain document rather
// than a scene: a timeline is read top to bottom, and the graph page next door
// is where a recall is seen in space (?replay=). Light by default; the
// day/night switch is the other pages' own setting (same storage key, same
// ?mode=), so changing pages does not change the lights. Every request is
// relative, so the page works wherever the view is mounted.
const recallsHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>CortexDB — recalls</title>
<style>
  :root{--bg:#f7f7f4;--card:#ffffff;--ink:#16181d;--ink2:#3d4350;--mute:#6b7280;--line:#e4e4de;--soft:#f0f0eb;
    --accent:#1f5fbf;--good:#13795b;--bad:#b42318;--warn:#a15c07;--chip:#eef2f8;
    --sans:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif;
    --mono:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
  [data-mode="dark"]{--bg:#111318;--card:#181b22;--ink:#e8eaee;--ink2:#c3c8d2;--mute:#8b93a3;--line:#2a2f3a;--soft:#1f232c;
    --accent:#7aa7ff;--good:#55c79b;--bad:#ff8a80;--warn:#f0b35a;--chip:#222838}
  *{box-sizing:border-box}
  html,body{margin:0;background:var(--bg);color:var(--ink);font-family:var(--sans);font-size:14px;line-height:1.45;-webkit-text-size-adjust:100%}
  a{color:var(--accent);text-decoration:none} a:hover{text-decoration:underline}
  header{position:sticky;top:0;z-index:5;background:var(--bg);border-bottom:1px solid var(--line);
    padding:12px max(16px,env(safe-area-inset-left)) 10px}
  .top{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
  h1{margin:0;font-size:18px;font-weight:650;letter-spacing:-.01em}
  .src{color:var(--mute);font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:40vw}
  nav{margin-left:auto;display:flex;gap:6px;align-items:center;flex-wrap:wrap}
  .pill{font:inherit;font-size:12.5px;color:var(--ink2);border:1px solid var(--line);background:var(--card);border-radius:999px;padding:3px 11px;cursor:pointer}
  .pill.on{background:var(--ink);color:var(--bg);border-color:var(--ink)}
  .bar{display:flex;gap:8px;margin-top:10px;flex-wrap:wrap;align-items:center}
  .bar input[type=search]{flex:1 1 220px;min-width:0;font:inherit;padding:7px 10px;border:1px solid var(--line);border-radius:8px;background:var(--card);color:var(--ink)}
  .bar label{font-size:12.5px;color:var(--ink2);display:flex;gap:5px;align-items:center}
  .bar .filter{font-size:12.5px;background:var(--chip);border-radius:999px;padding:3px 10px}
  main{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1.15fr);gap:16px;padding:16px max(16px,env(safe-area-inset-left)) 40px}
  @media (max-width:900px){main{grid-template-columns:minmax(0,1fr)}}
  .card{background:var(--card);border:1px solid var(--line);border-radius:12px}
  .say{padding:28px 20px;color:var(--ink2);text-align:center}
  .say b{display:block;color:var(--ink);margin-bottom:4px}
  .say code{font-family:var(--mono);font-size:12.5px;background:var(--soft);padding:1px 5px;border-radius:4px}
  #list .r{padding:11px 14px;border-bottom:1px solid var(--line);cursor:pointer}
  #list .r:last-child{border-bottom:0}
  #list .r:hover{background:var(--soft)} #list .r.sel{background:var(--chip)}
  .q{font-weight:560;color:var(--ink);overflow-wrap:anywhere}
  .meta{color:var(--mute);font-size:12px;margin-top:3px;display:flex;flex-wrap:wrap;gap:4px 10px}
  .badge{display:inline-block;font-size:11px;border-radius:5px;padding:0 6px;line-height:18px;background:var(--chip);color:var(--ink2)}
  .badge.bad{background:color-mix(in srgb,var(--bad) 14%,transparent);color:var(--bad)}
  .badge.warn{background:color-mix(in srgb,var(--warn) 16%,transparent);color:var(--warn)}
  .badge.good{background:color-mix(in srgb,var(--good) 14%,transparent);color:var(--good)}
  .more{display:block;width:100%;padding:10px;border:0;border-top:1px solid var(--line);background:transparent;color:var(--accent);font:inherit;cursor:pointer}
  #detail{position:sticky;top:120px;max-height:calc(100vh - 136px);overflow:auto}
  @media (max-width:900px){#detail{position:static;max-height:none}}
  #detail section{padding:14px 16px;border-bottom:1px solid var(--line)}
  #detail section:last-child{border-bottom:0}
  h2{margin:0 0 8px;font-size:12px;font-weight:600;letter-spacing:.06em;text-transform:uppercase;color:var(--mute)}
  .hit{display:grid;grid-template-columns:24px minmax(0,1fr) auto;gap:4px 10px;padding:8px 0;border-top:1px solid var(--line)}
  .hit:first-of-type{border-top:0}
  .hit .n{color:var(--mute);font-variant-numeric:tabular-nums;padding-top:1px}
  .hit .t{overflow-wrap:anywhere}
  .hit .fb{display:flex;gap:4px;align-items:flex-start}
  .fb button{font:inherit;font-size:12px;border:1px solid var(--line);background:var(--card);color:var(--ink2);border-radius:6px;padding:1px 7px;cursor:pointer}
  .fb button.on.useful{background:var(--good);color:#fff;border-color:var(--good)}
  .fb button.on.noise{background:var(--bad);color:#fff;border-color:var(--bad)}
  .kv{display:grid;grid-template-columns:max-content minmax(0,1fr);gap:3px 12px;font-size:13px}
  .kv dt{color:var(--mute)} .kv dd{margin:0;overflow-wrap:anywhere}
  table{width:100%;border-collapse:collapse;font-size:12.5px}
  th,td{text-align:left;padding:4px 6px;border-bottom:1px solid var(--line);vertical-align:top}
  th{color:var(--mute);font-weight:500}
  td.num{text-align:right;font-variant-numeric:tabular-nums;font-family:var(--mono);font-size:12px}
  td.id{font-family:var(--mono);font-size:11.5px;overflow-wrap:anywhere}
  tr.final td{background:color-mix(in srgb,var(--good) 8%,transparent)}
  .side{margin-top:10px} .side h3{margin:0 0 4px;font-size:13px}
  .reason{color:var(--ink2);font-size:12.5px;margin-bottom:6px}
  .tiles{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;padding:14px 16px}
  @media (max-width:560px){.tiles{grid-template-columns:repeat(2,minmax(0,1fr))}}
  .tile b{display:block;font-size:22px;font-weight:650;font-variant-numeric:tabular-nums}
  .tile span{font-size:12px;color:var(--mute)}
  .err{color:var(--bad);padding:14px 16px}
  .muted{color:var(--mute)}
</style>
</head>
<body>
<header>
  <div class="top">
    <h1 id="title">Recalls</h1>
    <span class="src" id="src"></span>
    <nav>
      <button class="pill on" id="tabTimeline">Timeline</button>
      <button class="pill" id="tabRecords">Records</button>
      <a class="pill" id="tograph" href="./">graph</a>
      <a class="pill" id="golden" href="api/recalls/golden" title="Feedback as recall-eval golden cases (JSONL)">golden.jsonl</a>
      <button class="pill" id="modebtn" title="Day / night"></button>
    </nav>
  </div>
  <div class="bar" id="filters">
    <input type="search" id="contains" placeholder="Filter by query text">
    <label><input type="checkbox" id="emptyOnly"> <span id="emptyLabel">returned nothing</span></label>
    <span class="filter" id="recordFilter" hidden></span>
  </div>
</header>
<main>
  <div class="card" id="list"></div>
  <div class="card" id="detail"><div class="say" id="pick"></div></div>
</main>
<script>
"use strict";
var Q = new URLSearchParams(location.search);
var LANG = (Q.get("lang") || (navigator.language || "en")).toLowerCase().indexOf("zh") === 0 ? "zh" : "en";
var S = {
  en:{title:"Recalls",timeline:"Timeline",records:"Records",filter:"Filter by query text",empty:"returned nothing",pick:"Pick a recall to see why it returned what it did.",
    off:"The recall log is off on this brain.",offHow:"Start the server with <code>CORTEXDB_RECALL_LOG=1</code>. It stores queries, kept for <code>CORTEXDB_RECALL_LOG_DAYS</code> (30).",
    none:"No recalls yet.",more:"Older recalls",hits:"hits",ms:"ms",replay:"replay on graph",plan:"Plan",hitsH:"Hits",cands:"Candidates",dropped:"Dropped by floors and filters",
    degraded:"degraded",query:"query",keywords:"keywords",entities:"entities",mode:"mode",planner:"planner",asked:"asked by",useful:"useful",noise:"noise",
    totals:"Last %d days",recalls:"recalls",emptyN:"returned nothing",degN:"degraded",most:"Most recalled",reached:"records reached",never:"Never recalled",count:"count",last:"last",queries:"queries",
    noneNever:"Every memory has been recalled at least once.",confined:"Not shown for a key with a scope.",byRecord:"recalls of",clear:"clear",graph:"graph",retriever:"retriever",rank:"rank",score:"score",by:"by",threshold:"threshold",
    noExplain:"No explain record was kept for this recall.",unverified:"unverified",verified:"verified"},
  zh:{title:"召回记录",timeline:"时间线",records:"记录",filter:"按查询文本筛选",empty:"无结果",pick:"选一条召回，看它为什么返回这些结果。",
    off:"这个大脑没有开启召回日志。",offHow:"用 <code>CORTEXDB_RECALL_LOG=1</code> 启动服务。日志会保存查询文本，保留 <code>CORTEXDB_RECALL_LOG_DAYS</code> 天（默认 30）。",
    none:"还没有召回。",more:"更早的召回",hits:"条命中",ms:"毫秒",replay:"在图上重放",plan:"检索计划",hitsH:"命中",cands:"候选",dropped:"被阈值和过滤掉的",
    degraded:"降级",query:"查询",keywords:"关键词",entities:"实体",mode:"模式",planner:"规划",asked:"提问方",useful:"有用",noise:"噪音",
    totals:"最近 %d 天",recalls:"次召回",emptyN:"无结果",degN:"降级",most:"最常被召回",reached:"被召回过的记录",never:"从未被召回",count:"次数",last:"最近",queries:"查询",
    noneNever:"每条记忆都至少被召回过一次。",confined:"限定范围的 key 不显示这一项。",byRecord:"召回了",clear:"清除",graph:"图谱",retriever:"检索器",rank:"名次",score:"分数",by:"原因",threshold:"阈值",
    noExplain:"这次召回没有保留解释记录。",unverified:"未验证",verified:"已验证"}
}[LANG];
document.documentElement.lang = LANG === "zh" ? "zh-CN" : "en";

// Day and night: the other pages' own setting. Light unless somebody chose
// otherwise.
var MODE_KEY = "cortexdb.liveview.mode", MODES = ["light", "dark", "auto"];
function stored(k){ try { return localStorage.getItem(k) || ""; } catch(e){ return ""; } }
var modeSetting = MODES.indexOf(Q.get("mode")) >= 0 ? Q.get("mode") : (MODES.indexOf(stored(MODE_KEY)) >= 0 ? stored(MODE_KEY) : "light");
var SYS_DARK = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;
function applyMode(){
  var dark = modeSetting === "dark" || (modeSetting === "auto" && !!(SYS_DARK && SYS_DARK.matches));
  document.documentElement.setAttribute("data-mode", dark ? "dark" : "light");
  document.getElementById("modebtn").textContent = {light:"☀", dark:"☾", auto:"◐"}[modeSetting];
}
applyMode();
document.getElementById("modebtn").addEventListener("click", function(){
  modeSetting = MODES[(MODES.indexOf(modeSetting) + 1) % MODES.length];
  try { localStorage.setItem(MODE_KEY, modeSetting); } catch(e){}
  applyMode();
});
document.getElementById("tograph").href = "./" + location.search.replace(/([?&])(record_id|recall)=[^&]*/g, "$1");

document.getElementById("title").textContent = S.title;
document.getElementById("tabTimeline").textContent = S.timeline;
document.getElementById("tabRecords").textContent = S.records;
document.getElementById("contains").placeholder = S.filter;
document.getElementById("emptyLabel").textContent = S.empty;
document.getElementById("pick").textContent = S.pick;
document.getElementById("tograph").textContent = S.graph;

function esc(s){ return String(s == null ? "" : s).replace(/[&<>"']/g, function(c){
  return {"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c]; }); }
function num(x, d){ return (typeof x === "number" && isFinite(x)) ? x.toFixed(d == null ? 3 : d) : ""; }
function ago(iso){
  var t = Date.parse(iso); if(!t) return "";
  var s = Math.max(0, (Date.now() - t) / 1000);
  if(s < 60) return Math.round(s) + "s"; if(s < 3600) return Math.round(s/60) + "m";
  if(s < 86400) return Math.round(s/3600) + "h"; return Math.round(s/86400) + "d";
}
function getJSON(url){
  return fetch(url, {headers:{Accept:"application/json"}}).then(function(r){
    return r.json().catch(function(){ return {}; }).then(function(b){
      if(!r.ok) throw new Error(b.error || r.statusText); return b;
    });
  });
}
function isOff(err){ return /recall log is off/i.test(String(err && err.message || err)); }
function offHTML(){ return "<div class='say'><b>" + esc(S.off) + "</b>" + S.offHow + "</div>"; }

var state = {view: Q.get("view") === "records" ? "records" : "timeline", contains: "", empty: false,
  record: Q.get("record_id") || "", recalls: [], next: 0, selected: Q.get("recall") || ""};

function query(before){
  var p = new URLSearchParams();
  if(state.contains) p.set("contains", state.contains);
  if(state.empty) p.set("empty", "1");
  if(state.record) p.set("record_id", state.record);
  if(before) p.set("before", before);
  return "api/recalls?" + p.toString();
}

function recallRow(r){
  var who = [r.key, r.host, r.project, r.agent].filter(Boolean).map(esc).join(" · ");
  var badges = (r.degraded || []).map(function(d){ return "<span class='badge bad'>" + esc(d) + "</span>"; }).join(" ");
  if(!r.hit_count) badges += " <span class='badge warn'>" + esc(S.empty) + "</span>";
  return "<div class='r" + (r.id === state.selected ? " sel" : "") + "' data-id='" + esc(r.id) + "'>" +
    "<div class='q'>" + esc(r.query) + "</div>" +
    "<div class='meta'><span>" + esc(ago(r.at)) + "</span><span>" + r.hit_count + " " + esc(S.hits) + "</span><span>" +
    r.latency_ms + " " + esc(S.ms) + "</span>" + (who ? "<span>" + who + "</span>" : "") + badges + "</div></div>";
}

function loadTimeline(more){
  var list = document.getElementById("list");
  if(!more){ state.recalls = []; state.next = 0; list.innerHTML = "<div class='say muted'>…</div>"; }
  var rf = document.getElementById("recordFilter");
  rf.hidden = !state.record;
  if(state.record) rf.innerHTML = esc(S.byRecord) + " <b>" + esc(state.record) + "</b> <a href='#' id='clearRecord'>" + esc(S.clear) + "</a>";
  getJSON(query(more ? state.next : 0)).then(function(res){
    state.recalls = state.recalls.concat(res.recalls || []);
    state.next = res.next_before || 0;
    if(!state.recalls.length){ list.innerHTML = "<div class='say'>" + esc(S.none) + "</div>"; return; }
    list.innerHTML = state.recalls.map(recallRow).join("") + (state.next ? "<button class='more' id='more'>" + esc(S.more) + "</button>" : "");
    if(!state.selected && window.innerWidth > 900) select(state.recalls[0].id);
  }).catch(function(err){
    list.innerHTML = isOff(err) ? offHTML() : "<div class='err'>" + esc(err.message || err) + "</div>";
    if(isOff(err)) document.getElementById("detail").innerHTML = "";
  });
}

document.getElementById("list").addEventListener("click", function(e){
  if(e.target.id === "more"){ loadTimeline(true); return; }
  var row = e.target.closest(".r[data-id]");
  if(row) select(row.getAttribute("data-id"));
});
document.addEventListener("click", function(e){
  if(e.target.id === "clearRecord"){ e.preventDefault(); state.record = ""; syncURL(); loadTimeline(false); }
});
var typing;
document.getElementById("contains").addEventListener("input", function(e){
  clearTimeout(typing); typing = setTimeout(function(){ state.contains = e.target.value.trim(); loadTimeline(false); }, 250);
});
document.getElementById("emptyOnly").addEventListener("change", function(e){ state.empty = e.target.checked; loadTimeline(false); });

function syncURL(){
  var p = new URLSearchParams(location.search);
  ["recall", "record_id", "view"].forEach(function(k){ p.delete(k); });
  if(state.selected) p.set("recall", state.selected);
  if(state.record) p.set("record_id", state.record);
  if(state.view === "records") p.set("view", "records");
  history.replaceState(null, "", "recalls" + (p.toString() ? "?" + p.toString() : ""));
}

function select(id){
  state.selected = id; syncURL();
  Array.prototype.forEach.call(document.querySelectorAll("#list .r"), function(el){ el.classList.toggle("sel", el.getAttribute("data-id") === id); });
  var box = document.getElementById("detail");
  box.innerHTML = "<div class='say muted'>…</div>";
  getJSON("api/recalls/get?id=" + encodeURIComponent(id)).then(function(r){ box.innerHTML = detailHTML(r); })
    .catch(function(err){ box.innerHTML = "<div class='err'>" + esc(err.message || err) + "</div>"; });
  if(window.innerWidth <= 900) box.scrollIntoView({behavior:"smooth", block:"start"});
}

function hitHTML(r, h){
  var via = (h.via || []).map(function(v){ return "<span class='badge'>" + esc(v) + "</span>"; }).join(" ");
  var scores = [S.score + " " + num(h.score)];
  if(h.similarity) scores.push("cos " + num(h.similarity));
  if(h.coverage) scores.push("cov " + num(h.coverage));
  var origin = [h.date, h.writer ? "key " + h.writer : "", h.source, h.project].filter(Boolean).map(esc).join(" · ");
  var graph = h.node_id ? " <a href='./?focus=" + encodeURIComponent(h.node_id) + "'>" + esc(S.graph) + "</a>" : "";
  // How the record says it is known: a guess is marked, a verified hit
  // shows its check on hover; an untagged one says nothing.
  var grade = h.grade === "asserted" || h.grade === "held"
    ? " <span class='badge warn' title='" + esc(h.grade) + "'>" + esc(S.unverified) + "</span>"
    : h.grade === "verified" ? " <span class='badge good' title='" + esc(h.grade_why || "") + "'>" + esc(S.verified) + "</span>" : "";
  return "<div class='hit'><span class='n'>" + h.rank + "</span><div class='t'>" +
    "<span class='badge'>" + esc(h.kind) + "</span>" + grade + " " + esc(h.title || h.id) +
    "<div class='meta'>" + via + "<span>" + esc(scores.join(" · ")) + "</span>" + (origin ? "<span>" + origin + "</span>" : "") +
    "<span class='muted'>" + esc(h.id) + "</span>" + graph + "</div></div>" +
    "<div class='fb' data-recall='" + esc(r.id) + "' data-record='" + esc(h.id) + "'>" +
    "<button class='useful" + (h.verdict === "useful" ? " on" : "") + "' data-v='useful' title='" + esc(S.useful) + "'>✓</button>" +
    "<button class='noise" + (h.verdict === "noise" ? " on" : "") + "' data-v='noise' title='" + esc(S.noise) + "'>✕</button></div></div>";
}

function sideHTML(name, side, finalIds){
  if(!side) return "";
  var out = "<div class='side'><h3>" + esc(name) + " <span class='badge'>" + esc(side.effective_mode || "") + "</span></h3>";
  if(side.reason) out += "<div class='reason'>" + esc(side.reason) + "</div>";
  var cands = side.candidates || {};
  Object.keys(cands).sort().forEach(function(ret){
    out += "<table><tr><th>" + esc(S.retriever) + ": " + esc(ret) + "</th><th>" + esc(S.rank) + "</th><th>" + esc(S.score) + "</th></tr>" +
      cands[ret].map(function(c){
        return "<tr" + (finalIds[c.id] ? " class='final'" : "") + "><td class='id'>" + esc(c.id) + "</td><td class='num'>" + c.rank + "</td><td class='num'>" + num(c.score) + "</td></tr>";
      }).join("") + "</table>";
  });
  if((side.dropped || []).length){
    out += "<h2 style='margin-top:10px'>" + esc(S.dropped) + "</h2><table><tr><th>" + esc(S.by) + "</th><th>id</th><th>" + esc(S.score) + "</th><th>" + esc(S.threshold) + "</th></tr>" +
      side.dropped.map(function(d){
        return "<tr><td>" + esc(d.by) + "</td><td class='id'>" + esc(d.id) + "</td><td class='num'>" + num(d.score) + "</td><td class='num'>" + num(d.threshold) + "</td></tr>";
      }).join("") + "</table>";
  }
  return out + "</div>";
}

function detailHTML(r){
  var ex = r.explain;
  var plan = ex ? ex.plan : {query: r.query};
  var finalIds = {}; (r.hits || []).forEach(function(h){ finalIds[h.id] = true; });
  var kv = [[S.query, plan.query], [S.keywords, (plan.keywords || []).join(", ")], [S.entities, (plan.entity_names || []).join(", ")],
    [S.mode, plan.retrieval_mode], [S.planner, plan.planner || r.planner], [S.asked, [r.key, r.host, r.project, r.agent].filter(Boolean).join(" · ")],
    ["request_id", r.request_id], ["", new Date(r.at).toLocaleString() + " · " + r.latency_ms + " " + S.ms]]
    .filter(function(p){ return p[1]; })
    .map(function(p){ return "<dt>" + esc(p[0]) + "</dt><dd>" + esc(p[1]) + "</dd>"; }).join("");
  var deg = (r.degraded || []).map(function(d){ return "<span class='badge bad'>" + esc(S.degraded) + ": " + esc(d) + "</span>"; }).join(" ");
  var out = "<section><h2>" + esc(S.plan) + "</h2><dl class='kv'>" + kv + "</dl>" + (deg ? "<div style='margin-top:8px'>" + deg + "</div>" : "") +
    "<div style='margin-top:8px'><a href='./?replay=" + encodeURIComponent(r.id) + "'>" + esc(S.replay) + " →</a></div></section>";
  out += "<section><h2>" + esc(S.hitsH) + " (" + (r.hits || []).length + ")</h2>" + (r.hits || []).map(function(h){ return hitHTML(r, h); }).join("") + "</section>";
  out += "<section><h2>" + esc(S.cands) + "</h2>" + (ex ? sideHTML("memory", ex.memory, finalIds) + sideHTML("knowledge", ex.knowledge, finalIds)
    : "<div class='muted'>" + esc(S.noExplain) + "</div>") + "</section>";
  return out;
}

document.getElementById("detail").addEventListener("click", function(e){
  var b = e.target.closest(".fb button"); if(!b) return;
  var box = b.parentNode, v = b.classList.contains("on") ? "clear" : b.getAttribute("data-v");
  fetch("api/recalls/feedback", {method:"POST", headers:{"Content-Type":"application/json"},
    body: JSON.stringify({recall_id: box.getAttribute("data-recall"), record_id: box.getAttribute("data-record"), verdict: v})})
    .then(function(r){ return r.json().then(function(b){ if(!r.ok) throw new Error(b.error || r.statusText); return b; }); })
    .then(function(){
      Array.prototype.forEach.call(box.querySelectorAll("button"), function(x){ x.classList.toggle("on", v !== "clear" && x.getAttribute("data-v") === v); });
    }).catch(function(err){ alertLine(err.message || err); });
});
function alertLine(msg){
  var d = document.createElement("div"); d.className = "err"; d.textContent = msg;
  document.getElementById("detail").prepend(d); setTimeout(function(){ d.remove(); }, 5000);
}

function loadRecords(){
  var list = document.getElementById("list"), box = document.getElementById("detail");
  list.innerHTML = box.innerHTML = "<div class='say muted'>…</div>";
  getJSON("api/recalls/stats?limit=50").then(function(s){
    var days = Math.max(1, Math.round((Date.now() - Date.parse(s.since)) / 86400000));
    var tiles = "<div class='tiles'>" + [[s.recalls, S.recalls], [s.empty_recalls, S.emptyN], [s.degraded_recalls, S.degN],
      [(s.most_recalled || []).length, S.reached]].map(function(t){ return "<div class='tile'><b>" + t[0] + "</b><span>" + esc(t[1]) + "</span></div>"; }).join("") + "</div>";
    list.innerHTML = "<section style='padding:14px 16px 0'><h2>" + esc(S.totals.replace("%d", days)) + "</h2></section>" + tiles +
      "<section style='padding:0 16px 14px'><h2>" + esc(S.most) + "</h2><table><tr><th>" + esc(S.count) + "</th><th></th><th>" + esc(S.last) + "</th></tr>" +
      (s.most_recalled || []).map(function(m){
        return "<tr><td class='num'>" + m.count + "</td><td><a href='#' data-record='" + esc(m.id) + "'>" + esc(m.title || m.id) + "</a>" +
          "<div class='meta'>" + (m.queries || []).map(function(q){ return "<span>“" + esc(q) + "”</span>"; }).join("") + "</div></td><td class='num'>" + esc(ago(m.last_at)) + "</td></tr>";
      }).join("") + "</table></section>";
    var never = s.never_recalled;
    box.innerHTML = "<section><h2>" + esc(S.never) + "</h2>" + (never === undefined || never === null
      ? "<div class='muted'>" + (s.recalls >= 0 ? esc(S.noneNever) : "") + "</div>"
      : never.map(function(n){
          return "<div class='hit'><span class='n'></span><div class='t'>" + esc(n.title) + "<div class='meta'><span>" + esc(ago(n.created_at)) +
            "</span><span class='muted'>" + esc(n.id) + "</span><a href='./?focus=" + encodeURIComponent(n.node_id) + "'>" + esc(S.graph) + "</a></div></div><span></span></div>";
        }).join("")) + "</section>";
  }).catch(function(err){
    list.innerHTML = isOff(err) ? offHTML() : "<div class='err'>" + esc(err.message || err) + "</div>";
    box.innerHTML = "";
  });
}
document.getElementById("list").addEventListener("click", function(e){
  var a = e.target.closest("a[data-record]"); if(!a) return;
  e.preventDefault(); state.record = a.getAttribute("data-record"); setView("timeline");
});

function setView(v){
  state.view = v; syncURL();
  document.getElementById("tabTimeline").classList.toggle("on", v === "timeline");
  document.getElementById("tabRecords").classList.toggle("on", v === "records");
  document.getElementById("filters").style.display = v === "timeline" ? "" : "none";
  if(v === "timeline"){
    document.getElementById("detail").innerHTML = "<div class='say' id='pick'>" + esc(S.pick) + "</div>";
    loadTimeline(false);
    if(state.selected) select(state.selected);
  } else loadRecords();
}
document.getElementById("tabTimeline").addEventListener("click", function(){ setView("timeline"); });
document.getElementById("tabRecords").addEventListener("click", function(){ setView("records"); });

document.getElementById("src").textContent = __SOURCE__;
setView(state.view);
</script>
</body>
</html>`
