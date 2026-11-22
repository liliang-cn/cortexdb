package cortexdb

import (
	"context"
	"strings"
	"testing"
	"time"
)

// With an embedder, the seeds of a hybrid search come from the vector index:
// the gate holds there too, and on search_text.
func TestTheReviewGateHoldsOnTheVectorPath(t *testing.T) {
	db := openReviewBrain(t, "review-vector", WithReviewGate(), WithEmbedder(wordBagEmbedder{dim: 64}))
	saveReviewed(t, db)
	resp, err := db.SearchKnowledge(ctx(), KnowledgeSearchRequest{Query: "heron protocol ledger", TopK: 10, RetrievalMode: RetrievalModeHybrid})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(hitIDs(resp), ","); strings.Contains(got, "k-prop") || strings.Contains(got, "k-rej") || !strings.Contains(got, "k-acc") {
		t.Fatalf("hybrid search under the gate returned %q", got)
	}
	text, err := db.GraphRAGTools().SearchText(ctx(), ToolSearchTextRequest{Query: "heron protocol ledger", TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range text.Chunks {
		if c.DocumentID == "k-prop" || c.DocumentID == "k-rej" {
			t.Fatalf("search_text under the gate returned %s", c.DocumentID)
		}
	}
	// cortex_query runs its own lanes against the store: the gate holds on
	// each of them.
	for _, lane := range []QueryPrefetch{{Kind: QueryPrefetchVector, Query: "heron protocol ledger"}, {Kind: QueryPrefetchLexical, Query: "heron protocol ledger"}, {Kind: QueryPrefetchHybrid, Query: "heron protocol ledger"}} {
		resp, err := db.Query(ctx(), QueryRequest{Limit: 10, Prefetch: []QueryPrefetch{lane}})
		if err != nil {
			t.Fatalf("%s lane: %v", lane.Kind, err)
		}
		var docs []string
		for _, r := range resp.Results {
			docs = append(docs, r.DocID)
		}
		if got := strings.Join(docs, ","); strings.Contains(got, "k-prop") || strings.Contains(got, "k-rej") || !strings.Contains(got, "k-acc") {
			t.Fatalf("cortex_query %s lane under the gate returned %q", lane.Kind, got)
		}
	}
}

// An acceptance or a rejection names the calling key when there is one. A
// reviewed_by that names someone else is refused, not replaced, and a
// proposal takes neither reviewed_by nor reviewed_at, on every write tool.
func TestTheReviewerIsTheKeyThatDecided(t *testing.T) {
	db, tools, plain := openIngestBrain(t, "review-by-key")
	hermes := WithWriter(plain, "hermes")
	if _, err := db.SaveKnowledge(plain, KnowledgeSaveRequest{KnowledgeID: "k2", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(plain, MemorySaveRequest{MemoryID: "m2", UserID: "u", Scope: "user", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	type write func(ctx context.Context, f reviewFields) error
	writes := map[string]write{
		"ingest_document": func(c context.Context, f reviewFields) error {
			_, err := tools.IngestDocument(c, ToolIngestDocumentRequest{DocumentID: "d", Content: "x", ReviewStatus: f.Status, ReviewedBy: f.By, ReviewedAt: f.At})
			return err
		},
		"knowledge_save": func(c context.Context, f reviewFields) error {
			_, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "k", Content: "x", ReviewStatus: f.Status, ReviewedBy: f.By, ReviewedAt: f.At})
			return err
		},
		"knowledge_update": func(c context.Context, f reviewFields) error {
			_, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "k2", ReviewStatus: f.Status, ReviewedBy: f.By, ReviewedAt: f.At})
			return err
		},
		"memory_save": func(c context.Context, f reviewFields) error {
			_, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "m", UserID: "u", Scope: "user", Content: "x", ReviewStatus: f.Status, ReviewedBy: f.By, ReviewedAt: f.At})
			return err
		},
		"memory_update": func(c context.Context, f reviewFields) error {
			_, err := db.UpdateMemory(c, MemoryUpdateRequest{MemoryID: "m2", ReviewStatus: f.Status, ReviewedBy: f.By, ReviewedAt: f.At})
			return err
		},
	}
	for name, w := range writes {
		for _, f := range []reviewFields{
			{Status: ReviewAccepted, By: "zeus"},
			{Status: ReviewRejected, By: "zeus"},
			{Status: ReviewProposed, By: "hermes"},
			{Status: ReviewProposed, At: "2026-10-18T10:00:00Z"},
		} {
			if err := w(hermes, f); err == nil || !strings.Contains(err.Error(), "review") {
				t.Errorf("%s accepted %+v from hermes: %v", name, f, err)
			}
		}
		if err := w(hermes, reviewFields{Status: ReviewAccepted, By: "hermes"}); err != nil {
			t.Errorf("%s refused hermes naming hermes: %v", name, err)
		}
	}
	for id, want := range map[string]string{"d": "hermes", "k": "hermes", "k2": "hermes"} {
		doc, err := db.store.GetDocument(plain, id)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Metadata[reviewedByKey] != want {
			t.Errorf("%s reviewed_by %v, want %s", id, doc.Metadata[reviewedByKey], want)
		}
	}
	for _, id := range []string{"m", "m2"} {
		got, err := db.GetMemory(plain, MemoryGetRequest{MemoryID: id})
		if err != nil {
			t.Fatal(err)
		}
		if got.Memory.ReviewedBy != "hermes" {
			t.Errorf("%s reviewed_by %q, want hermes", id, got.Memory.ReviewedBy)
		}
	}
	// Without a calling key, reviewed_by is the reviewer, and a proposal
	// carries no reviewer.
	if _, err := db.SaveKnowledge(plain, KnowledgeSaveRequest{KnowledgeID: "k3", Content: "x", ReviewStatus: ReviewAccepted, ReviewedBy: "zeus"}); err != nil {
		t.Fatal(err)
	}
	if doc, _ := db.store.GetDocument(plain, "k3"); doc.Metadata[reviewedByKey] != "zeus" {
		t.Errorf("k3 reviewed_by %v, want zeus", doc.Metadata[reviewedByKey])
	}
	if _, err := tools.IngestDocument(hermes, ToolIngestDocumentRequest{DocumentID: "p", Content: "x", ReviewStatus: ReviewProposed}); err != nil {
		t.Fatal(err)
	}
	if doc, _ := db.store.GetDocument(plain, "p"); doc.Metadata[reviewedByKey] != nil || doc.Metadata[reviewedAtKey] != nil {
		t.Errorf("a proposal got a reviewer: %v", doc.Metadata)
	}
}

