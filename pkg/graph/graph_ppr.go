package graph

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Personalized PageRank: the walk that starts where the question is.
//
// Global PageRank (PageRank above) answers "which nodes matter in this graph",
// which is the same answer for every question and therefore no answer to any
// one of them. Personalized PageRank changes a single thing — the walker no
// longer teleports to a uniformly random node, it teleports back to the nodes
// the question named — and that is enough to turn a popularity contest into a
// relevance score. Mass flows outward from the seeds, splits at every node in
// proportion to edge weight, and pools wherever many short paths from the
// seeds converge. A passage two hops away through a bridge entity collects
// mass from both ends of the question; a passage that merely shares a hub
// with it collects a sliver, because a hub splits what it receives a hundred
// ways. That is the whole of the HippoRAG argument for why one cheap graph
// walk competes with iterative multi-step retrieval on multi-hop questions.

// Defaults, chosen to match HippoRAG 2 where it states them and to bound the
// work everywhere else.
const (
	// DefaultPPRDamping is the probability of following an edge rather than
	// restarting at a seed. HippoRAG 2 uses 0.5 — far lower than the 0.85 of
	// web PageRank — because retrieval wants mass to stay near the question:
	// at 0.5 a node three hops out can hold at most 1/8 of what the seeds
	// hold, which is the right shape for "two or three hops, not ten".
	DefaultPPRDamping = 0.5
	// DefaultPPRTolerance is the L1 change between iterations below which the
	// vector counts as stationary. Each iteration contracts the error by the
	// damping factor, so at 0.5 this is reached in about twenty iterations.
	DefaultPPRTolerance = 1e-9
	// DefaultPPRMaxIterations stops a walk that has not converged — it cannot
	// fail to on a finite graph with damping < 1, but a bound that does not
	// rely on that is cheaper than a hang.
	DefaultPPRMaxIterations = 100
	// DefaultPPRMaxHops is how far from the seeds the subgraph is loaded. At
	// damping 0.5 a fourth hop carries at most 1/16 of the mass, and the
	// question/passage/bridge-entity/passage chain a two-hop question needs is
	// three edges long.
	DefaultPPRMaxHops = 3
	// DefaultPPRMaxNodes and DefaultPPRMaxEdges cap what one walk may load.
	// They are what makes a seeded walk safe to run on every query against a
	// graph of any size: the subgraph grows hop by hop from the seeds and
	// stops growing at the cap, rather than the query growing with the store.
	DefaultPPRMaxNodes = 20000
	DefaultPPRMaxEdges = 100000
	// DefaultPPRMaxExpandDegree is the degree above which a node reached by
	// the walk is not expanded further. A hub splits whatever reaches it
	// across all its edges, so each neighbour it would add receives a
	// vanishing share — while loading those neighbours is most of the cost
	// of a walk: on a 6k-passage corpus, three hops through "American" or
	// "Film" read tens of thousands of edge rows to move a fraction of a
	// percent of the mass.
	DefaultPPRMaxExpandDegree = 200
	// pprIDBatch is how many node ids go into one IN list while the
	// subgraph is grown, comfortably under SQLite's and PostgreSQL's
	// parameter limits.
	pprIDBatch = 400
)

// PPROptions configures PersonalizedPageRank. The zero value is HippoRAG 2's
// configuration over an undirected graph, bounded by the defaults above.
type PPROptions struct {
	// Damping is the probability of following an edge; 1 - Damping is the
	// probability of restarting at a seed. Zero means DefaultPPRDamping.
	Damping float64
	// Tolerance is the L1 convergence threshold. Zero means the default.
	Tolerance float64
	// MaxIterations bounds the power iteration. Zero means the default.
	MaxIterations int
	// MaxHops bounds how far from the seeds the subgraph is grown. Zero means
	// the default. A seed's connected component smaller than this is loaded
	// whole, and on it the result is exact: mass never leaves a component.
	MaxHops int
	// MaxNodes and MaxEdges cap the subgraph. Zero means the defaults.
	MaxNodes int
	MaxEdges int
	// MaxExpandDegree: a non-seed node with more incident edges than this is
	// kept in the walk, with the edges that led to it, but its other edges are
	// not loaded — it is a node of the subgraph, not a road out of it. Seeds
	// are always expanded: a question about a popular entity must still reach
	// what mentions it. Zero means the default; negative means no limit.
	MaxExpandDegree int
	// MaxFrontier caps how many of the nodes a hop admits are expanded at the
	// next one: those the walk is estimated to reach with the most mass, by
	// pushing each frontier node's estimate along its edges as the walk
	// would. The rest stay in the walk as nodes, not as roads out of it.
	// MaxNodes bounds what is kept but not what is read: a frontier of a few
	// thousand passages reads every one of their edges before the cap can
	// cut, and on a cold page cache that read is the whole cost of a walk.
	// Zero or negative means no cap.
	MaxFrontier int
	// EdgeTypeWeights multiplies each edge's own weight by a per-type factor,
	// so a caller can say "a mention is worth more than a next-chunk link"
	// without rewriting the graph. A type absent from the map keeps factor 1;
	// a factor <= 0 removes that type from the walk entirely, and the subgraph
	// is not grown across it either.
	EdgeTypeWeights map[string]float64
	// Directed walks edges only from_node -> to_node. The default is
	// undirected, and it has to be for retrieval: a chunk "mentions" an
	// entity, so a directed walk from a question's entity could never reach a
	// passage at all.
	Directed bool
}

