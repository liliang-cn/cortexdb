package graph

// The rest of OWL 2 RL: class expressions, the schema rules, and the
// contradictions owl.go and owl_rl.go do not cover.
//
// OWL 2 RL is a list of rules (OWL 2 Profiles §4.3, tables 4–9). Most of them
// are a join of a few triple patterns with one conclusion, and they are
// written here as data — a body of patterns, a head — and run by one joiner
// that, like every rule in this engine, tries the new record in each body
// position and joins the rest through the indexes. The rules that read an RDF
// list (intersections, unions, enumerations, the All* axioms, keys) have the
// list read once, from explicit rdf:first/rdf:rest statements as property
// chains are, and an axiom registered for the run.
//
// Covered, beyond owl.go and owl_rl.go:
//
//	prp-irp prp-asyp prp-npa1 prp-npa2 prp-adp prp-key
//	cls-nothing2 cls-int1 cls-int2 cls-uni cls-com cls-svf1 cls-svf2 cls-avf
//	cls-hv1 cls-hv2 cls-maxc1 cls-maxc2 cls-maxqc1 cls-maxqc2 cls-maxqc3
//	cls-maxqc4 cls-oo
//	cax-adc  eq-diff2 eq-diff3
//	scm-hv scm-svf1 scm-svf2 scm-avf1 scm-avf2 scm-int scm-uni
//
// Left out on purpose: eq-ref, cls-thing, cls-nothing1, prp-ap, scm-cls,
// scm-op, scm-dp and the reflexive halves of scm-eqc1/scm-eqp1, which state
// that every term is the same as itself, every class a subclass of itself
// and of owl:Thing — true of everything, and a copy of the vocabulary for
// every name in the graph; scm-dom1, scm-dom2, scm-rng1, scm-rng2, scm-eqc2
// and scm-eqp2, whose premises are plain RDFS and which would restate the
// schema of every graph — the instance types they lead to the RDFS rules
// already derive; eq-rep-p, for the reason fireSameAs gives; and the
// datatype rules dt-*, which concern the value spaces of literals rather than
// what a graph says.

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	owlOnPropertyIRI              = owlNamespace + "onProperty"
	owlSomeValuesFromIRI          = owlNamespace + "someValuesFrom"
	owlAllValuesFromIRI           = owlNamespace + "allValuesFrom"
	owlHasValueIRI                = owlNamespace + "hasValue"
	owlMaxCardinalityIRI          = owlNamespace + "maxCardinality"
	owlMaxQualifiedCardinalityIRI = owlNamespace + "maxQualifiedCardinality"
	owlOnClassIRI                 = owlNamespace + "onClass"
	owlIntersectionOfIRI          = owlNamespace + "intersectionOf"
	owlUnionOfIRI                 = owlNamespace + "unionOf"
	owlOneOfIRI                   = owlNamespace + "oneOf"
	owlComplementOfIRI            = owlNamespace + "complementOf"
	owlHasKeyIRI                  = owlNamespace + "hasKey"
	owlMembersIRI                 = owlNamespace + "members"
	owlDistinctMembersIRI         = owlNamespace + "distinctMembers"
	owlSourceIndividualIRI        = owlNamespace + "sourceIndividual"
	owlAssertionPropertyIRI       = owlNamespace + "assertionProperty"
	owlTargetIndividualIRI        = owlNamespace + "targetIndividual"
	owlTargetValueIRI             = owlNamespace + "targetValue"
	owlThingIRI                   = owlNamespace + "Thing"
	owlNothingIRI                 = owlNamespace + "Nothing"
	owlIrreflexivePropertyIRI     = owlNamespace + "IrreflexiveProperty"
	owlAsymmetricPropertyIRI      = owlNamespace + "AsymmetricProperty"
	owlAllDisjointClassesIRI      = owlNamespace + "AllDisjointClasses"
	owlAllDisjointPropertiesIRI   = owlNamespace + "AllDisjointProperties"
	owlAllDifferentIRI            = owlNamespace + "AllDifferent"

	// InconsistencyIrreflexive is OWL 2 RL prp-irp: x p x for an
	// irreflexive p.
	InconsistencyIrreflexive = "prp-irp"
	// InconsistencyAsymmetric is prp-asyp: x p y and y p x for an
	// asymmetric p.
	InconsistencyAsymmetric = "prp-asyp"
	// InconsistencyNegativeAssertion is prp-npa1 / prp-npa2: a statement a
	// negative property assertion denies.
	InconsistencyNegativeAssertion = "prp-npa"
	// InconsistencyNothing is cls-nothing2: an instance of owl:Nothing.
	InconsistencyNothing = "cls-nothing2"
	// InconsistencyComplement is cls-com: an instance of a class and of its
	// complement.
	InconsistencyComplement = "cls-com"
	// InconsistencyMaxCardinality is cls-maxc1 / cls-maxqc1 / cls-maxqc2: a
	// value where a cardinality restriction allows none.
	InconsistencyMaxCardinality = "cls-maxc"
	// InconsistencyAllDisjointClasses is cax-adc.
	InconsistencyAllDisjointClasses = "cax-adc"
	// InconsistencyAllDisjointProperties is prp-adp.
	InconsistencyAllDisjointProperties = "prp-adp"
	// InconsistencyAllDifferent is eq-diff2 / eq-diff3: two members of an
	// owl:AllDifferent that sameAs reasoning makes one individual.
	InconsistencyAllDifferent = "eq-diff2"

	owlRuleSomeValuesFrom = "owl_some_values_from"
	owlRuleAllValuesFrom  = "owl_all_values_from"
	owlRuleHasValue       = "owl_has_value"
	owlRuleMaxCardinality = "owl_max_cardinality"
	owlRuleIntersection   = "owl_intersection"
	owlRuleUnion          = "owl_union"
	owlRuleOneOf          = "owl_one_of"
	owlRuleKey            = "owl_has_key"
	owlRuleSchemaRestrict = "owl_schema_restriction"
	owlRuleSchemaList     = "owl_schema_list"
)

