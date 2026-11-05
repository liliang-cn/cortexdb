package graph

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func histTriple(s, p, o string) *RDFTriple {
	return &RDFTriple{Subject: NewIRI("http://ex/" + s), Predicate: NewIRI("http://ex/" + p), Object: NewIRI("http://ex/" + o)}
}

func mustTriple(t *testing.T, g *GraphStore, ctx context.Context, s, p, o string) *RDFTriple {
	t.Helper()
	tr := histTriple(s, p, o)
	if err := g.UpsertTriple(ctx, tr); err != nil {
		t.Fatalf("upsert %s %s %s: %v", s, p, o, err)
	}
	return tr
}

func countTriples(t *testing.T, g *GraphStore, ctx context.Context, pattern TriplePattern) int {
	t.Helper()
	got, err := g.FindTriples(ctx, pattern)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	return len(got)
}

func historyRows(t *testing.T, g *GraphStore, ctx context.Context) int {
	t.Helper()
	var n int
	if err := g.queryRow(ctx, `SELECT COUNT(*) FROM kg_triples_history`).Scan(&n); err != nil {
		t.Fatalf("count history: %v", err)
	}
	return n
}

// A deleted triple is still there before the delete, and gone from then on.
func TestADeletedTripleIsReadableAsOfBeforeTheDelete(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			tr := mustTriple(t, g, ctx, "leo", "livesIn", "beijing")
			mustTriple(t, g, ctx, "leo", "knows", "ann")
			before := g.Now()
			if err := g.DeleteTriple(ctx, *tr); err != nil {
				t.Fatalf("delete: %v", err)
			}
			after := g.Now()
			pattern := TriplePattern{Predicate: &RDFTerm{Kind: RDFTermIRI, Value: "http://ex/livesIn"}}

			if n := countTriples(t, g, ctx, pattern); n != 0 {
				t.Errorf("current read still finds the deleted triple: %d", n)
			}
			if n := countTriples(t, g, AsOf(ctx, before), pattern); n != 1 {
				t.Errorf("as of before the delete: %d triples, want 1", n)
			}
			if n := countTriples(t, g, AsOf(ctx, after), pattern); n != 0 {
				t.Errorf("as of after the delete: %d triples, want 0", n)
			}
			if n := countTriples(t, g, AsOf(ctx, before), TriplePattern{}); n != 2 {
				t.Errorf("as of before, whole graph: %d triples, want 2", n)
			}
			if _, err := g.GetTriple(AsOf(ctx, before), tr.ID); err != nil {
				t.Errorf("GetTriple as of before: %v", err)
			}
			if _, err := g.GetTriple(ctx, tr.ID); err == nil {
				t.Errorf("GetTriple now should not find it")
			}
			var buf bytes.Buffer
			if err := g.ExportRDF(AsOf(ctx, before), &buf, RDFFormatNTriples); err != nil || !strings.Contains(buf.String(), "livesIn") {
				t.Errorf("export as of before lacks the triple: %v %q", err, buf.String())
			}
			buf.Reset()
			if err := g.ExportRDF(ctx, &buf, RDFFormatNTriples); err != nil || strings.Contains(buf.String(), "livesIn") {
				t.Errorf("current export has the deleted triple: %v %q", err, buf.String())
			}
		})
	}
}

