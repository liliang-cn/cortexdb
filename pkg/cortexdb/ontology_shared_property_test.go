package cortexdb

import (
	"strings"
	"testing"
)

func sharedPropertySchema() OntologySchema {
	return OntologySchema{
		SchemaID: "shared",
		Name:     "Shared properties",
		SharedProperties: []OntologyProperty{
			{
				APIName:     "position",
				DisplayName: "Position",
				Description: "WGS84 location",
				DataType:    OntologyDataType{Kind: OntologyDataGeoPoint},
			},
		},
		ObjectTypes: []OntologyObjectType{
			{
				APIName:    "Airport",
				PrimaryKey: "iataCode",
				Properties: []OntologyProperty{
					{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					// No data_type: resolved from the shared property.
					{APIName: "position", Required: true},
				},
			},
			{
				APIName:    "Plant",
				PrimaryKey: "plantCode",
				Properties: []OntologyProperty{
					{APIName: "plantCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "position"},
				},
			},
		},
	}
}

func TestSharedPropertyIsResolvedIntoObjectTypes(t *testing.T) {
	if err := validateOntologySchema(sharedPropertySchema()); err != nil {
		t.Fatalf("expected the shared property to resolve, got %v", err)
	}

	compiled := compileOntology(sharedPropertySchema())
	property, ok := compiled.property("Airport", "position")
	if !ok {
		t.Fatal("expected position to resolve on Airport")
	}
	if property.DataType.Kind != OntologyDataGeoPoint {
		t.Fatalf("expected the shared data type, got %q", property.DataType.Kind)
	}
	if property.Description != "WGS84 location" {
		t.Fatalf("expected shared metadata to carry over, got %q", property.Description)
	}
}

func TestSharedPropertyKeepsPerObjectTypeRequiredFlag(t *testing.T) {
	compiled := compileOntology(sharedPropertySchema())

	airportPosition, _ := compiled.property("Airport", "position")
	if !airportPosition.Required {
		t.Fatal("Airport declares position required; the local flag must win")
	}
	plantPosition, _ := compiled.property("Plant", "position")
	if plantPosition.Required {
		t.Fatal("Plant leaves position optional; the local flag must win")
	}
}

func TestLocalDataTypeOverridesSharedProperty(t *testing.T) {
	schema := sharedPropertySchema()
	schema.ObjectTypes[0].Properties[1].DataType = OntologyDataType{Kind: OntologyDataString}

	compiled := compileOntology(schema)
	property, _ := compiled.property("Airport", "position")
	if property.DataType.Kind != OntologyDataString {
		t.Fatalf("an explicit local data type must win, got %q", property.DataType.Kind)
	}
}

func TestUnresolvedPropertyWithoutDataTypeIsRejected(t *testing.T) {
	schema := sharedPropertySchema()
	schema.SharedProperties = nil

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "position") {
		t.Fatalf("a property with no data type and no shared definition must be rejected, got %v", err)
	}
	// Misspelling a shared property name is the likely cause, so the message
	// has to point at both possibilities rather than at an empty kind.
	if !strings.Contains(err.Error(), "data_type.kind") || !strings.Contains(err.Error(), "shared property") {
		t.Fatalf("message should name data_type.kind and shared property, got %v", err)
	}
}

func TestSharedPropertyItselfIsValidated(t *testing.T) {
	schema := sharedPropertySchema()
	schema.SharedProperties[0].DataType = OntologyDataType{Kind: OntologyDataVector} // no dimension

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("an invalid shared property must be rejected")
	}
}

func TestDuplicateSharedPropertiesRejected(t *testing.T) {
	schema := sharedPropertySchema()
	schema.SharedProperties = append(schema.SharedProperties, schema.SharedProperties[0])

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("duplicate shared property api names must be rejected")
	}
}
