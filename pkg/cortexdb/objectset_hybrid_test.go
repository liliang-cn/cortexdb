package cortexdb

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestObjectSetTextPredicateUsesSearchableProperties(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateContainsAllTerms, Property: "facilityName", Value: "london heathrow"}))
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected only Heathrow, got %v", ids)
	}
}

func TestObjectSetContainsAnyTerm(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateContainsAnyTerm, Property: "facilityName", Value: "gatwick sunderland"}))
	if len(ids) != 2 {
		t.Fatalf("expected Gatwick and Sunderland, got %v", ids)
	}
}

// TestObjectSetTextPredicatesAreDistinguishable puts one shared term and one
// exclusive term in the same query, so all-terms and any-term must disagree.
func TestObjectSetTextPredicatesAreDistinguishable(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	all := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateContainsAllTerms, Property: "facilityName", Value: "sunderland gatwick"}))
	if len(all) != 0 {
		t.Fatalf("no facility carries both terms, got %v", all)
	}

	any := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateContainsAnyTerm, Property: "facilityName", Value: "sunderland gatwick"}))
	if len(any) != 2 {
		t.Fatalf("two facilities carry one term each, got %v", any)
	}
}

// TestObjectSetTextPredicateMatchesWholeTerms is what separates the text
// predicates from `contains`: "and" sits inside "Sunderland" but is not a term
// of it. A substring implementation makes contains_all_terms a slower spelling
// of contains.
func TestObjectSetTextPredicateMatchesWholeTerms(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	terms := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateContainsAnyTerm, Property: "facilityName", Value: "and"}))
	if len(terms) != 0 {
		t.Fatalf("\"and\" is not a term of any facility name, got %v", terms)
	}

	substring := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateContains, Property: "facilityName", Value: "and"}))
	if len(substring) != 1 {
		t.Fatalf("contains should still match inside Sunderland, got %v", substring)
	}
}

// TestObjectSetTextPredicateReadsTheNamedProperty uses a property whose value
// shares no term with the object's title, so a predicate that quietly matched
// against the name instead finds nothing.
func TestObjectSetTextPredicateReadsTheNamedProperty(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateContainsAnyTerm, Property: "iataCode", Value: "lhr"}))
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected Heathrow by its code, got %v", ids)
	}
}

func TestObjectSetTextPredicateIgnoresMissingProperties(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateContainsAnyTerm, Property: "runwayName", Value: "north south"}))
	if len(ids) != 0 {
		t.Fatalf("a property no object carries matches nothing, got %v", ids)
	}
}

// TestObjectSetTextPredicateWithNoUsableTerms pins a query that tokenizes to
// nothing: it selects nothing rather than vacuously selecting everything.
func TestObjectSetTextPredicateWithNoUsableTerms(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	for _, op := range []PredicateOp{PredicateContainsAllTerms, PredicateContainsAnyTerm} {
		ids := resolvedIDs(t, db, filterFacilitiesBy(
			ObjectSetPredicate{Op: op, Property: "facilityName", Value: "-- ,"}))
		if len(ids) != 0 {
			t.Fatalf("%s with no terms must select nothing, got %v", op, ids)
		}
	}
}

func TestObjectSetVectorPredicateRequiresEmbedder(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	_, err := db.ResolveObjectSet(context.Background(), filterAirportsBy(
		ObjectSetPredicate{Op: PredicateNearestNeighbors, Property: "embedding", Value: "busy london hub", K: 1}))
	// No embedder is configured in this test DB, so the resolver must say so
	// plainly rather than silently returning everything or nothing.
	if err == nil {
		t.Fatal("expected a clear error when a vector predicate runs without an embedder")
	}
	if !strings.Contains(err.Error(), "embedder") {
		t.Fatalf("error should name the missing embedder, got %v", err)
	}
}

