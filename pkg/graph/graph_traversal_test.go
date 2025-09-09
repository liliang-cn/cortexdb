package graph

import (
	"context"
	"testing"
)

func TestNeighbors(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create a test graph:
	//     A
	//    / \
	//   B   C
	//  / \   \
	// D   E   F
	
	nodes := []GraphNode{
		{ID: "A", Vector: []float32{1, 0, 0}},
		{ID: "B", Vector: []float32{0, 1, 0}},
		{ID: "C", Vector: []float32{0, 0, 1}},
		{ID: "D", Vector: []float32{1, 1, 0}},
		{ID: "E", Vector: []float32{1, 0, 1}},
		{ID: "F", Vector: []float32{0, 1, 1}},
	}
	
	for _, node := range nodes {
		graph.UpsertNode(ctx, &node)
	}
	
	edges := []GraphEdge{
		{ID: "e1", FromNodeID: "A", ToNodeID: "B"},
		{ID: "e2", FromNodeID: "A", ToNodeID: "C"},
		{ID: "e3", FromNodeID: "B", ToNodeID: "D"},
		{ID: "e4", FromNodeID: "B", ToNodeID: "E"},
		{ID: "e5", FromNodeID: "C", ToNodeID: "F"},
	}
	
	for _, edge := range edges {
		graph.UpsertEdge(ctx, &edge)
	}
	
	t.Run("OneHopNeighbors", func(t *testing.T) {
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
		
		// Check that B and C are neighbors
		foundB, foundC := false, false
		for _, n := range neighbors {
			if n.ID == "B" {
				foundB = true
			}
			if n.ID == "C" {
				foundC = true
			}
		}
		
		if !foundB || !foundC {
			t.Errorf("Expected B and C as neighbors")
		}
	})
	
	t.Run("TwoHopNeighbors", func(t *testing.T) {
		neighbors, err := graph.Neighbors(ctx, "A", TraversalOptions{
			MaxDepth:  2,
			Direction: "out",
		})
		
		if err != nil {
			t.Errorf("Failed to get neighbors: %v", err)
		}
		
		// Should get B, C (1-hop) and D, E, F (2-hop)
		if len(neighbors) != 5 {
			t.Errorf("Expected 5 neighbors, got %d", len(neighbors))
		}
	})
	
	t.Run("IncomingNeighbors", func(t *testing.T) {
		neighbors, err := graph.Neighbors(ctx, "D", TraversalOptions{
			MaxDepth:  1,
			Direction: "in",
		})
		
		if err != nil {
			t.Errorf("Failed to get incoming neighbors: %v", err)
		}
		
		if len(neighbors) != 1 {
			t.Errorf("Expected 1 incoming neighbor, got %d", len(neighbors))
		}
		
		if neighbors[0].ID != "B" {
			t.Errorf("Expected B as incoming neighbor, got %s", neighbors[0].ID)
		}
	})
	
	t.Run("BothDirections", func(t *testing.T) {
		neighbors, err := graph.Neighbors(ctx, "B", TraversalOptions{
			MaxDepth:  1,
			Direction: "both",
		})
		
		if err != nil {
			t.Errorf("Failed to get neighbors (both): %v", err)
		}
		
		// B has A (incoming), D and E (outgoing)
		if len(neighbors) != 3 {
			t.Errorf("Expected 3 neighbors, got %d", len(neighbors))
		}
	})
	
	t.Run("WithLimit", func(t *testing.T) {
		neighbors, err := graph.Neighbors(ctx, "A", TraversalOptions{
			MaxDepth:  2,
			Direction: "out",
			Limit:     3,
		})
		
		if err != nil {
			t.Errorf("Failed to get neighbors with limit: %v", err)
		}
		
		if len(neighbors) != 3 {
			t.Errorf("Expected 3 neighbors (limited), got %d", len(neighbors))
		}
	})
	
	t.Run("WithEdgeTypeFilter", func(t *testing.T) {
		// Add typed edges
		typedEdge := &GraphEdge{
			ID:         "typed_edge",
			FromNodeID: "A",
			ToNodeID:   "D",
			EdgeType:   "special",
		}
		graph.UpsertEdge(ctx, typedEdge)
		
		neighbors, err := graph.Neighbors(ctx, "A", TraversalOptions{
			MaxDepth:  1,
			Direction: "out",
			EdgeTypes: []string{"special"},
		})
		
		if err != nil {
			t.Errorf("Failed to get neighbors with edge filter: %v", err)
		}
		
		if len(neighbors) != 1 {
			t.Errorf("Expected 1 neighbor with special edge, got %d", len(neighbors))
		}
		
		if neighbors[0].ID != "D" {
			t.Errorf("Expected D as neighbor with special edge")
		}
	})
	
	t.Run("WithNodeTypeFilter", func(t *testing.T) {
		// Update some nodes with types
		nodeB := &GraphNode{
			ID:       "B",
			Vector:   []float32{0, 1, 0},
			NodeType: "type1",
		}
		nodeC := &GraphNode{
			ID:       "C",
			Vector:   []float32{0, 0, 1},
			NodeType: "type2",
		}
		graph.UpsertNode(ctx, nodeB)
		graph.UpsertNode(ctx, nodeC)
		
		neighbors, err := graph.Neighbors(ctx, "A", TraversalOptions{
			MaxDepth:  1,
			Direction: "out",
			NodeTypes: []string{"type1"},
		})
		
		if err != nil {
			t.Errorf("Failed to get neighbors with node filter: %v", err)
		}
		
		if len(neighbors) != 1 {
			t.Errorf("Expected 1 neighbor of type1, got %d", len(neighbors))
		}
		
		if neighbors[0].ID != "B" {
			t.Errorf("Expected B as neighbor of type1")
		}
	})
}