// owlPattern is one triple pattern of a rule. Each position is a variable
// ("?x"), an IRI, or "#n": a literal whose integer value is n, for the
// cardinality restrictions.
type owlPattern struct{ s, p, o string }

// owlRule is a rule of the joiner. A rule has a head (triples to derive), a
// sameAs conclusion between two variables, or a clash to report.
type owlRule struct {
	name string
	body []owlPattern
	head []owlPattern
	// same, when set, concludes ?same[0] sameAs ?same[1].
	same [2]string
	// differ lists variable pairs whose values must differ.
	differ [][2]string
	clash  string
	// explain words a clash; b holds the body's bindings.
	explain func(b map[string]RDFTerm) string
}

var owlRules = []owlRule{
	// Contradictions.
	{name: "prp-irp", clash: InconsistencyIrreflexive,
		body: []owlPattern{{"?p", rdfTypeIRI, owlIrreflexivePropertyIRI}, {"?x", "?p", "?x"}},
		explain: func(b map[string]RDFTerm) string {
			return fmt.Sprintf("%s relates %s to itself, but it is declared irreflexive", b["p"], b["x"])
		}},
	{name: "prp-asyp", clash: InconsistencyAsymmetric,
		body:   []owlPattern{{"?p", rdfTypeIRI, owlAsymmetricPropertyIRI}, {"?x", "?p", "?y"}, {"?y", "?p", "?x"}},
		differ: [][2]string{{"x", "y"}},
		explain: func(b map[string]RDFTerm) string {
			return fmt.Sprintf("%s relates %s and %s both ways, but it is declared asymmetric", b["p"], b["x"], b["y"])
		}},
	{name: "prp-npa1", clash: InconsistencyNegativeAssertion,
		body: []owlPattern{{"?n", owlSourceIndividualIRI, "?i1"}, {"?n", owlAssertionPropertyIRI, "?p"},
			{"?n", owlTargetIndividualIRI, "?i2"}, {"?i1", "?p", "?i2"}},
		explain: func(b map[string]RDFTerm) string {
			return fmt.Sprintf("%s %s %s is stated, and a negative property assertion denies it", b["i1"], b["p"], b["i2"])
		}},
	{name: "prp-npa2", clash: InconsistencyNegativeAssertion,
		body: []owlPattern{{"?n", owlSourceIndividualIRI, "?i"}, {"?n", owlAssertionPropertyIRI, "?p"},
			{"?n", owlTargetValueIRI, "?lt"}, {"?i", "?p", "?lt"}},
		explain: func(b map[string]RDFTerm) string {
			return fmt.Sprintf("%s %s %s is stated, and a negative property assertion denies it", b["i"], b["p"], b["lt"])
		}},
	{name: "cls-nothing2", clash: InconsistencyNothing,
		body: []owlPattern{{"?x", rdfTypeIRI, owlNothingIRI}},
		explain: func(b map[string]RDFTerm) string {
			return fmt.Sprintf("%s is an instance of owl:Nothing, which has no instances", b["x"])
		}},
	{name: "cls-com", clash: InconsistencyComplement,
		body: []owlPattern{{"?c1", owlComplementOfIRI, "?c2"}, {"?x", rdfTypeIRI, "?c1"}, {"?x", rdfTypeIRI, "?c2"}},
		explain: func(b map[string]RDFTerm) string {
			return fmt.Sprintf("%s is an instance of both %s and its complement %s", b["x"], b["c2"], b["c1"])
		}},
	{name: "cls-maxc1", clash: InconsistencyMaxCardinality,
		body: []owlPattern{{"?x", owlMaxCardinalityIRI, "#0"}, {"?x", owlOnPropertyIRI, "?p"}, {"?u", rdfTypeIRI, "?x"}, {"?u", "?p", "?y"}},
		explain: func(b map[string]RDFTerm) string {
			return fmt.Sprintf("%s has a value for %s, which a max-cardinality-0 restriction it is an instance of forbids", b["u"], b["p"])
		}},
	{name: "cls-maxqc1", clash: InconsistencyMaxCardinality,
		body: []owlPattern{{"?x", owlMaxQualifiedCardinalityIRI, "#0"}, {"?x", owlOnPropertyIRI, "?p"}, {"?x", owlOnClassIRI, "?c"},
			{"?u", rdfTypeIRI, "?x"}, {"?u", "?p", "?y"}, {"?y", rdfTypeIRI, "?c"}},
		explain: func(b map[string]RDFTerm) string {
			return fmt.Sprintf("%s has a %s value of class %s, which a max-qualified-cardinality-0 restriction forbids", b["u"], b["p"], b["c"])
		}},
	{name: "cls-maxqc2", clash: InconsistencyMaxCardinality,
		body: []owlPattern{{"?x", owlMaxQualifiedCardinalityIRI, "#0"}, {"?x", owlOnPropertyIRI, "?p"}, {"?x", owlOnClassIRI, owlThingIRI},
			{"?u", rdfTypeIRI, "?x"}, {"?u", "?p", "?y"}},
		explain: func(b map[string]RDFTerm) string {
			return fmt.Sprintf("%s has a value for %s, which a max-qualified-cardinality-0 restriction forbids", b["u"], b["p"])
		}},

	// Class expressions.
	{name: owlRuleSomeValuesFrom, // cls-svf1
		body: []owlPattern{{"?x", owlSomeValuesFromIRI, "?y"}, {"?x", owlOnPropertyIRI, "?p"}, {"?u", "?p", "?v"}, {"?v", rdfTypeIRI, "?y"}},
		head: []owlPattern{{"?u", rdfTypeIRI, "?x"}}},
	{name: owlRuleSomeValuesFrom, // cls-svf2
		body: []owlPattern{{"?x", owlSomeValuesFromIRI, owlThingIRI}, {"?x", owlOnPropertyIRI, "?p"}, {"?u", "?p", "?v"}},
		head: []owlPattern{{"?u", rdfTypeIRI, "?x"}}},
	{name: owlRuleAllValuesFrom, // cls-avf
		body: []owlPattern{{"?x", owlAllValuesFromIRI, "?y"}, {"?x", owlOnPropertyIRI, "?p"}, {"?u", rdfTypeIRI, "?x"}, {"?u", "?p", "?v"}},
		head: []owlPattern{{"?v", rdfTypeIRI, "?y"}}},
	{name: owlRuleHasValue, // cls-hv1
		body: []owlPattern{{"?x", owlHasValueIRI, "?y"}, {"?x", owlOnPropertyIRI, "?p"}, {"?u", rdfTypeIRI, "?x"}},
		head: []owlPattern{{"?u", "?p", "?y"}}},
	{name: owlRuleHasValue, // cls-hv2
		body: []owlPattern{{"?x", owlHasValueIRI, "?y"}, {"?x", owlOnPropertyIRI, "?p"}, {"?u", "?p", "?y"}},
		head: []owlPattern{{"?u", rdfTypeIRI, "?x"}}},
	{name: owlRuleMaxCardinality, // cls-maxc2
		body: []owlPattern{{"?x", owlMaxCardinalityIRI, "#1"}, {"?x", owlOnPropertyIRI, "?p"}, {"?u", rdfTypeIRI, "?x"}, {"?u", "?p", "?y1"}, {"?u", "?p", "?y2"}},
		same: [2]string{"y1", "y2"}, differ: [][2]string{{"y1", "y2"}}},
	{name: owlRuleMaxCardinality, // cls-maxqc3
		body: []owlPattern{{"?x", owlMaxQualifiedCardinalityIRI, "#1"}, {"?x", owlOnPropertyIRI, "?p"}, {"?x", owlOnClassIRI, "?c"},
			{"?u", rdfTypeIRI, "?x"}, {"?u", "?p", "?y1"}, {"?y1", rdfTypeIRI, "?c"}, {"?u", "?p", "?y2"}, {"?y2", rdfTypeIRI, "?c"}},
		same: [2]string{"y1", "y2"}, differ: [][2]string{{"y1", "y2"}}},
	{name: owlRuleMaxCardinality, // cls-maxqc4
		body: []owlPattern{{"?x", owlMaxQualifiedCardinalityIRI, "#1"}, {"?x", owlOnPropertyIRI, "?p"}, {"?x", owlOnClassIRI, owlThingIRI},
			{"?u", rdfTypeIRI, "?x"}, {"?u", "?p", "?y1"}, {"?u", "?p", "?y2"}},
		same: [2]string{"y1", "y2"}, differ: [][2]string{{"y1", "y2"}}},

	// Schema.
	{name: owlRuleSchemaRestrict, // scm-hv
		body: []owlPattern{{"?c1", owlHasValueIRI, "?i"}, {"?c1", owlOnPropertyIRI, "?p1"}, {"?c2", owlHasValueIRI, "?i"},
			{"?c2", owlOnPropertyIRI, "?p2"}, {"?p1", rdfsSubPropertyOfIRI, "?p2"}},
		differ: [][2]string{{"c1", "c2"}},
		head:   []owlPattern{{"?c1", rdfsSubClassOfIRI, "?c2"}}},
	{name: owlRuleSchemaRestrict, // scm-svf1
		body: []owlPattern{{"?c1", owlSomeValuesFromIRI, "?y1"}, {"?c1", owlOnPropertyIRI, "?p"}, {"?c2", owlSomeValuesFromIRI, "?y2"},
			{"?c2", owlOnPropertyIRI, "?p"}, {"?y1", rdfsSubClassOfIRI, "?y2"}},
		differ: [][2]string{{"c1", "c2"}},
		head:   []owlPattern{{"?c1", rdfsSubClassOfIRI, "?c2"}}},
	{name: owlRuleSchemaRestrict, // scm-svf2
		body: []owlPattern{{"?c1", owlSomeValuesFromIRI, "?y"}, {"?c1", owlOnPropertyIRI, "?p1"}, {"?c2", owlSomeValuesFromIRI, "?y"},
			{"?c2", owlOnPropertyIRI, "?p2"}, {"?p1", rdfsSubPropertyOfIRI, "?p2"}},
		differ: [][2]string{{"c1", "c2"}},
		head:   []owlPattern{{"?c1", rdfsSubClassOfIRI, "?c2"}}},
	{name: owlRuleSchemaRestrict, // scm-avf1
		body: []owlPattern{{"?c1", owlAllValuesFromIRI, "?y1"}, {"?c1", owlOnPropertyIRI, "?p"}, {"?c2", owlAllValuesFromIRI, "?y2"},
			{"?c2", owlOnPropertyIRI, "?p"}, {"?y1", rdfsSubClassOfIRI, "?y2"}},
		differ: [][2]string{{"c1", "c2"}},
		head:   []owlPattern{{"?c1", rdfsSubClassOfIRI, "?c2"}}},
	{name: owlRuleSchemaRestrict, // scm-avf2
		body: []owlPattern{{"?c1", owlAllValuesFromIRI, "?y"}, {"?c1", owlOnPropertyIRI, "?p1"}, {"?c2", owlAllValuesFromIRI, "?y"},
			{"?c2", owlOnPropertyIRI, "?p2"}, {"?p1", rdfsSubPropertyOfIRI, "?p2"}},
		differ: [][2]string{{"c1", "c2"}},
		head:   []owlPattern{{"?c2", rdfsSubClassOfIRI, "?c1"}}},
}

