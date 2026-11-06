package liveview

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func rawGet(t *testing.T, url string, gz bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if gz {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// The graph is the largest thing the view sends — three quarters of a
// megabyte on the shared brain — and it goes compressed to a browser that
// asks, and plain to one that does not.
func TestTheGraphIsCompressedForABrowserThatAsks(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: strings.Repeat("A", 4000), Type: "entity"}}, nil)
	sv := startTestServer(t, f, false)

	resp := rawGet(t, sv.URL()+"/api/graph", true)
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	var payload Payload
	if err := json.NewDecoder(zr).Decode(&payload); err != nil || len(payload.Nodes) != 1 {
		t.Fatalf("decoded %d nodes, err %v", len(payload.Nodes), err)
	}

	plain := rawGet(t, sv.URL()+"/api/graph", false)
	if got := plain.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("a client that did not ask got Content-Encoding %q", got)
	}
}

// The event stream is flushed event by event; a compressor would sit on them.
func TestTheStreamIsNotCompressed(t *testing.T) {
	f := &fakeSource{}
	sv := startTestServer(t, f, false)
	resp := rawGet(t, sv.URL()+"/api/stream", true)
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("stream Content-Encoding = %q, want none", got)
	}
}

// feedSource is a graph whose change feed the test controls.
type feedSource struct {
	fakeSource
	mu      sync.Mutex
	head    int64
	feedErr error
	asks    int
}

func (f *feedSource) bump() {
	f.mu.Lock()
	f.head++
	f.mu.Unlock()
}

func (f *feedSource) withFeed() *Source {
	src := f.fakeSource.source()
	src.Changes = func(_ context.Context, after int64) (int64, bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.asks++
		if f.feedErr != nil {
			return after, false, f.feedErr
		}
		if after < 0 {
			return f.head, false, nil
		}
		return f.head, f.head > after, nil
	}
	return src
}

func (f *feedSource) readCount() int {
	f.fakeSource.mu.Lock()
	defer f.fakeSource.mu.Unlock()
	return f.fakeSource.reads
}

func startFeedServer(t *testing.T, f *feedSource) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := Start(ctx, f.withFeed(), 0, 10*time.Millisecond, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
}

// With a feed that reports nothing new, the graph is read once at start and
// not again: the read is what cost the shared brain a third of a core.
func TestAQuietFeedMeansNoReread(t *testing.T) {
	f := &feedSource{}
	startFeedServer(t, f)
	time.Sleep(200 * time.Millisecond)
	if n := f.readCount(); n != 1 {
		t.Fatalf("graph read %d times over 20 quiet ticks, want 1", n)
	}
	f.bump()
	deadline := time.Now().Add(time.Second)
	for f.readCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := f.readCount(); n != 2 {
		t.Fatalf("after one change the graph was read %d times, want 2", n)
	}
}

// A brain without the feed is read on every tick, as before there was one.
func TestNoFeedFallsBackToReadingEveryTick(t *testing.T) {
	f := &feedSource{feedErr: io.ErrUnexpectedEOF}
	startFeedServer(t, f)
	time.Sleep(200 * time.Millisecond)
	if n := f.readCount(); n < 5 {
		t.Fatalf("graph read %d times over 20 ticks with no feed, want one a tick", n)
	}
}
