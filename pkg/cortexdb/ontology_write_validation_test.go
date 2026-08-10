package cortexdb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func activateAviationSchema(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{
		Schema:   validAviationSchema(),
		Activate: true,
	}); err != nil {
		t.Fatalf("activate schema: %v", err)
	}
}

func TestValidateEntityInputsAcceptsConformingEntity(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	})
	if err != nil {
		t.Fatalf("expected conforming entity to pass, got %v", err)
	}
}

func TestValidateEntityInputsRejectsUnknownObjectType(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "Voyager", Type: "Spacecraft", Metadata: map[string]string{"iataCode": "VGR"}},
	})
	if err == nil || !strings.Contains(err.Error(), "Spacecraft") {
		t.Fatalf("expected unknown object type to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsRejectsMissingPrimaryKey(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport"},
	})
	if err == nil || !strings.Contains(err.Error(), "iataCode") {
		t.Fatalf("expected missing primary key to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsRejectsMissingRequiredProperty(t *testing.T) {
	db := openOntologyTestDB(t)

	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties[1].Required = true // airportName
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	})
	if err == nil || !strings.Contains(err.Error(), "airportName") {
		t.Fatalf("expected missing required property to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsRejectsWrongDataType(t *testing.T) {
	db := openOntologyTestDB(t)

	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties, OntologyProperty{
		APIName:  "elevation",
		DataType: OntologyDataType{Kind: OntologyDataInteger},
	})
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR", "elevation": "eighty-three"}},
	})
	if err == nil || !strings.Contains(err.Error(), "elevation") {
		t.Fatalf("expected a type violation to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsRejectsUndeclaredProperty(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR", "runwayCount": "2"}},
	})
	if err == nil || !strings.Contains(err.Error(), "runwayCount") {
		t.Fatalf("expected an undeclared property to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsNoopWithoutActiveSchema(t *testing.T) {
	db := openOntologyTestDB(t)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "Anything", Type: "Whatever"},
	})
	if err != nil {
		t.Fatalf("with no active schema all writes must pass, got %v", err)
	}
}

func TestValidateEntityInputsMatchesRequiredPropertiesCaseInsensitively(t *testing.T) {
	db := openOntologyTestDB(t)

	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties[1].Required = true // airportName
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	// API names keep their declared casing but resolve case-insensitively, so
	// a caller spelling the key differently has still supplied the property.
	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"IATACODE": "LHR", "AIRPORTNAME": "London Heathrow"}},
	})
	if err != nil {
		t.Fatalf("expected differently-cased property keys to satisfy the schema, got %v", err)
	}
}

func TestValidateEntityInputsNoopWithEmptyActiveSchema(t *testing.T) {
	db := openOntologyTestDB(t)

	// A schema that declares no types is valid and activatable. It must mean
	// "nothing is modelled yet", not "reject every write".
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{
		Schema:   OntologySchema{SchemaID: "empty"},
		Activate: true,
	}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "Anything", Type: "Whatever"},
	})
	if err != nil {
		t.Fatalf("an empty active schema must not constrain writes, got %v", err)
	}
}

func TestValidateEntityInputsRejectionsAreInvalidOntologyErrors(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	// A rejected write is bad input, not a broken server: gRPC callers see
	// InvalidArgument only if the sentinel is in the chain.
	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "Voyager", Type: "Spacecraft"},
	})
	if !errors.Is(err, ErrInvalidOntology) {
		t.Fatalf("expected an ErrInvalidOntology chain, got %v", err)
	}
}

func TestValidateEntityInputsSkipsAnonymousEntities(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	// An entity with neither name nor ID is dropped by the write path, so
	// validating it would reject a write that never happens.
	if err := db.validateEntityInputs(context.Background(), []ToolEntityInput{{Type: "Spacecraft"}}); err != nil {
		t.Fatalf("expected an anonymous entity to be skipped, got %v", err)
	}
}

