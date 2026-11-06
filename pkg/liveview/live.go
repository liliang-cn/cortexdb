package liveview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// The live view's data model.
//
// Two things move on the page, and they arrive by different routes:
//
//   - structure — nodes and edges appearing or disappearing. The graph can be
//     written by any machine sharing it; the poller asks the change feed
//     whether anything did and re-reads and diffs only then (see changes.go).
//   - activity — a query running, something being saved, a relation being
//     drawn. Observed as it happens, from inside the MCP server that handles
//     the call, so the view lights up on the same call the agent just made.
//
// Structure is the truth and activity is the pulse: a query changes nothing in
// the graph, but watching which nodes it touched is most of what makes a brain
// legible.

const (
	KindQuery  = "query"
	KindWrite  = "write"
	KindRelate = "relate"
)

// Snapshot is one reading of the brain's entity graph.
type Snapshot struct {
	Version int64  `json:"version"`
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`
}

// Delta is what changed between two snapshots.
//
// AddedNodes and AddedEdges carry upsert semantics rather than insert: a record
// whose label, type or grade changed is re-sent here, not removed and re-added,
// so the view updates it in place and keeps the position the layout already
// settled on.
//
// For an edge that used to be a distinction without a difference — an edge was
// its two ends and its label, all three of which are its identity, so nothing
// could change without it becoming a different edge. It stopped being one when
// an edge started carrying its grade: a record reviewed from held to verified
// is the same edge saying something new about itself, and a view that only
// learned about edges appearing and disappearing would have gone on drawing
// last week's verdict.
type Delta struct {
	Version      int64    `json:"version"`
	AddedNodes   []Node   `json:"added_nodes"`
	RemovedNodes []string `json:"removed_nodes"`
	AddedEdges   []Edge   `json:"added_edges"`
	RemovedEdges []Edge   `json:"removed_edges"`
	// Nodes and Edges are the totals after applying, so the page's counters
	// cannot drift out of step with the brain if a delta is ever missed.
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

// Empty reports whether the delta changes nothing, so the poller can stay quiet
// instead of waking every connected page on a timer.
func (d Delta) Empty() bool {
	return len(d.AddedNodes) == 0 && len(d.RemovedNodes) == 0 &&
		len(d.AddedEdges) == 0 && len(d.RemovedEdges) == 0
}

// edgeKey identifies an edge by both endpoints AND its type: two nodes can be
// joined by several different relations, and keying on the pair alone would
// hide every one after the first.
func edgeKey(e Edge) string {
	return e.Source + "\x00" + e.Label + "\x00" + e.Target
}

// Diff computes what changed from prev to next.
func Diff(prev, next Snapshot) Delta {
	d := Delta{
		Version: next.Version,
		Nodes:   len(next.Nodes),
		Edges:   len(next.Edges),
	}

	prevNodes := make(map[string]Node, len(prev.Nodes))
	for _, n := range prev.Nodes {
		prevNodes[n.ID] = n
	}
	nextNodes := make(map[string]struct{}, len(next.Nodes))
	for _, n := range next.Nodes {
		nextNodes[n.ID] = struct{}{}
		if old, seen := prevNodes[n.ID]; !seen || old != n {
			d.AddedNodes = append(d.AddedNodes, n)
		}
	}
	for id := range prevNodes {
		if _, still := nextNodes[id]; !still {
			d.RemovedNodes = append(d.RemovedNodes, id)
		}
	}
	sort.Strings(d.RemovedNodes)

	prevEdges := make(map[string]Edge, len(prev.Edges))
	for _, e := range prev.Edges {
		prevEdges[edgeKey(e)] = e
	}
	nextEdges := make(map[string]struct{}, len(next.Edges))
	for _, e := range next.Edges {
		k := edgeKey(e)
		nextEdges[k] = struct{}{}
		if old, seen := prevEdges[k]; !seen || old != e {
			d.AddedEdges = append(d.AddedEdges, e)
		}
	}
	removedKeys := make([]string, 0)
	for k := range prevEdges {
		if _, still := nextEdges[k]; !still {
			removedKeys = append(removedKeys, k)
		}
	}
	sort.Strings(removedKeys)
	for _, k := range removedKeys {
		d.RemovedEdges = append(d.RemovedEdges, prevEdges[k])
	}
	return d
}

// Event is one thing the brain just did.
type Event struct {
	Seq  int64  `json:"seq"`
	At   int64  `json:"at"` // unix milliseconds
	Kind string `json:"kind"`
	Tool string `json:"tool"`
	// Text is the line the ticker shows.
	Text string `json:"text"`
	// Terms are the strings the page lights nodes up by. They are matched
	// loosely against labels and ids rather than joined on node identity: a
	// tool's arguments name things the way a person does ("CortexDB"), and the
	// graph keys them the way a database does ("entity:CortexDB").
	Terms []string `json:"terms,omitempty"`
	// Links are from/to pairs to draw a pulse along, for relation writes.
	Links  [][2]string `json:"links,omitempty"`
	Failed bool        `json:"failed,omitempty"`
}

// toolKinds classifies a tool by name, most specific first. Names that match
// nothing here still produce an event — an unrecognised tool is more likely a
// new one than a boring one — but they are read as queries, which is the
// harmless guess: a query only pulses, it never draws or erases anything.
var toolKinds = []struct {
	match func(string) bool
	kind  string
}{
	{func(n string) bool {
		return n == "upsert_relations" || n == "knowledge_graph_upsert" || n == "apply_inference" ||
			n == "knowledge_graph_infer_refresh"
	}, KindRelate},
	{func(n string) bool {
		return containsAny(n, "save", "update", "remember", "upsert", "ingest", "insert",
			"import", "delete", "promote", "consolidate", "extract", "repair", "resolve", "apply")
	}, KindWrite},
}

// viewTools are the tools that draw the view itself. They are deliberately
// silent: reporting them would make the ticker announce every time someone
// opened the page, and a page that reacts to being opened is noise.
var viewTools = map[string]bool{
	"render_graph_html": true,
	"serve_graph_3d":    true,
}

// ClassifyToolCall turns one MCP tool call into an event for the live view,
// reporting false for calls the view should ignore.
func ClassifyToolCall(tool string, args json.RawMessage, failed bool) (Event, bool) {
	tool = strings.TrimSpace(tool)
	if tool == "" || viewTools[tool] {
		return Event{}, false
	}

	kind := KindQuery
	for _, rule := range toolKinds {
		if rule.match(tool) {
			kind = rule.kind
			break
		}
	}

	ev := Event{
		At:     time.Now().UnixMilli(),
		Kind:   kind,
		Tool:   tool,
		Failed: failed,
	}
	ev.Terms, ev.Links = liftTerms(args)
	ev.Text = eventText(ev)
	return ev, true
}

// eventText composes the ticker line: what ran, and the first thing it named.
func eventText(ev Event) string {
	if len(ev.Links) > 0 {
		return fmt.Sprintf("%s → %s", ev.Links[0][0], ev.Links[0][1])
	}
	if len(ev.Terms) > 0 {
		return ev.Terms[0]
	}
	return ev.Tool
}

// termKeys are the argument fields that name something a person would recognise
// on the graph. Anything else in a tool's arguments — limits, modes, flags — is
// machinery, and lighting nodes up by it would be noise.
var termKeys = map[string]bool{
	"query": true, "keywords": true, "alternate_queries": true, "entity_names": true,
	"name": true, "names": true, "title": true, "subject": true, "object": true,
	"id": true, "ids": true, "node_id": true, "node_ids": true, "start": true, "end": true,
	"from": true, "to": true, "source": true, "target": true, "entity": true, "entities": true,
	"concept": true, "text": true, "content": true, "question": true,
}

// Deliberately not in termKeys: "type". On a relation it is the edge's label
// and on an entity it is the entity's class — neither is a node, so lighting
// nodes up by it means a save of type "Person" flashes whatever node happens to
// be named Person.

// linkPairKeys are the shapes a relation is written in across the tool surface.
var linkFromKeys = []string{"from", "source", "subject", "start"}
var linkToKeys = []string{"to", "target", "object", "end"}

// liftTerms walks a tool's arguments for the strings worth showing.
//
// It reads the JSON generically rather than per-tool: the tool surface is well
// over eighty calls and still growing, and a switch over all of them would be
// wrong the first time one was added. The cost is that it is a heuristic, which
// is the right trade for something that decides which dots glow.
func liftTerms(args json.RawMessage) ([]string, [][2]string) {
	if len(args) == 0 {
		return nil, nil
	}
	var root any
	if err := json.Unmarshal(args, &root); err != nil {
		return nil, nil
	}

	var (
		terms []string
		links [][2]string
		seen  = map[string]bool{}
	)
	add := func(s string) {
		s = strings.TrimSpace(s)
		// Long values are prose — a memory's body, a document — not a name.
		// Showing them would push everything else out of the ticker.
		if s == "" || len([]rune(s)) > 60 || seen[s] {
			return
		}
		seen[s] = true
		terms = append(terms, s)
	}

	var walk func(v any, key string, depth int)
	walk = func(v any, key string, depth int) {
		if depth > 6 || len(terms) >= 12 {
			return
		}
		switch t := v.(type) {
		case string:
			if termKeys[key] {
				add(t)
			}
		case []any:
			for _, item := range t {
				walk(item, key, depth+1)
			}
		case map[string]any:
			if from, to, ok := linkPair(t); ok {
				links = append(links, [2]string{from, to})
				add(from)
				add(to)
			}
			// Sorted so the same arguments always yield the same ticker line.
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			// The caller's own query outranks whatever a nested object names,
			// so surface it before descending.
			for _, k := range []string{"query", "question"} {
				if s, ok := t[k].(string); ok {
					add(s)
				}
			}
			for _, k := range keys {
				walk(t[k], k, depth+1)
			}
		}
	}
	walk(root, "", 0)
	if len(links) > 8 {
		links = links[:8]
	}
	return terms, links
}

// linkPair reads an object as a relation if it names both ends.
func linkPair(obj map[string]any) (string, string, bool) {
	pick := func(keys []string) string {
		for _, k := range keys {
			if s, ok := obj[k].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	from, to := pick(linkFromKeys), pick(linkToKeys)
	if from == "" || to == "" {
		return "", "", false
	}
	return from, to, true
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Hub holds the current graph and fans structure and activity out to every
// page watching.
//
// Slow readers are dropped rather than waited on: a browser tab that stopped
// draining its stream must never be able to stall the MCP server that is
// feeding it, because that server is also the one answering the agent.
type Hub struct {
	mu     sync.Mutex
	snap   Snapshot
	seq    int64
	recent []Event
	subs   map[chan Message]struct{}
	// cursor is the change feed's seq up to which every change has been
	// published to the subscribers. A new subscriber learns it under the lock
	// that adds it, so "replay the log up to the cursor, then take the live
	// channel" has neither a gap nor a duplicate: everything at or below it
	// was published before the subscription existed, everything above it will
	// arrive on the channel.
	cursor int64
}

// Message is one server-sent event: exactly one of the fields is set.
type Message struct {
	Delta  *Delta       `json:"delta,omitempty"`
	Event  *Event       `json:"event,omitempty"`
	Change *ChangeItem  `json:"change,omitempty"`
	Reset  *ResetNotice `json:"reset,omitempty"`
}

// ResetNotice tells a page its change cursor can no longer be trusted: it fell
// behind what the feed retains, so the page must rebuild its state from the
// snapshot that follows and continue from Head.
type ResetNotice struct {
	// Reason is "pruned" when the cursor is older than retention, "behind"
	// when too much happened for one replay, "ahead" when the cursor is from a
	// brain further along than this one, and "unavailable" when the feed could
	// not be read to replay.
	Reason        string `json:"reason"`
	PrunedThrough int64  `json:"pruned_through"`
	Head          int64  `json:"head"`
}

const recentEventsCap = 60

// subscriberBuffer is how many messages a page may fall behind by.
const subscriberBuffer = 256

func NewHub() *Hub {
	return &Hub{subs: make(map[chan Message]struct{})}
}

// subscribe returns a channel of updates plus the snapshot, backlog and change
// cursor they follow, taken under the same lock so a page cannot miss an update
// that landed between reading the graph and being wired up.
func (h *Hub) subscribe() (chan Message, Snapshot, []Event, int64) {
	ch := make(chan Message, subscriberBuffer)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs[ch] = struct{}{}
	backlog := append([]Event(nil), h.recent...)
	return ch, h.snap, backlog, h.cursor
}

// changeCursor is the seq the change feed has been published up to.
func (h *Hub) changeCursor() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cursor
}

// setCursor moves the cursor without publishing, for the head taken at start.
func (h *Hub) setCursor(c int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c > h.cursor {
		h.cursor = c
	}
}

// publishChanges fans out change items in order and moves the cursor to the
// seq the poller has read up to, which can be past the last item: events the
// view does not show (bookkeeping) still advance it. Items and cursor change
// under one lock, which is what subscribe's guarantee rests on.
func (h *Hub) publishChanges(items []ChangeItem, cursor int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range items {
		h.broadcastLocked(Message{Change: &items[i]})
	}
	if cursor > h.cursor {
		h.cursor = cursor
	}
}

// publishReset tells every page to rebuild: the feed was pruned past the point
// the poller had read, so changes between were never published.
func (h *Hub) publishReset(r ResetNotice) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Head > h.cursor {
		h.cursor = r.Head
	}
	h.broadcastLocked(Message{Reset: &r})
}

func (h *Hub) unsubscribe(ch chan Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

// snapshot returns the graph as last read.
func (h *Hub) snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snap
}

// publishSnapshot records a new reading and fans out what changed. It reports
// the delta so a caller can log it.
func (h *Hub) publishSnapshot(next Snapshot) Delta {
	h.mu.Lock()
	next.Version = h.snap.Version + 1
	d := Diff(h.snap, next)
	h.snap = next
	empty := d.Empty()
	h.mu.Unlock()
	if !empty {
		h.broadcast(Message{Delta: &d})
	}
	return d
}

// publishEvent stamps an event and fans it out.
func (h *Hub) publishEvent(ev Event) {
	h.mu.Lock()
	h.seq++
	ev.Seq = h.seq
	if ev.At == 0 {
		ev.At = time.Now().UnixMilli()
	}
	h.recent = append(h.recent, ev)
	if len(h.recent) > recentEventsCap {
		h.recent = h.recent[len(h.recent)-recentEventsCap:]
	}
	h.mu.Unlock()
	h.broadcast(Message{Event: &ev})
}

func (h *Hub) broadcast(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcastLocked(msg)
}

func (h *Hub) broadcastLocked(msg Message) {
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
			// This page is not draining. It used to lose the message and go on
			// listening, which for a delta meant a graph quietly out of step
			// and for a change meant a hole in a log whose point is having
			// none. The stream is closed instead: the browser reconnects by
			// itself, and a connection that resumes from its last change id
			// is made whole again.
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// recentEvents returns the activity backlog without subscribing, for the
// one-shot JSON endpoint.
func (h *Hub) recentEvents() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Event(nil), h.recent...)
}

func (h *Hub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
