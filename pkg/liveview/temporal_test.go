package liveview

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The whole point: the graph as it was, not as it is.
//
// The store is built here rather than skipped over, because the temporal
// columns only exist on a brain created by a release that has them, and a test
// that skipped on an old file would report success on the machines where this
// matters least. cortexdb.Open runs the migration, so a fresh file is a new
// enough brain by construction.
func TestAnAsOfReadReturnsTheGraphAsItWas(t *testing.T) {
	db, ctx := temporalStore(t)

	// Two instants the store itself hands out, so nothing here sleeps and
	// nothing depends on the wall clock's resolution: every write after Now()
	// is stamped strictly later.
	before := db.Graph().Now()
	if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{
		ID: "entity:late", Vector: []float32{0, 0, 1, 0}, NodeType: "Entity", Content: "arrived later",
		Properties: graded(cortexdb.GradeVerified, "", ""),
	}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
		ID: "edge:late", FromNodeID: "entity:early", ToNodeID: "entity:late",
		EdgeType: "mentions", Weight: 1,
		Properties: graded(cortexdb.GradeAsserted, "", ""),
	}); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	read := localReadAsOf(db)
	past, pastEdges, err := read(ctx, before)
	if err != nil {
		t.Fatalf("read as of: %v", err)
	}
	if ids := nodeIDs(past); ids != "entity:early" {
		t.Errorf("the graph as of %s = %q, want only the node that existed then", before, ids)
	}
	if len(pastEdges) != 0 {
		t.Errorf("an edge written after the instant showed up in the past: %+v", pastEdges)
	}

	// And now still answers the live question, unchanged.
	now, nowEdges, err := LoadLocal(ctx, db.SQL())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if ids := nodeIDs(now); ids != "entity:early,entity:late" {
		t.Errorf("the live graph = %q, want both nodes", ids)
	}
	if len(nowEdges) != 1 {
		t.Fatalf("the live graph has %d edges, want the one just written", len(nowEdges))
	}

	// A grade is still read at an instant in the past. The past read goes
	// through a union of the live table and its history, and a query that lost
	// the contract on the way would leave the pinned scene uncoloured in
	// exactly the mode a reader pinned it to look at.
	early := past[0]
	if early.Grade != cortexdb.GradeHeld {
		t.Errorf("the past node's grade = %q, want the one it carried then", early.Grade)
	}
}

