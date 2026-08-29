# Changelog

All notable changes to this project will be documented in this file.

## [2.84.1] - 2026-08-29

### Fixed

- **Closing a live view no longer waits on the streams it is closing.** `Shutdown` stops
  the listener at once and then waits for handlers to finish, and every open page is
  holding an SSE stream that ends only when its request context does — so the wait always
  ran to its deadline. Two seconds of nothing on every close, with the caller's lock held,
  and the timeout swallowed so it looked like a clean shutdown.

  Measured: 2.00s before, 0.25s after. It surfaced downstream as an application's view of
  one graph still answering while it was switching to another.

## [2.84.0] - 2026-08-29

### Added

- **The live 3D view is a package now, not just a tool.** `pkg/liveview` — moved out of
  `internal/`, where it had been for exactly one release. Nothing about it changed except
  that other modules can import it: it was internal because the only consumer was the MCP
  server, and that stopped being true the moment an application wanted to put the graph
  on one of its own screens.

  A caller supplies a `Source` — anything that reads nodes and edges — so an embedder that
  already knows which brain it talks to can say so directly with `LoadRemote`, rather than
  setting environment variables for `OpenSource` to read back.

  The listener still binds `127.0.0.1` and still has no option to widen it. The page is
  the whole graph with nothing in front of it; an embedder that needs it reachable further
  should put its own authenticated proxy there.

## [2.83.0] - 2026-08-29

### Added

- **The brain as something you watch, not something you re-render.** `render_graph_html`
  writes a file and is finished; the picture is true for the instant it was taken.
  The new `serve_graph_3d` tool holds a page open instead — a rotatable WebGL scene
  on `127.0.0.1` that keeps up with the brain on its own.

  Two things move on it, and they arrive by different routes:

  - **Structure** — nodes and relations appearing or disappearing. There is no change
    feed to subscribe to, and the graph can be written by any machine sharing it, so
    the server re-reads it every two seconds and streams the difference. Only the
    difference: the layout keeps every node that did not change exactly where it had
    settled, and reheats only when something new actually arrives.
  - **Activity** — a query running, something being saved, a relation being drawn.
    A query changes nothing in the graph, so no amount of polling can ever show one.
    These are observed as they happen, from inside the MCP server handling the call,
    and the nodes they named light up on the page while the answer is still being
    written.

  That second half is why the view is served from the MCP process rather than a
  side-car. Both modes get it from one hook — `AddReceivingMiddleware` on the
  `*mcp.Server` — which is the only point local mode (tools registered from the
  library) and shared-brain mode (tools proxied over gRPC) have in common. Neither
  the library's tool surface nor the gRPC proxy had to learn that a view exists.

  On the page: glow is a real bloom pass, so brightness is earned — a hub with forty
  edges is drawn bigger and blooms wider, and a node the brain just touched blooms
  because it went white. Links carry moving light, and **Trace path** takes two nodes
  and lights the chain between them while everything else falls away. The camera
  orbits on a toggle and yields the moment you drag it.

  Also `--graph-3d` on the command line, for a view without an agent attached. It
  polls, so it sees structure and not queries — and says so on the page, because a
  ticker that never moves is otherwise indistinguishable from a broken one.

  The listener binds `127.0.0.1` and there is deliberately no flag to widen it: the
  page is every entity anyone stored, with no authentication in front of it.

- **A place to put a graph that is not the brain.** Four tools — `side_graph_write`,
  `side_graph_read`, `side_graph_list`, `side_graph_drop` — write nodes and edges into a
  named graph kept in its own local database, and `serve_graph_3d` takes a `graph` name
  so each one can be watched in 3D on its own port.

  The brain is what is known. A run's steps, the plan behind them and what each was
  expected to do are none of that: they happened once, most are wrong by tomorrow, and
  there are thousands of them. A few thousand step nodes on top of a few thousand real
  ones and the graph stops being readable.

  This is the mechanism and not the policy. Nothing in the implementation mentions runs,
  steps or expectations — CortexDB does not know what those are. It gives a caller more
  than one graph and gets out of the way; what belongs in which is the caller's decision.

  Two things follow from how the store actually works, rather than from preference:

  - A side graph is a **separate database file**, because `graph_nodes` and `graph_edges`
    have no tenancy column. There is no namespace to scope by, so "another graph" can
    only honestly mean another store. It also makes a side graph disposable by deleting
    one file, which is most of the point.
  - Side graphs are **always local**, even when the brain is shared. These tools are
    registered beside the renderers rather than proxied over gRPC, so scratch work never
    travels to the machine everyone else reads, and no server needs upgrading to use
    them. The brain is shared; your working notes are yours.

  A graph name becomes a filename, so it is checked against an allowlist
  (`^[a-z0-9][a-z0-9_-]{0,63}$`) rather than a blocklist — that list is never finished.
  `..`, `a/b`, `.hidden`, `a.db` and a name with a NUL in it are all refused.

### Fixed

- **Node labels no longer carry line breaks into the views that print them.** A label
  is usually the first line of the text it came from, and that text has newlines in
  it. `clipLabel` collapses whitespace before clipping, which also fixes the same
  ragged output in the static renderer.

- **`next` edges are no longer dropped by name.** Both graph readers skipped the type
  outright, alongside `has_chunk`, as structural. But `next` is also the obvious name for
  one thing following another — a plan's steps, a trace — and a caller who modelled a
  sequence got its nodes back with every link between them silently missing. What makes
  an edge structural is that it wires chunks, so the endpoints decide now, not the label.
  Chunk wiring stops inflating degree too, which is what decides who survives a truncated
  listing.

  Both read paths changed: the local SQL the views use, and `ListGraphAll`, which is what
  a shared brain answers with — so a shared deployment needs this version on the *server*
  before its own view shows those edges.

- **The shared-brain truncation note stopped repeating forever.** `fetchGraphRemote`
  prints "showing the 2000 most-connected of 4015 nodes" — useful once from a one-shot
  render, and nothing but noise from a poller calling it every two seconds.

### Performance

- **The live view runs at 59fps on a 2000-node, 5892-edge brain, up from 17.** Measured
  on the page, not estimated. Almost all of it was one thing: an arrowhead on every
  link is a separate cone mesh, and 5892 of them cost three quarters of the frame
  budget. Arrowheads and flow particles are now spent only where they say something —
  the traced path, and links the brain just wrote — on any graph large enough for the
  difference to matter.

  Two layout fixes came with it. Repulsion had no maximum range, so the handful of
  nodes with no edges drifted until the interesting part was a speck in the middle of
  empty space; capping the range keeps the graph together and is cheaper besides. And
  framing now ignores the far tail, because fitting everything means mostly framing
  the gap between the outliers and the rest.

## [2.82.2] - 2026-08-28

### Fixed

- **Tests no longer name their databases after a clock that cannot count that
  fast.** Nothing in the shipped library changed; this is the test suite, and it had
  been failing about one run in six for a reason that looked like a fault in the
  store:

      open lexical evaluation db: failed to initialize store:
      vectorstore: init: failed to enable foreign keys: database is locked (5) (SQLITE_BUSY)

  Two `t.Parallel()` tests both built their path as
  `fmt.Sprintf("test_evaluation_lexical_%d.db", time.Now().UnixNano())`. The name
  says nanoseconds; the clock underneath does not have to tick that fast, and on
  darwin/arm64 it ticks once per microsecond — two goroutines reading it together get
  the same number about three times in four. That is not two databases but one file
  opened by two connection pools, and the loser reports SQLITE_BUSY at Open. When the
  two missed each other at Open they collided over the data instead, one test's rows
  landing in the other's assertions.

  `internal/testname.Nano` replaces the call at all 150 sites: still an int64, still
  ordered, still roughly the wall clock, but never handed out twice in a process, and
  seeded with the pid because `go test ./...` runs packages in parallel. The
  evaluation fixture also moves into `t.TempDir()` — it had been writing its database
  next to the source and removing only half of it, leaving the `-wal` and `-shm`
  behind run after run.

  Measured both ways: with the old naming, 4 of 25 runs fail; with this, 0 of 25,
  five clean runs of the full suite on each backend, and a clean `-race`.

## [2.82.1] - 2026-08-28

### Fixed

- **A search survives the `vector` type being replaced under a live connection.**
  `vector` is not a built-in: its OID is assigned when the extension is created and a
  new one is assigned if it is ever dropped and created again — a restore, an
  extension reinstall, a test suite sharing the database. pgx caches prepared
  statements per connection and a statement's descriptor names its parameter and
  result type OIDs, so a connection that prepared a vector query beforehand then asks
  the server about an OID that is gone:

      ERROR: cache lookup failed for type 91164 (SQLSTATE XX000)

  It reaches only the statements carrying the vector type in their descriptor — a
  bound parameter or an operator operand — which is the vector search path and
  nothing else. The error is raised while the server resolves the types, before the
  statement runs, and pgx drops the offending cache entry on the way out, so the
  reads now retry once and succeed. Once rather than in a loop, because a second
  failure means something other than a replaced type. Matched on SQLSTATE *and*
  message text: XX000 is PostgreSQL's catch-all internal error and 0A000 covers every
  unsupported feature, so either code alone would swallow failures that have to reach
  the caller.

  Worth knowing alongside it, because the two arrive together: `DROP EXTENSION vector
  CASCADE` also drops every vector *column*. The tables survive and
  `embeddings.vector` does not, so an extension reinstall needs the columns put back
  whatever this retry does.

## [2.82.0] - 2026-08-27

### Added

