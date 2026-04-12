package hindsight

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"
)

func TestStrategyWrapsMemoryflowRecall(t *testing.T) {
	dbPath := fmt.Sprintf("hindsight_strategy_%s.db", t.Name())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	strategy := NewStrategy(db, StrategyOptions{
		BankID:      "Agent 1",
		EntityNames: []string{"Apollo"},
		Keywords:    []string{"deadline"},
		UseKG:       true,
	})
	svc, err := memoryflow.New(db, nil, nil, memoryflow.WithRecallStrategy(strategy))
	if err != nil {
		t.Fatalf("new memoryflow: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.IngestTranscript(ctx, memoryflow.IngestTranscriptRequest{
		Transcript: memoryflow.Transcript{
			SessionID: "session-1",
			UserID:    "user-1",
			Turns: []memoryflow.TranscriptTurn{
				{Role: "user", Content: "Apollo deadline is Friday."},
				{Role: "assistant", Content: "Captured."},
			},
		},
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "hindsight:agent-1",
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	resp, err := svc.Recall(ctx, memoryflow.RecallRequest{
		Query:            "deadline",
		UserID:           "user-1",
		SessionID:        "session-1",
		Scope:            cortexdb.MemoryScopeSession,
		DisableKnowledge: true,
		State: memoryflow.SessionState{
			Taxonomy: memoryflow.Taxonomy{Entities: []string{"Apollo"}},
		},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if resp.Plan.RetrievalMode != cortexdb.RetrievalModeGraph {
		t.Fatalf("expected graph retrieval mode from hindsight strategy, got %+v", resp.Plan)
	}
	if len(resp.Plan.EntityNames) == 0 || resp.Plan.EntityNames[0] != "Apollo" {
		t.Fatalf("expected Apollo entity cue, got %+v", resp.Plan)
	}
	if resp.Response.MemoryPlan.Filters == nil || resp.Response.MemoryPlan.Filters.Namespace != "hindsight:agent-1" {
		t.Fatalf("expected sanitized hindsight namespace filter, got %+v", resp.Response.MemoryPlan)
	}
	if len(resp.Response.Memories) == 0 {
		t.Fatalf("expected memory recall result, got %+v", resp)
	}
}

func BenchmarkStrategyEnrichRecallRequest(b *testing.B) {
	strategy := NewStrategy(nil, StrategyOptions{
		BankID:      "Agent 1",
		EntityNames: []string{"Apollo", "Friday"},
		Keywords:    []string{"deadline", "preference"},
		UseKG:       true,
	})
	req := memoryflow.RecallRequest{
		Query: "apollo deadline",
		State: memoryflow.SessionState{
			Wing: "projects",
			Room: "apollo",
			Tags: []string{"planning"},
			Taxonomy: memoryflow.Taxonomy{
				Entities: []string{"Apollo"},
				Tags:     []string{"launch"},
			},
		},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out := strategy.enrichRecallRequest(req)
		if out.Plan == nil || out.Plan.RetrievalMode != cortexdb.RetrievalModeGraph {
			b.Fatalf("unexpected enriched request: %+v", out)
		}
	}
}
