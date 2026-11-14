package liveview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Every tool the analysis page may run exists and is a read.
func TestTheAnalysisToolsOnlyRead(t *testing.T) {
	defs := map[string]cortexdb.ToolDefinition{}
	for _, d := range cortexdb.ToolDefinitions() {
		defs[d.Name] = d
	}
	for _, d := range cortexdb.KnowledgeMemoryFacadeToolDefinitions() {
		defs[d.Name] = d
	}
	for _, name := range AnalysisTools {
		d, ok := defs[name]
		if !ok {
			t.Errorf("%s is on AnalysisTools but no tool has that name", name)
			continue
		}
		if d.Mutates {
			t.Errorf("%s can write, and the view promises to only read", name)
		}
		if !exploreAllowed(name) {
			t.Errorf("%s is on AnalysisTools and the callers refuse it", name)
		}
	}
}

// analysisBrain is a brain with memories, documents and a graph, some of it
// stored on earlier days, behind a view.
func analysisBrain(t *testing.T, opts ...cortexdb.Option) (*cortexdb.DB, http.Handler) {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "analysis.db")), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sv, err := New(context.Background(), SourceFor(db, "analysis <test>"), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	return db, sv.Handler()
}

// storedOn moves a row's stored time, the way a brain that has been written
// to for weeks has rows from different days.
func storedOn(t *testing.T, db *cortexdb.DB, table, column, id string, at time.Time) {
	t.Helper()
	// table and column are the test's own words, never a caller's.
	_, err := db.SQL().Exec(fmt.Sprintf(`UPDATE %s SET created_at = ? WHERE %s = ?`, table, column), at.UTC().Format("2006-01-02 15:04:05"), id)
	if err != nil {
		t.Fatal(err)
	}
}

func saveDoc(t *testing.T, db *cortexdb.DB, id string, meta map[string]string, at time.Time) {
	t.Helper()
	// Long enough to be cut into several chunks, so a count of chunks and a
	// count of documents are different numbers.
	body := strings.Repeat("The gateway fails over between the three nodes of the cluster. ", 120)
	if _, err := db.SaveKnowledge(context.Background(), cortexdb.KnowledgeSaveRequest{
		KnowledgeID: id, Title: "Runbook " + id, Content: body, ChunkSize: 200, Metadata: meta,
	}); err != nil {
		t.Fatal(err)
	}
	storedOn(t, db, "embeddings", "doc_id", id, at)
}

func seriesTotal(points []AnalysisPoint) (total float64) {
	for _, p := range points {
		total += p.Value
	}
	return total
}

