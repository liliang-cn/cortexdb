# Listing Cursors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `memory_list_all` and `graph_list_all` a cursor so a caller can read a brain that no longer fits in one gRPC message.

**Architecture:** Keyset pagination pushed into SQL. An opaque cursor encodes the last row's sort key; the next call resumes after it. `Truncated` stops being a dead end — it now always carries a `NextCursor`. `--export-memory` loops the cursor instead of asking for everything at once.

**Tech Stack:** Go 1.25, `modernc.org/sqlite`, `pkg/cortexdb` facade, existing MCP tool-definition files.

---

## Background: why this is the blocker

The shared brain at `192.168.123.252:47821` cannot be listed or exported today:

```
$ cortexdb-mcp-stdio --export-memory ./out
cortexdb: memory_list_all on 192.168.123.252:47821: rpc error: code = ResourceExhausted
  desc = grpc: received message larger than max (5121407 vs. 4194304) (is the server new enough?)
0 files written
```

`remote_bulk.go`'s `fetchAllMemoriesRemote` sends `MemoryListAllRequest{Limit}` through `CallTool` and gets one response back, so every bulk path — the HTML dashboard, the Markdown export, and any agent calling the tool — dies at the same wall. `defaultMemoryListLimit` is 5000 while the transport gives out near 1100 records; the two limits were never reconciled.

`ListAllMemoriesPaged` is named for paging it does not do. It takes a `Limit`, returns `Truncated`, and offers no way to continue.

## Two decisions this plan locks in

**1. The cursor is opaque and keyset-based, never an offset.** A shared brain is written while it is read. An offset silently skips records when rows land behind the cursor; a keyset does not.

**2. `graph_list_all` gets a cursor, but not "the same treatment" the spec's wording implies.** `ListGraphAll` truncates by keeping the *most-connected core* (`sortGraphNodesByDegree`), which is what makes a big graph renderable. Degree ranking and a stable walk are different orders and cannot be the same call:

- **No cursor** → unchanged. Degree-ranked core, `Truncated`, no `NextCursor`. The HTML view keeps working exactly as it does.
- **Cursor mode** (`cursor` supplied, or `order: "id"`) → id-ordered walk, `NextCursor`, no degree ranking.

Both paths are pinned by tests so neither can quietly become the other.

## File Structure

| File | Responsibility |
|---|---|
| `pkg/cortexdb/listing_cursor.go` | **new** — encode/decode the opaque cursor. Nothing else. |
| `pkg/cortexdb/listing_cursor_test.go` | **new** — codec round-trip and rejection. |
| `pkg/cortexdb/memory_list_all.go` | Request/response gain `Cursor`/`NextCursor`; keyset SQL page. |
| `pkg/cortexdb/memory_list_all_test.go` | Paging invariants for memories. |
| `pkg/cortexdb/graph_list_all.go` | Cursor mode alongside the degree-ranked core. |
| `pkg/cortexdb/graph_list_all_test.go` | **new** — both graph modes pinned. |
| `pkg/cortexdb/knowledge_memory_tooldefs.go` | Tool schemas + descriptions for the two tools. |
| `cmd/cortexdb-mcp-stdio/remote_bulk.go` | Loop the cursor when fetching the shared brain. |
| `README.md`, `README_CN.md`, `SKILL.md` | Kept in sync per `CLAUDE.md`. |

`pkg/cortexdb` is intentionally flat with topic-prefixed files, so the new cursor codec is a new topic file rather than a new package.

---

## Task 1: The cursor codec

**Files:**
- Create: `pkg/cortexdb/listing_cursor.go`
- Test: `pkg/cortexdb/listing_cursor_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/cortexdb/listing_cursor_test.go`:

```go
package cortexdb

import (
	"testing"
	"time"
)

func TestListingCursorRoundTrips(t *testing.T) {
	ts := time.Date(2026, 9, 12, 10, 30, 0, 123456789, time.UTC)
	enc := encodeListingCursor(ts, "memory:global:default")

	gotTS, gotID, err := decodeListingCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotTS.Equal(ts) {
		t.Errorf("timestamp: want %v, got %v", ts, gotTS)
	}
	if gotID != "memory:global:default" {
		t.Errorf("id: want memory:global:default, got %q", gotID)
	}
}

func TestListingCursorIsOpaque(t *testing.T) {
	// A caller must not be able to read an offset out of it and start doing
	// arithmetic on it — the value is ours to change.
	enc := encodeListingCursor(time.Now(), "some:id")
	if enc == "" {
		t.Fatal("empty cursor")
	}
	if containsAny(enc, []string{"some:id", ":"}) {
		t.Errorf("cursor leaks its contents: %q", enc)
	}
}

func TestListingCursorRejectsGarbage(t *testing.T) {
	// A malformed cursor must be an error, never silently "start from the
	// beginning" — that would make a resumed walk quietly return duplicates.
	for _, bad := range []string{"not-base64!!", "", "YWJjZA=="} {
		if _, _, err := decodeListingCursor(bad); err == nil {
			t.Errorf("accepted garbage cursor %q", bad)
		}
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd ~/Things/AI/base/CortexDB
go test ./pkg/cortexdb -run TestListingCursor -v
```

