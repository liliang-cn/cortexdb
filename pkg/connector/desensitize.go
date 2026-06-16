package connector

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// DesensitizerOptions configures a Desensitizer.
type DesensitizerOptions struct {
	Tenant      string
	KeyProvider KeyProvider  // required when the plan has reversible actions
	Vault       Vault        // required when the plan has reversible actions
	TextScanner *TextScanner // defaults to NewTextScanner()
}

// Desensitizer applies a signed MaskingPlan to records.
type Desensitizer struct {
	plan MaskingPlan
	opts DesensitizerOptions
	ts   *TextScanner
}

// NewDesensitizer validates the plan (must be signed) and returns a Desensitizer.
func NewDesensitizer(plan MaskingPlan, opts DesensitizerOptions) (*Desensitizer, error) {
	if !plan.IsSigned() {
		return nil, fmt.Errorf("connector: refusing unsigned MaskingPlan (sign it after review)")
	}
	hasReversible := false
	for _, c := range plan.Columns {
		if c.Action.Reversible() {
			hasReversible = true
		}
	}
	if hasReversible && (opts.Vault == nil || opts.KeyProvider == nil) {
		return nil, fmt.Errorf("connector: plan has reversible actions but Vault/KeyProvider not set")
	}
	ts := opts.TextScanner
	if ts == nil {
		ts = NewTextScanner()
	}
	return &Desensitizer{plan: plan, opts: opts, ts: ts}, nil
}

// keptColumns returns a table's columns minus any with action drop.
func (d *Desensitizer) keptColumns(table string, cols []importflow.Column) []importflow.Column {
	out := make([]importflow.Column, 0, len(cols))
	for _, c := range cols {
		if r, ok := d.plan.RuleFor(table, c.Name); ok && r.Action == ActionDrop {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Apply desensitizes one record per the plan. Dropped columns are removed;
// reversible actions write to the vault and emit a token.
func (d *Desensitizer) Apply(ctx context.Context, r importflow.Record) (importflow.Record, error) {
	out := importflow.Record{Table: r.Table, Row: r.Row, Values: map[string]string{}, Nulls: map[string]bool{}}
	for col, val := range r.Values {
		if r.Nulls[col] {
			out.Nulls[col] = true
			continue
		}
		rule, ok := d.plan.RuleFor(r.Table, col)
		if !ok {
			// Unclassified but present: if it's a text-scan column, redact PII;
			// otherwise default-deny is enforced at plan-build time, so a missing
			// rule here means "keep" only for columns the plan explicitly knows.
			if d.plan.TextScanFor(r.Table, col) {
				masked, _ := d.ts.Scan(val)
				out.Values[col] = masked
			} else {
				out.Values[col] = val
			}
			continue
		}
		switch rule.Action {
		case ActionDrop:
			// omit entirely
		case ActionKeep:
			if d.plan.TextScanFor(r.Table, col) {
				masked, _ := d.ts.Scan(val)
				out.Values[col] = masked
			} else {
				out.Values[col] = val
			}
		case ActionRedact:
			out.Values[col] = Redact(val)
		case ActionMask:
			out.Values[col] = MaskValue(rule.PiiKind, val)
		case ActionGeneralize:
			out.Values[col] = GeneralizeValue(rule.PiiKind, val)
		case ActionHash:
			out.Values[col] = oneWayToken(rule.PiiKind, val)
		case ActionPseudonymize:
			tok, err := d.opts.Vault.Put(ctx, d.opts.Tenant, rule.PiiKind, val, d.opts.KeyProvider)
			if err != nil {
				return out, err
			}
			out.Values[col] = tok
		default:
			out.Values[col] = MaskValue(rule.PiiKind, val)
		}
	}
	return out, nil
}

// oneWayToken is an irreversible deterministic token (no vault entry).
func oneWayToken(kind PiiKind, v string) string {
	// reuse the vault's HMAC shape but with a fixed public salt so it is
	// deterministic yet not reversible (no key, no stored ciphertext).
	return deterministicToken([]byte("connector-oneway-salt-v1"), kind, v)
}

// desensitizedSource wraps a Source so Schemas() hides dropped columns and
// Records() are desensitized.
type desensitizedSource struct {
	inner importflow.Source
	d     *Desensitizer
}

// Desensitized returns a Source that applies d to inner. Drops it straight into
// importflow.New(db).Run(ctx, Desensitized(src, d), plan).
func Desensitized(inner importflow.Source, d *Desensitizer) importflow.Source {
	return &desensitizedSource{inner: inner, d: d}
}

func (s *desensitizedSource) Schemas(ctx context.Context) ([]importflow.Schema, error) {
	in, err := s.inner.Schemas(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]importflow.Schema, len(in))
	for i, sc := range in {
		nsc := importflow.Schema{Table: sc.Table, Columns: s.d.keptColumns(sc.Table, sc.Columns)}
		for _, r := range sc.Sample {
			dr, err := s.d.Apply(ctx, r)
			if err != nil {
				return nil, err
			}
			nsc.Sample = append(nsc.Sample, dr)
		}
		out[i] = nsc
	}
	return out, nil
}

func (s *desensitizedSource) Records(ctx context.Context, fn func(importflow.Record) error) error {
	return s.inner.Records(ctx, func(r importflow.Record) error {
		dr, err := s.d.Apply(ctx, r)
		if err != nil {
			return err
		}
		return fn(dr)
	})
}

func (s *desensitizedSource) Close() error { return s.inner.Close() }
