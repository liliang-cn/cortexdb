// pkg/importflow/toolbox.go
package importflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Toolbox exposes importflow as agent-callable tools.
type Toolbox struct {
	im *Importer
}

// NewToolbox wraps an Importer in a tool surface.
func NewToolbox(im *Importer) *Toolbox { return &Toolbox{im: im} }

// Definitions returns the tool definitions (mirrors graphflow's toolbox shape).
func (t *Toolbox) Definitions() []cortexdb.ToolDefinition {
	return []cortexdb.ToolDefinition{
		{
			Name:        "importflow_plan",
			Description: "Infer a CortexDB import MappingPlan from CSV/SQL-dump schemas using the configured AI inferer.",
			InputSchema: ifObjectSchema(
				[]string{"format", "data"},
				map[string]any{
					"format":    ifEnumSchema("Source format.", "csv", "dump"),
					"table":     ifStringSchema("Logical table name for CSV input."),
					"data":      ifStringSchema("Raw CSV text or SQL dump text."),
					"build_rag": ifBoolSchema("Build RAG text index."),
					"build_kg":  ifBoolSchema("Build knowledge-graph triples."),
					"hint":      ifStringSchema("Domain hint for the inferer."),
				},
			),
		},
		{
			Name:        "importflow_run",
			Description: "Run an import of CSV/SQL-dump data into CortexDB using a MappingPlan, building RAG and/or KG.",
			InputSchema: ifObjectSchema(
				[]string{"format", "data", "plan"},
				map[string]any{
					"format": ifEnumSchema("Source format.", "csv", "dump"),
					"table":  ifStringSchema("Logical table name for CSV input."),
					"data":   ifStringSchema("Raw CSV text or SQL dump text."),
					"plan":   ifObjectSchema(nil, map[string]any{"tables": map[string]any{"type": "object"}}),
				},
			),
		},
		{
			Name:        "importflow_ddl_plan",
			Description: "Derive a knowledge-graph MappingPlan from SQL DDL (CREATE TABLE with primary/foreign keys), deterministically and without an LLM. Returns a reviewable plan plus the parsed tables.",
			InputSchema: ifObjectSchema(
				[]string{"ddl"},
				map[string]any{
					"ddl":            ifStringSchema("SQL DDL: one or more CREATE TABLE statements."),
					"relation_style": ifEnumSchema("How to name relation predicates from foreign keys.", "column", "reftable"),
				},
			),
		},
		{
			Name:        "importflow_ddl_plan_ai",
			Description: "Derive a knowledge-graph MappingPlan from SQL DDL using an LLM to refine the deterministic baseline (semantic predicates, implicit relations, free-text extraction, junction-table collapse). Requires the Importer to be configured with an LLM-backed inferer. Returns the refined plan, the deterministic baseline for comparison, the parsed tables, and llm_used.",
			InputSchema: ifObjectSchema(
				[]string{"ddl"},
				map[string]any{
					"ddl":            ifStringSchema("SQL DDL: one or more CREATE TABLE statements."),
					"relation_style": ifEnumSchema("How to name relation predicates from foreign keys (baseline only).", "column", "reftable"),
				},
			),
		},
	}
}

// Call dispatches a tool by name.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	switch name {
	case "importflow_plan":
		return t.callPlan(ctx, input)
	case "importflow_run":
		return t.callRun(ctx, input)
	case "importflow_ddl_plan":
		return t.callDDLPlan(input)
	case "importflow_ddl_plan_ai":
		return t.callDDLPlanAI(ctx, input)
	default:
		return nil, fmt.Errorf("importflow: unknown tool %q", name)
	}
}

type runInput struct {
	Format string      `json:"format"`
	Table  string      `json:"table"`
	Data   string      `json:"data"`
	Plan   MappingPlan `json:"plan"`
}

type planInput struct {
	Format   string `json:"format"`
	Table    string `json:"table"`
	Data     string `json:"data"`
	BuildRAG bool   `json:"build_rag"`
	BuildKG  bool   `json:"build_kg"`
	Hint     string `json:"hint"`
}

