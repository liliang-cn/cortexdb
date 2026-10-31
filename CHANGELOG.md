# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- `find_nodes` takes `skip_chunks`: nodes whose id begins `chunk:`, the
  chunks ingest writes, are left out of the match and their text is not
  decoded. Document nodes are still matched, and an untitled document holds
  its text, so the saving is largest on a store whose documents have
  titles. Off by default; an older server ignores the field. Lauro
  Foletti.
- `cortexdb-grpc` reads `CORTEXDB_VECTOR_INDEX` (`hnsw`, the default, or
  `binary` on a SQLite store) and `CORTEXDB_BINARY_OVERSAMPLE`, and logs the
  index it opens with. The binary index holds 1-bit codes in memory and
  keeps no snapshot: a store of 40,000 384-d vectors opens in 42 MB instead
  of 300. Lauro Foletti.

### Fixed

- A store whose HNSW snapshot was written quantized is rebuilt from its table
  when it is reopened without quantization or with another kind or number of
  bits, instead of loading codes nothing can read. A store closed under
  `EmbeddedConfig` (SQ8) panicked when reopened with `DefaultConfig`, as every
  binary in `cmd/` does, and so did an IVF store with a quantizer left by an
  earlier HNSW index; one moved between SQ8, SQ4 and binary quantization, or
  from binary to none, opened and then found almost nothing. The quantizer is
  now written or dropped in the same transaction as the snapshot, so the
  rebuild is paid once. A store already closed under the wrong
  configuration by an earlier version holds codes beside a quantizer that
  matches them and is not repaired: delete its HNSW snapshot rows
  (`DELETE FROM index_snapshots WHERE type LIKE 'HNSW%' OR type =
  'QUANTIZER'`) and it is rebuilt on the next open. `ImportIndex` still loads
  a graph as it is given. Reported by Lauro Foletti.
- A vector search on PostgreSQL no longer stops at the first 40 rows of the
  HNSW index. pgvector's scan returns at most `hnsw.ef_search` rows (40
  unless set), before any `WHERE`: a search for more than 40 came back with
  40, and one restricted to a collection, a metadata value, an ACL, a facet
  or a radius came back with the few of the first 40 that passed (3 of 5 for
  a collection holding 5 % of the store), all without an error. An
  unfiltered search of up to 1000 rows now widens the scan to its limit; a
  filtered, unbounded or longer one is ranked exactly, without the index.
  The graph's nearest nodes, asked for at least 50, got 40 and now get their
  limit; the binary index's shortlist past 1000 rows (from `top_k` 126 at
  the default oversample) failed with an `hnsw.ef_search` range error and is
  now ranked exactly. The scan still counts row versions a vacuum has not
  removed yet, so an unfiltered search on a table with many updated or
  deleted rows can still come back short until the next vacuum.
- On PostgreSQL, graph nodes written in a batch (`UpsertNodesBatch`,
  `ExecuteBatch`, and so ingest, memory, graph import and knowledge
  transactions) and the RDF term nodes written with triples are found by their
  vector. Only `UpsertNode` copied a vector into `graph_node_vectors`, the
  table the graph's vector search reads: once a single node had been written
  alone, no node written in a batch was found by similarity, with no error.
  These writes now put the mirror row in their own transaction, and opening a
  store mirrors the stored vectors that an earlier version left out; a stored
  vector it cannot take is skipped, and read again at the next open. Each node
  of a batch now pays the HNSW index insert that each `UpsertNode` already
  paid: a batch of 500 nodes of 384 dimensions went from 33 ms to 3.9 s here;
  a bulk load can drop `idx_graph_node_vectors_cos` and let the next open
  rebuild it.
- On PostgreSQL, the graph's vector table holds, and is asked about, only
  vectors of its column's width, read from the column (or the configured one,
  for a column without a width), and finite. A store opened with the default
  config makes that table 64 wide; with an embedder of another width, every
  graph vector search failed (`expected 64 dimensions, not 8`), and
  `UpsertNode` returned that error after writing the node. Such a search is
  now scored in Go and such a node is written without an error, left out of
  the table, as is a vector holding NaN or an infinity.

## [2.141.0] - 2026-10-29

### Security

- `find_nodes` sizes its matches by the nodes it can match, not by the
  caller's `limit`: a limit past what a slice can hold (`1 << 62`) panicked,
  and since gRPC handlers do not recover, `cortexdb-grpc` exited. Any key
  allowed to call `find_nodes` could stop the server.
- A node follows its document's scope rules only when its id is the one
  ingest derives from its document (`doc:<id>`, `chunk:<id>:NNN`), not when
  its type says `document` or `chunk`: an entity another collection typed
  `document` was served whole to a key confined to a collection that knows
  it, with that collection's description and collections list.
- A gRPC handler that panics is answered with `Internal` and logged with its
  stack instead of taking the server down.

Both reported privately by Lauro Foletti.

### Added

