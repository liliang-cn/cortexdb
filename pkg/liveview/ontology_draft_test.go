package liveview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Putting the draft on the review surface.
//
// The page next to this one draws what is declared against what is there. On
// a real brain nothing is declared, so it draws one half of a comparison and
// the other half is a band of strays. The draft is what *could* be declared,
// and the whole point of putting it through the same renderer is that the
// overlay then means something: a drafted object type IS a node type, so
// every box carries its real count and the band outside the model becomes
// exactly what the deriver bucketed out or withheld.
//
// Every test here is about a way that goes wrong. Two of them matter more
// than the rest, and they are the first two: a draft that reads as a saved
// ontology is a page inviting somebody to trust a schema nobody signed, and a
// server too old to draft that renders as an empty draft is the page telling
// a reader this brain has no vocabulary when it has three hundred types.

// draftableBrain is a shared brain in miniature: the same proportions and the
// same collisions, small enough to assert about exactly.
//
//   - `entity` and one untyped node — this codebase's own word for a write
//     that named none, which is the majority of a real store;
//   - `memory`, a record kind this library's own writers stamp;
//   - `host`, `service`, `tool` — the domain vocabulary underneath, at three
//     different sizes so a threshold has something to bite on;
//   - `crate` beside `Crate`, the collision a person decides and a machine
//     must not;
//   - `mentions` out of the records, which is provenance, beside `depends_on`
//     between two declared types, which is a relation.
func draftableBrain(t *testing.T) *cortexdb.DB {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "draftable.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	var seq float32
	node := func(id, nodeType, name string) {
		t.Helper()
		seq++
		props := map[string]any{}
		if name != "" {
			props["name"] = name
		}
		if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{
			ID: id, NodeType: nodeType, Content: name,
			Vector: []float32{seq, 1, 0, 0}, Properties: props,
		}); err != nil {
			t.Fatalf("UpsertNode %s: %v", id, err)
		}
	}
	edge := func(from, edgeType, to string) {
		t.Helper()
		if err := db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
			ID: from + "|" + edgeType + "|" + to, FromNodeID: from, EdgeType: edgeType,
			ToNodeID: to, Weight: 1,
		}); err != nil {
			t.Fatalf("UpsertEdge %s: %v", from+edgeType+to, err)
		}
	}

	for _, h := range []string{"dell", "hp", "mac"} {
		node("host:"+h, "host", h)
	}
	for _, s := range []string{"api", "web", "cache"} {
		node("svc:"+s, "service", s)
	}
	// One node, so a threshold of three drops it from the drawing and must not
	// drop it from the count of what was kept out.
	node("tool:ripgrep", "tool", "ripgrep")
	node("crate:serde", "crate", "serde")
	node("crate:tokio", "crate", "tokio")
	node("Crate:anyhow", "Crate", "anyhow")
	for i := 0; i < 5; i++ {
		node(fmt.Sprintf("entity:%02d", i), "entity", fmt.Sprintf("thing %d", i))
	}
	for i := 0; i < 4; i++ {
		node(fmt.Sprintf("memory:%02d", i), "memory", fmt.Sprintf("recalled %d", i))
	}
	node("bare:0", "", "nobody typed this")

	edge("svc:api", "depends_on", "svc:cache")
	edge("svc:web", "depends_on", "svc:api")
	edge("svc:cache", "depends_on", "svc:api")
	// The other kind of collision. `crate`/`Crate` fold to one API key and the
	// schema really would match both; `depends_on`/`dependsOn` are two keys
	// and one question, so the minority spelling stays outside the model.
	edge("host:dell", "dependsOn", "host:hp")
	edge("host:dell", "runs", "svc:api")
	edge("host:hp", "runs", "svc:web")
	// Provenance: out of the records, at whatever they happened to hold.
	for i := 0; i < 4; i++ {
		edge(fmt.Sprintf("memory:%02d", i), "mentions", fmt.Sprintf("entity:%02d", i))
	}
	edge("memory:00", "mentions", "host:dell")
	edge("memory:01", "mentions", "svc:api")
	edge("memory:02", "mentions", "crate:serde")
	edge("memory:03", "mentions", "tool:ripgrep")
	return db
}

