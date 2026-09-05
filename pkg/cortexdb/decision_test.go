package cortexdb

// The ledger, on both backends.
//
// Everything here goes through the graph API rather than SQL of its own, which
// is the claim these tests exist to hold: the same body of code has to answer
// the same way on SQLite and on PostgreSQL. PostgreSQL is opt-in through
// CORTEXDB_TEST_POSTGRES and skipped loudly, so a green laptop run is never
// mistaken for coverage it does not have.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// decisionBackends gives each test a brain per backend.
func decisionBackends(t *testing.T) []struct {
	name string
	open func(t *testing.T) *DB
} {
	t.Helper()
	return []struct {
		name string
		open func(t *testing.T) *DB
	}{
		{name: "sqlite", open: func(t *testing.T) *DB {
			t.Helper()
			db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "ledger.db")))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		}},
		// openPostgresBrain skips loudly when CORTEXDB_TEST_POSTGRES is unset,
		// so a green laptop run says in its output that half of this suite did
		// not happen.
		{name: "postgres", open: func(t *testing.T) *DB {
			t.Helper()
			return openPostgresBrain(t, 4)
		}},
	}
}

// seedLedgerGraph writes the facts a decision can rest on: two service nodes
// and the relation between them, which is an edge and therefore the premise
// shape that had to be designed rather than derived.
func seedLedgerGraph(t *testing.T, db *DB) (factEdgeID string) {
	t.Helper()
	ctx := context.Background()
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, n := range []*graph.GraphNode{
		{ID: "svc:ledger", Vector: []float32{1, 0, 0, 0}, NodeType: "Service", Content: "ledger-svc",
			Properties: map[string]any{KeyGrade: GradeVerified, KeySource: "runbook"}},
		{ID: "svc:riskd", Vector: []float32{0, 1, 0, 0}, NodeType: "Service", Content: "riskd",
			Properties: map[string]any{KeyGrade: GradeAsserted, KeySource: "runbook"}},
		// A node sitting in the decision namespace that is not a decision.
		// The ledger has to tell them apart by node_type, not by id shape.
		{ID: "decision:impostor", Vector: []float32{0, 0, 1, 0}, NodeType: "Service", Content: "impostor"},
	} {
		if err := db.graph.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}
	factEdgeID = "fact:ledger-depends-on-riskd"
	if err := db.graph.UpsertEdge(ctx, &graph.GraphEdge{
		ID: factEdgeID, FromNodeID: "svc:ledger", ToNodeID: "svc:riskd",
		EdgeType: "DEPENDS_ON", Weight: 1,
		Properties: map[string]any{KeyGrade: GradeAsserted, KeySource: "runbook"},
	}); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	return factEdgeID
}

// TestAChainCarriesItsPremisesAndTheirGrades is the point of the feature: a
// decision that can say what it rested on, and how sure each of those things
// was. A chain that lists premises without their grades looks like evidence
// and is not.
func TestAChainCarriesItsPremisesAndTheirGrades(t *testing.T) {
	for _, b := range decisionBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			db := b.open(t)
			ctx := context.Background()
			factEdge := seedLedgerGraph(t, db)

			rec, err := db.RecordDecision(ctx, DecisionRecordRequest{
				ID:       "hold-2026-03",
				Kind:     DecisionKindReview,
				Actor:    "liliang",
				Note:     "Held the ledger-svc release: riskd's rule source is unverified.",
				Verdict:  "hold",
				Subject:  "svc:ledger",
				Premises: []string{"svc:riskd", factEdge},
				At:       time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatalf("RecordDecision: %v", err)
			}
			if rec.ID != "decision:hold-2026-03" {
				t.Errorf("id = %q, want the prefixed form", rec.ID)
			}

			chain, err := db.DecisionChain(ctx, "hold-2026-03", 0)
			if err != nil {
				t.Fatalf("DecisionChain: %v", err)
			}
			if len(chain.Decisions) != 1 {
				t.Fatalf("chain has %d decisions, want 1: %+v", len(chain.Decisions), chain.Decisions)
			}
			got := chain.Decisions[0]
			if got.Actor != "liliang" || got.Verdict != "hold" || got.Subject != "svc:ledger" {
				t.Errorf("decision read back wrong: %+v", got)
			}
			if got.Note == "" {
				t.Error("the note did not survive the round trip")
			}

			byID := map[string]DecisionPremise{}
			for _, p := range got.Premises {
				byID[p.ID] = p
			}
			if len(byID) != 2 {
				t.Fatalf("premises = %+v, want two", got.Premises)
			}
			node := byID["svc:riskd"]
			if node.Edge || node.Grade != GradeAsserted || node.Source != "runbook" {
				t.Errorf("node premise did not carry its contract keys: %+v", node)
			}
			// The premise that is a fact. Its grade must be the *edge's*, not
			// the grade of the node the based_on edge had to be anchored on —
			// svc:ledger is verified and the fact is only asserted, so reading
			// the wrong one is visible here rather than plausible.
			fact := byID[factEdge]
			if !fact.Edge {
				t.Fatalf("the fact premise came back as a node: %+v", fact)
			}
			if fact.Grade != GradeAsserted || fact.Type != "DEPENDS_ON" {
				t.Errorf("fact premise = %+v, want the edge's own grade and type", fact)
			}
			if fact.From != "svc:ledger" || fact.To != "svc:riskd" {
				t.Errorf("fact premise ends = %s -> %s", fact.From, fact.To)
			}
		})
	}
}

