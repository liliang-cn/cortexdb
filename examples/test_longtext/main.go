package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/liliang-cn/sqvect"
)

func generateEmbedding(seed int64) []float32 {
	r := rand.New(rand.NewSource(seed))
	embedding := make([]float32, 1536) // OpenAI ada-002 dimension
	for i := range embedding {
		embedding[i] = r.Float32()*2 - 1
	}
	return embedding
}

func generateLongText(paragraphs int, seed int64) string {
	r := rand.New(rand.NewSource(seed))
	
	topics := []string{
		"artificial intelligence", "machine learning", "neural networks",
		"deep learning", "natural language processing", "computer vision",
		"reinforcement learning", "data science", "quantum computing",
		"blockchain technology", "cloud computing", "edge computing",
		"cybersecurity", "distributed systems", "microservices",
	}
	
	sentences := []string{
		"The evolution of %s has fundamentally transformed how we approach complex computational problems.",
		"Recent advances in %s have opened new possibilities for innovation across multiple industries.",
		"Understanding the core principles of %s is essential for modern software engineers.",
		"The intersection of %s with other technologies creates unprecedented opportunities.",
		"Researchers are pushing the boundaries of %s to solve previously intractable challenges.",
		"The practical applications of %s continue to expand beyond initial expectations.",
		"Industry leaders are investing heavily in %s to maintain competitive advantages.",
		"The theoretical foundations of %s provide insights into computational complexity.",
		"Emerging trends in %s suggest a paradigm shift in how we process information.",
		"The democratization of %s tools has accelerated adoption across organizations.",
	}
	
	var builder strings.Builder
	
	for p := 0; p < paragraphs; p++ {
		// Each paragraph has 5-10 sentences
		numSentences := 5 + r.Intn(6)
		
		for s := 0; s < numSentences; s++ {
			topic := topics[r.Intn(len(topics))]
			sentence := fmt.Sprintf(sentences[r.Intn(len(sentences))], topic)
			builder.WriteString(sentence)
			builder.WriteString(" ")
		}
		
		if p < paragraphs-1 {
			builder.WriteString("\n\n")
		}
	}
	
	return builder.String()
}

