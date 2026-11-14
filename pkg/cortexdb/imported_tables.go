package cortexdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// Imported tables.
//
// An import turns each row into text to search and facts to walk, and both
// lose the one thing a table is for: "average order value by region by
// month" is a GROUP BY, and no search returns it. So an imported table can
// also be kept as what it was — rows and typed columns in a real table of
// this store — and asked in SQL.
//
// The rows live in tables named imported_tbl_<…>, a prefix nothing else in
// CortexDB uses, and the name a caller knows a table by is only a row in the
// registry (imported_tables): no name out of somebody's dump is ever a table
// name here. A query sees the tables under their registry names and nothing
// else; core.RunGuardedSQLiteRead is what holds it to that.
//
// SQLite only. On PostgreSQL every entry point returns
// ErrImportedTablesUnsupported and runs no SQL: the confinement is built on
// SQLite's authorizer, and PostgreSQL's equivalent is a role with grants on
// exactly these tables, which this library does not manage.

// ErrImportedTablesUnsupported is returned by every imported-table call on a
// backend that cannot hold a query to the imported tables.
var ErrImportedTablesUnsupported = errors.New("cortexdb: imported tables are not supported on this backend (SQLite only)")

const (
	importedTableRegistry = "imported_tables"
	importedTablePrefix   = "imported_tbl_"
	// An unregistered table this old was left by an import that died before
	// it could commit or clean up.
	importedTableOrphanAge = 24 * time.Hour

	importedNameMaxRunes = 120

	defaultImportedQueryRows    = 1000
	maxImportedQueryRows        = 10000
	defaultImportedQueryTimeout = 10 * time.Second
	maxImportedQueryTimeout     = 60 * time.Second
	defaultImportedSampleRows   = 5
	maxImportedSampleRows       = 50
)

// Imported column types. They are the labels pkg/importflow infers.
const (
	ImportedTypeInteger   = "integer"
	ImportedTypeNumber    = "number"
	ImportedTypeText      = "text"
	ImportedTypeTimestamp = "timestamp"
)

// ImportedColumn is one column of an imported table.
type ImportedColumn struct {
	// Name is the column's name in queries. Quote it ("order id") when it is
	// not a plain identifier.
	Name string `json:"name"`
	// SourceName is the name the source gave it, when Name had to differ.
	SourceName string `json:"source_name,omitempty"`
	// Type is integer, number, text or timestamp. A timestamp is stored as
	// the text the source held, which SQLite's date functions read when it
	// is ISO 8601.
	Type string `json:"type"`
}

// ImportedTable is one registry entry.
type ImportedTable struct {
	// Name is the table's name in queries.
	Name string `json:"name"`
	// SourceTable is the name the source gave it.
	SourceTable string `json:"source_table"`
	// Source says what kind of source the rows came from.
	Source string `json:"source,omitempty"`
	// Provenance is whatever the import was stamped with.
	Provenance map[string]string `json:"provenance,omitempty"`
	Columns    []ImportedColumn  `json:"columns"`
	RowCount   int64             `json:"row_count"`
	// ImportedAt is when the current rows replaced whatever was there.
	ImportedAt string `json:"imported_at"`

	physical string
}

// ImportedTableSpec describes a table about to be imported.
type ImportedTableSpec struct {
	// SourceTable is the table's name in the source, taken as given. It is
	// the table's identity: importing the same SourceTable again replaces
	// the rows stored under it.
	SourceTable string
	Source      string
	Provenance  map[string]string
	// Columns in row order. Type is one of the ImportedType constants;
	// anything else is stored as text.
	Columns []ImportedColumn
}

// ImportedTableWriter fills one imported table. The rows go to a staging
// table nothing can see; Commit puts it in the registry in one transaction,
// in place of the previous import of the same source table, and Abort throws
// it away. Until Commit, queries keep reading the previous import.
type ImportedTableWriter struct {
	db       *DB
	spec     ImportedTableSpec
	columns  []ImportedColumn
	physical string
	insert   string
	rows     int64
	done     bool
}

