package liveview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// historyBrain is a real store with a People schema active: lives_in reaches
// one city from a person, knows reaches many people. Served through the
// view's own caller, so the routes are tested against the tools they call.
func historyBrain(t *testing.T) (*cortexdb.DB, *httptest.Server) {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "history.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	str := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}
	many, one := cortexdb.OntologyCardinalityMany, cortexdb.OntologyCardinalityOne
	if _, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{Activate: true, Schema: cortexdb.OntologySchema{
		SchemaID: "people", Name: "People",
		ObjectTypes: []cortexdb.OntologyObjectType{
			{APIName: "Person", PrimaryKey: "personId", Properties: []cortexdb.OntologyProperty{{APIName: "personId", DataType: str, Required: true}}},
			{APIName: "City", PrimaryKey: "cityId", Properties: []cortexdb.OntologyProperty{{APIName: "cityId", DataType: str, Required: true}}},
		},
		LinkTypes: []cortexdb.OntologyLinkType{
			{APIName: "lives_in",
				A: cortexdb.OntologyLinkSide{APIName: "city", ObjectTypeAPIName: "Person", Cardinality: one},
				B: cortexdb.OntologyLinkSide{APIName: "residents", ObjectTypeAPIName: "City", Cardinality: many}},
			{APIName: "knows",
				A: cortexdb.OntologyLinkSide{APIName: "friends", ObjectTypeAPIName: "Person", Cardinality: many},
				B: cortexdb.OntologyLinkSide{APIName: "friendsOf", ObjectTypeAPIName: "Person", Cardinality: many}},
		},
	}}); err != nil {
		t.Fatalf("save schema: %v", err)
	}
	ent := func(name, typ, pk string) cortexdb.ToolEntityInput {
		return cortexdb.ToolEntityInput{Name: name, Type: typ, Metadata: map[string]string{pk: strings.ToLower(name)}}
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		ent("Leo", "Person", "personId"), ent("Mia", "Person", "personId"), ent("Noah", "Person", "personId"),
		ent("Beijing", "City", "cityId"), ent("Chengdu", "City", "cityId"),
	}}); err != nil {
		t.Fatalf("entities: %v", err)
	}

	f := &fakeSource{}
	src := f.source()
	src.Call = localCaller(db)
	src.ReadAsOf = localReadAsOf(db)
	sctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	sv, err := New(sctx, src, time.Hour, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(sv.Handler())
	t.Cleanup(func() { ts.Close(); _ = sv.Close() })
	return db, ts
}

func relateNow(t *testing.T, db *cortexdb.DB, from, to, typ string) {
	t.Helper()
	if _, err := db.GraphRAGTools().UpsertRelations(context.Background(), cortexdb.ToolUpsertRelationsRequest{
		Relations: []cortexdb.ToolRelationInput{{From: from, To: to, Type: typ}},
	}); err != nil {
		t.Fatalf("relate %s %s %s: %v", from, typ, to, err)
	}
}

// pause keeps two writes on different sides of an instant the test then names.
func pause() time.Time {
	time.Sleep(15 * time.Millisecond)
	at := time.Now().UTC()
	time.Sleep(15 * time.Millisecond)
	return at
}

func getStatus(t *testing.T, u string, into any) int {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if into != nil {
		_ = json.NewDecoder(resp.Body).Decode(into)
	}
	return resp.StatusCode
}

func historyLineOf(t *testing.T, res cortexdb.GraphHistoryResult, predicate string) cortexdb.HistoryLine {
	t.Helper()
	for _, l := range res.Lines {
		if l.Predicate == predicate && l.Direction == "out" {
			return l
		}
	}
	t.Fatalf("no outbound %q line in %+v", predicate, res.Lines)
	return cortexdb.HistoryLine{}
}

func TestTheHistoryRouteReadsAMovedSingleValuedLinkAsOneLineAndNamesTheRule(t *testing.T) {
	db, ts := historyBrain(t)
	relateNow(t, db, "Leo", "Beijing", "lives_in")
	relateNow(t, db, "Leo", "Chengdu", "lives_in")

	var res cortexdb.GraphHistoryResult
	if code := getStatus(t, ts.URL+"/api/history?entity=Leo&relation=lives_in", &res); code != http.StatusOK {
		t.Fatalf("history: %d", code)
	}
	line := historyLineOf(t, res, "lives_in")
	if len(line.Versions) != 2 {
		t.Fatalf("want one line of two versions, got %+v", line.Versions)
	}
	old, cur := line.Versions[0], line.Versions[1]
	if old.Status != "superseded" || old.SupersededBy != cur.ID {
		t.Errorf("the first version should be superseded by the second: %+v", old)
	}
	if cur.Status != "current" || cur.InEffect == nil || cur.InEffect.Kind != cortexdb.InEffectReplaced {
		t.Fatalf("the current version should say it replaced the old one: %+v", cur)
	}
	if cur.InEffect.SchemaID != "people" || cur.InEffect.SchemaVersion < 1 {
		t.Errorf("the rule should cite the schema version: %+v", cur.InEffect)
	}
}

