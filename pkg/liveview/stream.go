package liveview

// The change half of the live stream: reading the feed for the poller, and
// resuming a connection from where it stopped.
//
// A page that was disconnected — a laptop lid, a proxy restart, a flaky
// network — reconnects by itself and sends the id of the last change it saw
// (Last-Event-ID). The server answers from the feed's own log rather than from
// memory, so a page that was away for an hour is made whole as surely as one
// that missed a second, and a server restart loses nothing.
//
// The order of a resumed connection is fixed:
//
//  1. the snapshot, as always, carrying the cursor it was taken at;
//  2. the changes after the page's cursor up to that cursor, oldest first;
//  3. everything after, live.
//
// There is no gap and no duplicate between 2 and 3 because the hub hands out
// the cursor under the same lock that registers the subscription: every change
// at or below it was published before the page was listening and is in the
// log, every change above it will reach the page's channel (see Hub.subscribe).

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const (
	// feedPageSize is the events read per page, here and in /api/changes.
	feedPageSize = 200
	// feedBatchPages bounds what one poll tick reads; a bigger backlog (a bulk
	// import) is drained over the next ticks.
	feedBatchPages = 10
	// replayPages bounds one replay. A page that was away for longer than this
	// is told to rebuild instead of being fed thousands of events one by one.
	replayPages = 25
)

// feedBatch is what one poll tick read from the change feed.
type feedBatch struct {
	events []graph.ChangeEvent
	labels map[string]string
	// cursor is the seq read up to: the last event scanned, shown or not.
	cursor int64
	// structural is whether any event was a node or an edge, which is the one
	// thing that makes the poller re-read the graph.
	structural bool
	// pruned is set when the feed no longer reaches back to the cursor.
	pruned *ResetNotice
}

// readFeed reads the events after a cursor, in order, a few pages at most.
func (s *Server) readFeed(ctx context.Context, after int64) (*feedBatch, error) {
	b := &feedBatch{cursor: after}
	for page := 0; page < feedBatchPages; page++ {
		r, err := s.src.ChangeLog(ctx, cortexdb.ChangesSinceRequest{After: b.cursor, Limit: feedPageSize, Explain: true})
		if err != nil {
			return nil, err
		}
		if r.Pruned {
			return &feedBatch{cursor: r.Head, pruned: &ResetNotice{Reason: "pruned", PrunedThrough: r.PrunedThrough, Head: r.Head}}, nil
		}
		b.events = append(b.events, r.Events...)
		for _, ev := range r.Events {
			if ev.Kind == graph.ChangeKindNode || ev.Kind == graph.ChangeKindEdge {
				b.structural = true
			}
		}
		if len(r.Labels) > 0 {
			if b.labels == nil {
				b.labels = make(map[string]string, len(r.Labels))
			}
			for id, l := range r.Labels {
				b.labels[id] = l
			}
		}
		b.cursor = r.NextCursor
		if !r.More {
			break
		}
	}
	return b, nil
}

// resumeCursor is where a reconnecting page says it stopped: the
// Last-Event-ID header, which the browser sends by itself, or ?after=, which a
// page that manages its own connection sends. The header wins.
func resumeCursor(r *http.Request) (int64, bool) {
	for _, raw := range []string{r.Header.Get("Last-Event-ID"), r.URL.Query().Get("after")} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
			return n, true
		}
	}
	return 0, false
}

// probeResume reads the first page of a replay before the stream writes
// anything. It returns a reset when the cursor cannot be replayed from, and
// otherwise the page to start replaying with.
func (s *Server) probeResume(ctx context.Context, after, head int64) (*ResetNotice, *cortexdb.ChangesSinceResponse) {
	if after > head {
		// From a brain further along than this one: a different database, or
		// one restored from a backup. Its seqs mean nothing here.
		return &ResetNotice{Reason: "ahead", Head: head}, nil
	}
	r, err := s.src.ChangeLog(ctx, cortexdb.ChangesSinceRequest{After: after, Limit: feedPageSize, Explain: true})
	switch {
	case err != nil:
		return &ResetNotice{Reason: "unavailable", Head: head}, nil
	case r.Pruned:
		return &ResetNotice{Reason: "pruned", PrunedThrough: r.PrunedThrough, Head: head}, nil
	}
	return nil, r
}

// errStreamClosed is replay's answer when the connection went away.
var errStreamClosed = &ResetNotice{Reason: "closed"}

// replay writes the missed changes, oldest first, from the page already read
// on to the cursor the subscription was taken at. It returns nil when done, a
// reset when the replay cannot be completed, and errStreamClosed when the
// connection is gone.
func (s *Server) replay(ctx context.Context, w http.ResponseWriter, f http.Flusher, r *cortexdb.ChangesSinceResponse, head int64) *ResetNotice {
	for pages := 1; ; pages++ {
		events := r.Events
		// The log can be ahead of the hub: what is past the cursor reaches the
		// page live, and sending it here as well would be a duplicate.
		for i, ev := range events {
			if ev.Seq > head {
				events = events[:i]
				break
			}
		}
		for _, item := range ChangeItems(events, s.hub.snapshot(), r.Labels) {
			if !writeSSEID(w, f, item.Seq, "change", item) {
				return errStreamClosed
			}
		}
		if !r.More || r.NextCursor >= head {
			return nil
		}
		if pages >= replayPages {
			return &ResetNotice{Reason: "behind", Head: head}
		}
		next, err := s.src.ChangeLog(ctx, cortexdb.ChangesSinceRequest{After: r.NextCursor, Limit: feedPageSize, Explain: true})
		if err != nil || next.Pruned {
			return &ResetNotice{Reason: "unavailable", Head: head}
		}
		r = next
	}
}
