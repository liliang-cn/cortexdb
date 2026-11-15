# Integrating CortexDB into your own agent

CortexDB gives an agent memory, a knowledge base and a knowledge graph. This
guide is for people who have their own agent system and want it to use one.
There are four ways in, and they reach the same tools:

| Your agent | Use | You write |
| --- | --- | --- |
| speaks MCP (Claude Code, Codex, Cursor, an MCP adapter in LangGraph, AutoGen, …) | the `cortexdb-mcp-stdio` MCP server | a few lines of config |
| is written in Go | the library, in your process | Go calls |
| is written in Python, Node.js or Rust | `cortexdb-grpc` and a typed client | client calls |
| is anything else | `cortexdb-grpc` with REST turned on | HTTP requests |

None of them needs an embedding model. Without one, retrieval is lexical and the
knowledge graph is built the same way; add a model on the server later if you
want vector search as well.

## 1. MCP

Install the server:

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-mcp-stdio@latest
```

Point your MCP client at it. For a client that takes the common `mcpServers`
JSON:

```json
{
  "mcpServers": {
    "cortexdb": {
      "command": "cortexdb-mcp-stdio",
      "env": { "CORTEXDB_PATH": "/home/me/.cortexdb/agent.db" }
    }
  }
}
```

The agent now has the full tool surface: `knowledge_memory_remember` and
`knowledge_memory_recall` for memory, `knowledge_save` / `knowledge_search` for
documents, `upsert_entities`, `upsert_relations`, `expand_graph` and
`knowledge_graph_query` (SPARQL) for the graph, and the rest listed in the
[guide](GUIDE.md#tools-mcp--plugin).

To share one brain between agents or machines, run `cortexdb-grpc` somewhere
(see [section 3](#3-python-nodejs-rust-the-grpc-server-and-a-client)) and give
each MCP server its address instead of a file:

```json
"env": {
  "CORTEXDB_REMOTE": "10.0.0.5:47821",
  "CORTEXDB_GRPC_TOKEN": "<this agent's key>"
}
```

The MCP server then opens no local file. It asks the server which tools it has
and forwards every call, so a client does not have to be upgraded when the
server is.

**One thing MCP cannot do for you:** the model decides when to call a tool, and
models often forget to recall before they answer. If your framework has hooks
around a turn, call `knowledge_memory_recall` yourself before the turn and put
the result in the prompt (section 3 shows the shape). The Claude Code and Codex
plugin works this way: a hook recalls on every prompt, and the tools are there
for anything else.

## 2. Go: the library

```bash
go get github.com/liliang-cn/cortexdb/v2
```

```go
db, err := cortexdb.Open(cortexdb.DefaultConfig("agent.db"))
if err != nil { return err }
defer db.Close()

brain := db.KnowledgeMemory()
_, _ = brain.Remember(ctx, cortexdb.KnowledgeMemoryRememberRequest{
	MemoryID: "deploy-db-reset",
	UserID:   "alice",
	Scope:    "user",
	Content:  "Reset the test database before running the migration tests.",
})

rec, _ := brain.Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{
	Query:  "how do I run the migration tests?",
	UserID: "alice",
})
prompt := rec.ContextPack.Text + "\n\n" + userMessage
```

`ContextPack.Text` is the recall formatted for a prompt: matching memories,
knowledge snippets and graph facts.

### Giving the tools to your own model

If your agent calls an LLM with function calling, hand it CortexDB's tools
without MCP:

```go
tools := db.GraphRAGTools()

for _, d := range tools.Definitions() {
	// d.Name, d.Description, d.InputSchema: register each one with your
	// model's tool list, in whatever shape its API wants.
}

// When the model asks for a tool:
result, err := tools.Call(ctx, call.Name, json.RawMessage(call.Arguments))
// Send result (or err) back to the model as the tool's answer.
```

These are the same tools, with the same names and schemas, that MCP serves.
`ToolDefinition.Mutates` says whether a tool writes, if you want to offer a
read-only set.

To serve MCP from your own process instead, use `db.NewMCPServer(...)` or
`db.RunMCPStdio(ctx, ...)`.

## 3. Python, Node.js, Rust: the gRPC server and a client

Run the server next to your agent, or once for everyone:

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc   # 127.0.0.1:47821
```

