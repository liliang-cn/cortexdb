package liveview

// /api/changes and /api/entity: the two reads the Changes and Entity pages
// are made of.
//
// Both are answered without asking the brain anything a page of the stream
// does not already ask. /api/changes pages the feed backwards, newest first,
// through the same ChangeLog hook the stream uses; /api/entity reads the graph
// the hub already holds.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

const (
	changesDefaultLimit = 50
	changesMaxLimit     = 200
	// changesScanPages bounds how many feed pages one request reads looking for
	// enough matches. A filter that matches almost nothing would otherwise
	// scan the whole retained log; instead the answer carries the cursor it
	// got to and the page asks again.
	changesScanPages = 10
)

// ChangesPage is one page of the feed for the timeline.
type ChangesPage struct {
	// Items are newest first.
	Items []ChangeItem `json:"items"`
	// Head is the newest committed seq.
	Head int64 `json:"head"`
	// NextBefore is the `before` of the next, older page; absent at the oldest
	// retained event.
	NextBefore int64 `json:"next_before,omitempty"`
	// PrunedThrough is the retention boundary, when the brain knows it.
	PrunedThrough int64 `json:"pruned_through,omitempty"`
}

// changeFilter is the filters of /api/changes. Repeated values of one filter
// are ORed; different filters are ANDed.
type changeFilter struct {
	kinds, ops, producers, entities map[string]bool
}

func newFilter(values []string) map[string]bool {
	var set map[string]bool
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			if set == nil {
				set = make(map[string]bool)
			}
			set[v] = true
		}
	}
	return set
}

