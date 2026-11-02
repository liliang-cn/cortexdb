package cortexdb

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// searchWith is searchIDs with include_proposed, under ctx, returning each
// hit's review status (metadata _review_status) beside its id.
func searchWith(t *testing.T, c context.Context, db *DB, filters *RetrievalFilters) map[string]string {
	t.Helper()
	req := KnowledgeSearchRequest{Query: "heron ledger", TopK: 10, IncludeProposed: true}
	if filters != nil {
		req.Plan = &RetrievalPlan{Filters: filters}
	}
	resp, err := db.SearchKnowledge(c, req)
	if err != nil {
		t.Fatalf("search with include_proposed: %v", err)
	}
	got := map[string]string{}
	for _, h := range resp.Results {
		got[h.KnowledgeID] = h.Metadata[reviewStatusKey]
	}
	return got
}

// Under the review gate, include_proposed serves the proposed documents
// beside the accepted ones and those with no status, and each hit's
// metadata says its status. A rejected document stays out. Without the
// argument the gate hides the proposal, as before.
func TestIncludeProposedServesWhatWaitsBesideWhatIsAccepted(t *testing.T) {
	db := openReviewBrain(t, "include-proposed", WithReviewGate())
	saveReviewed(t, db)

	if got := searchIDs(t, db, "heron ledger", nil); strings.Contains(got, "k-prop") {
		t.Fatalf("without include_proposed the gate served %s", got)
	}
	got := searchWith(t, ctx(), db, nil)
	want := map[string]string{"k-plain": "", "k-prop": ReviewProposed, "k-acc": ReviewAccepted}
	if len(got) != len(want) {
		t.Fatalf("include_proposed served %v, want %v", got, want)
	}
	for id, status := range want {
		if s, ok := got[id]; !ok || s != status {
			t.Fatalf("include_proposed served %v, want %v", got, want)
		}
	}
}

// A status the plan's filters ask for decides, include_proposed or not: the
// argument widens what the gate serves, it does not replace a filter.
func TestAStatusFilterDecidesOverIncludeProposed(t *testing.T) {
	db := openReviewBrain(t, "include-proposed-filter", WithReviewGate())
	saveReviewed(t, db)

	got := searchWith(t, ctx(), db, &RetrievalFilters{ReviewStatus: ReviewRejected})
	if len(got) != 1 || got["k-rej"] != ReviewRejected {
		t.Fatalf("include_proposed with review_status=rejected served %v, want k-rej alone", got)
	}
}

// Without the gate nothing is hidden, so include_proposed changes nothing:
// the four documents are served, each with its status.
func TestIncludeProposedChangesNothingWithoutTheGate(t *testing.T) {
	db := openReviewBrain(t, "include-proposed-off")
	saveReviewed(t, db)

	got := searchWith(t, ctx(), db, nil)
	if len(got) != 4 || got["k-rej"] != ReviewRejected || got["k-prop"] != ReviewProposed {
		t.Fatalf("without the gate include_proposed served %v, want the four documents", got)
	}
}

// saveWalkReviewed writes, in collection ops, an accepted document on
// Arvid Lund and Malmo, then a proposed and a rejected document of each
// kind: "-words" shares the question's words, "-walk" shares none and
// names Malmo alone, so only a graph walk from Arvid Lund reaches it.
// k-prop-rel, proposed, states the one relation from Arvid Lund to Uppsala
// University, which k-far, accepted and with none of the question's words,
// names: a walk reaches k-far only across that relation.
func saveWalkReviewed(t *testing.T, db *DB) {
	t.Helper()
	for _, d := range []KnowledgeSaveRequest{
		{KnowledgeID: "k-acc", Content: "Arvid Lund grew up in Malmo."},
		{KnowledgeID: "k-prop-words", Content: "Arvid Lund grew up near the harbour.", ReviewStatus: ReviewProposed},
		{KnowledgeID: "k-rej-words", Content: "Arvid Lund grew up by the docks.", ReviewStatus: ReviewRejected, ReviewedBy: "r"},
		{KnowledgeID: "k-prop-walk", Content: "Malmo hosted philosophy lectures in nineteen ninety.", ReviewStatus: ReviewProposed},
		{KnowledgeID: "k-rej-walk", Content: "Malmo dropped theology lectures in nineteen ninety-two.", ReviewStatus: ReviewRejected, ReviewedBy: "r"},
		{KnowledgeID: "k-far", Content: "Uppsala University hosts philosophy lectures."},
		{KnowledgeID: "k-prop-rel", Content: "a note on studies", ReviewStatus: ReviewProposed,
			Relations: []ToolRelationInput{{From: "Arvid Lund", To: "Uppsala University", Type: "studied_at"}}},
	} {
		d.Collection = "ops"
		if _, err := db.SaveKnowledge(ctx(), d); err != nil {
			t.Fatalf("save %s: %v", d.KnowledgeID, err)
		}
	}
}

