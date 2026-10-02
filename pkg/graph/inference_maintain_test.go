package graph

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

func startMaintainer(t *testing.T, g *GraphStore) *InferenceMaintainer {
	t.Helper()
	m, err := g.StartInferenceMaintenance(context.Background(), InferenceMaintenanceConfig{
		PollInterval: 5 * time.Millisecond,
		Logf:         t.Logf,
	})
	if err != nil {
		t.Fatalf("StartInferenceMaintenance: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func waitInference(t *testing.T, g *GraphStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := g.WaitForInference(ctx, 0); err != nil {
		t.Fatalf("WaitForInference: %v", err)
	}
}

// assertInferencesMatchRecompute compares what the store holds as inferred
// with what a full refresh would compute from the explicit facts the store
// holds now, and checks that every stored inference cites supports that exist.
func assertInferencesMatchRecompute(t *testing.T, g *GraphStore, context_ string) {
	t.Helper()
	assertMaintainedMatchesRecompute(t, g, nil, context_)
}

// assertMaintainedMatchesRecompute is assertInferencesMatchRecompute that also
// holds the maintainer's contradiction report to a full recompute's, and
// ignores the triples SHACL rules own.
func assertMaintainedMatchesRecompute(t *testing.T, g *GraphStore, m *InferenceMaintainer, context_ string) {
	t.Helper()
	ctx := context.Background()
	explicitOnly, inferredOnly := false, true
	explicit, err := g.FindTriples(ctx, TriplePattern{Inferred: &explicitOnly})
	if err != nil {
		t.Fatal(err)
	}
	outcome := computeInferenceOutcome(explicit, InferenceOptions{})
	want := inferredKeys(outcome.records)
	if m != nil {
		if w, g := inconsistencyJSON(t, outcome.inconsistencies), inconsistencyJSON(t, m.Inconsistencies()); w != g {
			t.Fatalf("%s: maintained inconsistency report differs from a full recompute\nwant %s\n got %s", context_, w, g)
		}
	}
	all, err := g.findStoredTriples(ctx, TriplePattern{Inferred: &inferredOnly})
	if err != nil {
		t.Fatal(err)
	}
	stored := make([]RDFTriple, 0, len(all))
	got := make([]string, 0, len(all))
	for _, triple := range all {
		if isSHACLRuleTriple(triple) {
			continue
		}
		stored = append(stored, triple)
		got = append(got, inferenceContentKey(tripleWithoutInference(triple)))
	}
	sort.Strings(got)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Fatalf("%s: stored inferences differ from a full recompute over %d explicit facts\n%s",
			context_, len(explicit), describeKeyDiff(want, got))
	}
	exists := make(map[string]bool, len(explicit)+len(stored))
	for _, triple := range explicit {
		exists[triple.ID] = true
	}
	for _, triple := range stored {
		exists[triple.ID] = true
	}
	for _, triple := range stored {
		for _, support := range triple.SupportIDs {
			if !exists[support] {
				t.Fatalf("%s: %s cites support %s, which is not in the store", context_, triple.String(), support)
			}
		}
	}
}

// storeOps drives a store with random writes over a small vocabulary that
// mixes stored triples with the property-graph projection: schema statements
// name projected classes (cxt:) and relations (cxr:), and stored statements
// name projected nodes (cxn:), so a node or edge write changes what the
// schema derives and the other way round.
type storeOps struct {
	// owlRL adds keys, disjointness, owl:differentFrom and property chains.
	owlRL bool
	lists int
	t     *testing.T
	g     *GraphStore
	rng   *rand.Rand
	live  map[string]RDFTriple
	nodes map[string]bool
	edges map[string]bool
}

