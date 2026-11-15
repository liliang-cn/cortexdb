package index

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// rewrites are the three ways a store writes a vector under an id the index
// has already held: over the live node, and after a delete by either insert.
var rewrites = map[string]func(h *HNSW, id string, v []float32) error{
	"Upsert": func(h *HNSW, id string, v []float32) error { return h.Upsert(id, v) },
	"Delete+Insert": func(h *HNSW, id string, v []float32) error {
		if err := h.Delete(id); err != nil {
			return err
		}
		return h.Insert(id, v)
	},
	"Delete+InsertBatch": func(h *HNSW, id string, v []float32) error {
		if err := h.Delete(id); err != nil {
			return err
		}
		return h.InsertBatch([]struct {
			ID     string
			Vector []float32
		}{{id, v}})
	},
}

// movedGraph is a graph written by hand, so that every link is known. x sits
// among p, q, u and w, and is about to move next to r and s:
//
//   - p and q are x's neighbours, linked both ways; p also holds it on layer 1;
//   - u is near x but not linked to it;
//   - w links to x and x does not link back, the way a link is left one-way
//     when a full list is pruned.
func movedGraph() *HNSW {
	h := NewHNSW(2, 8, EuclideanDistance)
	h.Dim = 2
	put := func(id string, v []float32, layers ...[]string) {
		h.Nodes[id] = &HNSWNode{ID: id, Vector: v, Level: len(layers) - 1, Neighbors: layers}
	}
	put("x", []float32{0, 0}, []string{"p", "q"}, []string{"p"})
	put("p", []float32{0, 1}, []string{"x", "q", "r", "u"}, []string{"x"})
	put("q", []float32{2, -3}, []string{"x", "p", "w"})
	put("u", []float32{1, 1}, []string{"p"})
	put("w", []float32{-5, -5}, []string{"x"})
	put("r", []float32{100, 100}, []string{"s", "p"})
	put("s", []float32{100, 101}, []string{"r"})
	h.EntryPoint = "p"
	return h
}

func sorted(links []string) []string {
	out := append([]string(nil), links...)
	sort.Strings(out)
	return out
}

func holds(links []string, id string) bool {
	for _, to := range links {
		if to == id {
			return true
		}
	}
	return false
}

// Writing a vector under an id the index has held rewrites the lists of the
// nodes that id was linked to, and of the ones it is linked to now. It used
// to read the list of every node in the index, on every such write.
func TestHNSWRewriteTouchesOnlyTheNeighbourhood(t *testing.T) {
	for name, rewrite := range rewrites {
		t.Run(name, func(t *testing.T) {
			// The level of the node is drawn again each time, and with it
			// the layers it is placed on.
			for round := 0; round < 40; round++ {
				h := movedGraph()
				if err := rewrite(h, "x", []float32{100, 100.6}); err != nil {
					t.Fatal(err)
				}
				x := h.Nodes["x"]
				if x.Deleted || x.Vector[0] != 100 {
					t.Fatalf("x is not the live node at its new place: %+v", x)
				}
				if got := sorted(x.Neighbors[0]); fmt.Sprint(got) != "[p r s u]" {
					t.Fatalf("x links to %v on layer 0, want its four nearest [p r s u]", got)
				}
				for _, back := range []string{"p", "r", "s", "u"} {
					if !holds(h.Nodes[back].Neighbors[0], "x") {
						t.Fatalf("%s does not link back to x: %v", back, h.Nodes[back].Neighbors[0])
					}
				}
				// p held x on layer 1 too: it holds it there again only when
				// x is drawn onto that layer.
				if holds(h.Nodes["p"].Neighbors[1], "x") != (x.Level >= 1) {
					t.Fatalf("x has level %d and p's layer 1 is %v", x.Level, h.Nodes["p"].Neighbors[1])
				}
				// q was a neighbour and is not one any more.
				if holds(h.Nodes["q"].Neighbors[0], "x") {
					t.Fatalf("q still links to x, which moved away: %v", h.Nodes["q"].Neighbors[0])
				}
				// w was never in x's lists, so the rewrite did not read it.
				if got := h.Nodes["w"].Neighbors[0]; fmt.Sprint(got) != "[x]" {
					t.Fatalf("w, which x never linked to, was rewritten: %v", got)
				}
				if h.Tombstones() != 0 {
					t.Fatalf("%d tombstones left", h.Tombstones())
				}
			}
		})
	}
}

