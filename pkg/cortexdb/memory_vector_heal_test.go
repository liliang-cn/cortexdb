package cortexdb

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// flakyEmbedder is stubEmbedder behind a switch: down, it refuses every call
// the way an embeddings gateway with no node behind it answers 503.
type flakyEmbedder struct {
	down  atomic.Bool
	calls atomic.Int64
}

func (f *flakyEmbedder) Dim() int { return 8 }
func (f *flakyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	f.calls.Add(1)
	if f.down.Load() {
		return nil, fmt.Errorf("503 Service Unavailable: no node currently serves model")
	}
	return stubEmbedder{dim: 8}.Embed(ctx, text)
}
func (f *flakyEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls.Add(1)
	if f.down.Load() {
		return nil, fmt.Errorf("503 Service Unavailable: no node currently serves model")
	}
	return stubEmbedder{dim: 8}.EmbedBatch(ctx, texts)
}

func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", within, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Memories saved while the embedder is down used to stay without a vector
// after it came back, out of semantic recall for good. They are embedded once
// it answers again, without anyone running a repair.
func TestHealerEmbedsMemoriesSavedDuringAnOutage(t *testing.T) {
	emb := &flakyEmbedder{}
	emb.down.Store(true)
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "heal.db")),
		WithEmbedder(emb),
		WithVectorHealing(VectorHealOptions{MinBackoff: 20 * time.Millisecond, MaxBackoff: 40 * time.Millisecond, Idle: 20 * time.Millisecond}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{
			MemoryID: fmt.Sprintf("m%d", i), Scope: "global",
			Content: fmt.Sprintf("saved while the embedder was down, number %d", i),
		}); err != nil {
			t.Fatalf("save during outage: %v", err)
		}
	}
	st, err := db.EmbedderStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Healthy || st.MemoriesWithoutVector != 3 || !strings.Contains(st.LastError, "503") {
		t.Fatalf("an outage must show: %+v", st)
	}

	emb.down.Store(false)
	waitFor(t, 5*time.Second, "the backlog to be embedded", func() bool {
		st, err := db.EmbedderStatus(ctx)
		return err == nil && st.MemoriesWithoutVector == 0 && st.Healthy
	})
	res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "embedder down", Scope: "global", TopK: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Results) == 0 {
		t.Fatal("healed memories are not recalled")
	}
}

// A dead embedder is not hammered: failed passes back off.
func TestHealerBacksOffWhileTheEmbedderIsDown(t *testing.T) {
	emb := &flakyEmbedder{}
	emb.down.Store(true)
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "backoff.db")),
		WithEmbedder(emb),
		WithVectorHealing(VectorHealOptions{MinBackoff: 50 * time.Millisecond, MaxBackoff: 200 * time.Millisecond, Idle: time.Hour}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m", Scope: "global", Content: "owed a vector"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// More saves while it is down must not each trigger a pass.
	for i := 0; i < 20; i++ {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: fmt.Sprintf("x%d", i), Scope: "global", Content: "also owed"}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	saves := emb.calls.Load() // one Embed per save
	time.Sleep(700 * time.Millisecond)
	// Backoff 50, 100, 200, 200, 200 ms: about five passes in 700ms. A loop
	// without backoff makes thousands.
	if passes := emb.calls.Load() - saves; passes > 8 {
		t.Fatalf("%d embedder calls in 700ms while it was down: the healer is not backing off", passes)
	}
}

// Recall used to say "auto mode used semantic vector search because an
// embedder is available" while its embedder was failing, so a caller could not
// tell a store with nothing close from one that could not look.
func TestRecallSaysWhenTheEmbedderIsFailing(t *testing.T) {
	emb := &flakyEmbedder{}
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "say.db")),
		WithEmbedder(emb), WithVectorHealing(VectorHealOptions{Disabled: true}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m", Scope: "global", Content: "peanut allergy noted"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	emb.down.Store(true)
	res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "peanut allergy", Scope: "global", TopK: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.Decision.EffectiveMode != RetrievalModeLexical || !strings.Contains(res.Decision.Reason, "embedder failing") {
		t.Fatalf("decision hides the outage: %+v", res.Decision)
	}
	if len(res.Results) == 0 {
		t.Fatal("keyword recall must still answer during an outage")
	}

	report, err := db.GraphHealth(ctx, GraphHealthOptions{})
	if err != nil {
		t.Fatalf("graph health: %v", err)
	}
	if report.Healthy || report.Embedder.Healthy {
		t.Fatalf("graph_health must raise the embedder alert: alerts=%v embedder=%+v", report.Alerts, report.Embedder)
	}
	found := false
	for _, a := range report.Alerts {
		found = found || a == "embedder"
	}
	if !found {
		t.Fatalf("alerts %v do not name the embedder", report.Alerts)
	}
}

// fixedEmbedder answers with a preset vector per text, so a test can place a
// query at an exact cosine from a memory.
type fixedEmbedder map[string][]float32

func (f fixedEmbedder) Dim() int { return 4 }
func (f fixedEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if v, ok := f[text]; ok {
		return v, nil
	}
	return []float32{0, 0, 0, 1}, nil
}
func (f fixedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i], _ = f.Embed(ctx, t)
	}
	return out, nil
}

// The noise band: a query about something the store holds nothing on still
// has a nearest neighbour, and embeddinggemma put unrelated probes as high as
// 0.307. At 0.30 a neighbour is dropped by default, kept when the floor is set
// lower, and a memory that shares the words asked about is returned whatever
// its cosine.
func TestSemanticRecallDropsTheNoiseBand(t *testing.T) {
	const memory = "新西兰自驾两周预算偏紧"
	const literal = "英伟达 持仓成本 95 美元"
	const query = "英伟达 成本"
	c := float32(0.30)
	emb := fixedEmbedder{
		memory:  {1, 0, 0, 0},
		literal: {0, 0, 1, 0},
		query:   {c, float32(math.Sqrt(float64(1 - c*c))), 0, 0},
	}
	open := func(name string, opts ...Option) *DB {
		db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), name)), append([]Option{WithEmbedder(emb)}, opts...)...)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	ctx := context.Background()
	ids := func(db *DB) []string {
		res, err := db.SearchMemory(ctx, MemorySearchRequest{Query: query, Scope: "global", TopK: 5})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		var out []string
		for _, h := range res.Results {
			out = append(out, h.Memory.ID)
		}
		return out
	}

	db := open("default.db")
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "nz", Scope: "global", Content: memory}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := ids(db); len(got) != 0 {
		t.Fatalf("a neighbour at cosine 0.30 is noise by default, got %v", got)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "nvda", Scope: "global", Content: literal}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := ids(db); len(got) != 1 || got[0] != "nvda" {
		t.Fatalf("the memory sharing the words asked about must be returned alone, got %v", got)
	}

	lower := open("lower.db", WithMemorySemanticFloor(0.25))
	if _, err := lower.SaveMemory(ctx, MemorySaveRequest{MemoryID: "nz", Scope: "global", Content: memory}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := ids(lower); len(got) != 1 || got[0] != "nz" {
		t.Fatalf("with the floor at 0.25 the 0.30 neighbour is kept, got %v", got)
	}
}
