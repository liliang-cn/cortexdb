package graph

import (
	"fmt"
	"strings"
	"testing"
)

// The OWL 2 RL rules of owl_rl_rules.go, one fixture per rule family. Each
// asserts the conclusions the rule must draw (among whatever the RDFS rules
// add around them), that every conclusion's supports are in the output, and
// the exact contradictions reported.

// listTriples states subject predicate (members...) with the list built
// from blank nodes named after the prefix.
func listTriples(prefix string, subject RDFTerm, predicate string, members ...RDFTerm) []RDFTriple {
	cell := func(i int) RDFTerm { return NewBlankNode(fmt.Sprintf("%s%d", prefix, i)) }
	out := []RDFTriple{infTri(subject, predicate, cell(0))}
	for i, m := range members {
		out = append(out, infTri(cell(i), rdfFirstIRI, m))
		rest := NewIRI(rdfNilIRI)
		if i+1 < len(members) {
			rest = cell(i + 1)
		}
		out = append(out, infTri(cell(i), rdfRestIRI, rest))
	}
	return out
}

func assertInferredIncludes(t *testing.T, records map[string]rdfsInferenceRecord, want ...string) {
	t.Helper()
	got := map[string]bool{}
	for _, line := range inferredLines(records) {
		got[line] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing inference %q\n got:\n  %s", w, strings.Join(inferredLines(records), "\n  "))
		}
	}
	byID := map[string]bool{}
	for _, record := range records {
		byID[record.Triple.ID] = true
	}
	for _, record := range records {
		for _, id := range record.SupportIDs {
			if !byID[id] {
				t.Errorf("%s (%s): support %s is not in the output", compactTriple(record.Triple), record.Rule, id)
			}
		}
	}
}

func assertNotInferred(t *testing.T, records map[string]rdfsInferenceRecord, triple string) {
	t.Helper()
	for _, line := range inferredLines(records) {
		if strings.HasPrefix(line, triple+"  ") {
			t.Errorf("unexpected inference %q", line)
		}
	}
}

func lit(n int) RDFTerm {
	return NewTypedLiteral(fmt.Sprint(n), XSDNamespace+"nonNegativeInteger")
}

func TestRestrictionsClassifyAndPropagate(t *testing.T) {
	r := func(name string) RDFTerm { return infEx(name) }
	records, inconsistencies := inconsistenciesOf(t, explicitFixture(
		// Parent ≡ ∃hasChild.Person (cls-svf1)
		infTri(r("Parent"), owlSomeValuesFromIRI, r("Person")),
		infTri(r("Parent"), owlOnPropertyIRI, r("hasChild")),
		infTri(r("ann"), exNS+"hasChild", r("bob")),
		infTri(r("bob"), rdfTypeIRI, r("Person")),
		// HasPet ≡ ∃ownsPet.Thing (cls-svf2)
		infTri(r("HasPet"), owlSomeValuesFromIRI, NewIRI(owlThingIRI)),
		infTri(r("HasPet"), owlOnPropertyIRI, r("ownsPet")),
		infTri(r("cat"), exNS+"ownsPet", r("tom")),
		// VeganShop: everything it sells is Vegan (cls-avf)
		infTri(r("VeganShop"), owlAllValuesFromIRI, r("Vegan")),
		infTri(r("VeganShop"), owlOnPropertyIRI, r("sells")),
		infTri(r("greens"), rdfTypeIRI, r("VeganShop")),
		infTri(r("greens"), exNS+"sells", r("tofu")),
		// Berliner ≡ livesIn value berlin (cls-hv1, cls-hv2)
		infTri(r("Berliner"), owlHasValueIRI, r("berlin")),
		infTri(r("Berliner"), owlOnPropertyIRI, r("livesIn")),
		infTri(r("carl"), rdfTypeIRI, r("Berliner")),
		infTri(r("dora"), exNS+"livesIn", r("berlin")),
	))
	assertInferredIncludes(t, records,
		"ex:ann rdf:type ex:Parent  owl_some_values_from",
		"ex:cat rdf:type ex:HasPet  owl_some_values_from",
		"ex:tofu rdf:type ex:Vegan  owl_all_values_from",
		"ex:carl ex:livesIn ex:berlin  owl_has_value",
		"ex:dora rdf:type ex:Berliner  owl_has_value",
	)
	assertNotInferred(t, records, "ex:bob rdf:type ex:Parent")
	assertInconsistencies(t, inconsistencies)
}

