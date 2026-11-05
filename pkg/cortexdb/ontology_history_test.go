package cortexdb

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// pause keeps two writes on different sides of a microsecond-resolution
// instant the test then reads between them.
func pause() { time.Sleep(8 * time.Millisecond) }

func saveVersion(t *testing.T, db *DB, ctx context.Context, schema OntologySchema, activate bool) *OntologySchema {
	t.Helper()
	resp, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: activate})
	if err != nil {
		t.Fatalf("save %s: %v", schema.SchemaID, err)
	}
	return &resp.Schema
}

// aviationV2 adds a property: not breaking. aviationV3 drops the Flight type:
// breaking.
func aviationV2() OntologySchema {
	s := validAviationSchema()
	s.ObjectTypes[0].Properties = append(s.ObjectTypes[0].Properties,
		OntologyProperty{APIName: "city", DataType: OntologyDataType{Kind: OntologyDataString}})
	return s
}

func aviationV3() OntologySchema {
	s := aviationV2()
	s.ObjectTypes = s.ObjectTypes[:1]
	s.LinkTypes = nil
	return s
}

func otherSchema() OntologySchema {
	return OntologySchema{
		SchemaID: "shipping",
		ObjectTypes: []OntologyObjectType{{
			APIName: "Port", PrimaryKey: "code",
			Properties: []OntologyProperty{{APIName: "code", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true}},
		}},
	}
}

type ontologyHistoryScenario struct {
	db *DB
	// Instants between the writes.
	afterV1, afterV2, afterShipping, afterV3 time.Time
}

// aviation v1 (active) -> v2 (still active) -> shipping activated (aviation
// loses the role) -> aviation v3 activated (shipping loses it).
func runOntologyHistoryScenario(t *testing.T, db *DB) ontologyHistoryScenario {
	t.Helper()
	ctx := WithWriter(context.Background(), "alice")
	var s ontologyHistoryScenario
	s.db = db
	saveVersion(t, db, ctx, validAviationSchema(), true)
	pause()
	s.afterV1 = time.Now().UTC()
	pause()
	saveVersion(t, db, WithWriter(context.Background(), "bob"), aviationV2(), false)
	pause()
	s.afterV2 = time.Now().UTC()
	pause()
	saveVersion(t, db, ctx, otherSchema(), true)
	pause()
	s.afterShipping = time.Now().UTC()
	pause()
	saveVersion(t, db, ctx, aviationV3(), true)
	pause()
	s.afterV3 = time.Now().UTC()
	return s
}

