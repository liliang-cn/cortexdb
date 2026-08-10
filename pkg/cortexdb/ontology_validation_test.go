package cortexdb

import (
	"errors"
	"strings"
	"testing"
)

func validAviationSchema() OntologySchema {
	return OntologySchema{
		SchemaID: "aviation",
		Name:     "Aviation",
		ObjectTypes: []OntologyObjectType{
			{
				APIName:    "Airport",
				PrimaryKey: "iataCode",
				Properties: []OntologyProperty{
					{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "airportName", DataType: OntologyDataType{Kind: OntologyDataString}},
				},
			},
			{
				APIName:    "Flight",
				PrimaryKey: "flightNumber",
				Properties: []OntologyProperty{
					{APIName: "flightNumber", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "originIata", DataType: OntologyDataType{Kind: OntologyDataString}},
				},
			},
		},
		LinkTypes: []OntologyLinkType{
			{
				APIName: "flightDeparture",
				A:       OntologyLinkSide{APIName: "departures", ObjectTypeAPIName: "Airport", Cardinality: OntologyCardinalityMany},
				B:       OntologyLinkSide{APIName: "origin", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityOne, ForeignKeyProperty: "originIata"},
			},
		},
	}
}

func TestValidateOntologySchemaAcceptsValid(t *testing.T) {
	if err := validateOntologySchema(validAviationSchema()); err != nil {
		t.Fatalf("expected valid schema, got %v", err)
	}
}

func TestValidateOntologySchemaRequiresPrimaryKey(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = ""

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected missing primary key to be rejected")
	}
	if !strings.Contains(err.Error(), "primary_key") {
		t.Fatalf("error should name primary_key, got %v", err)
	}
}

func TestValidateOntologySchemaPrimaryKeyMustBeDeclaredProperty(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = "notAProperty"

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected unknown primary key property to be rejected")
	}
	if !strings.Contains(err.Error(), "notAProperty") {
		t.Fatalf("error should name the property, got %v", err)
	}
}

func TestValidateOntologySchemaPrimaryKeyMustBeRequired(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties[0].Required = false

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected optional primary key property to be rejected")
	}
}

func TestValidateOntologySchemaRejectsDuplicateObjectTypes(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes = append(schema.ObjectTypes, OntologyObjectType{
		APIName:    "airport",
		PrimaryKey: "iataCode",
		Properties: []OntologyProperty{{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true}},
	})

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected case-insensitive duplicate object type to be rejected")
	}
}

func TestValidateOntologySchemaLinkSideMustReferenceKnownObjectType(t *testing.T) {
	schema := validAviationSchema()
	schema.LinkTypes[0].B.ObjectTypeAPIName = "Spacecraft"

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected unknown link side object type to be rejected")
	}
	if !strings.Contains(err.Error(), "Spacecraft") {
		t.Fatalf("error should name the object type, got %v", err)
	}
}

func TestValidateOntologySchemaForeignKeyOnlyOnOneSide(t *testing.T) {
	schema := validAviationSchema()
	schema.LinkTypes[0].A.ForeignKeyProperty = "airportName"

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected foreign key on a MANY side to be rejected")
	}
}

func TestValidateOntologySchemaForeignKeyMustBeDeclaredProperty(t *testing.T) {
	schema := validAviationSchema()
	schema.LinkTypes[0].B.ForeignKeyProperty = "notAProperty"

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected unknown foreign key property to be rejected")
	}
}

func TestValidateOntologySchemaRejectsVectorWithoutDimension(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties, OntologyProperty{
		APIName:  "embedding",
		DataType: OntologyDataType{Kind: OntologyDataVector},
	})

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected vector property without dimension to be rejected")
	}
}

func TestValidateOntologySchemaTitlePropertyMustExist(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].TitleProperty = "nope"

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected unknown title property to be rejected")
	}
}

// interfaceAviationSchema adds an interface that Airport implements.
func interfaceAviationSchema() OntologySchema {
	schema := validAviationSchema()
	schema.InterfaceTypes = []OntologyInterfaceType{
		{
			APIName: "Locatable",
			Properties: []OntologyProperty{
				{APIName: "position", DataType: OntologyDataType{Kind: OntologyDataGeoPoint}},
			},
		},
	}
	schema.ObjectTypes[0].Implements = []string{"Locatable"}
	return schema
}

func TestValidateOntologySchemaAcceptsInterfaces(t *testing.T) {
	if err := validateOntologySchema(interfaceAviationSchema()); err != nil {
		t.Fatalf("expected valid interface schema, got %v", err)
	}
}

