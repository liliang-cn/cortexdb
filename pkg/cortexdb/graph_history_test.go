package cortexdb

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// livesInSchema is a Person/City schema where lives_in reaches one city from a
// person and knows reaches many people.
func livesInSchema() OntologySchema {
	str := OntologyDataType{Kind: OntologyDataString}
	return OntologySchema{
		SchemaID: "people",
		Name:     "People",
		ObjectTypes: []OntologyObjectType{
			{APIName: "Person", PrimaryKey: "personId", Properties: []OntologyProperty{{APIName: "personId", DataType: str, Required: true}}},
			{APIName: "City", PrimaryKey: "cityId", Properties: []OntologyProperty{{APIName: "cityId", DataType: str, Required: true}}},
		},
		LinkTypes: []OntologyLinkType{
			{
				APIName: "lives_in",
				A:       OntologyLinkSide{APIName: "city", ObjectTypeAPIName: "Person", Cardinality: OntologyCardinalityOne},
				B:       OntologyLinkSide{APIName: "residents", ObjectTypeAPIName: "City", Cardinality: OntologyCardinalityMany},
			},
			{
				APIName: "knows",
				A:       OntologyLinkSide{APIName: "friends", ObjectTypeAPIName: "Person", Cardinality: OntologyCardinalityMany},
				B:       OntologyLinkSide{APIName: "friendsOf", ObjectTypeAPIName: "Person", Cardinality: OntologyCardinalityMany},
			},
		},
	}
}

func seedPeople(t *testing.T, db *DB, withSchema bool) {
	t.Helper()
	ctx := context.Background()
	if withSchema {
		if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: livesInSchema(), Activate: true}); err != nil {
			t.Fatalf("activate schema: %v", err)
		}
	}
	entity := func(name, typ, pk, id string) ToolEntityInput {
		in := ToolEntityInput{Name: name, Type: typ}
		if withSchema {
			in.Metadata = map[string]string{pk: id}
		}
		return in
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		entity("Leo", "Person", "personId", "leo"),
		entity("Mia", "Person", "personId", "mia"),
		entity("Noah", "Person", "personId", "noah"),
		entity("Beijing", "City", "cityId", "beijing"),
		entity("Chengdu", "City", "cityId", "chengdu"),
	}}); err != nil {
		t.Fatalf("seed entities: %v", err)
	}
}

func relate(t *testing.T, db *DB, from, to, typ string) string {
	t.Helper()
	res, err := db.GraphRAGTools().UpsertRelations(context.Background(), ToolUpsertRelationsRequest{
		Relations: []ToolRelationInput{{From: from, To: to, Type: typ}},
	})
	if err != nil {
		t.Fatalf("relate %s %s %s: %v", from, typ, to, err)
	}
	return res.EdgeIDs[0]
}

func historyLine(t *testing.T, res *GraphHistoryResult, predicate string) HistoryLine {
	t.Helper()
	for _, l := range res.Lines {
		if l.Predicate == predicate && l.Direction == "out" {
			return l
		}
	}
	t.Fatalf("no outbound %q line in %+v", predicate, res.Lines)
	return HistoryLine{}
}

func currentVersions(l HistoryLine) []HistoryVersion {
	var out []HistoryVersion
	for _, v := range l.Versions {
		if v.InEffect != nil {
			out = append(out, v)
		}
	}
	return out
}

