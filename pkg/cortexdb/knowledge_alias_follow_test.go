package cortexdb

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// knowledge_save, ingest_document (its media form) and InsertGraphDocument
// follow the aliases entity resolution records, as upsert_entities and
// upsert_relations do (entity_alias_follow_test.go).

// recordAliases records names on an entity as entity resolution does once
// it has folded the entities of those names into it.
func recordAliases(t *testing.T, db *DB, canonical string, aliases ...string) {
	t.Helper()
	ctx := context.Background()
	node, err := db.Graph().GetNode(ctx, EntityNodeID(canonical))
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = aliases
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
}

// requireNoNode fails when a node stands under id.
func requireNoNode(t *testing.T, db *DB, id string) {
	t.Helper()
	nodes, err := db.Graph().GetNodesBatch(context.Background(), []string{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("%s came back: %+v", id, nodes[0].Properties)
	}
}

// requireCanonicalAcme checks that Acme Corp kept its own name, content and
// description and names docs among its sources.
func requireCanonicalAcme(t *testing.T, db *DB, docs ...string) *graph.GraphNode {
	t.Helper()
	node, err := db.Graph().GetNode(context.Background(), EntityNodeID("Acme Corp"))
	if err != nil {
		t.Fatal(err)
	}
	if node.Content != "Acme Corp" || node.Properties["name"] != "Acme Corp" || node.Properties["description"] != "the canonical name" {
		t.Fatalf("the canonical entity now reads %q, name %v, description %v", node.Content, node.Properties["name"], node.Properties["description"])
	}
	sources := toStringSlice(node.Properties["source_document_ids"])
	for _, doc := range docs {
		if !slices.Contains(sources, doc) {
			t.Fatalf("the canonical entity's documents are %v, want %v among them", sources, docs)
		}
	}
	if !slices.Contains(toStringSlice(node.Properties["aliases"]), "ACME Ltd") {
		t.Fatalf("the canonical entity lost its aliases: %v", node.Properties["aliases"])
	}
	return node
}

// edgesOf lists the edges at id as "from type to".
func edgesOf(t *testing.T, db *DB, id string) []string {
	t.Helper()
	edges, err := db.Graph().GetEdges(context.Background(), id, "both")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range edges {
		out = append(out, e.FromNodeID+" "+e.EdgeType+" "+e.ToNodeID)
	}
	slices.Sort(out)
	return out
}

func TestKnowledgeSaveEntitiesUnderAnAliasWriteTheCanonicalEntity(t *testing.T) {
	db, _, ctx := aliasBrain(t)
	acme := EntityNodeID("Acme Corp")
	resp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "notes on suppliers", Entities: []ToolEntityInput{
		{Name: "acme ltd", Description: "an abbreviation", Metadata: map[string]string{"ticker": "ACM"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(resp.EntityNodeIDs, acme) || slices.Contains(resp.EntityNodeIDs, EntityNodeID("ACME Ltd")) {
		t.Fatalf("wrote %v, want the canonical entity %s", resp.EntityNodeIDs, acme)
	}
	requireNoNode(t, db, EntityNodeID("ACME Ltd"))
	// An entry under an alias adds its document and changes nothing the
	// entity says: its description and metadata describe the alias.
	node := requireCanonicalAcme(t, db, "d0", "k1")
	if _, ok := node.Properties["ticker"]; ok {
		t.Fatalf("the alias entry's metadata reached the entity: %v", node.Properties)
	}
}

func TestKnowledgeSaveContentNamingAnAliasMentionsTheCanonicalEntity(t *testing.T) {
	db, _, ctx := aliasBrain(t)
	acme := EntityNodeID("Acme Corp")
	resp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "Yesterday ACME Ltd shipped the parts."})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(resp.EntityNodeIDs, acme) {
		t.Fatalf("wrote %v, want the canonical entity %s", resp.EntityNodeIDs, acme)
	}
	requireNoNode(t, db, EntityNodeID("ACME Ltd"))
	node := requireCanonicalAcme(t, db, "d0", "k1")
	if !slices.Contains(toStringSlice(node.Properties[entityCollectionsKey]), defaultGraphRAGCollection) {
		t.Fatalf("the canonical entity's collections are %v", node.Properties[entityCollectionsKey])
	}
	want := graphChunkNodeID("k1", 0) + " mentions " + acme
	if !slices.Contains(edgesOf(t, db, acme), want) {
		t.Fatalf("no %q among %v", want, edgesOf(t, db, acme))
	}
}

func TestKnowledgeSaveRelationsFollowTheAliasAtEitherEnd(t *testing.T) {
	db, _, ctx := aliasBrain(t)
	acme, widget := EntityNodeID("Acme Corp"), EntityNodeID("Widget")
	resp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "notes on suppliers", Relations: []ToolRelationInput{
		{From: "ACME Ltd", To: "Widget", Type: "makes"},
		{From: "Widget", To: "Acme ltd", Type: "made_by"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{relationEdgeID(acme, widget, "makes", "k1"), relationEdgeID(widget, acme, "made_by", "k1")}
	slices.Sort(want)
	if !slices.Equal(resp.RelationEdgeIDs, want) {
		t.Fatalf("relation edges %v, want %v", resp.RelationEdgeIDs, want)
	}
	got := edgesOf(t, db, acme)
	for _, edge := range []string{acme + " makes " + widget, widget + " made_by " + acme} {
		if !slices.Contains(got, edge) {
			t.Fatalf("no %q among %v", edge, got)
		}
	}
	requireNoNode(t, db, EntityNodeID("ACME Ltd"))
}

// A request that names the canonical entity and its alias writes one node,
// and the canonical's own update is kept, in either order.
func TestKnowledgeSaveKeepsTheCanonicalsOwnUpdateBesideItsAlias(t *testing.T) {
	for _, order := range [][]ToolEntityInput{
		{{Name: "Acme Corp", Description: "updated"}, {Name: "ACME Ltd", Description: "the alias speaking"}},
		{{Name: "ACME Ltd", Description: "the alias speaking"}, {Name: "Acme Corp", Description: "updated"}},
	} {
		db, _, ctx := aliasBrain(t)
		acme := EntityNodeID("Acme Corp")
		resp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "Yesterday ACME Ltd shipped the parts.", Entities: order})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(resp.EntityNodeIDs, []string{acme}) {
			t.Fatalf("wrote %v, want %s alone", resp.EntityNodeIDs, acme)
		}
		requireNoNode(t, db, EntityNodeID("ACME Ltd"))
		node, err := db.Graph().GetNode(ctx, acme)
		if err != nil {
			t.Fatal(err)
		}
		if node.Properties["description"] != "updated" || node.Properties["name"] != "Acme Corp" {
			t.Fatalf("the canonical's own update was lost: %v", node.Properties)
		}
		if docs := toStringSlice(node.Properties["source_document_ids"]); !slices.Contains(docs, "k1") || !slices.Contains(docs, "d0") {
			t.Fatalf("documents %v", docs)
		}
	}
}

// An id the writer states is written as given, and so is a relation end it
// states.
func TestKnowledgeSaveWritesAStatedIDAsGiven(t *testing.T) {
	db, _, ctx := aliasBrain(t)
	ltd, widget := EntityNodeID("ACME Ltd"), EntityNodeID("Widget")
	resp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "notes on suppliers",
		Entities:  []ToolEntityInput{{ID: ltd, Name: "ACME Ltd"}},
		Relations: []ToolRelationInput{{From: ltd, To: "Widget", Type: "makes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resp.EntityNodeIDs, []string{ltd}) {
		t.Fatalf("wrote %v, want the stated id %s", resp.EntityNodeIDs, ltd)
	}
	// The given entity is the document's, so its chunk mentions it (#247).
	if got := edgesOf(t, db, ltd); !slices.Equal(got, []string{"chunk:k1:000 mentions " + ltd, ltd + " makes " + widget}) {
		t.Fatalf("edges of the stated id %v", got)
	}
	// Once a node stands under the name, the bare name is written to it.
	again, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k2", Content: "Yesterday ACME Ltd shipped the parts."})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(again.EntityNodeIDs, ltd) || slices.Contains(again.EntityNodeIDs, EntityNodeID("Acme Corp")) {
		t.Fatalf("wrote %v, want the node that stands under the name", again.EntityNodeIDs)
	}
}

// A confined caller's alias is followed only into an entity its scope
// serves: an entity of a collection it cannot read is not reached by a name
// it may not know is the same thing.
func TestKnowledgeSaveUnderACallerScopeFollowsOnlyIntoAnEntityItCanRead(t *testing.T) {
	open := func(t *testing.T) (*DB, context.Context) {
		db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "scope.db")))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		ctx := context.Background()
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "b1", Collection: "beta", Content: "Beta notes.",
			Entities: []ToolEntityInput{{Name: "Acme Corp", Description: "the canonical name"}}}); err != nil {
			t.Fatal(err)
		}
		recordAliases(t, db, "Acme Corp", "ACME Ltd")
		return db, ctx
	}
	acme, ltd := EntityNodeID("Acme Corp"), EntityNodeID("ACME Ltd")
	save := func(t *testing.T, ctx context.Context, db *DB) *KnowledgeSaveResponse {
		t.Helper()
		resp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "a1", Collection: "alpha", Content: "Yesterday ACME Ltd shipped the parts.",
			Entities:  []ToolEntityInput{{Name: "ACME Ltd"}, {Name: "Widget"}},
			Relations: []ToolRelationInput{{From: "ACME Ltd", To: "Widget", Type: "makes"}}})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	t.Run("hidden", func(t *testing.T) {
		db, ctx := open(t)
		resp := save(t, WithCallerScope(ctx, alphaScope()), db)
		if !slices.Contains(resp.EntityNodeIDs, ltd) || slices.Contains(resp.EntityNodeIDs, acme) {
			t.Fatalf("wrote %v, want the name's own node", resp.EntityNodeIDs)
		}
		node, err := db.Graph().GetNode(ctx, acme)
		if err != nil {
			t.Fatal(err)
		}
		if got := toStringSlice(node.Properties[entityCollectionsKey]); !slices.Equal(got, []string{"beta"}) {
			t.Fatalf("the hidden entity's collections are %v", got)
		}
		if docs := toStringSlice(node.Properties["source_document_ids"]); slices.Contains(docs, "a1") {
			t.Fatalf("the hidden entity names a1 among %v", docs)
		}
		for _, edge := range edgesOf(t, db, acme) {
			if edge != graphChunkNodeID("b1", 0)+" mentions "+acme {
				t.Fatalf("the hidden entity gained %q", edge)
			}
		}
	})
	t.Run("readable", func(t *testing.T) {
		db, ctx := open(t)
		scope := alphaScope()
		scope.Knowledge = func(c string) bool { return c == "alpha" || c == "beta" }
		resp := save(t, WithCallerScope(ctx, scope), db)
		if !slices.Contains(resp.EntityNodeIDs, acme) || slices.Contains(resp.EntityNodeIDs, ltd) {
			t.Fatalf("wrote %v, want the canonical entity", resp.EntityNodeIDs)
		}
		requireNoNode(t, db, ltd)
	})
	t.Run("unconfined", func(t *testing.T) {
		db, ctx := open(t)
		resp := save(t, ctx, db)
		if !slices.Contains(resp.EntityNodeIDs, acme) || slices.Contains(resp.EntityNodeIDs, ltd) {
			t.Fatalf("wrote %v, want the canonical entity", resp.EntityNodeIDs)
		}
		requireNoNode(t, db, ltd)
	})
}

