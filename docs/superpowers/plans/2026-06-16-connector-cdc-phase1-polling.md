# Connector CDC — Phase 1 (Polling) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add near-real-time sync to `pkg/connector`: a `Watcher` that consumes row-level `ChangeEvent`s from a `ChangeSource`, desensitizes them through the existing signed-`MaskingPlan` gate, and applies idempotent upserts / hard deletes into CortexDB's RAG + knowledge graph — with a resumable checkpoint. Phase 1 ships the shared model + apply loop + checkpoint store + the **polling** change source (DB-agnostic).

**Architecture:** A unified `ChangeEvent`/`ChangeSource` model. The `Watcher` apply loop reuses `connector.Desensitized` + `importflow.New(db).Run` for insert/update (idempotent because `SaveKnowledge` takes an explicit `KnowledgeID` and `UpsertKnowledgeGraph` is idempotent on deterministic IRIs), and `db.DeleteKnowledge` + `db.DeleteKnowledgeGraph` for deletes. `PollingChangeSource` queries `WHERE <cursor> > <watermark>` per table on an interval; it emits insert/update events only (it cannot see hard deletes). Checkpoints persist in a `connector_sync_state` table in the knowledge DB.

**Tech Stack:** Go 1.25, existing `pkg/connector` (desensitizer, sqlSource), `pkg/importflow`, `pkg/cortexdb` facade (`SaveKnowledge`/`DeleteKnowledge`/`UpsertKnowledgeGraph`/`DeleteKnowledgeGraph`), `pkg/graph` (`RDFTerm`, `TriplePattern`, `NewIRI`), `modernc.org/sqlite`. No new external dependencies in Phase 1.

**Spec:** `docs/superpowers/specs/2026-06-16-connector-cdc-design.md`

**Reused existing types/APIs** (verified; do not redefine):
- `importflow.Record{Table string; Values map[string]string; Nulls map[string]bool; Row int}`, `importflow.Source` (`Schemas(ctx)([]Schema,error)`, `Records(ctx, func(Record)error)error`, `Close()error`), `importflow.Schema{Table string; Columns []importflow.Column; Sample []importflow.Record}`, `importflow.Column{Name,Type string}`.
- `importflow.New(db) *importflow.Importer`; `(*Importer).Run(ctx, src, importflow.MappingPlan)(*importflow.Report,error)`; `importflow.MappingPlan{Tables map[string]importflow.TablePlan}`; `importflow.TablePlan{Skip bool; RAG *RAGPlan; KG *KGPlan}`; `importflow.RAGPlan{Namespace,ContentTmpl,IDColumn string; Metadata []string; Refine bool}`; `importflow.KGPlan{Entities []EntityMap; Relations []RelationMap; TextExtract []TextExtract}`; `importflow.EntityMap{Ref,Type,IDTmpl,LabelTmpl string; Props []string}`.
- entity IRI minting: `urn:cortexdb:<Type>:<rendered IDTmpl>` (from `pkg/importflow/mapper.go`).
- `db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID,...})`; `db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID string})(*KnowledgeDeleteResponse,error)`.
- `db.DeleteKnowledgeGraph(ctx, cortexdb.KnowledgeGraphDeleteRequest{Pattern *cortexdb.KnowledgeGraphTriplePattern})(*KnowledgeGraphDeleteResponse{Deleted int},error)`. `cortexdb.KnowledgeGraphTriplePattern = graph.TriplePattern{Subject,Predicate,Object,Graph *graph.RDFTerm; ...}`.
- `graph.NewIRI(string) graph.RDFTerm`.
- connector (this package): `Desensitizer`, `(*Desensitizer).Apply(ctx, importflow.Record)(importflow.Record,error)`, `Desensitized(importflow.Source,*Desensitizer) importflow.Source`, `MaskingPlan`, `ColumnRule`, `MaskAction`, `OpenSQLiteVault`, `StaticKeyProvider`, the `sqlSource` struct + `readRows` in `source_postgres.go`.

---

### Task 1: Change model — ChangeOp, ChangeEvent, ChangeSource

**Files:**
- Create: `pkg/connector/cdc.go`
- Test: `pkg/connector/cdc_test.go`

- [ ] **Step 1: Write the failing test** in `pkg/connector/cdc_test.go`

```go
package connector

import (
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestChangeOpString(t *testing.T) {
	if OpInsert.String() != "insert" || OpUpdate.String() != "update" || OpDelete.String() != "delete" {
		t.Fatalf("op strings: %q %q %q", OpInsert, OpUpdate, OpDelete)
	}
}

func TestChangeEventConstruction(t *testing.T) {
	e := ChangeEvent{
		Op:    OpInsert,
		Table: "users",
		Key:   map[string]string{"id": "1"},
		Row:   importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Bob"}},
	}
	if e.Op != OpInsert || e.Table != "users" || e.Key["id"] != "1" || e.Row.Values["name"] != "Bob" {
		t.Fatalf("bad event: %+v", e)
	}
}
```

- [ ] **Step 2: Run, verify fail** — `go test ./pkg/connector -run 'TestChangeOp|TestChangeEvent'` → FAIL (undefined).

- [ ] **Step 3: Implement `pkg/connector/cdc.go`**

