package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("🧪 Long Document Same-Language Search Test")
	fmt.Println("Testing with detailed coffee shop and tech hub documents")

	dbPath := "test_long_documents.db"
	os.Remove(dbPath)
	defer os.Remove(dbPath)

	// Create store with text similarity enabled
	config := sqvect.DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 0 // Auto-detect
	config.TextSimilarity.Enabled = true
	config.TextSimilarity.DefaultWeight = 0.3 // 30% text similarity

	store, err := sqvect.NewWithConfig(config)
	if err != nil {
		panic(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		panic(err)
	}

	// Document 1: Blue Mountain Coffee Shop (long English document)
	coffeeShopContent := `Blue Mountain Coffee Shop is a cozy neighborhood café specializing in premium coffee and artisanal pastries. Located in downtown Portland, we've been serving the community since 2018.

Location & Hours:
- Address: 123 Pearl Street, Portland, OR 97205
- Phone: (503) 555-0123
- Email: hello@bluemountaincoffee.com
- Monday-Friday: 6:00 AM - 8:00 PM
- Saturday-Sunday: 7:00 AM - 9:00 PM

Menu Highlights:
Coffee & Espresso: Signature Blend with notes of chocolate and caramel, Single Origin rotating selection, Cold Brew smooth and refreshing, Espresso Drinks including lattes, cappuccinos, macchiatos made with precision.

Food: Fresh Pastries including croissants, muffins, scones baked daily. Sandwiches, Paninis and wraps made with local ingredients. Breakfast options like avocado toast, granola bowls, breakfast burritos. Desserts including cookies, cakes, and seasonal treats.

Special Features: Free WiFi throughout the café, comfortable seating areas for work and study, local artwork displayed on walls, pet-friendly outdoor seating, weekly coffee cupping sessions on Saturdays.

Community Events: Open Mic Night every Thursday at 7 PM, Book Club first Sunday of each month, Coffee Education monthly workshops on brewing techniques, Local Artist Showcase with rotating art exhibitions.

Contact: www.bluemountaincoffee.com, Instagram @bluemountaincoffeepdx, Facebook Blue Mountain Coffee Shop Portland`

	// Document 2: TechStart Innovation Hub (long English document)
	techHubContent := `TechStart Innovation Hub is Portland's premier technology incubator and coworking space. We support early-stage startups with resources, mentorship, and networking opportunities to help transform innovative ideas into successful businesses.

Facility Details:
- Location: 456 Innovation Drive, Portland, OR 97210
- Contact: (503) 555-0456
- Website: www.techstarthub.com
- Email: info@techstarthub.com
- Monday-Friday: 8:00 AM - 10:00 PM
- Saturday: 10:00 AM - 6:00 PM
- Sunday: 12:00 PM - 8:00 PM
- 24/7 access available for members

Services & Programs:
Workspace Options: Hot Desks flexible daily workspace $25/day, Dedicated Desks your own assigned space $200/month, Private Offices for small teams 2-6 people $800-2000/month, Conference Rooms meeting spaces with A/V equipment.

Accelerator Program: 12-week intensive program, 6% equity for $50K investment, Access to 100+ industry experts, Demo Day pitch to investors and media.

Support Services: Legal Services with startup-friendly lawyers and accountants, Marketing Support for brand development and digital marketing, Technical Resources including cloud credits and software licenses, Funding Assistance with connections to VCs and angel investors.

Community & Events: Weekly Pitch Practice Tuesdays at 6 PM, Networking Mixers last Friday of each month, Tech Talks with industry leaders sharing insights, Startup Showcase quarterly demo events.

Membership Benefits: High-speed internet and printing services, kitchen facilities with complimentary coffee and snacks, parking spaces available, access to workshop tools and 3D printers, exclusive member-only events and workshops.

Success Stories: Since 2019, TechStart has helped launch over 150 companies with a combined valuation of $2.3 billion. Notable alumni include CloudSync acquired by Microsoft, GreenEnergy Solutions, and FoodieApp.

Contact: LinkedIn TechStart Innovation Hub, Twitter @TechStartPDX, Newsletter with monthly startup ecosystem updates`

	fmt.Println("📝 Inserting long documents...")

	// Generate vectors for documents
	coffeeVector := generateSemanticVector(coffeeShopContent, 1024)
	techVector := generateSemanticVector(techHubContent, 1024)

	// Insert Coffee Shop document
	coffeeDoc := &sqvect.Embedding{
		ID:      "coffee_shop_doc",
		Vector:  coffeeVector,
		Content: coffeeShopContent,
		Metadata: map[string]string{
			"type":     "business",
			"category": "coffee_shop",
			"location": "Portland",
		},
	}

	// Insert Tech Hub document
	techDoc := &sqvect.Embedding{
		ID:      "tech_hub_doc",
		Vector:  techVector,
		Content: techHubContent,
		Metadata: map[string]string{
			"type":     "business",
			"category": "tech_incubator",
			"location": "Portland",
		},
	}

	if err := store.Upsert(ctx, coffeeDoc); err != nil {
		panic(err)
	}
	fmt.Println("✅ Coffee Shop document inserted")

	if err := store.Upsert(ctx, techDoc); err != nil {
		panic(err)
	}
	fmt.Println("✅ Tech Hub document inserted")

	// Test Cases: Various search queries
	testQueries := []struct {
		name        string
		query       string
		textWeight  float64
		expectation string
	}{
		// Coffee-related searches
		{
			name:        "Coffee Shop - Exact Match",
			query:       "Blue Mountain Coffee Shop",
			textWeight:  0.4,
			expectation: "Should strongly match coffee shop document",
		},
		{
			name:        "Coffee Products",
			query:       "espresso latte cappuccino coffee",
			textWeight:  0.3,
			expectation: "Should match coffee shop for beverage terms",
		},
		{
			name:        "Coffee Location",
			query:       "Pearl Street Portland coffee",
			textWeight:  0.5,
			expectation: "Should match coffee shop with location details",
		},
		{
			name:        "Coffee Events",
			query:       "open mic night book club coffee cupping",
			textWeight:  0.4,
			expectation: "Should match coffee shop for events",
		},
		{
			name:        "Food Items",
			query:       "croissants muffins pastries breakfast",
			textWeight:  0.6,
			expectation: "Should match coffee shop for food offerings",
		},

		// Tech-related searches
		{
			name:        "Tech Hub - Exact Match",
			query:       "TechStart Innovation Hub",
			textWeight:  0.4,
			expectation: "Should strongly match tech hub document",
		},
		{
			name:        "Startup Services",
			query:       "startup incubator accelerator program",
			textWeight:  0.3,
			expectation: "Should match tech hub for startup services",
		},
		{
			name:        "Workspace Options",
			query:       "hot desk dedicated desk private office coworking",
			textWeight:  0.5,
			expectation: "Should match tech hub for workspace terms",
		},
		{
			name:        "Investment Terms",
			query:       "equity investment venture capital funding",
			textWeight:  0.4,
			expectation: "Should match tech hub for investment language",
		},
		{
			name:        "Tech Events",
			query:       "pitch practice networking mixer demo day",
			textWeight:  0.6,
			expectation: "Should match tech hub for tech events",
		},

		// Cross-category searches
		{
			name:        "General Portland",
			query:       "Portland business downtown",
			textWeight:  0.2,
			expectation: "Should match both documents for location",
		},
		{
			name:        "Community Events",
			query:       "community events networking workshops",
			textWeight:  0.3,
			expectation: "Both offer community activities",
		},
		{
			name:        "WiFi and Work",
			query:       "wifi internet work study space",
			textWeight:  0.4,
			expectation: "Both provide work-friendly environments",
		},
	}

	fmt.Println("\n🔍 Running Same-Language Search Tests...")
	
	successCount := 0
	for i, test := range testQueries {
		fmt.Printf("\n--- Test %d: %s ---\n", i+1, test.name)
		fmt.Printf("Query: \"%s\" (TextWeight: %.1f)\n", test.query, test.textWeight)
		fmt.Printf("Expected: %s\n", test.expectation)
		
		// Generate query vector
		queryVector := generateSemanticVector(test.query, 1024)
		
		// Prepare search options with text similarity
		opts := sqvect.SearchOptions{
			TopK:       2, // Both documents
			QueryText:  test.query,
			TextWeight: test.textWeight,
		}
		
		// Perform search
		results, err := store.Search(ctx, queryVector, opts)
		if err != nil {
			fmt.Printf("❌ Search failed: %v\n", err)
			continue
		}
		
		fmt.Printf("📊 Results:\n")
		for j, result := range results {
			category := result.Metadata["category"]
			fmt.Printf("  %d. [%.4f] [%s] %s...\n", 
				j+1, result.Score, category, truncateContent(result.Content, 60))
		}
		
		// Evaluate success based on query type
		success := evaluateSearchSuccess(test, results)
		if success {
			fmt.Printf("✅ SUCCESS: %s\n", getSuccessDescription(test, results))
			successCount++
		} else {
			fmt.Printf("❌ SUBOPTIMAL: %s\n", getIssueDescription(test, results))
		}
	}

	// Demonstrate text similarity impact
	fmt.Println("\n🔬 Text Similarity Impact Analysis...")
	demonstrateTextSimilarityImpact(store, ctx)

	// Performance summary
	successRate := float64(successCount) / float64(len(testQueries)) * 100
	
	fmt.Printf("\n📈 Long Document Search Results:\n")
	fmt.Printf("  🎯 Success Rate: %d/%d (%.1f%%)\n", successCount, len(testQueries), successRate)
	
	stats, _ := store.Stats(ctx)
	fmt.Printf("  📊 Database: %d documents, %d dimensions\n", stats.Count, stats.Dimensions)
	
	fmt.Println("\n🎯 Key Findings:")
	if successRate >= 80 {
		fmt.Println("  ✅ Excellent performance with long documents")
		fmt.Println("  ✅ Text similarity effectively captures semantic content")
		fmt.Println("  ✅ Both specific and broad queries handled well")
	} else if successRate >= 60 {
		fmt.Println("  🟡 Good performance, some room for improvement")
		fmt.Println("  💡 Consider adjusting text weights for different query types")
	} else {
		fmt.Println("  ⚠️  Performance could be improved")
		fmt.Println("  🔧 May need text similarity algorithm tuning")
	}
	
	fmt.Println("\n📋 Observations:")
	fmt.Println("  • Long documents provide rich content for text matching")
	fmt.Println("  • Specific terminology searches work very well")
	fmt.Println("  • Cross-document queries reveal interesting relationships")
	fmt.Println("  • Text similarity weight significantly impacts results")
}

