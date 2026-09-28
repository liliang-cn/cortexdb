package graph

// Path retrieval and per-relation-type traversal policy.
//
// Chunk retrieval answers "which passages look like the question". A multi-hop
// question — "how is Alice connected to the Kestrel satellite?" — is answered
// by a chain of facts, and the passages that state each link rarely look like
// the question at all, while a passage that merely shares its words ("Alice
// reviewed a paper on satellites") looks exactly like it. SearchPaths walks
// the chain instead: bounded search between seed entities, every path scored
// by its length and by the relations it used, every edge still carrying the
// chunk it came from so the answer can cite its evidence link by link.
//
// RelationPolicies is the other half: one global edge weight treats
// "works_at" and "co_occurs_with" as equally good evidence, and one global
// depth lets a statistical edge drag in the neighbourhood of a neighbour.
// A policy names, per relation type, how much an edge counts and how far from
// the start it may still be followed.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// RelationPolicy is how one relation type is treated during traversal.
type RelationPolicy struct {
	// Weight multiplies a path's score for every edge of this type on it.
	// Zero means unset and counts as 1.0; a negative weight excludes the type,
	// which is how a policy says "never follow this" without a second field.
	Weight float64 `json:"weight,omitempty"`
	// MaxDepth is the farthest hop, counted from the start (1 = the edges
	// touching the start node), at which an edge of this type may still be
	// followed. Zero means no per-type cap: the traversal's own depth applies.
	MaxDepth int `json:"max_depth,omitempty"`
}

// RelationPolicies maps a relation type to its policy. The key "*" is the
// policy for every type not listed; without it an unlisted type is followed at
// weight 1.0 with no per-type depth cap, which is what traversal did before
// policies existed. A nil or empty map changes nothing.
type RelationPolicies map[string]RelationPolicy

// RelationPolicyDefaultKey is the RelationPolicies key for unlisted types.
const RelationPolicyDefaultKey = "*"

// Allow reports whether an edge of edgeType may be followed as hop number hop
// (1-based) and, if so, the weight it contributes.
func (p RelationPolicies) Allow(edgeType string, hop int) (float64, bool) {
	if len(p) == 0 {
		return 1.0, true
	}
	policy, ok := p[edgeType]
	if !ok {
		policy, ok = p[RelationPolicyDefaultKey]
	}
	if !ok {
		return 1.0, true
	}
	if policy.Weight < 0 {
		return 0, false
	}
	if policy.MaxDepth > 0 && hop > policy.MaxDepth {
		return 0, false
	}
	if policy.Weight == 0 {
		return 1.0, true
	}
	return policy.Weight, true
}

const (
	defaultPathMaxDepth      = 3
	maxPathMaxDepth          = 6
	defaultPathDecay         = 0.5
	defaultPathMaxPaths      = 10
	defaultPathMaxExpansions = 5000
)

// PathSearchOptions bounds and scores SearchPaths.
type PathSearchOptions struct {
	// MaxDepth is the longest path in edges. Default 3, capped at 6.
	MaxDepth int `json:"max_depth,omitempty"`
	// Direction is "both" (default), "out" or "in". Relations are written in
	// whichever direction the extractor chose, so "both" is the honest default
	// for a question that does not care which way a fact points.
	Direction string `json:"direction,omitempty"`
	// EdgeTypes, when set, is the only relation types a path may use.
	EdgeTypes []string `json:"edge_types,omitempty"`
	// Relations weights and depth-caps relation types. See RelationPolicies.
	Relations RelationPolicies `json:"relations,omitempty"`
	// Decay is the per-hop score factor: a path of n edges scores
	// Decay^(n-1) times the product of its relation weights. Default 0.5.
	Decay float64 `json:"decay,omitempty"`
	// UseEdgeWeights also multiplies in each edge's own stored weight.
	UseEdgeWeights bool `json:"use_edge_weights,omitempty"`
	// MaxPaths caps the returned paths, best first. Default 10.
	MaxPaths int `json:"max_paths,omitempty"`
	// MaxExpansions caps the edges examined across the whole search. It is
	// the bound that controls runtime; hitting it sets Truncated. Default 5000.
	MaxExpansions int `json:"max_expansions,omitempty"`
	// IncludeBookkeeping lets paths run through the store's own filing —
	// chunk and document nodes, has_chunk/mentions/based_on/next edges. Off by
	// default: two entities mentioned by the same chunk are "connected" through
	// it, and every such path is a co-mention, not a fact.
	IncludeBookkeeping bool `json:"include_bookkeeping,omitempty"`
}