- **A brain can live on PostgreSQL.** The DSN picks the backend: a path is the
  SQLite file it has always been, a `postgres://` URL is PostgreSQL with pgvector.
  Everything follows — documents, collections, sessions and messages, the graph, RDF
  triples, temporal facts, agent memory — and vector search happens in the database
  rather than in Go. `pkg/sqldialect` holds the handful of things that genuinely
  cannot be written once; the rest of the SQL is shared. SQLite is untouched and
  remains the default.
- **A fact can say where it came from.** `fact_provenance` and `uncited_facts`
  report which chunk asserted an edge and which edges nothing supports.
- **The ontology decides what contradicts what.** A link type declared ONE on a side
  makes a second value for that side a contradiction rather than a second fact, so
  supersession is deterministic instead of heuristic.

### Fixed

Ten defects found by sending the same JSON to the same tool on both backends and
comparing the answers — sixty-four calls, seventeen of which failed on PostgreSQL
while every unit test stayed green.

- **All graph-mode retrieval on PostgreSQL.** `json_valid`/`json_extract` were
  written out in four files; the one reading a chunk's `document_id` sits under every
  graph-mode query and raised `function json_valid(text) does not exist`. Now on the
  dialect, with `JSONFlag` kept separate from `JSONTextGuarded` because a JSON `true`
  reads back as the integer `1` on SQLite and the text `'true'` on PostgreSQL — an
  inference rule was re-deriving its own output.
- **Document deletion on PostgreSQL**, which used `json_each` and answered with
  "syntax error at end of input", and ran its queries on the raw handle so its
  placeholders were never rebound.
- **Lexical search was handed FTS5 syntax as if it were prose.** The retrieval layer
  quotes every token and emits `owner OR name`; `plainto_tsquery` read that as an AND
  over the English word "or", which no document contains. `ParseFTS5` reads the
  expression now, and the rank is built from the same parse.
- **Every memory search on PostgreSQL**, which asked for `messages_fts MATCH ?` — an
  FTS5 virtual table and an operator that database has neither of.
- **Object sets and ontology links**, which compared with `COLLATE NOCASE`:
  `collation "nocase" for encoding "UTF8" does not exist`.
- **Saving a memory on PostgreSQL.** The vector went in as SQLite's blob encoding,
  so `memory_save`, `knowledge_memory_remember` and every consolidation failed.
- **Recall accounting**, patched with `json_set` and discarding its errors by design,
  so it wrote nothing on PostgreSQL and said nothing about it: `recall_count` stayed
  at zero forever.
- **Ontology actions on PostgreSQL.** The audit table's DDL used
  `INTEGER PRIMARY KEY AUTOINCREMENT`, so it could not be created and no action could
  be applied. `SELECT EXISTS` scanned into an `int` was next in line.
- **Saving new knowledge on PostgreSQL.** `GetDocument` spelled "not found" as a bare
  error, and `SaveKnowledge` reads that answer with `errors.Is` to decide
  create-or-update.
- **Scored rows carry their collection name again** from the vector arm, which had
  drifted from the other two copies of the projection.

Two more that are not portability at all, found because the other backend disagreed:

- **SQLite's vector search post-filtered.** The index was asked for the globally
  nearest rows and the collection filter applied afterwards, so a search scoped to a
  collection whose rows missed the global top-k came back short — or empty. It failed
  worst on a large store with several collections, which is the case it exists for.
- **Graph edges were read with no `ORDER BY`.** SQLite happened to return insertion
  order; PostgreSQL returned whatever the plan produced, and could return something
  else next time. Neighbors feeds graph-mode retrieval, so the same question could
  retrieve a different set of chunks on two runs of one database.

## [2.81.0] - 2026-08-27

### Fixed

- **Entity resolution no longer merges across node types.** Grouping was by spelling
  alone, so "Primary" the state and "primary" the node — one canonical key apart —
  merged, putting a role where a machine belongs and ending every `has_state` edge at
  a Node. The deterministic pass now keys groups by type as well, and the LLM pass
  takes the first member's type as the group's. Untyped nodes share the empty type
  and still meet each other; same-type spellings merge exactly as before.

## [2.80.0] - 2026-08-26

### Fixed

- **A relation endpoint resolves to the entity the schema is about.** An endpoint
  names an entity, and a name can belong to more than one node; resolution took the
  first by id. In a store holding a prose vocabulary and a code graph — a "Snapshot"
  entity and a Go type of that name — the code node's id sorted first, so edges
  extracted from runbooks attached to structs. An endpoint now prefers a node whose
  type the ontology declares; among equally declared or undeclared candidates the id
  still decides, so a store with no ontology is unaffected.

## [2.79.0] - 2026-08-26

### Fixed

- **A flag is no longer merged with an identifier.** `canonicalKey` dropped
  punctuation, so `--read-only` and `ReadOnly` shared a key and entity resolution
  collapsed them under whichever won — losing the difference between what you paste
  into a shell and the field of the same idea, which is the one thing a CLI's help
  text is ingested for. Five of eighteen proposed merges on a live base were this
  shape, and node type did not separate them. A leading dash now survives into the
  key; two spellings of one flag still merge.

## [2.78.0] - 2026-08-26

### Added

- **Entity resolution can be scoped to node types.** `ResolveOptions.NodeTypes`
  (and `--resolve-entities --types A,B`) restricts the merge to the types named.
  Resolution reads a node's content as its name, so a store that also holds a code
  graph — where each symbol keeps its bare name as content and its path in the id —
  had every package's `main.go` sharing one canonical key, and a resolve pass merged
  distinct files into one with their edges repointed. Empty keeps every entity, which
  is what callers had. The CLI prints the scope so a merge over everything cannot be
  mistaken for a scoped one.

## [2.77.0] - 2026-08-26

### Added

- **Aliases resolve at query time.** `--resolve-entities` merged "K8s" into
  "Kubernetes" and recorded the alias — and a query naming the alias then derived the
  dead node's id and found nothing. Lookup now falls through to alias records when the
  direct node is gone. The resolve prompt also covers plural forms and cross-language
  names (VM/VMs, 李员外/Liang Li).
- **Recall usage accounting and `--memory-usage`.** Every memory a search returns gets
  `recall_count` and `last_recalled_at` stamped server-side, best-effort. Deliberately
  observability, not policy — recall begets recall, so counts never feed ranking. The
  report shows most-recalled memories and never-recalled ones older than a cutoff as
  prune candidates for `--export-memory` / `--sync-memory`.

## [2.76.0] - 2026-08-26

### Added

- **Automatic session capture — the write-side loop.** A SessionEnd hook hands the
  finished transcript to `--capture-session`, which distils it into fact-scoped
  memories via `CORTEXDB_LLM_*` (declining quietly when unset) and saves each with its
  entities, so auto-captured facts are reachable through semantic, lexical and graph
  recall alike. One fact per memory by design: long mixed notes embed to mush. The
  hook detaches immediately — a session's exit never waits on a model. Ids are stable
  per session and slug, so re-capture overwrites rather than stacks. Disable with
  `cortexdb-session-end --disable`. Measured: probes phrased in Chinese hit English
  auto-captured facts at rank 1.

## [2.75.1] - 2026-08-26

### Added

- **`--graph-cleanup [--dry-run] [--prune-only|--reindex-only]`.** Prunes generic
  entity nodes whose names the current extraction rules would never produce (typed
  nodes are exempt: a declared type means a person said what the thing is), naming
  every victim, and backfills graph presence for stored memories — entities extracted
  with the current rules, a memory node, mention edges — so the pre-2.74 backlog is
  reachable through entity_names like a new save. Capped at twelve entities per
  memory. Idempotent.

### Fixed

- **Graph memory recall cut on an alphabet when mention counts tied.** The candidate
  list was truncated to topK in SQL order before boosts ran; on a well-indexed graph a
  popular entity has hundreds of one-mention neighbours, so ties went to whichever
  memory id sorted first. Candidates are now boosted and sorted before the cut.

## [2.75.0] - 2026-08-26

### Added

- **Semantic memory recall with a calibrated noise floor.** The memory path now uses
  the cosine scores `SearchChatHistory` always computed and threw away, filters them at
  a floor calibrated on a live store (unrelated probes peaked at 0.263, the weakest
  genuine hit scored 0.311; the floor sits at 0.28), and merges above-floor semantic
  hits ahead of lexical ones instead of replacing them. Golden-set effect against a
  copy of the live brain: total recall@5 0.654 → 0.846, paraphrase 0.000 → 0.667 with
  both cross-lingual cases recovered. Saving no longer fails when the embedder does —
  the memory is stored without a vector and `--reembed-memories` (new) fills the
  backlog; the existing reembed pass covered knowledge chunks only.
- **`supersedes` on memory_save.** A correction names the memories it replaces; they
  stay stored and exported with `superseded_by` in their metadata, but recall stops
  presenting them as current. Superseding a missing id fails loudly.
- **Importance and age rank memories, as a bounded tie-breaker.** The multiplier lives
  in [0.85, 1.0]: equal matches order by importance and recency, a genuinely better
  match cannot be dethroned by being old. Raw multiplicative decay was measured first
  and rejected — it pushed correct months-old answers out of the top ranks.
- **`--recall-eval <golden.jsonl>`** measures recall quality (recall@K, MRR, forbidden
  ids, score spread, per category) against either brain, so retrieval changes are
  judged by a fixed yardstick.

## [2.74.1] - 2026-08-23

### Fixed