func TestTheHistoryRouteSaysSeveralValuesAreInEffectWithoutPickingAWinner(t *testing.T) {
	db, ts := historyBrain(t)
	relateNow(t, db, "Mia", "Leo", "knows")
	relateNow(t, db, "Mia", "Noah", "knows")

	var res cortexdb.GraphHistoryResult
	getStatus(t, ts.URL+"/api/history?entity=Mia", &res)
	line := historyLineOf(t, res, "knows")
	n := 0
	for _, v := range line.Versions {
		if v.InEffect != nil {
			n++
			if v.InEffect.Kind != cortexdb.InEffectSeveralInEffect || v.InEffect.SchemaOpinion == "" {
				t.Errorf("a live value of a many-valued link: %+v", v.InEffect)
			}
		}
	}
	// "knows" is its own line per object here or one line of two; either way
	// both values are reported in effect.
	if n == 0 {
		for _, l := range res.Lines {
			for _, v := range l.Versions {
				if v.InEffect != nil && v.InEffect.Kind == cortexdb.InEffectSeveralInEffect {
					n++
				}
			}
		}
	}
	if n < 2 {
		t.Fatalf("both values should be marked in effect, got %d: %+v", n, res.Lines)
	}
}

func TestTheHistoryRouteMarksWhatWasInEffectAtAnInstantAndKeepsTheLaterVersionsListed(t *testing.T) {
	db, ts := historyBrain(t)
	relateNow(t, db, "Leo", "Beijing", "lives_in")
	between := pause()
	relateNow(t, db, "Leo", "Chengdu", "lives_in")

	var res cortexdb.GraphHistoryResult
	u := ts.URL + "/api/history?entity=Leo&relation=lives_in&as_of=" + url.QueryEscape(between.Format(time.RFC3339Nano))
	if code := getStatus(t, u, &res); code != http.StatusOK {
		t.Fatalf("history as_of: %d", code)
	}
	if res.AsOf == nil {
		t.Error("the answer should say which instant it marked")
	}
	line := historyLineOf(t, res, "lives_in")
	if len(line.Versions) != 2 {
		t.Fatalf("the log should still be complete: %+v", line.Versions)
	}
	if line.Versions[0].InEffect == nil || line.Versions[1].InEffect != nil {
		t.Errorf("Beijing should be the one in effect then, Chengdu not yet: %+v", line.Versions)
	}
}

func TestTheHistoryRouteAcceptsAnOffsetWrittenWithAnUnescapedPlus(t *testing.T) {
	_, ts := historyBrain(t)
	// A person typing the address writes +08:00 and the query parser turns the
	// plus into a space; that must still be an instant, not a 400.
	var res cortexdb.GraphHistoryResult
	if code := getStatus(t, ts.URL+"/api/history?entity=Leo&as_of=2030-01-01T00:00:00+08:00", &res); code != http.StatusOK {
		t.Fatalf("an RFC 3339 offset was refused: %d", code)
	}
	if res.AsOf == nil || !res.AsOf.Equal(time.Date(2029, 12, 31, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("the offset was misread: %v", res.AsOf)
	}
}

func TestTheHistoryRouteRefusesWhatItCannotReadInsteadOfReadingItAsNow(t *testing.T) {
	_, ts := historyBrain(t)
	for _, q := range []string{
		"entity=Leo&as_of=yesterday",
		"entity=Leo&as_of=-5",
		"entity=Leo&from=soon",
		"entity=Leo&to=2026-13-45",
		"entity=Leo&limit=many",
		"entity=Leo&max_versions=-1",
		"as_of=2030-01-01",
		"entity=",
	} {
		var body map[string]string
		if code := getStatus(t, ts.URL+"/api/history?"+q, &body); code != http.StatusBadRequest {
			t.Errorf("?%s answered %d, want 400", q, code)
		} else if body["error"] == "" {
			t.Errorf("?%s: a 400 with no sentence", q)
		}
	}
}

func TestTheHistoryRoutePagesByLineAndTheCursorContinues(t *testing.T) {
	db, ts := historyBrain(t)
	relateNow(t, db, "Leo", "Beijing", "lives_in")
	relateNow(t, db, "Leo", "Mia", "knows")

	var first cortexdb.GraphHistoryResult
	getStatus(t, ts.URL+"/api/history?entity=Leo&limit=1", &first)
	if len(first.Lines) != 1 || first.NextCursor == "" {
		t.Fatalf("one line and a cursor expected: %+v", first)
	}
	var second cortexdb.GraphHistoryResult
	getStatus(t, ts.URL+"/api/history?entity=Leo&limit=1&cursor="+url.QueryEscape(first.NextCursor), &second)
	if len(second.Lines) != 1 || second.Lines[0].Predicate == first.Lines[0].Predicate {
		t.Errorf("the next page should be the other line: %+v then %+v", first.Lines, second.Lines)
	}
	var bad map[string]string
	if code := getStatus(t, ts.URL+"/api/history?entity=Leo&cursor=nonsense", &bad); code == http.StatusOK {
		t.Error("a cursor that is not one was accepted")
	}
}

func TestEveryHistoryRouteOnlyReads(t *testing.T) {
	_, ts := historyBrain(t)
	for _, path := range []string{"/api/history?entity=Leo", "/api/ontology/history?schema=people", "/api/ontology/diff?schema=people&from=1"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader("{}"))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s answered %d, want 405", method, path, resp.StatusCode)
			}
		}
	}
}

