package cortexdb

import "testing"

func TestCompileOntologyLooksUpCaseInsensitively(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	objectType, ok := compiled.objectType("AIRPORT")
	if !ok {
		t.Fatal("expected case-insensitive object type lookup")
	}
	if objectType.APIName != "Airport" {
		t.Fatalf("lookup should return the declared casing, got %q", objectType.APIName)
	}

	if _, ok := compiled.linkType("FLIGHTDEPARTURE"); !ok {
		t.Fatal("expected case-insensitive link type lookup")
	}
	if _, ok := compiled.objectType("Spacecraft"); ok {
		t.Fatal("unknown object type must not resolve")
	}
}

func TestCompiledOntologyResolvesLinkSidesByEndpointType(t *testing.T) {
	compiled := compileOntology(validAviationSchema())
	linkType, _ := compiled.linkType("flightDeparture")

	from, to, err := compiled.orientLink(linkType, "Airport", "Flight")
	if err != nil {
		t.Fatalf("orient: %v", err)
	}
	if from.APIName != "departures" || to.APIName != "origin" {
		t.Fatalf("wrong orientation: from=%q to=%q", from.APIName, to.APIName)
	}

	from, to, err = compiled.orientLink(linkType, "Flight", "Airport")
	if err != nil {
		t.Fatalf("reverse orient: %v", err)
	}
	if from.APIName != "origin" || to.APIName != "departures" {
		t.Fatalf("wrong reverse orientation: from=%q to=%q", from.APIName, to.APIName)
	}

	if _, _, err := compiled.orientLink(linkType, "Airport", "Airport"); err == nil {
		t.Fatal("expected mismatched endpoint types to be rejected")
	}
}

func TestCompiledOntologyPropertyLookup(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	property, ok := compiled.property("Airport", "IATACODE")
	if !ok {
		t.Fatal("expected case-insensitive property lookup")
	}
	if property.APIName != "iataCode" {
		t.Fatalf("expected declared casing, got %q", property.APIName)
	}
	if _, ok := compiled.property("Airport", "nope"); ok {
		t.Fatal("unknown property must not resolve")
	}
}

func TestCompileOntologyOnEmptySchema(t *testing.T) {
	compiled := compileOntology(OntologySchema{})
	if compiled.isEmpty() != true {
		t.Fatal("empty schema should compile to an empty ontology")
	}
	if _, ok := compiled.objectType("Airport"); ok {
		t.Fatal("empty ontology must resolve nothing")
	}
}