// A retracted record is the case the history tables exist for: the row leaves
// the live table entirely, and an as-of read before the deletion must still
// find it. Anything less and "the graph as it was" means "the graph as it is,
// minus what arrived since".
func TestAnAsOfReadStillSeesWhatWasSinceDeleted(t *testing.T) {
	db, ctx := temporalStore(t)
	before := db.Graph().Now()
	if err := db.Graph().DeleteNode(ctx, "entity:early"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	past, _, err := localReadAsOf(db)(ctx, before)
	if err != nil {
		t.Fatalf("read as of: %v", err)
	}
	if ids := nodeIDs(past); ids != "entity:early" {
		t.Errorf("the graph before the deletion = %q, want the node that was deleted", ids)
	}
	now, _, err := LoadLocal(ctx, db.SQL())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if len(now) != 0 {
		t.Errorf("the live graph still has %+v after the deletion", now)
	}
}

// The instant is not a fallback. Everywhere else on this page a bad parameter
// falls back — a page is not worth a 400 — but "now" is the one wrong answer
// this one must never give, because the whole point of the pin is that the
// reader is told they are looking at the past.
func TestParseAsOfRefusesRatherThanFallingBackToNow(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, raw := range []string{
		fmt.Sprintf("%d", when.UnixMilli()),
		when.Format(time.RFC3339),
	} {
		got, err := ParseAsOf(raw)
		if err != nil {
			t.Fatalf("ParseAsOf(%q): %v", raw, err)
		}
		if !got.Equal(when) {
			t.Errorf("ParseAsOf(%q) = %s, want %s", raw, got, when)
		}
	}
	if got, err := ParseAsOf(""); err != nil || !got.IsZero() {
		t.Errorf("an absent parameter must mean now: got %s, %v", got, err)
	}
	for _, bad := range []string{"yesterday", "-1", "0", "2026-13-45"} {
		if _, err := ParseAsOf(bad); err == nil {
			t.Errorf("ParseAsOf(%q) was accepted; a bad instant must not silently become now", bad)
		}
	}
}

// A source that cannot be asked about the past says so, and the view keeps
// showing the present rather than an empty graph under a banner claiming to be
// last Tuesday.
func TestAPinIsRefusedInWordsWhenTheSourceHasNoPast(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A"}}, nil)
	sv := startTestServer(t, f, false)

	var live Payload
	decodeGet(t, sv.URL()+"/api/graph", &live)
	if live.Temporal {
		t.Error("a source with no ReadAsOf hook claimed on the wire that it has a past")
	}

	var pinned Payload
	decodeGet(t, sv.URL()+"/api/graph?as_of=1700000000000", &pinned)
	if pinned.Pinned {
		t.Fatal("a source that cannot answer about the past reported the view as pinned")
	}
	if pinned.PinReason == "" {
		t.Error("the pin was refused and the page is not told why")
	}
	if len(pinned.Nodes) != 1 {
		t.Errorf("a refused pin left the page with %+v instead of the live graph", pinned.Nodes)
	}
	if !strings.Contains(pageHTML, "still showing now — ") {
		t.Error("the page does not say that a refused pin left it on the present")
	}

	// An unparseable instant is refused the same way, and never as "now".
	var bad Payload
	decodeGet(t, sv.URL()+"/api/graph?as_of=yesterday", &bad)
	if bad.Pinned || bad.PinReason == "" {
		t.Errorf("an unparseable instant came back as %+v", bad)
	}
}

// The two halves of the promise: the payload is the past, and it is marked as
// the past. A graph quietly showing last week is worse than one that cannot
// show it at all, so Pinned and AsOf travel with the nodes.
func TestAPinnedPayloadIsMarkedAsThePast(t *testing.T) {
	db, ctx := temporalStore(t)
	before := millisecondApart(db)
	if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{
		ID: "entity:late", Vector: []float32{0, 0, 1, 0}, NodeType: "Entity", Content: "later",
	}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	sv := serveSource(t, SourceFor(db, "temporal"))

	var live Payload
	decodeGet(t, sv.URL()+"/api/graph", &live)
	if !live.Temporal {
		t.Fatal("a local source did not declare that it can be asked about the past")
	}
	if live.Pinned || live.AsOf != 0 {
		t.Errorf("the live payload claims to be pinned: %+v", live)
	}

	var past Payload
	decodeGet(t, sv.URL()+fmt.Sprintf("/api/graph?as_of=%d", before.UnixMilli()), &past)
	if !past.Pinned {
		t.Fatal("a payload from the past is not marked as the past")
	}
	if past.AsOf != before.UnixMilli() {
		t.Errorf("as_of = %d, want %d", past.AsOf, before.UnixMilli())
	}
	if ids := nodeIDs(past.Nodes); ids != "entity:early" {
		t.Errorf("the pinned payload = %q, want the graph as it stood then", ids)
	}
	// A pinned frame must not also claim the ticker is live: activity is
	// reported as calls are handled, which is a fact about now however far back
	// the scene is pinned.
	if past.Activity {
		t.Error("a pinned payload claimed to be watching calls")
	}

	// And the page has somewhere to say it, unfoldably.
	for _, want := range []string{
		`<div id="past">`,
		"Showing the graph as it stood at",
		"this is the past, and it is not updating",
		`t.textContent = !connected ? "reconnecting" :`,
	} {
		if !strings.Contains(pageHTML, want) {
			t.Errorf("the page never says it is showing the past: missing %q", want)
		}
	}
	// The banner is not one of the five foldable panels; a reader must not be
	// able to put it away while it is still true.
	if strings.Count(pageHTML, `class="fold"`) != 5 {
		t.Error("the past banner became a foldable panel, so it can be hidden while it is still true")
	}
}

// Live updates stop while pinned, and stop on the server rather than on trust:
// a pinned stream never subscribes to the poller, so there is no delta for the
// page to have to ignore. Per connection, not per server — one reader looking
// at last Tuesday must not freeze the page of the reader beside them.
func TestAPinnedStreamGetsNoUpdatesAndALiveOneStillDoes(t *testing.T) {
	db, ctx := temporalStore(t)
	before := millisecondApart(db)
	src := SourceFor(db, "temporal")
	svCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	sv, err := Start(svCtx, src, 0, 30*time.Millisecond, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })

	pinnedFrames, stopPinned := openStream(t, sv.URL()+fmt.Sprintf("/api/stream?as_of=%d", before.UnixMilli()))
	defer stopPinned()
	liveFrames, stopLive := openStream(t, sv.URL()+"/api/stream")
	defer stopLive()

	var open Payload
	mustJSON(t, nextFrame(t, pinnedFrames, "snapshot").data, &open)
	if !open.Pinned {
		t.Fatalf("the pinned stream's opening frame is not marked as the past: %+v", open)
	}
	if ids := nodeIDs(open.Nodes); ids != "entity:early" {
		t.Fatalf("the pinned stream opened on %q", ids)
	}
	var liveOpen Payload
	mustJSON(t, nextFrame(t, liveFrames, "snapshot").data, &liveOpen)
	if liveOpen.Pinned {
		t.Fatal("the live stream came back pinned")
	}

	// Write something. The live page must be told; the pinned one must not.
	if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{
		ID: "entity:late", Vector: []float32{0, 0, 1, 0}, NodeType: "Entity", Content: "later",
	}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	var d Delta
	mustJSON(t, nextFrame(t, liveFrames, "delta").data, &d)
	if len(d.AddedNodes) != 1 || d.AddedNodes[0].ID != "entity:late" {
		t.Fatalf("the live stream was told %+v", d.AddedNodes)
	}

	// The delta has been delivered to the live subscriber, so the poller has
	// certainly run. Anything the pinned stream was going to be sent it would
	// have been sent by now.
	select {
	case f, ok := <-pinnedFrames:
		if ok && f.event != "ping" {
			t.Fatalf("the pinned stream received a %q frame; it must receive nothing but heartbeats", f.event)
		}
	case <-time.After(150 * time.Millisecond):
	}

	// And the page refuses to apply one even if it somehow arrived.
	if !strings.Contains(pageHTML, "if(gen !== streamGen || pinnedAt) return;") {
		t.Error("the page would apply a delta to a graph pinned in the past")
	}
	if !strings.Contains(pageHTML, `return pinnedAt ? "api/stream?as_of=" + pinnedAt : "api/stream";`) {
		t.Error("the page does not pin its own stream, so the server could not know to stop sending")
	}
}

