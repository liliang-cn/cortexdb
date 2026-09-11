package liveview

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// A nil hook is a legitimate answer and not an oversight, and the endpoint has
// to be able to say so without a panic and without a 500 — a failed fetch
// leaves the panel showing the record it drew last, which after a click on a
// different node is one record's provenance under another record's name.
func TestTheInspectorIsNilSafeAndSaysWhichAnswerItIsGiving(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A"}}, nil)
	sv := startTestServer(t, f, false)

	var payload Payload
	decodeGet(t, sv.URL()+"/api/graph", &payload)
	if payload.Records {
		t.Error("a source with no Record hook claimed on the wire that it can look records up")
	}

	var detail RecordDetail
	decodeGet(t, sv.URL()+"/api/record?id=entity:a", &detail)
	if detail.Available {
		t.Fatal("a nil Record hook answered as if it had looked")
	}
	if detail.Reason == "" {
		t.Error("the panel is told it cannot ask and not why")
	}
	// The distinction the whole hook exists for: this must never read as a
	// record that carries nothing.
	if detail.Found {
		t.Error("a source that cannot be asked reported that it found something")
	}
	if !strings.Contains(pageHTML, "cannot look a record up") {
		t.Error("the page never says the source cannot look a record up")
	}
	if !strings.Contains(pageHTML, "not the same as a record that carries nothing") {
		t.Error("the page does not distinguish 'cannot ask' from 'carries nothing'")
	}
}

// The third answer, which is neither of the other two: the source looked, and
// the shelf no longer holds this. On a graph being written under the view that
// is a real event rather than a fault.
func TestTheInspectorTellsAMissingRecordFromASourceThatCannotAsk(t *testing.T) {
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A"}}, nil)
	src := f.source()
	src.Record = func(context.Context, string) (RecordDetail, error) {
		return notFoundRecord("entity:gone"), nil
	}
	sv := serveSource(t, src)

	var detail RecordDetail
	decodeGet(t, sv.URL()+"/api/record?id=entity:gone", &detail)
	if !detail.Available {
		t.Fatal("a source that answered was reported as unable to")
	}
	if detail.Found {
		t.Fatal("a record that is gone was reported as found")
	}
	if !strings.Contains(pageHTML, "is not on the shelf") {
		t.Error("the page never says a record on screen is no longer in the store")
	}
}

// A hook that errors is still a 200 with a report, for the same reason the
// contract endpoint is.
func TestTheInspectorReportsAFailedLookupWithoutFailingTheFetch(t *testing.T) {
	f := &fakeSource{}
	src := f.source()
	src.Record = func(context.Context, string) (RecordDetail, error) {
		return RecordDetail{}, errors.New("the shelf is locked")
	}
	sv := serveSource(t, src)

	var detail RecordDetail
	decodeGet(t, sv.URL()+"/api/record?id=x", &detail)
	if detail.Available {
		t.Error("a failed lookup answered as if it had succeeded")
	}
	if !strings.Contains(detail.Reason, "the shelf is locked") {
		t.Errorf("reason = %q, want the hook's own words", detail.Reason)
	}
}

