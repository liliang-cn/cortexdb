package graph

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// randomFactSpace generates statements over a deliberately small vocabulary,
// so that random sequences keep running into each other: chains of subClassOf
// two and three long, transitive and symmetric properties closing cycles,
// sameAs classes that grow and split, the same fact supported two ways.
type randomFactSpace struct {
	rng        *rand.Rand
	vocabulary bool // false: never produce a schema statement
	sameAs     bool
	// owlRL adds the OWL 2 RL slice of owl_rl.go: functional and
	// inverse-functional keys, disjointness, owl:differentFrom, and (through
	// chain) property chains read from rdf:first/rdf:rest lists.
	owlRL bool
	// lists numbers the blank nodes of generated chain lists.
	lists *int
}

func exIRI(name string) RDFTerm { return NewIRI("http://ex.test/" + name) }

func (s randomFactSpace) pick(prefix string, n int) RDFTerm {
	return exIRI(fmt.Sprintf("%s%d", prefix, s.rng.Intn(n)))
}

func (s randomFactSpace) fact() RDFTriple {
	class := func() RDFTerm { return s.pick("C", 5) }
	prop := func() RDFTerm { return s.pick("p", 4) }
	ind := func() RDFTerm { return s.pick("i", 6) }
	t := RDFTriple{}
	kinds := 3
	if s.vocabulary {
		kinds = 14
	}
	if s.owlRL {
		kinds = 19
	}
	switch k := s.rng.Intn(kinds); k {
	case 0:
		t = RDFTriple{Subject: ind(), Predicate: NewIRI(rdfTypeIRI), Object: class()}
	case 1:
		t = RDFTriple{Subject: ind(), Predicate: prop(), Object: ind()}
	case 2:
		t = RDFTriple{Subject: ind(), Predicate: prop(), Object: NewLiteral(fmt.Sprintf("v%d", s.rng.Intn(2)))}
	case 3, 4:
		t = RDFTriple{Subject: class(), Predicate: NewIRI(rdfsSubClassOfIRI), Object: class()}
	case 5:
		t = RDFTriple{Subject: prop(), Predicate: NewIRI(rdfsSubPropertyOfIRI), Object: prop()}
	case 6:
		t = RDFTriple{Subject: prop(), Predicate: NewIRI(rdfsDomainIRI), Object: class()}
	case 7:
		t = RDFTriple{Subject: prop(), Predicate: NewIRI(rdfsRangeIRI), Object: class()}
	case 8:
		t = RDFTriple{Subject: prop(), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI(owlTransitivePropertyIRI)}
	case 9:
		t = RDFTriple{Subject: prop(), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI(owlSymmetricPropertyIRI)}
	case 10:
		t = RDFTriple{Subject: prop(), Predicate: NewIRI(owlInverseOfIRI), Object: prop()}
	case 11:
		t = RDFTriple{Subject: class(), Predicate: NewIRI(owlEquivalentClassIRI), Object: class()}
	case 12:
		t = RDFTriple{Subject: class(), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI(rdfsClassIRI)}
	case 13:
		if s.sameAs {
			t = RDFTriple{Subject: ind(), Predicate: NewIRI(owlSameAsIRI), Object: ind()}
		} else {
			t = RDFTriple{Subject: prop(), Predicate: NewIRI(owlEquivalentPropertyIRI), Object: prop()}
		}
	case 14:
		t = RDFTriple{Subject: prop(), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI(owlFunctionalPropertyIRI)}
	case 15:
		t = RDFTriple{Subject: prop(), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI(owlInverseFunctionalPropertyIRI)}
	case 16:
		t = RDFTriple{Subject: class(), Predicate: NewIRI(owlDisjointWithIRI), Object: class()}
	case 17:
		t = RDFTriple{Subject: prop(), Predicate: NewIRI(owlPropertyDisjointWithIRI), Object: prop()}
	case 18:
		t = RDFTriple{Subject: ind(), Predicate: NewIRI(owlDifferentFromIRI), Object: ind()}
	}
	if s.rng.Intn(6) == 0 {
		g := exIRI("g1")
		t.Graph = &g
	}
	t.ID = tripleDigest(t)
	return t
}

