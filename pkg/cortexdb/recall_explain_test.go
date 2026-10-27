package cortexdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// A recall answered by keyword because the embedder could not be reached
// says so in its response. It used to say so only in the server's journal,
// and the caller got a keyword answer it could not tell from a semantic one.
func TestARecallWithTheEmbedderDownSaysItIsDegraded(t *testing.T) {
	emb := &flakyEmbedder{}
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "degraded.db")), WithEmbedder(emb))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m1", Scope: "global", Content: "the brain runs on haify with a VIP"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GraphRAGTools().IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "haify keeps the brain highly available behind a VIP."}); err != nil {
		t.Fatal(err)
	}

	up, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "haify VIP"})
	if err != nil {
		t.Fatal(err)
	}
	if len(up.Degraded) != 0 {
		t.Fatalf("a whole recall reported degraded: %v", up.Degraded)
	}

	emb.down.Store(true)
	// Another query: the one above is in the embedder's cache.
	down, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "VIP of haify"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(down.Degraded, []string{DegradedEmbedderUnreachable}) {
		t.Fatalf("degraded = %v", down.Degraded)
	}
	if down.MemoryDecision.EffectiveMode != RetrievalModeLexical || down.KnowledgeDecision.EffectiveMode != RetrievalModeLexical {
		t.Fatalf("decisions still claim a vector search: %+v / %+v", down.MemoryDecision, down.KnowledgeDecision)
	}
	if len(down.Memories) == 0 || len(down.Knowledge) == 0 {
		t.Fatalf("the keyword answer is gone: %d memories, %d knowledge", len(down.Memories), len(down.Knowledge))
	}
}

// A recall can be explained from its record alone: what it searched with,
// what each retriever put forward with its own score, what a floor dropped,
// and what came out.
func TestARecallExplainsItself(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "explain.db")), WithEmbedder(stubEmbedder{dim: 8}))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for id, content := range map[string]string{
		"m-vip":   "the shared brain answers on a VIP managed by haify",
		"m-other": "lunch was noodles",
	} {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Scope: "global", Content: content}); err != nil {
			t.Fatal(err)
		}
	}

	plain, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "haify VIP", DisableKnowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Explain != nil {
		t.Fatal("explain returned without being asked for")
	}

	resp, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{
		Query: "haify VIP", Keywords: []string{"haify"}, DisableKnowledge: true, Explain: true, Planner: "fallback: timeout",
	})
	if err != nil {
		t.Fatal(err)
	}
	ex := resp.Explain
	if ex == nil || ex.Memory == nil {
		t.Fatalf("no explain: %+v", ex)
	}
	if ex.Plan.Query != "haify VIP" || ex.Plan.Planner != "fallback: timeout" || !slices.Contains(ex.Plan.Keywords, "haify") {
		t.Fatalf("plan: %+v", ex.Plan)
	}
	if len(ex.Memory.Candidates[explainVector]) == 0 || len(ex.Memory.Candidates[explainKeyword]) == 0 {
		t.Fatalf("candidates: %+v", ex.Memory.Candidates)
	}
	if ex.Memory.EffectiveMode == "" {
		t.Fatalf("no decision: %+v", ex.Memory)
	}
	if len(ex.Final) == 0 || ex.Final[0].ID != "m-vip" || ex.Final[0].Kind != "memory" {
		t.Fatalf("final: %+v", ex.Final)
	}
	if b, err := json.Marshal(ex); err != nil || len(b) > 16<<10 {
		t.Fatalf("explain does not stay small: %d bytes, %v", len(b), err)
	}
}

// A recall inside a recorded call reports itself to the request log with
// its account, whether or not the caller asked for it in the response.
func TestARecordedRecallNotesItsAccount(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "note.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, call := observability.WithCall(context.Background(), "")
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m1", Scope: "global", Content: "noted recall about drbd"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "drbd"}); err != nil {
		t.Fatal(err)
	}
	note := call.Recall()
	if note == nil || note.Hits == 0 || note.Explain == nil {
		t.Fatalf("recall note: %+v", note)
	}
}
