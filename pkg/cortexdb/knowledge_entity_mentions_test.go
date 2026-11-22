package cortexdb

import (
	"context"
	"strings"
	"testing"
)

// An entity given with a document is linked to the chunks whose text names
// it, so search_chunks_by_entities reaches the text from it; one no chunk
// names is linked to all of them (#247).
func TestGivenEntitiesAreLinkedToTheirChunks(t *testing.T) {
	db, tools, c := openIngestBrain(t, "given-entity-mentions")
	text := "Ada Lovelace wrote to Charles Babbage about the engine. " + strings.Repeat("filler words here. ", 40) +
		"The Analytical Engine might compose elaborate pieces of music."
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{
		KnowledgeID: "lovelace", Content: text, ChunkSize: 60,
		Entities: []ToolEntityInput{{Name: "Ada Lovelace"}, {Name: "Analytical Engine"}, {Name: "Countess of Lovelace"}},
	}); err != nil {
		t.Fatal(err)
	}
	chunksOf := func(name string) []string {
		resp, err := tools.SearchChunksByEntities(context.Background(), ToolSearchChunksByEntitiesRequest{EntityNames: []string{name}, TopK: 20, MaxHops: 1})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, ch := range resp.Chunks {
			out = append(out, ch.Content)
		}
		return out
	}
	ada := chunksOf("Ada Lovelace")
	if len(ada) == 0 || !strings.Contains(ada[0], "Ada Lovelace wrote") {
		t.Fatalf("Ada Lovelace reaches %q", ada)
	}
	for _, ch := range ada {
		if !strings.Contains(ch, "Ada Lovelace") {
			t.Fatalf("Ada Lovelace is linked to a chunk that does not name her: %q", ch)
		}
	}
	engine := chunksOf("Analytical Engine")
	if len(engine) == 0 || !strings.Contains(strings.Join(engine, " "), "elaborate pieces of music") {
		t.Fatalf("Analytical Engine reaches %q", engine)
	}
	if countess := chunksOf("Countess of Lovelace"); len(countess) < 2 {
		t.Fatalf("an entity no chunk names reaches %d chunks, want all of them", len(countess))
	}
}
