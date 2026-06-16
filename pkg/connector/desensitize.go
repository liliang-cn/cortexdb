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
	// OnUnlisted is the action applied to a column that the plan does NOT cover
	// (no ColumnRule and not a TextScan column). The gate fails CLOSED: the
	// default is ActionDrop, so a column the signed plan never classified can
	// never leak through — even when the plan is hand-written (e.g. supplied to
	// the connector_run tool) and applied to a live source whose real columns
	// drifted from the plan. Set it to ActionRedact/ActionKeep only with intent.
	OnUnlisted MaskAction
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
	if opts.OnUnlisted == "" {
		opts.OnUnlisted = ActionDrop // fail closed: unknown columns never leak
	}
	return &Desensitizer{plan: plan, opts: opts, ts: ts}, nil
}

// resolve returns the effective action, PII kind, and text-scan flag for a
// table/column. A column the plan does not classify falls back to OnUnlisted
// (default ActionDrop) so the gate fails closed regardless of plan provenance.
func (d *Desensitizer) resolve(table, col string) (action MaskAction, kind PiiKind, scan bool) {
	if r, ok := d.plan.RuleFor(table, col); ok {
		return r.Action, r.PiiKind, d.plan.TextScanFor(table, col)
	}
	if d.plan.TextScanFor(table, col) {
		// Explicitly listed for free-text scanning even without a column rule.
		return ActionKeep, PiiNone, true
	}
	return d.opts.OnUnlisted, PiiNone, false
}

// keptColumns returns a table's columns minus any whose effective action is
// drop (explicit ActionDrop rules AND unlisted columns under a default-deny
// OnUnlisted), keeping the desensitized schema consistent with the records.
func (d *Desensitizer) keptColumns(table string, cols []importflow.Column) []importflow.Column {
	out := make([]importflow.Column, 0, len(cols))
	for _, c := range cols {
		if action, _, _ := d.resolve(table, c.Name); action == ActionDrop {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Apply desensitizes one record per the plan. Dropped columns (explicit or
// unlisted-under-default-deny) are removed entirely; reversible actions write to
// the vault and emit a token.
func (d *Desensitizer) Apply(ctx context.Context, r importflow.Record) (importflow.Record, error) {
	out := importflow.Record{Table: r.Table, Row: r.Row, Values: map[string]string{}, Nulls: map[string]bool{}}
	for col, val := range r.Values {
		action, kind, scan := d.resolve(r.Table, col)
		if action == ActionDrop {
			// Omit entirely — not even a Nulls marker, so a dropped column leaks
			// neither its value nor its existence.
			continue
		}
		if r.Nulls[col] {
			out.Nulls[col] = true
			continue
		}
		switch action {
		case ActionKeep:
			if scan {
				masked, _ := d.ts.Scan(val)
				out.Values[col] = masked
			} else {
				out.Values[col] = val
			}
		case ActionRedact:
			out.Values[col] = Redact(val)
		case ActionMask:
			out.Values[col] = MaskValue(kind, val)
		case ActionGeneralize:
			out.Values[col] = GeneralizeValue(kind, val)
		case ActionHash:
			out.Values[col] = oneWayToken(kind, val)
		case ActionPseudonymize:
			tok, err := d.opts.Vault.Put(ctx, d.opts.Tenant, kind, val, d.opts.KeyProvider)
			if err != nil {
				return out, err
			}
			out.Values[col] = tok
		default:
			out.Values[col] = MaskValue(kind, val)
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
