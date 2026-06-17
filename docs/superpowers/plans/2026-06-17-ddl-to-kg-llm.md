# LLM-Enhanced DDL → Knowledge-Graph Mapping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an LLM-enhanced DDL→KG mapper that refines the deterministic `MappingFromDDL` baseline (semantic naming, implicit relations, free-text→TextExtract, junction-table collapse) with graceful fallback, plus an opt-in MCP tool `importflow_ddl_plan_ai`.

**Architecture:** Refine-the-baseline. `MappingFromDDLWithLLM` computes the deterministic baseline via the existing `MappingFromDDL`, prompts a `graphflow.JSONGenerator` with the parsed tables (incl. PK/FK) + the baseline plan, and returns the refined plan — falling back to the baseline on any LLM error or empty output. The MCP tool reads the generator from a configured `LLMInferer`.

**Tech Stack:** Go 1.25, `pkg/importflow`, `graphflow.JSONGenerator` interface (no LLM SDK in `pkg/`), `encoding/json`.

---

## File Structure

- **Create `pkg/importflow/ddl_llm.go`** — `MappingFromDDLWithLLM`, the dedicated system prompt, the user-prompt builder, and refined-plan validation. Reuses `ParseDDL`, `MappingFromDDL`, `sanitizeJSON`.
- **Create `pkg/importflow/ddl_llm_test.go`** — fake-`JSONGenerator` unit tests (refine flow, fallback paths, nil generator) + a skipped live test.
- **Modify `pkg/importflow/toolbox.go`** — add the `importflow_ddl_plan_ai` definition, dispatch case, input/result types, and `callDDLPlanAI`. Extract a shared `ddlPlanNotes(tables)` helper (DRY with the existing `callDDLPlan`).

Notes on existing code this builds on (already in the repo):
- `ParseDDL(ddl) ([]DDLTable, error)` and `MappingFromDDL(ddl, DDLMappingOptions) (MappingPlan, []DDLTable, error)` in `ddl.go`.
- `DDLMappingOptions{RelationStyle string; IncludeRAG, IncludeKG *bool}` in `ddl.go`.
- `sanitizeJSON([]byte) []byte` in `infer.go`.
- `LLMInferer{Client graphflow.JSONGenerator}` and `MappingInferer` in `infer.go`.
- `graphflow.JSONGenerator.GenerateJSON(ctx, system, user string) ([]byte, error)` in `pkg/graphflow/llm.go`.
- `Toolbox{im *Importer}`, `Importer{inferer MappingInferer}` (same package → `t.im.inferer` readable).
- `MappingPlan{Tables map[string]TablePlan}` in `plan.go`.

---

## Task 1: `MappingFromDDLWithLLM` core + prompt + validation

**Files:**
- Create: `pkg/importflow/ddl_llm.go`
- Test: `pkg/importflow/ddl_llm_test.go`

- [ ] **Step 1: Write the failing test (refine flow + fallback + nil)**

Create `pkg/importflow/ddl_llm_test.go`:

