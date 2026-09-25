package graph

import (
	"context"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// The OWL rules, asserted as exact inferred sets.
//
// Each test states the whole of what the engine must infer from a small graph,
// not a sample of it, so an extra inference fails as loudly as a missing one.
// Triples are written compactly — ex:, rdf:, rdfs:, owl: — and a named graph,
// when there is one, follows the object.

const (
	owlSymmetric  = owlSymmetricPropertyIRI
	owlTransitive = owlTransitivePropertyIRI
)

func compactTerm(term RDFTerm) string {
	switch term.Kind {
	case RDFTermIRI:
		for prefix, ns := range map[string]string{"ex:": exNS, "rdf:": "http://www.w3.org/1999/02/22-rdf-syntax-ns#", "rdfs:": "http://www.w3.org/2000/01/rdf-schema#", "owl:": owlNamespace} {
			if strings.HasPrefix(term.Value, ns) {
				return prefix + strings.TrimPrefix(term.Value, ns)
			}
		}
	}
	return term.String()
}

func compactTriple(triple RDFTriple) string {
	out := compactTerm(triple.Subject) + " " + compactTerm(triple.Predicate) + " " + compactTerm(triple.Object)
	if triple.Graph != nil {
		out += " " + compactTerm(*triple.Graph)
	}
	return out
}

// inferredLines renders the engine's inferences as sorted "triple  rule" lines.
func inferredLines(records map[string]rdfsInferenceRecord) []string {
	var lines []string
	for _, record := range records {
		if !record.Explicit {
			lines = append(lines, compactTriple(record.Triple)+"  "+record.Rule)
		}
	}
	sort.Strings(lines)
	return lines
}

func assertInferred(t *testing.T, records map[string]rdfsInferenceRecord, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := inferredLines(records)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("inferred set differs\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	checkDerivations(t, records)
}

func TestAnInverseDeclarationWorksInBothDirections(t *testing.T) {
	records := computeRDFSInferenceRecords(explicitFixture(
		infTri(infEx("manages"), owlInverseOfIRI, infEx("reportsTo")),
		infTri(infEx("alice"), exNS+"manages", infEx("bob")),
		infTri(infEx("carol"), exNS+"reportsTo", infEx("dave")),
		// A literal cannot become a subject, so this has no inverse.
		infTri(infEx("erin"), exNS+"manages", NewLiteral("the night shift")),
		// Statements that are themselves inferred, one per direction, so
		// the rule is exercised with the declaration already known and the
		// statement arriving later.
		infTri(infEx("directlyReportsTo"), rdfsSubPropertyOfIRI, infEx("reportsTo")),
		infTri(infEx("frank"), exNS+"directlyReportsTo", infEx("gina")),
		infTri(infEx("directlyManages"), rdfsSubPropertyOfIRI, infEx("manages")),
		infTri(infEx("hana"), exNS+"directlyManages", infEx("ivan")),
	))
	assertInferred(t, records,
		"ex:bob ex:reportsTo ex:alice  owl_inverse_of",
		"ex:dave ex:manages ex:carol  owl_inverse_of",
		"ex:frank ex:reportsTo ex:gina  rdfs_subproperty_application",
		"ex:gina ex:manages ex:frank  owl_inverse_of",
		"ex:hana ex:manages ex:ivan  rdfs_subproperty_application",
		"ex:ivan ex:reportsTo ex:hana  owl_inverse_of",
		"ex:directlyReportsTo rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:reportsTo rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:directlyManages rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:manages rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:directlyReportsTo rdfs:subPropertyOf ex:directlyReportsTo  rdfs_property_reflexive",
		"ex:reportsTo rdfs:subPropertyOf ex:reportsTo  rdfs_property_reflexive",
		"ex:directlyManages rdfs:subPropertyOf ex:directlyManages  rdfs_property_reflexive",
		"ex:manages rdfs:subPropertyOf ex:manages  rdfs_property_reflexive",
	)
}

// A declaration that is itself inferred arrives after the statements it
// governs, and must still reach them — in both directions for inverseOf, and
// for symmetric and transitive declarations made through a class.
func TestAnInferredDeclarationAppliesToStatementsAlreadyKnown(t *testing.T) {
	records := computeRDFSInferenceRecords(explicitFixture(
		infTri(infEx("opposite"), rdfsSubPropertyOfIRI, NewIRI(owlInverseOfIRI)),
		infTri(infEx("manages"), exNS+"opposite", infEx("reportsTo")),
		infTri(infEx("alice"), exNS+"manages", infEx("bob")),
		infTri(infEx("carol"), exNS+"reportsTo", infEx("dave")),
		infTri(infEx("MutualRelation"), rdfsSubClassOfIRI, NewIRI(owlSymmetric)),
		infTri(infEx("knows"), rdfTypeIRI, infEx("MutualRelation")),
		infTri(infEx("erin"), exNS+"knows", infEx("frank")),
		infTri(infEx("ChainRelation"), rdfsSubClassOfIRI, NewIRI(owlTransitive)),
		infTri(infEx("partOf"), rdfTypeIRI, infEx("ChainRelation")),
		infTri(infEx("room"), exNS+"partOf", infEx("floor")),
		infTri(infEx("floor"), exNS+"partOf", infEx("building")),
	))
	assertInferred(t, records,
		"ex:manages owl:inverseOf ex:reportsTo  rdfs_subproperty_application",
		"ex:bob ex:reportsTo ex:alice  owl_inverse_of",
		"ex:dave ex:manages ex:carol  owl_inverse_of",
		"ex:knows rdf:type owl:SymmetricProperty  rdfs_type_via_subclass",
		"ex:frank ex:knows ex:erin  owl_symmetric",
		"ex:partOf rdf:type owl:TransitiveProperty  rdfs_type_via_subclass",
		"ex:room ex:partOf ex:building  owl_transitive",
		"ex:opposite rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"owl:inverseOf rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:opposite rdfs:subPropertyOf ex:opposite  rdfs_property_reflexive",
		"owl:inverseOf rdfs:subPropertyOf owl:inverseOf  rdfs_property_reflexive",
		"ex:MutualRelation rdf:type rdfs:Class  rdfs_subclass_declares_class",
		"owl:SymmetricProperty rdf:type rdfs:Class  rdfs_subclass_declares_class",
		"ex:MutualRelation rdfs:subClassOf ex:MutualRelation  rdfs_class_reflexive",
		"owl:SymmetricProperty rdfs:subClassOf owl:SymmetricProperty  rdfs_class_reflexive",
		"ex:ChainRelation rdf:type rdfs:Class  rdfs_subclass_declares_class",
		"owl:TransitiveProperty rdf:type rdfs:Class  rdfs_subclass_declares_class",
		"ex:ChainRelation rdfs:subClassOf ex:ChainRelation  rdfs_class_reflexive",
		"owl:TransitiveProperty rdfs:subClassOf owl:TransitiveProperty  rdfs_class_reflexive",
	)
}

func TestASymmetricPropertyIsStatedBothWays(t *testing.T) {
	records := computeRDFSInferenceRecords(explicitFixture(
		infTri(infEx("knows"), rdfTypeIRI, NewIRI(owlSymmetric)),
		infTri(infEx("alice"), exNS+"knows", infEx("bob")),
		infQuad(infEx("bob"), exNS+"knows", infEx("carol"), "g1"),
		// An inferred statement, reached only after the declaration.
		infTri(infEx("marriedTo"), rdfsSubPropertyOfIRI, infEx("knows")),
		infTri(infEx("dave"), exNS+"marriedTo", infEx("erin")),
	))
	assertInferred(t, records,
		"ex:bob ex:knows ex:alice  owl_symmetric",
		"ex:carol ex:knows ex:bob ex:g1  owl_symmetric",
		"ex:dave ex:knows ex:erin  rdfs_subproperty_application",
		"ex:erin ex:knows ex:dave  owl_symmetric",
		"ex:marriedTo rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:knows rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:marriedTo rdfs:subPropertyOf ex:marriedTo  rdfs_property_reflexive",
		"ex:knows rdfs:subPropertyOf ex:knows  rdfs_property_reflexive",
	)
}

func TestATransitivePropertyClosesAChainAndTerminatesOnACycle(t *testing.T) {
	records := computeRDFSInferenceRecords(explicitFixture(
		infTri(infEx("partOf"), rdfTypeIRI, NewIRI(owlTransitive)),
		infTri(infEx("room"), exNS+"partOf", infEx("floor")),
		infTri(infEx("floor"), exNS+"partOf", infEx("building")),
		infTri(infEx("building"), exNS+"partOf", infEx("campus")),
		// A two-cycle: each is part of the other, so each is part of itself.
		infTri(infEx("x"), exNS+"partOf", infEx("y")),
		infTri(infEx("y"), exNS+"partOf", infEx("x")),
	))
	assertInferred(t, records,
		"ex:room ex:partOf ex:building  owl_transitive",
		"ex:room ex:partOf ex:campus  owl_transitive",
		"ex:floor ex:partOf ex:campus  owl_transitive",
		"ex:x ex:partOf ex:x  owl_transitive",
		"ex:y ex:partOf ex:y  owl_transitive",
	)
}

// The production graph has node types "project" and "Project"; declaring them
// equivalent must make every instance of one an instance of the other.
func TestEquivalentClassesShareTheirInstances(t *testing.T) {
	records := computeRDFSInferenceRecords(explicitFixture(
		infTri(infEx("project"), owlEquivalentClassIRI, infEx("Project")),
		infTri(infEx("cortexdb"), rdfTypeIRI, infEx("project")),
		infTri(infEx("athanor"), rdfTypeIRI, infEx("Project")),
	))
	assertInferred(t, records,
		"ex:project rdfs:subClassOf ex:Project  owl_equivalent_class",
		"ex:Project rdfs:subClassOf ex:project  owl_equivalent_class",
		"ex:cortexdb rdf:type ex:Project  rdfs_type_via_subclass",
		"ex:athanor rdf:type ex:project  rdfs_type_via_subclass",
		"ex:project rdf:type rdfs:Class  rdfs_subclass_declares_class",
		"ex:Project rdf:type rdfs:Class  rdfs_subclass_declares_class",
		"ex:project rdfs:subClassOf ex:project  rdfs_subclass_transitive",
		"ex:Project rdfs:subClassOf ex:Project  rdfs_subclass_transitive",
	)
}

func TestEquivalentPropertiesShareTheirStatements(t *testing.T) {
	records := computeRDFSInferenceRecords(explicitFixture(
		infTri(infEx("runsOn"), owlEquivalentPropertyIRI, infEx("hostedOn")),
		infTri(infEx("api"), exNS+"runsOn", infEx("vm1")),
	))
	assertInferred(t, records,
		"ex:runsOn rdfs:subPropertyOf ex:hostedOn  owl_equivalent_property",
		"ex:hostedOn rdfs:subPropertyOf ex:runsOn  owl_equivalent_property",
		"ex:api ex:hostedOn ex:vm1  rdfs_subproperty_application",
		"ex:runsOn rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:hostedOn rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:runsOn rdfs:subPropertyOf ex:runsOn  rdfs_subproperty_transitive",
		"ex:hostedOn rdfs:subPropertyOf ex:hostedOn  rdfs_subproperty_transitive",
	)
}

// Three names for one host: the closure relates every pair (and no name to
// itself), and every statement about one name is made about the others, in
// the subject and object positions alike.
func TestSameAsIsClosedAndCopiesStatementsAcrossTheClass(t *testing.T) {
	records := computeRDFSInferenceRecords(explicitFixture(
		infTri(infEx("h1"), owlSameAsIRI, infEx("h2")),
		infTri(infEx("h2"), owlSameAsIRI, infEx("h3")),
		infTri(infEx("h1"), exNS+"ip", NewLiteral("10.0.0.1")),
		infTri(infEx("api"), exNS+"runsOn", infEx("h3")),
	))
	assertInferred(t, records,
		"ex:h2 owl:sameAs ex:h1  owl_same_as_symmetric",
		"ex:h3 owl:sameAs ex:h2  owl_same_as_symmetric",
		"ex:h1 owl:sameAs ex:h3  owl_same_as_transitive",
		"ex:h3 owl:sameAs ex:h1  owl_same_as_transitive",
		"ex:h2 ex:ip \"10.0.0.1\"  owl_same_as_subject",
		"ex:h3 ex:ip \"10.0.0.1\"  owl_same_as_subject",
		"ex:api ex:runsOn ex:h1  owl_same_as_object",
		"ex:api ex:runsOn ex:h2  owl_same_as_object",
	)
}

// Predicates are never rewritten, a literal is never a member of a class, and
// a term is never inferred to be the same as itself.
func TestSameAsNeverRewritesPredicatesOrLiterals(t *testing.T) {
	records := computeRDFSInferenceRecords(explicitFixture(
		infTri(infEx("likes"), owlSameAsIRI, infEx("enjoys")),
		infTri(infEx("alice"), exNS+"likes", infEx("tea")),
		infTri(infEx("bob"), owlSameAsIRI, NewLiteral("Bob")),
		infTri(infEx("bob"), exNS+"age", NewLiteral("40")),
		infTri(infEx("carol"), owlSameAsIRI, infEx("carol")),
	))
	assertInferred(t, records,
		"ex:enjoys owl:sameAs ex:likes  owl_same_as_symmetric",
	)
}

// A class above the cap is reported and left entirely alone, while a class
// under it in the same graph is materialized as usual.
func TestAnOversizedSameAsClassIsReportedNotMaterialized(t *testing.T) {
	explicit := explicitFixture(
		infTri(infEx("a1"), owlSameAsIRI, infEx("a2")),
		infTri(infEx("a2"), owlSameAsIRI, infEx("a3")),
		infTri(infEx("a3"), owlSameAsIRI, infEx("a4")),
		infTri(infEx("a1"), exNS+"name", NewLiteral("A")),
		infTri(infEx("b1"), owlSameAsIRI, infEx("b2")),
		infTri(infEx("b1"), exNS+"name", NewLiteral("B")),
	)
	records, oversized := computeInferenceRecords(explicit, InferenceOptions{MaxSameAsClassSize: 3})
	assertInferred(t, records,
		"ex:b2 owl:sameAs ex:b1  owl_same_as_symmetric",
		"ex:b2 ex:name \"B\"  owl_same_as_subject",
	)
	if len(oversized) != 1 {
		t.Fatalf("oversized = %+v, want exactly the a-class", oversized)
	}
	want := OversizedSameAsClass{Size: 4, Cap: 3, Members: []string{"<" + exNS + "a1>", "<" + exNS + "a2>", "<" + exNS + "a3>", "<" + exNS + "a4>"}}
	if got := oversized[0]; got.Size != want.Size || got.Cap != want.Cap || strings.Join(got.Members, " ") != strings.Join(want.Members, " ") {
		t.Errorf("report = %+v, want %+v", got, want)
	}

	// The default cap is far above four, so the same graph is materialized.
	if _, oversized := computeInferenceRecords(explicit, InferenceOptions{}); len(oversized) != 0 {
		t.Errorf("default cap reported %+v", oversized)
	}
}

// Two classes of two, each materialized in the first round, are joined by a
// sameAs edge that exists only by inference: alias is a sub-property of
// sameAs, so a2 alias b1 becomes a2 sameAs b1 one round after both classes
// have had their closure and copies made. The joined class of four is over a
// cap of three. Stopping at that point would leave both halves' copies behind;
// the engine must instead notice, start again, and infer nothing from the
// class at all.
func TestASameAsClassThatGrowsPastTheCapDuringInferenceIsNotHalfMaterialized(t *testing.T) {
	explicit := explicitFixture(
		infTri(infEx("alias"), rdfsSubPropertyOfIRI, NewIRI(owlSameAsIRI)),
		infTri(infEx("a1"), owlSameAsIRI, infEx("a2")),
		infTri(infEx("b1"), owlSameAsIRI, infEx("b2")),
		infTri(infEx("a2"), exNS+"alias", infEx("b1")),
		infTri(infEx("a1"), exNS+"name", NewLiteral("A")),
	)
	records, oversized := computeInferenceRecords(explicit, InferenceOptions{MaxSameAsClassSize: 3})
	assertInferred(t, records,
		"ex:alias rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"owl:sameAs rdf:type rdf:Property  rdfs_subproperty_declares_property",
		"ex:alias rdfs:subPropertyOf ex:alias  rdfs_property_reflexive",
		"owl:sameAs rdfs:subPropertyOf owl:sameAs  rdfs_property_reflexive",
		"ex:a2 owl:sameAs ex:b1  rdfs_subproperty_application",
	)
	if len(oversized) != 1 || oversized[0].Size != 4 {
		t.Fatalf("oversized = %+v, want one class of four", oversized)
	}
}

// Every OWL rule leaves a trail ExplainTripleTrace can follow from the stored
// inference back to explicit triples, on both databases.
func TestEveryOWLRuleExplainsBackToExplicitTriples(t *testing.T) {
	cases := []struct {
		rule     string
		explicit []RDFTriple
		target   RDFTriple
		leaves   []string
	}{
		{
			rule: owlRuleInverseOf,
			explicit: []RDFTriple{
				infTri(infEx("manages"), owlInverseOfIRI, infEx("reportsTo")),
				infTri(infEx("bob"), exNS+"reportsTo", infEx("alice")),
			},
			target: infTri(infEx("alice"), exNS+"manages", infEx("bob")),
			leaves: []string{"ex:manages owl:inverseOf ex:reportsTo", "ex:bob ex:reportsTo ex:alice"},
		},
		{
			rule: owlRuleSymmetric,
			explicit: []RDFTriple{
				infTri(infEx("knows"), rdfTypeIRI, NewIRI(owlSymmetric)),
				infTri(infEx("alice"), exNS+"knows", infEx("bob")),
			},
			target: infTri(infEx("bob"), exNS+"knows", infEx("alice")),
			leaves: []string{"ex:knows rdf:type owl:SymmetricProperty", "ex:alice ex:knows ex:bob"},
		},
		{
			// Three hops, so the answer rests on an inferred premise and the
			// trace has to pass through it.
			rule: owlRuleTransitive,
			explicit: []RDFTriple{
				infTri(infEx("partOf"), rdfTypeIRI, NewIRI(owlTransitive)),
				infTri(infEx("room"), exNS+"partOf", infEx("floor")),
				infTri(infEx("floor"), exNS+"partOf", infEx("building")),
				infTri(infEx("building"), exNS+"partOf", infEx("campus")),
			},
			target: infTri(infEx("room"), exNS+"partOf", infEx("campus")),
			leaves: []string{"ex:partOf rdf:type owl:TransitiveProperty", "ex:room ex:partOf ex:floor", "ex:floor ex:partOf ex:building", "ex:building ex:partOf ex:campus"},
		},
		{
			rule: owlRuleEquivalentClass,
			explicit: []RDFTriple{
				infTri(infEx("host"), owlEquivalentClassIRI, infEx("Host")),
			},
			target: infTri(infEx("Host"), rdfsSubClassOfIRI, infEx("host")),
			leaves: []string{"ex:host owl:equivalentClass ex:Host"},
		},
		{
			rule: owlRuleEquivalentProperty,
			explicit: []RDFTriple{
				infTri(infEx("runsOn"), owlEquivalentPropertyIRI, infEx("hostedOn")),
			},
			target: infTri(infEx("hostedOn"), rdfsSubPropertyOfIRI, infEx("runsOn")),
			leaves: []string{"ex:runsOn owl:equivalentProperty ex:hostedOn"},
		},
		{
			rule: owlRuleSameAsSymmetric,
			explicit: []RDFTriple{
				infTri(infEx("h1"), owlSameAsIRI, infEx("h2")),
			},
			target: infTri(infEx("h2"), owlSameAsIRI, infEx("h1")),
			leaves: []string{"ex:h1 owl:sameAs ex:h2"},
		},
		{
			rule: owlRuleSameAsTransitive,
			explicit: []RDFTriple{
				infTri(infEx("h1"), owlSameAsIRI, infEx("h2")),
				infTri(infEx("h2"), owlSameAsIRI, infEx("h3")),
			},
			target: infTri(infEx("h1"), owlSameAsIRI, infEx("h3")),
			leaves: []string{"ex:h1 owl:sameAs ex:h2", "ex:h2 owl:sameAs ex:h3"},
		},
		{
			rule: owlRuleSameAsSubject,
			explicit: []RDFTriple{
				infTri(infEx("h1"), owlSameAsIRI, infEx("h2")),
				infTri(infEx("h1"), exNS+"ip", NewLiteral("10.0.0.1")),
			},
			target: infTri(infEx("h2"), exNS+"ip", NewLiteral("10.0.0.1")),
			leaves: []string{"ex:h1 owl:sameAs ex:h2", "ex:h1 ex:ip \"10.0.0.1\""},
		},
		{
			rule: owlRuleSameAsObject,
			explicit: []RDFTriple{
				infTri(infEx("h1"), owlSameAsIRI, infEx("h2")),
				infTri(infEx("api"), exNS+"runsOn", infEx("h1")),
			},
			target: infTri(infEx("api"), exNS+"runsOn", infEx("h2")),
			leaves: []string{"ex:h1 owl:sameAs ex:h2", "ex:api ex:runsOn ex:h1"},
		},
		{
			// Not an OWL rule, but the case the old engine got wrong: a type
			// two subclass steps away rests on an inferred premise, which
			// used to be left out of its supports, so the trace stopped short
			// of one of the three facts it depends on.
			rule: rdfsRuleTypeSubClass,
			explicit: []RDFTriple{
				infTri(infEx("Manager"), rdfsSubClassOfIRI, infEx("Employee")),
				infTri(infEx("Employee"), rdfsSubClassOfIRI, infEx("Person")),
				infTri(infEx("alice"), rdfTypeIRI, infEx("Manager")),
			},
			target: infTri(infEx("alice"), rdfTypeIRI, infEx("Person")),
			leaves: []string{"ex:Manager rdfs:subClassOf ex:Employee", "ex:Employee rdfs:subClassOf ex:Person", "ex:alice rdf:type ex:Manager"},
		},
	}

	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.rule, func(t *testing.T) {
					ctx := context.Background()
					store := b.store
					// Each rule starts from an empty triple store, so the
					// trace can only reach what this case stated.
					if err := store.clearInferredTriples(ctx); err != nil {
						t.Fatalf("clear inferred: %v", err)
					}
					leftovers, err := store.FindTriples(ctx, TriplePattern{})
					if err != nil {
						t.Fatalf("list leftovers: %v", err)
					}
					for _, triple := range leftovers {
						if err := store.DeleteTriple(ctx, triple); err != nil {
							t.Fatalf("reset: %v", err)
						}
					}
					explicit := make([]*RDFTriple, len(tc.explicit))
					for i := range tc.explicit {
						triple := tc.explicit[i]
						explicit[i] = &triple
					}
					if _, err := store.UpsertTriplesBatch(ctx, explicit); err != nil {
						t.Fatalf("upsert: %v", err)
					}
					if _, err := store.RefreshRDFSInferences(ctx); err != nil {
						t.Fatalf("refresh: %v", err)
					}

					inferredOnly := true
					found, err := store.FindTriples(ctx, TriplePattern{
						Subject: &tc.target.Subject, Predicate: &tc.target.Predicate, Object: &tc.target.Object, Inferred: &inferredOnly,
					})
					if err != nil || len(found) != 1 {
						t.Fatalf("find target: %v, %d found", err, len(found))
					}
					explanation, err := store.ExplainTriple(ctx, found[0].ID)
					if err != nil {
						t.Fatalf("explain: %v", err)
					}
					if explanation.Explicit || explanation.Rule != tc.rule {
						t.Fatalf("explanation = %+v, want rule %s", explanation, tc.rule)
					}

					trace, err := store.ExplainTripleTrace(ctx, found[0].ID, 10)
					if err != nil {
						t.Fatalf("trace: %v", err)
					}
					leaves := map[string]bool{}
					for _, entry := range trace {
						if entry.Truncated {
							t.Errorf("trace truncated at %s", compactTriple(entry.Explanation.Triple))
						}
						if entry.Explanation.Explicit {
							leaves[compactTriple(entry.Explanation.Triple)] = true
						}
					}
					var got []string
					for leaf := range leaves {
						got = append(got, leaf)
					}
					sort.Strings(got)
					want := append([]string(nil), tc.leaves...)
					sort.Strings(want)
					if strings.Join(got, "\n") != strings.Join(want, "\n") {
						t.Errorf("trace reaches explicit\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
					}
				})
			}
		})
	}
}

