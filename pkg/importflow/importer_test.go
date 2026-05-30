// pkg/importflow/importer_test.go
package importflow

import (
	"context"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestImporterRunRAGAndKG(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	im := New(db)

	csv := "id,name,bio\n1,Ada,first programmer\n2,Alan,enigma codebreaker\n"
	src, err := NewCSVSource(strings.NewReader(csv), CSVOptions{Table: "people"})
	if err != nil {
		t.Fatalf("NewCSVSource: %v", err)
	}

	plan := MappingPlan{Tables: map[string]TablePlan{
		"people": {
			RAG: &RAGPlan{ContentTmpl: "{name}: {bio}", IDColumn: "id"},
			KG: &KGPlan{
				Entities: []EntityMap{{Ref: "p", Type: "Person", IDTmpl: "{id}", LabelTmpl: "{name}"}},
			},
		},
	}}

	rep, err := im.Run(ctx, src, plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.RowsRead != 2 {
		t.Fatalf("RowsRead = %d; want 2", rep.RowsRead)
	}
	if rep.ChunksIndexed != 2 {
		t.Fatalf("ChunksIndexed = %d; want 2", rep.ChunksIndexed)
	}
	if rep.TriplesCreated == 0 {
		t.Fatalf("TriplesCreated = 0; want > 0 (type + label per person)")
	}

	res, err := db.SearchTextOnly(ctx, "codebreaker", cortexdb.TextSearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected FTS5 hit for 'codebreaker'")
	}
}

func TestImporterAutoImportUsesInferer(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	payload := `{"tables":{"people":{"rag":{"content_tmpl":"{name}: {bio}","id_column":"id"}}}}`
	im := New(db, WithMappingInferer(LLMInferer{Client: fakeJSONGen{payload: payload}}))

	csv := "id,name,bio\n1,Ada,first programmer\n"
	src, _ := NewCSVSource(strings.NewReader(csv), CSVOptions{Table: "people"})

	rep, err := im.AutoImport(ctx, src, Goal{BuildRAG: true})
	if err != nil {
		t.Fatalf("AutoImport: %v", err)
	}
	if rep.ChunksIndexed != 1 {
		t.Fatalf("ChunksIndexed = %d; want 1", rep.ChunksIndexed)
	}
}
