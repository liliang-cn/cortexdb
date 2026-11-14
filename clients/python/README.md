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

## Results as DataFrames or CSV

`client.tools.CallTool` answers with JSON. `cortexdb_client.tabular` turns the
row-shaped part of it into flat records, a pandas DataFrame or a CSV file:

```python
import json
from cortexdb_client import CortexClient, proto, tabular

with CortexClient.connect("127.0.0.1:47821", token="s3cret") as client:
    res = client.tools.CallTool(proto.CallToolRequest(
        name="graph_cypher_query",
        args_json=json.dumps({"query": "MATCH (a)-[r]->(b) RETURN a, r, b LIMIT 100"}),
    ))

rows = tabular.records(res)       # list of flat dicts: {"a.id": ..., "r.type": ..., "b.id": ...}
df = tabular.to_pandas(res)       # needs the extra: pip install "cortexdb-client[pandas]"
tabular.to_csv(res, "edges.csv")  # standard library only; to_csv(res) returns the text
```

pandas is optional and imported only inside `to_pandas()`; the base install
stays `grpcio` + `protobuf`.

| Tool | Table(s) |
| --- | --- |
| `graph_cypher_query` | `rows` (default) — `columns` zipped with each row |
| `aggregate_metadata` | `results` (default) — one row per group |
| `memory_search`, `knowledge_search` | `results` (default); `knowledge_search` may also carry `chunks` |
| `knowledge_memory_recall` | `memories`, `knowledge`, `chunks`, `graph_facts` — pass `table=` |
| `memory_list_all` | `memories` (default) |
| `graph_list_all` | `nodes`, `edges` — pass `table=` |
| `search_text`, `recall_log_list`, `graph_property_values`, … | the one top-level list of objects (`chunks`, `recalls`, `values`) |

`tabular.tables(res)` names the tables a result has. A result with more than one
and no `rows` / `results` raises `ValueError` until you pick:
`tabular.records(res, "edges")`.

How a row becomes columns:

- a scalar is a column under its key;
- a nested object becomes one column per leaf, keys joined with `.`
  (`metadata.project`, `n.properties.city`); `sep="__"` changes the separator;
- a list becomes its JSON text, or stays a list with `lists="keep"`
  (for `DataFrame.explode`);
- the `keys` of an aggregate group and the `memory` of a memory hit are lifted,
  so their fields lead the row unprefixed (`team`, `year`, `value`, `count`;
  `id`, `content`, …, `score`). A lifted name that collides keeps its prefix
  (`keys.count`). Every other key of a group is a column whatever it is called,
  so new measures need no client change;
- rows need not share columns: CSV leaves the cell empty, pandas gives `NaN`.

Listings page with a cursor; collect the pages, then tabulate:

```python
memories, cursor = [], ""
while True:
    page = json.loads(client.tools.CallTool(proto.CallToolRequest(
        name="memory_list_all", args_json=json.dumps({"cursor": cursor}))).result_json)
    memories += tabular.records(page)
    cursor = page.get("next_cursor", "")
    if not cursor:
        break
```

To analyse the SQLite file itself with DuckDB, pandas or `sqlite3`, see
"Data analysis" in the [guide](https://github.com/liliang-cn/cortexdb/blob/main/docs/GUIDE.md).

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
