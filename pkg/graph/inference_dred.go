package graph

// Incremental maintenance of materialized inferences: delete and re-derive.
//
// RefreshRDFSInferences recomputes everything from the explicit facts, which
// is correct and costs the size of the store every time. A write changes a
// handful of facts, and what it can change downstream is usually a handful of
// inferences, so the materialization is kept in memory between writes and
// updated by the change alone, with DRed (Gupta, Mumick & Subrahmanian 1993;
// the scheme RDFox maintains its materialization with):
//
//  1. Overdelete. Everything with at least one derivation that uses a removed
//     fact is provisionally removed, transitively. This over-approximates — a
//     fact may also have a second derivation that never touched the removal —
//     which is why the next step exists.
//  2. Rederive. Each overdeleted fact that still has a derivation from what
//     remains is put back.
//  3. Insert. New facts are joined semi-naively against everything known, as
//     the full engine would.
//
// Counting (keeping, per fact, the number of derivations) was the other
// candidate and is wrong here: with recursive rules — transitive properties,
// subClassOf chains, sameAs — a cycle supports itself, so a fact's count never
// reaches zero when its last external support goes, and the cycle survives the
// deletion of everything that ever justified it.
//
// # Why the engine's own rules suffice
//
// Overdeletion asks "what did this fact help derive?". The semi-naive engine
// already answers that question for insertion: fire tries a record in every
// premise position of every rule and joins it against everything known (see
// inferenceEngine), so firing a removed fact against the old state enumerates
// exactly the derivations it took part in. The engine's collect hook turns
// those derivations into a report instead of new records. Nothing here knows a
// single rule, so a rule added to rdfs.go or owl.go is maintained correctly
// without touching this file.
//
// Rederivation needs the converse — "can this fact still be derived?" — and
// the engine has no backward rules. It uses a property every rule has instead:
// each conclusion's subject also appears in one of its premises, in some
// position (a rule cannot invent the individual a conclusion is about). So for
// every overdeleted fact, firing the remaining facts that mention its subject
// re-finds any derivation it still has. TestRulesKeepTheirSubjectInAPremise
// checks that property against the engine's own output, so a rule that broke
// it would fail a test rather than silently leave a fact deleted.
//
// # Where it gives up
//
// owl:sameAs is not maintained incrementally. Its equivalence classes are a
// union-find, which cannot split, and the class-size cap makes sameAs
// non-monotone: one more sameAs edge can push a class over the cap and so
// remove inferences rather than add them. A batch that adds or removes a sameAs
// fact, or would derive or overdelete one, is answered by recomputing the
// materialization from the explicit facts held in memory — still no database
// scan, and correct by construction.
//
// The OWL 2 RL slice in owl_rl.go is answered the same way, for as long as
// any of its vocabulary is stated (see isRecomputeVocabulary). Its keys
// conclude sameAs, its chains are registered from rdf:first/rdf:rest lists
// into per-run state, its contradictions are a report rather than triples, and
// an owl:differentFrom conflict suspends sameAs reasoning for a whole class —
// none of which a probe that only watches derive could undo. A store that
// uses them pays a recompute per batch; one that does not pays nothing for
// their existence.

import (
	"sort"
	"strings"
)

// persistedInference is what the store holds for one inferred fact.
type persistedInference struct {
	ID       string
	Rule     string
	Supports []string
}

// inferenceDiff is what a batch changes in the store.
type inferenceDiff struct {
	Upserts []RDFTriple // inferred, with Rule and SupportIDs
	Deletes []string    // ids of inferred triples
	// set and unset are applied to the persisted view once the store has the
	// diff, never before: a failed write must leave the view describing what
	// the store really holds.
	set   map[string]persistedInference
	unset []string
}

func (d inferenceDiff) empty() bool { return len(d.Upserts) == 0 && len(d.Deletes) == 0 }

// maintenanceStats counts what a batch did, for tests and for operators.
type maintenanceStats struct {
	Changed     int
	Overdeleted int
	Rederived   int
	Recomputed  bool
}

