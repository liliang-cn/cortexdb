package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// The OWL 2 RL keys, chains and contradictions of owl_rl.go, asserted as exact
// inferred sets and exact reports, and against a brute-force fixpoint.

const (
	owlFunctional        = owlFunctionalPropertyIRI
	owlInverseFunctional = owlInverseFunctionalPropertyIRI
)

// chainAxiomTriples states head owl:propertyChainAxiom (props...) with the
// list built from blank nodes named after the prefix.
func chainAxiomTriples(prefix, head string, props ...string) []RDFTriple {
	cell := func(i int) RDFTerm { return NewBlankNode(fmt.Sprintf("%s%d", prefix, i)) }
	out := []RDFTriple{infTri(infEx(head), owlPropertyChainAxiomIRI, cell(0))}
	for i, prop := range props {
		out = append(out, infTri(cell(i), rdfFirstIRI, infEx(prop)))
		rest := NewIRI(rdfNilIRI)
		if i+1 < len(props) {
			rest = cell(i + 1)
		}
		out = append(out, infTri(cell(i), rdfRestIRI, rest))
	}
	return out
}

func inconsistenciesOf(t *testing.T, explicit []RDFTriple) (map[string]rdfsInferenceRecord, []InferenceInconsistency) {
	t.Helper()
	outcome := computeInferenceOutcome(explicit, InferenceOptions{})
	return outcome.records, outcome.inconsistencies
}

// renderInconsistency is one report as "rule: triple | triple ..." with the
// suspended sameAs after a "~", compact enough to assert whole.
func renderInconsistency(inc InferenceInconsistency) string {
	var parts []string
	for _, triple := range inc.Triples {
		parts = append(parts, compactTriple(triple))
	}
	out := inc.Rule + ": " + strings.Join(parts, " | ")
	if len(inc.SuspendedSameAs) > 0 {
		var suspended []string
		for _, triple := range inc.SuspendedSameAs {
			suspended = append(suspended, compactTriple(triple))
		}
		out += " ~ " + strings.Join(suspended, " | ")
	}
	return out
}

func assertInconsistencies(t *testing.T, got []InferenceInconsistency, want ...string) {
	t.Helper()
	var lines []string
	for _, inc := range got {
		lines = append(lines, renderInconsistency(inc))
	}
	sort.Strings(lines)
	sort.Strings(want)
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("inconsistencies differ\n got:\n  %s\nwant:\n  %s", strings.Join(lines, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestTwoResourceValuesOfAFunctionalPropertyAreTheSameIndividual(t *testing.T) {
	records, inconsistencies := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("worksAt"), rdfTypeIRI, NewIRI(owlFunctional)),
		infTri(infEx("alice"), exNS+"worksAt", infEx("acme")),
		infTri(infEx("alice"), exNS+"worksAt", infEx("acmeInc")),
		infTri(infEx("acme"), exNS+"hq", infEx("berlin")),
	))
	assertInferred(t, records,
		"ex:acme owl:sameAs ex:acmeInc  owl_functional_property",
		"ex:acmeInc owl:sameAs ex:acme  owl_functional_property",
		"ex:acmeInc ex:hq ex:berlin  owl_same_as_subject",
	)
	assertInconsistencies(t, inconsistencies)
}

// The use the rule exists for: two records with the same e-mail address are
// one person, and what is known about either is known about both.
func TestTwoSubjectsWithTheSameInverseFunctionalKeyAreTheSameIndividual(t *testing.T) {
	records, _ := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("email"), rdfTypeIRI, NewIRI(owlInverseFunctional)),
		infTri(infEx("p1"), exNS+"email", NewLiteral("ann@example.com")),
		infTri(infEx("p2"), exNS+"email", NewLiteral("ann@example.com")),
		infTri(infEx("p1"), exNS+"name", NewLiteral("Ann")),
		infTri(infEx("p3"), exNS+"email", NewLiteral("bob@example.com")),
	))
	assertInferred(t, records,
		"ex:p1 owl:sameAs ex:p2  owl_inverse_functional_property",
		"ex:p2 owl:sameAs ex:p1  owl_inverse_functional_property",
		"ex:p2 ex:name \"Ann\"  owl_same_as_subject",
	)
}

// A key declared on a property after the statements that use it arrive — by
// inference, a round later — still merges them.
func TestAKeyDeclaredByInferenceAppliesToStatementsAlreadyKnown(t *testing.T) {
	cases := map[string]struct {
		key        string
		a, b       RDFTriple
		rule, want string
	}{
		"functional": {
			key: owlFunctional, rule: owlRuleFunctional,
			a: infTri(infEx("x"), exNS+"k", infEx("v1")), b: infTri(infEx("x"), exNS+"k", infEx("v2")),
			want: "ex:v1 owl:sameAs ex:v2",
		},
		"inverse functional": {
			key: owlInverseFunctional, rule: owlRuleInverseFunctional,
			a: infTri(infEx("p1"), exNS+"k", NewLiteral("123")), b: infTri(infEx("p2"), exNS+"k", NewLiteral("123")),
			want: "ex:p1 owl:sameAs ex:p2",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			records, _ := inconsistenciesOf(t, explicitFixture(
				infTri(infEx("Key"), rdfsSubClassOfIRI, NewIRI(tc.key)),
				infTri(infEx("k"), rdfTypeIRI, infEx("Key")),
				tc.a, tc.b,
			))
			var sameAs []string
			for _, line := range inferredLines(records) {
				if strings.Contains(line, "owl:sameAs") {
					sameAs = append(sameAs, line)
				}
			}
			reverse := strings.Fields(tc.want)
			want := []string{tc.want + "  " + tc.rule, reverse[2] + " owl:sameAs " + reverse[0] + "  " + tc.rule}
			sort.Strings(want)
			if strings.Join(sameAs, "\n") != strings.Join(want, "\n") {
				t.Errorf("sameAs = %v, want %v", sameAs, want)
			}
			checkDerivations(t, records)
		})
	}
}

