package graph

import (
	"fmt"
	"sort"
	"strings"
)

// Keys, chains and contradictions: the second slice of OWL 2 RL.
//
// The rules in owl.go repair what extraction leaves out — the other direction
// of a relation, the second name of a thing. The rules here use what a person
// knows about the domain to go further, and to say when the graph cannot be
// right:
//
//   - A functional property (prp-fp) has one value per subject, and an
//     inverse-functional one (prp-ifp) one subject per value. Two values, or
//     two subjects, therefore name the same individual. That is entity
//     resolution from keys: declare the e-mail property inverse-functional and
//     two person nodes with the same address become one person, through the
//     same sameAs machinery — and the same class-size cap — as a stated
//     owl:sameAs.
//   - A property chain (prp-spo2) composes properties: runs_on followed by
//     hosted_on is ultimately_hosted_on, however long the chain.
//   - Disjoint classes (cax-dw), disjoint properties (prp-pdw), a functional
//     property with two different literal values, and owl:differentFrom
//     between two names that sameAs reasoning would merge (eq-diff1) are
//     contradictions. A contradiction is not a triple: OWL's answer is that
//     the whole graph entails everything, which is useless, and quietly
//     picking one side would be worse — the graph would look consistent and
//     be arbitrarily wrong. So each one is reported, with the triples that
//     conflict and their ids, and nothing is merged or dropped because of it.
//
// A differentFrom conflict is the one contradiction that changes what is
// materialized. The class that contains both names is exactly the kind of
// over-eager merge the sameAs cap guards against, and copying statements
// across it would spread the mistake the report is pointing at. So its sameAs
// reasoning is suspended, as an oversized class's is, and the sameAs that a
// key would have concluded inside it is listed in the report rather than
// stored.

const (
	owlFunctionalPropertyIRI        = owlNamespace + "FunctionalProperty"
	owlInverseFunctionalPropertyIRI = owlNamespace + "InverseFunctionalProperty"
	owlPropertyChainAxiomIRI        = owlNamespace + "propertyChainAxiom"
	owlDisjointWithIRI              = owlNamespace + "disjointWith"
	owlPropertyDisjointWithIRI      = owlNamespace + "propertyDisjointWith"
	owlDifferentFromIRI             = owlNamespace + "differentFrom"

	owlRuleFunctional        = "owl_functional_property"
	owlRuleInverseFunctional = "owl_inverse_functional_property"
	owlRulePropertyChain     = "owl_property_chain"

	// InconsistencyDisjointClasses is OWL 2 RL cax-dw: an individual typed
	// with two classes declared owl:disjointWith.
	InconsistencyDisjointClasses = "cax-dw"
	// InconsistencyDisjointProperties is OWL 2 RL prp-pdw: one pair related by
	// two properties declared owl:propertyDisjointWith.
	InconsistencyDisjointProperties = "prp-pdw"
	// InconsistencyFunctionalValues is OWL 2 RL prp-fp where the two values
	// cannot be the same individual because at least one is a literal.
	InconsistencyFunctionalValues = "prp-fp"
	// InconsistencyDifferentFrom is OWL 2 RL eq-diff1: two terms declared
	// owl:differentFrom that sameAs reasoning makes the same individual, or a
	// term declared different from itself.
	InconsistencyDifferentFrom = "eq-diff1"

	// maxReportedInconsistencies bounds the list a refresh returns. The count
	// is always complete; the list is for a person to read and fix, and a
	// graph with thousands of clashes almost always has one bad axiom behind
	// them, which the first hundred show as well as all of them would.
	maxReportedInconsistencies = 100
)

var (
	keyRDFFirst                = engineTermKey(NewIRI(rdfFirstIRI))
	keyRDFRest                 = engineTermKey(NewIRI(rdfRestIRI))
	keyOWLDisjointWith         = engineTermKey(NewIRI(owlDisjointWithIRI))
	keyOWLPropertyDisjointWith = engineTermKey(NewIRI(owlPropertyDisjointWithIRI))
	keyOWLDifferentFrom        = engineTermKey(NewIRI(owlDifferentFromIRI))
)