```go
package importflow

import (
	"context"
	"errors"
	"testing"
)

// fakeGen is a JSONGenerator stub: returns Out (or Err) regardless of prompt.
type fakeGen struct {
	Out []byte
	Err error
}

func (f fakeGen) GenerateJSON(_ context.Context, _ string, _ string) ([]byte, error) {
	return f.Out, f.Err
}

const llmTestDDL = `
CREATE TABLE customers (
  id INTEGER PRIMARY KEY,
  name TEXT,
  email TEXT
);
CREATE TABLE orders (
  id INTEGER PRIMARY KEY,
  customer_id INTEGER REFERENCES customers(id),
  notes TEXT
);`

func TestMappingFromDDLWithLLM_RefineFlow(t *testing.T) {
	// Refined plan: semantic predicate placed_by, a text_extract on orders.notes,
	// and an implicit relation (orders -> customers via email) the baseline lacks.
	refined := []byte(`{"tables":{
	  "customers":{"kg":{"entities":[{"ref":"customers","type":"Customer","id_tmpl":"{id}","props":["name","email"]}]}},
	  "orders":{"kg":{
	    "entities":[
	      {"ref":"orders","type":"Order","id_tmpl":"{id}"},
	      {"ref":"customers","type":"Customer","id_tmpl":"{customer_id}"}],
	    "relations":[{"subject":"orders","predicate":"placed_by","object":"customers"}],
	    "text_extract":[{"column":"notes"}]}}}}`)

	plan, tables, llmUsed, err := MappingFromDDLWithLLM(context.Background(), llmTestDDL, fakeGen{Out: refined}, DDLMappingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !llmUsed {
		t.Fatal("expected llmUsed=true")
	}
	if len(tables) != 2 {
		t.Fatalf("expected 2 parsed tables, got %d", len(tables))
	}
	orders := plan.Tables["orders"]
	if orders.KG == nil || len(orders.KG.Relations) == 0 {
		t.Fatal("expected orders KG relations from refined plan")
	}
	if orders.KG.Relations[0].Predicate != "placed_by" {
		t.Fatalf("expected refined predicate placed_by, got %q", orders.KG.Relations[0].Predicate)
	}
	if len(orders.KG.TextExtract) != 1 || orders.KG.TextExtract[0].Column != "notes" {
		t.Fatalf("expected text_extract on notes, got %+v", orders.KG.TextExtract)
	}
}

func TestMappingFromDDLWithLLM_FallbackOnError(t *testing.T) {
	plan, _, llmUsed, err := MappingFromDDLWithLLM(context.Background(), llmTestDDL, fakeGen{Err: errors.New("boom")}, DDLMappingOptions{})
	if err != nil {
		t.Fatalf("LLM error must not be a hard error, got: %v", err)
	}
	if llmUsed {
		t.Fatal("expected llmUsed=false on LLM error")
	}
	// Baseline: deterministic predicate for customer_id is "customer".
	if got := plan.Tables["orders"].KG.Relations[0].Predicate; got != "customer" {
		t.Fatalf("expected baseline predicate customer, got %q", got)
	}
}

func TestMappingFromDDLWithLLM_FallbackOnEmpty(t *testing.T) {
	for _, out := range []string{`{}`, `{"tables":{}}`, "not json"} {
		_, _, llmUsed, err := MappingFromDDLWithLLM(context.Background(), llmTestDDL, fakeGen{Out: []byte(out)}, DDLMappingOptions{})
		if err != nil {
			t.Fatalf("out=%q: unexpected hard error: %v", out, err)
		}
		if llmUsed {
			t.Fatalf("out=%q: expected fallback (llmUsed=false)", out)
		}
	}
}

func TestMappingFromDDLWithLLM_NilGen(t *testing.T) {
	if _, _, _, err := MappingFromDDLWithLLM(context.Background(), llmTestDDL, nil, DDLMappingOptions{}); err == nil {
		t.Fatal("expected hard error for nil generator")
	}
}

func TestMappingFromDDLWithLLM_BadDDL(t *testing.T) {
	if _, _, _, err := MappingFromDDLWithLLM(context.Background(), "SELECT 1;", fakeGen{Out: []byte(`{"tables":{"x":{}}}`)}, DDLMappingOptions{}); err == nil {
		t.Fatal("expected hard error when no CREATE TABLE is present")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/importflow -run TestMappingFromDDLWithLLM -v`
Expected: FAIL — `undefined: MappingFromDDLWithLLM`.

- [ ] **Step 3: Write the implementation**

Create `pkg/importflow/ddl_llm.go`:

