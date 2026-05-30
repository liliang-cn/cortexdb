# importflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `pkg/importflow`, an opt-in workflow layer that imports external structured data (CSV, MySQL/PG SQL dumps) into CortexDB, building both RAG (vector/FTS5) and knowledge-graph (RDF triples) foundations, with optional AI assistance for mapping inference, triple extraction, and text refinement.

**Architecture:** `Source` (CSV / SQL-dump parser, plugin interface) yields a uniform `Record` stream. A `MappingPlan` declares, per table, how columns route to RAG content and KG entities/relations. A `Mapper` applies the plan deterministically; AI (`MappingInferer`, `TextRefiner`, and graphflow's `LLMExtractor`) is injected via interfaces and is always optional. Two sinks (`RAGSink`, `KGSink`) write through the `pkg/cortexdb` facade. `Importer` orchestrates `Plan`/`Run`/`AutoImport`. No LLM SDK enters `pkg/`.

**Tech Stack:** Go 1.25, `pkg/cortexdb` facade (`InsertTextBatch`, `SaveKnowledge`, `UpsertKnowledgeGraph`), `pkg/graph` (`RDFTriple`, `NewIRI`, `NewLiteral`), `pkg/graphflow` (`JSONGenerator`, `LLMExtractor`, `SourceDocument`, `ExtractionResult`), stdlib `encoding/csv`, `encoding/json`.

**Verified API references (do not guess — these are real):**
- `cortexdb.Embedder` — `pkg/cortexdb/embedder.go:11`: `Embed(ctx, string) ([]float32, error)`, `EmbedBatch`, `Dim() int`.
- `func (db *DB) InsertTextBatch(ctx, texts map[string]string, metadata map[string]string) error` — `pkg/cortexdb/text_search.go:45`.
- `func (db *DB) SaveKnowledge(ctx, KnowledgeSaveRequest) (*KnowledgeSaveResponse, error)` — `pkg/cortexdb/knowledge_api.go:20`. Request fields: `KnowledgeID, Title, Content, Collection, Metadata map[string]string, ...` (`knowledge_memory_types.go:33`).
- `func (db *DB) UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest) (*KnowledgeGraphUpsertResponse, error)` — `pkg/cortexdb/knowledge_graph_api.go:34`. `KnowledgeGraphUpsertRequest{ Triples []KnowledgeGraphTriple }`; `KnowledgeGraphTriple = graph.RDFTriple`; response `{ TripleIDs []string; Count int }`.
- `graph.RDFTriple{ Subject, Predicate, Object graph.RDFTerm }`; helpers `graph.NewIRI(string) RDFTerm`, `graph.NewLiteral(string) RDFTerm` — `pkg/graph/rdf.go:51,91,109`.
- `graphflow.JSONGenerator` interface: `GenerateJSON(ctx, system, user string) ([]byte, error)` — `pkg/graphflow/llm.go:10`.
- `graphflow.LLMExtractor{ Client JSONGenerator; MaxChars int }`, method `Extract(ctx, SourceDocument) (*ExtractionResult, error)` — `pkg/graphflow/llm.go:15,26`.
- `graphflow.SourceDocument{ ID, Content, Title string; Metadata map[string]string; ... }` — `pkg/graphflow/types.go:20`.
- `graphflow.ExtractionResult{ Nodes []ExtractionNode; Edges []ExtractionEdge }`; `ExtractionNode{ ID, Label, Type string }`; `ExtractionEdge{ Source, Target, Relation string }` — `pkg/graphflow/types.go:30-60`.
- Toolbox pattern — `pkg/graphflow/toolbox.go`: `Definitions() []cortexdb.ToolDefinition`, `Call(ctx, name string, input json.RawMessage) (any, error)`.
- Functional option pattern — `pkg/cortexdb/cortexdb.go:42`: `type Option func(*DB)`.
- Test DB: open an in-memory/temp cortexdb via `cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "x.db")))`.

**Module path:** all imports are `github.com/liliang-cn/cortexdb/v2/pkg/...`.

**Conventions:** standard `_test.go` next to source; `go test ./...` and `go build ./...` are the gates; no lint step. Commit after each green task.

---

## File Structure

```
pkg/importflow/
  doc.go            package doc
  types.go          Record, Column, Schema, Goal, Report
  source.go         Source interface
  source_csv.go     CSVSource
  source_dump.go    SQLDumpSource (INSERT + PG COPY + CREATE TABLE schema)
  plan.go           MappingPlan, TablePlan, RAGPlan, KGPlan, EntityMap, RelationMap, TextExtract, renderTemplate
  mapper.go         Mapper: TablePlan + Record -> ragChunk + []RDFTriple
  sink_rag.go       RAGSink
  sink_kg.go        KGSink
  infer.go          MappingInferer interface + LLMInferer (default, JSONGenerator-based)
  refine.go         TextRefiner interface + LLMRefiner
  importer.go       Importer: New, Options, Plan, Run, AutoImport
  toolbox.go        Toolbox: NewToolbox, Definitions, Call (importflow_plan / importflow_run)
  *_test.go         tests
examples/07_importflow/main.go   runnable example with a real JSONGenerator
```

Docs to update at the end: `README.md`, `README_CN.md`, `SKILL.md` (workflow-layer sections).

---

## Task 1: Package skeleton + core types

**Files:**
- Create: `pkg/importflow/doc.go`
- Create: `pkg/importflow/types.go`
- Test: `pkg/importflow/types_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/importflow/types_test.go
package importflow

import "testing"

func TestRecordGet(t *testing.T) {
	r := Record{
		Table:  "t",
		Values: map[string]string{"a": "1", "b": ""},
		Nulls:  map[string]bool{"b": true},
	}
	if v, ok := r.Get("a"); !ok || v != "1" {
		t.Fatalf("Get(a) = %q,%v; want 1,true", v, ok)
	}
	if _, ok := r.Get("b"); ok {
		t.Fatalf("Get(b) ok = true; want false (NULL)")
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatalf("Get(missing) ok = true; want false")
	}
}

func TestReportAddError(t *testing.T) {
	var rep Report
	rep.addError(nil)
	rep.addError(errFor("boom"))
	if len(rep.Errors) != 1 {
		t.Fatalf("len(Errors) = %d; want 1", len(rep.Errors))
	}
}

func errFor(s string) error { return &simpleErr{s} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run 'TestRecordGet|TestReportAddError' -v`
Expected: build failure — `undefined: Record`, `undefined: Report`.

- [ ] **Step 3: Write doc.go and types.go**

```go
// pkg/importflow/doc.go
// Package importflow imports external structured data (CSV, MySQL/PG SQL dumps)
// into CortexDB, building RAG (vector/FTS5) and knowledge-graph (RDF triple)
// foundations in a single pass. AI assistance (mapping inference, triple
// extraction, text refinement) is injected via interfaces and always optional;
// this package imports no LLM SDK.
package importflow
```

```go
// pkg/importflow/types.go
package importflow

// Column is one source column with a best-effort type label.
type Column struct {
	Name string
	Type string // "integer","number","text","timestamp","" (unknown)
}

// Record is one normalized source row.
type Record struct {
	Table  string
	Values map[string]string // column name -> string value
	Nulls  map[string]bool   // column name -> is NULL
	Row    int               // 0-based row index within the table
}

// Get returns the value for col and false when the column is missing or NULL.
func (r Record) Get(col string) (string, bool) {
	if r.Nulls[col] {
		return "", false
	}
	v, ok := r.Values[col]
	return v, ok
}

// Schema describes a table plus sample rows, used for AI mapping inference.
type Schema struct {
	Table   string
	Columns []Column
	Sample  []Record
}

// Goal declares what the import should build.
type Goal struct {
	BuildRAG bool
	BuildKG  bool
	Hint     string // domain hint passed to the AI inferer
}

// Report summarizes an import run. Per repo "no silent caps" rule, dropped /
// unparsed input is surfaced here rather than discarded silently.
type Report struct {
	RowsRead           int
	ChunksIndexed      int
	TriplesCreated     int
	Skipped            int
	UnparsedStatements []string
	Errors             []error
}

func (rep *Report) addError(err error) {
	if err != nil {
		rep.Errors = append(rep.Errors, err)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/importflow/ -run 'TestRecordGet|TestReportAddError' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/doc.go pkg/importflow/types.go pkg/importflow/types_test.go
git commit -m "feat(importflow): add package skeleton and core types"
```

---

## Task 2: Source interface + CSVSource

**Files:**
- Create: `pkg/importflow/source.go`
- Create: `pkg/importflow/source_csv.go`
- Test: `pkg/importflow/source_csv_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run TestCSVSource -v`
Expected: build failure — `undefined: NewCSVSource`.

- [ ] **Step 3: Write source.go and source_csv.go**

```go
// pkg/importflow/source.go
package importflow

import "context"

// Source yields table schemas (for planning) and a stream of records (for import).
// Implementations should make Schemas() cheap enough to call before Records().
type Source interface {
	// Schemas returns table schemas with up to a few sample rows each.
	Schemas(ctx context.Context) ([]Schema, error)
	// Records streams every record; fn returning an error aborts iteration.
	Records(ctx context.Context, fn func(Record) error) error
	// Close releases any underlying resources.
	Close() error
}
```

```go
// pkg/importflow/source_csv.go
package importflow

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CSVOptions configures a CSVSource.
type CSVOptions struct {
	Table      string // logical table name; default "csv"
	Delimiter  rune   // default ','
	SampleSize int    // sample rows in Schemas(); default 5
}

// CSVSource is an in-memory Source over a single CSV stream (header required).
type CSVSource struct {
	table      string
	columns    []Column
	records    []Record
	sampleSize int
}

// NewCSVSource eagerly reads the whole CSV into memory and infers column types.
func NewCSVSource(r io.Reader, opts CSVOptions) (*CSVSource, error) {
	if opts.Delimiter == 0 {
		opts.Delimiter = ','
	}
	if opts.SampleSize == 0 {
		opts.SampleSize = 5
	}
	if opts.Table == "" {
		opts.Table = "csv"
	}
	cr := csv.NewReader(r)
	cr.Comma = opts.Delimiter
	cr.FieldsPerRecord = -1
	raw, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty csv: no header row")
	}
	header := raw[0]
	cols := make([]Column, len(header))
	for i, h := range header {
		cols[i] = Column{Name: strings.TrimSpace(h)}
	}
	var recs []Record
	for ri, row := range raw[1:] {
		vals := make(map[string]string, len(cols))
		nulls := make(map[string]bool)
		for ci, c := range cols {
			if ci < len(row) {
				vals[c.Name] = row[ci]
			} else {
				nulls[c.Name] = true
			}
		}
		recs = append(recs, Record{Table: opts.Table, Values: vals, Nulls: nulls, Row: ri})
	}
	inferColumnTypes(cols, recs)
	return &CSVSource{table: opts.Table, columns: cols, records: recs, sampleSize: opts.SampleSize}, nil
}

func (s *CSVSource) Schemas(_ context.Context) ([]Schema, error) {
	n := s.sampleSize
	if n > len(s.records) {
		n = len(s.records)
	}
	return []Schema{{Table: s.table, Columns: s.columns, Sample: s.records[:n]}}, nil
}

func (s *CSVSource) Records(ctx context.Context, fn func(Record) error) error {
	for _, r := range s.records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func (s *CSVSource) Close() error { return nil }

// inferColumnTypes assigns a best-effort Type to each column from its values.
func inferColumnTypes(cols []Column, recs []Record) {
	for i := range cols {
		name := cols[i].Name
		allInt, allFloat, seen := true, true, false
		for _, r := range recs {
			v, ok := r.Get(name)
			if !ok || v == "" {
				continue
			}
			seen = true
			if _, err := strconv.ParseInt(v, 10, 64); err != nil {
				allInt = false
			}
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				allFloat = false
			}
		}
		switch {
		case !seen:
			cols[i].Type = ""
		case allInt:
			cols[i].Type = "integer"
		case allFloat:
			cols[i].Type = "number"
		default:
			cols[i].Type = "text"
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/importflow/ -run TestCSVSource -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/source.go pkg/importflow/source_csv.go pkg/importflow/source_csv_test.go
git commit -m "feat(importflow): add Source interface and in-memory CSVSource"
```

---

## Task 3: SQLDumpSource — INSERT statement parsing (MySQL/PG common subset)

**Files:**
- Create: `pkg/importflow/source_dump.go`
- Test: `pkg/importflow/source_dump_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run TestDumpSource -v`
Expected: build failure — `undefined: NewSQLDumpSource`.

- [ ] **Step 3: Write source_dump.go (statement scanner + INSERT parser)**

```go
// pkg/importflow/source_dump.go
package importflow

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Dialect selects dump-specific parsing rules.
type Dialect string

const (
	DialectAuto     Dialect = "auto"
	DialectMySQL    Dialect = "mysql"
	DialectPostgres Dialect = "postgres"
)

// DumpOptions configures a SQLDumpSource.
type DumpOptions struct {
	Dialect    Dialect // default DialectAuto
	SampleSize int     // sample rows per table in Schemas(); default 5
}

// SQLDumpSource parses a common subset of MySQL/PG dumps: CREATE TABLE (for
// column order), INSERT INTO ... VALUES (...), and PG COPY ... \. blocks.
// Unrecognized statements are recorded in Unparsed() instead of being dropped.
type SQLDumpSource struct {
	dialect    Dialect
	sampleSize int
	columns    map[string][]Column // table -> declared columns (from CREATE TABLE)
	order      []string            // table insertion order for stable Schemas()
	records    []Record
	unparsed   []string
}

// NewSQLDumpSource parses the entire dump eagerly.
func NewSQLDumpSource(r io.Reader, opts DumpOptions) (*SQLDumpSource, error) {
	if opts.Dialect == "" {
		opts.Dialect = DialectAuto
	}
	if opts.SampleSize == 0 {
		opts.SampleSize = 5
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read dump: %w", err)
	}
	s := &SQLDumpSource{
		dialect:    opts.Dialect,
		sampleSize: opts.SampleSize,
		columns:    map[string][]Column{},
	}
	if err := s.parse(string(data)); err != nil {
		return nil, err
	}
	return s, nil
}

// Unparsed returns statements the parser could not handle.
func (s *SQLDumpSource) Unparsed() []string { return s.unparsed }

func (s *SQLDumpSource) parse(dump string) error {
	for _, stmt := range splitStatements(dump) {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "CREATE TABLE"):
			s.parseCreateTable(trimmed)
		case strings.HasPrefix(upper, "INSERT INTO"):
			if err := s.parseInsert(trimmed); err != nil {
				s.unparsed = append(s.unparsed, trimmed)
			}
		default:
			s.unparsed = append(s.unparsed, trimmed)
		}
	}
	return nil
}

// splitStatements splits on ';' that is outside single/double quotes, honoring
// the backslash and doubled-quote escapes used by MySQL/PG dumps.
func splitStatements(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if quote != 0 {
			b.WriteRune(c)
			if c == '\\' && i+1 < len(runes) {
				i++
				b.WriteRune(runes[i])
				continue
			}
			if c == quote {
				if i+1 < len(runes) && runes[i+1] == quote { // doubled quote
					i++
					b.WriteRune(runes[i])
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			b.WriteRune(c)
		case ';':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(c)
		}
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, b.String())
	}
	return out
}

func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"")
	if dot := strings.LastIndex(s, "."); dot >= 0 { // schema.table -> table
		s = s[dot+1:]
	}
	return strings.Trim(s, "`\"")
}