// SPARQL reads the past, and an Update's deletes are recorded with a reason.
func TestSPARQLDeleteWhereGoesToHistoryAndIsReadableAsOf(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			mustTriple(t, g, ctx, "leo", "livesIn", "beijing")
			before := g.Now()
			res, err := g.ExecuteSPARQL(ctx, `DELETE WHERE { <http://ex/leo> <http://ex/livesIn> ?c }`)
			if err != nil || res.Count != 1 {
				t.Fatalf("delete where: %v %+v", err, res)
			}
			const q = `SELECT ?c WHERE { <http://ex/leo> <http://ex/livesIn> ?c }`
			now, err := g.ExecuteSPARQL(ctx, q)
			if err != nil || len(now.Bindings) != 0 {
				t.Fatalf("current select: %v %+v", err, now)
			}
			past, err := g.ExecuteSPARQL(AsOf(ctx, before), q)
			if err != nil || len(past.Bindings) != 1 {
				t.Fatalf("as-of select: %v %+v", err, past)
			}

			page, err := g.TripleHistory(ctx, TripleHistoryQuery{Subject: &RDFTerm{Kind: RDFTermIRI, Value: "http://ex/leo"}})
			if err != nil || len(page.Entries) != 1 {
				t.Fatalf("history: %v %+v", err, page)
			}
			e := page.Entries[0]
			if e.Status != "ended" || e.Reason != ReasonRetracted || e.Producer != ProducerSPARQLUpdate || e.RetractedAt.IsZero() {
				t.Errorf("history entry = %+v", e)
			}
		})
	}
}

// Reading the past is read-only, in every spelling of a write.
func TestWritesAreRefusedUnderAnAsOfContext(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			tr := mustTriple(t, g, ctx, "leo", "livesIn", "beijing")
			past := AsOf(ctx, g.Now())

			for _, update := range []string{
				`INSERT DATA { <http://ex/a> <http://ex/p> <http://ex/b> }`,
				`DELETE DATA { <http://ex/leo> <http://ex/livesIn> <http://ex/beijing> }`,
				`DELETE WHERE { ?s ?p ?o }`,
				`DELETE WHERE { <http://ex/nobody> ?p ?o }`, // matches nothing, still a write
				`CLEAR ALL`,
			} {
				if _, err := g.ExecuteSPARQL(past, update); err == nil || !strings.Contains(err.Error(), "as of") {
					t.Errorf("%q under as-of: err = %v, want refusal", update, err)
				}
			}
			if err := g.UpsertTriple(past, histTriple("a", "p", "b")); err == nil {
				t.Errorf("UpsertTriple under as-of succeeded")
			}
			if err := g.DeleteTriple(past, *tr); err == nil {
				t.Errorf("DeleteTriple under as-of succeeded")
			}
			if _, err := g.DeleteTriples(past, TriplePattern{}); err == nil {
				t.Errorf("DeleteTriples under as-of succeeded")
			}
			if n := countTriples(t, g, ctx, TriplePattern{}); n != 1 {
				t.Errorf("the graph changed under an as-of write: %d triples", n)
			}
		})
	}
}

// An inferred triple is in history with the rule that made it, so a past read
// reproduces what was inferred then.
func TestAnInferredTripleAnswersAsOf(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			sub := &RDFTriple{Subject: NewIRI("http://ex/Dog"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: NewIRI("http://ex/Animal")}
			if err := g.UpsertTriple(ctx, sub); err != nil {
				t.Fatal(err)
			}
			rex := &RDFTriple{Subject: NewIRI("http://ex/rex"), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI("http://ex/Dog")}
			if err := g.UpsertTriple(ctx, rex); err != nil {
				t.Fatal(err)
			}
			if _, err := g.RefreshRDFSInferences(ctx); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			const ask = `ASK { <http://ex/rex> a <http://ex/Animal> }`
			ok, _ := g.ExecuteSPARQL(ctx, ask)
			if !ok.Boolean {
				t.Fatalf("inference did not derive rex a Animal")
			}
			held := g.Now()

			// The support goes; inference is brought up to date.
			if err := g.DeleteTriple(ctx, *rex); err != nil {
				t.Fatal(err)
			}
			if _, err := g.RefreshRDFSInferencesIncremental(ctx, []RDFTriple{*rex}); err != nil {
				t.Fatalf("incremental refresh: %v", err)
			}
			if r, _ := g.ExecuteSPARQL(ctx, ask); r.Boolean {
				t.Fatalf("the inferred triple survived its support")
			}
			if r, err := g.ExecuteSPARQL(AsOf(ctx, held), ask); err != nil || !r.Boolean {
				t.Errorf("as of when it held: %v %+v, want true", err, r)
			}

			page, err := g.TripleHistory(ctx, TripleHistoryQuery{
				Subject: &RDFTerm{Kind: RDFTermIRI, Value: "http://ex/rex"},
				Object:  &RDFTerm{Kind: RDFTermIRI, Value: "http://ex/Animal"},
			})
			if err != nil || len(page.Entries) != 1 {
				t.Fatalf("history: %v %+v", err, page)
			}
			e := page.Entries[0]
			if !e.Triple.Inferred || e.Triple.Rule == "" || e.Reason != ReasonUnderived {
				t.Errorf("inferred history entry = %+v", e)
			}
		})
	}
}

