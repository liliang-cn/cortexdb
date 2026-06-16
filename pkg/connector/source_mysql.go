package connector

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// NewMySQLSource connects to MySQL/MariaDB and returns an importflow.Source.
func NewMySQLSource(dsn string, opts SourceOptions) (importflow.Source, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connector: mysql ping: %w", err)
	}
	if opts.SampleSize == 0 {
		opts.SampleSize = 5
	}
	// MySQL: the active schema is the connection's database; resolve it if unset.
	if opts.Schema == "" {
		if err := db.QueryRow("SELECT DATABASE()").Scan(&opts.Schema); err != nil {
			return nil, fmt.Errorf("connector: mysql current db: %w", err)
		}
	}
	return &sqlSource{
		db: db, driver: "mysql", opts: opts,
		listTables: myListTables, listCols: myListColumns,
		quote: func(s string) string { return "`" + s + "`" },
	}, nil
}

func myListTables(ctx context.Context, db *sql.DB, schema string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema=? AND table_type='BASE TABLE' ORDER BY table_name`, schema)
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

func myListColumns(ctx context.Context, db *sql.DB, schema, table string) ([]importflow.Column, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT column_name, data_type FROM information_schema.columns
		 WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`, schema, table)
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
