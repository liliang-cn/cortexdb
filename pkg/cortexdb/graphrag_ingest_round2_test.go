package cortexdb

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// graphCounts is the live graph as "type=n" by node type and by edge type,
// the shape a reviewer compares before and after a write.
func graphCounts(t *testing.T, db *DB) (nodes, edges string) {
	t.Helper()
	count := func(query string) string {
		rows, err := db.SQL().Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var parts []string
		for rows.Next() {
			var kind string
			var n int
			if err := rows.Scan(&kind, &n); err != nil {
				t.Fatal(err)
			}
			parts = append(parts, fmt.Sprintf("%s=%d", kind, n))
		}
		sort.Strings(parts)
		return strings.Join(parts, ",")
	}
	return count(`SELECT COALESCE(node_type, ''), COUNT(*) FROM graph_nodes GROUP BY node_type`),
		count(`SELECT COALESCE(edge_type, ''), COUNT(*) FROM graph_edges GROUP BY edge_type`)
}

// hasCount reports whether counts, as graphCounts writes them, holds want
// exactly: "mentions=2" is not found in "mentions=20".
func hasCount(counts, want string) bool {
	return slices.Contains(strings.Split(counts, ","), want)
}

// attachedBrain is a document of two chunks with two entities and one
// relation attached to its first chunk by the caller, the way an extractor
// attaches what it read.
func attachedBrain(t *testing.T, name string) (*DB, *GraphRAGToolbox, context.Context, ToolIngestDocumentRequest) {
	t.Helper()
	db, tools, ctx := openIngestBrain(t, name)
	req := ToolIngestDocumentRequest{DocumentID: "myth-1", Title: "Orpheus", Collection: "myths", ChunkSize: 8,
		Content: "Orpheus sang for Eurydice at the gates of the underworld. Hades listened, and Persephone wept at the song."}
	first, err := tools.IngestDocument(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ChunkNodeIDs) < 2 {
		t.Fatalf("the fixture needs two chunks, got %d", len(first.ChunkNodeIDs))
	}
	chunk := first.ChunkNodeIDs[0]
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: "myth-1", Entities: []ToolEntityInput{
		{Name: "Orpheus", Type: "person", ChunkIDs: []string{chunk}},
		{Name: "Eurydice", Type: "person", ChunkIDs: []string{chunk}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{DocumentID: "myth-1", Relations: []ToolRelationInput{
		{From: "Orpheus", To: "Eurydice", Type: "loves", ChunkIDs: []string{chunk}},
	}}); err != nil {
		t.Fatal(err)
	}
	return db, tools, ctx, req
}

func hitsFor(t *testing.T, tools *GraphRAGToolbox, ctx context.Context, entity string) int {
	t.Helper()
	resp, err := tools.SearchChunksByEntities(ctx, ToolSearchChunksByEntitiesRequest{EntityNames: []string{entity}})
	if err != nil {
		t.Fatal(err)
	}
	return len(resp.Chunks)
}

// A re-ingest of the same text with another title or other metadata updates
// the record, the document node and the chunks in place. The graph the
// caller attached to the chunks (entities, mention edges, relations) is not
// touched: the text did not change, so nothing it said is unsaid.
func TestATitleOnlyReingestKeepsTheGraphTheCallerAttached(t *testing.T) {
	db, tools, ctx, req := attachedBrain(t, "title-only")
	nodes, edges := graphCounts(t, db)
	if hitsFor(t, tools, ctx, "Orpheus") != 1 {
		t.Fatal("the fixture's entity reaches no chunk")
	}

	req.Title = "Orpheus and Eurydice"
	req.Metadata = map[string]string{"classification": "myth"}
	resp, err := tools.IngestDocument(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Unchanged {
		t.Fatal("a new title was reported unchanged")
	}
	if n, e := graphCounts(t, db); n != nodes || e != edges {
		t.Errorf("the graph changed on a title-only re-ingest:\n%s / %s\nwas\n%s / %s", n, e, nodes, edges)
	}
	if hitsFor(t, tools, ctx, "Orpheus") != 1 {
		t.Error("the entity lost its chunk")
	}
	doc, err := db.store.GetDocument(ctx, "myth-1")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Orpheus and Eurydice" || doc.Version != 2 || doc.Metadata["classification"] != "myth" {
		t.Errorf("the record was not updated: %+v", doc)
	}
	chunks, _ := db.store.GetByDocID(ctx, "myth-1")
	for _, c := range chunks {
		if c.Metadata["title"] != "Orpheus and Eurydice" || c.Metadata["classification"] != "myth" {
			t.Errorf("chunk %s was not updated: %v", c.ID, c.Metadata)
		}
	}
	node, err := db.graph.GetNode(ctx, resp.DocumentNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if node.Properties["title"] != "Orpheus and Eurydice" {
		t.Errorf("the document node was not updated: %v", node.Properties)
	}
}

// Other text keeps the chunk ids that survive (chunk i of the new text is
// chunk i of the old one, as on main) with the graph attached to them, and
// retires the chunks past the new text's end with their mention edges. The
// entities and the relations stay: nothing the text said is unsaid.
func TestAContentChangeReplacesTheChunksAndKeepsTheEntities(t *testing.T) {
	db, tools, ctx, req := attachedBrain(t, "content-change")
	before, _ := graphCounts(t, db)
	if !hasCount(before, "chunk=3") {
		t.Fatalf("fixture: %s", before)
	}
	first, err := tools.IngestDocument(ctx, req)
	if err != nil || !first.Unchanged {
		t.Fatalf("fixture re-ingest: %+v %v", first, err)
	}
	last := first.ChunkNodeIDs[len(first.ChunkNodeIDs)-1]
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: "myth-1", Entities: []ToolEntityInput{{Name: "Hades", Type: "person", ChunkIDs: []string{last}}}}); err != nil {
		t.Fatal(err)
	}
	req.Content = "Orpheus looked back."
	resp, err := tools.IngestDocument(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Unchanged || len(resp.ChunkNodeIDs) != 1 || resp.ChunkNodeIDs[0] != first.ChunkNodeIDs[0] {
		t.Fatalf("new text: %+v", resp)
	}
	nodes, edges := graphCounts(t, db)
	for _, want := range []string{"chunk=1", "document=1", "person=3"} {
		if !hasCount(nodes, want) {
			t.Errorf("nodes %s, want %s", nodes, want)
		}
	}
	if !hasCount(edges, "loves=1") || !hasCount(edges, "has_chunk=1") || !hasCount(edges, "mentions=2") {
		t.Errorf("edges %s: want the relation kept, one has_chunk, the two mentions of the chunk that survives", edges)
	}
	chunks, _ := db.store.GetByDocID(ctx, "myth-1")
	if len(chunks) != 1 || !strings.Contains(chunks[0].Content, "looked back") {
		t.Errorf("chunk rows %+v, want the one new chunk", chunks)
	}
	if hitsFor(t, tools, ctx, "Orpheus") != 1 {
		t.Error("the entity attached to the chunk that survives lost it")
	}
	if hitsFor(t, tools, ctx, "Hades") != 0 {
		t.Error("the entity attached to a chunk past the new text's end still reaches it")
	}
}

// "Unchanged" is said only when the record, the chunk rows and the graph
// nodes are all there. After delete_document_graph the nodes are gone, so a
// re-ingest of the same text rebuilds them and says so, as main does.
func TestUnchangedIsSaidOnlyWhenTheChunkNodesAreThere(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "unchanged-nodes")
	req := ToolIngestDocumentRequest{DocumentID: "d1", Title: "One", Content: "The ledger moves to the new cluster on Sunday.", Collection: "ops"}
	first, err := tools.IngestDocument(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools.DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if nodes, _ := graphCounts(t, db); nodes != "" {
		t.Fatalf("fixture: nodes left after delete: %s", nodes)
	}
	again, err := tools.IngestDocument(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if again.Unchanged {
		t.Fatal("a re-ingest with no graph nodes was reported unchanged")
	}
	nodes, _ := graphCounts(t, db)
	if !hasCount(nodes, fmt.Sprintf("chunk=%d", len(first.ChunkNodeIDs))) || !hasCount(nodes, "document=1") {
		t.Errorf("the graph was not rebuilt: %s", nodes)
	}
	third, err := tools.IngestDocument(ctx, req)
	if err != nil || !third.Unchanged {
		t.Fatalf("with everything there, the re-ingest was not unchanged: %+v %v", third, err)
	}
	// The graph node check fails loudly, never passes as "present".
	if _, err := db.SQL().Exec(`ALTER TABLE graph_nodes RENAME COLUMN node_type TO node_type_hidden`); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.IngestDocument(ctx, req); err == nil {
		t.Fatal("an unreadable graph passed the unchanged check")
	}
}

// The content lookup of dedupe runs inside the write transaction, so two
// ingests of the same content that race store one copy: the second sees
// the first's commit and names it.
func TestTwoConcurrentDedupeIngestsStoreOneCopy(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "dedupe-race")
	// One race is lost or won by the scheduler, so a lookup outside the
	// write transaction can pass one round. Many rounds, each started at
	// once by a closed channel, leave it no chance to pass them all.
	const rounds, writers = 25, 4
	for round := 0; round < rounds; round++ {
		content := fmt.Sprintf("The same page saved from %d browsers at once, round %d.", writers, round)
		ids := make([]string, writers)
		results := make([]*ToolIngestDocumentResponse, writers)
		errs := make([]error, writers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range ids {
			ids[i] = fmt.Sprintf("web-%d-%d", round, i)
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i], errs[i] = tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: ids[i], Content: content, Dedupe: true})
			}(i)
		}
		close(start)
		wg.Wait()
		stored := 0
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d, ingest %d: %v", round, i, err)
			}
			_, getErr := db.store.GetDocument(ctx, ids[i])
			if results[i].DuplicateOf == "" {
				stored++
			}
			if (getErr == nil) != (results[i].DuplicateOf == "") {
				t.Errorf("round %d, %s: stored=%v, but the response says duplicate_of=%q", round, ids[i], getErr == nil, results[i].DuplicateOf)
			}
		}
		if stored != 1 {
			t.Fatalf("round %d: %d copies stored, want 1: %+v", round, stored, results)
		}
	}
}

