package connector

import (
	"context"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// PlanOptions tunes plan building.
type PlanOptions struct {
	// DefaultAction for unclassified columns. Default-deny: ActionRedact.
	DefaultAction MaskAction
	// ActionFor overrides the action chosen for a given PII kind.
	ActionFor map[PiiKind]MaskAction
	// ScanTextColumns: columns of type "text" are also marked for free-text scanning.
	ScanTextColumns bool
}

// safeColumnNames are obvious non-PII columns kept as-is so default-deny does
// not redact structural/bookkeeping fields.
var safeColumnNames = map[string]bool{
	"id":         true,
	"uuid":       true,
	"created_at": true,
	"updated_at": true,
	"created":    true,
	"updated":    true,
	"status":     true,
	"type":       true,
	"count":      true,
}

// defaultActionFor maps a PII kind to its default desensitization action.
func defaultActionFor(k PiiKind) MaskAction {
	switch k {
	case PiiNone:
		return "" // decided by caller (keep vs default-deny)
	case PiiNationalID, PiiBankCard:
		return ActionDrop
	case PiiPhone, PiiEmail:
		return ActionMask
	case PiiDOB:
		return ActionGeneralize
	case PiiName:
		return ActionPseudonymize
	default:
		return ActionRedact
	}
}

// BuildMaskingPlan introspects the source schema (NOT bulk data), classifies
// each column, and proposes actions. The returned plan is UNSIGNED — the caller
// must review and Sign() it before Run. Default-deny: a column the classifier is
// unsure about gets DefaultAction (redact), never keep.
func BuildMaskingPlan(ctx context.Context, src importflow.Source, cls Classifier, opts PlanOptions) (MaskingPlan, error) {
	if opts.DefaultAction == "" {
		opts.DefaultAction = ActionRedact // default-deny
	}
	schemas, err := src.Schemas(ctx)
	if err != nil {
		return MaskingPlan{}, err
	}
	var plan MaskingPlan
	for _, sc := range schemas {
		for _, col := range sc.Columns {
			samples := columnSamples(sc, col.Name)
			kind, sens, reason := cls.Classify(ctx, col, samples)
			action := chooseAction(col.Name, kind, opts)
			plan.Columns = append(plan.Columns, ColumnRule{
				Table: sc.Table, Column: col.Name, PiiKind: kind, Sensitivity: sens,
				Action: action, Reason: reason, Source: "rule",
			})
			if opts.ScanTextColumns && col.Type == "text" {
				plan.TextScan = append(plan.TextScan, TextScanRule{Table: sc.Table, Column: col.Name})
			}
		}
	}
	return plan, nil
}

// chooseAction picks the action for a classified column, honoring overrides and
// default-deny for unclassified columns.
func chooseAction(name string, kind PiiKind, opts PlanOptions) MaskAction {
	if kind == PiiNone {
		if safeColumnNames[strings.ToLower(name)] {
			return ActionKeep
		}
		return opts.DefaultAction
	}
	if a, ok := opts.ActionFor[kind]; ok {
		return a
	}
	return defaultActionFor(kind)
}

func columnSamples(sc importflow.Schema, col string) []string {
	var out []string
	for _, r := range sc.Sample {
		if v, ok := r.Get(col); ok {
			out = append(out, v)
		}
	}
	return out
}

// Unmask reverses pseudonymized tokens back to originals via the vault. The only
// reverse path; audited by the vault implementation. Requires the tenant key.
func Unmask(ctx context.Context, v Vault, tenant string, tokens []string, kp KeyProvider) (map[string]string, error) {
	return v.Resolve(ctx, tenant, tokens, kp)
}
