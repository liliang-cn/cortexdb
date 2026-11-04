package cortexdb

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func futureInstant() string { return time.Now().UTC().Add(time.Hour).Format(time.RFC3339) }

func openEmbeddedBrain(t *testing.T, name string) (*DB, *GraphRAGToolbox, context.Context) {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), name+".db")), WithEmbedder(NewMockEmbedder(8)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, db.GraphRAGTools(), context.Background()
}

func served(t *testing.T, db *DB, ctx context.Context, query string) []string {
	t.Helper()
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: query, TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	return hitIDs(resp)
}

func documentMetadata(t *testing.T, db *DB, ctx context.Context, id string) map[string]any {
	t.Helper()
	doc, err := db.store.GetDocument(ctx, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return doc.Metadata
}

// A caller's metadata key named expires_at is the caller's: stored as it
// came, never validated, never read as an expiry. The three values are what
// importflow copies from a DATETIME column, a past instant, and junk.
func TestAClientKeyNamedExpiresAtIsStoredAsIsAndNeverRead(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "client-key")
	values := []string{"2030-01-01 00:00:00", pastInstant(), "junk"}
	for i, value := range values {
		id := fmt.Sprintf("row-%d", i)
		if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: id, Content: "The zebra ledger reconciles the quarterly accounts.", Metadata: map[string]string{"expires_at": value, "_table": "events"}}); err != nil {
			t.Fatalf("ingest with expires_at %q: %v", value, err)
		}
		md := documentMetadata(t, db, ctx, id)
		if md["expires_at"] != value || md["_table"] != "events" {
			t.Errorf("the caller's keys were changed: %v", md)
		}
		if _, has := md[expiresAtKey]; has {
			t.Errorf("the engine's key was set from the caller's: %v", md)
		}
	}
	if got := served(t, db, ctx, "zebra ledger"); len(got) != len(values) {
		t.Fatalf("served %v, want all %d rows", got, len(values))
	}
	report, err := db.SweepExpired(ctx, time.Now(), true)
	if err != nil || len(report.Documents) != 0 {
		t.Fatalf("the sweep would remove %v (%v)", report, err)
	}
}

// The engine's key in a caller's metadata is not the caller's to set: it is
// dropped on every write tool, and the expiry comes from the explicit field
// alone.
func TestTheEngineKeyInClientMetadataIsDropped(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "engine-key")
	forged := map[string]string{expiresAtKey: pastInstant(), "mine": "kept"}
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "i", Content: "The zebra ledger, ingested.", Metadata: forged}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "s", Content: "The zebra ledger, saved.", Metadata: forged}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "u", Content: "The zebra ledger, updated."}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "u", Metadata: forged}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"i", "s", "u"} {
		md := documentMetadata(t, db, ctx, id)
		if _, has := md[expiresAtKey]; has || md["mine"] != "kept" {
			t.Errorf("%s: %v", id, md)
		}
	}
	if got := served(t, db, ctx, "zebra ledger"); len(got) != 3 {
		t.Fatalf("served %v, want i, s and u", got)
	}
}

// A row that main wrote with an expires_at of its own is still updatable:
// nothing validates a key the caller owns.
func TestARowWrittenByMainWithThatKeyIsStillUpdatable(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "main-row")
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k", Title: "Old", Content: "The zebra ledger reconciles the quarterly accounts."}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`UPDATE documents SET metadata = json_set(metadata, '$.expires_at', 'junk') WHERE id = 'k'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "k", Title: strPtr("New")}); err != nil {
		t.Fatalf("title-only update of main's row: %v", err)
	}
	if md := documentMetadata(t, db, ctx, "k"); md["expires_at"] != "junk" {
		t.Fatalf("the caller's key was changed: %v", md)
	}
	if got := served(t, db, ctx, "zebra ledger"); len(got) != 1 {
		t.Fatalf("served %v, want k", got)
	}
}

