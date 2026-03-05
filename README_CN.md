# CortexDB

[![CI/CD](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/cortexdb/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/liliang-cn/cortexdb/v2/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/cortexdb/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/cortexdb/v2)](https://goreportcard.com/report/github.com/liliang-cn/cortexdb/v2)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/cortexdb/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/cortexdb/v2)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**为 AI 智能体打造的可嵌入式认知记忆与图数据库。**

CortexDB 是一个 **100% 纯 Go 库**，旨在将单个 SQLite 文件转化为强大的 AI 存储引擎。它无缝融合了 **混合向量搜索**、**GraphRAG** 和 **仿生智能体记忆系统 (Hindsight)**，无需复杂的外部基础设施，即可赋予您的 AI 应用一个结构化、持久化且智能的“大脑”。

## ✨ 为什么选择 CortexDB？

- 🧠 **智能体记忆 (Hindsight)** – 完整的 `retain → recall → reflect` 生命周期，内置多通道 TEMPR 检索。
- 🕸️ **GraphRAG 就绪** – 内置知识图谱的 `节点` 和 `边`，支持复杂的关系推导和遍历。
- 🔍 **混合搜索** – 结合 向量相似度 (HNSW) 和 精确关键词匹配 (FTS5) 使用 RRF 融合。
- 🏗️ **结构化数据友好** – 轻松将 SQL/CSV 数据映射为自然语言 + 元数据，支持高级 `PreFilter` 过滤。
- 🪶 **超轻量级** – 单个 SQLite 文件，零外部依赖，纯 Go 语言实现。
- 🛡️ **安全隔离** – 通过 **ACL** 字段实现行级安全 (RLS)，轻松支持多租户。

## 🚀 快速开始

```bash
go get github.com/liliang-cn/cortexdb/v2
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	// 初始化 CortexDB (自动创建向量、文档、记忆和图数据表)
	db, err := cortexdb.Open(cortexdb.DefaultConfig("brain.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// 1. 存储一个事实/记忆
	db.Quick().Add(ctx, []float32{0.1, 0.2, 0.9}, "Go 是一门静态类型的编译型语言。")

	// 2. 召回相关的概念
	results, _ := db.Quick().Search(ctx, []float32{0.1, 0.2, 0.8}, 1)
	if len(results) > 0 {
		fmt.Printf("找到记忆: %s\n", results[0].Content)
	}
}
```

## 🏗 核心能力

### 1. 智能体记忆系统 (Hindsight)

CortexDB 内置 `hindsight`，一个专为 Agent 打造的仿生记忆模块。它不仅仅是存储日志，而是将记忆分类（客观事实、信念、洞察）并动态召回。

```go
import "github.com/liliang-cn/cortexdb/v2/pkg/hindsight"

sys, _ := hindsight.New(&hindsight.Config{
	DBPath: "agent_memory.db",
})
defer sys.Close()

// 创建一个具有性格特征的 Agent 大脑
bank := hindsight.NewBank("travel-agent-1", "Travel Assistant")
bank.Empathy = 4    // 1-5 级，同理心
bank.Skepticism = 2 // 1-5 级，怀疑度
sys.CreateBank(ctx, bank)

// RETAIN：存入关于用户的结构化观察
sys.Retain(ctx, &hindsight.Memory{
	BankID:   "travel-agent-1",
	Type:     hindsight.WorldMemory,
	Content:  "Alice 偏好靠窗的座位，并且吃素。",
	Vector:   embedding,
	Entities: []string{"user:alice", "preference:flight", "preference:food"},
})

// RECALL：基于 TEMPR 模型的多通道检索
results, _ := sys.Recall(ctx, &hindsight.RecallRequest{
	BankID:      "travel-agent-1",
	QueryVector: queryEmbedding,
	Strategy:    hindsight.DefaultStrategy(), // 启用所有通道并进行 RRF 融合
})
```

### 2. 高级文本与结构化数据 API

告别手动处理 `[]float32` 数组的痛苦。只需注入您的 Embedding 模型，剩下的交给 CortexDB。

```go
// 1. 注入您的嵌入模型 (OpenAI, Ollama 等)
db, _ := cortexdb.Open(config, cortexdb.WithEmbedder(myOpenAIEmbedder))

// 2. 直接插入原始文本
db.InsertText(ctx, "doc_1", "新款 iPhone 15 Pro 采用了钛金属机身。", map[string]string{
	"category": "electronics",
	"price":    "999",
})

// 3. 纯文本语义搜索
results, _ := db.SearchText(ctx, "最新的苹果手机", 5)

// 4. 混合搜索 (语义向量 + 精确关键词)
hybridRes, _ := db.HybridSearchText(ctx, "钛金属机身", 5)

// 5. 纯 FTS5 搜索 (无需依赖向量引擎，速度极快！)
ftsRes, _ := db.SearchTextOnly(ctx, "iPhone", cortexdb.TextSearchOptions{TopK: 5})
```
*参考 `examples/text_api` 获取完整代码。*

### 3. GraphRAG (知识图谱)

将关系型数据（如 SQL 表）直接转化为知识图谱，实现多跳推理 (Multi-hop reasoning)。

```go
// 1. 插入图节点
db.Graph().UpsertNode(ctx, &graph.GraphNode{
	ID: "dept_eng", NodeType: "department", Content: "工程研发部门", Vector: vec1,
})
db.Graph().UpsertNode(ctx, &graph.GraphNode{
	ID: "emp_alice", NodeType: "employee", Content: "Alice (高级 Go 工程师)", Vector: vec2,
})

// 2. 建立关系 (边)
db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
	FromNodeID: "emp_alice",
	ToNodeID:   "dept_eng",
	EdgeType:   "BELONGS_TO",
	Weight:     1.0,
})

// 3. 在 RAG 中自动遍历图谱
neighbors, _ := db.Graph().Neighbors(ctx, "emp_alice", graph.TraversalOptions{
	EdgeTypes: []string{"BELONGS_TO"},
	MaxDepth:  1,
})
// 轻松找到 Alice 属于工程研发部门，无需编写复杂的 SQL JOIN！
```
*参考 `examples/structured_data` 获取完整代码。*

### 4. 高级元数据过滤

在执行向量搜索前，使用类似 SQL 的表达式构建器过滤庞大的数据集。

```go
// 查找价格低于 2000 且目前有库存的笔记本电脑
filter := core.NewMetadataFilter().
	And(core.NewMetadataFilter().Equal("category", "laptop")).
	And(core.NewMetadataFilter().LessThan("price", 2000.0)).
	And(core.NewMetadataFilter().GreaterThan("stock", 0)).
	Build()

opts := core.AdvancedSearchOptions{
	SearchOptions: core.SearchOptions{TopK: 5},
	PreFilter:     filter, // 过滤发生在向量计算之前！
}
results, _ := db.Vector().SearchWithAdvancedFilter(ctx, queryVec, opts)
```

## 📚 数据库架构

CortexDB 在单个 `.db` 文件中自动管理以下表结构：

| 表             | 描述                                     |
| :------------- | :--------------------------------------- |
| `embeddings`   | 核心向量、内容、JSON 元数据、ACL。       |
| `documents`    | 向量的父级记录 (标题、URL、版本)。       |
| `sessions`     | 聊天会话/线程。                          |
| `messages`     | 聊天日志 (角色、内容、向量、时间戳)。    |
| `messages_fts` | 消息 BM25 关键词搜索的 **FTS5** 虚拟表。 |
| `collections`  | 逻辑命名空间，用于多租户隔离。           |
| `chunks_fts`   | 向量内容混合搜索的 **FTS5** 虚拟表。     |
| `graph_nodes`  | 知识图谱节点（包含关联的向量）。         |
| `graph_edges`  | 图节点之间的有向关系与权重。             |

## 📊 性能 (128维, Apple M2 Pro)

| 索引类型 | 插入速度      | 搜索 QPS   | 内存占用 (100万向量) |
| :------- | :------------ | :--------- | :------------------- |
| **HNSW** | ~580 ops/s    | ~720 QPS   | ~1.2 GB (SQ8)        |
| **IVF**  | ~14,500 ops/s | ~1,230 QPS | ~1.0 GB (SQ8)        |

## ⚖️ 许可证

MIT License. 请参阅 [LICENSE](LICENSE) 文件。