package graph

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Hierarchical community detection: Louvain run to completion, keeping every
// level it passes through.
//
// CommunityDetection answers with one flat partition, and a flat partition has
// one granularity: on a large graph it is either a handful of vague themes or
// hundreds of narrow ones, and a caller asking "what is this about" needs
// whichever of those fits the question. Louvain already produces both — each
// pass moves nodes between communities until modularity stops improving, then
// collapses every community into a single node and runs again on that smaller
// graph, so the passes form a dendrogram from fine to coarse. The flat
// implementation threw the dendrogram away after one pass (and scored moves by
// raw edge weight rather than modularity gain). This keeps it.
//
// Level 0 is the finest partition; each later level merges communities of the
// level below. Every community names its leaf nodes, its parent one level up,
// and its children one level down, so a caller can walk the hierarchy either
// way without recomputing it.
//
// Deterministic: nodes are visited in a fixed order (by id), neighbours in
// index order, and ties are broken toward the community the node already sits
// in and then toward the lower id. Identical graphs give identical hierarchies,
// which is what lets summaries built from them be cached and diffed.

// HierarchicalCommunity is one community at one level of the hierarchy.
type HierarchicalCommunity struct {
	Level int `json:"level"`
	ID    int `json:"id"`
	// Nodes are the leaf node ids in this community, sorted.
	Nodes []string `json:"nodes"`
	// Parent is the id of the community one level up that contains this one,
	// or -1 at the top level.
	Parent int `json:"parent"`
	// Children are the ids of the communities one level down that make up
	// this one. Empty at level 0.
	Children []int `json:"children,omitempty"`
}

// CommunityLevel is one partition of the graph.
type CommunityLevel struct {
	Level       int                     `json:"level"`
	Modularity  float64                 `json:"modularity"`
	Communities []HierarchicalCommunity `json:"communities"`
}

// CommunityHierarchy is the full dendrogram, finest level first.
type CommunityHierarchy struct {
	Levels []CommunityLevel `json:"levels"`
	// NodeCount and EdgeCount describe the graph the hierarchy was computed on,
	// after the options' filters.
	NodeCount int `json:"node_count"`
	EdgeCount int `json:"edge_count"`
}

// HierarchyOptions bounds and filters a hierarchical community run.
type HierarchyOptions struct {
	// NodeIDPrefix restricts the graph to nodes whose id starts with it, for
	// example "entity:" to leave chunk and document nodes out. Empty keeps
	// every node.
	NodeIDPrefix string
	// ExcludeEdgeTypes drops edges of these types before partitioning —
	// structural edges such as has_chunk or next say where text sits, not what
	// it is about.
	ExcludeEdgeTypes []string
	// MaxLevels caps how many levels are kept. Zero means no cap: Louvain stops
	// on its own once a pass merges nothing.
	MaxLevels int
	// Resolution scales the null-model term. 1.0 (the default when zero) is
	// standard modularity; above 1 favours smaller communities.
	Resolution float64
}

// WeightedEdge is an undirected edge between two node indexes.
type WeightedEdge struct {
	From, To int
	Weight   float64
}

