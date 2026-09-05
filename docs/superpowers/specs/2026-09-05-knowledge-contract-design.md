# Knowledge Contract — what a record must carry to be trusted and explained

Date: 2026-09-05
Status: draft (pending review)

## Why this exists

An agent is two things: an orchestration harness, and a body of knowledge it
can trust and explain. The first half is one thing with one interface
(harness-rs, agent-go: the agent loop, with tools behind it). The second half
is four producers in two languages — alchemy, argus, DataIntelligence, and
CortexDB's own memory — and they already share one interface: **every one of
them writes into CortexDB.** No producer imports another. They all pass through
this door.

What the door does not have is a contract. harness-rs says what a tool must
declare to be callable. Nothing says what a record must carry to count as
"trusted and explainable" once it is on the shelf. So each producer invented
its own vocabulary for the same question — *how do I know this, and how sure
am I* — and the three vocabularies do not share a word:

| producer | its own words |
|---|---|
| alchemy | `Provenance{source, chunk, producer, model, ontology, chunking, confidence, reviewed_by, rule_set, by, at}` · `Violation` · `Conflict{left,right}` · `NEEDS_REVIEW` (conflict / violation / guess / low_confidence / duplicate) |
| argus | `Claim` (a report, not a fact) · `Source` —`reports`→ `Claim` · `attributed_to` · `contradicts` (both ways, both stay) |
| DataIntelligence | `di-anchor`: Anchored → VERIFIED / NoMatch / Ambiguous / TooCoarse · SELF-CONSISTENT · `di-consult`: Met / MetButCostly / Short / Missed / Incomparable / rejected · `_audit`: one row per decision, refusals included |

The moment a human wants to see all of this on one wall — the "situation room"
— three vocabularies become three colour schemes the reader has to learn. And
an agent recalling across collections gets three shapes for one question. The
contract is the single vocabulary, and it lives here because this is the door.

## What it is

**A set of reserved metadata keys** under the `_` prefix, written by the
producer onto whatever CortexDB record it creates — a GraphRAG document, an
entity, a relation, a knowledge record, a memory. CortexDB already stores
`map<string,string> metadata` on every one of those and treats it as opaque.
The contract names the keys, their types, and the closed value sets. Nothing
about the wire changes.

This ratifies what alchemy's CortexDB connector already does
(`connectors/cortexdb/provenance.go`: `_source _chunk _producer _model
_ontology _chunking _confidence _reviewed_by _rule_set _ruled_by _by _at
_provenance …`). Those keys keep their names and meanings. The contract adds
the five things the other producers needed and alchemy never had to say:

- **`_grade`** — the cross-producer answer to "how is this true", five values.
- **`_state`** — the producer's own fine-grained word, verbatim, never
  normalised. This is what keeps `_grade` from flattening anything.
- **`_contradicts`** — the other record(s) this one disagrees with. Both stay.
- **`_why`** — required whenever a record is held or refused.
- **`_producer` gains two values** — `measured` and `compiled` — for a
  producer whose output is a number under governance, not an extraction.

Why keys and not a proto message: the producers are Go and Rust, and a typed
`Provenance` field on every RPC would mean changing the server, three clients
and every producer at once, to carry what a `map<string,string>` already
carries. The single source of the names is `pkg/cortexdb/contract.go`; the
Rust client mirrors it from this document.

## The keys

Required on every record that claims to be knowledge: `_source`, `_producer`,
`_grade`, `_at`.