func TestTheOverviewCountsWhatTheBrainHolds(t *testing.T) {
	db, h := analysisBrain(t, cortexdb.WithRecallLog(cortexdb.RecallLogOptions{}))
	ctx := context.Background()
	now := time.Now().UTC()

	for i, scope := range []string{"global", "global", "user"} {
		id := fmt.Sprintf("m%d", i)
		if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: id, Scope: scope, UserID: "u1", Content: "note " + id}); err != nil {
			t.Fatal(err)
		}
	}
	// One memory from two days ago, one from long before any range.
	storedOn(t, db, "messages", "id", "m1", now.AddDate(0, 0, -2))
	storedOn(t, db, "messages", "id", "m2", now.AddDate(0, 0, -200))

	saveDoc(t, db, "d1", map[string]string{"kind": "runbook", "score": "0.5"}, now)
	saveDoc(t, db, "d2", map[string]string{"kind": "incident", "score": "0.9"}, now.AddDate(0, 0, -3))

	if _, err := db.GraphRAGTools().UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		{Name: "Gateway", Type: "service"}, {Name: "Node A", Type: "host"}, {Name: "Node B", Type: "host"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GraphRAGTools().UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: []cortexdb.ToolRelationInput{
		{From: "Gateway", To: "Node A", Type: "runs_on"}, {From: "Gateway", To: "Node B", Type: "runs_on"},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"note m0", "zzqx"} {
		if _, err := db.KnowledgeMemory().Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{Query: q}); err != nil {
			t.Fatal(err)
		}
	}

	var got AnalysisOverview
	if code := get(t, h, "/api/analysis/overview?days=30&tz=UTC", &got); code != 200 {
		t.Fatalf("overview: status %d", code)
	}
	if !got.Available || !got.Explore || got.Unit != "day" || got.Days != 30 || got.Timezone != "UTC" {
		t.Fatalf("overview header: %+v", got)
	}
	if want := now.AddDate(0, 0, -29).Format(time.DateOnly); got.Since != want || got.Until != now.Format(time.DateOnly) {
		t.Errorf("range %s..%s, want %s..%s", got.Since, got.Until, want, now.Format(time.DateOnly))
	}

	// Two of the three memories are in the last 30 days; the scan reached the
	// end of the list, so it also knows there are three.
	m := got.Memories
	if !m.Available || m.Total != 2 || m.All == nil || *m.All != 3 || m.Truncated {
		t.Errorf("memories: %+v", m)
	}
	byScope := map[string]float64{}
	for _, p := range m.Points {
		byScope[p.Series] += p.Value
	}
	if byScope["global"] != 2 || len(byScope) != 1 {
		t.Errorf("memories by scope: %v (the user-scoped one is 200 days old)", byScope)
	}

	// Documents, not chunks: two, on two different days.
	d := got.Documents
	if !d.Available || d.Total != 2 || len(d.Points) != 2 || d.All == nil || *d.All != 2 {
		t.Errorf("documents: %+v", d)
	}
	if d.Points[0].Bucket != now.AddDate(0, 0, -3).Format(time.DateOnly) || d.Points[1].Bucket != now.Format(time.DateOnly) {
		t.Errorf("document buckets: %+v", d.Points)
	}

	r := got.Recalls
	if !r.Available || r.Off || r.Total != 2 || r.Empty != 1 || len(r.Points) != 1 || r.Points[0].Recalls != 2 || r.P50 == nil || r.P95 == nil {
		t.Errorf("recalls: %+v", r)
	}

	w := got.Writes
	kinds := map[string]float64{}
	for _, p := range w.Points {
		kinds[p.Series] += p.Value
	}
	if !w.Available || w.All != nil || kinds["memory"] < 3 || kinds["knowledge"] < 2 || kinds["node"] < 3 || seriesTotal(w.Points) != float64(w.Total) {
		t.Errorf("writes: %+v by kind %v", w, kinds)
	}

	g := got.Graph
	types := map[string]int{}
	for _, tc := range g.NodeTypes {
		types[tc.Name] = tc.Count
	}
	if !g.Available || types["host"] != 2 || types["service"] != 1 || g.Nodes < 3 || g.OtherNodes != 0 {
		t.Errorf("graph nodes: %+v", g)
	}
	// The store's own filing is not the graph a reader asks about.
	for _, own := range []string{"chunk", "document", "memory"} {
		if _, listed := types[own]; listed {
			t.Errorf("%s is the store's own kind and is listed as a node type", own)
		}
	}
	if len(g.EdgeTypes) != 1 || g.EdgeTypes[0] != (AnalysisTypeCount{Name: "runs_on", Count: 2}) || g.Edges != 2 {
		t.Errorf("graph edges: %+v", g.EdgeTypes)
	}

	// A week reaches neither the memory from 200 days ago nor a longer range.
	var week AnalysisOverview
	get(t, h, "/api/analysis/overview?days=7", &week)
	if week.Memories.Total != 2 || week.Documents.Total != 2 || week.Unit != "day" {
		t.Errorf("7 days: memories %d documents %d unit %s", week.Memories.Total, week.Documents.Total, week.Unit)
	}
	var all AnalysisOverview
	get(t, h, "/api/analysis/overview?days=0", &all)
	if all.Memories.Total != 3 || all.Unit != "month" || all.Since != "" {
		t.Errorf("all time: memories %d unit %s since %q", all.Memories.Total, all.Unit, all.Since)
	}
	// The feed holds minutes of writes here, days of them on a real brain: a
	// range bucketed by month still draws them by day, and says so.
	if all.Writes.Unit != "day" || all.Writes.Points[0].Bucket != now.Format(time.DateOnly) || got.Writes.Unit != "" {
		t.Errorf("writes over all time: unit %q first bucket %+v; over 30 days unit %q", all.Writes.Unit, all.Writes.Points, got.Writes.Unit)
	}
}

