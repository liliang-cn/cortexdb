package cortexdb

// Reading the ledger back.
//
// decision.go writes a decision as a node plus edges. Nothing here needs new
// SQL: a chain is out-edges followed from a node, and a precedent search is
// the graph's own property query. That is the payoff of storing decisions as
// graph records — the walk is the same walk expand_graph does, so it runs
// unchanged on SQLite and PostgreSQL and shows up in every existing view.

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// defaultChainDepth is how far a chain walks when the caller does not say.
// Deep enough for the shapes a ledger actually takes — a decision, what it
// superseded, what that rested on — and shallow enough that an agent asking
// idly does not pull a year of history into its context.
const defaultChainDepth = 5

// maxChainDepth caps what a caller may ask for. A chain is a graph walk and a
// graph is not a tree: without a ceiling, a caller passing a large number on a
// densely linked ledger asks for a traversal nobody budgeted for. The cycle
// guard already keeps it from running forever; this keeps it from running long.
const maxChainDepth = 32

// DecisionChain is a decision and everything it stands on.
type DecisionChain struct {
	// Root is the decision that was asked about, normalized to its node id.
	Root string `json:"root"`
	// Decisions is the root first, then every decision reachable from it
	// through supersedes and through premises that are themselves decisions,
	// in the order the walk found them — breadth first, so the ones nearest
	// the root come first and a truncated chain is truncated at the far end.
	Decisions []DecisionRecord `json:"decisions"`
	// Depth is how many hops the walk actually took.
	Depth int `json:"depth"`
	// Truncated says the depth bound stopped the walk with decisions still
	// unvisited. A chain that quietly stopped reads as a complete account of
	// why something was done, which is the one thing it must never do.
	Truncated bool `json:"truncated,omitempty"`
}

// DecisionChain walks back from a decision to what it rested on.
//
// It follows two kinds of link and nothing else: supersedes, because the
// decision this one replaced is part of why it was made, and premises that are
// themselves decisions, because that is how a chain of reasoning is recorded.
// Premises that are facts are reported on each decision with their contract
// grade and source — the chain carries how sure, not just what — but they are
// leaves: walking into a fact's own provenance is what fact_provenance is for
// and folding it in here would make one call answer two questions badly.
//
// Cycles terminate. A ledger should not contain one, but a graph anybody can
// write into eventually does — two decisions superseding each other after a
// merge, most plausibly — and a walk that hangs on it is a worse failure than
// any wrong answer.
func (db *DB) DecisionChain(ctx context.Context, id string, maxDepth int) (DecisionChain, error) {
	if db == nil {
		return DecisionChain{}, fmt.Errorf("cortexdb: decision chain: nil db")
	}
	root := DecisionID(id)
	if root == DecisionIDPrefix {
		return DecisionChain{}, fmt.Errorf("cortexdb: decision chain: decision id is required")
	}
	if maxDepth <= 0 {
		maxDepth = defaultChainDepth
	}
	if maxDepth > maxChainDepth {
		maxDepth = maxChainDepth
	}
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return DecisionChain{}, fmt.Errorf("cortexdb: decision chain: %w", err)
	}

	out := DecisionChain{Root: root}
	type step struct {
		id    string
		depth int
	}
	queue := []step{{id: root}}
	seen := map[string]bool{root: true}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		rec, err := db.decisionRecord(ctx, cur.id)
		if err != nil {
			if cur.id == root {
				return DecisionChain{}, err
			}
			// A decision reached through an edge that no longer resolves is
			// dropped rather than failing the whole chain: the caller asked
			// about the root, and one broken link should not cost them the
			// account of it.
			continue
		}
		out.Decisions = append(out.Decisions, rec)
		if cur.depth > out.Depth {
			out.Depth = cur.depth
		}

		next := make([]string, 0, len(rec.Supersedes)+len(rec.Premises))
		next = append(next, rec.Supersedes...)
		for _, p := range rec.Premises {
			if p.Decision && !p.Missing {
				next = append(next, p.ID)
			}
		}
		for _, id := range next {
			if seen[id] {
				continue
			}
			if cur.depth+1 > maxDepth {
				out.Truncated = true
				continue
			}
			seen[id] = true
			queue = append(queue, step{id: id, depth: cur.depth + 1})
		}
	}
	return out, nil
}