// owlRuleTrigger is one body position a rule can be entered from, indexed by
// the predicate the record must have ("" for a variable predicate).
type owlRuleTrigger struct {
	rule     *owlRule
	position int
	gate     owlRuleGate
}

// owlRuleGate is a rule's schema pattern — a constant predicate, and for
// rdf:type a constant class — as index keys. A rule whose gate matches no
// record cannot fire, so it is skipped without a join: that is what a graph
// that states no OWL pays for these rules, a map lookup each.
type owlRuleGate struct {
	predicateKey string
	// typedKey, for an rdf:type gate, is the byPredicateObject key.
	typedKey string
}

var (
	owlRuleTriggers = map[string][]owlRuleTrigger{}
	owlRuleGates    = map[*owlRule]owlRuleGate{}
)

func init() {
	for i := range owlRules {
		r := &owlRules[i]
		for _, pat := range r.body {
			if strings.HasPrefix(pat.p, "?") {
				continue
			}
			if pat.p == rdfTypeIRI {
				if strings.HasPrefix(pat.o, "?") {
					continue
				}
				owlRuleGates[r] = owlRuleGate{typedKey: keyRDFType + "\x01" + engineTermKey(NewIRI(pat.o))}
				break
			}
			owlRuleGates[r] = owlRuleGate{predicateKey: engineTermKey(NewIRI(pat.p))}
			break
		}
		if _, ok := owlRuleGates[r]; !ok {
			panic("owl rule " + r.name + " has no schema pattern to gate it")
		}
		for pos, pat := range r.body {
			key := ""
			if !strings.HasPrefix(pat.p, "?") {
				key = pat.p
			}
			owlRuleTriggers[key] = append(owlRuleTriggers[key], owlRuleTrigger{rule: r, position: pos, gate: owlRuleGates[r]})
		}
	}
}