func (o *storeOps) term(kind string) RDFTerm {
	switch kind {
	case "class":
		if o.rng.Intn(2) == 0 {
			return NewIRI(PropertyTypeNamespace + fmt.Sprintf("T%d", o.rng.Intn(3)))
		}
		return exIRI(fmt.Sprintf("C%d", o.rng.Intn(4)))
	case "prop":
		if o.rng.Intn(2) == 0 {
			return NewIRI(PropertyRelNamespace + fmt.Sprintf("r%d", o.rng.Intn(2)))
		}
		return exIRI(fmt.Sprintf("p%d", o.rng.Intn(3)))
	default:
		if o.rng.Intn(2) == 0 {
			return NewIRI(PropertyGraphNodeIRI(fmt.Sprintf("n%d", o.rng.Intn(5))))
		}
		return exIRI(fmt.Sprintf("i%d", o.rng.Intn(5)))
	}
}

func (o *storeOps) randomTriple() RDFTriple {
	typ := NewIRI(rdfTypeIRI)
	if o.owlRL && o.rng.Intn(4) == 0 {
		switch o.rng.Intn(5) {
		case 0:
			return RDFTriple{Subject: o.term("prop"), Predicate: typ, Object: NewIRI(owlFunctionalPropertyIRI)}
		case 1:
			return RDFTriple{Subject: o.term("prop"), Predicate: typ, Object: NewIRI(owlInverseFunctionalPropertyIRI)}
		case 2:
			return RDFTriple{Subject: o.term("class"), Predicate: NewIRI(owlDisjointWithIRI), Object: o.term("class")}
		case 3:
			return RDFTriple{Subject: o.term("prop"), Predicate: NewIRI(owlPropertyDisjointWithIRI), Object: o.term("prop")}
		default:
			return RDFTriple{Subject: o.term("ind"), Predicate: NewIRI(owlDifferentFromIRI), Object: o.term("ind")}
		}
	}
	switch o.rng.Intn(13) {
	case 0, 1:
		return RDFTriple{Subject: o.term("class"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: o.term("class")}
	case 2:
		return RDFTriple{Subject: o.term("prop"), Predicate: NewIRI(rdfsSubPropertyOfIRI), Object: o.term("prop")}
	case 3:
		return RDFTriple{Subject: o.term("prop"), Predicate: NewIRI(rdfsDomainIRI), Object: o.term("class")}
	case 4:
		return RDFTriple{Subject: o.term("prop"), Predicate: NewIRI(rdfsRangeIRI), Object: o.term("class")}
	case 5:
		return RDFTriple{Subject: o.term("prop"), Predicate: typ, Object: NewIRI(owlTransitivePropertyIRI)}
	case 6:
		return RDFTriple{Subject: o.term("prop"), Predicate: typ, Object: NewIRI(owlSymmetricPropertyIRI)}
	case 7:
		return RDFTriple{Subject: o.term("prop"), Predicate: NewIRI(owlInverseOfIRI), Object: o.term("prop")}
	case 8:
		return RDFTriple{Subject: o.term("ind"), Predicate: NewIRI(owlSameAsIRI), Object: o.term("ind")}
	case 9:
		return RDFTriple{Subject: o.term("ind"), Predicate: typ, Object: o.term("class")}
	default:
		return RDFTriple{Subject: o.term("ind"), Predicate: o.term("prop"), Object: o.term("ind")}
	}
}

func (o *storeOps) step(ctx context.Context) string {
	g := o.g
	vec := []float32{0.1, 0.2, 0.3, 0.4}
	switch r := o.rng.Intn(10); {
	case o.owlRL && r == 0 && o.rng.Intn(3) == 0:
		// A two-link property chain, written as one batch the way an import
		// writes a list.
		o.lists++
		l1, l2 := NewBlankNode(fmt.Sprintf("c%da", o.lists)), NewBlankNode(fmt.Sprintf("c%db", o.lists))
		chain := []*RDFTriple{
			{Subject: o.term("prop"), Predicate: NewIRI(owlPropertyChainAxiomIRI), Object: l1},
			{Subject: l1, Predicate: NewIRI(rdfFirstIRI), Object: o.term("prop")},
			{Subject: l1, Predicate: NewIRI(rdfRestIRI), Object: l2},
			{Subject: l2, Predicate: NewIRI(rdfFirstIRI), Object: o.term("prop")},
			{Subject: l2, Predicate: NewIRI(rdfRestIRI), Object: NewIRI(rdfNilIRI)},
		}
		if _, err := g.UpsertTriplesBatch(ctx, chain); err != nil {
			o.t.Fatal(err)
		}
		for _, tr := range chain {
			o.live[tr.ID] = *tr
		}
		return "+ chain " + chain[0].String()
	case r < 3:
		tr := o.randomTriple()
		if err := g.UpsertTriple(ctx, &tr); err != nil {
			o.t.Fatalf("upsert %s: %v", tr.String(), err)
		}
		o.live[tr.ID] = tr
		return "+ " + tr.String()
	case r < 5 && len(o.live) > 0:
		ids := make([]string, 0, len(o.live))
		for id := range o.live {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		id := ids[o.rng.Intn(len(ids))]
		tr := o.live[id]
		delete(o.live, id)
		if err := g.DeleteTriple(ctx, tr); err != nil {
			o.t.Fatalf("delete %s: %v", tr.String(), err)
		}
		return "- " + tr.String()
	case r < 7:
		id := fmt.Sprintf("n%d", o.rng.Intn(5))
		node := &GraphNode{ID: id, Vector: vec, NodeType: fmt.Sprintf("T%d", o.rng.Intn(3))}
		if o.rng.Intn(3) == 0 {
			node.NodeType = ""
		}
		if err := g.UpsertNode(ctx, node); err != nil {
			o.t.Fatal(err)
		}
		o.nodes[id] = true
		return "node " + id + " " + node.NodeType
	case r < 8:
		id := fmt.Sprintf("n%d", o.rng.Intn(5))
		if !o.nodes[id] {
			return "noop"
		}
		if err := g.DeleteNode(ctx, id); err != nil {
			o.t.Fatal(err)
		}
		delete(o.nodes, id)
		for e := range o.edges {
			if strings.Contains(e, "|"+id+"|") {
				delete(o.edges, e)
			}
		}
		return "-node " + id
	default:
		from, to := fmt.Sprintf("n%d", o.rng.Intn(5)), fmt.Sprintf("n%d", o.rng.Intn(5))
		if !o.nodes[from] || !o.nodes[to] {
			return "noop"
		}
		id := fmt.Sprintf("e-%s-%s", from, to)
		key := id + "|" + from + "|" + to + "|"
		if o.edges[key] && o.rng.Intn(2) == 0 {
			if err := g.DeleteEdge(ctx, id); err != nil {
				o.t.Fatal(err)
			}
			delete(o.edges, key)
			return "-edge " + id
		}
		edge := &GraphEdge{ID: id, FromNodeID: from, ToNodeID: to, EdgeType: fmt.Sprintf("r%d", o.rng.Intn(2)), Weight: 1}
		if err := g.UpsertEdge(ctx, edge); err != nil {
			o.t.Fatal(err)
		}
		o.edges[key] = true
		return "edge " + id + " " + edge.EdgeType
	}
}

func TestMaintainedInferencesMatchAFullRecomputeAfterEveryRandomSequence(t *testing.T) {
	sequences := 1000
	if testing.Short() {
		sequences = 120
	}
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			m := startMaintainer(t, g)
			ops := &storeOps{t: t, g: g, rng: rand.New(rand.NewSource(20261002)),
				live: map[string]RDFTriple{}, nodes: map[string]bool{}, edges: map[string]bool{}}
			var history []string
			var writing, waiting, checking time.Duration
			withClashes := 0
			for seq := 0; seq < sequences; seq++ {
				began := time.Now()
				for n := 1 + ops.rng.Intn(5); n > 0; n-- {
					history = append(history, ops.step(ctx))
				}
				wrote := time.Now()
				waitInference(t, g)
				caught := time.Now()
				if len(history) > 40 {
					history = history[len(history)-40:]
				}
				// The second half adds the OWL 2 RL vocabulary, which the
				// maintainer answers by recomputing; the first half is DRed.
				ops.owlRL = seq >= sequences/2
				assertMaintainedMatchesRecompute(t, g, m, fmt.Sprintf("sequence %d (recent writes:\n%s\n)", seq, strings.Join(history, "\n")))
				if len(m.Inconsistencies()) > 0 {
					withClashes++
				}
				writing += wrote.Sub(began)
				waiting += caught.Sub(wrote)
				checking += time.Since(caught)
			}
			s := m.Stats()
			t.Logf("%d sequences (%d with a non-empty contradiction report): %d batches, %d baselines, %d recomputes, %d upserted, %d deleted; writing %v, waiting %v, checking %v",
				sequences, withClashes, s.Batches, s.Baselines, s.Recomputes, s.Upserted, s.Deleted, writing, waiting, checking)
			if withClashes == 0 {
				t.Fatalf("no sequence produced a contradiction; the OWL 2 RL half tested nothing")
			}
		})
	}
}

