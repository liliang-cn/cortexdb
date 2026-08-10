package cortexdb

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func seedObjectSetFacilities(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()

	schema := facilitySchema()
	// Declared but implemented by nothing, so "an interface selects no
	// objects" is a case these tests can actually reach.
	schema.InterfaceTypes = append(schema.InterfaceTypes, OntologyInterfaceType{
		APIName:    "Perishable",
		Properties: []OntologyProperty{{APIName: "shelfLifeDays", DataType: OntologyDataType{Kind: OntologyDataInteger}}},
	})
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties,
		OntologyProperty{APIName: "capacity", DataType: OntologyDataType{Kind: OntologyDataInteger}})
	schema.ObjectTypes[1].Properties = append(schema.ObjectTypes[1].Properties,
		OntologyProperty{APIName: "capacity", DataType: OntologyDataType{Kind: OntologyDataInteger}})

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{
			"iataCode": "LHR", "facilityName": "London Heathrow", "position": "51.4700,-0.4543", "capacity": "80000"}},
		{Name: "Gatwick", Type: "Airport", Metadata: map[string]string{
			"iataCode": "LGW", "facilityName": "Gatwick", "position": "51.1537,-0.1821", "capacity": "40000"}},
		{Name: "Sunderland Plant", Type: "Plant", Metadata: map[string]string{
			"plantCode": "SUN", "facilityName": "Sunderland Plant", "position": "54.9060,-1.3830", "capacity": "5000"}},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func resolvedIDs(t *testing.T, db *DB, set ObjectSet) []string {
	t.Helper()
	resolved, err := db.ResolveObjectSet(context.Background(), set)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ids := make([]string, 0, len(resolved))
	for id := range resolved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func filterAirportsBy(predicate ObjectSetPredicate) ObjectSet {
	return ObjectSet{
		Kind:   ObjectSetFilter,
		Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"},
		Where:  &predicate,
	}
}

func filterFacilitiesBy(predicate ObjectSetPredicate) ObjectSet {
	return ObjectSet{
		Kind:   ObjectSetFilter,
		Source: &ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"},
		Where:  &predicate,
	}
}

func TestResolveObjectSetBase(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"})
	if len(ids) != 2 {
		t.Fatalf("expected 2 airports, got %v", ids)
	}
}

// TestResolveObjectSetBaseIsCaseInsensitive keeps base sets consistent with
// the rest of the ontology, where every api name resolves case-insensitively.
// An exact node_type match would also miss rows written before object types
// were canonicalised to their declared spelling.
func TestResolveObjectSetBaseIsCaseInsensitive(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetBase, ObjectType: "airport"})
	if len(ids) != 2 {
		t.Fatalf("expected 2 airports for a lower-cased object type, got %v", ids)
	}
}

// TestResolveObjectSetBaseMatchesUncanonicalisedNodeTypes covers data written
// before a schema was activated: node_type is only folded to the declared
// spelling on ontology-validated writes, so older rows keep whatever casing
// they arrived with and are still the same objects.
func TestResolveObjectSetBaseMatchesUncanonicalisedNodeTypes(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	if err := db.graph.UpsertNode(context.Background(), &graph.GraphNode{
		ID:       "entity:airport:cdg",
		Content:  "Charles de Gaulle",
		NodeType: "airport",
		Vector:   []float32{0},
	}); err != nil {
		t.Fatalf("write legacy node: %v", err)
	}

	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"})
	if len(ids) != 3 {
		t.Fatalf("expected the legacy-cased airport to be included, got %v", ids)
	}
}

func TestResolveObjectSetBaseExcludesOtherTypes(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetBase, ObjectType: "Plant"})
	if len(ids) != 1 || ids[0] != ontologyNodeID("Plant", "SUN") {
		t.Fatalf("expected only the plant, got %v", ids)
	}
}

func TestResolveObjectSetInterfaceBase(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"})
	if len(ids) != 3 {
		t.Fatalf("expected all 3 facilities, got %v", ids)
	}
}

// TestResolveObjectSetInterfaceBaseWithNoImplementorsIsEmpty pins the
// behaviour of an interface nothing implements: empty, not everything.
func TestResolveObjectSetInterfaceBaseWithNoImplementorsIsEmpty(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	for _, interfaceType := range []string{"Perishable", "NotAnInterface"} {
		ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: interfaceType})
		if len(ids) != 0 {
			t.Fatalf("%s: expected no objects, got %v", interfaceType, ids)
		}
	}
}