func TestValidateEntityInputsChecksEveryEntity(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	// The offender is last: a loop that only checked the head would pass.
	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
		{Name: "Voyager", Type: "Spacecraft"},
	})
	if err == nil || !strings.Contains(err.Error(), "Spacecraft") {
		t.Fatalf("expected the second entity to be rejected, got %v", err)
	}
}

// initGraphSchemaForTest mirrors what every production caller of the write
// validators does before reaching them, so a test that validates without
// writing does not fail on a missing table and hide the real assertion.
func initGraphSchemaForTest(t *testing.T, db *DB) {
	t.Helper()
	if err := db.graph.InitGraphSchema(context.Background()); err != nil {
		t.Fatalf("init graph schema: %v", err)
	}
}

func upsertAviationEntities(t *testing.T, db *DB, entities ...ToolEntityInput) {
	t.Helper()
	if _, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
}

func aviationAirport(name string, iataCode string) ToolEntityInput {
	return ToolEntityInput{Name: name, Type: "Airport", Metadata: map[string]string{"iataCode": iataCode}}
}

func aviationFlight(flightNumber string) ToolEntityInput {
	return ToolEntityInput{Name: flightNumber, Type: "Flight", Metadata: map[string]string{"flightNumber": flightNumber}}
}

func TestValidateRelationInputsAcceptsConformingRelation(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	upsertAviationEntities(t, db, aviationAirport("London Heathrow", "LHR"), aviationFlight("BA117"))

	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
	})
	if err != nil {
		t.Fatalf("expected conforming relation to pass, got %v", err)
	}
}

func TestValidateRelationInputsRejectsUnknownLinkType(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	upsertAviationEntities(t, db, aviationAirport("London Heathrow", "LHR"), aviationFlight("BA117"))

	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "refuels"},
	})
	if err == nil || !strings.Contains(err.Error(), "refuels") {
		t.Fatalf("expected unknown link type to be rejected, got %v", err)
	}
}

func TestValidateRelationInputsRejectsWrongEndpointTypes(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	upsertAviationEntities(t, db, aviationAirport("London Heathrow", "LHR"), aviationAirport("Gatwick", "LGW"))

	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "London Heathrow", To: "Gatwick", Type: "flightDeparture"},
	})
	if err == nil || !strings.Contains(err.Error(), "connects Airport and Flight") {
		t.Fatalf("expected Airport->Airport on a flightDeparture link to be rejected, got %v", err)
	}
}

func TestValidateRelationInputsEnforcesOneCardinality(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	upsertAviationEntities(t, db,
		aviationAirport("London Heathrow", "LHR"),
		aviationAirport("Gatwick", "LGW"),
		aviationFlight("BA117"))

	if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
	}}); err != nil {
		t.Fatalf("first relation: %v", err)
	}

	// BA117's origin side has cardinality ONE, so a second origin airport
	// must be refused rather than silently producing two origins.
	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "Gatwick", To: "BA117", Type: "flightDeparture"},
	})
	if err == nil || !strings.Contains(err.Error(), "cardinality") {
		t.Fatalf("expected a cardinality violation, got %v", err)
	}
}

func TestValidateRelationInputsEnforcesOneCardinalityOnTheFromSide(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	upsertAviationEntities(t, db,
		aviationAirport("London Heathrow", "LHR"),
		aviationAirport("Gatwick", "LGW"),
		aviationFlight("BA117"))

	// Written Flight->Airport, so the ONE side is now the relation's source.
	if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "BA117", To: "London Heathrow", Type: "flightDeparture"},
	}}); err != nil {
		t.Fatalf("first relation: %v", err)
	}

	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "BA117", To: "Gatwick", Type: "flightDeparture"},
	})
	if err == nil || !strings.Contains(err.Error(), "cardinality") {
		t.Fatalf("expected a cardinality violation on the source side, got %v", err)
	}
}

