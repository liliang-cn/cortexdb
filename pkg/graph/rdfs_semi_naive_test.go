package graph

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
)

// The semi-naive engine is held to the old engine's answers on the golden
// fixtures: the same inferred triples, no more and no fewer, and for each one
// a rule the old engine also credited it to.
func TestTheSemiNaiveEngineReproducesTheOldEnginesInferences(t *testing.T) {
	for _, fx := range rdfsGoldenFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			content, err := os.ReadFile(goldenPath(fx.name))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			want := parseGolden(string(content))
			records := computeRDFSInferenceRecords(fx.explicit)

			got := make(map[string]string)
			for _, line := range renderInferred(records) {
				triple, rule, _ := strings.Cut(line, "\t")
				got[triple] = rule
			}
			for triple, rules := range want {
				rule, ok := got[triple]
				if !ok {
					t.Errorf("missing inference %s (old engine: %s)", triple, strings.Join(rules, "|"))
					continue
				}
				if !containsString(rules, rule) {
					t.Errorf("%s credited to %s; the old engine credited it to %s", triple, rule, strings.Join(rules, "|"))
				}
			}
			for triple, rule := range got {
				if _, ok := want[triple]; !ok {
					t.Errorf("unexpected inference %s (%s)", triple, rule)
				}
			}
			checkDerivations(t, records)
		})
	}
}

// Random graphs over a small vocabulary collide constantly — the same class
// reached by several paths, properties that are their own super-properties,
// schema statements used as data — which is where a join that forgets a
// premise position shows up. The oracle is the old engine, verbatim.
func TestSemiNaiveEvaluationAgreesWithTheNaiveEngineOnRandomGraphs(t *testing.T) {
	for seed := int64(1); seed <= 150; seed++ {
		explicit := randomRDFSGraph(seed)
		want := inferredTripleSet(naiveRDFSInferenceRecords(explicit))
		records := computeRDFSInferenceRecords(explicit)
		got := inferredTripleSet(records)
		if missing, extra := diffStringSets(want, got); len(missing)+len(extra) > 0 {
			t.Fatalf("seed %d: semi-naive differs from naive\nmissing: %v\nextra: %v", seed, missing, extra)
		}
		checkDerivations(t, records)
	}
}

