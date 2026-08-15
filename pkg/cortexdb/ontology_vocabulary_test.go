package cortexdb

import (
	"context"
	"strings"
	"testing"
)

func vocabularyAviationSchema() OntologySchema {
	schema := validAviationSchema()
	schema.Enforcement = OntologyEnforcementVocabulary
	return schema
}

func activateVocabularyAviationSchema(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{
		Schema:   vocabularyAviationSchema(),
		Activate: true,
	}); err != nil {
		t.Fatalf("activate vocabulary schema: %v", err)
	}
}

func TestVocabularySchemaAcceptsEntityWithoutPrimaryKey(t *testing.T) {
	db := openOntologyTestDB(t)
	activateVocabularyAviationSchema(t, db)
	ctx := context.Background()

	// An Airport with no iataCode: strict enforcement refuses this, which is
	// what forced extraction pipelines to leave schemas inactive.
	resp, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "prose.md",
		Entities:   []ToolEntityInput{{Name: "London Heathrow", Type: "Airport"}},
	})
	if err != nil {
		t.Fatalf("vocabulary schema refused a keyless entity: %v", err)
	}
	if len(resp.EntityNodeIDs) != 1 {
		t.Fatalf("expected 1 entity, got %v", resp.EntityNodeIDs)
	}
	node, err := db.Graph().GetNode(ctx, resp.EntityNodeIDs[0])
	if err != nil || node == nil {
		t.Fatalf("entity not written: %v", err)
	}
	// The declared spelling still wins: canonicalization is the point of
	// keeping the schema active.
	if node.NodeType != "Airport" {
		t.Fatalf("node type = %q, want Airport", node.NodeType)
	}
}

func TestVocabularySchemaAcceptsUndeclaredType(t *testing.T) {
	db := openOntologyTestDB(t)
	activateVocabularyAviationSchema(t, db)

	resp, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{Name: "Voyager", Type: "Spacecraft"}},
	})
	if err != nil {
		t.Fatalf("vocabulary schema refused an undeclared type: %v", err)
	}
	node, err := db.Graph().GetNode(context.Background(), resp.EntityNodeIDs[0])
	if err != nil || node == nil {
		t.Fatalf("entity not written: %v", err)
	}
	if node.NodeType != "Spacecraft" {
		t.Fatalf("undeclared type should pass through, got %q", node.NodeType)
	}
}

func TestVocabularySchemaStillKeysTypedEntities(t *testing.T) {
	db := openOntologyTestDB(t)
	activateVocabularyAviationSchema(t, db)

	// With the primary key supplied, the typed ID is used — same node either
	// enforcement mode, so flipping a schema to strict later does not fork ids.
	resp, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}}},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	want := ontologyNodeID("Airport", "LHR")
	if len(resp.EntityNodeIDs) != 1 || resp.EntityNodeIDs[0] != want {
		t.Fatalf("entity id = %v, want %s", resp.EntityNodeIDs, want)
	}
}

func TestVocabularySchemaAcceptsUndeclaredRelations(t *testing.T) {
	db := openOntologyTestDB(t)
	activateVocabularyAviationSchema(t, db)
	ctx := context.Background()

	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{
			{Name: "DRBD", Type: "entity"},
			{Name: "LINSTOR", Type: "entity"},
		},
	}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
	resp, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{
		Relations: []ToolRelationInput{{From: "DRBD", To: "LINSTOR", Type: "manages"}},
	})
	if err != nil {
		t.Fatalf("vocabulary schema refused an undeclared link type: %v", err)
	}
	if resp.Written != 1 {
		t.Fatalf("expected the edge to be written, got %+v", resp)
	}
}

func TestStrictSchemaStillRejectsKeylessEntity(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	_, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{Name: "London Heathrow", Type: "Airport"}},
	})
	if err == nil || !strings.Contains(err.Error(), "iataCode") {
		t.Fatalf("strict schema should still reject a keyless entity, got %v", err)
	}
}

func TestSaveOntologySchemaRejectsUnknownEnforcement(t *testing.T) {
	schema := validAviationSchema()
	schema.Enforcement = "lenient"
	if err := validateOntologySchema(schema); err == nil || !strings.Contains(err.Error(), "enforcement") {
		t.Fatalf("expected unknown enforcement to be rejected, got %v", err)
	}
}

func TestSaveOntologySchemaRejectsVocabularyWithStrictActions(t *testing.T) {
	schema := vocabularyAviationSchema()
	schema.StrictActions = true
	if err := validateOntologySchema(schema); err == nil || !strings.Contains(err.Error(), "contradictory") {
		t.Fatalf("expected contradiction to be rejected, got %v", err)
	}
}

func TestEnforcementRoundTripsThroughStorage(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: vocabularyAviationSchema()}); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.Schema.Enforcement != OntologyEnforcementVocabulary {
		t.Fatalf("enforcement lost in storage round trip: %q", loaded.Schema.Enforcement)
	}
}
