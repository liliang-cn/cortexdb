package memoryflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Memory edits are not graph edits. A graph fact deleted in error is
// re-derivable — run the extractor over the source text again. A memory is
// often the only record of what was said, so the destructive op is deliberately
// absent: the model may only supersede, which keeps the old row and links it
// forward, and is therefore reversible.

func editTestDB(t *testing.T) *cortexdb.DB {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "m.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func saveMem(t *testing.T, db *cortexdb.DB, id, content string) {
	t.Helper()
	if _, err := db.SaveMemory(context.Background(), cortexdb.MemorySaveRequest{
		MemoryID: id, Content: content, Scope: "global",
	}); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
}

func TestApplyMemoryEditsAddsNewMemories(t *testing.T) {
	db := editTestDB(t)
	rep, err := ApplyMemoryEdits(context.Background(), db, MemoryEditPlan{Edits: []MemoryEdit{
		{Op: MemoryEditOpAdd, Content: "用户偏好 dell 而不是 hp 跑重负载"},
	}}, MemoryEditOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rep.Added != 1 {
		t.Fatalf("want 1 added, got %+v", rep)
	}
}

// Supersede must be opt-in for the same reason delete is on the graph side: a
// model that has misread the situation should not be able to retire memories
// without the caller having asked for that.
func TestSupersedeIsOptIn(t *testing.T) {
	db := editTestDB(t)
	saveMem(t, db, "old", "旧的说法")

	rep, err := ApplyMemoryEdits(context.Background(), db, MemoryEditPlan{Edits: []MemoryEdit{
		{Op: MemoryEditOpSupersede, MemoryID: "old", Content: "新的说法"},
	}}, MemoryEditOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rep.Superseded != 0 {
		t.Errorf("supersede applied without AllowSupersede: %+v", rep)
	}
	if len(rep.Skipped) == 0 {
		t.Error("a skipped supersede must be reported, not silently dropped")
	}
}

// The old memory survives and points forward. That is the whole difference from
// a delete: recovery needs no backup, only a lookup.
func TestSupersedeKeepsTheOldMemoryAndLinksItForward(t *testing.T) {
	ctx := context.Background()
	db := editTestDB(t)
	saveMem(t, db, "old", "openclaw 只能当服务端")

	rep, err := ApplyMemoryEdits(ctx, db, MemoryEditPlan{Edits: []MemoryEdit{
		{Op: MemoryEditOpSupersede, MemoryID: "old", Content: "openclaw 也能当客户端", Reason: "读了插件 configSchema"},
	}}, MemoryEditOptions{AllowSupersede: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rep.Superseded != 1 || rep.Added != 0 {
		t.Fatalf("a supersede counts once, not as a supersede plus an add: %+v", rep)
	}

	got, err := db.GetMemory(ctx, cortexdb.MemoryGetRequest{MemoryID: "old"})
	if err != nil {
		t.Fatalf("the superseded memory must still be readable: %v", err)
	}
	if got.Memory.ID == "" {
		t.Fatal("superseded memory disappeared")
	}
	if got.Memory.Metadata[supersededByKey] == nil {
		t.Errorf("no forward link on the retired memory: %+v", got.Memory.Metadata)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	ctx := context.Background()
	db := editTestDB(t)
	saveMem(t, db, "old", "原样")

	rep, err := ApplyMemoryEdits(ctx, db, MemoryEditPlan{Edits: []MemoryEdit{
		{Op: MemoryEditOpSupersede, MemoryID: "old", Content: "不该写进去"},
		{Op: MemoryEditOpAdd, Content: "也不该写进去"},
	}}, MemoryEditOptions{AllowSupersede: true, DryRun: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !rep.DryRun || rep.Superseded != 1 || rep.Added != 1 {
		t.Fatalf("a dry run should still report what it would do: %+v", rep)
	}

	got, _ := db.GetMemory(ctx, cortexdb.MemoryGetRequest{MemoryID: "old"})
	if got != nil && got.Memory.Metadata[supersededByKey] != nil {
		t.Error("dry run mutated the database")
	}
}

// A confused model must not be able to retire a whole brain in one pass.
func TestSupersedesAreCapped(t *testing.T) {
	ctx := context.Background()
	db := editTestDB(t)
	var edits []MemoryEdit
	for _, id := range []string{"a", "b", "c"} {
		saveMem(t, db, id, "记忆 "+id)
		edits = append(edits, MemoryEdit{Op: MemoryEditOpSupersede, MemoryID: id, Content: "新 " + id})
	}

	rep, err := ApplyMemoryEdits(ctx, db, MemoryEditPlan{Edits: edits},
		MemoryEditOptions{AllowSupersede: true, MaxSupersedes: 2})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rep.Superseded != 2 {
		t.Errorf("cap not enforced: %+v", rep)
	}
	if len(rep.Skipped) == 0 {
		t.Error("the capped edit must be reported")
	}
}

func TestUnknownOpIsSkippedNotFatal(t *testing.T) {
	db := editTestDB(t)
	rep, err := ApplyMemoryEdits(context.Background(), db, MemoryEditPlan{Edits: []MemoryEdit{
		{Op: "delete", MemoryID: "old"},
		{Op: MemoryEditOpAdd, Content: "这条仍然要写进去"},
	}}, MemoryEditOptions{})
	if err != nil {
		t.Fatalf("one bad edit must not fail the whole plan: %v", err)
	}
	if rep.Added != 1 {
		t.Errorf("the good edit was lost: %+v", rep)
	}
	if len(rep.Skipped) == 0 {
		t.Error("the unknown op must be reported")
	}
}

// Superseding something that is not there is a model error, not a crash.
func TestSupersedeOfAMissingMemoryIsSkipped(t *testing.T) {
	db := editTestDB(t)
	rep, err := ApplyMemoryEdits(context.Background(), db, MemoryEditPlan{Edits: []MemoryEdit{
		{Op: MemoryEditOpSupersede, MemoryID: "nope", Content: "新的"},
	}}, MemoryEditOptions{AllowSupersede: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rep.Superseded != 0 || len(rep.Skipped) == 0 {
		t.Errorf("want a reported skip, got %+v", rep)
	}
}

// stubLLM records the prompt it was handed and replays a fixed plan.
type stubLLM struct {
	sawUser string
	reply   string
}

func (s *stubLLM) GenerateJSON(_ context.Context, _, user string) ([]byte, error) {
	s.sawUser = user
	return []byte(s.reply), nil
}

// A memory that has already been retired must not come back as a candidate:
// otherwise every later pass sees it, and the model can retire it again or
// argue with a claim that was withdrawn.
func TestProposeLeavesRetiredMemoriesOutOfTheCandidates(t *testing.T) {
	ctx := context.Background()
	db := editTestDB(t)
	saveMem(t, db, "live", "node-e 在 hp 上")
	saveMem(t, db, "gone", "node-e 在 dell 上")

	if _, err := ApplyMemoryEdits(ctx, db, MemoryEditPlan{Edits: []MemoryEdit{
		{Op: MemoryEditOpSupersede, MemoryID: "gone", Content: "node-e 在 hp 上"},
	}}, MemoryEditOptions{AllowSupersede: true}); err != nil {
		t.Fatalf("setup supersede: %v", err)
	}

	llm := &stubLLM{reply: `{"edits":[]}`}
	if _, err := ProposeMemoryEdits(ctx, db, "node-e 到底在哪台宿主机上", llm, MemoryEditOptions{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if strings.Contains(llm.sawUser, "id=gone") {
		t.Errorf("a retired memory was offered as a candidate:\n%s", llm.sawUser)
	}
	if !strings.Contains(llm.sawUser, "id=live") {
		t.Errorf("the live memory was not offered:\n%s", llm.sawUser)
	}
}

// Models wrap JSON in prose and code fences; the plan still has to come out.
func TestProposeDecodesAPlanWrappedInProse(t *testing.T) {
	db := editTestDB(t)
	llm := &stubLLM{reply: "Sure!\n```json\n{\"edits\":[{\"op\":\"add\",\"content\":\"一条新事实\"}]}\n```\n"}

	plan, err := ProposeMemoryEdits(context.Background(), db, "一条新事实", llm, MemoryEditOptions{})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(plan.Edits) != 1 || plan.Edits[0].Op != MemoryEditOpAdd {
		t.Fatalf("plan not decoded: %+v", plan)
	}
}

func TestProposeNeedsAnLLM(t *testing.T) {
	if _, err := ProposeMemoryEdits(context.Background(), editTestDB(t), "x", nil, MemoryEditOptions{}); err == nil {
		t.Error("a nil generator must be refused, not silently no-op")
	}
}
