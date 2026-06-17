# DDL → Knowledge-Graph Mapping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `importflow.ParseDDL` + `importflow.MappingFromDDL` (deterministic DDL → `MappingPlan`) and an `importflow_ddl_plan` MCP tool, so a SQL schema (`CREATE TABLE` with PK/FK) becomes a reviewable knowledge-graph structure with no LLM.

**Architecture:** A small pure-Go DDL parser extracts tables/columns/PK/FK; a deterministic mapper turns each table into an `EntityMap` (PK → IDTmpl), each foreign key into a referenced `EntityMap` + a `RelationMap`, and non-key columns into RAG content/props. The tool rides importflow's existing toolbox/MCP surface.

**Tech Stack:** Go 1.25, `pkg/importflow` (`MappingPlan`/`EntityMap`/`RelationMap`/`RAGPlan`/`KGPlan` in `plan.go`, `Toolbox` in `toolbox.go`), `pkg/cortexdb` (`ToolDefinition`), `pkg/graph` (SPARQL, for the e2e). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-06-17-ddl-to-kg-mapping-design.md`

**Reused existing types (do not redefine):**
- `importflow.MappingPlan{Tables map[string]TablePlan}`, `TablePlan{Skip bool; RAG *RAGPlan; KG *KGPlan}`.
- `RAGPlan{Namespace, ContentTmpl, IDColumn string; Metadata []string; Refine bool}`.
- `KGPlan{Entities []EntityMap; Relations []RelationMap; TextExtract []TextExtract}`.
- `EntityMap{Ref, Type, IDTmpl, LabelTmpl string; Props []string}`; `RelationMap{Subject, Predicate, Object string}`.
- `importflow.NewCSVSource(io.Reader, CSVOptions{Table})`, `importflow.New(db).Run(ctx, src, MappingPlan)`.
- `Toolbox` (`toolbox.go`) with `Definitions() []cortexdb.ToolDefinition`, `Call(ctx, name, json.RawMessage)`, and helpers `ifObjectSchema`, `ifStringSchema`, `ifEnumSchema`.
- `cortexdb.ToolDefinition{Name, Description string; InputSchema map[string]any}`.

---

### Task 1: DDL parser — types + ParseDDL

**Files:**
- Create: `pkg/importflow/ddl.go`
- Test: `pkg/importflow/ddl_test.go`

- [ ] **Step 1: Write the failing test** in `pkg/importflow/ddl_test.go`

```go
package importflow

import "testing"

func TestParseDDL(t *testing.T) {
	ddl := `
CREATE TABLE customers (
  id INT PRIMARY KEY,
  name TEXT,
  city VARCHAR(64)
);
CREATE TABLE IF NOT EXISTS public.orders (
  id INT PRIMARY KEY,
  customer_id INT REFERENCES customers(id),
  amount NUMERIC(10,2),
  status TEXT,
  FOREIGN KEY (status) REFERENCES statuses(code)
);
`
	tables, err := ParseDDL(ddl)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 {
		t.Fatalf("want 2 tables, got %d", len(tables))
	}
	byName := map[string]DDLTable{}
	for _, tb := range tables {
		byName[tb.Name] = tb
	}
	cust := byName["customers"]
	if len(cust.Columns) != 3 || cust.Columns[0].Name != "id" {
		t.Fatalf("customers columns: %+v", cust.Columns)
	}
	if len(cust.PrimaryKey) != 1 || cust.PrimaryKey[0] != "id" {
		t.Fatalf("customers pk: %+v", cust.PrimaryKey)
	}
	ord := byName["orders"] // schema prefix stripped
	if ord.Name != "orders" {
		t.Fatalf("orders name: %q", ord.Name)
	}
	// amount NUMERIC(10,2) must not be split into two columns
	cols := map[string]string{}
	for _, c := range ord.Columns {
		cols[c.Name] = c.Type
	}
	if _, ok := cols["amount"]; !ok || len(ord.Columns) != 4 {
		t.Fatalf("orders columns: %+v", ord.Columns)
	}
	// two FKs: inline customer_id->customers.id, table-level status->statuses.code
	if len(ord.ForeignKeys) != 2 {
		t.Fatalf("orders fks: %+v", ord.ForeignKeys)
	}
	fk := map[string]DDLForeignKey{}
	for _, f := range ord.ForeignKeys {
		fk[f.Column] = f
	}
	if fk["customer_id"].RefTable != "customers" || fk["customer_id"].RefColumn != "id" {
		t.Fatalf("customer_id fk: %+v", fk["customer_id"])
	}
	if fk["status"].RefTable != "statuses" || fk["status"].RefColumn != "code" {
		t.Fatalf("status fk: %+v", fk["status"])
	}
}