// The explicit field sets the expiry on every write tool and removes it on
// update; an update that says nothing keeps it, whatever it does with the
// caller's metadata; a bad value is refused on every tool.
func TestTheExpiryIsSetAndRemovedByTheExplicitFieldOnly(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "explicit")
	content := "The zebra ledger reconciles the quarterly accounts."
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d", Content: content, ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	if got := served(t, db, ctx, "zebra ledger"); len(got) != 0 {
		t.Fatalf("an expired ingest is served: %v", got)
	}
	// A re-ingest that says nothing about the expiry keeps it: a sync job
	// re-ingesting from the source does not lift a retention rule. An
	// empty value removes it.
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d", Content: content, Title: "again"}); err != nil {
		t.Fatal(err)
	}
	if got := served(t, db, ctx, "zebra ledger"); len(got) != 0 {
		t.Fatalf("a re-ingest without the field lifted the expiry: %v", got)
	}
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d", Content: content, ExpiresAt: strPtr("")}); err != nil {
		t.Fatal(err)
	}
	if got := served(t, db, ctx, "zebra ledger"); len(got) != 1 {
		t.Fatalf("an empty expires_at did not lift the expiry: %v", got)
	}

	// One instant, read once: pastInstant is relative to now, and a second
	// can pass between the save and the check.
	past := pastInstant()
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k", Content: content, ExpiresAt: strPtr(past)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "k", Title: strPtr("Renamed"), Metadata: map[string]string{"x": "y"}}); err != nil {
		t.Fatal(err)
	}
	md := documentMetadata(t, db, ctx, "k")
	if md["x"] != "y" || md[expiresAtKey] != past {
		t.Fatalf("an update that said nothing about the expiry changed it: %v", md)
	}
	if got := served(t, db, ctx, "zebra ledger"); len(got) != 1 || got[0] != "d" {
		t.Fatalf("served %v, want d alone", got)
	}
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "k", ExpiresAt: strPtr("")}); err != nil {
		t.Fatal(err)
	}
	if md := documentMetadata(t, db, ctx, "k"); md[expiresAtKey] != nil {
		t.Fatalf("an empty expires_at did not remove the expiry: %v", md)
	}
	if got := served(t, db, ctx, "zebra ledger"); len(got) != 2 {
		t.Fatalf("served %v, want d and k", got)
	}
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "k", ExpiresAt: strPtr("2031-10-18T02:00:00+02:00")}); err != nil {
		t.Fatal(err)
	}
	if md := documentMetadata(t, db, ctx, "k"); md[expiresAtKey] != "2031-10-18T00:00:00Z" {
		t.Fatalf("the expiry is not stored as a UTC instant: %v", md)
	}
	for what, err := range map[string]error{
		"ingest": func() error {
			_, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "bad", Content: content, ExpiresAt: strPtr("2031-13-01")})
			return err
		}(),
		"save": func() error {
			_, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "bad", Content: content, ExpiresAt: strPtr("2031-13-01")})
			return err
		}(),
		"update": func() error {
			_, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "k", ExpiresAt: strPtr("2031-13-01")})
			return err
		}(),
	} {
		if err == nil || !strings.Contains(err.Error(), "expires_at must be an RFC 3339 instant") {
			t.Errorf("%s with a bad expires_at: %v", what, err)
		}
	}
}

// search_vector_range serves no chunk of an expired document.
func TestSearchVectorRangeLeavesExpiredDocumentsOut(t *testing.T) {
	db, tools, ctx := openEmbeddedBrain(t, "range")
	content := "The zebra ledger reconciles the quarterly accounts."
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "old", Content: content, ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "live", Content: content}); err != nil {
		t.Fatal(err)
	}
	resp, err := tools.SearchVectorRange(ctx, ToolSearchVectorRangeRequest{Query: content, Radius: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 1 || resp.Matches[0].DocumentID != "live" || resp.Count != 1 {
		t.Fatalf("range search served %+v, want the live chunk alone", resp)
	}
}

// knowledge_memory_neighbors returns no chunk or document node of an
// expired document.
func TestNeighborsLeaveExpiredNodesOut(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "neighbors")
	zebra := func(id string) []ToolEntityInput {
		return []ToolEntityInput{{Name: "Zebra", Type: "animal", ChunkIDs: []string{graphChunkNodeID(id, 0)}}}
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "old", Content: "The zebra ledger reconciles the accounts.", Entities: zebra("old"), ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "live", Content: "The zebra ledger is audited.", Entities: zebra("live")}); err != nil {
		t.Fatal(err)
	}
	resp, err := db.KnowledgeMemory().Neighbors(ctx, KnowledgeMemoryNeighborsRequest{EntityName: "Zebra"})
	if err != nil {
		t.Fatal(err)
	}
	var docs []string
	for _, node := range resp.Neighbors {
		if doc, _ := node.Properties["document_id"].(string); doc != "" {
			docs = append(docs, doc)
		}
	}
	if strings.Contains(strings.Join(docs, " "), "old") || !strings.Contains(strings.Join(docs, " "), "live") {
		t.Fatalf("neighbors reach documents %v, want live only", docs)
	}
}