// parseCreateTable records column order; column types are best-effort.
func (s *SQLDumpSource) parseCreateTable(stmt string) {
	open := strings.Index(stmt, "(")
	if open < 0 {
		return
	}
	head := stmt[:open]
	fields := strings.Fields(head) // CREATE TABLE <name>
	if len(fields) < 3 {
		return
	}
	table := unquoteIdent(fields[2])
	body := stmt[open+1:]
	if close := strings.LastIndex(body, ")"); close >= 0 {
		body = body[:close]
	}
	var cols []Column
	for _, line := range strings.Split(body, ",") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		up := strings.ToUpper(line)
		if strings.HasPrefix(up, "PRIMARY") || strings.HasPrefix(up, "KEY") ||
			strings.HasPrefix(up, "UNIQUE") || strings.HasPrefix(up, "CONSTRAINT") ||
			strings.HasPrefix(up, "FOREIGN") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		name := unquoteIdent(parts[0])
		typ := ""
		if len(parts) > 1 {
			typ = strings.ToLower(parts[1])
		}
		cols = append(cols, Column{Name: name, Type: normalizeSQLType(typ)})
	}
	if len(cols) > 0 {
		if _, seen := s.columns[table]; !seen {
			s.order = append(s.order, table)
		}
		s.columns[table] = cols
	}
}

