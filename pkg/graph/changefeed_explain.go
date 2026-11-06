package graph

// What the history says about the events of a change page.
//
// A feed event for an edge says that row 41 was deleted, with the reason
// superseded. It does not say by what, and the answer that makes the event
// readable — "Leo lives_in Chengdu replaced Leo lives_in Beijing" — is in the
// history tables. Asked of them per event, a page of 200 would be 400 queries,
// and across a network to a shared brain that is the difference between a page
// and a stall. So one page costs a fixed handful of queries: the closed
// versions the page's updates and deletes refer to, the closures the page's
// inserted edges caused, the rows those point at, and the labels of every node
// named. Each is an IN over the ids the page holds, in chunks.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// explainChunk bounds the ids in one IN list, well under every backend's
// parameter limit.
const explainChunk = 300

// labelClip is how much of a node's content a label keeps.
const labelClip = 120

// histRow is one closed version, as far as an event needs it.
type histRow struct {
	validFrom            string // CAST(valid_from AS TEXT); "" when NULL
	null                 bool
	superseded, schemaID string
	schemaVersion        int
}

// explainChanges fills the Explain fields of the node and edge events and
// returns the labels of the nodes they name.
func (g *GraphStore) explainChanges(ctx context.Context, q rowQuerier, events []ChangeEvent) (map[string]string, error) {
	var closedNodes, closedEdges, insertedEdges []string
	for i := range events {
		ev := &events[i]
		closes := len(ev.Before) > 0 && (ev.Op == ChangeOpUpdate || ev.Op == ChangeOpDelete || ev.Op == ChangeOpSupersede || ev.Op == ChangeOpMerge)
		switch {
		case ev.Kind == ChangeKindNode && closes:
			closedNodes = append(closedNodes, ev.ID)
		case ev.Kind == ChangeKindEdge && closes:
			closedEdges = append(closedEdges, ev.ID)
		case ev.Kind == ChangeKindEdge && ev.Op == ChangeOpInsert:
			insertedEdges = append(insertedEdges, ev.ID)
		}
	}

	nodeHist, err := g.closedVersions(ctx, q, "graph_node_history", closedNodes)
	if err != nil {
		return nil, err
	}
	edgeHist, err := g.closedVersions(ctx, q, "graph_edge_history", closedEdges)
	if err != nil {
		return nil, err
	}
	closures, err := g.closuresCaused(ctx, q, insertedEdges)
	if err != nil {
		return nil, err
	}

	// What the closed edges were replaced by, in one read of the live edges
	// and one of the history for those since closed again.
	var replacers []string
	for i := range events {
		ev := &events[i]
		var h *histRow
		switch ev.Kind {
		case ChangeKindNode:
			h = matchVersion(nodeHist[ev.ID], ev.Before)
		case ChangeKindEdge:
			h = matchVersion(edgeHist[ev.ID], ev.Before)
		}
		if h == nil {
			continue
		}
		ev.SchemaID, ev.SchemaVersion = h.schemaID, h.schemaVersion
		if h.superseded != "" && h.superseded != ev.ID {
			ref := &ChangeRef{ID: h.superseded}
			ev.SupersededBy = ref
			if ev.Kind == ChangeKindEdge {
				replacers = append(replacers, h.superseded)
			}
		}
	}
	edgeRefs, err := g.edgeRefs(ctx, q, replacers)
	if err != nil {
		return nil, err
	}

	nodeNames := make(map[string]struct{})
	want := func(id string) {
		if id != "" {
			nodeNames[id] = struct{}{}
		}
	}
	for i := range events {
		ev := &events[i]
		if ev.Kind == ChangeKindEdge {
			ends := edgeEnds(ev)
			want(ends[0])
			want(ends[1])
			if ev.SupersededBy != nil {
				if r, ok := edgeRefs[ev.SupersededBy.ID]; ok {
					*ev.SupersededBy = r
				}
				want(ev.SupersededBy.From)
				want(ev.SupersededBy.To)
			}
			if c := closureOf(closures[ev.ID], ev.After); c != nil {
				ref := c.ref
				ev.Replaces = &ref
				if ev.SchemaID == "" {
					ev.SchemaID, ev.SchemaVersion = c.schemaID, c.schemaVersion
				}
				want(ref.From)
				want(ref.To)
			}
		}
		if ev.Kind == ChangeKindNode && ev.SupersededBy != nil {
			want(ev.SupersededBy.ID)
		}
	}
	labels, err := g.nodeLabels(ctx, q, nodeNames)
	if err != nil {
		return nil, err
	}
	for i := range events {
		if ref := events[i].SupersededBy; ref != nil && events[i].Kind == ChangeKindNode {
			ref.Content = labels[ref.ID]
		}
	}
	return labels, nil
}