// A value that reaches the key a round after another — here through a
// sub-property — is joined to it in both directions by the key itself, not
// left for the symmetric rule to complete.
func TestAValueThatArrivesLaterIsJoinedBothWaysByTheKey(t *testing.T) {
	for name, tc := range map[string]struct {
		key, rule string
		early     RDFTriple
		late      RDFTriple
		want      []string
	}{
		"functional": {
			key: owlFunctional, rule: owlRuleFunctional,
			early: infTri(infEx("x"), exNS+"k", infEx("a")), late: infTri(infEx("x"), exNS+"sub", infEx("b")),
			want: []string{"ex:a owl:sameAs ex:b", "ex:b owl:sameAs ex:a"},
		},
		"inverse functional": {
			key: owlInverseFunctional, rule: owlRuleInverseFunctional,
			early: infTri(infEx("a"), exNS+"k", NewLiteral("v")), late: infTri(infEx("b"), exNS+"sub", NewLiteral("v")),
			want: []string{"ex:a owl:sameAs ex:b", "ex:b owl:sameAs ex:a"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			records, _ := inconsistenciesOf(t, explicitFixture(
				infTri(infEx("k"), rdfTypeIRI, NewIRI(tc.key)),
				infTri(infEx("sub"), rdfsSubPropertyOfIRI, infEx("k")),
				tc.early, tc.late,
			))
			got := map[string]string{}
			for _, record := range records {
				if !record.Explicit && record.Triple.Predicate.Value == owlSameAsIRI {
					got[compactTriple(record.Triple)] = record.Rule
				}
			}
			for _, triple := range tc.want {
				if got[triple] != tc.rule {
					t.Errorf("%s credited to %q, want %s (all: %v)", triple, got[triple], tc.rule, got)
				}
			}
			checkDerivations(t, records)
		})
	}
}

// A disjointness that is itself inferred — stated through a sub-property of
// owl:disjointWith or owl:propertyDisjointWith — arrives after the statements
// it contradicts and still finds them.
func TestADisjointnessDeclaredByInferenceStillFindsItsClash(t *testing.T) {
	_, inconsistencies := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("excludes"), rdfsSubPropertyOfIRI, NewIRI(owlDisjointWithIRI)),
		infTri(infEx("Host"), exNS+"excludes", infEx("Software")),
		infTri(infEx("lima"), rdfTypeIRI, infEx("Host")),
		infTri(infEx("lima"), rdfTypeIRI, infEx("Software")),
		infTri(infEx("excludesProp"), rdfsSubPropertyOfIRI, NewIRI(owlPropertyDisjointWithIRI)),
		infTri(infEx("runs"), exNS+"excludesProp", infEx("runsOn")),
		infTri(infEx("vm"), exNS+"runs", infEx("api")),
		infTri(infEx("vm"), exNS+"runsOn", infEx("api")),
	))
	assertInconsistencies(t, inconsistencies,
		"cax-dw: ex:Host owl:disjointWith ex:Software | ex:lima rdf:type ex:Host | ex:lima rdf:type ex:Software",
		"prp-pdw: ex:runs owl:propertyDisjointWith ex:runsOn | ex:vm ex:runs ex:api | ex:vm ex:runsOn ex:api",
	)
}

// Two different literals for a functional property cannot be merged: that is
// a contradiction, reported with both statements, and nothing is inferred.
func TestTwoLiteralValuesOfAFunctionalPropertyAreReportedNotMerged(t *testing.T) {
	records, inconsistencies := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("birthDate"), rdfTypeIRI, NewIRI(owlFunctional)),
		infTri(infEx("ann"), exNS+"birthDate", NewLiteral("1990-01-01")),
		infTri(infEx("ann"), exNS+"birthDate", NewLiteral("1991-01-01")),
		infTri(infEx("bob"), exNS+"birthDate", NewLiteral("1980-05-05")),
		infTri(infEx("bob"), exNS+"birthDate", infEx("someDay")),
	))
	assertInferred(t, records)
	assertInconsistencies(t, inconsistencies,
		`prp-fp: ex:birthDate rdf:type owl:FunctionalProperty | ex:ann ex:birthDate "1990-01-01" | ex:ann ex:birthDate "1991-01-01"`,
		`prp-fp: ex:birthDate rdf:type owl:FunctionalProperty | ex:bob ex:birthDate ex:someDay | ex:bob ex:birthDate "1980-05-05"`,
	)
	for _, inc := range inconsistencies {
		if !strings.Contains(inc.Explanation, "functional property") {
			t.Errorf("explanation %q does not say why", inc.Explanation)
		}
	}
}

func TestAPropertyChainComposesItsLinks(t *testing.T) {
	explicit := chainAxiomTriples("l", "ultimatelyHostedOn", "runsOn", "hostedOn")
	explicit = append(explicit,
		infTri(infEx("api"), exNS+"runsOn", infEx("vm1")),
		infTri(infEx("db"), exNS+"runsOn", infEx("vm1")),
		infTri(infEx("vm1"), exNS+"hostedOn", infEx("dell")),
		infTri(infEx("vm1"), exNS+"hostedOn", NewLiteral("rack 4")),
		infTri(infEx("web"), exNS+"runsOn", infEx("vm2")),
	)
	records, _ := inconsistenciesOf(t, explicitFixture(explicit...))
	assertInferred(t, records,
		"ex:api ex:ultimatelyHostedOn ex:dell  owl_property_chain",
		"ex:db ex:ultimatelyHostedOn ex:dell  owl_property_chain",
		"ex:api ex:ultimatelyHostedOn \"rack 4\"  owl_property_chain",
		"ex:db ex:ultimatelyHostedOn \"rack 4\"  owl_property_chain",
	)
}

