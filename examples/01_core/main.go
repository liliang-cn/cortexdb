package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	dbPath := "example_core.db"
	defer func() { _ = os.Remove(dbPath) }()

	cfg := cortexdb.DefaultConfig(dbPath)
	cfg.Dimensions = 8

	db, err := cortexdb.Open(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	quick := db.Quick()

	items := []struct {
		id      string
		content string
		seed    int
	}{
		{id: "go", content: "Go is useful for embedded systems and servers.", seed: 1},
		{id: "sqlite", content: "SQLite stores application data in a single file.", seed: 2},
		{id: "rag", content: "RAG retrieves context before generation.", seed: 3},
	}

	for _, item := range items {
		id, err := quick.Add(ctx, sampleVector(8, item.seed), item.content)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("stored %s as %s\n", item.id, id)
	}

	results, err := quick.Search(ctx, sampleVector(8, 2), 2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nnearest results:")
	for _, result := range results {
		fmt.Printf("- %.3f %s\n", result.Score, result.Content)
	}

	if _, err := db.Vector().CreateCollection(ctx, "notes", 8); err != nil {
		log.Printf("create collection: %v", err)
	}
	id, err := quick.AddToCollection(ctx, "notes", sampleVector(8, 4), "Collection-scoped note.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\ncollection item: %s\n", id)
}

func sampleVector(dim int, seed int) []float32 {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32((seed+i*7)%17 + 1)
	}
	var sum float64
	for _, value := range vec {
		sum += float64(value * value)
	}
	norm := float32(math.Sqrt(sum))
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}