func TestIntersectionsUnionsAndEnumerations(t *testing.T) {
	r := func(name string) RDFTerm { return infEx(name) }
	triples := explicitFixture(
		infTri(r("alice"), rdfTypeIRI, r("Woman")),
		infTri(r("alice"), rdfTypeIRI, r("Parent")),
		infTri(r("beth"), rdfTypeIRI, r("Mother")),
		infTri(r("carl"), rdfTypeIRI, r("Man")),
	)
	triples = append(triples, listTriples("int", r("Mother"), owlIntersectionOfIRI, r("Woman"), r("Parent"))...)
	triples = append(triples, listTriples("uni", r("Person"), owlUnionOfIRI, r("Woman"), r("Man"))...)
	triples = append(triples, listTriples("one", r("Weekend"), owlOneOfIRI, r("saturday"), r("sunday"))...)
	records, inconsistencies := inconsistenciesOf(t, triples)
	assertInferredIncludes(t, records,
		"ex:alice rdf:type ex:Mother  owl_intersection", // cls-int1
		"ex:beth rdf:type ex:Woman  owl_intersection",   // cls-int2
		"ex:beth rdf:type ex:Parent  owl_intersection",
		"ex:carl rdf:type ex:Person  owl_union", // cls-uni
		"ex:saturday rdf:type ex:Weekend  owl_one_of",
		"ex:sunday rdf:type ex:Weekend  owl_one_of",
		"ex:Mother rdfs:subClassOf ex:Woman  owl_schema_list", // scm-int
		"ex:Man rdfs:subClassOf ex:Person  owl_schema_list",   // scm-uni
	)
	assertInconsistencies(t, inconsistencies)
}

func TestCardinalityAndKeysMergeIndividuals(t *testing.T) {
	r := func(name string) RDFTerm { return infEx(name) }
	triples := explicitFixture(
		// One birth mother at most (cls-maxc2).
		infTri(r("OneMother"), owlMaxCardinalityIRI, lit(1)),
		infTri(r("OneMother"), owlOnPropertyIRI, r("birthMother")),
		infTri(r("kid"), rdfTypeIRI, r("OneMother")),
		infTri(r("kid"), exNS+"birthMother", r("mum")),
		infTri(r("kid"), exNS+"birthMother", r("mother")),
		// At most one capital that is a City (cls-maxqc3).
		infTri(r("OneCapital"), owlMaxQualifiedCardinalityIRI, lit(1)),
		infTri(r("OneCapital"), owlOnPropertyIRI, r("capital")),
		infTri(r("OneCapital"), owlOnClassIRI, r("City")),
		infTri(r("france"), rdfTypeIRI, r("OneCapital")),
		infTri(r("france"), exNS+"capital", r("paris")),
		infTri(r("france"), exNS+"capital", r("parisFR")),
		infTri(r("paris"), rdfTypeIRI, r("City")),
		infTri(r("parisFR"), rdfTypeIRI, r("City")),
		// A key: two Persons with the same passport are one (prp-key).
		infTri(r("p1"), rdfTypeIRI, r("Citizen")),
		infTri(r("p2"), rdfTypeIRI, r("Citizen")),
		infTri(r("p1"), exNS+"passport", NewLiteral("X1")),
		infTri(r("p2"), exNS+"passport", NewLiteral("X1")),
		infTri(r("p1"), exNS+"country", r("fr")),
		infTri(r("p2"), exNS+"country", r("fr")),
		infTri(r("p3"), rdfTypeIRI, r("Citizen")),
		infTri(r("p3"), exNS+"passport", NewLiteral("X1")),
		infTri(r("p3"), exNS+"country", r("de")),
	)
	triples = append(triples, listTriples("key", r("Citizen"), owlHasKeyIRI, r("passport"), r("country"))...)
	records, inconsistencies := inconsistenciesOf(t, triples)
	assertInferredIncludes(t, records,
		"ex:mum owl:sameAs ex:mother  owl_max_cardinality",
		"ex:mother owl:sameAs ex:mum  owl_max_cardinality",
		"ex:paris owl:sameAs ex:parisFR  owl_max_cardinality",
		"ex:p1 owl:sameAs ex:p2  owl_has_key",
		"ex:p2 owl:sameAs ex:p1  owl_has_key",
	)
	assertNotInferred(t, records, "ex:p1 owl:sameAs ex:p3")
	assertInconsistencies(t, inconsistencies)
}

