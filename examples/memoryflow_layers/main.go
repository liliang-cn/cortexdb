package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"
)

func main() {
	dbPath := "memoryflow_layers.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	flow, err := memoryflow.New(db, nil, nil)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	if _, err := flow.IngestTranscript(ctx, memoryflow.IngestTranscriptRequest{
		Transcript: memoryflow.Transcript{
			SessionID: "session-1",
			UserID:    "user-1",
			Source:    "claude",
			Title:     "Planning",
			Turns: []memoryflow.TranscriptTurn{
				{Role: "user", Content: "We decided to ship on Friday and I prefer brief reports."},
				{Role: "assistant", Content: "Captured."},
			},
		},
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "assistant",
		Taxonomy: memoryflow.Taxonomy{
			Wing: "projects",
			Room: "planning",
		},
	}); err != nil {
		log.Fatal(err)
	}

	layers, err := flow.WakeUpLayers(ctx, memoryflow.WakeUpLayersRequest{
		Identity: "You are the project memory assistant.",
		Recall: memoryflow.RecallRequest{
			Query:            "startup context",
			UserID:           "user-1",
			SessionID:        "session-1",
			Scope:            cortexdb.MemoryScopeSession,
			Namespace:        "assistant",
			DisableKnowledge: true,
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, layer := range layers.Layers {
		fmt.Printf("\n[%s] %s\n%s\n", layer.Level, layer.Title, layer.Text)
	}
}
