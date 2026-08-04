package cortexdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// The HTML dashboard and the Markdown export both need every memory, not a
// search result. Without a bulk tool those two modes can only ever read a local
// file, which on a machine using a shared brain is the wrong database.

func listAllTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "l.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMemoryListAllToolReturnsEveryMemory(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Content: "记忆 " + id, Scope: "global"}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	got, err := db.GraphRAGTools().Call(ctx, "memory_list_all", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	resp, ok := got.(*MemoryListAllResponse)
	if !ok {
		t.Fatalf("unexpected type %T", got)
	}
	if len(resp.Memories) != 3 {
		t.Fatalf("want 3 memories, got %d", len(resp.Memories))
	}
	if resp.Memories[0].Content == "" || resp.Memories[0].ID == "" {
		t.Errorf("records came back hollow: %+v", resp.Memories[0])
	}
}

// A brain with tens of thousands of memories must not be pulled in one message
// by accident; the caller can raise it deliberately.
func TestMemoryListAllRespectsALimit(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Content: "记忆 " + id, Scope: "global"}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := db.GraphRAGTools().Call(ctx, "memory_list_all", json.RawMessage(`{"limit":2}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	resp := got.(*MemoryListAllResponse)
	if len(resp.Memories) != 2 {
		t.Errorf("limit ignored: got %d", len(resp.Memories))
	}
	if !resp.Truncated {
		t.Error("a truncated listing must say so, or the export silently loses memories")
	}
}