// open reports whether some record matches the gate.
func (e *inferenceEngine) open(g owlRuleGate) bool {
	if g.typedKey != "" {
		return len(e.byPredicateObject[g.typedKey]) > 0
	}
	return len(e.byPredicate[g.predicateKey]) > 0
}

// fireOWLRules runs the pattern rules and the list axioms with the record as
// a premise.
func (e *inferenceEngine) fireOWLRules(record *rdfsInferenceRecord) {
	for _, list := range [2][]owlRuleTrigger{owlRuleTriggers[record.Triple.Predicate.Value], owlRuleTriggers[""]} {
		for _, trig := range list {
			if e.open(trig.gate) {
				e.enterRule(trig.rule, trig.position, record)
			}
		}
	}
	if e.owlRL.listAxiomKeys != nil || isListAxiomTrigger(record.Triple) {
		e.fireOWLListAxioms(record)
	}
}

func isListAxiomTrigger(t RDFTriple) bool {
	switch t.Predicate.Value {
	case owlIntersectionOfIRI, owlUnionOfIRI, owlOneOfIRI, owlHasKeyIRI, owlMembersIRI, owlDistinctMembersIRI:
		return true
	}
	return false
}

func (e *inferenceEngine) enterRule(r *owlRule, position int, record *rdfsInferenceRecord) {
	if !owlMatchConstants(r.body[position], record.Triple) {
		return
	}
	b := make(map[string]RDFTerm, 8)
	if !owlMatch(r.body[position], record.Triple, b) {
		return
	}
	matched := make([]*rdfsInferenceRecord, len(r.body))
	matched[position] = record
	e.joinRule(r, matched, b)
}