func TestShortestPath(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create a test graph with multiple paths:
	//     A ---> B ---> C
	//     |             ^
	//     |             |
	//     +---> D ------+
	
	nodes := []GraphNode{
		{ID: "A", Vector: []float32{1, 0, 0}},
		{ID: "B", Vector: []float32{0, 1, 0}},
		{ID: "C", Vector: []float32{0, 0, 1}},
		{ID: "D", Vector: []float32{1, 1, 1}},
	}
	
	for _, node := range nodes {
		graph.UpsertNode(ctx, &node)
	}
	
	edges := []GraphEdge{
		{ID: "e1", FromNodeID: "A", ToNodeID: "B", Weight: 1.0},
		{ID: "e2", FromNodeID: "B", ToNodeID: "C", Weight: 1.0},
		{ID: "e3", FromNodeID: "A", ToNodeID: "D", Weight: 1.0},
		{ID: "e4", FromNodeID: "D", ToNodeID: "C", Weight: 1.0},
	}
	
	for _, edge := range edges {
		graph.UpsertEdge(ctx, &edge)
	}
	
	t.Run("DirectPath", func(t *testing.T) {
		path, err := graph.ShortestPath(ctx, "A", "B")
		if err != nil {
			t.Errorf("Failed to find path: %v", err)
		}
		
		if path.Distance != 1 {
			t.Errorf("Expected distance 1, got %d", path.Distance)
		}
		
		if len(path.Nodes) != 2 {
			t.Errorf("Expected 2 nodes in path, got %d", len(path.Nodes))
		}
		
		if path.Nodes[0].ID != "A" || path.Nodes[1].ID != "B" {
			t.Errorf("Unexpected path: %v", path.Nodes)
		}
	})
	
	t.Run("MultiHopPath", func(t *testing.T) {
		path, err := graph.ShortestPath(ctx, "A", "C")
		if err != nil {
			t.Errorf("Failed to find path: %v", err)
		}
		
		if path.Distance != 2 {
			t.Errorf("Expected distance 2, got %d", path.Distance)
		}
		
		if len(path.Nodes) != 3 {
			t.Errorf("Expected 3 nodes in path, got %d", len(path.Nodes))
		}
	})
	
	t.Run("SameNodePath", func(t *testing.T) {
		path, err := graph.ShortestPath(ctx, "A", "A")
		if err != nil {
			t.Errorf("Failed to find path: %v", err)
		}
		
		if path.Distance != 0 {
			t.Errorf("Expected distance 0 for same node, got %d", path.Distance)
		}
		
		if len(path.Nodes) != 1 {
			t.Errorf("Expected 1 node in path, got %d", len(path.Nodes))
		}
	})
	
	t.Run("NoPath", func(t *testing.T) {
		// Add disconnected node
		isolatedNode := &GraphNode{
			ID:     "Isolated",
			Vector: []float32{5, 5, 5},
		}
		graph.UpsertNode(ctx, isolatedNode)
		
		_, err := graph.ShortestPath(ctx, "A", "Isolated")
		if err == nil {
			t.Errorf("Expected error for no path")
		}
	})
}