// A one-way link to the rewritten id resolves to the node at its new place.
// The search that places the node walks that link and meets the node itself,
// nearest of all: it must not be taken as its own neighbour.
func TestHNSWRewrittenNodeIsNotItsOwnNeighbour(t *testing.T) {
	for name, rewrite := range rewrites {
		t.Run(name, func(t *testing.T) {
			h := movedGraph()
			if err := rewrite(h, "x", []float32{100, 100.6}); err != nil {
				t.Fatal(err)
			}
			for layer, links := range h.Nodes["x"].Neighbors {
				if holds(links, "x") {
					t.Fatalf("x is its own neighbour on layer %d: %v", layer, links)
				}
			}
			ids, _ := h.Search([]float32{100, 100.6}, 7, 16)
			if len(ids) != 7 || ids[0] != "x" {
				t.Fatalf("expected the 7 nodes, x first, got %v", ids)
			}
		})
	}
}

// A node can hold a link on a layer its target does not have: the first node
// drawn above the entry point links to it on every layer. The target cannot
// drop such a link, and the descent that places the node again walks it into
// the node itself.
func TestHNSWRewriteUnderALinkAboveItsLevel(t *testing.T) {
	for name, rewrite := range rewrites {
		t.Run(name, func(t *testing.T) {
			for round := 0; round < 40; round++ {
				h := NewHNSW(8, 32, EuclideanDistance)
				h.Dim = 2
				h.Nodes["a"] = &HNSWNode{ID: "a", Vector: []float32{1, 0}, Neighbors: [][]string{{"b", "c"}}}
				h.Nodes["b"] = &HNSWNode{ID: "b", Vector: []float32{0, 1}, Neighbors: [][]string{{"a", "c"}}}
				h.Nodes["c"] = &HNSWNode{ID: "c", Vector: []float32{1, 1}, Level: 1, Neighbors: [][]string{{"b", "a"}, {"a"}}}
				h.EntryPoint = "c"
				if err := rewrite(h, "a", []float32{1, 0}); err != nil {
					t.Fatal(err)
				}
				if got := sorted(h.Nodes["a"].Neighbors[0]); fmt.Sprint(got) != "[b c]" {
					t.Fatalf("a links to %v on layer 0, want [b c]", got)
				}
				checkGraph(t, h)
				if ids, _ := h.Search([]float32{1, 0}, 3, 16); len(ids) != 3 || ids[0] != "a" {
					t.Fatalf("expected the 3 nodes, a first, got %v", ids)
				}
			}
		})
	}
}

// A link the neighbour does not hold, a neighbour with fewer layers and a
// neighbour that is gone are all nothing to unlink.
func TestHNSWRewriteUnlinksWhatIsThere(t *testing.T) {
	h := movedGraph()
	h.Nodes["x"].Neighbors = [][]string{{"p", "u", "gone"}, {"p", "q"}}
	if err := h.Upsert("x", []float32{100, 100.6}); err != nil {
		t.Fatal(err)
	}
	if got := sorted(h.Nodes["x"].Neighbors[0]); fmt.Sprint(got) != "[p r s u]" {
		t.Fatalf("x links to %v on layer 0, want [p r s u]", got)
	}
	if got := h.Nodes["q"].Neighbors; len(got) != 1 {
		t.Fatalf("q, a layer-0 node, has %d layers", len(got))
	}
}

