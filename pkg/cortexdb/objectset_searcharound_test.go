package cortexdb

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// aviationTraversalSchema extends the aviation schema with what traversal
// tests need and validation alone does not: a second link type between the
// same two object types, so a hop has to name the edge type it follows; and a
// third object type exposing its own "origin" side, so a side name is
// ambiguous on its own — which the ontology permits, because side names are
// unique per object type rather than globally.
func aviationTraversalSchema() OntologySchema {
	schema := validAviationSchema()
	schema.ObjectTypes = append(schema.ObjectTypes, OntologyObjectType{
		APIName:    "Shipment",
		PrimaryKey: "shipmentId",
		Properties: []OntologyProperty{
			{APIName: "shipmentId", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
	})
	schema.LinkTypes = append(schema.LinkTypes,
		OntologyLinkType{
			APIName: "flightArrival",
			A:       OntologyLinkSide{APIName: "arrivals", ObjectTypeAPIName: "Airport", Cardinality: OntologyCardinalityMany},
			B:       OntologyLinkSide{APIName: "destination", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityOne},
		},
		OntologyLinkType{
			APIName: "shipmentOrigin",
			A:       OntologyLinkSide{APIName: "origin", ObjectTypeAPIName: "Shipment", Cardinality: OntologyCardinalityOne},
			B:       OntologyLinkSide{APIName: "outboundShipments", ObjectTypeAPIName: "Airport", Cardinality: OntologyCardinalityMany},
		},
	)
	return schema
}

func seedAviationGraph(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		Schema:   aviationTraversalSchema(),
		Activate: true,
	}); err != nil {
		t.Fatalf("activate schema: %v", err)
	}

	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
		{Name: "Gatwick", Type: "Airport", Metadata: map[string]string{"iataCode": "LGW"}},
		{Name: "BA117", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA117"}},
		{Name: "BA118", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA118"}},
		{Name: "U28422", Type: "Flight", Metadata: map[string]string{"flightNumber": "U28422"}},
		{Name: "SHIP1", Type: "Shipment", Metadata: map[string]string{"shipmentId": "SHIP1"}},
	}}); err != nil {
		t.Fatalf("seed entities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
		{From: "London Heathrow", To: "BA118", Type: "flightDeparture"},
		{From: "Gatwick", To: "U28422", Type: "flightDeparture"},
		// Heathrow is also where U28422 lands, along a different link type.
		{From: "London Heathrow", To: "U28422", Type: "flightArrival"},
		{From: "Gatwick", To: "SHIP1", Type: "shipmentOrigin"},
	}}); err != nil {
		t.Fatalf("seed relations: %v", err)
	}
}

func heathrowSet() ObjectSet {
	return ObjectSet{Kind: ObjectSetStatic, ObjectIDs: []string{ontologyNodeID("Airport", "LHR")}}
}

func searchAroundSet(source ObjectSet, link string) ObjectSet {
	return ObjectSet{Kind: ObjectSetSearchAround, Source: &source, Link: link}
}

func TestSearchAroundFollowsTheNamedSide(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	// From Heathrow, traverse the "departures" side to reach its flights.
	ids := resolvedIDs(t, db, searchAroundSet(heathrowSet(), "departures"))

	sort.Strings(ids)
	want := []string{ontologyNodeID("Flight", "BA117"), ontologyNodeID("Flight", "BA118")}
	if len(ids) != len(want) {
		t.Fatalf("expected Heathrow's 2 flights, got %v", ids)
	}
	for i, id := range want {
		if ids[i] != id {
			t.Fatalf("expected %v, got %v", want, ids)
		}
	}
}