Expected: FAIL to compile — `undefined: encodeListingCursor`.

- [ ] **Step 3: Write minimal implementation**

Create `pkg/cortexdb/listing_cursor.go`:

```go
package cortexdb

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// A listing cursor is an opaque resume point for a bulk read: the sort key of
// the last row a page scanned. It is keyset, never an offset — a shared brain
// is written while it is read, and an offset silently skips records when rows
// land behind it.
//
// Opaque is a contract, not decoration. A caller that parsed this and did
// arithmetic on it would be holding a lock on a detail we need to change.

// encodeListingCursor packs a sort key into a cursor value.
func encodeListingCursor(ts time.Time, id string) string {
	raw := ts.UTC().Format(time.RFC3339Nano) + "\x00" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeListingCursor unpacks a cursor. A malformed value is an error rather
// than a silent restart: a resumed walk that quietly began again would return
// duplicates and look complete.
func decodeListingCursor(cursor string) (time.Time, string, error) {
	if strings.TrimSpace(cursor) == "" {
		return time.Time{}, "", fmt.Errorf("cortexdb: empty listing cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("cortexdb: malformed listing cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("cortexdb: malformed listing cursor: missing separator")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("cortexdb: malformed listing cursor timestamp: %w", err)
	}
	return ts, parts[1], nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./pkg/cortexdb -run TestListingCursor -v
```

Expected: PASS, 3 tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/listing_cursor.go pkg/cortexdb/listing_cursor_test.go
git commit -m "A resume point for a listing, opaque on purpose"
```

---

## Task 2: Memories page by keyset

**Files:**
- Modify: `pkg/cortexdb/memory_list_all.go`
- Test: `pkg/cortexdb/memory_list_all_test.go`

The existing query orders `m.created_at DESC, m.id` (ascending id within a
timestamp). The keyset predicate must match that mixed direction exactly:

```sql
(m.created_at < :ts) OR (m.created_at = :ts AND m.id > :id)
```

`ListAllMemories` filters expired records **after** the SQL read
(`memoryExpired`), so a SQL `LIMIT n` can yield fewer than `n` kept rows. The
page loop must over-fetch until it has `limit` kept records or the table is
exhausted, and `NextCursor` must be the key of the last row **scanned**, not
the last row kept — otherwise expired rows get rescanned forever.

- [ ] **Step 1: Write the failing test**

Append to `pkg/cortexdb/memory_list_all_test.go`:

```go
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
```

Add `"fmt"` to that file's imports.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./pkg/cortexdb -run TestMemoryListAll -v
```

Expected: FAIL to compile — `unknown field Cursor in struct literal`.

- [ ] **Step 3: Write minimal implementation**

Replace the whole body of `pkg/cortexdb/memory_list_all.go` below the package
doc comment with:

