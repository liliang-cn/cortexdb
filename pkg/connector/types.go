// Package connector turns live data sources into agent-usable knowledge with
// desensitization as a first-class step. It introspects a source's schema,
// classifies PII, applies a human-signed MaskingPlan, and feeds the
// desensitized records into pkg/importflow (RAG + knowledge graph).
package connector

import "time"

// PiiKind labels what kind of sensitive data a column or span holds.
type PiiKind string

const (
	PiiNone       PiiKind = ""
	PiiName       PiiKind = "name"
	PiiPhone      PiiKind = "phone"
	PiiEmail      PiiKind = "email"
	PiiNationalID PiiKind = "national_id"
	PiiBankCard   PiiKind = "bank_card"
	PiiAddress    PiiKind = "address"
	PiiDOB        PiiKind = "dob"
	PiiIP         PiiKind = "ip"
	PiiGeo        PiiKind = "geo"
	PiiCustom     PiiKind = "custom"
)

// Sensitivity is an ordered confidentiality level.
type Sensitivity int

const (
	Public Sensitivity = iota
	Internal
	Confidential
	Restricted
)

// MaskAction is what the desensitizer does to a classified column/span.
type MaskAction string

const (
	ActionDrop         MaskAction = "drop"         // never imported (removed from schema)
	ActionRedact       MaskAction = "redact"       // [REDACTED] (irreversible)
	ActionMask         MaskAction = "mask"         // partial: 138****1234
	ActionHash         MaskAction = "hash"         // deterministic one-way token (irreversible)
	ActionPseudonymize MaskAction = "pseudonymize" // reversible via vault
	ActionGeneralize   MaskAction = "generalize"   // 34 -> 30-40 (irreversible)
	ActionKeep         MaskAction = "keep"         // non-sensitive
)

// Reversible reports whether an action's original is recoverable from the vault.
func (a MaskAction) Reversible() bool { return a == ActionPseudonymize }

// ColumnRule is one column's classification + chosen action.
type ColumnRule struct {
	Table       string      `json:"table"`
	Column      string      `json:"column"`
	PiiKind     PiiKind     `json:"pii_kind"`
	Sensitivity Sensitivity `json:"sensitivity"`
	Action      MaskAction  `json:"action"`
	Reason      string      `json:"reason,omitempty"` // rule id / LLM note
	Source      string      `json:"source,omitempty"` // "rule" | "llm" | "human"
}

// TextScanRule marks a free-text column for in-place PII scanning.
type TextScanRule struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

// MaskingPlan is the full, reviewable desensitization decision. Run refuses an
// unsigned plan (schema-first, data-second).
type MaskingPlan struct {
	Columns  []ColumnRule   `json:"columns"`
	TextScan []TextScanRule `json:"text_scan,omitempty"`
	SignedBy string         `json:"signed_by,omitempty"`
	SignedAt time.Time      `json:"signed_at,omitempty"`
}

// Sign marks the plan approved by a named reviewer.
func (p *MaskingPlan) Sign(by string, at time.Time) {
	p.SignedBy = by
	p.SignedAt = at
}

// IsSigned reports whether the plan has been approved.
func (p MaskingPlan) IsSigned() bool { return p.SignedBy != "" }

// RuleFor returns the rule for a table/column, if present.
func (p MaskingPlan) RuleFor(table, column string) (ColumnRule, bool) {
	for _, r := range p.Columns {
		if r.Table == table && r.Column == column {
			return r, true
		}
	}
	return ColumnRule{}, false
}

// TextScanFor reports whether a table/column should be free-text scanned.
func (p MaskingPlan) TextScanFor(table, column string) bool {
	for _, t := range p.TextScan {
		if t.Table == table && t.Column == column {
			return true
		}
	}
	return false
}