// The lookup reads every document that holds the content, not the first
// sixteen: the copy in this collection is found past seventeen holders in
// another one, whatever the order the rows come in (the ids sort it last).
func TestDedupeFindsTheCopyPastSixteenHolders(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "dedupe-many")
	content := "A page kept once per collection, seventeen times elsewhere."
	for i := 0; i < 17; i++ {
		if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: fmt.Sprintf("a-other-%02d", i), Content: content, Collection: "other"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "z-mine-1", Content: content, Collection: "mine"}); err != nil {
		t.Fatal(err)
	}
	dup, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "z-mine-2", Content: content, Collection: "mine", Dedupe: true})
	if err != nil {
		t.Fatal(err)
	}
	if dup.DuplicateOf != "z-mine-1" {
		t.Fatalf("duplicate_of %q, want z-mine-1", dup.DuplicateOf)
	}
	if _, err := db.store.GetDocument(ctx, "z-mine-2"); err == nil {
		t.Fatal("the duplicate was stored")
	}
	// An unreadable record table fails the lookup, never answers "new".
	if _, err := db.SQL().Exec(`ALTER TABLE documents RENAME COLUMN metadata TO metadata_hidden`); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "z-mine-3", Content: content, Collection: "mine", Dedupe: true}); err == nil {
		t.Fatal("a dedupe lookup on an unreadable record table reported success")
	}
}