```go
package importflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

const ddlLLMSystemPrompt = `You improve a relational-schema-to-knowledge-graph mapping.
You are given the parsed tables (columns, primary keys, foreign keys) and a correct
baseline MappingPlan. Return ONLY a JSON MappingPlan of the SAME shape that IMPROVES
the baseline by:
1. clearer relation predicates and entity types/labels (e.g. customer_id -> "placed_by", type "Customer");
2. relations implied by column names even without a declared foreign key;
3. routing long free-text columns (description, notes, body, comment) to kg.text_extract;
4. collapsing many-to-many junction tables into a direct relation between the two referenced entities.
Keep every table the baseline keeps, unless it is a pure junction table. Do NOT invent
columns that are not in the schema. JSON shape:
{"tables":{"<table>":{"skip":false,
  "rag":{"namespace":"","content_tmpl":"{col}","id_column":"","metadata":["col"],"refine":false},
  "kg":{"entities":[{"ref":"","type":"","id_tmpl":"{col}","label_tmpl":"{col}","props":["col"]}],
        "relations":[{"subject":"ref","predicate":"verb","object":"ref"}],
        "text_extract":[{"column":"col"}]}}}}`

// MappingFromDDLWithLLM parses DDL, builds the deterministic baseline plan, and asks
// the LLM to refine it (semantic naming, implicit relations, free-text TextExtract,
// junction-table collapse). It returns the refined plan, the parsed tables, and
// llmUsed=false (with the baseline plan) when the LLM is unavailable or returns an
// unusable result. err is non-nil only on a hard failure (bad DDL or nil generator).
func MappingFromDDLWithLLM(ctx context.Context, ddl string, gen graphflow.JSONGenerator, opts DDLMappingOptions) (MappingPlan, []DDLTable, bool, error) {
	if gen == nil {
		return MappingPlan{}, nil, false, fmt.Errorf("importflow: MappingFromDDLWithLLM requires a JSONGenerator")
	}
	baseline, tables, err := MappingFromDDL(ddl, opts)
	if err != nil {
		return MappingPlan{}, nil, false, err
	}

	user, err := buildDDLLLMUserPrompt(tables, baseline)
	if err != nil {
		return baseline, tables, false, nil
	}
	raw, err := gen.GenerateJSON(ctx, ddlLLMSystemPrompt, user)
	if err != nil {
		return baseline, tables, false, nil
	}
	var refined MappingPlan
	if jerr := json.Unmarshal(sanitizeJSON(raw), &refined); jerr != nil || len(refined.Tables) == 0 {
		return baseline, tables, false, nil
	}
	return refined, tables, true, nil
}

func buildDDLLLMUserPrompt(tables []DDLTable, baseline MappingPlan) (string, error) {
	type col struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	type fk struct {
		Column    string `json:"column"`
		RefTable  string `json:"ref_table"`
		RefColumn string `json:"ref_column"`
	}
	type tbl struct {
		Name        string   `json:"name"`
		Columns     []col    `json:"columns"`
		PrimaryKey  []string `json:"primary_key,omitempty"`
		ForeignKeys []fk     `json:"foreign_keys,omitempty"`
	}
	out := make([]tbl, 0, len(tables))
	for _, t := range tables {
		cols := make([]col, 0, len(t.Columns))
		for _, c := range t.Columns {
			cols = append(cols, col{Name: c.Name, Type: c.Type})
		}
		fks := make([]fk, 0, len(t.ForeignKeys))
		for _, f := range t.ForeignKeys {
			fks = append(fks, fk{Column: f.Column, RefTable: f.RefTable, RefColumn: f.RefColumn})
		}
		out = append(out, tbl{Name: t.Name, Columns: cols, PrimaryKey: t.PrimaryKey, ForeignKeys: fks})
	}
	payload := map[string]any{"tables": out, "baseline": baseline}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/importflow -run TestMappingFromDDLWithLLM -v`
Expected: PASS (all 5 subtests).

- [ ] **Step 5: Build the whole module**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 6: Commit**

```bash
git add pkg/importflow/ddl_llm.go pkg/importflow/ddl_llm_test.go
git commit -m "feat(importflow): MappingFromDDLWithLLM — LLM refines deterministic DDL→KG baseline with graceful fallback"
```

---

## Task 2: `importflow_ddl_plan_ai` MCP tool + shared notes helper

**Files:**
- Modify: `pkg/importflow/toolbox.go`
- Test: `pkg/importflow/ddl_llm_test.go` (append tool tests)

- [ ] **Step 1: Write the failing test**

