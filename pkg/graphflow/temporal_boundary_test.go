package graphflow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// The as-of filter moved from Go into SQL. These pin the semantics it had, so
// the move is a change of where the work happens and not of what the answer is.
//
// The interval is half-open, [valid_from, valid_to): a fact starting exactly
// at the queried instant is current, one ending exactly at it is not. That is
// what the Go loop did, and off-by-one at a boundary is precisely the kind of
// change a rewrite makes silently.
func TestAsOfBoundariesAreHalfOpen(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "temporal.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	start := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Leo", To: "Chengdu", Type: "lives_in",
		ValidFrom: &start, ValidTo: &end,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"the instant it starts", start, true},
		{"a moment after it starts", start.Add(time.Second), true},
		{"halfway through", start.Add(15 * 24 * time.Hour), true},
		{"a moment before it ends", end.Add(-time.Second), true},
		{"the instant it ends", end, false},
		{"after it ends", end.Add(time.Second), false},
		{"before it starts", start.Add(-time.Second), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts, err := QueryFactsAsOf(ctx, db, tc.at, TemporalFilter{From: "Leo"})
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if got := len(facts) > 0; got != tc.want {
				t.Errorf("at %s: found=%v, want %v", tc.at.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

// An open fact — no valid_to — is current at every instant from its start.
func TestAnOpenFactStaysCurrent(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "temporal.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Leo", To: "LINBIT", Type: "works_at", ValidFrom: &start,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	for _, at := range []time.Time{start, start.AddDate(0, 6, 0), start.AddDate(10, 0, 0)} {
		facts, err := QueryFactsAsOf(ctx, db, at, TemporalFilter{From: "Leo"})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(facts) != 1 {
			t.Errorf("at %s: %d facts, want 1", at.Format(time.RFC3339), len(facts))
		}
	}
}

// Supersession makes a subject's history a chain of non-overlapping intervals,
// so a query for any instant returns exactly the value that held then — not
// two, and not none.
func TestSupersessionLeavesExactlyOneCurrentFact(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "temporal.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	first := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Leo", To: "Beijing", Type: "lives_in", ValidFrom: &first,
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Leo", To: "Chengdu", Type: "lives_in", ValidFrom: &second, Supersede: true,
	}); err != nil {
		t.Fatalf("second: %v", err)
	}

	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{first.AddDate(1, 0, 0), "Beijing"},
		{second.AddDate(1, 0, 0), "Chengdu"},
		{second, "Chengdu"}, // the handover instant belongs to the new fact
	} {
		facts, err := QueryFactsAsOf(ctx, db, tc.at, TemporalFilter{From: "Leo", Type: "lives_in"})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(facts) != 1 {
			got := make([]string, len(facts))
			for i, f := range facts {
				got[i] = f.To
			}
			t.Fatalf("at %s: %d facts %v, want exactly 1 (%s)", tc.at.Format(time.RFC3339), len(facts), got, tc.want)
		}
		if facts[0].To != tc.want {
			t.Errorf("at %s: %s, want %s", tc.at.Format(time.RFC3339), facts[0].To, tc.want)
		}
	}
}
