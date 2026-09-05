package graph

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// A rule is a thing a person declares, not a shape this package ships.
//
// Two-hop composition — works_at then located_in gives works_in_city — was the
// only derivation CortexDB could perform, and it was hard-coded: three relation
// type names in a struct, one join, one hop. Every other rule anyone wanted
// ("a subclass of a subclass is a subclass", "the manager of my manager is in
// my chain") had to be written as application code against the graph, or not
// written at all.
//
// So the rule became data. A Rule is a Horn clause over graph edges: some
// number of premises whose variables bind across each other, one conclusion,
// and a confidence. ApplyRules forward-chains a set of them to a fixpoint and
// materializes what it derives with the same provenance the old two-hop path
// wrote, which is what lets one explanation path cover both.

// Rule is a Horn clause over graph edges: derive Then whenever every atom in
// When can be matched with one consistent set of variable bindings.
type Rule struct {
	// ID is the stable identity written into every edge this rule derives, and
	// the key it persists under. Required.
	ID string `json:"id"`
	// Name is a human label. Optional; it never participates in matching.
	Name string `json:"name,omitempty"`
	// When are the premises, matched in order. Each one is a graph edge
	// pattern; a variable bound by an earlier premise constrains the later
	// ones.
	When []Atom `json:"when"`
	// Then is the conclusion. Every variable in it must be bound by some
	// premise — see Validate, which is what stops a rule deriving edges
	// between nodes it never looked at.
	Then Atom `json:"then"`
	// Confidence multiplies into every derived edge's confidence. Zero means
	// 1.0, so a rule that says nothing about confidence does not silently
	// erase the premises'.
	Confidence float64 `json:"confidence,omitempty"`
	// Note is free text carried alongside the rule for whoever reads it later.
	Note string `json:"note,omitempty"`
	// Weight overrides the derived edge weight. Zero means the mean of the
	// premise weights, which for a two-premise rule is the average the
	// original two-hop inference wrote.
	Weight float64 `json:"weight,omitempty"`
	// Metadata is written onto every derived edge, except for the provenance
	// keys the engine owns — those cannot be overridden, because an edge whose
	// rule_id says something other than the rule that derived it is an edge
	// that lies to inference_explain.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Atom is one edge pattern: predicate(subject, object).
//
// A term starting with '?' is a variable. Anything else is a literal, resolved
// against the stored nodes — see ResolveRuleTerm for exactly how, because
// "matches a node" is the one part of this that a caller has to be told rather
// than able to guess.
type Atom struct {
	Predicate string `json:"predicate"`
	Subject   string `json:"subject"`
	Object    string `json:"object"`
}

// reservedRuleEdgeProperties are the property keys the engine writes itself.
// Rule metadata carrying one of them is dropped rather than applied.
var reservedRuleEdgeProperties = map[string]struct{}{
	"inferred":               {},
	"provenance":             {},
	"rule_id":                {},
	"rule_text":              {},
	"support_edge_ids":       {},
	"support_relation_types": {},
	"confidence":             {},
	"document_id":            {},
}

// ErrRuleCapExceeded reports that forward chaining stopped at a cap instead of
// at a fixpoint. Nothing is written when it is returned: a half-computed
// closure in the graph is worse than none, because nothing downstream can tell
// which half it got.
var ErrRuleCapExceeded = errors.New("rule derivation cap exceeded")

// IsRuleVariable reports whether a term is a variable rather than a literal.
func IsRuleVariable(term string) bool {
	return len(term) > 1 && strings.HasPrefix(term, "?")
}

// String renders an atom as predicate(subject, object).
func (a Atom) String() string {
	return fmt.Sprintf("%s(%s, %s)", a.Predicate, quoteRuleTerm(a.Subject), quoteRuleTerm(a.Object))
}

// Text renders a rule back into the textual form ParseRuleText accepts, so a
// rule built in Go and a rule typed by a person print the same.
func (r Rule) Text() string {
	premises := make([]string, 0, len(r.When))
	for _, atom := range r.When {
		premises = append(premises, atom.String())
	}
	return fmt.Sprintf("IF %s THEN %s", strings.Join(premises, " AND "), r.Then.String())
}

// Validate checks the rule is well formed and safe: an unsafe rule — one whose
// conclusion carries a variable no premise binds — would have to invent nodes
// to fire, so it is rejected here rather than quietly matching nothing.
func (r Rule) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("rule id is required")
	}
	if len(r.When) == 0 {
		return fmt.Errorf("rule %s: at least one premise is required", r.ID)
	}
	bound := make(map[string]struct{})
	for i, atom := range r.When {
		if err := atom.validate(); err != nil {
			return fmt.Errorf("rule %s: premise %d: %w", r.ID, i+1, err)
		}
		for _, term := range []string{atom.Subject, atom.Object} {
			if IsRuleVariable(term) {
				bound[term] = struct{}{}
			}
		}
	}
	if err := r.Then.validate(); err != nil {
		return fmt.Errorf("rule %s: conclusion: %w", r.ID, err)
	}
	unbound := make([]string, 0, 2)
	for _, term := range []string{r.Then.Subject, r.Then.Object} {
		if !IsRuleVariable(term) {
			continue
		}
		if _, ok := bound[term]; !ok {
			unbound = append(unbound, term)
		}
	}
	if len(unbound) > 0 {
		sort.Strings(unbound)
		return fmt.Errorf("rule %s: conclusion uses %s, which no premise binds", r.ID, strings.Join(unbound, " and "))
	}
	if r.Confidence < 0 || r.Confidence > 1 {
		return fmt.Errorf("rule %s: confidence %v is outside [0,1]", r.ID, r.Confidence)
	}
	return nil
}

func (a Atom) validate() error {
	if strings.TrimSpace(a.Predicate) == "" {
		return fmt.Errorf("predicate is required")
	}
	if strings.HasPrefix(strings.TrimSpace(a.Predicate), "?") {
		return fmt.Errorf("predicate %q is a variable; predicates must be literal relation types", a.Predicate)
	}
	if strings.TrimSpace(a.Subject) == "" {
		return fmt.Errorf("subject is required")
	}
	if strings.TrimSpace(a.Object) == "" {
		return fmt.Errorf("object is required")
	}
	return nil
}

// effectiveConfidence is the rule's confidence with zero read as 1.0.
func (r Rule) effectiveConfidence() float64 {
	if r.Confidence <= 0 {
		return 1
	}
	return r.Confidence
}

// normalizeRuleLabel folds a free-form relation type onto one key, the same way
// the ontology label canonicaliser does — "works at", "Works-At" and "works_at"
// are one predicate. Kept here so pkg/graph does not have to reach up into the
// facade for it.
func normalizeRuleLabel(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	return strings.Join(strings.Fields(value), "_")
}

// DerivedEdgeID is the identity of an edge derived by a rule. It deliberately
// excludes the premises: one rule concluding the same relation between the same
// two nodes is one edge, however many chains reach it, so a re-run upserts
// rather than accumulating near-duplicates.
func DerivedEdgeID(ruleID, fromNodeID, toNodeID, edgeType string) string {
	return fmt.Sprintf("edge:inferred:%s:%s:%s:%s",
		normalizeRuleLabel(ruleID), fromNodeID, toNodeID, normalizeRuleLabel(edgeType))
}

// quoteRuleTerm re-quotes a literal that could not be written bare.
func quoteRuleTerm(term string) string {
	if IsRuleVariable(term) {
		return term
	}
	if term != "" && !strings.ContainsAny(term, " \t\n\r,()\"") {
		return term
	}
	return `"` + strings.ReplaceAll(term, `"`, `\"`) + `"`
}