func TestParseDDLMySQLBackticksAndTableLevelPK(t *testing.T) {
	ddl := "CREATE TABLE `orders` (`id` INT, `customer_id` INT, PRIMARY KEY (`id`), FOREIGN KEY (`customer_id`) REFERENCES `customers`(`id`));"
	tables, err := ParseDDL(ddl)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Name != "orders" {
		t.Fatalf("tables: %+v", tables)
	}
	if len(tables[0].PrimaryKey) != 1 || tables[0].PrimaryKey[0] != "id" {
		t.Fatalf("pk: %+v", tables[0].PrimaryKey)
	}
	if len(tables[0].ForeignKeys) != 1 || tables[0].ForeignKeys[0].RefTable != "customers" {
		t.Fatalf("fk: %+v", tables[0].ForeignKeys)
	}
}

func TestParseDDLNoCreateTable(t *testing.T) {
	if _, err := ParseDDL("SELECT 1;"); err == nil {
		t.Fatal("expected error when no CREATE TABLE present")
	}
}
```

- [ ] **Step 2: Run, verify fail** — `go test ./pkg/importflow -run TestParseDDL` → FAIL (undefined).

- [ ] **Step 3: Implement `pkg/importflow/ddl.go`** (parser only; the mapper is Task 2)

```go
package importflow

import (
	"fmt"
	"strings"
)

// DDLColumn is one declared column.
type DDLColumn struct {
	Name string
	Type string // normalized: text/integer/number/timestamp/"" (unknown)
}

// DDLForeignKey is one foreign key: Column references RefTable(RefColumn).
type DDLForeignKey struct {
	Column    string
	RefTable  string
	RefColumn string
}

// DDLTable is one parsed CREATE TABLE statement.
type DDLTable struct {
	Name        string
	Columns     []DDLColumn
	PrimaryKey  []string
	ForeignKeys []DDLForeignKey
}

// ParseDDL parses a practical subset of Postgres/MySQL CREATE TABLE statements
// (columns, PRIMARY KEY, FOREIGN KEY/REFERENCES). Statements it cannot parse are
// skipped; it errors only when no CREATE TABLE was found at all.
func ParseDDL(ddl string) ([]DDLTable, error) {
	var tables []DDLTable
	for _, stmt := range strings.Split(ddl, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(stmt), "CREATE TABLE") {
			continue
		}
		if tb, ok := parseCreateTable(stmt); ok {
			tables = append(tables, tb)
		}
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("importflow: no CREATE TABLE statements parsed")
	}
	return tables, nil
}

