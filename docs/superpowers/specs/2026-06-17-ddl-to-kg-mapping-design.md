# DDL → Knowledge-Graph Mapping — Design

Date: 2026-06-17
Status: draft (pending review)

## Goal

Turn raw SQL **DDL** (`CREATE TABLE` statements) into an `importflow.MappingPlan`
**deterministically** — tables become entity classes, primary keys become entity
IDs, foreign keys become relations, and columns become RAG content + entity
properties. The result drops straight into `importflow.Run` / `connector_run` to
build a knowledge graph, with no LLM required. Exposed as an MCP tool so an agent
can paste a schema and get back a reviewable KG structure.

This is the deterministic counterpart to the existing LLM `MappingInferer`
(`pkg/importflow/infer.go`): same output type (`MappingPlan`), no model, derived
purely from declared keys.

## Where it lives

`pkg/importflow` — it already owns `MappingPlan`/`EntityMap`/`RelationMap`
(`plan.go`) and a `CREATE TABLE` column parser (`source_dump.go`). New code:

- `pkg/importflow/ddl.go` — DDL parser (`ParseDDL`) + mapper (`MappingFromDDL`).
- `pkg/importflow/ddl_test.go` — parser + mapper unit tests, e2e test.
- extend `pkg/importflow/toolbox.go` — add the `importflow_ddl_plan` tool.

## Parsed schema (intermediate type)

```go
// DDLColumn is one declared column.
type DDLColumn struct {
    Name string
    Type string // normalized: "text"/"integer"/"number"/"timestamp"/"" (reuses connector-style mapping)
}

// DDLForeignKey is one FK: Column in this table references RefTable(RefColumn).
type DDLForeignKey struct {
    Column   string
    RefTable string
    RefColumn string
}

// DDLTable is one parsed CREATE TABLE.
type DDLTable struct {
    Name        string
    Columns     []DDLColumn
    PrimaryKey  []string // column name(s); may be empty or composite
    ForeignKeys []DDLForeignKey
}
```

### Parser (`ParseDDL(ddl string) ([]DDLTable, error)`)

Parses a practical subset of Postgres/MySQL `CREATE TABLE`:

- Splits the input on `;`, processes each `CREATE TABLE [IF NOT EXISTS] <name> (...)`.
- Identifier quoting handled for both dialects: `"name"`, `` `name` ``, or bare.
  Schema-qualified names (`public.orders`) reduce to the table name (`orders`).
- Inside the parens, each comma-separated item is either a **column def** or a
  **table-level constraint**:
  - column: `<col> <type> [modifiers...]`. Capture name + type. Detect inline
    `PRIMARY KEY` → add to `PrimaryKey`. Detect inline
    `REFERENCES <reftable>(<refcol>)` → add a `ForeignKey{col, reftable, refcol}`.
  - `PRIMARY KEY (a, b)` → set `PrimaryKey = [a, b]`.
  - `FOREIGN KEY (col) REFERENCES reftable(refcol)` → add a `ForeignKey`.
  - other constraints (`UNIQUE`, `CHECK`, `CONSTRAINT <name> ...` wrappers) are
    skipped, except a `CONSTRAINT <name> FOREIGN KEY (...) REFERENCES ...` whose
    inner FK is captured.
- Commas inside type parens (`numeric(10,2)`, `varchar(255)`) must not split a
  column — the splitter tracks paren depth.
- Unparseable statements are skipped (not fatal); `ParseDDL` returns the tables it
  understood. (Mirrors `SQLDumpSource.Unparsed()` tolerance.)
- Type normalization reuses the same vocabulary as the connector
  (`text`/`integer`/`number`/`timestamp`/`""`) so downstream code is consistent.

## DDL → MappingPlan rules (`MappingFromDDL`)

```go
type DDLMappingOptions struct {
    // RelationStyle controls the relation predicate derived from a foreign key.
    //   "column"  (default): strip a trailing _id from the FK column → predicate
    //                        (customer_id → "customer"); fallback "references_<reftable>".
    //   "reftable": predicate = "references_<reftable>".
    RelationStyle string
    // IncludeRAG / IncludeKG toggle each facet (both default true).
    IncludeRAG bool
    IncludeKG  bool
}

func MappingFromDDL(ddl string, opts DDLMappingOptions) (MappingPlan, []DDLTable, error)
```

For each parsed table `T`:

- **Key resolution.** `idCol = T.PrimaryKey[0]` when the PK is a single column.
  Composite or missing PK → `idCol = ""` (see edge cases) and a note is attached.
- **RAG** (when `IncludeRAG`): `RAGPlan{IDColumn: idCol, ContentTmpl: tmpl}` where
  `tmpl` joins, space-separated, every column that is **not** a foreign-key column
  and **not** the PK, as `"{col}"` (e.g. `"{name} {city} {status}"`). If that set
  is empty, fall back to all columns.