// PPRResult is the stationary distribution of the seeded walk over the
// subgraph it loaded, plus enough about the run to judge it by.
type PPRResult struct {
	// Scores holds every node of the subgraph, highest mass first, ties
	// broken by node id so equal inputs always give an identical order. The
	// scores sum to 1 (up to the convergence tolerance).
	Scores []PageRankResult `json:"scores"`
	// Iterations is how many power-iteration steps ran; Converged says
	// whether the last one moved less than the tolerance.
	Iterations int  `json:"iterations"`
	Converged  bool `json:"converged"`
	// Nodes and Edges describe the subgraph the walk ran on. Truncated is set
	// when MaxNodes or MaxEdges stopped it growing before MaxHops did, which
	// means the scores are an approximation on a neighbourhood rather than
	// exact on the seeds' component.
	Nodes     int  `json:"nodes"`
	Edges     int  `json:"edges"`
	Truncated bool `json:"truncated"`
}

// Score returns the PPR mass of one node, zero when the walk never reached it.
func (r *PPRResult) Score(nodeID string) float64 {
	if r == nil {
		return 0
	}
	for _, s := range r.Scores {
		if s.NodeID == nodeID {
			return s.Score
		}
	}
	return 0
}

func (o PPROptions) withDefaults() PPROptions {
	if o.Damping <= 0 || o.Damping >= 1 {
		o.Damping = DefaultPPRDamping
	}
	if o.Tolerance <= 0 {
		o.Tolerance = DefaultPPRTolerance
	}
	if o.MaxIterations <= 0 {
		o.MaxIterations = DefaultPPRMaxIterations
	}
	if o.MaxHops <= 0 {
		o.MaxHops = DefaultPPRMaxHops
	}
	if o.MaxNodes <= 0 {
		o.MaxNodes = DefaultPPRMaxNodes
	}
	if o.MaxEdges <= 0 {
		o.MaxEdges = DefaultPPRMaxEdges
	}
	if o.MaxExpandDegree == 0 {
		o.MaxExpandDegree = DefaultPPRMaxExpandDegree
	}
	return o
}

// typeFactor is the multiplier for an edge type; <= 0 means "not walked".
func (o PPROptions) typeFactor(edgeType string) float64 {
	if o.EdgeTypeWeights == nil {
		return 1
	}
	f, ok := o.EdgeTypeWeights[edgeType]
	if !ok {
		return 1
	}
	return f
}

// pprEdge is one live edge as the walk sees it: endpoints and the weight the
// walk will use, edge weight times type factor.
type pprEdge struct {
	id       string
	from, to string
	weight   float64
}

