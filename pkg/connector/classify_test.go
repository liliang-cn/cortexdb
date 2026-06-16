package connector

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestRuleClassifier(t *testing.T) {
	c := NewRuleClassifier()
	ctx := context.Background()
	cases := []struct {
		col     string
		samples []string
		want    PiiKind
	}{
		{"phone", []string{"13812341234"}, PiiPhone},
		{"user_email", []string{"a@b.com"}, PiiEmail},
		{"id_card", []string{"110101199003078888"}, PiiNationalID},
		{"full_name", nil, PiiName},
		{"created_at", []string{"2026-01-01"}, PiiNone},
		{"notes", []string{"call me at 13812341234"}, PiiNone}, // free text: rule leaves kind none here
	}
	for _, tc := range cases {
		k, _, _ := c.Classify(ctx, importflow.Column{Name: tc.col}, tc.samples)
		if k != tc.want {
			t.Errorf("col %q -> %q want %q", tc.col, k, tc.want)
		}
	}
}

func TestRuleClassifierValueRegexBeatsName(t *testing.T) {
	c := NewRuleClassifier()
	// column name unhelpful, but values are clearly emails
	k, _, _ := c.Classify(context.Background(), importflow.Column{Name: "contact"}, []string{"a@b.com", "c@d.com"})
	if k != PiiEmail {
		t.Fatalf("value-regex classify failed: %q", k)
	}
}
