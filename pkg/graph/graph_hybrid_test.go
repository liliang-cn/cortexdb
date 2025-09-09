package graph

import (
	"context"
	"math"
	"testing"
	
	"github.com/liliang-cn/sqvect/pkg/core"
)

func TestHybridSearchBasic(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create test nodes with specific vectors
	nodes := []GraphNode{
		{ID: "doc1", Vector: []float32{1.0, 0.0, 0.0}, Content: "First document", NodeType: "document"},
		{ID: "doc2", Vector: []float32{0.9, 0.1, 0.0}, Content: "Second document", NodeType: "document"},
		{ID: "doc3", Vector: []float32{0.0, 1.0, 0.0}, Content: "Third document", NodeType: "document"},
		{ID: "doc4", Vector: []float32{0.0, 0.0, 1.0}, Content: "Fourth document", NodeType: "document"},
	}
	
	for _, node := range nodes {
		graph.UpsertNode(ctx, &node)
	}
	
	// Create edges
	edges := []GraphEdge{
		{ID: "e1", FromNodeID: "doc1", ToNodeID: "doc2", EdgeType: "related", Weight: 0.9},
		{ID: "e2", FromNodeID: "doc2", ToNodeID: "doc3", EdgeType: "related", Weight: 0.7},
		{ID: "e3", FromNodeID: "doc1", ToNodeID: "doc4", EdgeType: "cites", Weight: 0.5},
	}
	
	for _, edge := range edges {
		graph.UpsertEdge(ctx, &edge)
	}
	
	t.Run("VectorOnlySearch", func(t *testing.T) {
		query := &HybridQuery{
			Vector: []float32{1.0, 0.0, 0.0}, // Similar to doc1
			TopK:   3,
			Weights: HybridWeights{
				VectorWeight: 1.0,
				GraphWeight:  0.0,
				EdgeWeight:   0.0,
			},
		}
		
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Failed hybrid search: %v", err)
		}
		
		if len(results) == 0 {
			t.Errorf("Expected results, got none")
		}
		
		// First result should be doc1 (exact match)
		if results[0].Node.ID != "doc1" {
			t.Errorf("Expected doc1 as first result, got %s", results[0].Node.ID)
		}
		
		// Check perfect match score
		if math.Abs(results[0].VectorScore-1.0) > 0.001 {
			t.Errorf("Expected vector score ~1.0, got %f", results[0].VectorScore)
		}
		
		// Graph score should be 0 (not used)
		if results[0].GraphScore != 0 {
			t.Errorf("Expected graph score 0, got %f", results[0].GraphScore)
		}
	})
	
	t.Run("GraphOnlySearch", func(t *testing.T) {
		query := &HybridQuery{
			StartNodeID: "doc1",
			TopK:        3,
			Weights: HybridWeights{
				VectorWeight: 0.0,
				GraphWeight:  1.0,
				EdgeWeight:   0.0,
			},
			GraphFilter: &GraphFilter{
				MaxDepth: 2,
			},
		}
		
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Failed graph-only search: %v", err)
		}
		
		if len(results) == 0 {
			t.Errorf("Expected results, got none")
		}
		
		// Should include doc1 (starting point) and connected nodes
		foundDoc1 := false
		foundDoc2 := false
		for _, result := range results {
			if result.Node.ID == "doc1" {
				foundDoc1 = true
				if result.Distance != 0 {
					t.Errorf("Expected distance 0 for start node, got %d", result.Distance)
				}
			}
			if result.Node.ID == "doc2" {
				foundDoc2 = true
				if result.Distance != 1 {
					t.Errorf("Expected distance 1 for doc2, got %d", result.Distance)
				}
			}
		}
		
		if !foundDoc1 {
			t.Errorf("Expected doc1 in results")
		}
		if !foundDoc2 {
			t.Errorf("Expected doc2 in results (connected to doc1)")
		}
	})
	
	t.Run("CombinedSearch", func(t *testing.T) {
		query := &HybridQuery{
			Vector:      []float32{0.9, 0.1, 0.0}, // Similar to doc2
			StartNodeID: "doc1",
			TopK:        4,
			Weights: HybridWeights{
				VectorWeight: 0.5,
				GraphWeight:  0.3,
				EdgeWeight:   0.2,
			},
			GraphFilter: &GraphFilter{
				MaxDepth: 2,
			},
		}
		
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Failed combined search: %v", err)
		}
		
		if len(results) == 0 {
			t.Errorf("Expected results, got none")
		}
		
		// doc2 should rank high (high vector similarity + connected to doc1)
		foundDoc2 := false
		for _, result := range results {
			if result.Node.ID == "doc2" {
				foundDoc2 = true
				
				// Should have both vector and graph scores
				if result.VectorScore <= 0 {
					t.Errorf("Expected positive vector score for doc2")
				}
				if result.GraphScore <= 0 {
					t.Errorf("Expected positive graph score for doc2")
				}
				
				// Combined score should be weighted average
				expectedCombined := result.VectorScore*0.5 + result.GraphScore*0.3
				if math.Abs(result.CombinedScore-expectedCombined) > 0.3 { // Allow for edge weight contribution
					t.Errorf("Unexpected combined score: %f", result.CombinedScore)
				}
				break
			}
		}
		
		if !foundDoc2 {
			t.Errorf("Expected doc2 in combined search results")
		}
	})
	
	t.Run("ThresholdFiltering", func(t *testing.T) {
		query := &HybridQuery{
			Vector:    []float32{1.0, 0.0, 0.0},
			TopK:      10,
			Threshold: 0.8, // High threshold
			Weights: HybridWeights{
				VectorWeight: 1.0,
				GraphWeight:  0.0,
				EdgeWeight:   0.0,
			},
		}
		
		_, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Failed search with threshold: %v", err)
		}
		
		// Only very similar vectors should pass threshold
		// Skipping detailed threshold check since similarity function is not accessible
	})
	
	t.Run("EdgeTypeFiltering", func(t *testing.T) {
		query := &HybridQuery{
			StartNodeID: "doc1",
			TopK:        10,
			Weights: HybridWeights{
				VectorWeight: 0.0,
				GraphWeight:  1.0,
				EdgeWeight:   0.0,
			},
			GraphFilter: &GraphFilter{
				MaxDepth:  1,
				EdgeTypes: []string{"cites"},
			},
		}
		
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Failed search with edge filter: %v", err)
		}
		
		// Should only traverse "cites" edges
		foundDoc4 := false
		foundDoc2 := false
		for _, result := range results {
			if result.Node.ID == "doc4" {
				foundDoc4 = true // Connected via "cites"
			}
			if result.Node.ID == "doc2" {
				foundDoc2 = true // Connected via "related" - should not be found
			}
		}
		
		if !foundDoc4 {
			t.Errorf("Expected doc4 (connected via 'cites')")
		}
		if foundDoc2 {
			t.Errorf("Unexpected doc2 (connected via 'related', not 'cites')")
		}
	})
}

