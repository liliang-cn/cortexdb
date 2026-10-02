package graph

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
)

// karateClub is Zachary's karate club (1977): 34 members, 78 friendships,
// 1-indexed as published. Its modularity optimum is known exactly — 0.4198
// (Brandes et al. 2008, four communities) — which makes it the standard check
// that a modularity optimiser actually optimises.
var karateClub = [][2]int{
	{1, 2}, {1, 3}, {1, 4}, {1, 5}, {1, 6}, {1, 7}, {1, 8}, {1, 9}, {1, 11}, {1, 12}, {1, 13}, {1, 14}, {1, 18}, {1, 20}, {1, 22}, {1, 32},
	{2, 3}, {2, 4}, {2, 8}, {2, 14}, {2, 18}, {2, 20}, {2, 22}, {2, 31},
	{3, 4}, {3, 8}, {3, 9}, {3, 10}, {3, 14}, {3, 28}, {3, 29}, {3, 33},
	{4, 8}, {4, 13}, {4, 14},
	{5, 7}, {5, 11},
	{6, 7}, {6, 11}, {6, 17},
	{7, 17},
	{9, 31}, {9, 33}, {9, 34},
	{10, 34},
	{14, 34},
	{15, 33}, {15, 34},
	{16, 33}, {16, 34},
	{19, 33}, {19, 34},
	{20, 34},
	{21, 33}, {21, 34},
	{23, 33}, {23, 34},
	{24, 26}, {24, 28}, {24, 30}, {24, 33}, {24, 34},
	{25, 26}, {25, 28}, {25, 32},
	{26, 32},
	{27, 30}, {27, 34},
	{28, 34},
	{29, 32}, {29, 34},
	{30, 33}, {30, 34},
	{31, 33}, {31, 34},
	{32, 33}, {32, 34},
	{33, 34},
}

func karateGraph() ([]string, []WeightedEdge) {
	nodes := make([]string, 34)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("m%02d", i+1)
	}
	edges := make([]WeightedEdge, len(karateClub))
	for i, e := range karateClub {
		edges[i] = WeightedEdge{From: e[0] - 1, To: e[1] - 1, Weight: 1}
	}
	return nodes, edges
}

// lfrGraph builds an LFR-style benchmark graph (Lancichinetti, Fortunato &
// Radicchi 2008): power-law degrees (exponent 2.5) and community sizes
// (exponent 1.5), and each node keeps a fraction mu of its edges outside its
// planted community. It is the standard input for community detection
// because mu dials how hard the structure is to see — and, at high mu, how
// easily Louvain's moving phase strands the bridge of a community.
//
// Approximate, as the configuration model is: self loops and duplicate edges
// are dropped rather than rewired. Deterministic in seed.
func lfrGraph(n int, avgDegree float64, maxDegree int, mu float64, minComm, maxComm int, seed uint64) ([]string, []WeightedEdge, []int) {
	rng := rand.New(rand.NewPCG(seed, 0x6c6672))
	powerLaw := func(lo, hi, exp float64) float64 {
		// Inverse-CDF sampling of x^-exp on [lo, hi].
		a := math.Pow(lo, 1-exp)
		b := math.Pow(hi, 1-exp)
		return math.Pow(a+(b-a)*rng.Float64(), 1/(1-exp))
	}
	minDegree := math.Max(2, avgDegree/3) // mean of a 2.5 power law is ~3×min
	degree := make([]int, n)
	for i := range degree {
		degree[i] = int(math.Round(powerLaw(minDegree, float64(maxDegree), 2.5)))
	}
	var sizes []int
	total := 0
	for total < n {
		s := int(math.Round(powerLaw(float64(minComm), float64(maxComm), 1.5)))
		if total+s > n {
			s = n - total
		}
		sizes = append(sizes, s)
		total += s
	}
	if last := len(sizes) - 1; last > 0 && sizes[last] < minComm {
		sizes[last-1] += sizes[last]
		sizes = sizes[:last]
	}
	// Highest internal degree first, each into a community with room that is
	// big enough to hold its internal edges.
	order := rng.Perm(n)
	sort.SliceStable(order, func(i, j int) bool { return degree[order[i]] > degree[order[j]] })
	member := make([]int, n)
	room := append([]int(nil), sizes...)
	for _, v := range order {
		kin := int(math.Round((1 - mu) * float64(degree[v])))
		placed := false
		for tries := 0; tries < 50 && !placed; tries++ {
			c := rng.IntN(len(sizes))
			if room[c] > 0 && sizes[c]-1 >= kin {
				member[v], placed = c, true
				room[c]--
			}
		}
		for c := 0; !placed && c < len(sizes); c++ {
			if room[c] > 0 {
				member[v], placed = c, true
				room[c]--
			}
		}
	}
	seen := map[[2]int]bool{}
	var edges []WeightedEdge
	add := func(u, v int) {
		if u == v {
			return
		}
		if u > v {
			u, v = v, u
		}
		if seen[[2]int{u, v}] {
			return
		}
		seen[[2]int{u, v}] = true
		edges = append(edges, WeightedEdge{From: u, To: v, Weight: 1})
	}
	internal := make([][]int, len(sizes))
	var external []int
	for v := 0; v < n; v++ {
		kin := int(math.Round((1 - mu) * float64(degree[v])))
		if kin > sizes[member[v]]-1 {
			kin = sizes[member[v]] - 1
		}
		for i := 0; i < kin; i++ {
			internal[member[v]] = append(internal[member[v]], v)
		}
		for i := kin; i < degree[v]; i++ {
			external = append(external, v)
		}
	}
	for _, stubs := range internal {
		rng.Shuffle(len(stubs), func(i, j int) { stubs[i], stubs[j] = stubs[j], stubs[i] })
		for i := 0; i+1 < len(stubs); i += 2 {
			add(stubs[i], stubs[i+1])
		}
	}
	rng.Shuffle(len(external), func(i, j int) { external[i], external[j] = external[j], external[i] })
	for i := 0; i+1 < len(external); i += 2 {
		if member[external[i]] != member[external[i+1]] {
			add(external[i], external[i+1])
		}
	}
	nodes := make([]string, n)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("v%06d", i)
	}
	return nodes, edges, member
}

