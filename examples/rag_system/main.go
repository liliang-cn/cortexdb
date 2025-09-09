package main

import (
	"context"
	"fmt"
	"log"
	"math"

	"github.com/liliang-cn/sqvect/pkg/sqvect"
	"github.com/liliang-cn/sqvect/pkg/graph"
	"github.com/liliang-cn/sqvect/pkg/core"
)

func main() {
	// Example: Advanced RAG system with graph relationships
	
	config := sqvect.DefaultConfig("rag.db")
	config.Dimensions = 1536 // GPT-3 embedding dimensions
	
	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer func() { _ = db.Close() }()
	
	ctx := context.Background()
	
	// Create knowledge graph for documents and their relationships
	fmt.Println("Building knowledge graph for RAG system...")
	
	// Add document chunks with embeddings
	documents := []struct {
		id      string
		content string
		docType string
		vector  []float32
	}{
		{"doc1", "Introduction to machine learning algorithms", "intro", createDocumentEmbedding(1536, 1)},
		{"doc2", "Supervised learning: classification and regression", "concept", createDocumentEmbedding(1536, 2)},
		{"doc3", "Unsupervised learning: clustering and dimensionality reduction", "concept", createDocumentEmbedding(1536, 3)},
		{"doc4", "Deep learning and neural networks", "advanced", createDocumentEmbedding(1536, 4)},
		{"doc5", "Applications of ML in industry", "application", createDocumentEmbedding(1536, 5)},
	}
	
	// Store as graph nodes with vector embeddings
	graphStore := db.Graph()
	
	// Initialize graph schema
	if err := graphStore.InitGraphSchema(ctx); err != nil {
		log.Fatal("Failed to init graph schema:", err)
	}
	
	for _, doc := range documents {
		node := &graph.GraphNode{
			ID:      doc.id,
			Vector:  doc.vector,
			Content: doc.content,
			NodeType: doc.docType,
			Properties: map[string]interface{}{
				"document_type": doc.docType,
				"word_count":   len(doc.content),
			},
		}
		
		err := graphStore.UpsertNode(ctx, node)
		if err != nil {
			log.Printf("Failed to add node %s: %v", doc.id, err)
			continue
		}
		
		preview := doc.content
		if len(preview) > 50 {
			preview = preview[:50] + "..."
		}
		fmt.Printf("Added document: %s\n", preview)
	}
	
	// Create relationships between documents
	relationships := []struct {
		from     string
		to       string
		relType  string
		weight   float64
	}{
		{"doc1", "doc2", "leads_to", 0.8},
		{"doc1", "doc3", "leads_to", 0.7},
		{"doc2", "doc4", "builds_on", 0.9},
		{"doc3", "doc4", "builds_on", 0.8},
		{"doc4", "doc5", "applied_in", 0.7},
		{"doc2", "doc5", "applied_in", 0.6},
	}
	
	for _, rel := range relationships {
		edge := &graph.GraphEdge{
			ID:         fmt.Sprintf("%s_%s", rel.from, rel.to),
			FromNodeID: rel.from,
			ToNodeID:   rel.to,
			EdgeType:   rel.relType,
			Weight:     rel.weight,
		}
		
		err := graphStore.UpsertEdge(ctx, edge)
		if err != nil {
			log.Printf("Failed to add edge %s->%s: %v", rel.from, rel.to, err)
		}
	}
	
	fmt.Println("\nKnowledge graph built successfully!")
	
	// Demonstrate RAG queries
	fmt.Println("\n--- RAG Query Examples ---")
	
	// 1. Pure vector similarity search
	queryVector := createDocumentEmbedding(1536, 2) // Similar to supervised learning
	
	vectorResults, err := db.Vector().Search(ctx, queryVector, core.SearchOptions{
		TopK: 3,
		Threshold: 0.0,
	})
	if err == nil {
		fmt.Printf("\n1. Vector similarity search results:\n")
		for i, result := range vectorResults {
			fmt.Printf("   %d. %s (score: %.4f)\n", i+1, result.Content[:50]+"...", result.Score)
		}
	}
	
	// 2. Hybrid vector + graph search (best for RAG)
	hybridQuery := &graph.HybridQuery{
		Vector:          queryVector,
		StartNodeID:     "doc1", // Start from introduction
		TopK:           5,
		VectorThreshold: 0.3,
		TotalThreshold:  0.2,
		VectorWeight:    0.7,
		GraphWeight:     0.3,
	}
	
	hybridResults, err := graphStore.HybridSearch(ctx, hybridQuery)
	if err == nil {
		fmt.Printf("\n2. Hybrid search results (vector + graph):\n")
		for i, result := range hybridResults {
			fmt.Printf("   %d. %s\n      Vector: %.3f, Graph: %.3f, Total: %.3f\n",
				i+1, result.Node.Content[:50]+"...", 
				result.VectorScore, result.GraphScore, result.TotalScore)
		}
	}
	
	// 3. Graph traversal for finding related concepts
	fmt.Printf("\n3. Finding documents related to 'doc2' (supervised learning):\n")
	neighbors, err := graphStore.Neighbors(ctx, "doc2", graph.TraversalOptions{
		MaxDepth:  2,
		EdgeTypes: []string{"builds_on", "applied_in"},
	})
	if err == nil {
		for i, neighbor := range neighbors {
			fmt.Printf("   %d. %s\n", i+1, neighbor.Content[:50]+"...")
		}
	}
	
	// 4. Path finding for explanation chains
	fmt.Printf("\n4. Learning path from introduction to applications:\n")
	path, err := graphStore.ShortestPath(ctx, "doc1", "doc5")
	if err == nil && path != nil {
		fmt.Printf("   Path found with %d steps:\n", len(path.Nodes)-1)
		for i, node := range path.Nodes {
			if i > 0 {
				fmt.Printf("   -> ")
			}
			preview := node.Content
			if len(preview) > 30 {
				preview = preview[:30] + "..."
			}
			fmt.Printf("%s", preview)
		}
		fmt.Println()
	}
	
	// Performance stats
	stats, _ := db.Vector().Stats(ctx)
	fmt.Printf("\nRAG System Stats:\n")
	fmt.Printf("  Total embeddings: %d\n", stats.Count)
	fmt.Printf("  Vector dimensions: %d\n", stats.Dimensions)
	fmt.Printf("  Database size: %.2f KB\n", float64(stats.Size)/1024)
}

// createDocumentEmbedding simulates document embeddings for demonstration
// In real RAG systems, you'd use OpenAI, HuggingFace, or other embedding models
func createDocumentEmbedding(dim int, seed int) []float32 {
	vector := make([]float32, dim)
	for i := 0; i < dim; i++ {
		vector[i] = float32((seed*i*17)%200-100) / 100.0
	}
	// Simple normalization
	var norm float32
	for _, v := range vector {
		norm += v * v
	}
	if norm > 0 {
		norm = float32(1.0 / math.Sqrt(float64(norm)))
		for i := range vector {
			vector[i] *= norm
		}
	}
	return vector
}