// A chain of three, over data with a cycle, whose own property is also its
// first link: every path has exactly three steps, so the fixpoint is reached,
// and a path that concludes a triple already stated adds nothing.
func TestALongChainOverACycleTerminates(t *testing.T) {
	explicit := chainAxiomTriples("l", "p", "p", "q", "q")
	explicit = append(explicit,
		infTri(infEx("a"), exNS+"p", infEx("b")),
		infTri(infEx("b"), exNS+"q", infEx("c")),
		infTri(infEx("c"), exNS+"q", infEx("b")),
		infTri(infEx("c"), exNS+"q", infEx("d")),
	)
	records, _ := inconsistenciesOf(t, explicitFixture(explicit...))
	assertInferred(t, records,
		"ex:a ex:p ex:d  owl_property_chain",
	)
}

// A list that is not a list — a cycle, a cell with two firsts, a single
// member — is ignored, and the cycle in particular must not hang the engine.
func TestAMalformedChainListIsIgnored(t *testing.T) {
	data := []RDFTriple{
		infTri(infEx("a"), exNS+"p", infEx("b")),
		infTri(infEx("b"), exNS+"q", infEx("c")),
	}
	cyclic := []RDFTriple{
		infTri(infEx("r"), owlPropertyChainAxiomIRI, NewBlankNode("c0")),
		infTri(NewBlankNode("c0"), rdfFirstIRI, infEx("p")),
		infTri(NewBlankNode("c0"), rdfRestIRI, NewBlankNode("c1")),
		infTri(NewBlankNode("c1"), rdfFirstIRI, infEx("q")),
		infTri(NewBlankNode("c1"), rdfRestIRI, NewBlankNode("c0")),
	}
	twoFirsts := append(chainAxiomTriples("d", "r", "p", "q"), infTri(NewBlankNode("d0"), rdfFirstIRI, infEx("s")))
	single := chainAxiomTriples("s", "r", "p")
	for name, axiom := range map[string][]RDFTriple{"cycle": cyclic, "two firsts": twoFirsts, "one member": single} {
		t.Run(name, func(t *testing.T) {
			records, _ := inconsistenciesOf(t, explicitFixture(append(append([]RDFTriple(nil), data...), axiom...)...))
			assertInferred(t, records)
		})
	}
}

func TestAnIndividualOfTwoDisjointClassesIsReported(t *testing.T) {
	records, inconsistencies := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("Host"), owlDisjointWithIRI, infEx("Project")),
		infTri(infEx("VM"), rdfsSubClassOfIRI, infEx("Host")),
		infTri(infEx("x"), rdfTypeIRI, infEx("VM")),
		infTri(infEx("x"), rdfTypeIRI, infEx("Project")),
		infTri(infEx("y"), rdfTypeIRI, infEx("Host")),
		infTri(infEx("Empty"), owlDisjointWithIRI, infEx("Empty")),
		infTri(infEx("z"), rdfTypeIRI, infEx("Empty")),
	))
	assertInconsistencies(t, inconsistencies,
		"cax-dw: ex:Host owl:disjointWith ex:Project | ex:x rdf:type ex:Host | ex:x rdf:type ex:Project",
		"cax-dw: ex:Empty owl:disjointWith ex:Empty | ex:z rdf:type ex:Empty",
	)
	// The inferred type in the report carries its provenance.
	for _, inc := range inconsistencies {
		for _, triple := range inc.Triples {
			if compactTriple(triple) == "ex:x rdf:type ex:Host" && (!triple.Inferred || triple.Rule != rdfsRuleTypeSubClass || len(triple.SupportIDs) != 2) {
				t.Errorf("inferred premise reported without provenance: %+v", triple)
			}
		}
	}
	checkDerivations(t, records)
}

func TestAPairRelatedByTwoDisjointPropertiesIsReported(t *testing.T) {
	_, inconsistencies := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("runs"), owlPropertyDisjointWithIRI, infEx("runsOn")),
		infTri(infEx("vm"), exNS+"runs", infEx("api")),
		infTri(infEx("vm"), exNS+"runsOn", infEx("api")),
		infTri(infEx("api"), exNS+"runsOn", infEx("vm")),
	))
	assertInconsistencies(t, inconsistencies,
		"prp-pdw: ex:runs owl:propertyDisjointWith ex:runsOn | ex:vm ex:runs ex:api | ex:vm ex:runsOn ex:api",
	)
}

