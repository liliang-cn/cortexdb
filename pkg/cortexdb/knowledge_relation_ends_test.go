package cortexdb

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func entityExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	node, err := db.Graph().GetNode(context.Background(), EntityNodeID(name))
	return err == nil && node != nil
}

func saveWithRelation(t *testing.T, db *DB, id, from, to string) {
	t.Helper()
	if _, err := db.SaveKnowledge(context.Background(), KnowledgeSaveRequest{
		KnowledgeID: id, Content: "one two three four five six",
		Entities:  []ToolEntityInput{{Name: from, Type: "entity"}, {Name: to, Type: "entity"}},
		Relations: []ToolRelationInput{{From: from, To: to, Type: "related_to"}},
	}); err != nil {
		t.Fatal(err)
	}
}

// knowledge_save names the document among the sources of the entities it
// declares, keeping the documents that named them before, as upsert_entities
// does. The cleanup of a document reads that list to tell the entities it
// alone asserted from the ones something else still holds.
func TestKnowledgeSaveRecordsTheDocumentAmongItsEntitiesSources(t *testing.T) {
	// Both plans: the lexical one, and the one a brain with an embedder runs.
	for name, opts := range map[string][]Option{"lexical": nil, "embedder": {WithEmbedder(NewMockEmbedder(8))}} {
		t.Run(name, func(t *testing.T) {
			db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "record.db")), opts...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			saveWithRelation(t, db, "d1", "Alpha", "Beta")
			saveWithRelation(t, db, "d2", "Beta", "Gamma")
			for entity, want := range map[string][]string{"Alpha": {"d1"}, "Beta": {"d1", "d2"}, "Gamma": {"d2"}} {
				node, err := db.Graph().GetNode(context.Background(), EntityNodeID(entity))
				if err != nil {
					t.Fatal(err)
				}
				if got := toStringSlice(node.Properties["source_document_ids"]); !slices.Equal(got, want) {
					t.Errorf("%s sources = %v, want %v", entity, got, want)
				}
			}
		})
	}
}

// knowledge_update rebuilds a document's graph from the request. An entity
// the previous version declared and tied in by a relation only (declared
// entities get no mention edge) stayed when the new version dropped it: the
// relation went, and the cleanup only looked for orphans among the entities
// the chunks mentioned. It was left with no edge, cited by nothing.
func TestKnowledgeUpdateDropsAnEntityOnlyItsRelationsHeld(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "update")
	saveWithRelation(t, db, "d1", "Alpha", "Beta")
	content := "seven eight nine ten"
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{
		KnowledgeID: "d1", Content: &content,
		Entities: []ToolEntityInput{{Name: "Alpha", Type: "entity"}},
	}); err != nil {
		t.Fatal(err)
	}
	if entityExists(t, db, "Beta") {
		t.Error("Beta, dropped by the new version, is still there")
	}
	if !entityExists(t, db, "Alpha") {
		t.Error("Alpha, declared again by the new version, is gone")
	}
}

// knowledge_delete takes those entities with the document, and keeps the one
// another document's relation still holds.
func TestKnowledgeDeleteTakesTheEntitiesOnlyItsRelationsHeld(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "delete")
	saveWithRelation(t, db, "d1", "Alpha", "Beta")
	saveWithRelation(t, db, "d2", "Beta", "Gamma")
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if entityExists(t, db, "Alpha") {
		t.Error("Alpha, held only by a relation of d1, is still there")
	}
	if !entityExists(t, db, "Beta") {
		t.Error("Beta, still the end of a relation of d2, is gone")
	}
}

