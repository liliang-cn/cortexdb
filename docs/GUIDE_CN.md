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

## 功能一览

- **KnowledgeMemory 大脑门面** — `Recall` / `Remember` / `Reflect` / `Consolidate` / `PromoteToKnowledge` / 上下文包;融合检索横跨情景记忆、持久知识与 GraphRAG 分块;关系型问题以**图边事实**回答(`Alice —uses→ Apollo`),无 embedder 也可靠;确定性(不调 LLM)的 `extract_conversation`;记忆可内联携带实体/关系,存储与入图一次完成。
- **可组合检索** — `cortex_query`:vector / lexical / hybrid / graph 四条预取通道,RRF、加权 RRF 或 DBSF 融合,带元数据过滤与逐源打分调试;`Authorize` 回调在检索层对每个候选做 RBAC/ABAC 门禁;可插拔重排器。
- **向量 + 词法引擎** — FTS5、HNSW / IVF / Flat 索引、标量与乘积量化，以及 `IndexTypeBinary`：1 bit 编码（内存只占 1/32），按汉明距离粗筛，再用原始精度对候选精确重排——真实 768 维向量上，默认 8 倍过采样时 recall@10 为 0.979–0.996，10 万条向量时比精确扫描快约 110 倍；PostgreSQL 上用 pgvector 的 `binary_quantize`。地理索引、语义查询路由。
- **外部召回通道** — 你已经在跑的搜索集群（Meilisearch、Weaviate…）可以通过 `QuerySource` 成为参与融合的一条 lane，而不必成为存储：它只点名候选 id，内容仍归大脑所有，过期的 id 会被丢弃而不是被编造成结果。
- **知识契约** — 每条记录都能回答*我怎么知道的*和*我多确定*：`_source`、`_chunk`、`_producer`，以及一个闭集里的 `_grade` —— `verified`（有名有姓的人留下的）、`self_consistent`（从已陈述的东西推导出来的）、`asserted`（模型或人说的，没人核过）、`held`、`refused`（被词汇表拒绝，附原因）。`contract_tally` 回答整个书架站在什么之上，未标注的行也算进去；`contract_needs_attention` 列出需要人看的 —— 包括实体消解在"两个名字可能是同一个实体"时写下的 `possiblySame` 链接，而不是凭猜测直接合并；`fact_provenance` 引出事实所来自的原文；`verify_claims` 拿 (主语, 关系, 宾语) 三元组对照图谱逐条判定 —— supported、contradicted（单值关系上当前是另一个值、有效期已结束、或有 `_contradicts` 记录否定它）或 absent —— 每条判定都附来源，全程不调模型。生产者写入前调 `ValidateContract`。[alchemy](https://github.com/liliang-cn/alchemy) 往这里入库的每张图都带契约 —— 它六个 sink 里唯一这么做的。
- **可换存储后端** — 默认 SQLite；把 DSN 换成 `postgres://` 就把同一个大脑搬到 **PostgreSQL + pgvector**，向量、混合检索、记忆和 RDF 图谱在两个后端上都跑。注册表是编译期的，不是插件系统（存储是热路径）。104 个 opt-in 的 PostgreSQL 测试，大多是 parity 测试：一份测试体、两个数据库、必须给出相同答案。
- **知识图谱** — 同一文件上的 RDF 三元组/四元组,**属性图也能当 RDF 读**:抽取和 `upsert_entities` 写进去的一切,都以只读三元组的形式参与 SPARQL、推理和 SHACL(`cxn:` 节点、`cxt:` 类型、`cxr:` 关系、`cxp:` 属性),不复制数据,也就没有同步问题。**RDF 1.2**:三元组项 `<<( s p o )>>`、具名化三元组和 `{| |}` 注解,于是"关于一条事实的事实"(谁说的、多大把握)本身也能查询;支持带书写方向的字面量。SPARQL 1.1 和 1.2——查询、带图管理(`ADD`/`COPY`/`MOVE`/`CLEAR`/`DROP`)的更新、全部属性路径、JSON/XML/CSV/TSV 结果格式,以及通过应用设置的处理器执行的 `SERVICE`——在两种后端上通过 W3C 官方测试:RDF 1.2 与 RDF/XML 全部通过,SPARQL 1.2 269/269,SPARQL 1.1 查询 329/331(两条是有意的宽松处理:`"1" + "2"` 得 3)、更新 157/157、结果格式和联邦查询全部通过。半朴素求值的物化推理,覆盖 RDFS 和 OWL 2 RL 规则——`inverseOf`、对称、传递、`equivalentClass`/`equivalentProperty`、`sameAs`、**键**(`FunctionalProperty` / `InverseFunctionalProperty` / `hasKey` 推出 `sameAs`:邮箱相同就是同一个人)、**属性链**,以及类表达式(`someValuesFrom`、`allValuesFrom`、`hasValue`、`intersectionOf`、`unionOf`、`oneOf`、最大基数)——事实变化时**自动**保持最新(删除再重推,异步执行,`WaitForInference` 可读到自己刚写入的结果),每条推理结果都能解释来源。矛盾(`disjointWith` 与 `AllDisjointClasses`、不相交属性、函数型属性出现两个值、`differentFrom` 与 `AllDifferent` 和推出的 `sameAs` 冲突、非自反和非对称属性、否定属性断言、`complementOf`、`owl:Nothing`、基数为 0)会连同冲突的三元组一起报告,绝不靠猜来消解。**完整的 SHACL Core**——全部属性路径、限定值形状、`sh:lessThan`、隐式类目标;W3C Core 测试 98/98——以及 **SHACL-SPARQL**(`sh:sparql` 约束和基于 SPARQL 的约束组件,支持预绑定;22 条通过 21 条,规范中可选的 `$shapesGraph` 除外),还有 **SHACL-AF 规则**(`sh:TripleRule`,支持条件、顺序和节点表达式,迭代到不动点,结果可解释)。属性图上的**只读 Cypher / GQL**(`graph_cypher_query`:MATCH / OPTIONAL MATCH / WITH / 最多 6 跳的变长路径 / 聚合;3,897 个 openCypher TCK 场景 0 错答——做不到的直接拒绝)。N-Triples/N-Quads/Turtle/TriG/JSON-LD 读写和 RDF/XML 导入(大多数 OWL 本体以此发布),JSON-LD 绝不拉取远程 context;属性图侧 `apply_inference` 物化两跳关系组合并带出处;实体记录断言文档,`delete_document_graph` 是按摄入形状做的删除。
- **Ontology(Palantir 风格)** — 带主键与基数的对象/链接/接口类型,对象集代数(union / intersect / filter / `search_around`),带审计的受治理**动作类型**,自动生成的类型化 agent 工具,以及破坏性变更的 schema diff;`strict` 与 `vocabulary` 两种执行模式。
- **流水线** — `memoryflow`(转录 → 召回 → 唤醒 → 晋升)、`graphflow`(语料 → 图 → HTML 报告)、`importflow`(CSV / SQL dump / 在线 Postgres-MySQL → RAG + KG)、`connector`(PII 脱敏、签名计划、可逆保险库、CDC 同步)。
- **工具与 MCP** — 80+ 工具,进程内与 MCP 同名同义,另有交互式图谱视图 `render_graph_html`,实时 3D 视图 `serve_graph_3d`(可在全库查找、提问、用 Cypher 查询、逐个节点展开邻居;只读、适配手机,每个视角都是可分享的链接:`?focus=`、`?ask=`、`?cypher=`),以及会分页的批量列举工具 `memory_list_all`、`graph_list_all`——`graph_list_all` 默认只给连接最密的核心子图,传 `order: "id"` 才会逐页走完整张图,像 `memory_list_all` 一样返回 `next_cursor`。
- **对整个库发问** — 检索回答"什么与这个查询相关"，这几个回答"里面有什么"。*范围*检索返回距离查询一定距离内的全部、以外的一个不要，当 top-K 只是在编造一个 K 时这才是诚实的答案（`search_vector_range`，而且响应会说明是否有匹配被上限挡住）。`Aggregate` 按元数据字段计数、求和、分组（`aggregate_metadata`）。`VectorAggregate` 归约向量本身 —— 质心、几何中位数，以及 **medoid**：组内距离其余成员最近的那条**真实记录**，它用算术回答"这批近重复里哪条是正本""这一簇到底在讲什么"，全程不需要模型（`representative_records`）。两个后端行为一致，每种行为一份测试体、两边都跑。
- **图自己描述自己** — `graph_schema` 报告**观察到的** schema（有哪些节点类型和边类型、每种边实际连接哪对类型、每种节点带哪些属性键），`graph_property_values` 报告某个键实际取哪些值——因为照着键名写的过滤（`color == "black"` 而库里存的是 `BLK`）返回空，而空看起来像个事实。声明的本体是可选的，恰恰在"没人知道形状"的那些图上不存在；这个是从行里量出来的，还附带一份**可以直接贴进 prompt** 的文本形式。同组还有 `rank_graph_nodes`（这个大脑在结构上到底关于什么；结果带计算时间缓存，只在图变了之后才重算）、`graph_statistics`（连通分量远大于 1 说明实体写进去了却从没被连上）、`graph_health`（按 producer 的增长突增、度数长尾里的枢纽节点、每天的 supersede 次数、单值关系同时存在多个值）和 `predict_graph_edges`（缺的事实，或同一实体存了两份）。
- **整库问题** — `global_search` 把 GraphRAG 全局检索做成调用方显式选择的工具：`build_community_hierarchy` 用 Leiden 保留每一层社区（每一层的每个社区都内部连通，Louvain 保证不了这一点），自底向上写社区报告；`global_search` 在指定 `level` 的报告上做 map-reduce。有模型时由模型写报告和答案，没有模型时报告按确定性规则生成，按词面相关度排序后原样返回，不做综合。
- **沿着图走的检索** — `retrieval_mode: "ppr"` 从问题点名的实体出发做个性化 PageRank（HippoRAG 2 的做法），再和词法或混合检索的第一阶段结果按排名融合。没有 embedder 时，问题点名了足够具体的实体（不是大多数段落都提到的那种，比如对话里的说话人），`auto` 就走这条路，否则保持词法检索。不管有没有 embedder，`SaveKnowledge` 都会建好这张实体图；读起来像名字的标题会关联到文档的每个分块。经 `SaveKnowledge`/`SearchKnowledge` 实测（`cortexdb-bench`，无 embedder），recall@5 词法 → `auto`：2WikiMultiHopQA 0.657 → 0.807，MuSiQue 0.457 → 0.542，LoCoMo 0.493 → 0.495，LongMemEval 0.861 → 0.862；p95 低于 30 ms。`graph` 模式按排名融合词法、实体关联和扩展出来的片段，精确的词法命中不会被图上的邻居挤下去。
- **变更事件流** — 对节点、边、三元组、记忆、知识和本体 schema 的每一次已提交写入，都在同一个事务里追加到 `change_log`：按提交顺序、恰好一次，回滚的事务不留任何记录。可以按游标读取（`changes_since`、`db.Changes`），也可以在进程内订阅；默认保留 7 天或 50 万条。推理结果就是靠它保持最新的，触发器和同步也可以建在它上面。
- **拿图给名字消歧** — `disambiguate_mentions` 用同句出现的其它名字来解析一个有歧义的名字：在候选之间找最短路径，每条渲染成一句话并带上 `edge_ids`。确定性的前四步归库，第五步"选哪个"归调用方——所以完全不需要模型也能用，而且任何时候都能说出为什么。连不上任何东西的提及标记为未解析，绝不退化成最接近的字符串。
- **多跳问题的路径检索** — `search_paths` 在问题点名的实体之间遍历图，返回把它们连起来的事实链，按长度和关系类型打分，每条边都带着它出自的 chunk；`return_paths` 把这些链加进一次 GraphRAG 查询。`relation_policies`（按关系类型设权重和最大深度）可用于 `search_paths`、`expand_graph` 和 `HybridSearch`，让 `co_occurs_with` 不再和 `works_at` 算一样重。在一个带"长得像但错误"干扰项的 10 题多跳集上，5 个 chunk 预算下完整证据命中率从 0.50（chunk 检索）升到 0.80，干扰项占比从 0.36–0.43 降到 0.20。
- **命中不再是半句话** — `chunk_window` 把命中块的邻块作为**上下文**带回来，明确标注、绝不当成命中。它治的是分块必然带来的那个毛病：一次真实检索返回的命中块第一个词是 `ance.`——*importance* 被切在块边界上。
- **质量是测出来的** — `pkg/eval` 用标注查询集走真实检索路径,recall@k / nDCG 回归下限进 CI;FTS5 / SPARQL / SQL-dump 解析器有 fuzz 测试。

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
pkg/cortexdb   主 facade：向量、text/RAG 检索、knowledge、memory、KG、ontology、tools、MCP。  ← 从这里开始
               KnowledgeMemory 架在它之上：Recall / Remember / Reflect / Consolidate / context pack。
pkg/memoryflow Agent memory workflow：transcript ingest、recall、wake-up layers、promotion。
pkg/graphflow  语料 → 抽取 → build → analyze → report → export（HTML）。
pkg/importflow 导入 CSV / SQL dump / 线上 Postgres-MySQL 到 RAG + KG（DDL → 图谱）。
pkg/connector  importflow 之上的隐私闸门：PII 脱敏、人工签字方案、可逆金库、CDC 同步。
pkg/graph      底层 RDF/SPARQL/RDFS/SHACL + property graph。
pkg/core       存储引擎（默认 SQLite，按 DSN 可换 PostgreSQL + pgvector）、embeddings、FTS5、
               向量索引（HNSW/IVF/Flat）。
```

配套包：`pkg/eval`（检索质量评测 harness）、`pkg/rpcserver`（`cmd/cortexdb-grpc` 背后的 gRPC facade）、`pkg/agentmem` + `pkg/hindsight`（独立的 SQL agent memory bank，带 disposition 加权反思）、`pkg/semantic-router`（基于 embedding 的查询路由）、`pkg/quantization`（标量/二值向量压缩）、`pkg/geo`（地理空间索引）。

## 存储后端 —— SQLite 或 PostgreSQL

SQLite 是默认，也是这个库存在的理由：一个文件，不用起服务。当"一个文件"不再是
合适的形态时 —— 多台应用服务器共享一个大脑，或者数据库归运维统一备份和复制 ——
同一个大脑可以跑在 PostgreSQL + pgvector 上。**换后端只有 DSN 这一件事**：

```go
db, _ := cortexdb.Open(cortexdb.DefaultConfig("/var/lib/cortexdb/brain.db"))          // SQLite
db, _ := cortexdb.Open(cortexdb.DefaultConfig("postgres://u:pw@host:5432/cortex"))    // PostgreSQL + pgvector
```

裸路径一直表示 SQLite 文件，现在依然如此 —— 已有配置不用知道这些改动也照常工作。
store 之上的一切都没变：向量、混合检索、记忆、RDF 图谱、SPARQL、ontology 和工具面
在两个后端上都跑，`PostgresStore` 满足与 SQLite 版本相同的 `BrainStore` 契约。

注册表（`core.RegisterStore`）**刻意是编译期的，不是插件系统**。存储是热路径 ——
每次召回都经过它 —— 进程边界意味着每次搜索多一次 IPC 往返和序列化，而且事务无从谈起。
它和 agent-go 的 `RegisterMemoryStore` 是同一个形状：已经会在那边换记忆后端的人，
在这里不用学任何新东西，变的只有 DSN。

### 真正的差异

三处，而且都写在代码里，不是脚注：

| 查询 | SQLite | PostgreSQL |
| --- | --- | --- |
| 非 CJK 全文 | FTS5 `unicode61` MATCH | `tsvector @@ plainto_tsquery('simple')` |
| CJK，3 字及以上 | FTS5 trigram 伴随表 | `LIKE`，由 `pg_trgm` 加速 |
| CJK，1–2 字 | `LIKE`，无索引 | `LIKE`，无索引 |

最后一行不是 PostgreSQL 的短板 —— 两个字的词在任何一边都产生不出 trigram。同样的
弱点落在同样的位置，这才使它可预期。用 `'simple'` 而不是 `'english'` 是有意的：
`unicode61` 不做词干还原也不去停用词，一个偷偷做词干化的后端会让同一个查询
因为跑在哪里而返回不同的行。

- **pgvector 索引不了 2000 维以上。** 4096 维的模型依然可用、结果依然正确 ——
  只是退化成精确扫描，随表线性增长。store 会在日志里说明这件事，而不是让你从
  延迟曲线里推断。
- **pgvector 是可选的。** CortexDB 连接用的账号未必有 `CREATE EXTENSION` 权限。
  扩展缺失时，图谱的向量检索降级回原本就有的 Go 内扫描并提示一次，而不是拒绝启动。

### 用验证代替相信

PostgreSQL 覆盖是 opt-in 的，而且**跳过时会大声说** —— 一次全绿永远不会被误当成
它并不具备的覆盖：

```bash
docker run -d --name cortexpg -e POSTGRES_PASSWORD=cortex -e POSTGRES_DB=cortex \
  -p 127.0.0.1:43516:5432 pgvector/pgvector:pg16
CORTEXDB_TEST_POSTGRES="postgres://postgres:cortex@127.0.0.1:43516/cortex?sslmode=disable" \
  go test ./...
```

这会打开 `pkg/core`、`pkg/graph`、`pkg/cortexdb`、`pkg/agentmem` 里的
**104 个 PostgreSQL 测试**；不设这个变量，同一次运行会打印 59 处显式 skip，
并指名哪些没有被覆盖。其中大部分是 parity 测试：一份测试体，在两个数据库上各跑一遍，
断言它们给出相同答案。这才是要紧的那道闸 —— 因为这里的失效方式是静默的：
可移植的 SQL 照样能解析、照样返回行，只是不再返回对的行。

### 什么不算存储后端

Neo4j、Qdrant 之类不在这个名单上，不是因为没顾上。这里的图就是两张表 ——
`graph_nodes` 和 `graph_edges` —— 和向量、分块、agent 记忆在同一个数据库里，
共用一个事务边界。SPARQL、RDFS 推理和 SHACL 校验都实现在这套 SQL 之上。把图搬到
Neo4j 意味着：图与向量分家且两者之间没有事务；GraphRAG 的"先检索再沿边扩展"变成
两次网络往返 + 应用层拼接；现有的 SPARQL/RDFS/SHACL 实现整套作废，改用 Cypher 重写。
`pkg/sqldialect` 在这里也帮不上忙：它只处理四件真实差异（占位符重绑定、
BLOB→BYTEA、"duplicate column" 错误文本、JSON 取值），SQL 文本原地保留 ——
它是方言适配，不是查询构造器，够不到 Cypher。

如果想用图数据库的查询语言处理这些数据，就导出过去 —— `knowledge_graph_export`
输出 N-Triples/Turtle/TriG —— 并让 CortexDB 继续做事实来源。

### 真正合适的接口：外部召回通道

你已经在跑的搜索引擎 —— Meilisearch、Weaviate，任何能给文本排序的东西 ——
当不了大脑，但确实有这个大脑没有的召回能力。这和"存储"是两个问题，它有自己的接口：

```go
type QuerySource interface {
    Name() string
    Search(ctx context.Context, req QuerySourceRequest) ([]QuerySourceHit, error)
}

db, _ := cortexdb.Open(cfg, cortexdb.WithQuerySource(mySource))
resp, _ := db.Query(ctx, cortexdb.QueryRequest{
    Query: "rollbak",
    Prefetch: []cortexdb.QueryPrefetch{
        {Name: "lexical", Kind: cortexdb.QueryPrefetchLexical, Query: "rollbak"},
        {Kind: cortexdb.QueryPrefetchSource, Source: "meilisearch"},
    },
})
```

source 返回的是 **id 和分数**。内容、元数据、collection、doc id 全部来自大脑本身。
由此推出三件事，每一件都有对应的测试：

- source **无法**注入 CortexDB 从未存过的文本。
- 大脑里没有的 id 会被丢弃，而不是凭空造成一条结果。外部索引过期是常态，不是异常。
- 通道给出的候选照样过请求的 filter，照样走和内建 lane 相同的融合，并在
  `source_scores` 里带上通道名。

通道报错会让整个查询失败，而不是悄悄让结果集变少 —— 一条静默消失的召回路径
会在不声不响之间改变"搜索"的含义。

`examples/17_query_source` 用**纯 `net/http`** 对接了真实的 Meilisearch —— 不往
`pkg/` 里引任何 SDK，和把 LLM 客户端挡在外面是同一条规矩。它也给出了这么做的
诚实理由：一个拼错的词 `rollbak`，FTS5 正确地匹配不到，而带容错的引擎能匹配到。

## KnowledgeMemory —— 大脑 facade

`db.KnowledgeMemory()` 是最高层 API：一次调用融合 episodic memory、durable knowledge 和知识图谱，返回一个可直接粘贴、带来源归属的 context pack。

```go
brain := db.KnowledgeMemory()
_, _ = brain.Remember(ctx, cortexdb.KnowledgeMemoryRememberRequest{
    Content: "Alice prefers tabs over spaces.", Scope: "user"})
rec, _ := brain.Recall(ctx, cortexdb.KnowledgeMemoryRecallRequest{
    Query: "what does Alice prefer?", EntityNames: []string{"Alice"}})
fmt.Println(rec.ContextPack.Text) // sections + memory/knowledge/chunk ID + entities
```

- **`Recall` / `BuildContextPack`** —— 跨 memory、knowledge 与 GraphRAG chunk 的融合检索。关系型问题的答案以 **graph facts**（`Alice —uses→ Apollo`）形式返回，读的是图边而不是词法 chunk 匹配，所以"谁在用 X"这类问题即使没有 embedder 也能可靠回答。请求接受结构化检索计划：`keywords`、`alternate_queries`、`entity_names`、`retrieval_mode`。
- **`Reflect` / `Consolidate`** —— 对一次 recall 做反思（可插拔 `KnowledgeMemoryReflector`，默认确定性实现），并把摘要写回为一条整合后的 memory。
- **`PromoteToKnowledge`** —— 把 episodic memory 提升为持久、分块的 knowledge。
- **`ExpandEntityContext` / `Neighbors` / `ShortestPath`** —— 围绕实体的图谱探索。
- **`extract_conversation`** —— 确定性（无 LLM）地从会话文本或已存 session 中抽取实体、共现关系和摘要；`persist` 会写入 KG 与 durable knowledge。

`MemorySaveRequest` 可以内联携带 `entities`/`relations`，agent 写入的 memory 在存储的同一次调用里就落进图谱。以上全部也以 `knowledge_memory_*` MCP 工具形式暴露。

## 可组合检索

`db.Query`（工具名 `cortex_query`）是一次通用检索调用：具名 **prefetch lane**（`vector`、`lexical`、`hybrid`、`graph`），用 **RRF / 加权 RRF / DBSF** 融合，支持 metadata filter、可选打分公式，以及按来源的 rank/score 调试输出。

再往下，text search 接受 `Authorize` 回调——检索层的安全闸门（对每个候选做 RBAC/ABAC 判定；检索会不断放宽召回，直到能返回 TopK 条**已授权**结果）——以及可插拔的 `Reranker`，完成 recall→precision 的第二阶段。

## 知识契约 —— 一条记录怎么说明它凭什么

一个被多个生产者共用的大脑 —— 抽取管线、agent 的记忆工具、schema 导入 ——
以前各自用自己的措辞回答"我怎么知道这件事"，等于没回答。契约是一套词汇表，
写在每条记录本来就有的 metadata 里；有了它，这个库给出的答案才是可审计的，
而不只是自信的。

键全部带 `_` 前缀（`pkg/cortexdb/contract.go` 是可执行形式；规范文本在
`docs/superpowers/specs/2026-09-05-knowledge-contract-design.md`）：

| 键 | 含义 |
| --- | --- |
| `_source` | 来源 —— URL、文件、job、engagement。绝不是 DSN 或带凭据的路径：库是共享的，来源字符串谁都读得到 |
| `_chunk` | 在来源里的 chunk 序号，生产者没分块时为 `-1` |
| `_producer` | 怎么产出的：`llm-extract`、`ddl`、`tabular`、`graph-import`、`human`、`measured`、`compiled` |
| `_grade` | 它的真实性由哪一类东西确立 —— 下面的闭集 |
| `_state`、`_why` | 生产者自己对记录状态的说法，以及一次拒绝所附的原因 |
| `_by`、`_at` | 谁断言的、何时；`_producer` 为 `human` 时必填 |
| `_contradicts` | 与这条不能同时为真的记录 id，两边都写 |

五个 grade，越靠后越弱：

- **`verified`** —— 有名有姓的人复核后留下的。压过违规：复核者把投诉摆在面前
  仍然留下它，是一个决定；把它评成 `refused` 等于让词汇表推翻它被升级给的那个人。
- **`self_consistent`** —— 从已经陈述的东西（`CREATE TABLE`、既有图、表自己的行）
  确定性推导而来，没和自身之外的任何东西核对过。
- **`asserted`** —— 模型或人说的，没人核过。什么都不说的生产者默认得到它，
  因为"未经核实地到来"是犯错时安全的那个方向。
- **`held`** —— 等人处理。
- **`refused`** —— 词汇表拒绝了它，`_why` 说明是哪条规则。

生产者写入前调 `ValidateContract(meta)`；过不了契约的记录在写入时就被拒，
而不是事后在数据库浏览器里才被发现。

读回来是三个问题，各对应一个方法和一个工具：

```go
tally, _ := db.ContractTally(ctx)              // contract_tally
rows, _  := db.NeedsAttention(ctx, 20)         // contract_needs_attention
prov, _  := db.FactProvenanceFor(ctx, edgeID, true) // fact_provenance，带引文原文
```

生成的回答在转述之前可以先对照同一批记录核一遍。`VerifyClaims`（工具
`verify_claims`）接收按名字或 id 给出的 (主语, 关系, 宾语) 三元组，逐条返回
`supported`、`contradicted` 或 `absent`，附上判定所依据的边及其来源。contradicted
指：单值关系（本体里有 ONE 一侧，或调用方在 `SingleValued` 里声明）当前是另一个值、
事实的 `valid_to` 已过、或有一条当前边在 `_contradicts` 里点名否定它。确定性，不调模型。

`GraphHealth`（工具 `graph_health`）是全书架层面的对应物：按 producer 的增长（带突增检测）、
入度/出度长尾、每天的 supersede 次数（关闭的事实、被替换的版本、撤回）、时间不变式
（单值关系上区间重叠，或区间结束早于开始）。每项检查都报告自己是否报警。

`ContractTally` 节点和边一起数 —— 图的断言大多是边，只数节点会把书架报得比实际
可靠得多 —— 并在五个 grade 之外报出 `untagged`。在早于契约的书架上，或者一个
生产者写、另一个不写的书架上，这是最大的那个数字；不画它的图表描述的是 3% 的
数据，却看起来像全部。

`alchemy` 往 CortexDB 入库的每张图都写契约：按各记录的 provenance 和本体的
发现评 grade，并把复核者解决的冲突写成 `_contradicts`。它六个 sink 里只有这一个
这么做；其余五个存下图、丢掉理由。

## 知识图谱

同一文件内嵌 RDF：triples/quads、namespaces、N-Triples/N-Quads/Turtle/TriG/JSON-LD 导入导出和 RDF/XML 导入（`RDFFormatRDFXML`），SPARQL 1.1 和 1.2（SELECT/ASK/CONSTRUCT/DESCRIBE、FROM/FROM NAMED、更新语句和图管理、OPTIONAL/UNION/MINUS/VALUES/BIND/FILTER、函数库与 XSD 类型转换、聚合、子查询、全部 property path `^p p/q p|q p+ p* p? !(p|^q)`、`SERVICE`），RDFS 加 OWL 2 RL 的物化推理，以及 SHACL Core 和 SHACL-SPARQL 校验。

一致性用 W3C 官方测试衡量，SQLite 和 PostgreSQL 结果相同：RDF 1.2 语法测试（N-Triples、N-Quads、Turtle、TriG、RDF/XML）和 RDF 1.1 RDF/XML 全部通过；SPARQL 1.2 269/269；SPARQL 1.1 查询 329/331、更新 157/157、CSV/TSV/JSON 结果 10/10、联邦查询 10/10；SHACL Core 98/98，SHACL-SPARQL 21/22。两条 SPARQL 失败是有意的宽松处理——看起来像数字的普通字符串可以参与运算，因为通过非 SPARQL API 写入的数据常把数字存成字符串；SHACL 那一条是规范里可选的 `$shapesGraph`。每套测试都有一个代表性子集在 CI 中运行；设置 `CORTEXDB_W3C_TESTS`（w3c/rdf-tests 的检出）和 `CORTEXDB_SHACL_TESTS`（w3c/data-shapes 的 data-shapes-test-suite/tests）即可跑全量。

`SPARQLResult.WriteResults` 把结果写成 SPARQL JSON、XML、CSV 或 TSV。`SERVICE` 自己绝不联网：由 `GraphStore.SetSPARQLServiceHandler` 决定有哪些端点，`HTTPSPARQLService` 是可直接使用或包在处理器里的 SPARQL 1.1 Protocol 客户端；没有处理器时 `SERVICE` 报错，`SERVICE SILENT` 跳过。出于同样的原因 `LOAD` 会被拒绝——自己读取文档后用 `ImportRDF`。

属性图——抽取、`upsert_entities`、`upsert_relations` 写入的内容——也在其中。`FindTriples`（因此 SPARQL、推理、SHACL 都一样）会同时返回属性图蕴含的三元组，只读，位于图 `<urn:cortexdb:graph:property>`：节点 `X` 是 `cxn:X`，类型 `T` 是 `cxn:X a cxt:T`，类型为 `R` 的边是 `cxr:R`，属性 `k` 是 `cxp:k`，节点的 `name`（没有就用 `title`）是它的 `rdfs:label`。id 做精确的百分号编码（`entity:abc` → `cxn:entity%3Aabc`）。不复制数据，所以不会过期；`GraphStore.SetPropertyGraphProjection(false)` 可以关掉，对它的写入会被拒绝。

```sparql
SELECT ?who WHERE { ?x cxr:depends_on ?y . ?y rdfs:label "CortexDB" . ?x rdfs:label ?who }
```

推理用半朴素求值（每轮只 join 新增的部分），覆盖 `rdfs:subClassOf`、`rdfs:subPropertyOf`、`rdfs:domain`、`rdfs:range`、`owl:inverseOf`、`owl:SymmetricProperty`、`owl:TransitiveProperty`、`owl:equivalentClass`、`owl:equivalentProperty`、`owl:sameAs`。在投影上声明这些公理，能修正 LLM 抽取的常见问题：`cxt:host owl:equivalentClass cxt:Host` 统一类型的不同拼写，`cxr:depends_on owl:inverseOf cxr:depended_on_by` 回答反方向的问题，`owl:sameAs` 合并以两个名字存下的同一实体。超过上限（默认 32）的 `sameAs` 类只报告、不物化。

键（`owl:FunctionalProperty` / `owl:InverseFunctionalProperty` / `owl:hasKey`）和最大基数为 1 的限制会推出 `sameAs`，`owl:propertyChainAxiom` 推出关系链。限制（`owl:onProperty` 上的 `owl:someValuesFrom`、`owl:allValuesFrom`、`owl:hasValue`）、`owl:intersectionOf`、`owl:unionOf` 和 `owl:oneOf` 给个体分类，模式规则推出限制之间的子类关系。矛盾——`owl:disjointWith`、`owl:AllDisjointClasses`、`owl:propertyDisjointWith`、`owl:AllDisjointProperties`、函数型属性出现两个值、`owl:differentFrom` 或 `owl:AllDifferent` 与推出的 `sameAs` 冲突、非自反和非对称属性、否定属性断言、`owl:complementOf`、`owl:Nothing`、最大基数为 0——连同三元组一起报告，绝不靠猜来消解。没有实现的规则是：对每个词项都成立的废话（`x sameAs x`、每个类是自己和 `owl:Thing` 的子类）、只含 RDFS 前提且实例层结论 RDFS 已经推出的模式重述、改写谓词的 `sameAs`，以及数据类型规则。推理结果由变更事件流自动维护（删除再重推，异步执行；`db.WaitForInference` 可读到自己写入的后果；`WithAutoInference(false)` 关闭），SHACL-AF 的 `sh:TripleRule` 规则通过 `knowledge_graph_shacl_rules` 运行。

RDF 1.2 的三元组项（`<<( s p o )>>`、具名化、`{| |}` 注解）和 SPARQL 1.2 让"关于事实的事实"可以查询。`graph_cypher_query` 在属性图上回答只读的 openCypher / GQL 查询。

节点属性存为 JSON，对某个属性做筛选默认要读遍所有节点，除非给它建了索引。`GraphStore.IndexNodeProperty(ctx, "run_id")` 为单个属性建表达式索引（按键手动开启，每次写入都要付出维护代价；另有 `NodePropertyIndexes`、`DropNodePropertyIndex`）。对已索引键的等值和 `IN` 筛选——`GraphFilter.Properties`，以及 Cypher 里的 `WHERE s.run_id = $run` 或 `{run_id: $run}`——会变成索引查找，Cypher 也会像对待 `id(n) = …` 一样从它出发做连接；在 SQLite 上，数值范围（`s.latency_ms > 3000`）同样走索引。节点可以不带向量：结构性节点（agent 的执行步骤、run）不必带向量写入，也不会参与向量搜索。`examples/19_execution_graph` 把这些组合成一个 agent 的执行记录。

执行图有自己的 API。`db.StartRun` 开启一次运行（一个 `AgentRun` 节点）；`BeginStep` 在执行之前把步骤写成 running，并从它所消费输出的步骤连上 `TRIGGERED` 边；`EndStep` 用输出或错误、置信度、耗时（不填则从 begin 调用开始计算）、token 和费用重写这个步骤——事后补记一步用 `RecordStep` 一次完成——`FinishRun` 结束运行。步骤的种类（`LLMCall`、`ToolCall`、`Retrieval`、`DecisionPoint`、`Validation` 或自定义）就是节点类型，所以 Cypher 可以直接写 `(l:LLMCall)-[:TRIGGERED]->(t:ToolCall)`。`SummarizeRun` 汇总一次运行（按种类和状态计数、未结束和失败的步骤、token、费用、关键路径、置信度最低的步骤），`StepLineage` 向上游找出某个输出依赖的全部步骤，或向下游找出它影响的全部步骤，`ReplayRun` 从双时态历史中读出运行在任意时刻的状态。一个运行的步骤可以启动另一个运行（`RunStart.ParentStep`，一条 `SPAWNED` 边），用于子 agent。同样的九个操作也是 MCP 工具：`execution_run_start` … `execution_run_replay`。

检索：`retrieval_mode: "ppr"` 从问题点名的实体出发做个性化 PageRank，再与第一阶段结果按排名融合；没有 embedder 时，问题点名了实体，`auto` 就会用它。每一次已提交的写入也会追加到变更事件流（`changes_since`、`db.Changes`）。

```go
db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: triples})
res, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
    Query: `SELECT ?name WHERE { <https://example.com/alice> <https://schema.org/name> ?name }`})