// joinRule binds the remaining body patterns, the one with the fewest
// candidates first. Counting through the indexes is a map lookup per
// pattern, and it is what keeps a rule cheap on a graph that never states
// its vocabulary: the schema pattern has no candidates, and the join stops
// before it looks at any data.
func (e *inferenceEngine) joinRule(r *owlRule, matched []*rdfsInferenceRecord, b map[string]RDFTerm) {
	next := -1
	var candidates []*rdfsInferenceRecord
	for i, pat := range r.body {
		if matched[i] != nil {
			continue
		}
		c, ok := e.owlCandidates(pat, b)
		if !ok {
			continue // nothing to look it up by yet
		}
		if next < 0 || len(c) < len(candidates) {
			next, candidates = i, c
		}
		if len(c) == 0 {
			return
		}
	}
	if next < 0 {
		for i := range r.body {
			if matched[i] == nil {
				return // a pattern no binding reaches; rules are written so this cannot happen
			}
		}
		e.concludeRule(r, matched, b)
		return
	}
	pat := r.body[next]
	for _, candidate := range candidates {
		nb := make(map[string]RDFTerm, len(b)+3)
		for k, v := range b {
			nb[k] = v
		}
		if !owlMatch(pat, candidate.Triple, nb) {
			continue
		}
		matched[next] = candidate
		e.joinRule(r, matched, nb)
	}
	matched[next] = nil
}

// owlCandidates lists the records that can match pat under b, through the
// narrowest index available; ok is false when no position is known.
func (e *inferenceEngine) owlCandidates(pat owlPattern, b map[string]RDFTerm) ([]*rdfsInferenceRecord, bool) {
	term := func(part string) (RDFTerm, bool) {
		if strings.HasPrefix(part, "?") {
			v, ok := b[part[1:]]
			return v, ok
		}
		if strings.HasPrefix(part, "#") {
			return RDFTerm{}, false
		}
		return NewIRI(part), true
	}
	s, sok := term(pat.s)
	p, pok := term(pat.p)
	o, ook := term(pat.o)
	switch {
	case pok && sok && ook:
		var out []*rdfsInferenceRecord
		for _, r := range e.withSubject(engineTermKey(p), s) {
			if termsEqual(r.Triple.Object, o) {
				out = append(out, r)
			}
		}
		return out, true
	case pok && sok:
		return e.withSubject(engineTermKey(p), s), true
	case pok && ook:
		return e.withObject(engineTermKey(p), o), true
	case sok:
		return e.bySubject[engineTermKey(s)], true
	case ook:
		return e.byObject[engineTermKey(o)], true
	case pok:
		return e.byPredicate[engineTermKey(p)], true
	}
	return nil, false
}

// owlMatch matches a triple against a pattern, extending b.
func owlMatch(pat owlPattern, t RDFTriple, b map[string]RDFTerm) bool {
	return owlMatchPart(pat.s, t.Subject, b) && owlMatchPart(pat.p, t.Predicate, b) && owlMatchPart(pat.o, t.Object, b)
}

// owlMatchConstants checks a pattern's constant positions only, without
// binding anything.
func owlMatchConstants(pat owlPattern, t RDFTriple) bool {
	return owlMatchPart(pat.s, t.Subject, nil) && owlMatchPart(pat.p, t.Predicate, nil) && owlMatchPart(pat.o, t.Object, nil)
}

func owlMatchPart(part string, term RDFTerm, b map[string]RDFTerm) bool {
	switch part[0] {
	case '?':
		if b == nil {
			return true
		}
		name := part[1:]
		if bound, ok := b[name]; ok {
			return termsEqual(bound, term)
		}
		b[name] = term
		return true
	case '#':
		want, _ := strconv.Atoi(part[1:])
		if term.Kind != RDFTermLiteral {
			return false
		}
		n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(term.Value), "+"))
		return err == nil && n == want
	}
	return term.Kind == RDFTermIRI && term.Value == part
}

func (e *inferenceEngine) concludeRule(r *owlRule, matched []*rdfsInferenceRecord, b map[string]RDFTerm) {
	for _, d := range r.differ {
		if termsEqual(b[d[0]], b[d[1]]) {
			return
		}
	}
	supports := append([]*rdfsInferenceRecord(nil), matched...)
	graph := agreeingInferenceGraph(supports)
	switch {
	case r.clash != "":
		explain := r.explain
		e.reportClash(r.clash, func([]*rdfsInferenceRecord) string { return explain(b) }, supports[0], supports[1:]...)
	case r.same[0] != "":
		y1, y2 := b[r.same[0]], b[r.same[1]]
		if isResourceTerm(y1) && isResourceTerm(y2) {
			e.deriveKeySameAs(y1, y2, graph, r.name, supports...)
			e.deriveKeySameAs(y2, y1, graph, r.name, supports...)
		}
	default:
		for _, h := range r.head {
			s, p, o := b[strings.TrimPrefix(h.s, "?")], owlHeadTerm(h.p, b), owlHeadTerm(h.o, b)
			if !strings.HasPrefix(h.s, "?") {
				s = NewIRI(h.s)
			}
			if !isResourceTerm(s) || p.Kind != RDFTermIRI {
				continue
			}
			e.derive(s, p, o, graph, r.name, supports...)
		}
	}
}