// drawnObject finds one box the diagram would draw, or fails.
func drawnObject(t *testing.T, view OntologyDraftView, name string) OntologyObjectNode {
	t.Helper()
	return objectNamed(t, view.OntologyReport, name)
}

func groupOfKind(view OntologyDraftView, kind string) (OntologyDecisionGroup, bool) {
	for _, g := range view.Decisions {
		if g.Kind == kind {
			return g, true
		}
	}
	return OntologyDecisionGroup{}, false
}

/* ---- a draft is never a saved ontology ---- */

// The first thing that must not happen. Nobody signed this schema; a page that
// let it wear a saved one's clothes would be inviting somebody to trust it.
// So the state is its own word, Saved stays false, and the schema picker —
// which offers the store's saved schemas — is empty, because a draft is not
// one of them.
func TestADraftIsNeverPresentedAsASavedOntology(t *testing.T) {
	view, err := localDraft(draftableBrain(t))(context.Background(), OntologyDraftQuery{})
	if err != nil {
		t.Fatalf("localDraft: %v", err)
	}
	if !view.Draft {
		t.Fatal("the payload does not say it is a draft, so the page has to infer it")
	}
	if view.State != OntologyDrafted {
		t.Errorf("state = %q, want %q — never one of the saved states", view.State, OntologyDrafted)
	}
	if view.Saved {
		t.Error("a draft reports itself saved")
	}
	if len(view.Schemas) != 0 {
		t.Errorf("the draft offers itself in the saved-schema picker: %v", view.Schemas)
	}
	if view.Active {
		t.Error("a draft is active")
	}
	if view.Enforcement != string(cortexdb.OntologyEnforcementVocabulary) {
		t.Errorf("enforcement = %q, want the deriver's vocabulary", view.Enforcement)
	}
	// The four saved-state words must be unreachable from a draft, or a page
	// switching on state would draw a draft with a saved sentence.
	for _, saved := range []string{OntologyLive, OntologyUnused, OntologyAbsent, OntologyUnreadable} {
		if view.State == saved {
			t.Errorf("a draft reached the saved state %q", saved)
		}
	}
}

/* ---- the same renderer, and what the overlay then means ---- */

// The reason for putting the draft through the lanes rather than beside them:
// a drafted object type IS a node type, so the overlay is not an
// approximation, it is the same number the deriver counted. A box at "not
// counted" would be the page hiding the only evidence the draft has.
func TestEveryDraftedBoxCarriesTheCountTheDeriverSawUnderIt(t *testing.T) {
	view, err := localDraft(draftableBrain(t))(context.Background(), OntologyDraftQuery{})
	if err != nil {
		t.Fatalf("localDraft: %v", err)
	}
	if !view.Usage.Available {
		t.Fatal("a draft arrived without its overlay, so every box on it says 'not counted'")
	}
	if got := drawnObject(t, view, "host").Instances; got != 3 {
		t.Errorf("host instances = %d, want the 3 written", got)
	}
	if got := drawnObject(t, view, "service").Instances; got != 3 {
		t.Errorf("service instances = %d, want the 3 written", got)
	}
	// A draft is derived from what is there, so nothing in it can be at zero.
	// If this ever fires, the draft is describing types the store does not
	// have and the page would draw them dashed as "declared, never used" —
	// which of a draft is a contradiction.
	if view.DeclaredUnusedTypes != 0 || view.DeclaredUnusedLinks != 0 {
		t.Errorf("a draft declared %d unused types and %d unused links; a draft is derived from what is there",
			view.DeclaredUnusedTypes, view.DeclaredUnusedLinks)
	}
}

