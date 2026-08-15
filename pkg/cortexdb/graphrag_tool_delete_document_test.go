package cortexdb

import (
	"context"
	"strings"
	"testing"
)

// upsertProseEntities writes entities the way an external ingest pipeline does:
// chunks embedded outside the graph, chunk ids referenced by mention edges only.
func upsertProseEntities(t *testing.T, db *DB, docID string, names []string, chunkID string) {
	t.Helper()
	entities := make([]ToolEntityInput, 0, len(names))
	for _, name := range names {
		entities = append(entities, ToolEntityInput{
			Name:     name,
			Type:     "entity",
			ChunkIDs: []string{chunkID},
		})
	}
	if _, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{
		DocumentID: docID,
		Entities:   entities,
	}); err != nil {
		t.Fatalf("upsert entities for %s: %v", docID, err)
	}
}

func TestUpsertEntitiesRecordsProvenanceAndStubsChunks(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	// The chunk was never written as a graph node — the caller embedded it in
	// its own collection. Mention edges used to die on the foreign key here.
	upsertProseEntities(t, db, "guide.md", []string{"DRBD"}, "guide.md#0")

	entityID := EntityNodeID("DRBD")
	node, err := db.Graph().GetNode(ctx, entityID)
	if err != nil || node == nil {
		t.Fatalf("entity node missing: %v", err)
	}
	sources := toStringSlice(node.Properties["source_document_ids"])
	if len(sources) != 1 || sources[0] != "guide.md" {
		t.Fatalf("expected provenance [guide.md], got %v", sources)
	}

	chunk, err := db.Graph().GetNode(ctx, "guide.md#0")
	if err != nil || chunk == nil {
		t.Fatalf("chunk stub missing: %v", err)
	}
	if chunk.NodeType != "chunk" {
		t.Fatalf("stub node type = %q, want chunk", chunk.NodeType)
	}
	if chunk.Properties["document_id"] != "guide.md" {
		t.Fatalf("stub document_id = %v", chunk.Properties["document_id"])
	}

	var mentions int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE edge_type = 'mentions' AND to_node_id = ?`,
		entityID).Scan(&mentions); err != nil {
		t.Fatal(err)
	}
	if mentions != 1 {
		t.Fatalf("expected 1 mention edge, got %d", mentions)
	}
}

func TestUpsertEntitiesUnionsProvenanceAcrossDocuments(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	upsertProseEntities(t, db, "a.md", []string{"DRBD"}, "a.md#0")
	upsertProseEntities(t, db, "b.md", []string{"DRBD"}, "b.md#0")

	node, err := db.Graph().GetNode(ctx, EntityNodeID("DRBD"))
	if err != nil || node == nil {
		t.Fatalf("entity node missing: %v", err)
	}
	sources := toStringSlice(node.Properties["source_document_ids"])
	if len(sources) != 2 {
		t.Fatalf("expected both documents in provenance, got %v", sources)
	}
}

func TestDeleteDocumentGraphDeletesOwnedAndDetachesShared(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	// "DRBD" is asserted by both documents; "LINSTOR" only by a.md.
	upsertProseEntities(t, db, "a.md", []string{"DRBD", "LINSTOR"}, "a.md#0")
	upsertProseEntities(t, db, "b.md", []string{"DRBD"}, "b.md#0")

	dry, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{
		DocumentID: "a.md", DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.EntityNodesDeleted != 1 || dry.EntityNodesDetached != 1 || dry.ChunkNodesDeleted != 1 {
		t.Fatalf("dry run counts wrong: %+v", dry)
	}
	// A dry run must not have removed anything.
	if node, _ := db.Graph().GetNode(ctx, EntityNodeID("LINSTOR")); node == nil {
		t.Fatal("dry run deleted LINSTOR")
	}

	resp, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "a.md"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if resp.EntityNodesDeleted != 1 || resp.EntityNodesDetached != 1 || resp.ChunkNodesDeleted != 1 {
		t.Fatalf("delete counts wrong: %+v", resp)
	}

	if node, _ := db.Graph().GetNode(ctx, EntityNodeID("LINSTOR")); node != nil {
		t.Fatal("LINSTOR should be gone: a.md was its only source")
	}
	if node, _ := db.Graph().GetNode(ctx, "a.md#0"); node != nil {
		t.Fatal("a.md chunk stub should be gone")
	}
	shared, _ := db.Graph().GetNode(ctx, EntityNodeID("DRBD"))
	if shared == nil {
		t.Fatal("DRBD should survive: b.md still asserts it")
	}
	sources := toStringSlice(shared.Properties["source_document_ids"])
	if len(sources) != 1 || sources[0] != "b.md" {
		t.Fatalf("DRBD provenance after detach = %v, want [b.md]", sources)
	}

	// b.md's footprint is untouched.
	if node, _ := db.Graph().GetNode(ctx, "b.md#0"); node == nil {
		t.Fatal("b.md chunk should be untouched")
	}
}

func TestDeleteDocumentGraphRemovesRelationEdges(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	upsertProseEntities(t, db, "a.md", []string{"DRBD", "LINSTOR"}, "a.md#0")
	if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "a.md",
		Relations:  []ToolRelationInput{{From: "DRBD", To: "LINSTOR", Type: "manages"}},
	}); err != nil {
		t.Fatalf("upsert relations: %v", err)
	}

	resp, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "a.md"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if resp.RelationEdgesDeleted != 1 {
		t.Fatalf("expected 1 relation edge deleted, got %d", resp.RelationEdgesDeleted)
	}

	var edges int
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_edges`).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 0 {
		t.Fatalf("expected an empty edge table, got %d rows", edges)
	}
}

func TestDeleteDocumentGraphRequiresDocumentID(t *testing.T) {
	db := openOntologyTestDB(t)
	_, err := db.GraphRAGTools().DeleteDocumentGraph(context.Background(), ToolDeleteDocumentGraphRequest{})
	if err == nil || !strings.Contains(err.Error(), "document_id") {
		t.Fatalf("expected document_id error, got %v", err)
	}
}
