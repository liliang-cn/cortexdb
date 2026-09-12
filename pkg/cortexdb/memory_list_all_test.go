package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
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

func TestMemoryListAllPagesThroughEverything(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	const total = 25
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("m%02d", i)
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Content: "记忆 " + id, Scope: "global"}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	seen := map[string]int{}
	cursor := ""
	pages := 0
	for {
		resp, err := db.ListAllMemoriesPaged(ctx, MemoryListAllRequest{Limit: 7, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		if pages > 20 {
			t.Fatal("walk did not terminate")
		}
		for _, m := range resp.Memories {
			seen[m.ID]++
		}
		if !resp.Truncated {
			if resp.NextCursor != "" {
				t.Errorf("final page offered a cursor: %q", resp.NextCursor)
			}
			break
		}
		if resp.NextCursor == "" {
			t.Fatal("Truncated with no NextCursor: the caller has no way to continue")
		}
		cursor = resp.NextCursor
	}

	if len(seen) != total {
		t.Errorf("walk covered %d of %d memories", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s returned %d times, want exactly 1", id, n)
		}
	}
}

func TestMemoryListAllTruncatedAlwaysCarriesACursor(t *testing.T) {
	// This is the defect that stranded the shared brain: the listing said it
	// had stopped and offered no way to continue.
	ctx := context.Background()
	db := listAllTestDB(t)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("x%d", i)
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Content: id, Scope: "global"}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	resp, err := db.ListAllMemoriesPaged(ctx, MemoryListAllRequest{Limit: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !resp.Truncated {
		t.Fatal("want Truncated with limit 2 over 5 records")
	}
	if resp.NextCursor == "" {
		t.Fatal("Truncated without NextCursor")
	}
}

func TestMemoryListAllPagesAcrossIdenticalTimestamps(t *testing.T) {
	// Memories saved in the same instant share created_at, so the id is what
	// breaks the tie. If the keyset predicate gets that wrong the walk either
	// loops on one timestamp or skips the rest of it.
	ctx := context.Background()
	db := listAllTestDB(t)
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("same%d", i)
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Content: id, Scope: "global"}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 10; i++ {
		resp, err := db.ListAllMemoriesPaged(ctx, MemoryListAllRequest{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		for _, m := range resp.Memories {
			if seen[m.ID] {
				t.Fatalf("%s came back twice", m.ID)
			}
			seen[m.ID] = true
		}
		if !resp.Truncated {
			break
		}
		cursor = resp.NextCursor
	}
	if len(seen) != 6 {
		t.Errorf("saw %d of 6", len(seen))
	}
}

func TestMemoryListAllRejectsAMalformedCursor(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	if _, err := db.ListAllMemoriesPaged(ctx, MemoryListAllRequest{Cursor: "not-a-cursor!!"}); err == nil {
		t.Fatal("accepted a malformed cursor")
	}
}

// A row inserted between two page fetches must be neither skipped over nor
// counted twice among the rows the walk already had a position past. Keyset
// pagination gives this by construction — the cursor is a row's own sort key,
// not a position that shifts when the table does — but a shared brain is
// written while it is read constantly enough that the guarantee deserves its
// own test rather than living only in the reasoning above listMemoryPage.
func TestMemoryListAllToleratesAWriteBetweenPages(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	const total = 6
	ids := make([]string, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("w%d", i)
		ids[i] = id
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Content: id, Scope: "global"}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	first, err := db.ListAllMemoriesPaged(ctx, MemoryListAllRequest{Limit: 3})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if !first.Truncated || first.NextCursor == "" {
		t.Fatal("want a truncated first page with a cursor")
	}

	// A write lands between the two page fetches — the situation a shared
	// brain is in constantly.
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "late", Content: "late", Scope: "global"}); err != nil {
		t.Fatalf("late write: %v", err)
	}

	seen := map[string]int{}
	for _, m := range first.Memories {
		seen[m.ID]++
	}
	cursor := first.NextCursor
	for pages := 1; ; pages++ {
		if pages > 20 {
			t.Fatal("walk did not terminate")
		}
		resp, err := db.ListAllMemoriesPaged(ctx, MemoryListAllRequest{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, m := range resp.Memories {
			seen[m.ID]++
		}
		if !resp.Truncated {
			break
		}
		cursor = resp.NextCursor
	}

	for _, id := range ids {
		if seen[id] != 1 {
			t.Errorf("%s seen %d times, want exactly 1", id, seen[id])
		}
	}
	// "late" is newer than everything the walk had already moved past when it
	// was written, so it sorts ahead of the cursor's position under
	// created_at DESC — a walk already past that position correctly never
	// doubles back for it. An offset-based scheme would have had no such
	// guarantee: the insert would have shifted every later page by one,
	// dropping or repeating a real row instead.
	if seen["late"] != 0 {
		t.Errorf("a row inserted after the walk passed its position should not appear, got %d", seen["late"])
	}
}

// On PostgreSQL, created_at is a real TIMESTAMP with microsecond precision
// (pkg/core/store_postgres.go), unlike SQLite's second-granular stored text.
// A keyset cutoff that floors to the whole second — correct on SQLite, where
// nothing ever has a finer timestamp to floor away — skips any Postgres row
// that falls strictly between two whole seconds. This seeds several memories
// within the same second at distinct microsecond instants: exactly the shape
// that silently lost rows before memoryListingCutoffArg bound the cutoff
// per-backend.
func TestMemoryListAllPagesAcrossSubSecondTimestampsOnPostgres(t *testing.T) {
	ctx := context.Background()
	db := openPostgresBrain(t, 4)

	const total = 6
	base := time.Now().UTC().Truncate(time.Second)
	ids := make([]string, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("pgsub%d", i)
		ids[i] = id
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Content: "记忆 " + id, Scope: "global"}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		// SaveMemory always writes CURRENT_TIMESTAMP; overwrite it directly so
		// every id lands within the same second but at a distinct,
		// sub-second instant — the scenario a whole-second cutoff drops.
		ts := base.Add(time.Duration(i) * 150 * time.Millisecond)
		if _, err := db.exec(ctx, `UPDATE messages SET created_at = ? WHERE id = ?`, ts, id); err != nil {
			t.Fatalf("age %s: %v", id, err)
		}
	}

	seen := map[string]int{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatal("walk did not terminate")
		}
		resp, err := db.ListAllMemoriesPaged(ctx, MemoryListAllRequest{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, m := range resp.Memories {
			seen[m.ID]++
		}
		if !resp.Truncated {
			break
		}
		if resp.NextCursor == "" {
			t.Fatal("Truncated with no NextCursor")
		}
		cursor = resp.NextCursor
	}

	for _, id := range ids {
		if seen[id] != 1 {
			t.Errorf("%s seen %d times, want exactly 1 (sub-second timestamps on Postgres)", id, seen[id])
		}
	}
}
