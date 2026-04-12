package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	dbPath := "example_rag.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	_, err = db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
		KnowledgeID: "apollo-plan",
		Title:       "Apollo launch plan",
		Content:     "Alice owns the Apollo launch plan. Apollo ships on Friday. Bob handles release notes.",
		ChunkSize:   24,
		Entities: []cortexdb.ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{"chunk:apollo-plan:000"}},
			{Name: "Apollo", Type: "project", ChunkIDs: []string{"chunk:apollo-plan:000"}},
			{Name: "Bob", Type: "person", ChunkIDs: []string{"chunk:apollo-plan:000"}},
		},
		Relations: []cortexdb.ToolRelationInput{
			{From: "Alice", To: "Apollo", Type: "owns"},
			{From: "Bob", To: "Apollo", Type: "documents"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	resp, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
		Query:         "Who owns Apollo and when does it ship?",
		Keywords:      []string{"Apollo", "Alice", "Friday", "launch"},
		EntityNames:   []string{"Apollo", "Alice"},
		RetrievalMode: cortexdb.RetrievalModeLexical,
		TopK:          3,
		GraphLight:    true,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("knowledge hits:")
	for _, hit := range resp.Results {
		fmt.Printf("- %s score=%.3f entities=%v\n", hit.Title, hit.Score, hit.Entities)
	}
	fmt.Printf("\ncontext:\n%s\n", resp.Context)
}
