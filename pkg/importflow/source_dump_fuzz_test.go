package importflow

import (
	"context"
	"strings"
	"testing"
)

// FuzzSQLDumpSource throws arbitrary text at the SQL-dump parser and record
// iterator. A malformed dump must fail gracefully (error) or parse partially,
// never panic — dumps are untrusted external input.
func FuzzSQLDumpSource(f *testing.F) {
	for _, seed := range []string{
		"",
		"CREATE TABLE t (id INT, name TEXT);",
		"CREATE TABLE t (id INT);\nINSERT INTO t VALUES (1),(2);",
		"CREATE TABLE `u` (`id` int NOT NULL, `bio` text);",
		"COPY t (id, name) FROM stdin;\n1\tAlice\n\\.",
		"INSERT INTO t VALUES ('a''b', NULL, 3.14);",
		"CREATE TABLE (", "INSERT INTO", "COPY", ");;;--",
		"CREATE TABLE t (id INT DEFAULT '\\'); INSERT INTO t VALUES ('\\\\');",
	} {
		f.Add(seed, uint8(0))
	}

	f.Fuzz(func(t *testing.T, dump string, dialectSel uint8) {
		dialect := []Dialect{DialectAuto, DialectMySQL, DialectPostgres}[int(dialectSel)%3]
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("dump parse panicked (dialect=%s): %v\ninput=%q", dialect, r, dump)
			}
		}()
		src, err := NewSQLDumpSource(strings.NewReader(dump), DumpOptions{Dialect: dialect})
		if err != nil {
			return // graceful failure is fine
		}
		defer func() { _ = src.Close() }()
		ctx := context.Background()
		if _, err := src.Schemas(ctx); err != nil {
			return
		}
		// Iterating records must also not panic.
		_ = src.Records(ctx, func(Record) error { return nil })
	})
}