func TestTheHistoryRoutesSayWhenTheSourceCannotBeAsked(t *testing.T) {
	f := &fakeSource{}
	sv := startTestServer(t, f, false)
	for _, path := range []string{"/api/history?entity=Leo", "/api/ontology/history?schema=people", "/api/ontology/diff?schema=people&from=1"} {
		var body map[string]string
		if code := getStatus(t, sv.URL()+path, &body); code != http.StatusNotImplemented || body["error"] == "" {
			t.Errorf("%s: %d %v, want 501 with a sentence", path, code, body)
		}
	}
}

func TestExpandAsOfReturnsTheNeighbourhoodAsItStoodAndFindDropsWhatDidNotExistYet(t *testing.T) {
	db, ts := historyBrain(t)
	relateNow(t, db, "Leo", "Beijing", "lives_in")
	between := pause()
	relateNow(t, db, "Leo", "Chengdu", "lives_in")
	at := url.QueryEscape(between.Format(time.RFC3339Nano))
	// With a schema the node id is not EntityNodeID(name); ask the history for it.
	var h cortexdb.GraphHistoryResult
	getStatus(t, ts.URL+"/api/history?entity=Leo", &h)
	leo := h.Entity

	labels := func(u string) map[string]bool {
		var nb Neighbourhood
		if code := getStatus(t, u, &nb); code != http.StatusOK {
			t.Fatalf("%s: %d", u, code)
		}
		out := map[string]bool{}
		for _, n := range nb.Nodes {
			out[n.Label] = true
		}
		return out
	}
	now := labels(ts.URL + "/api/expand?id=" + url.QueryEscape(leo))
	if !now["Chengdu"] {
		t.Fatalf("now, Leo lives in Chengdu: %v", now)
	}
	then := labels(ts.URL + "/api/expand?id=" + url.QueryEscape(leo) + "&as_of=" + at)
	if !then["Beijing"] || then["Chengdu"] {
		t.Errorf("as of between the two, Leo lived in Beijing: %v", then)
	}

	// Nothing existed before the first write.
	before := url.QueryEscape(time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339))
	var found struct {
		Results []FindResult `json:"results"`
	}
	getStatus(t, ts.URL+"/api/find?q=Leo", &found)
	if len(found.Results) == 0 {
		t.Fatal("find should see Leo now")
	}
	getStatus(t, ts.URL+"/api/find?q=Leo&as_of="+before, &found)
	if len(found.Results) != 0 {
		t.Errorf("Leo did not exist two days ago: %+v", found.Results)
	}
	var gone Neighbourhood
	getStatus(t, ts.URL+"/api/expand?id="+url.QueryEscape(leo)+"&as_of="+before, &gone)
	if len(gone.Nodes) != 0 {
		t.Errorf("an entity that did not exist yet was anchored into the past: %+v", gone.Nodes)
	}
}

func TestFindAndExpandRefuseAMalformedInstant(t *testing.T) {
	_, ts := historyBrain(t)
	for _, u := range []string{"/api/find?q=Leo&as_of=tomorrow", "/api/expand?id=x&as_of=0", "/api/expand?id=x&as_of=2026-99-01"} {
		var body map[string]string
		if code := getStatus(t, ts.URL+u, &body); code != http.StatusBadRequest || body["error"] == "" {
			t.Errorf("%s: %d %v, want 400 with a sentence", u, code, body)
		}
	}
}