// disconnectedCommunities counts, per level, the communities whose members do
// not form one connected piece of the leaf graph — a breadth-first search
// from one member, through members only, that does not reach them all.
func disconnectedCommunities(nodes []string, edges []WeightedEdge, h *CommunityHierarchy) []int {
	index := make(map[string]int, len(nodes))
	for i, id := range nodes {
		index[id] = i
	}
	adj := make([][]int, len(nodes))
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}
	out := make([]int, len(h.Levels))
	in := make([]int, len(nodes))
	seen := make([]int, len(nodes))
	stamp := 0
	for li, level := range h.Levels {
		for _, c := range level.Communities {
			stamp++
			for _, id := range c.Nodes {
				in[index[id]] = stamp
			}
			start := index[c.Nodes[0]]
			seen[start] = stamp
			queue := []int{start}
			reached := 1
			for len(queue) > 0 {
				v := queue[0]
				queue = queue[1:]
				for _, u := range adj[v] {
					if in[u] == stamp && seen[u] != stamp {
						seen[u] = stamp
						reached++
						queue = append(queue, u)
					}
				}
			}
			if reached != len(c.Nodes) {
				out[li]++
			}
		}
	}
	return out
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

func topModularity(h *CommunityHierarchy) float64 {
	if len(h.Levels) == 0 {
		return 0
	}
	return h.Levels[len(h.Levels)-1].Modularity
}

// connectivityFixtures are LFR graphs on which this package's Louvain leaves
// at least one community disconnected (found by scanning seeds; the test
// re-checks each), so a connectivity test on them can actually fail.
func connectivityFixtures() []struct {
	name  string
	nodes []string
	edges []WeightedEdge
} {
	type fixture = struct {
		name  string
		nodes []string
		edges []WeightedEdge
	}
	var out []fixture
	for _, p := range []struct {
		n    int
		deg  float64
		mu   float64
		seed uint64
	}{
		{2000, 8, 0.5, 4},
		{2000, 8, 0.6, 3}, // disconnected at all four Louvain levels
		{5000, 4, 0.5, 2},
		{5000, 8, 0.4, 7},
	} {
		nodes, edges, _ := lfrGraph(p.n, p.deg, 50, p.mu, 10, 100, p.seed)
		out = append(out, fixture{fmt.Sprintf("lfr-n%d-k%.0f-mu%.1f-s%d", p.n, p.deg, p.mu, p.seed), nodes, edges})
	}
	return out
}

func TestEveryLeidenCommunityIsConnectedAtEveryLevel(t *testing.T) {
	louvainDisconnected, leidenDisconnected := 0, 0
	for _, f := range connectivityFixtures() {
		before := louvainDisconnected
		leiden := LeidenHierarchy(f.nodes, f.edges, HierarchyOptions{Seed: 7})
		if len(leiden.Levels) == 0 {
			t.Fatalf("%s: Leiden found no communities", f.name)
		}
		bad := disconnectedCommunities(f.nodes, f.edges, leiden)
		leidenDisconnected += sum(bad)
		if sum(bad) != 0 {
			t.Errorf("%s: Leiden left disconnected communities per level %v", f.name, bad)
		}
		louvainDisconnected += sum(disconnectedCommunities(f.nodes, f.edges, LouvainHierarchy(f.nodes, f.edges, HierarchyOptions{})))
		// The fixture has to be one Louvain gets wrong, or the assertion
		// above proves nothing about the refinement step.
		if louvainDisconnected == before {
			t.Errorf("%s: Louvain no longer leaves a community disconnected here; the fixture is vacuous", f.name)
		}
	}
	t.Logf("Louvain: %d disconnected communities across the fixtures; Leiden: %d", louvainDisconnected, leidenDisconnected)
}