// A document saved before the merge linked the folded entity. Saved again,
// it links the canonical one and the folded entity stays gone, even when the
// document was the canonical entity's only source.
func TestReSavingADocumentThatLinkedTheFoldedEntityLinksTheCanonical(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "resave.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	acme, ltd := EntityNodeID("Acme Corp"), EntityNodeID("ACME Ltd")
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "Acme Corp signed. Yesterday ACME Ltd shipped the parts."}); err != nil {
		t.Fatal(err)
	}
	// The merge: the folded node goes, its mentions move to the one kept,
	// and its name is recorded there.
	chunk := graphChunkNodeID("k1", 0)
	if err := db.Graph().DeleteNode(ctx, ltd); err != nil {
		t.Fatal(err)
	}
	recordAliases(t, db, "Acme Corp", "ACME Ltd")

	resp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "Acme Corp signed. Yesterday ACME Ltd shipped the parts, again."})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(resp.EntityNodeIDs, ltd) {
		t.Fatalf("wrote %v, the folded entity among them", resp.EntityNodeIDs)
	}
	requireNoNode(t, db, ltd)
	node, err := db.Graph().GetNode(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(toStringSlice(node.Properties["aliases"]), "ACME Ltd") {
		t.Fatalf("the canonical entity lost its aliases: %v", node.Properties)
	}
	if got := edgesOf(t, db, acme); !slices.Equal(got, []string{chunk + " mentions " + acme}) {
		t.Fatalf("edges of the canonical entity %v", got)
	}
}

