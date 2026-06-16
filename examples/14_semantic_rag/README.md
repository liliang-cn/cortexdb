# 14 · Semantic RAG — real embedding model + LLM

The embedder-backed counterpart to the lexical examples. It wires a real
**embedding model** into CortexDB so retrieval works on **meaning**, not keyword
overlap, and uses a **chat LLM** to answer — both via an OpenAI-compatible
endpoint (the defaults target DashScope / Qwen).

```
docs ──InsertText (embeds via text-embedding-v4)──▶ CortexDB vector index
query ──SearchText (embeds query, cosine search)──▶ top hit by MEANING
                                                      │
                                          qwen3.7-plus answers from that context
```

## Why it matters

The query *"who deals with production outages after hours?"* shares **no keywords**
with the stored fact *"When the site goes down at night, the engineer on rotation
gets paged…"* — lexical/FTS retrieval would miss it. With an embedder, vector
search ranks it first:

```
=== semantic vector search (no keyword overlap): "who deals with production outages after hours?" ===
  1. [doc-oncall  score=0.545] When the site goes down at night, the engineer on rotation gets paged and must respond.
  2. [doc-payments score=0.296] ...
=== qwen3.7-plus answer ===
  The engineer on rotation gets paged and must respond when the site goes down at night.
```

## Run it

Requires an embedding + chat endpoint (config via env or a `.env` loaded by godotenv):

```bash
OPENAI_API_KEY=sk-...                                   # required
OPENAI_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
OPENAI_MODEL=qwen3.7-plus                                # chat
EMBED_MODEL=text-embedding-v4                            # embeddings
EMBED_DIM=1024
go run ./examples/14_semantic_rag
```

Any OpenAI-compatible endpoint works (OpenAI, DashScope, a local Ollama with an
embedding model, etc.) — `embedder.go` is a ~60-line `cortexdb.Embedder`
implementation you can copy.

## How it plugs in

- `embedder.go` implements `cortexdb.Embedder` (`Embed` / `EmbedBatch` / `Dim`).
- `cortexdb.Open(cfg, cortexdb.WithEmbedder(e))` with `cfg.Dimensions = 1024`
  switches CortexDB from lexical to vector retrieval.
- `db.InsertText` embeds + indexes; `db.SearchText` embeds the query and runs
  cosine similarity over the HNSW index.
