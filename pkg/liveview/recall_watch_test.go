package liveview

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// A recall another process made reaches an open page as activity, lighting
// the records it returned; what was recalled before the view opened does not,
// and a recall this process handled is reported once, by the log.
func TestARecallAnyoneMakesReachesTheOpenPage(t *testing.T) {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "watch.db")), cortexdb.WithRecallLog(cortexdb.RecallLogOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "m1", Scope: "global", Content: "the brain's VIP is 192.168.123.253"}); err != nil {
		t.Fatal(err)
	}
	recall := func(q string) {
		t.Helper()
		if _, err := db.KnowledgeMemory().Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{Query: q}); err != nil {
			t.Fatal(err)
		}
	}
	recall("brain VIP before the view")

	src := &Source{
		Describe: "watch <test>",
		Read:     func(ctx context.Context) ([]Node, []Edge, error) { return LoadLocal(ctx, db.SQL()) },
		Call:     localCaller(db),
	}
	sv, err := New(ctx, src, 20*time.Millisecond, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	ch, _, _, _ := sv.hub.subscribe()
	defer sv.hub.unsubscribe(ch)

	waitLive := time.Now().Add(5 * time.Second)
	for !sv.recalls.logged(Event{Tool: "knowledge_memory_recall"}) {
		if time.Now().After(waitLive) {
			t.Fatal("the recall log was never read")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// What was recalled before the view opened is in the backlog a page opens
	// with, and was sent to no page as news.
	_, _, backlog, _ := sv.hub.subscribe()
	if len(backlog) != 1 || backlog[0].Text != "brain VIP before the view" {
		t.Fatalf("backlog %+v, want the recall made before the view", backlog)
	}
	// What the middleware would report for a recall this process handled.
	sv.Observe(Event{Kind: KindQuery, Tool: "knowledge_memory_recall", Text: "brain VIP"})
	recall("brain VIP")

	var got []Event
	deadline := time.After(5 * time.Second)
	for len(got) == 0 {
		select {
		case m := <-ch:
			if m.Event != nil {
				got = append(got, *m.Event)
			}
		case <-deadline:
			t.Fatal("no recall reached the page")
		}
	}
	// Anything else on its way arrives within a few ticks.
	settle := time.After(200 * time.Millisecond)
	for done := false; !done; {
		select {
		case m := <-ch:
			if m.Event != nil {
				got = append(got, *m.Event)
			}
		case <-settle:
			done = true
		}
	}
	if len(got) != 1 {
		t.Fatalf("want the one recall made while the page was open, got %+v", got)
	}
	ev := got[0]
	if ev.Text != "brain VIP" || ev.Tool != "knowledge_memory_recall" {
		t.Errorf("event %+v", ev)
	}
	if len(ev.Nodes) != 1 || ev.Nodes[0] != "memory:m1" {
		t.Errorf("lights %v, want the record it returned", ev.Nodes)
	}
	// A failed call is not in the log and still shows.
	if sv.recalls.logged(Event{Tool: "knowledge_memory_recall", Failed: true}) {
		t.Error("a failed recall was left to the log")
	}
}

// The ticker names who asked and the question as typed.
func TestARecallLineIsWhoAskedWhat(t *testing.T) {
	cases := []struct {
		e    cortexdb.RecallLogEntry
		want string
	}{
		{cortexdb.RecallLogEntry{Query: "  why\n is it   slow\n\nProject: x"}, "why is it slow"},
		{cortexdb.RecallLogEntry{Agent: "claude-code", Host: "Pro", Query: "？"}, "claude-code@Pro: ？"},
		{cortexdb.RecallLogEntry{Host: "node-b", Query: "q"}, "node-b: q"},
	}
	for _, c := range cases {
		if got := recallLine(c.e); got != c.want {
			t.Errorf("recallLine(%+v) = %q, want %q", c.e, got, c.want)
		}
	}
}