Append to `pkg/importflow/ddl_llm_test.go`:

```go
func TestToolbox_DDLPlanAI(t *testing.T) {
	refined := []byte(`{"tables":{
	  "orders":{"kg":{
	    "entities":[{"ref":"orders","type":"Order","id_tmpl":"{id}"},{"ref":"customers","type":"Customer","id_tmpl":"{customer_id}"}],
	    "relations":[{"subject":"orders","predicate":"placed_by","object":"customers"}]}}}}`)
	im := New(nil, WithMappingInferer(LLMInferer{Client: fakeGen{Out: refined}}))
	tb := NewToolbox(im)

	in := []byte(`{"ddl":` + mustJSONString(llmTestDDL) + `}`)
	out, err := tb.Call(context.Background(), "importflow_ddl_plan_ai", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res, ok := out.(ddlPlanAIResult)
	if !ok {
		t.Fatalf("expected ddlPlanAIResult, got %T", out)
	}
	if !res.LLMUsed {
		t.Fatal("expected LLMUsed=true")
	}
	if res.MappingPlan.Tables["orders"].KG.Relations[0].Predicate != "placed_by" {
		t.Fatal("expected refined predicate in MappingPlan")
	}
	if res.Baseline.Tables["orders"].KG.Relations[0].Predicate != "customer" {
		t.Fatal("expected deterministic baseline alongside refined plan")
	}
}

func TestToolbox_DDLPlanAI_NoInferer(t *testing.T) {
	tb := NewToolbox(New(nil))
	in := []byte(`{"ddl":` + mustJSONString(llmTestDDL) + `}`)
	if _, err := tb.Call(context.Background(), "importflow_ddl_plan_ai", in); err == nil {
		t.Fatal("expected error when no LLM-backed inferer is configured")
	}
}

func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
```

Add the `encoding/json` import to the test file's import block (alongside `context`, `errors`, `testing`):

```go
import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/importflow -run TestToolbox_DDLPlanAI -v`
Expected: FAIL — `undefined: ddlPlanAIResult` and unknown tool `importflow_ddl_plan_ai`.

- [ ] **Step 3: Add the tool definition**

In `pkg/importflow/toolbox.go`, inside `Definitions()`'s returned slice, add this entry immediately after the `importflow_ddl_plan` definition (after its closing `},` at line ~62):

```go
		{
			Name:        "importflow_ddl_plan_ai",
			Description: "Derive a knowledge-graph MappingPlan from SQL DDL using an LLM to refine the deterministic baseline (semantic predicates, implicit relations, free-text extraction, junction-table collapse). Requires the Importer to be configured with an LLM-backed inferer. Returns the refined plan, the deterministic baseline for comparison, the parsed tables, and llm_used.",
			InputSchema: ifObjectSchema(
				[]string{"ddl"},
				map[string]any{
					"ddl":            ifStringSchema("SQL DDL: one or more CREATE TABLE statements."),
					"relation_style": ifEnumSchema("How to name relation predicates from foreign keys (baseline only).", "column", "reftable"),
				},
			),
		},
```

- [ ] **Step 4: Add the dispatch case**

In `Call`'s `switch`, add this case after the `importflow_ddl_plan` case (after line ~74):

```go
	case "importflow_ddl_plan_ai":
		return t.callDDLPlanAI(ctx, input)
```

- [ ] **Step 5: Extract the shared notes helper and add the AI result type + handler**

In `pkg/importflow/toolbox.go`, replace the existing `callDDLPlan` (the function spanning lines ~162-180) with the refactored version that uses a shared `ddlPlanNotes`, then add the AI type and handler. Replace this block:

```go
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

with:

```go
func (t *Toolbox) callDDLPlan(input json.RawMessage) (any, error) {
	var in ddlPlanInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	plan, tables, err := MappingFromDDL(in.DDL, DDLMappingOptions{RelationStyle: in.RelationStyle})
	if err != nil {
		return nil, err
	}
	return ddlPlanResult{MappingPlan: plan, Tables: tables, Notes: ddlPlanNotes(tables)}, nil
}

