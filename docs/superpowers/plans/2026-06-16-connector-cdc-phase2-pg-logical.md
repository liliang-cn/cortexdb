# Connector CDC — Phase 2 (Postgres Logical Replication) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add a true-CDC `ChangeSource` for Postgres using logical replication (pgoutput), so the existing `Watcher` streams inserts/updates/**deletes** (Phase 1 polling could not see hard deletes) into RAG + knowledge graph, desensitized, with LSN-based resume.

**Architecture:** `PostgresCDCSource` opens a replication connection (`pgconn` with `replication=database`), ensures a pgoutput publication + replication slot, `StartReplication`, and runs the standard receive loop: decode pgoutput `Relation`/`Insert`/`Update`/`Delete` messages into `connector.ChangeEvent`s (caching relations by ID), emit them via the `Changes` callback, and send periodic standby status updates. It implements a new `positionCheckpointer` so the `Watcher` seeds the resume LSN and saves the applied LSN to the knowledge-DB checkpoint (`Checkpoint.Position`). Reuses the Phase 1 `Watcher.apply` (idempotent upsert + hard delete) verbatim.

**Tech Stack:** Go 1.25, `github.com/jackc/pglogrepl` (v0.0.0-20260401131349-e37c41485510, pure Go), `github.com/jackc/pgx/v5/pgconn` + `pgproto3` (already a dependency via pgx), existing `pkg/connector` (Watcher/ChangeEvent/checkpoint/desensitizer).

**Spec:** `docs/superpowers/specs/2026-06-16-connector-cdc-design.md` (Route B-PG).

**Verified pglogrepl API** (do not deviate):
- Connect: `pgconn.Connect(ctx, dsn)` where dsn has `replication=database`; returns `*pgconn.PgConn`.
- `pglogrepl.IdentifySystem(ctx, conn) (IdentifySystemResult{XLogPos LSN, ...}, error)`.
- `pglogrepl.CreateReplicationSlot(ctx, conn, slotName, "pgoutput", CreateReplicationSlotOptions{Temporary bool, Mode: pglogrepl.LogicalReplication}) (CreateReplicationSlotResult, error)` — **errors if slot exists**.
- `pglogrepl.StartReplication(ctx, conn, slotName, startLSN LSN, StartReplicationOptions{Mode: pglogrepl.LogicalReplication, PluginArgs: []string{"proto_version '1'", "publication_names '<pub>'"}}) error`.
- Receive loop: `conn.ReceiveMessage(ctx)` → assert `*pgproto3.CopyData`; `msg.Data[0]` is `pglogrepl.PrimaryKeepaliveMessageByteID` ('k') or `pglogrepl.XLogDataByteID` ('w'). `pglogrepl.ParsePrimaryKeepaliveMessage(msg.Data[1:]) (PrimaryKeepaliveMessage{ServerWALEnd LSN, ReplyRequested bool}, error)`; `pglogrepl.ParseXLogData(msg.Data[1:]) (XLogData{WALStart LSN, ServerWALEnd LSN, WALData []byte}, error)`. `pgconn.Timeout(err)` is true on deadline.
- `pglogrepl.SendStandbyStatusUpdate(ctx, conn, StandbyStatusUpdate{WALWritePosition: lsn})`.
- `pglogrepl.Parse(xld.WALData) (Message, error)` → type switch:
  - `*pglogrepl.RelationMessage{RelationID uint32, Namespace, RelationName string, Columns []*RelationMessageColumn{Flags uint8, Name string, DataType uint32}}` (Flags==1 ⇒ part of replica-identity key).
  - `*pglogrepl.InsertMessage{RelationID uint32, Tuple *TupleData}`.
  - `*pglogrepl.UpdateMessage{RelationID uint32, NewTuple *TupleData, OldTuple *TupleData}`.
  - `*pglogrepl.DeleteMessage{RelationID uint32, OldTuple *TupleData}`.
  - `TupleData{Columns []*TupleDataColumn}`, `TupleDataColumn{DataType uint8, Data []byte}` where DataType `'n'`=null, `'t'`=text (use `string(Data)`), `'u'`=unchanged-toast (skip).
- `pglogrepl.LSN` is `uint64`; `lsn.String()` / `pglogrepl.ParseLSN(s)`; advance `clientXLogPos = xld.WALStart + LSN(len(xld.WALData))`.