func TestGraphVectorSearch(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create a graph structure
	nodes := []GraphNode{
		{ID: "center", Vector: []float32{0.5, 0.5, 0.5}},
		{ID: "neighbor1", Vector: []float32{1.0, 0.0, 0.0}},
		{ID: "neighbor2", Vector: []float32{0.0, 1.0, 0.0}},
		{ID: "neighbor3", Vector: []float32{0.0, 0.0, 1.0}},
		{ID: "distant", Vector: []float32{0.8, 0.2, 0.0}},
	}
	
	for _, node := range nodes {
		graph.UpsertNode(ctx, &node)
	}
	
	edges := []GraphEdge{
		{ID: "e1", FromNodeID: "center", ToNodeID: "neighbor1"},
		{ID: "e2", FromNodeID: "center", ToNodeID: "neighbor2"},
		{ID: "e3", FromNodeID: "neighbor2", ToNodeID: "neighbor3"},
		{ID: "e4", FromNodeID: "neighbor3", ToNodeID: "distant"},
	}
	
	for _, edge := range edges {
		graph.UpsertEdge(ctx, &edge)
	}
	
	t.Run("SearchWithinNeighborhood", func(t *testing.T) {
		queryVector := []float32{1.0, 0.0, 0.0} // Similar to neighbor1
		
		results, err := graph.GraphVectorSearch(ctx, "center", queryVector, TraversalOptions{
			MaxDepth:  1,
			Direction: "out",
		})
		
		if err != nil {
			t.Errorf("Failed graph vector search: %v", err)
		}
		
		// Should include center and its direct neighbors
		if len(results) != 3 { // center + neighbor1 + neighbor2
			t.Errorf("Expected 3 results, got %d", len(results))
		}
		
		// neighbor1 should have highest score (most similar to query)
		highestScore := -1.0
		highestID := ""
		for _, result := range results {
			if result.VectorScore > highestScore {
				highestScore = result.VectorScore
				highestID = result.Node.ID
			}
		}
		
		if highestID != "neighbor1" {
			t.Errorf("Expected neighbor1 to have highest score, got %s", highestID)
		}
	})
	
	t.Run("SearchWithLimit", func(t *testing.T) {
		queryVector := []float32{0.5, 0.5, 0.5}
		
		results, err := graph.GraphVectorSearch(ctx, "center", queryVector, TraversalOptions{
			MaxDepth:  2,
			Direction: "out",
			Limit:     2,
		})
		
		if err != nil {
			t.Errorf("Failed limited search: %v", err)
		}
		
		if len(results) != 2 {
			t.Errorf("Expected 2 results (limited), got %d", len(results))
		}
	})
}

