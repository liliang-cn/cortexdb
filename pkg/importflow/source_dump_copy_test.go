// pkg/importflow/source_dump_copy_test.go
package importflow

import (
	"context"
	"strings"
	"testing"
)

func TestDumpSourceCopyAndUnparsed(t *testing.T) {
	dump := "" +
		"CREATE TABLE people (id integer, name text, bio text);\n" +
		"COPY people (id, name, bio) FROM stdin;\n" +
		"1\tAda\tmath pioneer\n" +
		"2\tAlan\t\\N\n" +
		"\\.\n" +
		"SET client_encoding = 'UTF8';\n"
	src, err := NewSQLDumpSource(strings.NewReader(dump), DumpOptions{Dialect: DialectPostgres})
	if err != nil {
		t.Fatalf("NewSQLDumpSource: %v", err)
	}
	defer src.Close()

	schemas, _ := src.Schemas(context.Background())
	if len(schemas) != 1 || len(schemas[0].Columns) != 3 {
		t.Fatalf("schemas = %+v; want 1 table, 3 columns", schemas)
	}

	var rows []Record
	_ = src.Records(context.Background(), func(r Record) error { rows = append(rows, r); return nil })
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(rows))
	}
	if v, _ := rows[0].Get("bio"); v != "math pioneer" {
		t.Fatalf("bio0 = %q", v)
	}
	if _, ok := rows[1].Get("bio"); ok {
		t.Fatalf("rows[1].bio should be NULL (\\N)")
	}
	if len(src.Unparsed()) != 1 || !strings.Contains(src.Unparsed()[0], "SET client_encoding") {
		t.Fatalf("unparsed = %+v; want the SET statement", src.Unparsed())
	}
}
