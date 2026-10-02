package graph

import "sort"

// An OWL subset for graphs that machines wrote.
//
// Most triples in a CortexDB brain come from LLM extraction, and extraction
// has two chronic failures that RDFS cannot repair. It stores one relation in
// only one direction — "alice manages bob" with no "bob reports to alice" —
// so a question asked from the other end finds nothing. And it stores one
// thing twice under two names — node types "project" and "Project", "host" and
// "Host", an entity once by its full name and once by its handle — so facts
// about it are split between two nodes that never meet.
//
// The rules here are the part of OWL 2 RL that answers those failures and
// nothing more: inverse, symmetric and transitive properties for the first;
// equivalent classes and properties and owl:sameAs for the second. They run in
// the same semi-naive loop as the RDFS rules in rdfs.go and leave the same
// trail — a rule name and the ids of the triples each inference rests on — so
// ExplainTriple can walk any of them back to what was actually stated.
//
// Keys, property chains and the contradictions — disjointness, differentFrom —
// are in owl_rl.go, which also gives the engine its inconsistency report.
// Restrictions, cardinality and the rest of OWL are deliberately absent: each
// would need a volume of materialization no one has asked for.

const (
	owlNamespace              = "http://www.w3.org/2002/07/owl#"
	owlInverseOfIRI           = owlNamespace + "inverseOf"
	owlSymmetricPropertyIRI   = owlNamespace + "SymmetricProperty"
	owlTransitivePropertyIRI  = owlNamespace + "TransitiveProperty"
	owlEquivalentClassIRI     = owlNamespace + "equivalentClass"
	owlEquivalentPropertyIRI  = owlNamespace + "equivalentProperty"
	owlSameAsIRI              = owlNamespace + "sameAs"
	owlRuleInverseOf          = "owl_inverse_of"
	owlRuleSymmetric          = "owl_symmetric"
	owlRuleTransitive         = "owl_transitive"
	owlRuleEquivalentClass    = "owl_equivalent_class"
	owlRuleEquivalentProperty = "owl_equivalent_property"
	owlRuleSameAsSymmetric    = "owl_same_as_symmetric"
	owlRuleSameAsTransitive   = "owl_same_as_transitive"
	owlRuleSameAsSubject      = "owl_same_as_subject"
	owlRuleSameAsObject       = "owl_same_as_object"

	// DefaultMaxSameAsClassSize is the largest owl:sameAs class materialized
	// when InferenceOptions does not say otherwise.
	//
	// Materializing sameAs copies every statement about a member onto every
	// other member, so a class of n names multiplies what is known about it by
	// n, and a statement between two classes by the product of their sizes.
	// Legitimate duplicates come in small numbers — a person under three
	// spellings, a host under its name and its address. A class of hundreds is
	// almost always an extraction mistake, one over-eager "same as" that
	// chained unrelated things together, and materializing it would bury the
	// graph in copies of the mistake. Thirty-two is well above the first case
	// and well below the point where the copies dominate.
	DefaultMaxSameAsClassSize = 32

	// oversizedSameAsSampleSize bounds how many members a report lists. The
	// size is always reported in full; the members are there to let a person
	// recognise the class, not to enumerate it.
	oversizedSameAsSampleSize = 16
)

// OversizedSameAsClass is an owl:sameAs equivalence class larger than the
// configured cap, reported instead of materialized.
//
// Nothing is inferred from an oversized class's sameAs edges — neither their
// symmetric and transitive closure nor the statements they would copy — and
// the class is named here so the edge that joined it can be found and fixed.
// Inferring part of it, say the first thirty-two members, would be worse than
// inferring none: the output would look complete and be arbitrary.
type OversizedSameAsClass struct {
	// Size is the number of distinct terms in the class.
	Size int `json:"size"`
	// Cap is the limit the class exceeded.
	Cap int `json:"cap"`
	// Members is a sorted sample of at most sixteen members, in N-Triples
	// syntax.
	Members []string `json:"members"`
}

var (
	keyOWLEquivalentClass    = engineTermKey(NewIRI(owlEquivalentClassIRI))
	keyOWLEquivalentProperty = engineTermKey(NewIRI(owlEquivalentPropertyIRI))
	termOWLSameAs            = NewIRI(owlSameAsIRI)
)

