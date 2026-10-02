package graph

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	rdfTypeIRI              = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	rdfPropertyIRI          = "http://www.w3.org/1999/02/22-rdf-syntax-ns#Property"
	rdfsSubClassOfIRI       = "http://www.w3.org/2000/01/rdf-schema#subClassOf"
	rdfsSubPropertyOfIRI    = "http://www.w3.org/2000/01/rdf-schema#subPropertyOf"
	rdfsClassIRI            = "http://www.w3.org/2000/01/rdf-schema#Class"
	rdfsDomainIRI           = "http://www.w3.org/2000/01/rdf-schema#domain"
	rdfsRangeIRI            = "http://www.w3.org/2000/01/rdf-schema#range"
	rdfsRuleSubClass        = "rdfs_subclass_transitive"
	rdfsRuleSubProperty     = "rdfs_subproperty_transitive"
	rdfsRuleTypeSubClass    = "rdfs_type_via_subclass"
	rdfsRuleDomain          = "rdfs_domain"
	rdfsRuleRange           = "rdfs_range"
	rdfsRuleSubPropertyUse  = "rdfs_subproperty_application"
	rdfsRuleSubclassClass   = "rdfs_subclass_declares_class"
	rdfsRuleSubpropProperty = "rdfs_subproperty_declares_property"
	rdfsRuleDomainSchema    = "rdfs_domain_declares_schema"
	rdfsRuleRangeSchema     = "rdfs_range_declares_schema"
	rdfsRuleClassReflexive  = "rdfs_class_reflexive"
	rdfsRulePropReflexive   = "rdfs_property_reflexive"
)

// RDFSInferenceRefreshResult summarizes a refresh of inferred triples.
type RDFSInferenceRefreshResult struct {
	ExplicitCount         int  `json:"explicit_count"`
	InferredCount         int  `json:"inferred_count"`
	Incremental           bool `json:"incremental,omitempty"`
	AffectedExplicitCount int  `json:"affected_explicit_count,omitempty"`
	RemovedInferredCount  int  `json:"removed_inferred_count,omitempty"`
	// OversizedSameAsClasses lists the owl:sameAs equivalence classes that
	// were larger than the configured cap and so were reported instead of
	// materialized. Nothing was inferred from their sameAs edges: not the
	// closure, and not the copied statements. An incremental refresh reports
	// only the classes inside the neighbourhood it recomputed.
	OversizedSameAsClasses []OversizedSameAsClass `json:"oversized_same_as_classes,omitempty"`
}

// InferenceOptions tunes a refresh. The zero value is the default behaviour.
type InferenceOptions struct {
	// MaxSameAsClassSize is the largest owl:sameAs equivalence class that is
	// materialized. Zero or less means DefaultMaxSameAsClassSize. See
	// OversizedSameAsClass for what happens to a class above it.
	MaxSameAsClassSize int `json:"max_same_as_class_size,omitempty"`
}

func (o InferenceOptions) sameAsClassCap() int {
	if o.MaxSameAsClassSize <= 0 {
		return DefaultMaxSameAsClassSize
	}
	return o.MaxSameAsClassSize
}

// RDFSInferenceSummary provides persisted inference counts and rule breakdowns.
type RDFSInferenceSummary struct {
	ExplicitCount int            `json:"explicit_count"`
	InferredCount int            `json:"inferred_count"`
	Rules         map[string]int `json:"rules,omitempty"`
}

// RDFSInferenceExplanation returns provenance information for one triple.
type RDFSInferenceExplanation struct {
	Triple           RDFTriple `json:"triple"`
	Explicit         bool      `json:"explicit"`
	Rule             string    `json:"rule,omitempty"`
	SupportTripleIDs []string  `json:"support_triple_ids,omitempty"`
}

// RDFSInferenceTraceEntry is one flattened node in an explanation trace.
type RDFSInferenceTraceEntry struct {
	TripleID       string                   `json:"triple_id"`
	ParentTripleID string                   `json:"parent_triple_id,omitempty"`
	Depth          int                      `json:"depth"`
	Explanation    RDFSInferenceExplanation `json:"explanation"`
	Truncated      bool                     `json:"truncated,omitempty"`
}

// RDFSInferenceMatchExplanation combines explanation and optional trace for one matched triple.
type RDFSInferenceMatchExplanation struct {
	Explanation RDFSInferenceExplanation  `json:"explanation"`
	Trace       []RDFSInferenceTraceEntry `json:"trace,omitempty"`
}

type rdfsInferenceRecord struct {
	Triple     RDFTriple
	Explicit   bool
	Rule       string
	SupportIDs []string
	// key is the record's content key in the engine; see inferenceContentKey.
	key string
}

