package graph

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkGraphNodeOperations(b *testing.B) {
	_, graph, cleanup := setupTestGraph(b)
	defer cleanup()
	
	ctx := context.Background()
	
	// Pre-create some nodes
	for i := 0; i < 100; i++ {
		node := &GraphNode{
			ID:     fmt.Sprintf("node_%d", i),
			Vector: []float32{float32(i), float32(i * 2), float32(i * 3)},
		}
		_ = graph.UpsertNode(ctx, node)
	}
	
	b.Run("UpsertNode", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			node := &GraphNode{
				ID:       fmt.Sprintf("bench_node_%d", i),
				Vector:   []float32{float32(i), float32(i + 1), float32(i + 2)},
				Content:  "Benchmark content",
				NodeType: "bench",
			}
			_ = graph.UpsertNode(ctx, node)
		}
	})
	
	b.Run("GetNode", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			nodeID := fmt.Sprintf("node_%d", i%100)
			_, _ = graph.GetNode(ctx, nodeID)
		}
	})
	
	b.Run("UpdateNode", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			nodeID := fmt.Sprintf("node_%d", i%100)
			node := &GraphNode{
				ID:      nodeID,
				Vector:  []float32{float32(i * 2), float32(i * 3), float32(i * 4)},
				Content: fmt.Sprintf("Updated content %d", i),
			}
			_ = graph.UpsertNode(ctx, node)
		}
	})
	
	b.Run("DeleteNode", func(b *testing.B) {
		// Pre-create nodes to delete
		for i := 0; i < b.N; i++ {
			node := &GraphNode{
				ID:     fmt.Sprintf("delete_node_%d", i),
				Vector: []float32{1, 2, 3},
			}
			_ = graph.UpsertNode(ctx, node)
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = graph.DeleteNode(ctx, fmt.Sprintf("delete_node_%d", i))
		}
	})
	
	b.Run("GetAllNodes", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.GetAllNodes(ctx, nil)
		}
	})
	
	b.Run("VectorSimilarity", func(b *testing.B) {
		// Similarity function is internal, skipping this benchmark
		b.Skip("Similarity function not accessible")
	})
}

func BenchmarkGraphEdgeOperations(b *testing.B) {
	_, graph, cleanup := setupTestGraph(b)
	defer cleanup()
	
	ctx := context.Background()
	
	// Pre-create nodes
	for i := 0; i < 100; i++ {
		node := &GraphNode{
			ID:     fmt.Sprintf("n%d", i),
			Vector: []float32{float32(i), 0, 0},
		}
		_ = graph.UpsertNode(ctx, node)
	}
	
	// Pre-create edges
	edgeCount := 0
	for i := 0; i < 100; i++ {
		for j := i + 1; j < i+5 && j < 100; j++ {
			edge := &GraphEdge{
				ID:         fmt.Sprintf("e%d", edgeCount),
				FromNodeID: fmt.Sprintf("n%d", i),
				ToNodeID:   fmt.Sprintf("n%d", j),
			}
			_ = graph.UpsertEdge(ctx, edge)
			edgeCount++
		}
	}
	
	b.Run("UpsertEdge", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			edge := &GraphEdge{
				ID:         fmt.Sprintf("bench_edge_%d", i),
				FromNodeID: fmt.Sprintf("n%d", i%100),
				ToNodeID:   fmt.Sprintf("n%d", (i+1)%100),
				EdgeType:   "bench",
				Weight:     0.5,
			}
			_ = graph.UpsertEdge(ctx, edge)
		}
	})
	
	b.Run("GetEdges", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			nodeID := fmt.Sprintf("n%d", i%100)
			_, _ = graph.GetEdges(ctx, nodeID, "out")
		}
	})
	
	b.Run("DeleteEdge", func(b *testing.B) {
		// Pre-create edges to delete
		for i := 0; i < b.N; i++ {
			edge := &GraphEdge{
				ID:         fmt.Sprintf("del_edge_%d", i),
				FromNodeID: "n0",
				ToNodeID:   "n1",
			}
			_ = graph.UpsertEdge(ctx, edge)
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = graph.DeleteEdge(ctx, fmt.Sprintf("del_edge_%d", i))
		}
	})
}

