package liveview

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// theApp is a tiny built app of the shape the real build has, so these tests
// do not depend on what `npm run build` last left in dist.
var theApp = fstest.MapFS{
	"index.html": {Data: []byte(`<!doctype html><html><head><base href="/" data-cortexdb-base><title>app</title>` +
		`<script type="module" src="./assets/index-abc123.js"></script></head><body></body></html>`)},
	"assets/index-abc123.js":  {Data: []byte(strings.Repeat("console.log('the app');\n", 200))},
	"assets/index-abc123.css": {Data: []byte("body{margin:0}")},
	"assets/logo-9f8e7d.png":  {Data: []byte("\x89PNG\r\n\x1a\nnot really")},
	"assets/.keep":            {Data: []byte("")},
}

// serveApp serves a view whose app is files, at the root or under prefix
// ("" or "/graph"), built with opts.
func serveApp(t *testing.T, files fstest.MapFS, prefix string, opts ...Option) *httptest.Server {
	t.Helper()
	f := &fakeSource{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := New(ctx, f.source(), 40*time.Millisecond, false, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	sv.ui = files
	if prefix == "" {
		ts := httptest.NewServer(sv.Handler())
		t.Cleanup(ts.Close)
		return ts
	}
	mux := http.NewServeMux()
	mux.Handle(prefix+"/", http.StripPrefix(prefix, sv.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func fetch(t *testing.T, url string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestTheIndexIsServedOnEveryClientRouteAndNeverCached(t *testing.T) {
	ts := serveApp(t, theApp, "")
	for _, route := range []string{"/", "/changes", "/entity", "/activity", "/recalls", "/library", "/ontology", "/ontology-history", "/analysis"} {
		resp, body := fetch(t, ts.URL+route, nil)
		if resp.StatusCode != 200 || !strings.Contains(body, "<title>app</title>") {
			t.Errorf("%s: %d %q", route, resp.StatusCode, body)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store: the index names the hashed bundle", route, cc)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type %q", route, ct)
		}
		if !strings.Contains(body, `<base href="/" data-cortexdb-base>`) {
			t.Errorf("%s at the root should be based at /: %q", route, body)
		}
	}
}

func TestTheIndexIsBasedAtTheMountPrefix(t *testing.T) {
	ts := serveApp(t, theApp, "/graph")
	for _, route := range []string{"/", "/changes", "/entity", "/activity", "/recalls", "/library", "/ontology", "/ontology-history", "/analysis"} {
		_, body := fetch(t, ts.URL+"/graph"+route, nil)
		if !strings.Contains(body, `<base href="/graph/" data-cortexdb-base>`) {
			t.Errorf("/graph%s: base not rewritten to the mount prefix: %q", route, body)
		}
	}
	// A deeper mount.
	ts = serveApp(t, theApp, "/a/b")
	_, body := fetch(t, ts.URL+"/a/b/changes", nil)
	if !strings.Contains(body, `<base href="/a/b/" data-cortexdb-base>`) {
		t.Errorf("deep prefix: %q", body)
	}
}

func TestOnlyTheAppsOwnRoutesGetTheIndexAndUnknownPathsAre404(t *testing.T) {
	ts := serveApp(t, theApp, "")
	for _, path := range []string{"/nope", "/changes/extra", "/index.html", "/ontologyz", "/recalls/extra", "/library/"} {
		resp, body := fetch(t, ts.URL+path, nil)
		if resp.StatusCode != 404 || strings.Contains(body, "data-cortexdb-base") {
			t.Errorf("%s answered %d with %q", path, resp.StatusCode, body)
		}
	}
}

func TestAnUnknownAPIPathIs404AndNeverTheIndex(t *testing.T) {
	ts := serveApp(t, theApp, "")
	for _, path := range []string{"/api/nope", "/api/", "/api/changes/extra", "/api"} {
		resp, body := fetch(t, ts.URL+path, nil)
		if path == "/api" {
			// Not under /api/: the app's router has no such route either.
			if resp.StatusCode != 404 {
				t.Errorf("%s answered %d", path, resp.StatusCode)
			}
			continue
		}
		if resp.StatusCode != 404 || strings.Contains(body, "<html") || strings.Contains(body, "data-cortexdb-base") {
			t.Errorf("%s answered %d with %q", path, resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: Content-Type %q, want JSON so a client's parse fails on the error and not on markup", path, ct)
		}
	}
	// The real routes still answer.
	if resp, _ := fetch(t, ts.URL+"/api/graph", nil); resp.StatusCode != 200 {
		t.Errorf("/api/graph answered %d", resp.StatusCode)
	}
}

func TestHashedAssetsAreImmutableTypedAndCompressedOnce(t *testing.T) {
	ts := serveApp(t, theApp, "")
	for path, ctype := range map[string]string{
		"/assets/index-abc123.js":  "text/javascript",
		"/assets/index-abc123.css": "text/css",
		"/assets/logo-9f8e7d.png":  "image/png",
	} {
		resp, body := fetch(t, ts.URL+path, nil)
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), ctype) {
			t.Errorf("%s: %d %q", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
			t.Errorf("%s: Cache-Control %q", path, cc)
		}
		if resp.Header.Get("Content-Encoding") != "" || body == "" {
			t.Errorf("%s: a client that did not ask for gzip got %q", path, resp.Header.Get("Content-Encoding"))
		}
	}

	resp, _ := fetch(t, ts.URL+"/assets/index-abc123.js", map[string]string{"Accept-Encoding": "gzip"})
	if resp.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("encoding %q vary %q", resp.Header.Get("Content-Encoding"), resp.Header.Get("Vary"))
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/assets/index-abc123.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	raw, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Body.Close()
	zr, err := gzip.NewReader(raw.Body)
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	plain, _ := io.ReadAll(zr)
	if string(plain) != string(theApp["assets/index-abc123.js"].Data) {
		t.Errorf("the compressed asset is not the asset")
	}
	// An image arrives compressed already; gzip would only cost.
	if resp, _ := fetch(t, ts.URL+"/assets/logo-9f8e7d.png", map[string]string{"Accept-Encoding": "gzip"}); resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("a png was gzipped")
	}
}

func TestAssetsThatAreNotBuildFilesAre404(t *testing.T) {
	ts := serveApp(t, theApp, "")
	for _, path := range []string{"/assets/missing.js", "/assets/.keep", "/assets/", "/assets/../index.html"} {
		if resp, _ := fetch(t, ts.URL+path, nil); resp.StatusCode != 404 {
			// net/http cleans some of these to a redirect first; a redirect is
			// not the file either.
			if resp.StatusCode/100 != 3 {
				t.Errorf("%s answered %d", path, resp.StatusCode)
			}
		}
	}
}

func TestAnAppThatWasNeverBuiltSaysSoInsteadOfServingNothing(t *testing.T) {
	ts := serveApp(t, fstest.MapFS{}, "")
	resp, body := fetch(t, ts.URL+"/", nil)
	if resp.StatusCode != 404 || !strings.Contains(body, "not built") {
		t.Errorf("%d %q", resp.StatusCode, body)
	}
}

// The index the build actually produced must carry a tag the rewrite finds,
// or a view mounted under a prefix loads its bundle and its API from the root.
func TestTheEmbeddedIndexCarriesTheBaseTag(t *testing.T) {
	index, err := embeddedUI().Open("index.html")
	if err != nil {
		t.Fatalf("no embedded index: %v", err)
	}
	defer index.Close()
	body, _ := io.ReadAll(index)
	if !baseTag.Match(body) {
		t.Fatalf("the embedded index has no <base ... data-cortexdb-base> tag")
	}
	if got := string(rebase(body, "/graph", nil)); !strings.Contains(got, `<base href="/graph/" data-cortexdb-base>`) {
		t.Errorf("the embedded index was not rebased onto /graph")
	}
}

// The build tool owns the tag's spelling. Vite writes it back self-closed, and
// the rewrite must find it however it is spelled.
func TestTheBaseTagIsRewrittenHoweverTheBuildSpellsIt(t *testing.T) {
	for _, tag := range []string{
		`<base href="/" data-cortexdb-base>`,
		`<base href="/" data-cortexdb-base />`,
		`<base data-cortexdb-base href="/">`,
		`<base href="./" data-cortexdb-base="">`,
	} {
		app := fstest.MapFS{"index.html": {Data: []byte(`<!doctype html><html><head>` + tag + `<title>app</title></head></html>`)}}
		ts := serveApp(t, app, "/graph")
		_, body := fetch(t, ts.URL+"/graph/changes", nil)
		if !strings.Contains(body, `<base href="/graph/" data-cortexdb-base><title>`) {
			t.Errorf("%s: not rebased: %q", tag, body)
		}
	}
}