// A refresh that re-derives what it cleared leaves no history, and the
// triples keep the instant they were first inferred.
func TestAnUnchangedInferenceRefreshWritesNoHistory(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			for _, tr := range []*RDFTriple{
				{Subject: NewIRI("http://ex/Dog"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: NewIRI("http://ex/Animal")},
				{Subject: NewIRI("http://ex/rex"), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI("http://ex/Dog")},
			} {
				if err := g.UpsertTriple(ctx, tr); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := g.RefreshRDFSInferences(ctx); err != nil {
				t.Fatal(err)
			}
			derived := g.Now()
			baseline := historyRows(t, g, ctx)
			inferred := true
			want := countTriples(t, g, ctx, TriplePattern{Inferred: &inferred})
			if want == 0 {
				t.Fatalf("nothing was inferred")
			}

			for i := 0; i < 3; i++ {
				if _, err := g.RefreshRDFSInferences(ctx); err != nil {
					t.Fatal(err)
				}
				rex := RDFTriple{Subject: NewIRI("http://ex/rex"), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI("http://ex/Dog")}
				if _, err := g.RefreshRDFSInferencesIncremental(ctx, []RDFTriple{rex}); err != nil {
					t.Fatal(err)
				}
			}
			if n := historyRows(t, g, ctx); n != baseline {
				t.Errorf("history grew from %d to %d rows over refreshes that changed nothing", baseline, n)
			}
			if n := countTriples(t, g, AsOf(ctx, derived), TriplePattern{Inferred: &inferred}); n != want {
				t.Errorf("as of the first derivation: %d inferred triples, want %d (recorded_at was not kept)", n, want)
			}
		})
	}
}

// One subject's versions read oldest first, paged, with why each ended.
func TestTripleHistoryListsEveryVersionOldestFirst(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			leo := &RDFTerm{Kind: RDFTermIRI, Value: "http://ex/leo"}
			tr := mustTriple(t, g, ctx, "leo", "livesIn", "beijing")
			t1 := g.Now()
			if err := g.DeleteTriple(WithInvalidation(ctx, Invalidation{Reason: "moved", Producer: "test"}), *tr); err != nil {
				t.Fatal(err)
			}
			t2 := g.Now()
			mustTriple(t, g, ctx, "leo", "livesIn", "beijing")
			mustTriple(t, g, ctx, "leo", "livesIn", "chengdu")
			mustTriple(t, g, ctx, "ann", "livesIn", "paris")

			page, err := g.TripleHistory(ctx, TripleHistoryQuery{Subject: leo})
			if err != nil || len(page.Entries) != 3 {
				t.Fatalf("history: %v %+v", err, page)
			}
			first := page.Entries[0]
			if first.Status != "ended" || first.Reason != "moved" || first.Producer != "test" {
				t.Errorf("first version = %+v", first)
			}
			for i := 1; i < len(page.Entries); i++ {
				if page.Entries[i].Status != "current" || page.Entries[i].RecordedAt.Before(page.Entries[i-1].RecordedAt) {
					t.Errorf("entry %d out of order or not current: %+v", i, page.Entries[i])
				}
			}

			// A predicate narrows; a window drops what ended before it.
			pred := &RDFTerm{Kind: RDFTermIRI, Value: "http://ex/livesIn"}
			if p, _ := g.TripleHistory(ctx, TripleHistoryQuery{Subject: leo, Predicate: pred, From: t2}); len(p.Entries) != 2 {
				t.Errorf("window from t2: %d entries, want 2 (the first ended before it)", len(p.Entries))
			}
			if p, _ := g.TripleHistory(ctx, TripleHistoryQuery{Subject: leo, To: t1}); len(p.Entries) != 1 {
				t.Errorf("window to t1: %d entries, want 1", len(p.Entries))
			}

			// Paging walks the same list.
			var ids []string
			cursor := ""
			for i := 0; i < 5; i++ {
				p, err := g.TripleHistory(ctx, TripleHistoryQuery{Subject: leo, Limit: 2, Cursor: cursor})
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range p.Entries {
					ids = append(ids, e.Triple.ID)
				}
				if p.NextCursor == "" {
					break
				}
				cursor = p.NextCursor
			}
			if len(ids) != 3 {
				t.Errorf("paged walk saw %d entries, want 3", len(ids))
			}
			if _, err := g.TripleHistory(ctx, TripleHistoryQuery{}); err == nil {
				t.Errorf("a history without a subject should be refused")
			}
		})
	}
}