func TestValidateRelationInputsCountsOnlyTheLinkTypeBeingChecked(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	// A second link type that also puts a ONE side on Flight. An edge of one
	// must not fill the other's slot.
	schema := validAviationSchema()
	schema.ObjectTypes = append(schema.ObjectTypes, OntologyObjectType{
		APIName:    "Airline",
		PrimaryKey: "airlineCode",
		Properties: []OntologyProperty{
			{APIName: "airlineCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
	})
	schema.LinkTypes = append(schema.LinkTypes, OntologyLinkType{
		APIName: "flightOperator",
		A:       OntologyLinkSide{APIName: "operatedFlights", ObjectTypeAPIName: "Airline", Cardinality: OntologyCardinalityMany},
		B:       OntologyLinkSide{APIName: "operator", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityOne},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	upsertAviationEntities(t, db,
		aviationAirport("London Heathrow", "LHR"),
		aviationFlight("BA117"),
		ToolEntityInput{Name: "British Airways", Type: "Airline", Metadata: map[string]string{"airlineCode": "BA"}})

	if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "British Airways", To: "BA117", Type: "flightOperator"},
	}}); err != nil {
		t.Fatalf("operator relation: %v", err)
	}

	// BA117 now has an operator, but no origin: its flightDeparture slot is free.
	if err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
	}); err != nil {
		t.Fatalf("an unrelated link type must not fill this one's ONE side, got %v", err)
	}
}

func TestValidateRelationInputsRejectsUnresolvableEndpoints(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	upsertAviationEntities(t, db, aviationAirport("London Heathrow", "LHR"), aviationFlight("BA117"))

	// Node IDs are taken at face value, so one that names nothing must be
	// refused rather than validated against an empty type.
	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "entity:airport:nowhere", To: "BA117", Type: "flightDeparture"},
	})
	if err == nil || !strings.Contains(err.Error(), "could not resolve source entity") {
		t.Fatalf("expected an unresolvable source to be rejected, got %v", err)
	}

	err = db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "London Heathrow", To: "entity:flight:nowhere", Type: "flightDeparture"},
	})
	if err == nil || !strings.Contains(err.Error(), "could not resolve target entity") {
		t.Fatalf("expected an unresolvable target to be rejected, got %v", err)
	}
}

func TestValidateRelationInputsAllowsReassertingTheSameLink(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	upsertAviationEntities(t, db, aviationAirport("London Heathrow", "LHR"), aviationFlight("BA117"))

	relations := []ToolRelationInput{{From: "London Heathrow", To: "BA117", Type: "flightDeparture"}}
	if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: relations}); err != nil {
		t.Fatalf("first relation: %v", err)
	}

	// Re-ingesting the same document must not trip cardinality on the edge it
	// wrote last time: a ONE side already filled by this very link is fine.
	if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: relations}); err != nil {
		t.Fatalf("expected re-asserting the same link to pass, got %v", err)
	}
}

func TestValidateRelationInputsIgnoresEdgeTypeCasingWhenCounting(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	upsertAviationEntities(t, db,
		aviationAirport("London Heathrow", "LHR"),
		aviationAirport("Gatwick", "LGW"),
		aviationFlight("BA117"))

	// Written with the link type spelled differently. API names resolve
	// case-insensitively, so this still occupies BA117's ONE origin slot and
	// must not let a second origin in through the back door.
	if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightdeparture"},
	}}); err != nil {
		t.Fatalf("first relation: %v", err)
	}

	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "Gatwick", To: "BA117", Type: "flightDeparture"},
	})
	if err == nil || !strings.Contains(err.Error(), "cardinality") {
		t.Fatalf("expected casing not to smuggle a second edge past cardinality, got %v", err)
	}
}

func TestValidateRelationInputsRejectsBlankEndpoints(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	upsertAviationEntities(t, db, aviationAirport("London Heathrow", "LHR"), aviationFlight("BA117"))

	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "", To: "BA117", Type: "flightDeparture"},
	})
	if err == nil || !strings.Contains(err.Error(), "relation endpoints are required") {
		t.Fatalf("expected a blank endpoint to be rejected as missing, got %v", err)
	}
}