// TestTheChainWalksSupersedes: a decision that replaced another is only half
// an account without the one it replaced.
func TestTheChainWalksSupersedes(t *testing.T) {
	for _, b := range decisionBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			db := b.open(t)
			ctx := context.Background()
			seedLedgerGraph(t, db)

			base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
			if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
				ID: "d1", Kind: DecisionKindReview, Actor: "liliang",
				Note: "Held the release.", Premises: []string{"svc:riskd"}, At: base,
			}); err != nil {
				t.Fatalf("d1: %v", err)
			}
			if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
				ID: "d2", Kind: DecisionKindReview, Actor: "liliang",
				Note: "Shipped it after riskd was re-measured.", Supersedes: "d1",
				At: base.Add(48 * time.Hour),
			}); err != nil {
				t.Fatalf("d2: %v", err)
			}

			chain, err := db.DecisionChain(ctx, "d2", 0)
			if err != nil {
				t.Fatalf("DecisionChain: %v", err)
			}
			if len(chain.Decisions) != 2 {
				t.Fatalf("chain = %d decisions, want 2", len(chain.Decisions))
			}
			if chain.Decisions[0].ID != "decision:d2" || chain.Decisions[1].ID != "decision:d1" {
				t.Errorf("chain order = %s, %s; want the root first",
					chain.Decisions[0].ID, chain.Decisions[1].ID)
			}
			if chain.Depth != 1 {
				t.Errorf("depth = %d, want 1", chain.Depth)
			}
			// The superseded decision's own premises came with it.
			if len(chain.Decisions[1].Premises) != 1 {
				t.Errorf("the superseded decision lost its premises: %+v", chain.Decisions[1])
			}

			if chain.Truncated {
				t.Error("a chain that fits reported itself truncated")
			}

			// A third link, and a bound that cannot reach it. The chain has to
			// say it stopped: a shorter chain handed back silently reads as a
			// complete account of why something was done.
			if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
				ID: "d3", Kind: DecisionKindReview, Actor: "liliang",
				Note: "Reverted after the incident.", Supersedes: "d2",
				At: base.Add(96 * time.Hour),
			}); err != nil {
				t.Fatalf("d3: %v", err)
			}
			shallow, err := db.DecisionChain(ctx, "d3", 1)
			if err != nil {
				t.Fatalf("DecisionChain(depth 1): %v", err)
			}
			if len(shallow.Decisions) != 2 {
				t.Errorf("depth 1 = %d decisions, want d3 and d2", len(shallow.Decisions))
			}
			if !shallow.Truncated {
				t.Error("the bound stopped the walk with d1 unvisited and the chain did not say so")
			}
			full, err := db.DecisionChain(ctx, "d3", 5)
			if err != nil {
				t.Fatalf("DecisionChain(depth 5): %v", err)
			}
			if len(full.Decisions) != 3 || full.Truncated {
				t.Errorf("depth 5 = %d decisions truncated=%v, want 3/false", len(full.Decisions), full.Truncated)
			}
		})
	}
}