```go
// MemoryListAllRequest asks for a page of stored memories.
type MemoryListAllRequest struct {
	// Limit caps how many records come back (0 = defaultMemoryListLimit).
	Limit int `json:"limit,omitempty"`
	// Cursor resumes a walk after the row a previous page stopped on. Empty
	// starts from the newest record. The value is opaque; pass back exactly
	// what NextCursor gave.
	Cursor string `json:"cursor,omitempty"`
}

// MemoryListAllResponse carries a page and how to get the next one.
type MemoryListAllResponse struct {
	Memories []MemoryRecord `json:"memories"`
	// Truncated is true when more records remain. It never stands alone:
	// whenever it is set, NextCursor says how to continue. A listing that
	// admitted it stopped and offered no way onward is what stranded a brain
	// that had grown past one message.
	Truncated bool `json:"truncated,omitempty"`
	// NextCursor is the resume point for the next page, set whenever
	// Truncated is.
	NextCursor string `json:"next_cursor,omitempty"`
}

// defaultMemoryListLimit is deliberately well under what one gRPC message
// carries. It used to be 5000 while the 4 MiB transport gave out near 1100 —
// the default was a promise the transport could not keep. The cursor, not a
// large limit, is how a caller gets everything.
const defaultMemoryListLimit = 500

// ListAllMemoriesPaged returns one page of memories, newest first.
func (db *DB) ListAllMemoriesPaged(ctx context.Context, req MemoryListAllRequest) (*MemoryListAllResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultMemoryListLimit
	}

	var (
		afterTS time.Time
		afterID string
		resume  bool
	)
	if req.Cursor != "" {
		ts, id, err := decodeListingCursor(req.Cursor)
		if err != nil {
			return nil, err
		}
		afterTS, afterID, resume = ts, id, true
	}

	resp := &MemoryListAllResponse{Memories: []MemoryRecord{}}
	for len(resp.Memories) <= limit {
		// Over-fetch by one so a full page can tell "exactly enough" from
		// "there is more", without a second query.
		batch, lastTS, lastID, err := db.listMemoryPage(ctx, afterTS, afterID, resume, limit+1)
		if err != nil {
			return nil, err
		}
		for _, rec := range batch {
			if memoryExpired(rec) {
				continue
			}
			resp.Memories = append(resp.Memories, rec)
		}
		if len(batch) < limit+1 {
			// The table is exhausted; whatever survived filtering is the rest.
			if len(resp.Memories) > limit {
				resp.Memories = resp.Memories[:limit]
				resp.Truncated = true
				last := resp.Memories[limit-1]
				resp.NextCursor = encodeListingCursor(last.CreatedAt, last.ID)
			}
			return resp, nil
		}
		// Resume from the last row *scanned*, not the last kept: expired rows
		// must not be rescanned on every page.
		afterTS, afterID, resume = lastTS, lastID, true
	}

	resp.Memories = resp.Memories[:limit]
	resp.Truncated = true
	last := resp.Memories[limit-1]
	resp.NextCursor = encodeListingCursor(last.CreatedAt, last.ID)
	return resp, nil
}

// listMemoryPage reads one SQL page in the listing's order, returning the rows
// and the sort key of the last one scanned.
func (db *DB) listMemoryPage(ctx context.Context, afterTS time.Time, afterID string, resume bool, limit int) ([]MemoryRecord, time.Time, string, error) {
	const base = `
		SELECT m.id, m.session_id, s.user_id, m.role, m.content, m.metadata, m.created_at
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE m.session_id LIKE 'memory:%'`
	// The listing orders created_at DESC but id ASC, so the keyset predicate
	// has to match that mixed direction exactly or a page boundary that lands
	// inside one timestamp will skip or repeat the rest of it.
	const tail = `
		ORDER BY m.created_at DESC, m.id
		LIMIT ?`

	var (
		rows *sql.Rows
		err  error
	)
	if resume {
		rows, err = db.query(ctx, base+`
		  AND (m.created_at < ? OR (m.created_at = ? AND m.id > ?))`+tail,
			afterTS, afterTS, afterID, limit)
	} else {
		rows, err = db.query(ctx, base+tail, limit)
	}
	if err != nil {
		return nil, time.Time{}, "", fmt.Errorf("list memories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		out    []MemoryRecord
		lastTS time.Time
		lastID string
	)
	for rows.Next() {
		var record MemoryRecord
		var metadataJSON []byte
		var createdAt time.Time
		if err := rows.Scan(&record.ID, &record.SessionID, &record.UserID, &record.Role, &record.Content, &metadataJSON, &createdAt); err != nil {
			return nil, time.Time{}, "", fmt.Errorf("scan memory: %w", err)
		}
		record.CreatedAt = createdAt
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
				return nil, time.Time{}, "", fmt.Errorf("decode memory metadata: %w", err)
			}
		}
		applyMemoryMetadata(&record)
		if record.Scope == "" {
			record.Scope = scopeFromBucketID(record.SessionID)
		}
		out = append(out, record)
		lastTS, lastID = createdAt, record.ID
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, "", err
	}
	return out, lastTS, lastID, nil
}
```

Set the imports of that file to:

```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)
```

