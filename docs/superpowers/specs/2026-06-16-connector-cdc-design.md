# Connector CDC / Near-Real-Time Sync — Design

Date: 2026-06-16
Status: draft (pending review)

## Goal

Keep a CortexDB knowledge base (RAG + knowledge graph) **continuously in sync**
with a live external database, through the existing `pkg/connector` privacy gate.
A change in the source (insert / update / delete) flows — desensitized — into
CortexDB with low latency, instead of the current one-shot snapshot import.

```
source change ─▶ ChangeEvent ─▶ desensitize ─▶ apply (upsert | delete) ─▶ RAG + KG
                                                            │
                                                       checkpoint (resume)
```

This extends `pkg/connector` (the privacy gate over importflow). It does **not**
change `pkg/core` / `pkg/cortexdb` / `pkg/importflow` engines.

## Non-goals (v1)

- No standalone daemon binary and no MCP tool. The surface is a **library API**
  (`Watcher.Run(ctx)`); the caller decides how to schedule/host it.
- No schema-change (DDL) replication. Column add/drop on the source is out of
  scope; the signed MaskingPlan defines the columns that matter.
- No multi-table transactional ordering guarantees beyond per-table apply order.
- No backfill orchestration: the initial load is the existing one-shot connector
  import; CDC takes over from a checkpoint after that.

## Core trust invariant (unchanged)

Every record still passes through a **human-signed `MaskingPlan`** before any
value reaches RAG/KG. Signing is one-time (at watcher construction), not per
event. Raw PII never enters RAG/KG; reversible originals live only in the vault.
The CDC path reuses `connector.Desensitized` / `connector.NewDesensitizer`
verbatim, so all connector privacy invariants hold for streamed rows too.

## Architecture: unified ChangeSource + Watcher

A single change model lets one apply-loop serve all three source mechanisms; the
three sources differ only in *how they produce events*.

```go
// ChangeOp is the kind of row change.
type ChangeOp int

const (
    OpInsert ChangeOp = iota
    OpUpdate
    OpDelete
)

// ChangeEvent is one row-level change from a source.
type ChangeEvent struct {
    Op    ChangeOp
    Table string
    // Key is the primary-key column(s) → raw value(s). Present for every op
    // (a delete carries only the key).
    Key map[string]string
    // Row is the full row for Insert/Update; nil/empty for Delete.
    Row importflow.Record
}

// ChangeSource streams row changes. For polling (Route A) Changes returns after
// one drained batch; for log-based sources (Route B) it blocks and streams until
// ctx is cancelled. fn returning an error aborts iteration.
type ChangeSource interface {
    Changes(ctx context.Context, fn func(ChangeEvent) error) error
    Close() error
}
```

### Route A — Polling change source (DB-agnostic; Phase 1)

`NewPollingChangeSource(driver, dsn, PollingOptions)` reuses the existing
`sqlSource` plumbing with an added **incremental predicate**. Per cycle it runs,
per configured table:

```sql
SELECT * FROM <table> WHERE <cursorColumn> > <watermark> ORDER BY <cursorColumn> [ , <pk> ] LIMIT <batch>
```

and emits `OpInsert`/`OpUpdate` events (it cannot distinguish the two and does
not need to — apply is an idempotent upsert). It advances the watermark to the
max `cursorColumn` seen.

- **PollingOptions:** `Tables []TableCursor` where `TableCursor{Table, CursorColumn, KeyColumns []string}`; `Interval time.Duration`; `BatchSize int`.
- **Cannot see hard deletes.** Documented explicitly. Soft deletes (a flag column,
  e.g. `deleted_at IS NOT NULL`) surface as updates and can be mapped to a delete
  by the caller's MappingPlan if desired (v1: treated as update; hard-delete
  capture requires Route B).
- **Cursor requirements:** a monotonic `updated_at` or sequence column. Ties on
  the same timestamp are handled with `>=` + a `(cursor, pk)` ordering and
  per-batch dedup against the last-seen key, so no row is skipped or double-applied.