- **KG** (when `IncludeKG`): a `KGPlan` containing
  - the table's own entity: `EntityMap{Ref: T.Name, Type: typeName(T.Name),
    IDTmpl: "{"+idCol+"}", Props: scalarProps}` where `scalarProps` = non-FK,
    non-PK columns, and `typeName` singularizes + TitleCases (`orders` → `Order`,
    `customers` → `Customer`; naive: strip one trailing `s`).
  - per foreign key `fk` (`T.fk.Column → fk.RefTable(fk.RefColumn)`): a referenced
    entity `EntityMap{Ref: fk.RefTable, Type: typeName(fk.RefTable),
    IDTmpl: "{"+fk.Column+"}"}` (no props — it's a reference), plus a
    `RelationMap{Subject: T.Name, Predicate: predicate(fk, opts), Object: fk.RefTable}`.
  - `predicate(fk, opts)`: per `RelationStyle` above.
- A table with no resolvable single-column key: RAG keeps `IDColumn=""`
  (importflow synthesizes `table:row`); its KG **own-entity is skipped** (can't
  mint a stable IRI), but it can still appear as an FK **target** from other
  tables. A human-readable note is returned (see "Notes" output).

Entity-IRI shape and relation predicate match `pkg/importflow/mapper.go`
exactly, so the produced plan behaves identically to a hand-written one.

### Worked example

DDL:
```sql
CREATE TABLE customers (id INT PRIMARY KEY, name TEXT, city TEXT);
CREATE TABLE orders (
  id INT PRIMARY KEY,
  customer_id INT REFERENCES customers(id),
  product TEXT, amount NUMERIC, status TEXT
);
```
→ MappingPlan:
- `customers`: RAG `{name} {city}` id=`id`; KG entity `Customer` id `{id}` props [name, city].
- `orders`: RAG `{product} {amount} {status}` id=`id`; KG entities `Order` id `{id}`
  props [product, amount, status] + `Customer` id `{customer_id}`; relation
  `Order ──customer──▶ Customer` (RelationStyle "column").

## MCP tool

Add to `pkg/importflow/toolbox.go` (rides the existing importflow toolbox/MCP):

- `importflow_ddl_plan` — input `{ "ddl": "<CREATE TABLE ...>", "relation_style": "column|reftable" }`;
  output `{ "mapping_plan": <MappingPlan>, "tables": [<DDLTable>], "notes": [<string>] }`.
  Read-only and LLM-free: it parses and proposes; the caller reviews, edits, then
  runs `importflow_run` / `connector_run` with the plan.

`Toolbox.Definitions()` gains the entry and `Toolbox.Call` a `case`.

## Errors and edge cases (explicit)

- Empty / no `CREATE TABLE` found → return an empty plan + an error
  `"importflow: no CREATE TABLE statements parsed"`.
- FK referencing a table not present in the DDL → still emit the relation +
  referenced entity (the target may live in another import); add a note.
- Composite PK → `IDColumn=""`, own-entity skipped, note: `"table X has a composite
  primary key; using table:row ids and skipping its KG entity"`.
- Self-referential FK (e.g. `manager_id → employees(id)`) → ref entity uses the
  same Type; relation Subject==Object Type is allowed.
- Duplicate FK columns / multiple FKs to the same table → each produces its own
  ref entity ref (suffix the Ref to keep them distinct, e.g. `customers#2`).

## Testing

- **Parser** (`ParseDDL`): table-driven over Postgres + MySQL fixtures — inline
  PK + inline `REFERENCES`; table-level `PRIMARY KEY (...)` + `FOREIGN KEY (...)
  REFERENCES`; `numeric(10,2)` (no comma-split); quoted/schema-qualified names;
  `IF NOT EXISTS`. Assert `DDLTable` columns/PK/FK.
- **Mapper** (`MappingFromDDL`): the worked example → assert exact MappingPlan
  (entity Types, IDTmpl, Props, relation Subject/Predicate/Object, RAG IDColumn +
  ContentTmpl). Composite-PK and no-PK notes. `RelationStyle` variants.
- **e2e**: DDL → `MappingFromDDL` → feed rows from a `NewCSVSource` matching the
  schema → `importflow.New(db).Run` → SPARQL asserts the FK relation edge exists
  and the entity IRIs are present. No external services (deterministic, CI-safe).
- **Tool**: `importflow_ddl_plan` returns a non-empty MappingPlan + tables for the
  worked-example DDL.

## Non-goals (v1)

- No ALTER TABLE, no views, no indexes, no `CREATE TABLE AS SELECT`.
- No LLM assistance (that path already exists via `MappingInferer`); a future
  option could post-process the deterministic plan with an LLM for better
  predicate names, out of scope here.
- No RDFS ontology / SHACL emission (the chosen output is `MappingPlan`); the KG
  built from the plan can still be reasoned over with the existing RDFS/SHACL APIs.

## Resolved decisions

- Output: **`importflow.MappingPlan`** (actionable; feeds importflow/connector_run).
- **Deterministic**, no LLM.
- Lives in **`pkg/importflow`** (`ddl.go`), tool on importflow's toolbox.
- Relation predicate default: **strip `_id` from the FK column** (`customer_id` →
  `customer`), fallback `references_<reftable>`; `RelationStyle` switches to
  `reftable`.
- Type naming: naive singularize + TitleCase.