func TestASingleValuedLinkReadsAsOneLineWithTwoVersions(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedPeople(t, db, true)

			beijing := relate(t, db, "Leo", "Beijing", "lives_in")
			before := db.Graph().Now()
			chengdu := relate(t, db, "Leo", "Chengdu", "lives_in")

			res, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Relation: "lives_in"})
			if err != nil {
				t.Fatalf("GraphHistory: %v", err)
			}
			if len(res.Lines) != 1 {
				t.Fatalf("got %d lines, want 1: %+v", len(res.Lines), res.Lines)
			}
			line := res.Lines[0]
			if len(line.Versions) != 2 {
				t.Fatalf("got %d versions, want 2: %+v", len(line.Versions), line.Versions)
			}
			old, cur := line.Versions[0], line.Versions[1]
			if old.ID != beijing || cur.ID != chengdu {
				t.Fatalf("versions are %s then %s, want %s then %s", old.ID, cur.ID, beijing, chengdu)
			}
			if old.Status != HistoryStatusSuperseded || old.Reason != graph.ReasonSuperseded || old.SupersededBy != chengdu {
				t.Errorf("Beijing = status %q reason %q superseded_by %q, want superseded by %s", old.Status, old.Reason, old.SupersededBy, chengdu)
			}
			if old.Producer != ProducerRelationUpsert {
				t.Errorf("producer = %q, want %q", old.Producer, ProducerRelationUpsert)
			}
			if old.SchemaID != "people" || old.SchemaVersion < 1 {
				t.Errorf("the closure should record the schema, got %q v%d", old.SchemaID, old.SchemaVersion)
			}
			if old.ObjectName != "Beijing" || cur.ObjectName != "Chengdu" {
				t.Errorf("object names = %q, %q", old.ObjectName, cur.ObjectName)
			}
			if cur.Status != HistoryStatusCurrent || old.InEffect != nil {
				t.Errorf("Chengdu should be the only version in effect: cur=%q old.in_effect=%v", cur.Status, old.InEffect)
			}
			ie := cur.InEffect
			if ie == nil || ie.Kind != InEffectReplaced || ie.Rule != InEffectRuleSingleValued {
				t.Fatalf("in_effect = %+v, want replaced by the single-valued rule", ie)
			}
			if ie.SchemaID != "people" || ie.SchemaVersion != old.SchemaVersion || ie.Replaced == nil || ie.Replaced.Object != old.Object {
				t.Errorf("in_effect should cite the recorded schema and what it replaced: %+v", ie)
			}
			if !strings.Contains(ie.Summary, "single-valued") || !strings.Contains(ie.Summary, "people v") {
				t.Errorf("summary does not name the rule and schema version: %q", ie.Summary)
			}

			// The past is still readable: before the change, Beijing.
			past, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Relation: "lives_in", AsOf: before})
			if err != nil {
				t.Fatalf("GraphHistory as of: %v", err)
			}
			then := currentVersions(historyLine(t, past, "lives_in"))
			if len(then) != 1 || then[0].ID != beijing || then[0].InEffect.Kind != InEffectOnlyVersion {
				t.Errorf("as of before the move, in effect = %+v, want Beijing as the only version", then)
			}
			src, args := db.Graph().EdgeSource(graph.AsOf(ctx, before))
			var n int
			if err := db.queryRow(ctx, `SELECT COUNT(*) FROM `+src+` AS e WHERE id = ?`, append(args, beijing)...).Scan(&n); err != nil || n != 1 {
				t.Errorf("graph as-of before the change should hold Beijing: n=%d err=%v", n, err)
			}
			if live, _ := db.Graph().GetEdges(ctx, graphNodeIDOf(t, db, "Leo"), "out"); len(live) != 1 {
				t.Errorf("only one lives_in edge should be live, got %d", len(live))
			}
		})
	}
}

func graphNodeIDOf(t *testing.T, db *DB, name string) string {
	t.Helper()
	id := db.existingNodeID(context.Background(), name)
	if id == "" {
		resolved, err := db.resolveClaimEndpoints(context.Background(), []Claim{{Subject: name}})
		if err != nil || len(resolved[name]) != 1 {
			t.Fatalf("cannot resolve %q: %v %v", name, resolved, err)
		}
		id = resolved[name][0]
	}
	return id
}

func TestAnUnconstrainedLinkKeepsBothValuesInEffect(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedPeople(t, db, true)
			relate(t, db, "Leo", "Mia", "knows")
			relate(t, db, "Leo", "Noah", "knows")

			res, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Relation: "knows"})
			if err != nil {
				t.Fatal(err)
			}
			now := currentVersions(historyLine(t, res, "knows"))
			if len(now) != 2 {
				t.Fatalf("both values should be in effect, got %d", len(now))
			}
			for _, v := range now {
				ie := v.InEffect
				if ie.Kind != InEffectSeveralInEffect || ie.SchemaOpinion != SchemaOpinionMultiValued || len(ie.Alongside) != 1 {
					t.Errorf("in_effect = %+v, want several_in_effect with a multi_valued schema", ie)
				}
				if v.Status != HistoryStatusCurrent || v.SupersededBy != "" {
					t.Errorf("nothing should have been closed: %+v", v)
				}
			}
		})
	}
}

