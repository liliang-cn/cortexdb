package liveview

import (
	"encoding/json"
	"strings"
)

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
const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="theme-color" content="#04060d">
<title>CortexDB — live brain</title>
<script src="https://unpkg.com/3d-force-graph@1.73.4/dist/3d-force-graph.min.js"></script>
<style>
  *{box-sizing:border-box;margin:0;padding:0}
  html,body{height:100%;overflow:hidden;background:#04060d;color:#c7d2e5;
    font:13px/1.5 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',sans-serif}
  #scene{position:absolute;inset:0}
  .bare .panel,.bare #boot{display:none!important}
  .panel{position:fixed;z-index:10;background:rgba(8,13,26,.86);border:1px solid #1b2740;
    border-radius:10px;backdrop-filter:blur(10px);box-shadow:0 8px 30px rgba(0,0,0,.5)}
  #head{top:14px;left:14px;padding:11px 14px;min-width:250px}
  #head h1{font-size:13px;font-weight:600;color:#e8eefc;letter-spacing:.2px;padding-right:16px}
  /* Relative, like everything else this page asks for: an absolute /ontology
     would leave the mount point when the view is framed inside another
     application. */
  #head h1 a{color:#7dd3fc;text-decoration:none;font-weight:500}
  #head h1 a:hover{text-decoration:underline}
  #counts{font-size:11px;color:#64748b;margin-top:5px}
  #counts b{color:#7dd3fc;font-weight:600}
  .badge{display:inline-flex;align-items:center;gap:5px;font-size:10px;padding:2px 7px;border-radius:20px;
    border:1px solid #1e3a5f;color:#7dd3fc;margin-top:7px;text-transform:uppercase;letter-spacing:.05em}
  .badge .led{width:6px;height:6px;border-radius:50%;background:#22d3ee;box-shadow:0 0 8px #22d3ee}
  .badge.cold{border-color:#334155;color:#64748b}
  .badge.cold .led{background:#475569;box-shadow:none;animation:none}
  .led{animation:blink 2s ease-in-out infinite}
  @keyframes blink{0%,100%{opacity:1}50%{opacity:.35}}
  #tools{top:14px;right:14px;padding:11px;width:222px}
  #tools .bd{display:flex;flex-direction:column;gap:8px}
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
  #feed .bd{display:flex;flex-direction:column;min-height:0;flex:1}
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
  /* Four panels sit in the four corners of a scene they are also covering. On
     a narrow window — a phone, or the graph framed inside another app — they
     meet in the middle and there is more chrome than graph. Each one folds to
     a chip that names what it is hiding, so the view can be cleared without
     losing the way back. */
  .fold{position:absolute;top:5px;right:5px;z-index:1;width:22px;height:22px;flex:none;padding:0;
    background:none;border:none;color:#3f4d66;font-size:15px;line-height:20px;cursor:pointer}
  .fold:hover{background:none;color:#cbd5e1}
  .fold::before{content:"–"}
  /* Written per panel, not as one .panel.folded rule: each panel sets its own
     width, padding and display through an id selector, and a class can never
     outrank one of those. */
  #head.folded>.fold::before,#tools.folded>.fold::before,
  #legend.folded>.fold::before,#feed.folded>.fold::before,#contract.folded>.fold::before{content:"+"}
  #head.folded>.bd,#tools.folded>.bd,#legend.folded>.bd,#feed.folded>.bd,
  #contract.folded>.bd{display:none}
  #head.folded,#tools.folded,#legend.folded,#feed.folded,#contract.folded{
    display:block;width:auto;min-width:0;max-height:none;overflow:visible;padding:5px 30px 5px 11px}
  #head.folded::before,#tools.folded::before,#legend.folded::before,#feed.folded::before,
  #contract.folded::before{
    content:attr(data-label);font-size:10px;text-transform:uppercase;
    letter-spacing:.06em;color:#4b5b76;white-space:nowrap}
  /* Over the controls rather than beside them: the centre of the top edge
     belongs to the explore bar now, and a reader looking at one record is not
     also flipping switches. Closing it gives the controls back. */
  #detail{top:14px;right:14px;width:340px;max-height:calc(100vh - 28px);overflow:auto;
    padding:12px;display:none;z-index:12}
  #detail.on{display:block}
  #detail .t{font-size:14px;font-weight:600;color:#e8eefc;word-break:break-word;padding-right:16px}
  #detail .x{position:absolute;top:8px;right:11px;cursor:pointer;color:#475569;font-size:17px;line-height:1}
  #detail .x:hover{color:#cbd5e1}
  #detail .r{margin-top:9px}
  #detail .k{font-size:9px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76}
  #detail .v{font-size:12px;color:#cbd5e1;word-break:break-word}
  /* The inspector's own rows. A field that names another record is a link, and
     it has to look like one: half of what this panel is for is walking from a
     fact to what it contradicts, and from a record to the decision that stands
     against it, without going back to the search box. */
  #detail a.lk{color:#7dd3fc;text-decoration:none;cursor:pointer;border-bottom:1px dotted #38536f}
  #detail a.lk:hover{color:#bae6fd;border-bottom-color:#7dd3fc}
  #detail .say{font-size:11px;color:#94a3b8;line-height:1.55;margin-top:9px}
  #detail .foot{font-size:10px;color:#3f4d66;margin-top:6px;line-height:1.5}
  #detail .sub{font-size:10px;text-transform:uppercase;letter-spacing:.06em;
    color:#4b5b76;margin:12px 0 5px;border-top:1px solid rgba(30,41,59,.7);padding-top:9px}
  /* The grade sits at the top in its own colour, because it is the answer to
     the question that brought the reader here, and reading it as a word in a
     list of words would make it one fact among nine. */
  #detail .gg{display:inline-flex;align-items:center;gap:6px;font-size:10px;
    text-transform:uppercase;letter-spacing:.06em;padding:2px 7px;border-radius:20px;
    border:1px solid currentColor;margin-top:8px}
  /* Quoted rather than paraphrased, and monospaced so a reader can see it is
     the source's words and not the page's. */
  #detail .quote{font:11px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace;color:#a9bcd8;
    background:#060b16;border-left:2px solid #23324f;border-radius:0 5px 5px 0;
    padding:6px 9px;margin-top:5px;white-space:pre-wrap;word-break:break-word;
    max-height:150px;overflow:auto}
  #detail .quote .qm{display:block;color:#3f4d66;font-size:9px;margin-bottom:3px}
  #detail .bad{color:#f87171}
  /* Pinned to a past instant. Loud, centred and above every panel, because a
     graph quietly showing last week is worse than one that cannot show it at
     all — and because the four corner panels are foldable and this must not be
     something a reader can put away while it is still true. */
  #past{position:fixed;top:0;left:0;right:0;z-index:30;padding:6px 14px;text-align:center;
    background:#b45309;color:#fff7ed;font-size:11px;font-weight:600;letter-spacing:.04em;
    box-shadow:0 2px 14px rgba(0,0,0,.55);display:none}
  #past.on{display:block}
  #past b{color:#fff;font-variant-numeric:tabular-nums}
  #past button{flex:none;margin-left:10px;padding:1px 8px;background:rgba(0,0,0,.28);
    border-color:rgba(255,255,255,.4);color:#fff7ed;font-size:10px}
  #past button:hover{background:rgba(0,0,0,.45);color:#fff}
  body.pinned #head,body.pinned #tools{top:38px}
  .badge.past{border-color:#b45309;color:#fdba74}
  .badge.past .led{background:#fdba74;box-shadow:none;animation:none}
  #cnote,#asofnote{font-size:10px;color:#4b5b76;line-height:1.5}
  #cnote.bad,#asofnote.bad{color:#fca5a5}
  /* The legend's rows carry the contract's meaning in grade mode, so they need
     to wrap instead of clipping to one line the way a type name does. */
  .li.wide{align-items:flex-start}
  .li.wide .dot{margin-top:4px}
  .li .mn{color:#4b5b76;font-size:10px;display:block}
  /* The contract panel: how much of this shelf stands on what, and what on it
     needs a person. Bottom-centre because the four corners are taken and a
     tally is a chart — it wants width more than it wants a corner. Everything
     else about it is its neighbours': the same chrome, the same fold, the same
     remembered fold, the same one-switch-one-URL-option rule. */
  #contract{bottom:14px;left:50%;transform:translateX(-50%);width:392px;max-height:44vh;
    padding:10px 12px;display:flex;flex-direction:column}
  #contract.off{display:none!important}
  #contract .bd{display:flex;flex-direction:column;min-height:0;flex:1}
  #contract h3{font-size:10px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76;
    margin-bottom:7px;flex:none;padding-right:16px}
  #contract .scroll{overflow:auto;min-height:0}
  #contract .sub{font-size:10px;text-transform:uppercase;letter-spacing:.06em;
    color:#4b5b76;margin:11px 0 5px}
  #contract .say{font-size:11px;color:#94a3b8;line-height:1.55}
  #contract .say b{color:#cbd5e1;font-weight:600}
  #contract .foot{font-size:10px;color:#3f4d66;margin-top:6px;line-height:1.5}
  .gr{display:grid;grid-template-columns:96px 1fr 84px;gap:8px;align-items:center;
    margin-bottom:3px;font-size:11px}
  .gr .gn{color:#94a3b8;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  .gr .gb{height:9px;border-radius:2px;background:#0a1120;display:flex;overflow:hidden}
  .gr .gb i{display:block;height:100%}
  /* Nodes solid, edges hatched, one bar and one hue. They are counted apart
     because an edge is an assertion about two things and a node is one thing;
     they are drawn together because they are the same shelf, and two charts
     would invite reading one of them and calling it the answer. */
  .gr .gb i.ge,#ckey i.ge{background-image:repeating-linear-gradient(135deg,
    rgba(4,6,13,.62) 0 2px,transparent 2px 4px)}
  .gr .gc{color:#64748b;text-align:right;white-space:nowrap;font-variant-numeric:tabular-nums}
  /* Untagged is a producer writing nothing; unknown is a producer writing
     something the contract does not define. One is the shape of the shelf and
     the other is somebody's bug, so they must not look alike. */
  .gr.untagged .gn{color:#64748b;font-style:italic}
  .gr.unknown .gn{color:#fca5a5}
  .gr.unknown .gn::before{content:"⚠ "}
  #ckey{font-size:10px;color:#3f4d66;margin:7px 0 2px;display:flex;gap:12px}
  #ckey i{display:inline-block;width:15px;height:8px;border-radius:2px;background:#64748b;
    margin-right:5px;vertical-align:-1px}
  .att{padding:6px 0;border-bottom:1px solid rgba(30,41,59,.5);font-size:11px}
  .att .ah{display:flex;gap:6px;align-items:baseline}
  .att .ag{flex:none;font-size:9px;text-transform:uppercase;letter-spacing:.06em;
    padding:1px 5px;border-radius:20px;border:1px solid currentColor}
  .att.held .ag{color:#fbbf24}
  .att.refused .ag{color:#f87171}
  .att .an{color:#cbd5e1;word-break:break-word;flex:1;min-width:0}
  .att .aw{color:#94a3b8;margin-top:3px;word-break:break-word}
  .att .aw.missing{color:#f87171}
  .att .am{color:#475569;font-size:10px;margin-top:2px;word-break:break-word}
  /* Below this the corner panels stop clearing each other: the title card runs
     under the controls, and the feed is wider than what is left. Folding takes
     care of the two that fold themselves; these two are the ones that stay. */
  @media (max-width:760px){
    #head{min-width:0;max-width:calc(100vw - 262px)}
    #feed{width:auto;max-width:calc(100vw - 28px)}
    #contract{width:auto;max-width:calc(100vw - 28px)}
  }
  /* ---------- explore ----------
     The bar that asks the store rather than the picture: find anything by
     name, ask a question, or run Cypher — each answer merged into the scene.
     Top centre on a desktop, where the eye starts; a full-width bar under the
     title on a phone. */
  #explore{top:14px;left:50%;transform:translateX(-50%);width:min(460px,calc(100vw - 540px));
    padding:9px 10px;display:none;flex-direction:column;gap:7px;max-height:calc(100vh - 28px)}
  #explore.on{display:flex}
  #explore:not(.on){display:none!important}
  #explore .bd{display:flex;flex-direction:column;gap:7px;min-height:0}
  #explore .tabs{display:flex;gap:4px;padding-right:22px}
  #explore .tabs button{flex:none;padding:4px 10px;font-size:11px}
  #explore .ask{display:flex;gap:6px}
  #explore input,#explore textarea{flex:1;min-width:0;padding:7px 10px;background:#060b16;border:1px solid #23324f;
    border-radius:7px;color:#dbe6f7;font-size:13px;font-family:inherit;resize:vertical}
  #explore textarea{font:12px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;min-height:58px}
  #explore input:focus,#explore textarea:focus{outline:none;border-color:#3b82f6}
  #explore .go{flex:none;padding:0 14px;background:#1d4ed8;border-color:#3b82f6;color:#fff}
  #xout{overflow:auto;min-height:0;max-height:46vh;font-size:12px}
  #xout:empty{display:none}
  #xout .say{color:#94a3b8;font-size:11px;padding:4px 2px}
  #xout .say.bad{color:#fca5a5}
  #xout a{color:#7dd3fc;text-decoration:none;border-bottom:1px dotted #38536f}
  #xout a:hover{color:#bae6fd}
  #xout .sub{font-size:10px;text-transform:uppercase;letter-spacing:.06em;color:#4b5b76;margin:9px 0 4px}
  .hit{display:flex;gap:8px;align-items:baseline;padding:6px 7px;border-radius:6px;cursor:pointer;
    border:1px solid transparent}
  .hit:hover,.hit:focus{background:#0f1a30;border-color:#1e3a5f;outline:none}
  .hit .dot{margin-top:4px}
  .hit .hl{flex:1;min-width:0;color:#dbe6f7;word-break:break-word}
  .hit .hm{flex:none;color:#4b5b76;font-size:10px}
  .hit .out{color:#fbbf24}
  .txt{padding:6px 7px;border-left:2px solid #23324f;margin:3px 0;color:#a9bcd8;word-break:break-word}
  .txt b{color:#cbd5e1;font-weight:600}
  .txt .m{display:block;color:#3f4d66;font-size:10px;margin-top:2px}
  .fact{padding:4px 7px;color:#cbd5e1;cursor:pointer;border-radius:5px}
  .fact:hover{background:#0f1a30}
  .fact i{font-style:normal;color:#7dd3fc}
  #xout table{border-collapse:collapse;width:100%;font:11px ui-monospace,SFMono-Regular,Menlo,monospace}
  #xout th,#xout td{text-align:left;padding:4px 6px;border-bottom:1px solid #16213a;vertical-align:top;
    word-break:break-word;color:#cbd5e1}
  #xout th{color:#64748b;font-weight:600;position:sticky;top:0;background:#0a1120}
  #xbar{display:flex;gap:6px;align-items:center;font-size:11px;color:#64748b}
  #xbar:empty{display:none}
  #xbar button{flex:none;padding:3px 9px}
  #explore.folded>.fold::before{content:"+"}
  #explore.folded>.bd{display:none}
  #explore.folded{display:block;width:auto;min-width:0;max-height:none;padding:5px 30px 5px 11px}
  #explore.folded::before{content:attr(data-label);font-size:10px;text-transform:uppercase;
    letter-spacing:.06em;color:#4b5b76;white-space:nowrap}
  /* The record's own actions: walk out from it, share where you are. */
  #dacts{display:flex;gap:6px;margin-top:9px}
  #dacts:empty{display:none}
  #dacts button{padding:6px 8px}
  #toast{position:fixed;left:50%;bottom:22px;transform:translateX(-50%);z-index:50;padding:7px 14px;
    background:#0f1a30;border:1px solid #1e3a5f;border-radius:20px;color:#bae6fd;font-size:12px;
    opacity:0;pointer-events:none;transition:opacity .2s}
  #toast.on{opacity:1}
  /* Between a desktop and a phone the top edge cannot hold three panels:
     the bar drops under the title card. */
  @media (max-width:1060px) and (min-width:761px){
    #explore{top:118px;left:14px;transform:none;width:min(460px,calc(100vw - 264px))}
  }
  /* A phone. The scene is the point, so everything else gets out of its way:
     the explore bar is one row across the top, the inspector is a sheet from
     the bottom edge, the corner panels start folded to chips, and every
     control is big enough for a thumb. 16px inputs stop iOS from zooming the
     page when one is tapped. */
  @media (max-width:760px){
    /* One line: the title, the counts beside it. The ontology page is a
       desktop diagram and its link is left off a phone. */
    #head{top:calc(8px + env(safe-area-inset-top));left:8px;padding:7px 30px 7px 10px;
      min-width:0;max-width:calc(100vw - 136px)}
    #head h1{font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;padding-right:0}
    #head h1 a{display:none}
    #head #counts{margin-top:2px}
    #head .badge{margin-top:4px}
    #tools{top:calc(8px + env(safe-area-inset-top));right:8px}
    #explore{top:calc(88px + env(safe-area-inset-top));left:8px;right:8px;transform:none;width:auto}
    #xout{max-height:38vh}
    #detail{top:auto;left:0;right:0;bottom:0;width:auto;max-height:62vh;border-radius:14px 14px 0 0;
      padding:14px 14px calc(14px + env(safe-area-inset-bottom))}
    #legend{bottom:calc(8px + env(safe-area-inset-bottom));left:8px}
    #feed{bottom:calc(8px + env(safe-area-inset-bottom));right:8px}
    #contract{bottom:calc(46px + env(safe-area-inset-bottom))}
    #explore input,#explore textarea,#tools input,#tools select{font-size:16px}
  }
  @media (pointer:coarse){
    button{min-height:36px;font-size:12px}
    #explore .tabs button{min-height:34px;padding:4px 12px}
    .fold{width:34px;height:34px;top:1px;right:1px;font-size:18px}
    .hit{padding:9px 8px}
  }
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

<!-- Said before anything else on the page, because everything else on the page
     is about to be a lie if this is on and unread. -->
<div id="past"><span id="pasttext"></span><button id="unpin" type="button">Back to now</button></div>

<div class="panel" id="head" data-label="CortexDB">
  <button class="fold" type="button" title="Collapse"></button>
  <div class="bd">
    <h1>CortexDB — live brain &nbsp;<a href="ontology" title="The ontology: what this brain is allowed to talk about">ontology →</a></h1>
    <div id="counts"><b id="n">0</b> nodes · <b id="e">0</b> edges</div>
    <div class="badge" id="live"><span class="led"></span><span id="livetext">connecting</span></div>
  </div>
</div>

<div class="panel" id="tools" data-label="Controls">
  <button class="fold" type="button" title="Collapse"></button>
  <div class="bd">
    <h3>Find</h3>
    <input id="q" placeholder="Highlight by name…" autocomplete="off">
    <h3>Type</h3>
    <select id="type"><option value="">All types</option></select>
    <h3>Relation</h3>
    <select id="etype"><option value="">All relations</option></select>
    <h3>Colour by</h3>
    <div class="row"><button id="cbytype" class="on">Type</button><button id="cbygrade">Grade</button></div>
    <div id="cnote"></div>
    <h3>As of</h3>
    <input id="asof" type="datetime-local" step="1">
    <div class="row"><button id="pin">Pin</button><button id="nowbtn">Now</button></div>
    <div id="asofnote"></div>
    <h3>Path</h3>
    <div class="row"><button id="pathbtn">Trace path</button><button id="clearpath">Clear</button></div>
    <div id="pathinfo"></div>
    <h3>View</h3>
    <div class="row"><button id="spin">Orbit</button><button id="fit">Fit</button></div>
    <div class="row"><button id="glow" class="on">Glow</button><button id="flow" class="on">Flow</button></div>
  </div>
</div>

<div class="panel" id="explore" data-label="Explore">
  <button class="fold" type="button" title="Collapse"></button>
  <div class="bd">
    <div class="tabs" role="tablist">
      <button type="button" id="xfind" class="on" data-mode="find">Find</button>
      <button type="button" id="xask" data-mode="ask">Ask</button>
      <button type="button" id="xcypher" data-mode="cypher">Cypher</button>
    </div>
    <form class="ask" id="xform" autocomplete="off">
      <input id="xq" placeholder="Find anything in the store by name…" enterkeyhint="search">
      <textarea id="xc" rows="3" hidden placeholder="MATCH (a)-[r]->(b) RETURN a, r, b LIMIT 25"></textarea>
      <button type="submit" class="go">Go</button>
    </form>
    <div id="xbar"></div>
    <div id="xout"></div>
  </div>
</div>

<div class="panel" id="legend" data-label="Node types">
  <button class="fold" type="button" title="Collapse"></button>
  <div class="bd"><h3 id="legendtitle">Node types</h3><div id="legenditems"></div></div>
</div>

<div class="panel" id="feed" data-label="Activity">
  <button class="fold" type="button" title="Collapse"></button>
  <div class="bd"><h3>Activity</h3><div id="feeditems"></div></div>
</div>

<div class="panel" id="contract" data-label="Contract">
  <button class="fold" type="button" title="Collapse"></button>
  <div class="bd"><h3>Knowledge contract</h3>
    <div class="scroll" id="cbody"><div class="say">Reading the contract…</div></div></div>
</div>

<div class="panel" id="detail"><span class="x" onclick="closeDetail()">&times;</span>
  <div class="t" id="dt"></div><div id="dacts"></div><div id="db"></div></div>
<div id="toast" role="status" aria-live="polite"></div>

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
var glowOn = true, flowOn = true; // set from OPTS once it exists, below
var spinning = false, spinAngle = 0, spinTimer = null;
var activityLive = false;
var userMovedCamera = false;
var hl = {}, hlCount = 0;       // ids an answer lit up (find, ask, cypher)
var hoverSet = null;            // the hovered node and its neighbours
var edgeFilter = "";            // show only this relation type
var explored = {nodes:{}, edges:{}}; // what exploring added beyond the core

/* ---------- options from the URL ----------

   The page is embedded by other applications — inside a dial, behind a mask,
   in a side panel two hundred pixels wide — and an embedder cannot press the
   buttons. So every switch the buttons flip is also a query parameter, read
   once here and applied where the switch lives:

     ?panels=0   no control panels, the graph alone
     ?spin=N     orbit from the start (the Orbit button, pressed for you), at
                 N times the button's pace - spin=1 is the pace the button
                 gives, spin=4 is a turn every twelve seconds
     ?glow=0     bloom off        ?flow=0   link particles off
     ?bg=RRGGBB  background colour, for a page shown through a shape
     ?fit=0      never re-frame the camera after the first fit
     ?contract=0 no contract panel, and nothing fetched for it - for an
                 embedder that only wants the shape of the graph
     ?color=grade  colour by the knowledge contract's grade rather than by
                 node type (the Grade button, pressed for you). Type stays the
                 default: a caller who upgrades and touches nothing gets the
                 page they had.
     ?as_of=MS   open pinned to that instant, unix milliseconds - and the page
                 says so in a banner, because a graph quietly showing last week
                 is worse than one that cannot show it at all

   And the ones that open the page somewhere rather than set a switch — a link
   someone shares is a question and an answer, not just a picture:

     ?focus=ID   fly to that node and open it; a name instead of an id is
                 looked up across the whole store, and a node outside the
                 drawn core is fetched with its neighbours
     ?find=TEXT  open Explore on Find with these results
     ?ask=TEXT   open Explore on Ask, answered, its things lit up
     ?cypher=Q   open Explore on Cypher, run, its nodes lit up
     ?q=TEXT     highlight by name (the Find box in Controls)
     ?type=T     show only nodes of type T   ?edge=E  only relations of type E
     ?hops=N     with focus: expand N hops out from it (1-3, default 0)
     ?explore=0  no Explore bar

   Nothing here overrides a person: a drag still stops the orbit, and a button
   still flips its switch. These are the starting positions, not locks. */
var OPTS = (function(){
  var q = {};
  try { new URLSearchParams(location.search).forEach(function(v, k){ q[k] = v; }); } catch(e){}
  return q;
})();
// A phone draws the same scene on a fraction of the GPU, and bloom plus a
// particle on every link is what costs the most. So on a small touch screen
// both start off — the buttons and ?glow=1 / ?flow=1 still turn them on.
var PHONE = window.matchMedia && window.matchMedia("(max-width:760px) and (pointer:coarse)").matches;
glowOn = OPTS.glow ? OPTS.glow !== "0" : !PHONE;
flowOn = OPTS.flow ? OPTS.flow !== "0" : !PHONE;
var BG = /^[0-9a-fA-F]{6}$/.test(OPTS.bg || "") ? "#" + OPTS.bg : "#04060d";
if(OPTS.panels === "0") document.documentElement.classList.add("bare");
if(OPTS.bg && BG !== "#04060d") document.body.style.background = BG;

var NAMED = {entity:"#38bdf8", concept:"#a78bfa", memory:"#34d399", knowledge:"#fbbf24",
  document:"#fb923c", person:"#f472b6", project:"#60a5fa", organization:"#2dd4bf",
  location:"#f59e0b", event:"#f87171", chunk:"#475569"};
function colorOf(t){
  if(!t) return "#7c8ba1";
  if(NAMED[t]) return NAMED[t];
  var h=0; for(var i=0;i<t.length;i++) h=(h*31+t.charCodeAt(i))%360;
  return "hsl("+h+",70%,65%)";
}

/* ---------- the contract's palette ----------

   Written in by Go rather than typed here, from liveview.GradeLegend() and the
   one map behind it. There were two grade palettes on this page before — this
   scene had none and the contract panel at the bottom had its own literal — and
   one of them disagreed with the product's: it drew asserted as a violet, which
   on a ladder whose whole point is that asserted means "nobody checked" reads
   as a distinction earned. Both halves of the page now read the same map, and
   the map is the one the rest of the product uses, lifted for this background.
   See grade.go for what "lifted" means and why it was necessary.

   GRADES is the legend in the contract's own order of standing, each row
   carrying the sentence that says what the word means — a legend that only
   names the five teaches nobody the difference between asserted and
   self_consistent, which is the one distinction a reader of this picture has
   to make. */
var GRADES = __GRADE_LEGEND__;
var GRADE_COLOR = __GRADE_PALETTE__;
var GRADE_UNTAGGED = __GRADE_UNTAGGED__;
var GRADE_UNKNOWN = __GRADE_UNKNOWN__;

/* Colour mode. Type is the default and stays the default: a caller who
   upgrades and touches nothing gets the page they had. ?color=grade is the
   embedder's way in, the same bargain every other switch on this page makes. */
var colorMode = OPTS.color === "grade" ? "grade" : "type";
// The instant the view is pinned to, unix milliseconds, 0 for now. Declared up
// here with the other switches because the contract panel below reads it — a
// tally of the present under a picture of the past is exactly the quiet
// disagreement that panel exists to surface rather than create.
var pinnedAt = Number(OPTS.as_of) > 0 ? Number(OPTS.as_of) : 0;
// Whether the source's reads carry a grade at all, from the opening snapshot.
// A source that cannot report them is not a shelf on which nothing is graded,
// and the legend says which rather than painting the brain the untagged grey.
var sourceGrades = false, sourceRecords = false, sourceTemporal = false, sourceExplore = false;

// gradeTint is GradeColor in Go, and makes the same three-way decision: a
// value the contract defines gets its rung on the ladder, no value at all gets
// the dim grey that says nobody stamped this, and anything else gets the rose
// that says a producer is writing a word nobody agreed on.
function gradeTint(g){
  if(!g) return GRADE_UNTAGGED;
  return GRADE_COLOR[g] || GRADE_UNKNOWN;
}
function gradingOn(){ return colorMode === "grade" && sourceGrades; }
function keyOf(l){
  var s=typeof l.source==="object"?l.source.id:l.source;
  var t=typeof l.target==="object"?l.target.id:l.target;
  return s+"|"+(l.label||"")+"|"+t;
}
function esc(s){return String(s==null?"":s).replace(/[&<>"]/g,function(c){
  return {"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;"}[c];});}

/* ---------- the scene ---------- */
G = ForceGraph3D()(document.getElementById("scene"))
  .backgroundColor(BG)
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
  // A relation is a record too, and until the inspector there was nothing on
  // this page it could have opened.
  .onLinkClick(onLinkClick)
  // Hovering a node lights it and what it touches and dims the rest, which is
  // the only way to read one neighbourhood out of a few thousand nodes. Not on
  // a touch screen: there is no hover, and a tap opens the record instead.
  .onNodeHover(PHONE ? null : onHover)
  .linkLabel(function(l){ return "<div style='background:#0a1120;border:1px solid #23324f;border-radius:6px;" +
    "padding:3px 8px;font:11px system-ui;color:#bae6fd'>" + esc(l.label||"related") + "</div>"; })
  .linkVisibility(function(l){ return !edgeFilter || l.label === edgeFilter; })
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
// baseColor is the one place the colour mode is read. Everything above it —
// the flash, the traced path, the search dimming — is a temporary state that
// outranks both modes, and folding the mode in here rather than at each of
// those keeps the precedence in one readable order.
function baseColor(n){ return gradingOn() ? gradeTint(n.grade) : colorOf(n.type); }
function nodeColor(n){
  var f = flash[n.id];
  if(f && f.until > Date.now()) return f.color;
  if(Object.keys(pathNodes).length) return pathNodes[n.id] ? "#fde68a" : "#131c2e";
  if(hoverSet) return hoverSet[n.id] ? baseColor(n) : "#141d31";
  if(hlCount) return hl[n.id] ? baseColor(n) : "#121a2b";
  if(query) return matches(n) ? baseColor(n) : "#121a2b";
  return baseColor(n);
}
function matches(n){
  return (n.label||"").toLowerCase().indexOf(query) >= 0 ||
         (n.id||"").toLowerCase().indexOf(query) >= 0;
}
// An edge carries a grade too, and this renderer takes a per-link colour
// accessor, so grade mode colours the relations as well as the things. That
// matters more than it sounds: a graph's assertions are mostly edges, and a
// picture that graded only the nodes would report a shelf far better
// established than it is — the argument graph.PropertyCount already makes about
// counting them apart.
function endsOf(l){
  return [typeof l.source==="object"?l.source.id:l.source, typeof l.target==="object"?l.target.id:l.target];
}
function linkColor(l){
  if(Object.keys(pathLinks).length) return pathLinks[keyOf(l)] ? "#fbbf24" : "#0d1526";
  if(hoverSet){ var h = endsOf(l); return (hoverSet.__id===h[0] || hoverSet.__id===h[1]) ? "#7dd3fc" : "#0d1526"; }
  if(hlCount){ var e = endsOf(l); if(hl[e[0]] && hl[e[1]]) return "#60a5fa"; return "#0d1526"; }
  if(l.__hot && l.__hot > Date.now()) return "#93c5fd";
  return gradingOn() ? gradeTint(l.grade) : "#2b5289";
}
function linkWidth(l){
  if(pathLinks[keyOf(l)]) return 2.4;
  if(hoverSet){ var h = endsOf(l); if(hoverSet.__id===h[0] || hoverSet.__id===h[1]) return 1.4; }
  if(hlCount){ var e = endsOf(l); if(hl[e[0]] && hl[e[1]]) return 1.3; }
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
  var nodes = p.nodes.map(function(n){
    return {id:n.id, label:n.label||n.id, type:n.type||"", grade:n.grade||""}; });
  nodes.forEach(function(n){ byId[n.id]=n; });
  // eid is the edge's own row id, and it is carried for one reason: the
  // inspector is asked about a record by id, and an edge identified only by
  // its two ends and a label is not a record anything can be asked about.
  var links = (p.edges||[]).filter(function(e){ return byId[e.source] && byId[e.target]; })
    .map(function(e){ return {source:e.source, target:e.target, label:e.label||"",
      grade:e.grade||"", eid:e.id||""}; });
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
  // Copies, for the reason mergeGraph gives: the live arrays belong to the
  // running simulation until graphData() hands it the new ones.
  var nodes = data.nodes.slice(), links = data.links.slice();
  var fresh = [];

  (d.added_nodes||[]).forEach(function(n){
    var ex = byId[n.id];
    // The grade is assigned rather than defaulted: a record that has just been
    // reviewed from held to verified arrives here as an upsert, and keeping the
    // old value because the new one is falsy would leave the scene showing a
    // grade the store no longer holds.
    if(ex){ ex.label = n.label||ex.label; ex.type = n.type||ex.type; ex.grade = n.grade||""; return; }
    var node = {id:n.id, label:n.label||n.id, type:n.type||"", grade:n.grade||""};
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
    // An edge already here is an upsert, not a duplicate: the same relation
    // re-sent because its grade changed. Updated in place, and lit, because
    // something was written — a review is a write.
    var have = linkKeys[k];
    if(have){ have.grade = e.grade||""; have.eid = e.id||have.eid; have.__hot = Date.now()+6000; return; }
    var link = {source:e.source, target:e.target, label:e.label||"",
      grade:e.grade||"", eid:e.id||"", __hot:Date.now()+6000};
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

// The type filter is built from the nodes on screen whichever mode is on: it
// filters by type, and that stays true when the colours stop meaning type.
// lastNodes is kept so flipping the mode can redraw the legend without waiting
// for the next snapshot.
var lastNodes = [];
function rebuildLegend(nodes){
  lastNodes = nodes;
  var seen = {}, types = [];
  nodes.forEach(function(n){ var t=n.type||"(untyped)"; if(!seen[t]){seen[t]=true;types.push(t);} });
  types.sort();
  var sel = document.getElementById("type"), keep = sel.value;
  sel.innerHTML = "<option value=''>All types</option>" + types.map(function(t){
    return "<option value='" + esc(t) + "'>" + esc(t) + "</option>"; }).join("");
  sel.value = keep;
  drawLegend(types);
}

// A colour scheme with no legend is decoration. In grade mode the legend is the
// contract's ladder in the contract's own order, every rung with the sentence
// that says what it means — including the two that are not grades, because a
// reader looking at a brain that is mostly dim grey has to be told that the
// grey is a producer's silence rather than a colour somebody chose.
function drawLegend(types){
  var title = document.getElementById("legendtitle");
  var box = document.getElementById("legenditems");
  var panel = document.getElementById("legend");
  if(colorMode !== "grade"){
    title.textContent = "Node types";
    panel.setAttribute("data-label", "Node types");
    box.innerHTML = types.map(function(t){
      return "<div class='li'><span class='dot' style='background:" +
        colorOf(t==="(untyped)"?"":t) + "'></span>" + esc(t) + "</div>";
    }).join("");
    return;
  }
  title.textContent = "Knowledge contract";
  panel.setAttribute("data-label", "Grades");
  if(!sourceGrades){
    // The distinction this page exists to keep: a source that cannot report
    // grades is not a shelf on which nothing is graded, and the second is what
    // an empty-looking legend would be read as.
    box.innerHTML = "<div class='say'>This view's source does not report grades, " +
      "so nothing here can be coloured by one. That is not the same as a store " +
      "on which nothing is graded.</div>";
    return;
  }
  box.innerHTML = GRADES.map(function(g){
    return "<div class='li wide'><span class='dot' style='background:" + g.color + "'></span>" +
      "<span>" + esc(g.grade) + "<span class='mn'>" + esc(g.means) + "</span></span></div>";
  }).join("");
}

/* The colour switch. Type is pressed on load, because type is what this page
   has always drawn and an upgrade must not change what a reader sees. */
function setColorMode(mode){
  colorMode = mode === "grade" ? "grade" : "type";
  document.getElementById("cbytype").classList.toggle("on", colorMode === "type");
  document.getElementById("cbygrade").classList.toggle("on", colorMode === "grade");
  var note = document.getElementById("cnote");
  if(colorMode === "grade" && !sourceGrades){
    note.className = "bad";
    note.textContent = "this source does not report grades";
  } else if(colorMode === "grade"){
    note.className = "";
    note.textContent = "nodes and edges, by how they are known";
  } else {
    note.className = "";
    note.textContent = "";
  }
  drawLegend(legendTypes());
  repaint();
}
function legendTypes(){
  var seen = {}, types = [];
  lastNodes.forEach(function(n){ var t=n.type||"(untyped)"; if(!seen[t]){seen[t]=true;types.push(t);} });
  types.sort();
  return types;
}
document.getElementById("cbytype").addEventListener("click", function(){ setColorMode("type"); });
document.getElementById("cbygrade").addEventListener("click", function(){ setColorMode("grade"); });

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

/* ---------- the inspector ----------

   The panel that joins the picture to the product. What the layout knows about
   a node — its label, its type, its degree, its neighbours — is drawn at once,
   because it is already here and a panel that opened empty while a fetch ran
   would feel broken. Everything the *store* knows arrives after: the source
   file, the chunk, the producer, the grade and its reason, when the fact became
   true, the words it was drawn from, what it was recorded as contradicting, and
   what anybody decided about it.

   Every field that names another record is a link, because the reason to look
   at provenance is almost never to read one field — it is to walk from a fact
   to what disagrees with it, or to the decision that stands against it. A page
   that printed those ids as text would make the reader retype them.

   One fetch per click, cancelled by the next: a reader clicking through six
   nodes must not end up with the sixth panel showing the third record, which is
   exactly what out-of-order responses would do. */
var inspectGen = 0;
function showDetail(n){ openInspector(n.id, n.label, false, n); }

// onLinkClick is why Edge carries its row id. An edge is a record — it has the
// source, the chunk and the grade on it — and before this the only thing on the
// page you could ask about was a node.
function onLinkClick(l){
  var s = typeof l.source==="object"?l.source.id:l.source;
  var t = typeof l.target==="object"?l.target.id:l.target;
  var from = byId[s] ? byId[s].label : s, to = byId[t] ? byId[t].label : t;
  var name = from + " —" + (l.label ? l.label + "→ " : "→ ") + to;
  if(!l.eid){
    // Honest rather than silent: the relation is on screen and cannot be
    // looked up, because this source did not give it a row id.
    openInspectorSays(name, "This view cannot look this relation up: the source did not name its record id.");
    return;
  }
  openInspector(l.eid, name, true, null);
}

function openInspectorSays(title, say){
  inspectGen++;
  document.getElementById("dt").textContent = title;
  document.getElementById("db").innerHTML = "<div class='say'>" + esc(say) + "</div>";
  document.getElementById("detail").classList.add("on");
}

// inspectById is what a link in the panel calls. A node that is on screen is
// flown to as well as opened, so the picture and the panel keep agreeing about
// what is being looked at; one that is not — a chunk, a superseded decision, a
// record outside the six hundred drawn — still opens, because the store can
// answer about it either way.
function inspectById(id){
  var n = byId[id];
  if(n && typeof n.x === "number"){
    var r = Math.hypot(n.x, n.y, n.z) || 1, k = 1 + 90/r;
    G.cameraPosition({x:n.x*k, y:n.y*k, z:n.z*k}, n, 700);
  }
  openInspector(id, n ? n.label : id, false, n || null);
}

function openInspector(id, title, isEdge, node){
  var gen = ++inspectGen;
  renderActs(id, isEdge);
  document.getElementById("dt").textContent = title || id;
  document.getElementById("db").innerHTML = localRows(id, isEdge, node) +
    "<div class='say'>Reading the record…</div>";
  document.getElementById("detail").classList.add("on");
  if(!sourceRecords){
    document.getElementById("db").innerHTML = localRows(id, isEdge, node) +
      "<div class='say'>This view's source cannot look a record up, so there is nothing " +
      "to show about where this came from. That is not the same as a record that carries nothing." +
      "</div>";
    return;
  }
  // Relative, like every other fetch on this page: it is mounted behind other
  // applications' proxies and an absolute path would leave the mount.
  fetch("api/record?id=" + encodeURIComponent(id), {cache:"no-store"})
    .then(function(res){ return res.json(); })
    .then(function(rep){
      if(gen !== inspectGen) return;   // the reader has moved on
      document.getElementById("db").innerHTML = localRows(id, isEdge, node) + recordRows(rep);
    })
    .catch(function(err){
      if(gen !== inspectGen) return;
      document.getElementById("db").innerHTML = localRows(id, isEdge, node) +
        "<div class='say bad'>" + esc(String(err)) + "</div>";
    });
}

function drow(k,v){
  return v ? "<div class='r'><div class='k'>"+esc(k)+"</div><div class='v'>"+v+"</div></div>" : "";
}
function dtext(k,v){ return drow(k, v ? esc(v) : ""); }
function dlink(id){
  return "<a class='lk' onclick=\"inspectById('" + esc(String(id)).replace(/'/g, "\\'") +
    "')\">" + esc(clip(id, 46)) + "</a>";
}

// localRows is what the layout already knew, drawn before any fetch returns.
function localRows(id, isEdge, node){
  var rows = "";
  if(node) rows += dtext("Type", node.type);
  rows += dtext(isEdge ? "Edge ID" : "ID", id);
  if(node){
    rows += dtext("Connections", String(degreeOf(node.id)));
    // Distinct neighbours: two things joined by three relations are one
    // neighbour, and listing it three times pushed the other seven out.
    var seenNb = {}, nbIds = [];
    (adj[node.id]||[]).forEach(function(a){ if(!seenNb[a.to]){ seenNb[a.to] = true; nbIds.push(a.to); } });
    var nb = nbIds.slice(0,8).map(function(x){ return dlink(x); });
    rows += drow("Linked to", nb.join(", ") + (nbIds.length > 8 ? " … +" + (nbIds.length - 8) : ""));
  }
  return rows;
}

// recordRows is what the store said.
function recordRows(rep){
  if(!rep || !rep.available){
    return "<div class='say'>This view cannot look the record up.<div class='foot'>" +
      esc((rep && rep.reason) || "no reason given") + "</div></div>";
  }
  if(!rep.found){
    // A third answer, and not the same as either of the other two: the source
    // looked, and the shelf no longer holds this. On a graph being written
    // under the view that is a real event rather than a fault.
    return "<div class='say'>The source has no record with this id any more — " +
      "it was on screen and is not on the shelf.</div>";
  }
  var html = "";
  // The grade first and in its own colour: it is the answer to the question
  // that brought the reader here, and a word in a list of words is not.
  var g = rep.grade || "";
  var label = g || "untagged";
  html += "<div><span class='gg' style='color:" + gradeTint(g) + "'>" + esc(label) + "</span></div>";
  if(rep.why) html += "<div class='say'>" + esc(rep.why) + "</div>";
  else if(g === "held" || g === "refused")
    html += "<div class='say bad'>no reason given — the contract requires one for " + esc(g) + "</div>";
  if(!g) html += "<div class='foot'>nothing on this record says how it is known</div>";

  html += dtext("State", rep.state);
  html += dtext("Source", rep.source);
  html += dtext("Chunk", rep.chunk);
  html += dtext("Producer", rep.producer);
  html += dtext("Produced at", rep.at);
  html += dtext("Asserted by", rep.by);
  html += dtext("Confidence", rep.confidence);
  // Not the same field as "produced at", and the difference is the whole point
  // of having both: one is when somebody made the record, the other is when the
  // fact it states became true.
  html += dtext("Valid from", rep.valid_from);
  if(rep.edge){
    html += drow("From", rep.from ? dlink(rep.from) : "");
    html += drow("To", rep.to ? dlink(rep.to) : "");
  }
  if(rep.inferred) html += dtext("Inferred by rule", rep.rule || "(unnamed)");
  html += drow("Document", rep.document_id ? esc(rep.document_id) : "");

  if((rep.contradicts||[]).length){
    html += "<div class='sub'>Contradicts</div>";
    // Still readable, and still linked. The disagreement was kept rather than
    // deleted, so both records are on the shelf and both can be opened.
    html += "<div class='v'>" + rep.contradicts.map(dlink).join(", ") + "</div>";
  }

  if((rep.text||[]).length){
    html += "<div class='sub'>Drawn from</div>";
    html += rep.text.map(function(t){
      return "<div class='quote'><span class='qm'>" + esc(t.chunk_id) +
        (t.document_id ? " · " + esc(t.document_id) : "") + "</span>" + esc(t.content) + "</div>";
    }).join("");
    var shown = rep.text.length, all = (rep.chunk_ids||[]).length;
    if(all > shown) html += "<div class='foot'>showing " + shown + " of " + all + " chunks</div>";
  } else if((rep.chunk_ids||[]).length){
    html += "<div class='sub'>Drawn from</div><div class='foot'>" +
      rep.chunk_ids.length + " chunks cited, none of them loaded</div>";
  }
  if((rep.missing_chunks||[]).length){
    // A citation pointing at text that has been deleted is exactly the failure
    // worth surfacing: the record still sounds right and no longer stands on
    // anything.
    html += "<div class='say bad'>" + rep.missing_chunks.length +
      " cited chunk(s) no longer exist: " + esc(rep.missing_chunks.join(", ")) + "</div>";
  }

  if((rep.decisions||[]).length){
    html += "<div class='sub'>Decided</div>" + rep.decisions.map(decisionRow).join("");
  }
  if((rep.chain||[]).length){
    html += "<div class='sub'>Rests on</div>" + rep.chain.map(decisionRow).join("");
    if(rep.chain_truncated)
      html += "<div class='foot'>the chain goes further than this — it was cut, not finished</div>";
  }
  if(rep.grade === "verified" && !(rep.decisions||[]).length && !rep.edge && rep.type !== "Decision"){
    // Verified means something outside the producer established it. Nothing in
    // the ledger saying who is not an error, and it is worth seeing.
    //
    // Not said about a decision. A decision is graded verified by construction
    // — somebody signed it — and asking which decision names a decision is a
    // question about its chain, which is drawn above.
    html += "<div class='foot'>graded verified, and no decision in the ledger names it</div>";
  }
  (rep.notes||[]).forEach(function(note){
    html += "<div class='foot'>" + esc(note) + "</div>";
  });
  return html;
}

function decisionRow(d){
  var meta = [];
  if(d.actor) meta.push(d.actor);
  if(d.kind) meta.push(d.kind);
  if(d.at) meta.push(d.at);
  if(d.grade) meta.push(d.grade);
  return "<div class='att'><div class='ah'>" +
    (d.verdict ? "<span class='ag'>" + esc(d.verdict) + "</span>" : "") +
    "<span class='an'>" + dlink(d.id) + "</span></div>" +
    (d.note ? "<div class='aw'>" + esc(clip(d.note, 180)) + "</div>" : "") +
    (meta.length ? "<div class='am'>" + esc(meta.join(" · ")) + "</div>" : "") +
    ((d.supersedes||[]).length ? "<div class='am'>supersedes " +
      d.supersedes.map(dlink).join(", ") + "</div>" : "") + "</div>";
}

function closeDetail(){ inspectGen++; document.getElementById("detail").classList.remove("on"); }

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
  // The same four routes the Explore bar takes, for an embedder that cannot
  // press it: {type:"cortexdb:focus", id}, {type:"cortexdb:find", query},
  // {type:"cortexdb:ask", query}, {type:"cortexdb:cypher", query}.
  if(msg && typeof msg.type === "string"){
    if(msg.type === "cortexdb:focus" && typeof msg.id === "string"){ focusNode(msg.id, 0); return; }
    var m = {"cortexdb:find":"find", "cortexdb:ask":"ask", "cortexdb:cypher":"cypher"}[msg.type];
    if(m && typeof msg.query === "string"){ runExplore(m, msg.query); return; }
  }
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
document.getElementById("flow").classList.toggle("on", flowOn);
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
  var pace = 0.0022 * (Number(OPTS.spin) > 0 ? Number(OPTS.spin) : 1);
  spinTimer = setInterval(function(){
    spinAngle += pace;
    var c = G.cameraPosition();
    G.cameraPosition({x: radius*Math.sin(spinAngle), y: c.y, z: radius*Math.cos(spinAngle)});
  }, 16);
}
document.getElementById("scene").addEventListener("pointerdown", function(){
  userMovedCamera = true;
  if(spinning) setSpin(false);
});
document.getElementById("scene").addEventListener("wheel", function(){ userMovedCamera = true; }, {passive:true});

/* ---------- folding the panels ----------

   Which panels are folded is remembered per browser, because someone who wants
   the legend out of the way wants it out of the way on the next look too.
   With nothing remembered yet the choice is made from the size of the window:
   on a desktop everything fits and everything opens, and below that the two
   panels that carry no controls fold themselves, since a window narrow enough
   for them to overlap the graph is one where they were covering it. */
var FOLD_KEY = "cortexdb.liveview.folded";
function foldable(){ return Array.prototype.slice.call(document.querySelectorAll(".panel[data-label]")); }
function saveFolds(){
  var ids = foldable().filter(function(p){ return p.classList.contains("folded"); })
                      .map(function(p){ return p.id; });
  try { localStorage.setItem(FOLD_KEY, ids.join(",")); } catch(e){}
}
function restoreFolds(){
  var stored = null;
  try { stored = localStorage.getItem(FOLD_KEY); } catch(e){}
  var ids;
  if(stored === null){
    // 760px is where #tools (222px, right) and #head (250px, left) stop
    // clearing each other with the graph still worth looking at between them.
    // 1120px is the same sum for the bottom row: #contract is 392px on the
    // centre line, and below that it starts overlapping the feed, so it comes
    // up folded — a chip that names what it is hiding, not a missing panel.
    if(window.innerWidth < 760) ids = ["tools", "legend", "feed", "contract"];
    else if(window.innerWidth < 1120) ids = ["contract"];
    else ids = [];
  } else {
    ids = stored ? stored.split(",") : [];
  }
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

/* ---------- the knowledge contract ----------

   The third route onto this page, and deliberately unlike the other two.
   Structure is polled and diffed because the graph moves under the view;
   activity is pushed the instant a call is handled because no poll can find a
   query. The contract is neither: two aggregate scans over the whole store and
   one filtered read, counting something that changes when a person clears a
   held record. So this panel fetches it on its own slow timer through its own
   endpoint, and it never rides the two-second structure poll — which would
   otherwise triple the store's read load for a number nobody watches tick.
   CONTRACT_MS mirrors liveview.ContractInterval; a test keeps them equal.

   Two things are shown and no more: the tally, and what needs a person. */
var CONTRACT_MS = 15000;
var contractOn = OPTS.contract !== "0";

// Untagged is muted because it is the shape of the shelf; unknown is rose and
// carries a mark because it is a producer writing something the contract does
// not define, and that is somebody's bug rather than somebody's silence.
//
// The colours are GRADE_COLOR, the one map Go writes into this page, rather than
// a literal of its own. This panel used to keep its own and the two disagreed
// about asserted; the scene above it now draws the same five, and the whole
// point of a shared palette is that the bar and the dot mean the same thing.
function gradeColor(r){
  if(r.kind === "untagged") return GRADE_UNTAGGED;
  if(r.kind === "unknown") return GRADE_UNKNOWN;
  return GRADE_COLOR[r.grade] || "#7c8ba1";
}
function num(n){ try { return Number(n||0).toLocaleString(); } catch(e){ return String(n||0); } }
function clip(s, n){ s = String(s==null?"":s); return s.length > n ? s.slice(0,n) + "…" : s; }

function renderContract(rep){
  var body = document.getElementById("cbody");
  if(!rep || !rep.available){
    // "This view cannot ask" is a different answer from "nothing here is
    // graded", and drawing an empty chart for the first would misreport the
    // second — which is the far more common state.
    body.innerHTML = "<div class='say'>This view cannot read the knowledge contract.<div class='foot'>" +
      esc((rep && rep.reason) || "no reason given") + "</div></div>";
    return;
  }
  var html = "";
  if(!rep.graded && !rep.untagged){
    html += "<div class='say'>Nothing on this shelf yet — no records to grade.</div>";
  } else if(!rep.graded){
    // The state a real machine is most likely in today. Five empty bars would
    // say a measurement was taken and came back empty; none was taken.
    html += "<div class='say'><b>" + num(rep.nodes) + "</b> nodes and <b>" + num(rep.edges) +
      "</b> edges, and not one of them carries a grade. Nothing here says how it is known.</div>";
  } else {
    // Scaled to the largest row, untagged included, so the graded bars read as
    // the fraction of the shelf they actually are — which on a real shelf is a
    // sliver, and saying otherwise is the whole thing this panel exists to
    // avoid. The floor is the one concession: at that scale twelve records
    // round to nothing, and a row that has some must not draw like a row that
    // has none. Under a percent of the bar is still visibly nothing.
    var max = 1;
    (rep.rows||[]).forEach(function(r){ max = Math.max(max, r.nodes + r.edges); });
    function seg(v){ return v > 0 ? Math.max(0.9, 100*v/max) : 0; }
    html += (rep.rows||[]).map(function(r){
      var c = gradeColor(r);
      var name = r.kind === "untagged" ? "untagged" : esc(r.grade || "(blank)");
      return "<div class='gr " + esc(r.kind) + "'>" +
        "<span class='gn' title=\"" + name + "\">" + name + "</span>" +
        "<span class='gb'>" +
          "<i style='width:" + seg(r.nodes) + "%;background:" + c + "'></i>" +
          "<i class='ge' style='width:" + seg(r.edges) + "%;background:" + c + "'></i>" +
        "</span><span class='gc'>" + num(r.nodes) + " · " + num(r.edges) + "</span></div>";
    }).join("");
    html += "<div id='ckey'><span><i></i>nodes</span><span><i class='ge'></i>edges</span></div>";
  }
  // Said out loud because the scene draws the most-connected part of a large
  // graph and this counts all of it: two numbers that disagree by an order of
  // magnitude are not a fault, and a reader should not have to guess that. Not
  // said over an empty store, where there are no two numbers to reconcile.
  if(rep.graded || rep.untagged){
    html += "<div class='foot'>counted over the whole store — the scene draws its most-connected part</div>";
  }

  var att = rep.attention || [];
  html += "<div class='sub'>Needs a person</div>";
  if(!att.length){
    html += "<div class='say'>Nothing held, nothing refused.</div>";
  } else {
    html += att.map(function(r){
      var g = r.grade === "refused" ? "refused" : "held";
      var meta = [];
      // Only when the record names one. A refusal says who refused, or says
      // nothing — never a name this page filled in.
      if(r.by) meta.push((g === "refused" ? "refused by " : "held by ") + r.by);
      if(r.state) meta.push(r.state);
      if(r.producer) meta.push(r.producer);
      if(r.source) meta.push(r.source);
      if(r.at) meta.push(r.at);
      return "<div class='att " + g + "'><div class='ah'><span class='ag'>" + g + "</span>" +
        "<span class='an'>" + esc(clip(r.content || r.id, 90)) +
        (r.edge ? " <span style='color:#475569'>edge</span>" : "") + "</span></div>" +
        // Every one of these carries a reason by contract, so a blank one is
        // itself the finding rather than an empty line to skip past.
        (r.why ? "<div class='aw'>" + esc(r.why) + "</div>"
               : "<div class='aw missing'>no reason given — the contract requires one</div>") +
        (meta.length ? "<div class='am'>" + esc(meta.join(" · ")) + "</div>" : "") + "</div>";
    }).join("");
    if(rep.truncated) html += "<div class='foot'>showing " + num(att.length) + " of " + num(rep.total) + "</div>";
  }
  body.innerHTML = html;
}

// Relative, for the same reason the stream is: the page is mounted behind
// other applications' proxies, and an absolute path would leave the mount.
function loadContract(){
  if(!contractOn) return;
  var panel = document.getElementById("contract");
  // Folded, nobody is looking, and this is two aggregate scans. It refreshes
  // the moment it is opened again.
  if(panel.classList.contains("folded")) return;
  if(pinnedAt){
    // The tally counts the store as it is now, and the scene above it is
    // pinned to an instant in the past. Read together they would be a quiet
    // disagreement of exactly the kind this panel exists to surface rather
    // than create, so while the view is pinned it says so and counts nothing.
    document.getElementById("cbody").innerHTML = "<div class='say'>The view is pinned to " +
      "a past instant. This tally counts the store as it is <b>now</b>, so it is not " +
      "read while the scene is showing then.</div>";
    return;
  }
  fetch("api/contract", {cache:"no-store"})
    .then(function(res){ return res.json(); })
    .then(renderContract)
    .catch(function(err){ renderContract({available:false, reason:String(err)}); });
}
if(contractOn){
  document.getElementById("contract").querySelector(".fold")
    .addEventListener("click", function(){ loadContract(); });
  loadContract();
  setInterval(loadContract, CONTRACT_MS);
} else {
  document.getElementById("contract").classList.add("off");
}

/* ---------- as of ----------

   The graph has kept its history since v2.99.0 and this page could only ask it
   about now. A date field and two buttons: Pin reads the graph at that instant,
   Now comes back.

   Two rules, and the second is the one that matters:

     - A page showing the past says so, loudly, in a banner that cannot be
       folded away and that pushes the rest of the chrome down so it cannot be
       mistaken for decoration. A graph quietly showing last week is worse than
       one that cannot show it at all.
     - Live updates stop while pinned, and stop on the server rather than here:
       the pinned stream never subscribes to the poller, so there is no delta to
       arrive and no code path on this page that could apply one to a graph it
       does not belong to. See handleStream. The poll itself keeps running,
       because the reader in the next tab is still watching today. */

// The field takes local time, which is what a person reading a date wants, and
// the wire takes unix milliseconds, which is what survives the trip without a
// timezone argument. This is the only place the two meet.
function localValue(ms){
  var d = new Date(ms), p = function(v){ return ("0"+v).slice(-2); };
  return d.getFullYear() + "-" + p(d.getMonth()+1) + "-" + p(d.getDate()) + "T" +
    p(d.getHours()) + ":" + p(d.getMinutes()) + ":" + p(d.getSeconds());
}
if(pinnedAt) document.getElementById("asof").value = localValue(pinnedAt);

function setPinned(ms){
  pinnedAt = ms > 0 ? ms : 0;
  // Reconnecting is how the pin takes effect, because the instant is a property
  // of the stream and not of a frame: a pinned connection is one the poller was
  // never told about.
  reconnect();
}
document.getElementById("pin").addEventListener("click", function(){
  var raw = document.getElementById("asof").value;
  var note = document.getElementById("asofnote");
  if(!raw){ note.className = "bad"; note.textContent = "pick an instant first"; return; }
  var ms = new Date(raw).getTime();
  if(!(ms > 0)){ note.className = "bad"; note.textContent = "that is not an instant"; return; }
  note.className = ""; note.textContent = "";
  setPinned(ms);
});
document.getElementById("nowbtn").addEventListener("click", function(){ setPinned(0); });
document.getElementById("unpin").addEventListener("click", function(){ setPinned(0); });

// showPast is the banner, and it is the one thing on this page that is allowed
// to shout. It is also what the two buttons and the badge agree with, so there
// is exactly one place that decides whether this view is claiming to be live.
function showPast(p){
  var bar = document.getElementById("past"), text = document.getElementById("pasttext");
  var note = document.getElementById("asofnote");
  var pinBtn = document.getElementById("pin");
  if(p.pinned){
    var when = new Date(p.as_of);
    text.innerHTML = "Showing the graph as it stood at <b>" + esc(when.toLocaleString()) +
      "</b> — this is the past, and it is not updating.";
    bar.classList.add("on");
    document.body.classList.add("pinned");
    pinBtn.classList.add("on");
    note.className = ""; note.textContent = "live updates are stopped";
    return;
  }
  bar.classList.remove("on");
  document.body.classList.remove("pinned");
  pinBtn.classList.remove("on");
  // Coming back to now, the tally is worth reading again at once. It refuses to
  // count under a pinned scene, so without this the panel would go on saying
  // that until its own slow timer came round.
  loadContract();
  if(p.pin_reason){
    // The pin was asked for and refused, and the scene is the live graph. Both
    // halves have to be said: silently showing now under a pin that looked
    // accepted is the exact failure the banner exists to prevent.
    note.className = "bad";
    note.textContent = "still showing now — " + p.pin_reason;
    pinnedAt = 0;
  } else if(!sourceTemporal){
    note.className = "";
    note.textContent = "this source cannot be asked about the past";
  } else {
    note.className = ""; note.textContent = "";
  }
}

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
  var fits = OPTS.fit === "0" ? [400] : [400, 1600, 3400, 6000];
  fits.forEach(function(ms){
    setTimeout(function(){ if(!userMovedCamera) fitCore(700); }, ms);
  });
  // The orbit starts after the last fit, or the fit would snap the camera
  // back out of the circle it had begun.
  if(Number(OPTS.spin) > 0) setTimeout(function(){ if(!userMovedCamera) setSpin(true); }, fits[fits.length - 1] + 900);
}

function setLive(connected, activity, pinned){
  var b = document.getElementById("live"), t = document.getElementById("livetext");
  activityLive = activity;
  b.classList.toggle("cold", !connected);
  b.classList.toggle("past", !!pinned);
  // Said plainly, because a ticker that never moves is otherwise indistinguishable
  // from one that is broken — and a badge saying "live" over a graph pinned to
  // last Tuesday is the same fault with the sign flipped.
  t.textContent = !connected ? "reconnecting" :
    (pinned ? "pinned · not updating" :
      (activity ? "live · watching calls" : "live · structure only"));
}

var retry = 800;
// One connection at a time, and a generation stamp on it. Pinning and
// unpinning are reconnects, and without the stamp the connection being replaced
// would fire its own onerror as it closed and schedule a retry that raced the
// new one — two streams, one of them to the wrong instant.
var stream = null, streamGen = 0;
function streamURL(){
  // Relative, not "/api/stream". The page is embedded behind reverse proxies —
  // an application putting an authenticated front door on a view that has none
  // of its own — and an absolute path would leave the mount point and land on
  // whatever the host serves at /api. Relative means the stream is always found
  // next to the page that asked for it, wherever that page is mounted.
  return pinnedAt ? "api/stream?as_of=" + pinnedAt : "api/stream";
}
function reconnect(){
  streamGen++;
  if(stream){ stream.close(); stream = null; }
  connect();
}
function connect(){
  var gen = ++streamGen;
  var es = new EventSource(streamURL());
  stream = es;
  es.addEventListener("snapshot", function(m){
    if(gen !== streamGen) return;
    var p = JSON.parse(m.data);
    retry = 800;
    // Read before the scene is drawn: the legend and the inspector both ask
    // what this source can answer, and a frame drawn before they knew would
    // colour by a grade nobody reported.
    sourceGrades = !!p.grades;
    sourceRecords = !!p.records;
    sourceTemporal = !!p.temporal;
    sourceExplore = !!p.explore;
    applySnapshot(p);
    reapplyExplored();
    setExploreAvailable();
    (p.events||[]).forEach(pushEvent);
    setLive(true, !!p.activity, !!p.pinned);
    showPast(p);
    setColorMode(colorMode);
    document.getElementById("head").title = "reading " + p.source;
    boot();
    runOpeningActions();
  });
  es.addEventListener("delta", function(m){
    // Belt and braces: a pinned stream is never subscribed to the poller, so
    // there is nothing to arrive. If one ever did, applying it would quietly
    // mix today into a picture of last week.
    if(gen !== streamGen || pinnedAt) return;
    applyDelta(JSON.parse(m.data));
  });
  es.addEventListener("activity", function(m){
    if(gen !== streamGen || pinnedAt) return;
    onActivity(JSON.parse(m.data));
  });
  es.onerror = function(){
    if(gen !== streamGen) return;   // superseded by a pin or an unpin
    es.close();
    setLive(false, activityLive, !!pinnedAt);
    // Backing off matters: the server is this machine's MCP process, and a page
    // left open overnight after it exited should not spend the night hammering
    // a closed port.
    setTimeout(function(){ if(gen === streamGen) connect(); }, retry);
    retry = Math.min(retry*2, 15000);
  };
}
connect();
// If the stream never arrives at all, show the scene anyway rather than a
// spinner that spins forever.
setTimeout(boot, 12000);


/* ---------- exploring the store ----------

   Everything above draws the core the server sends. This is how a reader
   reaches past it: Find looks a name up across the whole store, Ask runs the
   brain's own recall, Cypher runs a read-only query, and Expand fetches a
   node's neighbours whether or not the core included them. Answers are merged
   into the scene the way a delta is — nodes already placed keep their place —
   and remembered, so a reconnect that redraws the core puts them back. */
function setExploreAvailable(){
  var on = sourceExplore && OPTS.explore !== "0";
  document.getElementById("explore").classList.toggle("on", on);
}

// mergeGraph adds nodes and relations an answer brought, returning the ids
// that were not in the scene before.
function mergeGraph(nodes, edges, remember){
  // Copies, not the live arrays: graphData() applies on the next frame, and a
  // simulation tick in between would meet the new links still holding bare
  // ids ("Cannot create property 'vx' on string").
  var data = G.graphData(), allN = data.nodes.slice(), allL = data.links.slice(), fresh = [];
  (nodes||[]).forEach(function(n){
    if(remember) explored.nodes[n.id] = n;
    if(byId[n.id]) return;
    var anchor = null;
    var node = {id:n.id, label:n.label||n.id, type:n.type||"", grade:n.grade||"", __explored:true};
    // A newcomer starts beside a neighbour already placed, so the layout
    // settles it into the neighbourhood instead of flinging it in from the middle.
    (edges||[]).some(function(e){
      var other = e.source === n.id ? e.target : (e.target === n.id ? e.source : null);
      if(other && byId[other] && typeof byId[other].x === "number"){ anchor = byId[other]; return true; }
      return false;
    });
    var a = anchor || {x:0, y:0, z:0};
    node.x = a.x + (Math.random()-0.5)*30; node.y = a.y + (Math.random()-0.5)*30; node.z = a.z + (Math.random()-0.5)*30;
    byId[n.id] = node; allN.push(node); fresh.push(n.id);
  });
  (edges||[]).forEach(function(e){
    if(remember) explored.edges[e.source+"|"+(e.label||"")+"|"+e.target] = e;
    if(!byId[e.source] || !byId[e.target]) return;
    var k = e.source+"|"+(e.label||"")+"|"+e.target;
    if(linkKeys[k]) return;
    var link = {source:e.source, target:e.target, label:e.label||"", grade:e.grade||"", eid:e.id||"", __hot:Date.now()+5000};
    linkKeys[k] = link; allL.push(link);
  });
  rebuildAdj(allN, allL);
  linkTotal = allL.length;
  G.graphData({nodes:allN, links:allL});
  if(fresh.length) G.d3ReheatSimulation();
  setCounts(allN.length, allL.length);
  rebuildLegend(allN);
  refreshRelations();
  return fresh;
}
function reapplyExplored(){
  var ns = Object.keys(explored.nodes).map(function(k){ return explored.nodes[k]; });
  var es = Object.keys(explored.edges).map(function(k){ return explored.edges[k]; });
  if(ns.length || es.length) mergeGraph(ns, es, false);
  refreshRelations();
}

function getJSON(url){
  return fetch(url, {cache:"no-store"}).then(readAnswer);
}
function postJSON(url, body){
  return fetch(url, {method:"POST", cache:"no-store", headers:{"Content-Type":"application/json"},
    body:JSON.stringify(body)}).then(readAnswer);
}
function readAnswer(res){
  return res.json().catch(function(){ return {}; }).then(function(j){
    if(!res.ok) throw new Error(j.error || ("HTTP " + res.status));
    return j;
  });
}

// expandNode fetches what a node connects to and lights it.
function expandNode(id, quiet){
  return getJSON("api/expand?id=" + encodeURIComponent(id)).then(function(nb){
    var fresh = mergeGraph(nb.nodes, nb.edges, true);
    fresh.forEach(function(x){ strike(x, "#7dd3fc", 6000); });
    if(!quiet) toast(fresh.length ? fresh.length + " new around " + labelOf(id) : "Nothing new around " + labelOf(id));
    return nb;
  });
}
function labelOf(id){ return byId[id] ? byId[id].label : id; }

// focusNode flies to a node and opens it, fetching it first when it is not
// in the scene, and expanding hops rings out from it.
function focusNode(id, hops){
  id = String(id||"").trim();
  if(!id) return Promise.resolve();
  var chain = Promise.resolve();
  if(!byId[id] || hops > 0){
    if(!sourceExplore){ toast(byId[id] ? "" : "Not in view, and this source cannot be explored"); }
    else chain = expandRings(id, Math.max(1, hops||0)).catch(function(err){ toast(String(err.message||err)); });
  }
  return chain.then(function(){
    // A node that has just arrived has no settled position yet; give the
    // layout a moment before flying to it.
    setTimeout(function(){
      var n = byId[id];
      if(!n){ toast("No node " + id); return; }
      setHighlight([id].concat((adj[id]||[]).map(function(a){ return a.to; })), false);
      onNodeClick(n);
    }, byId[id] && typeof byId[id].x === "number" ? 0 : 700);
  });
}

// expandRings expands outward ring by ring: the node, then up to twelve of
// what that brought, and so on — bounded, because three unbounded hops from a
// hub is most of the store.
function expandRings(id, hops){
  var frontier = [id], seen = {};
  seen[id] = true;
  function step(h){
    if(h >= hops || !frontier.length) return Promise.resolve();
    var batch = frontier.slice(0, h === 0 ? 1 : 12);
    frontier = [];
    return Promise.all(batch.map(function(x){
      return expandNode(x, true).then(function(nb){
        (nb.nodes||[]).forEach(function(n){ if(!seen[n.id]){ seen[n.id] = true; frontier.push(n.id); } });
      });
    })).then(function(){ return step(h + 1); });
  }
  return step(0);
}

function setHighlight(ids, fly){
  hl = {}; hlCount = 0;
  (ids||[]).forEach(function(x){ if(byId[x] && !hl[x]){ hl[x] = true; hlCount++; } });
  renderBar();
  repaint();
  if(fly && hlCount){
    G.zoomToFit(900, 80, function(n){ return !!hl[n.id]; });
  }
}
function clearHighlight(){ hl = {}; hlCount = 0; renderBar(); repaint(); }

function onHover(n){
  if(!n){ if(hoverSet){ hoverSet = null; repaint(); } return; }
  hoverSet = {__id:n.id}; hoverSet[n.id] = true;
  (adj[n.id]||[]).forEach(function(a){ hoverSet[a.to] = true; });
  repaint();
}

// The relations select lists the types in the scene, most common first.
function refreshRelations(){
  var sel = document.getElementById("etype");
  var counts = {};
  G.graphData().links.forEach(function(l){ var t = l.label||""; if(t) counts[t] = (counts[t]||0) + 1; });
  var types = Object.keys(counts).sort(function(a,b){ return counts[b]-counts[a] || (a<b?-1:1); });
  var keep = edgeFilter;
  sel.innerHTML = "<option value=''>All relations</option>" + types.map(function(t){
    return "<option value='" + esc(t) + "'>" + esc(t) + " (" + counts[t] + ")</option>"; }).join("");
  sel.value = keep && counts[keep] ? keep : "";
}
document.getElementById("etype").addEventListener("change", function(){
  edgeFilter = this.value;
  G.linkVisibility(function(l){ return !edgeFilter || l.label === edgeFilter; });
});

/* The Explore bar. */
var xmode = "find", xgen = 0;
function setMode(m){
  xmode = m;
  ["find","ask","cypher"].forEach(function(k){
    document.getElementById("x"+k).classList.toggle("on", k === m);
  });
  var q = document.getElementById("xq"), c = document.getElementById("xc");
  q.hidden = m === "cypher"; c.hidden = m !== "cypher";
  q.placeholder = m === "ask" ? "Ask the brain a question…" : "Find anything in the store by name…";
  (m === "cypher" ? c : q).focus({preventScroll:true});
}
["find","ask","cypher"].forEach(function(k){
  document.getElementById("x"+k).addEventListener("click", function(){ setMode(k); });
});
document.getElementById("xform").addEventListener("submit", function(e){
  e.preventDefault();
  var text = xmode === "cypher" ? document.getElementById("xc").value : document.getElementById("xq").value;
  runExplore(xmode, text);
});
document.getElementById("xc").addEventListener("keydown", function(e){
  if(e.key === "Enter" && (e.metaKey || e.ctrlKey)){ e.preventDefault(); runExplore("cypher", this.value); }
});

function out(html){ document.getElementById("xout").innerHTML = html; }
function renderBar(){
  var bar = document.getElementById("xbar");
  if(!hlCount){ bar.innerHTML = ""; return; }
  bar.innerHTML = "<span>" + hlCount + " lit</span><button type='button' id='xfit'>Frame</button>" +
    "<button type='button' id='xclear'>Clear</button><button type='button' id='xshare'>Share</button>";
  document.getElementById("xfit").onclick = function(){ G.zoomToFit(800, 80, function(n){ return !!hl[n.id]; }); };
  document.getElementById("xclear").onclick = clearHighlight;
  document.getElementById("xshare").onclick = function(){ shareLink(currentQueryParams()); };
}
var lastQuery = null;
function currentQueryParams(){
  return lastQuery ? [[lastQuery.mode, lastQuery.text]] : [];
}

function runExplore(mode, text){
  text = String(text||"").trim();
  if(!text) return Promise.resolve();
  if(!sourceExplore){ out("<div class='say bad'>This view's source cannot be explored.</div>"); return Promise.resolve(); }
  var ex = document.getElementById("explore");
  ex.classList.remove("folded");
  if(xmode !== mode) setMode(mode);
  if(mode === "cypher") document.getElementById("xc").value = text; else document.getElementById("xq").value = text;
  lastQuery = {mode:mode, text:text};
  var gen = ++xgen;
  out("<div class='say'>" + (mode === "ask" ? "Asking…" : mode === "cypher" ? "Running…" : "Looking…") + "</div>");
  var job = mode === "find" ? getJSON("api/find?q=" + encodeURIComponent(text)).then(function(r){ if(gen===xgen) showFind(r); })
    : mode === "ask" ? postJSON("api/ask", {q:text}).then(function(r){ if(gen===xgen) showAsk(r); })
    : postJSON("api/cypher", {query:text}).then(function(r){ if(gen===xgen) showCypher(r); });
  return job.catch(function(err){ if(gen===xgen) out("<div class='say bad'>" + esc(err.message||err) + "</div>"); });
}

function hitRow(n, meta){
  var outside = !byId[n.id];
  return "<div class='hit' tabindex='0' data-id='" + esc(n.id) + "'><span class='dot' style='background:" +
    (gradingOn() ? gradeTint(n.grade) : colorOf(n.type)) + "'></span><span class='hl'>" + esc(n.label||n.id) +
    (outside ? " <span class='out' title='Not in the drawn core: opening it fetches it'>↗</span>" : "") +
    "</span><span class='hm'>" + esc(meta || n.type || "") + "</span></div>";
}
function wireHits(){
  Array.prototype.forEach.call(document.querySelectorAll("#xout .hit"), function(el){
    var go = function(){ focusNode(el.getAttribute("data-id"), 0); };
    el.addEventListener("click", go);
    el.addEventListener("keydown", function(e){ if(e.key === "Enter") go(); });
  });
  Array.prototype.forEach.call(document.querySelectorAll("#xout .fact"), function(el){
    el.addEventListener("click", function(){ focusNode(el.getAttribute("data-id"), 0); });
  });
}

function showFind(r){
  var res = r.results || [];
  if(!res.length){ out("<div class='say'>Nothing in the store is called that.</div>"); clearHighlight(); return; }
  var inView = res.filter(function(n){ return byId[n.id]; }).length;
  out("<div class='say'>" + res.length + " found · " + inView + " in view" +
    (res.length > inView ? " · ↗ opens one from outside it" : "") + "</div>" +
    res.map(function(n){ return hitRow(n, (n.type||"") + (n.match && n.match !== "exact" ? " · " + n.match : "")); }).join(""));
  wireHits();
  setHighlight(res.map(function(n){ return n.id; }), inView > 0);
}

function showAsk(r){
  var html = "";
  if(r.facts && r.facts.length){
    html += "<div class='sub'>Facts</div>" + r.facts.map(function(f){
      return "<div class='fact' data-id='" + esc(f.subject_id||"") + "'>" + esc(f.subject) + " <i>—" + esc(f.predicate) +
        "→</i> " + esc(f.object) + "</div>"; }).join("");
  }
  if(r.memories && r.memories.length){
    html += "<div class='sub'>Memories</div>" + r.memories.map(function(m){
      return "<div class='txt'>" + esc(m.text) + "<span class='m'>" + esc(m.id) + (m.score ? " · " + m.score.toFixed(2) : "") + "</span></div>"; }).join("");
  }
  if(r.passages && r.passages.length){
    html += "<div class='sub'>Knowledge</div>" + r.passages.map(function(m){
      return "<div class='txt'>" + (m.title ? "<b>" + esc(m.title) + "</b> " : "") + esc(m.text) +
        "<span class='m'>" + esc(m.id) + "</span></div>"; }).join("");
  }
  var touched = r.touched || [];
  var missing = touched.filter(function(x){ return !byId[x]; });
  if(!html) html = "<div class='say'>The brain recalled nothing for that.</div>";
  else html = "<div class='say'>" + touched.length + " things named · " + (touched.length - missing.length) + " in view" +
    (missing.length ? " · <a href='#' id='xbring'>bring the other " + missing.length + " in</a>" : "") +
    (r.mode ? " · " + esc(r.mode) : "") + "</div>" + html;
  out(html);
  wireHits();
  var b = document.getElementById("xbring");
  if(b) b.onclick = function(e){
    e.preventDefault();
    Promise.all(missing.slice(0, 8).map(function(x){ return expandNode(x, true).catch(function(){}); }))
      .then(function(){ setHighlight(touched, true); });
  };
  setHighlight(touched, true);
}

function showCypher(r){
  var cols = r.columns || [], rows = r.rows || [];
  var fresh = mergeGraph(r.nodes, r.edges, true);
  var html = "<div class='say'>" + (r.row_count||rows.length) + " row" + ((r.row_count||rows.length) === 1 ? "" : "s") +
    (r.truncated ? " (truncated)" : "") + " · " + (r.nodes||[]).length + " nodes" +
    (fresh.length ? ", " + fresh.length + " brought into view" : "") + "</div>";
  if(cols.length){
    html += "<table><thead><tr>" + cols.map(function(c){ return "<th>" + esc(c) + "</th>"; }).join("") + "</tr></thead><tbody>" +
      rows.map(function(row){ return "<tr>" + row.map(function(c){ return "<td>" + esc(c) + "</td>"; }).join("") + "</tr>"; }).join("") +
      "</tbody></table>";
  }
  out(html);
  setHighlight((r.nodes||[]).map(function(n){ return n.id; }), (r.nodes||[]).length > 0);
}

/* The record's actions: walk out from it, and share where you are. */
function renderActs(id, isEdge){
  var box = document.getElementById("dacts");
  if(isEdge){
    box.innerHTML = "<button type='button' id='dshare'>Share</button>";
  } else {
    box.innerHTML = (sourceExplore ? "<button type='button' id='dexpand'>Expand neighbours</button>" : "") +
      "<button type='button' id='dshare'>Share</button>";
    var ex = document.getElementById("dexpand");
    if(ex) ex.onclick = function(){
      ex.disabled = true; ex.textContent = "Expanding…";
      expandNode(id, false).then(function(){
        setHighlight([id].concat((adj[id]||[]).map(function(a){ return a.to; })), false);
      }).catch(function(err){ toast(String(err.message||err)); })
        .then(function(){ ex.disabled = false; ex.textContent = "Expand neighbours"; });
    };
  }
  document.getElementById("dshare").onclick = function(){ shareLink([["focus", id]]); };
}

// shareLink builds a link to this page with the given parameters, keeping
// the switches the reader arrived with, and hands it to the system share
// sheet on a phone or the clipboard elsewhere.
function shareLink(pairs){
  var u = new URL(location.href);
  ["focus","find","ask","cypher"].forEach(function(k){ u.searchParams.delete(k); });
  (pairs||[]).forEach(function(p){ if(p[1]) u.searchParams.set(p[0], p[1]); });
  var link = u.toString();
  if(navigator.share && PHONE){
    navigator.share({title:"CortexDB", url:link}).catch(function(){});
    return;
  }
  var done = function(){ toast("Link copied"); };
  if(navigator.clipboard && navigator.clipboard.writeText){
    navigator.clipboard.writeText(link).then(done, function(){ prompt("Link", link); });
  } else {
    prompt("Link", link);
  }
}

var toastTimer = null;
function toast(text){
  if(!text) return;
  var t = document.getElementById("toast");
  t.textContent = text; t.classList.add("on");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(function(){ t.classList.remove("on"); }, 2600);
}

// The parameters that open the page somewhere, run once, after the first
// picture is drawn — they ask about it, so it has to exist first.
var openingDone = false;
function runOpeningActions(){
  if(openingDone) return;
  openingDone = true;
  openingActions();
}
function openingActions(){
  if(OPTS.q){ document.getElementById("q").value = OPTS.q; query = OPTS.q.trim().toLowerCase(); repaint(); }
  if(OPTS.type){
    var tsel = document.getElementById("type");
    tsel.value = OPTS.type; tsel.dispatchEvent(new Event("change"));
  }
  if(OPTS.edge){
    refreshRelations();
    var esel = document.getElementById("etype");
    esel.value = OPTS.edge;
    if(esel.value === OPTS.edge) esel.dispatchEvent(new Event("change"));
  }
  if(!sourceExplore) return;
  if(OPTS.cypher) runExplore("cypher", OPTS.cypher);
  else if(OPTS.ask) runExplore("ask", OPTS.ask);
  else if(OPTS.find) runExplore("find", OPTS.find);
  if(OPTS.focus){
    var hops = Math.max(0, Math.min(3, Number(OPTS.hops)||0));
    var f = OPTS.focus;
    // An id names a node; anything else is a name, looked up first.
    if(byId[f] || f.indexOf(":") > 0) focusNode(f, hops);
    else getJSON("api/find?q=" + encodeURIComponent(f)).then(function(r){
      var first = (r.results||[])[0];
      if(first) focusNode(first.id, hops); else toast("Nothing called " + f);
    }).catch(function(err){ toast(String(err.message||err)); });
  }
}

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
  bloom.enabled = glowOn;
  btn.classList.toggle("on", glowOn);
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

// pageHTML is pageTemplate with the contract's palette written into it.
//
// The page is a var rather than a const because the palette must exist exactly
// once. It is Go's — grade.go decides which colour each rung of the contract's
// ladder gets, and why each is lifted off the product's light-background
// original — and a copy typed into the JavaScript would be a second place to
// keep it, with a page and a Go test that could disagree about what "asserted"
// looks like. There was already one such disagreement on this page before the
// palette existed in Go at all; see the comment over gradeColor.
//
// Built once at init and never per request, so serving the page is still one
// write of one string. A failure here is a programming error rather than a
// runtime condition — the values are marshalled from maps this package owns —
// so it panics rather than serving a page whose legend is the literal token
// "__GRADE_LEGEND__".
var pageHTML = buildPageHTML()

func buildPageHTML() string {
	must := func(v any) string {
		body, err := json.Marshal(v)
		if err != nil {
			panic("cortexdb/liveview: the grade palette will not marshal: " + err.Error())
		}
		return string(body)
	}
	out := pageTemplate
	for token, value := range map[string]string{
		"__GRADE_LEGEND__":   must(GradeLegend()),
		"__GRADE_PALETTE__":  must(gradePalette),
		"__GRADE_UNTAGGED__": must(GradeColorUntagged),
		"__GRADE_UNKNOWN__":  must(GradeColorUnknown),
	} {
		if !strings.Contains(out, token) {
			panic("cortexdb/liveview: the page has no " + token + " to fill in")
		}
		out = strings.ReplaceAll(out, token, value)
	}
	return out
}