// InferenceInconsistency is one contradiction a refresh found. It is
// reported, never resolved: nothing is merged, dropped or rewritten because of
// it, and the triples listed are the ones a person needs to look at to decide
// which statement is wrong.
type InferenceInconsistency struct {
	// Rule is the OWL 2 RL rule that detected it: cax-dw, prp-pdw, prp-fp or
	// eq-diff1.
	Rule string `json:"rule"`
	// Explanation says in one sentence what contradicts what.
	Explanation string `json:"explanation"`
	// Triples are the conflicting statements, each with its id. An inferred
	// one carries its rule and support ids, so ExplainTriple walks it back to
	// what was stated. For eq-diff1 the owl:differentFrom statement comes
	// first, followed by the stored owl:sameAs statements that join the two
	// terms.
	Triples []RDFTriple `json:"triples"`
	// SuspendedSameAs, for eq-diff1 only, are the owl:sameAs conclusions on
	// the joining path that a functional or inverse-functional key would have
	// drawn but that were not stored because of the conflict. They are not in
	// the store, so their ids resolve to nothing; their support ids do.
	SuspendedSameAs []RDFTriple `json:"suspended_same_as,omitempty"`
	// Members, for eq-diff1 only, is a sorted sample of the sameAs class whose
	// reasoning was suspended, in N-Triples syntax; ClassSize is its size.
	Members   []string `json:"members,omitempty"`
	ClassSize int      `json:"class_size,omitempty"`
}

// owlRLState is the per-run state of the rules in this file.
type owlRLState struct {
	// chainsByLink indexes registered property chains by each property they
	// contain, so a statement finds every chain it can be a link of.
	chainsByLink map[string][]chainLinkRef
	// candidates are sameAs conclusions of prp-fp and prp-ifp that were not
	// materialized because an end is suspended. They still join classes, so
	// the conflict that suspended them stays visible in the next run.
	candidates    []*rdfsInferenceRecord
	candidateKeys map[string]bool
	clashes       map[string]*InferenceInconsistency

	// The list axioms of owl_rl_rules.go, by member and by the class or
	// property they are about, and the owl:AllDifferent axioms, which
	// sameAsConflicts checks once the run is over.
	listAxiomKeys map[string]bool
	listByMember  map[string][]*owlListAxiom
	listByHead    map[string][]*owlListAxiom
	allDifferent  []*owlListAxiom
}

type chainAxiom struct {
	head     RDFTerm
	linkKeys []string
	// supports are the axiom and the list cells it was read from.
	supports []*rdfsInferenceRecord
}

type chainLinkRef struct {
	axiom    *chainAxiom
	position int
}

// fireOWLRL applies the rules of this file in which the record can be a
// premise. Like fire and fireOWL it tries the record in every position, as a
// declaration joined against the statements it governs and as a statement
// joined against the declarations that govern it, so each derivation is found
// through whichever premise arrived last.
func (e *inferenceEngine) fireOWLRL(record *rdfsInferenceRecord) {
	t := record.Triple

	switch t.Predicate.Value {
	case rdfTypeIRI:
		if t.Subject.Kind == RDFTermIRI && t.Object.Kind == RDFTermIRI {
			switch t.Object.Value {
			case owlFunctionalPropertyIRI:
				for _, use := range e.usingPredicate(t.Subject) {
					e.applyFunctional(use, record)
				}
			case owlInverseFunctionalPropertyIRI:
				for _, use := range e.usingPredicate(t.Subject) {
					e.applyInverseFunctional(use, record)
				}
			}
		}
		e.checkDisjointClassesOf(record)
	case owlPropertyChainAxiomIRI:
		e.registerChain(record)
	case owlDisjointWithIRI:
		if isResourceTerm(t.Subject) && isResourceTerm(t.Object) {
			for _, instance := range e.withObject(keyRDFType, t.Subject) {
				for _, other := range e.withSubject(keyRDFType, instance.Triple.Subject) {
					if termsEqual(other.Triple.Object, t.Object) {
						e.reportDisjointClasses(record, instance, other)
					}
				}
			}
		}
	case owlPropertyDisjointWithIRI:
		if t.Subject.Kind == RDFTermIRI && t.Object.Kind == RDFTermIRI {
			otherKey := engineTermKey(t.Object)
			for _, use := range e.usingPredicate(t.Subject) {
				for _, other := range e.withSubject(otherKey, use.Triple.Subject) {
					if termsEqual(other.Triple.Object, use.Triple.Object) {
						e.reportDisjointProperties(record, use, other)
					}
				}
			}
		}
	}

	// The record as a statement, against the declarations about its
	// predicate.
	for _, declaration := range e.withSubject(keyRDFType, t.Predicate) {
		if declaration.Triple.Object.Kind != RDFTermIRI {
			continue
		}
		switch declaration.Triple.Object.Value {
		case owlFunctionalPropertyIRI:
			e.applyFunctional(record, declaration)
		case owlInverseFunctionalPropertyIRI:
			e.applyInverseFunctional(record, declaration)
		}
	}
	for _, declaration := range e.withSubject(keyOWLPropertyDisjointWith, t.Predicate) {
		e.checkDisjointPropertyUse(record, declaration, declaration.Triple.Object)
	}
	for _, declaration := range e.withObject(keyOWLPropertyDisjointWith, t.Predicate) {
		e.checkDisjointPropertyUse(record, declaration, declaration.Triple.Subject)
	}
	for _, ref := range e.owlRL.chainsByLink[engineTermKey(t.Predicate)] {
		e.extendChain(ref.axiom, ref.position, record)
	}
	e.fireOWLRules(record)
}

