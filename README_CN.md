# sqvect

[![CI/CD](https://github.com/liliang-cn/sqvect/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/sqvect/v2/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/liliang-cn/sqvect/v2/branch/main/graph/badge.svg)](https://codecov.io/gh/liliang-cn/sqvect/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/sqvect/v2)](https://goreportcard.com/report/github.com/liliang-cn/sqvect/v2)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/sqvect/v2.svg)](https://pkg.go.dev/github.com/liliang-cn/sqvect/v2)
[![GitHub release](https://img.shields.io/github/release/liliang-cn/sqvect/v2.svg)](https://github.com/liliang-cn/sqvect/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**一个轻量级、可嵌入的 Go AI 项目向量数据库库。**

sqvect 是一个 **100% 纯 Go 库**，旨在为您的 RAG 应用提供存储内核。它在单个 SQLite 文件中提供向量存储、关键词搜索 (FTS5)、图关系和聊天内存管理功能。

## ✨ 特性

- 🪶 **轻量级** – 单个 SQLite 文件，无外部依赖。
- 🚀 **开箱即用的 RAG** – 内置 **文档**、**聊天会话** 和 **消息** 表。
- 🔍 **混合搜索** – 结合 **向量搜索 (HNSW)** + **关键词搜索 (FTS5)** 使用 RRF 融合。
- 🛡️ **安全** – 通过 **ACL** 字段和查询过滤实现行级安全 (RLS)。
- 🕸️ **图存储** – 内置知识图谱与图算法。
- 🧠 **内存高效** – **SQ8 量化** 减少 75% 内存使用。
- 🧭 **语义路由** – 基于相似度与阈值的意图路由。
- ⚡ **高性能** – 优化 WAL 模式，支持高并发访问。
- 🎯 **零配置** – 开箱即用。

## 🚀 快速开始

```bash
go get github.com/liliang-cn/sqvect/v2
```

```go
package main

import (
    "context"
    "fmt"
    "github.com/liliang-cn/sqvect/v2/pkg/core"
    "github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

func main() {
    // 1. 打开数据库 (自动为向量、文档、聊天创建表)
    db, _ := sqvect.Open(sqvect.DefaultConfig("rag.db"))
    defer db.Close()
    ctx := context.Background()

    // 2. 添加文档和向量
    // sqvect 管理文档和分块之间的关系
    db.Vector().CreateDocument(ctx, &core.Document{ID: "doc1", Title: "Go 指南"})

    db.Quick().Add(ctx, []float32{0.1, 0.2, 0.9}, "Go 很棒")

    // 3. 搜索
    results, _ := db.Quick().Search(ctx, []float32{0.1, 0.2, 0.8}, 1)
    fmt.Printf("找到: %s\n", results[0].Content)
}
```

## 💡 为什么选择 sqvect？

### 核心优势

**🎯 一体化 RAG 存储**

- 无需为向量、文档和聊天历史管理多个数据库
- 单个 SQLite 文件 = 轻松备份、迁移和版本控制
- 非常适合边缘部署和本地优先应用

**🚀 开发者体验**

- 零配置 - 开箱即用
- 类型安全的 Go API，支持完整的 IntelliSense
- 内置 RAG 架构 (无需 ORM/SQL)
- 丰富示例覆盖常见用例

**⚡ 性能与效率**

- SQ8 量化减少 75% 内存 (100万向量 ~1GB)
- 多种索引类型 (HNSW, IVF, LSH) 适应不同工作负载
- WAL 模式 + 连接池实现并发访问
- 高效的距离计算

**🔒 安全优先**

- 行级安全 (ACL) 内置在核心中
- 用户作用域查询强制执行权限边界
- 租户之间无数据泄露

**🧪 生产就绪**

- 核心 API 测试覆盖率 93%
- 经过实战验证的算法 (HNSW, RRF, PQ)
- CI/CD + Codecov + Go Report Card 徽章
- MIT 许可证便于集成

## 🧠 Hindsight：AI 智能体记忆系统

sqvect 内置 **Hindsight**，一个仿生记忆系统，用于让智能体在多轮交互中长期学习与改进。

### 三大核心操作

```go
import "github.com/liliang-cn/sqvect/v2/pkg/hindsight"

sys, _ := hindsight.New(&hindsight.Config{DBPath: "agent_memory.db"})

// RETAIN：写入记忆（调用方提供向量）
sys.Retain(ctx, &hindsight.Memory{
    Type:     hindsight.WorldMemory,
    Content:  "Alice works at Google as a senior engineer",
    Vector:   embedding,
    Entities: []string{"Alice", "Google"},
})

// RECALL：基于 TEMPR 策略检索
results, _ := sys.Recall(ctx, &hindsight.RecallRequest{
    BankID:      "agent-1",
    QueryVector: queryEmbedding,
    Strategy:    hindsight.DefaultStrategy(),
})

// OBSERVE：反思生成新洞察
resp, _ := sys.Observe(ctx, &hindsight.ReflectRequest{
    BankID:      "agent-1",
    Query:       "What does Alice prefer?",
    QueryVector: queryEmbedding,
})
// resp.Observations 包含新洞察
```

### 四种记忆类型

| 类型            | 描述           | 示例                           |
| :-------------- | :------------- | :----------------------------- |
| **World**       | 世界客观事实   | "Alice works at Google"        |
| **Bank**        | 智能体自身经历 | "I recommended Python to Bob"  |
| **Opinion**     | 带置信度的观点 | "Python is best for ML" (0.85) |
| **Observation** | 反思生成的洞察 | "Users prefer concise answers" |

### TEMPR 检索策略

Hindsight 同时运行四种检索并用 RRF 融合：

- **T**emporal – 时间范围过滤
- **E**ntity – 基于实体关系图
- **M**emory – 向量语义相似度
- **P**riming – 关键词/BM25 精确匹配
- **R**ecall – RRF 融合排序

### 记忆银行与性格倾向

```go
bank := hindsight.NewBank("agent-1", "Assistant Agent")
bank.Skepticism = 3  // 1=易信, 5=怀疑
bank.Literalism = 3  // 1=灵活, 5=字面
bank.Empathy = 4     // 1=冷静, 5=共情
sys.CreateBank(ctx, bank)
```

**为什么需要 Hindsight**

- 智能体会形成带置信度的**观点**，而非只检索事实
- **性格倾向**影响反思与洞察生成
- 记忆可跨会话持续积累
- 纯记忆系统，无 LLM 依赖（调用方负责嵌入）

### 可扩展 Hooks

两个注入点让您接入任意 LLM 或模型，而无需与特定提供商耦合。

**Hook 1 — `FactExtractorFn`：自动从对话提取事实**

```go
sys.SetFactExtractor(func(ctx context.Context, bankID string, msgs []*core.Message) ([]hindsight.ExtractedFact, error) {
    // 调用您的 LLM / 模型提取结构化事实并计算嵌入
    return []hindsight.ExtractedFact{
        {ID: "lang_pref", Type: hindsight.WorldMemory,
         Content: "Alice 偏好 Go", Vector: embed("Alice 偏好 Go")},
    }, nil
})

// 传入原始对话消息，自动完成提取与存储
result, err := sys.RetainFromText(ctx, "agent-1", messages)
// result.Retained / result.Skipped / result.Err()
```

**Hook 2 — `RerankerFn`：RRF 之后的 Cross-Encoder 重排**

```go
sys.SetReranker(func(ctx context.Context, query string, candidates []*hindsight.RecallResult) ([]*hindsight.RecallResult, error) {
    // 调用 Cohere Rerank / 本地 Cross-Encoder
    scores := crossEncoder.Score(query, texts(candidates))
    sort.Slice(candidates, func(i, j int) bool { return scores[i] > scores[j] })
    return candidates, nil
})
// Recall() 自动应用重排；出错时静默回退到 RRF 顺序。
```

## 🏗 企业级 RAG 能力

## 🧭 语义路由（意图路由）

在调用 LLM 前先做意图分类与路由。

```go
import (
    "context"
    "fmt"

    "github.com/liliang-cn/sqvect/v2/pkg/core"
    semanticrouter "github.com/liliang-cn/sqvect/v2/pkg/semantic-router"
)

embedder := semanticrouter.NewMockEmbedder(1536)
router, _ := semanticrouter.NewRouter(
    embedder,
    semanticrouter.WithThreshold(0.82),
    semanticrouter.WithSimilarityFunc(core.CosineSimilarity),
)

router.Add(&semanticrouter.Route{
    Name:       "refund",
    Utterances: []string{"我要退款", "申请退款"},
})

result, _ := router.Route(context.Background(), "我要退款")
fmt.Printf("Route: %s, Score: %.4f, Matched: %v\n", result.RouteName, result.Score, result.Matched)
```

sqvect 超越简单的向量存储，为复杂的 RAG 应用提供架构和 API。

### 1. 混合搜索 (向量 + 关键词)

使用倒数排名融合 (RRF) 将语义理解与精确关键词匹配结合。

```go
// 搜索 "apple" (关键词) 并结合向量相似度
results, _ := db.Vector().HybridSearch(ctx, queryVec, "apple", core.HybridSearchOptions{
    TopK: 5,
    RRFK: 60, // 融合参数
})
```

### 2. 聊天内存管理

直接在数据旁边存储对话历史。

```go
// 1. 创建会话
db.Vector().CreateSession(ctx, &core.Session{ID: "sess_1", UserID: "user_123"})

// 2. 添加消息 (用户和助手)
db.Vector().AddMessage(ctx, &core.Message{
    SessionID: "sess_1",
    Role:      "user",
    Content:   "什么是 sqvect？",
})

// 3. 检索历史用于上下文窗口
history, _ := db.Vector().GetSessionHistory(ctx, "sess_1", 10)
```

### 3. 行级安全 (ACL)

在数据库级别强制执行权限。

```go
// 插入受限文档
db.Vector().Upsert(ctx, &core.Embedding{
    ID: "secret_doc",
    Vector: vec,
    ACL: []string{"group:admin", "user:alice"}, // 仅管理员和爱丽丝
})

// 使用用户上下文搜索 (自动过滤结果)
results, _ := db.Vector().SearchWithACL(ctx, queryVec, []string{"user:bob"}, opts)
// 对 Bob 返回空结果！
```

### 4. 文档管理

跟踪源文件、版本和元数据。删除文档会自动删除其所有向量分块 (级联删除)。

```go
db.Vector().CreateDocument(ctx, &core.Document{
    ID: "manual_v1",
    Title: "用户手册",
    Version: 1,
})
// ... 添加链接到 "manual_v1" 的嵌入 ...

// 一键删除文档及其所有嵌入
db.Vector().DeleteDocument(ctx, "manual_v1")
```

## 📚 数据库架构

sqvect 为您管理以下表：

| 表             | 描述                                     |
| :------------- | :--------------------------------------- |
| `embeddings`   | 向量、内容、JSON 元数据、ACL。           |
| `documents`    | 向量的父级记录 (标题、URL、版本)。       |
| `sessions`     | 聊天会话/线程。                          |
| `messages`     | 聊天日志 (角色、内容、时间戳)。          |
| `messages_fts` | 消息 BM25 关键词搜索的 **FTS5** 虚拟表。 |
| `collections`  | 逻辑命名空间 (多租户)。                  |
| `chunks_fts`   | 向量内容关键词搜索的 **FTS5** 虚拟表。   |
| `graph_nodes`  | 知识图谱节点（含向量嵌入）。             |
| `graph_edges`  | 图节点间的有向关系。                     |

## 📊 性能 (128 维)

| 索引类型 | 插入速度      | 搜索 QPS   | 内存 (100万向量) |
| :------- | :------------ | :--------- | :--------------- |
| **HNSW** | ~580 ops/s    | ~720 QPS   | ~1.2 GB (SQ8)    |
| **IVF**  | ~14,500 ops/s | ~1,230 QPS | ~1.0 GB (SQ8)    |

_在 Apple M2 Pro 上测试。_

## 🎯 最佳使用场景

### 非常适合 ✅

| 使用场景              | 为什么选择 sqvect？               |
| :-------------------- | :-------------------------------- |
| **本地优先 RAG 应用** | 单文件、无服务器、离线工作        |
| **边缘 AI 设备**      | 低内存 (SQ8)、无外部依赖、纯 Go   |
| **个人知识库**        | 简单备份 (复制文件)、易于查询     |
| **内部工具**          | 快速设置、无 DevOps 开销          |
| **聊天内存系统**      | 内置会话/消息表                   |
| **多租户 SaaS**       | ACL + Collections 实现隔离        |
| **文档聚类**          | 图算法 (PageRank、社区检测)       |
| **混合搜索应用**      | 向量 + FTS5 使用 RRF 融合         |
| **原型到生产**        | 同一套代码从开发到生产 (只需扩展) |

### 不推荐用于 ❌

| 场景           | 更好的替代方案              |
| :------------- | :-------------------------- |
| >1亿向量       | Milvus, Qdrant (分布式)     |
| <10ms 延迟要求 | 基于 Redis 的向量数据库     |
| 多区域高可用   | 云原生向量数据库 (Pinecone) |
| 非 Go 团队     | Chroma (Python), Weaviate   |

### 真实世界示例

- **法律文档分析**：存储合同、条款和案例法并带有元数据过滤
- **客户支持聊天机器人**：持久对话历史 + 知识库搜索
- **代码搜索引擎**：语义代码搜索 + 语法感知过滤
- **研究论文图**：引用网络 + 向量相似度
- **电商推荐**：用户嵌入 + 产品图

## 📊 与竞品对比

### 向量数据库对比

| 特性           |    sqvect     |   Chroma    |  Weaviate   |   Milvus    |   Qdrant    |
| :------------- | :-----------: | :---------: | :---------: | :---------: | :---------: |
| **架构**       |    嵌入式     |   服务器    |   服务器    |   分布式    |   服务器    |
| **语言**       |      Go       |   Python    |     Go      |     Go      |    Rust     |
| **依赖**       |   仅 SQLite   |   DuckDB    | Vector+Obj  |    很多     |    很多     |
| **设置时间**   |     ~1 秒     |   ~5 分钟   |  ~10 分钟   |  ~30 分钟   |  ~10 分钟   |
| **向量搜索**   |      ✅       |     ✅      |     ✅      |     ✅      |     ✅      |
| **关键词搜索** |    ✅ FTS5    |     ❌      |     ⚠️      |     ❌      |     ❌      |
| **图数据库**   |    ✅ 内置    |     ❌      |     ❌      |     ❌      |     ❌      |
| **RAG 表**     |    ✅ 就绪    | ❌ 自行实现 | ❌ 自行实现 | ❌ 自行实现 | ❌ 自行实现 |
| **ACL/安全**   |    ✅ 行级    |     ❌      |     ⚠️      |     ⚠️      |     ⚠️      |
| **量化**       | SQ8/PQ/Binary |     ❌      |     ✅      |     ✅      |     ✅      |
| **可扩展性**   |    <1000万    |    <1亿     |    <10亿    |    >10亿    |    <10亿    |
| **备份**       |   复制文件    |    导出     |    快照     |    复杂     |    快照     |
| **适合场景**   |   边缘/本地   |  Python ML  |    企业     |   大数据    |    生产     |

### 何时选择 sqvect？

**选择 sqvect 如果：**

- ✅ 您想要 **单文件** 数据库 (无独立服务)
- ✅ 您正在构建 **本地优先** 或 **边缘 AI** 应用
- ✅ 您需要 **内置 RAG 架构** (文档、会话、消息)
- ✅ 您想要 **图算法** 而无需 Neo4j
- ✅ 您重视 **简单性** 超过水平可扩展性
- ✅ 您目标 **<1000 万向量**

**选择替代方案如果：**

- ❌ 您需要 **分布式** 部署跨越多个节点
- ❌ 您有 **>1亿向量** 并需要水平扩展
- ❌ 您要求 **<10ms** 查询延迟
- ❌ 您的团队不使用 Go (偏好 Python/TypeScript SDK)

### 独特差异化

🎯 **没有其他向量数据库能结合：**

1. 向量 + 图 + 文档 + 聊天 在一个文件中
2. 内置 RAG 架构 (无需设计工作)
3. 无需外部认证的行级安全
4. 边缘部署就绪 (无需网络/容器)
5. 纯 Go (跨平台编译到任何平台)

## ⚖️ 许可证

MIT 许可证。请参阅 [LICENSE](LICENSE) 文件。
