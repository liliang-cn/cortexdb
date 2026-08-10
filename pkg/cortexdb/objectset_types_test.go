package cortexdb

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObjectSetJSONRoundTripNested(t *testing.T) {
	set := ObjectSet{
		Kind: ObjectSetIntersect,
		Operands: []ObjectSet{
			{
				Kind:   ObjectSetFilter,
				Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"},
				Where: &ObjectSetPredicate{
					Op: PredicateAnd,
					Operands: []ObjectSetPredicate{
						{Op: PredicateGte, Property: "capacity", Value: "1000"},
						{Op: PredicateContainsAllTerms, Property: "facilityName", Value: "london heathrow"},
					},
				},
			},
			{
				Kind:   ObjectSetSearchAround,
				Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"},
				Link:   "origin",
			},
		},
	}

	encoded, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ObjectSet
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Operands[0].Source.ObjectType != "Airport" {
		t.Fatalf("nested source lost: %+v", decoded.Operands[0].Source)
	}
	if len(decoded.Operands[0].Where.Operands) != 2 {
		t.Fatalf("nested predicate lost: %+v", decoded.Operands[0].Where)
	}
	if decoded.Operands[1].Link != "origin" {
		t.Fatalf("search around link lost: %q", decoded.Operands[1].Link)
	}
}

// TestObjectSetJSONWireNamesTheSourceKey pins the wire format. Without it a
// `json:"-"` on Source — which is what the recursive-type workaround the plan
// sketched would need — still round-trips in-process while dropping the
// nested set entirely for any MCP caller.
func TestObjectSetJSONWireNamesTheSourceKey(t *testing.T) {
	encoded, err := json.Marshal(ObjectSet{
		Kind:   ObjectSetSearchAround,
		Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"},
		Link:   "origin",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"source":{"kind":"base"`) {
		t.Fatalf("source must serialize under the \"source\" key, got %s", encoded)
	}

	var decoded ObjectSet
	if err := json.Unmarshal([]byte(`{"kind":"search_around","link":"origin","source":{"kind":"base","object_type":"Flight"}}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Source == nil || decoded.Source.ObjectType != "Flight" {
		t.Fatalf("source must decode from the \"source\" key, got %+v", decoded.Source)
	}
}

func TestValidateObjectSetRejectsMissingSource(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: ObjectSetFilter}, 0); err == nil {
		t.Fatal("filter without a source must be rejected")
	}
}

func TestValidateObjectSetRejectsFilterWithoutPredicate(t *testing.T) {
	err := validateObjectSet(ObjectSet{
		Kind:   ObjectSetFilter,
		Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"},
	}, 0)
	if err == nil {
		t.Fatal("filter without a where clause must be rejected")
	}
	if !strings.Contains(err.Error(), "where") {
		t.Fatalf("error should name the missing clause, got %v", err)
	}
}

func TestValidateObjectSetRejectsBaseWithoutObjectType(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: ObjectSetBase}, 0); err == nil {
		t.Fatal("base without an object type must be rejected")
	}
}

func TestValidateObjectSetRejectsInterfaceBaseWithoutInterfaceType(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: ObjectSetInterfaceBase}, 0); err == nil {
		t.Fatal("interface_base without an interface type must be rejected")
	}
}

func TestValidateObjectSetRejectsStaticWithoutIDs(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: ObjectSetStatic}, 0); err == nil {
		t.Fatal("static without object ids must be rejected")
	}
}

func TestValidateObjectSetRejectsReferenceWithoutName(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: ObjectSetReference}, 0); err == nil {
		t.Fatal("reference without a name must be rejected")
	}
}

func TestValidateObjectSetRejectsUnknownKind(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: "everything"}, 0); err == nil {
		t.Fatal("an unknown kind must be rejected")
	}
}

func TestValidateObjectSetRejectsSearchAroundWithoutLink(t *testing.T) {
	err := validateObjectSet(ObjectSet{
		Kind:   ObjectSetSearchAround,
		Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"},
	}, 0)
	if err == nil {
		t.Fatal("search_around without a link side must be rejected")
	}
}

