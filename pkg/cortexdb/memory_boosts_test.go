package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Ranking used to be BM25 alone: a memory saved as important and a passing
// aside tied whenever their term frequencies did, and which one surfaced was
// luck. Importance and age now weigh in, the same rule agentmem already uses.
func TestSearchMemoryRanksImportantRecentAboveAsides(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "boosts.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Same wording, so lexical relevance is identical; only the boosts differ.
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "aside", Scope: "global",
		Content: "the gateway deployment finished",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "decision", Scope: "global",
		Content: "the gateway deployment finished", Importance: 0.95,
	}); err != nil {
		t.Fatal(err)
	}
	// Age the aside without touching the decision.
	if _, err := db.store.GetDB().ExecContext(ctx,
		`UPDATE messages SET created_at = datetime('now', '-120 days') WHERE id = 'aside'`); err != nil {
		t.Fatal(err)
	}

	res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "gateway deployment", Scope: "global", TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Results) < 2 {
		t.Fatalf("expected both memories, got %d", len(res.Results))
	}
	if res.Results[0].Memory.ID != "decision" {
		t.Errorf("important recent memory ranked below an old aside: %v then %v",
			res.Results[0].Memory.ID, res.Results[1].Memory.ID)
	}
	if res.Results[0].Score <= res.Results[1].Score {
		t.Errorf("scores do not reflect the boosts: %v <= %v", res.Results[0].Score, res.Results[1].Score)
	}
}

// Superseding is how a correction retires the fact it corrects. The old memory
// stays stored — exports show it, and its metadata names the replacement — but
// recall must stop presenting it as current.
func TestSupersededMemoryLeavesRecallButNotTheStore(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "supersede.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "primary-old", Scope: "global",
		Content: "the openclaw resource runs Primary on node-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "primary-new", Scope: "global",
		Content:    "the openclaw resource runs Primary on node-b since the failover",
		Supersedes: []string{"primary-old"},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "openclaw Primary node", Scope: "global", TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, hit := range res.Results {
		if hit.Memory.ID == "primary-old" {
			t.Errorf("superseded memory still answers recall: %v", hit.Memory.ID)
		}
	}
	found := false
	for _, hit := range res.Results {
		if hit.Memory.ID == "primary-new" {
			found = true
		}
	}
	if !found {
		t.Error("the superseding memory did not surface")
	}

	// Still stored, still exported, and it names its replacement.
	got, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "primary-old"})
	if err != nil {
		t.Fatalf("superseded memory should remain readable by id: %v", err)
	}
	if by, _ := got.Memory.Metadata["superseded_by"].(string); by != "primary-new" {
		t.Errorf("superseded_by = %q, want %q", by, "primary-new")
	}
	all, err := db.ListAllMemories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, m := range all {
		if m.ID == "primary-old" {
			seen = true
		}
	}
	if !seen {
		t.Error("superseded memory vanished from the export listing")
	}
}

// Superseding an id that does not exist is a caller mistake worth hearing
// about: succeeding silently would leave them sure the old fact was retired.
func TestSupersedingAMissingMemoryFails(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "missing.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	_, err = db.SaveMemory(context.Background(), MemorySaveRequest{
		MemoryID: "corrector", Scope: "global",
		Content:    "a correction aimed at nothing",
		Supersedes: []string{"never-existed"},
	})
	if err == nil {
		t.Fatal("expected an error for superseding a missing id")
	}
	if !strings.Contains(err.Error(), "never-existed") {
		t.Errorf("error should name the missing id: %v", err)
	}
}