// The key says the two values are one individual; differentFrom says they
// are not. Nothing is merged — no sameAs, no copied statement — and the
// report names the declaration, the key's statements and the sameAs the key
// would have drawn.
func TestDifferentFromStopsAKeyFromMergingThePairAndIsReported(t *testing.T) {
	records, inconsistencies := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("runsOn"), rdfTypeIRI, NewIRI(owlFunctional)),
		infTri(infEx("openclaw"), exNS+"runsOn", infEx("nodeA")),
		infTri(infEx("openclaw"), exNS+"runsOn", infEx("nodeB")),
		infTri(infEx("nodeA"), owlDifferentFromIRI, infEx("nodeB")),
		infTri(infEx("nodeA"), exNS+"ip", NewLiteral("10.0.0.1")),
	))
	assertInferred(t, records)
	assertInconsistencies(t, inconsistencies,
		"eq-diff1: ex:nodeA owl:differentFrom ex:nodeB ~ ex:nodeA owl:sameAs ex:nodeB",
	)
	suspended := inconsistencies[0].SuspendedSameAs[0]
	if suspended.Rule != owlRuleFunctional || len(suspended.SupportIDs) != 3 {
		t.Errorf("suspended sameAs without its derivation: %+v", suspended)
	}
	if inconsistencies[0].ClassSize != 2 || !strings.Contains(inconsistencies[0].Explanation, "declared different") {
		t.Errorf("report = %+v", inconsistencies[0])
	}
}

// Stated sameAs edges that join two terms declared different are kept — they
// were stated — but their closure and their copies are not drawn, and the
// report shows the path that joins the pair.
func TestDifferentFromSuspendsAStatedSameAsClass(t *testing.T) {
	records, inconsistencies := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("a"), owlSameAsIRI, infEx("b")),
		infTri(infEx("b"), owlSameAsIRI, infEx("c")),
		infTri(infEx("c"), owlDifferentFromIRI, infEx("a")),
		infTri(infEx("a"), exNS+"name", NewLiteral("A")),
		infTri(infEx("x"), owlSameAsIRI, infEx("y")),
	))
	assertInferred(t, records,
		"ex:y owl:sameAs ex:x  owl_same_as_symmetric",
	)
	assertInconsistencies(t, inconsistencies,
		"eq-diff1: ex:c owl:differentFrom ex:a | ex:b owl:sameAs ex:c | ex:a owl:sameAs ex:b",
	)
}

func TestATermDeclaredDifferentFromItselfIsReported(t *testing.T) {
	_, inconsistencies := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("a"), owlDifferentFromIRI, infEx("a")),
	))
	assertInconsistencies(t, inconsistencies, "eq-diff1: ex:a owl:differentFrom ex:a")
}

// A key joins p1 and p2; q is stated the same as p1 and different from p2.
// The conflict exists only once the key has fired, after the first run has
// already materialized q's class, so it takes a second run: the whole class
// of three is suspended, and nothing about it is closed or copied.
func TestAKeyThatJoinsTermsDeclaredDifferentSuspendsTheWholeClass(t *testing.T) {
	records, inconsistencies := inconsistenciesOf(t, explicitFixture(
		infTri(infEx("email"), rdfTypeIRI, NewIRI(owlInverseFunctional)),
		infTri(infEx("p1"), exNS+"email", NewLiteral("x@example.com")),
		infTri(infEx("p2"), exNS+"email", NewLiteral("x@example.com")),
		infTri(infEx("p1"), exNS+"name", NewLiteral("P")),
		infTri(infEx("q"), owlSameAsIRI, infEx("p1")),
		infTri(infEx("q"), owlDifferentFromIRI, infEx("p2")),
	))
	for _, line := range inferredLines(records) {
		if strings.Contains(line, "owl:sameAs") {
			t.Errorf("sameAs materialized in a suspended class: %s", line)
		}
		if strings.Contains(line, "owl_same_as_subject") || strings.Contains(line, "owl_same_as_object") {
			t.Errorf("statement copied across a suspended class: %s", line)
		}
	}
	if len(inconsistencies) != 1 || inconsistencies[0].Rule != InconsistencyDifferentFrom || inconsistencies[0].ClassSize != 3 {
		t.Fatalf("inconsistencies = %+v", inconsistencies)
	}
	checkDerivations(t, records)
}

// The new rules against a brute-force fixpoint that knows nothing of the
// engine's indexes, deltas, or its way of joining a key group: it applies
// every rule to every tuple of triples, suspends the classes it finds in
// conflict and starts again, and checks the contradictions by scanning. The
// reports must agree as exactly as the triples.
func TestTheOWLRLRulesAgreeWithABruteForceFixpointOnRandomGraphs(t *testing.T) {
	const graphs = 300
	var withConflict, withChain, withSameAs, withSuspension int
	for seed := int64(1); seed <= graphs; seed++ {
		explicit := randomOWLRLGraph(seed)
		wantInferred, wantInconsistent := bruteForceOWL(explicit)
		outcome := computeInferenceOutcome(explicit, InferenceOptions{})
		got := inferredTripleSet(outcome.records)
		gotInconsistent := inconsistencyKeys(outcome.inconsistencies)
		missing, extra := diffStringSets(wantInferred, got)
		missingI, extraI := diffStringSets(wantInconsistent, gotInconsistent)
		if len(missing)+len(extra)+len(missingI)+len(extraI) > 0 {
			var in []string
			for _, triple := range explicit {
				in = append(in, compactTriple(triple))
			}
			t.Fatalf("seed %d: engine differs from brute force\ninput:\n  %s\nmissing: %v\nextra: %v\nmissing inconsistencies: %v\nextra inconsistencies: %v",
				seed, strings.Join(in, "\n  "), missing, extra, missingI, extraI)
		}
		checkDerivations(t, outcome.records)
		checkReportedTriples(t, outcome)
		if len(outcome.inconsistencies) > 0 {
			withConflict++
		}
		for _, inc := range outcome.inconsistencies {
			if inc.Rule == InconsistencyDifferentFrom && inc.ClassSize > 0 {
				withSuspension++
				break
			}
		}
		for _, record := range outcome.records {
			if record.Rule == owlRulePropertyChain {
				withChain++
				break
			}
		}
		for _, record := range outcome.records {
			if record.Rule == owlRuleFunctional || record.Rule == owlRuleInverseFunctional {
				withSameAs++
				break
			}
		}
	}
	// A generator that never reaches a rule proves nothing about it.
	if withConflict < graphs/5 || withChain < graphs/10 || withSameAs < graphs/12 || withSuspension < graphs/20 {
		t.Fatalf("random graphs too tame: %d with inconsistencies, %d with chains, %d with key sameAs, %d with a suspended class",
			withConflict, withChain, withSameAs, withSuspension)
	}
	t.Logf("%d graphs: %d with inconsistencies, %d with chain inferences, %d with key sameAs, %d with a suspended class",
		graphs, withConflict, withChain, withSameAs, withSuspension)
}