func TestWithNoSchemaNothingIsClosedAndTheHistoryHasNoOpinion(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedPeople(t, db, false)
			relate(t, db, "Leo", "Beijing", "lives_in")
			relate(t, db, "Leo", "Chengdu", "lives_in")

			res, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Relation: "lives_in"})
			if err != nil {
				t.Fatal(err)
			}
			line := historyLine(t, res, "lives_in")
			now := currentVersions(line)
			if len(line.Versions) != 2 || len(now) != 2 {
				t.Fatalf("both must stay live without a schema: %+v", line.Versions)
			}
			for _, v := range now {
				if v.InEffect.Kind != InEffectSeveralInEffect || v.InEffect.SchemaOpinion != SchemaOpinionNone {
					t.Errorf("in_effect = %+v, want several_in_effect with no schema opinion", v.InEffect)
				}
				if strings.Contains(v.InEffect.Summary, "wins") || strings.Contains(v.InEffect.Summary, "latest") {
					t.Errorf("must not imply a winner: %q", v.InEffect.Summary)
				}
			}
		})
	}
}

func TestTheClosureKeepsCitingTheSchemaVersionItWasDecidedUnder(t *testing.T) {
	db := temporalBrains(t)["sqlite"]
	ctx := context.Background()
	seedPeople(t, db, true)
	relate(t, db, "Leo", "Beijing", "lives_in")
	relate(t, db, "Leo", "Chengdu", "lives_in")

	first, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Relation: "lives_in"})
	if err != nil {
		t.Fatal(err)
	}
	decidedUnder := currentVersions(historyLine(t, first, "lives_in"))[0].InEffect.SchemaVersion

	// A later schema makes lives_in multi-valued. The closure made under the
	// earlier one must still be explained by the earlier one.
	changed := livesInSchema()
	changed.LinkTypes[0].A.Cardinality = OntologyCardinalityMany
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: changed, Activate: true}); err != nil {
		t.Fatal(err)
	}
	if active, v, _ := db.ActiveOntologyRef(ctx); active != "people" || v <= decidedUnder {
		t.Fatalf("the schema should have a newer version now, got %s v%d (was v%d)", active, v, decidedUnder)
	}
	again, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Relation: "lives_in"})
	if err != nil {
		t.Fatal(err)
	}
	ie := currentVersions(historyLine(t, again, "lives_in"))[0].InEffect
	if ie.SchemaVersion != decidedUnder || ie.Rule != InEffectRuleSingleValued {
		t.Errorf("in_effect cites v%d (%s), want the recorded v%d", ie.SchemaVersion, ie.Rule, decidedUnder)
	}
}

func TestOnlyTheEndOfALinkTheSchemaCallsSingleValuedIsClosed(t *testing.T) {
	db := temporalBrains(t)["sqlite"]
	ctx := context.Background()
	str := OntologyDataType{Kind: OntologyDataString}
	schema := OntologySchema{
		SchemaID: "air", Name: "Air",
		ObjectTypes: []OntologyObjectType{
			{APIName: "Airport", PrimaryKey: "code", Properties: []OntologyProperty{{APIName: "code", DataType: str, Required: true}}},
			{APIName: "Flight", PrimaryKey: "number", Properties: []OntologyProperty{{APIName: "number", DataType: str, Required: true}}},
		},
		LinkTypes: []OntologyLinkType{{
			APIName: "departure",
			A:       OntologyLinkSide{APIName: "departures", ObjectTypeAPIName: "Airport", Cardinality: OntologyCardinalityMany},
			B:       OntologyLinkSide{APIName: "origin", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityOne},
		}},
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "LHR", Type: "Airport", Metadata: map[string]string{"code": "LHR"}},
		{Name: "BA1", Type: "Flight", Metadata: map[string]string{"number": "BA1"}},
		{Name: "BA2", Type: "Flight", Metadata: map[string]string{"number": "BA2"}},
	}}); err != nil {
		t.Fatal(err)
	}
	relate(t, db, "LHR", "BA1", "departure")
	relate(t, db, "LHR", "BA2", "departure")
	res, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "LHR", Relation: "departure"})
	if err != nil {
		t.Fatal(err)
	}
	if now := currentVersions(historyLine(t, res, "departure")); len(now) != 2 {
		t.Errorf("an airport departs many flights; both must stay in effect, got %d", len(now))
	}
}