**Reused connector APIs:** `ChangeEvent{Op, Table, Key, Row}`, `OpInsert/OpUpdate/OpDelete`, `ChangeSource`, `Checkpoint{Cursor, Position string}`, `Watcher` + `Watcher.Run` (currently handles `cursorCheckpointer`), `importflow.Record`.

---

### Task 1: Add `Position` to ChangeEvent + positionCheckpointer wiring in the Watcher

**Files:**
- Modify: `pkg/connector/cdc.go` (add `Position` field to `ChangeEvent`)
- Modify: `pkg/connector/watcher.go` (add `positionCheckpointer`, save position after apply, seed on start)
- Test: `pkg/connector/watcher_position_test.go`

- [ ] **Step 1: Write `pkg/connector/watcher_position_test.go`** — a fake position source drives insert events carrying an LSN string; assert the watcher seeds the loaded position and saves the last applied position.

```go
package connector

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// fakePositionSource streams scripted events (each carrying a Position) and
// records the position it was seeded with. It is a positionCheckpointer.
type fakePositionSource struct {
	events []ChangeEvent
	loaded string
}

func (f *fakePositionSource) Changes(_ context.Context, fn func(ChangeEvent) error) error {
	for _, e := range f.events {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakePositionSource) Close() error                  { return nil }
func (f *fakePositionSource) LoadPosition(pos string) error { f.loaded = pos; return nil }

func TestWatcherSeedsAndSavesPosition(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdbOpen(t, filepath.Join(dir, "kb.db"))
	defer db.Close()
	ctx := context.Background()
	cp, _ := NewSQLiteCheckpointStore(db)
	if err := cp.Save(ctx, "users-src", Checkpoint{Position: "0/16B3748"}); err != nil {
		t.Fatal(err)
	}
	src := &fakePositionSource{events: []ChangeEvent{
		{Op: OpInsert, Table: "users", Key: map[string]string{"id": "1"}, Position: "0/16B37A0",
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice", "phone": "13812341234"}}},
	}}
	w := newWatcherForTestWithSource(t, db, src, cp)
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if src.loaded != "0/16B3748" {
		t.Fatalf("position not seeded from checkpoint: %q", src.loaded)
	}
	got, _, _ := cp.Load(ctx, "users-src")
	if got.Position != "0/16B37A0" {
		t.Fatalf("applied position not saved: %q", got.Position)
	}
}
```

- [ ] **Step 2: Run** `go test ./pkg/connector -run TestWatcherSeedsAndSavesPosition` → FAIL.

- [ ] **Step 3a: Add `Position` to `ChangeEvent`** in `cdc.go` (after `Row`):

```go
	Row importflow.Record
	// Position is the source's resume token for this event (Route B: the LSN /
	// binlog position string). Empty for polling sources.
	Position string
```

- [ ] **Step 3b: Add `positionCheckpointer` + wire it into `Run`** in `watcher.go`. Add the interface next to `cursorCheckpointer`:

```go
// positionCheckpointer is implemented by log-based sources (Route B: PG logical
// replication, MySQL binlog) that resume from an opaque position token (LSN /
// binlog position) carried on each ChangeEvent.Position.
type positionCheckpointer interface {
	LoadPosition(pos string) error
}
```

Modify `Run` so that, for a `positionCheckpointer` source, it (a) seeds the loaded position before draining and (b) saves `Checkpoint{Position: e.Position}` after each applied event whose `Position` is non-empty. Keep the existing `cursorCheckpointer` branch. The new `Run`:

