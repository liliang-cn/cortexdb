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

Non-goals (v1): REST/飞书 sources, an un-mask key-custody service, distributed
multi-node import. These are v2.

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
- `pseudonymize` — consistent fake value; reversible only by the data owner via a
  tenant-held map
- `generalize` — `34`→`30-40`, city→province, date→month
- `keep` — non-sensitive

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

## Privacy invariants (enforced, not aspirational)

1. **Default-deny**: a column that is unclassified but "looks sensitive" defaults
   to the strictest action (drop/redact), never `keep`. Fail toward safety.
2. **Deterministic hash preserves relationships** for GraphRAG edges (per-tenant salt).
3. **Irreversible into the LLM**: anything fed to RAG/embeddings uses irreversible
   actions (redact/generalize/hash) only. Pseudonymize/reversible never reaches the LLM. (Design invariant — locked.)
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
- Agent-callable: add `connector_introspect`, `connector_plan`,
  `connector_run` to a toolbox + MCP (same pattern as importflow's toolbox), so
  SuperLeo/harness can drive "connect my DB → review plan → import" over MCP/gRPC.
- Multi-tenant: salt + output DB are per-tenant, consistent with the one-file-
  per-brain model.

## Testing

- Rule classifier: table-driven over column-name/value fixtures.
- Masking actions: golden tests per action (mask/hash determinism, generalize buckets).
- Desensitizer-as-Source: a fake `Source` → assert post-mask `Schemas()` drops
  columns and `Records()` masks values; unsigned plan → `Run` refuses.
- Free-text scan: fixtures with embedded phone/email/id/name → assert redaction +
  reported residual-risk note.
- Postgres/MySQL sources: integration tests against disposable containers
  (skipped when no DB available), introspection + streaming.
- Both no-embedder (lexical) and embedder modes for the downstream import.

## Open decisions

1. Un-mask / reversibility key custody (pseudonymize map) — v2; v1 ships only
   irreversible + deterministic-hash.
2. Exact LLM classification prompt + confidence threshold for escalation to human.
3. Whether `connector` gets its own MCP binary or rides importflow's.
