# CortexDB

[![CI/CD](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/cortexdb/v2)](https://goreportcard.com/report/github.com/liliang-cn/cortexdb/v2)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

CortexDB 是一个纯 Go、单文件的 AI memory 和 knowledge graph 库。它以 SQLite 为存储内核，提供向量检索、FTS5 lexical search、RAG knowledge 存储、agent memory workflow、RDF/SPARQL/RDFS/SHACL 知识图谱、corpus-to-graph workflow，以及 MCP/tool calling 接口。

目标很明确：给 local-first AI agent 一个可嵌入的长期记忆和知识图谱后端，不要求额外部署向量数据库、图数据库或 MCP 服务栈。

## 架构

```text
pkg/cortexdb
  主 DB facade：vectors、text search、knowledge、memory、KnowledgeMemory、KG、tools、MCP。

pkg/memoryflow
  Agent memory workflow：transcript ingest、recall、wake-up context、diary、promotion。

pkg/graphflow
  Corpus-to-graph workflow：extraction schema、build、analyze、report、export、HTML。

pkg/importflow
  外部结构化数据导入（CSV / MySQL-PG dump）到 RAG + 知识图谱基础设施，AI 辅助映射可选。

pkg/graph
  底层图引擎：property graph、RDF triples/quads、SPARQL、RDFS、SHACL。

pkg/core
  SQLite storage、embeddings、FTS5、vector indexes、chat/session primitives。
```

默认优先使用 `pkg/cortexdb`。做 agent memory UX 时用 `pkg/memoryflow`，做图谱抽取/报告/导出时用 `pkg/graphflow`，只有需要底层 RDF 或 property graph 控制时再直接用 `pkg/graph`。

## 安装

```bash
go get github.com/liliang-cn/cortexdb/v2
```

## 快速开始

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	db, err := cortexdb.Open(cortexdb.DefaultConfig("KnowledgeMemory.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	quick := db.Quick()

	_, _ = quick.Add(ctx, []float32{0.1, 0.2, 0.9}, "SQLite 是单文件数据库。")
	results, _ := quick.Search(ctx, []float32{0.1, 0.2, 0.8}, 1)

	if len(results) > 0 {
		fmt.Println(results[0].Content)
	}
}
```

## 怎么选 API

```text
需要 vectors / collections / FTS5?      -> pkg/cortexdb / pkg/core
需要 RAG knowledge 存储和检索?          -> pkg/cortexdb SaveKnowledge/SearchKnowledge
需要 chat/session memory workflow?      -> pkg/memoryflow
需要 RDF/SPARQL/RDFS/SHACL?             -> pkg/cortexdb knowledge graph APIs
需要 corpus-to-graph/report/export?     -> pkg/graphflow
需要 agent tools 或 MCP server?         -> db.GraphRAGTools() / db.NewMCPServer()
需要底层 graph 控制?                    -> pkg/graph
```

## Knowledge 和 Memory 高层 API

```go
_, _ = db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
	KnowledgeID: "apollo-plan",
	Title:       "Apollo launch plan",
	Content:     "Alice owns Apollo. Apollo ships on Friday.",
	ChunkSize:   24,
	Entities: []cortexdb.ToolEntityInput{
		{Name: "Alice", Type: "person", ChunkIDs: []string{"chunk:apollo-plan:000"}},
		{Name: "Apollo", Type: "project", ChunkIDs: []string{"chunk:apollo-plan:000"}},
	},
	Relations: []cortexdb.ToolRelationInput{
		{From: "Alice", To: "Apollo", Type: "owns"},
	},
})

resp, _ := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
	Query:         "Who owns Apollo?",
	Keywords:      []string{"Apollo", "Alice", "owns"},
	RetrievalMode: cortexdb.RetrievalModeLexical,
	TopK:          3,
})
_ = resp.Context
```

没有 embedder 时，CortexDB 会走 lexical retrieval，并使用 planner 提供的 keywords。配置 embedder 后，同一套高层 API 可以走 semantic 或 hybrid retrieval。

RAG benchmark 位于 `pkg/cortexdb`：

```bash
go test ./pkg/cortexdb -run '^$' -bench 'BenchmarkRAG' -benchmem
```

Apple M2 Pro 本地参考结果，`-benchtime=3x`：

| Benchmark | 数据集 | Time/op | 近似吞吐 | Alloc/op |
|---|---:|---:|---:|---:|
| SaveKnowledge | 1 个 document，3 个 entities，2 个 relations | ~3.26 ms | ~306 ops/s | ~75 KB |
| SearchKnowledge lexical | 500 docs，keyword plan，graph off | ~4.43 ms | ~226 QPS | ~234 KB |
| SearchKnowledge graph-light | 500 docs，entity plan，有界 graph expansion | ~8.40 ms | ~119 QPS | ~1.7 MB |
| BuildContext | 带 graph-light enrichment 的 chunk pack | ~0.41 ms | ~2,463 ops/s | ~94 KB |

## MemoryFlow

`pkg/memoryflow` 是 agent memory workflow 层。它负责把原始 transcript 存成 exchange-shaped episodic memory，做 recall，组装 wake-up layers，写 diary，恢复 transcript，并可在 session 结束时把长期事实 promote 到 knowledge。

```go
flow, _ := memoryflow.New(db, planner, extractor)

