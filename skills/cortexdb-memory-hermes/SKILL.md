---
name: cortexdb-memory-hermes
description: Give a Python agent (such as Hermes Agent by Nous Research) durable, local-first memory plus a queryable SPARQL knowledge graph, backed by CortexDB through its gRPC sidecar and the cortexdb-client PyPI package. Use when a Python agent needs to remember facts about a user across turns/sessions, recall them by meaning, store entities and relations, or answer multi-hop questions — and when the user mentions CortexDB, agent memory, long-term memory, RAG, knowledge graph, Hermes, or "remember this".
license: MIT
compatibility: Requires Python 3.9+, pip, and the cortexdb-grpc sidecar binary (Go install or prebuilt release). Optional embeddings via any OpenAI-compatible endpoint (e.g. Ollama).
metadata: {"author": "liliang-cn", "project": "cortexdb", "version": "1.0"}
---

# CortexDB memory for a Python agent (Hermes)

Wire CortexDB in as the memory layer for a Python agent. CortexDB is a pure-Go,
single-file database; the Python agent talks to it over gRPC via the
`cortexdb-client` package. Beyond vector/lexical recall, it gives the agent a
real **knowledge graph** (RDF + SPARQL) — the thing most agent-memory layers lack.

## When to use this

- The agent should remember user facts/preferences across turns or sessions.
- The agent should recall memories by meaning, not exact match.
- The agent needs entities + relations and multi-hop questions ("who, among the
  people Alice knows, works on X").
- You're integrating with Hermes Agent (or any Python agent/framework).

## Step 1 — Run the sidecar (once)

CortexDB runs as a local sidecar process, owning one SQLite file.

```bash
# install the binary (or download a prebuilt release)
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest

# lexical mode — zero config, no API key:
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
# → listening on 127.0.0.1:47821
```

To enable vector/semantic recall, point it at any OpenAI-compatible embeddings
endpoint (e.g. a local Ollama):

```bash
OPENAI_BASE_URL=http://localhost:11434/v1 \
CORTEXDB_EMBED_MODEL=embeddinggemma CORTEXDB_EMBED_DIM=768 \
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
```

## Step 2 — Install the client

```bash
pip install cortexdb-client          # or: uv add cortexdb-client
```

## Step 3 — The two core moves: remember + recall

```python
from cortexdb_client import CortexClient, proto

client = CortexClient.connect("127.0.0.1:47821", token="s3cret")

# remember a fact about the user (scoped per user)
client.memory.SaveMemory(proto.SaveMemoryRequest(
    memory_id="pref-coffee",
    user_id="alice", scope="user",
    content="Alice prefers dark roast coffee and dislikes heavy frameworks.",
))

# later turn / next session: recall by meaning
hits = client.memory.SearchMemory(proto.SearchMemoryRequest(
    query="what does the user like to drink?",
    user_id="alice", scope="user", top_k=3,
))
for h in hits.results:
    print(h.memory.content, h.score)
```

**Memory scopes** isolate data: `scope="user"` (per `user_id`), `scope="session"`
(per `session_id`), or `scope="global"`. Use `user` for durable preferences and
`session` for short-lived conversation state.

## Step 4 — Knowledge instead of plain memory (RAG)

For documents the agent should retrieve from (not just per-user notes), use the
knowledge service — it chunks, indexes, and (with an embedder) does GraphRAG:

```python
client.knowledge.SaveKnowledge(proto.SaveKnowledgeRequest(
    knowledge_id="doc-1",
    title="Project brief",
    content="The user is building an autonomous research agent in Python.",
))
res = client.knowledge.SearchKnowledge(proto.SearchKnowledgeRequest(
    query="what is the user building?", top_k=3,
))
```

## Step 5 — The differentiator: a knowledge graph

Store entities and relations, then traverse them with SPARQL. This is what makes
CortexDB more than a vector store for an agent.

```python
iri = lambda v: proto.RdfTerm(kind="iri", value=v)
client.graph.UpsertNamespace(proto.UpsertNamespaceRequest(
    prefix="ex", uri="https://example.com/"))
client.graph.UpsertKnowledgeGraph(proto.UpsertKnowledgeGraphRequest(triples=[
    proto.RdfTriple(subject=iri("ex:alice"), predicate=iri("ex:knows"), object=iri("ex:bob")),
]))
ans = client.graph.QuerySparql(proto.QuerySparqlRequest(
    query="SELECT ?o WHERE { <https://example.com/alice> <https://example.com/knows> ?o . }"))
print(ans.result.count, "result(s)")
```

## Expose CortexDB as Hermes tools

Hermes runs Python and dispatches tools/subagents. Wrap the calls above as small
tool functions the agent can call — `remember(text)`, `recall(query)`,
`relate(from, rel, to)`, `ask_graph(sparql)`. Each is a 3-line wrapper over the
client. A ready-to-import module is provided:

- `scripts/memory_tools.py` — `remember`, `recall`, `save_knowledge`,
  `search_knowledge`, `relate`, `ask_graph`, returning plain dicts/strings that
  drop straight into a tool-calling loop.

## Install this skill into Hermes

Hermes adopts the agentskills.io standard; skills live under `~/.hermes/skills/`
and activate as `/skill-name`.

```bash
# from a URL to this SKILL.md, or a local checkout:
hermes skills install https://raw.githubusercontent.com/liliang-cn/cortexdb/main/skills/cortexdb-memory-hermes/SKILL.md --name cortexdb-memory-hermes

# or point Hermes at a directory of skills via ~/.hermes/config.yaml:
#   skills:
#     external_dirs:
#       - /path/to/cortexdb/skills
```

Then in a Hermes session: `/cortexdb-memory-hermes`. Hermes also speaks MCP — if
you prefer, run `cortexdb-mcp-stdio` and connect it as an MCP server instead of
(or alongside) this skill.

## Sub-clients (full surface)

`client.knowledge`, `client.memory`, `client.graph` (RDF/SPARQL/SHACL/inference/
ontology), `client.graphrag`, `client.tools` (generic dispatch, same shape as
MCP), `client.admin`. Every RPC takes a `proto.<Name>Request` and returns the
response message. Auth is a bearer token; pass `token=` to `connect`.

## Notes & gotchas

- **Zero-key default**: without an embedder the sidecar uses lexical retrieval —
  good enough to start, no credentials needed.
- **One file, one process**: the sidecar owns one SQLite file. Isolate multiple
  users via memory scopes (above), not multiple files.
- **Plaintext localhost**: the bearer token rides plain gRPC; fine on localhost,
  add TLS / a reverse proxy for cross-machine use.
- Package and docs: https://pypi.org/project/cortexdb-client/ ·
  https://github.com/liliang-cn/cortexdb