// PersonalizedPageRank runs a seeded PageRank from the given seeds, whose
// values are relative teleport weights (they are normalised; only their ratio
// matters). Seeds that are not nodes of the graph, or carry a weight <= 0,
// are ignored; when none is left the result is empty rather than an error,
// because "the question names nothing this graph knows" is an answer.
//
// The walk runs on the subgraph grown breadth-first from the seeds, bounded by
// MaxHops, MaxNodes and MaxEdges — never on a whole-graph load. The result is
// deterministic: nodes are indexed in id order, adjacency is summed in a fixed
// order, and ties are broken by id.
func (g *GraphStore) PersonalizedPageRank(ctx context.Context, seeds map[string]float64, opts PPROptions) (*PPRResult, error) {
	opts = opts.withDefaults()

	seedIDs := make([]string, 0, len(seeds))
	for id, w := range seeds {
		if w > 0 && !math.IsInf(w, 0) && !math.IsNaN(w) && strings.TrimSpace(id) != "" {
			seedIDs = append(seedIDs, id)
		}
	}
	sort.Strings(seedIDs)
	if len(seedIDs) == 0 {
		return &PPRResult{Scores: []PageRankResult{}}, nil
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("personalized pagerank: %w", err)
	}

	// A seed must be a node: teleporting to an id with no row would park mass
	// on something no caller can look up.
	present, err := g.existingNodeIDs(ctx, seedIDs)
	if err != nil {
		return nil, fmt.Errorf("personalized pagerank: load seeds: %w", err)
	}
	kept := seedIDs[:0]
	for _, id := range seedIDs {
		if present[id] {
			kept = append(kept, id)
		}
	}
	seedIDs = kept
	if len(seedIDs) == 0 {
		return &PPRResult{Scores: []PageRankResult{}}, nil
	}

	nodeSet, edges, truncated, err := g.growPPRSubgraph(ctx, seedIDs, seeds, opts)
	if err != nil {
		return nil, fmt.Errorf("personalized pagerank: %w", err)
	}

	nodes := make([]string, 0, len(nodeSet))
	for id := range nodeSet {
		nodes = append(nodes, id)
	}
	sort.Strings(nodes)
	index := make(map[string]int, len(nodes))
	for i, id := range nodes {
		index[id] = i
	}

	// Personalization vector, normalised to sum 1.
	p := make([]float64, len(nodes))
	var seedTotal float64
	for _, id := range seedIDs {
		seedTotal += seeds[id]
	}
	for _, id := range seedIDs {
		p[index[id]] = seeds[id] / seedTotal
	}

	// Adjacency as a weighted transition structure. Parallel edges between
	// the same pair add up; self-loops are dropped because they only delay
	// the restart they stand in for.
	type arc struct {
		to     int
		weight float64
	}
	pairWeight := make(map[[2]int]float64, len(edges)*2)
	addArc := func(u, v int, w float64) {
		if u == v {
			return
		}
		pairWeight[[2]int{u, v}] += w
	}
	for _, e := range edges {
		u, okU := index[e.from]
		v, okV := index[e.to]
		if !okU || !okV {
			continue
		}
		addArc(u, v, e.weight)
		if !opts.Directed {
			addArc(v, u, e.weight)
		}
	}
	adj := make([][]arc, len(nodes))
	for pair, w := range pairWeight {
		adj[pair[0]] = append(adj[pair[0]], arc{to: pair[1], weight: w})
	}
	outWeight := make([]float64, len(nodes))
	for u := range adj {
		sort.Slice(adj[u], func(i, j int) bool { return adj[u][i].to < adj[u][j].to })
		for _, a := range adj[u] {
			outWeight[u] += a.weight
		}
	}

	// Power iteration: r' = (1-d)·p + d·(Wᵀr + dangling·p).
	// A node with no way out returns its mass to the seeds, not to the whole
	// graph: a dead end in a seeded walk is a reason to start over from the
	// question, and spreading it uniformly would reintroduce exactly the
	// popularity bias personalization exists to remove.
	d := opts.Damping
	r := append([]float64(nil), p...)
	next := make([]float64, len(nodes))
	result := &PPRResult{Nodes: len(nodes), Edges: len(edges), Truncated: truncated}
	for iter := 1; iter <= opts.MaxIterations; iter++ {
		var dangling float64
		for i := range next {
			next[i] = 0
		}
		for u, ru := range r {
			if ru == 0 {
				continue
			}
			if outWeight[u] == 0 {
				dangling += ru
				continue
			}
			share := d * ru / outWeight[u]
			for _, a := range adj[u] {
				next[a.to] += share * a.weight
			}
		}
		var diff float64
		for i := range next {
			next[i] += (1-d)*p[i] + d*dangling*p[i]
			diff += math.Abs(next[i] - r[i])
		}
		r, next = next, r
		result.Iterations = iter
		if diff < opts.Tolerance {
			result.Converged = true
			break
		}
	}

	result.Scores = make([]PageRankResult, len(nodes))
	for i, id := range nodes {
		result.Scores[i] = PageRankResult{NodeID: id, Score: r[i]}
	}
	sort.SliceStable(result.Scores, func(i, j int) bool {
		if result.Scores[i].Score != result.Scores[j].Score {
			return result.Scores[i].Score > result.Scores[j].Score
		}
		return result.Scores[i].NodeID < result.Scores[j].NodeID
	})
	return result, nil
}

