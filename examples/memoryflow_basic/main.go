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

func (planner) Plan(_ context.Context, _ string, _ memoryflow.SessionState) (*cortexdb.RetrievalPlan, error) {
	return &cortexdb.RetrievalPlan{
		Keywords:      []string{"deadline", "apollo"},
		RetrievalMode: cortexdb.RetrievalModeLexical,
	}, nil
}

func main() {
	dbPath := "memoryflow_basic.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	flow, err := memoryflow.New(
		db,
		planner{},
		nil,
		memoryflow.WithConventions(memoryflow.DefaultConventionSet("apollo")),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	transcript := memoryflow.Transcript{
		SessionID: "apollo-session",
		UserID:    "user-1",
		Source:    "chat",
		Title:     "Apollo standup",
		Turns: []memoryflow.TranscriptTurn{
			{Role: "user", Content: "The Apollo deadline is Friday."},
			{Role: "assistant", Content: "Noted."},
			{Role: "user", Content: "I prefer concise status updates."},
			{Role: "assistant", Content: "Stored."},
		},
	}

	ingest, err := flow.IngestTranscript(ctx, memoryflow.IngestTranscriptRequest{
		Transcript: transcript,
		Scope:      cortexdb.MemoryScopeSession,
		Namespace:  "assistant",
		Taxonomy: memoryflow.Taxonomy{
			Room: "standup",
			Tags: []string{"meeting"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("stored exchanges=%d\n", ingest.Count)

	recall, err := flow.Recall(ctx, memoryflow.RecallRequest{
		Query:            "What is the Apollo deadline?",
		UserID:           "user-1",
		SessionID:        "apollo-session",
		Scope:            cortexdb.MemoryScopeSession,
		Namespace:        "assistant",
		DisableKnowledge: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nrecall context:")
	fmt.Println(recall.Response.ContextPack.Text)
}
