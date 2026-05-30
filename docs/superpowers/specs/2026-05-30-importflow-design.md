# importflow — 外部数据导入工作流层设计

- 状态: 已批准设计，待实现计划
- 日期: 2026-05-30
- 包: `pkg/importflow`（新建，与 `memoryflow`/`graphflow` 平级的 opt-in 工作流层）

## 目标

支持把外部结构化数据（MySQL/PG dump、CSV）一次导入 CortexDB，**同时**建立：

- RAG 基础：可语义/词法检索的文本块（向量 + FTS5）
- 知识图谱基础：实体 + 关系三元组

导入过程"配合 AI"：AI 可参与四个环节——推断映射、抽取三元组、生成向量、生成摘要/清洗。所有 LLM 能力通过接口注入，`pkg/` 不引入任何 LLM SDK（遵守仓库约定）。

## 非目标（YAGNI）

- 不在 `pkg/` 内置 live-DB driver（mysql/pq）依赖；live-DB 通过用户自实现 `Source` + `examples/` 参考完成。
- 不做完整的 SQL 方言解析器；dump 解析只覆盖常见子集，无法解析的语句**收集上报**而非静默丢弃。
- 暂不做 CLI（`cmd/cortexdb-import` 留作后续，复用本库）。

## 架构与分层

```
pkg/core         engine
pkg/graph        engine
pkg/cortexdb     facade（RAG/KG/memory 写入入口）
pkg/graphflow    corpus→KG 抽取（被 importflow 复用）
pkg/importflow   ← 本设计：Source → MappingPlan → 双 sink(RAG+KG)
```

`importflow` 依赖 `pkg/cortexdb` facade（写入）与 `pkg/graphflow`（三元组抽取 / 默认 AI 实现）。所有 LLM 依赖为接口。

## 包内文件布局（topic 前缀，扁平）

```
pkg/importflow/
  doc.go            包说明
  types.go          Record / Column / Schema / Report / Goal
  source.go         Source 接口
  source_csv.go     CSVSource（内置）
  source_dump.go    SQLDumpSource（内置，纯文本 INSERT/COPY 解析）
  plan.go           MappingPlan / TablePlan / RAGPlan / KGPlan / EntityMap / RelationMap / TextExtract
  infer.go          MappingInferer 接口 + 基于 JSONGenerator 的默认实现
  refine.go         TextRefiner 接口 + 默认实现
  sink_rag.go       RAGSink
  sink_kg.go        KGSink
  importer.go       Importer 编排：New / Plan / Run / AutoImport / Options
  toolbox.go        importflow_plan / importflow_run（MCP/工具面）
  *_test.go         同目录测试
examples/07_importflow/   真实 LLM 客户端示例（隔离 SDK 依赖）
```

## 核心类型

```go
type Column struct { Name, Type string } // Type 尽力而为（int/text/timestamp...）

type Record struct {
    Table  string
    Values map[string]string // 列名 → 字符串值
    Nulls  map[string]bool   // 该列是否 NULL
    Row    int               // 表内行序号
}

type Schema struct { // 供 AI 推断/规划
    Table   string
    Columns []Column
    Sample  []Record // 前 N 行样本
}

type Goal struct {
    BuildRAG bool
    BuildKG  bool
    Hint     string // 领域提示、目标实体类型、语言等
}
```

## Source 层

```go
type Source interface {
    Schemas(ctx context.Context) ([]Schema, error)            // 表结构 + 样本（供 AI 规划）
    Records(ctx context.Context, fn func(Record) error) error // 流式产出全部行；fn 返回 err 则中止
    Close() error
}
```

两段式契约：先 `Schemas()` 给 AI 规划，再 `Records()` 流式执行。

内置实现：

- `NewCSVSource(r io.Reader, opts CSVOptions)` — header 行、分隔符、表名；类型尽力推断。
- `NewSQLDumpSource(r io.Reader, opts DumpOptions)` — 流式识别 `CREATE TABLE`、`INSERT INTO ... VALUES (...),(...)`、PG `COPY ... FROM stdin; ... \.`；`Dialect ∈ {MySQL, Postgres, Auto}`；处理常见引号/转义子集；无法解析的语句收集进 `Report.UnparsedStatements`。

live-DB 适配器**不进 pkg**：用户实现 `Source`，仓库在 `examples/` 提供参考实现（含 driver 依赖）。

## RAG vs KG 的判定模型

不是二选一——同一行可同时喂两边，由三层决策：

1. Goal（总开关）：`BuildRAG` / `BuildKG` / 两者。
2. 列角色分类（核心）：

   | 列特征 | 判定 | 流向 |
   |---|---|---|
   | 长自由文本（text/大 varchar） | 文档内容 | RAG 主体 + 可选 AI 抽三元组 |
   | 主键 / 唯一 id | 实体标识 | KG 实体 id |
   | 外键（`*_id` 对上他表主键） | 关系端点 | KG 关系 |
   | 低基数枚举（status/category） | 属性/分类 | KG 属性 or 节点 |
   | 数值 / 时间戳 | 元数据/属性 | RAG metadata + KG property |
   | 整表 `(id_a, id_b[, attrs])` | 连接表 | KG 边（一行一关系） |
   | 整表 `(id, 大文本, 属性)` | 文档表 | RAG 文档 |

3. 谁来填：AI `MappingInferer` 生成 `MappingPlan`（用户可编辑），或用户手写。

判定结果固化为 `MappingPlan`。

