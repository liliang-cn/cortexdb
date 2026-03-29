package cortexdb

import (
	"context"
	"encoding/json"
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

func TestOntologySchemaAPIAndToolValidation(t *testing.T) {
	dbPath := fmt.Sprintf("test_ontology_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	tools := db.GraphRAGTools()

	saveResp, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		SchemaID: "people-orgs",
		Name:     "People and Organizations",
		Activate: true,
		EntityTypes: []OntologyEntityType{
			{Name: "entity"},
			{Name: "person", RequiredProperties: []string{"role"}},
			{Name: "organization"},
		},
		RelationTypes: []OntologyRelationType{
			{Name: "works_at", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"organization"}},
		},
	})
	if err != nil {
		t.Fatalf("save ontology schema: %v", err)
	}
	if !saveResp.Schema.Active {
		t.Fatal("expected ontology schema to be active")
	}
	if saveResp.Schema.Version != 1 {
		t.Fatalf("expected version 1, got %d", saveResp.Schema.Version)
	}

	listResp, err := db.ListOntologySchemas(ctx, OntologyListRequest{ActiveOnly: true})
	if err != nil {
		t.Fatalf("list ontology schemas: %v", err)
	}
	if len(listResp.Schemas) != 1 || listResp.Schemas[0].SchemaID != "people-orgs" {
		t.Fatalf("unexpected active ontology list: %+v", listResp.Schemas)
	}

	getResp, err := tools.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "people-orgs"})
	if err != nil {
		t.Fatalf("get ontology schema: %v", err)
	}
	if len(getResp.Schema.EntityTypes) != 3 {
		t.Fatalf("expected 3 entity types, got %d", len(getResp.Schema.EntityTypes))
	}

	ontologyPayload, err := json.Marshal(OntologySaveRequest{
		SchemaID: "people-orgs",
		EntityTypes: []OntologyEntityType{
			{Name: "entity"},
			{Name: "person", RequiredProperties: []string{"role"}},
			{Name: "organization"},
		},
		RelationTypes: []OntologyRelationType{
			{Name: "works_at", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"organization"}},
		},
	})
	if err != nil {
		t.Fatalf("marshal ontology payload: %v", err)
	}
	dispatched, err := tools.Call(ctx, "ontology_save", ontologyPayload)
	if err != nil {
		t.Fatalf("dispatch ontology_save: %v", err)
	}
	savedViaDispatch, ok := dispatched.(*OntologySaveResponse)
	if !ok {
		t.Fatalf("unexpected ontology dispatch response type: %T", dispatched)
	}
	if savedViaDispatch.Schema.Version != 2 {
		t.Fatalf("expected version 2 after update, got %d", savedViaDispatch.Schema.Version)
	}

	ingestResp, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "doc-ontology",
		Title:      "Alice at Acme",
		Content:    "Alice works at Acme on ontology testing.",
		ChunkSize:  16,
	})
	if err != nil {
		t.Fatalf("ingest document: %v", err)
	}

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "doc-ontology",
		Entities: []ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{ingestResp.ChunkNodeIDs[0]}, Metadata: map[string]string{"role": "engineer"}},
			{Name: "Acme", Type: "organization", ChunkIDs: []string{ingestResp.ChunkNodeIDs[0]}},
		},
	}); err != nil {
		t.Fatalf("upsert typed entities: %v", err)
	}

	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "doc-ontology",
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at"},
		},
	}); err != nil {
		t.Fatalf("upsert typed relation: %v", err)
	}

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "doc-ontology",
		Entities: []ToolEntityInput{
			{Name: "Beta", Type: "person", ChunkIDs: []string{ingestResp.ChunkNodeIDs[0]}},
		},
	}); err == nil || !strings.Contains(err.Error(), "required property role") {
		t.Fatalf("expected required property validation error, got %v", err)
	}

	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "doc-ontology",
		Relations: []ToolRelationInput{
			{From: "Acme", To: "Alice", Type: "works_at"},
		},
	}); err == nil || !strings.Contains(err.Error(), "does not allow source entity type organization") {
		t.Fatalf("expected relation direction validation error, got %v", err)
	}

	deleteResp, err := db.DeleteOntologySchema(ctx, OntologyDeleteRequest{SchemaID: "people-orgs"})
	if err != nil {
		t.Fatalf("delete ontology schema: %v", err)
	}
	if !deleteResp.Deleted {
		t.Fatal("expected ontology schema delete confirmation")
	}
}

func TestInsertGraphDocumentRespectsActiveOntologySchema(t *testing.T) {
	dbPath := fmt.Sprintf("test_ontology_embedder_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath), WithEmbedder(newKeywordEmbedder("alice", "acme", "works", "ontology")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		SchemaID: "typed-graphrag",
		Activate: true,
		EntityTypes: []OntologyEntityType{
			{Name: "entity"},
			{Name: "person"},
			{Name: "organization"},
		},
		RelationTypes: []OntologyRelationType{
			{Name: "works_at", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"organization"}},
		},
	}); err != nil {
		t.Fatalf("save ontology schema: %v", err)
	}

	if _, err := db.InsertGraphDocument(ctx, GraphRAGDocument{
		ID:      "doc-good",
		Title:   "Alice at Acme",
		Content: "Alice works at Acme.",
	}, GraphRAGIngestOptions{
		ChunkSize: 16,
		Extractor: typedFixtureExtractor{},
	}); err != nil {
		t.Fatalf("insert typed graph document: %v", err)
	}

	_, err = db.InsertGraphDocument(ctx, GraphRAGDocument{
		ID:      "doc-bad",
		Title:   "Alice at Acme",
		Content: "Alice works at Acme.",
	}, GraphRAGIngestOptions{
		ChunkSize: 16,
		Extractor: fixtureExtractor{},
	})
	if err == nil || !strings.Contains(err.Error(), "source entity type entity") {
		t.Fatalf("expected ontology validation failure for untyped extractor, got %v", err)
	}
}