// applyFunctional is prp-fp with use as one of the two statements.
//
// Two resource values become the same individual, stated pairwise and in both
// directions as the rule states them, so that a value arriving a round after
// another is joined to it by the key and not left for the symmetric rule —
// which a suspended or oversized class would never run. A subject with more distinct resource values than the sameAs
// cap is the exception: its values form a class the cap will refuse to
// materialize anyway, and stating every pair would cost the square of the
// group before the cap could see it. Such a group is joined as a star around
// the first resource value the index holds — the index only grows, so that
// value never changes and every member reaches it — which is enough for the
// class to be found, sized and reported.
//
// A literal cannot be the same individual as anything but an equal literal,
// so a literal value that differs from another value is a contradiction and
// is reported with every value it differs from. Literals are compared as
// terms: "1"^^xsd:integer and "01"^^xsd:integer are reported as different.
func (e *inferenceEngine) applyFunctional(use, declaration *rdfsInferenceRecord) {
	u := use.Triple
	group := e.withSubject(engineTermKey(u.Predicate), u.Subject)
	if isResourceTerm(u.Object) {
		values := func(r *rdfsInferenceRecord) RDFTerm { return r.Triple.Object }
		for _, other := range e.keyPartners(group, u.Object, values) {
			graph := mergeInferenceGraph(u.Graph, other.Triple.Graph)
			e.deriveKeySameAs(u.Object, other.Triple.Object, graph, owlRuleFunctional, use, other, declaration)
			e.deriveKeySameAs(other.Triple.Object, u.Object, graph, owlRuleFunctional, other, use, declaration)
		}
	}
	for _, other := range group {
		o := other.Triple.Object
		if termsEqual(o, u.Object) || (isResourceTerm(o) && isResourceTerm(u.Object)) {
			continue
		}
		e.reportFunctionalValues(declaration, use, other)
	}
}

// applyInverseFunctional is prp-ifp with use as one of the two statements:
// two subjects with the same value are the same individual. The value may be
// a literal — an e-mail address, an ID number — which is the common case of a
// key. A group larger than the cap is joined as a star, as in
// applyFunctional.
func (e *inferenceEngine) applyInverseFunctional(use, declaration *rdfsInferenceRecord) {
	u := use.Triple
	if !isResourceTerm(u.Subject) {
		return
	}
	group := e.withObject(engineTermKey(u.Predicate), u.Object)
	subjects := func(r *rdfsInferenceRecord) RDFTerm { return r.Triple.Subject }
	for _, other := range e.keyPartners(group, u.Subject, subjects) {
		graph := mergeInferenceGraph(u.Graph, other.Triple.Graph)
		e.deriveKeySameAs(u.Subject, other.Triple.Subject, graph, owlRuleInverseFunctional, use, other, declaration)
		e.deriveKeySameAs(other.Triple.Subject, u.Subject, graph, owlRuleInverseFunctional, other, use, declaration)
	}
}

