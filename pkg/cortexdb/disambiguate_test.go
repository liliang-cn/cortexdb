package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// newDisambiguationDB opens an empty database for one test.
func newDisambiguationDB(t *testing.T) (*DB, *GraphRAGToolbox, context.Context) {
	t.Helper()
	dbPath := fmt.Sprintf("test_disambiguate_%d.db", testname.Nano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	return db, db.GraphRAGTools(), context.Background()
}

func seedDisambiguationGraph(t *testing.T, tools *GraphRAGToolbox, ctx context.Context, entities []ToolEntityInput, relations []ToolRelationInput) {
	t.Helper()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
		t.Fatalf("seed entities: %v", err)
	}
	if len(relations) == 0 {
		return
	}
	resp, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: relations})
	if err != nil {
		t.Fatalf("seed relations: %v", err)
	}
	if len(resp.Rejected) > 0 {
		t.Fatalf("seed relations rejected: %+v", resp.Rejected)
	}
}

// zikaTestDB is the worked example from Knowledge Graphs and LLMs in Action,
// ch. 8, built so that string matching and the graph disagree.
//
// "Zika" matches two nodes by containment. The one string matching prefers is
// "Zika Forest" — the place the virus was named after — because it is the
// shorter name, and shortness is the only thing a string comparison has left
// once both candidates match the same way. The one the graph supports is the
// disease, and the support is a fact somebody wrote down: it causes
// microcephaly.
func zikaTestDB(t *testing.T) (*DB, *GraphRAGToolbox, context.Context) {
	t.Helper()
	db, tools, ctx := newDisambiguationDB(t)
	seedDisambiguationGraph(t, tools, ctx,
		[]ToolEntityInput{
			{Name: "Zika Forest", Type: "Place"},
			{Name: "Congenital Zika virus infection", Type: "Disease"},
			{Name: "microcephaly", Type: "Symptom"},
			{Name: "birth defect", Type: "Category"},
			{Name: "Uganda", Type: "Place"},
			// Connected to nothing at all, for the case where the graph has a
			// node and no opinion about it.
			{Name: "Rift Valley fever", Type: "Disease"},
		},
		[]ToolRelationInput{
			{From: "Congenital Zika virus infection", To: "microcephaly", Type: "causes"},
			{From: "microcephaly", To: "birth defect", Type: "is_a"},
			{From: "Zika Forest", To: "Uganda", Type: "located_in"},
		},
	)
	return db, tools, ctx
}