// fireOWL applies the OWL rules in which the record can be a premise. Like
// fire, it tries the record in every position: as a vocabulary declaration,
// joined against the statements it governs, and as a statement, joined against
// the declarations that govern it.
func (e *inferenceEngine) fireOWL(record *rdfsInferenceRecord) {
	t := record.Triple

	switch t.Predicate.Value {
	case owlInverseOfIRI:
		// p inverseOf q is read in both directions: it turns p into q and q
		// into p. Materializing q inverseOf p and letting a second pass handle
		// it would give the same answer with an extra inference nobody asked
		// to see.
		if t.Subject.Kind == RDFTermIRI && t.Object.Kind == RDFTermIRI {
			for _, use := range e.usingPredicate(t.Subject) {
				e.deriveInverse(use, t.Object, record)
			}
			for _, use := range e.usingPredicate(t.Object) {
				e.deriveInverse(use, t.Subject, record)
			}
		}
	case owlEquivalentClassIRI:
		// Equivalence is subclass in both directions; the RDFS rules then
		// carry instances across, and nothing else needs to know OWL.
		if isResourceTerm(t.Subject) && isResourceTerm(t.Object) {
			e.derive(t.Subject, termSubClassOf, t.Object, t.Graph, owlRuleEquivalentClass, record)
			e.derive(t.Object, termSubClassOf, t.Subject, t.Graph, owlRuleEquivalentClass, record)
		}
	case owlEquivalentPropertyIRI:
		if t.Subject.Kind == RDFTermIRI && t.Object.Kind == RDFTermIRI {
			e.derive(t.Subject, termSubPropertyOf, t.Object, t.Graph, owlRuleEquivalentProperty, record)
			e.derive(t.Object, termSubPropertyOf, t.Subject, t.Graph, owlRuleEquivalentProperty, record)
		}
	case rdfTypeIRI:
		if t.Subject.Kind == RDFTermIRI && t.Object.Kind == RDFTermIRI {
			switch t.Object.Value {
			case owlSymmetricPropertyIRI:
				for _, use := range e.usingPredicate(t.Subject) {
					e.deriveSymmetric(use, record)
				}
			case owlTransitivePropertyIRI:
				predicate := engineTermKey(t.Subject)
				for _, first := range e.byPredicate[predicate] {
					for _, second := range e.withSubject(predicate, first.Triple.Object) {
						e.deriveTransitive(first, second, record)
					}
				}
			}
		}
	case owlSameAsIRI:
		e.fireSameAs(record)
	}

	predicate := engineTermKey(t.Predicate)
	for _, schema := range e.withSubject(keyOWLInverseOf, t.Predicate) {
		if schema.Triple.Object.Kind == RDFTermIRI {
			e.deriveInverse(record, schema.Triple.Object, schema)
		}
	}
	for _, schema := range e.withObject(keyOWLInverseOf, t.Predicate) {
		if schema.Triple.Subject.Kind == RDFTermIRI {
			e.deriveInverse(record, schema.Triple.Subject, schema)
		}
	}
	for _, declaration := range e.withSubject(keyRDFType, t.Predicate) {
		if declaration.Triple.Object.Kind != RDFTermIRI {
			continue
		}
		switch declaration.Triple.Object.Value {
		case owlSymmetricPropertyIRI:
			e.deriveSymmetric(record, declaration)
		case owlTransitivePropertyIRI:
			for _, second := range e.withSubject(predicate, t.Object) {
				e.deriveTransitive(record, second, declaration)
			}
			for _, first := range e.withObject(predicate, t.Subject) {
				e.deriveTransitive(first, record, declaration)
			}
		}
	}
	if t.Predicate.Value != owlSameAsIRI {
		e.replaceBySameAs(record)
	}
	e.fireOWLRL(record)
}

// deriveInverse turns x p y into y q x. A literal cannot be a subject, so a
// statement with a literal object has no inverse to state.
func (e *inferenceEngine) deriveInverse(use *rdfsInferenceRecord, inverse RDFTerm, schema *rdfsInferenceRecord) {
	u := use.Triple
	if u.Object.Kind == RDFTermLiteral {
		return
	}
	e.derive(u.Object, inverse, u.Subject, preferInferenceGraph(u.Graph, schema.Triple.Graph), owlRuleInverseOf, use, schema)
}

func (e *inferenceEngine) deriveSymmetric(use, declaration *rdfsInferenceRecord) {
	u := use.Triple
	if u.Object.Kind == RDFTermLiteral {
		return
	}
	e.derive(u.Object, u.Predicate, u.Subject, preferInferenceGraph(u.Graph, declaration.Triple.Graph), owlRuleSymmetric, use, declaration)
}

