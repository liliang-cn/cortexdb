package graph

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// The Leiden-versus-Louvain comparison behind the choice of default, kept
// runnable so the numbers can be re-measured rather than trusted:
//
//	CORTEXDB_LEIDEN_EVAL=1 go test ./pkg/graph -run TestLeidenEval -v
//	CORTEXDB_LEIDEN_EVAL=1 CORTEXDB_COMMUNITY_SNAPSHOT=/path/to/brain.db go test ./pkg/graph -run TestLeidenEval -v
//
// The snapshot is opened read-only. Skipped unless asked for: it is a
// measurement, not a check.

type evalRow struct {
	disconnected []int
	levels       []int // communities per level
	topQ, bestQ  float64
	median       time.Duration
	h            *CommunityHierarchy
}

func evalRun(nodes []string, edges []WeightedEdge, alg CommunityAlgorithm, reps int) evalRow {
	var times []time.Duration
	var h *CommunityHierarchy
	for i := 0; i < reps; i++ {
		start := time.Now()
		h = CommunityHierarchyOf(nodes, edges, HierarchyOptions{Algorithm: alg})
		times = append(times, time.Since(start))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	r := evalRow{disconnected: disconnectedCommunities(nodes, edges, h), median: times[len(times)/2], h: h}
	r.bestQ = math.Inf(-1)
	for _, l := range h.Levels {
		r.levels = append(r.levels, len(l.Communities))
		r.bestQ = math.Max(r.bestQ, l.Modularity)
	}
	r.topQ = topModularity(h)
	return r
}

func evalCompare(t *testing.T, name string, nodes []string, edges []WeightedEdge, reps int) (evalRow, evalRow) {
	lv := evalRun(nodes, edges, CommunityAlgorithmLouvain, reps)
	ld := evalRun(nodes, edges, CommunityAlgorithmLeiden, reps)
	t.Logf("| %s | %d/%d | louvain | %v | %v | %.4f | %v |", name, len(nodes), len(edges), lv.levels, lv.disconnected, lv.topQ, lv.median.Round(time.Microsecond))
	t.Logf("| %s | %d/%d | leiden  | %v | %v | %.4f | %v (%.2f×) |", name, len(nodes), len(edges), ld.levels, ld.disconnected, ld.topQ, ld.median.Round(time.Microsecond), float64(ld.median)/float64(lv.median))
	return lv, ld
}

func TestLeidenEval(t *testing.T) {
	if os.Getenv("CORTEXDB_LEIDEN_EVAL") == "" {
		t.Skip("set CORTEXDB_LEIDEN_EVAL=1 to run the Leiden/Louvain comparison")
	}
	t.Log("| graph | nodes/edges | algorithm | communities per level | disconnected per level | top Q | median time |")
	nodes, edges := karateGraph()
	evalCompare(t, "karate", nodes, edges, 21)
	for _, n := range []int{1000, 10000, 50000} {
		for _, mu := range []float64{0.1, 0.3, 0.5, 0.6} {
			nodes, edges, _ := lfrGraph(n, 10, 100, mu, 10, 200, 1)
			reps := 7
			if n >= 50000 {
				reps = 3
			}
			evalCompare(t, fmt.Sprintf("lfr k10 mu%.1f", mu), nodes, edges, reps)
		}
	}

	path := os.Getenv("CORTEXDB_COMMUNITY_SNAPSHOT")
	if path == "" {
		return
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := NewGraphStoreOn(db, nil, nil)
	ctx := context.Background()
	for _, cfg := range []struct {
		name string
		opts HierarchyOptions
	}{
		// What graphflow.BuildCommunityHierarchy (global search) asks for.
		{"snapshot entity graph", HierarchyOptions{NodeIDPrefix: "entity:", ExcludeEdgeTypes: []string{"has_chunk", "next", "mentions", "in_community"}}},
		// What CommunityDetection asks for.
		{"snapshot full graph", HierarchyOptions{}},
	} {
		var hs [2]*CommunityHierarchy
		var times [2][]time.Duration
		for i, alg := range []CommunityAlgorithm{CommunityAlgorithmLouvain, CommunityAlgorithmLeiden} {
			for r := 0; r < 7; r++ {
				o := cfg.opts
				o.Algorithm = alg
				start := time.Now()
				h, err := g.HierarchicalCommunities(ctx, o)
				if err != nil {
					t.Fatal(err)
				}
				times[i] = append(times[i], time.Since(start))
				hs[i] = h
			}
			sort.Slice(times[i], func(a, b int) bool { return times[i][a] < times[i][b] })
		}
		// Rebuild the topology the hierarchy saw, to check connectivity.
		nodes, edges := snapshotTopology(t, db, cfg.opts)
		for i, alg := range []string{"louvain", "leiden "} {
			var levels []int
			for _, l := range hs[i].Levels {
				levels = append(levels, len(l.Communities))
			}
			t.Logf("| %s | %d/%d | %s | %v | %v | %.4f | %v (incl. SQL load) |", cfg.name, hs[i].NodeCount, hs[i].EdgeCount, alg,
				levels, disconnectedCommunities(nodes, edges, hs[i]), topModularity(hs[i]), times[i][len(times[i])/2].Round(time.Microsecond))
		}
		reportSummaryDrift(t, cfg.name, hs[0], hs[1])
	}
}

// reportSummaryDrift says how much of what global search would summarise
// changes: per level, how many reportable communities (3+ members, the
// graphflow default MinSize) Leiden keeps with exactly Louvain's member set —
// an identical set gives an identical deterministic report — and the NMI of
// the two partitions.
func reportSummaryDrift(t *testing.T, name string, louvain, leiden *CommunityHierarchy) {
	key := func(c HierarchicalCommunity) string { return strings.Join(c.Nodes, "\x00") }
	louvainSets := map[string]bool{}
	for _, l := range louvain.Levels {
		for _, c := range l.Communities {
			louvainSets[key(c)] = true
		}
	}
	for li, l := range leiden.Levels {
		reportable, same := 0, 0
		for _, c := range l.Communities {
			if len(c.Nodes) < 3 {
				continue
			}
			reportable++
			if louvainSets[key(c)] {
				same++
			}
		}
		nmi := math.NaN()
		if li < len(louvain.Levels) {
			nmi = partitionNMI(louvain.Levels[li], l)
		}
		t.Logf("%s level %d: %d reportable Leiden communities, %d identical to a Louvain community (any level); NMI vs Louvain level %d = %.3f", name, li, reportable, same, li, nmi)
	}
}

func partitionNMI(a, b CommunityLevel) float64 {
	label := func(l CommunityLevel) map[string]int {
		m := map[string]int{}
		for _, c := range l.Communities {
			for _, n := range c.Nodes {
				m[n] = c.ID
			}
		}
		return m
	}
	la, lb := label(a), label(b)
	n := float64(len(la))
	joint := map[[2]int]float64{}
	ca, cb := map[int]float64{}, map[int]float64{}
	for node, x := range la {
		y := lb[node]
		joint[[2]int{x, y}]++
		ca[x]++
		cb[y]++
	}
	mi := 0.0
	for k, v := range joint {
		mi += v / n * math.Log(v*n/(ca[k[0]]*cb[k[1]]))
	}
	h := func(c map[int]float64) float64 {
		s := 0.0
		for _, v := range c {
			s -= v / n * math.Log(v/n)
		}
		return s
	}
	ha, hb := h(ca), h(cb)
	if ha+hb == 0 {
		return 1
	}
	return 2 * mi / (ha + hb)
}

func snapshotTopology(t *testing.T, db *sql.DB, opts HierarchyOptions) ([]string, []WeightedEdge) {
	rows, err := db.Query("SELECT id FROM graph_nodes ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var nodes []string
	index := map[string]int{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		if opts.NodeIDPrefix != "" && !strings.HasPrefix(id, opts.NodeIDPrefix) {
			continue
		}
		index[id] = len(nodes)
		nodes = append(nodes, id)
	}
	rows.Close()
	excluded := map[string]bool{}
	for _, e := range opts.ExcludeEdgeTypes {
		excluded[e] = true
	}
	erows, err := db.Query("SELECT from_node_id, to_node_id, COALESCE(edge_type,'') FROM graph_edges ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var edges []WeightedEdge
	for erows.Next() {
		var from, to, et string
		if err := erows.Scan(&from, &to, &et); err != nil {
			t.Fatal(err)
		}
		u, ok1 := index[from]
		v, ok2 := index[to]
		if ok1 && ok2 && !excluded[et] {
			edges = append(edges, WeightedEdge{From: u, To: v, Weight: 1})
		}
	}
	erows.Close()
	return nodes, edges
}
