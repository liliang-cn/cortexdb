package cortexdb

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// ingestWithEntities stores a document the way a producer that extracts its
// own entities does: ingest_document, then upsert_entities under the
// document id, each entity mentioned by the first chunk unless it is listed
// in unmentioned.
func ingestWithEntities(t *testing.T, tools *GraphRAGToolbox, docID, content string, names []string, unmentioned ...string) {
	t.Helper()
	ctx := context.Background()
	resp, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: docID, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	entities := make([]ToolEntityInput, 0, len(names))
	for _, name := range names {
		entity := ToolEntityInput{Name: name, Type: "entity", ChunkIDs: []string{resp.ChunkNodeIDs[0]}}
		if slices.Contains(unmentioned, name) {
			entity.ChunkIDs = nil
		}
		entities = append(entities, entity)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: docID, Entities: entities}); err != nil {
		t.Fatal(err)
	}
}

func entitySources(t *testing.T, db *DB, name string) ([]string, bool) {
	t.Helper()
	node, err := db.Graph().GetNode(context.Background(), EntityNodeID(name))
	if err != nil || node == nil {
		return nil, false
	}
	return toStringSlice(node.Properties["source_document_ids"]), true
}

// knowledge_delete takes the document out of the provenance of the entities
// it shares with other documents. It used to leave it there: the entity
// still named a document that was gone, so when its last real source was
// removed with delete_document_graph, that delete saw one source left and
// detached the entity instead of deleting it. Nothing cited it any more and
// nothing removed it.
func TestKnowledgeDeleteLetsGoOfTheEntitiesItSourced(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "sources")
	ingestWithEntities(t, tools, "d2", "zulu yankee xray whiskey", []string{"Shared"})
	ingestWithEntities(t, tools, "d1", "one two three four", []string{"Shared", "Alpha"})

	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := entitySources(t, db, "Alpha"); ok {
		t.Error("Alpha, sourced by d1 alone, is still there")
	}
	sources, ok := entitySources(t, db, "Shared")
	if !ok {
		t.Fatal("Shared, still sourced by d2, was deleted")
	}
	if !slices.Equal(sources, []string{"d2"}) {
		t.Errorf("Shared sources = %v after d1 was deleted, want [d2]", sources)
	}

	resp, err := tools.DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "d2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entitySources(t, db, "Shared"); ok {
		t.Errorf("Shared outlived its last document (deleted %d, detached %d)", resp.EntityNodesDeleted, resp.EntityNodesDetached)
	}
}

// An entity the document sourced without mentioning it (upsert_entities with
// no chunk ids) goes with the document when nothing else holds it. It used
// to stay: the cleanup only looked at the entities the chunks mentioned.
// One that another document's relation still holds stays, with no source,
// and one that another document still names stays with that source.
func TestKnowledgeDeleteRemovesAnUnmentionedEntityNothingHolds(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "unmentioned")
	ingestWithEntities(t, tools, "d1", "one two three four", []string{"Loose", "Held", "Cited"}, "Loose", "Held", "Cited")
	ingestWithEntities(t, tools, "d3", "five six seven eight", []string{"Other", "Cited"}, "Cited")
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "d3",
		Relations:  []ToolRelationInput{{From: "Other", To: "Held", Type: "related_to"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := entitySources(t, db, "Loose"); ok {
		t.Error("Loose, sourced by d1 alone and held by nothing, is still there")
	}
	sources, ok := entitySources(t, db, "Held")
	if !ok {
		t.Fatal("Held, still the end of a relation of d3, was deleted")
	}
	if len(sources) != 0 {
		t.Errorf("Held sources = %v after d1 was deleted, want none", sources)
	}
	// Cited has no edge left either, but d3 still names it.
	sources, ok = entitySources(t, db, "Cited")
	if !ok {
		t.Fatal("Cited, still sourced by d3, was deleted")
	}
	if !slices.Equal(sources, []string{"d3"}) {
		t.Errorf("Cited sources = %v after d1 was deleted, want [d3]", sources)
	}
}

// knowledge_update rebuilds the document's graph from the request, so it lets
// go of the sources the same way.
func TestKnowledgeUpdateLetsGoOfTheEntitiesItSourced(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "update")
	ingestWithEntities(t, tools, "d2", "zulu yankee xray whiskey", []string{"Shared"})
	ingestWithEntities(t, tools, "d1", "one two three four", []string{"Shared"})

	content := "nine ten eleven twelve"
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "d1", Content: &content}); err != nil {
		t.Fatal(err)
	}
	sources, ok := entitySources(t, db, "Shared")
	if !ok {
		t.Fatal("Shared, still sourced by d2, was deleted")
	}
	if !slices.Equal(sources, []string{"d2"}) {
		t.Errorf("Shared sources = %v after d1 was rewritten without it, want [d2]", sources)
	}
}

// A source list that cannot be read or released stops the delete, which
// then changes nothing: the record, its chunks and the entity stay.
func TestKnowledgeDeleteStopsWhenASourceCannotBeReleased(t *testing.T) {
	// Each breaker, and the error it must stop the delete with: the release
	// runs first, so a table the later steps cannot read either still fails
	// there, and says so.
	breakers := map[string][2]string{
		// SQLite takes the number, Go cannot hold it in a float64.
		"unreadable properties": {`UPDATE graph_nodes SET properties = json_set(properties, '$.weight', json('1e400')) WHERE id = 'entity:shared'`,
			"read the sources of entity:shared"},
		"refused update": {`CREATE TRIGGER refuse BEFORE UPDATE ON graph_nodes WHEN OLD.id = 'entity:shared'
			BEGIN SELECT RAISE(ABORT, 'refused'); END`, "release entity:shared from d1"},
		"unreadable table": {`ALTER TABLE graph_nodes RENAME COLUMN properties TO properties_hidden`,
			"find the entities d1 sourced"},
	}
	for name, breaker := range breakers {
		t.Run(name, func(t *testing.T) {
			db, tools, ctx := openIngestBrain(t, "stops")
			ingestWithEntities(t, tools, "d2", "zulu yankee xray whiskey", []string{"Shared"})
			ingestWithEntities(t, tools, "d1", "one two three four", []string{"Shared", "Alpha"})
			if _, err := db.SQL().Exec(breaker[0]); err != nil {
				t.Fatal(err)
			}
			_, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"})
			if err == nil {
				t.Fatal("the delete went through")
			}
			if !strings.Contains(err.Error(), breaker[1]) {
				t.Errorf("the delete failed with %q, want it to say %q", err, breaker[1])
			}
			var records, chunks, nodes int
			if err := db.SQL().QueryRow(`SELECT
				(SELECT COUNT(*) FROM documents WHERE id = 'd1'),
				(SELECT COUNT(*) FROM embeddings WHERE doc_id = 'd1'),
				(SELECT COUNT(*) FROM graph_nodes WHERE id IN ('entity:shared', 'entity:alpha', 'doc:d1'))`).Scan(&records, &chunks, &nodes); err != nil {
				t.Fatal(err)
			}
			if records != 1 || chunks != 1 || nodes != 3 {
				t.Errorf("after a failed delete: %d record, %d chunk rows, %d of 3 nodes", records, chunks, nodes)
			}
		})
	}
}
