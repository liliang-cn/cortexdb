# 把 CortexDB 接进你自己的 agent

CortexDB 给 agent 提供记忆、知识库和知识图谱。这篇写给已经有自己 agent 系统、想让它用上 CortexDB 的人。接入方式有四种，用到的是同一套工具：

| 你的 agent | 用什么 | 你要写的 |
| --- | --- | --- |
| 支持 MCP（Claude Code、Codex、Cursor，LangGraph、AutoGen 等框架的 MCP 适配器……） | `cortexdb-mcp-stdio` 这个 MCP server | 几行配置 |
| 用 Go 写 | 直接嵌库，在你的进程里跑 | Go 调用 |
| 用 Python、Node.js 或 Rust 写 | `cortexdb-grpc` 加对应的客户端 | 客户端调用 |
| 其他 | `cortexdb-grpc` 打开 REST | HTTP 请求 |

哪种都不需要 embedding 模型。没有模型时走关键词检索，知识图谱照样建；以后想要向量检索，在服务端配一个模型就行。

## 1. MCP

安装：

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-mcp-stdio@latest
```

在 MCP 客户端里配上它。常见的 `mcpServers` JSON 写法：

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

agent 就有了全部工具：记忆用 `knowledge_memory_remember` 和 `knowledge_memory_recall`，文档用 `knowledge_save` / `knowledge_search`，图谱用 `upsert_entities`、`upsert_relations`、`expand_graph` 和 `knowledge_graph_query`（SPARQL），其余见[使用指南](GUIDE_CN.md)。

想让多个 agent 或多台机器共用一个大脑，就在某处跑一个 `cortexdb-grpc`（见[第 3 节](#3-pythonnodejsrustgrpc-服务加客户端)），每个 MCP server 配它的地址，不再配文件：

```json
"env": {
  "CORTEXDB_REMOTE": "10.0.0.5:47821",
  "CORTEXDB_GRPC_TOKEN": "<这个 agent 的 key>"
}
```

这时 MCP server 不打开本地文件，启动时向服务端要工具列表，所有调用都转发过去，所以服务端升级后客户端不用跟着升级。

**MCP 替你做不到的一件事：** 什么时候调工具由模型决定，而模型经常忘了先召回再回答。如果你的框架在每轮对话前后有 hook，就在每轮之前自己调 `knowledge_memory_recall`，把结果放进 prompt（第 3 节有示例）。Claude Code 和 Codex 插件就是这么做的：每次提问都由 hook 召回，工具留给其他场景。

## 2. Go：直接嵌库

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

`ContextPack.Text` 是整理成 prompt 格式的召回结果：命中的记忆、知识片段和图谱事实。

### 把工具交给你自己的模型

如果你的 agent 用 function calling 调 LLM，可以不经过 MCP，直接把 CortexDB 的工具交给模型：

```go
tools := db.GraphRAGTools()

for _, d := range tools.Definitions() {
	// d.Name、d.Description、d.InputSchema：按你用的模型 API 的格式，
	// 逐个注册进工具列表。
}

// 模型要调用工具时：
result, err := tools.Call(ctx, call.Name, json.RawMessage(call.Arguments))
// 把 result（或 err）作为工具结果交还给模型。
```

这和 MCP 提供的是同一套工具，名字和参数结构都一样。`ToolDefinition.Mutates` 标明一个工具会不会写入，想只开放只读工具时可以用它筛。

想在自己的进程里提供 MCP，用 `db.NewMCPServer(...)` 或 `db.RunMCPStdio(ctx, ...)`。

## 3. Python、Node.js、Rust：gRPC 服务加客户端

服务可以跟 agent 跑在一起，也可以大家共用一个：

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc   # 127.0.0.1:47821
```

装客户端：`pip install cortexdb-client`、`npm install cortexdb-client` 或 `cargo add cortexdb-client`。每个客户端都有带类型的子客户端（`knowledge`、`memory`、`graph`、`graphrag`、`admin`），还有 `tools.CallTool`，可以按名字调任意工具，和 MCP 是同一套。