func owlHeadTerm(part string, b map[string]RDFTerm) RDFTerm {
	if strings.HasPrefix(part, "?") {
		return b[part[1:]]
	}
	return NewIRI(part)
}

// --- Axioms over lists ----------------------------------------------------

type owlListAxiom struct {
	kind    string // the predicate or class that introduced it
	head    RDFTerm
	members []RDFTerm
	// supports are the axiom statement and the list cells.
	supports []*rdfsInferenceRecord
}

// readOWLList walks an RDF list of resources, from explicit cells only.
func (e *inferenceEngine) readOWLList(head RDFTerm) ([]RDFTerm, []*rdfsInferenceRecord, bool) {
	var members []RDFTerm
	var cells []*rdfsInferenceRecord
	visited := map[string]bool{}
	for current := head; ; {
		if current.Kind == RDFTermIRI && current.Value == rdfNilIRI {
			return members, cells, true
		}
		key := engineTermKey(current)
		if visited[key] {
			return nil, nil, false
		}
		visited[key] = true
		first, ok := soleExplicitObject(e.withSubject(keyRDFFirst, current))
		if !ok {
			return nil, nil, false
		}
		rest, ok := soleExplicitObject(e.withSubject(keyRDFRest, current))
		if !ok {
			return nil, nil, false
		}
		members = append(members, first.Triple.Object)
		cells = append(cells, first, rest)
		current = rest.Triple.Object
	}
}

func (e *inferenceEngine) fireOWLListAxioms(record *rdfsInferenceRecord) {
	t := record.Triple
	switch t.Predicate.Value {
	case owlIntersectionOfIRI, owlUnionOfIRI, owlOneOfIRI, owlHasKeyIRI, owlMembersIRI, owlDistinctMembersIRI:
		if isResourceTerm(t.Subject) {
			e.registerListAxiom(record)
		}
	case rdfFirstIRI, rdfRestIRI:
		// A list completed after its axiom was seen: register the axioms
		// that point at the list's head again.
		for _, head := range e.listHeadsOf(t.Subject) {
			for _, pred := range []string{owlIntersectionOfIRI, owlUnionOfIRI, owlOneOfIRI, owlHasKeyIRI, owlMembersIRI, owlDistinctMembersIRI} {
				for _, axiom := range e.withObject(engineTermKey(NewIRI(pred)), head) {
					e.registerListAxiom(axiom)
				}
			}
		}
	case rdfTypeIRI:
		e.typedForListAxioms(record)
	}
	if t.Predicate.Value != rdfTypeIRI {
		e.usedForListAxioms(record)
	}
}

// listHeadsOf walks rdf:rest backwards from a cell to the heads it is part of.
func (e *inferenceEngine) listHeadsOf(cell RDFTerm) []RDFTerm {
	var heads []RDFTerm
	seen := map[string]bool{}
	queue := []RDFTerm{cell}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if seen[engineTermKey(c)] {
			continue
		}
		seen[engineTermKey(c)] = true
		prev := e.withObject(keyRDFRest, c)
		if len(prev) == 0 {
			heads = append(heads, c)
		}
		for _, p := range prev {
			queue = append(queue, p.Triple.Subject)
		}
	}
	return heads
}

