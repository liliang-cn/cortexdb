package connector

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// SourceOptions configures a live SQL source.
type SourceOptions struct {
	Schema     string   // DB schema; Postgres default "public"
	Tables     []string // allow-list; empty = all base tables
	SampleSize int      // sample rows per table in Schemas(); default 5
	RowLimit   int      // max rows streamed per table; 0 = no limit
}

type sqlSource struct {
	db          *sql.DB
	driver      string // "pgx" | "mysql"
	opts        SourceOptions
	listTables  func(ctx context.Context, db *sql.DB, schema string) ([]string, error)
	listCols    func(ctx context.Context, db *sql.DB, schema, table string) ([]importflow.Column, error)
	quote       func(ident string) string
	placeholder func(n int) string // $1 (pg) | ? (mysql)
}

// NewPostgresSource connects to Postgres and returns an importflow.Source.
func NewPostgresSource(dsn string, opts SourceOptions) (importflow.Source, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connector: postgres ping: %w", err)
	}
	if opts.Schema == "" {
		opts.Schema = "public"
	}
	if opts.SampleSize == 0 {
		opts.SampleSize = 5
	}
	return &sqlSource{
		db: db, driver: "pgx", opts: opts,
		listTables: pgListTables, listCols: pgListColumns,
		quote:       quotePostgresIdent,
		placeholder: func(n int) string { return fmt.Sprintf("$%d", n) },
	}, nil
}

// quotePostgresIdent double-quotes an identifier, escaping any embedded double
// quote so a table name can never break out of the SELECT.
func quotePostgresIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func pgListTables(ctx context.Context, db *sql.DB, schema string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema=$1 AND table_type='BASE TABLE' ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func pgListColumns(ctx context.Context, db *sql.DB, schema, table string) ([]importflow.Column, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT column_name, data_type FROM information_schema.columns
		 WHERE table_schema=$1 AND table_name=$2 ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []importflow.Column
	for rows.Next() {
		var name, dt string
		if err := rows.Scan(&name, &dt); err != nil {
			return nil, err
		}
		cols = append(cols, importflow.Column{Name: name, Type: normalizeSQLType(dt)})
	}
	return cols, rows.Err()
}

// normalizeSQLType maps SQL types onto importflow's small type vocabulary.
func normalizeSQLType(dt string) string {
	switch {
	case containsAny(dt, "char", "text", "uuid", "json", "enum"):
		return "text"
	case containsAny(dt, "int", "serial"):
		return "integer"
	case containsAny(dt, "numeric", "decimal", "real", "double", "float"):
		return "number"
	case containsAny(dt, "time", "date"):
		return "timestamp"
	default:
		return ""
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) <= len(s) && indexFold(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexFold(s, sub string) int {
	ls, lsub := toLower(s), toLower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func (s *sqlSource) tables(ctx context.Context) ([]string, error) {
	if len(s.opts.Tables) > 0 {
		return s.opts.Tables, nil
	}
	return s.listTables(ctx, s.db, s.opts.Schema)
}

func (s *sqlSource) Schemas(ctx context.Context) ([]importflow.Schema, error) {
	tables, err := s.tables(ctx)
	if err != nil {
		return nil, err
	}
	var out []importflow.Schema
	for _, t := range tables {
		cols, err := s.listCols(ctx, s.db, s.opts.Schema, t)
		if err != nil {
			return nil, err
		}
		sample, err := s.readRows(ctx, t, s.opts.SampleSize)
		if err != nil {
			return nil, err
		}
		out = append(out, importflow.Schema{Table: t, Columns: cols, Sample: sample})
	}
	return out, nil
}

func (s *sqlSource) Records(ctx context.Context, fn func(importflow.Record) error) error {
	tables, err := s.tables(ctx)
	if err != nil {
		return err
	}
	for _, t := range tables {
		recs, err := s.readRows(ctx, t, s.opts.RowLimit)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if err := fn(r); err != nil {
				return err
			}
		}
	}
	return nil
}

// readRows selects up to limit rows (0 = all) and converts them to Records. It
// reads column names from the result set itself, so it needs no precomputed
// column list.
func (s *sqlSource) readRows(ctx context.Context, table string, limit int) ([]importflow.Record, error) {
	q := "SELECT * FROM " + s.quote(table)
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, table)
}

// scanRows reads column names from the result set and converts every row to an
// importflow.Record. It closes rows on return.
func scanRows(rows *sql.Rows, table string) ([]importflow.Record, error) {
	defer rows.Close()
	colNames, _ := rows.Columns()
	var out []importflow.Record
	idx := 0
	for rows.Next() {
		raw := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		rec := importflow.Record{Table: table, Row: idx, Values: map[string]string{}, Nulls: map[string]bool{}}
		for i, name := range colNames {
			if raw[i] == nil {
				rec.Nulls[name] = true
				continue
			}
			rec.Values[name] = valueToString(raw[i])
		}
		out = append(out, rec)
		idx++
	}
	return out, rows.Err()
}

// readRowsWhere selects rows matching a cursor predicate, ordered by the cursor
// then primary key for stable pagination. Used by the polling change source; the
// snapshot Records()/Schemas() paths are unchanged.
func (s *sqlSource) readRowsWhere(ctx context.Context, table, cursorCol, watermark string, keyCols []string, limit int) ([]importflow.Record, error) {
	q := "SELECT * FROM " + s.quote(table)
	args := []any{}
	if watermark != "" {
		q += " WHERE " + s.quote(cursorCol) + " > " + s.placeholder(1)
		args = append(args, watermark)
	}
	q += " ORDER BY " + s.quote(cursorCol)
	for _, k := range keyCols {
		q += ", " + s.quote(k)
	}
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, table)
}

func valueToString(v any) string {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}

func (s *sqlSource) Close() error { return s.db.Close() }
