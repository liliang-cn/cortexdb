package cortexdb

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// A metadata key named review_status is the caller's: stored as it came,
// whatever its value, and never read by the gate. An importflow column
// named review_status=approved neither fails the import nor hides the row.
func TestAMetadataKeyNamedReviewStatusIsTheCallers(t *testing.T) {
	db := openReviewBrain(t, "review-client-key", WithReviewGate())
	tools, c := db.GraphRAGTools(), ctx()
	if _, err := tools.IngestDocument(c, ToolIngestDocumentRequest{DocumentID: "d", Content: "The heron ledger export.", Metadata: map[string]string{"review_status": "approved"}}); err != nil {
		t.Fatalf("a client key named review_status was refused: %v", err)
	}
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "k", Content: "The heron ledger export, saved.", Metadata: map[string]string{"review_status": "bogus"}}); err != nil {
		t.Fatalf("a client key named review_status was refused by knowledge_save: %v", err)
	}
	for _, id := range []string{"d", "k"} {
		doc, err := db.store.GetDocument(c, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, engine := doc.Metadata[reviewStatusKey]; engine || doc.Metadata["review_status"] == nil {
			t.Fatalf("%s: the client's key was read as the engine's: %v", id, doc.Metadata)
		}
	}
	if got := searchIDs(t, db, "heron ledger", nil); !strings.Contains(got, "d") || !strings.Contains(got, "k") {
		t.Fatalf("a client key named review_status hid a document under the gate: %q", got)
	}
	if _, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "m", UserID: "u", Scope: "user", Content: "The osprey job runs at dawn.", Metadata: map[string]any{"review_status": "bogus"}}); err != nil {
		t.Fatalf("a client key named review_status was refused by memory_save: %v", err)
	}
	if got := recallIDs(t, db); !strings.Contains(got, "m") {
		t.Fatalf("a client key named review_status hid a memory under the gate: %q", got)
	}
}

// A row main wrote with a client key named review_status is still
// updatable, and an engine key it never had is not invented.
func TestARowWrittenByMainWithTheReviewKeyIsStillUpdatable(t *testing.T) {
	db, _, c := openIngestBrain(t, "review-main-row")
	if err := db.store.CreateDocument(c, &core.Document{ID: "old", Title: "Old", Content: "The zebra ledger.", Metadata: map[string]any{"review_status": "approved"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "old", Title: strPtr("Older")}); err != nil {
		t.Fatalf("a row main wrote cannot be updated: %v", err)
	}
	md := documentMetadata(t, db, c, "old")
	if md["review_status"] != "approved" || md[reviewStatusKey] != nil {
		t.Fatalf("the update changed the client's key or invented the engine's: %v", md)
	}
}

// A copy of the engine's keys in a caller's metadata is dropped: the
// explicit fields are the only way to decide.
func TestTheEngineReviewKeysInClientMetadataAreDropped(t *testing.T) {
	db := openReviewBrain(t, "review-engine-copy", WithReviewGate())
	tools, c := db.GraphRAGTools(), ctx()
	copy := map[string]string{reviewStatusKey: ReviewRejected, reviewedByKey: "ghost", reviewedAtKey: "2026-10-18T10:00:00Z"}
	if _, err := tools.IngestDocument(c, ToolIngestDocumentRequest{DocumentID: "d", Content: "The heron ledger export.", Metadata: copy}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "k", Content: "The heron ledger export, saved.", Metadata: copy}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"d", "k"} {
		md := documentMetadata(t, db, c, id)
		for _, key := range []string{reviewStatusKey, reviewedByKey, reviewedAtKey} {
			if md[key] != nil {
				t.Fatalf("%s: the caller's copy of %s was stored: %v", id, key, md)
			}
		}
	}
	if got := searchIDs(t, db, "heron ledger", nil); !strings.Contains(got, "d") || !strings.Contains(got, "k") {
		t.Fatalf("a caller's copy of the engine's keys hid a document: %q", got)
	}
	if _, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "m", UserID: "u", Scope: "user", Content: "The osprey job runs at dawn.", Metadata: map[string]any{reviewStatusKey: ReviewProposed}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMemory(c, MemoryGetRequest{MemoryID: "m"})
	if err != nil || got.Memory.ReviewStatus != "" {
		t.Fatalf("a caller's copy of the engine's key was stored on a memory: %+v %v", got, err)
	}
}