// The marks the engine leaves (hash, chunking) live under names of its own,
// prefixed with "_". A caller's key of any other name, chunk_size or
// content_sha256 included, is stored as given; a caller's copy of a mark is
// dropped, the engine's value wins. The same holds on knowledge_save and
// knowledge_update.
func TestTheIngestMarksNeverOverwriteACallersKey(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "marks")
	meta := map[string]string{"chunk_size": "mine", "content_sha256": "mine", "_content_sha256": "forged", "_chunk_size": "forged"}
	resp, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "The ledger moves on Sunday.", Metadata: meta})
	if err != nil {
		t.Fatal(err)
	}
	check := func(what string, stored map[string]any) {
		t.Helper()
		if stored["chunk_size"] != "mine" || stored["content_sha256"] != "mine" {
			t.Errorf("%s: the caller's keys were overwritten: %v", what, stored)
		}
		if stored[contentHashKey] != resp.ContentSHA256 || stored[chunkSizeKey] == "forged" || stored[chunkSizeKey] == nil {
			t.Errorf("%s: the marks are not the engine's: %v", what, stored)
		}
		for key := range stored {
			if strings.HasPrefix(key, "_") && key != contentHashKey && key != chunkSizeKey && key != chunkOverlapKey && key != vectorsKey {
				t.Errorf("%s: unexpected engine key %s", what, key)
			}
		}
	}
	doc, err := db.store.GetDocument(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	check("ingest", doc.Metadata)
	if again, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Content: "The ledger moves on Sunday.", Metadata: meta}); err != nil || !again.Unchanged {
		t.Fatalf("the same ingest with the caller's keys was not unchanged: %+v %v", again, err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "Saved with a caller's keys.", Metadata: meta}); err != nil {
		t.Fatal(err)
	}
	saved, _ := db.store.GetDocument(ctx, "k1")
	resp.ContentSHA256 = contentSHA256("Saved with a caller's keys.")
	check("knowledge_save", saved.Metadata)
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "k1", Metadata: map[string]string{"content_sha256": "still mine", "_content_sha256": "forged again"}}); err != nil {
		t.Fatal(err)
	}
	updated, _ := db.store.GetDocument(ctx, "k1")
	if updated.Metadata["content_sha256"] != "still mine" || updated.Metadata[contentHashKey] != resp.ContentSHA256 {
		t.Errorf("knowledge_update: %v", updated.Metadata)
	}
}

