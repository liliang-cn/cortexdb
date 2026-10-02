package graph

import (
	"context"
	"fmt"
	"math"
	"testing"
)

// The tiny graphs below are small enough to solve by hand, which is the point:
// a PageRank test that compares one implementation's output with another's
// proves agreement, not correctness. These compare with arithmetic.

func pprSeedGraph(t *testing.T, ctx context.Context, g *GraphStore, nodes []string, edges []GraphEdge) {
	t.Helper()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, id := range nodes {
		if err := g.UpsertNode(ctx, &GraphNode{ID: id, Vector: []float32{1, 0, 0, 0}, NodeType: "n"}); err != nil {
			t.Fatalf("UpsertNode %s: %v", id, err)
		}
	}
	for i := range edges {
		if err := g.UpsertEdge(ctx, &edges[i]); err != nil {
			t.Fatalf("UpsertEdge %s: %v", edges[i].ID, err)
		}
	}
}

func assertPPR(t *testing.T, res *PPRResult, want map[string]float64) {
	t.Helper()
	if !res.Converged {
		t.Fatalf("walk did not converge in %d iterations", res.Iterations)
	}
	for id, w := range want {
		if got := res.Score(id); math.Abs(got-w) > 1e-7 {
			t.Errorf("score(%s) = %.9f, want %.9f", id, got, w)
		}
	}
	var sum float64
	for _, s := range res.Scores {
		sum += s.Score
	}
	if math.Abs(sum-1) > 1e-7 {
		t.Errorf("scores sum to %.9f, want 1", sum)
	}
}

// Path A —1— B —3— C, seeded at A, damping 1/2, undirected. The stationary
// vector solves r = ½·e_A + ½·Wᵀr with transitions A→B 1, B→A ¼, B→C ¾, C→B 1:
//
//	rA = ½ + ⅛·rB,  rB = ½·(rA + rC),  rC = ⅜·rB
//	⇒ rA = 13/24, rB = 8/24, rC = 3/24.
//
// Weighting matters to the answer: unweighted, C would hold 1/12, not 1/8.
// A fourth node D is attached to A by a "noise" edge whose type weight is 0,
// so D must take no part in the walk — not even as a node.
func TestPersonalizedPageRankMatchesTheHandSolvedStationaryVector(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			pprSeedGraph(t, ctx, b.store, []string{"A", "B", "C", "D", "Z"}, []GraphEdge{
				{ID: "ab", FromNodeID: "A", ToNodeID: "B", EdgeType: "rel", Weight: 1},
				{ID: "bc", FromNodeID: "C", ToNodeID: "B", EdgeType: "rel", Weight: 3},
				{ID: "ad", FromNodeID: "A", ToNodeID: "D", EdgeType: "noise", Weight: 5},
			})
			res, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"A": 1}, PPROptions{
				EdgeTypeWeights: map[string]float64{"noise": 0},
			})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			assertPPR(t, res, map[string]float64{"A": 13.0 / 24, "B": 8.0 / 24, "C": 3.0 / 24})
			if res.Nodes != 3 {
				t.Errorf("subgraph has %d nodes, want 3 (D is behind a zero-weight type, Z is unreachable)", res.Nodes)
			}
			if res.Truncated {
				t.Errorf("a three-node component reported truncation")
			}
		})
	}
}

// Two seeds weighted 3:1 at the ends of A — B — C (unit weights). B sends
// half its walked mass each way, and the restart puts ¾·½ on A and ¼·½ on C:
//
//	rA = 3/8 + ¼·rB,  rC = 1/8 + ¼·rB,  rB = ½·(rA + rC) = ½·(½ + ½·rB)
//	⇒ rB = 1/3, rA = 11/24, rC = 5/24.
//
// A uniform teleport would make A and C equal; this pins that the seed weights
// are the teleport distribution, not a set.
func TestPersonalizedPageRankTeleportsInProportionToSeedWeights(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			pprSeedGraph(t, ctx, b.store, []string{"A", "B", "C"}, []GraphEdge{
				{ID: "ab", FromNodeID: "A", ToNodeID: "B", Weight: 1},
				{ID: "bc", FromNodeID: "B", ToNodeID: "C", Weight: 1},
			})
			res, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"A": 3, "C": 1}, PPROptions{})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			assertPPR(t, res, map[string]float64{"A": 11.0 / 24, "B": 8.0 / 24, "C": 5.0 / 24})
		})
	}
}

// A directed walk from A along A→B→C, where C is a dead end. A dead end's mass
// returns to the seeds:
//
//	rB = ½·rA,  rC = ½·rB,  rA = ½ + ½·rC
//
// where the ½·rC term is C's walked share, which has nowhere to go but back
// to the seed. ⇒ rA = 4/7, rB = 2/7, rC = 1/7.
func TestDirectedPersonalizedPageRankReturnsDeadEndMassToTheSeeds(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			pprSeedGraph(t, ctx, b.store, []string{"A", "B", "C"}, []GraphEdge{
				{ID: "ab", FromNodeID: "A", ToNodeID: "B", Weight: 1},
				{ID: "bc", FromNodeID: "B", ToNodeID: "C", Weight: 1},
			})
			res, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"A": 1}, PPROptions{Directed: true})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			assertPPR(t, res, map[string]float64{"A": 4.0 / 7, "B": 2.0 / 7, "C": 1.0 / 7})
		})
	}
}

