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

func openMultiHopTestDB(t *testing.T) (*cortexdb.DB, context.Context) {
	t.Helper()
	dbPath := fmt.Sprintf("test_multihop_%d.db", testname.Nano())
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
	return db, context.Background()
}

// multiHopFakeLLM scripts a sequence of decision responses, one per call, so a
// test can drive the retrieve → reason → retrieve loop deterministically. Once
// the scripted responses are exhausted it repeats the last one.
type multiHopFakeLLM struct {
	responses []string
	calls     int
}

func (f *multiHopFakeLLM) GenerateJSON(_ context.Context, _ string, _ string) ([]byte, error) {
	i := f.calls
	f.calls++
	if i >= len(f.responses) {
		i = len(f.responses) - 1
	}
	return []byte(f.responses[i]), nil
}

func seedMultiHopKnowledge(t *testing.T, db *cortexdb.DB, ctx context.Context) {
	t.Helper()
	docs := []cortexdb.KnowledgeSaveRequest{
		{KnowledgeID: "doc:alpha", Title: "Project Alpha", Content: "Project Alpha is owned by the Platform team and depends on SQLite for storage."},
		{KnowledgeID: "doc:platform", Title: "Platform Team", Content: "The Platform team is led by Dana and maintains the storage layer."},
	}
	for _, d := range docs {
		if _, err := db.SaveKnowledge(ctx, d); err != nil {
			t.Fatalf("save knowledge %s: %v", d.KnowledgeID, err)
		}
	}
}

// TestMultiHopSearchLoops verifies the loop runs at least two hops: hop 1 says
// "not enough" and hands back a follow-up query, hop 2 says "enough" and
// returns the final answer.
func TestMultiHopSearchLoops(t *testing.T) {
	db, ctx := openMultiHopTestDB(t)
	seedMultiHopKnowledge(t, db, ctx)

	llm := &multiHopFakeLLM{responses: []string{
		`{"enough":false,"next_query":"who leads the Platform team","reasoning":"need the team lead"}`,
		`{"enough":true,"answer":"FINAL ANSWER: Project Alpha is owned by the Platform team, led by Dana."}`,
	}}

	result, err := MultiHopSearch(ctx, db, "who leads the team that owns Project Alpha?", MultiHopOptions{LLM: llm})
	if err != nil {
		t.Fatalf("multi-hop: %v", err)
	}
	if result.Hops < 2 {
		t.Fatalf("expected at least 2 hops, got %d (steps=%+v)", result.Hops, result.Steps)
	}
	if !strings.HasPrefix(result.Answer, "FINAL ANSWER") {
		t.Fatalf("expected the LLM's final answer, got %q", result.Answer)
	}
	if llm.calls < 2 {
		t.Fatalf("expected the LLM to be consulted on each hop, got %d calls", llm.calls)
	}
}

// TestMultiHopSearchMaxHops verifies that when the model never says "enough"
// the loop still terminates at MaxHops and returns a non-empty answer (either
// the model's running answer or a final reduce over the evidence).
func TestMultiHopSearchMaxHops(t *testing.T) {
	db, ctx := openMultiHopTestDB(t)
	seedMultiHopKnowledge(t, db, ctx)

	// Never enough, and each hop yields a *distinct* next query so the loop
	// guard doesn't short-circuit it before MaxHops. A trailing "answer" lets
	// the loop emit without a separate reduce call.
	llm := &multiHopFakeLLM{responses: []string{
		`{"enough":false,"next_query":"platform team storage","answer":"partial 1"}`,
		`{"enough":false,"next_query":"who is Dana","answer":"partial 2"}`,
	}}

	result, err := MultiHopSearch(ctx, db, "trace the ownership chain for Project Alpha", MultiHopOptions{LLM: llm, MaxHops: 2})
	if err != nil {
		t.Fatalf("multi-hop: %v", err)
	}
	if result.Hops != 2 {
		t.Fatalf("expected exactly MaxHops=2 hops, got %d", result.Hops)
	}
	if strings.TrimSpace(result.Answer) == "" {
		t.Fatalf("expected a non-empty answer even without 'enough'")
	}
}

// TestMultiHopSearchGuards verifies the required-argument guards.
func TestMultiHopSearchGuards(t *testing.T) {
	db, ctx := openMultiHopTestDB(t)
	if _, err := MultiHopSearch(ctx, db, "q", MultiHopOptions{}); err == nil {
		t.Fatalf("expected error when LLM is nil")
	}
	if _, err := MultiHopSearch(ctx, db, "   ", MultiHopOptions{LLM: &multiHopFakeLLM{responses: []string{`{}`}}}); err == nil {
		t.Fatalf("expected error on empty query")
	}
}