// include_proposed holds on every retrieval mode knowledge_search runs, not
// only the lexical one, as the gate does for memories
// (TestTheReviewGateHoldsOnEveryMemoryPath): each serves the proposals it
// reaches, and the graph walks (graph expansion, PPR, and the confined walk
// a caller scope takes) the one only they reach; the confined walk crosses
// the relation a proposal states. No mode serves a rejected document, and
// without the argument no mode serves a proposal or crosses its relation.
func TestIncludeProposedHoldsOnEverySearchMode(t *testing.T) {
	plain := openReviewBrain(t, "include-proposed-modes", WithReviewGate())
	saveWalkReviewed(t, plain)
	// Graph mode keeps a chunk that names no entity of the question only
	// with the relevance floors off: the walk alone reaches k-prop-walk.
	unfloored := openReviewBrain(t, "include-proposed-graph", WithReviewGate(), WithKnowledgeFloors(0, 0))
	saveWalkReviewed(t, unfloored)
	embedded := openReviewBrain(t, "include-proposed-hybrid", WithReviewGate(), WithEmbedder(wordBagEmbedder{dim: 64}))
	saveWalkReviewed(t, embedded)
	confined := WithCallerScope(ctx(), CallerScope{Key: "ops-key", Knowledge: func(c string) bool { return c == "ops" }})

	for _, tc := range []struct {
		name, mode string
		db         *DB
		c          context.Context
		always     []string
		proposed   []string // served with include_proposed only
	}{
		{"lexical", RetrievalModeLexical, plain, ctx(), []string{"k-acc"}, []string{"k-prop-words"}},
		{"hybrid", RetrievalModeHybrid, embedded, ctx(), []string{"k-acc"}, []string{"k-prop-words"}},
		{"graph", RetrievalModeGraph, unfloored, ctx(), []string{"k-acc"}, []string{"k-prop-words", "k-prop-walk"}},
		{"graph under a caller scope", RetrievalModeGraph, unfloored, confined, []string{"k-acc"}, []string{"k-prop-words", "k-prop-walk", "k-far"}},
		{"ppr", RetrievalModePPR, plain, ctx(), []string{"k-acc"}, []string{"k-prop-words", "k-prop-walk"}},
	} {
		for _, include := range []bool{false, true} {
			resp, err := tc.db.SearchKnowledge(tc.c, KnowledgeSearchRequest{Query: "Where did Arvid Lund grow up?", EntityNames: []string{"Arvid Lund"}, Collection: "ops",
				RetrievalMode: tc.mode, TopK: 10, MaxHops: 3, IncludeProposed: include})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got := hitIDs(resp)
			if slices.ContainsFunc(got, func(id string) bool { return strings.HasPrefix(id, "k-rej") }) {
				t.Fatalf("%s (include_proposed %v) served a rejected document: %v", tc.name, include, got)
			}
			for _, id := range append(slices.Clone(tc.always), tc.proposed...) {
				if served := slices.Contains(got, id); served != (include || slices.Contains(tc.always, id)) {
					t.Fatalf("%s (include_proposed %v, mode %s) served %v: %s served %v", tc.name, include, resp.Decision.EffectiveMode, got, id, served)
				}
			}
		}
	}
}

// aggregate_metadata with include_proposed counts the proposed rows beside
// the accepted ones, and not the rejected; a filter on _review_status still
// decides; representative_records, which does not take the argument, keeps
// the gate under the same call's context.
func TestIncludeProposedCountsWhatWaitsInTheAggregates(t *testing.T) {
	db := openReviewBrain(t, "include-proposed-aggregate", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	c := ctx()
	content := "The heron protocol governs the ledger."
	for _, row := range []struct{ id, status, by string }{{"a-ok", "", ""}, {"b-prop", ReviewProposed, ""}, {"c-prop", ReviewProposed, ""}, {"d-rej", ReviewRejected, "r"}} {
		if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: row.id, Content: content, Collection: "ops", ReviewStatus: row.status, ReviewedBy: row.by}); err != nil {
			t.Fatal(err)
		}
	}
	tools := db.GraphRAGTools()
	count := func(include bool, filters map[string]string) int {
		t.Helper()
		resp, err := tools.AggregateMetadata(c, ToolAggregateMetadataRequest{Type: "count", Collection: "ops", Filters: filters, IncludeProposed: include})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Results[0].Count
	}
	if got := count(false, nil); got != 1 {
		t.Fatalf("without include_proposed aggregate_metadata counted %d, want the accepted chunk alone", got)
	}
	if got := count(true, nil); got != 3 {
		t.Fatalf("include_proposed counted %d, want the accepted chunk and the two proposed", got)
	}
	if got := count(true, map[string]string{reviewStatusKey: ReviewRejected}); got != 1 {
		t.Fatalf("include_proposed with a rejected filter counted %d, want 1", got)
	}
	rep, err := tools.RepresentativeRecords(c, ToolRepresentativeRecordsRequest{Collection: "ops"})
	if err != nil || len(rep.Groups) != 1 || rep.Groups[0].Count != 1 {
		t.Fatalf("representative_records chose %+v %v, want the accepted chunk alone", rep, err)
	}
}