// TestAChainThroughADecisionPremiseRecurses: a decision resting on a decision
// is how a line of reasoning gets recorded, and it is what makes the walk a
// walk rather than a single lookup.
func TestAChainThroughADecisionPremiseRecurses(t *testing.T) {
	db := decisionBackends(t)[0].open(t)
	ctx := context.Background()
	seedLedgerGraph(t, db)

	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
		ID: "policy", Kind: DecisionKindAssert, Actor: "liliang",
		Note: "Unverified rule sources block a release.", At: base,
	}); err != nil {
		t.Fatalf("policy: %v", err)
	}
	if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
		ID: "hold", Kind: DecisionKindReview, Actor: "liliang",
		Note: "Held the release under the policy.", Premises: []string{"decision:policy", "svc:riskd"},
		At: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("hold: %v", err)
	}

	chain, err := db.DecisionChain(ctx, "hold", 0)
	if err != nil {
		t.Fatalf("DecisionChain: %v", err)
	}
	if len(chain.Decisions) != 2 {
		t.Fatalf("chain = %d decisions, want 2: %+v", len(chain.Decisions), chain.Decisions)
	}
	var sawDecisionPremise bool
	for _, p := range chain.Decisions[0].Premises {
		if p.ID == "decision:policy" {
			sawDecisionPremise = p.Decision
		}
	}
	if !sawDecisionPremise {
		t.Error("the premise that is a decision was not flagged as one, so nothing would recurse")
	}

	// One hop is enough to reach a premise that is a decision, which is the
	// difference between recursing through premises and only through
	// supersedes.
	shallow, err := db.DecisionChain(ctx, "hold", 1)
	if err != nil {
		t.Fatalf("DecisionChain(depth 1): %v", err)
	}
	if len(shallow.Decisions) != 2 {
		t.Errorf("depth 1 = %d decisions, want 2", len(shallow.Decisions))
	}
}

// TestACycleTerminates. A ledger should not contain one; a graph anybody can
// write into eventually does, and a walk that hangs on it is worse than any
// wrong answer.
func TestACycleTerminates(t *testing.T) {
	db := decisionBackends(t)[0].open(t)
	ctx := context.Background()
	seedLedgerGraph(t, db)

	if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
		ID: "a", Actor: "liliang", Note: "A.",
	}); err != nil {
		t.Fatalf("a: %v", err)
	}
	if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
		ID: "b", Actor: "liliang", Note: "B.", Supersedes: "a",
	}); err != nil {
		t.Fatalf("b: %v", err)
	}
	// Close the loop by re-recording a as superseding b. Only possible because
	// the ids are the caller's, which is exactly how it happens in the field.
	if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
		ID: "a", Actor: "liliang", Note: "A.", Supersedes: "b",
	}); err != nil {
		t.Fatalf("a again: %v", err)
	}

	done := make(chan DecisionChain, 1)
	go func() {
		chain, err := db.DecisionChain(ctx, "a", 32)
		if err != nil {
			t.Errorf("DecisionChain: %v", err)
		}
		done <- chain
	}()
	select {
	case chain := <-done:
		if len(chain.Decisions) != 2 {
			t.Errorf("a two-decision cycle produced %d entries", len(chain.Decisions))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DecisionChain did not terminate on a cycle")
	}
}

