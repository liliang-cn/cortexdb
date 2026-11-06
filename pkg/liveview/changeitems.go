package liveview

// Change items: the change feed's rows, read as sentences.
//
// The feed records every committed write — seq, op, id, reason, producer and
// a before/after summary of the row — and is exactly what a person watching
// the brain wants to read: which fact was added, replaced or retracted, why,
// by whom. This file turns one feed event into the [ChangeItem] the page
// renders, and decides which events are not worth showing at all.
//
// Everything here is a pure function of the event, the graph the view already
// holds and the labels the store resolved for the page in one batch
// (cortexdb.ChangesSinceResponse.Labels), so a page of 200 events costs no
// round trip per event, on a local file or a shared brain alike.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Ops a ChangeItem can carry.
const (
	OpAdded      = "added"
	OpUpdated    = "updated"
	OpSuperseded = "superseded"
	OpRetracted  = "retracted"
	OpMerged     = "merged"
	OpDeleted    = "deleted"
)

// Ref names a node the way the page shows it.
type Ref struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
}

// EdgeRef is an edge as subject, predicate and object.
type EdgeRef struct {
	Source    Ref    `json:"source"`
	Predicate string `json:"predicate"`
	Target    Ref    `json:"target"`
}

// TripleRef is an RDF triple, shortened for reading.
type TripleRef struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}

// RowRef points at another row a change refers to, with a sentence about it.
type RowRef struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// SchemaRef is the ontology schema a decision was made under.
type SchemaRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// ChangeItem is one change-feed event, readable.
type ChangeItem struct {
	// Seq is the feed's cursor: strictly increasing, with gaps.
	Seq int64 `json:"seq"`
	// At is unix milliseconds.
	At   int64  `json:"at"`
	Kind string `json:"kind"`
	Op   string `json:"op"`
	// ID is the changed row's id.
	ID       string `json:"id"`
	Producer string `json:"producer,omitempty"`
	// Reason is the history's raw reason: superseded, retracted, merged,
	// document_deleted, orphaned, pruned, or a caller's own.
	Reason  string `json:"reason,omitempty"`
	Summary string `json:"summary"`

	Node   *Ref       `json:"node,omitempty"`
	Edge   *EdgeRef   `json:"edge,omitempty"`
	Triple *TripleRef `json:"triple,omitempty"`

	// ReplacedBy is what took a superseded or merged row's place; Replaces is
	// the older row an added one closed.
	ReplacedBy *RowRef    `json:"replaced_by,omitempty"`
	Replaces   *RowRef    `json:"replaces,omitempty"`
	Schema     *SchemaRef `json:"schema,omitempty"`
	Grade      string     `json:"grade,omitempty"`
	// Entities are the node ids the change touched.
	Entities []string `json:"entities"`
}

// changeLabels resolves a node id to the text a person knows it by: the graph
// the view holds first, then what the store resolved for this page, then the
// event's own row, and as a last resort the id's last segment.
type changeLabels struct {
	nodes map[string]Node
	edges map[string]Edge
	feed  map[string]string
}

func newChangeLabels(snap Snapshot, feed map[string]string) *changeLabels {
	l := &changeLabels{
		nodes: make(map[string]Node, len(snap.Nodes)),
		edges: make(map[string]Edge, len(snap.Edges)),
		feed:  feed,
	}
	for _, n := range snap.Nodes {
		l.nodes[n.ID] = n
	}
	for _, e := range snap.Edges {
		if e.ID != "" {
			l.edges[e.ID] = e
		}
	}
	return l
}

func (l *changeLabels) label(id string) string {
	if n, ok := l.nodes[id]; ok && n.Label != "" {
		return n.Label
	}
	if s := strings.TrimSpace(l.feed[id]); s != "" {
		return ClipLabel(s)
	}
	return lastSegment(id)
}

func (l *changeLabels) ref(id string) Ref {
	r := Ref{ID: id, Label: l.label(id)}
	if n, ok := l.nodes[id]; ok {
		r.Type = n.Type
	}
	return r
}

