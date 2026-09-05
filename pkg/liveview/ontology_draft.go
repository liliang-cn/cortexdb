package liveview

// Putting the draft on the review surface.
//
// The page next door draws what a store declares against what it holds. On a
// real brain nothing is declared: 4,347 nodes under 153 node types, 8,494
// edges under 284 edge types, no ontology saved. So the page draws one half of
// a comparison, says so honestly, and there is nothing on it for a person to
// decide about — which is the correct rendering of a store nobody has
// modelled, and a dead end.
//
// pkg/cortexdb/ontology_draft.go is the way out of that dead end: it reads the
// graph and proposes a first schema, with the reasoning and the open questions
// beside it. Until now the only way to read one was raw JSON — half a megabyte
// of it on the real brain — and nobody signs a schema by reading JSON. This
// puts the draft through the same lanes, curves and overlay the saved schema
// gets, so the decision is made by looking.
//
// WHY THE SAME RENDERER, AND NOT A PAGE OF ITS OWN.
//
// Because the overlay is what makes a draft judgeable, and it is free. A
// drafted object type *is* a node type in the data — that is where it came
// from — so every box carries the count the deriver saw under it rather than
// an estimate, and "OUTSIDE THE MODEL" stops being "types nobody declared" and
// becomes exactly the set the deriver bucketed out (records, the unrecognised
// majority) or withheld (a minority spelling, a name that cannot be an API
// name, anything under the threshold). That band is the draft's own argument
// for itself, drawn in the place the reader already knows to look.
//
// The counts are not read a second time. buildOntologyReading is fed the
// deriver's own per-type findings, which are the same two aggregate scans
// DraftOntology already ran; a second pass would cost another scan and could
// disagree with the buckets printed beside it.
//
// WHAT MUST NOT HAPPEN, IN ORDER.
//
//  1. A draft reading as a saved ontology. Nobody signed this. So it gets its
//     own State words with no overlap at all with the saved four — a page
//     switching on state cannot reach a saved sentence from a draft even by
//     accident — Saved stays false, the saved-schema picker stays empty, and
//     saving is named as somebody else's act, done through ontology_save.
//  2. A server too old to draft reading as a brain with nothing in it. The
//     cluster runs a version that predates the tool and answers "unknown
//     tool"; that is a fact about the server, and rendering it as an empty
//     draft tells a reader their three hundred types do not exist.
//  3. A threshold reading as the size of the vocabulary. min=3 draws 36 object
//     types out of 124 because 124 is unreviewable, and a page that showed 36
//     without saying what it kept out would be describing a fraction while
//     looking like all of it.
//
// Nothing here writes. There is no save button and no write path: the page
// reviews, and a person saves.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// The three things this half of the page can have to say, decided in Go for
// the same reason the saved four are: an empty list of object types is
// produced by a source that cannot be asked, by a store with nothing in it,
// and by a threshold set too high, and telling a reader the wrong one of those
// is worse than telling them nothing.
//
// Deliberately disjoint from OntologyAbsent / OntologyUnreadable / ... A draft
// and a saved schema share a renderer and must never share a word: if these
// were spelled the same the page's own switch would be the place a draft
// acquired a saved schema's sentence.
const (
	// OntologyUndraftable is a source that keeps no draft hook, or a
	// derivation that failed. Today this is the shared brain: v2.93.0 has no
	// ontology_draft tool and says so. It is not an absence of vocabulary —
	// nobody could ask.
	OntologyUndraftable = "undraftable"
	// OntologyNothingToDraft is the answer, not the lack of one: the store was
	// read and holds no nodes, so there is no vocabulary to write down. A
	// store whose every type is unclassified is NOT this — that is a drafted
	// finding, and a loud one.
	OntologyNothingToDraft = "nothing-to-draft"
	// OntologyDrafted is a derivation that produced something. It says nothing
	// about whether the something is any good; that is what the decisions are
	// for.
	OntologyDrafted = "drafted"
)