## MappingPlan

```go
type MappingPlan struct{ Tables map[string]TablePlan }

type TablePlan struct {
    Skip bool
    RAG  *RAGPlan
    KG   *KGPlan
}

type RAGPlan struct {
    Namespace   string   // SaveKnowledge 命名空间
    ContentTmpl string   // 如 "{title}\n\n{body}"，按列拼内容
    IDColumn    string   // 稳定 doc id；缺省用 "table:row"
    Metadata    []string // 进 metadata 的列
    Refine      bool     // 嵌入前过 TextRefiner
}

type KGPlan struct {
    Entities    []EntityMap   // 列 → 实体
    Relations   []RelationMap // 实体ref --predicate--> 实体ref（结构化，无 LLM）
    TextExtract []TextExtract // 自由文本列 → AI 抽三元组（graphflow）
}

type EntityMap struct {
    Ref       string   // 行内局部句柄，如 "customer"
    Type      string   // 实体类型/类
    IDTmpl    string   // "{customer_id}"
    LabelTmpl string   // "{customer_name}"
    Props     []string // 作为属性的列
}

type RelationMap struct {
    Subject   string // EntityMap.Ref
    Predicate string // 边类型 / RDF 谓词
    Object    string // EntityMap.Ref
}

type TextExtract struct {
    Column string   // 自由文本列名
    Types  []string // 可选：提示抽取关注的实体类型
}
```

## AI 接口（全部可选；不注入则降级）

```go
type MappingInferer interface {
    InferPlan(ctx context.Context, schemas []Schema, goal Goal) (MappingPlan, error)
}

type TextRefiner interface {
    Refine(ctx context.Context, table, column, raw string) (string, error)
}
```

复用：

- `cortexdb.Embedder` — RAG 向量（可选，缺省走 FTS5）。
- `graphflow.JSONGenerator` — 三元组抽取，并作为默认 `MappingInferer` 与 `TextRefiner` 的底层实现。

设计要点：用户只需注入**一个** `graphflow.JSONGenerator` 即可驱动默认的推断 + 清洗 + 抽取；Embedder 单独注入或省略。

## Sink

- `RAGSink` → 批量 `cortexdb.InsertTextBatch` / `SaveKnowledge`（默认批 500）。
- `KGSink` → 结构化关系直接写 `kg_triples` / graph nodes-edges；自由文本列经 `graphflow extract` 出三元组后写入。

## Importer 编排 API

```go
func New(db *cortexdb.DB, opts ...Option) *Importer

func (im *Importer) Plan(ctx context.Context, src Source, goal Goal) (MappingPlan, error)   // AI 推断，可改
func (im *Importer) Run(ctx context.Context, src Source, plan MappingPlan) (*Report, error) // 执行
func (im *Importer) AutoImport(ctx context.Context, src Source, goal Goal) (*Report, error) // Plan+Run
```

Options（函数式）：`WithMappingInferer`、`WithTextRefiner`、`WithExtractor`、`WithBatchSize`、`WithStrictMode`。

默认行为：两段式（`Plan` → 人审 → `Run`）；`AutoImport` 为便捷一步式。

## 数据流

```
Source.Schemas ─▶ MappingInferer.InferPlan ─▶ MappingPlan(可编辑)
                                                   │
Source.Records ─▶ Mapper(plan) ─┬─▶ RAG块 ─(Refine?)-(Embed?)─▶ RAGSink ─▶ InsertTextBatch
                                └─▶ 实体+关系 ───────────────▶ KGSink ─▶ kg_triples/graph
                                     自由文本列 ─▶ extractor ─▶ 三元组 ─▶ KGSink
```

## Report 与透明度

```go
type Report struct {
    RowsRead            int
    ChunksIndexed       int
    TriplesCreated      int
    Skipped             int
    UnparsedStatements  []string // dump 中无法解析的语句（不静默丢弃）
    Errors              []error  // 非严格模式下收集的坏行错误
}
```

遵守 "no silent caps"：未解析语句、跳过项、降级行为都在 Report 中显式呈现。

## 错误处理与降级

| 缺什么 / 出错 | 行为 |
|---|---|
| 无 Embedder | RAG 走 FTS5 词法检索 |
| 无 JSONGenerator | 跳过 AI 推断/抽取/清洗；需用户手写 plan；TextExtract 在 Report 标注跳过 |
| dump 未知语句 | 收集进 `Report.UnparsedStatements` |
| 坏行 | 默认收集进 `Report.Errors` 不中断；`StrictMode` 下中断 |

流式 + 批处理（默认 500 行/批）。

## Toolbox / MCP

`importflow.NewToolbox(im)` 暴露 `importflow_plan`、`importflow_run`，与 `graphflow`/`memoryflow` 工具面风格一致。

## 测试策略

- CSV/dump source：表驱动；小 MySQL/PG dump fixture，断言记录数 + `UnparsedStatements` 上报。
- Mapper：plan → 期望块/三元组（无 LLM）。
- Sink：against in-memory cortexdb，无 embedder（词法）+ fake embedder 两路。
- AI 接口：fake `MappingInferer`/`TextRefiner`/extractor；真实 LLM 只在 `examples/07_importflow`。
- E2E：小 dump → `AutoImport`(fakes) → 断言 `SearchTextOnly` 命中 + KG 三元组可查。

## 文档同步

实现完成后更新 `README.md`、`README_CN.md`、`SKILL.md` 的工作流层小节，加入 importflow。