// nodeVector reads an object's stored embedding, so a nearest-neighbour test
// can query with a vector that is exactly one object's own.
func nodeVector(t *testing.T, db *DB, nodeID string) []float32 {
	t.Helper()
	node, err := db.graph.GetNode(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("get node %s: %v", nodeID, err)
	}
	return node.Vector
}

func TestObjectSetVectorPredicateRanksAndBounds(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	// Heathrow's own vector, so it is the nearest neighbour by construction.
	query := nodeVector(t, db, ontologyNodeID("Airport", "LHR"))

	nearest := resolvedIDs(t, db, filterFacilitiesBy(ObjectSetPredicate{
		Op: PredicateNearestNeighbors, Property: "embedding", Vector: query, K: 1,
	}))
	if len(nearest) != 1 || nearest[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected Heathrow as the single nearest neighbour, got %v", nearest)
	}

	// k is a bound on the neighbourhood, so widening it widens the answer.
	wider := resolvedIDs(t, db, filterFacilitiesBy(ObjectSetPredicate{
		Op: PredicateNearestNeighbors, Property: "embedding", Vector: query, K: 3,
	}))
	if len(wider) != 3 {
		t.Fatalf("expected all three facilities within k=3, got %v", wider)
	}
}

// TestObjectSetVectorPredicateIntersectsTheSource is the composition check:
// the nearest neighbour is the plant, which is not an Airport, so filtering
// the airports by it yields nothing rather than the plant.
func TestObjectSetVectorPredicateIntersectsTheSource(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	query := nodeVector(t, db, ontologyNodeID("Plant", "SUN"))
	ids := resolvedIDs(t, db, filterAirportsBy(ObjectSetPredicate{
		Op: PredicateNearestNeighbors, Property: "embedding", Vector: query, K: 1,
	}))
	if len(ids) != 0 {
		t.Fatalf("the nearest neighbour is outside the source, got %v", ids)
	}
}

// TestObjectSetVectorPredicateEmbedsTheQueryText covers the path agents will
// actually take: text in, embedding done by the configured embedder.
func TestObjectSetVectorPredicateEmbedsTheQueryText(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	// Swapped in after seeding so the stored node vectors stay the lexical
	// ones; only the query side is embedded here.
	db.embedder = newKeywordEmbedder("london", "heathrow")

	_, err := db.ResolveObjectSet(context.Background(), filterAirportsBy(ObjectSetPredicate{
		Op: PredicateNearestNeighbors, Property: "embedding", Value: "london heathrow", K: 1,
	}))
	if err != nil {
		t.Fatalf("expected the query text to be embedded, got %v", err)
	}
}