func TestValidateObjectSetRejectsExcessSearchAroundDepth(t *testing.T) {
	// Four chained search-arounds; Foundry caps this at three.
	set := ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"}
	for i := 0; i < 4; i++ {
		source := set
		set = ObjectSet{Kind: ObjectSetSearchAround, Source: &source, Link: "origin"}
	}
	err := validateObjectSet(set, 0)
	if err == nil {
		t.Fatal("more than 3 chained search-arounds must be rejected")
	}
}

func TestValidateObjectSetAcceptsThreeSearchArounds(t *testing.T) {
	set := ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"}
	for i := 0; i < 3; i++ {
		source := set
		set = ObjectSet{Kind: ObjectSetSearchAround, Source: &source, Link: "origin"}
	}
	if err := validateObjectSet(set, 0); err != nil {
		t.Fatalf("3 search-arounds is the documented limit, got %v", err)
	}
}

// TestValidateObjectSetCountsSearchAroundsPerBranch keeps the depth counter
// from being a global tally: two independent two-hop branches under a union
// are four search-arounds in total but only two deep, and must be accepted.
func TestValidateObjectSetCountsSearchAroundsPerBranch(t *testing.T) {
	branch := func() ObjectSet {
		set := ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"}
		for i := 0; i < 2; i++ {
			source := set
			set = ObjectSet{Kind: ObjectSetSearchAround, Source: &source, Link: "origin"}
		}
		return set
	}
	if err := validateObjectSet(ObjectSet{
		Kind:     ObjectSetUnion,
		Operands: []ObjectSet{branch(), branch()},
	}, 0); err != nil {
		t.Fatalf("two 2-hop branches are within the limit, got %v", err)
	}
}

func TestValidateObjectSetRejectsEmptySetOperation(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: ObjectSetUnion}, 0); err == nil {
		t.Fatal("union with no operands must be rejected")
	}
}

func TestValidateObjectSetRejectsSingleOperandSetOperation(t *testing.T) {
	for _, kind := range []ObjectSetKind{ObjectSetUnion, ObjectSetIntersect, ObjectSetSubtract} {
		err := validateObjectSet(ObjectSet{
			Kind:     kind,
			Operands: []ObjectSet{{Kind: ObjectSetBase, ObjectType: "Airport"}},
		}, 0)
		if err == nil {
			t.Fatalf("%s with one operand must be rejected", kind)
		}
	}
}

func TestValidateObjectSetRejectsInvalidOperand(t *testing.T) {
	err := validateObjectSet(ObjectSet{
		Kind: ObjectSetUnion,
		Operands: []ObjectSet{
			{Kind: ObjectSetBase, ObjectType: "Airport"},
			{Kind: ObjectSetBase},
		},
	}, 0)
	if err == nil {
		t.Fatal("an invalid operand must fail the whole set")
	}
}

func TestValidateObjectSetRejectsInvalidNestedSource(t *testing.T) {
	err := validateObjectSet(ObjectSet{
		Kind:   ObjectSetFilter,
		Source: &ObjectSet{Kind: ObjectSetBase},
		Where:  &ObjectSetPredicate{Op: PredicateEq, Property: "iataCode", Value: "LHR"},
	}, 0)
	if err == nil {
		t.Fatal("an invalid source must fail the filter that wraps it")
	}
}

func TestValidateObjectSetPredicateRequiresOperands(t *testing.T) {
	cases := []struct {
		name      string
		predicate ObjectSetPredicate
	}{
		{"and with one operand", ObjectSetPredicate{Op: PredicateAnd, Operands: []ObjectSetPredicate{
			{Op: PredicateEq, Property: "a", Value: "b"},
		}}},
		{"or with one operand", ObjectSetPredicate{Op: PredicateOr, Operands: []ObjectSetPredicate{
			{Op: PredicateEq, Property: "a", Value: "b"},
		}}},
		{"not with two operands", ObjectSetPredicate{Op: PredicateNot, Operands: []ObjectSetPredicate{
			{Op: PredicateEq, Property: "a", Value: "b"},
			{Op: PredicateEq, Property: "c", Value: "d"},
		}}},
		{"not with no operand", ObjectSetPredicate{Op: PredicateNot}},
	}
	for _, tc := range cases {
		if err := validateObjectSetPredicate(tc.predicate); err == nil {
			t.Fatalf("%s must be rejected", tc.name)
		}
	}
}

