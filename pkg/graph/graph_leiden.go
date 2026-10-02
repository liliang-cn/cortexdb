package graph

import (
	"math"
	"math/rand/v2"
	"sort"
)

// Leiden community detection (Traag, Waltman & van Eck 2019, "From Louvain
// to Leiden: guaranteeing well-connected communities").
//
// Why not Louvain: Louvain's local moving phase can move a node that bridges
// two halves of its community into another community, and nothing afterwards
// ever looks at the community it left. The next aggregation collapses that
// community — now two pieces with no edge between them — into a single node,
// and from then on it can never be split again. On real graphs that is not
// rare: the paper finds up to a quarter of Louvain's communities badly
// connected and a few percent outright disconnected. A disconnected
// "community" is a summarisation bug waiting to happen — global search writes
// one report about two unrelated topics because they happened to share a
// bridge that moved away.
//
// Leiden adds a refinement phase between moving and aggregating. Each
// community the moving phase found is re-partitioned from singletons, merging
// a node only into a sub-community it has an edge to and only while both are
// well connected to the rest of their community. Aggregation then collapses
// the refined sub-communities, not the moving phase's communities, and the
// moving phase's partition only seeds where the aggregate nodes start. So a
// community that fell apart during moving reaches the next iteration as
// several aggregate nodes that can drift apart, instead of as one node that
// cannot. Leiden iterates move → refine → aggregate until a moving phase
// leaves every aggregate node alone; every aggregate node is then a refined
// sub-community, connected by construction (each merge followed an edge), so
// every community of the result is connected.
//
// The hierarchy. Plain Leiden converges to one partition — roughly what
// Louvain's top level is — and its intermediate refined partitions are not
// usable levels: refinement merges singletons almost greedily in random
// order, so they are noisy cuts that the next iteration exists to clean up.
// What a caller of HierarchicalCommunities wants is Louvain's dendrogram
// shape, fine to coarse, each level a good partition, with every community
// connected. So each level here is one constrained Leiden run:
//
//  1. a moving phase from singletons on this level's graph — Louvain's own
//     (full sweeps in id order), so a level has the granularity the Louvain
//     level had and the planted structure a caller sees does not depend on
//     the seed;
//  2. Leiden (refine → aggregate the refined partition → move, repeated to
//     convergence) with every node confined to the community step 1 put it
//     in. That repairs what step 1 got wrong inside those communities —
//     splits one that fell apart, re-merges what refinement cut too finely —
//     without collapsing the level into the coarser ones above it.
//
// The next level's graph aggregates this level's communities, as in Louvain,
// and the hierarchy stops when a moving phase merges nothing. A level-k
// community is connected in the level-k graph, whose nodes are connected sets
// of leaves joined by leaf edges, so it is connected in the original graph —
// at every level.
//
// Deterministic with a seed: the constrained moving queue's initial order and
// the refinement's visiting order and randomized merge choice all come from
// one PCG stream seeded by HierarchyOptions.Seed. Identical graph and seed give an
// identical hierarchy.

// leidenTheta is the refinement's randomness: a node joins sub-community r
// with probability proportional to exp(gain_r / theta). The paper's 0.01.
// Gains here are in edge-weight units, so with unit weights this is close to
// greedy — the randomness breaks ties and near-ties, which is what keeps
// refinement from always building sub-communities around the lowest-numbered
// node.
const leidenTheta = 0.01

// maxLeidenIterations bounds the refine/aggregate/move loop within a level.
// Leiden converges in a handful of iterations on real graphs; the cap only
// guards against a pathological input cycling. Stopping early is safe: every
// partition the loop holds is made of refined, connected sub-communities.
const maxLeidenIterations = 64

// leidenStream is the second PCG word, fixed so that the seed alone decides
// the run.
const leidenStream = 0x6c656964656e // "leiden"

