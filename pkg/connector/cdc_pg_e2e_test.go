package connector

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestPostgresLogicalCDCEndToEnd(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_PG_REPL_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_PG_REPL_DSN to a wal_level=logical Postgres superuser DSN")
	}
	ctx := context.Background()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	mustExec(t, admin,
		`DROP TABLE IF EXISTS cdc_repl_users`,
		`DROP PUBLICATION IF EXISTS cdc_pub`,
		`SELECT pg_drop_replication_slot('cdc_slot') WHERE EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name='cdc_slot')`,
		`CREATE TABLE cdc_repl_users(id int primary key, name text, phone text)`,
		`CREATE PUBLICATION cdc_pub FOR TABLE cdc_repl_users`,
	)

	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	plan := MaskingPlan{Columns: []ColumnRule{
		{Table: "cdc_repl_users", Column: "id", Action: ActionKeep},
		{Table: "cdc_repl_users", Column: "name", Action: ActionKeep},
		{Table: "cdc_repl_users", Column: "phone", PiiKind: PiiPhone, Action: ActionMask},
	}}
	plan.Sign("tester", time.Unix(1, 0))
	d, _ := NewDesensitizer(plan, DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	cp, _ := NewSQLiteCheckpointStore(db)
	src, err := NewPostgresCDCSource(dsn, PostgresCDCOptions{
		Publication: "cdc_pub", Slot: "cdc_slot",
		Tables: map[string][]string{"cdc_repl_users": {"id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(db, src, WatcherOptions{
		SourceKey: "pgrepl", Desensitizer: d, Checkpoint: cp,
		Mapping: importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
			"cdc_repl_users": {RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone}", IDColumn: "id"}},
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

	time.Sleep(500 * time.Millisecond)
	mustExec(t, admin, `INSERT INTO cdc_repl_users(id,name,phone) VALUES (1,'Alice','13812341234')`)
	waitFor(t, 8*time.Second, func() bool {
		got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
		return err == nil && indexOf(got.Knowledge.Content, "Alice") >= 0
	})
	got, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if indexOf(got.Knowledge.Content, "13812341234") >= 0 {
		t.Fatalf("raw PII leaked via CDC: %q", got.Knowledge.Content)
	}

	mustExec(t, admin, `UPDATE cdc_repl_users SET name='Renamed' WHERE id=1`)
	waitFor(t, 8*time.Second, func() bool {
		got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
		return err == nil && indexOf(got.Knowledge.Content, "Renamed") >= 0
	})

	mustExec(t, admin, `DELETE FROM cdc_repl_users WHERE id=1`)
	waitFor(t, 8*time.Second, func() bool {
		_, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
		return err != nil
	})
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