// A write that does not decide keeps the stored decision, whatever it does
// with metadata: there is no way to clear a decision, only to replace it.
func TestAReviewDecisionIsKeptByEveryWriteThatDoesNotDecide(t *testing.T) {
	db, tools, c := openIngestBrain(t, "review-keep")
	if _, err := tools.IngestDocument(c, ToolIngestDocumentRequest{DocumentID: "d", Content: "The heron protocol.", ReviewStatus: ReviewProposed}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "d", ReviewStatus: ReviewAccepted, ReviewedBy: "r", ReviewedAt: "2026-10-18T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, req := range []KnowledgeUpdateRequest{
		{KnowledgeID: "d", Metadata: map[string]string{"owner": "ops"}},
		{KnowledgeID: "d", Metadata: map[string]string{}},
		{KnowledgeID: "d", Title: strPtr("Heron")},
		{KnowledgeID: "d", Metadata: map[string]string{reviewStatusKey: ReviewProposed, reviewedByKey: "nobody"}},
	} {
		if _, err := db.UpdateKnowledge(c, req); err != nil {
			t.Fatal(err)
		}
		doc, err := db.store.GetDocument(c, "d")
		if err != nil {
			t.Fatal(err)
		}
		if doc.Metadata[reviewStatusKey] != ReviewAccepted || doc.Metadata[reviewedByKey] != "r" || doc.Metadata[reviewedAtKey] != "2026-10-18T10:00:00Z" {
			t.Fatalf("an update without review_status changed the decision: %v", doc.Metadata)
		}
	}
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{KnowledgeID: "d", Content: "The heron protocol, revised."}); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.IngestDocument(c, ToolIngestDocumentRequest{DocumentID: "d", Content: "The heron protocol, revised again.", Metadata: map[string]string{reviewStatusKey: ReviewRejected}}); err != nil {
		t.Fatal(err)
	}
	doc, _ := db.store.GetDocument(c, "d")
	if doc.Metadata[reviewStatusKey] != ReviewAccepted || doc.Metadata[reviewedByKey] != "r" {
		t.Fatalf("a re-save or re-ingest without review_status changed the decision: %v", doc.Metadata)
	}

	if _, err := db.SaveMemory(c, MemorySaveRequest{MemoryID: "m", UserID: "u", Scope: "user", Content: "x", ReviewStatus: ReviewProposed}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateMemory(c, MemoryUpdateRequest{MemoryID: "m", Metadata: map[string]any{"owner": "ops", reviewStatusKey: ReviewAccepted}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMemory(c, MemoryGetRequest{MemoryID: "m"})
	if err != nil || got.Memory.ReviewStatus != ReviewProposed || got.Memory.Metadata["owner"] != "ops" {
		t.Fatalf("a memory update without review_status changed the status: %+v %v", got, err)
	}
}

func TestAnAcceptanceKeepsTheOtherSystemKeysAndItsDate(t *testing.T) {
	db, tools, c := openIngestBrain(t, "review-keys")
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	req := ToolIngestDocumentRequest{DocumentID: "d", Content: "The heron protocol.", ExpiresAt: strPtr(future), Metadata: map[string]string{"department": "hr", "shelf": "3"}}
	if _, err := tools.IngestDocument(c, req); err != nil {
		t.Fatal(err)
	}
	const at = "2026-10-18T10:00:00Z"
	if _, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "d", ReviewStatus: ReviewAccepted, ReviewedBy: "r", ReviewedAt: at, Metadata: map[string]string{"shelf": "4"}}); err != nil {
		t.Fatal(err)
	}
	doc, err := db.store.GetDocument(c, "d")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Metadata[expiresAtKey] != future || doc.Metadata[contentHashKey] != contentSHA256(req.Content) || doc.Metadata[chunkSizeKey] != "300" {
		t.Fatalf("the acceptance lost a system key: %v", doc.Metadata)
	}
	if _, old := doc.Metadata["department"]; old || doc.Metadata["shelf"] != "4" {
		t.Fatalf("the update did not replace the client's keys: %v", doc.Metadata)
	}
	if doc.Metadata[reviewedAtKey] != at {
		t.Fatalf("reviewed_at %v, want the given %s", doc.Metadata[reviewedAtKey], at)
	}

	req.Metadata = map[string]string{"shelf": "4"}
	again, err := tools.IngestDocument(c, req)
	if err != nil || !again.Unchanged {
		t.Fatalf("re-ingest of an accepted document: %+v %v", again, err)
	}
	if doc, _ = db.store.GetDocument(c, "d"); doc.Metadata[reviewedAtKey] != at || doc.Metadata[reviewStatusKey] != ReviewAccepted {
		t.Fatalf("the re-ingest rewrote the decision: %v", doc.Metadata)
	}
	if _, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "d", ReviewStatus: ReviewAccepted, ReviewedBy: "r"}); err != nil {
		t.Fatal(err)
	}
	if doc, _ = db.store.GetDocument(c, "d"); doc.Metadata[reviewedAtKey] != at {
		t.Fatalf("the same decision named again was redated: %v", doc.Metadata)
	}
	if _, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "d", ReviewStatus: ReviewRejected, ReviewedBy: "r"}); err != nil {
		t.Fatal(err)
	}
	if doc, _ = db.store.GetDocument(c, "d"); doc.Metadata[reviewedAtKey] == at {
		t.Fatalf("a new decision kept the old date: %v", doc.Metadata)
	}

	if _, err := db.UpdateKnowledge(c, KnowledgeUpdateRequest{KnowledgeID: "d", Metadata: map[string]string{"team": "x"}}); err != nil {
		t.Fatal(err)
	}
	doc, _ = db.store.GetDocument(c, "d")
	if _, old := doc.Metadata["shelf"]; old || doc.Metadata["team"] != "x" {
		t.Fatalf("a plain update did not replace the client's keys: %v", doc.Metadata)
	}
	if doc.Metadata[expiresAtKey] != future || doc.Metadata[reviewStatusKey] != ReviewRejected {
		t.Fatalf("a plain update lost the expiry or the decision: %v", doc.Metadata)
	}
}