func TestDeletingTheSupportOfAMultiStepInferenceLeavesNothingStale(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			startMaintainer(t, g)
			sub := NewIRI(rdfsSubClassOfIRI)
			typ := NewIRI(rdfTypeIRI)
			chain := []*RDFTriple{
				{Subject: exIRI("Dog"), Predicate: sub, Object: exIRI("Mammal")},
				{Subject: exIRI("Mammal"), Predicate: sub, Object: exIRI("Animal")},
				{Subject: exIRI("Animal"), Predicate: sub, Object: exIRI("Thing")},
				{Subject: exIRI("rex"), Predicate: typ, Object: exIRI("Dog")},
			}
			for _, tr := range chain {
				if err := g.UpsertTriple(ctx, tr); err != nil {
					t.Fatal(err)
				}
			}
			waitInference(t, g)
			thing := exIRI("Thing")
			rex := exIRI("rex")
			found, err := g.FindTriples(ctx, TriplePattern{Subject: &rex, Predicate: &typ, Object: &thing})
			if err != nil || len(found) != 1 || !found[0].Inferred {
				t.Fatalf("rex should be inferred a Thing: %v (%v)", found, err)
			}
			// The middle link goes: rex is still a Mammal, but no longer an
			// Animal or a Thing, and Dog is no longer below Animal.
			if err := g.DeleteTriple(ctx, *chain[1]); err != nil {
				t.Fatal(err)
			}
			waitInference(t, g)
			for _, gone := range []RDFTriple{
				{Subject: rex, Predicate: typ, Object: thing},
				{Subject: rex, Predicate: typ, Object: exIRI("Animal")},
				{Subject: exIRI("Dog"), Predicate: sub, Object: exIRI("Animal")},
				{Subject: exIRI("Dog"), Predicate: sub, Object: thing},
			} {
				s, p, o := gone.Subject, gone.Predicate, gone.Object
				if found, _ := g.FindTriples(ctx, TriplePattern{Subject: &s, Predicate: &p, Object: &o}); len(found) != 0 {
					t.Fatalf("stale inference survived the delete: %s", gone.String())
				}
			}
			assertInferencesMatchRecompute(t, g, "after delete")
		})
	}
}

