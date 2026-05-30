// pkg/importflow/source_csv.go
package importflow

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CSVOptions configures a CSVSource.
type CSVOptions struct {
	Table      string // logical table name; default "csv"
	Delimiter  rune   // default ','
	SampleSize int    // sample rows in Schemas(); default 5
}

// CSVSource is an in-memory Source over a single CSV stream (header required).
type CSVSource struct {
	table      string
	columns    []Column
	records    []Record
	sampleSize int
}

// NewCSVSource eagerly reads the whole CSV into memory and infers column types.
func NewCSVSource(r io.Reader, opts CSVOptions) (*CSVSource, error) {
	if opts.Delimiter == 0 {
		opts.Delimiter = ','
	}
	if opts.SampleSize == 0 {
		opts.SampleSize = 5
	}
	if opts.Table == "" {
		opts.Table = "csv"
	}
	cr := csv.NewReader(r)
	cr.Comma = opts.Delimiter
	cr.FieldsPerRecord = -1
	raw, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty csv: no header row")
	}
	header := raw[0]
	cols := make([]Column, len(header))
	for i, h := range header {
		cols[i] = Column{Name: strings.TrimSpace(h)}
	}
	var recs []Record
	for ri, row := range raw[1:] {
		vals := make(map[string]string, len(cols))
		nulls := make(map[string]bool)
		for ci, c := range cols {
			if ci < len(row) {
				vals[c.Name] = row[ci]
			} else {
				nulls[c.Name] = true
			}
		}
		recs = append(recs, Record{Table: opts.Table, Values: vals, Nulls: nulls, Row: ri})
	}
	inferColumnTypes(cols, recs)
	return &CSVSource{table: opts.Table, columns: cols, records: recs, sampleSize: opts.SampleSize}, nil
}

func (s *CSVSource) Schemas(_ context.Context) ([]Schema, error) {
	n := s.sampleSize
	if n > len(s.records) {
		n = len(s.records)
	}
	return []Schema{{Table: s.table, Columns: s.columns, Sample: s.records[:n]}}, nil
}

func (s *CSVSource) Records(ctx context.Context, fn func(Record) error) error {
	for _, r := range s.records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func (s *CSVSource) Close() error { return nil }

// inferColumnTypes assigns a best-effort Type to each column from its values.
func inferColumnTypes(cols []Column, recs []Record) {
	for i := range cols {
		name := cols[i].Name
		allInt, allFloat, seen := true, true, false
		for _, r := range recs {
			v, ok := r.Get(name)
			if !ok || v == "" {
				continue
			}
			seen = true
			if _, err := strconv.ParseInt(v, 10, 64); err != nil {
				allInt = false
			}
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				allFloat = false
			}
		}
		switch {
		case !seen:
			cols[i].Type = ""
		case allInt:
			cols[i].Type = "integer"
		case allFloat:
			cols[i].Type = "number"
		default:
			cols[i].Type = "text"
		}
	}
}