```

RDFS 物化推理通过 `knowledge_graph_infer_refresh` / `_summary` / `_explain` / `_explain_match` 驱动。在 property graph 一侧，`ApplyInferenceRules`（工具名 `apply_inference`）把确定性的两跳关系组合——`A works_on B` + `B part_of C` ⇒ `A contributes_to C`——物化为带来源的可查询边。

GraphRAG 实体带有 provenance：`upsert_entities` 把每个断言过该实体的文档记录进 `source_document_ids`；`delete_document_graph` 是与 ingest 对称的删除——移除文档的 chunk/document 节点、关系边，以及只被它断言过的实体（被其他文档共享的实体只解除关联、不删除），并提供 `dry_run` 模式。

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

同一时刻只有一份 schema 处于 **active**。激活的效果取决于 schema 的 `enforcement`：

- `"strict"`（默认）校验调用方的每一次写入：未声明的 object type、未声明的属性、缺失的必填值、解析不了的值都会被拒绝。它不管库自己的簿记——内置抽取器产出的实体是无类型的，属于豁免范围，所以 active ontology 不会让 `SaveKnowledge` 变得不可用。这个豁免只认类型名，因此你自己提供的抽取器同样能写进一个仅以名字为键的裸节点。在它之下写入的节点身份是 `entity:<objectType>:<primaryKey>`；没有 active schema 时仍沿用旧的按名字推导的 ID。
- `"vocabulary"` 把 schema 当作共享词表，不阻拦写入：已声明类型的拼写会被规范化、interface 在检索时照常展开，但一个说不出主键的实体——LLM 从散文抽取实体时的常态——会回落到按名字推导的 ID 而不是被拒绝，未声明的类型和 link type 直接放行。抽取管线用这个模式；strict 会逼它们在"激活 schema"和"保住实体"之间二选一。

`strict_actions` 与 `enforcement: "vocabulary"` 互斥——一个关闭通用写入路径，另一个承诺永不关闭。

**Object type** 带 `api_name`、`display_name`、`plural_display_name`、`description`、`status`、`visibility`、`primary_key`（必填）、`title_property`、`implements` 和类型化的 `properties`。数据类型：string、integer、long、double、decimal、boolean、date、timestamp、geopoint、geoshape、vector、array、struct、marking。属性可标记 `searchable`（进 FTS5）或 `vectorized`。`shared_properties` 让一个定义写一次、在多个 object type 与 interface 里按名字复用。

**Link type** 是双向的，两侧各有自己的 `api_name` 和 `ONE`/`MANY` 基数。一对多 = 一侧 `ONE` + 一侧 `MANY`；只有 `ONE` 侧可以声明 `foreign_key_property`。

**Interface** 提供多态：对 `Facility` 做 object set 或 `find_nodes` 查询会返回所有实现它的 object type。interface 可以多继承，object type 可以实现多个 interface，继承成环在保存时就被拒绝。interface 不能与 object type 同名——名字在同一命名空间里不区分大小写地解析，`Gateway` interface 和 `Gateway` object type 会成为一次歧义查找；`SaveOntologySchema` 在保存时就拒绝这种冲突。

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

- **`vectorized` 需要 embedder 才有意义。** 配置了 embedder 时，类型声明了 vectorized 属性的对象，其节点向量就是这段文本的 embedding，`nearest_neighbors` 按语义作答。没有 embedder 时节点仍是词法哈希向量，用**文本**查询做 `nearest_neighbors` 就是在两个向量空间之间比较——这种情况要么显式传查询 `vector`，要么配置 embedder。
- **属性只能通过删除对象来移除。** upsert 只更新它点名的属性，其余保持不变——这正是"一篇只是提到某对象的文档不会把它抹掉"的原因——所以省略一个属性并不会清空它。要清就用 `delete_entities` 再写一次。ontology 改版后不再声明的属性，在输入侧会被拒绝，但改版之前写下的行上仍然留着。
- **`modify_object` 不会改写节点的显示标题。** 通过 modify 规则改 title 属性只更新属性本身，存储的标题不变，所以按名字解析端点仍会命中改名前的名字。
- **刻意不建模：** Foundry 的 function runtime、branch/proposal、动态行级安全、backing datasource。那些需要的是 CortexDB 无意成为的那种平台。

## Tools、MCP 与插件

```go
tools := db.GraphRAGTools()                             // 进程内 tool calling
server := db.NewMCPServer(cortexdb.MCPServerOptions{})  // MCP server
```

工具分组（60+ 个工具，进程内与 MCP 同名）：GraphRAG（`ingest_document`、`search_text`、`build_context`、`expand_graph`、`search_paths`、`find_nodes`、`delete_document_graph`）、统一检索（`cortex_query`）、knowledge/memory（`knowledge_save`、`memory_search` …）、KnowledgeMemory（`knowledge_memory_recall`、`_reflect`、`_consolidate`、`extract_conversation`）、KG（`knowledge_graph_query`、`_shacl_validate`、`apply_inference`）、ontology（`ontology_save`、`ontology_action_apply`、`object_set_resolve`）、维护（`vector_dimension_repair`）。MCP server 额外提供 `render_graph_html`——交互式知识图谱视图。`memoryflow`/`graphflow`/`importflow`/`connector` 各自也暴露自己的 toolbox。

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

手动跑起来足够试用；要让它常驻，[`deploy/`](../deploy/) 里有加固过的 systemd
unit、healthcheck 就是服务端二进制自己（`cortexdb-grpc -health`）的容器镜像和
compose 文件，以及配套的备份、升级和端口覆盖说明。**所有端口都有默认值，也都可以
覆盖**：`CORTEXDB_GRPC_ADDR`（或 `-addr`）改服务端口，`CORTEXDB_LIVE_PORT`
改实时图谱视图端口。

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