// LeidenHierarchy runs hierarchical Leiden over an in-memory graph and keeps
// every level, finest first. nodes names each index; edges reference those
// indexes and are treated as undirected, parallel edges adding their weights.
// The output has the same shape as LouvainHierarchy's, so callers need not
// know which algorithm produced it.
func LeidenHierarchy(nodes []string, edges []WeightedEdge, opts HierarchyOptions) *CommunityHierarchy {
	out := &CommunityHierarchy{NodeCount: len(nodes), EdgeCount: len(edges)}
	n := len(nodes)
	if n == 0 {
		return out
	}
	resolution := opts.Resolution
	if resolution <= 0 {
		resolution = 1.0
	}
	adj := make([]map[int]float64, n)
	for i := range adj {
		adj[i] = make(map[int]float64)
	}
	for _, e := range edges {
		adj[e.From][e.To] += e.Weight
		adj[e.To][e.From] += e.Weight
	}
	cur := newLouvainGraph(adj)
	rng := rand.New(rand.NewPCG(uint64(opts.Seed), leidenStream))

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
			break
		}
		assignment, count, next := cur.leidenLevel(resolution, rng)
		if count == len(cur.nbr) {
			break // nothing merged: the previous level is the top
		}
		for i := range leafComm {
			leafComm[i] = assignment[leafComm[i]]
		}
		levels = appendLevel(levels, nodes, leafComm, assignment, count, cur.modularity(assignment, resolution))
		if count == 1 {
			break
		}
		if next == nil {
			next = cur.aggregate(assignment, count)
		}
		cur = next
	}
	out.Levels = levels
	return out
}

// leidenLevel computes one level: a moving phase from singletons fixes which
// nodes may share a community, then Leiden runs to convergence inside those
// groups. It returns the partition of this graph's nodes, labelled 0..k-1 —
// k == len(nodes) means nothing merged — and, when something did, the
// aggregate graph whose node i is community i, which the loop has built
// anyway and which is the next level's graph.
func (g *louvainGraph) leidenLevel(resolution float64, rng *rand.Rand) ([]int, int, *louvainGraph) {
	n := len(g.nbr)
	group, moved := g.localMoving(resolution)
	group, gc := renumber(group)
	if !moved || gc == n {
		return group, gc, nil
	}

	// membership maps this level's nodes to the current aggregate's nodes;
	// every aggregate node has members, so it is always labelled 0..len-1.
	membership := make([]int, n)
	for i := range membership {
		membership[i] = i
	}
	h, part, grp := g, append([]int(nil), group...), group
	for iter := 0; iter < maxLeidenIterations; iter++ {
		if iter > 0 {
			h.leidenMove(part, grp, resolution, rng)
			var pc int
			part, pc = renumber(part)
			if pc == len(h.nbr) {
				break // every aggregate node alone: converged
			}
		}
		refined, rc := renumber(h.refine(part, resolution, rng))
		if rc == len(h.nbr) {
			// Refinement merged nothing, which happens when a community is
			// worth keeping whole but no single node gains by joining any
			// other single node. Aggregating the refined partition would give
			// this same graph back and loop; aggregating part directly is
			// Louvain and can carry a disconnected community up. Its connected
			// components are the coarsest partition that is both a refinement
			// of it and connected.
			refined, rc = h.connectedComponentsWithin(part)
			if rc == len(h.nbr) {
				break
			}
		}
		for i := range membership {
			membership[i] = refined[membership[i]]
		}
		next := h.aggregate(refined, rc)
		nextPart := make([]int, rc)
		nextGrp := make([]int, rc)
		for i, r := range refined {
			nextPart[r] = part[i]
			nextGrp[r] = grp[i]
		}
		converged := sameLabels(refined, part)
		h, part, grp = next, nextPart, nextGrp
		if converged {
			// Refinement rebuilt every community whole, so each aggregate node
			// is alone in its group and the next moving phase cannot move it.
			// The common case; skipping that phase is most of the cost of a
			// level that Louvain already got right.
			break
		}
	}
	if h == g {
		assignment, count := renumber(membership)
		return assignment, count, nil
	}
	return membership, len(h.nbr), h
}

