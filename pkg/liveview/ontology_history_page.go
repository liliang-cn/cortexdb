package liveview

// ontologyHistoryHTML is the ontology's version list: every saved version of a
// schema, when and by whom, what it changed, and when it was the active
// validator; pick two and the page shows their diff.
//
// A page of its own rather than a panel on /ontology. That page is one
// diagram of one schema, redrawn every half minute; this is a list that is
// read, paged and compared, and a refresh in the middle of choosing the two
// versions to compare would throw the choice away. Light by default, with the
// day/night switch the other pages share (same storage key, same ?mode=).
// Every request is relative, so the page works wherever the view is mounted.
const ontologyHistoryHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>CortexDB — ontology versions</title>
<style>
  :root{--bg:#f7f7f4;--card:#ffffff;--ink:#16181d;--ink2:#3d4350;--mute:#6b7280;--line:#e4e4de;--soft:#f0f0eb;
    --accent:#1f5fbf;--good:#13795b;--bad:#b42318;--warn:#a15c07;--chip:#eef2f8;
    --sans:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif;
    --mono:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
  [data-mode="dark"]{--bg:#111318;--card:#181b22;--ink:#e8eaee;--ink2:#c3c8d2;--mute:#8b93a3;--line:#2a2f3a;--soft:#1f232c;
    --accent:#7aa7ff;--good:#55c79b;--bad:#ff8a80;--warn:#f0b35a;--chip:#222838}
  *{box-sizing:border-box}
  html,body{margin:0;background:var(--bg);color:var(--ink);font-family:var(--sans);font-size:14px;line-height:1.45}
  a{color:var(--accent);text-decoration:none} a:hover{text-decoration:underline}
  header{position:sticky;top:0;z-index:5;background:var(--bg);border-bottom:1px solid var(--line);
    padding:12px max(16px,env(safe-area-inset-left)) 10px}
  .top{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
  h1{margin:0;font-size:18px;font-weight:650;letter-spacing:-.01em}
  nav{margin-left:auto;display:flex;gap:10px;align-items:center;flex-wrap:wrap;font-size:13px}
  button,select{font:inherit;font-size:12.5px;color:var(--ink2);border:1px solid var(--line);background:var(--card);
    border-radius:8px;padding:4px 11px;cursor:pointer}
  button.on{background:var(--ink);color:var(--bg);border-color:var(--ink)}
  button:disabled{opacity:.45;cursor:not-allowed}
  .bar{display:flex;gap:8px;margin-top:10px;flex-wrap:wrap;align-items:center;font-size:12.5px;color:var(--ink2)}
  main{display:grid;grid-template-columns:minmax(0,1.1fr) minmax(0,1fr);gap:16px;padding:16px max(16px,env(safe-area-inset-left)) 40px}
  @media (max-width:900px){main{grid-template-columns:minmax(0,1fr)}}
  .card{background:var(--card);border:1px solid var(--line);border-radius:12px}
  .say{padding:24px 20px;color:var(--ink2);text-align:center}
  .say b{display:block;color:var(--ink);margin-bottom:4px}
  h2{margin:0;padding:12px 16px 8px;font-size:12px;font-weight:600;letter-spacing:.06em;text-transform:uppercase;color:var(--mute)}
  .v{display:grid;grid-template-columns:22px minmax(0,1fr);gap:0 10px;padding:12px 16px;border-top:1px solid var(--line)}
  .v .pick{display:flex;flex-direction:column;gap:6px;padding-top:3px}
  .v .pick input{margin:0}
  .vn{font-weight:650;font-variant-numeric:tabular-nums}
  .meta{color:var(--mute);font-size:12px;margin-top:2px;display:flex;flex-wrap:wrap;gap:2px 10px}
  .badge{display:inline-block;font-size:11px;border-radius:5px;padding:0 6px;line-height:18px;background:var(--chip);color:var(--ink2);margin-left:6px}
  .badge.good{background:color-mix(in srgb,var(--good) 14%,transparent);color:var(--good)}
  .badge.bad{background:color-mix(in srgb,var(--bad) 14%,transparent);color:var(--bad)}
  .badge.warn{background:color-mix(in srgb,var(--warn) 16%,transparent);color:var(--warn)}
  .act{margin-top:6px;font-size:12px;color:var(--ink2);font-variant-numeric:tabular-nums}
  .act i{font-style:normal;color:var(--mute)}
  .more{display:block;width:100%;padding:10px;border:0;border-top:1px solid var(--line);background:transparent;color:var(--accent);border-radius:0 0 12px 12px}
  #diffcard{position:sticky;top:110px;max-height:calc(100vh - 130px);overflow:auto}
  @media (max-width:900px){#diffcard{position:static;max-height:none}}
  .ch{padding:9px 16px;border-top:1px solid var(--line)}
  .ch .k{font-size:11px;letter-spacing:.04em;text-transform:uppercase;color:var(--mute);font-family:var(--mono)}
  .ch .t{font-weight:600;overflow-wrap:anywhere}
  .ch .d{color:var(--ink2);font-size:12.5px;overflow-wrap:anywhere}
  .ch.brk{border-left:3px solid var(--bad)}
  .err{color:var(--bad)}
</style>
</head>
<body>
<header>
  <div class="top">
    <h1>Ontology versions</h1>
    <nav><a id="toonto" href="ontology">ontology</a><a href=".">graph</a><button id="modebtn" type="button" title="Light / dark / follow system" aria-label="Light, dark or follow system">☀</button></nav>
  </div>
  <div class="bar"><label>Schema <select id="schema"></select></label><span id="state"></span></div>
</header>
<main>
  <section class="card" id="list"><div class="say">Reading the versions…</div></section>
  <section class="card" id="diffcard">
    <h2>Compare</h2>
    <div class="bar" style="padding:0 16px 10px"><span>Tick two versions on the left, or use</span><button id="cmp" type="button" disabled>Compare ticked</button></div>
    <div id="diff"><div class="say">Nothing compared yet.</div></div>
  </section>
</main>
<script>
var Q = new URLSearchParams(location.search);
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

function esc(s){ return String(s == null ? "" : s).replace(/[&<>"']/g, function(c){
  return {"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c]; }); }
function getJSON(url){
  return fetch(url, {headers:{Accept:"application/json"}, cache:"no-store"}).then(function(r){
    return r.json().catch(function(){ return {}; }).then(function(b){
      if(!r.ok) throw new Error(b.error || r.statusText); return b;
    });
  });
}
function when(iso){ var d = new Date(iso); return isNaN(d) ? "" : d.toLocaleString(); }

var PAGE = 25, schemaId = Q.get("schema") || "", versions = [], total = 0, ticked = [];
var END = {replaced:"replaced by a newer version", switched:"another schema was activated", deactivated:"deactivated", deleted:"schema deleted"};

function activations(v){
  if(!v.activations || !v.activations.length) return "<div class='act'><i>never the active schema</i></div>";
  return v.activations.map(function(a){
    var to = a.active_to ? when(a.active_to) : "now";
    return "<div class='act'>active <b>" + esc(when(a.active_from)) + "</b> → <b>" + esc(to) + "</b>" +
      (a.activated_by ? " <i>switched on by " + esc(a.activated_by) + "</i>" : "") +
      (a.end_reason ? " <i>ended: " + esc(END[a.end_reason] || a.end_reason) + (a.ended_by ? " by " + esc(a.ended_by) : "") + "</i>" : "") + "</div>";
  }).join("");
}
function summary(v){
  var s = v.summary;
  if(!s) return "<span class='badge'>no change summary (saved before history was kept)</span>";
  if(s.initial) return "<span class='badge good'>first version</span>";
  if(!s.definition_changed) return "<span class='badge'>no change</span>";
  var out = "<span class='badge'>" + (s.changes || 0) + " change" + (s.changes === 1 ? "" : "s") + "</span>";
  if(s.breaking) out += "<span class='badge bad'>" + s.breaking + " breaking</span>";
  if(!s.changes) out += "<span class='badge warn'>actions, interfaces or object sets only</span>";
  return out;
}
function renderList(exists, current, hasMore){
  var box = document.getElementById("list");
  if(!versions.length){
    box.innerHTML = "<div class='say'><b>No versions stored for " + esc(schemaId) + "</b>Save a schema with ontology_save and each save appears here.</div>";
    return;
  }
  var html = "<h2>" + total + " version" + (total === 1 ? "" : "s") + (exists ? "" : " — schema deleted, history kept") + "</h2>";
  versions.forEach(function(v){
    var on = ticked.indexOf(v.version) >= 0;
    html += "<div class='v'><div class='pick'><input type='checkbox' data-v='" + v.version + "'" + (on ? " checked" : "") + " aria-label='Compare version " + v.version + "'></div><div>" +
      "<div><span class='vn'>v" + v.version + "</span>" + (v.active ? "<span class='badge good'>active now</span>" : "") +
      (exists && v.version === current && !v.active ? "<span class='badge'>latest</span>" : "") + summary(v) + "</div>" +
      "<div class='meta'><span>saved " + esc(when(v.saved_at)) + "</span>" + (v.saved_by ? "<span>by " + esc(v.saved_by) + "</span>" : "<span>writer not recorded</span>") +
      (v.name ? "<span>" + esc(v.name) + "</span>" : "") + "</div>" + activations(v) +
      (v.version > 1 ? "<div class='act'><a href='#' data-prev='" + v.version + "'>diff against v" + (v.version - 1) + "</a></div>" : "") +
      "</div></div>";
  });
  if(hasMore) html += "<button class='more' id='more' type='button'>Load older versions</button>";
  box.innerHTML = html;
  Array.prototype.forEach.call(box.querySelectorAll("input[data-v]"), function(cb){
    cb.addEventListener("change", function(){
      var n = Number(cb.getAttribute("data-v"));
      ticked = ticked.filter(function(x){ return x !== n; });
      if(cb.checked) ticked.push(n);
      if(ticked.length > 2) ticked = ticked.slice(-2);
      renderList(exists, current, hasMore);
      document.getElementById("cmp").disabled = ticked.length !== 2;
    });
  });
  Array.prototype.forEach.call(box.querySelectorAll("a[data-prev]"), function(a){
    a.addEventListener("click", function(ev){ ev.preventDefault(); var n = Number(a.getAttribute("data-prev")); compare(n - 1, n); });
  });
  var more = document.getElementById("more");
  if(more) more.addEventListener("click", function(){ more.disabled = true; loadVersions(true); });
}

function loadVersions(append){
  var off = append ? versions.length : 0;
  return getJSON("api/ontology/history?schema=" + encodeURIComponent(schemaId) + "&limit=" + PAGE + "&offset=" + off).then(function(r){
    versions = append ? versions.concat(r.versions || []) : (r.versions || []);
    total = r.total || versions.length;
    document.getElementById("state").textContent = r.exists ? "current version v" + r.current_version : "";
    renderList(r.exists, r.current_version, !!r.has_more);
  }).catch(function(err){
    document.getElementById("list").innerHTML = "<div class='say err'><b>Could not read the versions</b>" + esc(err.message) + "</div>";
  });
}

function compare(a, b){
  var lo = Math.min(a, b), hi = Math.max(a, b), box = document.getElementById("diff");
  box.innerHTML = "<div class='say'>Comparing v" + lo + " with v" + hi + "…</div>";
  getJSON("api/ontology/diff?schema=" + encodeURIComponent(schemaId) + "&from=" + lo + "&to=" + hi).then(function(r){
    var d = r.diff || {}, ch = d.changes || [];
    var html = "<div class='ch'><b>v" + lo + " → v" + hi + "</b> " + (ch.length ? ch.length + " change" + (ch.length === 1 ? "" : "s") : "no changes") +
      (d.has_breaking_changes ? "<span class='badge bad'>breaks data already written</span>" : "") + "</div>";
    ch.forEach(function(c){
      html += "<div class='ch" + (c.breaking ? " brk" : "") + "'><div class='k'>" + esc(c.kind) + (c.breaking ? " · breaking" : "") + "</div>" +
        "<div class='t'>" + esc(c.target) + "</div><div class='d'>" + esc(c.detail) + "</div></div>";
    });
    box.innerHTML = html;
  }).catch(function(err){
    box.innerHTML = "<div class='say err'><b>Could not compare</b>" + esc(err.message) + "</div>";
  });
}
document.getElementById("cmp").addEventListener("click", function(){ if(ticked.length === 2) compare(ticked[0], ticked[1]); });

// The schema picker comes from the ontology report: it lists every saved
// schema, and the active one is the default.
var sel = document.getElementById("schema");
sel.addEventListener("change", function(){
  schemaId = sel.value; versions = []; ticked = [];
  document.getElementById("diff").innerHTML = "<div class='say'>Nothing compared yet.</div>";
  document.getElementById("cmp").disabled = true;
  var u = new URL(location.href); u.searchParams.set("schema", schemaId); history.replaceState(null, "", u);
  loadVersions(false);
});
getJSON("api/ontology?gap=0").then(function(rep){
  var ss = rep.schemas || [];
  if(schemaId && !ss.some(function(s){ return s.schema_id === schemaId; })) ss = ss.concat([{schema_id: schemaId, name: "", version: 0, active: false}]);
  if(!schemaId){
    var act = ss.filter(function(s){ return s.active; })[0] || ss[0];
    schemaId = act ? act.schema_id : "";
  }
  sel.innerHTML = ss.map(function(s){
    return "<option value='" + esc(s.schema_id) + "'" + (s.schema_id === schemaId ? " selected" : "") + ">" + esc(s.schema_id) + (s.active ? " (active)" : "") + "</option>";
  }).join("");
  if(!schemaId){
    document.getElementById("list").innerHTML = "<div class='say'><b>No schema saved</b>There is nothing to list yet.</div>";
    return;
  }
  loadVersions(false);
}).catch(function(err){
  if(schemaId){ sel.innerHTML = "<option>" + esc(schemaId) + "</option>"; loadVersions(false); return; }
  document.getElementById("list").innerHTML = "<div class='say err'><b>Could not list the schemas</b>" + esc(err.message) + "</div>";
});
</script>
</body>
</html>`