func TestOntologyHistoryListsEveryVersionWithItsDiffAndActiveIntervals(t *testing.T) {
	db := openOntologyTestDB(t)
	runOntologyHistoryScenario(t, db)
	ctx := context.Background()

	resp, err := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if resp.Total != 3 || len(resp.Versions) != 3 || !resp.Exists || resp.CurrentVersion != 3 {
		t.Fatalf("history = total %d, %d versions, exists %v, current %d; want 3, 3, true, 3",
			resp.Total, len(resp.Versions), resp.Exists, resp.CurrentVersion)
	}
	// Newest first.
	for i, want := range []int{3, 2, 1} {
		if resp.Versions[i].Version != want {
			t.Fatalf("versions[%d] = %d, want %d", i, resp.Versions[i].Version, want)
		}
	}
	v3, v2, v1 := resp.Versions[0], resp.Versions[1], resp.Versions[2]

	if v1.Summary == nil || !v1.Summary.Initial {
		t.Errorf("v1 summary = %+v, want initial", v1.Summary)
	}
	if v2.Summary == nil || v2.Summary.Changes == 0 || v2.Summary.HasBreakingChanges || v2.Summary.ByKind["property_added"] != 1 {
		t.Errorf("v2 summary = %+v, want a non-breaking property_added", v2.Summary)
	}
	if v3.Summary == nil || !v3.Summary.HasBreakingChanges || v3.Summary.Breaking == 0 || v3.Summary.ByKind["object_type_removed"] != 1 {
		t.Errorf("v3 summary = %+v, want breaking object_type_removed", v3.Summary)
	}
	if v1.SavedBy != "alice" || v2.SavedBy != "bob" || v3.SavedBy != "alice" {
		t.Errorf("saved_by = %q %q %q, want alice bob alice", v1.SavedBy, v2.SavedBy, v3.SavedBy)
	}

	// v1: [save, v2 save) replaced; v2: [v2 save?...] — v2 was saved while
	// aviation was active, so it took the role at once; it lost it when
	// shipping was activated. v3 is active now.
	if len(v1.Activations) != 1 || v1.Activations[0].ActiveTo == nil || v1.Activations[0].EndReason != "replaced" || v1.Active {
		t.Errorf("v1 activations = %+v active=%v, want one interval ended 'replaced'", v1.Activations, v1.Active)
	}
	if len(v2.Activations) != 1 || v2.Activations[0].ActiveTo == nil || v2.Activations[0].EndReason != "switched" ||
		v2.Activations[0].EndedBy != "alice" || v2.Active {
		t.Errorf("v2 activations = %+v, want one interval ended 'switched' by alice", v2.Activations)
	}
	if len(v3.Activations) != 1 || v3.Activations[0].ActiveTo != nil || !v3.Active || v3.Activations[0].ActivatedBy != "alice" {
		t.Errorf("v3 activations = %+v active=%v, want one open interval", v3.Activations, v3.Active)
	}
	// The intervals tile: each begins exactly where the previous ended.
	if !v1.Activations[0].ActiveTo.Equal(v2.Activations[0].ActiveFrom) || !v2.Activations[0].ActiveTo.After(v2.Activations[0].ActiveFrom) {
		t.Errorf("intervals do not hand over at one instant: %+v %+v", v1.Activations, v2.Activations)
	}

	// Oldest first, and paging.
	asc, err := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation", OldestFirst: true, Limit: 2})
	if err != nil {
		t.Fatalf("history asc: %v", err)
	}
	if len(asc.Versions) != 2 || asc.Versions[0].Version != 1 || !asc.HasMore {
		t.Fatalf("oldest-first page = %+v", asc)
	}
	next, err := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation", OldestFirst: true, Limit: 2, Offset: 2})
	if err != nil || len(next.Versions) != 1 || next.Versions[0].Version != 3 || next.HasMore {
		t.Fatalf("second page = %+v, %v", next, err)
	}

	// The other schema's one interval was closed by aviation v3 taking over.
	other, err := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "shipping"})
	if err != nil || len(other.Versions) != 1 || other.Versions[0].Activations[0].EndReason != "switched" {
		t.Fatalf("shipping history = %+v, %v", other, err)
	}

	if _, err := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "nope"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown schema history error = %v, want not found", err)
	}
}

func TestOntologyGetAsOfReturnsTheSchemaActiveThen(t *testing.T) {
	db := openOntologyTestDB(t)
	s := runOntologyHistoryScenario(t, db)
	ctx := context.Background()
	asOf := func(at time.Time, id string) (*OntologyGetResponse, error) {
		return db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: id, AsOf: at.Format(time.RFC3339Nano)})
	}

	got, err := asOf(s.afterV1, "aviation")
	if err != nil || got.Schema.Version != 1 || len(got.Schema.ObjectTypes[0].Properties) != 2 {
		t.Fatalf("as_of after v1 = %+v, %v; want aviation v1", got, err)
	}
	got, err = asOf(s.afterV2, "aviation")
	if err != nil || got.Schema.Version != 2 || len(got.Schema.ObjectTypes[0].Properties) != 3 || got.ActiveInterval == nil {
		t.Fatalf("as_of after v2 = %+v, %v; want aviation v2 with its interval", got, err)
	}
	// Whichever schema was active, when no id is given.
	got, err = asOf(s.afterShipping, "")
	if err != nil || got.Schema.SchemaID != "shipping" || got.Schema.Version != 1 {
		t.Fatalf("as_of between switches, no id = %+v, %v; want shipping v1", got, err)
	}
	// aviation was not the validator while shipping held the role.
	if _, err := asOf(s.afterShipping, "aviation"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("as_of aviation while shipping was active = %v, want not found", err)
	}
	got, err = asOf(s.afterV3, "")
	if err != nil || got.Schema.SchemaID != "aviation" || got.Schema.Version != 3 {
		t.Fatalf("as_of now = %+v, %v; want aviation v3", got, err)
	}
	// Before anything existed.
	if _, err := asOf(s.afterV1.Add(-time.Hour), ""); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("as_of before the first save = %v, want not found", err)
	}
}