// sameLabels reports whether two partitions, both labelled in order of first
// appearance (renumber), are the same partition.
func sameLabels(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// appendLevel records one partition as a level and links it to the level
// below, whose communities are exactly the nodes the partition was computed
// over — so assignment is the parent map.
func appendLevel(levels []CommunityLevel, nodes []string, leafComm, assignment []int, count int, modularity float64) []CommunityLevel {
	level := CommunityLevel{Level: len(levels), Modularity: modularity}
	members := make([][]string, count)
	for leaf, c := range leafComm {
		members[c] = append(members[c], nodes[leaf])
	}
	level.Communities = make([]HierarchicalCommunity, count)
	for c := 0; c < count; c++ {
		sort.Strings(members[c])
		level.Communities[c] = HierarchicalCommunity{Level: level.Level, ID: c, Nodes: members[c], Parent: -1}
	}
	if len(levels) > 0 {
		prev := &levels[len(levels)-1]
		for child := range prev.Communities {
			parent := assignment[child]
			prev.Communities[child].Parent = parent
			level.Communities[parent].Children = append(level.Communities[parent].Children, child)
		}
	}
	return append(levels, level)
}

// leidenMove is Leiden's fast local moving: a queue instead of Louvain's
// repeated full sweeps. Every node starts queued (in random order); a node
// that moves re-queues only those neighbours now outside its new community,
// since they are the only ones whose best move can have changed. That visits
// far fewer nodes than sweeping until a sweep moves nothing.
//
// part is the starting partition (labels < len(part)) and is updated in place.
// Unlike Louvain here, a node may also move to an empty community, which is
// the right move when it is attached to its own community more weakly than
// the null model expects.
//
// grp, when not nil, confines each node to communities of its own group: a
// node only weighs, and only moves toward, neighbours with the same grp label
// (or an empty community). Every community of part must start inside one
// group; the moves keep it so.
func (g *louvainGraph) leidenMove(part, grp []int, resolution float64, rng *rand.Rand) {
	n := len(g.nbr)
	tot := make([]float64, n)
	size := make([]int, n)
	for i, c := range part {
		tot[c] += g.degree[i]
		size[c]++
	}
	var empty []int
	for c := n - 1; c >= 0; c-- {
		if size[c] == 0 {
			empty = append(empty, c)
		}
	}
	queue := make([]int, n)
	for i, v := range rng.Perm(n) {
		queue[i] = v
	}
	head, count := 0, n
	inQueue := make([]bool, n)
	for i := range inQueue {
		inQueue[i] = true
	}
	weightTo := make([]float64, n)
	touched := make([]int, 0, 16)
	for count > 0 {
		v := queue[head]
		head = (head + 1) % n
		count--
		inQueue[v] = false
		kv := g.degree[v]
		if kv == 0 {
			continue
		}
		own := part[v]
		touched = touched[:0]
		for k, j := range g.nbr[v] {
			if j == v || (grp != nil && grp[j] != grp[v]) {
				continue
			}
			c := part[j]
			if weightTo[c] == 0 {
				touched = append(touched, c)
			}
			weightTo[c] += g.wt[v][k]
		}
		tot[own] -= kv
		size[own]--
		scale := resolution * kv / g.twoM
		best := own
		bestGain := weightTo[own] - tot[own]*scale
		for _, c := range touched {
			if c == own {
				continue
			}
			gain := weightTo[c] - tot[c]*scale
			if gain > bestGain+1e-12 || (gain > bestGain-1e-12 && best != own && c < best) {
				best, bestGain = c, gain
			}
		}
		// An empty community gains exactly 0. If own is empty now, staying is
		// that move already.
		if size[own] > 0 && bestGain < -1e-12 && len(empty) > 0 {
			best = empty[len(empty)-1]
			empty = empty[:len(empty)-1]
		}
		tot[best] += kv
		size[best]++
		for _, c := range touched {
			weightTo[c] = 0
		}
		weightTo[own] = 0
		if best == own {
			continue
		}
		part[v] = best
		if size[own] == 0 {
			empty = append(empty, own)
		}
		for _, u := range g.nbr[v] {
			if u != v && part[u] != best && !inQueue[u] && (grp == nil || grp[u] == grp[v]) {
				queue[(head+count)%n] = u
				count++
				inQueue[u] = true
			}
		}
	}
}

// refine is Leiden's refinement phase: inside each community of part, start
// from singletons and let each node that is still a singleton join a
// sub-community of the same community — only one it has an edge to, only if
// the node and the sub-community are each well connected to the rest of the
// community (γ-connected in the paper's terms), and only if the move does not
// lower modularity. Among the moves that qualify, staying alone included, the
// node picks at random with probability ∝ exp(gain/θ).
//
// Because every merge follows an edge, every sub-community it returns is
// connected — the property the moving phase alone cannot keep.
func (g *louvainGraph) refine(part []int, resolution float64, rng *rand.Rand) []int {
	n := len(g.nbr)
	refined := make([]int, n)
	rtot := make([]float64, n)
	rsize := make([]int, n)
	ctot := make([]float64, n)
	// ext[r] is the weight between sub-community r and the rest of its
	// community: E(r, C−r).
	ext := make([]float64, n)
	for i := 0; i < n; i++ {
		refined[i] = i
		rtot[i] = g.degree[i]
		rsize[i] = 1
		ctot[part[i]] += g.degree[i]
		for k, j := range g.nbr[i] {
			if j != i && part[j] == part[i] {
				ext[i] += g.wt[i][k]
			}
		}
	}
	inv2m := resolution / g.twoM
	weightTo := make([]float64, n)
	touched := make([]int, 0, 16)
	type choice struct {
		r    int
		gain float64
	}
	var choices []choice
	for _, v := range rng.Perm(n) {
		if rsize[refined[v]] != 1 {
			continue // only nodes still alone move
		}
		c := part[v]
		kv := g.degree[v]
		if kv == 0 || ext[v] < kv*(ctot[c]-kv)*inv2m {
			continue // v is not well connected to its community
		}
		touched = touched[:0]
		for k, j := range g.nbr[v] {
			if j == v || part[j] != c {
				continue
			}
			r := refined[j]
			if weightTo[r] == 0 {
				touched = append(touched, r)
			}
			weightTo[r] += g.wt[v][k]
		}
		own := refined[v]
		choices = append(choices[:0], choice{r: own, gain: 0})
		maxGain := 0.0
		for _, r := range touched {
			if r == own {
				continue
			}
			if ext[r] < rtot[r]*(ctot[c]-rtot[r])*inv2m {
				continue // r is not well connected to its community
			}
			gain := weightTo[r] - kv*rtot[r]*inv2m
			if gain < 0 {
				continue
			}
			choices = append(choices, choice{r: r, gain: gain})
			if gain > maxGain {
				maxGain = gain
			}
		}
		chosen := own
		if len(choices) > 1 {
			total := 0.0
			for i := range choices {
				w := math.Exp((choices[i].gain - maxGain) / leidenTheta)
				choices[i].gain = w
				total += w
			}
			x := rng.Float64() * total
			chosen = choices[len(choices)-1].r
			for _, ch := range choices {
				if x < ch.gain {
					chosen = ch.r
					break
				}
				x -= ch.gain
			}
		}
		if chosen != own {
			ext[chosen] += ext[v] - 2*weightTo[chosen]
			rtot[chosen] += kv
			rsize[chosen]++
			rsize[own] = 0
			refined[v] = chosen
		}
		for _, r := range touched {
			weightTo[r] = 0
		}
	}
	return refined
}

// connectedComponentsWithin splits every community of part into its
// connected components, labelled 0..k-1 in order of each component's lowest
// node.
func (g *louvainGraph) connectedComponentsWithin(part []int) ([]int, int) {
	n := len(g.nbr)
	out := make([]int, n)
	for i := range out {
		out[i] = -1
	}
	count := 0
	stack := make([]int, 0, 16)
	for s := 0; s < n; s++ {
		if out[s] >= 0 {
			continue
		}
		out[s] = count
		stack = append(stack[:0], s)
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, u := range g.nbr[v] {
				if out[u] < 0 && part[u] == part[v] {
					out[u] = count
					stack = append(stack, u)
				}
			}
		}
		count++
	}
	return out, count
}
