package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestQueryAPIFusesPrefetchesAndAppliesFormula(t *testing.T) {
	dbPath := fmt.Sprintf("test_query_api_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.InsertTextWithVector(ctx, "alpha", "apollo launch checklist", []float32{1, 0, 0}, map[string]string{
		"category":   "ops",
		"importance": "2",
	}); err != nil {
		t.Fatalf("insert alpha: %v", err)
	}
	if err := db.InsertTextWithVector(ctx, "beta", "apollo launch telemetry", []float32{0.95, 0.05, 0}, map[string]string{
		"category":   "ops",
		"importance": "10",
	}); err != nil {
		t.Fatalf("insert beta: %v", err)
	}
	if err := db.InsertTextWithVector(ctx, "gamma", "garden planning notes", []float32{0, 1, 0}, map[string]string{
		"category":   "personal",
		"importance": "50",
	}); err != nil {
		t.Fatalf("insert gamma: %v", err)
	}

	resp, err := db.Query(ctx, QueryRequest{
		Query:       "apollo launch",
		QueryVector: []float32{1, 0, 0},
		Fusion:      QueryFusionWeightedRRF,
		Limit:       2,
		IncludeRaw:  true,
		Prefetch: []QueryPrefetch{
			{Name: "dense", Kind: QueryPrefetchVector, Weight: 1, Limit: 5},
			{Name: "lexical", Kind: QueryPrefetchLexical, Weight: 1, Limit: 5},
		},
		Filter: &QueryFilter{
			Must: []QueryCondition{{Field: "category", Op: QueryFilterEqual, Value: "ops"}},
		},
		Formula: &QueryScoreFormula{
			NumericBoosts: []QueryNumericBoost{{Field: "importance", MaxValue: 10, Weight: 0.2}},
		},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].ID != "beta" {
		t.Fatalf("expected formula-boosted beta first, got %+v", resp.Results)
	}
	if resp.Results[0].SourceRanks["dense"] == 0 || resp.Results[0].SourceRanks["lexical"] == 0 {
		t.Fatalf("expected raw source ranks, got %+v", resp.Results[0])
	}
	for _, result := range resp.Results {
		if result.Metadata["category"] != "ops" {
			t.Fatalf("filter leaked non-ops result: %+v", result)
		}
	}
}

func TestQueryAPIDBSFAndMustNotFilter(t *testing.T) {
	dbPath := fmt.Sprintf("test_query_api_dbsf_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	fixtures := []struct {
		id       string
		content  string
		vector   []float32
		metadata map[string]string
	}{
		{"doc1", "incident response api latency", []float32{1, 0, 0}, map[string]string{"status": "active"}},
		{"doc2", "incident response database latency", []float32{0.9, 0.1, 0}, map[string]string{"status": "archived"}},
		{"doc3", "design notes", []float32{0, 1, 0}, map[string]string{"status": "active"}},
	}
	for _, fixture := range fixtures {
		if err := db.InsertTextWithVector(ctx, fixture.id, fixture.content, fixture.vector, fixture.metadata); err != nil {
			t.Fatalf("insert %s: %v", fixture.id, err)
		}
	}

	resp, err := db.Query(ctx, QueryRequest{
		Query:       "incident latency",
		QueryVector: []float32{1, 0, 0},
		Fusion:      QueryFusionDBSF,
		Limit:       3,
		Filter: &QueryFilter{
			MustNot: []QueryCondition{{Field: "status", Op: QueryFilterEqual, Value: "archived"}},
		},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected results")
	}
	for _, result := range resp.Results {
		if result.ID == "doc2" {
			t.Fatalf("must_not filter leaked archived result: %+v", resp.Results)
		}
	}
}

func TestQueryAPIGraphPrefetchFindsEntityLinkedChunks(t *testing.T) {
	dbPath := fmt.Sprintf("test_query_api_graph_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	tools := db.GraphRAGTools()
	ingestResp, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "doc-graph-prefetch",
		Title:      "Mission Notes",
		Content:    "The launch review packet is ready for the Friday readiness meeting.",
		ChunkSize:  16,
		Metadata:   map[string]string{"category": "ops"},
	})
	if err != nil {
		t.Fatalf("ingest document: %v", err)
	}
	if len(ingestResp.ChunkNodeIDs) == 0 {
		t.Fatal("expected ingested chunks")
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "doc-graph-prefetch",
		Entities: []ToolEntityInput{
			{Name: "Apollo", Type: "project", ChunkIDs: ingestResp.ChunkNodeIDs},
		},
	}); err != nil {
		t.Fatalf("upsert entity: %v", err)
	}

	resp, err := db.Query(ctx, QueryRequest{
		Fusion:     QueryFusionWeightedRRF,
		Limit:      3,
		IncludeRaw: true,
		Prefetch: []QueryPrefetch{
			{Name: "graph", Kind: QueryPrefetchGraph, EntityNames: []string{"Apollo"}, MaxHops: 1, Limit: 3},
		},
		Filter: &QueryFilter{
			Must: []QueryCondition{{Field: "category", Op: QueryFilterEqual, Value: "ops"}},
		},
	})
	if err != nil {
		t.Fatalf("query graph prefetch: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected graph prefetch results")
	}
	if resp.Results[0].ID != ingestResp.ChunkNodeIDs[0] {
		t.Fatalf("expected graph-linked chunk %q, got %+v", ingestResp.ChunkNodeIDs[0], resp.Results)
	}
	if resp.Results[0].SourceRanks["graph"] == 0 {
		t.Fatalf("expected graph source rank, got %+v", resp.Results[0])
	}

	defaultResp, err := db.Query(ctx, QueryRequest{
		EntityNames: []string{"Apollo"},
		Limit:       3,
		IncludeRaw:  true,
	})
	if err != nil {
		t.Fatalf("query default graph prefetch: %v", err)
	}
	if len(defaultResp.Results) == 0 {
		t.Fatal("expected default entity_names graph results")
	}
	if defaultResp.Results[0].SourceRanks["graph"] == 0 {
		t.Fatalf("expected default query to include graph source rank, got %+v", defaultResp.Results[0])
	}
}