```go
func (w *Watcher) Run(ctx context.Context) error {
	// Seed resume position from the checkpoint.
	cp, found, err := w.opts.Checkpoint.Load(ctx, w.opts.SourceKey)
	if err != nil {
		return err
	}
	if found {
		if cc, ok := w.src.(cursorCheckpointer); ok {
			if err := cc.LoadWatermarks(cp.Cursor); err != nil {
				return err
			}
		}
		if pc, ok := w.src.(positionCheckpointer); ok {
			if err := pc.LoadPosition(cp.Position); err != nil {
				return err
			}
		}
	}

	_, isPosition := w.src.(positionCheckpointer)
	if err := w.src.Changes(ctx, func(e ChangeEvent) error {
		if err := w.apply(ctx, e); err != nil {
			return err
		}
		// Position-based sources checkpoint after each applied event (at-least-once;
		// apply is idempotent, so a replayed event on restart is safe).
		if isPosition && e.Position != "" {
			return w.opts.Checkpoint.Save(ctx, w.opts.SourceKey, Checkpoint{Position: e.Position})
		}
		return nil
	}); err != nil {
		return err
	}

	// Cursor-based sources checkpoint the advanced watermark after a full drain.
	if cc, ok := w.src.(cursorCheckpointer); ok {
		cursor, err := cc.WatermarkJSON()
		if err != nil {
			return err
		}
		return w.opts.Checkpoint.Save(ctx, w.opts.SourceKey, Checkpoint{Cursor: cursor})
	}
	return nil
}
```

(This replaces the Phase 1 `Run`. The cursor path behaves exactly as before because the polling source is a `cursorCheckpointer`, not a `positionCheckpointer`.)

- [ ] **Step 4: Run** `go test ./pkg/connector -run TestWatcher -race -v` (the new position test AND all Phase 1 watcher/resume tests must pass — confirm the cursor path is unchanged). Then `go test ./pkg/connector -race`.
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): ChangeEvent.Position + position-based checkpoint wiring (Route B)"` (no co-author line).

---

### Task 2: PostgresCDCSource (pgoutput logical replication)

**Files:**
- Create: `pkg/connector/cdc_pg.go`
- Test: `pkg/connector/cdc_pg_test.go` (live; skips without DSN)

- [ ] **Step 1: Write `pkg/connector/cdc_pg_test.go`** — live test, skips without `CONNECTOR_PG_REPL_DSN`. It is exercised end-to-end in Task 3; here just assert the source constructs and the option validation works.

```go
package connector

import (
	"os"
	"testing"
)

func TestPostgresCDCSourceRequiresPublication(t *testing.T) {
	_, err := NewPostgresCDCSource("postgres://x/y", PostgresCDCOptions{})
	if err == nil {
		t.Fatal("expected error when Publication is empty")
	}
}

func TestPostgresCDCSourceConnects(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_PG_REPL_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_PG_REPL_DSN to a wal_level=logical Postgres (publication + slot auto-created)")
	}
	src, err := NewPostgresCDCSource(dsn, PostgresCDCOptions{
		Publication: "cdc_pub", Slot: "cdc_slot", Tables: map[string][]string{"cdc_repl_users": {"id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
```

- [ ] **Step 2: Run** `go test ./pkg/connector -run TestPostgresCDCSource` → FAIL (undefined) then SKIP for the live one.

- [ ] **Step 3: Implement `pkg/connector/cdc_pg.go`**