func TestOntologyGetRefusesAMalformedInstantAndBothSelectors(t *testing.T) {
	db := openOntologyTestDB(t)
	runOntologyHistoryScenario(t, db)
	ctx := context.Background()

	for _, asOf := range []string{"yesterday", "2026-03-05", "2026-03-05 15:00:00", "now"} {
		if _, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", AsOf: asOf}); err == nil ||
			!strings.Contains(err.Error(), "RFC 3339") {
			t.Errorf("as_of %q: err = %v, want an RFC 3339 refusal", asOf, err)
		}
	}
	if _, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 1, AsOf: "2026-03-05T15:00:00Z"}); err == nil ||
		!strings.Contains(err.Error(), "cannot both") {
		t.Errorf("version with as_of: err = %v, want refusal", err)
	}
	if _, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 99}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown version: err = %v, want not found", err)
	}
	if _, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: -1}); err == nil {
		t.Error("negative version accepted")
	}
}

func TestOntologyGetByVersionReturnsThatStoredDefinition(t *testing.T) {
	db := openOntologyTestDB(t)
	runOntologyHistoryScenario(t, db)
	ctx := context.Background()

	v1, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 1})
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if v1.Schema.Version != 1 || len(v1.Schema.ObjectTypes) != 2 || v1.Schema.Active {
		t.Fatalf("v1 = version %d, %d types, active %v", v1.Schema.Version, len(v1.Schema.ObjectTypes), v1.Schema.Active)
	}
	v3, _ := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 3})
	if !v3.Schema.Active || len(v3.Schema.ObjectTypes) != 1 {
		t.Fatalf("v3 = %+v, want the active one-type definition", v3.Schema)
	}
	cur, _ := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation"})
	if cur.Schema.Version != 3 {
		t.Fatalf("plain get = version %d, want the current 3", cur.Schema.Version)
	}
}

func TestOntologyDiffComparesTwoStoredVersions(t *testing.T) {
	db := openOntologyTestDB(t)
	runOntologyHistoryScenario(t, db)
	ctx := context.Background()

	resp, err := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", FromVersion: 1, ToVersion: 3})
	if err != nil {
		t.Fatalf("diff v1 v3: %v", err)
	}
	requireChange(t, resp.Diff, "object_type_removed", "Flight", true)
	requireChange(t, resp.Diff, "property_added", "Airport.city", false)

	// to_version omitted is the latest.
	latest, err := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", FromVersion: 1})
	if err != nil || !reflect.DeepEqual(latest.Diff, resp.Diff) {
		t.Fatalf("diff from v1 to latest = %+v, %v; want the v1..v3 diff", latest, err)
	}
	// Reversed, the removal becomes an addition: the sides are not symmetric.
	back, _ := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", FromVersion: 3, ToVersion: 1})
	requireChange(t, back.Diff, "object_type_added", "Flight", false)

	if _, err := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", FromVersion: 1, ToVersion: 3, Candidate: validAviationSchema()}); err == nil {
		t.Error("a candidate together with versions was accepted")
	}
	if _, err := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", ToVersion: 3}); err == nil {
		t.Error("to_version without from_version was accepted")
	}
	if _, err := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", FromVersion: 1, ToVersion: 42}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown to_version: err = %v, want not found", err)
	}

	// The candidate form is unchanged.
	cand, err := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", Candidate: validAviationSchema()})
	if err != nil {
		t.Fatalf("candidate diff: %v", err)
	}
	requireChange(t, cand.Diff, "object_type_added", "Flight", false)
}

