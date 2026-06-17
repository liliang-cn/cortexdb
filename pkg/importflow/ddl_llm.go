package importflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

const ddlLLMSystemPrompt = `You improve a relational-schema-to-knowledge-graph mapping.
You are given the parsed tables (columns, primary keys, foreign keys) and a correct
baseline MappingPlan. Return ONLY a JSON MappingPlan of the SAME shape that IMPROVES
the baseline by:
1. clearer relation predicates and entity types/labels (e.g. customer_id -> "placed_by", type "Customer");
2. relations implied by column names even without a declared foreign key;
3. routing long free-text columns (description, notes, body, comment) to kg.text_extract;
4. collapsing many-to-many junction tables into a direct relation between the two referenced entities.
Keep every table the baseline keeps, unless it is a pure junction table. Do NOT invent
columns that are not in the schema. JSON shape:
{"tables":{"<table>":{"skip":false,
  "rag":{"namespace":"","content_tmpl":"{col}","id_column":"","metadata":["col"],"refine":false},
  "kg":{"entities":[{"ref":"","type":"","id_tmpl":"{col}","label_tmpl":"{col}","props":["col"]}],
        "relations":[{"subject":"ref","predicate":"verb","object":"ref"}],
        "text_extract":[{"column":"col"}]}}}}`

// MappingFromDDLWithLLM parses DDL, builds the deterministic baseline plan, and asks
// the LLM to refine it (semantic naming, implicit relations, free-text TextExtract,
// junction-table collapse). It returns the refined plan, the parsed tables, and
// llmUsed=false (with the baseline plan) when the LLM is unavailable or returns an
// unusable result. err is non-nil only on a hard failure (bad DDL or nil generator).
func MappingFromDDLWithLLM(ctx context.Context, ddl string, gen graphflow.JSONGenerator, opts DDLMappingOptions) (MappingPlan, []DDLTable, bool, error) {
	if gen == nil {
		return MappingPlan{}, nil, false, fmt.Errorf("importflow: MappingFromDDLWithLLM requires a JSONGenerator")
	}
	baseline, tables, err := MappingFromDDL(ddl, opts)
	if err != nil {
		return MappingPlan{}, nil, false, err
	}

	user, err := buildDDLLLMUserPrompt(tables, baseline)
	if err != nil {
		return baseline, tables, false, nil
	}
	raw, err := gen.GenerateJSON(ctx, ddlLLMSystemPrompt, user)
	if err != nil {
		return baseline, tables, false, nil
	}
	var refined MappingPlan
	if jerr := json.Unmarshal(sanitizeJSON(raw), &refined); jerr != nil || len(refined.Tables) == 0 {
		return baseline, tables, false, nil
	}
	return refined, tables, true, nil
}

func buildDDLLLMUserPrompt(tables []DDLTable, baseline MappingPlan) (string, error) {
	type col struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	type fk struct {
		Column    string `json:"column"`
		RefTable  string `json:"ref_table"`
		RefColumn string `json:"ref_column"`
	}
	type tbl struct {
		Name        string   `json:"name"`
		Columns     []col    `json:"columns"`
		PrimaryKey  []string `json:"primary_key,omitempty"`
		ForeignKeys []fk     `json:"foreign_keys,omitempty"`
	}
	out := make([]tbl, 0, len(tables))
	for _, t := range tables {
		cols := make([]col, 0, len(t.Columns))
		for _, c := range t.Columns {
			cols = append(cols, col{Name: c.Name, Type: c.Type})
		}
		fks := make([]fk, 0, len(t.ForeignKeys))
		for _, f := range t.ForeignKeys {
			fks = append(fks, fk{Column: f.Column, RefTable: f.RefTable, RefColumn: f.RefColumn})
		}
		out = append(out, tbl{Name: t.Name, Columns: cols, PrimaryKey: t.PrimaryKey, ForeignKeys: fks})
	}
	payload := map[string]any{"tables": out, "baseline": baseline}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
