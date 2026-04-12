package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"
)

type planner struct{}

func (planner) Plan(_ context.Context, query string, _ memoryflow.SessionState) (*cortexdb.RetrievalPlan, error) {
	return &cortexdb.RetrievalPlan{
		Query:         query,
		Keywords:      []string{"Apollo", "Friday", "brief"},
		RetrievalMode: cortexdb.RetrievalModeLexical,
	}, nil
}

func main() {
	dbPath := "example_memoryflow.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	flow, err := memoryflow.New(db, planner{}, nil, memoryflow.WithConventions(memoryflow.DefaultConventionSet("apollo")))
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	transcript := memoryflow.Transcript{
		SessionID: "session-1",
		UserID:    "user-1",
		Source:    "chat",
		Title:     "Apollo planning",
		Turns: []memoryflow.TranscriptTurn{
			{Role: "user", Content: "The Apollo deadline is Friday."},
			{Role: "assistant", Content: "Captured."},
			{Role: "user", Content: "I prefer brief status updates."},
			{Role: "assistant", Content: "Stored."},
		},
	}

	ingest, err := flow.IngestTranscript(ctx, memoryflow.IngestTranscriptRequest{
		Transcript: transcript,
		Scope:      cortexdb.MemoryScopeSession,
		Namespace:  "assistant",
		Taxonomy: memoryflow.Taxonomy{
			Wing: "projects",
			Room: "apollo",
			Tags: []string{"planning"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("stored episodes=%d\n", ingest.Count)

	layers, err := flow.WakeUpLayers(ctx, memoryflow.WakeUpLayersRequest{
		Identity: "You are the Apollo project assistant.",
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

	if _, err := flow.AppendDiaryEntry(ctx, memoryflow.DiaryEntryRequest{
		UserID:    "user-1",
		SessionID: "session-1",
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "assistant",
		Content:   "Apollo memory was initialized from planning chat.",
	}); err != nil {
		log.Fatal(err)
	}

	reconstructed, err := flow.GetTranscript(ctx, memoryflow.GetTranscriptRequest{
		SessionID: "session-1",
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "assistant",
		Limit:     10,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nreconstructed turns=%d\n", len(reconstructed.Transcript.Turns))
}
