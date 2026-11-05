package graphflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func openTemporalTestDB(t *testing.T) (*cortexdb.DB, context.Context) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("test_temporal_%d.db", testname.Nano()))
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	return db, context.Background()
}

// TestQueryFactsAsOfDisjointIntervals saves two facts for the same subject with
// non-overlapping validity intervals and verifies an as-of query returns the one
// whose interval contains the queried instant.
func TestQueryFactsAsOfDisjointIntervals(t *testing.T) {
	db, ctx := openTemporalTestDB(t)

	// Alice worked at Acme in [2020, 2022), then at Globex in [2022, open).
	t2020 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t2022 := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Alice", To: "Acme", Type: "works_at",
		ValidFrom: &t2020, ValidTo: &t2022,
	}); err != nil {
		t.Fatalf("save fact 1: %v", err)
	}
	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Alice", To: "Globex", Type: "works_at",
		ValidFrom: &t2022,
	}); err != nil {
		t.Fatalf("save fact 2: %v", err)
	}

	// Inside interval 1 → Acme.
	at2021 := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC)
	facts, err := QueryFactsAsOf(ctx, db, at2021, TemporalFilter{From: "Alice"})
	if err != nil {
		t.Fatalf("query 2021: %v", err)
	}
	if len(facts) != 1 || facts[0].To != "Acme" {
		t.Fatalf("as of 2021 expected [Acme], got %+v", facts)
	}
	if facts[0].RecordedAt == nil {
		t.Fatalf("expected recorded_at (transaction time) to be set")
	}

	// Inside interval 2 → Globex.
	at2023 := time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC)
	facts, err = QueryFactsAsOf(ctx, db, at2023, TemporalFilter{From: "Alice"})
	if err != nil {
		t.Fatalf("query 2023: %v", err)
	}
	if len(facts) != 1 || facts[0].To != "Globex" {
		t.Fatalf("as of 2023 expected [Globex], got %+v", facts)
	}

	// Type filter also scopes the query.
	facts, err = QueryFactsAsOf(ctx, db, at2021, TemporalFilter{From: "Alice", Type: "works_at"})
	if err != nil {
		t.Fatalf("query typed: %v", err)
	}
	if len(facts) != 1 || facts[0].Type != "works_at" {
		t.Fatalf("typed filter expected 1 works_at, got %+v", facts)
	}
}

// TestSupersedeClosesOpenFact saves an open fact, supersedes it, and verifies the
// old fact is closed (valid_to set) so it no longer resolves after the cutoff but
// still resolves before it.
func TestSupersedeClosesOpenFact(t *testing.T) {
	db, ctx := openTemporalTestDB(t)

	t2020 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Bob", To: "Engineer", Type: "has_title",
		ValidFrom: &t2020, // open-ended
	}); err != nil {
		t.Fatalf("save open fact: %v", err)
	}

	// Sanity: currently valid.
	at2022 := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	facts, err := QueryFactsAsOf(ctx, db, at2022, TemporalFilter{From: "Bob"})
	if err != nil {
		t.Fatalf("query before supersede: %v", err)
	}
	if len(facts) != 1 || facts[0].To != "Engineer" {
		t.Fatalf("before supersede expected [Engineer], got %+v", facts)
	}

	// Supersede at 2023: closes the open Engineer fact.
	t2023 := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	closed, err := SupersedeFact(ctx, db, "Bob", "has_title", t2023)
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if closed != 1 {
		t.Fatalf("expected to close 1 fact, closed %d", closed)
	}

	// After the cutoff the old fact no longer applies.
	at2024 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	facts, err = QueryFactsAsOf(ctx, db, at2024, TemporalFilter{From: "Bob"})
	if err != nil {
		t.Fatalf("query after supersede: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("after supersede expected no valid facts, got %+v", facts)
	}

	// Before the cutoff it still applies (valid_to is exclusive).
	facts, err = QueryFactsAsOf(ctx, db, at2022, TemporalFilter{From: "Bob"})
	if err != nil {
		t.Fatalf("query pre-cutoff: %v", err)
	}
	if len(facts) != 1 || facts[0].ValidTo == nil {
		t.Fatalf("pre-cutoff expected 1 closed fact with valid_to set, got %+v", facts)
	}
}