Install a client: `pip install cortexdb-client`, `npm install cortexdb-client`
or `cargo add cortexdb-client`. Each has typed sub-clients (`knowledge`,
`memory`, `graph`, `graphrag`, `admin`) and `tools.CallTool`, which reaches any
tool by name, the same surface as MCP.

A turn with memory around it, in Python:

```python
import json
from cortexdb_client import CortexClient, proto

def call(client, name, **args):
    res = client.tools.CallTool(proto.CallToolRequest(name=name, args_json=json.dumps(args)))
    return json.loads(res.result_json)

with CortexClient.connect("127.0.0.1:47821", token="s3cret") as client:
    # Before the turn: recall, and put what came back in the prompt.
    rec = call(client, "knowledge_memory_recall",
               query=user_message, user_id="alice",
               keywords=["migration", "tests", "database"])
    prompt = rec["context_pack"]["text"] + "\n\n" + user_message

    answer = my_llm(prompt)

    # After the turn: keep what is worth keeping.
    call(client, "knowledge_memory_remember",
         memory_id="deploy-db-reset", user_id="alice", scope="user",
         content="Reset the test database before running the migration tests.")
```

Node.js and Rust look the same; see the client READMEs:
[Python](../clients/python/README.md), [Node.js](../clients/node/README.md),
[Rust](../clients/rust/cortexdb-client/README.md).

`keywords`, `alternate_queries` and `entity_names` are optional. Without an
embedding model they are what lexical recall matches on, so an agent that
expands a question into a few keywords and names gets better results.

## 4. Anything else: REST

Start the server with an HTTP address as well:

```bash
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret \
  cortexdb-grpc -http-addr 127.0.0.1:47823
```

Every tool is `POST /v1/tools/{name}` with the arguments as a JSON object, and
`GET /v1/tools` lists them:

```bash
curl -s http://127.0.0.1:47823/v1/tools/knowledge_memory_recall \
  -H "Authorization: Bearer s3cret" \
  -d '{"query": "how do I run the migration tests?", "user_id": "alice"}'
```

There are also plain routes for the common calls: `/v1/knowledge`,
`/v1/memory`, `/v1/recall`, `/v1/query`, `/v1/graph/entities`,
`/v1/graph/relations`, `/v1/graph/expand` and `/v1/graph/sparql`.

## Keys: several agents on one brain

A single `CORTEXDB_GRPC_TOKEN` gives whoever holds it full read and write
access. For several agents, give the server a key file instead
(`CORTEXDB_KEY_FILE=keys.json` or `-keys keys.json`):

```json
{
  "keys": [
    { "id": "planner", "secret": "<random>", "clearance": "read-write" },
    { "id": "support-bot", "secret": "<random>", "clearance": "read-only",
      "scope": { "collection": "support-docs" } },
    { "id": "alice-assistant", "secret": "<random>", "clearance": "read-write",
      "scope": { "user_id": "alice" } }
  ]
}
```

Each agent sends its own secret as the token. A key is `read-only` or
`read-write`, and its `scope` can confine it to user ids, memory scopes,
namespaces and collections (singular or plural fields, such as `collection` or
`collections`). The server reads the file again when it changes, so adding or
removing a key needs no restart. Deleting a key's entry revokes it.

The transport is plaintext. Run it on loopback, a trusted network or Tailscale,
or put a TLS proxy in front. [`deploy/`](../deploy/) has a systemd unit and a
container image.

## Which one to pick

- Your agent already speaks MCP and you don't want to write code: MCP.
- Your agent is in Go: the library. It runs in your process, with no server.
- You want memory to happen on every turn, not when the model remembers to ask:
  call recall and remember from your own code (section 2 or 3). This usually
  works better than tools alone.
- Several agents should share what they learn: one `cortexdb-grpc`, a key per
  agent, and each agent connects with MCP remote mode or a client.

## Ready-made adapters

If your agent is one of these, there is already a memory plugin:

- Claude Code and Codex: the [`cortexdb` plugin](GUIDE.md#claude-code-and-codex-plugin).
- OpenClaw and Hermes Agent: [native memory plugins](GUIDE.md#openclaw-and-hermes-memory-plugins).
- DeepSeek Harness and Obsidian: see the install table in the [README](../README.md#install).
