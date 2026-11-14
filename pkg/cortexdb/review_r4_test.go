package cortexdb

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// saveWalkMemories writes three memories that mention one entity: one with
// no status, one proposed, one rejected.
func saveWalkMemories(t *testing.T, db *DB) {
	t.Helper()
	for _, m := range []struct{ id, content, status, by string }{
		{"m-ok", "Kestrel Vale keeps the open ledger.", "", ""},
		{"m-prop", "Kestrel Vale keeps the secret plan.", ReviewProposed, ""},
		{"m-rej", "Kestrel Vale keeps the burned map.", ReviewRejected, "r"},
	} {
		if _, err := db.SaveMemory(ctx(), MemorySaveRequest{MemoryID: m.id, Scope: MemoryScopeGlobal, Content: m.content,
			ReviewStatus: m.status, ReviewedBy: m.by, Entities: []ToolEntityInput{{Name: "Kestrel Vale"}}}); err != nil {
			t.Fatalf("save %s: %v", m.id, err)
		}
	}
}

// walkBodies runs every tool that walks the graph from the entity and
// returns each answer as JSON.
func walkBodies(t *testing.T, db *DB) map[string]string {
	t.Helper()
	kestrel := EntityNodeID("Kestrel Vale")
	out := map[string]string{}
	for name, args := range map[string]string{
		"knowledge_memory_neighbors":             `{"node_id":"` + kestrel + `","depth":1}`,
		"knowledge_memory_expand_entity_context": `{"entity_names":["Kestrel Vale"],"max_hops":1}`,
		"expand_graph":                           `{"node_ids":["` + kestrel + `"],"max_hops":1}`,
		"expand_graph with scores":               `{"node_ids":["` + kestrel + `"],"max_hops":1,"relation_policies":{"mentions":{"weight":1}}}`,
		"search_paths":                           `{"entity_names":["Kestrel Vale"],"include_bookkeeping":true,"direction":"both","max_depth":1}`,
		"get_nodes":                              `{"node_ids":["memory:m-ok","memory:m-prop","memory:m-rej"]}`,
	} {
		resp, err := db.GraphRAGTools().Call(ctx(), strings.TrimSuffix(name, " with scores"), json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, _ := json.Marshal(resp)
		out[name] = string(body)
	}
	return out
}

// Under the gate, a proposed or rejected memory is out of the graph walks
// as it is out of recall: its node, and the text on it, are not returned.
// A memory with no status is.
func TestAProposedOrRejectedMemoryIsOutOfTheGraphWalks(t *testing.T) {
	db := openReviewBrain(t, "review-memory-walks", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	saveWalkMemories(t, db)
	for name, body := range walkBodies(t, db) {
		for _, leak := range []string{"secret plan", "burned map", "memory:m-prop", "memory:m-rej"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s returned a memory the gate hides (%s): %.400s", name, leak, body)
			}
		}
		if !strings.Contains(body, "memory:m-ok") {
			t.Errorf("%s lost the memory with no status, so its check proves nothing: %.400s", name, body)
		}
	}
	tools := db.GraphRAGTools()
	if _, err := tools.Call(ctx(), "knowledge_memory_shortest_path", json.RawMessage(`{"from_node_id":"memory:m-prop","to_node_id":"`+EntityNodeID("Kestrel Vale")+`"}`)); err == nil || !strings.Contains(err.Error(), "no path") {
		t.Errorf("a path from a proposed memory: %v, want no path", err)
	}
	if _, err := tools.Call(ctx(), "knowledge_memory_shortest_path", json.RawMessage(`{"from_node_id":"memory:m-ok","to_node_id":"`+EntityNodeID("Kestrel Vale")+`"}`)); err != nil {
		t.Errorf("a path from the memory with no status: %v", err)
	}
	resp, err := db.SearchMemory(ctx(), MemorySearchRequest{Query: "Kestrel Vale keeps", Scope: MemoryScopeGlobal, TopK: 10, RetrievalMode: RetrievalModeLexical})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Errorf("memory_search lost the memory with no status, so its check proves nothing")
	}
	for _, h := range resp.Results {
		if h.Memory.ID != "m-ok" {
			t.Errorf("memory_search served %s under the gate", h.Memory.ID)
		}
	}
}