- **`--export-memory` dropped memories, and `--sync-memory --prune` would have
  finished the job.** Measured against a store of 2043: the export reported all of
  them and wrote 2041 files, of which one held the index. Three memories were gone,
  and because the directory is what a sync treats as the truth, pruning from it would
  have deleted those three for real. Three defects, all in the export. `slugify`
  returned `"memory"` when nothing survived — every CJK-only title did — and on macOS
  and Windows `memory.md` is `MEMORY.md`, so writing the index overwrote a memory; the
  id, which is ASCII and always present, is the fallback now, and the index name is
  reserved before the first file is written. `uniqueSlug` assumed its suffixed form was
  free: `"situation"` seen nine times produced `"situation-9"`, which is also what a
  memory titled `"situation 9"` slugs to, and the second write replaced the first — each
  candidate is checked now. And nothing compared the file count to the memory count, so
  both losses were silent; the export verifies before reporting success and fails
  loudly otherwise. The frontmatter `name` is the slug the file actually got, so it can
  no longer be empty. The same store now exports 2043 files and syncs back with nothing
  to create, update or delete.

## [2.74.0] - 2026-08-23

### Fixed

- **Memory recall ranked its worst matches first.** FTS5 `bm25()` is negative and a
  better match is more negative. Both memory paths turned it into a score with
  `1/(1+|rank|)`, which is decreasing in match quality, and then sorted descending — so
  the `ORDER BY` handed back the best match first and the scoring put it last. A long
  memory that mentioned a term once outranked a short one about nothing else, and the
  top-K cut discarded the real hits. Measured on a five-document corpus: the strong
  match scored 0.589 against the weak match's 0.807. In a populated store it showed up
  as recalled memories whose scores sat inside a 0.006-wide band — no discrimination
  left to rank on, so recall read as noise. `pkg/core/chat.go` was never affected: it
  orders by `bm25()` in SQL and never converts, which is why knowledge retrieval kept
  working while memory recall did not.

### Added

- **Memories can be corrected and forgotten by editing files.** `--export-memory` wrote
  a directory of Markdown files but nothing read one back, so removing a wrong memory
  meant calling `memory_delete` with an id nobody had written down. (`--import-agent-memory`
  is not the reverse: it scans `~/.claude` and `~/.codex`, writes into a knowledge
  collection rather than the memory store, and only ever adds.) `--sync-memory [dir]
  [--prune] [--dry-run]` closes the loop, using the frontmatter `metadata.id` that the
  export already emits to match a file to its record; a file without one gets an id from
  its name, so a memory can also be written by hand. Planning is separate from applying,
  because with `CORTEXDB_REMOTE` set the same plan is replayed through `memory_save` and
  `memory_delete`. `--prune` is opt-in and refuses to run against a directory holding no
  memory files — an empty directory is far more likely to be the wrong path than an
  instruction to delete everything — and deletions are listed by id before they happen.

- **Entity hints can reach a memory, because memories are now in the graph.**
  `saveMemoryGraph` wrote the entities a memory was saved with but nothing joining them
  to it: mention edges hang off `ChunkIDs` and it passed none. The graph grew entity
  nodes no memory pointed at, so memory search answered `entity_names` with "this API
  does not use graph expansion" — which was true; there was no edge leading to a memory
  to walk. A memory now gets a node of its own, typed `memory` rather than left to the
  stub filler that would call it a chunk, and each entity mentions it, putting memories
  on the same footing as knowledge chunks. Graph hits are scored by how many of the
  named entities a memory mentions and merged ahead of lexical ones, which still run to
  fill top-K, so a graph miss degrades to the old answer instead of no answer. Auto mode
  will not infer graph from entity-looking words in the query on this path: memory graph
  presence is opt-in, so a guess usually finds nothing and only mislabels the decision on
  the way to the same lexical answer. `memory_search` takes `entity_names` directly.

## [2.73.0] - 2026-08-23

### Added

- **A link side may name an interface, so a polymorphic relation can be traversed.**
  An interface was a first-class thing on one half of the schema and did not exist on
  the other: a type filter on `FindNodes`, search or brain expanded one into its
  implementors, while a link side refused to name one at all. The gap was not inert.
  Foundry models a relation whose source is polymorphic — anything Protector protects a
  Volume — by pointing a side at the interface, and with that unavailable the only
  encoding left was an open supertype standing in for "any of these". CortexDB decides a
  traversal's direction by keeping only nodes of the far side's object type, and nothing
  is ever stored under a supertype's name, so every such traversal returned the empty
  set — which is also what the graph says about a subject it knows nothing about, on a
  schema that read as complete. Validation, `orientLink` and the search-around filter now
  all ask the type closure instead of comparing the name.

### Fixed

- **Two ambiguities interfaces make reachable are now rejected at save time.** A side
  name has to identify one hop from the object type a traversal starts at, and an
  interface makes a collision indirect: `protects` on Protector and `protects` on
  Snapshot do not collide by name, but a traversal starting at a Snapshot matches both
  and declaration order decides which. And a link whose two ends overlap without being
  identical has no unambiguous direction — with one side over {Snapshot, Backup} and the
  other over {Backup, Volume}, an edge between two Backups reads both ways. Complete
  overlap stays legal: that is a self-link, where either orientation is the same
  statement. A foreign key on an interface side is also refused, since a key is a column
  on one concrete row.

## [2.72.2] - 2026-08-21

### Fixed

- **Auto-recall injects memories, not just knowledge.** The `--recall` mode behind the
  Claude Code `UserPromptSubmit` hook asked `knowledge_search`, which reads durable
  knowledge only — so anything written with `memory_save` never reached a prompt
  automatically, however well it matched. The failure looked like working software:
  memories were injected, they were simply always knowledge documents. Both paths now
  ask for the fused view (`knowledge_memory_recall` on a shared brain,
  `KnowledgeMemory().Recall` on a local one), memories first, with light graph
  expansion because this runs before every message. Memory bodies are flattened to one
  line and cut to 220 runes — knowledge arrives pre-snippetted, memories arrive whole.
  A brain on an older server still answers in the knowledge-only shape, and that shape
  is still parsed.

## [2.72.1] - 2026-08-20

### Fixed

- **Graph read paths no longer fail with "no such table" on a fresh database.** The
  `graph_nodes`/`graph_edges` schema was only created lazily by write paths via
  `InitGraphSchema`, so read-only tools hitting a database that had never taken a graph
  write — `find_nodes`, `object_set_resolve`, `expand_graph`, graph queries, the graph
  branch of `knowledge_memory_recall` — errored with `SQL logic error: no such table:
  graph_nodes` (or `graph_edges`) instead of returning an empty result. `Open` now
  initializes the graph schema eagerly (idempotent, cached), so every read path sees the
  tables from the first query and an empty database answers with empty sets. Downstream
  callers that special-cased these errors as "empty graph" no longer need to.

## [2.72.0] - 2026-08-19

### Changed

- **README overhaul (EN + CN) to match the code.** Documented the `KnowledgeMemory` brain facade
  (`Recall`/`Remember`/`Reflect`/`Consolidate`/`BuildContextPack`/`PromoteToKnowledge` and graph
  exploration), composable retrieval via `db.Query`/`cortex_query` (prefetch lanes, RRF/DBSF fusion,
  the `Authorize` retrieval gate and pluggable reranking), deterministic `extract_conversation`,
  property-graph inference (`apply_inference`) next to the RDFS `knowledge_graph_infer_*` surface,
  entity provenance + `delete_document_graph`, and the supporting packages (`eval`, `rpcserver`,
  `agentmem`/`hindsight`, `semantic-router`, `quantization`, `geo`). The Chinese README also picks up
  the ontology `enforcement: "vocabulary"` mode and the interface/object-type name-collision rule it
  was missing.
- **Version manifests re-synced with releases.** `version.go` and the plugin manifests sat at 2.67.0
  while 2.68–2.71 shipped, so plugin installs kept downloading the 2.67.0 MCP binary — this release
  moves the pinned binary forward past the 2.68–2.71 retrieval fixes (FTS5 query sanitization, the
  `Authorize` widening, the HNSW `ef >= k` fix).

## [2.71.0] - 2026-08-15

### Fixed

- **HNSW search was capped at ~50 results regardless of TopK.** `ef` is the size of the candidate
  list a graph walk keeps, so it must be at least `k` — the search was calling the index with
  `k = TopK*2` and the configured `EfSearch` (default 50), which returns `ef` neighbours and says
  nothing about the rest. Every vector search therefore truncated at ~50: a request for 2000 came
  back with 50, and any row ranked past that was unreachable through any API — deep retrieval,
  pagination-style over-fetch, and the `Authorize` widening added in 2.70.0 all silently hit the
  same invisible ceiling. `ef` is now raised to `k` when the configuration sets it lower.

## [2.70.1] - 2026-08-15

### Fixed

- **`Authorize` widening stopped one round too early.** A recall that returned fewer rows than it
  asked for was read as "the corpus is spent", but hybrid recall fuses vector and BM25 results and
  dedupes, so it is routinely short while a wider fetch would still reach more. Only a round that
  adds no new rows ends the search now. Without this the widening still missed the authorized rows
  in the case it was written for.

## [2.70.0] - 2026-08-15

### Fixed

- **`Authorize` now delivers the TopK it documents.** The retrieval gate promised that "the search
  over-fetches internally so the caller still receives up to TopK authorized results", but the
  over-fetch was a single fixed round of `max(TopK*5, 50)`: a predicate selective enough to matter
  saw every one of its rows ranked below that cut and the caller got a near-empty result that reads
  as "nothing matched". This is worst for the feature's main use case — an RBAC subject who may read
  a small share of the corpus could search and find almost nothing, with no error to say why. The
  gate now widens its recall (doubling, bounded at 4096) until TopK candidates pass or the corpus is
  exhausted; a generous predicate still costs exactly one pass. Found in a store where one imported
  code graph outnumbered the prose ten to one and scored identically, so no prose row reached rank 50.