_, _ = flow.IngestTranscript(ctx, memoryflow.IngestTranscriptRequest{
	Transcript: memoryflow.Transcript{
		SessionID: "session-1",
		UserID:    "user-1",
		Source:    "chat",
		Turns: []memoryflow.TranscriptTurn{
			{Role: "user", Content: "Apollo ships on Friday."},
			{Role: "assistant", Content: "Captured."},
		},
	},
	Scope:     cortexdb.MemoryScopeSession,
	Namespace: "assistant",
})

layers, _ := flow.WakeUpLayers(ctx, memoryflow.WakeUpLayersRequest{
	Identity: "You are the Apollo project assistant.",
	Recall: memoryflow.RecallRequest{
		Query:     "startup context",
		SessionID: "session-1",
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "assistant",
	},
})
_ = layers
```

需要 LLM 的部分都是 interface：

```go
type QueryPlanner interface {
	Plan(ctx context.Context, query string, state memoryflow.SessionState) (*cortexdb.RetrievalPlan, error)
}

type SessionExtractor interface {
	Extract(ctx context.Context, transcript memoryflow.Transcript, state memoryflow.SessionState) ([]memoryflow.PromotionCandidate, error)
}
```

MemoryFlow 也可以挂可选 recall strategy。`pkg/hindsight` 现在提供了一个兼容策略插件：它用 bank/entity/keyword 信号增强 recall，但不替代 MemoryFlow 默认工作流：

```go
flow, _ := memoryflow.New(
	db,
	planner,
	extractor,
	memoryflow.WithRecallStrategy(hindsight.NewStrategy(db, hindsight.StrategyOptions{
		BankID:      "apollo-agent",
		EntityNames: []string{"Apollo"},
		Keywords:    []string{"deadline"},
		UseKG:       true,
	})),
)
```

## Knowledge Graph

CortexDB 在同一个 SQLite 文件里提供 RDF/KG 能力：

- RDF terms、triples、quads
- namespace 管理
- N-Triples / N-Quads / Turtle / TriG import/export
- 实用 SPARQL 子集
- 带 provenance 的 RDFS-lite materialized inference
- 增量 RDFS inference refresh
- SHACL-lite validation

```go
_, _ = db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
	Triples: []cortexdb.KnowledgeGraphTriple{
		{
			Subject:   graph.NewIRI("https://example.com/alice"),
			Predicate: graph.NewIRI(graph.RDFType),
			Object:    graph.NewIRI("https://example.com/Person"),
		},
		{
			Subject:   graph.NewIRI("https://example.com/alice"),
			Predicate: graph.NewIRI("https://schema.org/name"),
			Object:    graph.NewLiteral("Alice"),
		},
	},
})

result, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
	Query: `
PREFIX schema: <https://schema.org/>
SELECT ?name WHERE {
	<https://example.com/alice> schema:name ?name .
}
`,
})
_ = result
```

SPARQL 支持的是 practical embedded subset，包括：`SELECT`、`ASK`、`CONSTRUCT`、`DESCRIBE`、`INSERT DATA`、`INSERT ... WHERE`、`DELETE DATA`、`DELETE WHERE`、`DELETE ... INSERT ... WHERE`、`WITH`、`USING`、`GRAPH`、`OPTIONAL`、`UNION`、`MINUS`、`VALUES`、`BIND`、`FILTER`、`EXISTS`、`NOT EXISTS`、`REGEX`、`LANG`、`DATATYPE`、`COALESCE`、`IF`、算术表达式、`GROUP BY`、`HAVING`、`COUNT`、`SUM`、`AVG`、`MIN`、`MAX`、`SAMPLE`、`GROUP_CONCAT`、`ORDER BY`、`LIMIT`、`OFFSET`、subqueries，以及受限 property paths：`^pred`、`p|q`、`p+`、`p*`。

RDFS-lite：

```go
refresh, _ := db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{
	Mode: cortexdb.KnowledgeGraphInferenceRefreshModeIncremental,
	Triples: []cortexdb.KnowledgeGraphTriple{
		{
			Subject:   graph.NewIRI("https://example.com/Employee"),
			Predicate: graph.NewIRI("http://www.w3.org/2000/01/rdf-schema#subClassOf"),
			Object:    graph.NewIRI("https://example.com/Person"),
		},
	},
})
_ = refresh
```

SHACL-lite：

```go
report, _ := db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{
	Shapes: []cortexdb.KnowledgeGraphTriple{
		{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(graph.SHACLNodeShape)},
		{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.SHACLTargetClass), Object: graph.NewIRI("https://example.com/Person")},
	},
})
_ = report
```

当前 SHACL-lite 支持 `sh:targetClass`、`sh:targetNode`、`sh:datatype`、`sh:minCount`、`sh:maxCount`、`sh:minInclusive`、`sh:maxInclusive`、`sh:pattern`、`sh:class`、`sh:nodeKind`、`sh:in` 和 `sh:message`。

Knowledge graph benchmark 位于 `pkg/graph`：

```bash
go test ./pkg/graph -run '^$' -bench 'BenchmarkKnowledgeGraph' -benchmem
```

Apple M2 Pro 本地参考结果，`-benchtime=3x`：

| Benchmark | 数据集 | Time/op | 近似吞吐 | Alloc/op |
|---|---:|---:|---:|---:|
| RDF upsert | 唯一 person/name triple | ~0.97 ms | ~1,028 ops/s | ~37 KB |
| RDF find by predicate | 1,000 条 name triples，limit 20 | ~0.45 ms | ~2,242 QPS | ~49 KB |
| SPARQL select | 1,000 个 people 上的直接 lookup | ~0.56 ms | ~1,802 QPS | ~26 KB |
| SPARQL property path | 500-node chain 上的 `ex:knows+` | ~2.21 ms | ~453 QPS | ~2.5 MB |
| SPARQL subquery | 500 个 people 上的 grouped friend counts | ~74.45 ms | ~13 QPS | ~185 MB |
| RDFS full refresh | 25 个 class/type closure fixture | ~805.94 ms | ~1.2 ops/s | ~40 MB |
| RDFS incremental refresh | changed subclass triple fixture | ~859.85 ms | ~1.2 ops/s | ~46 MB |
| SHACL-lite validation | 500 个 people age constraints | ~139.24 ms | ~7.2 ops/s | ~6.6 MB |

这些数字只是本地参考，不是跨机器保证。RDFS 链式 fixture 和 SPARQL subquery benchmark 故意偏重，主要用于后续跟踪优化效果。当前 benchmark 套件还包含 `BenchmarkKnowledgeGraphRDFSIncrementalLocalRefresh`，用于衡量多组件图上更接近真实场景的局部增量变更。

## GraphFlow

`pkg/graphflow` 是 corpus-to-graph workflow 层：

- 统一 extraction schema：`ExtractionResult`、`ExtractionNode`、`ExtractionEdge`
- deterministic `HeuristicExtractor`
- 通过 `JSONGenerator` 接 LLM-backed extraction
- `Build`、`Analyze`、`RenderReport`
- `Export` 到 JSON/Markdown，以及 `ExportHTML`

库里只保留模型接口：

```go
type JSONGenerator interface {
	GenerateJSON(ctx context.Context, systemPrompt string, userPrompt string) ([]byte, error)
}
```

示例 `examples/05_graphflow` 展示了 `openai-go/v3` 和 JSON Schema structured output。用 `.env` 配置：

```env
OPENAI_API_KEY=...
OPENAI_BASE_URL=http://43.167.167.6:8080/v1
OPENAI_MODEL=gpt-5.4
```

运行：

```bash
go run ./examples/05_graphflow
```

## Tools 和 MCP

进程内 tool calling：

```go
tools := db.GraphRAGTools()
defs := tools.Definitions()
resp, err := tools.Call(ctx, "knowledge_graph_query", payload)
_ = defs
_ = resp
_ = err
```

MCP server：

```go
server := db.NewMCPServer(cortexdb.MCPServerOptions{})
_ = server
```

主要 tool 分组：

- GraphRAG：`ingest_document`、`search_text`、`expand_graph`、`build_context`
- Knowledge/memory：`knowledge_save`、`knowledge_search`、`memory_save`、`memory_search`
- Knowledge graph：`knowledge_graph_upsert`、`knowledge_graph_query`、`knowledge_graph_shacl_validate`、`knowledge_graph_infer_refresh`
- KnowledgeMemory：`knowledge_memory_recall`、`knowledge_memory_build_context_pack`、`knowledge_memory_reflect`、`knowledge_memory_consolidate`
- Ontology/inference：`ontology_save`、`apply_inference`

`memoryflow` 和 `graphflow` 也有独立 toolbox/MCP surface：

- memoryflow：`memoryflow_ingest_transcript`、`memoryflow_recall`、`memoryflow_wake_up_layers`、`memoryflow_prepare_reply`
- graphflow：`graphflow_build`、`graphflow_analyze`、`graphflow_report`、`graphflow_export`、`graphflow_run`

## 从 Rust 使用（gRPC sidecar）

完整 facade 也可以通过 `cortexdb-grpc` sidecar 以 gRPC 暴露，并提供类型化的
Rust 客户端 crate（[`cortexdb-client`](clients/rust/cortexdb-client)）。

启动 sidecar：

```bash
go install github.com/liliang-cn/cortexdb/v2/cmd/cortexdb-grpc@latest
CORTEXDB_PATH=my.db CORTEXDB_GRPC_TOKEN=s3cret cortexdb-grpc
# 监听 127.0.0.1:47821
```

| 环境变量 / flag | 默认值 | 含义 |
|---|---|---|
| `CORTEXDB_PATH` / `-db` | `cortexdb.db` | SQLite 文件 |
| `CORTEXDB_GRPC_ADDR` / `-addr` | `127.0.0.1:47821` | 监听地址（仅本机） |
| `CORTEXDB_GRPC_TOKEN` / `-token` | 空（不启用认证） | 每个 RPC 的 Bearer token |
| `OPENAI_BASE_URL` | 空（lexical 模式） | OpenAI 兼容 embeddings 端点 |
| `OPENAI_API_KEY` | 空 | embeddings API key |
| `CORTEXDB_EMBED_MODEL` | `text-embedding-3-small` | 嵌入模型 |
| `CORTEXDB_EMBED_DIM` | `1536` | 向量维度 |

兼容任何 OpenAI 风格端点，例如 Ollama：
`OPENAI_BASE_URL=http://localhost:11434/v1 CORTEXDB_EMBED_MODEL=embeddinggemma CORTEXDB_EMBED_DIM=768`。

