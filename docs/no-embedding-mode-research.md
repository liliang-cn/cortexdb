# No-Embedding Mode Research

Date: 2026-03-13

## Summary

The codebase can remain usable without an in-process embedding model, but only in two distinct modes:

1. External/precomputed vector mode:
   The application process does not run an embedder, but vectors are still supplied by the caller or an upstream pipeline.
   This preserves most vector, graph, and retrieval features.

2. Lexical-only mode:
   No embedder and no vectors are available at runtime.
   In this mode, the system can still provide exact text retrieval, filtering, document management, and graph traversal over existing graphs, but not the current semantic or GraphRAG APIs.

The main conclusion is:
If the goal is "keep as much functionality as possible without an embedding model", the lowest-risk path is to make lexical fallback a first-class mode and to officially support externally supplied vectors as the full-featured no-model path.

If an LLM is guaranteed to exist even when no embedding model is available, the preferred design becomes:
- use the LLM for natural-language understanding
- use structured output or tool calling for extraction/planning
- use FTS5/BM25 as the retrieval engine

That is a better fit than inventing fake semantic vectors.

## What Already Works Without an Embedder

### 1. Text-only retrieval via FTS5

`SearchTextOnly` is explicitly designed to work without an embedder.

Relevant code:
- `pkg/cortexdb/cortexdb.go`
  - `SearchTextOnly`: lines 412-442
- `pkg/core/store_init.go`
  - `chunks_fts` virtual table and triggers: lines 160-175

This gives:
- keyword search
- BM25 ranking
- collection filtering
- text retrieval over stored chunk content

### 2. Message keyword search

Chat/message retrieval also has a keyword-only path.

Relevant code:
- `pkg/core/chat.go`
  - `KeywordSearchMessages`: lines 221-254
- `pkg/core/store_init.go`
  - `messages_fts` virtual table and triggers: lines 144-158

This gives:
- keyword recall over messages
- usable fallback for chat memory scenarios

### 3. External/precomputed vectors preserve most vector features

The lower-level APIs already accept vectors directly:
- `core.Store.Upsert`
- `Quick.Add`
- `graph.UpsertNode`

This means:
- no embedder needs to exist inside the process
- vectors can come from offline jobs, another service, or imported data
- search, hybrid retrieval, graph traversal, and current graph indexes can still work

In practice, this is the closest thing to "full functionality without an embedding model" that exists today.

## What Does Not Work Today Without an Embedder

### 1. High-level text APIs hard-fail

Relevant code:
- `pkg/cortexdb/cortexdb.go`
  - `Quick.AddText`: lines 224-253
  - `InsertText`: lines 283-306
  - `InsertTextBatch`: lines 308-345
  - `SearchTextInCollection`: lines 353-375
  - `HybridSearchText`: lines 377-399

These paths all require `db.embedder != nil` and return `ErrEmbedderNotConfigured` otherwise.

### 2. Current GraphRAG API hard-fails

Relevant code:
- `pkg/cortexdb/graphrag.go`
  - `InsertGraphDocument`: lines 98-346
  - `SearchGraphRAG`: lines 349-390

Current GraphRAG depends on embedder-generated vectors for:
- chunk storage
- document nodes
- entity nodes
- query vectors

Without an embedder, GraphRAG is not currently usable.

### 3. Core schema still requires vectors

Relevant code:
- `pkg/core/store_init.go`
  - `embeddings.vector BLOB NOT NULL`: lines 97-108
- `pkg/graph/graph.go`
  - `graph_nodes.vector BLOB NOT NULL`: lines 96-104
  - `UpsertNode` rejects empty vectors: lines 132-140

This means:
- lexical-only mode cannot currently ingest content without inventing some vector value
- graph nodes cannot be persisted as pure text nodes

## Capability Matrix

| Scenario | Current Status | What Works | What Breaks |
|----------|----------------|------------|-------------|
| No local embedder, but external vectors provided | Viable now | most vector APIs, ANN search, graph traversal, graph indexes, manual GraphRAG-style pipelines | high-level text APIs and current GraphRAG convenience APIs |
| No embedder, no vectors, only raw text | Partially viable now | `SearchTextOnly`, BM25/FTS5, message keyword search, documents, filters, ACL, graph traversal over already-built graphs | semantic retrieval, high-level text ingestion, current GraphRAG |
| No embedder, but want "GraphRAG-like" exact-text retrieval | Requires implementation | can reuse FTS5 + graph expansion + current rerank/packing design | current API hard-blocks |

## Recommended Path

### Recommendation 1: Officially support external-vector mode

