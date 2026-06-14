# Data Connector with Privacy/Desensitization — Design

Date: 2026-06-12
Status: draft (pending review)

## Goal

A connector that turns **real-world data** into **agent-usable knowledge**, with
desensitization as a first-class citizen. It sits in front of CortexDB
`pkg/importflow` and adds the two layers ImportFlow does not own: **live-source
schema introspection** and a **privacy/desensitization gate**.

```
source → introspect(schema) → classify(PII) → MaskingPlan (human-signed)
       → desensitize → importflow.Run → RAG + knowledge graph
```

Non-goals (v1): REST/飞书 sources, distributed multi-node import. These are v2.
**Reversibility / un-mask key custody IS in v1** (see "Reversibility & key
custody" below).

## Where it lives

`github.com/liliang-cn/cortexdb/v2/pkg/connector` (Go). Rationale: the sink
(RAG+KG, LLM for classification, storage) already lives in CortexDB; building a
separate crate would force raw data across a process/language boundary **before
desensitization completes**, violating the core invariant. The agent side
(harness/SuperLeo) only triggers import + queries results over MCP/gRPC — no new
crate needed.

Optional complementary library (separate, out of this spec): a tiny
`harness-rs-redact` for in-process, pre-storage redaction inside a Rust agent.
That is a utility, not the connector.

## Core trust invariant

**Schema-first, data-second.** Introspection and classification use only
structure + a few sample rows. No bulk plaintext flows until the user has
reviewed and **signed the MaskingPlan**. This is the trust anchor.

## Key architectural move: the desensitizer is a `Source` decorator

`pkg/importflow` already defines:

```go
type Source interface {
    Schemas(ctx) ([]Schema, error)        // introspection + samples (cheap)
    Records(ctx, func(Record) error) error // streaming rows
    Close() error
}
```

The privacy gate wraps any `Source` and returns a `Source` whose `Records`
yields **desensitized** rows, so it drops transparently into the existing
pipeline:

```go
live  := connector.NewPostgresSource(dsn)            // or MySQL
plan  := connector.BuildMaskingPlan(ctx, live, classifier) // schema-only
//      → user reviews & signs plan (CLI/UI/tool)  ← DATA STILL HASN'T MOVED
safe  := connector.Desensitized(live, plan)          // Source decorator
report, _ := importflow.New(db, ...).Run(ctx, safe, mappingPlan)
```

`safe.Schemas()` returns the **post-masking** schema (dropped columns removed),
so ImportFlow's mapping inference never even sees sensitive columns.

## Types

```go
// Live sources implementing importflow.Source via information_schema.
func NewPostgresSource(dsn string, opts SourceOptions) (importflow.Source, error)
func NewMySQLSource(dsn string, opts SourceOptions) (importflow.Source, error)
// SourceOptions: tables allow/deny list, sample size (default small), row limit.

type PiiKind string  // none, name, phone, email, national_id, address,
                     // bank_card, dob, gender, geo, ip, custom...
type Sensitivity int // Public, Internal, Confidential, Restricted

type Classifier interface {
    // Classify a column from its name + type + sample values (NOT full data).
    Classify(ctx, col Column, samples []string) (PiiKind, Sensitivity, Reason)
}

type MaskAction string // drop, redact, mask, hash, pseudonymize, generalize, keep

type ColumnRule struct {
    Table, Column string
    PiiKind       PiiKind
    Sensitivity   Sensitivity
    Action        MaskAction
    Reason        string   // why classified (rule id / LLM note)
    Source        string   // "rule" | "llm" | "human"
}

type MaskingPlan struct {
    Columns   []ColumnRule
    TextScan  []TextScanRule // free-text columns to NER-scan in place
    SignedBy  string         // non-empty == approved; unsigned plans refuse to run
    SignedAt  time.Time
}

type Desensitizer interface { Apply(r importflow.Record) importflow.Record }
```

### MaskAction semantics

- `drop` — column never imported (removed from schema too)
- `redact` — replaced with `[REDACTED]` (irreversible)
- `mask` — partial: `138****1234`, `张*`, `a***@b.com`
- `hash` — deterministic token, **per-tenant salt**: same input → same token, so
  GraphRAG entity edges survive without leaking the原值
- `pseudonymize` — consistent fake value; reversible only by the data owner via
  the tenant vault (see below)
- `generalize` — `34`→`30-40`, city→province, date→month
- `keep` — non-sensitive

Reversible actions (`pseudonymize`, keyed `hash` when marked reversible) write
the original→token mapping to the **vault** so the data owner can un-mask later.
Irreversible actions (`redact`, `generalize`, unsalted/one-way `hash`) never
touch the vault.

## Classification: three layers (v1)

1. **Rules** (column name patterns + value regex: phone/email/national-id/
   bank-card/name lists) — fast, deterministic, runs first.
2. **LLM** — for columns rules are unsure about, send column name + a few sample
   values (already a trust-boundary crossing: gated to sampled, non-bulk data,
   and only when the user opted into LLM classification) → proposed
   PiiKind/Sensitivity. Reuses CortexDB's existing LLM interface; no new SDK.
3. **Human sign-off** — the proposed `MaskingPlan` is presented for review; the
   user edits actions and **signs**. `Run` refuses an unsigned plan.

## Free-text PII (v1 includes this)

Text columns (and, downstream, document chunks) get a `TextScanRule`: a PII pass
that finds names/phones/IDs embedded in prose and redacts **in place** before the
text is chunked/embedded.

- Layer A: regex/rule scanners (phone, email, national-id, bank-card, IP) — high
  precision, deterministic.
- Layer B: optional LLM/NER pass for names/addresses regex can't catch.
- Honest boundary (documented, not hidden): free-text NER **will miss things**;
  residual re-identification risk is surfaced in the report, never claimed to be
  zero.

## Reversibility & key custody (v1)

Some fields must be recoverable by the data owner (e.g. resolve a pseudonymized
customer back to the real record for an operational action), while **never**
being recoverable by the LLM or by anyone holding only the knowledge DB. v1 ships
a **token vault** that makes this split explicit.

```go
type KeyProvider interface { TenantKey(ctx, tenant string) ([]byte, error) }
// impls: EnvKey, FileKey (0600), later KMS. Keys are never logged or persisted
// in the knowledge DB.

type Vault interface {
    // Put stores enc(original) under a stable token; returns the token to embed.
    Put(ctx, tenant, piiKind, original string) (token string, err error)
    // Resolve reverses token→original; requires the tenant key. Audited.
    Resolve(ctx, tenant string, tokens []string) (map[string]string, error)
}
```

Design:

- **Separate file, separate trust domain.** The vault is its own
  `*.vault.db` (SQLite), distinct from the CortexDB knowledge file. Leaking the
  agent's knowledge DB does **not** leak originals — un-mask needs *both* the
  vault file *and* the tenant key.
- **Encryption.** Values are stored as AES-256-GCM ciphertext under the
  per-tenant key from `KeyProvider`. Tokens are deterministic per (tenant, kind,
  value) so relationships/joins survive (GraphRAG edges), but the token reveals
  nothing without the vault+key.
- **The only reverse path** is `connector.Unmask(ctx, tenant, tokens, keyProvider)`
  → `map[token]original`. Every call is recorded in an audit log (who/when/how
  many) for compliance.
- **The LLM never gets a reverse path.** RAG/embeddings receive only tokens
  (or fully irreversible values). The vault is operational-side only; it is never
  read during retrieval/inference. This keeps invariant #3 intact.
- **Right to erasure.** Dropping a tenant's vault file (or a token row)
  permanently destroys reversibility — a clean GDPR-style erase that leaves the
  knowledge graph's structure intact (tokens become opaque forever).

## Privacy invariants (enforced, not aspirational)

1. **Default-deny**: a column that is unclassified but "looks sensitive" defaults
   to the strictest action (drop/redact), never `keep`. Fail toward safety.
2. **Deterministic hash preserves relationships** for GraphRAG edges (per-tenant salt).
3. **Irreversible into the LLM**: anything fed to RAG/embeddings only ever sees a
   token or a fully irreversible value. Reversible originals live solely in the
   tenant vault and are never read during retrieval/inference. (Design invariant — locked.)
4. **In-place before any trust boundary**: desensitization runs tenant-side /
   in-process, before data enters the LLM or leaves the tenant's DB.
5. **Audit & provenance**: the run records which column got which action and how
   many values were affected (counts), for compliance evidence — mirrors
   CortexDB's "with evidence" approach.
6. **Schema-first, data-second** (the trust anchor, above).
7. **Honest residual risk**: quasi-identifier combinations (DOB+gender+zip) can
   re-identify; the report flags this rather than pretending zero risk.

## Integration & surface

- `Run` path: `importflow.New(db).Run(ctx, connector.Desensitized(src, plan), mappingPlan)`.
- Agent-callable: **ride importflow's existing toolbox + MCP** — no separate
  binary. `connector_introspect`, `connector_plan`, `connector_run`, and
  `connector_unmask` are registered onto the importflow MCP/toolbox surface
  (connector depends on importflow, extends its tool set). SuperLeo/harness drive
  "connect my DB → review plan → import → (later) un-mask" over the same MCP/gRPC.
- Multi-tenant: salt, vault file, and output DB are all per-tenant, consistent
  with the one-file-per-brain model.

## Testing

- Rule classifier: table-driven over column-name/value fixtures.
- Masking actions: golden tests per action (mask/hash determinism, generalize buckets).
- Desensitizer-as-Source: a fake `Source` → assert post-mask `Schemas()` drops
  columns and `Records()` masks values; unsigned plan → `Run` refuses.
- Free-text scan: fixtures with embedded phone/email/id/name → assert redaction +
  reported residual-risk note.
- Vault: deterministic token per (tenant,kind,value); `Unmask` with the right key
  round-trips original; wrong/absent key fails; dropping the vault file makes
  tokens permanently opaque; tokens reaching the RAG sink are never reversible
  there (no vault read on the retrieval path).
- Postgres/MySQL sources: integration tests against disposable containers
  (skipped when no DB available), introspection + streaming.
- Both no-embedder (lexical) and embedder modes for the downstream import.

## Open decisions

1. Exact LLM classification prompt + confidence threshold for escalating an
   "unsure" column to mandatory human review.
2. `KeyProvider` v1 impls to ship: env var + file (0600) are in; KMS/age/SOPS later.
3. Vault audit-log destination: same vault file vs a separate append-only log.

Resolved: connector rides importflow's MCP (no separate binary); reversibility /
un-mask key custody is in v1 via the tenant vault (above).
