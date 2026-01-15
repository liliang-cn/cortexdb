package graph

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

func TestGraphBasicOperations(t *testing.T) {
	dbPath := fmt.Sprintf("test_graph_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, err := core.New(dbPath, 3)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	graph := NewGraphStore(store)

	// Initialize graph schema
	if err := graph.InitGraphSchema(ctx); err != nil {
		t.Fatalf("Failed to init graph schema: %v", err)
	}

	// Test node operations
	t.Run("NodeOperations", func(t *testing.T) {
		node1 := &GraphNode{
			ID:       "node1",
			Vector:   []float32{1.0, 2.0, 3.0},
			Content:  "First node",
			NodeType: "document",
			Properties: map[string]interface{}{
				"author": "Alice",
				"year":   2024,
			},
		}

		// Test upsert
		if err := graph.UpsertNode(ctx, node1); err != nil {
			t.Errorf("Failed to upsert node: %v", err)
		}

		// Test get
		retrieved, err := graph.GetNode(ctx, "node1")
		if err != nil {
			t.Errorf("Failed to get node: %v", err)
		}
		if retrieved.ID != node1.ID {
			t.Errorf("Expected ID %s, got %s", node1.ID, retrieved.ID)
		}
		if retrieved.Content != node1.Content {
			t.Errorf("Expected content %s, got %s", node1.Content, retrieved.Content)
		}

		// Test update
		node1.Content = "Updated content"
		if err := graph.UpsertNode(ctx, node1); err != nil {
			t.Errorf("Failed to update node: %v", err)
		}

		retrieved, err = graph.GetNode(ctx, "node1")
		if err != nil {
			t.Errorf("Failed to get updated node: %v", err)
		}
		if retrieved.Content != "Updated content" {
			t.Errorf("Expected updated content, got %s", retrieved.Content)
		}

		// Test delete
		if err := graph.DeleteNode(ctx, "node1"); err != nil {
			t.Errorf("Failed to delete node: %v", err)
		}

		_, err = graph.GetNode(ctx, "node1")
		if err == nil {
			t.Errorf("Expected error getting deleted node")
		}
	})

	// Test edge operations
	t.Run("EdgeOperations", func(t *testing.T) {
		// Create nodes first
		node1 := &GraphNode{
			ID:       "node1",
			Vector:   []float32{1.0, 2.0, 3.0},
			NodeType: "person",
		}
		node2 := &GraphNode{
			ID:       "node2",
			Vector:   []float32{4.0, 5.0, 6.0},
			NodeType: "person",
		}

		_ = graph.UpsertNode(ctx, node1)
		_ = graph.UpsertNode(ctx, node2)

		// Create edge
		edge := &GraphEdge{
			ID:         "edge1",
			FromNodeID: "node1",
			ToNodeID:   "node2",
			EdgeType:   "knows",
			Weight:     0.8,
			Properties: map[string]interface{}{
				"since": "2020",
			},
		}

		if err := graph.UpsertEdge(ctx, edge); err != nil {
			t.Errorf("Failed to create edge: %v", err)
		}

		// Get edges
		edges, err := graph.GetEdges(ctx, "node1", "out")
		if err != nil {
			t.Errorf("Failed to get edges: %v", err)
		}
		if len(edges) != 1 {
			t.Errorf("Expected 1 edge, got %d", len(edges))
		}
		if edges[0].EdgeType != "knows" {
			t.Errorf("Expected edge type 'knows', got %s", edges[0].EdgeType)
		}

		// Delete edge
		if err := graph.DeleteEdge(ctx, "edge1"); err != nil {
			t.Errorf("Failed to delete edge: %v", err)
		}

		edges, err = graph.GetEdges(ctx, "node1", "out")
		if err != nil {
			t.Errorf("Failed to get edges after delete: %v", err)
		}
		if len(edges) != 0 {
			t.Errorf("Expected 0 edges after delete, got %d", len(edges))
		}
	})
}

func TestGraphTraversal(t *testing.T) {
	dbPath := fmt.Sprintf("test_traversal_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, err := core.New(dbPath, 3)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	graph := NewGraphStore(store)
	if err := graph.InitGraphSchema(ctx); err != nil {
		t.Fatalf("Failed to init graph schema: %v", err)
	}

	// Create a simple graph: A -> B -> C
	//                         \-> D
	nodes := []*GraphNode{
		{ID: "A", Vector: []float32{1, 0, 0}, NodeType: "type1"},
		{ID: "B", Vector: []float32{0, 1, 0}, NodeType: "type1"},
		{ID: "C", Vector: []float32{0, 0, 1}, NodeType: "type2"},
		{ID: "D", Vector: []float32{1, 1, 0}, NodeType: "type2"},
	}

	for _, node := range nodes {
		if err := graph.UpsertNode(ctx, node); err != nil {
			t.Fatalf("Failed to create node %s: %v", node.ID, err)
		}
	}

	edges := []*GraphEdge{
		{ID: "e1", FromNodeID: "A", ToNodeID: "B", EdgeType: "connects"},
		{ID: "e2", FromNodeID: "B", ToNodeID: "C", EdgeType: "connects"},
		{ID: "e3", FromNodeID: "A", ToNodeID: "D", EdgeType: "connects"},
	}

	for _, edge := range edges {
		if err := graph.UpsertEdge(ctx, edge); err != nil {
			t.Fatalf("Failed to create edge %s: %v", edge.ID, err)
		}
	}

	t.Run("Neighbors", func(t *testing.T) {
		// Test 1-hop neighbors
		neighbors, err := graph.Neighbors(ctx, "A", TraversalOptions{
			MaxDepth:  1,
			Direction: "out",
		})
		if err != nil {
			t.Errorf("Failed to get neighbors: %v", err)
		}
		if len(neighbors) != 2 {
			t.Errorf("Expected 2 neighbors, got %d", len(neighbors))
		}

		// Test 2-hop neighbors
		neighbors, err = graph.Neighbors(ctx, "A", TraversalOptions{
			MaxDepth:  2,
			Direction: "out",
		})
		if err != nil {
			t.Errorf("Failed to get 2-hop neighbors: %v", err)
		}
		if len(neighbors) != 3 { // B, D from hop 1; C from hop 2
			t.Errorf("Expected 3 neighbors, got %d", len(neighbors))
		}
	})

	t.Run("ShortestPath", func(t *testing.T) {
		path, err := graph.ShortestPath(ctx, "A", "C")
		if err != nil {
			t.Errorf("Failed to find shortest path: %v", err)
		}
		if path.Distance != 2 {
			t.Errorf("Expected distance 2, got %d", path.Distance)
		}
		if len(path.Nodes) != 3 {
			t.Errorf("Expected 3 nodes in path, got %d", len(path.Nodes))
		}
		if path.Nodes[0].ID != "A" || path.Nodes[1].ID != "B" || path.Nodes[2].ID != "C" {
			t.Errorf("Unexpected path: %v", path.Nodes)
		}
	})

	t.Run("Connected", func(t *testing.T) {
		connected, err := graph.Connected(ctx, "A", "C", 3)
		if err != nil {
			t.Errorf("Failed to check connection: %v", err)
		}
		if !connected {
			t.Errorf("Expected A and C to be connected")
		}

		connected, err = graph.Connected(ctx, "C", "D", 1)
		if err != nil {
			t.Errorf("Failed to check connection: %v", err)
		}
		if connected {
			t.Errorf("Expected C and D to not be directly connected with max depth 1")
		}
	})
}

func TestHybridSearch(t *testing.T) {
	dbPath := fmt.Sprintf("test_hybrid_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, err := core.New(dbPath, 3)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	graph := NewGraphStore(store)
	if err := graph.InitGraphSchema(ctx); err != nil {
		t.Fatalf("Failed to init graph schema: %v", err)
	}

	// Create nodes with meaningful vectors
	nodes := []*GraphNode{
		{ID: "doc1", Vector: []float32{1.0, 0.0, 0.0}, Content: "Machine learning basics", NodeType: "document"},
		{ID: "doc2", Vector: []float32{0.9, 0.1, 0.0}, Content: "Deep learning intro", NodeType: "document"},
		{ID: "doc3", Vector: []float32{0.0, 1.0, 0.0}, Content: "Database systems", NodeType: "document"},
		{ID: "doc4", Vector: []float32{0.8, 0.2, 0.0}, Content: "Neural networks", NodeType: "document"},
	}

	for _, node := range nodes {
		if err := graph.UpsertNode(ctx, node); err != nil {
			t.Fatalf("Failed to create node: %v", err)
		}
	}

	// Create edges representing relationships
	edges := []*GraphEdge{
		{ID: "e1", FromNodeID: "doc1", ToNodeID: "doc2", EdgeType: "related", Weight: 0.9},
		{ID: "e2", FromNodeID: "doc2", ToNodeID: "doc4", EdgeType: "related", Weight: 0.8},
		{ID: "e3", FromNodeID: "doc1", ToNodeID: "doc4", EdgeType: "references", Weight: 0.7},
	}

	for _, edge := range edges {
		if err := graph.UpsertEdge(ctx, edge); err != nil {
			t.Fatalf("Failed to create edge: %v", err)
		}
	}

	t.Run("HybridSearchVectorOnly", func(t *testing.T) {
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
		if results[0].VectorScore < 0.99 {
			t.Errorf("Expected high vector score, got %f", results[0].VectorScore)
		}
	})

	t.Run("HybridSearchWithGraph", func(t *testing.T) {
		query := &HybridQuery{
			Vector:      []float32{1.0, 0.0, 0.0},
			StartNodeID: "doc1",
			TopK:        3,
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
			t.Errorf("Failed hybrid search: %v", err)
		}

		if len(results) == 0 {
			t.Errorf("Expected results, got none")
		}

		// Results should include graph-connected nodes
		foundDoc2 := false
		foundDoc4 := false
		for _, result := range results {
			if result.Node.ID == "doc2" {
				foundDoc2 = true
				if result.GraphScore == 0 {
					t.Errorf("Expected non-zero graph score for connected node")
				}
			}
			if result.Node.ID == "doc4" {
				foundDoc4 = true
			}
		}

		if !foundDoc2 {
			t.Errorf("Expected doc2 in results (connected to doc1)")
		}
		if !foundDoc4 {
			t.Errorf("Expected doc4 in results (2-hop from doc1)")
		}
	})

	t.Run("GraphVectorSearch", func(t *testing.T) {
		results, err := graph.GraphVectorSearch(ctx, "doc1", []float32{0.8, 0.2, 0.0}, TraversalOptions{
			MaxDepth:  2,
			Direction: "out",
		})
		if err != nil {
			t.Errorf("Failed graph vector search: %v", err)
		}

		if len(results) == 0 {
			t.Errorf("Expected results, got none")
		}

		// Should return neighbors sorted by vector similarity
		// doc4 vector [0.8, 0.2, 0.0] is exact match to query
		foundDoc4 := false
		for _, result := range results {
			if result.Node.ID == "doc4" {
				foundDoc4 = true
				if result.VectorScore < 0.99 {
					t.Errorf("Expected high similarity for doc4, got %f", result.VectorScore)
				}
				break
			}
		}

		if !foundDoc4 {
			t.Errorf("Expected doc4 in graph vector search results")
		}
	})
}