(`db.query` is declared in `pkg/cortexdb/sql_exec.go:26` as
`func (db *DB) query(ctx context.Context, q string, args ...any) (*sql.Rows, error)`.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./pkg/cortexdb -run TestMemoryListAll -v
```

Expected: PASS, including the pre-existing `TestMemoryListAllToolReturnsEveryMemory`.

- [ ] **Step 5: Run the whole package to catch callers**

```bash
go test ./pkg/cortexdb 2>&1 | tail -20
```

Expected: PASS. If a test asserted the old 5000 default, update it to 500 — that
change is deliberate and documented in the spec.

- [ ] **Step 6: Commit**

```bash
git add pkg/cortexdb/memory_list_all.go pkg/cortexdb/memory_list_all_test.go
git commit -m "The memory listing can say how to continue"
```

---

## Task 3: The graph listing gets a cursor without losing its core

**Files:**
- Modify: `pkg/cortexdb/graph_list_all.go`
- Test: `pkg/cortexdb/graph_list_all_test.go` (create)

Cursor mode walks nodes in `id` order and returns, on each page, the edges
whose `from` endpoint is on that page — so a complete walk yields every node
once and every edge once. Without a cursor the behaviour is untouched:
degree-ranked core, self-consistent subgraph, no `NextCursor`.

- [ ] **Step 1: Write the failing test**

Create `pkg/cortexdb/graph_list_all_test.go`:

```go
package cortexdb

import (
	"context"
	"fmt"
	"testing"
)

func seedGraphChain(t *testing.T, db *DB, n int) {
	t.Helper()
	ctx := context.Background()
	tools := db.GraphRAGTools()
	ents := make([]ToolEntityInput, 0, n)
	for i := 0; i < n; i++ {
		ents = append(ents, ToolEntityInput{Name: fmt.Sprintf("node%02d", i), Type: "Thing"})
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: ents}); err != nil {
		t.Fatalf("seed entities: %v", err)
	}
	rels := make([]ToolRelationInput, 0, n-1)
	for i := 0; i+1 < n; i++ {
		rels = append(rels, ToolRelationInput{
			From: fmt.Sprintf("node%02d", i),
			To:   fmt.Sprintf("node%02d", i+1),
			Type: "links_to",
		})
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: rels}); err != nil {
		t.Fatalf("seed relations: %v", err)
	}
}

func TestGraphListAllWithoutCursorKeepsTheConnectedCore(t *testing.T) {
	// The HTML view depends on this: a big graph is readable because the most
	// connected nodes are what survive truncation. A cursor must not quietly
	// replace that with an id-ordered slice.
	ctx := context.Background()
	db := listAllTestDB(t)
	seedGraphChain(t, db, 12)

	resp, err := db.ListGraphAll(ctx, GraphListAllRequest{Limit: 4})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !resp.Truncated {
		t.Fatal("want Truncated with limit 4")
	}
	if resp.NextCursor != "" {
		t.Errorf("degree-ranked mode must not offer a cursor, got %q", resp.NextCursor)
	}
	for i := 1; i < len(resp.Nodes); i++ {
		if resp.Nodes[i-1].Degree < resp.Nodes[i].Degree {
			t.Fatalf("nodes are not degree-ranked: %+v", resp.Nodes)
		}
	}
}

func TestGraphListAllCursorWalksEveryNodeOnce(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	const total = 12
	seedGraphChain(t, db, total)

	seenNodes := map[string]int{}
	seenEdges := map[string]int{}
	cursor := ""
	for i := 0; i < 20; i++ {
		resp, err := db.ListGraphAll(ctx, GraphListAllRequest{Limit: 5, Cursor: cursor, Order: "id"})
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		for _, n := range resp.Nodes {
			seenNodes[n.ID]++
		}
		for _, e := range resp.Edges {
			seenEdges[e.From+"->"+e.To+":"+e.Type]++
		}
		if !resp.Truncated {
			if resp.NextCursor != "" {
				t.Errorf("final page offered a cursor")
			}
			break
		}
		if resp.NextCursor == "" {
			t.Fatal("Truncated with no NextCursor")
		}
		cursor = resp.NextCursor
	}

	if len(seenNodes) != total {
		t.Errorf("walked %d of %d nodes", len(seenNodes), total)
	}
	for id, n := range seenNodes {
		if n != 1 {
			t.Errorf("node %s seen %d times", id, n)
		}
	}
	if len(seenEdges) != total-1 {
		t.Errorf("walked %d of %d edges", len(seenEdges), total-1)
	}
	for e, n := range seenEdges {
		if n != 1 {
			t.Errorf("edge %s seen %d times", e, n)
		}
	}
}