func TestLeidenOnTheKarateClubReachesTheKnownModularityOptimum(t *testing.T) {
	nodes, edges := karateGraph()
	for seed := int64(0); seed < 5; seed++ {
		h := LeidenHierarchy(nodes, edges, HierarchyOptions{Seed: seed})
		q := topModularity(h)
		// 0.4198 is the proven optimum; anything above it is a modularity bug.
		if q < 0.415 || q > 0.41981 {
			t.Errorf("seed %d: top-level modularity %.4f, want within [0.415, 0.4198]", seed, q)
		}
		if bad := disconnectedCommunities(nodes, edges, h); sum(bad) != 0 {
			t.Errorf("seed %d: disconnected communities per level %v", seed, bad)
		}
		top := h.Levels[len(h.Levels)-1]
		if n := len(top.Communities); n < 3 || n > 4 {
			t.Errorf("seed %d: %d top-level communities, want 3 or 4", seed, n)
		}
	}
	louvain := topModularity(LouvainHierarchy(nodes, edges, HierarchyOptions{}))
	leiden := topModularity(LeidenHierarchy(nodes, edges, HierarchyOptions{}))
	if leiden < louvain-0.005 {
		t.Errorf("Leiden %.4f is worse than Louvain %.4f on karate", leiden, louvain)
	}
}

func TestLeidenModularityKeepsUpWithLouvainOnLFRGraphs(t *testing.T) {
	for _, mu := range []float64{0.1, 0.3, 0.5} {
		nodes, edges, _ := lfrGraph(3000, 15, 80, mu, 20, 150, 11)
		louvain := topModularity(LouvainHierarchy(nodes, edges, HierarchyOptions{}))
		leiden := topModularity(LeidenHierarchy(nodes, edges, HierarchyOptions{}))
		if leiden < louvain-0.005 {
			t.Errorf("mu=%.1f: Leiden modularity %.4f < Louvain %.4f - 0.005", mu, leiden, louvain)
		}
	}
}

func TestLeidenHierarchyRecoversPlantedLevels(t *testing.T) {
	nodes, edges := plantedHierarchy(3, 4, 8)
	h := LeidenHierarchy(nodes, edges, HierarchyOptions{})
	if len(h.Levels) < 2 {
		t.Fatalf("expected at least two levels, got %d", len(h.Levels))
	}
	if got := len(h.Levels[0].Communities); got != 12 {
		t.Fatalf("level 0: expected 12 clique communities, got %d", got)
	}
	if got := len(h.Levels[len(h.Levels)-1].Communities); got != 3 {
		t.Fatalf("top level: expected 3 groups, got %d", got)
	}
	for li := 0; li+1 < len(h.Levels); li++ {
		for _, c := range h.Levels[li].Communities {
			parent := h.Levels[li+1].Communities[c.Parent]
			members := map[string]bool{}
			for _, n := range parent.Nodes {
				members[n] = true
			}
			for _, n := range c.Nodes {
				if !members[n] {
					t.Fatalf("level %d community %d is not inside its parent", li, c.ID)
				}
			}
		}
	}
}

func TestTheSameSeedGivesTheSameLeidenHierarchy(t *testing.T) {
	nodes, edges, _ := lfrGraph(1500, 10, 50, 0.4, 10, 80, 5)
	a := LeidenHierarchy(nodes, edges, HierarchyOptions{Seed: 42})
	for i := 0; i < 3; i++ {
		if b := LeidenHierarchy(nodes, edges, HierarchyOptions{Seed: 42}); !reflect.DeepEqual(a, b) {
			t.Fatal("the same graph and seed gave two different hierarchies")
		}
	}
}