func TestSavingAnOldDefinitionAgainRevertsByAppendingANewVersion(t *testing.T) {
	db := openOntologyTestDB(t)
	runOntologyHistoryScenario(t, db)
	ctx := context.Background()

	old, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 1})
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	reverted := saveVersion(t, db, ctx, old.Schema, true)
	if reverted.Version != 4 {
		t.Fatalf("revert saved as version %d, want a new version 4", reverted.Version)
	}
	v4, _ := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 4})
	v1, _ := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 1})
	if !reflect.DeepEqual(ontologySchemaDefinitionOf(v4.Schema), ontologySchemaDefinitionOf(v1.Schema)) {
		t.Fatalf("v4 is not v1's definition:\nv4 %+v\nv1 %+v", v4.Schema, v1.Schema)
	}
	// Nothing was rewritten: v2 and v3 are as they were, and the step back is
	// itself in the history, flagged as a change from v3.
	hist, _ := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation"})
	if hist.Total != 4 || hist.Versions[0].Version != 4 || hist.Versions[0].Summary.ByKind["object_type_added"] != 1 {
		t.Fatalf("history after revert = %+v", hist)
	}
	v3, _ := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 3})
	if len(v3.Schema.ObjectTypes) != 1 {
		t.Fatalf("v3 was rewritten by the revert: %+v", v3.Schema)
	}
	diff, _ := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", FromVersion: 1, ToVersion: 4})
	if len(diff.Diff.Changes) != 0 {
		t.Fatalf("v1 vs v4 = %+v, want no changes", diff.Diff.Changes)
	}
}

func TestDeletingASchemaKeepsItsHistoryAndClosesItsActiveInterval(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := WithWriter(context.Background(), "carol")
	saveVersion(t, db, ctx, validAviationSchema(), true)
	saveVersion(t, db, ctx, aviationV2(), false)
	pause()
	if _, err := db.DeleteOntologySchema(ctx, OntologyDeleteRequest{SchemaID: "aviation"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	afterDelete := time.Now().UTC()

	if _, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("plain get after delete = %v, want not found", err)
	}
	hist, err := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation"})
	if err != nil || hist.Exists || hist.Total != 2 {
		t.Fatalf("history after delete = %+v, %v; want 2 versions, exists=false", hist, err)
	}
	last := hist.Versions[0].Activations[0]
	if last.ActiveTo == nil || last.EndReason != "deleted" || last.EndedBy != "carol" || hist.Versions[0].Active {
		t.Fatalf("v2 interval after delete = %+v", last)
	}
	if v1, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 1}); err != nil || len(v1.Schema.ObjectTypes) != 2 {
		t.Fatalf("v1 unreadable after delete: %+v, %v", v1, err)
	}
	if _, err := db.GetOntologySchema(ctx, OntologyGetRequest{AsOf: afterDelete.Format(time.RFC3339Nano)}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("as_of after delete = %v, want nothing active", err)
	}

	// Saved again under the same id, numbering continues: (schema, version)
	// never names two definitions.
	again := saveVersion(t, db, ctx, validAviationSchema(), true)
	if again.Version != 3 {
		t.Fatalf("re-save after delete = version %d, want 3", again.Version)
	}
	if diff, _ := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", FromVersion: 2, ToVersion: 3}); diff == nil || len(diff.Diff.Changes) == 0 {
		t.Fatalf("v2..v3 diff after delete and re-save = %+v", diff)
	}
	hist, _ = db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation"})
	// Its change summary compares with the last version before the delete.
	if hist.Versions[0].Summary.Initial || hist.Versions[0].Summary.ByKind["property_removed"] != 1 {
		t.Fatalf("v3 summary = %+v, want a comparison against v2", hist.Versions[0].Summary)
	}
}

