// pkg/importflow/infer.go
package importflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// MappingInferer proposes a MappingPlan from table schemas and a goal.
type MappingInferer interface {
	InferPlan(ctx context.Context, schemas []Schema, goal Goal) (MappingPlan, error)
}

// LLMInferer is the default MappingInferer, backed by a graphflow.JSONGenerator.
type LLMInferer struct {
	Client graphflow.JSONGenerator
}

const inferSystemPrompt = `You map relational tables to a CortexDB import plan.
Return ONLY JSON matching this shape:
{"tables":{"<table>":{"skip":false,
  "rag":{"namespace":"","content_tmpl":"{col}...","id_column":"","metadata":["col"],"refine":false},
  "kg":{"entities":[{"ref":"","type":"","id_tmpl":"{col}","label_tmpl":"{col}","props":["col"]}],
        "relations":[{"subject":"ref","predicate":"verb","object":"ref"}],
        "text_extract":[{"column":"col"}]}}}}
Route long free-text columns to rag.content_tmpl; id/foreign-key columns to kg
entities/relations. Omit rag or kg when the goal does not request it.`

func (l LLMInferer) InferPlan(ctx context.Context, schemas []Schema, goal Goal) (MappingPlan, error) {
	if l.Client == nil {
		return MappingPlan{}, fmt.Errorf("importflow: LLMInferer requires a JSONGenerator client")
	}
	user, err := buildInferUserPrompt(schemas, goal)
	if err != nil {
		return MappingPlan{}, err
	}
	raw, err := l.Client.GenerateJSON(ctx, inferSystemPrompt, user)
	if err != nil {
		return MappingPlan{}, fmt.Errorf("importflow: infer plan: %w", err)
	}
	var plan MappingPlan
	if err := json.Unmarshal(sanitizeJSON(raw), &plan); err != nil {
		return MappingPlan{}, fmt.Errorf("importflow: parse inferred plan: %w", err)
	}
	if plan.Tables == nil {
		plan.Tables = map[string]TablePlan{}
	}
	return plan, nil
}

func buildInferUserPrompt(schemas []Schema, goal Goal) (string, error) {
	payload := map[string]any{
		"goal":    map[string]any{"build_rag": goal.BuildRAG, "build_kg": goal.BuildKG, "hint": goal.Hint},
		"schemas": schemas,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// sanitizeJSON strips Markdown code fences some models add around JSON.
func sanitizeJSON(raw []byte) []byte {
	s := strings.TrimSpace(string(raw))
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return []byte(strings.TrimSpace(s))
}
