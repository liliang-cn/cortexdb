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
		"format": "csv",
		"table":  "people",
		"data":   csv,
		"plan":   MappingPlan{Tables: map[string]TablePlan{"people": {RAG: &RAGPlan{ContentTmpl: "{name}: {bio}", IDColumn: "id"}}}},
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