## [2.69.0] - 2026-08-15

### Added

- **Entity provenance, and deletion shaped like ingest.** `upsert_entities` now records the union of
  every `document_id` that asserted an entity in the node's `source_document_ids`, and creates stub
  chunk nodes for mention edges whose chunks were embedded outside the graph — previously those edges
  died on the foreign key, for a long time silently, so a store could hold hundreds of entities with
  no record of what mentioned them and no way to ask "where did this come from". On that provenance
  sits **`delete_document_graph`** (also `GraphRAGToolbox.DeleteDocumentGraph`): it removes a
  document's chunk and document nodes, its relation edges, and the entities it alone asserted;
  entities other documents also assert are detached, not deleted. `dry_run` reports without removing.
  Stores audited after a few rebuild cycles carried ~20% orphaned entity nodes; this is the missing
  half of the ingest lifecycle that produced them.
- **`enforcement: "vocabulary"` on ontology schemas.** The strict default forced extraction
  pipelines to choose: activate the schema and lose every extracted entity to primary-key validation
  (LLM extraction from prose has no storage identity to offer), or leave it inactive and lose
  canonical type spellings and interface retrieval with it. A vocabulary schema canonicalizes
  without gating: declared types with a supplied primary key still get typed node IDs, keyless and
  undeclared ones fall back to name-derived IDs, undeclared link types pass through.
  `strict_actions` and vocabulary enforcement are rejected as contradictory at save time.

### Fixed

- **Batch write failures are no longer silent.** The graph batch writers report per-row rejections
  in their result rather than their error return, and every ingest-path caller discarded the result:
  an ingest whose every row was rejected still answered "ok". Callers now fold the result back into
  an error (`BatchResult.Err`), and the batch writers create the graph schema themselves instead of
  failing every row with "no such table" when a caller reached them before initialization.

### Notes

- The v1 `graph_ontology_schemas` table is legacy: v2 stores schemas in `ontology_schemas_v2` and
  neither reads, writes nor drops the old table. An empty `graph_ontology_schemas` in an upgraded
  store is expected and ignorable.

## [2.68.0] - 2026-08-15

### Fixed

- **A user's query is no longer read as FTS5 syntax.** Hyphenated and punctuated terms
  (`on-drbd-demote-failure`, `al-extents`) made FTS5 raise a syntax error instead of matching;
  queries are now sanitized into plain terms before they reach the index.

## [2.67.0] - 2026-08-10

### Added

- **A Palantir-style ontology, replacing ontology-lite.** Object types now carry typed properties
  and a mandatory primary key, so two spellings of one airport stop becoming two nodes — identity is
  `objectType + primaryKey` rather than a normalised name. Link types are bidirectional with a
  cardinality per side, the way Foundry models them: a one-to-many link is one `ONE` side and one
  `MANY` side, and only the `ONE` side may name a foreign key. 14 property data types, including
  `vector`, `geopoint`, `array` and `struct`.
- **Interfaces give polymorphic retrieval.** An object set or `find_nodes` query against `Facility`
  returns every implementing object type, and a new implementor becomes visible without touching the
  query. Interfaces may extend several parents; cycles and unsatisfied required properties are
  rejected at save time rather than surfacing later as a confusing write failure.
- **`object_set_resolve` — a composable object set algebra.** `base`, `interface_base`, `static`,
  `reference`, `filter`, `search_around`, `union`, `intersect`, `subtract`, with predicates
  `eq/lt/lte/gt/gte/in/is_null/contains/starts_with/contains_all_terms/contains_any_term/nearest_neighbors`
  and `and/or/not`. This is the part worth having: vector KNN, full-text terms and link traversal
  become peer operators inside one expression instead of three retrieval APIs that cannot be
  combined. Chained `search_around` is capped at three hops, matching Foundry's own limit.
- **`ontology_action_apply` / `ontology_action_list` — governed writes.** An action declares typed
  parameters, declarative rules and submission criteria, and every application is recorded in an
  audit trail. `validate_only` checks parameters and criteria without writing; `return_edits` reports
  what changed (the two are mutually exclusive, as in OSDK 2.0). Setting `strict_actions` on a schema
  closes the generic upsert tools so actions become the only write path — the point being that an
  agent gets a reviewed, named surface rather than free-form graph access.
- **`ontology_diff`** — reports what changed between two schema versions and flags the changes that
  would invalidate data already written: removed types, changed property types, newly required
  properties, a changed primary key, tightened cardinality.
- **Typed tool generation from a schema** (`GenerateOntologyTools`) — turns action and object types
  into tool definitions whose parameter names, types and required-ness the model can see, instead of
  prose inside a generic upsert. Bounded at 32 by default and deliberately not auto-registered: the
  cost of a large generated surface lands on the agent's context window.
- **gRPC `schema_json`** on `SaveOntologySchemaRequest` and `OntologySchema`. The v1 repeated fields
  cannot express a primary key or a typed property, so they are deprecated (numbers retained) and a
  request setting only them is refused with a message saying so, rather than silently ignored.

### Fixed

- **Relations whose endpoints were created in the same batch failed validation** with "could not
  resolve" — the resolver only looked in the graph, never at the entities being written alongside.
- **Endpoint resolution by name could return a chunk.** `graph_nodes.content` also holds chunk text
  and document titles, and `chunk:` sorts before `entity:`, so an unqualified lookup preferred them.
- **Node type casing was whatever the last writer used.** Node IDs already folded case, so
  `type: "airport"` and `type: "Airport"` were one node with a flapping `node_type` column that
  leaked into subgraphs, exports and model context. Ontology-validated writes now store the declared
  casing.

### Known limitations

- `OntologyProperty.Vectorized` is declarative only — no write path embeds those properties yet, and
  entity vectors are lexical hashes, so `nearest_neighbors` is meaningful only with an explicit query
  vector.
- An active ontology constrains `SaveKnowledge`: its built-in extractor emits untyped entities, so
  the schema needs a catch-all `entity` object type and a `related_to` link type.
- `modify_object` does not update `graph_nodes.content`, so name-based endpoint resolution still
  finds the pre-rename title.
- Foundry's function runtime, branches/proposals, dynamic row-level security and backing datasources
  are deliberately not modelled.

## [2.63.2] - 2026-08-04

### Added

- **`memory_list_all`** — bulk listing for the views that need every record rather than a search
  result. It reports `truncated` when a limit cut the listing short, because an export that silently
  dropped records would look complete and not be.
- **`memoryflow_apply_memory_edits`** — apply memory edits an agent has already decided on: add,
  update, or supersede. The apply path is exposed rather than the propose path on purpose: whoever
  calls the tool is a model that has just read the conversation, so having it decide the edits is
  better informed and cheaper than a second LLM round-trip inside the server. Superseding is opt-in,
  capped, and reversible — the old memory is kept and linked forward.
- **LLM-driven memory maintenance in `pkg/memoryflow`** (`ProposeMemoryEdits`, `ApplyMemoryEdits`,
  `UpdateMemoryFromText`) — the memory-side equivalent of `UpdateGraphFromText`, deliberately
  without its delete. A graph fact removed in error is re-derivable; a memory is often the only
  record that something was said.

### Fixed

- **Three memoryflow tools were defined but never reachable over MCP** —
  `memoryflow_resolve_taxonomy`, `memoryflow_list_episodes` and `memoryflow_get_transcript` were in
  `Definitions()` and absent from `NewMCPServer`, so they worked through the Go API and were
  invisible to every MCP host. Same gap `find_nodes` shipped with in 2.62.0; `pkg/cortexdb` grew a
  coverage test then, and `memoryflow` — which has the identical two-list shape — never did. It does
  now, and the test found all three on its first run.
- **`--memory-html` and `--export-memory` ignored the shared brain**, rendering a local file that
  nothing writes to on a machine using a remote. They now fetch every record over gRPC.

## [2.63.1] - 2026-08-04

### Fixed

- **Auto-recall ignored the shared brain.** `--recall` returned from `main` before the
  `CORTEXDB_REMOTE` branch was ever reached, so a machine configured for a shared brain still
  answered the `UserPromptSubmit` hook from its own local database file. The failure is silent and
  reads as working software: memories are injected on every prompt, they are simply the wrong ones —
  frozen at whenever that machine switched over. Found on a host whose local file had been read-only
  for hours while every write went to the shared brain; the hook kept surfacing month-old entries and
  never once surfaced anything written that afternoon. The hook now calls `knowledge_search` over
  gRPC when `CORTEXDB_REMOTE` is set, and both paths render hits through one formatter so the text an
  agent sees never depends on where the brain lives. Silent on every failure as before: a bad token,
  an unreachable server, or an unexpected response shape all inject nothing rather than blocking the
  prompt.
- The other one-shot modes (`--graph-html`, `--export-memory`, `--learn-path`) still act on a local
  database; the docs now say which is which.

## [2.62.1] - 2026-07-31

### Fixed

- **`find_nodes` was defined but not reachable over MCP.** It shipped in 2.62.0 with a schema, a
  dispatcher entry, tests and a changelog entry — and was absent from every host that speaks MCP
  rather than the Go API, because `NewMCPServer` registers each tool by hand and that list is
  separate from `Definitions()`. Nothing about the symptom points at the cause: a host asks for a
  tool and is told it does not exist, by a server whose release notes say it does. Found within the
  hour by the application it was built for.

- **The two lists now have to agree.** A test connects a client to the server over an in-memory
  transport, asks it what it exposes the way a host does, and fails naming any defined tool that is
  missing. `LearningPath`, `NextConcepts`, `MissingPrerequisites` and `UpdateGraphFromText` are not
  caught by it — they are not in `Definitions()` at all, and remain CLI-only.