// TestResolveObjectSetInterfaceBaseFollowsInterfaceInheritance checks the
// expansion goes through the whole closure: Airport and Plant implement
// Facility, which extends Locatable, so both are Locatable too. Only
// Warehouse, seeded nowhere, is not.
func TestResolveObjectSetInterfaceBaseFollowsInterfaceInheritance(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Locatable"})
	if len(ids) != 3 {
		t.Fatalf("expected all 3 locatable objects, got %v", ids)
	}
}

func TestResolveObjectSetStatic(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, ObjectSet{
		Kind:      ObjectSetStatic,
		ObjectIDs: []string{ontologyNodeID("Airport", "LHR")},
	})
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected exactly the static ID, got %v", ids)
	}
}

func TestResolveObjectSetFilterOnScalarProperty(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, filterAirportsBy(
		ObjectSetPredicate{Op: PredicateGt, Property: "capacity", Value: "50000"}))
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected only Heathrow, got %v", ids)
	}
}

// TestResolveObjectSetOrderedPredicatesCompareNumerically catches the
// comparison falling back to string order: "40000" sorts below "5000"
// lexicographically but is the larger number.
func TestResolveObjectSetOrderedPredicatesCompareNumerically(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateGt, Property: "capacity", Value: "5000"}))
	if len(ids) != 2 {
		t.Fatalf("expected the two airports, got %v", ids)
	}
}

// TestResolveObjectSetOrderedPredicateBoundaries keeps gt/gte and lt/lte
// apart: with a threshold that sits exactly on a value, an off-by-one in the
// comparison changes the result by one object.
func TestResolveObjectSetOrderedPredicateBoundaries(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	cases := []struct {
		op   PredicateOp
		want int
	}{
		{PredicateGt, 1},
		{PredicateGte, 2},
		{PredicateLt, 1},
		{PredicateLte, 2},
	}
	for _, tc := range cases {
		ids := resolvedIDs(t, db, filterFacilitiesBy(
			ObjectSetPredicate{Op: tc.op, Property: "capacity", Value: "40000"}))
		if len(ids) != tc.want {
			t.Fatalf("%s 40000: expected %d, got %v", tc.op, tc.want, ids)
		}
	}
}

// TestResolveObjectSetOrderedPredicateFallsBackToStringOrder covers the other
// half of the comparison: values that are not numbers still have to order.
func TestResolveObjectSetOrderedPredicateFallsBackToStringOrder(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, filterAirportsBy(
		ObjectSetPredicate{Op: PredicateGt, Property: "iataCode", Value: "LGW"}))
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected LHR to sort above LGW, got %v", ids)
	}
}

func TestResolveObjectSetFilterEqAndIn(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	eq := resolvedIDs(t, db, filterAirportsBy(
		ObjectSetPredicate{Op: PredicateEq, Property: "iataCode", Value: "LGW"}))
	if len(eq) != 1 {
		t.Fatalf("eq: expected 1, got %v", eq)
	}

	in := resolvedIDs(t, db, filterAirportsBy(
		ObjectSetPredicate{Op: PredicateIn, Property: "iataCode", Values: []string{"LHR", "LGW"}}))
	if len(in) != 2 {
		t.Fatalf("in: expected 2, got %v", in)
	}

	notIn := resolvedIDs(t, db, filterAirportsBy(
		ObjectSetPredicate{Op: PredicateIn, Property: "iataCode", Values: []string{"CDG"}}))
	if len(notIn) != 0 {
		t.Fatalf("in: expected no match, got %v", notIn)
	}
}

func TestResolveObjectSetContainsAndStartsWith(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	contains := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateContains, Property: "facilityName", Value: "and"}))
	if len(contains) != 1 || contains[0] != ontologyNodeID("Plant", "SUN") {
		t.Fatalf("contains: expected the plant, got %v", contains)
	}

	startsWith := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateStartsWith, Property: "facilityName", Value: "sunder"}))
	if len(startsWith) != 1 || startsWith[0] != ontologyNodeID("Plant", "SUN") {
		t.Fatalf("starts_with: expected the plant, got %v", startsWith)
	}

	// "and" appears inside "Sunderland" but not at its start, so an
	// implementation that treats starts_with as contains fails here.
	notPrefix := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateStartsWith, Property: "facilityName", Value: "and"}))
	if len(notPrefix) != 0 {
		t.Fatalf("starts_with: expected no match, got %v", notPrefix)
	}
}

