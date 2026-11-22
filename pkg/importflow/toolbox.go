// pkg/importflow/toolbox.go
package importflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// MaxInlineBytes is the most source text a tool call may carry inline (in
// data or ddl): 1 MiB. It is the same wherever the tool is served. A gRPC
// brain accepts messages of at most 4 MiB (grpc-go's default, which
// cortexdb-grpc does not raise), and the text travels as a JSON string inside
// that message, escaped by whatever client encoded it: a quote or a newline
// doubles, and a client that escapes non-ASCII as \uXXXX doubles CJK text
// and triples an emoji. 1 MiB stays under the transport in those cases, so a
// call over the limit is refused here, with this limit in the message, and
// not by the transport. There is no chunked upload, and a table cannot be sent in parts,
// since importing a table again replaces its rows: a larger file is imported
// by path where the toolbox reads local files, or through the Go API.
const MaxInlineBytes = 1 << 20

// ErrInvalidRequest marks a tool call refused for what it asked, before
// anything was read or written.
var ErrInvalidRequest = errors.New("importflow: invalid request")

// ErrLocalFilesDisabled is ErrInvalidRequest for a call that names a path on a
// toolbox that reads no local files.
var ErrLocalFilesDisabled = fmt.Errorf("%w: path is refused", ErrInvalidRequest)

// ErrInputTooLarge is ErrInvalidRequest for inline text over MaxInlineBytes.
var ErrInputTooLarge = fmt.Errorf("%w: inline text is too large", ErrInvalidRequest)

// Toolbox exposes importflow as agent-callable tools.
type Toolbox struct {
	im         *Importer
	localFiles bool
}

// ToolboxOption configures a Toolbox.
type ToolboxOption func(*Toolbox)

// WithLocalFiles lets importflow_plan and importflow_run read the file a call
// names under path. It is off by default, and it must stay off wherever the
// caller is not the owner of the machine the toolbox runs on: a path is
// resolved by the process serving the tool, so on a shared server it would be
// of no use to the caller and would let any key read the server's files.
// Turn it on for a process the caller started for itself, such as an MCP
// server on stdio.
func WithLocalFiles() ToolboxOption { return func(t *Toolbox) { t.localFiles = true } }

// NewToolbox wraps an Importer in a tool surface.
func NewToolbox(im *Importer, opts ...ToolboxOption) *Toolbox {
	t := &Toolbox{im: im}
	for _, o := range opts {
		o(t)
	}
	return t
}

const (
	planToolName      = "importflow_plan"
	runToolName       = "importflow_run"
	ddlPlanToolName   = "importflow_ddl_plan"
	ddlPlanAIToolName = "importflow_ddl_plan_ai"
)

const importFlowText = "The flow: get a plan (importflow_plan from the data, or importflow_ddl_plan from CREATE TABLE statements), review it, importflow_run it with keep_tables, then read the rows with imported_tables_list, imported_tables_describe and imported_tables_query."

