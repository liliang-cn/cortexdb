---
name: cortexdb-memory-openclaw
description: Give a Node.js agent (such as OpenClaw) durable, local-first memory plus a queryable SPARQL knowledge graph, backed by CortexDB through its gRPC sidecar and the cortexdb-client npm package. Use when a Node agent needs to remember facts about a user across turns/sessions, recall them by meaning, store entities and relations, or answer multi-hop questions — and when the user mentions CortexDB, agent memory, long-term memory, RAG, knowledge graph, OpenClaw, or "remember this".
license: MIT
compatibility: Requires Node.js 18+, npm, and the cortexdb-grpc sidecar binary (Go install or prebuilt release). Optional embeddings via any OpenAI-compatible endpoint (e.g. Ollama).
metadata: {"author": "liliang-cn", "project": "cortexdb", "version": "1.0"}
---

# CortexDB memory for a Node agent (OpenClaw)

Wire CortexDB in as the memory layer for a Node.js agent. CortexDB is a pure-Go,
single-file database; the Node agent talks to it over gRPC via the
`cortexdb-client` package. Beyond vector/lexical recall, it gives the agent a
real **knowledge graph** (RDF + SPARQL) — the thing most agent-memory layers lack.
It fits OpenClaw's local-first philosophy: one binary, one SQLite file, no
separate service to stand up.

## When to use this

- The agent should remember user facts/preferences across turns or sessions.
- The agent should recall memories by meaning, not exact match.
- The agent needs entities + relations and multi-hop questions ("who, among the
  people Alice knows, works on X").
- You're integrating with OpenClaw (or any Node agent/skill).

## Step 1 — Run the sidecar (once)

```bash
# install the binary (or download a prebuilt release)
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest

# lexical mode — zero config, no API key:
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
# → listening on 127.0.0.1:47821
```

Enable vector/semantic recall by pointing it at any OpenAI-compatible embeddings
endpoint (e.g. a local Ollama):

```bash
OPENAI_BASE_URL=http://localhost:11434/v1 \
CORTEXDB_EMBED_MODEL=embeddinggemma CORTEXDB_EMBED_DIM=768 \
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
```

## Step 2 — Install the client

```bash
npm install cortexdb-client
```

## Step 3 — The two core moves: remember + recall

```js
const { CortexClient } = require('cortexdb-client');

const client = CortexClient.connect('127.0.0.1:47821', { token: 's3cret' });

// remember a fact about the user (scoped per user)
await client.memory.SaveMemory({
  memoryId: 'pref-coffee', userId: 'alice', scope: 'user',
  content: 'Alice prefers dark roast coffee and runs OpenClaw locally.',
});

// later turn / next session: recall by meaning
const hits = await client.memory.SearchMemory({
  query: 'what does the user like to drink?',
  userId: 'alice', scope: 'user', topK: 3,
});
for (const h of hits.results) console.log(h.memory.content, h.score);
```

Every RPC is a promise; request fields are camelCase. **Memory scopes** isolate
data: `scope: 'user'` (per `userId`), `scope: 'session'` (per `sessionId`), or
`scope: 'global'`.

## Step 4 — Knowledge instead of plain memory (RAG)

```js
await client.knowledge.SaveKnowledge({
  knowledgeId: 'doc-1', title: 'Project brief',
  content: 'The user is building an autonomous agent in TypeScript.',
});
const res = await client.knowledge.SearchKnowledge({
  query: 'what is the user building?', topK: 3,
});
```

## Step 5 — The differentiator: a knowledge graph

```js
const iri = (v) => ({ kind: 'iri', value: v });
await client.graph.UpsertNamespace({ prefix: 'ex', uri: 'https://example.com/' });
await client.graph.UpsertKnowledgeGraph({ triples: [
  { subject: iri('ex:alice'), predicate: iri('ex:knows'), object: iri('ex:bob') },
] });
const ans = await client.graph.QuerySparql({
  query: 'SELECT ?o WHERE { <https://example.com/alice> <https://example.com/knows> ?o . }',
});
console.log(ans.result.count, 'result(s)');
```

## Expose CortexDB as OpenClaw tools

OpenClaw skills teach the agent how and when to call tools. A ready-to-use helper
module wraps the calls above into `remember`, `recall`, `saveKnowledge`,
`searchKnowledge`, `relate`, and `askGraph`:

- `scripts/memory-tools.js` — import these and register them as OpenClaw tools,
  or call them directly from a custom skill/plugin.

In your skill's instructions, tell the agent: *to remember a durable fact about
the user, call `remember(text)`; to recall, call `recall(query)`; to record a
relationship, call `relate(subject, predicate, object)`; to answer a structured
"who/what is related to X" question, call `askGraph(sparql)`.*

## Install this skill into OpenClaw

OpenClaw follows the agentskills.io spec and discovers skills under
`<workspace>/skills`, `<workspace>/.agents/skills`, `~/.agents/skills`, and
`~/.openclaw/skills`.

```bash
# from this repo (local directory):
openclaw skills install ./skills/cortexdb-memory-openclaw --as cortexdb-memory

# or from git / ClawHub:
openclaw skills install git:liliang-cn/cortexdb@main --global
```

`--global` installs to `~/.openclaw/skills`. The skill becomes eligible
automatically once the `cortexdb-grpc` binary is present and a sidecar is
running.

## Sub-clients (full surface)

`client.knowledge`, `client.memory`, `client.graph` (RDF/SPARQL/SHACL/inference/
ontology), `client.graphrag`, `client.tools` (generic dispatch, same shape as
MCP), `client.admin`. Each RPC is available in both PascalCase (`SaveMemory`) and
camelCase (`saveMemory`). Auth is a bearer token; pass `{ token }` to `connect`.

## Notes & gotchas

- **Zero-key default**: without an embedder the sidecar uses lexical retrieval —
  good enough to start, no credentials needed.
- **One file, one process**: the sidecar owns one SQLite file. Isolate multiple
  users via memory scopes (above), not multiple files.
- **Plaintext localhost**: the bearer token rides plain gRPC; fine on localhost,
  add TLS / a reverse proxy for cross-machine use.
- **No build step**: the npm client loads the proto contract at runtime.
- Package and docs: https://www.npmjs.com/package/cortexdb-client ·
  https://github.com/liliang-cn/cortexdb