// The only node of an index is its entry point, live or deleted.
func TestHNSWRewriteOfTheOnlyNode(t *testing.T) {
	for name, rewrite := range rewrites {
		t.Run(name, func(t *testing.T) {
			h := NewHNSW(8, 32, EuclideanDistance)
			if err := h.Insert("a", []float32{1, 0}); err != nil {
				t.Fatal(err)
			}
			if err := rewrite(h, "a", []float32{0, 1}); err != nil {
				t.Fatal(err)
			}
			if h.EntryPoint != "a" || h.Size() != 1 || h.Tombstones() != 0 {
				t.Fatalf("entry %q, %d live, %d tombstones", h.EntryPoint, h.Size(), h.Tombstones())
			}
			ids, dists := h.Search([]float32{0, 1}, 1, 16)
			if len(ids) != 1 || ids[0] != "a" || dists[0] != 0 {
				t.Fatalf("expected a at distance 0, got %v %v", ids, dists)
			}
			if err := h.Insert("b", []float32{1, 1}); err != nil {
				t.Fatal(err)
			}
			if ids, _ := h.Search([]float32{1, 1}, 2, 16); len(ids) != 2 || ids[0] != "b" {
				t.Fatalf("expected [b a], got %v", ids)
			}
		})
	}
}

// checkGraph holds a graph to what every search relies on: a link names a
// node that is there, once, and never its owner.
func checkGraph(t *testing.T, h *HNSW) {
	t.Helper()
	for id, node := range h.Nodes {
		if len(node.Neighbors) != node.Level+1 {
			t.Fatalf("%s has level %d and %d layers", id, node.Level, len(node.Neighbors))
		}
		for layer, links := range node.Neighbors {
			seen := map[string]bool{}
			for _, to := range links {
				_, ok := h.Nodes[to]
				switch {
				case !ok:
					t.Fatalf("%s links to %s, which is gone", id, to)
				case to == id:
					t.Fatalf("%s links to itself on layer %d", id, layer)
				case seen[to]:
					t.Fatalf("%s links to %s twice on layer %d", id, to, layer)
				}
				seen[to] = true
			}
		}
	}
}

// After many rewrites the graph is still one a search can walk, and it still
// finds what a scan of every vector finds.
func TestHNSWManyRewritesKeepTheGraphSearchable(t *testing.T) {
	const n, dim, k = 400, 8, 10
	for name, rewrite := range rewrites {
		t.Run(name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(7))
			vec := func() []float32 {
				v := make([]float32, dim)
				for i := range v {
					v[i] = float32(rng.NormFloat64())
				}
				return v
			}
			h := NewHNSW(8, 64, EuclideanDistance)
			held := map[string][]float32{}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("v%03d", i)
				held[id] = vec()
				if err := h.Insert(id, held[id]); err != nil {
					t.Fatal(err)
				}
			}
			for round := 0; round < 3; round++ {
				for i := 0; i < n; i += 2 {
					id := fmt.Sprintf("v%03d", (i+round)%n)
					held[id] = vec()
					if err := rewrite(h, id, held[id]); err != nil {
						t.Fatal(err)
					}
				}
				checkGraph(t, h)
			}
			if h.Size() != n || h.Tombstones() != 0 {
				t.Fatalf("%d live nodes and %d tombstones, want %d and 0", h.Size(), h.Tombstones(), n)
			}

			found, wanted := 0, 0
			for q := 0; q < 50; q++ {
				query := vec()
				type hit struct {
					id   string
					dist float32
				}
				exact := make([]hit, 0, n)
				for id, v := range held {
					exact = append(exact, hit{id, EuclideanDistance(query, v)})
				}
				sort.Slice(exact, func(i, j int) bool { return exact[i].dist < exact[j].dist })
				got, _ := h.Search(query, k, 64)
				for _, want := range exact[:k] {
					wanted++
					if holds(got, want.id) {
						found++
					}
				}
			}
			if recall := float64(found) / float64(wanted); recall < 0.95 {
				t.Fatalf("recall@%d is %.3f after the rewrites, want 0.95", k, recall)
			}
		})
	}
}

