package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/sqvect"
)

func testSimilarity(name string, fn sqvect.SimilarityFunc) {
	// Create a temporary test database
	dbPath := fmt.Sprintf("test_%s.db", name)
	defer os.Remove(dbPath)

	// Create store with specified similarity
	config := sqvect.DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 3
	config.SimilarityFn = fn
	
	store, err := sqvect.NewWithConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	
	// Initialize the store
	if err := store.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// Test vectors
	vectors := []struct {
		id     string
		vector []float32
	}{
		{"exact", []float32{1.0, 2.0, 3.0}},
		{"normalized", []float32{0.267261, 0.534522, 0.801784}}, // normalized version of [1,2,3]
	}

	// Insert vectors
	for _, v := range vectors {
		err := store.Upsert(ctx, &sqvect.Embedding{
			ID:       v.id,
			Vector:   v.vector,
			Metadata: map[string]string{},
		})
		if err != nil {
			log.Printf("Failed to insert %s: %v", v.id, err)
		}
	}

	// Search with the query vector
	query := []float32{1.0, 2.0, 3.0}
	results, err := store.Search(ctx, query, sqvect.SearchOptions{TopK: 10})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n%s Similarity Results:\n", name)
	fmt.Println("==========================================")
	for i, result := range results {
		fmt.Printf("%d. ID: %-12s Score: %+.6f\n", i+1, result.ID, result.Score)
	}
}

func main() {
	fmt.Println("Testing Vector Similarity Functions")
	fmt.Println("Query vector: [1.0, 2.0, 3.0]")
	
	testSimilarity("Cosine", sqvect.GetCosineSimilarity())
	testSimilarity("DotProduct", sqvect.GetDotProduct())
	testSimilarity("Euclidean", sqvect.GetEuclideanDist())
	
	fmt.Println("\nNotes:")
	fmt.Println("- Cosine: 1.0 = identical direction, 0 = orthogonal, -1 = opposite")
	fmt.Println("- DotProduct: Higher = more similar (unbounded)")
	fmt.Println("- Euclidean: 0 = identical, negative values (closer to 0 = more similar)")
}