// TestARefusedDecisionWritesNothing. Fail closed, and fail before writing: a
// decision that half-wrote leaves an entry with fewer premises than the caller
// believes it has, and nothing downstream can tell that from a decision that
// genuinely rested on less.
func TestARefusedDecisionWritesNothing(t *testing.T) {
	for _, b := range decisionBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			db := b.open(t)
			ctx := context.Background()
			seedLedgerGraph(t, db)

			cases := []struct {
				name string
				req  DecisionRecordRequest
				want string
			}{
				{
					name: "empty actor",
					req:  DecisionRecordRequest{ID: "x", Note: "something", Premises: []string{"svc:riskd"}},
					want: "actor is required",
				},
				{
					name: "blank actor",
					req:  DecisionRecordRequest{ID: "x", Actor: "   ", Note: "something"},
					want: "actor is required",
				},
				{
					name: "empty note",
					req:  DecisionRecordRequest{ID: "x", Actor: "liliang"},
					want: "note is required",
				},
				{
					name: "unknown premise",
					req: DecisionRecordRequest{ID: "x", Actor: "liliang", Note: "n",
						Premises: []string{"svc:riskd", "svc:ghost"}},
					want: "neither a node nor an edge",
				},
				{
					name: "unknown subject",
					req:  DecisionRecordRequest{ID: "x", Actor: "liliang", Note: "n", Subject: "svc:ghost"},
					want: "is not a node in this graph",
				},
				{
					name: "unknown supersedes",
					req:  DecisionRecordRequest{ID: "x", Actor: "liliang", Note: "n", Supersedes: "nope"},
					want: "does not exist",
				},
				{
					// A node does sit at decision:impostor — it is just not a
					// decision. Without the type check this would link the
					// ledger to an entity and the chain would read it back as
					// an entry with no actor and no note.
					name: "supersedes a node that is not a decision",
					req:  DecisionRecordRequest{ID: "x", Actor: "liliang", Note: "n", Supersedes: "impostor"},
					want: "not a decision",
				},
				{
					name: "held without a reason fails the contract",
					req:  DecisionRecordRequest{ID: "x", Actor: "liliang", Note: "n", Grade: GradeHeld},
					want: "knowledge contract",
				},
				{
					name: "a source carrying a credential fails the contract",
					req: DecisionRecordRequest{ID: "x", Actor: "liliang", Note: "n",
						Source: "postgres://bob:hunter2@db/prod"},
					want: "knowledge contract",
				},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					_, err := db.RecordDecision(ctx, c.req)
					if err == nil {
						t.Fatalf("RecordDecision accepted %s", c.name)
					}
					if !strings.Contains(err.Error(), c.want) {
						t.Errorf("error = %v, want it to mention %q", err, c.want)
					}
					// Nothing written: the node the request named is absent.
					if _, err := db.graph.GetNode(ctx, "decision:x"); err == nil {
						t.Fatalf("%s was refused but decision:x exists", c.name)
					}
				})
			}
		})
	}
}