// A brain without a recall log is not a broken one: the recalls chart says
// the log is off and every other chart is still drawn.
func TestTheOverviewSaysTheRecallLogIsOff(t *testing.T) {
	db, h := analysisBrain(t)
	saveDoc(t, db, "d1", nil, time.Now())
	var got AnalysisOverview
	get(t, h, "/api/analysis/overview", &got)
	if got.Recalls.Available || !got.Recalls.Off || got.Recalls.Reason == "" {
		t.Errorf("recalls: %+v", got.Recalls)
	}
	if !got.Documents.Available || got.Documents.Total != 1 || !got.Memories.Available || got.Memories.Total != 0 {
		t.Errorf("the other charts: documents %+v memories %+v", got.Documents, got.Memories)
	}
}

// A source that only draws the graph answers 200 with the reason, so the page
// says why there is nothing instead of showing a failed fetch.
func TestASourceWithNoHooksSaysItCannotBeAnalysed(t *testing.T) {
	ts := serveApp(t, theApp, "")
	resp, body := fetch(t, ts.URL+"/api/analysis/overview", nil)
	var got AnalysisOverview
	if err := json.Unmarshal([]byte(body), &got); err != nil || resp.StatusCode != 200 {
		t.Fatalf("overview: %d %s", resp.StatusCode, body)
	}
	if got.Available || got.Explore || got.Reason == "" || got.Memories.Available || got.Graph.Available || got.Recalls.Off {
		t.Errorf("overview: %+v", got)
	}
	if got.Memories.Points == nil || got.Graph.NodeTypes == nil || got.Recalls.Points == nil {
		t.Errorf("an empty list must be [] on the wire, not null: %s", body)
	}
	// The builder needs tools, which this source does not run.
	for _, path := range []string{"/api/analysis/query?measure=count", "/api/analysis/fields"} {
		if resp, body := fetch(t, ts.URL+path, nil); resp.StatusCode != http.StatusNotImplemented || !strings.Contains(body, "cannot be explored") {
			t.Errorf("GET %s: %d %s", path, resp.StatusCode, body)
		}
	}
}