func TestTheRestOfTheContradictionsAreReported(t *testing.T) {
	r := func(name string) RDFTerm { return infEx(name) }
	triples := explicitFixture(
		infTri(r("parentOf"), rdfTypeIRI, NewIRI(owlIrreflexivePropertyIRI)),
		infTri(r("x"), exNS+"parentOf", r("x")),
		infTri(r("olderThan"), rdfTypeIRI, NewIRI(owlAsymmetricPropertyIRI)),
		infTri(r("a"), exNS+"olderThan", r("b")),
		infTri(r("b"), exNS+"olderThan", r("a")),
		infTri(r("ghost"), rdfTypeIRI, NewIRI(owlNothingIRI)),
		infTri(r("Alive"), owlComplementOfIRI, r("Dead")),
		infTri(r("cat"), rdfTypeIRI, r("Alive")),
		infTri(r("cat"), rdfTypeIRI, r("Dead")),
		infTri(r("Childless"), owlMaxCardinalityIRI, lit(0)),
		infTri(r("Childless"), owlOnPropertyIRI, r("hasChild")),
		infTri(r("monk"), rdfTypeIRI, r("Childless")),
		infTri(r("monk"), exNS+"hasChild", r("son")),
		// A negative property assertion: bill does not know joe.
		infTri(NewBlankNode("npa"), owlSourceIndividualIRI, r("bill")),
		infTri(NewBlankNode("npa"), owlAssertionPropertyIRI, r("knows")),
		infTri(NewBlankNode("npa"), owlTargetIndividualIRI, r("joe")),
		infTri(r("bill"), exNS+"knows", r("joe")),
		infTri(NewBlankNode("dis"), rdfTypeIRI, NewIRI(owlAllDisjointClassesIRI)),
		infTri(r("tom"), rdfTypeIRI, r("Cat")),
		infTri(r("tom"), rdfTypeIRI, r("Dog")),
		infTri(NewBlankNode("dif"), rdfTypeIRI, NewIRI(owlAllDifferentIRI)),
		infTri(r("ann"), owlSameAsIRI, r("anne")),
	)
	triples = append(triples, listTriples("dc", NewBlankNode("dis"), owlMembersIRI, r("Cat"), r("Dog"), r("Cow"))...)
	triples = append(triples, listTriples("df", NewBlankNode("dif"), owlDistinctMembersIRI, r("ann"), r("anne"), r("bea"))...)
	_, inconsistencies := inconsistenciesOf(t, triples)
	rules := map[string]int{}
	for _, inc := range inconsistencies {
		rules[inc.Rule]++
		if inc.Explanation == "" {
			t.Errorf("%s has no explanation", inc.Rule)
		}
	}
	for _, want := range []string{InconsistencyIrreflexive, InconsistencyAsymmetric, InconsistencyNothing, InconsistencyComplement,
		InconsistencyMaxCardinality, InconsistencyNegativeAssertion, InconsistencyAllDisjointClasses, InconsistencyAllDifferent} {
		if rules[want] == 0 {
			t.Errorf("no %s inconsistency reported; got %v", want, rules)
		}
	}
	if rules[InconsistencyAsymmetric] != 1 {
		t.Errorf("one asymmetric pair is one report, got %d", rules[InconsistencyAsymmetric])
	}
}