func TestLeidenHandlesEmptyEdgelessAndCappedGraphs(t *testing.T) {
	if h := LeidenHierarchy(nil, nil, HierarchyOptions{}); len(h.Levels) != 0 {
		t.Fatalf("empty graph: %d levels", len(h.Levels))
	}
	if h := LeidenHierarchy([]string{"a", "b"}, nil, HierarchyOptions{}); len(h.Levels) != 0 {
		t.Fatalf("edgeless graph: %d levels", len(h.Levels))
	}
	nodes, edges := plantedHierarchy(3, 4, 8)
	if h := LeidenHierarchy(nodes, edges, HierarchyOptions{MaxLevels: 1}); len(h.Levels) != 1 {
		t.Fatalf("MaxLevels 1: %d levels", len(h.Levels))
	}
	// Two components that are each a clique: two communities, never one.
	two := []WeightedEdge{{0, 1, 1}, {1, 2, 1}, {0, 2, 1}, {3, 4, 1}, {4, 5, 1}, {3, 5, 1}}
	h := LeidenHierarchy([]string{"a", "b", "c", "d", "e", "f"}, two, HierarchyOptions{})
	if top := h.Levels[len(h.Levels)-1]; len(top.Communities) != 2 {
		t.Fatalf("two triangles: %d top communities, want 2", len(top.Communities))
	}
}

func TestTheAlgorithmOptionSelectsLouvainOrLeiden(t *testing.T) {
	nodes, edges, _ := lfrGraph(2000, 12, 60, 0.5, 10, 100, 1)
	louvain := CommunityHierarchyOf(nodes, edges, HierarchyOptions{Algorithm: CommunityAlgorithmLouvain})
	if !reflect.DeepEqual(louvain, LouvainHierarchy(nodes, edges, HierarchyOptions{})) {
		t.Fatal("Algorithm louvain did not run Louvain")
	}
	if !reflect.DeepEqual(CommunityHierarchyOf(nodes, edges, HierarchyOptions{}), LeidenHierarchy(nodes, edges, HierarchyOptions{})) {
		t.Fatal("the default did not run Leiden")
	}
}

func TestHierarchicalCommunitiesAgreeAcrossBackendsAndAreConnected(t *testing.T) {
	nodes, edges := karateGraph()
	var results []*CommunityHierarchy
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			for _, id := range nodes {
				if err := b.store.UpsertNode(ctx, &GraphNode{ID: id, NodeType: "member", Vector: []float32{1, 0, 0, 0}}); err != nil {
					t.Fatalf("UpsertNode: %v", err)
				}
			}
			for i, e := range edges {
				if err := b.store.UpsertEdge(ctx, &GraphEdge{ID: fmt.Sprintf("e%02d", i), FromNodeID: nodes[e.From], ToNodeID: nodes[e.To], EdgeType: "knows", Weight: 1}); err != nil {
					t.Fatalf("UpsertEdge: %v", err)
				}
			}
			h, err := b.store.HierarchicalCommunities(ctx, HierarchyOptions{})
			if err != nil {
				t.Fatalf("HierarchicalCommunities: %v", err)
			}
			if bad := disconnectedCommunities(nodes, edges, h); sum(bad) != 0 {
				t.Fatalf("disconnected communities per level %v", bad)
			}
			if q := topModularity(h); q < 0.415 {
				t.Fatalf("modularity %.4f", q)
			}
			results = append(results, h)
		})
	}
	if len(results) == 2 && !reflect.DeepEqual(results[0], results[1]) {
		t.Fatal("SQLite and PostgreSQL gave different hierarchies for the same graph")
	}
}

func TestCommunityDetectionSplitsTwoTrianglesAtTheirWeakLink(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	for _, id := range []string{"A", "B", "C", "D", "E", "F", "lonely"} {
		if err := g.UpsertNode(ctx, &GraphNode{ID: id, Vector: []float32{1, 0, 0}}); err != nil {
			t.Fatal(err)
		}
	}
	for i, e := range [][2]string{{"A", "B"}, {"B", "C"}, {"A", "C"}, {"D", "E"}, {"E", "F"}, {"D", "F"}, {"C", "D"}} {
		w := 1.0
		if e[0] == "C" {
			w = 0.1
		}
		if err := g.UpsertEdge(ctx, &GraphEdge{ID: fmt.Sprintf("e%d", i), FromNodeID: e[0], ToNodeID: e[1], Weight: w}); err != nil {
			t.Fatal(err)
		}
	}
	communities, err := g.CommunityDetection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range communities {
		sorted := append([]string(nil), c.Nodes...)
		sort.Strings(sorted)
		got = append(got, fmt.Sprint(sorted))
	}
	want := []string{"[A B C]", "[D E F]", "[lonely]"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("communities %v, want %v", got, want)
	}
}

func BenchmarkCommunityHierarchy(b *testing.B) {
	for _, size := range []int{2000, 20000} {
		nodes, edges, _ := lfrGraph(size, 15, 100, 0.4, 20, 200, 3)
		for _, alg := range []CommunityAlgorithm{CommunityAlgorithmLouvain, CommunityAlgorithmLeiden} {
			b.Run(fmt.Sprintf("%s/n=%d", alg, size), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					CommunityHierarchyOf(nodes, edges, HierarchyOptions{Algorithm: alg})
				}
			})
		}
	}
}