This is the fastest way to keep the most functionality.

Recommended changes:
- document it as a supported mode
- add high-level helpers that accept caller-provided vectors
  - `InsertTextWithVector`
  - `InsertGraphDocumentWithVectors`
  - `SearchGraphRAGWithQueryVector`

Why this should be first:
- no schema change required
- preserves current retrieval quality
- keeps ANN, graph search, and GraphRAG behavior intact

### Recommendation 2: Add lexical fallback mode as a first-class product path

This is the best answer for "truly no embedding model available".

Recommended behavior:
- `SearchText` falls back to `SearchTextOnly`
- `HybridSearchText` falls back to BM25/FTS5 + text-only reranking
- `SearchGraphRAG` falls back to:
  - seed retrieval from `SearchTextOnly`
  - graph neighborhood expansion using existing graph edges
  - current rerank/context packing reused as-is

This would produce a usable "lexical GraphRAG" mode with lower semantic quality but the same high-level workflow.

### Recommendation 2A: When LLM is available, make the lexical mode LLM-assisted

This is the preferred design if the product can always call an LLM but may not have an embedding model.

Use the LLM for:
- entity extraction
- relationship extraction
- query intent parsing
- keyword expansion and query planning
- optional answer synthesis

Use SQLite/FTS5 for:
- exact keyword retrieval
- BM25 ranking
- graph expansion over extracted entities and stored edges

The existing `GraphRAGExtractor` interface is already compatible with this approach.
It can be implemented by an LLM adapter that returns strict structured JSON or uses tool calling.

The main missing interface is a query-planning seam, for example:

```go
type GraphRAGQueryPlanner interface {
    Plan(ctx context.Context, query string) (*GraphRAGQueryPlan, error)
}
```

Where `GraphRAGQueryPlan` would contain:
- rewritten keywords
- target entities
- node/edge filters
- optional traversal hints

With that design, no-embedder GraphRAG would work like this:

1. LLM parses the query into entities, keywords, and constraints.
2. FTS5/BM25 retrieves seed chunks.
3. Graph traversal expands through mentioned entities and linked chunks.
4. Existing rerank/context packing assembles the prompt context.
5. Optional LLM answer generation happens on top.

### Recommendation 3: Do not use zero-vectors as the main design

Using zero vectors everywhere would satisfy the `NOT NULL` schema constraint, but it has serious drawbacks:
- ANN/HNSW results become meaningless
- vector search semantics become misleading
- graph node similarity becomes polluted

If a no-model mode needs vector placeholders, a deterministic lexical encoder is safer.

## Best Technical Options for No-Model Mode

### Option A: Deterministic lexical encoder

Examples:
- feature hashing bag-of-words
- character n-gram hashing
- sparse-to-dense lexical projection

Benefits:
- satisfies current non-null vector schema
- no external model required
- preserves existing vector-based APIs with degraded but still useful lexical similarity

Tradeoff:
- lower semantic quality than real embeddings

### Option B: Make vectors nullable for lexical-only collections/graph nodes

Benefits:
- cleaner architecture
- honest separation between lexical-only and vector-enabled data

Tradeoff:
- larger schema and code-path change
- more places must branch around missing vectors

If an LLM is guaranteed, Option A should not be the first choice for product mode.
It is still a useful fallback for callers that need vector-compatible APIs without an embedding service.

Short-term:
- preferred product path: LLM-assisted lexical GraphRAG
- preferred compatibility path: external/precomputed vectors

Long-term:
- nullable vectors or split lexical/vector storage is architecturally cleaner

## Suggested Implementation Order

1. Add a structured LLM extractor implementation for `GraphRAGExtractor`
2. Add a structured LLM query-planner interface for lexical GraphRAG
3. Add lexical fallback to `SearchText` and `HybridSearchText`
4. Add `SearchGraphRAGTextOnly` or make `SearchGraphRAG` auto-fallback when no embedder exists
5. Add external-vector helpers for callers who can supply vectors upstream
6. Decide whether to keep deterministic lexical vectors as a compatibility layer or evolve toward nullable vectors

## Practical Conclusion

If the requirement is "no embedding model, but keep the product usable":

- Best immediate answer:
  Support external vectors as the near-full-functionality mode.

- Best true no-model answer when LLM exists:
  Build an LLM-assisted lexical GraphRAG mode on top of FTS5/BM25 and graph traversal.

- Current repository status:
  The necessary primitives already exist for lexical retrieval and graph traversal, but the current high-level GraphRAG and text APIs are not yet wired for no-embedder usage.