// Purge reclaims triple history too, and a dry run only counts.
func TestPurgeRemovesTripleHistory(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			tr := mustTriple(t, g, ctx, "leo", "livesIn", "beijing")
			if err := g.DeleteTriple(ctx, *tr); err != nil {
				t.Fatal(err)
			}
			cutoff := g.Now().Add(time.Second)
			dry, err := g.Purge(ctx, cutoff, true)
			if err != nil || dry.Triples != 1 || historyRows(t, g, ctx) != 1 {
				t.Fatalf("dry run: %v %+v rows=%d", err, dry, historyRows(t, g, ctx))
			}
			rep, err := g.Purge(ctx, cutoff, false)
			if err != nil || rep.Triples != 1 {
				t.Fatalf("purge: %v %+v", err, rep)
			}
			if n := historyRows(t, g, ctx); n != 0 {
				t.Errorf("history rows after purge: %d", n)
			}
		})
	}
}

// A brain written before triple history has no recorded_at; its triples read as
// always having been there.
func TestTriplesWithoutARecordingInstantAreVisibleAtAnyInstant(t *testing.T) {
	ctx := context.Background()
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	mustTriple(t, g, ctx, "leo", "knows", "ann")
	if _, err := g.exec(ctx, `UPDATE kg_triples SET recorded_at = NULL`); err != nil {
		t.Fatal(err)
	}
	if n := countTriples(t, g, AsOf(ctx, time.Unix(1, 0)), TriplePattern{}); n != 1 {
		t.Errorf("a legacy triple was invisible long ago: %d", n)
	}
}

// Moving a triple to history does not change what the change feed says: one
// delete event for it, and none for the history row or the recording instant a
// refresh puts back.
func TestTheChangeFeedStillReportsATripleDeleteOnce(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			tr := exTriple("a", "p", "b")
			if err := g.UpsertTriple(ctx, tr); err != nil {
				t.Fatal(err)
			}
			head, err := g.ChangesHead(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := g.DeleteTriple(ctx, *tr); err != nil {
				t.Fatal(err)
			}
			events := allChanges(t, g, head)
			if len(events) != 1 || events[0].Kind != ChangeKindTriple || events[0].Op != ChangeOpDelete || events[0].ID != tr.ID {
				t.Fatalf("events after a triple delete: %v", summarize(events))
			}

			// A reconcile with nothing to settle writes no events either.
			head, _ = g.ChangesHead(ctx)
			if err := g.reconcileInferenceRefresh(ctx); err != nil {
				t.Fatal(err)
			}
			if events := allChanges(t, g, head); len(events) != 0 {
				t.Errorf("an empty reconcile produced events: %v", summarize(events))
			}
		})
	}
}
