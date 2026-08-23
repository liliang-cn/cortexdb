package cortexdb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMemoryFile(t *testing.T, dir, name, id, body string) {
	t.Helper()
	doc := "---\nname: " + strings.TrimSuffix(name, ".md") + "\ndescription: a memory\nmetadata:\n  id: " + id + "\n  scope: global\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func openSyncDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "sync.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func saveMemories(t *testing.T, db *DB, pairs ...[2]string) {
	t.Helper()
	ctx := context.Background()
	for _, p := range pairs {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: p[0], Scope: "global", Content: p[1]}); err != nil {
			t.Fatalf("save %s: %v", p[0], err)
		}
	}
}

// Deleting a memory has to be as cheap as deleting a file, which is the whole
// reason the export exists. Without prune the directory could only ever add.
func TestPlanMemorySyncPrunesMissingFiles(t *testing.T) {
	db := openSyncDB(t)
	saveMemories(t, db, [2]string{"keep", "the gateway fronts the store"}, [2]string{"wrong", "a fact that turned out to be false"})

	dir := t.TempDir()
	writeMemoryFile(t, dir, "keep.md", "keep", "the gateway fronts the store")

	current, err := db.ListAllMemories(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	plan, err := PlanMemorySync(current, MemorySyncOptions{Dir: dir, Prune: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Delete) != 1 || plan.Delete[0] != "wrong" {
		t.Fatalf("expected to prune %q, got %v", "wrong", plan.Delete)
	}
	if plan.Unchanged != 1 {
		t.Errorf("expected the untouched file to count as unchanged, got %d", plan.Unchanged)
	}
	if len(plan.Create) != 0 || len(plan.Update) != 0 {
		t.Errorf("nothing should be written: create=%v update=%v", plan.Create, plan.Update)
	}
}

// Prune off is the default so an ordinary sync cannot lose anything.
func TestPlanMemorySyncWithoutPruneDeletesNothing(t *testing.T) {
	db := openSyncDB(t)
	saveMemories(t, db, [2]string{"keep", "kept"}, [2]string{"gone", "not in the directory"})

	dir := t.TempDir()
	writeMemoryFile(t, dir, "keep.md", "keep", "kept")

	current, _ := db.ListAllMemories(context.Background())
	plan, err := PlanMemorySync(current, MemorySyncOptions{Dir: dir})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Delete) != 0 {
		t.Errorf("prune is off, nothing should be deleted: %v", plan.Delete)
	}
}

// Pointing prune at the wrong directory would otherwise empty the brain, and an
// empty directory looks exactly like "delete everything".
func TestPlanMemorySyncRefusesToPruneFromEmptyDir(t *testing.T) {
	db := openSyncDB(t)
	saveMemories(t, db, [2]string{"a", "one"}, [2]string{"b", "two"})

	current, _ := db.ListAllMemories(context.Background())
	_, err := PlanMemorySync(current, MemorySyncOptions{Dir: t.TempDir(), Prune: true})
	if err == nil {
		t.Fatal("expected a refusal to prune from a directory with no memory files")
	}
	if !strings.Contains(err.Error(), "refusing to prune") {
		t.Errorf("error should say why it refused, got: %v", err)
	}
}

// Editing the body of an exported file is the intended way to correct a memory.
func TestSyncMemoryDirAppliesEditsCreatesAndDeletes(t *testing.T) {
	db := openSyncDB(t)
	ctx := context.Background()
	saveMemories(t, db,
		[2]string{"edit-me", "the old wrong text"},
		[2]string{"delete-me", "obsolete"},
		[2]string{"leave-me", "still true"},
	)

	dir := t.TempDir()
	writeMemoryFile(t, dir, "edit-me.md", "edit-me", "the corrected text")
	writeMemoryFile(t, dir, "leave-me.md", "leave-me", "still true")
	writeMemoryFile(t, dir, "brand-new.md", "brand-new", "written by hand in an editor")

	report, err := SyncMemoryDir(ctx, db, MemorySyncOptions{Dir: dir, Prune: true})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if report.Created != 1 || report.Updated != 1 || report.Deleted != 1 || report.Unchanged != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}

	got, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "edit-me"})
	if err != nil {
		t.Fatalf("get edited: %v", err)
	}
	if strings.TrimSpace(got.Memory.Content) != "the corrected text" {
		t.Errorf("edit did not reach the store: %q", got.Memory.Content)
	}
	if _, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "delete-me"}); err == nil {
		t.Error("pruned memory is still readable")
	}
	if _, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "brand-new"}); err != nil {
		t.Errorf("hand-written memory was not created: %v", err)
	}
}

// A memory edited through the directory must stay in the bucket it already
// lives in, or correcting a typo would quietly move it out of recall's reach.
func TestPlanMemorySyncKeepsExistingScopeWhenFileOmitsIt(t *testing.T) {
	db := openSyncDB(t)
	saveMemories(t, db, [2]string{"m1", "original"})

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "m1.md"),
		[]byte("---\nname: m1\nmetadata:\n  id: m1\n---\n\nedited body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	current, _ := db.ListAllMemories(context.Background())
	plan, err := PlanMemorySync(current, MemorySyncOptions{Dir: dir})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Update) != 1 {
		t.Fatalf("expected one update, got %d", len(plan.Update))
	}
	if plan.Update[0].Scope != current[0].Scope {
		t.Errorf("scope drifted: file gave %q, record had %q", plan.Update[0].Scope, current[0].Scope)
	}
}

// The index is a file of pointers, not a memory; syncing must not turn it into one.
func TestPlanMemorySyncSkipsIndexFile(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "real.md", "real", "a real memory")
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("# Memory Index\n\n- [real](real.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanMemorySync(nil, MemorySyncOptions{Dir: dir})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Scanned != 1 {
		t.Errorf("MEMORY.md should not be scanned as a memory, scanned=%d", plan.Scanned)
	}
	if len(plan.Create) != 1 || plan.Create[0].MemoryID != "real" {
		t.Errorf("unexpected creates: %+v", plan.Create)
	}
}