// OntologyDraftDefaultMin is the threshold the page opens at.
//
// Not zero, which is the deriver's own default and the right one for a tool
// whose caller is a program. On the real shared brain zero yields 124 object
// types, 233 link types and 272 decisions: a page nobody reviews, which
// teaches a reader that this page is not for reviewing. Three yields 36 / 30 /
// 117, which is an afternoon. The number is a choice about a person's
// attention rather than about the data, so it is stated on the page and
// changed from the URL.
const OntologyDraftDefaultMin = 3

// ontologyDecisionLimit caps one group's list, never its count.
//
// Same discipline as ontologyStrayLimit: a reader is told how many merge
// candidates there are and shown as many as a page can hold, rather than shown
// a list that quietly stops. High enough that the real brain's largest group
// at the default threshold arrives whole.
const ontologyDecisionLimit = 120

// OntologyDraftQuery is what the page asks for.
//
// No Usage field, unlike OntologyQuery. Deriving a draft reads the type counts
// as part of deriving it, so there is no overlay to switch off and nothing
// saved by switching one off — the ?gap=0 bargain has nothing to buy here.
type OntologyDraftQuery struct {
	// SchemaID names the draft. Nothing is saved under it; it is what the
	// header calls the thing and what a later ontology_diff would compare.
	SchemaID string
	// MinNodes and MinEdges keep small types out of the drawing. They never
	// keep anything out of the counts.
	MinNodes int
	MinEdges int
}

// OntologyDecision is one question the data cannot answer, as the page shows
// it. A flattened copy of cortexdb.OntologyDraftDecision rather than the type
// itself, so the wire shape of this page is this package's to keep.
type OntologyDecision struct {
	Target string `json:"target"`
	// Detail is the question in a sentence; Evidence is what was observed, so
	// a reader can decide without going back to the graph. Both are the
	// deriver's own words: it knows why it asked and a view paraphrasing it
	// would be a second opinion pretending to be the first.
	Detail   string `json:"detail"`
	Evidence string `json:"evidence,omitempty"`
}

// OntologyDecisionGroup is one kind of question, with its count.
//
// Grouped because seven merge candidates and one guessed primary key are two
// very different amounts of reading and a flat list of a hundred and seventeen
// communicates neither. Title and Question travel with the group rather than
// living on the page, for the same reason the rulebook travels with the
// deriver's report: a heading like "cardinality_suspicion" is a verdict asking
// to be trusted, and the sentence that makes it actionable belongs beside it.
type OntologyDecisionGroup struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Question string `json:"question"`
	// Count is how many there are; Decisions may be shorter — see
	// ontologyDecisionLimit.
	Count     int                `json:"count"`
	Decisions []OntologyDecision `json:"decisions"`
	Truncated bool               `json:"truncated,omitempty"`
}

