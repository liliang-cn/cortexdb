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