// edgeEnds reads the endpoints out of an edge event's row summary.
func edgeEnds(ev *ChangeEvent) [2]string {
	raw := ev.After
	if len(raw) == 0 {
		raw = ev.Before
	}
	var row struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	_ = json.Unmarshal(raw, &row)
	return [2]string{row.From, row.To}
}

// forChunks runs fn over ids in IN-list sized pieces, once per distinct id.
func forChunks(ids []string, fn func(chunk []string) error) error {
	seen := make(map[string]struct{}, len(ids))
	uniq := ids[:0:0]
	for _, id := range ids {
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			uniq = append(uniq, id)
		}
	}
	sort.Strings(uniq)
	for len(uniq) > 0 {
		n := min(explainChunk, len(uniq))
		if err := fn(uniq[:n]); err != nil {
			return err
		}
		uniq = uniq[n:]
	}
	return nil
}

func inList(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

func anyArgs(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// closedVersions reads the closed versions of the given ids from a history
// table: when each one began (to tell which version an event closed), what
// replaced it and the schema it was closed under.
func (g *GraphStore) closedVersions(ctx context.Context, q rowQuerier, table string, ids []string) (map[string][]histRow, error) {
	out := make(map[string][]histRow)
	err := forChunks(ids, func(chunk []string) error {
		rows, err := q.QueryContext(ctx, g.dialect.Rebind(`SELECT id, CAST(valid_from AS TEXT), COALESCE(superseded_by, ''),
			COALESCE(schema_id, ''), COALESCE(schema_version, 0) FROM `+table+` WHERE id IN (`+inList(len(chunk))+`)`), anyArgs(chunk)...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			var from *string
			var h histRow
			if err := rows.Scan(&id, &from, &h.superseded, &h.schemaID, &h.schemaVersion); err != nil {
				return err
			}
			if from == nil {
				h.null = true
			} else {
				h.validFrom = *from
			}
			out[id] = append(out[id], h)
		}
		return rows.Err()
	})
	return out, err
}

// matchVersion picks the history row that is the version a change closed: the
// one whose valid_from is the valid_from of the row the change removed.
func matchVersion(rows []histRow, before json.RawMessage) *histRow {
	if len(rows) == 0 {
		return nil
	}
	var row struct {
		ValidFrom *string `json:"valid_from"`
	}
	if err := json.Unmarshal(before, &row); err != nil {
		return nil
	}
	var match *histRow
	for i := range rows {
		h := &rows[i]
		switch {
		case row.ValidFrom == nil:
			if h.null {
				match = h
			}
		case !h.null && sameInstant(h.validFrom, *row.ValidFrom):
			match = h
		}
	}
	return match
}

// instantLayouts are the spellings a timestamp takes between the two
// backends: SQLite keeps what the driver wrote, PostgreSQL renders its own in
// the JSON the trigger builds and in a CAST.
var instantLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999-07",
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
}

func parseInstant(s string) (time.Time, bool) {
	// The monotonic reading a time.Time prints is not part of the instant.
	if i := strings.Index(s, " m="); i >= 0 {
		s = s[:i]
	}
	for _, layout := range instantLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func sameInstant(a, b string) bool {
	if a == b {
		return true
	}
	ta, okA := parseInstant(a)
	tb, okB := parseInstant(b)
	return okA && okB && ta.Equal(tb)
}

type closure struct {
	ref           ChangeRef
	schemaID      string
	schemaVersion int
	// validTo is when the closed version ended. A supersession ends the old
	// value where the new one begins, so it is the new version's valid_from.
	validTo string
}

// closuresCaused finds, for each newly inserted edge id, the older edges its
// versions closed. An id is not a version: a relation written, replaced and
// written again (Leo lives_in Beijing, then Chengdu, then Beijing) inserts the
// same edge id twice, and only the second insert closed anything. Which
// closure belongs to which insert is settled by closureOf.
func (g *GraphStore) closuresCaused(ctx context.Context, q rowQuerier, ids []string) (map[string][]closure, error) {
	out := make(map[string][]closure)
	err := forChunks(ids, func(chunk []string) error {
		rows, err := q.QueryContext(ctx, g.dialect.Rebind(`SELECT superseded_by, id, from_node_id, to_node_id, COALESCE(edge_type, ''),
			COALESCE(schema_id, ''), COALESCE(schema_version, 0), CAST(valid_to AS TEXT) FROM graph_edge_history
			WHERE superseded_by IN (`+inList(len(chunk))+`) AND reason = 'superseded' AND id <> superseded_by ORDER BY id`), anyArgs(chunk)...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var by string
			var to *string
			var c closure
			if err := rows.Scan(&by, &c.ref.ID, &c.ref.From, &c.ref.To, &c.ref.Type, &c.schemaID, &c.schemaVersion, &to); err != nil {
				return err
			}
			if to != nil {
				c.validTo = *to
			}
			out[by] = append(out[by], c)
		}
		return rows.Err()
	})
	return out, err
}

// closureOf picks the closure an insert event caused: the closed version that
// ended at the instant the inserted version began. None matching means this
// insert closed nothing, even when another version of the same id did; saying
// nothing is right there, and naming that other closure would be false. (A
// value stated as true from before the old one began closes it at the
// recording instant instead, which the event does not carry, so such an
// insert is left without a `replaces` rather than given a guess.)
func closureOf(cs []closure, after json.RawMessage) *closure {
	if len(cs) == 0 {
		return nil
	}
	var row struct {
		ValidFrom *string `json:"valid_from"`
	}
	if err := json.Unmarshal(after, &row); err != nil || row.ValidFrom == nil {
		return nil
	}
	for i := range cs {
		if cs[i].validTo != "" && sameInstant(cs[i].validTo, *row.ValidFrom) {
			return &cs[i]
		}
	}
	return nil
}

// edgeRefs reads edges by id: from the live table, and for those since closed
// again from the history.
func (g *GraphStore) edgeRefs(ctx context.Context, q rowQuerier, ids []string) (map[string]ChangeRef, error) {
	out := make(map[string]ChangeRef)
	for _, table := range []string{"graph_edges", "graph_edge_history"} {
		var missing []string
		for _, id := range ids {
			if _, ok := out[id]; !ok {
				missing = append(missing, id)
			}
		}
		err := forChunks(missing, func(chunk []string) error {
			rows, err := q.QueryContext(ctx, g.dialect.Rebind(`SELECT id, from_node_id, to_node_id, COALESCE(edge_type, '') FROM `+table+
				` WHERE id IN (`+inList(len(chunk))+`)`), anyArgs(chunk)...)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var r ChangeRef
				if err := rows.Scan(&r.ID, &r.From, &r.To, &r.Type); err != nil {
					return err
				}
				out[r.ID] = r
			}
			return rows.Err()
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// nodeLabels reads the content of the given nodes, clipped: the live node, and
// for one since deleted the last version the history kept.
func (g *GraphStore) nodeLabels(ctx context.Context, q rowQuerier, want map[string]struct{}) (map[string]string, error) {
	out := make(map[string]string, len(want))
	for _, table := range []string{"graph_nodes", "graph_node_history"} {
		var missing []string
		for id := range want {
			if _, ok := out[id]; !ok {
				missing = append(missing, id)
			}
		}
		err := forChunks(missing, func(chunk []string) error {
			rows, err := q.QueryContext(ctx, g.dialect.Rebind(`SELECT id, COALESCE(content, '') FROM `+table+
				` WHERE id IN (`+inList(len(chunk))+`)`), anyArgs(chunk)...)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id, content string
				if err := rows.Scan(&id, &content); err != nil {
					return err
				}
				if content = strings.TrimSpace(content); content != "" {
					out[id] = clipRunes(content, labelClip)
				}
			}
			return rows.Err()
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