// TestDisambiguationBeatsStringMatching is the whole claim of the method: the
// candidate the strings prefer loses to the candidate the graph connects to
// something else in the same sentence.
func TestDisambiguationBeatsStringMatching(t *testing.T) {
	db, tools, ctx := zikaTestDB(t)

	// First, what string matching alone says, so the comparison is on the record
	// rather than asserted in a comment.
	found, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Zika"}})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}
	if got := found.Matches[0].Nodes[0].Content; got != "Zika Forest" {
		t.Fatalf("fixture no longer sets up the disagreement: string matching picked %q", got)
	}

	res, err := db.DisambiguateMentions(ctx, []string{"Zika", "microcephaly"}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	zika := res.Mentions[0]
	if zika.Mention != "Zika" {
		t.Fatalf("mentions must come back in the order given, got %q first", zika.Mention)
	}
	if zika.Unresolved {
		t.Fatalf("expected Zika to resolve, got unresolved (%s): %+v", zika.Reason, zika.Candidates)
	}
	if got := zika.Candidates[0].Name; got != "Congenital Zika virus infection" {
		t.Fatalf("expected the connected candidate to win, got %q", got)
	}
	if zika.Candidates[0].SupportingMentions != 1 {
		t.Fatalf("expected support from one co-occurring mention, got %d", zika.Candidates[0].SupportingMentions)
	}

	// The evidence is the answer's reason, so it has to be present and readable.
	evidence := zika.Candidates[0].Evidence
	if len(evidence) != 1 {
		t.Fatalf("expected one supporting path, got %d", len(evidence))
	}
	if evidence[0].Sentence != "Congenital Zika virus infection causes microcephaly." {
		t.Fatalf("path was not rendered as a sentence: %q", evidence[0].Sentence)
	}
	if evidence[0].Length != 1 || evidence[0].ToMention != "microcephaly" {
		t.Fatalf("unexpected evidence shape: %+v", evidence[0])
	}
	if len(evidence[0].EdgeIDs) != 1 {
		t.Fatalf("evidence must name the edges it rests on, got %+v", evidence[0].EdgeIDs)
	}
}

// TestDisambiguationKeepsTheLosingCandidate guards the part that is tempting to
// optimise away. A caller comparing one supported candidate against one
// supported and one unsupported is looking at two different situations, and can
// only tell them apart if the loser comes back.
func TestDisambiguationKeepsTheLosingCandidate(t *testing.T) {
	db, _, ctx := zikaTestDB(t)

	res, err := db.DisambiguateMentions(ctx, []string{"Zika", "microcephaly"}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	zika := res.Mentions[0]
	if len(zika.Candidates) != 2 {
		t.Fatalf("expected both candidates back, got %d", len(zika.Candidates))
	}
	loser := zika.Candidates[1]
	if loser.Name != "Zika Forest" {
		t.Fatalf("expected Zika Forest to be ranked second, got %q", loser.Name)
	}
	if loser.Score != 0 || loser.SupportingMentions != 0 {
		t.Fatalf("the unsupported candidate must score nothing, got %+v", loser)
	}
	if len(loser.Evidence) != 0 {
		t.Fatalf("the unsupported candidate must come back with empty evidence, got %+v", loser.Evidence)
	}
}

// TestDisambiguationFollowsTheContextItIsGiven changes only the co-occurring
// mention and expects the other reading to win. Nothing about the ambiguous
// string changed, which is the point: the context is doing the work.
func TestDisambiguationFollowsTheContextItIsGiven(t *testing.T) {
	db, _, ctx := zikaTestDB(t)

	res, err := db.DisambiguateMentions(ctx, []string{"Zika", "Uganda"}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	zika := res.Mentions[0]
	if zika.Unresolved {
		t.Fatalf("expected Zika to resolve against Uganda, got unresolved (%s)", zika.Reason)
	}
	if got := zika.Candidates[0].Name; got != "Zika Forest" {
		t.Fatalf("expected the place, got %q", got)
	}
	if got := zika.Candidates[0].Evidence[0].Sentence; got != "Zika Forest located in Uganda." {
		t.Fatalf("unexpected sentence: %q", got)
	}
}

// TestDisambiguationReportsUnresolvedRatherThanGuessing covers the contract this
// repository is built on. A mention whose candidates connect to nothing gets an
// admission, not the first candidate wearing an answer's clothes.
func TestDisambiguationReportsUnresolvedRatherThanGuessing(t *testing.T) {
	db, _, ctx := zikaTestDB(t)

	res, err := db.DisambiguateMentions(ctx, []string{"Rift Valley fever", "microcephaly"}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	for _, mention := range res.Mentions {
		if !mention.Unresolved {
			t.Fatalf("%q should be unresolved: %+v", mention.Mention, mention.Candidates)
		}
		if mention.Reason != DisambiguationNoSupport {
			t.Fatalf("%q: expected %q, got %q", mention.Mention, DisambiguationNoSupport, mention.Reason)
		}
		// An honest absence: nothing bounded the search, so "not connected"
		// means not connected.
		if mention.SearchIncomplete || mention.ConnectedBeyondMaxLength {
			t.Fatalf("%q reported a bound that was never hit: %+v", mention.Mention, mention)
		}
		if len(mention.Candidates) == 0 {
			t.Fatalf("%q: unresolved must still show what it was choosing between", mention.Mention)
		}
	}

	// A name the graph has never heard of is a different failure and says so.
	res, err = db.DisambiguateMentions(ctx, []string{"Nipah", "microcephaly"}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	if res.Mentions[0].Reason != DisambiguationNoCandidates {
		t.Fatalf("expected %q, got %q", DisambiguationNoCandidates, res.Mentions[0].Reason)
	}
}

// TestDisambiguationDistinguishesAPathLengthBoundFromAnAbsence is the
// distinction the response must never blur: "there is no connection" and "I did
// not look that far" lead to opposite next moves.
func TestDisambiguationDistinguishesAPathLengthBoundFromAnAbsence(t *testing.T) {
	db, _, ctx := zikaTestDB(t)

	// "birth defect" is two hops from the disease, through microcephaly.
	bounded, err := db.DisambiguateMentions(ctx, []string{"Zika", "birth defect"}, DisambiguationOptions{MaxPathLength: 1})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	zika := bounded.Mentions[0]
	if !zika.Unresolved || zika.Reason != DisambiguationNoSupport {
		t.Fatalf("expected an unresolved mention, got %+v", zika)
	}
	if !zika.ConnectedBeyondMaxLength {
		t.Fatalf("a connection was found and discarded for length; the answer must say so: %+v", zika)
	}
	if bounded.Bounds.MaxPathLength != 1 {
		t.Fatalf("the run must echo the bounds it used, got %+v", bounded.Bounds)
	}

	// Widening the bound is the move that flag is telling the caller to make,
	// and it has to actually work.
	widened, err := db.DisambiguateMentions(ctx, []string{"Zika", "birth defect"}, DisambiguationOptions{MaxPathLength: 2})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	zika = widened.Mentions[0]
	if zika.Unresolved {
		t.Fatalf("expected two hops to resolve it, got unresolved (%s)", zika.Reason)
	}
	if zika.ConnectedBeyondMaxLength {
		t.Fatalf("nothing was discarded this time: %+v", zika)
	}
	if got := zika.Candidates[0].Evidence[0].Sentence; got != "Congenital Zika virus infection causes microcephaly, and microcephaly is a birth defect." {
		t.Fatalf("multi-hop path was not rendered as a sentence: %q", got)
	}
	if got := zika.Candidates[0].Score; got != 0.5 {
		t.Fatalf("a two-hop path should be worth half a one-hop path, got %v", got)
	}
}

// TestDisambiguationReportsAnExhaustedSearchBudget is the same distinction for
// the other bound. Here the surviving search is the one between the two
// candidates string matching liked best, which finds nothing — so the answer
// looks exactly like a genuine absence unless the budget flag says otherwise.
func TestDisambiguationReportsAnExhaustedSearchBudget(t *testing.T) {
	db, _, ctx := zikaTestDB(t)

	res, err := db.DisambiguateMentions(ctx, []string{"Zika", "microcephaly"}, DisambiguationOptions{MaxPathSearches: 1})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	if !res.SearchBudgetExhausted {
		t.Fatalf("the budget stopped the run and the response did not say so: %+v", res)
	}
	if res.SearchesPerformed != 1 {
		t.Fatalf("expected exactly one search, got %d", res.SearchesPerformed)
	}
	for _, mention := range res.Mentions {
		if !mention.SearchIncomplete {
			t.Fatalf("%q lost a pair to the budget and must say so: %+v", mention.Mention, mention)
		}
		if !mention.Unresolved || mention.Reason != DisambiguationNoSupport {
			t.Fatalf("%q: expected an unresolved mention, got %+v", mention.Mention, mention)
		}
	}

	// The same question with the budget the work actually needs.
	full, err := db.DisambiguateMentions(ctx, []string{"Zika", "microcephaly"}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	if full.SearchBudgetExhausted || full.Mentions[0].SearchIncomplete {
		t.Fatalf("nothing should have been bounded: %+v", full)
	}
	if full.Mentions[0].Unresolved {
		t.Fatalf("expected the unbounded run to resolve Zika")
	}
}

// TestDisambiguationReportsATie covers the other way the top of the ranking can
// fail to be an answer: the graph supports two readings exactly as well, so
// whichever one sorts first got there by string length.
func TestDisambiguationReportsATie(t *testing.T) {
	db, tools, ctx := newDisambiguationDB(t)
	seedDisambiguationGraph(t, tools, ctx,
		[]ToolEntityInput{
			{Name: "Mercury the planet", Type: "Planet"},
			{Name: "Mercury the element", Type: "Element"},
			{Name: "Venus", Type: "Planet"},
			{Name: "thermometer", Type: "Instrument"},
		},
		[]ToolRelationInput{
			{From: "Mercury the planet", To: "Venus", Type: "orbits near"},
			{From: "Mercury the element", To: "thermometer", Type: "used in"},
		},
	)

	res, err := db.DisambiguateMentions(ctx, []string{"Mercury", "Venus", "thermometer"}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	mercury := res.Mentions[0]
	if len(mercury.Candidates) != 2 {
		t.Fatalf("expected two readings of Mercury, got %d", len(mercury.Candidates))
	}
	if !mercury.Unresolved || mercury.Reason != DisambiguationTie {
		t.Fatalf("equal support is not an answer; expected %q, got %+v", DisambiguationTie, mercury)
	}
	if mercury.Candidates[0].Score == 0 || mercury.Candidates[0].Score != mercury.Candidates[1].Score {
		t.Fatalf("expected two equally supported candidates, got %v and %v",
			mercury.Candidates[0].Score, mercury.Candidates[1].Score)
	}
}

// TestDisambiguationNeedsContext covers the input the method cannot work with.
// One mention has no co-occurrence to reason from, and blaming the graph for
// that would send a caller looking for missing facts that are not missing.
func TestDisambiguationNeedsContext(t *testing.T) {
	db, _, ctx := zikaTestDB(t)

	res, err := db.DisambiguateMentions(ctx, []string{"Zika"}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	if !res.Mentions[0].Unresolved || res.Mentions[0].Reason != DisambiguationNoContext {
		t.Fatalf("expected %q, got %+v", DisambiguationNoContext, res.Mentions[0])
	}
	if len(res.Mentions[0].Candidates) != 2 {
		t.Fatalf("the candidates are still worth returning, got %d", len(res.Mentions[0].Candidates))
	}
	if res.SearchesPerformed != 0 {
		t.Fatalf("there was nothing to search, got %d searches", res.SearchesPerformed)
	}

	// The same mention twice is one mention seen twice. If it were treated as
	// context for itself, every repeated word would resolve itself.
	repeated, err := db.DisambiguateMentions(ctx, []string{"Zika", " Zika "}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	if len(repeated.Mentions) != 1 || repeated.Mentions[0].Reason != DisambiguationNoContext {
		t.Fatalf("duplicates must collapse, got %+v", repeated.Mentions)
	}

	if _, err := db.DisambiguateMentions(ctx, []string{"  "}, DisambiguationOptions{}); !errors.Is(err, ErrNoMentions) {
		t.Fatalf("expected ErrNoMentions, got %v", err)
	}
}

// TestDisambiguationCandidateCapIsReported guards the third bound. A cap that
// cut the right reading out of the candidate list is not something the caller
// can see any other way.
func TestDisambiguationCandidateCapIsReported(t *testing.T) {
	db, _, ctx := zikaTestDB(t)

	res, err := db.DisambiguateMentions(ctx, []string{"Zika", "microcephaly"}, DisambiguationOptions{MaxCandidatesPerMention: 1})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	zika := res.Mentions[0]
	if !zika.CandidatesTruncated {
		t.Fatalf("the cap dropped a candidate and the answer must say so: %+v", zika)
	}
	if len(zika.Candidates) != 1 || zika.Candidates[0].Name != "Zika Forest" {
		t.Fatalf("expected only the best string match to survive the cap, got %+v", zika.Candidates)
	}
	if !zika.Unresolved || zika.Reason != DisambiguationNoSupport {
		t.Fatalf("the surviving candidate has no support and must not be presented as the answer: %+v", zika)
	}
}

// TestDisambiguateMentionsToolSurface checks the tool wrapper passes the bounds
// through, since a tool caller that cannot set them has no way to control what
// the call costs.
func TestDisambiguateMentionsToolSurface(t *testing.T) {
	_, tools, ctx := zikaTestDB(t)

	res, err := tools.DisambiguateMentions(ctx, ToolDisambiguateMentionsRequest{
		Mentions:      []string{"Zika", "birth defect"},
		NodeTypes:     []string{"Disease", "Category"},
		MaxPathLength: 2,
	})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	zika := res.Mentions[0]
	// The type filter alone removes the place, so one candidate is left — and it
	// is supported, which is what separates this from the unresolved cases.
	if len(zika.Candidates) != 1 || zika.Candidates[0].Name != "Congenital Zika virus infection" {
		t.Fatalf("node_types filter did not reach find_nodes: %+v", zika.Candidates)
	}
	if zika.Unresolved {
		t.Fatalf("expected the surviving candidate to be supported, got %s", zika.Reason)
	}
	if res.Bounds.MaxPathLength != 2 || res.Bounds.MaxPathSearches != defaultDisambiguationSearches {
		t.Fatalf("bounds were not passed through and defaulted: %+v", res.Bounds)
	}
}

// TestHumanizeEdgeType covers the sentence rendering directly, because edge
// labels come out of extraction in every shape people write them in and a
// sentence is only evidence if it reads like one.
func TestHumanizeEdgeType(t *testing.T) {
	cases := map[string]string{
		"causes":       "causes",
		"CAUSES":       "causes",
		"is_a":         "is a",
		"located-in":   "located in",
		"isTreatedBy":  "is treated by",
		"":             "is related to",
		"   ":          "is related to",
		"引起":           "引起",
		"HAS_SYMPTOM":  "has symptom",
		"treats2Cases": "treats2 cases",
	}
	for in, want := range cases {
		if got := humanizeEdgeType(in); got != want {
			t.Fatalf("humanizeEdgeType(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDisambiguationSentenceNamesEveryHop keeps the rendering honest about
// direction: the graph's shortest path only walks edges forwards, so a
// connection found from the other end must still read the way the fact was
// written down.
func TestDisambiguationSentenceNamesEveryHop(t *testing.T) {
	db, _, ctx := zikaTestDB(t)

	// microcephaly is the tail of the causes edge, so the connecting path is
	// found by searching from the Zika candidate, not from this mention.
	res, err := db.DisambiguateMentions(ctx, []string{"microcephaly", "Zika"}, DisambiguationOptions{})
	if err != nil {
		t.Fatalf("disambiguate: %v", err)
	}
	micro := res.Mentions[0]
	if micro.Unresolved {
		t.Fatalf("expected microcephaly to resolve, got %s", micro.Reason)
	}
	got := micro.Candidates[0].Evidence[0].Sentence
	if !strings.HasPrefix(got, "Congenital Zika virus infection causes") {
		t.Fatalf("the sentence must follow the edge as stored, got %q", got)
	}
	if micro.Candidates[0].Evidence[0].ToMention != "Zika" {
		t.Fatalf("evidence must name the mention it supports against: %+v", micro.Candidates[0].Evidence[0])
	}
}