// TestResolveObjectSetPredicateOnPropertyMissingFromSomeMembers pins what a
// predicate does when a member simply does not carry the property: it is not a
// match, and it is not an error. Airports have iataCode; the plant does not.
func TestResolveObjectSetPredicateOnPropertyMissingFromSomeMembers(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	present := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateEq, Property: "iataCode", Value: "LHR"}))
	if len(present) != 1 || present[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected only Heathrow, got %v", present)
	}

	// A comparison a missing property would trivially satisfy if it were read
	// as the empty string: "" sorts below every code, so the plant would slip
	// in unless absence is checked before the comparison.
	compared := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateLte, Property: "iataCode", Value: "ZZZ"}))
	if len(compared) != 2 {
		t.Fatalf("expected only the two airports to carry iataCode, got %v", compared)
	}

	missing := resolvedIDs(t, db, filterFacilitiesBy(
		ObjectSetPredicate{Op: PredicateIsNull, Property: "iataCode"}))
	if len(missing) != 1 || missing[0] != ontologyNodeID("Plant", "SUN") {
		t.Fatalf("is_null: expected the plant, got %v", missing)
	}
}

func TestResolveObjectSetBooleanPredicates(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, filterAirportsBy(ObjectSetPredicate{
		Op: PredicateAnd,
		Operands: []ObjectSetPredicate{
			{Op: PredicateGt, Property: "capacity", Value: "10000"},
			{Op: PredicateNot, Operands: []ObjectSetPredicate{
				{Op: PredicateEq, Property: "iataCode", Value: "LGW"},
			}},
		},
	}))
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected only Heathrow, got %v", ids)
	}
}

// TestResolveObjectSetNotNegatesWithinTheSource is the check that `not` is a
// complement against the source set and not against the whole graph. Negating
// "is Gatwick" over the airports must leave Heathrow alone — never the plant,
// which is not in the source at all.
func TestResolveObjectSetNotNegatesWithinTheSource(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	ids := resolvedIDs(t, db, filterAirportsBy(ObjectSetPredicate{
		Op: PredicateNot,
		Operands: []ObjectSetPredicate{
			{Op: PredicateEq, Property: "iataCode", Value: "LGW"},
		},
	}))
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected only Heathrow, got %v", ids)
	}
}

// TestResolveObjectSetAndOrAreDistinguishable uses two disjoint conditions, so
// `and` yields nothing and `or` yields both. Swapping them cannot pass both.
// Filtering the three facilities rather than the two airports also means `or`
// has to build its answer from the matches: seeding it with the source set
// would return the plant, which satisfies neither condition.
func TestResolveObjectSetAndOrAreDistinguishable(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	operands := []ObjectSetPredicate{
		{Op: PredicateEq, Property: "iataCode", Value: "LHR"},
		{Op: PredicateEq, Property: "iataCode", Value: "LGW"},
	}

	and := resolvedIDs(t, db, filterFacilitiesBy(ObjectSetPredicate{Op: PredicateAnd, Operands: operands}))
	if len(and) != 0 {
		t.Fatalf("and of two disjoint conditions must be empty, got %v", and)
	}

	or := resolvedIDs(t, db, filterFacilitiesBy(ObjectSetPredicate{Op: PredicateOr, Operands: operands}))
	if len(or) != 2 {
		t.Fatalf("or of two disjoint conditions must be exactly the two airports, got %v", or)
	}
}

func TestResolveObjectSetUnionIntersectSubtract(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	airports := ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}
	plants := ObjectSet{Kind: ObjectSetBase, ObjectType: "Plant"}
	facilities := ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"}

	if ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetUnion, Operands: []ObjectSet{airports, plants}}); len(ids) != 3 {
		t.Fatalf("union: expected 3, got %v", ids)
	}
	if ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetIntersect, Operands: []ObjectSet{facilities, airports}}); len(ids) != 2 {
		t.Fatalf("intersect: expected 2, got %v", ids)
	}
	if ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetSubtract, Operands: []ObjectSet{facilities, airports}}); len(ids) != 1 {
		t.Fatalf("subtract: expected 1, got %v", ids)
	}
}