func TestSearchAroundTraversesTheOtherDirection(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	// From all flights, traverse "origin" back to their airports.
	ids := resolvedIDs(t, db, searchAroundSet(ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"}, "origin"))

	if len(ids) != 2 {
		t.Fatalf("expected both airports, got %v", ids)
	}
}

// TestSearchAroundSidesAreNotInterchangeable is the direction check proper.
// Traversing "origin" from an airport, or "departures" from a flight, asks for
// a hop that does not exist and must land nowhere — an implementation that
// only follows the link type, ignoring which side was named, returns the
// neighbours anyway.
func TestSearchAroundSidesAreNotInterchangeable(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	if ids := resolvedIDs(t, db, searchAroundSet(heathrowSet(), "origin")); len(ids) != 0 {
		t.Fatalf("an airport has no origin, got %v", ids)
	}
	flights := ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"}
	if ids := resolvedIDs(t, db, searchAroundSet(flights, "departures")); len(ids) != 0 {
		t.Fatalf("a flight has no departures, got %v", ids)
	}
}

// TestSearchAroundStaysOnTheSourcesOwnEdges guards the SQL argument order:
// misordered placeholders make the two halves of the union match the wrong
// column, which quietly widens the hop to every flight in the graph.
func TestSearchAroundStaysOnTheSourcesOwnEdges(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	ids := resolvedIDs(t, db, searchAroundSet(heathrowSet(), "departures"))
	for _, id := range ids {
		if id == ontologyNodeID("Flight", "U28422") {
			t.Fatalf("Gatwick's flight must not be reachable from Heathrow, got %v", ids)
		}
	}
}

// TestSearchAroundFollowsOnlyTheNamedLinkType separates the two link types
// that join the same pair of object types. Heathrow departs BA117 and BA118
// and receives U28422; a hop that matched edges by endpoint type alone would
// return all three for either side.
func TestSearchAroundFollowsOnlyTheNamedLinkType(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	departures := resolvedIDs(t, db, searchAroundSet(heathrowSet(), "departures"))
	if len(departures) != 2 {
		t.Fatalf("expected 2 departures, got %v", departures)
	}

	arrivals := resolvedIDs(t, db, searchAroundSet(heathrowSet(), "arrivals"))
	if len(arrivals) != 1 || arrivals[0] != ontologyNodeID("Flight", "U28422") {
		t.Fatalf("expected the single arrival, got %v", arrivals)
	}
}

// TestSearchAroundWalksEverySideWithThatName covers the ambiguous case the
// schema rules allow: "origin" is a side of Flight and, separately, of
// Shipment. A traversal over a set holding both has to follow both, not
// whichever link type happens to be declared first.
func TestSearchAroundWalksEverySideWithThatName(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	mixed := ObjectSet{Kind: ObjectSetStatic, ObjectIDs: []string{
		ontologyNodeID("Flight", "BA117"),
		ontologyNodeID("Shipment", "SHIP1"),
	}}
	ids := resolvedIDs(t, db, searchAroundSet(mixed, "origin"))

	want := []string{ontologyNodeID("Airport", "LGW"), ontologyNodeID("Airport", "LHR")}
	if len(ids) != len(want) {
		t.Fatalf("expected both origins, got %v", ids)
	}
	for i, id := range want {
		if ids[i] != id {
			t.Fatalf("expected %v, got %v", want, ids)
		}
	}
}

// TestSearchAroundMatchesUncanonicalisedRows covers graph data written before
// a schema was activated, which keeps whatever casing it arrived with. Those
// are the same objects and the same links, so a hop has to reach them; an
// exact match on either node_type or edge_type quietly drops them.
func TestSearchAroundMatchesUncanonicalisedRows(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)
	ctx := context.Background()

	if err := db.graph.UpsertNode(ctx, &graph.GraphNode{
		ID:       "entity:flight:legacy",
		Content:  "BA900",
		NodeType: "flight",
		Vector:   []float32{0},
	}); err != nil {
		t.Fatalf("write legacy node: %v", err)
	}
	if err := db.graph.UpsertEdge(ctx, &graph.GraphEdge{
		ID:         "edge:legacy",
		FromNodeID: ontologyNodeID("Airport", "LHR"),
		ToNodeID:   "entity:flight:legacy",
		EdgeType:   "flightdeparture",
	}); err != nil {
		t.Fatalf("write legacy edge: %v", err)
	}

	ids := resolvedIDs(t, db, searchAroundSet(heathrowSet(), "departures"))
	if len(ids) != 3 {
		t.Fatalf("expected the legacy flight to be reachable, got %v", ids)
	}
}

func TestSearchAroundRejectsUnknownLinkSide(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	_, err := db.ResolveObjectSet(context.Background(),
		searchAroundSet(ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"}, "refuelsAt"))
	if err == nil {
		t.Fatal("expected an unknown link side to be rejected")
	}
	if !strings.Contains(err.Error(), "refuelsAt") {
		t.Fatalf("error should name the side, got %v", err)
	}
}

// TestSearchAroundRejectsTheLinkTypeAsASide keeps the side name from being
// quietly accepted as a link type name: the side is what fixes the direction,
// so the link type on its own is not a hop.
func TestSearchAroundRejectsTheLinkTypeAsASide(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	_, err := db.ResolveObjectSet(context.Background(),
		searchAroundSet(heathrowSet(), "flightDeparture"))
	if err == nil {
		t.Fatal("expected the link type name to be rejected as a side name")
	}
}

func TestSearchAroundNeedsAnActiveOntology(t *testing.T) {
	db := openOntologyTestDB(t)

	_, err := db.ResolveObjectSet(context.Background(),
		searchAroundSet(ObjectSet{Kind: ObjectSetStatic, ObjectIDs: []string{"entity:airport:lhr"}}, "departures"))
	if err == nil {
		t.Fatal("search_around without an active ontology must be rejected")
	}
}

func TestSearchAroundFromAnEmptySourceIsEmpty(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	source := ObjectSet{
		Kind:   ObjectSetFilter,
		Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"},
		Where:  &ObjectSetPredicate{Op: PredicateEq, Property: "iataCode", Value: "CDG"},
	}
	if ids := resolvedIDs(t, db, searchAroundSet(source, "departures")); len(ids) != 0 {
		t.Fatalf("expected nothing, got %v", ids)
	}
}

func TestSearchAroundChainsTwice(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	// Heathrow -> its flights -> back to their origin airports (Heathrow).
	ids := resolvedIDs(t, db, searchAroundSet(searchAroundSet(heathrowSet(), "departures"), "origin"))

	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected to come back to Heathrow, got %v", ids)
	}
}

// TestSearchAroundChainsThreeHops is the documented limit exercised end to
// end, not just accepted by the validator.
func TestSearchAroundChainsThreeHops(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	ids := resolvedIDs(t, db, searchAroundSet(
		searchAroundSet(searchAroundSet(heathrowSet(), "departures"), "origin"), "departures"))

	if len(ids) != 2 {
		t.Fatalf("expected Heathrow's 2 flights again, got %v", ids)
	}
}

func TestSearchAroundRejectsAFourthHop(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	set := searchAroundSet(searchAroundSet(
		searchAroundSet(searchAroundSet(heathrowSet(), "departures"), "origin"), "departures"), "origin")

	if _, err := db.ResolveObjectSet(context.Background(), set); err == nil {
		t.Fatal("a fourth chained search_around must be rejected")
	}
}

// TestSearchAroundIsComposableWithSetOperations is the point of the whole
// algebra: a traversal is an operand like any other.
func TestSearchAroundIsComposableWithSetOperations(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	heathrowFlights := searchAroundSet(heathrowSet(), "departures")
	allFlights := ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"}

	ids := resolvedIDs(t, db, ObjectSet{
		Kind:     ObjectSetSubtract,
		Operands: []ObjectSet{allFlights, heathrowFlights},
	})
	if len(ids) != 1 || ids[0] != ontologyNodeID("Flight", "U28422") {
		t.Fatalf("expected only Gatwick's flight, got %v", ids)
	}
}

// TestSearchAroundIgnoresSourceObjectsOfTheWrongType checks a mixed source:
// only the objects that actually own the side contribute a hop.
func TestSearchAroundIgnoresSourceObjectsOfTheWrongType(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	mixed := ObjectSet{Kind: ObjectSetStatic, ObjectIDs: []string{
		ontologyNodeID("Airport", "LHR"),
		ontologyNodeID("Flight", "U28422"),
	}}
	ids := resolvedIDs(t, db, searchAroundSet(mixed, "departures"))

	if len(ids) != 2 {
		t.Fatalf("expected only Heathrow's flights, got %v", ids)
	}
	for _, id := range ids {
		if id == ontologyNodeID("Flight", "U28422") {
			t.Fatalf("Gatwick's flight is the source, not a hop from it: %v", ids)
		}
	}
}