// maintainedInference is a materialization held in memory and updated per
// batch of explicit changes.
type maintainedInference struct {
	opts     InferenceOptions
	engine   *inferenceEngine
	explicit *explicitFacts
	// persisted mirrors the inferred rows the store holds, keyed by content.
	persisted map[string]persistedInference
	oversized []OversizedSameAsClass
	// inconsistencies is the OWL 2 RL contradiction report of the current
	// materialization; see InferenceInconsistency.
	inconsistencies []InferenceInconsistency
}

// newMaintainedInference materializes the explicit facts from scratch.
func newMaintainedInference(explicit *explicitFacts, opts InferenceOptions) *maintainedInference {
	m := &maintainedInference{opts: opts, explicit: explicit, persisted: make(map[string]persistedInference)}
	m.engine, m.oversized, m.inconsistencies = materializeEngine(explicit.chosenTriples(), opts)
	return m
}

// materializeEngine is computeInferenceOutcome keeping the engine, whose
// indexes incremental maintenance reuses. The suspension loop is the same
// one, for the same reason: see computeInferenceOutcome, and
// TestMaterializeEngineMatchesTheFullRefresh for the check that the two agree.
func materializeEngine(explicit []RDFTriple, opts InferenceOptions) (*inferenceEngine, []OversizedSameAsClass, []InferenceInconsistency) {
	suspended := make(map[string]bool)
	for {
		engine := runInferenceEngine(explicit, opts, suspended)
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
		inconsistencies := conflicts
		for _, clash := range engine.owlRL.clashes {
			inconsistencies = append(inconsistencies, *clash)
		}
		sortInconsistencies(inconsistencies)
		return engine, engine.sameAs.report(), inconsistencies
	}
}

// fullDiff compares the whole materialization against the persisted view.
func (m *maintainedInference) fullDiff() inferenceDiff {
	keys := make(map[string]struct{}, len(m.persisted)+len(m.engine.records))
	for key := range m.persisted {
		keys[key] = struct{}{}
	}
	for key := range m.engine.records {
		keys[key] = struct{}{}
	}
	return m.diffKeys(keys, nil)
}

// diffKeys compares the given keys of the materialization against the
// persisted view.
//
// With live set, a stored inference whose cited supports are all still live
// keeps its provenance even if the engine now credits another derivation.
// A recompute uses this: it credits every fact afresh, and rewriting
// thousands of rows to swap one valid explanation for another is churn, not
// maintenance.
func (m *maintainedInference) diffKeys(keys map[string]struct{}, live map[string]bool) inferenceDiff {
	diff := inferenceDiff{set: make(map[string]persistedInference)}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		have, stored := m.persisted[key]
		record := m.engine.records[key]
		if record == nil || record.Explicit {
			if stored {
				diff.Deletes = append(diff.Deletes, have.ID)
				diff.unset = append(diff.unset, key)
			}
			continue
		}
		want := persistedInference{ID: record.Triple.ID, Rule: record.Rule, Supports: record.SupportIDs}
		if stored && have.ID == want.ID && have.Rule == want.Rule && equalStrings(have.Supports, want.Supports) {
			continue
		}
		if stored && have.ID == want.ID && live != nil && allLive(have.Supports, live) {
			continue
		}
		if stored && have.ID != want.ID {
			diff.Deletes = append(diff.Deletes, have.ID)
		}
		triple := record.Triple
		triple.Inferred = true
		triple.Rule = record.Rule
		triple.SupportIDs = append([]string(nil), record.SupportIDs...)
		diff.Upserts = append(diff.Upserts, triple)
		diff.set[key] = want
	}
	return diff
}

// commit records that the store now holds the diff.
func (m *maintainedInference) commit(diff inferenceDiff) {
	for _, key := range diff.unset {
		delete(m.persisted, key)
	}
	for key, value := range diff.set {
		m.persisted[key] = value
	}
}

// recompute rebuilds the materialization from the explicit facts and diffs it
// against the store.
func (m *maintainedInference) recompute() (inferenceDiff, maintenanceStats) {
	m.engine, m.oversized, m.inconsistencies = materializeEngine(m.explicit.chosenTriples(), m.opts)
	live := make(map[string]bool, len(m.engine.records))
	for _, record := range m.engine.records {
		live[record.Triple.ID] = true
	}
	keys := make(map[string]struct{}, len(m.persisted)+len(m.engine.records))
	for key := range m.persisted {
		keys[key] = struct{}{}
	}
	for key := range m.engine.records {
		keys[key] = struct{}{}
	}
	return m.diffKeys(keys, live), maintenanceStats{Recomputed: true}
}

