package cortexdb

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// What only beta knows of the entities: the entities only its text
// mentions, the description it wrote last on the shared one, and the list
// that names it.
var outOfScopeEntityMarkers = []string{EntityNodeID("Project Nightjar"), EntityNodeID("BETASECRET"), "BETADESC", `"beta"`, `"description"`, `"source_document_ids"`}

func TestUnderACallerScopeEveryGraphToolLeavesOutAnEntityOnlyAnotherCollectionKnows(t *testing.T) {
	db := scopedGraphBrain(t)
	ctx := WithCallerScope(context.Background(), alphaScope())
	for _, tool := range graphToolCalls() {
		t.Run(tool.name, func(t *testing.T) {
			out, err := tool.call(ctx, db)
			answer := answerOf(t, out, err)
			for _, marker := range outOfScopeEntityMarkers {
				if strings.Contains(answer, marker) {
					t.Errorf("%s serves %q to a caller confined to alpha: %s", tool.name, marker, answer)
				}
			}
			if !strings.Contains(answer, "Kestrel Vale") {
				t.Errorf("%s no longer serves the entity both collections know: %s", tool.name, answer)
			}
		})
	}
}

// A served entity keeps its id and its name: its description and its type
// are the last writer's, whichever collection that was, and its other
// properties and its vector come from the same writes. Without a knowledge
// rule it is served whole.
func TestUnderACallerScopeAnEntityKeepsOnlyItsIdAndName(t *testing.T) {
	db := scopedGraphBrain(t)
	// Beta types the entity. A later write that names a document records
	// it on the entity (source_document_ids), as the ingest does, and keeps
	// beta's type in properties.type.
	for _, entity := range []ToolEntityInput{{Name: "Kestrel Vale", Type: "acquisition_target"}, {Name: "Kestrel Vale"}} {
		if _, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{DocumentID: "b1", Entities: []ToolEntityInput{entity}}); err != nil {
			t.Fatal(err)
		}
	}
	req := ToolGetNodesRequest{NodeIDs: []string{EntityNodeID("Kestrel Vale")}}
	ctx := WithCallerScope(context.Background(), alphaScope())
	out, err := db.GraphRAGTools().GetNodes(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 1 {
		t.Fatalf("get_nodes served %d nodes, want Kestrel Vale", len(out.Nodes))
	}
	node := out.Nodes[0]
	if node.ID != EntityNodeID("Kestrel Vale") || node.Content != "Kestrel Vale" {
		t.Fatalf("the served entity lost its id or name: %+v", node)
	}
	if node.NodeType != "" {
		t.Fatalf("the served entity keeps the type %q the last writer gave it", node.NodeType)
	}
	for key := range node.Properties {
		if key != "name" {
			t.Fatalf("the served entity keeps property %q, want its name alone: %v", key, node.Properties)
		}
	}
	if node.Properties["name"] != "Kestrel Vale" {
		t.Fatalf("the served entity lost its name property: %v", node.Properties)
	}
	if len(node.Vector) != 0 {
		t.Fatalf("the served entity keeps its vector: %v", node.Vector)
	}
	// The time of the last write says when some collection last wrote it.
	if !node.UpdatedAt.IsZero() || !node.ValidFrom.IsZero() || !node.RecordedAt.IsZero() {
		t.Fatalf("the served entity keeps the time of its last write: updated %v, valid from %v, recorded %v", node.UpdatedAt, node.ValidFrom, node.RecordedAt)
	}
	// Left out, not served as a date in year 1.
	if raw := answerOf(t, node, nil); strings.Contains(raw, "updated_at") || strings.Contains(raw, "0001-01-01") {
		t.Fatalf("the served entity carries a time of last write: %s", raw)
	} else if strings.Contains(raw, "acquisition_target") || strings.Contains(raw, "node_type") {
		t.Fatalf("the served entity carries a type: %s", raw)
	}
	stored, err := db.Graph().GetNode(context.Background(), EntityNodeID("Kestrel Vale"))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Properties["description"] == nil || stored.Properties["source_document_ids"] == nil || stored.Properties["type"] != "acquisition_target" || stored.NodeType != "acquisition_target" || stored.UpdatedAt.IsZero() || stored.RecordedAt.IsZero() {
		t.Fatalf("the stored entity was changed, or lacks what the check above needs: %+v", stored)
	}
	for label, ctx := range map[string]context.Context{
		"no scope":           context.Background(),
		"a memory rule only": WithCallerScope(context.Background(), CallerScope{Key: "hermes", Memory: func(MemoryBucket) bool { return true }}),
	} {
		whole, err := db.GraphRAGTools().GetNodes(ctx, ToolGetNodesRequest{NodeIDs: req.NodeIDs, IncludeVectors: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(whole.Nodes) != 1 || whole.Nodes[0].Properties["description"] == nil || len(whole.Nodes[0].Vector) == 0 {
			t.Fatalf("under %s the entity is not served whole: %+v", label, whole.Nodes)
		}
	}
}

// search_paths reports a seed the scope hides as it reports one that does
// not exist.
func TestAPathSeedTheScopeHidesIsReportedUnresolved(t *testing.T) {
	db := scopedGraphBrain(t)
	req := ToolSearchPathsRequest{EntityNames: []string{"Kestrel Vale", "Project Nightjar"}, NodeIDs: []string{graphChunkNodeID("b1", 0)}}
	open, err := db.GraphRAGTools().SearchPaths(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(open.Seeds) != 3 || len(open.Unresolved) != 0 {
		t.Fatalf("without a scope search_paths resolves %v and leaves %v, want the three seeds", open.Seeds, open.Unresolved)
	}
	ctx := WithCallerScope(context.Background(), alphaScope())
	out, err := db.GraphRAGTools().SearchPaths(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Seeds) != 1 || out.Seeds[0].NodeID != EntityNodeID("Kestrel Vale") {
		t.Fatalf("under alpha's scope search_paths resolves %v, want Kestrel Vale alone", out.Seeds)
	}
	if !slices.Equal(out.Unresolved, []string{graphChunkNodeID("b1", 0), "Project Nightjar"}) {
		t.Fatalf("under alpha's scope search_paths leaves %v unresolved, want beta's chunk and Project Nightjar", out.Unresolved)
	}
}

// A walk does not start from a node the scope hides: from beta's chunk,
// from an entity only beta knows, or from Zeus's memory, the graph tools
// answer as from a node that does not exist, and do not reveal what the
// hidden node links to.
func TestAWalkDoesNotStartFromANodeTheScopeHides(t *testing.T) {
	db := scopedGraphBrain(t)
	tools, km := db.GraphRAGTools(), db.KnowledgeMemory()
	ctx := WithCallerScope(context.Background(), alphaScope())
	for _, seed := range []string{graphChunkNodeID("b1", 0), EntityNodeID("Project Nightjar"), memoryGraphNodeID("zeus-mem")} {
		expanded, err := tools.ExpandGraph(ctx, ToolExpandGraphRequest{NodeIDs: []string{seed}, MaxHops: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(expanded.Nodes) != 0 || len(expanded.Edges) != 0 {
			t.Errorf("expand_graph from hidden %s serves %d nodes and %d edges", seed, len(expanded.Nodes), len(expanded.Edges))
		}
		neighbors, err := km.Neighbors(ctx, KnowledgeMemoryNeighborsRequest{NodeID: seed, MaxDepth: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(neighbors.Neighbors) != 0 {
			t.Errorf("knowledge_memory_neighbors of hidden %s serves %d nodes", seed, len(neighbors.Neighbors))
		}
	}
	// A memory rule alone keeps a walk from starting at another user's
	// memory too.
	hermes := WithCallerScope(context.Background(), CallerScope{Key: "hermes", Memory: func(b MemoryBucket) bool { return b.UserID == "hermes" }})
	if expanded, err := tools.ExpandGraph(hermes, ToolExpandGraphRequest{NodeIDs: []string{memoryGraphNodeID("zeus-mem")}}); err != nil || len(expanded.Nodes) != 0 {
		t.Errorf("expand_graph from Zeus's memory under Hermes's memory rule serves %+v, %v", expanded, err)
	}
	chunks, err := tools.SearchChunksByEntities(ctx, ToolSearchChunksByEntitiesRequest{EntityNames: []string{"Project Nightjar"}, MaxHops: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks.Chunks) != 0 {
		t.Errorf("search_chunks_by_entities from Project Nightjar reaches alpha's chunks through beta's relation: %+v", chunks.Chunks)
	}
	open, err := tools.SearchChunksByEntities(context.Background(), ToolSearchChunksByEntitiesRequest{EntityNames: []string{"Project Nightjar"}, MaxHops: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(open.Chunks) == 0 {
		t.Fatal("without a scope search_chunks_by_entities from Project Nightjar finds nothing, so the check above proves nothing")
	}
}

// Under a knowledge rule, an entity no collection lists (written by
// upsert_entities, by a memory, or before the list existed) is not served:
// nothing says which collection it came from. Without a scope it is.
func TestUnderAKnowledgeRuleAnEntityWithNoCollectionListIsNotServed(t *testing.T) {
	db := scopedGraphBrain(t)
	if _, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Lone Star", Description: "written with no collection"}}}); err != nil {
		t.Fatal(err)
	}
	req := ToolGetNodesRequest{NodeIDs: []string{EntityNodeID("Lone Star")}}
	open, err := db.GraphRAGTools().GetNodes(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(open.Nodes) != 1 {
		t.Fatalf("without a scope get_nodes served %d nodes, want Lone Star", len(open.Nodes))
	}
	out, err := db.GraphRAGTools().GetNodes(WithCallerScope(context.Background(), alphaScope()), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 0 {
		t.Fatalf("under alpha's scope get_nodes served an entity no collection lists: %+v", out.Nodes)
	}
}

// When the seeds of a walk cannot be read, a caller under a scope walks
// from none of them and is told why; without a scope rule nothing is read
// and every seed is kept.
func TestUnderACallerScopeSeedsThatCannotBeReadAreNotWalkedFrom(t *testing.T) {
	db := scopedGraphBrain(t)
	_ = db.Close()
	ids := []string{EntityNodeID("Kestrel Vale"), "entity:nobody"}
	if got, err := db.servedSeeds(WithCallerScope(context.Background(), alphaScope()), ids); len(got) != 0 || err == nil {
		t.Fatalf("a closed brain under a scope served seeds %v, %v, want none and an error", got, err)
	}
	if got, err := db.servedSeeds(WithCallerScope(context.Background(), CallerScope{Key: "k"}), ids); !slices.Equal(got, ids) || err != nil {
		t.Fatalf("a scope with no rule served seeds %v, %v, want %v", got, err, ids)
	}
}

// A document node of the caller's own collection is served as it is: the
// entity rule is not its rule.
func TestUnderACallerScopeADocumentNodeOfTheCallersCollectionIsServedWhole(t *testing.T) {
	db := scopedGraphBrain(t)
	req := ToolGetNodesRequest{NodeIDs: []string{graphDocumentNodeID("a1")}}
	open, err := db.GraphRAGTools().GetNodes(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(open.Nodes) != 1 {
		t.Fatalf("without a scope get_nodes served %d nodes, want alpha's document node", len(open.Nodes))
	}
	out, err := db.GraphRAGTools().GetNodes(WithCallerScope(context.Background(), alphaScope()), req)
	if got, want := answerOf(t, out, err), answerOf(t, open, nil); got != want {
		t.Fatalf("under alpha's scope get_nodes serves alpha's document node as\n%s\nwant\n%s", got, want)
	}
}

// knowledge_memory_expand_entity_context given node ids answers a node the
// scope hides (beta's chunk, Zeus's memory, an entity only beta knows) as a
// node that does not exist, its own id echoed aside.
func TestExpandEntityContextAnswersAHiddenNodeAsAMissingOne(t *testing.T) {
	db := scopedGraphBrain(t)
	answer := func(ctx context.Context, id string) string {
		out, err := db.KnowledgeMemory().ExpandEntityContext(ctx, KnowledgeMemoryExpandEntityContextRequest{EntityIDs: []string{id, EntityNodeID("Kestrel Vale")}})
		return strings.ReplaceAll(answerOf(t, out, err), id, "<id>")
	}
	scoped := WithCallerScope(context.Background(), alphaScope())
	missing := answer(scoped, "entity:no_such")
	for _, id := range []string{graphChunkNodeID("b1", 0), memoryGraphNodeID("zeus-mem"), EntityNodeID("Project Nightjar")} {
		if got := answer(scoped, id); got != missing {
			t.Errorf("under alpha's scope, entity_ids %s answers\n%s\nwant, as for a missing node,\n%s", id, got, missing)
		}
		if answer(context.Background(), id) == answer(context.Background(), "entity:no_such") {
			t.Errorf("without a scope entity_ids %s answers as a missing node, so the check above proves nothing", id)
		}
	}
}