// OntologyDraftView is a draft as the review page reads it.
//
// It embeds OntologyReport rather than restating it, which is the whole design
// in one line: the drawing half of a draft and the drawing half of a saved
// schema are the same fields, so they are the same JSON and the same renderer,
// and everything below the embed is the half a draft has and a saved schema
// does not.
type OntologyDraftView struct {
	OntologyReport

	// Draft is always true on this payload. The page reads it before anything
	// else and never infers "this is a draft" from a state it does not
	// recognise — a payload whose draftness depended on an enum match would
	// render as a saved schema the first time somebody added a state.
	Draft bool `json:"draft"`

	// The threshold this draft was actually derived at, so the page names the
	// number a reader is being invited to disagree with rather than the one it
	// hoped was used.
	MinNodes int `json:"min_nodes"`
	MinEdges int `json:"min_edges"`
	// What the threshold kept out of the drawing. Counted off the deriver's
	// own findings — it marks them withheld:below-threshold — and never
	// recomputed, so the note under the diagram cannot disagree with the
	// report that produced it.
	PrunedNodeTypes int `json:"pruned_node_types"`
	PrunedNodes     int `json:"pruned_nodes"`
	PrunedEdgeTypes int `json:"pruned_edge_types"`
	PrunedEdges     int `json:"pruned_edges"`

	// What was read, from the deriver's own accounting: the denominator every
	// number above is a part of.
	SourceNodes     int            `json:"source_nodes"`
	SourceEdges     int            `json:"source_edges"`
	SourceNodeTypes int            `json:"source_node_types"`
	SourceEdgeTypes int            `json:"source_edge_types"`
	Buckets         map[string]int `json:"buckets,omitempty"`
	// DerivedAt is when the deriver ran, so a slow draft does not look like a
	// stuck one. Milliseconds, like OntologyReport.At.
	DerivedAt int64 `json:"derived_at,omitempty"`

	Decisions      []OntologyDecisionGroup `json:"decisions"`
	DecisionsTotal int                     `json:"decisions_total"`
	// Notes are the deriver's own words about what it did and did not do —
	// that nothing was saved, that no data type was inferred. They are the
	// caveats on the picture and belong under it, not in a JSON blob.
	Notes []string `json:"notes"`

	// Held against a schema somebody did save, when there is one. On a store
	// with no ontology these are empty and the page says nothing: a diff
	// against nothing would render as "every type added", which reads as a
	// change somebody made.
	Against        string                    `json:"against,omitempty"`
	AgainstVersion int                       `json:"against_version,omitempty"`
	Changes        []cortexdb.OntologyChange `json:"changes,omitempty"`
	ChangesTotal   int                       `json:"changes_total,omitempty"`
	Breaking       bool                      `json:"breaking,omitempty"`
}

// ontologyDecisionKinds is the order the groups are offered in, and the words
// that make each one actionable.
//
// Ordered by what it costs to get wrong rather than by how many there are. A
// merge decided badly puts two names on one thing forever; a guessed primary
// key re-identifies every object of its type when it is corrected; a
// cardinality tightened on a hunch turns a working ingest into a review queue.
// The list is exhaustive over cortexdb's decision kinds, and a kind that
// appeared without an entry here would arrive under its raw name rather than
// be dropped — see groupDecisions.
var ontologyDecisionKinds = []struct{ kind, title, question string }{
	{cortexdb.OntologyDecisionMerge, "Two spellings, not merged",
		"The deriver never merges: two spellings may be two things. For each, decide whether it is one type that needs an alias, or two types that need declaring separately."},
	{cortexdb.OntologyDecisionRename, "Cannot be an API name",
		"The name in the store cannot be declared at all. Rename it there, or decide what it should be called and add it by hand."},
	{cortexdb.OntologyDecisionOrphanLink, "A relation with nothing to connect",
		"The relation is real and cannot be drawn: a link type needs two declared ends. Classify the types at its ends, then draft again."},
	{cortexdb.OntologyDecisionLinkShape, "One relation, several shapes",
		"A link type has two ends, and these were observed between more than one pair of declared types. The draft took the commonest — declare an interface over the others, or split the relation."},
	{cortexdb.OntologyDecisionNoPrimaryKey, "Nothing here identifies anything",
		"No property on these records is both near-universal and near-distinct, so the draft declared a placeholder to make the schema valid. Decide what identifies one of these before saving."},
	{cortexdb.OntologyDecisionPrimaryKey, "A key the machine guessed",
		"A primary key is what a person promises will stay unique. Nothing in the data can confirm one, and changing it later re-identifies every object of its type."},
	{cortexdb.OntologyDecisionCardinality, "One-ness that is not evidence",
		"Every side of every relation is MANY. These are the ones that happen to look like ONE today — tighten one only if a person promises it."},
}

