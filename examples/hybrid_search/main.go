package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/liliang-cn/sqvect/v2/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/v2/pkg/graph"
	"github.com/liliang-cn/sqvect/v2/v2/pkg/sqvect"
)

// Article represents a research article
type Article struct {
	ID       string
	Title    string
	Abstract string
	Authors  []string
	Year     int
	Field    string
}

// generateEmbedding creates a vector embedding for text
func generateEmbedding(text string, dim int) []float32 {
	h := 0
	for _, r := range text {
		h = h*31 + int(r)
	}
	rng := rand.New(rand.NewSource(int64(h)))
	
	embedding := make([]float32, dim)
	for i := range embedding {
		embedding[i] = rng.Float32()*2 - 1
	}
	
	// Normalize
	var sum float32
	for _, v := range embedding {
		sum += v * v
	}
	norm := float32(math.Sqrt(float64(sum)))
	if norm > 0 {
		for i := range embedding {
			embedding[i] /= norm
		}
	}
	
	return embedding
}

func main() {
	fmt.Println("=== Hybrid Search Example ===")
	fmt.Println("Combining vector similarity with graph relationships for enhanced search")
	fmt.Println()

	// Initialize database
	dbPath := "hybrid_search.db"
	defer func() { _ = os.Remove(dbPath) }()

	config := sqvect.Config{
		Path:       dbPath,
		Dimensions: 256,
	}

	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	graphStore := db.Graph()

	// Initialize graph schema
	if err := graphStore.InitGraphSchema(ctx); err != nil {
		log.Fatal("Failed to init graph schema:", err)
	}

	// Sample research articles
	articles := []Article{
		// Machine Learning
		{
			ID:       "ml1",
			Title:    "Attention Is All You Need",
			Abstract: "We propose a new simple network architecture, the Transformer, based solely on attention mechanisms",
			Authors:  []string{"Vaswani", "Shazeer", "Parmar"},
			Year:     2017,
			Field:    "ML",
		},
		{
			ID:       "ml2",
			Title:    "BERT: Pre-training of Deep Bidirectional Transformers",
			Abstract: "We introduce a new language representation model called BERT, which stands for Bidirectional Encoder Representations from Transformers",
			Authors:  []string{"Devlin", "Chang", "Lee"},
			Year:     2018,
			Field:    "ML",
		},
		{
			ID:       "ml3",
			Title:    "GPT-3: Language Models are Few-Shot Learners",
			Abstract: "We demonstrate that scaling up language models greatly improves task-agnostic, few-shot performance",
			Authors:  []string{"Brown", "Mann", "Ryder"},
			Year:     2020,
			Field:    "ML",
		},
		// Computer Vision
		{
			ID:       "cv1",
			Title:    "ImageNet Classification with Deep CNNs",
			Abstract: "We trained a large, deep convolutional neural network to classify images in the ImageNet dataset",
			Authors:  []string{"Krizhevsky", "Sutskever", "Hinton"},
			Year:     2012,
			Field:    "CV",
		},
		{
			ID:       "cv2",
			Title:    "ResNet: Deep Residual Learning",
			Abstract: "We present a residual learning framework to ease the training of networks that are substantially deeper",
			Authors:  []string{"He", "Zhang", "Ren", "Sun"},
			Year:     2015,
			Field:    "CV",
		},
		{
			ID:       "cv3",
			Title:    "Vision Transformer (ViT)",
			Abstract: "While the Transformer architecture has become the de-facto standard for NLP, its applications to computer vision remain limited",
			Authors:  []string{"Dosovitskiy", "Beyer", "Kolesnikov"},
			Year:     2020,
			Field:    "CV",
		},
		// Reinforcement Learning
		{
			ID:       "rl1",
			Title:    "Playing Atari with Deep RL",
			Abstract: "We present the first deep learning model to successfully learn control policies directly from high-dimensional sensory input",
			Authors:  []string{"Mnih", "Kavukcuoglu", "Silver"},
			Year:     2013,
			Field:    "RL",
		},
		{
			ID:       "rl2",
			Title:    "AlphaGo: Mastering Go with Deep Neural Networks",
			Abstract: "The game of Go has long been viewed as the most challenging of classic games for artificial intelligence",
			Authors:  []string{"Silver", "Huang", "Maddison"},
			Year:     2016,
			Field:    "RL",
		},
	}

	// Step 1: Create article nodes in the graph
	fmt.Println("1. Building Knowledge Graph")
	fmt.Println("   Adding articles as nodes...")
	
	for _, article := range articles {
		// Generate embedding from title and abstract
		content := article.Title + " " + article.Abstract
		embedding := generateEmbedding(content, 256)
		
		// Create graph node
		node := &graph.GraphNode{
			ID:       article.ID,
			Vector:   embedding,
			Content:  article.Title,
			NodeType: article.Field,
			Properties: map[string]interface{}{
				"year":     article.Year,
				"authors":  article.Authors,
				"abstract": article.Abstract,
			},
		}
		
		if err := graphStore.UpsertNode(ctx, node); err != nil {
			log.Printf("Failed to create node: %v", err)
		} else {
			fmt.Printf("   ✓ Added: %s (%d)\n", article.Title, article.Year)
		}
	}

	// Step 2: Create relationships between articles
	fmt.Println("\n2. Creating Relationships")
	
	relationships := []struct {
		from     string
		to       string
		relation string
		weight   float64
	}{
		// Citation relationships
		{"ml2", "ml1", "cites", 0.9},      // BERT cites Transformer
		{"ml3", "ml1", "cites", 0.9},      // GPT-3 cites Transformer
		{"ml3", "ml2", "builds_on", 0.8},  // GPT-3 builds on BERT
		{"cv3", "ml1", "applies", 0.7},    // ViT applies Transformer to vision
		{"cv2", "cv1", "improves", 0.8},   // ResNet improves on AlexNet
		{"rl2", "rl1", "extends", 0.7},    // AlphaGo extends DQN
		
		// Author collaborations
		{"ml1", "ml2", "shared_authors", 0.5},
		{"cv1", "cv2", "same_field", 0.6},
		{"rl1", "rl2", "same_field", 0.6},
		
		// Cross-field connections
		{"cv3", "cv1", "bridges", 0.6}, // ViT bridges NLP and CV
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
		} else {
			fmt.Printf("   ✓ Created: %s -[%s]-> %s\n", rel.from, rel.relation, rel.to)
		}
	}

	// Step 3: Demonstrate different search strategies
	fmt.Println("\n3. Search Strategies Comparison")
	
	// Test query
	queryText := "transformer architecture for visual recognition"
	queryVector := generateEmbedding(queryText, 256)
	fmt.Printf("   Query: \"%s\"\n", queryText)
	fmt.Println("   " + string(make([]byte, 60)))

	// A. Pure vector search
	fmt.Println("\n   A. Vector-Only Search (Traditional)")
	vectorResults, err := db.Vector().Search(ctx, queryVector, core.SearchOptions{
		TopK: 5,
	})
	if err != nil {
		log.Printf("Vector search failed: %v", err)
	} else {
		for i, result := range vectorResults {
			if i >= 3 {
				break
			}
			fmt.Printf("      %d. %s (Score: %.3f)\n", i+1, result.Content, result.Score)
		}
	}

	// B. Graph-enhanced search (starting from a relevant node)
	fmt.Println("\n   B. Graph-Enhanced Search (From Transformer Paper)")
	graphResults, err := graphStore.GraphVectorSearch(
		ctx,
		"ml1", // Start from Transformer paper
		queryVector,
		graph.TraversalOptions{
			MaxDepth:  2,
			Direction: "both",
		},
	)
	if err != nil {
		log.Printf("Graph search failed: %v", err)
	} else {
		for i, result := range graphResults {
			if i >= 3 {
				break
			}
			fmt.Printf("      %d. %s (Vector: %.3f, Graph: %.3f)\n",
				i+1, result.Node.Content, result.VectorScore, result.GraphScore)
		}
	}

	// C. Hybrid search combining both approaches
	fmt.Println("\n   C. Hybrid Search (Vector + Graph)")
	hybridResults, err := graphStore.HybridSearch(ctx, &graph.HybridQuery{
		Vector:      queryVector,
		StartNodeID: "ml1",
		TopK:        5,
		Weights: graph.HybridWeights{
			VectorWeight: 0.5,
			GraphWeight:  0.3,
			EdgeWeight:   0.2,
		},
		GraphFilter: &graph.GraphFilter{
			MaxDepth:  2,
			EdgeTypes: []string{"cites", "applies", "builds_on"},
		},
	})
	if err != nil {
		log.Printf("Hybrid search failed: %v", err)
	} else {
		for i, result := range hybridResults {
			if i >= 3 {
				break
			}
			fmt.Printf("      %d. %s (Combined: %.3f, V:%.3f, G:%.3f)\n",
				i+1, result.Node.Content, result.CombinedScore,
				result.VectorScore, result.GraphScore)
		}
	}

	// Step 4: Time-aware search
	fmt.Println("\n4. Time-Aware Search")
	fmt.Println("   Finding recent papers related to 'transformers'...")
	
	// Search and filter by year
	transformerQuery := generateEmbedding("transformer models", 256)
	allResults, _ := db.Vector().Search(ctx, transformerQuery, core.SearchOptions{
		TopK: 10,
	})
	
	fmt.Println("   Recent papers (2018+):")
	for _, result := range allResults {
		// Get the full node to access properties
		node, err := graphStore.GetNode(ctx, result.ID)
		if err != nil {
			continue
		}
		
		if yearProp, ok := node.Properties["year"]; ok {
			if year, ok := yearProp.(float64); ok && year >= 2018 {
				fmt.Printf("      • %s (%d) - Score: %.3f\n",
					node.Content, int(year), result.Score)
			}
		}
	}

	// Step 5: Citation network analysis
	fmt.Println("\n5. Citation Network Analysis")
	fmt.Println("   Finding most influential papers...")
	
	// Use PageRank to find influential papers
	pageRankResults, err := graphStore.PageRank(ctx, 50, 0.85)
	if err != nil {
		log.Printf("PageRank failed: %v", err)
	} else {
		fmt.Println("   Papers by influence (PageRank):")
		for i, result := range pageRankResults {
			if i >= 5 {
				break
			}
			node, _ := graphStore.GetNode(ctx, result.NodeID)
			if node != nil {
				fmt.Printf("      %d. %s (Score: %.4f)\n",
					i+1, node.Content, result.Score)
			}
		}
	}

	// Step 6: Research path finding
	fmt.Println("\n6. Research Evolution Path")
	fmt.Println("   Finding path from CNNs to Vision Transformers...")
	
	path, err := graphStore.ShortestPath(ctx, "cv1", "cv3")
	if err != nil {
		fmt.Printf("   No path found: %v\n", err)
	} else {
		fmt.Printf("   Path (distance=%d):\n", path.Distance)
		for i, node := range path.Nodes {
			yearProp := node.Properties["year"]
			year := 0
			if y, ok := yearProp.(float64); ok {
				year = int(y)
			}
			fmt.Printf("      %d. %s (%d)\n", i+1, node.Content, year)
			
			if i < len(path.Edges) {
				fmt.Printf("         ↓ [%s]\n", path.Edges[i].EdgeType)
			}
		}
	}

	// Step 7: Collaborative filtering
	fmt.Println("\n7. Paper Recommendations")
	fmt.Println("   If you liked 'BERT', you might also like...")
	
	// Find papers similar to BERT through various relationships
	bertNeighbors, err := graphStore.Neighbors(ctx, "ml2", graph.TraversalOptions{
		MaxDepth:  2,
		Direction: "both",
		Limit:     5,
	})
	if err != nil {
		log.Printf("Failed to find neighbors: %v", err)
	} else {
		seen := make(map[string]bool)
		for _, neighbor := range bertNeighbors {
			if neighbor.ID != "ml2" && !seen[neighbor.ID] {
				seen[neighbor.ID] = true
				
				// Get similarity to BERT
				bertNode, _ := graphStore.GetNode(ctx, "ml2")
				if bertNode != nil {
					similarity := cosineSimilarity(bertNode.Vector, neighbor.Vector)
					fmt.Printf("      • %s (Similarity: %.3f)\n",
						neighbor.Content, similarity)
				}
			}
		}
	}

	// Step 8: Field bridging papers
	fmt.Println("\n8. Cross-Field Connections")
	fmt.Println("   Papers that bridge multiple fields...")
	
	// Find papers connected to multiple fields
	fieldConnections := make(map[string]map[string]bool)
	
	for _, article := range articles {
		edges, _ := graphStore.GetEdges(ctx, article.ID, "both")
		fields := make(map[string]bool)
		fields[article.Field] = true
		
		for _, edge := range edges {
			var otherID string
			if edge.FromNodeID == article.ID {
				otherID = edge.ToNodeID
			} else {
				otherID = edge.FromNodeID
			}
			
			otherNode, _ := graphStore.GetNode(ctx, otherID)
			if otherNode != nil {
				fields[otherNode.NodeType] = true
			}
		}
		
		if len(fields) > 1 {
			fieldConnections[article.Title] = fields
		}
	}
	
	for title, fields := range fieldConnections {
		fmt.Printf("      • %s connects: ", title)
		first := true
		for field := range fields {
			if !first {
				fmt.Print(", ")
			}
			fmt.Print(field)
			first = false
		}
		fmt.Println()
	}

	// Step 9: Performance comparison
	fmt.Println("\n9. Performance Metrics")
	
	testQuery := generateEmbedding("test query", 256)
	
	// Vector search performance
	start := time.Now()
	for i := 0; i < 100; i++ {
		_, _ = db.Vector().Search(ctx, testQuery, core.SearchOptions{TopK: 5})
	}
	vectorTime := time.Since(start)
	
	// Hybrid search performance
	start = time.Now()
	for i := 0; i < 100; i++ {
		_, _ = graphStore.HybridSearch(ctx, &graph.HybridQuery{
			Vector: testQuery,
			TopK:   5,
			Weights: graph.HybridWeights{
				VectorWeight: 0.5,
				GraphWeight:  0.3,
				EdgeWeight:   0.2,
			},
		})
	}
	hybridTime := time.Since(start)
	
	fmt.Printf("   Vector search (100 queries): %.2f ms avg\n",
		float64(vectorTime.Milliseconds())/100)
	fmt.Printf("   Hybrid search (100 queries): %.2f ms avg\n",
		float64(hybridTime.Milliseconds())/100)
	
	// Graph statistics
	stats, _ := graphStore.GetGraphStatistics(ctx)
	if stats != nil {
		fmt.Printf("   Graph nodes: %d\n", stats.NodeCount)
		fmt.Printf("   Graph edges: %d\n", stats.EdgeCount)
		fmt.Printf("   Average degree: %.2f\n", stats.AverageDegree)
	}

	fmt.Println("\n✨ Hybrid Search Example Complete!")
	fmt.Println("This example demonstrated:")
	fmt.Println("  • Building a knowledge graph with embeddings")
	fmt.Println("  • Vector vs Graph vs Hybrid search comparison")
	fmt.Println("  • Citation network analysis with PageRank")
	fmt.Println("  • Research path finding")
	fmt.Println("  • Cross-field paper discovery")
	fmt.Println("  • Performance metrics and optimization")
}

// cosineSimilarity calculates cosine similarity
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	
	var dotProduct, normA, normB float32
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	
	if normA == 0 || normB == 0 {
		return 0
	}
	
	return dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}