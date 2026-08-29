package liveview

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSource is a graph the test moves under the server's feet.
type fakeSource struct {
	mu     sync.Mutex
	nodes  []Node
	edges  []Edge
	reads  int
	failed error
}

func (f *fakeSource) set(nodes []Node, edges []Edge) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes, f.edges = nodes, edges
}

func (f *fakeSource) source() *Source {
	return &Source{
		Describe: "test brain",
		Read: func(context.Context) ([]Node, []Edge, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.reads++
			if f.failed != nil {
				return nil, nil, f.failed
			}
			return append([]Node(nil), f.nodes...), append([]Edge(nil), f.edges...), nil
		},
		Close: func() error { return nil },
	}
}

func startTestServer(t *testing.T, f *fakeSource, activity bool) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Port 0: never fight another test, or a real view the developer left open.
	sv, err := Start(ctx, f.source(), 0, 40*time.Millisecond, activity)
	if err != nil {
		t.Fatalf("start live server: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	return sv
}

func TestLiveServerServesPageAndGraph(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A", Type: "entity"}}, nil)
	sv := startTestServer(t, f, true)

	page := httpGet(t, sv.URL()+"/")
	for _, want := range []string{"ForceGraph3D", "UnrealBloomPass", `EventSource("api/stream")`} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}

	var payload Payload
	decodeGet(t, sv.URL()+"/api/graph", &payload)
	if len(payload.Nodes) != 1 || payload.Nodes[0].ID != "entity:a" {
		t.Errorf("payload nodes = %+v, want entity:a", payload.Nodes)
	}
	if payload.Source != "test brain" || !payload.Activity {
		t.Errorf("payload = %+v, want the test source with activity on", payload)
	}
}

// The listener must never reach past this machine: the page is the whole brain,
// unauthenticated.
func TestLiveServerBindsLoopbackOnly(t *testing.T) {
	f := &fakeSource{}
	sv := startTestServer(t, f, false)
	if !strings.HasPrefix(sv.URL(), "http://127.0.0.1:") {
		t.Errorf("listening on %s, want 127.0.0.1", sv.URL())
	}
}

// The whole point: a node written after the page opened arrives without a reload.
func TestLiveServerStreamsStructuralChange(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A"}}, nil)
	sv := startTestServer(t, f, false)

	frames, stop := openStream(t, sv.URL()+"/api/stream")
	defer stop()

	first := nextFrame(t, frames, "snapshot")
	var open Payload
	mustJSON(t, first.data, &open)
	if len(open.Nodes) != 1 {
		t.Fatalf("opening snapshot had %d nodes, want 1", len(open.Nodes))
	}
	if open.Activity {
		t.Error("a command-line view must not claim to be watching calls")
	}

	f.set([]Node{{ID: "entity:a", Label: "A"}, {ID: "entity:b", Label: "B"}},
		[]Edge{{Source: "entity:a", Target: "entity:b", Label: "knows"}})

	frame := nextFrame(t, frames, "delta")
	var d Delta
	mustJSON(t, frame.data, &d)
	if len(d.AddedNodes) != 1 || d.AddedNodes[0].ID != "entity:b" {
		t.Errorf("delta added %+v, want entity:b", d.AddedNodes)
	}
	if len(d.AddedEdges) != 1 {
		t.Errorf("delta added edges %+v, want the knows edge", d.AddedEdges)
	}
	if d.Nodes != 2 || d.Edges != 1 {
		t.Errorf("delta totals = %d/%d, want 2/1", d.Nodes, d.Edges)
	}
}

// A query changes nothing in the graph, so a poller can never report it. This
// is the path that makes queries visible at all.
func TestLiveServerStreamsActivity(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "CortexDB"}}, nil)
	sv := startTestServer(t, f, true)

	frames, stop := openStream(t, sv.URL()+"/api/stream")
	defer stop()
	nextFrame(t, frames, "snapshot")

	ev, ok := ClassifyToolCall("knowledge_memory_recall", json.RawMessage(`{"query":"CortexDB"}`), false)
	if !ok {
		t.Fatal("recall produced no event")
	}
	sv.Observe(ev)

	frame := nextFrame(t, frames, "activity")
	var got Event
	mustJSON(t, frame.data, &got)
	if got.Kind != KindQuery || got.Tool != "knowledge_memory_recall" {
		t.Errorf("activity = %+v, want a recall query", got)
	}
	if got.Seq == 0 {
		t.Error("activity event was not sequenced")
	}
	if !containsString(got.Terms, "CortexDB") {
		t.Errorf("terms %v lost the query", got.Terms)
	}
}