// checkReportedTriples holds every report to its ids: each conflicting
// triple is in the output under the id it is reported with, and each
// suspended sameAs rests on triples that are.
func checkReportedTriples(t *testing.T, outcome inferenceOutcome) {
	t.Helper()
	byID := make(map[string]string, len(outcome.records))
	for _, record := range outcome.records {
		byID[record.Triple.ID] = inferenceContentKey(record.Triple)
	}
	for _, inc := range outcome.inconsistencies {
		for _, triple := range inc.Triples {
			if byID[triple.ID] != inferenceContentKey(triple) {
				t.Errorf("%s: reported triple %s is not in the output under id %s", inc.Rule, compactTriple(triple), triple.ID)
			}
		}
		for _, triple := range inc.SuspendedSameAs {
			if _, stored := byID[triple.ID]; stored {
				t.Errorf("%s: suspended %s was stored", inc.Rule, compactTriple(triple))
			}
			for _, id := range triple.SupportIDs {
				if _, ok := byID[id]; !ok {
					t.Errorf("%s: suspended %s rests on %s, which is not in the output", inc.Rule, compactTriple(triple), id)
				}
			}
		}
	}
}

func TestOWLRLInferenceIsDeterministicWhateverTheInputOrder(t *testing.T) {
	for _, seed := range []int64{3, 11, 42} {
		explicit := randomOWLRLGraph(seed)
		render := func(in []RDFTriple) string {
			outcome := computeInferenceOutcome(in, InferenceOptions{})
			report, _ := json.Marshal(outcome.inconsistencies)
			return strings.Join(renderRecordsWithSupport(outcome.records), "\n") + "\n" + string(report)
		}
		first := render(explicit)
		rng := rand.New(rand.NewSource(seed))
		for i := 0; i < 10; i++ {
			shuffled := append([]RDFTriple(nil), explicit...)
			rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
			if render(shuffled) != first {
				t.Fatalf("seed %d shuffle %d changed the output", seed, i)
			}
		}
	}
}

func randomOWLRLGraph(seed int64) []RDFTriple {
	rng := rand.New(rand.NewSource(seed))
	classes := []string{"C0", "C1", "C2"}
	props := []string{"p0", "p1", "p2"}
	things := []string{"a", "b", "c", "d"}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	var triples []RDFTriple
	for i := 0; i < 8+rng.Intn(12); i++ {
		var batch []RDFTriple
		switch c := rng.Intn(20); {
		case c >= 16:
			// A key or a chain again: both need a declaration and data that
			// meets it, so they are drawn more often than the rest.
			if c%2 == 0 {
				batch = append(batch, infTri(infEx(pick(props)), rdfTypeIRI, NewIRI([]string{owlFunctional, owlInverseFunctional}[rng.Intn(2)])))
			} else {
				batch = append(batch, chainAxiomTriples(fmt.Sprintf("s%dk%d_", seed, i), pick(props), pick(props), pick(props))...)
			}
		case c <= 3:
			if rng.Intn(4) == 0 {
				batch = append(batch, infTri(infEx(pick(things)), exNS+pick(props), NewLiteral(pick(things))))
			} else {
				batch = append(batch, infTri(infEx(pick(things)), exNS+pick(props), infEx(pick(things))))
			}
		case c == 4:
			batch = append(batch, infTri(infEx(pick(things)), rdfTypeIRI, infEx(pick(classes))))
		case c == 5:
			batch = append(batch, infTri(infEx(pick(props)), rdfTypeIRI, NewIRI([]string{owlFunctional, owlInverseFunctional}[rng.Intn(2)])))
		case c == 6:
			n := 2 + rng.Intn(2)
			links := make([]string, n)
			for j := range links {
				links[j] = pick(props)
			}
			chain := chainAxiomTriples(fmt.Sprintf("s%dc%d_", seed, i), pick(props), links...)
			switch rng.Intn(8) {
			case 0:
				// A cycle: the last cell's rest points back to the first.
				last := len(chain) - 1
				chain[last] = infTri(chain[last].Subject, rdfRestIRI, chain[0].Object)
			case 1:
				// A cell with a second first.
				chain = append(chain, infTri(chain[0].Object, rdfFirstIRI, infEx(pick(props))))
			}
			batch = append(batch, chain...)
		case c == 7:
			batch = append(batch, infTri(infEx(pick(classes)), owlDisjointWithIRI, infEx(pick(classes))))
		case c == 8:
			batch = append(batch, infTri(infEx(pick(props)), owlPropertyDisjointWithIRI, infEx(pick(props))))
		case c == 9:
			batch = append(batch, infTri(infEx(pick(things)), owlDifferentFromIRI, infEx(pick(things))))
		case c == 10:
			batch = append(batch, infTri(infEx(pick(things)), owlSameAsIRI, infEx(pick(things))))
		case c == 11:
			batch = append(batch, infTri(infEx(pick(classes)), rdfsSubClassOfIRI, infEx(pick(classes))))
		case c == 12:
			batch = append(batch, infTri(infEx(pick(props)), rdfsSubPropertyOfIRI, infEx(pick(props))))
		case c == 13:
			switch rng.Intn(4) {
			case 0:
				batch = append(batch, infTri(infEx(pick(props)), owlInverseOfIRI, infEx(pick(props))))
			case 1:
				batch = append(batch, infTri(infEx(pick(props)), rdfTypeIRI, NewIRI([]string{owlSymmetric, owlTransitive}[rng.Intn(2)])))
			case 2:
				// A class of keys, so a key is declared by inference.
				batch = append(batch, infTri(infEx(pick(classes)), rdfsSubClassOfIRI, NewIRI([]string{owlFunctional, owlInverseFunctional}[rng.Intn(2)])))
			default:
				batch = append(batch, infTri(infEx(pick(props)), rdfTypeIRI, infEx(pick(classes))))
			}
		case c == 14:
			// A property whose statements are differentFrom, sameAs or
			// disjointness statements, so any of them can arrive by
			// inference, after what it governs.
			batch = append(batch, infTri(infEx(pick(props)), rdfsSubPropertyOfIRI,
				NewIRI([]string{owlDifferentFromIRI, owlSameAsIRI, owlDisjointWithIRI, owlPropertyDisjointWithIRI}[rng.Intn(4)])))
		case c == 15:
			batch = append(batch, infTri(infEx(pick(props)), []string{rdfsDomainIRI, rdfsRangeIRI}[rng.Intn(2)], infEx(pick(classes))))
		}
		if rng.Intn(5) == 0 && len(batch) == 1 {
			graph := infEx([]string{"g1", "g2"}[rng.Intn(2)])
			batch[0].Graph = &graph
		}
		triples = append(triples, batch...)
	}
	return explicitFixture(dedupeTriples(triples)...)
}