// keyPartners returns the statements of a key group whose resource term the
// use's own term is to be joined with: every other one while the group's
// distinct resource terms fit under the sameAs cap, only the first beyond it.
func (e *inferenceEngine) keyPartners(group []*rdfsInferenceRecord, own RDFTerm, term func(*rdfsInferenceRecord) RDFTerm) []*rdfsInferenceRecord {
	distinct := make(map[string]bool)
	var resources []*rdfsInferenceRecord
	for _, record := range group {
		if t := term(record); isResourceTerm(t) {
			resources = append(resources, record)
			distinct[engineTermKey(t)] = true
		}
	}
	if len(distinct) > e.sameAs.limit {
		resources = resources[:1]
	}
	out := resources[:0:0]
	for _, record := range resources {
		if !termsEqual(term(record), own) {
			out = append(out, record)
		}
	}
	return out
}

// deriveKeySameAs derives a sameAs concluded from a key, unless an end has
// its sameAs reasoning suspended by a differentFrom conflict. Then the
// conclusion is kept aside as a candidate: not stored, but still joining the
// two classes, so the conflict that caused the suspension is found again on
// the next run and can be reported with this conclusion in it.
func (e *inferenceEngine) deriveKeySameAs(subject, object RDFTerm, graph *RDFTerm, rule string, supports ...*rdfsInferenceRecord) {
	if !e.sameAs.suspended[engineTermKey(subject)] && !e.sameAs.suspended[engineTermKey(object)] {
		e.derive(subject, termOWLSameAs, object, graph, rule, supports...)
		return
	}
	triple := RDFTriple{Subject: subject, Predicate: termOWLSameAs, Object: object, Graph: cloneGraphTerm(graph)}
	key := inferenceContentKey(triple)
	if e.owlRL.candidateKeys[key] {
		return
	}
	if e.owlRL.candidateKeys == nil {
		e.owlRL.candidateKeys = make(map[string]bool)
	}
	e.owlRL.candidateKeys[key] = true
	triple.ID = tripleDigest(triple)
	ids := make([]string, 0, len(supports))
	for _, support := range supports {
		ids = append(ids, support.Triple.ID)
	}
	e.owlRL.candidates = append(e.owlRL.candidates, &rdfsInferenceRecord{Triple: triple, Rule: rule, SupportIDs: uniqueSortedStrings(ids), key: key})
}

// registerChain reads a propertyChainAxiom and joins it against every path
// already known.
//
// The list is read from explicit rdf:first / rdf:rest statements only. A list
// is syntax for the axiom, not knowledge about the world, and letting
// inference rewrite it — sameAs copying a cell onto another node, say — would
// let the rules change their own premises. A list that is not a proper chain
// (a cell with two firsts or two rests, a cycle, a non-IRI member, fewer than
// two members) is ignored rather than guessed at.
func (e *inferenceEngine) registerChain(record *rdfsInferenceRecord) {
	t := record.Triple
	if t.Subject.Kind != RDFTermIRI {
		return
	}
	links, cells, ok := e.readChainList(t.Object)
	if !ok {
		return
	}
	axiom := &chainAxiom{head: t.Subject, supports: append([]*rdfsInferenceRecord{record}, cells...)}
	for _, link := range links {
		axiom.linkKeys = append(axiom.linkKeys, engineTermKey(link))
	}
	if e.owlRL.chainsByLink == nil {
		e.owlRL.chainsByLink = make(map[string][]chainLinkRef)
	}
	for position, key := range axiom.linkKeys {
		e.owlRL.chainsByLink[key] = append(e.owlRL.chainsByLink[key], chainLinkRef{axiom: axiom, position: position})
	}
	// Every path has a first link, so starting from each statement of the
	// first property finds every path once.
	for _, first := range e.byPredicate[axiom.linkKeys[0]] {
		e.extendChain(axiom, 0, first)
	}
}

// readChainList walks an RDF list of properties. The visited set is what ends
// a list whose rdf:rest points back into itself.
func (e *inferenceEngine) readChainList(head RDFTerm) ([]RDFTerm, []*rdfsInferenceRecord, bool) {
	var links []RDFTerm
	var cells []*rdfsInferenceRecord
	visited := make(map[string]bool)
	for current := head; ; {
		if current.Kind == RDFTermIRI && current.Value == rdfNilIRI {
			break
		}
		key := engineTermKey(current)
		if visited[key] {
			return nil, nil, false
		}
		visited[key] = true
		first, ok := soleExplicitObject(e.withSubject(keyRDFFirst, current))
		if !ok || first.Triple.Object.Kind != RDFTermIRI {
			return nil, nil, false
		}
		rest, ok := soleExplicitObject(e.withSubject(keyRDFRest, current))
		if !ok {
			return nil, nil, false
		}
		links = append(links, first.Triple.Object)
		cells = append(cells, first, rest)
		current = rest.Triple.Object
	}
	if len(links) < 2 {
		return nil, nil, false
	}
	return links, cells, true
}

