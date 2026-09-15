package core

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// What this file is about: a store in WAL mode that never checkpoints grows its `-wal` file until
// the disk is full. An Athanor instance ingesting 248 documents produced an 11 GB `brain.db-wal`
// against a 636 MB database, filled the 57 GB disk it shared with other services, and took an
// unrelated application's PostgreSQL down with it — every connection answering "could not write init
// file: No space left on device". SQLite's automatic checkpoint could not save it: it can only copy
// frames older than the oldest reader still holding a snapshot, and this store keeps a pool of 25
// connections, so during an ingest there is always a reader in flight.

// walSize is the size of the store's write-ahead log, or 0 when there is none.
func walSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path + "-wal")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("could not stat the write-ahead log: %v", err)
	}
	return info.Size()
}

// writeEnoughToGrowTheLog writes past `wal_autocheckpoint` (1000 pages, 4 MB) with a reader open
// throughout, which is the shape that starves the automatic checkpoint.
func writeEnoughToGrowTheLog(t *testing.T, store *SQLiteStore, ctx context.Context) {
	t.Helper()
	// An explicit read transaction, held open across the writes. This is the shape that starves the
	// automatic checkpoint: it can only copy frames older than the oldest reader's snapshot, and the
	// snapshot is taken by the first statement here and released only when the transaction ends.
	reader, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("could not open a reader: %v", err)
	}
	defer func() { _ = reader.Rollback() }()
	var documents int
	if err := reader.QueryRowContext(ctx, "SELECT count(*) FROM documents").Scan(&documents); err != nil {
		t.Fatalf("reader failed: %v", err)
	}

	body := make([]byte, 16*1024)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	for i := 0; i < 400; i++ {
		doc := &Document{
			ID:      fmt.Sprintf("doc-%03d", i),
			Title:   fmt.Sprintf("Document %03d", i),
			Content: string(body),
			Version: 1,
		}
		if err := store.CreateDocument(ctx, doc); err != nil {
			t.Fatalf("could not write document %d: %v", i, err)
		}
	}
}

func TestCheckpointReturnsTheWriteAheadLogToTheDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.db")
	store, err := New(path, 3)
	if err != nil {
		t.Fatalf("could not create the store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("could not initialise the store: %v", err)
	}

	writeEnoughToGrowTheLog(t, store, ctx)
	grew := walSize(t, path)
	if grew == 0 {
		t.Fatalf("the write-ahead log never grew, so this test proves nothing")
	}

	store.checkpointWAL(ctx)

	if after := walSize(t, path); after != 0 {
		t.Fatalf("the log was %d bytes and is still %d after a checkpoint; TRUNCATE is what returns the space", grew, after)
	}
	// And the documents are still there: a checkpoint moves the log into the database, it does not
	// discard it.
	doc, err := store.GetDocument(ctx, "doc-399")
	if err != nil || doc == nil {
		t.Fatalf("a document written before the checkpoint is gone after it: %v", err)
	}
}

func TestClosingAStoreLeavesNoWriteAheadLogBehind(t *testing.T) {
	// SQLite checkpoints on the last connection close by itself, so this passes without the explicit
	// one in `Close` too — it is here as the postcondition, not as the regression. What it does catch
	// is the opposite mistake: a `Close` that tears the pool down while the checkpoint goroutine is
	// mid-flight, or one that stops the goroutine while holding the lock the goroutine needs.
	path := filepath.Join(t.TempDir(), "closing.db")
	store, err := New(path, 3)
	if err != nil {
		t.Fatalf("could not create the store: %v", err)
	}
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("could not initialise the store: %v", err)
	}
	writeEnoughToGrowTheLog(t, store, ctx)

	if err := store.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	if after := walSize(t, path); after != 0 {
		t.Fatalf("a closed store left a %d-byte write-ahead log behind", after)
	}
}

func TestClosingTwiceIsSafe(t *testing.T) {
	// `Close` stops the checkpoint goroutine by closing a channel, and closing a closed channel
	// panics. Two callers racing to close a store is ordinary in a service shutting down.
	store, err := New(filepath.Join(t.TempDir(), "twice.db"), 3)
	if err != nil {
		t.Fatalf("could not create the store: %v", err)
	}
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("could not initialise the store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestInitialisingTwiceStartsOneCheckpointer(t *testing.T) {
	// `Init` is called more than once on the same store: `hindsight.New` opens the database, which
	// inits it, and then inits the vector store again itself. A second start overwrote the channels
	// the first goroutine was selecting on — a write racing that goroutine's read, and a goroutine
	// left running on a store nobody could stop afterwards. Seventeen tests in `pkg/hindsight` failed
	// under `-race` with exactly that report, and none of them are about checkpointing.
	store, err := New(filepath.Join(t.TempDir(), "twice-init.db"), 3)
	if err != nil {
		t.Fatalf("could not create the store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("first init failed: %v", err)
	}
	first := store.checkpointStop

	if err := store.Init(ctx); err != nil {
		t.Fatalf("second init failed: %v", err)
	}

	if store.checkpointStop != first {
		t.Fatal("a second Init replaced the channel the running checkpointer selects on")
	}
}

func TestAnInMemoryStoreStartsNoCheckpointer(t *testing.T) {
	// There is no file to grow and no log to fold, so there is nothing for a goroutine to do — and a
	// goroutine per in-memory store is a leak in every test that opens one.
	store, err := New(":memory:", 3)
	if err != nil {
		t.Fatalf("could not create the store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("could not initialise the store: %v", err)
	}

	if store.checkpointStop != nil {
		t.Fatal("an in-memory store started a write-ahead-log checkpointer")
	}
}
