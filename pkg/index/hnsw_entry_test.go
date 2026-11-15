package index

import (
	"fmt"
	"math/rand"
	"testing"
)

// reinserts are the three ways a store writes a vector under an id whose
// node was deleted.
var reinserts = map[string]func(h *HNSW, id string, v []float32) error{
	"Insert": func(h *HNSW, id string, v []float32) error { return h.Insert(id, v) },
	"Upsert": func(h *HNSW, id string, v []float32) error { return h.Upsert(id, v) },
	"InsertBatch": func(h *HNSW, id string, v []float32) error {
		return h.InsertBatch([]struct {
			ID     string
			Vector []float32
		}{{id, v}})
	},
}

// tombstoneEntryGraph is a graph written by hand, in the shape of the state
// an index is in after every node was deleted and some were written again: the entry
// point t is still the last node deleted, and b, c and d are live around
// it. c is the live node on the highest layer, above t; g is another
// deleted node, higher than any live one.
func tombstoneEntryGraph() *HNSW {
	h := NewHNSW(4, 16, EuclideanDistance)
	h.Dim = 2
	put := func(id string, deleted bool, v []float32, layers ...[]string) {
		h.Nodes[id] = &HNSWNode{ID: id, Vector: v, Level: len(layers) - 1, Neighbors: layers, Deleted: deleted}
	}
	put("t", true, []float32{0, 0}, []string{"b", "c", "g"}, []string{"c", "g"})
	put("g", true, []float32{9, 9}, []string{"t", "d"}, []string{"t"}, []string{}, []string{})
	put("b", false, []float32{0, 1}, []string{"t", "c"})
	put("c", false, []float32{1, 0}, []string{"t", "b", "d"}, []string{"t", "d"}, []string{})
	put("d", false, []float32{2, 0}, []string{"c", "g"}, []string{"c"})
	h.EntryPoint = "t"
	return h
}

// When every node of an index is deleted, the entry point stays on the last
// one. Writing that id again used to leave the index with no entry point, so
// the node was taken for the first of an empty index: it became the entry
// point with no link, and the nodes written since could not be reached.
func TestHNSWRewriteOfADeletedEntryPointKeepsTheLiveNodes(t *testing.T) {
	for name, reinsert := range reinserts {
		t.Run(name, func(t *testing.T) {
			// The choice walks a map: twenty graphs, so that its order
			// cannot hide a wrong choice.
			for run := 0; run < 20; run++ {
				h := tombstoneEntryGraph()
				if err := reinsert(h, "t", []float32{0, 0.5}); err != nil {
					t.Fatal(err)
				}
				found, _ := h.Search([]float32{0, 0.5}, 10, 50)
				if got := fmt.Sprint(found); got != "[t b c d]" {
					t.Fatalf("a search reaches %s, want the four live nodes [t b c d]", got)
				}
				if len(h.Nodes["t"].Neighbors[0]) == 0 {
					t.Fatal("t was written with no link")
				}
				// c is the live node on the highest layer: the walk starts
				// there, unless t is written on a layer above it.
				switch entry := h.EntryPoint; {
				case entry == "c" && h.Nodes["t"].Level <= h.Nodes["c"].Level:
				case entry == "t" && h.Nodes["t"].Level > h.Nodes["c"].Level:
				default:
					t.Fatalf("entry point %q (t on layer %d, c on layer %d)", entry, h.Nodes["t"].Level, h.Nodes["c"].Level)
				}
			}
		})
	}
}

// One live node is enough to keep: it is the entry point, and the node
// written links to it.
func TestHNSWRewriteOfADeletedEntryPointKeepsTheOnlyLiveNode(t *testing.T) {
	for name, reinsert := range reinserts {
		t.Run(name, func(t *testing.T) {
			h := tombstoneEntryGraph()
			for _, id := range []string{"c", "d"} {
				if err := h.Delete(id); err != nil {
					t.Fatal(err)
				}
			}
			if err := reinsert(h, "t", []float32{0, 0.5}); err != nil {
				t.Fatal(err)
			}
			if found, _ := h.Search([]float32{0, 0.5}, 10, 50); fmt.Sprint(found) != "[t b]" {
				t.Fatalf("a search reaches %v, want [t b]", found)
			}
			if !holdsLink(h.Nodes["t"].Neighbors[0], "b") || !holdsLink(h.Nodes["b"].Neighbors[0], "t") {
				t.Fatalf("t links to %v and b to %v, want each to hold the other", h.Nodes["t"].Neighbors[0], h.Nodes["b"].Neighbors[0])
			}
		})
	}
}