func (db *DB) importedTablesSupported() error {
	if db == nil || db.store == nil {
		return errors.New("cortexdb: database is not open")
	}
	if db.Dialect().Kind() != sqldialect.SQLite {
		return fmt.Errorf("%w: this store is %s", ErrImportedTablesUnsupported, db.Dialect().Kind())
	}
	return nil
}

// BeginImportedTable creates the staging table for spec and returns its
// writer.
func (db *DB) BeginImportedTable(ctx context.Context, spec ImportedTableSpec) (*ImportedTableWriter, error) {
	if err := db.importedTablesSupported(); err != nil {
		return nil, err
	}
	if spec.SourceTable == "" {
		return nil, errors.New("cortexdb: imported table needs a source table name")
	}
	if len(spec.Columns) == 0 {
		return nil, fmt.Errorf("cortexdb: imported table %q has no columns", spec.SourceTable)
	}
	columns := importedColumns(spec.Columns)

	if _, err := db.SQL().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+importedTableRegistry+` (
		source_table TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		name_key TEXT NOT NULL UNIQUE,
		physical_name TEXT NOT NULL UNIQUE,
		source TEXT NOT NULL DEFAULT '',
		provenance TEXT NOT NULL DEFAULT '{}',
		columns TEXT NOT NULL,
		row_count INTEGER NOT NULL DEFAULT 0,
		imported_at TEXT NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("cortexdb: imported table registry: %w", err)
	}
	db.dropOrphanedImportedTables(ctx)

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return nil, err
	}
	physical := fmt.Sprintf("%s%d_%s", importedTablePrefix, time.Now().Unix(), hex.EncodeToString(suffix))
	defs := make([]string, len(columns))
	marks := make([]string, len(columns))
	for i, c := range columns {
		defs[i] = quoteImportedIdent(c.Name) + " " + importedSQLiteType(c.Type)
		marks[i] = "?"
	}
	if _, err := db.SQL().ExecContext(ctx, "CREATE TABLE "+quoteImportedIdent(physical)+" ("+strings.Join(defs, ", ")+")"); err != nil {
		return nil, fmt.Errorf("cortexdb: create imported table %q: %w", spec.SourceTable, err)
	}
	return &ImportedTableWriter{
		db:       db,
		spec:     spec,
		columns:  columns,
		physical: physical,
		insert:   "INSERT INTO " + quoteImportedIdent(physical) + " VALUES (" + strings.Join(marks, ", ") + ")",
	}, nil
}

// Columns returns the columns as stored, in row order.
func (w *ImportedTableWriter) Columns() []ImportedColumn {
	return append([]ImportedColumn(nil), w.columns...)
}

// Append stores rows, each one value per column in column order; nil is
// NULL. The batch is one transaction.
func (w *ImportedTableWriter) Append(ctx context.Context, rows [][]any) error {
	if w.done {
		return errors.New("cortexdb: imported table writer is closed")
	}
	if len(rows) == 0 {
		return nil
	}
	tx, err := w.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, w.insert)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, row := range rows {
		if len(row) != len(w.columns) {
			return fmt.Errorf("cortexdb: imported table %q: row has %d values for %d columns", w.spec.SourceTable, len(row), len(w.columns))
		}
		if _, err := stmt.ExecContext(ctx, row...); err != nil {
			return fmt.Errorf("cortexdb: imported table %q: %w", w.spec.SourceTable, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.rows += int64(len(rows))
	return nil
}

// Commit registers the staged rows as the table's contents and drops the
// rows of the previous import of the same source table.
func (w *ImportedTableWriter) Commit(ctx context.Context) (*ImportedTable, error) {
	if w.done {
		return nil, errors.New("cortexdb: imported table writer is closed")
	}
	columns, err := json.Marshal(w.columns)
	if err != nil {
		return nil, err
	}
	provenance := []byte("{}")
	if len(w.spec.Provenance) > 0 {
		if provenance, err = json.Marshal(w.spec.Provenance); err != nil {
			return nil, err
		}
	}
	tx, err := w.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var name, previous string
	err = tx.QueryRowContext(ctx, `SELECT name, physical_name FROM `+importedTableRegistry+` WHERE source_table = ?`, w.spec.SourceTable).Scan(&name, &previous)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// A first import: the query name is the source's name made safe, and
		// if another source table already answers to it, numbered.
		base := importedName(w.spec.SourceTable, "table")
		if strings.HasPrefix(strings.ToLower(base), "sqlite_") {
			base = "t_" + base // SQLite keeps sqlite_ for itself
		}
		name = base
		for n := 2; ; n++ {
			var taken int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+importedTableRegistry+` WHERE name_key = ?`, strings.ToLower(name)).Scan(&taken); err != nil {
				return nil, err
			}
			if taken == 0 {
				break
			}
			name = base + "_" + strconv.Itoa(n)
		}
	case err != nil:
		return nil, err
	}
	importedAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+importedTableRegistry+`
		(source_table, name, name_key, physical_name, source, provenance, columns, row_count, imported_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_table) DO UPDATE SET
			physical_name = excluded.physical_name, source = excluded.source, provenance = excluded.provenance,
			columns = excluded.columns, row_count = excluded.row_count, imported_at = excluded.imported_at`,
		w.spec.SourceTable, name, strings.ToLower(name), w.physical, w.spec.Source, string(provenance), string(columns), w.rows, importedAt); err != nil {
		return nil, fmt.Errorf("cortexdb: register imported table %q: %w", w.spec.SourceTable, err)
	}
	if previous != "" {
		if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+quoteImportedIdent(previous)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	w.done = true
	return &ImportedTable{
		Name: name, SourceTable: w.spec.SourceTable, Source: w.spec.Source, Provenance: w.spec.Provenance,
		Columns: w.Columns(), RowCount: w.rows, ImportedAt: importedAt, physical: w.physical,
	}, nil
}

// Abort drops the staged rows. The previous import, if any, is untouched.
// Calling it after Commit does nothing.
func (w *ImportedTableWriter) Abort(ctx context.Context) error {
	if w.done {
		return nil
	}
	w.done = true
	_, err := w.db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS "+quoteImportedIdent(w.physical))
	return err
}

// dropOrphanedImportedTables removes staging tables no registry row points
// at and that are older than any running import could be. Best effort: the
// next import tries again.
func (db *DB) dropOrphanedImportedTables(ctx context.Context) {
	rows, err := db.SQL().QueryContext(ctx, `SELECT name FROM sqlite_master
		WHERE type = 'table' AND substr(name, 1, ?) = ?
		AND name NOT IN (SELECT physical_name FROM `+importedTableRegistry+`)`, len(importedTablePrefix), importedTablePrefix)
	if err != nil {
		return
	}
	var orphans []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			continue
		}
		created, _, _ := strings.Cut(strings.TrimPrefix(name, importedTablePrefix), "_")
		sec, err := strconv.ParseInt(created, 10, 64)
		if err != nil || time.Since(time.Unix(sec, 0)) < importedTableOrphanAge {
			continue
		}
		orphans = append(orphans, name)
	}
	_ = rows.Close()
	for _, name := range orphans {
		_, _ = db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS "+quoteImportedIdent(name))
	}
}

// ListImportedTables returns every imported table with its schema, by name.
func (db *DB) ListImportedTables(ctx context.Context) ([]ImportedTable, error) {
	if err := db.importedTablesSupported(); err != nil {
		return nil, err
	}
	var exists int
	if err := db.SQL().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, importedTableRegistry).Scan(&exists); err != nil {
		return nil, err
	}
	tables := []ImportedTable{}
	if exists == 0 {
		return tables, nil // nothing was ever imported as a table
	}
	rows, err := db.SQL().QueryContext(ctx, `SELECT name, source_table, physical_name, source, provenance, columns, row_count, imported_at
		FROM `+importedTableRegistry+` ORDER BY name_key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var t ImportedTable
		var provenance, columns string
		if err := rows.Scan(&t.Name, &t.SourceTable, &t.physical, &t.Source, &provenance, &columns, &t.RowCount, &t.ImportedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(columns), &t.Columns); err != nil {
			return nil, fmt.Errorf("cortexdb: imported table %q: stored columns: %w", t.Name, err)
		}
		_ = json.Unmarshal([]byte(provenance), &t.Provenance)
		if len(t.Provenance) == 0 {
			t.Provenance = nil
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// ImportedTableDescription is one table with a few of its rows.
type ImportedTableDescription struct {
	ImportedTable
	// SampleRows are the first rows as stored, values in column order.
	SampleRows [][]any `json:"sample_rows"`
}

// DescribeImportedTable returns one table's schema, row count and up to
// sampleRows rows (default 5, at most 50). The name is matched without
// regard to case, and the source's name for the table is accepted too.
func (db *DB) DescribeImportedTable(ctx context.Context, name string, sampleRows int) (*ImportedTableDescription, error) {
	tables, err := db.ListImportedTables(ctx)
	if err != nil {
		return nil, err
	}
	var found *ImportedTable
	for i := range tables {
		if strings.EqualFold(tables[i].Name, name) {
			found = &tables[i]
			break
		}
		if found == nil && tables[i].SourceTable == name {
			found = &tables[i]
		}
	}
	if found == nil {
		known := make([]string, len(tables))
		for i, t := range tables {
			known[i] = t.Name
		}
		return nil, fmt.Errorf("cortexdb: no imported table named %q (imported tables: %s)", name, strings.Join(known, ", "))
	}
	if sampleRows <= 0 {
		sampleRows = defaultImportedSampleRows
	}
	sampleRows = min(sampleRows, maxImportedSampleRows)
	rows, err := db.SQL().QueryContext(ctx, "SELECT * FROM "+quoteImportedIdent(found.physical)+" LIMIT ?", sampleRows)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := &ImportedTableDescription{ImportedTable: *found, SampleRows: [][]any{}}
	for rows.Next() {
		values := make([]any, len(found.Columns))
		targets := make([]any, len(values))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		out.SampleRows = append(out.SampleRows, jsonSafeRow(values))
	}
	return out, rows.Err()
}

// DropImportedTable removes an imported table and its registry entry.
func (db *DB) DropImportedTable(ctx context.Context, name string) error {
	desc, err := db.DescribeImportedTable(ctx, name, 1)
	if err != nil {
		return err
	}
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+importedTableRegistry+` WHERE source_table = ?`, desc.SourceTable); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+quoteImportedIdent(desc.physical)); err != nil {
		return err
	}
	return tx.Commit()
}