// growPPRSubgraph loads the neighbourhood of the seeds hop by hop.
//
// Each hop fetches the live edges incident to the current frontier — one
// indexed lookup per batch of ids, on from_node_id and (undirected) on
// to_node_id — and admits the nodes at their other end. When admitting a
// hop's new nodes would pass MaxNodes, the ones joined to the frontier by the
// most edge weight are admitted first and the rest are left out, so a cap
// cuts the weakest links rather than whichever ids sort last. Edges whose
// far end was left out are dropped with it.
func (g *GraphStore) growPPRSubgraph(ctx context.Context, seeds []string, seedWeights map[string]float64, opts PPROptions) (map[string]struct{}, []pprEdge, bool, error) {
	// estimate is the mass a forward push credits each node with so far; it
	// only picks the frontier when MaxFrontier is set.
	estimate := make(map[string]float64, len(seeds))
	if opts.MaxFrontier > 0 {
		var total float64
		for _, id := range seeds {
			total += seedWeights[id]
		}
		for _, id := range seeds {
			estimate[id] = seedWeights[id] / total
		}
	}
	nodes := make(map[string]struct{}, len(seeds))
	for _, id := range seeds {
		nodes[id] = struct{}{}
	}
	edgeSeen := make(map[string]struct{})
	var edges []pprEdge
	truncated := false
	frontier := append([]string(nil), seeds...)

	for hop := 0; hop < opts.MaxHops && len(frontier) > 0; hop++ {
		if hop > 0 && opts.MaxExpandDegree > 0 {
			expandable, hubs, err := g.pprBelowDegree(ctx, frontier, opts)
			if err != nil {
				return nil, nil, false, err
			}
			if hubs > 0 {
				truncated = true
			}
			frontier = expandable
		}
		incident, err := g.pprIncidentEdges(ctx, frontier, opts)
		if err != nil {
			return nil, nil, false, err
		}
		if opts.MaxFrontier > 0 {
			pushPPREstimate(estimate, frontier, incident, opts)
		}
		// Candidate new nodes, with the weight that connects them.
		pull := make(map[string]float64)
		for _, e := range incident {
			for _, end := range []string{e.from, e.to} {
				if _, in := nodes[end]; !in {
					pull[end] += e.weight
				}
			}
		}
		candidates := make([]string, 0, len(pull))
		for id := range pull {
			candidates = append(candidates, id)
		}
		sort.Slice(candidates, func(i, j int) bool {
			if pull[candidates[i]] != pull[candidates[j]] {
				return pull[candidates[i]] > pull[candidates[j]]
			}
			return candidates[i] < candidates[j]
		})
		if room := opts.MaxNodes - len(nodes); len(candidates) > room {
			if room < 0 {
				room = 0
			}
			candidates = candidates[:room]
			truncated = true
		}
		next := make([]string, 0, len(candidates))
		for _, id := range candidates {
			nodes[id] = struct{}{}
			next = append(next, id)
		}
		for _, e := range incident {
			if _, dup := edgeSeen[e.id]; dup {
				continue
			}
			_, okFrom := nodes[e.from]
			_, okTo := nodes[e.to]
			if !okFrom || !okTo {
				continue
			}
			if len(edges) >= opts.MaxEdges {
				truncated = true
				break
			}
			edgeSeen[e.id] = struct{}{}
			edges = append(edges, e)
		}
		if opts.MaxFrontier > 0 && len(next) > opts.MaxFrontier {
			sort.Slice(next, func(i, j int) bool {
				if estimate[next[i]] != estimate[next[j]] {
					return estimate[next[i]] > estimate[next[j]]
				}
				return next[i] < next[j]
			})
			next = next[:opts.MaxFrontier]
			truncated = true
		}
		sort.Strings(next)
		frontier = next
	}
	return nodes, edges, truncated, nil
}

// pushPPREstimate moves each frontier node's estimated mass one step along
// the edges just loaded for it, split by edge weight and damped, as one
// iteration of the walk would. Mass that lands on a node already in the
// subgraph is kept too: a node reached twice is reached more.
func pushPPREstimate(estimate map[string]float64, frontier []string, incident []pprEdge, opts PPROptions) {
	inFrontier := make(map[string]struct{}, len(frontier))
	for _, id := range frontier {
		inFrontier[id] = struct{}{}
	}
	out := make(map[string]float64, len(frontier))
	walk := func(fn func(u, v string, w float64)) {
		for _, e := range incident {
			if _, ok := inFrontier[e.from]; ok {
				fn(e.from, e.to, e.weight)
			}
			if opts.Directed {
				continue
			}
			if _, ok := inFrontier[e.to]; ok {
				fn(e.to, e.from, e.weight)
			}
		}
	}
	walk(func(u, _ string, w float64) { out[u] += w })
	push := make(map[string]float64)
	walk(func(u, v string, w float64) {
		if u != v && out[u] > 0 {
			push[v] += opts.Damping * estimate[u] * w / out[u]
		}
	})
	for id, m := range push {
		estimate[id] += m
	}
}