func allLive(ids []string, live map[string]bool) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if !live[id] {
			return false
		}
	}
	return true
}

// update brings the materialization in line with explicit changes to the
// given content keys — keys whose explicit fact appeared, disappeared, or
// changed which row supplies it — and returns what the store must change.
func (m *maintainedInference) update(changed []string) (inferenceDiff, maintenanceStats) {
	if len(changed) == 0 {
		return inferenceDiff{}, maintenanceStats{}
	}
	e := m.engine
	stats := maintenanceStats{Changed: len(changed)}
	if m.explicit.recompute > 0 {
		return m.recompute()
	}
	for _, key := range changed {
		if triple, ok := m.explicit.chosen(key); ok && isRecomputeVocabulary(triple) {
			return m.recompute()
		}
		if record := e.records[key]; record != nil && isRecomputeVocabulary(record.Triple) {
			return m.recompute()
		}
	}

	// 1. Overdelete, against the old state: every record whose explicit
	// status changed, then everything any of them helped derive.
	removed := make(map[string]*rdfsInferenceRecord)
	queue := make([]*rdfsInferenceRecord, 0, len(changed))
	for _, key := range changed {
		if record := e.records[key]; record != nil {
			removed[key] = record
			queue = append(queue, record)
		}
	}
	e.collect = func(key string) {
		if _, done := removed[key]; done {
			return
		}
		record := e.records[key]
		if record == nil || record.Explicit {
			// An explicit fact is not derived from anything; whether it is
			// still stated is the change list's business, not this probe's.
			return
		}
		removed[key] = record
		queue = append(queue, record)
	}
	for i := 0; i < len(queue); i++ {
		e.fire(queue[i])
	}
	e.collect = nil
	for _, record := range removed {
		if isRecomputeVocabulary(record.Triple) {
			return m.recompute()
		}
	}
	stats.Overdeleted = len(removed)

	dirty := make(map[string]struct{}, len(removed)+len(changed))
	for key, record := range removed {
		dirty[key] = struct{}{}
		e.unindex(record)
		delete(e.records, key)
	}

	// 2. The explicit facts as they now stand.
	added := make([]*rdfsInferenceRecord, 0, len(changed))
	for _, key := range changed {
		dirty[key] = struct{}{}
		triple, ok := m.explicit.chosen(key)
		if !ok {
			continue
		}
		record := &rdfsInferenceRecord{Triple: tripleWithoutInference(triple), Explicit: true, key: key}
		e.records[key] = record
		added = append(added, record)
	}
	for _, record := range added {
		if restart := e.index(record); restart != nil {
			return m.recompute()
		}
	}

	// 3. Rederive: fire every remaining fact that mentions the subject of
	// something overdeleted. See the file comment for why that finds every
	// derivation still standing.
	candidates := make(map[*rdfsInferenceRecord]struct{})
	seenTerms := make(map[string]struct{})
	for _, record := range removed {
		term := engineTermKey(record.Triple.Subject)
		if _, ok := seenTerms[term]; ok {
			continue
		}
		seenTerms[term] = struct{}{}
		for _, index := range [][]*rdfsInferenceRecord{e.bySubject[term], e.byObject[term], e.byPredicate[term]} {
			for _, candidate := range index {
				candidates[candidate] = struct{}{}
			}
		}
	}
	for _, record := range added {
		candidates[record] = struct{}{}
	}
	ordered := make([]*rdfsInferenceRecord, 0, len(candidates))
	for candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	// Fired in content order so that which derivation a fact is credited to
	// does not depend on map iteration.
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].key < ordered[j].key })
	e.next = nil
	for _, candidate := range ordered {
		e.fire(candidate)
	}

	// 4. Propagate what was rederived or newly derived, semi-naively.
	for len(e.next) > 0 {
		delta := e.next
		e.next = nil
		for _, record := range delta {
			if isRecomputeVocabulary(record.Triple) {
				return m.recompute()
			}
			if _, was := removed[record.key]; was {
				stats.Rederived++
			}
			dirty[record.key] = struct{}{}
			if restart := e.index(record); restart != nil {
				return m.recompute()
			}
		}
		for _, record := range delta {
			e.fire(record)
		}
	}
	return m.diffKeys(dirty, nil), stats
}