// RefreshRDFSInferences recomputes and persists inferred triples using an
// RDFS-lite ruleset and the OWL subset in owl.go, with default options.
func (g *GraphStore) RefreshRDFSInferences(ctx context.Context) (*RDFSInferenceRefreshResult, error) {
	return g.RefreshRDFSInferencesWithOptions(ctx, InferenceOptions{})
}

// RefreshRDFSInferencesWithOptions is RefreshRDFSInferences with the options
// spelled out.
func (g *GraphStore) RefreshRDFSInferencesWithOptions(ctx context.Context, opts InferenceOptions) (*RDFSInferenceRefreshResult, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	if err := g.clearInferredTriples(ctx); err != nil {
		return nil, err
	}

	explicitOnly := false
	explicitTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &explicitOnly})
	if err != nil {
		return nil, err
	}
	records, oversized := computeInferenceRecords(explicitTriples, opts)
	inferredCount, err := g.persistInferredRecords(ctx, records)
	if err != nil {
		return nil, err
	}

	return &RDFSInferenceRefreshResult{
		ExplicitCount:          len(explicitTriples),
		InferredCount:          inferredCount,
		AffectedExplicitCount:  len(explicitTriples),
		OversizedSameAsClasses: oversized,
	}, nil
}

// RefreshRDFSInferencesIncremental recomputes inferred triples only for the neighborhood
// affected by the supplied changed explicit triples.
func (g *GraphStore) RefreshRDFSInferencesIncremental(ctx context.Context, changedTriples []RDFTriple) (*RDFSInferenceRefreshResult, error) {
	return g.RefreshRDFSInferencesIncrementalWithOptions(ctx, changedTriples, InferenceOptions{})
}

// RefreshRDFSInferencesIncrementalWithOptions is RefreshRDFSInferencesIncremental
// with the options spelled out.
func (g *GraphStore) RefreshRDFSInferencesIncrementalWithOptions(ctx context.Context, changedTriples []RDFTriple, opts InferenceOptions) (*RDFSInferenceRefreshResult, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	if len(changedTriples) == 0 {
		return g.RefreshRDFSInferencesWithOptions(ctx, opts)
	}

	explicitOnly := false
	explicitTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &explicitOnly})
	if err != nil {
		return nil, err
	}
	inferredOnly := true
	inferredTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &inferredOnly})
	if err != nil {
		return nil, err
	}

	normalizedSeeds, err := g.normalizeInferenceSeedTriples(ctx, changedTriples)
	if err != nil {
		return nil, err
	}
	affectedExplicit := expandRDFSExplicitNeighborhood(explicitTriples, normalizedSeeds)
	impactedInferredIDs := collectImpactedInferredTripleIDs(inferredTriples, normalizedSeeds, affectedExplicit)

	removed, err := g.deleteInferredTriplesByID(ctx, impactedInferredIDs)
	if err != nil {
		return nil, err
	}

	records, oversized := computeInferenceRecords(affectedExplicit, opts)
	inferredCount, err := g.persistInferredRecords(ctx, records)
	if err != nil {
		return nil, err
	}

	return &RDFSInferenceRefreshResult{
		ExplicitCount:          len(explicitTriples),
		InferredCount:          inferredCount,
		Incremental:            true,
		AffectedExplicitCount:  len(affectedExplicit),
		RemovedInferredCount:   removed,
		OversizedSameAsClasses: oversized,
	}, nil
}

// ExplainTriple returns whether a triple is explicit or inferred and its immediate provenance.
func (g *GraphStore) ExplainTriple(ctx context.Context, tripleID string) (*RDFSInferenceExplanation, error) {
	triple, err := g.GetTriple(ctx, tripleID)
	if err != nil {
		return nil, err
	}
	return &RDFSInferenceExplanation{
		Triple:           *triple,
		Explicit:         !triple.Inferred,
		Rule:             triple.Rule,
		SupportTripleIDs: append([]string(nil), triple.SupportIDs...),
	}, nil
}

// InferenceSummary returns explicit/inferred counts and an inference-rule breakdown.
func (g *GraphStore) InferenceSummary(ctx context.Context) (*RDFSInferenceSummary, error) {
	explicitOnly := false
	explicitTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &explicitOnly})
	if err != nil {
		return nil, err
	}
	inferredOnly := true
	inferredTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &inferredOnly})
	if err != nil {
		return nil, err
	}
	rules := make(map[string]int)
	for _, triple := range inferredTriples {
		if strings.TrimSpace(triple.Rule) == "" {
			continue
		}
		rules[triple.Rule]++
	}
	return &RDFSInferenceSummary{
		ExplicitCount: len(explicitTriples),
		InferredCount: len(inferredTriples),
		Rules:         rules,
	}, nil
}