## [2.62.0] - 2026-07-31

### Added

- **`find_nodes` — enter the property graph by name.** `expand_graph` and `get_nodes` both take node
  IDs and nothing else, and nothing turned a name into one. A caller holding a name had to *derive*
  the ID its writer would have produced: re-implement someone else's hashing, guess the entity type,
  and hope. A wrong derivation returned an empty subgraph — the same answer the graph gives for
  something it has genuinely never heard of — so the failure was silent and read as missing data.

  Found from outside: a tutoring application built a study-path planner on these edges and got
  nothing on every query against a real database, because 957 of its 1017 prerequisite edges hung
  off IDs whose scheme it had guessed wrong. Deriving an ID also needs every input the writer used —
  for that application, which textbook a concept came from — and 953 of its 1030 sessions do not have
  one, so 92% of the time there was nothing to derive from and the walk had no seeds at all. A name
  needs none of that.

  It matches strings, not meanings. Measured against that database: `two phase commit` reaches
  `Two-Phase Commit`, 泰勒级数 reaches 泰勒级数 — but 左极限 does not reach "Left-Hand Limit", and is
  not meant to. Translation is a different problem and this is not a step towards it. An earlier
  draft of this entry used that example as though it worked; it does not.

  Three passes, weakest last — exact, case-and-punctuation folded, containment — and every match
  says which it was, because a caller acting on a containment hit should be able to decline. Shorter
  names win among equals: extra words narrow a concept, so a bare "Limit" wants `Limit` and not
  `Infinite Limit`. `node_types` keeps places in a book ("Chapter 4") out of an answer about things
  to learn.

  The fold keeps letters and digits by Unicode class rather than by ASCII. Without that every CJK
  name folds to `""` and they all match each other — proven by breaking it deliberately, which
  resolved 量子色动力学, absent from the graph, confidently onto an unrelated Chinese concept.

### Fixed

- **`LearningPath` no longer reports a concept as cyclic for being stuck behind a cycle.** `Cycles`
  was read off the topological sort: whatever Kahn's algorithm was still holding when it stalled got
  named. That set is "has an unmet prerequisite" — the cycle *plus* everything queued behind it —
  and the two coincide only when the whole graph is one cycle, which is what the test built. On two
  disjoint cycles under a target requiring both, seven concepts produced eleven entries: the second
  cycle twice, and the target itself, which nothing requires and so cannot lie on any cycle. Now
  computed with Tarjan before the sort runs, so each concept appears once and only if it is
  genuinely on a cycle. The stall still breaks the deadlock the same deterministic way; it just no
  longer draws conclusions from being stuck.

## [2.61.0] - 2026-07-31

Not logged when released. Recorded here from the release commit.

### Added

- **Learning prerequisite graphs (`pkg/graphflow/learning.go`).** Study material as concepts linked
  by prerequisite edges. `LearningPath` returns the prerequisite closure of a target, topologically
  sorted, minus what is mastered — the question retrieval cannot answer: what must I learn, and in
  what order, before X. `NextConcepts` gives the learnable frontier, `MissingPrerequisites` explains
  being stuck, `MarkMastered` persists mastery on the graph.
- **LLM-driven graph CRUD (`pkg/graphflow/graph_edit.go`).** Ingestion only ever added, so wrong
  facts accumulated with no way to retract them. `UpdateGraphFromText` shows an LLM the relevant
  existing subgraph alongside new text and applies the resulting add/update/delete edits.
  Deliberately embedder-free — relevant entities are found by lexical mention, writes use lexical
  vectors — so the whole surface works with only a chat model. Deletes are opt-in (`AllowDelete`),
  capped per run, and previewable with `DryRun`.

  Reachable from `cortexdb-mcp --graph-update` only; it is not registered as an MCP tool, so a host
  that speaks MCP rather than the CLI cannot use it.

## [2.60.3] - 2026-07-28

### Fixed

- **A lexical-only search whose one arm cannot run now fails instead of returning nothing.**
  `HybridSearch` built the keyword query, handed it to SQLite and ignored the error (`if err == nil`).
  With no vector supplied that arm is the *only* arm, so a broken or half-migrated FTS index produced an
  empty result set and a nil error — indistinguishable from a corpus that genuinely contains nothing, and
  unfalsifiable from outside because every caller sees success. It now returns the error when it is the
  only arm, and degrades with a log line when a vector arm is still answering (vector-only results look
  exactly like ordinary ones, so the difference has to be said out loud).
- **Rows that will not scan are logged instead of silently dropped.** Three `rows.Scan` sites — two in
  `HybridSearch`, one in `ftsSearch` — did `continue` on error. One column that will not scan (a NULL
  metadata, a NULL collection_id) silently cost every row it affected, which is how a search that matched
  plenty answers "nothing found".
- **`HNSWHybridSearch` no longer truncates an uncapped query to nothing.** `TopK` is optional, and its
  sibling `HybridSearch` treats zero as "no cap" — but the HNSW path truncated unconditionally, so the
  same query returned every match without an index and an empty slice with one. An uncapped query now
  goes down the exhaustive path, because an HNSW search takes a candidate count and cannot answer
  "everything".

### Known issue

- **`SimpleHNSW` recall is not deterministic and can be poor.** `Add` links each node to the candidates
  from a *single-candidate* search of the entry point, and with `randomLevel()` deciding the layout, 2
  runs in 15 of a four-node fixture returned 1 of 4 matches. Real HNSW selects `M` neighbours from an
  `efConstruction` candidate set; fixing this is a rewrite of the index's construction rather than a
  guard, so it is recorded rather than patched. Searches that need reliable recall should leave the HNSW
  index off, where the exhaustive path is exact.

## [2.60.2] - 2026-07-28

### Fixed

- **`search_text` returned nothing when `top_k` was omitted.** `top_k` is optional in the tool
  schema, so a model calling the tool routinely leaves it out — and the zero was carried all the way
  into the final `ordered[:TopK]` truncation, cutting the entire result set away. The tool answered
  `chunks: null` with a plan and a decision that both looked correct, so it read as "this corpus does
  not contain the term" while the FTS index held dozens of matches; the same query with `top_k: 5`
  returned five. `SearchTextOnly`'s existing default did not help because it normalises its own copy
  of the options, not the value the caller's merge truncates by. Normalised once at the top of the
  candidate merge, against a new package-level `defaultSearchTopK`. Every existing search test passed
  `TopK` explicitly, which is why none of them caught it.

## [2.60.1] - 2026-07-27

### Fixed

- **`plan.collection` is accepted as shorthand for `plan.filters.collection`.** `collection` is a
  top-level parameter of the same call and also lives inside `plan.filters`, so a model reaches for
  `plan.collection` — and with `additionalProperties: false` that was a hard schema rejection
  (`unexpected additional properties ["collection"]`), costing a whole model round trip on every
  scoped search before it guessed the longer spelling. It is now folded into the filters before
  anything downstream sees the plan. Precedence is unchanged and explicit: the request's own
  `collection` outranks `plan.filters.collection`, which outranks the shorthand.

## [2.60.0] - 2026-07-26

### Fixed

- **A named collection no longer leaks other collections' chunks.** Hybrid search runs a vector
  arm and a keyword arm; only the vector arm applied `opts.Collection`. The keyword arm queried
  the shared FTS index unrestricted, so asking one collection returned rows from every other one
  — and with no query vector supplied (`SearchTextOnly`, and therefore `search_text` and every
  lexical-mode `knowledge_search`) the keyword arm is the *only* arm, so the collection filter
  had no effect at all. A tutor scoped to one textbook could be handed another book's text, or
  another user's, as if it came from the book asked for.
- **An unscoped search no longer means "only the default collection".** Query defaults
  substituted `graphrag_chunks` whenever a caller named no collection, silently narrowing a
  search that asked for no narrowing; anything ingested elsewhere — imported agent memory, a
  per-book collection — was unreachable unless the caller already knew where to look. Empty now
  means unrestricted, as it does on the vector arm. Ingest still defaults, since content has to
  land somewhere. This was masked by the leak above: the unrestricted keyword arm happened to
  return the rows the filter should have excluded.
- **Two-character Chinese terms are findable again.** The trigram tokenizer cannot produce a
  token shorter than three characters, so `MATCH` returned nothing for 乘法, 分数, 除法, 周长 and
  most other Chinese words a lesson is actually about, however many chunks contained them. Such
  queries now fall back to a substring scan (collection filter and all) instead of reporting an
  empty corpus.

## [2.59.4] - 2026-07-25

### Fixed

- **`knowledge_save` no longer turns romanisation into graph entities.** 2.59.3 guarded the
  `graphflow` heuristic extractor, but `knowledge_save` uses the GraphRAG Title Case
  extractor, which was untouched — so a Chinese textbook still produced entities like
  `entity:wng` and `entity:gu_dr` and no concepts at all. Worse, a scanned book's text
  layer mangles pinyin tone marks into stray capitals (`hSn dAi`, `pT Wng`, `Gu Dr`), which
  are valid Title Case, so tone marks cannot be tested for. When the text contains CJK,
  Latin matches must now be at least three letters per word and contain a vowel: syllable
  debris is dropped while genuine bilingual entities (`Transformer`, `Chain Rule`) and
  Latin-only text are untouched.

## [2.59.3] - 2026-07-25

### Fixed

