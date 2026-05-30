package importflow

import "strings"

// MappingPlan declares, per table, how source rows route to RAG and KG.
type MappingPlan struct {
	Tables map[string]TablePlan `json:"tables"`
}

// TablePlan is the per-table routing decision.
type TablePlan struct {
	Skip bool     `json:"skip,omitempty"`
	RAG  *RAGPlan `json:"rag,omitempty"`
	KG   *KGPlan  `json:"kg,omitempty"`
}

// RAGPlan describes how a row becomes a retrievable text chunk.
type RAGPlan struct {
	Namespace   string   `json:"namespace,omitempty"`
	ContentTmpl string   `json:"content_tmpl"`        // "{title}\n\n{body}"
	IDColumn    string   `json:"id_column,omitempty"` // default synthesized "table:row"
	Metadata    []string `json:"metadata,omitempty"`  // columns copied into metadata
	Refine      bool     `json:"refine,omitempty"`    // run TextRefiner before embedding
}

// KGPlan describes how a row becomes entities and relations.
type KGPlan struct {
	Entities    []EntityMap   `json:"entities,omitempty"`
	Relations   []RelationMap `json:"relations,omitempty"`
	TextExtract []TextExtract `json:"text_extract,omitempty"`
}

// EntityMap maps columns to one entity per row.
type EntityMap struct {
	Ref       string   `json:"ref"`     // local handle, e.g. "customer"
	Type      string   `json:"type"`    // entity class
	IDTmpl    string   `json:"id_tmpl"` // "{customer_id}"
	LabelTmpl string   `json:"label_tmpl,omitempty"`
	Props     []string `json:"props,omitempty"`
}

// RelationMap connects two entity refs with a predicate.
type RelationMap struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}

// TextExtract names a free-text column for AI triple extraction.
type TextExtract struct {
	Column string   `json:"column"`
	Types  []string `json:"types,omitempty"`
}

// renderTemplate substitutes {column} placeholders with record values.
// Missing or NULL columns render as the empty string.
func renderTemplate(tmpl string, r Record) string {
	var b strings.Builder
	runes := []rune(tmpl)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '{' {
			if close := indexRune(runes, i+1, '}'); close >= 0 {
				name := string(runes[i+1 : close])
				v, _ := r.Get(name)
				b.WriteString(v)
				i = close
				continue
			}
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

func indexRune(rs []rune, from int, target rune) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == target {
			return i
		}
	}
	return -1
}
