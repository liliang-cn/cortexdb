package graph

import (
	"context"
	"testing"
)

// Two signals that agree must reinforce each other, not cancel out.
//
// PredictEdges combined vector similarity with common neighbours by averaging
// them, and the branch that did so ran only when there were common neighbours.
// Both halves of that were wrong in the same direction:
//
//   - The structural term is commonNeighbors/(degree+1), which is strictly
//     below 1, so halved it is strictly below 0.5 — and 0.5 is the threshold a
//     prediction has to clear. A pair joined by nothing but shared neighbours,
//     the strongest purely structural evidence a graph can offer, could never
//     be returned at all.
//   - Averaging a strong similarity with a weaker structural score pulls it
//     down: 0.9 with common neighbours scored 0.6, below the 0.9 it would have
//     scored with none. Sharing neighbours actively demoted a pair.
//
// So the "combined" method was strictly worse than not combining, which is the
// opposite of what combining two independent signals is for. The replacement is
// a noisy-OR: the structural term closes some of the distance left between the
// similarity and certainty, so it can only raise a score, never lower one, and
// structure alone can still carry a pair over the line.
func TestPredictEdgesLetsTheTwoSignalsAgree(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}

			// alice and dave both point at bob and carol and at nothing else,
			// so they share every neighbour either of them has. Their vectors
			// are orthogonal: there is no similarity to lean on, only structure.
			nodes := []*GraphNode{
				{ID: "alice", Vector: []float32{1, 0, 0, 0}, NodeType: "person", Content: "alice"},
				{ID: "dave", Vector: []float32{0, 1, 0, 0}, NodeType: "person", Content: "dave"},
				{ID: "bob", Vector: []float32{0, 0, 1, 0}, NodeType: "person", Content: "bob"},
				{ID: "carol", Vector: []float32{0, 0, 0, 1}, NodeType: "person", Content: "carol"},
			}
			for _, n := range nodes {
				if err := b.store.UpsertNode(ctx, n); err != nil {
					t.Fatalf("UpsertNode %s: %v", n.ID, err)
				}
			}
			edges := []*GraphEdge{
				{ID: "a-b", FromNodeID: "alice", ToNodeID: "bob", EdgeType: "knows", Weight: 1},
				{ID: "a-c", FromNodeID: "alice", ToNodeID: "carol", EdgeType: "knows", Weight: 1},
				{ID: "d-b", FromNodeID: "dave", ToNodeID: "bob", EdgeType: "knows", Weight: 1},
				{ID: "d-c", FromNodeID: "dave", ToNodeID: "carol", EdgeType: "knows", Weight: 1},
			}
			for _, e := range edges {
				if err := b.store.UpsertEdge(ctx, e); err != nil {
					t.Fatalf("UpsertEdge %s: %v", e.ID, err)
				}
			}

			got, err := b.store.PredictEdges(ctx, "alice", 10)
			if err != nil {
				t.Fatalf("PredictEdges: %v", err)
			}

			var dave *EdgePrediction
			for i := range got {
				if got[i].ToNodeID == "dave" {
					dave = &got[i]
				}
			}
			if dave == nil {
				t.Fatalf("alice and dave share every neighbour and nothing else, and the prediction is not there: %+v", got)
			}
			if dave.Method != "combined" {
				t.Errorf("method = %q, want combined — this prediction rests on shared neighbours", dave.Method)
			}
			if dave.Score <= 0.5 {
				t.Errorf("score = %v, want above the 0.5 threshold it had to clear to be here at all", dave.Score)
			}
		})
	}
}

// Sharing neighbours must never demote a pair below what similarity alone would
// have scored it. This is the half of the defect that survives even when the
// threshold is cleared, and it is the one that silently reorders results.
func TestSharedNeighboursNeverLowerAScore(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}

			// twin and echo are both near-identical to seed. Only twin shares a
			// neighbour with it. Under averaging that made twin score lower.
			nodes := []*GraphNode{
				{ID: "seed", Vector: []float32{1, 0, 0, 0}, NodeType: "doc", Content: "seed"},
				{ID: "twin", Vector: []float32{0.99, 0.01, 0, 0}, NodeType: "doc", Content: "twin"},
				{ID: "echo", Vector: []float32{0.99, 0.01, 0, 0}, NodeType: "doc", Content: "echo"},
				{ID: "hub", Vector: []float32{0, 0, 1, 0}, NodeType: "doc", Content: "hub"},
			}
			for _, n := range nodes {
				if err := b.store.UpsertNode(ctx, n); err != nil {
					t.Fatalf("UpsertNode %s: %v", n.ID, err)
				}
			}
			for _, e := range []*GraphEdge{
				{ID: "s-h", FromNodeID: "seed", ToNodeID: "hub", EdgeType: "cites", Weight: 1},
				{ID: "t-h", FromNodeID: "twin", ToNodeID: "hub", EdgeType: "cites", Weight: 1},
			} {
				if err := b.store.UpsertEdge(ctx, e); err != nil {
					t.Fatalf("UpsertEdge %s: %v", e.ID, err)
				}
			}

			got, err := b.store.PredictEdges(ctx, "seed", 10)
			if err != nil {
				t.Fatalf("PredictEdges: %v", err)
			}
			scores := map[string]float64{}
			for _, p := range got {
				scores[p.ToNodeID] = p.Score
			}
			twin, okTwin := scores["twin"]
			echo, okEcho := scores["echo"]
			if !okTwin || !okEcho {
				t.Fatalf("expected both twin and echo to be predicted, got %+v", got)
			}
			if twin < echo {
				t.Errorf("twin scored %v and echo %v: twin is as similar as echo and additionally shares a neighbour, so it cannot rank lower", twin, echo)
			}
		})
	}
}