- **The heuristic extractor no longer fills CJK graphs with romanisation.** Entities were
  found with a capitalised-Latin-word pattern, which on Chinese, Japanese or Korean text
  can only match romanisation — a primary-school textbook prints pinyin above every new
  character, so its graph ended up holding syllable fragments (`entity:wng`, `entity:qn`)
  and not one real concept. The Latin pass is now skipped for predominantly CJK documents
  (leaving extraction to an LLM extractor) and tone-marked syllables are rejected
  everywhere. Explicit backtick spans and Latin-text extraction are unchanged.

## [2.59.2] - 2026-07-25

### Fixed

- **`vector_dimension_repair` reconciles metadata even when there is nothing to
  re-embed.** An early return skipped the reconciliation whenever the candidate list was
  empty, which is exactly the state a previous repair leaves behind: correct vectors,
  stale declared dimension. The drift report then kept flagging rows that were already
  fixed.

## [2.59.1] - 2026-07-25

### Fixed

- **Re-embedding now reconciles the collection's declared dimension.** A collection
  records the size it was created with, so after `vector_dimension_repair` rewrote the
  vectors the drift report kept flagging every repaired row — the numbers were right and
  the metadata was stale. `ReconcileCollectionDimensions` updates a collection once all
  of its vectors agree on one size, and the repair reports how many it reconciled.

## [2.59.0] - 2026-07-25

### Added

- **Vector-dimension drift is now visible and repairable.** A store outlives the
  embedding model that filled it: change models and the old rows keep their old
  dimensionality. Those vectors cannot enter the vector index — one graph holds one
  dimensionality — so they quietly stop being retrievable by similarity while lexical
  search still finds them, which hides the loss. Nothing reported it.

  `SQLiteStore.DimensionReport` groups stored vectors by collection and size and counts
  the rows that disagree with their collection. `DB.ReembedMismatchedVectors` repairs
  them by embedding the stored text again with the configured embedder, in batches, with
  a dry run. The numbers cannot be salvaged by truncating or padding — vectors from two
  models occupy unrelated spaces — so re-embedding is the only honest repair.

  Both are exposed as the `vector_dimension_repair` MCP tool, which defaults to
  `dry_run: true` so an operator can look before writing.

## [2.58.1] - 2026-07-25

### Fixed

- **A store holding more than one vector dimensionality no longer crashes the process.**
  `CosineDistance` iterated one vector while indexing the other, so comparing a 4096-dim
  vector with a 768-dim one panicked with `index out of range [768] with length 768`.
  The panic surfaced inside `HNSW.Insert` during `knowledge_save`, killing the server
  mid-write — a database that had outlived an embedding-model change could not be written
  to at all. All three distance functions now treat mismatched or empty vectors as
  incomparable (maximum distance) instead of indexing out of bounds, and `HNSW.Insert`
  rejects a vector whose dimensionality differs from the graph's with a clear error.
  Callers already log and skip, so mixed-dimension stores stay writable. (IVF and LSH
  already checked dimensions; HNSW was the outlier.)

## [2.58.0] - 2026-07-25

### Fixed

- **Lexical search now works for Chinese, Japanese and Korean text.** FTS5's default
  `unicode61` tokenizer does not segment CJK: a whole run of Han characters becomes a
  single token, so no realistic Chinese query could ever match. Lexical search silently
  returned zero rows for CJK corpora while English worked fine, which also made hybrid
  search fall back to vectors alone and left `memory_search` unable to find Chinese
  memories. Each FTS index now has a `_cjk` companion built with the `trigram`
  tokenizer, and `knowledge_search`, `search_text`, hybrid search, message keyword
  search and memory recall route to it when the query contains CJK.

  The word indexes keep the `unicode61` tokenizer. Replacing it outright was the obvious
  one-line fix but it degrades English relevance — trigram is a substring index, not a
  word index — and it reordered `TestHybridSearch`. Routing by script fixes CJK without
  touching existing behaviour for space-separated languages.

  Existing databases are upgraded on open: the companion indexes are created and built
  once over the current corpus, recorded via `PRAGMA user_version`.

  Known limit: the trigram tokenizer matches substrings of three characters or more, so
  a query of only one or two CJK characters (e.g. `函数`) still matches nothing and has
  to rely on vector search.

### Added

- `CORTEXDB_LEXICAL_DIM` to set the lexical placeholder vector dimension when no
  collection dimension is configured.
- Adaptive embedding batches in `cortexdb-mcp-stdio`: `CORTEXDB_EMBED_BATCH_SIZE` and
  `CORTEXDB_EMBED_TIMEOUT_SECONDS` are honoured, oversized batches are split, and chunk
  character limits are enforced before embedding.

## [2.51.0] - 2026-07-09

### 🚀 Features

- *(graphflow)* LLM-backed graph distillation for organize (v2.51.0)

## [2.50.0] - 2026-07-09

### 🚀 Features

- *(mcp-stdio)* Env-driven embedder for semantic retrieval (v2.50.0)

## [client-v0.1.1] - 2026-07-09

### ⚙️ Miscellaneous Tasks

- Fix crates.io version check (User-Agent) + tolerate already-published in publish-clients
- *(clients)* Bump Rust/Python/Node clients to 0.1.1

## [client-v0.1.0] - 2026-07-09

### ⚙️ Miscellaneous Tasks

- Publish-clients workflow — auto-publish Rust/Python/Node on client-v* tags

## [2.49.0] - 2026-07-09

### 🚀 Features

- *(tools)* Extract_conversation tool — summary/entities/relations from a chat (v2.49.0)

## [2.48.0] - 2026-07-09

### 🚀 Features

- *(graph)* Degree-ranked node selection for the graph view (v2.48.0)

## [2.47.0] - 2026-07-09

### 🚀 Features

- *(import)* Import Claude Code / Codex memory into the brain + build graph (v2.47.0)

## [2.46.0] - 2026-07-09

### 🚀 Features

- *(plugin)* Sharpen SessionStart memory directive — recall/graph/save (v2.46.0)

## [2.45.0] - 2026-07-05

### 🚀 Features

- *(retrieval)* Hybrid RRF knowledge search, sentence-aware chunking, harder eval (v2.45.0)

## [2.44.0] - 2026-07-04

### 🐛 Bug Fixes

- *(retrieval)* Auto mode uses the embedder for semantic knowledge search (v2.44.0)

## [2.43.0] - 2026-07-04

### 🚀 Features

- *(quality)* Retrieval-eval suite + fuzz/property tests; fix FTS NUL crash (v2.43.0)

## [2.42.0] - 2026-06-30

### 🐛 Bug Fixes

- *(graph)* Entity-quality filter + render the real graph (v2.42.0)

## [2.41.0] - 2026-06-30

### 🚀 Features

- *(graph)* Organize memories into the graph; /cortexdb-graph extracts then shows (v2.41.0)

## [2.40.0] - 2026-06-30

### 🐛 Bug Fixes

- *(plugin)* Install is pure prebuilt-binary download, no Go ever (v2.40.0)

## [2.39.0] - 2026-06-30

### 🚀 Features

- *(recall)* Surface graph facts in knowledge_memory_recall (v2.39.0)

### 📚 Documentation

- Clarify Codex plugin install
- Sharpen CortexDB positioning

## [2.38.0] - 2026-06-28

### 🚀 Features

- *(plugin)* Proactive memory directive, /remember /recall /cortexdb-graph, graph viewer (v2.38.0)

## [2.37.0] - 2026-06-28

### 🚀 Features

- Public generic Rerank API (cross-encoder + MMR)

### 🐛 Bug Fixes

- *(ci)* Sync plugin manifests to 2.37.0 (version gate)

### 📚 Documentation

- Add a Claude Code plugin install + usage section to the READMEs

## [2.36.0] - 2026-06-25

### 🚀 Features

- *(plugin)* Default to a single global ~/.cortexdb/cortexdb.db (v2.36.0)

## [2.35.0] - 2026-06-25

### 🐛 Bug Fixes

- *(plugin)* Version-pin the MCP binary cache so upgrades auto-refresh (v2.35.0)

## [2.33.1] - 2026-06-25

### 🐛 Bug Fixes

- *(search)* Escape FTS5 syntax in lexical query text (v2.33.1)

## [2.33.0] - 2026-06-25

### 🚀 Features

- *(search)* Retrieval-layer Authorize gate, Reranker + MinScore, options-based hybrid search

### 📚 Documentation

- Split Claude Code plugin install into separate slash commands

## [2.32.0] - 2026-06-24

### 🚀 Features

- Default DB path to .cortexdb/, auto-create parent dirs (v2.32.0)

## [2.31.0] - 2026-06-24

### 🚀 Features

- Auto-recall UserPromptSubmit hook for the plugin (v2.31.0)

## [2.30.1] - 2026-06-24

### 🧪 Testing

- *(knowledge)* Regression for no-embedder lexical save+search (v2.30.1)

## [2.30.0] - 2026-06-24

### 🚀 Features

- *(plugin)* Ship prebuilt MCP binaries, drop Go-toolchain requirement (v2.30.0)

## [2.29.0] - 2026-06-24

### 🚀 Features

- *(plugin)* Package CortexDB as a Claude Code & Codex plugin (v2.29.0)

## [2.28.0] - 2026-06-23

### 🚀 Features

- *(graphflow)* Add 3d html export view

### ⚙️ Miscellaneous Tasks

- *(release)* V2.28.0

## [2.27.0] - 2026-06-23

### 🚀 Features

- Add composable cortex query api

### 📚 Documentation

- *(spec)* Profileflow — LLM-maintained narrative user profile

### ⚙️ Miscellaneous Tasks

- *(release)* V2.27.0

## [2.26.0] - 2026-06-20

### 🚀 Features

- Export EntityNodeID, add GraphStore.MergeEntities, ALTER TABLE FKs

