package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

var healthNow = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func healthOpts() GraphHealthOptions { return GraphHealthOptions{Now: healthNow} }

// backdate moves every row of a table out of the growth window, so a fixture
// controls exactly which days its rows land on.
func backdateAll(t *testing.T, db *DB, table string) {
	t.Helper()
	if _, err := db.SQL().Exec(`UPDATE `+table+` SET recorded_at = ?`, healthNow.AddDate(0, 0, -90)); err != nil {
		t.Fatal(err)
	}
}

func setRecorded(t *testing.T, db *DB, edgeID string, at time.Time) {
	t.Helper()
	if _, err := db.SQL().Exec(`UPDATE graph_edges SET recorded_at = ? WHERE id = ?`, at, edgeID); err != nil {
		t.Fatal(err)
	}
}

func entities(t *testing.T, tools *GraphRAGToolbox, names ...string) {
	t.Helper()
	in := make([]ToolEntityInput, len(names))
	for i, n := range names {
		in[i] = ToolEntityInput{Name: n}
	}
	if _, err := tools.UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{Entities: in}); err != nil {
		t.Fatal(err)
	}
}

func runHealth(t *testing.T, db *DB, opts GraphHealthOptions) *GraphHealthReport {
	t.Helper()
	r, err := db.GraphHealth(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// growthFixture writes perDay edges a day for ten days from one producer,
// and, when burst > 0, burst more on the last day.
func growthFixture(t *testing.T, burst int) *DB {
	db, tools := provenanceBrain(t)
	names := []string{}
	for i := 0; i < 400; i++ {
		names = append(names, fmt.Sprintf("n%03d", i))
	}
	entities(t, tools, names...)
	backdateAll(t, db, "graph_nodes")
	backdateAll(t, db, "graph_edges") // the mention edges UpsertEntities wrote
	k := 0
	for day := 0; day < 10; day++ {
		n := 5
		if day == 9 {
			n += burst
		}
		for i := 0; i < n; i++ {
			id := writeRelation(t, tools, "", ToolRelationInput{From: names[k], To: names[k+1], Type: "related_to",
				Metadata: map[string]string{KeyProducer: ProducerLLMExtract}})
			setRecorded(t, db, id, healthNow.AddDate(0, 0, -9+day))
			k++
		}
	}
	return db
}

func TestGraphHealthGrowth(t *testing.T) {
	clean := runHealth(t, growthFixture(t, 0), healthOpts())
	if clean.Growth.Alert || !clean.Healthy {
		t.Errorf("clean growth fired: %+v alerts=%v", clean.Growth, clean.Alerts)
	}
	bad := runHealth(t, growthFixture(t, 120), healthOpts())
	if !bad.Growth.Alert {
		t.Fatalf("burst did not fire: %+v", bad.Growth)
	}
	var p *ProducerGrowth
	for i := range bad.Growth.Producers {
		if bad.Growth.Producers[i].Producer == ProducerLLMExtract {
			p = &bad.Growth.Producers[i]
		}
	}
	if p == nil || !p.Spike || p.Peak != 125 || p.Baseline != 5 || p.PeakDay != "2026-09-20" {
		t.Errorf("producer growth = %+v", p)
	}
	if len(bad.Alerts) != 1 || bad.Alerts[0] != "growth" {
		t.Errorf("alerts = %v, want only growth", bad.Alerts)
	}
}

// degreeFixture is a chain; with hub it also has sixty nodes pointing at one.
func degreeFixture(t *testing.T, hub bool) *DB {
	db, tools := provenanceBrain(t)
	names := []string{"Unknown"}
	for i := 0; i < 60; i++ {
		names = append(names, fmt.Sprintf("c%02d", i))
	}
	entities(t, tools, names...)
	rels := []ToolRelationInput{}
	for i := 1; i < 60; i++ {
		rels = append(rels, ToolRelationInput{From: names[i], To: names[i+1], Type: "next"})
		if hub {
			rels = append(rels, ToolRelationInput{From: names[i], To: "Unknown", Type: "related_to"})
		}
	}
	if _, err := tools.UpsertRelations(context.Background(), ToolUpsertRelationsRequest{Relations: rels}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestGraphHealthDegreeTail(t *testing.T) {
	clean := runHealth(t, degreeFixture(t, false), healthOpts())
	if clean.Degree.Alert || !clean.Healthy {
		t.Errorf("clean degree fired: %+v alerts=%v", clean.Degree, clean.Alerts)
	}
	bad := runHealth(t, degreeFixture(t, true), healthOpts())
	if !bad.Degree.Alert || len(bad.Degree.TopIn) == 0 || bad.Degree.TopIn[0].Label != "Unknown" ||
		bad.Degree.TopIn[0].Degree != 59 || !bad.Degree.TopIn[0].Hub {
		t.Fatalf("hub not reported: %+v", bad.Degree)
	}
	if len(bad.Alerts) != 1 || bad.Alerts[0] != "degree" {
		t.Errorf("alerts = %v, want only degree", bad.Alerts)
	}
}

// supersessionFixture closes facts on four days: a steady two or three a day,
// and optionally a burst on the last.
func supersessionFixture(t *testing.T, burst int) *DB {
	db, tools := provenanceBrain(t)
	names := []string{}
	for i := 0; i < 200; i++ {
		names = append(names, fmt.Sprintf("s%03d", i))
	}
	entities(t, tools, names...)
	perDay := []int{2, 3, 2, 2 + burst}
	k := 0
	for d, n := range perDay {
		closed := healthNow.AddDate(0, 0, -4+d).Format(time.RFC3339)
		for i := 0; i < n; i++ {
			writeRelation(t, tools, "", ToolRelationInput{From: names[k], To: names[k+1], Type: "status",
				Metadata: map[string]string{"valid_from": "2020-01-01T00:00:00Z", "valid_to": closed}})
			k++
		}
	}
	return db
}

func TestGraphHealthSupersessions(t *testing.T) {
	clean := runHealth(t, supersessionFixture(t, 0), healthOpts())
	if clean.Supersession.Alert || !clean.Healthy {
		t.Errorf("clean supersession fired: %+v alerts=%v", clean.Supersession, clean.Alerts)
	}
	if len(clean.Supersession.Days) != 4 || clean.Supersession.Days[1].ClosedFacts != 3 {
		t.Errorf("days = %+v", clean.Supersession.Days)
	}
	bad := runHealth(t, supersessionFixture(t, 60), healthOpts())
	if !bad.Supersession.Alert {
		t.Fatalf("burst did not fire: %+v", bad.Supersession)
	}
	last := bad.Supersession.Days[len(bad.Supersession.Days)-1]
	if !last.Spike || last.Total != 62 {
		t.Errorf("last day = %+v", last)
	}
	if len(bad.Alerts) != 1 || bad.Alerts[0] != "supersession" {
		t.Errorf("alerts = %v, want only supersession", bad.Alerts)
	}

	// The absolute ceiling fires without a baseline, and retractions and
	// replaced versions count alongside closed facts.
	db, tools := provenanceBrain(t)
	entities(t, tools, "a", "b", "c")
	id := writeRelation(t, tools, "", ToolRelationInput{From: "a", To: "b", Type: "x"})
	writeRelation(t, tools, "", ToolRelationInput{From: "a", To: "b", Type: "x", Metadata: map[string]string{"k": "v2"}})
	if err := db.Graph().DeleteEdge(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r := runHealth(t, db, GraphHealthOptions{MaxSupersedesPerDay: 1})
	if !r.Supersession.Alert || len(r.Supersession.Days) != 1 ||
		r.Supersession.Days[0].Versions != 1 || r.Supersession.Days[0].Retractions != 1 {
		t.Errorf("ceiling = %+v", r.Supersession)
	}
}

func temporalFixture(t *testing.T, corrupt bool) *DB {
	db, tools := claimBrain(t)
	entities(t, tools, "Leo", "Beijing", "Chengdu", "Mia", "Vienna")
	writeRelation(t, tools, "", ToolRelationInput{From: "Leo", To: "Beijing", Type: "lives_in",
		Metadata: map[string]string{"valid_from": "2019-01-01T00:00:00Z", "valid_to": "2023-01-01T00:00:00Z"}})
	writeRelation(t, tools, "", ToolRelationInput{From: "Leo", To: "Chengdu", Type: "lives_in",
		Metadata: map[string]string{"valid_from": "2023-01-01T00:00:00Z"}})
	// knows is many-valued: two current values are fine.
	writeRelation(t, tools, "", ToolRelationInput{From: "Leo", To: "Mia", Type: "knows"})
	writeRelation(t, tools, "", ToolRelationInput{From: "Leo", To: "Vienna", Type: "knows"})
	if corrupt {
		// A writer that bypassed supersession: Mia lives in two places.
		writeRelation(t, tools, "", ToolRelationInput{From: "Mia", To: "Beijing", Type: "lives_in"})
		writeRelation(t, tools, "", ToolRelationInput{From: "Mia", To: "Vienna", Type: "lives_in",
			Metadata: map[string]string{"valid_from": "2024-01-01T00:00:00Z"}})
		// And an interval that ends before it starts.
		writeRelation(t, tools, "", ToolRelationInput{From: "Mia", To: "Chengdu", Type: "visited",
			Metadata: map[string]string{"valid_from": "2024-01-01T00:00:00Z", "valid_to": "2022-01-01T00:00:00Z"}})
	}
	return db
}

func TestGraphHealthTemporalInvariants(t *testing.T) {
	clean := runHealth(t, temporalFixture(t, false), healthOpts())
	if clean.Temporal.Alert || !clean.Healthy {
		t.Errorf("clean temporal fired: %+v alerts=%v", clean.Temporal, clean.Alerts)
	}
	bad := runHealth(t, temporalFixture(t, true), healthOpts())
	if !bad.Temporal.Alert || len(bad.Temporal.Violations) != 2 {
		t.Fatalf("violations = %+v", bad.Temporal)
	}
	kinds := map[string]TemporalViolation{}
	for _, v := range bad.Temporal.Violations {
		kinds[v.Kind] = v
	}
	if v, ok := kinds["multiple_open"]; !ok || v.Subject != EntityNodeID("Mia") || len(v.EdgeIDs) != 2 {
		t.Errorf("multiple_open = %+v", v)
	}
	if _, ok := kinds["inverted_interval"]; !ok {
		t.Errorf("inverted interval not reported: %+v", bad.Temporal.Violations)
	}
	if len(bad.Alerts) != 1 || bad.Alerts[0] != "temporal" {
		t.Errorf("alerts = %v, want only temporal", bad.Alerts)
	}

	// Declaring a relation single-valued brings it under the check.
	db, tools := provenanceBrain(t)
	entities(t, tools, "Leo", "Rome", "Oslo")
	writeRelation(t, tools, "", ToolRelationInput{From: "Leo", To: "Rome", Type: "born_in"})
	writeRelation(t, tools, "", ToolRelationInput{From: "Leo", To: "Oslo", Type: "born_in"})
	if r := runHealth(t, db, healthOpts()); r.Temporal.Alert {
		t.Errorf("undeclared relation fired: %+v", r.Temporal)
	}
	opts := healthOpts()
	opts.SingleValued = []string{"Born In"}
	if r := runHealth(t, db, opts); !r.Temporal.Alert {
		t.Errorf("declared relation did not fire: %+v", r.Temporal)
	}

	// Through the tool dispatcher too.
	raw, _ := json.Marshal(ToolGraphHealthRequest{SingleValued: []string{"born_in"}})
	out, err := db.GraphRAGTools().Call(context.Background(), "graph_health", raw)
	if err != nil {
		t.Fatal(err)
	}
	if rep := out.(GraphHealthReport); rep.Healthy || !rep.Temporal.Alert {
		t.Errorf("tool report = %+v", rep)
	}
}
