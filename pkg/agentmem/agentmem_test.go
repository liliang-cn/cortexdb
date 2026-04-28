package agentmem_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/agentmem"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func newStore(t *testing.T) (*agentmem.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, fmt.Sprintf("agentmem_%d.db", time.Now().UnixNano()))
	cdb, err := cortexdb.Open(cortexdb.DefaultConfig(path))
	if err != nil {
		t.Fatalf("open cortexdb: %v", err)
	}
	store, err := agentmem.New(cdb)
	if err != nil {
		_ = cdb.Close()
		t.Fatalf("new agentmem store: %v", err)
	}
	return store, func() { _ = cdb.Close() }
}

func TestSaveGetRoundtrip(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "alice"}
	m := &agentmem.Memory{
		ID:          "m1",
		Scope:       scope,
		Type:        agentmem.TypeFact,
		Content:     "Apollo ships on Friday.",
		Importance:  0.8,
		Tags:        []string{"apollo", "deadline"},
		Keywords:    []string{"apollo", "friday", "ship"},
		EvidenceIDs: []string{"src-1", "src-2"},
	}
	if err := store.Save(ctx, m); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.Get(ctx, "m1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != m.Content {
		t.Errorf("content mismatch: %q vs %q", got.Content, m.Content)
	}
	if got.Scope != scope {
		t.Errorf("scope mismatch: %+v vs %+v", got.Scope, scope)
	}
	if got.Importance != 0.8 {
		t.Errorf("importance: got %v want 0.8", got.Importance)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "apollo" {
		t.Errorf("tags: %+v", got.Tags)
	}
	if len(got.EvidenceIDs) != 2 {
		t.Errorf("evidence: %+v", got.EvidenceIDs)
	}
}

func TestSaveDefaultsAndAutoID(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	m := &agentmem.Memory{
		Scope:   agentmem.Scope{Type: agentmem.ScopeGlobal},
		Content: "Plain note.",
	}
	if err := store.Save(ctx, m); err != nil {
		t.Fatalf("save: %v", err)
	}
	if m.ID == "" {
		t.Fatal("expected auto id")
	}
	if m.Type != agentmem.TypeFact {
		t.Errorf("default type: got %q", m.Type)
	}
	if m.Importance != 0.5 {
		t.Errorf("default importance: got %v", m.Importance)
	}
	if m.SourceType != agentmem.SourceUserInput {
		t.Errorf("default source: got %q", m.SourceType)
	}
}

func TestIncrementAccess(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	m := &agentmem.Memory{ID: "a", Content: "x"}
	_ = store.Save(ctx, m)

	for i := 0; i < 3; i++ {
		if err := store.IncrementAccess(ctx, "a"); err != nil {
			t.Fatalf("increment: %v", err)
		}
	}
	got, _ := store.Get(ctx, "a")
	if got.AccessCount != 3 {
		t.Errorf("access_count: got %d want 3", got.AccessCount)
	}
	if got.LastAccessed.IsZero() {
		t.Error("last_accessed not set")
	}
}