// memory_save over an existing id keeps its decision, as an update does:
// a proposed memory saved again without a status is still proposed, and
// still out of recall under the gate.
func TestMemorySaveOverAnExistingIdCarriesTheReviewOver(t *testing.T) {
	db := openReviewBrain(t, "review-resave", WithReviewGate())
	c := ctx()
	if _, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "m-prop", UserID: "u", Scope: "user", Content: "The osprey job runs at dusk.", ReviewStatus: ReviewProposed}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "m-prop", UserID: "u", Scope: "user", Content: "The osprey job runs at dusk, revised."}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMemory(c, MemoryGetRequest{MemoryID: "m-prop"})
	if err != nil || got.Memory.ReviewStatus != ReviewProposed {
		t.Fatalf("a re-save cleared the status: %+v %v", got, err)
	}
	if got := recallIDs(t, db); got != "" {
		t.Fatalf("a re-saved proposed memory is served: %q", got)
	}
	if _, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "m-prop", UserID: "u", Scope: "user", Content: "The osprey job runs at dusk.", ReviewStatus: ReviewAccepted, ReviewedBy: "r"}); err != nil {
		t.Fatal(err)
	}
	if got := recallIDs(t, db); got != "m-prop" {
		t.Fatalf("an accepted memory is not served: %q", got)
	}
}

// The gate holds on the four paths the round-1 gate left open, with the
// same brain: a proposed document is out of search_vector_range,
// knowledge_memory_neighbors, aggregate_metadata and representative_records.
func TestTheReviewGateHoldsOnEveryReadPath(t *testing.T) {
	db := openReviewBrain(t, "review-paths", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	c := ctx()
	content := "The heron protocol governs the ledger."
	for _, id := range []string{"k-ok", "k-prop"} {
		status := ""
		if id == "k-prop" {
			status = ReviewProposed
		}
		if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: id, Content: content, Collection: "ops", ReviewStatus: status,
			Entities: []ToolEntityInput{{Name: "Heron", Type: "bird", ChunkIDs: []string{graphChunkNodeID(id, 0)}}}}); err != nil {
			t.Fatal(err)
		}
	}
	tools := db.GraphRAGTools()
	rng, err := tools.SearchVectorRange(c, ToolSearchVectorRangeRequest{Query: content, Radius: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rng.Matches) != 1 || rng.Matches[0].DocumentID != "k-ok" {
		t.Fatalf("search_vector_range served %+v, want the accepted chunk alone", rng.Matches)
	}
	nb, err := db.KnowledgeMemory().Neighbors(c, KnowledgeMemoryNeighborsRequest{EntityName: "Heron"})
	if err != nil {
		t.Fatal(err)
	}
	var docs []string
	for _, node := range nb.Neighbors {
		if doc, _ := node.Properties["document_id"].(string); doc != "" {
			docs = append(docs, doc)
		}
	}
	if got := strings.Join(docs, " "); strings.Contains(got, "k-prop") || !strings.Contains(got, "k-ok") {
		t.Fatalf("knowledge_memory_neighbors reach %v, want k-ok only", docs)
	}
	agg, err := tools.AggregateMetadata(c, ToolAggregateMetadataRequest{Type: "count", Collection: "ops"})
	if err != nil || agg.Results[0].Count != 1 {
		t.Fatalf("aggregate_metadata counted %+v %v, want the one visible chunk", agg, err)
	}
	rep, err := tools.RepresentativeRecords(c, ToolRepresentativeRecordsRequest{Collection: "ops"})
	if err != nil || len(rep.Groups) != 1 || rep.Groups[0].Count != 1 || strings.Contains(rep.Groups[0].MemberID, "k-prop") {
		t.Fatalf("representative_records chose %+v %v, want the accepted chunk alone", rep, err)
	}
}