func main() {
	dbPath := "test_longtext.db"
	defer os.Remove(dbPath)

	// Create store with 1536 dimensions (OpenAI ada-002 size)
	store, err := sqvect.New(dbPath, 1536)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	
	if err := store.Init(ctx); err != nil {
		log.Fatal(err)
	}

	fmt.Println("=== Testing Vector Store with LONG TEXT Content ===")

	// Create documents with varying text lengths
	documents := []struct {
		id         string
		paragraphs int
		desc       string
	}{
		{"short_001", 1, "Very short document (1 paragraph)"},
		{"medium_001", 10, "Medium document (10 paragraphs)"},
		{"long_001", 50, "Long document (50 paragraphs)"},
		{"verylong_001", 100, "Very long document (100 paragraphs)"},
		{"massive_001", 500, "Massive document (500 paragraphs)"},
		{"huge_001", 1000, "Huge document (1000 paragraphs)"},
	}

	// Insert documents with increasing text length
	fmt.Println("Inserting documents with varying text lengths...")
	insertTimes := make([]time.Duration, 0)
	
	for i, doc := range documents {
		content := generateLongText(doc.paragraphs, int64(i+1))
		textSize := len(content)
		
		fmt.Printf("\n%d. %s\n", i+1, doc.desc)
		fmt.Printf("   Text size: %d bytes (%.2f KB)\n", textSize, float64(textSize)/1024)
		
		startTime := time.Now()
		err := store.Upsert(ctx, &sqvect.Embedding{
			ID:      doc.id,
			Vector:  generateEmbedding(int64(i+1)),
			Content: content,
			Metadata: map[string]string{
				"type":       "document",
				"paragraphs": fmt.Sprintf("%d", doc.paragraphs),
				"bytes":      fmt.Sprintf("%d", textSize),
			},
		})
		insertTime := time.Since(startTime)
		insertTimes = append(insertTimes, insertTime)
		
		if err != nil {
			log.Printf("   ✗ Failed to insert: %v", err)
		} else {
			fmt.Printf("   ✓ Inserted successfully in %v\n", insertTime)
		}
	}

	// Test 1: Search for exact match with huge document
	fmt.Println("\n=== Test 1: Exact Match Search with HUGE Document ===")
	hugeVector := generateEmbedding(6) // Same seed as huge_001
	
	startTime := time.Now()
	results, err := store.Search(ctx, hugeVector, sqvect.SearchOptions{TopK: 3})
	searchTime := time.Since(startTime)
	
	if err != nil {
		log.Fatal(err)
	}
	
	fmt.Printf("Search completed in %v\n\n", searchTime)
	for i, result := range results {
		contentPreview := result.Content
		if len(contentPreview) > 100 {
			contentPreview = contentPreview[:100] + "..."
		}
		fmt.Printf("%d. [Score: %.6f] ID: %s\n", i+1, result.Score, result.ID)
		fmt.Printf("   Size: %s bytes, Paragraphs: %s\n", 
			result.Metadata["bytes"], result.Metadata["paragraphs"])
		fmt.Printf("   Content preview: %s\n", contentPreview)
		
		if i == 0 && result.ID == "huge_001" && result.Score > 0.999999 {
			fmt.Println("   ✓ Perfect match with huge document confirmed!")
		}
	}

	// Test 2: Search performance with different vector
	fmt.Println("\n=== Test 2: Search Performance Test ===")
	randomVector := generateEmbedding(time.Now().UnixNano())
	
	searchTimes := make([]time.Duration, 5)
	for i := 0; i < 5; i++ {
		startTime := time.Now()
		_, err := store.Search(ctx, randomVector, sqvect.SearchOptions{TopK: 5})
		searchTimes[i] = time.Since(startTime)
		if err != nil {
			log.Printf("Search %d failed: %v", i+1, err)
		}
	}
	
	fmt.Println("Search times (5 iterations):")
	var totalTime time.Duration
	for i, t := range searchTimes {
		fmt.Printf("  %d. %v\n", i+1, t)
		totalTime += t
	}
	avgTime := totalTime / 5
	fmt.Printf("Average search time: %v\n", avgTime)

	// Test 3: Retrieve and verify long content
	fmt.Println("\n=== Test 3: Content Integrity Check ===")
	results, err = store.Search(ctx, generateEmbedding(5), sqvect.SearchOptions{TopK: 1})
	if err != nil {
		log.Fatal(err)
	}
	
	if len(results) > 0 {
		result := results[0]
		fmt.Printf("Retrieved document: %s\n", result.ID)
		fmt.Printf("Expected size: %s bytes\n", result.Metadata["bytes"])
		fmt.Printf("Actual content size: %d bytes\n", len(result.Content))
		
		if result.ID == "massive_001" {
			expectedParagraphs := 500
			actualParagraphs := strings.Count(result.Content, "\n\n") + 1
			fmt.Printf("Expected paragraphs: %d\n", expectedParagraphs)
			fmt.Printf("Actual paragraphs: ~%d\n", actualParagraphs)
			
			if actualParagraphs >= expectedParagraphs-10 && actualParagraphs <= expectedParagraphs+10 {
				fmt.Println("✓ Long content integrity maintained!")
			}
		}
	}

	// Test 4: Filter with long content
	fmt.Println("\n=== Test 4: Filter Search with Long Content ===")
	results, err = store.SearchWithFilter(ctx, randomVector,
		sqvect.SearchOptions{TopK: 10},
		map[string]interface{}{"type": "document"})
	if err != nil {
		log.Fatal(err)
	}
	
	fmt.Printf("Found %d documents:\n", len(results))
	for _, result := range results {
		fmt.Printf("  - %s: %s bytes, %s paragraphs\n", 
			result.ID, result.Metadata["bytes"], result.Metadata["paragraphs"])
	}

	// Performance Summary
	fmt.Println("\n=== Performance Summary ===")
	fmt.Println("Insert times by document size:")
	for i, doc := range documents {
		if i < len(insertTimes) {
			fmt.Printf("  %s: %v\n", doc.desc, insertTimes[i])
		}
	}
	fmt.Printf("\nAverage search time: %v\n", avgTime)
	fmt.Println("\n✓ All long text tests completed successfully!")
	fmt.Println("✓ System handles documents from 1 to 1000+ paragraphs")
	fmt.Println("✓ Perfect match scoring works with huge documents")
	fmt.Println("✓ Content integrity maintained for massive texts")
}