// Under the gate, a brain whose documents cannot be read hides every
// document met, and the aggregates report the error: an outage must not
// lift the gate. Without the gate, the same outage hides nothing, because
// the sweep is the guarantee for the expiry.
func TestTheGateFailsClosedWhenTheBrainCannotBeRead(t *testing.T) {
	db := openReviewBrain(t, "review-closed", WithReviewGate())
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	c := ctx()
	if hidden := db.hiddenAmong(c, nil, []string{"any"}); len(hidden) != 1 {
		t.Fatal("the gate let a document through that it could not check")
	}
	if _, err := db.GraphRAGTools().AggregateMetadata(c, ToolAggregateMetadataRequest{Type: "count"}); err == nil {
		t.Fatal("aggregate_metadata counted on a brain it could not read")
	}
	if _, err := db.GraphRAGTools().RepresentativeRecords(c, ToolRepresentativeRecordsRequest{}); err == nil {
		t.Fatal("representative_records chose on a brain it could not read")
	}
}

// The gate's own query, over the whole brain for the aggregates, is
// answered from the two expression indexes. With a wanted status the query
// is bounded by the ids the call met, through the primary key.
func TestTheReviewStatusIsIndexed(t *testing.T) {
	db := openReviewBrain(t, "review-indexed", WithReviewGate())
	rows, err := db.SQL().QueryContext(ctx(), `EXPLAIN QUERY PLAN SELECT id FROM documents WHERE `+db.hiddenPredicate(db.reviewHiddenPredicate("")), instant(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	err = eachRow(rows, func() error {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			return err
		}
		plan = append(plan, detail)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(plan, "; "); !strings.Contains(got, "idx_documents_expires_at") || !strings.Contains(got, "idx_documents_review_status") {
		t.Fatalf("the gate query does not use both indexes: %s", got)
	}
}
