package cortexdb

import (
	"errors"
	"strings"
	"testing"
)

func facilitySchema() OntologySchema {
	return OntologySchema{
		SchemaID: "facilities",
		Name:     "Facilities",
		InterfaceTypes: []OntologyInterfaceType{
			{
				APIName: "Locatable",
				Properties: []OntologyProperty{
					{APIName: "position", DataType: OntologyDataType{Kind: OntologyDataGeoPoint}, Required: true},
				},
			},
			{
				APIName: "Facility",
				Extends: []string{"Locatable"},
				Properties: []OntologyProperty{
					{APIName: "facilityName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "capacity", DataType: OntologyDataType{Kind: OntologyDataInteger}},
				},
			},
			// Storable exists so that the object type which is not a Facility is
			// still an implementor of something. Without it "Warehouse is
			// excluded" would hold for an implementation that returned every
			// object type declaring any Implements at all.
			{
				APIName: "Storable",
				Properties: []OntologyProperty{
					{APIName: "storageUnits", DataType: OntologyDataType{Kind: OntologyDataInteger}, Required: true},
				},
			},
		},
		ObjectTypes: []OntologyObjectType{
			{
				APIName:    "Airport",
				PrimaryKey: "iataCode",
				Implements: []string{"Facility"},
				Properties: []OntologyProperty{
					{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "facilityName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "position", DataType: OntologyDataType{Kind: OntologyDataGeoPoint}, Required: true},
				},
			},
			{
				APIName:    "Plant",
				PrimaryKey: "plantCode",
				Implements: []string{"Facility"},
				Properties: []OntologyProperty{
					{APIName: "plantCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "facilityName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "position", DataType: OntologyDataType{Kind: OntologyDataGeoPoint}, Required: true},
				},
			},
			{
				APIName:    "Warehouse",
				PrimaryKey: "warehouseCode",
				Implements: []string{"Storable"},
				Properties: []OntologyProperty{
					{APIName: "warehouseCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "storageUnits", DataType: OntologyDataType{Kind: OntologyDataInteger}, Required: true},
				},
			},
		},
	}
}

func TestInterfaceClosureIncludesInheritedInterfaces(t *testing.T) {
	compiled := compileOntology(facilitySchema())

	closure := compiled.interfaceClosure("Facility")
	if len(closure) != 2 {
		t.Fatalf("expected Facility and Locatable, got %v", closure)
	}
	if _, ok := closure[ontologyAPIKey("Locatable")]; !ok {
		t.Fatalf("Facility extends Locatable, closure was %v", closure)
	}
}

func TestImplementingObjectTypesResolvesThroughInheritance(t *testing.T) {
	compiled := compileOntology(facilitySchema())

	implementors := compiled.implementingObjectTypes("Locatable")
	if len(implementors) != 2 {
		t.Fatalf("expected Airport and Plant to implement Locatable transitively, got %v", implementors)
	}

	direct := compiled.implementingObjectTypes("Facility")
	if len(direct) != 2 {
		t.Fatalf("expected 2 Facility implementors, got %v", direct)
	}
	// Warehouse implements Storable, so being excluded from Facility has to
	// come from the closure and not from having no interfaces at all.
	for _, name := range append(append([]string{}, implementors...), direct...) {
		if name == "Warehouse" {
			t.Fatalf("Warehouse implements only Storable, got %v / %v", implementors, direct)
		}
	}
	if storable := compiled.implementingObjectTypes("Storable"); len(storable) != 1 || storable[0] != "Warehouse" {
		t.Fatalf("expected Warehouse to implement Storable, got %v", storable)
	}
	if len(compiled.implementingObjectTypes("Nonexistent")) != 0 {
		t.Fatal("unknown interface must resolve to no implementors")
	}
}

func TestResolveTypeClosureExpandsInterfacesButNotObjectTypes(t *testing.T) {
	compiled := compileOntology(facilitySchema())

	byInterface := compiled.resolveTypeClosure("Facility")
	if len(byInterface) != 2 {
		t.Fatalf("interface must expand to its implementors, got %v", byInterface)
	}

	byObjectType := compiled.resolveTypeClosure("Airport")
	if len(byObjectType) != 1 || byObjectType[0] != "Airport" {
		t.Fatalf("object type must resolve to itself, got %v", byObjectType)
	}

	// The declared spelling, not the caller's: what comes out of here is
	// matched against stored node types, which are written declared-cased.
	misspelt := compiled.resolveTypeClosure("airport")
	if len(misspelt) != 1 || misspelt[0] != "Airport" {
		t.Fatalf("a differently cased object type must resolve to its declared name, got %v", misspelt)
	}
	if byInterface[0] != "Airport" || byInterface[1] != "Plant" {
		t.Fatalf("implementors must come back declared-cased and sorted, got %v", byInterface)
	}

	unknown := compiled.resolveTypeClosure("Spacecraft")
	if len(unknown) != 1 || unknown[0] != "Spacecraft" {
		t.Fatalf("unknown type passes through unchanged, got %v", unknown)
	}
}

func TestValidateSchemaRejectsInterfaceCycle(t *testing.T) {
	schema := facilitySchema()
	schema.InterfaceTypes[0].Extends = []string{"Facility"} // Locatable -> Facility -> Locatable

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected an interface cycle to be rejected, got %v", err)
	}
	// Interface rejections are schema rejections, so a protocol boundary can
	// map them to InvalidArgument like every other one.
	if !errors.Is(err, ErrInvalidOntology) {
		t.Fatalf("expected ErrInvalidOntology, got %v", err)
	}
}

func TestValidateSchemaRejectsSelfExtendingInterface(t *testing.T) {
	schema := facilitySchema()
	schema.InterfaceTypes[0].Extends = []string{"Locatable"}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected an interface extending itself to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsInterfaceExtendingUnknownInterface(t *testing.T) {
	schema := facilitySchema()
	schema.InterfaceTypes[1].Extends = []string{"Locateable"} // misspelled

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "Locateable") {
		t.Fatalf("expected an unknown parent interface to be rejected, got %v", err)
	}
}

func TestValidateSchemaRequiresImplementorsToSatisfyRequiredProperties(t *testing.T) {
	schema := facilitySchema()
	// Drop the position property Airport needs to satisfy Locatable.
	schema.ObjectTypes[0].Properties = schema.ObjectTypes[0].Properties[:2]

	err := validateOntologySchema(schema)
	// The exact complaint matters: an undeclared property also reads as a data
	// type mismatch against the zero value, so matching on "position" alone
	// passes even when the missing-property check has been removed.
	if err == nil || !strings.Contains(err.Error(), "position") || !strings.Contains(err.Error(), "does not declare it") {
		t.Fatalf("expected an unsatisfied interface property to be rejected, got %v", err)
	}
}

func TestValidateSchemaRequiresImplementorPropertyTypesToMatch(t *testing.T) {
	schema := facilitySchema()
	schema.ObjectTypes[0].Properties[2].DataType = OntologyDataType{Kind: OntologyDataString}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "position") || !strings.Contains(err.Error(), string(OntologyDataGeoPoint)) {
		t.Fatalf("expected a data type mismatch against the interface to be rejected, got %v", err)
	}
}

func TestValidateSchemaAllowsOptionalInterfacePropertyToBeSkipped(t *testing.T) {
	// capacity is optional on Facility and declared by neither implementor.
	if err := validateOntologySchema(facilitySchema()); err != nil {
		t.Fatalf("optional interface properties may be skipped, got %v", err)
	}
}
