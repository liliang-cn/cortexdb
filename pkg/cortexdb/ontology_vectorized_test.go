package cortexdb

import (
	"context"
	"path/filepath"
	"testing"
)

// A property declared Vectorized has to be, or the flag is a lie.
//
// The flag was stored, validated and inherited, and nothing ever embedded the
// property: every entity node carried a lexical hash vector regardless, so the
// nearest_neighbors predicate — implemented on the read side — compared a real
// embedding of the query against hash vectors and matched nothing. Now, under
// a schema that declares a Vectorized property and with an embedder present,
// an object's node vector is the embedding of its vectorized text, and the
// predicate answers by meaning.
func TestAVectorizedPropertyIsEmbeddedAndSearchable(t *testing.T) {
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "vec.db"))
	cfg.Dimensions = 4
	db, err := Open(cfg, WithEmbedder(newKeywordEmbedder("backup", "copies", "metadata", "controller")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	str := OntologyDataType{Kind: OntologyDataString}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Activate: true, Schema: OntologySchema{
		SchemaID: "sds", Name: "sds",
		ObjectTypes: []OntologyObjectType{{
			APIName: "Resource", PrimaryKey: "resourceName", TitleProperty: "resourceName",
			Properties: []OntologyProperty{
				{APIName: "resourceName", DataType: str, Required: true},
				{APIName: "purpose", DataType: str, Vectorized: true},
			},
		}},
	}}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{
			{Name: "backup-vault", Type: "Resource", Metadata: map[string]string{
				"resourceName": "backup-vault", "purpose": "nightly backup copies we can throw away"}},
			{Name: "sds-meta", Type: "Resource", Metadata: map[string]string{
				"resourceName": "sds-meta", "purpose": "cluster metadata and the controller database"}},
		},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	nearest := func(text string) []string {
		t.Helper()
		resolved, err := db.ResolveObjectSetObjects(ctx, ObjectSetResolveRequest{
			ObjectSet: ObjectSet{
				Kind:   ObjectSetFilter,
				Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Resource"},
				Where: &ObjectSetPredicate{
					Op: PredicateNearestNeighbors, Property: "purpose", Value: text, K: 1,
				},
			},
		})
		if err != nil {
			t.Fatalf("nearest_neighbors %q: %v", text, err)
		}
		out := make([]string, 0, len(resolved.Objects))
		for _, o := range resolved.Objects {
			out = append(out, o.Title)
		}
		return out
	}

	if got := nearest("throwaway backup copies"); len(got) != 1 || got[0] != "backup-vault" {
		t.Fatalf("nearest to a backup question = %v, want [backup-vault]", got)
	}
	if got := nearest("the controller's metadata"); len(got) != 1 || got[0] != "sds-meta" {
		t.Fatalf("nearest to a metadata question = %v, want [sds-meta]", got)
	}
}

// Without an embedder there is nothing to embed with, and the flag must not
// break the write: the object is stored, with the lexical vector it always had.
func TestAVectorizedPropertyWithoutAnEmbedderStillWrites(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "novec.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	str := OntologyDataType{Kind: OntologyDataString}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Activate: true, Schema: OntologySchema{
		SchemaID: "sds", Name: "sds",
		ObjectTypes: []OntologyObjectType{{
			APIName: "Resource", PrimaryKey: "resourceName",
			Properties: []OntologyProperty{
				{APIName: "resourceName", DataType: str, Required: true},
				{APIName: "purpose", DataType: str, Vectorized: true},
			},
		}},
	}}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{Name: "x", Type: "Resource",
			Metadata: map[string]string{"resourceName": "x", "purpose": "anything"}}},
	}); err != nil {
		t.Fatalf("a vectorized property with no embedder broke the write: %v", err)
	}
}