func TestTheInferenceSummaryCountsOWLRules(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			explicit := []*RDFTriple{
				ptrTriple(infTri(infEx("manages"), owlInverseOfIRI, infEx("reportsTo"))),
				ptrTriple(infTri(infEx("alice"), exNS+"manages", infEx("bob"))),
				ptrTriple(infTri(infEx("h1"), owlSameAsIRI, infEx("h2"))),
			}
			if _, err := b.store.UpsertTriplesBatch(ctx, explicit); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			if _, err := b.store.RefreshRDFSInferences(ctx); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			summary, err := b.store.InferenceSummary(ctx)
			if err != nil {
				t.Fatalf("summary: %v", err)
			}
			want := map[string]int{owlRuleInverseOf: 1, owlRuleSameAsSymmetric: 1}
			if summary.InferredCount != 2 || len(summary.Rules) != len(want) {
				t.Fatalf("summary = %+v, want %v", summary, want)
			}
			for rule, n := range want {
				if summary.Rules[rule] != n {
					t.Errorf("rule %s counted %d, want %d", rule, summary.Rules[rule], n)
				}
			}
		})
	}
}

func ptrTriple(triple RDFTriple) *RDFTriple { return &triple }

// The whole ruleset, RDFS and OWL together, against a brute-force fixpoint
// that applies every rule of ruleConclusions to every tuple of known triples
// until nothing changes. It shares no code with the engine's joins, so a
// premise position the engine forgets to index — a statement that arrives
// after its declaration, a sameAs edge that arrives after the statements it
// copies — shows up as a missing triple.
func TestTheRulesetAgreesWithABruteForceFixpointOnRandomGraphs(t *testing.T) {
	for seed := int64(1); seed <= 80; seed++ {
		explicit := randomOWLGraph(seed)
		want := bruteForceFixpoint(explicit)
		records := computeRDFSInferenceRecords(explicit)
		got := inferredTripleSet(records)
		if missing, extra := diffStringSets(want, got); len(missing)+len(extra) > 0 {
			var in []string
			for _, triple := range explicit {
				in = append(in, compactTriple(triple))
			}
			t.Fatalf("seed %d: engine differs from brute force\ninput:\n  %s\nmissing: %v\nextra: %v", seed, strings.Join(in, "\n  "), missing, extra)
		}
		checkDerivations(t, records)
	}
}

