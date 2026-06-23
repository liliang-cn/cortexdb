# profileflow — LLM-maintained user profile — Design

Date: 2026-06-23
Status: draft (pending review)

## Goal

A new `pkg/profileflow` workflow layer that maintains an evolving **narrative
profile** of a person (habits, preferences, goals, communication style) from
signals the caller pushes in (chat snippets, knowledge, stated preferences). The
profile is a concise prose summary, updated by **incremental merge** as new
signals arrive, suitable for injecting into prompts to personalize an agent.

It is an explicitly **LLM-backed** feature: the prose synthesis goes through an
interface seam that reuses the existing `graphflow.JSONGenerator` — no LLM SDK
enters `pkg/`, consistent with the rest of the repository.

## Why a new layer (not agentmem / facade)

`agentmem` already has `TypePreference` memories, `MentalModel` rules, and
`ContextSlot`s, and `memoryflow` already extracts `PromotionKindPreference`
candidates — but those are deterministic, no-LLM stores. A narrative profile is
inherently LLM-synthesized, so it belongs in a `*flow` workflow layer (siblings:
`memoryflow`, `graphflow`, `importflow`), not bolted onto the no-LLM `agentmem`
or the already-large `cortexdb` facade. `profileflow` is a thin layer on top of
the `cortexdb` facade, owning its own tables via `db.SQL()` (the same pattern as
`connector`'s `SQLiteCheckpointStore`).

## Decisions (resolved during brainstorming)

- Profile representation: **synthesized narrative** (prose), not structured attributes.
- Update model: **incremental merge** (current summary + new signals → evolved summary).
- Signal source: **push** — the caller supplies signal text via `Update`; the
  module does not pull from agentmem/memoryflow/knowledge itself.
- No-LLM behavior: **LLM required** for `Update`/`Refresh` (clear error without a
  configured synthesizer). `Get`/`ListSignals`/`Delete` work without one.
- v1 ops: `Update`, `Get`, `Refresh` (full re-synth), `ListSignals`, `Delete`, MCP tools.

## Storage

Two tables, created with `CREATE TABLE IF NOT EXISTS` in `New()` via `db.SQL()`:

```sql
CREATE TABLE IF NOT EXISTS profileflow_profiles (
  subject      TEXT PRIMARY KEY,
  narrative    TEXT NOT NULL,
  version      INTEGER NOT NULL,
  signal_count INTEGER NOT NULL,
  created_at   TIMESTAMP NOT NULL,
  updated_at   TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS profileflow_signals (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  subject    TEXT NOT NULL,
  source     TEXT NOT NULL DEFAULT '',
  text       TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_profileflow_signals_subject
  ON profileflow_signals (subject, id);
```

`subject` is an opaque owner key chosen by the caller (e.g. `"user:liang"`),
making the store multi-user-safe within one DB. `profileflow_signals` is an
append-only provenance log and the input for `Refresh`.

## Public API (`pkg/profileflow`)

```go
type Store struct { /* db *cortexdb.DB; syn Synthesizer */ }

type Option func(*Store)
func WithSynthesizer(s Synthesizer) Option

// New ensures the schema and returns a Store. Without WithSynthesizer, the
// read/delete ops still work; Update/Refresh return an error.
func New(db *cortexdb.DB, opts ...Option) (*Store, error)

type Signal struct {
    Text      string
    Source    string    // optional tag, e.g. "chat", "knowledge"
    CreatedAt time.Time // set by the store on read
}

type Profile struct {
    Subject     string
    Narrative   string
    Version     int
    SignalCount int
    UpdatedAt   time.Time
}

// Update appends sig to the log, then merges it into the current narrative.
// Requires a Synthesizer. Bumps version and signal_count.
func (s *Store) Update(ctx context.Context, subject string, sig Signal) (Profile, error)

// Get returns the current profile. A missing profile is not an error: it
// returns a zero Profile (Version == 0, Narrative == "") and nil error.
func (s *Store) Get(ctx context.Context, subject string) (Profile, error)

// Refresh re-synthesizes the whole narrative from the entire signal log,
// ignoring the current summary (antidote to incremental drift). Requires a
// Synthesizer. Errors if the subject has no signals.
func (s *Store) Refresh(ctx context.Context, subject string) (Profile, error)

// ListSignals returns the append-only provenance log, newest first.
func (s *Store) ListSignals(ctx context.Context, subject string, limit, offset int) ([]Signal, error)

// Delete removes the narrative and all signals for a subject.
func (s *Store) Delete(ctx context.Context, subject string) error
```

Behavioral notes:
- `subject` must be non-empty (error otherwise).
- `Update`: insert the signal row; load current narrative (may be ""); call
  `syn.Merge(ctx, current, []Signal{sig})`; if the result is empty → error and
  do NOT overwrite the narrative (never silently wipe); else upsert
  `profileflow_profiles` with the new narrative, `version+1`,
  `signal_count = COUNT(signals)`, `updated_at = now`. The store stamps all
  timestamps from its internal clock (injectable in tests — see Testing).
- `Refresh`: load all signals (oldest→newest); `syn.Merge(ctx, "", all)`; same
  empty-guard; upsert with `version+1`.

## LLM seam

```go
// Synthesizer merges new signals into a profile narrative.
type Synthesizer interface {
    Merge(ctx context.Context, current string, signals []Signal) (string, error)
}

// LLMSynthesizer is the default Synthesizer, backed by a graphflow.JSONGenerator.
type LLMSynthesizer struct { Client graphflow.JSONGenerator }
```

`LLMSynthesizer.Merge` builds a system + user prompt and asks the model to
return `{"summary":"..."}`, then sanitizes markdown fences (same helper style as
`importflow.sanitizeJSON`) and unmarshals. This reuses the exact LLM seam used by
`importflow.LLMInferer`/`graphflow.LLMExtractor` — no new generator interface.

- System prompt: *"You maintain a concise, factual profile of a person built from
  accumulated signals — their chats, knowledge, and stated preferences. Given the
  CURRENT profile and NEW signals, return an updated profile that integrates the
  new information: keep stable traits, update changed ones, and capture habits,
  preferences, goals, and communication style. Be concise (a few short paragraphs
  or bullet lines), factual, and avoid speculation. Return ONLY JSON
  {\"summary\":\"...\"}."*
- User prompt: JSON `{"current": "<narrative>", "signals": ["<text>", ...]}`.
- Errors: nil `Client` → error; generator error → wrapped error; unmarshal
  failure or empty `summary` → error (so the Store's empty-guard preserves the
  prior narrative).

## MCP / toolbox

`pkg/profileflow/toolbox.go` exposes a `Toolbox` (mirrors `importflow.Toolbox`):
- `profileflow_update`  — input `{subject, text, source?}` → profile
- `profileflow_get`     — input `{subject}` → profile
- `profileflow_refresh` — input `{subject}` → profile
- `profileflow_list_signals` — input `{subject, limit?}` → `{signals}`
- `profileflow_delete`  — input `{subject}` → `{ok:true}`

`NewToolbox(store)`, `Definitions()`, `Call(ctx, name, input)` dispatch, plus
`pkg/profileflow/mcp.go` with `NewMCPServer(store, opts)` / `RunMCPStdio(ctx,
store, opts)` following the `importflow`/`memoryflow` MCP wiring. `update`/`refresh`
surface the "requires a Synthesizer" error when none is configured.

## Errors & edge cases

- Empty `subject` → error from every op.
- `Update`/`Refresh` with no `Synthesizer` → `"profileflow: Update requires a
  Synthesizer (use WithSynthesizer)"` (and the analogous message for Refresh).
- `Refresh` on a subject with no signals → error (nothing to synthesize).
- Synthesizer returns empty/whitespace → error; the stored narrative is left
  unchanged. The signal row inserted by `Update` remains in the log (it is real
  provenance even if synthesis failed) — documented behavior.
- `Get`/`ListSignals` on an unknown subject → zero Profile / empty slice, nil err.
- `Delete` on an unknown subject → nil err (idempotent).

## Testing

All non-live tests are deterministic and CI-safe (real temp SQLite, fake LLM):
- `fakeSynth` implementing `Synthesizer` (records `current` + `signals` it was
  given, returns a canned narrative or an error): drive `Update` (assert merge
  input = current narrative + the new signal, version/signal_count bump, narrative
  persisted), `Refresh` (assert merge input = "" + all signals), `Get` (round-trip
  + missing→zero), `ListSignals` (order, limit/offset), `Delete` (idempotent +
  wipes signals), and the empty-result guard (narrative unchanged on empty merge).
- No-synthesizer: `Update`/`Refresh` error; `Get`/`ListSignals`/`Delete` succeed.
- `LLMSynthesizer` with a fake `graphflow.JSONGenerator` returning
  `{"summary":"..."}`: assert it parses, returns the prose, handles fenced JSON,
  and errors on empty summary / nil client / generator error.
- `Toolbox`: dispatch each tool with a `fakeSynth`-backed store; assert the
  "requires a Synthesizer" error when none is configured.
- Optional skipped live test (`OPENAI_API_KEY` gate) exercising `LLMSynthesizer`
  against the `.env` model, like the DDL→KG live test.

To keep tests deterministic without `Date.now()`-style nondeterminism, the Store
takes an injectable clock (a `func() time.Time` field defaulting to `time.Now`),
set in tests to a fixed time. This is an internal detail, not part of the public
option surface.

## Files

- `pkg/profileflow/profile.go` — `Store`, `New`, schema, `Update`/`Get`/`Refresh`/
  `ListSignals`/`Delete`, `Signal`/`Profile`/`Option` types, clock.
- `pkg/profileflow/synth.go` — `Synthesizer`, `LLMSynthesizer`, prompts, sanitize.
- `pkg/profileflow/toolbox.go` — `Toolbox` + tool defs + dispatch + JSON-schema helpers.
- `pkg/profileflow/mcp.go` — `NewMCPServer` / `RunMCPStdio`.
- `pkg/profileflow/*_test.go` — `profile_test.go`, `synth_test.go`, `toolbox_test.go`.

## Non-goals (v1)

- No automatic pulling from agentmem/memoryflow/knowledge (push-only).
- No structured attributes / queryable fields (narrative only).
- No sectioned narrative or per-aspect updates.
- No embeddings / semantic search over signals.
- No new LLM SDK in `pkg/` — only `graphflow.JSONGenerator`.
- No changes to `agentmem` or the `cortexdb` facade.

## Docs to update at implementation/release time

`README.md`, `README_CN.md`, `SKILL.md` (architecture layer list + tool list),
and the version bump — consistent with how prior features were released.
