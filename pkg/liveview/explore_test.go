package liveview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// The page promises to read and never write. Every tool it may run is held
// against the toolbox's own catalogue: it must exist, and the catalogue must
// call it a read.
func TestEveryToolTheViewMayRunOnlyReads(t *testing.T) {
	defs := map[string]cortexdb.ToolDefinition{}
	for _, d := range cortexdb.ToolDefinitions() {
		defs[d.Name] = d
	}
	for _, d := range cortexdb.KnowledgeMemoryFacadeToolDefinitions() {
		defs[d.Name] = d
	}
	for _, name := range ExploreTools {
		d, ok := defs[name]
		if !ok {
			t.Errorf("%s is on ExploreTools but no tool has that name", name)
			continue
		}
		if d.Mutates {
			t.Errorf("%s can write, and the view promises to only read", name)
		}
	}
	if exploreAllowed("knowledge_graph_query") {
		t.Error("SPARQL can update, so knowledge_graph_query must not be on the list")
	}
}

// The list is enforced where the call is made, not trusted to the routes.
func TestTheViewRefusesAToolNotOnItsList(t *testing.T) {
	ran := false
	call := guardCaller(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		ran = true
		return json.RawMessage(`{}`), nil
	})
	if _, err := call(context.Background(), "upsert_entities", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a write tool was accepted")
	}
	if ran {
		t.Fatal("the refused tool still reached the source")
	}
}

// exploreBrain is a small store: a hub with five neighbours, one of which
// has a neighbour of its own.
func exploreBrain(t *testing.T) *cortexdb.DB {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "explore.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	tools := db.GraphRAGTools()
	ents := []cortexdb.ToolEntityInput{{Name: "Quorvane Hall", Type: "place"}, {Name: "Mira Holt", Type: "person"}}
	rels := []cortexdb.ToolRelationInput{{From: "Mira Holt", To: "Quorvane Hall", Type: "works_at"}}
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("Ledger %d", i)
		ents = append(ents, cortexdb.ToolEntityInput{Name: name, Type: "record"})
		rels = append(rels, cortexdb.ToolRelationInput{From: name, To: "Quorvane Hall", Type: "kept_at"})
	}
	ents = append(ents, cortexdb.ToolEntityInput{Name: "Port Aster", Type: "place"})
	rels = append(rels, cortexdb.ToolRelationInput{From: "Mira Holt", To: "Port Aster", Type: "lives_in"})
	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: ents}); err != nil {
		t.Fatalf("entities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: rels}); err != nil {
		t.Fatalf("relations: %v", err)
	}
	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "m-mira", Scope: "global",
		Content: "Mira Holt keeps the ledgers at Quorvane Hall."}); err != nil {
		t.Fatalf("memory: %v", err)
	}
	return db
}

func TestFindReachesTheWholeStore(t *testing.T) {
	db := exploreBrain(t)
	res, err := Find(context.Background(), localCaller(db), "Port Aster")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(res) == 0 || res[0].ID != cortexdb.EntityNodeID("Port Aster") || res[0].Label != "Port Aster" {
		t.Fatalf("find Port Aster = %+v", res)
	}
}

func TestExpandBringsANodesNeighboursAndTheirRelations(t *testing.T) {
	db := exploreBrain(t)
	mira := cortexdb.EntityNodeID("Mira Holt")
	nb, err := Expand(context.Background(), localCaller(db), mira, 0)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	ids := map[string]bool{}
	for _, n := range nb.Nodes {
		ids[n.ID] = true
		if bookkeepingNode(n.Type) {
			t.Errorf("bookkeeping node %s reached the page", n.ID)
		}
	}
	for _, want := range []string{mira, cortexdb.EntityNodeID("Quorvane Hall"), cortexdb.EntityNodeID("Port Aster")} {
		if !ids[want] {
			t.Errorf("neighbourhood of Mira Holt lacks %s: %v", want, ids)
		}
	}
	types := map[string]bool{}
	for _, e := range nb.Edges {
		if !ids[e.Source] || !ids[e.Target] {
			t.Errorf("edge %s points outside the answer", e.ID)
		}
		types[e.Label] = true
	}
	if !types["works_at"] || !types["lives_in"] {
		t.Errorf("relation types = %v, want works_at and lives_in", types)
	}
}