func BenchmarkGraphTraversal(b *testing.B) {
	_, graph, cleanup := setupTestGraph(b)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create a larger graph for traversal benchmarks
	nodeCount := 1000
	for i := 0; i < nodeCount; i++ {
		node := &GraphNode{
			ID:     fmt.Sprintf("node_%d", i),
			Vector: []float32{float32(i % 10), float32(i % 20), float32(i % 30)},
		}
		_ = graph.UpsertNode(ctx, node)
	}
	
	// Create edges (each node connects to next 3)
	for i := 0; i < nodeCount; i++ {
		for j := 1; j <= 3; j++ {
			if i+j < nodeCount {
				edge := &GraphEdge{
					ID:         fmt.Sprintf("edge_%d_%d", i, i+j),
					FromNodeID: fmt.Sprintf("node_%d", i),
					ToNodeID:   fmt.Sprintf("node_%d", i+j),
				}
				_ = graph.UpsertEdge(ctx, edge)
			}
		}
	}
	
	b.Run("Neighbors_1Hop", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.Neighbors(ctx, "node_500", TraversalOptions{
				MaxDepth:  1,
				Direction: "out",
			})
		}
	})
	
	b.Run("Neighbors_2Hop", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.Neighbors(ctx, "node_500", TraversalOptions{
				MaxDepth:  2,
				Direction: "out",
			})
		}
	})
	
	b.Run("Neighbors_3Hop", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.Neighbors(ctx, "node_500", TraversalOptions{
				MaxDepth:  3,
				Direction: "out",
			})
		}
	})
	
	b.Run("ShortestPath", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.ShortestPath(ctx, "node_100", "node_110")
		}
	})
	
	b.Run("Connected", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.Connected(ctx, "node_100", "node_200", 5)
		}
	})
	
	b.Run("Subgraph", func(b *testing.B) {
		nodeIDs := []string{
			"node_100", "node_101", "node_102", "node_103", "node_104",
			"node_105", "node_106", "node_107", "node_108", "node_109",
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.Subgraph(ctx, nodeIDs)
		}
	})
}

func BenchmarkHybridSearch(b *testing.B) {
	_, graph, cleanup := setupTestGraph(b)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create nodes
	for i := 0; i < 500; i++ {
		node := &GraphNode{
			ID:       fmt.Sprintf("doc_%d", i),
			Vector:   []float32{float32(i % 10) / 10, float32(i % 20) / 20, float32(i % 30) / 30},
			Content:  fmt.Sprintf("Document %d", i),
			NodeType: "document",
		}
		_ = graph.UpsertNode(ctx, node)
	}
	
	// Create edges
	for i := 0; i < 500; i++ {
		for j := 1; j <= 2; j++ {
			if i+j < 500 {
				edge := &GraphEdge{
					ID:         fmt.Sprintf("e_%d_%d", i, i+j),
					FromNodeID: fmt.Sprintf("doc_%d", i),
					ToNodeID:   fmt.Sprintf("doc_%d", i+j),
					EdgeType:   "related",
					Weight:     0.8,
				}
				_ = graph.UpsertEdge(ctx, edge)
			}
		}
	}
	
	queryVector := []float32{0.5, 0.5, 0.5}
	
	b.Run("VectorOnly", func(b *testing.B) {
		query := &HybridQuery{
			Vector: queryVector,
			TopK:   10,
			Weights: HybridWeights{
				VectorWeight: 1.0,
				GraphWeight:  0.0,
				EdgeWeight:   0.0,
			},
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.HybridSearch(ctx, query)
		}
	})
	
	b.Run("GraphOnly", func(b *testing.B) {
		query := &HybridQuery{
			StartNodeID: "doc_250",
			TopK:        10,
			Weights: HybridWeights{
				VectorWeight: 0.0,
				GraphWeight:  1.0,
				EdgeWeight:   0.0,
			},
			GraphFilter: &GraphFilter{
				MaxDepth: 2,
			},
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.HybridSearch(ctx, query)
		}
	})
	
	b.Run("Combined", func(b *testing.B) {
		query := &HybridQuery{
			Vector:      queryVector,
			StartNodeID: "doc_250",
			TopK:        10,
			Weights: HybridWeights{
				VectorWeight: 0.5,
				GraphWeight:  0.3,
				EdgeWeight:   0.2,
			},
			GraphFilter: &GraphFilter{
				MaxDepth: 2,
			},
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.HybridSearch(ctx, query)
		}
	})
	
	b.Run("GraphVectorSearch", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.GraphVectorSearch(ctx, "doc_250", queryVector, TraversalOptions{
				MaxDepth:  2,
				Direction: "out",
			})
		}
	})
	
	b.Run("SimilarityInGraph", func(b *testing.B) {
		// SimilarityInGraph function needs to be implemented or available
		// Skipping for now
		b.Skip("SimilarityInGraph not yet implemented")
	})
}

