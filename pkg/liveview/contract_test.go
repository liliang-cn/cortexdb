package liveview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// count is a node/edge pair, spelled short because these tables are mostly of
// them.
func count(nodes, edges int) graph.PropertyCount {
	return graph.PropertyCount{Nodes: nodes, Edges: edges}
}

// rowsOf renders a report's tally as "kind:grade=nodes/edges" strings, which is
// what the assertions are actually about: order, kind and counts together.
func rowsOf(rep ContractReport) []string {
	out := make([]string, 0, len(rep.Rows))
	for _, r := range rep.Rows {
		out = append(out, fmt.Sprintf("%s:%s=%d/%d", r.Kind, r.Grade, r.Nodes, r.Edges))
	}
	return out
}

func sameRows(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The tally is read top to bottom, so its order is part of the answer: the
// contract's five in the order they stand, then untagged in the same scale
// rather than as a footnote, then anything the contract does not define.
func TestContractReportOrdersTheGradesAndPutsUnknownLast(t *testing.T) {
	rep := buildContractReport(cortexdb.ContractTally{
		Verified:       count(3, 1),
		SelfConsistent: count(5, 2),
		Asserted:       count(20, 40),
		Held:           count(2, 0),
		Refused:        count(1, 1),
		Untagged:       count(900, 1200),
		Unknown: map[string]graph.PropertyCount{
			"probably": count(7, 0),
			"maybe":    count(4, 3),
		},
	}, cortexdb.NeedsAttentionResponse{})

	want := []string{
		"graded:verified=3/1",
		"graded:self_consistent=5/2",
		"graded:asserted=20/40",
		"graded:held=2/0",
		"graded:refused=1/1",
		"untagged:=900/1200",
		// Sorted, so the same store draws the same panel twice running: a row
		// that moved between refreshes would read as a change in the data.
		"unknown:maybe=4/3",
		"unknown:probably=7/0",
	}
	if got := rowsOf(rep); !sameRows(got, want) {
		t.Errorf("rows =\n  %v\nwant\n  %v", got, want)
	}
	if rep.Graded != 3+1+5+2+20+40+2+0+1+1+7+0+4+3 {
		t.Errorf("graded = %d, want every record carrying any grade, unrecognised ones included", rep.Graded)
	}
	if rep.Untagged != 2100 {
		t.Errorf("untagged = %d, want 2100", rep.Untagged)
	}
	// The store's own totals, so the panel can say it counted the whole shelf
	// rather than the six hundred nodes the scene draws.
	if rep.Nodes != 942 || rep.Edges != 1247 {
		t.Errorf("store totals = %d nodes / %d edges, want 942 / 1247", rep.Nodes, rep.Edges)
	}
}

// An unknown grade is a producer writing something wrong and untagged is a
// producer writing nothing. Folding one into the other would hide the row a
// maintainer is the only one who can act on.
func TestContractReportKeepsUnknownApartFromUntagged(t *testing.T) {
	rep := buildContractReport(cortexdb.ContractTally{
		Asserted: count(1, 0),
		Untagged: count(10, 0),
		Unknown:  map[string]graph.PropertyCount{"VERIFIED": count(2, 0)},
	}, cortexdb.NeedsAttentionResponse{})

	var untagged, unknown *ContractRow
	for i := range rep.Rows {
		switch rep.Rows[i].Kind {
		case ContractUntagged:
			untagged = &rep.Rows[i]
		case ContractUnknown:
			unknown = &rep.Rows[i]
		}
	}
	if untagged == nil || unknown == nil {
		t.Fatalf("rows = %v, want both an untagged and an unknown row", rowsOf(rep))
	}
	if untagged.Grade != "" {
		t.Errorf("untagged row carries grade %q, want none — nobody wrote one", untagged.Grade)
	}
	// The value is carried verbatim so the maintainer can go and find the
	// producer writing it.
	if unknown.Grade != "VERIFIED" {
		t.Errorf("unknown row grade = %q, want the value the producer actually wrote", unknown.Grade)
	}
}

// The state a real machine is most likely in. Five empty bars would describe a
// measurement that was taken and came back empty; none was taken.
func TestContractReportOfAnUngradedStoreDrawsNoGradedRows(t *testing.T) {
	rep := buildContractReport(cortexdb.ContractTally{
		Untagged: count(4021, 9110),
	}, cortexdb.NeedsAttentionResponse{})

	if rep.Graded != 0 {
		t.Errorf("graded = %d, want 0 on a shelf nobody stamped", rep.Graded)
	}
	if got := rowsOf(rep); !sameRows(got, []string{"untagged:=4021/9110"}) {
		t.Errorf("rows = %v, want only the untagged row", got)
	}
	if rep.Nodes != 4021 || rep.Edges != 9110 {
		t.Errorf("store totals = %d/%d, want 4021/9110 — the shelf is there, it is just ungraded",
			rep.Nodes, rep.Edges)
	}
}

// An empty store has to be distinguishable from an ungraded one: "there is
// nothing here" and "there is a lot here and none of it says how it is known"
// are different things to tell a reader.
func TestContractReportOfAnEmptyStoreIsEmpty(t *testing.T) {
	rep := buildContractReport(cortexdb.ContractTally{}, cortexdb.NeedsAttentionResponse{})
	if len(rep.Rows) != 0 {
		t.Errorf("rows = %v, want none", rowsOf(rep))
	}
	if rep.Graded != 0 || rep.Untagged != 0 || rep.Nodes != 0 || rep.Edges != 0 {
		t.Errorf("report = %+v, want every count zero", rep)
	}
	if !rep.Available {
		t.Error("an empty store is still a store this view can read")
	}
	// Rendered by a page that indexes them, so nil is a null dereference
	// waiting for the one store that has neither.
	if rep.Rows == nil || rep.Attention == nil {
		t.Error("rows and attention must be empty lists, not null")
	}
}

// Held and refused travel together and are told apart by grade, with the
// reason each producer gave. A capped list that did not say so would read as
// "this is everything".
func TestContractReportCarriesWhatNeedsAPersonAndItsTruncation(t *testing.T) {
	rep := buildContractReport(cortexdb.ContractTally{
		Held:    count(1, 0),
		Refused: count(1, 0),
	}, cortexdb.NeedsAttentionResponse{
		Records: []cortexdb.GradedRecord{
			{ID: "n1", Grade: cortexdb.GradeHeld, Why: "two sources disagree on the figure"},
			{ID: "n2", Grade: cortexdb.GradeRefused, Why: "no governed query for this metric", By: "Wei"},
		},
		Truncated: true,
		Total:     118,
	})

	if len(rep.Attention) != 2 {
		t.Fatalf("attention = %+v, want both records", rep.Attention)
	}
	if rep.Attention[0].Grade != cortexdb.GradeHeld || rep.Attention[1].Grade != cortexdb.GradeRefused {
		t.Errorf("attention grades = %q/%q, want held and refused kept apart",
			rep.Attention[0].Grade, rep.Attention[1].Grade)
	}
	for _, r := range rep.Attention {
		if r.Why == "" {
			t.Errorf("record %s has no reason, and the contract requires one for %s", r.ID, r.Grade)
		}
	}
	// A refusal says who refused when the record names one — and this is the
	// only place the name can come from.
	if rep.Attention[1].By != "Wei" {
		t.Errorf("refusal lost its _by (%q)", rep.Attention[1].By)
	}
	if !rep.Truncated || rep.Total != 118 {
		t.Errorf("truncation = %v / total %d, want the true 118 reported", rep.Truncated, rep.Total)
	}
}

/* ---- the endpoint ---- */

func contractSource(f *fakeSource, contract func(context.Context) (ContractReport, error)) *Source {
	src := f.source()
	src.Contract = contract
	return src
}

func startContractServer(t *testing.T, contract func(context.Context) (ContractReport, error)) *Server {
	t.Helper()
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A"}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := Start(ctx, contractSource(f, contract), 0, 40*time.Millisecond, false)
	if err != nil {
		t.Fatalf("start live server: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	return sv
}

func TestLiveServerServesTheContract(t *testing.T) {
	want := buildContractReport(cortexdb.ContractTally{
		Verified: count(2, 0),
		Untagged: count(50, 60),
	}, cortexdb.NeedsAttentionResponse{
		Records: []cortexdb.GradedRecord{
			{ID: "n1", Grade: cortexdb.GradeHeld, Why: "nobody has looked"},
		},
		Total: 1,
	})
	sv := startContractServer(t, func(context.Context) (ContractReport, error) { return want, nil })

	var got ContractReport
	decodeGet(t, sv.URL()+"/api/contract", &got)
	if !got.Available {
		t.Fatalf("report = %+v, want an available one", got)
	}
	if !sameRows(rowsOf(got), rowsOf(want)) {
		t.Errorf("rows = %v, want %v", rowsOf(got), rowsOf(want))
	}
	if len(got.Attention) != 1 || got.Attention[0].Why != "nobody has looked" {
		t.Errorf("attention = %+v, want the held record with its reason", got.Attention)
	}
}

// A source that keeps no contract is not a store where nothing is graded, and
// the panel must be able to tell a reader which it is looking at.
func TestLiveServerSaysWhenItCannotReadTheContract(t *testing.T) {
	sv := startContractServer(t, nil)

	var got ContractReport
	decodeGet(t, sv.URL()+"/api/contract", &got)
	if got.Available {
		t.Fatalf("report = %+v, want it to admit it cannot answer", got)
	}
	if got.Reason == "" {
		t.Error("no reason given, so the panel has nothing to show but an empty chart")
	}
	if got.Rows == nil || got.Attention == nil {
		t.Error("rows and attention must be empty lists, not null")
	}
}

// A read that fails must still answer, or the panel keeps showing the numbers
// it drew last as though they were current.
func TestLiveServerReportsAFailedContractReadWithoutFailingTheRequest(t *testing.T) {
	sv := startContractServer(t, func(context.Context) (ContractReport, error) {
		return ContractReport{}, errors.New("contract tally: database is locked")
	})

	var got ContractReport
	decodeGet(t, sv.URL()+"/api/contract", &got)
	if got.Available {
		t.Fatalf("report = %+v, want unavailable", got)
	}
	if !strings.Contains(got.Reason, "database is locked") {
		t.Errorf("reason = %q, want it to carry the read's own error", got.Reason)
	}
}

/* ---- the panel ---- */

// It behaves like its neighbours or it is a second design in one page: the
// same fold, the same remembered fold, the same one-switch-one-URL-option rule.
func TestTheContractPanelIsAUrlOptionLikeEveryOtherSwitch(t *testing.T) {
	if !strings.Contains(pageHTML, `OPTS.contract !== "0"`) {
		t.Error("the contract panel cannot be turned off from the URL, so an embedder cannot suppress it")
	}
	if !strings.Contains(pageHTML, "?contract=0") {
		t.Error("the option is not documented beside the others it belongs with")
	}
}

// Same reason the stream is relative: the page is mounted behind other
// applications' proxies, and an absolute path leaves the mount point.
func TestTheContractPanelAsksForItsDataRelatively(t *testing.T) {
	if strings.Contains(pageHTML, `fetch("/api/`) {
		t.Error("the contract is fetched at an absolute path, so the panel breaks under a prefix")
	}
	if !strings.Contains(pageHTML, `fetch("api/contract"`) {
		t.Error("the panel never asks for the contract")
	}
}

// One store, one word for how often it is read. A page that drifted from the
// constant would be polling at a cadence nothing in Go describes.
func TestTheContractPanelPollsAtTheIntervalGoDeclares(t *testing.T) {
	want := fmt.Sprintf("var CONTRACT_MS = %d;", ContractInterval.Milliseconds())
	if !strings.Contains(pageHTML, want) {
		t.Errorf("the page does not poll at ContractInterval (%s); expected %q", ContractInterval, want)
	}
}

// Untagged is a producer writing nothing and unknown is a producer writing
// something wrong. They are different findings and must not be drawn alike.
func TestTheContractPanelDrawsUnknownDifferentlyFromUntagged(t *testing.T) {
	for _, rule := range []string{".gr.untagged .gn{", ".gr.unknown .gn{", ".gr.unknown .gn::before{"} {
		if !strings.Contains(pageHTML, rule) {
			t.Errorf("the page has no %s rule, so a producer bug looks like a producer's silence", rule)
		}
	}
	// Nodes and edges share a bar and a hue but must stay separable in it.
	if !strings.Contains(pageHTML, "class='ge'") {
		t.Error("the bar never distinguishes edges from nodes")
	}
}

// The most likely state on a real machine, and the one a reader most needs the
// truth about.
func TestTheContractPanelSaysSoWhenNothingIsGraded(t *testing.T) {
	if !strings.Contains(pageHTML, "not one of them carries a grade") {
		t.Error("an ungraded store has no plain sentence to render, so it would draw empty bars")
	}
	if !strings.Contains(pageHTML, "This view cannot read the knowledge contract") {
		t.Error("a source with no contract has nothing to say, and would be shown as an ungraded store")
	}
}

/* ---- the local reader, against a real store ---- */

// graded builds the metadata a producer writes onto a record it puts here.
func graded(grade, why, by string) map[string]interface{} {
	p := map[string]interface{}{
		cortexdb.KeyGrade:    grade,
		cortexdb.KeySource:   "test-fixture",
		cortexdb.KeyProducer: cortexdb.ProducerLLMExtract,
		cortexdb.KeyAt:       "2026-09-05T00:00:00Z",
	}
	if why != "" {
		p[cortexdb.KeyWhy] = why
	}
	if by != "" {
		p[cortexdb.KeyBy] = by
		p[cortexdb.KeyProducer] = cortexdb.ProducerHuman
	}
	return p
}

// The panel's numbers come off a real shelf or they are a shape nobody has
// seen the store produce. This writes one of everything a reader has to tell
// apart — grades, an ungraded record, an undefined grade, and a held edge,
// because a graph's assertions are mostly edges — and reads it back the way
// the endpoint does.
func TestLocalContractReadsARealStore(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "shelf.db")))
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
		{ID: "precedent:p2", Vector: []float32{0, 0, 1, 0}, NodeType: "Precedent", Content: "hold stock before the season turns",
			Properties: graded(cortexdb.GradeRefused, "no governed query for this", "Wei")},
		// A producer that does not write the contract at all. On a shared
		// shelf this is most of what is there.
		{ID: "note:old", Vector: []float32{1, 1, 0, 0}, NodeType: "Note", Content: "written before the contract"},
		// A grade nobody defined: somebody's bug, and it has to surface.
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

	rep, err := localContract(db)(ctx)
	if err != nil {
		t.Fatalf("localContract: %v", err)
	}
	want := []string{
		"graded:verified=1/0",
		"graded:self_consistent=0/0",
		"graded:asserted=1/0",
		"graded:held=0/1",
		"graded:refused=1/0",
		"untagged:=1/0",
		"unknown:probably=1/0",
	}
	if got := rowsOf(rep); !sameRows(got, want) {
		t.Errorf("rows =\n  %v\nwant\n  %v", got, want)
	}
	// The held record is an edge. A tally over nodes alone would have missed
	// it entirely, and so would a needs-attention list.
	var held, refused *cortexdb.GradedRecord
	for i := range rep.Attention {
		switch rep.Attention[i].Grade {
		case cortexdb.GradeHeld:
			held = &rep.Attention[i]
		case cortexdb.GradeRefused:
			refused = &rep.Attention[i]
		}
	}
	if held == nil || refused == nil {
		t.Fatalf("attention = %+v, want both the held edge and the refused node", rep.Attention)
	}
	if !held.Edge {
		t.Error("the held record came back as a node; the panel would not say it is an assertion about two things")
	}
	if held.Why != "two sources disagree on the figure" {
		t.Errorf("held reason = %q, want the one its producer wrote", held.Why)
	}
	// A refusal says who refused, and this is the only place the name exists.
	if refused.By != "Wei" {
		t.Errorf("refusal _by = %q, want Wei", refused.By)
	}
}