Rust 侧：

```rust
use cortexdb_client::{proto, CortexClient};

let client = CortexClient::builder("http://127.0.0.1:47821")
    .token("s3cret")
    .connect()
    .await?;
client.knowledge().save_knowledge(proto::SaveKnowledgeRequest {
    knowledge_id: "k1".into(),
    content: "CortexDB from Rust over gRPC.".into(),
    ..Default::default()
}).await?;
```

服务包含：`knowledge()`、`memory()`、`graph()`（SPARQL/RDF/SHACL/推理/本体）、
`graphrag()`、`tools()`（通用工具分发，和 MCP 同一套 surface）、`admin()`。

启用 `managed-server` feature 后，crate 可以自己解析二进制（环境变量 → PATH →
从 GitHub Releases 下载并校验 sha256），用随机端口 + 随机 token 拉起 sidecar：

```rust
use cortexdb_client::sidecar::Sidecar;

let running = Sidecar::ensure().await?.spawn("my.db").await?;
let client = running.client().await?;
```

注意：token 走明文 gRPC，仅适合 localhost；跨机器请自行加 TLS 或反向代理。
v1 为单节点：一个 sidecar 管一个 SQLite 文件（多用户用文件内的 memory scope /
KG namespace / collection 隔离）。Proto 契约在 `proto/cortexdb/v1/`；Go 侧用
`scripts/gen-proto.sh` 重新生成，Rust 侧在 `clients/rust/` 下 `cargo run -p gen`。

## Optional Semantic Router

`pkg/semantic-router` 仍然作为可选工具包保留，可在 retrieval 前把用户输入路由到 handler 或 CortexDB tool。它不是 CortexDB、MemoryFlow、GraphFlow 主路径的必需依赖。

无 embedder 场景可以直接使用 lexical router：

```go
router, _ := semanticrouter.NewLexicalRouter(semanticrouter.WithSparseThreshold(0.1))
_ = router.Add(&semanticrouter.SparseRoute{
	Name:       "memory_save",
	Utterances: []string{"remember this", "save to memory"},
})
route, _ := router.Route(ctx, "please remember this preference")
_ = route.RouteName
```

## Examples

现在 examples 按架构收敛为 6 个：

```bash
go run ./examples/01_core
go run ./examples/02_rag
go run ./examples/03_memoryflow
go run ./examples/04_knowledge_graph
go run ./examples/05_graphflow
go run ./examples/06_tools_mcp
```

选择指南见 [examples/README.md](examples/README.md)。

## 状态说明

CortexDB 是一个 embedded AI memory/KG library，不是 Fuseki、GraphDB、Stardog 这类完整图数据库产品的 drop-in replacement。目标是给 agent 提供 practical local-first storage and reasoning：单文件、Go API、tool/MCP surface，以及足够实用的 RDF/SPARQL/RDFS/SHACL 能力。