// Definitions returns the tool definitions (mirrors graphflow's toolbox shape).
// Only importflow_run is marked Mutates: the three planning tools compute a
// plan from their arguments and write nothing.
func (t *Toolbox) Definitions() []cortexdb.ToolDefinition {
	dataSchema := ifStringSchema("Raw CSV text (header row first) or SQL dump text, at most 1 MiB; a larger text is refused whole, and a table cannot be sent in parts. Give data or path, not both.")
	pathSchema := ifStringSchema("Not available here: this server reads no files for its callers, and a call that names a path is refused. Read the file and send its text as data.")
	if t.localFiles {
		pathSchema = ifStringSchema("A CSV or SQL dump file on this machine, read by this process, instead of data. Not limited to 1 MiB. A CSV with no table name takes the file's name.")
	}
	return []cortexdb.ToolDefinition{
		{
			Name: planToolName,
			Description: "Propose a MappingPlan for importing CSV or SQL-dump data: per table, how rows become searchable text (rag), knowledge-graph entities and relations (kg), and a queryable table (table). Writes nothing; pass the result as plan to importflow_run. " +
				"With a model configured the plan is the model's (mode \"model\", one model call). Without one it is derived from the column list (mode \"no_model\"): build_tables keeps every table, build_rag makes each row one chunk of all its columns, and build_kg adds nothing — for entities and relations without a model use importflow_ddl_plan. " +
				importFlowText,
			InputSchema: ifObjectSchema(
				[]string{"format"},
				map[string]any{
					"format":       ifEnumSchema("Source format.", "csv", "dump"),
					"table":        ifStringSchema("Logical table name for CSV input (default \"csv\")."),
					"data":         dataSchema,
					"path":         pathSchema,
					"build_rag":    ifBoolSchema("Build RAG text index."),
					"build_kg":     ifBoolSchema("Build knowledge-graph triples."),
					"build_tables": ifBoolSchema("Also keep each table as a queryable table (see importflow_run keep_tables)."),
					"hint":         ifStringSchema("Domain hint for the model."),
				},
			),
		},
		{
			Name:    runToolName,
			Mutates: true, Destructive: true,
			Description: "Import CSV or SQL-dump data into CortexDB using a MappingPlan, building RAG and/or KG. A table whose plan entry has \"table\": {} — or every table, with keep_tables — is also kept as a table with typed columns (integer, number, timestamp or text, read from the data), which imported_tables_query can then aggregate, group and join in SQL; importing it again replaces its rows. A plan may hold nothing but that: {\"tables\": {}} with keep_tables stores the tables and builds neither RAG nor KG, and needs no model. Kept tables need a SQLite store; on another backend the call fails before anything is written. " +
				"Returns the counts of rows read, chunks indexed, triples created and tables stored, and Errors for the rows that failed. " +
				importFlowText,
			InputSchema: ifObjectSchema(
				[]string{"format", "plan"},
				map[string]any{
					"format":      ifEnumSchema("Source format.", "csv", "dump"),
					"table":       ifStringSchema("Logical table name for CSV input (default \"csv\"); the name a kept table is stored under."),
					"data":        dataSchema,
					"path":        pathSchema,
					"plan":        ifObjectSchema(nil, map[string]any{"tables": map[string]any{"type": "object"}}),
					"keep_tables": ifBoolSchema("Keep every table of the source that the plan does not skip as a queryable table, whether or not the plan mentions it."),
				},
			),
		},
		{
			Name: ddlPlanToolName,
			Description: "Derive a knowledge-graph MappingPlan from SQL DDL (CREATE TABLE with primary/foreign keys), deterministically and without a model. Writes nothing. Returns a reviewable mapping_plan plus the parsed tables; pass mapping_plan as plan to importflow_run with the dump. " +
				importFlowText,
			InputSchema: ifObjectSchema(
				[]string{"ddl"},
				map[string]any{
					"ddl":            ifStringSchema("SQL DDL: one or more CREATE TABLE statements, at most 1 MiB."),
					"relation_style": ifEnumSchema("How to name relation predicates from foreign keys.", "column", "reftable"),
				},
			),
		},
		{
			Name: ddlPlanAIToolName,
			Description: "Derive a knowledge-graph MappingPlan from SQL DDL, with a model refining the deterministic baseline (semantic predicates, implicit relations, free-text extraction, junction-table collapse; one model call). Writes nothing. Returns mapping_plan, the deterministic baseline for comparison, the parsed tables, and llm_used. Without a model configured, or when the model's answer is unusable, mapping_plan is the baseline and llm_used is false. " +
				importFlowText,
			InputSchema: ifObjectSchema(
				[]string{"ddl"},
				map[string]any{
					"ddl":            ifStringSchema("SQL DDL: one or more CREATE TABLE statements, at most 1 MiB."),
					"relation_style": ifEnumSchema("How to name relation predicates from foreign keys (baseline only).", "column", "reftable"),
				},
			),
		},
	}
}

// Call dispatches a tool by name.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	switch name {
	case planToolName:
		var in planInput
		if err := decodeInput(name, input, &in); err != nil {
			return nil, err
		}
		return t.plan(ctx, in)
	case runToolName:
		var in runInput
		if err := decodeInput(name, input, &in); err != nil {
			return nil, err
		}
		return t.run(ctx, in)
	case ddlPlanToolName:
		var in ddlPlanInput
		if err := decodeInput(name, input, &in); err != nil {
			return nil, err
		}
		return t.ddlPlan(ctx, in)
	case ddlPlanAIToolName:
		var in ddlPlanInput
		if err := decodeInput(name, input, &in); err != nil {
			return nil, err
		}
		return t.ddlPlanAI(ctx, in)
	default:
		return nil, fmt.Errorf("importflow: unknown tool %q", name)
	}
}

func decodeInput(name string, input json.RawMessage, into any) error {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	if err := json.Unmarshal(input, into); err != nil {
		return fmt.Errorf("%w: decode %s: %v", ErrInvalidRequest, name, err)
	}
	return nil
}