// soleExplicitObject returns the first explicit record when every explicit
// record agrees on the object — the same cell stated in two graphs is still
// one cell — and false when there is none or they disagree.
func soleExplicitObject(records []*rdfsInferenceRecord) (*rdfsInferenceRecord, bool) {
	var found *rdfsInferenceRecord
	for _, record := range records {
		if !record.Explicit {
			continue
		}
		if found == nil {
			found = record
			continue
		}
		if !termsEqual(found.Triple.Object, record.Triple.Object) {
			return nil, false
		}
	}
	return found, found != nil
}

// extendChain finds every path through the chain with record at position and
// derives the chain's property from each. It walks left to the first link and
// right to the last through the indexes, so only paths containing the new
// record are visited. A cycle in the data needs no guard: a path has exactly
// as many steps as the chain has links, and a conclusion already known is not
// derived again.
func (e *inferenceEngine) extendChain(axiom *chainAxiom, position int, record *rdfsInferenceRecord) {
	n := len(axiom.linkKeys)
	path := make([]*rdfsInferenceRecord, n)
	path[position] = record
	var right func(j int, from RDFTerm)
	right = func(j int, from RDFTerm) {
		if j == n {
			supports := append(append([]*rdfsInferenceRecord(nil), path...), axiom.supports...)
			e.derive(path[0].Triple.Subject, axiom.head, path[n-1].Triple.Object, agreeingInferenceGraph(path), owlRulePropertyChain, supports...)
			return
		}
		for _, next := range e.withSubject(axiom.linkKeys[j], from) {
			path[j] = next
			right(j+1, next.Triple.Object)
		}
	}
	var left func(j int, to RDFTerm)
	left = func(j int, to RDFTerm) {
		if j < 0 {
			right(position+1, record.Triple.Object)
			return
		}
		for _, prev := range e.withObject(axiom.linkKeys[j], to) {
			path[j] = prev
			left(j-1, prev.Triple.Subject)
		}
	}
	left(position-1, record.Triple.Subject)
}

// agreeingInferenceGraph is mergeInferenceGraph over any number of premises:
// the graph they all name, ignoring those in the default graph, or the
// default graph when two disagree. For two premises it is mergeInferenceGraph.
func agreeingInferenceGraph(records []*rdfsInferenceRecord) *RDFTerm {
	var graph *RDFTerm
	for _, record := range records {
		g := record.Triple.Graph
		if g == nil {
			continue
		}
		if graph == nil {
			graph = g
		} else if !termsEqual(*graph, *g) {
			return nil
		}
	}
	return cloneGraphTerm(graph)
}

// checkDisjointClassesOf is cax-dw with record as one of the two types.
func (e *inferenceEngine) checkDisjointClassesOf(record *rdfsInferenceRecord) {
	t := record.Triple
	for _, declaration := range e.withSubject(keyOWLDisjointWith, t.Object) {
		e.checkTypedAs(record, declaration, declaration.Triple.Object)
	}
	for _, declaration := range e.withObject(keyOWLDisjointWith, t.Object) {
		e.checkTypedAs(record, declaration, declaration.Triple.Subject)
	}
}

func (e *inferenceEngine) checkTypedAs(record, declaration *rdfsInferenceRecord, other RDFTerm) {
	if !isResourceTerm(declaration.Triple.Subject) || !isResourceTerm(declaration.Triple.Object) {
		return
	}
	for _, typed := range e.withSubject(keyRDFType, record.Triple.Subject) {
		if termsEqual(typed.Triple.Object, other) {
			e.reportDisjointClasses(declaration, record, typed)
		}
	}
}

// checkDisjointPropertyUse is prp-pdw with record as one of the two uses.
func (e *inferenceEngine) checkDisjointPropertyUse(record, declaration *rdfsInferenceRecord, other RDFTerm) {
	if declaration.Triple.Subject.Kind != RDFTermIRI || declaration.Triple.Object.Kind != RDFTermIRI {
		return
	}
	t := record.Triple
	for _, use := range e.withSubject(engineTermKey(other), t.Subject) {
		if termsEqual(use.Triple.Object, t.Object) {
			e.reportDisjointProperties(declaration, record, use)
		}
	}
}

