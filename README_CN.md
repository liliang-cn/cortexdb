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

## Ontology（本体）

CortexDB 在同一个文件里建模 Palantir 风格的 ontology：带强制主键的类型化 object type、每侧独立基数的 link type、用于多态检索的 interface、可组合的 object set 代数，以及通过 action type 完成的受治理写入。完整可运行示例见 [`examples/16_ontology`](examples/16_ontology)。

```go
_, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{
    Schema: cortexdb.OntologySchema{
        SchemaID: "aviation",
        InterfaceTypes: []cortexdb.OntologyInterfaceType{{APIName: "Facility"}},
        ObjectTypes: []cortexdb.OntologyObjectType{{
            APIName:       "Airport",
            PrimaryKey:    "iataCode",     // 必填：对象的身份就来自它
            TitleProperty: "facilityName",
            Implements:    []string{"Facility"},
            Properties: []cortexdb.OntologyProperty{
                {APIName: "iataCode", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
            },
        }},
    },
    Activate: true,
})
```

同一时刻只有一份 schema 处于 **active**，它会校验每一次写入：未声明的 object type、未声明的属性、缺失的必填值、解析不了的值都会被拒绝。在它之下写入的节点身份是 `entity:<objectType>:<primaryKey>`；没有 active schema 时仍沿用旧的按名字推导的 ID。

**Object type** 带 `api_name`、`display_name`、`plural_display_name`、`description`、`status`、`visibility`、`primary_key`（必填）、`title_property`、`implements` 和类型化的 `properties`。数据类型：string、integer、long、double、decimal、boolean、date、timestamp、geopoint、geoshape、vector、array、struct、marking。属性可标记 `searchable`（进 FTS5）或 `vectorized`。`shared_properties` 让一个定义写一次、在多个 object type 与 interface 里按名字复用。

**Link type** 是双向的，两侧各有自己的 `api_name` 和 `ONE`/`MANY` 基数。一对多 = 一侧 `ONE` + 一侧 `MANY`；只有 `ONE` 侧可以声明 `foreign_key_property`。

**Interface** 提供多态：对 `Facility` 做 object set 或 `find_nodes` 查询会返回所有实现它的 object type。interface 可以多继承，object type 可以实现多个 interface，继承成环在保存时就被拒绝。

**Object set** 组合检索——向量检索、全文检索与图遍历在同一个表达式里是同级算子，而不是三套 API：

```go
resolved, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
    ObjectSet: cortexdb.ObjectSet{
        Kind:     cortexdb.ObjectSetIntersect,
        Operands: []cortexdb.ObjectSet{largeFacilities, airportsNearLondon},
    },
})
```

九种 kind：`base`、`interface_base`、`static`、`reference`（schema 上保存的具名集合）、`filter`、`search_around`、`union`、`intersect`、`subtract`。过滤谓词：`eq`、`lt`、`lte`、`gt`、`gte`、`in`、`is_null`、`contains`、`starts_with`、`contains_all_terms`、`contains_any_term`、`nearest_neighbors`，以及 `and`/`or`/`not`。`search_around` 最多串联 3 跳，与 Foundry 的限制一致。

**Action type** 是受治理、可审计的写入：类型化参数、编辑规则（`create_object`、`modify_object`、`create_or_modify_object`、`delete_object`、`create_link`、`delete_link`）和提交条件。`validate_only` 只校验参数和提交条件、不写入；`return_edits` 返回本次产生的图编辑——两者互斥。校验从不读图，所以它不会报主键冲突。每次成功执行都会写审计记录。schema 上设 `strict_actions: true` 会关掉通用 upsert 工具，让 action 成为唯一写入路径。

**类型化工具** 把 schema 变成 agent 可调用的界面——每个 action type 一个工具，可选每个 object type 一个列表工具，参数是真正的 JSON Schema 类型而不是一团自由文本：

```go
tools, err := db.GenerateOntologyTools(ctx, cortexdb.OntologyToolGenOptions{IncludeObjectTypes: true})
```

结果有数量上限（默认 32），并且**刻意不**注册进 `NewMCPServer`。OSDK 1.x 的生成代码随 ontology 线性膨胀；在这里同样的膨胀会落到 agent 每一次请求的 context window 上，所以是否暴露由调用方显式决定。

**Schema diff** 在应用新版本之前回答"它会让哪些已有数据失效"：

```go
diff, err := db.DiffOntologySchema(ctx, cortexdb.OntologyDiffRequest{SchemaID: "aviation", Candidate: candidate})
```

破坏性变更：删除 object type 或 link type、删除属性、属性数据类型改变、属性由可选变必填、新增必填属性、主键变更、link 一侧改指向、基数由 `MANY` 收紧为 `ONE`。非破坏性的新增与放宽也会被报告，并标记为安全。两侧都会先展开 shared property，所以改共享属性的类型是可见的。

