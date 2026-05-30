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
