package cortexdb

import (
	"context"
	"path/filepath"
	"testing"
)

// A retrieved chunk must name the objects it mentions, whatever their type.
//
// The chunk-to-entity lookup used to match node_type = 'entity', which is the
// type an UNTYPED entity gets. Under an ontology an entity's node type is its
// object type — Airport, Resource — so the lookup found nothing and every
// search hit came back with an empty Entities list, for exactly the users who
// had adopted the ontology. The relation that matters is the mention edge,
// not the type of the node at the far end of it.
func TestSearchHitsNameTheTypedObjectsTheirChunksMention(t *testing.T) {
	for _, mode := range []struct {
		name string
		open func(t *testing.T) *DB
	}{
		{"vector", func(t *testing.T) *DB {
			t.Helper()
			cfg := DefaultConfig(filepath.Join(t.TempDir(), "enrich.db"))
			cfg.Dimensions = 4
			db, err := Open(cfg, WithEmbedder(newKeywordEmbedder("heathrow", "ground", "handling", "runway")))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		}},
		{"lexical", func(t *testing.T) *DB {
			t.Helper()
			db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "enrich.db")))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			db := mode.open(t)
			activateAviationSchema(t, db)
			ctx := context.Background()
			tools := db.GraphRAGTools()

			saved, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
				KnowledgeID: "note-1",
				Title:       "Ground handling",
				Content:     "Ground handling at Heathrow is contracted out to a runway operator.",
				Collection:  "notes",
			})
			if err != nil {
				t.Fatalf("save: %v", err)
			}
			// The join: the typed object, linked to the chunks that mention it.
			if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
				DocumentID: "note-1",
				Entities: []ToolEntityInput{{
					Name: "Heathrow", Type: "Airport",
					Metadata: map[string]string{"iataCode": "LHR"},
					ChunkIDs: saved.Knowledge.ChunkIDs,
				}},
			}); err != nil {
				t.Fatalf("link: %v", err)
			}

			hits, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
				Query: "ground handling at heathrow", Collection: "notes", TopK: 3, MaxHops: 1,
			})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(hits.Results) == 0 {
				t.Fatal("the note was not retrieved at all")
			}
			if !hasString(hits.Results[0].Entities, "Heathrow") {
				t.Fatalf("the hit does not name the typed object its chunk mentions: hit.Entities=%v resp.Entities=%v",
					hits.Results[0].Entities, hits.Entities)
			}
			if !hasString(hits.Entities, "Heathrow") {
				t.Fatalf("the response-level entity list is missing it too: %v", hits.Entities)
			}
		})
	}
}

func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
