package graph

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

// plantedHierarchy builds groups × cliques × size nodes: every clique is
// complete, each pair of cliques in one group is joined by size edges (dense
// enough that merging them raises modularity), and adjacent groups by one.
func plantedHierarchy(groups, cliques, size int) ([]string, []WeightedEdge) {
	var nodes []string
	idx := func(g, c, i int) int { return (g*cliques+c)*size + i }
	for g := 0; g < groups; g++ {
		for c := 0; c < cliques; c++ {
			for i := 0; i < size; i++ {
				nodes = append(nodes, fmt.Sprintf("n-%d-%d-%02d", g, c, i))
			}
		}
	}
	var edges []WeightedEdge
	for g := 0; g < groups; g++ {
		for c := 0; c < cliques; c++ {
			for i := 0; i < size; i++ {
				for j := i + 1; j < size; j++ {
					edges = append(edges, WeightedEdge{From: idx(g, c, i), To: idx(g, c, j), Weight: 1})
				}
			}
			for d := c + 1; d < cliques; d++ {
				for k := 0; k < size; k++ {
					edges = append(edges, WeightedEdge{From: idx(g, c, k), To: idx(g, d, (k+1)%size), Weight: 1})
				}
			}
		}
		if g+1 < groups {
			edges = append(edges, WeightedEdge{From: idx(g, 0, 0), To: idx(g+1, 0, 0), Weight: 1})
		}
	}
	return nodes, edges
}

func TestLouvainHierarchyRecoversPlantedLevels(t *testing.T) {
	nodes, edges := plantedHierarchy(3, 4, 8)
	h := LouvainHierarchy(nodes, edges, HierarchyOptions{})
	if len(h.Levels) < 2 {
		t.Fatalf("expected at least two levels, got %d", len(h.Levels))
	}
	// The finest level is the cliques.
	if got := len(h.Levels[0].Communities); got != 12 {
		t.Fatalf("level 0: expected 12 clique communities, got %d", got)
	}
	for _, c := range h.Levels[0].Communities {
		prefix := c.Nodes[0][:len("n-0-0")]
		for _, n := range c.Nodes {
			if n[:len(prefix)] != prefix {
				t.Fatalf("level 0 community mixes cliques: %v", c.Nodes)
			}
		}
	}
	// The next level is the groups.
	if got := len(h.Levels[1].Communities); got != 3 {
		t.Fatalf("level 1: expected 3 group communities, got %d", got)
	}

	for li, level := range h.Levels {
		// Every level partitions every node exactly once.
		var all []string
		for _, c := range level.Communities {
			all = append(all, c.Nodes...)
		}
		sort.Strings(all)
		want := append([]string(nil), nodes...)
		sort.Strings(want)
		if !reflect.DeepEqual(all, want) {
			t.Fatalf("level %d is not a partition of the nodes", li)
		}
		if li > 0 && level.Modularity < h.Levels[li-1].Modularity-1e-9 {
			t.Fatalf("modularity fell from %f to %f", h.Levels[li-1].Modularity, level.Modularity)
		}
		// Parent/child links agree in both directions and on membership.
		for _, c := range level.Communities {
			if li+1 < len(h.Levels) {
				if c.Parent < 0 {
					t.Fatalf("level %d community %d has no parent", li, c.ID)
				}
				parent := h.Levels[li+1].Communities[c.Parent]
				found := false
				for _, ch := range parent.Children {
					if ch == c.ID {
						found = true
					}
				}
				if !found {
					t.Fatalf("parent %d does not list child %d", c.Parent, c.ID)
				}
			} else if c.Parent != -1 {
				t.Fatalf("top-level community has parent %d", c.Parent)
			}
		}
	}
}

func TestLouvainHierarchyIsDeterministic(t *testing.T) {
	nodes, edges := plantedHierarchy(3, 4, 8)
	a := LouvainHierarchy(nodes, edges, HierarchyOptions{})
	for i := 0; i < 5; i++ {
		b := LouvainHierarchy(nodes, edges, HierarchyOptions{})
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestLouvainHierarchyEdgeCases(t *testing.T) {
	if h := LouvainHierarchy(nil, nil, HierarchyOptions{}); len(h.Levels) != 0 {
		t.Fatalf("empty graph: expected no levels")
	}
	if h := LouvainHierarchy([]string{"a", "b"}, nil, HierarchyOptions{}); len(h.Levels) != 0 {
		t.Fatalf("edgeless graph: expected no levels, got %d", len(h.Levels))
	}
	nodes, edges := plantedHierarchy(3, 4, 8)
	if h := LouvainHierarchy(nodes, edges, HierarchyOptions{MaxLevels: 1}); len(h.Levels) != 1 {
		t.Fatalf("MaxLevels 1: got %d levels", len(h.Levels))
	}
}
