# DDL → Knowledge-Graph Mapping (LLM-enhanced) — Design

Date: 2026-06-17
Status: draft (pending review)

## Goal

An **LLM-enhanced** sibling of the deterministic `importflow.MappingFromDDL`: it
parses DDL, computes the deterministic `MappingPlan` as a baseline, then has an
LLM **refine** that baseline into a richer knowledge-graph structure. It always
falls back to the deterministic baseline when the LLM is unavailable or returns
unusable output. Exposed as an opt-in MCP tool `importflow_ddl_plan_ai`.

This builds directly on the just-shipped deterministic feature (`ParseDDL`,
`MappingFromDDL`, `DDLMappingOptions`, `importflow_ddl_plan`) and reuses the
project's LLM seam (`graphflow.JSONGenerator`) exactly like `LLMInferer` —
**no LLM SDK enters `pkg/`**.

## What the LLM adds over the deterministic version

1. **Semantic naming** — better relation predicates (`customer_id` → `placed_by`
   instead of `customer`), accurate entity types (`txn` → `Transaction` instead
   of the naive `Txn`), and human labels (`label_tmpl`).
2. **Implicit relations** — edges the foreign keys do not encode but column
   names/semantics imply (e.g. `posts.author_email` ↔ `users.email`).
3. **Free-text → TextExtract** — route long prose columns (`description`,
   `notes`, `body`) to `kg.text_extract` for in-content triple extraction,
   instead of treating them only as RAG/props.
4. **Junction-table collapse** — recognize many-to-many link tables
   (`order_items`, `user_roles`) and emit a direct relation between the two
   referenced entities rather than modeling the link table as its own entity.

## Architecture: refine the baseline (grounded + graceful)

```
ddl ──ParseDDL──▶ []DDLTable (with PK/FK)
          │
          ├─ MappingFromDDL ──▶ baseline MappingPlan (deterministic)
          │
          ▼
  prompt LLM with {parsed tables incl. PK/FK, baseline plan, the 4 goals}
          │
          ▼
  refined MappingPlan ── validate ──▶ on error/empty: return baseline
```

The LLM never starts blind: it receives the parsed schema **and** a correct
deterministic plan to improve. If the model errors, times out, or returns an
empty/invalid plan, the function returns the deterministic baseline — so it is
strictly an enhancement and never worse than the deterministic tool.

## Where it lives

`pkg/importflow` (alongside the deterministic mapper):
- `pkg/importflow/ddl_llm.go` — `MappingFromDDLWithLLM` + prompt + validation.
- `pkg/importflow/ddl_llm_test.go` — fake-generator unit tests + fallback test.
- extend `pkg/importflow/toolbox.go` — `importflow_ddl_plan_ai` tool.

Reuses (unchanged): `ParseDDL`, `MappingFromDDL`, `DDLMappingOptions`, `DDLTable`,
`MappingPlan`/`TablePlan`/`RAGPlan`/`KGPlan`/`EntityMap`/`RelationMap`/`TextExtract`
(`plan.go`), `graphflow.JSONGenerator`, the `LLMInferer` pattern (`infer.go`), and
the `Toolbox`/`Importer` (`toolbox.go`/`importer.go`).

## Library API

```go
// MappingFromDDLWithLLM parses DDL, builds the deterministic baseline plan, and
// asks the LLM to refine it (semantic naming, implicit relations, free-text
// TextExtract, junction-table collapse). It returns the refined plan, the parsed
// tables, and llmUsed=false (with the baseline plan) when the LLM is unavailable
// or returns an unusable result.
func MappingFromDDLWithLLM(
    ctx context.Context,
    ddl string,
    gen graphflow.JSONGenerator,
    opts DDLMappingOptions,
) (plan MappingPlan, tables []DDLTable, llmUsed bool, err error)
```

- `err` is non-nil only for a hard failure: empty/no `CREATE TABLE` (from
  `ParseDDL`) or a nil `gen`. An LLM call error is **not** a hard failure — it
  returns `(baseline, tables, false, nil)`.
- The deterministic baseline is always computed first; the refined plan replaces
  it only when the LLM returns a non-empty, JSON-valid `MappingPlan`.

### Prompt

System prompt (new, dedicated — does not reuse `inferSystemPrompt` because that
one is schema-only and unaware of keys/baseline):

> You improve a relational-schema-to-knowledge-graph mapping. You are given the
> parsed tables (columns, primary keys, foreign keys) and a correct baseline
> mapping. Return ONLY a JSON `MappingPlan` of the same shape that IMPROVES the
> baseline by: (1) clearer relation predicates and entity types/labels; (2)
> relations implied by column names even without a foreign key; (3) routing long
> free-text columns to `kg.text_extract`; (4) collapsing many-to-many junction
> tables into a direct relation between the two referenced entities. Keep every
> table the baseline keeps unless it is a pure junction table. Do not invent
> columns that are not in the schema. JSON shape:
> `{"tables":{"<t>":{"rag":{...},"kg":{"entities":[...],"relations":[...],"text_extract":[{"column":"c"}]}}}}`