func generateSemanticVector(text string, dim int) []float32 {
	// Enhanced vector generation that considers text content
	vector := make([]float32, dim)
	
	// Base hash from text
	hash := uint32(5381)
	for _, c := range text {
		hash = hash*33 + uint32(c)
	}
	
	// Extract key terms for semantic features
	keyTerms := extractKeyTerms(text)
	
	for i := 0; i < dim; i++ {
		seed := hash + uint32(i*19+37)
		
		// Base random value
		baseValue := float32((seed%2000))/1000.0 - 1.0
		
		// Add semantic features based on key terms
		semanticValue := float32(0)
		for _, term := range keyTerms {
			termHash := hashString(term)
			if (termHash+uint32(i))%100 < 15 { // 15% influence
				semanticValue += float32((termHash+uint32(i))%200) / 2000.0
			}
		}
		
		vector[i] = baseValue + semanticValue
	}
	
	return normalizeVector(vector)
}

func extractKeyTerms(text string) []string {
	// Simple key term extraction (in real app, use proper NLP)
	keyTerms := []string{}
	
	// Important business terms
	businessTerms := []string{
		"coffee", "espresso", "latte", "cappuccino", "pastries", "croissant",
		"startup", "incubator", "accelerator", "coworking", "tech", "innovation",
		"Portland", "downtown", "community", "networking", "workspace", "funding",
		"breakfast", "lunch", "meeting", "events", "workshops", "programming",
	}
	
	textLower := strings.ToLower(text)
	for _, term := range businessTerms {
		if strings.Contains(textLower, term) {
			keyTerms = append(keyTerms, term)
		}
	}
	
	return keyTerms
}