// TestPrecedentsByKindAndSubject: ordered newest first, bounded, and refusing
// to hand back the whole ledger when asked for nothing in particular.
func TestPrecedentsByKindAndSubject(t *testing.T) {
	for _, b := range decisionBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			db := b.open(t)
			ctx := context.Background()
			seedLedgerGraph(t, db)

			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			write := func(id, kind, subject string, day int) {
				t.Helper()
				if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
					ID: id, Kind: kind, Actor: "liliang", Subject: subject,
					Note: "decision " + id, At: base.AddDate(0, 0, day),
				}); err != nil {
					t.Fatalf("record %s: %v", id, err)
				}
			}
			write("r1", DecisionKindReview, "svc:ledger", 1)
			write("r2", DecisionKindReview, "svc:riskd", 2)
			write("r3", DecisionKindReview, "svc:ledger", 3)
			write("l1", DecisionKindLoad, "svc:ledger", 4)

			byKind, err := db.Precedents(ctx, PrecedentsQuery{Kind: DecisionKindReview})
			if err != nil {
				t.Fatalf("Precedents by kind: %v", err)
			}
			if ids := decisionIDs(byKind); strings.Join(ids, ",") != "decision:r3,decision:r2,decision:r1" {
				t.Errorf("by kind = %v, want r3, r2, r1 newest first", ids)
			}

			bySubject, err := db.Precedents(ctx, PrecedentsQuery{Subject: "svc:ledger"})
			if err != nil {
				t.Fatalf("Precedents by subject: %v", err)
			}
			if ids := decisionIDs(bySubject); strings.Join(ids, ",") != "decision:l1,decision:r3,decision:r1" {
				t.Errorf("by subject = %v, want l1, r3, r1", ids)
			}

			both, err := db.Precedents(ctx, PrecedentsQuery{Kind: DecisionKindReview, Subject: "svc:ledger"})
			if err != nil {
				t.Fatalf("Precedents by both: %v", err)
			}
			if ids := decisionIDs(both); strings.Join(ids, ",") != "decision:r3,decision:r1" {
				t.Errorf("by kind and subject = %v", ids)
			}

			// Bounded, and bounded *after* the ordering: a cap applied by the
			// query would return whichever ids sorted first and call them the
			// newest.
			capped, err := db.Precedents(ctx, PrecedentsQuery{Kind: DecisionKindReview, Limit: 2})
			if err != nil {
				t.Fatalf("Precedents limited: %v", err)
			}
			if ids := decisionIDs(capped); strings.Join(ids, ",") != "decision:r3,decision:r2" {
				t.Errorf("limited = %v, want the two newest", ids)
			}

			// The decision being taken now is not its own precedent.
			excluded, err := db.Precedents(ctx, PrecedentsQuery{Kind: DecisionKindReview, Exclude: "r3"})
			if err != nil {
				t.Fatalf("Precedents excluding: %v", err)
			}
			for _, r := range excluded {
				if r.ID == "decision:r3" {
					t.Error("exclude did not exclude")
				}
			}

			if _, err := db.Precedents(ctx, PrecedentsQuery{}); err == nil {
				t.Error("an empty precedents query returned the whole ledger instead of being refused")
			}

			by, err := db.DecisionsBy(ctx, "liliang", 10)
			if err != nil {
				t.Fatalf("DecisionsBy: %v", err)
			}
			if len(by) != 4 {
				t.Errorf("DecisionsBy = %d, want 4", len(by))
			}
			if len(by) > 0 && by[0].ID != "decision:l1" {
				t.Errorf("DecisionsBy[0] = %s, want the newest", by[0].ID)
			}
			if _, err := db.DecisionsBy(ctx, "", 10); err == nil {
				t.Error("DecisionsBy accepted an empty actor")
			}
		})
	}
}

// TestARecordedDecisionShowsInTheContractTally. The reason a decision is a
// graph record and not a side table: every reader the contract already has
// sees it, without being told about decisions.
func TestARecordedDecisionShowsInTheContractTally(t *testing.T) {
	for _, b := range decisionBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			db := b.open(t)
			ctx := context.Background()
			seedLedgerGraph(t, db)

			before, err := db.ContractTally(ctx)
			if err != nil {
				t.Fatalf("ContractTally: %v", err)
			}
			if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
				ID: "d", Kind: DecisionKindReview, Actor: "liliang",
				Note: "Held it.", Subject: "svc:ledger", Premises: []string{"svc:riskd"},
			}); err != nil {
				t.Fatalf("RecordDecision: %v", err)
			}
			after, err := db.ContractTally(ctx)
			if err != nil {
				t.Fatalf("ContractTally: %v", err)
			}
			if got := after.Verified.Nodes - before.Verified.Nodes; got != 1 {
				t.Errorf("verified nodes grew by %d, want 1 — the decision itself", got)
			}
			// based_on and about. Both are assertions the same actor signed,
			// so both are graded; leaving them untagged would make the tally
			// report a ledger far less established than it is.
			if got := after.Verified.Edges - before.Verified.Edges; got != 2 {
				t.Errorf("verified edges grew by %d, want 2", got)
			}

			graded, err := db.GradedRecords(ctx, GradedQuery{Grades: []string{GradeVerified}})
			if err != nil {
				t.Fatalf("GradedRecords: %v", err)
			}
			var found bool
			for _, r := range graded {
				if r.ID == "decision:d" {
					found = true
					if r.Producer != ProducerHuman || r.By != "liliang" {
						t.Errorf("the decision's contract keys are wrong: %+v", r)
					}
					if r.Source != defaultDecisionSource {
						t.Errorf("source = %q, want the ledger's default", r.Source)
					}
				}
			}
			if !found {
				t.Error("the decision is not among the verified records")
			}
		})
	}
}