func (e *inferenceEngine) registerListAxiom(record *rdfsInferenceRecord) {
	t := record.Triple
	members, cells, ok := e.readOWLList(t.Object)
	if !ok {
		return
	}
	kind := t.Predicate.Value
	if kind == owlMembersIRI || kind == owlDistinctMembersIRI {
		// owl:members belongs to AllDisjointClasses, AllDisjointProperties
		// or AllDifferent; the subject's type says which.
		kind = ""
		for _, typed := range e.withSubject(keyRDFType, t.Subject) {
			switch typed.Triple.Object.Value {
			case owlAllDisjointClassesIRI, owlAllDisjointPropertiesIRI, owlAllDifferentIRI:
				kind = typed.Triple.Object.Value
				cells = append(cells, typed)
			}
		}
		if kind == "" {
			return
		}
	}
	if e.owlRL.listAxiomKeys == nil {
		e.owlRL.listAxiomKeys = map[string]bool{}
		e.owlRL.listByMember = map[string][]*owlListAxiom{}
		e.owlRL.listByHead = map[string][]*owlListAxiom{}
	}
	id := kind + "\x01" + record.key
	if e.owlRL.listAxiomKeys[id] {
		return
	}
	e.owlRL.listAxiomKeys[id] = true
	axiom := &owlListAxiom{kind: kind, head: t.Subject, members: members, supports: append([]*rdfsInferenceRecord{record}, cells...)}
	switch kind {
	case owlAllDifferentIRI:
		e.owlRL.allDifferent = append(e.owlRL.allDifferent, axiom)
		return
	case owlHasKeyIRI:
		e.owlRL.listByHead[engineTermKey(axiom.head)] = append(e.owlRL.listByHead[engineTermKey(axiom.head)], axiom)
		for _, m := range members {
			e.owlRL.listByMember[engineTermKey(m)] = append(e.owlRL.listByMember[engineTermKey(m)], axiom)
		}
		for _, instance := range e.withObject(keyRDFType, axiom.head) {
			e.applyKey(axiom, instance.Triple.Subject)
		}
		return
	}
	e.owlRL.listByHead[engineTermKey(axiom.head)] = append(e.owlRL.listByHead[engineTermKey(axiom.head)], axiom)
	for _, m := range members {
		e.owlRL.listByMember[engineTermKey(m)] = append(e.owlRL.listByMember[engineTermKey(m)], axiom)
	}
	graph := agreeingInferenceGraph(axiom.supports)
	switch kind {
	case owlIntersectionOfIRI:
		for _, m := range members {
			if isResourceTerm(m) {
				e.derive(axiom.head, termSubClassOf, m, graph, owlRuleSchemaList, axiom.supports...) // scm-int
			}
		}
		if len(members) > 0 {
			for _, instance := range e.withObject(keyRDFType, members[0]) {
				e.checkIntersection(axiom, instance.Triple.Subject)
			}
		}
		for _, instance := range e.withObject(keyRDFType, axiom.head) {
			e.splitIntersection(axiom, instance)
		}
	case owlUnionOfIRI:
		for _, m := range members {
			if isResourceTerm(m) {
				e.derive(m, termSubClassOf, axiom.head, graph, owlRuleSchemaList, axiom.supports...) // scm-uni
			}
		}
	case owlOneOfIRI:
		for _, m := range members {
			if isResourceTerm(m) {
				e.derive(m, termRDFType, axiom.head, graph, owlRuleOneOf, axiom.supports...) // cls-oo
			}
		}
	case owlAllDisjointClassesIRI:
		for _, m := range members {
			for _, instance := range e.withObject(keyRDFType, m) {
				e.checkAllDisjointClasses(axiom, instance)
			}
		}
	case owlAllDisjointPropertiesIRI:
		for _, m := range members {
			for _, use := range e.usingPredicate(m) {
				e.checkAllDisjointProperties(axiom, use)
			}
		}
	}
}

// typedForListAxioms handles x rdf:type C against the list axioms about C.
func (e *inferenceEngine) typedForListAxioms(record *rdfsInferenceRecord) {
	t := record.Triple
	class := engineTermKey(t.Object)
	for _, axiom := range e.owlRL.listByMember[class] {
		switch axiom.kind {
		case owlIntersectionOfIRI:
			e.checkIntersection(axiom, t.Subject) // cls-int1
		case owlUnionOfIRI:
			e.derive(t.Subject, termRDFType, axiom.head, agreeingInferenceGraph(append([]*rdfsInferenceRecord{record}, axiom.supports...)),
				owlRuleUnion, append([]*rdfsInferenceRecord{record}, axiom.supports...)...) // cls-uni
		case owlAllDisjointClassesIRI:
			e.checkAllDisjointClasses(axiom, record) // cax-adc
		}
	}
	for _, axiom := range e.owlRL.listByHead[class] {
		switch axiom.kind {
		case owlIntersectionOfIRI:
			e.splitIntersection(axiom, record) // cls-int2
		case owlHasKeyIRI:
			e.applyKey(axiom, t.Subject) // prp-key
		}
	}
}

// usedForListAxioms handles a statement x p y against the axioms that list p.
func (e *inferenceEngine) usedForListAxioms(record *rdfsInferenceRecord) {
	t := record.Triple
	for _, axiom := range e.owlRL.listByMember[engineTermKey(t.Predicate)] {
		switch axiom.kind {
		case owlAllDisjointPropertiesIRI:
			e.checkAllDisjointProperties(axiom, record)
		case owlHasKeyIRI:
			e.applyKey(axiom, t.Subject)
		}
	}
}

// checkIntersection is cls-int1: x typed with every member is typed with
// the intersection.
func (e *inferenceEngine) checkIntersection(axiom *owlListAxiom, x RDFTerm) {
	if !isResourceTerm(x) {
		return
	}
	supports := append([]*rdfsInferenceRecord(nil), axiom.supports...)
	for _, m := range axiom.members {
		var found *rdfsInferenceRecord
		for _, typed := range e.withSubject(keyRDFType, x) {
			if termsEqual(typed.Triple.Object, m) {
				found = typed
				break
			}
		}
		if found == nil {
			return
		}
		supports = append(supports, found)
	}
	e.derive(x, termRDFType, axiom.head, agreeingInferenceGraph(supports), owlRuleIntersection, supports...)
}

// splitIntersection is cls-int2: an instance of the intersection is an
// instance of each member.
func (e *inferenceEngine) splitIntersection(axiom *owlListAxiom, typed *rdfsInferenceRecord) {
	supports := append([]*rdfsInferenceRecord{typed}, axiom.supports...)
	for _, m := range axiom.members {
		if isResourceTerm(m) {
			e.derive(typed.Triple.Subject, termRDFType, m, agreeingInferenceGraph(supports), owlRuleIntersection, supports...)
		}
	}
}

