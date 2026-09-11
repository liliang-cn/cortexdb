package liveview

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// contractClosedSet is the grades the contract actually defines, read out of
// the contract rather than written down again here.
//
// ValidateContract is the only thing in the module that knows the closed set,
// and it keeps it in an unexported map. What it does export is its refusal:
// handed a grade it does not recognise it names every value it would have
// accepted. So the set is read back from that sentence.
//
// Parsing an error message is not something to do lightly, and the alternative
// here is worse: a list of five strings typed into this file, which passes
// forever and stops meaning anything the day somebody adds a sixth grade to
// contract.go. This fails on that day, which is the whole job.
func contractClosedSet(t *testing.T) []string {
	t.Helper()
	// Everything else about this record is valid, so the grade is the only
	// problem reported and the sentence has nothing else in it.
	err := cortexdb.ValidateContract(map[string]string{
		cortexdb.KeySource:   "test",
		cortexdb.KeyProducer: cortexdb.ProducerHuman,
		cortexdb.KeyBy:       "a tester",
		cortexdb.KeyGrade:    "no-such-grade",
		cortexdb.KeyAt:       time.Now().UTC().Format(time.RFC3339),
	})
	if err == nil {
		t.Fatal("ValidateContract accepted a grade the contract does not define; the closed set is no longer closed")
	}
	m := regexp.MustCompile(`is not one of \[([^\]]*)\]`).FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("ValidateContract no longer names the grades it accepts, so this test cannot read the closed set: %v", err)
	}
	set := strings.Fields(m[1])
	if len(set) == 0 {
		t.Fatalf("read an empty closed set out of %q", err.Error())
	}
	sort.Strings(set)
	return set
}

// A grade with no colour is a record the page draws in the "somebody's bug"
// rose while being perfectly well-formed. The palette has to cover the contract
// and the contract is allowed to grow, so this walks what the contract says
// rather than what this file remembers.
func TestThePaletteCoversEveryGradeTheContractDefines(t *testing.T) {
	want := contractClosedSet(t)
	for _, g := range want {
		colour, ok := gradePalette[g]
		if !ok {
			t.Errorf("the contract defines %q and the palette has no colour for it, "+
				"so the page would draw it as an unrecognised value", g)
			continue
		}
		if colour == GradeColorUnknown || colour == GradeColorUntagged {
			t.Errorf("%q is drawn in the colour reserved for a grade the contract does not define", g)
		}
	}
	// And the reverse, because a palette entry for a grade nobody defines is a
	// colour the page can never show and a reader can never see explained.
	have := make([]string, 0, len(gradePalette))
	for g := range gradePalette {
		have = append(have, g)
	}
	sort.Strings(have)
	if strings.Join(have, " ") != strings.Join(want, " ") {
		t.Errorf("palette covers %v; the contract defines %v", have, want)
	}
}

// The legend is the other half of the palette. A colour scheme with no legend
// is decoration, and a legend that names a grade without saying what the word
// means teaches nobody the difference between asserted and self_consistent —
// which is the one distinction a reader of this picture has to make.
func TestTheGradeLegendExplainsEveryRungAndTheTwoThatAreNotRungs(t *testing.T) {
	legend := GradeLegend()
	byGrade := map[string]GradeLegendEntry{}
	for _, e := range legend {
		byGrade[e.Grade] = e
		if strings.TrimSpace(e.Means) == "" {
			t.Errorf("legend row %q says what colour it is and not what it means", e.Grade)
		}
		if strings.TrimSpace(e.Color) == "" {
			t.Errorf("legend row %q has no colour", e.Grade)
		}
	}
	for _, g := range contractClosedSet(t) {
		if _, ok := byGrade[g]; !ok {
			t.Errorf("the contract defines %q and the legend never mentions it", g)
		}
	}
	// Untagged is a producer writing nothing and unknown is a producer writing
	// something wrong. Neither is a grade, both are on screen, and a reader
	// looking at a brain that is mostly dim grey has to be told which.
	for _, kind := range []string{ContractUntagged, ContractUnknown} {
		if _, ok := byGrade[kind]; !ok {
			t.Errorf("the legend has no %q row, so its colour is unexplained", kind)
		}
	}
	// The order is the contract's ladder of standing, top to bottom, which is
	// how the panel below the scene already reads.
	for i, g := range contractGradeOrder {
		if i >= len(legend) || legend[i].Grade != g {
			t.Fatalf("legend order = %v, want the contract's ladder %v first", legend, contractGradeOrder)
		}
	}
}

