package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func openEndpointBrain(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "endpoints.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// A relation's end the document names and nothing stands for is an entity
// of the document: the save writes it, linked where the text names it, and
// deleting the document takes it away again. Before, the save failed on the
// foreign key and kept nothing (#261).
func TestKnowledgeSaveDeclaresTheEntitiesItsRelationsName(t *testing.T) {
	db := openEndpointBrain(t)
	ctx := context.Background()
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "ada", Content: "Ada described the engine.",
		Relations: []ToolRelationInput{{From: "Ada", To: "Analytical Engine", Type: "described"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	node, err := db.Graph().GetNode(ctx, EntityNodeID("Analytical Engine"))
	if err != nil || node == nil || node.Content != "Analytical Engine" {
		t.Fatalf("endpoint node %+v, %v", node, err)
	}
	edges, err := db.Graph().GetEdges(ctx, EntityNodeID("Ada"), "out")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range edges {
		if e.EdgeType == "described" && e.ToNodeID == EntityNodeID("Analytical Engine") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no described edge: %+v", edges)
	}
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "ada"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Graph().GetNode(ctx, EntityNodeID("Analytical Engine")); n != nil {
		t.Fatalf("the entity the document alone stated outlived it: %+v", n)
	}
}

// An end given by node id that names no node is refused by its name, and the
// document is not saved (#261).
func TestKnowledgeSaveRefusesARelationToAMissingNodeID(t *testing.T) {
	db := openEndpointBrain(t)
	ctx := context.Background()
	_, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "x", Content: "Ada knew someone.",
		Relations: []ToolRelationInput{{From: "Ada", To: "entity:nobody", Type: "knew"}}})
	if err == nil || !strings.Contains(err.Error(), `"entity:nobody"`) || strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("err = %v, want one naming entity:nobody", err)
	}
	if got, err := db.GetKnowledge(ctx, KnowledgeGetRequest{KnowledgeID: "x"}); err == nil && got != nil {
		t.Fatalf("the document was saved anyway: %+v", got)
	}
}

// An end that names an alias reaches the entity carrying it, and no new
// entity is made for it (#261).
func TestKnowledgeSaveRelationEndpointFollowsAnAlias(t *testing.T) {
	db := openEndpointBrain(t)
	ctx := context.Background()
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Acme Corp"}, {Name: "ACME Ltd"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "deal", Content: "Bea signed with them.",
		Relations: []ToolRelationInput{{From: "Bea", To: "ACME Ltd", Type: "signed_with"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if n, _ := db.Graph().GetNode(ctx, EntityNodeID("ACME Ltd")); n != nil {
		t.Fatalf("an entity was made for the alias: %+v", n)
	}
	edges, err := db.Graph().GetEdges(ctx, EntityNodeID("Acme Corp"), "in")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if e.EdgeType == "signed_with" {
			return
		}
	}
	t.Fatalf("the relation did not reach the canonical entity: %+v", edges)
}

// upsert_relations names the endpoint that is missing, and still writes the
// relations whose endpoints exist (#261).
func TestUpsertRelationsNamesTheMissingEndpoint(t *testing.T) {
	db := openEndpointBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Ada"}, {Name: "Charles Babbage"}}}); err != nil {
		t.Fatal(err)
	}
	_, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{{From: "Ada", To: "Analytical Engine", Type: "described"}}})
	if err == nil || !strings.Contains(err.Error(), `endpoint "Analytical Engine" is not in the store`) || strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("err = %v", err)
	}
	resp, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "Ada", To: "Charles Babbage", Type: "knew"},
		{From: "Ada", To: "Analytical Engine", Type: "described"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Written != 1 || len(resp.Rejected) != 1 || !strings.Contains(resp.Rejected[0], `"Analytical Engine"`) {
		t.Fatalf("written %d rejected %v", resp.Written, resp.Rejected)
	}
}