// ImportedTableQueryRequest is one read-only SQL query over imported tables.
type ImportedTableQueryRequest struct {
	// SQL is a single SELECT (WITH … SELECT included) in SQLite's dialect,
	// naming tables as ListImportedTables does.
	SQL string `json:"sql"`
	// Params are values for `?` placeholders, in order.
	Params []any `json:"params,omitempty"`
	// MaxRows caps the rows returned (default 1000, at most 10000);
	// Truncated says when more existed.
	MaxRows int `json:"max_rows,omitempty"`
	// TimeoutMS bounds the query (default 10000, at most 60000).
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// ImportedTableQueryResponse is the query's answer, values in column order.
type ImportedTableQueryResponse struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"row_count"`
	Truncated bool     `json:"truncated,omitempty"`
}

// QueryImportedTables runs one read-only SELECT that can read the imported
// tables and nothing else in the store. See core.RunGuardedSQLiteRead for
// what enforces that; an error wrapping core.ErrGuardedReadRefused means the
// query asked for something else.
func (db *DB) QueryImportedTables(ctx context.Context, req ImportedTableQueryRequest) (*ImportedTableQueryResponse, error) {
	tables, err := db.ListImportedTables(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.SQL) == "" {
		return nil, errors.New("imported_tables_query: sql is required")
	}
	if len(tables) == 0 {
		return nil, errors.New("imported_tables_query: no tables have been imported")
	}
	read := core.GuardedRead{
		SQL:     req.SQL,
		Args:    req.Params,
		Tables:  make(map[string]string, len(tables)),
		MaxRows: defaultImportedQueryRows,
		Timeout: defaultImportedQueryTimeout,
	}
	for _, t := range tables {
		read.Tables[t.Name] = t.physical
	}
	if req.MaxRows > 0 {
		read.MaxRows = min(req.MaxRows, maxImportedQueryRows)
	}
	if req.TimeoutMS > 0 {
		read.Timeout = min(time.Duration(req.TimeoutMS)*time.Millisecond, maxImportedQueryTimeout)
	}
	res, err := core.RunGuardedSQLiteRead(ctx, db.SQL(), read)
	if err != nil {
		return nil, fmt.Errorf("imported_tables_query: %w", err)
	}
	for i := range res.Rows {
		res.Rows[i] = jsonSafeRow(res.Rows[i])
	}
	return &ImportedTableQueryResponse{Columns: res.Columns, Rows: res.Rows, RowCount: len(res.Rows), Truncated: res.Truncated}, nil
}

// jsonSafeRow makes a row's values ones encoding/json accepts: a float JSON
// has no spelling for (NaN, an infinity) becomes its name, and a time its
// RFC 3339 form.
func jsonSafeRow(row []any) []any {
	for i, v := range row {
		switch x := v.(type) {
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				row[i] = strconv.FormatFloat(x, 'g', -1, 64)
			}
		case time.Time:
			row[i] = x.Format(time.RFC3339Nano)
		}
	}
	return row
}

// importedColumns turns the source's columns into ones a table can have:
// usable names, no two alike in the way SQLite compares them, known types.
func importedColumns(source []ImportedColumn) []ImportedColumn {
	out := make([]ImportedColumn, len(source))
	taken := make(map[string]bool, len(source))
	for i, c := range source {
		base := importedName(c.Name, "column_"+strconv.Itoa(i+1))
		name := base
		for n := 2; taken[strings.ToLower(name)]; n++ {
			name = base + "_" + strconv.Itoa(n)
		}
		taken[strings.ToLower(name)] = true
		out[i] = ImportedColumn{Name: name, Type: c.Type}
		if name != c.Name {
			out[i].SourceName = c.Name
		}
		switch c.Type {
		case ImportedTypeInteger, ImportedTypeNumber, ImportedTypeTimestamp:
		default:
			out[i].Type = ImportedTypeText
		}
	}
	return out
}

// importedName makes a name out of a source's that SQLite can hold and a
// person can type: valid UTF-8, no control characters, not blank, not
// endless. Everything else — spaces, quotes, punctuation, any script — is
// kept, because the name is only ever used quoted.
func importedName(raw, fallback string) string {
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return '_'
		}
		return r
	}, strings.ToValidUTF8(raw, "_"))
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > importedNameMaxRunes {
		name = strings.TrimSpace(string(runes[:importedNameMaxRunes]))
	}
	if name == "" {
		return fallback
	}
	return name
}

func importedSQLiteType(t string) string {
	switch t {
	case ImportedTypeInteger:
		return "INTEGER"
	case ImportedTypeNumber:
		return "REAL"
	}
	return "TEXT"
}

func quoteImportedIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
