// pkg/importflow/source_dump_test.go
package importflow

import (
	"context"
	"strings"
	"testing"
)

func TestDumpSourceInsert(t *testing.T) {
	dump := "" +
		"INSERT INTO `people` (`id`, `name`, `bio`) VALUES (1,'Ada','math, pioneer'),(2,'Alan',NULL);\n"
	src, err := NewSQLDumpSource(strings.NewReader(dump), DumpOptions{})
	if err != nil {
		t.Fatalf("NewSQLDumpSource: %v", err)
	}
	defer src.Close()

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
	if rows[0].Table != "people" {
		t.Fatalf("table = %q; want people", rows[0].Table)
	}
	if v, _ := rows[0].Get("bio"); v != "math, pioneer" {
		t.Fatalf("bio = %q; want 'math, pioneer' (comma inside quotes must not split)", v)
	}
	if _, ok := rows[1].Get("bio"); ok {
		t.Fatalf("rows[1].bio should be NULL")
	}
}

func TestDumpSourceQuoteEscapes(t *testing.T) {
	dump := "INSERT INTO t (a) VALUES ('O''Brien'),('line1\\nline2');\n"
	src, _ := NewSQLDumpSource(strings.NewReader(dump), DumpOptions{})
	defer src.Close()
	var rows []Record
	_ = src.Records(context.Background(), func(r Record) error { rows = append(rows, r); return nil })
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(rows))
	}
	if v, _ := rows[0].Get("a"); v != "O'Brien" {
		t.Fatalf("a0 = %q; want O'Brien", v)
	}
	if v, _ := rows[1].Get("a"); v != "line1\nline2" {
		t.Fatalf("a1 = %q; want line1<newline>line2", v)
	}
}
