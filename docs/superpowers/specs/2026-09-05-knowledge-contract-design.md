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
| `_producer` | enum | yes | How it was made. alchemy's `Producer` strings **as its connector writes them today** (`ddl`, `graph-import`, `tabular`, `llm-extract`, `human` — `pkg/alchemy/types.go`; not the proto enum names, which never leave the RPC) plus two: **`measured`** — a value obtained by running a governed query against a system of record — and **`compiled`** — derived deterministically from a declared model (a metric definition, a schema). |
| `_grade` | enum | yes | See below. |
| `_state` | string | no | The producer's own word for where this record stands, verbatim: `met_but_costly`, `needs_review:conflict`, `too_coarse`, `PENDING`. Displayed as detail; never interpreted across producers. |
| `_at` | RFC 3339 | yes | When the record was produced (for a measurement: when it was measured; for a claim: when it was published; for a person's assertion: when they made it). A writer with no producer-side time writes the moment it put the record on the shelf — alchemy stamps no clock on an extraction because its results are content-addressed, so its connector fills `_at` at commit. |
| `_by` | string | when `_producer=human` | The named person who asserted this record into the graph. **Not** the speaker a report quotes — that is what a claim is *about*, and it lives in the graph as an `attributed_to` edge, where it can be queried; folding it into `_by` would make "who put this here" and "who the report says said it" one field. |
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

**alchemy** (connector already writes every key but the new five). What the
connector can say is bounded by what reaches it: a rejected record is removed
before any sink sees it (`pkg/review/apply.go` `VerbReject`), a held result is
refused whole (`ErrHeld`, §7.3), and `Conflict` names its sides as prose with
no `Ref`. So the table has two halves.

Reachable on the write path today:

| alchemy | `_producer` | `_grade` | `_state` |
|---|---|---|---|
| `ddl` / `graph-import` / `tabular`, not reviewed, no violation | as today | `self_consistent` | — |
| `llm-extract`, not reviewed | as today | `asserted` | — |
| `human` (an `Assert`) | as today | `asserted` | — (`_by` already written) |
| `ReviewedBy` set — the record survived review | as today | `verified` | — (the verb is not on the wire; nothing is invented) |
| a `Violation` whose `About` names this record | as today | `refused` | `violation:<ViolationKind>`; `_why` = `Violation.Detail` |
| a fused edge (several relations → one CortexDB edge) | — | its **least established** member's grade | — |

Review outranks a violation: the reviewer was shown the finding. A fused edge
takes the weakest member's grade, the same rule the connector already applies
to `inferred`; every member's own provenance stays in `_provenance`.

Not reachable without a change in `pkg/alchemy` / `pkg/sink`, and therefore
**not written** rather than guessed:

| row | why not | what it needs |
|---|---|---|
| `refused` from a review rejection | `review.Apply` deletes the record before a sink sees it | `Result` carrying its rejections |
| `_state` = review verb | `ReviewedBy` is a list of names; no verb reaches a sink | a decision on `Result` |
| `held` | the only survivable NEEDS_REVIEW is a conflict, and §7.3 refuses the whole result first | `review.Kind` reaching a sink |
| `_contradicts` | `Conflict{left,right}` carries statements, not `Ref`s — recovering ids means parsing prose, which `Violation.About` exists to abolish | an `About Ref` on each side of `Conflict` |

**argus** (writes through alchemy; its ontology `world@3` is the declared type,
and it adds nothing of its own):

| argus | key |
|---|---|
| the article (URL alchemy was handed as the source) | `_source` — finer than the outlet: the outlet is derivable from it and is already in the graph as the `Source` node the `reports` edge points at |
| `Claim.published_at` | not `_at` — `_at` is when alchemy's connector put the record on the shelf; `published_at` stays a `Claim` attribute where a query can compare the two |
| `attributed_to` (the speaker) | stays an edge; **not** `_by` (see the key table) |
| every `Claim` | `_grade=asserted`, `_producer=llm-extract` — free, from alchemy's row |
| `contradicts` edge | stays an ontology edge, queryable as such. `_contradicts` on the two claims is **not written today** — see alchemy's "not reachable" table; it needs `Conflict` to carry `Ref`s |

So argus gets every key it can truthfully have without a line of its own code.
What it does not get (`_contradicts`) is an alchemy gap, not an argus one.

**DataIntelligence** (writes `InsertGraphDocument`; today's `source: "di"` stays as a plain attribute):

| DI | `_source` | `_producer` | `_grade` | `_state` / `_why` |
|---|---|---|---|---|
| metric document (`di-recall::index`) | database name | `compiled` | `self_consistent` | — |
| di-anchor `Anchored` | engagement | `measured` | `verified` | — |
| di-anchor `Ambiguous` / `TooCoarse` | engagement | `measured` | `held` | `_why` from the verdict |
| di-anchor `NoMatch` | engagement | `measured` | `refused` | `_why` |
| precedent, verdict ∈ {met, met_but_costly, short, missed} | engagement | `measured` | `verified` | `_state` = verdict |
| precedent, verdict `incomparable` | engagement | `measured` | `held` | `_why` = "measure redefined after adoption" |
| precedent, verdict `rejected`, name kept | engagement | `human` | `refused` | `_why` = `Precedent.why`; `_by` = `Precedent.who`, from `Decision::who` |
| precedent, verdict `rejected`, name lost | engagement | `measured` | `refused` | `_why` only — a rejection is never signed by a guess |
| `_audit` row, `refused = true` | database | `measured` | `refused` | `_why` = `note` |

Note what the DI rows do **not** carry: `baseline`, `measured`, `target`. See
the hard rule.

And note the two `rejected` rows. `_by` is required whenever a record claims
`human`, so the claim and the name have to move together: a precedent whose
ledger kept the name is signed, and one whose ledger did not stays `measured`
with no `_by`. A rejection carrying an invented signature destroys the one
thing that record is for, which makes "sign it with whoever is nearest" the
worst of the three available answers.

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