```go
package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

// PostgresCDCOptions configures the logical-replication change source.
type PostgresCDCOptions struct {
	Publication string              // pgoutput publication name (required)
	Slot        string              // replication slot name (required)
	Tables      map[string][]string // table -> primary-key column names (for ChangeEvent.Key)
	// CreateSlot: create a permanent slot if absent (default true). Set false if
	// the slot is managed externally.
	CreateSlot       bool
	StandbyInterval  time.Duration // standby status cadence; default 10s
}

// postgresCDCSource streams pgoutput logical-replication changes as ChangeEvents.
type postgresCDCSource struct {
	dsn        string
	opts       PostgresCDCOptions
	conn       *pgconn.PgConn
	relations  map[uint32]*pglogrepl.RelationMessage
	keyCols    map[string]map[string]bool // table -> set of PK column names
	startLSN   pglogrepl.LSN
}

// NewPostgresCDCSource opens a replication connection and prepares (but does not
// start) streaming. The DSN must allow replication; "replication=database" is
// appended if absent.
func NewPostgresCDCSource(dsn string, opts PostgresCDCOptions) (ChangeSource, error) {
	if opts.Publication == "" {
		return nil, fmt.Errorf("connector: PostgresCDCOptions.Publication is required")
	}
	if opts.Slot == "" {
		return nil, fmt.Errorf("connector: PostgresCDCOptions.Slot is required")
	}
	if opts.StandbyInterval == 0 {
		opts.StandbyInterval = 10 * time.Second
	}
	replDSN := dsn
	if !strings.Contains(replDSN, "replication=") {
		sep := "?"
		if strings.Contains(replDSN, "?") {
			sep = "&"
		}
		replDSN = replDSN + sep + "replication=database"
	}
	conn, err := pgconn.Connect(context.Background(), replDSN)
	if err != nil {
		return nil, fmt.Errorf("connector: pg replication connect: %w", err)
	}
	keyCols := map[string]map[string]bool{}
	for table, pk := range opts.Tables {
		set := map[string]bool{}
		for _, c := range pk {
			set[c] = true
		}
		keyCols[table] = set
	}
	return &postgresCDCSource{
		dsn: replDSN, opts: opts, conn: conn,
		relations: map[uint32]*pglogrepl.RelationMessage{}, keyCols: keyCols,
	}, nil
}

// LoadPosition resumes from a saved LSN string (positionCheckpointer).
func (s *postgresCDCSource) LoadPosition(pos string) error {
	if pos == "" {
		return nil
	}
	lsn, err := pglogrepl.ParseLSN(pos)
	if err != nil {
		return fmt.Errorf("connector: bad resume LSN %q: %w", pos, err)
	}
	s.startLSN = lsn
	return nil
}

func (s *postgresCDCSource) Close() error { return s.conn.Close(context.Background()) }

// Changes starts logical replication and streams ChangeEvents until ctx is done.
func (s *postgresCDCSource) Changes(ctx context.Context, fn func(ChangeEvent) error) error {
	// Ensure a slot exists (permanent). CreateReplicationSlot errors if it already
	// exists; treat that as success.
	if s.opts.CreateSlot || s.startLSN == 0 {
		_, err := pglogrepl.CreateReplicationSlot(ctx, s.conn, s.opts.Slot, "pgoutput",
			pglogrepl.CreateReplicationSlotOptions{Mode: pglogrepl.LogicalReplication})
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return fmt.Errorf("connector: create slot: %w", err)
		}
	}
	startLSN := s.startLSN
	if startLSN == 0 {
		sys, err := pglogrepl.IdentifySystem(ctx, s.conn)
		if err != nil {
			return fmt.Errorf("connector: identify system: %w", err)
		}
		startLSN = sys.XLogPos
	}
	if err := pglogrepl.StartReplication(ctx, s.conn, s.opts.Slot, startLSN,
		pglogrepl.StartReplicationOptions{
			Mode:       pglogrepl.LogicalReplication,
			PluginArgs: []string{"proto_version '1'", "publication_names '" + s.opts.Publication + "'"},
		}); err != nil {
		return fmt.Errorf("connector: start replication: %w", err)
	}

	clientXLogPos := startLSN
	nextStandby := time.Now().Add(s.opts.StandbyInterval)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(nextStandby) {
			if err := pglogrepl.SendStandbyStatusUpdate(ctx, s.conn,
				pglogrepl.StandbyStatusUpdate{WALWritePosition: clientXLogPos}); err != nil {
				return fmt.Errorf("connector: standby update: %w", err)
			}
			nextStandby = time.Now().Add(s.opts.StandbyInterval)
		}
		rctx, cancel := context.WithDeadline(ctx, nextStandby)
		raw, err := s.conn.ReceiveMessage(rctx)
		cancel()
		if err != nil {
			if pgconn.Timeout(err) {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("connector: receive: %w", err)
		}
		cd, ok := raw.(*pgproto3.CopyData)
		if !ok {
			continue
		}
		switch cd.Data[0] {
		case pglogrepl.PrimaryKeepaliveMessageByteID:
			pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(cd.Data[1:])
			if err != nil {
				return fmt.Errorf("connector: parse keepalive: %w", err)
			}
			if pkm.ServerWALEnd > clientXLogPos {
				clientXLogPos = pkm.ServerWALEnd
			}
			if pkm.ReplyRequested {
				nextStandby = time.Time{}
			}
		case pglogrepl.XLogDataByteID:
			xld, err := pglogrepl.ParseXLogData(cd.Data[1:])
			if err != nil {
				return fmt.Errorf("connector: parse xlogdata: %w", err)
			}
			if err := s.handleWAL(xld, fn); err != nil {
				return err
			}
			clientXLogPos = xld.WALStart + pglogrepl.LSN(len(xld.WALData))
		}
	}
}

// handleWAL decodes one pgoutput message and emits a ChangeEvent for DML.
func (s *postgresCDCSource) handleWAL(xld pglogrepl.XLogData, fn func(ChangeEvent) error) error {
	msg, err := pglogrepl.Parse(xld.WALData)
	if err != nil {
		return fmt.Errorf("connector: parse pgoutput: %w", err)
	}
	pos := (xld.WALStart + pglogrepl.LSN(len(xld.WALData))).String()
	switch m := msg.(type) {
	case *pglogrepl.RelationMessage:
		s.relations[m.RelationID] = m
	case *pglogrepl.InsertMessage:
		return s.emit(m.RelationID, m.Tuple, OpInsert, pos, fn)
	case *pglogrepl.UpdateMessage:
		return s.emit(m.RelationID, m.NewTuple, OpUpdate, pos, fn)
	case *pglogrepl.DeleteMessage:
		return s.emit(m.RelationID, m.OldTuple, OpDelete, pos, fn)
	}
	return nil
}

// emit turns a tuple into a ChangeEvent using the cached relation for column names.
func (s *postgresCDCSource) emit(relID uint32, tuple *pglogrepl.TupleData, op ChangeOp, pos string, fn func(ChangeEvent) error) error {
	rel, ok := s.relations[relID]
	if !ok || tuple == nil {
		return nil // no relation metadata yet, or no tuple (e.g. REPLICA IDENTITY NONE delete)
	}
	values := map[string]string{}
	nulls := map[string]bool{}
	for i, col := range tuple.Columns {
		if i >= len(rel.Columns) {
			break
		}
		name := rel.Columns[i].Name
		switch col.DataType {
		case 'n':
			nulls[name] = true
		case 't':
			values[name] = string(col.Data)
		case 'u':
			// unchanged toast: column value not present in this message; skip
		}
	}
	key := map[string]string{}
	if pkSet := s.keyCols[rel.RelationName]; len(pkSet) > 0 {
		// Explicit PK config: take those columns from the decoded values.
		for name := range pkSet {
			if v, ok := values[name]; ok {
				key[name] = v
			}
		}
	} else {
		// No explicit PK: fall back to replica-identity key columns (Flags==1).
		for i, rc := range rel.Columns {
			if rc.Flags == 1 && i < len(tuple.Columns) && tuple.Columns[i].DataType == 't' {
				key[rc.Name] = string(tuple.Columns[i].Data)
			}
		}
	}
	return fn(ChangeEvent{
		Op:    op,
		Table: rel.RelationName,
		Key:   key,
		Row:   importflow.Record{Table: rel.RelationName, Values: values, Nulls: nulls},
		Position: pos,
	})
}
```

