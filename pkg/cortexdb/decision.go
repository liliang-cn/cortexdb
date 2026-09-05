package cortexdb

// Why an action was taken, on the same shelf as the facts it rested on.
//
// contract.go and fact_provenance.go answer "how do I know this" about a
// *fact*: which document, which chunk, which grade. Nothing answered the same
// question about an *action*. A brain could say "riskd loads rules from rulex"
// and say how sure it was, and then have nothing at all to say about the
// moment somebody read that and decided to hold the release — who decided,
// what they were looking at, what the decision replaced, and what else had
// been decided the same way.
//
// A decision is stored as a graph record rather than a side table, and that is
// the whole design:
//
//   - the decision is a node, `decision:<id>`, typed Decision, content = the
//     note, carrying the knowledge contract like everything else on the shelf,
//     so contract_tally counts it and contract_needs_attention can surface a
//     decision somebody held;
//   - its premises, its subject and what it supersedes are edges, so a chain
//     is a graph walk and expand_graph, render_graph_html, the live view and
//     SPARQL all see it without a line of new code;
//   - a decision that rests on another decision is simply a based_on edge to
//     another Decision node, which is what makes DecisionChain a walk rather
//     than a join.
//
// The one place this had to be decided rather than derived is a premise that
// is an *edge* — "we did this because Leo works at LINBIT", where the fact is
// a relation and its id names an edge. graph_edges declares
// FOREIGN KEY (to_node_id) REFERENCES graph_nodes(id), and SQLite is opened
// with foreign_keys=ON while PostgreSQL enforces it unconditionally, so an
// edge pointing at another edge's id is rejected by the database on both
// backends. Reifying the fact as a node instead would mint a node per premise
// on a shelf whose whole point is that facts are edges. So: the based_on edge
// is anchored on the premise edge's subject node — a real node, so the key
// holds and a walk from the subject still reaches the decision — and names the
// fact exactly in its own `premise_edge_id` property. DecisionChain resolves
// that back to the edge and reports the edge's grade, not the subject's.

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// DecisionNodeType is the node_type every decision carries. A graph view that
// groups by type shows the ledger without being told about it.
const DecisionNodeType = "Decision"

// DecisionIDPrefix namespaces decision node ids away from entities. Callers
// may pass an id with or without it; everything returned carries it.
const DecisionIDPrefix = "decision:"

// The three edge types a decision writes.
const (
	// DecisionEdgeBasedOn points at a premise: the node or fact the decision
	// rested on. For a fact (an edge) the target is the fact's subject node
	// and the edge names the fact in premise_edge_id — see the file comment.
	DecisionEdgeBasedOn = "based_on"
	// DecisionEdgeAbout points at what the decision was about.
	DecisionEdgeAbout = "about"
	// DecisionEdgeSupersedes points at the decision this one replaces.
	DecisionEdgeSupersedes = "supersedes"
)

// The kinds the product uses. The vocabulary is open — a caller may write its
// own word and everything here still works — but these four are what the
// ledger was built for, and a caller that uses them gets precedents that line
// up with everybody else's.
const (
	// DecisionKindLoad: data was taken in, or refused. "we loaded the March
	// extract", "we declined this feed".
	DecisionKindLoad = "load"
	// DecisionKindReview: a person looked at something and passed judgement.
	DecisionKindReview = "review"
	// DecisionKindAction: something was done in the world.
	DecisionKindAction = "action"
	// DecisionKindAssert: a claim was adopted as the house position.
	DecisionKindAssert = "assert"
)

// The decision node's own property keys, alongside the contract's `_`-prefixed
// ones. Unprefixed because they are what the record says about itself, in the
// same place a source's own attributes live.
const (
	decisionPropMarker  = "decision"
	decisionPropKind    = "kind"
	decisionPropActor   = "actor"
	decisionPropAt      = "at"
	decisionPropVerdict = "verdict"
	decisionPropSubject = "subject"
	// premiseEdgeIDProp is on the based_on edge, not the decision, and names
	// the fact edge a premise really is when the based_on edge could only be
	// anchored on its subject.
	premiseEdgeIDProp = "premise_edge_id"
)

