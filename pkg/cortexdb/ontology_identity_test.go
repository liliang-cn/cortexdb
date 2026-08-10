package cortexdb

import "testing"

func TestOntologyNodeIDUsesObjectTypeAndPrimaryKey(t *testing.T) {
	id := ontologyNodeID("Airport", "LHR")
	if id != "entity:airport:lhr" {
		t.Fatalf("unexpected node id: %q", id)
	}
	if ontologyNodeID("Airport", "LHR") != ontologyNodeID("airport", "lhr") {
		t.Fatal("node id must be case-insensitive")
	}
	if ontologyNodeID("Airport", "LHR") == ontologyNodeID("Aircraft", "LHR") {
		t.Fatal("same key under different object types must not collide")
	}
}

func TestOntologyNodeIDNormalizesSeparators(t *testing.T) {
	if ontologyNodeID("Airport", "London Heathrow") != "entity:airport:london_heathrow" {
		t.Fatalf("unexpected node id: %q", ontologyNodeID("Airport", "London Heathrow"))
	}
}

func TestResolveOntologyPrimaryKeyValue(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	value, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{
		Name:     "London Heathrow",
		Type:     "Airport",
		Metadata: map[string]string{"iataCode": "LHR"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "LHR" {
		t.Fatalf("expected LHR, got %q", value)
	}
}

func TestResolveOntologyPrimaryKeyValueIsCaseInsensitiveOnMetadataKey(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	value, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{
		Type:     "Airport",
		Metadata: map[string]string{"IATACODE": "LHR"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "LHR" {
		t.Fatalf("expected LHR, got %q", value)
	}
}

func TestResolveOntologyPrimaryKeyValueFallsBackToNameProperty(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = "airportName"
	schema.ObjectTypes[0].Properties[1].Required = true
	compiled := compileOntology(schema)

	value, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{Name: "London Heathrow"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "London Heathrow" {
		t.Fatalf("expected the entity name, got %q", value)
	}
}

func TestResolveOntologyPrimaryKeyValueFallsBackToTitleProperty(t *testing.T) {
	schema := validAviationSchema()
	// A primary key that is also the declared title property arrives as the
	// entity name, whatever the property happens to be called.
	schema.ObjectTypes[0].TitleProperty = "iataCode"
	compiled := compileOntology(schema)

	value, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{Name: "LHR"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "LHR" {
		t.Fatalf("expected the entity name, got %q", value)
	}
}

func TestResolveOntologyPrimaryKeyValueErrorsWhenMissing(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	_, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{Name: "London Heathrow"})
	if err == nil {
		t.Fatal("expected a missing primary key value to be an error")
	}
}

func TestResolveOntologyPrimaryKeyValueErrorsOnUnknownObjectType(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	_, err := resolveOntologyPrimaryKeyValue(compiled, "Spacecraft", ToolEntityInput{Name: "Voyager"})
	if err == nil {
		t.Fatal("expected an unknown object type to be an error")
	}
}

func TestResolveOntologyPrimaryKeyValueIgnoresBlankMetadataValue(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	// A blank value is no value: it must not become a node ID of "entity:airport:".
	_, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{
		Name:     "London Heathrow",
		Metadata: map[string]string{"iataCode": "  "},
	})
	if err == nil {
		t.Fatal("expected a blank primary key value to be an error")
	}
}
