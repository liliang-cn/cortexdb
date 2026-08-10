package cortexdb

import (
	"context"
	"errors"
	"strings"
	"testing"
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