// testReadList reads an RDF list of IRIs from the given triples: exactly one
// distinct rdf:first and rdf:rest per cell, no cycle, at least two members.
// It is written apart from the engine's reader so a broken reader cannot be
// agreed with.
func testReadList(triples []RDFTriple, head RDFTerm) ([]RDFTerm, []RDFTriple, bool) {
	var items []RDFTerm
	var cells []RDFTriple
	seen := map[string]bool{}
	for current := head; !(current.Kind == RDFTermIRI && current.Value == rdfNilIRI); {
		if seen[current.String()] {
			return nil, nil, false
		}
		seen[current.String()] = true
		var firsts, rests []RDFTriple
		for _, tr := range triples {
			if !termsEqual(tr.Subject, current) {
				continue
			}
			switch tr.Predicate.Value {
			case rdfFirstIRI:
				if len(firsts) == 0 || !termsEqual(firsts[0].Object, tr.Object) {
					firsts = append(firsts, tr)
				}
			case rdfRestIRI:
				if len(rests) == 0 || !termsEqual(rests[0].Object, tr.Object) {
					rests = append(rests, tr)
				}
			}
		}
		if len(firsts) != 1 || len(rests) != 1 || firsts[0].Object.Kind != RDFTermIRI {
			return nil, nil, false
		}
		items = append(items, firsts[0].Object)
		cells = append(cells, firsts[0], rests[0])
		current = rests[0].Object
	}
	if len(items) < 2 {
		return nil, nil, false
	}
	return items, cells, true
}

// testAgreeingGraph is the graph a multi-premise conclusion lands in: the one
// every premise outside the default graph names, else the default graph.
func testAgreeingGraph(premises []RDFTriple) *RDFTerm {
	var graph *RDFTerm
	for _, p := range premises {
		if p.Graph == nil {
			continue
		}
		if graph != nil && !termsEqual(*graph, *p.Graph) {
			return nil
		}
		graph = p.Graph
	}
	return cloneGraphTerm(graph)
}

// chainConcludes is ruleConcludes for the property chain, whose arity is the
// chain's length plus the axiom and its list: one support is the axiom, the
// list is read from the supports, and the rest must form a path through the
// chain, every one of them used, that concludes the target.
func chainConcludes(supports []RDFTriple, target RDFTriple) bool {
	for _, axiom := range supports {
		if axiom.Predicate.Value != owlPropertyChainAxiomIRI || !termsEqual(axiom.Subject, target.Predicate) {
			continue
		}
		links, cells, ok := testReadList(supports, axiom.Object)
		if !ok {
			continue
		}
		listed := map[string]bool{axiom.ID: true}
		for _, cell := range cells {
			listed[cell.ID] = true
		}
		var uses []RDFTriple
		for _, s := range supports {
			if !listed[s.ID] {
				uses = append(uses, s)
			}
		}
		path := make([]RDFTriple, len(links))
		var walk func(j int) bool
		walk = func(j int) bool {
			if j == len(links) {
				used := map[string]bool{}
				for _, p := range path {
					used[p.ID] = true
				}
				conclusion := RDFTriple{Subject: path[0].Subject, Predicate: axiom.Subject, Object: path[len(path)-1].Object, Graph: testAgreeingGraph(path)}
				return len(used) == len(uses) && inferenceContentKey(conclusion) == inferenceContentKey(target)
			}
			for _, u := range uses {
				if termsEqual(u.Predicate, links[j]) && (j == 0 || termsEqual(path[j-1].Object, u.Subject)) {
					path[j] = u
					if walk(j + 1) {
						return true
					}
				}
			}
			return false
		}
		if walk(0) {
			return true
		}
	}
	return false
}