// A page that opens after some activity should still see what it missed.
func TestLiveServerReplaysRecentActivity(t *testing.T) {
	f := &fakeSource{}
	sv := startTestServer(t, f, true)
	ev, _ := ClassifyToolCall("memory_save", json.RawMessage(`{"title":"earlier"}`), false)
	sv.Observe(ev)

	frames, stop := openStream(t, sv.URL()+"/api/stream")
	defer stop()
	var open Payload
	mustJSON(t, nextFrame(t, frames, "snapshot").data, &open)
	if len(open.Events) != 1 || open.Events[0].Tool != "memory_save" {
		t.Errorf("backlog = %+v, want the earlier save", open.Events)
	}
}

// A brain that is briefly unreadable must not take the view down with it.
func TestLiveServerSurvivesAFailedRead(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a"}}, nil)
	sv := startTestServer(t, f, false)

	f.mu.Lock()
	f.failed = context.DeadlineExceeded
	f.mu.Unlock()
	time.Sleep(150 * time.Millisecond)

	f.mu.Lock()
	f.failed = nil
	f.mu.Unlock()
	f.set([]Node{{ID: "entity:a"}, {ID: "entity:b"}}, nil)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(sv.hub.snapshot().Nodes) == 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the poller never recovered after a failed read")
}

// A tab that stopped reading must not be able to stall the MCP server that is
// also answering the agent.
func TestLiveHubDropsUpdatesForAStalledSubscriber(t *testing.T) {
	h := NewHub()
	ch, _, _ := h.subscribe()
	defer h.unsubscribe(ch)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			h.publishEvent(Event{Kind: KindQuery, Tool: "search_text"})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocked on a subscriber that never read")
	}
	if h.subscriberCount() != 1 {
		t.Errorf("subscriber count = %d, want the stalled reader still registered", h.subscriberCount())
	}
}

/* ---- helpers ---- */

type sseFrame struct{ event, data string }

func openStream(t *testing.T, url string) (<-chan sseFrame, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("build stream request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		cancel()
		t.Fatalf("stream Content-Type = %q, want text/event-stream", ct)
	}
	out := make(chan sseFrame, 32)
	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		cur := sseFrame{}
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "" && cur.event != "":
				select {
				case out <- cur:
				case <-ctx.Done():
					return
				}
				cur = sseFrame{}
			}
		}
	}()
	return out, cancel
}

// nextFrame waits for a frame of the wanted kind, skipping heartbeats.
func nextFrame(t *testing.T, frames <-chan sseFrame, want string) sseFrame {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("stream closed before a %q frame", want)
			}
			if f.event == want {
				return f
			}
		case <-deadline:
			t.Fatalf("timed out waiting for a %q frame", want)
		}
	}
}

func httpGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	buf := new(strings.Builder)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		buf.WriteString(sc.Text())
		buf.WriteByte('\n')
	}
	return buf.String()
}

func decodeGet(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func mustJSON(t *testing.T, data string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(data), v); err != nil {
		t.Fatalf("decode frame %q: %v", data, err)
	}
}

// Closing a view must not wait on the streams it is closing.
//
// Shutdown stops the listener at once and then waits for handlers, and every
// open page holds an SSE stream that ends only when its request context does.
// So Shutdown always ran to its deadline — seconds of nothing on every close,
// with the caller's lock held, and the timeout swallowed so it looked clean.
func TestCloseDoesNotWaitOnAnOpenStream(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "a", Label: "A"}}, nil)
	sv, err := Start(context.Background(), f.source(), 0, time.Second, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	url := sv.URL()

	frames, stop := openStream(t, url+"/api/stream")
	defer stop()
	nextFrame(t, frames, "snapshot")

	start := time.Now()
	if cerr := sv.Close(); cerr != nil {
		t.Fatalf("close: %v", cerr)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Close took %v with one stream open — it is waiting for streams that never end", took)
	}
	if _, err := http.Get(url + "/api/graph"); err == nil {
		t.Error("the listener is still accepting after Close")
	}
}

// The page is embedded behind reverse proxies — an application putting an
// authenticated front door on a view that has none of its own. An absolute
// stream path would leave the mount point and land on whatever the host serves
// at /api, so the page must ask for the stream relative to itself.
func TestThePageAsksForItsStreamRelatively(t *testing.T) {
	if strings.Contains(pageHTML, `EventSource("/api/`) {
		t.Error("the page opens its stream at an absolute path, so it cannot be mounted under a prefix")
	}
	if !strings.Contains(pageHTML, `EventSource("api/stream")`) {
		t.Error("the page does not open a relative stream")
	}
}
