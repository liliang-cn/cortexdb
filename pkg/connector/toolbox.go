package connector

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// ToolboxOptions wires the connector tools to a vault + classifier.
type ToolboxOptions struct {
	Vault       Vault
	KeyProvider KeyProvider
	Tenant      string
	Classifier  Classifier // defaults to NewRuleClassifier()
}

// Toolbox exposes the connector as agent-callable tools, designed to be merged
// into importflow's MCP/toolbox surface.
type Toolbox struct {
	db   *cortexdb.DB
	opts ToolboxOptions
	cls  Classifier
}

// NewToolbox builds a connector toolbox.
func NewToolbox(db *cortexdb.DB, opts ToolboxOptions) *Toolbox {
	cls := opts.Classifier
	if cls == nil {
		cls = NewRuleClassifier()
	}
	return &Toolbox{db: db, opts: opts, cls: cls}
}

func openSource(driver, dsn string, so SourceOptions) (importflow.Source, error) {
	switch driver {
	case "postgres", "pgx":
		return NewPostgresSource(dsn, so)
	case "mysql":
		return NewMySQLSource(dsn, so)
	default:
		return nil, fmt.Errorf("connector: unknown driver %q", driver)
	}
}

// Definitions returns the connector tool definitions.
func (t *Toolbox) Definitions() []cortexdb.ToolDefinition {
	src := map[string]any{
		"driver":      map[string]any{"type": "string", "description": "postgres | mysql"},
		"dsn":         map[string]any{"type": "string", "description": "connection string"},
		"schema":      map[string]any{"type": "string", "description": "DB schema (optional)"},
		"sample_size": map[string]any{"type": "integer", "description": "sample rows per table"},
	}
	obj := func(props map[string]any, req ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": req}
	}
	return []cortexdb.ToolDefinition{
		{Name: "connector_introspect", Description: "Introspect a live DB's schema (tables, columns, sample rows). Data is NOT imported.", InputSchema: obj(src, "driver", "dsn")},
		{Name: "connector_plan", Description: "Introspect + classify PII and return an UNSIGNED MaskingPlan for review.", InputSchema: obj(src, "driver", "dsn")},
		{Name: "connector_run", Description: "Run a desensitized import into RAG+KG using a signed MaskingPlan and a MappingPlan.", InputSchema: obj(map[string]any{
			"driver": src["driver"], "dsn": src["dsn"], "schema": src["schema"],
			"masking_plan": map[string]any{"type": "object", "description": "signed MaskingPlan JSON"},
			"mapping_plan": map[string]any{"type": "object", "description": "importflow MappingPlan JSON"},
		}, "driver", "dsn", "masking_plan", "mapping_plan")},
		{Name: "connector_unmask", Description: "Reverse pseudonymized tokens to originals via the tenant vault (audited).", InputSchema: obj(map[string]any{
			"tokens": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}, "tokens")},
	}
}

// Call dispatches a connector tool by name.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	switch name {
	case "connector_introspect":
		var in struct {
			Driver     string `json:"driver"`
			DSN        string `json:"dsn"`
			Schema     string `json:"schema"`
			SampleSize int    `json:"sample_size"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		src, err := openSource(in.Driver, in.DSN, SourceOptions{Schema: in.Schema, SampleSize: in.SampleSize})
		if err != nil {
			return nil, err
		}
		defer src.Close()
		return src.Schemas(ctx)

	case "connector_plan":
		var in struct {
			Driver     string `json:"driver"`
			DSN        string `json:"dsn"`
			Schema     string `json:"schema"`
			SampleSize int    `json:"sample_size"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		src, err := openSource(in.Driver, in.DSN, SourceOptions{Schema: in.Schema, SampleSize: in.SampleSize})
		if err != nil {
			return nil, err
		}
		defer src.Close()
		return BuildMaskingPlan(ctx, src, t.cls, PlanOptions{ScanTextColumns: true})

	case "connector_run":
		var in struct {
			Driver      string                 `json:"driver"`
			DSN         string                 `json:"dsn"`
			Schema      string                 `json:"schema"`
			MaskingPlan MaskingPlan            `json:"masking_plan"`
			MappingPlan importflow.MappingPlan `json:"mapping_plan"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		src, err := openSource(in.Driver, in.DSN, SourceOptions{Schema: in.Schema})
		if err != nil {
			return nil, err
		}
		defer src.Close()
		d, err := NewDesensitizer(in.MaskingPlan, DesensitizerOptions{Tenant: t.opts.Tenant, KeyProvider: t.opts.KeyProvider, Vault: t.opts.Vault})
		if err != nil {
			return nil, err
		}
		return importflow.New(t.db).Run(ctx, Desensitized(src, d), in.MappingPlan)

	case "connector_unmask":
		var in struct {
			Tokens []string `json:"tokens"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		if t.opts.Vault == nil || t.opts.KeyProvider == nil {
			return nil, fmt.Errorf("connector: unmask needs Vault + KeyProvider")
		}
		return Unmask(ctx, t.opts.Vault, t.opts.Tenant, in.Tokens, t.opts.KeyProvider)

	default:
		return nil, fmt.Errorf("connector: unknown tool %q", name)
	}
}