- A key confined to a collection or user may call `find_nodes`, held by the
  facade's hidden-node filter; `node_types` matches the type the caller is
  served (#111). Lauro Foletti.
- Flow traces (W3C Trace Context): gRPC, HTTP and MCP calls join the caller's
  `traceparent` (MCP: `params._meta.traceparent`) as a child span or start a
  trace, return their own span (`traceparent`, MCP `_meta`
  `cortexdb/traceparent`), and forward it to the brain, the embedder, LLM,
  reranker and SPARQL `SERVICE`. Log lines, trace records and the recall log
  carry `trace_id` and `span_id`; `recall_log_list` and `/recalls?trace=` filter
  by it; `observability.WithTraceparent` joins library calls to a flow.

## [2.140.0] - 2026-10-28

### Added

- Request ids from MCP through gRPC to the trace record and back (`x-request-id`,
  MCP `_meta` `cortexdb/request_id`); the Node and Python clients send one per
  call and put it on errors (#104).
- Plugins and the `cortexdb-live` mod report a reply that is not JSON by its
  size, first bytes and request id, not by the parser's message (#104).
- Recalls explain themselves (`explain: true`, and always in the trace): plan,
  candidates per retriever with raw scores, fused list, what each floor dropped,
  final hits. A `degraded` field says when the answer was keyword-only because
  the embedder was unreachable (#104).
- Metrics: embedder reachability, latency and a periodic probe; recalls per tool
  and key; empty and degraded recalls; floor drops; reply sizes. `/healthz`
  includes the embedder (#104).
- Structured logs (`log/slog`, `CORTEXDB_LOG_FORMAT`, `CORTEXDB_LOG_LEVEL`) and a
  rotating trace file (#104).
- Memories say how they are known: `grade` (`verified`, `asserted`, …) and
  `grade_why` on save and update, `min_grade` on memory search and recall,
  guesses in their own `unverified_memories` section of the context pack and
  marked `UNVERIFIED` in auto-recall, graded capture, and `--export-memory
  --min-grade verified` for sharing lessons without the guesses (#107).
- An opt-in recall log (`CORTEXDB_RECALL_LOG=1`) with `recall_log_*` tools,
  feedback that exports as recall-eval golden cases, a `/recalls` page in the
  live view, and graph shading and replay by recall (#104).

### Changed

- The server's log lines are `log/slog` records (logfmt by default): the
  request log writes `req_id`, `rpc`, `dur_s`, `stages_ms.<stage>` and
  `slow=true` where it wrote `dur=…s`, `<stage>=…s` and `(slow)`. Every value
  is quoted when it needs to be, so a caller's tool name cannot write fields of
  its own. A parser of the old text line needs updating; the JSON-lines trace
  file keeps its fields and gains `req_id`, `resp_bytes`, `result_bytes`, the
  client's host and project, and a recall's explain record (#104).
- A knowledge search whose vector half failed now says it answered by keyword
  (`effective_mode: lexical`, and `degraded`), as a memory search already did
  (#104).
- The Python client's generated code is regenerated from the current protos,
  so `AdminService.Backup`, `ContractService` and `DecisionService` are
  callable from Python (#104).
- `find_nodes` folds each node's name once a call rather than once per name
  asked about: a batch of eight names takes 156 ms on a 30,000-node store,
  where it took 798 ms (#110). Lauro Foletti.

## [2.139.1] - 2026-10-27

### Security

- `POST /v1/graph/sparql` is a write unless the executor's parser says the
  query does not mutate, as the tool route and gRPC `QuerySparql` already
  were. It was classified as a read for every query, so a read-only key
  could store or delete triples with `INSERT DATA` / `DELETE DATA`.
  Reported privately by Lauro Foletti.

### Changed

- `upsert_entities` records the collections of the stored chunks it names
  on each entity, in union with the stored list, so a caller confined to one
  of them is served the entity (#105). Lauro Foletti.

## [2.139.0] - 2026-10-24

### Security

- A request body that names one field twice (the same key, or two spellings
  encoding/json reads into one field, such as `query` and `QUERY` or
  `user_id` and `uſer_id`), at any depth, is refused with 400 on REST and
  InvalidArgument on gRPC when it comes from a confined or read-only key,
  before any policy reads it. The policy read the last copy of a key, by its
  exact spelling, and the handler a different one, so such a body could read,
  overwrite or delete another collection's or user's rows, and a read-only
  key could run a SPARQL update. Reported privately by Lauro Foletti.
- Under a caller scope, the graph tools (expand_graph, get_nodes,
  knowledge_memory_neighbors, knowledge_memory_shortest_path,
  knowledge_memory_expand_entity_context, search_paths,
  search_chunks_by_entities) walk only the nodes and edges the caller is
  served. They walked the whole graph and served the caller's own nodes they
  reached, which told a key confined to one collection what another
  collection states, through a relation or a chunk it could not read.
- The request log quotes a tool name that is not a plain word, so a caller
  cannot write fields or lines of its own into it.

### Added

- Confined keys may call the graph tools, confined by the facade (#97,
  #95, #96, #94); the graph lane of a query with a collection walks only that
  collection's graph (#98). Lauro Foletti.
- Servers read the key file again when it changes (#87); every typed write
  signs `written_by` from the calling key (#88); extract_conversation,
  memory_list_all and promote_to_knowledge honour the caller's memory scope
  (#89); the request log names the rows a tool call names (#90). Lauro
  Foletti.
- `knowledge_get` takes a list of ids, at most 100, each served once, as
  `memory_get` now does too (#91). Lauro Foletti.
- `check_against_sources`: the facts of a sentence (numbers, names) that its
  cited sources do not contain, with no model involved; a name is found as a
  word of a source, not inside another word (#92). Lauro Foletti.
- `glama.json` names the maintainer for the Glama listing.

### Changed

- The duplicate graph indexes idx_edges_from and idx_edges_to are dropped;
  the composite and walk indexes serve the same queries (#93). Lauro Foletti.
- Keyword retrieval folds accents as the word index does (#100), the words
  of alternate queries join the keyword query (#101), and with alternate
  queries or keywords every phrasing runs, fused by rank (#102); a Swiss
  lexical retrieval set joins the eval (#99). Lauro Foletti.
- In that fusion the question's own lists count in full and those of
  alternate queries and keywords half; an alternate whose words are all in
  the question is left out, ties go to the question's own rank, the
  keyword floor is applied before the cut to top_k, and the alternates and
  synonyms a search measures are capped. On a snapshot of a live brain, with
  planned queries (lexical): knowledge hit@3 8/18 → 14/18 and memory
  off-topic rows 6 → 3; on the golden sets as written, unchanged.

### Fixed

- Two documents that state the same relation keep an edge each, with its
  own provenance; the second overwrote the first's.
- cortexdb-live: a recall whose reply passes the host's size limit is asked
  again as an index, and a reply that is not JSON is reported by what it
  says instead of as a JSON parse error.

## [2.138.0] - 2026-10-23

### Changed

- Keyword hits are scored by the rarity of the words they match: relevance
  is the IDF-weighted share of the question's words a row covers, so a row
  that shares only a common word ("是什么", "CortexDB") with the question no
  longer scores high, and a hit must match more than one word of the
  question. Fusion ranks a hit found by both retrievers first and breaks ties
  in favour of the semantic one.
- Knowledge search has a relevance floor: a chunk must reach a cosine of
  0.40 or a keyword coverage of 0.25, and keyword memory hits a coverage of
  0.25; below it fewer or no hits come back instead of noise.
  `CORTEXDB_KNOWLEDGE_VECTOR_FLOOR`, `CORTEXDB_KNOWLEDGE_KEYWORD_FLOOR` and
  `CORTEXDB_MEMORY_KEYWORD_FLOOR` tune them. On a snapshot of a live brain:
  memory hit@5 0.604 → 0.750 (lexical) and 0.750 → 0.833 (hybrid), no
  labelled answer lost; hits returned for off-topic questions 43 → 1
  (knowledge, hybrid). In lexical mode a memory that shares one word with a
  longer question is no longer returned.

### Fixed

- The HNSW index loaded from its snapshot could stay far behind the table
  and nobody noticed. On a copy of a live brain, 316 of 529 rows were
  missing from the loaded index, 66 nodes still held 64-d lexical vectors
  their rows had been re-embedded away from, and 1,296 of 1,509 nodes were
  tombstones. Vector search matched brute force on 1 of 18 golden queries,
  and knowledge hit@3 was 4/18. Opening a store now checks a loaded HNSW or
  IVF snapshot against the table: missing rows are inserted, ids that are
  gone are removed, and rows written since the snapshot are written again.
  A snapshot that cannot be patched (mixed or foreign dimensions, more
  tombstones than live nodes, or over 30% of rows off) is rebuilt. The
  outcome is logged, to the standard logger when the store has no logger.
  On the same copy: 18/18 and 16/18.
- Writing a row the index already held kept the old vector. Re-embedding,
  `UpsertBatch` and transactional upserts all go through `HNSW.Upsert` now,
  which replaces the node, and treats an identical vector as no change.
- A reloaded HNSW index lost its dimension, so the first insert after a
  restart set it. One 64-d lexical vector written first, and every 768-d
  write was refused until the next restart. `Load` now takes the dimension
  from the nodes. A rebuild uses the dimension most rows have, not the
  first row's.
- `DeleteByDocID`, `ClearByDocID`, `Clear` and `DeleteCollection` now take
  the deleted ids out of the in-memory indexes as well.

## [2.137.0] - 2026-10-23

### Added

- `ingest_document` hashes its content. An identical re-ingest (same text,
  chunking, collection, metadata and vectors) answers `unchanged` and writes
  nothing; a re-ingest of changed text keeps the chunks and the graph the
  caller attached where they still apply. `dedupe: true` refuses content
  already stored in the same collection and names the holder (#75).
- Documents can expire: `expires_at` on `ingest_document`, `knowledge_save`
  and `knowledge_update` leaves an expired document out of every search,
  chunk tool, graph walk and aggregate. `sweep_expired`, `DB.SweepExpired` and
  `cortexdb-mcp-stdio --sweep-expired [--dry-run] [--json]` delete expired
  documents and memories (#76).
- Documents and memories carry a review decision (`review_status`,
  `reviewed_by`, `reviewed_at`); with `CORTEXDB_REVIEW_GATE=1` or
  `WithReviewGate()` proposed and rejected rows are left out of search and
  recall. A curation filter, not access control (#77).

### Fixed

- With an embedder, `ingest_document` wrote lexical vectors of another width,
  which the vector index refused, so its chunks never reached semantic
  search. It now embeds as `knowledge_save` does (#84).
- `vector_dimension_repair` wrote each repaired row back without its
  document, metadata and ACL. It now changes the vector only, and gives back
  to chunk rows an earlier repair stripped their document and metadata, from
  the chunk id and the document record; their ACL cannot be recovered (#84).

### Upgrade notes

- The engine keeps its own marks under keys that start with `_`
  (`_expires_at`, `_review_status`, `_content_sha256`, `_vectors`, …). A
  database that already used `_expires_at` or `_review_status` as ordinary
  metadata will see those rows hidden; run `--sweep-expired --dry-run` before
  the first real sweep.
- Documents stored before this release carry no `_vectors` mark, so their
  next `ingest_document` rewrites them once, with the current embedder's
  vectors, instead of answering `unchanged`.

## [2.136.0] - 2026-10-22

### Added

- A scope-confined key can call the toolbox: `CallTool` over gRPC and
  `POST /v1/tools/<name>` over REST, which were refused to it outright. An
  explicit allowlist in `pkg/authz` (one line per tool, closed by default,
  held complete against the tool definitions by a test) says which tools a
  confined key may call and classifies every argument: the confined field is
  forced to the key's value, two-store tools are gated per store, and every
  argument that names a row by id, nested ones included, is checked against
  the row, a foreign id answered as missing. Declared entities and relations,
  engine metadata keys and path retrieval are refused to a confined key
  (#72, #73, #74).

### Changed

- `memory_save` checks its `supersedes` targets before it writes: a missing
  or foreign target now leaves no new memory, where it used to store the
  memory and then report the target missing (#72).

## [2.135.1] - 2026-10-22

### Fixed

- Over REST, a confined key could overwrite a row of another scope by naming
  its id on `POST /v1/knowledge` or `POST /v1/memory`, and so move it into
  its own scope, and could retire another user's memory through
  `supersedes`. The REST policy now reads the row an upsert names, under
  every spelling of the id field, and answers a row outside the key's scope
  as not found, as gRPC does; a supersede target outside the scope, or
  missing, is refused before anything is written. An id of blanks is now
  guarded like any other on both ports (#82).
- `InsertGraphDocument` let a collection-confined key overwrite a document of
  another collection by id; it now goes through the same guard as
  `SaveKnowledge` (#83).
- SQLite's `JSONArrayContains` failed the whole statement on a node whose
  properties held no JSON, so one such node broke every document deletion
  (#79).
- The geo index shared a point's metadata map with callers on the way in and
  out; every path now copies it (#80).
- Redeclaring a histogram with different buckets returned the first
  declaration silently; it now panics like any other conflicting
  redeclaration (#81).

## [2.135.0] - 2026-10-21

### Changed

- `cortexdb.WithMemoryOwners` (v2.134.1) is replaced by
  `cortexdb.WithCallerScope`, which carries a confined caller's memory and
  knowledge rules and its identity. `authz.Key.CallerContext` builds it; both
  ports already call that, so a server needs no change.

### Fixed

- A key confined to some user_ids could create a bucket nobody had used yet,
  the global bucket of a new namespace or a session id another user had not
  yet opened, and own what unconfined writers saved there later. The global
  bucket now belongs to nobody whoever writes to it first, a confined key
  cannot create it, and a save that names a user_id is refused (invalid) in
  a session another user owns.
- Identical memory searches that arrive together share one run; a run is no
  longer shared between callers with different scopes, which handed a
  narrower key the answer of a wider one.
- `POST /v1/recall` for a confined key reached the store its scope does not
  cover: a user-confined key recalled every collection's knowledge, a
  collection-confined key any user's memories. Each half of a recall now
  answers only what the key reaches, the rules the typed RPCs apply to rows
  read by id, and a confined caller gets no graph facts, whose shared edges
  carry no scope.
- The graph lane of cortex_query stays in the request's collection (#78).

## [2.134.1] - 2026-10-20

### Fixed

- A key confined to some user_ids could read and write another user's
  session memories by sending its own `user_id` beside the other user's
  `session_id`: the policy checked the fields and the session then picked
  the bucket. A confined key now reaches only memory buckets its user owns,
  over gRPC, REST and recall alike. A bucket it may not read answers like
  an empty one, and a write into it is refused as not found. The global
  bucket, which no user owns, is out of reach for a user-confined key too,
  as unowned rows already were for reads by id. Unconfined keys and the
  legacy token are unchanged.
- REST saves are signed with `written_by` and `updated_by` from the
  authenticated key, as gRPC saves have been since v2.134.0.

## [2.134.0] - 2026-10-19

### Added

- Saves are signed. Behind a key policy, memory_save, memory_update,
  knowledge_save and knowledge_update record the authenticated key as
  `written_by` (a save) or `updated_by` (an update, which keeps the original
  `written_by`), replacing whatever the caller wrote under those names.
  `cortexdb.WithWriter` does the same for an embedded server.
- Memory search hits carry `via`, the retrievers that found them
  (`semantic`, `keyword`, `graph`), and `similarity`, the raw vector
  similarity before the importance and recency boosts that make `score`.
- Auto-recall explains each hit, in the injected block and in the
  cortexdb-live band: the retriever and its own score, the query terms the
  hit contains, and where it came from (date, agent, source, model, key,
  host, project, session, URL). The shared `legacy-token` is not shown as a
  writer.
- Captured memories record `agent`, `host` and `project` beside `session`.

### Changed

- Auto-recall asks with the conversation. The cortexdb-live planner gets the
  project and the last four turns and resolves what the message points at
  ("这个修复", "按建议", "the fix") before it writes the query. Without a
  plan, and in the shell hook, the query carries the project and the start
  of the last reply, so a short follow-up no longer recalls another
  project's notes.

## [2.133.0] - 2026-10-19

### Added

- A key's scope may list several values per field: `user_ids`, `scopes`,
  `namespaces` and `collections` beside the singular `user_id`, `scope`,
  `namespace` and `collection`, which keep working; the two forms add up.
  A request still names one value; the list widens what the key may name.
  A list holding an empty string is refused when the key file loads (#71).
- The gRPC request log names the key behind each call (`key=<id>`, never the
  secret) and the tool a CallTool ran (`tool=<name>`), on served and denied
  calls alike. CORTEXDB_TRACE_FILE records carry the same two fields (#68).
- ingest_document takes `source_url` and `author` and keeps them, with the
  caller's metadata, on the document record and the document node, as
  knowledge_save does, so a search hit reports where the document came from.
  A re-ingest replaces the record's provenance (#69).

### Fixed

- A scope-confined key could reach another user's rows over REST by sending
  a second spelling of the field: `{"user_id":"hermes","USER_ID":"zeus"}`
  passed the check, which read only the exact key, while the handler's JSON
  decoder took zeus. Every case variant, top-level or nested, is now held to
  the confinement (#64).
- A graph search confined to one collection reached another collection's
  chunks through an entity both mention. Entity nodes now record which
  collections mention them, the walk keeps to entities the searched
  collection knows, and every chunk it reaches is checked against the
  collection. Unconfined searches are unchanged (#70).
- The SessionEnd capture hook declines before writing anything when no LLM
  is configured, and removes its payload file however the capture ends,
  including a signal; payloads older than a day are swept (#67).
- IVF training rejects vectors of the wrong dimension instead of panicking
  (#65). OptimizedBinaryQuantizer.Project returns nil for a vector longer
  than its input dimension instead of panicking (#63).

## [2.132.1] - 2026-10-18

### Fixed

- changes_since over MCP failed output validation for every event that
  carried a before or after row summary: the inferred schema read those
  fields as byte arrays and rejected the JSON objects the change feed writes.
  They now get an open schema and stay embedded JSON (#61).
- MultiIndex rerank keeps the primary index's distance as the first vote. A
  candidate no secondary index returned used to score zero and rank first,
  and with no secondary indices every result came back with distance zero in
  map order (#57). MultiIndex and HybridIndex return nothing for a
  non-positive k instead of panicking (#58).
- HNSW and FlatIndex batch inserts validate every vector's dimension before
  writing, so a batch with one bad vector leaves the index unchanged (#55,
  #56).
- A k-means cluster that attracts no vector keeps its centroid instead of
  being reset to the origin, where it won encodes for any vector near zero
  (#59). SearchBinary sorts in O(n log n) and keeps database order on equal
  distances (#60).
- Geo search between antipodal points returned NaN distances, so a point on
  the far side of the globe was missing from KNN and whole-sphere radius
  searches (#54).

## [2.132.0] - 2026-10-17

### Added

- In the live graph view, clicking a memory node opens the whole memory: its
  text, rendered with the headings, bullets, bold and code it was written in,
  when it was saved, its scope and importance, and its metadata (type,
  project, topic, recall count and the rest). The node itself is labelled
  with a clipped summary; until now the panel showed only that. Works on a
  local database and on a shared brain, where it reads through memory_get,
  which is added to the view's read-only tool list. The panel widens for a
  memory.

## [2.131.0] - 2026-10-17

### Changed

- The live graph view re-reads the graph only when the change feed says a
  node or edge changed, and once a minute regardless. It used to pull the
  whole graph every two seconds over a fresh connection: on the shared brain
  (2,000 nodes, 5,600 edges) that is about a second of server time a read,
  and one open page held the brain at 35% of a core on a two-core host. With
  the page open it now sits at 7%, against 6% with no page. A brain without
  changes_since is read on every tick, as before.
- Every call the view makes to a shared brain goes over one kept connection
  instead of dialing per call.
- The view loads 3d-force-graph, three and the bloom pass as one bundle
  embedded in the binary, gzipped and cached for good. The page used to load
  two copies of three from unpkg, and drew nothing offline.
- Pages and JSON are gzipped: the graph goes from 755 KB to 112 KB, the page
  from 141 KB to 47 KB. The event stream is left uncompressed.
- A flash animates the node's mesh instead of re-setting every accessor on
  every node and link every 90 ms. With twenty nodes flashing the page held
  30 fps and spent 1.75 s of every 3 s in long tasks; it now holds its full
  frame rate with none.
- The view and the GraphFlow 3D export open light. Dark and follow-the-system
  are one click away and remembered.

### Added

- Nodes are named as the camera comes close: a sphere drawn at least 7 px
  across gets its label, largest first, without overlaps. Zoomed into a
  neighbourhood of the shared brain that is 30 names where there were 3.
- The type filter has "All but memories" (`?type=!memory`): memories are 906
  of the shared brain's 2,000 nodes, and hiding them shows the entities and
  projects between them.

## [2.130.1] - 2026-10-16

### Fixed

- A one-word query was padded with the shortest memories in the store. After
  the memories that contain the word, the semantic hits filled the rest of
  topK, and a single word embeds next to whatever is shortest: on the shared
  brain the user's city (seven characters) and name came back for 48 of 60
  one-word queries. Semantic hits for a one-word query now have to clear the
  memory floor plus 0.16 (0.47 on embeddinggemma). They reach 3 of the 60,
  hit@5 stays 0.917, and a memory that means the word without containing it
  (在持美股 for the VOO and OKLO holdings) still fills. Sentence queries are
  unchanged: golden-set hit@5 0.792 and MRR 0.627 before and after.
- Index and quantizer fixes contributed in #43–#53: IVF inverted lists stay
  aligned after Delete and an ID added again replaces its entry; LSH and HNSW
  replace or re-insert an ID that was there before; HNSW and IVF snapshots
  load atomically and inconsistent ones are rejected, so the index is rebuilt
  instead of panicking; a non-positive k returns nothing; scalar, binary and
  product quantizers validate their state, sizes, training vectors, queries
  and codes; the IVF size is read under the index lock.
- TestProductQuantizerSearch failed about 8 times in 2,000 runs: it required
  the query's own code to rank first, which a lossy product quantizer does
  not promise. It now requires it in the top 5.

## [2.130.0] - 2026-10-16

### Changed

- Memory recall with an embedder fuses the semantic and keyword answers by
  reciprocal rank, as knowledge search already did. The semantic list used to
  fill topK first and the keyword list only what was left, so five vaguely
  similar memories always beat an exact match. On a snapshot of the shared
  brain: hit@5 0.771 → 0.792 on the golden set (MRR unchanged), and on 24
  one-word queries the share of top-5 memories that contain the word went
  from 0.30 to 0.85.
- A query that is a single term (an acronym, a name, up to four Chinese
  characters) is answered by the memories and knowledge chunks that contain
  it first, with semantic hits filling the rest. Asking "HA" returned the
  user's name and city; it now returns the memories about HA.

### Fixed

- Text with few spaces, such as Chinese, was cut into fixed-width windows
  that started and ended mid-word and could leave a last chunk of a single
  ")". Windows now end at a space or punctuation, and a short remainder
  joins the last window. Documents saved before keep their chunks until
  they are updated.
- Knowledge hits with equal scores were ordered by id, so a document sorting
  early in the alphabet won every tie. They keep the order retrieval gave
  them.

## [2.129.3] - 2026-10-15

### Fixed

- `geo.GeoIndex.Insert` left an earlier point with the same ID in its grid
  cell, so a moved point was still found at its old location and a point
  inserted twice in place was listed twice (#39).
- `geo.GeoIndex.SearchRadius` sized its longitude window as at the equator,
  so it missed points inside the radius away from the equator and never
  looked across the antimeridian (#40).
- `ProductQuantizer.DeserializeCodebooks` panicked on truncated data,
  allocated from an untrusted header, and left a half-loaded quantizer on
  failure; it now checks the header against the body first (#41).
- `BinaryQuantizer.SearchBinary` ranked codes of the wrong length first and
  panicked on a non-positive `topK` (#42).

## [2.129.2] - 2026-10-14

### Fixed

- The cortexdb-live mod did not load on Claude Code 2.1.299, which refuses
  an optional call on `$` (`$.session.root?.()`) as reading the event as a
  value, so recall and capture were off in every session. It calls
  `$.session.root()` directly and keeps absolute paths where the call fails.

## [2.129.1] - 2026-10-14

### Fixed

- A client of a shared brain no longer fails a call with `connection reset
  by peer` when the connection it held was lost while idle — the brain
  failed over, or the connection was dropped on the way. The remote MCP
  server and liveview ping an idle connection every 30 s, so a dead one is
  replaced before a call needs it, and retry a call that fails as
  UNAVAILABLE once on a fresh connection; the server never returns that code
  itself. The gRPC server accepts those pings (one per 20 s, with or without
  a call in flight); an older server hangs up on them, so upgrade the
  server first.

## [2.129.0] - 2026-10-14

### Changed

- A memory recall is several times faster, and stays fast under a burst.
  Measured on a two-core node against a copy of a 4,300-memory shared brain
  with its embedding model on another machine: one recall went from 1.0 s
  (p50) / 1.6 s (p95) to 0.30 s / 0.45 s, and sixteen at once from 6.2 s /
  7.8 s to 0.82 s / 0.84 s.
  - The vector scan keeps each session's decoded vectors in memory, checked
    against a version SQLite triggers bump on every insert, delete or update
    of a vector or its session — by any writer — and reads only the rows
    that make the cut in full. Vectors are encoded and decoded directly
    instead of one value at a time through `binary.Read`.
  - The full-text update triggers on `messages` fire on a change of
    `content` only. They fired on any column, so every recall counter
    written to a memory's metadata rewrote its text in both full-text
    indexes.
  - Query embeddings are cached (2,048 texts), and concurrent requests for
    the same text share one call to the model.
  - The keyword search runs beside the embedding and the vector search
    instead of after them, in memory recall and in hybrid knowledge search.
    A query embedding gives up after 800 ms (`CORTEXDB_QUERY_EMBED_TIMEOUT_MS`)
    and the search answers by keyword; hybrid knowledge search no longer
    waits on the model without a bound.
  - Identical memory searches that arrive together share one run, and each
    is still counted as a recall.

## [2.128.0] - 2026-10-14

### Added

- A shared brain can be reached over TLS: `CORTEXDB_REMOTE=https://host[:port]`
  (port 443 by default) dials with TLS checked against the system roots, for a
  `cortexdb-grpc` published behind a TLS-terminating proxy such as Caddy with
  an `h2c://` upstream. `host:port` and `http://host:port` stay plaintext.
  `cortexdb-mcp`, `cortexdb-grpc -health` and the live view all go through the
  same `pkg/rpc.Endpoint`.

## [2.127.2] - 2026-10-14

### Fixed

- The context pack that recall returns clipped each memory and knowledge
  section at a byte count, which split a CJK character and left U+FFFD
  (`/dev/lab/data �...`). The cut keeps the same byte budget and steps back
  to the start of the character it would have split.

## [2.127.1] - 2026-10-14

### Fixed

- Knowledge snippets, graphflow summaries and rule-syntax errors were cut at
  a byte count, which in Chinese, Japanese or Korean text lands inside a
  character and leaves U+FFFD (`高可用�`) at the end. They are cut at a
  character count now; a snippet is 180 characters.
- The Obsidian panel no longer repeats a knowledge document's title at the
  start of its snippet, and expanding a card shows its text as it is, rather
  than rendering a flattened snippet that begins with `#` as one large heading.

### Added

- The Obsidian panel reads a result in full: its title, or the expand button,
  opens the whole memory or the whole knowledge document rendered as Markdown.
  Copy and insert take the full text.

## [2.127.0] - 2026-10-14

### Added

- An Obsidian plugin (`plugins/obsidian-cortexdb`, released as
  `cortexdb-obsidian.zip`). A side panel shows what the brain knows about the
  open note — memories, knowledge, and the entities they touch — and answers
  typed questions. Commands remember a selection as a memory, and send a note
  or the vault as knowledge documents (`obsidian:<vault>/<path>`, only changed
  notes, deleted ones taken out, optionally as you write). *Export the brain*
  writes every memory and entity as linked notes, so the graph view and
  backlinks show the brain; an edited memory note goes back through
  `memory_update`. The server token lives in Obsidian's keychain, not the vault.

### Fixed

- `cortexdb-grpc -h` printed `$CORTEXDB_GRPC_TOKEN`: the flag took the token
  as its default, and the help screen shows defaults. The token is now read
  from the environment after the flags are parsed.

### Changed

- The DeepSeek Harness plugin installs from this repository's release tags
  (`dsh plugin --profile web add "github:liliang-cn/cortexdb#semver:^2&path:/plugins/dsh-cortexdb-memory"`)
  instead of npm, and CI no longer publishes it there. A release needs no npm
  token, and the plugin is never behind the binaries it downloads.

## [2.126.0] - 2026-10-14

### Added

- A session starts with an index of what the brain knows about its project.
  The plugin's SessionStart hook (`cortexdb-mcp --session-context`) finds the
  graph node of type `project` named like the working directory or its git
  repository (case and punctuation ignored) and lists the memories that
  mention it, newest first, one line each — id, date, first line — leaving out
  superseded ones; `CORTEXDB_SESSION_CONTEXT_MAX` (default 30) caps it. The
  first prompt of a session usually says too little for the per-prompt recall
  to find anything.
- Memories about files. A capture lists the files the session read and
  modified, named from their repository root, and the model links each memory
  to the files it is about as entities of type `file`. A new PreToolUse hook on
  Read, Edit and Write (`--file-context`) hands the agent the current memories
  about a file before it opens it, once per file per session.
- `knowledge_memory_recall` takes `detail: "index"`: the same hits in the same
  order, each cut to its first line, for a caller that picks what to read
  before reading it. `memory_get` takes `memory_ids` to fetch several at once,
  in order, listing unknown ids in `missing`.
- Captures are scrubbed before the model sees them and before anything is
  saved: text between `<private>` and `</private>` is removed, and credentials
  in the shapes they are issued in (API keys, GitHub, AWS, Slack, Google and
  npm tokens, `Bearer` tokens, PEM private keys, `password=`-style settings)
  become `[REDACTED]`. This covers Claude Code, Codex and DeepSeek Harness
  sessions, `import_agent_memory`'s session import and the cortexdb-live mod.
- A captured fact that repeats a current memory word for word (case and
  punctuation aside) is not saved again; the import reports such facts as
  `duplicates`.

### Fixed

- A hook's context block could exceed what Claude Code passes on: past about
  ten thousand characters it replaces `additionalContext` with a short
  preview, so the model received none of the recall. Every hook block now stays
  under 9,000 characters, dropping whole entries from the end and saying how
  many it left out; the cortexdb-live mod's recall does the same.
- Two tests failed for reasons outside the code: the hook tests inside a
  session running the cortexdb-live mod, whose variables tell the hooks to
  stand down, and the reembed backlog test whenever the background healer
  filled the backlog first.

## [2.125.1] - 2026-10-14

Thirty of these fixes came as pull requests from @rudycelekli (#9–#38), each
with a regression test; three follow-ups adjust how two of them fail.

### Fixed

- Graph: PageRank dropped the score of nodes with no outgoing edges, so scores
  no longer summed to 1; it is redistributed now. GraphML and GEXF imports gave
  vectorless nodes a fake 3-d zero vector and replaced an unreadable vector
  with zeros; vectorless nodes stay vectorless and a malformed vector fails the
  import. RDF/XML accepted mismatched and unclosed elements. SPARQL `DISTINCT`
  and `GROUP BY` keys were joined with separators that values can contain, so
  different solutions could collide. SHACL did not check `xsd:negativeInteger`
  or `xsd:nonPositiveInteger`.
- Cypher: `range()` with a step near the integer limits wrapped around instead
  of stopping, and `substring()`, `left()` and `right()` accepted positions
  past what a string slice can hold.
- Semantic router: a route whose best score was zero or negative (negative
  Euclidean distance, negative dot product) could never be chosen; functions
  were registered under the function name while routing returns the route
  name; per-function extractors and a schema's `required` list were ignored;
  keyword patterns were not escaped and extracted values were lowercased; a
  failed utterance embedding was cached for good; the cached embedder and the
  sparse encoders' refit were not safe under concurrent use; one batch could
  add two routes of the same name, and a nil route panicked.
- agentmem: `AppendContext` read, concatenated and wrote back, so concurrent
  appends lost sections (20 concurrent appends kept 15 on SQLite and 5 on
  PostgreSQL); it is one upsert now. A reflection could mark an observation of
  another scope stale; such a target is now ignored and reported in
  `IgnoredTargets`, and the observation is still saved.
- Importers and connectors: duplicate CSV header names silently shadowed a
  column (now an error; an empty header becomes `column_<n>`); a negative
  sample size was used as given instead of the default; email masking cut multi-byte local parts and
  printed a value with no `@` unmasked; columns that were NULL in every row
  were dropped.
- `quick` and text APIs wrapped embedding errors with `%v`, so `errors.Is`
  could not see the cause; an empty vector batch with non-empty contents
  returned success. CSV export did not report a failed final flush. Key files
  with trailing data after the JSON document were accepted. Exported memory
  frontmatter mis-escaped backslashes, and now keeps `<`, `>` and `&` readable.
- graphflow lost extraction metadata in JSON exports and aliased the caller's
  metadata map while merging nodes.
- The DeepSeek Harness plugin accepted a truncated gRPC frame or protobuf field
  as a successful, shorter answer.

## [2.125.0] - 2026-10-12

### Added

- DeepSeek Harness support. `plugins/dsh-cortexdb-memory`, published to npm
  as `cortexdb-dsh-memory` and installed with
  `dsh plugin --profile <profile> add cortexdb-dsh-memory`, is a cordis
  bundle that gives dsh what the Claude Code and Codex plugins give those
  agents: recall before each prompt a person types (the same question, top-3,
  snippets and wording as their recall hook, across the whole brain unless
  `scope`/`namespace`/`userId` narrow it), capture of a session's durable
  facts when it goes quiet or ends (`auto:dsh-<session>:<slug>`, retiring the
  memories they make untrue), and `cortexdb_memory_search` / `_store` /
  `_forget` tools. It connects to a sidecar at `endpoint` or starts the
  release `cortexdb-grpc` of its own version against the local database. It
  has no runtime dependencies — gRPC is spoken over `node:http2` — because
  dsh installs plugins with pnpm, which refuses unapproved build scripts, and
  every gRPC library for Node brings protobufjs and its postinstall.
- `cortexdb-mcp --capture-session` reads a dsh session log as well as a
  Claude Code transcript, and `import_agent_memory` has a `dsh_sessions`
  source (included by `--sessions`) that distils past sessions from
  `$DSH_HOME` (default `~/.dsh`). dsh's logs are Zstandard-compressed JSON
  lines; a session still being written is read up to its last complete frame.
- The gRPC server logs every call slower than `CORTEXDB_SLOW_MS` (default
  1000, `0` for none) with the time each stage took — embedding, vector,
  lexical, graph — so a slow recall says which of the embedder, the index or
  the graph it waited on. `CORTEXDB_TRACE=1` logs every call;
  `CORTEXDB_TRACE_FILE` also appends the records as JSON lines.

### Fixed

- A recall waited as long as its embedder took to embed the query — 24
  seconds when the embedder was a model on another machine that had to load
  first. Embedding the query is bounded at 3 seconds; past it the recall
  answers by keyword, as it did anyway once the caller gave up.
- The live view's library had no project shelves for a local brain: loading
  it dropped the `mentions` edges memories make, which are what shelve them.
- `TestHealerBacksOffWhileTheEmbedderIsDown` failed on a loaded machine: it
  allowed eight embedder calls for about five healing passes, but each pass
  makes two (21 memories, in batches of 16).

## [2.124.0] - 2026-10-08

### Added

- `cortexdb.EmbeddedConfig(path)`, a configuration for small devices — a
  Raspberry Pi, an industrial gateway, a router-class ARM board. The HNSW
  index is held as SQ8 codes; SQLite keeps a 1 MiB page cache on each of at
  most 4 connections, maps up to 64 MiB of the file and puts temporary
  b-trees in files; the index snapshot is saved every 10 minutes when it has
  changed. On 100,000 768-d vectors a store opened from its snapshot in
  0.28 s with a 193 MB heap and a 297 MB peak RSS, against 1.7 s, 485 MB and
  1,982 MB for the default configuration before this release.
- `Config.Quantization`, `Config.Binary`, `Config.Resources` and
  `Config.SnapshotInterval` on the facade, which offered none of them: the
  store could quantize its index, but nothing opened through `cortexdb.Open`
  could ask it to. `core.ResourceConfig` sets SQLite's per-connection page
  cache, memory mapping, temporary storage and pool size.

### Fixed

- Rebuilding the HNSW index from stored vectors split them among four
  goroutines, built a graph from each and merged them — four graphs that
  hardly linked to each other. Recall@10 was 0.24 on 100,000 clustered 768-d
  vectors; it is 0.65–0.72 now that the rebuild builds one graph. Every open
  without a usable snapshot took this path. `index.HNSW.InsertBatchParallel`
  now inserts on one goroutine for the same reason.
- The rebuild decoded every stored vector into one slice before inserting
  any, doubling its peak; it streams now. Peak RSS for that open fell from
  2,359 MB to 786 MB at 100,000 vectors (435 MB with `EmbeddedConfig`).
- Index snapshots were one blob, built whole in memory to save and read whole
  to load. They are written and read in 4 MiB chunks; a snapshot written
  before this release still loads.
- A snapshot was rewritten on every close and every auto-save tick, changed
  or not (`AutoSave.MinChanges` was never consulted). It is written only when
  the index has changed.
- Closing allowed five seconds to save the snapshot, which a large index
  overran; the write was abandoned and the next open rebuilt everything.
- A quantized HNSW or binary index reopened with its dimension left to
  auto-detection was built without its codes — float32 vectors, or no binary
  index at all and a table scan per search — and a float32 snapshot was
  loaded as it was under a quantized configuration. Both now learn the
  dimension from the stored vectors, and a float32 snapshot is rebuilt.
- The binary index decoded every vector into memory to learn its center; it
  streams the table twice instead and keeps only the codes.
- A search with no vector index held the whole table in memory to sort it;
  it keeps the best TopK as it scans.
- SQ8 distances decoded each code bit by bit into a fresh vector. 8-bit codes
  are measured in one pass with no allocation, and building a graph no longer
  re-measures candidates it has just ranked or prunes a full neighbour list
  on every new link: a 20,000-vector quantized rebuild went from 1 m 45 s to
  15 s.

## [2.123.0] - 2026-10-07

### Fixed

- Memories saved while the embedder is down get their vectors once it answers.
  A save that could not embed used to fall back to keyword search silently and
  keep its row without a vector for good; now a background pass embeds every
  memory that lacks a vector (or has one of the wrong size) when the store
  opens and again after any save that had to go without one, backing off from
  30s to 30m while the embedder stays unreachable. Tune or disable it with
  `WithVectorHealing`.
- An embedder outage is visible: recall's decision says it answered by keyword
  search only and how many memories still lack vectors, `graph_health` raises an
  `embedder` alert, and gRPC `Info`, HTTP `/info` and `cortexdb-grpc -health`
  report `embedder_healthy`, `embedder_error` and `memories_without_vector`
  (the health probe still exits 0). `db.EmbedderStatus` returns the same.
- Semantic memory recall drops the 0.28–0.31 noise band: the floor is now 0.31
  (`DefaultMemorySemanticFloor`), configurable with `WithMemorySemanticFloor` or
  `CORTEXDB_MEMORY_SEMANTIC_FLOOR`. Keyword matches are never floored.

## [2.122.1] - 2026-10-06

### Added

- `import_agent_memory`, an MCP tool that brings in the memory a person
  already has: Claude Code's memory notes, `CLAUDE.md` / `AGENTS.md`, and
  Codex's own memories (`~/.codex/memories_1.sqlite`, `~/.codex/memories`) by
  default; with `claude_sessions` / `codex_sessions`, past transcripts
  distilled into memories the way a session is captured when it ends, a few
  per call (`max_sessions`, `since`), remembered in
  `~/.cortexdb/imported-sessions.json` so none is read twice. It reads files
  where the MCP server runs and writes to the brain that server uses, local
  or shared. `dry_run` counts first.
- Capture retires what it made untrue. The SessionEnd capture, the
  cortexdb-live mod and the session import look each new fact up and ask the
  model which existing memories it contradicts; those are saved as
  superseded by it — kept and exported, no longer recalled as current. The
  later date wins: an old session's fact never retires a newer memory, and is
  dropped when a newer one already contradicts it. At most five per pass.
- A superseded memory records when (`superseded_at`), so a mistaken pass can
  be found and undone.

### Fixed

- `--import-agent-memory` wrote to the local file even when `CORTEXDB_REMOTE`
  named a shared brain, and reported success. It now runs the same import as
  the tool, into the configured brain, and takes `--sessions`, `--since`,
  `--max` and `--dry-run`.
- Live view library: walls, cornices, window mullions and bookcases no longer
  flicker where two faces shared a plane.

## [2.122.0] - 2026-10-06

### Added

- Live view: a library. The same brain, drawn as a reading room at
  `/library` and linked from the graph and ontology pages. Each memory is a
  book on the shelf of the project it names — the most specific one, when it
  names several — and shelves stand in wings cut from the projects' own
  relations with the module's Leiden. Memories that name no project fill the
  general stacks, documents the reference room. An entity is an index card:
  opening it threads every book that mentions it back to the catalogue and
  lays its relations out on a table. A book opens on the lectern with its text
  paged, its index and the books sharing most of it, each a link. Recalls
  reported over the activity stream light books where they stand. Day and
  night follow the graph page's setting; Chinese or English follows the
  browser (`?lang=`). `serve_graph_3d` returns `library_url` and opens it with
  `view: "library"`.

## [2.121.2] - 2026-10-06

### Fixed

- `cortexdb-live`: every session started with "unreachable: no connected MCP
  tool graph_statistics" and no node count for five minutes. The mod asked
  for the count before the session's MCP servers had finished connecting. A
  server still connecting is now asked again a few seconds later, and a
  successful recall fills in a missing count.

## [2.121.1] - 2026-10-06

### Fixed

- `cortexdb-live`: outside bypass mode every recall, node count and capture
  failed with a clipped "recall failed: … ref". A mod's own MCP calls pass the
  session's permission rules and a hook has no prompt to ask with, so they
  were denied. The status line now names the `permissions.allow` rules to
  add, and the README gives them.
- `cortexdb-live`: the band and status line came out in English on a
  Chinese system, because Claude Code's own `language` row reads English
  until set. Only a Chinese value there decides now; otherwise the locale and
  macOS's language do.

## [2.121.0] - 2026-10-06

### Added

- `cortexdb-live`, an optional Claude Code mod in the same marketplace
  (`/plugin install cortexdb-live@cortexdb`). Haiku plans each prompt's
  recall (Chinese and English keywords, aliases, entity names, retrieval mode,
  or no search at all for a bare go-ahead); what was recalled shows above the
  prompt (`/cortexdb-show` brings back one that was hidden); the status line shows the brain, its size and the last recall's
  time; and an idle session is distilled into memories with haiku. No API
  key: it runs on Claude Code's own model access. `language` is `auto`, `zh`
  or `en`.

### Changed

- The plugin's shell recall and capture hooks stand down in a session the
  mod runs (`CORTEXDB_RECALL_BY_MOD`, `CORTEXDB_CAPTURE_BY_MOD`), so nothing
  is recalled or captured twice. Codex is unaffected.

## [2.120.2] - 2026-10-05

### Fixed

- Live view: a node with no relations could be found but not opened —
  clicking it said "No node …", because expanding it returned nothing, not
  even the node itself. Expanding such a node now returns the node.
- Live view: no more lavender. The hashed colour of a type whose hue fell in
  indigo (`file` among them) read violet once lit and shaded; hues 225°–250°
  are now pulled to a plain blue. On light grounds, dimmed nodes faded to a
  pastel of their hue, which made blue ones lavender; they now fade to their
  own grey.
- Live view, light themes: answer source titles and ids, Cypher's table (its
  header was a near-black bar), facts, the contract's figures and an
  inspector field's value still used the dark theme's literal greys and were
  close to invisible; they now use the theme's inks.

## [2.120.1] - 2026-10-04

### Fixed

- SPARQL: a trailing `VALUES` clause after `GROUP BY`, `HAVING` or `ORDER BY`
  failed to parse (`unexpected token "VALUES"`): those modifiers read it as one
  more group or sort key. SPARQL 1.1 puts the ValuesClause after every solution
  modifier; it now parses after any of them.

## [2.120.0] - 2026-10-04

### Added

- The execution graph API: an agent's record of its own run, as graph
  records. `StartRun`, `BeginStep` / `EndStep` (the two-phase write: a step is
  on the record as running, with `TRIGGERED` edges from the steps it consumed,
  before its work runs), `RecordStep`, `FinishRun`; and to read it back
  `GetRun`, `ListRuns`, `GetStep`, `RunSteps`, `SummarizeRun` (tokens, cost,
  open and failed steps, critical path, least confident step), `StepLineage`
  (upstream or downstream over `TRIGGERED`) and `ReplayRun` (the run as of any
  instant, from history). Step kinds are node types and Cypher labels;
  sub-agent runs hang off the step that started them (`SPAWNED`). The nine are
  also MCP tools, `execution_*`, five of them writes. `examples/19_execution_graph`
  now uses the API.
- SPARQL 1.1 in full, measured against the W3C suites on SQLite and
  PostgreSQL: query 329/331 (the two are a deliberate leniency, `"1" + "2"`
  is 3), update 157/157, results formats 10/10, federated query 10/10; SPARQL
  1.2 269/269. New: every property path (sequence, negated property sets,
  `?`, with SPARQL's set/bag semantics); XSD casts and calls by IRI; trailing
  `VALUES`; `GRAPH ?g` over patterns that do not name the graph, subqueries
  and `MINUS` inside it; bottom-up scope for `FILTER`/`BIND` and the
  grouped-`SELECT`, `BIND` and subquery syntax rules; `ADD`, `COPY`, `MOVE`,
  `CLEAR`, `DROP`, `CREATE` and `LOAD` (refused: the store never fetches);
  `USING`/`USING NAMED` as the update's dataset; `SELECT *` in order of
  appearance.
- `SPARQLResult.WriteResults`: SPARQL results as JSON, XML, CSV or TSV, triple
  terms and base directions included; `ReadSPARQLResultsJSON`.
- `SERVICE` through `GraphStore.SetSPARQLServiceHandler`, off by default so a
  query never reaches the network on its own; `HTTPSPARQLService` is a SPARQL
  1.1 Protocol client.
- RDF/XML import (`RDFFormatRDFXML`), including the RDF 1.2 additions
  (`its:dir`, `rdf:parseType="Triple"`, annotations); the W3C RDF 1.1 and RDF
  1.2 RDF/XML suites pass in full.
- SHACL Core in full — every property path, `sh:qualifiedValueShape` with its
  counts and disjointness, `sh:lessThan`, `sh:lessThanOrEquals`, implicit class
  targets, `sh:deactivated` — passing the W3C Core suite 98/98; and
  SHACL-SPARQL: `sh:sparql` constraints and SPARQL-based constraint components
  with pre-binding, 21/22 (`$shapesGraph`, optional in the spec, is not
  supported). `SHACLValidationResult.SourceConstraint` names the constraint.
- The rest of the OWL 2 RL rule tables: `someValuesFrom`, `allValuesFrom`,
  `hasValue`, `intersectionOf`, `unionOf`, `oneOf`, max (qualified)
  cardinality and `owl:hasKey`, the schema rules over restrictions and lists,
  and as reported contradictions irreflexive and asymmetric properties,
  negative property assertions, `complementOf`, `owl:Nothing`,
  `AllDisjointClasses`, `AllDisjointProperties` and `AllDifferent`. The rules
  are gated on their schema, so a graph that states no OWL pays about 5% on a
  refresh.
- Node property indexes: `GraphStore.IndexNodeProperty(ctx, key)` puts an
  expression index on one JSON property (`NodePropertyIndexes`,
  `DropNodePropertyIndex`; opt-in per key, as each is paid for on every
  write). Equality and `IN` filters on an indexed key become index lookups in
  `GraphFilter.Properties` with no change of its own, and in Cypher, which
  reads the index catalog and also starts its joins from such a filter as it
  does from `id(n) = …`; on SQLite numeric ranges on an indexed key use it as
  well. Results are identical with and without an index. Keys are identifier
  characters only, and a key is written into SQL only from the catalog.

### Changed

- A graph node no longer needs a vector. `UpsertNode`, `UpsertNodesBatch` and
  `ExecuteBatch` accept a node with none, for structural records found by id,
  type, properties and edges rather than by similarity (an agent's execution
  steps, runs, bookkeeping), which previously had to invent one. It is stored
  as an empty vector, so existing brains need no migration, and it is never a
  vector-search candidate: hybrid search, `GraphVectorSearch`,
  `SimilarityInGraph` and `PredictEdges` skip it or give it no vector score,
  instead of scoring it 0 (cosine) or -Inf (Euclidean). A node rewritten
  without a vector is removed from the HNSW index and from pgvector's table.

### Fixed

- SPARQL `FILTER` and `BIND` no longer see variables bound outside their group
  (a nested group's `FILTER(?v = ?z)` read an outer `?z`; a `BIND` in a `UNION`
  branch computed from an outer variable), and a `FILTER` applies to its whole
  group wherever it is written. `IRI()` resolves against `BASE`, `BNODE(str)`
  is per solution, decimal results print exactly (the W3C test's `SUM` is `11.1`,
  not `11.100000000000001`), and `SELECT` aliases already in scope are a
  syntax error.
- SHACL: `sh:datatype` checks the range of bounded integer types
  (`"300"^^xsd:byte` is ill-formed), a date with a timezone and one without are
  incomparable, and only `sh:uniqueLang true` activates the constraint.
- Concurrent writers in one process no longer fail with `database is locked`
  on SQLite. SQLite's busy handler polls with sleeps growing to 100ms, so
  under steady contention the writer that had waited longest kept waking
  after the lock had been taken again and could starve past `busy_timeout`:
  eight goroutines batch-upserting an execution graph lost 3–9 writes in
  15,000, after waits of 5–8.5s, and with the change feed off the worst wait
  still reached 4.6s. The store now wraps its SQLite connections so that a
  transaction that may write, or a statement outside one that writes, first
  takes its turn on a first-come-first-served queue shared by every pool on
  the same file in the process; reads and read-only transactions never
  queue. Throughput is unchanged, there are no failures, and the slowest
  write in the same run takes 48–78ms. Other processes still meet at SQLite's
  lock as before.
- Cypher queries anchored on an id (`WHERE id(t) = $id`, or a node bound by
  an earlier clause) now walk out from that node on SQLite instead of growing
  with the graph. The anchor was already the traversal's seed, but SQLite's
  planner — with no statistics on a brain nobody has `ANALYZE`d — drove typed
  hops against the direction of `idx_edges_composite` from `idx_edges_type`
  (every edge of the type, on every hop) and scanned whole node tables to
  probe a variable-length traversal's few rows. On a 20,000-step execution
  graph a lineage walk back from one step took 67–89ms, one hop back 11ms and
  two hops 40ms; each now takes under 2ms at any size. An anchored part of a
  pattern is joined in a fixed order out from its anchor with its relationship
  types kept off the type index; clauses without an anchor, and PostgreSQL,
  plan as before. The openCypher TCK result is unchanged (2256 pass, 0 wrong).
- A backdated graph write — a `GraphNode` or `GraphEdge` whose `ValidFrom` is
  earlier than the moment it is written — no longer erases the version it
  replaces from as-of reads. The old version was closed at the stated
  `ValidFrom`, so between that instant and the write no version was visible;
  writing back a node read with `GetNode`, which returns the version's own
  `ValidFrom`, made the replaced version invisible at every instant. The
  replaced version is now also kept as a history row covering the corrected
  stretch, retracted at the moment of the write, on every write path
  (`UpsertNode`, `UpsertEdge`, the batch upserts and `ExecuteBatch`) and both
  backends. Ordinary writes archive exactly as before.

## [2.119.1] - 2026-10-03

### Fixed

- `--doctor` reported healthy local brains as failed in two cases. The
  integrity check shared the 15-second network timeout, and at the ~90 MB/s
  measured on a cluster node any brain over about 1.3 GB timed out; it now has
  its own 10-minute limit and says so if it runs out. A WAL brain in a
  directory that cannot be written, with no `-shm` file, failed with "attempt
  to write a readonly database"; it is now checked as immutable and the report
  notes it.
- The `hook_trust` field is gone from the `--doctor` report: it was the same
  sentence on every run and checked nothing.

## [2.119.0] - 2026-10-03

### Added

- MCP binary `--doctor`: JSON diagnostics for the active local or remote brain,
  binary version and auto-recall setting. Checks SQLite read-only or discovers
  remote tools with authentication; `--self-test` verifies memory persistence
  and lexical recall in a temporary database.

### Fixed

- Auto-recall on a remote-only machine no longer requires a local database.
- Session directives also run on resume and use host-independent tool names;
  they distinguish semantic relations from mere name co-occurrence.
- Codex documentation explains plugin hook discovery and required trust review.

## [2.118.0] - 2026-10-03

### Added

- **`liveview.RemoteSource(addr, token)`** — a live-view source over a shared
  brain whose address and token a host keeps in its own settings. Assembling
  one from `LoadRemote` alone gave a view that drew the graph and nothing else:
  no find, ask or Cypher across the store, no node-by-node expansion, no
  inspector, no contract. `OpenSource` now builds its remote branch with it, so
  there is one implementation. (`SourceFor` is the same for a local brain a
  host already holds open.)

### Changed

- **No violet or magenta in the live view.** `concept` was a violet and
  `person` a magenta, the ontology page's lane colours had both, and the hashed
  colour for any other node type could land in that band. Concept is now an
  ochre, person a rose, and hashed hues skip 250°–330°. Products embedding the
  view had been re-painting it through page globals to get there.

## [2.117.1] - 2026-10-03

### Fixed

- **Live view: nothing covers the title card any more.** The ☀/☾ switch sat on
  the end of the title's "ontology →" link — the card kept room for the
  collapse button only — and now has its own gutter, and goes away when the
  card is folded. On narrow screens and phones the explore bar was placed 88px
  down while the card ends near 97px, so it hid the card's last line; it now
  follows the card's real bottom edge (wrapped, folded, or moved down by the
  past banner), and the card is bounded by where the controls chip actually
  starts, which is wider on a touch screen.
- **Live view: the + and – on folded chips are centred.** They were typed
  glyphs pinned 5px from the top of chips whose height came from each panel's
  own font (29px for one, 32px for the rest), so they sat low. They are now
  drawn bars, centred by construction, in chips of one height; on a touch
  screen the chip grows to hold the 34px button. Checked in a browser from
  320px to 1344px wide, with and without touch.

## [2.117.0] - 2026-10-02

### Changed

- **The live view, redesigned.** Glass panels on a deep field with one accent;
  the explore bar is the page's main control, its results fold away once you
  open a node so the node is not under them; node types are chips with counts,
  each one a filter; the inspector leads with the record's name and a coloured
  type chip. Names are drawn on the scene — the hubs on a first look, then
  whatever is selected, hovered or lit — placed every frame from the graph's
  own projection and kept from overlapping. Focus no longer turns the rest of
  the brain into grey beads: dimmed nodes keep their hue at a fifth of its
  brightness and shrink. Bloom is calmer and the layout airier, so the core is
  no longer one white-cyan blur. Flying to a node stops at a fixed distance
  along the line of sight instead of inside the cluster, re-aims once the
  layout settles, and on a phone lands above the inspector sheet. Controls,
  activity and the contract start folded; the remembered fold layout is reset
  once (new storage key) so everyone gets the new defaults.
- **Themes, light and dark.** Three families — Space (the default), Ember and
  Mono — each with a light and a dark set for the panels and the scene, and a
  mode that is Light, Dark or Auto (follows the system, and follows it when it
  changes). The title card's ☀/☾ switches light and dark in one click; the
  controls choose family and mode; both are remembered per browser, are URL
  parameters (`?theme=ember&mode=light`), and can be sent by an embedder
  (`{type:"cortexdb:theme", theme, mode}`). Light modes turn bloom off — glow on
  a light ground only greys it — and use darker inks of the same type hues.
- **Phones.** The light/dark switch has its own thumb-sized place on the title
  card; the selected node's label stays a solid accent chip in light mode; and a
  link that opens on a node keeps that node in frame while a 2,000-node layout
  settles — the camera follows it until the simulation rests or the reader
  touches the screen — instead of showing it drift off and leaving an empty
  view.

### Fixed

- CI: `pkg/cortexdb` took 583s of `go test`'s 10-minute default under `-race`
  and coverage, and the v2.116.0 tag build crossed it. The race step now has a
  25-minute budget; the 33,000-entity placeholder test, single-goroutine and
  266s under the race detector against 4s without, skips under `-race` and runs
  in its own step without it; the as-of query-plan test seeds 60 rows instead
  of 2,000, since the planner has no statistics and the plan does not depend on
  the count. **2.116.0 has no release binaries** — its tag build timed out — but
  it is on the module proxy, so 2.117.0 ships its changes.

## [2.116.0] - 2026-10-02

### Added

- **The live view can be asked, not only looked at.** `serve_graph_3d` drew
  the store's most-connected core — 2,000 nodes of a shared brain — and
  nothing past it was reachable from the page. Its new Explore bar finds a node
  by name anywhere in the store, asks the brain a question (recall's facts,
  memories and passages, with every thing the answer names lit up and one
  click to bring the rest into view), and runs read-only Cypher as a table
  whose nodes are drawn. Any node can be expanded to its neighbours, including
  ones the core left out; hovering a node lights its neighbourhood; relations
  can be filtered by type.
- **A shared brain gets the inspector.** Clicking a node on a shared brain used
  to say the source could not look a record up. The inspector now reads it
  through the brain's own tools (`get_nodes`, `get_chunks`, `fact_provenance`):
  source document, chunk, producer, grade, when it became true, and the text it
  was drawn from.
- **Every view is a link.** `?focus=ID|name&hops=N`, `?find=`, `?ask=`,
  `?cypher=`, `?q=`, `?type=`, `?edge=`, `?explore=0`, and a Share button that
  copies the current one (the system share sheet on a phone). Embedders can do
  the same with `postMessage` (`cortexdb:focus`, `cortexdb:find`,
  `cortexdb:ask`, `cortexdb:cypher`).
- **Phones.** One-line title, the Explore bar across the top, the inspector as a
  bottom sheet, corner panels folded to chips, thumb-sized controls, 16px
  inputs so iOS does not zoom, glow and particles off by default on a small
  touch screen.

### Fixed

- Expanding a hub drew dots with no relations: `expand_graph` spent its limit
  on the memories that mention the hub, joined by bookkeeping edges the scene
  does not draw. The view now asks for the hub's relations with parameterised
  Cypher and falls back to `expand_graph` on a store without Cypher.
- Merging nodes into the scene mutated the arrays the running simulation held,
  so a tick between the merge and the library applying it met links still
  holding bare ids (`Cannot create property 'vx' on string`). Structural deltas
  had the same latent fault.

### Security

- The view stays read-only by construction: it can run only the tools on
  `liveview.ExploreTools`, a test holds every one against the catalogue's
  `Mutates`, and the callers refuse any other name before a request leaves the
  process. SPARQL is not on the list, because `knowledge_graph_query` also runs
  updates.

## [2.115.0] - 2026-10-02

### Added

- **Retrieval that walks the graph.** `retrieval_mode: "ppr"` runs
  Personalized PageRank (HippoRAG 2) from the entities a question names and
  fuses the walk with the lexical or hybrid first stage by rank. Without an
  embedder `auto` takes the walk when the question names an entity specific
  enough to start from, and stays lexical otherwise. Recall@5 through
  `SaveKnowledge`/`SearchKnowledge`, lexical → `auto`, no embedder
  (`cortexdb-bench`, full sets): 2WikiMultiHopQA 0.657 → 0.807, MuSiQue
  0.457 → 0.542, LoCoMo 0.493 → 0.495, LongMemEval 0.861 → 0.862; p95 under
  30 ms.
- **A change feed.** Every committed write to nodes, edges, triples, memories,
  knowledge and ontology schemas lands in `change_log` in the same
  transaction: commit order, exactly once, nothing from a rolled-back
  transaction, on both backends. Read by cursor (`changes_since`,
  `db.Changes`) or subscribe in-process; seven days or 500,000 events kept by
  default.
- **Inferences maintained automatically.** Delete-and-rederive over the change
  feed keeps materialized RDFS / OWL inferences current as facts change,
  asynchronously; `WaitForInference` reads your own writes. Agreement with a
  full recomputation was checked over thousands of random edit sequences.
- **OWL 2 RL keys, property chains and contradictions.** `FunctionalProperty`
  and `InverseFunctionalProperty` derive `sameAs`; `propertyChainAxiom` derives
  compositions. `disjointWith`, `propertyDisjointWith`, two values of a
  functional property and `differentFrom` against a derived `sameAs` are
  reported with the conflicting triples — on the tool, the Go API and the
  typed RPC — never resolved by guessing.
- **SHACL-AF triple rules** (`sh:TripleRule` with conditions, order and node
  expressions), run to a fixpoint, every result explainable
  (`knowledge_graph_shacl_rules`).
- **RDF 1.2 and SPARQL 1.2**: triple terms `<<( s p o )>>`, reified triples,
  `{| |}` annotations, base-directed literals, triple-term patterns and
  functions. The in-scope W3C RDF 1.2 and SPARQL 1.2 suites pass on both
  backends. N-Triples, N-Quads, Turtle and TriG are parsed in-house.
- **Read-only Cypher / GQL** over the property graph (`graph_cypher_query`):
  MATCH, OPTIONAL MATCH, WITH, variable-length paths up to six hops,
  aggregation. Zero wrong answers across 3,897 openCypher TCK scenarios; what
  it cannot do, it refuses. Every user value is bound, never spliced.
- **Leiden communities.** `build_community_hierarchy` refines Louvain's moves
  so no community is ever disconnected.
- **Binary quantization** (`IndexTypeBinary`): 1-bit codes, a Hamming
  shortlist rescored exactly. Recall@10 0.979–0.996 on real 768-d embeddings
  at 8× oversample, 1/32 of the memory; `binary_quantize` on PostgreSQL.
- **`cortexdb-bench`**: LongMemEval, LoCoMo, MuSiQue and 2WikiMultiHopQA
  through the public facade with one command, datasets pinned by sha256, every
  retrieval mode including `auto`. Baselines for v2.114.1 are in
  `pkg/eval/testdata`.

### Changed

- **`SaveKnowledge` builds the entity graph without an embedder.** It used to
  skip extraction when no embedder was set — the plugin's default — so a
  knowledge base had chunks and no entities, and every graph mode silently
  scored what lexical did. A document title that reads as a name is now an
  entity every chunk of the document mentions; a fragment of a name the caller
  declared ("Bridge" of "Bridge 01") is not extracted as an entity of its own.
- **The walk does not start from hubs.** An entity more than 10% of passages
  mention — a conversation's speaker — is not a seed; walking from one cost
  LoCoMo ten points of recall@1.
- **`graph` mode fuses by rank**, so an exact lexical match is never buried
  under graph neighbours.
- **Writes are cheaper on SQLite.** Write statements stay prepared across
  transactions, statement journals and temp b-trees live in memory, and the
  allocator keeps a reserve of slabs instead of mapping and unmapping one per
  statement. What the change feed adds to a write through the facade, p50:
  memory 22–25% → 11–18%, knowledge 32–34% → 26–30%, entities 44–45% →
  35–38%.
- MMR reranking tokenizes each candidate once and updates redundancy
  incrementally: 200 candidates 25.1 s → 5.4 ms, identical output.

### Fixed

- The change feed's `graph_nodes` triggers built an ephemeral index on every
  write: an `IN` list in their `WHEN` clause. Equalities now; trigger version 4.
- Tests no longer leave databases in the source tree (10,819 files had built
  up); every test database lives under `t.TempDir()`.

## [2.114.1] - 2026-09-29

### Fixed

- **HNSW built ten layers where it should build two or three.** `selectLevel`
  promoted a node with probability 1/2 per level whatever `M` was — mL = 1/ln 2
  instead of the paper's 1/ln M — and the `ML` field held the same wrong
  constant and was never read. At M = 16 a thousand nodes reached level 8–13;
  the upper layers were a handful of nodes wired to one another, greedy descent
  entered the bottom layer far from the query, and a vector searched for by its
  own coordinates was occasionally not found: `TestHNSWLargeScale` failed the
  v2.114.0 tag build with `vec_0` answered by `vec_378` at distance 8.0. Levels
  now follow floor(-ln U · 1/ln M), taken from `M` rather than the stored `ML`
  so a saved index builds its new nodes correctly; the same test passed 400 of
  400 runs afterwards, and a new test holds the level distribution to 1/M.
  **2.114.0 is on the module proxy but its release build failed, so it has no
  binaries; use this instead.** Everything 2.114.0 added is here.

## [2.114.0] - 2026-09-29

### Added

- **A Chinese question finds its answer without an embedder.** The word index
  saw `微信机器人为什么从来不主动给我发提醒` as one seventeen-character token and
  asked the trigram index for it as a phrase — and a trigram index cannot match
  the two-character words most Chinese is made of anyway. On a snapshot of the
  shared brain, five of six Chinese sentence queries in the golden set returned
  an empty list while the memory answering them was there. Lexical search now
  also cuts a CJK question at function words (的, 了, 为什么, 那个, …), breaks the
  rest into character bigrams, finds candidates by substring and ranks them with
  BM25, merged beside the word-index results. It covers memory recall,
  `SearchKnowledge`, `SearchTextOnly` and the `search_text` tool, on SQLite and
  PostgreSQL alike, and runs inside the authorization gate.
  A row is returned only when it matches more than one word of the question —
  two separate stretches, one stretch of four characters, or all of a one-word
  question — and at least 30% of its content characters. Measured on the real
  golden set (48 labelled queries, 7 negatives, lexical, no LLM): hit@5 0.604 →
  0.875, MRR 0.503 → 0.734, Chinese natural questions 0.50 → 0.86, and every
  Chinese negative still returns nothing. 29–46 ms per query over 20,000 chunks.
- `pkg/eval` gains `BuiltinChinese()`, an invented Chinese ops notebook with
  sentence queries and negatives, and `Dataset.Negatives` for queries that must
  come back empty. CI holds it at recall@5 ≥ 0.90 and MRR ≥ 0.85.

### Fixed

- **A keyword search ranked the best match last.** `SearchTextOnly` with
  keywords or alternate queries — and the FTS fallback behind hybrid search —
  mapped FTS5's `bm25()`, which is negative and lower for a better match, to a
  score with `1/(1+(-bm25))`: highest for the weakest match. SQL returned rows
  best first and the merge re-sorted them worst first, on both backends. This is
  the path an agent reaches by expanding keywords, which is what every tool
  description tells it to do when there is no embedder. The memory path had the
  same inversion and was fixed earlier; this one was missed.

## [2.113.0] - 2026-09-28

### Added

- **History says why a row ended.** `graph_node_history` and
  `graph_edge_history` gain `reason`, `superseded_by` and `producer`, migrated
  in place on SQLite and PostgreSQL (rows archived before this read with an
  empty reason — the store does not know, and does not invent one). Every
  archive path records one: an upsert's replaced version is `superseded` (by
  its own id), a delete is `retracted`, and the facade's own paths say
  `document_deleted`, `orphaned`, `pruned` or `merged`. Callers state their own
  with `graph.WithInvalidation`. Read back through `NodeHistory` /
  `EdgeHistory` (Go, both on `GraphStore` and the facade), `HistoryReasons`,
  and on every retracted or changed row of `graph_diff` as `invalidation`.
  Exercising every invalidation path the facade and graphflow expose: 15 of 15
  history rows carry a reason, against 0 of 13 before.
- **Entity resolution links first, merges later.** `graphflow.ResolveEntities`
  scores each candidate pair — name similarity (normalized key, or
  Jaro-Winkler capped at 0.92), shared neighbours, type agreement, and whether
  the model grouped it — and merges only at 0.95 or above. From 0.85 it writes
  a `possiblySame` edge with the evidence, graded `held` with a `_why`, so
  `contract_needs_attention` lists it for a person; grading that edge
  `verified` merges the pair on the next pass, `refused` keeps it apart. On a
  24-pair labeled fixture (12 aliases, 12 near-miss homonyms and siblings):
  false merges 5/12 → 0/12 without a model and 6/12 → 0/12 with one; aliases
  found (merged or linked) 8/12 → 10/12 and 10/12 → 11/12, of which 6 are
  merged outright. `ResolveOptions.Mode = ResolveMergeAll` (CLI
  `--merge-all`) restores the unscored behaviour.
- **`verify_claims`** (`DB.VerifyClaims`): checks (subject, relation, object)
  triples against the graph and answers supported / contradicted / absent per
  claim, with the evidence edges and their provenance and contract fields.
  Contradicted comes from the store's own notions — ontology cardinality or a
  caller-declared single-valued relation, an ended `valid_to`, `_contradicts`.
  Deterministic. On a seeded 337-claim set: supported and contradicted
  precision 1.00; contradicted recall 0.80 with the ontology alone (the misses
  are a relation the ontology does not describe) and 1.00 once declared.
- **`graph_health`** (`DB.GraphHealth`): growth per producer, degree tail,
  supersessions per day and temporal invariant violations, each reporting
  whether it fired.
- **`search_paths` — path retrieval for multi-hop questions.** Bounded
  breadth-first search between the entities a question names (or every path
  out of one), scored `decay^(hops-1)` × relation weights, each path returned
  as edges that cite their source chunks, plus the deduplicated evidence set
  in path order. MCP tool, `DB.SearchPaths`, `graph.GraphStore.SearchPaths`,
  and `ReturnPaths` on GraphRAG queries (`return_paths` on
  `search_graphrag_lexical`). Measured on a 10-question fixture (2–3 hops,
  look-alike distractors, lexical mode, k=5): full-evidence hit rate 0.50 for
  `search_text` and `search_graphrag_lexical`, 0.80 for `search_paths`;
  distractor share 0.43 / 0.36 / 0.20. On the six questions naming both ends
  of the chain, 1.00 against 0.67 at best. On the four naming one end, paths
  tie lexical search at 0.50 — with one seed the walk does not know which way
  the question goes. A homonym merged into one node (a chef sharing a
  founder's name) yields a one-hop path that outranks the true chain; only
  entity resolution fixes that.
- **Per-relation-type weight and depth.** `graph.RelationPolicies`
  (`{type: {weight, max_depth}}`, `"*"` for the rest) on `SearchPaths`,
  `expand_graph` (`relation_policies`, which also returns per-node scores and
  makes `limit` keep the best), `Neighbors`/`ScoredNeighbors` and
  `HybridSearch` (`GraphFilter.Relations`). Unset, all of them behave as
  before. Around five entities whose real facts sit among co-occurrence edges,
  down-weighting `co_occurs_with` to 0.2 and capping it at one hop took
  precision@5 from 0.40 to 0.80 and nDCG@10 from 0.88 to 1.00 — a weighting
  chosen knowing the fixture, so it shows the mechanism, not a forecast.
- **`global_search` and `build_community_hierarchy` tools.** `graphflow.GlobalSearch`
  was reachable only from Go and the `--global-search` CLI mode; it is now an
  MCP tool on `cortexdb-mcp-stdio` (local mode) and `graphflow.NewMCPServer`.
  The caller chooses it; nothing routes a query to it.
- **Hierarchical communities.** `graph.GraphStore.HierarchicalCommunities` /
  `graph.LouvainHierarchy` run Louvain with modularity gain and keep every
  level; `graphflow.BuildCommunityHierarchy` writes reports bottom-up (parents
  from children's reports) with or without a model, and `GlobalSearch` takes a
  `Level`. On a 2000-entity planted graph: 2 levels, 67 then 17 communities.
- **PageRank cache.** `rank_graph_nodes` serves scores from a side table with
  their computation time, recomputing when the graph changed (`refresh`,
  `allow_stale`, `max_age_seconds`; Go: `GraphPageRankOptions.Cache`,
  `RefreshPageRankCache`, `InvalidatePageRankCache`, `StartPageRankRefresher`).
  2000 nodes / 8770 edges: 16.4ms p50 uncached, 0.11ms p50 cached.

### Changed

- **Merges, fact supersession and graph-edit deletes leave history.**
  `MergeEntities` archives the alias's edges before repointing them, and the
  alias itself, with reason `merged` and the survivor as `superseded_by`; it
  no longer drops unrelated self-loops elsewhere in the graph.
  `graphflow.SupersedeFact` rewrites the closed fact through the graph store
  (so its open version reaches history) instead of an in-place UPDATE, and
  runs on PostgreSQL; `SupersedeFactWith` names what replaced it, and
  `TemporalFact.SupersededBy` reads it back. `ApplyGraphEdits` relation deletes
  go through `RetractEdgeAt` rather than a bare DELETE, which left no trace.

### Fixed

- **Ingesting after an as-of query failed on SQLite** with "malformed JSON":
  the as-of expression index was an unguarded `json_extract`, which SQLite
  evaluates on every insert, and chunk edges carry empty properties. The index
  is now guarded (and renamed; the old one is dropped on first use).
- `HybridSearch` breaks score ties by node id; equal scores used to come out
  in map order, differently from run to run.

## [2.112.0] - 2026-09-25

### Added

- **The property graph answers SPARQL.** The shared brain had 3,812 nodes and
  5,809 edges in its property graph and zero rows in `kg_triples`, so SPARQL,
  RDFS inference and SHACL — real features — had nothing to act on:
  `SELECT ?p (COUNT(*) AS ?n) WHERE { ?s ?p ?o } GROUP BY ?p` returned nothing.
  `FindTriples`, the one read path all three share, now also returns the
  triples the property graph implies, read-only, in graph
  `<urn:cortexdb:graph:property>`: `cxn:` nodes, `cxt:` types, `cxr:`
  relations, `cxp:` properties, and `rdfs:label` from `name` or `title`. The
  pattern is pushed down into SQL on both backends, nothing is copied, and
  deleting or inserting a projected triple is refused. On a snapshot of the
  brain, "who depends on CortexDB" is
  `SELECT ?who WHERE { ?x cxr:depends_on ?y . ?y rdfs:label "CortexDB" . ?x rdfs:label ?who }`
  and returns eleven distinct projects in 14 ms (some twice, because the
  brain stores them under two nodes — which is what `owl:sameAs` is for).
- **An OWL-RL subset**: `owl:inverseOf`, `owl:SymmetricProperty`,
  `owl:TransitiveProperty`, `owl:equivalentClass`, `owl:equivalentProperty` and
  `owl:sameAs`, each inferred triple explainable back to explicit ones. Aimed at
  what extraction gets wrong. On the brain snapshot, `cxt:host
  owl:equivalentClass cxt:Host` unifies two spellings of one type, and
  `owl:sameAs` between `node-e` and `sds-e` — one machine stored twice, each
  copy holding half its facts — gives either name all nine relations. A sameAs
  class above `max_same_as_class_size` (default 32) is reported in
  `oversized_same_as_classes` rather than materialized, on the tool, the Go API
  and the typed RPC alike.
- **SHACL constraints**: `sh:class` (following `rdfs:subClassOf*`),
  `sh:minExclusive`/`sh:maxExclusive`, `sh:hasValue`, `sh:minLength`/
  `sh:maxLength`, `sh:languageIn`, `sh:uniqueLang`, `sh:node`, `sh:not`,
  `sh:and`, `sh:or`, `sh:xone`, `sh:closed` with `sh:ignoredProperties`,
  `sh:targetSubjectsOf`/`sh:targetObjectsOf`, `sh:inversePath`, `sh:flags`, and
  constraints on node shapes, not only property shapes. Results name the
  constraint component. Recursive shapes and malformed lists are errors, not
  loops or silent passes.
- **SPARQL**: `FROM` / `FROM NAMED`, GRAPH blocks in CONSTRUCT templates, and
  the SPARQL 1.1 function library (term tests, strings counted in characters,
  numerics, dates, hashes). A per-row type error drops the row in FILTER and
  leaves the variable unbound in BIND, as the spec says, instead of failing the
  query.
- **JSON-LD 1.1 import and export** (`jsonld`, also `json-ld`), through
  `piprate/json-gold` behind a loader that performs no I/O: schema.org's
  context is answered from memory, any other remote `@context` is refused with
  an error naming it. json-gold panics on some trivial inputs; those are caught
  and returned as errors, and kept as fuzz seeds.

### Changed

- **Inference is semi-naive.** Each round joins only what the previous round
  derived, instead of every pair of triples. 2,044 explicit triples: 11.87 s →
  60 ms. 20,035: the old engine had not finished after 28 CPU-minutes; the new
  one takes 1.84 s. Output is identical to the old engine on the golden
  fixtures, and rule credit no longer depends on map order.
- SHACL treats a plain literal as `xsd:string` (RDF 1.1), checks the lexical
  form of common XSD datatypes, and reports malformed shape parameters as
  errors instead of skipping them.

### Fixed

- **`ORDER BY` on an aggregate alias sorted wrongly.** `GROUP BY ?t ORDER BY
  DESC(?n)` over the brain returned counts in the order 403, 107, 2015, 2, 1:
  ORDER BY could not see SELECT aliases in grouped queries, and compared values
  as text.
- **A literal joined into a subject position failed the whole query.** `?x ?rel
  ?y . ?y rdfs:label ?o` binds `?y` to a name whenever `?rel` is a label, and
  the query died with `rdf subject must be iri or blank node`. That row now
  simply has no solutions.
- **Deletes counted requests, not removals.** SPARQL `DELETE WHERE`/`DELETE …
  INSERT` and `DeleteKnowledgeGraph` added one per triple asked for, including
  triples that were never stored.
- **Inferred triples carried incomplete support ids**, so traces stopped at the
  first inferred step.
- **The plugin manifests were at 2.109.0** while `version.go` said 2.111.1, so
  CI failed for 2.110.0 and 2.111.x and no release binaries were built; the
  plugin's MCP launcher kept downloading the 2.109.0 server. All four now read
  2.112.0.

## [2.111.1] - 2026-09-15

### Fixed

- **The checkpointer added in 2.111.0 raced; use this instead.** `Init` is
  called more than once on the same store — `hindsight.New` opens the database,
  which inits it, and then inits the vector store again itself — and the second
  call assigned fresh channels over the ones the running checkpoint goroutine
  was selecting on. A write under the store lock against a read holding no lock
  at all, plus a goroutine left running on channels nobody could reach to stop
  it. Seventeen tests in `pkg/hindsight`, none of them about checkpointing,
  failed under `-race` with that report. The goroutine now closes over its
  channels as locals so there is no shared read to race, a second start returns
  early, and `stopWALCheckpointer` releases the lock before waiting. **2.111.0
  is on the module proxy and should be skipped.**

## [2.111.0] - 2026-09-15

### Fixed

- **The write-ahead log no longer grows until the disk is full.** An Athanor
  instance ingesting 248 documents produced an 11 GB `brain.db-wal` against a
  636 MB database, filled the 57 GB disk it shared with other services, and
  took an unrelated application's PostgreSQL down with it — every connection
  answering `FATAL: could not write init file: No space left on device` for
  ninety seconds. Nothing in this package had ever called `wal_checkpoint`.
  SQLite's own automatic checkpoint could not cover it: it can only copy out
  frames older than the oldest reader still holding a snapshot, and the store
  keeps a pool of 25 connections with 10 idle for two hours, so during any
  sustained ingest there is always a reader in flight and every automatic
  checkpoint gives up silently. The store now checkpoints on a clock of its
  own, every 30 seconds, with `TRUNCATE` rather than `PASSIVE` — the problem is
  the file's *size*, and `PASSIVE` leaves the log as long as it ever got. A
  checkpoint that cannot get its locks reports busy rather than failing, and
  waits for the next tick. In-memory stores start no goroutine.

## [2.109.0] - 2026-09-14

### Added

- **`UpdateDocument` is on the `Store` interface.** Both backends had it and
  the interface did not, so a caller holding the store as an interface — which
  `cortexdb.DB` does — could create a document and delete one but never write
  over one. Replacing a record meant delete then create, with a window where it
  is neither. Same gap `parity_aggregate_test.go` was written about; the test
  is next to it and runs on both backends.

## [2.108.0] - 2026-09-14

### Fixed

- **Deleting a document's graph is one transaction.** The removal is four write
  phases — the relation edges, an UPDATE per entity that survives with one
  fewer source, the nodes, and the vector index sync — and they ran as four
  independent statements. A failure between any two left a graph half removed:
  edges gone with their endpoints still standing, or some entities detached and
  the rest still naming a document that is no longer there. Nothing downstream
  can tell that state from a real one, and running the delete again does not
  restore what the first pass took. The reads now happen on the same
  transaction as the writes, so the plan and what it acts on cannot drift; the
  index sync stays outside and after the commit, because syncing ids whose rows
  a rollback restored would be worse than not syncing at all.

## [2.107.0] - 2026-09-14

The provenance a machine-written fact could not carry.

### Added

- **`RDFTriple.Provenance`, merged onto the edge a triple becomes.** An import
  wrote triples into the brain carrying nothing about their origin, so a fact
  read out of a live database could not say which plan was signed to read the
  table, which run read it, or who authorized it — all of which were in the
  ledger and on none of the facts. The map lands where the knowledge contract is
  already read from: edge properties. It cannot overwrite the keys that say what
  the triple *is*, because a fact able to relabel itself as inferred through the
  field meant to hold it to account is worse than an anonymous one. It is not
  part of the triple's identity, so re-importing a row refreshes its provenance
  rather than forking the fact.
- **`importflow.WithProvenance`**, stamped by the one sink every triple reaches
  — column mapping, relation mapping and AI extraction all pass through it, and
  a path that forgot would produce exactly the anonymous facts this ends. The
  importer invents nothing: what a row out of somebody's production database is
  worth is the calling product's decision, not this code's.

### Changed

- **`uncited_facts` counts a named source as an accounting.** A live-database
  row and a rule-derived edge have no chunk anybody can quote and are not
  anonymous; reporting them would bury the facts that genuinely came from
  nowhere under one line per imported row.
- The two copies of the RDF edge-property construction are one function. They
  were identical, and a provenance legible through one path and not the other
  would have been a hole nobody could see.

## [2.106.0] - 2026-09-14

Two defects on the provenance surface, both found by calling it over a port
rather than from a test, and the flag that makes an upgrade checkable.

### Added

- **`-version` on all three binaries.** `cortexdb-grpc -version` prints the
  version of the file on disk and exits, opening no database, reading no `.env`
  and touching no network — it answers on a machine where nothing is configured
  yet, which is the machine you are on right after copying a binary into place.
  `-health` already reported the version of a server that is *running*; there
  was no way to ask the artifact, so telling two builds apart meant comparing
  checksums against a build you still had lying around. Both spellings work,
  and only as the first argument: in `-db --version` the second word is a
  database path, and a typo that printed a version and exited 0 would look,
  under systemd, like a unit that came up. `cortexdb-mcp-stdio` and
  `cortexdb-connector-mcp` answer the same way, since an MCP client cannot be
  asked what it launched.

### Fixed

- **`fact_provenance`, `uncited_facts` and `vector_dimension_repair` were
  listed on every door and reachable through none of them.** All three were in
  the catalogue `/v1/tools` publishes, classified for authorization, and
  registered with the MCP server — and absent from `GraphRAGToolbox.Call`,
  which the REST port and the gRPC `ToolCall` both dispatch through. A caller
  could read the catalogue, build a valid request from the published schema,
  and get back `unknown tool: fact_provenance`. Asking a record where it came
  from is what the store is for, and over both ports it could not be asked.
  Registering a handler with the MCP server consumes the definition, so it
  looked like registration and nothing downstream noticed the missing case. A
  test now walks the catalogue and calls every name with malformed input: a
  known name fails at decode, an unknown one says so, and no tool executes.
- **The sweep for uncited facts failed on any brain with a document in it.**
  `ingest_document` writes one `has_chunk` edge per chunk, and an edge written
  with no properties gets the empty string in that column. The empty string is
  not JSON, so `json_extract` on it is an error rather than a null, and one
  such row failed the whole query — with `malformed JSON`, naming neither the
  column nor the row. Every brain in use has ingested a document, so the tool
  that reports which facts cannot say where they came from was itself the one
  tool that could not be run. The dialect already carried `JSONTextGuarded`,
  written for exactly this and used by five other queries. Structural edges
  (`has_chunk`, `mentions`) are excluded while the query is open: a document
  owning its chunks is not a claim anybody can cite a source for, and one per
  chunk of every document would bury the facts that are actually uncited.

## [2.105.0] - 2026-09-14

Four capabilities built from parts the engine already had and nothing could
reach, and the three defects that turned up in them once real data was pointed
at them.

### Added

- **`graph_schema` and `graph_property_values`** report the graph's *observed*
  schema: which node and edge types exist, which node-type pairs each edge type
  really connects, which property keys each type carries, and what values one
  key actually takes. `ontology_get` answers the *declared* schema, which is
  opt-in and absent on exactly the graphs built by extraction whose shape nobody
  knows. The answer carries a rendered `text` form, because it is read in a
  prompt rather than parsed. Without the value distribution, a filter written
  from a key's name — `color == "black"` against rows that all say `BLK` —
  returns nothing and reads as a fact.
- **`rank_graph_nodes`, `predict_graph_edges`, `graph_statistics`** open the
  rest of `pkg/graph/graph_algorithms.go`, which had no callers at all.
  `predict_graph_edges` reports `tied_at_top`: several unrelated nodes tied at
  exactly 1.0 means the graph's vectors cannot tell them apart, which is what
  short lexical hashes do to short entity names, and a maximal score is the
  worst possible way to say "no signal".
- **`disambiguate_mentions`** resolves an ambiguous name against the other names
  given with it — shortest paths between their candidates, each rendered as a
  sentence with its edge ids, ranked by that support. The deterministic four
  steps are the library's and the choice is the caller's, so it works with no
  model at all and can always explain itself. A mention nothing connects to is
  unresolved rather than the nearest string match, and a bound that stopped the
  search is reported apart from an absence.
- **`ChunkWindow` / `chunk_window`** widens a hit to its neighbours in the same
  document. Off by default; neighbours are context and never hits — not scored,
  not reranked, never displacing a match, tagged `+context` in the assembled
  text. Applied on every retrieval path, not only the one it was written
  against: an option honoured on one path and ignored on another is worse than
  no option.

### Fixed

- **`PredictEdges` let its two signals cancel instead of agreeing.** It averaged
  vector similarity with a structural score that is strictly below 1, so purely
  structural evidence sat permanently under the 0.5 a prediction must clear — a
  pair sharing every neighbour and nothing else could never be returned — and
  averaging *lowered* a strong similarity, so sharing neighbours demoted a pair.
  The branch only ran when there was structure, which made structure strictly
  harmful. Replaced with a noisy-OR: the structural term closes part of the gap
  the similarity leaves to certainty, so it can only raise a score, and
  structure alone still carries a pair over the line.
- **`EdgeShapes` and `EdgeEndpointPairs` could not be filtered to the untyped
  bucket.** Their SELECTs present a NULL type as `""`, which is the only name a
  caller is given for it, and their WHERE compared the raw column, where NULL
  equals nothing — so asking for rows the previous call had just counted
  returned an empty answer. `NodePropertyKeys` already filtered on the
  COALESCE'd expression; the other two now do too.
- **`PredictEdges` silently dropped edges it could not scan**, leaving holes in
  the topology every prediction is computed from.

## [2.104.0] - 2026-09-14

Four capabilities existed in the engine and could not be used, and looking at
why turned up three defects in them.

### Fixed

- **`RangeSearch` returned opposite things on the two backends.** SQLite put
  the distance in `Score` and sorted ascending; PostgreSQL delegated to
  `Search`, so `Score` was a similarity sorted descending — and it silently
  capped at a thousand rows and accepted a non-positive radius. The same call,
  a different answer, no error. `Score` is the similarity on both now,
  descending; `TopK` 0 means every match; `radius <= 0` is refused.
- **Its distance conversion guessed the metric from the sign of a score.**
  Cosine runs −1..1 and embedding vectors routinely have negative components,
  so cosine −0.5 came out as distance 0.5 instead of 1.5: the whole negative
  half of the range scored as nearer than it was, and a radius of 0.5 quietly
  admitted vectors anti-correlated with the query. Orthogonal — exactly 0 —
  came out as distance 0, the nearest possible, and passed every radius. The
  conversion now subtracts from the metric's own fixed point,
  `similarityFn(query, query)`, which needs nothing to know which metric it has.
- **`SearchWithFacets` had never worked with a facet on SQLite.** Its query
  LEFT JOINs `collections` onto `embeddings`, both tables have a `metadata`
  column, and the facet conditions named it unqualified — so every facet filter
  and every `opts.Filter` failed with "ambiguous column name: metadata" before
  a row was read. The method worked only when asked to filter nothing.
- **Aggregation names went into SQL unchecked.** `order_by` was pasted raw
  after `ORDER BY`, so an arbitrary expression ran; a `Filters` key could close
  its own `json_extract` and reopen the next one, turning the predicate into a
  tautology — a count scoped to a value nothing had returned the whole table.
  Names are now checked fail-closed before they reach the statement, by two
  rules: one for names that stay inside a JSON path (dots and hyphens stay
  legal, because `user.id` and `content-type` are real keys) and a stricter one
  for names that become bare SQL.
- **A Postgres test leaked a database per run.** A test function's defers all
  run before any `t.Cleanup`, so `defer admin.Close()` shut the connection the
  cleanup needed to drop its database, and the drop discarded its error.

### Added

- **`BatchRangeSearch`, `SearchWithFacets`, `Aggregate` and `VectorAggregate`
  are implemented on PostgreSQL and promoted into the `Store` interface.** They
  were `*SQLiteStore`-only and outside the interface, so nothing holding a
  `Store` — `pkg/cortexdb.DB` holds one — could reach them, and anything that
  did would break under PostgreSQL.
- **`VectorAggregate`** reduces the vectors themselves: centroid, geometric
  median (Weiszfeld with the Vardi–Zhang step for a coincident iterate), and
  the **medoid** — the stored record closest to all the others in its group,
  returned with its text. It answers "which of these near-duplicates is the
  canonical one" and "what is this cluster about" arithmetically, with no model
  in the loop.
- **Facade and tools.** `RangeSearchVector` / `RangeSearchText`, `Aggregate`,
  `SearchWithFacets`, `VectorAggregate`; tools `search_vector_range`,
  `aggregate_metadata`, `representative_records`. The range tool reports
  `truncated`, because a trimmed range answer is otherwise indistinguishable
  from a complete one and decays into an unlabelled top-K.

### Changed

- `SKILL.md` had two unresolved merge-conflict markers committed into it,
  swallowing the decision-ledger and declared-inference sections into each
  other. Resolved as the union; both features shipped.
- `gofmt` had never been run on thirty files, so `gofmt -l .` could not come
  back clean and was useless as a gate.

## [2.103.0] - 2026-09-12

### Fixed

- **`--export-memory` writes `source` and `expires_at` into the frontmatter.**
  It wrote scope, namespace, importance and created_at and dropped `source`,
  so from the export a verbatim transcript turn and a distilled experience
  were indistinguishable — both carry the same `harness-<timestamp>-<n>` id.
  A deletion plan drawn from such an export would have taken 387 experience
  memories along with the noise; it was caught only because the plan was read
  before it was applied. Absent fields stay absent.

## [2.102.0] - 2026-09-12

A bulk delete of 3390 memories, done through `memory_delete`, left 1097
`memory:<id>` graph nodes pointing at rows that no longer existed. Nothing could
remove them: `delete_entities` only knows `entity:` nodes, and the junk prune
only looks at entities.

### Fixed

- **`DeleteMemory` retracts the memory's graph node.** A memory saved with
  entities has a `memory:<id>` node and mention edges; deleting the row now
  retracts them in the same call (archived, so still readable as of before the
  delete). The response says whether a node went (`graph_node_retracted`). A
  memory that never had a node costs one no-op statement.

### Added

- **`PruneDanglingMemoryNodes`**, wired into `--graph-cleanup` as its own stage
  and selectable alone with `--dangling-only`, retracts `memory:` nodes whose
  row is gone. Separate from the junk-entity prune so a brain can be repaired
  after a bulk delete without also deciding what to do about its entities.
  `--dry-run` names every node it would take.

## [2.101.0] - 2026-09-12

A shared brain grew past what its own listing tool could return. `memory_list_all`
answered with `truncated: true` and no way to continue, so `--export-memory`
wrote zero files and the brain had no backup path at all. Both bulk listings now
page.

### Added

- **`memory_list_all` and `graph_list_all` take a `cursor`** and return
  `next_cursor` alongside `truncated`. The cursor is opaque and keyset-based,
  never an offset: a shared brain is written while it is read, and an offset
  silently skips records inserted behind it. It is also tagged with the listing
  that produced it, so feeding one listing's cursor to the other fails loudly
  instead of returning a plausible empty page.
- **`graph_list_all` gained `order`.** The default still returns the
  most-connected core, which is what makes a large graph renderable, and sets
  `truncated` alone — degree ranking has no stable page boundary to resume from.
  `order: "id"` walks the whole graph in a stable, resumable order. In that mode
  a page's edges may reference nodes on a later page; the subgraph is complete
  once the walk finishes. Any other `order` value is now rejected rather than
  silently falling back to the ranked mode.

### Changed

- **`defaultMemoryListLimit` drops from 5000 to 500.** The old default was a
  promise the transport could not keep: 5000 records exceed the 4 MiB gRPC
  message limit, which gives out near 1100. The cursor, not a large limit, is
  how a caller gets everything.
- **`--export-memory` (and `--memory-html`, `--memory-usage`, `--sync-memory`)
  walk the cursor** through one shared remote-fetch path. Against a server too
  old to page they now fail loudly naming the cause, rather than writing a
  partial backup that looks complete.

### Fixed

- **The keyset comparison was wrong on PostgreSQL and silently dropped rows.**
  `created_at` is SQLite TEXT at one-second granularity but a microsecond
  `TIMESTAMP` on PostgreSQL, and the cutoff was formatted with a layout carrying
  no fractional seconds. A row inside the cutoff second was then neither `<` nor
  `=` it, so it was skipped on every page, forever, with no error. The binding is
  now per-dialect, and a parity test seeds sub-second instants to hold it.

## [2.100.0] - 2026-09-09

An example that put vectors, the knowledge graph and the ontology over one
estate found five defects at the seams between them. All five close here.

### Fixed

- **Naming an object no longer erases what is known about it.** An entity upsert
  replaced the property map, so a caller that only named an object — a document
  declaring the entities it mentions, which carries identity and nothing else —
  wiped every domain property an earlier, fuller write had established. Silently,
  with the ontology's required-property check still passing because the primary
  key was the one thing a mention carried. An upsert now updates: a property the
  request names wins, one it leaves out survives, and there is no way to remove a
  property by omitting it (use `delete_entities`). Three write paths had the bug —
  the toolbox upsert, declared entities in `SaveKnowledge`, and the extractor's
  mentions on both knowledge paths — and they share one merger. The prior read is
  outside the write transaction, so the rule holds against committed state, not
  against a concurrent writer; a property an ontology revision no longer declares
  survives on rows written before the change.

- **A strict ontology no longer blocks embedder-backed `SaveKnowledge`.** The
  built-in extractor types everything it finds `entity`; that literal was validated
  as an ordinary object type against a schema with no reason to declare it, so a
  governed ontology and embedder-backed knowledge were mutually exclusive and the
  error blamed the user's schema for a name the library had chosen. The bookkeeping
  type is exempt unless a schema declares it and means something by it. The
  `upsert_entities` and `SaveKnowledge` entity doors still validate a caller-typed
  `entity`; an extractor supplied by the caller can write a bare name-keyed node
  under strict enforcement, and the guide now says so.

- **Search hits name the typed objects their chunks mention.** The chunk-to-entity
  lookup matched `node_type = 'entity'` — what an untyped entity is stored as — so
  under an ontology, where an entity's node type is its object type, every hit came
  back with an empty entity list for exactly the users who had adopted the schema.
  It follows the mention edge now. And it runs in auto mode: naming what a chunk
  mentions had been gated behind the decision to run graph *expansion*, which a
  lowercase question never triggers. An explicit lexical mode or `disable_graph`
  still leaves the graph untouched.

- **`vectorized` is embedded on write.** The flag was stored, validated and
  inherited, and nothing ever embedded the property, so the `nearest_neighbors`
  object-set predicate compared a real embedding of the query against lexical hash
  vectors and matched nothing. With an embedder, an object whose type declares a
  vectorized property gets its node vector from the embedding of that text (name
  first), in one batch per write. Without one the node keeps its lexical vector and
  the write goes through; a dimension that does not match the store's is refused.

- **`GetNodesBatch` chunks its ids** (999 per query). It bound one placeholder per
  id and never chunked, so an ingest of more than 32766 entities failed with "too
  many SQL variables" on every path that read prior state.

### Added

- **Hybrid results keep what each retriever thought.** Reciprocal rank fusion at
  k=60 scores the top three results 1/61, 1/62 and 1/63 whatever the corpus — right
  to order by, wrong to read, and until now the only thing kept. Every chunk carries
  `VectorScore`/`VectorRank` and `LexicalScore`/`LexicalRank` (rank 0 means that
  retriever never returned it); a `KnowledgeSearchHit` carries its 1-based `Rank`
  and the best of each score. The fused order is unchanged.

- **Example `18_vector_graph_ontology`** — the three layers over one replicated
  storage estate, and the seams between them in both directions: a passage's chunks
  walk to the objects they mention, an exact typed set walks back to the prose about
  it, and retrieved passages become a `static` object set intersected with a graph
  traversal. Runs lexically with no model; with an embedder, `nearest_neighbors`
  answers over a vectorized property. Its README records the five defects above.

## [2.99.0] - 2026-09-06

### Added

- **Point-in-time reads.** `graph_nodes` and `graph_edges` carry `valid_from`,
  `valid_to`, `recorded_at` and `retracted_at`, added by `ALTER TABLE` under the
  existing duplicate-column guard with defaults that make every existing row current —
  a brain that predates this opens and reads exactly as it did. Default reads are
  unchanged; as-of is opt-in.

- **Delete becomes retraction.** `DeleteNode`/`DeleteEdge` and the delete tools keep
  their names and their observable current behaviour, but the row stays with
  `retracted_at` set, so an as-of read from before that instant still sees it.
  `vacuum_graph` is the single operation that removes anything permanently: it refuses
  without a cutoff and offers a dry run.

- **`graph_snapshot`, `graph_diff`** (reads) and **`vacuum_graph`** (a write), plus
  `GraphDiff(t1, t2)` reporting what was added, retracted and changed between two
  instants, and an Allen interval helper over `(valid_from, valid_to)` — how two claims
  about one subject state that the runbook's validity ended where the incident's began.

## [2.98.0] - 2026-09-05

The brain could say how it knew a fact. It could not say why anything was done,
and it could derive only the one rule shape we shipped. Both close here.

### Added

- **Decisions as first-class records** (`RecordDecision`, `DecisionChain`,
  `Precedents`, `DecisionsBy`; tools `decision_record` / `decision_chain` /
  `decision_precedents`; gRPC `DecisionService`). A decision is a graph record —
  `decision:<id>` with `based_on`, `about` and `supersedes` edges — so a chain is a graph
  walk and every existing tool sees it. It carries the contract: a named actor signed
  it, so it is `verified`, `_producer=human`, `_by=<actor>`; `contract_tally` counts it.
  A premise that is an edge is anchored on that edge's subject node and named exactly
  in `premise_edge_id`, because `graph_edges.to_node_id` is a foreign key both backends
  enforce; the chain reports the *edge's* grade. Unknown premise, empty actor, or a
  supersedes target that does not exist: refused before the first write.

- **Rules a user declares** (`pkg/graph/rules*.go`; `RuleDefinition` with text
  `IF p(?x,?y) AND q(?y,?z) THEN r(?x,?z)` or structured when/then; tools `rules_save` /
  `rules_list` / `rules_delete` / `rules_apply` / `rules_explain`). Semi-naive forward
  chaining to a fixpoint; derived edges carry `rule_id`, `rule_text`, `support_edge_ids`
  and a confidence; re-running derives nothing new; `inference_explain` shows the chain.
  Hard caps (16 iterations, 50 000 derived edges, overridable) that **write nothing** when
  hit and say so. Literal terms match by node id, then `Type:Name`, then bare name; a term
  matching nothing is reported in `unresolved_terms` rather than matching nothing quietly.
  Rules persist in a `kg_rules` table, not as graph nodes: a rule is configuration for the
  engine, not a fact about the world.

### Changed

- **`apply_inference` runs through the rule engine** — its two-hop request is one rule,
  and the old join is gone, so there is one derivation path. Tool name, arguments and
  provenance unchanged; the response gained `unchanged_edge_ids`.

## [2.97.0] - 2026-09-05

### Changed

- **REST tool calls are classified per tool**, from `ToolDefinition.Mutates`, the same
  way the gRPC side already was. Until now `POST /v1/tools/{name}` was a write for every
  name, so a read-only key could not call `contract_tally` — a read-only auditor unable to
  read the audit, found the first time the doors were run with one. An unknown tool name
  is refused by the policy, naming it; a confined key is refused every tool, because a
  tool's arguments are opaque JSON no scope can be checked against (gRPC's rule too).
  `knowledge_graph_query` is narrowed to a read when the executor's own parser says the
  query does not mutate, so a read-only key keeps `SELECT` on both ports and the two
  ports cannot disagree.

## [2.96.0] - 2026-09-05

### Added

- **`liveview.SourceFor(db, describe)`** — a live view over a brain the caller already
  holds open. `OpenSource` opens its own database from the environment; a process
  writing through a `*cortexdb.DB` should not open the same file a second time to draw
  it. The Source does not close what it did not open.

## [2.95.0] - 2026-09-05

### Added

- **`rpcserver.AuthInterceptor`** — the policy `NewWithPolicy` installs, as a value a
  process can put in a `grpc.Server` of its own. `Register` attaches the services;
  this attaches the authorization; together they are `NewWithPolicy` without the
  server it constructs. For the process that mounts `cortexdb.v1` beside other
  services on one listener, where the alternatives were to rewrite the policy or to
  drop it.

## [2.94.0] - 2026-09-05

### Added

- **`liveview.New` and `(*Server).Handler`** — the live graph view as routes a process
  mounts on a mux it already owns, instead of a loopback listener of its own. `Start`
  is now `New` plus a listener and behaves exactly as before. The pages fetch by
  relative path, so the handler works under a prefix (`/graph/`, stripped, trailing
  slash). Needed by a process that serves the brain as one page of a larger service
  and should not run a second HTTP server on a second port for one route.

## [2.93.0] - 2026-09-05

The knowledge contract: what a record must carry to be trusted and explained,
and the three ways to read it back.

### Added

- **The contract itself** (`pkg/cortexdb/contract.go`, and the design in
  `docs/superpowers/specs/2026-09-05-knowledge-contract-design.md`). Every
  producer that writes into this store — alchemy, argus, DataIntelligence, the
  memory tools — answers the same two questions about each record: how do I
  know this, and how sure am I. Each answered in its own words and the
  vocabularies shared none. This is the one vocabulary, as reserved
  `_`-prefixed metadata keys on the maps the store already carries. No RPC
  changed to add it.

  `_grade` is closed and five-valued — `verified`, `self_consistent`,
  `asserted`, `held`, `refused` — and answers only "by what kind of thing is
  this established". The producer's own finer word rides in `_state`
  untouched, which is what keeps `_grade` from flattening distinctions each
  producer documents at length. `_why` is required whenever a record is held
  or refused: a refusal without a reason is noise the reader will delete.
  `ValidateContract` is a library call a producer makes before it writes; the
  server stores what it is given.

- **`ContractTally` and `NeedsAttention`** (`pkg/cortexdb/contract_query.go`)
  read it back. The tally's two uncomfortable fields are why the type exists:
  `Untagged` is every record nobody graded, which on a real shelf is the
  largest number and whose omission turns a five-bar chart over 3% of the data
  into one that looks like all of it; `Unknown` is every grade the contract
  does not define, which means a producer is writing something wrong and must
  not be folded in with producers writing nothing.

- **`PropertyCounts` and `RecordsWithProperties`** (`pkg/graph`), the
  domain-neutral half: group and list nodes *and edges* by any top-level
  property. `GraphFilter.Properties` could already narrow a search to one
  value; nothing could count them and nothing saw edges at all, so callers
  wanting the breakdown wrote SQL over both tables themselves.

- **`contract_tally` and `contract_needs_attention` over MCP**, both reads. A
  tool that could write a grade would let something mark a record verified
  with nothing having checked it.

- **`ContractService` over gRPC** (`proto/cortexdb/v1/contract.proto`), for the
  consumers that are not in this process: a Rust one and a separate Go one
  that must not open the same database file.

- **The live view says how much of what it shows stands on what.** A panel with
  the tally and the records waiting for a person, on its own fifteen-second
  timer through its own endpoint, and not fetched at all while folded — the
  structure poll runs every two seconds and this is two aggregate scans.
  `?contract=0` turns it off.

### Fixed

- **`scripts/gen-proto.sh` vendors the protos into the Node client too.** They
  were copied by hand, which is how that copy came to be missing an RPC
  `proto/` had had for a while: a vendored copy nothing syncs is a stale copy.

## [2.92.1] - 2026-09-03

### Changed

- **`pkg/liveview` `?spin=N`** is a pace, not a switch: `spin=1` is the Orbit
  button's own pace (a turn every forty-six seconds, which on a dense graph
  reads as standing still), `spin=4` a turn every twelve. Any positive value
  starts the orbit.

## [2.92.0] - 2026-09-03

The live view could be embedded but not configured: an application putting the
graph inside a dial got the graph and the five control panels drawn over it,
with no way to ask for the graph alone. Every switch on those panels is now
also a query parameter — `?panels=0`, `?spin=1`, `?glow=0`, `?flow=0`,
`?bg=RRGGBB`, `?fit=0` — read once on load and applied where the switch lives.
They set starting positions, not locks: a drag still stops the orbit and a
button still flips its switch.

### Added

- **`pkg/liveview` URL options.** `panels=0` hides the control panels and the
  boot splash; `spin=1` presses Orbit once the layout has settled; `glow=0` and
  `flow=0` start with bloom and link particles off; `bg=RRGGBB` sets the scene
  and page background; `fit=0` stops the camera re-framing after the first fit,
  for an embedder whose frame must not jump.

## [2.91.0] - 2026-09-03

CortexDB could be run as a service, but not operated as one, and its own headline —
many agents on one shared brain — was enforced by a single token that gave every agent
full read/write over every other agent's memory. This release is that gap.

### Added

- **A REST API** (`pkg/httpapi`, served by `cortexdb-grpc -http-addr`). Nineteen endpoints
  over the *same facade calls* the gRPC handlers make, standard library only. `POST
  /v1/tools/{name}` reaches the whole toolbox, so every current and future tool is
  curlable. Existing gRPC users are unaffected; the point is that evaluating CortexDB no
  longer starts with installing Go or generating a client.

- **Scoped API keys** (`pkg/authz`). A key carries a clearance (read-only / read-write)
  and a scope confining `user_id`, `scope`, `namespace`, `collection`. Both ports read the
  same key file and a denial reads identically on either. The single `CORTEXDB_GRPC_TOKEN`
  deployment keeps working unchanged, as one implicit full-access key.

  Two design choices are load-bearing. Method classification is an explicit table with a
  test that walks the registered gRPC service descriptors and fails on any method missing
  from it — a `HasPrefix(method, "Delete")` heuristic would silently misclassify the next
  RPC somebody adds, and a write passing as a read is the failure this exists to prevent.
  It earned itself immediately: it caught `AdminService/Backup`, added in the same release
  by different work. Row confinement rejects rather than narrows — a caller that asked for
  something it may not have is told, not handed a quietly different answer.

  This is not transport security. The gRPC transport is plaintext by design; scoped keys
  reduce the blast radius between cooperating agents on a trusted network, and change
  nothing for someone reading the wire.

- **Observability** (`pkg/observability`, `rpcserver.MetricsInterceptor`). Prometheus text
  format, expvar, and a tracing interface this repo owns with a no-op default — all
  standard library, no `client_golang` and no OTel in `go.mod`, for the same reason no LLM
  SDK is in `pkg/`. Labels are bounded to gRPC method and status code, asserted by a test
  that walks the exposition and fails on any other label name. Metrics wrap authorization
  rather than sitting inside it, so denials are counted: on a server whose new feature is
  scoped keys, an empty `errors_total` under a brute-forced token was the wrong answer.

- **`AdminService.Backup`** and `db.Backup`, confined to a configured backup directory
  (absolute paths, `..` and re-entering traversals refused). `SQLiteStore.Backup` had
  existed since long before and been reachable from nowhere. Backends declare it through
  an optional `core.Backupper` rather than `BrainStore`: a brain on PostgreSQL is complete
  without a self-backup, because its backups are `pg_dump` and a retention policy.

- **`QuerySource`** (`pkg/cortexdb`): an external search engine — Meilisearch, Weaviate —
  as one fused retrieval lane without becoming the storage. A source returns ids and
  scores; content is hydrated from the brain, so a source cannot inject text CortexDB
  never stored and a stale id is dropped rather than fabricated into a result.
  `examples/17_query_source` implements it against a real Meilisearch in plain `net/http`.

- **`deploy/`**: a hardened systemd unit, a container image whose `HEALTHCHECK` is the
  server binary itself (`cortexdb-grpc -health`), a compose file, and the backup, upgrade
  and port-override notes. Verified by installing it on Ubuntu 24.04 with systemd 255.

- **`graph.SPARQLMutates`** reports whether a query would change the graph, by asking the
  executor's own parser. It exists so authorization can tell a SPARQL read from a SPARQL
  write without maintaining a second parser that has to agree with `ExecuteSPARQL` forever.

- **`ToolDefinition.Mutates`**, set on all 61 tools, so `CallTool` is authorized by the
  named tool rather than by a blanket classification. Without it a read-only key could
  call no tool at all, including search — which made read-only useless for the MCP client
  path, since it proxies everything through `CallTool`.

### Fixed

- **A confined key could overwrite and steal another user's rows by naming their id.**
  `SaveMemory`, `SaveKnowledge` and `UpdateKnowledge` are upserts: the request carries the
  caller's own `user_id`, so the scope check passed, while the id named somebody else's
  row. For memory the `ON CONFLICT DO UPDATE` behind it also re-homed the row into the
  caller's bucket, so the theft erased the owner field that would have caught it. Found by
  demonstrating it against a running server. Present only in unreleased commits; no
  released version enforced scopes at all.

- **`SQLiteStore.Backup` interpolated its destination into `VACUUM INTO '%s'`.** The
  driver executes multiple statements from one string, so a filename of
  `x'; DROP TABLE embeddings; --` dropped the table and returned a nil error. Latent while
  nothing exposed the path; the new backup RPC would have made it remotely reachable. Now
  a bound parameter.

- **`.dockerignore`'s `*.db-wal` did not cross a `/`**, so `pkg/`'s test databases were in
  every build context: 3.34GB and 47.6s of it, now 214.82kB and 0.3s.

### Changed

- **`QuerySparql` and `knowledge_graph_query` are writes by default**, narrowed to reads
  per call when the parser says the query does not mutate. CortexDB's SPARQL subset
  executes `INSERT DATA` and the `DELETE` forms, so a tool named "query" can rewrite the
  graph. A read-only key keeps `SELECT`, `ASK`, `CONSTRUCT` and `DESCRIBE`.

- **A confined key can now reach its own rows by id.** `GetMemory`, `UpdateMemory`,
  `DeleteMemory`, `GetKnowledge` and `DeleteKnowledge` check ownership against the stored
  row instead of refusing outright. A forbidden row and a missing one answer identically,
  so ids cannot be enumerated to map the brain.

- **PostgreSQL + pgvector as a storage backend is documented**, which it was not. The
  capability shipped earlier; a `postgres://` DSN moves the whole brain, and 104 opt-in
  parity tests cover it.

## [2.90.0] - 2026-09-02

### Added

- **`GraphFilter.Contains`** matches a property's text case-insensitively, ANDed with
  everything else on the filter. `Properties` is how a caller names a batch it already
  knows; this is how a search enters the graph at all. The fold is spelled `LOWER()` on
  both sides rather than left to `LIKE` — SQLite folds ASCII case and PostgreSQL does not
  — and the needle is escaped, because `_` and `%` are ordinary characters in a name and
  unescaped they are wildcards: searching a schema for `user_id` also matched `userxid`,
  and searching for a literal `%` returned every node in the store.

- **`ListNodes`** is `GetAllNodes` without the vector column. A vector is the largest
  thing on a node, and enumerating the types in a 400k-node import through `GetAllNodes`
  moves a gigabyte of embeddings across the driver and decodes every one of them in order
  to count names. Nodes come back with a nil `Vector` rather than an empty one.

  `ListNodes`, `GetAllNodes` and `CountNodes` share one WHERE builder, so a list and the
  count printed beside it cannot come to disagree about which rows matched.

## [2.89.0] - 2026-09-02

### Added

- **A read can now name the batch it wants.** `GraphFilter` gains `Properties` and `Limit`,
  and `GetAllNodes` honours both. `graph_nodes` is shared by everything on the machine, and
  the only filter a read had was `node_type` — but a type name is not a batch. An importer
  that wrote a thousand `Person` nodes on Tuesday and a thousand more on Friday could ask
  for all two thousand or for none, even though it had stamped which run each row belonged
  to on the way in.

  `Properties` matches top-level string fields in a node's `properties` JSON, ANDed, read
  through the guarded dialect helper: `properties` is a TEXT column, a node written without
  any carries the empty string, and an unguarded read of that raises "malformed JSON" on
  SQLite and "invalid input syntax" on PostgreSQL — failing the whole statement over one
  unrelated row, so a caller asking about their own batch would be told their batch was
  empty.

- **`CountNodes`** reports how many rows match a filter, deliberately ignoring its `Limit`.
  It is the other half of a cap: a caller handed 100 rows has learned nothing about whether
  there were 100 or 4000, and a count that stopped at the cap would always equal the cap.

  Both fields are additive. A filter whose new fields are zero produces the query it
  produced before, and a capped read is ordered by id while an uncapped one keeps the cost
  it had.

## [2.88.0] - 2026-08-30

### Added

- **The live view's panels fold.** Four panels sit in the four corners of the scene they
  describe. On a wide window they have room; in a narrow one — a phone, or the view framed
  inside another application — they meet in the middle and there is more chrome than graph.
  Embedded in a 440px panel the title card ran under the controls and the legend and the
  activity feed took the bottom third between them.

  Each panel now folds to a chip in its corner naming what it is hiding, so the view can be
  cleared without losing the way back. What is folded is remembered per browser; with
  nothing remembered yet the choice comes from the window, and below 760px the two panels
  carrying no controls start folded — a window narrow enough for them to overlap the graph
  is one where they were covering it.

### Fixed

- **The title card no longer runs under the controls on a narrow window.** Below 760px it
  is bounded to the room left beside them and wraps instead of sliding underneath.

## [2.87.0] - 2026-08-29

### Fixed

- **`CREATE EXTENSION IF NOT EXISTS` is not protection against a concurrent creation.**
  It checks and inserts in two steps, so two connections running it at the same moment
  both pass the guard and one loses in the catalogue with `duplicate key value violates
  unique constraint pg_extension_name_index` — an error naming a system index and nothing
  the caller did. Opening two brains on one PostgreSQL database at once is ordinary and
  each initialises its extensions on the way up, so this is reachable in production, not
  only under a parallel test run. `PostgresStore.Init` returned "pgvector is required by
  the PostgreSQL store" and refused to start; `GraphStore.initPgVector` recorded "pgvector
  unavailable" and did exact scans for the life of that store, having taken a momentary
  collision for a deployment fact; and the lexical DDL, run as one list whose loop broke
  on first error, lost both indexes and left CJK search linear without saying so.

  `sqldialect.EnsureExtension` replaces all three. It does not try to recognise the race
  by SQLSTATE or error text — on any failure it asks the catalogue whether the extension
  is present, which is the only question whose answer does not depend on a server's
  wording. A managed instance that refuses `CREATE EXTENSION` still gets an error,
  because there the extension really is absent.

  The race only exists while the extension is absent, which is why it presented as
  unreproducible flakiness: a database set up once by hand never opens the window.
  Dropping the extension first makes it fire on every run.

### Changed

- **`PostgresLexicalDDL` no longer returns the `CREATE EXTENSION` statement**; the
  extension is named by the new `PostgresLexicalExtension` and should be created with
  `sqldialect.EnsureExtension` before running the index DDL. Creating an extension is not
  the same kind of operation as creating an index and cannot be one statement in a list.

- **`pg_trgm` is now created by the test suite** rather than assumed. Nothing in this
  module ever created it: the trigram index worked only because some earlier run had
  installed it by hand, and a database without it failed with `operator class
  "gin_trgm_ops" does not exist`, which names the index and not the missing extension.

### Added

- **`internal/pgtest`**, one place for PostgreSQL test isolation. Every PostgreSQL test
  read one DSN and most shared one schema, so a package's `DROP TABLE ... CASCADE`
  reached into whatever else `go test ./...` had running. It hands out a schema per test
  (`Open`/`DSN`) or a database per test (`Database`, for the database-wide operations a
  schema cannot isolate), creates the extensions under an advisory lock, and drops what
  it made. Two packages had already worked around this by hand; both now use the helper.

## [2.86.0] - 2026-08-29

### Added

- **The graph can be asked what is unreachable, and what things are called.** 2.85.0
  gave the graph a census by type and stopped one caller writing SQL for it; the same
  caller had four more queries against `graph_nodes`, and two of them are questions this
  library should answer. `Connectivity` counts the nodes no edge touches alongside the
  total, from one statement so the two describe one graph — a large share of orphans
  means writes landed and their edges did not, which is the state a store reaches when
  an ingest's edges are rejected or when entities accumulate across re-ingests with
  nothing joining them, and it is invisible in every count that only grows. It is
  distinct from `GraphStatistics.ConnectedComponents`, which asks how the reachable part
  is divided rather than how much is reachable at all. `NodeLabels` lists nodes with
  their type and label, narrowed by id prefix and minimum label length: which spellings
  a vocabulary produced, whether one concept arrived under two names, which labels are
  long enough to match text against.

- **`EntityNodeIDPrefix`**, exported beside `EntityNodeID`. The caller listing entity
  nodes was matching `id LIKE 'entity:%'`, and reaching into this library's naming is
  worse than reaching into its tables — nothing would have failed if the namespacing
  changed. The five places inside `pkg/cortexdb` that spelled the prefix themselves now
  use the constant.

### Notes

- `NodeLabelQuery.IDPrefix` is matched literally: `%` and `_` in a prefix are escaped
  rather than treated as wildcards, since a caller passing an id convention is passing a
  literal. Without the escaping the test for it fails on both backends.

- The two `pkg/core` PostgreSQL lexical tests fail under `go test ./...` and pass under
  `-p 1`. The packages share one database and `pkg/graph`'s harness truncates it per
  run, so adding parity tests anywhere makes the collision likelier. Pre-existing.

## [2.85.0] - 2026-08-29

### Added

- **The graph can be asked what is in it, by type.** `GetGraphStatistics` reports how
  many nodes there are and never what kind, so a caller checking its graph against a
  declared vocabulary had nowhere to go but SQL of its own against `graph_nodes` and
  `graph_edges`. That is the worst place to leave a caller: its query is pinned to this
  schema and to one database's dialect, neither of which is promised to it, and when the
  graph layer learned to run on PostgreSQL as well as SQLite nothing in such a query
  would have failed loudly — it still parses, still returns rows, and simply stops
  describing the graph. Four methods on `Graph()` cover what those callers were writing:
  `NodeTypeCounts` and `EdgeTypeCounts` for the breakdown by type, `EdgeShapes` for the
  type shapes the edges actually have — how a declared relation is wrong — and
  `EdgeEndpointPairs` for the nodes those edges run through, which is what is wrong.
  Untyped nodes are counted under `""` rather than dropped, so the counts still sum to
  the node count. Both edge queries take optional edge types and push the filter into
  SQL. All four go through the dialect-aware path and are covered by the parity suite,
  which asks both databases the same question and compares the answers field by field.

## [2.84.4] - 2026-08-29

### Added

- **The page takes a highlight from whoever embedded it.** An application mounting this
  view has its own search box, and searching it asks about the same things these nodes
  are. It cannot reach into the page — different origin, behind its own proxy — so it
  posts `{type: "cortexdb:highlight", query}` and the page puts that in the Find box a
  person would have typed into. Unknown messages are ignored, so an embedder speaking a
  later dialect gets a page that works rather than one that throws.

## [2.84.3] - 2026-08-29

### Fixed

- **The scene follows its container.** The HUD is positioned in CSS and reflowed on its
  own; the WebGL canvas is a fixed pixel buffer and did not, so maximising the window
  grew the frame around a picture still the size the window had been at load. It is
  observed with a ResizeObserver on the element rather than a window resize listener,
  because an embedded page shares a window with panels that can be collapsed — that
  changes the container's width with no window resize to hear.

- **The glow resizes too, and is sized to the container in the first place.** The bloom
  pass keeps its own render targets and was built once from `window.innerWidth`. Embedded,
  that is the wrong size from the start, and it stayed wrong afterwards.

- **The reconnect path closes the stream with a method that exists.** `es.Close()` should
  be `es.close()`; a rename applied across the package's Go files reached into the
  JavaScript held in one of them, and nothing failed to compile to say so. The error
  handler threw instead of closing, on the path that runs when the connection drops.

## [2.84.2] - 2026-08-29

### Fixed

- **The live view can be mounted behind a reverse proxy.** Its page opened the event
  stream at an absolute `/api/stream`, which leaves whatever prefix the page was served
  under and lands on whatever the host serves at `/api`. The stream is now asked for
  relative to the page, so a view mounted at `/graph/` finds it at `/graph/api/stream`.

  This is what the package doc already told embedders to do — the view binds loopback and
  has no authentication, so an application that needs it reachable further should put its
  own authenticated proxy in front. It just could not actually be done until now.

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
