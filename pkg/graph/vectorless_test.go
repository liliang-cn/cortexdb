package graph

import (
	"context"
	"math"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// A node needs no vector. A structural node — a step in an agent's execution
// record, found by its id, type, properties and edges — used to be refused
// with "missing vector", so every such caller invented one, and the invented
// points then took part in similarity search.

// similarityHost swaps the similarity function the graph scores with.
type similarityHost struct {
	vectorHost
	sim core.SimilarityFunc
}

func (h similarityHost) GetSimilarityFunc() core.SimilarityFunc { return h.sim }

func writeVectorless(t *testing.T, g *GraphStore, ctx context.Context, path string, nodes ...*GraphNode) {
	t.Helper()
	switch path {
	case "UpsertNode":
		for _, n := range nodes {
			if err := g.UpsertNode(ctx, n); err != nil {
				t.Fatalf("UpsertNode %s: %v", n.ID, err)
			}
		}
	case "UpsertNodesBatch":
		res, err := g.UpsertNodesBatch(ctx, nodes)
		if err == nil {
			err = res.Err()
		}
		if err != nil {
			t.Fatalf("UpsertNodesBatch: %v", err)
		}
	case "ExecuteBatch":
		res, err := g.ExecuteBatch(ctx, &BatchGraphOperation{NodeUpserts: nodes})
		if err == nil {
			err = res.Err()
		}
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
	default:
		t.Fatalf("unknown write path %q", path)
	}
}

func TestANodeNeedsNoVector(t *testing.T) {
	for _, path := range []string{"UpsertNode", "UpsertNodesBatch", "ExecuteBatch"} {
		for _, b := range backends(t) {
			t.Run(b.name+"/"+path, func(t *testing.T) {
				ctx := context.Background()
				if err := b.store.InitGraphSchema(ctx); err != nil {
					t.Fatalf("schema: %v", err)
				}
				mustNode(t, b.store, ctx, "vl:run", "run")
				writeVectorless(t, b.store, ctx, path,
					&GraphNode{ID: "vl:step", NodeType: "Step", Content: "plan",
						Properties: map[string]any{"status": "done"}})
				mustEdge(t, b.store, ctx, "vl:has", "vl:run", "vl:step", "HAS_STEP", nil)

				got, err := b.store.GetNode(ctx, "vl:step")
				if err != nil {
					t.Fatalf("GetNode: %v", err)
				}
				if len(got.Vector) != 0 || got.Content != "plan" || got.NodeType != "Step" {
					t.Errorf("read back %+v, want the step with no vector", got)
				}
				res, err := b.store.QueryCypher(ctx, CypherRequest{
					Query: `MATCH (r)-[:HAS_STEP]->(s:Step) RETURN id(r), s.status`})
				if err != nil {
					t.Fatalf("QueryCypher: %v", err)
				}
				if len(res.Rows) != 1 || res.Rows[0][0] != "vl:run" || res.Rows[0][1] != "done" {
					t.Errorf("structural read = %v, want [[vl:run done]]", res.Rows)
				}
			})
		}
	}
}

// Never a vector-search candidate, under every scoring path — including the
// graph-proximity one, where Euclidean similarity would otherwise have scored
// a vectorless node -Inf.
func TestAVectorlessNodeIsNeverAScoredCandidate(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			mustNode(t, b.store, ctx, "vl:a", "a") // vector (1,0,0,0)
			if err := b.store.UpsertNode(ctx, &GraphNode{ID: "vl:bare", Content: "bare"}); err != nil {
				t.Fatalf("UpsertNode: %v", err)
			}
			mustEdge(t, b.store, ctx, "vl:e", "vl:a", "vl:bare", "next", nil)

			hits, err := b.store.HybridSearch(ctx, &HybridQuery{Vector: vec(), TopK: 10})
			if err != nil {
				t.Fatalf("HybridSearch: %v", err)
			}
			for _, h := range hits {
				if h.Node.ID == "vl:bare" {
					t.Errorf("vector search returned the vectorless node with score %v", h.VectorScore)
				}
			}

			// Reached through the graph, it still ranks — on structure alone.
			for name, sim := range map[string]core.SimilarityFunc{"cosine": core.CosineSimilarity, "euclidean": core.EuclideanDist} {
				host := b.store.store
				b.store.store = similarityHost{host, sim}
				hits, err = b.store.HybridSearch(ctx, &HybridQuery{Vector: vec(), StartNodeID: "vl:a", TopK: 10})
				b.store.store = host
				if err != nil {
					t.Fatalf("%s: HybridSearch from a start node: %v", name, err)
				}
				found := false
				for _, h := range hits {
					if math.IsInf(h.CombinedScore, 0) || math.IsNaN(h.CombinedScore) {
						t.Errorf("%s: %s has combined score %v", name, h.Node.ID, h.CombinedScore)
					}
					if h.Node.ID == "vl:bare" {
						found = true
						if h.VectorScore != 0 {
							t.Errorf("%s: vectorless node has vector score %v, want 0", name, h.VectorScore)
						}
					}
				}
				if !found {
					t.Errorf("%s: the vectorless neighbour was not reached through the graph", name)
				}
			}

			near, err := b.store.GraphVectorSearch(ctx, "vl:a", vec(), TraversalOptions{MaxDepth: 1, Direction: "out"})
			if err != nil {
				t.Fatalf("GraphVectorSearch: %v", err)
			}
			for _, h := range near {
				if h.Node.ID == "vl:bare" {
					t.Error("GraphVectorSearch scored the vectorless node")
				}
			}
			similar, err := b.store.SimilarityInGraph(ctx, "vl:a", core.SearchOptions{})
			if err != nil {
				t.Fatalf("SimilarityInGraph: %v", err)
			}
			for _, h := range similar {
				if h.Node.ID == "vl:bare" {
					t.Error("SimilarityInGraph compared against the vectorless node")
				}
			}
			if similar, err = b.store.SimilarityInGraph(ctx, "vl:bare", core.SearchOptions{}); err != nil || len(similar) != 0 {
				t.Errorf("SimilarityInGraph from a vectorless node = %d results (%v), want none", len(similar), err)
			}
			if _, err := b.store.PredictEdges(ctx, "vl:bare", 5); err != nil {
				t.Errorf("PredictEdges from a vectorless node: %v", err)
			}
		})
	}
}

