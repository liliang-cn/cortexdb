package graphflow

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// routingFakeLLM answers based on which system prompt it is handed, so one fake
// can serve the community-summary, map, and reduce steps.
type routingFakeLLM struct {
	summaryCalls, mapCalls, reduceCalls int
}

func (f *routingFakeLLM) GenerateJSON(_ context.Context, system, _ string) ([]byte, error) {
	switch {
	case strings.Contains(system, "community of related entities"):
		f.summaryCalls++
		return []byte(`{"title":"Payments","summary":"Stripe and PayPal handle checkout.","findings":["Stripe is primary"]}`), nil
	case strings.Contains(system, "extract, from community reports"):
		f.mapCalls++
		return []byte(`{"points":[{"point":"Stripe is the primary payments provider","score":90}]}`), nil
	case strings.Contains(system, "answer the user's question"):
		f.reduceCalls++
		return []byte(`{"answer":"The corpus is mainly about payments, led by Stripe."}`), nil
	default:
		return []byte(`{}`), nil
	}
}

// TestCommunitySummariesAndGlobalSearch builds a small entity graph with a
// clear community, summarizes it, and runs global search end-to-end against a
// routing fake LLM.
func TestCommunitySummariesAndGlobalSearch(t *testing.T) {
	dbPath := fmt.Sprintf("test_community_%d.db", testname.Nano())
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	ctx := context.Background()

	// A connected community of ≥3 entities.
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		{Name: "Stripe", Type: "tool"},
		{Name: "PayPal", Type: "tool"},
		{Name: "Checkout", Type: "concept"},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: []cortexdb.ToolRelationInput{
		{From: "Checkout", To: "Stripe", Type: "uses"},
		{From: "Checkout", To: "PayPal", Type: "uses"},
		{From: "Stripe", To: "PayPal", Type: "related_to"},
	}}); err != nil {
		t.Fatalf("upsert relations: %v", err)
	}

	llm := &routingFakeLLM{}

	report, err := BuildCommunitySummaries(ctx, db, CommunityOptions{LLM: llm, MinSize: 2})
	if err != nil {
		t.Fatalf("build communities: %v", err)
	}
	if len(report.Communities) == 0 {
		t.Fatalf("expected at least one community summary")
	}
	if report.Communities[0].Title != "Payments" {
		t.Fatalf("expected Payments title, got %q", report.Communities[0].Title)
	}

	// Persisted as a knowledge document, reusable by GlobalSearch.
	res, err := GlobalSearch(ctx, db, "What is this corpus about?", GlobalSearchOptions{LLM: llm})
	if err != nil {
		t.Fatalf("global search: %v", err)
	}
	if !strings.Contains(res.Answer, "payments") {
		t.Fatalf("expected an answer mentioning payments, got %q", res.Answer)
	}
	if llm.mapCalls == 0 || llm.reduceCalls == 0 {
		t.Fatalf("expected map and reduce to run, got map=%d reduce=%d", llm.mapCalls, llm.reduceCalls)
	}
}

// TestGlobalSearchWithoutCommunitiesErrors verifies the guard when no summaries
// exist and BuildIfEmpty is not set.
func TestGlobalSearchWithoutCommunitiesErrors(t *testing.T) {
	dbPath := fmt.Sprintf("test_community_empty_%d.db", testname.Nano())
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	if _, err := GlobalSearch(context.Background(), db, "q", GlobalSearchOptions{LLM: &routingFakeLLM{}}); err == nil {
		t.Fatalf("expected error when no communities are built")
	}
}