func sourceFromInput(format, table, data string) (Source, error) {
	switch strings.ToLower(format) {
	case "csv":
		return NewCSVSource(strings.NewReader(data), CSVOptions{Table: table})
	case "dump":
		return NewSQLDumpSource(strings.NewReader(data), DumpOptions{})
	default:
		return nil, fmt.Errorf("importflow: unsupported format %q", format)
	}
}

func (t *Toolbox) callRun(ctx context.Context, input json.RawMessage) (any, error) {
	var in runInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	src, err := sourceFromInput(in.Format, in.Table, in.Data)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return t.im.Run(ctx, src, in.Plan)
}

func (t *Toolbox) callPlan(ctx context.Context, input json.RawMessage) (any, error) {
	var in planInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	src, err := sourceFromInput(in.Format, in.Table, in.Data)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return t.im.Plan(ctx, src, Goal{BuildRAG: in.BuildRAG, BuildKG: in.BuildKG, Hint: in.Hint})
}

// --- local JSON-schema helpers (mirror cortexdb's unexported helpers) ---

func ifObjectSchema(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func ifStringSchema(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func ifBoolSchema(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

func ifEnumSchema(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

type ddlPlanInput struct {
	DDL           string `json:"ddl"`
	RelationStyle string `json:"relation_style"`
}

type ddlPlanResult struct {
	MappingPlan MappingPlan `json:"mapping_plan"`
	Tables      []DDLTable  `json:"tables"`
	Notes       []string    `json:"notes,omitempty"`
}

func (t *Toolbox) callDDLPlan(input json.RawMessage) (any, error) {
	var in ddlPlanInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	plan, tables, err := MappingFromDDL(in.DDL, DDLMappingOptions{RelationStyle: in.RelationStyle})
	if err != nil {
		return nil, err
	}
	return ddlPlanResult{MappingPlan: plan, Tables: tables, Notes: ddlPlanNotes(tables)}, nil
}

// ddlPlanNotes flags tables whose primary key prevents a clean KG entity mapping.
func ddlPlanNotes(tables []DDLTable) []string {
	var notes []string
	for _, tb := range tables {
		if len(tb.PrimaryKey) == 0 {
			notes = append(notes, fmt.Sprintf("table %q has no primary key; using synthesized table:row ids and no KG entity for it", tb.Name))
		} else if len(tb.PrimaryKey) > 1 {
			notes = append(notes, fmt.Sprintf("table %q has a composite primary key; using synthesized table:row ids and no KG entity for it", tb.Name))
		}
	}
	return notes
}

type ddlPlanAIResult struct {
	MappingPlan MappingPlan `json:"mapping_plan"`
	Baseline    MappingPlan `json:"baseline"`
	Tables      []DDLTable  `json:"tables"`
	Notes       []string    `json:"notes,omitempty"`
	LLMUsed     bool        `json:"llm_used"`
}

func (t *Toolbox) callDDLPlanAI(ctx context.Context, input json.RawMessage) (any, error) {
	var in ddlPlanInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if t.im == nil {
		return nil, fmt.Errorf("importflow: importflow_ddl_plan_ai requires a Toolbox built over an Importer")
	}
	li, ok := t.im.inferer.(LLMInferer)
	if !ok || li.Client == nil {
		return nil, fmt.Errorf("importflow: importflow_ddl_plan_ai requires an LLM-backed inferer; construct the Importer with WithMappingInferer(LLMInferer{Client: gen})")
	}
	opts := DDLMappingOptions{RelationStyle: in.RelationStyle}
	baseline, _, err := MappingFromDDL(in.DDL, opts)
	if err != nil {
		return nil, err
	}
	plan, tables, llmUsed, err := MappingFromDDLWithLLM(ctx, in.DDL, li.Client, opts)
	if err != nil {
		return nil, err
	}
	return ddlPlanAIResult{MappingPlan: plan, Baseline: baseline, Tables: tables, Notes: ddlPlanNotes(tables), LLMUsed: llmUsed}, nil
}