工具：`ontology_save`、`ontology_get`、`ontology_list`、`ontology_delete`、`ontology_diff`、`ontology_action_list`、`ontology_action_apply`、`object_set_resolve`。

### 当前限制

- **`vectorized` 目前只是声明。** 这个标记会被存储和校验，但没有任何写入路径会去 embedding 这些属性。无论是否配置了 embedder，`upsert_entities` 都往节点里写一个词法 FNV 哈希向量，所以用**文本**查询做 `nearest_neighbors` 是在两个不同的向量空间之间比较。今天只有显式传入查询 `vector` 时，object set 的向量谓词才有意义。
- **active ontology 会约束 `SaveKnowledge`。** 它总会跑内置的启发式抽取器，产出的实体是无类型的，会被写入校验拒绝。两者都要用的话，在 schema 里声明一个兜底的 `entity` object type（主键 `name`）和一个 `related_to` link type。
- **`modify_object` 不会改写节点的显示标题。** 通过 modify 规则改 title 属性只更新属性本身，存储的标题不变，所以按名字解析端点仍会命中改名前的名字。
- **刻意不建模：** Foundry 的 function runtime、branch/proposal、动态行级安全、backing datasource。那些需要的是 CortexDB 无意成为的那种平台。

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

### 共享大脑 —— 一个 CortexDB，多个 agent 和机器

默认每个 agent 各开一个 SQLite 文件。改成都指向同一个 `cortexdb-grpc`，
Claude Code、Codex、OpenClaw 以及其他 VM 里的 agent 就读写**同一份**记忆和知识图谱。

在持有数据库的那台机器上：

```bash
CORTEXDB_PATH=$HOME/.cortexdb/cortexdb.db \
CORTEXDB_GRPC_ADDR=10.0.0.5:47821 \
CORTEXDB_GRPC_TOKEN=<token> cortexdb-grpc
```

在每个客户端上：

```bash
export CORTEXDB_REMOTE="10.0.0.5:47821"
export CORTEXDB_GRPC_TOKEN="<同一个 token>"
```

改动就这些。MCP 服务器随后不再打开本地数据库：它在启动时向服务端查询工具清单并
转发每次调用，所以**现有的和将来新增的工具都自动可用**。`UserPromptSubmit`
自动召回 hook 走同一个远端，因此注入的记忆和工具写入的是同一个大脑；
`--memory-html` 和 `--export-memory` 同样如此。

传输是明文的，这是设计使然 —— 只能跑在环回、可信 LAN 或 Tailscale 上。
**token 就是全部的访问控制**：拿到它就有完整读写权。embedder 和 LLM 配置在
服务端，不在客户端。`--graph-html` 同样读共享大脑；其余一次性模式
（`--export-memory`、`--learn-path`）仍作用于本地数据库。

图谱视图也是一个 MCP 工具 `render_graph_html`。它是唯一**不**代理到共享大脑的
工具：图谱数据从远端读，但 HTML 在 MCP 服务所在的机器上渲染和落盘 —— 调用方需要
文件在自己的文件系统上才能打开或作为附件发出，服务端渲染会把文件留在大脑主机上，
请求它的一方根本够不到。用 `CORTEXDB_VIEW_DIR` 指定输出目录。

## OpenClaw 与 Hermes 原生记忆插件

CortexDB 还提供两个会进入宿主记忆生命周期的原生适配器。它们都复用现有
gRPC sidecar 与统一的 `knowledge_memory_recall`，不会建立平行的存储路径。

- OpenClaw：[`liliang-cn/openclaw-cortexdb-memory`](https://github.com/liliang-cn/openclaw-cortexdb-memory)
  注册独占的 `memory` capability，以及召回、保存、删除工具。
- Hermes Agent：[`liliang-cn/hermes-cortexdb-memory`](https://github.com/liliang-cn/hermes-cortexdb-memory)
  注册 `MemoryProvider`，自动执行每轮前召回与完成轮次同步。

```bash
# OpenClaw
openclaw plugins install npm:cortexdb-openclaw-memory@2.57.1
openclaw config set plugins.slots.memory cortexdb-memory
openclaw gateway restart

# Hermes Agent
hermes plugins install liliang-cn/hermes-cortexdb-memory --enable
hermes config set memory.provider cortexdb
hermes gateway restart
```

先运行 `cortexdb-grpc`，再按各插件 README 安装。现有 [`skills/`](skills/)
仍适合提供显式工具指引与 helper，但 skill 本身不会替换宿主的原生记忆后端。

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

`examples/01_core` … `16_ontology` 短小、面向架构（`go run ./examples/01_core`）；01-07/09/15/16 可独立运行，其余需要 LLM/embeddings/线上 DB——见 [examples/README.md](examples/README.md)。

一个可嵌入的 local-first AI memory/KG 库——并非 Fuseki/GraphDB/Stardog 这类完整图数据库产品的替代品。一个文件、Go API、tool/MCP 接口，以及足够构建实用记忆工作流的 RDF/SPARQL/RDFS/SHACL 能力。