// TestResolveObjectSetOperationsAreMutuallyDistinct runs all three over the
// same disjoint pair, where each has a different answer. No two of the three
// implementations can be swapped and still satisfy this.
func TestResolveObjectSetOperationsAreMutuallyDistinct(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	airports := ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}
	plants := ObjectSet{Kind: ObjectSetBase, ObjectType: "Plant"}

	cases := []struct {
		kind ObjectSetKind
		want int
	}{
		{ObjectSetUnion, 3},
		{ObjectSetIntersect, 0},
		{ObjectSetSubtract, 2},
	}
	for _, tc := range cases {
		ids := resolvedIDs(t, db, ObjectSet{Kind: tc.kind, Operands: []ObjectSet{airports, plants}})
		if len(ids) != tc.want {
			t.Fatalf("%s of airports and plants: expected %d, got %v", tc.kind, tc.want, ids)
		}
	}
}

// TestResolveObjectSetSubtractIsOrderSensitive stops subtract from being
// implemented as a symmetric difference or as intersection's complement.
func TestResolveObjectSetSubtractIsOrderSensitive(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	airports := ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}
	facilities := ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"}

	if ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetSubtract, Operands: []ObjectSet{airports, facilities}}); len(ids) != 0 {
		t.Fatalf("airports minus facilities must be empty, got %v", ids)
	}
}

// TestResolveObjectSetOperationsFoldEveryOperand catches an implementation
// that stops after the second operand.
func TestResolveObjectSetOperationsFoldEveryOperand(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	facilities := ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"}
	airports := ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}
	heathrow := ObjectSet{Kind: ObjectSetStatic, ObjectIDs: []string{ontologyNodeID("Airport", "LHR")}}

	ids := resolvedIDs(t, db, ObjectSet{
		Kind:     ObjectSetIntersect,
		Operands: []ObjectSet{facilities, airports, heathrow},
	})
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("intersect of three sets: expected Heathrow, got %v", ids)
	}
}

func TestResolveObjectSetReference(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	seedObjectSetFacilities(t, db)
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	schema.ObjectSets = []OntologyNamedObjectSet{{
		APIName:    "bigAirports",
		Definition: filterAirportsBy(ObjectSetPredicate{Op: PredicateGt, Property: "capacity", Value: "50000"}),
	}}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: *schema, Activate: true}); err != nil {
		t.Fatalf("save named object set: %v", err)
	}

	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetReference, Reference: "bigairports"})
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected the saved set to resolve to Heathrow, got %v", ids)
	}

	if _, err := db.ResolveObjectSet(ctx, ObjectSet{Kind: ObjectSetReference, Reference: "smallAirports"}); err == nil {
		t.Fatal("an unknown saved object set must be rejected")
	}
}

func TestResolveObjectSetRejectsInvalidDefinition(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	if _, err := db.ResolveObjectSet(context.Background(), ObjectSet{Kind: ObjectSetBase}); err == nil {
		t.Fatal("expected an invalid object set to be rejected before evaluation")
	}
}

// TestResolveObjectSetRejectsInvalidNestedDefinition checks validation runs
// over the whole tree, not just the root a caller happened to send.
func TestResolveObjectSetRejectsInvalidNestedDefinition(t *testing.T) {
	db := openOntologyTestDB(t)
	seedObjectSetFacilities(t, db)

	_, err := db.ResolveObjectSet(context.Background(), ObjectSet{
		Kind: ObjectSetUnion,
		Operands: []ObjectSet{
			{Kind: ObjectSetBase, ObjectType: "Airport"},
			{Kind: ObjectSetFilter, Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}},
		},
	})
	if err == nil {
		t.Fatal("expected the invalid operand to be rejected")
	}
}

// TestResolveObjectSetInterfaceBaseNeedsAnOntology makes the no-ontology case
// an explicit refusal. Returning nothing would read as "no such facilities".
func TestResolveObjectSetInterfaceBaseNeedsAnOntology(t *testing.T) {
	db := openOntologyTestDB(t)

	_, err := db.ResolveObjectSet(context.Background(),
		ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"})
	if err == nil {
		t.Fatal("interface_base without an active ontology must be rejected")
	}
	if !strings.Contains(err.Error(), "ontology") {
		t.Fatalf("error should name the missing ontology, got %v", err)
	}
}