// The failures a re-ingest over other text can meet, each reported and none
// half-applied: no chunk rows to retire (the rows were removed underneath
// the record), the history table refusing the retired nodes, the live table
// refusing their deletion, the chunk rows refusing theirs. And the error a
// duplicate carries is readable.
func TestARetireThatCannotCompleteIsReportedAndWritesNothing(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "retire-fails")
	long := "The ledger moves to the new cluster on Sunday, and the old cluster stays up for a week after that."
	req := ToolIngestDocumentRequest{DocumentID: "d1", Title: "One", Content: long, Collection: "ops", ChunkSize: 8}
	if first, err := tools.IngestDocument(ctx, req); err != nil || len(first.ChunkNodeIDs) < 2 {
		t.Fatalf("fixture: %+v %v", first, err)
	}
	// No rows to retire: the record is there, its chunks are not.
	if _, err := db.SQL().Exec(`DELETE FROM embeddings WHERE doc_id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	req.Content = "The ledger stays."
	if resp, err := tools.IngestDocument(ctx, req); err != nil || len(resp.ChunkNodeIDs) != 1 {
		t.Fatalf("re-ingest with no chunk rows to retire: %+v %v", resp, err)
	}
	for _, c := range []struct{ what, trigger, want string }{
		{"history refuses", `CREATE TRIGGER no_history BEFORE INSERT ON graph_node_history BEGIN SELECT RAISE(ABORT, 'no history tonight'); END`, "record previous chunk nodes"},
		{"delete refused", `CREATE TRIGGER no_delete BEFORE DELETE ON graph_nodes BEGIN SELECT RAISE(ABORT, 'no delete tonight'); END`, "delete previous chunk nodes"},
		{"chunk rows kept", `CREATE TRIGGER no_chunk_delete BEFORE DELETE ON embeddings BEGIN SELECT RAISE(ABORT, 'no chunk delete tonight'); END`, "delete prior knowledge chunks"},
	} {
		// The long text again, so that the short one has chunks to retire.
		req.Content = long
		if _, err := tools.IngestDocument(ctx, req); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec(c.trigger); err != nil {
			t.Fatal(err)
		}
		req.Content = "The ledger stays."
		_, err := tools.IngestDocument(ctx, req)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.what, err, c.want)
		}
		if doc, _ := db.store.GetDocument(ctx, "d1"); doc.Content != long {
			t.Errorf("%s: the record was written although the retire failed: %q", c.what, doc.Content)
		}
		if _, err := db.SQL().Exec(`DROP TRIGGER ` + strings.Fields(c.trigger)[2]); err != nil {
			t.Fatal(err)
		}
	}
	if got := (duplicateContent{holder: "web-1"}).Error(); got != "content already stored as web-1" {
		t.Errorf("duplicate error: %q", got)
	}
}

// knowledge_save and knowledge_update report a transaction that fails, as
// ingest_document does.
func TestAKnowledgeWriteWhoseTransactionFailsIsReported(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "knowledge-tx-fails")
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "First text."}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`CREATE TRIGGER no_chunks BEFORE INSERT ON embeddings BEGIN SELECT RAISE(ABORT, 'no chunks tonight'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "Second text."}); err == nil || !strings.Contains(err.Error(), "no chunks tonight") {
		t.Errorf("knowledge_save: %v", err)
	}
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "k1", Content: strPtr("Third text.")}); err == nil || !strings.Contains(err.Error(), "no chunks tonight") {
		t.Errorf("knowledge_update: %v", err)
	}
	if doc, _ := db.store.GetDocument(ctx, "k1"); doc.Content != "First text." {
		t.Errorf("a failed write landed: %q", doc.Content)
	}
}