This requires extending the connector SQL source: today `readRows` does
`SELECT * FROM <quoted table> [LIMIT n]` with no predicate. Add an internal
`readRowsWhere(table, predicate, args, orderBy, limit)` used by the polling
source; the existing `Schemas`/`Records` paths are unchanged.

### Route B-PG — Postgres logical replication (Phase 2)

`NewPostgresCDCSource(dsn, PostgresCDCOptions)` uses
`github.com/jackc/pglogrepl` (pure Go, pairs with the existing pgx dependency)
to consume a logical replication slot and decode WAL into I/U/**D** events.

- **Source prerequisites (documented):** `wal_level = logical`, a replication
  role, a publication, and a replication slot. Options carry slot + publication
  names; v1 may create the slot if absent (configurable) but assumes the
  publication exists.
- Decodes `pgoutput` messages → `ChangeEvent`. `REPLICA IDENTITY` must expose the
  primary key so deletes carry `Key` (documented; default-deny: if a delete event
  lacks a usable key, it is logged and skipped, never applied blindly).
- Checkpoint = the confirmed flush LSN.

### Route B-MySQL — binlog (Phase 3)

`NewMySQLBinlogSource(dsn, MySQLBinlogOptions)` uses
`github.com/go-mysql-org/go-mysql` (pure Go) to act as a replica and read
**ROW-format** binlog into I/U/**D** events.

- **Source prerequisites (documented):** `log_bin` on, `binlog_format = ROW`, a
  user with `REPLICATION SLAVE, REPLICATION CLIENT`, and a unique `server-id`
  (configurable in options).
- Maps `RowsEvent` (write/update/delete) → `ChangeEvent`, using table metadata to
  name columns and identify the primary key.
- Checkpoint = `(binlog filename, position)` (or GTID set when available).

### Watcher — the apply loop (library API)

```go
type WatcherOptions struct {
    Desensitizer *Desensitizer          // signed plan; required
    Mapping      importflow.MappingPlan // required; see key precondition below
    Checkpoint   CheckpointStore        // defaults to the knowledge-DB-backed store
    InferenceRefresh bool               // run incremental RDFS refresh after a batch
}

func NewWatcher(db *cortexdb.DB, src ChangeSource, opts WatcherOptions) (*Watcher, error)
func (w *Watcher) Run(ctx context.Context) error // blocks until ctx done or fatal error
```

Per `ChangeEvent`:

- **Insert / Update:** wrap the single event row as a one-row `importflow.Source`,
  apply `connector.Desensitized(oneRow, d)`, and call
  `importflow.New(db).Run(ctx, safeOneRow, mapping)`. Because `SaveKnowledge`
  takes an explicit `KnowledgeID` and `UpsertKnowledgeGraph` is idempotent on
  deterministic IRIs, re-applying the same key **updates** rather than
  duplicates. This reuses the entire tested connector + importflow path,
  including desensitization and the deterministic token vault.
- **Delete:** resolve the row's stable identifiers from `Key`:
  - RAG chunk id = the same id `importflow` would mint, i.e. the configured
    `RAGPlan.IDColumn` value (the primary key). If that column is pseudonymized,
    run the key value through the same `Desensitizer` (deterministic token) so it
    matches what was indexed. → `db.DeleteKnowledge(...)`.
  - Entity IRI(s) = `urn:cortexdb:<Type>:<rendered IDTmpl>` for each `EntityMap`
    whose `IDTmpl` is built from the key (same deterministic rendering). The entity
    is fully removed: `db.DeleteTriples` is called for the pattern with that IRI as
    **subject** (its rdf:type, label, props, outgoing relations) and again with it
    as **object** (incoming relations), so no dangling edges remain.
- After each drained batch (Route A) or every N events / time window (Route B),
  persist the checkpoint, then optionally run incremental
  `RefreshKnowledgeGraphInference`.

Error handling: an apply error aborts the batch without advancing the
checkpoint, so the next run retries from the last committed position (at-least-once
delivery; apply is idempotent for I/U, and delete-of-absent is a no-op, so
re-delivery is safe).

### Key precondition (enforced)

For updates and deletes to address the right RAG chunk and entity, the
`MappingPlan` **must** key on a stable primary key:

- every `TablePlan.RAG` must set `RAGPlan.IDColumn` to the table's PK (not the
  synthesized `table:row`), and
- every `EntityMap.IDTmpl` used as a delete target must be built from the PK.

`NewWatcher` validates this against the source's declared key columns and returns
an error if a watched table routes to RAG/KG without a stable key. Fail closed.

## Checkpoint store

A small table in the **knowledge DB** (co-located with the data it syncs, so a
single file is still the unit of backup/restore):

```sql
CREATE TABLE IF NOT EXISTS connector_sync_state (
    source_key TEXT PRIMARY KEY,  -- stable id for (source, tableset)
    cursor     TEXT,              -- Route A: max watermark (JSON per table)
    position   TEXT,              -- Route B: LSN / binlog pos / GTID
    updated_at TEXT NOT NULL
);
```

`CheckpointStore` interface: `Load(sourceKey) (Checkpoint, error)` /
`Save(sourceKey, Checkpoint) error`. Default impl is SQLite-backed on the
knowledge DB handle; the interface lets a caller substitute their own.

## Dependencies (pure Go, no CGO)

- `github.com/jackc/pglogrepl` — Route B-PG only.
- `github.com/go-mysql-org/go-mysql` — Route B-MySQL only.

Route A adds none. Both new deps are pure Go, consistent with the project's
no-CGO constraint, and only imported by their respective source files.

## Honest limitations (documented, not hidden)

- Route A polling **cannot capture hard deletes** and depends on a reliable
  monotonic cursor column; clock skew / non-monotonic cursors can miss rows.
- Near-real-time is **single-node, seconds-scale**. Not a high-throughput stream
  processor; for very high event rates, run a dedicated CDC/stream engine upstream
  and sync derived knowledge in.
- DDL/schema drift on the source is not replicated; a new sensitive column added
  after signing is not auto-classified (it simply isn't in the plan, and the
  desensitizer fails closed by dropping unlisted columns).
- Delete-of-absent and re-applied upserts are no-ops (at-least-once + idempotent),
  but ordering across tables is not transactionally guaranteed.

## Testing

- **Route A:** throwaway Postgres + MySQL (skipped without DSN). Insert rows →
  run watcher one cycle → assert upsert into RAG/KG; update a row → assert content
  changes (same id, not duplicated); advance + restart → assert resume from
  checkpoint (no reprocessing).
- **Route B-PG:** container with `wal_level=logical`; I/U/D → assert the graph and
  RAG reflect the delete (chunk + triples gone).
- **Route B-MySQL:** container with ROW binlog; I/U/D → same assertions.
- **Privacy:** every route asserts no raw PII string reaches RAG content or graph
  export (reuse the connector leak-check helper).
- **Unit:** ChangeEvent apply loop with a fake `ChangeSource` (no DB) covering
  insert/update/delete → upsert/delete calls and checkpoint advance; key-precondition
  validation error path.
- CI-safe: live-DB tests skip without DSNs; the fake-source apply test always runs.

## Decomposition / delivery order

One spec, three implementation phases (each independently shippable and testable):

1. **Phase 1 — Route A** (polling, PG/MySQL/Neon): ChangeEvent model, ChangeSource
   interface, PollingChangeSource, Watcher apply loop (incl. delete-by-key resolver
   used by all routes), checkpoint store, key-precondition validation, fake-source
   unit test + live polling test. This delivers near-real-time upsert sync end to
   end.
2. **Phase 2 — Route B-PG** (logical replication): PostgresCDCSource + deps +
   live test (incl. delete propagation).
3. **Phase 3 — Route B-MySQL** (binlog): MySQLBinlogSource + deps + live test.

## Resolved decisions

- Deletes: **hard delete** — sync-remove the RAG chunk and the row's entity
  triples (Route B captures deletes; Route A cannot).
- Surface: **library API only** (`Watcher.Run(ctx)`), no daemon/MCP in v1.
- Checkpoint: stored in the **knowledge DB** (`connector_sync_state`).
- Scope: A + B for **both** databases, delivered in three phases.