// The three-way decision, which is the same one the contract panel makes: a
// defined grade, no grade at all, and a word nobody agreed on.
func TestGradeColorTellsSilenceApartFromAWrongWord(t *testing.T) {
	if GradeColor(cortexdb.GradeVerified) != GradeColorVerified {
		t.Error("a defined grade did not get its own colour")
	}
	if GradeColor("") != GradeColorUntagged {
		t.Error("a record nobody stamped is not drawn as untagged")
	}
	if GradeColor("probably") != GradeColorUnknown {
		t.Error("a grade the contract does not define is not drawn as a producer bug")
	}
	if GradeColorUntagged == GradeColorUnknown {
		t.Error("silence and a wrong word are drawn alike, so the one bug a maintainer must act on is hidden")
	}
}

// The palette exists once, in Go, and is written into the page. Two copies is
// how the page came to draw asserted as a violet while the product drew it as
// a grey — the same word, two meanings, on one screen.
func TestThePageDrawsTheGoPaletteAndNotItsOwn(t *testing.T) {
	for _, colour := range []string{
		GradeColorVerified, GradeColorSelfConsistent, GradeColorAsserted,
		GradeColorHeld, GradeColorRefused, GradeColorUntagged, GradeColorUnknown,
	} {
		if !strings.Contains(pageHTML, colour) {
			t.Errorf("the page never mentions %s, so the palette did not reach it", colour)
		}
	}
	for _, token := range []string{"__GRADE_LEGEND__", "__GRADE_PALETTE__", "__GRADE_UNTAGGED__", "__GRADE_UNKNOWN__"} {
		if strings.Contains(pageHTML, token) {
			t.Errorf("the page still carries the placeholder %s", token)
		}
	}
	// Every rung's sentence has to survive the trip, or the legend is a row of
	// coloured dots with words beside them.
	for _, e := range GradeLegend() {
		if !strings.Contains(pageHTML, e.Means) {
			t.Errorf("the legend's explanation of %q never reaches the page", e.Grade)
		}
	}
}

// Type is what this page has always drawn. A caller who upgrades and touches
// nothing must get the page they had, so grade is a mode and type is where the
// page starts — in the JavaScript and in the button that is pressed.
func TestThePageStillColoursByTypeByDefault(t *testing.T) {
	if !strings.Contains(pageHTML, `var colorMode = OPTS.color === "grade" ? "grade" : "type";`) {
		t.Error("the page's default colour mode is no longer type")
	}
	if !strings.Contains(pageHTML, `<button id="cbytype" class="on">Type</button>`) {
		t.Error("the Type button is not the one pressed on load")
	}
	if strings.Contains(pageHTML, `<button id="cbygrade" class="on"`) {
		t.Error("the Grade button is pressed on load, which changes what an unchanged caller sees")
	}
	// And the accessor must fall back to type whenever grade mode is not on —
	// including when it is asked for and the source cannot answer.
	if !strings.Contains(pageHTML, `function baseColor(n){ return gradingOn() ? gradeTint(n.grade) : colorOf(n.type); }`) {
		t.Error("the colour accessor no longer falls back to node type")
	}
	if !strings.Contains(pageHTML, `function gradingOn(){ return colorMode === "grade" && sourceGrades; }`) {
		t.Error("grade mode no longer requires a source that reports grades")
	}
}

// A source that cannot report grades is not a shelf on which nothing is
// graded, and painting the whole brain the untagged grey would say the second
// while meaning the first. The page says which, in words.
func TestThePageSaysWhenASourceCannotReportGrades(t *testing.T) {
	if !strings.Contains(pageHTML, "does not report grades") {
		t.Error("the legend never distinguishes a source that cannot be asked from a store nobody graded")
	}
	f := &fakeSource{}
	f.set([]Node{{ID: "a", Label: "A", Type: "entity"}}, nil)
	sv := startTestServer(t, f, false)

	var payload Payload
	decodeGet(t, sv.URL()+"/api/graph", &payload)
	if payload.Grades {
		t.Error("a source that does not fill in grades claimed on the wire that it does")
	}
}

