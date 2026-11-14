// pkg/importflow/sink_table.go
package importflow

import (
	"context"
	"fmt"
	"strconv"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// tableSink keeps each planned table as a table: the rows as they were, in
// typed columns, queryable through the facade's imported-table API.
//
// Importing a table again replaces its rows, which is what the other two
// sinks amount to — the RAG sink saves a row's chunk under the same id and
// the KG sink upserts the same triples — so an import run twice leaves one
// copy here too. The replacement happens when the run ends: until then, and
// if the run fails, queries read the previous import.
type tableSink struct {
	db         *cortexdb.DB
	batchSize  int
	source     string
	provenance map[string]string
	tables     map[string]*tableWriter // by source table name
	stored     int
	rows       int
}

type tableWriter struct {
	w       *cortexdb.ImportedTableWriter
	columns []Column // the source's, in the stored order
	pending [][]any
	failed  bool
}

func newTableSink(db *cortexdb.DB, batchSize int, source string, provenance map[string]string) *tableSink {
	if batchSize <= 0 {
		batchSize = 500
	}
	return &tableSink{db: db, batchSize: batchSize, source: source, provenance: provenance, tables: map[string]*tableWriter{}}
}

// open stages a table for schema, under name when the plan gives one.
func (s *tableSink) open(ctx context.Context, schema Schema, name string) error {
	if name == "" {
		name = schema.Table
	}
	columns := make([]cortexdb.ImportedColumn, len(schema.Columns))
	for i, c := range schema.Columns {
		columns[i] = cortexdb.ImportedColumn{Name: c.Name, Type: c.Type}
	}
	w, err := s.db.BeginImportedTable(ctx, cortexdb.ImportedTableSpec{
		SourceTable: name,
		Source:      s.source,
		Provenance:  s.provenance,
		Columns:     columns,
	})
	if err != nil {
		return err
	}
	s.tables[schema.Table] = &tableWriter{w: w, columns: schema.Columns}
	return nil
}

// add stores one row. A table whose write failed is abandoned — a table
// missing a batch from its middle is worse than the previous import — and
// the error is returned once.
func (s *tableSink) add(ctx context.Context, r Record) error {
	t := s.tables[r.Table]
	if t == nil || t.failed {
		return nil
	}
	row := make([]any, len(t.columns))
	for i, c := range t.columns {
		if v, ok := r.Get(c.Name); ok {
			row[i] = typedValue(c.Type, v)
		}
	}
	t.pending = append(t.pending, row)
	if len(t.pending) >= s.batchSize {
		return s.flushTable(ctx, r.Table, t)
	}
	return nil
}

func (s *tableSink) flushTable(ctx context.Context, table string, t *tableWriter) error {
	if len(t.pending) == 0 {
		return nil
	}
	if err := t.w.Append(ctx, t.pending); err != nil {
		t.failed = true
		_ = t.w.Abort(context.WithoutCancel(ctx))
		return fmt.Errorf("importflow: table %q not stored: %w", table, err)
	}
	t.pending = t.pending[:0]
	return nil
}

// commit writes what is pending and puts every table that took all its rows
// in place of its previous import. Errors are per table.
func (s *tableSink) commit(ctx context.Context) []error {
	var errs []error
	for table, t := range s.tables {
		if t.failed {
			continue
		}
		if err := s.flushTable(ctx, table, t); err != nil {
			errs = append(errs, err)
			continue
		}
		stored, err := t.w.Commit(ctx)
		if err != nil {
			_ = t.w.Abort(context.WithoutCancel(ctx))
			errs = append(errs, fmt.Errorf("importflow: table %q not stored: %w", table, err))
			continue
		}
		s.stored++
		s.rows += int(stored.RowCount)
	}
	return errs
}

// abort drops everything staged; the previous imports stay.
func (s *tableSink) abort(ctx context.Context) {
	for _, t := range s.tables {
		_ = t.w.Abort(context.WithoutCancel(ctx))
	}
}

// typedValue is the value stored for a source string in a column of type typ.
// An integer or number column holds a number when the text is one, NULL when
// it is empty, and otherwise the text itself: a column the source declared
// numeric may still hold "n/a", and the row is kept rather than the value
// invented.
func typedValue(typ, v string) any {
	switch typ {
	case "integer":
		if v == "" {
			return nil
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	case "number":
		if v == "" {
			return nil
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	case "timestamp":
		if v == "" {
			return nil
		}
	}
	return v
}

// sourceLabel names the kind of source for the registry.
func sourceLabel(src Source) string {
	switch src.(type) {
	case *CSVSource:
		return "csv"
	case *SQLDumpSource:
		return "sql_dump"
	}
	return fmt.Sprintf("%T", src)
}

// KeepTables returns plan with every table in schemas also kept as a
// queryable table, except those the plan skips. A table the plan does not
// mention is added with nothing but the table kept.
func KeepTables(plan MappingPlan, schemas []Schema) MappingPlan {
	out := MappingPlan{Tables: make(map[string]TablePlan, len(plan.Tables)+len(schemas))}
	for name, tp := range plan.Tables {
		out.Tables[name] = tp
	}
	for _, schema := range schemas {
		tp := out.Tables[schema.Table]
		if tp.Skip || tp.Table != nil {
			continue
		}
		tp.Table = &TableStorePlan{}
		out.Tables[schema.Table] = tp
	}
	return out
}