// HierarchicalCommunities loads the (filtered) graph topology and runs
// hierarchical Louvain over it. Edges are treated as undirected; parallel
// edges add their weights. An empty graph is an empty hierarchy, not an error.
func (g *GraphStore) HierarchicalCommunities(ctx context.Context, opts HierarchyOptions) (*CommunityHierarchy, error) {
	nodeQuery := "SELECT id FROM graph_nodes"
	var nodeArgs []any
	if opts.NodeIDPrefix != "" {
		nodeQuery += " WHERE id LIKE ?"
		nodeArgs = append(nodeArgs, opts.NodeIDPrefix+"%")
	}
	nodeQuery += " ORDER BY id"
	rows, err := g.query(ctx, nodeQuery, nodeArgs...)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	var nodes []string
	index := make(map[string]int)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if opts.NodeIDPrefix != "" && !strings.HasPrefix(id, opts.NodeIDPrefix) {
			continue // LIKE treats _ and % as wildcards; the prefix is literal.
		}
		index[id] = len(nodes)
		nodes = append(nodes, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return &CommunityHierarchy{}, nil
	}

	excluded := make(map[string]struct{}, len(opts.ExcludeEdgeTypes))
	for _, t := range opts.ExcludeEdgeTypes {
		excluded[t] = struct{}{}
	}
	edgeRows, err := g.query(ctx, "SELECT from_node_id, to_node_id, COALESCE(edge_type,''), COALESCE(weight, 1.0) FROM graph_edges ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	var edges []WeightedEdge
	for edgeRows.Next() {
		var from, to, etype string
		var weight float64
		if err := edgeRows.Scan(&from, &to, &etype, &weight); err != nil {
			_ = edgeRows.Close()
			return nil, err
		}
		if _, skip := excluded[etype]; skip {
			continue
		}
		u, ok1 := index[from]
		v, ok2 := index[to]
		if !ok1 || !ok2 {
			continue
		}
		if weight <= 0 {
			weight = 1
		}
		edges = append(edges, WeightedEdge{From: u, To: v, Weight: weight})
	}
	_ = edgeRows.Close()
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}

	h := LouvainHierarchy(nodes, edges, opts)
	return h, nil
}

// LouvainHierarchy runs hierarchical Louvain over an in-memory graph. nodes
// names each index; edges reference those indexes. It is exported so callers
// holding a topology already (tests, benchmarks, other stores) need not round
// trip it through SQL.
func LouvainHierarchy(nodes []string, edges []WeightedEdge, opts HierarchyOptions) *CommunityHierarchy {
	out := &CommunityHierarchy{NodeCount: len(nodes), EdgeCount: len(edges)}
	n := len(nodes)
	if n == 0 {
		return out
	}
	resolution := opts.Resolution
	if resolution <= 0 {
		resolution = 1.0
	}

	// Symmetric adjacency, self loops counted twice so that k_i = Σ_j A_ij and
	// 2m = Σ_i k_i hold on the original graph and on every aggregate of it.
	adj := make([]map[int]float64, n)
	for i := range adj {
		adj[i] = make(map[int]float64)
	}
	for _, e := range edges {
		adj[e.From][e.To] += e.Weight
		adj[e.To][e.From] += e.Weight
	}
	cur := newLouvainGraph(adj)

	// leafComm[i] is leaf i's community at the most recent level.
	leafComm := make([]int, n)
	for i := range leafComm {
		leafComm[i] = i
	}

	var levels []CommunityLevel
	for {
		if opts.MaxLevels > 0 && len(levels) >= opts.MaxLevels {
			break
		}
		if cur.twoM == 0 {
			break // no edges: every node is its own community and stays so
		}
		assignment, moved := cur.localMoving(resolution)
		if !moved {
			break
		}
		assignment, count := renumber(assignment)
		for i := range leafComm {
			leafComm[i] = assignment[leafComm[i]]
		}
		level := CommunityLevel{Level: len(levels), Modularity: cur.modularity(assignment, resolution)}
		members := make([][]string, count)
		for leaf, c := range leafComm {
			members[c] = append(members[c], nodes[leaf])
		}
		level.Communities = make([]HierarchicalCommunity, count)
		for c := 0; c < count; c++ {
			sort.Strings(members[c])
			level.Communities[c] = HierarchicalCommunity{Level: level.Level, ID: c, Nodes: members[c], Parent: -1}
		}
		// The previous level's communities are exactly this pass's input
		// nodes, so the assignment is the parent map.
		if len(levels) > 0 {
			prev := &levels[len(levels)-1]
			for child := range prev.Communities {
				parent := assignment[child]
				prev.Communities[child].Parent = parent
				level.Communities[parent].Children = append(level.Communities[parent].Children, child)
			}
		}
		levels = append(levels, level)
		if count == 1 {
			break
		}
		cur = cur.aggregate(assignment, count)
	}
	out.Levels = levels
	return out
}

// louvainGraph is one level's graph: adjacency as sorted slices so iteration
// order never depends on map order.
type louvainGraph struct {
	nbr    [][]int
	wt     [][]float64
	degree []float64
	twoM   float64
}