func TestADatabaseWithoutVersionTablesGetsItsCurrentSchemasBackfilled(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	runOntologyHistoryScenario(t, db)

	// Make it look like a database from before history: the live rows stay,
	// the two history tables are gone, and the process has not set up yet.
	for _, stmt := range []string{`DROP TABLE ontology_schema_versions`, `DROP TABLE ontology_activations`} {
		if _, err := db.exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	db.ontologySchemaMu.Lock()
	db.ontologySchemaReady = false
	db.ontologySchemaMu.Unlock()

	hist, err := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("history on a pre-history database: %v", err)
	}
	if hist.Total != 1 || hist.Versions[0].Version != 3 || !hist.Versions[0].Active || len(hist.Versions[0].Activations) != 1 ||
		hist.Versions[0].Activations[0].ActiveTo != nil {
		t.Fatalf("backfilled history = %+v, want the current v3 with an open interval", hist)
	}
	if hist.Versions[0].Summary != nil {
		t.Errorf("a backfilled version claims a change summary it cannot know: %+v", hist.Versions[0].Summary)
	}
	other, _ := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "shipping"})
	if other.Total != 1 || other.Versions[0].Active {
		t.Fatalf("inactive schema backfill = %+v, want one inactive version", other)
	}
	// The live definition is what the backfilled version holds.
	v3, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation", Version: 3})
	if err != nil || len(v3.Schema.ObjectTypes) != 1 {
		t.Fatalf("backfilled v3 = %+v, %v", v3, err)
	}
	// And the next save carries on from it.
	if next := saveVersion(t, db, ctx, aviationV2(), true); next.Version != 4 {
		t.Fatalf("save after backfill = version %d, want 4", next.Version)
	}
	got, err := db.GetOntologySchema(ctx, OntologyGetRequest{AsOf: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil || got.Schema.Version != 4 {
		t.Fatalf("as_of now after backfill = %+v, %v", got, err)
	}
}

func TestBackfillIsIdempotentAndSafeWhenSeveralConnectionsOpenTogether(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	runOntologyHistoryScenario(t, db)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.ensureOntologyHistoryTables(ctx); err != nil {
				t.Errorf("concurrent ensure: %v", err)
			}
		}()
	}
	wg.Wait()

	hist, err := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation"})
	if err != nil || hist.Total != 3 {
		t.Fatalf("re-running the backfill changed the history: %+v, %v", hist, err)
	}
	var open int
	if err := db.queryRow(ctx, `SELECT COUNT(*) FROM ontology_activations WHERE active_to IS NULL`).Scan(&open); err != nil || open != 1 {
		t.Fatalf("open intervals = %d (%v), want exactly one", open, err)
	}
}

func TestExplicitDeactivationClosesTheInterval(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	saveVersion(t, db, ctx, validAviationSchema(), true)
	pause()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema(), Deactivate: true}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	hist, _ := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation", OldestFirst: true})
	if len(hist.Versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(hist.Versions))
	}
	a := hist.Versions[0].Activations
	if len(a) != 1 || a[0].EndReason != "deactivated" || a[0].ActiveTo == nil || len(hist.Versions[1].Activations) != 0 {
		t.Fatalf("activations after deactivate: v1 %+v, v2 %+v", a, hist.Versions[1].Activations)
	}
	// An unchanged save of an inactive schema opens nothing.
	saveVersion(t, db, ctx, validAviationSchema(), false)
	var intervals int
	if err := db.queryRow(ctx, `SELECT COUNT(*) FROM ontology_activations`).Scan(&intervals); err != nil || intervals != 1 {
		t.Fatalf("intervals = %d (%v), want 1", intervals, err)
	}
}

func TestSavedByIsEmptyWhenNoWriterIsKnown(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	saveVersion(t, db, ctx, validAviationSchema(), true)
	hist, _ := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation"})
	if hist.Versions[0].SavedBy != "" || hist.Versions[0].Activations[0].ActivatedBy != "" {
		t.Fatalf("invented a writer: %+v", hist.Versions[0])
	}
}

func TestActionAuditRecordsTheOntologyVersionTheActionRanUnder(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	activateAviationActions(t, db)
	saved := saveVersion(t, db, ctx, aviationSchemaWithActions(), true) // v2, still active
	if saved.Version != 2 {
		t.Fatalf("setup: version %d, want 2", saved.Version)
	}
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action: "registerAirport", Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"}, Actor: "tester",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var id string
	var version int
	if err := db.queryRow(ctx, `SELECT schema_id, schema_version FROM ontology_action_audit WHERE action_api_name = ?`, "registerAirport").
		Scan(&id, &version); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if id != "aviation" || version != 2 {
		t.Fatalf("audit schema = %s v%d, want aviation v2", id, version)
	}
}