// Outside the model, for a draft, is exactly what the deriver bucketed out or
// withheld — the records, the unrecognised majority, and whatever a threshold
// took. It has to be that set and not "types with no box", which is the same
// set only by accident and would stop being so the moment anything else went
// wrong.
func TestOutsideTheModelIsWhatTheDeriverBucketedOutOrWithheld(t *testing.T) {
	view, err := localDraft(draftableBrain(t))(context.Background(), OntologyDraftQuery{})
	if err != nil {
		t.Fatalf("localDraft: %v", err)
	}
	outside := map[string]int{}
	for _, s := range view.Usage.UndeclaredNodes {
		outside[s.Name] = s.Count
	}
	for _, want := range []string{"entity", "memory", ""} {
		if _, ok := outside[want]; !ok {
			t.Errorf("%q is not outside the model; the band is %v", want, strayNames(view.Usage.UndeclaredNodes))
		}
	}
	if _, drawn := outside["host"]; drawn {
		t.Error("a type the draft declares is also outside the model")
	}
	// `Crate` is NOT here, and that is the saved page's rule rather than an
	// omission: the ontology's own matching folds case, so a node typed
	// `Crate` really would validate against the drafted `crate` object type
	// and its node is counted in that box. The question of whether they are
	// one thing lives where it belongs — in the merge decision — rather than
	// being answered twice and differently by a diagram.
	if _, stray := outside["Crate"]; stray {
		t.Error("a spelling that folds onto a declared type was also drawn outside the model")
	}
	if got := drawnObject(t, view, "crate").Instances; got != 3 {
		t.Errorf("crate instances = %d, want the 3 nodes whose type folds onto it", got)
	}

	edgesOutside := map[string]int{}
	for _, s := range view.Usage.UndeclaredEdges {
		edgesOutside[s.Name] = s.Count
	}
	if _, ok := edgesOutside["mentions"]; !ok {
		t.Errorf("provenance is not outside the model: %v", strayNames(view.Usage.UndeclaredEdges))
	}
	// The collision the schema's folding does not resolve: two API keys, one
	// question, and the minority spelling really is outside the model.
	if _, ok := edgesOutside["dependsOn"]; !ok {
		t.Errorf("a minority spelling the schema cannot match is missing from the band: %v",
			strayNames(view.Usage.UndeclaredEdges))
	}
}

/* ---- the threshold ---- */

// The threshold prunes the drawing, never the vocabulary. A reader looking at
// thirty boxes has to be told that a hundred more types exist and were kept
// out by a number they chose, or a low threshold reads as a small brain.
func TestTheThresholdPrunesTheDrawingAndSaysWhatItKeptOut(t *testing.T) {
	db := draftableBrain(t)
	whole, err := localDraft(db)(context.Background(), OntologyDraftQuery{})
	if err != nil {
		t.Fatalf("localDraft: %v", err)
	}
	pruned, err := localDraft(db)(context.Background(), OntologyDraftQuery{MinNodes: 3, MinEdges: 3})
	if err != nil {
		t.Fatalf("localDraft with a threshold: %v", err)
	}

	if len(pruned.ObjectTypes) >= len(whole.ObjectTypes) {
		t.Errorf("a threshold of 3 drew %d object types against %d unthresholded",
			len(pruned.ObjectTypes), len(whole.ObjectTypes))
	}
	if whole.PrunedNodeTypes != 0 || whole.PrunedEdgeTypes != 0 {
		t.Errorf("min 0 pruned %d types and %d links", whole.PrunedNodeTypes, whole.PrunedEdgeTypes)
	}
	if pruned.PrunedNodeTypes == 0 {
		t.Fatal("the threshold kept nothing out, or the page is not told what it kept out")
	}
	if pruned.PrunedNodes < pruned.PrunedNodeTypes {
		t.Errorf("%d types kept out under %d nodes", pruned.PrunedNodeTypes, pruned.PrunedNodes)
	}
	// The threshold it was actually run at, so the page can name the number a
	// reader is being asked to disagree with.
	if pruned.MinNodes != 3 || pruned.MinEdges != 3 {
		t.Errorf("the view reports min %d/%d, want the 3/3 it was asked for", pruned.MinNodes, pruned.MinEdges)
	}
	// Kept out of the drawing, still in the band: the whole point of a
	// threshold that prunes the draft and not the report.
	found := false
	for _, s := range pruned.Usage.UndeclaredNodes {
		if s.Name == "tool" {
			found = true
		}
	}
	if !found {
		t.Errorf("a type the threshold pruned vanished instead of moving outside the model: %v",
			strayNames(pruned.Usage.UndeclaredNodes))
	}
}