// The engine credits a triple derivable two ways to the same rule on every
// run and for every input order, which the old engine did not: its snapshot
// came from map iteration.
func TestInferenceIsDeterministicWhateverTheInputOrder(t *testing.T) {
	explicit := randomRDFSGraph(7)
	first := renderRecordsWithSupport(computeRDFSInferenceRecords(explicit))
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 20; i++ {
		shuffled := append([]RDFTriple(nil), explicit...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := renderRecordsWithSupport(computeRDFSInferenceRecords(shuffled)); strings.Join(got, "\n") != strings.Join(first, "\n") {
			t.Fatalf("shuffle %d changed the output", i)
		}
	}
}

func randomRDFSGraph(seed int64) []RDFTriple {
	rng := rand.New(rand.NewSource(seed))
	classes := []string{"C0", "C1", "C2", "C3", "C4", "C5"}
	props := []string{"p0", "p1", "p2", "p3"}
	things := []string{"a", "b", "c", "d"}
	graphs := []string{"", "", "g1", "g2"}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	var triples []RDFTriple
	for i := 0; i < 8+rng.Intn(14); i++ {
		var tr RDFTriple
		switch rng.Intn(8) {
		case 0, 1:
			tr = infTri(infEx(pick(classes)), rdfsSubClassOfIRI, infEx(pick(classes)))
		case 2:
			tr = infTri(infEx(pick(props)), rdfsSubPropertyOfIRI, infEx(pick(props)))
		case 3:
			tr = infTri(infEx(pick(props)), rdfsDomainIRI, infEx(pick(classes)))
		case 4:
			tr = infTri(infEx(pick(props)), rdfsRangeIRI, infEx(pick(classes)))
		case 5:
			tr = infTri(infEx(pick(things)), rdfTypeIRI, infEx(pick(classes)))
		case 6:
			if rng.Intn(3) == 0 {
				tr = infTri(infEx(pick(things)), exNS+pick(props), NewLiteral(pick(things)))
			} else {
				tr = infTri(infEx(pick(things)), exNS+pick(props), infEx(pick(things)))
			}
		case 7:
			switch rng.Intn(3) {
			case 0:
				tr = infTri(infEx(pick(classes)), rdfTypeIRI, NewIRI(rdfsClassIRI))
			case 1:
				tr = infTri(infEx(pick(props)), rdfTypeIRI, NewIRI(rdfPropertyIRI))
			default:
				// A property that is itself subPropertyOf a schema property.
				tr = infTri(infEx(pick(props)), rdfsSubPropertyOfIRI, NewIRI(rdfsSubClassOfIRI))
			}
		}
		if g := pick(graphs); g != "" {
			graph := infEx(g)
			tr.Graph = &graph
		}
		triples = append(triples, tr)
	}
	return explicitFixture(dedupeTriples(triples)...)
}

func inferredTripleSet(records map[string]rdfsInferenceRecord) map[string]bool {
	out := make(map[string]bool)
	for _, record := range records {
		if !record.Explicit {
			out[tripleWithoutInference(record.Triple).String()] = true
		}
	}
	return out
}

func diffStringSets(want, got map[string]bool) (missing, extra []string) {
	for key := range want {
		if !got[key] {
			missing = append(missing, key)
		}
	}
	for key := range got {
		if !want[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func renderRecordsWithSupport(records map[string]rdfsInferenceRecord) []string {
	var lines []string
	for _, record := range records {
		if record.Explicit {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", tripleWithoutInference(record.Triple).String(), record.Rule, strings.Join(record.SupportIDs, ",")))
	}
	sort.Strings(lines)
	return lines
}

// checkDerivations proves every inferred record's explanation: each support id
// names a record in the output, and the record's rule, applied to those
// supports and nothing else, concludes exactly this triple. The rules are
// restated here declaratively rather than borrowed from the engine, so an
// engine that credits the wrong rule, lists the wrong premises, or puts the
// conclusion in the wrong graph fails here even when the set of triples is
// right.
func checkDerivations(t *testing.T, records map[string]rdfsInferenceRecord) {
	t.Helper()
	byID := make(map[string]RDFTriple, len(records))
	for _, record := range records {
		byID[record.Triple.ID] = tripleWithoutInference(record.Triple)
	}
	for _, record := range records {
		if record.Explicit {
			continue
		}
		target := tripleWithoutInference(record.Triple)
		if record.Triple.ID != tripleDigest(RDFTriple{Subject: target.Subject, Predicate: target.Predicate, Object: target.Object, Graph: target.Graph}) {
			t.Errorf("%s: id %s is not the id the store will assign", target, record.Triple.ID)
		}
		supports := make([]RDFTriple, 0, len(record.SupportIDs))
		for _, id := range record.SupportIDs {
			support, ok := byID[id]
			if !ok {
				t.Errorf("%s (%s): support %s is not in the output", target, record.Rule, id)
				continue
			}
			supports = append(supports, support)
		}
		if len(supports) != len(record.SupportIDs) {
			continue
		}
		if !ruleConcludes(record.Rule, supports, target) {
			t.Errorf("%s: rule %s does not conclude it from its supports %v", target, record.Rule, supports)
		}
	}
}

// ruleConcludes tries every assignment of the supports to the rule's premises
// (a premise may be used twice, as in x subClassOf x joined with itself, and
// every support must be used) and reports whether any assignment concludes the
// target.
func ruleConcludes(rule string, supports []RDFTriple, target RDFTriple) bool {
	arity, ok := ruleArity[rule]
	if !ok || len(supports) == 0 || len(supports) > arity {
		return false
	}
	var try func(chosen []RDFTriple) bool
	try = func(chosen []RDFTriple) bool {
		if len(chosen) == arity {
			used := make(map[string]bool)
			for _, c := range chosen {
				used[c.ID] = true
			}
			if len(used) != len(supports) {
				return false
			}
			for _, conclusion := range ruleConclusions(rule, chosen) {
				if inferenceContentKey(conclusion) == inferenceContentKey(target) {
					return true
				}
			}
			return false
		}
		for _, s := range supports {
			if try(append(chosen, s)) {
				return true
			}
		}
		return false
	}
	return try(nil)
}

var ruleArity = map[string]int{
	rdfsRuleSubclassClass:     1,
	rdfsRuleSubpropProperty:   1,
	rdfsRuleDomainSchema:      1,
	rdfsRuleRangeSchema:       1,
	rdfsRuleClassReflexive:    1,
	rdfsRulePropReflexive:     1,
	rdfsRuleSubClass:          2,
	rdfsRuleSubProperty:       2,
	rdfsRuleTypeSubClass:      2,
	rdfsRuleSubPropertyUse:    2,
	rdfsRuleDomain:            2,
	rdfsRuleRange:             2,
	owlRuleInverseOf:          2,
	owlRuleSymmetric:          2,
	owlRuleTransitive:         3,
	owlRuleEquivalentClass:    1,
	owlRuleEquivalentProperty: 1,
	owlRuleSameAsSymmetric:    1,
	owlRuleSameAsTransitive:   2,
	owlRuleSameAsSubject:      2,
	owlRuleSameAsObject:       2,
}

func ruleConclusions(rule string, p []RDFTriple) []RDFTriple {
	is := func(term RDFTerm, iri string) bool { return term.Kind == RDFTermIRI && term.Value == iri }
	mk := func(s RDFTerm, pred RDFTerm, o RDFTerm, g *RDFTerm) RDFTriple {
		return RDFTriple{Subject: s, Predicate: pred, Object: o, Graph: g}
	}
	typ, class, prop := NewIRI(rdfTypeIRI), NewIRI(rdfsClassIRI), NewIRI(rdfPropertyIRI)
	sc, sp, same := NewIRI(rdfsSubClassOfIRI), NewIRI(rdfsSubPropertyOfIRI), NewIRI(owlSameAsIRI)
	a := p[0]
	var b, c RDFTriple
	if len(p) > 1 {
		b = p[1]
	}
	if len(p) > 2 {
		c = p[2]
	}
	switch rule {
	case rdfsRuleSubclassClass:
		if is(a.Predicate, rdfsSubClassOfIRI) {
			return []RDFTriple{mk(a.Subject, typ, class, a.Graph), mk(a.Object, typ, class, a.Graph)}
		}
	case rdfsRuleSubpropProperty:
		if is(a.Predicate, rdfsSubPropertyOfIRI) {
			return []RDFTriple{mk(a.Subject, typ, prop, a.Graph), mk(a.Object, typ, prop, a.Graph)}
		}
	case rdfsRuleDomainSchema:
		if is(a.Predicate, rdfsDomainIRI) {
			return []RDFTriple{mk(a.Subject, typ, prop, a.Graph), mk(a.Object, typ, class, a.Graph)}
		}
	case rdfsRuleRangeSchema:
		if is(a.Predicate, rdfsRangeIRI) {
			return []RDFTriple{mk(a.Subject, typ, prop, a.Graph), mk(a.Object, typ, class, a.Graph)}
		}
	case rdfsRuleClassReflexive:
		if is(a.Predicate, rdfTypeIRI) && is(a.Object, rdfsClassIRI) {
			return []RDFTriple{mk(a.Subject, sc, a.Subject, a.Graph)}
		}
	case rdfsRulePropReflexive:
		if is(a.Predicate, rdfTypeIRI) && is(a.Object, rdfPropertyIRI) {
			return []RDFTriple{mk(a.Subject, sp, a.Subject, a.Graph)}
		}
	case rdfsRuleSubClass, rdfsRuleSubProperty:
		iri := rdfsSubClassOfIRI
		if rule == rdfsRuleSubProperty {
			iri = rdfsSubPropertyOfIRI
		}
		if is(a.Predicate, iri) && is(b.Predicate, iri) && termsEqual(a.Object, b.Subject) {
			return []RDFTriple{mk(a.Subject, NewIRI(iri), b.Object, mergeInferenceGraph(a.Graph, b.Graph))}
		}
	case rdfsRuleTypeSubClass:
		if is(a.Predicate, rdfTypeIRI) && is(b.Predicate, rdfsSubClassOfIRI) && termsEqual(a.Object, b.Subject) {
			return []RDFTriple{mk(a.Subject, typ, b.Object, preferInferenceGraph(a.Graph, b.Graph))}
		}
	case rdfsRuleSubPropertyUse:
		if is(b.Predicate, rdfsSubPropertyOfIRI) && termsEqual(a.Predicate, b.Subject) {
			return []RDFTriple{mk(a.Subject, b.Object, a.Object, preferInferenceGraph(a.Graph, b.Graph))}
		}
	case rdfsRuleDomain:
		if is(b.Predicate, rdfsDomainIRI) && termsEqual(a.Predicate, b.Subject) {
			return []RDFTriple{mk(a.Subject, typ, b.Object, preferInferenceGraph(a.Graph, b.Graph))}
		}
	case rdfsRuleRange:
		if is(b.Predicate, rdfsRangeIRI) && termsEqual(a.Predicate, b.Subject) && a.Object.Kind != RDFTermLiteral {
			return []RDFTriple{mk(a.Object, typ, b.Object, preferInferenceGraph(a.Graph, b.Graph))}
		}
	case owlRuleInverseOf:
		if !is(b.Predicate, owlInverseOfIRI) || a.Object.Kind == RDFTermLiteral || b.Subject.Kind != RDFTermIRI || b.Object.Kind != RDFTermIRI {
			return nil
		}
		var out []RDFTriple
		if termsEqual(a.Predicate, b.Subject) {
			out = append(out, mk(a.Object, b.Object, a.Subject, preferInferenceGraph(a.Graph, b.Graph)))
		}
		if termsEqual(a.Predicate, b.Object) {
			out = append(out, mk(a.Object, b.Subject, a.Subject, preferInferenceGraph(a.Graph, b.Graph)))
		}
		return out
	case owlRuleSymmetric:
		if is(b.Predicate, rdfTypeIRI) && is(b.Object, owlSymmetricPropertyIRI) && termsEqual(a.Predicate, b.Subject) && a.Object.Kind != RDFTermLiteral {
			return []RDFTriple{mk(a.Object, a.Predicate, a.Subject, preferInferenceGraph(a.Graph, b.Graph))}
		}
	case owlRuleTransitive:
		if is(c.Predicate, rdfTypeIRI) && is(c.Object, owlTransitivePropertyIRI) &&
			termsEqual(a.Predicate, c.Subject) && termsEqual(b.Predicate, c.Subject) && termsEqual(a.Object, b.Subject) {
			return []RDFTriple{mk(a.Subject, a.Predicate, b.Object, mergeInferenceGraph(a.Graph, b.Graph))}
		}
	case owlRuleEquivalentClass:
		if is(a.Predicate, owlEquivalentClassIRI) && isResourceTerm(a.Subject) && isResourceTerm(a.Object) {
			return []RDFTriple{mk(a.Subject, sc, a.Object, a.Graph), mk(a.Object, sc, a.Subject, a.Graph)}
		}
	case owlRuleEquivalentProperty:
		if is(a.Predicate, owlEquivalentPropertyIRI) && a.Subject.Kind == RDFTermIRI && a.Object.Kind == RDFTermIRI {
			return []RDFTriple{mk(a.Subject, sp, a.Object, a.Graph), mk(a.Object, sp, a.Subject, a.Graph)}
		}
	case owlRuleSameAsSymmetric:
		if individualsSameAs(a) {
			return []RDFTriple{mk(a.Object, same, a.Subject, a.Graph)}
		}
	case owlRuleSameAsTransitive:
		if individualsSameAs(a) && individualsSameAs(b) && termsEqual(a.Object, b.Subject) && !termsEqual(a.Subject, b.Object) {
			return []RDFTriple{mk(a.Subject, same, b.Object, mergeInferenceGraph(a.Graph, b.Graph))}
		}
	case owlRuleSameAsSubject:
		if !is(a.Predicate, owlSameAsIRI) && individualsSameAs(b) && termsEqual(a.Subject, b.Subject) {
			return []RDFTriple{mk(b.Object, a.Predicate, a.Object, preferInferenceGraph(a.Graph, b.Graph))}
		}
	case owlRuleSameAsObject:
		if !is(a.Predicate, owlSameAsIRI) && individualsSameAs(b) && termsEqual(a.Object, b.Subject) {
			return []RDFTriple{mk(a.Subject, a.Predicate, b.Object, preferInferenceGraph(a.Graph, b.Graph))}
		}
	}
	return nil
}

// individualsSameAs is a sameAs statement the rules act on: two distinct
// individuals, never a literal and never a term with itself.
func individualsSameAs(triple RDFTriple) bool {
	return triple.Predicate.Kind == RDFTermIRI && triple.Predicate.Value == owlSameAsIRI &&
		isResourceTerm(triple.Subject) && isResourceTerm(triple.Object) && !termsEqual(triple.Subject, triple.Object)
}