func normalizeSQLType(t string) string {
	switch {
	case t == "":
		return ""
	case strings.HasPrefix(t, "int") || strings.HasPrefix(t, "bigint") ||
		strings.HasPrefix(t, "smallint") || strings.HasPrefix(t, "serial"):
		return "integer"
	case strings.HasPrefix(t, "float") || strings.HasPrefix(t, "double") ||
		strings.HasPrefix(t, "numeric") || strings.HasPrefix(t, "decimal") ||
		strings.HasPrefix(t, "real"):
		return "number"
	case strings.HasPrefix(t, "timestamp") || strings.HasPrefix(t, "date") ||
		strings.HasPrefix(t, "datetime"):
		return "timestamp"
	default:
		return "text"
	}
}

// parseInsert handles: INSERT INTO <table> [(c1,c2,...)] VALUES (..),(..) ;
func (s *SQLDumpSource) parseInsert(stmt string) error {
	rest := strings.TrimSpace(stmt[len("INSERT INTO"):])
	// table name = up to first space or '('
	end := strings.IndexAny(rest, " (")
	if end < 0 {
		return fmt.Errorf("malformed insert")
	}
	table := unquoteIdent(rest[:end])
	rest = strings.TrimSpace(rest[end:])

	var cols []string
	if strings.HasPrefix(rest, "(") {
		close := strings.Index(rest, ")")
		if close < 0 {
			return fmt.Errorf("unterminated column list")
		}
		for _, c := range strings.Split(rest[1:close], ",") {
			cols = append(cols, unquoteIdent(c))
		}
		rest = strings.TrimSpace(rest[close+1:])
	} else if declared, ok := s.columns[table]; ok {
		for _, c := range declared {
			cols = append(cols, c.Name)
		}
	} else {
		return fmt.Errorf("no column list and no CREATE TABLE for %q", table)
	}

	vi := strings.Index(strings.ToUpper(rest), "VALUES")
	if vi < 0 {
		return fmt.Errorf("missing VALUES")
	}
	tuples := parseValueTuples(rest[vi+len("VALUES"):])
	if len(tuples) == 0 {
		return fmt.Errorf("no value tuples")
	}
	if _, seen := s.columns[table]; !seen {
		s.order = append(s.order, table)
		decl := make([]Column, len(cols))
		for i, c := range cols {
			decl[i] = Column{Name: c}
		}
		s.columns[table] = decl
	}
	startRow := s.tableRowCount(table)
	for ti, tuple := range tuples {
		vals := make(map[string]string, len(cols))
		nulls := make(map[string]bool)
		for i, c := range cols {
			if i < len(tuple) {
				if tuple[i].isNull {
					nulls[c] = true
				} else {
					vals[c] = tuple[i].value
				}
			} else {
				nulls[c] = true
			}
		}
		s.records = append(s.records, Record{Table: table, Values: vals, Nulls: nulls, Row: startRow + ti})
	}
	return nil
}

func (s *SQLDumpSource) tableRowCount(table string) int {
	n := 0
	for _, r := range s.records {
		if r.Table == table {
			n++
		}
	}
	return n
}

type cell struct {
	value  string
	isNull bool
}

// parseValueTuples parses "(a,b),(c,d)" into tuples of cells, honoring quotes,
// doubled-quote and backslash escapes, and bareword NULL.
func parseValueTuples(s string) [][]cell {
	var tuples [][]cell
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		for i < len(runes) && runes[i] != '(' {
			i++
		}
		if i >= len(runes) {
			break
		}
		i++ // skip '('
		var tuple []cell
		var b strings.Builder
		var quote rune
		quoted := false
		flush := func() {
			raw := b.String()
			b.Reset()
			if !quoted && strings.EqualFold(strings.TrimSpace(raw), "NULL") {
				tuple = append(tuple, cell{isNull: true})
			} else if quoted {
				tuple = append(tuple, cell{value: raw})
			} else {
				tuple = append(tuple, cell{value: strings.TrimSpace(raw)})
			}
			quoted = false
		}
		for i < len(runes) {
			c := runes[i]
			if quote != 0 {
				if c == '\\' && i+1 < len(runes) {
					next := runes[i+1]
					switch next {
					case 'n':
						b.WriteRune('\n')
					case 't':
						b.WriteRune('\t')
					case 'r':
						b.WriteRune('\r')
					default:
						b.WriteRune(next)
					}
					i += 2
					continue
				}
				if c == quote {
					if i+1 < len(runes) && runes[i+1] == quote {
						b.WriteRune(quote)
						i += 2
						continue
					}
					quote = 0
					i++
					continue
				}
				b.WriteRune(c)
				i++
				continue
			}
			switch c {
			case '\'', '"':
				quote = c
				quoted = true
				i++
			case ',':
				flush()
				i++
			case ')':
				flush()
				i++
				tuples = append(tuples, tuple)
				goto nextTuple
			default:
				b.WriteRune(c)
				i++
			}
		}
	nextTuple:
	}
	return tuples
}

func (s *SQLDumpSource) Schemas(_ context.Context) ([]Schema, error) {
	var out []Schema
	for _, table := range s.order {
		cols := s.columns[table]
		var sample []Record
		for _, r := range s.records {
			if r.Table == table {
				sample = append(sample, r)
				if len(sample) >= s.sampleSize {
					break
				}
			}
		}
		out = append(out, Schema{Table: table, Columns: cols, Sample: sample})
	}
	return out, nil
}

func (s *SQLDumpSource) Records(ctx context.Context, fn func(Record) error) error {
	for _, r := range s.records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLDumpSource) Close() error { return nil }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/importflow/ -run TestDumpSource -v`
Expected: PASS (both `TestDumpSourceInsert` and `TestDumpSourceQuoteEscapes`).

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/source_dump.go pkg/importflow/source_dump_test.go
git commit -m "feat(importflow): parse INSERT statements from SQL dumps"
```

---

## Task 4: SQLDumpSource — PG COPY blocks + unparsed reporting + CREATE TABLE schema

**Files:**
- Modify: `pkg/importflow/source_dump.go`
- Test: `pkg/importflow/source_dump_copy_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run TestDumpSourceCopy -v`
Expected: FAIL — COPY block is currently treated as unparsed and produces 0 rows.

- [ ] **Step 3: Add COPY handling**

COPY blocks span multiple statements because the data rows contain no `;`. Parse them from the raw dump *before* statement splitting. Replace `parse` and add `extractCopyBlocks`:

