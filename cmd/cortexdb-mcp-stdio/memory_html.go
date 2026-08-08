package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// runMemoryHTML renders every stored memory to a self-contained, interactive
// HTML dashboard — cards grouped by scope, newest first, with live search,
// importance, and expiry — and prints its path. One-shot mode behind
// `--memory-html [out]`, used by /cortexdb-memory-view.
//
// Unlike --graph-html (which visualizes the entity graph derived from memory),
// this visualizes the memory *records* themselves.
func runMemoryHTML(outDir string) {
	memories, source := loadAllMemories(context.Background())

	if outDir == "" {
		outDir = defaultViewDir("memory-view")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: create %s: %v\n", outDir, err)
		os.Exit(1)
	}
	htmlPath := filepath.Join(outDir, "memory.html")
	if err := writeMemoryHTML(htmlPath, memories); err != nil {
		fmt.Fprintf(os.Stderr, "cortexdb: write html: %v\n", err)
		os.Exit(1)
	}
	if abs, aerr := filepath.Abs(htmlPath); aerr == nil {
		htmlPath = abs
	}
	fmt.Fprintf(os.Stderr, "cortexdb: read from %s\n", source)
	fmt.Fprintf(os.Stderr, "cortexdb: %d memories rendered\n", len(memories))
	fmt.Println(htmlPath)
}

// memoryView is the per-memory shape handed to the HTML template's JS.
type memoryView struct {
	ID         string  `json:"id"`
	Scope      string  `json:"scope"`
	Namespace  string  `json:"namespace"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
	Created    string  `json:"created"`
	Expires    string  `json:"expires"`
}

func writeMemoryHTML(path string, memories []cortexdb.MemoryRecord) error {
	views := make([]memoryView, 0, len(memories))
	for _, m := range memories {
		scope := m.Scope
		if scope == "" {
			scope = "global"
		}
		created := ""
		if !m.CreatedAt.IsZero() {
			created = m.CreatedAt.UTC().Format("2006-01-02 15:04")
		}
		expires := ""
		if m.ExpiresAt != nil {
			expires = m.ExpiresAt.UTC().Format("2006-01-02 15:04")
		}
		views = append(views, memoryView{
			ID:         m.ID,
			Scope:      scope,
			Namespace:  m.Namespace,
			Content:    m.Content,
			Importance: m.Importance,
			Created:    created,
			Expires:    expires,
		})
	}
	dataJSON, err := json.Marshal(views)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return memoryTemplate.Execute(f, map[string]any{
		"Memories":  template.JS(dataJSON),
		"Count":     len(views),
		"Generated": time.Now().UTC().Format("2006-01-02 15:04 UTC"),
	})
}

// memoryTemplate renders a self-contained dark-theme memory dashboard: a search
// box plus scope-grouped cards, all client-side (no dependencies).
var memoryTemplate = template.Must(template.New("memory").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>CortexDB memories</title>
<style>
  :root{--bg:#f8fafc;--card:#ffffff;--line:#e2e8f0;--fg:#0f172a;--dim:#64748b;--accent:#2563eb}
  html,body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 system-ui,-apple-system,sans-serif}
  header{position:sticky;top:0;background:rgba(248,250,252,.95);border-bottom:1px solid var(--line);padding:14px 20px;backdrop-filter:blur(4px)}
  h1{margin:0 0 8px;font-size:16px}h1 b{color:var(--accent)}
  #q{width:100%;max-width:520px;padding:8px 12px;background:var(--card);border:1px solid var(--line);border-radius:8px;color:var(--fg);font-size:14px}
  #stats{color:var(--dim);margin-top:6px;font-size:12px}
  main{padding:16px 20px 60px;max-width:900px;margin:0 auto}
  h2{margin:22px 0 10px;font-size:13px;text-transform:uppercase;letter-spacing:.06em;color:var(--accent)}
  .card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px 14px;margin:8px 0;box-shadow:0 1px 2px rgba(15,23,42,.04)}
  .content{white-space:pre-wrap;word-break:break-word}
  .meta{display:flex;flex-wrap:wrap;gap:10px;margin-top:8px;font-size:12px;color:var(--dim);align-items:center}
  .imp{height:6px;width:60px;background:var(--line);border-radius:3px;overflow:hidden}
  .imp>span{display:block;height:100%;background:var(--accent)}
  .badge{padding:1px 8px;border:1px solid var(--line);border-radius:20px}
  .exp{color:#dc2626}
  .empty{color:var(--dim);padding:40px;text-align:center}
</style></head>
<body>
<header>
  <h1>CortexDB memories — <b>{{.Count}}</b> total <span style="color:#94a3b8;font-weight:400">· {{.Generated}}</span></h1>
  <input id="q" placeholder="Search memories…" autofocus>
  <div id="stats"></div>
</header>
<main id="list"></main>
<script>
  var mem = {{.Memories}};
  var listEl = document.getElementById('list'), statsEl = document.getElementById('stats');
  function esc(s){var d=document.createElement('div');d.textContent=s==null?'':String(s);return d.innerHTML;}
  function render(filter){
    var f=(filter||'').toLowerCase().trim(), groups={}, order=[], shown=0;
    mem.forEach(function(m){
      if(f && (m.content||'').toLowerCase().indexOf(f)<0) return;
      if(!groups[m.scope]){groups[m.scope]=[];order.push(m.scope);}
      groups[m.scope].push(m); shown++;
    });
    listEl.innerHTML='';
    if(shown===0){listEl.innerHTML='<div class="empty">No memories match.</div>';statsEl.textContent='0 / '+mem.length;return;}
    order.forEach(function(scope){
      var h=document.createElement('h2');h.textContent=scope+' ('+groups[scope].length+')';listEl.appendChild(h);
      groups[scope].forEach(function(m){
        var imp=Math.max(0,Math.min(1,m.importance||0));
        var parts=['<div class="content">'+esc(m.content)+'</div>','<div class="meta">'];
        if(m.namespace && m.namespace!=='default') parts.push('<span class="badge">'+esc(m.namespace)+'</span>');
        parts.push('<span class="badge">'+esc(m.id)+'</span>');
        if(m.created) parts.push('<span>'+esc(m.created)+'</span>');
        if(imp>0) parts.push('<span title="importance '+imp.toFixed(2)+'" class="imp"><span style="width:'+(imp*100)+'%"></span></span>');
        if(m.expires) parts.push('<span class="exp">expires '+esc(m.expires)+'</span>');
        parts.push('</div>');
        var c=document.createElement('div');c.className='card';c.innerHTML=parts.join('');listEl.appendChild(c);
      });
    });
    statsEl.textContent=shown+' / '+mem.length+' memories';
  }
  document.getElementById('q').addEventListener('input',function(e){render(e.target.value);});
  render('');
</script>
</body></html>`))