// ddlPlanNotes flags tables whose primary key prevents a clean KG entity mapping.
func ddlPlanNotes(tables []DDLTable) []string {
	var notes []string
	for _, tb := range tables {
		if len(tb.PrimaryKey) == 0 {
			notes = append(notes, fmt.Sprintf("table %q has no primary key; using synthesized table:row ids and no KG entity for it", tb.Name))
		} else if len(tb.PrimaryKey) > 1 {
			notes = append(notes, fmt.Sprintf("table %q has a composite primary key; using synthesized table:row ids and no KG entity for it", tb.Name))
		}
	}
	return notes
}

type ddlPlanAIResult struct {
	MappingPlan MappingPlan `json:"mapping_plan"`
	Baseline    MappingPlan `json:"baseline"`
	Tables      []DDLTable  `json:"tables"`
	Notes       []string    `json:"notes,omitempty"`
	LLMUsed     bool        `json:"llm_used"`
}

func (t *Toolbox) callDDLPlanAI(ctx context.Context, input json.RawMessage) (any, error) {
	var in ddlPlanInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if t.im == nil {
		return nil, fmt.Errorf("importflow: importflow_ddl_plan_ai requires a Toolbox built over an Importer")
	}
	li, ok := t.im.inferer.(LLMInferer)
	if !ok || li.Client == nil {
		return nil, fmt.Errorf("importflow: importflow_ddl_plan_ai requires an LLM-backed inferer; construct the Importer with WithMappingInferer(LLMInferer{Client: gen})")
	}
	opts := DDLMappingOptions{RelationStyle: in.RelationStyle}
	baseline, _, err := MappingFromDDL(in.DDL, opts)
	if err != nil {
		return nil, err
	}
	plan, tables, llmUsed, err := MappingFromDDLWithLLM(ctx, in.DDL, li.Client, opts)
	if err != nil {
		return nil, err
	}
	return ddlPlanAIResult{MappingPlan: plan, Baseline: baseline, Tables: tables, Notes: ddlPlanNotes(tables), LLMUsed: llmUsed}, nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./pkg/importflow -run 'TestToolbox_DDLPlanAI|TestMappingFromDDLWithLLM' -v`
Expected: PASS (all subtests, including the no-inferer error path).

- [ ] **Step 7: Run the full importflow package tests**

Run: `go test ./pkg/importflow`
Expected: `ok  github.com/liliang-cn/cortexdb/v2/pkg/importflow`.

- [ ] **Step 8: Commit**

```bash
git add pkg/importflow/toolbox.go pkg/importflow/ddl_llm_test.go
git commit -m "feat(importflow): importflow_ddl_plan_ai MCP tool — LLM-refined DDL→KG plan with baseline alongside"
```

---

## Task 3: Skipped live test (real DashScope model)

**Files:**
- Test: `pkg/importflow/ddl_llm_test.go` (append)

- [ ] **Step 1: Write the live test (skips without key)**

Append to `pkg/importflow/ddl_llm_test.go`. This test only runs when `OPENAI_API_KEY` is set; CI leaves it unset so it skips. It uses a tiny inline OpenAI-compatible client so the test file stays SDK-free (mirrors how examples talk to DashScope). Add the imports `net/http`, `bytes`, `os`, `strings`, `time`, `io` to the test file's import block.

```go
// liveGen is a minimal OpenAI-compatible chat client for the skipped live test.
type liveGen struct {
	base, key, model string
}

func (g liveGen) GenerateJSON(ctx context.Context, system, user string) ([]byte, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"model": g.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.base, "/")+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.key)
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Choices) == 0 {
		return nil, errors.New("no choices in response")
	}
	return []byte(parsed.Choices[0].Message.Content), nil
}