func TestAnAuditTableFromBeforeVersionsGainsTheColumnsAndKeepsItsRows(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE ontology_action_audit (
			id ` + db.Dialect().AutoIncrementPK() + `,
			action_api_name TEXT NOT NULL, actor TEXT, parameters TEXT NOT NULL, edits TEXT NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO ontology_action_audit (action_api_name, actor, parameters, edits) VALUES ('old', 'x', '{}', '[]')`,
	} {
		if _, err := db.exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	activateAviationActions(t, db)
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action: "registerAirport", Parameters: map[string]string{"iataCode": "CDG", "airportName": "Paris"}, Actor: "tester",
	}); err != nil {
		t.Fatalf("apply on a pre-version audit table: %v", err)
	}
	var oldVersion *int
	if err := db.queryRow(ctx, `SELECT schema_version FROM ontology_action_audit WHERE action_api_name = 'old'`).Scan(&oldVersion); err != nil || oldVersion != nil {
		t.Fatalf("old audit row = %v, %v; want it kept with no version", oldVersion, err)
	}
	var version int
	if err := db.queryRow(ctx, `SELECT schema_version FROM ontology_action_audit WHERE action_api_name = 'registerAirport'`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("new audit row version = %d, %v; want 1", version, err)
	}
}

func TestOntologyHistoryToolsRunThroughTheToolbox(t *testing.T) {
	db := openOntologyTestDB(t)
	s := runOntologyHistoryScenario(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	raw, err := tools.Call(ctx, "ontology_history", json.RawMessage(`{"schema_id":"aviation","limit":2}`))
	if err != nil {
		t.Fatalf("ontology_history: %v", err)
	}
	hist, ok := raw.(*OntologyHistoryResponse)
	if !ok || len(hist.Versions) != 2 || !hist.HasMore {
		t.Fatalf("ontology_history = %T %+v", raw, raw)
	}
	raw, err = tools.Call(ctx, "ontology_get", mustJSON(t, map[string]any{"schema_id": "aviation", "as_of": s.afterV2.Format(time.RFC3339Nano)}))
	if err != nil || raw.(*OntologyGetResponse).Schema.Version != 2 {
		t.Fatalf("ontology_get as_of = %+v, %v", raw, err)
	}
	raw, err = tools.Call(ctx, "ontology_diff", json.RawMessage(`{"schema_id":"aviation","from_version":1,"to_version":3}`))
	if err != nil {
		t.Fatalf("ontology_diff versions: %v", err)
	}
	requireChange(t, raw.(*OntologyDiffResponse).Diff, "object_type_removed", "Flight", true)
}

// The same history on PostgreSQL: the tables, the partial unique index, the
// ON CONFLICT DO NOTHING backfill, instants compared across the driver, and
// the audit columns added to a table that may predate them.
func TestOntologyHistoryOnPostgreSQL(t *testing.T) {
	db := openPostgresBrain(t, 4)
	ctx := context.Background()
	s := runOntologyHistoryScenario(t, db)

	hist, err := db.OntologyHistory(ctx, OntologyHistoryRequest{SchemaID: "aviation"})
	if err != nil || hist.Total != 3 || hist.Versions[0].Summary == nil || !hist.Versions[0].Summary.HasBreakingChanges ||
		!hist.Versions[0].Active || hist.Versions[2].Activations[0].EndReason != "replaced" {
		t.Fatalf("history on postgres = %+v, %v", hist, err)
	}
	got, err := db.GetOntologySchema(ctx, OntologyGetRequest{AsOf: s.afterShipping.Format(time.RFC3339Nano)})
	if err != nil || got.Schema.SchemaID != "shipping" {
		t.Fatalf("as_of on postgres = %+v, %v; want shipping", got, err)
	}
	if _, err := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", FromVersion: 1, ToVersion: 3}); err != nil {
		t.Fatalf("diff on postgres: %v", err)
	}
	if _, err := db.DeleteOntologySchema(ctx, OntologyDeleteRequest{SchemaID: "aviation"}); err != nil {
		t.Fatalf("delete on postgres: %v", err)
	}
	if again := saveVersion(t, db, ctx, validAviationSchema(), true); again.Version != 4 {
		t.Fatalf("re-save on postgres = version %d, want 4", again.Version)
	}
	if err := db.ensureOntologyHistoryTables(ctx); err != nil {
		t.Fatalf("backfill re-run on postgres: %v", err)
	}

	activateAviationActions(t, db)
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action: "registerAirport", Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"}, Actor: "tester",
	}); err != nil {
		t.Fatalf("apply on postgres: %v", err)
	}
	var version int
	if err := db.queryRow(ctx, `SELECT schema_version FROM ontology_action_audit WHERE action_api_name = ?`, "registerAirport").Scan(&version); err != nil || version < 1 {
		t.Fatalf("audit version on postgres = %d, %v", version, err)
	}
}
