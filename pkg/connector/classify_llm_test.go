package connector

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

type fakeJSON struct{ out string }

func (f fakeJSON) GenerateJSON(context.Context, string, string) ([]byte, error) {
	return []byte(f.out), nil
}

func TestLLMClassifier(t *testing.T) {
	llm := &LLMClassifier{Client: fakeJSON{out: `{"pii_kind":"address","sensitivity":2,"reason":"looks like a street address"}`}}
	k, s, reason := llm.Classify(context.Background(), importflow.Column{Name: "loc"}, []string{"123 Main St"})
	if k != PiiAddress || s != Confidential || reason == "" {
		t.Fatalf("llm classify: %q %d %q", k, s, reason)
	}
}

func TestChainClassifierRuleFirst(t *testing.T) {
	// rule resolves phone; LLM must NOT be consulted (would return junk)
	llm := &LLMClassifier{Client: fakeJSON{out: `{"pii_kind":"name"}`}}
	chain := ChainClassifier{NewRuleClassifier(), llm}
	k, _, _ := chain.Classify(context.Background(), importflow.Column{Name: "phone"}, []string{"13812341234"})
	if k != PiiPhone {
		t.Fatalf("chain should keep rule result: %q", k)
	}
}