func parseCreateTable(stmt string) (DDLTable, bool) {
	open := strings.Index(stmt, "(")
	close := strings.LastIndex(stmt, ")")
	if open < 0 || close <= open {
		return DDLTable{}, false
	}
	header := strings.TrimSpace(stmt[:open]) // CREATE TABLE [IF NOT EXISTS] name
	body := stmt[open+1 : close]

	// table name = last whitespace-separated token of the header, unquoted.
	fields := strings.Fields(header)
	if len(fields) == 0 {
		return DDLTable{}, false
	}
	name := unquoteIdent(fields[len(fields)-1])
	tb := DDLTable{Name: name}

	for _, item := range splitTopLevelCommas(body) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		upper := strings.ToUpper(item)
		switch {
		case strings.HasPrefix(upper, "PRIMARY KEY"):
			tb.PrimaryKey = identsInParens(item)
		case strings.HasPrefix(upper, "FOREIGN KEY"):
			if fk, ok := parseForeignKey(item); ok {
				tb.ForeignKeys = append(tb.ForeignKeys, fk)
			}
		case strings.HasPrefix(upper, "CONSTRAINT"):
			if idx := strings.Index(upper, "FOREIGN KEY"); idx >= 0 {
				if fk, ok := parseForeignKey(item[idx:]); ok {
					tb.ForeignKeys = append(tb.ForeignKeys, fk)
				}
			} else if idx := strings.Index(upper, "PRIMARY KEY"); idx >= 0 {
				tb.PrimaryKey = identsInParens(item[idx:])
			}
		case strings.HasPrefix(upper, "UNIQUE"), strings.HasPrefix(upper, "CHECK"),
			strings.HasPrefix(upper, "KEY "), strings.HasPrefix(upper, "INDEX "):
			// table-level constraint we don't model; skip
		default:
			// column definition
			col, pk, fk, ok := parseColumnDef(item)
			if !ok {
				continue
			}
			tb.Columns = append(tb.Columns, col)
			if pk {
				tb.PrimaryKey = append(tb.PrimaryKey, col.Name)
			}
			if fk != nil {
				fk.Column = col.Name
				tb.ForeignKeys = append(tb.ForeignKeys, *fk)
			}
		}
	}
	return tb, true
}

// parseColumnDef parses "<name> <type> [modifiers]" and detects inline
// PRIMARY KEY and REFERENCES.
func parseColumnDef(item string) (col DDLColumn, isPK bool, fk *DDLForeignKey, ok bool) {
	fields := strings.Fields(item)
	if len(fields) < 1 {
		return DDLColumn{}, false, nil, false
	}
	col.Name = unquoteIdent(fields[0])
	if len(fields) >= 2 {
		col.Type = normalizeDDLType(fields[1])
	}
	upper := strings.ToUpper(item)
	if strings.Contains(upper, "PRIMARY KEY") {
		isPK = true
	}
	if idx := strings.Index(upper, "REFERENCES"); idx >= 0 {
		if f, fok := parseReferences(item[idx:]); fok {
			fk = &f
		}
	}
	return col, isPK, fk, true
}

// parseForeignKey parses "FOREIGN KEY (col) REFERENCES reftable(refcol)".
func parseForeignKey(item string) (DDLForeignKey, bool) {
	cols := identsInParens(item) // first parens = local column(s)
	if len(cols) == 0 {
		return DDLForeignKey{}, false
	}
	upper := strings.ToUpper(item)
	idx := strings.Index(upper, "REFERENCES")
	if idx < 0 {
		return DDLForeignKey{}, false
	}
	ref, ok := parseReferences(item[idx:])
	if !ok {
		return DDLForeignKey{}, false
	}
	ref.Column = cols[0]
	return ref, true
}

// parseReferences parses "REFERENCES reftable(refcol)" → {RefTable, RefColumn}.
func parseReferences(item string) (DDLForeignKey, bool) {
	rest := strings.TrimSpace(item[len("REFERENCES"):])
	p := strings.Index(rest, "(")
	if p < 0 {
		return DDLForeignKey{}, false
	}
	refTable := unquoteIdent(strings.TrimSpace(rest[:p]))
	cols := identsInParens(rest[p:])
	if refTable == "" || len(cols) == 0 {
		return DDLForeignKey{}, false
	}
	return DDLForeignKey{RefTable: refTable, RefColumn: cols[0]}, true
}