// decisionMarker is written as the string "true", not as a JSON boolean.
//
// Precedents filters on properties through the graph's property primitives,
// which read a key as text, and the two backends disagree about what a boolean
// scalar looks like as text: SQLite's json_extract yields 1, PostgreSQL's ->>
// yields "true". A string is the same on both. node_type is still the real
// discriminator — this exists because a property query cannot filter on it.
const decisionMarker = "true"

// DecisionRecordRequest records why something was done.
type DecisionRecordRequest struct {
	// ID names the decision. Empty mints one from the actor, the note and the
	// moment. Given, it makes the write idempotent, which is what lets a
	// caller replay a transcript without doubling its ledger.
	ID string `json:"id,omitempty"`
	// Kind is the shape of the decision: load, review, action, assert, or a
	// word of the caller's own. Precedents groups by it.
	Kind string `json:"kind,omitempty"`
	// Actor is who decided. Required: a decision nobody made is a note.
	Actor string `json:"actor"`
	// Note is the decision in words. Required, for the same reason a refusal
	// needs a why — a ledger entry a reader cannot act on is noise.
	Note string `json:"note"`
	// Verdict is the outcome in the caller's own word: "hold", "ship",
	// "rejected". Never interpreted here, for the reason KeyState is not.
	Verdict string `json:"verdict,omitempty"`
	// Subject is the node the decision was about. It must exist.
	Subject string `json:"subject,omitempty"`
	// Premises are the node ids and edge ids the decision rested on. Every one
	// must exist: a decision resting on a fact that does not exist is not a
	// decision, and accepting it would produce a chain that reads as evidence
	// while pointing at nothing.
	Premises []string `json:"premises,omitempty"`
	// Supersedes is the decision this one replaces. It must exist, and it must
	// be a decision.
	Supersedes string `json:"supersedes,omitempty"`
	// At is when it was decided. Zero means now.
	At time.Time `json:"at,omitempty"`

	// The contract keys a caller may override. The defaults are the case the
	// ledger exists for — a named person signing a decision, which is
	// established by something outside any producer and so is verified — and
	// an agent recording its own decision says so by setting Producer and
	// Grade to what is true of it.
	Source   string `json:"source,omitempty"`
	Producer string `json:"producer,omitempty"`
	Grade    string `json:"grade,omitempty"`
	State    string `json:"state,omitempty"`
	// Why is required by the contract when Grade is held or refused.
	Why string `json:"why,omitempty"`
}

// defaultDecisionSource is where a decision came from when the caller does not
// say: the ledger itself. The contract requires a source and a decision has no
// document behind it — the actor is the origin, and _by already carries them.
const defaultDecisionSource = "decision-ledger"

// DecisionPremise is one thing a decision rested on, with how sure that thing
// was. The grade is the point: a chain that lists its premises without saying
// how well established each one is looks like evidence and is not.
type DecisionPremise struct {
	// ID is the node or edge id, as the caller gave it.
	ID string `json:"id"`
	// Edge says the premise is a fact — a relation — rather than a thing.
	Edge bool `json:"edge,omitempty"`
	// Type is node_type or edge_type.
	Type string `json:"type,omitempty"`
	// Content is a node's label; edges have none.
	Content string `json:"content,omitempty"`
	// From and To are a fact's ends.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Decision says this premise is itself a decision, which is what makes the
	// chain recurse.
	Decision bool `json:"decision,omitempty"`

	// Grade and Source are the premise's own contract keys. Empty means the
	// record carries none — which is an answer, and a different one from
	// "asserted".
	Grade  string `json:"grade,omitempty"`
	Source string `json:"source,omitempty"`

	// Missing says the premise was recorded and has since been deleted. It
	// cannot happen at write time — RecordDecision refuses an unknown premise
	// — so it means the shelf changed under a decision that already stands,
	// which is exactly what a reader has to be told rather than shown a gap.
	Missing bool `json:"missing,omitempty"`
}