// One chart's source failing leaves the others standing.
func TestAChartThatCannotBeReadDoesNotTakeTheOthersWithIt(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "partial.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	local := localCaller(db)
	src := SourceFor(db, "partial <test>")
	src.Ontology = nil
	src.ChangeLog = nil
	src.Call = func(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
		if tool == "memory_list_all" {
			return nil, fmt.Errorf("memory_list_all is refused for this key")
		}
		return local(ctx, tool, args)
	}
	sv, err := New(context.Background(), src, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	saveDoc(t, db, "d1", nil, time.Now())

	var got AnalysisOverview
	get(t, sv.Handler(), "/api/analysis/overview", &got)
	if !got.Available || got.Memories.Available || !strings.Contains(got.Memories.Reason, "refused for this key") {
		t.Errorf("memories: %+v", got.Memories)
	}
	if got.Writes.Available || got.Graph.Available || got.Writes.Reason == "" || got.Graph.Reason == "" {
		t.Errorf("writes %+v graph %+v", got.Writes, got.Graph)
	}
	if !got.Documents.Available || got.Documents.Total != 1 {
		t.Errorf("documents: %+v", got.Documents)
	}
}

// A brain whose aggregate_metadata predates measures ignores them and answers
// a plain count. The page must not draw that as the answer to its question.
func TestAnOlderAggregateIsNamedNotDrawn(t *testing.T) {
	old := func(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"results":[{"count":7,"value":7}],"total":1}`), nil
	}
	if _, err := AnalysisAsk(context.Background(), old, AnalysisQuery{Of: "chunks", Measure: "count", Timezone: "UTC"}); err == nil || !strings.Contains(err.Error(), "older than this page") {
		t.Errorf("builder: %v", err)
	}
	if _, err := AnalysisFieldSample(context.Background(), old, ""); err == nil || !strings.Contains(err.Error(), "older than this page") {
		t.Errorf("fields: %v", err)
	}
}

func askBuilder(t *testing.T, h http.Handler, query string) AnalysisResult {
	t.Helper()
	var out AnalysisResult
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/analysis/query?"+query, nil))
	if rec.Code != 200 {
		t.Fatalf("GET %s: %d %s", query, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTheBuilderMeasuresGroupsAndBuckets(t *testing.T) {
	db, h := analysisBrain(t)
	day := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	saveDoc(t, db, "d1", map[string]string{"kind": "runbook", "score": "0.5"}, day)
	saveDoc(t, db, "d2", map[string]string{"kind": "runbook", "score": "0.7"}, day.AddDate(0, 0, 1))
	saveDoc(t, db, "d3", map[string]string{"kind": "incident", "score": "n/a"}, day.AddDate(0, 1, 0))
	saveDoc(t, db, "d4", map[string]string{"score": "0.9"}, day.AddDate(0, 1, 0))

	// A bare count is one row: four documents, and more chunks than that.
	docs := askBuilder(t, h, "measure=count")
	if len(docs.Rows) != 1 || docs.Rows[0].Rows != 4 || *docs.Rows[0].Value != 4 || docs.Query.Of != "documents" {
		t.Fatalf("documents: %+v", docs)
	}
	if chunks := askBuilder(t, h, "measure=count&of=chunks"); chunks.Rows[0].Rows <= 4 {
		t.Errorf("chunks: %+v, want more rows than documents", chunks.Rows)
	}

	// Grouped: the largest group first, the documents without the field as a
	// group whose name is null, and a value that is not a number left out of
	// the average and counted as skipped, never read as 0.
	byKind := askBuilder(t, h, "measure=avg&field=score&group=kind")
	got := map[string]AnalysisRow{}
	for _, r := range byKind.Rows {
		name := "<none>"
		if r.Group != nil {
			name = *r.Group
		}
		got[name] = r
	}
	if len(got) != 3 || byKind.Rows[0].Group == nil || *byKind.Rows[0].Group != "runbook" {
		t.Fatalf("by kind: %+v", byKind.Rows)
	}
	if v := got["runbook"].Value; v == nil || *v < 0.599 || *v > 0.601 || got["runbook"].Rows != 2 {
		t.Errorf("runbook: %+v", got["runbook"])
	}
	if r := got["incident"]; r.Value != nil || r.Skipped != 1 || r.Rows != 1 {
		t.Errorf("incident: %+v, want a null average and one skipped value", r)
	}
	if r := got["<none>"]; r.Value == nil || *r.Value != 0.9 {
		t.Errorf("no kind: %+v", r)
	}

	// Bucketed by month, in time order, on the stored time.
	byMonth := askBuilder(t, h, "measure=count&bucket=month")
	if len(byMonth.Rows) != 2 || *byMonth.Rows[0].Bucket != "2026-10" || *byMonth.Rows[1].Bucket != "2026-11" || byMonth.Rows[0].Rows != 2 {
		t.Errorf("by month: %+v", byMonth.Rows)
	}
	// Both: one row per (bucket, group).
	if both := askBuilder(t, h, "measure=count&bucket=month&group=kind"); len(both.Rows) != 3 || *both.Rows[0].Bucket != "2026-10" {
		t.Errorf("month and kind: %+v", both.Rows)
	}
	// A date range is inclusive at both ends, in the zone asked for.
	if ranged := askBuilder(t, h, "measure=count&since=2026-10-14&until=2026-10-15&tz=UTC"); ranged.Rows[0].Rows != 2 {
		t.Errorf("14th to 15th: %+v", ranged.Rows)
	}
	if one := askBuilder(t, h, "measure=count&since=2026-10-15&until=2026-10-15"); one.Rows[0].Rows != 1 {
		t.Errorf("the 15th alone: %+v", one.Rows)
	}
	// The day a row falls on is the reader's, not UTC's: noon UTC on the 14th
	// is already the 15th in Kiritimati.
	if shifted := askBuilder(t, h, "measure=count&bucket=day&until=2026-10-31&tz=Pacific/Kiritimati"); *shifted.Rows[0].Bucket != "2026-10-15" || shifted.Query.Timezone != "Pacific/Kiritimati" {
		t.Errorf("another zone: %+v", shifted)
	}
	// Percentiles are the aggregation's own.
	if p := askBuilder(t, h, "measure=median&field=score"); p.Rows[0].Value == nil || *p.Rows[0].Value != 0.7 || p.Rows[0].Skipped != 1 {
		t.Errorf("median: %+v", p.Rows)
	}
}

// Nothing a caller types becomes part of a statement: a name outside the
// identifier shape is refused before any tool is asked.
func TestTheBuilderRefusesWhatIsNotAQuestion(t *testing.T) {
	asked := 0
	src := (&fakeSource{}).source()
	src.Call = func(_ context.Context, tool string, _ json.RawMessage) (json.RawMessage, error) {
		// The view follows the recall log by itself; only the builder's own
		// tools are counted.
		if tool == "aggregate_metadata" || tool == "knowledge_get" {
			asked++
		}
		return json.RawMessage(`{}`), nil
	}
	sv, err := New(context.Background(), src, time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	for _, query := range []string{
		"measure=drop",
		"measure=avg",
		"measure=avg&field=score')--",
		"measure=count&group=kind%3B+DROP+TABLE+embeddings",
		"measure=count&group=a%20b",
		"measure=count&group=" + strings.Repeat("a", 65),
		"measure=count&group=cx_value",
		"measure=count&bucket=hour",
		"measure=count&bucket=day&bucket_field=at'",
		"measure=count&of=memories",
		"measure=count&since=yesterday",
		"measure=count&since=2026-10-10&until=2026-10-01",
		"measure=count&collection=" + strings.Repeat("c", 129),
	} {
		rec := httptest.NewRecorder()
		sv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/analysis/query?"+query, nil))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("GET %s: %d %s, want 400", query, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	sv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/analysis/fields?collection="+strings.Repeat("c", 129), nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("fields with a long collection: %d", rec.Code)
	}
	if asked != 0 {
		t.Errorf("a refused question reached the brain %d times", asked)
	}
}

// A request is bounded whatever it asks: one more group than the page shows,
// so the page can say there were more.
func TestTheBuilderBoundsWhatItAsksFor(t *testing.T) {
	q, err := parseAnalysisQuery(map[string][]string{"measure": {"p95"}, "field": {"latency"}, "group": {"kind"}, "bucket": {"week"}, "bucket_field": {"at"}, "since": {"2026-10-01"}, "until": {"2026-10-31"}, "tz": {"Asia/Shanghai"}, "of": {"chunks"}, "collection": {"notes"}})
	if err != nil {
		t.Fatal(err)
	}
	req := q.request()
	if req.Limit != analysisQueryRows+1 || req.Collection != "notes" || req.Filters != nil || len(req.GroupBy) != 1 || req.GroupBy[0] != "kind" {
		t.Errorf("request: %+v", req)
	}
	if req.Measures[0] != (core.AggregationMeasure{Type: "p95", Field: "latency", Alias: analysisValueAlias}) {
		t.Errorf("measure: %+v", req.Measures)
	}
	if b := req.TimeBucket; b == nil || b.Unit != "week" || b.Field != "at" || b.Timezone != "Asia/Shanghai" || req.Order != "desc" {
		t.Errorf("bucket: %+v order %s", req.TimeBucket, req.Order)
	}
	if req.CreatedSince != "2026-10-01T00:00:00+08:00" || req.CreatedBefore != "2026-11-01T00:00:00+08:00" {
		t.Errorf("range: %s .. %s", req.CreatedSince, req.CreatedBefore)
	}
	// A zone nobody knows is UTC, and the answer says so.
	if q, _ := parseAnalysisQuery(map[string][]string{"tz": {"Mars/Olympus"}}); q.Timezone != "UTC" || q.Measure != "count" || q.Of != "documents" {
		t.Errorf("defaults: %+v", q)
	}

	// 2001 groups come back as 2000 and a flag.
	many := func(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
		var b strings.Builder
		b.WriteString(`{"results":[`)
		for i := 0; i <= analysisQueryRows; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"keys":{"kind":%d},"count":1,"values":{"cx_value":1,"cx_rows":1}}`, i)
		}
		b.WriteString(`],"total":2001}`)
		return json.RawMessage(b.String()), nil
	}
	out, err := AnalysisAsk(context.Background(), many, AnalysisQuery{Of: "chunks", Measure: "count", Group: "kind", Timezone: "UTC"})
	if err != nil || len(out.Rows) != analysisQueryRows || !out.Truncated || *out.Rows[1].Group != "1" {
		t.Errorf("capped answer: %d rows truncated=%v err=%v", len(out.Rows), out.Truncated, err)
	}
}

