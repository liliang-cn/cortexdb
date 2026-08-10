package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type typedFixtureExtractor struct{}

func (typedFixtureExtractor) Extract(_ context.Context, text string) (*GraphExtraction, error) {
	lower := strings.ToLower(text)
	extraction := &GraphExtraction{}
	if strings.Contains(lower, "alice") {
		extraction.Entities = append(extraction.Entities, GraphEntity{Name: "Alice", Type: "person"})
	}
	if strings.Contains(lower, "acme") {
		extraction.Entities = append(extraction.Entities, GraphEntity{Name: "Acme", Type: "organization"})
	}
	if strings.Contains(lower, "alice works at acme") {
		extraction.Relationships = append(extraction.Relationships, GraphRelationship{
			From: "Alice",
			To:   "Acme",
			Type: "works_at",
		})
	}
	return extraction, nil
}

// TestOntologySchemaAPIAndToolValidation checks that the active schema is
// enforced through the tool surface an agent actually calls, not only through
// the validators underneath it.
func TestOntologySchemaAPIAndToolValidation(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport"},
	}}); err == nil || !strings.Contains(err.Error(), "iataCode") {
		t.Fatalf("expected upsert_entities to reject a missing required property, got %v", err)
	}

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
		{Name: "Gatwick", Type: "Airport", Metadata: map[string]string{"iataCode": "LGW"}},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}

	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "London Heathrow", To: "Gatwick", Type: "flightDeparture"},
	}}); err == nil || !strings.Contains(err.Error(), "connects Airport and Flight") {
		t.Fatalf("expected upsert_relations to reject mistyped endpoints, got %v", err)
	}
}

// TestInsertGraphDocumentRespectsActiveOntologySchema covers the extraction
// path: entity types come from an extractor rather than the caller, so it is
// the one write path where nothing upstream has already checked them.
func TestInsertGraphDocumentRespectsActiveOntologySchema(t *testing.T) {
	dbPath := fmt.Sprintf("test_ontology_extract_%d.db", time.Now().UnixNano())
	db, err := Open(DefaultConfig(dbPath), WithEmbedder(newKeywordEmbedder("alice", "acme", "works")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	ctx := context.Background()
	activateAviationSchema(t, db)

	// The extractor emits person/organization, which the aviation schema does
	// not define.
	_, err = db.InsertGraphDocument(ctx, GraphRAGDocument{
		ID:      "doc-ontology",
		Title:   "Alice at Acme",
		Content: "Alice works at Acme.",
	}, GraphRAGIngestOptions{ChunkSize: 10, Extractor: typedFixtureExtractor{}})
	if err == nil || !strings.Contains(err.Error(), `does not define object type "organization"`) {
		t.Fatalf("expected extracted entity types to be checked against the schema, got %v", err)
	}
}
