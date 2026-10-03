# CortexDB

[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2) [![CI](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml) [![codecov](https://codecov.io/gh/liliang-cn/cortexdb/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/cortexdb) [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

一个 SQLite 文件装下 AI 记忆与知识图谱。纯 Go，不用额外跑服务，没有 embedding 模型也能用。

![CortexDB 实时视图](docs/assets/img/live-view-dark-1600.webp)

## 安装

| | |
| --- | --- |
| Go 库 | `go get github.com/liliang-cn/cortexdb/v2` |
| Claude Code | `/plugin marketplace add liliang-cn/cortexdb`，再 `/plugin install cortexdb@cortexdb` |
| Codex | `codex plugin marketplace add liliang-cn/cortexdb && codex plugin add cortexdb@cortexdb` |
| 共享大脑服务端 | `go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest` |
| 客户端 | `cargo add cortexdb-client` · `pip install cortexdb-client` · `npm install cortexdb-client` |

## 使用

```go
db, _ := cortexdb.Open(cortexdb.DefaultConfig("brain.db"))
defer db.Close()
brain := db.KnowledgeMemory()
_, _ = brain.Remember(ctx, cortexdb.KnowledgeMemoryRememberRequest{Content: "Alice 偏好 tab 缩进。", Scope: "user"})
rec, _ := brain.Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{Query: "Alice 偏好什么?"})
fmt.Println(rec.ContextPack.Text)
```

插件给 Claude Code 和 Codex 一个全局大脑 `~/.cortexdb/cortexdb.db`，带 `/remember`、`/recall` 和自动召回 hook。多个 agent、多台机器指向同一个 `cortexdb-grpc`，就共享同一份记忆和图谱。

## 里面有什么

- 向量（HNSW、IVF、Flat、二值编码）、FTS5 全文检索、混合检索与图检索
- RAG 知识、分作用域的 agent 记忆、带来源的上下文包
- RDF 1.2 知识图谱：SPARQL、RDFS + OWL 2 RL 推理、SHACL、只读 Cypher
- Palantir 风格的 ontology，带受治理的动作
- 80+ 工具，进程内调用或走 MCP
- `serve_graph_3d`：实时 3D 视图，可查找、提问、查询、展开，桌面和手机都能用
- 变更事件流：每次已提交的写入，按顺序、恰好一次
- 默认 SQLite，换成 `postgres://` DSN 就跑在 PostgreSQL + pgvector 上

## 环境变量

| 变量 | 含义 |
| --- | --- |
| `CORTEXDB_PATH` | 数据库文件（默认 `~/.cortexdb/cortexdb.db`） |
| `CORTEXDB_REMOTE` | 连到 `host:port` 上的共享 `cortexdb-grpc`，不用本地文件 |
| `CORTEXDB_GRPC_TOKEN` | 该服务的 Bearer token |
| `CORTEXDB_GRPC_ADDR` | 服务端监听地址（默认 `127.0.0.1:47821`） |
| `CORTEXDB_EMBED_BASE_URL` | OpenAI 兼容的 embeddings 端点；不设就是词法模式 |
| `CORTEXDB_EMBED_MODEL` / `CORTEXDB_EMBED_DIM` | embedding 模型和维度 |

## 文档

[官网](https://liliang-cn.github.io/cortexdb/zh/) · [指南](docs/GUIDE_CN.md)（完整功能一览） · [示例](examples/README.md) · [更新日志](CHANGELOG.md) · [English](README.md)

## License

MIT