// The panel exists to answer one question about one record, and this is that
// question asked of a real store: where did this come from, who made it, how
// well established is it, when did it become true, what does it disagree with,
// and what did anybody decide about it.
func TestTheLocalInspectorAnswersTheWholeQuestion(t *testing.T) {
	db, ctx := inspectableStore(t)
	read := localRecord(db)

	fact, err := read(ctx, "edge:works_at")
	if err != nil {
		t.Fatalf("localRecord(edge): %v", err)
	}
	if !fact.Available || !fact.Found || !fact.Edge {
		t.Fatalf("the relation came back as %+v", fact)
	}
	if fact.Grade != cortexdb.GradeAsserted {
		t.Errorf("grade = %q, want asserted", fact.Grade)
	}
	if fact.Source != "runbook.md" {
		t.Errorf("_source = %q, want the file it came from", fact.Source)
	}
	if fact.Chunk != "3" {
		t.Errorf("_chunk = %q, want the index its producer wrote", fact.Chunk)
	}
	if fact.Producer != cortexdb.ProducerLLMExtract {
		t.Errorf("_producer = %q", fact.Producer)
	}
	if fact.ValidFrom == "" {
		t.Error("the edge carries a validity and the panel would not show it")
	}
	if fact.From != "person:leo" || fact.To != "org:linbit" {
		t.Errorf("the relation's ends are %q → %q", fact.From, fact.To)
	}
	// The disagreement was kept rather than deleted, so it has to be readable.
	if len(fact.Contradicts) != 1 || fact.Contradicts[0] != "edge:works_elsewhere" {
		t.Errorf("_contradicts = %v, want the record it cannot both-be-true with", fact.Contradicts)
	}

	node, err := read(ctx, "person:leo")
	if err != nil {
		t.Fatalf("localRecord(node): %v", err)
	}
	if !node.Found || node.Edge {
		t.Fatalf("the node came back as %+v", node)
	}
	if node.Grade != cortexdb.GradeVerified {
		t.Errorf("grade = %q, want verified", node.Grade)
	}
	// The decision that verified it. Without this the panel can say a record is
	// verified and nothing about who established it, which is the half of
	// "verified" that carries the meaning.
	if len(node.Decisions) != 1 {
		t.Fatalf("decisions = %+v, want the one recorded against this node", node.Decisions)
	}
	d := node.Decisions[0]
	if d.Actor != "Wei" || d.Verdict != "confirmed" {
		t.Errorf("the decision came back as %+v, want Wei's confirmation", d)
	}
	if d.ID == "" {
		t.Error("the decision has no id, so the panel could not link to it")
	}

	// And the decision itself is a record on the same shelf, so opening it must
	// work — that is what makes the link in the panel worth having.
	dec, err := read(ctx, d.ID)
	if err != nil {
		t.Fatalf("localRecord(decision): %v", err)
	}
	if !dec.Found || dec.Type != cortexdb.DecisionNodeType {
		t.Fatalf("the decision came back as %+v", dec)
	}

	// A record nobody has ever heard of is found=false, not an error.
	gone, err := read(ctx, "entity:never-existed")
	if err != nil {
		t.Fatalf("localRecord(missing): %v", err)
	}
	if !gone.Available || gone.Found {
		t.Errorf("a record that is not there came back as %+v", gone)
	}
}

// A held or refused record carries a reason by contract, and the panel's job
// is to show the reason next to the verdict rather than the verdict alone.
func TestTheInspectorCarriesTheReasonAHeldRecordMustHave(t *testing.T) {
	db, ctx := gradedStore(t)
	got, err := localRecord(db)(ctx, "edge:held")
	if err != nil {
		t.Fatalf("localRecord: %v", err)
	}
	if got.Grade != cortexdb.GradeHeld {
		t.Fatalf("grade = %q", got.Grade)
	}
	if got.Why != "two sources disagree on the figure" {
		t.Errorf("_why = %q, want the reason its producer gave", got.Why)
	}
	if !strings.Contains(pageHTML, "the contract requires one for") {
		t.Error("the page does not mark a held record that came with no reason")
	}
}