// aggregate_metadata counts visible rows only: not the chunks of an
// expired document, and still the rows that belong to no document.
func TestAggregateMetadataCountsVisibleRowsOnly(t *testing.T) {
	db, tools, ctx := openEmbeddedBrain(t, "aggregate")
	content := "The zebra ledger reconciles the quarterly accounts."
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "old", Content: content, Collection: "ops", ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "live", Content: content, Collection: "ops"}); err != nil {
		t.Fatal(err)
	}
	free, _ := db.embedder.Embed(ctx, "free")
	if err := db.store.Upsert(ctx, &core.Embedding{ID: "free", Vector: free, Content: "free", Metadata: map[string]string{"kind": "loose"}}); err != nil {
		t.Fatal(err)
	}
	count := func(collection string) int {
		resp, err := tools.AggregateMetadata(ctx, ToolAggregateMetadataRequest{Type: "count", Collection: collection})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Results[0].Count
	}
	if got := count("ops"); got != 1 {
		t.Errorf("count in ops = %d, want 1: the live chunk", got)
	}
	if got := count(""); got != 2 {
		t.Errorf("count over everything = %d, want 2: the live chunk and the row without a document", got)
	}
}

// representative_records never picks a hidden chunk as the representative,
// nor counts it. The three chunks embed alike, so the medoid is the first
// by id: the hidden one, unless it is left out.
func TestRepresentativeRecordsNeverPickAHiddenChunk(t *testing.T) {
	db, tools, ctx := openEmbeddedBrain(t, "representative")
	content := "The zebra ledger reconciles the quarterly accounts."
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "a-hidden", Content: content, Collection: "ops", ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b-live", "c-live"} {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content, Collection: "ops"}); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := tools.RepresentativeRecords(ctx, ToolRepresentativeRecordsRequest{Collection: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Groups) != 1 || resp.Groups[0].Count != 2 || strings.Contains(resp.Groups[0].MemberID, "a-hidden") {
		t.Fatalf("representative %+v, want 2 members and a live representative", resp.Groups)
	}
}

