package graphflow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// An ontology with one of each: a link a subject can only have once, and one
// it can have many of.
func brainWithCardinality(t *testing.T) *cortexdb.DB {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "onto.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	idProp := []cortexdb.OntologyProperty{{APIName: "id", Required: true, DataType: cortexdb.OntologyDataType{Kind: "string"}}}
	_, err = db.SaveOntologySchema(context.Background(), cortexdb.OntologySaveRequest{
		Activate: true,
		Schema: cortexdb.OntologySchema{
			SchemaID: "temporal", Name: "temporal",
			Enforcement: cortexdb.OntologyEnforcementVocabulary,
			ObjectTypes: []cortexdb.OntologyObjectType{
				{APIName: "Person", DisplayName: "Person", PrimaryKey: "id", Properties: idProp},
				{APIName: "City", DisplayName: "City", PrimaryKey: "id", Properties: idProp},
			},
			LinkTypes: []cortexdb.OntologyLinkType{
				{
					APIName: "lives_in",
					A:       cortexdb.OntologyLinkSide{APIName: "home", ObjectTypeAPIName: "Person", Cardinality: cortexdb.OntologyCardinalityOne},
					B:       cortexdb.OntologyLinkSide{APIName: "residents", ObjectTypeAPIName: "City", Cardinality: cortexdb.OntologyCardinalityMany},
				},
				{
					APIName: "knows",
					A:       cortexdb.OntologyLinkSide{APIName: "knows", ObjectTypeAPIName: "Person", Cardinality: cortexdb.OntologyCardinalityMany},
					B:       cortexdb.OntologyLinkSide{APIName: "known_by", ObjectTypeAPIName: "Person", Cardinality: cortexdb.OntologyCardinalityMany},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("save ontology: %v", err)
	}
	return db
}

// A person lives in one city, so a new home closes the old one — without the
// caller having to remember to say so. That forgetting is the gap this fills:
// two open facts both claiming to be true today, and nothing reporting it.
func TestASingleValuedLinkSupersedesItself(t *testing.T) {
	db := brainWithCardinality(t)
	ctx := context.Background()

	first := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Note: no Supersede flag on either call.
	for _, f := range []TemporalFact{
		{From: "Leo", To: "Beijing", Type: "lives_in", ValidFrom: &first},
		{From: "Leo", To: "Chengdu", Type: "lives_in", ValidFrom: &second},
	} {
		if err := SaveTemporalFact(ctx, db, f); err != nil {
			t.Fatalf("save %s: %v", f.To, err)
		}
	}

	facts, err := QueryFactsAsOf(ctx, db, second.AddDate(1, 0, 0), TemporalFilter{From: "Leo", Type: "lives_in"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(facts) != 1 {
		got := make([]string, len(facts))
		for i, f := range facts {
			got[i] = f.To
		}
		t.Fatalf("%d facts current at once: %v — a person lives in one city", len(facts), got)
	}
	if facts[0].To != "Chengdu" {
		t.Errorf("current home is %s, want Chengdu", facts[0].To)
	}

	// The old fact is closed, not deleted: history still answers.
	past, err := QueryFactsAsOf(ctx, db, first.AddDate(1, 0, 0), TemporalFilter{From: "Leo", Type: "lives_in"})
	if err != nil {
		t.Fatalf("query past: %v", err)
	}
	if len(past) != 1 || past[0].To != "Beijing" {
		t.Errorf("history lost: %+v", past)
	}
}

// The case that matters most to get right. Closing an old "knows" because a
// new one arrived would be silent data loss, and nothing afterwards would show
// that the graph used to know more than it does.
func TestAManyValuedLinkIsNeverSuperseded(t *testing.T) {
	db := brainWithCardinality(t)
	ctx := context.Background()
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, who := range []string{"Rene", "Alice", "Bob"} {
		when := at
		if err := SaveTemporalFact(ctx, db, TemporalFact{
			From: "Leo", To: who, Type: "knows", ValidFrom: &when,
		}); err != nil {
			t.Fatalf("save knows %s: %v", who, err)
		}
		at = at.AddDate(1, 0, 0)
	}

	facts, err := QueryFactsAsOf(ctx, db, at, TemporalFilter{From: "Leo", Type: "knows"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(facts) != 3 {
		got := make([]string, len(facts))
		for i, f := range facts {
			got[i] = f.To
		}
		t.Fatalf("Leo knows %d people (%v), want 3 — a many-valued link must not supersede", len(facts), got)
	}
}

// Re-stating a fact is not a contradiction. Closing and reopening would shred
// one continuous fact into fragments, which reads afterwards as though the
// value kept changing back to itself.
func TestRestatingAFactDoesNotFragmentIt(t *testing.T) {
	db := brainWithCardinality(t)
	ctx := context.Background()

	first := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	again := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, when := range []time.Time{first, again} {
		at := when
		if err := SaveTemporalFact(ctx, db, TemporalFact{
			From: "Leo", To: "Chengdu", Type: "lives_in", ValidFrom: &at,
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	// Still one continuous fact, still reaching back to the original start.
	facts, err := QueryFactsAsOf(ctx, db, first.AddDate(0, 6, 0), TemporalFilter{From: "Leo", Type: "lives_in"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("%d facts six months in, want 1 — the restatement fragmented the interval", len(facts))
	}
	if !facts[0].ValidFrom.Equal(first) {
		t.Errorf("valid_from moved to %s; the fact was true from %s",
			facts[0].ValidFrom.Format(time.RFC3339), first.Format(time.RFC3339))
	}
}

// A link the schema does not describe gets no opinion. Guessing here would
// close facts nobody said were contradictory.
func TestAnUndeclaredLinkIsLeftAlone(t *testing.T) {
	db := brainWithCardinality(t)
	ctx := context.Background()
	first := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		to   string
		when time.Time
	}{{"LINBIT", first}, {"HSBC", second}} {
		at := tc.when
		if err := SaveTemporalFact(ctx, db, TemporalFact{
			From: "Leo", To: tc.to, Type: "works_at", ValidFrom: &at,
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	facts, err := QueryFactsAsOf(ctx, db, second.AddDate(1, 0, 0), TemporalFilter{From: "Leo", Type: "works_at"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("%d facts for an undeclared link, want both left open — the ontology said nothing", len(facts))
	}

	// And the caller can still force it, which is what the flag is for.
	third := second.AddDate(2, 0, 0)
	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Leo", To: "Anthropic", Type: "works_at", ValidFrom: &third, Supersede: true,
	}); err != nil {
		t.Fatalf("forced supersede: %v", err)
	}
	facts, err = QueryFactsAsOf(ctx, db, third.AddDate(1, 0, 0), TemporalFilter{From: "Leo", Type: "works_at"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(facts) != 1 || facts[0].To != "Anthropic" {
		t.Errorf("forced supersede did not close the others: %+v", facts)
	}
}
