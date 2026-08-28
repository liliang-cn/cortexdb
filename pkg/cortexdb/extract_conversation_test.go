package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

func TestExtractConversation(t *testing.T) {
	dbPath := fmt.Sprintf("test_extract_conv_%d.db", testname.Nano())
	db, err := Open(DefaultConfig(dbPath))
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
	tb := db.GraphRAGTools()

	text := "Alice deployed Apollo to Kubernetes. Apollo uses Postgres and Redis. Bob reviewed the Apollo rollout."

	// From text, with persistence.
	resp, err := tb.ExtractConversation(ctx, ToolExtractConversationRequest{Text: text, Persist: true})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if resp.Summary == "" {
		t.Errorf("expected a summary")
	}
	if !containsFold(resp.Entities, "Apollo") || !containsFold(resp.Entities, "Kubernetes") {
		t.Errorf("expected Apollo + Kubernetes entities, got %v", resp.Entities)
	}
	if len(resp.Relations) == 0 {
		t.Errorf("expected co-occurrence relations, got none")
	}
	if !resp.Persisted || resp.KnowledgeID == "" {
		t.Errorf("expected persisted with a knowledge id, got %+v", resp)
	}
	// Persisted entity must be in the graph.
	if node, _ := db.Graph().GetNode(ctx, "entity:apollo"); node == nil {
		t.Errorf("expected entity:apollo in graph after persist")
	}

	// Reachable through the generic toolbox Call (the path both MCP and gRPC use).
	raw, err := tb.Call(ctx, "extract_conversation", json.RawMessage(`{"text":"Zeta integrates with Stripe."}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	got, ok := raw.(*ToolExtractConversationResponse)
	if !ok || !containsFold(got.Entities, "Stripe") {
		t.Errorf("Call did not extract via dispatch: %+v", raw)
	}

	// Listed in Definitions (so ListTools / gRPC list exposes it).
	found := false
	for _, d := range tb.Definitions() {
		if d.Name == "extract_conversation" {
			found = true
		}
	}
	if !found {
		t.Errorf("extract_conversation missing from tool definitions")
	}
}

func containsFold(xs []string, want string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, want) {
			return true
		}
	}
	return false
}