func (e *inferenceEngine) reportDisjointClasses(declaration, a, b *rdfsInferenceRecord) {
	e.reportClash(InconsistencyDisjointClasses, func(premises []*rdfsInferenceRecord) string {
		d := declaration.Triple
		if termsEqual(d.Subject, d.Object) {
			return fmt.Sprintf("%s is an instance of %s, which is declared disjoint with itself and so can have no instances",
				premises[0].Triple.Subject, d.Subject)
		}
		return fmt.Sprintf("%s is an instance of both %s and %s, which are declared disjoint",
			premises[0].Triple.Subject, d.Subject, d.Object)
	}, declaration, a, b)
}

func (e *inferenceEngine) reportDisjointProperties(declaration, a, b *rdfsInferenceRecord) {
	e.reportClash(InconsistencyDisjointProperties, func(premises []*rdfsInferenceRecord) string {
		d, u := declaration.Triple, premises[0].Triple
		if termsEqual(d.Subject, d.Object) {
			return fmt.Sprintf("%s relates %s to %s, but the property is declared disjoint with itself", d.Subject, u.Subject, u.Object)
		}
		return fmt.Sprintf("%s is related to %s by both %s and %s, which are declared disjoint properties",
			u.Subject, u.Object, d.Subject, d.Object)
	}, declaration, a, b)
}

func (e *inferenceEngine) reportFunctionalValues(declaration, a, b *rdfsInferenceRecord) {
	e.reportClash(InconsistencyFunctionalValues, func(premises []*rdfsInferenceRecord) string {
		return fmt.Sprintf("%s is a functional property, but %s has two different values for it, %s and %s; a literal cannot be merged with another value, so nothing was merged",
			declaration.Triple.Subject, premises[0].Triple.Subject, premises[0].Triple.Object, premises[len(premises)-1].Triple.Object)
	}, declaration, a, b)
}

// reportClash records one contradiction, once. Its identity is the rule and
// the set of statements involved, so a clash found from both of its premises,
// or in two rounds, is one report. The premises after the declaration are put
// in content order, so the report reads the same however the engine found it.
func (e *inferenceEngine) reportClash(rule string, explain func(premises []*rdfsInferenceRecord) string, declaration *rdfsInferenceRecord, premises ...*rdfsInferenceRecord) {
	unique := make([]*rdfsInferenceRecord, 0, len(premises))
	seen := map[string]bool{declaration.key: true}
	for _, premise := range premises {
		if !seen[premise.key] {
			seen[premise.key] = true
			unique = append(unique, premise)
		}
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].key < unique[j].key })
	if len(unique) == 0 {
		// The declaration is its own premise — p disjointWith p stated with
		// p as a type, say. The statement still names the contradiction.
		unique = append(unique, declaration)
	}
	keys := []string{rule, declaration.key}
	for _, premise := range unique {
		keys = append(keys, premise.key)
	}
	id := strings.Join(keys, "\x02")
	if _, ok := e.owlRL.clashes[id]; ok {
		return
	}
	if e.owlRL.clashes == nil {
		e.owlRL.clashes = make(map[string]*InferenceInconsistency)
	}
	triples := []RDFTriple{reportedTriple(declaration)}
	for _, premise := range unique {
		if premise != declaration {
			triples = append(triples, reportedTriple(premise))
		}
	}
	e.owlRL.clashes[id] = &InferenceInconsistency{Rule: rule, Explanation: explain(unique), Triples: triples}
}

// reportedTriple is the record as the store holds it, provenance included.
func reportedTriple(record *rdfsInferenceRecord) RDFTriple {
	out := tripleWithoutInference(record.Triple)
	if !record.Explicit {
		out.Inferred = true
		out.Rule = record.Rule
		out.SupportIDs = append([]string(nil), record.SupportIDs...)
	}
	return out
}