// upsert_relations writes edges, and FindTriples projects edges as triples,
// so a relation write changes what a schema over cxr: predicates derives.
func TestAnEdgeWriteUpdatesInferencesOverTheProjectedRelation(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			startMaintainer(t, g)
			vec := []float32{0.1, 0.2, 0.3, 0.4}
			for _, id := range []string{"alice", "bob", "carol"} {
				if err := g.UpsertNode(ctx, &GraphNode{ID: id, Vector: vec, NodeType: "Person"}); err != nil {
					t.Fatal(err)
				}
			}
			manages := NewIRI(PropertyRelNamespace + "manages")
			if err := g.UpsertTriple(ctx, &RDFTriple{Subject: manages, Predicate: NewIRI(rdfTypeIRI), Object: NewIRI(owlTransitivePropertyIRI)}); err != nil {
				t.Fatal(err)
			}
			for _, e := range [][2]string{{"alice", "bob"}, {"bob", "carol"}} {
				if err := g.UpsertEdge(ctx, &GraphEdge{ID: e[0] + "-" + e[1], FromNodeID: e[0], ToNodeID: e[1], EdgeType: "manages", Weight: 1}); err != nil {
					t.Fatal(err)
				}
			}
			waitInference(t, g)
			alice, carol := NewIRI(PropertyGraphNodeIRI("alice")), NewIRI(PropertyGraphNodeIRI("carol"))
			if found, _ := g.FindTriples(ctx, TriplePattern{Subject: &alice, Predicate: &manages, Object: &carol}); len(found) != 1 {
				t.Fatalf("alice should be inferred to manage carol, found %v", found)
			}
			// Retyping the edge removes the projected premise.
			if err := g.UpsertEdge(ctx, &GraphEdge{ID: "bob-carol", FromNodeID: "bob", ToNodeID: "carol", EdgeType: "mentors", Weight: 1}); err != nil {
				t.Fatal(err)
			}
			waitInference(t, g)
			if found, _ := g.FindTriples(ctx, TriplePattern{Subject: &alice, Predicate: &manages, Object: &carol}); len(found) != 0 {
				t.Fatalf("inference survived the edge that supported it: %v", found)
			}
			assertInferencesMatchRecompute(t, g, "after retyping the edge")
		})
	}
}