func lastSegment(id string) string {
	if i := strings.LastIndexByte(id, ':'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// feedRow is the part of a row summary the sentences are made of. Properties
// and Metadata are whatever JSON the column held.
type feedRow struct {
	NodeType   string `json:"node_type"`
	Content    string `json:"content"`
	Properties any    `json:"properties"`
	Metadata   any    `json:"metadata"`
	Title      string `json:"title"`
	Name       string `json:"name"`
	Version    any    `json:"version"`
	Role       string `json:"role"`
	From       string `json:"from"`
	To         string `json:"to"`
	EdgeType   string `json:"edge_type"`
	Subject    string `json:"subject"`
	Predicate  string `json:"predicate"`
	Object     string `json:"object"`
}

func parseRow(raw json.RawMessage) (feedRow, bool) {
	var r feedRow
	if len(raw) == 0 || string(raw) == "null" {
		return r, false
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, false
	}
	return r, true
}

// changeOp names what happened, from the feed's op and the history's reason.
func changeOp(op, reason string) string {
	named := func() (string, bool) {
		switch reason {
		case graph.ReasonSuperseded:
			return OpSuperseded, true
		case graph.ReasonRetracted:
			return OpRetracted, true
		case graph.ReasonMerged:
			return OpMerged, true
		}
		return "", false
	}
	switch op {
	case graph.ChangeOpInsert:
		return OpAdded
	case graph.ChangeOpSupersede:
		return OpSuperseded
	case graph.ChangeOpMerge:
		return OpMerged
	case graph.ChangeOpUpdate:
		if w, ok := named(); ok {
			return w
		}
		return OpUpdated
	case graph.ChangeOpDelete:
		if w, ok := named(); ok {
			return w
		}
		return OpDeleted
	}
	return op
}

// bookkeepingOnly reports whether an update changed nothing a reader would
// call a change: the row before and after differ only in usage accounting.
//
// Decided by comparing the two summaries column by column, not by guessing
// from the kind. Every search that finds a memory rewrites its recall_count
// and last_recalled_at (cortexdb.MemoryBookkeepingKeys, kept beside the
// writer), so without this the Changes page would list a "memory updated"
// for every question anyone asked, and bury the writes that matter.
func bookkeepingOnly(ev graph.ChangeEvent) bool {
	if ev.Op != graph.ChangeOpUpdate || len(ev.Before) == 0 || len(ev.After) == 0 {
		return false
	}
	var before, after map[string]any
	if json.Unmarshal(ev.Before, &before) != nil || json.Unmarshal(ev.After, &after) != nil {
		return false
	}
	strip := func(row map[string]any) {
		for _, column := range []string{"metadata", "properties"} {
			if inner, ok := row[column].(map[string]any); ok {
				for _, k := range cortexdb.MemoryBookkeepingKeys {
					delete(inner, k)
				}
			}
		}
	}
	strip(before)
	strip(after)
	return reflect.DeepEqual(before, after)
}

// ChangeItems reads feed events as items, in the order given, leaving out the
// bookkeeping-only updates. The result is never nil.
func ChangeItems(events []graph.ChangeEvent, snap Snapshot, feedLabels map[string]string) []ChangeItem {
	l := newChangeLabels(snap, feedLabels)
	items := make([]ChangeItem, 0, len(events))
	for _, ev := range events {
		if bookkeepingOnly(ev) {
			continue
		}
		items = append(items, changeItem(ev, l))
	}
	return items
}

func changeItem(ev graph.ChangeEvent, l *changeLabels) ChangeItem {
	item := ChangeItem{
		Seq: ev.Seq, Kind: ev.Kind, Op: changeOp(ev.Op, ev.Reason), ID: ev.ID,
		Producer: ev.Producer, Reason: ev.Reason, Entities: []string{},
	}
	if !ev.At.IsZero() {
		item.At = ev.At.UnixMilli()
	}
	after, hasAfter := parseRow(ev.After)
	before, _ := parseRow(ev.Before)
	row := after
	if !hasAfter {
		row = before
	}

	switch ev.Kind {
	case graph.ChangeKindNode:
		ref := l.ref(ev.ID)
		// A node that is no longer in the graph (deleted, merged away) is read
		// from its own row.
		if _, live := l.nodes[ev.ID]; !live {
			if text := strings.TrimSpace(row.Content); text != "" {
				ref.Label = ClipLabel(text)
			}
		}
		if ref.Type == "" {
			ref.Type = row.NodeType
		}
		item.Node = &ref
		item.Entities = []string{ev.ID}
		item.Summary = nodeSummary(ref)
		item.Grade = gradeOf(row.Properties)
	case graph.ChangeKindEdge:
		src, dst := l.ref(row.From), l.ref(row.To)
		item.Edge = &EdgeRef{Source: src, Predicate: row.EdgeType, Target: dst}
		item.Entities = nonEmpty(row.From, row.To)
		item.Summary = edgeSummary(src.Label, row.EdgeType, dst.Label)
		item.Grade = gradeOf(row.Properties)
	case graph.ChangeKindTriple:
		t := TripleRef{Subject: shortTerm(row.Subject), Predicate: shortTerm(row.Predicate), Object: shortTerm(row.Object)}
		item.Triple = &t
		item.Summary = strings.Join([]string{t.Subject, t.Predicate, t.Object}, " ")
	case graph.ChangeKindMemory:
		item.Summary = fmt.Sprintf("memory %q", clipText(row.Content, 60))
	case graph.ChangeKindKnowledge:
		item.Summary = fmt.Sprintf("document %q", clipText(row.Title, 60))
	case graph.ChangeKindOntologySchema:
		item.Summary = "schema " + row.Name
		if row.Version != nil {
			item.Summary += fmt.Sprintf(" v%v", row.Version)
		}
	default:
		item.Summary = ev.Kind + " " + ev.ID
	}

	if ref := ev.SupersededBy; ref != nil && (item.Op == OpSuperseded || item.Op == OpMerged) {
		item.ReplacedBy = &RowRef{ID: ref.ID, Summary: refSummary(ev.Kind, ref, l)}
	}
	if ref := ev.Replaces; ref != nil && item.Op == OpAdded {
		item.Replaces = &RowRef{ID: ref.ID, Summary: refSummary(graph.ChangeKindEdge, ref, l)}
	}
	if ev.SchemaID != "" {
		item.Schema = &SchemaRef{ID: ev.SchemaID, Version: ev.SchemaVersion}
	}
	return item
}

func nodeSummary(r Ref) string {
	kind := r.Type
	if kind == "" {
		kind = "node"
	}
	return kind + " " + r.Label
}

func edgeSummary(src, predicate, dst string) string {
	return strings.Join(nonEmpty(src, predicate, dst), " ")
}

// refSummary says what a row a change points at is.
func refSummary(kind string, ref *graph.ChangeRef, l *changeLabels) string {
	if kind == graph.ChangeKindEdge {
		return edgeSummary(l.label(ref.From), ref.Type, l.label(ref.To))
	}
	label := ref.Content
	if strings.TrimSpace(label) == "" {
		label = l.label(ref.ID)
	} else {
		label = ClipLabel(label)
	}
	return nodeSummary(Ref{ID: ref.ID, Label: label, Type: l.ref(ref.ID).Type})
}

func gradeOf(properties any) string {
	m, _ := properties.(map[string]any)
	g, _ := m[cortexdb.KeyGrade].(string)
	return g
}

func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func clipText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// shortTerm is an IRI as its local name, and anything else unchanged: the
// namespace of a triple is noise in a sentence about it.
func shortTerm(s string) string {
	if strings.Contains(s, "://") {
		if i := strings.LastIndexAny(s, "/#"); i >= 0 && i+1 < len(s) {
			return s[i+1:]
		}
	}
	return clipText(s, 80)
}