// Without the gate the walks serve every memory, as on main.
func TestWithoutTheGateTheWalksServeEveryMemory(t *testing.T) {
	db := openReviewBrain(t, "review-memory-walks-off", WithEmbedder(NewMockEmbedder(8)))
	saveWalkMemories(t, db)
	if body := walkBodies(t, db)["knowledge_memory_neighbors"]; !strings.Contains(body, "memory:m-prop") || !strings.Contains(body, "memory:m-rej") {
		t.Errorf("without the gate the neighbors lost a memory: %.400s", body)
	}
}

// Under the gate, a walk over memories that cannot be read hides them all:
// an outage does not lift the gate.
func TestAWalkHidesEveryMemoryWhenTheMemoriesCannotBeRead(t *testing.T) {
	db := openReviewBrain(t, "review-memory-walks-outage", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	saveWalkMemories(t, db)
	if _, err := db.SQL().Exec(`ALTER TABLE messages RENAME COLUMN metadata TO metadata_hidden`); err != nil {
		t.Fatal(err)
	}
	if body := walkBodies(t, db)["knowledge_memory_neighbors"]; strings.Contains(body, "memory:") {
		t.Errorf("a walk over unreadable memories served one: %.400s", body)
	}
}

// Under the gate, a proposed or rejected memory is not promoted: the
// promotion would serve its text as knowledge before anyone accepts it.
// Without the gate the status is plain metadata and the promotion runs.
func TestPromotionRefusesAMemoryTheGateHides(t *testing.T) {
	db := openReviewBrain(t, "review-promote", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	saveWalkMemories(t, db)
	brain := db.KnowledgeMemory()
	for _, id := range []string{"m-prop", "m-rej"} {
		_, err := brain.PromoteToKnowledge(ctx(), KnowledgeMemoryPromoteToKnowledgeRequest{MemoryIDs: []string{"m-ok", id}, KnowledgeID: "k-" + id})
		if err == nil || !strings.Contains(err.Error(), id) {
			t.Errorf("promotion of %s under the gate: %v, want a refusal that names it", id, err)
		}
		if _, err := db.GetKnowledge(ctx(), KnowledgeGetRequest{KnowledgeID: "k-" + id}); err == nil {
			t.Errorf("the refused promotion of %s still wrote a document", id)
		}
	}
	if _, err := brain.PromoteToKnowledge(ctx(), KnowledgeMemoryPromoteToKnowledgeRequest{MemoryIDs: []string{"m-ok"}, KnowledgeID: "k-ok"}); err != nil {
		t.Errorf("promotion of a memory with no status: %v", err)
	}

	off := openReviewBrain(t, "review-promote-off", WithEmbedder(NewMockEmbedder(8)))
	saveWalkMemories(t, off)
	if _, err := off.KnowledgeMemory().PromoteToKnowledge(ctx(), KnowledgeMemoryPromoteToKnowledgeRequest{MemoryIDs: []string{"m-prop"}, KnowledgeID: "k-prop"}); err != nil {
		t.Errorf("promotion without the gate: %v", err)
	}
}

// accepted means the same on the aggregates as in a plan: a document with
// no status counts as accepted.
func TestAnAcceptedFilterOnTheAggregatesCountsTheDocumentsWithNoStatus(t *testing.T) {
	for _, gate := range []bool{false, true} {
		opts := []Option{WithEmbedder(NewMockEmbedder(8))}
		if gate {
			opts = append(opts, WithReviewGate())
		}
		db := openReviewBrain(t, "review-aggregate-accepted", opts...)
		saveReviewed(t, db)
		// A record whose status is an empty string, as main could store
		// from a caller's metadata, has no status.
		if _, err := db.SaveKnowledge(ctx(), KnowledgeSaveRequest{KnowledgeID: "k-empty", Content: "The heron protocol governs the nightly ledger export."}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec(`UPDATE documents SET metadata = json_set(COALESCE(metadata, '{}'), '$._review_status', '') WHERE id = 'k-empty'`); err != nil {
			t.Fatal(err)
		}
		filter := map[string]string{reviewStatusKey: ReviewAccepted}
		agg, err := db.GraphRAGTools().AggregateMetadata(ctx(), ToolAggregateMetadataRequest{Type: "count", Filters: map[string]any{reviewStatusKey: ReviewAccepted}})
		if err != nil || agg.Results[0].Count != 3 {
			t.Errorf("gate %v: aggregate_metadata accepted counted %+v (%v), want k-plain, k-empty and k-acc", gate, agg, err)
		}
		rep, err := db.GraphRAGTools().RepresentativeRecords(ctx(), ToolRepresentativeRecordsRequest{Filter: filter})
		if err != nil || len(rep.Groups) != 1 || rep.Groups[0].Count != 3 {
			t.Errorf("gate %v: representative_records accepted chose %+v (%v), want k-plain, k-empty and k-acc", gate, rep, err)
		}
		if got := strings.Split(searchIDs(t, db, "heron protocol ledger", &RetrievalFilters{ReviewStatus: ReviewAccepted}), ","); len(got) != 3 || slices.Contains(got, "k-prop") || slices.Contains(got, "k-rej") {
			t.Errorf("gate %v: the plan filter accepted served %v, want k-plain, k-empty and k-acc", gate, got)
		}
	}
}

// A filter that asks for a status is closed on a brain whose documents
// cannot be read, with the gate or without it: it never serves the rows
// it was asked to leave out.
func TestAStatusFilterIsClosedOnAnOutageWithoutTheGate(t *testing.T) {
	db := openReviewBrain(t, "review-filter-outage", WithEmbedder(NewMockEmbedder(8)))
	saveReviewed(t, db)
	if _, err := db.SQL().Exec(`ALTER TABLE documents RENAME COLUMN metadata TO metadata_hidden`); err != nil {
		t.Fatal(err)
	}
	text := func(filters *RetrievalFilters) string {
		t.Helper()
		req := ToolSearchTextRequest{Query: "heron protocol ledger", TopK: 10}
		if filters != nil {
			req.Plan = &RetrievalPlan{Filters: filters}
		}
		args, _ := json.Marshal(req)
		raw, err := db.GraphRAGTools().Call(ctx(), "search_text", args)
		if err != nil {
			t.Fatalf("search_text: %v", err)
		}
		var ids []string
		for _, c := range raw.(*ToolSearchTextResponse).Chunks {
			ids = append(ids, c.DocumentID)
		}
		return strings.Join(ids, ",")
	}
	if got := text(&RetrievalFilters{ReviewStatus: ReviewProposed}); got != "" {
		t.Errorf("a proposed filter over unreadable documents served %s, want nothing", got)
	}
	if got := text(nil); !strings.Contains(got, "k-prop") || !strings.Contains(got, "k-plain") {
		t.Errorf("without a filter or the gate the search is advisory, as on main, but served %s", got)
	}
}

// Every lane of knowledge_search honours plan.filters.review_status.
func TestEveryKnowledgeSearchLaneHonoursThePlanStatus(t *testing.T) {
	db := openReviewBrain(t, "review-lanes", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	saveReviewed(t, db)
	for _, mode := range []string{RetrievalModeLexical, RetrievalModeGraph, RetrievalModePPR, RetrievalModeHybrid} {
		resp, err := db.SearchKnowledge(ctx(), KnowledgeSearchRequest{Query: "heron protocol ledger", TopK: 10, RetrievalMode: mode,
			Plan: &RetrievalPlan{Filters: &RetrievalFilters{ReviewStatus: ReviewProposed}}})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if got := strings.Join(hitIDs(resp), ","); got != "k-prop" {
			t.Errorf("%s with a proposed filter served %q, want k-prop", mode, got)
		}
		for _, tool := range []string{"knowledge_memory_recall", "knowledge_memory_build_context_pack", "search_graphrag_lexical", "search_text"} {
			args := `{"query":"heron protocol ledger","top_k":10,"plan":{"retrieval_mode":"` + mode + `","filters":{"review_status":"proposed"}}}`
			out, err := db.GraphRAGTools().Call(ctx(), tool, json.RawMessage(args))
			if err != nil {
				t.Fatalf("%s %s: %v", tool, mode, err)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "k-acc") || strings.Contains(string(body), "k-plain") || !strings.Contains(string(body), "k-prop") {
				t.Errorf("%s %s with a proposed filter: %.400s, want k-prop alone", tool, mode, body)
			}
		}
	}
}

// A reviewer made of blanks is no reviewer.
func TestABlankReviewerIsNoReviewer(t *testing.T) {
	db := openReviewBrain(t, "review-blank-reviewer", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	_, err := db.SaveKnowledge(ctx(), KnowledgeSaveRequest{KnowledgeID: "k", Content: "text", ReviewStatus: ReviewAccepted, ReviewedBy: "   "})
	if err == nil || !strings.Contains(err.Error(), "needs reviewed_by") {
		t.Errorf("a decision by %q: %v, want it refused", "   ", err)
	}
}

// A proposal takes no reviewer, and a reviewer made of blanks is none.
func TestAProposalWithABlankReviewerIsAProposal(t *testing.T) {
	db := openReviewBrain(t, "review-blank-proposal", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	if _, err := db.SaveKnowledge(ctx(), KnowledgeSaveRequest{KnowledgeID: "k", Content: "text", ReviewStatus: ReviewProposed, ReviewedBy: "   "}); err != nil {
		t.Errorf("a proposal with a blank reviewer: %v, want it stored", err)
	}
}

// plan.filters.review_status holds on the lanes that reach other documents
// through the entities a chunk mentions, not only on the seeds.
func TestThePlanStatusHoldsThroughTheEntityLanes(t *testing.T) {
	db := openReviewBrain(t, "review-entity-lanes", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	save := func(id, content, status string) {
		c := []string{graphChunkNodeID(id, 0)}
		ents := []ToolEntityInput{{Name: "Heron Protocol", ChunkIDs: c}, {Name: "Nightly Ledger", ChunkIDs: c}}
		if _, err := db.SaveKnowledge(ctx(), KnowledgeSaveRequest{KnowledgeID: id, Content: content, ReviewStatus: status, Entities: ents}); err != nil {
			t.Fatal(err)
		}
	}
	save("k-prop", "The Heron Protocol governs the Nightly Ledger export at night.", ReviewProposed)
	save("k-plain", "Quarterly audits mention the Heron Protocol and the Nightly Ledger in passing.", "")
	for _, mode := range []string{RetrievalModePPR, RetrievalModeGraph} {
		for want, id := range map[string]string{ReviewProposed: "k-prop", ReviewAccepted: "k-plain"} {
			resp, err := db.SearchKnowledge(ctx(), KnowledgeSearchRequest{Query: "Heron Protocol Nightly Ledger export", TopK: 10, RetrievalMode: mode,
				Plan: &RetrievalPlan{EntityNames: []string{"Heron Protocol"}, Filters: &RetrievalFilters{ReviewStatus: want}}})
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(hitIDs(resp), ","); got != id {
				t.Errorf("%s with a %s filter served %q, want %s", mode, want, got, id)
			}
		}
	}
}

// Under the gate, a conversation summary is not made from the memories the
// gate hides: persisted, it would serve their text as knowledge. Without the
// gate every memory is in it, as on main.
func TestConversationExtractionLeavesOutAMemoryTheGateHides(t *testing.T) {
	for _, gate := range []bool{true, false} {
		opts := []Option{WithEmbedder(NewMockEmbedder(8))}
		if gate {
			opts = append(opts, WithReviewGate())
		}
		db := openReviewBrain(t, "review-extract", opts...)
		saveWalkMemories(t, db)
		got, err := db.GetMemory(ctx(), MemoryGetRequest{MemoryID: "m-ok"})
		if err != nil {
			t.Fatal(err)
		}
		out, err := db.GraphRAGTools().Call(ctx(), "extract_conversation", json.RawMessage(`{"session_id":"`+got.Memory.SessionID+`","persist":true}`))
		if err != nil {
			t.Fatalf("gate %v: %v", gate, err)
		}
		body, _ := json.Marshal(out)
		if !strings.Contains(string(body), "open ledger") {
			t.Errorf("gate %v: the summary lost the memory with no status: %.400s", gate, body)
		}
		if leaked := strings.Contains(string(body), "secret plan") || strings.Contains(string(body), "burned map"); leaked == gate {
			t.Errorf("gate %v: hidden memories in the summary = %v: %.400s", gate, leaked, body)
		}
	}
}