func TestMaintenanceHoldsNothingUntilASchemaStatementArrives(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			m := startMaintainer(t, g)
			vec := []float32{0.1, 0.2, 0.3, 0.4}
			for i := 0; i < 20; i++ {
				if err := g.UpsertNode(ctx, &GraphNode{ID: fmt.Sprintf("x%d", i), Vector: vec, NodeType: "Thing",
					Properties: map[string]any{"name": fmt.Sprintf("x%d", i)}}); err != nil {
					t.Fatal(err)
				}
				if err := g.UpsertTriple(ctx, exTriple(fmt.Sprintf("a%d", i), "p", "b")); err != nil {
					t.Fatal(err)
				}
			}
			waitInference(t, g)
			if s := m.Stats(); s.Active || s.Holding || s.Upserted != 0 || s.Baselines != 0 {
				t.Fatalf("without vocabulary the maintainer should hold nothing, not even the lease: %+v", s)
			}
			schema := &RDFTriple{Subject: NewIRI(PropertyTypeNamespace + "Thing"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: exIRI("Entity")}
			if err := g.UpsertTriple(ctx, schema); err != nil {
				t.Fatal(err)
			}
			waitInference(t, g)
			if s := m.Stats(); !s.Active || !s.Holding || s.Upserted < 20 {
				t.Fatalf("a schema statement should activate maintenance: %+v", s)
			}
			assertInferencesMatchRecompute(t, g, "after the schema arrived")
			if err := g.DeleteTriple(ctx, *schema); err != nil {
				t.Fatal(err)
			}
			waitInference(t, g)
			if s := m.Stats(); s.Active || s.Holding {
				t.Fatalf("with the schema gone the maintainer should let go: %+v", s)
			}
			assertInferencesMatchRecompute(t, g, "after the schema left")
		})
	}
}