func TestSearchByTextScoringAndExclusion(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	mustSave := func(id, content string, importance float64) {
		t.Helper()
		err := store.Save(ctx, &agentmem.Memory{
			ID: id, Content: content, Importance: importance,
			Scope: agentmem.Scope{Type: agentmem.ScopeUser, ID: "alice"},
		})
		if err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	mustSave("hi", "Apollo project ships on Friday", 0.9)
	mustSave("lo", "Apollo unrelated trivia", 0.2)
	mustSave("noise", "Completely different topic about gardening", 0.5)

	hits, err := store.SearchByText(ctx, "Apollo Friday", agentmem.SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected ≥2 hits, got %d", len(hits))
	}
	if hits[0].Memory.ID != "hi" {
		t.Errorf("expected high-importance match first, got %q", hits[0].Memory.ID)
	}
	for _, h := range hits {
		if h.Memory.ID == "noise" {
			t.Errorf("unrelated row matched: %+v", h)
		}
	}
}

func TestSearchExcludesArchivedAndStale(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "u"}
	_ = store.Save(ctx, &agentmem.Memory{ID: "active", Scope: scope, Content: "Mars rover landed"})
	_ = store.Save(ctx, &agentmem.Memory{ID: "stale", Scope: scope, Content: "Mars old report"})
	_ = store.Save(ctx, &agentmem.Memory{ID: "archived", Scope: scope, Content: "Mars retired note"})

	if err := store.MarkStale(ctx, "stale", "active"); err != nil {
		t.Fatalf("mark stale: %v", err)
	}
	if err := store.Archive(ctx, "archived", "no longer relevant"); err != nil {
		t.Fatalf("archive: %v", err)
	}

	hits, err := store.SearchByText(ctx, "Mars", agentmem.SearchOptions{TopK: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Memory.ID != "active" {
		t.Fatalf("expected only active row, got %+v", hits)
	}

	hitsAll, err := store.SearchByText(ctx, "Mars", agentmem.SearchOptions{
		TopK: 10, IncludeArchived: true, IncludeStale: true,
	})
	if err != nil {
		t.Fatalf("search incl: %v", err)
	}
	if len(hitsAll) != 3 {
		t.Errorf("with includes: got %d want 3", len(hitsAll))
	}
}

func TestSearchByScope(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	alice := agentmem.Scope{Type: agentmem.ScopeUser, ID: "alice"}
	bob := agentmem.Scope{Type: agentmem.ScopeUser, ID: "bob"}

	_ = store.Save(ctx, &agentmem.Memory{ID: "a", Scope: alice, Content: "secret apollo plan"})
	_ = store.Save(ctx, &agentmem.Memory{ID: "b", Scope: bob, Content: "secret apollo plan"})

	hits, err := store.SearchByScope(ctx, "apollo", []agentmem.Scope{alice}, agentmem.SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("search by scope: %v", err)
	}
	if len(hits) != 1 || hits[0].Memory.ID != "a" {
		t.Fatalf("scope filter failed: %+v", hits)
	}
}

func TestCJKQueries(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	if store.UsesFallbackTokenizer() {
		t.Skip("trigram tokenizer unavailable; CJK substring search not guaranteed")
	}
	ctx := context.Background()

	_ = store.Save(ctx, &agentmem.Memory{ID: "z", Content: "阿波罗项目周五发布", Importance: 0.7})
	hits, err := store.SearchByText(ctx, "阿波罗", agentmem.SearchOptions{TopK: 3})
	if err != nil {
		t.Fatalf("cjk search: %v", err)
	}
	if len(hits) == 0 || hits[0].Memory.ID != "z" {
		t.Fatalf("cjk no hit: %+v", hits)
	}
}

type stubReflector struct{}

func (stubReflector) Consolidate(ctx context.Context, facts, existing []*agentmem.Memory) ([]agentmem.Observation, error) {
	if len(facts) < 2 {
		return nil, nil
	}
	ids := make([]string, 0, len(facts))
	for _, f := range facts {
		ids = append(ids, f.ID)
	}
	return []agentmem.Observation{{
		Content:     "consolidated: project apollo is on track",
		Confidence:  0.85,
		EvidenceIDs: ids,
	}}, nil
}

func TestReflect(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "alice"}
	for i, content := range []string{
		"Apollo team finished review.",
		"Apollo ships Friday.",
		"Apollo passed integration tests.",
	} {
		_ = store.Save(ctx, &agentmem.Memory{
			ID: fmt.Sprintf("f%d", i), Scope: scope,
			Type: agentmem.TypeFact, Content: content, Importance: 0.6,
		})
	}

	res, err := store.Reflect(ctx, scope, stubReflector{})
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if res.Created != 1 {
		t.Errorf("created: got %d want 1", res.Created)
	}
	obs, err := store.GetByType(ctx, agentmem.TypeObservation, 10)
	if err != nil {
		t.Fatalf("list obs: %v", err)
	}
	if len(obs) != 1 || len(obs[0].EvidenceIDs) != 3 {
		t.Fatalf("observation shape: %+v", obs)
	}
	if obs[0].SourceType != agentmem.SourceConsolidated {
		t.Errorf("source: got %q", obs[0].SourceType)
	}

	// Reflect again: facts already evidenced, nothing new.
	res2, _ := store.Reflect(ctx, scope, stubReflector{})
	if res2.Created != 0 {
		t.Errorf("second reflect created %d, want 0", res2.Created)
	}
}

func TestBankConfig(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	scope := agentmem.Scope{Type: agentmem.ScopeAgent, ID: "apollo"}
	cfg := &agentmem.BankConfig{
		Mission:    "ship Apollo",
		Directives: []string{"no scope creep", "report blockers"},
		Skepticism: 3,
	}
	if err := store.ConfigureBank(ctx, scope, cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	got, err := store.GetBankConfig(ctx, scope)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Mission != "ship Apollo" || len(got.Directives) != 2 || got.Skepticism != 3 {
		t.Fatalf("config mismatch: %+v", got)
	}
}

func TestMentalModel(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	mm := &agentmem.MentalModel{
		ID: "rule-1", Name: "Speak plainly",
		Content: "Prefer concrete nouns over abstractions.",
		Tags:    []string{"style"},
	}
	if err := store.AddMentalModel(ctx, mm); err != nil {
		t.Fatalf("add: %v", err)
	}
	all, err := store.ListMentalModels(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 || all[0].ID != "rule-1" || len(all[0].Tags) != 1 {
		t.Fatalf("mismatch: %+v", all)
	}
}

func TestContextSlotsAndBuild(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	scope := agentmem.Scope{Type: agentmem.ScopeAgent, ID: "apollo"}
	if err := store.SetContext(ctx, scope, agentmem.SlotSoul, "be helpful"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := store.AppendContext(ctx, scope, agentmem.SlotSoul, "be concise"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.SetContext(ctx, scope, agentmem.SlotHeartbeat, "[ ] check inbox"); err != nil {
		t.Fatalf("set hb: %v", err)
	}

	got, err := store.GetContext(ctx, scope, agentmem.SlotSoul)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "be helpful\nbe concise" {
		t.Errorf("soul content: %q", got)
	}

	full, err := store.BuildContextString(ctx, scope, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !contains(full, "# SOUL") || !contains(full, "be concise") || !contains(full, "# HEARTBEAT") {
		t.Errorf("context build missing sections: %q", full)
	}
}

func TestBuildEntrypoint(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()
	scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "alice"}

	_ = store.Save(ctx, &agentmem.Memory{ID: "a", Scope: scope, Content: "Top priority", Importance: 0.9})
	_ = store.Save(ctx, &agentmem.Memory{ID: "b", Scope: scope, Content: "Lower priority", Importance: 0.3})

	entry, err := store.BuildEntrypoint(ctx, scope, agentmem.EntrypointOptions{TopN: 5})
	if err != nil {
		t.Fatalf("entrypoint: %v", err)
	}
	if !contains(entry, "Top priority") || !contains(entry, "Memory Entrypoint") {
		t.Errorf("entrypoint missing content: %q", entry)
	}
}

func TestDeleteAndClear(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	_ = store.Save(ctx, &agentmem.Memory{ID: "x", Content: "to delete", Tags: []string{"t1"}})
	if err := store.Delete(ctx, "x"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, "x"); err != agentmem.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	_ = store.Save(ctx, &agentmem.Memory{ID: "y", Content: "yyy"})
	_ = store.Save(ctx, &agentmem.Memory{ID: "z", Content: "zzz"})
	if err := store.Clear(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	all, total, err := store.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 || len(all) != 0 {
		t.Errorf("after clear: total=%d list=%d", total, len(all))
	}
}

func TestRevisionHistory(t *testing.T) {
	t.Parallel()
	store, cleanup := newStore(t)
	defer cleanup()
	ctx := context.Background()

	_ = store.Save(ctx, &agentmem.Memory{ID: "r", Content: "rev test"})
	if err := store.AddRevision(ctx, "r", "user", "first edit"); err != nil {
		t.Fatalf("rev1: %v", err)
	}
	if err := store.AddRevision(ctx, "r", "agent", "second edit"); err != nil {
		t.Fatalf("rev2: %v", err)
	}
	got, err := store.Get(ctx, "r")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.RevisionHistory) != 2 {
		t.Fatalf("history len: %d", len(got.RevisionHistory))
	}
	if got.RevisionHistory[0].By != "user" || got.RevisionHistory[1].By != "agent" {
		t.Errorf("history order: %+v", got.RevisionHistory)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