Add the `importflow` import (used in `emit`). Run `goimports`/`go build` to confirm imports.

- [ ] **Step 4: `go mod tidy`** (promotes pglogrepl from indirect to direct). Run `go build ./pkg/connector`.
- [ ] **Step 5: Run** `go test ./pkg/connector -run TestPostgresCDCSource -v` (constructor-required test passes; live connect test skips without DSN).
- [ ] **Step 6: Commit** — `git add pkg/connector/cdc_pg.go pkg/connector/cdc_pg_test.go go.mod go.sum && git commit -m "feat(connector): Postgres logical-replication change source (pgoutput, Route B)"` (no co-author line; stage only these files, not the untracked PNGs).

---

### Task 3: Live e2e — PG logical replication through the Watcher (insert/update/delete)

**Files:**
- Test: `pkg/connector/cdc_pg_e2e_test.go` (live; skips without DSN)

- [ ] **Step 1: Write the test.** It needs a streaming source running in a goroutine (Changes blocks), with a cancelable context. It seeds a table, sets up publication, starts the watcher in a goroutine, performs insert→update→delete, polls the knowledge DB until each change is reflected (or times out), and asserts no raw PII + that the delete removed the chunk.

```go
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

	// Give replication a moment to start, then drive DML.
	time.Sleep(500 * time.Millisecond)
	mustExec(t, admin, `INSERT INTO cdc_repl_users(id,name,phone) VALUES (1,'Alice','13812341234')`)

	// Poll until the chunk appears.
	waitFor(t, 5*time.Second, func() bool {
		got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
		return err == nil && indexOf(got.Knowledge.Content, "Alice") >= 0
	})
	got, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if indexOf(got.Knowledge.Content, "13812341234") >= 0 {
		t.Fatalf("raw PII leaked via CDC: %q", got.Knowledge.Content)
	}

	mustExec(t, admin, `UPDATE cdc_repl_users SET name='Renamed' WHERE id=1`)
	waitFor(t, 5*time.Second, func() bool {
		got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
		return err == nil && indexOf(got.Knowledge.Content, "Renamed") >= 0
	})

	mustExec(t, admin, `DELETE FROM cdc_repl_users WHERE id=1`)
	waitFor(t, 5*time.Second, func() bool {
		_, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
		return err != nil // gone
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
```