// The tally counts the store as it is now. Under a scene pinned to an instant
// in the past the two read as one answer, which is the quiet disagreement this
// page exists to surface rather than create.
func TestTheContractPanelDoesNotCountThePresentUnderAPinnedScene(t *testing.T) {
	if !strings.Contains(pageHTML, "so it is not ") ||
		!strings.Contains(pageHTML, "read while the scene is showing then") {
		t.Error("the contract panel would go on counting the present under a picture of the past")
	}
}

// millisecondApart is Now() with room either side of it for a millisecond.
//
// The store stamps to microseconds and the page pins to milliseconds — a
// datetime field has no finer unit and a millisecond count is what survives the
// trip through JavaScript without a timezone argument. Truncating an instant to
// the millisecond rounds it down, so an instant taken in the same millisecond
// as the write before it lands *before* that write. Everywhere this matters a
// person is picking a date, so the resolution is not the problem; a test that
// took both instants inside one millisecond would be.
func millisecondApart(db *cortexdb.DB) time.Time {
	time.Sleep(2 * time.Millisecond)
	at := db.Graph().Now()
	time.Sleep(2 * time.Millisecond)
	return at
}

func nodeIDs(nodes []Node) string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	// LoadLocal ranks by degree and then by id, so a set with equal degrees is
	// already in id order; sorting here only guards a future change to that.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return strings.Join(out, ",")
}

// temporalStore is a brain new enough to have the bitemporal columns, holding
// one node written before the test does anything else.
func temporalStore(t *testing.T) (*cortexdb.DB, context.Context) {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "temporal.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{
		ID: "entity:early", Vector: []float32{1, 0, 0, 0}, NodeType: "Entity", Content: "was here first",
		Properties: graded(cortexdb.GradeHeld, "nobody has looked", ""),
	}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	return db, ctx
}