// A node rewritten without its vector stops being found by the one it had —
// in the in-process HNSW index and in pgvector's table alike.
func TestANodeThatLosesItsVectorLeavesTheVectorIndexes(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			if err := b.store.EnableHNSWIndex(4); err != nil {
				t.Fatalf("EnableHNSWIndex: %v", err)
			}
			mustNode(t, b.store, ctx, "vl:was", "had a vector")
			if err := b.store.UpsertNode(ctx, &GraphNode{ID: "vl:was", Content: "has none now"}); err != nil {
				t.Fatalf("UpsertNode: %v", err)
			}

			if got := b.store.hnswIndex.index.Search(vec(), 10); len(got) != 0 {
				t.Errorf("HNSW still returns %d candidate(s) for a node without a vector", len(got))
			}
			if b.store.vecCap.Enabled {
				var n int
				if err := b.store.queryRow(ctx, `SELECT COUNT(*) FROM graph_node_vectors WHERE node_id = ?`, "vl:was").Scan(&n); err != nil {
					t.Fatalf("count mirrored vectors: %v", err)
				}
				if n != 0 {
					t.Errorf("pgvector still holds %d vector(s) for a node without one", n)
				}
			}
			hits, err := b.store.HybridSearch(ctx, &HybridQuery{Vector: vec(), TopK: 10})
			if err != nil {
				t.Fatalf("HybridSearch: %v", err)
			}
			if len(hits) != 0 {
				t.Errorf("vector search returned %d hit(s), want none", len(hits))
			}
		})
	}
}
