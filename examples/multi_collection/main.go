package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

// Product represents an e-commerce product
type Product struct {
	ID          string
	Name        string
	Description string
	Category    string
	Price       float64
	Brand       string
}

// User represents a user profile
type User struct {
	ID         string
	Name       string
	Interests  []string
	SearchHist []string
}

// Review represents a product review
type Review struct {
	ProductID string
	UserID    string
	Text      string
	Rating    int
}

// generateEmbedding creates embeddings for different types of content
func generateEmbedding(text string, seed int, dim int) []float32 {
	h := seed
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
	fmt.Println("=== Multi-Collection Example ===")
	fmt.Println("Managing different data types in separate collections")
	fmt.Println()

	// Initialize database
	dbPath := "multi_collection.db"
	defer func() { _ = os.Remove(dbPath) }()

	config := sqvect.Config{
		Path:       dbPath,
		Dimensions: 128, // Default dimensions
	}

	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	vectorStore := db.Vector()
	_ = db.Quick() // Using vectorStore directly for this example

	// Step 1: Create collections for different data types
	fmt.Println("1. Creating Collections")
	
	collections := []struct {
		name string
		dim  int
		desc string
	}{
		{"products", 256, "Product catalog embeddings"},
		{"users", 128, "User profile embeddings"},
		{"reviews", 128, "Review text embeddings"},
		{"images", 512, "Product image embeddings"},
		{"searches", 128, "Search query embeddings"},
	}
	
	for _, col := range collections {
		collection, err := vectorStore.CreateCollection(ctx, col.name, col.dim)
		if err != nil {
			fmt.Printf("   ⚠ Collection %s might already exist: %v\n", col.name, err)
		} else {
			fmt.Printf("   ✓ Created: %s (%d dims) - %s\n",
				collection.Name, collection.Dimensions, col.desc)
		}
	}

	// Step 2: Index products
	fmt.Println("\n2. Indexing Products")
	
	products := []Product{
		// Electronics
		{"p1", "iPhone 15 Pro", "Latest iPhone with titanium design and A17 Pro chip", "Electronics", 999.99, "Apple"},
		{"p2", "Samsung Galaxy S24", "Android flagship with AI features", "Electronics", 899.99, "Samsung"},
		{"p3", "MacBook Air M3", "Thin and light laptop with Apple Silicon", "Electronics", 1299.99, "Apple"},
		{"p4", "Sony WH-1000XM5", "Premium noise-canceling headphones", "Electronics", 399.99, "Sony"},
		
		// Clothing
		{"p5", "Nike Air Max", "Classic running shoes with Air cushioning", "Clothing", 149.99, "Nike"},
		{"p6", "Levi's 501 Jeans", "Original fit denim jeans", "Clothing", 79.99, "Levi's"},
		{"p7", "Patagonia Fleece", "Sustainable outdoor fleece jacket", "Clothing", 199.99, "Patagonia"},
		
		// Books
		{"p8", "The Pragmatic Programmer", "Software development best practices", "Books", 39.99, ""},
		{"p9", "Deep Learning", "Comprehensive ML textbook by Goodfellow", "Books", 89.99, ""},
		{"p10", "Clean Code", "Writing maintainable software", "Books", 34.99, ""},
	}
	
	for _, product := range products {
		// Generate product embedding from name + description
		productText := product.Name + " " + product.Description + " " + product.Brand
		embedding := generateEmbedding(productText, 1, 256)
		
		// Create embedding with metadata
		emb := &core.Embedding{
			ID:         product.ID,
			Collection: "products",
			Vector:     embedding,
			Content:    product.Name,
			Metadata: map[string]string{
				"category":    product.Category,
				"price":       fmt.Sprintf("%.2f", product.Price),
				"brand":       product.Brand,
				"description": product.Description,
			},
		}
		
		if err := vectorStore.Upsert(ctx, emb); err != nil {
			log.Printf("Failed to index product: %v", err)
		} else {
			fmt.Printf("   ✓ Indexed: %s ($%.2f)\n", product.Name, product.Price)
		}
	}

	// Step 3: Index users
	fmt.Println("\n3. Indexing Users")
	
	users := []User{
		{"u1", "Tech Enthusiast", []string{"smartphones", "laptops", "gadgets"}, []string{"iPhone", "MacBook", "AirPods"}},
		{"u2", "Fashion Lover", []string{"clothing", "shoes", "accessories"}, []string{"Nike", "Adidas", "Zara"}},
		{"u3", "Bookworm", []string{"programming", "science", "fiction"}, []string{"Python", "algorithms", "sci-fi"}},
		{"u4", "Outdoor Explorer", []string{"hiking", "camping", "sports"}, []string{"backpack", "tent", "boots"}},
	}
	
	for _, user := range users {
		// Generate user embedding from interests and search history
		userText := user.Name + " " + fmt.Sprint(user.Interests) + " " + fmt.Sprint(user.SearchHist)
		embedding := generateEmbedding(userText, 2, 128)
		
		emb := &core.Embedding{
			ID:         user.ID,
			Collection: "users",
			Vector:     embedding,
			Content:    user.Name,
			Metadata: map[string]string{
				"interests": fmt.Sprint(user.Interests),
				"searches":  fmt.Sprint(user.SearchHist),
			},
		}
		
		if err := vectorStore.Upsert(ctx, emb); err != nil {
			log.Printf("Failed to index user: %v", err)
		} else {
			fmt.Printf("   ✓ Indexed: %s\n", user.Name)
		}
	}

	// Step 4: Index reviews
	fmt.Println("\n4. Indexing Reviews")
	
	reviews := []Review{
		{"p1", "u1", "Amazing phone! The camera quality is outstanding.", 5},
		{"p3", "u1", "Fast and reliable. Great for development work.", 5},
		{"p5", "u2", "Comfortable and stylish. Perfect for running.", 4},
		{"p8", "u3", "Must-read for every programmer. Timeless advice.", 5},
		{"p2", "u1", "Good phone but prefer iOS ecosystem.", 3},
		{"p7", "u4", "Warm and eco-friendly. Great for hiking.", 5},
	}
	
	for i, review := range reviews {
		embedding := generateEmbedding(review.Text, 3+i, 128)
		
		emb := &core.Embedding{
			ID:         fmt.Sprintf("r%d", i+1),
			Collection: "reviews",
			Vector:     embedding,
			Content:    review.Text,
			Metadata: map[string]string{
				"product_id": review.ProductID,
				"user_id":    review.UserID,
				"rating":     fmt.Sprintf("%d", review.Rating),
			},
		}
		
		if err := vectorStore.Upsert(ctx, emb); err != nil {
			log.Printf("Failed to index review: %v", err)
		} else {
			fmt.Printf("   ✓ Indexed review for %s (★%d)\n", review.ProductID, review.Rating)
		}
	}

	// Step 5: Collection statistics
	fmt.Println("\n5. Collection Statistics")
	
	allCollections, err := vectorStore.ListCollections(ctx)
	if err != nil {
		log.Printf("Failed to list collections: %v", err)
	} else {
		for _, col := range allCollections {
			stats, err := vectorStore.GetCollectionStats(ctx, col.Name)
			if err != nil {
				continue
			}
			fmt.Printf("   • %s: %d embeddings (%d dimensions)\n",
				col.Name, stats.Count, stats.Dimensions)
		}
	}

	// Step 6: Cross-collection search
	fmt.Println("\n6. Cross-Collection Search")
	fmt.Println("   Query: 'comfortable running shoes with good reviews'")
	
	searchQuery := "comfortable running shoes with good reviews"
	
	// Search in products
	fmt.Println("\n   A. Product Search:")
	productQuery := generateEmbedding(searchQuery, 10, 256)
	productResults, err := vectorStore.Search(ctx, productQuery, core.SearchOptions{
		Collection: "products",
		TopK:       3,
	})
	if err == nil {
		for i, result := range productResults {
			meta := result.Metadata
			fmt.Printf("      %d. %s - $%s (Score: %.3f)\n",
				i+1, result.Content, meta["price"], result.Score)
		}
	}
	
	// Search in reviews
	fmt.Println("\n   B. Review Search:")
	reviewQuery := generateEmbedding(searchQuery, 11, 128)
	reviewResults, err := vectorStore.Search(ctx, reviewQuery, core.SearchOptions{
		Collection: "reviews",
		TopK:       3,
	})
	if err == nil {
		for i, result := range reviewResults {
			rating := result.Metadata["rating"]
			preview := result.Content
			if len(preview) > 30 {
				preview = preview[:30]
			}
			fmt.Printf("      %d. \"%s...\" (★%s, Score: %.3f)\n",
				i+1, preview, rating, result.Score)
		}
	}

	// Step 7: User-based recommendations
	fmt.Println("\n7. Personalized Recommendations")
	
	// Get user profile by searching
	userName := "Fashion Lover"
	userQuery := generateEmbedding(userName, 2, 128)
	userResults, err := vectorStore.Search(ctx, userQuery, core.SearchOptions{
		Collection: "users",
		TopK:       1,
	})
	if err != nil || len(userResults) == 0 {
		log.Printf("Failed to get user: %v", err)
	} else {
		userEmb := userResults[0]
		fmt.Printf("   User: %s\n", userEmb.Content)
		fmt.Printf("   Interests: %s\n", userEmb.Metadata["interests"])
		
		// Find similar products
		fmt.Println("   Recommended products:")
		productRecs, err := vectorStore.Search(ctx, userEmb.Vector[:256], core.SearchOptions{
			Collection: "products",
			TopK:       3,
		})
		if err == nil {
			for i, rec := range productRecs {
				fmt.Printf("      %d. %s (%s) - $%s\n",
					i+1, rec.Content, rec.Metadata["category"], rec.Metadata["price"])
			}
		}
	}

	// Step 8: Similar users (collaborative filtering)
	fmt.Println("\n8. Finding Similar Users")
	
	targetName := "Tech Enthusiast"
	targetQuery := generateEmbedding(targetName, 1, 128)
	targetResults, err := vectorStore.Search(ctx, targetQuery, core.SearchOptions{
		Collection: "users",
		TopK:       1,
	})
	if err == nil && len(targetResults) > 0 {
		targetEmb := targetResults[0]
		fmt.Printf("   Target user: %s\n", targetEmb.Content)
		
		similarUsers, err := vectorStore.Search(ctx, targetEmb.Vector, core.SearchOptions{
			Collection: "users",
			TopK:       5,
		})
		if err == nil {
			fmt.Println("   Similar users:")
			for _, user := range similarUsers {
				if user.Content != targetEmb.Content {
					fmt.Printf("      • %s (Similarity: %.3f)\n",
						user.Content, user.Score)
				}
			}
		}
	}

	// Step 9: Search history tracking
	fmt.Println("\n9. Search History Tracking")
	
	searches := []string{
		"wireless headphones",
		"programming books",
		"running shoes Nike",
		"laptop for development",
	}
	
	fmt.Println("   Recording search queries...")
	for i, search := range searches {
		embedding := generateEmbedding(search, 100+i, 128)
		
		emb := &core.Embedding{
			ID:         fmt.Sprintf("search_%d", i),
			Collection: "searches",
			Vector:     embedding,
			Content:    search,
			Metadata: map[string]string{
				"timestamp": time.Now().Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
				"user_id":   "u1",
			},
		}
		
		if err := vectorStore.Upsert(ctx, emb); err != nil {
			log.Printf("Failed to record search: %v", err)
		} else {
			fmt.Printf("   ✓ Recorded: \"%s\"\n", search)
		}
	}
	
	// Find trending searches
	fmt.Println("\n   Trending searches (similar to 'programming'):")
	trendQuery := generateEmbedding("programming", 200, 128)
	trendResults, err := vectorStore.Search(ctx, trendQuery, core.SearchOptions{
		Collection: "searches",
		TopK:       3,
	})
	if err == nil {
		for _, result := range trendResults {
			fmt.Printf("      • \"%s\" at %s\n",
				result.Content, result.Metadata["timestamp"][:10])
		}
	}

	// Step 10: Collection migration example
	fmt.Println("\n10. Collection Migration")
	fmt.Println("   Migrating high-rated products to 'featured' collection...")
	
	// Create featured collection
	_, err = vectorStore.CreateCollection(ctx, "featured", 256)
	if err != nil {
		fmt.Printf("   Note: Featured collection might exist: %v\n", err)
	}
	
	// Find and migrate high-rated products
	allProducts, _ := vectorStore.Search(ctx, generateEmbedding("", 0, 256), core.SearchOptions{
		Collection: "products",
		TopK:       100,
	})
	
	featured := 0
	for _, product := range allProducts {
		// Check if product has good reviews (simplified)
		if product.Metadata["category"] == "Electronics" {
			featuredEmb := &core.Embedding{
				ID:         "featured_" + product.ID,
				Collection: "featured",
				Vector:     product.Vector,
				Content:    product.Content + " ⭐",
				Metadata:   product.Metadata,
			}
			
			if err := vectorStore.Upsert(ctx, featuredEmb); err == nil {
				featured++
			}
		}
	}
	fmt.Printf("   ✓ Migrated %d products to featured collection\n", featured)

	// Step 11: Collection cleanup
	fmt.Println("\n11. Collection Management")
	
	// Delete a collection (example)
	fmt.Println("   Cleaning up test collections...")
	if err := vectorStore.DeleteCollection(ctx, "featured"); err != nil {
		fmt.Printf("   Failed to delete featured collection: %v\n", err)
	} else {
		fmt.Println("   ✓ Deleted featured collection")
	}
	
	// Final statistics
	fmt.Println("\n12. Final Statistics")
	finalCollections, _ := vectorStore.ListCollections(ctx)
	totalEmbeddings := 0
	for _, col := range finalCollections {
		stats, _ := vectorStore.GetCollectionStats(ctx, col.Name)
		totalEmbeddings += int(stats.Count)
	}
	
	fmt.Printf("   Total collections: %d\n", len(finalCollections))
	fmt.Printf("   Total embeddings: %d\n", totalEmbeddings)
	fmt.Printf("   Database size: %s\n", dbPath)

	fmt.Println("\n✨ Multi-Collection Example Complete!")
	fmt.Println("This example demonstrated:")
	fmt.Println("  • Creating and managing multiple collections")
	fmt.Println("  • Different embedding dimensions per collection")
	fmt.Println("  • Cross-collection search")
	fmt.Println("  • User-based recommendations")
	fmt.Println("  • Search history tracking")
	fmt.Println("  • Collection migration and management")
}