func TestTheFieldsAreReadFromASampleOfDocuments(t *testing.T) {
	db, h := analysisBrain(t)
	now := time.Now()
	saveDoc(t, db, "d1", map[string]string{"kind": "runbook", "score": "0.5", "due": "2026-10-01"}, now)
	saveDoc(t, db, "d2", map[string]string{"kind": "incident", "score": "12", "due": "2026-11-05T10:00:00Z", "owner": "ops"}, now.AddDate(0, -2, 0))

	var got AnalysisFields
	if code := get(t, h, "/api/analysis/fields", &got); code != 200 || got.Sampled != 2 {
		t.Fatalf("fields: %d %+v", code, got)
	}
	kinds := map[string]AnalysisField{}
	for _, f := range got.Fields {
		kinds[f.Name] = f
	}
	if kinds["score"].Kind != "number" || kinds["due"].Kind != "date" || kinds["kind"].Kind != "text" || kinds["owner"].Documents != 1 {
		t.Errorf("kinds: %+v", got.Fields)
	}
	if k := kinds["kind"]; k.Distinct != 2 || len(k.Examples) != 2 || k.Examples[0] != "incident" {
		t.Errorf("kind: %+v", k)
	}
	for name := range kinds {
		if strings.HasPrefix(name, "_") {
			t.Errorf("%s is the engine's own key and is offered as a field", name)
		}
	}
	// The fields most documents carry come first.
	if got.Fields[len(got.Fields)-1].Name != "owner" {
		t.Errorf("order: %+v", got.Fields)
	}
	if len(got.Collections) != 1 {
		t.Errorf("collections: %v", got.Collections)
	}

	// An empty brain has no fields and that is not an error.
	_, empty := analysisBrain(t)
	var none AnalysisFields
	if code := get(t, empty, "/api/analysis/fields", &none); code != 200 || none.Sampled != 0 || none.Fields == nil {
		t.Errorf("empty brain: %d %+v", code, none)
	}
}

