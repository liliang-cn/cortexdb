# Changelog

All notable changes to this project will be documented in this file.

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