func randomOWLGraph(seed int64) []RDFTriple {
	rng := rand.New(rand.NewSource(seed))
	classes := []string{"C0", "C1", "C2"}
	props := []string{"p0", "p1", "p2"}
	things := []string{"a", "b", "c", "d"}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	var triples []RDFTriple
	for i := 0; i < 6+rng.Intn(8); i++ {
		var tr RDFTriple
		switch rng.Intn(11) {
		case 0:
			tr = infTri(infEx(pick(classes)), rdfsSubClassOfIRI, infEx(pick(classes)))
		case 1:
			tr = infTri(infEx(pick(props)), rdfsSubPropertyOfIRI, infEx(pick(props)))
		case 2:
			tr = infTri(infEx(pick(props)), []string{rdfsDomainIRI, rdfsRangeIRI}[rng.Intn(2)], infEx(pick(classes)))
		case 3:
			tr = infTri(infEx(pick(things)), rdfTypeIRI, infEx(pick(classes)))
		case 4, 5:
			if rng.Intn(4) == 0 {
				tr = infTri(infEx(pick(things)), exNS+pick(props), NewLiteral(pick(things)))
			} else {
				tr = infTri(infEx(pick(things)), exNS+pick(props), infEx(pick(things)))
			}
		case 6:
			tr = infTri(infEx(pick(props)), owlInverseOfIRI, infEx(pick(props)))
		case 7:
			tr = infTri(infEx(pick(props)), rdfTypeIRI, NewIRI([]string{owlSymmetric, owlTransitive}[rng.Intn(2)]))
		case 8:
			tr = infTri(infEx(pick(things)), owlSameAsIRI, infEx(pick(things)))
		case 9:
			tr = infTri(infEx(pick(classes)), owlEquivalentClassIRI, infEx(pick(classes)))
		case 10:
			switch rng.Intn(6) {
			case 0:
				tr = infTri(infEx(pick(props)), owlEquivalentPropertyIRI, infEx(pick(props)))
			case 1:
				// A property that makes sameAs statements.
				tr = infTri(infEx(pick(props)), rdfsSubPropertyOfIRI, NewIRI(owlSameAsIRI))
			case 2:
				tr = infTri(infEx(pick(things)), owlSameAsIRI, NewLiteral(pick(things)))
			case 3:
				// A property that makes inverse declarations, so a
				// declaration can arrive after the statements it governs.
				tr = infTri(infEx(pick(props)), rdfsSubPropertyOfIRI, NewIRI(owlInverseOfIRI))
			case 4:
				// A class of symmetric or transitive properties, for the
				// same reason: p rdf:type C then declares p by inference.
				tr = infTri(infEx(pick(classes)), rdfsSubClassOfIRI, NewIRI([]string{owlSymmetric, owlTransitive}[rng.Intn(2)]))
			default:
				tr = infTri(infEx(pick(props)), rdfTypeIRI, infEx(pick(classes)))
			}
		}
		if rng.Intn(4) == 0 {
			graph := infEx([]string{"g1", "g2"}[rng.Intn(2)])
			tr.Graph = &graph
		}
		triples = append(triples, tr)
	}
	return explicitFixture(dedupeTriples(triples)...)
}

