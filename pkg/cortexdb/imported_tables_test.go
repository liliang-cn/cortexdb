package cortexdb

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

const importedTablesSecret = "the vault code is 4471-ZEBRA"

// openImportedTablesBrain is a brain with something to protect — a document
// and a memory holding importedTablesSecret — and two imported tables.
func openImportedTablesBrain(t *testing.T) (*DB, *GraphRAGToolbox, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "tables.db")), WithoutCredentialRedaction())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "secret-doc", Content: "Operations note: " + importedTablesSecret}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "secret-mem", UserID: "u1", Content: "Remember that " + importedTablesSecret}); err != nil {
		t.Fatal(err)
	}
	storeImportedTable(t, db, "orders",
		[]ImportedColumn{{Name: "id", Type: ImportedTypeInteger}, {Name: "customer_id", Type: ImportedTypeInteger}, {Name: "region", Type: ImportedTypeText}, {Name: "total", Type: ImportedTypeNumber}, {Name: "ordered_at", Type: ImportedTypeTimestamp}},
		[][]any{
			{int64(1), int64(10), "eu", 10.0, "2026-01-05"},
			{int64(2), int64(10), "eu", 30.0, "2026-01-20"},
			{int64(3), int64(11), "us", 5.0, "2026-02-01"},
			{int64(4), int64(11), "eu", 20.0, "2026-02-11"},
		})
	storeImportedTable(t, db, "customers",
		[]ImportedColumn{{Name: "id", Type: ImportedTypeInteger}, {Name: "name", Type: ImportedTypeText}},
		[][]any{{int64(10), "Ada"}, {int64(11), "Grace"}})
	return db, db.GraphRAGTools(), ctx
}

func storeImportedTable(t *testing.T, db *DB, source string, columns []ImportedColumn, rows [][]any) *ImportedTable {
	t.Helper()
	ctx := context.Background()
	w, err := db.BeginImportedTable(ctx, ImportedTableSpec{SourceTable: source, Source: "test", Columns: columns})
	if err != nil {
		t.Fatalf("begin %q: %v", source, err)
	}
	if err := w.Append(ctx, rows); err != nil {
		t.Fatalf("append %q: %v", source, err)
	}
	stored, err := w.Commit(ctx)
	if err != nil {
		t.Fatalf("commit %q: %v", source, err)
	}
	return stored
}

func queryImported(t *testing.T, tools *GraphRAGToolbox, args map[string]any) (*ImportedTableQueryResponse, string, error) {
	t.Helper()
	input, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tools.Call(context.Background(), "imported_tables_query", input)
	if err != nil {
		return nil, "", err
	}
	resp := out.(*ImportedTableQueryResponse)
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("answer is not JSON: %v", err)
	}
	return resp, string(encoded), nil
}