func TestSubgraph(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create test graph
	nodes := []GraphNode{
		{ID: "N1", Vector: []float32{1, 0, 0}},
		{ID: "N2", Vector: []float32{0, 1, 0}},
		{ID: "N3", Vector: []float32{0, 0, 1}},
		{ID: "N4", Vector: []float32{1, 1, 1}},
	}
	
	for _, node := range nodes {
		graph.UpsertNode(ctx, &node)
	}
	
	edges := []GraphEdge{
		{ID: "e1", FromNodeID: "N1", ToNodeID: "N2"},
		{ID: "e2", FromNodeID: "N2", ToNodeID: "N3"},
		{ID: "e3", FromNodeID: "N3", ToNodeID: "N4"},
		{ID: "e4", FromNodeID: "N1", ToNodeID: "N3"},
	}
	
	for _, edge := range edges {
		graph.UpsertEdge(ctx, &edge)
	}
	
	t.Run("ExtractSubgraph", func(t *testing.T) {
		subgraph, err := graph.Subgraph(ctx, []string{"N1", "N2", "N3"})
		if err != nil {
			t.Errorf("Failed to extract subgraph: %v", err)
		}
		
		if len(subgraph.Nodes) != 3 {
			t.Errorf("Expected 3 nodes in subgraph, got %d", len(subgraph.Nodes))
		}
		
		// Should have edges e1 (N1->N2), e2 (N2->N3), and e4 (N1->N3)
		// But not e3 (N3->N4) because N4 is not in subgraph
		if len(subgraph.Edges) != 3 {
			t.Errorf("Expected 3 edges in subgraph, got %d", len(subgraph.Edges))
		}
		
		// Verify no edge points to N4
		for _, edge := range subgraph.Edges {
			if edge.ToNodeID == "N4" || edge.FromNodeID == "N4" {
				t.Errorf("Subgraph should not contain edges to N4")
			}
		}
	})
	
	t.Run("EmptySubgraph", func(t *testing.T) {
		subgraph, err := graph.Subgraph(ctx, []string{})
		if err != nil {
			t.Errorf("Failed to extract empty subgraph: %v", err)
		}
		
		if len(subgraph.Nodes) != 0 {
			t.Errorf("Expected 0 nodes in empty subgraph, got %d", len(subgraph.Nodes))
		}
		
		if len(subgraph.Edges) != 0 {
			t.Errorf("Expected 0 edges in empty subgraph, got %d", len(subgraph.Edges))
		}
	})
	
	t.Run("NonExistentNodes", func(t *testing.T) {
		subgraph, err := graph.Subgraph(ctx, []string{"N1", "NonExistent"})
		if err != nil {
			t.Errorf("Failed to extract subgraph: %v", err)
		}
		
		// Should only have N1
		if len(subgraph.Nodes) != 1 {
			t.Errorf("Expected 1 node in subgraph, got %d", len(subgraph.Nodes))
		}
		
		if subgraph.Nodes[0].ID != "N1" {
			t.Errorf("Expected N1 in subgraph")
		}
	})
}

func TestConnected(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create two disconnected components:
	// Component 1: A <-> B <-> C
	// Component 2: D <-> E
	
	nodes := []GraphNode{
		{ID: "A", Vector: []float32{1, 0, 0}},
		{ID: "B", Vector: []float32{0, 1, 0}},
		{ID: "C", Vector: []float32{0, 0, 1}},
		{ID: "D", Vector: []float32{1, 1, 0}},
		{ID: "E", Vector: []float32{0, 1, 1}},
	}
	
	for _, node := range nodes {
		graph.UpsertNode(ctx, &node)
	}
	
	edges := []GraphEdge{
		// Component 1
		{ID: "e1", FromNodeID: "A", ToNodeID: "B"},
		{ID: "e2", FromNodeID: "B", ToNodeID: "A"},
		{ID: "e3", FromNodeID: "B", ToNodeID: "C"},
		{ID: "e4", FromNodeID: "C", ToNodeID: "B"},
		// Component 2
		{ID: "e5", FromNodeID: "D", ToNodeID: "E"},
		{ID: "e6", FromNodeID: "E", ToNodeID: "D"},
	}
	
	for _, edge := range edges {
		graph.UpsertEdge(ctx, &edge)
	}
	
	t.Run("DirectlyConnected", func(t *testing.T) {
		connected, err := graph.Connected(ctx, "A", "B", 1)
		if err != nil {
			t.Errorf("Failed to check connection: %v", err)
		}
		
		if !connected {
			t.Errorf("Expected A and B to be directly connected")
		}
	})
	
	t.Run("IndirectlyConnected", func(t *testing.T) {
		connected, err := graph.Connected(ctx, "A", "C", 2)
		if err != nil {
			t.Errorf("Failed to check connection: %v", err)
		}
		
		if !connected {
			t.Errorf("Expected A and C to be connected within 2 hops")
		}
	})
	
	t.Run("NotConnected", func(t *testing.T) {
		connected, err := graph.Connected(ctx, "A", "D", 10)
		if err != nil {
			t.Errorf("Failed to check connection: %v", err)
		}
		
		if connected {
			t.Errorf("Expected A and D to not be connected")
		}
	})
	
	t.Run("SameNode", func(t *testing.T) {
		connected, err := graph.Connected(ctx, "A", "A", 0)
		if err != nil {
			t.Errorf("Failed to check connection: %v", err)
		}
		
		if !connected {
			t.Errorf("Expected node to be connected to itself")
		}
	})
	
	t.Run("MaxDepthLimit", func(t *testing.T) {
		connected, err := graph.Connected(ctx, "A", "C", 1)
		if err != nil {
			t.Errorf("Failed to check connection: %v", err)
		}
		
		if connected {
			t.Errorf("Expected A and C to not be connected within 1 hop")
		}
	})
}