// TestRecordingIsIdempotentUnderAGivenID. Replaying a transcript must not
// double the ledger.
func TestRecordingIsIdempotentUnderAGivenID(t *testing.T) {
	db := decisionBackends(t)[0].open(t)
	ctx := context.Background()
	seedLedgerGraph(t, db)

	req := DecisionRecordRequest{
		ID: "same", Kind: DecisionKindLoad, Actor: "liliang", Note: "Loaded the March extract.",
		Subject: "svc:ledger", Premises: []string{"svc:riskd"},
		At: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
	}
	for range 3 {
		if _, err := db.RecordDecision(ctx, req); err != nil {
			t.Fatalf("RecordDecision: %v", err)
		}
	}
	recs, err := db.DecisionsBy(ctx, "liliang", 10)
	if err != nil {
		t.Fatalf("DecisionsBy: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("three identical records produced %d entries", len(recs))
	}
	chain, err := db.DecisionChain(ctx, "same", 0)
	if err != nil {
		t.Fatalf("DecisionChain: %v", err)
	}
	if len(chain.Decisions[0].Premises) != 1 {
		t.Errorf("premises = %+v, want one", chain.Decisions[0].Premises)
	}
}

// TestAPremiseDeletedAfterTheFactIsReportedNotHidden. RecordDecision refuses an
// unknown premise, so a missing one at read time means the shelf changed under
// a decision that already stands — which the reader has to be told rather than
// shown a gap.
func TestAPremiseDeletedAfterTheFactIsReportedNotHidden(t *testing.T) {
	db := decisionBackends(t)[0].open(t)
	ctx := context.Background()
	factEdge := seedLedgerGraph(t, db)

	if _, err := db.RecordDecision(ctx, DecisionRecordRequest{
		ID: "d", Actor: "liliang", Note: "Held it.", Premises: []string{factEdge},
	}); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if err := db.graph.DeleteEdge(ctx, factEdge); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	chain, err := db.DecisionChain(ctx, "d", 0)
	if err != nil {
		t.Fatalf("DecisionChain: %v", err)
	}
	prem := chain.Decisions[0].Premises
	if len(prem) != 1 || !prem[0].Missing || prem[0].ID != factEdge {
		t.Errorf("premises = %+v, want the deleted fact reported as missing", prem)
	}
}

// TestTheDecisionToolsRoundTripThroughTheToolbox — the surface an agent
// actually reaches. Definitions() and Call are two hand-kept lists, and a tool
// defined in one and absent from the other builds, tests and ships.
func TestTheDecisionToolsRoundTripThroughTheToolbox(t *testing.T) {
	db := decisionBackends(t)[0].open(t)
	ctx := context.Background()
	seedLedgerGraph(t, db)
	tools := db.GraphRAGTools()

	call := func(name string, req any) any {
		t.Helper()
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		out, err := tools.Call(ctx, name, raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return out
	}

	rec := call("decision_record", DecisionRecordToolRequest{
		ID: "t1", Kind: DecisionKindAction, Actor: "liliang", Note: "Rolled back.",
		Subject: "svc:ledger", Premises: []string{"svc:riskd"}, At: "2026-03-01T09:00:00Z",
	}).(DecisionRecordToolResponse)
	if rec.Decision.ID != "decision:t1" {
		t.Fatalf("decision_record returned %+v", rec)
	}

	chain := call("decision_chain", DecisionChainToolRequest{ID: "t1"}).(DecisionChain)
	if len(chain.Decisions) != 1 || len(chain.Decisions[0].Premises) != 1 {
		t.Errorf("decision_chain returned %+v", chain)
	}

	prec := call("decision_precedents", DecisionPrecedentsToolRequest{Kind: DecisionKindAction}).(DecisionPrecedentsToolResponse)
	if prec.Count != 1 || prec.Truncated {
		t.Errorf("decision_precedents returned %+v", prec)
	}

	// A bad timestamp is refused rather than silently becoming now.
	raw, _ := json.Marshal(DecisionRecordToolRequest{Actor: "a", Note: "n", At: "March"})
	if _, err := tools.Call(ctx, "decision_record", raw); err == nil {
		t.Error("decision_record accepted a timestamp that is not RFC 3339")
	}
}

func decisionIDs(recs []DecisionRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.ID)
	}
	return out
}