func TestGraphListAllCursorIsIdOrdered(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	seedGraphChain(t, db, 8)

	resp, err := db.ListGraphAll(ctx, GraphListAllRequest{Limit: 8, Order: "id"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := 1; i < len(resp.Nodes); i++ {
		if resp.Nodes[i-1].ID >= resp.Nodes[i].ID {
			t.Fatalf("not id-ordered at %d: %q then %q", i, resp.Nodes[i-1].ID, resp.Nodes[i].ID)
		}
	}
}
```

(These are the types the `upsert_entities` / `upsert_relations` tools decode
into — `ToolEntityInput`, `ToolUpsertEntitiesRequest`, `ToolRelationInput` and
`ToolUpsertRelationsRequest`, all declared in
`pkg/cortexdb/graphrag_tool_types.go:70-105`.)

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./pkg/cortexdb -run TestGraphListAll -v
```

Expected: FAIL to compile — `unknown field Cursor`, `unknown field Order`.

- [ ] **Step 3: Write minimal implementation**

In `pkg/cortexdb/graph_list_all.go`, extend the request and response:

```go
// GraphListAllRequest asks for the whole meaningful entity graph.
type GraphListAllRequest struct {
	// Limit caps how many nodes come back (0 = defaultGraphListLimit).
	Limit int `json:"limit,omitempty"`
	// Cursor resumes an id-ordered walk. Supplying it implies Order "id".
	Cursor string `json:"cursor,omitempty"`
	// Order selects what a page means. "" (default) keeps the most-connected
	// core, which is what makes a large graph renderable. "id" walks the graph
	// in a stable order so a caller can read all of it.
	//
	// These are different operations, not a flag on one: degree ranking and a
	// resumable walk cannot share a page boundary.
	Order string `json:"order,omitempty"`
}
```

Add to `GraphListAllResponse`:

```go
	// NextCursor is the resume point for the next page. Only set in id order —
	// the degree-ranked core has no stable boundary to resume from.
	NextCursor string `json:"next_cursor,omitempty"`
```

Then, at the top of `ListGraphAll`, branch before the existing body:

```go
	if req.Cursor != "" || req.Order == "id" {
		return db.listGraphPageByID(ctx, req)
	}
```

And add the walk below `ListGraphAll`:

```go
// listGraphPageByID walks the entity graph in id order so a caller can read all
// of it. Each page carries the edges whose `from` endpoint is on that page, so
// a complete walk yields every node once and every edge once.
func (db *DB) listGraphPageByID(ctx context.Context, req GraphListAllRequest) (*GraphListAllResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultGraphListLimit
	}
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, err
	}

	afterID := ""
	if req.Cursor != "" {
		_, id, err := decodeListingCursor(req.Cursor)
		if err != nil {
			return nil, err
		}
		afterID = id
	}

	// Degree still comes from the whole edge set: a node's degree is a property
	// of the graph, not of the page it landed on.
	raw, degree, err := db.listGraphEdgesAndDegree(ctx)
	if err != nil {
		return nil, err
	}

	var (
		rows *sql.Rows
		qErr error
	)
	const nodeBase = `SELECT id, COALESCE(content,''), COALESCE(node_type,'')
		 FROM graph_nodes WHERE node_type != 'chunk'`
	if afterID != "" {
		rows, qErr = db.query(ctx, nodeBase+` AND id > ? ORDER BY id LIMIT ?`, afterID, limit+1)
	} else {
		rows, qErr = db.query(ctx, nodeBase+` ORDER BY id LIMIT ?`, limit+1)
	}
	if qErr != nil {
		return nil, qErr
	}
	defer func() { _ = rows.Close() }()

	page := make([]GraphListAllNode, 0, limit)
	more := false
	for rows.Next() {
		var id, content, ntype string
		if err := rows.Scan(&id, &content, &ntype); err != nil {
			return nil, err
		}
		if len(page) == limit {
			more = true
			break
		}
		label := content
		if label == "" {
			label = trimGraphNodePrefix(id)
		}
		page = append(page, GraphListAllNode{ID: id, Label: label, Type: ntype, Degree: degree[id]})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	onPage := make(map[string]struct{}, len(page))
	for _, n := range page {
		onPage[n.ID] = struct{}{}
	}
	edges := make([]GraphListAllEdge, 0)
	for _, e := range raw {
		if _, ok := onPage[e.from]; !ok {
			continue
		}
		edges = append(edges, GraphListAllEdge{From: e.from, To: e.to, Type: e.etype})
	}

	total, err := db.countGraphEntityNodes(ctx)
	if err != nil {
		return nil, err
	}
	resp := &GraphListAllResponse{Nodes: page, Edges: edges, TotalNodes: total}
	if more && len(page) > 0 {
		resp.Truncated = true
		resp.NextCursor = encodeListingCursor(time.Time{}, page[len(page)-1].ID)
	}
	return resp, nil
}

// countGraphEntityNodes counts the non-chunk nodes, so a paged caller can say
// what fraction it is holding.
func (db *DB) countGraphEntityNodes(ctx context.Context) (int, error) {
	row := db.queryRow(ctx, `SELECT COUNT(*) FROM graph_nodes WHERE node_type != 'chunk'`)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
```

Both modes need the same edge scan, so lift it out of `ListGraphAll` rather than
writing it twice — one definition of "which edges are meaningful". Move the
`rawEdge` type to package scope and add:

```go
// rawEdge is one meaningful edge, before it is narrowed to a page.
type rawEdge struct{ from, etype, to string }

// listGraphEdgesAndDegree reads every meaningful edge and the degree it gives
// each node. Degree is a property of the graph, not of the page a node lands
// on, so both listing modes compute it over the whole edge set.
func (db *DB) listGraphEdgesAndDegree(ctx context.Context) ([]rawEdge, map[string]int, error) {
	edgeRows, err := db.query(ctx,
		`SELECT e.from_node_id, COALESCE(e.edge_type,''), e.to_node_id
		 FROM graph_edges e
		 JOIN graph_nodes f ON f.id = e.from_node_id
		 JOIN graph_nodes t ON t.id = e.to_node_id
		 WHERE e.edge_type != 'has_chunk'
		   AND COALESCE(f.node_type,'') != 'chunk'
		   AND COALESCE(t.node_type,'') != 'chunk'
		 ORDER BY e.from_node_id, e.to_node_id, e.edge_type`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = edgeRows.Close() }()

	raw := make([]rawEdge, 0)
	degree := make(map[string]int)
	for edgeRows.Next() {
		var e rawEdge
		if err := edgeRows.Scan(&e.from, &e.etype, &e.to); err != nil {
			return nil, nil, err
		}
		raw = append(raw, e)
		degree[e.from]++
		degree[e.to]++
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, err
	}
	return raw, degree, nil
}
```

Then replace the edge-reading block at the top of `ListGraphAll` (its local
`type rawEdge`, the `edgeRows` query, and the scan loop that fills `raw` and
`degree`) with:

```go
	raw, degree, err := db.listGraphEdgesAndDegree(ctx)
	if err != nil {
		return nil, err
	}
```

Add `"database/sql"` and `"time"` to the file's imports. `db.queryRow` exists
(`pkg/cortexdb/sql_exec.go:30`, returning `*sql.Row`).

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./pkg/cortexdb -run TestGraphListAll -v
```

Expected: PASS, 3 tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/graph_list_all.go pkg/cortexdb/graph_list_all_test.go
git commit -m "The graph can be walked, or ranked, and says which it did"
```

---

## Task 4: The tool schemas say the cursor exists

**Files:**
- Modify: `pkg/cortexdb/knowledge_memory_tooldefs.go`
- Test: `pkg/cortexdb/knowledge_memory_tooldefs_test.go` (create if absent)

An agent only knows what the schema tells it. A cursor the schema does not
mention is a cursor nobody uses — which is the state `--export-memory` was in.

- [ ] **Step 1: Write the failing test**

Create `pkg/cortexdb/knowledge_memory_tooldefs_test.go` (or append):

```go
package cortexdb

import "testing"

func TestBulkListingToolsAdvertiseTheirCursor(t *testing.T) {
	want := map[string]bool{"memory_list_all": false, "graph_list_all": false}
	for _, def := range ToolDefinitions() {
		if _, ok := want[def.Name]; !ok {
			continue
		}
		schema, _ := def.InputSchema["properties"].(map[string]any)
		if schema == nil {
			t.Fatalf("%s has no properties", def.Name)
		}
		if _, ok := schema["cursor"]; !ok {
			t.Errorf("%s does not advertise a cursor, so no caller will use one", def.Name)
		}
		want[def.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s missing from ToolDefinitions()", name)
		}
	}
}

func TestToolCountIsUnchangedByTheCursorWork(t *testing.T) {
	// The cursor adds arguments, not tools. If this moves, something
	// unintended was registered.
	if got := len(ToolDefinitions()); got != toolCount {
		t.Fatalf("tool count %d, want %d", got, toolCount)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./pkg/cortexdb -run 'TestBulkListingTools|TestToolCountIsUnchanged' -v
```

Expected: FAIL — `memory_list_all does not advertise a cursor`.

- [ ] **Step 3: Write minimal implementation**

In `pkg/cortexdb/knowledge_memory_tooldefs.go`, replace the `memory_list_all`
definition's properties with:

```go
				map[string]any{
					"limit":  map[string]any{"type": "integer", "description": "Maximum records in this page (default 500)."},
					"cursor": map[string]any{"type": "string", "description": "Resume point from a previous page's next_cursor. Omit for the first page."},
				},
```

and its description with:

```go
			Description: "List stored memories, newest first, one page at a time. When the response sets truncated it also returns next_cursor — pass it back to get the rest. For dashboards and exports that need the whole set; use memory_search to find specific memories.",
```

Replace the `graph_list_all` properties with:

```go
				map[string]any{
					"limit":  map[string]any{"type": "integer", "description": "Maximum nodes in this page (default 2000)."},
					"cursor": map[string]any{"type": "string", "description": "Resume point from a previous page's next_cursor. Supplying it implies order \"id\"."},
					"order":  map[string]any{"type": "string", "description": "\"\" (default) returns the most-connected core, best for rendering. \"id\" walks the whole graph in a stable order, resumable with cursor."},
				},
```

and its description with:

```go
			Description: "List the entity knowledge graph — every non-chunk node and the edges between them. By default returns the most-connected core, which is what makes a large graph renderable; pass order \"id\" with a cursor to walk all of it. Use expand_graph or find_nodes when you already know where to start.",
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./pkg/cortexdb -run 'TestBulkListingTools|TestToolCountIsUnchanged' -v
go test ./pkg/cortexdb
```

Expected: PASS. `toolCount` stays 75.

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/knowledge_memory_tooldefs.go pkg/cortexdb/knowledge_memory_tooldefs_test.go
git commit -m "A cursor the schema does not mention is a cursor nobody uses"
```

---

## Task 5: `--export-memory` loops the cursor

**Files:**
- Modify: `cmd/cortexdb-mcp-stdio/remote_bulk.go:32` (`fetchAllMemoriesRemote`)

This is the task that turns the fix into a working backup of the shared brain.

- [ ] **Step 1: Read the current function**

```bash
sed -n '27,80p' cmd/cortexdb-mcp-stdio/remote_bulk.go
```

Note how it marshals `MemoryListAllRequest{Limit: limit}`, calls `CallTool`, and
unmarshals one `MemoryListAllResponse`.

- [ ] **Step 2: Rewrite it to page**

Replace the single call with a loop. Keep the existing dial, timeout and error
wrapping exactly as they are; only the request/response handling changes:

```go
// fetchAllMemoriesRemote pulls every memory from the shared brain, one page at
// a time. A single call cannot do it: a brain past a few thousand records
// exceeds the 4 MiB gRPC message limit, and the failure is total — this is the
// mode that produces a backup file, so returning part of one silently would be
// worse than an error.
func fetchAllMemoriesRemote(ctx context.Context, addr, token string, limit int) ([]cortexdb.MemoryRecord, error) {
	conn, err := dialCortexDB(addr, token)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	const pageSize = 500
	var (
		all    []cortexdb.MemoryRecord
		cursor string
	)
	for page := 0; ; page++ {
		if page > 10_000 {
			return nil, fmt.Errorf("memory export did not terminate after %d pages", page)
		}
		args, err := json.Marshal(cortexdb.MemoryListAllRequest{Limit: pageSize, Cursor: cursor})
		if err != nil {
			return nil, err
		}

		callCtx, cancel := context.WithTimeout(ctx, remoteDialTimeout)
		resp, err := rpcv1.NewToolsServiceClient(conn).CallTool(callCtx, &rpcv1.CallToolRequest{
			Name:     "memory_list_all",
			ArgsJson: string(args),
		})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("memory_list_all on %s: %w", addr, err)
		}

		var out cortexdb.MemoryListAllResponse
		if err := json.Unmarshal([]byte(resp.GetResultJson()), &out); err != nil {
			return nil, fmt.Errorf("decode memory_list_all from %s: %w", addr, err)
		}
		all = append(all, out.Memories...)

		if limit > 0 && len(all) >= limit {
			return all[:limit], nil
		}
		if !out.Truncated {
			return all, nil
		}
		if out.NextCursor == "" {
			// An older server: it truncated and cannot say where to resume.
			return nil, fmt.Errorf(
				"memory_list_all on %s stopped at %d records without a cursor; "+
					"this server predates paged listings and cannot export a brain this size",
				addr, len(all))
		}
		cursor = out.NextCursor
	}
}
```

Two things this deliberately drops from the old version:

- **`(is the server new enough?)` on the error.** It blames a version for a
  payload-size failure. The new code says what actually happened, and the
  old-server case gets its own message where it is genuinely true.
- **The `note: the listing was truncated; pass a higher limit to get everything`
  warning.** That advice cannot work — a higher limit is exactly what exceeds
  the message size. The loop replaces it: there is nothing left to warn about.

- [ ] **Step 3: Build**

```bash
go build ./cmd/cortexdb-mcp-stdio
```

Expected: no output.

- [ ] **Step 4: Verify against a local brain**

```bash
cd /tmp && rm -rf cdbexport && \
  env -u CORTEXDB_REMOTE CORTEXDB_PATH=~/.cortexdb/cortexdb.db \
  ~/Things/AI/base/CortexDB/cortexdb-mcp-stdio --export-memory ./cdbexport
ls cdbexport | wc -l
```

Expected: a non-zero file count and no error. (Local mode does not exercise the
cursor loop, but it proves the export path still works.)

- [ ] **Step 5: Verify against the shared brain — the actual goal**

This needs a server carrying Tasks 2–4. Once the cluster is upgraded:

```bash
cd /tmp && rm -rf cdbshared && \
  ~/Things/AI/base/CortexDB/cortexdb-mcp-stdio --export-memory ./cdbshared
ls cdbshared | wc -l
```

Expected: roughly 1783 files. Before this change the same command wrote 0 and
failed with `ResourceExhausted ... (5121407 vs. 4194304)`.

- [ ] **Step 6: Commit**

```bash
git add cmd/cortexdb-mcp-stdio/remote_bulk.go
git commit -m "The export reads the brain in pages, so it can finish"
```

---

## Task 6: Documentation sync

**Files:**
- Modify: `README.md`, `README_CN.md`, `SKILL.md`

`CLAUDE.md` requires these three stay in sync for non-trivial changes.

- [ ] **Step 1: Find what needs updating**

```bash
grep -n 'memory_list_all\|graph_list_all' README.md README_CN.md SKILL.md
```

- [ ] **Step 2: Update each hit**

Every description of the two tools should say they are paged. Where the tool
list mentions `memory_list_all`, add: "paged — `truncated` always comes with a
`next_cursor`". Where `graph_list_all` is described, say the default returns the
most-connected core and `order: "id"` walks the whole graph.

- [ ] **Step 3: Verify the three agree**

```bash
grep -c 'next_cursor' README.md README_CN.md SKILL.md
```

Expected: a non-zero count from all three.

- [ ] **Step 4: Commit**

```bash
git add README.md README_CN.md SKILL.md
git commit -m "Say that the listings page"
```

---

## Task 7: Full gate

- [ ] **Step 1: Build everything**

```bash
cd ~/Things/AI/base/CortexDB
go build ./...
```

Expected: no output.

- [ ] **Step 2: Race-test the whole repo**

```bash
go test -race ./... 2>&1 | grep -Ev '^ok |no test files' | head -30
```

Expected: nothing but a trailing empty result. `go build ./...` and
`go test -race ./...` are CI's only gates; there is no separate lint step.

- [ ] **Step 3: Examples still compile**

```bash
for dir in examples/*/; do (cd "$dir" && go build -o /dev/null .) || echo "FAIL $dir"; done
```

Expected: no `FAIL` lines.

- [ ] **Step 4: Commit any fixes**

```bash
git commit -am "Close what the full gate found"
```

---

## Out of scope for this plan

- `cmd/cortexdb`, the operator CLI. It is the second half of the spec and gets
  its own plan once this lands — it depends on these cursors to read a brain at
  all, and its `gc` plan file cannot be written for records it cannot read.
- Deleting the 562 transcript memories in the shared brain. Blocked on this
  plan for exactly that reason.
- `cortexdb-mcp-stdio` closing on stdin EOF before answering queued requests.
  A real defect found in the same audit, unrelated to paging.
- The misleading `(is the server new enough?)` hint on a `ResourceExhausted`
  error, which blames a version for a payload-size failure.