// aliasExtractor finds the alias and a relation from it in every chunk.
type aliasExtractor struct{}

func (aliasExtractor) Extract(context.Context, string) (*GraphExtraction, error) {
	return &GraphExtraction{
		Entities:      []GraphEntity{{Name: "ACME Ltd"}, {Name: "Widget"}},
		Relationships: []GraphRelationship{{From: "ACME Ltd", To: "Widget", Type: "makes"}},
	}, nil
}

func TestInsertGraphDocumentNamingAnAliasWritesTheCanonicalEntity(t *testing.T) {
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "insert.db"))
	cfg.Dimensions = 4
	db, err := Open(cfg, WithEmbedder(newKeywordEmbedder("acme", "widget", "parts", "shipped")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: "d0", Entities: []ToolEntityInput{
		{Name: "Acme Corp", Description: "the canonical name"}, {Name: "Widget"},
	}}); err != nil {
		t.Fatal(err)
	}
	recordAliases(t, db, "Acme Corp", "ACME Ltd")
	acme, widget := EntityNodeID("Acme Corp"), EntityNodeID("Widget")
	resp, err := db.InsertGraphDocument(ctx, GraphRAGDocument{ID: "g1", Content: "ACME Ltd shipped the parts."}, GraphRAGIngestOptions{Extractor: aliasExtractor{}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(resp.EntityNodeIDs, acme) || slices.Contains(resp.EntityNodeIDs, EntityNodeID("ACME Ltd")) {
		t.Fatalf("wrote %v, want the canonical entity", resp.EntityNodeIDs)
	}
	requireNoNode(t, db, EntityNodeID("ACME Ltd"))
	requireCanonicalAcme(t, db, "d0")
	chunk := graphChunkNodeID("g1", 0)
	got := edgesOf(t, db, acme)
	for _, edge := range []string{chunk + " mentions " + acme, acme + " makes " + widget} {
		if !slices.Contains(got, edge) {
			t.Fatalf("no %q among %v", edge, got)
		}
	}
}

func TestIngestDocumentOfMediaNamingAnAliasMentionsTheCanonicalEntity(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	acme := EntityNodeID("Acme Corp")
	for _, text := range []string{"Yesterday ACME Ltd shipped the parts.", "Today ACME Ltd shipped the parts again."} {
		if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "m1", MediaType: "image/png", Bytes: []byte("png " + text), Caption: text}); err != nil {
			t.Fatal(err)
		}
		requireNoNode(t, db, EntityNodeID("ACME Ltd"))
		requireCanonicalAcme(t, db, "d0", "m1")
		want := graphChunkNodeID("m1", 0) + " mentions " + acme
		if !slices.Contains(edgesOf(t, db, acme), want) {
			t.Fatalf("no %q among %v", want, edgesOf(t, db, acme))
		}
	}
}