// ScoredPath is one path SearchPaths found, oriented from From to To. Each
// edge is returned as stored, so an edge may point against the walk.
type ScoredPath struct {
	From  string       `json:"from"`
	To    string       `json:"to"`
	Nodes []*GraphNode `json:"nodes"`
	Edges []*GraphEdge `json:"edges"`
	Hops  int          `json:"hops"`
	Score float64      `json:"score"`
}

// PathSearchResult is what SearchPaths returns.
type PathSearchResult struct {
	Paths []*ScoredPath `json:"paths"`
	// Expansions is how many edges the search examined.
	Expansions int `json:"expansions"`
	// Truncated says MaxExpansions stopped the search before every path
	// within MaxDepth was enumerated. Absence of a path is then not evidence
	// that none exists.
	Truncated bool `json:"truncated,omitempty"`
}

// SearchPaths finds scored simple paths among seed nodes.
//
// With two or more seeds it returns paths connecting pairs of them; a path
// stops at the first other seed it reaches, so A–B and B–C are two paths, not
// one A–B–C. With exactly one seed it returns every simple path leaving it,
// which is the shape of a compositional question ("the city of the company
// Alice works for") whose answer is not a seed.
//
// The search is breadth-first over partial paths, so when MaxExpansions cuts
// it short the paths lost are the longest ones.
func (g *GraphStore) SearchPaths(ctx context.Context, seeds []string, opts PathSearchOptions) (*PathSearchResult, error) {
	seeds = dedupeNonEmpty(seeds)
	if len(seeds) == 0 {
		return &PathSearchResult{Paths: []*ScoredPath{}}, nil
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = defaultPathMaxDepth
	}
	if opts.MaxDepth > maxPathMaxDepth {
		opts.MaxDepth = maxPathMaxDepth
	}
	if opts.Decay <= 0 || opts.Decay > 1 {
		opts.Decay = defaultPathDecay
	}
	if opts.MaxPaths <= 0 {
		opts.MaxPaths = defaultPathMaxPaths
	}
	if opts.MaxExpansions <= 0 {
		opts.MaxExpansions = defaultPathMaxExpansions
	}
	direction := opts.Direction
	switch direction {
	case "", "both":
		direction = "both"
	case "out", "in":
	default:
		return nil, fmt.Errorf("path search: direction must be both, out or in, got %q", opts.Direction)
	}

	seedIndex := make(map[string]int, len(seeds))
	for i, s := range seeds {
		seedIndex[s] = i
	}
	open := len(seeds) == 1

	adj := newAdjacencyCache(g, direction)
	bookkeepingNodes := map[string]bool{}
	if !opts.IncludeBookkeeping {
		// Intermediate nodes of a bookkeeping type are skipped; the node
		// types are only known once loaded, so the check is done through the
		// edge types that reach them, plus a lookup for the rest below.
		for _, t := range BookkeepingNodeTypes {
			bookkeepingNodes[t] = true
		}
	}

	type partial struct {
		nodes []string
		edges []*GraphEdge
		score float64
	}
	var found []partial
	result := &PathSearchResult{}

	for _, start := range seeds {
		queue := []partial{{nodes: []string{start}, score: 1}}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			hop := len(cur.edges) + 1
			if hop > opts.MaxDepth {
				continue
			}
			tail := cur.nodes[len(cur.nodes)-1]
			edges, err := adj.edges(ctx, tail)
			if err != nil {
				return nil, err
			}
			for _, edge := range edges {
				if result.Expansions >= opts.MaxExpansions {
					result.Truncated = true
					break
				}
				result.Expansions++
				if !opts.IncludeBookkeeping && isBookkeepingEdge(edge.EdgeType) {
					continue
				}
				if len(opts.EdgeTypes) > 0 && !contains(opts.EdgeTypes, edge.EdgeType) {
					continue
				}
				weight, ok := opts.Relations.Allow(edge.EdgeType, hop)
				if !ok {
					continue
				}
				next := edge.ToNodeID
				if next == tail {
					next = edge.FromNodeID
				}
				if next == tail || containsString(cur.nodes, next) {
					continue
				}
				score := cur.score * weight
				if len(cur.edges) > 0 {
					score *= opts.Decay
				}
				if opts.UseEdgeWeights && edge.Weight > 0 {
					score *= edge.Weight
				}
				nodes := append(append(make([]string, 0, len(cur.nodes)+1), cur.nodes...), next)
				pathEdges := append(append(make([]*GraphEdge, 0, len(cur.edges)+1), cur.edges...), edge)
				p := partial{nodes: nodes, edges: pathEdges, score: score}

				if idx, isSeed := seedIndex[next]; isSeed {
					// Another seed: record once per unordered pair when the
					// walk is undirected, and do not walk through it.
					if direction != "both" || seedIndex[start] < idx {
						found = append(found, p)
					}
					continue
				}
				if open {
					found = append(found, p)
				}
				queue = append(queue, p)
			}
			if result.Truncated {
				break
			}
		}
		if result.Truncated {
			break
		}
	}

	// Drop paths through bookkeeping nodes (chunk/document) — edges alone do
	// not rule them out, since a caller may have filed a relation onto one.
	nodeIDs := map[string]struct{}{}
	for _, p := range found {
		for _, id := range p.nodes {
			nodeIDs[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(nodeIDs))
	for id := range nodeIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	nodesByID, err := g.getNodesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	paths := make([]*ScoredPath, 0, len(found))
	for _, p := range found {
		sp := &ScoredPath{
			From:  p.nodes[0],
			To:    p.nodes[len(p.nodes)-1],
			Edges: p.edges,
			Hops:  len(p.edges),
			Score: roundScore(p.score),
			Nodes: make([]*GraphNode, 0, len(p.nodes)),
		}
		skip := false
		for i, id := range p.nodes {
			node, ok := nodesByID[id]
			if !ok {
				skip = true
				break
			}
			if !opts.IncludeBookkeeping && i > 0 && bookkeepingNodes[node.NodeType] {
				skip = true
				break
			}
			sp.Nodes = append(sp.Nodes, node)
		}
		if !skip {
			paths = append(paths, sp)
		}
	}
	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].Score != paths[j].Score {
			return paths[i].Score > paths[j].Score
		}
		if paths[i].Hops != paths[j].Hops {
			return paths[i].Hops < paths[j].Hops
		}
		return pathKey(paths[i]) < pathKey(paths[j])
	})
	if len(paths) > opts.MaxPaths {
		paths = paths[:opts.MaxPaths]
	}
	result.Paths = paths
	return result, nil
}

