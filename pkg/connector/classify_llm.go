package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// LLMClassifier asks a model to classify a column from its name + sample values.
// Used only for columns the rule layer is unsure about (cost + trust boundary).
type LLMClassifier struct {
	Client graphflow.JSONGenerator
}

const classifySystemPrompt = `You classify a database column's privacy sensitivity.
Given a column name and a few sample values, return ONLY JSON:
{"pii_kind":"<none|name|phone|email|national_id|bank_card|address|dob|ip|geo|custom>",
 "sensitivity":<0=public|1=internal|2=confidential|3=restricted>,
 "reason":"short"}
Judge by meaning; sample values may be partial. When unsure, prefer a higher
sensitivity (fail safe).`

func (c *LLMClassifier) Classify(ctx context.Context, col importflow.Column, samples []string) (PiiKind, Sensitivity, string) {
	if c.Client == nil {
		return PiiNone, Public, ""
	}
	user := fmt.Sprintf("column: %s\ntype: %s\nsamples: %s", col.Name, col.Type, strings.Join(samples, " | "))
	raw, err := c.Client.GenerateJSON(ctx, classifySystemPrompt, user)
	if err != nil {
		return PiiNone, Public, "llm:error:" + err.Error()
	}
	var parsed struct {
		PiiKind     string `json:"pii_kind"`
		Sensitivity int    `json:"sensitivity"`
		Reason      string `json:"reason"`
	}
	s := string(raw)
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		_ = json.Unmarshal([]byte(s[i:j+1]), &parsed)
	}
	k := PiiKind(parsed.PiiKind)
	if k == "none" {
		k = PiiNone
	}
	sens := Sensitivity(parsed.Sensitivity)
	if k == PiiNone {
		sens = Public
	}
	return k, sens, "llm:" + parsed.Reason
}

// ChainClassifier runs classifiers in order; the first non-none result wins, so
// cheap deterministic rules short-circuit before the LLM is consulted.
type ChainClassifier []Classifier

func (ch ChainClassifier) Classify(ctx context.Context, col importflow.Column, samples []string) (PiiKind, Sensitivity, string) {
	for _, c := range ch {
		if k, s, r := c.Classify(ctx, col, samples); k != PiiNone {
			return k, s, r
		}
	}
	return PiiNone, Public, ""
}
