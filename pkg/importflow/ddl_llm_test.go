package importflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
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

func TestMappingFromDDLWithLLM_DanglingRelationFallsBack(t *testing.T) {
	// orders' relation points at a "ghost" ref with no entity → not executable, so
	// orders falls back to the baseline (predicate "customer"); customers is fine and
	// is adopted, so llmUsed stays true.
	refined := []byte(`{"tables":{
	  "customers":{"kg":{"entities":[{"ref":"customers","type":"Person","id_tmpl":"{id}"}]}},
	  "orders":{"kg":{
	    "entities":[{"ref":"orders","type":"Order","id_tmpl":"{id}"}],
	    "relations":[{"subject":"orders","predicate":"placed_by","object":"ghost"}]}}}}`)
	plan, _, llmUsed, err := MappingFromDDLWithLLM(context.Background(), llmTestDDL, fakeGen{Out: refined}, DDLMappingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !llmUsed {
		t.Fatal("expected llmUsed=true (customers was adopted)")
	}
	if got := plan.Tables["orders"].KG.Relations[0].Predicate; got != "customer" {
		t.Fatalf("expected orders to fall back to baseline predicate customer, got %q", got)
	}
	if got := plan.Tables["customers"].KG.Entities[0].Type; got != "Person" {
		t.Fatalf("expected refined customers entity type Person, got %q", got)
	}
}

func TestMappingFromDDLWithLLM_DroppedTableUnioned(t *testing.T) {
	// LLM returns only orders (dropping customers). The dropped table must be unioned
	// back from the baseline so no table silently disappears.
	refined := []byte(`{"tables":{
	  "orders":{"kg":{
	    "entities":[{"ref":"orders","type":"Order","id_tmpl":"{id}"},{"ref":"customers","type":"Customer","id_tmpl":"{customer_id}"}],
	    "relations":[{"subject":"orders","predicate":"placed_by","object":"customers"}]}}}}`)
	plan, _, llmUsed, err := MappingFromDDLWithLLM(context.Background(), llmTestDDL, fakeGen{Out: refined}, DDLMappingOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !llmUsed {
		t.Fatal("expected llmUsed=true")
	}
	if _, ok := plan.Tables["customers"]; !ok {
		t.Fatal("expected dropped customers table to be unioned back from baseline")
	}
	if got := plan.Tables["orders"].KG.Relations[0].Predicate; got != "placed_by" {
		t.Fatalf("expected refined orders predicate placed_by, got %q", got)
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