// bruteForceFixpoint returns the inferred triples, as strings, that the rules
// derive from the explicit ones. It is quadratic per round for binary rules
// and only tries the ternary transitive rule with a real declaration, which
// keeps it fast on graphs of this size without borrowing any of the engine's
// indexing.
func bruteForceFixpoint(explicit []RDFTriple) map[string]bool {
	known := make(map[string]RDFTriple)
	var all []RDFTriple
	add := func(triple RDFTriple) bool {
		triple = RDFTriple{Subject: triple.Subject, Predicate: triple.Predicate, Object: triple.Object, Graph: cloneGraphTerm(triple.Graph)}
		triple.ID = tripleDigest(triple)
		key := inferenceContentKey(triple)
		if _, ok := known[key]; ok {
			return false
		}
		known[key] = triple
		all = append(all, triple)
		return true
	}
	for _, triple := range explicit {
		add(triple)
	}
	explicitKeys := make(map[string]bool, len(all))
	for key := range known {
		explicitKeys[key] = true
	}
	for changed := true; changed; {
		changed = false
		snapshot := append([]RDFTriple(nil), all...)
		for rule, arity := range ruleArity {
			switch arity {
			case 1:
				for _, a := range snapshot {
					for _, c := range ruleConclusions(rule, []RDFTriple{a}) {
						changed = add(c) || changed
					}
				}
			case 2:
				for _, a := range snapshot {
					for _, b := range snapshot {
						for _, c := range ruleConclusions(rule, []RDFTriple{a, b}) {
							changed = add(c) || changed
						}
					}
				}
			case 3:
				for _, d := range snapshot {
					if d.Predicate.Value != rdfTypeIRI || d.Object.Value != owlTransitive {
						continue
					}
					for _, a := range snapshot {
						for _, b := range snapshot {
							for _, c := range ruleConclusions(rule, []RDFTriple{a, b, d}) {
								changed = add(c) || changed
							}
						}
					}
				}
			}
		}
	}
	out := make(map[string]bool)
	for key, triple := range known {
		if !explicitKeys[key] {
			out[tripleWithoutInference(triple).String()] = true
		}
	}
	return out
}