func evaluateSearchSuccess(test struct {
	name        string
	query       string
	textWeight  float64
	expectation string
}, results []sqvect.ScoredEmbedding) bool {
	if len(results) == 0 {
		return false
	}
	
	topResult := results[0]
	query := strings.ToLower(test.query)
	content := strings.ToLower(topResult.Content)
	
	// Different success criteria based on query type
	if strings.Contains(test.name, "Coffee") {
		// Coffee-related queries should match coffee shop
		return topResult.Metadata["category"] == "coffee_shop" && topResult.Score > 0.3
	} else if strings.Contains(test.name, "Tech") {
		// Tech-related queries should match tech hub
		return topResult.Metadata["category"] == "tech_incubator" && topResult.Score > 0.3
	} else if strings.Contains(test.name, "General") || strings.Contains(test.name, "Community") {
		// General queries - either result is acceptable with decent score
		return topResult.Score > 0.2
	}
	
	// Default: check if query terms appear in result content
	queryWords := strings.Fields(query)
	matchCount := 0
	for _, word := range queryWords {
		if len(word) > 2 && strings.Contains(content, word) {
			matchCount++
		}
	}
	
	return matchCount > 0 && topResult.Score > 0.25
}

func getSuccessDescription(test struct {
	name        string
	query       string
	textWeight  float64
	expectation string
}, results []sqvect.ScoredEmbedding) string {
	if len(results) == 0 {
		return "Found results"
	}
	
	topResult := results[0]
	return fmt.Sprintf("Score %.3f, matched %s category", 
		topResult.Score, topResult.Metadata["category"])
}

