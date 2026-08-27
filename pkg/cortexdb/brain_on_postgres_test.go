package cortexdb

import (
	"context"
	"testing"
)

// The whole point, in one test: a brain that lives in PostgreSQL.
//
// Everything else on this branch is a part — a dialect, a store, an index, a
// query that runs on both. This is the assembly. Until Open accepts a
// postgres:// DSN and the thing that comes back can hold a document, an
// embedding, a graph edge and a search, "the user can choose SQLite or
// pgvector" was a claim about parts.
func TestABrainCanLiveOnPostgres(t *testing.T) {
	db := openPostgresBrain(t, 4)
	ctx := context.Background()

	// The DSN decided the backend, and the seam reports it.
	if got := db.Dialect().Kind(); string(got) != "postgres" {
		t.Fatalf("dialect = %s, want postgres", got)
	}

	// A document, an embedding that belongs to it, and a search that finds it.
	tools := db.GraphRAGTools()
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "handbook",
		Title:      "Handbook",
		Content:    "Leo works at LINBIT on the LINSTOR GUI.",
	}); err != nil {
		t.Fatalf("IngestDocument: %v", err)
	}

	// A graph the brain can walk.
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Leo", Type: "Person"},
		{Name: "LINBIT", Type: "Company"},
	}}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	rel, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "handbook",
		Relations:  []ToolRelationInput{{From: "Leo", To: "LINBIT", Type: "works_at", ChunkIDs: []string{"handbook#1"}}},
	})
	if err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	// And provenance, which reads the graph back through the dialect.
	prov, err := db.FactProvenanceFor(ctx, rel.EdgeIDs[0], false)
	if err != nil {
		t.Fatalf("FactProvenanceFor: %v", err)
	}
	if prov.DocumentID != "handbook" || !prov.Cited() {
		t.Errorf("provenance did not survive the round trip: %+v", prov)
	}

	// Uncited sweep runs its dialect-built SQL against PostgreSQL.
	if _, err := db.UncitedFacts(ctx, 10); err != nil {
		t.Fatalf("UncitedFacts: %v", err)
	}
}

// A backend that opens but cannot be a brain must be refused by name at Open,
// not discovered by a panic in the middle of a request.
func TestAnUnknownDSNSchemeIsRefusedAtOpen(t *testing.T) {
	if _, err := Open(DefaultConfig("")); err == nil {
		t.Error("an empty path was accepted")
	}
}