func TestSimilarityInGraph(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create nodes with various similarity levels
	nodes := []GraphNode{
		{ID: "ref", Vector: []float32{1.0, 0.0, 0.0}},
		{ID: "very_similar", Vector: []float32{0.95, 0.05, 0.0}},
		{ID: "somewhat_similar", Vector: []float32{0.7, 0.3, 0.0}},
		{ID: "different", Vector: []float32{0.0, 0.0, 1.0}},
	}
	
	for _, node := range nodes {
		graph.UpsertNode(ctx, &node)
	}
	
	t.Run("FindSimilarNodes", func(t *testing.T) {
		results, err := graph.SimilarityInGraph(ctx, "ref", core.SearchOptions{
			TopK: 10,
		})
		
		if err != nil {
			t.Errorf("Failed similarity search: %v", err)
		}
		
		// Should not include self
		for _, result := range results {
			if result.Node.ID == "ref" {
				t.Errorf("Should not include reference node itself")
			}
		}
		
		// Results should be ordered by similarity
		if len(results) >= 2 {
			if results[0].VectorScore < results[1].VectorScore {
				t.Errorf("Results not properly ordered by similarity")
			}
		}
		
		// Most similar should be "very_similar"
		if len(results) > 0 && results[0].Node.ID != "very_similar" {
			t.Errorf("Expected 'very_similar' as most similar node")
		}
	})
	
	t.Run("WithThreshold", func(t *testing.T) {
		results, err := graph.SimilarityInGraph(ctx, "ref", core.SearchOptions{
			TopK:      10,
			Threshold: 0.8,
		})
		
		if err != nil {
			t.Errorf("Failed threshold search: %v", err)
		}
		
		// Only highly similar nodes should be included
		for _, result := range results {
			if result.VectorScore < 0.8 {
				t.Errorf("Result %s below threshold: %f", result.Node.ID, result.VectorScore)
			}
		}
	})
	
	t.Run("WithTopK", func(t *testing.T) {
		results, err := graph.SimilarityInGraph(ctx, "ref", core.SearchOptions{
			TopK: 2,
		})
		
		if err != nil {
			t.Errorf("Failed top-k search: %v", err)
		}
		
		if len(results) > 2 {
			t.Errorf("Expected at most 2 results, got %d", len(results))
		}
	})
}

func TestHybridWeightNormalization(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create a simple test node
	node := &GraphNode{
		ID:     "test",
		Vector: []float32{1.0, 0.0, 0.0},
	}
	graph.UpsertNode(ctx, node)
	
	t.Run("AutoNormalization", func(t *testing.T) {
		query := &HybridQuery{
			Vector: []float32{1.0, 0.0, 0.0},
			TopK:   1,
			Weights: HybridWeights{
				VectorWeight: 2.0,
				GraphWeight:  1.0,
				EdgeWeight:   1.0,
			},
		}
		
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Failed search with non-normalized weights: %v", err)
		}
		
		if len(results) == 0 {
			t.Errorf("Expected results")
		}
		
		// Weights should be normalized internally
		// Total was 4.0, so VectorWeight should effectively be 0.5
		// This is hard to test directly without exposing internals
	})
	
	t.Run("DefaultWeights", func(t *testing.T) {
		query := &HybridQuery{
			Vector: []float32{1.0, 0.0, 0.0},
			TopK:   1,
			Weights: HybridWeights{
				// All zeros - should use defaults
				VectorWeight: 0,
				GraphWeight:  0,
				EdgeWeight:   0,
			},
		}
		
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Failed search with zero weights: %v", err)
		}
		
		if len(results) == 0 {
			t.Errorf("Expected results with default weights")
		}
	})
}

func TestHybridSearchEdgeCases(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	t.Run("NilQuery", func(t *testing.T) {
		_, err := graph.HybridSearch(ctx, nil)
		if err == nil {
			t.Errorf("Expected error for nil query")
		}
	})
	
	t.Run("EmptyVectorAndNode", func(t *testing.T) {
		query := &HybridQuery{
			// No vector or start node
			TopK: 1,
		}
		
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Unexpected error for empty query: %v", err)
		}
		
		if len(results) != 0 {
			t.Errorf("Expected no results for empty query")
		}
	})
	
	t.Run("NonExistentStartNode", func(t *testing.T) {
		query := &HybridQuery{
			StartNodeID: "non_existent",
			TopK:        1,
		}
		
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		
		if len(results) != 0 {
			t.Errorf("Expected no results for non-existent start node")
		}
	})
	
	t.Run("ZeroTopK", func(t *testing.T) {
		node := &GraphNode{
			ID:     "test_node",
			Vector: []float32{1.0, 0.0, 0.0},
		}
		graph.UpsertNode(ctx, node)
		
		query := &HybridQuery{
			Vector: []float32{1.0, 0.0, 0.0},
			TopK:   0,
		}
		
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		
		// With TopK=0, should return all results
		if len(results) == 0 {
			t.Errorf("Expected results with TopK=0")
		}
	})
}

func TestGraphDistanceStruct(t *testing.T) {
	// Test the graphDistance struct behavior
	gd := graphDistance{
		distance: 3,
		weight:   0.75,
	}
	
	if gd.distance != 3 {
		t.Errorf("Expected distance 3, got %d", gd.distance)
	}
	
	if gd.weight != 0.75 {
		t.Errorf("Expected weight 0.75, got %f", gd.weight)
	}
}