// The analysis routes sit behind the same gate as every other.
func TestTheAnalysisRoutesNeedTheAccessToken(t *testing.T) {
	_, ts := gatedView(t, "s3cret-token")
	for _, p := range []string{"/view/api/analysis/overview", "/view/api/analysis/fields", "/view/api/analysis/query?measure=count"} {
		r, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusUnauthorized || !strings.Contains(string(b), "access token") {
			t.Errorf("GET %s without the token: %d %s", p, r.StatusCode, b)
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+p, nil)
		req.Header.Set("Authorization", "Bearer s3cret-token")
		r, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode == http.StatusUnauthorized {
			t.Errorf("GET %s with the token: still 401", p)
		}
	}
	page, err := http.Get(ts.URL + "/view/analysis")
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.StatusCode != http.StatusUnauthorized {
		t.Errorf("the page without the token: %d", page.StatusCode)
	}
}

func TestLeavingOutAnalysisTakesItsPageAndItsRoutes(t *testing.T) {
	ts := serveApp(t, theApp, "", WithoutPages(PageAnalysis))
	if resp, _ := fetch(t, ts.URL+"/analysis", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /analysis: %d, want 404", resp.StatusCode)
	}
	for _, path := range []string{"/api/analysis/overview", "/api/analysis/fields", "/api/analysis/query?measure=count"} {
		resp, body := fetch(t, ts.URL+path, nil)
		if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "leaves out") {
			t.Errorf("GET %s: %d %s, want the left-out 404", path, resp.StatusCode, body)
		}
	}
	shown := serveApp(t, theApp, "")
	if resp, _ := fetch(t, shown.URL+"/analysis", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /analysis on a view that keeps it: %d", resp.StatusCode)
	}
}