// unindex removes a record from every index the engine joins through.
func (e *inferenceEngine) unindex(record *rdfsInferenceRecord) {
	t := record.Triple
	predicate := engineTermKey(t.Predicate)
	subject := engineTermKey(t.Subject)
	object := engineTermKey(t.Object)
	removeIndexed(e.byPredicate, predicate, record)
	removeIndexed(e.byPredicateSubject, predicate+"\x01"+subject, record)
	removeIndexed(e.byPredicateObject, predicate+"\x01"+object, record)
	removeIndexed(e.bySubject, subject, record)
	removeIndexed(e.byObject, object, record)
}

func removeIndexed(index map[string][]*rdfsInferenceRecord, key string, record *rdfsInferenceRecord) {
	list := index[key]
	for i, candidate := range list {
		if candidate == record {
			last := len(list) - 1
			list[i] = list[last]
			list[last] = nil
			list = list[:last]
			break
		}
	}
	if len(list) == 0 {
		delete(index, key)
		return
	}
	index[key] = list
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- the explicit facts --------------------------------------------------

// explicitFacts is the set of explicit statements inference starts from, as
// FindTriples would return it: stored triples that are not inferred, plus the
// property-graph projection. It is kept by row so that a change event — which
// names a row, not a statement — can be applied without reading anything.
type explicitFacts struct {
	// byID maps a triple id to its content key. Stored triples have their own
	// ids; projected ones have the pg: ids the projection gives them.
	byID map[string]string
	// byKey holds, per content key, every row currently stating it. The
	// statement is explicit while any row states it, and the inference engine
	// credits the one with the smallest id, as a full refresh does.
	byKey map[string]map[string]RDFTriple
	// rows maps a property-graph row ("node:<id>", "edge:<id>") to the ids of
	// the triples it projects.
	rows map[string][]string
	// vocabulary counts content keys that are inference vocabulary: while it
	// is zero nothing can be inferred and maintenance has nothing to hold.
	vocabulary int
	// recompute counts content keys of the owl_rl.go vocabulary
	// (isOWLRLVocabulary): while it is above zero every batch is recomputed.
	// sameAs alone does not count — a batch that touches no sameAs fact is
	// maintained by DRed even when the store holds some.
	recompute int
	// touched records the chosen triple of each key before the first change
	// in the current batch.
	touched map[string]chosenBefore
}

type chosenBefore struct {
	triple RDFTriple
	ok     bool
}

func newExplicitFacts() *explicitFacts {
	return &explicitFacts{
		byID:    make(map[string]string),
		byKey:   make(map[string]map[string]RDFTriple),
		rows:    make(map[string][]string),
		touched: make(map[string]chosenBefore),
	}
}

// chosen is the triple a key's explicit statement is credited to.
func (f *explicitFacts) chosen(key string) (RDFTriple, bool) {
	holders := f.byKey[key]
	if len(holders) == 0 {
		return RDFTriple{}, false
	}
	best := ""
	for id := range holders {
		if best == "" || id < best {
			best = id
		}
	}
	return holders[best], true
}

func (f *explicitFacts) chosenTriples() []RDFTriple {
	out := make([]RDFTriple, 0, len(f.byKey))
	for key := range f.byKey {
		if triple, ok := f.chosen(key); ok {
			out = append(out, triple)
		}
	}
	return out
}

func (f *explicitFacts) touch(key string) {
	if _, ok := f.touched[key]; ok {
		return
	}
	triple, ok := f.chosen(key)
	f.touched[key] = chosenBefore{triple: triple, ok: ok}
}

// set states (or, with ok false, withdraws) the triple a row id supplies.
func (f *explicitFacts) set(id string, triple RDFTriple, ok bool) {
	if oldKey, had := f.byID[id]; had {
		f.touch(oldKey)
		holders := f.byKey[oldKey]
		old := holders[id]
		delete(holders, id)
		if len(holders) == 0 {
			delete(f.byKey, oldKey)
			if isInferenceVocabulary(old) {
				f.vocabulary--
			}
			if isOWLRLVocabulary(old) {
				f.recompute--
			}
		}
		delete(f.byID, id)
	}
	if !ok {
		return
	}
	triple = tripleWithoutInference(triple)
	triple.ID = id
	key := inferenceContentKey(triple)
	f.touch(key)
	holders := f.byKey[key]
	if holders == nil {
		holders = make(map[string]RDFTriple, 1)
		f.byKey[key] = holders
		if isInferenceVocabulary(triple) {
			f.vocabulary++
		}
		if isOWLRLVocabulary(triple) {
			f.recompute++
		}
	}
	holders[id] = triple
	f.byID[id] = key
}

// setRow replaces the triples a property-graph row projects.
func (f *explicitFacts) setRow(row string, triples []RDFTriple) {
	next := make(map[string]struct{}, len(triples))
	for _, triple := range triples {
		next[triple.ID] = struct{}{}
	}
	for _, id := range f.rows[row] {
		if _, keep := next[id]; !keep {
			f.set(id, RDFTriple{}, false)
		}
	}
	ids := make([]string, 0, len(triples))
	for _, triple := range triples {
		f.set(triple.ID, triple, true)
		ids = append(ids, triple.ID)
	}
	if len(ids) == 0 {
		delete(f.rows, row)
		return
	}
	f.rows[row] = ids
}

// drainChanged returns the keys whose chosen triple differs from what it was
// when the batch began, and starts the next batch.
func (f *explicitFacts) drainChanged() []string {
	out := make([]string, 0, len(f.touched))
	for key, before := range f.touched {
		now, ok := f.chosen(key)
		if ok != before.ok || (ok && now.ID != before.triple.ID) {
			out = append(out, key)
		}
	}
	f.touched = make(map[string]chosenBefore)
	sort.Strings(out)
	return out
}

// isInferenceVocabulary reports whether a statement can make any rule fire:
// the RDFS schema predicates, anything in the OWL namespace, and a type
// declaration naming an RDFS or OWL class. A store without one of these has
// nothing to infer, which is what lets maintenance cost nothing on a brain
// that has never declared a schema. TestVocabularyFreeDataInfersNothing keeps
// this list honest against the rules.
func isInferenceVocabulary(triple RDFTriple) bool {
	predicate := triple.Predicate.Value
	switch predicate {
	case rdfsSubClassOfIRI, rdfsSubPropertyOfIRI, rdfsDomainIRI, rdfsRangeIRI:
		return true
	case rdfTypeIRI:
		object := triple.Object.Value
		return triple.Object.Kind == RDFTermIRI && (strings.HasPrefix(object, owlNamespace) ||
			strings.HasPrefix(object, rdfsNamespace) || object == rdfPropertyIRI)
	}
	return strings.HasPrefix(predicate, owlNamespace)
}

const rdfsNamespace = "http://www.w3.org/2000/01/rdf-schema#"

// isRecomputeVocabulary names the statements whose consequences are
// maintained by recomputing rather than by DRed: owl:sameAs (see the file
// comment) and the vocabulary of owl_rl.go — keys, chains and the list cells
// they are read from, and the declarations whose violation is reported as a
// contradiction.
func isRecomputeVocabulary(triple RDFTriple) bool {
	return triple.Predicate.Value == owlSameAsIRI || isOWLRLVocabulary(triple)
}

// isOWLRLVocabulary names the vocabulary of owl_rl.go.
func isOWLRLVocabulary(triple RDFTriple) bool {
	switch triple.Predicate.Value {
	case owlPropertyChainAxiomIRI, owlDisjointWithIRI, owlPropertyDisjointWithIRI,
		owlDifferentFromIRI, rdfFirstIRI, rdfRestIRI:
		return true
	case rdfTypeIRI:
		return triple.Object.Value == owlFunctionalPropertyIRI || triple.Object.Value == owlInverseFunctionalPropertyIRI
	}
	return false
}