func (f changeFilter) matches(it ChangeItem) bool {
	if f.kinds != nil && !f.kinds[it.Kind] {
		return false
	}
	if f.ops != nil && !f.ops[it.Op] {
		return false
	}
	if f.producers != nil && !f.producers[it.Producer] {
		return false
	}
	if f.entities != nil {
		hit := false
		for _, id := range it.Entities {
			if f.entities[id] {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func (f changeFilter) kindList() []string {
	out := make([]string, 0, len(f.kinds))
	for k := range f.kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// int64Param reads an optional non-negative integer.
func int64Param(r *http.Request, name string) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s %q is not a non-negative integer", name, raw)
	}
	return n, nil
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	before, err := int64Param(r, "before")
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	after, err := int64Param(r, "after")
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	limit, err := intParam(r, "limit")
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	if limit == 0 {
		limit = changesDefaultLimit
	}
	limit = min(limit, changesMaxLimit)
	q := r.URL.Query()
	filter := changeFilter{kinds: newFilter(q["kind"]), ops: newFilter(q["op"]), producers: newFilter(q["producer"]), entities: newFilter(q["entity"])}

	if s.src.ChangeLog == nil {
		// Not an error: this brain keeps no feed, and the page says so from
		// snapshot.changes. An empty timeline is what it has to show.
		_ = json.NewEncoder(w).Encode(ChangesPage{Items: []ChangeItem{}})
		return
	}
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	page, err := s.pageChanges(ctx, before, after, limit, filter)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(page)
}

// pageChanges reads the feed backwards from `before` (the head when zero)
// down to `after`, until it has limit items that pass the filter.
func (s *Server) pageChanges(ctx context.Context, before, after int64, limit int, f changeFilter) (ChangesPage, error) {
	out := ChangesPage{Items: []ChangeItem{}}
	cursor := before
	if cursor == 0 {
		cursor = -1 // the head, the way ChangesSinceRequest.Before spells it
	}
	// Only the kind filter can be pushed into the read; the others are about
	// the item, and a page of exactly the size asked is the cheapest read when
	// nothing else will be thrown away.
	pageSize := feedPageSize
	if f.ops == nil && f.producers == nil && f.entities == nil {
		pageSize = limit
	}
	for scanned := 0; scanned < changesScanPages; scanned++ {
		r, err := s.src.ChangeLog(ctx, cortexdb.ChangesSinceRequest{
			After: after, Before: cursor, Limit: pageSize, Kinds: f.kindList(), Explain: true,
		})
		if err != nil {
			return out, err
		}
		if scanned == 0 {
			out.Head = r.Head
		}
		if r.PrunedThrough > 0 {
			out.PrunedThrough = r.PrunedThrough
		}
		// A brain that predates Before answers with the oldest events first.
		// Say so rather than draw a timeline that runs the wrong way.
		if len(r.Events) > 1 && r.Events[0].Seq < r.Events[len(r.Events)-1].Seq {
			return out, errors.New("this brain's changes_since cannot read backwards; upgrade it to page the timeline")
		}
		items := ChangeItems(r.Events, s.hub.snapshot(), r.Labels)
		for _, it := range items {
			if !f.matches(it) {
				continue
			}
			out.Items = append(out.Items, it)
			if len(out.Items) == limit {
				// The rest of this page, and everything older, is for the
				// next request: resume just below the last item taken.
				out.NextBefore = it.Seq
				return out, nil
			}
		}
		if !r.More || len(r.Events) == 0 {
			return out, nil
		}
		cursor = r.NextCursor
		out.NextBefore = cursor
	}
	return out, nil
}

// EntityRelation is one edge of an entity as the Entity page lists it.
type EntityRelation struct {
	EdgeID    string `json:"edge_id"`
	Predicate string `json:"predicate"`
	Other     Ref    `json:"other"`
	Grade     string `json:"grade,omitempty"`
	// ValidFrom and RecordedAt are unix milliseconds, when the snapshot knows
	// them. It does not today; /api/history has the versions.
	ValidFrom  int64 `json:"valid_from,omitempty"`
	RecordedAt int64 `json:"recorded_at,omitempty"`
}

// RelationGroup is the relations of one predicate.
type RelationGroup struct {
	Predicate string           `json:"predicate"`
	Edges     []EntityRelation `json:"edges"`
}

// EntityView is one node with its relations grouped by predicate.
type EntityView struct {
	// Node is from the current snapshot, null when the id is not in the graph
	// now (a node since superseded or deleted still has a history).
	Node *Node           `json:"node"`
	Out  []RelationGroup `json:"out"`
	In   []RelationGroup `json:"in"`
}

// entityView groups the relations of one node, outgoing and incoming, each
// group's edges and the groups themselves in a fixed order: largest group
// first, then by predicate.
func entityView(snap Snapshot, id string) EntityView {
	nodes := make(map[string]Node, len(snap.Nodes))
	for _, n := range snap.Nodes {
		nodes[n.ID] = n
	}
	view := EntityView{Out: []RelationGroup{}, In: []RelationGroup{}}
	if n, ok := nodes[id]; ok {
		view.Node = &n
	}
	ref := func(id string) Ref {
		n, ok := nodes[id]
		if !ok {
			return Ref{ID: id, Label: lastSegment(id)}
		}
		return Ref{ID: n.ID, Label: n.Label, Type: n.Type}
	}
	out, in := map[string][]EntityRelation{}, map[string][]EntityRelation{}
	for _, e := range snap.Edges {
		switch {
		case e.Source == id:
			out[e.Label] = append(out[e.Label], EntityRelation{EdgeID: e.ID, Predicate: e.Label, Other: ref(e.Target), Grade: e.Grade})
		case e.Target == id:
			in[e.Label] = append(in[e.Label], EntityRelation{EdgeID: e.ID, Predicate: e.Label, Other: ref(e.Source), Grade: e.Grade})
		}
		// A self-loop is listed outgoing only; the Entity page would otherwise
		// show the same edge on both sides.
	}
	view.Out, view.In = groupRelations(out), groupRelations(in)
	return view
}

func groupRelations(by map[string][]EntityRelation) []RelationGroup {
	groups := make([]RelationGroup, 0, len(by))
	for predicate, edges := range by {
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].Other.Label != edges[j].Other.Label {
				return edges[i].Other.Label < edges[j].Other.Label
			}
			return edges[i].EdgeID < edges[j].EdgeID
		})
		groups = append(groups, RelationGroup{Predicate: predicate, Edges: edges})
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Edges) != len(groups[j].Edges) {
			return len(groups[i].Edges) > len(groups[j].Edges)
		}
		return groups[i].Predicate < groups[j].Predicate
	})
	return groups
}

func (s *Server) handleEntity(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeBadRequest(w, errors.New("id is required: a node id"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(entityView(s.hub.snapshot(), id))
}

// handleUnknownAPI answers any /api/ path no route claimed. Without it the
// catch-all "/" would hand a typo'd API call the app's index.html and a
// client would try to parse HTML as JSON.
func handleUnknownAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "no such API route: " + r.URL.Path})
}