(`mustExec` already exists in `cdc_e2e_test.go`; reuse it. `time.Now()` is fine in a normal test — this is not a workflow script.)

- [ ] **Step 2: Run live.** Start a logical-replication Postgres in Docker on a non-standard port and run:

```bash
docker rm -f cx_pg_repl >/dev/null 2>&1
docker run --rm -d --name cx_pg_repl -e POSTGRES_PASSWORD=p -p 55450:5432 postgres:16 -c wal_level=logical >/dev/null
for i in $(seq 1 25); do docker exec cx_pg_repl pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
CONNECTOR_PG_REPL_DSN='postgres://postgres:p@localhost:55450/postgres?sslmode=disable' \
  go test ./pkg/connector -run TestPostgresLogicalCDCEndToEnd -v
docker rm -f cx_pg_repl >/dev/null 2>&1
```

Report the actual result. The insert/update/delete must all reflect in the knowledge DB and the raw phone must never appear.

- [ ] **Step 3: Commit** — `git add pkg/connector/cdc_pg_e2e_test.go && git commit -m "test(connector): live PG logical-replication CDC e2e (insert/update/delete, no PII)"` (no co-author line).

---

### Task 4: Docs

**Files:**
- Modify: `README.md`, `README_CN.md`, `SKILL.md`

- [ ] **Step 1:** Update the "Near-real-time sync (CDC)" subsection: Route B-PG (Postgres logical replication) is now available via `NewPostgresCDCSource(dsn, PostgresCDCOptions{Publication, Slot, Tables})` — it captures hard deletes and streams continuously; prerequisites `wal_level=logical` + a publication. Keep MySQL binlog (Route B-MySQL) listed as the remaining planned phase. Note the streaming `Watcher.Run(ctx)` is long-lived for Route B (cancel ctx to stop) and resumes by LSN. Mirror across all three docs.
- [ ] **Step 2: Commit** — `git commit -am "docs(connector): document Postgres logical-replication CDC (Route B-PG)"` (no co-author line).

---

### Task 5: Full verification

- [ ] **Step 1:** `go build ./...` → OK
- [ ] **Step 2:** `go test ./pkg/connector -race` → pass (live CDC tests skip without DSN)
- [ ] **Step 3:** `go test ./... -race` → no regressions
- [ ] **Step 4:** examples compile
- [ ] **Step 5:** Commit any tidy-ups; ensure `go.mod`/`go.sum` include pglogrepl + pgio.

---

## Self-review notes

- **Spec coverage (Route B-PG):** logical-replication source decoding I/U/D (T2), position-based resume via checkpoint Position (T1), hard-delete propagation reusing the Phase 1 Watcher delete path (T3 asserts the chunk is gone), privacy preserved (T3 asserts no raw PII). MySQL binlog (Route B-MySQL) remains a separate Phase 3 plan.
- **At-least-once + idempotent:** position is saved after each applied event; on restart a replayed event re-upserts (idempotent) or re-deletes (no-op) — safe. Documented.
- **REPLICA IDENTITY:** deletes need the PK in `OldTuple`; default replica identity (PK) suffices and `emit` falls back to `Flags==1` columns when no explicit `Tables` PK is configured. If a table has `REPLICA IDENTITY NOTHING`, deletes carry no key and are skipped (documented limitation).
- **Verify-at-impl:** confirm `pgproto3` import path is `github.com/jackc/pgx/v5/pgproto3` at build; confirm `CreateReplicationSlot`'s "already exists" error text match (adjust the substring if the driver wraps it differently — the test in T3 drops the slot first, so first run creates cleanly).