// An entity the document declared with neither a relation nor a mention had
// no edge from the start: its source is all that ties it to the document,
// and it goes with it.
func TestKnowledgeDeleteTakesADeclaredEntityWithNoEdge(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "noedge")
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "d1", Content: "one two three four five six",
		Entities: []ToolEntityInput{{Name: "Alone", Type: "entity"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if entityExists(t, db, "Alone") {
		t.Error("Alone, declared by d1 alone, is still there")
	}
}

// A document, a chunk or a memory at the end of one of the document's
// relations is not an entity: it stays when the document goes, whatever
// edges it has left. Its own row says when it goes.
func TestKnowledgeDeleteKeepsANonEntityAtTheEndOfItsRelation(t *testing.T) {
	for _, nodeType := range []string{"document", "chunk", "memory"} {
		t.Run(nodeType, func(t *testing.T) {
			db, _, ctx := openIngestBrain(t, nodeType)
			if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
				KnowledgeID: "d1", Content: "one two three four five six",
				Entities: []ToolEntityInput{{Name: "Alpha", Type: "entity"}},
			}); err != nil {
				t.Fatal(err)
			}
			end := nodeType + ":elsewhere"
			if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{ID: end, NodeType: nodeType, Content: "kept"}); err != nil {
				t.Fatal(err)
			}
			if err := db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
				ID: "edge:alpha-" + nodeType, FromNodeID: EntityNodeID("Alpha"), ToNodeID: end,
				EdgeType: "related_to", Weight: 1, Properties: map[string]interface{}{"document_id": "d1"},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"}); err != nil {
				t.Fatal(err)
			}
			if node, err := db.Graph().GetNode(ctx, end); err != nil || node == nil {
				t.Errorf("the %s at the end of the relation went with the document: %v", nodeType, err)
			}
			if entityExists(t, db, "Alpha") {
				t.Error("Alpha, held only by a relation of d1, is still there")
			}
		})
	}
}

// An entity another document still sources stays when a relation of the
// deleted document was all that tied it in: d2 sourced it with no mention,
// d1 related it to Beta.
func TestKnowledgeDeleteKeepsARelationEndAnotherDocumentSources(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "sourced")
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "d2", Content: "zulu yankee xray whiskey"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "d2", Entities: []ToolEntityInput{{Name: "Kept", Type: "entity"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "d1", Content: "one two three four five six",
		Entities:  []ToolEntityInput{{Name: "Beta", Type: "entity"}},
		Relations: []ToolRelationInput{{From: "Kept", To: "Beta", Type: "related_to"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if !entityExists(t, db, "Kept") {
		t.Error("Kept, still sourced by d2, went with d1")
	}
	if entityExists(t, db, "Beta") {
		t.Error("Beta, declared by d1 alone, is still there")
	}
}

// An entity another document declares stays, edge or none: d2 declared it
// with no relation, d1 declared it and related it to Beta.
func TestKnowledgeDeleteKeepsARelationEndAnotherDocumentDeclares(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "declared")
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "d2", Content: "zulu yankee xray whiskey",
		Entities: []ToolEntityInput{{Name: "Kept", Type: "entity"}},
	}); err != nil {
		t.Fatal(err)
	}
	saveWithRelation(t, db, "d1", "Kept", "Beta")
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if !entityExists(t, db, "Kept") {
		t.Error("Kept, still declared by d2, went with d1")
	}
	if entityExists(t, db, "Beta") {
		t.Error("Beta, declared by d1 alone, is still there")
	}
}

// A node no document sources, written by hand, is not the document's to
// remove: it stays when the document that pointed an edge at it goes.
func TestKnowledgeDeleteKeepsAHandMadeNodeItsRelationReached(t *testing.T) {
	db, _, ctx := openIngestBrain(t, "handmade")
	if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{ID: "person:ada", NodeType: "person", Content: "Ada"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "d1", Content: "one two three four five six",
		Entities: []ToolEntityInput{{Name: "Alpha", Type: "entity"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
		ID: "edge:alpha-ada", FromNodeID: EntityNodeID("Alpha"), ToNodeID: "person:ada",
		EdgeType: "related_to", Weight: 1, Properties: map[string]interface{}{"document_id": "d1"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if node, err := db.Graph().GetNode(ctx, "person:ada"); err != nil || node == nil {
		t.Errorf("the hand-made node went with d1: %v", err)
	}
	if entityExists(t, db, "Alpha") {
		t.Error("Alpha, declared by d1 alone, is still there")
	}
}
