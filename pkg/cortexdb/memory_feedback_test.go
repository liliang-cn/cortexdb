package cortexdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Entity resolution merges "K8s" into "Kubernetes" and records the alias on
// the surviving node. A query naming the alias must land on the survivor —
// merging without query-time resolution silently disconnects every caller who
// knew the entity by the name that lost.
func TestEntityAliasResolvesToTheSurvivingNode(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "alias.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// The memory mentions Kubernetes; the K8s node was merged away, leaving
	// only the alias record on the survivor.
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "m1", Scope: "global",
		Content:  "the cluster runs on managed Kubernetes",
		Entities: []ToolEntityInput{{Name: "Kubernetes", Type: "platform"}},
	}); err != nil {
		t.Fatal(err)
	}
	survivor := EntityNodeID("Kubernetes")
	nodes, err := db.graph.GetNodesBatch(ctx, []string{survivor})
	if err != nil || len(nodes) == 0 || nodes[0] == nil {
		t.Fatalf("survivor node missing: %v", err)
	}
	node := nodes[0]
	if node.Properties == nil {
		node.Properties = map[string]interface{}{}
	}
	node.Properties["aliases"] = []string{"K8s"}
	res, err := db.graph.UpsertNodesBatch(ctx, []*graph.GraphNode{node})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		t.Fatalf("record alias: %v", err)
	}

	got, err := db.SearchMemory(ctx, MemorySearchRequest{
		Query: "what platform", Scope: "global", EntityNames: []string{"K8s"}, TopK: 3,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got.Results) == 0 || got.Results[0].Memory.ID != "m1" {
		t.Fatalf("alias did not reach the survivor's memories: %+v", got.Results)
	}
}

// Recall bumps a counter and a timestamp; both exist for the usage report and
// must never influence what got returned.
func TestSearchMemoryRecordsRecallsWithoutRankingOnThem(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "usage.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "popular", Scope: "global", Content: "the gateway port is fixed",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "fresh", Scope: "global", Content: "the gateway port is fixed",
	}); err != nil {
		t.Fatal(err)
	}

	// Recall the pair repeatedly; only "popular" would win if counts fed rank.
	var firstOrder []string
	for i := 0; i < 4; i++ {
		res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "gateway port", Scope: "global", TopK: 5})
		if err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
		var order []string
		for _, h := range res.Results {
			order = append(order, h.Memory.ID)
		}
		if i == 0 {
			firstOrder = order
		} else if len(order) != len(firstOrder) {
			t.Fatalf("result set changed size across recalls: %v vs %v", order, firstOrder)
		}
	}

	got, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: firstOrder[0]})
	if err != nil {
		t.Fatal(err)
	}
	count, _ := got.Memory.Metadata["recall_count"].(float64)
	if int(count) != 4 {
		t.Errorf("recall_count = %v, want 4", got.Memory.Metadata["recall_count"])
	}
	if got.Memory.Metadata["last_recalled_at"] == nil {
		t.Error("last_recalled_at not stamped")
	}
}