// TestValidateObjectSetPredicateChecksBooleanOperands stops the boolean cases
// from being a shape check only: a malformed leaf nested under and/or/not has
// to be rejected too, or it surfaces at evaluation time instead.
func TestValidateObjectSetPredicateChecksBooleanOperands(t *testing.T) {
	cases := map[string]ObjectSetPredicate{
		"and": {Op: PredicateAnd, Operands: []ObjectSetPredicate{
			{Op: PredicateEq, Property: "a", Value: "b"},
			{Op: PredicateEq, Property: "", Value: "b"},
		}},
		"or": {Op: PredicateOr, Operands: []ObjectSetPredicate{
			{Op: PredicateEq, Property: "a", Value: "b"},
			{Op: "nonsense"},
		}},
		"not": {Op: PredicateNot, Operands: []ObjectSetPredicate{
			{Op: PredicateIn, Property: "a"},
		}},
	}
	for name, predicate := range cases {
		if err := validateObjectSetPredicate(predicate); err == nil {
			t.Fatalf("%s must reject its malformed operand", name)
		}
	}
}

func TestValidateObjectSetPredicateLeafRequirements(t *testing.T) {
	cases := []struct {
		name      string
		predicate ObjectSetPredicate
		wantErr   bool
	}{
		{"eq without value", ObjectSetPredicate{Op: PredicateEq, Property: "iataCode"}, true},
		{"eq without property", ObjectSetPredicate{Op: PredicateEq, Value: "LHR"}, true},
		{"eq complete", ObjectSetPredicate{Op: PredicateEq, Property: "iataCode", Value: "LHR"}, false},
		{"is_null without property", ObjectSetPredicate{Op: PredicateIsNull}, true},
		{"is_null complete", ObjectSetPredicate{Op: PredicateIsNull, Property: "capacity"}, false},
		{"in without values", ObjectSetPredicate{Op: PredicateIn, Property: "iataCode"}, true},
		{"in complete", ObjectSetPredicate{Op: PredicateIn, Property: "iataCode", Values: []string{"LHR"}}, false},
		{"unknown op", ObjectSetPredicate{Op: "matches", Property: "iataCode", Value: "LHR"}, true},
		{"knn without property", ObjectSetPredicate{Op: PredicateNearestNeighbors, Value: "hub"}, true},
		{"knn without value or vector", ObjectSetPredicate{Op: PredicateNearestNeighbors, Property: "embedding"}, true},
		{"knn with vector only", ObjectSetPredicate{Op: PredicateNearestNeighbors, Property: "embedding", Vector: []float32{0.1}}, false},
		{"knn with value only", ObjectSetPredicate{Op: PredicateNearestNeighbors, Property: "embedding", Value: "hub"}, false},
		{"knn with k over the cap", ObjectSetPredicate{Op: PredicateNearestNeighbors, Property: "embedding", Value: "hub", K: 101}, true},
		{"knn at the k cap", ObjectSetPredicate{Op: PredicateNearestNeighbors, Property: "embedding", Value: "hub", K: 100}, false},
		{"knn with negative k", ObjectSetPredicate{Op: PredicateNearestNeighbors, Property: "embedding", Value: "hub", K: -1}, true},
	}
	for _, tc := range cases {
		err := validateObjectSetPredicate(tc.predicate)
		if tc.wantErr && err == nil {
			t.Fatalf("%s: expected rejection", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: expected acceptance, got %v", tc.name, err)
		}
	}
}