// unavailableDraft is the view for a source that cannot be asked, for the same
// reason unavailableOntology is a report rather than an HTTP error: a failed
// fetch leaves the page showing whatever it drew last, which after a source
// change is the previous store's draft presented as this one's.
//
// Draft stays true. The payload is still the answer to "draft this", and a
// page that read it as a saved report would put a saved schema's sentence over
// a failure to derive one.
func unavailableDraft(reason string) OntologyDraftView {
	return OntologyDraftView{
		OntologyReport: OntologyReport{
			State:       OntologyUndraftable,
			Reason:      reason,
			Schemas:     []OntologySchemaRef{},
			ObjectTypes: []OntologyObjectNode{},
			LinkTypes:   []OntologyLinkEdge{},
			Interfaces:  []OntologyInterfaceNode{},
			Usage:       emptyOntologyUsage(""),
			At:          time.Now().UnixMilli(),
		},
		Draft:     true,
		Decisions: []OntologyDecisionGroup{},
		Notes:     []string{},
	}
}

// undraftableRemote phrases a shared brain that would not draft.
//
// Two failures, and they must not be told as one. A server older than the tool
// answers CallTool with NotFound — rpcserver maps its handler's "unknown tool"
// to exactly that code — and that is a fact about the server's version, worth
// saying in those words. Anything else is a connection that broke, a timeout,
// a denial: this view then does not know whether the tool exists, and a
// sentence claiming the server lacks it would be inventing the one detail it
// failed to learn. Both say the same thing about the brain, which is nothing.
//
// Either way the page must never render this as an empty draft. A brain with
// no vocabulary and a server that would not answer are the same blank diagram
// and completely different findings, and on the cluster the second is the one
// that was true until it was upgraded.
func undraftableRemote(addr string, err error) string {
	if status.Code(err) == codes.NotFound ||
		strings.Contains(strings.ToLower(err.Error()), "unknown tool") {
		return fmt.Sprintf("%s cannot draft an ontology: ontology_draft is not a tool it has (is the server new enough? the tool arrived in v2.97.0). This says nothing about what is in the brain — %v",
			addr, err)
	}
	return fmt.Sprintf("%s could not be asked to draft an ontology, so this view cannot say whether it has ontology_draft at all. This says nothing about what is in the brain — %v",
		addr, err)
}

// groupDecisions folds the deriver's flat, pre-sorted list into the groups the
// page draws.
//
// Unknown kinds are kept under their own raw name rather than dropped. A
// decision the deriver raised and this page has no words for is still a
// question somebody has to answer, and silently swallowing it would make the
// page quietly wrong every time cortexdb learns a new one.
func groupDecisions(in []cortexdb.OntologyDraftDecision) ([]OntologyDecisionGroup, int) {
	byKind := map[string][]OntologyDecision{}
	for _, d := range in {
		byKind[d.Kind] = append(byKind[d.Kind], OntologyDecision{
			Target: d.Target, Detail: d.Detail, Evidence: d.Evidence,
		})
	}

	groups := make([]OntologyDecisionGroup, 0, len(byKind))
	add := func(kind, title, question string) {
		list, ok := byKind[kind]
		if !ok {
			return
		}
		delete(byKind, kind)
		g := OntologyDecisionGroup{Kind: kind, Title: title, Question: question, Count: len(list)}
		if len(list) > ontologyDecisionLimit {
			list, g.Truncated = list[:ontologyDecisionLimit], true
		}
		g.Decisions = list
		groups = append(groups, g)
	}
	for _, k := range ontologyDecisionKinds {
		add(k.kind, k.title, k.question)
	}
	// Whatever this build has no words for, in a stable order.
	rest := make([]string, 0, len(byKind))
	for kind := range byKind {
		rest = append(rest, kind)
	}
	sort.Strings(rest)
	for _, kind := range rest {
		add(kind, kind, "This build of the view has no description for this kind of decision; the deriver's own wording is below.")
	}

	total := 0
	for _, g := range groups {
		total += g.Count
	}
	return groups, total
}

