package connector

import (
	"context"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// singleRowSource wraps one record as an importflow.Source so the Watcher can run
// a single insert/update through the normal importflow pipeline (with the
// desensitizer decorator on top). Schemas reports the row's table + columns; the
// row is also offered as the lone sample.
func singleRowSource(table string, cols []importflow.Column, rec importflow.Record) importflow.Source {
	return &oneRow{table: table, cols: cols, rec: rec}
}

type oneRow struct {
	table string
	cols  []importflow.Column
	rec   importflow.Record
}

func (o *oneRow) Schemas(context.Context) ([]importflow.Schema, error) {
	return []importflow.Schema{{Table: o.table, Columns: o.cols, Sample: []importflow.Record{o.rec}}}, nil
}
func (o *oneRow) Records(_ context.Context, fn func(importflow.Record) error) error {
	return fn(o.rec)
}
func (o *oneRow) Close() error { return nil }

// ragChunkID reproduces the KnowledgeID importflow mints for a RAG chunk: the
// value of RAGPlan.IDColumn. (Phase 1 requires IDColumn to be set — see the
// Watcher key precondition — so deletes/updates address the same chunk.)
func ragChunkID(_ string, rag *importflow.RAGPlan, key map[string]string) string {
	if rag == nil || rag.IDColumn == "" {
		return ""
	}
	return key[rag.IDColumn]
}

// entityIRIsForKey reproduces the entity IRIs importflow mints for entities whose
// IDTmpl is built ENTIRELY from key columns (so they are resolvable from a delete
// event that carries only the key). IRI = urn:cortexdb:<Type>:<renderedID>.
func entityIRIsForKey(kg *importflow.KGPlan, key map[string]string) []string {
	if kg == nil {
		return nil
	}
	var out []string
	for _, e := range kg.Entities {
		id, ok := renderFromKey(e.IDTmpl, key)
		if !ok || id == "" {
			continue // IDTmpl references a non-key column; not resolvable from key alone
		}
		out = append(out, "urn:cortexdb:"+e.Type+":"+id)
	}
	return out
}

// renderFromKey substitutes {col} placeholders using only key columns. ok is
// false if any referenced column is absent from key.
func renderFromKey(tmpl string, key map[string]string) (string, bool) {
	var b strings.Builder
	runes := []rune(tmpl)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '{' {
			b.WriteRune(runes[i])
			continue
		}
		j := i + 1
		for j < len(runes) && runes[j] != '}' {
			j++
		}
		if j >= len(runes) {
			b.WriteRune(runes[i])
			continue
		}
		name := string(runes[i+1 : j])
		v, ok := key[name]
		if !ok {
			return "", false
		}
		b.WriteString(v)
		i = j
	}
	return b.String(), true
}