// A filter on the engine's status key lifts the gate for that status on
// the aggregates: that is how a curator lists what waits for review.
func TestAStatusFilterOnTheAggregatesListsWhatWaits(t *testing.T) {
	db := openReviewBrain(t, "review-aggregate-filter", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	c := ctx()
	content := "The heron protocol governs the ledger."
	for _, row := range []struct{ id, status, by string }{{"a-ok", "", ""}, {"b-prop", ReviewProposed, ""}, {"c-prop", ReviewProposed, ""}, {"d-rej", ReviewRejected, "r"}} {
		if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: row.id, Content: content, Collection: "ops", ReviewStatus: row.status, ReviewedBy: row.by}); err != nil {
			t.Fatal(err)
		}
	}
	tools := db.GraphRAGTools()
	count := func(status string) int {
		resp, err := tools.AggregateMetadata(c, ToolAggregateMetadataRequest{Type: "count", Collection: "ops", Filters: map[string]any{reviewStatusKey: status}})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Results[0].Count
	}
	if got := count(ReviewProposed); got != 2 {
		t.Fatalf("aggregate_metadata with a proposed filter counted %d, want the two proposed chunks", got)
	}
	if got := count(ReviewRejected); got != 1 {
		t.Fatalf("aggregate_metadata with a rejected filter counted %d, want 1", got)
	}
	rep, err := tools.RepresentativeRecords(c, ToolRepresentativeRecordsRequest{Collection: "ops", Filter: map[string]string{reviewStatusKey: ReviewProposed}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Groups) != 1 || rep.Groups[0].Count != 2 || !strings.Contains(rep.Groups[0].MemberID, "b-prop") {
		t.Fatalf("representative_records with a proposed filter chose %+v, want the two proposed chunks", rep.Groups)
	}
	// The caller's other keys stay in the filter beside the status.
	both := map[string]string{reviewStatusKey: ReviewProposed, "document_id": "c-prop"}
	agg, err := tools.AggregateMetadata(c, ToolAggregateMetadataRequest{Type: "count", Collection: "ops", Filters: map[string]any{reviewStatusKey: ReviewProposed, "document_id": "c-prop"}})
	if err != nil || agg.Results[0].Count != 1 {
		t.Fatalf("aggregate_metadata with the status and a document_id counted %+v (%v), want 1", agg, err)
	}
	rep, err = tools.RepresentativeRecords(c, ToolRepresentativeRecordsRequest{Collection: "ops", Filter: both})
	if err != nil || len(rep.Groups) != 1 || rep.Groups[0].Count != 1 || !strings.Contains(rep.Groups[0].MemberID, "c-prop") {
		t.Fatalf("representative_records with the status and a document_id chose %+v (%v), want c-prop alone", rep, err)
	}
}