func TestCypherAnswersATableAndTheNodesInIt(t *testing.T) {
	db := exploreBrain(t)
	ans, err := Cypher(context.Background(), localCaller(db),
		`MATCH (p)-[r:works_at]->(h) RETURN p, r, h.name AS hall`)
	if err != nil {
		t.Fatalf("cypher: %v", err)
	}
	if len(ans.Columns) != 3 || len(ans.Rows) != 1 {
		t.Fatalf("table = %v %v", ans.Columns, ans.Rows)
	}
	if ans.Rows[0][0] != "(Mira Holt:person)" || ans.Rows[0][1] != "-[:works_at]->" || ans.Rows[0][2] != "Quorvane Hall" {
		t.Errorf("row rendered as %q", ans.Rows[0])
	}
	if len(ans.Nodes) != 1 || len(ans.Edges) != 1 || ans.Edges[0].Label != "works_at" {
		t.Errorf("drawable answer = %+v / %+v", ans.Nodes, ans.Edges)
	}
}

// A write in Cypher is refused by the engine; the view must report that, not
// pretend it ran.
func TestCypherCannotWriteThroughTheView(t *testing.T) {
	db := exploreBrain(t)
	if _, err := Cypher(context.Background(), localCaller(db), `CREATE (n:person {name:"Eve"}) RETURN n`); err == nil {
		t.Fatal("a CREATE ran through the view")
	}
}

func TestAskReturnsWhatItRecalledAndTheThingsItNames(t *testing.T) {
	db := exploreBrain(t)
	ans, err := Ask(context.Background(), localCaller(db), "Who keeps the ledgers at Quorvane Hall?")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if len(ans.Memories) == 0 || ans.Memories[0].ID != "m-mira" {
		t.Fatalf("memories = %+v", ans.Memories)
	}
	if len(ans.Touched) == 0 {
		t.Fatal("the answer names nothing for the page to light up")
	}
}

// The inspector through tools — the shared brain's path — reads the same
// record the tables do.
func TestTheInspectorWorksThroughTools(t *testing.T) {
	db := exploreBrain(t)
	rec := callerRecord(localCaller(db))
	ctx := context.Background()

	node, err := rec(ctx, cortexdb.EntityNodeID("Mira Holt"))
	if err != nil || !node.Found || node.Edge || node.Type != "person" {
		t.Fatalf("node record = %+v, %v", node, err)
	}

	nb, err := Expand(ctx, localCaller(db), cortexdb.EntityNodeID("Mira Holt"), 0)
	if err != nil || len(nb.Edges) == 0 {
		t.Fatalf("expand for an edge id: %v", err)
	}
	edge, err := rec(ctx, nb.Edges[0].ID)
	if err != nil || !edge.Found || !edge.Edge || edge.From == "" || edge.To == "" {
		t.Fatalf("edge record = %+v, %v", edge, err)
	}

	missing, err := rec(ctx, "entity:nobody_here")
	if err != nil || missing.Found {
		t.Fatalf("missing record = %+v, %v", missing, err)
	}
}

func TestTheExploreRoutes(t *testing.T) {
	db := exploreBrain(t)
	src := SourceFor(db, "explore test")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := Start(ctx, src, 0, time.Hour, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })

	var payload Payload
	decodeGet(t, sv.URL()+"/api/graph", &payload)
	if !payload.Explore {
		t.Error("a source with Call did not say it can be explored")
	}

	var found struct{ Results []FindResult }
	decodeGet(t, sv.URL()+"/api/find?q=Mira+Holt", &found)
	if len(found.Results) == 0 {
		t.Error("/api/find found nothing")
	}

	resp, err := http.Get(sv.URL() + "/api/ask?q=x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET /api/ask = %d, want 400: a question is posted", resp.StatusCode)
	}

	body, _ := json.Marshal(map[string]string{"query": "MATCH (n:person) RETURN n"})
	resp, err = http.Post(sv.URL()+"/api/cypher", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var ans CypherAnswer
	_ = json.NewDecoder(resp.Body).Decode(&ans)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(ans.Nodes) != 1 {
		t.Errorf("POST /api/cypher = %d %+v", resp.StatusCode, ans)
	}

	big := strings.Repeat("x", exploreBodyLimit+10)
	resp, err = http.Post(sv.URL()+"/api/ask", "application/json", strings.NewReader(`{"q":"`+big+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an oversized question = %d, want 400", resp.StatusCode)
	}
}

// A source without Call cannot be explored, and says so rather than failing
// in a way that reads as a broken store.
func TestASourceWithoutCallSaysItCannotBeExplored(t *testing.T) {
	f := &fakeSource{}
	sv := startTestServer(t, f, false)
	var payload Payload
	decodeGet(t, sv.URL()+"/api/graph", &payload)
	if payload.Explore {
		t.Error("a source with no Call claims it can be explored")
	}
	resp, err := http.Get(sv.URL() + "/api/find?q=anything")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("/api/find on an unexplorable source = %d, want 501", resp.StatusCode)
	}
}