func TestValidateExtractedGraphDataReportsTheSameOffenderEveryTime(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	initGraphSchemaForTest(t, db)
	ctx := context.Background()

	entities := map[string]GraphEntity{
		"entity:alpha:a": {Name: "A", Type: "Alpha"},
		"entity:zulu:z":  {Name: "Z", Type: "Zulu"},
	}
	// Both are undeclared. Which one is named must not depend on map order,
	// or the same bad extraction produces a different error each run.
	for i := 0; i < 20; i++ {
		err := db.validateExtractedGraphData(ctx, entities, nil)
		if err == nil || !strings.Contains(err.Error(), "Alpha") {
			t.Fatalf("run %d: expected the first offender by ID order, got %v", i, err)
		}
	}
}

func TestValidateRelationInputsNoopWithoutActiveSchema(t *testing.T) {
	db := openOntologyTestDB(t)

	err := db.validateRelationInputs(context.Background(), []ToolRelationInput{
		{From: "Anything", To: "Whatever", Type: "madeUp"},
	})
	if err != nil {
		t.Fatalf("with no active schema all writes must pass, got %v", err)
	}
}

func TestValidateRelationInputsRejectionsAreInvalidOntologyErrors(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	upsertAviationEntities(t, db, aviationAirport("London Heathrow", "LHR"), aviationFlight("BA117"))

	err := db.validateRelationInputs(context.Background(), []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "refuels"},
	})
	if !errors.Is(err, ErrInvalidOntology) {
		t.Fatalf("expected an ErrInvalidOntology chain, got %v", err)
	}
}

func TestValidateRelationInputsResolvesEndpointsFromSameBatch(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	initGraphSchemaForTest(t, db)
	ctx := context.Background()

	// Neither endpoint exists in the graph yet. v1 failed here with
	// "could not resolve"; v2 must resolve from the batch.
	err := db.validateExtractedGraphData(ctx,
		map[string]GraphEntity{
			"entity:airport:lhr":  {Name: "London Heathrow", Type: "Airport"},
			"entity:flight:ba117": {Name: "BA117", Type: "Flight"},
		},
		map[string]graph.GraphEdge{
			"e1": {FromNodeID: "entity:airport:lhr", ToNodeID: "entity:flight:ba117", EdgeType: "flightDeparture"},
		})
	if err != nil {
		t.Fatalf("expected same-batch endpoints to resolve, got %v", err)
	}
}

func TestValidateExtractedGraphDataStillRejectsMistypedEndpoints(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	initGraphSchemaForTest(t, db)
	ctx := context.Background()

	// Batch fallback supplies the missing types, it does not excuse them: two
	// airports on a flightDeparture link is still wrong.
	err := db.validateExtractedGraphData(ctx,
		map[string]GraphEntity{
			"entity:airport:lhr": {Name: "London Heathrow", Type: "Airport"},
			"entity:airport:lgw": {Name: "Gatwick", Type: "Airport"},
		},
		map[string]graph.GraphEdge{
			"e1": {FromNodeID: "entity:airport:lhr", ToNodeID: "entity:airport:lgw", EdgeType: "flightDeparture"},
		})
	if err == nil || !strings.Contains(err.Error(), "connects Airport and Flight") {
		t.Fatalf("expected Airport->Airport on a flightDeparture link to be rejected, got %v", err)
	}
}

func TestValidateExtractedGraphDataValidatesEntitiesToo(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateExtractedGraphData(context.Background(),
		map[string]GraphEntity{"entity:spacecraft:vgr": {Name: "Voyager", Type: "Spacecraft"}},
		nil)
	if err == nil || !strings.Contains(err.Error(), "Spacecraft") {
		t.Fatalf("expected the extracted entity to be validated, got %v", err)
	}
}