func holdsLink(links []string, id string) bool {
	for _, to := range links {
		if to == id {
			return true
		}
	}
	return false
}

// Deleting the last live node leaves the entry point on it: the deleted
// nodes are still the way into what is written next. This is the state the
// tests above start from.
func TestHNSWDeleteOfTheLastLiveNodeLeavesTheEntryPoint(t *testing.T) {
	h := NewHNSW(4, 16, EuclideanDistance)
	for _, id := range []string{"a", "b"} {
		if err := h.Insert(id, []float32{1, float32(len(h.Nodes))}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b"} {
		if err := h.Delete(id); err != nil {
			t.Fatal(err)
		}
	}
	if h.EntryPoint != "b" || !h.Nodes["b"].Deleted {
		t.Fatalf("entry point %q, want b, the last node deleted", h.EntryPoint)
	}
}

// With no live node left there is nothing to keep: the node written is the
// first of the index again, whatever deleted nodes remain.
func TestHNSWRewriteOfADeletedEntryPointWithNoLiveNode(t *testing.T) {
	for name, reinsert := range reinserts {
		t.Run(name, func(t *testing.T) {
			h := tombstoneEntryGraph()
			for _, id := range []string{"b", "c", "d"} {
				if err := h.Delete(id); err != nil {
					t.Fatal(err)
				}
			}
			if h.EntryPoint != "t" {
				t.Fatalf("entry point %q before the rewrite, want t", h.EntryPoint)
			}
			if err := reinsert(h, "t", []float32{0, 0.5}); err != nil {
				t.Fatal(err)
			}
			if h.EntryPoint != "t" {
				t.Fatalf("entry point %q, want t", h.EntryPoint)
			}
			if found, _ := h.Search([]float32{0, 0.5}, 10, 50); fmt.Sprint(found) != "[t]" {
				t.Fatalf("a search reaches %v, want [t]", found)
			}
			for layer, links := range h.Nodes["t"].Neighbors {
				if len(links) != 0 {
					t.Fatalf("t links to %v on layer %d, want no link", links, layer)
				}
			}
		})
	}
}

// The whole path, as a store runs it when it deletes every row and writes
// them again: each vector is found afterwards. In the order of the deletes,
// the last id written again is the one the entry point stayed on, and the
// search returned that node alone; in another order it comes earlier, and
// the search lost the nodes written before it. The index draws its levels at
// random and the case arose on one index in three or more, so each order is
// run on forty.
func TestHNSWEveryNodeDeletedThenWrittenAgain(t *testing.T) {
	const n = 20
	vec := func(i int) []float32 { return []float32{float32(i), float32(i % 3), 1} }
	inOrder := func(*rand.Rand) []int {
		order := make([]int, n)
		for i := range order {
			order[i] = i
		}
		return order
	}
	shuffled := func(r *rand.Rand) []int { return r.Perm(n) }
	for name, reinsert := range reinserts {
		for orderName, order := range map[string]func(*rand.Rand) []int{"same order": inOrder, "shuffled": shuffled} {
			t.Run(name+"/"+orderName, func(t *testing.T) {
				r := rand.New(rand.NewSource(28))
				for run := 0; run < 40; run++ {
					h := NewHNSW(16, 64, EuclideanDistance)
					for i := 0; i < n; i++ {
						if err := h.Insert(fmt.Sprintf("n%02d", i), vec(i)); err != nil {
							t.Fatal(err)
						}
					}
					for _, i := range order(r) {
						if err := h.Delete(fmt.Sprintf("n%02d", i)); err != nil {
							t.Fatal(err)
						}
					}
					for _, i := range order(r) {
						if err := reinsert(h, fmt.Sprintf("n%02d", i), vec(i)); err != nil {
							t.Fatal(err)
						}
					}
					if found, _ := h.Search(vec(0), n, 200); len(found) != n {
						t.Fatalf("run %d: a search reaches %d of the %d nodes written again", run, len(found), n)
					}
				}
			})
		}
	}
}
