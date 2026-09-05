package graph

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRuleReadsAWrittenRule(t *testing.T) {
	rule, err := ParseRule("employment_city", "IF works_at(?x, ?y) AND located_in(?y, ?z) THEN works_in_city(?x, ?z)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rule.When) != 2 {
		t.Fatalf("premises: got %d, want 2", len(rule.When))
	}
	if rule.When[0] != (Atom{Predicate: "works_at", Subject: "?x", Object: "?y"}) {
		t.Errorf("first premise: %+v", rule.When[0])
	}
	if rule.When[1] != (Atom{Predicate: "located_in", Subject: "?y", Object: "?z"}) {
		t.Errorf("second premise: %+v", rule.When[1])
	}
	if rule.Then != (Atom{Predicate: "works_in_city", Subject: "?x", Object: "?z"}) {
		t.Errorf("conclusion: %+v", rule.Then)
	}

	// The struct form and the text form are the same rule, so rendering back
	// and reparsing has to be a fixed point — otherwise a rule saved from a
	// program and a rule saved from a person would drift apart in the table.
	round, err := ParseRule("employment_city", rule.Text())
	if err != nil {
		t.Fatalf("reparse %q: %v", rule.Text(), err)
	}
	if round.Text() != rule.Text() {
		t.Errorf("round trip: %q became %q", rule.Text(), round.Text())
	}
}

func TestParseRuleAcceptsLiteralsAndQuoting(t *testing.T) {
	rule, err := ParseRule("mortal", `IF instance_of(?x, "Class: Human") AND subclass_of(Class:Human, class:mortal) THEN instance_of(?x, class:mortal)`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rule.When[0].Object != "Class: Human" {
		t.Errorf("quoted literal: %q", rule.When[0].Object)
	}
	if rule.When[1].Subject != "Class:Human" {
		t.Errorf("bare literal with a colon: %q", rule.When[1].Subject)
	}
	if !strings.Contains(rule.Text(), `"Class: Human"`) {
		t.Errorf("a literal that needed quotes lost them: %s", rule.Text())
	}
}

func TestParseRuleNamesTheOffsetOfTheMistake(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		wantOffset int
		wantSays   string
	}{
		{
			name:       "missing close paren",
			text:       "IF works_at(?x, ?y AND located_in(?y, ?z) THEN works_in_city(?x, ?z)",
			wantOffset: 36,
			wantSays:   "expected ')'",
		},
		{
			name:       "no THEN",
			text:       "IF works_at(?x, ?y) located_in(?y, ?z)",
			wantOffset: 20,
			wantSays:   "expected AND or THEN",
		},
		{
			name:       "does not start with IF",
			text:       "WHEN works_at(?x, ?y) THEN colleague(?x, ?y)",
			wantOffset: 0,
			wantSays:   "expected IF",
		},
		{
			name:       "variable predicate",
			text:       "IF ?p(?x, ?y) THEN q(?x, ?y)",
			wantOffset: 3,
			wantSays:   "predicates must be literal relation types",
		},
		{
			name:       "unterminated quote",
			text:       `IF works_at(?x, "Acme) THEN q(?x, ?x)`,
			wantOffset: 16,
			wantSays:   "unterminated quoted term",
		},
		{
			name:       "trailing junk",
			text:       "IF works_at(?x, ?y) THEN colleague(?x, ?y) AND",
			wantOffset: 43,
			wantSays:   "unexpected input after the conclusion",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRule("r", tc.text)
			if err == nil {
				t.Fatalf("parsed %q, which is not a rule", tc.text)
			}
			var parseErr *RuleParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("error is %T, not a *RuleParseError: %v", err, err)
			}
			if parseErr.Offset != tc.wantOffset {
				t.Errorf("offset %d, want %d (%q)", parseErr.Offset, tc.wantOffset, err)
			}
			if !strings.Contains(err.Error(), tc.wantSays) {
				t.Errorf("message %q does not say %q", err.Error(), tc.wantSays)
			}
			// The message has to be usable on its own: it carries the position
			// and the text around it, not just a complaint.
			if !strings.Contains(err.Error(), "offset") {
				t.Errorf("message %q does not name a position", err.Error())
			}
		})
	}
}

func TestRuleValidateRejectsAConclusionNoPremiseBinds(t *testing.T) {
	// The unsafe rule: ?z appears only in the conclusion, so firing it would
	// mean inventing a node to point at.
	_, err := ParseRule("unsafe", "IF works_at(?x, ?y) THEN works_in_city(?x, ?z)")
	if err == nil {
		t.Fatal("accepted a rule whose conclusion binds nothing")
	}
	if !strings.Contains(err.Error(), "?z") || !strings.Contains(err.Error(), "no premise binds") {
		t.Errorf("error does not name the unbound variable: %v", err)
	}
}

func TestRuleValidateRequiresAnID(t *testing.T) {
	rule, err := ParseRuleText("IF p(?x, ?y) THEN q(?x, ?y)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := rule.Validate(); err == nil {
		t.Fatal("a rule with no id validated")
	}
}
