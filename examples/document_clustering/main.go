package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"sort"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

// Cluster represents a document cluster
type Cluster struct {
	ID       string
	Centroid []float32
	Members  []string
	Label    string
}

// generateEmbedding creates embeddings with controlled similarity for demonstration
func generateEmbedding(text string, category int, noise float32) []float32 {
	dim := 128
	embedding := make([]float32, dim)
	
	// Base pattern for each category
	patterns := [][]float32{
		{1.0, 0.0, 0.0}, // Tech
		{0.0, 1.0, 0.0}, // Science
		{0.0, 0.0, 1.0}, // Business
		{1.0, 1.0, 0.0}, // Health
		{0.0, 1.0, 1.0}, // Education
	}
	
	pattern := patterns[category%len(patterns)]
	
	// Generate embedding based on pattern
	rng := rand.New(rand.NewSource(int64(len(text) + category)))
	for i := 0; i < dim; i++ {
		// Use pattern for first few dimensions
		if i < len(pattern) {
			embedding[i] = pattern[i] + (rng.Float32()-0.5)*noise
		} else {
			embedding[i] = (rng.Float32() - 0.5) * noise
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

// calculateCentroid computes the centroid of a set of vectors
func calculateCentroid(vectors [][]float32) []float32 {
	if len(vectors) == 0 {
		return nil
	}
	
	dim := len(vectors[0])
	centroid := make([]float32, dim)
	
	for _, vec := range vectors {
		for i, v := range vec {
			centroid[i] += v
		}
	}
	
	// Average
	n := float32(len(vectors))
	for i := range centroid {
		centroid[i] /= n
	}
	
	// Normalize
	var sum float32
	for _, v := range centroid {
		sum += v * v
	}
	norm := float32(math.Sqrt(float64(sum)))
	if norm > 0 {
		for i := range centroid {
			centroid[i] /= norm
		}
	}
	
	return centroid
}

func main() {
	fmt.Println("=== Document Clustering Example ===")
	fmt.Println("This example demonstrates clustering documents using vector similarity")
	fmt.Println()

	// Initialize database
	dbPath := "document_clustering.db"
	defer func() { _ = os.Remove(dbPath) }()

	config := sqvect.Config{
		Path:       dbPath,
		Dimensions: 128,
	}

	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	quick := db.Quick()
	vectorStore := db.Vector()

	// Sample documents with categories
	type Doc struct {
		Title    string
		Category int
		Content  string
	}

	documents := []Doc{
		// Technology cluster (0)
		{"Machine Learning Basics", 0, "Introduction to supervised and unsupervised learning algorithms"},
		{"Deep Neural Networks", 0, "Understanding deep learning architectures and training methods"},
		{"Computer Vision Applications", 0, "Image recognition and object detection using AI"},
		{"Natural Language Processing", 0, "Text analysis and language understanding with ML"},
		
		// Science cluster (1)
		{"Quantum Physics Fundamentals", 1, "Basic principles of quantum mechanics and wave functions"},
		{"Molecular Biology Research", 1, "DNA sequencing and protein synthesis mechanisms"},
		{"Climate Science Studies", 1, "Global warming patterns and environmental impacts"},
		{"Space Exploration Technology", 1, "Rocket propulsion and orbital mechanics"},
		
		// Business cluster (2)
		{"Market Analysis Strategies", 2, "Understanding market trends and consumer behavior"},
		{"Financial Risk Management", 2, "Portfolio optimization and risk assessment techniques"},
		{"Supply Chain Optimization", 2, "Logistics and inventory management best practices"},
		{"Digital Marketing Trends", 2, "Social media and content marketing strategies"},
		
		// Health cluster (3)
		{"Preventive Medicine Approaches", 3, "Disease prevention and health maintenance strategies"},
		{"Mental Health Treatments", 3, "Therapy techniques and psychological interventions"},
		{"Nutrition and Diet Planning", 3, "Balanced diet and nutritional requirements"},
		{"Exercise Physiology", 3, "Physical fitness and training principles"},
		
		// Education cluster (4)
		{"Online Learning Platforms", 4, "E-learning technologies and virtual classrooms"},
		{"Curriculum Development", 4, "Educational program design and assessment methods"},
		{"Student Engagement Techniques", 4, "Active learning and participation strategies"},
		{"Educational Psychology", 4, "Learning theories and cognitive development"},
	}

	// Step 1: Index all documents
	fmt.Println("1. Indexing Documents")
	docVectors := make(map[string][]float32)
	docTitles := make(map[string]string)
	
	for _, doc := range documents {
		// Generate embedding with some noise for realistic clustering
		embedding := generateEmbedding(doc.Content, doc.Category, 0.3)
		
		// Store in database
		id, err := quick.Add(ctx, embedding, doc.Title)
		if err != nil {
			log.Printf("Failed to add document: %v", err)
			continue
		}
		
		docVectors[id] = embedding
		docTitles[id] = doc.Title
		fmt.Printf("   ✓ Indexed: %s\n", doc.Title)
	}
	fmt.Printf("   Total documents indexed: %d\n\n", len(docVectors))

	// Step 2: K-means clustering
	fmt.Println("2. Performing K-Means Clustering")
	k := 5 // Number of clusters
	fmt.Printf("   Number of clusters: %d\n", k)
	
	// Initialize cluster centroids (using first k documents as seeds)
	clusters := make([]*Cluster, k)
	docIDs := make([]string, 0, len(docVectors))
	for id := range docVectors {
		docIDs = append(docIDs, id)
	}
	
	for i := 0; i < k && i < len(docIDs); i++ {
		clusters[i] = &Cluster{
			ID:       fmt.Sprintf("cluster_%d", i),
			Centroid: docVectors[docIDs[i]],
			Members:  []string{},
		}
	}
	
	// K-means iterations
	maxIterations := 10
	for iter := 0; iter < maxIterations; iter++ {
		// Clear cluster members
		for _, cluster := range clusters {
			cluster.Members = []string{}
		}
		
		// Assign documents to nearest cluster
		for docID, vector := range docVectors {
			nearestCluster := 0
			maxSimilarity := float32(-1.0)
			
			for i, cluster := range clusters {
				// Calculate cosine similarity
				similarity := cosineSimilarity(vector, cluster.Centroid)
				if similarity > maxSimilarity {
					maxSimilarity = similarity
					nearestCluster = i
				}
			}
			
			clusters[nearestCluster].Members = append(clusters[nearestCluster].Members, docID)
		}
		
		// Update centroids
		changed := false
		for _, cluster := range clusters {
			if len(cluster.Members) > 0 {
				vectors := make([][]float32, len(cluster.Members))
				for i, memberID := range cluster.Members {
					vectors[i] = docVectors[memberID]
				}
				newCentroid := calculateCentroid(vectors)
				
				// Check if centroid changed significantly
				diff := cosineSimilarity(cluster.Centroid, newCentroid)
				if diff < 0.99 {
					changed = true
				}
				cluster.Centroid = newCentroid
			}
		}
		
		if !changed {
			fmt.Printf("   Converged after %d iterations\n", iter+1)
			break
		}
	}
	
	// Assign cluster labels based on common themes
	clusterLabels := []string{
		"Technology & AI",
		"Science & Research",
		"Business & Finance",
		"Health & Wellness",
		"Education & Learning",
	}
	
	for i, cluster := range clusters {
		if i < len(clusterLabels) {
			cluster.Label = clusterLabels[i]
		} else {
			cluster.Label = fmt.Sprintf("Cluster %d", i)
		}
	}
	
	// Display clustering results
	fmt.Println("\n3. Clustering Results")
	for _, cluster := range clusters {
		fmt.Printf("\n   %s (%d documents):\n", cluster.Label, len(cluster.Members))
		
		// Sort members by title for display
		memberTitles := make([]string, len(cluster.Members))
		for i, memberID := range cluster.Members {
			memberTitles[i] = docTitles[memberID]
		}
		sort.Strings(memberTitles)
		
		for _, title := range memberTitles {
			fmt.Printf("      • %s\n", title)
		}
	}

	// Step 3: Find similar documents within clusters
	fmt.Println("\n4. Intra-Cluster Similarity Analysis")
	
	for _, cluster := range clusters {
		if len(cluster.Members) < 2 {
			continue
		}
		
		fmt.Printf("\n   %s:\n", cluster.Label)
		
		// Find most similar pair within cluster
		maxSim := float32(0)
		var doc1, doc2 string
		
		for i := 0; i < len(cluster.Members)-1; i++ {
			for j := i + 1; j < len(cluster.Members); j++ {
				sim := cosineSimilarity(
					docVectors[cluster.Members[i]],
					docVectors[cluster.Members[j]],
				)
				if sim > maxSim {
					maxSim = sim
					doc1 = docTitles[cluster.Members[i]]
					doc2 = docTitles[cluster.Members[j]]
				}
			}
		}
		
		fmt.Printf("   Most similar pair (similarity: %.3f):\n", maxSim)
		fmt.Printf("      • %s\n", doc1)
		fmt.Printf("      • %s\n", doc2)
	}

	// Step 4: Find outliers
	fmt.Println("\n5. Outlier Detection")
	fmt.Println("   Documents furthest from their cluster centroid:")
	
	type Outlier struct {
		Title    string
		Cluster  string
		Distance float32
	}
	
	outliers := []Outlier{}
	
	for _, cluster := range clusters {
		for _, memberID := range cluster.Members {
			distance := 1.0 - cosineSimilarity(docVectors[memberID], cluster.Centroid)
			outliers = append(outliers, Outlier{
				Title:    docTitles[memberID],
				Cluster:  cluster.Label,
				Distance: distance,
			})
		}
	}
	
	// Sort by distance (descending)
	sort.Slice(outliers, func(i, j int) bool {
		return outliers[i].Distance > outliers[j].Distance
	})
	
	// Show top 5 outliers
	for i := 0; i < 5 && i < len(outliers); i++ {
		fmt.Printf("   %d. %s (Cluster: %s, Distance: %.3f)\n",
			i+1, outliers[i].Title, outliers[i].Cluster, outliers[i].Distance)
	}

	// Step 5: Cross-cluster similarity
	fmt.Println("\n6. Cross-Cluster Analysis")
	fmt.Println("   Finding documents that could belong to multiple clusters:")
	
	for docID, vector := range docVectors {
		similarities := make([]float32, len(clusters))
		for i, cluster := range clusters {
			similarities[i] = cosineSimilarity(vector, cluster.Centroid)
		}
		
		// Find top 2 similarities
		first, second := -1, -1
		for i := range similarities {
			if first == -1 || similarities[i] > similarities[first] {
				second = first
				first = i
			} else if second == -1 || similarities[i] > similarities[second] {
				second = i
			}
		}
		
		// If document has high similarity to multiple clusters
		if first != -1 && second != -1 && similarities[second] > 0.7 {
			ratio := similarities[second] / similarities[first]
			if ratio > 0.85 { // Document is similar to multiple clusters
				fmt.Printf("   • %s\n", docTitles[docID])
				fmt.Printf("     Primary: %s (%.3f), Secondary: %s (%.3f)\n",
					clusters[first].Label, similarities[first],
					clusters[second].Label, similarities[second])
			}
		}
	}

	// Step 6: Save cluster centroids for future classification
	fmt.Println("\n7. Saving Cluster Models")
	
	// Create a collection for cluster centroids
	centroidCollection := "cluster_centroids"
	_, err = vectorStore.CreateCollection(ctx, centroidCollection, 128)
	if err != nil {
		fmt.Printf("   Note: Collection might already exist: %v\n", err)
	}
	
	for _, cluster := range clusters {
		emb := &core.Embedding{
			ID:         cluster.ID,
			Collection: centroidCollection,
			Vector:     cluster.Centroid,
			Content:    cluster.Label,
		}
		
		err := vectorStore.Upsert(ctx, emb)
		if err != nil {
			log.Printf("Failed to save centroid: %v", err)
		} else {
			fmt.Printf("   ✓ Saved centroid for %s\n", cluster.Label)
		}
	}

	// Step 7: Classify new document
	fmt.Println("\n8. Classifying New Documents")
	newDocs := []string{
		"Blockchain technology and cryptocurrency mining",
		"Genetic engineering and CRISPR applications",
		"Investment strategies and portfolio management",
	}
	
	for _, content := range newDocs {
		fmt.Printf("\n   New document: \"%s\"\n", content)
		
		// Generate embedding for new document
		embedding := generateEmbedding(content, rand.Intn(5), 0.3)
		
		// Find nearest cluster centroid
		results, err := vectorStore.Search(ctx, embedding, core.SearchOptions{
			Collection: centroidCollection,
			TopK:       1,
		})
		
		if err != nil || len(results) == 0 {
			fmt.Println("   Classification failed")
			continue
		}
		
		fmt.Printf("   Classified as: %s (Confidence: %.3f)\n",
			results[0].Content, results[0].Score)
	}

	fmt.Println("\n✨ Document Clustering Example Complete!")
	fmt.Println("This example demonstrated:")
	fmt.Println("  • K-means clustering with vector embeddings")
	fmt.Println("  • Intra-cluster similarity analysis")
	fmt.Println("  • Outlier detection")
	fmt.Println("  • Cross-cluster analysis")
	fmt.Println("  • Saving and using cluster models for classification")
}

// cosineSimilarity calculates cosine similarity between two vectors
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