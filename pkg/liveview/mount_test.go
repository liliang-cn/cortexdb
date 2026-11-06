package liveview

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// A process that already has an HTTP server should not need a second listener
// to show the graph. New hands back the routes; the process mounts them.
func TestTheViewCanBeMountedUnderAPrefixOfSomebodyElsesMux(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A", Type: "entity"}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	sv, err := New(ctx, f.source(), 40*time.Millisecond, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })

	// The pages link and fetch by relative path, so a prefix only works when
	// it ends in a slash and is stripped before the view sees the request.
	mux := http.NewServeMux()
	mux.Handle("/graph/", http.StripPrefix("/graph", sv.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	page, err := http.Get(ts.URL + "/graph/")
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	defer page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("page under a prefix: %d", page.StatusCode)
	}
	// The app resolves every URL it uses against its <base>, so naming the
	// mount there is the whole of supporting one.
	index, _ := io.ReadAll(page.Body)
	if !strings.Contains(string(index), `<base href="/graph/" data-cortexdb-base>`) {
		t.Fatalf("the index is not based at the mount prefix: %s", index)
	}
	if cc := page.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("index Cache-Control = %q", cc)
	}

	// A build file is reachable under the prefix too, and unknown API paths
	// stay 404s there.
	sv.ui = fstest.MapFS{"assets/app-1.js": {Data: []byte("1")}, "index.html": {Data: []byte("x")}}
	asset, err := http.Get(ts.URL + "/graph/assets/app-1.js")
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	asset.Body.Close()
	if asset.StatusCode != http.StatusOK || !strings.Contains(asset.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset under a prefix: %d %q", asset.StatusCode, asset.Header.Get("Cache-Control"))
	}
	missing, err := http.Get(ts.URL + "/graph/api/nope")
	if err != nil {
		t.Fatalf("api 404: %v", err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown API under a prefix: %d", missing.StatusCode)
	}

	api, err := http.Get(ts.URL + "/graph/api/graph")
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	defer api.Body.Close()
	if api.StatusCode != http.StatusOK {
		t.Fatalf("api under a prefix: %d", api.StatusCode)
	}
	var snap struct {
		Nodes []Node `json:"nodes"`
	}
	if err := json.NewDecoder(api.Body).Decode(&snap); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if len(snap.Nodes) != 1 || snap.Nodes[0].ID != "entity:a" {
		t.Fatalf("the mounted view served somebody else's graph: %+v", snap.Nodes)
	}
}

func TestAMountedViewHasNoURLAndClosesWithoutAListener(t *testing.T) {
	f := &fakeSource{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := New(ctx, f.source(), time.Second, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sv.URL() != "" {
		t.Fatalf("a view nobody listened for claims a URL: %q", sv.URL())
	}
	// Close must not touch a server that was never started.
	if err := sv.Close(); err != nil {
		t.Fatalf("close without a listener: %v", err)
	}
}

func TestStartIsStillNewPlusAListener(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A", Type: "entity"}}, nil)
	sv := startTestServer(t, f, false)
	if !strings.HasPrefix(sv.URL(), "http://127.0.0.1:") {
		t.Fatalf("Start no longer listens on loopback: %q", sv.URL())
	}
	resp, err := http.Get(sv.URL() + "/api/graph")
	if err != nil {
		t.Fatalf("Start's own listener: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Start's own listener answered %d", resp.StatusCode)
	}
}
