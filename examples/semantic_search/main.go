package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/liliang-cn/sqvect/pkg/core"
	"github.com/liliang-cn/sqvect/pkg/sqvect"
)

// Document represents a text document with metadata
type Document struct {
	Title    string
	Content  string
	Category string
	Date     string
}

// generateEmbedding simulates generating embeddings from text
// In production, you would use an actual embedding model like OpenAI, Sentence-BERT, etc.
func generateEmbedding(text string, dim int) []float32 {
	// Simple hash-based embedding for demonstration
	h := 0
	for _, r := range text {
		h = h*31 + int(r)
	}
	rand.Seed(int64(h))
	
	embedding := make([]float32, dim)
	for i := range embedding {
		embedding[i] = rand.Float32()*2 - 1
	}
	
	// Normalize the vector
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
	fmt.Println("=== Semantic Search Example ===")
	fmt.Println("This example demonstrates semantic search capabilities of sqvect")
	fmt.Println()

	// Initialize database
	dbPath := "semantic_search.db"
	defer os.Remove(dbPath)

	config := sqvect.Config{
		Path:       dbPath,
		Dimensions: 384, // Typical dimension for sentence transformers
	}

	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	ctx := context.Background()
	quick := db.Quick()

	// Sample documents
	documents := []Document{
		// Technology
		{
			Title:    "Introduction to Machine Learning",
			Content:  "Machine learning is a subset of artificial intelligence that enables systems to learn and improve from experience without being explicitly programmed. It focuses on developing computer programs that can access data and use it to learn for themselves.",
			Category: "Technology",
			Date:     "2024-01-15",
		},
		{
			Title:    "Deep Learning Fundamentals",
			Content:  "Deep learning is a machine learning technique that teaches computers to do what comes naturally to humans: learn by example. Deep learning is a key technology behind driverless cars, enabling them to recognize a stop sign or distinguish pedestrians.",
			Category: "Technology",
			Date:     "2024-01-20",
		},
		{
			Title:    "Natural Language Processing",
			Content:  "NLP is a branch of artificial intelligence that helps computers understand, interpret and manipulate human language. NLP draws from many disciplines including computer science and computational linguistics to bridge the gap between human communication and computer understanding.",
			Category: "Technology",
			Date:     "2024-02-01",
		},
		// Science
		{
			Title:    "Quantum Computing Basics",
			Content:  "Quantum computing is a type of computation that harnesses the phenomena of quantum mechanics to process information. Unlike classical computers that use bits, quantum computers use quantum bits or qubits.",
			Category: "Science",
			Date:     "2024-01-25",
		},
		{
			Title:    "Climate Change and Global Warming",
			Content:  "Climate change refers to long-term shifts in global temperatures and weather patterns. While climate variations are natural, scientific evidence shows that human activities have been the dominant driver of climate change since the mid-20th century.",
			Category: "Science",
			Date:     "2024-02-10",
		},
		// Business
		{
			Title:    "Digital Transformation Strategies",
			Content:  "Digital transformation is the integration of digital technology into all areas of business, fundamentally changing how businesses operate and deliver value to customers. It requires a cultural change that challenges the status quo.",
			Category: "Business",
			Date:     "2024-01-30",
		},
		{
			Title:    "Startup Growth Hacking",
			Content:  "Growth hacking is a marketing technique developed by technology startups that uses creativity, analytical thinking, and social metrics to sell products and gain exposure. It focuses on low-cost and innovative alternatives to traditional marketing.",
			Category: "Business",
			Date:     "2024-02-05",
		},
		// Health
		{
			Title:    "Mental Health in Digital Age",
			Content:  "The digital age has brought both opportunities and challenges for mental health. While technology provides new tools for mental health support, excessive screen time and social media use can contribute to anxiety and depression.",
			Category: "Health",
			Date:     "2024-02-15",
		},
		{
			Title:    "Nutrition and Wellness",
			Content:  "Good nutrition is essential for health and development. It helps maintain healthy body weight, reduces chronic disease risk, and promotes overall well-being. A balanced diet includes fruits, vegetables, whole grains, and lean proteins.",
			Category: "Health",
			Date:     "2024-02-20",
		},
	}

	// Index documents
	fmt.Println("1. Indexing Documents")
	fmt.Println("   Adding", len(documents), "documents to the database...")
	
	docIDs := make([]string, len(documents))
	for i, doc := range documents {
		// Generate embedding from title and content
		fullText := doc.Title + " " + doc.Content
		embedding := generateEmbedding(fullText, 384)
		
		// Store document with metadata
		metadata := fmt.Sprintf("[%s] %s | %s", doc.Category, doc.Title, doc.Date)
		id, err := quick.Add(ctx, embedding, metadata)
		if err != nil {
			log.Printf("Failed to add document: %v", err)
			continue
		}
		docIDs[i] = id
		fmt.Printf("   ✓ Indexed: %s\n", doc.Title)
	}
	fmt.Println()

	// Perform semantic searches
	fmt.Println("2. Semantic Search Queries")
	queries := []string{
		"artificial intelligence and neural networks",
		"environmental sustainability",
		"business growth strategies",
		"health and wellbeing",
		"quantum mechanics and computing",
	}

	for _, query := range queries {
		fmt.Printf("\n   Query: \"%s\"\n", query)
		fmt.Println("   " + strings.Repeat("-", 50))
		
		// Generate query embedding
		queryEmbedding := generateEmbedding(query, 384)
		
		// Search for similar documents
		results, err := quick.Search(ctx, queryEmbedding, 3)
		if err != nil {
			log.Printf("Search failed: %v", err)
			continue
		}
		
		// Display results
		for i, result := range results {
			// Extract title from metadata
			parts := strings.Split(result.Content, "|")
			if len(parts) > 0 {
				titlePart := strings.TrimSpace(parts[0])
				if idx := strings.Index(titlePart, "]"); idx >= 0 && idx+1 < len(titlePart) {
					title := strings.TrimSpace(titlePart[idx+1:])
					fmt.Printf("   %d. %s (Score: %.3f)\n", i+1, title, result.Score)
				}
			}
		}
	}

	// Advanced search with filtering
	fmt.Println("\n3. Category-Specific Search")
	fmt.Println("   Searching for 'innovation' in Technology category...")
	
	// Create a collection for each category
	techCollection := "technology"
	_, err = db.Vector().CreateCollection(ctx, techCollection, 384)
	if err != nil {
		fmt.Printf("   Note: Collection might already exist: %v\n", err)
	}
	
	// Add technology documents to the collection
	for _, doc := range documents {
		if doc.Category == "Technology" {
			fullText := doc.Title + " " + doc.Content
			embedding := generateEmbedding(fullText, 384)
			metadata := fmt.Sprintf("%s | %s", doc.Title, doc.Date)
			_, err := quick.AddToCollection(ctx, techCollection, embedding, metadata)
			if err != nil {
				log.Printf("Failed to add to collection: %v", err)
			}
		}
	}
	
	// Search within the technology collection
	innovationQuery := "innovation and new technologies"
	queryEmb := generateEmbedding(innovationQuery, 384)
	techResults, err := quick.SearchInCollection(ctx, techCollection, queryEmb, 5)
	if err != nil {
		log.Printf("Collection search failed: %v", err)
	} else {
		fmt.Println("   Results from Technology collection:")
		for i, result := range techResults {
			parts := strings.Split(result.Content, "|")
			if len(parts) > 0 {
				title := strings.TrimSpace(parts[0])
				fmt.Printf("   %d. %s (Score: %.3f)\n", i+1, title, result.Score)
			}
		}
	}

	// Similarity threshold search
	fmt.Println("\n4. Similarity Threshold Search")
	fmt.Println("   Finding highly similar documents to 'Deep Learning'...")
	
	// Find a document to use as reference
	deepLearningEmb := generateEmbedding("Deep Learning Fundamentals Deep learning is a machine learning technique", 384)
	
	// Search with high similarity threshold
	similarDocs, err := db.Vector().Search(ctx, deepLearningEmb, core.SearchOptions{
		TopK:      10,
		Threshold: 0.7, // Only return results with similarity > 0.7
	})
	
	if err != nil {
		log.Printf("Threshold search failed: %v", err)
	} else {
		fmt.Printf("   Found %d documents with similarity > 0.7:\n", len(similarDocs))
		for i, result := range similarDocs {
			if i >= 3 {
				break // Show only top 3
			}
			parts := strings.Split(result.Content, "|")
			if len(parts) > 0 {
				titlePart := strings.TrimSpace(parts[0])
				if idx := strings.Index(titlePart, "]"); idx >= 0 && idx+1 < len(titlePart) {
					title := strings.TrimSpace(titlePart[idx+1:])
					fmt.Printf("   %d. %s (Similarity: %.3f)\n", i+1, title, result.Score)
				}
			}
		}
	}

	// Performance metrics
	fmt.Println("\n5. Performance Metrics")
	
	// Measure search performance
	searchQuery := generateEmbedding("test query", 384)
	
	start := time.Now()
	for i := 0; i < 100; i++ {
		_, _ = quick.Search(ctx, searchQuery, 5)
	}
	elapsed := time.Since(start)
	
	fmt.Printf("   Average search time (100 queries): %.2f ms\n", float64(elapsed.Milliseconds())/100)
	
	// Database statistics
	collections, err := db.Vector().ListCollections(ctx)
	if err == nil {
		fmt.Printf("   Total collections: %d\n", len(collections))
	}
	
	stats, err := db.Vector().GetCollectionStats(ctx, "")
	if err == nil {
		fmt.Printf("   Total embeddings in default collection: %d\n", stats.Count)
		fmt.Printf("   Dimensions: %d\n", stats.Dimensions)
	}

	fmt.Println("\n✨ Semantic Search Example Complete!")
	fmt.Println("This example demonstrated:")
	fmt.Println("  • Document indexing with embeddings")
	fmt.Println("  • Semantic similarity search")
	fmt.Println("  • Collection-based filtering")
	fmt.Println("  • Similarity threshold filtering")
	fmt.Println("  • Performance metrics")
}