func newLouvainGraph(adj []map[int]float64) *louvainGraph {
	g := &louvainGraph{
		nbr:    make([][]int, len(adj)),
		wt:     make([][]float64, len(adj)),
		degree: make([]float64, len(adj)),
	}
	for i, m := range adj {
		keys := make([]int, 0, len(m))
		for j := range m {
			keys = append(keys, j)
		}
		sort.Ints(keys)
		g.nbr[i] = keys
		g.wt[i] = make([]float64, len(keys))
		for k, j := range keys {
			g.wt[i][k] = m[j]
			g.degree[i] += m[j]
		}
		g.twoM += g.degree[i]
	}
	return g
}

// localMoving is Louvain phase one: move each node to the neighbouring
// community with the largest modularity gain until no move improves it.
// It reports whether any node ended outside its starting singleton.
func (g *louvainGraph) localMoving(resolution float64) ([]int, bool) {
	n := len(g.nbr)
	comm := make([]int, n)
	tot := make([]float64, n)
	for i := 0; i < n; i++ {
		comm[i] = i
		tot[i] = g.degree[i]
	}
	weightTo := make([]float64, n)
	touched := make([]int, 0, 16)
	movedAny := false
	const maxSweeps = 100
	for sweep := 0; sweep < maxSweeps; sweep++ {
		moves := 0
		for i := 0; i < n; i++ {
			ki := g.degree[i]
			if ki == 0 {
				continue
			}
			own := comm[i]
			touched = touched[:0]
			for k, j := range g.nbr[i] {
				if j == i {
					continue
				}
				c := comm[j]
				if weightTo[c] == 0 {
					touched = append(touched, c)
				}
				weightTo[c] += g.wt[i][k]
			}
			tot[own] -= ki
			scale := resolution * ki / g.twoM
			best := own
			bestGain := weightTo[own] - tot[own]*scale
			for _, c := range touched {
				if c == own {
					continue
				}
				gain := weightTo[c] - tot[c]*scale
				if gain > bestGain+1e-12 || (gain > bestGain-1e-12 && c < best && best != own) {
					best, bestGain = c, gain
				}
			}
			tot[best] += ki
			for _, c := range touched {
				weightTo[c] = 0
			}
			weightTo[own] = 0
			if best != own {
				comm[i] = best
				moves++
			}
		}
		if moves == 0 {
			break
		}
		movedAny = true
	}
	if !movedAny {
		return comm, false
	}
	// A sweep can move nodes and still end with every node alone again;
	// only a partition coarser than singletons is a level.
	seen := make(map[int]struct{}, n)
	for _, c := range comm {
		seen[c] = struct{}{}
	}
	return comm, len(seen) < n
}

// modularity of an assignment on this graph.
func (g *louvainGraph) modularity(assignment []int, resolution float64) float64 {
	if g.twoM == 0 {
		return 0
	}
	in := map[int]float64{}
	tot := map[int]float64{}
	for i := range g.nbr {
		ci := assignment[i]
		tot[ci] += g.degree[i]
		for k, j := range g.nbr[i] {
			if assignment[j] == ci {
				in[ci] += g.wt[i][k]
			}
		}
	}
	q := 0.0
	for c, t := range tot {
		q += in[c]/g.twoM - resolution*(t/g.twoM)*(t/g.twoM)
	}
	return q
}

// aggregate collapses each community into one node (Louvain phase two).
func (g *louvainGraph) aggregate(assignment []int, count int) *louvainGraph {
	adj := make([]map[int]float64, count)
	for i := range adj {
		adj[i] = make(map[int]float64)
	}
	for i := range g.nbr {
		ci := assignment[i]
		for k, j := range g.nbr[i] {
			adj[ci][assignment[j]] += g.wt[i][k]
		}
	}
	return newLouvainGraph(adj)
}

// renumber maps community labels to 0..k-1 in order of first appearance.
func renumber(assignment []int) ([]int, int) {
	mapping := make(map[int]int)
	out := make([]int, len(assignment))
	for i, c := range assignment {
		id, ok := mapping[c]
		if !ok {
			id = len(mapping)
			mapping[c] = id
		}
		out[i] = id
	}
	return out, len(mapping)
}