// Every field naming another record is a link, because the reason to open
// provenance is to walk from a fact to what disagrees with it or to the
// decision that stands against it — not to read one field and retype an id.
func TestTheInspectorsPanelLinksTheIdsItShows(t *testing.T) {
	for _, want := range []string{
		"function inspectById(id)",
		"function dlink(id)",
		`html += "<div class='sub'>Contradicts</div>";`,
		`html += "<div class='sub'>Drawn from</div>";`,
		`html += "<div class='sub'>Decided</div>"`,
		`html += "<div class='sub'>Rests on</div>"`,
	} {
		if !strings.Contains(pageHTML, want) {
			t.Errorf("the inspector never draws %s", want)
		}
	}
	// A relation is a record too, and until the inspector there was nothing on
	// this page that could open one.
	if !strings.Contains(pageHTML, ".onLinkClick(onLinkClick)") {
		t.Error("an edge cannot be selected, so half the records on screen have no panel")
	}
	if !strings.Contains(pageHTML, `fetch("api/record?id=" + encodeURIComponent(id)`) {
		t.Error("the inspector does not fetch relative to the page, so it breaks under a mount prefix")
	}
	if strings.Contains(pageHTML, `fetch("/api/record`) {
		t.Error("the inspector fetches at an absolute path")
	}
}

// A property map arrives from a JSON column, so a producer that wrote a number
// wrote a number. _chunk is documented as an index and is routinely one, and a
// panel that dropped it because it was not a string would be silently missing
// the field that says where in the file this came from.
func TestContractKeysSurviveWhateverTypeTheProducerWroteThem(t *testing.T) {
	var out RecordDetail
	readContractKeys(&out, map[string]any{
		cortexdb.KeyGrade:       cortexdb.GradeAsserted,
		cortexdb.KeyChunk:       float64(3),
		cortexdb.KeyConfidence:  0.82,
		cortexdb.KeyContradicts: []any{"a", "b"},
	})
	if out.Chunk != "3" {
		t.Errorf("_chunk = %q, want 3 — a chunk index must not arrive as scientific notation", out.Chunk)
	}
	if out.Confidence != "0.82" {
		t.Errorf("_confidence = %q", out.Confidence)
	}
	if strings.Join(out.Contradicts, ",") != "a,b" {
		t.Errorf("_contradicts = %v", out.Contradicts)
	}
	// A key the record does not carry stays empty rather than becoming a blank
	// row: "nothing says" and "says the empty string" are different answers.
	if out.Source != "" || out.Why != "" {
		t.Errorf("keys the record does not carry came back as %q / %q", out.Source, out.Why)
	}
}

// serveSource starts a view over a Source the test built by hand.
func serveSource(t *testing.T, src *Source) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := Start(ctx, src, 0, 40*time.Millisecond, false)
	if err != nil {
		t.Fatalf("start live server: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	return sv
}

// inspectableStore is a shelf with everything the panel has to render: a fact
// with a source, a chunk index, a validity and a contradiction; the two things
// it joins; and a decision somebody signed against one of them.
func inspectableStore(t *testing.T) (*cortexdb.DB, context.Context) {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "inspect.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	verified := graded(cortexdb.GradeVerified, "", "")
	for _, n := range []*graph.GraphNode{
		{ID: "person:leo", Vector: []float32{1, 0, 0, 0}, NodeType: "Person", Content: "Leo",
			Properties: verified},
		{ID: "org:linbit", Vector: []float32{0, 1, 0, 0}, NodeType: "Organization", Content: "LINBIT",
			Properties: graded(cortexdb.GradeAsserted, "", "")},
	} {
		if err := db.Graph().UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}

	props := graded(cortexdb.GradeAsserted, "", "")
	props[cortexdb.KeySource] = "runbook.md"
	props[cortexdb.KeyChunk] = 3
	props[cortexdb.KeyContradicts] = []string{"edge:works_elsewhere"}
	if err := db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
		ID: "edge:works_at", FromNodeID: "person:leo", ToNodeID: "org:linbit",
		EdgeType: "works_at", Weight: 1, Properties: props,
		ValidFrom: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	if _, err := db.RecordDecision(ctx, cortexdb.DecisionRecordRequest{
		Kind:    cortexdb.DecisionKindReview,
		Actor:   "Wei",
		Verdict: "confirmed",
		Subject: "person:leo",
		Note:    "checked against the staff directory",
	}); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	return db, ctx
}