// The grade has to survive the whole trip: off the record's contract metadata,
// through the loader, onto the wire, for both a node and an edge — because a
// graph's assertions are mostly edges and a picture that graded only the nodes
// would report a shelf far better established than it is.
func TestAGradeReachesTheWireForNodesAndEdges(t *testing.T) {
	db, ctx := gradedStore(t)

	nodes, edges, err := LoadLocal(ctx, db.SQL())
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	byID := map[string]Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	if got := byID["metric:revenue"].Grade; got != cortexdb.GradeVerified {
		t.Errorf("verified node came back with grade %q", got)
	}
	if got := byID["note:old"].Grade; got != "" {
		t.Errorf("a record nobody stamped came back with grade %q, want the empty string", got)
	}
	if got := byID["note:odd"].Grade; got != "probably" {
		t.Errorf("a grade the contract does not define came back as %q; it must arrive verbatim "+
			"or the page cannot tell a producer's bug from a producer's silence", got)
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want the one held relation", edges)
	}
	if edges[0].Grade != cortexdb.GradeHeld {
		t.Errorf("the held edge came back with grade %q", edges[0].Grade)
	}
	if edges[0].ID != "edge:held" {
		t.Errorf("edge id = %q; without it the inspector cannot be asked about a relation", edges[0].ID)
	}

	// And through the server, which is where the page actually reads it.
	sv, err := Start(ctx, SourceFor(db, "graded"), 0, time.Hour, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	var payload Payload
	decodeGet(t, sv.URL()+"/api/graph", &payload)
	if !payload.Grades {
		t.Fatal("a local source did not declare that its reads carry grades")
	}
	var seen bool
	for _, n := range payload.Nodes {
		if n.ID == "metric:revenue" && n.Grade == cortexdb.GradeVerified {
			seen = true
		}
	}
	if !seen {
		t.Errorf("the grade did not reach the page's payload: %+v", payload.Nodes)
	}
}

// An edge whose grade changed is the same edge saying something new about
// itself. Before grades an edge was its two ends and its label — all identity,
// nothing that could change — so the diff only ever reported edges appearing
// and disappearing. A review that moved a relation from held to verified would
// have left the scene drawing last week's verdict.
func TestAnEdgeWhoseGradeChangedIsRedelivered(t *testing.T) {
	prev := Snapshot{
		Nodes: []Node{{ID: "a"}, {ID: "b"}},
		Edges: []Edge{{Source: "a", Target: "b", Label: "about", ID: "e1", Grade: cortexdb.GradeHeld}},
	}
	next := Snapshot{
		Nodes: prev.Nodes,
		Edges: []Edge{{Source: "a", Target: "b", Label: "about", ID: "e1", Grade: cortexdb.GradeVerified}},
	}
	d := Diff(prev, next)
	if len(d.AddedEdges) != 1 || d.AddedEdges[0].Grade != cortexdb.GradeVerified {
		t.Fatalf("delta added %+v, want the re-graded edge", d.AddedEdges)
	}
	if len(d.RemovedEdges) != 0 {
		t.Errorf("the re-graded edge was reported as removed as well: %+v", d.RemovedEdges)
	}
	// An unchanged edge must still be silent, or every poll wakes every page.
	if !Diff(next, next).Empty() {
		t.Error("a snapshot diffed against itself is no longer empty")
	}
}

// gradedStore writes one of everything a reader has to tell apart, the way
// contract_test.go's does, and hands back a store the loaders can read.
func gradedStore(t *testing.T) (*cortexdb.DB, context.Context) {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "graded.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, n := range []*graph.GraphNode{
		{ID: "metric:revenue", Vector: []float32{1, 0, 0, 0}, NodeType: "Metric", Content: "revenue",
			Properties: graded(cortexdb.GradeVerified, "", "")},
		{ID: "claim:reuters", Vector: []float32{0, 1, 0, 0}, NodeType: "Claim", Content: "Reuters said so",
			Properties: graded(cortexdb.GradeAsserted, "", "")},
		{ID: "note:old", Vector: []float32{1, 1, 0, 0}, NodeType: "Note", Content: "written before the contract"},
		{ID: "note:odd", Vector: []float32{0, 1, 1, 0}, NodeType: "Note", Content: "probably true",
			Properties: graded("probably", "", "")},
	} {
		if err := db.Graph().UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}
	if err := db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
		ID: "edge:held", FromNodeID: "claim:reuters", ToNodeID: "metric:revenue",
		EdgeType: "about", Weight: 1,
		Properties: graded(cortexdb.GradeHeld, "two sources disagree on the figure", ""),
	}); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	return db, ctx
}