| key | type | required | meaning |
|---|---|---|---|
| `_source` | string | yes | Where it came from: a URL, a file name, a job, an engagement or database **name**. Never a DSN, never a path with credentials. |
| `_chunk` | int | no | Chunk index within the source. `-1` when the producer did not work in chunks (DDL, graph import, a measurement). alchemy's semantics, unchanged. |
| `_producer` | enum | yes | How it was made. alchemy's `Producer` values as written today (`PRODUCER_DDL`, `PRODUCER_GRAPH_IMPORT`, `PRODUCER_TABULAR`, `PRODUCER_LLM_EXTRACT`, `PRODUCER_HUMAN`) plus two: **`PRODUCER_MEASURED`** — a value obtained by running a governed query against a system of record — and **`PRODUCER_COMPILED`** — derived deterministically from a declared model (a metric definition, a schema). |
| `_grade` | enum | yes | See below. |
| `_state` | string | no | The producer's own word for where this record stands, verbatim: `met_but_costly`, `needs_review:conflict`, `too_coarse`, `PENDING`. Displayed as detail; never interpreted across producers. |
| `_at` | RFC 3339 | yes | When the record was produced (for a measurement: when it was measured; for a claim: when it was published). |
| `_by` | string | when `_producer=PRODUCER_HUMAN` | The named person or, for a claim, the speaker the report attributes it to. **Not** the outlet — that is `_source`. |
| `_why` | string | when `_grade` ∈ {`held`, `refused`} | The reason, in words a person can act on. A refusal without a reason is noise the reader will delete. |
| `_contradicts` | JSON `[]string` | no | Ids of records this one cannot both-be-true with. Written on **both** records by whoever detects it. The disagreement is information, not an error. |
| `_confidence` | float `[0,1]` | no | alchemy's extraction confidence. Never a substitute for `_grade`. |
| `_model`, `_ontology`, `_chunking`, `_reviewed_by`, `_rule_set`, `_ruled_by`, `_deterministic`, `_run`, `_provenance` | as alchemy writes them | no | Unchanged. `_provenance` remains alchemy's JSON array for a fused edge. |

Everything **not** under `_` is the source's own attribute, verbatim — alchemy's
rule, kept.

## `_grade` — five values, and why not one ladder

The three producers' ladders are not one axis. di-consult's verdicts are the
*outcome of an acceptance*; di-anchor's are *how a figure was pinned*;
alchemy's is *extraction confidence plus review*. Forcing them onto one ladder
would flatten exactly the distinctions each producer spends its documentation
refusing to flatten. So `_grade` answers one narrow question — **by what kind
of thing is this record's truth established** — and the producer's finer word
goes in `_state` untouched.

| `_grade` | established by | examples |
|---|---|---|
| `verified` | something outside the producer: a re-measurement, an external published figure, a named person's review | di-anchor `Anchored`; a di-consult acceptance (re-measured, whatever the outcome — `_state` says `met` or `missed`); an alchemy edge a reviewer accepted |
| `self_consistent` | internal coherence only — derived deterministically from something already stated, not checked against the world | DDL → graph; a compiled metric document; a slice-conservation identity that holds |
| `asserted` | a source or a model said so, and nothing has checked it | every argus `Claim`, by construction; an unreviewed LLM extraction; a human `Assert` |
| `held` | nothing yet — a person has to look | any alchemy `NEEDS_REVIEW`; di-anchor `Ambiguous` / `TooCoarse`; di-consult `Incomparable` (the measure changed under the plan) |
| `refused` | the producer declined to produce it and can say why | a rejected plan; an ontology violation; an `_audit` row for a governed query that was denied; di-anchor `NoMatch` |

Two rules that fall out:

- **A claim stays `asserted` even after review.** Reviewing a `Claim` confirms
  that the outlet said it, not that it is true. The thing that can become
  `verified` is a fact the claim is `about`, and that is a different record.