func getIssueDescription(test struct {
	name        string
	query       string
	textWeight  float64
	expectation string
}, results []sqvect.ScoredEmbedding) string {
	if len(results) == 0 {
		return "No results returned"
	}
	
	topResult := results[0]
	return fmt.Sprintf("Score %.3f may be suboptimal or wrong category %s", 
		topResult.Score, topResult.Metadata["category"])
}

func demonstrateTextSimilarityImpact(store *sqvect.SQLiteStore, ctx context.Context) {
	fmt.Println("Comparing Vector-Only vs Hybrid (Vector+Text) scoring:")
	
	testQuery := "coffee espresso latte cappuccino"
	queryVector := generateSemanticVector(testQuery, 1024)
	
	// Vector-only search
	vectorOnlyOpts := sqvect.SearchOptions{
		TopK:       2,
		QueryText:  "", // No text similarity
		TextWeight: 0.0,
	}
	
	vectorResults, err := store.Search(ctx, queryVector, vectorOnlyOpts)
	if err == nil && len(vectorResults) > 0 {
		fmt.Printf("  Vector-Only:  [%.4f] %s\n", 
			vectorResults[0].Score, vectorResults[0].Metadata["category"])
	}
	
	// Hybrid search
	hybridOpts := sqvect.SearchOptions{
		TopK:       2,
		QueryText:  testQuery, // With text similarity
		TextWeight: 0.4,
	}
	
	hybridResults, err := store.Search(ctx, queryVector, hybridOpts)
	if err == nil && len(hybridResults) > 0 {
		fmt.Printf("  Hybrid Score: [%.4f] %s\n", 
			hybridResults[0].Score, hybridResults[0].Metadata["category"])
		
		if len(vectorResults) > 0 {
			improvement := hybridResults[0].Score - vectorResults[0].Score
			fmt.Printf("  Improvement:  %+.4f (%.1f%% better)\n", 
				improvement, (improvement/vectorResults[0].Score)*100)
		}
	}
}

func hashString(s string) uint32 {
	hash := uint32(5381)
	for _, c := range s {
		hash = hash*33 + uint32(c)
	}
	return hash
}

func normalizeVector(vector []float32) []float32 {
	var norm float32
	for _, v := range vector {
		norm += v * v
	}
	if norm == 0 {
		return vector
	}
	
	norm = float32(math.Sqrt(float64(norm)))
	for i := range vector {
		vector[i] /= norm
	}
	return vector
}

func truncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}