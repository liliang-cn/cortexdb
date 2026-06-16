package connector

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestMySQLBinlogCDCEndToEnd(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_MYSQL_BINLOG_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_MYSQL_BINLOG_DSN to a ROW-binlog MySQL")
	}
	ctx := context.Background()

	admin, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	mustExec(t, admin,
		"DROP TABLE IF EXISTS cdc_binlog_users",
		"CREATE TABLE cdc_binlog_users(id int primary key, name varchar(64), phone varchar(32))",
	)

	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	plan := MaskingPlan{Columns: []ColumnRule{
		{Table: "cdc_binlog_users", Column: "id", Action: ActionKeep},
		{Table: "cdc_binlog_users", Column: "name", Action: ActionKeep},
		{Table: "cdc_binlog_users", Column: "phone", PiiKind: PiiPhone, Action: ActionMask},
	}}
	plan.Sign("tester", time.Unix(1, 0))
	d, _ := NewDesensitizer(plan, DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	cp, _ := NewSQLiteCheckpointStore(db)
	src, err := NewMySQLBinlogSource(dsn, MySQLBinlogOptions{
		ServerID: 1101, Tables: map[string][]string{"cdc_binlog_users": {"id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(db, src, WatcherOptions{
		SourceKey: "mybinlog", Desensitizer: d, Checkpoint: cp,
		Mapping: importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
			"cdc_binlog_users": {RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone}", IDColumn: "id"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	defer func() { cancel(); <-done }()

	time.Sleep(1 * time.Second)
	mustExec(t, admin, "INSERT INTO cdc_binlog_users(id,name,phone) VALUES (1,'Alice','13812341234')")
	waitFor(t, 10*time.Second, func() bool {
		got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
		return err == nil && indexOf(got.Knowledge.Content, "Alice") >= 0
	})
	got, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if indexOf(got.Knowledge.Content, "13812341234") >= 0 {
		t.Fatalf("raw PII leaked via binlog CDC: %q", got.Knowledge.Content)
	}

	mustExec(t, admin, "UPDATE cdc_binlog_users SET name='Renamed' WHERE id=1")
	waitFor(t, 10*time.Second, func() bool {
		got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
		return err == nil && indexOf(got.Knowledge.Content, "Renamed") >= 0
	})

	mustExec(t, admin, "DELETE FROM cdc_binlog_users WHERE id=1")
	waitFor(t, 10*time.Second, func() bool {
		_, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
		return err != nil
	})
}