// inferredKeys is the set a full refresh would store.
func inferredKeys(records map[string]rdfsInferenceRecord) []string {
	out := make([]string, 0)
	for key, record := range records {
		if !record.Explicit {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func maintainedKeys(m *maintainedInference) []string {
	out := make([]string, 0, len(m.persisted))
	for key := range m.persisted {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func describeKeyDiff(want, got []string) string {
	w := make(map[string]bool, len(want))
	for _, k := range want {
		w[k] = true
	}
	g := make(map[string]bool, len(got))
	for _, k := range got {
		g[k] = true
	}
	var b strings.Builder
	for _, k := range want {
		if !g[k] {
			fmt.Fprintf(&b, "  missing: %s\n", strings.ReplaceAll(k, "\x00", " "))
		}
	}
	for _, k := range got {
		if !w[k] {
			fmt.Fprintf(&b, "  stale:   %s\n", strings.ReplaceAll(k, "\x00", " "))
		}
	}
	return b.String()
}

// checkProvenance asserts every stored inference names supports that exist —
// a support id pointing at a fact the batch removed is a stale explanation
// even when the fact itself is right.
func checkProvenance(t *testing.T, m *maintainedInference) {
	t.Helper()
	ids := make(map[string]bool, len(m.engine.records))
	for _, record := range m.engine.records {
		ids[record.Triple.ID] = true
	}
	for key, p := range m.persisted {
		for _, support := range p.Supports {
			if !ids[support] {
				t.Fatalf("inference %q cites support %s, which no longer exists", key, support)
			}
		}
	}
}

// chain states a two-link property chain: q owl:propertyChainAxiom (a b).
func (s randomFactSpace) chain() []RDFTriple {
	*s.lists++
	l1 := NewBlankNode(fmt.Sprintf("l%da", *s.lists))
	l2 := NewBlankNode(fmt.Sprintf("l%db", *s.lists))
	prop := func() RDFTerm { return s.pick("p", 4) }
	out := []RDFTriple{
		{Subject: prop(), Predicate: NewIRI(owlPropertyChainAxiomIRI), Object: l1},
		{Subject: l1, Predicate: NewIRI(rdfFirstIRI), Object: prop()},
		{Subject: l1, Predicate: NewIRI(rdfRestIRI), Object: l2},
		{Subject: l2, Predicate: NewIRI(rdfFirstIRI), Object: prop()},
		{Subject: l2, Predicate: NewIRI(rdfRestIRI), Object: NewIRI(rdfNilIRI)},
	}
	for i := range out {
		out[i].ID = tripleDigest(out[i])
	}
	return out
}

func inconsistencyJSON(t *testing.T, list []InferenceInconsistency) string {
	t.Helper()
	if len(list) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func runDRedSequences(t *testing.T, sequences int, sameAs, owlRL bool) {
	seed := int64(sequences) * 7919
	if sameAs {
		seed++
	}
	if owlRL {
		seed += 2
	}
	rng := rand.New(rand.NewSource(seed))
	space := randomFactSpace{rng: rng, vocabulary: true, sameAs: sameAs, owlRL: owlRL, lists: new(int)}
	recomputed := 0
	for seq := 0; seq < sequences; seq++ {
		explicit := newExplicitFacts()
		live := make(map[string]RDFTriple)
		for i := 0; i < 4+rng.Intn(8); i++ {
			f := space.fact()
			live[f.ID] = f
			explicit.set(f.ID, f, true)
		}
		explicit.drainChanged()
		m := newMaintainedInference(explicit, InferenceOptions{MaxSameAsClassSize: 3})
		m.commit(m.fullDiff())

		var history []string
		for step := 0; step < 12; step++ {
			// A batch of one to three changes, as one transaction would be.
			for n := 1 + rng.Intn(3); n > 0; n-- {
				if len(live) > 0 && rng.Intn(5) < 2 {
					ids := make([]string, 0, len(live))
					for id := range live {
						ids = append(ids, id)
					}
					sort.Strings(ids)
					id := ids[rng.Intn(len(ids))]
					history = append(history, "- "+live[id].String())
					delete(live, id)
					explicit.set(id, RDFTriple{}, false)
				} else if owlRL && rng.Intn(6) == 0 {
					for _, f := range space.chain() {
						history = append(history, "+ "+f.String())
						live[f.ID] = f
						explicit.set(f.ID, f, true)
					}
				} else {
					f := space.fact()
					history = append(history, "+ "+f.String())
					live[f.ID] = f
					explicit.set(f.ID, f, true)
				}
			}
			diff, stats := m.update(explicit.drainChanged())
			if stats.Recomputed {
				recomputed++
			}
			m.commit(diff)

			all := make([]RDFTriple, 0, len(live))
			for _, f := range live {
				all = append(all, f)
			}
			outcome := computeInferenceOutcome(all, InferenceOptions{MaxSameAsClassSize: 3})
			want := inferredKeys(outcome.records)
			got := maintainedKeys(m)
			if strings.Join(want, "\n") != strings.Join(got, "\n") {
				t.Fatalf("sequence %d step %d: maintained inferences differ from a full recompute\n%s\nhistory:\n%s",
					seq, step, describeKeyDiff(want, got), strings.Join(history, "\n"))
			}
			if w, g := inconsistencyJSON(t, outcome.inconsistencies), inconsistencyJSON(t, m.inconsistencies); w != g {
				t.Fatalf("sequence %d step %d: inconsistency report differs from a full recompute\nwant %s\n got %s", seq, step, w, g)
			}
			checkProvenance(t, m)
		}
	}
	t.Logf("%d sequences, %d batches answered by recompute", sequences, recomputed)
}

func computeInferenceRecordsWith(explicit []RDFTriple, opts InferenceOptions) map[string]rdfsInferenceRecord {
	records, _ := computeInferenceRecords(explicit, opts)
	return records
}

func TestDRedAgreesWithAFullRecomputeOverRandomSequences(t *testing.T) {
	n := exhaustiveRuns(1500, 300)
	runDRedSequences(t, n, false, false)
}

func TestDRedAgreesWithAFullRecomputeWhenSameAsComesAndGoes(t *testing.T) {
	n := exhaustiveRuns(1000, 200)
	runDRedSequences(t, n, true, false)
}

// Keys, chains and contradictions (owl_rl.go): the maintained materialization
// and its inconsistency report must both equal a full recompute's.
func TestDRedAgreesWithAFullRecomputeUnderOWLRLKeysChainsAndContradictions(t *testing.T) {
	n := exhaustiveRuns(1000, 200)
	runDRedSequences(t, n, true, true)
}

// The rederive step fires the facts that mention an overdeleted fact's
// subject, which finds every remaining derivation only if every rule keeps
// its conclusion's subject in one of its premises. This holds the rules to it.
func TestRulesKeepTheirSubjectInAPremise(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	space := randomFactSpace{rng: rng, vocabulary: true, sameAs: true}
	for round := 0; round < 300; round++ {
		facts := make([]RDFTriple, 0, 16)
		for i := 0; i < 16; i++ {
			facts = append(facts, space.fact())
		}
		records := computeInferenceRecordsWith(facts, InferenceOptions{})
		byID := make(map[string]rdfsInferenceRecord, len(records))
		for _, record := range records {
			byID[record.Triple.ID] = record
		}
		for _, record := range records {
			if record.Explicit {
				continue
			}
			subject := engineTermKey(record.Triple.Subject)
			found := false
			for _, id := range record.SupportIDs {
				s := byID[id].Triple
				if engineTermKey(s.Subject) == subject || engineTermKey(s.Object) == subject || engineTermKey(s.Predicate) == subject {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("rule %s concluded %s from premises none of which mentions its subject %v",
					record.Rule, record.Triple.String(), record.SupportIDs)
			}
		}
	}
}

// Maintenance holds nothing and does nothing while the store has no schema,
// which is only sound if no rule fires without one.
func TestVocabularyFreeDataInfersNothing(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	space := randomFactSpace{rng: rng}
	for round := 0; round < 300; round++ {
		facts := make([]RDFTriple, 0, 24)
		for i := 0; i < 24; i++ {
			f := space.fact()
			if isInferenceVocabulary(f) {
				t.Fatalf("generator produced vocabulary: %s", f.String())
			}
			facts = append(facts, f)
		}
		if got := inferredKeys(computeInferenceRecordsWith(facts, InferenceOptions{})); len(got) != 0 {
			t.Fatalf("vocabulary-free data inferred %d facts, e.g. %q", len(got), got[0])
		}
	}
}

// materializeEngine must reproduce computeInferenceRecords exactly, or a
// maintained store and a refreshed one would disagree from the first batch.
func TestMaterializeEngineMatchesTheFullRefresh(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	space := randomFactSpace{rng: rng, vocabulary: true, sameAs: true, owlRL: true, lists: new(int)}
	for round := 0; round < 200; round++ {
		facts := make([]RDFTriple, 0, 25)
		for i := 0; i < 20; i++ {
			facts = append(facts, space.fact())
		}
		facts = append(facts, space.chain()...)
		opts := InferenceOptions{MaxSameAsClassSize: 3}
		outcome := computeInferenceOutcome(facts, opts)
		want := outcome.records
		engine, _, inconsistencies := materializeEngine(facts, opts)
		if inconsistencyJSON(t, inconsistencies) != inconsistencyJSON(t, outcome.inconsistencies) {
			t.Fatalf("inconsistency reports differ")
		}
		if len(engine.records) != len(want) {
			t.Fatalf("engine holds %d records, refresh %d", len(engine.records), len(want))
		}
		for key, record := range want {
			got := engine.records[key]
			if got == nil || got.Rule != record.Rule || !equalStrings(got.SupportIDs, record.SupportIDs) {
				t.Fatalf("record %q differs", key)
			}
		}
	}
}