func BenchmarkGraphAlgorithms(b *testing.B) {
	_, graph, cleanup := setupTestGraph(b)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create a medium-sized graph
	nodeCount := 100
	for i := 0; i < nodeCount; i++ {
		node := &GraphNode{
			ID:     fmt.Sprintf("n%d", i),
			Vector: []float32{float32(i), float32(i * 2), float32(i * 3)},
		}
		_ = graph.UpsertNode(ctx, node)
	}
	
	// Create random edges
	for i := 0; i < nodeCount*3; i++ {
		edge := &GraphEdge{
			ID:         fmt.Sprintf("e%d", i),
			FromNodeID: fmt.Sprintf("n%d", i%nodeCount),
			ToNodeID:   fmt.Sprintf("n%d", (i*7)%nodeCount),
			Weight:     0.5 + float64(i%10)/20,
		}
		_ = graph.UpsertEdge(ctx, edge)
	}
	
	b.Run("PageRank", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.PageRank(ctx, 50, 0.85)
		}
	})
	
	b.Run("CommunityDetection", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.CommunityDetection(ctx)
		}
	})
	
	b.Run("PredictEdges", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.PredictEdges(ctx, fmt.Sprintf("n%d", i%nodeCount), 5)
		}
	})
	
	b.Run("GetGraphStatistics", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.GetGraphStatistics(ctx)
		}
	})
}

func BenchmarkLargeGraph(b *testing.B) {
	_, graph, cleanup := setupTestGraph(b)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create a large graph for stress testing
	nodeCount := 10000
	
	b.Run("CreateLargeGraph", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Create nodes
			for j := 0; j < nodeCount/b.N; j++ {
				idx := i*nodeCount/b.N + j
				node := &GraphNode{
					ID:     fmt.Sprintf("large_n%d", idx),
					Vector: []float32{float32(idx % 100), float32(idx % 200), float32(idx % 300)},
				}
				_ = graph.UpsertNode(ctx, node)
			}
		}
	})
	
	// Actually create the graph for following benchmarks
	for i := 0; i < 1000; i++ {
		node := &GraphNode{
			ID:     fmt.Sprintf("test_n%d", i),
			Vector: []float32{float32(i % 100), float32(i % 200), float32(i % 300)},
		}
		_ = graph.UpsertNode(ctx, node)
	}
	
	// Create edges
	for i := 0; i < 1000; i++ {
		for j := 1; j <= 5; j++ {
			if i+j < 1000 {
				edge := &GraphEdge{
					ID:         fmt.Sprintf("test_e%d_%d", i, j),
					FromNodeID: fmt.Sprintf("test_n%d", i),
					ToNodeID:   fmt.Sprintf("test_n%d", i+j),
				}
				_ = graph.UpsertEdge(ctx, edge)
			}
		}
	}
	
	b.Run("LargeGraphTraversal", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.Neighbors(ctx, "test_n500", TraversalOptions{
				MaxDepth:  3,
				Direction: "out",
			})
		}
	})
	
	b.Run("LargeGraphSearch", func(b *testing.B) {
		query := &HybridQuery{
			Vector: []float32{50, 100, 150},
			TopK:   20,
			Weights: HybridWeights{
				VectorWeight: 0.7,
				GraphWeight:  0.3,
			},
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = graph.HybridSearch(ctx, query)
		}
	})
}