// A chain longer than MaxHops is cut at the hop limit, and a node cap keeps the
// best-connected candidates: from the hub H, the neighbour joined by weight 5
// is admitted before the one joined by weight 1.
func TestPersonalizedPageRankStaysInsideItsBounds(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			pprSeedGraph(t, ctx, b.store, []string{"H", "strong", "weak", "x1", "x2", "x3"}, []GraphEdge{
				{ID: "h-strong", FromNodeID: "H", ToNodeID: "strong", Weight: 5},
				{ID: "h-weak", FromNodeID: "H", ToNodeID: "weak", Weight: 1},
				{ID: "s-x1", FromNodeID: "strong", ToNodeID: "x1", Weight: 1},
				{ID: "x1-x2", FromNodeID: "x1", ToNodeID: "x2", Weight: 1},
				{ID: "x2-x3", FromNodeID: "x2", ToNodeID: "x3", Weight: 1},
			})

			capped, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"H": 1}, PPROptions{MaxNodes: 2})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			if !capped.Truncated || capped.Nodes != 2 || capped.Score("strong") == 0 || capped.Score("weak") != 0 {
				t.Fatalf("node cap kept %d nodes (truncated=%v, strong=%v, weak=%v), want H and strong only",
					capped.Nodes, capped.Truncated, capped.Score("strong"), capped.Score("weak"))
			}

			hops, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"H": 1}, PPROptions{MaxHops: 2})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			if hops.Score("x1") == 0 || hops.Score("x2") != 0 {
				t.Fatalf("two hops from H should reach x1 and not x2: x1=%v x2=%v", hops.Score("x1"), hops.Score("x2"))
			}
		})
	}
}

func TestPersonalizedPageRankIgnoresSeedsTheGraphDoesNotHold(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			pprSeedGraph(t, ctx, b.store, []string{"A"}, nil)
			res, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"ghost": 1, "A": 0}, PPROptions{})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			if len(res.Scores) != 0 {
				t.Fatalf("got %v, want no scores when no usable seed exists", res.Scores)
			}
		})
	}
}

// A hub reached by the walk is a node of it but not a road through it, while a
// hub the question names is expanded all the same — a question about a popular
// entity still has to reach what mentions it.
func TestPersonalizedPageRankDoesNotExpandHubsItOnlyPassesThrough(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			pprSeedGraph(t, ctx, b.store, []string{"S", "H", "L1", "L2", "L3"}, []GraphEdge{
				{ID: "s-h", FromNodeID: "S", ToNodeID: "H", Weight: 1},
				{ID: "h-l1", FromNodeID: "H", ToNodeID: "L1", Weight: 1},
				{ID: "h-l2", FromNodeID: "H", ToNodeID: "L2", Weight: 1},
				{ID: "h-l3", FromNodeID: "H", ToNodeID: "L3", Weight: 1},
			})

			passing, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"S": 1}, PPROptions{MaxExpandDegree: 2})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			if passing.Score("H") == 0 || passing.Score("L1") != 0 || !passing.Truncated {
				t.Fatalf("walk through hub H: H=%v L1=%v truncated=%v, want H kept, its leaves unreached, truncation reported",
					passing.Score("H"), passing.Score("L1"), passing.Truncated)
			}

			named, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"H": 1}, PPROptions{MaxExpandDegree: 2})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			for _, leaf := range []string{"L1", "L2", "L3", "S"} {
				if named.Score(leaf) == 0 {
					t.Fatalf("seeded at hub H, the walk did not reach %s", leaf)
				}
			}
		})
	}
}

// S reaches Z by a heavy edge and B1..B4 by light ones; each leads one hop on,
// to X and to Y1..Y4. With room to expand one node per hop, the walk must
// expand the one it sends the most mass to — Z, not B1, which sorts first —
// so X is reached and no Y is.
func TestPersonalizedPageRankExpandsTheFrontierNodesItSendsTheMostMassTo(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			nodes := []string{"S", "Z", "X"}
			edges := []GraphEdge{
				{ID: "s-z", FromNodeID: "S", ToNodeID: "Z", Weight: 5},
				{ID: "z-x", FromNodeID: "Z", ToNodeID: "X", Weight: 1},
			}
			for i := 1; i <= 4; i++ {
				bi, yi := fmt.Sprintf("B%d", i), fmt.Sprintf("Y%d", i)
				nodes = append(nodes, bi, yi)
				edges = append(edges,
					GraphEdge{ID: "s-" + bi, FromNodeID: "S", ToNodeID: bi, Weight: 1},
					GraphEdge{ID: bi + "-" + yi, FromNodeID: bi, ToNodeID: yi, Weight: 1})
			}
			pprSeedGraph(t, ctx, b.store, nodes, edges)

			capped, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"S": 1}, PPROptions{MaxFrontier: 1})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			if capped.Score("X") == 0 {
				t.Fatalf("X, behind the heaviest edge, was not reached: %+v", capped.Scores)
			}
			for i := 1; i <= 4; i++ {
				if y := fmt.Sprintf("Y%d", i); capped.Score(y) != 0 {
					t.Fatalf("%s was reached through a light edge the frontier cap should have left unexpanded", y)
				}
			}
			if capped.Score("B1") == 0 || !capped.Truncated {
				t.Fatalf("B1 should stay in the walk as a node, and the cap be reported: B1=%v truncated=%v", capped.Score("B1"), capped.Truncated)
			}

			full, err := b.store.PersonalizedPageRank(ctx, map[string]float64{"S": 1}, PPROptions{})
			if err != nil {
				t.Fatalf("PersonalizedPageRank: %v", err)
			}
			if full.Score("Y1") == 0 {
				t.Fatal("uncapped, the walk should reach Y1")
			}
		})
	}
}