func TestTraversalEdgeCases(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	t.Run("NonExistentNode", func(t *testing.T) {
		neighbors, err := graph.Neighbors(ctx, "NonExistent", TraversalOptions{
			MaxDepth: 1,
		})
		
		if err != nil {
			t.Errorf("Unexpected error for non-existent node: %v", err)
		}
		
		if len(neighbors) != 0 {
			t.Errorf("Expected 0 neighbors for non-existent node, got %d", len(neighbors))
		}
	})
	
	t.Run("ZeroMaxDepth", func(t *testing.T) {
		// Create a simple node
		node := &GraphNode{
			ID:     "TestNode",
			Vector: []float32{1, 2, 3},
		}
		graph.UpsertNode(ctx, node)
		
		neighbors, err := graph.Neighbors(ctx, "TestNode", TraversalOptions{
			MaxDepth: 0,
		})
		
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		
		// With MaxDepth 0, should be treated as 1
		if len(neighbors) != 0 {
			t.Errorf("Expected 0 neighbors with no edges, got %d", len(neighbors))
		}
	})
	
	t.Run("CyclicGraph", func(t *testing.T) {
		// Create a cycle: X -> Y -> Z -> X
		nodes := []GraphNode{
			{ID: "X", Vector: []float32{1, 0, 0}},
			{ID: "Y", Vector: []float32{0, 1, 0}},
			{ID: "Z", Vector: []float32{0, 0, 1}},
		}
		
		for _, n := range nodes {
			graph.UpsertNode(ctx, &n)
		}
		
		edges := []GraphEdge{
			{ID: "ex1", FromNodeID: "X", ToNodeID: "Y"},
			{ID: "ex2", FromNodeID: "Y", ToNodeID: "Z"},
			{ID: "ex3", FromNodeID: "Z", ToNodeID: "X"},
		}
		
		for _, e := range edges {
			graph.UpsertEdge(ctx, &e)
		}
		
		// Should handle cycle without infinite loop
		neighbors, err := graph.Neighbors(ctx, "X", TraversalOptions{
			MaxDepth:  10,
			Direction: "out",
		})
		
		if err != nil {
			t.Errorf("Failed to traverse cyclic graph: %v", err)
		}
		
		// Should only visit Y and Z once despite cycle
		if len(neighbors) != 2 {
			t.Errorf("Expected 2 unique neighbors in cycle, got %d", len(neighbors))
		}
	})
}

func TestGetEdgeByID(t *testing.T) {
	_, graph, cleanup := setupTestGraph(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create nodes and edge
	node1 := &GraphNode{ID: "node1", Vector: []float32{1, 0, 0}}
	node2 := &GraphNode{ID: "node2", Vector: []float32{0, 1, 0}}
	graph.UpsertNode(ctx, node1)
	graph.UpsertNode(ctx, node2)
	
	edge := &GraphEdge{
		ID:         "test_edge",
		FromNodeID: "node1",
		ToNodeID:   "node2",
		EdgeType:   "test",
		Weight:     0.5,
		Properties: map[string]interface{}{
			"key": "value",
		},
	}
	graph.UpsertEdge(ctx, edge)
	
	t.Run("GetExistingEdge", func(t *testing.T) {
		retrieved, err := graph.getEdgeByID(ctx, "test_edge")
		if err != nil {
			t.Errorf("Failed to get edge: %v", err)
		}
		
		if retrieved.ID != "test_edge" {
			t.Errorf("Expected edge ID 'test_edge', got '%s'", retrieved.ID)
		}
		
		if retrieved.Weight != 0.5 {
			t.Errorf("Expected weight 0.5, got %f", retrieved.Weight)
		}
	})
	
	t.Run("GetNonExistentEdge", func(t *testing.T) {
		_, err := graph.getEdgeByID(ctx, "non_existent")
		if err == nil {
			t.Errorf("Expected error for non-existent edge")
		}
	})
}

func TestContainsHelper(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		value    string
		expected bool
	}{
		{
			name:     "ContainsValue",
			slice:    []string{"a", "b", "c"},
			value:    "b",
			expected: true,
		},
		{
			name:     "DoesNotContainValue",
			slice:    []string{"a", "b", "c"},
			value:    "d",
			expected: false,
		},
		{
			name:     "EmptySlice",
			slice:    []string{},
			value:    "a",
			expected: false,
		},
		{
			name:     "NilSlice",
			slice:    nil,
			value:    "a",
			expected: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := contains(tt.slice, tt.value)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}