// ExplainTriplesByPattern expands explanations for all triples matched by the given pattern.
func (g *GraphStore) ExplainTriplesByPattern(ctx context.Context, pattern TriplePattern, depth int) ([]RDFSInferenceMatchExplanation, error) {
	triples, err := g.FindTriples(ctx, pattern)
	if err != nil {
		return nil, err
	}
	out := make([]RDFSInferenceMatchExplanation, 0, len(triples))
	for _, triple := range triples {
		explanation, err := g.ExplainTriple(ctx, triple.ID)
		if err != nil {
			return nil, err
		}
		item := RDFSInferenceMatchExplanation{Explanation: *explanation}
		if depth > 0 {
			trace, err := g.ExplainTripleTrace(ctx, triple.ID, depth)
			if err != nil {
				return nil, err
			}
			item.Trace = trace
		}
		out = append(out, item)
	}
	return out, nil
}

// ExplainTripleTrace recursively expands provenance for a triple into a flattened trace list.
func (g *GraphStore) ExplainTripleTrace(ctx context.Context, tripleID string, depth int) ([]RDFSInferenceTraceEntry, error) {
	if depth < 0 {
		depth = 0
	}
	seen := make(map[string]bool)
	entries := make([]RDFSInferenceTraceEntry, 0)
	if err := g.explainTripleTrace(ctx, tripleID, "", 0, depth, seen, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (g *GraphStore) explainTripleTrace(ctx context.Context, tripleID, parentTripleID string, currentDepth, remainingDepth int, seen map[string]bool, entries *[]RDFSInferenceTraceEntry) error {
	explanation, err := g.ExplainTriple(ctx, tripleID)
	if err != nil {
		return err
	}
	entry := RDFSInferenceTraceEntry{
		TripleID:       tripleID,
		ParentTripleID: parentTripleID,
		Depth:          currentDepth,
		Explanation:    *explanation,
	}
	*entries = append(*entries, entry)
	if explanation.Explicit {
		return nil
	}
	if remainingDepth == 0 {
		if len(explanation.SupportTripleIDs) > 0 {
			(*entries)[len(*entries)-1].Truncated = true
		}
		return nil
	}
	if seen[tripleID] {
		(*entries)[len(*entries)-1].Truncated = true
		return nil
	}
	seen[tripleID] = true
	defer delete(seen, tripleID)

	for _, supportID := range explanation.SupportTripleIDs {
		if err := g.explainTripleTrace(ctx, supportID, tripleID, currentDepth+1, remainingDepth-1, seen, entries); err != nil {
			return err
		}
	}
	return nil
}

// computeRDFSInferenceRecords runs the ruleset with default options. It is the
// form the benchmarks and the fixture tests call, because they judge the
// engine and not its configuration.
func computeRDFSInferenceRecords(explicitTriples []RDFTriple) map[string]rdfsInferenceRecord {
	records, _ := computeInferenceRecords(explicitTriples, InferenceOptions{})
	return records
}

// computeInferenceRecords materializes everything the RDFS and OWL rules
// derive from the explicit triples, and reports the sameAs classes it refused
// to materialize.
//
// The outer loop exists only for sameAs. Whether a class is too large to
// materialize is not always known before inference starts: a sameAs edge can
// itself be derived — through subPropertyOf, inverseOf, or equivalentProperty —
// and can join two classes that were each under the cap and have each already
// been materialized. Stopping there would leave half a class's copies in the
// output, which is the silent truncation the cap exists to prevent. So the
// engine gives up on that run, and the next run starts knowing the class is
// oversized and never touches it. Every restart marks at least one term that
// was not marked before, so the loop is bounded by the number of terms; in a
// graph with no sameAs edges it runs exactly once.
func computeInferenceRecords(explicitTriples []RDFTriple, opts InferenceOptions) (map[string]rdfsInferenceRecord, []OversizedSameAsClass) {
	oversized := make(map[string]bool)
	for {
		engine := newInferenceEngine(opts.sameAsClassCap(), oversized)
		restart := engine.run(explicitTriples)
		if restart == nil {
			out := make(map[string]rdfsInferenceRecord, len(engine.records))
			for key, record := range engine.records {
				out[key] = *record
			}
			return out, engine.sameAs.report()
		}
		for _, key := range restart {
			oversized[key] = true
		}
	}
}

// inferenceEngine evaluates the ruleset semi-naively.
//
// The engine it replaces re-joined every pair of records in every round: each
// round cost the square of everything known so far, and most of that work
// rediscovered derivations earlier rounds had already made. That was harmless
// while the input was a few hand-written ontology triples. It stopped being
// harmless when the property graph began to be projected into the triple
// store, because tens of thousands of typed instances and edges turned each
// round into billions of comparisons.
//
// Semi-naive evaluation rests on one observation: a derivation that is new in
// round k must use at least one premise that was new in round k-1, since if
// every premise had been known earlier the derivation would have been made
// earlier. So each round joins only the delta — the records the previous round
// produced — against everything known, through indexes keyed by predicate and
// by term, and the fixpoint is reached when a round produces nothing. The
// result is the same least fixpoint the naive loop reached; only the work
// spent reaching it differs.
//
// Records enter the indexes at the start of the round that processes them, so
// a join made while processing the delta always sees the delta itself as part
// of "everything known", which is what lets two premises that arrived in the
// same round find each other. Records derived during a round wait in next and
// are indexed at the start of the following one.
type inferenceEngine struct {
	records map[string]*rdfsInferenceRecord

	byPredicate        map[string][]*rdfsInferenceRecord
	byPredicateSubject map[string][]*rdfsInferenceRecord
	byPredicateObject  map[string][]*rdfsInferenceRecord
	bySubject          map[string][]*rdfsInferenceRecord
	byObject           map[string][]*rdfsInferenceRecord

	next []*rdfsInferenceRecord
	// fired is set once any rule has run. Before that, discovering an
	// oversized sameAs class costs nothing; after it, the class may already
	// have been partly materialized and the run must restart.
	fired  bool
	sameAs *sameAsClasses
}

// Index keys of the vocabulary the rules join on, computed once rather than
// for every lookup.
var (
	keyRDFType        = engineTermKey(NewIRI(rdfTypeIRI))
	keySubClassOf     = engineTermKey(NewIRI(rdfsSubClassOfIRI))
	keySubPropertyOf  = engineTermKey(NewIRI(rdfsSubPropertyOfIRI))
	keyDomain         = engineTermKey(NewIRI(rdfsDomainIRI))
	keyRange          = engineTermKey(NewIRI(rdfsRangeIRI))
	keyOWLInverseOf   = engineTermKey(NewIRI(owlInverseOfIRI))
	keyOWLSameAs      = engineTermKey(NewIRI(owlSameAsIRI))
	termRDFType       = NewIRI(rdfTypeIRI)
	termSubClassOf    = NewIRI(rdfsSubClassOfIRI)
	termSubPropertyOf = NewIRI(rdfsSubPropertyOfIRI)
	termRDFSClass     = NewIRI(rdfsClassIRI)
	termRDFProperty   = NewIRI(rdfPropertyIRI)
)

func newInferenceEngine(sameAsCap int, oversized map[string]bool) *inferenceEngine {
	return &inferenceEngine{
		records:            make(map[string]*rdfsInferenceRecord),
		byPredicate:        make(map[string][]*rdfsInferenceRecord),
		byPredicateSubject: make(map[string][]*rdfsInferenceRecord),
		byPredicateObject:  make(map[string][]*rdfsInferenceRecord),
		bySubject:          make(map[string][]*rdfsInferenceRecord),
		byObject:           make(map[string][]*rdfsInferenceRecord),
		sameAs:             newSameAsClasses(sameAsCap, oversized),
	}
}

// run evaluates to the fixpoint, or stops early and returns the members of a
// sameAs class that became oversized after rules had already fired.
func (e *inferenceEngine) run(explicitTriples []RDFTriple) []string {
	// Explicit input is ordered by content before anything else happens, so
	// the output — including which rule is credited for a triple that can be
	// derived two ways — does not depend on the order the store returned rows
	// in. That is also what lets an incremental refresh of one neighbourhood
	// reproduce what a full refresh wrote for it.
	explicit := make([]*rdfsInferenceRecord, 0, len(explicitTriples))
	for _, triple := range explicitTriples {
		triple = tripleWithoutInference(triple)
		explicit = append(explicit, &rdfsInferenceRecord{Triple: triple, Explicit: true, key: inferenceContentKey(triple)})
	}
	sort.SliceStable(explicit, func(i, j int) bool {
		if explicit[i].key != explicit[j].key {
			return explicit[i].key < explicit[j].key
		}
		return explicit[i].Triple.ID < explicit[j].Triple.ID
	})

	// Records are keyed by content, not by id. An explicit triple that came
	// with an id of its own must still stop the engine from inferring the same
	// statement a second time, and two explicit rows that say the same thing
	// are one fact to the rules.
	delta := make([]*rdfsInferenceRecord, 0, len(explicit))
	for _, record := range explicit {
		if _, ok := e.records[record.key]; ok {
			continue
		}
		e.records[record.key] = record
		delta = append(delta, record)
	}

	for len(delta) > 0 {
		for _, record := range delta {
			if restart := e.index(record); restart != nil {
				return restart
			}
		}
		for _, record := range delta {
			e.fire(record)
		}
		e.fired = true
		delta, e.next = e.next, nil
	}
	return nil
}

func (e *inferenceEngine) index(record *rdfsInferenceRecord) []string {
	t := record.Triple
	predicate := engineTermKey(t.Predicate)
	subject := engineTermKey(t.Subject)
	object := engineTermKey(t.Object)
	e.byPredicate[predicate] = append(e.byPredicate[predicate], record)
	e.byPredicateSubject[predicate+"\x01"+subject] = append(e.byPredicateSubject[predicate+"\x01"+subject], record)
	e.byPredicateObject[predicate+"\x01"+object] = append(e.byPredicateObject[predicate+"\x01"+object], record)
	e.bySubject[subject] = append(e.bySubject[subject], record)
	e.byObject[object] = append(e.byObject[object], record)
	if t.Predicate.Value == owlSameAsIRI {
		return e.sameAs.observe(t.Subject, t.Object, e.fired)
	}
	return nil
}

// withSubject returns the known records with this predicate and subject.
func (e *inferenceEngine) withSubject(predicateKey string, subject RDFTerm) []*rdfsInferenceRecord {
	return e.byPredicateSubject[predicateKey+"\x01"+engineTermKey(subject)]
}

// withObject returns the known records with this predicate and object.
func (e *inferenceEngine) withObject(predicateKey string, object RDFTerm) []*rdfsInferenceRecord {
	return e.byPredicateObject[predicateKey+"\x01"+engineTermKey(object)]
}

// usingPredicate returns the known records whose predicate is this term.
func (e *inferenceEngine) usingPredicate(predicate RDFTerm) []*rdfsInferenceRecord {
	return e.byPredicate[engineTermKey(predicate)]
}

// derive adds one inferred triple unless it is already known. The first
// derivation of a triple is the one it is credited to.
//
// An inferred record is given the id the store will give it, and that id is
// what goes into the support list of anything derived from it. The engine
// before this one left inferred records without ids, so a triple derived from
// another inferred triple listed only its explicit premises, and a trace could
// not walk from a two-step inference back to the facts it rested on.
func (e *inferenceEngine) derive(subject, predicate, object RDFTerm, graph *RDFTerm, rule string, supports ...*rdfsInferenceRecord) {
	// RDF 1.2 allows a triple term only as an object; a rule that would move
	// one into subject or predicate position (range, symmetry, inverse) has
	// nothing to conclude.
	if subject.Kind == RDFTermTriple || predicate.Kind == RDFTermTriple {
		return
	}
	triple := RDFTriple{Subject: subject, Predicate: predicate, Object: object, Graph: graph}
	key := inferenceContentKey(triple)
	if _, ok := e.records[key]; ok {
		return
	}
	triple.Graph = cloneGraphTerm(graph)
	triple.ID = tripleDigest(triple)
	ids := make([]string, 0, len(supports))
	for _, support := range supports {
		ids = append(ids, support.Triple.ID)
	}
	record := &rdfsInferenceRecord{Triple: triple, Rule: rule, SupportIDs: uniqueSortedStrings(ids), key: key}
	e.records[key] = record
	e.next = append(e.next, record)
}

// fire applies every rule in which the record can be a premise, joining it
// against everything known. A record is tried in every premise position it
// can occupy, because semi-naive evaluation only finds a derivation through
// whichever of its premises arrived last.
func (e *inferenceEngine) fire(record *rdfsInferenceRecord) {
	t := record.Triple

	switch t.Predicate.Value {
	case rdfsSubClassOfIRI:
		e.derive(t.Subject, termRDFType, termRDFSClass, t.Graph, rdfsRuleSubclassClass, record)
		e.derive(t.Object, termRDFType, termRDFSClass, t.Graph, rdfsRuleSubclassClass, record)
		for _, next := range e.withSubject(keySubClassOf, t.Object) {
			e.derive(t.Subject, termSubClassOf, next.Triple.Object, mergeInferenceGraph(t.Graph, next.Triple.Graph), rdfsRuleSubClass, record, next)
		}
		for _, prev := range e.withObject(keySubClassOf, t.Subject) {
			e.derive(prev.Triple.Subject, termSubClassOf, t.Object, mergeInferenceGraph(prev.Triple.Graph, t.Graph), rdfsRuleSubClass, prev, record)
		}
		for _, instance := range e.withObject(keyRDFType, t.Subject) {
			e.derive(instance.Triple.Subject, termRDFType, t.Object, preferInferenceGraph(instance.Triple.Graph, t.Graph), rdfsRuleTypeSubClass, instance, record)
		}
	case rdfsSubPropertyOfIRI:
		e.derive(t.Subject, termRDFType, termRDFProperty, t.Graph, rdfsRuleSubpropProperty, record)
		e.derive(t.Object, termRDFType, termRDFProperty, t.Graph, rdfsRuleSubpropProperty, record)
		for _, next := range e.withSubject(keySubPropertyOf, t.Object) {
			e.derive(t.Subject, termSubPropertyOf, next.Triple.Object, mergeInferenceGraph(t.Graph, next.Triple.Graph), rdfsRuleSubProperty, record, next)
		}
		for _, prev := range e.withObject(keySubPropertyOf, t.Subject) {
			e.derive(prev.Triple.Subject, termSubPropertyOf, t.Object, mergeInferenceGraph(prev.Triple.Graph, t.Graph), rdfsRuleSubProperty, prev, record)
		}
		for _, use := range e.usingPredicate(t.Subject) {
			e.derive(use.Triple.Subject, t.Object, use.Triple.Object, preferInferenceGraph(use.Triple.Graph, t.Graph), rdfsRuleSubPropertyUse, use, record)
		}
	case rdfsDomainIRI:
		e.derive(t.Subject, termRDFType, termRDFProperty, t.Graph, rdfsRuleDomainSchema, record)
		e.derive(t.Object, termRDFType, termRDFSClass, t.Graph, rdfsRuleDomainSchema, record)
		for _, use := range e.usingPredicate(t.Subject) {
			e.derive(use.Triple.Subject, termRDFType, t.Object, preferInferenceGraph(use.Triple.Graph, t.Graph), rdfsRuleDomain, use, record)
		}
	case rdfsRangeIRI:
		e.derive(t.Subject, termRDFType, termRDFProperty, t.Graph, rdfsRuleRangeSchema, record)
		e.derive(t.Object, termRDFType, termRDFSClass, t.Graph, rdfsRuleRangeSchema, record)
		for _, use := range e.usingPredicate(t.Subject) {
			if use.Triple.Object.Kind == RDFTermLiteral {
				continue
			}
			e.derive(use.Triple.Object, termRDFType, t.Object, preferInferenceGraph(use.Triple.Graph, t.Graph), rdfsRuleRange, use, record)
		}
	case rdfTypeIRI:
		if t.Object.Kind == RDFTermIRI && t.Object.Value == rdfsClassIRI {
			e.derive(t.Subject, termSubClassOf, t.Subject, t.Graph, rdfsRuleClassReflexive, record)
		}
		if t.Object.Kind == RDFTermIRI && t.Object.Value == rdfPropertyIRI {
			e.derive(t.Subject, termSubPropertyOf, t.Subject, t.Graph, rdfsRulePropReflexive, record)
		}
		for _, schema := range e.withSubject(keySubClassOf, t.Object) {
			e.derive(t.Subject, termRDFType, schema.Triple.Object, preferInferenceGraph(t.Graph, schema.Triple.Graph), rdfsRuleTypeSubClass, record, schema)
		}
	}

	// Every record, schema triples included, is also a use of its predicate,
	// so the property-schema rules see it in the data position.
	predicate := engineTermKey(t.Predicate)
	for _, schema := range e.byPredicateSubject[keySubPropertyOf+"\x01"+predicate] {
		e.derive(t.Subject, schema.Triple.Object, t.Object, preferInferenceGraph(t.Graph, schema.Triple.Graph), rdfsRuleSubPropertyUse, record, schema)
	}
	for _, schema := range e.byPredicateSubject[keyDomain+"\x01"+predicate] {
		e.derive(t.Subject, termRDFType, schema.Triple.Object, preferInferenceGraph(t.Graph, schema.Triple.Graph), rdfsRuleDomain, record, schema)
	}
	if t.Object.Kind != RDFTermLiteral {
		for _, schema := range e.byPredicateSubject[keyRange+"\x01"+predicate] {
			e.derive(t.Object, termRDFType, schema.Triple.Object, preferInferenceGraph(t.Graph, schema.Triple.Graph), rdfsRuleRange, record, schema)
		}
	}

	e.fireOWL(record)
}

// engineTermKey identifies a term the way termsEqual compares terms. The
// separator is a byte no IRI or sensible literal contains; inferenceTermKey's
// "|" is not, and a literal "a|b" would share its key with a literal "a" typed
// "b" — harmless for the neighbourhood heuristic that uses it, wrong for an
// index that decides which triples join.
func engineTermKey(term RDFTerm) string {
	return term.Kind + "\x00" + term.Value + "\x00" + term.Datatype + "\x00" + term.Language
}

// inferenceContentKey identifies a statement by what it says — subject,
// predicate, object and graph — without hashing, since the engine computes one
// for every derivation it attempts and most attempts are duplicates.
func inferenceContentKey(triple RDFTriple) string {
	graph := ""
	if triple.Graph != nil {
		graph = engineTermKey(*triple.Graph)
	}
	return engineTermKey(triple.Subject) + "\x01" + engineTermKey(triple.Predicate) + "\x01" + engineTermKey(triple.Object) + "\x01" + graph
}

func (g *GraphStore) persistInferredRecords(ctx context.Context, records map[string]rdfsInferenceRecord) (int, error) {
	inferredTriples := make([]*RDFTriple, 0, len(records))
	for _, record := range records {
		if record.Explicit {
			continue
		}
		inferredTriple := record.Triple
		inferredTriple.Inferred = true
		inferredTriple.Rule = record.Rule
		inferredTriple.SupportIDs = append([]string(nil), record.SupportIDs...)
		tripleCopy := inferredTriple
		inferredTriples = append(inferredTriples, &tripleCopy)
	}
	if len(inferredTriples) == 0 {
		return 0, nil
	}

	result, err := g.UpsertTriplesBatch(ctx, inferredTriples)
	if err != nil {
		return 0, err
	}
	if result.FailedCount > 0 {
		if len(result.Errors) > 0 {
			return result.SuccessCount, result.Errors[0]
		}
		return result.SuccessCount, fmt.Errorf("failed to persist %d inferred triples", result.FailedCount)
	}
	return result.SuccessCount, nil
}

func (g *GraphStore) normalizeInferenceSeedTriples(ctx context.Context, triples []RDFTriple) ([]RDFTriple, error) {
	out := make([]RDFTriple, 0, len(triples))
	for _, triple := range triples {
		normalized, err := g.normalizeTriple(ctx, tripleWithoutInference(triple))
		if err != nil {
			return nil, err
		}
		if normalized.ID == "" {
			normalized.ID = tripleDigest(normalized)
		}
		out = append(out, normalized)
	}
	return out, nil
}

func expandRDFSExplicitNeighborhood(explicitTriples, seeds []RDFTriple) []RDFTriple {
	if len(seeds) == 0 || len(explicitTriples) == 0 {
		return nil
	}
	impactedTerms := make(map[string]struct{})
	for _, triple := range seeds {
		addInferenceNeighborhoodSeedTerms(impactedTerms, triple)
	}

	selected := make(map[string]RDFTriple)
	changed := true
	for changed {
		changed = false
		for _, triple := range explicitTriples {
			key := tripleKey(triple)
			if _, ok := selected[key]; ok {
				continue
			}
			if !tripleTouchesInferenceNeighborhood(triple, impactedTerms) {
				continue
			}
			selected[key] = triple
			if addInferenceNeighborhoodTerms(impactedTerms, triple) {
				changed = true
			}
		}
	}

	out := make([]RDFTriple, 0, len(selected))
	for _, triple := range selected {
		out = append(out, triple)
	}
	sort.Slice(out, func(i, j int) bool {
		return tripleKey(out[i]) < tripleKey(out[j])
	})
	return out
}

func collectImpactedInferredTripleIDs(inferredTriples, seeds, affectedExplicit []RDFTriple) []string {
	if len(inferredTriples) == 0 || (len(seeds) == 0 && len(affectedExplicit) == 0) {
		return nil
	}
	reverse := make(map[string][]string)
	for _, triple := range inferredTriples {
		for _, supportID := range triple.SupportIDs {
			reverse[supportID] = append(reverse[supportID], triple.ID)
		}
	}

	seen := make(map[string]struct{})
	queue := make([]string, 0, len(seeds))
	for _, triple := range seeds {
		if triple.ID == "" {
			continue
		}
		queue = append(queue, triple.ID)
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dependentID := range reverse[current] {
			if _, ok := seen[dependentID]; ok {
				continue
			}
			seen[dependentID] = struct{}{}
			queue = append(queue, dependentID)
		}
	}

	impactedTerms := make(map[string]struct{})
	for _, triple := range seeds {
		addInferenceNeighborhoodSeedTerms(impactedTerms, triple)
	}
	for _, triple := range affectedExplicit {
		addInferenceNeighborhoodTerms(impactedTerms, triple)
	}
	for _, triple := range inferredTriples {
		if !tripleTouchesInferenceNeighborhood(triple, impactedTerms) {
			continue
		}
		seen[triple.ID] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func addInferenceNeighborhoodSeedTerms(target map[string]struct{}, triple RDFTriple) {
	addInferenceTerm(target, triple.Subject)
	addInferenceTerm(target, triple.Object)
	if triple.Graph != nil {
		addInferenceTerm(target, *triple.Graph)
	}
	if !isStructuralInferencePredicate(triple.Predicate.Value) {
		addInferenceTerm(target, triple.Predicate)
	}
}

func addInferenceNeighborhoodTerms(target map[string]struct{}, triple RDFTriple) bool {
	before := len(target)
	addInferenceTerm(target, triple.Subject)
	addInferenceTerm(target, triple.Object)
	if triple.Graph != nil {
		addInferenceTerm(target, *triple.Graph)
	}
	if !isStructuralInferencePredicate(triple.Predicate.Value) {
		addInferenceTerm(target, triple.Predicate)
	}
	return len(target) != before
}

func addInferenceTerm(target map[string]struct{}, term RDFTerm) {
	target[inferenceTermKey(term)] = struct{}{}
}

func tripleTouchesInferenceNeighborhood(triple RDFTriple, impactedTerms map[string]struct{}) bool {
	if _, ok := impactedTerms[inferenceTermKey(triple.Subject)]; ok {
		return true
	}
	if _, ok := impactedTerms[inferenceTermKey(triple.Object)]; ok {
		return true
	}
	if triple.Graph != nil {
		if _, ok := impactedTerms[inferenceTermKey(*triple.Graph)]; ok {
			return true
		}
	}
	if !isStructuralInferencePredicate(triple.Predicate.Value) {
		if _, ok := impactedTerms[inferenceTermKey(triple.Predicate)]; ok {
			return true
		}
	}
	return false
}

func inferenceTermKey(term RDFTerm) string {
	return term.Kind + "|" + term.Value + "|" + term.Datatype + "|" + term.Language
}

// isStructuralInferencePredicate names the vocabulary whose predicate is not
// itself a term of the neighbourhood. The rules join vocabulary statements
// through their subjects and objects, so counting these predicates would only
// tie every sameAs or subClassOf statement in the store into one neighbourhood
// and make each incremental refresh a full one. The cost is a known blind spot:
// a statement that makes one of these predicates the subject of its own schema
// (rdf:type rdfs:subPropertyOf x) is not followed from the triples that use it.
func isStructuralInferencePredicate(value string) bool {
	switch value {
	case rdfTypeIRI, rdfsSubClassOfIRI, rdfsSubPropertyOfIRI, rdfsDomainIRI, rdfsRangeIRI,
		owlSameAsIRI, owlInverseOfIRI, owlEquivalentClassIRI, owlEquivalentPropertyIRI:
		return true
	default:
		return false
	}
}

func (g *GraphStore) deleteInferredTriplesByID(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ids = uniqueSortedStrings(ids)
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin delete inferred transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleted := 0
	for _, chunk := range chunkStrings(ids, 900) {
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")

		result, err := g.txExec(ctx, tx, fmt.Sprintf(`DELETE FROM graph_edges WHERE id IN (%s)`, placeholders), args...)
		if err != nil {
			return deleted, fmt.Errorf("delete inferred graph edge: %w", err)
		}
		if _, err := result.RowsAffected(); err != nil {
			return deleted, fmt.Errorf("count inferred graph edge delete: %w", err)
		}

		result, err = g.txExec(ctx, tx, fmt.Sprintf(`DELETE FROM kg_triples WHERE inferred = 1 AND id IN (%s)`, placeholders), args...)
		if err != nil {
			return deleted, fmt.Errorf("delete inferred triple: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return deleted, fmt.Errorf("count inferred triple delete: %w", err)
		}
		deleted += int(rows)
	}
	if err := tx.Commit(); err != nil {
		return deleted, fmt.Errorf("commit delete inferred transaction: %w", err)
	}
	return deleted, nil
}

func chunkStrings(values []string, size int) [][]string {
	if len(values) == 0 {
		return nil
	}
	if size <= 0 {
		size = len(values)
	}
	chunks := make([][]string, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, values[start:end])
	}
	return chunks
}

func tripleWithoutInference(triple RDFTriple) RDFTriple {
	return RDFTriple{
		ID:        triple.ID,
		Subject:   triple.Subject,
		Predicate: triple.Predicate,
		Object:    triple.Object,
		Graph:     cloneGraphTerm(triple.Graph),
	}
}

func cloneGraphTerm(term *RDFTerm) *RDFTerm {
	if term == nil {
		return nil
	}
	out := *term
	return &out
}

func tripleKey(triple RDFTriple) string {
	clone := tripleWithoutInference(triple)
	if clone.ID != "" {
		return clone.ID
	}
	return tripleDigest(clone)
}

func mergeInferenceGraph(left, right *RDFTerm) *RDFTerm {
	switch {
	case left == nil && right == nil:
		return nil
	case left == nil:
		return cloneGraphTerm(right)
	case right == nil:
		return cloneGraphTerm(left)
	case termsEqual(*left, *right):
		return cloneGraphTerm(left)
	default:
		return nil
	}
}

func preferInferenceGraph(primary, fallback *RDFTerm) *RDFTerm {
	if primary != nil {
		return cloneGraphTerm(primary)
	}
	return cloneGraphTerm(fallback)
}

func (g *GraphStore) clearInferredTriples(ctx context.Context) error {
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clear inferred transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := g.txExec(ctx, tx, `DELETE FROM graph_edges WHERE id IN (SELECT id FROM kg_triples WHERE inferred = 1)`); err != nil {
		return fmt.Errorf("delete inferred graph edges: %w", err)
	}
	if _, err := g.txExec(ctx, tx, `DELETE FROM kg_triples WHERE inferred = 1`); err != nil {
		return fmt.Errorf("delete inferred triples: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clear inferred transaction: %w", err)
	}
	return nil
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
