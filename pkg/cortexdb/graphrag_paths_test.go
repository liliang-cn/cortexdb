package cortexdb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func seedPathGraph(t *testing.T) (*DB, *GraphRAGToolbox, map[string]string) {
	t.Helper()
	db, tools, ctx := newDisambiguationDB(t)
	docs := []struct {
		id, text string
		ents     []string
		rel      [3]string
	}{
		{"p1", "Alice Chen works at Borealis Labs.", []string{"Alice Chen", "Borealis Labs"}, [3]string{"Alice Chen", "works_at", "Borealis Labs"}},
		{"p2", "Borealis Labs built the Kestrel satellite.", []string{"Borealis Labs", "Kestrel"}, [3]string{"Borealis Labs", "built", "Kestrel"}},
		{"p3", "Alice Chen and Kestrel appear in the same newsletter.", []string{"Alice Chen", "Kestrel"}, [3]string{"Alice Chen", "co_occurs_with", "Kestrel"}},
	}
	chunkOf := map[string]string{}
	for _, d := range docs {
		ing, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: d.id, Content: d.text})
		if err != nil {
			t.Fatal(err)
		}
		chunk := ing.ChunkNodeIDs[0]
		chunkOf[d.id] = chunk
		ents := []ToolEntityInput{}
		for _, e := range d.ents {
			ents = append(ents, ToolEntityInput{Name: e, Type: "entity", ChunkIDs: []string{chunk}})
		}
		if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: d.id, Entities: ents}); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{DocumentID: d.id, Relations: []ToolRelationInput{
			{From: d.rel[0], Type: d.rel[1], To: d.rel[2], ChunkIDs: []string{chunk}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	return db, tools, chunkOf
}

func TestSearchPathsCitesChunks(t *testing.T) {
	db, tools, chunkOf := seedPathGraph(t)
	ctx := t.Context()

	resp, err := db.SearchPaths(ctx, ToolSearchPathsRequest{
		EntityNames: []string{"alice chen", "Kestrel", "Nobody Known"},
		WithText:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Seeds) != 2 || len(resp.Unresolved) != 1 || resp.Unresolved[0] != "Nobody Known" {
		t.Fatalf("seeds %+v unresolved %v", resp.Seeds, resp.Unresolved)
	}
	if len(resp.Paths) != 2 {
		t.Fatalf("want direct co-occurrence path and the two-hop chain, got %+v", resp.Paths)
	}
	// The one-hop path scores 1.0 and ranks first without a policy.
	if resp.Paths[0].Hops != 1 || resp.Paths[1].Hops != 2 {
		t.Fatalf("order: %+v", resp.Paths)
	}
	chain := resp.Paths[1]
	if chain.Edges[0].Type != "works_at" || chain.Edges[1].Type != "built" {
		t.Fatalf("chain edges %+v", chain.Edges)
	}
	if got := chain.Edges[0].ChunkIDs; len(got) != 1 || got[0] != chunkOf["p1"] {
		t.Fatalf("edge does not cite its chunk: %v", got)
	}
	if chain.Edges[0].FromName != "Alice Chen" || !strings.Contains(chain.Sentence, "Borealis Labs") {
		t.Fatalf("names/sentence: %+v", chain)
	}
	if len(resp.ChunkIDs) != 3 || len(resp.Chunks) != 3 {
		t.Fatalf("chunk ids %v, chunks %d", resp.ChunkIDs, len(resp.Chunks))
	}

	// A policy that drops the statistical edge leaves only the chain.
	resp, err = tools.SearchPaths(ctx, ToolSearchPathsRequest{
		EntityNames:      []string{"Alice Chen", "Kestrel"},
		RelationPolicies: graph.RelationPolicies{"co_occurs_with": {Weight: -1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Paths) != 1 || resp.Paths[0].Hops != 2 || len(resp.ChunkIDs) != 2 {
		t.Fatalf("policy ignored: %+v", resp.Paths)
	}

	// Through the generic dispatcher, as an MCP client reaches it.
	out, err := tools.Call(ctx, "search_paths", json.RawMessage(`{"entity_names":["Alice Chen","Kestrel"],"relation_policies":{"co_occurs_with":{"weight":0.1}}}`))
	if err != nil {
		t.Fatal(err)
	}
	called := out.(*ToolSearchPathsResponse)
	if len(called.Paths) != 2 || called.Paths[0].Hops != 2 {
		t.Fatalf("weighted via Call: %+v", called.Paths)
	}
}

func TestGraphRAGReturnPathsAndExpandScores(t *testing.T) {
	_, tools, _ := seedPathGraph(t)
	ctx := t.Context()

	plain, err := tools.SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
		Query: "Alice Chen Kestrel", EntityNames: []string{"Alice Chen", "Kestrel"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Paths) != 0 {
		t.Fatal("paths returned without being asked for")
	}
	withPaths, err := tools.SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
		Query: "Alice Chen Kestrel", EntityNames: []string{"Alice Chen", "Kestrel"}, ReturnPaths: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withPaths.Paths) != 2 {
		t.Fatalf("return_paths: %+v", withPaths.Paths)
	}
	if len(withPaths.Chunks) != len(plain.Chunks) {
		t.Fatal("return_paths changed the chunk result")
	}

	start := resolveEntityNodeID("", "Alice Chen")
	exp, err := tools.ExpandGraph(ctx, ToolExpandGraphRequest{NodeIDs: []string{start}, MaxHops: 2})
	if err != nil {
		t.Fatal(err)
	}
	if exp.Scores != nil {
		t.Fatal("scores without a policy")
	}
	exp, err = tools.ExpandGraph(ctx, ToolExpandGraphRequest{
		NodeIDs: []string{start}, MaxHops: 2, NodeTypes: []string{"entity"},
		RelationPolicies: graph.RelationPolicies{"co_occurs_with": {Weight: 0.2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	borealis, kestrel := resolveEntityNodeID("", "Borealis Labs"), resolveEntityNodeID("", "Kestrel")
	// Kestrel: direct co-occurrence 0.2/2 = 0.1, chain 1/3 — the chain wins.
	if exp.Scores[borealis] != 0.5 || exp.Scores[kestrel] < 0.33 || exp.Scores[kestrel] > 0.34 {
		t.Fatalf("scores %v", exp.Scores)
	}
}