// draftCounts recovers the per-type tallies the deriver already took.
//
// The findings carry a count per node type and per edge type — the whole of
// NodeTypeCounts and EdgeTypeCounts, since a threshold prunes the draft and
// never the report. So the overlay is built from them rather than from a
// second pair of aggregate scans: one read, one instant, and no way for the
// counts under the boxes to disagree with the buckets printed beside them.
func draftCounts(rep cortexdb.OntologyDraftReport) (map[string]int, map[string]int) {
	nodes := make(map[string]int, len(rep.NodeTypes))
	for _, f := range rep.NodeTypes {
		nodes[f.Type] = f.Nodes
	}
	edges := make(map[string]int, len(rep.EdgeTypes))
	for _, f := range rep.EdgeTypes {
		edges[f.Type] = f.Edges
	}
	return nodes, edges
}

// buildOntologyDraftView shapes a derivation into what the page draws.
//
// saved is whatever schema the store actually holds, for the diff; a store
// with none passes false and the diff half stays empty.
func buildOntologyDraftView(resp *cortexdb.OntologyDraftResponse, q OntologyDraftQuery,
	saved cortexdb.OntologySchema, hasSaved bool) OntologyDraftView {

	nodeCounts, edgeCounts := draftCounts(resp.Report)

	// The one place the draft's schema is treated as a schema. Everything the
	// saved page knows how to draw — the lanes, the per-side cardinality, the
	// implementors, the dashed unused box — is computed here, by the same
	// function, from the same shapes.
	read := buildOntologyReading(
		"counted while the draft was derived: the deriver's own read of every row in the store, not a second scan",
		nodeCounts, edgeCounts, resp.Schema)
	rep := buildOntologyReport([]cortexdb.OntologySchema{resp.Schema}, resp.Schema.SchemaID, &read)

	// And the one place it is un-treated as one. buildOntologyReport concluded
	// "saved, and in use", which is true of the schema it was handed and false
	// of everything about where this one came from.
	rep.Saved = false
	rep.Schemas = []OntologySchemaRef{}
	rep.Active = false
	rep.State = OntologyDrafted

	view := OntologyDraftView{
		OntologyReport:  rep,
		Draft:           true,
		MinNodes:        q.MinNodes,
		MinEdges:        q.MinEdges,
		SourceNodes:     resp.Report.Source.Nodes,
		SourceEdges:     resp.Report.Source.Edges,
		SourceNodeTypes: resp.Report.Source.NodeTypes,
		SourceEdgeTypes: resp.Report.Source.EdgeTypes,
		Buckets:         resp.Report.Source.Buckets,
		DerivedAt:       resp.Report.Source.DerivedAt.UnixMilli(),
		Notes:           append([]string{}, resp.Report.Notes...),
	}
	view.Decisions, view.DecisionsTotal = groupDecisions(resp.Decisions)

	// What the threshold kept out, read off the deriver's verdicts rather than
	// worked out again from the counts. The deriver is the only thing that
	// knows a type was withheld *for that reason* — a type absent from the
	// draft may equally be a minority spelling or a name that cannot be an API
	// name, and calling those pruned would blame the reader's number for the
	// deriver's judgement.
	for _, f := range resp.Report.NodeTypes {
		if f.Withheld == cortexdb.OntologyDraftRuleBelowThreshold {
			view.PrunedNodeTypes++
			view.PrunedNodes += f.Nodes
		}
	}
	for _, f := range resp.Report.EdgeTypes {
		if !f.Included && f.Rule == cortexdb.OntologyDraftRuleBelowThreshold {
			view.PrunedEdgeTypes++
			view.PrunedEdges += f.Edges
		}
	}

	// Nothing in the store is a finding about the store, not about the draft,
	// and it outranks everything above: a page saying "0 object types drafted"
	// over an empty brain has described the deriver instead of the brain.
	if resp.Report.Source.Nodes == 0 {
		view.State = OntologyNothingToDraft
	}

	if hasSaved {
		// The interesting question once a schema exists. DiffOntologySchemas
		// speaks of types added and removed, which is the vocabulary of a
		// change over time — right here, where both sides are schemas, and
		// deliberately not used by ontology.go, where one side is data.
		diff := cortexdb.DiffOntologySchemas(saved, resp.Schema)
		view.Against = saved.SchemaID
		view.AgainstVersion = saved.Version
		view.Changes = diff.Changes
		view.ChangesTotal = len(diff.Changes)
		view.Breaking = diff.HasBreakingChanges
		if len(diff.Changes) > ontologyDecisionLimit {
			view.Changes = diff.Changes[:ontologyDecisionLimit]
		}
	}
	return view
}

