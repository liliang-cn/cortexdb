package liveview

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestALeftOutPageIs404AndTheOthersStillServeTheIndex(t *testing.T) {
	ts := serveApp(t, theApp, "", WithoutPages(PageRecalls, PageLibrary))
	for path, want := range map[string]int{
		"/":         http.StatusOK,
		"/entity":   http.StatusOK,
		"/ontology": http.StatusOK,
		"/recalls":  http.StatusNotFound,
		"/library":  http.StatusNotFound,
	} {
		resp, _ := fetch(t, ts.URL+path, nil)
		if resp.StatusCode != want {
			t.Errorf("GET %s: status %d, want %d", path, resp.StatusCode, want)
		}
	}
}

// /ontology draws the Ontology page and the Versions page as two tabs, so it
// answers while either is left in; /ontology-history, which the app redirects
// to the Versions tab, answers only while the Versions page is.
func TestTheOntologyRouteStaysWhileEitherOfItsTabsDoes(t *testing.T) {
	for _, c := range []struct {
		hidden             []Page
		ontology, versions int
	}{
		{nil, http.StatusOK, http.StatusOK},
		{[]Page{PageOntology}, http.StatusOK, http.StatusOK},
		{[]Page{PageVersions}, http.StatusOK, http.StatusNotFound},
		{[]Page{PageOntology, PageVersions}, http.StatusNotFound, http.StatusNotFound},
	} {
		ts := serveApp(t, theApp, "", WithoutPages(c.hidden...))
		for path, want := range map[string]int{"/ontology": c.ontology, "/ontology-history": c.versions} {
			if resp, _ := fetch(t, ts.URL+path, nil); resp.StatusCode != want {
				t.Errorf("without %v, GET %s: status %d, want %d", c.hidden, path, resp.StatusCode, want)
			}
		}
	}
}

// The library is a view of the graph page at /, which cannot be left out, so
// / answers either way; /library, which the app redirects to the view,
// answers only while the library is shown, and so does its API.
func TestTheGraphRouteStaysAndTheLibraryRouteGoesWithTheLibrary(t *testing.T) {
	for _, c := range []struct {
		hidden                 []Page
		graph, library, apiLib int
	}{
		{nil, http.StatusOK, http.StatusOK, http.StatusOK},
		{[]Page{PageLibrary}, http.StatusOK, http.StatusNotFound, http.StatusNotFound},
	} {
		ts := serveApp(t, theApp, "", WithoutPages(c.hidden...))
		for path, want := range map[string]int{"/": c.graph, "/?view=library": c.graph, "/library": c.library, "/api/library": c.apiLib, "/api/graph": http.StatusOK} {
			if resp, _ := fetch(t, ts.URL+path, nil); resp.StatusCode != want {
				t.Errorf("without %v, GET %s: status %d, want %d", c.hidden, path, resp.StatusCode, want)
			}
		}
	}
}

func TestTheIndexNamesTheLeftOutPagesBesideTheBaseTag(t *testing.T) {
	ts := serveApp(t, theApp, "/kg", WithoutPages(PageRecalls, PageOntology, PageRecalls))
	_, body := fetch(t, ts.URL+"/kg/library", nil)
	want := `<base href="/kg/" data-cortexdb-base><meta name="cortexdb-hidden-pages" content="ontology recalls">`
	if !strings.Contains(body, want) {
		t.Fatalf("index does not carry %s:\n%s", want, body)
	}

	plain := serveApp(t, theApp, "")
	if _, body := fetch(t, plain.URL+"/", nil); strings.Contains(body, "cortexdb-hidden-pages") {
		t.Fatalf("a view that leaves nothing out names hidden pages:\n%s", body)
	}
}

func TestTheAPIsOnlyALeftOutPageReadsAre404(t *testing.T) {
	ts := serveApp(t, theApp, "", WithoutPages(PageRecalls, PageLibrary, PageVersions))
	for _, path := range []string{
		"/api/recalls", "/api/recalls/get?id=1", "/api/recalls/stats", "/api/recalls/heat",
		"/api/recalls/feedback", "/api/recalls/golden",
		"/api/library", "/api/library/card?id=x", "/api/library/book?id=x",
		"/api/ontology/history?schema=s", "/api/ontology/diff?schema=s&from=1&to=2",
	} {
		resp, body := fetch(t, ts.URL+path, nil)
		if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "leaves out") {
			t.Errorf("GET %s: %d %s, want the left-out 404", path, resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s: Content-Type %q, want JSON", path, ct)
		}
	}
	if resp, _ := fetch(t, ts.URL+"/api/graph", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/graph: status %d, want 200", resp.StatusCode)
	}
}

// /api/ontology serves the Ontology page and the Versions page's list of
// schemas, so it stays until both are left out.
func TestAnAPITwoPagesReadGoesOnlyWhenBothAreLeftOut(t *testing.T) {
	leftOut := func(opts ...Option) bool {
		ts := serveApp(t, theApp, "", opts...)
		_, body := fetch(t, ts.URL+"/api/ontology", nil)
		return strings.Contains(body, "leaves out")
	}
	if leftOut(WithoutPages(PageOntology)) {
		t.Error("/api/ontology went with the Ontology page while Versions still reads it")
	}
	if leftOut(WithoutPages(PageVersions)) {
		t.Error("/api/ontology went with the Versions page while Ontology still reads it")
	}
	if !leftOut(WithoutPages(PageOntology, PageVersions)) {
		t.Error("/api/ontology is still served with both pages that read it left out")
	}
}

// The graph's inspector and the entity page read a node's changes from
// /api/changes, so leaving the Changes page out keeps it.
func TestLeavingOutChangesKeepsTheChangeFeed(t *testing.T) {
	ts := serveApp(t, theApp, "", WithoutPages(PageChanges, PageActivity))
	for _, path := range []string{"/changes", "/activity"} {
		if resp, _ := fetch(t, ts.URL+path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
	if _, body := fetch(t, ts.URL+"/api/changes", nil); strings.Contains(body, "leaves out") {
		t.Errorf("/api/changes was left out with the Changes page: %s", body)
	}
}

func TestNewRefusesAPageThatCannotBeLeftOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, p := range []Page{"", "graph", "entity", "Recalls", "/recalls", "versions"} {
		sv, err := New(ctx, (&fakeSource{}).source(), time.Minute, false, WithoutPages(p))
		if err == nil {
			_ = sv.Close()
			t.Errorf("WithoutPages(%q): New accepted it", p)
		}
	}
}

func TestHiddenPagesAreReportedOnceAndSorted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sv, err := New(ctx, (&fakeSource{}).source(), time.Minute, false,
		WithoutPages(PageVersions, PageLibrary), nil, WithoutPages(PageLibrary, PageActivity))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sv.Close()
	want := []Page{PageActivity, PageLibrary, PageVersions}
	if got := sv.HiddenPages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("HiddenPages() = %v, want %v", got, want)
	}
}