func TestOneMaintainerWorksAtATimeAndAnotherTakesOverWhenItStops(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			first := startMaintainer(t, g)
			waitInference(t, g)
			second, err := g.StartInferenceMaintenance(ctx, InferenceMaintenanceConfig{PollInterval: 5 * time.Millisecond, Logf: t.Logf})
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			if err := g.UpsertTriple(ctx, &RDFTriple{Subject: exIRI("A"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: exIRI("B")}); err != nil {
				t.Fatal(err)
			}
			waitInference(t, g)
			if first.Stats().Holding == second.Stats().Holding {
				t.Fatalf("exactly one maintainer should hold the lease: %+v / %+v", first.Stats(), second.Stats())
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			if err := g.UpsertTriple(ctx, &RDFTriple{Subject: exIRI("x"), Predicate: NewIRI(rdfTypeIRI), Object: exIRI("A")}); err != nil {
				t.Fatal(err)
			}
			waitInference(t, g)
			if !second.Stats().Holding {
				t.Fatalf("the second maintainer should have taken over: %+v", second.Stats())
			}
			assertInferencesMatchRecompute(t, g, "after the takeover")
		})
	}
}

// The incremental refresh deleted every inference that touched its
// neighbourhood but recomputed only the neighbourhood's own component, so an
// inference whose only link to the neighbourhood was a vocabulary constant
// such as rdfs:Class — "A is a class", derived elsewhere — was deleted and
// never put back.
func TestAnIncrementalRefreshKeepsInferencesThatOnlyShareAVocabularyTerm(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	sub := NewIRI(rdfsSubClassOfIRI)
	if err := g.UpsertTriple(ctx, &RDFTriple{Subject: exIRI("A"), Predicate: sub, Object: exIRI("B")}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RefreshRDFSInferences(ctx); err != nil {
		t.Fatal(err)
	}
	seed := RDFTriple{Subject: exIRI("X"), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI(rdfsClassIRI)}
	if err := g.UpsertTriple(ctx, &seed); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RefreshRDFSInferencesIncremental(ctx, []RDFTriple{seed}); err != nil {
		t.Fatal(err)
	}
	assertInferencesMatchRecompute(t, g, "after an incremental refresh seeded with X a rdfs:Class")
}

// Per-write overhead of automatic maintenance on a store of about 20,000
// triples with a schema, measured end to end: the write, then waiting until
// inference has caught up with it. Opt-in, since it is a measurement.
func TestMaintenanceOverheadOnALargeStoreWithASchema(t *testing.T) {
	if os.Getenv("CORTEXDB_INFERENCE_BENCH") == "" {
		t.Skip("set CORTEXDB_INFERENCE_BENCH=1 to measure")
	}
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			typ, sub := NewIRI(rdfTypeIRI), NewIRI(rdfsSubClassOfIRI)
			batch := make([]*RDFTriple, 0, 20000)
			for c := 0; c < 50; c++ {
				batch = append(batch, &RDFTriple{Subject: exIRI(fmt.Sprintf("C%d", c)), Predicate: sub, Object: exIRI(fmt.Sprintf("C%d", c/5))})
			}
			for p := 0; p < 10; p++ {
				batch = append(batch, &RDFTriple{Subject: exIRI(fmt.Sprintf("p%d", p)), Predicate: NewIRI(rdfsDomainIRI), Object: exIRI(fmt.Sprintf("C%d", p))})
			}
			for len(batch) < 20000 {
				i := len(batch)
				if i%2 == 0 {
					batch = append(batch, &RDFTriple{Subject: exIRI(fmt.Sprintf("i%d", i)), Predicate: typ, Object: exIRI(fmt.Sprintf("C%d", i%50))})
				} else {
					batch = append(batch, &RDFTriple{Subject: exIRI(fmt.Sprintf("i%d", i-1)), Predicate: exIRI(fmt.Sprintf("p%d", i%10)), Object: exIRI(fmt.Sprintf("i%d", (i*7)%20000))})
				}
			}
			for start := 0; start < len(batch); start += 2000 {
				end := min(start+2000, len(batch))
				if _, err := g.UpsertTriplesBatch(ctx, batch[start:end]); err != nil {
					t.Fatal(err)
				}
			}
			measure := func(label string, wait bool) []time.Duration {
				out := make([]time.Duration, 0, 200)
				for i := 0; i < 200; i++ {
					tr := &RDFTriple{Subject: exIRI(fmt.Sprintf("%s-new%d", label, i)), Predicate: typ, Object: exIRI(fmt.Sprintf("C%d", 10+i%40))}
					if i%2 == 1 {
						prev := RDFTriple{Subject: exIRI(fmt.Sprintf("%s-new%d", label, i-1)), Predicate: typ, Object: exIRI(fmt.Sprintf("C%d", 10+(i-1)%40))}
						start := time.Now()
						if err := g.DeleteTriple(ctx, prev); err != nil {
							t.Fatal(err)
						}
						if wait {
							waitInference(t, g)
						}
						out = append(out, time.Since(start))
						continue
					}
					start := time.Now()
					if err := g.UpsertTriple(ctx, tr); err != nil {
						t.Fatal(err)
					}
					if wait {
						waitInference(t, g)
					}
					out = append(out, time.Since(start))
				}
				sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
				return out
			}
			plain := measure("plain", false)
			m := startMaintainer(t, g)
			waitInference(t, g)
			maintained := measure("maintained", true)
			p := func(d []time.Duration, q float64) time.Duration { return d[int(float64(len(d)-1)*q)] }
			t.Logf("write alone p50=%v p95=%v | write+inference caught up p50=%v p95=%v | overhead p50=%v p95=%v | stats %+v",
				p(plain, .5), p(plain, .95), p(maintained, .5), p(maintained, .95),
				p(maintained, .5)-p(plain, .5), p(maintained, .95)-p(plain, .95), m.Stats())
			assertInferencesMatchRecompute(t, g, "after the measurement")
		})
	}
}

