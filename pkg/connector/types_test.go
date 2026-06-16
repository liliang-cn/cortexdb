package connector

import (
	"testing"
	"time"
)

func TestMaskingPlanSign(t *testing.T) {
	p := MaskingPlan{Columns: []ColumnRule{{Table: "users", Column: "phone", PiiKind: PiiPhone, Sensitivity: Confidential, Action: ActionMask}}}
	if p.IsSigned() {
		t.Fatal("new plan must be unsigned")
	}
	p.Sign("alice", time.Unix(1000, 0))
	if !p.IsSigned() || p.SignedBy != "alice" {
		t.Fatalf("sign failed: %+v", p)
	}
}

func TestRuleFor(t *testing.T) {
	p := MaskingPlan{Columns: []ColumnRule{{Table: "users", Column: "phone", Action: ActionMask}}}
	r, ok := p.RuleFor("users", "phone")
	if !ok || r.Action != ActionMask {
		t.Fatalf("RuleFor miss: %+v %v", r, ok)
	}
	if _, ok := p.RuleFor("users", "name"); ok {
		t.Fatal("unexpected rule for unlisted column")
	}
}