```go
// (replace the existing parse method in source_dump.go)
func (s *SQLDumpSource) parse(dump string) error {
	remainder := s.extractCopyBlocks(dump)
	for _, stmt := range splitStatements(remainder) {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "CREATE TABLE"):
			s.parseCreateTable(trimmed)
		case strings.HasPrefix(upper, "INSERT INTO"):
			if err := s.parseInsert(trimmed); err != nil {
				s.unparsed = append(s.unparsed, trimmed)
			}
		default:
			s.unparsed = append(s.unparsed, trimmed)
		}
	}
	return nil
}

// extractCopyBlocks consumes "COPY <t> (cols) FROM stdin; <tab-rows> \." blocks
// and returns the dump with those blocks removed (so the leftover is safe to
// split on ';'). CREATE TABLE statements may appear before the COPY block, so
// scan the remainder for them first.
func (s *SQLDumpSource) extractCopyBlocks(dump string) string {
	lines := strings.Split(dump, "\n")
	var kept []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		up := strings.ToUpper(strings.TrimSpace(line))
		if strings.HasPrefix(up, "COPY ") && strings.Contains(up, "FROM STDIN") {
			s.parseCopyHeader(line)
			i++
			for i < len(lines) {
				if strings.TrimSpace(lines[i]) == `\.` {
					i++
					break
				}
				s.parseCopyRow(lines[i])
				i++
			}
			continue
		}
		kept = append(kept, line)
		i++
	}
	return strings.Join(kept, "\n")
}

// copyState tracks the table/columns of the COPY block currently being read.
func (s *SQLDumpSource) parseCopyHeader(line string) {
	line = strings.TrimSpace(line)
	rest := strings.TrimSpace(line[len("COPY"):])
	end := strings.IndexAny(rest, " (")
	if end < 0 {
		s.copyTable, s.copyCols = "", nil
		return
	}
	table := unquoteIdent(rest[:end])
	rest = strings.TrimSpace(rest[end:])
	var cols []string
	if strings.HasPrefix(rest, "(") {
		if close := strings.Index(rest, ")"); close >= 0 {
			for _, c := range strings.Split(rest[1:close], ",") {
				cols = append(cols, unquoteIdent(c))
			}
		}
	} else if declared, ok := s.columns[table]; ok {
		for _, c := range declared {
			cols = append(cols, c.Name)
		}
	}
	if _, seen := s.columns[table]; !seen {
		s.order = append(s.order, table)
		decl := make([]Column, len(cols))
		for i, c := range cols {
			decl[i] = Column{Name: c}
		}
		s.columns[table] = decl
	}
	s.copyTable, s.copyCols = table, cols
}

func (s *SQLDumpSource) parseCopyRow(line string) {
	if s.copyTable == "" || strings.TrimSpace(line) == "" {
		return
	}
	fields := strings.Split(line, "\t")
	vals := make(map[string]string, len(s.copyCols))
	nulls := make(map[string]bool)
	for i, c := range s.copyCols {
		if i < len(fields) {
			f := fields[i]
			if f == `\N` {
				nulls[c] = true
			} else {
				vals[c] = decodeCopyField(f)
			}
		} else {
			nulls[c] = true
		}
	}
	s.records = append(s.records, Record{
		Table:  s.copyTable,
		Values: vals,
		Nulls:  nulls,
		Row:    s.tableRowCount(s.copyTable),
	})
}

func decodeCopyField(f string) string {
	r := strings.NewReplacer(`\t`, "\t", `\n`, "\n", `\r`, "\r", `\\`, `\`)
	return r.Replace(f)
}
```

Add the COPY-state fields to the struct (modify the `SQLDumpSource` definition):

```go
type SQLDumpSource struct {
	dialect    Dialect
	sampleSize int
	columns    map[string][]Column
	order      []string
	records    []Record
	unparsed   []string
	copyTable  string   // current COPY block target table
	copyCols   []string // current COPY block columns
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/importflow/ -run TestDumpSource -v`
Expected: PASS — `TestDumpSourceInsert`, `TestDumpSourceQuoteEscapes`, `TestDumpSourceCopyAndUnparsed`.

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/source_dump.go pkg/importflow/source_dump_copy_test.go
git commit -m "feat(importflow): parse PG COPY blocks and report unparsed statements"
```

---

## Task 5: MappingPlan types + template rendering

**Files:**
- Create: `pkg/importflow/plan.go`
- Test: `pkg/importflow/plan_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/importflow/plan_test.go
package importflow

import "testing"