/* ---- the review half ---- */

// A draft rendered without its decisions is the half that hides the work.
// Grouped, because seven merge candidates and one guessed key are two
// different amounts of reading, and a flat list of a hundred and seventeen
// says neither.
func TestTheDecisionsArriveGroupedByKindWithTheirOwnCounts(t *testing.T) {
	view, err := localDraft(draftableBrain(t))(context.Background(), OntologyDraftQuery{})
	if err != nil {
		t.Fatalf("localDraft: %v", err)
	}
	if view.DecisionsTotal == 0 {
		t.Fatal("nothing was left for a person to decide, which on this fixture is impossible")
	}
	merge, ok := groupOfKind(view, cortexdb.OntologyDecisionMerge)
	if !ok {
		t.Fatalf("no merge candidates; the groups are %v", view.Decisions)
	}
	if merge.Count != len(merge.Decisions) {
		t.Errorf("merge group counts %d and carries %d", merge.Count, len(merge.Decisions))
	}
	// Both spellings and both counts, or the reader has to go back to the
	// graph to answer the question the page just asked them.
	ev := merge.Decisions[0].Evidence
	if !strings.Contains(ev, "crate") || !strings.Contains(ev, "Crate") {
		t.Errorf("merge evidence = %q, want both spellings", ev)
	}
	if merge.Title == "" || merge.Question == "" {
		t.Error("a group arrives without saying what is being asked")
	}
	// The cardinality warning is the one that costs the most to get wrong, and
	// the deriver's own words about it must survive to the page.
	if card, has := groupOfKind(view, cortexdb.OntologyDecisionCardinality); has {
		if !strings.Contains(card.Decisions[0].Detail, "CONFLICT_KIND_CARDINALITY") {
			t.Errorf("the cardinality warning lost the conflict it warns about: %q", card.Decisions[0].Detail)
		}
	}
	total := 0
	for _, g := range view.Decisions {
		total += g.Count
	}
	if total != view.DecisionsTotal {
		t.Errorf("groups add to %d, total says %d", total, view.DecisionsTotal)
	}
}

// The deriver's own notes — "nothing here was saved", "no data type was
// inferred" — are what it says about what it did. They belong beside the
// picture, not in a JSON blob nobody opens.
func TestTheDeriversNotesReachThePage(t *testing.T) {
	view, err := localDraft(draftableBrain(t))(context.Background(), OntologyDraftQuery{})
	if err != nil {
		t.Fatalf("localDraft: %v", err)
	}
	if len(view.Notes) == 0 {
		t.Fatal("the deriver's notes were dropped")
	}
	joined := strings.Join(view.Notes, " ")
	if !strings.Contains(joined, "ontology_save") {
		t.Errorf("nothing says where saving happens: %q", joined)
	}
}

/* ---- the three new honesty states ---- */