// ScoredNeighbor is a node reached by WeightedNeighbors.
type ScoredNeighbor struct {
	NodeID string `json:"node_id"`
	// Distance is the hop count of the path that gave Score.
	Distance int `json:"distance"`
	// Score is the best over admissible paths of
	// (product of relation weights) / (distance + 1) — the inverse-distance
	// proximity HybridSearch has always used, scaled by what the path used.
	Score float64 `json:"score"`
	// RelationWeight is the product of the relation-policy weights on it.
	RelationWeight float64 `json:"relation_weight"`
	// EdgeWeight is the product of the stored edge weights along that path.
	EdgeWeight float64 `json:"edge_weight"`
}

// WeightedNeighbors walks out from startID under per-type policies and
// returns every reachable node (start excluded) with its best score, best
// first, ties broken by distance then id.
//
// The search keeps the best score per (node, depth) rather than per node:
// a node reached cheaply at depth 2 and expensively at depth 1 can only be
// continued from depth 1 through a type capped at depth 2, so both states
// matter.
func (g *GraphStore) WeightedNeighbors(ctx context.Context, startID string, maxDepth int, direction string, edgeTypes []string, relations RelationPolicies) ([]ScoredNeighbor, error) {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if direction == "" {
		direction = "both"
	}
	type state struct {
		node  string
		depth int
	}
	type value struct {
		typeWeight float64
		edgeWeight float64
	}
	best := map[state]value{{startID, 0}: {1, 1}}
	frontier := []state{{startID, 0}}
	adj := newAdjacencyCache(g, direction)
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		nextFrontier := map[state]bool{}
		for _, st := range frontier {
			cur := best[st]
			edges, err := adj.edges(ctx, st.node)
			if err != nil {
				return nil, err
			}
			for _, edge := range edges {
				if len(edgeTypes) > 0 && !contains(edgeTypes, edge.EdgeType) {
					continue
				}
				w, ok := relations.Allow(edge.EdgeType, depth+1)
				if !ok {
					continue
				}
				next := edge.ToNodeID
				if next == st.node {
					next = edge.FromNodeID
				}
				if next == startID {
					continue
				}
				ns := state{next, depth + 1}
				cand := value{typeWeight: cur.typeWeight * w, edgeWeight: cur.edgeWeight * edge.Weight}
				if prev, seen := best[ns]; !seen || cand.typeWeight > prev.typeWeight {
					best[ns] = cand
					nextFrontier[ns] = true
				}
			}
		}
		frontier = frontier[:0]
		for st := range nextFrontier {
			frontier = append(frontier, st)
		}
		sort.Slice(frontier, func(i, j int) bool { return frontier[i].node < frontier[j].node })
	}

	byNode := map[string]ScoredNeighbor{}
	for st, v := range best {
		if st.node == startID {
			continue
		}
		score := roundScore(v.typeWeight / float64(st.depth+1))
		prev, ok := byNode[st.node]
		if !ok || score > prev.Score || (score == prev.Score && st.depth < prev.Distance) {
			byNode[st.node] = ScoredNeighbor{NodeID: st.node, Distance: st.depth, Score: score, RelationWeight: v.typeWeight, EdgeWeight: v.edgeWeight}
		}
	}
	out := make([]ScoredNeighbor, 0, len(byNode))
	for _, n := range byNode {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Distance != out[j].Distance {
			return out[i].Distance < out[j].Distance
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out, nil
}

// adjacencyCache loads each node's edges once per search. A path search
// revisits the same node from many partial paths, and each visit was a query.
type adjacencyCache struct {
	g         *GraphStore
	direction string
	byNode    map[string][]*GraphEdge
}

func newAdjacencyCache(g *GraphStore, direction string) *adjacencyCache {
	return &adjacencyCache{g: g, direction: direction, byNode: map[string][]*GraphEdge{}}
}

func (a *adjacencyCache) edges(ctx context.Context, nodeID string) ([]*GraphEdge, error) {
	if edges, ok := a.byNode[nodeID]; ok {
		return edges, nil
	}
	edges, err := a.g.GetEdges(ctx, nodeID, a.direction)
	if err != nil {
		return nil, err
	}
	// Deterministic order: the store returns rows in insertion order, which
	// is stable, but sorting makes ties independent of write history.
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	a.byNode[nodeID] = edges
	return edges, nil
}

func isBookkeepingEdge(edgeType string) bool {
	if edgeType == "next" {
		return true
	}
	for _, t := range BookkeepingEdgeTypes {
		if t == edgeType {
			return true
		}
	}
	return false
}

func dedupeNonEmpty(xs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

func pathKey(p *ScoredPath) string {
	var b strings.Builder
	for _, e := range p.Edges {
		b.WriteString(e.ID)
		b.WriteByte('|')
	}
	return b.String()
}

// round keeps float noise out of reported scores.
func roundScore(x float64) float64 { return math.Round(x*1e9) / 1e9 }
