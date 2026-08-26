package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// An edge is identified by what it connects and what it means. Before this, the identity included the
// relation's index within the request, so a caller re-sending the same graph — a learning trace pushed
// again each turn — wrote a new row every time. One store held 102,855 edges of which 61,197 were
// distinct, and a single edge had been written 292 times.
func TestUpsertRelationsIsIdempotent(t *testing.T) {
	db, tools, ctx := relationTestStore(t)

	mustHaveEntities(t, tools, ctx, "Learner", "Roman History", "The Republic")
	relations := []ToolRelationInput{
		{From: "Learner", To: "Roman History", Type: "LEARNING"},
		{From: "Roman History", To: "The Republic", Type: "ABOUT"},
	}

	for round := 0; round < 5; round++ {
		if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: relations}); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}

	if got := countEdges(t, db, ctx); got != 2 {
		t.Fatalf("five pushes of the same two relations left %d edges, want 2", got)
	}
}

// Order is not identity: the same edge sent in a different position is the same edge.
func TestUpsertRelationsIgnoresPositionInTheRequest(t *testing.T) {
	db, tools, ctx := relationTestStore(t)

	mustHaveEntities(t, tools, ctx, "A", "B", "C", "D", "E", "F")
	first := []ToolRelationInput{
		{From: "A", To: "B", Type: "NEXT_STEP"},
		{From: "C", To: "D", Type: "NEXT_STEP"},
	}
	// Same two edges, swapped, with a third in between — which is what an extra turn of a lesson does
	// to a trace that is rebuilt and re-sent whole.
	second := []ToolRelationInput{
		{From: "C", To: "D", Type: "NEXT_STEP"},
		{From: "E", To: "F", Type: "NEXT_STEP"},
		{From: "A", To: "B", Type: "NEXT_STEP"},
	}

	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: first}); err != nil {
		t.Fatalf("first push: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: second}); err != nil {
		t.Fatalf("second push: %v", err)
	}

	if got := countEdges(t, db, ctx); got != 3 {
		t.Fatalf("got %d edges, want 3 — reordering must not create edges", got)
	}
}

// Two documents that state the same relation keep one edge each: collapsing them would silently drop
// one document's provenance, and provenance is the reason the edge is worth having.
func TestUpsertRelationsKeepsOneEdgePerDocument(t *testing.T) {
	db, tools, ctx := relationTestStore(t)

	mustHaveEntities(t, tools, ctx, "Raft", "Consensus")
	same := []ToolRelationInput{{From: "Raft", To: "Consensus", Type: "IS_A"}}
	for _, document := range []string{"book:ddia", "book:tanenbaum", "book:ddia"} {
		if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
			DocumentID: document,
			Relations:  same,
		}); err != nil {
			t.Fatalf("%s: %v", document, err)
		}
	}

	if got := countEdges(t, db, ctx); got != 2 {
		t.Fatalf("got %d edges, want 2 — one per document, and re-indexing a book must not add one", got)
	}
}

// A repeat inside a single request is one edge, and the chunk ids of both are kept. The batch writer
// runs the rows in order, so without merging the later one would overwrite the earlier one's chunk ids
// and the provenance would be gone with nothing reported.
func TestUpsertRelationsMergesChunkIDsOfARepeatedEdge(t *testing.T) {
	_, tools, ctx := relationTestStore(t)

	mustHaveEntities(t, tools, ctx, "Alice", "Acme")
	resp, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "doc",
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "WORKS_AT", ChunkIDs: []string{"chunk-1"}},
			{From: "Alice", To: "Acme", Type: "WORKS_AT", ChunkIDs: []string{"chunk-2"}},
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if len(resp.EdgeIDs) != 2 || resp.EdgeIDs[0] != resp.EdgeIDs[1] {
		t.Fatalf("both relations should report the same edge id, got %v", resp.EdgeIDs)
	}

	chunks := chunkIDsOfEdge(t, tools, ctx, resp.EdgeIDs[0])
	if len(chunks) != 2 {
		t.Fatalf("edge kept %v, want both chunk-1 and chunk-2", chunks)
	}
}