func TestImportedTablesListDescribeAndQuery(t *testing.T) {
	_, tools, ctx := openImportedTablesBrain(t)

	out, err := tools.Call(ctx, "imported_tables_list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	list := out.(*ToolImportedTablesListResponse)
	if list.Count != 2 || list.Tables[0].Name != "customers" || list.Tables[1].Name != "orders" {
		t.Fatalf("list = %+v", list)
	}
	orders := list.Tables[1]
	if orders.RowCount != 4 || orders.Source != "test" || orders.ImportedAt == "" || len(orders.Columns) != 5 ||
		orders.Columns[3] != (ImportedColumn{Name: "total", Type: ImportedTypeNumber}) {
		t.Fatalf("orders = %+v", orders)
	}
	if encoded, _ := json.Marshal(list); strings.Contains(string(encoded), importedTablePrefix) {
		t.Fatalf("the listing shows a physical table name: %s", encoded)
	}

	out, err = tools.Call(ctx, "imported_tables_describe", json.RawMessage(`{"table":"ORDERS","sample_rows":2}`))
	if err != nil {
		t.Fatal(err)
	}
	desc := out.(*ImportedTableDescription)
	if desc.RowCount != 4 || len(desc.SampleRows) != 2 || desc.SampleRows[0][2] != "eu" || desc.SampleRows[0][3] != 10.0 || desc.SampleRows[0][0] != int64(1) {
		t.Fatalf("describe = %+v", desc)
	}
	if _, err := tools.Call(ctx, "imported_tables_describe", json.RawMessage(`{"table":"messages"}`)); err == nil || !strings.Contains(err.Error(), "customers, orders") {
		t.Fatalf("describing an internal table: %v", err)
	}

	resp, _, err := queryImported(t, tools, map[string]any{
		"sql": `SELECT region, strftime('%Y-%m', ordered_at) AS month, avg(total) AS avg_order, count(*) AS n
		        FROM orders GROUP BY region, month ORDER BY region, month`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{"eu", "2026-01", 20.0, int64(2)}, {"eu", "2026-02", 20.0, int64(1)}, {"us", "2026-02", 5.0, int64(1)}}
	if got, _ := json.Marshal(resp.Rows); string(got) != asJSON(t, want) || resp.Truncated || resp.RowCount != 3 ||
		strings.Join(resp.Columns, ",") != "region,month,avg_order,n" {
		t.Fatalf("group by = %s %v", got, resp.Columns)
	}

	resp, _, err = queryImported(t, tools, map[string]any{
		"sql": `WITH spend AS (SELECT customer_id, sum(total) AS spent FROM orders GROUP BY customer_id)
		        SELECT c.name, s.spent, rank() OVER (ORDER BY s.spent DESC) AS place
		        FROM spend s JOIN customers c ON c.id = s.customer_id WHERE s.spent > ? ORDER BY place`,
		"params": []any{1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := asJSON(t, resp.Rows); got != `[["Ada",40,1],["Grace",25,2]]` {
		t.Fatalf("join = %s", got)
	}
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every statement here asks for something an imported-table query does not
// do. Each must be refused by the guard — not fail for some other reason —
// and none may change or reveal anything. Without the authorizer in
// core.RunGuardedSQLiteRead the reads below return the secret and this fails.
func TestImportedTablesQueryRefusesWritesAndTheStoresOwnTables(t *testing.T) {
	db, tools, ctx := openImportedTablesBrain(t)
	// An imported table that shares a name with one of the store's own: the
	// bare name must mean the imported one, and main.messages must stay shut.
	storeImportedTable(t, db, "messages", []ImportedColumn{{Name: "content", Type: ImportedTypeText}}, [][]any{{"an imported row"}})

	refused := []string{
		// writes and DDL
		`INSERT INTO orders VALUES (9, 9, 'x', 1, 'now')`,
		`INSERT INTO main.messages (id, content) VALUES ('x', 'y')`,
		`UPDATE main.documents SET content = ''`,
		`DELETE FROM main.messages`,
		`DROP TABLE orders`,
		`DROP TABLE main.documents`,
		`DROP VIEW temp.orders`,
		`CREATE TABLE loot (a)`,
		`CREATE TEMP TABLE loot AS SELECT * FROM orders`,
		`CREATE INDEX loot ON documents (content)`,
		`ALTER TABLE imported_tables ADD COLUMN x`,
		`REPLACE INTO imported_tables (source_table) VALUES ('x')`,
		`VACUUM`,
		`VACUUM INTO '` + filepath.Join(t.TempDir(), "copy.db") + `'`,
		`REINDEX`,
		`ANALYZE`,
		// the connection and its files
		`ATTACH DATABASE ':memory:' AS other`,
		`ATTACH DATABASE '` + filepath.Join(t.TempDir(), "other.db") + `' AS other`,
		`DETACH DATABASE main`,
		`PRAGMA query_only = OFF`,
		`PRAGMA writable_schema = ON`,
		`PRAGMA journal_mode = DELETE`,
		`PRAGMA table_info(messages)`,
		`PRAGMA database_list`,
		`BEGIN`,
		`COMMIT`,
		`ROLLBACK`,
		`SAVEPOINT s`,
		`SELECT load_extension('evil')`,
		// more than one statement
		`SELECT 1; SELECT 2`,
		`SELECT 1; DROP TABLE orders`,
		`SELECT 1; SELECT content FROM main.messages`,
		// the store's own tables, every way round
		`SELECT * FROM main.messages`,
		`SELECT content FROM main.messages`,
		`SELECT * FROM documents`,
		`SELECT content FROM documents`,
		`SELECT * FROM "documents"`,
		`SELECT * FROM [documents]`,
		`SELECT * FROM MAIN.Documents`,
		`SELECT count(*) FROM documents`,
		`SELECT count(*) FROM main.messages`,
		`SELECT 1 FROM documents`,
		`SELECT o.* FROM orders o, documents`,
		`SELECT o.* FROM orders o LEFT JOIN documents d ON 1 = 1`,
		`SELECT * FROM embeddings`,
		`SELECT * FROM chunks_fts`,
		`SELECT * FROM graph_nodes`,
		`SELECT * FROM imported_tables`,
		`SELECT * FROM sqlite_master`,
		`SELECT sql FROM sqlite_schema`,
		`SELECT count(*) FROM sqlite_master`,
		`SELECT * FROM main.sqlite_master`,
		`SELECT * FROM sqlite_temp_master`,
		`SELECT * FROM temp.sqlite_master`,
		`SELECT * FROM pragma_table_info('messages')`,
		`SELECT count(*) FROM pragma_database_list`,
		`SELECT * FROM pragma_table_list`,
		`SELECT * FROM dbstat`,
		`SELECT count(*) FROM dbstat`,
		// subqueries, CTEs, UNION
		`SELECT (SELECT content FROM documents LIMIT 1)`,
		`SELECT * FROM orders WHERE EXISTS (SELECT 1 FROM documents)`,
		`SELECT * FROM orders WHERE region IN (SELECT content FROM main.messages)`,
		`SELECT * FROM (SELECT content FROM documents)`,
		`WITH d AS (SELECT content FROM documents) SELECT * FROM d`,
		`WITH d AS (SELECT * FROM main.messages) SELECT count(*) FROM d`,
		`WITH documents AS (SELECT 1 AS content) SELECT count(*) FROM documents`,
		`WITH RECURSIVE r(x) AS (SELECT content FROM documents UNION ALL SELECT x FROM r LIMIT 2) SELECT * FROM r`,
		`SELECT region FROM orders UNION SELECT content FROM documents`,
		`SELECT region FROM orders UNION ALL SELECT content FROM main.messages`,
		`SELECT id, region FROM orders EXCEPT SELECT id, content FROM documents`,
		`SELECT j.value FROM json_each((SELECT json_group_array(content) FROM documents)) j`,
		`SELECT * FROM orders ORDER BY (SELECT content FROM documents LIMIT 1)`,
		`SELECT region, (SELECT count(*) FROM documents) FROM orders`,
		`EXPLAIN SELECT * FROM documents`,
		`EXPLAIN QUERY PLAN SELECT * FROM main.messages`,
	}
	for _, query := range refused {
		resp, encoded, err := queryImported(t, tools, map[string]any{"sql": query})
		if err == nil {
			t.Errorf("ran instead of being refused: %s\n  answer: %s", query, encoded)
			continue
		}
		if !errors.Is(err, core.ErrGuardedReadRefused) {
			t.Errorf("failed, but not by the guard: %s\n  error: %v", query, err)
		}
		if resp != nil || strings.Contains(err.Error(), "ZEBRA") {
			t.Errorf("leaked: %s: %v", query, err)
		}
	}

	// Refused by SQLite before the guard is asked: the name is a view, and
	// the text is run as given, with nothing wrapped around it to close.
	for _, query := range []string{
		`UPDATE orders SET total = 0`,
		`DELETE FROM orders`,
		`SELECT count(*) FROM orders); DELETE FROM orders; SELECT (1`,
	} {
		if _, encoded, err := queryImported(t, tools, map[string]any{"sql": query}); err == nil {
			t.Errorf("ran: %s\n  answer: %s", query, encoded)
		}
	}

	// The bare name is the imported table.
	resp, encoded, err := queryImported(t, tools, map[string]any{"sql": `SELECT * FROM messages`})
	if err != nil || len(resp.Rows) != 1 || resp.Rows[0][0] != "an imported row" || strings.Contains(encoded, "ZEBRA") {
		t.Fatalf("imported table named messages: %s, %v", encoded, err)
	}
	if resp, _, err := queryImported(t, tools, map[string]any{"sql": `SELECT count(*) FROM Messages`}); err != nil || resp.Rows[0][0] != int64(1) {
		t.Fatalf("count of the imported table named messages: %v, %v", resp, err)
	}

	// Nothing was written, dropped or created, and the store still works.
	var n int
	for query, want := range map[string]int{
		`SELECT count(*) FROM messages WHERE content LIKE '%ZEBRA%'`:                  1,
		`SELECT count(*) FROM documents`:                                              1,
		`SELECT count(*) FROM imported_tables`:                                        3,
		`SELECT count(*) FROM sqlite_master WHERE name = 'loot'`:                      0,
		`SELECT count(*) FROM sqlite_master WHERE name LIKE 'imported_tbl_%'`:         3,
		`SELECT count(*) FROM pragma_database_list WHERE name = 'other'`:              0,
		`SELECT count(*) FROM pragma_table_info('imported_tables') WHERE name = 'x'`:  0,
		`SELECT count(*) FROM sqlite_temp_master`:                                     0,
		`SELECT count(*) FROM pragma_journal_mode WHERE lower(journal_mode) <> 'wal'`: 0,
	} {
		if err := db.SQL().QueryRowContext(ctx, query).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d, %v; want %d", query, n, err, want)
		}
	}
	if resp, _, err := queryImported(t, tools, map[string]any{"sql": `SELECT count(*), sum(total) FROM orders`}); err != nil || resp.Rows[0][0] != int64(4) || resp.Rows[0][1] != 65.0 {
		t.Fatalf("orders after the attempts: %v, %v", resp, err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "after", UserID: "u1", Content: "the store still takes writes"}); err != nil {
		t.Fatalf("write after the attempts: %v", err)
	}
}

// What a query may do beyond a plain SELECT, so the guard is not mistaken
// for one that refuses everything.
func TestImportedTablesQueryAllowsOrdinaryAnalysis(t *testing.T) {
	_, tools, _ := openImportedTablesBrain(t)
	for query, want := range map[string]string{
		`SELECT count(*) FROM orders`:                                                                      `[[4]]`,
		`SELECT count(*) FROM "orders" -- trailing comment`:                                                `[[4]]`,
		`SELECT count(*) FROM temp.orders;`:                                                                `[[4]]`,
		`WITH big AS (SELECT * FROM orders WHERE total >= 20) SELECT count(*) FROM big`:                    `[[2]]`,
		`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n WHERE x < 5) SELECT sum(x) FROM n`: `[[15]]`,
		`SELECT name FROM customers WHERE id IN (SELECT customer_id FROM orders WHERE region = 'us')`:      `[["Grace"]]`,
		`SELECT region FROM orders UNION SELECT name FROM customers ORDER BY 1`:                            `[["Ada"],["Grace"],["eu"],["us"]]`,
		`SELECT j.value FROM json_each('[1,2]') j`:                                                         `[[1],[2]]`,
		`SELECT upper(region), round(max(total)) FROM orders GROUP BY 1 HAVING count(*) > 1`:               `[["EU",30]]`,
		`SELECT 1e999, -1e999`: `[["+Inf","-Inf"]]`,
	} {
		resp, _, err := queryImported(t, tools, map[string]any{"sql": query})
		if err != nil {
			t.Errorf("%s: %v", query, err)
			continue
		}
		if got := asJSON(t, resp.Rows); got != want {
			t.Errorf("%s = %s, want %s", query, got, want)
		}
	}
}

func TestImportedTablesQueryRowAndTimeLimits(t *testing.T) {
	db, tools, ctx := openImportedTablesBrain(t)

	resp, _, err := queryImported(t, tools, map[string]any{"sql": `SELECT id FROM orders ORDER BY id`, "max_rows": 3})
	if err != nil || resp.RowCount != 3 || len(resp.Rows) != 3 || !resp.Truncated {
		t.Fatalf("max_rows 3 of 4: %+v, %v", resp, err)
	}
	resp, _, err = queryImported(t, tools, map[string]any{"sql": `SELECT id FROM orders`, "max_rows": 4})
	if err != nil || resp.RowCount != 4 || resp.Truncated {
		t.Fatalf("max_rows 4 of 4: %+v, %v", resp, err)
	}

	const endless = `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT x FROM n`
	// The tool's default row cap, and the facade's ceiling on a larger ask.
	resp, _, err = queryImported(t, tools, map[string]any{"sql": endless})
	if err != nil || resp.RowCount != defaultImportedToolRows || !resp.Truncated {
		t.Fatalf("default cap: %d rows, truncated %v, %v", resp.RowCount, resp.Truncated, err)
	}
	big, err := db.QueryImportedTables(ctx, ImportedTableQueryRequest{SQL: endless, MaxRows: maxImportedQueryRows * 5})
	if err != nil || big.RowCount != maxImportedQueryRows || !big.Truncated {
		t.Fatalf("ceiling: %d rows, %v", big.RowCount, err)
	}

	_, _, err = queryImported(t, tools, map[string]any{"sql": `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT count(*) FROM n`, "timeout_ms": 100})
	if !errors.Is(err, core.ErrGuardedReadTimeout) {
		t.Fatalf("endless count: %v, want a timeout", err)
	}
	// A query that finds its first rows at once and then never another.
	_, _, err = queryImported(t, tools, map[string]any{"sql": `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT x FROM n WHERE x < 3`, "timeout_ms": 100})
	if !errors.Is(err, core.ErrGuardedReadTimeout) {
		t.Fatalf("endless scan after the first rows: %v, want a timeout", err)
	}
	if resp, _, err := queryImported(t, tools, map[string]any{"sql": `SELECT count(*) FROM orders`}); err != nil || resp.Rows[0][0] != int64(4) {
		t.Fatalf("query after a timeout: %v, %v", resp, err)
	}
}

func TestImportedTableNamesFromTheSourceAreNeverSQL(t *testing.T) {
	db, tools, ctx := openImportedTablesBrain(t)
	hostileTable := `x"; DROP TABLE documents; --`
	hostileColumn := `c"); DELETE FROM messages; --`
	stored := storeImportedTable(t, db, hostileTable,
		[]ImportedColumn{
			{Name: hostileColumn, Type: ImportedTypeText},
			{Name: "ID", Type: ImportedTypeInteger},
			{Name: "id", Type: ImportedTypeInteger},
			{Name: "", Type: "geometry"},
			{Name: "line\nbreak\x00nul", Type: ImportedTypeText},
			{Name: "列 名", Type: ImportedTypeText},
		},
		[][]any{{"v", int64(1), int64(2), "blank", "ctl", "cjk"}})
	if stored.Name != hostileTable {
		t.Fatalf("name = %q", stored.Name)
	}
	names := make([]string, len(stored.Columns))
	for i, c := range stored.Columns {
		names[i] = c.Name
	}
	if got := strings.Join(names, "|"); got != hostileColumn+"|ID|id_2|column_4|line_break_nul|列 名" {
		t.Fatalf("columns = %q", got)
	}
	if stored.Columns[2].SourceName != "id" || stored.Columns[3].Type != ImportedTypeText || stored.Columns[0].SourceName != "" {
		t.Fatalf("columns = %+v", stored.Columns)
	}

	// Names SQLite reserves or cannot tell apart get a different query name.
	for source, want := range map[string]string{
		"sqlite_master": "t_sqlite_master",
		"ORDERS":        "ORDERS_2", // "orders" is taken, and SQL names ignore case
		"   ":           "table",
		"main.messages": "main.messages",
		"tab\tname":     "tab_name",
	} {
		got := storeImportedTable(t, db, source, []ImportedColumn{{Name: "a"}}, [][]any{{"1"}})
		if got.Name != want || got.SourceTable != source {
			t.Errorf("source %q stored as %q, want %q", source, got.Name, want)
		}
	}

	resp, _, err := queryImported(t, tools, map[string]any{
		"sql": `SELECT "c""); DELETE FROM messages; --", id_2, "列 名" FROM "x""; DROP TABLE documents; --"`,
	})
	if err != nil || asJSON(t, resp.Rows) != `[["v",2,"cjk"]]` {
		t.Fatalf("hostile names, quoted: %v, %v", resp, err)
	}
	if resp, _, err := queryImported(t, tools, map[string]any{"sql": `SELECT count(*) FROM "main.messages"`}); err != nil || resp.Rows[0][0] != int64(1) {
		t.Fatalf(`table named "main.messages": %v, %v`, resp, err)
	}
	if resp, _, err := queryImported(t, tools, map[string]any{"sql": `SELECT a FROM t_sqlite_master`}); err != nil || resp.Rows[0][0] != "1" {
		t.Fatalf("table from a source called sqlite_master: %v, %v", resp, err)
	}
	desc, err := db.DescribeImportedTable(ctx, hostileTable, 0)
	if err != nil || desc.RowCount != 1 || len(desc.SampleRows) != 1 {
		t.Fatalf("describe by hostile name: %+v, %v", desc, err)
	}

	var documents, secrets int
	if err := db.SQL().QueryRowContext(ctx, `SELECT (SELECT count(*) FROM documents), (SELECT count(*) FROM messages WHERE content LIKE '%ZEBRA%')`).Scan(&documents, &secrets); err != nil || documents != 1 || secrets != 1 {
		t.Fatalf("the store's own tables after hostile names: %d documents, %d memories, %v", documents, secrets, err)
	}
}

func TestImportingATableAgainReplacesItOnCommit(t *testing.T) {
	db, tools, ctx := openImportedTablesBrain(t)
	count := func() string {
		resp, _, err := queryImported(t, tools, map[string]any{"sql": `SELECT count(*), group_concat(name, ',') FROM customers`})
		if err != nil {
			t.Fatal(err)
		}
		return asJSON(t, resp.Rows)
	}
	columns := []ImportedColumn{{Name: "id", Type: ImportedTypeInteger}, {Name: "name", Type: ImportedTypeText}, {Name: "tier", Type: ImportedTypeText}}

	// Staged but not committed: the previous import is what queries read.
	w, err := db.BeginImportedTable(ctx, ImportedTableSpec{SourceTable: "customers", Columns: columns})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ctx, [][]any{{int64(1), "Linus", "gold"}}); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != `[[2,"Ada,Grace"]]` {
		t.Fatalf("before commit: %s", got)
	}
	if err := w.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != `[[2,"Ada,Grace"]]` {
		t.Fatalf("after abort: %s", got)
	}

	stored := storeImportedTable(t, db, "customers", columns, [][]any{{int64(1), "Linus", "gold"}})
	if stored.Name != "customers" || stored.RowCount != 1 {
		t.Fatalf("stored = %+v", stored)
	}
	if got := count(); got != `[[1,"Linus"]]` {
		t.Fatalf("after re-import: %s", got)
	}
	tables, err := db.ListImportedTables(ctx)
	if err != nil || len(tables) != 2 || len(tables[0].Columns) != 3 {
		t.Fatalf("registry after re-import: %+v, %v", tables, err)
	}
	// One physical table per registered table: the old rows and the aborted
	// staging table are gone.
	var physical int
	if err := db.SQL().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'imported_tbl_%'`).Scan(&physical); err != nil || physical != 2 {
		t.Fatalf("physical tables = %d, %v", physical, err)
	}

	if err := db.DropImportedTable(ctx, "customers"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := queryImported(t, tools, map[string]any{"sql": `SELECT * FROM customers`}); err == nil {
		t.Fatal("a dropped table is still queryable")
	}
}

// On a backend with no way to confine the query, nothing runs at all.
func TestImportedTablesAreRefusedOffSQLite(t *testing.T) {
	db, tools, ctx := openImportedTablesBrain(t)
	db.dialect = sqldialect.For(sqldialect.Postgres)
	defer func() { db.dialect = sqldialect.For(sqldialect.SQLite) }()

	if _, err := db.BeginImportedTable(ctx, ImportedTableSpec{SourceTable: "t", Columns: []ImportedColumn{{Name: "a"}}}); !errors.Is(err, ErrImportedTablesUnsupported) {
		t.Errorf("begin: %v", err)
	}
	if _, err := db.ListImportedTables(ctx); !errors.Is(err, ErrImportedTablesUnsupported) {
		t.Errorf("list: %v", err)
	}
	if err := db.DropImportedTable(ctx, "orders"); !errors.Is(err, ErrImportedTablesUnsupported) {
		t.Errorf("drop: %v", err)
	}
	for name, input := range map[string]string{
		"imported_tables_list":     `{}`,
		"imported_tables_describe": `{"table":"orders"}`,
		"imported_tables_query":    `{"sql":"SELECT 1"}`,
	} {
		if _, err := tools.Call(ctx, name, json.RawMessage(input)); !errors.Is(err, ErrImportedTablesUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestImportedTablesWithNothingImported(t *testing.T) {
	db, tools, ctx := openIngestBrain(t, "no-tables")
	out, err := tools.Call(ctx, "imported_tables_list", json.RawMessage(`{}`))
	if err != nil || asJSON(t, out) != `{"tables":[],"count":0}` {
		t.Fatalf("list = %s, %v", asJSON(t, out), err)
	}
	if _, err := tools.Call(ctx, "imported_tables_query", json.RawMessage(`{"sql":"SELECT 1"}`)); err == nil {
		t.Fatal("a query with no imported tables ran")
	}
	// Reading did not create the registry.
	var n int
	if err := db.SQL().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = ?`, importedTableRegistry).Scan(&n); err != nil || n != 0 {
		t.Fatalf("registry after reads: %d, %v", n, err)
	}
}