func TestAMergeSurvivorIsExplained(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
				{Name: "Leo"}, {Name: "Leo Li"}, {Name: "LINBIT"},
			}}); err != nil {
				t.Fatal(err)
			}
			relate(t, db, "Leo Li", "LINBIT", "works_at")
			if err := db.Graph().MergeEntities(ctx, EntityNodeID("Leo"), []string{EntityNodeID("Leo Li")}); err != nil {
				t.Fatalf("merge: %v", err)
			}

			res, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo"})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.MergedFrom) != 1 || res.MergedFrom[0].Name != "Leo Li" {
				t.Errorf("merged_from = %+v", res.MergedFrom)
			}
			line := historyLine(t, res, "works_at")
			now := currentVersions(line)
			if len(now) != 1 || now[0].InEffect.Kind != InEffectMergeSurvivor || now[0].InEffect.MergedFrom != "Leo Li" {
				t.Fatalf("in_effect = %+v, want a merge survivor from Leo Li", now)
			}
			if len(line.Versions) != 2 || line.Versions[0].Status != HistoryStatusMerged || line.Versions[0].Subject != EntityNodeID("Leo Li") {
				t.Errorf("the pre-merge version should be on the same line, merged: %+v", line.Versions)
			}
		})
	}
}

func TestHistoryPagesByLineAndHonoursTheWindow(t *testing.T) {
	db := temporalBrains(t)["sqlite"]
	ctx := context.Background()
	seedPeople(t, db, false)
	start := db.Graph().Now()
	for _, typ := range []string{"a_rel", "b_rel", "c_rel"} {
		relate(t, db, "Leo", "Beijing", typ)
	}
	mid := db.Graph().Now()
	relate(t, db, "Leo", "Chengdu", "b_rel")

	var seen []string
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		res, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Lines) != 1 {
			t.Fatalf("page %d has %d lines, want 1", pages, len(res.Lines))
		}
		seen = append(seen, res.Lines[0].Predicate)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if strings.Join(seen, ",") != "a_rel,b_rel,c_rel" {
		t.Errorf("paged lines = %v", seen)
	}

	// A window that ends before the second b_rel value leaves one version.
	res, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Relation: "b_rel", From: start, To: mid})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(historyLine(t, res, "b_rel").Versions); got != 1 {
		t.Errorf("window before the change has %d versions, want 1", got)
	}
	// A window after everything began still sees the live value but not the closed one.
	res, err = db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Relation: "b_rel", From: db.Graph().Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range historyLine(t, res, "b_rel").Versions {
		if v.Status != HistoryStatusCurrent {
			t.Errorf("a window after the change returned a closed version: %+v", v)
		}
	}
	// And a cap on versions keeps the newest and says so.
	res, err = db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Relation: "b_rel", MaxVersions: 1})
	if err != nil {
		t.Fatal(err)
	}
	l := historyLine(t, res, "b_rel")
	if len(l.Versions) != 1 || !l.VersionsTruncated || l.Versions[0].ObjectName != "Chengdu" {
		t.Errorf("version cap = %+v truncated=%v", l.Versions, l.VersionsTruncated)
	}
}

func TestAnAmbiguousNameIsRefusedAndABadWindowIsToo(t *testing.T) {
	db := temporalBrains(t)["sqlite"]
	ctx := context.Background()
	seedPeople(t, db, false)
	if _, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "no such entity"}); err == nil {
		t.Error("an unknown entity should be an error")
	}
	if _, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", From: time.Now(), To: time.Now().Add(-time.Hour)}); err == nil {
		t.Error("an empty window should be an error")
	}
	if _, err := db.GraphHistory(ctx, GraphHistoryRequest{Entity: "Leo", Cursor: "!!"}); err == nil {
		t.Error("a bad cursor should be an error")
	}
}

func TestGraphHistoryIsAReadOnlyToolThatAnswersOverTheToolbox(t *testing.T) {
	db := temporalBrains(t)["sqlite"]
	ctx := context.Background()
	seedPeople(t, db, true)
	relate(t, db, "Leo", "Beijing", "lives_in")
	relate(t, db, "Leo", "Chengdu", "lives_in")

	for _, d := range ToolDefinitions() {
		if d.Name == "graph_history" && d.Mutates {
			t.Fatal("graph_history must not be a write")
		}
	}
	raw, err := db.GraphRAGTools().Call(ctx, "graph_history", mustJSON(t, GraphHistoryToolRequest{Entity: "Leo", Relation: "lives_in"}))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	res, ok := raw.(GraphHistoryResult)
	if !ok || len(res.Lines) != 1 || len(res.Lines[0].Versions) != 2 {
		t.Fatalf("tool answered %+v", raw)
	}
	if _, err := db.GraphRAGTools().Call(ctx, "graph_history", mustJSON(t, GraphHistoryToolRequest{Entity: "Leo", AsOf: "yesterday"})); err == nil {
		t.Error("a malformed as_of must be refused, not read as now")
	}
}