func TestMappingFromDDLWithLLM_Live(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("set OPENAI_API_KEY (and optionally OPENAI_BASE_URL/OPENAI_MODEL) to run the live DDL→KG test")
	}
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "qwen3.7-plus"
	}
	gen := liveGen{base: base, key: key, model: model}
	plan, tables, llmUsed, err := MappingFromDDLWithLLM(context.Background(), llmTestDDL, gen, DDLMappingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("expected 2 parsed tables, got %d", len(tables))
	}
	if !llmUsed {
		t.Fatal("expected llmUsed=true with a reachable model")
	}
	if len(plan.Tables) == 0 {
		t.Fatal("expected a non-empty refined plan")
	}
	t.Logf("refined plan tables: %d, orders KG present: %v", len(plan.Tables), plan.Tables["orders"].KG != nil)
}
```

- [ ] **Step 2: Verify it skips by default**

Run: `go test ./pkg/importflow -run TestMappingFromDDLWithLLM_Live -v`
Expected: `--- SKIP: TestMappingFromDDLWithLLM_Live` (no `OPENAI_API_KEY` in env).

- [ ] **Step 3: Optionally verify it runs live**

Run (loads the local `.env` keys/model — DashScope): `set -a; source .env; set +a; go test ./pkg/importflow -run TestMappingFromDDLWithLLM_Live -v`
Expected: PASS, with a log line reporting the refined plan size. (Slow — `qwen3.7-plus` is a reasoning model.)

- [ ] **Step 4: Commit**

```bash
git add pkg/importflow/ddl_llm_test.go
git commit -m "test(importflow): skipped live DDL→KG LLM-refine test against OPENAI_*/.env model"
```

---

## Task 4: Race + full-suite gate

**Files:** none (verification only).

- [ ] **Step 1: Run the importflow package under the race detector**

Run: `go test -race ./pkg/importflow`
Expected: `ok` (live test skips).

- [ ] **Step 2: Build everything (incl. examples mirror)**

Run: `go build ./... && for dir in examples/*/; do (cd "$dir" && go build -o /dev/null .) || exit 1; done && echo OK`
Expected: `OK`.

- [ ] **Step 3: Full suite (matches CI)**

Run: `go test ./...`
Expected: all packages `ok` (or cached). No new failures introduced.

---

## Self-Review

**1. Spec coverage:**
- `MappingFromDDLWithLLM` (refine-the-baseline, 4 enhancements via prompt, graceful fallback) — Task 1. ✓
- Graceful fallback on LLM error / empty / invalid JSON — Task 1 Steps 1 & 3 (`FallbackOnError`, `FallbackOnEmpty`). ✓
- Nil generator + bad DDL hard errors — Task 1. ✓
- `importflow_ddl_plan_ai` tool: reads generator from configured `LLMInferer`, clear error when absent, returns refined + baseline + tables + notes + `llm_used` — Task 2. ✓
- Deterministic `importflow_ddl_plan` unchanged (only refactored to share `ddlPlanNotes`, identical output) — Task 2. ✓
- Fake-generator CI-safe tests + skipped live test — Tasks 1-3. ✓
- No LLM SDK in `pkg/` (only `graphflow.JSONGenerator`; live test uses stdlib `net/http`) — Tasks 1 & 3. ✓

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code. ✓

**3. Type consistency:**
- `MappingFromDDLWithLLM(ctx, ddl string, gen graphflow.JSONGenerator, opts DDLMappingOptions) (MappingPlan, []DDLTable, bool, error)` — consistent across Task 1 (def), its tests, and Task 2 (`callDDLPlanAI` call site, 4 returns). ✓
- `GenerateJSON(ctx, system, user string) ([]byte, error)` — matches `graphflow.JSONGenerator` and `fakeGen`/`liveGen`. ✓
- `t.im.inferer.(LLMInferer)` + `.Client` — matches `Importer.inferer MappingInferer` and `LLMInferer{Client graphflow.JSONGenerator}`. ✓
- `ddlPlanAIResult` fields used identically in handler and `TestToolbox_DDLPlanAI`. ✓
- `New(nil, ...)` / `New(nil)` — `Importer` constructor `New(db, opts...)`; nil DB is fine because these tool tests never call `Run` (only `MappingFromDDL*`, which don't touch the DB). ✓