// The sweep lists what has expired, then deletes each document only if it
// is still expired when the delete runs: a document renewed in between is
// kept, and the report names what was removed.
func TestTheSweepDeletesOnlyWhatIsStillExpiredWhenItDeletes(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "sweep-recheck")
	now := time.Now()
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d", Content: "The zebra ledger.", ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m-renewed", "m-gone"} {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, UserID: "u", Scope: "user", Content: id, TTLSeconds: 60}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SQL().Exec(`UPDATE messages SET metadata = json_set(metadata, '$.expires_at', '2001-01-01T00:00:00Z') WHERE id IN ('m-renewed', 'm-gone')`); err != nil {
		t.Fatal(err)
	}
	listed, err := db.SweepExpired(ctx, now, true)
	if err != nil {
		t.Fatal(err)
	}
	// The listing runs oldest first (created_at, then id), and the two
	// memories land in different seconds on a slow run: compare them as a set.
	memories := slices.Sorted(slices.Values(listed.Memories))
	if strings.Join(listed.Documents, ",") != "d" || strings.Join(memories, ",") != "m-gone,m-renewed" {
		t.Fatalf("listing: %+v %v", listed, err)
	}
	// Renewed between the listing and the delete: both are kept.
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "d", ExpiresAt: strPtr(futureInstant())}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateMemory(ctx, MemoryUpdateRequest{MemoryID: "m-renewed", TTLSeconds: func() *int { n := 3600; return &n }()}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := db.sweepDocument(ctx, "d", now); err != nil || deleted {
		t.Fatalf("a renewed document was deleted: %v %v", deleted, err)
	}
	if deleted, err := db.sweepMemory(ctx, "m-renewed", now); err != nil || deleted {
		t.Fatalf("a renewed memory was deleted: %v %v", deleted, err)
	}
	if deleted, err := db.sweepMemory(ctx, "m-gone", now); err != nil || !deleted {
		t.Fatalf("an expired memory was kept: %v %v", deleted, err)
	}
	if _, err := db.store.GetDocument(ctx, "d"); err != nil {
		t.Fatalf("the document is gone: %v", err)
	}
	if _, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "m-renewed"}); err != nil {
		t.Fatalf("the renewed memory is gone: %v", err)
	}
	// A memory deleted between the listing and the delete is not an error.
	if deleted, err := db.sweepMemory(ctx, "m-gone", now); err != nil || deleted {
		t.Fatalf("a memory already gone: %v %v", deleted, err)
	}
	// A memory the store refuses to delete, or cannot read, is reported.
	if _, err := db.SQL().Exec(`UPDATE messages SET metadata = json_set(metadata, '$.expires_at', '2001-01-01T00:00:00Z') WHERE id = 'm-renewed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`CREATE TRIGGER keep_memories BEFORE DELETE ON messages BEGIN SELECT RAISE(ABORT, 'not tonight'); END`); err != nil {
		t.Fatal(err)
	}
	if report, err := db.SweepExpired(ctx, now, false); err == nil || !strings.Contains(err.Error(), "sweep memory m-renewed") || len(report.Memories) != 0 {
		t.Fatalf("a refused memory delete: %+v %v", report, err)
	}
	if _, err := db.SQL().Exec(`DROP TRIGGER keep_memories`); err != nil {
		t.Fatal(err)
	}
	restore := renameColumn(t, db, "messages", "role", "role_hidden")
	if _, err := db.sweepMemory(ctx, "m-renewed", now); err == nil {
		t.Fatal("an unreadable memory was swept without an error")
	}
	restore()

	// A delete the store refuses is reported, and nothing is removed.
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "d", ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`CREATE TRIGGER keep_docs BEFORE DELETE ON documents BEGIN SELECT RAISE(ABORT, 'not tonight'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SweepExpired(ctx, time.Now(), false); err == nil || !strings.Contains(err.Error(), "delete knowledge document") {
		t.Fatalf("a refused delete: %v", err)
	}
	if _, err := db.SQL().Exec(`DROP TRIGGER keep_docs`); err != nil {
		t.Fatal(err)
	}
	report, err := db.SweepExpired(ctx, time.Now(), false)
	if err != nil || len(report.Documents) != 1 || report.Documents[0] != "d" {
		t.Fatalf("sweep: %+v %v", report, err)
	}
	if _, err := db.store.GetDocument(ctx, "d"); err == nil {
		t.Fatal("the expired document is still there")
	}
}

// A record main wrote without any metadata takes an expiry through the
// explicit field like any other.
func TestAnExpiryCanBeSetOnARecordWithoutMetadata(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "bare")
	if err := db.store.CreateDocument(ctx, &core.Document{ID: "bare", Title: "Bare", Content: "The zebra ledger."}); err != nil {
		t.Fatal(err)
	}
	past := pastInstant()
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "bare", ExpiresAt: strPtr(past)}); err != nil {
		t.Fatal(err)
	}
	if md := documentMetadata(t, db, ctx, "bare"); md[expiresAtKey] != past {
		t.Fatalf("expiry not set on a bare record: %v", md)
	}
}

// Two brains open on the same file are two processes as far as a cache is
// concerned: an expiry written by one hides the document from the other at
// once.
func TestASecondProcessSeesAnExpiryAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	a, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	ctx := context.Background()
	if _, err := a.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k", Content: "The zebra ledger reconciles the quarterly accounts."}); err != nil {
		t.Fatal(err)
	}
	if got := served(t, b, ctx, "zebra ledger"); len(got) != 1 {
		t.Fatalf("the second brain does not see the document: %v", got)
	}
	if _, err := a.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "k", ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	if got := served(t, b, ctx, "zebra ledger"); len(got) != 0 {
		t.Fatalf("the second brain still serves the expired document: %v", got)
	}
}

// The dedupe lookup goes through the search gate: an expired document does
// not hold its content, so the same text is stored again under a new id,
// and the copy is then the one that holds it.
func TestDedupeLooksUpThroughTheSearchGate(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "dedupe-gate")
	content := "The zebra ledger reconciles the quarterly accounts."
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "old", Content: content, ExpiresAt: strPtr(pastInstant())}); err != nil {
		t.Fatal(err)
	}
	resp, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "new", Content: content, Dedupe: true})
	if err != nil || resp.DuplicateOf != "" {
		t.Fatalf("an expired holder counted as a copy: %+v %v", resp, err)
	}
	resp, err = tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "third", Content: content, Dedupe: true})
	if err != nil || resp.DuplicateOf != "new" {
		t.Fatalf("the live copy does not hold the content: %+v %v", resp, err)
	}
	if _, err := db.store.GetDocument(ctx, "third"); err == nil {
		t.Fatal("the duplicate was stored")
	}
}