// The curator's list reads the decision on the record: a document accepted
// or rejected by knowledge_update leaves it at once, on both aggregates, and
// a row that belongs to no document is never in it, whatever its metadata.
func TestTheCuratorListFollowsTheDecisionOnTheRecord(t *testing.T) {
	db := openReviewBrain(t, "review-curator-list", WithReviewGate(), WithEmbedder(NewMockEmbedder(8)))
	c := ctx()
	tools := db.GraphRAGTools()
	content := "The heron protocol governs the ledger."
	for _, id := range []string{"a", "b", "c"} {
		if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: id, Content: content, Collection: "ops", ReviewStatus: ReviewProposed}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tools.IngestDocument(c, ToolIngestDocumentRequest{DocumentID: "d", Content: content, Collection: "ops", ReviewStatus: ReviewProposed}); err != nil {
		t.Fatal(err)
	}
	if err := db.store.Upsert(c, &core.Embedding{ID: "loose", Vector: make([]float32, 8), Content: content, Collection: "ops",
		Metadata: map[string]string{reviewStatusKey: ReviewProposed}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "a", ReviewStatus: ReviewAccepted, ReviewedBy: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "d", ReviewStatus: ReviewRejected, ReviewedBy: "r"}); err != nil {
		t.Fatal(err)
	}
	waiting, err := tools.AggregateMetadata(c, ToolAggregateMetadataRequest{Type: "group_by", GroupBy: []string{"document_id"}, Collection: "ops", Filters: map[string]any{reviewStatusKey: ReviewProposed}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, g := range waiting.Results {
		ids = append(ids, fmt.Sprint(g.Keys["document_id"]))
	}
	sort.Strings(ids)
	if got := strings.Join(ids, ","); got != "b,c" {
		t.Fatalf("the curator list after a decision holds %q, want b,c", got)
	}
	rejected, err := tools.AggregateMetadata(c, ToolAggregateMetadataRequest{Type: "count", Collection: "ops", Filters: map[string]any{reviewStatusKey: ReviewRejected}})
	if err != nil || rejected.Results[0].Count != 1 {
		t.Fatalf("rejected count %+v %v, want d's one chunk", rejected, err)
	}
	rep, err := tools.RepresentativeRecords(c, ToolRepresentativeRecordsRequest{Collection: "ops", Filter: map[string]string{reviewStatusKey: ReviewAccepted}})
	if err != nil || len(rep.Groups) != 1 || rep.Groups[0].Count != 1 || !strings.Contains(rep.Groups[0].MemberID, ":a:") {
		t.Fatalf("representative_records of accepted rows chose %+v %v, want a alone", rep, err)
	}
}

// The decision lives on the record alone, as the expiry does: not on the
// chunks, where a decision by knowledge_update would leave a stale copy, and
// not on the document node.
func TestTheReviewDecisionIsKeptOnTheRecordOnly(t *testing.T) {
	db, tools, c := openIngestBrain(t, "review-record-only")
	if _, err := tools.IngestDocument(c, ToolIngestDocumentRequest{DocumentID: "i", Content: "The zebra ledger.", ReviewStatus: ReviewAccepted, ReviewedBy: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "k", Content: "The zebra ledger.", ReviewStatus: ReviewAccepted, ReviewedBy: "r", Metadata: map[string]string{"department": "hr"}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"i", "k"} {
		if doc, err := db.store.GetDocument(c, id); err != nil || doc.Metadata[reviewStatusKey] != ReviewAccepted {
			t.Fatalf("%s: the record lost its decision: %v %v", id, doc, err)
		}
		chunks, err := db.store.GetByDocID(c, id)
		if err != nil || len(chunks) == 0 {
			t.Fatalf("%s: chunks %v %v", id, chunks, err)
		}
		for _, ch := range chunks {
			for _, key := range []string{reviewStatusKey, reviewedByKey, reviewedAtKey} {
				if _, ok := ch.Metadata[key]; ok {
					t.Errorf("%s: chunk %s carries a copy of %s", id, ch.ID, key)
				}
			}
		}
		node, err := db.graph.GetNode(c, graphDocumentNodeID(id))
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{reviewStatusKey, reviewedByKey, reviewedAtKey} {
			if _, ok := node.Properties[key]; ok {
				t.Errorf("%s: the document node carries a copy of %s", id, key)
			}
		}
	}
	// knowledge_save's document node keeps the shape it has on main.
	node, err := db.graph.GetNode(c, graphDocumentNodeID("k"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := node.Properties["department"]; ok {
		t.Errorf("knowledge_save's document node took the caller's metadata: %v", node.Properties)
	}
}

// plan.filters.review_status names one of the three statuses. Any other
// value keeps nothing, even a status written behind the engine's back, and
// never reaches the SQL as text.
func TestAReviewStatusFilterKeepsOnlyTheThreeStatuses(t *testing.T) {
	db := openReviewBrain(t, "review-filter-values", WithReviewGate())
	c := ctx()
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "odd", Content: "The heron protocol."}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`UPDATE documents SET metadata = json_set(metadata, '$._review_status', 'it''s') WHERE id = 'odd'`); err != nil {
		t.Fatal(err)
	}
	resp, err := db.SearchKnowledge(c, KnowledgeSearchRequest{Query: "heron protocol", TopK: 5, Plan: &RetrievalPlan{Filters: &RetrievalFilters{ReviewStatus: "it's"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("a filter on a status outside the three kept %d rows", len(resp.Results))
	}
}

// The review_status filter of a plan reads documents only, as its schema
// says: memory_search under the gate serves no proposed memory with it,
// and memory_get reads a proposed memory by id.
func TestTheReviewStatusFilterReadsDocumentsOnly(t *testing.T) {
	db := openReviewBrain(t, "review-filter-memories", WithReviewGate())
	c := ctx()
	if _, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "mp", UserID: "u", Scope: "user", Content: "The heron protocol is proposed.", ReviewStatus: ReviewProposed}); err != nil {
		t.Fatal(err)
	}
	resp, err := db.SearchMemory(c, MemorySearchRequest{Query: "heron protocol", UserID: "u", Scope: "user", TopK: 5, Plan: &RetrievalPlan{Filters: &RetrievalFilters{ReviewStatus: ReviewProposed}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("memory_search with a proposed filter served %d memories", len(resp.Results))
	}
	if got, err := db.GetMemory(c, MemoryGetRequest{MemoryID: "mp"}); err != nil || got.Memory.ReviewStatus != ReviewProposed {
		t.Fatalf("memory_get of a proposed memory: %+v %v", got, err)
	}
	if !strings.Contains(ReviewGateNote, "documents") || !strings.Contains(toolRetrievalPlanSchema("")["properties"].(map[string]any)["filters"].(map[string]any)["properties"].(map[string]any)["review_status"].(map[string]any)["description"].(string), "Documents only") {
		t.Fatal("the note and the schema do not say the filter reads documents only")
	}
}

// The schemas promise nothing about entities and relations: a review key
// in their metadata is the caller's data, neither validated nor read.
func TestTheSchemasPromiseNothingAboutEntitiesAndRelations(t *testing.T) {
	for _, schema := range []map[string]any{toolEntityArraySchema(), toolRelationArraySchema()} {
		raw, err := json.Marshal(schema)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(raw)), "review") {
			t.Fatalf("the entity or relation schema mentions a review: %s", raw)
		}
	}
	for _, def := range ToolDefinitions() {
		if def.Name != "upsert_entities" && def.Name != "upsert_relations" {
			continue
		}
		raw, _ := json.Marshal(def)
		if strings.Contains(strings.ToLower(string(raw)), "review") {
			t.Fatalf("%s mentions a review: %s", def.Name, raw)
		}
	}
}

func TestAReviewKeyOnAnEntityIsTheCallersData(t *testing.T) {
	db := openReviewBrain(t, "review-entity", WithReviewGate())
	c := ctx()
	entityMeta := map[string]string{"review_status": "bogus"}
	entityMeta[reviewStatusKey] = ReviewProposed
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "k", Content: "Arvid Lund grew up in Malmo.",
		Entities:  []ToolEntityInput{{Name: "Arvid Lund", Metadata: entityMeta}},
		Relations: []ToolRelationInput{{From: "Arvid Lund", To: "Malmo", Type: "GREW_UP_IN", Metadata: map[string]string{reviewStatusKey: ReviewRejected}}}}); err != nil {
		t.Fatalf("a review key on an entity was refused: %v", err)
	}
	node, err := db.graph.GetNode(c, graphEntityNodeID("Arvid Lund"))
	if err != nil || node == nil {
		t.Fatalf("entity node: %v", err)
	}
	if node.Properties["review_status"] != "bogus" {
		t.Fatalf("the caller's key on the entity was changed: %v", node.Properties)
	}
	if got := searchIDs(t, db, "Arvid Lund Malmo", nil); got != "k" {
		t.Fatalf("a review key on an entity changed what the graph lane serves: %q", got)
	}
}

// Every tool description that mentions the gate says what it is.
func TestTheToolDescriptionsSayTheGateIsACurationFilter(t *testing.T) {
	var defs []ToolDefinition
	defs = append(defs, ToolDefinitions()...)
	defs = append(defs, KnowledgeMemoryToolDefinitions()...)
	mentions := 0
	for _, def := range defs {
		raw, err := json.Marshal(def)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if !strings.Contains(text, "review gate") {
			continue
		}
		mentions++
		if !strings.Contains(text, "curation filter, not access control") {
			t.Fatalf("%s mentions the review gate without saying what it is", def.Name)
		}
	}
	if mentions < 7 {
		t.Fatalf("only %d tools mention the review gate", mentions)
	}
	for _, name := range []string{"ingest_document", "knowledge_save", "knowledge_update", "memory_save", "memory_update"} {
		found := false
		for _, def := range defs {
			if def.Name != name {
				continue
			}
			found = true
			raw, _ := json.Marshal(def.InputSchema)
			for _, field := range []string{`"review_status"`, `"reviewed_by"`, `"reviewed_at"`} {
				if !strings.Contains(string(raw), field) {
					t.Fatalf("%s has no %s field", name, field)
				}
			}
		}
		if !found {
			t.Fatalf("no tool %s", name)
		}
	}
}

// reviewed_at is stable only for the same decision by the same reviewer;
// an instant given with no status is not a decision.
func TestDecideReviewKeepsTheInstantOfARepeatedDecisionOnly(t *testing.T) {
	c := ctx()
	prev := reviewState{status: ReviewAccepted, by: "r", at: "2026-10-18T10:00:00Z"}
	same, err := decideReview(c, reviewFields{Status: ReviewAccepted, By: "r"}, prev)
	if err != nil || same != prev {
		t.Fatalf("the same decision: %+v %v", same, err)
	}
	other, err := decideReview(c, reviewFields{Status: ReviewAccepted, By: "s"}, prev)
	if err != nil || other.at == prev.at {
		t.Fatalf("another reviewer kept the instant: %+v %v", other, err)
	}
	if _, perr := time.Parse(time.RFC3339, other.at); perr != nil {
		t.Fatalf("reviewed_at %q", other.at)
	}
	none, err := decideReview(c, reviewFields{At: "2026-10-18T10:00:00Z", By: "r"}, reviewState{})
	if err != nil || none != (reviewState{}) {
		t.Fatalf("an instant without a status: %+v %v", none, err)
	}
	if _, err := decideReview(c, reviewFields{Status: ReviewProposed, By: "r", At: "2026-10-18T10:00:00Z"}, prev); err == nil {
		t.Fatal("a proposal with a reviewer and an instant was taken")
	}
}

// The sweep removes what has expired and nothing else: under the gate, a
// proposed document that has not expired is not even a candidate.
func TestTheSweepLeavesProposedDocumentsAlone(t *testing.T) {
	db := openReviewBrain(t, "review-sweep", WithReviewGate())
	c := ctx()
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "k-prop", Content: "The heron protocol.", ReviewStatus: ReviewProposed}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "k-old", Content: "The heron protocol, old.", ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	report, err := db.SweepExpired(c, time.Now(), true)
	if err != nil || strings.Join(report.Documents, ",") != "k-old" {
		t.Fatalf("dry-run sweep listed %+v %v, want k-old alone", report, err)
	}
}

// Dedupe is about content, not visibility: under the gate, a proposed or a
// rejected holder still holds its content, so the same page proposed again
// is refused and the answer names the holder. An expired holder does not
// hold it (TestDedupeLooksUpThroughTheSearchGate): that content is to be
// swept, this one was looked at, or waits to be.
func TestDedupeSeesAProposedOrRejectedHolderUnderTheGate(t *testing.T) {
	db := openReviewBrain(t, "review-dedupe-gate", WithReviewGate())
	tools, c := db.GraphRAGTools(), ctx()
	for _, row := range []struct{ id, status, by, content string }{
		{"p", ReviewProposed, "", "The heron page, proposed once."},
		{"r", ReviewRejected, "curator", "The heron page, rejected once."},
	} {
		if _, err := tools.IngestDocument(c, ToolIngestDocumentRequest{DocumentID: row.id, Content: row.content, Collection: "ops", ReviewStatus: row.status, ReviewedBy: row.by}); err != nil {
			t.Fatal(err)
		}
		resp, err := tools.IngestDocument(c, ToolIngestDocumentRequest{DocumentID: row.id + "-again", Content: row.content, Collection: "ops", ReviewStatus: ReviewProposed, Dedupe: true})
		if err != nil || resp.DuplicateOf != row.id {
			t.Errorf("%s holder: the same content was stored again under the gate: %+v %v", row.status, resp, err)
		}
	}
}

// A knowledge_update can decide on a record that has no metadata at all (a
// row main wrote, before the ingest marks of #75), with no calling key: the
// decision needs a reviewer, and is then stored under the engine's keys.
func TestAKnowledgeUpdateCanDecideOnARecordWithoutMetadata(t *testing.T) {
	db := openReviewBrain(t, "review-update-bare")
	c := ctx()
	if err := db.store.CreateDocument(c, &core.Document{ID: "k", Title: "Bare", Content: "The bare heron record."}); err != nil {
		t.Fatal(err)
	}
	_, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "k", ReviewStatus: ReviewAccepted})
	if err == nil || !strings.Contains(err.Error(), "reviewed_by") {
		t.Fatalf("a decision without a reviewer on a bare record: %v, want it refused", err)
	}
	if _, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "k", ReviewStatus: ReviewAccepted, ReviewedBy: "curator"}); err != nil {
		t.Fatal(err)
	}
	doc, err := db.store.GetDocument(c, "k")
	if err != nil || doc.Metadata[reviewStatusKey] != ReviewAccepted || doc.Metadata[reviewedByKey] != "curator" {
		t.Fatalf("the decision on a bare record was lost: %v %v", doc.Metadata, err)
	}
}

// A memory_save over an id whose row cannot be read reports the failure
// rather than writing over a decision it could not see.
func TestAMemorySaveReportsARowItCannotRead(t *testing.T) {
	db := openReviewBrain(t, "review-memory-unreadable")
	c := ctx()
	if _, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "m", UserID: "u", Content: "The heron note.", ReviewStatus: ReviewProposed}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`ALTER TABLE messages RENAME COLUMN role TO role_hidden`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "m", UserID: "u", Content: "The heron note, again."}); err == nil {
		t.Fatal("a memory_save over a row it could not read went through")
	}
}
