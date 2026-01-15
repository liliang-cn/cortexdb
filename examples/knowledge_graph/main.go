package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"

	"github.com/liliang-cn/sqvect/v2/v2/pkg/sqvect"
	"github.com/liliang-cn/sqvect/v2/v2/pkg/graph"
)

// generateEmbedding creates a simple embedding for demonstration
func generateEmbedding(concept string) []float32 {
	rng := rand.New(rand.NewSource(int64(len(concept))))
	embedding := make([]float32, 128)
	for i := range embedding {
		embedding[i] = rng.Float32()*2 - 1
	}
	return embedding
}

func main() {
	// Create a knowledge graph database
	dbPath := "knowledge_graphStore.db"
	defer func() { _ = os.Remove(dbPath) }()

	fmt.Println("=== Knowledge Graph with Vector Embeddings ===")
	fmt.Println()

	// Initialize database
	config := sqvect.Config{
		Path:       dbPath,
		Dimensions: 128, // 128-dimensional embeddings
	}
	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	graphStore := db.Graph()

	// Initialize graph schema
	if err := graphStore.InitGraphSchema(ctx); err != nil {
		log.Fatal("Failed to init graph schema:", err)
	}

	// Create knowledge nodes
	fmt.Println("1. Creating Knowledge Nodes...")
	concepts := []struct {
		id       string
		content  string
		category string
	}{
		{"ml", "Machine Learning", "field"},
		{"dl", "Deep Learning", "field"},
		{"nn", "Neural Networks", "concept"},
		{"cnn", "Convolutional Neural Networks", "concept"},
		{"rnn", "Recurrent Neural Networks", "concept"},
		{"transformer", "Transformer Architecture", "concept"},
		{"bert", "BERT Model", "model"},
		{"gpt", "GPT Model", "model"},
		{"nlp", "Natural Language Processing", "field"},
		{"cv", "Computer Vision", "field"},
	}

	for _, concept := range concepts {
		node := &graph.GraphNode{
			ID:       concept.id,
			Vector:   generateEmbedding(concept.content),
			Content:  concept.content,
			NodeType: concept.category,
			Properties: map[string]interface{}{
				"description": fmt.Sprintf("Knowledge about %s", concept.content),
			},
		}
		if err := graphStore.UpsertNode(ctx, node); err != nil {
			log.Printf("Failed to create node %s: %v", concept.id, err)
		}
	}
	fmt.Println("   ✓ Created", len(concepts), "knowledge nodes")
	fmt.Println()

	// Create relationships
	fmt.Println("2. Creating Relationships...")
	relationships := []struct {
		from     string
		to       string
		relation string
		weight   float64
	}{
		// Hierarchical relationships
		{"ml", "dl", "includes", 0.9},
		{"dl", "nn", "based_on", 0.95},
		{"nn", "cnn", "specialization", 0.85},
		{"nn", "rnn", "specialization", 0.85},
		{"nn", "transformer", "specialization", 0.9},
		
		// Model relationships
		{"transformer", "bert", "architecture_of", 0.95},
		{"transformer", "gpt", "architecture_of", 0.95},
		{"bert", "nlp", "used_in", 0.9},
		{"gpt", "nlp", "used_in", 0.95},
		
		// Field relationships
		{"cnn", "cv", "used_in", 0.95},
		{"rnn", "nlp", "used_in", 0.8},
		{"ml", "nlp", "includes", 0.85},
		{"ml", "cv", "includes", 0.85},
	}

	for i, rel := range relationships {
		edge := &graph.GraphEdge{
			ID:         fmt.Sprintf("edge_%d", i),
			FromNodeID: rel.from,
			ToNodeID:   rel.to,
			EdgeType:   rel.relation,
			Weight:     rel.weight,
		}
		if err := graphStore.UpsertEdge(ctx, edge); err != nil {
			log.Printf("Failed to create edge: %v", err)
		}
	}
	fmt.Println("   ✓ Created", len(relationships), "relationships")
	fmt.Println()

	// Demonstrate graph queries
	fmt.Println("3. Graph Traversal Examples")
	fmt.Println("   Finding concepts related to 'Deep Learning':")
	
	neighbors, err := graphStore.Neighbors(ctx, "dl", graph.TraversalOptions{
		MaxDepth:  2,
		Direction: "both",
	})
	if err != nil {
		log.Printf("Failed to find neighbors: %v", err)
	} else {
		for _, node := range neighbors {
			fmt.Printf("   - %s (%s)\n", node.Content, node.NodeType)
		}
	}

	// Shortest path
	fmt.Println("\n4. Finding Path from CNN to GPT:")
	path, err := graphStore.ShortestPath(ctx, "cnn", "gpt")
	if err != nil {
		fmt.Printf("   No path found: %v\n", err)
	} else {
		fmt.Printf("   Path (distance=%d): ", path.Distance)
		for i, node := range path.Nodes {
			if i > 0 {
				fmt.Print(" → ")
			}
			fmt.Print(node.Content)
		}
		fmt.Println()
	}

	// Hybrid search
	fmt.Println("\n5. Hybrid Search (Vector + Graph)")
	fmt.Println("   Query: Find concepts similar to 'Transformer' and their connections")
	
	transformerNode, _ := graphStore.GetNode(ctx, "transformer")
	hybridResults, err := graphStore.HybridSearch(ctx, &graph.HybridQuery{
		Vector:      transformerNode.Vector,
		StartNodeID: "transformer",
		TopK:        5,
		Weights: graph.HybridWeights{
			VectorWeight: 0.4,
			GraphWeight:  0.4,
			EdgeWeight:   0.2,
		},
		GraphFilter: &graph.GraphFilter{
			MaxDepth: 2,
		},
	})

	if err != nil {
		log.Printf("Hybrid search failed: %v", err)
	} else {
		for i, result := range hybridResults {
			fmt.Printf("   %d. %s (Score: %.3f, Vector: %.3f, Graph: %.3f)\n",
				i+1, result.Node.Content, result.CombinedScore,
				result.VectorScore, result.GraphScore)
		}
	}

	// PageRank
	fmt.Println("\n6. PageRank Analysis")
	fmt.Println("   Computing importance of concepts...")
	
	pageRankResults, err := graphStore.PageRank(ctx, 100, 0.85)
	if err != nil {
		log.Printf("PageRank failed: %v", err)
	} else {
		fmt.Println("   Top 5 most important concepts:")
		for i := 0; i < 5 && i < len(pageRankResults); i++ {
			node, _ := graphStore.GetNode(ctx, pageRankResults[i].NodeID)
			fmt.Printf("   %d. %s (Score: %.4f)\n",
				i+1, node.Content, pageRankResults[i].Score)
		}
	}

	// Community detection
	fmt.Println("\n7. Community Detection")
	communities, err := graphStore.CommunityDetection(ctx)
	if err != nil {
		log.Printf("Community detection failed: %v", err)
	} else {
		fmt.Printf("   Found %d communities:\n", len(communities))
		for i, community := range communities {
			fmt.Printf("   Community %d: ", i+1)
			for j, nodeID := range community.Nodes {
				if j > 0 {
					fmt.Print(", ")
				}
				node, _ := graphStore.GetNode(ctx, nodeID)
				fmt.Print(node.Content)
			}
			fmt.Println()
		}
	}

	// Edge prediction
	fmt.Println("\n8. Edge Prediction")
	fmt.Println("   Suggesting new connections for 'BERT':")
	
	predictions, err := graphStore.PredictEdges(ctx, "bert", 3)
	if err != nil {
		log.Printf("Edge prediction failed: %v", err)
	} else {
		for _, pred := range predictions {
			node, _ := graphStore.GetNode(ctx, pred.ToNodeID)
			fmt.Printf("   - %s (Score: %.3f, Method: %s)\n",
				node.Content, pred.Score, pred.Method)
		}
	}

	// Graph statistics
	fmt.Println("\n9. Graph Statistics")
	stats, err := graphStore.GetGraphStatistics(ctx)
	if err != nil {
		log.Printf("Failed to get statistics: %v", err)
	} else {
		fmt.Printf("   Nodes: %d\n", stats.NodeCount)
		fmt.Printf("   Edges: %d\n", stats.EdgeCount)
		fmt.Printf("   Average Degree: %.2f\n", stats.AverageDegree)
		fmt.Printf("   Density: %.4f\n", stats.Density)
		fmt.Printf("   Connected Components: %d\n", stats.ConnectedComponents)
	}

	fmt.Println("\n=== Knowledge Graph Demo Complete ===")
}