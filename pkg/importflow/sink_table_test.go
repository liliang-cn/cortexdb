// pkg/importflow/sink_table_test.go
package importflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

const ordersCSV = `id,customer_id,region,total,ordered_at,note
1,10,eu,10.50,2026-01-05,first
2,10,eu,30,2026-01-20,
3,11,us,5.25,2026-02-01,gift
4,11,eu,20,2026-02-11,
5,12,us,,2026-02-12,unpriced
`

func tableQuery(t *testing.T, db *cortexdb.DB, sql string) string {
	t.Helper()
	input, _ := json.Marshal(map[string]any{"sql": sql})
	out, err := db.GraphRAGTools().Call(context.Background(), "imported_tables_query", input)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	rows, err := json.Marshal(out.(*cortexdb.ImportedTableQueryResponse).Rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(rows)
}

func importCSV(t *testing.T, db *cortexdb.DB, table, data string, plan TablePlan) *Report {
	t.Helper()
	src, err := NewCSVSource(strings.NewReader(data), CSVOptions{Table: table})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := New(db).Run(context.Background(), src, MappingPlan{Tables: map[string]TablePlan{table: plan}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("Run errors: %v", rep.Errors)
	}
	return rep
}

func TestTableSinkKeepsACSVAsATypedTable(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	rep := importCSV(t, db, "orders", ordersCSV, TablePlan{
		RAG:   &RAGPlan{ContentTmpl: "order {id}: {note}", IDColumn: "id"},
		Table: &TableStorePlan{},
	})
	if rep.TablesStored != 1 || rep.TableRowsStored != 5 || rep.RowsRead != 5 {
		t.Fatalf("report = %+v", rep)
	}
	// The other sinks still ran for the same rows.
	if rep.ChunksIndexed != 5 {
		t.Fatalf("ChunksIndexed = %d; want 5", rep.ChunksIndexed)
	}

	desc, err := db.DescribeImportedTable(ctx, "orders", 2)
	if err != nil {
		t.Fatal(err)
	}
	if desc.RowCount != 5 || desc.Source != "csv" || desc.SourceTable != "orders" {
		t.Fatalf("describe = %+v", desc)
	}
	var types []string
	for _, c := range desc.Columns {
		types = append(types, c.Name+":"+c.Type)
	}
	if got := strings.Join(types, " "); got != "id:integer customer_id:integer region:text total:number ordered_at:text note:text" {
		t.Fatalf("columns = %s", got)
	}
	if got, _ := json.Marshal(desc.SampleRows); string(got) != `[[1,10,"eu",10.5,"2026-01-05","first"],[2,10,"eu",30,"2026-01-20",""]]` {
		t.Fatalf("sample rows = %s", got)
	}

	// Stored as numbers, not as the text the CSV held: typeof says so, an
	// empty numeric cell is NULL, and the sums are arithmetic.
	if got := tableQuery(t, db, `SELECT typeof(id), typeof(total), typeof(region) FROM orders WHERE id = 1`); got != `[["integer","real","text"]]` {
		t.Fatalf("storage classes = %s", got)
	}
	if got := tableQuery(t, db, `SELECT count(*), count(total) FROM orders`); got != `[[5,4]]` {
		t.Fatalf("NULL for an empty number = %s", got)
	}
	got := tableQuery(t, db, `SELECT region, strftime('%Y-%m', ordered_at) AS month, count(*), sum(total), avg(total)
		FROM orders GROUP BY region, month ORDER BY region, month`)
	if got != `[["eu","2026-01",2,40.5,20.25],["eu","2026-02",1,20,20],["us","2026-02",2,5.25,5.25]]` {
		t.Fatalf("group by = %s", got)
	}
}

const shopDump = `
CREATE TABLE customers (
  id INT PRIMARY KEY,
  name VARCHAR(64),
  joined TIMESTAMP
);
CREATE TABLE orders (
  id INT PRIMARY KEY,
  customer_id INT,
  total DECIMAL(10,2)
);
INSERT INTO customers (id, name, joined) VALUES (10, 'Ada', '2025-03-01 09:00:00'), (11, 'Grace', NULL);
INSERT INTO orders (id, customer_id, total) VALUES (1, 10, 10.50), (2, 10, 30.00), (3, 11, 5.25);
`

func TestTableSinkJoinsTwoTablesOfADump(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	src, err := NewSQLDumpSource(strings.NewReader(shopDump), DumpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := src.Schemas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing planned but the tables themselves.
	rep, err := New(db, WithProvenance(map[string]string{"authority": "shop-db"})).Run(ctx, src, KeepTables(MappingPlan{}, schemas))
	if err != nil || len(rep.Errors) > 0 {
		t.Fatalf("Run: %v %v", err, rep.Errors)
	}
	if rep.TablesStored != 2 || rep.TableRowsStored != 5 || rep.ChunksIndexed != 0 || rep.TriplesCreated != 0 || rep.Skipped != 0 {
		t.Fatalf("report = %+v", rep)
	}

	tables, err := db.ListImportedTables(ctx)
	if err != nil || len(tables) != 2 {
		t.Fatalf("tables = %+v, %v", tables, err)
	}
	customers := tables[0]
	if customers.Name != "customers" || customers.Source != "sql_dump" || customers.Provenance["authority"] != "shop-db" ||
		customers.Columns[0].Type != "integer" || customers.Columns[1].Type != "text" || customers.Columns[2].Type != "timestamp" {
		t.Fatalf("customers = %+v", customers)
	}
	if tables[1].Columns[2].Type != "number" {
		t.Fatalf("orders.total type = %q", tables[1].Columns[2].Type)
	}

	got := tableQuery(t, db, `SELECT c.name, count(o.id), sum(o.total), c.joined IS NULL
		FROM customers c JOIN orders o ON o.customer_id = c.id GROUP BY c.name ORDER BY c.name`)
	if got != `[["Ada",2,40.5,0],["Grace",1,5.25,1]]` {
		t.Fatalf("join = %s", got)
	}
}

// Importing a table again replaces its rows, as the RAG sink replaces a
// row's chunk and the KG sink re-upserts its triples: one copy, the latest.
func TestTableSinkReimportReplacesTheRows(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	plan := TablePlan{RAG: &RAGPlan{ContentTmpl: "{name}", IDColumn: "id"}, Table: &TableStorePlan{}}

	importCSV(t, db, "people", "id,name\n1,Ada\n2,Grace\n3,Linus\n", plan)
	importCSV(t, db, "people", "id,name\n1,Ada\n2,Grace\n3,Linus\n", plan)
	if got := tableQuery(t, db, `SELECT count(*) FROM people`); got != `[[3]]` {
		t.Fatalf("same file twice = %s rows; want 3", got)
	}

	// A new file with fewer rows and another column: the table is the file.
	rep := importCSV(t, db, "people", "id,name,team\n7,Margaret,apollo\n", plan)
	if rep.TablesStored != 1 || rep.TableRowsStored != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if got := tableQuery(t, db, `SELECT id, name, team FROM people`); got != `[[7,"Margaret","apollo"]]` {
		t.Fatalf("after re-import = %s", got)
	}
	tables, err := db.ListImportedTables(ctx)
	if err != nil || len(tables) != 1 || tables[0].RowCount != 1 || len(tables[0].Columns) != 3 {
		t.Fatalf("registry = %+v, %v", tables, err)
	}

	// A run that fails leaves the previous import in place.
	src, err := NewCSVSource(strings.NewReader("id,name\n1,x\n2,y\n"), CSVOptions{Table: "people"})
	if err != nil {
		t.Fatal(err)
	}
	failing := &failingSource{Source: src, after: 1}
	if _, err := New(db).Run(ctx, failing, MappingPlan{Tables: map[string]TablePlan{"people": {Table: &TableStorePlan{}}}}); err == nil {
		t.Fatal("a source that fails mid-stream did not fail the run")
	}
	if got := tableQuery(t, db, `SELECT id, name FROM people`); got != `[[7,"Margaret"]]` {
		t.Fatalf("after a failed re-import = %s", got)
	}
	var physical int
	if err := db.SQL().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'imported_tbl_%'`).Scan(&physical); err != nil || physical != 1 {
		t.Fatalf("physical tables = %d, %v; want 1", physical, err)
	}
}

type failingSource struct {
	Source
	after int
}

func (f *failingSource) Records(ctx context.Context, fn func(Record) error) error {
	n := 0
	return f.Source.Records(ctx, func(r Record) error {
		if n >= f.after {
			return errors.New("source went away")
		}
		n++
		return fn(r)
	})
}

func TestTableSinkTakesHostileNamesAsData(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: "keep", Content: "a document that must survive"}); err != nil {
		t.Fatal(err)
	}
	table := `t"; DROP TABLE documents; --`
	data := "\"a\"\"); DROP TABLE documents; --\",select,Order Total,ORDER TOTAL\n" +
		"x'); DELETE FROM documents; --,2,3.5,4\n"
	rep := importCSV(t, db, table, data, TablePlan{Table: &TableStorePlan{}})
	if rep.TablesStored != 1 || rep.TableRowsStored != 1 {
		t.Fatalf("report = %+v", rep)
	}

	desc, err := db.DescribeImportedTable(ctx, table, 5)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range desc.Columns {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, "|"); got != `a"); DROP TABLE documents; --|select|Order Total|ORDER TOTAL_2` {
		t.Fatalf("columns = %s", got)
	}
	if got, _ := json.Marshal(desc.SampleRows); string(got) != `[["x'); DELETE FROM documents; --",2,3.5,4]]` {
		t.Fatalf("rows = %s", got)
	}
	got := tableQuery(t, db, `SELECT "select", "Order Total" + "ORDER TOTAL_2" FROM "t""; DROP TABLE documents; --"`)
	if got != `[[2,7.5]]` {
		t.Fatalf("query over hostile names = %s", got)
	}

	var documents int
	if err := db.SQL().QueryRowContext(ctx, `SELECT count(*) FROM documents`).Scan(&documents); err != nil || documents != 1 {
		t.Fatalf("documents = %d, %v", documents, err)
	}
	// And the import gave the query path nothing new to read.
	input, _ := json.Marshal(map[string]any{"sql": `SELECT content FROM documents`})
	if _, err := db.GraphRAGTools().Call(ctx, "imported_tables_query", input); !errors.Is(err, core.ErrGuardedReadRefused) {
		t.Fatalf("reading documents: %v", err)
	}
}

func TestTablePlanSkipNameAndGoal(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Tables are kept only where the plan says so.
	importCSV(t, db, "plain", "id,name\n1,Ada\n", TablePlan{RAG: &RAGPlan{ContentTmpl: "{name}"}})
	if tables, err := db.ListImportedTables(ctx); err != nil || len(tables) != 0 {
		t.Fatalf("a plan without a table kept one: %+v, %v", tables, err)
	}

	importCSV(t, db, "export_2026_final", "id,name\n1,Ada\n", TablePlan{Table: &TableStorePlan{Name: "people"}})
	if got := tableQuery(t, db, `SELECT name FROM people`); got != `[["Ada"]]` {
		t.Fatalf("named table = %s", got)
	}

	schemas := []Schema{{Table: "a"}, {Table: "b"}, {Table: "c"}}
	plan := KeepTables(MappingPlan{Tables: map[string]TablePlan{
		"a": {RAG: &RAGPlan{ContentTmpl: "{x}"}},
		"b": {Skip: true},
	}}, schemas)
	if plan.Tables["a"].Table == nil || plan.Tables["a"].RAG == nil || plan.Tables["b"].Table != nil || plan.Tables["c"].Table == nil {
		t.Fatalf("KeepTables = %+v", plan.Tables)
	}

	// Goal.BuildTables adds the tables to whatever the inferer proposed.
	src, err := NewCSVSource(strings.NewReader("id,name\n1,Ada\n"), CSVOptions{Table: "staff"})
	if err != nil {
		t.Fatal(err)
	}
	im := New(db, WithMappingInferer(stubInferer{}))
	inferred, err := im.Plan(ctx, src, Goal{BuildRAG: true, BuildTables: true})
	if err != nil || inferred.Tables["staff"].Table == nil {
		t.Fatalf("Plan with BuildTables = %+v, %v", inferred, err)
	}
	if inferred, err := im.Plan(ctx, src, Goal{BuildRAG: true}); err != nil || inferred.Tables["staff"].Table != nil {
		t.Fatalf("Plan without BuildTables = %+v, %v", inferred, err)
	}
}

type stubInferer struct{}

func (stubInferer) InferPlan(context.Context, []Schema, Goal) (MappingPlan, error) {
	return MappingPlan{Tables: map[string]TablePlan{}}, nil
}

func TestToolboxRunKeepTables(t *testing.T) {
	db := testDB(t)
	tb := NewToolbox(New(db))
	input, _ := json.Marshal(map[string]any{
		"format":      "csv",
		"table":       "orders",
		"data":        ordersCSV,
		"plan":        map[string]any{"tables": map[string]any{}},
		"keep_tables": true,
	})
	out, err := tb.Call(context.Background(), "importflow_run", input)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if rep := out.(*Report); rep.TablesStored != 1 || rep.TableRowsStored != 5 || rep.Skipped != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if got := tableQuery(t, db, `SELECT region, round(sum(total), 2) FROM orders GROUP BY region ORDER BY region`); got != `[["eu",60.5],["us",5.25]]` {
		t.Fatalf("totals = %s", got)
	}

	// The same through the plan's own field, as importflow_run documents it.
	input, _ = json.Marshal(map[string]any{
		"format": "csv", "table": "orders2", "data": ordersCSV,
		"plan": map[string]any{"tables": map[string]any{"orders2": map[string]any{"table": map[string]any{}}}},
	})
	if _, err := tb.Call(context.Background(), "importflow_run", input); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := tableQuery(t, db, `SELECT count(*) FROM orders2`); got != `[[5]]` {
		t.Fatalf("orders2 = %s", got)
	}
}

func TestTypedValue(t *testing.T) {
	for _, c := range []struct {
		typ, in string
		want    any
	}{
		{"integer", "42", int64(42)},
		{"integer", "", nil},
		{"integer", "4.5", 4.5},
		{"integer", "n/a", "n/a"},
		{"number", "1e3", 1000.0},
		{"number", "", nil},
		{"number", "abc", "abc"},
		{"timestamp", "2026-01-05", "2026-01-05"},
		{"timestamp", "", nil},
		{"text", "", ""},
		{"text", "007", "007"},
		{"", "007", "007"},
	} {
		if got := typedValue(c.typ, c.in); got != c.want {
			t.Errorf("typedValue(%q, %q) = %#v; want %#v", c.typ, c.in, got, c.want)
		}
	}
}