func TestResolveObjectSetToolIsReachable(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	resp, err := db.GraphRAGTools().ResolveObjectSet(context.Background(), ObjectSetResolveRequest{
		ObjectSet: ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("resolve tool: %v", err)
	}
	if len(resp.Objects) != 3 {
		t.Fatalf("expected 3 facilities, got %d", len(resp.Objects))
	}
	if resp.Objects[0].ObjectType == "" {
		t.Fatal("resolved objects must carry their object type")
	}
	if resp.Objects[0].Title == "" {
		t.Fatal("resolved objects must carry their title")
	}
	if resp.Objects[0].Properties["facilityName"] == "" {
		t.Fatal("resolved objects must carry their properties")
	}
}

// TestResolveObjectSetToolLimitsWithoutLosingTheTotal keeps the page and the
// count apart: a caller that asks for one object still learns there are three.
func TestResolveObjectSetToolLimitsWithoutLosingTheTotal(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	resp, err := db.GraphRAGTools().ResolveObjectSet(context.Background(), ObjectSetResolveRequest{
		ObjectSet: ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"},
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("resolve tool: %v", err)
	}
	if len(resp.Objects) != 1 {
		t.Fatalf("expected the limit to apply, got %d", len(resp.Objects))
	}
	if resp.Total != 3 {
		t.Fatalf("expected the unlimited total, got %d", resp.Total)
	}

	// No limit means every member.
	all, err := db.GraphRAGTools().ResolveObjectSet(context.Background(), ObjectSetResolveRequest{
		ObjectSet: ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"},
	})
	if err != nil {
		t.Fatalf("resolve tool: %v", err)
	}
	if len(all.Objects) != 3 {
		t.Fatalf("expected no limit to return everything, got %d", len(all.Objects))
	}
}

// TestResolveObjectSetToolOrdersItsResults matters because the set being
// resolved is a Go map, whose iteration order Go deliberately randomises. Both
// halves are checked over repeated calls: the sequence returned, and — the one
// that actually bites — which members a limit selects. An unordered page hands
// back a different object every call for the same request.
func TestResolveObjectSetToolOrdersItsResults(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)
	facilities := ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"}

	for i := 0; i < 20; i++ {
		resp, err := db.GraphRAGTools().ResolveObjectSet(context.Background(), ObjectSetResolveRequest{
			ObjectSet: facilities,
		})
		if err != nil {
			t.Fatalf("resolve tool: %v", err)
		}
		ids := make([]string, 0, len(resp.Objects))
		for _, object := range resp.Objects {
			ids = append(ids, object.ObjectID)
		}
		if !sort.StringsAreSorted(ids) {
			t.Fatalf("resolved objects must come back in a stable order, got %v", ids)
		}

		limited, err := db.GraphRAGTools().ResolveObjectSet(context.Background(), ObjectSetResolveRequest{
			ObjectSet: facilities,
			Limit:     1,
		})
		if err != nil {
			t.Fatalf("resolve tool: %v", err)
		}
		if len(limited.Objects) != 1 || limited.Objects[0].ObjectID != ontologyNodeID("Airport", "LGW") {
			t.Fatalf("a limited page must always be the first objects, got %+v", limited.Objects)
		}
	}
}

// TestResolveObjectSetToolSkipsObjectsThatDoNotExist covers a static set that
// names an id nothing was ever written under: it counts toward the total the
// set resolved to, but there is no object to return for it.
func TestResolveObjectSetToolSkipsObjectsThatDoNotExist(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	resp, err := db.GraphRAGTools().ResolveObjectSet(context.Background(), ObjectSetResolveRequest{
		ObjectSet: ObjectSet{Kind: ObjectSetStatic, ObjectIDs: []string{
			ontologyNodeID("Airport", "LHR"),
			"entity:airport:nowhere",
		}},
	})
	if err != nil {
		t.Fatalf("resolve tool: %v", err)
	}
	if len(resp.Objects) != 1 || resp.Objects[0].ObjectID != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected only the object that exists, got %+v", resp.Objects)
	}
}

func TestResolveObjectSetToolRejectsAnInvalidSet(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	if _, err := db.GraphRAGTools().ResolveObjectSet(context.Background(), ObjectSetResolveRequest{
		ObjectSet: ObjectSet{Kind: ObjectSetFilter},
	}); err == nil {
		t.Fatal("expected an invalid object set to be rejected")
	}
}

// TestResolveObjectSetToolDecodesANestedSetFromJSON is the wire-format check
// for the recursive type: an agent sends this shape, not a Go struct.
func TestResolveObjectSetToolDecodesANestedSetFromJSON(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	input := json.RawMessage(`{
		"object_set": {
			"kind": "subtract",
			"operands": [
				{"kind": "interface_base", "interface_type": "Facility"},
				{
					"kind": "filter",
					"source": {"kind": "base", "object_type": "Airport"},
					"where": {"op": "contains_any_term", "property": "facilityName", "value": "gatwick"}
				}
			]
		}
	}`)

	result, err := db.GraphRAGTools().Call(context.Background(), "object_set_resolve", input)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	resp, ok := result.(*ObjectSetResolveResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", result)
	}
	if resp.Total != 2 {
		t.Fatalf("expected everything but Gatwick, got %d", resp.Total)
	}
	for _, object := range resp.Objects {
		if object.ObjectID == ontologyNodeID("Airport", "LGW") {
			t.Fatalf("Gatwick was subtracted, got %+v", resp.Objects)
		}
	}
}
