# CortexDB

[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2) [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

纯 Go、单文件的 AI memory 和知识图谱库。SQLite 为存储内核——一个文件即承载向量、lexical/RAG 检索、分作用域的 agent memory、RDF/SPARQL/RDFS/SHACL 知识图谱，以及 MCP tools。为需要长期记忆的 local-first agent 而生，无需额外部署向量库、图数据库或 MCP 服务栈。支持无 embedder（lexical 模式）或任意 OpenAI 兼容的 embeddings 端点。

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

作为 Claude Code / Codex 插件安装（打包 skill + MCP server，lexical 模式，无需 API key）：

```bash
/plugin marketplace add liliang-cn/cortexdb && /plugin install cortexdb@cortexdb            # Claude Code
codex plugin marketplace add liliang-cn/cortexdb && codex plugin install cortexdb@cortexdb  # Codex
```

## 在其他语言中使用（gRPC sidecar）

`cortexdb-grpc` 通过 gRPC 暴露完整 facade，并提供 Rust/Python/Node 的类型化客户端：

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=my.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc   # 127.0.0.1:47821
cargo add cortexdb-client   # pip install cortexdb-client   # npm install cortexdb-client
```

## 示例与状态

`examples/01_core` … `15_cortex_query` 短小、面向架构（`go run ./examples/01_core`）；01-07/09/15 可独立运行，其余需要 LLM/embeddings/线上 DB——见 [examples/README.md](examples/README.md)。

一个可嵌入的 local-first AI memory/KG 库——并非 Fuseki/GraphDB/Stardog 这类完整图数据库产品的替代品。一个文件、Go API、tool/MCP 接口，以及足够构建实用记忆工作流的 RDF/SPARQL/RDFS/SHACL 能力。
