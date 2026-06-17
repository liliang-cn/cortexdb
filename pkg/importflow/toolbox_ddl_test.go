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
