# CortexDB

[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2) [![CI](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml) [![codecov](https://codecov.io/gh/liliang-cn/cortexdb/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/cortexdb) [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

纯 Go、单文件的 AI memory 和知识图谱库与插件。你可以把 CortexDB 嵌入自己的 Go agent 项目，作为 memory/KG 层；也可以把它装进 Claude Code 和 Codex，作为跨项目共享的记忆大脑。SQLite 为存储内核——一个文件即承载向量、lexical/RAG 检索、分作用域的 agent memory、RDF/SPARQL/RDFS/SHACL 知识图谱，以及 MCP tools。支持无 embedder（lexical 模式）或任意 OpenAI 兼容的 embeddings 端点。

## 为什么选 CortexDB？

当你想给 agent 一个可嵌入、可审计、图谱感知的长期记忆层，又不想额外部署更多基础设施时，CortexDB 适合放进候选名单。

| 如果你正在考虑... | CortexDB 给你... | 取舍 |
| --- | --- | --- |
| `chromem-go` 或小型嵌入式向量库 | 一个 SQLite 文件里的向量、lexical 检索、durable knowledge、分作用域 memory、RDF/SPARQL 和 MCP tools | 如果只需要一个很小的向量集合，CortexDB 的表面积会更大 |
| `sqlite-vec` 或裸 SQLite 扩展 | 面向 RAG、memory、hybrid retrieval、graph facts 和 agent tools 的 Go facade | 不如自己拼扩展时那么底层可控 |
| Chroma、Qdrant、LanceDB 或托管向量库 | 无需单独服务、无需额外存储平面，lexical 模式也不需要 API key | 目标不是分布式向量数据库 |
| Fuseki、GraphDB、Stardog 或独立图数据库 | 足够支撑 local-first agent 工作流的 RDF/SPARQL/RDFS/SHACL，并且和文本、memory 存在同一文件 | 不是完整企业级 RDF server |
| 为 Claude Code/Codex 自己写 memory 表 | 打包好的插件、MCP server、auto-recall 路径，以及可复用的 memory/KG tools | 具体产品的记忆策略仍需要你自己定义 |

准备 launch 或社区发帖时，可看 [docs/LAUNCH_KIT.md](docs/LAUNCH_KIT.md)，里面有可直接改的 Show HN、Reddit 和 demo 脚本。

## 安装与快速开始

```bash
go get github.com/liliang-cn/cortexdb/v2
```

```go
db, _ := cortexdb.Open(cortexdb.DefaultConfig("KnowledgeMemory.db"))
defer db.Close()

q := db.Quick()
_, _ = q.Add(ctx, []float32{0.1, 0.2, 0.9}, "SQLite is a single-file database.")
hits, _ := q.Search(ctx, []float32{0.1, 0.2, 0.8}, 1)

// 无 embedder 的 RAG（lexical）：
_, _ = db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
    KnowledgeID: "apollo", Content: "Alice owns Apollo. Apollo ships Friday."})
resp, _ := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
    Query: "Who owns Apollo?", RetrievalMode: cortexdb.RetrievalModeLexical, TopK: 3})
```

## 分层 —— 选对层

```text
pkg/cortexdb   主 facade：向量、text/RAG 检索、knowledge、memory、KG、tools、MCP。  ← 从这里开始
pkg/memoryflow Agent memory workflow：transcript ingest、recall、wake-up layers、promotion。
pkg/graphflow  语料 → 抽取 → build → analyze → report → export（HTML）。
pkg/importflow 导入 CSV / SQL dump / 线上 Postgres-MySQL 到 RAG + KG（DDL → 图谱）。
pkg/connector  importflow 之上的隐私闸门：PII 脱敏、人工签字方案、可逆金库、CDC 同步。
pkg/graph      底层 RDF/SPARQL/RDFS/SHACL + property graph。
pkg/core       SQLite 存储、embeddings、FTS5、向量索引（HNSW/IVF/Flat）。
```

## 知识图谱

同一文件内嵌 RDF：triples/quads、namespaces、N-Triples/Turtle/TriG 导入导出，实用 SPARQL 子集（SELECT/ASK/CONSTRUCT/DESCRIBE、更新语句、OPTIONAL/UNION/MINUS/VALUES/BIND/FILTER、聚合、子查询、property path `^p p|q p+ p*`），RDFS-lite 物化推理，以及 SHACL-lite 校验。

```go
db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: triples})
res, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
    Query: `SELECT ?name WHERE { <https://example.com/alice> <https://schema.org/name> ?name }`})
```

## Tools、MCP 与插件

```go
tools := db.GraphRAGTools()                             // 进程内 tool calling
server := db.NewMCPServer(cortexdb.MCPServerOptions{})  // MCP server
```

工具分组：GraphRAG（`ingest_document`、`search_text`、`build_context`）、knowledge/memory（`knowledge_save`、`memory_search` …）、KG（`knowledge_graph_query`、`_shacl_validate`）、KnowledgeMemory（`knowledge_memory_recall`、`_reflect`）。`memoryflow`/`graphflow`/`importflow`/`connector` 各自也暴露自己的 toolbox。

## Claude Code 和 Codex 插件

把持久记忆 + 知识图谱作为插件装进 Claude Code（和 Codex）。它打包了 `cortexdb` skill 和一个常驻 MCP server，默认跑无 embedder 的 **lexical 模式**（无需 API key、无需 Go 工具链——server 二进制从对应 release 自动下载），所有数据存在一个**全局** SQLite 文件里、被所有项目共用。

**安装 — Claude Code** —— 在 Claude Code 里逐条作为 slash 命令运行：

```text
/plugin marketplace add liliang-cn/cortexdb
/plugin install cortexdb@cortexdb
/reload-plugins
```

**安装 — Codex** —— 在 shell 里运行：

```bash
codex plugin marketplace add liliang-cn/cortexdb
codex plugin add cortexdb@cortexdb
```

Codex 使用同一个默认全局大脑：`~/.cortexdb/cortexdb.db`。

**使用** —— 直接跟 Claude 说话即可，它会替你调 MCP 工具（“记住我偏好……”、“你知道关于 X 的什么？”）；也可以用 slash 命令：`/remember <内容>`、`/recall <查询>`、`/cortexdb-graph`（交互式知识图谱查看），或 `/cortexdb` 唤起 skill。核心工具：`memory_save` / `memory_search`、`knowledge_save` / `knowledge_search`、`knowledge_graph_query`,以及统一的 `knowledge_memory_recall`。开启后，`SessionStart` 常驻指令 + `UserPromptSubmit` 自动召回 hook 会让 Claude 主动召回与保存（每台机器只问一次）。

**数据存哪** —— 默认 `~/.cortexdb/cortexdb.db`，记忆跨项目跟着你走（多会话共用,SQLite WAL 保证安全）。想按项目隔离就覆盖：

```bash
export CORTEXDB_PATH=.cortexdb/cortexdb.db   # 会被启动的 server 继承
```

如果想显式指定同一个全局大脑：

```bash
export CORTEXDB_PATH="$HOME/.cortexdb/cortexdb.db"
```

升级：`/plugin update cortexdb` 然后 `/reload-plugins`——server 二进制会自动刷新（按版本号缓存）。全部环境变量见 `plugins/cortexdb/README.md`。

## 在其他语言中使用（gRPC sidecar）

`cortexdb-grpc` 通过 gRPC 暴露完整 facade，并提供 Rust/Python/Node 的类型化客户端：

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=my.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc   # 127.0.0.1:47821
cargo add cortexdb-client   # pip install cortexdb-client   # npm install cortexdb-client
```

## 质量

检索质量是**被测量**的,不是假设的:`pkg/eval` 用标注查询集跑真实检索路径,报告 recall@k / precision@k / MRR / nDCG,并在 CI 里设回归下限(`go test ./pkg/eval -run TestLexicalRetrievalQuality -v`)。解析/检索面(FTS5、SPARQL、SQL dump 导入)有 Go fuzz 测试(`go test ./... -run Fuzz`),其保存的语料是永久回归种子。

## 示例与状态

`examples/01_core` … `15_cortex_query` 短小、面向架构（`go run ./examples/01_core`）；01-07/09/15 可独立运行，其余需要 LLM/embeddings/线上 DB——见 [examples/README.md](examples/README.md)。

一个可嵌入的 local-first AI memory/KG 库——并非 Fuseki/GraphDB/Stardog 这类完整图数据库产品的替代品。一个文件、Go API、tool/MCP 接口，以及足够构建实用记忆工作流的 RDF/SPARQL/RDFS/SHACL 能力。