// The page reads its starting point from the URL, and every request it makes
// stays relative so it works behind an embedder's proxy.
func TestThePageOpensWhereItsLinkSays(t *testing.T) {
	for _, want := range []string{"OPTS.focus", "OPTS.find", "OPTS.ask", "OPTS.cypher", "OPTS.edge", "OPTS.hops",
		`getJSON("api/find`, `getJSON("api/expand`, `postJSON("api/ask"`, `postJSON("api/cypher"`,
		`"cortexdb:focus"`, "viewport-fit=cover", "(pointer:coarse)"} {
		if !strings.Contains(pageHTML, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	for _, absolute := range []string{`fetch("/api/`, `getJSON("/api/`, `postJSON("/api/`, `EventSource("/api/`} {
		if strings.Contains(pageHTML, absolute) {
			t.Errorf("the page requests %s…, an absolute path that leaves an embedder's mount point", absolute)
		}
	}
}

// On a real brain a hub is mentioned by more memories than one answer may
// carry. Expanding it must still bring its relations, not a page of
// memories joined to it by bookkeeping edges the scene does not draw.
func TestExpandSpendsItsLimitOnRelationsNotMentions(t *testing.T) {
	db := exploreBrain(t)
	ctx := context.Background()
	for i := 0; i < expandDefault+10; i++ {
		if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: fmt.Sprintf("m-%03d", i), Scope: "global",
			Content:  fmt.Sprintf("Note %d about the hall.", i),
			Entities: []cortexdb.ToolEntityInput{{Name: "Quorvane Hall", Type: "place"}}}); err != nil {
			t.Fatalf("memory %d: %v", i, err)
		}
	}
	nb, err := Expand(ctx, localCaller(db), cortexdb.EntityNodeID("Quorvane Hall"), 0)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	types := map[string]int{}
	for _, e := range nb.Edges {
		types[e.Label]++
	}
	if types["kept_at"] != 4 || types["works_at"] != 1 {
		t.Fatalf("relations of the hub = %v, want 4 kept_at and 1 works_at (nodes %d)", types, len(nb.Nodes))
	}
}

// Every family has a light and a dark set, the mode can follow the system,
// and each choice is a URL parameter and a message an embedder can send.
func TestThemesComeInLightAndDarkAndFollowTheSystem(t *testing.T) {
	for _, want := range []string{
		`[data-mode="light"]{`, `[data-theme="ember"][data-mode="dark"]{`, `[data-theme="ember"][data-mode="light"]{`,
		`[data-theme="mono"][data-mode="dark"]{`, `[data-theme="mono"][data-mode="light"]{`,
		`var THEME_ORDER = ["space", "ember", "mono"];`, `var MODE_ORDER = ["auto", "light", "dark"];`,
		"prefers-color-scheme: dark", "OPTS.theme", "OPTS.mode", `"cortexdb:theme"`, "localStorage.setItem(MODE_KEY",
	} {
		if !strings.Contains(pageHTML, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	for _, fam := range []string{"space", "ember", "mono"} {
		block := pageHTML[strings.Index(pageHTML, "  "+fam+": {label:"):]
		block = block[:strings.Index(block, "}}")]
		if !strings.Contains(block, "dark: {bg:") || !strings.Contains(block, "light:{bg:") {
			t.Errorf("theme %s does not carry both a light and a dark scene palette", fam)
		}
	}
}