// The tool surface takes include_proposed by name, on both tools, as
// POST /v1/tools/<name> and CallTool hand it over (GraphRAGToolbox.Call),
// and the schemas declare it. A call under any key may send it: the gate is a
// curation filter, not access control.
func TestTheToolsTakeIncludeProposedByName(t *testing.T) {
	db := openReviewBrain(t, "include-proposed-tools", WithReviewGate())
	saveReviewed(t, db)
	tools := db.GraphRAGTools()
	reader := WithWriter(ctx(), "reader")

	out, err := tools.Call(reader, "knowledge_search", json.RawMessage(`{"query":"heron ledger","top_k":10,"include_proposed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"knowledge_id":"k-prop"`) || !strings.Contains(string(raw), `"_review_status":"proposed"`) {
		t.Fatalf("knowledge_search with include_proposed answered %s, want the proposal and its status", raw)
	}
	for args, want := range map[string]int{`{"type":"count"}`: 2, `{"type":"count","include_proposed":true}`: 3} {
		out, err = tools.Call(reader, "aggregate_metadata", json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		if resp := out.(*ToolAggregateMetadataResponse); resp.Results[0].Count != want {
			t.Fatalf("aggregate_metadata %s counted %+v, want %d", args, resp, want)
		}
	}
	declared := map[string]bool{}
	for _, def := range ToolDefinitions() {
		props, _ := def.InputSchema["properties"].(map[string]any)
		if _, ok := props["include_proposed"]; ok {
			declared[def.Name] = true
		}
	}
	if len(declared) != 2 || !declared["knowledge_search"] || !declared["aggregate_metadata"] {
		t.Fatalf("the tools that declare include_proposed are %v, want knowledge_search and aggregate_metadata", declared)
	}
}

// An expired document stays out under include_proposed, proposed or not:
// the argument widens what the review gate serves, never what expiry
// hides, in the search and in the aggregates.
func TestIncludeProposedLeavesAnExpiredProposalOut(t *testing.T) {
	db := openReviewBrain(t, "include-proposed-expired", WithReviewGate())
	c := ctx()
	past := pastInstant()
	for _, d := range []KnowledgeSaveRequest{
		{KnowledgeID: "k-live", Content: "The heron protocol governs the ledger.", ReviewStatus: ReviewProposed},
		{KnowledgeID: "k-old", Content: "The heron protocol governs the ledger.", ReviewStatus: ReviewProposed, ExpiresAt: &past},
	} {
		if _, err := db.SaveKnowledge(c, d); err != nil {
			t.Fatalf("save %s: %v", d.KnowledgeID, err)
		}
	}
	if got := searchWith(t, c, db, nil); len(got) != 1 || got["k-live"] != ReviewProposed {
		t.Fatalf("include_proposed served %v, want k-live alone", got)
	}
	resp, err := db.GraphRAGTools().AggregateMetadata(c, ToolAggregateMetadataRequest{Type: "count", IncludeProposed: true})
	if err != nil || resp.Results[0].Count != 1 {
		t.Fatalf("include_proposed counted %+v %v, want the live proposal alone", resp, err)
	}
}

// Over MCP, which decodes and checks each call against the tool's schema
// on its own path, both tools take include_proposed: the proposal is
// served and counted.
func TestIncludeProposedOverMCP(t *testing.T) {
	db := openReviewBrain(t, "include-proposed-mcp", WithReviewGate())
	saveReviewed(t, db)
	c := ctx()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := db.NewMCPServer(MCPServerOptions{}).Connect(c, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "include-proposed-test"}, nil).Connect(c, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	call := func(name string, args map[string]any) string {
		t.Helper()
		result, err := session.CallTool(c, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("%s over MCP: %v %+v", name, err, result)
		}
		payload, _ := json.Marshal(result.StructuredContent)
		return string(payload)
	}
	if got := call("knowledge_search", map[string]any{"query": "heron ledger", "top_k": 10, "include_proposed": true}); !strings.Contains(got, `"knowledge_id":"k-prop"`) || !strings.Contains(got, `"_review_status":"proposed"`) {
		t.Fatalf("knowledge_search over MCP with include_proposed: %s, want the proposal and its status", got)
	}
	if got := call("aggregate_metadata", map[string]any{"type": "count", "include_proposed": true}); !strings.Contains(got, `"count":3`) {
		t.Fatalf("aggregate_metadata over MCP with include_proposed: %s, want 3", got)
	}
}
