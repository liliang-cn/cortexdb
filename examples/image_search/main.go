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

	"github.com/liliang-cn/sqvect/pkg/sqvect"
)

// Image represents an image with metadata
type Image struct {
	ID          string
	Filename    string
	Description string
	Tags        []string
	Width       int
	Height      int
	Format      string
}

// simulateCLIPEmbedding simulates CLIP-like image embeddings
// In production, you would use actual CLIP or similar models
func simulateCLIPEmbedding(description string, tags []string, seed int) []float32 {
	dim := 512 // CLIP embedding dimension
	embedding := make([]float32, dim)
	
	// Combine description and tags for embedding
	text := description + " " + strings.Join(tags, " ")
	
	h := seed
	for _, r := range text {
		h = h*31 + int(r)
	}
	rand.Seed(int64(h))
	
	// Generate base embedding
	for i := 0; i < dim; i++ {
		embedding[i] = rand.Float32()*2 - 1
	}
	
	// Add tag-specific patterns
	for _, tag := range tags {
		tagHash := 0
		for _, r := range tag {
			tagHash += int(r)
		}
		// Modify specific dimensions based on tags
		for i := 0; i < 10 && i < dim; i++ {
			idx := (tagHash + i) % dim
			embedding[idx] += 0.1
		}
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

// generateTextEmbedding generates embedding for text queries
func generateTextEmbedding(text string, dim int) []float32 {
	return simulateCLIPEmbedding(text, []string{}, 0)
}

func main() {
	fmt.Println("=== Image Search Example ===")
	fmt.Println("Multi-modal search using CLIP-like embeddings")
	fmt.Println()

	// Initialize database
	dbPath := "image_search.db"
	defer os.Remove(dbPath)

	config := sqvect.Config{
		Path:       dbPath,
		Dimensions: 512, // CLIP dimension
	}

	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	ctx := context.Background()
	quick := db.Quick()
	vectorStore := db.Vector()

	// Sample image dataset
	images := []Image{
		// Nature
		{
			ID:          "img001",
			Filename:    "sunset_beach.jpg",
			Description: "Beautiful sunset over the ocean with orange and pink sky",
			Tags:        []string{"sunset", "beach", "ocean", "nature", "landscape"},
			Width:       1920, Height: 1080, Format: "jpg",
		},
		{
			ID:          "img002",
			Filename:    "mountain_lake.jpg",
			Description: "Snow-capped mountains reflected in a crystal clear alpine lake",
			Tags:        []string{"mountain", "lake", "nature", "landscape", "reflection"},
			Width:       2048, Height: 1536, Format: "jpg",
		},
		{
			ID:          "img003",
			Filename:    "forest_path.jpg",
			Description: "Winding path through a dense green forest with sunlight filtering through trees",
			Tags:        []string{"forest", "path", "trees", "nature", "hiking"},
			Width:       1600, Height: 1200, Format: "jpg",
		},
		// Animals
		{
			ID:          "img004",
			Filename:    "golden_retriever.jpg",
			Description: "Happy golden retriever dog playing in a park",
			Tags:        []string{"dog", "golden retriever", "pet", "animal", "park"},
			Width:       1280, Height: 960, Format: "jpg",
		},
		{
			ID:          "img005",
			Filename:    "cat_window.jpg",
			Description: "Curious cat sitting by a window watching birds outside",
			Tags:        []string{"cat", "pet", "animal", "window", "indoor"},
			Width:       1024, Height: 768, Format: "jpg",
		},
		{
			ID:          "img006",
			Filename:    "wild_elephant.jpg",
			Description: "Majestic elephant walking across African savanna",
			Tags:        []string{"elephant", "wildlife", "africa", "savanna", "animal"},
			Width:       1920, Height: 1280, Format: "jpg",
		},
		// Urban
		{
			ID:          "img007",
			Filename:    "city_skyline.jpg",
			Description: "Modern city skyline at night with illuminated skyscrapers",
			Tags:        []string{"city", "skyline", "night", "urban", "architecture"},
			Width:       2560, Height: 1440, Format: "jpg",
		},
		{
			ID:          "img008",
			Filename:    "street_cafe.jpg",
			Description: "Cozy street cafe with outdoor seating in European city",
			Tags:        []string{"cafe", "street", "urban", "restaurant", "europe"},
			Width:       1440, Height: 1080, Format: "jpg",
		},
		// Food
		{
			ID:          "img009",
			Filename:    "sushi_platter.jpg",
			Description: "Colorful sushi platter with various types of sushi and sashimi",
			Tags:        []string{"food", "sushi", "japanese", "seafood", "cuisine"},
			Width:       1200, Height: 900, Format: "jpg",
		},
		{
			ID:          "img010",
			Filename:    "coffee_latte.jpg",
			Description: "Cappuccino with beautiful latte art in a ceramic cup",
			Tags:        []string{"coffee", "latte", "beverage", "cafe", "drink"},
			Width:       800, Height: 800, Format: "jpg",
		},
		// Technology
		{
			ID:          "img011",
			Filename:    "laptop_desk.jpg",
			Description: "Modern workspace with laptop, monitor, and accessories",
			Tags:        []string{"technology", "laptop", "workspace", "office", "computer"},
			Width:       1920, Height: 1080, Format: "jpg",
		},
		{
			ID:          "img012",
			Filename:    "smartphone.jpg",
			Description: "Latest smartphone displaying colorful app interface",
			Tags:        []string{"technology", "smartphone", "mobile", "apps", "device"},
			Width:       1080, Height: 1920, Format: "jpg",
		},
	}

	// Step 1: Index images
	fmt.Println("1. Indexing Images")
	fmt.Println("   Creating multi-modal embeddings...")
	
	for i, img := range images {
		// Generate CLIP-like embedding
		embedding := simulateCLIPEmbedding(img.Description, img.Tags, i+1)
		
		// Create metadata string
		metadata := fmt.Sprintf("%s | %s | %dx%d",
			img.Filename,
			strings.Join(img.Tags, ", "),
			img.Width, img.Height)
		
		id, err := quick.Add(ctx, embedding, metadata)
		if err != nil {
			log.Printf("Failed to index image: %v", err)
			continue
		}
		
		// Store ID mapping
		images[i].ID = id
		fmt.Printf("   ✓ Indexed: %s (%d tags)\n", img.Filename, len(img.Tags))
	}

	// Step 2: Text-to-image search
	fmt.Println("\n2. Text-to-Image Search")
	
	textQueries := []string{
		"sunset on the beach",
		"cute pets",
		"mountain landscape",
		"coffee in cafe",
		"modern technology",
		"wildlife in nature",
	}
	
	for _, query := range textQueries {
		fmt.Printf("\n   Query: \"%s\"\n", query)
		fmt.Println("   " + strings.Repeat("-", 40))
		
		queryEmbedding := generateTextEmbedding(query, 512)
		results, err := quick.Search(ctx, queryEmbedding, 3)
		if err != nil {
			log.Printf("Search failed: %v", err)
			continue
		}
		
		for i, result := range results {
			// Parse metadata
			parts := strings.Split(result.Content, " | ")
			if len(parts) > 0 {
				fmt.Printf("   %d. %s (Score: %.3f)\n",
					i+1, parts[0], result.Score)
				if len(parts) > 1 {
					fmt.Printf("      Tags: %s\n", parts[1])
				}
			}
		}
	}

	// Step 3: Image-to-image search (find similar images)
	fmt.Println("\n3. Image-to-Image Search")
	fmt.Println("   Finding images similar to 'sunset_beach.jpg'...")
	
	// Get embedding of first image
	referenceImg := images[0]
	refEmbedding := simulateCLIPEmbedding(referenceImg.Description, referenceImg.Tags, 1)
	
	similarImages, err := quick.Search(ctx, refEmbedding, 5)
	if err == nil {
		fmt.Printf("\n   Reference: %s\n", referenceImg.Filename)
		fmt.Println("   Similar images:")
		for i, result := range similarImages {
			if i == 0 {
				continue // Skip self
			}
			parts := strings.Split(result.Content, " | ")
			if len(parts) > 0 {
				fmt.Printf("   %d. %s (Similarity: %.3f)\n",
					i, parts[0], result.Score)
			}
		}
	}

	// Step 4: Tag-based filtering
	fmt.Println("\n4. Tag-Based Search")
	
	tagGroups := map[string][]string{
		"Nature":     {"nature", "landscape", "outdoor"},
		"Animals":    {"animal", "pet", "wildlife"},
		"Urban":      {"city", "urban", "architecture"},
		"Food":       {"food", "cuisine", "beverage"},
		"Technology": {"technology", "computer", "device"},
	}
	
	for category, tags := range tagGroups {
		fmt.Printf("\n   Category: %s\n", category)
		
		// Create query from tags
		tagQuery := strings.Join(tags, " ")
		queryEmb := generateTextEmbedding(tagQuery, 512)
		
		results, err := quick.Search(ctx, queryEmb, 2)
		if err != nil {
			continue
		}
		
		for _, result := range results {
			parts := strings.Split(result.Content, " | ")
			if len(parts) > 0 {
				fmt.Printf("      • %s (Score: %.3f)\n",
					parts[0], result.Score)
			}
		}
	}

	// Step 5: Complex queries
	fmt.Println("\n5. Complex Multi-Modal Queries")
	
	complexQueries := []struct {
		text   string
		filter string
	}{
		{"animals in natural habitat", "outdoor"},
		{"food and drinks in restaurant setting", "indoor"},
		{"technology in modern workspace", "office"},
	}
	
	for _, cq := range complexQueries {
		fmt.Printf("\n   Query: \"%s\" + filter: %s\n", cq.text, cq.filter)
		
		queryEmb := generateTextEmbedding(cq.text+" "+cq.filter, 512)
		results, err := quick.Search(ctx, queryEmb, 3)
		if err != nil {
			continue
		}
		
		for i, result := range results {
			parts := strings.Split(result.Content, " | ")
			if len(parts) > 1 {
				// Check if result contains filter tag
				if strings.Contains(parts[1], cq.filter) {
					fmt.Printf("   %d. %s ✓ (Score: %.3f)\n",
						i+1, parts[0], result.Score)
				} else {
					fmt.Printf("   %d. %s (Score: %.3f)\n",
						i+1, parts[0], result.Score)
				}
			}
		}
	}

	// Step 6: Image clustering
	fmt.Println("\n6. Image Clustering Analysis")
	
	// Group images by similarity
	threshold := float32(0.7)
	clusters := make(map[int][]string)
	assigned := make(map[string]int)
	clusterID := 0
	
	for _, img := range images {
		if _, exists := assigned[img.ID]; exists {
			continue
		}
		
		// Create new cluster
		clusterID++
		clusters[clusterID] = []string{img.Filename}
		assigned[img.ID] = clusterID
		
		// Find similar images
		embedding := simulateCLIPEmbedding(img.Description, img.Tags, 0)
		similar, _ := quick.Search(ctx, embedding, 10)
		
		for _, sim := range similar {
			if float32(sim.Score) > threshold {
				parts := strings.Split(sim.Content, " | ")
				if len(parts) > 0 && parts[0] != img.Filename {
					if _, exists := assigned[sim.ID]; !exists {
						clusters[clusterID] = append(clusters[clusterID], parts[0])
						assigned[sim.ID] = clusterID
					}
				}
			}
		}
	}
	
	fmt.Printf("   Found %d clusters:\n", len(clusters))
	for id, members := range clusters {
		if len(members) > 1 {
			fmt.Printf("   Cluster %d (%d images):\n", id, len(members))
			for _, member := range members {
				fmt.Printf("      • %s\n", member)
			}
		}
	}

	// Step 7: Resolution-based search
	fmt.Println("\n7. Resolution-Based Filtering")
	
	resolutionGroups := map[string]func(w, h int) bool{
		"HD (1920x1080+)": func(w, h int) bool { return w >= 1920 || h >= 1080 },
		"Medium (1024-1920)": func(w, h int) bool { return w >= 1024 && w < 1920 },
		"Square": func(w, h int) bool { return math.Abs(float64(w-h)) < 100 },
	}
	
	for groupName, filterFunc := range resolutionGroups {
		fmt.Printf("\n   %s:\n", groupName)
		count := 0
		for _, img := range images {
			if filterFunc(img.Width, img.Height) {
				fmt.Printf("      • %s (%dx%d)\n",
					img.Filename, img.Width, img.Height)
				count++
				if count >= 3 {
					break
				}
			}
		}
	}

	// Step 8: Color palette simulation
	fmt.Println("\n8. Color-Based Search (Simulated)")
	
	colorQueries := map[string][]string{
		"Warm colors": {"sunset", "orange", "warm"},
		"Cool colors": {"ocean", "blue", "cool"},
		"Green nature": {"forest", "green", "nature"},
	}
	
	for colorDesc, keywords := range colorQueries {
		fmt.Printf("\n   %s:\n", colorDesc)
		
		colorQuery := strings.Join(keywords, " ")
		queryEmb := generateTextEmbedding(colorQuery, 512)
		
		results, _ := quick.Search(ctx, queryEmb, 2)
		for _, result := range results {
			parts := strings.Split(result.Content, " | ")
			if len(parts) > 0 {
				fmt.Printf("      • %s (Score: %.3f)\n",
					parts[0], result.Score)
			}
		}
	}

	// Step 9: Duplicate detection
	fmt.Println("\n9. Duplicate/Near-Duplicate Detection")
	
	// Find images with very high similarity
	duplicateThreshold := float32(0.95)
	duplicates := []struct {
		img1, img2 string
		similarity float32
	}{}
	
	for i, img1 := range images {
		emb1 := simulateCLIPEmbedding(img1.Description, img1.Tags, i+1)
		
		for j, img2 := range images {
			if i >= j {
				continue
			}
			
			emb2 := simulateCLIPEmbedding(img2.Description, img2.Tags, j+1)
			
			// Calculate similarity
			var similarity float32
			for k := 0; k < len(emb1); k++ {
				similarity += emb1[k] * emb2[k]
			}
			
			if similarity > duplicateThreshold {
				duplicates = append(duplicates, struct {
					img1, img2 string
					similarity float32
				}{img1.Filename, img2.Filename, similarity})
			}
		}
	}
	
	if len(duplicates) > 0 {
		fmt.Println("   Found potential duplicates:")
		for _, dup := range duplicates {
			fmt.Printf("      • %s ≈ %s (Similarity: %.3f)\n",
				dup.img1, dup.img2, dup.similarity)
		}
	} else {
		fmt.Println("   No duplicates found (threshold: 0.95)")
	}

	// Step 10: Performance metrics
	fmt.Println("\n10. Search Performance Analysis")
	
	// Measure search times
	searchSizes := []int{1, 5, 10}
	
	for _, k := range searchSizes {
		testQuery := generateTextEmbedding("test query", 512)
		
		start := time.Now()
		iterations := 100
		for i := 0; i < iterations; i++ {
			_, _ = quick.Search(ctx, testQuery, k)
		}
		elapsed := time.Since(start)
		
		fmt.Printf("   Top-%d search (100 iterations): %.2f ms avg\n",
			k, float64(elapsed.Milliseconds())/float64(iterations))
	}
	
	// Database statistics
	stats, _ := vectorStore.GetCollectionStats(ctx, "")
	fmt.Printf("\n   Total images indexed: %d\n", stats.Count)
	fmt.Printf("   Embedding dimensions: %d\n", stats.Dimensions)
	fmt.Printf("   Database file: %s\n", dbPath)

	fmt.Println("\n✨ Image Search Example Complete!")
	fmt.Println("This example demonstrated:")
	fmt.Println("  • Multi-modal image search with CLIP-like embeddings")
	fmt.Println("  • Text-to-image and image-to-image search")
	fmt.Println("  • Tag-based and complex query filtering")
	fmt.Println("  • Image clustering and duplicate detection")
	fmt.Println("  • Resolution and color-based filtering")
	fmt.Println("  • Performance optimization for image search")
}