// A relation between an alias and the entity carrying it is a loop once
// followed, and is not written; a loop the writer states is.
func TestKnowledgeSaveDoesNotWriteARelationBetweenAnAliasAndItsEntity(t *testing.T) {
	db, _, ctx := aliasBrain(t)
	acme, widget := EntityNodeID("Acme Corp"), EntityNodeID("Widget")
	resp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "notes on suppliers", Relations: []ToolRelationInput{
		{From: "ACME Ltd", To: "Acme Corp", Type: "same_as"},
		{From: "Acme Corp", To: "acme ltd", Type: "same_as"},
		{From: "ACME Ltd", To: "Widget", Type: "makes"},
		{From: "Acme Corp", To: "Acme Corp", Type: "owns"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{relationEdgeID(acme, widget, "makes", "k1"), relationEdgeID(acme, acme, "owns", "k1")}
	slices.Sort(want)
	if !slices.Equal(resp.RelationEdgeIDs, want) {
		t.Fatalf("relation edges %v, want %v", resp.RelationEdgeIDs, want)
	}
	got := edgesOf(t, db, acme)
	if slices.Contains(got, acme+" same_as "+acme) || !slices.Contains(got, acme+" owns "+acme) {
		t.Fatalf("edges of the entity %v", got)
	}
}

// Under an ontology that resolves relation ends by the names of stored
// nodes, an end named by an alias resolves to the entity carrying it.
func TestKnowledgeSaveFollowsAnAliasedEndpointUnderAnOntology(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema(), Activate: true}); err != nil {
		t.Fatal(err)
	}
	written, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Zurich Airport", Type: "Airport", Metadata: map[string]string{"iataCode": "ZRH"}},
		{Name: "LX 318", Type: "Flight", Metadata: map[string]string{"flightNumber": "LX318"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	airport := written.EntityNodeIDs[0]
	node, err := db.Graph().GetNode(ctx, airport)
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = []string{"Kloten"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Content: "the morning departures",
		Relations: []ToolRelationInput{{From: "kloten", To: "LX 318", Type: "flightDeparture"}}}); err != nil {
		t.Fatalf("a relation from an alias under a validating ontology: %v", err)
	}
	edges, err := db.Graph().GetEdges(ctx, airport, "out")
	if err != nil || len(edges) != 1 || edges[0].ToNodeID != written.EntityNodeIDs[1] {
		t.Fatalf("the airport's edges are %v (%v), want the departure of LX 318", edges, err)
	}
}

// Deleting a document gives back what it added to the canonical entity under
// an alias (its mentions, relations and source) exactly as it does for a
// document that named the entity itself; the entity and what other documents
// gave it stay.
func TestDeletingADocumentGivesBackWhatItAddedUnderAnAlias(t *testing.T) {
	deletes := map[string]func(*DB, context.Context) error{
		"knowledge_delete": func(db *DB, ctx context.Context) error {
			_, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "k1"})
			return err
		},
		"delete_document_graph": func(db *DB, ctx context.Context) error {
			_, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "k1"})
			return err
		},
	}
	acme := EntityNodeID("Acme Corp")
	after := func(t *testing.T, name string, del func(*DB, context.Context) error) (string, []string) {
		db, tools, ctx := aliasBrain(t)
		// d0 holds an edge of the entity, so the delete's orphan check,
		// which looks at edges, leaves the entity whichever way k1 named it.
		if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{DocumentID: "d0", Relations: []ToolRelationInput{{From: "Acme Corp", To: "Widget", Type: "owns"}}}); err != nil {
			t.Fatal(err)
		}
		chunk := graphChunkNodeID("k1", 0)
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Collection: "gamma", Content: "Yesterday " + name + " shipped the parts.",
			Entities:  []ToolEntityInput{{Name: name, ChunkIDs: []string{chunk}}},
			Relations: []ToolRelationInput{{From: name, To: "Widget", Type: "makes"}}}); err != nil {
			t.Fatal(err)
		}
		// What k1 gave the entity, which the delete has to give back.
		saved, err := db.Graph().GetNode(ctx, acme)
		if err != nil {
			t.Fatal(err)
		}
		if docs := toStringSlice(saved.Properties["source_document_ids"]); !slices.Contains(docs, "k1") || !slices.Contains(toStringSlice(saved.Properties[entityCollectionsKey]), "gamma") {
			t.Fatalf("%s: the save gave the entity %v", name, saved.Properties)
		}
		for _, edge := range []string{chunk + " mentions " + acme, acme + " makes " + EntityNodeID("Widget")} {
			if !slices.Contains(edgesOf(t, db, acme), edge) {
				t.Fatalf("%s: no %q among %v", name, edge, edgesOf(t, db, acme))
			}
		}
		if err := del(db, ctx); err != nil {
			t.Fatal(err)
		}
		node, err := db.Graph().GetNode(ctx, acme)
		if err != nil {
			t.Fatalf("%s: the entity went with the document: %v", name, err)
		}
		if docs := toStringSlice(node.Properties["source_document_ids"]); !slices.Equal(docs, []string{"d0"}) {
			t.Fatalf("%s: the entity's documents are %v, want d0 alone", name, docs)
		}
		if node.Properties["description"] != "the canonical name" || !slices.Contains(toStringSlice(node.Properties["aliases"]), "ACME Ltd") {
			t.Fatalf("%s: the entity now reads %v", name, node.Properties)
		}
		requireNoNode(t, db, EntityNodeID("ACME Ltd"))
		return fmt.Sprint(toStringSlice(node.Properties[entityCollectionsKey])), edgesOf(t, db, acme)
	}
	for how, del := range deletes {
		t.Run(how, func(t *testing.T) {
			directCollections, directEdges := after(t, "Acme Corp", del)
			aliasCollections, aliasEdges := after(t, "ACME Ltd", del)
			if want := []string{acme + " owns " + EntityNodeID("Widget")}; !slices.Equal(aliasEdges, want) || !slices.Equal(directEdges, want) {
				t.Fatalf("edges left: under the alias %v, under the name %v; want d0's alone", aliasEdges, directEdges)
			}
			if aliasCollections != directCollections {
				t.Fatalf("collections left: under the alias %s, under the name %s", aliasCollections, directCollections)
			}
		})
	}
}