// SHACL rules store their conclusions as inferred triples too. The maintainer
// did not derive them and must leave them alone — through a baseline, through
// DRed, and through the cleanup a store with no schema gets.
func TestMaintenanceLeavesTriplesASHACLRuleInferredAlone(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			shaclRow := func(id string) {
				if _, err := g.exec(ctx, `INSERT INTO kg_triples (id, subject_kind, subject_value, predicate_value, object_kind, object_value, inferred, inference_rule)
					VALUES (?, 'iri', 'http://ex.test/s', 'http://ex.test/derived', 'iri', ?, 1, ?)`,
					id, "http://ex.test/"+id, SHACLTripleRuleNamePrefix+"http://ex.test/rule"); err != nil {
					t.Fatal(err)
				}
			}
			shaclRow("before")
			m := startMaintainer(t, g)
			waitInference(t, g)
			if err := g.UpsertTriple(ctx, &RDFTriple{Subject: exIRI("A"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: exIRI("B")}); err != nil {
				t.Fatal(err)
			}
			shaclRow("during")
			if err := g.UpsertTriple(ctx, &RDFTriple{Subject: exIRI("x"), Predicate: NewIRI(rdfTypeIRI), Object: exIRI("A")}); err != nil {
				t.Fatal(err)
			}
			if err := g.DeleteTriple(ctx, RDFTriple{Subject: exIRI("A"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: exIRI("B")}); err != nil {
				t.Fatal(err)
			}
			waitInference(t, g)
			assertMaintainedMatchesRecompute(t, g, m, "after the schema came and went")
			var n int
			if err := g.queryRow(ctx, `SELECT COUNT(*) FROM kg_triples WHERE inference_rule LIKE ?`, SHACLTripleRuleNamePrefix+"%").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 2 {
				t.Fatalf("the maintainer removed SHACL rule triples it did not derive: %d of 2 left", n)
			}
		})
	}
}