func TestAllDisjointPropertiesIsAContradiction(t *testing.T) {
	r := func(name string) RDFTerm { return infEx(name) }
	triples := explicitFixture(
		infTri(NewBlankNode("adp"), rdfTypeIRI, NewIRI(owlAllDisjointPropertiesIRI)),
		infTri(r("a"), exNS+"likes", r("b")),
		infTri(r("a"), exNS+"hates", r("b")),
	)
	triples = append(triples, listTriples("p", NewBlankNode("adp"), owlMembersIRI, r("likes"), r("hates"))...)
	_, inconsistencies := inconsistenciesOf(t, triples)
	if len(inconsistencies) != 1 || inconsistencies[0].Rule != InconsistencyAllDisjointProperties {
		t.Fatalf("want one prp-adp report, got %+v", inconsistencies)
	}
}

func TestSchemaRulesOverRestrictions(t *testing.T) {
	r := func(name string) RDFTerm { return infEx(name) }
	records, _ := inconsistenciesOf(t, explicitFixture(
		// scm-svf1: ∃p.Dog ⊑ ∃p.Animal when Dog ⊑ Animal
		infTri(r("OwnsDog"), owlSomeValuesFromIRI, r("Dog")),
		infTri(r("OwnsDog"), owlOnPropertyIRI, r("owns")),
		infTri(r("OwnsAnimal"), owlSomeValuesFromIRI, r("Animal")),
		infTri(r("OwnsAnimal"), owlOnPropertyIRI, r("owns")),
		infTri(r("Dog"), rdfsSubClassOfIRI, r("Animal")),
		// scm-hv: hasValue over a subproperty
		infTri(r("LivesInRome"), owlHasValueIRI, r("rome")),
		infTri(r("LivesInRome"), owlOnPropertyIRI, r("livesIn")),
		infTri(r("LocatedInRome"), owlHasValueIRI, r("rome")),
		infTri(r("LocatedInRome"), owlOnPropertyIRI, r("locatedIn")),
		infTri(r("livesIn"), rdfsSubPropertyOfIRI, r("locatedIn")),
		// scm-avf1
		infTri(r("OnlyDogs"), owlAllValuesFromIRI, r("Dog")),
		infTri(r("OnlyDogs"), owlOnPropertyIRI, r("owns")),
		infTri(r("OnlyAnimals"), owlAllValuesFromIRI, r("Animal")),
		infTri(r("OnlyAnimals"), owlOnPropertyIRI, r("owns")),
	))
	assertInferredIncludes(t, records,
		"ex:OwnsDog rdfs:subClassOf ex:OwnsAnimal  owl_schema_restriction",
		"ex:LivesInRome rdfs:subClassOf ex:LocatedInRome  owl_schema_restriction",
		"ex:OnlyDogs rdfs:subClassOf ex:OnlyAnimals  owl_schema_restriction",
	)
}

func TestClassExpressionVocabularyIsMaintainedByRecomputing(t *testing.T) {
	for _, triple := range []RDFTriple{
		infTri(infEx("R"), owlOnPropertyIRI, infEx("p")),
		infTri(infEx("C"), owlIntersectionOfIRI, NewBlankNode("l")),
		infTri(infEx("p"), rdfTypeIRI, NewIRI(owlIrreflexivePropertyIRI)),
	} {
		if !isRecomputeVocabulary(triple) {
			t.Errorf("%s should be maintained by recomputing", compactTriple(triple))
		}
	}
	if isRecomputeVocabulary(infTri(infEx("a"), exNS+"p", infEx("b"))) {
		t.Error("an ordinary statement is not recompute vocabulary")
	}
}