// saveThreeVersions saves the people schema three times, each adding a
// property, and returns after the third.
func saveThreeVersions(t *testing.T, db *cortexdb.DB) {
	t.Helper()
	ctx := context.Background()
	cur, err := db.GetOntologySchema(ctx, cortexdb.OntologyGetRequest{SchemaID: "people"})
	if err != nil {
		t.Fatalf("get schema: %v", err)
	}
	for _, name := range []string{"nickname", "birthday"} {
		s := cur.Schema
		props := append([]cortexdb.OntologyProperty{}, s.ObjectTypes[0].Properties...)
		props = append(props, cortexdb.OntologyProperty{APIName: name, DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}})
		types := append([]cortexdb.OntologyObjectType{}, s.ObjectTypes...)
		types[0].Properties = props
		s.ObjectTypes = types
		if _, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{Schema: s}); err != nil {
			t.Fatalf("save v+: %v", err)
		}
		cur.Schema = s
	}
}

func TestTheOntologyHistoryRouteListsEveryVersionAndPages(t *testing.T) {
	db, ts := historyBrain(t)
	saveThreeVersions(t, db)

	var all cortexdb.OntologyHistoryResponse
	if code := getStatus(t, ts.URL+"/api/ontology/history?schema=people", &all); code != http.StatusOK {
		t.Fatalf("history: %d", code)
	}
	if all.Total != 3 || len(all.Versions) != 3 || all.Versions[0].Version != 3 {
		t.Fatalf("three versions newest first expected: %+v", all)
	}
	if !all.Versions[0].Active || all.Versions[0].SavedAt.IsZero() {
		t.Errorf("the latest should be active and dated: %+v", all.Versions[0])
	}
	if s := all.Versions[0].Summary; s == nil || s.Changes == 0 {
		t.Errorf("a version should carry what it changed: %+v", all.Versions[0].Summary)
	}

	var page cortexdb.OntologyHistoryResponse
	getStatus(t, ts.URL+"/api/ontology/history?schema=people&limit=2&offset=2", &page)
	if len(page.Versions) != 1 || page.Versions[0].Version != 1 || page.HasMore {
		t.Errorf("the last page is v1 alone: %+v", page)
	}
	var first cortexdb.OntologyHistoryResponse
	getStatus(t, ts.URL+"/api/ontology/history?schema=people&limit=2", &first)
	if len(first.Versions) != 2 || !first.HasMore {
		t.Errorf("the first page is two and has more: %+v", first)
	}

	// A schema nobody saved is not there, which is not the brain failing.
	var missing map[string]string
	if code := getStatus(t, ts.URL+"/api/ontology/history?schema=nobody-saved-this", &missing); code != http.StatusNotFound ||
		!strings.Contains(missing["error"], "has no history") {
		t.Errorf("an unknown schema answered %d %v, want 404 saying it has no history", code, missing)
	}

	for _, q := range []string{"", "schema=people&limit=-1", "schema=people&offset=x"} {
		if code := getStatus(t, ts.URL+"/api/ontology/history?"+q, nil); code != http.StatusBadRequest {
			t.Errorf("?%s answered %d, want 400", q, code)
		}
	}
}

func TestTheOntologyDiffRouteComparesTwoStoredVersions(t *testing.T) {
	db, ts := historyBrain(t)
	saveThreeVersions(t, db)

	var d cortexdb.OntologyDiffResponse
	if code := getStatus(t, ts.URL+"/api/ontology/diff?schema=people&from=1&to=3", &d); code != http.StatusOK {
		t.Fatalf("diff: %d", code)
	}
	if len(d.Diff.Changes) != 2 {
		t.Errorf("v1 to v3 adds two properties: %+v", d.Diff.Changes)
	}
	var same cortexdb.OntologyDiffResponse
	raw := httpGet(t, ts.URL+"/api/ontology/diff?schema=people&from=3&to=3")
	if err := json.Unmarshal([]byte(raw), &same); err != nil || !strings.Contains(raw, `"changes":[]`) {
		t.Errorf("no change should be an empty list, not null: %s", raw)
	}
	for _, q := range []string{"schema=people", "schema=people&from=0", "schema=people&from=x", "from=1", "schema=people&from=1&to=-2"} {
		if code := getStatus(t, ts.URL+"/api/ontology/diff?"+q, nil); code != http.StatusBadRequest {
			t.Errorf("?%s answered %d, want 400", q, code)
		}
	}
	if code := getStatus(t, ts.URL+"/api/ontology/diff?schema=people&from=9", nil); code == http.StatusOK {
		t.Error("a version that was never stored was compared")
	}
}

func TestTheHistoryToolsAreOnTheViewsReadOnlyList(t *testing.T) {
	for _, name := range []string{"graph_history", "ontology_history", "ontology_diff"} {
		if !exploreAllowed(name) {
			t.Errorf("%s is not a tool the view may run", name)
		}
	}
}