func relationTestStore(t *testing.T) (*DB, *GraphRAGToolbox, context.Context) {
	t.Helper()
	path := fmt.Sprintf("test_relation_identity_%d.db", time.Now().UnixNano())
	db, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(path)
	})
	return db, db.GraphRAGTools(), context.Background()
}

// The store rejects an edge whose endpoints are not nodes, so the entities come first — which is also
// what every real caller does.
func mustHaveEntities(t *testing.T, tools *GraphRAGToolbox, ctx context.Context, names ...string) {
	t.Helper()
	entities := make([]ToolEntityInput, 0, len(names))
	for _, name := range names {
		entities = append(entities, ToolEntityInput{Name: name})
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
		t.Fatalf("create entities: %v", err)
	}
}

func countEdges(t *testing.T, db *DB, ctx context.Context) int {
	t.Helper()
	var count int
	// Counted in the store rather than from the response: the response says what was asked for, and
	// what this is about is what was written.
	if err := db.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_edges").Scan(&count); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	return count
}

func chunkIDsOfEdge(t *testing.T, tools *GraphRAGToolbox, ctx context.Context, edgeID string) []string {
	t.Helper()
	var properties string
	if err := tools.db.SQL().QueryRowContext(ctx,
		"SELECT COALESCE(properties, '') FROM graph_edges WHERE id = ?", edgeID).Scan(&properties); err != nil {
		t.Fatalf("read edge %s: %v", edgeID, err)
	}
	var decoded struct {
		ChunkIDs []string `json:"chunk_ids"`
	}
	if err := json.Unmarshal([]byte(properties), &decoded); err != nil {
		t.Fatalf("decode properties %q: %v", properties, err)
	}
	return decoded.ChunkIDs
}

// A relation endpoint names an entity, and the name may belong to more than one
// node. The store held a prose entity "Snapshot" and, from a code graph in the
// same database, a Go type of that name — two nodes, different ids, identical
// content. Resolution took the first by id, which is deterministic and wrong:
// it attached what a runbook says about snapshots to a struct.
//
// The ontology already says which of them the domain is about. An endpoint
// resolves to a node whose type the schema declares, before one it does not.
func TestRelationEndpointPrefersADeclaredObjectType(t *testing.T) {
	db, tools, ctx := relationTestStore(t)

	idProp := []OntologyProperty{{
		APIName: "id", DisplayName: "Node id", Required: true,
		DataType: OntologyDataType{Kind: OntologyDataString},
	}}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Activate: true, Schema: OntologySchema{
		SchemaID: "test", Name: "test", Enforcement: OntologyEnforcementVocabulary,
		ObjectTypes: []OntologyObjectType{
			{APIName: "Snapshot", DisplayName: "Snapshot", PrimaryKey: "id", Properties: idProp},
			{APIName: "Volume", DisplayName: "Volume", PrimaryKey: "id", Properties: idProp},
		},
	}}); err != nil {
		t.Fatalf("save ontology: %v", err)
	}

	// The undeclared node sorts first by id, which is what used to decide it.
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{ID: "aaa-pkg-rbac-snapshot", Name: "Snapshot", Type: "class"},
		{Name: "Snapshot", Type: "Snapshot"},
		{Name: "Volume", Type: "Volume"},
	}}); err != nil {
		t.Fatalf("create entities: %v", err)
	}

	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "runbook",
		Relations:  []ToolRelationInput{{From: "Snapshot", To: "Volume", Type: "protects"}},
	}); err != nil {
		t.Fatalf("upsert relations: %v", err)
	}

	var fromType string
	if err := db.SQL().QueryRowContext(ctx, `
		SELECT n.node_type FROM graph_edges e JOIN graph_nodes n ON n.id = e.from_node_id
		WHERE e.edge_type = 'protects'`).Scan(&fromType); err != nil {
		t.Fatalf("read edge: %v", err)
	}
	if fromType != "Snapshot" {
		t.Errorf("edge starts at a %q, want the declared Snapshot", fromType)
	}
}
