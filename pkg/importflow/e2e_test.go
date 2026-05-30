package importflow

import (
	"context"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestEndToEndDumpToRAGAndKG(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	dump := "" +
		"CREATE TABLE customers (id integer, name text, bio text);\n" +
		"INSERT INTO customers (id, name, bio) VALUES (1,'Ada','wrote the first algorithm'),(2,'Alan','broke the Enigma');\n" +
		"INSERT INTO orders (customer_id, product_id) VALUES (1,'p9'),(2,'p9');\n"
	src, err := NewSQLDumpSource(strings.NewReader(dump), DumpOptions{})
	if err != nil {
		t.Fatalf("NewSQLDumpSource: %v", err)
	}
	defer src.Close()

	plan := MappingPlan{Tables: map[string]TablePlan{
		"customers": {
			RAG: &RAGPlan{ContentTmpl: "{name}: {bio}", IDColumn: "id"},
			KG:  &KGPlan{Entities: []EntityMap{{Ref: "c", Type: "Customer", IDTmpl: "{id}", LabelTmpl: "{name}"}}},
		},
		"orders": {
			KG: &KGPlan{
				Entities: []EntityMap{
					{Ref: "c", Type: "Customer", IDTmpl: "{customer_id}"},
					{Ref: "p", Type: "Product", IDTmpl: "{product_id}"},
				},
				Relations: []RelationMap{{Subject: "c", Predicate: "purchased", Object: "p"}},
			},
		},
	}}

	rep, err := New(db).Run(ctx, src, plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.RowsRead != 4 {
		t.Fatalf("RowsRead = %d; want 4", rep.RowsRead)
	}
	if rep.ChunksIndexed != 2 {
		t.Fatalf("ChunksIndexed = %d; want 2", rep.ChunksIndexed)
	}
	if rep.TriplesCreated == 0 {
		t.Fatalf("TriplesCreated = 0; want > 0")
	}

	res, err := db.SearchTextOnly(ctx, "Enigma", cortexdb.TextSearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected FTS5 hit for 'Enigma'")
	}
}