## [2.25.0] - 2026-06-17

### 🚀 Features

- *(importflow)* ParseDDL — parse CREATE TABLE columns, primary keys, foreign keys
- *(importflow)* MappingFromDDL — deterministic DDL -> MappingPlan (PK->id, FK->relations)
- *(importflow)* Importflow_ddl_plan tool — DDL -> reviewable MappingPlan over MCP
- *(importflow)* MappingFromDDLWithLLM — LLM refines deterministic DDL→KG baseline with graceful fallback
- *(importflow)* Importflow_ddl_plan_ai MCP tool — LLM-refined DDL→KG plan with baseline alongside

### 🐛 Bug Fixes

- *(importflow)* Guarantee LLM-refined DDL plans are executable
- *(importflow)* Text-extraction triples use IRI subjects, not literals

### 📚 Documentation

- *(spec)* DDL -> knowledge-graph MappingPlan (deterministic) + importflow_ddl_plan MCP tool
- *(plan)* DDL -> knowledge-graph MappingPlan implementation plan
- Importflow DDL -> knowledge-graph mapping (MappingFromDDL + importflow_ddl_plan)
- *(spec)* LLM-enhanced DDL→KG mapping (refine-the-baseline + importflow_ddl_plan_ai)
- *(plan)* LLM-enhanced DDL→KG mapping implementation plan
- *(importflow)* Clarify executability guard scope and text-IRI case-folding

### 🎨 Styling

- *(importflow)* Gofmt toolbox.go schema helpers

### 🧪 Testing

- *(importflow)* Cover CONSTRAINT-form PRIMARY KEY / FOREIGN KEY in ParseDDL
- *(importflow)* E2e DDL -> MappingPlan -> knowledge graph (FK becomes a relation edge)
- *(importflow)* Skipped live DDL→KG LLM-refine test against OPENAI_*/.env model

### ⚙️ Miscellaneous Tasks

- *(release)* V2.25.0 — LLM-enhanced DDL→KG mapping (importflow_ddl_plan_ai)

## [2.24.1] - 2026-06-16

### 🚀 Features

- *(examples)* Semantic RAG — pluggable embedding model (DashScope text-embedding-v4) for vector search by meaning + LLM answer

### 🐛 Bug Fixes

- *(examples)* 05_graphflow falls back to heuristic extractor if the LLM endpoint is unreachable; README marks per-example prereqs (LLM/DB)
- *(examples)* 08 + 12 load .env and use OPENAI_* config (08 no longer requires local Ollama; 12 honors OPENAI_MODEL)

### 📚 Documentation

- Surface data connector + CDC and examples 09-13 in README/README_CN and landing page

### ⚙️ Miscellaneous Tasks

- *(release)* V2.24.1 — examples + docs (semantic-RAG example, .env/OPENAI_* config, connector/CDC in README + landing)

## [2.24.0] - 2026-06-16

### 🚀 Features

- *(connector)* ChangeEvent.Position + position-based checkpoint wiring (Route B)
- *(connector)* Postgres logical-replication change source (pgoutput, Route B)
- *(connector)* MySQL binlog change source (ROW, Route B)
- *(examples)* Support-agent brain — end-to-end live Postgres/MySQL → desensitize → RAG+KG → masked Q&A → un-mask → CDC sync
- *(examples)* Optional LLM answer step for support-brain (answers over masked context only)
- *(examples)* Unified brain — dual-source PG+MySQL, concurrent streaming CDC, free-text PII, RDFS+SPARQL+SHACL reasoning; quiet binlog logger
- *(examples)* Incident-analysis agent (LLM-required) — LLM KG extraction + agentic tool use over CortexDB
- *(examples)* Scale + analytics — bulk dual-source ingest with throughput, SPARQL/RDFS/SHACL analytics, CDC under load

### 🐛 Bug Fixes

- *(connector)* ValueToString renders time.Time as RFC3339 so polling watermark round-trips (Postgres timestamptz)
- *(core)* Apply SQLite pragmas via modernc _pragma() DSN syntax

### 📚 Documentation

- *(plan)* Connector CDC phase 2 (Postgres logical replication) implementation plan
- *(connector)* Document Postgres logical-replication CDC (Route B-PG)
- *(plan)* Connector CDC phase 3 (MySQL binlog) implementation plan
- *(connector)* MySQL binlog CDC (Route B-MySQL); CDC complete for PG + MySQL

### 🧪 Testing

- *(connector)* Live PG logical-replication CDC e2e (insert/update/delete, no PII)
- *(connector)* Live MySQL binlog CDC e2e (insert/update/delete, no PII)

### ⚙️ Miscellaneous Tasks

- Gitignore SQLite WAL/SHM sidecars (test DBs not ending in .db)
- *(release)* V2.24.0 — CDC complete (PG logical replication + MySQL binlog), 4 end-to-end examples, SQLite concurrency fix

## [2.23.0] - 2026-06-16

### 🚀 Features

- *(connector)* MCP server + tools (NewMCPServer/RunMCPStdio/RegisterMCPTools), cortexdb-connector-mcp cmd; document connector in skills/README
- *(connector)* CDC change model (ChangeOp/ChangeEvent/ChangeSource)
- *(connector)* Knowledge-DB-backed CDC checkpoint store
- *(connector)* CDC apply helpers (one-row source, delete-key resolver)
- *(connector)* CDC watcher apply loop (idempotent upsert + hard delete, key precondition)
- *(connector)* Polling change source (Route A) + incremental sqlSource reader
- *(connector)* CDC checkpoint seed/save for polling watcher (resume)
- *(connector)* Compile-time assert polling source satisfies cursorCheckpointer

### 🚜 Refactor

- *(connector)* Watcher uses maps.Copy + deterministic column order (review nits)

### 📚 Documentation

- *(spec)* Connector CDC / near-real-time sync (polling + PG logical replication + MySQL binlog)
- *(plan)* Connector CDC phase 1 (polling) implementation plan
- *(connector)* Near-real-time CDC sync (polling) in README/SKILL

### 🧪 Testing

- *(connector)* E2e desensitized import into knowledge graph (tokenized entity IRIs, edges preserved, no PII)
- *(connector)* Live polling CDC e2e (upsert, no PII, resume) for Postgres + MySQL

### ⚙️ Miscellaneous Tasks

- *(deploy)* Docker demo for OpenClaw + Hermes agents using CortexDB; add .dockerignore
- *(release)* V2.23.0 — connector near-real-time CDC sync (polling) + MCP server/tools

## [2.22.0] - 2026-06-16

### 🚀 Features

- *(clients)* Python (cortexdb-client) + Node (@cortexdb/client) gRPC clients
- *(skills)* Agentskills.io skills for Hermes (Python) + OpenClaw (Node) memory
- *(connector)* Core privacy types (PiiKind/Sensitivity/MaskAction/MaskingPlan)
- *(connector)* Masking primitives (mask/generalize/redact)
- *(connector)* Rule-based PII classifier (name hints + value regex)
- *(connector)* AES-GCM token vault with per-tenant key + deterministic tokens
- *(connector)* Env + file key providers
- *(connector)* Regex free-text PII scanner (in-place redaction)
- *(connector)* Desensitizer + Source decorator (mask/drop/pseudonymize/text-scan)
- *(connector)* BuildMaskingPlan (default-deny) + Unmask
- *(connector)* LLM classifier + rule->LLM chain
- *(connector)* Live Postgres source via information_schema
- *(connector)* Live MySQL source via information_schema
- *(connector)* Agent toolbox (introspect/plan/run/unmask) for importflow MCP

### 🐛 Bug Fixes

- *(connector)* Vault enforces 32-byte AES-256 key (no silent downgrade)
- *(connector)* Desensitizer fails closed on unlisted columns (default-deny gate)

### 🚜 Refactor

- *(connector)* Escape identifier quotes; drop unused readRows cols param

### 📚 Documentation

- *(landing)* Add self-hosted GoatCounter analytics (host-prefixed paths)
- Document Python (PyPI) + Node (npm) clients — all three published as cortexdb-client
- *(landing+readme)* Agent Skills section — install CortexDB memory for Hermes/OpenClaw from ClawHub/skills.sh/git
- *(landing)* Add Go/Rust/Python/Node client section; remove misplaced Rust 'example' card
- *(spec)* Data connector with privacy/desensitization — pkg/connector design
- *(spec)* Connector rides importflow MCP; add v1 token-vault key custody for un-mask
- *(plan)* Data connector with privacy/desensitization implementation plan
- *(connector)* Example 09 + README/SKILL data-connector section

### 🧪 Testing

- *(connector)* End-to-end desensitized import into ImportFlow RAG (no PII leak)

### ⚙️ Miscellaneous Tasks

- *(release)* V2.22.0 — data connector (privacy/desensitization), Python/Node clients, agent skills

## [2.21.0] - 2026-06-07

### 🚀 Features

- *(rpc)* Proto contract cortexdb.v1 + generated Go stubs
- *(rpcserver)* Scaffolding, error mapping, bearer-token auth interceptor
- *(rpcserver)* Bufconn harness, conversion helpers, AdminService
- *(rpcserver)* KnowledgeService
- *(rpcserver)* MemoryService
- *(rpcserver)* GraphRagService
- *(rpcserver)* KnowledgeGraphService (RDF/SPARQL/SHACL/inference/ontology)
- *(rpcserver)* ToolsService generic dispatch
- *(cmd)* Cortexdb-grpc sidecar binary with token auth and optional OpenAI-compatible embedder
- *(rust)* Cortexdb-client crate with typed sub-clients and bearer-token interceptor
- *(rust)* Managed-server feature — sidecar resolve/download/spawn with auto token
- *(rust)* E2e_ollama example — qwen3.5 extraction + embeddinggemma vector search + graph expand
- *(examples)* 08_self_knowledge_graph — build a KG of CortexDB itself via graphflow (ollama or OpenAI-compatible extractor)

