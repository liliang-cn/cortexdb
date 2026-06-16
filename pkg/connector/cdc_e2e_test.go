package connector

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func cdcE2EPlan(table string) MaskingPlan {
	p := MaskingPlan{Columns: []ColumnRule{
		{Table: table, Column: "id", Action: ActionKeep},
		{Table: table, Column: "name", Action: ActionKeep},
		{Table: table, Column: "phone", PiiKind: PiiPhone, Action: ActionMask},
		{Table: table, Column: "updated_at", Action: ActionKeep},
	}}
	p.Sign("tester", time.Unix(1, 0))
	return p
}

func cdcE2EMapping(table string) importflow.MappingPlan {
	return importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		table: {RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone}", IDColumn: "id"}},
	}}
}

// runCDCSync runs one polling cycle end to end against the given driver/dsn. The
// checkpoint store is wired into the watcher so the per-table watermark persists
// between calls — that is what makes the second-sync (resume) assertion real.
func runCDCSync(t *testing.T, driver, dsn string, db *cortexdb.DB, cp CheckpointStore, table string) {
	t.Helper()
	d, err := NewDesensitizer(cdcE2EPlan(table), DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	if err != nil {
		t.Fatal(err)
	}
	src, err := NewPollingChangeSource(driver, dsn, PollingOptions{
		Tables: []TableCursor{{Table: table, CursorColumn: "updated_at", KeyColumns: []string{"id"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	w, err := NewWatcher(db, src, WatcherOptions{SourceKey: "e2e", Desensitizer: d, Mapping: cdcE2EMapping(table), Checkpoint: cp})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCDCPollingEndToEndPostgres(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_PG_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_PG_DSN; test seeds its own table cdc_e2e_users")
	}
	ctx := context.Background()
	sdb, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	mustExec(t, sdb,
		`DROP TABLE IF EXISTS cdc_e2e_users`,
		`CREATE TABLE cdc_e2e_users(id int primary key, name text, phone text, updated_at timestamptz default now())`,
		`INSERT INTO cdc_e2e_users(id,name,phone) VALUES (1,'Alice','13812341234')`,
	)

	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cp, _ := NewSQLiteCheckpointStore(db)

	runCDCSync(t, "pgx", dsn, db, cp, "cdc_e2e_users")
	got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if err != nil {
		t.Fatalf("expected chunk after first sync: %v", err)
	}
	if indexOf(got.Knowledge.Content, "13812341234") >= 0 {
		t.Fatalf("raw PII leaked: %q", got.Knowledge.Content)
	}
	if indexOf(got.Knowledge.Content, "Alice") < 0 {
		t.Fatalf("missing kept field: %q", got.Knowledge.Content)
	}

	// update the row (bump cursor) and re-sync; only the changed row should flow.
	time.Sleep(10 * time.Millisecond)
	mustExec(t, sdb, `UPDATE cdc_e2e_users SET name='Renamed', updated_at=now() WHERE id=1`)
	runCDCSync(t, "pgx", dsn, db, cp, "cdc_e2e_users")
	got2, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if indexOf(got2.Knowledge.Content, "Renamed") < 0 {
		t.Fatalf("second sync did not pick up update: %q", got2.Knowledge.Content)
	}
}

func TestCDCPollingEndToEndMySQL(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_MYSQL_DSN; test seeds its own table cdc_e2e_users")
	}
	ctx := context.Background()
	sdb, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	mustExec(t, sdb,
		"DROP TABLE IF EXISTS cdc_e2e_users",
		"CREATE TABLE cdc_e2e_users(id int primary key, name varchar(64), phone varchar(32), updated_at datetime(3))",
		"INSERT INTO cdc_e2e_users(id,name,phone,updated_at) VALUES (1,'Alice','13812341234', NOW(3))",
	)

	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cp, _ := NewSQLiteCheckpointStore(db)

	runCDCSync(t, "mysql", dsn, db, cp, "cdc_e2e_users")
	got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if err != nil {
		t.Fatalf("expected chunk after first sync: %v", err)
	}
	if indexOf(got.Knowledge.Content, "13812341234") >= 0 {
		t.Fatalf("raw PII leaked: %q", got.Knowledge.Content)
	}
	if indexOf(got.Knowledge.Content, "Alice") < 0 {
		t.Fatalf("missing kept field: %q", got.Knowledge.Content)
	}

	time.Sleep(10 * time.Millisecond)
	mustExec(t, sdb, "UPDATE cdc_e2e_users SET name='Renamed', updated_at=NOW(3) WHERE id=1")
	runCDCSync(t, "mysql", dsn, db, cp, "cdc_e2e_users")
	got2, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if indexOf(got2.Knowledge.Content, "Renamed") < 0 {
		t.Fatalf("second sync did not pick up update: %q", got2.Knowledge.Content)
	}
}

func mustExec(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}
