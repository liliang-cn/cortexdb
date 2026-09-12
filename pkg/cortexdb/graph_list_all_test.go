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