// BenchmarkHNSWUpsertExisting rewrites ids the index already holds.
func BenchmarkHNSWUpsertExisting(b *testing.B) {
	for _, n := range []int{5000, 20000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			const dim = 64
			rng := rand.New(rand.NewSource(1))
			vec := func() []float32 {
				v := make([]float32, dim)
				for i := range v {
					v[i] = float32(rng.NormFloat64())
				}
				return v
			}
			h := NewHNSW(16, 64, CosineDistance)
			for i := 0; i < n; i++ {
				if err := h.Insert(fmt.Sprintf("v%d", i), vec()); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := h.Upsert(fmt.Sprintf("v%d", i%n), vec()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// When the descent ends on the node being placed, it goes on from the
// closest other node of that layer, not from where it started. Here the two
// are far apart and layer 0 does not join them: e and f on one side, a and b
// on the other, and a link on layer 1 from e to x, a node of layer 0. x moves
// next to a and b. The descent from e meets x itself first; it has to come
// down at a to find the neighbours x belongs with.
func TestHNSWRewriteComesDownAtTheClosestOtherNode(t *testing.T) {
	for name, rewrite := range rewrites {
		t.Run(name, func(t *testing.T) {
			h := NewHNSW(2, 8, EuclideanDistance)
			h.Dim = 2
			put := func(id string, v []float32, layers ...[]string) {
				h.Nodes[id] = &HNSWNode{ID: id, Vector: v, Level: len(layers) - 1, Neighbors: layers}
			}
			put("e", []float32{0, 0}, []string{"f"}, []string{"x", "a"})
			put("f", []float32{0, 1}, []string{"e"})
			put("a", []float32{100, 0}, []string{"b"}, []string{"e"})
			put("b", []float32{100, 1}, []string{"a"})
			put("x", []float32{50, 50}, []string{})
			h.EntryPoint = "e"

			if err := rewrite(h, "x", []float32{100, 0.5}); err != nil {
				t.Fatal(err)
			}
			if got := sorted(h.Nodes["x"].Neighbors[0]); fmt.Sprint(got) != "[a b]" {
				t.Fatalf("x links to %v on layer 0, want [a b]", got)
			}
		})
	}
}

// The node written keeps the level of the one it replaces, so a link another
// node holds to its id stays on a layer the node has. An id the index never
// held draws its level.
func TestHNSWRewrittenNodeKeepsItsLevel(t *testing.T) {
	for name, rewrite := range rewrites {
		t.Run(name, func(t *testing.T) {
			for run := 0; run < 20; run++ {
				h := movedGraph()
				// r stands on layer 3 with no link above layer 0: its level
				// is kept as it is, not up to a bound or for its links.
				h.Nodes["r"].Level = 3
				h.Nodes["r"].Neighbors = [][]string{{"s", "p"}, {}, {}, {}}
				for id, want := range map[string]int{"x": 1, "u": 0, "r": 3} {
					if err := rewrite(h, id, []float32{100, 100.6}); err != nil {
						t.Fatal(err)
					}
					if got := h.Nodes[id]; got.Level != want || len(got.Neighbors) != want+1 {
						t.Fatalf("%s is written on level %d with %d layers, it was on level %d", id, got.Level, len(got.Neighbors), want)
					}
				}
			}
		})
	}
	h := NewHNSW(4, 16, EuclideanDistance)
	above := 0
	for i := 0; i < 400; i++ {
		id := fmt.Sprintf("n%d", i)
		if err := h.Insert(id, []float32{float32(i), 1}); err != nil {
			t.Fatal(err)
		}
		if h.Nodes[id].Level > 0 {
			above++
		}
	}
	if above == 0 || above == 400 {
		t.Fatalf("%d of 400 new nodes drew a level above 0", above)
	}
}
