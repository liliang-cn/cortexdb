package cortexdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// TestABackupTakenWhileWritingCanBeOpened is the only test that proves the
// feature: a backup nobody has restored is a hope, not a backup. It writes,
// backs up with the source still open, then opens the copy as a database of its
// own and reads the rows back out of it.
func TestABackupTakenWhileWritingCanBeOpened(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "brain.db")

	db, err := Open(DefaultConfig(source)) // no embedder: lexical retrieval
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer db.Close()

	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "apollo",
		Title:       "Apollo",
		Content:     "Alice owns the Apollo project. Apollo ships on Friday.",
		ChunkSize:   40,
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "prefers-go",
		Content:  "Alice prefers Go over Rust for services.",
		Scope:    "global",
	}); err != nil {
		t.Fatalf("save memory: %v", err)
	}

	// The source is still open and was never checkpointed, so this is the
	// live-server case rather than a copy of a quiesced file.
	dest := filepath.Join(dir, "backup.db")
	if err := db.Backup(ctx, dest); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// A backup that needs the source's -wal beside it is not one file, and
	// restoring it by copying only the .db is the mistake this API exists to
	// remove. VACUUM INTO folds the WAL into the copy, so the copy stands alone.
	for _, sidecar := range []string{dest + "-wal", dest + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Fatalf("backup left %s behind; the copy is not self-contained", filepath.Base(sidecar))
		}
	}

	// The source stays usable: a backup that ends the server's day is not one
	// an operator can take on a schedule.
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "after-backup",
		Content:  "Written after the backup was taken.",
		Scope:    "global",
	}); err != nil {
		t.Fatalf("write after backup: %v", err)
	}

	restored, err := Open(DefaultConfig(dest))
	if err != nil {
		t.Fatalf("open the backup: %v", err)
	}
	defer restored.Close()

	got, err := restored.GetMemory(ctx, MemoryGetRequest{MemoryID: "prefers-go"})
	if err != nil {
		t.Fatalf("read memory from the backup: %v", err)
	}
	if !strings.Contains(got.Memory.Content, "prefers Go") {
		t.Fatalf("memory content = %q", got.Memory.Content)
	}

	hits, err := restored.SearchKnowledge(ctx, KnowledgeSearchRequest{
		Query:         "Apollo Alice owns",
		RetrievalMode: RetrievalModeLexical,
		TopK:          3,
	})
	if err != nil {
		t.Fatalf("search the backup: %v", err)
	}
	if len(hits.Results) == 0 {
		t.Fatal("knowledge saved before the backup is not in the backup")
	}

	// Writes that happened after the snapshot must not be in it: a backup that
	// silently kept moving would not be a point in time.
	if _, err := restored.GetMemory(ctx, MemoryGetRequest{MemoryID: "after-backup"}); err == nil {
		t.Fatal("backup contains a memory written after it was taken")
	}
}

// TestBackupRefusesToOverwriteAnExistingFile keeps a scheduled backup from
// quietly destroying yesterday's when the name does not vary.
func TestBackupRefusesToOverwriteAnExistingFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	db, err := Open(DefaultConfig(filepath.Join(dir, "brain.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	dest := filepath.Join(dir, "backup.db")
	if err := db.Backup(ctx, dest); err != nil {
		t.Fatalf("first backup: %v", err)
	}
	if err := db.Backup(ctx, dest); err == nil {
		t.Fatal("second backup overwrote the first")
	}
}

// storeWithoutBackup is a brain that cannot copy itself — PostgreSQL's shape,
// without needing a PostgreSQL to test it. The embedded interface is nil, which
// is fine: Backup must refuse before it calls anything.
type storeWithoutBackup struct{ core.BrainStore }

// TestBackupOnABackendThatCannotSaysSo checks that the run-time cost of making
// Backupper optional is paid back with an error an operator can act on.
func TestBackupOnABackendThatCannotSaysSo(t *testing.T) {
	db := &DB{store: storeWithoutBackup{}}

	err := db.Backup(context.Background(), filepath.Join(t.TempDir(), "backup.db"))
	if !errors.Is(err, ErrBackupUnsupported) {
		t.Fatalf("err = %v, want ErrBackupUnsupported", err)
	}
	if !strings.Contains(err.Error(), "storeWithoutBackup") {
		t.Fatalf("error does not name the backend: %v", err)
	}
}
