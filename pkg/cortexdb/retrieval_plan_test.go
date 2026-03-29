package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestResolveRetrievalPlanMergesFieldsAndDecisions(t *testing.T) {
	resolution := resolveRetrievalPlan(retrievalPlanInput{
		Query: "Find Alice's employer",
		Plan: &RetrievalPlan{
			Query:            "plan query",
			Keywords:         []string{"alice", "acme"},
			AlternateQueries: []string{"Alice works at Acme"},
			EntityNames:      []string{"Acme"},
			RetrievalMode:    RetrievalModeLexical,
			Filters: &RetrievalFilters{
				Collection:  "plan-collection",
				DocumentIDs: []string{"doc-plan"},
				Namespace:   "assistant",
			},
		},
		Keywords:         []string{"employer", "alice"},
		AlternateQueries: []string{"Alice employer"},
		EntityNames:      []string{"Alice"},
		RetrievalMode:    RetrievalModeGraph,
		Filters: &RetrievalFilters{
			Collection:  "legacy-collection",
			DocumentIDs: []string{"doc-legacy"},
			UserID:      "user-1",
		},
		SupportsGraph: true,
	})

	if resolution.Plan.Query != "Find Alice's employer" {
		t.Fatalf("unexpected resolved query: %q", resolution.Plan.Query)
	}
	if got, want := resolution.Plan.Keywords, []string{"employer", "alice", "acme"}; len(got) != len(want) {
		t.Fatalf("unexpected keyword count: got %v want %v", got, want)
	}
	if resolution.Plan.RetrievalMode != RetrievalModeGraph {
		t.Fatalf("unexpected requested mode: %s", resolution.Plan.RetrievalMode)
	}
	if resolution.Plan.Filters == nil {
		t.Fatal("expected merged filters")
	}
	if resolution.Plan.Filters.Collection != "legacy-collection" {
		t.Fatalf("unexpected collection filter: %s", resolution.Plan.Filters.Collection)
	}
	if len(resolution.Plan.Filters.DocumentIDs) != 2 {
		t.Fatalf("expected merged document filters, got %+v", resolution.Plan.Filters.DocumentIDs)
	}
	if resolution.Decision.EffectiveMode != RetrievalModeGraph || !resolution.Decision.UseGraph {
		t.Fatalf("expected graph decision, got %+v", resolution.Decision)
	}

	memoryDecision := resolveRetrievalPlan(retrievalPlanInput{
		Query:                    "remember Alice",
		RetrievalMode:            RetrievalModeGraph,
		SupportsGraph:            false,
		UnsupportedEffectiveMode: RetrievalModeAuto,
		UnsupportedReason:        "memory search does not use graph expansion",
	})
	if memoryDecision.Decision.UseGraph {
		t.Fatalf("expected memory decision to disable graph, got %+v", memoryDecision.Decision)
	}
	if memoryDecision.Decision.EffectiveMode != RetrievalModeAuto {
		t.Fatalf("expected memory fallback to auto, got %+v", memoryDecision.Decision)
	}
}

func TestSearchTextPlanAppliesDocumentFilters(t *testing.T) {
	dbPath := fmt.Sprintf("test_retrieval_plan_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	tools := db.GraphRAGTools()
	ctx := context.Background()

	for _, doc := range []ToolIngestDocumentRequest{
		{
			DocumentID: "doc-a",
			Title:      "Alice at Acme",
			Content:    "Alice works at Acme.",
			ChunkSize:  16,
		},
		{
			DocumentID: "doc-b",
			Title:      "Alice at Beta Labs",
			Content:    "Alice works at Beta Labs.",
			ChunkSize:  16,
		},
	} {
		if _, err := tools.IngestDocument(ctx, doc); err != nil {
			t.Fatalf("ingest %s: %v", doc.DocumentID, err)
		}
	}

	resp, err := tools.SearchText(ctx, ToolSearchTextRequest{
		Query: "Find Alice's employer",
		TopK:  5,
		Plan: &RetrievalPlan{
			Keywords:         []string{"Alice", "employer", "works"},
			AlternateQueries: []string{"Alice works"},
			Filters: &RetrievalFilters{
				DocumentIDs: []string{"doc-b"},
			},
		},
	})
	if err != nil {
		t.Fatalf("search text with plan filters: %v", err)
	}
	if resp.Plan.Filters == nil || len(resp.Plan.Filters.DocumentIDs) != 1 {
		t.Fatalf("expected plan filters in response, got %+v", resp.Plan.Filters)
	}
	if len(resp.Chunks) == 0 {
		t.Fatal("expected filtered search results")
	}
	for _, chunk := range resp.Chunks {
		if chunk.DocumentID != "doc-b" {
			t.Fatalf("expected doc-b only, got %+v", chunk)
		}
	}
}
