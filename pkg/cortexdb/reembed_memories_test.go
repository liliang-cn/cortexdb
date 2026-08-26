package cortexdb

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// stubEmbedder is deterministic and cheap: dimension dim, first component 1.
type stubEmbedder struct{ dim int }

func (s stubEmbedder) Dim() int { return s.dim }
func (s stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, s.dim)
	v[0] = 1
	if len(text) > 0 {
		v[s.dim-1] = float32(len(text)%7) + 1
	}
	return v, nil
}
func (s stubEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := s.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// failingEmbedder refuses every request, standing in for an unreachable service.
type failingEmbedder struct{ dim int }

func (f failingEmbedder) Dim() int { return f.dim }
func (f failingEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, fmt.Errorf("connection refused")
}
func (f failingEmbedder) EmbedBatch(context.Context, []string) ([][]float32, error) {
	return nil, fmt.Errorf("connection refused")
}

// A memory that cannot be embedded right now must still be remembered: the
// embedder is a network service, and a save refused is a memory gone, unlike a
// vector, which a later re-embed pass fills in.
func TestSaveMemorySurvivesEmbedderOutage(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "outage.db")),
		WithEmbedder(failingEmbedder{dim: 8}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "m1", Scope: "global", Content: "remember me even when the embedder is down",
	}); err != nil {
		t.Fatalf("save should degrade, not fail: %v", err)
	}
	got, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "m1"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Memory.Content == "" {
		t.Fatal("memory content lost")
	}
	// And it must still be findable — lexical search does not need the vector.
	res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "remember embedder down", Scope: "global", TopK: 3, RetrievalMode: RetrievalModeLexical})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Results) == 0 {
		t.Fatal("memory saved during outage is unfindable")
	}
}

// The backlog a store accumulates while running without an embedder: vectors
// missing entirely, or written by a model of another dimension.
func TestReembedMemoryVectorsFillsTheBacklog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backlog.db")
	ctx := context.Background()

	// Era one: no embedder. Memories stored with no vector.
	plain, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := plain.SaveMemory(ctx, MemorySaveRequest{
			MemoryID: fmt.Sprintf("old-%d", i), Scope: "global",
			Content: fmt.Sprintf("memory from the lexical era %d", i),
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	if err := plain.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Era two: embedder configured. The backlog must become embeddable.
	db, err := Open(DefaultConfig(path), WithEmbedder(stubEmbedder{dim: 8}))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()

	dry, err := db.ReembedMemoryVectors(ctx, ReembedOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Candidates != 3 || dry.Reembedded != 0 {
		t.Fatalf("dry run should count 3 and write 0, got %+v", dry)
	}

	report, err := db.ReembedMemoryVectors(ctx, ReembedOptions{})
	if err != nil {
		t.Fatalf("reembed: %v", err)
	}
	if report.Reembedded != 3 || report.Failed != 0 {
		t.Fatalf("expected 3 reembedded, got %+v", report)
	}

	// Second pass finds nothing: the byte-length predicate must recognise the
	// vectors it just wrote.
	again, err := db.ReembedMemoryVectors(ctx, ReembedOptions{})
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if again.Candidates != 0 {
		t.Fatalf("second pass should be a no-op, found %d candidates", again.Candidates)
	}

	// And semantic recall now reaches the backlog.
	res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "lexical era", Scope: "global", TopK: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Results) == 0 {
		t.Fatal("backfilled memories are not retrievable")
	}
}
