# cortexdb-client (Python)

Typed gRPC client for [CortexDB](https://github.com/liliang-cn/cortexdb) — a
pure-Go, single-file AI memory and knowledge graph database, served as a
sidecar (`cortexdb-grpc`). Give your Python agent (e.g. **Hermes**) durable
memory **and** a queryable knowledge graph.

## Install

```bash
pip install cortexdb-client          # or: uv add cortexdb-client
```

Start the sidecar (one binary, one SQLite file):

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
# listening on 127.0.0.1:47821
```

## Quick start

```python
from cortexdb_client import CortexClient, proto

with CortexClient.connect("127.0.0.1:47821", token="s3cret") as client:
    client.knowledge.SaveKnowledge(proto.SaveKnowledgeRequest(
        knowledge_id="note-1",
        content="The user is building an autonomous research agent in Python.",
    ))
    hits = client.knowledge.SearchKnowledge(proto.SearchKnowledgeRequest(
        query="what is the user building?", top_k=3,
    ))
    for h in hits.results:
        print(h.knowledge_id, h.score, h.snippet)
```

Sub-clients mirror the Rust crate: `client.knowledge`, `client.memory`,
`client.graph` (SPARQL/RDF/SHACL/inference/ontology), `client.graphrag`,
`client.tools` (generic tool dispatch, same surface as MCP), `client.admin`.

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

## Regenerating the protobuf code

Generated code is committed under `cortexdb_client/_pb`. To regenerate after a
proto change: `uv run --extra dev ./gen.sh`.