// draftRequest is the one place a page query becomes a deriver request, so the
// two source shapes cannot drift in what they ask for.
func draftRequest(q OntologyDraftQuery) cortexdb.OntologyDraftRequest {
	return cortexdb.OntologyDraftRequest{
		SchemaID: strings.TrimSpace(q.SchemaID),
		MinNodes: q.MinNodes,
		MinEdges: q.MinEdges,
	}
}

// localDraft derives a draft straight off an open database.
//
// It also reads the saved schemas, which the derivation itself does not need:
// a store that has one turns the page's question from "what could be declared"
// into "what is declared and what is here that it does not describe", and that
// is a diff. One extra list read against a table with a handful of rows.
func localDraft(db *cortexdb.DB) func(context.Context, OntologyDraftQuery) (OntologyDraftView, error) {
	return func(ctx context.Context, q OntologyDraftQuery) (OntologyDraftView, error) {
		resp, err := db.DraftOntology(ctx, draftRequest(q))
		if err != nil {
			return OntologyDraftView{}, fmt.Errorf("draft ontology: %w", err)
		}
		// A store that cannot be asked for its saved schemas still has a
		// drawable draft, so this degrades the diff rather than the page.
		saved, hasSaved := cortexdb.OntologySchema{}, false
		if listed, lerr := db.ListOntologySchemas(ctx, cortexdb.OntologyListRequest{}); lerr == nil {
			saved, hasSaved = pickOntologySchema(listed.Schemas, "")
		}
		return buildOntologyDraftView(resp, q, saved, hasSaved), nil
	}
}

// remoteDraft derives a draft on a shared brain.
//
// Through CallTool, the same door LoadRemote, remoteContract and
// remoteOntology already use. The failure that matters is a server older than
// the tool: it comes back as "unknown tool", and undraftableRemote turns that
// into a sentence about the server rather than about the brain. That
// distinction is the whole reason this returns an error the handler renders as
// OntologyUndraftable instead of an empty draft.
func remoteDraft(addr, token string) func(context.Context, OntologyDraftQuery) (OntologyDraftView, error) {
	return func(ctx context.Context, q OntologyDraftQuery) (OntologyDraftView, error) {
		conn, err := dial(addr, token)
		if err != nil {
			return OntologyDraftView{}, fmt.Errorf("connect to %s: %w", addr, err)
		}
		defer func() { _ = conn.Close() }()

		callCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()
		client := rpcv1.NewToolsServiceClient(conn)

		call := func(name string, args any, out any) error {
			body, merr := json.Marshal(args)
			if merr != nil {
				return merr
			}
			resp, cerr := client.CallTool(callCtx, &rpcv1.CallToolRequest{Name: name, ArgsJson: string(body)})
			if cerr != nil {
				return cerr
			}
			return json.Unmarshal([]byte(resp.GetResultJson()), out)
		}

		var drafted cortexdb.OntologyDraftResponse
		if err := call("ontology_draft", draftRequest(q), &drafted); err != nil {
			return OntologyDraftView{}, fmt.Errorf("%s", undraftableRemote(addr, err))
		}
		saved, hasSaved := cortexdb.OntologySchema{}, false
		var listed cortexdb.OntologyListResponse
		if lerr := call("ontology_list", cortexdb.OntologyListRequest{}, &listed); lerr == nil {
			saved, hasSaved = pickOntologySchema(listed.Schemas, "")
		}
		return buildOntologyDraftView(&drafted, q, saved, hasSaved), nil
	}
}
