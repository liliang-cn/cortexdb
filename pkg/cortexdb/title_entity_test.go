package cortexdb

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestOnlyATitleThatReadsAsANameBecomesAnEntity(t *testing.T) {
	for title, want := range map[string]bool{
		"Lothair II":          true,
		"God's Gift to Women": true, // one film, kept whole
		"  Raft   Consensus ": true,
		"微信机器人":               true, // no capitals in the script
		"lowercase notes":     false,
		"":                    false,
		"A very long title that describes in a full sentence what this document is about": false,
	} {
		entity, ok := documentTitleEntity(title)
		if ok != want {
			t.Errorf("%q: entity=%v, want %v", title, ok, want)
		}
		if ok && entity.Name != "" && entity.Name[0] == ' ' {
			t.Errorf("%q: name %q is not trimmed", title, entity.Name)
		}
	}
	if e, _ := documentTitleEntity("God's Gift to Women"); e.Name != "God's Gift to Women" {
		t.Errorf("name = %q, want the whole title", e.Name)
	}
}

// mentionedChunks returns the chunk nodes with a mentions edge to the entity.
func mentionedChunks(t *testing.T, db *DB, name string) []string {
	t.Helper()
	edges, err := db.Graph().GetEdges(context.Background(), graphEntityNodeID(name), "in")
	if err != nil {
		t.Fatalf("edges of %s: %v", name, err)
	}
	var chunks []string
	for _, e := range edges {
		if e.EdgeType == "mentions" {
			chunks = append(chunks, e.FromNodeID)
		}
	}
	sort.Strings(chunks)
	return chunks
}

// The title names what every chunk of a document is about, so every chunk
// mentions it — even the chunks whose text never repeats the name.
func TestEveryChunkOfADocumentMentionsTheEntityItsTitleNames(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
				KnowledgeID: "lothair",
				Title:       "Lothair II",
				Content:     "He was the son of the emperor. His marriage was contested for years. He died in Piacenza in 869.",
				ChunkSize:   6, // words: several chunks, most never naming him
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			docEdges, err := db.Graph().GetEdges(ctx, graphDocumentNodeID("lothair"), "out")
			if err != nil {
				t.Fatalf("document edges: %v", err)
			}
			var want []string
			for _, e := range docEdges {
				if e.EdgeType == "has_chunk" {
					want = append(want, e.ToNodeID)
				}
			}
			sort.Strings(want)
			got := mentionedChunks(t, db, "Lothair II")
			if len(want) < 2 || strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("chunks mentioning the title entity = %v; want every chunk of the document %v", got, want)
			}

		})
	}
}

// A name the caller declared stays the caller's: the title must not create a
// second, untyped node for it.
func TestATitleTheCallerAlsoDeclaredKeepsTheDeclaredEntity(t *testing.T) {
	db := lexicalBackends(t)["sqlite"]
	ctx := context.Background()
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "apollo",
		Title:       "Apollo",
		Content:     "Alice owns the project.",
		Entities:    []ToolEntityInput{{Name: "Apollo", Type: "project"}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	node, err := db.Graph().GetNode(ctx, graphEntityNodeID("Apollo"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.NodeType != "project" {
		t.Fatalf("node type = %q, want the declared type project", node.NodeType)
	}
}

// The same holds where an embedder is set, on both ingest paths that take a
// title: SaveKnowledge and InsertGraphDocument.
func TestWithAnEmbedderBothIngestPathsLinkTheTitle(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "titles.db")), WithEmbedder(newKeywordEmbedder("city", "italy", "emperor", "son")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "lothair", Title: "Lothair II", Content: "He was the son of the emperor.",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := mentionedChunks(t, db, "Lothair II"); len(got) == 0 {
		t.Fatal("SaveKnowledge with an embedder did not link its document's title")
	}

	if _, err := db.InsertGraphDocument(ctx, GraphRAGDocument{
		ID: "piacenza", Title: "Piacenza", Content: "A city in northern Italy on the Po.",
	}, GraphRAGIngestOptions{}); err != nil {
		t.Fatalf("insert graph document: %v", err)
	}
	if got := mentionedChunks(t, db, "Piacenza"); len(got) == 0 {
		t.Fatal("InsertGraphDocument did not link its document's title")
	}
}

// The extractor reads a declared "Bridge 01" as the bare word "Bridge". That
// fragment must not become an entity of its own: it would join every chunk
// naming any bridge into one hub.
func TestAFragmentOfADeclaredNameIsNotASecondEntity(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			for _, id := range []string{"01", "02"} {
				doc := "first-" + id
				if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: doc, Content: "Quorvane Hall hosted Bridge " + id + ".",
					Entities: []ToolEntityInput{{Name: "Bridge " + id, ChunkIDs: []string{graphChunkNodeID(doc, 0)}}}}); err != nil {
					t.Fatalf("save %s: %v", doc, err)
				}
			}
			if got := mentionedChunks(t, db, "Bridge"); len(got) != 0 {
				t.Fatalf("the fragment \"Bridge\" became an entity mentioned by %v", got)
			}
			if got := mentionedChunks(t, db, "Quorvane Hall"); len(got) != 2 {
				t.Fatalf("an undeclared name stopped being extracted: Quorvane Hall mentioned by %v", got)
			}
		})
	}
}