func TestValidateOntologySchemaRejectsDuplicateInterfaceTypes(t *testing.T) {
	schema := interfaceAviationSchema()
	schema.InterfaceTypes = append(schema.InterfaceTypes, OntologyInterfaceType{APIName: "locatable"})

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected case-insensitive duplicate interface type to be rejected")
	}
	if !strings.Contains(err.Error(), "duplicate interface type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOntologySchemaRejectsInterfaceCollidingWithObjectType(t *testing.T) {
	schema := interfaceAviationSchema()
	schema.InterfaceTypes[0].APIName = "airport"
	schema.ObjectTypes[0].Implements = []string{"airport"}

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected an interface named like an object type to be rejected")
	}
	if !strings.Contains(err.Error(), "collides") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOntologySchemaRejectsUnknownImplementedInterface(t *testing.T) {
	schema := interfaceAviationSchema()
	schema.ObjectTypes[0].Implements = []string{"Nonexistent"}

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected implementing an undeclared interface to be rejected")
	}
	if !strings.Contains(err.Error(), "Nonexistent") {
		t.Fatalf("error should name the interface, got %v", err)
	}
}

func TestValidateOntologySchemaRejectsInvalidInterfaceProperty(t *testing.T) {
	schema := interfaceAviationSchema()
	schema.InterfaceTypes[0].Properties[0].DataType = OntologyDataType{Kind: OntologyDataVector}

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected an invalid interface property to be rejected")
	}
}

func TestValidateOntologySchemaRejectsArrayWithoutItemType(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties, OntologyProperty{
		APIName:  "runways",
		DataType: OntologyDataType{Kind: OntologyDataArray},
	})

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected an array without item_type to be rejected")
	}
	if !strings.Contains(err.Error(), "item_type") {
		t.Fatalf("error should name item_type, got %v", err)
	}
}

func TestValidateOntologySchemaRejectsArrayWithInvalidItemType(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties, OntologyProperty{
		APIName: "embeddings",
		DataType: OntologyDataType{
			Kind:     OntologyDataArray,
			ItemType: &OntologyDataType{Kind: OntologyDataVector}, // no dimension
		},
	})

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected an array whose item type is invalid to be rejected")
	}
}

func TestValidateOntologySchemaRejectsStructWithoutFields(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties, OntologyProperty{
		APIName:  "address",
		DataType: OntologyDataType{Kind: OntologyDataStruct},
	})

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected a struct without fields to be rejected")
	}
	if !strings.Contains(err.Error(), "fields") {
		t.Fatalf("error should name fields, got %v", err)
	}
}

func TestValidateOntologySchemaRejectsStructWithInvalidField(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties, OntologyProperty{
		APIName: "address",
		DataType: OntologyDataType{
			Kind:   OntologyDataStruct,
			Fields: []OntologyProperty{{APIName: "1bad", DataType: OntologyDataType{Kind: OntologyDataString}}},
		},
	})

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected a struct with an invalid field to be rejected")
	}
}

func TestValidateOntologySchemaRejectsDuplicateSideNameOnOneObjectType(t *testing.T) {
	schema := validAviationSchema()
	// A second link type reusing "departures" on Airport: a traversal naming
	// that side could no longer tell which link type it meant.
	schema.LinkTypes = append(schema.LinkTypes, OntologyLinkType{
		APIName: "flightArrival",
		A:       OntologyLinkSide{APIName: "departures", ObjectTypeAPIName: "Airport", Cardinality: OntologyCardinalityMany},
		B:       OntologyLinkSide{APIName: "destination", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityOne},
	})

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected a side api name reused on one object type to be rejected")
	}
	if !strings.Contains(err.Error(), "departures") {
		t.Fatalf("error should name the side, got %v", err)
	}
}

func TestValidateOntologySchemaAllowsSameSideNameOnDifferentObjectTypes(t *testing.T) {
	schema := validAviationSchema()
	// "origin" already exists on Flight; reusing it on Airport is unambiguous
	// because a traversal is always rooted at a known object type.
	schema.LinkTypes = append(schema.LinkTypes, OntologyLinkType{
		APIName: "airportRegion",
		A:       OntologyLinkSide{APIName: "origin", ObjectTypeAPIName: "Airport", Cardinality: OntologyCardinalityOne},
		B:       OntologyLinkSide{APIName: "regionAirports", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityMany},
	})

	if err := validateOntologySchema(schema); err != nil {
		t.Fatalf("expected a side name reused across object types to be allowed, got %v", err)
	}
}

func TestValidateOntologySchemaErrorsAreInvalidOntology(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = ""

	err := validateOntologySchema(schema)
	if !errors.Is(err, ErrInvalidOntology) {
		t.Fatalf("schema rejections must wrap ErrInvalidOntology, got %v", err)
	}
	// The specific cause has to survive the wrapping, or callers lose the
	// only thing that tells them what to fix.
	if !strings.Contains(err.Error(), "primary_key") {
		t.Fatalf("wrapping lost the detail: %v", err)
	}
}
