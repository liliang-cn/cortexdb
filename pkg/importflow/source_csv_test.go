// pkg/importflow/source_csv_test.go
package importflow

import (
	"context"
	"strings"
	"testing"
)

func TestCSVSourceSchemasAndRecords(t *testing.T) {
	csv := "id,name,bio\n1,Ada,math pioneer\n2,Alan,codebreaker\n"
	src, err := NewCSVSource(strings.NewReader(csv), CSVOptions{Table: "people"})
	if err != nil {
		t.Fatalf("NewCSVSource: %v", err)
	}
	defer src.Close()

	schemas, err := src.Schemas(context.Background())
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	if len(schemas) != 1 || schemas[0].Table != "people" {
		t.Fatalf("schemas = %+v; want one table 'people'", schemas)
	}
	if len(schemas[0].Columns) != 3 || schemas[0].Columns[0].Name != "id" {
		t.Fatalf("columns = %+v", schemas[0].Columns)
	}
	if schemas[0].Columns[0].Type != "integer" {
		t.Fatalf("id type = %q; want integer", schemas[0].Columns[0].Type)
	}

	var rows []Record
	if err := src.Records(context.Background(), func(r Record) error {
		rows = append(rows, r)
		return nil
	}); err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(rows))
	}
	if v, _ := rows[0].Get("name"); v != "Ada" {
		t.Fatalf("rows[0].name = %q; want Ada", v)
	}
}