// A store with nothing in it drafts nothing, and that is an answer rather
// than a failure. It must not arrive as a drafted schema with no types, which
// on the page is indistinguishable from a deriver that threw everything away.
func TestAnEmptyStoreHasNothingToDraftRatherThanAnEmptyDraft(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "empty.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Graph().InitGraphSchema(context.Background()); err != nil {
		t.Fatalf("schema: %v", err)
	}
	view, err := localDraft(db)(context.Background(), OntologyDraftQuery{})
	if err != nil {
		t.Fatalf("localDraft on an empty store: %v", err)
	}
	if view.State != OntologyNothingToDraft {
		t.Errorf("state = %q, want %q", view.State, OntologyNothingToDraft)
	}
	if !view.Available {
		t.Error("the store answered; unavailable means nobody asked")
	}
	if !view.Draft {
		t.Error("the payload stopped calling itself a draft")
	}
}

// The state the cluster is in today, and the one that must never look like an
// empty draft: a server that predates the tool answers "unknown tool", which
// is a fact about the server and not about the brain behind it.
func TestASourceThatCannotDraftSaysSoAndNeverLooksLikeAnEmptyDraft(t *testing.T) {
	view := unavailableDraft("this view's source cannot be asked to draft an ontology")
	if view.Available {
		t.Error("a source that cannot answer reports itself available")
	}
	if view.State != OntologyUndraftable {
		t.Errorf("state = %q, want %q — not %q, which is a claim about the store",
			view.State, OntologyUndraftable, OntologyNothingToDraft)
	}
	if view.Reason == "" {
		t.Error("no reason given, so the page has nothing to show but an empty diagram")
	}
	// Indexed by the page.
	if view.ObjectTypes == nil || view.LinkTypes == nil || view.Interfaces == nil ||
		view.Schemas == nil || view.Decisions == nil || view.Notes == nil {
		t.Error("the lists must be empty, not null")
	}
	if view.Usage.UndeclaredNodes == nil || view.Usage.UndeclaredEdges == nil {
		t.Error("the usage lists must be empty, not null")
	}
}

// A shared brain older than the tool is the common case today, and the
// message has to name the version rather than leaking a gRPC status a reader
// would have to translate. Same shape remoteContract used before the last
// upgrade.
func TestAServerTooOldToDraftIsToldApartFromABrainWithNothingInIt(t *testing.T) {
	// rpcserver maps its handler's "unknown tool" onto NotFound, which is the
	// only shape that licenses a claim about the server's version.
	reason := undraftableRemote("192.168.123.252:47821",
		status.Error(codes.NotFound, "unknown tool: ontology_draft"))
	for _, want := range []string{"cannot draft", "new enough", "v2.97.0", "192.168.123.252:47821", "ontology_draft"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason = %q, want it to carry %q", reason, want)
		}
	}
	if !strings.Contains(reason, "says nothing about what is in the brain") {
		t.Errorf("reason = %q, want it to refuse the claim about the brain it did not read", reason)
	}
}

// And a connection that broke is not a version. This view did not learn
// whether the tool exists, and a sentence saying the server lacks it would be
// inventing the one thing the failure prevented it from finding out — the
// mistake this whole surface is arranged around, pointed the other way.
func TestATransportFailureIsNotReportedAsAnOldServer(t *testing.T) {
	reason := undraftableRemote("192.168.123.252:47821",
		status.Error(codes.Unavailable, "connection refused"))
	if strings.Contains(reason, "is not a tool it has") || strings.Contains(reason, "new enough") {
		t.Errorf("a broken connection was reported as a server too old: %q", reason)
	}
	if !strings.Contains(reason, "cannot say whether") {
		t.Errorf("reason = %q, want it to admit it does not know", reason)
	}
	if !strings.Contains(reason, "connection refused") {
		t.Errorf("reason = %q, want it to carry the failure's own words", reason)
	}
}

/* ---- the endpoint ---- */