// decisionRecord reads one decision node and the edges that explain it.
func (db *DB) decisionRecord(ctx context.Context, id string) (DecisionRecord, error) {
	node, err := db.graph.GetNode(ctx, id)
	if err != nil {
		return DecisionRecord{}, fmt.Errorf("cortexdb: decision %q: %w", id, err)
	}
	if node.NodeType != DecisionNodeType {
		return DecisionRecord{}, fmt.Errorf("cortexdb: decision %q: node is a %s, not a decision", id, node.NodeType)
	}
	rec := decisionFromNode(node)

	edges, err := db.graph.GetEdges(ctx, id, "out")
	if err != nil {
		return DecisionRecord{}, fmt.Errorf("cortexdb: decision %q: read edges: %w", id, err)
	}
	premiseIDs := make([]string, 0, len(edges))
	premiseIsEdge := map[string]bool{}
	for _, e := range edges {
		switch e.EdgeType {
		case DecisionEdgeSupersedes:
			rec.Supersedes = append(rec.Supersedes, e.ToNodeID)
		case DecisionEdgeBasedOn:
			// The premise is the fact this edge names, when it names one, and
			// otherwise the node it points at. See the schema note in
			// decision.go: a fact is an edge, and an edge cannot be an edge's
			// endpoint on either backend.
			pid := propString(e.Properties, premiseEdgeIDProp)
			if pid != "" {
				premiseIsEdge[pid] = true
			} else {
				pid = e.ToNodeID
			}
			premiseIDs = append(premiseIDs, pid)
		}
	}
	rec.Premises, err = db.loadPremises(ctx, premiseIDs, premiseIsEdge)
	if err != nil {
		return DecisionRecord{}, fmt.Errorf("cortexdb: decision %q: %w", id, err)
	}
	return rec, nil
}

// loadPremises resolves recorded premise ids back to what they point at,
// reporting anything that has since been deleted rather than dropping it.
func (db *DB) loadPremises(ctx context.Context, ids []string, isEdge map[string]bool) ([]DecisionPremise, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	nodeIDs := make([]string, 0, len(ids))
	edgeIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if isEdge[id] {
			edgeIDs = append(edgeIDs, id)
			continue
		}
		nodeIDs = append(nodeIDs, id)
	}

	byNode := map[string]*graph.GraphNode{}
	if len(nodeIDs) > 0 {
		nodes, err := db.graph.GetNodesBatch(ctx, nodeIDs)
		if err != nil {
			return nil, fmt.Errorf("read premise nodes: %w", err)
		}
		for _, n := range nodes {
			byNode[n.ID] = n
		}
	}
	byEdge := map[string]*graph.GraphEdge{}
	if len(edgeIDs) > 0 {
		edges, err := db.graph.GetEdgesBatch(ctx, edgeIDs)
		if err != nil {
			return nil, fmt.Errorf("read premise edges: %w", err)
		}
		for _, e := range edges {
			byEdge[e.ID] = e
		}
	}

	out := make([]DecisionPremise, 0, len(ids))
	for _, id := range ids {
		switch {
		case byNode[id] != nil:
			out = append(out, premiseFromNode(byNode[id]))
		case byEdge[id] != nil:
			out = append(out, premiseFromEdge(byEdge[id]))
		default:
			out = append(out, DecisionPremise{ID: id, Edge: isEdge[id], Missing: true})
		}
	}
	return out, nil
}

// decisionFromNode reads the decision's own properties off its node. The
// premises and what it supersedes are edges and are loaded separately.
func decisionFromNode(n *graph.GraphNode) DecisionRecord {
	return DecisionRecord{
		ID:       n.ID,
		Kind:     propString(n.Properties, decisionPropKind),
		Actor:    propString(n.Properties, decisionPropActor),
		At:       propString(n.Properties, decisionPropAt),
		Verdict:  propString(n.Properties, decisionPropVerdict),
		Subject:  propString(n.Properties, decisionPropSubject),
		Note:     n.Content,
		Grade:    propString(n.Properties, KeyGrade),
		Source:   propString(n.Properties, KeySource),
		Producer: propString(n.Properties, KeyProducer),
		State:    propString(n.Properties, KeyState),
		Why:      propString(n.Properties, KeyWhy),
	}
}