// knowledge_save over an existing id is a rewrite, as on main: the chunks
// and the graph the previous text left are retired before the new ones are
// written, so a shorter text leaves no chunk of the longer one behind.
func TestAKnowledgeSaveOverAnExistingIdRetiresWhatTheOldTextLeft(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "save-over-existing")
	long := "The ledger moves to the new cluster on Sunday, and the old cluster stays up for a week after that."
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k", Content: long, Collection: "ops", ChunkSize: 8}); err != nil {
		t.Fatal(err)
	}
	if before, _ := db.store.GetByDocID(ctx, "k"); len(before) < 2 {
		t.Fatalf("the fixture needs two chunks, got %d", len(before))
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k", Content: "The ledger stays.", Collection: "ops", ChunkSize: 8}); err != nil {
		t.Fatal(err)
	}
	chunks, _ := db.store.GetByDocID(ctx, "k")
	nodes, _ := graphCounts(t, db)
	if len(chunks) != 1 || !hasCount(nodes, "chunk=1") {
		t.Fatalf("after a shorter save: %d chunk rows, nodes %s, want one of each", len(chunks), nodes)
	}
}

// The dedupe lookup is one indexed read: the plan of its query uses the
// expression index on the content hash, so a dedupe ingest does not scan
// every document under the write lock.
func TestTheDedupeLookupUsesTheContentHashIndex(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "hash-index")
	rows, err := db.SQL().QueryContext(ctx, `EXPLAIN QUERY PLAN `+db.contentHolderQuery(), "hash", "id", "collection", "2031-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(plan, "; "); !strings.Contains(got, "idx_documents_content_sha256") {
		t.Fatalf("the dedupe lookup does not use the index: %s", got)
	}
}