// bruteForceRun applies every rule of ruleConclusions, and every property
// chain, to every tuple of known triples until nothing changes. sameAs
// reasoning is withheld from suspended terms: the closure and copying rules
// do not use an edge that touches one, and a key's sameAs that would is set
// aside as a candidate.
func bruteForceRun(explicit []RDFTriple, suspended map[string]bool) (map[string]RDFTriple, map[string]bool, []RDFTriple) {
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
	candidateKeys := map[string]bool{}
	var candidates []RDFTriple
	free := func(t RDFTriple) bool {
		return !suspended[engineTermKey(t.Subject)] && !suspended[engineTermKey(t.Object)]
	}
	declares := map[string]bool{owlTransitive: true, owlFunctional: true, owlInverseFunctional: true}

	for changed := true; changed; {
		changed = false
		snapshot := append([]RDFTriple(nil), all...)
		apply := func(rule string, premises []RDFTriple) {
			switch rule {
			case owlRuleSameAsSymmetric:
				if !free(premises[0]) {
					return
				}
			case owlRuleSameAsTransitive:
				if !free(premises[0]) || !free(premises[1]) {
					return
				}
			case owlRuleSameAsSubject, owlRuleSameAsObject:
				if !free(premises[1]) {
					return
				}
			}
			for _, c := range ruleConclusions(rule, premises) {
				if (rule == owlRuleFunctional || rule == owlRuleInverseFunctional) && !free(c) {
					if key := inferenceContentKey(c); !candidateKeys[key] {
						candidateKeys[key] = true
						candidates = append(candidates, c)
					}
					continue
				}
				changed = add(c) || changed
			}
		}
		for rule, arity := range ruleArity {
			switch arity {
			case 1:
				for _, a := range snapshot {
					apply(rule, []RDFTriple{a})
				}
			case 2:
				for _, a := range snapshot {
					for _, b := range snapshot {
						apply(rule, []RDFTriple{a, b})
					}
				}
			case 3:
				for _, d := range snapshot {
					if d.Predicate.Value != rdfTypeIRI || !declares[d.Object.Value] {
						continue
					}
					for _, a := range snapshot {
						for _, b := range snapshot {
							apply(rule, []RDFTriple{a, b, d})
						}
					}
				}
			}
		}
		for _, axiom := range snapshot {
			if axiom.Predicate.Value != owlPropertyChainAxiomIRI || axiom.Subject.Kind != RDFTermIRI {
				continue
			}
			links, _, ok := testReadList(explicit, axiom.Object)
			if !ok {
				continue
			}
			path := make([]RDFTriple, len(links))
			var walk func(j int)
			walk = func(j int) {
				if j == len(links) {
					changed = add(RDFTriple{Subject: path[0].Subject, Predicate: axiom.Subject, Object: path[j-1].Object, Graph: testAgreeingGraph(path)}) || changed
					return
				}
				for _, u := range snapshot {
					if termsEqual(u.Predicate, links[j]) && (j == 0 || termsEqual(path[j-1].Object, u.Subject)) {
						path[j] = u
						walk(j + 1)
					}
				}
			}
			walk(0)
		}
	}
	return known, explicitKeys, candidates
}

// bruteForceOWL is the whole ruleset with contradictions: brute-force runs
// repeated until every sameAs class an owl:differentFrom contradicts is
// suspended, then the inferred triples and the contradictions of the last
// run, found by scanning.
func bruteForceOWL(explicit []RDFTriple) (map[string]bool, map[string]bool) {
	suspended := map[string]bool{}
	for {
		known, explicitKeys, candidates := bruteForceRun(explicit, suspended)
		parent := map[string]string{}
		var find func(string) string
		find = func(k string) string {
			if _, ok := parent[k]; !ok {
				parent[k] = k
			}
			if parent[k] != k {
				parent[k] = find(parent[k])
			}
			return parent[k]
		}
		edges := append([]RDFTriple(nil), candidates...)
		for _, triple := range known {
			if individualsSameAs(triple) {
				edges = append(edges, triple)
			}
		}
		for _, e := range edges {
			parent[find(engineTermKey(e.Subject))] = find(engineTermKey(e.Object))
		}
		var differents []RDFTriple
		for _, triple := range known {
			if triple.Predicate.Value == owlDifferentFromIRI && isResourceTerm(triple.Subject) && isResourceTerm(triple.Object) {
				differents = append(differents, triple)
			}
		}
		conflictedRoots := map[string]bool{}
		inconsistent := map[string]bool{}
		for _, d := range differents {
			s, o := engineTermKey(d.Subject), engineTermKey(d.Object)
			if s == o {
				inconsistent[InconsistencyDifferentFrom+"|"+d.String()] = true
				continue
			}
			if find(s) == find(o) {
				conflictedRoots[find(s)] = true
				inconsistent[InconsistencyDifferentFrom+"|"+d.String()] = true
			}
		}
		grew := false
		for key := range parent {
			if conflictedRoots[find(key)] && !suspended[key] {
				suspended[key] = true
				grew = true
			}
		}
		if grew {
			continue
		}

		clash := func(rule string, triples ...RDFTriple) {
			set := map[string]bool{}
			for _, t := range triples {
				set[tripleWithoutInference(RDFTriple{Subject: t.Subject, Predicate: t.Predicate, Object: t.Object, Graph: t.Graph}).String()] = true
			}
			var parts []string
			for s := range set {
				parts = append(parts, s)
			}
			sort.Strings(parts)
			inconsistent[rule+"|"+strings.Join(parts, "|")] = true
		}
		for _, d := range known {
			switch {
			case d.Predicate.Value == owlDisjointWithIRI && isResourceTerm(d.Subject) && isResourceTerm(d.Object):
				for _, a := range known {
					if a.Predicate.Value != rdfTypeIRI || !termsEqual(a.Object, d.Subject) {
						continue
					}
					for _, b := range known {
						if b.Predicate.Value == rdfTypeIRI && termsEqual(b.Subject, a.Subject) && termsEqual(b.Object, d.Object) {
							clash(InconsistencyDisjointClasses, d, a, b)
						}
					}
				}
			case d.Predicate.Value == owlPropertyDisjointWithIRI && d.Subject.Kind == RDFTermIRI && d.Object.Kind == RDFTermIRI:
				for _, a := range known {
					if !termsEqual(a.Predicate, d.Subject) {
						continue
					}
					for _, b := range known {
						if termsEqual(b.Predicate, d.Object) && termsEqual(a.Subject, b.Subject) && termsEqual(a.Object, b.Object) {
							clash(InconsistencyDisjointProperties, d, a, b)
						}
					}
				}
			case d.Predicate.Value == rdfTypeIRI && d.Object.Kind == RDFTermIRI && d.Object.Value == owlFunctional && d.Subject.Kind == RDFTermIRI:
				for _, a := range known {
					if !termsEqual(a.Predicate, d.Subject) {
						continue
					}
					for _, b := range known {
						if termsEqual(b.Predicate, d.Subject) && termsEqual(a.Subject, b.Subject) && !termsEqual(a.Object, b.Object) &&
							!(isResourceTerm(a.Object) && isResourceTerm(b.Object)) {
							clash(InconsistencyFunctionalValues, d, a, b)
						}
					}
				}
			}
		}

		inferred := map[string]bool{}
		for key, triple := range known {
			if !explicitKeys[key] {
				inferred[tripleWithoutInference(triple).String()] = true
			}
		}
		return inferred, inconsistent
	}
}