- **`refused` is a record, not an absence.** DataIntelligence is the only
  producer that stores its refusals today (`_audit`, "one row per decision,
  refusals included"). The contract makes that everyone's rule: a query the
  governance layer denied, an extraction the ontology rejected, a plan the
  client turned down — each is a record with `_grade=refused` and a `_why`.
  The reader must be able to tell "we have no precedent" from "we refused to
  form one".

## Mapping the three producers (the evidence the vocabulary holds)

**alchemy** (connector already writes every key but the new five):

| alchemy | `_producer` | `_grade` | `_state` |
|---|---|---|---|
| deterministic (DDL, graph import, tabular without LLM) | as today | `self_consistent` | — |
| LLM extraction, not reviewed | `PRODUCER_LLM_EXTRACT` | `asserted` | — |
| reviewer accepted (`reviewed_by` set, verb accept) | as today | `verified` | verb |
| reviewer rejected | as today | `refused` | verb; `_why` = the rejection |
| any `NEEDS_REVIEW` | as today | `held` | `needs_review:<ReviewKind>`; `_why` = finding detail |
| `Violation` | as today | `refused` | `violation:<ViolationKind>` |
| `Conflict{left,right}` | — | both sides keep their own grade | `_contradicts` written on both |

**argus** (writes through alchemy; its ontology `world@3` is the declared type):

| argus | key |
|---|---|
| `Claim.text`, `published_at` | record content; `_at` |
| `Source` (outlet domain, via `reports`) | `_source` |
| `attributed_to` (the speaker) | `_by` |
| every `Claim` | `_grade=asserted`, `_producer=PRODUCER_LLM_EXTRACT` |
| `contradicts` edge | `_contradicts` on both claims |

**DataIntelligence** (writes `InsertGraphDocument`; today's `source: "di"` stays as a plain attribute):

| DI | `_source` | `_producer` | `_grade` | `_state` / `_why` |
|---|---|---|---|---|
| metric document (`di-recall::index`) | database name | `PRODUCER_COMPILED` | `self_consistent` | — |
| di-anchor `Anchored` | engagement | `PRODUCER_MEASURED` | `verified` | — |
| di-anchor `Ambiguous` / `TooCoarse` | engagement | `PRODUCER_MEASURED` | `held` | `_why` from the verdict |
| di-anchor `NoMatch` | engagement | `PRODUCER_MEASURED` | `refused` | `_why` |
| precedent, verdict ∈ {met, met_but_costly, short, missed} | engagement | `PRODUCER_MEASURED` | `verified` | `_state` = verdict |
| precedent, verdict `incomparable` | engagement | `PRODUCER_MEASURED` | `held` | `_why` = "measure redefined after adoption" |
| precedent, verdict `rejected` | engagement | `PRODUCER_HUMAN` | `refused` | `_why` = `Precedent.why`; `_by` = who rejected |
| `_audit` row, `refused = true` | database | `PRODUCER_MEASURED` | `refused` | `_why` = `note` |

Note what the DI rows do **not** carry: `baseline`, `measured`, `target`. See
the hard rule.

## Hard rule: judgements travel, numbers do not

CortexDB is a shared brain. DataIntelligence draws its line at the number: the
consultant's own words (goal, method, guard names, expected relative change, a
verdict) may leave the client's database; anything measured from the client's
data may not. `Precedent` has *no field* for a measured value — "not a reminder
not to write it; structurally no place to put it".

The contract adopts that line for every producer: **no contract key, and no
record content written under the contract, carries a measured value from a
governed source.** `_grade=verified` on a measurement means "re-measured and it
matched"; the value stays in the system of record and the record points back
to it by `_source`. alchemy and argus never had to think about this because
their sources are public; the rule costs them nothing and protects the first
client whose engagement lands next to a news graph.

## Non-goals (v1)

- **No new RPC, no proto change.** The keys ride in existing `metadata` maps.
- **No server-side enforcement.** `pkg/cortexdb.ValidateContract` is a library
  call a producer makes before writing; the server stores what it is given.
  Enforcement at the door is a later decision, once the three producers write
  conformant records and we know what the rejections would have been.
- **No renaming alchemy's keys.** Its connector is already the most complete
  writer; it gains five keys and changes none.
- **No propagation.** CortexDB does not copy a document's `_*` keys onto the
  entities and relations extracted from it. The producer writes the keys on
  each record it means them for.
- **No Rust validator yet.** `clients/rust` mirrors the constants from this
  document; a generated validator follows once the key set has survived one
  producer.
- **No `_supersedes`.** Memory already has `supersedes` natively; whether
  knowledge records need one is a separate question.

## What happens next, in order

1. `pkg/cortexdb/contract.go`: constants and `ValidateContract`. This document
   is normative; the code is its executable form.
2. DataIntelligence `di-recall`: add the contract keys to `InsertGraphDocument`
   metadata for metric documents and precedents. DI first because it is the
   only producer that already stores refusals and acceptance outcomes — its
   rows exercise every `_grade`.
3. alchemy `connectors/cortexdb`: write `_grade`, `_state`, `_why`,
   `_contradicts` alongside the keys it writes today. argus follows for free.
4. Then there is one shape to render.