// sameAsConflicts finds the eq-diff1 contradictions of a finished run: the
// owl:differentFrom statements whose two ends are in one sameAs class, and
// those that declare a term different from itself. It returns the members of
// every such class, which the next run suspends, and a report for each
// statement.
//
// Classes are taken over every sameAs statement the run holds, explicit or
// inferred, plus the key conclusions it set aside, so a class whose reasoning
// is already suspended is still recognised as the one in conflict.
func (e *inferenceEngine) sameAsConflicts() ([]string, []InferenceInconsistency) {
	differents := e.byPredicate[keyOWLDifferentFrom]
	if len(differents) == 0 && len(e.owlRL.allDifferent) == 0 {
		return nil, nil
	}
	parent := make(map[string]string)
	terms := make(map[string]RDFTerm)
	var find func(string) string
	find = func(key string) string {
		for parent[key] != key {
			parent[key] = parent[parent[key]]
			key = parent[key]
		}
		return key
	}
	add := func(term RDFTerm) string {
		key := engineTermKey(term)
		if _, ok := parent[key]; !ok {
			parent[key] = key
			terms[key] = term
		}
		return find(key)
	}
	type edge struct {
		to     string
		record *rdfsInferenceRecord
		held   bool
	}
	adjacent := make(map[string][]edge)
	join := func(record *rdfsInferenceRecord, held bool) {
		t := record.Triple
		if !isResourceTerm(t.Subject) || !isResourceTerm(t.Object) || termsEqual(t.Subject, t.Object) {
			return
		}
		a, b := add(t.Subject), add(t.Object)
		if a != b {
			parent[a] = b
		}
		s, o := engineTermKey(t.Subject), engineTermKey(t.Object)
		adjacent[s] = append(adjacent[s], edge{to: o, record: record, held: held})
		adjacent[o] = append(adjacent[o], edge{to: s, record: record, held: held})
	}
	for _, record := range e.byPredicate[keyOWLSameAs] {
		join(record, true)
	}
	for _, record := range e.owlRL.candidates {
		join(record, false)
	}

	// addPath adds the shortest sameAs path joining s and o to a report,
	// found breadth-first over neighbours in a fixed order so the report does
	// not depend on map order.
	addPath := func(report *InferenceInconsistency, s, o string) {
		via := map[string]edge{s: {}}
		from := map[string]string{}
		queue := []string{s}
		for len(queue) > 0 && !hasKey(via, o) {
			current := queue[0]
			queue = queue[1:]
			edges := append([]edge(nil), adjacent[current]...)
			sort.Slice(edges, func(i, j int) bool {
				if edges[i].to != edges[j].to {
					return edges[i].to < edges[j].to
				}
				return edges[i].record.key < edges[j].record.key
			})
			for _, next := range edges {
				if hasKey(via, next.to) {
					continue
				}
				via[next.to] = next
				from[next.to] = current
				queue = append(queue, next.to)
			}
		}
		var path []edge
		for at := o; at != s; at = from[at] {
			path = append(path, via[at])
		}
		for i := len(path) - 1; i >= 0; i-- {
			if path[i].held {
				report.Triples = append(report.Triples, reportedTriple(path[i].record))
			} else {
				report.SuspendedSameAs = append(report.SuspendedSameAs, reportedTriple(path[i].record))
			}
		}
	}
	classMembers := func(root string) []string {
		var members []string
		for key := range parent {
			if find(key) == root {
				members = append(members, terms[key].String())
			}
		}
		sort.Strings(members)
		return members
	}

	var reports []InferenceInconsistency
	conflicted := make(map[string]bool)
	// eq-diff2 / eq-diff3: two members of an owl:AllDifferent in one class.
	for _, axiom := range e.owlRL.allDifferent {
		firstByRoot := map[string]string{}
		for _, m := range axiom.members {
			key := engineTermKey(m)
			if _, ok := parent[key]; !ok || !isResourceTerm(m) {
				continue
			}
			root := find(key)
			first, seen := firstByRoot[root]
			if !seen {
				firstByRoot[root] = key
				continue
			}
			if first == key {
				continue
			}
			conflicted[root] = true
			report := InferenceInconsistency{Rule: InconsistencyAllDifferent}
			for _, support := range axiom.supports {
				report.Triples = append(report.Triples, reportedTriple(support))
			}
			addPath(&report, first, key)
			members := classMembers(root)
			report.ClassSize = len(members)
			if len(members) > oversizedSameAsSampleSize {
				members = members[:oversizedSameAsSampleSize]
			}
			report.Members = members
			report.Explanation = fmt.Sprintf("%s and %s are members of an owl:AllDifferent, but owl:sameAs reasoning makes them one individual; sameAs reasoning over their class of %d terms is suspended and nothing in it was merged",
				terms[first], m, report.ClassSize)
			reports = append(reports, report)
		}
	}
	for _, different := range differents {
		d := different.Triple
		if !isResourceTerm(d.Subject) || !isResourceTerm(d.Object) {
			continue
		}
		if termsEqual(d.Subject, d.Object) {
			reports = append(reports, InferenceInconsistency{
				Rule:        InconsistencyDifferentFrom,
				Explanation: fmt.Sprintf("%s is declared different from itself, but every individual is the same as itself", d.Subject),
				Triples:     []RDFTriple{reportedTriple(different)},
			})
			continue
		}
		s, o := engineTermKey(d.Subject), engineTermKey(d.Object)
		if _, ok := parent[s]; !ok {
			continue
		}
		if _, ok := parent[o]; !ok || find(s) != find(o) {
			continue
		}
		root := find(s)
		conflicted[root] = true

		report := InferenceInconsistency{
			Rule:    InconsistencyDifferentFrom,
			Triples: []RDFTriple{reportedTriple(different)},
		}
		addPath(&report, s, o)
		members := classMembers(root)
		report.ClassSize = len(members)
		if len(members) > oversizedSameAsSampleSize {
			members = members[:oversizedSameAsSampleSize]
		}
		report.Members = members
		report.Explanation = fmt.Sprintf("%s and %s are declared different, but owl:sameAs reasoning makes them one individual; sameAs reasoning over their class of %d terms is suspended and nothing in it was merged",
			d.Subject, d.Object, report.ClassSize)
		reports = append(reports, report)
	}

	var members []string
	for key := range parent {
		if conflicted[find(key)] {
			members = append(members, key)
		}
	}
	sort.Strings(members)
	return members, reports
}