func TestRenderTemplate(t *testing.T) {
	r := Record{Values: map[string]string{"title": "Go", "body": "fast"}}
	got := renderTemplate("{title}\n\n{body}", r)
	if got != "Go\n\nfast" {
		t.Fatalf("render = %q; want %q", got, "Go\n\nfast")
	}
	// missing/NULL columns render as empty string
	got2 := renderTemplate("{title}-{missing}", r)
	if got2 != "Go-" {
		t.Fatalf("render = %q; want %q", got2, "Go-")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run TestRenderTemplate -v`
Expected: build failure — `undefined: renderTemplate`.

- [ ] **Step 3: Write plan.go**

```go
// pkg/importflow/plan.go
package importflow

import "strings"

// MappingPlan declares, per table, how source rows route to RAG and KG.
type MappingPlan struct {
	Tables map[string]TablePlan `json:"tables"`
}

// TablePlan is the per-table routing decision.
type TablePlan struct {
	Skip bool      `json:"skip,omitempty"`
	RAG  *RAGPlan  `json:"rag,omitempty"`
	KG   *KGPlan   `json:"kg,omitempty"`
}

// RAGPlan describes how a row becomes a retrievable text chunk.
type RAGPlan struct {
	Namespace   string   `json:"namespace,omitempty"`
	ContentTmpl string   `json:"content_tmpl"`        // "{title}\n\n{body}"
	IDColumn    string   `json:"id_column,omitempty"` // default synthesized "table:row"
	Metadata    []string `json:"metadata,omitempty"`  // columns copied into metadata
	Refine      bool     `json:"refine,omitempty"`    // run TextRefiner before embedding
}

// KGPlan describes how a row becomes entities and relations.
type KGPlan struct {
	Entities    []EntityMap   `json:"entities,omitempty"`
	Relations   []RelationMap `json:"relations,omitempty"`
	TextExtract []TextExtract `json:"text_extract,omitempty"`
}

// EntityMap maps columns to one entity per row.
type EntityMap struct {
	Ref       string   `json:"ref"`        // local handle, e.g. "customer"
	Type      string   `json:"type"`       // entity class
	IDTmpl    string   `json:"id_tmpl"`    // "{customer_id}"
	LabelTmpl string   `json:"label_tmpl,omitempty"`
	Props     []string `json:"props,omitempty"`
}

// RelationMap connects two entity refs with a predicate.
type RelationMap struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}

// TextExtract names a free-text column for AI triple extraction.
type TextExtract struct {
	Column string   `json:"column"`
	Types  []string `json:"types,omitempty"`
}

// renderTemplate substitutes {column} placeholders with record values.
// Missing or NULL columns render as the empty string.
func renderTemplate(tmpl string, r Record) string {
	var b strings.Builder
	runes := []rune(tmpl)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '{' {
			if close := indexRune(runes, i+1, '}'); close >= 0 {
				name := string(runes[i+1 : close])
				v, _ := r.Get(name)
				b.WriteString(v)
				i = close
				continue
			}
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

func indexRune(rs []rune, from int, target rune) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == target {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/importflow/ -run TestRenderTemplate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/plan.go pkg/importflow/plan_test.go
git commit -m "feat(importflow): add MappingPlan types and template renderer"
```

---

## Task 6: Mapper — apply a TablePlan to a Record (deterministic, no LLM)

**Files:**
- Create: `pkg/importflow/mapper.go`
- Test: `pkg/importflow/mapper_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/importflow/mapper_test.go
package importflow

import "testing"

func TestMapperRAGChunk(t *testing.T) {
	plan := TablePlan{RAG: &RAGPlan{
		ContentTmpl: "{title}\n\n{body}",
		IDColumn:    "id",
		Metadata:    []string{"author"},
	}}
	r := Record{Table: "docs", Values: map[string]string{
		"id": "7", "title": "Go", "body": "fast", "author": "Ada",
	}}
	chunk, ok := mapRAG(plan.RAG, r)
	if !ok {
		t.Fatal("mapRAG ok = false; want true")
	}
	if chunk.id != "7" || chunk.content != "Go\n\nfast" {
		t.Fatalf("chunk = %+v", chunk)
	}
	if chunk.metadata["author"] != "Ada" || chunk.metadata["_table"] != "docs" {
		t.Fatalf("metadata = %+v", chunk.metadata)
	}
}

func TestMapperStructuredTriples(t *testing.T) {
	plan := &KGPlan{
		Entities: []EntityMap{
			{Ref: "cust", Type: "Customer", IDTmpl: "{customer_id}", LabelTmpl: "{customer_id}"},
			{Ref: "prod", Type: "Product", IDTmpl: "{product_id}"},
		},
		Relations: []RelationMap{
			{Subject: "cust", Predicate: "purchased", Object: "prod"},
		},
	}
	r := Record{Table: "orders", Values: map[string]string{"customer_id": "c1", "product_id": "p9"}}
	triples := mapTriples(plan, r)
	// expect: cust rdf:type Customer, prod rdf:type Product, cust purchased prod,
	// plus the label triple for cust
	var rel bool
	for _, tr := range triples {
		if tr.Subject.Value == "c1" && tr.Predicate.Value == "purchased" && tr.Object.Value == "p9" {
			rel = true
		}
	}
	if !rel {
		t.Fatalf("expected purchased relation in %+v", triples)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run TestMapper -v`
Expected: build failure — `undefined: mapRAG`, `undefined: mapTriples`.

- [ ] **Step 3: Write mapper.go**

```go
// pkg/importflow/mapper.go
package importflow

import (
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// ragChunk is one row prepared for the RAG sink.
type ragChunk struct {
	id        string
	content   string
	metadata  map[string]string
	namespace string
	refine    bool
}

// mapRAG renders a row into a ragChunk; ok is false when content is empty.
func mapRAG(p *RAGPlan, r Record) (ragChunk, bool) {
	if p == nil {
		return ragChunk{}, false
	}
	content := renderTemplate(p.ContentTmpl, r)
	if content == "" {
		return ragChunk{}, false
	}
	id := ""
	if p.IDColumn != "" {
		if v, ok := r.Get(p.IDColumn); ok {
			id = v
		}
	}
	if id == "" {
		id = fmt.Sprintf("%s:%d", r.Table, r.Row)
	}
	md := map[string]string{"_table": r.Table}
	for _, col := range p.Metadata {
		if v, ok := r.Get(col); ok {
			md[col] = v
		}
	}
	return ragChunk{id: id, content: content, metadata: md, namespace: p.Namespace, refine: p.Refine}, true
}

// entityIRI builds a stable IRI for an entity instance.
func entityIRI(typ, id string) string {
	return fmt.Sprintf("urn:cortexdb:%s:%s", typ, id)
}

// mapTriples produces structured RDF triples for a row (rdf:type, labels,
// relations). Entities with an empty rendered ID are skipped.
func mapTriples(p *KGPlan, r Record) []graph.RDFTriple {
	if p == nil {
		return nil
	}
	const (
		rdfType  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
		rdfsLabel = "http://www.w3.org/2000/01/rdf-schema#label"
	)
	refIRI := map[string]string{} // entity ref -> instance IRI
	var triples []graph.RDFTriple
	for _, e := range p.Entities {
		id := renderTemplate(e.IDTmpl, r)
		if id == "" {
			continue
		}
		iri := entityIRI(e.Type, id)
		refIRI[e.Ref] = iri
		triples = append(triples, graph.RDFTriple{
			Subject:   graph.NewIRI(iri),
			Predicate: graph.NewIRI(rdfType),
			Object:    graph.NewIRI("urn:cortexdb:class:" + e.Type),
		})
		if e.LabelTmpl != "" {
			if label := renderTemplate(e.LabelTmpl, r); label != "" {
				triples = append(triples, graph.RDFTriple{
					Subject:   graph.NewIRI(iri),
					Predicate: graph.NewIRI(rdfsLabel),
					Object:    graph.NewLiteral(label),
				})
			}
		}
		for _, prop := range e.Props {
			if v, ok := r.Get(prop); ok {
				triples = append(triples, graph.RDFTriple{
					Subject:   graph.NewIRI(iri),
					Predicate: graph.NewIRI("urn:cortexdb:prop:" + prop),
					Object:    graph.NewLiteral(v),
				})
			}
		}
	}
	for _, rel := range p.Relations {
		s, sok := refIRI[rel.Subject]
		o, ook := refIRI[rel.Object]
		if !sok || !ook {
			continue
		}
		triples = append(triples, graph.RDFTriple{
			Subject:   graph.NewIRI(s),
			Predicate: graph.NewIRI("urn:cortexdb:rel:" + rel.Predicate),
			Object:    graph.NewIRI(o),
		})
	}
	return triples
}
```

Note: the test asserts on `tr.Predicate.Value == "purchased"` but the implementation prefixes predicates with `urn:cortexdb:rel:`. Fix the test assertion to match the implementation before running:

```go
		if tr.Subject.Value == "c1" ... // WRONG: subject is an IRI, not the raw id
```

Replace `TestMapperStructuredTriples`'s loop with assertions on the IRI form:

```go
	wantSubj := entityIRI("Customer", "c1")
	wantObj := entityIRI("Product", "p9")
	var rel bool
	for _, tr := range triples {
		if tr.Subject.Value == wantSubj &&
			tr.Predicate.Value == "urn:cortexdb:rel:purchased" &&
			tr.Object.Value == wantObj {
			rel = true
		}
	}
	if !rel {
		t.Fatalf("expected purchased relation in %+v", triples)
	}
```

(Apply this corrected assertion in Step 1's test file.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/importflow/ -run TestMapper -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/mapper.go pkg/importflow/mapper_test.go
git commit -m "feat(importflow): add deterministic Mapper for RAG chunks and KG triples"
```

---

## Task 7: RAGSink — write chunks via the cortexdb facade

**Files:**
- Create: `pkg/importflow/sink_rag.go`
- Test: `pkg/importflow/sink_rag_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/importflow/sink_rag_test.go
package importflow

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func testDB(t *testing.T) *cortexdb.DB {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "test.db")))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRAGSinkFlush(t *testing.T) {
	db := testDB(t)
	sink := newRAGSink(db, 2)
	ctx := context.Background()

	chunks := []ragChunk{
		{id: "1", content: "Ada Lovelace wrote the first algorithm", metadata: map[string]string{"_table": "people"}},
		{id: "2", content: "Alan Turing broke the Enigma code", metadata: map[string]string{"_table": "people"}},
	}
	for _, c := range chunks {
		if err := sink.add(ctx, c); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	if err := sink.flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sink.count() != 2 {
		t.Fatalf("count = %d; want 2", sink.count())
	}

	// No embedder configured -> lexical/FTS5 retrieval must still find it.
	res, err := db.SearchTextOnly(ctx, "Enigma", cortexdb.TextSearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected at least one FTS5 hit for 'Enigma'")
	}
}
```

> Before writing the implementation, confirm the exact `SearchTextOnly` signature and `TextSearchOptions` field names in `pkg/cortexdb/text_search.go`; if `SearchTextOnly` takes a different option type or arg shape, adjust the test call accordingly (the assertion — "non-empty results for 'Enigma'" — stays the same).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run TestRAGSink -v`
Expected: build failure — `undefined: newRAGSink`.

- [ ] **Step 3: Write sink_rag.go**

```go
// pkg/importflow/sink_rag.go
package importflow

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// ragSink batches ragChunks and writes them through the cortexdb facade.
// With no embedder configured, InsertTextBatch stores text for FTS5 retrieval.
type ragSink struct {
	db        *cortexdb.DB
	batchSize int
	pending   []ragChunk
	written   int
}

func newRAGSink(db *cortexdb.DB, batchSize int) *ragSink {
	if batchSize <= 0 {
		batchSize = 500
	}
	return &ragSink{db: db, batchSize: batchSize}
}

func (s *ragSink) add(ctx context.Context, c ragChunk) error {
	s.pending = append(s.pending, c)
	if len(s.pending) >= s.batchSize {
		return s.flush(ctx)
	}
	return nil
}

func (s *ragSink) flush(ctx context.Context) error {
	if len(s.pending) == 0 {
		return nil
	}
	texts := make(map[string]string, len(s.pending))
	for _, c := range s.pending {
		texts[c.id] = c.content
	}
	// Per-chunk metadata differs; InsertTextBatch applies one metadata map to
	// all. We pass a minimal shared map and rely on SaveKnowledge for richer
	// per-row metadata in a later iteration. For now, batch insert the text.
	if err := s.db.InsertTextBatch(ctx, texts, nil); err != nil {
		return err
	}
	s.written += len(s.pending)
	s.pending = s.pending[:0]
	return nil
}

func (s *ragSink) count() int { return s.written }
```

> Design note: `InsertTextBatch` applies a single metadata map to the whole batch. Per-row metadata fidelity is deferred (YAGNI for the first cut); the `_table` and configured metadata columns are still rendered into `ragChunk.metadata` and can be persisted via `SaveKnowledge` in a follow-up. Keep the chunk metadata populated so the upgrade path is clean.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/importflow/ -run TestRAGSink -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/sink_rag.go pkg/importflow/sink_rag_test.go
git commit -m "feat(importflow): add batching RAGSink over InsertTextBatch"
```

---

## Task 8: KGSink — write triples via UpsertKnowledgeGraph

**Files:**
- Create: `pkg/importflow/sink_kg.go`
- Test: `pkg/importflow/sink_kg_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/importflow/sink_kg_test.go
package importflow

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func TestKGSinkFlush(t *testing.T) {
	db := testDB(t)
	sink := newKGSink(db, 100)
	ctx := context.Background()

	triples := []graph.RDFTriple{
		{
			Subject:   graph.NewIRI("urn:cortexdb:Customer:c1"),
			Predicate: graph.NewIRI("urn:cortexdb:rel:purchased"),
			Object:    graph.NewIRI("urn:cortexdb:Product:p9"),
		},
	}
	if err := sink.add(ctx, triples); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := sink.flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sink.count() != 1 {
		t.Fatalf("count = %d; want 1", sink.count())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run TestKGSink -v`
Expected: build failure — `undefined: newKGSink`.

- [ ] **Step 3: Write sink_kg.go**

```go
// pkg/importflow/sink_kg.go
package importflow

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// kgSink batches RDF triples and writes them through UpsertKnowledgeGraph.
type kgSink struct {
	db        *cortexdb.DB
	batchSize int
	pending   []graph.RDFTriple
	written   int
}

func newKGSink(db *cortexdb.DB, batchSize int) *kgSink {
	if batchSize <= 0 {
		batchSize = 500
	}
	return &kgSink{db: db, batchSize: batchSize}
}

func (s *kgSink) add(ctx context.Context, triples []graph.RDFTriple) error {
	s.pending = append(s.pending, triples...)
	if len(s.pending) >= s.batchSize {
		return s.flush(ctx)
	}
	return nil
}

func (s *kgSink) flush(ctx context.Context) error {
	if len(s.pending) == 0 {
		return nil
	}
	resp, err := s.db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
		Triples: s.pending,
	})
	if err != nil {
		return err
	}
	s.written += resp.Count
	s.pending = s.pending[:0]
	return nil
}

func (s *kgSink) count() int { return s.written }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/importflow/ -run TestKGSink -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/sink_kg.go pkg/importflow/sink_kg_test.go
git commit -m "feat(importflow): add batching KGSink over UpsertKnowledgeGraph"
```

---

## Task 9: AI interfaces — MappingInferer + TextRefiner (with JSONGenerator-backed defaults)

**Files:**
- Create: `pkg/importflow/infer.go`
- Create: `pkg/importflow/refine.go`
- Test: `pkg/importflow/infer_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/importflow/infer_test.go
package importflow

import (
	"context"
	"testing"
)

// fakeJSONGen returns canned JSON regardless of prompt.
type fakeJSONGen struct{ payload string }

func (f fakeJSONGen) GenerateJSON(_ context.Context, _ string, _ string) ([]byte, error) {
	return []byte(f.payload), nil
}

func TestLLMInfererParsesPlan(t *testing.T) {
	payload := `{"tables":{"people":{"rag":{"content_tmpl":"{name}\n{bio}","id_column":"id"}}}}`
	inferer := LLMInferer{Client: fakeJSONGen{payload: payload}}
	schemas := []Schema{{Table: "people", Columns: []Column{{Name: "id"}, {Name: "name"}, {Name: "bio"}}}}

	plan, err := inferer.InferPlan(context.Background(), schemas, Goal{BuildRAG: true})
	if err != nil {
		t.Fatalf("InferPlan: %v", err)
	}
	tp, ok := plan.Tables["people"]
	if !ok || tp.RAG == nil || tp.RAG.ContentTmpl != "{name}\n{bio}" {
		t.Fatalf("plan = %+v; want people.rag.content_tmpl set", plan)
	}
}

func TestLLMRefiner(t *testing.T) {
	r := LLMRefiner{Client: fakeJSONGen{payload: `{"text":"cleaned"}`}}
	out, err := r.Refine(context.Background(), "t", "c", "raw  text")
	if err != nil {
		t.Fatalf("Refine: %v", err)
	}
	if out != "cleaned" {
		t.Fatalf("out = %q; want cleaned", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run 'TestLLMInferer|TestLLMRefiner' -v`
Expected: build failure — `undefined: LLMInferer`, `undefined: LLMRefiner`.

- [ ] **Step 3: Write infer.go and refine.go**

```go
// pkg/importflow/infer.go
package importflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// MappingInferer proposes a MappingPlan from table schemas and a goal.
type MappingInferer interface {
	InferPlan(ctx context.Context, schemas []Schema, goal Goal) (MappingPlan, error)
}

// LLMInferer is the default MappingInferer, backed by a graphflow.JSONGenerator.
type LLMInferer struct {
	Client graphflow.JSONGenerator
}

const inferSystemPrompt = `You map relational tables to a CortexDB import plan.
Return ONLY JSON matching this shape:
{"tables":{"<table>":{"skip":false,
  "rag":{"namespace":"","content_tmpl":"{col}...","id_column":"","metadata":["col"],"refine":false},
  "kg":{"entities":[{"ref":"","type":"","id_tmpl":"{col}","label_tmpl":"{col}","props":["col"]}],
        "relations":[{"subject":"ref","predicate":"verb","object":"ref"}],
        "text_extract":[{"column":"col"}]}}}}
Route long free-text columns to rag.content_tmpl; id/foreign-key columns to kg
entities/relations. Omit rag or kg when the goal does not request it.`

func (l LLMInferer) InferPlan(ctx context.Context, schemas []Schema, goal Goal) (MappingPlan, error) {
	if l.Client == nil {
		return MappingPlan{}, fmt.Errorf("importflow: LLMInferer requires a JSONGenerator client")
	}
	user, err := buildInferUserPrompt(schemas, goal)
	if err != nil {
		return MappingPlan{}, err
	}
	raw, err := l.Client.GenerateJSON(ctx, inferSystemPrompt, user)
	if err != nil {
		return MappingPlan{}, fmt.Errorf("importflow: infer plan: %w", err)
	}
	var plan MappingPlan
	if err := json.Unmarshal(sanitizeJSON(raw), &plan); err != nil {
		return MappingPlan{}, fmt.Errorf("importflow: parse inferred plan: %w", err)
	}
	if plan.Tables == nil {
		plan.Tables = map[string]TablePlan{}
	}
	return plan, nil
}

func buildInferUserPrompt(schemas []Schema, goal Goal) (string, error) {
	payload := map[string]any{
		"goal":    map[string]any{"build_rag": goal.BuildRAG, "build_kg": goal.BuildKG, "hint": goal.Hint},
		"schemas": schemas,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// sanitizeJSON strips Markdown code fences some models add around JSON.
func sanitizeJSON(raw []byte) []byte {
	s := strings.TrimSpace(string(raw))
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return []byte(strings.TrimSpace(s))
}
```

```go
// pkg/importflow/refine.go
package importflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// TextRefiner cleans/summarizes a raw column value before embedding.
type TextRefiner interface {
	Refine(ctx context.Context, table, column, raw string) (string, error)
}

// LLMRefiner is the default TextRefiner, backed by a graphflow.JSONGenerator.
type LLMRefiner struct {
	Client graphflow.JSONGenerator
}

const refineSystemPrompt = `Clean and lightly summarize the given text for retrieval.
Return ONLY JSON: {"text":"<refined>"}.`

func (l LLMRefiner) Refine(ctx context.Context, table, column, raw string) (string, error) {
	if l.Client == nil {
		return raw, nil // no client: pass through unchanged
	}
	user, _ := json.Marshal(map[string]string{"table": table, "column": column, "text": raw})
	out, err := l.Client.GenerateJSON(ctx, refineSystemPrompt, string(user))
	if err != nil {
		return "", fmt.Errorf("importflow: refine: %w", err)
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(sanitizeJSON(out), &parsed); err != nil {
		return "", fmt.Errorf("importflow: parse refined text: %w", err)
	}
	return parsed.Text, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/importflow/ -run 'TestLLMInferer|TestLLMRefiner' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/infer.go pkg/importflow/refine.go pkg/importflow/infer_test.go
git commit -m "feat(importflow): add MappingInferer and TextRefiner with JSONGenerator defaults"
```

---

## Task 10: Importer orchestration — New, Options, Plan, Run, AutoImport

**Files:**
- Create: `pkg/importflow/importer.go`
- Test: `pkg/importflow/importer_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run TestImporter -v`
Expected: build failure — `undefined: New`, `undefined: WithMappingInferer`.

- [ ] **Step 3: Write importer.go**

```go
// pkg/importflow/importer.go
package importflow

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// Importer orchestrates Source -> MappingPlan -> RAG + KG sinks.
type Importer struct {
	db        *cortexdb.DB
	inferer   MappingInferer
	refiner   TextRefiner
	extractor graphflow.Extractor
	batchSize int
	strict    bool
}

// Option configures an Importer.
type Option func(*Importer)

// WithMappingInferer sets the AI mapping inferer used by Plan/AutoImport.
func WithMappingInferer(m MappingInferer) Option { return func(im *Importer) { im.inferer = m } }

// WithTextRefiner sets the AI text refiner used when RAGPlan.Refine is true.
func WithTextRefiner(r TextRefiner) Option { return func(im *Importer) { im.refiner = r } }

// WithExtractor sets the graphflow extractor used for KGPlan.TextExtract columns.
func WithExtractor(e graphflow.Extractor) Option { return func(im *Importer) { im.extractor = e } }

// WithBatchSize sets the sink batch size (default 500).
func WithBatchSize(n int) Option { return func(im *Importer) { im.batchSize = n } }

// WithStrictMode aborts the run on the first row error instead of collecting it.
func WithStrictMode() Option { return func(im *Importer) { im.strict = true } }

// New constructs an Importer over an open cortexdb.DB.
func New(db *cortexdb.DB, opts ...Option) *Importer {
	im := &Importer{db: db, batchSize: 500}
	for _, o := range opts {
		o(im)
	}
	return im
}

// Plan asks the configured MappingInferer to propose a plan from the source schemas.
func (im *Importer) Plan(ctx context.Context, src Source, goal Goal) (MappingPlan, error) {
	if im.inferer == nil {
		return MappingPlan{}, fmt.Errorf("importflow: Plan requires a MappingInferer (use WithMappingInferer)")
	}
	schemas, err := src.Schemas(ctx)
	if err != nil {
		return MappingPlan{}, fmt.Errorf("importflow: read schemas: %w", err)
	}
	return im.inferer.InferPlan(ctx, schemas, goal)
}

// AutoImport runs Plan then Run in one step.
func (im *Importer) AutoImport(ctx context.Context, src Source, goal Goal) (*Report, error) {
	plan, err := im.Plan(ctx, src, goal)
	if err != nil {
		return nil, err
	}
	return im.Run(ctx, src, plan)
}

// Run streams every record through the plan into the RAG and KG sinks.
func (im *Importer) Run(ctx context.Context, src Source, plan MappingPlan) (*Report, error) {
	rep := &Report{}
	if ds, ok := src.(*SQLDumpSource); ok {
		rep.UnparsedStatements = ds.Unparsed()
	}
	rag := newRAGSink(im.db, im.batchSize)
	kg := newKGSink(im.db, im.batchSize)

	err := src.Records(ctx, func(r Record) error {
		rep.RowsRead++
		tp, ok := plan.Tables[r.Table]
		if !ok || tp.Skip {
			rep.Skipped++
			return nil
		}
		if err := im.processRow(ctx, r, tp, rag, kg, rep); err != nil {
			if im.strict {
				return err
			}
			rep.addError(err)
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	if err := rag.flush(ctx); err != nil {
		return rep, err
	}
	if err := kg.flush(ctx); err != nil {
		return rep, err
	}
	rep.ChunksIndexed = rag.count()
	rep.TriplesCreated = kg.count()
	return rep, nil
}

func (im *Importer) processRow(ctx context.Context, r Record, tp TablePlan, rag *ragSink, kg *kgSink, rep *Report) error {
	if tp.RAG != nil {
		if chunk, ok := mapRAG(tp.RAG, r); ok {
			if chunk.refine && im.refiner != nil {
				refined, err := im.refiner.Refine(ctx, r.Table, tp.RAG.ContentTmpl, chunk.content)
				if err != nil {
					return err
				}
				chunk.content = refined
			}
			if err := rag.add(ctx, chunk); err != nil {
				return err
			}
		}
	}
	if tp.KG != nil {
		triples := mapTriples(tp.KG, r)
		if len(triples) > 0 {
			if err := kg.add(ctx, triples); err != nil {
				return err
			}
		}
		if im.extractor != nil {
			extracted, err := im.extractFromText(ctx, r, tp.KG)
			if err != nil {
				return err
			}
			if len(extracted) > 0 {
				if err := kg.add(ctx, extracted); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// extractFromText runs the graphflow extractor over configured free-text columns
// and converts the resulting edges into RDF triples.
func (im *Importer) extractFromText(ctx context.Context, r Record, kp *KGPlan) ([]graph.RDFTriple, error) {
	var triples []graph.RDFTriple
	for _, te := range kp.TextExtract {
		text, ok := r.Get(te.Column)
		if !ok || text == "" {
			continue
		}
		doc := graphflow.SourceDocument{
			ID:      fmt.Sprintf("%s:%d:%s", r.Table, r.Row, te.Column),
			Content: text,
		}
		res, err := im.extractor.Extract(ctx, doc)
		if err != nil {
			return nil, err
		}
		for _, e := range res.Edges {
			triples = append(triples, graph.RDFTriple{
				Subject:   graph.NewLiteral(e.Source),
				Predicate: graph.NewIRI("urn:cortexdb:rel:" + e.Relation),
				Object:    graph.NewLiteral(e.Target),
			})
		}
	}
	return triples, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/importflow/ -run TestImporter -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Run the full package suite with race**

Run: `go test -race ./pkg/importflow/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/importflow/importer.go pkg/importflow/importer_test.go
git commit -m "feat(importflow): add Importer orchestration (Plan/Run/AutoImport)"
```

---

## Task 11: Toolbox / MCP surface

**Files:**
- Create: `pkg/importflow/toolbox.go`
- Test: `pkg/importflow/toolbox_test.go`

> First inspect `pkg/graphflow/toolbox.go` and the schema helpers it calls (`gfObjectSchema`, etc.) and `cortexdb.ToolDefinition` (its exact fields: `Name`, `Description`, `InputSchema`). Mirror that pattern. The schema helpers in `pkg/cortexdb` (`toolObjectSchema`, ...) are unexported, so define small local equivalents in `toolbox.go` as shown below.

- [ ] **Step 1: Write the failing test**

```go
// pkg/importflow/toolbox_test.go
package importflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestToolboxDefinitions(t *testing.T) {
	db := testDB(t)
	tb := NewToolbox(New(db))
	defs := tb.Definitions()
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	if !names["importflow_run"] {
		t.Fatalf("missing importflow_run in %v", names)
	}
}

func TestToolboxCallRun(t *testing.T) {
	db := testDB(t)
	tb := NewToolbox(New(db))
	csv := "id,name,bio\n1,Ada,first programmer\n"
	input, _ := json.Marshal(map[string]any{
		"format":  "csv",
		"table":   "people",
		"data":    csv,
		"plan":    MappingPlan{Tables: map[string]TablePlan{"people": {RAG: &RAGPlan{ContentTmpl: "{name}: {bio}", IDColumn: "id"}}}},
	})
	out, err := tb.Call(context.Background(), "importflow_run", input)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	rep, ok := out.(*Report)
	if !ok {
		t.Fatalf("out type = %T; want *Report", out)
	}
	if rep.ChunksIndexed != 1 {
		t.Fatalf("ChunksIndexed = %d; want 1", rep.ChunksIndexed)
	}
	_ = strings.TrimSpace // keep import if unused after edits
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/importflow/ -run TestToolbox -v`
Expected: build failure — `undefined: NewToolbox`.

- [ ] **Step 3: Write toolbox.go**

```go
// pkg/importflow/toolbox.go
package importflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Toolbox exposes importflow as agent-callable tools.
type Toolbox struct {
	im *Importer
}

// NewToolbox wraps an Importer in a tool surface.
func NewToolbox(im *Importer) *Toolbox { return &Toolbox{im: im} }

// Definitions returns the tool definitions (mirrors graphflow's toolbox shape).
func (t *Toolbox) Definitions() []cortexdb.ToolDefinition {
	return []cortexdb.ToolDefinition{
		{
			Name:        "importflow_plan",
			Description: "Infer a CortexDB import MappingPlan from CSV/SQL-dump schemas using the configured AI inferer.",
			InputSchema: ifObjectSchema(
				[]string{"format", "data"},
				map[string]any{
					"format":    ifEnumSchema("Source format.", "csv", "dump"),
					"table":     ifStringSchema("Logical table name for CSV input."),
					"data":      ifStringSchema("Raw CSV text or SQL dump text."),
					"build_rag": ifBoolSchema("Build RAG text index."),
					"build_kg":  ifBoolSchema("Build knowledge-graph triples."),
					"hint":      ifStringSchema("Domain hint for the inferer."),
				},
			),
		},
		{
			Name:        "importflow_run",
			Description: "Run an import of CSV/SQL-dump data into CortexDB using a MappingPlan, building RAG and/or KG.",
			InputSchema: ifObjectSchema(
				[]string{"format", "data", "plan"},
				map[string]any{
					"format": ifEnumSchema("Source format.", "csv", "dump"),
					"table":  ifStringSchema("Logical table name for CSV input."),
					"data":   ifStringSchema("Raw CSV text or SQL dump text."),
					"plan":   ifObjectSchema(nil, map[string]any{"tables": map[string]any{"type": "object"}}),
				},
			),
		},
	}
}

// Call dispatches a tool by name.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	switch name {
	case "importflow_plan":
		return t.callPlan(ctx, input)
	case "importflow_run":
		return t.callRun(ctx, input)
	default:
		return nil, fmt.Errorf("importflow: unknown tool %q", name)
	}
}

type runInput struct {
	Format string       `json:"format"`
	Table  string       `json:"table"`
	Data   string       `json:"data"`
	Plan   MappingPlan  `json:"plan"`
}

type planInput struct {
	Format   string `json:"format"`
	Table    string `json:"table"`
	Data     string `json:"data"`
	BuildRAG bool   `json:"build_rag"`
	BuildKG  bool   `json:"build_kg"`
	Hint     string `json:"hint"`
}

func sourceFromInput(format, table, data string) (Source, error) {
	switch strings.ToLower(format) {
	case "csv":
		return NewCSVSource(strings.NewReader(data), CSVOptions{Table: table})
	case "dump":
		return NewSQLDumpSource(strings.NewReader(data), DumpOptions{})
	default:
		return nil, fmt.Errorf("importflow: unsupported format %q", format)
	}
}

func (t *Toolbox) callRun(ctx context.Context, input json.RawMessage) (any, error) {
	var in runInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	src, err := sourceFromInput(in.Format, in.Table, in.Data)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return t.im.Run(ctx, src, in.Plan)
}

func (t *Toolbox) callPlan(ctx context.Context, input json.RawMessage) (any, error) {
	var in planInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	src, err := sourceFromInput(in.Format, in.Table, in.Data)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return t.im.Plan(ctx, src, Goal{BuildRAG: in.BuildRAG, BuildKG: in.BuildKG, Hint: in.Hint})
}

// --- local JSON-schema helpers (mirror cortexdb's unexported helpers) ---

func ifObjectSchema(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func ifStringSchema(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func ifBoolSchema(desc string) map[string]any   { return map[string]any{"type": "boolean", "description": desc} }
func ifEnumSchema(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}
```

> Verify `cortexdb.ToolDefinition`'s field names (`Name`, `Description`, `InputSchema`) against `pkg/cortexdb` before compiling; adjust if the real struct differs. Also confirm that omitting `required` (Task header note from commit `f1fbadb` — "omit empty required") matches the established schema convention; `ifObjectSchema` already omits it when empty.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/importflow/ -run TestToolbox -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/importflow/toolbox.go pkg/importflow/toolbox_test.go
git commit -m "feat(importflow): add agent-callable Toolbox (importflow_plan/run)"
```

---

## Task 12: End-to-end test, runnable example, docs sync

**Files:**
- Create: `pkg/importflow/e2e_test.go`
- Create: `examples/07_importflow/main.go`
- Modify: `README.md`, `README_CN.md`, `SKILL.md`

- [ ] **Step 1: Write the end-to-end failing test**

```go
// pkg/importflow/e2e_test.go
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
```

- [ ] **Step 2: Run it to verify it passes (all prior tasks complete)**

Run: `go test ./pkg/importflow/ -run TestEndToEnd -v`
Expected: PASS.

- [ ] **Step 3: Write the runnable example**

```go
// examples/07_importflow/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func main() {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(".", "importflow_demo.db")))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	csv := "id,name,bio\n1,Ada,first programmer\n2,Alan,enigma codebreaker\n"
	src, err := importflow.NewCSVSource(strings.NewReader(csv), importflow.CSVOptions{Table: "people"})
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()

	plan := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"people": {
			RAG: &importflow.RAGPlan{ContentTmpl: "{name}: {bio}", IDColumn: "id"},
			KG:  &importflow.KGPlan{Entities: []importflow.EntityMap{{Ref: "p", Type: "Person", IDTmpl: "{id}", LabelTmpl: "{name}"}}},
		},
	}}

	rep, err := importflow.New(db).Run(ctx, src, plan)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("rows=%d chunks=%d triples=%d unparsed=%d\n",
		rep.RowsRead, rep.ChunksIndexed, rep.TriplesCreated, len(rep.UnparsedStatements))

	// With a real graphflow.JSONGenerator you could instead call:
	//   im := importflow.New(db, importflow.WithMappingInferer(importflow.LLMInferer{Client: yourLLM}))
	//   rep, _ := im.AutoImport(ctx, src, importflow.Goal{BuildRAG: true, BuildKG: true})
}
```

- [ ] **Step 4: Verify example compiles (mirrors CI step)**

Run: `(cd examples/07_importflow && go build -o /dev/null .)`
Expected: builds with no output.

- [ ] **Step 5: Update docs**

In `README.md` and `README_CN.md`, add `importflow` to the workflow-layers list/section alongside `memoryflow` and `graphflow`, describing it as: "external structured-data import (CSV / MySQL-PG dumps) into RAG + knowledge-graph foundations, AI-assisted mapping optional."

In `SKILL.md`, add the same one-line entry in the layered-architecture list so the status doc stays in sync (the CLAUDE.md convention requires README/README_CN/SKILL to move together).

- [ ] **Step 6: Run the full suite + build gates**

Run:
```bash
go build ./...
go test -race ./pkg/importflow/
for dir in examples/*/; do (cd "$dir" && go build -o /dev/null .); done
```
Expected: all PASS / build clean.

- [ ] **Step 7: Commit**

```bash
git add pkg/importflow/e2e_test.go examples/07_importflow/main.go README.md README_CN.md SKILL.md
git commit -m "feat(importflow): add e2e test, runnable example, and docs"
```

---

## Self-Review Notes (addressed)

- **Spec coverage:** Source interface + CSV (T2) + dump INSERT/COPY/CREATE (T3-4); RAG-vs-KG model encoded as MappingPlan (T5) + Mapper (T6); sinks (T7-8); AI interfaces with optional injection + degradation (T9); Importer Plan/Run/AutoImport + Report transparency + StrictMode + unparsed reporting (T10); Toolbox/MCP (T11); e2e + example + docs sync (T12). Embedder-optional path covered by using `SearchTextOnly` (FTS5) assertions throughout (no embedder wired in tests).
- **Degradation matrix:** no embedder → `InsertTextBatch` stores for FTS5 (tests rely on this); no JSONGenerator → `Plan` errors clearly, `LLMRefiner` passes through; unparsed dump statements surfaced in `Report.UnparsedStatements`.
- **Type consistency:** `ragChunk`/`kgSink`/`ragSink` lowercase internal; `mapRAG`/`mapTriples` signatures match sink `add` calls; `graph.RDFTriple` + `graph.NewIRI/NewLiteral` used consistently; `cortexdb.KnowledgeGraphUpsertRequest{Triples}` and `*KnowledgeGraphUpsertResponse{Count}` match verified API.
- **Verification flags:** three steps explicitly say to confirm a real signature before coding (`SearchTextOnly`/`TextSearchOptions` in T7; `cortexdb.ToolDefinition` fields and graphflow schema helpers in T11). These are real APIs but their exact option-struct shapes should be eyeballed at implementation time.
- **Deferred (YAGNI, noted in-plan):** per-row RAG metadata fidelity (InsertTextBatch shares one metadata map); live-DB Source adapter (users implement `Source`); CLI (`cmd/cortexdb-import`).