User prompt: a compact JSON of `{tables: [{name, columns:[{name,type}], primary_key:[...], foreign_keys:[{column,ref_table,ref_column}]}], baseline: <baseline MappingPlan>}`. Reuse `sanitizeJSON` (already in `infer.go`) to strip prose/fences before unmarshalling.

### Validation / fallback

After `GenerateJSON`:
- `sanitizeJSON` → `json.Unmarshal` into `MappingPlan`.
- Accept only if `err == nil` **and** `len(plan.Tables) > 0`. Otherwise log nothing
  fatal and return `(baseline, tables, false, nil)`.
- No deep schema validation in v1 (the model is told not to invent columns; the
  reviewer sees both plans via the tool). Documented as a known trust boundary.

## MCP tool `importflow_ddl_plan_ai`

Added to `pkg/importflow/toolbox.go`. Unlike `importflow_ddl_plan`, it needs an
LLM. It obtains the generator from the toolbox's `Importer` when that importer was
configured with an `LLMInferer` (same-package access to `im.inferer`):

```go
// inside callDDLPlanAI (same package → can read the unexported im.inferer):
//   Toolbox{im *Importer}; Importer{inferer MappingInferer}; LLMInferer{Client graphflow.JSONGenerator}
if t.im == nil {
    return nil, fmt.Errorf("importflow: importflow_ddl_plan_ai requires a Toolbox built over an Importer")
}
li, ok := t.im.inferer.(LLMInferer)
if !ok || li.Client == nil {
    return nil, fmt.Errorf("importflow: importflow_ddl_plan_ai requires an LLM-backed inferer; construct the Importer with WithMappingInferer(LLMInferer{Client: gen})")
}
gen := li.Client
```

- Definition: `importflow_ddl_plan_ai`, input
  `{ "ddl": string (required), "relation_style": "column|reftable" }`.
- Output `ddlPlanAIResult{ MappingPlan, Baseline MappingPlan, Tables []DDLTable, Notes []string, LLMUsed bool }` — returns BOTH the refined plan and the deterministic baseline so a reviewer can diff what the LLM changed; `Notes` reuses the composite/no-PK notes from the deterministic tool; `LLMUsed` is false when it fell back.
- Read-only/proposal: like `importflow_ddl_plan`, the caller reviews then runs
  `importflow_run` / `connector_run` to build the graph.

## Errors and edge cases

- Nil generator (library) → hard error. Tool with no `LLMInferer` configured →
  clear actionable error (above).
- LLM error / timeout / invalid JSON / empty `tables` → graceful fallback to the
  deterministic baseline (`LLMUsed=false`), no error.
- LLM drops a table the baseline kept → "keep every table" prompt instruction
  mitigates; v1 does not force-merge, but the baseline is always available in the
  tool output for comparison. (Documented; a future version could union missing
  tables back in.)
- DDL with no `CREATE TABLE` → hard error from `ParseDDL` (propagated).

## Testing

- **Refine flow** (fake `JSONGenerator` returning a hand-written refined plan):
  assert the refined plan flows through — e.g. a `placed_by` predicate, a
  `text_extract` entry, and an implicit relation that the baseline lacked;
  `llmUsed=true`.
- **Fallback** (fake generator returning an error, and one returning `"{}"`/empty
  tables): `MappingFromDDLWithLLM` returns the deterministic baseline,
  `llmUsed=false`, `err=nil`.
- **Nil generator**: hard error.
- **Tool**: build a `Toolbox` over `New(db, WithMappingInferer(LLMInferer{Client: fake}))`;
  `importflow_ddl_plan_ai` returns `ddlPlanAIResult` with the refined plan +
  baseline + `LLMUsed=true`. With `NewToolbox(New(db))` (no inferer) → the clear error.
- **Live (skipped without key)**: real DDL through the configured DashScope model
  (`.env`) → assert a non-empty, JSON-valid refined plan and `llmUsed=true`. CI
  skips when `OPENAI_API_KEY` is unset.
- All non-live tests are deterministic (fake generator), CI-safe.

## Non-goals (v1)

- No deep semantic validation of the LLM's plan against the schema beyond
  "non-empty tables" + "don't invent columns" (prompt-level).
- No automatic union of tables the LLM dropped back into the plan (baseline is
  exposed for comparison instead).
- No change to the deterministic `importflow_ddl_plan` tool — this is a separate
  opt-in tool.
- No new LLM SDK in `pkg/` — only the `graphflow.JSONGenerator` interface.

## Resolved decisions

- Output: refined `importflow.MappingPlan` + the deterministic baseline alongside.
- Architecture: **refine-the-baseline** with graceful fallback to the baseline.
- LLM enhancements in v1: **all four** — semantic naming, implicit relations,
  free-text→TextExtract, junction-table collapse.
- Reuse `graphflow.JSONGenerator`; tool reads it from a configured `LLMInferer`.
- Lives in `pkg/importflow` (`ddl_llm.go`); deterministic tool unchanged.