type runInput struct {
	Format string      `json:"format"`
	Table  string      `json:"table,omitempty"`
	Data   string      `json:"data,omitempty"`
	Path   string      `json:"path,omitempty"`
	Plan   MappingPlan `json:"plan"`
	// KeepTables applies KeepTables to the plan over the source's schemas.
	KeepTables bool `json:"keep_tables,omitempty"`
}

type planInput struct {
	Format      string `json:"format"`
	Table       string `json:"table,omitempty"`
	Data        string `json:"data,omitempty"`
	Path        string `json:"path,omitempty"`
	BuildRAG    bool   `json:"build_rag,omitempty"`
	BuildKG     bool   `json:"build_kg,omitempty"`
	BuildTables bool   `json:"build_tables,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

// planResult is the plan with how it was made. The plan's own field (tables)
// is at the top level, so the whole result is accepted as importflow_run's
// plan.
type planResult struct {
	MappingPlan
	// Mode is "model" when the configured inferer proposed the plan and
	// "no_model" when it was derived from the column list.
	Mode  string   `json:"mode"`
	Notes []string `json:"notes,omitempty"`
}

const (
	planModeModel   = "model"
	planModeNoModel = "no_model"
)

// source opens what a call names: the inline text, or the file at path where
// the toolbox reads local files.
func (t *Toolbox) source(format, table, data, path string) (Source, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format != "csv" && format != "dump" {
		return nil, fmt.Errorf("%w: unsupported format %q (want csv or dump)", ErrInvalidRequest, format)
	}
	path = strings.TrimSpace(path)
	var r io.Reader
	switch {
	case path != "" && data != "":
		return nil, fmt.Errorf("%w: give data or path, not both", ErrInvalidRequest)
	case path != "":
		if !t.localFiles {
			return nil, fmt.Errorf("%w: this server reads no files for its callers, so %q was not opened; read the file where it is and send its text as data (at most %d bytes per call)", ErrLocalFilesDisabled, path, MaxInlineBytes)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("%w: open path: %v", ErrInvalidRequest, err)
		}
		// Both sources read everything they are given before they return.
		defer f.Close()
		r = f
		if table == "" {
			table = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
	case data != "":
		if err := checkInline("data", data); err != nil {
			return nil, err
		}
		r = strings.NewReader(data)
	default:
		return nil, fmt.Errorf("%w: data is required", ErrInvalidRequest)
	}
	var (
		src Source
		err error
	)
	if format == "csv" {
		src, err = NewCSVSource(r, CSVOptions{Table: table})
	} else {
		src, err = NewSQLDumpSource(r, DumpOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return src, nil
}

// checkInline refuses inline text over MaxInlineBytes, whole: a prefix of a
// CSV or a dump would import as if it were all of it.
func checkInline(field, text string) error {
	if len(text) > MaxInlineBytes {
		return fmt.Errorf("%w: %s is %d bytes and the limit is %d; nothing was imported. A table imported again is replaced, so it cannot be sent in parts: import a larger file by path on a server that reads local files, or through the Go API", ErrInputTooLarge, field, len(text), MaxInlineBytes)
	}
	return nil
}

// generator is the model the Importer plans with, or nil.
func (t *Toolbox) generator() graphflow.JSONGenerator {
	if t.im == nil {
		return nil
	}
	if li, ok := t.im.inferer.(LLMInferer); ok {
		return li.Client
	}
	return nil
}

// hasInferer reports whether Plan has something to ask: an LLMInferer with no
// client counts as none.
func (t *Toolbox) hasInferer() bool {
	if t.im == nil || t.im.inferer == nil {
		return false
	}
	if li, ok := t.im.inferer.(LLMInferer); ok {
		return li.Client != nil
	}
	return true
}

// run is importflow_run.
func (t *Toolbox) run(ctx context.Context, in runInput) (*Report, error) {
	if t.im == nil {
		return nil, fmt.Errorf("importflow: %s requires a Toolbox built over an Importer", runToolName)
	}
	src, err := t.source(in.Format, in.Table, in.Data, in.Path)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	plan := in.Plan
	if in.KeepTables {
		schemas, err := src.Schemas(ctx)
		if err != nil {
			return nil, fmt.Errorf("importflow: read schemas: %w", err)
		}
		plan = KeepTables(plan, schemas)
	}
	rep, err := t.im.Run(ctx, src, plan)
	if err != nil {
		return nil, err
	}
	return rep, nil
}

// plan is importflow_plan.
func (t *Toolbox) plan(ctx context.Context, in planInput) (*planResult, error) {
	src, err := t.source(in.Format, in.Table, in.Data, in.Path)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	goal := Goal{BuildRAG: in.BuildRAG, BuildKG: in.BuildKG, BuildTables: in.BuildTables, Hint: in.Hint}
	if t.hasInferer() {
		plan, err := t.im.Plan(ctx, src, goal)
		if err != nil {
			return nil, err
		}
		return &planResult{MappingPlan: plan, Mode: planModeModel}, nil
	}
	schemas, err := src.Schemas(ctx)
	if err != nil {
		return nil, fmt.Errorf("importflow: read schemas: %w", err)
	}
	plan, notes := planFromSchemas(schemas, goal)
	return &planResult{MappingPlan: plan, Mode: planModeNoModel, Notes: notes}, nil
}

// planFromSchemas is the plan importflow_plan gives with no model: what the
// column list alone decides. A table is kept when the goal asks; a row is one
// chunk of all its columns, the template MappingFromDDL falls back to when it
// cannot tell the key columns from the rest; and nothing is proposed for the
// graph, because which column names an entity is not in a column list.
func planFromSchemas(schemas []Schema, goal Goal) (MappingPlan, []string) {
	plan := MappingPlan{Tables: make(map[string]TablePlan, len(schemas))}
	for _, schema := range schemas {
		var tp TablePlan
		if goal.BuildRAG {
			tb := DDLTable{Name: schema.Table}
			for _, c := range schema.Columns {
				tb.Columns = append(tb.Columns, DDLColumn{Name: c.Name, Type: c.Type})
			}
			tp.RAG = &RAGPlan{ContentTmpl: ddlRAGTemplate(tb, nil, nil)}
		}
		plan.Tables[schema.Table] = tp
	}
	if goal.BuildTables {
		plan = KeepTables(plan, schemas)
	}
	notes := []string{"no model is configured: this plan is derived from the column list alone"}
	if goal.BuildRAG {
		notes = append(notes, "build_rag: each row becomes one chunk of all its columns; edit content_tmpl to keep only the text worth searching")
	}
	if goal.BuildKG {
		notes = append(notes, "build_kg: nothing was proposed for the knowledge graph; "+ddlPlanToolName+" derives entities and relations from CREATE TABLE statements without a model, or write the kg entries yourself")
	}
	return plan, notes
}

// --- local JSON-schema helpers (mirror cortexdb's unexported helpers) ---

func ifObjectSchema(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func ifStringSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func ifBoolSchema(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func ifEnumSchema(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

type ddlPlanInput struct {
	DDL           string `json:"ddl"`
	RelationStyle string `json:"relation_style,omitempty"`
}

type ddlPlanResult struct {
	MappingPlan MappingPlan `json:"mapping_plan"`
	Tables      []DDLTable  `json:"tables"`
	Notes       []string    `json:"notes,omitempty"`
}

// ddlPlan is importflow_ddl_plan.
func (t *Toolbox) ddlPlan(_ context.Context, in ddlPlanInput) (ddlPlanResult, error) {
	if err := checkInline("ddl", in.DDL); err != nil {
		return ddlPlanResult{}, err
	}
	plan, tables, err := MappingFromDDL(in.DDL, DDLMappingOptions{RelationStyle: in.RelationStyle})
	if err != nil {
		return ddlPlanResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
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

// ddlPlanAI is importflow_ddl_plan_ai. With no model it answers with the
// baseline, as it does when the model's answer cannot be used.
func (t *Toolbox) ddlPlanAI(ctx context.Context, in ddlPlanInput) (ddlPlanAIResult, error) {
	if err := checkInline("ddl", in.DDL); err != nil {
		return ddlPlanAIResult{}, err
	}
	opts := DDLMappingOptions{RelationStyle: in.RelationStyle}
	baseline, tables, err := MappingFromDDL(in.DDL, opts)
	if err != nil {
		return ddlPlanAIResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	notes := ddlPlanNotes(tables)
	gen := t.generator()
	if gen == nil {
		notes = append(notes, "no model is configured: mapping_plan is the deterministic baseline, the plan "+ddlPlanToolName+" returns")
		return ddlPlanAIResult{MappingPlan: baseline, Baseline: baseline, Tables: tables, Notes: notes}, nil
	}
	plan, tables, llmUsed, err := MappingFromDDLWithLLM(ctx, in.DDL, gen, opts)
	if err != nil {
		return ddlPlanAIResult{}, err
	}
	return ddlPlanAIResult{MappingPlan: plan, Baseline: baseline, Tables: tables, Notes: notes, LLMUsed: llmUsed}, nil
}
