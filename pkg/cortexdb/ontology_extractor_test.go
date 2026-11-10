package cortexdb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// A schema of any size gives a bounded list: at most ExtractorTypeLimit
// types, each description cut to ExtractorDescriptionChars, at most
// ExtractorAliasLimit aliases named — and every declared type, listed or not,
// still canonicalises.
func TestExtractorVocabularyIsBounded(t *testing.T) {
	var schema OntologySchema
	schema.SchemaID = "big"
	for i := 0; i < ExtractorTypeLimit+15; i++ {
		schema.ObjectTypes = append(schema.ObjectTypes, OntologyObjectType{
			APIName:     fmt.Sprintf("Type%d", i),
			Description: strings.Repeat("long words ", 100),
			Aliases:     []string{"a1x" + fmt.Sprint(i), "a2x" + fmt.Sprint(i), "a3x" + fmt.Sprint(i), "a4x" + fmt.Sprint(i), "a5x" + fmt.Sprint(i), "a6x" + fmt.Sprint(i)},
		})
	}
	vocab := ExtractorVocabularyFromSchemas([]OntologySchema{schema})
	if len(vocab.Types) != ExtractorTypeLimit || vocab.Omitted != 15 {
		t.Fatalf("types %d omitted %d", len(vocab.Types), vocab.Omitted)
	}
	for _, typ := range vocab.Types {
		if n := utf8.RuneCountInString(typ.Description); n > ExtractorDescriptionChars {
			t.Fatalf("%s description is %d runes", typ.APIName, n)
		}
		if len(typ.Aliases) > ExtractorAliasLimit {
			t.Fatalf("%s names %d aliases", typ.APIName, len(typ.Aliases))
		}
	}
	list := vocab.PromptList()
	if !strings.Contains(list, "(15 more declared types are not listed.)") || strings.Contains(list, "a6x0") {
		t.Fatalf("list:\n%s", list)
	}
	last := fmt.Sprintf("Type%d", ExtractorTypeLimit+14)
	if got, ok := vocab.Canonical(strings.ToLower(last)); !ok || got != last {
		t.Fatalf("an omitted type does not canonicalise: %q %v", got, ok)
	}
	if got, ok := vocab.Canonical("A6X3"); !ok || got != "Type3" {
		t.Fatalf("an unlisted alias does not canonicalise: %q %v", got, ok)
	}
	if _, ok := vocab.Canonical(""); ok {
		t.Fatal("the empty type canonicalised")
	}
}

// No active schema gives the empty vocabulary; the active one gives its types.
func TestActiveExtractorVocabulary(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	vocab, err := db.ActiveExtractorVocabulary(ctx)
	if err != nil || !vocab.Empty() || vocab.PromptList() != "" {
		t.Fatalf("no schema: %+v %v", vocab, err)
	}
	activateVocabularyAviationSchema(t, db)
	vocab, err = db.ActiveExtractorVocabulary(ctx)
	if err != nil || vocab.SchemaID != "aviation" || len(vocab.Types) != 2 {
		t.Fatalf("active schema: %+v %v", vocab, err)
	}
	if got, ok := vocab.Canonical("airport"); !ok || got != "Airport" {
		t.Fatalf("Canonical(airport) = %q %v", got, ok)
	}
}
