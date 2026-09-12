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

// TestGraphListAllCursorWalksEveryNodeOnce (12 nodes, limit 5) never lands on
// an exact multiple, and TestGraphListAllCursorIsIdOrdered (8 nodes, limit 8)
// is a single page. Neither would catch a regression that marks a true final
// page Truncated just because it happens to be exactly `limit` long — that
// only shows up when the node count divides evenly by the limit.
func TestGraphListAllCursorFinalPageOnExactMultipleIsNotTruncated(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	const total, limit = 10, 5 // total == 2*limit
	seedGraphChain(t, db, total)

	seen := map[string]bool{}
	cursor := ""
	var last *GraphListAllResponse
	for i := 0; i < 10; i++ {
		resp, err := db.ListGraphAll(ctx, GraphListAllRequest{Limit: limit, Cursor: cursor, Order: "id"})
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		for _, n := range resp.Nodes {
			seen[n.ID] = true
		}
		last = resp
		if !resp.Truncated {
			break
		}
		cursor = resp.NextCursor
	}
	if len(seen) != total {
		t.Fatalf("walked %d of %d nodes before reaching a final page", len(seen), total)
	}
	if last.Truncated {
		t.Errorf("final page on an exact multiple of the limit was marked Truncated")
	}
	if last.NextCursor != "" {
		t.Errorf("final page on an exact multiple of the limit offered a cursor: %q", last.NextCursor)
	}
	if len(last.Nodes) != limit {
		t.Errorf("want the final page to hold exactly %d nodes, got %d", limit, len(last.Nodes))
	}
}

func TestGraphListAllRejectsAMalformedCursor(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	if _, err := db.ListGraphAll(ctx, GraphListAllRequest{Cursor: "not-a-cursor!!"}); err == nil {
		t.Fatal("accepted a malformed cursor")
	}
}

// Order is a free string with exactly two meaningful values, "" and "id".
// Anything else must error rather than silently fall through to
// degree-ranked mode: that mode never sets NextCursor, so a caller who typo'd
// "order" would be told Truncated with no way to resume — the exact failure
// this whole cursor mechanism exists to eliminate, one field over.

func TestGraphListAllEmptyOrderStillDegreeRanks(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	seedGraphChain(t, db, 12)

	resp, err := db.ListGraphAll(ctx, GraphListAllRequest{Limit: 4, Order: ""})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !resp.Truncated {
		t.Fatal("want Truncated with limit 4")
	}
	if resp.NextCursor != "" {
		t.Errorf("degree-ranked mode must not offer a cursor, got %q", resp.NextCursor)
	}
}

func TestGraphListAllOrderIdStillWalks(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	seedGraphChain(t, db, 12)

	resp, err := db.ListGraphAll(ctx, GraphListAllRequest{Limit: 4, Order: "id"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !resp.Truncated {
		t.Fatal("want Truncated with limit 4")
	}
	if resp.NextCursor == "" {
		t.Error("id-walk mode must offer a cursor when truncated")
	}
}

func TestGraphListAllRejectsAnUnrecognizedOrder(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)
	seedGraphChain(t, db, 12)

	for _, order := range []string{"ID", "asc", "by-id", "Id"} {
		t.Run(order, func(t *testing.T) {
			resp, err := db.ListGraphAll(ctx, GraphListAllRequest{Limit: 4, Order: order})
			if err == nil {
				t.Fatalf("accepted order %q: got Truncated=%v NextCursor=%q instead of an error",
					order, resp.Truncated, resp.NextCursor)
			}
		})
	}
}

// graph_list_all and memory_list_all both take an opaque `cursor` string
// described the same way, so an agent mixing them up is a realistic mistake.
// A cursor from one listing fed into the other must error loudly rather than
// return an empty page that looks like "nothing left" (the memory side) or
// silently filter by the wrong ordering (the graph side) — exactly the kind
// of silent data loss this whole feature exists to prevent.
func TestListingCursorsAreNotInterchangeable(t *testing.T) {
	ctx := context.Background()
	db := listAllTestDB(t)

	for _, id := range []string{"a", "b", "c"} {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Content: "记忆 " + id, Scope: "global"}); err != nil {
			t.Fatalf("seed memory %s: %v", id, err)
		}
	}
	seedGraphChain(t, db, 4)

	memResp, err := db.ListAllMemoriesPaged(ctx, MemoryListAllRequest{Limit: 1})
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	if memResp.NextCursor == "" {
		t.Fatal("test needs a real memory cursor to cross-feed")
	}

	graphResp, err := db.ListGraphAll(ctx, GraphListAllRequest{Limit: 1, Order: "id"})
	if err != nil {
		t.Fatalf("list graph: %v", err)
	}
	if graphResp.NextCursor == "" {
		t.Fatal("test needs a real graph cursor to cross-feed")
	}

	if _, err := db.ListGraphAll(ctx, GraphListAllRequest{Cursor: memResp.NextCursor}); err == nil {
		t.Error("graph listing silently accepted a memory cursor")
	}
	if _, err := db.ListAllMemoriesPaged(ctx, MemoryListAllRequest{Cursor: graphResp.NextCursor}); err == nil {
		t.Error("memory listing silently accepted a graph cursor")
	}
}