// The case the OWL rules were added for, end to end on both databases: the
// property graph holds node types "project" and "Project", its projection
// hands them to inference as classes, and one stored equivalentClass statement
// makes every node of either type an instance of both. The explanation must
// reach the projected rdf:type triple, whose id belongs to the projection and
// not to kg_triples.
func TestEquivalentNodeTypesFromThePropertyGraphShareTheirNodes(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			store := b.store
			vector := make([]float32, store.rdfVectorDim())
			vector[0] = 1
			if err := store.UpsertNode(ctx, &GraphNode{ID: "proj:cortexdb", NodeType: "project", Vector: vector}); err != nil {
				t.Fatalf("upsert node: %v", err)
			}
			if err := store.UpsertNode(ctx, &GraphNode{ID: "proj:athanor", NodeType: "Project", Vector: vector}); err != nil {
				t.Fatalf("upsert node: %v", err)
			}
			lower, upper := NewIRI(PropertyTypeNamespace+"project"), NewIRI(PropertyTypeNamespace+"Project")
			if err := store.UpsertTriple(ctx, &RDFTriple{Subject: lower, Predicate: NewIRI(owlEquivalentClassIRI), Object: upper}); err != nil {
				t.Fatalf("upsert equivalence: %v", err)
			}
			if _, err := store.RefreshRDFSInferences(ctx); err != nil {
				t.Fatalf("refresh: %v", err)
			}

			inferredOnly := true
			typ := NewIRI(rdfTypeIRI)
			for node, class := range map[string]RDFTerm{"proj:cortexdb": upper, "proj:athanor": lower} {
				subject := NewIRI(PropertyGraphNodeIRI(node))
				found, err := store.FindTriples(ctx, TriplePattern{Subject: &subject, Predicate: &typ, Object: &class, Inferred: &inferredOnly})
				if err != nil || len(found) != 1 {
					t.Fatalf("%s rdf:type %s: %v, %d found", node, class.Value, err, len(found))
				}
				if found[0].Rule != rdfsRuleTypeSubClass {
					t.Errorf("%s: rule %s, want %s", node, found[0].Rule, rdfsRuleTypeSubClass)
				}
				trace, err := store.ExplainTripleTrace(ctx, found[0].ID, 5)
				if err != nil {
					t.Fatalf("trace: %v", err)
				}
				var projected bool
				for _, entry := range trace {
					if entry.Explanation.Explicit && strings.HasPrefix(entry.TripleID, projectedTripleIDPrefix) &&
						termsEqual(entry.Explanation.Triple.Subject, subject) {
						projected = true
					}
				}
				if !projected {
					t.Errorf("%s: trace never reached the projected rdf:type triple: %+v", node, trace)
				}
			}
		})
	}
}