// PrecedentsQuery asks what was decided this way before.
type PrecedentsQuery struct {
	// Kind narrows to decisions of the same shape.
	Kind string `json:"kind,omitempty"`
	// Subject narrows to decisions about the same node.
	Subject string `json:"subject,omitempty"`
	// Exclude leaves one decision out — the one being decided now, whose own
	// entry is not a precedent for itself.
	Exclude string `json:"exclude,omitempty"`
	// Limit caps the rows. 0 takes defaultPrecedentsLimit.
	Limit int `json:"limit,omitempty"`
}

// defaultPrecedentsLimit is what an unbounded ask gets.
const defaultPrecedentsLimit = 20

// Precedents lists earlier decisions of the same kind, or about the same
// subject, newest first.
//
// Both empty is refused rather than meaning "the whole ledger": a caller
// asking for every decision anybody ever made is not asking about a precedent,
// and on a shared brain that is other people's ledger.
func (db *DB) Precedents(ctx context.Context, q PrecedentsQuery) ([]DecisionRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: precedents: nil db")
	}
	kind := strings.TrimSpace(q.Kind)
	subject := strings.TrimSpace(q.Subject)
	if kind == "" && subject == "" {
		return nil, fmt.Errorf("cortexdb: precedents: name a kind or a subject — refusing to return the whole ledger")
	}
	where := map[string][]string{decisionPropMarker: {decisionMarker}}
	if kind != "" {
		where[decisionPropKind] = []string{kind}
	}
	if subject != "" {
		where[decisionPropSubject] = []string{subject}
	}
	exclude := ""
	if e := strings.TrimSpace(q.Exclude); e != "" {
		exclude = DecisionID(e)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPrecedentsLimit
	}
	return db.decisionsWhere(ctx, where, exclude, limit)
}

// DecisionsBy lists what one actor decided, newest first.
func (db *DB) DecisionsBy(ctx context.Context, actor string, limit int) ([]DecisionRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: decisions by: nil db")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, fmt.Errorf("cortexdb: decisions by: actor is required")
	}
	if limit <= 0 {
		limit = defaultPrecedentsLimit
	}
	return db.decisionsWhere(ctx, map[string][]string{
		decisionPropMarker: {decisionMarker},
		decisionPropActor:  {actor},
	}, "", limit)
}

// decisionsWhere runs the property query and orders the result.
//
// Deliberately without a LIMIT in the query. RecordsWithProperties caps by id,
// and a cap applied before the sort would hand back the alphabetically first
// decisions and call them the newest — a bug that looks like a working feature
// until the ledger outgrows the cap. The scan is bounded instead by how narrow
// the filter is, which is the caller's own question: every use of this asks
// about one kind, one subject or one actor.
func (db *DB) decisionsWhere(ctx context.Context, where map[string][]string, exclude string, limit int) ([]DecisionRecord, error) {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("cortexdb: decisions: %w", err)
	}
	recs, err := db.graph.RecordsWithProperties(ctx, graph.PropertyRecordQuery{
		Where: where,
		Fetch: []string{
			decisionPropKind, decisionPropActor, decisionPropAt,
			decisionPropVerdict, decisionPropSubject,
			KeyGrade, KeySource, KeyProducer, KeyState, KeyWhy,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("cortexdb: decisions: %w", err)
	}
	out := make([]DecisionRecord, 0, len(recs))
	for _, r := range recs {
		// Edges carry the decision's contract keys but never its marker, so
		// this is belt and braces — and cheap insurance against a future
		// writer that stamps one.
		if r.Edge || r.ID == exclude {
			continue
		}
		out = append(out, DecisionRecord{
			ID:       r.ID,
			Kind:     r.Properties[decisionPropKind],
			Actor:    r.Properties[decisionPropActor],
			At:       r.Properties[decisionPropAt],
			Verdict:  r.Properties[decisionPropVerdict],
			Subject:  r.Properties[decisionPropSubject],
			Note:     r.Content,
			Grade:    r.Properties[KeyGrade],
			Source:   r.Properties[KeySource],
			Producer: r.Properties[KeyProducer],
			State:    r.Properties[KeyState],
			Why:      r.Properties[KeyWhy],
		})
	}
	sortDecisionsNewestFirst(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