// inconsistencyKeys renders reports the way bruteForceOWL keys them: the rule
// and the set of conflicting statements, or for eq-diff1 the differentFrom
// statement alone, since the joining path is one choice among several.
func inconsistencyKeys(list []InferenceInconsistency) map[string]bool {
	out := map[string]bool{}
	for _, inc := range list {
		if inc.Rule == InconsistencyDifferentFrom {
			out[inc.Rule+"|"+tripleWithoutInference(inc.Triples[0]).String()] = true
			continue
		}
		set := map[string]bool{}
		for _, t := range inc.Triples {
			set[tripleWithoutInference(t).String()] = true
		}
		var parts []string
		for s := range set {
			parts = append(parts, s)
		}
		sort.Strings(parts)
		out[inc.Rule+"|"+strings.Join(parts, "|")] = true
	}
	return out
}

// The refresh result carries the report on both databases, every triple in
// it can be fetched by its id, and nothing was merged.
func TestARefreshReportsContradictionsAndMergesNothingOnBothBackends(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			explicit := []*RDFTriple{
				ptrTriple(infTri(infEx("name"), rdfTypeIRI, NewIRI(owlFunctional))),
				ptrTriple(infTri(infEx("vm"), exNS+"name", NewLiteral("apps"))),
				ptrTriple(infTri(infEx("vm"), exNS+"name", NewLiteral("apps-vm-105"))),
				ptrTriple(infTri(infEx("deployedOn"), rdfTypeIRI, NewIRI(owlFunctional))),
				ptrTriple(infTri(infEx("argus"), exNS+"deployedOn", infEx("h1"))),
				ptrTriple(infTri(infEx("argus"), exNS+"deployedOn", infEx("h2"))),
				ptrTriple(infTri(infEx("h1"), owlDifferentFromIRI, infEx("h2"))),
				ptrTriple(infTri(infEx("h1"), exNS+"ip", NewLiteral("10.0.0.1"))),
			}
			if _, err := b.store.UpsertTriplesBatch(ctx, explicit); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			result, err := b.store.RefreshRDFSInferences(ctx)
			if err != nil {
				t.Fatalf("refresh: %v", err)
			}
			if result.InconsistencyCount != 2 || len(result.Inconsistencies) != 2 {
				t.Fatalf("inconsistencies = %d %+v, want 2", result.InconsistencyCount, result.Inconsistencies)
			}
			assertInconsistencies(t, result.Inconsistencies,
				"eq-diff1: ex:h1 owl:differentFrom ex:h2 ~ ex:h1 owl:sameAs ex:h2",
				`prp-fp: ex:name rdf:type owl:FunctionalProperty | ex:vm ex:name "apps" | ex:vm ex:name "apps-vm-105"`,
			)
			for _, inc := range result.Inconsistencies {
				for _, triple := range inc.Triples {
					stored, err := b.store.GetTriple(ctx, triple.ID)
					if err != nil || compactTriple(*stored) != compactTriple(triple) {
						t.Errorf("%s: %s not fetchable by id %s: %v", inc.Rule, compactTriple(triple), triple.ID, err)
					}
				}
				for _, triple := range inc.SuspendedSameAs {
					for _, id := range triple.SupportIDs {
						if _, err := b.store.GetTriple(ctx, id); err != nil {
							t.Errorf("suspended %s rests on %s: %v", compactTriple(triple), id, err)
						}
					}
				}
			}
			sameAs := NewIRI(owlSameAsIRI)
			merged, err := b.store.FindTriples(ctx, TriplePattern{Predicate: &sameAs})
			if err != nil || len(merged) != 0 {
				t.Errorf("sameAs stored despite the contradictions: %v %+v", err, merged)
			}
			if result.InferredCount != 0 {
				t.Errorf("inferred %d triples, want none", result.InferredCount)
			}
		})
	}
}
