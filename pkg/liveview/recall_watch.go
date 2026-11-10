package liveview

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Every recall the brain logs reaches the view as it happens, whoever asked.
//
// The tool calls the MCP middleware reports are the ones this one process
// handled; on a shared brain that is almost none of them, as every other
// agent, hook and machine recalls through a process of its own. The recall
// log is the one place all of them meet, so the view reads its head on the
// same tick as the graph and turns each recall it has not seen into an
// activity event: the graph lights the records the recall returned, the
// activity feed lists it, and the recalls page, which reads its head again on
// any activity, shows it at once.
//
// One reader per view, however many pages are open, and none while no page is
// open: a page that opens later is sent what came in while it was away, up to
// a page of the log.

// recallWatchPage is how much of the log's head one tick reads.
const recallWatchPage = 25

type recallWatch struct {
	mu sync.Mutex
	// seen is the ids at the newest instant read so far, and since that
	// instant: a recall older than since has been reported or predates the
	// view, and one at since is told apart by its id.
	since time.Time
	seen  map[string]bool
	// tools is the tools whose recalls the log keeps. A call of one of them
	// this process handled is reported by the log, with the records it
	// returned, so the middleware's own event for it is dropped (Observe).
	tools  map[string]bool
	live   bool
	failed bool
}

// logged reports whether a tool call is one the recall log will report.
func (w *recallWatch) logged(ev Event) bool {
	if w == nil || ev.Failed {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.live && w.tools[ev.Tool]
}

// following reports whether recalls reach the view from the log: a view
// started from the command line observes no tool calls, but follows every
// recall when the brain logs them.
func (w *recallWatch) following() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.live
}

// watchRecalls reads the recall log's head on every tick while a page is open.
func (s *Server) watchRecalls(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.readRecalls(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if s.hub.subscriberCount() > 0 {
			s.readRecalls(ctx)
		}
	}
}

// readRecalls publishes the recalls logged since the last read, oldest first.
// The first read marks where the log stood and puts what it found in the
// backlog a page opens with: a page opened just after the view started shows
// the latest recalls instead of nothing, and lights none of them.
func (s *Server) readRecalls(ctx context.Context) {
	w := s.recalls
	ctx, cancel := context.WithTimeout(ctx, exploreTimeout)
	defer cancel()
	var page cortexdb.RecallLogListResponse
	err := callInto(ctx, s.src.Call, "recall_log_list", cortexdb.RecallLogListRequest{Limit: recallWatchPage}, &page)
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		// A brain without the log says so on every read; a brain restarting
		// comes back. Either way one line, and the next tick tries again.
		if !w.failed {
			fmt.Fprintf(os.Stderr, "cortexdb: live view: recall log: %v\n", err)
			w.failed = true
		}
		w.live = false
		return
	}
	w.failed = false
	first := !w.live
	w.live = true

	var fresh []cortexdb.RecallLogEntry
	for _, e := range page.Recalls {
		if e.Tool != "" {
			w.tools[e.Tool] = true
		}
		if !first && (e.At.After(w.since) || e.At.Equal(w.since) && !w.seen[e.ID]) {
			fresh = append(fresh, e)
		}
	}
	for _, e := range page.Recalls {
		switch {
		case e.At.After(w.since):
			w.since = e.At
			w.seen = map[string]bool{e.ID: true}
		case e.At.Equal(w.since):
			w.seen[e.ID] = true
		}
	}
	if first {
		var backlog []Event
		for i := len(page.Recalls) - 1; i >= 0; i-- {
			backlog = append(backlog, recallEvent(page.Recalls[i]))
		}
		s.hub.seedEvents(backlog)
		return
	}
	// The log answers newest first.
	for i := len(fresh) - 1; i >= 0; i-- {
		s.hub.publishEvent(recallEvent(fresh[i]))
	}
}

// recallEvent is one logged recall as the view's activity: who asked what,
// lighting the records it returned.
func recallEvent(e cortexdb.RecallLogEntry) Event {
	ev := Event{
		At:   e.At.UnixMilli(),
		Kind: KindQuery,
		Tool: e.Tool,
		Text: recallLine(e),
	}
	if ev.Tool == "" {
		ev.Tool = "recall"
	}
	for _, h := range e.Hits {
		if h.NodeID != "" {
			ev.Nodes = append(ev.Nodes, h.NodeID)
		}
	}
	return ev
}

// recallLine is the ticker's line for a recall: the question as typed (the
// auto-recall hooks add the project and the last reply below a blank line),
// after who asked.
func recallLine(e cortexdb.RecallLogEntry) string {
	q := strings.TrimSpace(e.Query)
	if i := strings.Index(q, "\n\n"); i > 0 {
		q = q[:i]
	}
	q = strings.Join(strings.Fields(q), " ")
	if r := []rune(q); len(r) > 140 {
		q = string(r[:140]) + "…"
	}
	who := e.Agent
	if e.Host != "" {
		if who != "" {
			who += "@"
		}
		who += e.Host
	}
	if who == "" {
		return q
	}
	return who + ": " + q
}