// deriveTransitive joins x p y and y p z into x p z. A cycle needs no guard of
// its own: it produces x p x and then nothing new, and derive refuses anything
// already known, which is what ends the loop. The graph follows the rule for
// subClassOf chains — kept when both premises share it, the default graph when
// they do not — because a chain that crosses graphs belongs to neither.
func (e *inferenceEngine) deriveTransitive(first, second, declaration *rdfsInferenceRecord) {
	e.derive(first.Triple.Subject, first.Triple.Predicate, second.Triple.Object,
		mergeInferenceGraph(first.Triple.Graph, second.Triple.Graph), owlRuleTransitive, first, second, declaration)
}

// fireSameAs applies the sameAs rules with the record as the equality.
//
// Four choices keep sameAs bounded and its output worth reading:
//
//   - Only IRIs and blank nodes are members. owl:sameAs relates individuals;
//     a literal on either side is a data error, not an identity, and is
//     ignored.
//   - x sameAs x is never derived. The closure of a cycle would otherwise
//     state every member identical to itself, which is true of everything and
//     informative about nothing.
//   - Statements are copied across the subject and object positions only,
//     never the predicate. Rewriting predicates would make sameAs a second,
//     unannounced equivalentProperty.
//   - sameAs statements themselves are not copied by replacement. Their
//     spread is exactly the symmetric and transitive closure, and crediting it
//     to those rules keeps an explanation saying what actually happened.
//
// And a class above the cap is left alone entirely; see OversizedSameAsClass.
func (e *inferenceEngine) fireSameAs(record *rdfsInferenceRecord) {
	t := record.Triple
	if !e.sameAs.materializes(t.Subject, t.Object) {
		return
	}
	e.derive(t.Object, termOWLSameAs, t.Subject, t.Graph, owlRuleSameAsSymmetric, record)
	for _, next := range e.withSubject(keyOWLSameAs, t.Object) {
		n := next.Triple
		if termsEqual(t.Subject, n.Object) || !e.sameAs.materializes(n.Subject, n.Object) {
			continue
		}
		e.derive(t.Subject, termOWLSameAs, n.Object, mergeInferenceGraph(t.Graph, n.Graph), owlRuleSameAsTransitive, record, next)
	}
	for _, prev := range e.withObject(keyOWLSameAs, t.Subject) {
		p := prev.Triple
		if termsEqual(p.Subject, t.Object) || !e.sameAs.materializes(p.Subject, p.Object) {
			continue
		}
		e.derive(p.Subject, termOWLSameAs, t.Object, mergeInferenceGraph(p.Graph, t.Graph), owlRuleSameAsTransitive, prev, record)
	}
	for _, about := range e.bySubject[engineTermKey(t.Subject)] {
		a := about.Triple
		if a.Predicate.Value == owlSameAsIRI {
			continue
		}
		e.derive(t.Object, a.Predicate, a.Object, preferInferenceGraph(a.Graph, t.Graph), owlRuleSameAsSubject, about, record)
	}
	for _, about := range e.byObject[engineTermKey(t.Subject)] {
		a := about.Triple
		if a.Predicate.Value == owlSameAsIRI {
			continue
		}
		e.derive(a.Subject, a.Predicate, t.Object, preferInferenceGraph(a.Graph, t.Graph), owlRuleSameAsObject, about, record)
	}
}

// replaceBySameAs applies the sameAs replacement rules with the record as the
// statement being copied. Looking only at sameAs edges leaving the term is
// enough, because the closure puts an edge in each direction.
func (e *inferenceEngine) replaceBySameAs(record *rdfsInferenceRecord) {
	t := record.Triple
	for _, same := range e.withSubject(keyOWLSameAs, t.Subject) {
		s := same.Triple
		if !e.sameAs.materializes(s.Subject, s.Object) {
			continue
		}
		e.derive(s.Object, t.Predicate, t.Object, preferInferenceGraph(t.Graph, s.Graph), owlRuleSameAsSubject, record, same)
	}
	if !isResourceTerm(t.Object) {
		return
	}
	for _, same := range e.withSubject(keyOWLSameAs, t.Object) {
		s := same.Triple
		if !e.sameAs.materializes(s.Subject, s.Object) {
			continue
		}
		e.derive(t.Subject, t.Predicate, s.Object, preferInferenceGraph(t.Graph, s.Graph), owlRuleSameAsObject, record, same)
	}
}