// TestSaveTemporalFactSupersede verifies the Supersede option closes the prior
// open value automatically when a replacement value is recorded.
func TestSaveTemporalFactSupersede(t *testing.T) {
	db, ctx := openTemporalTestDB(t)

	t2020 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Carol", To: "Junior", Type: "has_level", ValidFrom: &t2020,
	}); err != nil {
		t.Fatalf("save first: %v", err)
	}

	// New value supersedes the old one starting 2023.
	t2023 := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := SaveTemporalFact(ctx, db, TemporalFact{
		From: "Carol", To: "Senior", Type: "has_level", ValidFrom: &t2023, Supersede: true,
	}); err != nil {
		t.Fatalf("save superseding: %v", err)
	}

	// 2021 → Junior; 2024 → Senior; the two never overlap.
	at2021 := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC)
	facts, err := QueryFactsAsOf(ctx, db, at2021, TemporalFilter{From: "Carol"})
	if err != nil {
		t.Fatalf("query 2021: %v", err)
	}
	if len(facts) != 1 || facts[0].To != "Junior" {
		t.Fatalf("as of 2021 expected [Junior], got %+v", facts)
	}

	at2024 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	facts, err = QueryFactsAsOf(ctx, db, at2024, TemporalFilter{From: "Carol"})
	if err != nil {
		t.Fatalf("query 2024: %v", err)
	}
	if len(facts) != 1 || facts[0].To != "Senior" {
		t.Fatalf("as of 2024 expected [Senior], got %+v", facts)
	}
}

// A fact the ontology closed through SaveTemporalFact reads as one line in
// graph_history: the old value superseded by the new edge, the schema that
// decided it recorded, and the live row graphflow keeps for the closed fact
// folded into the same version rather than shown twice.
func TestAGraphflowSupersessionReadsAsOneLineWithTheSchemaRecorded(t *testing.T) {
	db, ctx := openTemporalTestDB(t)
	str := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}
	if _, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{Activate: true, Schema: cortexdb.OntologySchema{
		SchemaID: "work", Name: "Work", Enforcement: cortexdb.OntologyEnforcementVocabulary,
		ObjectTypes: []cortexdb.OntologyObjectType{
			{APIName: "Person", PrimaryKey: "id", Properties: []cortexdb.OntologyProperty{{APIName: "id", DataType: str, Required: true}}},
			{APIName: "Company", PrimaryKey: "id", Properties: []cortexdb.OntologyProperty{{APIName: "id", DataType: str, Required: true}}},
		},
		LinkTypes: []cortexdb.OntologyLinkType{{
			APIName: "works_at",
			A:       cortexdb.OntologyLinkSide{APIName: "employer", ObjectTypeAPIName: "Person", Cardinality: cortexdb.OntologyCardinalityOne},
			B:       cortexdb.OntologyLinkSide{APIName: "staff", ObjectTypeAPIName: "Company", Cardinality: cortexdb.OntologyCardinalityMany},
		}},
	}}); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t2020 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t2022 := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range []TemporalFact{
		{From: "Alice", To: "Acme", Type: "works_at", ValidFrom: &t2020},
		{From: "Alice", To: "Globex", Type: "works_at", ValidFrom: &t2022},
	} {
		if err := SaveTemporalFact(ctx, db, f); err != nil {
			t.Fatalf("save %s: %v", f.To, err)
		}
	}

	res, err := db.GraphHistory(ctx, cortexdb.GraphHistoryRequest{Entity: "Alice", Relation: "works_at"})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(res.Lines) != 1 || len(res.Lines[0].Versions) != 2 {
		t.Fatalf("want one line with two versions, got %+v", res.Lines)
	}
	old, cur := res.Lines[0].Versions[0], res.Lines[0].Versions[1]
	if old.ObjectName != "Acme" || old.Status != cortexdb.HistoryStatusSuperseded || old.SupersededBy != cur.ID {
		t.Errorf("Acme should be superseded by %s: %+v", cur.ID, old)
	}
	if old.SchemaID != "work" || old.SchemaVersion < 1 {
		t.Errorf("the closure should record the schema: %q v%d", old.SchemaID, old.SchemaVersion)
	}
	if cur.InEffect == nil || cur.InEffect.Kind != cortexdb.InEffectReplaced || cur.InEffect.Rule != cortexdb.InEffectRuleSingleValued {
		t.Errorf("in_effect = %+v", cur.InEffect)
	}
}