// pprBelowDegree splits a frontier into the nodes small enough to expand and
// a count of the hubs left unexpanded. Degrees come from GROUP BY over the
// two edge indexes, which reads index entries only — the cheap half of what
// expanding a hub would cost.
func (g *GraphStore) pprBelowDegree(ctx context.Context, frontier []string, opts PPROptions) ([]string, int, error) {
	degree := make(map[string]int, len(frontier))
	columns := []string{"from_node_id"}
	if !opts.Directed {
		columns = append(columns, "to_node_id")
	}
	for start := 0; start < len(frontier); start += pprIDBatch {
		end := min(start+pprIDBatch, len(frontier))
		batch := frontier[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		for _, col := range columns {
			rows, err := g.query(ctx, `SELECT `+col+`, COUNT(*) FROM graph_edges WHERE `+col+` IN (`+placeholders+`) GROUP BY `+col, args...)
			if err != nil {
				return nil, 0, fmt.Errorf("count node degrees: %w", err)
			}
			for rows.Next() {
				var id string
				var n int
				if err := rows.Scan(&id, &n); err != nil {
					rows.Close()
					return nil, 0, fmt.Errorf("scan node degree: %w", err)
				}
				degree[id] += n
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, 0, err
			}
			rows.Close()
		}
	}
	out := frontier[:0:0]
	hubs := 0
	for _, id := range frontier {
		if degree[id] > opts.MaxExpandDegree {
			hubs++
			continue
		}
		out = append(out, id)
	}
	return out, hubs, nil
}

// pprIncidentEdges returns the walkable live edges touching any frontier node,
// in edge-id order so the subgraph does not depend on the database's scan
// order.
func (g *GraphStore) pprIncidentEdges(ctx context.Context, frontier []string, opts PPROptions) ([]pprEdge, error) {
	columns := []string{"from_node_id"}
	if !opts.Directed {
		columns = append(columns, "to_node_id")
	}
	seen := make(map[string]struct{})
	var out []pprEdge
	for start := 0; start < len(frontier); start += pprIDBatch {
		end := start + pprIDBatch
		if end > len(frontier) {
			end = len(frontier)
		}
		batch := frontier[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		for _, col := range columns {
			rows, err := g.query(ctx, `SELECT id, from_node_id, to_node_id, edge_type, weight FROM graph_edges WHERE `+
				col+` IN (`+placeholders+`)`, args...)
			if err != nil {
				return nil, fmt.Errorf("load incident edges: %w", err)
			}
			for rows.Next() {
				var (
					id, from, to string
					edgeType     sql.NullString
					weight       sql.NullFloat64
				)
				if err := rows.Scan(&id, &from, &to, &edgeType, &weight); err != nil {
					rows.Close()
					return nil, fmt.Errorf("scan incident edge: %w", err)
				}
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
				w := 1.0
				if weight.Valid {
					w = weight.Float64
				}
				w *= opts.typeFactor(edgeType.String)
				// A non-positive weight cannot be a transition probability;
				// such an edge is not walked rather than walked backwards.
				if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
					continue
				}
				out = append(out, pprEdge{id: id, from: from, to: to, weight: w})
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, fmt.Errorf("iterate incident edges: %w", err)
			}
			rows.Close()
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, nil
}

// existingNodeIDs reports which of ids are live nodes.
func (g *GraphStore) existingNodeIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	present := make(map[string]bool, len(ids))
	for start := 0; start < len(ids); start += pprIDBatch {
		end := start + pprIDBatch
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := g.query(ctx, `SELECT id FROM graph_nodes WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			present[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return present, nil
}

// ExistingNodeIDs reports which of the given ids are live nodes, in batches
// that stay under every backend's parameter limit. Retrieval uses it to turn
// candidate entity names into the subset the graph actually holds with one
// indexed lookup per few hundred names.
func (g *GraphStore) ExistingNodeIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	return g.existingNodeIDs(ctx, ids)
}
