# Connector CDC — Phase 3 (MySQL Binlog) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add the last CDC change source — MySQL binlog (ROW format) via `go-mysql` canal — so the existing `Watcher` streams inserts/updates/**deletes** from MySQL into RAG + knowledge graph, desensitized, with binlog-position resume. This completes Route B for both databases.

**Architecture:** `mysqlBinlogSource` builds a `canal.Canal` (initial mysqldump disabled — binlog-only), registers an `EventHandler` (embedding `canal.DummyEventHandler`, overriding `OnRow`/`OnRotate`) that decodes `canal.RowsEvent` (Insert/Update/Delete, using `Table.Columns` + `Table.PKColumns` for column names + key) into `connector.ChangeEvent`s and calls the `Changes` callback. `RunFrom(position)` blocks (the source's `Changes` is blocking by contract); a goroutine closes the canal on ctx cancel. It implements `positionCheckpointer` (binlog `file:pos`), so the `Watcher` seeds/saves position via `Checkpoint.Position`. Reuses the Phase 1/2 `Watcher.apply` (idempotent upsert + hard delete) verbatim.

**Tech Stack:** Go 1.25, `github.com/go-mysql-org/go-mysql` (v1.15.0, pure Go) packages `canal`, `mysql`, `replication`, `schema`; `github.com/go-sql-driver/mysql` (already a dep — used for `ParseDSN`); existing `pkg/connector`.

**Spec:** `docs/superpowers/specs/2026-06-16-connector-cdc-design.md` (Route B-MySQL).

**Verified go-mysql canal API** (v1.15.0; use EXACTLY):
- `canal.NewDefaultConfig() *canal.Config`; fields: `Addr, User, Password string`, `ServerID uint32`, `Flavor string` ("mysql"), `Dump canal.DumpConfig{ExecutionPath string}` (set `""` to disable the initial dump → binlog-only), `IncludeTableRegex []string`.
- `canal.NewCanal(cfg *canal.Config) (*canal.Canal, error)`; `c.SetEventHandler(h canal.EventHandler)`; `c.GetMasterPos() (mysql.Position, error)`; `c.RunFrom(pos mysql.Position) error` (blocking until error or `Close`); `c.Close()`.
- `canal.EventHandler` is a large interface; embed `canal.DummyEventHandler` (all methods return nil) and override only what you need. Required override here: `OnRow(e *canal.RowsEvent) error`, `OnRotate(h *replication.EventHeader, r *replication.RotateEvent) error`, `String() string`.
- `canal.RowsEvent{Action string, Table *schema.Table, Rows [][]any, Header *replication.EventHeader}`. Constants `canal.InsertAction`="insert", `canal.UpdateAction`="update", `canal.DeleteAction`="delete". UPDATE `Rows` is `[old0,new0,old1,new1,...]` (step 2, use the NEW row). INSERT/DELETE: one row per entry. `Rows[i][j]` aligns with `Table.Columns[j]`.
- `schema.Table{Schema, Name string, Columns []schema.TableColumn, PKColumns []int}`; `schema.TableColumn{Name string}`. PK names = `Columns[idx].Name for idx in PKColumns`.
- Cell types: `int64`/`uint64`/`float64`/`[]byte`/`string`/`nil`; format via a small helper (`[]byte`→string, nil→skip, else `fmt.Sprintf("%v")`).
- `replication.EventHeader{LogPos uint32}` (end position of the event); `replication.RotateEvent{NextLogName []byte}`.
- `mysql.Position{Name string, Pos uint32}`.
- DSN parsing: `github.com/go-sql-driver/mysql`.`ParseDSN(dsn string) (*mysql.Config, error)` → fields `Addr, User, Passwd, DBName string` (note: package alias collision — import the driver as `mysqldriver`).

**MySQL prerequisites (documented):** `log_bin=ON`, `binlog_format=ROW`, `binlog_row_image=FULL` (all defaults in MySQL 8), a user with `REPLICATION SLAVE, REPLICATION CLIENT` (root has it), and a unique `ServerID` for the canal client.

**Reused connector APIs:** `ChangeEvent{Op, Table, Key, Row, Position}`, `OpInsert/OpUpdate/OpDelete`, `ChangeSource`, `positionCheckpointer{LoadPosition(string)error}`, `Watcher`, `importflow.Record`.

---

### Task 1: MySQLBinlogSource

**Files:**
- Create: `pkg/connector/cdc_mysql.go`
- Test: `pkg/connector/cdc_mysql_test.go`

- [ ] **Step 1: Write `pkg/connector/cdc_mysql_test.go`**

```go
package connector

import (
	"os"
	"testing"
)

func TestMySQLBinlogSourceRequiresDSN(t *testing.T) {
	if _, err := NewMySQLBinlogSource("", MySQLBinlogOptions{ServerID: 1001}); err == nil {
		t.Fatal("expected error for empty DSN")
	}
}

func TestMySQLBinlogSourceConnects(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_MYSQL_BINLOG_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_MYSQL_BINLOG_DSN to a ROW-binlog MySQL (e.g. root:p@tcp(localhost:3306)/test)")
	}
	src, err := NewMySQLBinlogSource(dsn, MySQLBinlogOptions{
		ServerID: 1001, Tables: map[string][]string{"cdc_binlog_users": {"id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
```

- [ ] **Step 2: Run** `go test ./pkg/connector -run TestMySQLBinlogSource` → FAIL (undefined).

- [ ] **Step 3: Implement `pkg/connector/cdc_mysql.go`**

```go
package connector

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/go-mysql-org/go-mysql/schema"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// MySQLBinlogOptions configures the binlog change source.
type MySQLBinlogOptions struct {
	ServerID uint32              // unique replica id for this client (required; default 1001)
	Tables   map[string][]string // table -> PK column names (optional; falls back to Table.PKColumns)
}

// mysqlBinlogSource streams ROW-format binlog changes as ChangeEvents.
type mysqlBinlogSource struct {
	cfg      *canal.Config
	dbName   string
	opts     MySQLBinlogOptions
	canal    *canal.Canal
	startPos mysql.Position
}

// NewMySQLBinlogSource parses the (go-sql-driver) DSN and prepares a binlog
// reader. It does not start streaming until Changes is called.
func NewMySQLBinlogSource(dsn string, opts MySQLBinlogOptions) (ChangeSource, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("connector: mysql binlog DSN is required")
	}
	mc, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("connector: parse mysql dsn: %w", err)
	}
	if opts.ServerID == 0 {
		opts.ServerID = 1001
	}
	cfg := canal.NewDefaultConfig()
	cfg.Addr = mc.Addr
	cfg.User = mc.User
	cfg.Password = mc.Passwd
	cfg.ServerID = opts.ServerID
	cfg.Flavor = "mysql"
	cfg.Dump.ExecutionPath = "" // binlog-only; no initial mysqldump snapshot
	// Restrict to the configured tables in the connection's database.
	if mc.DBName != "" {
		var inc []string
		for table := range opts.Tables {
			inc = append(inc, regexpEscape(mc.DBName)+"\\."+regexpEscape(table))
		}
		cfg.IncludeTableRegex = inc
	}
	return &mysqlBinlogSource{cfg: cfg, dbName: mc.DBName, opts: opts}, nil
}

// regexpEscape escapes regex metacharacters in an identifier for IncludeTableRegex.
func regexpEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`.\+*?()|[]{}^$`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// LoadPosition resumes from a saved "file:pos" binlog position.
func (s *mysqlBinlogSource) LoadPosition(pos string) error {
	if pos == "" {
		return nil
	}
	i := strings.LastIndex(pos, ":")
	if i < 0 {
		return fmt.Errorf("connector: bad binlog position %q (want file:pos)", pos)
	}
	off, err := strconv.ParseUint(pos[i+1:], 10, 32)
	if err != nil {
		return fmt.Errorf("connector: bad binlog offset in %q: %w", pos, err)
	}
	s.startPos = mysql.Position{Name: pos[:i], Pos: uint32(off)}
	return nil
}

func (s *mysqlBinlogSource) Close() error {
	if s.canal != nil {
		s.canal.Close()
	}
	return nil
}

// Changes starts binlog streaming and emits ChangeEvents until ctx is done.
func (s *mysqlBinlogSource) Changes(ctx context.Context, fn func(ChangeEvent) error) error {
	c, err := canal.NewCanal(s.cfg)
	if err != nil {
		return fmt.Errorf("connector: new canal: %w", err)
	}
	s.canal = c

	start := s.startPos
	if start.Name == "" {
		start, err = c.GetMasterPos()
		if err != nil {
			c.Close()
			return fmt.Errorf("connector: master pos: %w", err)
		}
	}
	h := &binlogHandler{fn: fn, keyCols: s.opts.Tables, curFile: start.Name}
	c.SetEventHandler(h)

	// Stop streaming when ctx is cancelled.
	go func() {
		<-ctx.Done()
		c.Close()
	}()

	err = c.RunFrom(start)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if h.err != nil {
		return h.err
	}
	return err
}

// binlogHandler translates canal row events into ChangeEvents.
type binlogHandler struct {
	canal.DummyEventHandler
	fn      func(ChangeEvent) error
	keyCols map[string][]string
	mu      sync.Mutex
	curFile string
	err     error
}

func (h *binlogHandler) String() string { return "connector-binlog" }

func (h *binlogHandler) OnRotate(_ *replication.EventHeader, r *replication.RotateEvent) error {
	h.mu.Lock()
	h.curFile = string(r.NextLogName)
	h.mu.Unlock()
	return nil
}

func (h *binlogHandler) OnRow(e *canal.RowsEvent) error {
	h.mu.Lock()
	pos := h.curFile + ":" + strconv.FormatUint(uint64(e.Header.LogPos), 10)
	h.mu.Unlock()
	switch e.Action {
	case canal.InsertAction:
		for _, row := range e.Rows {
			if err := h.emit(e.Table, row, OpInsert, pos); err != nil {
				h.err = err
				return err
			}
		}
	case canal.DeleteAction:
		for _, row := range e.Rows {
			if err := h.emit(e.Table, row, OpDelete, pos); err != nil {
				h.err = err
				return err
			}
		}
	case canal.UpdateAction:
		for i := 0; i+1 < len(e.Rows); i += 2 {
			if err := h.emit(e.Table, e.Rows[i+1], OpUpdate, pos); err != nil { // new image
				h.err = err
				return err
			}
		}
	}
	return nil
}

func (h *binlogHandler) emit(table *schema.Table, row []any, op ChangeOp, pos string) error {
	values := map[string]string{}
	nulls := map[string]bool{}
	for j, col := range table.Columns {
		if j >= len(row) {
			break
		}
		if row[j] == nil {
			nulls[col.Name] = true
			continue
		}
		values[col.Name] = cellToString(row[j])
	}
	key := map[string]string{}
	if pk := h.keyCols[table.Name]; len(pk) > 0 {
		for _, name := range pk {
			if v, ok := values[name]; ok {
				key[name] = v
			}
		}
	} else {
		for _, idx := range table.PKColumns {
			if idx < len(table.Columns) {
				name := table.Columns[idx].Name
				if v, ok := values[name]; ok {
					key[name] = v
				}
			}
		}
	}
	return h.fn(ChangeEvent{
		Op:       op,
		Table:    table.Name,
		Key:      key,
		Row:      importflow.Record{Table: table.Name, Values: values, Nulls: nulls},
		Position: pos,
	})
}

// cellToString renders a binlog cell value as a string.
func cellToString(v any) string {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}
```

NOTE on `cellToString`: `valueToString` already exists in `source_postgres.go` with `[]byte`/`string`/`time.Time`/default cases. To avoid a duplicate-symbol collision, **do NOT redefine `valueToString`** — instead either (a) reuse `valueToString` here and delete this local `cellToString`, or (b) keep `cellToString` with a DIFFERENT name. Prefer (a): replace `cellToString(row[j])` with `valueToString(row[j])` and remove the local `cellToString` func. Confirm it compiles (one definition only).

- [ ] **Step 4: `go mod tidy`** (promotes go-mysql to direct). `go build ./pkg/connector`.
- [ ] **Step 5: Run** `go test ./pkg/connector -run TestMySQLBinlogSource -v` (RequiresDSN passes; Connects skips without DSN).

LIVE smoke (Docker available): a default `mysql:8` already has `log_bin=ON`, `binlog_format=ROW`, `binlog_row_image=FULL`.
```bash
docker rm -f cx_my_binlog >/dev/null 2>&1
docker run --rm -d --name cx_my_binlog -e MYSQL_ROOT_PASSWORD=p -e MYSQL_DATABASE=test -p 33370:3306 mysql:8 >/dev/null
for i in $(seq 1 40); do docker exec cx_my_binlog mysqladmin ping -uroot -pp >/dev/null 2>&1 && break; sleep 1; done
docker exec cx_my_binlog mysql -uroot -pp test -e "CREATE TABLE cdc_binlog_users(id int primary key, name varchar(64), phone varchar(32));"
CONNECTOR_MYSQL_BINLOG_DSN='root:p@tcp(localhost:33370)/test' go test ./pkg/connector -run TestMySQLBinlogSourceConnects -v
docker rm -f cx_my_binlog >/dev/null 2>&1
```
Report the result.

- [ ] **Step 6: Commit** — `git add pkg/connector/cdc_mysql.go pkg/connector/cdc_mysql_test.go go.mod go.sum && git commit -m "feat(connector): MySQL binlog change source (ROW, Route B)"` (no co-author line; stage only those 4 files, not the untracked PNGs).

---

### Task 2: Live e2e — MySQL binlog through the Watcher (insert/update/delete)

**Files:**
- Test: `pkg/connector/cdc_mysql_e2e_test.go` (live; skips without DSN)

- [ ] **Step 1: Write the test** — streaming source in a goroutine with a cancelable context; seed a table, start the watcher, perform insert→update→delete, poll the knowledge DB, assert no raw PII + delete removes the chunk. Reuses `mustExec` (from `cdc_e2e_test.go`) and `waitFor` (from `cdc_pg_e2e_test.go`).

```go
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

	time.Sleep(1 * time.Second) // let binlog streaming start from current pos
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
```

- [ ] **Step 2: Run live (Docker):**
```bash
docker rm -f cx_my_binlog_e2e >/dev/null 2>&1
docker run --rm -d --name cx_my_binlog_e2e -e MYSQL_ROOT_PASSWORD=p -e MYSQL_DATABASE=test -p 33371:3306 mysql:8 >/dev/null
for i in $(seq 1 40); do docker exec cx_my_binlog_e2e mysqladmin ping -uroot -pp >/dev/null 2>&1 && break; sleep 1; done
sleep 3
CONNECTOR_MYSQL_BINLOG_DSN='root:p@tcp(localhost:33371)/test' go test ./pkg/connector -run TestMySQLBinlogCDCEndToEnd -v -timeout 120s
docker rm -f cx_my_binlog_e2e >/dev/null 2>&1
```
The test MUST pass: insert appears masked (no raw phone), update propagates (Renamed), delete removes the chunk. Debug if needed (timing: binlog streaming needs a moment to start; the test sleeps 1s before the first insert and uses 10s waits). Report the REAL outcome. Do not weaken the no-PII / delete assertions.

- [ ] **Step 3: Commit** — `git add pkg/connector/cdc_mysql_e2e_test.go && git commit -m "test(connector): live MySQL binlog CDC e2e (insert/update/delete, no PII)"` (no co-author line).

---

### Task 3: Docs

**Files:**
- Modify: `README.md`, `README_CN.md`, `SKILL.md`

- [ ] **Step 1:** Update the "Near-real-time sync (CDC)" subsection: Route B-MySQL (binlog) is now available via `NewMySQLBinlogSource(dsn, MySQLBinlogOptions{ServerID, Tables})` — captures hard deletes, streams continuously, resumes by binlog position. Prereqs: `binlog_format=ROW`, `binlog_row_image=FULL`, a `REPLICATION SLAVE,CLIENT` user, a unique ServerID. **CDC is now complete for both Postgres (logical replication) and MySQL (binlog)** — drop the "MySQL planned" note. Mirror across all three docs; README_CN in Chinese. Add a short Go snippet (README + SKILL):

```go
src, _ := connector.NewMySQLBinlogSource(dsn, connector.MySQLBinlogOptions{
    ServerID: 1101, Tables: map[string][]string{"orders": {"id"}},
})
w, _ := connector.NewWatcher(db, src, connector.WatcherOptions{
    SourceKey: "orders-binlog", Desensitizer: d, Checkpoint: cp,
    Mapping: importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
        "orders": {RAG: &importflow.RAGPlan{ContentTmpl: "{name}", IDColumn: "id"}},
    }},
})
go w.Run(ctx) // streams insert/update/delete; resumes by binlog position
```

- [ ] **Step 2: Commit** — `git commit -am "docs(connector): document MySQL binlog CDC (Route B-MySQL); CDC complete"` (no co-author line).

---

### Task 4: Full verification + Docker run of all flows

- [ ] **Step 1:** `go build ./...` → OK
- [ ] **Step 2:** `go test ./pkg/connector -race` → pass (live CDC tests skip without DSN)
- [ ] **Step 3:** `go test ./... -race` → no regressions
- [ ] **Step 4:** examples compile
- [ ] **Step 5:** Live Docker matrix — run the MySQL binlog e2e (Task 2) plus the existing PG logical e2e and polling e2e against throwaway containers; confirm all green. Tear down containers.
- [ ] **Step 6:** Commit any tidy-ups; ensure `go.mod`/`go.sum` include go-mysql.

---

## Self-review notes

- **Spec coverage (Route B-MySQL):** binlog ROW source decoding I/U/D (T1), position-based resume via `Checkpoint.Position` (reuses Phase 2 wiring), hard-delete propagation through the shared Watcher delete path (T2 asserts the chunk is gone), privacy preserved (T2 asserts no raw PII). With this, the CDC spec's Route A + Route B (PG + MySQL) are all implemented.
- **Symbol collision:** `cellToString` vs the existing `valueToString` — the plan instructs to reuse `valueToString` (one definition). Verify at build.
- **At-least-once + idempotent:** position saved per applied event; replay on restart re-upserts/re-deletes safely.
- **Delete keys:** ROW binlog with `binlog_row_image=FULL` (MySQL 8 default) includes the full row in the DELETE image, so PK columns are present; `emit` reads the key from the configured `Tables` PK or `Table.PKColumns`.
- **Verify-at-impl:** confirm `RotateEvent.NextLogName` is `[]byte` (string() it); confirm `canal.DummyEventHandler` embedding satisfies the full `EventHandler` interface so only `OnRow`/`OnRotate`/`String` need overriding; confirm `RunFrom` blocks and `Close` unblocks it with a non-fatal error (the test treats ctx-cancel as clean).