func isResourceTerm(term RDFTerm) bool {
	return term.Kind == RDFTermIRI || term.Kind == RDFTermBlankNode
}

// sameAsClasses tracks owl:sameAs equivalence classes as a union-find over
// every sameAs edge the engine has indexed, explicit or inferred, so that the
// size of a class is known before anything is copied across it.
type sameAsClasses struct {
	limit     int
	parent    map[string]string
	members   map[string][]RDFTerm
	oversized map[string]bool
	// marked are terms a previous run found in an oversized class. They are
	// oversized from the moment they are seen, so this run never starts
	// materializing the class it would otherwise only discover later.
	marked map[string]bool
	// suspended are terms a previous run found in a class that an
	// owl:differentFrom statement contradicts. No sameAs edge touching one
	// is materialized; see computeInferenceOutcome.
	suspended map[string]bool
}

func newSameAsClasses(limit int, marked map[string]bool) *sameAsClasses {
	return &sameAsClasses{
		limit:     limit,
		parent:    make(map[string]string),
		members:   make(map[string][]RDFTerm),
		oversized: make(map[string]bool),
		marked:    marked,
	}
}

func (c *sameAsClasses) find(key string) string {
	for c.parent[key] != key {
		c.parent[key] = c.parent[c.parent[key]]
		key = c.parent[key]
	}
	return key
}

func (c *sameAsClasses) add(term RDFTerm) string {
	key := engineTermKey(term)
	if _, ok := c.parent[key]; ok {
		return c.find(key)
	}
	c.parent[key] = key
	c.members[key] = []RDFTerm{term}
	if c.marked[key] {
		c.oversized[key] = true
	}
	return key
}

// observe records one sameAs edge. It returns the members of the class when
// the edge makes a class oversized after rules have already fired, which is
// the engine's signal to restart with those members marked.
//
// A restart is needed only if part of the newly oversized class may already
// have been materialized: a side that was under the cap and already had a
// sameAs edge of its own. A term seen for the first time in this edge has had
// no sameAs rule fire for it, so joining it to an oversized class costs
// nothing.
func (c *sameAsClasses) observe(subject, object RDFTerm, fired bool) []string {
	if !isResourceTerm(subject) || !isResourceTerm(object) || termsEqual(subject, object) {
		return nil
	}
	_, seenSubject := c.parent[engineTermKey(subject)]
	_, seenObject := c.parent[engineTermKey(object)]
	a, b := c.add(subject), c.add(object)
	if a == b {
		return nil
	}
	materializedA := seenSubject && !c.oversized[a]
	materializedB := seenObject && !c.oversized[b]
	if len(c.members[a]) < len(c.members[b]) {
		a, b = b, a
	}
	c.parent[b] = a
	c.members[a] = append(c.members[a], c.members[b]...)
	delete(c.members, b)
	c.oversized[a] = c.oversized[a] || c.oversized[b] || len(c.members[a]) > c.limit
	delete(c.oversized, b)

	if !c.oversized[a] || !fired || !(materializedA || materializedB) {
		return nil
	}
	restart := make([]string, 0, len(c.members[a]))
	for _, member := range c.members[a] {
		restart = append(restart, engineTermKey(member))
	}
	return restart
}

// materializes reports whether sameAs rules may use the edge subject sameAs
// object: both ends individuals, not the same term, neither end suspended by
// a differentFrom conflict, and not in an oversized class.
func (c *sameAsClasses) materializes(subject, object RDFTerm) bool {
	if !isResourceTerm(subject) || !isResourceTerm(object) || termsEqual(subject, object) {
		return false
	}
	key := engineTermKey(subject)
	if c.suspended[key] || c.suspended[engineTermKey(object)] {
		return false
	}
	if _, ok := c.parent[key]; !ok {
		return true
	}
	return !c.oversized[c.find(key)]
}

func (c *sameAsClasses) report() []OversizedSameAsClass {
	var out []OversizedSameAsClass
	for root, isOversized := range c.oversized {
		if !isOversized || c.find(root) != root {
			continue
		}
		names := make([]string, 0, len(c.members[root]))
		for _, member := range c.members[root] {
			names = append(names, member.String())
		}
		sort.Strings(names)
		if len(names) > oversizedSameAsSampleSize {
			names = names[:oversizedSameAsSampleSize]
		}
		out = append(out, OversizedSameAsClass{Size: len(c.members[root]), Cap: c.limit, Members: names})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Members[0] < out[j].Members[0]
	})
	return out
}