```go
package connector

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// ChangeOp is the kind of row-level change captured from a source.
type ChangeOp int

const (
	OpInsert ChangeOp = iota
	OpUpdate
	OpDelete
)

// String returns the lowercase op name.
func (o ChangeOp) String() string {
	switch o {
	case OpInsert:
		return "insert"
	case OpUpdate:
		return "update"
	case OpDelete:
		return "delete"
	default:
		return "unknown"
	}
}

// ChangeEvent is one row-level change from a source. Key holds the primary-key
// column(s) -> raw value(s) and is present for every op (a delete carries only
// the key). Row holds the full row for Insert/Update and is empty for Delete.
type ChangeEvent struct {
	Op    ChangeOp
	Table string
	Key   map[string]string
	Row   importflow.Record
}

// ChangeSource streams row changes. For polling (Route A) Changes returns after
// one drained batch; for log-based sources (Route B) it blocks and streams until
// ctx is cancelled. fn returning an error aborts iteration.
type ChangeSource interface {
	Changes(ctx context.Context, fn func(ChangeEvent) error) error
	Close() error
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run 'TestChangeOp|TestChangeEvent' -v` → PASS.
- [ ] **Step 5: Commit** — `git add pkg/connector/cdc.go pkg/connector/cdc_test.go && git commit -m "feat(connector): CDC change model (ChangeOp/ChangeEvent/ChangeSource)"`

(No co-author trailer. Message exactly as written.)

---

### Task 2: Checkpoint store (knowledge-DB-backed)

**Files:**
- Create: `pkg/connector/checkpoint.go`
- Test: `pkg/connector/checkpoint_test.go`

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestSQLiteCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	cp, err := NewSQLiteCheckpointStore(db)
	if err != nil {
		t.Fatal(err)
	}

	// missing -> zero Checkpoint, ok=false
	got, ok, err := cp.Load(ctx, "src1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("expected no checkpoint yet, got %+v", got)
	}

	if err := cp.Save(ctx, "src1", Checkpoint{Cursor: `{"users":"100"}`, Position: ""}); err != nil {
		t.Fatal(err)
	}
	got, ok, err = cp.Load(ctx, "src1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.Cursor != `{"users":"100"}` {
		t.Fatalf("load mismatch: %+v ok=%v", got, ok)
	}

	// overwrite
	if err := cp.Save(ctx, "src1", Checkpoint{Cursor: `{"users":"200"}`}); err != nil {
		t.Fatal(err)
	}
	got, _, _ = cp.Load(ctx, "src1")
	if got.Cursor != `{"users":"200"}` {
		t.Fatalf("overwrite failed: %+v", got)
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `pkg/connector/checkpoint.go`**

Note: `cortexdb.DB` exposes its underlying `*sql.DB` for low-level needs. Verify the accessor name during implementation — search `pkg/cortexdb` for a method returning `*sql.DB` (e.g. `DB()`/`SQLDB()`/`Conn()`). If none is exported, open a second `sql.Open("sqlite", <same path>)` handle is NOT acceptable (two writers); instead add a minimal exported accessor `func (db *DB) SQLDB() *sql.DB` in `pkg/cortexdb` returning the shared handle, and use it here. Pick whichever already exists; only add the accessor if none does.

```go
package connector

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Checkpoint is a source's resumable position. Cursor carries Route A's per-table
// watermark (JSON); Position carries Route B's LSN / binlog position. Only one is
// used per source kind.
type Checkpoint struct {
	Cursor   string
	Position string
}

// CheckpointStore persists and loads a source's Checkpoint by a stable key.
type CheckpointStore interface {
	Load(ctx context.Context, sourceKey string) (Checkpoint, bool, error)
	Save(ctx context.Context, sourceKey string, cp Checkpoint) error
}

// SQLiteCheckpointStore stores checkpoints in the knowledge DB, co-located with
// the data they track.
type SQLiteCheckpointStore struct{ db *sql.DB }

// NewSQLiteCheckpointStore creates the state table on the knowledge DB handle.
func NewSQLiteCheckpointStore(cdb *cortexdb.DB) (*SQLiteCheckpointStore, error) {
	sdb := cdb.SQLDB() // accessor verified/added in Step 3 note
	if _, err := sdb.Exec(`CREATE TABLE IF NOT EXISTS connector_sync_state (
		source_key TEXT PRIMARY KEY,
		cursor     TEXT,
		position   TEXT,
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return nil, fmt.Errorf("connector: create sync state table: %w", err)
	}
	return &SQLiteCheckpointStore{db: sdb}, nil
}

func (s *SQLiteCheckpointStore) Load(ctx context.Context, sourceKey string) (Checkpoint, bool, error) {
	var cp Checkpoint
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor, position FROM connector_sync_state WHERE source_key=?`, sourceKey).
		Scan(&cp.Cursor, &cp.Position)
	if err == sql.ErrNoRows {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, err
	}
	return cp, true, nil
}

func (s *SQLiteCheckpointStore) Save(ctx context.Context, sourceKey string, cp Checkpoint) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO connector_sync_state (source_key, cursor, position, updated_at)
		 VALUES (?,?,?, datetime('now'))
		 ON CONFLICT(source_key) DO UPDATE SET cursor=excluded.cursor, position=excluded.position, updated_at=datetime('now')`,
		sourceKey, cp.Cursor, cp.Position)
	return err
}
```

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run TestSQLiteCheckpoint -v`.
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): knowledge-DB-backed CDC checkpoint store"`

---

### Task 3: One-row Source + delete-key resolver (apply helpers)

