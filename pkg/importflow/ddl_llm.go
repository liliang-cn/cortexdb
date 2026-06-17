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
4. collapsing many-to-many junction tables: declare BOTH referenced entities (each with
   id_tmpl bound to its foreign-key column) and a single direct relation between them; do
   NOT declare an entity for the junction table itself.
INVARIANT: every relation's "subject" and "object" MUST each match the "ref" of an entity
declared in the SAME table's kg.entities — otherwise the relation cannot be built and is
dropped. Keep every table the baseline keeps. Do NOT invent columns that are not in the
schema, and do NOT invent tables. JSON shape:
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
	merged, usedRefined := reconcileRefined(refined, baseline)
	if !usedRefined {
		return baseline, tables, false, nil
	}
	return merged, tables, true, nil
}

// reconcileRefined keeps the deterministic baseline as the source of truth for the set
// of tables, adopting a refined table only when it is executable (its relations bind to
// entities declared in the same table). Tables the LLM dropped or hallucinated, or
// refined tables with dangling relations, fall back to the baseline. usedRefined reports
// whether at least one refined table was adopted.
func reconcileRefined(refined, baseline MappingPlan) (MappingPlan, bool) {
	merged := MappingPlan{Tables: make(map[string]TablePlan, len(baseline.Tables))}
	usedRefined := false
	for name, base := range baseline.Tables {
		if rt, ok := refined.Tables[name]; ok && tablePlanExecutable(rt) {
			merged.Tables[name] = rt
			usedRefined = true
			continue
		}
		merged.Tables[name] = base
	}
	return merged, usedRefined
}

// tablePlanExecutable reports whether every relation in the table's KG plan references
// entities declared in that same plan. mapTriples drops relations whose subject/object
// ref is unknown, so this rules out the dangling-ref failure mode. (It does not guarantee
// edges at runtime: an entity whose id_tmpl renders empty for a row still yields none —
// the same property the deterministic baseline has.)
func tablePlanExecutable(tp TablePlan) bool {
	if tp.KG == nil {
		return true
	}
	refs := make(map[string]bool, len(tp.KG.Entities))
	for _, e := range tp.KG.Entities {
		refs[e.Ref] = true
	}
	for _, r := range tp.KG.Relations {
		if !refs[r.Subject] || !refs[r.Object] {
			return false
		}
	}
	return true
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