func hasKey[V any](m map[string]V, key string) bool {
	_, ok := m[key]
	return ok
}

// inferenceOutcome is everything a refresh learns from one computation.
type inferenceOutcome struct {
	records         map[string]rdfsInferenceRecord
	oversized       []OversizedSameAsClass
	inconsistencies []InferenceInconsistency
}

// computeInferenceOutcome runs the engine until no differentFrom conflict is
// left unsuspended.
//
// Which sameAs classes conflict is known only once inference has finished,
// since the sameAs and differentFrom statements that make a conflict can both
// be inferred. So a run that finds a conflicted class whose members were not
// yet suspended suspends them all and runs again from the explicit triples.
// Suspending only removes conclusions, so the next run's classes are the same
// or smaller and every member of a suspended class stays inside a suspended
// class; each extra run suspends at least one new term, so the loop ends. A
// graph without owl:differentFrom runs exactly once.
func computeInferenceOutcome(explicitTriples []RDFTriple, opts InferenceOptions) inferenceOutcome {
	suspended := make(map[string]bool)
	for {
		engine := runInferenceEngine(explicitTriples, opts, suspended)
		members, conflicts := engine.sameAsConflicts()
		grew := false
		for _, key := range members {
			if !suspended[key] {
				suspended[key] = true
				grew = true
			}
		}
		if grew {
			continue
		}
		out := make(map[string]rdfsInferenceRecord, len(engine.records))
		for key, record := range engine.records {
			out[key] = *record
		}
		inconsistencies := conflicts
		for _, clash := range engine.owlRL.clashes {
			inconsistencies = append(inconsistencies, *clash)
		}
		sortInconsistencies(inconsistencies)
		return inferenceOutcome{records: out, oversized: engine.sameAs.report(), inconsistencies: inconsistencies}
	}
}

func sortInconsistencies(list []InferenceInconsistency) {
	type keyed struct {
		key  string
		item InferenceInconsistency
	}
	sorted := make([]keyed, len(list))
	for i, item := range list {
		parts := []string{item.Rule}
		for _, triple := range item.Triples {
			parts = append(parts, triple.String())
		}
		sorted[i] = keyed{key: strings.Join(parts, "\x02"), item: item}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
	for i := range sorted {
		list[i] = sorted[i].item
	}
}

// reportInconsistencies trims the list for a refresh result and returns it
// with the full count.
func reportInconsistencies(list []InferenceInconsistency) ([]InferenceInconsistency, int) {
	if len(list) > maxReportedInconsistencies {
		return list[:maxReportedInconsistencies], len(list)
	}
	return list, len(list)
}
