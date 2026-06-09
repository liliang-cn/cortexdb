# cortexdb-client (Node.js)

Typed gRPC client for [CortexDB](https://github.com/liliang-cn/cortexdb) — a
pure-Go, single-file AI memory and knowledge graph database, served as a
sidecar (`cortexdb-grpc`). Give your Node agent (e.g. **OpenClaw**) durable
memory **and** a queryable knowledge graph.

## Install

```bash
npm install cortexdb-client
```

Start the sidecar (one binary, one SQLite file):

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
# listening on 127.0.0.1:47821
```

## Quick start

```js
const { CortexClient } = require('cortexdb-client');

const client = CortexClient.connect('127.0.0.1:47821', { token: 's3cret' });

await client.knowledge.SaveKnowledge({
  knowledgeId: 'note-1',
  content: 'The user runs an autonomous agent locally and prefers TypeScript.',
});

const hits = await client.knowledge.SearchKnowledge({
  query: 'what does the user prefer?', topK: 3,
});
for (const h of hits.results) console.log(h.knowledgeId, h.score, h.snippet);

client.close();
```

Every RPC is available as a promise, in both `PascalCase` (`SaveKnowledge`) and
`camelCase` (`saveKnowledge`). Request fields are camelCase.

Sub-clients mirror the Rust crate and Python package: `client.knowledge`,
`client.memory`, `client.graph` (SPARQL/RDF/SHACL/inference/ontology),
`client.graphrag`, `client.tools` (generic tool dispatch, same surface as MCP),
`client.admin`.

## Why a knowledge graph, not just vectors

Beyond semantic recall, the `graph` service lets an agent store and traverse
**entities and relations** — multi-hop questions like "who, among the people
Alice knows, works on X" — with SPARQL, RDFS-lite inference, and SHACL-lite
validation. That is the capability most agent-memory layers lack.

## Embeddings

Lexical mode needs no keys. Point the sidecar at any OpenAI-compatible
embeddings endpoint (e.g. Ollama) to enable vector retrieval:

```bash
OPENAI_BASE_URL=http://localhost:11434/v1 \
CORTEXDB_EMBED_MODEL=embeddinggemma CORTEXDB_EMBED_DIM=768 \
cortexdb-grpc
```

The `.proto` contract is vendored under `proto/` and loaded at runtime via
`@grpc/proto-loader` — no build step.