// identsInParens returns the unquoted identifiers inside the FIRST (...) group.
func identsInParens(s string) []string {
	open := strings.Index(s, "(")
	if open < 0 {
		return nil
	}
	depth, end := 0, -1
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s[open+1:end], ",") {
		if id := unquoteIdent(strings.TrimSpace(part)); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// splitTopLevelCommas splits on commas that are NOT inside parentheses.
func splitTopLevelCommas(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

// unquoteIdent strips quoting (" ` [ ]) and any schema prefix (public.orders).
func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"[]")
	if dot := strings.LastIndex(s, "."); dot >= 0 {
		s = s[dot+1:]
		s = strings.Trim(s, "`\"[]")
	}
	// drop a trailing type-size paren if it slipped in (defensive)
	if p := strings.Index(s, "("); p >= 0 {
		s = s[:p]
	}
	return strings.TrimSpace(s)
}

// normalizeDDLType maps a SQL type token onto importflow's small vocabulary.
func normalizeDDLType(t string) string {
	t = strings.ToLower(t)
	switch {
	case strings.Contains(t, "char"), strings.Contains(t, "text"), strings.Contains(t, "uuid"),
		strings.Contains(t, "json"), strings.Contains(t, "enum"):
		return "text"
	case strings.Contains(t, "int"), strings.Contains(t, "serial"):
		return "integer"
	case strings.Contains(t, "numeric"), strings.Contains(t, "decimal"), strings.Contains(t, "real"),
		strings.Contains(t, "double"), strings.Contains(t, "float"):
		return "number"
	case strings.Contains(t, "time"), strings.Contains(t, "date"):
		return "timestamp"
	default:
		return ""
	}
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/importflow -run TestParseDDL -v`
- [ ] **Step 5: Commit** — `git add pkg/importflow/ddl.go pkg/importflow/ddl_test.go && git commit -m "feat(importflow): ParseDDL — parse CREATE TABLE columns, primary keys, foreign keys"`

(No co-author trailer. Exact message.)

---

### Task 2: Deterministic mapper — MappingFromDDL

**Files:**
- Modify: `pkg/importflow/ddl.go` (append the mapper)
- Test: `pkg/importflow/ddl_test.go` (append)

- [ ] **Step 1: Write the failing test** (append to `ddl_test.go`)

```go
func TestMappingFromDDL(t *testing.T) {
	ddl := `
CREATE TABLE customers (id INT PRIMARY KEY, name TEXT, city TEXT);
CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT REFERENCES customers(id), product TEXT, amount NUMERIC, status TEXT);
`
	plan, tables, err := MappingFromDDL(ddl, DDLMappingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 {
		t.Fatalf("tables: %d", len(tables))
	}

	cust := plan.Tables["customers"]
	if cust.RAG == nil || cust.RAG.IDColumn != "id" {
		t.Fatalf("customers RAG: %+v", cust.RAG)
	}
	if cust.KG == nil || len(cust.KG.Entities) != 1 ||
		cust.KG.Entities[0].Type != "Customer" || cust.KG.Entities[0].IDTmpl != "{id}" {
		t.Fatalf("customers KG entity: %+v", cust.KG)
	}

	ord := plan.Tables["orders"]
	if ord.RAG.IDColumn != "id" {
		t.Fatalf("orders RAG id: %+v", ord.RAG)
	}
	// own entity Order + referenced entity Customer
	types := map[string]EntityMap{}
	for _, e := range ord.KG.Entities {
		types[e.Type] = e
	}
	if types["Order"].IDTmpl != "{id}" {
		t.Fatalf("Order entity: %+v", types["Order"])
	}
	if types["Customer"].IDTmpl != "{customer_id}" {
		t.Fatalf("ref Customer entity: %+v", types["Customer"])
	}
	// relation Order --customer--> Customer (RelationStyle column: strip _id)
	if len(ord.KG.Relations) != 1 {
		t.Fatalf("orders relations: %+v", ord.KG.Relations)
	}
	rel := ord.KG.Relations[0]
	if rel.Subject != "orders" || rel.Predicate != "customer" || rel.Object != "customers" {
		t.Fatalf("relation: %+v", rel)
	}
	// FK column is not in the Order entity props (it's modeled as a relation)
	for _, p := range types["Order"].Props {
		if p == "customer_id" {
			t.Fatal("customer_id should not be an Order prop")
		}
	}
}

func TestMappingFromDDLRelationStyleReftable(t *testing.T) {
	ddl := `CREATE TABLE a (id INT PRIMARY KEY, b_ref INT REFERENCES bs(id));`
	plan, _, err := MappingFromDDL(ddl, DDLMappingOptions{RelationStyle: "reftable"})
	if err != nil {
		t.Fatal(err)
	}
	rel := plan.Tables["a"].KG.Relations[0]
	if rel.Predicate != "references_bs" {
		t.Fatalf("predicate: %q", rel.Predicate)
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Append the mapper to `pkg/importflow/ddl.go`**

```go
// DDLMappingOptions tunes MappingFromDDL.
type DDLMappingOptions struct {
	// RelationStyle picks the relation predicate from a foreign key:
	//   "" / "column": strip a trailing _id from the FK column (customer_id -> "customer"),
	//                  fallback "references_<reftable>".
	//   "reftable":    predicate = "references_<reftable>".
	RelationStyle string
	IncludeRAG    *bool // default true
	IncludeKG     *bool // default true
}

// MappingFromDDL parses DDL and derives a deterministic MappingPlan: tables ->
// entities (PK -> id), foreign keys -> referenced entities + relations, non-key
// columns -> RAG content + entity props. It also returns the parsed tables.
func MappingFromDDL(ddl string, opts DDLMappingOptions) (MappingPlan, []DDLTable, error) {
	tables, err := ParseDDL(ddl)
	if err != nil {
		return MappingPlan{}, nil, err
	}
	includeRAG := opts.IncludeRAG == nil || *opts.IncludeRAG
	includeKG := opts.IncludeKG == nil || *opts.IncludeKG

	plan := MappingPlan{Tables: map[string]TablePlan{}}
	for _, tb := range tables {
		idCol := ""
		if len(tb.PrimaryKey) == 1 {
			idCol = tb.PrimaryKey[0]
		}
		fkCols := map[string]bool{}
		for _, fk := range tb.ForeignKeys {
			fkCols[fk.Column] = true
		}
		pkCols := map[string]bool{}
		for _, c := range tb.PrimaryKey {
			pkCols[c] = true
		}

		var tp TablePlan
		if includeRAG {
			tp.RAG = &RAGPlan{IDColumn: idCol, ContentTmpl: ragTemplate(tb, fkCols, pkCols)}
		}
		if includeKG {
			tp.KG = ddlKGPlan(tb, idCol, fkCols, pkCols, opts)
		}
		plan.Tables[tb.Name] = tp
	}
	return plan, tables, nil
}

// ragTemplate joins non-PK, non-FK columns as "{col}"; falls back to all columns.
func ragTemplate(tb DDLTable, fkCols, pkCols map[string]bool) string {
	var parts []string
	for _, c := range tb.Columns {
		if pkCols[c.Name] || fkCols[c.Name] {
			continue
		}
		parts = append(parts, "{"+c.Name+"}")
	}
	if len(parts) == 0 {
		for _, c := range tb.Columns {
			parts = append(parts, "{"+c.Name+"}")
		}
	}
	return strings.Join(parts, " ")
}

// ddlKGPlan builds the KG plan: the table's own entity (if it has a single-column
// PK) plus one referenced entity + relation per foreign key.
func ddlKGPlan(tb DDLTable, idCol string, fkCols, pkCols map[string]bool, opts DDLMappingOptions) *KGPlan {
	kg := &KGPlan{}
	used := map[string]bool{} // entity refs already taken (avoid self-ref/dup collisions)

	if idCol != "" {
		var props []string
		for _, c := range tb.Columns {
			if pkCols[c.Name] || fkCols[c.Name] {
				continue
			}
			props = append(props, c.Name)
		}
		kg.Entities = append(kg.Entities, EntityMap{
			Ref: tb.Name, Type: typeName(tb.Name), IDTmpl: "{" + idCol + "}", Props: props,
		})
		used[tb.Name] = true
	}

	for _, fk := range tb.ForeignKeys {
		ref := uniqueRef(fk.RefTable, used)
		kg.Entities = append(kg.Entities, EntityMap{
			Ref: ref, Type: typeName(fk.RefTable), IDTmpl: "{" + fk.Column + "}",
		})
		// Subject is the table's own entity ref when it exists, else the table name.
		subject := tb.Name
		kg.Relations = append(kg.Relations, RelationMap{
			Subject: subject, Predicate: predicateFor(fk, opts), Object: ref,
		})
	}
	if len(kg.Entities) == 0 && len(kg.Relations) == 0 {
		return nil
	}
	return kg
}

func uniqueRef(base string, used map[string]bool) string {
	ref := base
	for n := 2; used[ref]; n++ {
		ref = fmt.Sprintf("%s#%d", base, n)
	}
	used[ref] = true
	return ref
}

func predicateFor(fk DDLForeignKey, opts DDLMappingOptions) string {
	if strings.EqualFold(opts.RelationStyle, "reftable") {
		return "references_" + fk.RefTable
	}
	// "column" (default): strip a trailing _id from the FK column.
	base := fk.Column
	for _, suf := range []string{"_id", "_ID", "Id", "ID"} {
		if strings.HasSuffix(base, suf) && len(base) > len(suf) {
			return base[:len(base)-len(suf)]
		}
	}
	if base != "" {
		return base
	}
	return "references_" + fk.RefTable
}

// typeName singularizes (naive: strip one trailing 's') and TitleCases a table
// name: orders -> Order, customers -> Customer, statuses -> Statuse (acceptable).
func typeName(table string) string {
	t := table
	if strings.HasSuffix(t, "s") && len(t) > 1 {
		t = t[:len(t)-1]
	}
	if t == "" {
		return table
	}
	return strings.ToUpper(t[:1]) + t[1:]
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/importflow -run TestMappingFromDDL -v`. Then `go test ./pkg/importflow` (whole package — no regressions).
- [ ] **Step 5: Commit** — `git commit -am "feat(importflow): MappingFromDDL — deterministic DDL -> MappingPlan (PK->id, FK->relations)"`

---

### Task 3: importflow_ddl_plan MCP tool

**Files:**
- Modify: `pkg/importflow/toolbox.go`
- Test: `pkg/importflow/toolbox_ddl_test.go`

- [ ] **Step 1: Write the failing test** in `pkg/importflow/toolbox_ddl_test.go`

```go
package importflow

import (
	"context"
	"encoding/json"
	"testing"
)

func TestToolboxDDLPlan(t *testing.T) {
	tb := NewToolbox(nil) // ddl_plan needs no Importer
	names := map[string]bool{}
	for _, d := range tb.Definitions() {
		names[d.Name] = true
	}
	if !names["importflow_ddl_plan"] {
		t.Fatal("missing importflow_ddl_plan definition")
	}

	in := json.RawMessage(`{"ddl":"CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT REFERENCES customers(id), total NUMERIC);"}`)
	out, err := tb.Call(context.Background(), "importflow_ddl_plan", in)
	if err != nil {
		t.Fatal(err)
	}
	res, ok := out.(ddlPlanResult)
	if !ok {
		t.Fatalf("unexpected result type %T", out)
	}
	if len(res.MappingPlan.Tables) != 1 {
		t.Fatalf("plan tables: %+v", res.MappingPlan.Tables)
	}
	if len(res.Tables) != 1 || res.Tables[0].Name != "orders" {
		t.Fatalf("parsed tables: %+v", res.Tables)
	}
	if rel := res.MappingPlan.Tables["orders"].KG.Relations; len(rel) != 1 || rel[0].Object != "customers" {
		t.Fatalf("relation: %+v", rel)
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Edit `pkg/importflow/toolbox.go`** — add the definition, the dispatch case, the input/result types, and the handler.

Add to the slice returned by `Definitions()` (after the `importflow_run` entry, before the closing `}`):

```go
		{
			Name:        "importflow_ddl_plan",
			Description: "Derive a knowledge-graph MappingPlan from SQL DDL (CREATE TABLE with primary/foreign keys), deterministically and without an LLM. Returns a reviewable plan plus the parsed tables.",
			InputSchema: ifObjectSchema(
				[]string{"ddl"},
				map[string]any{
					"ddl":            ifStringSchema("SQL DDL: one or more CREATE TABLE statements."),
					"relation_style": ifEnumSchema("How to name relation predicates from foreign keys.", "column", "reftable"),
				},
			),
		},
```

Add the dispatch case in `Call` (in the `switch name`):

```go
	case "importflow_ddl_plan":
		return t.callDDLPlan(input)
```

Add the types + handler at the end of the file:

```go
type ddlPlanInput struct {
	DDL           string `json:"ddl"`
	RelationStyle string `json:"relation_style"`
}

type ddlPlanResult struct {
	MappingPlan MappingPlan `json:"mapping_plan"`
	Tables      []DDLTable  `json:"tables"`
	Notes       []string    `json:"notes,omitempty"`
}

func (t *Toolbox) callDDLPlan(input json.RawMessage) (any, error) {
	var in ddlPlanInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	plan, tables, err := MappingFromDDL(in.DDL, DDLMappingOptions{RelationStyle: in.RelationStyle})
	if err != nil {
		return nil, err
	}
	var notes []string
	for _, tb := range tables {
		if len(tb.PrimaryKey) == 0 {
			notes = append(notes, fmt.Sprintf("table %q has no primary key; using synthesized table:row ids and no KG entity for it", tb.Name))
		} else if len(tb.PrimaryKey) > 1 {
			notes = append(notes, fmt.Sprintf("table %q has a composite primary key; using synthesized table:row ids and no KG entity for it", tb.Name))
		}
	}
	return ddlPlanResult{MappingPlan: plan, Tables: tables, Notes: notes}, nil
}
```

Note: `callDDLPlan` does not use the `ctx` argument (parsing is pure), so it takes only `input` — matching the test call. The `fmt` import is already present in `toolbox.go`.

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/importflow -run TestToolboxDDLPlan -v`. Then `go test ./pkg/importflow -race`.
- [ ] **Step 5: Commit** — `git commit -am "feat(importflow): importflow_ddl_plan tool — DDL -> reviewable MappingPlan over MCP"`

---

### Task 4: End-to-end — DDL plan builds a real knowledge graph

**Files:**
- Test: `pkg/importflow/ddl_e2e_test.go`

- [ ] **Step 1: Write the test** — DDL → MappingFromDDL → feed matching CSV rows → `importflow.New(db).Run` → SPARQL confirms the FK relation edge + entities exist.

```go
package importflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestDDLToKnowledgeGraphE2E(t *testing.T) {
	ctx := context.Background()
	ddl := `
CREATE TABLE customers (id INT PRIMARY KEY, name TEXT, city TEXT);
CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT REFERENCES customers(id), product TEXT);
`
	plan, _, err := MappingFromDDL(ddl, DDLMappingOptions{})
	if err != nil {
		t.Fatal(err)
	}

	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Feed rows whose columns match each table; import one table at a time
	// (CSV source carries one logical table).
	custCSV := "id,name,city\n1,Alice,Chengdu\n"
	custSrc, _ := NewCSVSource(strings.NewReader(custCSV), CSVOptions{Table: "customers"})
	if _, err := New(db).Run(ctx, custSrc, MappingPlan{Tables: map[string]TablePlan{"customers": plan.Tables["customers"]}}); err != nil {
		t.Fatalf("import customers: %v", err)
	}
	custSrc.Close()

	ordCSV := "id,customer_id,product\n100,1,Pro Plan\n"
	ordSrc, _ := NewCSVSource(strings.NewReader(ordCSV), CSVOptions{Table: "orders"})
	if _, err := New(db).Run(ctx, ordSrc, MappingPlan{Tables: map[string]TablePlan{"orders": plan.Tables["orders"]}}); err != nil {
		t.Fatalf("import orders: %v", err)
	}
	ordSrc.Close()

	// The FK became a graph edge: Order:100 --customer--> Customer:1
	q := `ASK { <urn:cortexdb:Order:100> <urn:cortexdb:rel:customer> <urn:cortexdb:Customer:1> . }`
	res, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Result.Boolean {
		t.Fatalf("expected the FK relation edge to exist; ASK was false (result: %+v)", res.Result)
	}
}
```

- [ ] **Step 2: Run, verify pass** — `go test ./pkg/importflow -run TestDDLToKnowledgeGraphE2E -v`.

  VERIFY-AT-IMPL: confirm the SPARQL `ASK` result field is `res.Result.Boolean` (read `pkg/graph` `SPARQLResult`); if the boolean field has a different name, use it. Also confirm the relation IRI is `urn:cortexdb:rel:customer` and entity IRIs are `urn:cortexdb:Order:100` / `urn:cortexdb:Customer:1` (per `pkg/importflow/mapper.go` `entityIRI` and the `urn:cortexdb:rel:` prefix). If `ASK`/`.Boolean` is not supported, replace with a `SELECT` that binds the object and assert one row equals `urn:cortexdb:Customer:1`.

- [ ] **Step 3: Commit** — `git commit -am "test(importflow): e2e DDL -> MappingPlan -> knowledge graph (FK becomes a relation edge)"`

---

### Task 5: Docs + verification

**Files:**
- Modify: `README.md`, `README_CN.md`, `SKILL.md`

- [ ] **Step 1: Docs.** In each file's ImportFlow / connector tool area, add a one-liner that `importflow.MappingFromDDL(ddl, opts)` / the `importflow_ddl_plan` tool turn `CREATE TABLE` DDL into a reviewable KG `MappingPlan` deterministically (tables→entities, PK→id, FK→relations), with no LLM. Add `importflow_ddl_plan` to the importflow tool list in `SKILL.md`. Keep all three consistent; README_CN in Chinese.
- [ ] **Step 2: Commit** — `git commit -am "docs: importflow DDL -> knowledge-graph mapping (MappingFromDDL + importflow_ddl_plan)"`

- [ ] **Step 3: Full verification:**
  - `go build ./...` → OK
  - `go test ./pkg/importflow -race` → all pass
  - `go test ./... -race` → no regressions
  - examples compile: `for d in examples/*/; do (cd "$d" && go build -o /dev/null .) || echo "FAIL $d"; done`

---

## Self-review notes

- **Spec coverage:** DDLColumn/DDLForeignKey/DDLTable + ParseDDL (T1) · MappingFromDDL rules incl. PK→id, FK→ref entity + relation, RAG content, props, predicate styles, type naming, dedup/self-ref refs (T2) · importflow_ddl_plan tool + notes for composite/no PK (T3) · e2e FK→edge (T4) · docs (T5). Non-goals (ALTER/views/LLM/RDFS-SHACL) intentionally excluded.
- **Type consistency:** `DDLTable`/`DDLColumn`/`DDLForeignKey`, `DDLMappingOptions{RelationStyle,IncludeRAG,IncludeKG}`, `MappingFromDDL(ddl,opts)(MappingPlan,[]DDLTable,error)`, `ddlPlanInput`/`ddlPlanResult` consistent across tasks. Entity/relation field names match `plan.go`.
- **Verify-at-impl markers:** (a) SPARQL `ASK`/`.Boolean` field name in T4 (fall back to SELECT if absent); (b) `NewToolbox(nil)` is acceptable because `callDDLPlan` never dereferences `t.im` — confirm at build (it doesn't). (c) confirm `entityIRI`/`rel:` prefixes in mapper.go match the T4 assertion IRIs.