// DecisionRecord is one ledger entry read back.
type DecisionRecord struct {
	ID      string `json:"id"`
	Kind    string `json:"kind,omitempty"`
	Actor   string `json:"actor,omitempty"`
	At      string `json:"at,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Subject string `json:"subject,omitempty"`
	Note    string `json:"note,omitempty"`

	Grade    string `json:"grade,omitempty"`
	Source   string `json:"source,omitempty"`
	Producer string `json:"producer,omitempty"`
	State    string `json:"state,omitempty"`
	Why      string `json:"why,omitempty"`

	Premises []DecisionPremise `json:"premises,omitempty"`
	// Supersedes is what this decision replaced. A list rather than a single
	// id because a decision that consolidates three earlier ones is ordinary,
	// and a field that could only hold one would make the ledger lie by
	// arithmetic.
	Supersedes []string `json:"supersedes,omitempty"`
}

// RecordDecision writes a decision and the edges that explain it.
//
// It fails closed and it fails before writing anything: an empty actor, an
// empty note, a premise that does not exist, a subject that does not exist, a
// supersedes target that is not a decision, or metadata the knowledge contract
// refuses. The order matters — every check runs against the store before the
// node is upserted — because a decision that half-wrote leaves a ledger entry
// with fewer premises than the caller believes it has, and nothing downstream
// can tell that from a decision that genuinely rested on less.
func (db *DB) RecordDecision(ctx context.Context, req DecisionRecordRequest) (DecisionRecord, error) {
	if db == nil {
		return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: nil db")
	}
	actor := strings.TrimSpace(req.Actor)
	if actor == "" {
		return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: actor is required — a decision nobody made is a note")
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: note is required — a ledger entry a reader cannot act on is noise")
	}
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: %w", err)
	}

	at := req.At
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()
	id := DecisionID(req.ID)
	if id == DecisionIDPrefix {
		id = DecisionIDPrefix + mintDecisionID(at, actor, note)
	}

	meta := map[string]string{
		KeySource:   firstNonEmpty(strings.TrimSpace(req.Source), defaultDecisionSource),
		KeyProducer: firstNonEmpty(strings.TrimSpace(req.Producer), ProducerHuman),
		KeyGrade:    firstNonEmpty(strings.TrimSpace(req.Grade), GradeVerified),
		KeyAt:       at.Format(time.RFC3339),
		KeyBy:       actor,
	}
	if s := strings.TrimSpace(req.State); s != "" {
		meta[KeyState] = s
	}
	if w := strings.TrimSpace(req.Why); w != "" {
		meta[KeyWhy] = w
	}
	if err := ValidateContract(meta); err != nil {
		return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: %w", err)
	}

	// Resolve everything the decision points at before writing any of it.
	subject := strings.TrimSpace(req.Subject)
	if subject != "" {
		if _, err := db.graph.GetNode(ctx, subject); err != nil {
			return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: subject %q is not a node in this graph: %w", subject, err)
		}
	}
	supersedes := ""
	if s := strings.TrimSpace(req.Supersedes); s != "" {
		supersedes = DecisionID(s)
		node, err := db.graph.GetNode(ctx, supersedes)
		if err != nil {
			return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: supersedes %q does not exist: %w", supersedes, err)
		}
		if node.NodeType != DecisionNodeType {
			return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: supersedes %q is a %s, not a decision", supersedes, node.NodeType)
		}
		if supersedes == id {
			return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: %q cannot supersede itself", id)
		}
	}
	premises, err := db.resolvePremises(ctx, req.Premises)
	if err != nil {
		return DecisionRecord{}, err
	}

	// Now write. The node first: an edge to a decision that does not exist yet
	// is refused by the same foreign key that shaped the premise schema.
	props := map[string]any{
		decisionPropMarker: decisionMarker,
		decisionPropActor:  actor,
		decisionPropAt:     meta[KeyAt],
	}
	if k := strings.TrimSpace(req.Kind); k != "" {
		props[decisionPropKind] = k
	}
	if v := strings.TrimSpace(req.Verdict); v != "" {
		props[decisionPropVerdict] = v
	}
	if subject != "" {
		props[decisionPropSubject] = subject
	}
	for k, v := range meta {
		props[k] = v
	}

	vector, err := db.decisionVector(ctx, note)
	if err != nil {
		return DecisionRecord{}, err
	}
	if err := db.graph.UpsertNode(ctx, &graph.GraphNode{
		ID:         id,
		Vector:     vector,
		Content:    note,
		NodeType:   DecisionNodeType,
		Properties: props,
	}); err != nil {
		return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: write node: %w", err)
	}

	// The edges carry the same contract keys as the node. An edge is an
	// assertion too — "this decision rested on that fact" is a claim the same
	// actor signed — and leaving them ungraded would make the tally report a
	// ledger far less established than it is.
	edges := make([]*graph.GraphEdge, 0, len(premises)+2)
	for _, p := range premises {
		edgeProps := decisionEdgeProps(meta)
		target := p.ID
		if p.Edge {
			target = p.From
			edgeProps[premiseEdgeIDProp] = p.ID
		}
		edges = append(edges, &graph.GraphEdge{
			ID:         decisionEdgeID(DecisionEdgeBasedOn, id, p.ID),
			FromNodeID: id,
			ToNodeID:   target,
			EdgeType:   DecisionEdgeBasedOn,
			Weight:     1,
			Properties: edgeProps,
		})
	}
	if subject != "" {
		edges = append(edges, &graph.GraphEdge{
			ID:         decisionEdgeID(DecisionEdgeAbout, id, subject),
			FromNodeID: id,
			ToNodeID:   subject,
			EdgeType:   DecisionEdgeAbout,
			Weight:     1,
			Properties: decisionEdgeProps(meta),
		})
	}
	if supersedes != "" {
		edges = append(edges, &graph.GraphEdge{
			ID:         decisionEdgeID(DecisionEdgeSupersedes, id, supersedes),
			FromNodeID: id,
			ToNodeID:   supersedes,
			EdgeType:   DecisionEdgeSupersedes,
			Weight:     1,
			Properties: decisionEdgeProps(meta),
		})
	}
	if len(edges) > 0 {
		result, err := db.graph.UpsertEdgesBatch(ctx, edges)
		if err != nil {
			return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: write edges: %w", err)
		}
		// A batch reports rejected rows in its result, not in err. Ignoring it
		// is how a decision that recorded none of its premises still answered
		// "ok" — the one failure this whole call is built to prevent.
		if err := result.Err(); err != nil {
			return DecisionRecord{}, fmt.Errorf("cortexdb: record decision: write edges: %w", err)
		}
	}

	rec := DecisionRecord{
		ID:       id,
		Kind:     strings.TrimSpace(req.Kind),
		Actor:    actor,
		At:       meta[KeyAt],
		Verdict:  strings.TrimSpace(req.Verdict),
		Subject:  subject,
		Note:     note,
		Grade:    meta[KeyGrade],
		Source:   meta[KeySource],
		Producer: meta[KeyProducer],
		State:    meta[KeyState],
		Why:      meta[KeyWhy],
		Premises: premises,
	}
	if supersedes != "" {
		rec.Supersedes = []string{supersedes}
	}
	return rec, nil
}

// resolvePremises turns the caller's ids into premises, refusing any that do
// not exist. Duplicates collapse, keeping the caller's order: a premise listed
// twice would otherwise write the same edge twice under one id and read back
// as two.
func (db *DB) resolvePremises(ctx context.Context, ids []string) ([]DecisionPremise, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(ids))
	wanted := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, fmt.Errorf("cortexdb: record decision: a premise id is empty")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		wanted = append(wanted, id)
	}

	nodes, err := db.graph.GetNodesBatch(ctx, wanted)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: record decision: read premises: %w", err)
	}
	byNode := make(map[string]*graph.GraphNode, len(nodes))
	for _, n := range nodes {
		byNode[n.ID] = n
	}
	rest := make([]string, 0, len(wanted))
	for _, id := range wanted {
		if _, ok := byNode[id]; !ok {
			rest = append(rest, id)
		}
	}
	byEdge := map[string]*graph.GraphEdge{}
	if len(rest) > 0 {
		edges, err := db.graph.GetEdgesBatch(ctx, rest)
		if err != nil {
			return nil, fmt.Errorf("cortexdb: record decision: read premises: %w", err)
		}
		for _, e := range edges {
			byEdge[e.ID] = e
		}
	}

	out := make([]DecisionPremise, 0, len(wanted))
	for _, id := range wanted {
		switch {
		case byNode[id] != nil:
			out = append(out, premiseFromNode(byNode[id]))
		case byEdge[id] != nil:
			out = append(out, premiseFromEdge(byEdge[id]))
		default:
			return nil, fmt.Errorf("cortexdb: record decision: premise %q is neither a node nor an edge in this graph — "+
				"a decision resting on a fact that does not exist is not a decision", id)
		}
	}
	return out, nil
}

func premiseFromNode(n *graph.GraphNode) DecisionPremise {
	return DecisionPremise{
		ID:       n.ID,
		Type:     n.NodeType,
		Content:  n.Content,
		Decision: n.NodeType == DecisionNodeType,
		Grade:    propString(n.Properties, KeyGrade),
		Source:   propString(n.Properties, KeySource),
	}
}

func premiseFromEdge(e *graph.GraphEdge) DecisionPremise {
	return DecisionPremise{
		ID:     e.ID,
		Edge:   true,
		Type:   e.EdgeType,
		From:   e.FromNodeID,
		To:     e.ToNodeID,
		Grade:  propString(e.Properties, KeyGrade),
		Source: propString(e.Properties, KeySource),
	}
}

// decisionEdgeProps copies the contract onto an edge the decision writes.
func decisionEdgeProps(meta map[string]string) map[string]any {
	out := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	return out
}

// decisionEdgeID is deterministic in the pair it joins, which is what makes
// re-recording the same decision an update rather than a second ledger entry.
func decisionEdgeID(edgeType, from, to string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(from))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(to))
	return fmt.Sprintf("%s:%s:%016x", edgeType, from, h.Sum64())
}

func mintDecisionID(at time.Time, actor, note string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(actor))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(note))
	return fmt.Sprintf("%d-%016x", at.UnixNano(), h.Sum64())
}

// DecisionID normalizes a decision id to its prefixed form, so a caller may
// hold either the bare id it minted or the node id it read back.
func DecisionID(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, DecisionIDPrefix) {
		return id
	}
	return DecisionIDPrefix + id
}

// decisionVector gives the decision node a vector, because graph_nodes.vector
// is NOT NULL and a node without one cannot be written.
//
// With an embedder the note is embedded, so a decision is reachable by
// similarity like anything else on the shelf. Without one it takes the same
// lexical vector every other no-embedder writer in this package uses — the
// no-embedder path is first-class here as everywhere.
func (db *DB) decisionVector(ctx context.Context, note string) ([]float32, error) {
	if db.embedder != nil {
		v, err := db.embedder.Embed(ctx, note)
		if err != nil {
			return nil, fmt.Errorf("cortexdb: record decision: embed note: %w", err)
		}
		return v, nil
	}
	dim, err := db.GraphRAGTools().lexicalVectorDim(ctx, defaultGraphRAGCollection)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: record decision: vector dim: %w", err)
	}
	return lexicalVectorForText(note, dim), nil
}

func propString(props map[string]any, key string) string {
	if props == nil {
		return ""
	}
	s, _ := props[key].(string)
	return s
}

// sortDecisionsNewestFirst orders a ledger the way a reader reads it, with the
// id as the tie-break so two decisions stamped in the same second have a
// stable order rather than whichever one the scan happened to reach first.
func sortDecisionsNewestFirst(recs []DecisionRecord) {
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].At != recs[j].At {
			return recs[i].At > recs[j].At
		}
		return recs[i].ID > recs[j].ID
	})
}