func (e *inferenceEngine) checkAllDisjointClasses(axiom *owlListAxiom, typed *rdfsInferenceRecord) {
	x := typed.Triple.Subject
	for _, other := range e.withSubject(keyRDFType, x) {
		if termsEqual(other.Triple.Object, typed.Triple.Object) {
			continue
		}
		for _, m := range axiom.members {
			if termsEqual(m, other.Triple.Object) {
				premises := append([]*rdfsInferenceRecord{typed, other}, axiom.supports[1:]...)
				e.reportClash(InconsistencyAllDisjointClasses, func([]*rdfsInferenceRecord) string {
					return fmt.Sprintf("%s is an instance of %s and %s, which an owl:AllDisjointClasses declares disjoint",
						x, typed.Triple.Object, other.Triple.Object)
				}, axiom.supports[0], premises...)
			}
		}
	}
}

func (e *inferenceEngine) checkAllDisjointProperties(axiom *owlListAxiom, use *rdfsInferenceRecord) {
	u := use.Triple
	for _, other := range e.bySubject[engineTermKey(u.Subject)] {
		o := other.Triple
		if termsEqual(o.Predicate, u.Predicate) || !termsEqual(o.Object, u.Object) {
			continue
		}
		for _, m := range axiom.members {
			if termsEqual(m, o.Predicate) {
				premises := append([]*rdfsInferenceRecord{use, other}, axiom.supports[1:]...)
				e.reportClash(InconsistencyAllDisjointProperties, func([]*rdfsInferenceRecord) string {
					return fmt.Sprintf("%s is related to %s by %s and %s, which an owl:AllDisjointProperties declares disjoint",
						u.Subject, u.Object, u.Predicate, o.Predicate)
				}, axiom.supports[0], premises...)
			}
		}
	}
}

// applyKey is prp-key: two instances of the class with the same values for
// every key property are the same individual. x's key tuples are matched
// against every other instance's.
func (e *inferenceEngine) applyKey(axiom *owlListAxiom, x RDFTerm) {
	if !isResourceTerm(x) {
		return
	}
	var xType *rdfsInferenceRecord
	for _, typed := range e.withSubject(keyRDFType, x) {
		if termsEqual(typed.Triple.Object, axiom.head) {
			xType = typed
			break
		}
	}
	if xType == nil || len(axiom.members) == 0 {
		return
	}
	first := engineTermKey(axiom.members[0])
	for _, xFirst := range e.withSubject(first, x) {
		// Candidates share the first key value.
		for _, yFirst := range e.withObject(first, xFirst.Triple.Object) {
			y := yFirst.Triple.Subject
			if termsEqual(x, y) || !isResourceTerm(y) {
				continue
			}
			var yType *rdfsInferenceRecord
			for _, typed := range e.withSubject(keyRDFType, y) {
				if termsEqual(typed.Triple.Object, axiom.head) {
					yType = typed
					break
				}
			}
			if yType == nil {
				continue
			}
			supports := append([]*rdfsInferenceRecord{xType, yType, xFirst, yFirst}, axiom.supports...)
			if !e.sharedKeyValues(axiom.members[1:], x, y, &supports) {
				continue
			}
			graph := agreeingInferenceGraph(supports)
			e.deriveKeySameAs(x, y, graph, owlRuleKey, supports...)
			e.deriveKeySameAs(y, x, graph, owlRuleKey, supports...)
		}
	}
}

// sharedKeyValues reports whether x and y share a value for every property,
// adding the statements that show it to supports.
func (e *inferenceEngine) sharedKeyValues(properties []RDFTerm, x, y RDFTerm, supports *[]*rdfsInferenceRecord) bool {
	for _, p := range properties {
		key := engineTermKey(p)
		found := false
		for _, xv := range e.withSubject(key, x) {
			for _, yv := range e.withSubject(key, y) {
				if termsEqual(xv.Triple.Object, yv.Triple.Object) {
					*supports = append(*supports, xv, yv)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// isOWLClassExpressionVocabulary names the vocabulary of this file, which
// incremental maintenance answers by recomputing (see isRecomputeVocabulary).
func isOWLClassExpressionVocabulary(triple RDFTriple) bool {
	switch triple.Predicate.Value {
	case owlOnPropertyIRI, owlSomeValuesFromIRI, owlAllValuesFromIRI, owlHasValueIRI, owlMaxCardinalityIRI,
		owlMaxQualifiedCardinalityIRI, owlOnClassIRI, owlIntersectionOfIRI, owlUnionOfIRI, owlOneOfIRI,
		owlComplementOfIRI, owlHasKeyIRI, owlMembersIRI, owlDistinctMembersIRI, owlSourceIndividualIRI,
		owlAssertionPropertyIRI, owlTargetIndividualIRI, owlTargetValueIRI:
		return true
	case rdfTypeIRI:
		switch triple.Object.Value {
		case owlIrreflexivePropertyIRI, owlAsymmetricPropertyIRI, owlAllDisjointClassesIRI,
			owlAllDisjointPropertiesIRI, owlAllDifferentIRI, owlNothingIRI:
			return true
		}
	}
	return false
}
