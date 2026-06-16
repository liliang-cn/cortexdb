package connector

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestBuildMaskingPlanDefaultDeny(t *testing.T) {
	src := &fakeSource{schema: importflow.Schema{Table: "users", Columns: []importflow.Column{
		{Name: "id"}, {Name: "phone"}, {Name: "mystery"},
	}, Sample: []importflow.Record{
		{Table: "users", Values: map[string]string{"id": "1", "phone": "13812341234", "mystery": "xyz"}},
	}}}
	plan, err := BuildMaskingPlan(context.Background(), src, NewRuleClassifier(), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	byCol := map[string]ColumnRule{}
	for _, r := range plan.Columns {
		byCol[r.Column] = r
	}
	if byCol["phone"].Action != ActionMask {
		t.Errorf("phone action: %q", byCol["phone"].Action)
	}
	if byCol["id"].Action != ActionKeep {
		t.Errorf("id should be keep: %q", byCol["id"].Action)
	}
	// "mystery": unclassified -> default-deny -> redact, never keep
	if byCol["mystery"].Action == ActionKeep {
		t.Errorf("default-deny violated: unclassified column kept")
	}
	if plan.IsSigned() {
		t.Error("freshly built plan must be unsigned")
	}
}