func startDraftServer(t *testing.T, hook func(context.Context, OntologyDraftQuery) (OntologyDraftView, error)) *Server {
	t.Helper()
	f := &fakeSource{}
	f.set([]Node{{ID: "entity:a", Label: "A"}}, nil)
	src := f.source()
	src.Ontology = func(context.Context, OntologyQuery) (OntologyReport, error) {
		return buildOntologyReport(nil, "", nil), nil
	}
	src.Draft = hook
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sv, err := Start(ctx, src, 0, 40*time.Millisecond, false)
	if err != nil {
		t.Fatalf("start live server: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	return sv
}

// Three because 233 link types is not something a person reviews, and the
// default has to be the reviewable one — a page whose first load is
// unreviewable teaches a reader that the page is unreviewable.
func TestTheDraftEndpointDefaultsToAReviewableThresholdAndTakesOneFromTheURL(t *testing.T) {
	seen := make(chan OntologyDraftQuery, 8)
	sv := startDraftServer(t, func(_ context.Context, q OntologyDraftQuery) (OntologyDraftView, error) {
		seen <- q
		return unavailableDraft("stub"), nil
	})

	var sink OntologyDraftView
	decodeGet(t, sv.URL()+"/api/ontology?draft=1", &sink)
	q := <-seen
	if q.MinNodes != OntologyDraftDefaultMin || q.MinEdges != OntologyDraftDefaultMin {
		t.Errorf("default min = %d/%d, want %d on both", q.MinNodes, q.MinEdges, OntologyDraftDefaultMin)
	}

	decodeGet(t, sv.URL()+"/api/ontology?draft=1&min=0", &sink)
	if q = <-seen; q.MinNodes != 0 || q.MinEdges != 0 {
		t.Errorf("min=0 arrived as %d/%d; a reader asking for everything must get everything", q.MinNodes, q.MinEdges)
	}

	decodeGet(t, sv.URL()+"/api/ontology?draft=1&min_nodes=2&min_edges=9", &sink)
	if q = <-seen; q.MinNodes != 2 || q.MinEdges != 9 {
		t.Errorf("min_nodes/min_edges arrived as %d/%d", q.MinNodes, q.MinEdges)
	}

	// A number nobody can act on is not a threshold. Falling back to the
	// default is better than drafting a hundred and twenty-four types because
	// somebody typed a word.
	decodeGet(t, sv.URL()+"/api/ontology?draft=1&min=banana", &sink)
	if q = <-seen; q.MinNodes != OntologyDraftDefaultMin {
		t.Errorf("an unreadable min became %d rather than the default", q.MinNodes)
	}
}

// The same door as the saved ontology, because it answers the same question
// with a different verb. Without ?draft=1 the endpoint must be exactly what it
// was, or every embedder pointing at it starts getting a draft.
func TestTheOntologyEndpointStillAnswersTheSavedSchemaWithoutTheDraftFlag(t *testing.T) {
	drafted := make(chan struct{}, 4)
	sv := startDraftServer(t, func(context.Context, OntologyDraftQuery) (OntologyDraftView, error) {
		drafted <- struct{}{}
		return unavailableDraft("stub"), nil
	})

	var got OntologyDraftView
	decodeGet(t, sv.URL()+"/api/ontology", &got)
	if got.Draft {
		t.Error("the plain endpoint answered with a draft")
	}
	select {
	case <-drafted:
		t.Error("the plain endpoint derived a draft nobody asked for")
	default:
	}
}

// A source with no Draft hook is not a store with nothing to draft. The two
// are the difference between "this server is too old" and "this brain is
// empty", and they are one nil check apart.
func TestTheEndpointSaysWhenTheSourceCannotBeAskedToDraft(t *testing.T) {
	sv := startDraftServer(t, nil)
	var got OntologyDraftView
	decodeGet(t, sv.URL()+"/api/ontology?draft=1", &got)
	if got.Available || got.State != OntologyUndraftable {
		t.Fatalf("view = %+v, want it to admit it cannot draft", got)
	}
	if !got.Draft {
		t.Error("the page asked for a draft and got a payload that does not say it is one")
	}
}

// A derivation that fails still answers 200, for the same reason the ontology
// read does: a failed fetch leaves the page showing what it drew last, which
// after an error is the previous store's draft presented as this one's.
func TestAFailedDerivationAnswersWithoutFailingTheRequest(t *testing.T) {
	sv := startDraftServer(t, func(context.Context, OntologyDraftQuery) (OntologyDraftView, error) {
		return OntologyDraftView{}, errors.New("draft ontology: database is locked")
	})
	var got OntologyDraftView
	decodeGet(t, sv.URL()+"/api/ontology?draft=1", &got)
	if got.State != OntologyUndraftable {
		t.Fatalf("state = %q, want %q", got.State, OntologyUndraftable)
	}
	if !strings.Contains(got.Reason, "database is locked") {
		t.Errorf("reason = %q, want it to carry the derivation's own error", got.Reason)
	}
}

/* ---- against a schema somebody did save ---- */

// When a schema is saved and a draft is asked for, the interesting question is
// neither of them on its own: it is what the store contains that the schema
// does not describe, and what the schema describes that the store has never
// held. That is a diff, and ontology_diff.go already knows how to take one.
func TestADraftAgainstASavedSchemaCarriesTheDifference(t *testing.T) {
	db := draftableBrain(t)
	if _, err := db.SaveOntologySchema(context.Background(),
		cortexdb.OntologySaveRequest{Schema: aviation(), Activate: true}); err != nil {
		t.Fatalf("save ontology: %v", err)
	}
	view, err := localDraft(db)(context.Background(), OntologyDraftQuery{})
	if err != nil {
		t.Fatalf("localDraft: %v", err)
	}
	if view.Against != "aviation" {
		t.Fatalf("against = %q, want the saved schema it was held up to", view.Against)
	}
	if view.ChangesTotal == 0 {
		t.Fatal("a draft of a store the aviation schema never touched shows no difference")
	}
	if !view.Breaking {
		t.Error("dropping every declared type is not reported as breaking")
	}
	// And the draft is still a draft: the diff is a second reading, not a
	// promotion.
	if view.Saved || view.State != OntologyDrafted {
		t.Errorf("holding a draft against a saved schema made it saved=%v state=%q", view.Saved, view.State)
	}
}

// Nothing saved is the state of a real brain, and a diff against nothing is
// not a diff. It has to be absent rather than "everything added", which reads
// as a change somebody made.
func TestADraftOfAStoreWithNoSchemaHoldsItselfAgainstNothing(t *testing.T) {
	view, err := localDraft(draftableBrain(t))(context.Background(), OntologyDraftQuery{})
	if err != nil {
		t.Fatalf("localDraft: %v", err)
	}
	if view.Against != "" || view.ChangesTotal != 0 {
		t.Errorf("a draft with no saved schema was diffed against %q with %d changes",
			view.Against, view.ChangesTotal)
	}
}

/* ---- the page ---- */

// Every switch is also a URL option, because an embedder cannot press a
// button — the rule this page already states for the four it had.
func TestTheDraftSwitchesAreAlsoUrlOptions(t *testing.T) {
	for opt, expr := range map[string]string{
		"?draft=1":     `OPTS.draft === "1"`,
		"?min=N":       `OPTS.min`,
		"?min_nodes=N": `OPTS.min_nodes`,
		"?min_edges=N": `OPTS.min_edges`,
	} {
		if !strings.Contains(ontologyHTML, expr) {
			t.Errorf("%s is not read from the URL (expected %s)", opt, expr)
		}
		if !strings.Contains(ontologyHTML, opt) {
			t.Errorf("%s is not documented beside the others it belongs with", opt)
		}
	}
	if !strings.Contains(ontologyHTML, `id="draftbtn"`) {
		t.Error("there is no way to ask for a draft without editing the URL")
	}
}

// Three more states, three more sentences, and none of them may be one of the
// saved four. A page that fell through to the saved branch would describe a
// derivation nobody signed in the words of a schema somebody did.
func TestThePageHasADistinctSentenceForEachDraftState(t *testing.T) {
	for state, sentence := range map[string]string{
		OntologyDrafted:        "Nobody has saved this.",
		OntologyNothingToDraft: "There is nothing here to draft from.",
		OntologyUndraftable:    "This source cannot draft an ontology.",
	} {
		if !strings.Contains(ontologyHTML, sentence) {
			t.Errorf("state %q has no sentence of its own; expected %q", state, sentence)
		}
		if !strings.Contains(ontologyHTML, `"`+state+`"`) {
			t.Errorf("the page never tests for state %q, so Go's decision is not the one drawn", state)
		}
	}
	// The word itself, wherever a reader's eye lands: the header chip and the
	// finding above the diagram.
	if !strings.Contains(ontologyHTML, "pill draft") {
		t.Error("the header does not mark a draft as one")
	}
	if !strings.Contains(ontologyHTML, "ontology_save") {
		t.Error("the page never says where saving happens, so it looks like this page's job")
	}
}

// The review half. A draft drawn without its decisions is the picture with
// the work hidden behind it.
func TestThePageDrawsTheDecisionsAndTheThreshold(t *testing.T) {
	for what, mark := range map[string]string{
		"the decision groups":  `id="decisions"`,
		"a count per kind":     "g.count",
		"the evidence":         "d.evidence",
		"the threshold it ran": "min_nodes",
		"what it kept out":     "pruned_node_types",
	} {
		if !strings.Contains(ontologyHTML, mark) {
			t.Errorf("the page does not draw %s (expected %s)", what, mark)
		}
	}
}

// The draft is drawn through the same lanes and curves as a saved schema, so
// the diagram must not be gated on anything a draft lacks. It used to be
// gated on rep.saved, which a draft is not.
func TestTheDiagramIsNotGatedOnSomethingADraftLacks(t *testing.T) {
	if strings.Contains(ontologyHTML, "if(!rep.saved || !(rep.object_types||[]).length)") {
		t.Error("the diagram still hides itself for anything unsaved, so a draft draws nothing")
	}
	if !strings.Contains(ontologyHTML, "renderDiagram(rep)") {
		t.Error("the draft no longer goes through the saved schema's renderer")
	}
}

/* ---- the local reader, against a real store ---- */

// The numbers on the page come off a real store or they are a shape nobody
// has seen it produce. This drafts one end to end and checks the three halves
// arrive together: the drawing, the overlay under it, and the questions.
func TestLocalDraftReadsARealStore(t *testing.T) {
	view, err := localDraft(draftableBrain(t))(context.Background(),
		OntologyDraftQuery{SchemaID: "brain", MinNodes: 2, MinEdges: 2})
	if err != nil {
		t.Fatalf("localDraft: %v", err)
	}
	if view.SchemaID != "brain" {
		t.Errorf("schema_id = %q, want the caller's", view.SchemaID)
	}
	if len(view.ObjectTypes) == 0 || len(view.LinkTypes) == 0 {
		t.Fatalf("the draft drew %d object types and %d link types",
			len(view.ObjectTypes), len(view.LinkTypes))
	}
	// The overlay comes off the deriver's own read rather than a second scan,
	// so the totals cannot disagree with the buckets beside them.
	if view.Usage.Nodes != view.SourceNodes {
		t.Errorf("the overlay counted %d nodes and the deriver read %d; two scans have disagreed",
			view.Usage.Nodes, view.SourceNodes)
	}
	if view.Usage.Scope == "" {
		t.Error("the reading does not say what it counted")
	}
	if view.Buckets[cortexdb.OntologyBucketUnclassified] == 0 {
		t.Error("the unrecognised majority is not reported, which on this fixture is impossible")
	}
	if view.DecisionsTotal == 0 {
		t.Error("no questions came back from a store built entirely out of them")
	}
	if view.DerivedAt == 0 {
		t.Error("the draft does not say when it was taken, so a slow one looks stuck")
	}
}