**Files:**
- Create: `pkg/connector/apply.go`
- Test: `pkg/connector/apply_test.go`

These are the pure, DB-light helpers the Watcher uses: wrap one record as an `importflow.Source`, and compute the stable RAG chunk id / entity IRIs for a key so deletes hit what inserts wrote.

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestSingleRowSource(t *testing.T) {
	rec := importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Bob"}}
	src := singleRowSource("users", []importflow.Column{{Name: "id"}, {Name: "name"}}, rec)
	schemas, err := src.Schemas(context.Background())
	if err != nil || len(schemas) != 1 || schemas[0].Table != "users" {
		t.Fatalf("schemas: %+v %v", schemas, err)
	}
	var got []importflow.Record
	_ = src.Records(context.Background(), func(r importflow.Record) error { got = append(got, r); return nil })
	if len(got) != 1 || got[0].Values["name"] != "Bob" {
		t.Fatalf("records: %+v", got)
	}
}

func TestRAGChunkID(t *testing.T) {
	rag := &importflow.RAGPlan{IDColumn: "id"}
	if id := ragChunkID("users", rag, map[string]string{"id": "42"}); id != "42" {
		t.Fatalf("rag id = %q want 42", id)
	}
}

func TestEntityIRIsForKey(t *testing.T) {
	kg := &importflow.KGPlan{Entities: []importflow.EntityMap{
		{Ref: "customer", Type: "Customer", IDTmpl: "{id}"},
		{Ref: "product", Type: "Product", IDTmpl: "{product_id}"}, // not built from key -> skipped
	}}
	iris := entityIRIsForKey(kg, map[string]string{"id": "42"})
	if len(iris) != 1 || iris[0] != "urn:cortexdb:Customer:42" {
		t.Fatalf("iris = %+v", iris)
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `pkg/connector/apply.go`**

```go
package connector

import (
	"context"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// singleRowSource wraps one record as an importflow.Source so the Watcher can run
// a single insert/update through the normal importflow pipeline (with the
// desensitizer decorator on top). Schemas reports the row's table + columns; the
// row is also offered as the lone sample.
func singleRowSource(table string, cols []importflow.Column, rec importflow.Record) importflow.Source {
	return &oneRow{table: table, cols: cols, rec: rec}
}

type oneRow struct {
	table string
	cols  []importflow.Column
	rec   importflow.Record
}

func (o *oneRow) Schemas(context.Context) ([]importflow.Schema, error) {
	return []importflow.Schema{{Table: o.table, Columns: o.cols, Sample: []importflow.Record{o.rec}}}, nil
}
func (o *oneRow) Records(_ context.Context, fn func(importflow.Record) error) error {
	return fn(o.rec)
}
func (o *oneRow) Close() error { return nil }

// ragChunkID reproduces the KnowledgeID importflow mints for a RAG chunk: the
// value of RAGPlan.IDColumn. (Phase 1 requires IDColumn to be set — see the
// Watcher key precondition — so deletes/updates address the same chunk.)
func ragChunkID(_ string, rag *importflow.RAGPlan, key map[string]string) string {
	if rag == nil || rag.IDColumn == "" {
		return ""
	}
	return key[rag.IDColumn]
}

// entityIRIsForKey reproduces the entity IRIs importflow mints for entities whose
// IDTmpl is built ENTIRELY from key columns (so they are resolvable from a delete
// event that carries only the key). IRI = urn:cortexdb:<Type>:<renderedID>.
func entityIRIsForKey(kg *importflow.KGPlan, key map[string]string) []string {
	if kg == nil {
		return nil
	}
	var out []string
	for _, e := range kg.Entities {
		id, ok := renderFromKey(e.IDTmpl, key)
		if !ok || id == "" {
			continue // IDTmpl references a non-key column; not resolvable from key alone
		}
		out = append(out, "urn:cortexdb:"+e.Type+":"+id)
	}
	return out
}

// renderFromKey substitutes {col} placeholders using only key columns. ok is
// false if any referenced column is absent from key.
func renderFromKey(tmpl string, key map[string]string) (string, bool) {
	var b strings.Builder
	runes := []rune(tmpl)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '{' {
			b.WriteRune(runes[i])
			continue
		}
		j := i + 1
		for j < len(runes) && runes[j] != '}' {
			j++
		}
		if j >= len(runes) {
			b.WriteRune(runes[i])
			continue
		}
		name := string(runes[i+1 : j])
		v, ok := key[name]
		if !ok {
			return "", false
		}
		b.WriteString(v)
		i = j
	}
	return b.String(), true
}
```

Note on pseudonymized keys: when the key column's action is `pseudonymize`/`hash`, the value indexed by importflow is the deterministic token, not the raw value. The Watcher (Task 5) desensitizes the delete event's `Key` BEFORE calling `ragChunkID`/`entityIRIsForKey`, so the same token is produced and the right chunk/IRI is addressed. These helpers operate on the already-desensitized key.

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run 'TestSingleRowSource|TestRAGChunkID|TestEntityIRIs' -v`.
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): CDC apply helpers (one-row source, delete-key resolver)"`

---

### Task 4: Watcher apply loop (insert/update/delete)

**Files:**
- Create: `pkg/connector/watcher.go`
- Test: `pkg/connector/watcher_test.go`

This is the core. The test uses a **fake ChangeSource** + a **real cortexdb DB** (no external DB needed), so it runs in CI and exercises the full apply path including deletes and the privacy gate.

- [ ] **Step 1: Write the failing test**

```go
package connector

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// scriptedSource emits a fixed list of events once, then returns.
type scriptedSource struct{ events []ChangeEvent }

func (s *scriptedSource) Changes(_ context.Context, fn func(ChangeEvent) error) error {
	for _, e := range s.events {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}
func (s *scriptedSource) Close() error { return nil }

func cdcMapping() importflow.MappingPlan {
	return importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"users": {
			RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone}", IDColumn: "id"},
			KG: &importflow.KGPlan{Entities: []importflow.EntityMap{
				{Ref: "u", Type: "User", IDTmpl: "{id}", Props: []string{"name"}},
			}},
		},
	}}
}

func cdcSignedPlan() MaskingPlan {
	p := MaskingPlan{Columns: []ColumnRule{
		{Table: "users", Column: "id", Action: ActionKeep},
		{Table: "users", Column: "name", Action: ActionKeep},
		{Table: "users", Column: "phone", PiiKind: PiiPhone, Action: ActionMask},
	}}
	p.Sign("tester", time.Unix(1, 0))
	return p
}

func newWatcherForTest(t *testing.T, db *cortexdb.DB, src ChangeSource) *Watcher {
	t.Helper()
	d, err := NewDesensitizer(cdcSignedPlan(), DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := NewSQLiteCheckpointStore(db)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(db, src, WatcherOptions{
		SourceKey:    "users-src",
		Desensitizer: d,
		Mapping:      cdcMapping(),
		Checkpoint:   cp,
		Columns:      map[string][]importflow.Column{"users": {{Name: "id"}, {Name: "name"}, {Name: "phone"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWatcherInsertThenDelete(t *testing.T) {
	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	src := &scriptedSource{events: []ChangeEvent{
		{Op: OpInsert, Table: "users", Key: map[string]string{"id": "1"},
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice", "phone": "13812341234"}}},
	}}
	w := newWatcherForTest(t, db, src)
	if err := w.Run(ctx); err != nil {
		t.Fatalf("run insert: %v", err)
	}

	// RAG chunk present at id "1", phone masked, no raw PII.
	got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if err != nil {
		t.Fatalf("get after insert: %v", err)
	}
	if indexOf(got.Knowledge.Content, "13812341234") >= 0 {
		t.Fatalf("raw PII leaked: %q", got.Knowledge.Content)
	}
	if indexOf(got.Knowledge.Content, "Alice") < 0 {
		t.Fatalf("content missing kept field: %q", got.Knowledge.Content)
	}

	// Now delete the row.
	src2 := &scriptedSource{events: []ChangeEvent{
		{Op: OpDelete, Table: "users", Key: map[string]string{"id": "1"}},
	}}
	w2 := newWatcherForTest(t, db, src2)
	if err := w2.Run(ctx); err != nil {
		t.Fatalf("run delete: %v", err)
	}
	if _, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"}); err == nil {
		t.Fatal("expected chunk gone after delete")
	}
}

func TestWatcherUpdateUpserts(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	defer db.Close()
	ctx := context.Background()

	w := newWatcherForTest(t, db, &scriptedSource{events: []ChangeEvent{
		{Op: OpInsert, Table: "users", Key: map[string]string{"id": "1"},
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice", "phone": "13812341234"}}},
		{Op: OpUpdate, Table: "users", Key: map[string]string{"id": "1"},
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice Cooper", "phone": "13812341234"}}},
	}})
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if indexOf(got.Knowledge.Content, "Cooper") < 0 {
		t.Fatalf("update did not upsert content: %q", got.Knowledge.Content)
	}
}

func TestWatcherRejectsMissingIDColumn(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	defer db.Close()
	d, _ := NewDesensitizer(cdcSignedPlan(), DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	cp, _ := NewSQLiteCheckpointStore(db)
	badMapping := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"users": {RAG: &importflow.RAGPlan{ContentTmpl: "{name}"}}, // no IDColumn
	}}
	_, err := NewWatcher(db, &scriptedSource{}, WatcherOptions{
		SourceKey: "k", Desensitizer: d, Mapping: badMapping, Checkpoint: cp,
	})
	if err == nil {
		t.Fatal("expected key-precondition error for RAG without IDColumn")
	}
}
```

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement `pkg/connector/watcher.go`**

```go
package connector

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// WatcherOptions configures a Watcher.
type WatcherOptions struct {
	// SourceKey is the stable checkpoint key for this (source, tableset).
	SourceKey string
	// Desensitizer applies the signed MaskingPlan. Required.
	Desensitizer *Desensitizer
	// Mapping routes rows to RAG/KG. Required; every RAG table MUST set IDColumn
	// and KG delete targets MUST key on the primary key (validated).
	Mapping importflow.MappingPlan
	// Checkpoint persists resume position. Required.
	Checkpoint CheckpointStore
	// Columns gives each table's column list for the one-row import source. If a
	// table is absent, columns are inferred from the event row's value keys.
	Columns map[string][]importflow.Column
}

// Watcher consumes ChangeEvents and keeps RAG + KG in sync with the source.
type Watcher struct {
	db   *cortexdb.DB
	src  ChangeSource
	opts WatcherOptions
	im   *importflow.Importer
}

// NewWatcher validates options (incl. the stable-key precondition) and returns a
// Watcher. It does not start consuming until Run.
func NewWatcher(db *cortexdb.DB, src ChangeSource, opts WatcherOptions) (*Watcher, error) {
	if opts.Desensitizer == nil {
		return nil, fmt.Errorf("connector: watcher requires a Desensitizer")
	}
	if opts.Checkpoint == nil {
		return nil, fmt.Errorf("connector: watcher requires a CheckpointStore")
	}
	if opts.SourceKey == "" {
		return nil, fmt.Errorf("connector: watcher requires a SourceKey")
	}
	// Key precondition: updates/deletes must be addressable. Every RAG table must
	// set IDColumn (not the synthesized table:row id).
	for table, tp := range opts.Mapping.Tables {
		if tp.RAG != nil && tp.RAG.IDColumn == "" {
			return nil, fmt.Errorf("connector: table %q routes to RAG without RAGPlan.IDColumn; CDC needs a stable key", table)
		}
	}
	return &Watcher{db: db, src: src, opts: opts, im: importflow.New(db)}, nil
}

// Run consumes changes until the source's Changes returns (polling: one batch;
// log-based: until ctx is cancelled), applying each event and checkpointing.
func (w *Watcher) Run(ctx context.Context) error {
	return w.src.Changes(ctx, func(e ChangeEvent) error {
		return w.apply(ctx, e)
	})
}

func (w *Watcher) apply(ctx context.Context, e ChangeEvent) error {
	switch e.Op {
	case OpInsert, OpUpdate:
		return w.applyUpsert(ctx, e)
	case OpDelete:
		return w.applyDelete(ctx, e)
	default:
		return fmt.Errorf("connector: unknown change op %d", e.Op)
	}
}

// applyUpsert desensitizes the row and runs it through importflow as a one-row
// source, which upserts by KnowledgeID (RAGPlan.IDColumn) and idempotent IRIs.
func (w *Watcher) applyUpsert(ctx context.Context, e ChangeEvent) error {
	cols := w.opts.Columns[e.Table]
	if cols == nil {
		cols = columnsFromRecord(e.Row)
	}
	src := Desensitized(singleRowSource(e.Table, cols, e.Row), w.opts.Desensitizer)
	_, err := w.im.Run(ctx, src, w.opts.Mapping)
	return err
}

// applyDelete removes the row's RAG chunk and entity triples. The key is
// desensitized first so a pseudonymized key resolves to the same token that was
// indexed.
func (w *Watcher) applyDelete(ctx context.Context, e ChangeEvent) error {
	dkey, err := w.desensitizeKey(ctx, e.Table, e.Key)
	if err != nil {
		return err
	}
	tp := w.opts.Mapping.Tables[e.Table]

	if tp.RAG != nil {
		if id := ragChunkID(e.Table, tp.RAG, dkey); id != "" {
			if _, err := w.db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID: id}); err != nil {
				return err
			}
		}
	}
	if tp.KG != nil {
		for _, iri := range entityIRIsForKey(tp.KG, dkey) {
			if err := w.deleteEntity(ctx, iri); err != nil {
				return err
			}
		}
	}
	return nil
}

// deleteEntity removes all triples where iri is subject (type/label/props/outgoing
// relations) and where it is object (incoming relations), leaving no dangling edge.
func (w *Watcher) deleteEntity(ctx context.Context, iri string) error {
	subj := graph.NewIRI(iri)
	if _, err := w.db.DeleteKnowledgeGraph(ctx, cortexdb.KnowledgeGraphDeleteRequest{
		Pattern: &cortexdb.KnowledgeGraphTriplePattern{Subject: &subj},
	}); err != nil {
		return err
	}
	obj := graph.NewIRI(iri)
	if _, err := w.db.DeleteKnowledgeGraph(ctx, cortexdb.KnowledgeGraphDeleteRequest{
		Pattern: &cortexdb.KnowledgeGraphTriplePattern{Object: &obj},
	}); err != nil {
		return err
	}
	return nil
}

// desensitizeKey runs the key columns through the desensitizer so reversible keys
// become the same token used at index time. Wraps the key as a one-row record.
func (w *Watcher) desensitizeKey(ctx context.Context, table string, key map[string]string) (map[string]string, error) {
	rec := importflow.Record{Table: table, Values: map[string]string{}, Nulls: map[string]bool{}}
	for k, v := range key {
		rec.Values[k] = v
	}
	out, err := w.opts.Desensitizer.Apply(ctx, rec)
	if err != nil {
		return nil, err
	}
	return out.Values, nil
}

func columnsFromRecord(r importflow.Record) []importflow.Column {
	cols := make([]importflow.Column, 0, len(r.Values))
	for name := range r.Values {
		cols = append(cols, importflow.Column{Name: name})
	}
	return cols
}
```

Note: the `Checkpoint` field is accepted now and persisted by the polling source loop in Task 5 (the scripted-source tests don't exercise resume; the live polling test does). Keeping `Run` thin lets Route B reuse the same `apply`.

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run TestWatcher -v`. Then whole-package `go test ./pkg/connector -race`.
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): CDC watcher apply loop (idempotent upsert + hard delete, key precondition)"`

---

### Task 5: Polling change source (Route A)

**Files:**
- Modify: `pkg/connector/source_postgres.go` (add an internal incremental reader on `sqlSource`)
- Create: `pkg/connector/cdc_polling.go`
- Test: `pkg/connector/cdc_polling_test.go`

- [ ] **Step 1: Add the incremental reader to `sqlSource`** in `source_postgres.go`, next to `readRows`:

```go
// readRowsWhere selects rows matching a cursor predicate, ordered by the cursor
// then primary key for stable pagination. It is used by the polling change
// source; the snapshot Records()/Schemas() paths are unchanged.
func (s *sqlSource) readRowsWhere(ctx context.Context, table, cursorCol string, watermark string, keyCols []string, limit int) ([]importflow.Record, error) {
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
	defer rows.Close()
	return scanRows(rows, table)
}
```

This requires two small refactors in the same file:
1. Add a `placeholder func(n int) string` field to `sqlSource` (Postgres uses `$1`, MySQL uses `?`). Set it in `NewPostgresSource` to `func(n int) string { return fmt.Sprintf("$%d", n) }` and in `NewMySQLSource` (`source_mysql.go`) to `func(int) string { return "?" }`.
2. Extract the row-scanning loop currently inside `readRows` into a helper `scanRows(rows *sql.Rows, table string) ([]importflow.Record, error)` and have `readRows` call it, so `readRowsWhere` reuses it. (Pure refactor; existing `readRows` behavior unchanged.)

- [ ] **Step 2: Write the failing test** `pkg/connector/cdc_polling_test.go` (live; skips without DSN)

```go
package connector

import (
	"context"
	"os"
	"testing"
)

func TestPollingChangeSourcePostgres(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_PG_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_PG_DSN (table: cdc_users(id int pk, name text, updated_at timestamptz))")
	}
	ctx := context.Background()
	src, err := NewPollingChangeSource("postgres", dsn, PollingOptions{
		Tables:   []TableCursor{{Table: "cdc_users", CursorColumn: "updated_at", KeyColumns: []string{"id"}}},
		BatchSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	var events []ChangeEvent
	if err := src.Changes(ctx, func(e ChangeEvent) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	// At least the seeded rows come back as insert/update events, each with a key.
	for _, e := range events {
		if e.Op != OpInsert && e.Op != OpUpdate {
			t.Fatalf("polling must emit insert/update only, got %v", e.Op)
		}
		if e.Key["id"] == "" {
			t.Fatalf("event missing key: %+v", e)
		}
	}
}
```

- [ ] **Step 3: Implement `pkg/connector/cdc_polling.go`**

```go
package connector

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// TableCursor declares how to poll one table for changes.
type TableCursor struct {
	Table        string
	CursorColumn string   // monotonic column, e.g. updated_at or a sequence
	KeyColumns   []string // primary key column(s)
}

// PollingOptions configures a polling change source.
type PollingOptions struct {
	Tables    []TableCursor
	BatchSize int // rows per table per drain; default 500
}

// pollingChangeSource emits insert/update events for rows whose cursor advanced.
// It CANNOT see hard deletes. One Changes() call drains current changes once.
type pollingChangeSource struct {
	inner      *sqlSource
	opts       PollingOptions
	watermarks map[string]string // table -> last seen cursor value
}

// NewPollingChangeSource opens a live SQL source for polling. driver is
// "postgres"/"pgx" or "mysql".
func NewPollingChangeSource(driver, dsn string, opts PollingOptions) (ChangeSource, error) {
	if opts.BatchSize == 0 {
		opts.BatchSize = 500
	}
	src, err := openSource(driver, dsn, SourceOptions{})
	if err != nil {
		return nil, err
	}
	ss, ok := src.(*sqlSource)
	if !ok {
		_ = src.Close()
		return nil, fmt.Errorf("connector: polling requires a live SQL source")
	}
	return &pollingChangeSource{inner: ss, opts: opts, watermarks: map[string]string{}}, nil
}

func (p *pollingChangeSource) Changes(ctx context.Context, fn func(ChangeEvent) error) error {
	for _, tc := range p.opts.Tables {
		recs, err := p.inner.readRowsWhere(ctx, tc.Table, tc.CursorColumn, p.watermarks[tc.Table], tc.KeyColumns, p.opts.BatchSize)
		if err != nil {
			return err
		}
		for _, r := range recs {
			key := map[string]string{}
			for _, k := range tc.KeyColumns {
				key[k] = r.Values[k]
			}
			if err := fn(ChangeEvent{Op: OpUpdate, Table: tc.Table, Key: key, Row: r}); err != nil {
				return err
			}
			if cv, ok := r.Values[tc.CursorColumn]; ok {
				p.watermarks[tc.Table] = cv
			}
		}
	}
	return nil
}

func (p *pollingChangeSource) Close() error { return p.inner.Close() }

// WatermarkJSON serializes the current per-table watermarks for the checkpoint.
func (p *pollingChangeSource) WatermarkJSON() (string, error) {
	b, err := json.Marshal(p.watermarks)
	return string(b), err
}

// LoadWatermarks restores per-table watermarks from a checkpoint cursor JSON.
func (p *pollingChangeSource) LoadWatermarks(cursor string) error {
	if cursor == "" {
		return nil
	}
	return json.Unmarshal([]byte(cursor), &p.watermarks)
}

var _ importflow.Source = (*sqlSource)(nil) // sqlSource still satisfies Source
```

- [ ] **Step 4: Run** — `go build ./pkg/connector && go test ./pkg/connector -run 'TestPollingChangeSource' -v` (skips without DSN). If Docker is available, verify live: start `postgres:16` on a non-standard port, create `cdc_users(id int primary key, name text, updated_at timestamptz default now())`, insert rows, run with `CONNECTOR_PG_DSN` set; report the result.
- [ ] **Step 5: Commit** — `git add -A pkg/connector && git commit -m "feat(connector): polling change source (Route A) + incremental sqlSource reader"`

---

### Task 6: Watcher↔polling integration + resume (checkpoint wiring)

**Files:**
- Modify: `pkg/connector/watcher.go` (drain-and-checkpoint when the source is a polling source)
- Test: `pkg/connector/watcher_resume_test.go`

The Watcher must (a) load the checkpoint and seed the polling source's watermarks before running, and (b) save the watermark after a successful drain. Log-based sources (Route B) checkpoint differently (Position), so gate this on a capability interface rather than a concrete type.

- [ ] **Step 1: Define the capability + write the failing test**

```go
package connector

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// fakeCursorSource is a ChangeSource that also exposes watermark load/save, like
// the polling source, but emits scripted events and records the cursor it was
// seeded with.
type fakeCursorSource struct {
	events    []ChangeEvent
	loaded    string
	saveValue string
}

func (f *fakeCursorSource) Changes(_ context.Context, fn func(ChangeEvent) error) error {
	for _, e := range f.events {
		if err := fn(e); err != nil {
			return err
		}
	}
	f.saveValue = `{"users":"200"}`
	return nil
}
func (f *fakeCursorSource) Close() error                      { return nil }
func (f *fakeCursorSource) LoadWatermarks(cursor string) error { f.loaded = cursor; return nil }
func (f *fakeCursorSource) WatermarkJSON() (string, error)     { return f.saveValue, nil }

func TestWatcherSeedsAndSavesCheckpoint(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdbOpen(t, filepath.Join(dir, "kb.db"))
	defer db.Close()
	ctx := context.Background()
	cp, _ := NewSQLiteCheckpointStore(db)
	// pre-seed a checkpoint
	if err := cp.Save(ctx, "users-src", Checkpoint{Cursor: `{"users":"100"}`}); err != nil {
		t.Fatal(err)
	}
	src := &fakeCursorSource{events: []ChangeEvent{
		{Op: OpInsert, Table: "users", Key: map[string]string{"id": "1"},
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice", "phone": "13812341234"}}},
	}}
	w := newWatcherForTestWithSource(t, db, src, cp)
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if src.loaded != `{"users":"100"}` {
		t.Fatalf("watermark not seeded from checkpoint: %q", src.loaded)
	}
	got, _, _ := cp.Load(ctx, "users-src")
	if got.Cursor != `{"users":"200"}` {
		t.Fatalf("watermark not saved after drain: %q", got.Cursor)
	}
}
```

Add the two small test helpers `cortexdbOpen(t, path)` (thin wrapper over `cortexdb.Open(cortexdb.DefaultConfig(path))` that fails the test on error) and `newWatcherForTestWithSource(t, db, src, cp)` (like `newWatcherForTest` but takes the source and checkpoint store) at the bottom of `watcher_resume_test.go`.

- [ ] **Step 2: Run, verify fail.**

- [ ] **Step 3: Implement** — add to `watcher.go`:

```go
// cursorCheckpointer is implemented by polling-style sources that track a
// per-table watermark cursor (Route A). Log-based sources (Route B) implement a
// positionCheckpointer instead (added in their phase).
type cursorCheckpointer interface {
	LoadWatermarks(cursor string) error
	WatermarkJSON() (string, error)
}
```

and update `Run`:

```go
func (w *Watcher) Run(ctx context.Context) error {
	// Seed resume position from the checkpoint, if the source supports cursors.
	if cc, ok := w.src.(cursorCheckpointer); ok {
		cp, found, err := w.opts.Checkpoint.Load(ctx, w.opts.SourceKey)
		if err != nil {
			return err
		}
		if found {
			if err := cc.LoadWatermarks(cp.Cursor); err != nil {
				return err
			}
		}
	}

	if err := w.src.Changes(ctx, func(e ChangeEvent) error {
		return w.apply(ctx, e)
	}); err != nil {
		return err
	}

	// Persist the advanced watermark after a successful drain.
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

(Replace the thin `Run` from Task 4 with this version.)

- [ ] **Step 4: Run, verify pass** — `go test ./pkg/connector -run 'TestWatcher' -v` and whole-package `go test ./pkg/connector -race`.
- [ ] **Step 5: Commit** — `git commit -am "feat(connector): CDC checkpoint seed/save for polling watcher (resume)"`

---

### Task 7: Live polling e2e (Postgres + MySQL)

**Files:**
- Test: `pkg/connector/cdc_e2e_test.go`

- [ ] **Step 1: Write the test** (skips without DSN): seed a table, run the watcher once, assert RAG upsert + no PII; update a row's cursor, run again, assert the content updated and the checkpoint advanced (resume — second run only sees the changed row).

```go
package connector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestCDCPollingEndToEndPostgres(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_PG_DSN")
	if dsn == "" {
		t.Skip("set CONNECTOR_PG_DSN; test seeds its own table cdc_e2e_users")
	}
	ctx := context.Background()
	seedPGTable(t, dsn) // helper: DROP+CREATE cdc_e2e_users(id int pk, name text, phone text, updated_at timestamptz default now()); INSERT one row id=1

	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	d, _ := NewDesensitizer(func() MaskingPlan {
		p := MaskingPlan{Columns: []ColumnRule{
			{Table: "cdc_e2e_users", Column: "id", Action: ActionKeep},
			{Table: "cdc_e2e_users", Column: "name", Action: ActionKeep},
			{Table: "cdc_e2e_users", Column: "phone", PiiKind: PiiPhone, Action: ActionMask},
			{Table: "cdc_e2e_users", Column: "updated_at", Action: ActionKeep},
		}}
		p.Sign("tester", time.Unix(1, 0))
		return p
	}(), DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})

	mapping := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"cdc_e2e_users": {RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone}", IDColumn: "id"}},
	}}
	cp, _ := NewSQLiteCheckpointStore(db)

	run := func() {
		src, err := NewPollingChangeSource("postgres", dsn, PollingOptions{
			Tables: []TableCursor{{Table: "cdc_e2e_users", CursorColumn: "updated_at", KeyColumns: []string{"id"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		w, err := NewWatcher(db, src, WatcherOptions{SourceKey: "e2e", Desensitizer: d, Mapping: mapping, Checkpoint: cp})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}

	run()
	got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if err != nil {
		t.Fatalf("expected chunk after first sync: %v", err)
	}
	if indexOf(got.Knowledge.Content, "13812341234") >= 0 {
		t.Fatalf("raw PII leaked: %q", got.Knowledge.Content)
	}

	updatePGRow(t, dsn) // helper: UPDATE cdc_e2e_users SET name='Renamed', updated_at=now() WHERE id=1
	run()
	got2, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if indexOf(got2.Knowledge.Content, "Renamed") < 0 {
		t.Fatalf("second sync did not pick up update: %q", got2.Knowledge.Content)
	}
}
```

Add the `seedPGTable`/`updatePGRow` helpers in the same test file using `database/sql` + `_ "github.com/jackc/pgx/v5/stdlib"` (already a dependency), executing the SQL described in the comments. A MySQL twin (`TestCDCPollingEndToEndMySQL`, `CONNECTOR_MYSQL_DSN`, backtick DDL, `updated_at` as `DATETIME` updated explicitly) follows the same shape.

- [ ] **Step 2: Run** — `go test ./pkg/connector -run TestCDCPolling -v` (skips without DSN). If Docker is available, run both live against throwaway containers on non-standard ports and report results.
- [ ] **Step 3: Commit** — `git commit -am "test(connector): live polling CDC e2e (upsert, no PII, resume) for Postgres + MySQL"`

---

### Task 8: Docs

**Files:**
- Modify: `README.md`, `README_CN.md`, `SKILL.md`

- [ ] **Step 1:** Add a short "Near-real-time sync (CDC)" subsection under the data-connector material in each file. Cover: `Watcher` + `ChangeSource` model; Route A polling available now (`NewPollingChangeSource`), Routes B (PG logical replication / MySQL binlog) coming in later phases; idempotent upsert + hard delete; the stable-`IDColumn` precondition; checkpoint in the knowledge DB; library-API-only surface (`Watcher.Run(ctx)`); honest limits (polling misses hard deletes; single-node seconds-scale). Keep all three in sync; README_CN in Chinese.
- [ ] **Step 2: Commit** — `git commit -am "docs(connector): near-real-time CDC sync (polling) in README/SKILL"`

---

### Task 9: Full verification

- [ ] **Step 1:** `go build ./...` → OK
- [ ] **Step 2:** `go test ./pkg/connector -race` → all pass (live polling/e2e skip without DSNs)
- [ ] **Step 3:** `go test ./... -race` → no regressions
- [ ] **Step 4:** examples compile: `for d in examples/*/; do (cd "$d" && go build -o /dev/null .) || echo "FAIL $d"; done`
- [ ] **Step 5: Commit** any tidy-ups.

---

## Self-review notes

- **Spec coverage (Phase 1 scope):** unified ChangeEvent/ChangeSource (T1) · checkpoint in knowledge DB (T2) · one-row source + delete-key resolver (T3) · Watcher apply loop with idempotent upsert + hard delete + key precondition (T4) · polling source + incremental sqlSource reader (T5) · checkpoint seed/save + resume (T6) · live e2e PG+MySQL incl. no-PII + resume (T7) · docs (T8) · verification (T9). Routes B-PG and B-MySQL are deliberately deferred to their own plans (`...-phase2-pg-logical.md`, `...-phase3-mysql-binlog.md`) written against the pinned `pglogrepl` / `go-mysql` libraries — they reuse T1–T4/T6's model, Watcher, `apply`, and checkpoint store, adding only a `ChangeSource` impl and a `positionCheckpointer`.
- **Key precondition** (spec) enforced in `NewWatcher` (T4) and depended on by the delete resolver (T3).
- **Privacy invariant** reused verbatim (Desensitizer) and asserted in T4 (no raw PII in content) and T7 (live).
- **Verify-at-impl markers:** (a) confirm the `cortexdb.DB` accessor for `*sql.DB` (T2 Step 3) — add `SQLDB()` only if none exists; (b) confirm `graph.NewIRI` returns a value usable as `&subj` for `TriplePattern.Subject *graph.RDFTerm` (T4); (c) the `readRows` refactor into `scanRows` must keep existing snapshot behavior identical (T5).