// The holder found by its hash is checked against the collection through its
// own chunks. Joined on the collection, SQLite read the collection index
// instead, and walked the chunks of the collection until it met one of the
// holder's: on a collection of 250,000 chunks, 0.2 s for a holder ingested
// last and 0.4 s when the same text lived in another collection, at every
// ingest under the write lock.
func TestTheDedupeLookupChecksTheCollectionThroughTheHoldersChunks(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "holder-chunks")
	rows, err := db.SQL().QueryContext(ctx, `EXPLAIN QUERY PLAN `+db.contentHolderQuery(), "hash", "id", "collection", "2031-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(plan, "; "); !strings.Contains(got, "idx_embeddings_doc_id") || strings.Contains(got, "idx_embeddings_collection_id") {
		t.Fatalf("the dedupe lookup does not read the holder's chunks by their index: %s", got)
	}
}

// InsertGraphDocument writes the record without metadata, so it carries no
// marks: a later ingest of the same text over its id is not unchanged (the
// stored chunks were not made from the marks it would compare), and its
// content is no holder for dedupe. An ingest over the id after it stamps the
// marks again.
func TestADocumentWrittenWithoutTheMarksIsNeitherUnchangedNorAHolder(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "nomarks.db")), WithEmbedder(newKeywordEmbedder("ledger", "sunday")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tools, ctx := db.GraphRAGTools(), context.Background()
	const content = "The ledger moves on Sunday."
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Collection: "c", Content: content}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertGraphDocument(ctx, GraphRAGDocument{ID: "d1", Content: content}, GraphRAGIngestOptions{Collection: "c"}); err != nil {
		t.Fatal(err)
	}
	doc, err := db.store.GetDocument(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if _, marked := doc.Metadata[contentHashKey]; marked {
		t.Fatalf("InsertGraphDocument kept the marks of the previous ingest: %v", doc.Metadata)
	}
	dup, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d2", Collection: "c", Content: content, Dedupe: true})
	if err != nil || dup.DuplicateOf != "" {
		t.Fatalf("a document without marks held the content: %+v %v", dup, err)
	}
	again, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Collection: "c", Content: content})
	if err != nil || again.Unchanged {
		t.Fatalf("an ingest over a document without marks was unchanged: %+v %v", again, err)
	}
	if again, err = tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d1", Collection: "c", Content: content}); err != nil || !again.Unchanged {
		t.Fatalf("the ingest after it did not stamp the marks again: %+v %v", again, err)
	}
}

// InsertGraphDocument writes no mark, and a caller's copy of one is not kept
// on the chunks either: the marks are the engine's, so a document read back
// and sent again does not carry stale marks onto chunks they do not describe.
// Its other keys are kept, as on main.
func TestInsertGraphDocumentDropsACallersCopyOfTheMarks(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "dropmarks.db")), WithEmbedder(newKeywordEmbedder("ledger", "sunday")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	// The caller's other keys stay, an engine key that is not a mark and
	// another spelling of a mark included.
	others := map[string]string{"team": "a", "_table": "t", strings.ToUpper(contentHashKey): "x"}
	sent := maps.Clone(others)
	for _, k := range EngineDocumentKeys() {
		sent[k] = "forged"
	}
	if _, err := db.InsertGraphDocument(ctx, GraphRAGDocument{ID: "d1", Content: "The ledger moves on Sunday.", Metadata: sent}, GraphRAGIngestOptions{Collection: "c"}); err != nil {
		t.Fatal(err)
	}
	if sent[contentHashKey] != "forged" {
		t.Fatalf("InsertGraphDocument changed the caller's map: %v", sent)
	}
	record, err := db.store.GetDocument(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range EngineDocumentKeys() {
		if record.Metadata[k] == "forged" {
			t.Errorf("the record took the caller's %s: %v", k, record.Metadata)
		}
	}
	chunk, err := db.store.GetByID(ctx, graphChunkNodeID("d1", 0))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range others {
		if chunk.Metadata[k] != v {
			t.Errorf("the chunk lost the caller's %s: %v", k, chunk.Metadata)
		}
	}
	for _, k := range EngineDocumentKeys() {
		if _, kept := chunk.Metadata[k]; kept {
			t.Errorf("the chunk kept the caller's %s: %v", k, chunk.Metadata)
		}
	}
}