func TestTheRangePicksItsUnitAndStartsOnABucket(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("no zone database on this system")
	}
	// Saturday 14 November 2026, early morning in Shanghai: still Friday in UTC.
	now := time.Date(2026, 11, 13, 17, 30, 0, 0, time.UTC)
	for _, c := range []struct {
		days               int
		unit, since, until string
	}{
		{7, "day", "2026-11-08", "2026-11-14"},
		{30, "day", "2026-10-16", "2026-11-14"},
		// 90 days back is Monday 17 August; a week range starts on a Monday.
		{90, "week", "2026-08-17", "2026-11-09"},
		{365, "month", "2025-11-01", "2026-11"},
		{0, "month", "", "2026-11"},
	} {
		r := newAnalysisRange(c.days, shanghai, now)
		since := ""
		if !r.since.IsZero() {
			since = r.since.Format(time.DateOnly)
		}
		if r.unit != c.unit || since != c.since || r.key(r.now) != c.until {
			t.Errorf("%d days: unit %s since %s until %s, want %s %s %s", c.days, r.unit, since, r.key(r.now), c.unit, c.since, c.until)
		}
	}
	r := newAnalysisRange(7, shanghai, now)
	if r.includes(time.Date(2026, 11, 7, 15, 59, 0, 0, time.UTC)) || !r.includes(time.Date(2026, 11, 7, 16, 0, 0, 0, time.UTC)) {
		t.Error("the range starts at local midnight of its first day")
	}
	if got := analysisBucketKey(time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), "week", time.UTC); got != "2026-11-09" {
		t.Errorf("a Sunday belongs to the week of the Monday before it, got %s", got)
	}
	if got := analysisBucketKey(now, "year", shanghai); got != "2026" {
		t.Errorf("year key %s", got)
	}
	if analysisDays("") != 30 || analysisDays("x") != 30 || analysisDays("-3") != 30 || analysisDays("0") != 0 || analysisDays("99999") != analysisMaxDays {
		t.Error("days: the default is a month, 0 is everything, and a number is capped")
	}
}

// A scan stops at the start of the range, at the end of the list, or at its
// cap, and says which, so a chart never passes a partial count off as whole.
func TestAScanSaysHowFarBackItReached(t *testing.T) {
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	pages := func(n int) func() ([]int, time.Time, bool, error) {
		page := 0
		return func() ([]int, time.Time, bool, error) {
			page++
			return []int{page}, now.AddDate(0, 0, -page), page < n, nil
		}
	}
	// The list ends: everything is here.
	if s, _ := pageScan(10, time.Time{}, pages(3)); len(s.items) != 3 || s.capped || !s.covered.IsZero() {
		t.Errorf("exhausted: %+v", s)
	}
	// The range starts two and a half days ago: the third page crosses it.
	since := now.Add(-60 * time.Hour)
	if s, _ := pageScan(10, since, pages(50)); len(s.items) != 3 || s.capped || !s.covered.Equal(since) {
		t.Errorf("stopped at the range: %+v", s)
	}
	// The cap: four pages, reaching four days back.
	s, _ := pageScan(4, time.Time{}, pages(50))
	if len(s.items) != 4 || !s.capped || !s.covered.Equal(now.AddDate(0, 0, -4)) {
		t.Errorf("capped: %+v", s)
	}
	r := analysisRange{since: now.AddDate(0, 0, -30)}
	if short, covered := scanShort(s.capped, s.covered, r); !short || covered != "2026-11-10T00:00:00Z" {
		t.Errorf("a capped scan inside the range: %v %s", short, covered)
	}
	if short, _ := scanShort(s.capped, s.covered, analysisRange{since: now.AddDate(0, 0, -2)}); short {
		t.Error("a cap that fell before the range hid nothing in it")
	}

	// A kept scan answers a narrower range and not a wider one.
	s.taken = now
	if !s.covers(now.AddDate(0, 0, -1), now) || !s.covers(now.AddDate(0, 0, -400), now) {
		t.Error("a capped scan is as far as a scan goes; it is reused")
	}
	early := &analysisScan[int]{covered: since, taken: now}
	if !early.covers(now.AddDate(0, 0, -1), now) || early.covers(now.AddDate(0, 0, -7), now) || early.covers(time.Time{}, now) {
		t.Error("a scan that stopped at a range covers that range and no more")
	}
	if early.covers(now.AddDate(0, 0, -1), now.Add(analysisScanTTL+time.Second)) {
		t.Error("a scan older than its TTL is read again")
	}
}

func TestPercentilesInterpolate(t *testing.T) {
	v := []float64{10, 20, 30, 40}
	if percentileOf(v, 50) != 25 || percentileOf(v, 100) != 40 || percentileOf([]float64{7}, 95) != 7 || percentileOf(nil, 50) != 0 {
		t.Errorf("p50 %v p100 %v", percentileOf(v, 50), percentileOf(v, 100))
	}
	if got := percentileOf(v, 95); got < 38.49 || got > 38.51 {
		t.Errorf("p95 = %v, want 38.5", got)
	}
}