### 🐛 Bug Fixes

- *(graphflow)* Decode base64 graph payload as UTF-8 in HTML viz (atob mojibake for non-ASCII labels)

### 📚 Documentation

- *(spec)* GRPC sidecar server + Rust client design
- *(spec)* Add bearer-token auth to gRPC sidecar design
- *(spec)* Note single-node v1 and multi-node evolution path
- *(spec)* Document multi-tenancy model — single DB per process in v1, in-file scoping
- *(plan)* GRPC sidecar + Rust client implementation plan
- GRPC sidecar + Rust client usage (README/README_CN/SKILL)
- *(landing)* Self knowledge-graph demo section — screenshots + graph-backed Q&A
- *(landing)* Refresh for v2.21 — gRPC/Rust feature cards, honest stats, FAQ + JSON-LD/OG/llms.txt SEO-GEO, fix ScrollToPlugin
- *(landing)* Tighten hero subtitle
- *(landing)* Fix footer dead links — enable Discussions, drop missing CONTRIBUTING
- *(pages)* Add .nojekyll — Go braces in plan docs break Liquid parsing
- Sync README/README_CN/SKILL/examples — example 08, crates.io install, importflow entries

### 🧪 Testing

- *(rust)* Sidecar integration test + roundtrip example (verified against ollama embeddinggemma)
- *(examples)* Knowledge-graph QA test over self_kg.db (skips when graph not generated)

### ⚙️ Miscellaneous Tasks

- Rust client job + multi-platform sidecar release assets
- *(release)* V2.21.0 — gRPC sidecar + Rust client (cortexdb-client on crates.io)

## [2.20.3] - 2026-05-31

### 🐛 Bug Fixes

- *(graphrag)* Sliding word-window chunker (merge short lines) instead of per-paragraph splitting

## [2.20.2] - 2026-05-31

### ⚙️ Miscellaneous Tasks

- Bump version to 2.20.2

## [2.20.1] - 2026-05-30

### 🚀 Features

- *(importflow)* Add MCP server (importflow_plan/run) and document in SKILL

### 📚 Documentation

- *(site)* Add ImportFlow to landing page (feature, architecture, example)

### ⚙️ Miscellaneous Tasks

- *(release)* V2.20.1 — importflow MCP server + skill docs

## [2.20.0] - 2026-05-30

### 🚀 Features

- *(importflow)* Add package skeleton and core types
- *(importflow)* Add Source interface and in-memory CSVSource
- *(importflow)* Parse INSERT statements from SQL dumps
- *(importflow)* Parse PG COPY blocks and report unparsed statements
- *(importflow)* Add MappingPlan types and template renderer
- *(importflow)* Add deterministic Mapper for RAG chunks and KG triples
- *(importflow)* Add batching RAGSink over InsertTextBatch
- *(importflow)* Add batching KGSink over UpsertKnowledgeGraph
- *(importflow)* Add MappingInferer and TextRefiner with JSONGenerator defaults
- *(importflow)* Add Importer orchestration (Plan/Run/AutoImport)
- *(importflow)* Add agent-callable Toolbox (importflow_plan/run)
- *(importflow)* Add e2e test, runnable example, and docs

### 🐛 Bug Fixes

- *(importflow)* Parse CREATE TABLE before COPY blocks and make dump escaping dialect-aware

### 📚 Documentation

- Add importflow external-data import design spec
- Add importflow implementation plan

### ⚙️ Miscellaneous Tasks

- *(release)* V2.20.0 — add importflow external-data import layer

## [2.19.1] - 2026-05-30

### 🐛 Bug Fixes

- Omit empty "required" from tool JSON schemas; add KG e2e coverage

## [2.19.0] - 2026-04-28

### 🚀 Features

- Add pkg/agentmem SQL-backed agent memory layer

## [2.18.0] - 2026-04-12

### 🚀 Features

- Expand knowledge graph workflows
- Align knowledge memory architecture

### 📚 Documentation

- Align examples with architecture

## [2.17.0] - 2026-04-08

### 🚀 Features

- Add memoryflow workflow layer

## [2.16.0] - 2026-04-01

### 🚀 Features

- Add embedded RDF knowledge graph and RDFS-lite inference

## [2.15.1] - 2026-03-29

### 🚀 Features

- *(brain)* Add unified knowledge memory and MCP tools

## [2.14.0] - 2026-03-29

### 📚 Documentation

- Add SKILL.md for Claude Code integration

## [2.13.0] - 2026-03-20

### 🚀 Features

- Fallback to FTS5/BM25 when embedder not configured
- LLM-assisted retrieval and external vector support

### 📚 Documentation

- Unify CortexDB branding and update API references

## [2.9.1] - 2026-03-06

### 🚀 Features

- Add Content field to Document for full document storage

## [2.9.0] - 2026-03-06

### 🐛 Bug Fixes

- *(hindsight)* Only apply RRF fusion when multiple strategies enabled

## [2.8.0] - 2026-03-05

### ⚡ Performance

- Optimize GraphRAG startup speed with batch insert, parallel build, quantization, and auto-save

## [2.7.0] - 2026-03-05

### 🚀 Features

- Add Embedder interface for dual-mode text/vector operations
- Add Info() interface to get database configuration

### 🐛 Bug Fixes

- Complete HybridSearch implementation and update CI for v2.7.0

### 📚 Documentation

- Improve Go code style in READMEs
- *(examples)* Add example for new high-level text APIs
- *(examples)* Add structured data examples (Textification, Memory Entities, GraphRAG)
- Add new text and structured data APIs to READMEs
- Rewrite README for CortexDB with new positioning and features

### ⚙️ Miscellaneous Tasks

- Rename project from sqvect to cortexdb

## [2.6.1] - 2026-02-20

### 🚀 Features

- Add UpdateDocument method for metadata updates

### 🐛 Bug Fixes

- Eliminate data race in auto-retain goroutine vs Close()

## [2.6.0] - 2026-02-19

### 🚀 Features

- Auto-retain via sys.AddMessage with sliding-window async extraction

### 📚 Documentation

- Fix README.md duplicate content and update both READMEs for v2.5.0

### 🎨 Styling

- Format alignment fixes in hindsight.go, sqvect.go and README table

## [2.5.0] - 2026-02-19

### 🚜 Refactor

- Merge pkg/memory into pkg/hindsight, remove redundant package

## [2.4.0] - 2026-02-19

### 🚀 Features

- Add pkg/memory with Hindsight-style retain/recall/reflect and extensibility hooks

## [2.3.2] - 2026-02-05

### 🐛 Bug Fixes

- Properly persist and load banks in CreateBank

## [2.3.1] - 2026-02-04

### 📚 Documentation

- Update documentation for v2.3.1

## [2.3.0] - 2026-02-04

### 🚀 Features

- Add semantic-router module with hybrid and function calling support

## [2.2.0] - 2026-02-03

### 🐛 Bug Fixes

- Correct module path in CI workflow
- Update all paths to use /v2 module path
- Remove /v2 from CI/CD badge URL
- Remove /v2 from GitHub release badge URL

### 🚜 Refactor

- Optimize graph algorithms and clarify performance claims

### ⚙️ Miscellaneous Tasks

- Add automatic release creation on tag push
- Update codecov to v5 with CODECOV_TOKEN
- Simplify workflows and fix release generation

## [2.1.0] - 2026-02-03

### 🚀 Features

- Add Hindsight AI agent memory system (v2.1.0)

### 📚 Documentation

- Add Chinese README (README_CN.md)

## [2.0.1] - 2026-01-15

### 🚀 Features

- Upgrade to v2 module path

## [2.0.0] - 2026-01-14

### 🚜 Refactor

- Complete project overhaul for v2.0.0 (file splitting, UUID, observability, and passing tests)

### 📚 Documentation

- Comprehensive update of Go documentation comments for v2.0.0

## [1.4.0] - 2026-01-14

### 📚 Documentation

- Update examples and README with RAG features

## [1.3.0] - 2026-01-14

### 🚀 Features

- Add RAG capabilities (documents, chat, hybrid search, ACL) and update docs/examples

## [1.2.0] - 2026-01-13

### 🚀 Features

- Add advanced vector search features and fix range search
- Add advanced vector search features and LLM integration
- Add IVF index support, benchmarking tools, and updated docs
- Integrate SQ8 quantization and optimize WAL/dimension policies

### 🐛 Bug Fixes

- Remove docs deployment from release workflow
- Resolve all golangci-lint issues

## [1.0.0] - 2025-09-09

### 🚀 Features

- Enhance document retrieval and listing capabilities
- Implement HNSW (Hierarchical Navigable Small World) index
- Implement core functionality improvements
- Replace CGO SQLite with pure Go implementation
- Implement core functionality improvements
- Add similarity scoring improvements
- Implement core functionality improvements

### 🐛 Bug Fixes

- Resolve search filtering errors with metadata queries
- Resolve all golangci-lint issues and add GitHub Pages deployment

### 🚜 Refactor

- Restructure codebase with modular architecture and advanced features

### ⚡ Performance

- Optimize pinyin processing performance
- Optimize pinyin processing performance

### 🧪 Testing

- Add comprehensive test coverage for document listing

## [0.2.0] - 2025-08-07

### 🚀 Features

- List all docs

## [0.1.0] - 2025-08-07

### 💼 Other

- V0.1.0

<!-- generated by git-cliff -->