用 Python 给一轮对话前后加上记忆：

```python
import json
from cortexdb_client import CortexClient, proto

def call(client, name, **args):
    res = client.tools.CallTool(proto.CallToolRequest(name=name, args_json=json.dumps(args)))
    return json.loads(res.result_json)

with CortexClient.connect("127.0.0.1:47821", token="s3cret") as client:
    # 每轮之前：先召回，把结果放进 prompt。
    rec = call(client, "knowledge_memory_recall",
               query=user_message, user_id="alice",
               keywords=["migration", "tests", "database"])
    prompt = rec["context_pack"]["text"] + "\n\n" + user_message

    answer = my_llm(prompt)

    # 每轮之后：把值得留下的存起来。
    call(client, "knowledge_memory_remember",
         memory_id="deploy-db-reset", user_id="alice", scope="user",
         content="Reset the test database before running the migration tests.")
```

Node.js 和 Rust 写法一样，见各自的 README：[Python](../clients/python/README.md)、[Node.js](../clients/node/README.md)、[Rust](../clients/rust/cortexdb-client/README.md)。

`keywords`、`alternate_queries` 和 `entity_names` 都是可选的。没有 embedding 模型时，关键词召回靠的就是它们，所以让 agent 先把问题展开成几个关键词和名字，召回效果会更好。

## 4. 其他语言：REST

启动服务时再给一个 HTTP 地址：

```bash
CORTEXDB_PATH=agent.db CORTEXDB_GRPC_TOKEN=s3cret \
  cortexdb-grpc -http-addr 127.0.0.1:47823
```

每个工具都是 `POST /v1/tools/{name}`，参数是一个 JSON 对象；`GET /v1/tools` 列出全部工具：

```bash
curl -s http://127.0.0.1:47823/v1/tools/knowledge_memory_recall \
  -H "Authorization: Bearer s3cret" \
  -d '{"query": "how do I run the migration tests?", "user_id": "alice"}'
```

常用操作也有直接的路由：`/v1/knowledge`、`/v1/memory`、`/v1/recall`、`/v1/query`、`/v1/graph/entities`、`/v1/graph/relations`、`/v1/graph/expand` 和 `/v1/graph/sparql`。

## Key：多个 agent 共用一个大脑

只设一个 `CORTEXDB_GRPC_TOKEN` 时，拿到它的人就有完整读写权限。多个 agent 共用时，改给服务端一个 key 文件（`CORTEXDB_KEY_FILE=keys.json` 或 `-keys keys.json`）：

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

每个 agent 用自己的 secret 当 token。key 分 `read-only` 和 `read-write`，`scope` 可以把它限定在某些 user id、记忆 scope、namespace 和 collection 上（单数、复数字段都行，比如 `collection` 或 `collections`）。文件改了服务端会重新读，加减 key 不用重启；删掉一条就等于吊销。

传输是明文的。放在本机回环、可信内网或 Tailscale 上，或者前面加一层 TLS 代理。[`deploy/`](../deploy/) 里有 systemd unit 和容器镜像。

## 怎么选

- agent 已经支持 MCP，不想写代码：用 MCP。
- agent 用 Go 写：直接嵌库，在你的进程里跑，不用起服务。
- 想每轮对话都自动用上记忆，而不是等模型想起来调工具：在自己的代码里调 recall 和 remember（第 2 或第 3 节）。效果通常比只给工具好。
- 多个 agent 要共享学到的东西：起一个 `cortexdb-grpc`，每个 agent 一把 key，各自用 MCP 远程模式或客户端连上去。

## 现成的适配

如果你的 agent 是下面这些，已经有现成的记忆插件：

- Claude Code 和 Codex：[`cortexdb` 插件](GUIDE_CN.md)。
- OpenClaw 和 Hermes Agent：[原生记忆插件](GUIDE.md#openclaw-and-hermes-memory-plugins)。
- DeepSeek Harness 和 Obsidian：见 [README](../README_CN.md) 里的安装表。
