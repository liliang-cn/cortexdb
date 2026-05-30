// pkg/importflow/refine.go
package importflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// TextRefiner cleans/summarizes a raw column value before embedding.
type TextRefiner interface {
	Refine(ctx context.Context, table, column, raw string) (string, error)
}

// LLMRefiner is the default TextRefiner, backed by a graphflow.JSONGenerator.
type LLMRefiner struct {
	Client graphflow.JSONGenerator
}

const refineSystemPrompt = `Clean and lightly summarize the given text for retrieval.
Return ONLY JSON: {"text":"<refined>"}.`

func (l LLMRefiner) Refine(ctx context.Context, table, column, raw string) (string, error) {
	if l.Client == nil {
		return raw, nil // no client: pass through unchanged
	}
	user, _ := json.Marshal(map[string]string{"table": table, "column": column, "text": raw})
	out, err := l.Client.GenerateJSON(ctx, refineSystemPrompt, string(user))
	if err != nil {
		return "", fmt.Errorf("importflow: refine: %w", err)
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(sanitizeJSON(out), &parsed); err != nil {
		return "", fmt.Errorf("importflow: parse refined text: %w", err)
	}
	return parsed.Text, nil
}
