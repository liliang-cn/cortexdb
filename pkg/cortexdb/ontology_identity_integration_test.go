package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUpsertEntitiesUsesPrimaryKeyIdentity(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	// Same airport, two different spellings of the name, one primary key.
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Heathrow Airport", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("expected a node at the primary-key ID: %v", err)
	}
	if node.NodeType != "Airport" {
		t.Fatalf("expected node type Airport, got %q", node.NodeType)
	}

	// Two names, one object: the second write must not have created a
	// second node.
	var count int
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_nodes WHERE node_type = 'Airport'`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected primary key to dedupe to 1 node, got %d", count)
	}
}

func TestUpsertEntitiesReportsPrimaryKeyNodeIDs(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()

	resp, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Callers wire mentions and relations off the returned IDs, so reporting
	// the legacy ID while writing the ontology one would silently misdirect them.
	if len(resp.EntityNodeIDs) != 1 || resp.EntityNodeIDs[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected the primary-key node ID to be reported, got %v", resp.EntityNodeIDs)
	}
}

func TestUpsertEntitiesKeepsLegacyIdentityWithoutSchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "airport"},
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// With no active ontology the old name-derived ID must still be used,
	// so existing graphs keep resolving.
	if _, err := db.graph.GetNode(ctx, graphEntityNodeID("London Heathrow")); err != nil {
		t.Fatalf("expected legacy node ID to be preserved: %v", err)
	}
}

func TestUpsertRelationsWritesEdgesAtPrimaryKeyIdentity(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
		{Name: "BA117", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA117"}},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
	}}); err != nil {
		t.Fatalf("upsert relations: %v", err)
	}

	// An edge landing on a name-derived ID would dangle: no node lives there.
	var count int
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE from_node_id = ? AND to_node_id = ?`,
		ontologyNodeID("Airport", "LHR"), ontologyNodeID("Flight", "BA117"))
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one edge between the primary-key nodes, got %d", count)
	}
}

func TestUpsertRelationsRejectsUnknownEndpointNameWithActiveSchema(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}

	// A name alone carries no primary key, so an object that has not been
	// created yet cannot be invented from one.
	_, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
	}})
	if err == nil {
		t.Fatal("expected an unknown endpoint name to be rejected")
	}
}

func TestOntologyRelationEndpointNodeIDRejectsAnUnknownName(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	initGraphSchemaForTest(t, db)
	ctx := context.Background()

	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// The write path relies on this refusing rather than returning an empty
	// node ID, which would write an edge dangling off "".
	if _, err := db.ontologyRelationEndpointNodeID(ctx, compiled, "Nowhere"); err == nil {
		t.Fatal("expected an unknown endpoint name to be refused")
	}

	// Without a schema the same call is the legacy name-derived ID.
	nodeID, err := db.ontologyRelationEndpointNodeID(ctx, nil, "Nowhere")
	if err != nil || nodeID != graphEntityNodeID("Nowhere") {
		t.Fatalf("expected the legacy node ID with no ontology, got %q / %v", nodeID, err)
	}
}

func TestOntologyRelationEndpointPrefersEntityNodesOverChunks(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	// A chunk whose text is exactly the entity's name. Its node ID sorts
	// before "entity:", so an unrestricted lookup would pick the chunk.
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "doc-heathrow",
		Content:    "London Heathrow",
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
		{Name: "BA117", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA117"}},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}

	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	nodeID, err := db.ontologyRelationEndpointNodeID(ctx, compiled, "London Heathrow")
	if err != nil {
		t.Fatalf("resolve endpoint: %v", err)
	}
	if nodeID != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected the entity node, got %q", nodeID)
	}
}

func TestValidateExtractedGraphDataRejectsNodeIDDisagreeingWithItsType(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	initGraphSchemaForTest(t, db)

	// The ID says Flight, the type says Airport. Reading the primary key out
	// of an ID whose object type segment does not match would invent one.
	err := db.validateExtractedGraphData(context.Background(),
		map[string]GraphEntity{"entity:flight:ba117": {Name: "BA117", Type: "Airport"}},
		nil)
	if err == nil {
		t.Fatal("expected a node ID that contradicts its object type to be rejected")
	}
}

func openOntologyKnowledgeTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := fmt.Sprintf("test_ontology_knowledge_%d.db", time.Now().UnixNano())
	db, err := Open(DefaultConfig(dbPath), WithEmbedder(newKeywordEmbedder("ba117", "departs", "heathrow")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	return db
}

// activateAviationSchemaWithFreeformEntities adds a catch-all object and link
// type to the aviation schema. SaveKnowledge always runs its built-in
// heuristic extractor, whose output is untyped, so a schema that models only
// aviation would reject every SaveKnowledge call rather than the relation
// under test.
func activateAviationSchemaWithFreeformEntities(t *testing.T, db *DB) {
	t.Helper()
	schema := validAviationSchema()
	schema.ObjectTypes = append(schema.ObjectTypes, OntologyObjectType{
		APIName:    "entity",
		PrimaryKey: "name",
		Properties: []OntologyProperty{
			{APIName: "name", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
	})
	schema.LinkTypes = append(schema.LinkTypes, OntologyLinkType{
		APIName: "related_to",
		A:       OntologyLinkSide{APIName: "relatesTo", ObjectTypeAPIName: "entity", Cardinality: OntologyCardinalityMany},
		B:       OntologyLinkSide{APIName: "relatedFrom", ObjectTypeAPIName: "entity", Cardinality: OntologyCardinalityMany},
	})
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate schema: %v", err)
	}
}

func TestSaveKnowledgeWritesEntitiesAtPrimaryKeyIdentity(t *testing.T) {
	db := openOntologyKnowledgeTestDB(t)
	activateAviationSchemaWithFreeformEntities(t, db)
	ctx := context.Background()

	// Entities and the relation between them arrive in one request, so the
	// endpoints name objects that do not exist in the graph yet.
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "k-aviation",
		Title:       "BA117 departs Heathrow",
		Content:     "BA117 departs Heathrow.",
		ChunkSize:   24,
		Entities: []ToolEntityInput{
			{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
			{Name: "BA117", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA117"}},
		},
		Relations: []ToolRelationInput{
			{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
		},
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}

	if _, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR")); err != nil {
		t.Fatalf("expected the airport at its primary-key ID: %v", err)
	}
	var count int
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE from_node_id = ? AND to_node_id = ?`,
		ontologyNodeID("Airport", "LHR"), ontologyNodeID("Flight", "BA117"))
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one edge between the primary-key nodes, got %d", count)
	}
}

func TestSaveKnowledgeLinksEntitiesCreatedByAnEarlierRequest(t *testing.T) {
	db := openOntologyKnowledgeTestDB(t)
	activateAviationSchemaWithFreeformEntities(t, db)
	ctx := context.Background()

	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "k-objects",
		Title:       "Objects",
		Content:     "Heathrow and BA117.",
		ChunkSize:   24,
		Entities: []ToolEntityInput{
			{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
			{Name: "BA117", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA117"}},
		},
	}); err != nil {
		t.Fatalf("save objects: %v", err)
	}

	// A later request names the endpoints only. They are not in this batch, so
	// they have to be found in the graph — and the edge must land on the nodes
	// validation checked, not on name-derived IDs where nothing lives.
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "k-link",
		Title:       "Departure",
		Content:     "BA117 departs Heathrow.",
		ChunkSize:   24,
		Relations: []ToolRelationInput{
			{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
		},
	}); err != nil {
		t.Fatalf("save relation: %v", err)
	}

	var count int
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE from_node_id = ? AND to_node_id = ?`,
		ontologyNodeID("Airport", "LHR"), ontologyNodeID("Flight", "BA117"))
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one edge between the primary-key nodes, got %d", count)
	}
}

func TestSaveKnowledgeRejectsRelationsThatBreakTheSchema(t *testing.T) {
	db := openOntologyKnowledgeTestDB(t)
	activateAviationSchemaWithFreeformEntities(t, db)

	_, err := db.SaveKnowledge(context.Background(), KnowledgeSaveRequest{
		KnowledgeID: "k-bad",
		Title:       "Two airports",
		Content:     "Heathrow and Gatwick.",
		ChunkSize:   24,
		Entities: []ToolEntityInput{
			{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
			{Name: "Gatwick", Type: "Airport", Metadata: map[string]string{"iataCode": "LGW"}},
		},
		Relations: []ToolRelationInput{
			{From: "London Heathrow", To: "Gatwick", Type: "flightDeparture"},
		},
	})
	// Named explicitly: an endpoint that merely failed to resolve would also
	// be an error, and would hide the fact that the type check never ran.
	if err == nil || !strings.Contains(err.Error(), "connects Airport and Flight") {
		t.Fatalf("expected Airport->Airport on a flightDeparture link to be rejected, got %v", err)
	}
}

func TestUpsertRelationsKeepsLegacyEndpointIdentityWithoutSchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "airport"},
		{Name: "BA117", Type: "flight"},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "departs"},
	}}); err != nil {
		t.Fatalf("upsert relations: %v", err)
	}

	var count int
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE from_node_id = ? AND to_node_id = ?`,
		graphEntityNodeID("London Heathrow"), graphEntityNodeID("BA117"))
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected the legacy name-derived edge to be preserved, got %d", count)
	}
}
